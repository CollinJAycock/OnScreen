package transcode

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

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
	ID             string    `json:"id"`
	UserID         uuid.UUID `json:"user_id"`
	MediaItemID    uuid.UUID `json:"media_item_id"`
	FileID         uuid.UUID `json:"file_id"`
	WorkerID       string    `json:"worker_id"`
	WorkerAddr     string    `json:"worker_addr"`
	Decision       string    `json:"decision"` // "directPlay"|"directStream"|"transcode"
	FilePath       string    `json:"file_path"`
	PositionMS     int64     `json:"position_ms"`
	CreatedAt      time.Time `json:"created_at"`
	LastActivityAt time.Time `json:"last_activity_at,omitempty"`
	ClientName     string    `json:"client_name,omitempty"`
	ClientID       string    `json:"client_id,omitempty"`
	SegToken       string    `json:"seg_token,omitempty"`
	BitrateKbps    int       `json:"bitrate_kbps,omitempty"`
	HEVCOutput     bool      `json:"hevc_output,omitempty"` // true = fMP4 segments (.m4s) with hvc1 codec
	AV1Output      bool      `json:"av1_output,omitempty"`  // true = fMP4 segments (.m4s) with av01 codec; either fMP4 flag selects the .m4s wait path
}

// WorkerRegistration is the record a transcode worker writes to Valkey.
type WorkerRegistration struct {
	ID             string            `json:"id"`
	Addr           string            `json:"addr"` // "host:port" of the worker HTTP server
	Capabilities   []string          `json:"capabilities"`
	EncoderLabels  map[string]string `json:"encoder_labels,omitempty"` // encoder → human label (e.g. "h264_nvenc" → "NVIDIA GeForce RTX 5080")
	MaxSessions    int               `json:"max_sessions"`
	ActiveSessions int               `json:"active_sessions"`
	RegisteredAt   time.Time         `json:"registered_at"`
}

// SessionStore manages transcode sessions in Valkey.
type SessionStore struct {
	v *valkey.Client
}

// NewSessionStore creates a SessionStore backed by the given Valkey client.
func NewSessionStore(v *valkey.Client) *SessionStore {
	return &SessionStore{v: v}
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
	return s.v.Del(ctx, sessionKey(id))
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
		return "", s.EnqueueJob(ctx, job)
	}

	// Read each worker's dispatch counter and add to ActiveSessions.
	for i := range workers {
		n, err := s.v.Raw().Get(ctx, dispatchCounterKey(workers[i].Addr)).Int()
		if err == nil {
			workers[i].ActiveSessions += n
		}
	}

	best := selectWorker(workers)
	b, err := json.Marshal(job)
	if err != nil {
		return "", fmt.Errorf("marshal job: %w", err)
	}
	// Atomically increment dispatch counter, then push job.
	s.v.Raw().Incr(ctx, dispatchCounterKey(best.Addr))
	s.v.Raw().Expire(ctx, dispatchCounterKey(best.Addr), workerTTL)
	return best.Addr, s.v.Raw().RPush(ctx, workerQueueKey(best.Addr), string(b)).Err()
}

// AckDispatch decrements the dispatch counter when a worker starts processing
// a job. Called by the worker after BLPOP returns a job from its queue.
func (s *SessionStore) AckDispatch(ctx context.Context, workerAddr string) {
	key := dispatchCounterKey(workerAddr)
	val, err := s.v.Raw().Decr(ctx, key).Result()
	if err == nil && val <= 0 {
		s.v.Raw().Del(ctx, key)
	}
}

func dispatchCounterKey(addr string) string {
	return "transcode:dispatched:" + addr
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

// selectWorker picks the best worker for a new job. GPU-capable workers are
// strongly preferred (score +1000), then the worker with the most available
// capacity wins ties.
func selectWorker(workers []WorkerRegistration) WorkerRegistration {
	best := workers[0]
	bestScore := workerScore(best)
	for _, w := range workers[1:] {
		s := workerScore(w)
		if s > bestScore {
			bestScore = s
			best = w
		}
	}
	return best
}

func workerScore(w WorkerRegistration) int {
	avail := w.MaxSessions - w.ActiveSessions
	if avail < 0 {
		avail = 0
	}
	if avail == 0 {
		return 0 // at capacity — don't prefer regardless of GPU
	}
	score := avail
	for _, cap := range w.Capabilities {
		if isGPUEncoder(cap) {
			score += 1000
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
	SessionID        string    `json:"session_id"`
	FilePath         string    `json:"file_path"`
	SessionDir       string    `json:"session_dir"`
	StartOffsetSec   float64   `json:"start_offset_sec"`
	Decision         string    `json:"decision"`
	Encoder          string    `json:"encoder"`
	Width            int       `json:"width"`
	Height           int       `json:"height"`
	BitrateKbps      int       `json:"bitrate_kbps"`
	AudioCodec       string    `json:"audio_codec"`
	AudioChannels    int       `json:"audio_channels"`
	AudioStreamIndex int       `json:"audio_stream_index"` // -1 = default
	NeedsToneMap     bool      `json:"needs_tone_map"`
	IsHEVC           bool      `json:"is_hevc"`
	PreferHEVC       bool      `json:"prefer_hevc"` // request HEVC output (4K + client supports it)
	PreferAV1        bool      `json:"prefer_av1"`  // request AV1 output (AV1 source + client supports AV1 + we have an AV1 encoder); takes priority over PreferHEVC since the natural use case is AV1 source playback
	SubtitleStreams  []int     `json:"subtitle_streams,omitempty"`
	EnqueuedAt       time.Time `json:"enqueued_at"`
}

// NewSessionID generates a new transcode session ID.
func NewSessionID() string {
	return uuid.New().String()
}

func sessionKey(id string) string   { return "transcode:session:" + id }
func heartbeatKey(id string) string { return "transcode:heartbeat:" + id }
func workerKey(id string) string    { return "worker:" + id }
