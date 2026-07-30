package v1

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// analyticsCacheTTL bounds how stale the dashboard can be. The aggregates
// (most over the watch_plays window) cost hundreds of ms each on a populated
// catalog, and the page auto-refreshes every 30s — so without memoization that
// whole bundle re-runs twice a minute per open tab. The response is
// server-global per (display timezone, range), so one cached value serves
// everyone on that view; a short TTL keeps the numbers fresh enough for a
// dashboard while amortizing the cost. Same posture as the hub's
// trendingCache.
const analyticsCacheTTL = 5 * time.Minute

// analyticsQuerier is the DB subset needed by AnalyticsHandler.
type analyticsQuerier interface {
	GetAnalyticsOverview(ctx context.Context) (gen.GetAnalyticsOverviewRow, error)
	GetLibraryAnalytics(ctx context.Context) ([]gen.GetLibraryAnalyticsRow, error)
	GetVideoCodecBreakdown(ctx context.Context) ([]gen.GetVideoCodecBreakdownRow, error)
	GetContainerBreakdown(ctx context.Context) ([]gen.GetContainerBreakdownRow, error)
	GetPlaysPerDay(ctx context.Context, arg gen.GetPlaysPerDayParams) ([]gen.GetPlaysPerDayRow, error)
	GetBandwidthPerDay(ctx context.Context, arg gen.GetBandwidthPerDayParams) ([]gen.GetBandwidthPerDayRow, error)
	GetTopPlayed(ctx context.Context, days int32) ([]gen.GetTopPlayedRow, error)
	GetRecentPlays(ctx context.Context) ([]gen.GetRecentPlaysRow, error)
	GetTopUsers(ctx context.Context, days int32) ([]gen.GetTopUsersRow, error)
	GetClientBreakdown(ctx context.Context, days int32) ([]gen.GetClientBreakdownRow, error)
	GetPlaysByHour(ctx context.Context, arg gen.GetPlaysByHourParams) ([]gen.GetPlaysByHourRow, error)
	GetCompletionStats(ctx context.Context, days int32) (gen.GetCompletionStatsRow, error)
	GetStreamTypesPerDay(ctx context.Context, arg gen.GetStreamTypesPerDayParams) ([]gen.GetStreamTypesPerDayRow, error)
}

// AnalyticsHandler handles GET /api/v1/analytics.
type AnalyticsHandler struct {
	db     analyticsQuerier
	logger *slog.Logger
	cache  *analyticsCache
	// group collapses concurrent cache misses for the same (tz, range) into a
	// single compute (the dashboard's 30s poll across multiple open tabs
	// would otherwise stampede the pool every TTL expiry).
	group singleflight.Group
}

// NewAnalyticsHandler creates an AnalyticsHandler.
func NewAnalyticsHandler(db analyticsQuerier, logger *slog.Logger) *AnalyticsHandler {
	return &AnalyticsHandler{db: db, logger: logger, cache: &analyticsCache{ttl: analyticsCacheTTL}}
}

// analyticsCache memoizes the assembled analytics response per
// (display timezone, range) for up to ttl. In practice a homelab sees one or
// two zones and three ranges.
type analyticsCache struct {
	mu      sync.Mutex
	entries map[string]analyticsCacheEntry
	ttl     time.Duration
}

type analyticsCacheEntry struct {
	resp    *analyticsResponse
	fetched time.Time
}

func (c *analyticsCache) get(key string) (*analyticsResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if ok && time.Since(e.fetched) < c.ttl {
		return e.resp, true
	}
	return nil, false
}

func (c *analyticsCache) set(key string, r *analyticsResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]analyticsCacheEntry)
	}
	c.entries[key] = analyticsCacheEntry{resp: r, fetched: time.Now()}
}

// ── JSON response types ───────────────────────────────────────────────────────

type analyticsOverview struct {
	TotalItems       int64 `json:"total_items"`
	TotalFiles       int64 `json:"total_files"`
	TotalSizeBytes   int64 `json:"total_size_bytes"`
	TotalPlays       int64 `json:"total_plays"`
	TotalWatchTimeMS int64 `json:"total_watch_time_ms"`
}

type libraryAnalytics struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	ItemCount      int64  `json:"item_count"`
	TotalSizeBytes int64  `json:"total_size_bytes"`
	Res4K          int64  `json:"res_4k"`
	Res1080p       int64  `json:"res_1080p"`
	Res720p        int64  `json:"res_720p"`
	ResSD          int64  `json:"res_sd"`
}

