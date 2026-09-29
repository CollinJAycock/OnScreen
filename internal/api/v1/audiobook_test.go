package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// fakeAudiobookStore is an in-memory AudiobookStore.
type fakeAudiobookStore struct {
	items map[uuid.UUID]gen.GetMediaItemRow

	rates      map[uuid.UUID]float32 // by book
	latest     *float32
	setRate    *gen.SetAudiobookRateParams
	bookmarks  []gen.ListAudiobookBookmarksRow
	listed     *gen.ListAudiobookBookmarksParams
	created    *gen.CreateAudiobookBookmarkParams
	createErr  error
	updated    *gen.UpdateAudiobookBookmarkNoteParams
	deleted    *gen.DeleteAudiobookBookmarkParams
	rowsChange int64
}

func (f *fakeAudiobookStore) GetMediaItem(_ context.Context, id uuid.UUID) (gen.GetMediaItemRow, error) {
	it, ok := f.items[id]
	if !ok {
		return gen.GetMediaItemRow{}, pgx.ErrNoRows
	}
	return it, nil
}

func (f *fakeAudiobookStore) GetAudiobookRate(_ context.Context, arg gen.GetAudiobookRateParams) (float32, error) {
	if r, ok := f.rates[arg.BookID]; ok {
		return r, nil
	}
	return 0, pgx.ErrNoRows
}

func (f *fakeAudiobookStore) GetLatestAudiobookRate(context.Context, uuid.UUID) (float32, error) {
	if f.latest == nil {
		return 0, pgx.ErrNoRows
	}
	return *f.latest, nil
}

func (f *fakeAudiobookStore) SetAudiobookRate(_ context.Context, arg gen.SetAudiobookRateParams) error {
	f.setRate = &arg
	return nil
}

func (f *fakeAudiobookStore) ListAudiobookBookmarks(_ context.Context, arg gen.ListAudiobookBookmarksParams) ([]gen.ListAudiobookBookmarksRow, error) {
	f.listed = &arg
	return f.bookmarks, nil
}

func (f *fakeAudiobookStore) CreateAudiobookBookmark(_ context.Context, arg gen.CreateAudiobookBookmarkParams) (gen.CreateAudiobookBookmarkRow, error) {
	f.created = &arg
	if f.createErr != nil {
		return gen.CreateAudiobookBookmarkRow{}, f.createErr
	}
	return gen.CreateAudiobookBookmarkRow{
		ID: uuid.New(), CreatedAt: pgtype.Timestamptz{Time: time.Unix(1_700_000_000, 0), Valid: true},
	}, nil
}

func (f *fakeAudiobookStore) UpdateAudiobookBookmarkNote(_ context.Context, arg gen.UpdateAudiobookBookmarkNoteParams) (int64, error) {
	f.updated = &arg
	return f.rowsChange, nil
}

func (f *fakeAudiobookStore) DeleteAudiobookBookmark(_ context.Context, arg gen.DeleteAudiobookBookmarkParams) (int64, error) {
	f.deleted = &arg
	return f.rowsChange, nil
}

// audiobookFixture is a multi-file book with one chapter, a single-file
// book, and a movie.
type audiobookFixture struct {
	store                        *fakeAudiobookStore
	book, chapter, single, movie uuid.UUID
	user                         uuid.UUID
}

func newAudiobookFixture() *audiobookFixture {
	f := &audiobookFixture{
		book: uuid.New(), chapter: uuid.New(), single: uuid.New(), movie: uuid.New(), user: uuid.New(),
	}
	lib := uuid.New()
	seven := int32(7)
	pg := "PG"
	f.store = &fakeAudiobookStore{
		rates: map[uuid.UUID]float32{},
		items: map[uuid.UUID]gen.GetMediaItemRow{
			f.book:    {ID: f.book, LibraryID: lib, Type: "audiobook", Title: "The Book", ContentRating: &pg},
			f.chapter: {ID: f.chapter, LibraryID: lib, Type: "audiobook_chapter", Title: "Chapter 7", Index: &seven, ContentRating: &pg, ParentID: pgtype.UUID{Bytes: f.book, Valid: true}},
			f.single:  {ID: f.single, LibraryID: lib, Type: "audiobook", Title: "One File", ContentRating: &pg},
			f.movie:   {ID: f.movie, LibraryID: lib, Type: "movie", Title: "A Film", ContentRating: &pg},
		},
	}
	return f
}

func (f *audiobookFixture) handler() *AudiobookHandler {
	return NewAudiobookHandler(f.store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// call runs one handler method as f.user (maxRating "" = unrestricted).
func (f *audiobookFixture) call(fn http.HandlerFunc, method, id, body, maxRating string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, "/", nil)
	} else {
		r = httptest.NewRequest(method, "/", strings.NewReader(body))
	}
	r = withChiParam(r, "id", id)
	r = r.WithContext(middleware.WithClaims(r.Context(), &auth.Claims{UserID: f.user, MaxContentRating: maxRating}))
	rec := httptest.NewRecorder()
	fn(rec, r)
	return rec
}

