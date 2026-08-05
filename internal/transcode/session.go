package transcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/onscreen/onscreen/internal/observability"
	"github.com/onscreen/onscreen/internal/valkey"
)

// ErrUserAtCap is returned by CreateWithUserCap when the user is already at or
// above their concurrent-session cap at the moment of the atomic write. The
// API layer maps it to 429.
var ErrUserAtCap = errors.New("user at concurrent-session cap")

const (
	sessionTTL    = 4 * time.Hour
	heartbeatTTL  = 10 * time.Second
	workerTTL     = 15 * time.Second
	workerRefresh = 5 * time.Second

	// Index sets — O(active_sessions) instead of O(total_keys) SCAN.
	sessionIndexKey = "transcode:sessions" // Set of active session IDs
	workerIndexKey  = "transcode:workers"  // Set of active worker IDs
)

// Session represents an active transcode or direct-stream session.
type Session struct {
	ID          string    `json:"id"`
	UserID      uuid.UUID `json:"user_id"`
	MediaItemID uuid.UUID `json:"media_item_id"`
	FileID      uuid.UUID `json:"file_id"`
	WorkerID    string    `json:"worker_id"`
	WorkerAddr  string    `json:"worker_addr"`
	Decision    string    `json:"decision"` // "directPlay"|"directStream"|"transcode"
	FilePath    string    `json:"file_path"`
	// SourceURL is an HTTP fallback the worker reads when FilePath isn't
	// reachable on its own filesystem (a remote worker with no shared
	// storage). It points at this server's /media/stream/{file_id} with a
	// 24 h stream token, so the worker pulls the source over the LAN.
	// Embedded / shared-storage workers ignore it (FilePath wins). See
	// transcode_abr.go buildSourceURL.
	SourceURL      string    `json:"source_url,omitempty"`
	PositionMS     int64     `json:"position_ms"`
	CreatedAt      time.Time `json:"created_at"`
	LastActivityAt time.Time `json:"last_activity_at,omitempty"`
	ClientName     string    `json:"client_name,omitempty"`
	ClientID       string    `json:"client_id,omitempty"`
	SegToken       string    `json:"seg_token,omitempty"`
	BitrateKbps    int       `json:"bitrate_kbps,omitempty"`
	HEVCOutput     bool      `json:"hevc_output,omitempty"` // true = fMP4 segments (.m4s) with hvc1 codec
	AV1Output      bool      `json:"av1_output,omitempty"`  // true = fMP4 segments (.m4s) with av01 codec; either fMP4 flag selects the .m4s wait path
	// Source codec markers (distinct from the *Output flags, which are the
	// chosen OUTPUT codec). Carried so ABR rung children can set job.IsHEVC /
	// job.IsAV1 — the worker keys hardware decode (AV1 NVDEC, opt-in QSV HEVC)
	// off the SOURCE codec, and rung-child jobs would otherwise lose it.
	SourceIsHEVC bool `json:"source_is_hevc,omitempty"`
	SourceIsAV1  bool `json:"source_is_av1,omitempty"`
	SourceIsH264 bool `json:"source_is_h264,omitempty"`

	// ── Adaptive-bitrate (on-demand ladder) ───────────────────────────────
	// On an ABR PARENT session, ABR=true and the fields below describe the
	// ladder + what the per-rung child sessions need. The parent runs no
	// ffmpeg itself; playlist.m3u8 serves a master listing the rungs, and
	// each rung is a CHILD session ({parent}-r{rung}) transcoded on demand
	// by the segment handler. See transcode_abr.go.
	ABR              bool        `json:"abr,omitempty"`
	ABRRenditions    []Rendition `json:"abr_renditions,omitempty"`
	DurationMS       int64       `json:"duration_ms,omitempty"`
	AudioStreamIndex int         `json:"audio_stream_index,omitempty"`
	// AudioChannels is the AAC output channel count for the ABR ladder's
	// children (preserves the source 5.1/7.1 instead of downmixing to stereo).
	AudioChannels int  `json:"audio_channels,omitempty"`
	NeedsToneMap  bool `json:"needs_tone_map,omitempty"`
	// FrameRate (parent ABR session) is the source fps. The predicted
	// variant playlist and each child's restart offset are quantized to the
	// frame ffmpeg actually forces a keyframe on (ceil(i*SegmentDuration*fps)
	// /fps), so the advertised timeline matches the encoded one to <1 frame
	// regardless of fps. Zero falls back to a flat SegmentDuration grid.
	FrameRate float64 `json:"frame_rate,omitempty"`
	// StartSeg is set on CHILD rung sessions: the global segment index this
	// child's ffmpeg began encoding at. The segment handler maps a requested
	// global index to the child-local file as local = global - StartSeg.
	StartSeg int `json:"start_seg,omitempty"`
	// ParentID links a CHILD rung session back to its ABR parent. Set only on
	// children; the /sessions listing filters these out so an ABR stream shows
	// as one card (the parent) rather than one per rung.
	ParentID string `json:"parent_id,omitempty"`
	// Incarnation counts how many times this session ID has been (re)started.
	//
	// An ABR rung child is restarted IN PLACE — same session ID, new ffmpeg at
	// a new offset — on every backward seek and forward-seek recovery. Reusing
	// the ID meant every incarnation shared one output directory and one Valkey
	// key, which broke two ways at once: the superseded job's deferred cleanup
	// deleted the SUCCESSOR's live segments 30 s later, and a remote worker's
	// watchdog (which only checks "does the session still exist?") saw the
	// recreated key and happily kept the old ffmpeg running, so two encoders
	// wrote conflicting seg00000 files into one directory.
	//
	// Bumping this on every restart gives each incarnation its own directory
	// (see SessionDirName) and lets the watchdog recognise that it has been
	// superseded. Zero — every non-ABR session and every first start — keeps
	// the historical bare-session-ID directory, so this is inert everywhere
	// except a rung restart.
	Incarnation int `json:"incarnation,omitempty"`
	// SelectedRendition is the rung label ("720p") the client most recently
	// pulled a segment for, recorded on the PARENT. Surfaced as
	// selected_rendition on /api/v1/sessions so operators see which bitrate
	// the player's ABR logic actually settled on.
	SelectedRendition string `json:"selected_rendition,omitempty"`
}

