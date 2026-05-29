package scrobble

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeStore struct {
	settings Settings
	err      error
}

func (f fakeStore) Get(context.Context, uuid.UUID) (Settings, error) { return f.settings, f.err }

type fakeTracks struct {
	track Track
	ok    bool
	err   error
}

func (f fakeTracks) Track(context.Context, uuid.UUID) (Track, bool, error) {
	return f.track, f.ok, f.err
}

// newSvc wires a Service whose ListenBrainz submit hits the given httptest
// server (captured via the returned recorder), using a plain client so the
// loopback test URL isn't blocked.
func newSvc(t *testing.T, store SettingsStore, tracks TrackLookup) (*Service, *capture) {
	t.Helper()
	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.hits++
		cap.authHeader = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &cap.body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	svc := NewService(store, tracks, srv.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.submitURL = srv.URL
	return svc, cap
}

type capture struct {
	hits       int
	authHeader string
	body       map[string]any
}

func dur(ms int64) *int64 { return &ms }

func TestOnScrobble_SubmitsListen(t *testing.T) {
	store := fakeStore{settings: Settings{ListenBrainzToken: "tok123", ListenBrainzEnabled: true}}
	tracks := fakeTracks{ok: true, track: Track{
		TrackName: "Black Dog", ArtistName: "Led Zeppelin", ReleaseName: "IV",
		RecordingMBID: "11111111-1111-1111-1111-111111111111",
	}}
	svc, cap := newSvc(t, store, tracks)

	at := time.Unix(1_700_000_000, 0)
	// Played 200s of a 300s track — well past the 50% threshold.
	svc.OnScrobble(context.Background(), uuid.New(), uuid.New(), 200_000, dur(300_000), at)

	if cap.hits != 1 {
		t.Fatalf("expected 1 submit, got %d", cap.hits)
	}
	if cap.authHeader != "Token tok123" {
		t.Errorf("auth header: got %q, want %q", cap.authHeader, "Token tok123")
	}
	if cap.body["listen_type"] != "single" {
		t.Errorf("listen_type: got %v", cap.body["listen_type"])
	}
	payload, _ := cap.body["payload"].([]any)
	if len(payload) != 1 {
		t.Fatalf("payload length: got %d, want 1", len(payload))
	}
	listen, _ := payload[0].(map[string]any)
	if listen["listened_at"].(float64) != float64(at.Unix()) {
		t.Errorf("listened_at: got %v, want %d", listen["listened_at"], at.Unix())
	}
	meta, _ := listen["track_metadata"].(map[string]any)
	if meta["artist_name"] != "Led Zeppelin" || meta["track_name"] != "Black Dog" {
		t.Errorf("track_metadata wrong: %+v", meta)
	}
	if meta["release_name"] != "IV" {
		t.Errorf("release_name: got %v", meta["release_name"])
	}
}

// A skip / brief preview (well under both 50% and 4 minutes) emits a 'stop'
// but must NOT scrobble.
func TestOnScrobble_SkipsBelowThreshold(t *testing.T) {
	store := fakeStore{settings: Settings{ListenBrainzToken: "tok", ListenBrainzEnabled: true}}
	tracks := fakeTracks{ok: true, track: Track{TrackName: "x", ArtistName: "y"}}
	svc, cap := newSvc(t, store, tracks)

	svc.OnScrobble(context.Background(), uuid.New(), uuid.New(), 10_000, dur(300_000), time.Now())
	if cap.hits != 0 {
		t.Errorf("below-threshold play must not submit, got %d hits", cap.hits)
	}
}

func TestOnScrobble_SkipsWhenDisabled(t *testing.T) {
	store := fakeStore{settings: Settings{ListenBrainzToken: "tok", ListenBrainzEnabled: false}}
	tracks := fakeTracks{ok: true, track: Track{TrackName: "x", ArtistName: "y"}}
	svc, cap := newSvc(t, store, tracks)

	svc.OnScrobble(context.Background(), uuid.New(), uuid.New(), 200_000, dur(300_000), time.Now())
	if cap.hits != 0 {
		t.Errorf("disabled user must not submit, got %d hits", cap.hits)
	}
}

func TestOnScrobble_SkipsNonMusic(t *testing.T) {
	store := fakeStore{settings: Settings{ListenBrainzToken: "tok", ListenBrainzEnabled: true}}
	tracks := fakeTracks{ok: false} // not a music track
	svc, cap := newSvc(t, store, tracks)

	svc.OnScrobble(context.Background(), uuid.New(), uuid.New(), 200_000, dur(300_000), time.Now())
	if cap.hits != 0 {
		t.Errorf("non-music play must not submit, got %d hits", cap.hits)
	}
}

func TestOnScrobble_SkipsMissingRequiredFields(t *testing.T) {
	store := fakeStore{settings: Settings{ListenBrainzToken: "tok", ListenBrainzEnabled: true}}
	// Music track but no resolvable artist — ListenBrainz requires artist_name.
	tracks := fakeTracks{ok: true, track: Track{TrackName: "Untitled", ArtistName: ""}}
	svc, cap := newSvc(t, store, tracks)

	svc.OnScrobble(context.Background(), uuid.New(), uuid.New(), 200_000, dur(300_000), time.Now())
	if cap.hits != 0 {
		t.Errorf("missing artist must not submit, got %d hits", cap.hits)
	}
}

// listenedEnough is the listen-threshold gate: ≥50% of a known duration, or
// ≥4 minutes regardless. An unknown/zero duration falls back to the 4-minute
// rule alone.
func TestListenedEnough(t *testing.T) {
	tests := []struct {
		name       string
		positionMS int64
		durationMS *int64
		want       bool
	}{
		{"exactly half of a known track", 150_000, dur(300_000), true},
		{"just past half", 150_001, dur(300_000), true},
		{"just under half, under 4min", 149_999, dur(300_000), false},
		{"early skip", 10_000, dur(300_000), false},
		{"four-minute rule fires below 50%", 240_000, dur(600_000), true},
		{"one ms under four minutes, below 50%", 239_999, dur(600_000), false},
		{"unknown duration past 4min", 250_000, nil, true},
		{"unknown duration under 4min", 60_000, nil, false},
		{"zero duration under 4min", 60_000, dur(0), false},
		{"zero duration past 4min", 300_000, dur(0), true},
		{"negative duration ignored, under 4min", 60_000, dur(-5), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listenedEnough(tt.positionMS, tt.durationMS); got != tt.want {
				t.Errorf("listenedEnough(%d, %v) = %v, want %v", tt.positionMS, tt.durationMS, got, tt.want)
			}
		})
	}
}
