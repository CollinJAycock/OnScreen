package tmdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/onscreen/onscreen/internal/metadata"
)

// IsNotFound reports whether err is (or wraps) a TMDB 404 — the id does not
// exist on TMDB. Callers use it to tell "definitively nothing there" apart
// from transient failures worth retrying.
func IsNotFound(err error) bool { return errors.Is(err, errNotFound) }

// MovieCollection returns the TMDB collection a movie belongs to, or nil when
// it belongs to none. It issues exactly the request RefreshMovie does (same
// path and parameters), so the two share a disk-cache entry: a movie that was
// just enriched or Fix-Matched answers from cache, and a backfill lookup warms
// the cache for the next refresh.
func (c *Client) MovieCollection(ctx context.Context, movieTMDBID int) (*metadata.CollectionRef, error) {
	res, err := c.RefreshMovie(ctx, movieTMDBID)
	if err != nil {
		return nil, err
	}
	return res.Collection, nil
}

// GetCollection fetches /collection/{id}: the collection's name, art and
// every film in it (parts), sorted into release order with undated parts
// last. Cached with the shorter collection TTL (see cache.go).
func (c *Client) GetCollection(ctx context.Context, collectionID int) (*metadata.CollectionDetail, error) {
	var resp tmdbCollection
	params := url.Values{}
	params.Set("language", c.language)
	if err := c.get(ctx, fmt.Sprintf("/collection/%d", collectionID), params, &resp); err != nil {
		return nil, fmt.Errorf("tmdb get collection %d: %w", collectionID, err)
	}
	return resp.toDetail(), nil
}

// ── wire types ────────────────────────────────────────────────────────────────

// tmdbCollectionRef is a movie-details belongs_to_collection block.
type tmdbCollectionRef struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
}

func (r *tmdbCollectionRef) toRef() *metadata.CollectionRef {
	if r == nil || r.ID <= 0 {
		return nil
	}
	return &metadata.CollectionRef{
		TMDBID:      r.ID,
		Name:        r.Name,
		PosterURL:   imageURL(r.PosterPath),
		BackdropURL: imageURL(r.BackdropPath),
	}
}

type tmdbCollection struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Overview     string `json:"overview"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
	Parts        []struct {
		ID          int    `json:"id"`
		Title       string `json:"title"`
		MediaType   string `json:"media_type"`
		Overview    string `json:"overview"`
		ReleaseDate string `json:"release_date"`
		PosterPath  string `json:"poster_path"`
	} `json:"parts"`
}

func (t tmdbCollection) toDetail() *metadata.CollectionDetail {
	out := &metadata.CollectionDetail{
		TMDBID:      t.ID,
		Name:        t.Name,
		Overview:    t.Overview,
		PosterURL:   imageURL(t.PosterPath),
		BackdropURL: imageURL(t.BackdropPath),
		Parts:       make([]metadata.CollectionPart, 0, len(t.Parts)),
	}
	seen := make(map[int]bool, len(t.Parts))
	for _, p := range t.Parts {
		// Collections are movie groupings; skip anything else TMDB might
		// embed and any duplicate/invalid id.
		if p.ID <= 0 || seen[p.ID] || (p.MediaType != "" && p.MediaType != "movie") {
			continue
		}
		seen[p.ID] = true
		part := metadata.CollectionPart{
			TMDBID:    p.ID,
			Title:     p.Title,
			PosterURL: imageURL(p.PosterPath),
			Overview:  p.Overview,
		}
		if d, err := time.Parse("2006-01-02", p.ReleaseDate); err == nil {
			part.ReleaseDate = p.ReleaseDate
			part.Year = d.Year()
		}
		out.Parts = append(out.Parts, part)
	}
	sort.SliceStable(out.Parts, func(i, j int) bool {
		a, b := out.Parts[i].ReleaseDate, out.Parts[j].ReleaseDate
		switch {
		case a == "" && b == "":
			return false
		case a == "":
			return false
		case b == "":
			return true
		default:
			return a < b
		}
	})
	return out
}
