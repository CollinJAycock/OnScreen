package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/onscreen/onscreen/internal/transcode"
	"github.com/onscreen/onscreen/internal/valkey"
)

// decisionDirectPlay is the watch_events.decision value for a client served
// the original file bytes (StreamFile).
const decisionDirectPlay = "directPlay"

const (
	// playDecisionTTL bounds how long a recorded decision can attribute the
	// progress reports of one (user, item). Long enough to cover a film with a
	// few long pauses; short enough that last night's transcode doesn't label
	// tomorrow's direct play of the same title.
	playDecisionTTL = 6 * time.Hour
	// playDecisionRefreshEvery throttles StreamFile: a direct-play client
	// issues many range requests per title, and only the first in this window
	// pays for the session lookup + shared write.
	playDecisionRefreshEvery = time.Minute
	// playDecisionSweepEvery bounds the in-process map: expired entries are
	// swept at most this often.
	playDecisionSweepEvery = 10 * time.Minute
	// playDecisionKeyPrefix namespaces the shared record in Valkey.
	playDecisionKeyPrefix = "playdecision:"
)

// normalizePlayDecision returns d when it is one of the playback decisions the
// analytics dimension understands, else "". This is the allowlist that keeps
// typos and junk from a hostile client out of watch_events.decision.
func normalizePlayDecision(d string) string {
	switch d {
	case "directPlay", "directStream", "remux", "transcode":
		return d
	}
	return ""
}

// PlayDecisionRecorder remembers the last playback decision the server made
// for each (user, item): the session decision when transcode Start creates a
// session, directPlay when StreamFile serves the original bytes. Progress
// consults it to attribute heartbeats from clients that don't report a
// decision themselves (Android TV / Fire TV, the music players, the TV web
// clients, older app versions) — without it those plays chart as "Unknown"
// even though the server knew exactly what it served.
//
// Shared through Valkey when a client is wired (so any API instance can answer
// for a stream another instance started), with a per-process expiring map that
// both answers when Valkey is unreachable and dedupes StreamFile's rewrites.
// All methods are nil-receiver safe, so an unwired handler is a no-op.
type PlayDecisionRecorder struct {
	v      *valkey.Client // nil = in-process only
	logger *slog.Logger
	now    func() time.Time // nil = time.Now (tests override)

	mu        sync.Mutex
	local     map[playDecisionKey]playDecisionEntry
	lastSweep time.Time
}

type playDecisionKey struct{ user, item uuid.UUID }

type playDecisionEntry struct {
	decision string
	recorded time.Time
	expires  time.Time
}

// NewPlayDecisionRecorder returns a recorder backed by v, or an in-process one
// when v is nil.
func NewPlayDecisionRecorder(v *valkey.Client, logger *slog.Logger) *PlayDecisionRecorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &PlayDecisionRecorder{v: v, logger: logger, local: make(map[playDecisionKey]playDecisionEntry)}
}

func (p *PlayDecisionRecorder) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

func playDecisionRedisKey(userID, itemID uuid.UUID) string {
	return playDecisionKeyPrefix + userID.String() + ":" + itemID.String()
}

// Record stores decision as the latest for (userID, itemID). Best-effort: a
// Valkey failure is logged and the in-process record still answers for this
// instance. Decisions outside the allowlist are ignored.
func (p *PlayDecisionRecorder) Record(ctx context.Context, userID, itemID uuid.UUID, decision string) {
	d := normalizePlayDecision(decision)
	if p == nil || d == "" {
		return
	}
	now := p.clock()
	p.mu.Lock()
	p.sweepLocked(now)
	p.local[playDecisionKey{userID, itemID}] = playDecisionEntry{decision: d, recorded: now, expires: now.Add(playDecisionTTL)}
	p.mu.Unlock()

	if p.v == nil {
		return
	}
	if err := p.v.Set(ctx, playDecisionRedisKey(userID, itemID), d, playDecisionTTL); err != nil {
		p.logger.WarnContext(ctx, "play decision: record", "item_id", itemID, "err", err)
	}
}

// Last returns the most recent decision recorded for (userID, itemID) within
// the TTL, or "" when there is none.
func (p *PlayDecisionRecorder) Last(ctx context.Context, userID, itemID uuid.UUID) string {
	if p == nil {
		return ""
	}
	if p.v != nil {
		val, err := p.v.Get(ctx, playDecisionRedisKey(userID, itemID))
		if err == nil {
			if d := normalizePlayDecision(val); d != "" {
				return d
			}
		} else if !errors.Is(err, redis.Nil) {
			// Debug, not Warn: this runs on every decision-less heartbeat, so an
			// outage would otherwise log once per stream every few seconds.
			p.logger.DebugContext(ctx, "play decision: lookup", "item_id", itemID, "err", err)
		}
	}
	now := p.clock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.local[playDecisionKey{userID, itemID}]; ok && now.Before(e.expires) {
		return e.decision
	}
	return ""
}

