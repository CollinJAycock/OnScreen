// Package watchevent contains business logic for recording and querying playback events.
package watchevent

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// WatchState represents the derived playback state for a user+media pair.
// LastClient fields carry the most-recent device's attribution so resume
// UX can say "pick up where you left off on Living Room TV" rather than
// just showing a bare position.
type WatchState struct {
	UserID         uuid.UUID
	MediaID        uuid.UUID
	PositionMS     int64
	DurationMS     *int64
	Status         string // "watched" | "in_progress" | "unwatched"
	LastWatchedAt  time.Time
	LastClientID   *string
	LastClientName *string
}

// RecordParams holds the input for inserting a watch event.
type RecordParams struct {
	UserID     uuid.UUID
	MediaID    uuid.UUID
	FileID     *uuid.UUID
	SessionID  *uuid.UUID
	EventType  string // "play"|"pause"|"resume"|"stop"|"seek"|"scrobble"
	PositionMS int64
	DurationMS *int64
	ClientID   *string
	ClientName *string
	ClientIP   *netip.Addr
	OccurredAt time.Time
}

// Querier defines the DB operations the service needs.
type Querier interface {
	InsertWatchEvent(ctx context.Context, p InsertWatchEventParams) (InsertWatchEventRow, error)
	RefreshWatchState(ctx context.Context) error
	GetWatchState(ctx context.Context, userID, mediaID uuid.UUID) (WatchState, error)
	ListWatchStateForUser(ctx context.Context, userID uuid.UUID) ([]WatchState, error)
}

// InsertWatchEventParams mirrors the generated sqlc params but uses domain types.
type InsertWatchEventParams struct {
	UserID     uuid.UUID
	MediaID    uuid.UUID
	FileID     *uuid.UUID
	SessionID  *uuid.UUID
	EventType  string
	PositionMS int64
	DurationMS *int64
	ClientID   *string
	ClientName *string
	ClientIP   *netip.Addr
	OccurredAt time.Time
}

// InsertWatchEventRow is what comes back from the INSERT RETURNING.
type InsertWatchEventRow struct {
	ID         uuid.UUID
	OccurredAt time.Time
}

// Service implements watch event business logic.
type Service struct {
	rw     Querier
	ro     Querier
	logger *slog.Logger
}

// NewService constructs a watch event Service.
func NewService(rw, ro Querier, logger *slog.Logger) *Service {
	return &Service{rw: rw, ro: ro, logger: logger}
}

// Record inserts a watch event. For stop and scrobble events it also
// triggers an async materialized view refresh.
func (s *Service) Record(ctx context.Context, p RecordParams) error {
	_, err := s.rw.InsertWatchEvent(ctx, InsertWatchEventParams{
		UserID:     p.UserID,
		MediaID:    p.MediaID,
		FileID:     p.FileID,
		SessionID:  p.SessionID,
		EventType:  p.EventType,
		PositionMS: p.PositionMS,
		DurationMS: p.DurationMS,
		ClientID:   p.ClientID,
		ClientName: p.ClientName,
		ClientIP:   p.ClientIP,
		OccurredAt: p.OccurredAt,
	})
	if err != nil {
		return fmt.Errorf("insert watch event: %w", err)
	}

	// Refresh watch_state after terminal events so subsequent metadata
	// responses reflect the updated status immediately.
	if p.EventType == "stop" || p.EventType == "scrobble" {
		go func() {
			if err := s.rw.RefreshWatchState(context.Background()); err != nil {
				s.logger.Warn("watch_state refresh failed", "err", err)
			}
		}()
	}
	return nil
}

// GetState returns the current watch state for a user+media pair.
// Returns a zero-value WatchState with Status="unwatched" if not found.
func (s *Service) GetState(ctx context.Context, userID, mediaID uuid.UUID) (WatchState, error) {
	state, err := s.ro.GetWatchState(ctx, userID, mediaID)
	if err != nil {
		// No row means unwatched — not an error for callers.
		return WatchState{
			UserID:  userID,
			MediaID: mediaID,
			Status:  "unwatched",
		}, nil
	}
	return state, nil
}

// ListStates returns all watch states for a user.
func (s *Service) ListStates(ctx context.Context, userID uuid.UUID) ([]WatchState, error) {
	states, err := s.ro.ListWatchStateForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list watch states: %w", err)
	}
	return states, nil
}