func audiobookData(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		t.Fatalf("decode data: %v (%s)", err, env.Data)
	}
}

func TestPlaybackRate_SourcesInOrder(t *testing.T) {
	f := newAudiobookFixture()
	h := f.handler()
	var got PlaybackRateResponse

	rec := f.call(h.GetPlaybackRate, http.MethodGet, f.book.String(), "", "")
	audiobookData(t, rec, &got)
	if got != (PlaybackRateResponse{Rate: 1, Source: "default"}) {
		t.Errorf("nothing saved: %+v", got)
	}

	recent := float32(1.5)
	f.store.latest = &recent
	audiobookData(t, f.call(h.GetPlaybackRate, http.MethodGet, f.book.String(), "", ""), &got)
	if got != (PlaybackRateResponse{Rate: 1.5, Source: "recent"}) {
		t.Errorf("another book's speed: %+v", got)
	}

	// Asked through a chapter, the book's own speed wins; float32 noise is
	// rounded off.
	f.store.rates[f.book] = 1.2500001
	audiobookData(t, f.call(h.GetPlaybackRate, http.MethodGet, f.chapter.String(), "", ""), &got)
	if got != (PlaybackRateResponse{Rate: 1.25, Source: "book"}) {
		t.Errorf("the book's speed via a chapter: %+v", got)
	}
}

func TestPlaybackRate_SaveValidatesAndStoresOnTheBook(t *testing.T) {
	f := newAudiobookFixture()
	h := f.handler()

	for _, body := range []string{`{"rate":0.49}`, `{"rate":3.01}`, `{}`, `{not json`} {
		if rec := f.call(h.SetPlaybackRate, http.MethodPut, f.book.String(), body, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", body, rec.Code)
		}
	}
	if f.store.setRate != nil {
		t.Fatal("nothing may be saved from a bad request")
	}

	rec := f.call(h.SetPlaybackRate, http.MethodPut, f.chapter.String(), `{"rate":1.753}`, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if s := f.store.setRate; s.BookID != f.book || s.UserID != f.user || s.PlaybackRate != 1.75 {
		t.Errorf("saved %+v; want the book, the caller, 1.75", s)
	}
}

func TestAudiobook_OnlyVisibleAudiobooks(t *testing.T) {
	f := newAudiobookFixture()
	h := f.handler()

	if rec := f.call(h.GetPlaybackRate, http.MethodGet, f.movie.String(), "", ""); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("a movie: got %d, want 422", rec.Code)
	}
	if rec := f.call(h.ListBookmarks, http.MethodGet, uuid.NewString(), "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown item: got %d, want 404", rec.Code)
	}
	// Above a G-capped profile's ceiling: hidden, so 404 rather than 422.
	if rec := f.call(h.ListBookmarks, http.MethodGet, f.chapter.String(), "", "G"); rec.Code != http.StatusNotFound {
		t.Errorf("rated above the ceiling: got %d, want 404", rec.Code)
	}
	// A chapter whose parent isn't an audiobook isn't one either.
	orphan := uuid.New()
	f.store.items[orphan] = gen.GetMediaItemRow{ID: orphan, Type: "audiobook_chapter", ParentID: pgtype.UUID{Bytes: f.movie, Valid: true}}
	if rec := f.call(h.ListBookmarks, http.MethodGet, orphan.String(), "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("chapter of a non-book: got %d, want 404", rec.Code)
	}
	// Outside the caller's libraries.
	denied := NewAudiobookHandler(f.store, denyAllAccess{}, slog.Default())
	if rec := f.call(denied.ListBookmarks, http.MethodGet, f.book.String(), "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("library not granted: got %d, want 404", rec.Code)
	}
	// Unauthenticated.
	rec := httptest.NewRecorder()
	h.ListBookmarks(rec, withChiParam(httptest.NewRequest(http.MethodGet, "/", nil), "id", f.book.String()))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no claims: got %d, want 401", rec.Code)
	}
}

