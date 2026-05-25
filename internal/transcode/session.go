package transcode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/onscreen/onscreen/internal/observability"
	"github.com/onscreen/onscreen/internal/valkey"
)

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
	NeedsToneMap     bool        `json:"needs_tone_map,omitempty"`
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
	// SelectedRendition is the rung label ("720p") the client most recently
	// pulled a segment for, recorded on the PARENT. Surfaced as
	// selected_rendition on /api/v1/sessions so operators see which bitrate
	// the player's ABR logic actually settled on.
	SelectedRendition string `json:"selected_rendition,omitempty"`
}

// WorkerRegistration is the record a transcode worker writes to Valkey.
type WorkerRegistration struct {
	ID             string            `json:"id"`
	Addr           string            `json:"addr"` // "host:port" of the worker HTTP server
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
	raw, err := s.v.Get(ctx, sessionKey(sessionID))
	if err != nil {
		return
	}
	var sess Session
	if err := json.Unmarshal([]byte(raw), &sess); err != nil {
		return
	}
	sess.LastActivityAt = time.Now()
	b, err := json.Marshal(sess)
	if err != nil {
		return
	}
	ttl := s.v.Raw().TTL(ctx, sessionKey(sessionID)).Val()
	if ttl <= 0 {
		ttl = sessionTTL
	}
	_ = s.v.Set(ctx, sessionKey(sessionID), string(b), ttl)
}

// CountByUser returns the number of active sessions for the given user
// across all media items. Used by the per-user concurrency cap so a
// single account can't pin every GPU/CPU slot by spamming Start with
// different item IDs (each one bypasses supersedeUserItem because the
// match key is (user, item), not (user)).
func (s *SessionStore) CountByUser(ctx context.Context, userID uuid.UUID) (int, error) {
	all, err := s.List(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sess := range all {
		if sess.UserID == userID {
			n++
		}
	}
	return n, nil
}

// DeleteByMedia removes all sessions for the given media item.
// Called by the progress endpoint on "stopped" to clean up even if the client
// never explicitly hits the Stop endpoint (e.g. tab closed after playback ends).
func (s *SessionStore) DeleteByMedia(ctx context.Context, mediaItemID uuid.UUID) error {
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
		if sess.MediaItemID == mediaItemID {
			s.v.Raw().SRem(ctx, sessionIndexKey, ids[i])
			_ = s.v.Del(ctx, keys[i])
		}
	}
	return nil
}

// UpdatePositionByMedia finds the active session for the given media item and
// updates its PositionMS. Silently no-ops if no matching session exists.
//
// NOTE: concurrent position updates for the same session may race (lost update).
// A Valkey WATCH/MULTI/EXEC or Lua script would provide atomicity.
func (s *SessionStore) UpdatePositionByMedia(ctx context.Context, mediaItemID uuid.UUID, positionMS int64) error {
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
		if sess.MediaItemID != mediaItemID {
			continue
		}
		sess.PositionMS = positionMS
		sess.LastActivityAt = time.Now()
		b, err := json.Marshal(sess)
		if err != nil {
			continue
		}
		ttl := s.v.Raw().TTL(ctx, keys[i]).Val()
		if ttl <= 0 {
			ttl = sessionTTL
		}
		_ = s.v.Set(ctx, keys[i], string(b), ttl)
		break
	}
	return nil
}

// SetWorkerInfo stamps the session with the worker ID and address that claimed
// the job. The API uses WorkerAddr to proxy segment requests to the correct
// worker in multi-instance deployments.
func (s *SessionStore) SetWorkerInfo(ctx context.Context, sessionID, workerID, workerAddr string, hevcOutput, av1Output bool) error {
	raw, err := s.v.Get(ctx, sessionKey(sessionID))
	if err != nil {
		return fmt.Errorf("get session for worker stamp: %w", err)
	}
	var sess Session
	if err := json.Unmarshal([]byte(raw), &sess); err != nil {
		return fmt.Errorf("unmarshal session: %w", err)
	}
	sess.WorkerID = workerID
	sess.WorkerAddr = workerAddr
	sess.HEVCOutput = hevcOutput
	sess.AV1Output = av1Output
	b, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	ttl := s.v.Raw().TTL(ctx, sessionKey(sessionID)).Val()
	if ttl <= 0 {
		ttl = sessionTTL
	}
	return s.v.Set(ctx, sessionKey(sessionID), string(b), ttl)
}

// SetSelectedRendition records on an ABR PARENT session which rung the client
// most recently fetched a segment for, and refreshes the parent's activity so
// its /sessions card stays live while playback rides on rung children. A no-op
// when the rung is unchanged and the activity stamp is recent, to skip a Valkey
// write on the common steady-state segment-by-segment case.
func (s *SessionStore) SetSelectedRendition(ctx context.Context, sessionID, rungLabel string) {
	raw, err := s.v.Get(ctx, sessionKey(sessionID))
	if err != nil {
		return
	}
	var sess Session
	if err := json.Unmarshal([]byte(raw), &sess); err != nil {
		return
	}
	if sess.SelectedRendition == rungLabel && time.Since(sess.LastActivityAt) < 2*time.Second {
		return
	}
	sess.SelectedRendition = rungLabel
	sess.LastActivityAt = time.Now()
	b, err := json.Marshal(sess)
	if err != nil {
		return
	}
	ttl := s.v.Raw().TTL(ctx, sessionKey(sessionID)).Val()
	if ttl <= 0 {
		ttl = sessionTTL
	}
	_ = s.v.Set(ctx, sessionKey(sessionID), string(b), ttl)
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

// hasEncoderPrefix reports whether the worker advertises an encoder of the given
// codec family ("av1" → av1_nvenc/av1_qsv/av1_amf/av1_vaapi, "hevc" → hevc_*).
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
	PreferHEVC       bool    `json:"prefer_hevc"` // request HEVC output (4K + client supports it)
	PreferAV1        bool    `json:"prefer_av1"`  // request AV1 output (AV1 source + client supports AV1 + we have an AV1 encoder); takes priority over PreferHEVC since the natural use case is AV1 source playback
	SubtitleStreams  []int   `json:"subtitle_streams,omitempty"`
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
