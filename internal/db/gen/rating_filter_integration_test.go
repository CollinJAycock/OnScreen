//go:build integration

// Pins the v2.1 Track G item 4 content-rating gates on the queries
// that previously returned all items regardless of caller ceiling:
// ListCollectionItems (playlists), ListItemsByGenre + count (auto-
// genre rows), ListWatchHistory (history page).
//
// Run with: go test -tags=integration ./internal/db/gen/...
package gen_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// seedRated creates a media item with a specific content_rating set
// directly via SQL — CreateMediaItem doesn't expose the column and
// adding it just for tests would be churn.
func seedRated(ctx context.Context, t *testing.T, pool gen.DBTX, q *gen.Queries, lib uuid.UUID, title, rating string) uuid.UUID {
	t.Helper()
	id := seedMediaItem(ctx, t, q, lib, title)
	if _, err := pool.Exec(ctx, `UPDATE media_items SET content_rating = $1 WHERE id = $2`, rating, id); err != nil {
		t.Fatalf("set content_rating %q: %v", rating, err)
	}
	return id
}

// rankPtr converts a content_rating_rank() integer to the *int32
// shape sqlc generates for narg parameters.
func rankPtr(v int32) *int32 { return &v }

// TestListCollectionItems_Integration_RatingGate proves the
// max_rating_rank narg filters out items above the caller's ceiling
// while leaving lower-rated items in. Backs the "kid in a parent's
// playlist" use case.
func TestListCollectionItems_Integration_RatingGate(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "ci-rg-u-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "ci-rg-lib-"+uuid.New().String()[:8])
	gKid := seedRated(ctx, t, pool, q, lib, "Family Pic", "G")
	rKid := seedRated(ctx, t, pool, q, lib, "Action Pic", "R")

	// Create a playlist and add both items.
	col, err := q.CreateCollection(ctx, gen.CreateCollectionParams{
		UserID: pgtype.UUID{Bytes: user, Valid: true},
		Name:   "Mixed",
		Type:   "playlist",
	})
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	for _, mid := range []uuid.UUID{gKid, rKid} {
		if _, err := q.AddCollectionItem(ctx, gen.AddCollectionItemParams{
			CollectionID: col.ID, MediaItemID: mid,
		}); err != nil {
			t.Fatalf("AddCollectionItem: %v", err)
		}
	}

	// PG ceiling = rank 2; G=1, PG=2, PG-13=3, R=4. So a PG-rank
	// caller should see G but not R.
	rows, err := q.ListCollectionItems(ctx, gen.ListCollectionItemsParams{
		CollectionID:  col.ID,
		MaxRatingRank: rankPtr(2),
	})
	if err != nil {
		t.Fatalf("ListCollectionItems: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (only G should pass PG ceiling)", len(rows))
	}
	if rows[0].ID != gKid {
		t.Errorf("got id %s, want %s (G item)", rows[0].ID, gKid)
	}

	// Nil ceiling → see everything (admin / no parental control).
	rows, err = q.ListCollectionItems(ctx, gen.ListCollectionItemsParams{
		CollectionID: col.ID,
	})
	if err != nil {
		t.Fatalf("ListCollectionItems unrestricted: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("nil ceiling: got %d rows, want 2", len(rows))
	}
}

// TestListItemsByGenre_Integration_RatingGate proves the auto-genre
// "Action" row hides R items from a kid profile while showing them
// to an unrestricted caller. Also verifies CountItemsByGenre stays
// in sync with the filtered list — otherwise pagination would lie.
func TestListItemsByGenre_Integration_RatingGate(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	lib := seedLibrary(ctx, t, q, "byg-rg-lib-"+uuid.New().String()[:8])
	gKid := seedRated(ctx, t, pool, q, lib, "Family Action", "G")
	rKid := seedRated(ctx, t, pool, q, lib, "Hard Action", "R")
	for _, id := range []uuid.UUID{gKid, rKid} {
		if _, err := pool.Exec(ctx, `UPDATE media_items SET genres = $1 WHERE id = $2`, []string{"Action"}, id); err != nil {
			t.Fatalf("set genres: %v", err)
		}
	}

	// PG ceiling — only G.
	rows, err := q.ListItemsByGenre(ctx, gen.ListItemsByGenreParams{
		Genre: "Action", Lim: 100, Off: 0, MaxRatingRank: rankPtr(2),
	})
	if err != nil {
		t.Fatalf("ListItemsByGenre PG: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != gKid {
		t.Errorf("PG ceiling: got %d rows; want 1 (G only)", len(rows))
	}

	cnt, err := q.CountItemsByGenre(ctx, gen.CountItemsByGenreParams{
		Genre: "Action", MaxRatingRank: rankPtr(2),
	})
	if err != nil {
		t.Fatalf("CountItemsByGenre PG: %v", err)
	}
	if cnt != 1 {
		t.Errorf("count vs list mismatch: count=%d, list=1 — pagination would lie", cnt)
	}

	// No ceiling — both items.
	rows, _ = q.ListItemsByGenre(ctx, gen.ListItemsByGenreParams{
		Genre: "Action", Lim: 100, Off: 0,
	})
	if len(rows) != 2 {
		t.Errorf("nil ceiling: got %d rows, want 2", len(rows))
	}
}

// TestListWatchHistory_Integration_RatingGate proves that lowering
// a profile's ceiling retroactively hides past plays of items above
// the new ceiling — important for the "admin tightens parental
// control after the fact" flow.
func TestListWatchHistory_Integration_RatingGate(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "wh-rg-u-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "wh-rg-lib-"+uuid.New().String()[:8])
	gItem := seedRated(ctx, t, pool, q, lib, "Family", "G")
	rItem := seedRated(ctx, t, pool, q, lib, "Adult", "R")

	// Insert a 'stop' event for each item.
	for _, mid := range []uuid.UUID{gItem, rItem} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO watch_events (user_id, media_id, event_type, position_ms, duration_ms, occurred_at)
			 VALUES ($1, $2, 'stop', 1000, 5000, NOW())`,
			user, mid,
		); err != nil {
			t.Fatalf("insert watch_event: %v", err)
		}
	}

	rows, err := q.ListWatchHistory(ctx, gen.ListWatchHistoryParams{
		UserID: user, Lim: 50, Off: 0, MaxRatingRank: rankPtr(2),
	})
	if err != nil {
		t.Fatalf("ListWatchHistory PG: %v", err)
	}
	if len(rows) != 1 || rows[0].MediaID != gItem {
		t.Errorf("PG ceiling: got %d rows; want 1 (G item only)", len(rows))
	}

	// Nil ceiling — both events.
	rows, _ = q.ListWatchHistory(ctx, gen.ListWatchHistoryParams{
		UserID: user, Lim: 50, Off: 0,
	})
	if len(rows) != 2 {
		t.Errorf("nil ceiling: got %d rows, want 2", len(rows))
	}
}
