// Package scanner - enricher wires the TMDB metadata agent and artwork manager
// into the MetadataAgent interface consumed by the scanner.
package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/metadata/nfo"
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
	// ReplacePoster writes url to mediaDir/{itemID}-poster.jpg even if
	// the file already exists. Used by the music enricher to override
	// embedded album art with AudioDB's authoritative cover. The
	// ID-qualified filename prevents cross-album collisions in flat
	// layouts (see DownloadArtistPoster).
	ReplacePoster(ctx context.Context, itemID uuid.UUID, url, mediaDir string) (string, error)
	// ReplaceShowPoster / ReplaceShowFanart force-overwrite the bare
	// poster.jpg / fanart.jpg used by show + movie folders. Called by
	// the manual Fix Match flow so picking a different TMDB id swaps
	// the on-disk image too — the standard Download* path skips when
	// the file already exists.
	ReplaceShowPoster(ctx context.Context, itemID uuid.UUID, url, mediaDir string) (string, error)
	ReplaceShowFanart(ctx context.Context, itemID uuid.UUID, url, mediaDir string) (string, error)
	// DownloadArtistPoster/Fanart write to ID-qualified filenames to avoid
	// collisions in flat music layouts where multiple artists share a parent
	// directory (e.g., /Music/Artist/track.flac yields the library root as
	// the artist dir for every artist, clobbering poster.jpg).
	DownloadArtistPoster(ctx context.Context, itemID uuid.UUID, url, mediaDir string) (string, error)
	DownloadArtistFanart(ctx context.Context, itemID uuid.UUID, url, mediaDir string) (string, error)
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

// AlbumCoverByMBIDAgent looks up an album's front cover by its
// MusicBrainz release / release-group ID. Used as a fallback after
// a name-based music agent (TheAudioDB) fails to find a cover —
// MusicBrainz + Cover Art Archive catalog many indie / classical /
// compilation releases that TheAudioDB doesn't track.
//
// Returns ("", nil) when no cover is available for either ID so the
// enricher can fall through cleanly without an error path.
type AlbumCoverByMBIDAgent interface {
	FrontCoverURL(ctx context.Context, releaseID, releaseGroupID uuid.UUID) (string, error)
}

