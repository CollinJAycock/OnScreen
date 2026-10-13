package serverimport

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// maxUnmatchedSamples bounds the unmatched items a job keeps for display.
const maxUnmatchedSamples = 100

// unknownViewedAt dates a watched mark when the source doesn't say when the
// item was watched (an item marked played by hand on Jellyfin). Old enough
// that any activity on OnScreen counts as newer.
var unknownViewedAt = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// Mark is a "watched" mark to write.
type Mark struct {
	User, Media uuid.UUID
	At          time.Time
}

// Event types an import writes into watch_events.
const (
	eventPlay   = "scrobble" // a finished play
	eventResume = "pause"    // a resume point
)

// Event is a play or resume point to write as a watch event.
type Event struct {
	User, Media uuid.UUID
	Type        string
	PositionMS  int64
	DurationMS  int64
	At          time.Time
}

// Rating is a star rating to write.
type Rating struct {
	User, Media uuid.UUID
	Score       float64
}

// Favorite is a favorite to write.
type Favorite struct{ User, Media uuid.UUID }

// PlaylistPlan is a playlist to create (or top up) for a user.
type PlaylistPlan struct {
	User      uuid.UUID
	Name      string
	SourceKey string
	Items     []uuid.UUID
}

// Counts are an import's numbers of each kind of write: planned, or done.
type Counts struct {
	Watched       int `json:"watched"`
	ResumePoints  int `json:"resume_points"`
	Plays         int `json:"plays"`
	Ratings       int `json:"ratings"`
	Favorites     int `json:"favorites"`
	Playlists     int `json:"playlists"`
	PlaylistItems int `json:"playlist_items"`
}

// Stats is an import's progress and outcome.
type Stats struct {
	SourceItems int `json:"source_items"` // items read from the source
	Referenced  int `json:"referenced"`   // items a mapped account did something with
	Matched     int `json:"matched"`
	Unmatched   int `json:"unmatched"`
	// PlaysOutsideRetention were older than the server's watch-history
	// retention; their items still get a watched mark.
	PlaysOutsideRetention int     `json:"plays_outside_retention"`
	Planned               Counts  `json:"planned"`
	Written               Counts  `json:"written"`
	WriteProgress         float64 `json:"write_progress"` // 0-1 while writing
}

// Plan is the writes an import makes.
type Plan struct {
	Marks     []Mark
	Events    []Event
	Ratings   []Rating
	Favorites []Favorite
	Playlists []PlaylistPlan
	Stats     Stats
	Unmatched []string
}

// total is the number of writes in the plan.
func (p *Plan) total() int {
	n := len(p.Marks) + len(p.Events) + len(p.Ratings) + len(p.Favorites)
	for _, pl := range p.Playlists {
		n += 1 + len(pl.Items)
	}
	return n
}

// isWatchable reports whether an item type has watch state (watched marks
// and resume points); the same set the watched endpoints accept.
func isWatchable(t string) bool {
	switch t {
	case "movie", "episode", "music_video", "home_video":
		return true
	}
	return false
}

