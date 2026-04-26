package transcode

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/valkey"
)

const segTokenTTL = 4 * time.Hour

// segTokenData is stored in Valkey under each segment token.
type segTokenData struct {
	SessionID string    `json:"session_id"`
	UserID    uuid.UUID `json:"user_id"`
}

// SegmentTokenManager issues and validates short-lived HLS segment auth tokens (ADR-019).
type SegmentTokenManager struct {
	v *valkey.Client
}

// NewSegmentTokenManager creates a SegmentTokenManager.
func NewSegmentTokenManager(v *valkey.Client) *SegmentTokenManager {
	return &SegmentTokenManager{v: v}
}

// Issue creates a new segment token for the given session and user.
// The token is stored in Valkey with a 4-hour TTL. The token is also
// added to a per-user index set so RevokeAllForUser can wipe every
// outstanding token when the user's session_epoch is bumped (password
// reset, admin demote). The index set is given the same TTL so a
// long-idle user doesn't leave an unbounded set behind in Valkey.
func (m *SegmentTokenManager) Issue(ctx context.Context, sessionID string, userID uuid.UUID) (string, error) {
	token := uuid.New().String()
	data := segTokenData{SessionID: sessionID, UserID: userID}
	b, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal seg token: %w", err)
	}
	if err := m.v.Set(ctx, segTokenKey(token), string(b), segTokenTTL); err != nil {
		return "", fmt.Errorf("store seg token: %w", err)
	}
	idx := userIndexKey(userID)
	if err := m.v.SAdd(ctx, idx, token); err != nil {
		// Best-effort: a missing index entry just means RevokeAllForUser
		// won't see this token. The token still expires on its own TTL.
		// Don't fail the Issue call — playback would 503.
		_ = err
	} else {
		_ = m.v.Expire(ctx, idx, segTokenTTL)
	}
	return token, nil
}

// Validate looks up a segment token and returns the associated session and user.
func (m *SegmentTokenManager) Validate(ctx context.Context, token string) (sessionID string, userID uuid.UUID, err error) {
	raw, err := m.v.Get(ctx, segTokenKey(token))
	if err == valkey.ErrNotFound {
		return "", uuid.UUID{}, fmt.Errorf("invalid or expired segment token")
	}
	if err != nil {
		return "", uuid.UUID{}, fmt.Errorf("validate seg token: %w", err)
	}
	var data segTokenData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return "", uuid.UUID{}, fmt.Errorf("decode seg token: %w", err)
	}
	return data.SessionID, data.UserID, nil
}

// Revoke deletes a segment token immediately (called on session stop).
// We don't have the userID here so the index entry is left as a dangling
// reference — RevokeAllForUser tolerates that by treating Del of a missing
// token-key as a no-op.
func (m *SegmentTokenManager) Revoke(ctx context.Context, token string) error {
	return m.v.Del(ctx, segTokenKey(token))
}

// RevokeAllForUser deletes every outstanding segment token for the given
// user. Called from credential-rotation paths (password reset, admin demote)
// so an active HLS playback can't outlive the access-token revocation.
//
// Returns nil even when the user has no live tokens — the post-condition
// "no live segment tokens for this user" is satisfied either way.
func (m *SegmentTokenManager) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	idx := userIndexKey(userID)
	tokens, err := m.v.SMembers(ctx, idx)
	if err != nil {
		return fmt.Errorf("list user seg tokens: %w", err)
	}
	if len(tokens) == 0 {
		return nil
	}
	keys := make([]string, 0, len(tokens)+1)
	for _, t := range tokens {
		keys = append(keys, segTokenKey(t))
	}
	keys = append(keys, idx)
	if err := m.v.Del(ctx, keys...); err != nil {
		return fmt.Errorf("delete user seg tokens: %w", err)
	}
	return nil
}

func segTokenKey(token string) string { return "segment_token:" + token }

func userIndexKey(userID uuid.UUID) string { return "user_segtokens:" + userID.String() }