// Enricher implements MetadataAgent using the TMDB metadata provider and
// the artwork manager. Returning nil from agentFn disables enrichment —
// this lets the TMDB key be set at runtime via server settings without restart.
type Enricher struct {
	agentFn      func() metadata.Agent        // returns nil when no key is configured
	tvdbFn       func() TVDBFallback          // returns nil when no key is configured
	musicAgentFn func() metadata.MusicAgent   // returns nil when not configured
	caaFn        func() AlbumCoverByMBIDAgent // returns nil when disabled
	artwork      ArtworkFetcher
	updater      ItemUpdater
	scanPaths    ScanPathsProvider
	logger       *slog.Logger

	// musicSF collapses concurrent enrichment calls for the same music item:
	// a 10-track album produces 10 tracks' worth of enrichMusicItem calls that
	// each walk up to the same album. Without singleflight they all fire
	// SearchAlbum in parallel and blow the AudioDB rate limit.
	musicSF singleflight.Group
	// musicAttempted records music item IDs that have already been attempted
	// for enrichment in this process (success or failure). Prevents hammering
	// AudioDB with repeat calls for the same album/artist across the thousands
	// of tracks that walk up to them. Cleared only on process restart — a
	// rescan after a restart will retry previously failed items.
	musicAttempted sync.Map
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

// SetAlbumCoverByMBIDFn sets the lazy factory for the Cover Art Archive
// fallback client. Called per album-enrichment attempt after TheAudioDB —
// returning nil disables the fallback (used by tests + when the
// operator explicitly opts out).
func (e *Enricher) SetAlbumCoverByMBIDFn(fn func() AlbumCoverByMBIDAgent) {
	e.caaFn = fn
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

// applyMovieNFO layers NFO values over the in-flight
// UpdateItemMetadataParams. NFO wins where it's populated — it's
// operator-curated data, not a scraper guess. Empty NFO fields fall
// through to whatever TMDB already set on p.
func applyMovieNFO(p *media.UpdateItemMetadataParams, m *nfo.Movie) {
	if m == nil {
		return
	}
	if m.Title != "" {
		p.Title = m.Title
		sortTitle := m.SortTitle
		if sortTitle == "" {
			sortTitle = m.Title
		}
		p.SortTitle = sortTitle
	}
	if m.OriginalTitle != "" {
		v := m.OriginalTitle
		p.OriginalTitle = &v
	}
	if m.Year != 0 {
		v := m.Year
		p.Year = &v
	}
	if m.Plot != "" {
		v := m.Plot
		p.Summary = &v
	}
	if m.Tagline != "" {
		v := m.Tagline
		p.Tagline = &v
	}
	if m.Rating > 0 {
		v := m.Rating
		p.Rating = &v
	}
	if m.MPAA != "" {
		v := m.MPAA
		p.ContentRating = &v
	}
	if m.RuntimeMin > 0 {
		ms := int64(m.RuntimeMin) * 60 * 1000
		p.DurationMS = &ms
	}
	if len(m.Genres) > 0 {
		p.Genres = m.Genres
	}
	if m.Premiered != nil {
		v := *m.Premiered
		p.OriginallyAvailableAt = &v
	}
}

// writeNFOOnly persists NFO metadata when the TMDB lookup failed or
// wasn't configured. Missing posters aren't populated here — if the
// user cares they can drop a folder.jpg / poster.jpg next to the
// movie and the scanner's art discovery will pick it up on the
// next pass.
func (e *Enricher) writeNFOOnly(ctx context.Context, itemID uuid.UUID, m *nfo.Movie) error {
	p := media.UpdateItemMetadataParams{ID: itemID}
	applyMovieNFO(&p, m)
	if p.Title == "" {
		return nil // nothing useful in the NFO
	}
	if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
		return fmt.Errorf("update item metadata (nfo-only): %w", err)
	}
	e.logger.InfoContext(ctx, "item enriched from NFO (no TMDB match)",
		"item_id", itemID, "title", m.Title)
	return nil
}

func (e *Enricher) enrichMovie(ctx context.Context, agent metadata.Agent, item *media.Item, file *media.File) error {
	// NFO sidecar (movie.nfo / <basename>.nfo) is an operator-curated
	// metadata source — if it exists we trust it over TMDB guesses.
	// Use its title for the TMDB search too (gets us better poster
	// matches when the filename is junk like "Movie_2009_WEB-DL").
	var nfoMovie *nfo.Movie
	if nfoPath, err := nfo.FindMovieNFO(file.FilePath); err == nil {
		if f, err := os.Open(nfoPath); err == nil {
			parsed, perr := nfo.ParseMovie(f)
			_ = f.Close()
			if perr == nil {
				nfoMovie = parsed
			} else {
				e.logger.WarnContext(ctx, "nfo parse failed; falling through to TMDB",
					"path", nfoPath, "err", perr)
			}
		}
	}

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
	// NFO overrides for the TMDB search terms — its title + year are
	// user-curated, so they beat whatever we derived from the filename.
	if nfoMovie != nil {
		if nfoMovie.Title != "" {
			searchTitle = nfoMovie.Title
		}
		if nfoMovie.Year != 0 {
			year = nfoMovie.Year
		}
	}

	result, err := agent.SearchMovie(ctx, searchTitle, year)
	if err != nil || result == nil {
		// No result or API error — not a scan-blocking error.
		e.logger.InfoContext(ctx, "tmdb search found no result",
			"title", item.Title, "year", year, "err", err)
		// Even without TMDB, NFO alone is enough to populate metadata
		// for a Kodi-migrated library. Write what we have.
		if nfoMovie != nil {
			return e.writeNFOOnly(ctx, item.ID, nfoMovie)
		}
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
				e.setRelPath(&p.PosterPath, absPath)
			}
		}
		if result.FanartURL != "" {
			absPath, err := e.artwork.DownloadFanart(ctx, item.ID, result.FanartURL, artDir)
			if err != nil {
				e.logger.WarnContext(ctx, "fanart download failed",
					"item_id", item.ID, "err", err)
			} else {
				e.setRelPath(&p.FanartPath, absPath)
			}
		}
	}

	// NFO overrides happen LAST so user-curated data beats TMDB's
	// scrape. Artwork URLs (poster/fanart paths we just filled from
	// TMDB) stay — the NFO doesn't override those.
	applyMovieNFO(&p, nfoMovie)

	if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
		return fmt.Errorf("update item metadata: %w", err)
	}

	e.logger.InfoContext(ctx, "item enriched",
		"item_id", item.ID,
		"title", p.Title,
		"tmdb_id", result.TMDBID,
		"has_poster", p.PosterPath != nil,
		"from_nfo", nfoMovie != nil,
	)
	return nil
}