// recentlyRecorded reports whether THIS instance recorded decision for
// (userID, itemID) within playDecisionRefreshEvery — StreamFile's cue to skip
// the lookup + rewrite on a range continuation.
func (p *PlayDecisionRecorder) recentlyRecorded(userID, itemID uuid.UUID, decision string) bool {
	if p == nil {
		return false
	}
	now := p.clock()
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.local[playDecisionKey{userID, itemID}]
	return ok && e.decision == decision && now.Sub(e.recorded) < playDecisionRefreshEvery
}

// sweepLocked drops expired entries, at most once per playDecisionSweepEvery.
// Caller holds p.mu.
func (p *PlayDecisionRecorder) sweepLocked(now time.Time) {
	if now.Sub(p.lastSweep) < playDecisionSweepEvery {
		return
	}
	for k, e := range p.local {
		if !now.Before(e.expires) {
			delete(p.local, k)
		}
	}
	p.lastSweep = now
}

// itemSessionLister is the optional read side of the session store Progress
// and StreamFile use for decision attribution. Checked with a type assertion
// on h.sessions so test doubles that only implement ItemSessionCleaner keep
// compiling. Satisfied by *transcode.SessionStore.
type itemSessionLister interface {
	ListByUserItem(ctx context.Context, userID, mediaItemID uuid.UUID) ([]transcode.Session, error)
}

// WithPlayDecisions attaches the decision recorder: StreamFile records direct
// play into it and Progress falls back to it for heartbeats that carry no
// decision. nil = no attribution (decision-less heartbeats record NULL).
func (h *ItemHandler) WithPlayDecisions(p *PlayDecisionRecorder) *ItemHandler {
	h.decisions = p
	return h
}

// WithPlayDecisions attaches the decision recorder so Start remembers the
// decision of each session it creates. nil = not recorded.
func (h *NativeTranscodeHandler) WithPlayDecisions(p *PlayDecisionRecorder) *NativeTranscodeHandler {
	h.decisions = p
	return h
}

// activeSessionDecision returns the decision of the newest live transcode
// session userID has for itemID, and whether any session exists at all.
func (h *ItemHandler) activeSessionDecision(ctx context.Context, userID, itemID uuid.UUID) (string, bool) {
	lister, ok := h.sessions.(itemSessionLister)
	if !ok {
		return "", false
	}
	sessions, err := lister.ListByUserItem(ctx, userID, itemID)
	if err != nil || len(sessions) == 0 {
		return "", false
	}
	var newest *transcode.Session
	for i := range sessions {
		if normalizePlayDecision(sessions[i].Decision) == "" {
			continue
		}
		if newest == nil || sessions[i].CreatedAt.After(newest.CreatedAt) {
			newest = &sessions[i]
		}
	}
	if newest == nil {
		return "", true
	}
	return newest.Decision, true
}

// inferPlayDecision attributes a heartbeat whose client didn't report a
// (valid) decision: the newest live session for (user, item) wins, else the
// recorder's last decision, else nil (recorded as unknown).
func (h *ItemHandler) inferPlayDecision(ctx context.Context, userID, itemID uuid.UUID) *string {
	if d, _ := h.activeSessionDecision(ctx, userID, itemID); d != "" {
		return &d
	}
	if d := h.decisions.Last(ctx, userID, itemID); d != "" {
		return &d
	}
	return nil
}

// noteDirectPlay records that StreamFile served userID the original bytes of
// itemID. Two kinds of request reach StreamFile without being a direct play,
// and neither may overwrite the session's decision: a storage-less transcode
// worker pulls its source through this route with a stream token minted from
// the viewer's claims (buildSourceURL), so it looks like the viewer; ffmpeg
// announces itself as Lavf, which catches the trailing reads after a session
// is torn down, and a live session for the (user, item) catches the rest.
func (h *ItemHandler) noteDirectPlay(r *http.Request, userID, itemID uuid.UUID) {
	if h.decisions == nil || r.Method != http.MethodGet {
		return
	}
	if strings.HasPrefix(r.Header.Get("User-Agent"), "Lavf/") {
		return
	}
	if h.decisions.recentlyRecorded(userID, itemID, decisionDirectPlay) {
		return
	}
	if _, live := h.activeSessionDecision(r.Context(), userID, itemID); live {
		return
	}
	h.decisions.Record(r.Context(), userID, itemID, decisionDirectPlay)
}
