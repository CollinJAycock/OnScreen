// Package scanner - enricher wires the TMDB metadata agent and artwork manager
// into the MetadataAgent interface consumed by the scanner.
package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/metadata"
)

// ItemUpdater saves enriched metadata back to the database.
type ItemUpdater interface {
	UpdateItemMetadata(ctx context.Context, p media.UpdateItemMetadataParams) (*media.Item, error)
	GetItem(ctx context.Context, id uuid.UUID) (*media.Item, error)
	GetFiles(ctx context.Context, itemID uuid.UUID) ([]media.File, error)
	ListChildren(ctx context.Context, parentID uuid.UUID) ([]media.Item, error)
}

// ArtworkFetcher downloads artwork files and returns their relative paths.
// mediaDir is a directory path relative to the artwork manager's mediaPath root.
type ArtworkFetcher interface {
	DownloadPoster(ctx context.Context, itemID uuid.UUID, url, mediaDir string) (string, error)
	DownloadFanart(ctx context.Context, itemID uuid.UUID, url, mediaDir string) (string, error)
	DownloadThumb(ctx context.Context, itemID uuid.UUID, url, mediaDir string) (string, error)
	// ReplacePoster writes url to mediaDir/poster.jpg even if the file
	// already exists. Used by the music enricher to override embedded album
	// art with AudioDB's authoritative cover.
	ReplacePoster(ctx context.Context, itemID uuid.UUID, url, mediaDir string) (string, error)
}

// TVDBFallback provides episode + show metadata from TheTVDB as a fallback when
// TMDB lookups fail or return no art (anime, niche shows absent from TMDB, or
// TMDB matches that have no poster).
type TVDBFallback interface {
	GetEpisode(ctx context.Context, tvdbSeriesID, seasonNum, episodeNum int) (*metadata.EpisodeResult, error)
	SearchSeries(ctx context.Context, title string, year int) (*metadata.TVShowResult, error)
}

// ScanPathsProvider returns all active library scan paths so the enricher
// can convert absolute artwork paths to paths relative to the library root.
type ScanPathsProvider func() []string

// Enricher implements MetadataAgent using the TMDB metadata provider and
// the artwork manager. Returning nil from agentFn disables enrichment —
// this lets the TMDB key be set at runtime via server settings without restart.
type Enricher struct {
	agentFn      func() metadata.Agent      // returns nil when no key is configured
	tvdbFn       func() TVDBFallback        // returns nil when no key is configured
	musicAgentFn func() metadata.MusicAgent // returns nil when not configured
	artwork      ArtworkFetcher
	updater      ItemUpdater
	scanPaths    ScanPathsProvider
	logger       *slog.Logger
}

// NewEnricher creates an Enricher.
// agentFn is called on each new file — return nil to skip enrichment.
// scanPaths returns all library scan_paths for relative-path computation.
func NewEnricher(
	agentFn func() metadata.Agent,
	art ArtworkFetcher,
	updater ItemUpdater,
	scanPaths ScanPathsProvider,
	logger *slog.Logger,
) *Enricher {
	return &Enricher{
		agentFn:   agentFn,
		artwork:   art,
		updater:   updater,
		scanPaths: scanPaths,
		logger:    logger,
	}
}

// SetTVDBFallbackFn sets the lazy factory for the optional TVDB fallback client.
// The function is called per enrichment — return nil to skip TVDB.
func (e *Enricher) SetTVDBFallbackFn(fn func() TVDBFallback) {
	e.tvdbFn = fn
}

// SetMusicAgentFn sets the lazy factory for the music metadata client.
// The function is called per enrichment — return nil to skip music enrichment.
func (e *Enricher) SetMusicAgentFn(fn func() metadata.MusicAgent) {
	e.musicAgentFn = fn
}

// Enrich implements MetadataAgent. It's called for newly discovered files.
// Errors are logged but never propagated — a metadata failure must not abort a scan.
func (e *Enricher) Enrich(ctx context.Context, item *media.Item, file *media.File) error {
	agent := e.agentFn()
	if agent == nil {
		return nil
	}
	switch item.Type {
	case "movie":
		return e.enrichMovie(ctx, agent, item, file)
	case "show":
		return e.enrichShow(ctx, agent, item, file)
	case "season":
		return e.enrichSeason(ctx, agent, item, file)
	case "episode":
		return e.enrichEpisode(ctx, agent, item, file)
	case "artist", "album", "track":
		if e.musicAgentFn != nil {
			if ma := e.musicAgentFn(); ma != nil {
				return e.enrichMusicItem(ctx, ma, item, file)
			}
		}
		return nil
	default:
		return nil
	}
}

