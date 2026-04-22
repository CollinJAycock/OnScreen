package discovery

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// freePort returns a UDP port that's available right now. There's a tiny race
// between close and reopen but it's good enough for a unit test.
func freePort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// runListener starts a listener on a random port in a goroutine and returns
// the port + a cancel func + a counter of how many times info was called.
func runListener(t *testing.T, info ServerInfo) (int, context.CancelFunc, *atomic.Int64) {
	t.Helper()
	port := freePort(t)
	calls := &atomic.Int64{}
	infoFn := func() ServerInfo {
		calls.Add(1)
		return info
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	l := NewListener(port, infoFn, logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = l.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(7 * time.Second):
			t.Error("listener didn't stop")
		}
	})
	// Give the listener a moment to bind.
	time.Sleep(50 * time.Millisecond)
	return port, cancel, calls
}

func sendProbe(t *testing.T, port int, body string) ServerInfo {
	t.Helper()
	c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte(body)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	var got ServerInfo
	if err := json.Unmarshal(buf[:n], &got); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	return got
}

func TestDiscovery_RespondsToProbe(t *testing.T) {
	want := ServerInfo{
		ServerName: "Test Server",
		MachineID:  "machine-xyz",
		Version:    "1.0.0",
		HTTPURL:    "http://127.0.0.1:8080",
	}
	port, _, calls := runListener(t, want)

	got := sendProbe(t, port, ProbePrefix)
	if got != want {
		t.Errorf("info mismatch:\ngot  %+v\nwant %+v", got, want)
	}
	if c := calls.Load(); c != 1 {
		t.Errorf("info() calls = %d, want 1", c)
	}
}

func TestDiscovery_ProbeIsCaseInsensitive(t *testing.T) {
	want := ServerInfo{ServerName: "x"}
	port, _, _ := runListener(t, want)
	got := sendProbe(t, port, "onscreen-discovery-probe v=1")
	if got.ServerName != "x" {
		t.Errorf("expected response to lowercased probe; got %+v", got)
	}
}

func TestDiscovery_IgnoresUnrelatedPackets(t *testing.T) {
	port, _, calls := runListener(t, ServerInfo{ServerName: "x"})

	// Send something that isn't a probe — server should not respond.
	c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("hello world")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, 256)
	if _, err := c.Read(buf); err == nil {
		t.Errorf("expected no reply to unrelated packet")
	}
	if c := calls.Load(); c != 0 {
		t.Errorf("info() should not have been called; got %d", c)
	}
}

func TestDiscovery_NewListenerDefaultsPort(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	l := NewListener(0, func() ServerInfo { return ServerInfo{} }, logger)
	if l.port != DefaultPort {
		t.Errorf("default port: got %d, want %d", l.port, DefaultPort)
	}
}
