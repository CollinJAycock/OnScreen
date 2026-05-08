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
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/metadata/anilist"
	"github.com/onscreen/onscreen/internal/metadata/animedb"
	"github.com/onscreen/onscreen/internal/metadata/nfo"
)

// ItemUpdater saves enriched metadata back to the database.
type ItemUpdater interface {
	UpdateItemMetadata(ctx context.Context, p media.UpdateItemMetadataParams) (*media.Item, error)
	GetItem(ctx context.Context, id uuid.UUID) (*media.Item, error)
	GetFiles(ctx context.Context, itemID uuid.UUID) ([]media.File, error)
	ListChildren(ctx context.Context, parentID uuid.UUID) ([]media.Item, error)
	// GetItemByTMDBID + MergeIntoTopLevel power the merge-aware Fix Match
	// path: when the chosen TMDB id is already attached to a different row
	// in the same library, matchShow / matchMovie merges the current row
	// into that survivor instead of trying to update its title (which
	// would clash on the library_id+type+title+year unique constraint).
	// Both methods return nil error when the lookup misses; merge is a
	// no-op when loser == survivor.
	GetItemByTMDBID(ctx context.Context, libraryID uuid.UUID, tmdbID int) (*media.Item, error)
	MergeIntoTopLevel(ctx context.Context, loserID, survivorID uuid.UUID, itemType string) error
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

// LibraryAnimeChecker reports whether a given library is flagged as
// anime — used by the show-enricher to decide whether AniList runs
// primary (true) or as a fallback when TMDB returns nothing (false).
//
// Narrow single-method interface so the enricher doesn't drag in the
// full library-service surface; only the is-anime bool matters here.
type LibraryAnimeChecker interface {
	IsLibraryAnime(ctx context.Context, libraryID uuid.UUID) (bool, error)
	// IsLibraryManga reports whether the library is flagged as
	// manga — flips the book enricher to AniList primary instead of
	// the (eventual) OpenLibrary / Hardcover book agents. Mirrors
	// the IsLibraryAnime narrow interface for the same reason: the
	// enricher only needs this one bit, not the full library row.
	IsLibraryManga(ctx context.Context, libraryID uuid.UUID) (bool, error)
}

// AniListAgent is the anime-native metadata source. Slotted into the
// show-enrichment fallback chain between TMDB and TVDB: TMDB still
// catches mainstream + Western shows; AniList catches anime that TMDB
// either lacks entirely or has thin metadata for; TVDB is the
// universal last-ditch fallback that handles anime-numbering quirks
// at the episode level.
//
// Narrower interface than metadata.Agent because AniList doesn't
// natively model TMDB-shaped seasons/episodes (anime treats sequels
// as separate Media entries, not seasons). Only the show-level
// search lookups belong here.
type AniListAgent interface {
	SearchAnime(ctx context.Context, title string, year int) (*metadata.TVShowResult, error)
	GetAnimeByID(ctx context.Context, anilistID int) (*metadata.TVShowResult, error)
	// GetAnimeEpisodes returns the streamingEpisodes list for a Media
	// row. Used as a final fallback when neither TMDB nor TVDB has the
	// show — anime episodes still get a title + thumbnail. Result rows
	// have Title / ThumbURL populated and Summary / AirDate empty;
	// AniList isn't an episode-data store. EpisodeNum carries the
	// 1-based index parsed from the AniList title format.
	GetAnimeEpisodes(ctx context.Context, anilistID int) ([]metadata.EpisodeResult, error)
	// GetAnimeFranchise returns the matched Media plus its PREQUEL /
	// SEQUEL chain, sorted by start year. Used to map our seasons
	// (Season 1, Season 2, …) onto distinct AniList Media rows so
	// each season carries the right anilist_id and per-episode
	// metadata resolves to the correct cour. Returns AniListRelation
	// records bare-bones enough to drive that mapping.
	GetAnimeFranchise(ctx context.Context, anilistID int) ([]anilist.AniListRelation, error)
	// SearchManga returns the top manga match for a title — Mangaka
	// (author/artist), serialization status, demographic / magazine
	// tags, reading direction (rtl for JP, ttb for KR/CN webtoons,
	// ltr otherwise). Used by the book scanner's enricher path when
	// the library is a manga library.
	SearchManga(ctx context.Context, title string, year int) (*metadata.MangaResult, error)
	GetMangaByID(ctx context.Context, anilistID int) (*metadata.MangaResult, error)
}

// AnimeDBLookup is the offline title→AniList-ID resolver, backed by
// the manami-project anime-offline-database. The enricher consults
// this on AniList live-search misses — manami's curated synonyms
// list catches fansub-style folder names ("Akame ga Kill Theater")
// that the live `Media(search:$q)` GraphQL field can't recover from.
//
// Narrow single-method interface so the enricher doesn't drag the
// download / cache machinery into its tests; only the resolution
// step matters here.
type AnimeDBLookup interface {
	Lookup(title string) (animedb.Entry, bool)
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
	anilistFn    func() AniListAgent          // returns nil when AniList is disabled
	animeDBFn    func() AnimeDBLookup         // returns nil when offline-DB lookup is disabled
	libAnimeFn   func() LibraryAnimeChecker   // returns nil when not wired
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

	// aniListEpsSF + aniListEpsCache collapse and memoize AniList
	// streamingEpisodes lookups. Without these, a 13-episode anime
	// season cascade fires 13 separate `GetAnimeEpisodes` calls in
	// rapid succession — AniList's 90 req/min ceiling on the public
	// endpoint flips to HTTP 429 after the first 5-6, so all but the
	// first episode in the cascade silently lose their metadata.
	// singleflight.Do collapses concurrent fetches; the cache short-
	// circuits subsequent cascades for the same show inside the TTL
	// window so a per-show refresh stays at one upstream call.
	aniListEpsSF    singleflight.Group
	aniListEpsCache sync.Map // map[int]aniListEpsCacheEntry — key = anilist_id
}

// aniListEpsCacheEntry is the cached return of a streamingEpisodes
// fetch. fetchedAt + an explicit nil distinction (no entries vs. not
// fetched yet) lets the lookup short-circuit cleanly.
type aniListEpsCacheEntry struct {
	eps       []metadata.EpisodeResult
	fetchedAt time.Time
}

// aniListEpsTTL is how long a streamingEpisodes lookup stays cached
// in-process. Anime episode titles essentially never change; the TTL
// exists to bound staleness on a long-running server (so a freshly-
// added episode on AniList still surfaces eventually) rather than to
// expire wrong data. A long TTL keeps refresh-button mash-presses at
// zero upstream cost.
const aniListEpsTTL = 1 * time.Hour

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

// SetAniListFn sets the lazy factory for the AniList anime metadata
// client. The function is called per show-enrichment attempt — return
// nil to skip AniList. AniList has no API key requirement so the
// factory typically just returns a singleton client built at server
// bootstrap; the lazy factory shape exists for parity with TVDB and
// to leave room for future per-library opt-out.
func (e *Enricher) SetAniListFn(fn func() AniListAgent) {
	e.anilistFn = fn
}

// SetAnimeDBFn sets the lazy factory for the offline AniList-ID
// resolver (manami-project anime-offline-database). Called by
// anilistShowFallback as a recovery path when AniList live search
// misses — the dataset's curated synonyms list catches fansub-style
// folder names AniList's fuzzy search rejects. Returning nil from
// the factory disables the fallback.
func (e *Enricher) SetAnimeDBFn(fn func() AnimeDBLookup) {
	e.animeDBFn = fn
}

// SetLibraryAnimeCheckerFn sets the lazy factory for the per-library
// anime-flag lookup. When the lookup returns true for a show's
// library, the enrichShow agent order flips: AniList runs primary
// instead of fallback, with TMDB and TVDB as the secondary chain.
// Returning nil from the factory (or this never being called) leaves
// the enricher in default TMDB-first behaviour.
func (e *Enricher) SetLibraryAnimeCheckerFn(fn func() LibraryAnimeChecker) {
	e.libAnimeFn = fn
}

// libraryIsAnime returns true when the given library has its
// is_anime flag set. Returns false on any error or when the checker
// isn't wired — failing closed keeps the existing TMDB-first path
// for installs where the lookup hasn't been bootstrapped.
func (e *Enricher) libraryIsAnime(ctx context.Context, libraryID uuid.UUID) bool {
	if e.libAnimeFn == nil {
		return false
	}
	checker := e.libAnimeFn()
	if checker == nil {
		return false
	}
	is, err := checker.IsLibraryAnime(ctx, libraryID)
	if err != nil {
		e.logger.WarnContext(ctx, "library is_anime lookup failed; defaulting to false",
			"library_id", libraryID, "err", err)
		return false
	}
	return is
}

// libraryIsManga returns true when the library's type is `manga`.
// Flips the book enricher to AniList-primary metadata.
func (e *Enricher) libraryIsManga(ctx context.Context, libraryID uuid.UUID) bool {
	if e.libAnimeFn == nil {
		return false
	}
	checker := e.libAnimeFn()
	if checker == nil {
		return false
	}
	is, err := checker.IsLibraryManga(ctx, libraryID)
	if err != nil {
		e.logger.WarnContext(ctx, "library is_manga lookup failed; defaulting to false",
			"library_id", libraryID, "err", err)
		return false
	}
	return is
}

// SetMusicAgentFn sets the lazy factory for the music metadata client.
// The function is called per enrichment — return nil to skip music enrichment.
func (e *Enricher) SetMusicAgentFn(fn func() metadata.MusicAgent) {
	e.musicAgentFn = fn
}

// forceReenrichKey is a context key that signals the cascade should
// bypass the "already-has-summary-and-thumb" skip on episode rows.
// Set by EnrichItem (manual refresh from the detail-page button) and
// read by enrichSeasonChildren. The default zero value (skip enabled)
// is what scan-time enrichment wants — refreshing every episode on
// every scan would burn TMDB / AniList rate limits to no real gain.
type forceReenrichKey struct{}

// withForceReenrich returns a child context that signals the cascade
// to overwrite existing episode metadata. Used by manual refresh and
// by the franchise-walk path where prior anilist_ids may have been
// pointed at the wrong cour.
func withForceReenrich(ctx context.Context) context.Context {
	return context.WithValue(ctx, forceReenrichKey{}, true)
}

// shouldForceReenrich reads the force flag from the context. False
// in any path that didn't explicitly set it, including all scan-time
// flows.
func shouldForceReenrich(ctx context.Context) bool {
	v, _ := ctx.Value(forceReenrichKey{}).(bool)
	return v
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
		// Standalone case (Enrich called for a single episode item, e.g.
		// admin "re-enrich item"). No prefetched season data — pay the
		// per-episode TMDB round-trip; this is rare and one-off.
		return e.enrichEpisode(ctx, agent, item, file, nil)
	case "artist", "album", "track":
		if e.musicAgentFn != nil {
			if ma := e.musicAgentFn(); ma != nil {
				return e.enrichMusicItem(ctx, ma, item, file)
			}
		}
		return nil
	case "book":
		// Books only get enriched when their library is a manga
		// library — the AniList agent is the only book-side
		// metadata source we ship today. Generic-book agents
		// (OpenLibrary / Hardcover) are a future track; until they
		// land, non-manga books stay at their scanner-derived state.
		if e.libraryIsManga(ctx, item.LibraryID) {
			return e.enrichManga(ctx, item, file)
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

// enrichManga runs AniList against a `book` row in a manga library.
// One file = one row at the scanner layer (no volume / chapter
// hierarchy yet — that's a v2.x track of its own), so this enrich
// shapes the manga as a top-level series row with mangaka + status
// + reading-direction metadata. The reader UI reads
// reading_direction at open time to flip page-flip orientation.
//
// Cover art comes from AniList's coverImage. Filename-derived title
// is the search input; AniList's English title (or romaji fallback)
// becomes the canonical title once matched. Operator's NFO / manual
// edits beat AniList — applied after the agent result lands.
func (e *Enricher) enrichManga(ctx context.Context, item *media.Item, file *media.File) error {
	if e.anilistFn == nil {
		return nil
	}
	al := e.anilistFn()
	if al == nil {
		return nil
	}

	// Title-derived search. parseBookTitle stripped volume / issue
	// prefixes at scan time, so item.Title is already a clean
	// series name in the common case ("Death Note Vol. 03" →
	// "Death Note"). Year hint comes from the optional year field
	// on the row when the operator's folder structure carried it.
	year := 0
	if item.Year != nil {
		year = *item.Year
	}

	result, err := al.SearchManga(ctx, item.Title, year)
	if err != nil || result == nil {
		e.logger.InfoContext(ctx, "anilist manga search found no result",
			"title", item.Title, "err", err)
		return nil
	}

	p := media.UpdateItemMetadataParams{
		ID:        item.ID,
		Title:     result.Title,
		SortTitle: result.Title,
	}
	if result.OriginalTitle != "" {
		v := result.OriginalTitle
		p.OriginalTitle = &v
	}
	if result.StartYear != 0 {
		v := result.StartYear
		p.Year = &v
	}
	if result.Summary != "" {
		v := result.Summary
		p.Summary = &v
	}
	if result.Rating != 0 {
		v := result.Rating
		p.Rating = &v
	}
	if result.ContentRating != "" {
		v := result.ContentRating
		p.ContentRating = &v
	}
	if len(result.Genres) > 0 {
		p.Genres = result.Genres
	}
	if len(result.Tags) > 0 {
		p.Tags = result.Tags
	}
	if result.AniListID != 0 {
		v := result.AniListID
		p.AniListID = &v
	}
	if result.MALID != 0 {
		v := result.MALID
		p.MALID = &v
	}
	if result.ReadingDirection != "" {
		v := result.ReadingDirection
		p.ReadingDirection = &v
	}

	// Cover art — drop alongside the manga file (volume / chapter
	// directory). Same shape as movie posters.
	artDir := filepath.Dir(file.FilePath)
	if e.artwork != nil && result.PosterURL != "" && artDir != "" && artDir != "." {
		absPath, err := e.artwork.DownloadPoster(ctx, item.ID, result.PosterURL, artDir)
		if err != nil {
			e.logger.WarnContext(ctx, "manga poster download failed",
				"item_id", item.ID, "err", err)
		} else {
			e.setRelPath(&p.PosterPath, absPath)
		}
	}

	if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
		return fmt.Errorf("update manga metadata: %w", err)
	}
	e.logger.InfoContext(ctx, "manga enriched",
		"item_id", item.ID,
		"title", p.Title,
		"anilist_id", result.AniListID,
		"author", result.Author,
		"status", result.SerializationStatus,
		"reading_direction", result.ReadingDirection)
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

	// Pre-flight merge — same shape as enrichShow / matchShow / matchMovie.
	// Without this the UpdateItemMetadata below would crash whenever the
	// canonical TMDB title+year already lives on a sibling row in the
	// library (year-suffix duplicate from the scanner's folder parser,
	// or a hand-renamed re-scan that produced two rows for the same film).
	if result.TMDBID != 0 {
		if survivor, sErr := e.updater.GetItemByTMDBID(ctx, item.LibraryID, result.TMDBID); sErr == nil && survivor != nil && survivor.ID != item.ID {
			e.logger.InfoContext(ctx, "enrich: merging movie into existing canonical row",
				"loser_id", item.ID, "survivor_id", survivor.ID,
				"loser_title", item.Title, "survivor_title", survivor.Title,
				"tmdb_id", result.TMDBID)
			if err := e.updater.MergeIntoTopLevel(ctx, item.ID, survivor.ID, item.Type); err != nil {
				return fmt.Errorf("merge into canonical row: %w", err)
			}
			return nil
		}
	}

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

	// Per-library is_anime flag flips agent priority. Anime libraries
	// get AniList first (richer anime metadata even when TMDB has
	// the show); non-anime libraries keep TMDB-first (avoids spurious
	// AniList matches for Western shows that share a title with an
	// anime).
	result := e.searchShow(ctx, agent, searchTitle, year, e.libraryIsAnime(ctx, item.LibraryID))
	if result == nil {
		e.logger.InfoContext(ctx, "no metadata source matched show",
			"title", item.Title)
		// Fall back to NFO-only when none of TMDB / AniList / TVDB
		// matched.
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
	if result.PosterURL == "" {
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
	if result.AniListID != 0 {
		anilistID := result.AniListID
		p.AniListID = &anilistID
		// Walk the AniList relations graph to compute a stable
		// franchise_id (smallest AniList ID in the PREQUEL/SEQUEL/PARENT
		// connected subgraph, filtered to TV / TV_SHORT). This is what
		// lets the UI optionally collapse "Dr. STONE" / "Dr. STONE
		// SCIENCE FRONTIERS" / "Dr. STONE: STONE WARS" into a single
		// franchise card without us regexing titles. Failures (offline,
		// rate-limited) are non-fatal — leave franchise_id nil and a
		// later refresh-missing-art / re-scan will fill it in.
		if fid := e.computeFranchiseID(ctx, anilistID); fid != 0 {
			f := fid
			p.FranchiseID = &f
		}
	}
	if result.MALID != 0 {
		malID := result.MALID
		p.MALID = &malID
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

	// Pre-flight merge: if another row in this library is already
	// attached to the canonical TMDB id, the UpdateItemMetadata below
	// would crash with the idx_media_items_library_type_title_year
	// unique-constraint violation (the operator-visible "100 Day Hotel
	// Challenge" / "Battlestar Galactica 1978" / "1923 2022" symptom on
	// QA — every cascade re-enrich attempt collided because the year-
	// suffix duplicate row tried to upgrade to canonical title+year and
	// lost to the already-canonical sibling). Reparent this row's
	// children onto the survivor and stop — same shape as matchShow's
	// pre-flight, lifted into the auto-enrich path.
	if result.TMDBID != 0 {
		if survivor, sErr := e.updater.GetItemByTMDBID(ctx, item.LibraryID, result.TMDBID); sErr == nil && survivor != nil && survivor.ID != item.ID {
			e.logger.InfoContext(ctx, "enrich: merging into existing canonical row",
				"loser_id", item.ID, "survivor_id", survivor.ID,
				"loser_title", item.Title, "survivor_title", survivor.Title,
				"tmdb_id", result.TMDBID)
			if err := e.updater.MergeIntoTopLevel(ctx, item.ID, survivor.ID, item.Type); err != nil {
				return fmt.Errorf("merge into canonical row: %w", err)
			}
			return nil
		}
	}

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

	// Per-season AniList linking for anime franchises. Anime cours are
	// distinct Media rows on AniList — Solo Leveling S1 (153406) and
	// S2 (151807) have different streamingEpisodes lists. We walk the
	// PREQUEL/SEQUEL chain from whatever Media row matched and map
	// each row to a Season under our show by start-year ordering.
	// Without this, every season cascades against the same anilist_id
	// and Season 1 silently picks up Season 2's episode titles via
	// position fallback.
	if result.AniListID != 0 {
		e.attachAniListFranchiseToSeasons(ctx, item.ID, result.AniListID)
	}

	// After enriching the show, trigger enrichment for its seasons.
	e.enrichShowChildren(ctx, agent, item, file)

	return nil
}

// enrichShowChildren enriches all seasons (and their episodes) under a show
// after the show itself has been enriched. This ensures that when a show is
// first scanned, all its seasons and episodes also get TMDB metadata.
func (e *Enricher) enrichShowChildren(ctx context.Context, agent metadata.Agent, show *media.Item, file *media.File) {
	// Re-load the show to pick up the IDs we just saved (any of TMDB,
	// TVDB, or AniList unlocks the cascade — enrichSeason / enrichEpisode
	// know how to fall back across providers).
	show, err := e.updater.GetItem(ctx, show.ID)
	if err != nil {
		return
	}
	if show.TMDBID == nil && show.TVDBID == nil && show.AniListID == nil {
		return
	}

	seasons, err := e.updater.ListChildren(ctx, show.ID)
	if err != nil {
		return
	}
	for i := range seasons {
		s := &seasons[i]
		if s.Type != "season" {
			continue
		}
		// Scan-time path skips already-postered seasons (their TMDB
		// season metadata is rarely worth re-fetching); manual
		// refresh (forceReenrich) re-runs every season so the
		// episode cascade underneath also re-runs.
		if !shouldForceReenrich(ctx) && s.PosterPath != nil {
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
	if show.TMDBID == nil && show.TVDBID == nil && show.AniListID == nil {
		// Show hasn't been enriched yet; season enrichment will happen when
		// the show is enriched (via enrichShowChildren).
		return nil
	}

	// TMDB-side season metadata only fires when we have a TMDB ID.
	// Anime shows matched on AniList alone skip the season fetch (AniList
	// doesn't model anime by season anyway — sequels are separate Media
	// rows) but still cascade to episode-level enrichment below.
	//
	// preloadedEpisodes carries the season's full episode list out to
	// the cascade so each episode can skip its own TMDB round-trip.
	// Empty when TMDB wasn't called or returned nothing — the cascade
	// falls back to TVDB / AniList per episode in that case.
	var preloadedEpisodes []metadata.EpisodeResult
	if show.TMDBID != nil {
		result, err := agent.GetSeason(ctx, *show.TMDBID, *item.Index)
		if err != nil {
			e.logger.InfoContext(ctx, "tmdb get season found no result",
				"show_tmdb_id", *show.TMDBID, "season", *item.Index, "err", err)
		} else {
			preloadedEpisodes = result.Episodes
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
		}
	}

	// Cascade: enrich episodes under this season. Runs even when TMDB
	// season metadata was skipped above so anime libraries with only an
	// AniList ID still get per-episode titles + thumbnails from the
	// streamingEpisodes fallback in enrichEpisode. preloadedEpisodes
	// is the batch from the season fetch above; per-episode TMDB calls
	// only fire when this slice doesn't cover an episode.
	e.enrichSeasonChildren(ctx, agent, show, item, file, preloadedEpisodes)

	return nil
}

// enrichSeasonChildren enriches all episodes under a season after the season
// has been enriched. Runs whenever the show has *any* provider ID
// (TMDB, TVDB, or AniList) — enrichEpisode itself handles the
// fallback chain across providers, so the cascade no longer needs
// to gate on TMDB.
func (e *Enricher) enrichSeasonChildren(ctx context.Context, agent metadata.Agent, show *media.Item, season *media.Item, file *media.File, preloaded []metadata.EpisodeResult) {
	if season.Index == nil {
		return
	}
	if show.TMDBID == nil && show.TVDBID == nil && show.AniListID == nil {
		return
	}

	// Index the preloaded TMDB season episodes by episode number so
	// each child can grab its own row in O(1) without another network
	// call. preloaded is empty when TMDB wasn't queried (no show TMDB
	// ID) or returned no data — the cascade falls through to per-
	// episode TVDB / AniList lookups in that case.
	preloadedByNum := make(map[int]*metadata.EpisodeResult, len(preloaded))
	for i := range preloaded {
		ep := &preloaded[i]
		preloadedByNum[ep.EpisodeNum] = ep
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
		// Skip episodes that already have both metadata and artwork —
		// the scan-time cascade hits every episode every time, and
		// re-fetching identical data on every rescan would burn
		// rate limits with no behavioural gain. Manual refresh
		// (forceReenrich set in ctx) bypasses the skip so prior
		// wrong data — e.g. Season 1 episodes that picked up Season
		// 2 titles before the per-season anilist_id wiring landed —
		// gets replaced.
		if !shouldForceReenrich(ctx) && ep.Summary != nil && ep.ThumbPath != nil {
			continue
		}
		var prefetched *metadata.EpisodeResult
		if ep.Index != nil {
			prefetched = preloadedByNum[*ep.Index]
		}
		if err := e.enrichEpisode(ctx, agent, ep, file, prefetched); err != nil {
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

// enrichEpisode applies metadata to a single episode item. When prefetched
// is non-nil (the common path — set by enrichSeasonChildren from the
// season's bulk fetch), no TMDB GetEpisode call is made: the data was
// already pulled in the parent season's response. The TVDB and AniList
// fallback paths still fire when prefetched is nil and the show carries
// the corresponding ID, so anime-only and TVDB-only libraries still get
// per-episode data. Standalone re-enrichment (admin "re-enrich item")
// passes prefetched=nil and pays the round-trip — fine because it's a
// one-off, not the bulk scan path.
func (e *Enricher) enrichEpisode(ctx context.Context, agent metadata.Agent, item *media.Item, file *media.File, prefetched *metadata.EpisodeResult) error {
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
	if (show.TMDBID == nil && show.TVDBID == nil && show.AniListID == nil) || show.PosterPath == nil {
		// Show hasn't been enriched yet, or is missing artwork — enrich it.
		// enrichShow will cascade down to seasons and episodes.
		if err := e.enrichShow(ctx, agent, show, file); err != nil {
			e.logger.WarnContext(ctx, "cascade show enrich from episode failed",
				"show_id", show.ID, "err", err)
		}
		// Re-load show to pick up TMDB / TVDB / AniList IDs.
		show, err = e.updater.GetItem(ctx, show.ID)
		if err != nil || (show.TMDBID == nil && show.TVDBID == nil && show.AniListID == nil) {
			return nil // show enrichment failed; no provider IDs to look up against
		}
	}

	// Episode metadata sources, in order:
	//   1. TMDB by `show.TMDBID`         (best for mainstream shows + cross-anime)
	//   2. TVDB by `show.TVDBID`         (anime episode data; mainstream-show fallback)
	//
	// AniList isn't in the chain — its `streamingEpisodes` field carries
	// only title/thumbnail/url, not the per-episode summaries detail
	// pages need. AniList catches the show match (so the scanner records
	// `anilist_id` and the cross-harvested TMDB / TVDB ID); per-episode
	// enrichment then runs against whichever of those two IDs is set.
	//
	// The pre-fix logic returned early when `show.TMDBID == nil` —
	// orphaning episodes on anime libraries that AniList matched (and
	// for which we cross-harvested only TVDB). Now: TVDB stands alone
	// as a primary source when TMDB ID is absent, instead of being
	// gated behind a failed TMDB call.
	var (
		result    *metadata.EpisodeResult
		usedTVDB  bool
	)

	var tvdbClient TVDBFallback
	if e.tvdbFn != nil {
		tvdbClient = e.tvdbFn()
	}

	switch {
	case prefetched != nil:
		// Bulk path: data came from the parent season's GetSeason call.
		// No network round-trip.
		result = prefetched
	case show.TMDBID != nil:
		// Standalone path (no preloaded season data). Pay the per-
		// episode round-trip.
		result, err = agent.GetEpisode(ctx, *show.TMDBID, *season.Index, *item.Index)
		if err != nil {
			e.logger.InfoContext(ctx, "tmdb get episode found no result",
				"show_tmdb_id", *show.TMDBID,
				"season", *season.Index,
				"episode", *item.Index,
				"err", err)
			result = nil // fall through to TVDB
		}
	}
	if result == nil && tvdbClient != nil && show.TVDBID != nil {
		result, err = tvdbClient.GetEpisode(ctx, *show.TVDBID, *season.Index, *item.Index)
		if err != nil {
			e.logger.InfoContext(ctx, "tvdb get episode found no result",
				"show_tvdb_id", *show.TVDBID,
				"season", *season.Index,
				"episode", *item.Index,
				"err", err)
			result = nil // fall through to AniList
		}
		if result != nil {
			usedTVDB = true
			e.logger.InfoContext(ctx, "tvdb episode lookup",
				"show_tvdb_id", *show.TVDBID,
				"season", *season.Index,
				"episode", *item.Index,
				"title", result.Title)
		}
	}
	// AniList streamingEpisodes — last-resort fallback when neither TMDB
	// nor TVDB returned data, but the show has an anilist_id (anime
	// libraries where the operator has not configured a TMDB / TVDB
	// key, which is the common case post-AniList primary). Gives users
	// at least a title + thumbnail per episode; AniList doesn't carry
	// per-episode summaries.
	// Prefer the season's anilist_id when set: anime franchises split
	// each cour into its own AniList Media row (Solo Leveling S1 =
	// 153406, S2 = 151807), and the show row holds whichever cour
	// matched the title search — usually the wrong one for at least
	// one season. attachAniListFranchiseToSeasons populates per-
	// season IDs at show-match time so this lookup hits the right
	// streamingEpisodes list per season.
	anilistLookupID := show.AniListID
	if season.AniListID != nil {
		anilistLookupID = season.AniListID
	}
	if result == nil && anilistLookupID != nil && e.anilistFn != nil {
		if al := e.anilistFn(); al != nil {
			eps, alErr := e.fetchAniListEpisodes(ctx, al, *anilistLookupID)
			if alErr != nil {
				e.logger.InfoContext(ctx, "anilist get episodes failed",
					"anilist_id", *anilistLookupID,
					"err", alErr)
			} else if matched := pickAniListEpisode(eps, *item.Index); matched != nil {
				result = matched
				e.logger.InfoContext(ctx, "anilist episode fallback",
					"anilist_id", *anilistLookupID,
					"episode", *item.Index,
					"title", result.Title,
					"has_thumb", result.ThumbURL != "")
			}
		}
	}
	if result == nil {
		// All three providers returned nothing usable; episode stays
		// at its filename-derived title.
		return nil
	}
	_ = usedTVDB // reserved for future provider-attribution metrics

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

// attachAniListFranchiseToSeasons maps AniList franchise rows onto
// our Season items. Anime franchises on AniList are split: each cour
// is its own Media (Solo Leveling = 153406, Solo Leveling S2 = 151807)
// joined by PREQUEL/SEQUEL edges. This walk fetches the chain from
// the matched row, sorts by startYear ascending, and assigns:
//
//	franchise[0].AniListID  →  Season 1.AniListID
//	franchise[1].AniListID  →  Season 2.AniListID
//	…
//
// Mapping by index means a multi-season anime where seasons are
// numbered 1..N matches cleanly. Anime with non-sequential seasons
// (e.g. an OVA breaking the chain) gets a best-effort attach — the
// season cascade still works, just possibly aimed at the wrong
// AniList row for that one season. Better than the alternative of
// every season pointing at the matched-show row.
//
// Failures are non-fatal: a network error or rate limit just leaves
// the seasons un-attached, in which case the per-episode lookup
// falls back to the show-level anilist_id (today's behavior). Logged
// so an operator can spot persistent walk failures.
// computeFranchiseID walks the AniList relations graph from
// [anilistID] and returns the smallest AniList ID in the connected
// PREQUEL/SEQUEL/PARENT subgraph (filtered to TV / TV_SHORT formats
// by the AniList client). That smallest ID is a stable, deterministic
// franchise key — every cour of the same franchise yields the same
// number regardless of which one we walked from, so the UI can
// "Group by franchise" with a simple equality test.
//
// Returns 0 (treated as nil franchise_id by the caller) when:
//   - the AniList client isn't wired
//   - the walk errors (offline, rate-limited)
//   - the walk returns nothing — fall back to the input ID itself
//     so a one-off (no relations) anime still gets a franchise_id
//     equal to its own AniList ID and clusters cleanly with itself.
func (e *Enricher) computeFranchiseID(ctx context.Context, anilistID int) int {
	if e.anilistFn == nil {
		return anilistID
	}
	al := e.anilistFn()
	if al == nil {
		return anilistID
	}
	franchise, err := al.GetAnimeFranchise(ctx, anilistID)
	if err != nil {
		e.logger.InfoContext(ctx, "anilist franchise walk failed for franchise_id",
			"anilist_id", anilistID, "err", err)
		// Conservative on failure: leave the column nil so a later
		// refresh can fill it in with a real walked value rather
		// than a possibly-wrong self-reference.
		return 0
	}
	if len(franchise) == 0 {
		// No relations — anime is a singleton. Use its own ID so it
		// still has a franchise_id, just one with cardinality 1.
		return anilistID
	}
	smallest := anilistID
	for _, m := range franchise {
		if m.AniListID > 0 && m.AniListID < smallest {
			smallest = m.AniListID
		}
	}
	return smallest
}

func (e *Enricher) attachAniListFranchiseToSeasons(ctx context.Context, showID uuid.UUID, anilistID int) {
	if e.anilistFn == nil {
		return
	}
	al := e.anilistFn()
	if al == nil {
		return
	}
	franchise, err := al.GetAnimeFranchise(ctx, anilistID)
	if err != nil {
		e.logger.InfoContext(ctx, "anilist franchise walk failed",
			"show_id", showID, "anilist_id", anilistID, "err", err)
		return
	}
	if len(franchise) == 0 {
		return
	}

	// Show title is used to suppress redundant season titles. AniList
	// returns the original-cour Media row with the franchise's base
	// title (e.g. "Solo Leveling" for S1) — writing that into the
	// season would just duplicate the show's name in the tab. Reserve
	// the franchise title for cours whose AniList Media has a
	// distinct subtitle ("Solo Leveling Season 2: Arise from the
	// Shadow"), and fall back to the scanner-derived "Season N" for
	// the base cour.
	show, err := e.updater.GetItem(ctx, showID)
	if err != nil || show == nil {
		return
	}
	showTitle := strings.TrimSpace(show.Title)

	seasons, err := e.updater.ListChildren(ctx, showID)
	if err != nil {
		return
	}
	// Iterate seasons in scanner-order (Season 1, Season 2, …) and
	// attach by index. ListChildren returns in `index` order today —
	// re-sort defensively in case the ordering changes.
	type seasonRef struct {
		id  uuid.UUID
		idx int
	}
	ordered := make([]seasonRef, 0, len(seasons))
	for i := range seasons {
		s := &seasons[i]
		if s.Type != "season" || s.Index == nil {
			continue
		}
		ordered = append(ordered, seasonRef{id: s.ID, idx: *s.Index})
	}
	// Sort by season index ascending so seasons[0] is S1 regardless
	// of underlying query order.
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			if ordered[j].idx < ordered[i].idx {
				ordered[i], ordered[j] = ordered[j], ordered[i]
			}
		}
	}

	for i, sref := range ordered {
		if i >= len(franchise) {
			break
		}
		// UpdateItemMetadata's SQL rewrites title / sort_title without
		// COALESCE, so we must pass the current values through to avoid
		// blanking them. Re-load to get the live title (the cached row
		// in `seasons` is fine, but a separate GetItem is cheap and
		// keeps this helper independent of caller staleness).
		current, err := e.updater.GetItem(ctx, sref.id)
		if err != nil || current == nil {
			continue
		}
		anilistID := franchise[i].AniListID
		malID := franchise[i].MalID

		// Pick a season title that distinguishes cours visually.
		// franchise[i].Title is the AniList Media row's title — for
		// the base cour it equals the show title (Solo Leveling S1's
		// AniList row IS "Solo Leveling"); for sequel cours it carries
		// the subtitle ("Solo Leveling Season 2: Arise from the Shadow").
		// Use the franchise title only when it adds information beyond
		// the show name; otherwise keep the existing title (which the
		// UI fallback renders as "Season N" when blank).
		title := current.Title
		sortTitle := current.SortTitle
		franchiseTitle := strings.TrimSpace(franchise[i].Title)
		if franchiseTitle != "" && !strings.EqualFold(franchiseTitle, showTitle) {
			title = franchiseTitle
			sortTitle = franchiseTitle
		}
		p := media.UpdateItemMetadataParams{
			ID:        sref.id,
			Title:     title,
			SortTitle: sortTitle,
			AniListID: &anilistID,
		}
		if malID != 0 {
			p.MALID = &malID
		}
		if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
			e.logger.WarnContext(ctx, "season anilist link failed",
				"season_id", sref.id, "anilist_id", anilistID, "err", err)
			continue
		}
		e.logger.InfoContext(ctx, "season anilist linked",
			"season_id", sref.id, "season_num", sref.idx,
			"anilist_id", anilistID, "anilist_title", franchise[i].Title)
	}
}

// fetchAniListEpisodes returns the streamingEpisodes list for the
// given AniList ID, collapsing concurrent calls with singleflight and
// caching the result for aniListEpsTTL. AniList's public endpoint is
// rate-limited at 90 req/min — a single per-show cascade can fire
// 25+ episode lookups in <2 seconds, which trips the limit and
// silently drops metadata for every episode after the first few.
//
// On success the cache holds the slice (which may be nil if AniList
// returned no streamingEpisodes data — distinct from "not fetched
// yet"). On error the cache is NOT populated, so a transient HTTP
// failure / 429 doesn't pin a poisoned-empty result for an hour.
func (e *Enricher) fetchAniListEpisodes(ctx context.Context, al AniListAgent, anilistID int) ([]metadata.EpisodeResult, error) {
	if v, ok := e.aniListEpsCache.Load(anilistID); ok {
		entry := v.(aniListEpsCacheEntry)
		if time.Since(entry.fetchedAt) < aniListEpsTTL {
			return entry.eps, nil
		}
	}

	key := fmt.Sprintf("anilist-eps-%d", anilistID)
	v, err, _ := e.aniListEpsSF.Do(key, func() (interface{}, error) {
		// Re-check the cache inside singleflight: a concurrent
		// caller may have already populated it while we were waiting.
		if v, ok := e.aniListEpsCache.Load(anilistID); ok {
			entry := v.(aniListEpsCacheEntry)
			if time.Since(entry.fetchedAt) < aniListEpsTTL {
				return entry.eps, nil
			}
		}
		eps, err := al.GetAnimeEpisodes(ctx, anilistID)
		if err != nil {
			return nil, err
		}
		e.aniListEpsCache.Store(anilistID, aniListEpsCacheEntry{
			eps:       eps,
			fetchedAt: time.Now(),
		})
		return eps, nil
	})
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	return v.([]metadata.EpisodeResult), nil
}

// pickAniListEpisode finds the streamingEpisodes entry that maps to
// `target` (an absolute episode index in our anime model — fansub-
// flat folders synthesize Season 1, so item.Index already carries
// the absolute number).
//
// Match strategy, in order:
//
//  1. Exact: an entry whose parsed EpisodeNum == target. AniList
//     titles like "Episode 13 - You Aren't E-Rank" parse cleanly.
//  2. Position fallback: when target is 1..len(eps), use eps[target-1].
//     Real-world AniList lists mix "Episode N - Title" entries with
//     bare-title entries on the same show — position-1 indexing is
//     the canonical layout AniList exports them in. The position
//     match is the only way to attach metadata to bare-title rows.
//
// Returns nil only when target is out of range or the list is empty.
// Risk note: if AniList's list happens to be out-of-order (rare —
// specials are split into a separate Media row, not interleaved),
// position-match attaches the wrong title. We accept that since the
// alternative is "no episode metadata at all" for every bare-title
// entry, which is the common case and a worse user experience.
func pickAniListEpisode(eps []metadata.EpisodeResult, target int) *metadata.EpisodeResult {
	if target <= 0 || len(eps) == 0 {
		return nil
	}
	for i := range eps {
		if eps[i].EpisodeNum == target {
			return &eps[i]
		}
	}
	if target-1 < len(eps) {
		return &eps[target-1]
	}
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
	// Manual refresh = force re-enrichment of children. The cascade
	// normally skips episodes that already have summary+thumb (a
	// scan-time efficiency) but a manual refresh is exactly the case
	// where the operator wants the existing data REPLACED — TMDB key
	// just got fixed, the show was re-matched, the franchise walk
	// just landed correct per-season anilist_ids, etc. Mark the ctx
	// and let the cascade-level skip honour it.
	return e.Enrich(withForceReenrich(ctx), item, file)
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
	// Pre-flight: is another top-level row in the same library already
	// attached to this TMDB id? If yes, this Fix Match call is really a
	// merge — reparent the current row's children onto the survivor and
	// soft-delete the current row, instead of trying to update the title
	// to the canonical TMDB title (which would crash with the
	// idx_media_items_library_type_title_year unique-constraint
	// violation that the operator hit on QA).
	if survivor, err := e.updater.GetItemByTMDBID(ctx, item.LibraryID, tmdbID); err == nil && survivor != nil && survivor.ID != item.ID {
		e.logger.InfoContext(ctx, "fix match: merging into existing canonical row",
			"loser_id", item.ID, "survivor_id", survivor.ID,
			"loser_title", item.Title, "survivor_title", survivor.Title,
			"tmdb_id", tmdbID)
		if err := e.updater.MergeIntoTopLevel(ctx, item.ID, survivor.ID, item.Type); err != nil {
			return fmt.Errorf("merge into canonical row: %w", err)
		}
		return nil
	}

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
	// Pre-flight merge: same shape as matchShow, see comment there for
	// the unique-constraint reasoning.
	if survivor, err := e.updater.GetItemByTMDBID(ctx, item.LibraryID, tmdbID); err == nil && survivor != nil && survivor.ID != item.ID {
		e.logger.InfoContext(ctx, "fix match: merging into existing canonical row",
			"loser_id", item.ID, "survivor_id", survivor.ID,
			"loser_title", item.Title, "survivor_title", survivor.Title,
			"tmdb_id", tmdbID)
		if err := e.updater.MergeIntoTopLevel(ctx, item.ID, survivor.ID, item.Type); err != nil {
			return fmt.Errorf("merge into canonical row: %w", err)
		}
		return nil
	}

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

// searchShow runs the show-metadata agent chain in the order
// dictated by the library's is_anime flag and returns the first
// non-nil result, or nil when every agent missed.
//
//   - is_anime=true (anime library):  AniList → TMDB → TVDB
//   - is_anime=false (default):       TMDB    → AniList → TVDB
//
// AniList runs primary on anime libraries because its anime metadata
// is usually richer than TMDB's even when both have the show; on
// non-anime libraries it stays as a fallback to avoid spurious
// matches against Western shows that share a title with an anime.
// TVDB stays the universal last-ditch fallback in both orders.
func (e *Enricher) searchShow(ctx context.Context, agent metadata.Agent, title string, year int, isAnimeLibrary bool) *metadata.TVShowResult {
	tryTMDB := func() *metadata.TVShowResult {
		r, err := agent.SearchTV(ctx, title, year)
		if err != nil || r == nil {
			e.logger.InfoContext(ctx, "tmdb tv search found no result",
				"title", title, "err", err)
			return nil
		}
		return r
	}
	tryAniList := func() *metadata.TVShowResult {
		return e.anilistShowFallback(ctx, title, year, nil)
	}
	tryTVDB := func() *metadata.TVShowResult {
		return e.tvdbShowFallback(ctx, title, year, nil)
	}

	if isAnimeLibrary {
		// Anime libraries need AniList primary for text fields AND
		// TMDB / TVDB IDs harvested for per-episode enrichment —
		// AniList doesn't have rich per-episode metadata, so we still
		// rely on TMDB.GetEpisode (and TVDB as fallback) at episode
		// time. Without harvesting these IDs at show-match time, the
		// episode rows have no description / air date / rating
		// because there's no agent ID to dispatch GetEpisode against.
		if primary := tryAniList(); primary != nil {
			// Best-effort cross-references. Each agent miss leaves the
			// corresponding ID at 0; the show row still gets the
			// AniList text fields and IDs we already have.
			if tmdb := tryTMDB(); tmdb != nil {
				if primary.TMDBID == 0 {
					primary.TMDBID = tmdb.TMDBID
				}
				if primary.IMDBID == "" {
					primary.IMDBID = tmdb.IMDBID
				}
			}
			if tvdb := tryTVDB(); tvdb != nil {
				if primary.TVDBID == 0 {
					primary.TVDBID = tvdb.TVDBID
				}
			}
			return primary
		}
		// AniList missed — fall through to TMDB then TVDB. No
		// cross-harvest needed because TMDB has TVDB-equivalent
		// per-episode coverage on its own (and the AniList agent
		// we already tried is the one we'd be harvesting anyway).
		if r := tryTMDB(); r != nil {
			return r
		}
		return tryTVDB()
	}

	// Non-anime: TMDB primary, AniList for catches TMDB doesn't
	// cover (rare anime in non-anime libraries), TVDB as last-ditch.
	for _, try := range []func() *metadata.TVShowResult{tryTMDB, tryAniList, tryTVDB} {
		if r := try(); r != nil {
			return r
		}
	}
	return nil
}

// anilistShowFallback asks AniList for a show. Used as the first
// fallback when TMDB returns no result (most common cause: anime not
// in TMDB's catalogue, or thin TMDB metadata that AniList covers
// better). Returns nil when AniList isn't configured or has no
// match. base is reserved for the same merge-on-top-of-TMDB-result
// pattern that tvdbShowFallback uses; it's accepted but unused
// today — future work merges AniList-side ratings / genres / banner
// over a TMDB base when both match.
func (e *Enricher) anilistShowFallback(ctx context.Context, title string, year int, _ *metadata.TVShowResult) *metadata.TVShowResult {
	if e.anilistFn == nil {
		return nil
	}
	client := e.anilistFn()
	if client == nil {
		return nil
	}
	tv, err := client.SearchAnime(ctx, title, year)
	if err == nil && tv != nil {
		e.logger.InfoContext(ctx, "anilist anime search matched",
			"title", title, "anilist_id", tv.AniListID, "mal_id", tv.MALID)
		return tv
	}

	// Live search missed. Manami's offline-DB synonyms list
	// resolves fansub-style folder names ("Akame ga Kill Theater" →
	// AniList ID 20988 for "Akame ga Kill! Gaiden: Theater") that
	// the live fuzzy `Media(search:$q)` field rejects. Skip the
	// recovery path when no offline DB is wired.
	if e.animeDBFn != nil {
		if db := e.animeDBFn(); db != nil {
			if entry, ok := db.Lookup(title); ok && entry.AniListID > 0 {
				resolved, byIDErr := client.GetAnimeByID(ctx, entry.AniListID)
				if byIDErr == nil && resolved != nil {
					e.logger.InfoContext(ctx, "anilist match recovered via offline db",
						"title", title, "anilist_id", entry.AniListID,
						"matched_title", entry.Title)
					return resolved
				}
				e.logger.InfoContext(ctx, "offline-db hit but anilist by-id lookup failed",
					"title", title, "anilist_id", entry.AniListID, "err", byIDErr)
			}
		}
	}

	e.logger.InfoContext(ctx, "anilist anime search found no match",
		"title", title, "year", year, "err", err)
	return nil
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
