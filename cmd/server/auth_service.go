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
	// Matches the current OR the immediately-previous hash, so a superseded
	// refresh token resolves to its session instead of looking like garbage.
	// That is what makes reuse detection reachable — see Refresh.
	GetSessionByAnyTokenHash(ctx context.Context, tokenHash string) (gen.GetSessionByAnyTokenHashRow, error)
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
	// totpReplay is the shared record of spent TOTP time steps (Valkey in
	// production) that makes a code single-use across instances. Optional:
	// nil — or a Valkey error at check time — falls back to totpReplayLocal,
	// which protects this process only. Never skipped outright.
	totpReplay      auth.TOTPReplayGuard
	totpReplayLocal auth.MemoryTOTPReplayGuard
	// totpChallenges records redeemed TOTP login challenges (by jti) so one
	// challenge mints at most one session. Same shape as totpReplay: shared
	// Valkey guard when wired, totpChallengesLocal as the per-instance
	// fallback (see redeemTOTPChallenge).
	totpChallenges      auth.TOTPChallengeGuard
	totpChallengesLocal auth.MemoryTOTPChallengeGuard
	// segTokens revokes every outstanding HLS segment token for a user.
	// Used on the refresh-reuse (theft) path, which burns the whole session
	// family. Optional — nil leaves segment tokens to their idle TTL.
	segTokens segmentTokenRevoker
	// now is the clock for TOTP step matching; nil = time.Now (tests pin it).
	now func() time.Time
}

