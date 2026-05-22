package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/valkey"
)

// authQuerier is the DB subset needed by authService.
type authQuerier interface {
	CountUsers(ctx context.Context) (int64, error)
	GetUser(ctx context.Context, id uuid.UUID) (gen.User, error)
	GetUserByUsername(ctx context.Context, username string) (gen.User, error)
	CreateUser(ctx context.Context, arg gen.CreateUserParams) (gen.User, error)
	CreateFirstAdmin(ctx context.Context, arg gen.CreateFirstAdminParams) (gen.User, error)
	// GrantAutoLibrariesToUser inserts library_access rows for every
	// library flagged auto_grant_new_users. Called from CreateUser so
	// fresh accounts on all-private installs default into the
	// admin-chosen library set instead of seeing nothing.
	GrantAutoLibrariesToUser(ctx context.Context, userID uuid.UUID) error
	CreateSession(ctx context.Context, arg gen.CreateSessionParams) (gen.Session, error)
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (gen.Session, error)
	RotateSession(ctx context.Context, arg gen.RotateSessionParams) (gen.Session, error)
	// RotateSessionConditional is the compare-and-swap rotation used by
	// refresh-token reuse detection. Returns the rows affected (1 = ok,
	// 0 = the token was already rotated by someone else, i.e. theft).
	RotateSessionConditional(ctx context.Context, arg gen.RotateSessionConditionalParams) (int64, error)
	DeleteSession(ctx context.Context, id uuid.UUID) error
	DeleteSessionsForUser(ctx context.Context, userID uuid.UUID) error
	TouchSession(ctx context.Context, id uuid.UUID) error
	// BumpSessionEpoch increments the user's session_epoch so any
	// outstanding access / stream tokens get rejected by the auth
	// middleware. Logout calls it; admin demote and user delete
	// already do.
	BumpSessionEpoch(ctx context.Context, id uuid.UUID) error
	// TOTP / 2FA.
	SetUserTOTPSecret(ctx context.Context, arg gen.SetUserTOTPSecretParams) error
	ActivateUserTOTP(ctx context.Context, id uuid.UUID) error
	DisableUserTOTP(ctx context.Context, id uuid.UUID) error
	InsertTOTPRecoveryCode(ctx context.Context, arg gen.InsertTOTPRecoveryCodeParams) error
	DeleteTOTPRecoveryCodes(ctx context.Context, userID uuid.UUID) error
	ConsumeTOTPRecoveryCode(ctx context.Context, arg gen.ConsumeTOTPRecoveryCodeParams) (int64, error)
	CountUnusedTOTPRecoveryCodes(ctx context.Context, userID uuid.UUID) (int64, error)
}

type authService struct {
	db     authQuerier
	tokens *auth.TokenMaker
	enc    *auth.Encryptor // at-rest encryption for the TOTP secret
	logger *slog.Logger
	// rateLimiter enforces per-username login throttling on top of
	// the per-IP /auth/login rate limit. Per-IP alone doesn't stop
	// a botnet (or CGNAT pool) credential-stuffing one account from
	// many addresses; per-username caps total failures regardless
	// of source. Optional — nil disables the per-username check
	// (used by tests that don't wire Valkey).
	rateLimiter *valkey.RateLimiter
	// usernamePepper keys the HMAC used to derive the Valkey rate-limit
	// key and any log fields that would otherwise carry the raw
	// attempted username. See auth.HashUsernameForLog. Optional — when
	// nil, the rate-limit key falls back to the lowercased username
	// (still functional, just not opaque).
	usernamePepper []byte
}

// MaxLoginFailuresPerUsername / loginFailureWindow set the per-username
// brute-force cap. After this many failures within the window, further
// attempts are rejected with "too many failed logins, try again later"
// regardless of source IP. Cleared automatically when the window
// elapses (sliding window in Valkey).
const (
	MaxLoginFailuresPerUsername = 10
	loginFailureWindow          = 15 * time.Minute
)

func (s *authService) UserCount(ctx context.Context) (int64, error) {
	return s.db.CountUsers(ctx)
}

