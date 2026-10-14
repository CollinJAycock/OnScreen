// Package userstats computes a user's own listening and viewing stats: the
// My stats page, Year in review, and the digest email's month in numbers.
//
// Everything is aggregated in SQL (internal/db/queries/user_stats.sql) over
// the watch_plays analytics matview for one user, through its
// (user_id, occurred_at) index (migration 00054), so a long history costs an
// index range scan rather than a walk over every user's plays. watch_plays is
// refreshed every 10 minutes by the refresh_watch_plays task, so the newest
// plays show up within that window, as on the admin dashboard.
//
// Every query applies the viewer's library access, content-rating ceiling and
// label restriction (Viewer): a title the user can't see never appears in
// their stats, the same rule their history follows.
package userstats

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/sync/errgroup"

	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/itemlabel"
)

// Querier is the generated-query subset the service reads. Satisfied by
// *gen.Queries.
type Querier interface {
	GetUserStatsTotals(ctx context.Context, arg gen.GetUserStatsTotalsParams) (gen.GetUserStatsTotalsRow, error)
	GetUserStatsByType(ctx context.Context, arg gen.GetUserStatsByTypeParams) ([]gen.GetUserStatsByTypeRow, error)
	GetUserStatsTopGenres(ctx context.Context, arg gen.GetUserStatsTopGenresParams) ([]gen.GetUserStatsTopGenresRow, error)
	GetUserStatsTopTitles(ctx context.Context, arg gen.GetUserStatsTopTitlesParams) ([]gen.GetUserStatsTopTitlesRow, error)
	GetUserStatsByMonth(ctx context.Context, arg gen.GetUserStatsByMonthParams) ([]gen.GetUserStatsByMonthRow, error)
	GetUserStatsHeatmap(ctx context.Context, arg gen.GetUserStatsHeatmapParams) ([]gen.GetUserStatsHeatmapRow, error)
	GetUserStatsDays(ctx context.Context, arg gen.GetUserStatsDaysParams) ([]gen.GetUserStatsDaysRow, error)
	GetUserStatsDevices(ctx context.Context, arg gen.GetUserStatsDevicesParams) ([]gen.GetUserStatsDevicesRow, error)
	GetUserBooksFinished(ctx context.Context, arg gen.GetUserBooksFinishedParams) (int64, error)
	GetUserFirstPlay(ctx context.Context, arg gen.GetUserFirstPlayParams) (gen.GetUserFirstPlayRow, error)
	GetUserNewArtists(ctx context.Context, arg gen.GetUserNewArtistsParams) ([]gen.GetUserNewArtistsRow, error)
}

// Service computes stats. Construct with New.
type Service struct {
	q Querier
}

// New returns a Service reading through q.
func New(q Querier) *Service { return &Service{q: q} }

// Viewer is whose stats are computed and what they may see.
type Viewer struct {
	UserID uuid.UUID
	// LibraryIDs is the set of libraries the user can see; nil means every
	// library (admins). A non-nil empty slice means none.
	LibraryIDs []uuid.UUID
	// MaxContentRating is the user's rating ceiling ("" = none).
	MaxContentRating string
	Labels           itemlabel.Filter
}