func (e *Enricher) enrichMovie(ctx context.Context, agent metadata.Agent, item *media.Item, file *media.File) error {
	// Clean the stored title before searching: items scanned before this fix
	// may have filenames like "Movie_Title_2009_1080p_WEB-DL" stored verbatim.
	searchTitle, extractedYear := cleanTitle(item.Title)
	year := 0
	if item.Year != nil {
		year = *item.Year
	}
	if year == 0 && extractedYear != nil {
		year = *extractedYear
	}

	result, err := agent.SearchMovie(ctx, searchTitle, year)
	if err != nil || result == nil {
		// No result or API error — not a scan-blocking error.
		e.logger.InfoContext(ctx, "tmdb search found no result",
			"title", item.Title, "year", year, "err", err)
		return nil
	}

	p := media.UpdateItemMetadataParams{
		ID:        item.ID,
		Title:     result.Title,
		SortTitle: result.Title,
	}

	if result.OriginalTitle != "" {
		p.OriginalTitle = &result.OriginalTitle
	}
	if result.Year != 0 {
		p.Year = &result.Year
	}
	if result.Summary != "" {
		p.Summary = &result.Summary
	}
	if result.Tagline != "" {
		p.Tagline = &result.Tagline
	}
	if result.Rating != 0 {
		p.Rating = &result.Rating
	}
	if result.ContentRating != "" {
		p.ContentRating = &result.ContentRating
	}
	if result.DurationMS != 0 {
		p.DurationMS = &result.DurationMS
	}
	if len(result.Genres) > 0 {
		p.Genres = result.Genres
	}
	if !result.ReleaseDate.IsZero() {
		p.OriginallyAvailableAt = &result.ReleaseDate
	}

	// Download artwork next to the media file.
	artDir := filepath.Dir(file.FilePath)
	if e.artwork != nil && artDir != "" && artDir != "." {
		if result.PosterURL != "" {
			absPath, err := e.artwork.DownloadPoster(ctx, item.ID, result.PosterURL, artDir)
			if err != nil {
				e.logger.WarnContext(ctx, "poster download failed",
					"item_id", item.ID, "err", err)
			} else {
				rel := e.relPath(absPath)
				p.PosterPath = &rel
			}
		}
		if result.FanartURL != "" {
			absPath, err := e.artwork.DownloadFanart(ctx, item.ID, result.FanartURL, artDir)
			if err != nil {
				e.logger.WarnContext(ctx, "fanart download failed",
					"item_id", item.ID, "err", err)
			} else {
				rel := e.relPath(absPath)
				p.FanartPath = &rel
			}
		}
	}

	if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
		return fmt.Errorf("update item metadata: %w", err)
	}

	e.logger.InfoContext(ctx, "item enriched",
		"item_id", item.ID,
		"title", result.Title,
		"tmdb_id", result.TMDBID,
		"has_poster", p.PosterPath != nil,
	)
	return nil
}