type codecCount struct {
	Codec string `json:"codec"`
	Count int64  `json:"count"`
}

type containerCount struct {
	Container string `json:"container"`
	Count     int64  `json:"count"`
}

type dayCount struct {
	Date  string `json:"date"` // "2006-01-02" in the requested display timezone
	Count int64  `json:"count"`
}

type dayBytes struct {
	Date  string `json:"date"` // "2006-01-02" in the requested display timezone
	Bytes int64  `json:"bytes"`
}

type topPlayedItem struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Year        *int    `json:"year,omitempty"`
	Type        string  `json:"type"`
	PosterPath  *string `json:"poster_path,omitempty"`
	ParentTitle *string `json:"parent_title,omitempty"` // show/artist for episodes/tracks
	PlayCount   int64   `json:"play_count"`
}

type recentPlay struct {
	Title       string  `json:"title"`
	Year        *int    `json:"year,omitempty"`
	Type        string  `json:"type"`
	ParentTitle *string `json:"parent_title,omitempty"`
	UserName    *string `json:"user_name,omitempty"`
	OccurredAt  string  `json:"occurred_at"` // RFC 3339, UTC
	ClientName  *string `json:"client_name,omitempty"`
	DurationMS  *int64  `json:"duration_ms,omitempty"`
}

type topUser struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	PlayCount   int64  `json:"play_count"`
	WatchTimeMS int64  `json:"watch_time_ms"`
}

type clientCount struct {
	Client string `json:"client"`
	Count  int64  `json:"count"`
}

type hourCount struct {
	Hour  int   `json:"hour"` // 0-23 in the requested display timezone
	Count int64 `json:"count"`
}

type completionStats struct {
	// Plays of media with a known runtime; Completed = final position ≥ 90%.
	Plays     int64 `json:"plays"`
	Completed int64 `json:"completed"`
}

type dayStreamTypes struct {
	Date      string `json:"date"`   // "2006-01-02" in the requested display timezone
	Direct    int64  `json:"direct"` // directPlay + directStream + remux
	Transcode int64  `json:"transcode"`
	Unknown   int64  `json:"unknown"` // rows from clients that don't report a decision
}

type analyticsResponse struct {
	RangeDays        int                `json:"range_days"`
	Overview         analyticsOverview  `json:"overview"`
	Libraries        []libraryAnalytics `json:"libraries"`
	VideoCodecs      []codecCount       `json:"video_codecs"`
	Containers       []containerCount   `json:"containers"`
	PlaysByDay       []dayCount         `json:"plays_by_day"`
	BandwidthByDay   []dayBytes         `json:"bandwidth_by_day"`
	TopPlayed        []topPlayedItem    `json:"top_played"`
	RecentPlays      []recentPlay       `json:"recent_plays"`
	TopUsers         []topUser          `json:"top_users"`
	Clients          []clientCount      `json:"clients"`
	PlaysByHour      []hourCount        `json:"plays_by_hour"`
	Completion       completionStats    `json:"completion"`
	StreamTypesByDay []dayStreamTypes   `json:"stream_types_by_day"`
}

// displayTZ validates the caller-supplied IANA zone name (e.g.
// "America/Detroit" from the browser's Intl API). Anything unloadable falls
// back to UTC — the value is interpolated into AT TIME ZONE as a bind
// parameter, so this is about correct bucketing, not injection.
func displayTZ(raw string) string {
	if raw == "" {
		return "UTC"
	}
	if _, err := time.LoadLocation(raw); err != nil {
		return "UTC"
	}
	return raw
}

// rangeDays clamps the requested window to the supported presets. Anything
// unrecognized gets the default 30.
func rangeDays(raw string) int32 {
	switch raw {
	case "7":
		return 7
	case "90":
		return 90
	default:
		return 30
	}
}

