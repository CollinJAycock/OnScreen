// Package auth handles Paseto v4 local token issuance and session management
// (ADR-003, ADR-013).
package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"aidanwoods.dev/go-paseto"
	"github.com/google/uuid"
)

const (
	AccessTokenTTL  = time.Hour           // Paseto access token TTL (ADR-013)
	RefreshTokenTTL = 30 * 24 * time.Hour // Refresh token TTL
	// StreamTokenTTL covers per-file media-stream tokens minted at
	// item-detail fetch and embedded in the file's stream URL. Native
	// players (ExoPlayer, AVPlay) use their own HTTP stack and can't
	// invoke the Bearer-refresh path, so a 1 h access token expires
	// mid-playback and surfaces as ERROR_CODE_IO_BAD_HTTP_STATUS on
	// the next range request. 24 h is long enough that a single
	// movie / TV-binge session never trips on it; the token still
	// dies on session_epoch bumps (admin demote, force-logout) so a
	// stale credential stays revocable in seconds.
	StreamTokenTTL = 24 * time.Hour
	// AssetTokenTTL covers the user-scoped asset token minted at login /
	// refresh and embedded in `?token=` on the read-only asset routes —
	// artwork, trickplay sprites, external subtitles, and the SSE
	// notification stream — for cross-origin clients (Tauri, the TV
	// fleet) where <img>/CSS-background/EventSource can't carry an
	// Authorization header and cookies don't survive cross-origin.
	// Same 24 h / epoch-revocable rationale as the stream token: long
	// enough that a TV session idling on a poster wall doesn't 401, but
	// killed instantly on logout/demote. Unlike the stream token it
	// carries no file binding (artwork/SSE aren't file-scoped); unlike
	// the access token it has purpose=asset so it is rejected on the
	// Bearer/cookie path and can't be replayed as a general-API
	// credential if a poster URL leaks via logs / Referer / history.
	AssetTokenTTL = 24 * time.Hour
	// TOTPChallengeTTL bounds the window between a correct password and
	// the second factor. The token is minted when a TOTP-enabled user
	// passes the password check; the client must post a valid 6-digit
	// code (or a recovery code) against it before it expires. 5 minutes
	// is long enough to fish a phone out of a pocket, short enough that
	// a leaked challenge token (which on its own grants nothing without
	// the code) ages out fast.
	TOTPChallengeTTL = 5 * time.Minute
)

// Claims are the standard fields embedded in every Paseto access token.
//
// SessionEpoch is bumped server-side on admin demotion, user delete, or
// any "force logout" path. The auth middleware compares the token's
// epoch against the DB row's epoch on every authenticated request and
// rejects mismatches — this is what makes PASETO (a stateless token)
// revocable in seconds rather than waiting out its 1h TTL.
type Claims struct {
	UserID           uuid.UUID `json:"user_id"`
	Username         string    `json:"username"`
	IsAdmin          bool      `json:"is_admin"`
	MaxContentRating string    `json:"max_content_rating,omitempty"` // parental filter ceiling; empty = unrestricted
	SessionEpoch     int64     `json:"session_epoch"`
	IssuedAt         time.Time `json:"iat"`
	ExpiresAt        time.Time `json:"exp"`
	// Purpose narrows what a token is allowed to do. Empty for the
	// classic 1 h access token (Bearer + asset query both work).
	// "stream" for the 24 h streaming token: rejected on Bearer
	// extraction, accepted only by RequiredAllowQueryToken on asset
	// routes — so a leaked stream URL can't be repurposed as a
	// general-API credential.
	Purpose string `json:"purpose,omitempty"`
	// FileID binds a stream-purpose token to a single file UUID.
	// The /media/stream/{id} handler matches the URL param against
	// this claim. Stops a leaked URL from being repurposed across
	// files the user has access to. Zero / nil for non-stream
	// tokens.
	FileID *uuid.UUID `json:"file_id,omitempty"`
	// Switched marks a token minted via PIN-switch (one household
	// profile assuming another's identity). The PIN-switch handler
	// refuses to act on a token that already carries this flag, so a
	// switched-into profile can't itself initiate further switches —
	// no profile-hopping chains. Absent / false on a normal credential
	// login.
	Switched bool `json:"switched,omitempty"`
}

