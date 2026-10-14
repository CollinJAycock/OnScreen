package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

func TestRemoteStreamPolicy_IsLocal(t *testing.T) {
	extra, err := ParseLocalNetworks([]string{" 198.51.100.0/24 ", "", "2001:db8::/32", "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	p := RemoteStreamPolicy{LocalNetworks: extra}
	cases := map[string]bool{
		"127.0.0.1":       true,
		"192.168.1.20":    true,
		"10.4.5.6":        true,
		"100.101.102.103": true, // Tailscale / CGNAT is home by default
		"fd00::1":         true,
		"198.51.100.77":   true, // admin's extra network
		"203.0.113.9":     true, // bare IP entry = single host
		"203.0.113.10":    false,
		"2001:db8::5":     true,
		"8.8.8.8":         false,
		"::ffff:8.8.8.8":  false,
		"not-an-ip":       false, // unparseable is remote: the limit fails closed
		"":                false,
	}
	for ip, want := range cases {
		if got := p.IsLocal(ip); got != want {
			t.Errorf("IsLocal(%q) = %v, want %v", ip, got, want)
		}
	}
	if _, err := ParseLocalNetworks([]string{"10.0.0.0/8", "office"}); err == nil {
		t.Error("ParseLocalNetworks accepted a non-CIDR entry")
	}
}

func TestEffectiveStreamCap(t *testing.T) {
	user := StreamCaps{BitrateKbps: 20000, RemoteBitrateKbps: 8000}
	server := RemoteStreamPolicy{MaxBitrateKbps: 4000}

	// LAN keeps only the per-user cap, whatever the internet limits are.
	if c := effectiveStreamCap(user, server, false, true); c.Kbps != 20000 || c.RemoteBinding || c.RemoteLimited {
		t.Errorf("LAN: %+v", c)
	}
	// Remote: the lowest of the three.
	if c := effectiveStreamCap(user, server, true, true); c.Kbps != 4000 || !c.RemoteBinding || !c.RemoteLimited {
		t.Errorf("remote: %+v", c)
	}
	if c := effectiveStreamCap(user, RemoteStreamPolicy{}, true, true); c.Kbps != 8000 || !c.RemoteBinding {
		t.Errorf("remote, user internet cap only: %+v", c)
	}
	// The user's own cap is lower than the internet limits: it binds.
	if c := effectiveStreamCap(StreamCaps{BitrateKbps: 3000}, server, true, true); c.Kbps != 3000 || c.RemoteBinding || !c.RemoteLimited {
		t.Errorf("remote, user cap lower: %+v", c)
	}
	// No caps anywhere: unlimited, not limited.
	if c := effectiveStreamCap(StreamCaps{}, RemoteStreamPolicy{}, true, true); c.Kbps != 0 || c.RemoteLimited {
		t.Errorf("remote, no caps: %+v", c)
	}
	// Audio never gets the internet limit.
	if c := effectiveStreamCap(StreamCaps{}, server, true, false); c.Kbps != 0 || c.RemoteLimited {
		t.Errorf("remote audio: %+v", c)
	}
}

func TestRemoteDecisionOverride(t *testing.T) {
	br := int64(25_000_000)
	file := &media.File{VideoCodec: strPtr("h264"), Bitrate: &br}
	if why, ok := remoteDecisionOverride(streamCap{Kbps: 8000, Remote: true, RemoteBinding: true, RemoteLimited: true}, file); !ok || why != ReasonRemoteBitrateLimit {
		t.Errorf("over internet limit: %q %v", why, ok)
	}
	if why, ok := remoteDecisionOverride(streamCap{Kbps: 3000, Remote: true, RemoteLimited: true}, file); !ok || why != ReasonUserBitrateLimit {
		t.Errorf("remote, user cap binds: %q %v", why, ok)
	}
	// LAN with a user cap: unchanged from before internet limits existed.
	if _, ok := remoteDecisionOverride(streamCap{Kbps: 3000}, file); ok {
		t.Error("LAN decision overridden")
	}
	if _, ok := remoteDecisionOverride(streamCap{Kbps: 40000, Remote: true, RemoteBinding: true, RemoteLimited: true}, file); ok {
		t.Error("source under the cap overridden")
	}
	if _, ok := remoteDecisionOverride(streamCap{Kbps: 8000, Remote: true, RemoteBinding: true, RemoteLimited: true}, &media.File{VideoCodec: strPtr("h264")}); ok {
		t.Error("unknown source bitrate overridden")
	}
}

type staticPolicy RemoteStreamPolicy

func (p staticPolicy) RemoteStreamPolicy(context.Context) RemoteStreamPolicy {
	return RemoteStreamPolicy(p)
}

type staticCaps StreamCaps

func (c staticCaps) GetStreamCaps(context.Context, uuid.UUID) (StreamCaps, error) {
	return StreamCaps(c), nil
}

// End to end through the decision handler: the same compatible file is a
// direct play on the LAN and a transcode (with the reason and the cap) for an
// internet client, judged by the trusted RemoteAddr.
func TestDecision_RemoteBitrateLimit(t *testing.T) {
	br := int64(25_000_000)
	file := media.File{
		VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"), Container: strPtr("mp4"),
		AudioStreams: []byte(`[{"channels":2}]`), Bitrate: &br,
	}
	const caps = "videoDecoder=h264,audioDecoder=aac,protocols=mp4"

	h := newDecisionHandler(t, file).WithRemotePolicy(staticPolicy{MaxBitrateKbps: 8000})

	lan := decisionRequest(t, caps, true)
	lan.RemoteAddr = "192.168.1.50:5000"
	rec := httptest.NewRecorder()
	h.Decision(rec, lan)
	if !bodyHas(rec, `"decision":"directPlay"`) || bodyHas(rec, `"reason"`) {
		t.Errorf("LAN: want plain directPlay; body: %s", rec.Body.String())
	}

	remote := decisionRequest(t, caps, true)
	remote.RemoteAddr = "203.0.113.20:5000"
	rec = httptest.NewRecorder()
	h.Decision(rec, remote)
	if !bodyHas(rec, `"decision":"transcode"`) || !bodyHas(rec, `"reason":"remote_bitrate_limit"`) ||
		!bodyHas(rec, `"max_bitrate_kbps":8000`) {
		t.Errorf("remote: want transcode + remote_bitrate_limit + cap; body: %s", rec.Body.String())
	}

	// The admin's extra home network makes the same address local.
	nets, _ := ParseLocalNetworks([]string{"203.0.113.0/24"})
	h2 := newDecisionHandler(t, file).WithRemotePolicy(staticPolicy{MaxBitrateKbps: 8000, LocalNetworks: nets})
	rec = httptest.NewRecorder()
	h2.Decision(rec, remote)
	if !bodyHas(rec, `"decision":"directPlay"`) {
		t.Errorf("extra home network: want directPlay; body: %s", rec.Body.String())
	}

	// A per-user internet cap applies without any server-wide limit.
	h3 := newDecisionHandler(t, file).WithUserCaps(staticCaps{RemoteBitrateKbps: 10000})
	rec = httptest.NewRecorder()
	h3.Decision(rec, remote)
	if !bodyHas(rec, `"reason":"remote_bitrate_limit"`) || !bodyHas(rec, `"max_bitrate_kbps":10000`) {
		t.Errorf("per-user internet cap: body: %s", rec.Body.String())
	}
}

// The internet cap is optional in the PUT body: omitted keeps the stored value
// (an older caller can't clear it), explicit null clears it, a value sets it.
func TestSetStreamingLimits_RemoteCapPresence(t *testing.T) {
	targetID := uuid.New()
	put := func(body string) *mockUserDB {
		db := &mockUserDB{}
		h := NewUserHandler(&mockUserService{}).WithDB(db)
		req := httptest.NewRequest("PUT", "/api/v1/users/"+targetID.String()+"/streaming-limits", strings.NewReader(body))
		req = adminAuthedRequest(req, uuid.New())
		req = withChiParam(req, "id", targetID.String())
		rec := httptest.NewRecorder()
		h.SetStreamingLimits(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s: status %d (%s)", body, rec.Code, rec.Body.String())
		}
		if len(db.streamCaps) != 1 {
			t.Fatalf("%s: %d updates", body, len(db.streamCaps))
		}
		return db
	}

	got := put(`{"max_concurrent_streams":2,"max_stream_bitrate_kbps":null}`).streamCaps[0]
	if got.SetRemote {
		t.Error("omitted internet cap was written")
	}
	got = put(`{"max_concurrent_streams":null,"max_stream_bitrate_kbps":null,"max_remote_stream_bitrate_kbps":null}`).streamCaps[0]
	if !got.SetRemote || got.MaxRemoteStreamBitrateKbps != nil {
		t.Errorf("explicit null: %+v", got)
	}
	got = put(`{"max_concurrent_streams":null,"max_stream_bitrate_kbps":20000,"max_remote_stream_bitrate_kbps":6000}`).streamCaps[0]
	if !got.SetRemote || got.MaxRemoteStreamBitrateKbps == nil || *got.MaxRemoteStreamBitrateKbps != 6000 {
		t.Errorf("value: %+v", got)
	}

	db := &mockUserDB{}
	h := NewUserHandler(&mockUserService{}).WithDB(db)
	req := httptest.NewRequest("PUT", "/", strings.NewReader(`{"max_remote_stream_bitrate_kbps":-1}`))
	req = withChiParam(adminAuthedRequest(req, uuid.New()), "id", targetID.String())
	rec := httptest.NewRecorder()
	h.SetStreamingLimits(rec, req)
	if rec.Code != http.StatusBadRequest || len(db.streamCaps) != 0 {
		t.Errorf("negative internet cap: status %d, updates %d", rec.Code, len(db.streamCaps))
	}
}