// VideoOutput reads the two *Output flags back as the container question they
// actually encode. Use this rather than testing the flags by hand: the API
// predicts segment names from it while the worker WRITES those segments from
// ResolveVideoOutput, and the two only stay in step because both sides go
// through one type. Hand-rolled `HEVCOutput || AV1Output` tests are how they
// drifted apart before. See videooutput.go.
//
// Authoritative only AFTER the worker has stamped the session
// (SetWorkerInfo). Before that these flags hold the API's client-preference
// guess, which is what the ForceFMP4 contract exists to reconcile.
func (s *Session) VideoOutput() VideoOutput {
	return VideoOutput{HEVC: s.HEVCOutput, AV1: s.AV1Output}
}

// WorkerRegistration is the record a transcode worker writes to Valkey.
type WorkerRegistration struct {
	ID             string            `json:"id"`
	Addr           string            `json:"addr"`              // "host:port" of the worker HTTP server
	NodeID         string            `json:"node_id,omitempty"` // worker's NODE_ID (hostname default) — keys its per-node config row
	Capabilities   []string          `json:"capabilities"`
	EncoderLabels  map[string]string `json:"encoder_labels,omitempty"` // encoder → human label (e.g. "h264_nvenc" → "NVIDIA GeForce RTX 5080")
	MaxSessions    int               `json:"max_sessions"`
	ActiveSessions int               `json:"active_sessions"`
	// ActiveCostCenti is the summed cost of in-flight jobs in centi-units of a
	// 1080p transcode (1080p ≈ 100, 4K ≈ 400, remux ≈ 25). Drives weighted
	// dispatch so a worker chewing on a single 4K stream is deprioritized far
	// more than its raw ActiveSessions count would suggest. See JobCostCenti.
	ActiveCostCenti int `json:"active_cost_centi,omitempty"`
	// HasGPUTonemap is true when the worker has a GPU HDR→SDR tonemap path
	// (libplacebo/Vulkan, tonemap_cuda, or tonemap_opencl). The dispatcher
	// prefers such a worker for HDR jobs so 4K HDR doesn't land on a node that
	// can only software-tonemap (zscale), which can't sustain 4K. Filter
	// capabilities otherwise stay local to each worker; this one is registered
	// because it's the highest-value routing signal. See JobNeeds.
	HasGPUTonemap bool      `json:"has_gpu_tonemap,omitempty"`
	RegisteredAt  time.Time `json:"registered_at"`
}

// SessionStore manages transcode sessions in Valkey.
type SessionStore struct {
	v       *valkey.Client
	metrics *observability.Metrics
}

// NewSessionStore creates a SessionStore backed by the given Valkey client.
func NewSessionStore(v *valkey.Client) *SessionStore {
	return &SessionStore{v: v}
}

// WithMetrics enables the onscreen_transcode_sessions_active gauge, refreshed
// from the live Valkey session index after each create/delete (accurate across
// instances; nil is a no-op).
func (s *SessionStore) WithMetrics(m *observability.Metrics) *SessionStore {
	s.metrics = m
	return s
}

// refreshActiveGauge sets the active-sessions gauge to the live index count.
// Reading SCARD keeps it correct under multi-instance dispatch and TTL expiry,
// rather than drifting like a local inc/dec would.
func (s *SessionStore) refreshActiveGauge(ctx context.Context) {
	if s.metrics == nil {
		return
	}
	if n, err := s.v.Raw().SCard(ctx, sessionIndexKey).Result(); err == nil {
		s.metrics.TranscodeActive.Set(float64(n))
	}
}

// Create stores a new session. TTL is sessionTTL (4 hours).
func (s *SessionStore) Create(ctx context.Context, sess Session) error {
	b, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	if err := s.v.Set(ctx, sessionKey(sess.ID), string(b), sessionTTL); err != nil {
		return err
	}
	// Add to session index set for O(1) listing.
	s.v.Raw().SAdd(ctx, sessionIndexKey, sess.ID)
	s.refreshActiveGauge(ctx)
	return nil
}

