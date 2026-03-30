package metadata

import "context"

// ArtistResult holds artist metadata from a music metadata provider.
type ArtistResult struct {
	Name      string
	ThumbURL  string // artist photo / thumbnail
	FanartURL string // artist backdrop
	Biography string
}

// AlbumResult holds album metadata from a music metadata provider.
type AlbumResult struct {
	Name        string
	ThumbURL    string // album cover art
	Description string
	Year        int
	Genres      []string
}

// MusicAgent is implemented by music metadata providers (e.g. TheAudioDB).
type MusicAgent interface {
	SearchArtist(ctx context.Context, name string) (*ArtistResult, error)
	SearchAlbum(ctx context.Context, artistName, albumName string) (*AlbumResult, error)
}
