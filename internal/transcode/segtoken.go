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
// The token is stored in Valkey with a 4-hour TTL.
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
func (m *SegmentTokenManager) Revoke(ctx context.Context, token string) error {
	return m.v.Del(ctx, segTokenKey(token))
}

func segTokenKey(token string) string { return "segment_token:" + token }
