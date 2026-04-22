package v1

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
)

// PairStore is the small key/value contract the pairing flow needs. Backed
// by Valkey in production; an in-memory map in tests.
type PairStore interface {
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Get(ctx context.Context, key string) (string, error)
	Del(ctx context.Context, keys ...string) error
}

// ErrPairNotFound is returned by PairStore.Get when the key has expired or
// never existed. Implementations must surface this exact sentinel so the
// handler can distinguish "wrong PIN" from "Valkey unreachable".
var ErrPairNotFound = errors.New("pair: not found")

// PairTokenIssuer issues a TokenPair for a given user id. Provided by the
// auth service so the pair handler doesn't need to know how sessions are
// persisted.
type PairTokenIssuer func(ctx context.Context, userID uuid.UUID) (*TokenPair, error)

const (
	pairCodeTTL    = 10 * time.Minute // pending code lifespan
	pairClaimTTL   = 5 * time.Minute  // window for native client to fetch tokens after claim
	pairPINMaxTry  = 8                // how many times we retry on PIN collision before failing
	pairKeyDev     = "pair:dev:"
	pairKeyPIN     = "pair:pin:"
	pairStatusOpen = "pending"
	pairStatusDone = "claimed"
)

// pairRecord is the JSON we serialise into Valkey for each pending pairing.
type pairRecord struct {
	PIN        string    `json:"pin"`
	Status     string    `json:"status"`
	UserID     string    `json:"user_id,omitempty"`
	DeviceName string    `json:"device_name,omitempty"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// PairHandler implements the device-pairing endpoints.
type PairHandler struct {
	store  PairStore
	issuer PairTokenIssuer
	logger *slog.Logger
}

// NewPairHandler constructs a PairHandler.
func NewPairHandler(store PairStore, issuer PairTokenIssuer, logger *slog.Logger) *PairHandler {
	return &PairHandler{store: store, issuer: issuer, logger: logger}
}

// CreateCode handles POST /api/v1/auth/pair/code.
//
// No auth required — the native client (TV, phone) calls this on its own to
// kick off pairing. Returns a 6-digit PIN to display on screen plus an
// opaque device_token the client uses to poll.
func (h *PairHandler) CreateCode(w http.ResponseWriter, r *http.Request) {
	deviceToken := uuid.NewString()
	expires := time.Now().Add(pairCodeTTL)

	// Retry on PIN collision; with ~10⁶ PINs and 10-min TTL collisions are
	// rare but not impossible on a busy server.
	var pin string
	rec := pairRecord{Status: pairStatusOpen, ExpiresAt: expires}
	for i := 0; i < pairPINMaxTry; i++ {
		candidate, err := randomPIN()
		if err != nil {
			h.logger.ErrorContext(r.Context(), "pair: generate pin", "err", err)
			respond.InternalError(w, r)
			return
		}
		// SET NX semantics: only store if PIN is free. We don't have SETNX on
		// our store wrapper, so use Get-then-Set with the small race window
		// it implies — collisions resolve on the next attempt.
		if _, err := h.store.Get(r.Context(), pairKeyPIN+candidate); err == nil {
			continue // taken, retry
		} else if !errors.Is(err, ErrPairNotFound) {
			h.logger.ErrorContext(r.Context(), "pair: store get", "err", err)
			respond.InternalError(w, r)
			return
		}
		pin = candidate
		break
	}
	if pin == "" {
		h.logger.ErrorContext(r.Context(), "pair: pin space exhausted")
		respond.Error(w, r, http.StatusServiceUnavailable, "PAIR_BUSY", "could not allocate pairing code, retry shortly")
		return
	}

	rec.PIN = pin
	body, err := json.Marshal(rec)
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	if err := h.store.Set(r.Context(), pairKeyDev+deviceToken, string(body), pairCodeTTL); err != nil {
		h.logger.ErrorContext(r.Context(), "pair: store set dev", "err", err)
		respond.InternalError(w, r)
		return
	}
	if err := h.store.Set(r.Context(), pairKeyPIN+pin, deviceToken, pairCodeTTL); err != nil {
		// Best-effort cleanup of the device record we just wrote.
		_ = h.store.Del(r.Context(), pairKeyDev+deviceToken)
		h.logger.ErrorContext(r.Context(), "pair: store set pin", "err", err)
		respond.InternalError(w, r)
		return
	}

	respond.Created(w, r, map[string]any{
		"pin":          pin,
		"device_token": deviceToken,
		"expires_at":   expires,
		"poll_after":   2, // seconds — hint for client polling cadence
	})
}

// Poll handles GET /api/v1/auth/pair/poll?device_token=...
//
// No auth required; the device_token itself is the credential. Returns 202
// while pending, 200 with TokenPair once claimed (and consumes the record),
// 410 once expired or already collected.
func (h *PairHandler) Poll(w http.ResponseWriter, r *http.Request) {
	deviceToken := strings.TrimSpace(r.URL.Query().Get("device_token"))
	if deviceToken == "" {
		respond.BadRequest(w, r, "device_token is required")
		return
	}
	raw, err := h.store.Get(r.Context(), pairKeyDev+deviceToken)
	if err != nil {
		if errors.Is(err, ErrPairNotFound) {
			respond.Error(w, r, http.StatusGone, "PAIR_EXPIRED", "pairing code expired or already used")
			return
		}
		h.logger.ErrorContext(r.Context(), "pair: store get dev", "err", err)
		respond.InternalError(w, r)
		return
	}
	var rec pairRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		respond.InternalError(w, r)
		return
	}

	if rec.Status != pairStatusDone {
		respond.JSON(w, r, http.StatusAccepted, map[string]any{
			"status":     rec.Status,
			"expires_at": rec.ExpiresAt,
		})
		return
	}

	uid, err := uuid.Parse(rec.UserID)
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	pair, err := h.issuer(r.Context(), uid)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "pair: issue token", "err", err)
		respond.InternalError(w, r)
		return
	}

	// One-shot: delete both keys so the same device_token can't redeem twice.
	_ = h.store.Del(r.Context(), pairKeyDev+deviceToken, pairKeyPIN+rec.PIN)

	respond.Success(w, r, pair)
}

// Claim handles POST /api/v1/auth/pair/claim — authenticated user binds the
// PIN they typed in their browser to their account, authorising the device.
func (h *PairHandler) Claim(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}

	var body struct {
		PIN        string `json:"pin"`
		DeviceName string `json:"device_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	body.PIN = strings.TrimSpace(body.PIN)
	if !validPIN(body.PIN) {
		respond.BadRequest(w, r, "pin must be 6 digits")
		return
	}

	deviceToken, err := h.store.Get(r.Context(), pairKeyPIN+body.PIN)
	if err != nil {
		if errors.Is(err, ErrPairNotFound) {
			respond.Error(w, r, http.StatusNotFound, "PAIR_INVALID", "pairing code not recognised")
			return
		}
		h.logger.ErrorContext(r.Context(), "pair: store get pin", "err", err)
		respond.InternalError(w, r)
		return
	}
	raw, err := h.store.Get(r.Context(), pairKeyDev+deviceToken)
	if err != nil {
		if errors.Is(err, ErrPairNotFound) {
			respond.Error(w, r, http.StatusGone, "PAIR_EXPIRED", "pairing code expired")
			return
		}
		respond.InternalError(w, r)
		return
	}
	var rec pairRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		respond.InternalError(w, r)
		return
	}
	if rec.Status == pairStatusDone {
		respond.Error(w, r, http.StatusConflict, "PAIR_USED", "pairing code already claimed")
		return
	}

	rec.Status = pairStatusDone
	rec.UserID = claims.UserID.String()
	rec.DeviceName = strings.TrimSpace(body.DeviceName)
	rec.ExpiresAt = time.Now().Add(pairClaimTTL)
	updated, err := json.Marshal(rec)
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	if err := h.store.Set(r.Context(), pairKeyDev+deviceToken, string(updated), pairClaimTTL); err != nil {
		respond.InternalError(w, r)
		return
	}
	// Drop the PIN reverse-index immediately — the code is spent, even if
	// the device hasn't picked up its tokens yet.
	_ = h.store.Del(r.Context(), pairKeyPIN+body.PIN)

	respond.Success(w, r, map[string]any{
		"status":      rec.Status,
		"device_name": rec.DeviceName,
	})
}

// randomPIN returns a 6-digit zero-padded PIN drawn from crypto/rand.
func randomPIN() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	n := binary.BigEndian.Uint32(b[:]) % 1_000_000
	return fmt.Sprintf("%06d", n), nil
}

// validPIN reports whether s is exactly six ASCII digits.
func validPIN(s string) bool {
	if len(s) != 6 {
		return false
	}
	for i := 0; i < 6; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