// LibraryIDList turns the map form library-access checkers return (nil =
// every library) into Viewer.LibraryIDs, keeping nil as nil and an empty set
// as an empty, non-nil slice.
func LibraryIDList(set map[uuid.UUID]struct{}) []uuid.UUID {
	if set == nil {
		return nil
	}
	out := make([]uuid.UUID, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out
}

func (v Viewer) ratingRank() *int32 {
	rk := contentrating.MaxRatingRank(v.MaxContentRating)
	if rk == nil {
		return nil
	}
	r := int32(*rk)
	return &r
}

// Range is a half-open time window [Since, Until).
type Range struct {
	// Key is the canonical request value: "30d", "90d", "365d", "all" or
	// "year:2026".
	Key   string
	Since time.Time
	Until time.Time
}

// ErrBadRange is returned by ParseRange for an unsupported value.
var ErrBadRange = errors.New("userstats: range must be 30d, 90d, 365d, all or year:YYYY")

// epoch is the start of the "all" range; watch history can't predate it.
var epoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// ParseRange resolves a range value against now in loc. Empty means 30d.
// A year range runs from 1 January to 1 January in loc; a year after now's is
// refused, and so is one before 2000.
func ParseRange(raw string, now time.Time, loc *time.Location) (Range, error) {
	if loc == nil {
		loc = time.UTC
	}
	// A minute of headroom so a play recorded "now" by a skewed clock still
	// lands inside the window.
	until := now.Add(time.Minute)
	switch raw {
	case "", "30d":
		return Range{Key: "30d", Since: now.AddDate(0, 0, -30), Until: until}, nil
	case "90d":
		return Range{Key: "90d", Since: now.AddDate(0, 0, -90), Until: until}, nil
	case "365d":
		return Range{Key: "365d", Since: now.AddDate(0, 0, -365), Until: until}, nil
	case "all":
		return Range{Key: "all", Since: epoch, Until: until}, nil
	}
	if y, ok := strings.CutPrefix(raw, "year:"); ok {
		year, err := strconv.Atoi(y)
		if err != nil || len(y) != 4 {
			return Range{}, ErrBadRange
		}
		return YearRange(year, now, loc)
	}
	return Range{}, ErrBadRange
}

// YearRange is the calendar year in loc. Years after now's (in loc) and before
// 2000 are refused with ErrBadRange.
func YearRange(year int, now time.Time, loc *time.Location) (Range, error) {
	if loc == nil {
		loc = time.UTC
	}
	if year < 2000 || year > now.In(loc).Year() {
		return Range{}, ErrBadRange
	}
	return Range{
		Key:   "year:" + strconv.Itoa(year),
		Since: time.Date(year, 1, 1, 0, 0, 0, 0, loc),
		Until: time.Date(year+1, 1, 1, 0, 0, 0, 0, loc),
	}, nil
}

// ── Response types ───────────────────────────────────────────────────────────

// Totals are the headline numbers. VideoMS is time watched (movies, episodes,
// music videos, home videos), AudioMS time listened (tracks, audiobooks,
// podcasts); the hour fields are the same rounded to one decimal.
type Totals struct {
	Plays         int64   `json:"plays"`
	VideoMS       int64   `json:"watch_ms"`
	AudioMS       int64   `json:"listen_ms"`
	HoursWatched  float64 `json:"hours_watched"`
	HoursListened float64 `json:"hours_listened"`
	ItemsFinished int64   `json:"items_finished"`
	BooksRead     int64   `json:"books_read"` // ebooks/comics marked read + audiobooks finished
	ActiveDays    int64   `json:"active_days"`
}

// TypeCount is plays and time for one media type.
type TypeCount struct {
	Type  string `json:"type"`
	Plays int64  `json:"plays"`
	MS    int64  `json:"ms"`
}

// GenreCount is plays and time for one genre.
type GenreCount struct {
	Genre string `json:"genre"`
	Plays int64  `json:"plays"`
	MS    int64  `json:"ms"`
}

// Title is one entry of a top list.
type Title struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Year        *int   `json:"year,omitempty"`
	PosterPath  string `json:"poster_path,omitempty"`
	ParentTitle string `json:"parent_title,omitempty"` // an album's artist
	Plays       int64  `json:"plays"`
	MS          int64  `json:"ms"`
}

// TopTitles are the top lists per category. Empty lists are [], never null.
type TopTitles struct {
	Movies  []Title `json:"movies"`
	Shows   []Title `json:"shows"`
	Artists []Title `json:"artists"`
	Albums  []Title `json:"albums"`
}

// MonthCount is one calendar month ("2026-10") of activity.
type MonthCount struct {
	Month string `json:"month"`
	Plays int64  `json:"plays"`
	MS    int64  `json:"ms"`
}

// HeatCell is plays in one weekday (1 = Monday .. 7 = Sunday) and hour.
// Sparse: cells without plays are absent.
type HeatCell struct {
	Weekday int   `json:"weekday"`
	Hour    int   `json:"hour"`
	Plays   int64 `json:"plays"`
}

