package userstats

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/itemlabel"
)

func TestParseRange(t *testing.T) {
	loc, _ := time.LoadLocation("America/Detroit")
	now := time.Date(2026, 10, 14, 15, 0, 0, 0, loc)
	cases := []struct {
		raw     string
		key     string
		since   time.Time
		wantErr bool
	}{
		{"", "30d", now.AddDate(0, 0, -30), false},
		{"30d", "30d", now.AddDate(0, 0, -30), false},
		{"90d", "90d", now.AddDate(0, 0, -90), false},
		{"365d", "365d", now.AddDate(0, 0, -365), false},
		{"all", "all", epoch, false},
		{"year:2025", "year:2025", time.Date(2025, 1, 1, 0, 0, 0, 0, loc), false},
		{"year:2026", "year:2026", time.Date(2026, 1, 1, 0, 0, 0, 0, loc), false},
		{"year:2027", "", time.Time{}, true}, // the future
		{"year:1999", "", time.Time{}, true},
		{"year:26", "", time.Time{}, true},
		{"year:abcd", "", time.Time{}, true},
		{"7d", "", time.Time{}, true},
	}
	for _, tc := range cases {
		r, err := ParseRange(tc.raw, now, loc)
		if tc.wantErr {
			if !errors.Is(err, ErrBadRange) {
				t.Errorf("%q: err = %v, want ErrBadRange", tc.raw, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tc.raw, err)
			continue
		}
		if r.Key != tc.key || !r.Since.Equal(tc.since) {
			t.Errorf("%q: got key %q since %v, want %q since %v", tc.raw, r.Key, r.Since, tc.key, tc.since)
		}
		if tc.key != "year:2025" && !r.Until.After(now) {
			t.Errorf("%q: until %v must cover now", tc.raw, r.Until)
		}
	}
	r, _ := ParseRange("year:2025", now, loc)
	if want := time.Date(2026, 1, 1, 0, 0, 0, 0, loc); !r.Until.Equal(want) {
		t.Errorf("year until = %v, want %v", r.Until, want)
	}
}

func day(s string) gen.GetUserStatsDaysRow {
	t, _ := time.Parse(time.DateOnly, s)
	return gen.GetUserStatsDaysRow{Day: pgtype.Date{Time: t, Valid: true}, Plays: 1, WatchedMs: 1000}
}

func TestLongestStreak(t *testing.T) {
	if got := longestStreak(nil); got.Days != 0 {
		t.Errorf("empty = %+v", got)
	}
	rows := []gen.GetUserStatsDaysRow{
		day("2026-01-05"), day("2026-01-01"), day("2026-01-02"), day("2026-01-03"),
		day("2026-01-07"), day("2026-01-08"), day("2026-01-09"),
		// Month boundary inside a run.
		day("2026-01-30"), day("2026-01-31"), day("2026-02-01"), day("2026-02-02"),
	}
	got := longestStreak(rows)
	if got.Days != 4 || got.Start != "2026-01-30" || got.End != "2026-02-02" {
		t.Errorf("streak = %+v, want 4 days 2026-01-30..2026-02-02", got)
	}
	// Ties keep the earliest run.
	got = longestStreak([]gen.GetUserStatsDaysRow{day("2026-03-01"), day("2026-03-02"), day("2026-03-10"), day("2026-03-11")})
	if got.Days != 2 || got.Start != "2026-03-01" {
		t.Errorf("tie = %+v, want the earlier run", got)
	}
}

func TestBusiestDay(t *testing.T) {
	if busiestDay(nil) != nil {
		t.Error("no rows must give no busiest day")
	}
	a, b, c := day("2026-04-01"), day("2026-04-02"), day("2026-04-03")
	a.WatchedMs, b.WatchedMs, c.WatchedMs = 5000, 9000, 9000
	b.Plays, c.Plays = 2, 3
	got := busiestDay([]gen.GetUserStatsDaysRow{a, b, c})
	if got == nil || got.Date != "2026-04-03" || got.Plays != 3 {
		t.Errorf("busiest = %+v, want 2026-04-03 (same time, more plays)", got)
	}
}

func TestFillMonths(t *testing.T) {
	loc := time.UTC
	r, _ := YearRange(2025, time.Date(2026, 6, 1, 0, 0, 0, 0, loc), loc)
	got := fillMonths([]gen.GetUserStatsByMonthRow{{Month: "2025-03", Plays: 4, WatchedMs: 10}}, r, loc)
	if len(got) != 12 || got[0].Month != "2025-01" || got[11].Month != "2025-12" {
		t.Fatalf("year months = %+v", got)
	}
	if got[2].Plays != 4 || got[0].Plays != 0 {
		t.Errorf("fill = %+v", got[:3])
	}
	all := Range{Key: "all", Since: epoch, Until: time.Date(2026, 2, 10, 0, 0, 0, 0, loc)}
	got = fillMonths([]gen.GetUserStatsByMonthRow{{Month: "2025-11", Plays: 1}}, all, loc)
	if len(got) != 4 || got[0].Month != "2025-11" || got[3].Month != "2026-02" {
		t.Errorf("all range starts at the first month with plays: %+v", got)
	}
	if got := fillMonths(nil, all, loc); len(got) != 0 {
		t.Errorf("all range without plays = %+v", got)
	}
}

// fakeQ records the shared filter arguments and returns canned rows.
type fakeQ struct {
	totals  map[int]gen.GetUserStatsTotalsRow // by since-year
	books   int64
	titles  []gen.GetUserStatsTopTitlesRow
	days    []gen.GetUserStatsDaysRow
	devices []gen.GetUserStatsDevicesRow
	first   *gen.GetUserFirstPlayRow
	artists []gen.GetUserNewArtistsRow

	gotLibs    []uuid.UUID
	gotRank    *int32
	gotBlocked []string
	gotAllowed []string
	gotTZ      string
}

func (f *fakeQ) GetUserStatsTotals(_ context.Context, a gen.GetUserStatsTotalsParams) (gen.GetUserStatsTotalsRow, error) {
	return f.totals[a.Since.Time.Year()], nil
}
func (f *fakeQ) GetUserStatsByType(context.Context, gen.GetUserStatsByTypeParams) ([]gen.GetUserStatsByTypeRow, error) {
	return []gen.GetUserStatsByTypeRow{{Type: "movie", Plays: 2, WatchedMs: 100}}, nil
}
func (f *fakeQ) GetUserStatsTopGenres(context.Context, gen.GetUserStatsTopGenresParams) ([]gen.GetUserStatsTopGenresRow, error) {
	return nil, nil
}
func (f *fakeQ) GetUserStatsTopTitles(_ context.Context, a gen.GetUserStatsTopTitlesParams) ([]gen.GetUserStatsTopTitlesRow, error) {
	f.gotLibs, f.gotRank, f.gotBlocked, f.gotAllowed = a.LibraryIds, a.MaxRatingRank, a.BlockedLabels, a.AllowedLabels
	return f.titles, nil
}
func (f *fakeQ) GetUserStatsByMonth(context.Context, gen.GetUserStatsByMonthParams) ([]gen.GetUserStatsByMonthRow, error) {
	return nil, nil
}
func (f *fakeQ) GetUserStatsHeatmap(_ context.Context, a gen.GetUserStatsHeatmapParams) ([]gen.GetUserStatsHeatmapRow, error) {
	f.gotTZ = a.Tz
	return []gen.GetUserStatsHeatmapRow{{Weekday: 1, Hour: 20, Plays: 3}}, nil
}
func (f *fakeQ) GetUserStatsDays(context.Context, gen.GetUserStatsDaysParams) ([]gen.GetUserStatsDaysRow, error) {
	return f.days, nil
}
func (f *fakeQ) GetUserStatsDevices(context.Context, gen.GetUserStatsDevicesParams) ([]gen.GetUserStatsDevicesRow, error) {
	return f.devices, nil
}
func (f *fakeQ) GetUserBooksFinished(context.Context, gen.GetUserBooksFinishedParams) (int64, error) {
	return f.books, nil
}
func (f *fakeQ) GetUserFirstPlay(context.Context, gen.GetUserFirstPlayParams) (gen.GetUserFirstPlayRow, error) {
	if f.first == nil {
		return gen.GetUserFirstPlayRow{}, pgx.ErrNoRows
	}
	return *f.first, nil
}
func (f *fakeQ) GetUserNewArtists(context.Context, gen.GetUserNewArtistsParams) ([]gen.GetUserNewArtistsRow, error) {
	return f.artists, nil
}

func TestCompute_PassesViewerGatesAndShapes(t *testing.T) {
	lib := uuid.New()
	f := &fakeQ{
		totals: map[int]gen.GetUserStatsTotalsRow{2026: {Plays: 5, VideoMs: 5_400_000, AudioMs: 1_800_000, ItemsFinished: 2, AudiobooksFinished: 1}},
		books:  2,
		titles: []gen.GetUserStatsTopTitlesRow{
			{Category: "movie", RefID: uuid.New(), Title: "Heat", Plays: 2},
			{Category: "show", RefID: uuid.New(), Title: "Severance", Plays: 7},
			{Category: "album", RefID: uuid.New(), Title: "Blue", ParentTitle: "Joni Mitchell", Plays: 9},
		},
	}
	svc := New(f)
	v := Viewer{
		UserID: uuid.New(), LibraryIDs: []uuid.UUID{lib}, MaxContentRating: "PG-13",
		Labels: itemlabel.Filter{Blocked: []string{"horror"}},
	}
	r, _ := ParseRange("year:2026", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.UTC)
	out, err := svc.Compute(context.Background(), v, r, "UTC", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.gotLibs) != 1 || f.gotLibs[0] != lib {
		t.Errorf("library ids = %v", f.gotLibs)
	}
	if f.gotRank == nil || *f.gotRank != 2 {
		t.Errorf("rating rank = %v, want 2 (PG-13)", f.gotRank)
	}
	if len(f.gotBlocked) != 1 || f.gotAllowed != nil {
		t.Errorf("labels = %v / %v", f.gotBlocked, f.gotAllowed)
	}
	if out.Totals.HoursWatched != 1.5 || out.Totals.HoursListened != 0.5 || out.Totals.BooksRead != 3 {
		t.Errorf("totals = %+v", out.Totals)
	}
	if len(out.TopTitles.Movies) != 1 || len(out.TopTitles.Shows) != 1 || len(out.TopTitles.Albums) != 1 || out.TopTitles.Artists == nil {
		t.Errorf("top titles = %+v", out.TopTitles)
	}
	if out.TopTitles.Albums[0].ParentTitle != "Joni Mitchell" {
		t.Errorf("album artist = %q", out.TopTitles.Albums[0].ParentTitle)
	}
	if len(out.ByMonth) != 12 || len(out.Heatmap) != 1 || out.Devices == nil || out.TopGenres == nil {
		t.Errorf("shape: months %d heatmap %d devices %v genres %v", len(out.ByMonth), len(out.Heatmap), out.Devices, out.TopGenres)
	}
}

func TestCompute_UnrestrictedViewerSendsNulls(t *testing.T) {
	f := &fakeQ{}
	r, _ := ParseRange("30d", time.Now(), time.UTC)
	if _, err := New(f).Compute(context.Background(), Viewer{UserID: uuid.New()}, r, "UTC", time.UTC); err != nil {
		t.Fatal(err)
	}
	if f.gotLibs != nil || f.gotRank != nil || f.gotBlocked != nil || f.gotAllowed != nil {
		t.Errorf("an admin with no restriction must pass NULLs: %v %v %v %v", f.gotLibs, f.gotRank, f.gotBlocked, f.gotAllowed)
	}
	// An empty grant set is NOT "every library".
	if LibraryIDList(map[uuid.UUID]struct{}{}) == nil {
		t.Error("an empty grant set must stay a non-nil empty slice")
	}
	if LibraryIDList(nil) != nil {
		t.Error("nil (admin) must stay nil")
	}
}

func TestYearInReview(t *testing.T) {
	first := gen.GetUserFirstPlayRow{ID: uuid.New(), Type: "movie", Title: "Dune", OccurredAt: pgtype.Timestamptz{Time: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), Valid: true}}
	busy := day("2026-03-03")
	busy.WatchedMs = 99_000
	f := &fakeQ{
		totals: map[int]gen.GetUserStatsTotalsRow{
			2026: {Plays: 40, VideoMs: 36_000_000},
			2025: {Plays: 10, VideoMs: 7_200_000},
		},
		days:    []gen.GetUserStatsDaysRow{day("2026-03-01"), day("2026-03-02"), busy},
		devices: []gen.GetUserStatsDevicesRow{{Device: "Fire TV", Plays: 30}},
		first:   &first,
		artists: []gen.GetUserNewArtistsRow{{ID: uuid.New(), Title: "Khruangbin", Plays: 12, Total: 7}},
	}
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	out, err := New(f).YearInReview(context.Background(), Viewer{UserID: uuid.New()}, 2026, now, "UTC", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if !out.HasData || out.Totals.HoursWatched != 10 {
		t.Errorf("headline = %+v", out.Totals)
	}
	if out.PreviousYear == nil || out.PreviousYear.Year != 2025 || out.PreviousYear.Totals.Plays != 10 {
		t.Errorf("previous year = %+v", out.PreviousYear)
	}
	if out.FirstPlay == nil || out.FirstPlay.Title != "Dune" {
		t.Errorf("first play = %+v", out.FirstPlay)
	}
	if out.BusiestDay == nil || out.BusiestDay.Date != "2026-03-03" {
		t.Errorf("busiest day = %+v", out.BusiestDay)
	}
	if out.LongestStreak.Days != 3 {
		t.Errorf("streak = %+v", out.LongestStreak)
	}
	if out.NewArtists.Count != 7 || len(out.NewArtists.Top) != 1 {
		t.Errorf("new artists = %+v", out.NewArtists)
	}
	if out.TopDevice == nil || out.TopDevice.Device != "Fire TV" {
		t.Errorf("top device = %+v", out.TopDevice)
	}

	// No plays the year before: no comparison. No plays at all: has_data false.
	f2 := &fakeQ{totals: map[int]gen.GetUserStatsTotalsRow{}}
	out, err = New(f2).YearInReview(context.Background(), Viewer{UserID: uuid.New()}, 2026, now, "UTC", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if out.HasData || out.PreviousYear != nil || out.FirstPlay != nil || out.BusiestDay != nil {
		t.Errorf("empty year = %+v", out)
	}
	if _, err := New(f2).YearInReview(context.Background(), Viewer{}, 2027, now, "UTC", time.UTC); !errors.Is(err, ErrBadRange) {
		t.Errorf("future year err = %v", err)
	}
}