// CreateFirstAdmin atomically creates the first user as admin only if
// the users table is empty. Returns v1.ErrNotFirstUser when another
// goroutine (or operator) has already completed setup — the loser of
// the race gets a clean error instead of a spurious unique-constraint
// conflict or a silent "second admin created" incident.
func (s *authService) CreateFirstAdmin(ctx context.Context, username, email, password string) (*v1.UserInfo, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	hashStr := string(hash)
	var emailPtr *string
	if email != "" {
		emailPtr = &email
	}
	user, err := s.db.CreateFirstAdmin(ctx, gen.CreateFirstAdminParams{
		Username:     username,
		Email:        emailPtr,
		PasswordHash: &hashStr,
	})
	if err != nil {
		// pgx returns ErrNoRows when the WHERE NOT EXISTS clause filters
		// out the insert — that's our "setup already done" signal.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, v1.ErrNotFirstUser
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, v1.ErrUserExists
		}
		return nil, fmt.Errorf("create first admin: %w", err)
	}
	return &v1.UserInfo{ID: user.ID, Username: user.Username, IsAdmin: user.IsAdmin}, nil
}

func (s *authService) CreateUser(ctx context.Context, username, email, password string, isAdmin bool) (*v1.UserInfo, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	hashStr := string(hash)
	var emailPtr *string
	if email != "" {
		emailPtr = &email
	}
	user, err := s.db.CreateUser(ctx, gen.CreateUserParams{
		Username:     username,
		Email:        emailPtr,
		PasswordHash: &hashStr,
		IsAdmin:      isAdmin,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, v1.ErrUserExists
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	// Auto-grant: gives the new account access to every library the
	// admin flagged for default access. Logged-but-not-fatal — a missing
	// grant degrades UX (empty home page) but doesn't break account
	// creation, and admins can backfill manually via /settings/users.
	if !isAdmin {
		if err := s.db.GrantAutoLibrariesToUser(ctx, user.ID); err != nil {
			s.logger.WarnContext(ctx, "auto-grant libraries", "user_id", user.ID, "err", err)
		}
	}
	return &v1.UserInfo{ID: user.ID, Username: user.Username, IsAdmin: user.IsAdmin}, nil
}

// dummyBcryptHash is used to make user-not-found and user-without-password
// paths take roughly the same time as the real bcrypt compare. Cost-12
// matches the cost we use for stored hashes (see cmd/server/auth_service.go
// CreateUser / SetPassword), so the dummy compare runs in the same ~100-300
// ms window as a real password check. Without this, a login attempt on a
// non-existent username returns immediately while a real account spends
// ~150 ms in bcrypt — trivially measurable, lets an attacker enumerate
// valid usernames before brute-forcing.
//
// Generated once with: bcrypt.GenerateFromPassword([]byte("not_a_real_password"), 12).
// Value is constant so we don't burn 150 ms at process start.
var dummyBcryptHash = []byte("$2a$12$L6uF.4eJ4MCJ.5J3rYY.X.D6CzQ/uCqgQRdWJq3.2Ey0Wc5DQ4XwS")

func (s *authService) LoginLocal(ctx context.Context, username, password string) (*v1.TokenPair, error) {
	// Per-username brute-force throttle. Per-IP rate-limit (10/min)
	// already wraps the route, but a botnet or CGNAT pool defeats
	// per-IP — credential stuffing one account from many IPs sails
	// past it. Per-username caps total *failures* regardless of source.
	//
	// Failure-only counter: CheckFailures reads without incrementing;
	// IncrFailure runs only on a confirmed bad login; ResetFailures
	// clears the counter on success. The earlier sliding-window form
	// counted successes too, so a user who logs in `limit` times
	// legitimately would lock themselves out — fixed here.
	//
	// Counter is keyed by the username the caller is *trying*, not by
	// whether it exists. An enumerator probing usernames burns through
	// the same cap and gets the same response either way, so the throttle
	// itself doesn't leak existence.
	uHash := ""
	if s.usernamePepper != nil {
		uHash = auth.HashUsernameForLog(s.usernamePepper, username)
	} else {
		// No pepper configured (test path): fall back to lowercased username.
		uHash = strings.ToLower(strings.TrimSpace(username))
	}
	rlKey := "ratelimit:auth_user:" + uHash
	if s.rateLimiter != nil && username != "" {
		allowed, _ := s.rateLimiter.CheckFailures(ctx, rlKey, MaxLoginFailuresPerUsername)
		if !allowed {
			// Log the hash, not the username — operator logs and any retained
			// archive shouldn't accumulate raw attempted usernames. The hash
			// is enough to correlate against the Valkey counter for ops
			// triage. (See auth.HashUsernameForLog.)
			s.logger.WarnContext(ctx, "per-username login throttle hit",
				"username_hash", uHash)
			return nil, fmt.Errorf("too many failed logins; try again in 15 minutes")
		}
	}

	recordFailure := func() {
		if s.rateLimiter != nil && username != "" {
			s.rateLimiter.IncrFailure(ctx, rlKey, loginFailureWindow)
		}
	}

	user, err := s.db.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Run a dummy bcrypt compare so timing matches the success
			// path. Otherwise login latency reveals whether the username
			// exists, enabling enumeration ahead of credential stuffing.
			_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
			recordFailure()
			return nil, fmt.Errorf("invalid credentials")
		}
		return nil, fmt.Errorf("login: %w", err)
	}
	if user.PasswordHash == nil {
		// Federated user (OIDC/SAML/LDAP) trying to log in via password —
		// same dummy compare so this path can't be distinguished from
		// "user does not exist" or "wrong password" by timing.
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(password))
		recordFailure()
		return nil, fmt.Errorf("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(password)); err != nil {
		recordFailure()
		return nil, fmt.Errorf("invalid credentials")
	}
	// Success: clear the failure counter so a user who eventually got
	// their password right starts fresh next time.
	if s.rateLimiter != nil && username != "" {
		s.rateLimiter.ResetFailures(ctx, rlKey)
	}
	// Second-factor gate: a TOTP-enabled local account doesn't get a
	// session from the password alone. Mint a short-lived challenge token
	// and tell the client to collect a code; /auth/totp/verify completes
	// the login. (Federated accounts never have totp_enabled set.)
	if user.TotpEnabled {
		challenge, err := s.tokens.IssueTOTPChallengeToken(user.ID)
		if err != nil {
			return nil, fmt.Errorf("issue totp challenge: %w", err)
		}
		return &v1.TokenPair{
			TOTPRequired:        true,
			LoginChallengeToken: challenge,
			UserID:              user.ID,
			Username:            user.Username,
		}, nil
	}
	return s.issueTokenPair(ctx, user)
}

