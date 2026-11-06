package v1

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/itemlabel"
	"github.com/onscreen/onscreen/internal/photoimage"
)

// mockLibraryAccess is a stub LibraryAccessChecker that returns whatever
// the test sets. Used to exercise the access-denial path on the photos
// handler without standing up the full library service.
type mockLibraryAccess struct {
	allow bool
	err   error
}

func (m *mockLibraryAccess) CanAccessLibrary(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ bool) (bool, error) {
	return m.allow, m.err
}

func (m *mockLibraryAccess) AllowedLibraryIDs(_ context.Context, _ uuid.UUID, _ bool) (map[uuid.UUID]struct{}, error) {
	return nil, m.err
}

// ── mocks ────────────────────────────────────────────────────────────────────

type mockPhotoMedia struct {
	item     *media.Item
	files    []media.File
	photos   []media.PhotoListItem
	timeline []media.PhotoTimelineBucket
	mapPts   []media.PhotoMapPoint
	mapTotal int64

	listParams    *media.ListPhotosParams
	timelineLibID uuid.UUID
	timelineRank  *int
	mapParams     *media.ListPhotoMapPointsParams
	mapCountLibID uuid.UUID
	mapCountRank  *int
}

func (m *mockPhotoMedia) GetItem(_ context.Context, id uuid.UUID) (*media.Item, error) {
	if m.item != nil {
		return m.item, nil
	}
	return nil, media.ErrNotFound
}

func (m *mockPhotoMedia) GetFiles(_ context.Context, _ uuid.UUID) ([]media.File, error) {
	return m.files, nil
}

func (m *mockPhotoMedia) ListPhotos(_ context.Context, p media.ListPhotosParams) ([]media.PhotoListItem, error) {
	pp := p
	m.listParams = &pp
	return m.photos, nil
}

func (m *mockPhotoMedia) CountPhotos(_ context.Context, _ media.ListPhotosParams) (int64, error) {
	return int64(len(m.photos)), nil
}

func (m *mockPhotoMedia) ListPhotoTimeline(_ context.Context, libraryID uuid.UUID, maxRatingRank *int, _ itemlabel.Filter) ([]media.PhotoTimelineBucket, error) {
	m.timelineLibID = libraryID
	m.timelineRank = maxRatingRank
	return m.timeline, nil
}

func (m *mockPhotoMedia) ListPhotoMapPoints(_ context.Context, p media.ListPhotoMapPointsParams) ([]media.PhotoMapPoint, error) {
	pp := p
	m.mapParams = &pp
	return m.mapPts, nil
}

func (m *mockPhotoMedia) CountPhotoMapPoints(_ context.Context, libraryID uuid.UUID, maxRatingRank *int, _ itemlabel.Filter) (int64, error) {
	m.mapCountLibID = libraryID
	m.mapCountRank = maxRatingRank
	return m.mapTotal, nil
}

func makeTestJPEG(t *testing.T, dir string, w, h int) string {
	t.Helper()
	path := filepath.Join(dir, "test.jpg")
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 50, G: 100, B: 200, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return path
}

// ── List ─────────────────────────────────────────────────────────────────────