// TokenMaker issues and validates Paseto v4 local tokens.
type TokenMaker struct {
	key paseto.V4SymmetricKey
}

// NewTokenMaker creates a TokenMaker from the 32-byte SECRET_KEY.
// secretKey must be exactly 32 bytes.
func NewTokenMaker(secretKey []byte) (*TokenMaker, error) {
	k, err := paseto.V4SymmetricKeyFromBytes(secretKey)
	if err != nil {
		return nil, fmt.Errorf("auth: invalid secret key: %w", err)
	}
	return &TokenMaker{key: k}, nil
}

// IssueAccessToken creates a Paseto v4 local token for the given user.
func (m *TokenMaker) IssueAccessToken(claims Claims) (string, error) {
	token := paseto.NewToken()
	token.SetIssuedAt(time.Now())
	token.SetNotBefore(time.Now())
	token.SetExpiration(time.Now().Add(AccessTokenTTL))
	token.SetString("user_id", claims.UserID.String())
	token.SetString("username", claims.Username)
	isAdminStr := "false"
	if claims.IsAdmin {
		isAdminStr = "true"
	}
	token.SetString("is_admin", isAdminStr)
	if claims.MaxContentRating != "" {
		token.SetString("max_content_rating", claims.MaxContentRating)
	}
	token.SetString("session_epoch", fmt.Sprintf("%d", claims.SessionEpoch))
	if claims.Switched {
		token.SetString("switched", "true")
	}

	return token.V4Encrypt(m.key, nil), nil
}

// IssueStreamToken creates a Paseto v4 local token with the longer
// StreamTokenTTL, scoped to a single file via the FileID claim and
// flagged with purpose=stream so the Bearer-extraction path rejects
// it. Bearer-only routes can't be reached with a stream token; the
// asset middleware (RequiredAllowQueryToken) accepts it on
// /media/stream + /media/subtitles + /artwork + /trickplay.
func (m *TokenMaker) IssueStreamToken(claims Claims, fileID uuid.UUID) (string, error) {
	token := paseto.NewToken()
	token.SetIssuedAt(time.Now())
	token.SetNotBefore(time.Now())
	token.SetExpiration(time.Now().Add(StreamTokenTTL))
	token.SetString("user_id", claims.UserID.String())
	token.SetString("username", claims.Username)
	isAdminStr := "false"
	if claims.IsAdmin {
		isAdminStr = "true"
	}
	token.SetString("is_admin", isAdminStr)
	if claims.MaxContentRating != "" {
		token.SetString("max_content_rating", claims.MaxContentRating)
	}
	token.SetString("session_epoch", fmt.Sprintf("%d", claims.SessionEpoch))
	token.SetString("purpose", "stream")
	token.SetString("file_id", fileID.String())
	return token.V4Encrypt(m.key, nil), nil
}

// IssueAssetToken creates a Paseto v4 local token with AssetTokenTTL,
// flagged purpose=asset. It carries the same user identity / content-
// rating / epoch as the access token (the asset handlers still enforce
// library ACLs and the rating ceiling), but no file binding: it
// authenticates the read-only asset routes (artwork, trickplay,
// external subtitles, SSE) for cross-origin clients via `?token=`.
// Because purpose != "", validateGeneralPurposeToken rejects it on the
// Bearer/cookie path, so a leaked asset URL can't grant general API
// access.
func (m *TokenMaker) IssueAssetToken(claims Claims) (string, error) {
	token := paseto.NewToken()
	token.SetIssuedAt(time.Now())
	token.SetNotBefore(time.Now())
	token.SetExpiration(time.Now().Add(AssetTokenTTL))
	token.SetString("user_id", claims.UserID.String())
	token.SetString("username", claims.Username)
	isAdminStr := "false"
	if claims.IsAdmin {
		isAdminStr = "true"
	}
	token.SetString("is_admin", isAdminStr)
	if claims.MaxContentRating != "" {
		token.SetString("max_content_rating", claims.MaxContentRating)
	}
	token.SetString("session_epoch", fmt.Sprintf("%d", claims.SessionEpoch))
	token.SetString("purpose", "asset")
	if claims.Switched {
		token.SetString("switched", "true")
	}
	return token.V4Encrypt(m.key, nil), nil
}