// CreateWithUserCap atomically enforces the per-user concurrent-session cap at
// write time, closing the check-then-act race in the Start handler: two Start
// requests that each read "4 < 5" via CountByUser would both proceed and leave
// the user with 6 sessions. Here the count + create run under a short per-user
// lock, so only one writer occupies the (count→decide→write) window at a time.
//
// maxPerUser <= 0 disables the cap (plain Create). The lock is held only across
// two fast Valkey round-trips, and is per-user, so it never serializes the
// fleet — only a single account's simultaneous Start spam. The handler still
// does a cheap CountByUser pre-check before the heavy planning work; this is
// the authoritative backstop, not the first line of defense.
//
// Lock acquisition spins briefly. On Valkey error or (pathological) lock
// timeout it falls open to a plain Create and logs nothing here — the caller's
// 10/min Start rate limit is the remaining backstop, and hard-blocking a
// legitimate near-simultaneous two-device start would be worse than a rare
// one-over overshoot.
func (s *SessionStore) CreateWithUserCap(ctx context.Context, sess Session, maxPerUser int) error {
	if maxPerUser <= 0 {
		return s.Create(ctx, sess)
	}

	lockKey := "transcode:caplock:" + sess.UserID.String()
	acquired := false
	// ~2s budget (40 × 50 ms). The critical section is two Valkey ops, so even
	// a full rate-limited burst of same-user starts clears well inside this.
	for i := 0; i < 40; i++ {
		ok, err := s.v.SetNX(ctx, lockKey, "1", 5*time.Second)
		if err != nil {
			break // Valkey degraded — fall open rather than block playback.
		}
		if ok {
			acquired = true
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		time.Sleep(50 * time.Millisecond)
	}
	if acquired {
		defer func() { _ = s.v.Del(ctx, lockKey) }()
		active, err := s.CountByUser(ctx, sess.UserID)
		if err == nil && active >= maxPerUser {
			return ErrUserAtCap
		}
	}
	return s.Create(ctx, sess)
}

// Get retrieves a session by ID.
func (s *SessionStore) Get(ctx context.Context, id string) (*Session, error) {
	raw, err := s.v.Get(ctx, sessionKey(id))
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	var sess Session
	if err := json.Unmarshal([]byte(raw), &sess); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	return &sess, nil
}

// Delete removes a session.
func (s *SessionStore) Delete(ctx context.Context, id string) error {
	s.v.Raw().SRem(ctx, sessionIndexKey, id)
	err := s.v.Del(ctx, sessionKey(id))
	s.refreshActiveGauge(ctx)
	return err
}

// List returns all active sessions using the session index set (O(active_sessions)).
func (s *SessionStore) List(ctx context.Context) ([]Session, error) {
	ids, err := s.v.Raw().SMembers(ctx, sessionIndexKey).Result()
	if err != nil {
		return nil, fmt.Errorf("list session index: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = sessionKey(id)
	}

	pipe := s.v.Raw().Pipeline()
	cmds := make([]*redis.StringCmd, len(keys))
	for i, k := range keys {
		cmds[i] = pipe.Get(ctx, k)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("pipeline get sessions: %w", err)
	}

	sessions := make([]Session, 0, len(keys))
	for i, cmd := range cmds {
		raw, err := cmd.Result()
		if err != nil {
			// Session key expired but index entry lingers — clean up.
			s.v.Raw().SRem(ctx, sessionIndexKey, ids[i])
			continue
		}
		var sess Session
		if err := json.Unmarshal([]byte(raw), &sess); err == nil {
			sessions = append(sessions, sess)
		}
	}
	return sessions, nil
}

// ListByUserItem returns active sessions belonging to userID for mediaItemID.
// Used to enforce last-writer-wins semantics on Start: a fresh transcode
// request from the same user on the same item supersedes any prior session
// the user had for it (matches Plex/Jellyfin behavior — a new device taking
// over kills the old playback).
func (s *SessionStore) ListByUserItem(ctx context.Context, userID, mediaItemID uuid.UUID) ([]Session, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, 1)
	for _, sess := range all {
		if sess.UserID == userID && sess.MediaItemID == mediaItemID {
			out = append(out, sess)
		}
	}
	return out, nil
}

// errSkipSessionMutation tells mutateSession the caller decided not to write.
var errSkipSessionMutation = errors.New("skip session mutation")

// mutateSession applies fn to a session under an optimistic lock, retrying if
// another writer changed the key in between.
//
// The session is one JSON blob, and four call sites used to read it, mutate one
// field, and write the whole thing back with no synchronisation at all — so any
// two overlapping writers silently discarded one of the two updates. The
// damaging pairing is TouchActivity (every ~150ms while a rung child starts)
// racing SetWorkerInfo (once, ~1s in, from the worker that claimed the job): a
// touch that read the blob before the stamp and wrote after it reset
// WorkerAddr to "". The API then has no worker to proxy segments to, so on a
// multi-worker deployment every segment for that session 404s until the client
// gives up — from a race that leaves no error behind.
//
// WATCH/MULTI/EXEC is the right tool: EXEC aborts if the key changed after the
// WATCH, so the loser re-reads and re-applies rather than clobbering. fn may
// return errSkipSessionMutation to abort the write cleanly (used by
// SetSelectedRendition's no-op fast path).
func (s *SessionStore) mutateSession(ctx context.Context, sessionID string, fn func(*Session) error) error {
	key := sessionKey(sessionID)
	const maxAttempts = 5

	txn := func(tx *redis.Tx) error {
		raw, err := tx.Get(ctx, key).Result()
		if err != nil {
			return err
		}
		var sess Session
		if err := json.Unmarshal([]byte(raw), &sess); err != nil {
			return fmt.Errorf("unmarshal session: %w", err)
		}
		if err := fn(&sess); err != nil {
			return err
		}
		b, err := json.Marshal(sess)
		if err != nil {
			return fmt.Errorf("marshal session: %w", err)
		}
		// Every successful mutation refreshes the TTL to the full window,
		// making sessionTTL an IDLE timeout rather than an absolute lifetime.
		// It used to preserve the remaining TTL, which hard-killed active
		// playback at 4 h wall-clock: the key expired mid-stream, the worker's
		// watchdog read not-found and shot the ffmpeg, and anything longer
		// than the TTL — a film marathon, a long pause partway — died at the
		// stroke of the clock. Activity stamps (TouchActivity on every segment
		// fetch, SetSelectedRendition on ABR) flow through here, so a watched
		// session never expires and an abandoned one still ages out sessionTTL
		// after its last write — the same lingering the old behavior had.
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, string(b), sessionTTL)
			return nil
		})
		return err
	}

	// go-redis surfaces a lost optimistic lock as redis.TxFailedErr: another
	// writer touched the key between our WATCH and EXEC, so nothing was
	// written and we re-read and re-apply. Bounded and iterative — these
	// writers all complete in microseconds, so sustained contention means
	// something is wrong rather than that we should keep spinning.
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		err = s.v.Raw().Watch(ctx, txn, key)
		if !errors.Is(err, redis.TxFailedErr) {
			return err
		}
	}
	return err
}

