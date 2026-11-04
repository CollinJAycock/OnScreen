package v1

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
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
// person who sent it has stopped waiting). In memory, like the broker
// whose events it shadows: a restart loses both.

const pendingTransferTTL = 2 * time.Minute

type pendingTransfer struct {
	payload TransferPayload
	expires time.Time
}

type pendingTransfers struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[uuid.UUID]map[string]pendingTransfer // user → target name → transfer
}

func newPendingTransfers() *pendingTransfers {
	return &pendingTransfers{now: time.Now, m: map[uuid.UUID]map[string]pendingTransfer{}}
}

// put records p for user, replacing an earlier one to the same name.
func (s *pendingTransfers) put(user uuid.UUID, p TransferPayload) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweep(now)
	byName := s.m[user]
	if byName == nil {
		byName = map[string]pendingTransfer{}
		s.m[user] = byName
	}
	byName[transferKey(p.TargetClientName)] = pendingTransfer{payload: p, expires: now.Add(pendingTransferTTL)}
}

// take removes and returns the transfer for user to name, if one is
// waiting and not stale.
func (s *pendingTransfers) take(user uuid.UUID, name string) (TransferPayload, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	byName := s.m[user]
	key := transferKey(name)
	p, ok := byName[key]
	if !ok {
		return TransferPayload{}, false
	}
	delete(byName, key)
	if len(byName) == 0 {
		delete(s.m, user)
	}
	if !s.now().Before(p.expires) {
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
	p, ok := h.pending.take(claims.UserID, body.ClientName)
	if !ok {
		respond.NoContent(w)
		return
	}
	respond.Success(w, r, p)
}
