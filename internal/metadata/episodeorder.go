package metadata

import (
	"context"
	"errors"
)

// Episode orders a show can be numbered in. Aired is the provider's
// broadcast numbering (what GetSeason / GetEpisode return); DVD and
// absolute are the alternative orders Plex, Emby and Jellyfin let a show
// use, for files numbered by their DVD release or by a single running
// episode number.
const (
	EpisodeOrderAired    = "aired"
	EpisodeOrderDVD      = "dvd"
	EpisodeOrderAbsolute = "absolute"
)

// ErrEpisodeOrderUnavailable is what a provider returns when it has no
// such order for the show (TMDB lists no episode group of that type, TVDB
// has no episodes in that season type). Callers fall back to aired order.
var ErrEpisodeOrderUnavailable = errors.New("episode order not available")

// OrderedEpisode is one episode of a show in an alternative order.
type OrderedEpisode struct {
	// Season / Number place the episode in the order: DVD season 1,
	// episode 3. Season 0 holds specials.
	Season int
	Number int
	// Absolute is the episode's running number across the show's regular
	// (non-special) episodes in the order, from 1; 0 for a special. What an
	// absolute-numbered file ("Show - 123") is matched on.
	Absolute int
	// Episode is the episode's metadata. Its SeasonNum / EpisodeNum are
	// the provider's own numbering of it (TMDB: the aired numbers; TVDB:
	// the season type's) — informational only, lookups go by the fields
	// above.
	Episode EpisodeResult
}

// EpisodeOrdering is a show's episodes in one alternative order.
type EpisodeOrdering struct {
	Order    string
	Episodes []OrderedEpisode
}

// Lookup returns the episode a file numbered (season, episode) points at
// in this order, or nil when the order has no such episode. In DVD order
// the pair is the DVD season and episode. In absolute order the episode
// number is the absolute number and the season is ignored — absolute
// files sit in a single (synthetic) season, or in whatever season folder
// the user filed them under — except season 0, which holds specials and
// has no absolute number (nil: the caller keeps aired order for it).
func (o *EpisodeOrdering) Lookup(season, episode int) *EpisodeResult {
	if o == nil || episode <= 0 {
		return nil
	}
	for i := range o.Episodes {
		oe := &o.Episodes[i]
		switch o.Order {
		case EpisodeOrderAbsolute:
			if season != 0 && oe.Absolute == episode {
				return &oe.Episode
			}
		default:
			if oe.Season == season && oe.Number == episode {
				return &oe.Episode
			}
		}
	}
	return nil
}

// EpisodeOrderProvider is implemented by metadata clients that can list a
// show's episodes in an alternative order: TMDB through episode groups,
// TVDB through season types. showID is the provider's own show id. order
// is EpisodeOrderDVD or EpisodeOrderAbsolute; ErrEpisodeOrderUnavailable
// when the show has no such order.
type EpisodeOrderProvider interface {
	GetEpisodeOrdering(ctx context.Context, showID int, order string) (*EpisodeOrdering, error)
}
