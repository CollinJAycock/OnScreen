package v1

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/livetv"
)

type stubRTMPStatus struct{ live bool }

func (s stubRTMPStatus) IsLive(string) bool { return s.live }
func (s stubRTMPStatus) Addr() string       { return ":1935" }

func TestCreateBroadcast_GeneratesKeyAndIngestURL(t *testing.T) {
	svc := newMockLiveTVService()
	h := NewLiveTVHandler(svc, slog.Default()).
		WithRTMP(stubRTMPStatus{live: true}, "stream.example.com", ":1935")

	req := httptest.NewRequest("POST", "/api/v1/tv/broadcasts", bytes.NewBufferString(`{"name":"Studio Cam"}`))
	rec := httptest.NewRecorder()
	h.CreateBroadcast(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data BroadcastResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data.StreamKey) != 32 {
		t.Errorf("stream key: got %q (len %d), want 32 hex chars", env.Data.StreamKey, len(env.Data.StreamKey))
	}
	want := "rtmp://stream.example.com:1935/live/" + env.Data.StreamKey
	if env.Data.IngestURL != want {
		t.Errorf("ingest url: got %q, want %q", env.Data.IngestURL, want)
	}
	if len(svc.tuners) != 1 {
		t.Fatalf("tuners: got %d, want 1", len(svc.tuners))
	}
	for _, tn := range svc.tuners {
		if tn.Type != livetv.TunerTypeRTMP {
			t.Errorf("tuner type: got %q, want rtmp", tn.Type)
		}
	}
}

func TestCreateBroadcast_RequiresName(t *testing.T) {
	svc := newMockLiveTVService()
	h := NewLiveTVHandler(svc, slog.Default()).WithRTMP(stubRTMPStatus{}, "", ":1935")
	req := httptest.NewRequest("POST", "/api/v1/tv/broadcasts", bytes.NewBufferString(`{"name":"  "}`))
	rec := httptest.NewRecorder()
	h.CreateBroadcast(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("blank name should 400; got %d", rec.Code)
	}
}

func TestBroadcasts_503WhenRTMPDisabled(t *testing.T) {
	svc := newMockLiveTVService()
	h := NewLiveTVHandler(svc, slog.Default()) // no WithRTMP → rtmp nil
	rec := httptest.NewRecorder()
	h.ListBroadcasts(rec, httptest.NewRequest("GET", "/api/v1/tv/broadcasts", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503 when rtmp disabled; got %d", rec.Code)
	}
}

func TestListBroadcasts_ReportsLiveStatus(t *testing.T) {
	svc := newMockLiveTVService()
	h := NewLiveTVHandler(svc, slog.Default()).WithRTMP(stubRTMPStatus{live: true}, "host", ":1935")
	cfg, _ := json.Marshal(livetv.RTMPConfig{StreamKey: "abcd1234"})
	id := uuid.New()
	svc.tuners[id] = livetv.TunerDevice{ID: id, Type: livetv.TunerTypeRTMP, Name: "Cam", Config: cfg, Enabled: true}

	rec := httptest.NewRecorder()
	h.ListBroadcasts(rec, httptest.NewRequest("GET", "/api/v1/tv/broadcasts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data []BroadcastResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data) != 1 {
		t.Fatalf("want 1 broadcast, got %d", len(env.Data))
	}
	if !env.Data[0].IsLive {
		t.Error("want is_live true (stub reports live)")
	}
	if env.Data[0].StreamKey != "abcd1234" {
		t.Errorf("stream key: got %q", env.Data[0].StreamKey)
	}
}
