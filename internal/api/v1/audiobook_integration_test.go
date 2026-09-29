//go:build integration

package v1

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// The audiobook handler against real Postgres: speeds per book and the
// "recent" fallback, bookmarks in listening order, the per-book cap, owner
// checks, and what cascades when a chapter goes away.
func TestAudiobook_Integration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := gen.New(pool)
	h := NewAudiobookHandler(q, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	user := func() uuid.UUID {
		u, err := q.CreateUser(ctx, gen.CreateUserParams{Username: "u-" + uuid.NewString()[:8]})
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		return u.ID
	}
	alice, bob := user(), user()
	lib, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
		Name: "Books-" + uuid.NewString()[:8], Type: "audiobook", ScanPaths: []string{"/tmp/books"},
		Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	item := func(typ, title string, parent uuid.UUID, index int32) uuid.UUID {
		p := gen.CreateMediaItemParams{LibraryID: lib.ID, Type: typ, Title: title, SortTitle: title, Genres: []string{}, Tags: []string{}}
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
		return row.ID
	}
	book := item("audiobook", "Long Book", uuid.Nil, 0)
	ch1 := item("audiobook_chapter", "Chapter 1", book, 1)
	ch2 := item("audiobook_chapter", "Chapter 2", book, 2)
	single := item("audiobook", "Short Book", uuid.Nil, 3) // a series index, not a chapter

	do := func(as uuid.UUID, fn http.HandlerFunc, method, id, body string) *httptest.ResponseRecorder {
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, "/", nil)
		} else {
			r = httptest.NewRequest(method, "/", strings.NewReader(body))
		}
		r = withChiParam(r, "id", id)
		r = r.WithContext(middleware.WithClaims(r.Context(), &auth.Claims{UserID: as}))
		rec := httptest.NewRecorder()
		fn(rec, r)
		return rec
	}
	data := func(rec *httptest.ResponseRecorder, out any) {
		t.Helper()
		var env struct{ Data json.RawMessage }
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || json.Unmarshal(env.Data, out) != nil {
			t.Fatalf("decode %d %s", rec.Code, rec.Body.String())
		}
	}
	rate := func(as uuid.UUID, id uuid.UUID) PlaybackRateResponse {
		var out PlaybackRateResponse
		data(do(as, h.GetPlaybackRate, http.MethodGet, id.String(), ""), &out)
		return out
	}

	// ── speed ─────────────────────────────────────────────────────────
	if got := rate(alice, book); got != (PlaybackRateResponse{Rate: 1, Source: "default"}) {
		t.Errorf("fresh: %+v", got)
	}
	if rec := do(alice, h.SetPlaybackRate, http.MethodPut, ch2.String(), `{"rate":1.75}`); rec.Code != http.StatusNoContent {
		t.Fatalf("set via chapter: %d %s", rec.Code, rec.Body.String())
	}
	if got := rate(alice, book); got != (PlaybackRateResponse{Rate: 1.75, Source: "book"}) {
		t.Errorf("after set: %+v", got)
	}
	if got := rate(alice, single); got != (PlaybackRateResponse{Rate: 1.75, Source: "recent"}) {
		t.Errorf("another book starts at the recent speed: %+v", got)
	}
	do(alice, h.SetPlaybackRate, http.MethodPut, single.String(), `{"rate":1.25}`)
	if got := rate(alice, single); got.Rate != 1.25 || got.Source != "book" {
		t.Errorf("single: %+v", got)
	}
	if got := rate(bob, book); got.Source != "default" {
		t.Errorf("speeds are per user: %+v", got)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_audiobook_rate (user_id, book_id, playback_rate) VALUES ($1, $2, 3.5)`, bob, book); err == nil {
		t.Error("the table must refuse a speed above 3.0")
	}

	// ── bookmarks ─────────────────────────────────────────────────────
	add := func(as, id uuid.UUID, pos int64, note string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]any{"position_ms": pos, "note": note})
		return do(as, h.CreateBookmark, http.MethodPost, id.String(), string(b))
	}
	// Added out of order; listed by chapter, then position.
	for _, a := range []struct {
		id  uuid.UUID
		pos int64
	}{{ch2, 5000}, {ch1, 9000}, {ch1, 1000}} {
		if rec := add(alice, a.id, a.pos, ""); rec.Code != http.StatusCreated {
			t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
		}
	}
	add(bob, ch1, 42, "bob's")
	var list []BookmarkJSON
	data(do(alice, h.ListBookmarks, http.MethodGet, book.String(), ""), &list)
	var order []string
	for _, b := range list {
		order = append(order, b.ItemTitle+"@"+time.Duration(b.PositionMS*int64(time.Millisecond)).String())
	}
	if strings.Join(order, ",") != "Chapter 1@1s,Chapter 1@9s,Chapter 2@5s" {
		t.Errorf("listening order (alice's only): %v", order)
	}

	// Bob can neither edit nor delete Alice's bookmark.
	first := list[0].ID
	if rec := do(bob, h.UpdateBookmark, http.MethodPatch, first, `{"note":"mine now"}`); rec.Code != http.StatusNotFound {
		t.Errorf("bob edits alice's: %d", rec.Code)
	}
	if rec := do(bob, h.DeleteBookmark, http.MethodDelete, first, ""); rec.Code != http.StatusNotFound {
		t.Errorf("bob deletes alice's: %d", rec.Code)
	}
	if rec := do(alice, h.UpdateBookmark, http.MethodPatch, first, `{"note":"good bit"}`); rec.Code != http.StatusNoContent {
		t.Errorf("alice edits: %d", rec.Code)
	}
	data(do(alice, h.ListBookmarks, http.MethodGet, ch2.String(), ""), &list)
	if list[0].Note != "good bit" || len(list) != 3 {
		t.Errorf("after edit, via a chapter: %+v", list)
	}

	// A chapter that's soft-deleted drops out of the list; hard-deleting
	// it takes its bookmarks with it.
	if _, err := pool.Exec(ctx, `UPDATE media_items SET deleted_at = now() WHERE id = $1`, ch2); err != nil {
		t.Fatal(err)
	}
	data(do(alice, h.ListBookmarks, http.MethodGet, book.String(), ""), &list)
	if len(list) != 2 {
		t.Errorf("soft-deleted chapter: %d bookmarks listed, want 2", len(list))
	}
	if _, err := pool.Exec(ctx, `DELETE FROM media_items WHERE id = $1`, ch2); err != nil {
		t.Fatal(err)
	}
	var left int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM audiobook_bookmarks WHERE user_id = $1`, alice).Scan(&left)
	if left != 2 {
		t.Errorf("after the chapter is deleted: %d bookmarks, want 2", left)
	}

	// The per-book cap is enforced inside the INSERT.
	row, err := q.CreateAudiobookBookmark(ctx, gen.CreateAudiobookBookmarkParams{
		UserID: alice, BookID: book, ItemID: ch1, PositionMs: 1, MaxPerBook: 2,
	})
	if err == nil {
		t.Errorf("a third bookmark past a cap of 2 was stored: %v", row.ID)
	}

	if rec := do(alice, h.DeleteBookmark, http.MethodDelete, first, ""); rec.Code != http.StatusNoContent {
		t.Errorf("alice deletes: %d", rec.Code)
	}
	data(do(alice, h.ListBookmarks, http.MethodGet, book.String(), ""), &list)
	if len(list) != 1 {
		t.Errorf("after delete: %d", len(list))
	}
}
