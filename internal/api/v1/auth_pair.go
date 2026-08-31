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
	"github.com/onscreen/onscreen/internal/audit"
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
	pairPINDigits  = 6                // PIN length, as displayed on the device
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
	// Attestation about the device that REQUESTED the code, captured at
	// creation from the request itself rather than supplied by the claimer.
	//
	// None of this was recorded, so the confirmation page had nothing true to
	// show: the PIN is a bare number, and the only "device name" in the record
	// came from whoever was claiming, not from the device being authorised.
	// That made the flow trivially phishable — "OnScreen support: your Living
	// Room TV lost its link, go to /pair and enter 481920" — with nothing on
	// screen to contradict the story. Surfacing the requesting IP and client
	// lets a user notice that the request came from somewhere they are not.
	RequestIP   string    `json:"request_ip,omitempty"`
	UserAgent   string    `json:"user_agent,omitempty"`
	RequestedAt time.Time `json:"requested_at,omitempty"`
}

// truncate bounds an attacker-supplied header before it is persisted, so a
// pathological User-Agent cannot bloat the pair record.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// PairHandler implements the device-pairing endpoints.
type PairHandler struct {
	store  PairStore
	issuer PairTokenIssuer
	logger *slog.Logger
	audit  *audit.Logger
}

// NewPairHandler constructs a PairHandler.
func NewPairHandler(store PairStore, issuer PairTokenIssuer, logger *slog.Logger) *PairHandler {
	return &PairHandler{store: store, issuer: issuer, logger: logger}
}

// WithAudit attaches an audit logger. Pairing mints a 30-day session — the
// same durable credential a password login produces — and was the only such
// path writing no audit row at all, so an operator reviewing "how did this
// device get access" found nothing. Returns the handler for chaining.
func (h *PairHandler) WithAudit(a *audit.Logger) *PairHandler {
	h.audit = a
	return h
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
	rec := pairRecord{
		Status:      pairStatusOpen,
		ExpiresAt:   expires,
		RequestIP:   audit.ClientIP(r),
		UserAgent:   truncate(r.UserAgent(), 200),
		RequestedAt: time.Now(),
	}
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

// Poll handles GET /api/v1/auth/pair/poll.
//
// The device_token is passed via `Authorization: Bearer <token>` — and
// only via the header — so it never appears in reverse-proxy access
// logs, CDN caches, browser history, referer headers, or OTel span
// attributes. The earlier `?device_token=...` query fallback was
// removed for log-leak hygiene.
//
// No user auth required; the device_token itself is the one-shot
// credential. Returns 202 while pending, 200 with TokenPair once
// claimed (and consumes the record), 410 once expired or already
// collected.
func (h *PairHandler) Poll(w http.ResponseWriter, r *http.Request) {
	deviceToken := extractDeviceToken(r)
	if deviceToken == "" {
		respond.BadRequest(w, r, "device_token is required (pass as Authorization: Bearer <token>)")
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

// Pending handles GET /api/v1/auth/pair/pending?pin=NNNNNN.
//
// Returns ONLY descriptive facts about the device that requested a pending
// code, so the confirmation page can tell the user what they are about to
// authorise instead of showing them a bare six-digit number. Authenticated and
// on the same throttle as Claim, because it answers "does this PIN exist" —
// which is not free information even though the PIN space is already
// rate-limited.
//
// Deliberately returns NO token, no user id, and nothing that advances the
// pairing: it is a read for the human in the loop.
func (h *PairHandler) Pending(w http.ResponseWriter, r *http.Request) {
	if middleware.ClaimsFromContext(r.Context()) == nil {
		respond.Unauthorized(w, r)
		return
	}
	pin := strings.TrimSpace(r.URL.Query().Get("pin"))
	if len(pin) != pairPINDigits {
		respond.BadRequest(w, r, "invalid pin")
		return
	}
	raw, err := h.store.Get(r.Context(), pairKeyPIN+pin)
	if err != nil {
		// 404 for both "no such PIN" and a store error — the caller learns
		// nothing either way.
		respond.NotFound(w, r)
		return
	}
	var rec pairRecord
	if jerr := json.Unmarshal([]byte(raw), &rec); jerr != nil {
		respond.NotFound(w, r)
		return
	}
	if rec.Status != pairStatusOpen || time.Now().After(rec.ExpiresAt) {
		respond.NotFound(w, r)
		return
	}
	respond.Success(w, r, map[string]any{
		"ip":           rec.RequestIP,
		"user_agent":   rec.UserAgent,
		"requested_at": rec.RequestedAt,
	})
}

// Claim handles POST /api/v1/auth/pair/claim — authenticated user binds the
// PIN they typed in their browser to their account, authorising the device.
func (h *PairHandler) Claim(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	// A PIN-switched session must not authorise a device.
	//
	// /auth/pin-switch deliberately mints an EPHEMERAL credential: Switched=true
	// and no refresh token, so entering a household profile with a 4-digit PIN
	// grants no durable access. Pairing bypassed that entirely — Claim accepted
	// the switched token, and Poll then issued a full pair through
	// issueTokenPair, which never sets Switched and does create a 30-day refresh
	// session. Two requests turned a deliberately-weak profile session into a
	// stronger credential than the switch itself was ever allowed to produce.
	// Pair from the account you actually signed in as.
	if claims.Switched {
		respond.Error(w, r, http.StatusForbidden, "PAIR_SWITCHED",
			"sign in with your account password before pairing a device")
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

	// Audit: this authorises a 30-day session for the caller's account, which
	// is the same durable grant a password login produces. Record who
	// authorised it and what the requesting device looked like, so "when did
	// this device get access, and from where" is answerable after the fact.
	if h.audit != nil {
		h.audit.Log(r.Context(), &claims.UserID, audit.ActionLoginSuccess, claims.UserID.String(),
			map[string]any{
				"method":         "device_pair",
				"device_name":    rec.DeviceName,
				"device_ip":      rec.RequestIP,
				"device_agent":   rec.UserAgent,
				"code_requested": rec.RequestedAt,
			}, audit.ClientIP(r))
	}

	respond.Success(w, r, map[string]any{
		"status":      rec.Status,
		"device_name": rec.DeviceName,
	})
}

// randomPIN returns a 6-digit zero-padded PIN drawn from crypto/rand.
// Rejection-samples to eliminate modulo bias — without this, values
// 0..967,295 would occur ~0.023% more often than 967,296..999,999.
// The expected retry rate is <1%; in practice this completes on the
// first iteration almost always.
func randomPIN() (string, error) {
	const pinSpace uint32 = 1_000_000
	// Largest multiple of pinSpace that fits in uint32; reads above
	// this threshold are discarded to keep the distribution uniform.
	const threshold = 4_294_000_000
	for {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		n := binary.BigEndian.Uint32(b[:])
		if n >= threshold {
			continue
		}
		return fmt.Sprintf("%06d", n%pinSpace), nil
	}
}

// extractDeviceToken pulls the one-shot device credential from the
// Authorization header. Header-only — query-string secrets land in
// nginx access logs, browser history, referer headers, and OTel span
// attributes, and a pair token is exactly the kind of high-value
// short-lived credential that survives long enough in those sinks to
// matter.
func extractDeviceToken(r *http.Request) string {
	bearer := r.Header.Get("Authorization")
	if !strings.HasPrefix(bearer, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(bearer, "Bearer "))
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