func (s *authService) Refresh(ctx context.Context, refreshToken string) (*v1.TokenPair, error) {
	hash := auth.HashToken(refreshToken)
	session, err := s.db.GetSessionByTokenHash(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("refresh: session not found or expired")
	}
	user, err := s.db.GetUser(ctx, session.UserID)
	if err != nil {
		return nil, fmt.Errorf("refresh: get user: %w", err)
	}

	raw, newHash, err := auth.IssueRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("refresh: issue token: %w", err)
	}
	expiry := time.Now().Add(auth.RefreshTokenTTL)

	// Compare-and-swap rotation: rotate ONLY if the row's current
	// token_hash still matches the one the caller presented. Refresh
	// tokens are one-shot; if the same token has already been rotated
	// (i.e. somebody else used it before us), the row count is 0 and
	// we treat this as theft.
	//
	// Reuse-detection response: invalidate the entire session family for
	// the user (DeleteSessionsForUser + BumpSessionEpoch). The legitimate
	// owner gets logged out on every device; better than a silent leak
	// where attacker + victim share refresh access. Audit log lets ops
	// see why every device suddenly logged out.
	rows, err := s.db.RotateSessionConditional(ctx, gen.RotateSessionConditionalParams{
		ID:        session.ID,
		TokenHash: newHash,
		ExpiresAt: pgtype.Timestamptz{Time: expiry, Valid: true},
		// gen.RotateSessionConditionalParams's 4th param is the previous
		// token_hash to compare against. sqlc names it after the column;
		// we send the hash we just looked up by.
		TokenHash_2: hash,
	})
	if err != nil {
		return nil, fmt.Errorf("refresh: rotate session: %w", err)
	}
	if rows == 0 {
		// Theft path: the token we just verified got rotated by someone
		// between our lookup and our rotate. Burn the whole session
		// family so the thief and the legitimate owner BOTH get logged
		// out — neither side can keep refreshing from this point.
		s.logger.WarnContext(ctx, "refresh token reuse detected; invalidating session family",
			"user_id", session.UserID, "session_id", session.ID)
		_ = s.db.DeleteSessionsForUser(ctx, session.UserID)
		_ = s.db.BumpSessionEpoch(ctx, session.UserID)
		return nil, fmt.Errorf("refresh: token already used; session invalidated")
	}

	refreshClaims := auth.Claims{
		UserID:       user.ID,
		Username:     user.Username,
		IsAdmin:      user.IsAdmin,
		SessionEpoch: user.SessionEpoch,
	}
	if user.MaxContentRating != nil {
		refreshClaims.MaxContentRating = *user.MaxContentRating
	}
	accessToken, err := s.tokens.IssueAccessToken(refreshClaims)
	if err != nil {
		return nil, fmt.Errorf("refresh: issue access token: %w", err)
	}
	assetToken, err := s.tokens.IssueAssetToken(refreshClaims)
	if err != nil {
		return nil, fmt.Errorf("refresh: issue asset token: %w", err)
	}
	return &v1.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: raw,
		AssetToken:   assetToken,
		ExpiresAt:    expiry,
		UserID:       user.ID,
		Username:     user.Username,
		IsAdmin:      user.IsAdmin,
	}, nil
}

