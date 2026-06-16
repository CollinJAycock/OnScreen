// Package watchevent contains business logic for recording and querying playback events.
package watchevent

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/observability"
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
	Decision   *string // "directPlay"|"directStream"|"remux"|"transcode" — nil for clients that predate the field
	OccurredAt time.Time
}

// Querier defines the DB operations the service needs.
type Querier interface {
	InsertWatchEvent(ctx context.Context, p InsertWatchEventParams) (InsertWatchEventRow, error)
	RefreshWatchState(ctx context.Context) error
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
type Service struct {
	rw       Querier
	ro       Querier
	logger   *slog.Logger
	metrics  *observability.Metrics
	scrobble ScrobbleHook

	// refreshMu/refreshScheduled coalesce watch_state matview refreshes. A
	// REFRESH ... CONCURRENTLY scans the whole watch_events history, so firing
	// it per stop/scrobble made the cost of one playback completion O(total
	// history) and let concurrent stops queue behind the exclusive refresh lock.
	// See scheduleWatchStateRefresh.
	refreshMu        sync.Mutex
	refreshScheduled bool
	refreshDebounce  time.Duration // defaults to watchStateRefreshDebounce; overridable in tests
}

// watchStateRefreshDebounce bounds how often the watch_state matview is rebuilt:
// at most one refresh per window, fired within this delay of the first stop in a
// burst. Short enough that continue-watching / recommendation rows (which read
// the matview) stay fresh; long enough that a flurry of concurrent stops
// collapses to a single full-history refresh instead of one each.
const watchStateRefreshDebounce = 10 * time.Second

// NewService constructs a watch event Service.
func NewService(rw, ro Querier, logger *slog.Logger) *Service {
	return &Service{rw: rw, ro: ro, logger: logger, refreshDebounce: watchStateRefreshDebounce}
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

	// Refresh watch_state after terminal events so continue-watching /
	// recommendation rows reflect the updated status. Coalesced (not per-event):
	// the refresh scans the whole history, so firing it for every stop scaled
	// with total watch history and serialized concurrent stops behind the
	// refresh lock.
	if p.EventType == "stop" || p.EventType == "scrobble" {
		s.scheduleWatchStateRefresh()
	}
	return nil
}

// scheduleWatchStateRefresh ensures a single watch_state refresh runs within
// watchStateRefreshDebounce. If one is already pending, this is a no-op — the
// pending refresh will pick up this event too (the matview reads live data when
// it fires). This caps refreshes at ≤1 per window regardless of stop volume,
// turning a per-completion O(history) cost into a per-window one.
func (s *Service) scheduleWatchStateRefresh() {
	s.refreshMu.Lock()
	if s.refreshScheduled {
		s.refreshMu.Unlock()
		return
	}
	s.refreshScheduled = true
	s.refreshMu.Unlock()

	observability.SafeGo(s.logger, "watchevent.refresh-state", func() {
		time.Sleep(s.refreshDebounce)
		// Clear the flag before the refresh so a stop arriving during the
		// (potentially slow) refresh re-arms and is captured by the next one.
		s.refreshMu.Lock()
		s.refreshScheduled = false
		s.refreshMu.Unlock()
		if err := s.rw.RefreshWatchState(context.Background()); err != nil {
			s.logger.Warn("watch_state refresh failed", "err", err)
		}
	})
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