// Streak is the longest run of consecutive days with at least one play.
type Streak struct {
	Days  int    `json:"days"`
	Start string `json:"start,omitempty"` // "2006-01-02"
	End   string `json:"end,omitempty"`
}

// DeviceCount is plays and time on one device.
type DeviceCount struct {
	Device string `json:"device"`
	Plays  int64  `json:"plays"`
	MS     int64  `json:"ms"`
}

// Stats is the GET /users/me/stats response.
type Stats struct {
	Range         string        `json:"range"`
	Since         string        `json:"since"` // RFC 3339
	Until         string        `json:"until"`
	Timezone      string        `json:"timezone"`
	Totals        Totals        `json:"totals"`
	ByType        []TypeCount   `json:"by_type"`
	TopGenres     []GenreCount  `json:"top_genres"`
	TopTitles     TopTitles     `json:"top_titles"`
	ByMonth       []MonthCount  `json:"by_month"`
	Heatmap       []HeatCell    `json:"heatmap"`
	LongestStreak Streak        `json:"longest_streak"`
	Devices       []DeviceCount `json:"devices"`
}

// topLimit / genreLimit / deviceLimit bound the stats page's lists.
const (
	topLimit    = 10
	genreLimit  = 10
	deviceLimit = 8
)

// Compute returns v's stats over r, bucketing days, months and hours in tz
// (a validated IANA name; loc is the same zone loaded).
func (s *Service) Compute(ctx context.Context, v Viewer, r Range, tz string, loc *time.Location) (*Stats, error) {
	out, _, err := s.compute(ctx, v, r, tz, loc)
	return out, err
}

// compute is Compute that also hands back the per-day rows (Year in review
// derives the busiest day from them).
func (s *Service) compute(ctx context.Context, v Viewer, r Range, tz string, loc *time.Location) (*Stats, []gen.GetUserStatsDaysRow, error) {
	p := params(v, r)
	var (
		totals  gen.GetUserStatsTotalsRow
		books   int64
		byType  []gen.GetUserStatsByTypeRow
		genres  []gen.GetUserStatsTopGenresRow
		titles  []gen.GetUserStatsTopTitlesRow
		months  []gen.GetUserStatsByMonthRow
		heat    []gen.GetUserStatsHeatmapRow
		days    []gen.GetUserStatsDaysRow
		devices []gen.GetUserStatsDevicesRow
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { totals, err = s.q.GetUserStatsTotals(gctx, p.totals(tz)); return })
	g.Go(func() (err error) { books, err = s.q.GetUserBooksFinished(gctx, p.books()); return })
	g.Go(func() (err error) { byType, err = s.q.GetUserStatsByType(gctx, p.byType()); return })
	g.Go(func() (err error) { genres, err = s.q.GetUserStatsTopGenres(gctx, p.genres(genreLimit)); return })
	g.Go(func() (err error) { titles, err = s.q.GetUserStatsTopTitles(gctx, p.titles(topLimit)); return })
	g.Go(func() (err error) { months, err = s.q.GetUserStatsByMonth(gctx, p.months(tz)); return })
	g.Go(func() (err error) { heat, err = s.q.GetUserStatsHeatmap(gctx, p.heatmap(tz)); return })
	g.Go(func() (err error) { days, err = s.q.GetUserStatsDays(gctx, p.days(tz)); return })
	g.Go(func() (err error) { devices, err = s.q.GetUserStatsDevices(gctx, p.devices(deviceLimit)); return })
	if err := g.Wait(); err != nil {
		return nil, nil, fmt.Errorf("userstats: %w", err)
	}

	out := &Stats{
		Range:         r.Key,
		Since:         r.Since.UTC().Format(time.RFC3339),
		Until:         r.Until.UTC().Format(time.RFC3339),
		Timezone:      tz,
		Totals:        makeTotals(totals, books),
		ByType:        make([]TypeCount, 0, len(byType)),
		TopGenres:     make([]GenreCount, 0, len(genres)),
		TopTitles:     splitTitles(titles),
		Heatmap:       make([]HeatCell, 0, len(heat)),
		LongestStreak: longestStreak(days),
		Devices:       make([]DeviceCount, 0, len(devices)),
	}
	for _, t := range byType {
		out.ByType = append(out.ByType, TypeCount{Type: t.Type, Plays: t.Plays, MS: t.WatchedMs})
	}
	for _, g := range genres {
		out.TopGenres = append(out.TopGenres, GenreCount{Genre: g.Genre, Plays: g.Plays, MS: g.WatchedMs})
	}
	for _, h := range heat {
		out.Heatmap = append(out.Heatmap, HeatCell{Weekday: int(h.Weekday), Hour: int(h.Hour), Plays: h.Plays})
	}
	for _, d := range devices {
		out.Devices = append(out.Devices, DeviceCount{Device: d.Device, Plays: d.Plays, MS: d.WatchedMs})
	}
	out.ByMonth = fillMonths(months, r, loc)
	return out, days, nil
}