// BuildPlan turns what each mapped account did on the source into writes
// for its OnScreen user. Plays older than historyFrom aren't recorded as
// plays (the server would never keep them), only as watched marks.
func BuildPlan(s *Snapshot, idx *Index, mapping map[string]uuid.UUID, o Options, historyFrom time.Time) *Plan {
	p := &Plan{}
	p.Stats.SourceItems = len(s.Items)
	matches := map[string][]uuid.UUID{}
	match := func(key string) []uuid.UUID {
		if ids, ok := matches[key]; ok {
			return ids
		}
		it := s.Items[key]
		var ids []uuid.UUID
		if it != nil {
			ids = idx.Match(it)
		}
		matches[key] = ids
		p.Stats.Referenced++
		if len(ids) > 0 {
			p.Stats.Matched++
		} else {
			p.Stats.Unmatched++
			if it != nil && len(p.Unmatched) < maxUnmatchedSamples {
				p.Unmatched = append(p.Unmatched, describe(it))
			}
		}
		return ids
	}
	duration := func(key string, id uuid.UUID) int64 {
		if d := idx.Duration(id); d > 0 {
			return d
		}
		if it := s.Items[key]; it != nil {
			return it.DurationMS
		}
		return 0
	}

	type markKey struct{ user, media uuid.UUID }
	marks := map[markKey]time.Time{}
	addMark := func(user, media uuid.UUID, at time.Time) {
		k := markKey{user, media}
		if cur, ok := marks[k]; !ok || at.After(cur) {
			marks[k] = at
		}
	}

	full := map[string]bool{}
	for _, a := range s.Accounts {
		full[a.ID] = a.FullState
	}

	// Per-item state of the accounts the source shows it for.
	for _, acct := range sortedKeys(s.States) {
		user, ok := mapping[acct]
		if !ok {
			continue
		}
		for _, st := range s.States[acct] {
			ids := match(st.Key)
			for _, id := range ids {
				watchable := isWatchable(idx.Type(id))
				if o.Watched && watchable {
					at := st.LastViewedAt
					if at.IsZero() {
						at = unknownViewedAt
					}
					resume := st.PositionMS > 0
					if st.Played {
						if resume {
							// Rewatching: the mark must predate the resume
							// point, or it would hide it.
							addMark(user, id, at.Add(-time.Second))
						} else {
							addMark(user, id, at)
						}
					}
					if resume {
						if d := duration(st.Key, id); d > 0 && st.PositionMS < d {
							// A fixed date when the source has none, so a
							// re-run finds the event it wrote before.
							when := at
							p.Events = append(p.Events, Event{User: user, Media: id, Type: eventResume,
								PositionMS: st.PositionMS, DurationMS: d, At: when})
						}
					}
				}
				if o.Ratings && st.Rating != nil {
					p.Ratings = append(p.Ratings, Rating{User: user, Media: id, Score: clampRating(*st.Rating)})
				}
				if o.Favorites && st.Favorite {
					p.Favorites = append(p.Favorites, Favorite{User: user, Media: id})
				}
			}
		}
	}

	// Plays. An account the source shows no per-item state for is known
	// to have watched what it played.
	for _, pl := range s.Plays {
		user, ok := mapping[pl.Account]
		if !ok {
			continue
		}
		ids := match(pl.Key)
		if len(ids) == 0 {
			continue
		}
		if o.Watched && !full[pl.Account] {
			for _, id := range ids {
				if isWatchable(idx.Type(id)) {
					addMark(user, id, pl.At)
				}
			}
		}
		if !o.History {
			continue
		}
		if pl.At.Before(historyFrom) {
			p.Stats.PlaysOutsideRetention++
			continue
		}
		// One play, on the best match: a play counted on every copy would
		// count it twice in play counts and analytics.
		id := ids[0]
		d := duration(pl.Key, id)
		if d <= 0 {
			d = 1 // unknown length: still a finished play
		}
		p.Events = append(p.Events, Event{User: user, Media: id, Type: eventPlay, PositionMS: d, DurationMS: d, At: pl.At})
	}

	for k, at := range marks {
		p.Marks = append(p.Marks, Mark{User: k.user, Media: k.media, At: at})
	}
	sort.Slice(p.Marks, func(i, j int) bool { return p.Marks[i].At.Before(p.Marks[j].At) })

	if o.Playlists {
		for _, acct := range sortedKeys(s.Playlists) {
			user, ok := mapping[acct]
			if !ok {
				continue
			}
			for _, pl := range s.Playlists[acct] {
				plan := PlaylistPlan{User: user, Name: pl.Name, SourceKey: fmt.Sprintf("%s:%s:%s", s.Server.ID, acct, pl.ID)}
				seen := map[uuid.UUID]bool{}
				for _, key := range pl.Keys {
					if ids := match(key); len(ids) > 0 && !seen[ids[0]] {
						seen[ids[0]] = true
						plan.Items = append(plan.Items, ids[0])
					}
				}
				if len(plan.Items) > 0 && plan.Name != "" {
					p.Playlists = append(p.Playlists, plan)
				}
			}
		}
	}

	c := &p.Stats.Planned
	c.Watched, c.Ratings, c.Favorites, c.Playlists = len(p.Marks), len(p.Ratings), len(p.Favorites), len(p.Playlists)
	for _, e := range p.Events {
		if e.Type == eventPlay {
			c.Plays++
		} else {
			c.ResumePoints++
		}
	}
	for _, pl := range p.Playlists {
		c.PlaylistItems += len(pl.Items)
	}
	return p
}

func clampRating(r float64) float64 {
	return min(max(r, 0), 10)
}

func describe(it *Item) string {
	switch {
	case it.Kind == ItemEpisode && it.HasSE:
		return fmt.Sprintf("%s (S%02dE%02d)", it.Title, it.Season, it.Episode)
	case it.Kind == ItemMovie || it.Kind == ItemShow:
		return fmt.Sprintf("%s (%s)", it.Title, it.Kind)
	}
	return it.Title
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