// IssueTOTPChallengeToken mints the short-lived token that bridges the
// password step and the TOTP step of a two-factor login. purpose=
// totp_challenge means validateGeneralPurposeToken rejects it on the
// Bearer/cookie path and the asset middleware rejects it too — it is
// only ever accepted by the /auth/totp/verify handler, which checks the
// purpose explicitly and reads UserID to issue the real token pair.
func (m *TokenMaker) IssueTOTPChallengeToken(userID uuid.UUID) (string, error) {
	token := paseto.NewToken()
	token.SetIssuedAt(time.Now())
	token.SetNotBefore(time.Now())
	token.SetExpiration(time.Now().Add(TOTPChallengeTTL))
	token.SetString("user_id", userID.String())
	token.SetString("purpose", "totp_challenge")
	return token.V4Encrypt(m.key, nil), nil
}

// ValidateAccessToken decrypts and validates a Paseto v4 local token.
// Returns the claims on success, or an error if the token is invalid or expired.
func (m *TokenMaker) ValidateAccessToken(tokenStr string) (*Claims, error) {
	parser := paseto.NewParserWithoutExpiryCheck()
	token, err := parser.ParseV4Local(m.key, tokenStr, nil)
	if err != nil {
		return nil, fmt.Errorf("validate token: %w", err)
	}

	exp, err := token.GetExpiration()
	if err != nil || time.Now().After(exp) {
		return nil, fmt.Errorf("validate token: expired")
	}
	// The parser skips the built-in time checks, so re-enforce not-before too
	// (every token we mint sets nbf). Prevents a future-dated token from being
	// honored early should any issuance path ever set a forward nbf.
	if nbf, nerr := token.GetNotBefore(); nerr == nil && time.Now().Before(nbf) {
		return nil, fmt.Errorf("validate token: not yet valid")
	}

	userIDStr, err := token.GetString("user_id")
	if err != nil {
		return nil, fmt.Errorf("validate token: missing user_id claim")
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, fmt.Errorf("validate token: invalid user_id: %w", err)
	}

	username, _ := token.GetString("username")
	isAdminStr, _ := token.GetString("is_admin")
	isAdmin := isAdminStr == "true"
	maxContentRating, _ := token.GetString("max_content_rating")
	// session_epoch is zero for tokens minted before the field was
	// added — that's OK, validateSessionEpoch treats missing/zero as
	// matching any current epoch on upgrade. After a single demote/
	// delete the epoch diverges and old tokens get rejected.
	epochStr, _ := token.GetString("session_epoch")
	var sessionEpoch int64
	if epochStr != "" {
		// strconv.ParseInt tolerates leading/trailing whitespace poorly;
		// Sprintf wrote a clean decimal so we're safe.
		fmt.Sscanf(epochStr, "%d", &sessionEpoch)
	}
	issuedAt, _ := token.GetIssuedAt()
	purpose, _ := token.GetString("purpose")
	var fileID *uuid.UUID
	if s, _ := token.GetString("file_id"); s != "" {
		if id, err := uuid.Parse(s); err == nil {
			fileID = &id
		}
	}
	switchedStr, _ := token.GetString("switched")

	return &Claims{
		UserID:           userID,
		Username:         username,
		IsAdmin:          isAdmin,
		MaxContentRating: maxContentRating,
		SessionEpoch:     sessionEpoch,
		IssuedAt:         issuedAt,
		ExpiresAt:        exp,
		Purpose:          purpose,
		FileID:           fileID,
		Switched:         switchedStr == "true",
	}, nil
}

// IssueRefreshToken generates a cryptographically random refresh token string
// and returns both the raw token (sent to client) and its SHA-256 hash
// (stored in the sessions table).
func IssueRefreshToken() (raw string, hash string, err error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	raw = id.String()
	hash = HashToken(raw)
	return raw, hash, nil
}

// HashToken returns the SHA-256 hex digest of a token string.
// Used to hash refresh tokens before DB storage.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
