//go:build integration

// The music discovery endpoints against a real Postgres: similar artists
// (co-listening from item_cooccurrence, shared genres), "Appears on"
// (track_artists) and the instant mix, each under the three visibility
// rules — library access, the content-rating ceiling (on the track itself)
// and label restrictions (a label on an album hides its tracks by
// inheritance) — and a one-star rating keeping a track out of mixes.
//
// Run with: go test -tags=integration -run Integration_Music ./internal/api/v1/
package v1

import (
	"context"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestIntegration_Music_DiscoveryVisibility(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := gen.New(pool)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}

	lib, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
		Name: "Music", Type: "music", ScanPaths: []string{"/music"},
		Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	item := func(typ, title string, parent uuid.UUID, genres []string, year int32) uuid.UUID {
		t.Helper()
		p := gen.CreateMediaItemParams{LibraryID: lib.ID, Type: typ, Title: title, SortTitle: title, Genres: genres, Tags: []string{}}
		if parent != uuid.Nil {
			p.ParentID = pgtype.UUID{Bytes: parent, Valid: true}
		}
		if year != 0 {
			p.Year = &year
		}
		row, err := q.CreateMediaItem(ctx, p)
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		if typ == "track" {
			if _, err := q.CreateMediaFile(ctx, gen.CreateMediaFileParams{
				MediaItemID: row.ID, FilePath: "/music/" + title + ".flac", FileSize: 1,
			}); err != nil {
				t.Fatal(err)
			}
		}
		return row.ID
	}
	seed := item("artist", "Seed", uuid.Nil, []string{}, 0)
	seedAlbum := item("album", "Seed Album", seed, []string{"Rock"}, 1975)
	s1 := item("track", "s1", seedAlbum, []string{}, 0)
	s2 := item("track", "s2", seedAlbum, []string{}, 0)
	sim := item("artist", "Sim", uuid.Nil, []string{}, 0)
	simAlbum := item("album", "Sim Album", sim, []string{"rock"}, 1977)
	m1 := item("track", "m1", simAlbum, []string{}, 0)
	m2 := item("track", "m2", simAlbum, []string{}, 0)
	other := item("artist", "Other", uuid.Nil, []string{}, 0)
	otherAlbum := item("album", "Other Album", other, []string{"Jazz"}, 1960)
	o1 := item("track", "o1", otherAlbum, []string{}, 0)
	feat := item("artist", "Feat", uuid.Nil, []string{}, 0)
	// Feat appears on s1 and on Sim's m1.
	exec(`INSERT INTO track_artists (track_id, artist_id, role) VALUES ($1, $2, 'featured'), ($3, $2, 'featured')`, s1, feat, m1)
	// Everything is rated G except s1 (R).
	exec(`UPDATE media_items SET content_rating = 'G' WHERE library_id = $1`, lib.ID)
	exec(`UPDATE media_items SET content_rating = 'R' WHERE id = $1`, s1)
	// People who play s2 also play o1: Other is co-listened with Seed.
	a, b := s2, o1
	if b.String() < a.String() {
		a, b = b, a
	}
	exec(`INSERT INTO item_cooccurrence (item_a, item_b, score) VALUES ($1, $2, 5)`, a, b)
	user, err := q.CreateUser(ctx, gen.CreateUserParams{Username: "listener"})
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO user_ratings (user_id, media_item_id, score) VALUES ($1, $2, 1)`, user.ID, m2)

	access := &stubLibraryAccessChecker{allowed: map[uuid.UUID]struct{}{lib.ID: {}}}
	h := NewMusicHandler(q, slog.New(slog.NewTextHandler(io.Discard, nil))).WithLibraryAccess(access)
	h.rnd = func() *rand.Rand { return rand.New(rand.NewSource(1)) }

	free := &auth.Claims{UserID: user.ID}
	pg13 := &auth.Claims{UserID: user.ID, MaxContentRating: "PG-13"}
	blocked := &auth.Claims{UserID: user.ID, BlockedLabels: []string{"explicit"}}
	call := func(fn http.HandlerFunc, claims *auth.Claims, target, id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		if id != "" {
			r = withChiParam(r, "id", id)
		}
		r = r.WithContext(WithItemLabelGate(middleware.WithClaims(r.Context(), claims), q))
		rec := httptest.NewRecorder()
		fn(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", target, rec.Code, rec.Body.String())
		}
		return rec
	}
	ids := func(list []string) map[string]bool {
		out := map[string]bool{}
		for _, s := range list {
			out[s] = true
		}
		return out
	}

	// ── Appears on ──────────────────────────────────────────────────────
	appears := func(claims *auth.Claims) map[string]bool {
		var titles []string
		for _, a := range decodeList[ArtistAppearanceResponse](t, call(h.AppearsOn, claims, "/", feat.String())) {
			titles = append(titles, a.Title)
		}
		return ids(titles)
	}
	if got := appears(free); !got["s1"] || !got["m1"] || len(got) != 2 {
		t.Errorf("appears on (unrestricted) = %v", got)
	}
	if got := appears(pg13); got["s1"] || !got["m1"] {
		t.Errorf("appears on (PG-13) = %v: the R-rated track must be hidden", got)
	}
	// A label on Seed's album hides its tracks by inheritance.
	exec(`UPDATE media_items SET labels = '{explicit}' WHERE id = $1`, seedAlbum)
	if got := appears(blocked); got["s1"] || !got["m1"] {
		t.Errorf("appears on (label-blocked album) = %v", got)
	}
	exec(`UPDATE media_items SET labels = '{}' WHERE id = $1`, seedAlbum)

	// ── Similar artists ─────────────────────────────────────────────────
	similar := func(claims *auth.Claims) []SimilarArtistResponse {
		return decodeList[SimilarArtistResponse](t, call(h.Similar, claims, "/", seed.String()))
	}
	got := similar(free)
	if len(got) != 2 || got[0].Title != "Other" || !got[0].CoListened || got[1].Title != "Sim" ||
		len(got[1].SharedGenres) != 1 || got[1].SharedGenres[0] != "rock" {
		t.Errorf("similar = %+v, want Other (co-listened) then Sim (shared rock)", got)
	}
	exec(`UPDATE media_items SET labels = '{explicit}' WHERE id = $1`, sim)
	for _, s := range similar(blocked) {
		if s.Title == "Sim" {
			t.Error("a label-blocked artist must not be suggested")
		}
	}
	exec(`UPDATE media_items SET labels = '{}' WHERE id = $1`, sim)

	// ── Instant mix ─────────────────────────────────────────────────────
	mix := func(claims *auth.Claims, seedID uuid.UUID) []string {
		var out []string
		for _, m := range decodeList[MixTrackResponse](t, call(h.Mix, claims, "/music/mix?seed="+seedID.String(), "")) {
			if m.FileID == "" {
				t.Errorf("mix track %s has no file", m.Title)
			}
			out = append(out, m.Title)
		}
		return out
	}
	tracks := mix(free, s2)
	if len(tracks) == 0 || tracks[0] != "s2" {
		t.Fatalf("mix = %v, want s2 first", tracks)
	}
	got2 := ids(tracks)
	for _, want := range []string{"s1", "m1", "o1"} {
		if !got2[want] {
			t.Errorf("mix %v is missing %s", tracks, want)
		}
	}
	if got2["m2"] {
		t.Errorf("mix %v includes m2, which the listener rated one star", tracks)
	}
	if got := ids(mix(pg13, s2)); got["s1"] {
		t.Errorf("PG-13 mix includes the R-rated s1: %v", got)
	}
	// An artist seed whose album is label-blocked for the caller: the
	// artist itself is visible, its blocked tracks are not.
	exec(`UPDATE media_items SET labels = '{explicit}' WHERE id = $1`, seedAlbum)
	if got := ids(mix(blocked, seed)); got["s1"] || got["s2"] {
		t.Errorf("label-blocked album's tracks in the mix: %v", got)
	}
}
