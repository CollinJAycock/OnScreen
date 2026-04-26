//go:build integration

// Round-trips the user_favorites queries. Favorites is "low-stakes" by
// content but high-frequency by call: every Items list does an
// IsFavorite probe, and Add/Remove are the user's mental "hide" path
// for items they're tired of seeing.
//
// Run with: go test -tags=integration ./internal/db/gen/...
package gen_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestFavorites_Integration_AddIsIdempotent — the SQL uses
// ON CONFLICT DO NOTHING so adding the same favorite twice must
// succeed without error and without creating a duplicate row.
// Otherwise a double-click on the heart icon would fail.
func TestFavorites_Integration_AddIsIdempotent(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "fav-idem-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "fav-idem-lib-"+uuid.New().String()[:8])
	item := seedMediaItem(ctx, t, q, lib, "Movie")

	for i := 0; i < 3; i++ {
		if err := q.AddFavorite(ctx, gen.AddFavoriteParams{UserID: user, MediaID: item}); err != nil {
			t.Fatalf("AddFavorite (call %d): %v", i+1, err)
		}
	}

	if c, _ := q.CountFavorites(ctx, user); c != 1 {
		t.Errorf("count = %d, want 1 — duplicate inserts should be no-ops", c)
	}
}

// TestFavorites_Integration_RemoveOnlyAffectsTargetPair — removing
// a (user, media) pair must not affect the same media for OTHER
// users, or other media for the same user.
func TestFavorites_Integration_RemoveOnlyAffectsTargetPair(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	alice := seedUser(ctx, t, q, "fav-rm-a-"+uuid.New().String()[:8])
	bob := seedUser(ctx, t, q, "fav-rm-b-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "fav-rm-lib-"+uuid.New().String()[:8])
	itemA := seedMediaItem(ctx, t, q, lib, "Movie A")
	itemB := seedMediaItem(ctx, t, q, lib, "Movie B")

	_ = q.AddFavorite(ctx, gen.AddFavoriteParams{UserID: alice, MediaID: itemA})
	_ = q.AddFavorite(ctx, gen.AddFavoriteParams{UserID: alice, MediaID: itemB})
	_ = q.AddFavorite(ctx, gen.AddFavoriteParams{UserID: bob, MediaID: itemA})

	// Alice removes Movie A.
	if err := q.RemoveFavorite(ctx, gen.RemoveFavoriteParams{UserID: alice, MediaID: itemA}); err != nil {
		t.Fatalf("RemoveFavorite: %v", err)
	}

	// Alice's Movie A: gone. Alice's Movie B: still there. Bob's Movie A: still there.
	if ok, _ := q.IsFavorite(ctx, gen.IsFavoriteParams{UserID: alice, MediaID: itemA}); ok {
		t.Error("alice's Movie A still favorited after remove")
	}
	if ok, _ := q.IsFavorite(ctx, gen.IsFavoriteParams{UserID: alice, MediaID: itemB}); !ok {
		t.Error("alice's Movie B disappeared — Remove leaked beyond target pair")
	}
	if ok, _ := q.IsFavorite(ctx, gen.IsFavoriteParams{UserID: bob, MediaID: itemA}); !ok {
		t.Error("bob's Movie A disappeared — Remove leaked across users")
	}
}

// TestFavorites_Integration_ListSkipsSoftDeletedItems — when a media
// item is soft-deleted (deleted_at IS NOT NULL), it must drop out of
// the user's favorites list without us having to hard-delete the
// favorite row. This is the behaviour the scanner relies on after
// detecting a removed file.
func TestFavorites_Integration_ListSkipsSoftDeletedItems(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "fav-soft-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "fav-soft-lib-"+uuid.New().String()[:8])
	itemA := seedMediaItem(ctx, t, q, lib, "Live")
	itemB := seedMediaItem(ctx, t, q, lib, "Doomed")

	_ = q.AddFavorite(ctx, gen.AddFavoriteParams{UserID: user, MediaID: itemA})
	_ = q.AddFavorite(ctx, gen.AddFavoriteParams{UserID: user, MediaID: itemB})

	// Soft-delete itemB.
	if err := q.SoftDeleteMediaItem(ctx, itemB); err != nil {
		t.Fatalf("SoftDeleteMediaItem: %v", err)
	}

	rows, err := q.ListFavorites(ctx, gen.ListFavoritesParams{
		UserID: user, Limit: 100, Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListFavorites: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (soft-deleted should be hidden)", len(rows))
	}
	if rows[0].ID != itemA {
		t.Errorf("wrong item visible: got %s, want %s", rows[0].ID, itemA)
	}
}

// TestFavorites_Integration_CountIsUserScoped — same WHERE user_id
// guard as before. CountFavorites for alice must not include bob's.
func TestFavorites_Integration_CountIsUserScoped(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	alice := seedUser(ctx, t, q, "fav-cnt-a-"+uuid.New().String()[:8])
	bob := seedUser(ctx, t, q, "fav-cnt-b-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "fav-cnt-lib-"+uuid.New().String()[:8])
	item := seedMediaItem(ctx, t, q, lib, "Movie")

	_ = q.AddFavorite(ctx, gen.AddFavoriteParams{UserID: alice, MediaID: item})
	_ = q.AddFavorite(ctx, gen.AddFavoriteParams{UserID: bob, MediaID: item})

	if c, _ := q.CountFavorites(ctx, alice); c != 1 {
		t.Errorf("alice count = %d, want 1", c)
	}
	if c, _ := q.CountFavorites(ctx, bob); c != 1 {
		t.Errorf("bob count = %d, want 1", c)
	}
}
