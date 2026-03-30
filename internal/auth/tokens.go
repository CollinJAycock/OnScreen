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
	AccessTokenTTL  = time.Hour             // Paseto access token TTL (ADR-013)
	RefreshTokenTTL = 30 * 24 * time.Hour   // Refresh token TTL
)

// Claims are the standard fields embedded in every Paseto access token.
type Claims struct {
	UserID    uuid.UUID `json:"user_id"`
	Username  string    `json:"username"`
	IsAdmin   bool      `json:"is_admin"`
	IssuedAt  time.Time `json:"iat"`
	ExpiresAt time.Time `json:"exp"`
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
	issuedAt, _ := token.GetIssuedAt()

	return &Claims{
		UserID:    userID,
		Username:  username,
		IsAdmin:   isAdmin,
		IssuedAt:  issuedAt,
		ExpiresAt: exp,
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
