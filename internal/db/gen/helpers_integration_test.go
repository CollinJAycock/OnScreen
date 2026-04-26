//go:build integration

// Shared test helpers for the gen integration test suite. Each helper
// is intentionally narrow — it inserts the minimal valid row so a test
// can exercise the query under test without hand-writing INSERTs.
//
// seedUser and seedLibrary are defined in users_integration_test.go and
// library_access_integration_test.go respectively; this file adds the
// helpers shared by the newer integration tests (watch_events, dvr,
// notifications, etc.).
package gen_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// seedMediaItem inserts a minimal movie row in the given library and
// returns its ID. Callers that need richer metadata (year, summary, etc.)
// should use q.CreateMediaItem directly.
func seedMediaItem(ctx context.Context, t *testing.T, q *gen.Queries, libraryID uuid.UUID, title string) uuid.UUID {
	t.Helper()
	item, err := q.CreateMediaItem(ctx, gen.CreateMediaItemParams{
		LibraryID: libraryID,
		Type:      "movie",
		Title:     title,
		SortTitle: title,
	})
	if err != nil {
		t.Fatalf("CreateMediaItem %q: %v", title, err)
	}
	return item.ID
}
