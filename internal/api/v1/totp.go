package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
)

// TOTP error sentinels — the service returns these so the handler can map
// to the right status code without leaking which step failed.
var (
	// ErrBadTOTPCode — the submitted 6-digit code (or recovery code) did
	// not validate.
	ErrBadTOTPCode = errors.New("totp: invalid code")
	// ErrTOTPNotPending — activate called with no secret staged by setup.
	ErrTOTPNotPending = errors.New("totp: no pending setup")
	// ErrTOTPAlreadyEnabled — setup called while 2FA is already active;
	// the caller must disable first so a stray setup can't silently
	// rotate the secret and lock the user out.
	ErrTOTPAlreadyEnabled = errors.New("totp: already enabled")
	// ErrInvalidTOTPChallenge — the login challenge token is missing,
	// malformed, expired, or not a totp_challenge token.
	ErrInvalidTOTPChallenge = errors.New("totp: invalid login challenge")
	// ErrTOTPLocalOnly — setup attempted on a passwordless (federated)
	// account. Those users authenticate at their IdP, where 2FA belongs;
	// a local secret would never be checked and give a false sense of
	// security.
	ErrTOTPLocalOnly = errors.New("totp: local password accounts only")
)

// TOTPService is the domain surface for two-factor auth. Implemented by
// the cmd/server authService (it already holds the DB, token maker, and
// encryptor the TOTP flow needs).
type TOTPService interface {
	// SetupTOTP stages a fresh secret (totp_enabled stays false) and
	// returns the otpauth:// URI + base32 secret for the client to render
	// as a QR / manual-entry string. Returns ErrTOTPAlreadyEnabled if 2FA
	// is already active.
	SetupTOTP(ctx context.Context, userID uuid.UUID, accountName string) (otpauthURL, secret string, err error)
	// ActivateTOTP confirms the staged secret with a live code, flips
	// totp_enabled true, and returns freshly-minted single-use recovery
	// codes (shown to the user exactly once).
	ActivateTOTP(ctx context.Context, userID uuid.UUID, code string) (recoveryCodes []string, err error)
	// DisableTOTP turns 2FA off after re-proving possession (a current
	// code or recovery code) and wipes the secret + recovery codes.
	DisableTOTP(ctx context.Context, userID uuid.UUID, code string) error
	// VerifyTOTPLogin completes a two-step login: validates the challenge
	// token + second factor and returns the real token pair.
	VerifyTOTPLogin(ctx context.Context, challengeToken, code string) (*TokenPair, error)
	// TOTPStatus reports whether 2FA is on for the user and how many
	// unused recovery codes remain.
	TOTPStatus(ctx context.Context, userID uuid.UUID) (enabled bool, recoveryRemaining int, err error)
}

// TOTPHandler serves the /auth/totp/* endpoints.
type TOTPHandler struct {
	svc    TOTPService
	logger *slog.Logger
	audit  *audit.Logger
}

func NewTOTPHandler(svc TOTPService, logger *slog.Logger) *TOTPHandler {
	return &TOTPHandler{svc: svc, logger: logger}
}

// WithAudit attaches an audit logger (login-success on the verify step).
func (h *TOTPHandler) WithAudit(a *audit.Logger) *TOTPHandler {
	h.audit = a
	return h
}

// Status — GET /auth/totp/status (authenticated). Drives the Settings →
// Security toggle (Enable vs Disable) and the "N recovery codes left"
// hint.
func (h *TOTPHandler) Status(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	enabled, remaining, err := h.svc.TOTPStatus(r.Context(), claims.UserID)
	if err != nil {
		h.logErr(r, "totp status", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, map[string]any{
		"enabled":                  enabled,
		"recovery_codes_remaining": remaining,
	})
}

// Setup — POST /auth/totp/setup (authenticated). Stages a secret and
// returns the provisioning details; does not enable 2FA yet.
func (h *TOTPHandler) Setup(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	url, secret, err := h.svc.SetupTOTP(r.Context(), claims.UserID, claims.Username)
	if err != nil {
		if errors.Is(err, ErrTOTPAlreadyEnabled) {
			respond.Error(w, r, http.StatusConflict, "TOTP_ALREADY_ENABLED",
				"two-factor auth is already enabled; disable it first to re-enrol")
			return
		}
		if errors.Is(err, ErrTOTPLocalOnly) {
			respond.Error(w, r, http.StatusBadRequest, "TOTP_LOCAL_ONLY",
				"two-factor auth applies to password accounts; your account signs in through an identity provider")
			return
		}
		h.logErr(r, "totp setup", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, map[string]any{
		"otpauth_url": url,
		"secret":      secret,
	})
}

// Activate — POST /auth/totp/activate (authenticated). Confirms the
// staged secret with a live code and returns one-time recovery codes.
func (h *TOTPHandler) Activate(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	codes, err := h.svc.ActivateTOTP(r.Context(), claims.UserID, body.Code)
	if err != nil {
		switch {
		case errors.Is(err, ErrBadTOTPCode):
			respond.Error(w, r, http.StatusBadRequest, "TOTP_BAD_CODE", "that code didn't match — try again")
		case errors.Is(err, ErrTOTPNotPending):
			respond.Error(w, r, http.StatusBadRequest, "TOTP_NOT_PENDING", "start setup before activating")
		default:
			h.logErr(r, "totp activate", err)
			respond.InternalError(w, r)
		}
		return
	}
	respond.Success(w, r, map[string]any{"recovery_codes": codes})
}

// Disable — POST /auth/totp/disable (authenticated). Requires a current
// code (or recovery code) to prove possession before turning 2FA off.
func (h *TOTPHandler) Disable(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if err := h.svc.DisableTOTP(r.Context(), claims.UserID, body.Code); err != nil {
		if errors.Is(err, ErrBadTOTPCode) {
			respond.Error(w, r, http.StatusBadRequest, "TOTP_BAD_CODE", "that code didn't match — 2FA is still on")
			return
		}
		h.logErr(r, "totp disable", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, map[string]any{"enabled": false})
}

// Verify — POST /auth/totp/verify (public). Second step of a two-step
// login: exchanges the challenge token + code for a real token pair.
func (h *TOTPHandler) Verify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LoginChallengeToken string `json:"login_challenge_token"`
		Code                string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	pair, err := h.svc.VerifyTOTPLogin(r.Context(), body.LoginChallengeToken, body.Code)
	if err != nil {
		// Both bad-code and bad-challenge collapse to 401 — the client
		// only needs "try again / restart login", and distinguishing them
		// would tell an attacker holding a challenge token whether their
		// guessed code was the only thing wrong.
		respond.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "invalid code")
		return
	}
	if h.audit != nil {
		h.audit.Log(r.Context(), &pair.UserID, audit.ActionLoginSuccess, pair.Username, nil, audit.ClientIP(r))
	}
	setAuthCookies(w, r, pair)
	respond.Success(w, r, pair)
}

func (h *TOTPHandler) logErr(r *http.Request, msg string, err error) {
	if h.logger != nil {
		h.logger.ErrorContext(r.Context(), msg, "err", err)
	}
}
