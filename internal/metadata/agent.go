// Package metadata defines the Agent interface and shared types for metadata
// providers (TMDB, TVDB, MusicBrainz).
package metadata

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MovieResult holds movie metadata returned by an agent.
type MovieResult struct {
	TMDBID        int
	IMDBID        string
	Title         string
	OriginalTitle string
	Year          int
	Summary       string
	Tagline       string
	Rating        float64
	ContentRating string
	DurationMS    int64
	Genres        []string
	ReleaseDate   time.Time
	PosterURL     string
	FanartURL     string
}

// TVShowResult holds TV show metadata returned by an agent.
type TVShowResult struct {
	TMDBID        int
	TVDBID        int // TheTVDB series ID (0 if unknown)
	IMDBID        string
	Title         string
	OriginalTitle string
	FirstAirYear  int
	Summary       string
	Rating        float64
	ContentRating string
	Genres        []string
	PosterURL     string
	FanartURL     string
}

// SeasonResult holds season metadata.
type SeasonResult struct {
	Number    int
	Name      string
	Summary   string
	AirDate   time.Time
	PosterURL string
}

// EpisodeResult holds episode metadata.
type EpisodeResult struct {
	ShowTMDBID int
	SeasonNum  int
	EpisodeNum int
	Title      string
	Summary    string
	AirDate    time.Time
	DurationMS int64
	Rating     float64
	ThumbURL   string
}

// Agent is the interface implemented by all metadata providers.
// Agents are called by the scanner and the metadata refresh worker.
type Agent interface {
	// SearchMovie looks up a movie by title and year.
	SearchMovie(ctx context.Context, title string, year int) (*MovieResult, error)
	// SearchTV looks up a TV show by title. year=0 means any year.
	SearchTV(ctx context.Context, title string, year int) (*TVShowResult, error)
	// SearchTVCandidates returns multiple TV show matches for manual selection.
	SearchTVCandidates(ctx context.Context, query string) ([]TVShowResult, error)
	// GetSeason fetches season metadata for a TV show.
	GetSeason(ctx context.Context, showTMDBID, seasonNum int) (*SeasonResult, error)
	// GetEpisode fetches episode metadata.
	GetEpisode(ctx context.Context, showTMDBID, seasonNum, episodeNum int) (*EpisodeResult, error)
	// RefreshMovie refreshes metadata for an existing media item by TMDB ID.
	RefreshMovie(ctx context.Context, tmdbID int) (*MovieResult, error)
	// RefreshTV refreshes TV show metadata by TMDB ID.
	RefreshTV(ctx context.Context, tmdbID int) (*TVShowResult, error)
}

// ArtworkDownloader downloads and stores artwork alongside media files.
type ArtworkDownloader interface {
	DownloadPoster(ctx context.Context, itemID uuid.UUID, url string) (relativePath string, err error)
	DownloadFanart(ctx context.Context, itemID uuid.UUID, url string) (relativePath string, err error)
	DownloadThumb(ctx context.Context, itemID uuid.UUID, url string) (relativePath string, err error)
}
