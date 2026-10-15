package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/onscreen/onscreen/internal/metadata"
)

// TMDB episode group types (https://developer.themoviedb.org/reference/tv-series-episode-groups):
// 1 original air date, 2 absolute, 3 DVD, 4 digital, 5 story arc,
// 6 production, 7 TV. Only DVD and absolute back an episode order.
const (
	episodeGroupTypeAbsolute = 2
	episodeGroupTypeDVD      = 3
)

// GetEpisodeOrdering implements metadata.EpisodeOrderProvider through TMDB
// episode groups: it lists the show's groups (/tv/{id}/episode_groups),
// takes the one of the order's type with the most episodes — the most
// complete community list when there are several — and reads it
// (/tv/episode_group/{id}). Both requests go through get(), so they share
// the rate limiter, the circuit breaker and the disk cache.
//
// Numbering follows Jellyfin's reading of a group: a group's order is the
// season number and an episode's order within it, plus one, is its episode
// number. Absolute numbers run over the groups in order, skipping
// episodes TMDB airs as specials (season 0).
//
// ErrEpisodeOrderUnavailable (wrapped) when the show has no group of that
// type, or the group is empty.
func (c *Client) GetEpisodeOrdering(ctx context.Context, tmdbID int, order string) (*metadata.EpisodeOrdering, error) {
	var groupType int
	switch order {
	case metadata.EpisodeOrderDVD:
		groupType = episodeGroupTypeDVD
	case metadata.EpisodeOrderAbsolute:
		groupType = episodeGroupTypeAbsolute
	default:
		return nil, fmt.Errorf("tmdb episode ordering %q: %w", order, metadata.ErrEpisodeOrderUnavailable)
	}

	var list struct {
		Results []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Type         int    `json:"type"`
			EpisodeCount int    `json:"episode_count"`
		} `json:"results"`
	}
	if err := c.get(ctx, fmt.Sprintf("/tv/%d/episode_groups", tmdbID), url.Values{}, &list); err != nil {
		return nil, fmt.Errorf("tmdb episode groups %d: %w", tmdbID, err)
	}
	groupID, best := "", -1
	for _, g := range list.Results {
		if g.Type == groupType && g.ID != "" && g.EpisodeCount > best {
			groupID, best = g.ID, g.EpisodeCount
		}
	}
	if groupID == "" {
		return nil, fmt.Errorf("tmdb show %d has no %s episode group: %w", tmdbID, order, metadata.ErrEpisodeOrderUnavailable)
	}

	var group tmdbEpisodeGroup
	params := url.Values{}
	params.Set("language", c.lang(ctx))
	if err := c.get(ctx, "/tv/episode_group/"+url.PathEscape(groupID), params, &group); err != nil {
		return nil, fmt.Errorf("tmdb episode group %s: %w", groupID, err)
	}
	out := group.toOrdering(tmdbID, order)
	if len(out.Episodes) == 0 {
		return nil, fmt.Errorf("tmdb episode group %s is empty: %w", groupID, metadata.ErrEpisodeOrderUnavailable)
	}
	return out, nil
}

// tmdbEpisodeGroup is a /tv/episode_group/{id} payload.
type tmdbEpisodeGroup struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   int    `json:"type"`
	Groups []struct {
		Name     string `json:"name"`
		Order    int    `json:"order"`
		Episodes []struct {
			EpisodeNumber int     `json:"episode_number"`
			SeasonNumber  int     `json:"season_number"`
			Name          string  `json:"name"`
			Overview      string  `json:"overview"`
			AirDate       string  `json:"air_date"`
			StillPath     string  `json:"still_path"`
			VoteAverage   float64 `json:"vote_average"`
			Runtime       int     `json:"runtime"`
			Order         int     `json:"order"`
		} `json:"episodes"`
	} `json:"groups"`
}

func (g tmdbEpisodeGroup) toOrdering(tmdbID int, order string) *metadata.EpisodeOrdering {
	groups := g.Groups
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Order < groups[j].Order })
	out := &metadata.EpisodeOrdering{Order: order}
	absolute := 0
	for _, grp := range groups {
		eps := grp.Episodes
		sort.SliceStable(eps, func(i, j int) bool { return eps[i].Order < eps[j].Order })
		for _, e := range eps {
			oe := metadata.OrderedEpisode{
				Season: grp.Order,
				Number: e.Order + 1,
			}
			if e.SeasonNumber != 0 {
				absolute++
				oe.Absolute = absolute
			}
			air, _ := time.Parse("2006-01-02", e.AirDate)
			oe.Episode = metadata.EpisodeResult{
				ShowTMDBID: tmdbID,
				SeasonNum:  e.SeasonNumber,
				EpisodeNum: e.EpisodeNumber,
				Title:      e.Name,
				Summary:    e.Overview,
				AirDate:    air,
				Rating:     e.VoteAverage,
				ThumbURL:   imageURL(e.StillPath),
			}
			out.Episodes = append(out.Episodes, oe)
		}
	}
	return out
}