// enrichShow searches TMDB for the show and updates metadata, poster, and fanart.
func (e *Enricher) enrichShow(ctx context.Context, agent metadata.Agent, item *media.Item, file *media.File) error {
	searchTitle, extractedYear := cleanTitle(item.Title)
	year := 0
	if item.Year != nil {
		year = *item.Year
	}
	if year == 0 && extractedYear != nil {
		year = *extractedYear
	}

	result, err := agent.SearchTV(ctx, searchTitle, year)
	if err != nil || result == nil {
		e.logger.InfoContext(ctx, "tmdb tv search found no result",
			"title", item.Title, "err", err)
		// No TMDB match — try TVDB outright.
		result = e.tvdbShowFallback(ctx, searchTitle, year, nil)
		if result == nil {
			return nil
		}
	} else if result.PosterURL == "" {
		// TMDB matched but has no poster — ask TVDB for art while keeping
		// TMDB's other fields.
		if merged := e.tvdbShowFallback(ctx, searchTitle, year, result); merged != nil {
			result = merged
		}
	}

	p := media.UpdateItemMetadataParams{
		ID:        item.ID,
		Title:     result.Title,
		SortTitle: result.Title,
	}
	if result.TMDBID != 0 {
		tmdbID := result.TMDBID
		p.TMDBID = &tmdbID
	}
	if result.TVDBID != 0 {
		tvdbID := result.TVDBID
		p.TVDBID = &tvdbID
	}

	if result.OriginalTitle != "" {
		p.OriginalTitle = &result.OriginalTitle
	}
	if result.FirstAirYear != 0 {
		p.Year = &result.FirstAirYear
	}
	if result.Summary != "" {
		p.Summary = &result.Summary
	}
	if result.Rating != 0 {
		p.Rating = &result.Rating
	}
	if result.ContentRating != "" {
		p.ContentRating = &result.ContentRating
	}
	if len(result.Genres) > 0 {
		p.Genres = result.Genres
	}

	// Download artwork next to the media files. For a show, go up to the
	// show root directory (parent of the season dir containing the episode file).
	artDir := showDirFromFile(file.FilePath)
	if e.artwork != nil && artDir != "" && artDir != "." {
		if result.PosterURL != "" {
			absPath, err := e.artwork.DownloadPoster(ctx, item.ID, result.PosterURL, artDir)
			if err != nil {
				e.logger.WarnContext(ctx, "show poster download failed",
					"item_id", item.ID, "err", err)
			} else {
				rel := e.relPath(absPath)
				p.PosterPath = &rel
			}
		}
		if result.FanartURL != "" {
			absPath, err := e.artwork.DownloadFanart(ctx, item.ID, result.FanartURL, artDir)
			if err != nil {
				e.logger.WarnContext(ctx, "show fanart download failed",
					"item_id", item.ID, "err", err)
			} else {
				rel := e.relPath(absPath)
				p.FanartPath = &rel
			}
		}
	}

	if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
		return fmt.Errorf("update show metadata: %w", err)
	}

	e.logger.InfoContext(ctx, "show enriched",
		"item_id", item.ID,
		"title", result.Title,
		"tmdb_id", result.TMDBID,
		"has_poster", p.PosterPath != nil,
	)

	// After enriching the show, trigger enrichment for its seasons.
	e.enrichShowChildren(ctx, agent, item, file)

	return nil
}

// enrichShowChildren enriches all seasons (and their episodes) under a show
// after the show itself has been enriched. This ensures that when a show is
// first scanned, all its seasons and episodes also get TMDB metadata.
func (e *Enricher) enrichShowChildren(ctx context.Context, agent metadata.Agent, show *media.Item, file *media.File) {
	// Re-load the show to pick up the TMDB ID we just saved.
	show, err := e.updater.GetItem(ctx, show.ID)
	if err != nil || show.TMDBID == nil {
		return
	}

	seasons, err := e.updater.ListChildren(ctx, show.ID)
	if err != nil {
		return
	}
	for i := range seasons {
		s := &seasons[i]
		if s.Type != "season" || s.PosterPath != nil {
			continue
		}
		if err := e.enrichSeason(ctx, agent, s, file); err != nil {
			e.logger.WarnContext(ctx, "season enrich in cascade failed",
				"season_id", s.ID, "err", err)
		}
	}
}

// enrichSeason fetches season metadata from TMDB using the parent show's TMDB ID.
func (e *Enricher) enrichSeason(ctx context.Context, agent metadata.Agent, item *media.Item, file *media.File) error {
	if item.ParentID == nil || item.Index == nil {
		return nil
	}

	// Load parent show to get TMDB ID.
	show, err := e.updater.GetItem(ctx, *item.ParentID)
	if err != nil {
		return fmt.Errorf("get parent show for season: %w", err)
	}
	if show.TMDBID == nil {
		// Show hasn't been enriched yet; season enrichment will happen when
		// the show is enriched (via enrichShowChildren).
		return nil
	}

	result, err := agent.GetSeason(ctx, *show.TMDBID, *item.Index)
	if err != nil {
		e.logger.InfoContext(ctx, "tmdb get season found no result",
			"show_tmdb_id", *show.TMDBID, "season", *item.Index, "err", err)
		return nil
	}

	p := media.UpdateItemMetadataParams{
		ID:        item.ID,
		Title:     result.Name,
		SortTitle: result.Name,
	}
	if result.Summary != "" {
		p.Summary = &result.Summary
	}
	if !result.AirDate.IsZero() {
		p.OriginallyAvailableAt = &result.AirDate
	}

	// Download season poster next to the episode files (season directory).
	artDir := filepath.Dir(file.FilePath)
	if e.artwork != nil && result.PosterURL != "" && artDir != "" && artDir != "." {
		absPath, err := e.artwork.DownloadPoster(ctx, item.ID, result.PosterURL, artDir)
		if err != nil {
			e.logger.WarnContext(ctx, "season poster download failed",
				"item_id", item.ID, "err", err)
		} else {
			rel := e.relPath(absPath)
			p.PosterPath = &rel
		}
	}

	if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
		return fmt.Errorf("update season metadata: %w", err)
	}

	e.logger.InfoContext(ctx, "season enriched",
		"item_id", item.ID,
		"title", result.Name,
		"season_num", result.Number,
	)

	// Cascade: enrich episodes under this season.
	e.enrichSeasonChildren(ctx, agent, show, item, file)

	return nil
}

