// Package watchevent contains business logic for recording and querying playback events.
package watchevent

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/observability"
)

// WatchState represents the derived playback state for a user+media pair,
// read from the user_watch_state view (migration 00023) — the one derivation
// every surface shares, manual played/unplayed marks included.
// LastClient fields carry the most-recent device's attribution so resume
// UX can say "pick up where you left off on Living Room TV" rather than
// just showing a bare position.
type WatchState struct {
	UserID     uuid.UUID
	MediaID    uuid.UUID
	PositionMS int64
	DurationMS *int64
	Status     string // "watched" | "in_progress" | "unwatched"
	// Resumable reports that PositionMS is a mid-way resume point (0 < pos
	// <= 90%) from activity since the latest manual mark. Independent of the
	// sticky Status: rewatching a watched title is watched AND resumable.
	Resumable      bool
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
	Decision   *string // "directPlay"|"directStream"|"remux"|"transcode" — nil for clients that predate the field
	OccurredAt time.Time
}

// Querier defines the DB operations the service needs.
type Querier interface {
	InsertWatchEvent(ctx context.Context, p InsertWatchEventParams) (InsertWatchEventRow, error)
	GetWatchState(ctx context.Context, userID, mediaID uuid.UUID) (WatchState, error)
	GetWatchStatesForItems(ctx context.Context, userID uuid.UUID, mediaIDs []uuid.UUID) ([]WatchState, error)
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
	Decision   *string
	OccurredAt time.Time
}

// InsertWatchEventRow is what comes back from the INSERT RETURNING.
type InsertWatchEventRow struct {
	ID         uuid.UUID
	OccurredAt time.Time
}

// ScrobbleHook is invoked asynchronously after a terminal 'stop' event, so an
// external scrobbler (ListenBrainz / Last.fm) can export the listen. It gets
// the final position + duration so the dispatcher can apply the "played
// enough" listen threshold; it must not block — Record fires it in its own
// goroutine and ignores the result. nil disables it.
type ScrobbleHook func(ctx context.Context, userID, mediaID uuid.UUID, positionMS int64, durationMS *int64, occurredAt time.Time)

// Service implements watch event business logic.
//
// Derived state needs no upkeep here: a trigger on watch_events folds every
// inserted event into the watch_progress rollup (migration 00023), so reads
// see a play tick as soon as it commits. (The watch_state materialized view
// and its debounced full-history refresh that this replaced are gone.)
type Service struct {
	rw       Querier
	ro       Querier
	logger   *slog.Logger
	metrics  *observability.Metrics
	scrobble ScrobbleHook
}

// NewService constructs a watch event Service.
func NewService(rw, ro Querier, logger *slog.Logger) *Service {
	return &Service{rw: rw, ro: ro, logger: logger}
}

// WithMetrics enables Prometheus instrumentation (watch events by type). nil is
// a no-op, so callers without a metrics registry are unaffected.
func (s *Service) WithMetrics(m *observability.Metrics) *Service {
	s.metrics = m
	return s
}

// WithScrobbleHook attaches the external-scrobble dispatcher, called async on
// completed-play ('scrobble') events. nil is a no-op.
func (s *Service) WithScrobbleHook(fn ScrobbleHook) *Service {
	s.scrobble = fn
	return s
}

// Record inserts a watch event. The watch_progress rollup (and so every
// derived watch state) is updated by the database in the same statement.
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
		Decision:   p.Decision,
		OccurredAt: p.OccurredAt,
	})
	if err != nil {
		return fmt.Errorf("insert watch event: %w", err)
	}
	if s.metrics != nil {
		s.metrics.WatchEventsTotal.WithLabelValues(p.EventType).Inc()
	}

	// Fire-and-forget external scrobble on the terminal 'stop' event — the
	// universal completion signal every first-party client emits (the web/
	// native players don't produce a distinct 'scrobble' event). The
	// dispatcher applies the listen threshold (played enough) and gates on a
	// linked account + music track, so handing it every 'stop' is fine.
	if p.EventType == "stop" && s.scrobble != nil {
		at := p.OccurredAt
		if at.IsZero() {
			at = time.Now().UTC()
		}
		observability.SafeGo(s.logger, "watchevent.scrobble", func() {
			s.scrobble(context.Background(), p.UserID, p.MediaID, p.PositionMS, p.DurationMS, at)
		})
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

// GetStates returns the watch state for each of mediaIDs in one query, keyed by
// media id. Media the user has never played are absent from the map — callers
// treat a missing entry as unwatched (mirrors GetState's zero-value default).
// Used to avoid an N+1 when rendering a list of children.
func (s *Service) GetStates(ctx context.Context, userID uuid.UUID, mediaIDs []uuid.UUID) (map[uuid.UUID]WatchState, error) {
	out := make(map[uuid.UUID]WatchState, len(mediaIDs))
	if len(mediaIDs) == 0 {
		return out, nil
	}
	states, err := s.ro.GetWatchStatesForItems(ctx, userID, mediaIDs)
	if err != nil {
		return nil, fmt.Errorf("get watch states for items: %w", err)
	}
	for _, st := range states {
		out[st.MediaID] = st
	}
	return out, nil
}

// ListStates returns all watch states for a user.
func (s *Service) ListStates(ctx context.Context, userID uuid.UUID) ([]WatchState, error) {
	states, err := s.ro.ListWatchStateForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list watch states: %w", err)
	}
	return states, nil
}
