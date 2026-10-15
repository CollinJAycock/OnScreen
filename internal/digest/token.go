// Package digest sends the opt-in digest email: what was added to the
// libraries a user can see over the last week or month, optionally with their
// Continue Watching titles and their own numbers for the period.
//
// Visibility: every title in a digest passes the recipient's library access,
// content-rating ceiling and label restriction, the same three gates their
// home page applies. Posters travel as inline (CID) attachments resized by the
// artwork manager, so the HTML never points at the server and carries no
// asset token a forwarded email could reuse.
package digest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"

	"github.com/google/uuid"
)

// Signer makes and checks the unsubscribe token in every digest's links and
// List-Unsubscribe header. The token is the user id plus an HMAC-SHA256 of it
// under a key derived from the server's SECRET_KEY, so it can't be guessed or
// forged for another user, and unsubscribing needs no sign-in. It only ever
// grants "turn this user's digest off".
type Signer struct {
	key []byte
}

// NewSigner derives the unsubscribe key from secretKey (domain-separated, so
// the key is useless for anything else the secret signs).
func NewSigner(secretKey string) *Signer {
	m := hmac.New(sha256.New, []byte(secretKey))
	_, _ = m.Write([]byte("onscreen-digest-unsubscribe-v1"))
	return &Signer{key: m.Sum(nil)}
}

func (s *Signer) mac(id uuid.UUID) []byte {
	m := hmac.New(sha256.New, s.key)
	_, _ = m.Write(id[:])
	return m.Sum(nil)
}

// Token returns the unsubscribe token for userID (URL-safe base64).
func (s *Signer) Token(userID uuid.UUID) string {
	buf := make([]byte, 0, 16+sha256.Size)
	buf = append(buf, userID[:]...)
	buf = append(buf, s.mac(userID)...)
	return base64.RawURLEncoding.EncodeToString(buf)
}

// Verify returns the user a token was made for, or false when it is
// malformed or its signature doesn't match.
func (s *Signer) Verify(token string) (uuid.UUID, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 16+sha256.Size {
		return uuid.Nil, false
	}
	id, err := uuid.FromBytes(raw[:16])
	if err != nil {
		return uuid.Nil, false
	}
	if !hmac.Equal(raw[16:], s.mac(id)) {
		return uuid.Nil, false
	}
	return id, true
}