// enrichSeasonChildren enriches all episodes under a season after the season
// has been enriched.
func (e *Enricher) enrichSeasonChildren(ctx context.Context, agent metadata.Agent, show *media.Item, season *media.Item, file *media.File) {
	if show.TMDBID == nil || season.Index == nil {
		return
	}

	episodes, err := e.updater.ListChildren(ctx, season.ID)
	if err != nil {
		return
	}
	for i := range episodes {
		ep := &episodes[i]
		if ep.Type != "episode" {
			continue
		}
		// Skip episodes that already have both metadata and artwork.
		if ep.Summary != nil && ep.ThumbPath != nil {
			continue
		}
		if err := e.enrichEpisode(ctx, agent, ep, file); err != nil {
			e.logger.WarnContext(ctx, "episode enrich in cascade failed",
				"episode_id", ep.ID, "err", err)
		}
	}
}

// enrichEpisode fetches episode metadata from TMDB using the grandparent
// show's TMDB ID and the season/episode indices.
func (e *Enricher) enrichEpisode(ctx context.Context, agent metadata.Agent, item *media.Item, file *media.File) error {
	if item.ParentID == nil || item.Index == nil {
		return nil
	}

	// Load parent season.
	season, err := e.updater.GetItem(ctx, *item.ParentID)
	if err != nil {
		return fmt.Errorf("get parent season for episode: %w", err)
	}
	if season.ParentID == nil || season.Index == nil {
		return nil
	}

	// Load grandparent show.
	show, err := e.updater.GetItem(ctx, *season.ParentID)
	if err != nil {
		return fmt.Errorf("get grandparent show for episode: %w", err)
	}
	if show.TMDBID == nil || show.PosterPath == nil {
		// Show hasn't been enriched yet, or is missing artwork — enrich it.
		// enrichShow will cascade down to seasons and episodes.
		if err := e.enrichShow(ctx, agent, show, file); err != nil {
			e.logger.WarnContext(ctx, "cascade show enrich from episode failed",
				"show_id", show.ID, "err", err)
		}
		// Re-load show to pick up TMDB ID.
		show, err = e.updater.GetItem(ctx, show.ID)
		if err != nil || show.TMDBID == nil {
			return nil // show enrichment failed or no TMDB match
		}
	}

	result, err := agent.GetEpisode(ctx, *show.TMDBID, *season.Index, *item.Index)
	if err != nil {
		e.logger.InfoContext(ctx, "tmdb get episode found no result",
			"show_tmdb_id", *show.TMDBID,
			"season", *season.Index,
			"episode", *item.Index,
			"err", err)

		// Fall back to TVDB if available and the show has a TVDB ID.
		var tvdbClient TVDBFallback
		if e.tvdbFn != nil {
			tvdbClient = e.tvdbFn()
		}
		if tvdbClient != nil && show.TVDBID != nil {
			result, err = tvdbClient.GetEpisode(ctx, *show.TVDBID, *season.Index, *item.Index)
			if err != nil {
				e.logger.InfoContext(ctx, "tvdb fallback also found no result",
					"show_tvdb_id", *show.TVDBID,
					"season", *season.Index,
					"episode", *item.Index,
					"err", err)
				return nil
			}
			e.logger.InfoContext(ctx, "tvdb fallback found episode",
				"show_tvdb_id", *show.TVDBID,
				"season", *season.Index,
				"episode", *item.Index,
				"title", result.Title)
		} else {
			return nil
		}
	}

	p := media.UpdateItemMetadataParams{
		ID:        item.ID,
		Title:     result.Title,
		SortTitle: result.Title,
	}
	if result.Summary != "" {
		p.Summary = &result.Summary
	}
	if result.Rating != 0 {
		p.Rating = &result.Rating
	}
	if !result.AirDate.IsZero() {
		p.OriginallyAvailableAt = &result.AirDate
	}

	// Download episode thumb next to the episode file.
	artDir := filepath.Dir(file.FilePath)
	if e.artwork != nil && result.ThumbURL != "" && artDir != "" && artDir != "." {
		absPath, err := e.artwork.DownloadThumb(ctx, item.ID, result.ThumbURL, artDir)
		if err != nil {
			e.logger.WarnContext(ctx, "episode thumb download failed",
				"item_id", item.ID, "err", err)
		} else {
			rel := e.relPath(absPath)
			p.ThumbPath = &rel
		}
	}

	if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
		return fmt.Errorf("update episode metadata: %w", err)
	}

	e.logger.InfoContext(ctx, "episode enriched",
		"item_id", item.ID,
		"title", result.Title,
		"season", *season.Index,
		"episode", *item.Index,
	)
	return nil
}