func TestBookmarks_CreateInAChapter(t *testing.T) {
	f := newAudiobookFixture()
	h := f.handler()

	rec := f.call(h.CreateBookmark, http.MethodPost, f.chapter.String(), `{"position_ms":61000,"note":"  the twist  "}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	c := f.store.created
	if c.BookID != f.book || c.ItemID != f.chapter || c.UserID != f.user || c.PositionMs != 61000 ||
		c.Note != "the twist" || c.MaxPerBook != maxBookmarksPerBook {
		t.Errorf("stored %+v", c)
	}
	var b BookmarkJSON
	audiobookData(t, rec, &b)
	if b.ItemID != f.chapter.String() || b.ItemTitle != "Chapter 7" || b.ItemIndex == nil || *b.ItemIndex != 7 || b.Note != "the twist" {
		t.Errorf("response %+v", b)
	}

	// In a single-file book the item is the book and has no chapter index.
	rec = f.call(h.CreateBookmark, http.MethodPost, f.single.String(), `{"position_ms":0}`, "")
	b = BookmarkJSON{}
	audiobookData(t, rec, &b)
	if f.store.created.BookID != f.single || f.store.created.ItemID != f.single || b.ItemIndex != nil {
		t.Errorf("single-file: stored %+v, response %+v", f.store.created, b)
	}
}

func TestBookmarks_CreateRejects(t *testing.T) {
	f := newAudiobookFixture()
	h := f.handler()
	for _, body := range []string{
		`{"position_ms":-1}`,
		`{"note":"no position"}`,
		`{"position_ms":1,"note":"` + strings.Repeat("é", 501) + `"}`,
	} {
		f.store.created = nil
		if rec := f.call(h.CreateBookmark, http.MethodPost, f.book.String(), body, ""); rec.Code != http.StatusBadRequest || f.store.created != nil {
			t.Errorf("%.40s: got %d (stored %v), want 400", body, rec.Code, f.store.created)
		}
	}
	// 500 characters (not bytes) is fine.
	if rec := f.call(h.CreateBookmark, http.MethodPost, f.book.String(), `{"position_ms":1,"note":"`+strings.Repeat("é", 500)+`"}`, ""); rec.Code != http.StatusCreated {
		t.Errorf("500-character note: got %d", rec.Code)
	}

	f.store.createErr = pgx.ErrNoRows // the cap
	rec := f.call(h.CreateBookmark, http.MethodPost, f.book.String(), `{"position_ms":1}`, "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "BOOKMARK_LIMIT") {
		t.Errorf("at the cap: %d %s", rec.Code, rec.Body.String())
	}
	f.store.createErr = errors.New("db down")
	if rec := f.call(h.CreateBookmark, http.MethodPost, f.book.String(), `{"position_ms":1}`, ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("store failure: got %d", rec.Code)
	}
}

func TestBookmarks_ListForTheBook(t *testing.T) {
	f := newAudiobookFixture()
	h := f.handler()
	f.store.bookmarks = []gen.ListAudiobookBookmarksRow{{
		ID: uuid.New(), ItemID: f.chapter, ItemTitle: "Chapter 7", PositionMs: 5000, Note: "n",
		CreatedAt: pgtype.Timestamptz{Time: time.Unix(1_700_000_000, 0), Valid: true},
	}}

	rec := f.call(h.ListBookmarks, http.MethodGet, f.chapter.String(), "", "")
	var got []BookmarkJSON
	audiobookData(t, rec, &got)
	if f.store.listed.BookID != f.book || f.store.listed.UserID != f.user {
		t.Errorf("listed %+v; want the whole book, the caller's", f.store.listed)
	}
	if len(got) != 1 || got[0].ItemID != f.chapter.String() || got[0].PositionMS != 5000 || got[0].Note != "n" {
		t.Errorf("got %+v", got)
	}

	f.store.bookmarks = nil
	rec = f.call(h.ListBookmarks, http.MethodGet, f.book.String(), "", "")
	if strings.TrimSpace(rec.Body.String()) == "" || !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("none: want an empty array, got %s", rec.Body.String())
	}
}

func TestBookmarks_EditAndDeleteOnlyYourOwn(t *testing.T) {
	f := newAudiobookFixture()
	h := f.handler()
	id := uuid.NewString()

	f.store.rowsChange = 0 // not the caller's, or gone
	if rec := f.call(h.UpdateBookmark, http.MethodPatch, id, `{"note":"x"}`, ""); rec.Code != http.StatusNotFound {
		t.Errorf("edit someone else's: got %d, want 404", rec.Code)
	}
	if rec := f.call(h.DeleteBookmark, http.MethodDelete, id, "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("delete someone else's: got %d, want 404", rec.Code)
	}

	f.store.rowsChange = 1
	if rec := f.call(h.UpdateBookmark, http.MethodPatch, id, `{"note":"  new note "}`, ""); rec.Code != http.StatusNoContent {
		t.Errorf("edit: got %d", rec.Code)
	}
	if u := f.store.updated; u.UserID != f.user || u.Note != "new note" || u.ID.String() != id {
		t.Errorf("updated %+v", u)
	}
	if rec := f.call(h.UpdateBookmark, http.MethodPatch, id, `{}`, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("edit without a note: got %d, want 400", rec.Code)
	}
	if rec := f.call(h.DeleteBookmark, http.MethodDelete, id, "", ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete: got %d", rec.Code)
	}
	if d := f.store.deleted; d.UserID != f.user || d.ID.String() != id {
		t.Errorf("deleted %+v", d)
	}
	if rec := f.call(h.DeleteBookmark, http.MethodDelete, "not-a-uuid", "", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: got %d, want 400", rec.Code)
	}
}
