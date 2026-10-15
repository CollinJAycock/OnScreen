package v1

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/transcode"
)

// streamSessionLister lists the server's transcode / remux sessions.
// Satisfied by *transcode.SessionStore.
type streamSessionLister interface {
	List(ctx context.Context) ([]transcode.Session, error)
}

// StreamLimit enforces the admin's per-user concurrent-stream limit
// (users.max_concurrent_streams) across every kind of playback. Transcode
// Start used to count only its own sessions, so direct plays — most of a
// household's streams — never counted and the limit held only for
// transcodes. It counts a user's live transcode sessions plus their direct
// plays, which the stream tracker sees from byte traffic and progress
// heartbeats.
//
// Only an admin-set limit is enforced here. With none set, playback behaves
// as before: Start's built-in cap of 5 transcode sessions still guards the
// encoders, and direct plays are unlimited.
type StreamLimit struct {
	caps     UserStreamCapsReader
	sessions streamSessionLister
	tracker  *streaming.Tracker
}

// NewStreamLimit builds the limit. Any nil part disables the matching half:
// no caps reader = never enforced, no tracker = direct plays not counted.
func NewStreamLimit(caps UserStreamCapsReader, sessions streamSessionLister, tracker *streaming.Tracker) *StreamLimit {
	return &StreamLimit{caps: caps, sessions: sessions, tracker: tracker}
}

// streamRequest identifies the playback being asked for: who, from which
// client address, and which item. The user's existing streams of the same
// item and from the same address are not counted against it — the first is
// a device taking over (Start supersedes it), the second the same device
// moving on (the next track of an album, the next episode).
type streamRequest struct {
	user     uuid.UUID
	clientIP string
	item     uuid.UUID
}

// limitFor returns the user's admin-set limit, 0 when none.
func (l *StreamLimit) limitFor(ctx context.Context, user uuid.UUID) int {
	if l == nil || l.caps == nil {
		return 0
	}
	c, err := l.caps.GetStreamCaps(ctx, user)
	if err != nil {
		return 0 // fail open: a caps read error must not block playback
	}
	return c.ConcurrentStreams
}

// directStreams counts the user's direct plays on other devices.
func (l *StreamLimit) directStreams(ctx context.Context, req streamRequest) int {
	if l == nil || l.tracker == nil {
		return 0
	}
	var sessions []transcode.Session
	if l.sessions != nil {
		sessions, _ = l.sessions.List(ctx)
	}
	return countDirectStreams(sessions, l.tracker.List(), req)
}

// otherStreams counts every stream of the user's that the requested one
// would join: live transcode sessions and direct plays, minus the ones
// streamRequest excludes.
func (l *StreamLimit) otherStreams(ctx context.Context, req streamRequest) int {
	if l == nil {
		return 0
	}
	var sessions []transcode.Session
	if l.sessions != nil {
		sessions, _ = l.sessions.List(ctx)
	}
	var entries []streaming.Entry
	if l.tracker != nil {
		entries = l.tracker.List()
	}
	return countTranscodeStreams(sessions, req, time.Now()) + countDirectStreams(sessions, entries, req)
}

// refuseStreamLimit answers 429 TOO_MANY_SESSIONS — the code transcode
// Start has always used for its session cap, which the clients already
// handle. The players show the message as-is, so it is written for the
// viewer.
func refuseStreamLimit(w http.ResponseWriter, r *http.Request, active, limit int) {
	respond.Error(w, r, http.StatusTooManyRequests, "TOO_MANY_SESSIONS", streamLimitMessage(active, limit))
}

func streamLimitMessage(active, limit int) string {
	if limit == 1 {
		return "Your account can play one stream at a time, and another device is already playing. Stop it to watch here."
	}
	return fmt.Sprintf("Your account can play %d streams at a time, and %d are already playing on other devices. Stop one to watch here.",
		limit, active)
}

// Blocks reports whether the requested playback is over the user's limit,
// writing the 429 when it is. Used by the play decision and by the direct
// stream routes; transcode Start folds directStreams into its own atomic
// session cap instead.
func (l *StreamLimit) Blocks(w http.ResponseWriter, r *http.Request, req streamRequest) bool {
	limit := l.limitFor(r.Context(), req.user)
	if limit <= 0 {
		return false
	}
	if n := l.otherStreams(r.Context(), req); n >= limit {
		refuseStreamLimit(w, r, n, limit)
		return true
	}
	return false
}

// countTranscodeStreams counts the user's live transcode / remux playbacks:
// parent sessions (an ABR rung is part of its parent's playback) active
// within transcode.ActiveSessionWindow — the same definition of live as
// SessionStore.CountByUser.
func countTranscodeStreams(sessions []transcode.Session, req streamRequest, now time.Time) int {
	n := 0
	for _, s := range sessions {
		if s.UserID != req.user || s.ParentID != "" || s.MediaItemID == req.item {
			continue
		}
		if s.ClientIP != "" && canonicalIP(s.ClientIP) == req.clientIP {
			continue
		}
		at := s.LastActivityAt
		if at.IsZero() {
			at = s.CreatedAt
		}
		if now.Sub(at) <= transcode.ActiveSessionWindow {
			n++
		}
	}
	return n
}

// countDirectStreams counts the user's direct plays as one per client
// address: a player preloading the next track, or a byte-traffic entry
// lingering after the track change, is still one stream. Entries for an
// item one of the user's transcode sessions covers are that session's own
// traffic (its progress heartbeat, or a storage-less worker pulling the
// source) and aren't counted again. Expired entries are already gone: the
// tracker drops them after its TTL.
func countDirectStreams(sessions []transcode.Session, entries []streaming.Entry, req streamRequest) int {
	covered := make(map[uuid.UUID]bool)
	for _, s := range sessions {
		if s.UserID == req.user {
			covered[s.MediaItemID] = true
		}
	}
	clients := make(map[string]bool)
	for _, e := range entries {
		if e.UserID != req.user || e.MediaItemID == uuid.Nil || e.MediaItemID == req.item || covered[e.MediaItemID] {
			continue
		}
		ip := canonicalIP(e.ClientIP)
		if ip == req.clientIP {
			continue
		}
		clients[ip] = true
	}
	return len(clients)
}

// BlocksNewStream is Blocks for the direct stream routes, which see every
// range request of a playback: a request continuing a stream the tracker
// already holds for this viewer, client and item always passes, so only a
// stream's first request pays for the count.
func (l *StreamLimit) BlocksNewStream(w http.ResponseWriter, r *http.Request, req streamRequest) bool {
	if l == nil || l.caps == nil {
		return false
	}
	if l.tracker != nil && l.tracker.HasStream(req.user, req.clientIP, req.item) {
		return false
	}
	return l.Blocks(w, r, req)
}