// Get handles GET /api/v1/analytics?tz=<IANA zone>&days=<7|30|90>[&refresh=true].
//
// The underlying aggregates are independent, so they run in parallel against
// the shared pgxpool. Wall time drops from the sum of query latencies to the
// max — critical for this endpoint because the heavy watch_events scans each
// take hundreds of ms on non-trivial libraries and sequential execution
// compounded into ~10s page loads.
func (h *AnalyticsHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tz := displayTZ(r.URL.Query().Get("tz"))
	days := rangeDays(r.URL.Query().Get("days"))
	key := fmt.Sprintf("%s|%d", tz, days)

	// Serve the memoized bundle unless the caller forces a recompute (?refresh=true).
	if r.URL.Query().Get("refresh") != "true" {
		if cached, ok := h.cache.get(key); ok {
			respond.Success(w, r, *cached)
			return
		}
	}

	// Collapse concurrent misses for the same view into one compute. Detach
	// the compute from this request's context: with singleflight, cancelling
	// the first caller would fail every waiter sharing its flight.
	v, err, _ := h.group.Do(key, func() (any, error) {
		resp, err := h.compute(context.WithoutCancel(ctx), tz, days)
		if err != nil {
			return nil, err
		}
		h.cache.set(key, resp)
		return resp, nil
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "analytics: query", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, *(v.(*analyticsResponse)))
}

func (h *AnalyticsHandler) compute(ctx context.Context, tz string, days int32) (*analyticsResponse, error) {
	var (
		overview        gen.GetAnalyticsOverviewRow
		libs            []gen.GetLibraryAnalyticsRow
		codecs          []gen.GetVideoCodecBreakdownRow
		containers      []gen.GetContainerBreakdownRow
		playsPerDay     []gen.GetPlaysPerDayRow
		bandwidthPerDay []gen.GetBandwidthPerDayRow
		topPlayed       []gen.GetTopPlayedRow
		recentPlays     []gen.GetRecentPlaysRow
		topUsers        []gen.GetTopUsersRow
		clients         []gen.GetClientBreakdownRow
		playsByHour     []gen.GetPlaysByHourRow
		completion      gen.GetCompletionStatsRow
		streamTypes     []gen.GetStreamTypesPerDayRow
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		overview, err = h.db.GetAnalyticsOverview(gctx)
		return err
	})
	g.Go(func() (err error) {
		libs, err = h.db.GetLibraryAnalytics(gctx)
		return err
	})
	g.Go(func() (err error) {
		codecs, err = h.db.GetVideoCodecBreakdown(gctx)
		return err
	})
	g.Go(func() (err error) {
		containers, err = h.db.GetContainerBreakdown(gctx)
		return err
	})
	g.Go(func() (err error) {
		playsPerDay, err = h.db.GetPlaysPerDay(gctx, gen.GetPlaysPerDayParams{Tz: tz, Days: days})
		return err
	})
	g.Go(func() (err error) {
		bandwidthPerDay, err = h.db.GetBandwidthPerDay(gctx, gen.GetBandwidthPerDayParams{Tz: tz, Days: days})
		return err
	})
	g.Go(func() (err error) {
		topPlayed, err = h.db.GetTopPlayed(gctx, days)
		return err
	})
	g.Go(func() (err error) {
		recentPlays, err = h.db.GetRecentPlays(gctx)
		return err
	})
	g.Go(func() (err error) {
		topUsers, err = h.db.GetTopUsers(gctx, days)
		return err
	})
	g.Go(func() (err error) {
		clients, err = h.db.GetClientBreakdown(gctx, days)
		return err
	})
	g.Go(func() (err error) {
		playsByHour, err = h.db.GetPlaysByHour(gctx, gen.GetPlaysByHourParams{Tz: tz, Days: days})
		return err
	})
	g.Go(func() (err error) {
		completion, err = h.db.GetCompletionStats(gctx, days)
		return err
	})
	g.Go(func() (err error) {
		streamTypes, err = h.db.GetStreamTypesPerDay(gctx, gen.GetStreamTypesPerDayParams{Tz: tz, Days: days})
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}

	// ── Map to response types ─────────────────────────────────────────────────

	respLibs := make([]libraryAnalytics, len(libs))
	for i, l := range libs {
		respLibs[i] = libraryAnalytics{
			ID:             l.ID.String(),
			Name:           l.Name,
			Type:           l.Type,
			ItemCount:      l.ItemCount,
			TotalSizeBytes: l.TotalSizeBytes,
			Res4K:          l.Res4k,
			Res1080p:       l.Res1080p,
			Res720p:        l.Res720p,
			ResSD:          l.ResSd,
		}
	}

	respCodecs := make([]codecCount, len(codecs))
	for i, c := range codecs {
		respCodecs[i] = codecCount{Codec: c.Codec, Count: c.Count}
	}

	respContainers := make([]containerCount, len(containers))
	for i, c := range containers {
		respContainers[i] = containerCount{Container: c.Container, Count: c.Count}
	}

	respDays := make([]dayCount, len(playsPerDay))
	for i, d := range playsPerDay {
		respDays[i] = dayCount{Date: d.Date.Time.Format("2006-01-02"), Count: d.Count}
	}

	respBandwidth := make([]dayBytes, len(bandwidthPerDay))
	for i, d := range bandwidthPerDay {
		respBandwidth[i] = dayBytes{Date: d.Date.Time.Format("2006-01-02"), Bytes: d.Bytes}
	}

	respTop := make([]topPlayedItem, len(topPlayed))
	for i, t := range topPlayed {
		item := topPlayedItem{
			ID:        t.ID.String(),
			Title:     t.Title,
			Type:      t.Type,
			PlayCount: t.PlayCount,
		}
		if t.Year != nil {
			y := int(*t.Year)
			item.Year = &y
		}
		if t.PosterPath != "" {
			pp := t.PosterPath
			item.PosterPath = &pp
		}
		if t.ParentTitle != "" {
			pt := t.ParentTitle
			item.ParentTitle = &pt
		}
		respTop[i] = item
	}

	respRecent := make([]recentPlay, len(recentPlays))
	for i, p := range recentPlays {
		play := recentPlay{
			Title: p.Title,
			Type:  p.Type,
			// .UTC() before formatting: pgx returns timestamptz in the server's
			// local zone, and a literal Z on local wall-clock time shifted every
			// displayed timestamp by the UTC offset.
			OccurredAt: p.OccurredAt.Time.UTC().Format(time.RFC3339),
			UserName:   p.UserName,
			ClientName: p.ClientName,
			DurationMS: p.DurationMs,
		}
		if p.Year != nil {
			y := int(*p.Year)
			play.Year = &y
		}
		if p.ParentTitle != "" {
			pt := p.ParentTitle
			play.ParentTitle = &pt
		}
		respRecent[i] = play
	}

	respUsers := make([]topUser, len(topUsers))
	for i, u := range topUsers {
		name := u.Username
		if name == "" {
			name = "Deleted user"
		}
		respUsers[i] = topUser{
			UserID:      u.UserID.String(),
			Username:    name,
			PlayCount:   u.PlayCount,
			WatchTimeMS: u.WatchTimeMs,
		}
	}

	respClients := make([]clientCount, len(clients))
	for i, c := range clients {
		respClients[i] = clientCount{Client: c.Client, Count: c.Count}
	}

	respHours := make([]hourCount, len(playsByHour))
	for i, hr := range playsByHour {
		respHours[i] = hourCount{Hour: int(hr.Hour), Count: hr.Count}
	}

	// Collapse the decision dimension into direct / transcode / unknown per
	// day — directStream and remux are "the original bits reached the client",
	// which is the capacity question the chart answers.
	streamByDate := map[string]*dayStreamTypes{}
	var streamOrder []string
	for _, st := range streamTypes {
		date := st.Date.Time.Format("2006-01-02")
		row, ok := streamByDate[date]
		if !ok {
			row = &dayStreamTypes{Date: date}
			streamByDate[date] = row
			streamOrder = append(streamOrder, date)
		}
		switch st.Decision {
		case "directPlay", "directStream", "remux":
			row.Direct += st.Count
		case "transcode":
			row.Transcode += st.Count
		default:
			row.Unknown += st.Count
		}
	}
	respStreamTypes := make([]dayStreamTypes, len(streamOrder))
	for i, date := range streamOrder {
		respStreamTypes[i] = *streamByDate[date]
	}

	return &analyticsResponse{
		RangeDays: int(days),
		Overview: analyticsOverview{
			TotalItems:       overview.TotalItems,
			TotalFiles:       overview.TotalFiles,
			TotalSizeBytes:   overview.TotalSizeBytes,
			TotalPlays:       overview.TotalPlays,
			TotalWatchTimeMS: overview.TotalWatchTimeMs,
		},
		Libraries:        respLibs,
		VideoCodecs:      respCodecs,
		Containers:       respContainers,
		PlaysByDay:       respDays,
		BandwidthByDay:   respBandwidth,
		TopPlayed:        respTop,
		RecentPlays:      respRecent,
		TopUsers:         respUsers,
		Clients:          respClients,
		PlaysByHour:      respHours,
		Completion:       completionStats{Plays: completion.PlaysWithDuration, Completed: completion.Completed},
		StreamTypesByDay: respStreamTypes,
	}, nil
}