// enrichMusicItem enriches an artist or album item using a MusicAgent.
// For artists it fetches an artist photo and biography.
// For albums it fetches cover art, description, year, and genre.
// The file path is used to determine the directory for artwork storage.
func (e *Enricher) enrichMusicItem(ctx context.Context, agent metadata.MusicAgent, item *media.Item, file *media.File) error {
	// Determine the directory where artwork should be stored.
	// For albums the file sits in the album dir; for artists it's one level up.
	artDir := ""
	if file != nil {
		dir := filepath.Dir(file.FilePath)
		if item.Type == "artist" {
			dir = filepath.Dir(dir) // go up past the album dir
		}
		artDir = dir
	}

	switch item.Type {
	case "track":
		// Tracks themselves are not enriched; walk up and enrich album then artist.
		if item.ParentID == nil {
			return nil
		}
		album, err := e.updater.GetItem(ctx, *item.ParentID)
		if err != nil || album == nil {
			return nil
		}
		albumDir := ""
		if file != nil {
			albumDir = filepath.Dir(file.FilePath)
		}
		// Run album enrichment whenever AudioDB hasn't successfully returned
		// metadata yet (Summary is set only by enrichAlbum). This lets
		// AudioDB override wrong embedded album art, which the scanner wrote
		// as poster.jpg before enrichment could provide a better cover.
		if album.Summary == nil {
			if err := e.enrichAlbum(ctx, agent, album, albumDir); err != nil {
				e.logger.WarnContext(ctx, "album enrich failed", "album_id", album.ID, "err", err)
			}
		}
		if album.ParentID == nil {
			return nil
		}
		artist, err := e.updater.GetItem(ctx, *album.ParentID)
		if err != nil || artist == nil {
			return nil
		}
		artistDir := ""
		if file != nil {
			artistDir = filepath.Dir(filepath.Dir(file.FilePath))
		}
		if artist.PosterPath == nil {
			if err := e.enrichArtist(ctx, agent, artist, artistDir); err != nil {
				e.logger.WarnContext(ctx, "artist enrich failed", "artist_id", artist.ID, "err", err)
			}
		}
		return nil

	case "artist":
		return e.enrichArtist(ctx, agent, item, artDir)

	case "album":
		return e.enrichAlbum(ctx, agent, item, artDir)
	}
	return nil
}

func (e *Enricher) enrichArtist(ctx context.Context, agent metadata.MusicAgent, item *media.Item, artDir string) error {
	result, err := agent.SearchArtist(ctx, item.Title)
	if err != nil {
		return fmt.Errorf("audiodb search artist %q: %w", item.Title, err)
	}
	if result == nil {
		return nil
	}
	p := media.UpdateItemMetadataParams{
		ID:        item.ID,
		Title:     item.Title,
		SortTitle: item.SortTitle,
	}
	if result.Biography != "" {
		p.Summary = &result.Biography
	}
	if result.ThumbURL != "" && artDir != "" && e.artwork != nil {
		if abs, err := e.artwork.DownloadPoster(ctx, item.ID, result.ThumbURL, artDir); err == nil {
			rel := e.relPath(abs)
			p.PosterPath = &rel
		}
	}
	if result.FanartURL != "" && artDir != "" && e.artwork != nil {
		if abs, err := e.artwork.DownloadFanart(ctx, item.ID, result.FanartURL, artDir); err == nil {
			rel := e.relPath(abs)
			p.FanartPath = &rel
		}
	}
	if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
		return fmt.Errorf("update artist metadata: %w", err)
	}
	e.logger.InfoContext(ctx, "artist enriched", "item_id", item.ID, "name", item.Title)
	return nil
}