// TouchActivity stamps LastActivityAt = now on the session, signalling
// that the client is still actively consuming. Called by the segment-
// fetch endpoint so the worker's idle-kill path can distinguish "client
// is watching" from "client crashed; keep encoding for nothing." Cheap
// fire-and-forget update — silent no-op if the session is gone.
//
// Position is preserved as-is. UpdatePosition* writes the player's
// reported position from a separate progress beacon; this just stamps
// the activity timestamp from segment-fetch traffic, which is more
// reliable (segments fetch every ~4s; progress beacons every ~5s, but
// progress also fires from non-watching tabs).
func (s *SessionStore) TouchActivity(ctx context.Context, sessionID string) {
	_ = s.mutateSession(ctx, sessionID, func(sess *Session) error {
		sess.LastActivityAt = time.Now()
		return nil
	})
}

// ActiveSessionWindow is how long since the last segment-fetch / progress
// heartbeat a session still counts as "live" for the per-user concurrency cap.
// Sessions quieter than this are treated as abandoned — the client crashed, the
// TV was powered off mid-stream, or a DELETE never fired — and their ffmpeg has
// already idle-exited, so they hold no GPU slot and must not hold a cap slot
// either. Mirrors the api/v1 "Now Playing" display window so "live" means the
// same thing in both places.
const ActiveSessionWindow = 2 * time.Minute

// CountByUser returns the number of LIVE sessions for the given user across all
// media items — those with activity within ActiveSessionWindow. Used by the
// per-user concurrency cap so a single account can't pin every GPU/CPU slot by
// spamming Start with different item IDs (each one bypasses supersedeUserItem
// because the match key is (user, item), not (user)).
//
// Stale/abandoned sessions are deliberately NOT counted. Their Valkey entry
// lingers for the full sessionTTL (4 h), but the ffmpeg behind it has idle-
// exited (no GPU held). Counting them would falsely lock a user out at the cap
// — "you already have 5 active streams" while they're watching nothing — for up
// to 4 h, with no way to see or kill the phantom sessions. A brand-new session
// that hasn't fetched its first segment yet has no LastActivityAt, so we fall
// back to CreatedAt: rapid Start-spam is still capped (all freshly created).
func (s *SessionStore) CountByUser(ctx context.Context, userID uuid.UUID) (int, error) {
	all, err := s.List(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	n := 0
	for _, sess := range all {
		if sess.UserID != userID {
			continue
		}
		// ABR rung children are INTERNAL sessions: one playback = one parent
		// + one child per rung the player has touched, all under the same
		// user. Counting them made a single ABR stream consume two or more of
		// the user's concurrency slots — with the default cap of 5, two ABR
		// playbacks plus their active rungs could brick the account with
		// "you already have 5 active streams". One playback counts once: the
		// parent.
		if sess.ParentID != "" {
			continue
		}
		activeAt := sess.LastActivityAt
		if activeAt.IsZero() {
			activeAt = sess.CreatedAt
		}
		if now.Sub(activeAt) <= ActiveSessionWindow {
			n++
		}
	}
	return n, nil
}

// DeleteByMedia removes the given USER's sessions for a media item.
//
// The userID is not decoration. This is reached from the progress beacon
// (PUT /items/{id}/progress with state=stopped), whose only authorization is
// that the caller can READ the item — and libraries are shared by default.
// Scoped on the item alone, any household profile stopping playback deleted
// every other viewer's session for the same title; the worker's heartbeat then
// saw the key gone and killed their ffmpeg within ~2s. The dedicated Stop
// endpoint already refuses this with a 403 when sess.UserID != claims.UserID,
// and transcode_handler_test.go asserts "other user's session must NOT be
// superseded" — the invariant was enforced one route over and skipped here.
// Called by the progress endpoint on "stopped" to clean up even if the client
// never explicitly hits the Stop endpoint (e.g. tab closed after playback ends).
// It returns the sessions it deleted so the caller can release any API-side
// state keyed to them — the progress-beacon stop is the one deletion path that
// does not go through the transcode handler's tearDown, and before this it
// leaked the per-rung child locks of every ABR parent it removed.
func (s *SessionStore) DeleteByMedia(ctx context.Context, userID, mediaItemID uuid.UUID) ([]Session, error) {
	ids, err := s.v.Raw().SMembers(ctx, sessionIndexKey).Result()
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = sessionKey(id)
	}
	pipe := s.v.Raw().Pipeline()
	cmds := make([]*redis.StringCmd, len(keys))
	for i, k := range keys {
		cmds[i] = pipe.Get(ctx, k)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, err
	}
	var deleted []Session
	for i, cmd := range cmds {
		raw, err := cmd.Result()
		if err != nil {
			s.v.Raw().SRem(ctx, sessionIndexKey, ids[i])
			continue
		}
		var sess Session
		if err := json.Unmarshal([]byte(raw), &sess); err != nil {
			continue
		}
		if sess.MediaItemID == mediaItemID && sess.UserID == userID {
			s.v.Raw().SRem(ctx, sessionIndexKey, ids[i])
			_ = s.v.Del(ctx, keys[i])
			deleted = append(deleted, sess)
		}
	}
	return deleted, nil
}

