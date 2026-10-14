//go:build integration

// Label restrictions against a real Postgres: a blocked label hides an item
// from item detail, children (an episode of a blocked show, and a single
// labelled episode), library browse (rows and total), search, the hub, a
// collection's items and count, and playback start (direct stream and
// transcode); an allowed-labels list shows only items carrying one. Every
// surface also runs unrestricted as a positive control. Labels are set
// through the admin label API, which this exercises too.
//
// Run with: go test -tags=integration -run TestLabelRestrictions ./internal/api/v1/
package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/testdb"
)

// lrMedia is the item / transcode handlers' media service over the real
// queries — only the reads those handlers make on these routes.
type lrMedia struct {
	q     *gen.Queries
	files map[uuid.UUID]uuid.UUID // file id → item id (synthetic, not on disk)
}

func (m lrMedia) GetItem(ctx context.Context, id uuid.UUID) (*media.Item, error) {
	row, err := m.q.GetMediaItem(ctx, id)
	if err != nil {
		return nil, media.ErrNotFound
	}
	it := &media.Item{ID: row.ID, LibraryID: row.LibraryID, Type: row.Type, Title: row.Title, ContentRating: row.ContentRating}
	if row.ParentID.Valid {
		p := uuid.UUID(row.ParentID.Bytes)
		it.ParentID = &p
	}
	return it, nil
}

func (m lrMedia) GetFile(_ context.Context, id uuid.UUID) (*media.File, error) {
	itemID, ok := m.files[id]
	if !ok {
		return nil, media.ErrNotFound
	}
	return &media.File{ID: id, MediaItemID: itemID, Status: "active", FilePath: "/nonexistent/" + id.String() + ".mkv"}, nil
}

func (m lrMedia) GetFiles(context.Context, uuid.UUID) ([]media.File, error) { return nil, nil }

func (m lrMedia) ListChildren(ctx context.Context, parentID uuid.UUID) ([]media.Item, error) {
	rows, err := m.q.ListMediaItemChildren(ctx, pgtype.UUID{Bytes: parentID, Valid: true})
	if err != nil {
		return nil, err
	}
	out := make([]media.Item, 0, len(rows))
	for _, r := range rows {
		out = append(out, media.Item{ID: r.ID, LibraryID: r.LibraryID, Type: r.Type, Title: r.Title, ContentRating: r.ContentRating})
	}
	return out, nil
}

func (lrMedia) GetPhotoMetadata(context.Context, uuid.UUID) (*media.PhotoMetadata, error) {
	return nil, media.ErrNotFound
}
func (lrMedia) UpdateItemMetadata(context.Context, media.UpdateItemMetadataParams) (*media.Item, error) {
	return nil, media.ErrNotFound
}
func (lrMedia) RenameItemFile(context.Context, uuid.UUID, string) (string, bool, error) {
	return "", false, nil
}
func (lrMedia) TouchItemFileMtime(context.Context, uuid.UUID, time.Time) error { return nil }

