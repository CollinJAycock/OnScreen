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
	TMDBID    int
	IMDBID    string
	AniListID int // AniList Media ID (0 if unknown — anime films only)
	MALID     int // MyAnimeList ID (0 if unknown — anime films only)
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
	TMDBID    int
	TVDBID    int    // TheTVDB series ID (0 if unknown)
	IMDBID    string
	AniListID int // AniList Media ID (0 if unknown — anime only)
	MALID     int // MyAnimeList ID (0 if unknown — anime only)
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

// MangaResult holds manga metadata returned by an agent (AniList).
// One row per series — chapter / volume splits live in the
// scanner's hierarchy (manga_volume / manga_chapter under the
// matched book_series row), not in this struct.
//
// Mangaka splits author + artist when AniList exposes both staff
// roles; ReadingDirection comes from countryOfOrigin (JP→rtl,
// KR/CN→ttb for webtoons / manhua, otherwise ltr). Demographic
// (Shōnen / Shōjo / Seinen / Josei) and Magazine (Weekly Shōnen
// Jump etc.) ride on Tags + Source — exposed as tags rather than
// dedicated columns to avoid a schema explosion.
type MangaResult struct {
	AniListID  int
	MALID      int
	Title         string
	OriginalTitle string // native (Japanese / Korean / Chinese) title
	StartYear     int
	Summary       string
	Rating        float64
	ContentRating string

	// Mangaka. Author writes the story; Artist illustrates. Often
	// the same person; AniList's staff list can split or fuse them.
	// Empty when AniList didn't expose a staff role match.
	Author string
	Artist string

	// SerializationStatus: "FINISHED" | "RELEASING" | "NOT_YET_RELEASED" |
	// "CANCELLED" | "HIATUS" — AniList's MediaStatus enum verbatim so the
	// UI can render its own labels.
	SerializationStatus string

	// Demographic + Magazine come through as tags. Genres are the
	// AniList genre list; tags carry the more specific classifications
	// (Shounen, Slice of Life, etc.).
	Genres []string
	Tags   []string

	// ReadingDirection: "ltr" | "rtl" | "ttb" derived from
	// countryOfOrigin. Reader uses this as the default unless the
	// per-item override is set.
	ReadingDirection string

	// VolumeCount / ChapterCount from AniList. -1 = ongoing
	// (status RELEASING) so we don't pretend an in-flight series
	// has a final count.
	Volumes  int
	Chapters int

	PosterURL string
	BannerURL string
}

// CreditMember is one cast or crew entry on a media item.
// Cast entries set Character and Order; crew entries set Role and Job.
type CreditMember struct {
	TMDBID      int
	Name        string
	ProfilePath string // TMDB-relative; empty if unknown
	Character   string // cast only
	Order       int    // cast only; lower = more prominent
	Role        string // crew only: "director" | "writer" | "producer" | "creator"
	Job         string // crew only: original TMDB job title
}

// CreditsResult bundles cast and crew for a single movie or show.
type CreditsResult struct {
	Cast []CreditMember
	Crew []CreditMember
}

// PersonResult holds biographical metadata for a single person.
type PersonResult struct {
	TMDBID       int
	Name         string
	Bio          string
	ProfilePath  string
	Birthday     time.Time
	Deathday     time.Time
	PlaceOfBirth string
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

// PosterCandidate is one image variant returned by a metadata agent for the
// manual poster-picker workflow. Width is the source pixel width (so the UI
// can size previews accurately) and Language is the ISO 639-1 code or nil
// for language-agnostic art.
type PosterCandidate struct {
	URL      string  `json:"url"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Language *string `json:"language,omitempty"`
	Vote     float64 `json:"vote"`
}

// PosterLister returns every poster variant a metadata provider has for a
// given show or movie. Implemented by the TMDB client; kept as its own
// interface so other agents (TVDB, MusicBrainz) can opt out.
type PosterLister interface {
	ListMoviePostersForID(ctx context.Context, tmdbID int) ([]PosterCandidate, error)
	ListTVPostersForID(ctx context.Context, tmdbID int) ([]PosterCandidate, error)
}