// UpdatePositionByMedia finds the active session for the given media item and
// updates its PositionMS. Silently no-ops if no matching session exists.
//
// NOTE: concurrent position updates for the same session may race (lost update).
// The write goes through mutateSession (WATCH/MULTI/EXEC): this used to be
// the one remaining raw read-modify-write of the whole session blob, and a
// beacon racing the worker's SetWorkerInfo could resurrect the pre-stamp
// session — wiping WorkerAddr/HEVCOutput exactly the way the A8 race did
// before the other mutators were converted. Only the match SCAN is
// non-transactional; the mutation itself re-reads under WATCH.
func (s *SessionStore) UpdatePositionByMedia(ctx context.Context, userID, mediaItemID uuid.UUID, positionMS int64) error {
	ids, err := s.v.Raw().SMembers(ctx, sessionIndexKey).Result()
	if err != nil || len(ids) == 0 {
		return err
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = sessionKey(id)
	}
	pipe := s.v.Raw().Pipeline()
	cmds := make([]*redis.StringCmd, len(keys))
	for i, k := range keys {
		cmds[i] = pipe.Get(ctx, k)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return err
	}
	for i, cmd := range cmds {
		raw, err := cmd.Result()
		if err != nil {
			s.v.Raw().SRem(ctx, sessionIndexKey, ids[i])
			continue
		}
		var sess Session
		if err := json.Unmarshal([]byte(raw), &sess); err != nil {
			continue
		}
		// Owner-scoped for the same reason as DeleteByMedia: without it one
		// viewer's beacon rewrote another viewer's reported position and
		// refreshed their LastActivityAt, which anchors the idle-kill timer.
		if sess.MediaItemID != mediaItemID || sess.UserID != userID {
			continue
		}
		_ = s.mutateSession(ctx, ids[i], func(fresh *Session) error {
			// Re-verify under WATCH — the session may have been superseded or
			// reassigned between the scan and this transaction.
			if fresh.MediaItemID != mediaItemID || fresh.UserID != userID {
				return errSkipSessionMutation
			}
			fresh.PositionMS = positionMS
			fresh.LastActivityAt = time.Now()
			return nil
		})
		break
	}
	return nil
}

// SetWorkerInfo stamps the session with the worker ID and address that claimed
// the job. The API uses WorkerAddr to proxy segment requests to the correct
// worker in multi-instance deployments.
func (s *SessionStore) SetWorkerInfo(ctx context.Context, sessionID, workerID, workerAddr string, hevcOutput, av1Output bool) error {
	return s.mutateSession(ctx, sessionID, func(sess *Session) error {
		sess.WorkerID = workerID
		sess.WorkerAddr = workerAddr
		sess.HEVCOutput = hevcOutput
		sess.AV1Output = av1Output
		return nil
	})
}

// SetSelectedRendition records on an ABR PARENT session which rung the client
// most recently fetched a segment for, and refreshes the parent's activity so
// its /sessions card stays live while playback rides on rung children. A no-op
// when the rung is unchanged and the activity stamp is recent, to skip a Valkey
// write on the common steady-state segment-by-segment case.
func (s *SessionStore) SetSelectedRendition(ctx context.Context, sessionID, rungLabel string) {
	_ = s.mutateSession(ctx, sessionID, func(sess *Session) error {
		if sess.SelectedRendition == rungLabel && time.Since(sess.LastActivityAt) < 2*time.Second {
			return errSkipSessionMutation
		}
		sess.SelectedRendition = rungLabel
		sess.LastActivityAt = time.Now()
		return nil
	})
}

// SetHeartbeat writes/refreshes the session heartbeat key (2s TTL reset to 10s).
// Called by the worker every 2 seconds while an FFmpeg process is active.
func (s *SessionStore) SetHeartbeat(ctx context.Context, id string) error {
	return s.v.Set(ctx, heartbeatKey(id), "1", heartbeatTTL)
}

// IsAlive returns true if the session heartbeat is still valid.
func (s *SessionStore) IsAlive(ctx context.Context, id string) (bool, error) {
	_, err := s.v.Get(ctx, heartbeatKey(id))
	if err == valkey.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// RegisterWorker writes a worker registration record to Valkey with workerTTL.
func (s *SessionStore) RegisterWorker(ctx context.Context, reg WorkerRegistration) error {
	b, err := json.Marshal(reg)
	if err != nil {
		return fmt.Errorf("marshal worker: %w", err)
	}
	if err := s.v.Set(ctx, workerKey(reg.ID), string(b), workerTTL); err != nil {
		return err
	}
	s.v.Raw().SAdd(ctx, workerIndexKey, reg.ID)
	return nil
}

// ListWorkers returns all registered workers.
func (s *SessionStore) ListWorkers(ctx context.Context) ([]WorkerRegistration, error) {
	ids, err := s.v.Raw().SMembers(ctx, workerIndexKey).Result()
	if err != nil {
		return nil, fmt.Errorf("list worker index: %w", err)
	}
	var workers []WorkerRegistration
	for _, id := range ids {
		raw, err := s.v.Get(ctx, workerKey(id))
		if err != nil {
			// Worker key expired — clean up stale index entry.
			s.v.Raw().SRem(ctx, workerIndexKey, id)
			continue
		}
		var reg WorkerRegistration
		if err := json.Unmarshal([]byte(raw), &reg); err == nil {
			workers = append(workers, reg)
		}
	}
	return workers, nil
}

// EnqueueJob pushes a transcode job onto the global Valkey queue.
// Prefer DispatchJob which routes to the best available worker.
func (s *SessionStore) EnqueueJob(ctx context.Context, job TranscodeJob) error {
	b, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}
	return s.v.Raw().RPush(ctx, "transcode:queue", string(b)).Err()
}

