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
}

type authService struct {
	db     authQuerier
	tokens *auth.TokenMaker
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
	return &v1.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: raw,
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
		ExpiresAt:    expiry,
		UserID:       user.ID,
		Username:     user.Username,
		IsAdmin:      user.IsAdmin,
	}, nil
}