func (s *authService) Logout(ctx context.Context, refreshToken string) error {
	hash := auth.HashToken(refreshToken)
	session, err := s.db.GetSessionByTokenHash(ctx, hash)
	if err != nil {
		return nil // already gone
	}
	if err := s.db.DeleteSession(ctx, session.ID); err != nil {
		return err
	}
	// Bump the user's session_epoch so any outstanding access /
	// stream tokens minted under the old epoch get rejected by the
	// auth middleware on the next request. Without this, a 1 h
	// access token (or 24 h stream token) keeps working after
	// "log out" until its natural TTL elapses — the user's
	// expectation is that logout invalidates *now*. Best-effort
	// because session was already deleted; a transient DB error
	// here doesn't fail the logout, but it does leave the window
	// open until the token's TTL.
	if err := s.db.BumpSessionEpoch(ctx, session.UserID); err != nil {
		s.logger.WarnContext(ctx, "logout: bump session epoch", "err", err, "user_id", session.UserID)
	}
	return nil
}

// ── TOTP / 2FA ──────────────────────────────────────────────────────────────

// SetupTOTP stages a fresh encrypted secret (totp_enabled stays false)
// and returns the otpauth URI + base32 secret for the client to render.
func (s *authService) SetupTOTP(ctx context.Context, userID uuid.UUID, accountName string) (string, string, error) {
	user, err := s.db.GetUser(ctx, userID)
	if err != nil {
		return "", "", fmt.Errorf("setup totp: get user: %w", err)
	}
	if user.TotpEnabled {
		return "", "", v1.ErrTOTPAlreadyEnabled
	}
	// Federated (OIDC/SAML/LDAP) accounts have no password and never run
	// LoginLocal, so a local TOTP secret would never be checked. Refuse
	// rather than hand them a false sense of protection.
	if user.PasswordHash == nil {
		return "", "", v1.ErrTOTPLocalOnly
	}
	secret, url, err := auth.GenerateTOTPSecret(accountName)
	if err != nil {
		return "", "", err
	}
	ciphertext, err := s.enc.Encrypt(secret)
	if err != nil {
		return "", "", fmt.Errorf("setup totp: encrypt secret: %w", err)
	}
	if err := s.db.SetUserTOTPSecret(ctx, gen.SetUserTOTPSecretParams{ID: userID, TotpSecret: &ciphertext}); err != nil {
		return "", "", fmt.Errorf("setup totp: store secret: %w", err)
	}
	return url, secret, nil
}

// ActivateTOTP confirms the staged secret with a live code, enables 2FA,
// and returns fresh single-use recovery codes.
func (s *authService) ActivateTOTP(ctx context.Context, userID uuid.UUID, code string) ([]string, error) {
	user, err := s.db.GetUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("activate totp: get user: %w", err)
	}
	if user.TotpSecret == nil || *user.TotpSecret == "" {
		return nil, v1.ErrTOTPNotPending
	}
	secret, err := s.enc.Decrypt(*user.TotpSecret)
	if err != nil {
		return nil, fmt.Errorf("activate totp: decrypt secret: %w", err)
	}
	if !auth.ValidateTOTPCode(code, secret) {
		return nil, v1.ErrBadTOTPCode
	}
	if err := s.db.ActivateUserTOTP(ctx, userID); err != nil {
		return nil, fmt.Errorf("activate totp: enable: %w", err)
	}
	// Fresh codes — clear any leftovers from a prior enrolment.
	if err := s.db.DeleteTOTPRecoveryCodes(ctx, userID); err != nil {
		return nil, fmt.Errorf("activate totp: clear old codes: %w", err)
	}
	display, hashes, err := auth.GenerateRecoveryCodes(auth.RecoveryCodeCount)
	if err != nil {
		return nil, err
	}
	for _, h := range hashes {
		if err := s.db.InsertTOTPRecoveryCode(ctx, gen.InsertTOTPRecoveryCodeParams{UserID: userID, CodeHash: h}); err != nil {
			return nil, fmt.Errorf("activate totp: store recovery code: %w", err)
		}
	}
	return display, nil
}

