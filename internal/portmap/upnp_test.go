package portmap

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/huin/goupnp"
)

type fakeIGD struct {
	leases  []uint32
	deleted []uint16
	fail725 bool
}

func (f *fakeIGD) AddPortMappingCtx(_ context.Context, _ string, _ uint16, _ string, _ uint16, _ string, _ bool, _ string, lease uint32) error {
	f.leases = append(f.leases, lease)
	if f.fail725 && lease != 0 {
		return errors.New("SOAP fault: UPnPError: 725 OnlyPermanentLeasesSupported")
	}
	return nil
}

func (f *fakeIGD) DeletePortMappingCtx(_ context.Context, _ string, port uint16, _ string) error {
	f.deleted = append(f.deleted, port)
	return nil
}

func (f *fakeIGD) GetExternalIPAddressCtx(context.Context) (string, error) {
	return "203.0.113.7", nil
}

func (f *fakeIGD) GetServiceClient() *goupnp.ServiceClient { return nil }

func TestUPnP_PermanentOnlyRouterFallsBackToLeaseZero(t *testing.T) {
	igd := &fakeIGD{fail725: true}
	g := &upnpGateway{c: igd, service: "WANIPConnection:1"}
	m := Mapping{Protocol: ProtocolTCP, InternalIP: net.IPv4(192, 168, 1, 20), InternalPort: 7070, ExternalPort: 7070, Lease: time.Hour}
	granted, err := g.AddMapping(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if granted.Lease != 0 || granted.ExternalPort != 7070 {
		t.Fatalf("granted = %+v, want a permanent mapping", granted)
	}
	if len(igd.leases) != 2 || igd.leases[0] != 3600 || igd.leases[1] != 0 {
		t.Fatalf("lease requests = %v, want [3600 0]", igd.leases)
	}
	// Remembered: the renewal goes straight to lease 0.
	if _, err := g.AddMapping(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if len(igd.leases) != 3 || igd.leases[2] != 0 {
		t.Fatalf("lease requests = %v", igd.leases)
	}
	if err := g.DeleteMapping(context.Background(), m); err != nil || len(igd.deleted) != 1 || igd.deleted[0] != 7070 {
		t.Fatalf("delete: err=%v deleted=%v", err, igd.deleted)
	}
	if ip, err := g.ExternalIP(context.Background()); err != nil || !ip.Equal(net.IPv4(203, 0, 113, 7)) {
		t.Fatalf("external ip = %v, %v", ip, err)
	}
}
