//go:build integration

package gen_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// An album's count, cover, page and total all see the same photos: live
// ones within the caller's rating ceiling, in libraries they can open.
func TestPhotoAlbums_FilterByCeilingAndLibrary(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := gen.New(pool)
	libA := seedLibrary(ctx, t, q, "photos-a-"+uuid.NewString()[:8])
	libB := seedLibrary(ctx, t, q, "photos-b-"+uuid.NewString()[:8])
	user, err := q.CreateUser(ctx, gen.CreateUserParams{Username: "u-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	userPG := pgtype.UUID{Bytes: user.ID, Valid: true}
	album, err := q.CreateCollection(ctx, gen.CreateCollectionParams{UserID: userPG, Name: "Trip", Type: "photo_album"})
	if err != nil {
		t.Fatal(err)
	}

	// Newest first, so each photo would be the cover if it were visible.
	photo := func(lib uuid.UUID, title, rating string, age string) uuid.UUID {
		p := gen.CreateMediaItemParams{LibraryID: lib, Type: "photo", Title: title, SortTitle: title}
		poster := "/art/" + title + ".jpg"
		p.PosterPath = &poster
		if rating != "" {
			p.ContentRating = &rating
		}
		row, err := q.CreateMediaItem(ctx, p)
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		if _, err := pool.Exec(ctx, `UPDATE media_items SET created_at = now() - $2::interval WHERE id = $1`, row.ID, age); err != nil {
			t.Fatal(err)
		}
		if _, err := q.AddCollectionItem(ctx, gen.AddCollectionItemParams{CollectionID: album.ID, MediaItemID: row.ID}); err != nil {
			t.Fatalf("add %s: %v", title, err)
		}
		return row.ID
	}
	photo(libB, "other-library", "", "1 hour")
	photo(libA, "adult", "R", "2 hours")
	deleted := photo(libA, "deleted", "", "3 hours")
	visible := photo(libA, "visible", "PG", "4 hours")
	photo(libA, "older", "G", "5 hours") // unrated would be hidden: it ranks above every ceiling
	if _, err := pool.Exec(ctx, `UPDATE media_items SET deleted_at = now() WHERE id = $1`, deleted); err != nil {
		t.Fatal(err)
	}

	pg := int32(*contentrating.MaxRatingRank("PG"))
	onlyA := []uuid.UUID{libA}

	albums, err := q.ListMyPhotoAlbums(ctx, gen.ListMyPhotoAlbumsParams{UserID: userPG, MaxRatingRank: &pg, LibraryIds: onlyA})
	if err != nil || len(albums) != 1 {
		t.Fatalf("list: %v %+v", err, albums)
	}
	if albums[0].ItemCount != 2 || albums[0].CoverPath == nil || *albums[0].CoverPath != "/art/visible.jpg" {
		t.Errorf("restricted: count %d, cover %v; want 2 and the visible photo", albums[0].ItemCount, albums[0].CoverPath)
	}

	items, err := q.ListPhotoAlbumItems(ctx, gen.ListPhotoAlbumItemsParams{CollectionID: album.ID, MaxRatingRank: &pg, LibraryIds: onlyA, Lim: 1})
	if err != nil || len(items) != 1 || items[0].ID != visible {
		t.Errorf("first page of one: %v %+v; want the visible photo", err, items)
	}
	total, err := q.CountPhotoAlbumItems(ctx, gen.CountPhotoAlbumItemsParams{CollectionID: album.ID, MaxRatingRank: &pg, LibraryIds: onlyA})
	if err != nil || total != 2 {
		t.Errorf("total %d, %v; want 2", total, err)
	}

	// Unrestricted: everything live.
	albums, err = q.ListMyPhotoAlbums(ctx, gen.ListMyPhotoAlbumsParams{UserID: userPG})
	if err != nil || albums[0].ItemCount != 4 || *albums[0].CoverPath != "/art/other-library.jpg" {
		t.Errorf("unrestricted: %v count %d cover %v; want 4 and the newest", err, albums[0].ItemCount, albums[0].CoverPath)
	}
	// No libraries at all: nothing.
	albums, err = q.ListMyPhotoAlbums(ctx, gen.ListMyPhotoAlbumsParams{UserID: userPG, LibraryIds: []uuid.UUID{}})
	if err != nil || albums[0].ItemCount != 0 || albums[0].CoverPath != nil {
		t.Errorf("no libraries: %v count %d cover %v; want 0 and none", err, albums[0].ItemCount, albums[0].CoverPath)
	}
}
