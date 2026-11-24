package v1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/valkey"
)

// A transfer is an SSE event, and a client that can't hold the stream
// open never sees it: a Roku's roUrlTransfer only hands over a finished
// body, and its raw socket has no TLS. So Transfer also leaves the
// transfer here, keyed by user and target name, and such a client asks
// for it (POST /playback/transfer/claim) every few seconds.
//
// Claiming removes it, so one transfer starts playback once even when
// two polls cross. A newer transfer to the same name replaces an older
// one; one nobody claims within pendingTransferTTL is dropped (the
// person who sent it has stopped waiting).
//
// The transfer and the claim can reach different API instances, so the
// waiting transfers are shared through Valkey when it is wired (as the
// relay shares the SSE event), and a claim takes one with GETDEL, so two
// instances can't both hand it out. Each instance also keeps the
// transfers it recorded in memory: that copy answers while Valkey is
// unreachable, and it is all a restart loses.

const (
	pendingTransferTTL = 2 * time.Minute
	// pendingTransferKeyPrefix namespaces the shared transfers in Valkey:
	// xfer:<user>:<hash of the target name>.
	pendingTransferKeyPrefix = "xfer:"
)

type pendingTransfer struct {
	payload TransferPayload
	expires time.Time
	// shared: Valkey has it too. A claim that then finds nothing in
	// Valkey means another instance's claim took it (or it lapsed), so
	// this copy is dropped, not played a second time.
	shared bool
}

type pendingTransfers struct {
	v      *valkey.Client // nil = this process only
	logger *slog.Logger

	mu  sync.Mutex
	now func() time.Time
	m   map[uuid.UUID]map[string]pendingTransfer // user → target name → transfer
}

func newPendingTransfers(logger *slog.Logger) *pendingTransfers {
	if logger == nil {
		logger = slog.Default()
	}
	return &pendingTransfers{logger: logger, now: time.Now, m: map[uuid.UUID]map[string]pendingTransfer{}}
}

// pendingTransferRedisKey hashes the name, which is whatever the sender
// picked or typed: the key stays short whatever its length.
func pendingTransferRedisKey(user uuid.UUID, name string) string {
	sum := sha256.Sum256([]byte(name))
	return pendingTransferKeyPrefix + user.String() + ":" + hex.EncodeToString(sum[:16])
}

// put records p for user, replacing an earlier one to the same name.
func (s *pendingTransfers) put(ctx context.Context, user uuid.UUID, p TransferPayload) {
	key := transferKey(p.TargetClientName)
	shared := false
	if s.v != nil {
		data, err := json.Marshal(p)
		if err == nil {
			err = s.v.Set(ctx, pendingTransferRedisKey(user, key), data, pendingTransferTTL)
		}
		if err != nil {
			s.logger.WarnContext(ctx, "playback transfer: share for claim; only this instance can hand it out", "err", err)
		} else {
			shared = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweep(now)
	byName := s.m[user]
	if byName == nil {
		byName = map[string]pendingTransfer{}
		s.m[user] = byName
	}
	byName[key] = pendingTransfer{payload: p, expires: now.Add(pendingTransferTTL), shared: shared}
}

// take removes and returns the transfer for user to name, if one is
// waiting and not stale.
func (s *pendingTransfers) take(ctx context.Context, user uuid.UUID, name string) (TransferPayload, bool) {
	key := transferKey(name)
	if s.v != nil {
		raw, err := s.v.GetDel(ctx, pendingTransferRedisKey(user, key))
		switch {
		case err == nil:
			// This instance's copy is the same transfer or an older one:
			// it must not play later.
			s.takeLocal(user, key, false)
			var p TransferPayload
			if err := json.Unmarshal([]byte(raw), &p); err != nil {
				s.logger.WarnContext(ctx, "playback transfer: unreadable shared transfer", "err", err)
				return TransferPayload{}, false
			}
			return p, true
		case errors.Is(err, redis.Nil):
			// Nothing shared: only a transfer recorded here while Valkey
			// was unreachable is still owed.
			return s.takeLocal(user, key, true)
		default:
			// Debug: a Roku polls every few seconds, so an outage would
			// otherwise log once per poll per device.
			s.logger.DebugContext(ctx, "playback transfer: claim lookup", "err", err)
		}
	}
	return s.takeLocal(user, key, false)
}

// takeLocal removes the transfer this process holds for user to key and
// returns it unless it is stale, or shared while unsharedOnly is set.
func (s *pendingTransfers) takeLocal(user uuid.UUID, key string, unsharedOnly bool) (TransferPayload, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	byName := s.m[user]
	p, ok := byName[key]
	if !ok {
		return TransferPayload{}, false
	}
	delete(byName, key)
	if len(byName) == 0 {
		delete(s.m, user)
	}
	if !s.now().Before(p.expires) || (unsharedOnly && p.shared) {
		return TransferPayload{}, false
	}
	return p.payload, true
}

// sweep drops stale transfers. Called under mu from put, so the map
// can't grow with transfers to names that never claim.
func (s *pendingTransfers) sweep(now time.Time) {
	for user, byName := range s.m {
		for name, p := range byName {
			if !now.Before(p.expires) {
				delete(byName, name)
			}
		}
		if len(byName) == 0 {
			delete(s.m, user)
		}
	}
}

// Names are matched as the SSE receivers match them, but without the
// case and surrounding-space slips a typed device name picks up.
func transferKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// ClaimTransferRequest is the POST body for /playback/transfer/claim:
// the caller's own client_name (the one its progress reports carry,
// which is what the "Play on…" picker offers).
type ClaimTransferRequest struct {
	ClientName string `json:"client_name"`
}

// ClaimTransfer handles POST /api/v1/playback/transfer/claim: 200 with
// the transfer waiting for this client (and removes it), 204 when there
// is none.
func (h *PlaybackHandler) ClaimTransfer(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	var body ClaimTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid body")
		return
	}
	if strings.TrimSpace(body.ClientName) == "" {
		respond.BadRequest(w, r, "client_name required")
		return
	}
	p, ok := h.pending.take(r.Context(), claims.UserID, body.ClientName)
	if !ok {
		respond.NoContent(w)
		return
	}
	respond.Success(w, r, p)
}