// DispatchJob selects the best available worker and pushes the job to its
// per-worker queue. GPU-capable workers are preferred, then the worker with
// the most available capacity is chosen. Falls back to the global queue when
// no workers are registered (e.g. embedded-only mode).
//
// A Valkey counter (transcode:dispatched:{addr}) tracks jobs dispatched but
// not yet started by the worker. This prevents stale-heartbeat over-dispatch:
// even though the heartbeat ActiveSessions updates every 5s, the dispatch
// counter is incremented atomically here and decremented by the worker when
// it starts processing the job.
func (s *SessionStore) DispatchJob(ctx context.Context, job TranscodeJob) (string, error) {
	workers, err := s.ListWorkers(ctx)
	if err != nil || len(workers) == 0 {
		eqErr := s.EnqueueJob(ctx, job)
		s.recordJob("queued", eqErr)
		return "", eqErr
	}

	// Read each worker's in-flight dispatch counters (count + cost) and fold
	// them in, so back-to-back dispatches before the next heartbeat don't all
	// pile onto the same worker.
	for i := range workers {
		if n, err := s.v.Raw().Get(ctx, dispatchCounterKey(workers[i].Addr)).Int(); err == nil {
			workers[i].ActiveSessions += n
		}
		if c, err := s.v.Raw().Get(ctx, dispatchCostKey(workers[i].Addr)).Int(); err == nil {
			workers[i].ActiveCostCenti += c
		}
	}

	best := selectWorker(workers, jobNeeds(job))
	job.CostCenti = JobCostCenti(job.Width, job.Height, job.Decision)
	b, err := json.Marshal(job)
	if err != nil {
		s.recordJob("error", err)
		return "", fmt.Errorf("marshal job: %w", err)
	}
	// Atomically bump both in-flight counters, then push job. The worker
	// decrements them (AckDispatch) once it picks the job up.
	s.v.Raw().Incr(ctx, dispatchCounterKey(best.Addr))
	s.v.Raw().Expire(ctx, dispatchCounterKey(best.Addr), workerTTL)
	s.v.Raw().IncrBy(ctx, dispatchCostKey(best.Addr), int64(job.CostCenti))
	s.v.Raw().Expire(ctx, dispatchCostKey(best.Addr), workerTTL)
	pushErr := s.v.Raw().RPush(ctx, workerQueueKey(best.Addr), string(b)).Err()
	s.recordJob("dispatched", pushErr)
	return best.Addr, pushErr
}

// recordJob increments onscreen_transcode_jobs_total. A non-nil err overrides
// the status with "error" so failures are visible regardless of the path.
func (s *SessionStore) recordJob(status string, err error) {
	if s.metrics == nil {
		return
	}
	if err != nil {
		status = "error"
	}
	s.metrics.TranscodeJobsTotal.WithLabelValues(status).Inc()
}

// AckDispatch decrements the in-flight dispatch counters when a worker starts
// processing a job. Called by the worker after BLPOP returns a job from its
// queue; costCenti is the job's stamped CostCenti.
func (s *SessionStore) AckDispatch(ctx context.Context, workerAddr string, costCenti int) {
	key := dispatchCounterKey(workerAddr)
	if val, err := s.v.Raw().Decr(ctx, key).Result(); err == nil && val <= 0 {
		s.v.Raw().Del(ctx, key)
	}
	if costCenti <= 0 {
		return
	}
	ck := dispatchCostKey(workerAddr)
	if val, err := s.v.Raw().DecrBy(ctx, ck, int64(costCenti)).Result(); err == nil && val <= 0 {
		s.v.Raw().Del(ctx, ck)
	}
}

func dispatchCounterKey(addr string) string {
	return "transcode:dispatched:" + addr
}

func dispatchCostKey(addr string) string {
	return "transcode:dispatched-cost:" + addr
}

// DequeueJob blocks up to timeout waiting for a job. Checks the worker's own
// per-worker queue first, then the shared global queue as a fallback.
// Returns (nil, nil) on timeout.
func (s *SessionStore) DequeueJob(ctx context.Context, workerAddr string, timeout time.Duration) (*TranscodeJob, error) {
	keys := []string{workerQueueKey(workerAddr), "transcode:queue"}
	results, err := s.v.Raw().BLPop(ctx, timeout, keys...).Result()
	if err == redis.Nil {
		return nil, nil // timeout, no job
	}
	if err != nil {
		return nil, fmt.Errorf("blpop: %w", err)
	}
	if len(results) < 2 {
		return nil, nil
	}
	var job TranscodeJob
	if err := json.Unmarshal([]byte(results[1]), &job); err != nil {
		return nil, fmt.Errorf("unmarshal job: %w", err)
	}
	return &job, nil
}

func workerQueueKey(addr string) string {
	return "transcode:queue:worker:" + addr
}

// JobNeeds is the set of capability requirements the dispatcher matches against
// worker registrations, derived from a TranscodeJob's intent flags. A worker
// that satisfies a need is strongly preferred (capMatchBonus), but a job is
// never blocked from a non-matching worker: every worker self-resolves a working
// path (software zscale tonemap, HEVC instead of AV1, software decode), just
// more slowly. So this routes for speed/quality, not correctness.
type JobNeeds struct {
	ToneMap    bool // HDR→SDR tonemap — prefer a GPU-tonemap node over software zscale
	PreferAV1  bool // AV1 output requested — prefer a node with an AV1 encoder
	PreferHEVC bool // HEVC output requested — prefer a node with an HEVC encoder
}

