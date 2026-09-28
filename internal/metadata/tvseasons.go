package metadata

import (
	"sort"
	"time"
)

// TVSeasonSummary is one entry of a show's season list, as TMDB's tv details
// list them (no episodes).
type TVSeasonSummary struct {
	Number       int
	Name         string
	EpisodeCount int
	// AirDate is the season premiere; zero when unannounced.
	AirDate   time.Time
	PosterURL string
}

// TVSeasonList is a show's seasons plus TMDB's airing position, which is what
// tells aired episodes apart from announced ones. Backs season-level requests:
// the season picker, request validation, and the library-side "is this
// season complete" check.
type TVSeasonList struct {
	TMDBID  int
	Seasons []TVSeasonSummary
	// LastAiredSeason / LastAiredEpisode are TMDB's last_episode_to_air;
	// both 0 when nothing has aired or TMDB doesn't say.
	LastAiredSeason  int
	LastAiredEpisode int
}

// Season returns season n; ok is false when the show has no such season.
func (l *TVSeasonList) Season(n int) (TVSeasonSummary, bool) {
	if l == nil {
		return TVSeasonSummary{}, false
	}
	for _, s := range l.Seasons {
		if s.Number == n {
			return s, true
		}
	}
	return TVSeasonSummary{}, false
}

// Has reports whether the show has season n.
func (l *TVSeasonList) Has(n int) bool {
	_, ok := l.Season(n)
	return ok
}

// AiredEpisodes estimates how many episodes of season n have aired by now.
// Seasons before TMDB's last aired episode are complete; the season holding
// it has aired up to that episode; later seasons have aired nothing. When
// TMDB gives no airing position the season premiere decides: a premiere in
// the past counts the whole season, anything else counts nothing. Specials
// (season 0) are judged by their premiere alone — TMDB's last aired episode
// is a regular one.
func (l *TVSeasonList) AiredEpisodes(n int, now time.Time) int {
	s, ok := l.Season(n)
	if !ok || s.EpisodeCount <= 0 {
		return 0
	}
	if n == 0 || l.LastAiredSeason <= 0 {
		if !s.AirDate.IsZero() && !s.AirDate.After(now) {
			return s.EpisodeCount
		}
		return 0
	}
	switch {
	case n < l.LastAiredSeason:
		return s.EpisodeCount
	case n == l.LastAiredSeason:
		if l.LastAiredEpisode <= 0 {
			return 0
		}
		return min(l.LastAiredEpisode, s.EpisodeCount)
	default:
		return 0
	}
}

// AiredSeasons lists the regular seasons (number > 0) with at least one aired
// episode, ascending — what an "all seasons" request covers today.
func (l *TVSeasonList) AiredSeasons(now time.Time) []int {
	if l == nil {
		return nil
	}
	var out []int
	for _, s := range l.Seasons {
		if s.Number > 0 && l.AiredEpisodes(s.Number, now) > 0 {
			out = append(out, s.Number)
		}
	}
	sort.Ints(out)
	return out
}