func (e *Enricher) enrichAlbum(ctx context.Context, agent metadata.MusicAgent, item *media.Item, artDir string) error {
	artistName := ""
	if item.ParentID != nil {
		if parent, err := e.updater.GetItem(ctx, *item.ParentID); err == nil {
			artistName = parent.Title
		}
	}
	result, err := agent.SearchAlbum(ctx, artistName, item.Title)
	if err != nil {
		return fmt.Errorf("audiodb search album %q/%q: %w", artistName, item.Title, err)
	}
	if result == nil {
		return nil
	}
	p := media.UpdateItemMetadataParams{
		ID:        item.ID,
		Title:     item.Title,
		SortTitle: item.SortTitle,
		Year:      item.Year,
		Genres:    item.Genres,
	}
	if result.Description != "" {
		p.Summary = &result.Description
	}
	if result.Year > 0 {
		p.Year = &result.Year
	}
	if len(result.Genres) > 0 {
		p.Genres = result.Genres
	}
	if result.ThumbURL != "" && artDir != "" && e.artwork != nil {
		// Overwrite poster.jpg — it may be wrong embedded art from the scan.
		if abs, err := e.artwork.ReplacePoster(ctx, item.ID, result.ThumbURL, artDir); err == nil {
			rel := e.relPath(abs)
			p.PosterPath = &rel
		}
	}
	if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
		return fmt.Errorf("update album metadata: %w", err)
	}
	e.logger.InfoContext(ctx, "album enriched", "item_id", item.ID, "title", item.Title)
	return nil
}

// EnrichItem re-runs metadata enrichment for a single item by ID.
// Implements the v1.ItemEnricher interface for on-demand metadata refresh.
func (e *Enricher) EnrichItem(ctx context.Context, itemID uuid.UUID) error {
	item, err := e.updater.GetItem(ctx, itemID)
	if err != nil {
		return fmt.Errorf("get item %s: %w", itemID, err)
	}
	files, err := e.updater.GetFiles(ctx, itemID)
	if err != nil {
		return fmt.Errorf("get files %s: %w", itemID, err)
	}
	// Pick the first active file for the artwork directory hint.
	var file *media.File
	for i := range files {
		if files[i].Status == "active" {
			file = &files[i]
			break
		}
	}
	if file == nil {
		return fmt.Errorf("no active file for item %s", itemID)
	}
	return e.Enrich(ctx, item, file)
}

// MatchItem applies a specific TMDB ID to a show or movie, re-enriches it with
// RefreshTV/RefreshMovie, and cascades to children. This is used by the manual
// "Fix Match" feature when the automatic search picked the wrong result.
func (e *Enricher) MatchItem(ctx context.Context, itemID uuid.UUID, tmdbID int) error {
	agent := e.agentFn()
	if agent == nil {
		return fmt.Errorf("metadata agent not configured")
	}

	item, err := e.updater.GetItem(ctx, itemID)
	if err != nil {
		return fmt.Errorf("get item %s: %w", itemID, err)
	}

	files, err := e.updater.GetFiles(ctx, itemID)
	if err != nil {
		return fmt.Errorf("get files %s: %w", itemID, err)
	}
	var file *media.File
	for i := range files {
		if files[i].Status == "active" {
			file = &files[i]
			break
		}
	}
	// Shows/seasons may not have direct files — find one from a descendant.
	if file == nil {
		file = e.findDescendantFile(ctx, item.ID)
	}
	if file == nil {
		return fmt.Errorf("no active file for item %s or its descendants", itemID)
	}

	switch item.Type {
	case "show":
		return e.matchShow(ctx, agent, item, file, tmdbID)
	case "movie":
		return e.matchMovie(ctx, agent, item, file, tmdbID)
	default:
		return fmt.Errorf("match not supported for item type %q", item.Type)
	}
}