func jobNeeds(j TranscodeJob) JobNeeds {
	return JobNeeds{ToneMap: j.NeedsToneMap, PreferAV1: j.PreferAV1, PreferHEVC: j.PreferHEVC}
}

// selectWorker picks the best worker for a new job. Workers are ranked in strict
// tiers: GPU availability first, then capability fit for this specific job
// (GPU tonemap for HDR, an AV1/HEVC encoder for AV1/HEVC output), then the most
// proportional headroom (lowest cost-weighted load), with absolute headroom
// breaking final ties.
func selectWorker(workers []WorkerRegistration, needs JobNeeds) WorkerRegistration {
	best := workers[0]
	bestScore := workerScore(best, needs)
	for _, w := range workers[1:] {
		s := workerScore(w, needs)
		if s > bestScore {
			bestScore = s
			best = w
		}
	}
	return best
}

const (
	// Dispatch score tiers, highest priority first — each strictly dominates
	// every lower tier, so the ordering is: GPU availability, then capability
	// fit for the job, then proportional load.
	//
	// gpuScoreBonus: a GPU worker always outranks a CPU-only one.
	gpuScoreBonus = 1_000_000_000_000 // 1e12
	// capMatchBonus: added once per job-capability a worker satisfies. Stacks,
	// but a handful of matches stays far below gpuScoreBonus.
	capMatchBonus = 1_000_000_000 // 1e9
	// loadScoreUnit weights the proportional-headroom term (per-mille of budget
	// free, 0–1000); the whole load term stays under capMatchBonus.
	loadScoreUnit = 100_000
)

// FleetCanEncode reports whether any registered worker advertises an encoder
// for the given codec family ("hevc", "av1") — hasEncoderPrefix does the
// per-worker check ("av1" → av1_nvenc/av1_qsv/av1_amf/av1_vaapi, "hevc" →
// hevc_*).
//
// The ABR ladder picks its codec from CLIENT capability alone, then the master
// playlist advertises CODECS and the variant playlist names .m4s segments. If
// no node can actually encode that family the worker silently falls back to
// H.264 and the advertisement was a lie. Asking the fleet first makes the
// common case — a CPU-only worker, whose encoder list is just [libx264] —
// correct at the point of decision rather than papered over downstream.
//
// A registry read error, or an empty fleet, reports false: refusing to promise
// what we cannot confirm degrades to an H.264 ladder, which every node can
// serve. Note this is a point-in-time answer; the ForceFMP4 container contract
// is what covers a fleet that changes mid-session, or a per-file fallback like
// a rotated source that no capability check can predict.
func (s *SessionStore) FleetCanEncode(ctx context.Context, codec string) bool {
	workers, err := s.ListWorkers(ctx)
	if err != nil || len(workers) == 0 {
		return false
	}
	for _, w := range workers {
		if hasEncoderPrefix(w.Capabilities, codec) {
			return true
		}
	}
	return false
}

func hasEncoderPrefix(caps []string, codec string) bool {
	p := codec + "_"
	for _, c := range caps {
		if strings.HasPrefix(c, p) {
			return true
		}
	}
	return false
}

// JobCostCenti estimates a job's load in centi-units of a 1080p transcode
// (1080p ≈ 100). A 4K transcode is ~400, 720p ~44, and remux/direct decisions
// are a small fixed cost since they do no scaling/encoding work. Used to weight
// worker capacity so one 4K stream isn't accounted the same as one 480p stream.
//
// The decision string here is the job-level value set in the transcode handler
// ("transcode" or "remux"); "directPlay"/"directStream" are accepted too so the
// session-level Decision strings cost the same cheap copy path.
func JobCostCenti(width, height int, decision string) int {
	switch decision {
	case "remux", "directPlay", "directStream":
		return 25
	}
	const ref = 1920 * 1080
	px := width * height
	if px <= 0 {
		return 100 // unknown dimensions — assume a 1080p-class transcode
	}
	cost := px * 100 / ref
	if cost < 25 {
		cost = 25
	}
	return cost
}

func workerScore(w WorkerRegistration, needs JobNeeds) int {
	// Hard count cap: never exceed MaxSessions concurrent jobs, regardless of
	// how cheap each one is (a worker swamped with remuxes still has finite I/O).
	if w.MaxSessions > 0 && w.ActiveSessions >= w.MaxSessions {
		return 0
	}
	// Cost budget: MaxSessions 1080p-equivalents, each worth 100 centi. A 4K
	// job (≈400) deducts 4× a 1080p job, so heavily-loaded workers fall behind.
	budget := w.MaxSessions * 100
	if budget <= 0 {
		budget = 100
	}
	avail := budget - w.ActiveCostCenti
	if avail < 0 {
		avail = 0
	}
	// Load tier — primary: proportional headroom (per-mille of budget still free)
	// so the job goes to the least-utilized worker, not merely the physically
	// largest one. Secondary: absolute headroom, so among equally-utilized
	// workers the larger one wins. A loaded 16-slot worker thus yields to an idle
	// 12-slot worker, but two idle workers still favor the bigger.
	fracPerMille := avail * 1000 / budget
	score := fracPerMille*loadScoreUnit + avail

	// Capability tier (soft): reward workers that can serve this job on their
	// fast path. Non-matching workers still score — they self-resolve a slower
	// fallback — so a job is never stranded, but a 4K-HDR job prefers a node that
	// can GPU-tonemap over one that would fall back to software zscale.
	if needs.ToneMap && w.HasGPUTonemap {
		score += capMatchBonus
	}
	if needs.PreferAV1 && hasEncoderPrefix(w.Capabilities, "av1") {
		score += capMatchBonus
	}
	if needs.PreferHEVC && hasEncoderPrefix(w.Capabilities, "hevc") {
		score += capMatchBonus
	}

	// GPU tier: dominates everything above so a GPU node always beats CPU-only.
	for _, cap := range w.Capabilities {
		if isGPUEncoder(cap) {
			score += gpuScoreBonus
			break
		}
	}
	return score
}