// applyShowNFO layers NFO values over TMDB-derived UpdateItemMetadataParams
// for tvshow.nfo. Same philosophy as applyMovieNFO — operator-curated
// data wins.
func applyShowNFO(p *media.UpdateItemMetadataParams, s *nfo.Show) {
	if s == nil {
		return
	}
	if s.Title != "" {
		p.Title = s.Title
		sortTitle := s.SortTitle
		if sortTitle == "" {
			sortTitle = s.Title
		}
		p.SortTitle = sortTitle
	}
	if s.OriginalTitle != "" {
		v := s.OriginalTitle
		p.OriginalTitle = &v
	}
	if s.Year != 0 {
		v := s.Year
		p.Year = &v
	}
	if s.Plot != "" {
		v := s.Plot
		p.Summary = &v
	}
	if s.Rating > 0 {
		v := s.Rating
		p.Rating = &v
	}
	if s.ContentRating != "" {
		v := s.ContentRating
		p.ContentRating = &v
	}
	if len(s.Genres) > 0 {
		p.Genres = s.Genres
	}
	if s.Premiered != nil {
		v := *s.Premiered
		p.OriginallyAvailableAt = &v
	}
}

// enrichShow searches TMDB for the show and updates metadata, poster, and fanart.
func (e *Enricher) enrichShow(ctx context.Context, agent metadata.Agent, item *media.Item, file *media.File) error {
	// tvshow.nfo lives in the show directory (two levels up from the
	// episode file: episode → season → show). Reads happen before the
	// TMDB search so the NFO's title can feed a more accurate search.
	var nfoShow *nfo.Show
	showDir := showDirFromFile(file.FilePath)
	if showDir != "" {
		if nfoPath, err := nfo.FindShowNFO(showDir); err == nil {
			if f, err := os.Open(nfoPath); err == nil {
				parsed, perr := nfo.ParseShow(f)
				_ = f.Close()
				if perr == nil {
					nfoShow = parsed
				} else {
					e.logger.WarnContext(ctx, "tvshow.nfo parse failed; falling through to TMDB",
						"path", nfoPath, "err", perr)
				}
			}
		}
	}

	searchTitle, extractedYear := cleanTitle(item.Title)
	year := 0
	if item.Year != nil {
		year = *item.Year
	}
	if year == 0 && extractedYear != nil {
		year = *extractedYear
	}
	if nfoShow != nil {
		if nfoShow.Title != "" {
			searchTitle = nfoShow.Title
		}
		if nfoShow.Year != 0 {
			year = nfoShow.Year
		}
	}

	result, err := agent.SearchTV(ctx, searchTitle, year)
	if err != nil || result == nil {
		e.logger.InfoContext(ctx, "tmdb tv search found no result",
			"title", item.Title, "err", err)
		// No TMDB match — try TVDB outright.
		result = e.tvdbShowFallback(ctx, searchTitle, year, nil)
		if result == nil {
			// Fall back to NFO-only when neither TMDB nor TVDB matched.
			if nfoShow != nil {
				p := media.UpdateItemMetadataParams{ID: item.ID}
				applyShowNFO(&p, nfoShow)
				if p.Title != "" {
					if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
						return fmt.Errorf("update show metadata (nfo-only): %w", err)
					}
					e.logger.InfoContext(ctx, "show enriched from NFO (no TMDB/TVDB match)",
						"item_id", item.ID, "title", nfoShow.Title)
				}
			}
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
				e.setRelPath(&p.PosterPath, absPath)
			}
		}
		if result.FanartURL != "" {
			absPath, err := e.artwork.DownloadFanart(ctx, item.ID, result.FanartURL, artDir)
			if err != nil {
				e.logger.WarnContext(ctx, "show fanart download failed",
					"item_id", item.ID, "err", err)
			} else {
				e.setRelPath(&p.FanartPath, absPath)
			}
		}
	}

	// NFO wins where it has values — applied after TMDB so a Kodi-curated
	// tvshow.nfo beats a TMDB guess on title/plot/genres.
	applyShowNFO(&p, nfoShow)

	if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
		return fmt.Errorf("update show metadata: %w", err)
	}

	e.logger.InfoContext(ctx, "show enriched",
		"item_id", item.ID,
		"title", p.Title,
		"tmdb_id", result.TMDBID,
		"has_poster", p.PosterPath != nil,
		"from_nfo", nfoShow != nil,
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
			e.setRelPath(&p.PosterPath, absPath)
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
// applyEpisodeNFO overrides TMDB/TVDB fields with values from the
// episode-specific NFO (<basename>.nfo with <episodedetails>). NFO
// is the operator's hand-edit so its title/plot win.
func applyEpisodeNFO(p *media.UpdateItemMetadataParams, e *nfo.Episode) {
	if e == nil {
		return
	}
	if e.Title != "" {
		p.Title = e.Title
		p.SortTitle = e.Title
	}
	if e.Plot != "" {
		v := e.Plot
		p.Summary = &v
	}
	if e.Rating > 0 {
		v := e.Rating
		p.Rating = &v
	}
	if e.Aired != nil {
		v := *e.Aired
		p.OriginallyAvailableAt = &v
	}
	if e.RuntimeMin > 0 {
		ms := int64(e.RuntimeMin) * 60 * 1000
		p.DurationMS = &ms
	}
}

func (e *Enricher) enrichEpisode(ctx context.Context, agent metadata.Agent, item *media.Item, file *media.File) error {
	if item.ParentID == nil || item.Index == nil {
		return nil
	}

	// Episode NFO lives next to the media file, same basename. Read
	// before TMDB so we can fall back to the NFO when the online
	// lookup can't find the episode.
	var nfoEpisode *nfo.Episode
	if nfoPath, err := nfo.FindEpisodeNFO(file.FilePath); err == nil {
		if f, ferr := os.Open(nfoPath); ferr == nil {
			parsed, perr := nfo.ParseEpisode(f)
			_ = f.Close()
			if perr == nil {
				nfoEpisode = parsed
			} else {
				e.logger.WarnContext(ctx, "episode NFO parse failed",
					"path", nfoPath, "err", perr)
			}
		}
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
			e.setRelPath(&p.ThumbPath, absPath)
		}
	}

	// Apply NFO last so user-curated fields beat TMDB/TVDB.
	applyEpisodeNFO(&p, nfoEpisode)

	if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
		return fmt.Errorf("update episode metadata: %w", err)
	}

	e.logger.InfoContext(ctx, "episode enriched",
		"item_id", item.ID,
		"title", p.Title,
		"season", *season.Index,
		"episode", *item.Index,
		"from_nfo", nfoEpisode != nil,
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
			e.enrichMusicOnce(ctx, agent, album, albumDir)
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
			e.enrichMusicOnce(ctx, agent, artist, artistDir)
		}
		return nil

	case "artist":
		return e.enrichArtist(ctx, agent, item, artDir)

	case "album":
		return e.enrichAlbum(ctx, agent, item, artDir)
	}
	return nil
}