func TestPhotosList_RequiresLibraryID(t *testing.T) {
	h := NewPhotosHandler(&mockPhotoMedia{}, nil, slog.Default())
	req := httptest.NewRequest("GET", "/api/v1/photos", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestPhotosList_ReturnsItems(t *testing.T) {
	libID := uuid.New()
	now := time.Now().UTC()
	taken := now.Add(-24 * time.Hour)
	m := &mockPhotoMedia{
		photos: []media.PhotoListItem{
			{ID: uuid.New(), LibraryID: libID, Title: "IMG_001.jpg", TakenAt: &taken, CreatedAt: now, UpdatedAt: now},
			{ID: uuid.New(), LibraryID: libID, Title: "IMG_002.jpg", CreatedAt: now, UpdatedAt: now},
		},
	}
	h := NewPhotosHandler(m, nil, slog.Default())

	req := httptest.NewRequest("GET", "/api/v1/photos?library_id="+libID.String(), nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []PhotoListItemResponse `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("data len: got %d, want 2", len(resp.Data))
	}
	if resp.Meta.Total != 2 {
		t.Errorf("total: got %d, want 2", resp.Meta.Total)
	}
}

func TestPhotosList_FromToParsedAndPassedThrough(t *testing.T) {
	libID := uuid.New()
	from := "2024-01-01T00:00:00Z"
	to := "2024-12-31T23:59:59Z"
	m := &mockPhotoMedia{}
	h := NewPhotosHandler(m, nil, slog.Default())

	req := httptest.NewRequest("GET",
		"/api/v1/photos?library_id="+libID.String()+"&from="+from+"&to="+to, nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if m.listParams == nil {
		t.Fatal("ListPhotos was not called")
	}
	if m.listParams.From == nil || m.listParams.From.Year() != 2024 {
		t.Errorf("from: got %v, want a 2024 date", m.listParams.From)
	}
	if m.listParams.To == nil || m.listParams.To.Year() != 2024 {
		t.Errorf("to: got %v, want a 2024 date", m.listParams.To)
	}
}

func TestPhotosList_InvalidFromIsBadRequest(t *testing.T) {
	libID := uuid.New()
	h := NewPhotosHandler(&mockPhotoMedia{}, nil, slog.Default())
	req := httptest.NewRequest("GET", "/api/v1/photos?library_id="+libID.String()+"&from=not-a-date", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

// ── Timeline ─────────────────────────────────────────────────────────────────

func TestPhotosTimeline_RequiresLibraryID(t *testing.T) {
	h := NewPhotosHandler(&mockPhotoMedia{}, nil, slog.Default())
	req := httptest.NewRequest("GET", "/api/v1/photos/timeline", nil)
	rec := httptest.NewRecorder()
	h.Timeline(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestPhotosTimeline_ReturnsBuckets(t *testing.T) {
	libID := uuid.New()
	m := &mockPhotoMedia{
		timeline: []media.PhotoTimelineBucket{
			{Year: 2024, Month: 12, Count: 47},
			{Year: 2024, Month: 11, Count: 12},
			{Year: 2023, Month: 8, Count: 102},
		},
	}
	h := NewPhotosHandler(m, nil, slog.Default())

	req := httptest.NewRequest("GET", "/api/v1/photos/timeline?library_id="+libID.String(), nil)
	rec := httptest.NewRecorder()
	h.Timeline(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []PhotoTimelineBucketResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 3 {
		t.Errorf("buckets: got %d, want 3", len(resp.Data))
	}
	if m.timelineLibID != libID {
		t.Errorf("library_id passed through: got %s, want %s", m.timelineLibID, libID)
	}
}

// ── Library access ───────────────────────────────────────────────────────────

func TestPhotosList_AccessDeniedReturns404(t *testing.T) {
	libID := uuid.New()
	h := NewPhotosHandler(&mockPhotoMedia{}, nil, slog.Default()).
		WithLibraryAccess(&mockLibraryAccess{allow: false})

	req := httptest.NewRequest("GET", "/api/v1/photos?library_id="+libID.String(), nil)
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New()})
	rec := httptest.NewRecorder()
	h.List(rec, req.WithContext(ctx))
	if rec.Code != http.StatusNotFound {
		t.Errorf("denied access should 404; got %d", rec.Code)
	}
}

func TestPhotosList_NoClaimsReturns403(t *testing.T) {
	libID := uuid.New()
	h := NewPhotosHandler(&mockPhotoMedia{}, nil, slog.Default()).
		WithLibraryAccess(&mockLibraryAccess{allow: true})

	req := httptest.NewRequest("GET", "/api/v1/photos?library_id="+libID.String(), nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("missing claims should 403; got %d", rec.Code)
	}
}

func TestPhotosList_LimitClampedTo500(t *testing.T) {
	libID := uuid.New()
	m := &mockPhotoMedia{}
	h := NewPhotosHandler(m, nil, slog.Default())

	req := httptest.NewRequest("GET", "/api/v1/photos?library_id="+libID.String()+"&limit=99999&offset=42", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if m.listParams.Limit != 500 {
		t.Errorf("limit clamp: got %d, want 500", m.listParams.Limit)
	}
	if m.listParams.Offset != 42 {
		t.Errorf("offset passthrough: got %d, want 42", m.listParams.Offset)
	}
}

// ── Map ──────────────────────────────────────────────────────────────────────

func TestPhotosMap_RequiresLibraryID(t *testing.T) {
	h := NewPhotosHandler(&mockPhotoMedia{}, nil, slog.Default())
	req := httptest.NewRequest("GET", "/api/v1/photos/map", nil)
	rec := httptest.NewRecorder()
	h.Map(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestPhotosMap_InvalidLibraryIDIsBadRequest(t *testing.T) {
	h := NewPhotosHandler(&mockPhotoMedia{}, nil, slog.Default())
	req := httptest.NewRequest("GET", "/api/v1/photos/map?library_id=not-a-uuid", nil)
	rec := httptest.NewRecorder()
	h.Map(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestPhotosMap_ReturnsPoints(t *testing.T) {
	libID := uuid.New()
	now := time.Now().UTC()
	taken := now.Add(-12 * time.Hour)
	m := &mockPhotoMedia{
		mapPts: []media.PhotoMapPoint{
			{ID: uuid.New(), LibraryID: libID, Title: "IMG_001.jpg", Lat: 47.6062, Lon: -122.3321, TakenAt: &taken, CreatedAt: now},
			{ID: uuid.New(), LibraryID: libID, Title: "IMG_002.jpg", Lat: 48.8566, Lon: 2.3522, CreatedAt: now},
		},
		mapTotal: 23107,
	}
	h := NewPhotosHandler(m, nil, slog.Default())

	req := httptest.NewRequest("GET", "/api/v1/photos/map?library_id="+libID.String(), nil)
	rec := httptest.NewRecorder()
	h.Map(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []PhotoMapPointResponse `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Errorf("data len: got %d, want 2", len(resp.Data))
	}
	if resp.Data[0].Lat != 47.6062 || resp.Data[0].Lon != -122.3321 {
		t.Errorf("lat/lon round-trip: got %v,%v", resp.Data[0].Lat, resp.Data[0].Lon)
	}
	if resp.Meta.Total != 23107 {
		t.Errorf("total: got %d, want 23107 (library-wide count, not bbox-scoped)", resp.Meta.Total)
	}
	if m.mapCountLibID != libID {
		t.Errorf("count called with wrong libID: %s", m.mapCountLibID)
	}
}

func TestPhotosMap_BboxParsedAndPassedThrough(t *testing.T) {
	libID := uuid.New()
	m := &mockPhotoMedia{}
	h := NewPhotosHandler(m, nil, slog.Default())

	req := httptest.NewRequest("GET",
		"/api/v1/photos/map?library_id="+libID.String()+
			"&min_lat=40&max_lat=50&min_lon=-130&max_lon=-110", nil)
	rec := httptest.NewRecorder()
	h.Map(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if m.mapParams == nil {
		t.Fatal("ListPhotoMapPoints was not called")
	}
	if m.mapParams.MinLat == nil || *m.mapParams.MinLat != 40 {
		t.Errorf("min_lat: got %v, want 40", m.mapParams.MinLat)
	}
	if m.mapParams.MaxLat == nil || *m.mapParams.MaxLat != 50 {
		t.Errorf("max_lat: got %v, want 50", m.mapParams.MaxLat)
	}
	if m.mapParams.MinLon == nil || *m.mapParams.MinLon != -130 {
		t.Errorf("min_lon: got %v, want -130", m.mapParams.MinLon)
	}
	if m.mapParams.MaxLon == nil || *m.mapParams.MaxLon != -110 {
		t.Errorf("max_lon: got %v, want -110", m.mapParams.MaxLon)
	}
}

func TestPhotosMap_PartialBboxLeavesOthersNil(t *testing.T) {
	libID := uuid.New()
	m := &mockPhotoMedia{}
	h := NewPhotosHandler(m, nil, slog.Default())

	// Only min_lat — the rest should arrive at the service as nil so the
	// SQL query uses "no bound" on those edges.
	req := httptest.NewRequest("GET",
		"/api/v1/photos/map?library_id="+libID.String()+"&min_lat=10", nil)
	rec := httptest.NewRecorder()
	h.Map(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if m.mapParams.MinLat == nil || *m.mapParams.MinLat != 10 {
		t.Errorf("min_lat: got %v, want 10", m.mapParams.MinLat)
	}
	if m.mapParams.MaxLat != nil || m.mapParams.MinLon != nil || m.mapParams.MaxLon != nil {
		t.Errorf("unset bbox edges should be nil; got max_lat=%v min_lon=%v max_lon=%v",
			m.mapParams.MaxLat, m.mapParams.MinLon, m.mapParams.MaxLon)
	}
}

func TestPhotosMap_OutOfRangeLatIsBadRequest(t *testing.T) {
	libID := uuid.New()
	h := NewPhotosHandler(&mockPhotoMedia{}, nil, slog.Default())
	req := httptest.NewRequest("GET",
		"/api/v1/photos/map?library_id="+libID.String()+"&min_lat=200", nil)
	rec := httptest.NewRecorder()
	h.Map(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestPhotosMap_OutOfRangeLonIsBadRequest(t *testing.T) {
	libID := uuid.New()
	h := NewPhotosHandler(&mockPhotoMedia{}, nil, slog.Default())
	req := httptest.NewRequest("GET",
		"/api/v1/photos/map?library_id="+libID.String()+"&max_lon=999", nil)
	rec := httptest.NewRecorder()
	h.Map(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
}

func TestPhotosMap_LimitDefaultedAndClamped(t *testing.T) {
	libID := uuid.New()
	m := &mockPhotoMedia{}
	h := NewPhotosHandler(m, nil, slog.Default())

	// Way beyond max — should clamp back to default.
	req := httptest.NewRequest("GET",
		"/api/v1/photos/map?library_id="+libID.String()+"&limit=99999999", nil)
	rec := httptest.NewRecorder()
	h.Map(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if m.mapParams.Limit != defaultMapPointLimit {
		t.Errorf("limit clamp: got %d, want %d", m.mapParams.Limit, defaultMapPointLimit)
	}
}

func TestPhotosMap_AccessDeniedReturns404(t *testing.T) {
	libID := uuid.New()
	h := NewPhotosHandler(&mockPhotoMedia{}, nil, slog.Default()).
		WithLibraryAccess(&mockLibraryAccess{allow: false})

	req := httptest.NewRequest("GET", "/api/v1/photos/map?library_id="+libID.String(), nil)
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New()})
	rec := httptest.NewRecorder()
	h.Map(rec, req.WithContext(ctx))
	if rec.Code != http.StatusNotFound {
		t.Errorf("denied access should 404; got %d", rec.Code)
	}
}

// ── Search ───────────────────────────────────────────────────────────────────

func TestPhotosImage_NoImageServerIs503(t *testing.T) {
	id := uuid.New()
	h := NewPhotosHandler(&mockPhotoMedia{
		item: &media.Item{ID: id, Type: "photo"},
	}, nil, slog.Default())
	req := httptest.NewRequest("GET", "/api/v1/items/"+id.String()+"/image", nil)
	req = withChiParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.Image(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", rec.Code)
	}
}

// The image endpoint serves the item's actual bytes, so it must enforce the
// caller's content-rating ceiling alongside the library ACL — a restricted
// profile used to pull any over-ceiling photo/audiobook cover by direct URL.
// 404, not 403, matching the handler's existing don't-reveal-existence shape.
func TestPhotosImage_ContentRatingCeiling(t *testing.T) {
	id := uuid.New()
	dir := t.TempDir()
	srcPath := makeTestJPEG(t, dir, 40, 40)
	srv := photoimage.New(filepath.Join(dir, "cache"))
	rating := "R"
	h := NewPhotosHandler(&mockPhotoMedia{
		item:  &media.Item{ID: id, Type: "photo", ContentRating: &rating},
		files: []media.File{{ID: uuid.New(), FilePath: srcPath}},
	}, srv, slog.Default())

	req := httptest.NewRequest("GET", "/api/v1/items/"+id.String()+"/image", nil)
	req = withChiParam(req, "id", id.String())
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{
		UserID: uuid.New(), Username: "kid", MaxContentRating: "PG",
	}))
	rec := httptest.NewRecorder()
	h.Image(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("R-rated image for a PG-capped profile: got %d, want 404", rec.Code)
	}

	// An unrestricted caller still gets the bytes.
	req2 := httptest.NewRequest("GET", "/api/v1/items/"+id.String()+"/image", nil)
	req2 = withChiParam(req2, "id", id.String())
	req2 = req2.WithContext(middleware.WithClaims(req2.Context(), &auth.Claims{
		UserID: uuid.New(), Username: "adult",
	}))
	rec2 := httptest.NewRecorder()
	h.Image(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("unrestricted profile blocked: got %d, want 200", rec2.Code)
	}
}

func TestPhotosImage_NonPhotoItemIs404(t *testing.T) {
	id := uuid.New()
	dir := t.TempDir()
	srv := photoimage.New(filepath.Join(dir, "cache"))
	h := NewPhotosHandler(&mockPhotoMedia{
		item: &media.Item{ID: id, Type: "movie"}, // not a photo
	}, srv, slog.Default())

	req := httptest.NewRequest("GET", "/api/v1/items/"+id.String()+"/image", nil)
	req = withChiParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.Image(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("non-photo type should 404; got %d", rec.Code)
	}
}

func TestPhotosImage_ServesResizedJPEG(t *testing.T) {
	dir := t.TempDir()
	srcPath := makeTestJPEG(t, dir, 800, 600)
	srv := photoimage.New(filepath.Join(dir, "cache"))

	id := uuid.New()
	libID := uuid.New()
	h := NewPhotosHandler(&mockPhotoMedia{
		item:  &media.Item{ID: id, LibraryID: libID, Type: "photo"},
		files: []media.File{{ID: uuid.New(), FilePath: srcPath}},
	}, srv, slog.Default())

	req := httptest.NewRequest("GET", "/api/v1/items/"+id.String()+"/image?w=200&h=200&fit=contain", nil)
	req = withChiParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.Image(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type: got %q, want image/jpeg", ct)
	}
	img, err := jpeg.Decode(rec.Body)
	if err != nil {
		t.Fatalf("response not valid JPEG: %v", err)
	}
	b := img.Bounds()
	// 800×600 contained inside 200×200 → 200×150.
	if b.Dx() != 200 || b.Dy() != 150 {
		t.Errorf("got %dx%d, want 200x150", b.Dx(), b.Dy())
	}
}

func TestPhotosImage_DimensionClampedToMax(t *testing.T) {
	dir := t.TempDir()
	srcPath := makeTestJPEG(t, dir, 100, 100)
	srv := photoimage.New(filepath.Join(dir, "cache"))

	id := uuid.New()
	h := NewPhotosHandler(&mockPhotoMedia{
		item:  &media.Item{ID: id, Type: "photo"},
		files: []media.File{{ID: uuid.New(), FilePath: srcPath}},
	}, srv, slog.Default())

	// Request a 99999×99999 derivative — should clamp to maxImageDimension
	// and not blow up the server.
	req := httptest.NewRequest("GET",
		"/api/v1/items/"+id.String()+"/image?w=99999&h=99999", nil)
	req = withChiParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.Image(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
}

// Every photo listing must carry the caller's content-rating ceiling into the
// query. /photos/{id}/image already refused an over-ceiling photo's bytes, but
// the listings handed a restricted profile its title, EXIF and GPS anyway.
func TestPhotos_ListingsApplyRatingCeiling(t *testing.T) {
	libID := uuid.New()
	m := &mockPhotoMedia{}
	h := NewPhotosHandler(m, nil, slog.Default())
	call := func(fn http.HandlerFunc, path, maxRating string) {
		req := httptest.NewRequest(http.MethodGet, path+"?library_id="+libID.String(), nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), MaxContentRating: maxRating}))
		rec := httptest.NewRecorder()
		fn(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
	}
	rank := func(p *int) int {
		if p == nil {
			return -1
		}
		return *p
	}

	call(h.List, "/api/v1/photos", "PG")
	call(h.Timeline, "/api/v1/photos/timeline", "PG")
	call(h.Map, "/api/v1/photos/map", "PG")
	want := *contentrating.MaxRatingRank("PG")
	for name, got := range map[string]*int{
		"list":      m.listParams.MaxRatingRank,
		"timeline":  m.timelineRank,
		"map":       m.mapParams.MaxRatingRank,
		"map count": m.mapCountRank,
	} {
		if rank(got) != want {
			t.Errorf("%s: rating rank = %d, want %d", name, rank(got), want)
		}
	}

	// An unrestricted caller passes no ceiling.
	call(h.List, "/api/v1/photos", "")
	if m.listParams.MaxRatingRank != nil {
		t.Errorf("unrestricted caller got ceiling %d", *m.listParams.MaxRatingRank)
	}
}