// TotalsFor returns only the headline numbers for v over r (the digest's
// month in numbers, the year-over-year comparison).
func (s *Service) TotalsFor(ctx context.Context, v Viewer, r Range, tz string) (Totals, error) {
	p := params(v, r)
	var (
		totals gen.GetUserStatsTotalsRow
		books  int64
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { totals, err = s.q.GetUserStatsTotals(gctx, p.totals(tz)); return })
	g.Go(func() (err error) { books, err = s.q.GetUserBooksFinished(gctx, p.books()); return })
	if err := g.Wait(); err != nil {
		return Totals{}, fmt.Errorf("userstats: totals: %w", err)
	}
	return makeTotals(totals, books), nil
}

// ── Year in review ───────────────────────────────────────────────────────────

// FirstPlay is the first thing played in the year.
type FirstPlay struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Year        *int   `json:"year,omitempty"`
	ParentTitle string `json:"parent_title,omitempty"`
	PosterPath  string `json:"poster_path,omitempty"`
	OccurredAt  string `json:"occurred_at"` // RFC 3339
}

// BusiestDay is the day with the most time played.
type BusiestDay struct {
	Date  string `json:"date"` // "2006-01-02" in the request timezone
	Plays int64  `json:"plays"`
	MS    int64  `json:"ms"`
}

// NewArtists are the artists first played this year.
type NewArtists struct {
	Count int64   `json:"count"`
	Top   []Title `json:"top"`
}

// YearComparison is the previous year's headline numbers, for "up 20% on
// last year".
type YearComparison struct {
	Year   int    `json:"year"`
	Totals Totals `json:"totals"`
}

// YearInReview is the GET /users/me/year-in-review response. Lists hold at
// most five entries.
type YearInReview struct {
	Year          int             `json:"year"`
	Timezone      string          `json:"timezone"`
	HasData       bool            `json:"has_data"`
	Totals        Totals          `json:"totals"`
	LongestStreak Streak          `json:"longest_streak"`
	TopMovies     []Title         `json:"top_movies"`
	TopShows      []Title         `json:"top_shows"`
	TopArtists    []Title         `json:"top_artists"`
	TopAlbums     []Title         `json:"top_albums"`
	TopGenres     []GenreCount    `json:"top_genres"`
	TopDevice     *DeviceCount    `json:"top_device"`
	FirstPlay     *FirstPlay      `json:"first_play"`
	BusiestDay    *BusiestDay     `json:"busiest_day"`
	NewArtists    NewArtists      `json:"new_artists"`
	ByMonth       []MonthCount    `json:"by_month"`
	PreviousYear  *YearComparison `json:"previous_year"`
}

const reviewTop = 5