func TestLabelRestrictions_Integration(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// ── fixture ──────────────────────────────────────────────────────────
	kidRow, err := q.CreateUser(ctx, gen.CreateUserParams{Username: "lr-kid-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	kid := kidRow.ID
	lib := func(name, typ string) uuid.UUID {
		l, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
			Name: name + "-" + uuid.NewString()[:8], Type: typ, ScanPaths: []string{"/tmp/" + name},
			Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
		})
		if err != nil {
			t.Fatalf("create library: %v", err)
		}
		return l.ID
	}
	libTV, libMovies := lib("lr-tv", "show"), lib("lr-movies", "movie")
	g := rcStr("G")
	item := func(libID uuid.UUID, typ, title string, parent uuid.UUID, index int32) uuid.UUID {
		poster := "/p/" + strings.ReplaceAll(title, " ", "_") + ".jpg"
		p := gen.CreateMediaItemParams{
			LibraryID: libID, Type: typ, Title: title, SortTitle: strings.ToLower(title),
			ContentRating: g, Genres: []string{}, Tags: []string{}, PosterPath: &poster,
		}
		if parent != uuid.Nil {
			p.ParentID = pgtype.UUID{Bytes: parent, Valid: true}
		}
		if index != 0 {
			p.Index = &index
		}
		row, err := q.CreateMediaItem(ctx, p)
		if err != nil {
			t.Fatalf("create %s %q: %v", typ, title, err)
		}
		return row.ID
	}
	scaryShow := item(libTV, "show", "Zebra Scary Show", uuid.Nil, 0)
	scarySeason := item(libTV, "season", "Zebra Scary Show S1", scaryShow, 1)
	scaryEp := item(libTV, "episode", "Zebra Scary Episode", scarySeason, 1)
	fineShow := item(libTV, "show", "Zebra Fine Show", uuid.Nil, 0)
	fineSeason := item(libTV, "season", "Zebra Fine Show S1", fineShow, 1)
	fineEp1 := item(libTV, "episode", "Zebra Fine Episode One", fineSeason, 1)
	fineEp2 := item(libTV, "episode", "Zebra Fine Episode Two", fineSeason, 2)
	scaryMovie := item(libMovies, "movie", "Zebra Scary Movie", uuid.Nil, 0)
	kidsMovie := item(libMovies, "movie", "Zebra Kids Movie", uuid.Nil, 0)
	plainMovie := item(libMovies, "movie", "Zebra Plain Movie", uuid.Nil, 0)

	playlist, err := q.CreateCollection(ctx, gen.CreateCollectionParams{
		UserID: pgtype.UUID{Bytes: kid, Valid: true}, Name: "lr-playlist", Type: "playlist",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []uuid.UUID{scaryMovie, kidsMovie, plainMovie} {
		if _, err := q.AddCollectionItem(ctx, gen.AddCollectionItemParams{CollectionID: playlist.ID, MediaItemID: m}); err != nil {
			t.Fatal(err)
		}
	}
	scaryFile, plainFile := uuid.New(), uuid.New()
	files := map[uuid.UUID]uuid.UUID{scaryFile: scaryMovie, plainFile: plainMovie}

	// ── handlers ─────────────────────────────────────────────────────────
	mediaSvc := lrMedia{q: q, files: files}
	access := rcAccess{granted: map[uuid.UUID]struct{}{libTV: {}, libMovies: {}}}
	libs := rcLibraries{rcAccess: access, types: map[uuid.UUID]string{libTV: "show", libMovies: "movie"}}
	itemH := NewItemHandler(mediaSvc, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, nil, nil, streaming.NewTracker(), logger).
		WithLibraryAccess(access).WithLabels(q)
	transH := NewNativeTranscodeHandler(nil, nil, mediaSvc, &config.Config{TranscodeMaxHeight: 2160}, logger).WithLibraryAccess(access)
	libH := NewLibraryHandler(libs, logger).WithMedia(rcMedia{q: q})
	searchH := NewSearchHandler(q, logger).WithLibraryAccess(access)
	hubH := NewHubHandler(q, logger).WithLibraryAccess(access).WithWatchRows(q)
	colH := NewCollectionHandler(q, logger).WithLibraryAccess(access)
	labelH := NewLabelHandler(q, logger)

	r := chi.NewRouter()
	r.Use(ItemLabelGateMiddleware(q))
	r.Get("/items/{id}", itemH.Get)
	r.Get("/items/{id}/children", itemH.Children)
	r.Get("/media/stream/{id}", itemH.StreamFile)
	r.Post("/items/{id}/transcode", transH.Start)
	r.Get("/libraries/{id}/items", libH.Items)
	r.Get("/search", searchH.Search)
	r.Get("/hub", hubH.Get)
	r.Get("/collections/{id}/items", colH.Items)
	r.Get("/labels", labelH.List)
	r.Post("/labels/apply", labelH.Apply)
	r.Put("/items/{id}/labels", labelH.SetItem)

	do := func(t *testing.T, c *auth.Claims, method, path string, body any) (int, []byte) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			rd = bytes.NewReader(raw)
		}
		req := httptest.NewRequest(method, path, rd)
		req = req.WithContext(middleware.WithClaims(req.Context(), c))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}
	admin := &auth.Claims{UserID: uuid.New(), Username: "admin", IsAdmin: true}
	blocked := &auth.Claims{UserID: kid, Username: "kid", BlockedLabels: []string{"horror"}}
	allowed := &auth.Claims{UserID: kid, Username: "kid", AllowedLabels: []string{"kids"}}
	free := &auth.Claims{UserID: kid, Username: "kid"}

	// ── labels through the admin API ─────────────────────────────────────
	if code, body := do(t, admin, http.MethodPut, "/items/"+scaryShow.String()+"/labels",
		map[string]any{"labels": []string{"  Horror "}}); code != http.StatusOK {
		t.Fatalf("set show labels: %d %s", code, body)
	}
	if code, body := do(t, admin, http.MethodPost, "/labels/apply", map[string]any{
		"item_ids": []string{scaryMovie.String(), fineEp2.String()}, "add": []string{"HORROR"},
	}); code != http.StatusOK || !strings.Contains(string(body), `"added":2`) {
		t.Fatalf("bulk add horror: %d %s", code, body)
	}
	if code, body := do(t, admin, http.MethodPost, "/labels/apply", map[string]any{
		"item_ids": []string{kidsMovie.String()}, "add": []string{"kids"},
	}); code != http.StatusOK {
		t.Fatalf("bulk add kids: %d %s", code, body)
	}
	if code, _ := do(t, free, http.MethodPost, "/labels/apply", map[string]any{
		"item_ids": []string{plainMovie.String()}, "add": []string{"x"},
	}); code != http.StatusForbidden {
		t.Fatalf("non-admin bulk apply = %d, want 403", code)
	}
	t.Run("labels in use", func(t *testing.T) {
		code, body := do(t, admin, http.MethodGet, "/labels", nil)
		if code != http.StatusOK {
			t.Fatalf("%d %s", code, body)
		}
		var env struct {
			Data []labelInUseResponse `json:"data"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatal(err)
		}
		got := map[string]int64{}
		for _, l := range env.Data {
			got[l.Label] = l.ItemCount
		}
		if got["horror"] != 3 || got["kids"] != 1 {
			t.Errorf("labels in use = %v, want horror:3 kids:1", got)
		}
	})

	forbiddenBlocked := map[uuid.UUID]string{
		scaryShow: "labelled show", scarySeason: "season of a labelled show", scaryEp: "episode of a labelled show",
		fineEp2: "labelled episode", scaryMovie: "labelled movie",
	}
	noneOf := func(t *testing.T, what string, body []byte, forbidden map[uuid.UUID]string) {
		t.Helper()
		for id, why := range forbidden {
			if strings.Contains(string(body), id.String()) {
				t.Errorf("%s leaked %s (%s)", what, id, why)
			}
		}
	}
	type listEnv struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	list := func(t *testing.T, c *auth.Claims, path string) ([]string, int64, []byte) {
		t.Helper()
		code, body := do(t, c, http.MethodGet, path, nil)
		if code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, code, body)
		}
		var env listEnv
		if err := json.Unmarshal(body, &env); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		out := make([]string, 0, len(env.Data))
		for _, d := range env.Data {
			out = append(out, d.ID)
		}
		sort.Strings(out)
		return out, env.Meta.Total, body
	}
	sorted := func(in ...uuid.UUID) []string {
		out := make([]string, 0, len(in))
		for _, id := range in {
			out = append(out, id.String())
		}
		sort.Strings(out)
		return out
	}
	same := func(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

	t.Run("item detail", func(t *testing.T) {
		for id, why := range forbiddenBlocked {
			if code, _ := do(t, blocked, http.MethodGet, "/items/"+id.String(), nil); code != http.StatusForbidden {
				t.Errorf("blocked detail of %s (%s) = %d, want 403 (same as the rating ceiling)", id, why, code)
			}
			if code, _ := do(t, free, http.MethodGet, "/items/"+id.String(), nil); code != http.StatusOK {
				t.Errorf("unrestricted detail of %s = %d, want 200 (control)", id, code)
			}
		}
		for _, id := range []uuid.UUID{fineShow, fineEp1, plainMovie, kidsMovie} {
			if code, body := do(t, blocked, http.MethodGet, "/items/"+id.String(), nil); code != http.StatusOK {
				t.Errorf("blocked detail of unlabelled %s = %d %s, want 200", id, code, body)
			}
		}
		// Labels are on the admin's detail only.
		_, body := do(t, admin, http.MethodGet, "/items/"+scaryMovie.String(), nil)
		if !strings.Contains(string(body), `"labels":["horror"]`) {
			t.Errorf("admin detail lacks labels: %s", body)
		}
		_, body = do(t, free, http.MethodGet, "/items/"+scaryMovie.String(), nil)
		if strings.Contains(string(body), `"labels"`) {
			t.Errorf("non-admin detail carries labels: %s", body)
		}
	})

	t.Run("children", func(t *testing.T) {
		if code, _ := do(t, blocked, http.MethodGet, "/items/"+scaryShow.String()+"/children", nil); code != http.StatusForbidden {
			t.Errorf("children of a labelled show = %d, want 403", code)
		}
		if code, _ := do(t, blocked, http.MethodGet, "/items/"+scarySeason.String()+"/children", nil); code != http.StatusForbidden {
			t.Errorf("children of a labelled show's season = %d, want 403", code)
		}
		code, body := do(t, blocked, http.MethodGet, "/items/"+fineSeason.String()+"/children", nil)
		if code != http.StatusOK || !strings.Contains(string(body), fineEp1.String()) {
			t.Fatalf("children of the fine season = %d %s", code, body)
		}
		noneOf(t, "children", body, forbiddenBlocked)
		_, body = do(t, free, http.MethodGet, "/items/"+fineSeason.String()+"/children", nil)
		if !strings.Contains(string(body), fineEp2.String()) {
			t.Errorf("unrestricted children lack the labelled episode (control): %s", body)
		}
	})

	t.Run("library browse", func(t *testing.T) {
		got, total, body := list(t, blocked, "/libraries/"+libMovies.String()+"/items")
		if want := sorted(kidsMovie, plainMovie); !same(got, want) || total != 2 {
			t.Errorf("blocked movies = %v (total %d), want %v (total 2)", got, total, want)
		}
		noneOf(t, "browse", body, forbiddenBlocked)
		got, total, _ = list(t, free, "/libraries/"+libMovies.String()+"/items")
		if want := sorted(scaryMovie, kidsMovie, plainMovie); !same(got, want) || total != 3 {
			t.Errorf("unrestricted movies = %v (total %d), want %v", got, total, want)
		}
		got, _, _ = list(t, blocked, "/libraries/"+libTV.String()+"/items")
		if want := sorted(fineShow); !same(got, want) {
			t.Errorf("blocked shows = %v, want %v", got, want)
		}
	})

	t.Run("search", func(t *testing.T) {
		code, body := do(t, blocked, http.MethodGet, "/search?q=zebra", nil)
		if code != http.StatusOK || !strings.Contains(string(body), plainMovie.String()) {
			t.Fatalf("blocked search = %d %s", code, body)
		}
		noneOf(t, "search", body, forbiddenBlocked)
		_, body = do(t, free, http.MethodGet, "/search?q=zebra", nil)
		if !strings.Contains(string(body), scaryMovie.String()) {
			t.Errorf("unrestricted search lacks the labelled movie (control): %s", body)
		}
		_, body = do(t, blocked, http.MethodGet, "/search?q=zebra&library_id="+libMovies.String(), nil)
		noneOf(t, "library search", body, forbiddenBlocked)
	})

	t.Run("hub", func(t *testing.T) {
		code, body := do(t, blocked, http.MethodGet, "/hub", nil)
		if code != http.StatusOK || !strings.Contains(string(body), plainMovie.String()) {
			t.Fatalf("blocked hub = %d %s", code, body)
		}
		noneOf(t, "hub", body, forbiddenBlocked)
		_, body = do(t, free, http.MethodGet, "/hub", nil)
		if !strings.Contains(string(body), scaryMovie.String()) {
			t.Errorf("unrestricted hub lacks the labelled movie (control): %s", body)
		}
	})

	t.Run("collection items and count", func(t *testing.T) {
		path := "/collections/" + playlist.ID.String() + "/items"
		got, total, body := list(t, blocked, path)
		if want := sorted(kidsMovie, plainMovie); !same(got, want) || total != 2 {
			t.Errorf("blocked playlist = %v (total %d), want %v (total 2)", got, total, want)
		}
		noneOf(t, "playlist", body, forbiddenBlocked)
		if got, total, _ := list(t, allowed, path); !same(got, sorted(kidsMovie)) || total != 1 {
			t.Errorf("allowed-mode playlist = %v (total %d), want only the kids movie", got, total)
		}
		if _, total, _ := list(t, free, path); total != 3 {
			t.Errorf("unrestricted playlist total = %d, want 3 (control)", total)
		}
	})

	t.Run("stream start", func(t *testing.T) {
		if code, _ := do(t, blocked, http.MethodGet, "/media/stream/"+scaryFile.String(), nil); code != http.StatusForbidden {
			t.Errorf("direct stream of a labelled movie = %d, want 403", code)
		}
		if code, _ := do(t, blocked, http.MethodGet, "/media/stream/"+plainFile.String(), nil); code == http.StatusForbidden {
			t.Errorf("direct stream of an unlabelled movie = 403, want the gate to pass")
		}
		if code, _ := do(t, blocked, http.MethodPost, "/items/"+scaryMovie.String()+"/transcode", map[string]any{}); code != http.StatusForbidden {
			t.Errorf("transcode start of a labelled movie = %d, want 403", code)
		}
		if code, _ := do(t, blocked, http.MethodPost, "/items/"+scaryEp.String()+"/transcode", map[string]any{}); code != http.StatusForbidden {
			t.Errorf("transcode start of an episode of a labelled show = %d, want 403", code)
		}
		if code, _ := do(t, blocked, http.MethodPost, "/items/"+plainMovie.String()+"/transcode", map[string]any{}); code == http.StatusForbidden {
			t.Errorf("transcode start of an unlabelled movie = 403, want the gate to pass")
		}
	})

	t.Run("allowed labels mode", func(t *testing.T) {
		if code, _ := do(t, allowed, http.MethodGet, "/items/"+kidsMovie.String(), nil); code != http.StatusOK {
			t.Errorf("allowed-mode detail of the kids movie = %d, want 200", code)
		}
		for _, id := range []uuid.UUID{plainMovie, scaryMovie, fineShow, fineEp1} {
			if code, _ := do(t, allowed, http.MethodGet, "/items/"+id.String(), nil); code != http.StatusForbidden {
				t.Errorf("allowed-mode detail of %s (no allowed label) = %d, want 403", id, code)
			}
		}
		got, total, _ := list(t, allowed, "/libraries/"+libMovies.String()+"/items")
		if !same(got, sorted(kidsMovie)) || total != 1 {
			t.Errorf("allowed-mode movies = %v (total %d), want only the kids movie", got, total)
		}
		if got, _, _ := list(t, allowed, "/libraries/"+libTV.String()+"/items"); len(got) != 0 {
			t.Errorf("allowed-mode shows = %v, want none", got)
		}
		_, body := do(t, allowed, http.MethodGet, "/search?q=zebra", nil)
		for _, id := range []uuid.UUID{plainMovie, scaryMovie, fineShow, fineEp1} {
			if strings.Contains(string(body), id.String()) {
				t.Errorf("allowed-mode search leaked %s: %s", id, body)
			}
		}
		if code, _ := do(t, allowed, http.MethodGet, "/media/stream/"+plainFile.String(), nil); code != http.StatusForbidden {
			t.Errorf("allowed-mode stream of an unlabelled movie = %d, want 403", code)
		}
	})

	t.Run("removing the label lifts the restriction", func(t *testing.T) {
		if code, body := do(t, admin, http.MethodPost, "/labels/apply", map[string]any{
			"item_ids": []string{scaryMovie.String()}, "remove": []string{"horror"},
		}); code != http.StatusOK || !strings.Contains(string(body), `"removed":1`) {
			t.Fatalf("bulk remove: %d %s", code, body)
		}
		if code, _ := do(t, blocked, http.MethodGet, "/items/"+scaryMovie.String(), nil); code != http.StatusOK {
			t.Errorf("detail after removing the label = %d, want 200", code)
		}
	})
}
