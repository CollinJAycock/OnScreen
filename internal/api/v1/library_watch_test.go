package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// fpCapturingLister records the FilterParams the listing handed the data
// layer, so tests can assert the watch filter reached SQL.
type fpCapturingLister struct {
	mockMediaLister
	gotFP      *media.FilterParams
	gotCountFP *media.FilterParams
}

func (m *fpCapturingLister) ListItemsFiltered(ctx context.Context, lib uuid.UUID, t string, l, o int32, fp media.FilterParams) ([]media.Item, error) {
	m.gotFP = &fp
	return m.mockMediaLister.ListItemsFiltered(ctx, lib, t, l, o, fp)
}
func (m *fpCapturingLister) CountItemsFiltered(ctx context.Context, lib uuid.UUID, t string, fp media.FilterParams) (int64, error) {
	m.gotCountFP = &fp
	return m.mockMediaLister.CountItemsFiltered(ctx, lib, t, fp)
}

type fakeLibraryWatchDB struct {
	summaries   []gen.ListItemWatchSummariesRow
	summaryArgs []gen.ListItemWatchSummariesParams
	summaryErr  error

	random     gen.PickRandomLibraryItemRow
	randomErr  error
	randomArgs []gen.PickRandomLibraryItemParams
}

func (f *fakeLibraryWatchDB) ListItemWatchSummaries(_ context.Context, arg gen.ListItemWatchSummariesParams) ([]gen.ListItemWatchSummariesRow, error) {
	f.summaryArgs = append(f.summaryArgs, arg)
	return f.summaries, f.summaryErr
}
func (f *fakeLibraryWatchDB) PickRandomLibraryItem(_ context.Context, arg gen.PickRandomLibraryItemParams) (gen.PickRandomLibraryItemRow, error) {
	f.randomArgs = append(f.randomArgs, arg)
	return f.random, f.randomErr
}

func libWatchRequest(target string, libID uuid.UUID, claims *auth.Claims) *http.Request {
	req := withChiParam(httptest.NewRequest(http.MethodGet, target, nil), "id", libID.String())
	return req.WithContext(middleware.WithClaims(req.Context(), claims))
}

func showLibrary(id uuid.UUID) *mockLibraryService {
	return &mockLibraryService{lib: &library.Library{
		ID: id, Name: "TV", Type: "show", Agent: "tmdb", Lang: "en",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}}
}

func TestParseWatchFilter(t *testing.T) {
	for _, v := range []string{"", "unwatched", "in_progress", "watched"} {
		if got, ok := parseWatchFilter(v); !ok || got != v {
			t.Errorf("%q: got (%q, %v)", v, got, ok)
		}
	}
	for _, v := range []string{"WATCHED", "seen", "1", "in-progress"} {
		if _, ok := parseWatchFilter(v); ok {
			t.Errorf("%q must be rejected", v)
		}
	}
}

