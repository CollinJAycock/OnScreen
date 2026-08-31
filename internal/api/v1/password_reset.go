package v1

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/email"
	"github.com/onscreen/onscreen/internal/observability"
)

// PasswordResetDB is the database interface for the password reset flow.
type PasswordResetDB interface {
	GetUserByEmail(ctx context.Context, email *string) (PRUser, error)
	CreateResetToken(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error
	GetResetToken(ctx context.Context, tokenHash string) (PRToken, error)
	// MarkResetTokenUsed atomically claims the token. Returns
	// (true, nil) when this caller won the race; (false, nil) when
	// another concurrent submission already consumed it. The handler
	// MUST call this BEFORE UpdatePassword so two concurrent reset
	// requests can't both pass GetResetToken's used_at IS NULL check
	// and run last-write-wins on the password column.
	MarkResetTokenUsed(ctx context.Context, id uuid.UUID) (bool, error)
	UpdatePassword(ctx context.Context, userID uuid.UUID, passwordHash string) error
	// BumpSessionEpoch + DeleteSessionsForUser together revoke all
	// outstanding credentials for the user — see ResetPassword for why
	// we call them after a successful password update.
	BumpSessionEpoch(ctx context.Context, userID uuid.UUID) error
	DeleteSessionsForUser(ctx context.Context, userID uuid.UUID) error
}

// PRUser is the minimal user info needed for password reset.
type PRUser struct {
	ID       uuid.UUID
	Username string
	Email    *string
}

// PRToken represents a password reset token row.
type PRToken struct {
	ID     uuid.UUID
	UserID uuid.UUID
}

// PasswordResetHandler handles forgot password / reset password flows.
type PasswordResetHandler struct {
	db        PasswordResetDB
	sender    *email.Sender // always non-nil; live SMTP state via sender.Enabled(ctx)
	baseURL   string
	logger    *slog.Logger
	segTokens SegmentTokenRevoker // optional; nil means HLS tokens age out via TTL
	audit     *audit.Logger       // optional; nil disables audit on successful reset

	// resetThrottle bounds how often a reset mail may be sent to one address,
	// independent of who asked. The route's IP limiter cannot do this job: it
	// budgets per CALLER, so rotating source addresses multiplied the budget
	// and let an attacker mailbomb a known member and burn the operator's SMTP
	// quota.
	resetThrottle *emailThrottle
}

// emailThrottle is a tiny per-key fixed-window limiter. In-process and
// non-persistent on purpose: it exists to blunt a flood, and a restart
// clearing it is not a security property anyone relies on.
type emailThrottle struct {
	mu     sync.Mutex
	window time.Duration
	limit  int
	seen   map[string][]time.Time
}

func newEmailThrottle(window time.Duration, limit int) *emailThrottle {
	return &emailThrottle{window: window, limit: limit, seen: make(map[string][]time.Time)}
}

// allow reports whether another send to key is permitted now, recording it if
// so. Keys are normalised so casing/whitespace variants share one budget.
func (t *emailThrottle) allow(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	now := time.Now()
	cutoff := now.Add(-t.window)

	t.mu.Lock()
	defer t.mu.Unlock()
	// Opportunistic sweep so the map cannot grow without bound from an
	// attacker cycling addresses.
	if len(t.seen) > emailThrottleMaxKeys {
		for k2, ts := range t.seen {
			if len(ts) == 0 || ts[len(ts)-1].Before(cutoff) {
				delete(t.seen, k2)
			}
		}
	}
	kept := t.seen[k][:0]
	for _, ts := range t.seen[k] {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	if len(kept) >= t.limit {
		t.seen[k] = kept
		return false
	}
	t.seen[k] = append(kept, now)
	return true
}

const (
	// Three mails an hour to one address: enough for a user who fumbles the
	// flow, far short of a mailbomb.
	resetThrottleWindow = time.Hour
	resetThrottleLimit  = 3
	// Bound on distinct tracked addresses before the sweep runs.
	emailThrottleMaxKeys = 10_000
)

// NewPasswordResetHandler creates a PasswordResetHandler.
func NewPasswordResetHandler(db PasswordResetDB, sender *email.Sender, baseURL string, logger *slog.Logger) *PasswordResetHandler {
	return &PasswordResetHandler{
		db: db, sender: sender, baseURL: baseURL, logger: logger,
		resetThrottle: newEmailThrottle(resetThrottleWindow, resetThrottleLimit),
	}
}

// WithSegmentTokenRevoker attaches the HLS segment-token revoker so a
// successful password reset also wipes in-flight playback credentials.
// Without it, an attacker holding a stolen segment token keeps streaming
// for up to 4h after the legitimate user resets their password.
func (h *PasswordResetHandler) WithSegmentTokenRevoker(r SegmentTokenRevoker) *PasswordResetHandler {
	h.segTokens = r
	return h
}

// WithAudit attaches an audit logger so successful password resets are
// recorded. Returns the handler for chaining.
func (h *PasswordResetHandler) WithAudit(a *audit.Logger) *PasswordResetHandler {
	h.audit = a
	return h
}

// Enabled returns whether the forgot password flow is available.
func (h *PasswordResetHandler) Enabled(w http.ResponseWriter, r *http.Request) {
	respond.Success(w, r, map[string]bool{"enabled": h.sender.Enabled(r.Context())})
}

// ForgotPassword handles POST /api/v1/auth/forgot-password.
// Sends a password reset email if the email exists. Always returns 200
// to prevent email + SMTP-config enumeration.
//
// The previous version returned 400 ("Email is not configured on this
// server") when SMTP was off, which let an unauthenticated probe
// distinguish "this server has email enabled" from "this server
// doesn't." Combined with the existing /auth/forgot-password/enabled
// admin endpoint, that's redundant — but cheap enough to fix here so
// the failure-mode timing/response shape stays uniform.
func (h *PasswordResetHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		respond.BadRequest(w, r, "email is required")
		return
	}

	// Answer NOW, identically, before doing any work whose duration depends on
	// whether the address exists.
	//
	// This used to be a deferred Success, which runs at return — so the body
	// was uniform but the LATENCY was not. An unknown address returned after a
	// single SELECT; a known one additionally inserted a token and ran a fully
	// synchronous SMTP session (dial, TLS, AUTH, DATA). Against a hosted relay
	// that is routinely 300 ms to 3 s, so one probe classified an address with
	// no statistics required — the only pre-auth email-existence oracle in the
	// API, since login takes a username and registration needs an admin.
	respond.Success(w, r, map[string]string{"message": "If an account with that email exists, a password reset link has been sent."})

	// Email disabled on this server: log so the operator can spot the
	// stuck flow. The response above already went out unchanged.
	if !h.sender.Enabled(r.Context()) {
		h.logger.InfoContext(r.Context(), "forgot password: SMTP not configured; silently dropping request")
		return
	}

	// Per-account send throttle. There was none, so an attacker who knew one
	// member's address could mailbomb them and burn the operator's SMTP quota
	// at whatever rate the IP limiter allowed — and that limiter was itself
	// bypassable until the X-Forwarded-For fix. Keyed on the address rather
	// than the caller, so rotating source IPs does not multiply the budget.
	if !h.resetThrottle.allow(body.Email) {
		h.logger.InfoContext(r.Context(), "forgot password: per-account send throttle hit; dropping",
			"email_hash", hashEmailForLog(body.Email))
		return
	}

	// Everything past here runs detached: the client already has its
	// response, and the work must not be cancellable by the caller
	// disconnecting (which would also reintroduce a timing signal).
	// context.WithoutCancel keeps request-scoped values — same pattern the arr
	// webhook uses for its detached scan.
	ctx := context.WithoutCancel(r.Context())
	emailAddr := body.Email
	observability.SafeGo(h.logger, "auth:password-reset-send", func() {
		user, err := h.db.GetUserByEmail(ctx, &emailAddr)
		if err != nil {
			return // user not found — nothing to do, and nothing observable
		}

		// Generate a secure random token.
		tokenBytes := make([]byte, 32)
		if _, err := rand.Read(tokenBytes); err != nil {
			h.logger.ErrorContext(ctx, "password reset: generate token", "err", err)
			return
		}
		rawToken := hex.EncodeToString(tokenBytes)

		// Store the hash (not the raw token) in the DB.
		hash := sha256.Sum256([]byte(rawToken))
		tokenHash := hex.EncodeToString(hash[:])

		if err := h.db.CreateResetToken(ctx, user.ID, tokenHash, time.Now().Add(time.Hour)); err != nil {
			h.logger.ErrorContext(ctx, "password reset: store token", "err", err)
			return
		}

		// Send the email with the raw token (user clicks link, we hash and look up).
		resetURL := h.baseURL + "/reset-password?token=" + rawToken
		subject, htmlBody := email.PasswordResetEmail(user.Username, resetURL)
		if err := h.sender.Send(ctx, []string{emailAddr}, subject, htmlBody); err != nil {
			h.logger.ErrorContext(ctx, "password reset: send email", "err", err)
		}
	})
}

// hashEmailForLog returns a short, stable, non-reversible tag for an address
// so throttle hits are correlatable in logs without writing the address
// itself into logcat/journald.
func hashEmailForLog(addr string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(addr))))
	return hex.EncodeToString(sum[:6])
}

