package scanner

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/metadata"
)

// LibraryEpisodeOrderer reports a library's default episode order
// (libraries.episode_order). *gen.Queries satisfies it.
type LibraryEpisodeOrderer interface {
	GetLibraryEpisodeOrder(ctx context.Context, id uuid.UUID) (string, error)
}

// SetLibraryEpisodeOrderFn sets the lazy factory for the library default
// episode order lookup. Unwired (or returning nil) every show without an
// order of its own follows aired order, as before.
func (e *Enricher) SetLibraryEpisodeOrderFn(fn func() LibraryEpisodeOrderer) {
	e.libOrderFn = fn
}

// episodeOrderFor is the order a show's episode files are numbered in: the
// show's own (media_items.episode_order), else its library's default, else
// aired. A failed library lookup reads as aired, the long-standing
// behaviour.
func (e *Enricher) episodeOrderFor(ctx context.Context, show *media.Item) string {
	if show.EpisodeOrder != nil && media.ValidEpisodeOrder(*show.EpisodeOrder) {
		return *show.EpisodeOrder
	}
	if e.libOrderFn == nil {
		return metadata.EpisodeOrderAired
	}
	lo := e.libOrderFn()
	if lo == nil {
		return metadata.EpisodeOrderAired
	}
	order, err := lo.GetLibraryEpisodeOrder(ctx, show.LibraryID)
	if err != nil || !media.ValidEpisodeOrder(order) {
		if err != nil {
			e.logger.WarnContext(ctx, "library episode order lookup failed; using aired order",
				"library_id", show.LibraryID, "err", err)
		}
		return metadata.EpisodeOrderAired
	}
	return order
}

// episodeOrderingTTL is how long a show's alternative ordering stays cached
// in-process: a season cascade looks every episode up in the same ordering,
// and the providers' own caches (TMDB's disk cache) sit behind this.
// episodeOrderingMissTTL bounds a miss — the order not existing, or a
// failed fetch — so the fallback warning is logged once per show rather
// than once per episode, and a transient failure is retried soon.
const (
	episodeOrderingTTL     = time.Hour
	episodeOrderingMissTTL = 10 * time.Minute
)

type episodeOrderingEntry struct {
	ordering  *metadata.EpisodeOrdering // nil: unavailable
	fetchedAt time.Time
}

// episodeOrdering returns the show's episodes in order (EpisodeOrderDVD or
// EpisodeOrderAbsolute) from the provider the show was matched with: TMDB
// episode groups when the show has a TMDB id, else — or when TMDB has no
// such group — TVDB season types when it has a TVDB id. Every call goes
// through the existing clients (rate limits, TMDB's disk cache); results
// are memoized per show for episodeOrderingTTL. nil when the order isn't
// available: a warning is logged once and the caller falls back to aired
// order.
func (e *Enricher) episodeOrdering(ctx context.Context, agent metadata.Agent, show *media.Item, order string) *metadata.EpisodeOrdering {
	key := fmt.Sprintf("%s:%s", show.ID, order)
	if v, ok := e.orderingCache.Load(key); ok {
		entry := v.(episodeOrderingEntry)
		ttl := episodeOrderingTTL
		if entry.ordering == nil {
			ttl = episodeOrderingMissTTL
		}
		if time.Since(entry.fetchedAt) < ttl {
			return entry.ordering
		}
	}
	v, _, _ := e.orderingSF.Do(key, func() (any, error) {
		var errs []error
		var ordering *metadata.EpisodeOrdering
		if op, ok := agent.(metadata.EpisodeOrderProvider); ok && show.TMDBID != nil {
			o, err := op.GetEpisodeOrdering(ctx, *show.TMDBID, order)
			if err == nil {
				ordering = o
			} else {
				errs = append(errs, err)
			}
		}
		if ordering == nil && show.TVDBID != nil && e.tvdbFn != nil {
			if op, ok := e.tvdbFn().(metadata.EpisodeOrderProvider); ok {
				o, err := op.GetEpisodeOrdering(ctx, *show.TVDBID, order)
				if err == nil {
					ordering = o
				} else {
					errs = append(errs, err)
				}
			}
		}
		if ordering == nil {
			e.logger.WarnContext(ctx, "episode order not available from the show's providers; using aired order",
				"show_id", show.ID, "title", show.Title, "episode_order", order,
				"err", errors.Join(errs...))
		}
		e.orderingCache.Store(key, episodeOrderingEntry{ordering: ordering, fetchedAt: time.Now()})
		return ordering, nil
	})
	o, _ := v.(*metadata.EpisodeOrdering)
	return o
}

// episodePartRE splits a title that ends in a part marker into its base
// and part number: "The Best of Both Worlds (1)", "Pilot, Part 2",
// "Pilot Pt. 2".
var episodePartRE = regexp.MustCompile(`(?i)^(.*?)(?:\s*\((\d+)\)|,?\s+(?:Part|Pt\.?)\s*(\d+))$`)

// joinEpisodeTitles names a multi-episode item from its episodes' titles.
// Two-parters whose titles differ only in a consecutive part number fold
// into one name — "The Best of Both Worlds (1)" + "(2)" → "The Best of
// Both Worlds (1–2)", "Pilot, Part 1" + "Pilot, Part 2" → "Pilot (Parts
// 1–2)". Anything else is joined with " / " the way Jellyfin and Plex
// show a multi-episode file ("Pilot / Cat's in the Bag..."). Empty titles
// are skipped and a repeated title is listed once.
func joinEpisodeTitles(titles []string) string {
	var list []string
	for _, t := range titles {
		t = strings.TrimSpace(t)
		if t == "" || (len(list) > 0 && list[len(list)-1] == t) {
			continue
		}
		list = append(list, t)
	}
	switch len(list) {
	case 0:
		return ""
	case 1:
		return list[0]
	}
	if folded, ok := foldPartTitles(list); ok {
		return folded
	}
	return strings.Join(list, " / ")
}

// foldPartTitles folds part-numbered titles with one shared base and
// consecutive numbers (see joinEpisodeTitles).
func foldPartTitles(list []string) (string, bool) {
	var base string
	var paren bool
	first, prev := 0, 0
	for i, t := range list {
		m := episodePartRE.FindStringSubmatch(t)
		if m == nil || strings.TrimSpace(m[1]) == "" {
			return "", false
		}
		isParen := m[2] != ""
		numStr := m[2]
		if !isParen {
			numStr = m[3]
		}
		n, _ := strconv.Atoi(numStr)
		b := strings.TrimSpace(m[1])
		if i == 0 {
			base, paren, first = b, isParen, n
		} else if b != base || isParen != paren || n != prev+1 {
			return "", false
		}
		prev = n
	}
	if paren {
		return fmt.Sprintf("%s (%d–%d)", base, first, prev), true
	}
	return fmt.Sprintf("%s (Parts %d–%d)", base, first, prev), true
}

// mergeEpisodeResults folds the metadata of the episodes a multi-episode
// item covers into one result: the joined title, and everything else —
// summary, air date, rating, thumbnail — from the first episode found
// (Jellyfin's choice too: the item opens on episode one). nil entries are
// episodes no provider had; nil when none were found.
func mergeEpisodeResults(results []*metadata.EpisodeResult) *metadata.EpisodeResult {
	var merged *metadata.EpisodeResult
	titles := make([]string, 0, len(results))
	for _, r := range results {
		if r == nil {
			continue
		}
		if merged == nil {
			cp := *r
			merged = &cp
		}
		titles = append(titles, r.Title)
	}
	if merged != nil && len(titles) > 1 {
		merged.Title = joinEpisodeTitles(titles)
	}
	return merged
}