// enrichMusicOnce enriches an album or artist, coalescing concurrent callers
// via singleflight and skipping items already attempted in this process.
// Errors are logged but not propagated — a per-track enrichment pass must
// never fail the scan.
func (e *Enricher) enrichMusicOnce(ctx context.Context, agent metadata.MusicAgent, item *media.Item, artDir string) {
	key := item.Type + ":" + item.ID.String()
	if _, ok := e.musicAttempted.Load(key); ok {
		return
	}
	_, err, _ := e.musicSF.Do(key, func() (any, error) {
		if _, ok := e.musicAttempted.Load(key); ok {
			return nil, nil
		}
		var innerErr error
		switch item.Type {
		case "album":
			innerErr = e.enrichAlbum(ctx, agent, item, artDir)
		case "artist":
			innerErr = e.enrichArtist(ctx, agent, item, artDir)
		}
		e.musicAttempted.Store(key, struct{}{})
		return nil, innerErr
	})
	if err != nil {
		e.logger.WarnContext(ctx, item.Type+" enrich failed",
			item.Type+"_id", item.ID, "err", err)
	}
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
		if abs, err := e.artwork.DownloadArtistPoster(ctx, item.ID, result.ThumbURL, artDir); err == nil {
			e.setRelPath(&p.PosterPath, abs)
		}
	}
	if result.FanartURL != "" && artDir != "" && e.artwork != nil {
		if abs, err := e.artwork.DownloadArtistFanart(ctx, item.ID, result.FanartURL, artDir); err == nil {
			e.setRelPath(&p.FanartPath, abs)
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
	// A nil result is "TheAudioDB didn't find this album" — not a hard
	// stop. We still want to try Cover Art Archive when the album has
	// MusicBrainz IDs, so use an empty result shell and fall through.
	result, err := agent.SearchAlbum(ctx, artistName, item.Title)
	if err != nil {
		return fmt.Errorf("audiodb search album %q/%q: %w", artistName, item.Title, err)
	}
	if result == nil {
		result = &metadata.AlbumResult{}
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
	// Resolve cover URL: TheAudioDB first (authoritative for the popular
	// catalog), then Cover Art Archive via MusicBrainz release IDs
	// (catches indie / classical / compilations TheAudioDB doesn't have).
	coverURL := result.ThumbURL
	if coverURL == "" && e.caaFn != nil {
		if caa := e.caaFn(); caa != nil {
			relID := uuid.Nil
			if item.MusicBrainzReleaseID != nil {
				relID = *item.MusicBrainzReleaseID
			}
			rgID := uuid.Nil
			if item.MusicBrainzReleaseGroupID != nil {
				rgID = *item.MusicBrainzReleaseGroupID
			}
			if relID != uuid.Nil || rgID != uuid.Nil {
				if u, err := caa.FrontCoverURL(ctx, relID, rgID); err == nil && u != "" {
					coverURL = u
					e.logger.InfoContext(ctx, "album cover from CAA fallback",
						"item_id", item.ID, "title", item.Title,
						"release_id", relID, "release_group_id", rgID,
					)
				}
			}
		}
	}
	if coverURL != "" && artDir != "" && e.artwork != nil {
		// Overwrite {id}-poster.jpg — if a scan wrote embedded art for
		// this album, the enricher's cover should take precedence.
		if abs, err := e.artwork.ReplacePoster(ctx, item.ID, coverURL, artDir); err == nil {
			e.setRelPath(&p.PosterPath, abs)
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
	// Top-level show / season items don't have direct files — the files
	// belong to descendant episodes. Walk the parent_id chain to find one.
	// Mirrors MatchItem's behavior so the on-demand Enrich path works for
	// shows the same way "Fix Match" does (and so the admin bulk
	// re-enrich-unmatched path works for the unmatched-show recovery
	// case it was built for — every row it returns is type='show').
	if file == nil {
		file = e.findDescendantFile(ctx, item.ID)
	}
	if file == nil {
		return fmt.Errorf("no active file for item %s or its descendants", itemID)
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
		// Use the Replace variant so a wrong-match poster.jpg /
		// fanart.jpg from a previous enrich gets overwritten — the
		// non-Replace path short-circuits when the file already
		// exists, which leaves the old image bytes on disk even
		// though poster_path now points at the same filename.
		if result.PosterURL != "" {
			absPath, dlErr := e.artwork.ReplaceShowPoster(ctx, item.ID, result.PosterURL, artDir)
			if dlErr != nil {
				e.logger.WarnContext(ctx, "match poster download failed",
					"item_id", item.ID, "err", dlErr)
			} else {
				e.setRelPath(&p.PosterPath, absPath)
			}
		}
		if result.FanartURL != "" {
			absPath, dlErr := e.artwork.ReplaceShowFanart(ctx, item.ID, result.FanartURL, artDir)
			if dlErr != nil {
				e.logger.WarnContext(ctx, "match fanart download failed",
					"item_id", item.ID, "err", dlErr)
			} else {
				e.setRelPath(&p.FanartPath, absPath)
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

	// Force-overwrite via ReplaceShow* (non-Replace path skips when
	// the file already exists, which would leave the old wrong-match
	// poster bytes on disk).
	artDir := filepath.Dir(file.FilePath)
	if e.artwork != nil && artDir != "" && artDir != "." {
		if result.PosterURL != "" {
			absPath, dlErr := e.artwork.ReplaceShowPoster(ctx, item.ID, result.PosterURL, artDir)
			if dlErr == nil {
				e.setRelPath(&p.PosterPath, absPath)
			}
		}
		if result.FanartURL != "" {
			absPath, dlErr := e.artwork.ReplaceShowFanart(ctx, item.ID, result.FanartURL, artDir)
			if dlErr == nil {
				e.setRelPath(&p.FanartPath, absPath)
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

// ListPosters fetches every poster variant TMDB has for the given show or
// movie tmdbID. The user picks one of these URLs and SetItemPoster applies
// it. Returns a non-nil empty slice if the agent isn't a PosterLister
// (e.g. tests with a mock agent), so callers can keep the response shape
// consistent.
func (e *Enricher) ListPosters(ctx context.Context, itemType string, tmdbID int) ([]metadata.PosterCandidate, error) {
	agent := e.agentFn()
	if agent == nil {
		return nil, fmt.Errorf("metadata agent not configured")
	}
	pl, ok := agent.(metadata.PosterLister)
	if !ok {
		return []metadata.PosterCandidate{}, nil
	}
	switch itemType {
	case "show":
		return pl.ListTVPostersForID(ctx, tmdbID)
	case "movie":
		return pl.ListMoviePostersForID(ctx, tmdbID)
	default:
		return nil, fmt.Errorf("poster listing only supports show and movie items, got %q", itemType)
	}
}

// SetItemPoster downloads the chosen poster URL into the item's art
// directory and updates poster_path. Used by the manual poster-picker
// after the user picks one of the variants from ListPosters. Works for
// any item that has on-disk files reachable via parent traversal —
// shows resolve to the show root directory, movies to the file's
// containing folder, episodes to the season/show folder.
//
// posterURL can be any HTTP(S) URL (TMDB image, TVDB image, or a
// user-pasted URL); the artwork manager fetches the bytes and writes
// them atomically over {item.id}-poster.jpg in the art directory.
func (e *Enricher) SetItemPoster(ctx context.Context, itemID uuid.UUID, posterURL string) error {
	if e.artwork == nil {
		return fmt.Errorf("artwork manager not configured")
	}
	if posterURL == "" {
		return fmt.Errorf("posterURL is required")
	}

	item, err := e.updater.GetItem(ctx, itemID)
	if err != nil {
		return fmt.Errorf("get item %s: %w", itemID, err)
	}

	// Locate any active file in the subtree so we can resolve the
	// art directory. Shows have no direct file; an episode's path
	// gives us showDirFromFile after walking up two levels.
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
	if file == nil {
		file = e.findDescendantFile(ctx, item.ID)
	}
	if file == nil {
		return fmt.Errorf("no active file under item %s — can't resolve art directory", itemID)
	}

	var artDir string
	switch item.Type {
	case "show":
		artDir = showDirFromFile(file.FilePath)
	case "season":
		// Season posters live next to the season's episodes.
		artDir = filepath.Dir(file.FilePath)
	default:
		// movies, episodes, albums, etc.: drop alongside the file.
		artDir = filepath.Dir(file.FilePath)
	}
	if artDir == "" || artDir == "." {
		return fmt.Errorf("could not resolve art directory for item %s", itemID)
	}

	absPath, err := e.artwork.ReplacePoster(ctx, item.ID, posterURL, artDir)
	if err != nil {
		return fmt.Errorf("download poster: %w", err)
	}

	rel := e.relPath(absPath)
	if rel == "" {
		// The downloaded file landed somewhere /artwork/* can't serve
		// from. Surface the misconfiguration to the caller (manual
		// poster picker UI) instead of silently writing an unservable
		// poster_path that 404s on every render.
		return fmt.Errorf("downloaded poster %s falls outside library scan_paths", absPath)
	}
	p := media.UpdateItemMetadataParams{
		ID:         item.ID,
		Title:      item.Title,
		SortTitle:  item.SortTitle,
		Year:       item.Year,
		Genres:     item.Genres,
		PosterPath: &rel,
	}
	if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
		return fmt.Errorf("update poster_path: %w", err)
	}
	e.logger.InfoContext(ctx, "poster updated via manual picker",
		"item_id", item.ID, "poster_url", posterURL, "rel_path", rel)
	return nil
}

// relPath converts an absolute artwork path to a path relative to the first
// matching library scan_path. This relative path is what gets stored in the DB
// and used in /artwork/* URLs.
//
// The fallback returns the bare basename because for flat-layout music
// libraries the artist poster legitimately sits at the scan root
// (/<Music>/<artist_id>-poster.jpg), and a "<parent-dir>/<basename>"
// guess produces a double-path (Music/<artist_id>-poster.jpg) that
// 404s against <scan_root>/Music/.... Album art, which sits one level
// deep inside <Artist>/, is populated by the scanner (not the
// enricher), and the scanner's own fallback includes the parent dir
// — so the split fallback policies together cover both cases.
// Returns "" when the file is outside every scan_path. Callers MUST
// guard on empty before storing — a bare basename would 404 against
// any non-flat layout (an album poster at <Artist>/<Album>/<id>.jpg
// stored as just "<id>.jpg" can't be resolved by /artwork/*, since
// the route walks scan roots and the file lives two dirs deeper).
// The previous fallback returned filepath.Base() and silently wrote
// unservable rows whenever scanPaths() didn't match — that's the
// failure mode migration 00054 nulled out, and migration 00070 cleans
// up the regression that re-broke them.
//
// Flat-layout music libraries where the artist poster sits at the
// scan root still resolve correctly: filepath.Rel(root, root+"/x.jpg")
// returns "x.jpg" through the loop, never reaching the empty fallback.
// setRelPath stores relPath(abs) in *dest unless the resolution
// failed. The skip-on-empty path leaves *dest nil, which combines
// with the COALESCE in UpdateMediaItemMetadata to preserve whatever
// the caller had already (the scanner-derived path or a previous
// successful enricher result) rather than overwriting it with an
// unservable bare basename.
func (e *Enricher) setRelPath(dest **string, abs string) {
	if rel := e.relPath(abs); rel != "" {
		*dest = &rel
	}
}

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
	return ""
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
