package v1

import (
	"context"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// EpisodePosterDB is the slice of the queries surface this helper
// needs. Kept narrow so test stubs only mock these two calls.
type EpisodePosterDB interface {
	GetUserPreferences(ctx context.Context, id uuid.UUID) (gen.GetUserPreferencesRow, error)
	GetShowPostersForEpisodes(ctx context.Context, ids []uuid.UUID) ([]gen.GetShowPostersForEpisodesRow, error)
}

// resolveEpisodeShowPosters returns a map (episode_id → show poster
// path) for episodes whose user has the episode_use_show_poster
// preference enabled. Returns an empty map when:
//
//   - the user pref is off
//   - none of the supplied IDs are episodes whose two-hop ancestor
//     chain yields a non-empty show poster
//   - the lookup fails (best-effort — caller falls through to the
//     existing per-episode thumbs)
//
// Callers iterate their response items and, for each item where
// type=="episode" and the map has a hit, replace poster_path /
// thumb_path with the show poster before serialising.
func resolveEpisodeShowPosters(ctx context.Context, db EpisodePosterDB, userID uuid.UUID, episodeIDs []uuid.UUID) map[uuid.UUID]string {
	if db == nil || len(episodeIDs) == 0 {
		return nil
	}
	prefs, err := db.GetUserPreferences(ctx, userID)
	if err != nil || !prefs.EpisodeUseShowPoster {
		return nil
	}
	rows, err := db.GetShowPostersForEpisodes(ctx, episodeIDs)
	if err != nil || len(rows) == 0 {
		return nil
	}
	out := make(map[uuid.UUID]string, len(rows))
	for _, r := range rows {
		if r.ShowPosterPath != nil && *r.ShowPosterPath != "" {
			out[r.EpisodeID] = *r.ShowPosterPath
		}
	}
	return out
}