// matchShow re-enriches a show using RefreshTV with a specific TMDB ID, then
// cascades enrichment to all seasons and episodes.
func (e *Enricher) matchShow(ctx context.Context, agent metadata.Agent, item *media.Item, file *media.File, tmdbID int) error {
	result, err := agent.RefreshTV(ctx, tmdbID)
	if err != nil {
		return fmt.Errorf("refresh tv %d: %w", tmdbID, err)
	}

	p := media.UpdateItemMetadataParams{
		ID:        item.ID,
		Title:     result.Title,
		SortTitle: result.Title,
		TMDBID:    &tmdbID,
	}
	if result.TVDBID != 0 {
		tvdbID := result.TVDBID
		p.TVDBID = &tvdbID
	}
	if result.OriginalTitle != "" {
		p.OriginalTitle = &result.OriginalTitle
	}
	if result.FirstAirYear != 0 {
		p.Year = &result.FirstAirYear
	}
	if result.Summary != "" {
		p.Summary = &result.Summary
	}
	if result.Rating != 0 {
		p.Rating = &result.Rating
	}
	if result.ContentRating != "" {
		p.ContentRating = &result.ContentRating
	}
	if len(result.Genres) > 0 {
		p.Genres = result.Genres
	}

	// Download artwork next to the media files (show root directory).
	artDir := showDirFromFile(file.FilePath)
	e.logger.DebugContext(ctx, "match artwork download",
		"item_id", item.ID, "art_dir", artDir,
		"poster_url", result.PosterURL, "fanart_url", result.FanartURL)
	if e.artwork != nil && artDir != "" && artDir != "." {
		if result.PosterURL != "" {
			absPath, dlErr := e.artwork.DownloadPoster(ctx, item.ID, result.PosterURL, artDir)
			if dlErr != nil {
				e.logger.WarnContext(ctx, "match poster download failed",
					"item_id", item.ID, "err", dlErr)
			} else {
				rel := e.relPath(absPath)
				p.PosterPath = &rel
			}
		}
		if result.FanartURL != "" {
			absPath, dlErr := e.artwork.DownloadFanart(ctx, item.ID, result.FanartURL, artDir)
			if dlErr != nil {
				e.logger.WarnContext(ctx, "match fanart download failed",
					"item_id", item.ID, "err", dlErr)
			} else {
				rel := e.relPath(absPath)
				p.FanartPath = &rel
			}
		}
	}

	if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
		return fmt.Errorf("update show metadata: %w", err)
	}

	e.logger.InfoContext(ctx, "show matched",
		"item_id", item.ID, "title", result.Title, "tmdb_id", tmdbID,
		"has_poster", p.PosterPath != nil, "has_fanart", p.FanartPath != nil)

	// Cascade to seasons and episodes.
	e.enrichShowChildren(ctx, agent, item, file)
	return nil
}

// matchMovie re-enriches a movie using RefreshMovie with a specific TMDB ID.
func (e *Enricher) matchMovie(ctx context.Context, agent metadata.Agent, item *media.Item, file *media.File, tmdbID int) error {
	result, err := agent.RefreshMovie(ctx, tmdbID)
	if err != nil {
		return fmt.Errorf("refresh movie %d: %w", tmdbID, err)
	}

	p := media.UpdateItemMetadataParams{
		ID:        item.ID,
		Title:     result.Title,
		SortTitle: result.Title,
		TMDBID:    &tmdbID,
	}
	if result.OriginalTitle != "" {
		p.OriginalTitle = &result.OriginalTitle
	}
	if result.Year != 0 {
		p.Year = &result.Year
	}
	if result.Summary != "" {
		p.Summary = &result.Summary
	}
	if result.Tagline != "" {
		p.Tagline = &result.Tagline
	}
	if result.Rating != 0 {
		p.Rating = &result.Rating
	}
	if result.ContentRating != "" {
		p.ContentRating = &result.ContentRating
	}
	if result.DurationMS != 0 {
		p.DurationMS = &result.DurationMS
	}
	if len(result.Genres) > 0 {
		p.Genres = result.Genres
	}
	if !result.ReleaseDate.IsZero() {
		p.OriginallyAvailableAt = &result.ReleaseDate
	}

	artDir := filepath.Dir(file.FilePath)
	if e.artwork != nil && artDir != "" && artDir != "." {
		if result.PosterURL != "" {
			absPath, dlErr := e.artwork.DownloadPoster(ctx, item.ID, result.PosterURL, artDir)
			if dlErr == nil {
				rel := e.relPath(absPath)
				p.PosterPath = &rel
			}
		}
		if result.FanartURL != "" {
			absPath, dlErr := e.artwork.DownloadFanart(ctx, item.ID, result.FanartURL, artDir)
			if dlErr == nil {
				rel := e.relPath(absPath)
				p.FanartPath = &rel
			}
		}
	}

	if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
		return fmt.Errorf("update movie metadata: %w", err)
	}

	e.logger.InfoContext(ctx, "movie matched",
		"item_id", item.ID, "title", result.Title, "tmdb_id", tmdbID)
	return nil
}

// findDescendantFile walks children to find an active file for artwork directory hints.
func (e *Enricher) findDescendantFile(ctx context.Context, parentID uuid.UUID) *media.File {
	children, err := e.updater.ListChildren(ctx, parentID)
	if err != nil {
		return nil
	}
	for i := range children {
		files, err := e.updater.GetFiles(ctx, children[i].ID)
		if err == nil {
			for j := range files {
				if files[j].Status == "active" {
					return &files[j]
				}
			}
		}
		// Recurse into grandchildren (season → episode).
		if f := e.findDescendantFile(ctx, children[i].ID); f != nil {
			return f
		}
	}
	return nil
}