// YearInReview returns v's curated summary of year (in loc).
func (s *Service) YearInReview(ctx context.Context, v Viewer, year int, now time.Time, tz string, loc *time.Location) (*YearInReview, error) {
	r, err := YearRange(year, now, loc)
	if err != nil {
		return nil, err
	}
	p := params(v, r)
	var (
		stats    *Stats
		days     []gen.GetUserStatsDaysRow
		first    gen.GetUserFirstPlayRow
		hasFirst bool
		newArt   []gen.GetUserNewArtistsRow
		prev     *YearComparison
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { stats, days, err = s.compute(gctx, v, r, tz, loc); return })
	g.Go(func() error {
		row, err := s.q.GetUserFirstPlay(gctx, p.firstPlay())
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("userstats: first play: %w", err)
		}
		first, hasFirst = row, true
		return nil
	})
	g.Go(func() (err error) {
		newArt, err = s.q.GetUserNewArtists(gctx, p.newArtists(reviewTop))
		return
	})
	if year > 2000 {
		g.Go(func() error {
			pr, err := YearRange(year-1, now, loc)
			if err != nil {
				return nil
			}
			t, err := s.TotalsFor(gctx, v, pr, tz)
			if err != nil {
				return err
			}
			if t.Plays > 0 || t.BooksRead > 0 {
				prev = &YearComparison{Year: year - 1, Totals: t}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	out := &YearInReview{
		Year:          year,
		Timezone:      tz,
		HasData:       stats.Totals.Plays > 0 || stats.Totals.BooksRead > 0,
		Totals:        stats.Totals,
		LongestStreak: stats.LongestStreak,
		TopMovies:     head(stats.TopTitles.Movies, reviewTop),
		TopShows:      head(stats.TopTitles.Shows, reviewTop),
		TopArtists:    head(stats.TopTitles.Artists, reviewTop),
		TopAlbums:     head(stats.TopTitles.Albums, reviewTop),
		TopGenres:     head(stats.TopGenres, reviewTop),
		ByMonth:       stats.ByMonth,
		PreviousYear:  prev,
		NewArtists:    NewArtists{Top: make([]Title, 0, len(newArt))},
	}
	if len(stats.Devices) > 0 {
		d := stats.Devices[0]
		out.TopDevice = &d
	}
	if hasFirst {
		out.FirstPlay = &FirstPlay{
			ID:          first.ID.String(),
			Type:        first.Type,
			Title:       first.Title,
			Year:        intPtr(first.Year),
			ParentTitle: first.ParentTitle,
			PosterPath:  first.PosterPath,
			OccurredAt:  first.OccurredAt.Time.UTC().Format(time.RFC3339),
		}
	}
	for _, a := range newArt {
		out.NewArtists.Count = a.Total
		out.NewArtists.Top = append(out.NewArtists.Top, Title{
			ID: a.ID.String(), Title: a.Title, PosterPath: a.PosterPath, Plays: a.Plays,
		})
	}
	out.BusiestDay = busiestDay(days)
	return out, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func makeTotals(t gen.GetUserStatsTotalsRow, books int64) Totals {
	return Totals{
		Plays:         t.Plays,
		VideoMS:       t.VideoMs,
		AudioMS:       t.AudioMs,
		HoursWatched:  hours(t.VideoMs),
		HoursListened: hours(t.AudioMs),
		ItemsFinished: t.ItemsFinished,
		BooksRead:     books + t.AudiobooksFinished,
		ActiveDays:    t.ActiveDays,
	}
}

// hours converts milliseconds to hours rounded to one decimal.
func hours(ms int64) float64 {
	return math.Round(float64(ms)/3_600_000*10) / 10
}

func intPtr(v *int32) *int {
	if v == nil {
		return nil
	}
	i := int(*v)
	return &i
}

func head[T any](s []T, n int) []T {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// splitTitles groups GetUserStatsTopTitles rows (already ranked per
// category) into the four lists.
func splitTitles(rows []gen.GetUserStatsTopTitlesRow) TopTitles {
	out := TopTitles{Movies: []Title{}, Shows: []Title{}, Artists: []Title{}, Albums: []Title{}}
	for _, r := range rows {
		t := Title{
			ID:          r.RefID.String(),
			Title:       r.Title,
			Year:        intPtr(r.Year),
			PosterPath:  r.PosterPath,
			ParentTitle: r.ParentTitle,
			Plays:       r.Plays,
			MS:          r.WatchedMs,
		}
		switch r.Category {
		case "movie":
			out.Movies = append(out.Movies, t)
		case "show":
			out.Shows = append(out.Shows, t)
		case "artist":
			out.Artists = append(out.Artists, t)
		case "album":
			out.Albums = append(out.Albums, t)
		}
	}
	return out
}

func dayOf(d pgtype.Date) time.Time {
	t := d.Time
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// longestStreak finds the longest run of consecutive days in rows (one row
// per day with plays, any order). Ties keep the earliest run.
func longestStreak(rows []gen.GetUserStatsDaysRow) Streak {
	if len(rows) == 0 {
		return Streak{}
	}
	days := make([]time.Time, 0, len(rows))
	for _, r := range rows {
		if r.Day.Valid {
			days = append(days, dayOf(r.Day))
		}
	}
	if len(days) == 0 {
		return Streak{}
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
	best := Streak{Days: 1, Start: days[0].Format(time.DateOnly), End: days[0].Format(time.DateOnly)}
	runStart, runLen := days[0], 1
	for i := 1; i < len(days); i++ {
		switch days[i].Sub(days[i-1]) {
		case 0:
			continue // duplicate day; can't happen from the query, harmless
		case 24 * time.Hour:
			runLen++
		default:
			runStart, runLen = days[i], 1
		}
		if runLen > best.Days {
			best = Streak{Days: runLen, Start: runStart.Format(time.DateOnly), End: days[i].Format(time.DateOnly)}
		}
	}
	return best
}

// busiestDay is the day with the most time played (plays break ties, then
// the earlier day).
func busiestDay(rows []gen.GetUserStatsDaysRow) *BusiestDay {
	var best *gen.GetUserStatsDaysRow
	for i := range rows {
		r := &rows[i]
		if !r.Day.Valid {
			continue
		}
		if best == nil || r.WatchedMs > best.WatchedMs ||
			(r.WatchedMs == best.WatchedMs && r.Plays > best.Plays) ||
			(r.WatchedMs == best.WatchedMs && r.Plays == best.Plays && dayOf(r.Day).Before(dayOf(best.Day))) {
			best = r
		}
	}
	if best == nil {
		return nil
	}
	return &BusiestDay{Date: dayOf(best.Day).Format(time.DateOnly), Plays: best.Plays, MS: best.WatchedMs}
}

// fillMonths returns one entry per calendar month of r (in loc), zero where
// the query had no row. For the open-ended "all" range it starts at the first
// month with plays rather than at the epoch.
func fillMonths(rows []gen.GetUserStatsByMonthRow, r Range, loc *time.Location) []MonthCount {
	if loc == nil {
		loc = time.UTC
	}
	have := make(map[string]gen.GetUserStatsByMonthRow, len(rows))
	first := ""
	for _, row := range rows {
		have[row.Month] = row
		if first == "" || row.Month < first {
			first = row.Month
		}
	}
	start := r.Since.In(loc)
	if r.Key == "all" {
		if first == "" {
			return []MonthCount{}
		}
		t, err := time.ParseInLocation("2006-01", first, loc)
		if err != nil {
			return []MonthCount{}
		}
		start = t
	}
	// The last instant inside the range decides the final month (a year range
	// ends at midnight on 1 January, which belongs to the next year).
	end := r.Until.Add(-time.Nanosecond).In(loc)
	out := []MonthCount{}
	m := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, loc)
	last := time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, loc)
	for i := 0; !m.After(last) && i < 400; i++ {
		key := m.Format("2006-01")
		row := have[key]
		out = append(out, MonthCount{Month: key, Plays: row.Plays, MS: row.WatchedMs})
		m = m.AddDate(0, 1, 0)
	}
	return out
}

// queryParams carries the shared filter arguments of every stats query.
type queryParams struct {
	userID  uuid.UUID
	since   pgtype.Timestamptz
	until   pgtype.Timestamptz
	libs    []uuid.UUID
	rank    *int32
	blocked []string
	allowed []string
}

func params(v Viewer, r Range) queryParams {
	return queryParams{
		userID:  v.UserID,
		since:   pgtype.Timestamptz{Time: r.Since, Valid: true},
		until:   pgtype.Timestamptz{Time: r.Until, Valid: true},
		libs:    v.LibraryIDs,
		rank:    v.ratingRank(),
		blocked: v.Labels.BlockedArg(),
		allowed: v.Labels.AllowedArg(),
	}
}

func (p queryParams) totals(tz string) gen.GetUserStatsTotalsParams {
	return gen.GetUserStatsTotalsParams{Tz: tz, UserID: p.userID, Since: p.since, Until: p.until,
		LibraryIds: p.libs, MaxRatingRank: p.rank, BlockedLabels: p.blocked, AllowedLabels: p.allowed}
}

func (p queryParams) books() gen.GetUserBooksFinishedParams {
	return gen.GetUserBooksFinishedParams{UserID: p.userID, Since: p.since, Until: p.until,
		LibraryIds: p.libs, MaxRatingRank: p.rank, BlockedLabels: p.blocked, AllowedLabels: p.allowed}
}

func (p queryParams) byType() gen.GetUserStatsByTypeParams {
	return gen.GetUserStatsByTypeParams{UserID: p.userID, Since: p.since, Until: p.until,
		LibraryIds: p.libs, MaxRatingRank: p.rank, BlockedLabels: p.blocked, AllowedLabels: p.allowed}
}

func (p queryParams) genres(lim int32) gen.GetUserStatsTopGenresParams {
	return gen.GetUserStatsTopGenresParams{UserID: p.userID, Since: p.since, Until: p.until,
		LibraryIds: p.libs, MaxRatingRank: p.rank, BlockedLabels: p.blocked, AllowedLabels: p.allowed, Lim: lim}
}

func (p queryParams) titles(lim int32) gen.GetUserStatsTopTitlesParams {
	return gen.GetUserStatsTopTitlesParams{UserID: p.userID, Since: p.since, Until: p.until,
		LibraryIds: p.libs, MaxRatingRank: p.rank, BlockedLabels: p.blocked, AllowedLabels: p.allowed, Lim: lim}
}

func (p queryParams) months(tz string) gen.GetUserStatsByMonthParams {
	return gen.GetUserStatsByMonthParams{Tz: tz, UserID: p.userID, Since: p.since, Until: p.until,
		LibraryIds: p.libs, MaxRatingRank: p.rank, BlockedLabels: p.blocked, AllowedLabels: p.allowed}
}

func (p queryParams) heatmap(tz string) gen.GetUserStatsHeatmapParams {
	return gen.GetUserStatsHeatmapParams{Tz: tz, UserID: p.userID, Since: p.since, Until: p.until,
		LibraryIds: p.libs, MaxRatingRank: p.rank, BlockedLabels: p.blocked, AllowedLabels: p.allowed}
}

func (p queryParams) days(tz string) gen.GetUserStatsDaysParams {
	return gen.GetUserStatsDaysParams{Tz: tz, UserID: p.userID, Since: p.since, Until: p.until,
		LibraryIds: p.libs, MaxRatingRank: p.rank, BlockedLabels: p.blocked, AllowedLabels: p.allowed}
}

func (p queryParams) devices(lim int32) gen.GetUserStatsDevicesParams {
	return gen.GetUserStatsDevicesParams{UserID: p.userID, Since: p.since, Until: p.until,
		LibraryIds: p.libs, MaxRatingRank: p.rank, BlockedLabels: p.blocked, AllowedLabels: p.allowed, Lim: lim}
}

func (p queryParams) firstPlay() gen.GetUserFirstPlayParams {
	return gen.GetUserFirstPlayParams{UserID: p.userID, Since: p.since, Until: p.until,
		LibraryIds: p.libs, MaxRatingRank: p.rank, BlockedLabels: p.blocked, AllowedLabels: p.allowed}
}

func (p queryParams) newArtists(lim int32) gen.GetUserNewArtistsParams {
	return gen.GetUserNewArtistsParams{UserID: p.userID, Since: p.since, Until: p.until,
		LibraryIds: p.libs, MaxRatingRank: p.rank, BlockedLabels: p.blocked, AllowedLabels: p.allowed, Lim: lim}
}
