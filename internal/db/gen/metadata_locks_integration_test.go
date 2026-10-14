//go:build integration

package gen_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestMetadataLocks_Integration runs the field-lock rules (migration 00043)
// against Postgres: an admin edit writes and locks only the fields it names;
// the automatic writes (UpdateMediaItemMetadata — enrichment, refresh, Fix
// Match; UpdateMediaItemTitle; UpdateMediaItemTakenAt) keep locked fields
// and still write the rest, provider IDs included; a locked title keeps the
// sort title; unlocking hands a field back to the next refresh; and the
// generated search_vector follows an edited title.
//
// Run with: go test -tags 'dev integration' -run MetadataLocks ./internal/db/gen/
func TestMetadataLocks_Integration(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := gen.New(pool)

	var lib uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO libraries (name, type, scan_paths) VALUES ('M', 'movie', '{/m}') RETURNING id`).Scan(&lib); err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO media_items (library_id, type, title, sort_title, year, summary, tagline, content_rating, genres)
		VALUES ($1, 'movie', 'Provider Title', 'Provider Title', 2001, 'provider summary', 'provider tagline', 'PG', '{Drama}')
		RETURNING id`, lib).Scan(&id); err != nil {
		t.Fatal(err)
	}
	ptr := func(s string) *string { return &s }
	i32 := func(v int32) *int32 { return &v }
	date := func(s string) pgtype.Date {
		d, _ := time.Parse("2006-01-02", s)
		return pgtype.Date{Time: d, Valid: true}
	}

	// The admin edits title, summary, content rating and genres; the sort
	// title derived from the title is written but not locked.
	row, err := q.EditMediaItemMetadata(ctx, gen.EditMediaItemMetadataParams{
		ID:            id,
		SetFields:     []string{"title", "sort_title", "summary", "content_rating", "genres"},
		LockFields:    []string{"title", "summary", "content_rating", "genres"},
		UnlockFields:  []string{},
		Title:         "Director's Cut",
		SortTitle:     "Director's Cut",
		Summary:       ptr("my summary"),
		ContentRating: ptr("R"),
		Genres:        []string{"Sci-Fi", "Noir"},
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if row.Title != "Director's Cut" || *row.Summary != "my summary" || *row.ContentRating != "R" ||
		!slices.Equal(row.Genres, []string{"Sci-Fi", "Noir"}) {
		t.Errorf("edit didn't write: %+v", row)
	}
	if *row.Tagline != "provider tagline" || *row.Year != 2001 {
		t.Errorf("edit touched fields it didn't name: tagline=%q year=%d", *row.Tagline, *row.Year)
	}
	if want := []string{"content_rating", "genres", "summary", "title"}; !slices.Equal(row.LockedFields, want) {
		t.Errorf("locked_fields = %v, want %v", row.LockedFields, want)
	}

	// A refresh / Fix Match: new provider values for everything, a new
	// tmdb_id. Locked fields keep the admin's values (and the locked title
	// keeps the sort title); the rest and the provider ID update.
	refresh := gen.UpdateMediaItemMetadataParams{
		ID:                    id,
		Title:                 "Provider Title 2",
		SortTitle:             "Provider Title 2",
		Year:                  i32(2002),
		Summary:               ptr("provider summary 2"),
		Tagline:               ptr("provider tagline 2"),
		ContentRating:         ptr("PG-13"),
		Genres:                []string{"Action"},
		Tags:                  []string{},
		OriginallyAvailableAt: date("2002-05-01"),
		TmdbID:                i32(78),
	}
	up, err := q.UpdateMediaItemMetadata(ctx, refresh)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if up.Title != "Director's Cut" || up.SortTitle != "Director's Cut" || *up.Summary != "my summary" ||
		*up.ContentRating != "R" || !slices.Equal(up.Genres, []string{"Sci-Fi", "Noir"}) {
		t.Errorf("refresh overwrote a locked field: %+v", up)
	}
	if *up.Year != 2002 || *up.Tagline != "provider tagline 2" || up.TmdbID == nil || *up.TmdbID != 78 ||
		up.OriginallyAvailableAt.Time.Format("2006-01-02") != "2002-05-01" {
		t.Errorf("refresh didn't write unlocked fields: %+v", up)
	}

	// The narrow title write (re-enrich-unmatched clean-up, music tags).
	if err := q.UpdateMediaItemTitle(ctx, gen.UpdateMediaItemTitleParams{ID: id, Title: "X", SortTitle: "X"}); err != nil {
		t.Fatal(err)
	}
	got, err := q.GetMediaItem(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Director's Cut" || got.SortTitle != "Director's Cut" {
		t.Errorf("UpdateMediaItemTitle overwrote a locked title: %q / %q", got.Title, got.SortTitle)
	}
	if !slices.Contains(got.LockedFields, "title") {
		t.Errorf("GetMediaItem locked_fields = %v", got.LockedFields)
	}

	// The edited title is searchable (search_vector is generated).
	var hits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_items WHERE id = $1 AND search_vector @@ plainto_tsquery('english', 'director')`, id).Scan(&hits); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Error("search_vector doesn't follow the edited title")
	}

	// Taken-at: free until locked, kept once locked. Clearing the date
	// through the editor (set, nil value) locks it as cleared.
	if err := q.UpdateMediaItemTakenAt(ctx, gen.UpdateMediaItemTakenAtParams{ID: id, TakenAt: date("2003-01-01")}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.EditMediaItemMetadata(ctx, gen.EditMediaItemMetadataParams{
		ID: id, SetFields: []string{"originally_available_at"}, LockFields: []string{"originally_available_at"},
		UnlockFields: []string{}, Genres: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateMediaItemTakenAt(ctx, gen.UpdateMediaItemTakenAtParams{ID: id, TakenAt: date("2004-01-01")}); err != nil {
		t.Fatal(err)
	}
	if got, _ = q.GetMediaItem(ctx, id); got.OriginallyAvailableAt.Valid {
		t.Errorf("locked (cleared) date was rewritten: %v", got.OriginallyAvailableAt.Time)
	}

	// An unlock-only edit with empty arrays keeps the other locks and the
	// values; the next refresh then takes the title (and its sort title).
	row, err = q.EditMediaItemMetadata(ctx, gen.EditMediaItemMetadataParams{
		ID: id, SetFields: []string{}, LockFields: []string{}, UnlockFields: []string{"title"}, Genres: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.Title != "Director's Cut" || slices.Contains(row.LockedFields, "title") || !slices.Contains(row.LockedFields, "genres") {
		t.Errorf("unlock: title=%q locks=%v", row.Title, row.LockedFields)
	}
	if up, err = q.UpdateMediaItemMetadata(ctx, refresh); err != nil {
		t.Fatal(err)
	}
	if up.Title != "Provider Title 2" || up.SortTitle != "Provider Title 2" || *up.Summary != "my summary" {
		t.Errorf("after unlock: title=%q sort=%q summary=%q", up.Title, up.SortTitle, *up.Summary)
	}

	// An explicitly locked sort title survives refreshes on its own.
	if _, err := q.EditMediaItemMetadata(ctx, gen.EditMediaItemMetadataParams{
		ID: id, SetFields: []string{"sort_title"}, LockFields: []string{"sort_title"}, UnlockFields: []string{},
		SortTitle: "Cut, Director's", Genres: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	refresh.Title, refresh.SortTitle = "Provider Title 3", "Provider Title 3"
	if up, err = q.UpdateMediaItemMetadata(ctx, refresh); err != nil {
		t.Fatal(err)
	}
	if up.Title != "Provider Title 3" || up.SortTitle != "Cut, Director's" {
		t.Errorf("locked sort title: title=%q sort=%q", up.Title, up.SortTitle)
	}

	// Top-level title + year stay unique per library and type.
	if _, err := pool.Exec(ctx, `INSERT INTO media_items (library_id, type, title, sort_title, year) VALUES ($1, 'movie', 'Taken', 'Taken', 2002)`, lib); err != nil {
		t.Fatal(err)
	}
	_, err = q.EditMediaItemMetadata(ctx, gen.EditMediaItemMetadataParams{
		ID: id, SetFields: []string{"title"}, LockFields: []string{"title"}, UnlockFields: []string{},
		Title: "Taken", Genres: []string{},
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Errorf("duplicate title+year: err = %v, want unique violation", err)
	}

	// A deleted item isn't edited.
	if _, err := pool.Exec(ctx, `UPDATE media_items SET deleted_at = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	_, err = q.EditMediaItemMetadata(ctx, gen.EditMediaItemMetadataParams{
		ID: id, SetFields: []string{}, LockFields: []string{}, UnlockFields: []string{}, Genres: []string{},
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("edit of a deleted item: err = %v, want no rows", err)
	}
}