// ResetPassword handles POST /api/v1/auth/reset-password.
// Validates the token and sets the new password.
func (h *PasswordResetHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if body.Token == "" || body.Password == "" {
		respond.BadRequest(w, r, "token and password are required")
		return
	}
	if err := ValidatePassword(body.Password); err != nil {
		respond.BadRequest(w, r, err.Error())
		return
	}

	// Hash the token to look up in DB.
	hash := sha256.Sum256([]byte(body.Token))
	tokenHash := hex.EncodeToString(hash[:])

	token, err := h.db.GetResetToken(r.Context(), tokenHash)
	if err != nil {
		respond.BadRequest(w, r, "Invalid or expired reset link")
		return
	}

	// Hash the new password with bcrypt.
	pwHash, err := bcrypt.GenerateFromPassword([]byte(body.Password), 12)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: hash password", "err", err)
		respond.InternalError(w, r)
		return
	}

	// Atomically claim the token BEFORE writing the password. This
	// closes the race where two concurrent submissions of the same
	// token both pass GetResetToken's "used_at IS NULL" check, both
	// run UpdatePassword last-write-wins, and both fire the
	// session-revocation cascade. With the conditional UPDATE, only
	// the first request's claim affects rows; the loser sees won=false
	// and bails with the same generic "invalid or expired" message.
	won, err := h.db.MarkResetTokenUsed(r.Context(), token.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: claim token", "err", err)
		respond.InternalError(w, r)
		return
	}
	if !won {
		respond.BadRequest(w, r, "Invalid or expired reset link")
		return
	}

	if err := h.db.UpdatePassword(r.Context(), token.UserID, string(pwHash)); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: update password", "err", err)
		respond.InternalError(w, r)
		return
	}

	// Cut every existing credential for the user. The whole point of
	// "forgot password" is recovery from compromise — leaving the old
	// PASETO access tokens (1h TTL) and refresh tokens (30d) live would
	// hand the attacker a continued session even after the legitimate
	// owner reset. Bump the epoch (revokes access tokens) AND wipe the
	// sessions table (revokes the refresh path).
	if err := h.db.BumpSessionEpoch(r.Context(), token.UserID); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: bump session epoch", "err", err)
		respond.InternalError(w, r)
		return
	}
	if err := h.db.DeleteSessionsForUser(r.Context(), token.UserID); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: delete sessions", "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.segTokens != nil {
		if err := h.segTokens.RevokeAllForUser(r.Context(), token.UserID); err != nil {
			// Log but don't fail — the password is already changed and the
			// PASETO/refresh paths are revoked. Segment tokens still age
			// out on their own 4h TTL.
			h.logger.WarnContext(r.Context(), "password reset: revoke segment tokens", "err", err)
		}
	}

	// Token was already claimed atomically before UpdatePassword above.
	if h.audit != nil {
		uid := token.UserID
		h.audit.Log(r.Context(), &uid, audit.ActionPasswordReset, uid.String(), nil, audit.ClientIP(r))
	}

	respond.Success(w, r, map[string]string{"message": "Password has been reset. You can now sign in."})
}
