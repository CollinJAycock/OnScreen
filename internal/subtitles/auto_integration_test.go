//go:build integration

package subtitles

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/subtitles/opensubtitles"
	"github.com/onscreen/onscreen/internal/testdb"
)

// The automatic download's SQL against real Postgres: the candidate query
// keeps live movies and episodes with a file, carries an episode's show and
// numbers, and drops an (item, language) once it is downloaded, given up, or
// waiting on a retry that isn't due; a run records its attempts.
func TestAutoDownload_Integration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := gen.New(pool)

	mkLib := func(typ string) uuid.UUID {
		lib, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
			Name: typ + "-" + uuid.NewString()[:8], Type: typ, ScanPaths: []string{"/nonexistent/" + typ},
			Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
		})
		if err != nil {
			t.Fatalf("create library: %v", err)
		}
		return lib.ID
	}
	movies, shows := mkLib("movie"), mkLib("show")
	item := func(lib uuid.UUID, typ, title string, parent uuid.UUID, index int32, withFile bool) uuid.UUID {
		p := gen.CreateMediaItemParams{LibraryID: lib, Type: typ, Title: title, SortTitle: title, Genres: []string{}, Tags: []string{}}
		if parent != uuid.Nil {
			p.ParentID = pgtype.UUID{Bytes: parent, Valid: true}
		}
		if index != 0 {
			p.Index = &index
		}
		row, err := q.CreateMediaItem(ctx, p)
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		if withFile {
			if _, err := q.CreateMediaFile(ctx, gen.CreateMediaFileParams{
				MediaItemID: row.ID, FilePath: "/nonexistent/" + row.ID.String() + ".mkv", FileSize: 1 << 20,
			}); err != nil {
				t.Fatalf("create file: %v", err)
			}
		}
		return row.ID
	}
	film := item(movies, "movie", "Film", uuid.Nil, 0, true)
	noFile := item(movies, "movie", "No File", uuid.Nil, 0, false)
	show := item(shows, "show", "The Show", uuid.Nil, 0, false)
	season := item(shows, "season", "Season 2", show, 2, false)
	ep := item(shows, "episode", "Pilot-ish", season, 5, true)
	gone := item(movies, "movie", "Deleted", uuid.Nil, 0, true)
	if _, err := pool.Exec(ctx, `UPDATE media_items SET deleted_at = now() WHERE id = $1`, gone); err != nil {
		t.Fatal(err)
	}

	cands := func(langs ...string) map[uuid.UUID]gen.ListSubtitleAutoCandidatesRow {
		rows, err := q.ListSubtitleAutoCandidates(ctx, gen.ListSubtitleAutoCandidatesParams{
			LibraryIds: []uuid.UUID{movies, shows}, Languages: langs, ResultLimit: 50,
		})
		if err != nil {
			t.Fatalf("candidates: %v", err)
		}
		out := map[uuid.UUID]gen.ListSubtitleAutoCandidatesRow{}
		for _, r := range rows {
			out[r.ID] = r
		}
		return out
	}
	got := cands("en")
	if len(got) != 2 {
		t.Fatalf("want film + episode, got %d: %v", len(got), got)
	}
	if _, ok := got[noFile]; ok {
		t.Fatal("item without a file is a candidate")
	}
	e := got[ep]
	if e.ShowTitle != "The Show" || e.SeasonNumber == nil || *e.SeasonNumber != 2 || e.EpisodeNumber == nil || *e.EpisodeNumber != 5 {
		t.Fatalf("episode row: %+v", e)
	}
	if got[film].ShowTitle != "" {
		t.Fatalf("movie has a show title: %q", got[film].ShowTitle)
	}

	attempt := func(id uuid.UUID, lang, status string, next time.Time) {
		var n pgtype.Timestamptz
		if !next.IsZero() {
			n = pgtype.Timestamptz{Time: next, Valid: true}
		}
		if err := q.UpsertSubtitleAutoAttempt(ctx, gen.UpsertSubtitleAutoAttemptParams{
			MediaID: id, Language: lang, Attempts: 1, Status: status, NextAttemptAt: n,
		}); err != nil {
			t.Fatal(err)
		}
	}
	attempt(film, "en", "downloaded", time.Time{})
	attempt(ep, "en", "retry", time.Now().Add(time.Hour))
	if got := cands("en"); len(got) != 0 {
		t.Fatalf("done / not-due rows still candidates: %v", got)
	}
	// Another language is still open for both.
	if got := cands("en", "es"); len(got) != 2 {
		t.Fatalf("es still wanted: %d", len(got))
	}
	attempt(ep, "en", "retry", time.Now().Add(-time.Minute))
	if got := cands("en"); len(got) != 1 || got[ep].ID != ep {
		t.Fatalf("due retry not a candidate: %v", got)
	}
	attempt(ep, "en", "gave_up", time.Time{})
	if got := cands("en"); len(got) != 0 {
		t.Fatalf("given-up row is a candidate: %v", got)
	}

	// A run over the real tables: the film needs French; the provider has
	// nothing, so a retry is scheduled a day out.
	finder := &autoFakeFinder{configured: true, results: map[string][]opensubtitles.SearchResult{}}
	a := &AutoDownloader{
		q:      q,
		finder: finder,
		config: func(context.Context) AutoConfig {
			return AutoConfig{Enabled: true, Languages: []string{"fr"}, HearingImpaired: HIAny}
		},
		libraries:   func(context.Context) ([]uuid.UUID, error) { return []uuid.UUID{movies}, nil },
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:         time.Now,
		hash:        MovieHash,
		sidecars:    SidecarLanguages,
		maxSearches: autoMaxSearches,
	}
	if _, err := a.Run(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := q.ListSubtitleAutoAttempts(ctx, []uuid.UUID{film})
	if err != nil {
		t.Fatal(err)
	}
	var fr *gen.SubtitleAutoAttempt
	for i := range rows {
		if rows[i].Language == "fr" {
			fr = &rows[i]
		}
	}
	if fr == nil || fr.Status != "retry" || fr.Attempts != 1 || !fr.NextAttemptAt.Valid ||
		fr.NextAttemptAt.Time.Sub(time.Now()) < 23*time.Hour {
		t.Fatalf("fr attempt: %+v", fr)
	}
	if len(finder.searches) != 1 || finder.searches[0].Query != "Film" {
		t.Fatalf("searches: %+v", finder.searches)
	}
}
