package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

type fakeHubWatchDB struct {
	nextUp     []gen.ListNextUpRow
	nextUpErr  error
	nextUpArgs []gen.ListNextUpParams
	plan       []gen.ListPlanToWatchHubRow
	planErr    error
	planArgs   []gen.ListPlanToWatchHubParams
}

func (f *fakeHubWatchDB) ListNextUp(_ context.Context, arg gen.ListNextUpParams) ([]gen.ListNextUpRow, error) {
	f.nextUpArgs = append(f.nextUpArgs, arg)
	return f.nextUp, f.nextUpErr
}
func (f *fakeHubWatchDB) ListPlanToWatchHub(_ context.Context, arg gen.ListPlanToWatchHubParams) ([]gen.ListPlanToWatchHubRow, error) {
	f.planArgs = append(f.planArgs, arg)
	return f.plan, f.planErr
}

func hubGet(t *testing.T, h *HubHandler, claims *auth.Claims) (HubResponse, map[string]json.RawMessage) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/hub", nil)
	req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var typed struct {
		Data HubResponse `json:"data"`
	}
	var raw struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &typed)
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	return typed.Data, raw.Data
}

// Older builds never wire the watch rows; the keys must still be present as
// empty arrays so clients can render unconditionally.
func TestHub_WatchRows_EmptyWhenUnwired(t *testing.T) {
	_, raw := hubGet(t, newHubHandler(&mockHubDB{}), &auth.Claims{UserID: uuid.New()})
	for _, k := range []string{"next_up", "plan_to_watch"} {
		if string(raw[k]) != "[]" {
			t.Errorf("%s = %s, want []", k, raw[k])
		}
	}
}

func TestHub_NextUp_TilesAndACL(t *testing.T) {
	allowedLib, hiddenLib := uuid.New(), uuid.New()
	s2, e5 := int32(2), int32(5)
	still := "/art/ep-still.jpg"
	showPoster, showFanart := "/art/show-poster.jpg", "/art/show-fanart.jpg"
	showYear := int32(2019)
	visible := gen.ListNextUpRow{
		ID: uuid.New(), LibraryID: allowedLib, Title: "The Next One",
		ThumbPath: &still, SeasonNumber: &s2, EpisodeNumber: &e5,
		ShowID: uuid.New(), ShowTitle: "Some Show", ShowYear: &showYear,
		ShowPosterPath: &showPoster, ShowFanartPath: &showFanart,
	}
	hidden := gen.ListNextUpRow{ID: uuid.New(), LibraryID: hiddenLib, Title: "Hidden", ShowID: uuid.New()}
	wdb := &fakeHubWatchDB{nextUp: []gen.ListNextUpRow{hidden, visible}}
	h := newHubHandler(&mockHubDB{}).
		WithLibraryAccess(&stubLibraryAccessChecker{allowed: map[uuid.UUID]struct{}{allowedLib: {}}}).
		WithWatchRows(wdb)

	user := uuid.New()
	data, _ := hubGet(t, h, &auth.Claims{UserID: user, MaxContentRating: "TV-14"})
	if len(data.NextUp) != 1 {
		t.Fatalf("next_up = %d tiles, want 1 (hidden library filtered)", len(data.NextUp))
	}
	tile := data.NextUp[0]
	if tile.ID != visible.ID.String() || tile.Type != "episode" || tile.Title != "The Next One" {
		t.Errorf("identity = %+v", tile)
	}
	if tile.ShowID == nil || *tile.ShowID != visible.ShowID.String() ||
		tile.ShowTitle == nil || *tile.ShowTitle != "Some Show" {
		t.Errorf("show fields = %v / %v", tile.ShowID, tile.ShowTitle)
	}
	if tile.SeasonNumber == nil || *tile.SeasonNumber != 2 || tile.EpisodeNumber == nil || *tile.EpisodeNumber != 5 {
		t.Errorf("numbers = %v / %v", tile.SeasonNumber, tile.EpisodeNumber)
	}
	if tile.PosterPath == nil || *tile.PosterPath != showPoster || tile.ThumbPath == nil || *tile.ThumbPath != still {
		t.Errorf("art = poster %v thumb %v", tile.PosterPath, tile.ThumbPath)
	}
	arg := wdb.nextUpArgs[0]
	if arg.UserID != user || arg.MaxRatingRank == nil || *arg.MaxRatingRank != 2 {
		t.Errorf("next up args = %+v", arg)
	}
}

func TestHub_PlanToWatch(t *testing.T) {
	lib := uuid.New()
	row := gen.ListPlanToWatchHubRow{ID: uuid.New(), LibraryID: lib, Type: "movie", Title: "Queued"}
	wdb := &fakeHubWatchDB{plan: []gen.ListPlanToWatchHubRow{row}}
	h := newHubHandler(&mockHubDB{}).WithWatchRows(wdb)
	data, _ := hubGet(t, h, &auth.Claims{UserID: uuid.New()})
	if len(data.PlanToWatch) != 1 || data.PlanToWatch[0].ID != row.ID.String() || data.PlanToWatch[0].Type != "movie" {
		t.Errorf("plan_to_watch = %+v", data.PlanToWatch)
	}
	if wdb.planArgs[0].MaxRatingRank != nil {
		t.Errorf("unrestricted caller must pass a nil ceiling, got %v", *wdb.planArgs[0].MaxRatingRank)
	}
}

func TestHub_WatchRows_ErrorsYieldEmptyRows(t *testing.T) {
	wdb := &fakeHubWatchDB{nextUpErr: errors.New("x"), planErr: errors.New("y")}
	h := NewHubHandler(&mockHubDB{}, slog.Default()).WithWatchRows(wdb)
	data, raw := hubGet(t, h, &auth.Claims{UserID: uuid.New()})
	if len(data.NextUp) != 0 || len(data.PlanToWatch) != 0 || string(raw["next_up"]) != "[]" {
		t.Errorf("rows = %v / %v", data.NextUp, data.PlanToWatch)
	}
}

func TestWithDefaultHubRows(t *testing.T) {
	keys := func(l []hubRowPref) []string {
		out := make([]string, len(l))
		for i, p := range l {
			out[i] = p.Key
			if !p.Enabled && (p.Key == "next_up" || p.Key == "plan_to_watch") {
				out[i] += "(off)"
			}
		}
		return out
	}
	eq := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	cases := []struct {
		name string
		in   []hubRowPref
		want []string
	}{
		{"never customized stays empty", nil, []string{}},
		{"inserted after the last continue row", []hubRowPref{
			{Key: "trending", Enabled: true},
			{Key: "continue_tv", Enabled: true},
			{Key: "continue_movies", Enabled: false},
			{Key: "library:x", Enabled: true},
		}, []string{"trending", "continue_tv", "continue_movies", "next_up", "plan_to_watch", "library:x"}},
		{"no continue rows → top", []hubRowPref{
			{Key: "library:x", Enabled: true},
		}, []string{"next_up", "plan_to_watch", "library:x"}},
		{"user's choice is kept", []hubRowPref{
			{Key: "plan_to_watch", Enabled: false},
			{Key: "continue_tv", Enabled: true},
		}, []string{"plan_to_watch(off)", "continue_tv", "next_up"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := keys(withDefaultHubRows(tc.in))
			if !eq(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
	// Inserted rows are enabled.
	for _, p := range withDefaultHubRows([]hubRowPref{{Key: "continue_tv", Enabled: true}}) {
		if !p.Enabled {
			t.Errorf("%s inserted disabled", p.Key)
		}
	}
}
