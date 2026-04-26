package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/lyrics"
)

// ── mocks ────────────────────────────────────────────────────────────────────

type fakeLyricsStore struct {
	plain, synced string
	getErr        error

	setCalls  int
	setPlain  string
	setSynced string
}

func (f *fakeLyricsStore) GetLyrics(_ context.Context, _ uuid.UUID) (string, string, error) {
	return f.plain, f.synced, f.getErr
}

func (f *fakeLyricsStore) SetLyrics(_ context.Context, _ uuid.UUID, plain, synced string) error {
	f.setCalls++
	f.setPlain = plain
	f.setSynced = synced
	return nil
}

type fakeLyricsItems struct {
	item       *media.Item
	itemErr    error
	artist     string
	album      string
	metaErr    error
	files      []media.File
	filesErr   error
}

func (f *fakeLyricsItems) GetItem(_ context.Context, _ uuid.UUID) (*media.Item, error) {
	return f.item, f.itemErr
}
func (f *fakeLyricsItems) GetFiles(_ context.Context, _ uuid.UUID) ([]media.File, error) {
	return f.files, f.filesErr
}
func (f *fakeLyricsItems) GetTrackMetadata(_ context.Context, _ uuid.UUID) (string, string, error) {
	return f.artist, f.album, f.metaErr
}

type fakeLyricsFetcher struct {
	res    lyrics.Result
	err    error
	called bool
	got    lyrics.LookupParams
}

func (f *fakeLyricsFetcher) Lookup(_ context.Context, p lyrics.LookupParams) (lyrics.Result, error) {
	f.called = true
	f.got = p
	return f.res, f.err
}

type fakeLyricsAccess struct {
	allow bool
	err   error
}

