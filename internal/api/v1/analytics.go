package v1

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// analyticsQuerier is the DB subset needed by AnalyticsHandler.
type analyticsQuerier interface {
	GetAnalyticsOverview(ctx context.Context) (gen.AnalyticsOverviewRow, error)
	GetLibraryAnalytics(ctx context.Context) ([]gen.LibraryAnalyticsRow, error)
	GetVideoCodecBreakdown(ctx context.Context) ([]gen.CodecCountRow, error)
	GetContainerBreakdown(ctx context.Context) ([]gen.ContainerCountRow, error)
	GetPlaysPerDay(ctx context.Context) ([]gen.DayCountRow, error)
	GetBandwidthPerDay(ctx context.Context) ([]gen.DayBytesRow, error)
	GetTopPlayed(ctx context.Context) ([]gen.TopPlayedRow, error)
	GetRecentPlays(ctx context.Context) ([]gen.RecentPlayRow, error)
}

// AnalyticsHandler handles GET /api/v1/analytics.
type AnalyticsHandler struct {
	db     analyticsQuerier
	logger *slog.Logger
}

// NewAnalyticsHandler creates an AnalyticsHandler.
func NewAnalyticsHandler(db analyticsQuerier, logger *slog.Logger) *AnalyticsHandler {
	return &AnalyticsHandler{db: db, logger: logger}
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
	Date  string `json:"date"`  // "2006-01-02"
	Count int64  `json:"count"`
}

type dayBytes struct {
	Date  string `json:"date"`  // "2006-01-02"
	Bytes int64  `json:"bytes"`
}

type topPlayedItem struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Year       *int    `json:"year,omitempty"`
	Type       string  `json:"type"`
	PosterPath *string `json:"poster_path,omitempty"`
	PlayCount  int64   `json:"play_count"`
}

type recentPlay struct {
	Title      string  `json:"title"`
	Year       *int    `json:"year,omitempty"`
	Type       string  `json:"type"`
	OccurredAt string  `json:"occurred_at"`
	ClientName *string `json:"client_name,omitempty"`
	DurationMS *int64  `json:"duration_ms,omitempty"`
}

type analyticsResponse struct {
	Overview       analyticsOverview  `json:"overview"`
	Libraries      []libraryAnalytics `json:"libraries"`
	VideoCodecs    []codecCount       `json:"video_codecs"`
	Containers     []containerCount   `json:"containers"`
	PlaysByDay     []dayCount         `json:"plays_by_day"`
	BandwidthByDay []dayBytes         `json:"bandwidth_by_day"`
	TopPlayed      []topPlayedItem    `json:"top_played"`
	RecentPlays    []recentPlay       `json:"recent_plays"`
}

// Get handles GET /api/v1/analytics.
func (h *AnalyticsHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	overview, err := h.db.GetAnalyticsOverview(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "analytics: overview", "err", err)
		respond.InternalError(w, r)
		return
	}

	libs, err := h.db.GetLibraryAnalytics(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "analytics: libraries", "err", err)
		respond.InternalError(w, r)
		return
	}

	codecs, err := h.db.GetVideoCodecBreakdown(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "analytics: codecs", "err", err)
		respond.InternalError(w, r)
		return
	}

	containers, err := h.db.GetContainerBreakdown(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "analytics: containers", "err", err)
		respond.InternalError(w, r)
		return
	}

	playsPerDay, err := h.db.GetPlaysPerDay(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "analytics: plays per day", "err", err)
		respond.InternalError(w, r)
		return
	}

	bandwidthPerDay, err := h.db.GetBandwidthPerDay(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "analytics: bandwidth per day", "err", err)
		respond.InternalError(w, r)
		return
	}

	topPlayed, err := h.db.GetTopPlayed(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "analytics: top played", "err", err)
		respond.InternalError(w, r)
		return
	}

	recentPlays, err := h.db.GetRecentPlays(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "analytics: recent plays", "err", err)
		respond.InternalError(w, r)
		return
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
			Res4K:          l.Res4K,
			Res1080p:       l.Res1080p,
			Res720p:        l.Res720p,
			ResSD:          l.ResSD,
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
		respDays[i] = dayCount{Date: d.Date.Format("2006-01-02"), Count: d.Count}
	}

	respBandwidth := make([]dayBytes, len(bandwidthPerDay))
	for i, d := range bandwidthPerDay {
		respBandwidth[i] = dayBytes{Date: d.Date.Format("2006-01-02"), Bytes: d.Bytes}
	}

	respTop := make([]topPlayedItem, len(topPlayed))
	for i, t := range topPlayed {
		item := topPlayedItem{
			ID:        t.ID.String(),
			Title:     t.Title,
			Type:      t.Type,
			PlayCount: t.PlayCount,
		}
		if t.Year.Valid {
			y := int(t.Year.Int32)
			item.Year = &y
		}
		if t.PosterPath.Valid {
			item.PosterPath = &t.PosterPath.String
		}
		respTop[i] = item
	}

	respRecent := make([]recentPlay, len(recentPlays))
	for i, p := range recentPlays {
		play := recentPlay{
			Title:      p.Title,
			Type:       p.Type,
			OccurredAt: p.OccurredAt.Format("2006-01-02T15:04:05Z"),
		}
		if p.Year.Valid {
			y := int(p.Year.Int32)
			play.Year = &y
		}
		if p.ClientName.Valid {
			play.ClientName = &p.ClientName.String
		}
		if p.DurationMS.Valid {
			play.DurationMS = &p.DurationMS.Int64
		}
		respRecent[i] = play
	}

	respond.Success(w, r, analyticsResponse{
		Overview: analyticsOverview{
			TotalItems:       overview.TotalItems,
			TotalFiles:       overview.TotalFiles,
			TotalSizeBytes:   overview.TotalSizeBytes,
			TotalPlays:       overview.TotalPlays,
			TotalWatchTimeMS: overview.TotalWatchTimeMS,
		},
		Libraries:   respLibs,
		VideoCodecs: respCodecs,
		Containers:  respContainers,
		PlaysByDay:     respDays,
		BandwidthByDay: respBandwidth,
		TopPlayed:      respTop,
		RecentPlays: respRecent,
	})
}