// SearchTVCandidates returns TMDB TV search results for manual match selection.
func (e *Enricher) SearchTVCandidates(ctx context.Context, query string) ([]TVMatchCandidate, error) {
	agent := e.agentFn()
	if agent == nil {
		return nil, fmt.Errorf("metadata agent not configured")
	}
	results, err := agent.SearchTVCandidates(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]TVMatchCandidate, len(results))
	for i, r := range results {
		out[i] = TVMatchCandidate{
			TMDBID:    r.TMDBID,
			Title:     r.Title,
			Year:      r.FirstAirYear,
			Summary:   r.Summary,
			PosterURL: r.PosterURL,
			Rating:    r.Rating,
		}
	}
	return out, nil
}

// SearchMovieCandidates returns TMDB movie search results for manual match selection.
func (e *Enricher) SearchMovieCandidates(ctx context.Context, query string) ([]TVMatchCandidate, error) {
	agent := e.agentFn()
	if agent == nil {
		return nil, fmt.Errorf("metadata agent not configured")
	}
	result, err := agent.SearchMovie(ctx, query, 0)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	// SearchMovie returns a single result — wrap it. For a proper multi-result
	// movie search we'd add SearchMovieCandidates to the Agent interface; for now
	// return the top match.
	return []TVMatchCandidate{{
		TMDBID:    result.TMDBID,
		Title:     result.Title,
		Year:      result.Year,
		Summary:   result.Summary,
		PosterURL: result.PosterURL,
		Rating:    result.Rating,
	}}, nil
}

// TVMatchCandidate is returned by the search methods for manual match selection.
type TVMatchCandidate struct {
	TMDBID    int
	Title     string
	Year      int
	Summary   string
	PosterURL string
	Rating    float64
}

// relPath converts an absolute artwork path to a path relative to the first
// matching library scan_path. This relative path is what gets stored in the DB
// and used in /artwork/* URLs.
func (e *Enricher) relPath(absPath string) string {
	clean := filepath.Clean(absPath)
	if e.scanPaths != nil {
		for _, root := range e.scanPaths() {
			root = filepath.Clean(root)
			if rel, err := filepath.Rel(root, clean); err == nil && !strings.HasPrefix(rel, "..") {
				return strings.ReplaceAll(rel, `\`, "/")
			}
		}
	}
	// Fallback: return the basename (poster.jpg / fanart.jpg).
	return filepath.Base(absPath)
}

// tvdbShowFallback asks TVDB for a show when TMDB couldn't help. When base is
// non-nil the TMDB fields (title/id/rating/etc.) are preserved and only the
// missing artwork + tvdb id are merged from TVDB. Returns nil when TVDB isn't
// configured or has no match, so callers can decide how to proceed.
func (e *Enricher) tvdbShowFallback(ctx context.Context, title string, year int, base *metadata.TVShowResult) *metadata.TVShowResult {
	if e.tvdbFn == nil {
		return nil
	}
	client := e.tvdbFn()
	if client == nil {
		return nil
	}
	tv, err := client.SearchSeries(ctx, title, year)
	if err != nil || tv == nil {
		e.logger.InfoContext(ctx, "tvdb show fallback found no match",
			"title", title, "year", year, "err", err)
		return nil
	}
	if base == nil {
		return tv
	}
	// Merge: keep TMDB-sourced fields; fill in poster/fanart/tvdb id from TVDB.
	merged := *base
	if merged.TVDBID == 0 {
		merged.TVDBID = tv.TVDBID
	}
	if merged.PosterURL == "" {
		merged.PosterURL = tv.PosterURL
	}
	if merged.FanartURL == "" {
		merged.FanartURL = tv.FanartURL
	}
	if merged.Summary == "" {
		merged.Summary = tv.Summary
	}
	return &merged
}

// showDirFromFile returns the show root directory given an episode file path.
// For standard layout (Show/Season NN/episode.mkv) it goes up 2 levels.
// For flat layout (Show/episode.mkv) it goes up 1 level.
func showDirFromFile(filePath string) string {
	seasonDir := filepath.Dir(filePath)
	if looksLikeSeasonDir(filepath.Base(seasonDir)) {
		return filepath.Dir(seasonDir)
	}
	return seasonDir
}

// looksLikeSeasonDir returns true if the directory name looks like a season
// folder (e.g. "Season 01", "Specials", "Season 1").
func looksLikeSeasonDir(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "season") || lower == "specials"
}
