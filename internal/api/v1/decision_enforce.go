package v1

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/transcode"
)

// The play decision (POST /items/{id}/playback-decision) is the server's:
// clients play its verdict and decide for themselves only when they can't
// get one. These two checks hold the streaming routes to it, so a client
// that decided on its own — an old build, or one whose decision call failed
// — gets the same outcome instead of bypassing a conversion the server
// requires.

// videoCopyRefused reports whether transcode-start must re-encode video a
// client asked it to copy: the play decision for file is a transcode. With
// no capability profile the client's decoders are unknown, so only the
// viewer's resolution ceiling is held (a bitrate cap already escalates the
// copy in Start).
func videoCopyRefused(file media.File, caps transcode.ClientCapabilities, hasProfile bool, q QualityProfile, server transcode.ServerCaps) bool {
	if hasProfile {
		// "unsupported" here is Dolby Vision profile 5 that would need a
		// re-encode; Start refuses that once the copy is off.
		d := transcode.Decide(file, caps, server)
		return d == transcode.DecisionTranscode || d == transcode.DecisionUnsupported
	}
	return q.overResolution(&file)
}

// overResolution reports whether a video's picture is above the viewer's
// resolution ceiling (the 16:9 box applyTo folds into the capabilities).
func (q QualityProfile) overResolution(file *media.File) bool {
	if q.MaxVideoHeight <= 0 || file == nil {
		return false
	}
	w, h := 0, 0
	if file.ResolutionW != nil {
		w = *file.ResolutionW
	}
	if file.ResolutionH != nil {
		h = *file.ResolutionH
	}
	return h > q.MaxVideoHeight || w > q.MaxVideoHeight*16/9
}

// Error code for a direct stream of a file the server would convert.
const codeTranscodeRequired = "TRANSCODE_REQUIRED"

// DirectPlayGate refuses a direct stream (GET /media/stream/{id}) of a
// video the play decision would convert for this viewer whatever their
// device: a file the integrity probe marked damaged, a source above the
// internet streaming limit for a client outside the home network, or one
// above the viewer's own quality profile (video bitrate or resolution).
// Capability-based verdicts (a codec the device can't decode) aren't judged
// here — the stream request carries no capability profile, and a device
// that plays the file anyway is no harm to anyone.
//
// Audio-only files and books are never refused: the web music player
// streams originals without asking for a decision, and the audio limit only
// binds clients that ask. Downloads use /media/download.
//
// Every request is checked, not only a playback's first: a later Range
// request is as easy to send as the first. Each user's limits and the
// internet policy are reused for gateMemoTTL so a range-heavy play costs a
// map read, not a query per request.
type DirectPlayGate struct {
	caps   UserStreamCapsReader
	policy RemoteStreamPolicyReader

	mu      sync.Mutex
	users   map[uuid.UUID]gateCapsEntry
	pol     RemoteStreamPolicy
	polAt   time.Time
	nowFunc func() time.Time // overridable in tests
}

type gateCapsEntry struct {
	caps StreamCaps
	at   time.Time
}

// gateMemoTTL bounds how stale a user's limits or the internet policy can
// be on the direct stream route: a change applies to the next play within
// seconds.
const gateMemoTTL = 10 * time.Second

// NewDirectPlayGate builds the gate. A nil caps reader leaves only the
// damaged-file check; a nil policy reader, no internet limit.
func NewDirectPlayGate(caps UserStreamCapsReader, policy RemoteStreamPolicyReader) *DirectPlayGate {
	return &DirectPlayGate{
		caps:    caps,
		policy:  policy,
		users:   map[uuid.UUID]gateCapsEntry{},
		nowFunc: time.Now,
	}
}

// Blocks writes the refusal and returns true when user may not stream file
// directly from this request's client address.
func (g *DirectPlayGate) Blocks(w http.ResponseWriter, r *http.Request, user uuid.UUID, file *media.File) bool {
	if g == nil || file == nil || file.VideoCodec == nil || *file.VideoCodec == "" {
		return false
	}
	if file.IntegrityStatus == media.IntegrityDamaged {
		respond.Error(w, r, http.StatusUnprocessableEntity, "FILE_DAMAGED",
			"this file is damaged and can't be played")
		return true
	}
	ctx := r.Context()
	caps := g.userCaps(ctx, user)
	policy := g.remotePolicy(ctx)
	bc := effectiveStreamCap(caps, policy, !policy.IsLocal(audit.ClientIP(r)), true)
	if _, over := remoteDecisionOverride(bc, file); over ||
		caps.Quality.overQuality(file) || caps.Quality.overResolution(file) {
		respond.Error(w, r, http.StatusConflict, codeTranscodeRequired,
			"this video has to be converted for you; play it through the server")
		return true
	}
	return false
}

func (g *DirectPlayGate) userCaps(ctx context.Context, user uuid.UUID) StreamCaps {
	if g.caps == nil {
		return StreamCaps{}
	}
	now := g.nowFunc()
	g.mu.Lock()
	e, ok := g.users[user]
	g.mu.Unlock()
	if ok && now.Sub(e.at) < gateMemoTTL {
		return e.caps
	}
	c, err := g.caps.GetStreamCaps(ctx, user)
	if err != nil {
		// Fail open, as Start does: the caps read failing must not stop
		// every direct play.
		return StreamCaps{}
	}
	g.mu.Lock()
	if len(g.users) > 4096 {
		g.users = map[uuid.UUID]gateCapsEntry{}
	}
	g.users[user] = gateCapsEntry{caps: c, at: now}
	g.mu.Unlock()
	return c
}

func (g *DirectPlayGate) remotePolicy(ctx context.Context) RemoteStreamPolicy {
	now := g.nowFunc()
	g.mu.Lock()
	if !g.polAt.IsZero() && now.Sub(g.polAt) < gateMemoTTL {
		p := g.pol
		g.mu.Unlock()
		return p
	}
	g.mu.Unlock()
	p := readRemotePolicy(ctx, g.policy)
	g.mu.Lock()
	g.pol, g.polAt = p, now
	g.mu.Unlock()
	return p
}
