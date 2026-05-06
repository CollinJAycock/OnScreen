package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// ── mock library service ─────────────────────────────────────────────────────

type mockLibraryService struct {
	libs      []library.Library
	lib       *library.Library
	listErr   error
	getErr    error
	createErr error
	updateErr error
	deleteErr error
	scanErr   error
	created   *library.Library
}

func (m *mockLibraryService) List(_ context.Context) ([]library.Library, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.libs, nil
}
func (m *mockLibraryService) Get(_ context.Context, _ uuid.UUID) (*library.Library, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.lib, nil
}
func (m *mockLibraryService) Create(_ context.Context, p library.CreateLibraryParams) (*library.Library, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	if m.created != nil {
		return m.created, nil
	}
	lib := &library.Library{
		ID:        uuid.New(),
		Name:      p.Name,
		Type:      p.Type,
		Paths:     p.Paths,
		Agent:     p.Agent,
		Lang:      p.Lang,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return lib, nil
}
func (m *mockLibraryService) Update(_ context.Context, p library.UpdateLibraryParams) (*library.Library, error) {
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	lib := &library.Library{
		ID:        p.ID,
		Name:      p.Name,
		Paths:     p.Paths,
		Agent:     p.Agent,
		Lang:      p.Lang,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return lib, nil
}
func (m *mockLibraryService) Delete(_ context.Context, _ uuid.UUID) error {
	return m.deleteErr
}
func (m *mockLibraryService) EnqueueScan(_ context.Context, _ uuid.UUID) error {
	return m.scanErr
}
func (m *mockLibraryService) ListForUser(ctx context.Context, _ uuid.UUID, _ bool) ([]library.Library, error) {
	return m.List(ctx)
}
func (m *mockLibraryService) CanAccessLibrary(_ context.Context, _, _ uuid.UUID, _ bool) (bool, error) {
	return true, nil
}

// ── mock media item lister ───────────────────────────────────────────────────

type mockMediaLister struct {
	items    []media.Item
	listErr  error
	count    int64
	countErr error
	// gotType captures the most recent item type passed to ListItems /
	// ListItemsFiltered so tests can assert the handler's `?type=`
	// override actually flowed through to the data layer.
	gotType string
}

func (m *mockMediaLister) ListItems(_ context.Context, _ uuid.UUID, t string, _, _ int32) ([]media.Item, error) {
	m.gotType = t
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.items, nil
}
func (m *mockMediaLister) CountItems(_ context.Context, _ uuid.UUID, _ string) (int64, error) {
	if m.countErr != nil {
		return 0, m.countErr
	}
	return m.count, nil
}
func (m *mockMediaLister) ListItemsFiltered(_ context.Context, _ uuid.UUID, t string, _, _ int32, _ media.FilterParams) ([]media.Item, error) {
	m.gotType = t
	return m.items, m.listErr
}
func (m *mockMediaLister) CountItemsFiltered(_ context.Context, _ uuid.UUID, _ string, _ media.FilterParams) (int64, error) {
	return m.count, m.countErr
}
func (m *mockMediaLister) ListDistinctGenres(_ context.Context, _ uuid.UUID) ([]string, error) {
	return nil, nil
}
func (m *mockMediaLister) ListGenresWithCounts(_ context.Context, _ uuid.UUID, _ string) ([]media.GenreCount, error) {
	return nil, nil
}
func (m *mockMediaLister) ListYearsWithCounts(_ context.Context, _ uuid.UUID, _ string) ([]media.YearCount, error) {
	return nil, nil
}
func (m *mockMediaLister) ListEventCollectionsForLibrary(_ context.Context, _ uuid.UUID) ([]media.EventCollection, error) {
	return nil, nil
}

func newLibHandler(svc *mockLibraryService) *LibraryHandler {
	return NewLibraryHandler(svc, slog.Default())
}

// ── List ─────────────────────────────────────────────────────────────────────

func TestLibrary_List_Success(t *testing.T) {
	svc := &mockLibraryService{
		libs: []library.Library{
			{ID: uuid.New(), Name: "Movies", Type: "movie", Paths: []string{"/movies"}, Agent: "tmdb", Lang: "en", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		},
	}
	h := newLibHandler(svc)

	rec := httptest.NewRecorder()
	req := withClaims(httptest.NewRequest("GET", "/api/v1/libraries", nil))
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestLibrary_List_Error(t *testing.T) {
	svc := &mockLibraryService{listErr: errors.New("db down")}
	h := newLibHandler(svc)

	rec := httptest.NewRecorder()
	req := withClaims(httptest.NewRequest("GET", "/", nil))
	h.List(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// ── Get ──────────────────────────────────────────────────────────────────────

func TestLibrary_Get_Success(t *testing.T) {
	id := uuid.New()
	svc := &mockLibraryService{
		lib: &library.Library{ID: id, Name: "Movies", Type: "movie", Agent: "tmdb", Lang: "en", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	h := newLibHandler(svc)

	rec := httptest.NewRecorder()
	req := withClaims(withChiParam(httptest.NewRequest("GET", "/", nil), "id", id.String()))
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestLibrary_Get_NotFound(t *testing.T) {
	svc := &mockLibraryService{getErr: library.ErrNotFound}
	h := newLibHandler(svc)

	rec := httptest.NewRecorder()
	req := withClaims(withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String()))
	h.Get(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestLibrary_Get_InvalidID(t *testing.T) {
	h := newLibHandler(&mockLibraryService{})

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", "bad")
	h.Get(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// ── Create ───────────────────────────────────────────────────────────────────

func TestLibrary_Create_Success(t *testing.T) {
	svc := &mockLibraryService{}
	h := newLibHandler(svc)

	body := `{"name":"Movies","type":"movie","scan_paths":["/media/movies"]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	h.Create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusCreated)
	}
}

func TestLibrary_Create_InvalidBody(t *testing.T) {
	h := newLibHandler(&mockLibraryService{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader("not json"))
	h.Create(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestLibrary_Create_ValidationError(t *testing.T) {
	svc := &mockLibraryService{
		createErr: &library.ValidationError{Field: "name", Message: "required"},
	}
	h := newLibHandler(svc)

	body := `{"name":"","type":"movie","scan_paths":["/media"]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	h.Create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestLibrary_Create_Defaults(t *testing.T) {
	// When agent, language, and scan_interval are not specified, defaults should apply.
	svc := &mockLibraryService{}
	h := newLibHandler(svc)

	body := `{"name":"Shows","type":"show","scan_paths":["/shows"]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	h.Create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusCreated)
	}
}

// ── Update ───────────────────────────────────────────────────────────────────

func TestLibrary_Update_Success(t *testing.T) {
	id := uuid.New()
	// PATCH now reads the existing row first so partial bodies can fall
	// back to current values — seed the mock with one to mimic real
	// service behaviour.
	svc := &mockLibraryService{lib: &library.Library{ID: id, Name: "Movies", Paths: []string{"/old/path"}}}
	h := newLibHandler(svc)

	body := `{"name":"Updated Movies","scan_paths":["/new/path"]}`
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", id.String())
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestLibrary_Update_NotFound(t *testing.T) {
	// Update now does GET-then-PATCH — the not-found surface fires on
	// the initial GET, not the UpdateLibrary call.
	svc := &mockLibraryService{getErr: library.ErrNotFound}
	h := newLibHandler(svc)

	body := `{"name":"X"}`
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", uuid.New().String())
	h.Update(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestLibrary_Update_ScanIntervalMinutes(t *testing.T) {
	id := uuid.New()
	svc := &mockLibraryService{lib: &library.Library{ID: id, Name: "Movies", Paths: []string{"/m"}}}
	h := newLibHandler(svc)

	body := `{"scan_interval_minutes":60}`
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", id.String())
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

// Partial-update semantics: sending only is_private must not require a
// name. Previously the handler treated empty Name as set-to-empty, which
// the SQL layer rejected with a 500. Locks the v2.1 fix that drove the
// e2e policy.spec.ts is_private flow.
func TestLibrary_Update_OnlyIsPrivate(t *testing.T) {
	id := uuid.New()
	svc := &mockLibraryService{lib: &library.Library{ID: id, Name: "Existing", Paths: []string{"/a"}}}
	h := newLibHandler(svc)

	body := `{"is_private":true}`
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", id.String())
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// Unknown fields must 400 — without DisallowUnknownFields a typo like
// `is-private` (hyphen) silently decodes into nothing and the caller's
// intent is lost without any signal.
func TestLibrary_Update_UnknownFieldRejected(t *testing.T) {
	id := uuid.New()
	svc := &mockLibraryService{lib: &library.Library{ID: id, Name: "X"}}
	h := newLibHandler(svc)

	body := `{"is-private":true}` // hyphen instead of underscore
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", id.String())
	h.Update(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// ── Delete ───────────────────────────────────────────────────────────────────

func TestLibrary_Delete_Success(t *testing.T) {
	svc := &mockLibraryService{}
	h := newLibHandler(svc)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("DELETE", "/", nil), "id", uuid.New().String())
	h.Delete(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestLibrary_Delete_NotFound(t *testing.T) {
	svc := &mockLibraryService{deleteErr: library.ErrNotFound}
	h := newLibHandler(svc)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("DELETE", "/", nil), "id", uuid.New().String())
	h.Delete(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// ── Refresh (scan) ───────────────────────────────────────────────────────────

func TestLibrary_Refresh_Success(t *testing.T) {
	svc := &mockLibraryService{}
	h := newLibHandler(svc)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("POST", "/", nil), "id", uuid.New().String())
	h.Refresh(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestLibrary_Refresh_NotFound(t *testing.T) {
	svc := &mockLibraryService{scanErr: library.ErrNotFound}
	h := newLibHandler(svc)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("POST", "/", nil), "id", uuid.New().String())
	h.Refresh(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// ── Items ────────────────────────────────────────────────────────────────────

func TestLibrary_Items_NoMediaService(t *testing.T) {
	svc := &mockLibraryService{}
	h := newLibHandler(svc) // no WithMedia

	rec := httptest.NewRecorder()
	req := withClaims(withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String()))
	h.Items(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotImplemented)
	}
}

func TestLibrary_Items_Success(t *testing.T) {
	libID := uuid.New()
	svc := &mockLibraryService{
		lib: &library.Library{ID: libID, Name: "Movies", Type: "movie", Agent: "tmdb", Lang: "en", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	ml := &mockMediaLister{
		items: []media.Item{{ID: uuid.New(), Title: "Test", Type: "movie"}},
		count: 1,
	}
	h := newLibHandler(svc)
	h.WithMedia(ml)

	rec := httptest.NewRecorder()
	req := withClaims(withChiParam(httptest.NewRequest("GET", "/?limit=10&offset=0", nil), "id", libID.String()))
	h.Items(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data []json.RawMessage `json:"data"`
		Meta map[string]any    `json:"meta"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data) != 1 {
		t.Errorf("data length: got %d, want 1", len(resp.Data))
	}
}

// TestLibrary_Items_TypeOverride covers the v2.1 `?type=` query param
// that lets callers list a non-root child type within a library — e.g.
// asking for music_video items from a music library, where the default
// rootItemType resolves to "artist". Validates the allow-list rejects
// unrelated types and accepts in-hierarchy ones.
func TestLibrary_Items_TypeOverride(t *testing.T) {
	t.Run("music library accepts ?type=music_video", func(t *testing.T) {
		libID := uuid.New()
		svc := &mockLibraryService{
			lib: &library.Library{ID: libID, Name: "Tunes", Type: "music", Agent: "musicbrainz", Lang: "en", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		}
		ml := &mockMediaLister{
			items: []media.Item{{ID: uuid.New(), Title: "MV1", Type: "music_video"}},
			count: 1,
		}
		h := newLibHandler(svc).WithMedia(ml)

		rec := httptest.NewRecorder()
		req := withClaims(withChiParam(httptest.NewRequest("GET", "/?type=music_video", nil), "id", libID.String()))
		h.Items(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
		}
		if ml.gotType != "music_video" {
			t.Errorf("data layer received type=%q, want music_video", ml.gotType)
		}
	})

	t.Run("music library rejects ?type=movie", func(t *testing.T) {
		libID := uuid.New()
		svc := &mockLibraryService{
			lib: &library.Library{ID: libID, Name: "Tunes", Type: "music", Agent: "musicbrainz", Lang: "en", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		}
		h := newLibHandler(svc).WithMedia(&mockMediaLister{})

		rec := httptest.NewRecorder()
		req := withClaims(withChiParam(httptest.NewRequest("GET", "/?type=movie", nil), "id", libID.String()))
		h.Items(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for cross-hierarchy type, got %d", rec.Code)
		}
	})

	t.Run("default falls back to root item type when ?type= absent", func(t *testing.T) {
		libID := uuid.New()
		svc := &mockLibraryService{
			lib: &library.Library{ID: libID, Name: "Tunes", Type: "music", Agent: "musicbrainz", Lang: "en", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		}
		ml := &mockMediaLister{
			items: []media.Item{{ID: uuid.New(), Title: "Some Artist", Type: "artist"}},
			count: 1,
		}
		h := newLibHandler(svc).WithMedia(ml)

		rec := httptest.NewRecorder()
		req := withClaims(withChiParam(httptest.NewRequest("GET", "/", nil), "id", libID.String()))
		h.Items(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status: got %d", rec.Code)
		}
		if ml.gotType != "artist" {
			t.Errorf("default music root type: got %q, want artist", ml.gotType)
		}
	})
}

// TestValidItemTypeForLibrary covers the allow-list directly — the
// handler's BadRequest path is wired to it, but the allow-list itself
// has more shapes than one HTTP-level test would surface. Worth its
// own table-driven check.
func TestValidItemTypeForLibrary(t *testing.T) {
	cases := []struct {
		libraryType, itemType string
		want                  bool
	}{
		// music
		{"music", "artist", true},
		{"music", "album", true},
		{"music", "track", true},
		{"music", "music_video", true},
		{"music", "movie", false},
		{"music", "podcast_episode", false},
		// show
		{"show", "show", true},
		{"show", "season", true},
		{"show", "episode", true},
		{"show", "music_video", false},
		// movie
		{"movie", "movie", true},
		{"movie", "show", false},
		// podcast
		{"podcast", "podcast", true},
		{"podcast", "podcast_episode", true},
		{"podcast", "audiobook", false},
		// audiobook — full hierarchy is book_author → book_series →
		// audiobook → audiobook_chapter; all four valid through the
		// type check so detail / children fetches resolve.
		{"audiobook", "book_author", true},
		{"audiobook", "book_series", true},
		{"audiobook", "audiobook", true},
		{"audiobook", "audiobook_chapter", true},
		{"audiobook", "track", false},
		// photo
		{"photo", "photo", true},
		{"photo", "movie", false},
		// home_video — single-type, no children
		{"home_video", "home_video", true},
		{"home_video", "movie", false},
		{"home_video", "music_video", false},
		// book — single-type
		{"book", "book", true},
		{"book", "movie", false},
		{"book", "audiobook", false},
		// unknown library
		{"weird", "anything", false},
	}
	for _, c := range cases {
		got := validItemTypeForLibrary(c.libraryType, c.itemType)
		if got != c.want {
			t.Errorf("validItemTypeForLibrary(%q, %q) = %v, want %v",
				c.libraryType, c.itemType, got, c.want)
		}
	}
}

func TestLibrary_Items_LibraryNotFound(t *testing.T) {
	svc := &mockLibraryService{getErr: library.ErrNotFound}
	ml := &mockMediaLister{}
	h := newLibHandler(svc)
	h.WithMedia(ml)

	rec := httptest.NewRecorder()
	req := withClaims(withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String()))
	h.Items(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// ── toLibraryResponse ────────────────────────────────────────────────────────

func TestToLibraryResponse_NilPaths(t *testing.T) {
	lib := &library.Library{
		ID:        uuid.New(),
		Name:      "Test",
		Paths:     nil,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	resp := toLibraryResponse(lib, true)
	if resp.ScanPaths == nil {
		t.Error("ScanPaths should not be nil for admin (should be empty slice)")
	}
	// Non-admin sees no scan_paths at all (omitempty drops the field).
	respUser := toLibraryResponse(lib, false)
	if respUser.ScanPaths != nil {
		t.Errorf("non-admin ScanPaths should be nil; got %v", respUser.ScanPaths)
	}
}

func TestToLibraryResponse_WithScanInterval(t *testing.T) {
	dur := 2 * time.Hour
	lib := &library.Library{
		ID:           uuid.New(),
		Name:         "Test",
		ScanInterval: &dur,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	resp := toLibraryResponse(lib, true)
	if resp.ScanIntervalMinutes == nil {
		t.Fatal("ScanIntervalMinutes should not be nil")
	}
	if *resp.ScanIntervalMinutes != 120 {
		t.Errorf("ScanIntervalMinutes: got %d, want 120", *resp.ScanIntervalMinutes)
	}
}
