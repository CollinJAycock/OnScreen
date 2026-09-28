package v1

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/valkey"
)

// Admin "stop this stream" for every playback mode.
//
// A transcode session has server state to tear down, so stopping one always
// worked. A direct play has none — the client streams the original file from
// /media/stream/{id} — so the old Stop silently did nothing for it (and direct
// play is the common case). An admin stop now does two things for streams the
// server can't kill outright:
//
//  1. It publishes an ignorable playback.stop event on the viewer's SSE
//     channel; the web player stops and shows the admin's message.
//  2. It enforces the stop server-side for clients that don't listen: for
//     playbackStopWindow, media-byte requests (StreamFile, DownloadFile), transcode Start
//     and 'playing' progress beacons for that exact (user, item, client IP)
//     are refused with 403 PLAYBACK_STOPPED. The same user's other devices,
//     other titles and other users are untouched.
const (
	// PlaybackStopEventType is the SSE event an admin stop publishes to the
	// viewer. Dotted like the other non-persisted sync events
	// (progress.updated, playback.transfer) — it is never stored as a
	// notification, and clients that don't know it ignore it.
	PlaybackStopEventType = "playback.stop"
	// playbackStopWindow is how long a stopped (user, item, client) is
	// refused. Long enough that a client's automatic retry loop gives up;
	// short enough that a stop is not a ban.
	playbackStopWindow = 2 * time.Minute
	// playbackStopMessageMax caps the admin's message, in characters.
	playbackStopMessageMax = 200
	// playbackStopKeyPrefix namespaces the shared block in Valkey.
	playbackStopKeyPrefix = "playstop:"
	// playbackStopSweepEvery bounds the in-process map.
	playbackStopSweepEvery = time.Minute
	// errCodePlaybackStopped is the 403 code clients can key a message on.
	errCodePlaybackStopped = "PLAYBACK_STOPPED"
)

// PlaybackStopPayload is the data blob of a playback.stop SSE event. A
// receiving player stops when ItemID is the item it's playing AND the event
// targets it: SessionID equals its own transcode session, or ClientName equals
// its own client name, or neither is set (untargeted — every player of that
// item on the user's account stops).
type PlaybackStopPayload struct {
	ItemID     string `json:"item_id"`
	SessionID  string `json:"session_id,omitempty"`
	ClientName string `json:"client_name,omitempty"`
	Decision   string `json:"decision,omitempty"`
	Message    string `json:"message,omitempty"`
}

// PlaybackStopGuard remembers admin stops so the serving paths can refuse the
// stopped stream for playbackStopWindow. Shared through Valkey when a client
// is wired (any API instance may serve the next range request), with a
// per-process expiring map that also answers when Valkey is unreachable. All
// methods are nil-receiver safe: an unwired guard blocks nothing.
type PlaybackStopGuard struct {
	v      *valkey.Client // nil = in-process only
	logger *slog.Logger
	now    func() time.Time // nil = time.Now (tests override)

	mu        sync.Mutex
	local     map[playbackStopKey]playbackStopEntry
	lastSweep time.Time
}

type playbackStopKey struct {
	user, item uuid.UUID
	ip         string
}

type playbackStopEntry struct {
	message string
	expires time.Time
}

// NewPlaybackStopGuard returns a guard backed by v, or an in-process one when
// v is nil.
func NewPlaybackStopGuard(v *valkey.Client, logger *slog.Logger) *PlaybackStopGuard {
	if logger == nil {
		logger = slog.Default()
	}
	return &PlaybackStopGuard{v: v, logger: logger, local: make(map[playbackStopKey]playbackStopEntry)}
}

func (g *PlaybackStopGuard) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// canonicalIP normalises an address so the key written at stop time matches
// the one a later request derives ("::ffff:10.0.0.5" and "10.0.0.5" are the
// same client). Non-IP strings pass through unchanged.
func canonicalIP(ip string) string {
	if parsed := net.ParseIP(strings.TrimSpace(ip)); parsed != nil {
		return parsed.String()
	}
	return strings.TrimSpace(ip)
}

func playbackStopRedisKey(k playbackStopKey) string {
	return playbackStopKeyPrefix + k.user.String() + ":" + k.item.String() + ":" + k.ip
}