// DisableTOTP turns 2FA off after re-proving possession (a current code
// or recovery code) and wipes the secret + recovery codes.
func (s *authService) DisableTOTP(ctx context.Context, userID uuid.UUID, code string) error {
	user, err := s.db.GetUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("disable totp: get user: %w", err)
	}
	if !user.TotpEnabled {
		return nil // already off — idempotent
	}
	ok, err := s.validateSecondFactor(ctx, user, code)
	if err != nil {
		return err
	}
	if !ok {
		return v1.ErrBadTOTPCode
	}
	if err := s.db.DisableUserTOTP(ctx, userID); err != nil {
		return fmt.Errorf("disable totp: %w", err)
	}
	if err := s.db.DeleteTOTPRecoveryCodes(ctx, userID); err != nil {
		return fmt.Errorf("disable totp: clear codes: %w", err)
	}
	return nil
}

// VerifyTOTPLogin validates the login challenge token + second factor and
// issues the real token pair.
func (s *authService) VerifyTOTPLogin(ctx context.Context, challengeToken, code string) (*v1.TokenPair, error) {
	claims, err := s.tokens.ValidateAccessToken(challengeToken)
	if err != nil || claims == nil || claims.Purpose != "totp_challenge" {
		return nil, v1.ErrInvalidTOTPChallenge
	}
	user, err := s.db.GetUser(ctx, claims.UserID)
	if err != nil {
		return nil, v1.ErrInvalidTOTPChallenge
	}
	// 2FA disabled between password and verify → the challenge is stale.
	if !user.TotpEnabled {
		return nil, v1.ErrInvalidTOTPChallenge
	}
	ok, err := s.validateSecondFactor(ctx, user, code)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, v1.ErrBadTOTPCode
	}
	return s.issueTokenPair(ctx, user)
}

// TOTPStatus reports enablement + how many unused recovery codes remain.
func (s *authService) TOTPStatus(ctx context.Context, userID uuid.UUID) (bool, int, error) {
	user, err := s.db.GetUser(ctx, userID)
	if err != nil {
		return false, 0, fmt.Errorf("totp status: get user: %w", err)
	}
	if !user.TotpEnabled {
		return false, 0, nil
	}
	n, err := s.db.CountUnusedTOTPRecoveryCodes(ctx, userID)
	if err != nil {
		return true, 0, fmt.Errorf("totp status: count codes: %w", err)
	}
	return true, int(n), nil
}

// validateSecondFactor accepts EITHER a live TOTP code OR an unused
// recovery code (consumed on success). A valid TOTP code never consumes
// a recovery code; a recovery code is single-use and burned atomically.
func (s *authService) validateSecondFactor(ctx context.Context, user gen.User, code string) (bool, error) {
	if user.TotpSecret != nil && *user.TotpSecret != "" {
		secret, err := s.enc.Decrypt(*user.TotpSecret)
		if err != nil {
			return false, fmt.Errorf("validate 2fa: decrypt secret: %w", err)
		}
		if auth.ValidateTOTPCode(code, secret) {
			return true, nil
		}
	}
	norm := auth.NormalizeRecoveryCode(code)
	if norm == "" {
		return false, nil
	}
	rows, err := s.db.ConsumeTOTPRecoveryCode(ctx, gen.ConsumeTOTPRecoveryCodeParams{
		UserID:   user.ID,
		CodeHash: auth.HashToken(norm),
	})
	if err != nil {
		return false, fmt.Errorf("validate 2fa: consume recovery code: %w", err)
	}
	return rows == 1, nil
}

// issueTokenPair creates an access + refresh token and persists the session.
func (s *authService) issueTokenPair(ctx context.Context, user gen.User) (*v1.TokenPair, error) {
	claims := auth.Claims{
		UserID:       user.ID,
		Username:     user.Username,
		IsAdmin:      user.IsAdmin,
		SessionEpoch: user.SessionEpoch,
	}
	if user.MaxContentRating != nil {
		claims.MaxContentRating = *user.MaxContentRating
	}
	accessToken, err := s.tokens.IssueAccessToken(claims)
	if err != nil {
		return nil, fmt.Errorf("issue access token: %w", err)
	}
	assetToken, err := s.tokens.IssueAssetToken(claims)
	if err != nil {
		return nil, fmt.Errorf("issue asset token: %w", err)
	}

	raw, hash, err := auth.IssueRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("issue refresh token: %w", err)
	}

	expiry := time.Now().Add(auth.RefreshTokenTTL)
	if _, err = s.db.CreateSession(ctx, gen.CreateSessionParams{
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{Time: expiry, Valid: true},
	}); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	return &v1.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: raw,
		AssetToken:   assetToken,
		ExpiresAt:    expiry,
		UserID:       user.ID,
		Username:     user.Username,
		IsAdmin:      user.IsAdmin,
	}, nil
}