func (f *fakeLyricsAccess) CanAccessLibrary(_ context.Context, _, _ uuid.UUID, _ bool) (bool, error) {
	return f.allow, f.err
}
func (f *fakeLyricsAccess) AllowedLibraryIDs(_ context.Context, _ uuid.UUID, _ bool) (map[uuid.UUID]struct{}, error) {
	return nil, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func lyricsTrackItem() *media.Item {
	libraryID := uuid.New()
	dur := int64(180_000)
	return &media.Item{
		ID:         uuid.New(),
		LibraryID:  libraryID,
		Type:       "track",
		Title:      "Time",
		DurationMS: &dur,
	}
}

func lyricsRequest(t *testing.T, idParam string, claims *auth.Claims) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/items/"+idParam+"/lyrics", nil)
	if claims != nil {
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", idParam)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	return req
}

type lyricsEnvelope struct {
	Data LyricsResponse `json:"data"`
}

func decodeLyrics(t *testing.T, body []byte) LyricsResponse {
	t.Helper()
	var e lyricsEnvelope
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	return e.Data
}

// ── happy path: cached lyrics returned without LRCLIB hit ────────────────────

func TestLyrics_Get_ReturnsCachedSyncedLyrics(t *testing.T) {
	item := lyricsTrackItem()
	store := &fakeLyricsStore{plain: "I am here", synced: "[00:00.00]I am here"}
	items := &fakeLyricsItems{item: item}
	fetcher := &fakeLyricsFetcher{}
	h := NewLyricsHandler(store, items, fetcher, slog.Default())

	rec := httptest.NewRecorder()
	h.Get(rec, lyricsRequest(t, item.ID.String(), &auth.Claims{UserID: uuid.New()}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rec.Code, rec.Body.String())
	}
	got := decodeLyrics(t, rec.Body.Bytes())
	if got.Synced != "[00:00.00]I am here" || got.Plain != "I am here" {
		t.Errorf("got %+v, want both fields populated from cache", got)
	}
	if fetcher.called {
		t.Error("fetcher should NOT be called when cache has lyrics")
	}
}

// ── LRCLIB miss caches empty so we don't keep refetching ─────────────────────

func TestLyrics_Get_LRCLIBMissCachesEmpty(t *testing.T) {
	item := lyricsTrackItem()
	store := &fakeLyricsStore{} // cache empty
	items := &fakeLyricsItems{item: item, artist: "Pink Floyd", album: "The Dark Side of the Moon"}
	fetcher := &fakeLyricsFetcher{err: lyrics.ErrNotFound}
	h := NewLyricsHandler(store, items, fetcher, slog.Default())

	rec := httptest.NewRecorder()
	h.Get(rec, lyricsRequest(t, item.ID.String(), &auth.Claims{UserID: uuid.New()}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	got := decodeLyrics(t, rec.Body.Bytes())
	if got.Plain != "" || got.Synced != "" {
		t.Errorf("got %+v, want empty strings on LRCLIB miss", got)
	}
	if store.setCalls != 1 || store.setPlain != "" || store.setSynced != "" {
		t.Errorf("store.SetLyrics: calls=%d plain=%q synced=%q — want 1 call with empty strings (cache the miss)",
			store.setCalls, store.setPlain, store.setSynced)
	}
}

// ── LRCLIB hit persists and returns the result ───────────────────────────────

func TestLyrics_Get_LRCLIBHitPersistsAndReturns(t *testing.T) {
	item := lyricsTrackItem()
	store := &fakeLyricsStore{} // cache empty
	items := &fakeLyricsItems{item: item, artist: "Pink Floyd", album: "Wish You Were Here"}
	fetcher := &fakeLyricsFetcher{
		res: lyrics.Result{Plain: "fresh plain", Synced: "[00:00.00]fresh synced"},
	}
	h := NewLyricsHandler(store, items, fetcher, slog.Default())

	rec := httptest.NewRecorder()
	h.Get(rec, lyricsRequest(t, item.ID.String(), &auth.Claims{UserID: uuid.New()}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rec.Code, rec.Body.String())
	}
	got := decodeLyrics(t, rec.Body.Bytes())
	if got.Plain != "fresh plain" || got.Synced != "[00:00.00]fresh synced" {
		t.Errorf("got %+v, want fetched lyrics", got)
	}
	if !fetcher.called {
		t.Error("fetcher should be called on cache miss")
	}
	if fetcher.got.Artist != "Pink Floyd" || fetcher.got.Track != "Time" {
		t.Errorf("LookupParams: got %+v, want artist=Pink Floyd, track=Time", fetcher.got)
	}
	// Duration should be milliseconds → seconds (180_000ms → 180s).
	if fetcher.got.DurationS != 180 {
		t.Errorf("LookupParams.DurationS: got %d, want 180 (ms→s conversion)", fetcher.got.DurationS)
	}
	if store.setCalls != 1 || store.setPlain != "fresh plain" {
		t.Errorf("store.SetLyrics: calls=%d, want 1 with fetched plain", store.setCalls)
	}
}

// ── nil fetcher: cache miss returns empty without fetching ───────────────────

func TestLyrics_Get_NilFetcherReturnsEmpty(t *testing.T) {
	item := lyricsTrackItem()
	store := &fakeLyricsStore{} // miss
	items := &fakeLyricsItems{item: item}
	h := NewLyricsHandler(store, items, nil, slog.Default())

	rec := httptest.NewRecorder()
	h.Get(rec, lyricsRequest(t, item.ID.String(), &auth.Claims{UserID: uuid.New()}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	got := decodeLyrics(t, rec.Body.Bytes())
	if got.Plain != "" || got.Synced != "" {
		t.Errorf("got %+v, want empty when no fetcher configured", got)
	}
}

// ── non-track items 404 — lyrics on movies/shows is meaningless noise ────────

func TestLyrics_Get_NonTrackReturns404(t *testing.T) {
	item := lyricsTrackItem()
	item.Type = "movie"
	h := NewLyricsHandler(&fakeLyricsStore{}, &fakeLyricsItems{item: item}, nil, slog.Default())

	rec := httptest.NewRecorder()
	h.Get(rec, lyricsRequest(t, item.ID.String(), &auth.Claims{UserID: uuid.New()}))

	if rec.Code != http.StatusNotFound {
		t.Errorf("non-track item should 404, got %d", rec.Code)
	}
}

// ── library ACL is enforced ──────────────────────────────────────────────────

func TestLyrics_Get_LibraryACLDeniedReturns404(t *testing.T) {
	item := lyricsTrackItem()
	h := NewLyricsHandler(&fakeLyricsStore{}, &fakeLyricsItems{item: item}, nil, slog.Default()).
		WithLibraryAccess(&fakeLyricsAccess{allow: false})

	rec := httptest.NewRecorder()
	h.Get(rec, lyricsRequest(t, item.ID.String(), &auth.Claims{UserID: uuid.New()}))

	// Fail-closed: 404 (not 403) so ACL denial is indistinguishable from
	// missing track ID — same posture the items handler uses.
	if rec.Code != http.StatusNotFound {
		t.Errorf("ACL deny should 404, got %d", rec.Code)
	}
}

// ── invalid UUID is 400 ──────────────────────────────────────────────────────

func TestLyrics_Get_BadUUIDReturns400(t *testing.T) {
	h := NewLyricsHandler(&fakeLyricsStore{}, &fakeLyricsItems{}, nil, slog.Default())
	rec := httptest.NewRecorder()
	h.Get(rec, lyricsRequest(t, "not-a-uuid", &auth.Claims{UserID: uuid.New()}))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad UUID should 400, got %d", rec.Code)
	}
}

// ── missing item is 404 ──────────────────────────────────────────────────────

func TestLyrics_Get_ItemNotFoundReturns404(t *testing.T) {
	h := NewLyricsHandler(
		&fakeLyricsStore{},
		&fakeLyricsItems{itemErr: media.ErrNotFound},
		nil, slog.Default())
	rec := httptest.NewRecorder()
	h.Get(rec, lyricsRequest(t, uuid.New().String(), &auth.Claims{UserID: uuid.New()}))

	if rec.Code != http.StatusNotFound {
		t.Errorf("missing item should 404, got %d", rec.Code)
	}
}

// ── metadata lookup failure degrades gracefully ──────────────────────────────

func TestLyrics_Get_MetadataLookupFailureReturnsEmpty(t *testing.T) {
	item := lyricsTrackItem()
	store := &fakeLyricsStore{} // cache empty
	items := &fakeLyricsItems{item: item, metaErr: errors.New("db hiccup")}
	fetcher := &fakeLyricsFetcher{}
	h := NewLyricsHandler(store, items, fetcher, slog.Default())

	rec := httptest.NewRecorder()
	h.Get(rec, lyricsRequest(t, item.ID.String(), &auth.Claims{UserID: uuid.New()}))

	if rec.Code != http.StatusOK {
		t.Errorf("metadata lookup error should degrade to empty 200, got %d", rec.Code)
	}
	if fetcher.called {
		t.Error("fetcher should NOT be called when artist/album resolution failed")
	}
}
