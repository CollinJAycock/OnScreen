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

type fixedCaps struct {
	c StreamCaps
	// household is what HouseholdMembers answers (nil: a household of one).
	household []uuid.UUID
}

func (f fixedCaps) GetStreamCaps(context.Context, uuid.UUID) (StreamCaps, error) { return f.c, nil }
func (f fixedCaps) HouseholdMembers(context.Context, uuid.UUID) ([]uuid.UUID, error) {
	return f.household, nil
}

// HouseholdMembers makes staticCaps (remote_limits_test.go) a StreamLimitCaps
// for the tests that build a StreamLimit from it: a household of one.
func (staticCaps) HouseholdMembers(context.Context, uuid.UUID) ([]uuid.UUID, error) {
	return nil, nil
}

// S7: a managed profile's streams count with its owner's and the other
// profiles' against the household's limit. A user capped at one stream used to
// get one more per profile they created and switched to.
func TestStreamLimit_CountsTheHousehold(t *testing.T) {
	owner, kid, teen, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tr := streaming.NewTracker()
	// The owner direct-plays on the TV.
	tr.TouchFile(owner, "10.0.0.5", uuid.New(), uuid.New(), "/m/a.mkv", "TV")
	// A stranger's stream never counts.
	tr.TouchFile(stranger, "10.0.0.9", uuid.New(), uuid.New(), "/m/s.mkv", "Phone")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	caps := fixedCaps{c: StreamCaps{ConcurrentStreams: 1}, household: []uuid.UUID{owner, kid, teen}}

	// The kid, on a tablet, would be the household's second stream.
	l := NewStreamLimit(caps, fixedSessions(nil), tr)
	if !l.Blocks(httptest.NewRecorder(), r, streamRequest{user: kid, clientIP: "10.0.0.7", item: uuid.New()}) {
		t.Error("a profile's stream passed while its owner used the household's only slot")
	}

	// A transcode of another profile counts too, for the owner as for anyone.
	sessions := fixedSessions{{UserID: teen, MediaItemID: uuid.New(), ClientIP: "10.0.0.8", LastActivityAt: time.Now()}}
	l = NewStreamLimit(fixedCaps{c: StreamCaps{ConcurrentStreams: 2}, household: caps.household}, sessions, tr)
	if !l.Blocks(httptest.NewRecorder(), r, streamRequest{user: owner, clientIP: "10.0.0.1", item: uuid.New()}) {
		t.Error("the owner's third household stream (TV + the teen's transcode + this) passed a limit of 2")
	}

	// Transcode Start counts the requester's own sessions itself; directStreams
	// gives it everything else: the household's direct plays and the other
	// accounts' transcodes.
	if n := l.directStreams(context.Background(), streamRequest{user: kid, clientIP: "10.0.0.7", item: uuid.New()}); n != 2 {
		t.Errorf("directStreams = %d, want 2 (owner's direct play + teen's transcode)", n)
	}

	// Without a household (an account with no profiles) nothing changes: the
	// kid's view sees no streams of its own.
	l = NewStreamLimit(fixedCaps{c: StreamCaps{ConcurrentStreams: 1}}, fixedSessions(nil), tr)
	if l.Blocks(httptest.NewRecorder(), r, streamRequest{user: kid, clientIP: "10.0.0.7", item: uuid.New()}) {
		t.Error("an account's limit counted streams of an unrelated account")
	}
}

// Another account's playback of the same item is not "the requester taking it
// over": the takeover exclusion applies to the requester's own streams only.
func TestStreamLimit_HouseholdSameItemStillCounts(t *testing.T) {
	owner, kid := uuid.New(), uuid.New()
	item := uuid.New()
	now := time.Now()
	req := streamRequest{user: kid, clientIP: "10.0.0.7", item: item,
		members: map[uuid.UUID]bool{owner: true, kid: true}}
	sessions := []transcode.Session{{UserID: owner, MediaItemID: item, ClientIP: "10.0.0.5", LastActivityAt: now}}
	if n := countTranscodeStreams(sessions, req, now); n != 1 {
		t.Errorf("owner's transcode of the same item: %d, want 1", n)
	}
	entries := []streaming.Entry{{UserID: owner, ClientIP: "10.0.0.5", MediaItemID: item}}
	if n := countDirectStreams(nil, entries, req); n != 1 {
		t.Errorf("owner's direct play of the same item: %d, want 1", n)
	}
}

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
		l := NewStreamLimit(fixedCaps{c: StreamCaps{ConcurrentStreams: tc.limit}}, sessions, tr)
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
	l := NewStreamLimit(fixedCaps{c: StreamCaps{ConcurrentStreams: 1}}, nil, tr)
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