func TestLibrary_Items_WatchFilter(t *testing.T) {
	libID := uuid.New()
	user := uuid.New()
	ml := &fpCapturingLister{mockMediaLister: mockMediaLister{count: 0}}
	h := newLibHandler(showLibrary(libID)).WithMedia(ml)

	// Unknown value → 400, nothing listed.
	rec := httptest.NewRecorder()
	h.Items(rec, libWatchRequest("/?watch=seen", libID, &auth.Claims{UserID: user}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bogus watch: status %d, want 400", rec.Code)
	}
	if ml.gotFP != nil {
		t.Fatal("a rejected filter must not reach the data layer")
	}

	// A valid value takes the filtered path with the caller's id, and the
	// count uses the same params — so total matches the filtered page.
	rec = httptest.NewRecorder()
	h.Items(rec, libWatchRequest("/?watch=in_progress", libID, &auth.Claims{UserID: user}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	if ml.gotFP == nil || ml.gotFP.Watch != "in_progress" || ml.gotFP.WatchUserID != user {
		t.Fatalf("list params = %+v", ml.gotFP)
	}
	if ml.gotCountFP == nil || ml.gotCountFP.Watch != "in_progress" || ml.gotCountFP.WatchUserID != user {
		t.Errorf("count params = %+v", ml.gotCountFP)
	}
}

func TestLibrary_Items_WatchFields(t *testing.T) {
	libID := uuid.New()
	movie, show, track := uuid.New(), uuid.New(), uuid.New()
	ml := &fpCapturingLister{mockMediaLister: mockMediaLister{
		items: []media.Item{
			{ID: movie, Title: "M", Type: "movie"},
			{ID: show, Title: "S", Type: "show"},
			{ID: track, Title: "T", Type: "track"},
		},
		count: 3,
	}}
	wdb := &fakeLibraryWatchDB{summaries: []gen.ListItemWatchSummariesRow{
		{ID: movie, Type: "movie", Status: "in_progress", PositionMs: 42_000, Resumable: true},
		{ID: show, Type: "show", Status: "unwatched", LeafCount: 10, WatchedCount: 4},
	}}
	h := newLibHandler(showLibrary(libID)).WithMedia(ml).WithWatchState(wdb)

	rec := httptest.NewRecorder()
	h.Items(rec, libWatchRequest("/", libID, &auth.Claims{UserID: uuid.New(), MaxContentRating: "TV-14"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	// One batched query for the page, leaf + container ids only.
	if len(wdb.summaryArgs) != 1 {
		t.Fatalf("summary queries = %d, want 1", len(wdb.summaryArgs))
	}
	if got := wdb.summaryArgs[0].MediaIds; len(got) != 2 || got[0] != movie || got[1] != show {
		t.Errorf("summary ids = %v, want [movie show]", got)
	}
	if r := wdb.summaryArgs[0].MaxRatingRank; r == nil || *r != 2 {
		t.Errorf("summary rating rank = %v, want 2", r)
	}

	var resp struct {
		Data []map[string]any `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data) != 3 {
		t.Fatalf("rows = %d", len(resp.Data))
	}
	m, s, tr := resp.Data[0], resp.Data[1], resp.Data[2]
	if m["watch_state"] != "in_progress" || m["view_offset_ms"] != float64(42_000) {
		t.Errorf("movie watch fields = %v / %v", m["watch_state"], m["view_offset_ms"])
	}
	if _, ok := m["leaf_count"]; ok {
		t.Error("leaf rows carry no leaf_count")
	}
	if s["leaf_count"] != float64(10) || s["unwatched_count"] != float64(6) {
		t.Errorf("show counts = %v / %v", s["leaf_count"], s["unwatched_count"])
	}
	if _, ok := s["watch_state"]; ok {
		t.Error("show rows carry counts, not watch_state")
	}
	for _, k := range []string{"watch_state", "view_offset_ms", "leaf_count", "unwatched_count"} {
		if _, ok := tr[k]; ok {
			t.Errorf("track must not carry %s", k)
		}
	}
}

func TestLibrary_Items_WatchSummaryErrorDegrades(t *testing.T) {
	libID := uuid.New()
	ml := &fpCapturingLister{mockMediaLister: mockMediaLister{
		items: []media.Item{{ID: uuid.New(), Title: "M", Type: "movie"}}, count: 1,
	}}
	h := newLibHandler(showLibrary(libID)).WithMedia(ml).
		WithWatchState(&fakeLibraryWatchDB{summaryErr: errors.New("down")})
	rec := httptest.NewRecorder()
	h.Items(rec, libWatchRequest("/", libID, &auth.Claims{UserID: uuid.New()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("listing must survive a watch-summary failure, got %d", rec.Code)
	}
}

func TestApplyWatchSummary_NoOffsetWithoutResumePoint(t *testing.T) {
	var dst MediaItemResponse
	applyWatchSummary(&dst, "movie", gen.ListItemWatchSummariesRow{Status: "watched", PositionMs: 0})
	if dst.WatchState == nil || *dst.WatchState != "watched" || dst.ViewOffsetMS != nil {
		t.Errorf("got state=%v offset=%v", dst.WatchState, dst.ViewOffsetMS)
	}
}

func TestLibrary_Random(t *testing.T) {
	libID := uuid.New()
	user := uuid.New()
	picked := uuid.New()
	wdb := &fakeLibraryWatchDB{random: gen.PickRandomLibraryItemRow{ID: picked, Type: "show"}}
	h := newLibHandler(showLibrary(libID)).WithWatchState(wdb)

	rec := httptest.NewRecorder()
	h.Random(rec, libWatchRequest("/?watch=unwatched&genre=Drama&year_min=2000&rating_min=7.5&type=show",
		libID, &auth.Claims{UserID: user, MaxContentRating: "PG"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data RandomItemResponse `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Data.ID != picked.String() || resp.Data.Type != "show" {
		t.Errorf("body = %+v", resp.Data)
	}
	arg := wdb.randomArgs[0]
	if arg.LibraryID != libID || arg.ItemType != "show" || arg.WatchUserID != user {
		t.Errorf("args = %+v", arg)
	}
	if arg.Watch == nil || *arg.Watch != "unwatched" || arg.Genre == nil || *arg.Genre != "Drama" ||
		arg.YearMin == nil || *arg.YearMin != 2000 || !arg.RatingMin.Valid {
		t.Errorf("filters not forwarded: %+v", arg)
	}
	if arg.MaxRatingRank == nil || *arg.MaxRatingRank != 1 {
		t.Errorf("rating ceiling = %v, want 1 (PG)", arg.MaxRatingRank)
	}

	// Nothing matches → 404.
	wdb.randomErr = pgx.ErrNoRows
	rec = httptest.NewRecorder()
	h.Random(rec, libWatchRequest("/", libID, &auth.Claims{UserID: user}))
	if rec.Code != http.StatusNotFound {
		t.Errorf("no match: status %d, want 404", rec.Code)
	}

	// Bad filters → 400.
	for _, q := range []string{"/?watch=nope", "/?type=movie"} {
		rec = httptest.NewRecorder()
		h.Random(rec, libWatchRequest(q, libID, &auth.Claims{UserID: user}))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, rec.Code)
		}
	}

	// Not wired → 501.
	rec = httptest.NewRecorder()
	newLibHandler(showLibrary(libID)).Random(rec, libWatchRequest("/", libID, &auth.Claims{UserID: user}))
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("unwired: status %d, want 501", rec.Code)
	}
}

// denyLibraryService hides every library from the caller.
type denyLibraryService struct{ mockLibraryService }

func (denyLibraryService) CanAccessLibrary(_ context.Context, _, _ uuid.UUID, _ bool) (bool, error) {
	return false, nil
}

func TestLibrary_Random_HiddenLibraryIs404(t *testing.T) {
	libID := uuid.New()
	wdb := &fakeLibraryWatchDB{random: gen.PickRandomLibraryItemRow{ID: uuid.New(), Type: "movie"}}
	h := NewLibraryHandler(&denyLibraryService{}, nil).WithWatchState(wdb)
	rec := httptest.NewRecorder()
	h.Random(rec, libWatchRequest("/", libID, &auth.Claims{UserID: uuid.New()}))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
	if len(wdb.randomArgs) != 0 {
		t.Error("a hidden library must not be sampled")
	}
}
