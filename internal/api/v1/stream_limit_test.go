package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/transcode"
)

type fixedCaps struct{ c StreamCaps }

func (f fixedCaps) GetStreamCaps(context.Context, uuid.UUID) (StreamCaps, error) { return f.c, nil }

type fixedSessions []transcode.Session

func (f fixedSessions) List(context.Context) ([]transcode.Session, error) { return f, nil }

func TestCountDirectStreams(t *testing.T) {
	user, other := uuid.New(), uuid.New()
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	entries := []streaming.Entry{
		// The TV: one stream, seen as bytes and as a heartbeat.
		{UserID: user, ClientIP: "10.0.0.5", MediaItemID: a},
		{UserID: user, ClientIP: "10.0.0.5", MediaItemID: a, FromHeartbeat: true},
		// The phone moving through an album: two tracks, one stream.
		{UserID: user, ClientIP: "10.0.0.6", MediaItemID: b},
		{UserID: user, ClientIP: "10.0.0.6", MediaItemID: c},
		// A transcode's own heartbeat — the session already counts it.
		{UserID: user, ClientIP: "10.0.0.7", MediaItemID: d, FromHeartbeat: true},
		// Someone else, and an unattributed legacy entry.
		{UserID: other, ClientIP: "10.0.0.8", MediaItemID: a},
		{ClientIP: "10.0.0.9", FilePath: "/m/x.mkv"},
	}
	sessions := []transcode.Session{{UserID: user, MediaItemID: d, ClientIP: "10.0.0.7"}}

	if n := countDirectStreams(sessions, entries, streamRequest{user: user, clientIP: "10.0.0.1", item: uuid.New()}); n != 2 {
		t.Errorf("new device: %d direct streams, want 2 (TV + phone)", n)
	}
	// The phone itself moving to the next track doesn't count its own stream.
	if n := countDirectStreams(sessions, entries, streamRequest{user: user, clientIP: "10.0.0.6", item: uuid.New()}); n != 1 {
		t.Errorf("same device: %d, want 1", n)
	}
	// Taking the TV's item over on another device doesn't count the TV.
	if n := countDirectStreams(sessions, entries, streamRequest{user: user, clientIP: "10.0.0.1", item: a}); n != 1 {
		t.Errorf("takeover: %d, want 1", n)
	}
}

func TestCountTranscodeStreams(t *testing.T) {
	user := uuid.New()
	now := time.Now()
	item := uuid.New()
	sessions := []transcode.Session{
		{UserID: user, MediaItemID: uuid.New(), ClientIP: "10.0.0.5", LastActivityAt: now},
		{UserID: user, MediaItemID: uuid.New(), ParentID: "p", LastActivityAt: now},                        // ABR rung
		{UserID: user, MediaItemID: uuid.New(), ClientIP: "10.0.0.6", LastActivityAt: now.Add(-time.Hour)}, // stale
		{UserID: user, MediaItemID: item, ClientIP: "10.0.0.7", LastActivityAt: now},                       // superseded
		{UserID: user, MediaItemID: uuid.New(), ClientIP: "10.0.0.1", CreatedAt: now},                      // same device
		{UserID: uuid.New(), MediaItemID: uuid.New(), ClientIP: "10.0.0.8", LastActivityAt: now},           // other user
	}
	if n := countTranscodeStreams(sessions, streamRequest{user: user, clientIP: "10.0.0.1", item: item}, now); n != 1 {
		t.Errorf("got %d, want 1", n)
	}
}

func TestStreamLimitBlocks(t *testing.T) {
	user := uuid.New()
	tr := streaming.NewTracker()
	tr.TouchFile(user, "10.0.0.5", uuid.New(), uuid.New(), "/m/a.mkv", "TV")
	sessions := fixedSessions{{UserID: user, MediaItemID: uuid.New(), ClientIP: "10.0.0.6", LastActivityAt: time.Now()}}
	req := streamRequest{user: user, clientIP: "10.0.0.1", item: uuid.New()}

	cases := []struct {
		name  string
		limit int
		want  bool
	}{
		{"no limit set", 0, false},
		{"room for one more", 3, false},
		{"at the limit", 2, true},
	}
	for _, tc := range cases {
		l := NewStreamLimit(fixedCaps{StreamCaps{ConcurrentStreams: tc.limit}}, sessions, tr)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if got := l.Blocks(w, r, req); got != tc.want {
			t.Errorf("%s: Blocks = %v, want %v", tc.name, got, tc.want)
		}
		if tc.want {
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.NewDecoder(w.Body).Decode(&body)
			if w.Code != http.StatusTooManyRequests || body.Error.Code != "TOO_MANY_SESSIONS" {
				t.Errorf("%s: got %d %q, want 429 TOO_MANY_SESSIONS", tc.name, w.Code, body.Error.Code)
			}
		}
	}

	// A nil limit never blocks.
	var nilLimit *StreamLimit
	if nilLimit.Blocks(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), req) {
		t.Error("nil StreamLimit blocked")
	}
}

// Range requests continuing a stream the tracker already holds pass even
// when the user is at the limit; only a stream's first request is counted.
func TestStreamLimitBlocksNewStreamOnly(t *testing.T) {
	user, item := uuid.New(), uuid.New()
	tr := streaming.NewTracker()
	tr.TouchFile(user, "10.0.0.5", uuid.New(), uuid.New(), "/m/a.mkv", "TV")
	l := NewStreamLimit(fixedCaps{StreamCaps{ConcurrentStreams: 1}}, nil, tr)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	req := streamRequest{user: user, clientIP: "10.0.0.1", item: item}

	if !l.BlocksNewStream(httptest.NewRecorder(), r, req) {
		t.Fatal("second device's first request passed a limit of 1")
	}
	tr.TouchFile(user, "10.0.0.1", item, uuid.New(), "/m/b.mkv", "Phone")
	if l.BlocksNewStream(httptest.NewRecorder(), r, req) {
		t.Error("a request continuing a tracked stream was refused")
	}
}
