// Package watchstatus implements the per-(user, item) watching-status
// mirror — Plan to Watch / Watching / Completed / On Hold / Dropped.
// Anime-tracker convention (MyAnimeList / AniList shape) shipped as
// a generic feature so every type benefits.
//
// Distinct from watchevent: watchevent records playback positions
// the player generates automatically; watchstatus records the user's
// explicit classification ("I want to watch this later", "I gave
// up"). The two complement each other — a "Watching" status with
// position 73% gives both signals to the UI.
package watchstatus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Valid status values. Mirrors the CHECK constraint in migration 76.
const (
	StatusPlanToWatch = "plan_to_watch"
	StatusWatching    = "watching"
	StatusCompleted   = "completed"
	StatusOnHold      = "on_hold"
	StatusDropped     = "dropped"
)

// AllStatuses returns the canonical ordered list. Order matches the
// mental "tracker progression" — discovery → engagement → endpoint
// states — so a UI dropdown reads naturally.
func AllStatuses() []string {
	return []string{
		StatusPlanToWatch,
		StatusWatching,
		StatusOnHold,
		StatusCompleted,
		StatusDropped,
	}
}

// IsValidStatus reports whether s is a recognised status string.
// Used by the API handler to reject stray values before the DB
// CHECK constraint would (clearer error message at the boundary).
func IsValidStatus(s string) bool {
	for _, v := range AllStatuses() {
		if s == v {
			return true
		}
	}
	return false
}

// Status holds one (user, item) classification record.
type Status struct {
	UserID      uuid.UUID
	MediaItemID uuid.UUID
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ErrNotFound is returned by Get when no row exists for the
// (user, item) pair. Callers map this to a 404 (or, for the detail
// page's "current status?" load, treat it as "no status set" and
// render the dropdown blank).
var ErrNotFound = errors.New("watch status not found")

// Querier is the narrow set of DB operations the service needs.
// Wired by the cmd/server adapter using sqlc-generated queries.
type Querier interface {
	GetUserWatchStatus(ctx context.Context, userID, mediaItemID uuid.UUID) (Status, error)
	UpsertUserWatchStatus(ctx context.Context, userID, mediaItemID uuid.UUID, status string) (Status, error)
	DeleteUserWatchStatus(ctx context.Context, userID, mediaItemID uuid.UUID) error
}

// Service is the public domain entry point.
type Service struct {
	q Querier
}

// New constructs the service.
func New(q Querier) *Service {
	return &Service{q: q}
}

// Get returns the current status for (user, item) or ErrNotFound
// when nothing is set.
func (s *Service) Get(ctx context.Context, userID, mediaItemID uuid.UUID) (Status, error) {
	st, err := s.q.GetUserWatchStatus(ctx, userID, mediaItemID)
	if err != nil {
		return Status{}, err
	}
	return st, nil
}

// Set creates or updates the status. The CHECK constraint guarantees
// `status` is one of the recognised values; the IsValidStatus
// pre-check at the API boundary surfaces a cleaner error than the
// pgx 23514 wrapper.
func (s *Service) Set(ctx context.Context, userID, mediaItemID uuid.UUID, status string) (Status, error) {
	if !IsValidStatus(status) {
		return Status{}, fmt.Errorf("invalid status %q", status)
	}
	return s.q.UpsertUserWatchStatus(ctx, userID, mediaItemID, status)
}

// Clear removes the (user, item) row. Distinct from setting status
// to a sentinel: "no row" means the user has neither queued nor
// classified the item, different from "Plan to Watch".
func (s *Service) Clear(ctx context.Context, userID, mediaItemID uuid.UUID) error {
	return s.q.DeleteUserWatchStatus(ctx, userID, mediaItemID)
}