// segmentTokenRevoker is satisfied by *transcode.SegmentTokenManager.
type segmentTokenRevoker interface {
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
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

// MaxTOTPFailuresPerUser / totpFailureWindow cap second-factor (TOTP /
// recovery-code) guesses per user. The per-IP AuthLimit alone is defeated by a
// botnet/CGNAT pool, and a correct password re-mints a fresh challenge token
// without touching the per-username login counter — so without a per-USER cap
// on the verify step an attacker holding the password could grind the 10^6 code
// space. Keyed by user ID so re-minting challenges can't reset it.
//
// In-session step-up — DisableTOTP (code) and VerifyReauth (password and code;
// it gates the SECRET_KEY/DB-URL reveal and backup restore) — has its OWN
// per-user budget of the same size, shared by both endpoints so an attacker
// can't get a fresh 10 guesses per endpoint. It is deliberately separate from
// the login verify budget: step-up is reachable from any live session with no
// password, so sharing one counter let a stolen session burn it and lock the
// real user out of signing in (and so out of revoking that very session).
const (
	MaxTOTPFailuresPerUser = 10
	totpFailureWindow      = 15 * time.Minute
)

// loginTOTPFailureKey is the per-user counter for the pre-session second-factor
// step of login (TOTP or recovery code). The key name predates this split, so
// counters already in Valkey carry over.
func loginTOTPFailureKey(userID uuid.UUID) string {
	return "ratelimit:totp_verify:" + userID.String()
}

// stepUpFailureKey is the per-user counter for in-session re-proofs: TOTP
// disable and step-up reauth. Kept apart from loginTOTPFailureKey — see the
// MaxTOTPFailuresPerUser note.
func stepUpFailureKey(userID uuid.UUID) string {
	return "ratelimit:stepup:" + userID.String()
}

// errStepUpLocked is returned by VerifyReauth once the per-user cap is hit.
// Callers already collapse every reauth failure into one REAUTH_FAILED
// response, so it is not distinguishable from a wrong password on the wire.
var errStepUpLocked = errors.New("too many failed verification attempts; try again in 15 minutes")

// reserveStepUp atomically takes one slot under the per-user cap held at key
// (see valkey.RateLimiter.CheckFailures). false = locked out.
func (s *authService) reserveStepUp(ctx context.Context, key string, userID uuid.UUID) bool {
	if s.rateLimiter == nil {
		return true
	}
	allowed, _ := s.rateLimiter.CheckFailures(ctx, key, MaxTOTPFailuresPerUser)
	if !allowed {
		s.logger.WarnContext(ctx, "per-user step-up / TOTP throttle hit", "user_id", userID)
	}
	return allowed
}

func (s *authService) recordStepUpFailure(ctx context.Context, key string) {
	if s.rateLimiter != nil {
		s.rateLimiter.IncrFailure(ctx, key, totpFailureWindow)
	}
}

func (s *authService) resetStepUpFailures(ctx context.Context, key string) {
	if s.rateLimiter != nil {
		s.rateLimiter.ResetFailures(ctx, key)
	}
}

// validateTOTPOnce reports whether code is a live TOTP code for secret AND its
// time step has not been spent by this user before; a success spends it. So an
// observed code (shoulder-surf, shared screen, phishing relay) can't be
// replayed inside its ~90 s validity window, and two concurrent submissions of
// one code can't both pass — the guard's check-and-record is atomic.
//
// The shared Valkey guard is authoritative across instances. If it is not
// wired or errors, the in-process guard still refuses replays on this
// instance rather than skipping the check. Every success is recorded locally
// too, so a later Valkey outage doesn't forget steps spent before it.
func (s *authService) validateTOTPOnce(ctx context.Context, userID uuid.UUID, code, secret string) bool {
	step, ok := auth.MatchTOTPStep(code, secret, s.clock())
	if !ok {
		return false
	}
	return s.spendTOTPStep(ctx, userID, step)
}

// spendTOTPStep consumes a matched time step for userID; false = already spent.
func (s *authService) spendTOTPStep(ctx context.Context, userID uuid.UUID, step int64) bool {
	localFresh, _ := s.totpReplayLocal.ConsumeTOTPStep(ctx, userID, step)
	if s.totpReplay != nil {
		fresh, err := s.totpReplay.ConsumeTOTPStep(ctx, userID, step)
		if err == nil {
			return fresh && localFresh
		}
		s.logger.WarnContext(ctx, "totp replay guard unavailable; using per-instance guard",
			"user_id", userID, "err", err)
	}
	return localFresh
}

// challengeBurnSlack keeps a redeemed-challenge record a little past the
// token's own expiry, so an instance whose clock runs behind the burning one
// still finds the record for as long as it would accept the token.
const challengeBurnSlack = time.Minute

// redeemTOTPChallenge burns a login challenge's jti; false = already redeemed.
// Same posture as spendTOTPStep: the shared guard is authoritative across
// instances, and a Valkey outage falls back to the in-process record rather
// than skipping the check. Every burn the shared guard grants is recorded
// locally too, so an outage later doesn't forget it. The shared guard goes
// first and a loss there returns before touching the local record — that
// ordering guarantees exactly one of several concurrent verifies wins, where
// checking both in either order could let each refuse the other.
func (s *authService) redeemTOTPChallenge(ctx context.Context, jti string, expiresAt time.Time) bool {
	ttl := time.Until(expiresAt) + challengeBurnSlack
	if s.totpChallenges != nil {
		fresh, err := s.totpChallenges.RedeemTOTPChallenge(ctx, jti, ttl)
		if err == nil && !fresh {
			return false
		}
		if err != nil {
			s.logger.WarnContext(ctx, "totp challenge guard unavailable; using per-instance guard", "err", err)
		}
	}
	localFresh, _ := s.totpChallengesLocal.RedeemTOTPChallenge(ctx, jti, ttl)
	return localFresh
}

func (s *authService) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

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
	// The PL/pgSQL function NULLIFs an empty email so the partial unique
	// index on email-when-not-null doesn't collide across blank-email
	// users — Go side stays straightforward.
	user, err := s.db.CreateFirstAdmin(ctx, gen.CreateFirstAdminParams{
		Username:     username,
		Email:        email,
		PasswordHash: string(hash),
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
	// Failure-only counter: CheckFailures atomically compares AND reserves
	// a slot for this attempt (so a concurrent burst can't all pass the
	// check before any failure is recorded); IncrFailure turns the
	// reservation into a committed failure on a confirmed bad login;
	// ResetFailures clears everything on success. Successes therefore
	// still never count toward the lockout (the earlier sliding-window
	// form counted them, so `limit` legitimate logins locked a user out).
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
		challenge, err := s.tokens.IssueTOTPChallengeToken(user.ID, user.SessionEpoch)
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
	// Resolve by the current hash OR the immediately-previous one. Rotation
	// rewrites token_hash in place, so a SUPERSEDED token used to match no row
	// and exit here as a plain "not found" — which is exactly what the real
	// theft case looks like: the attacker refreshes first, and the victim's
	// client presents the retired token minutes later. That path returned a
	// bare 401 and left the attacker's chain untouched, so the compromise was
	// both permanent and invisible. Matching the previous hash is what makes
	// the theft branch below reachable outside a sub-millisecond race.
	found, err := s.db.GetSessionByAnyTokenHash(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("refresh: session not found or expired")
	}
	if !found.IsCurrent {
		// A retired token was presented. The legitimate client always holds
		// the newest one, so this is reuse: burn the family exactly as the
		// CAS-miss branch below does.
		s.logger.WarnContext(ctx, "refresh token reuse detected (superseded hash presented); invalidating session family",
			"user_id", found.UserID, "session_id", found.ID)
		if derr := s.db.DeleteSessionsForUser(ctx, found.UserID); derr != nil {
			s.logger.ErrorContext(ctx, "refresh reuse: failed to delete session family; thief may retain refresh access",
				"user_id", found.UserID, "err", derr)
		}
		if berr := s.db.BumpSessionEpoch(ctx, found.UserID); berr != nil {
			s.logger.ErrorContext(ctx, "refresh reuse: failed to bump session epoch; outstanding access tokens not invalidated",
				"user_id", found.UserID, "err", berr)
		}
		s.revokeSegmentTokens(ctx, found.UserID)
		return nil, fmt.Errorf("refresh: token already used; session invalidated")
	}
	session := found
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
		// These two steps ARE the theft response: DeleteSessionsForUser kills
		// every refresh session, BumpSessionEpoch invalidates already-issued
		// access tokens. If either fails the family is NOT fully revoked and a
		// thief may retain access — that's a security incident, not a swallow-
		// and-move-on, so log each at ERROR so monitoring can page ops. The
		// reused token is rejected regardless via the error return below.
		if derr := s.db.DeleteSessionsForUser(ctx, session.UserID); derr != nil {
			s.logger.ErrorContext(ctx, "refresh reuse: failed to delete session family; thief may retain refresh access",
				"user_id", session.UserID, "err", derr)
		}
		if berr := s.db.BumpSessionEpoch(ctx, session.UserID); berr != nil {
			s.logger.ErrorContext(ctx, "refresh reuse: failed to bump session epoch; outstanding access tokens not invalidated",
				"user_id", session.UserID, "err", berr)
		}
		s.revokeSegmentTokens(ctx, session.UserID)
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
	// HLS segment tokens are deliberately NOT revoked here. They are indexed
	// per user only — nothing ties a segment token to the refresh session
	// being logged out — so the only revoke available (RevokeAllForUser)
	// would cut playback mid-stream on every OTHER device of this user, and
	// unlike access tokens those players have no refresh path to recover.
	// Segment tokens are revoked on the paths that mean "every device":
	// refresh-token theft (Refresh above), password reset / admin password
	// change / demote / delete (UserHandler, PasswordResetHandler).
	return nil
}

// revokeSegmentTokens is the HLS leg of a session-family burn. Best-effort:
// the epoch bump already cut API access; a failure here only leaves segment
// tokens to their idle TTL, so it is logged at ERROR for ops, not returned.
func (s *authService) revokeSegmentTokens(ctx context.Context, userID uuid.UUID) {
	if s.segTokens == nil {
		return
	}
	if err := s.segTokens.RevokeAllForUser(ctx, userID); err != nil {
		s.logger.ErrorContext(ctx, "revoke segment tokens after session family burn",
			"user_id", userID, "err", err)
	}
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
	// Spend the step even here: the enrolment code is otherwise still live
	// for ~90 s and could be replayed as the first login second factor.
	if !s.validateTOTPOnce(ctx, userID, code, secret) {
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
	// Same per-user cap as the login verify step: a stolen session must not
	// be able to grind the code space here, where only the general
	// per-session API limit applied before. Locked out → the same
	// ErrBadTOTPCode the verify path returns.
	if !s.reserveStepUp(ctx, stepUpFailureKey(userID), userID) {
		return v1.ErrBadTOTPCode
	}
	ok, err := s.validateSecondFactor(ctx, user, code)
	if err != nil {
		return err
	}
	if !ok {
		s.recordStepUpFailure(ctx, stepUpFailureKey(userID))
		return v1.ErrBadTOTPCode
	}
	s.resetStepUpFailures(ctx, stepUpFailureKey(userID))
	if err := s.db.DisableUserTOTP(ctx, userID); err != nil {
		return fmt.Errorf("disable totp: %w", err)
	}
	if err := s.db.DeleteTOTPRecoveryCodes(ctx, userID); err != nil {
		return fmt.Errorf("disable totp: clear codes: %w", err)
	}
	return nil
}

// VerifyTOTPLogin validates the login challenge token + second factor and
// issues the real token pair. A challenge is single-use and dies with the
// user's session epoch — see the checks below.
func (s *authService) VerifyTOTPLogin(ctx context.Context, challengeToken, code string) (*v1.TokenPair, error) {
	claims, err := s.tokens.ValidateAccessToken(challengeToken)
	// No jti = minted before challenges became single-use; refuse rather
	// than let it bypass the burn below (it ages out in TOTPChallengeTTL).
	if err != nil || claims == nil || claims.Purpose != "totp_challenge" || claims.TokenID == "" {
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
	// Session epoch moved since the password step (password reset, admin
	// force-logout, demote, refresh-theft burn) → the challenge is revoked
	// along with every other credential. Without this, whoever got past the
	// password step (a real-time phishing relay, say) could still finish the
	// login for the challenge's full TTL after the victim reset the password.
	if claims.SessionEpoch != user.SessionEpoch {
		return nil, v1.ErrInvalidTOTPChallenge
	}

	// Per-user second-factor brute-force throttle. Keyed by user ID (not
	// username, not IP) so neither a distributed IP pool nor re-minting fresh
	// challenge tokens lets an attacker exceed the cap on TOTP/recovery-code
	// guesses for a single account. Shared with DisableTOTP / VerifyReauth.
	if !s.reserveStepUp(ctx, loginTOTPFailureKey(claims.UserID), claims.UserID) {
		return nil, v1.ErrBadTOTPCode
	}

	ok, err := s.validateSecondFactor(ctx, user, code)
	if err != nil {
		return nil, err
	}
	if !ok {
		s.recordStepUpFailure(ctx, loginTOTPFailureKey(claims.UserID))
		return nil, v1.ErrBadTOTPCode
	}
	s.resetStepUpFailures(ctx, loginTOTPFailureKey(claims.UserID))
	// Burn the challenge only now: a wrong code must leave it usable (the
	// per-user failure cap above bounds guessing), but a redeemed one must
	// never mint a second session, even with a fresh code. Atomic, so two
	// concurrent verifies of one challenge can't both get here and pass.
	if !s.redeemTOTPChallenge(ctx, claims.TokenID, claims.ExpiresAt) {
		return nil, v1.ErrInvalidTOTPChallenge
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

// VerifyReauth re-checks a user's password (and second factor, if 2FA is
// enabled) for step-up auth before revealing sensitive data such as the
// worker connection credentials. Returns nil only on a full match.
func (s *authService) VerifyReauth(ctx context.Context, userID uuid.UUID, password, totpCode string) error {
	user, err := s.db.GetUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("reauth: get user: %w", err)
	}
	if user.PasswordHash == nil {
		// Federated (OIDC/SAML/LDAP) accounts have no local password to
		// re-verify against; their IdP owns the credential. Return the sentinel
		// so callers can distinguish "no local step-up is possible" from "wrong
		// password".
		return v1.ErrReauthNoLocalPassword
	}
	// Per-user failure cap, shared with the TOTP verify/disable paths. Step-up
	// guards the SECRET_KEY / DATABASE_URL reveal and backup restore, so a
	// stolen admin session must not get to grind the password (or code) here
	// under nothing but the general per-session API limit. Wrong password and
	// wrong code both count.
	if !s.reserveStepUp(ctx, stepUpFailureKey(userID), userID) {
		return errStepUpLocked
	}
	if err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(password)); err != nil {
		s.recordStepUpFailure(ctx, stepUpFailureKey(userID))
		return fmt.Errorf("invalid password")
	}
	if user.TotpEnabled {
		ok, err := s.validateSecondFactor(ctx, user, totpCode)
		if err != nil {
			return err
		}
		if !ok {
			s.recordStepUpFailure(ctx, stepUpFailureKey(userID))
			return fmt.Errorf("invalid 2FA code")
		}
	}
	s.resetStepUpFailures(ctx, stepUpFailureKey(userID))
	return nil
}

// validateSecondFactor accepts EITHER a live, not-yet-spent TOTP code OR an
// unused recovery code (consumed on success). A valid TOTP code never
// consumes a recovery code; a TOTP step and a recovery code are each
// single-use and burned atomically.
func (s *authService) validateSecondFactor(ctx context.Context, user gen.User, code string) (bool, error) {
	if user.TotpSecret != nil && *user.TotpSecret != "" {
		secret, err := s.enc.Decrypt(*user.TotpSecret)
		if err != nil {
			return false, fmt.Errorf("validate 2fa: decrypt secret: %w", err)
		}
		if step, live := auth.MatchTOTPStep(code, secret, s.clock()); live {
			// A live code decides the outcome by itself: spent before (a
			// replay) is a failure, not a cue to try it as a recovery code.
			return s.spendTOTPStep(ctx, user.ID, step), nil
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