func isGPUEncoder(enc string) bool {
	switch Encoder(enc) {
	case EncoderNVENC, EncoderHEVCNVENC, EncoderAMF, EncoderQSV, EncoderVAAPI:
		return true
	default:
		return false
	}
}

// TranscodeJob holds the parameters for an FFmpeg transcode job.
type TranscodeJob struct {
	SessionID string `json:"session_id"`
	FilePath  string `json:"file_path"`
	// SourceURL is the HTTP fallback input (see Session.SourceURL). The
	// worker uses it only when FilePath can't be stat'd locally.
	SourceURL        string  `json:"source_url,omitempty"`
	SessionDir       string  `json:"session_dir"`
	StartOffsetSec   float64 `json:"start_offset_sec"`
	Decision         string  `json:"decision"`
	Encoder          string  `json:"encoder"`
	Width            int     `json:"width"`
	Height           int     `json:"height"`
	BitrateKbps      int     `json:"bitrate_kbps"`
	AudioCodec       string  `json:"audio_codec"`
	AudioChannels    int     `json:"audio_channels"`
	AudioStreamIndex int     `json:"audio_stream_index"` // -1 = default
	NeedsToneMap     bool    `json:"needs_tone_map"`
	IsHEVC           bool    `json:"is_hevc"`
	IsAV1            bool    `json:"is_av1"`      // source is AV1; remux must use fMP4 (mpegts has no AV1 stream type)
	IsH264           bool    `json:"is_h264"`     // source is H.264; pins h264_cuvid on the full-VRAM scale_cuda path
	PreferHEVC       bool    `json:"prefer_hevc"` // request HEVC output (4K + client supports it)
	// ForceFMP4 pins the SEGMENT CONTAINER independently of which encoder the
	// worker ends up using.
	//
	// The container used to be inferred from the actual encoder, while the
	// playlist the client is already holding was written from the codec the API
	// PREDICTED. Those disagree whenever the worker falls back — no HEVC
	// encoder on the node, or a rotated source forcing libx264 — and the client
	// then waits for seg00000.m4s while the worker writes seg00000.ts, so every
	// segment 503s and playback never starts. fMP4 carries H.264 perfectly
	// well, so honouring the promised container costs nothing and makes the
	// mismatch impossible.
	ForceFMP4       bool  `json:"force_fmp4,omitempty"`
	PreferAV1       bool  `json:"prefer_av1"` // request AV1 output (AV1 source + client supports AV1 + we have an AV1 encoder); takes priority over PreferHEVC since the natural use case is AV1 source playback
	SubtitleStreams []int `json:"subtitle_streams,omitempty"`
	// Incarnation is the restart counter of the session this job belongs to
	// (see Session.Incarnation). The worker writes into the directory for THIS
	// incarnation and kills itself when the stored session moves past it, so a
	// rung restart that reuses its session ID can't have its output eaten — or
	// kept alive — by the run it replaced.
	Incarnation int `json:"incarnation,omitempty"`
	// OutputTSOffsetSec shifts the output timeline so this job's media
	// timestamps start at its true CONTENT time rather than at zero.
	//
	// ABR rung children are seeked with `-ss`, which rebases timestamps to 0.
	// Every rung child therefore emitted segment N with the same PTS as every
	// other rung's segment 0, while the predicted playlist advertised them at
	// their global time — so a rung switch or a post-seek restart spliced two
	// disagreeing timelines together and the player stalled or jumped. Zero
	// leaves ffmpeg's default behaviour untouched (all non-ABR paths).
	OutputTSOffsetSec float64 `json:"output_ts_offset_sec,omitempty"`
	// AudioOnly / NoAudio mirror BuildArgs: the source has no video stream /
	// no audio stream, so the corresponding map + codec args must be omitted
	// (a hard -map of a missing stream kills ffmpeg instantly). Zero values
	// mean "both streams present" — the historical assumption.
	AudioOnly bool `json:"audio_only,omitempty"`
	NoAudio   bool `json:"no_audio,omitempty"`
	// Force8Bit pins the output to 8-bit — set when the client's declared
	// MaxVideoBitDepth is below 10, so a bit-depth-forced transcode cannot
	// hand back the very property that forced it. See BuildArgs.Force8Bit.
	Force8Bit bool `json:"force_8bit,omitempty"`
	// CostCenti is the job's weighted cost (see JobCostCenti), stamped by
	// DispatchJob so the worker can decrement the right amount on Ack.
	CostCenti  int       `json:"cost_centi,omitempty"`
	EnqueuedAt time.Time `json:"enqueued_at"`
}

// NewSessionID generates a new transcode session ID.
func NewSessionID() string {
	return uuid.New().String()
}

func sessionKey(id string) string   { return "transcode:session:" + id }
func heartbeatKey(id string) string { return "transcode:heartbeat:" + id }
func workerKey(id string) string    { return "worker:" + id }