// Block refuses (userID, itemID, clientIP) for playbackStopWindow and returns
// when the refusal lapses. message is the admin's note, echoed in the 403.
func (g *PlaybackStopGuard) Block(ctx context.Context, userID, itemID uuid.UUID, clientIP, message string) time.Time {
	if g == nil {
		return time.Time{}
	}
	k := playbackStopKey{userID, itemID, canonicalIP(clientIP)}
	now := g.clock()
	until := now.Add(playbackStopWindow)
	g.mu.Lock()
	g.sweepLocked(now)
	g.local[k] = playbackStopEntry{message: message, expires: until}
	g.mu.Unlock()
	if g.v != nil {
		if err := g.v.Set(ctx, playbackStopRedisKey(k), message, playbackStopWindow); err != nil {
			g.logger.WarnContext(ctx, "playback stop: record block", "item_id", itemID, "err", err)
		}
	}
	return until
}

// Blocked reports whether (userID, itemID, clientIP) is inside an admin stop's
// refusal window, with the admin's message.
func (g *PlaybackStopGuard) Blocked(ctx context.Context, userID, itemID uuid.UUID, clientIP string) (string, bool) {
	if g == nil || userID == uuid.Nil || itemID == uuid.Nil {
		return "", false
	}
	k := playbackStopKey{userID, itemID, canonicalIP(clientIP)}
	if g.v != nil {
		msg, err := g.v.Get(ctx, playbackStopRedisKey(k))
		if err == nil {
			return msg, true
		}
		if !errors.Is(err, redis.Nil) {
			// Debug: this runs on every media byte request, so an outage
			// would otherwise log once per range request.
			g.logger.DebugContext(ctx, "playback stop: lookup", "item_id", itemID, "err", err)
		}
	}
	now := g.clock()
	g.mu.Lock()
	defer g.mu.Unlock()
	if e, ok := g.local[k]; ok && now.Before(e.expires) {
		return e.message, true
	}
	return "", false
}

// sweepLocked drops expired entries, at most once per playbackStopSweepEvery.
// Caller holds g.mu.
func (g *PlaybackStopGuard) sweepLocked(now time.Time) {
	if now.Sub(g.lastSweep) < playbackStopSweepEvery {
		return
	}
	for k, e := range g.local {
		if !now.Before(e.expires) {
			delete(g.local, k)
		}
	}
	g.lastSweep = now
}

// playbackStoppedText is the sentence a refused client shows.
func playbackStoppedText(message string) string {
	if message == "" {
		return "Playback was stopped by the server admin."
	}
	return "Playback was stopped by the server admin: " + message
}

// playbackStopBlocks writes a 403 PLAYBACK_STOPPED and reports true when the
// caller's (user, item, client IP) is inside an admin stop's window.
func playbackStopBlocks(w http.ResponseWriter, r *http.Request, g *PlaybackStopGuard, userID, itemID uuid.UUID) bool {
	msg, blocked := g.Blocked(r.Context(), userID, itemID, audit.ClientIP(r))
	if !blocked {
		return false
	}
	respond.Error(w, r, http.StatusForbidden, errCodePlaybackStopped, playbackStoppedText(msg))
	return true
}

// errStopMessageTooLong is returned by cleanStopMessage.
var errStopMessageTooLong = errors.New("message must be 200 characters or fewer")

// cleanStopMessage trims the admin's note and flattens control characters
// (newlines, tabs, escapes) to spaces so it renders as one line everywhere.
func cleanStopMessage(raw string) (string, error) {
	msg := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, raw)
	msg = strings.TrimSpace(msg)
	if utf8.RuneCountInString(msg) > playbackStopMessageMax {
		return "", errStopMessageTooLong
	}
	return msg, nil
}

// WithPlaybackStops wires the admin-stop guard into StreamFile, DownloadFile
// and Progress so a stopped direct play is refused for the stop window. nil =
// no enforcement.
func (h *ItemHandler) WithPlaybackStops(g *PlaybackStopGuard) *ItemHandler {
	h.stops = g
	return h
}

// WithPlaybackStops wires the admin-stop guard into Start so a stopped remux
// or direct-stream client can't immediately start a replacement session.
func (h *NativeTranscodeHandler) WithPlaybackStops(g *PlaybackStopGuard) *NativeTranscodeHandler {
	h.stops = g
	return h
}
