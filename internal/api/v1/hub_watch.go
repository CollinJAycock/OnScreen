package v1

import (
	"context"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// nextUpRow builds the hub's Next Up row. The query applies the rating
// ceiling (show and episode) and dismissals; the library ACL is applied here
// because the query doesn't know the caller's grant set. Errors are logged
// and yield an empty row, like every other hub row.
func (h *HubHandler) nextUpRow(ctx context.Context, userID uuid.UUID, maxRank *int32, libAllowed func(uuid.UUID) bool) []HubItem {
	out := []HubItem{}
	rows, err := h.watchDB.ListNextUp(ctx, gen.ListNextUpParams{
		UserID:        userID,
		MaxRatingRank: maxRank,
		ResultLimit:   hubWatchRowFetch,
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "hub: next up", "err", err)
		return out
	}
	for _, row := range rows {
		if !libAllowed(row.LibraryID) {
			continue
		}
		out = append(out, nextUpHubItem(row))
		if len(out) >= hubWatchRowLimit {
			break
		}
	}
	return out
}

// nextUpHubItem renders one Next Up row: the tile IS the episode (id, title,
// type=episode) so a click plays or opens it, with the show's identity and
// artwork alongside — poster/fanart from the show, thumb the episode still
// when it has one (falling back to the show's).
func nextUpHubItem(row gen.ListNextUpRow) HubItem {
	showTitle := row.ShowTitle
	showID := row.ShowID.String()
	thumb := row.ThumbPath
	if thumb == nil {
		thumb = row.ShowThumbPath
	}
	return HubItem{
		ID:            row.ID.String(),
		Title:         row.Title,
		ShowTitle:     &showTitle,
		ShowID:        &showID,
		Type:          "episode",
		Year:          intPtrFrom32(row.ShowYear),
		PosterPath:    row.ShowPosterPath,
		FanartPath:    row.ShowFanartPath,
		ThumbPath:     thumb,
		DurationMS:    row.DurationMs,
		UpdatedAt:     timestamptzToMilli(row.UpdatedAt),
		SeasonNumber:  intOrZero32(row.SeasonNumber),
		EpisodeNumber: intOrZero32(row.EpisodeNumber),
	}
}

// planToWatchRow builds the hub's Plan to Watch row (see ListPlanToWatchHub).
func (h *HubHandler) planToWatchRow(ctx context.Context, userID uuid.UUID, maxRank *int32, libAllowed func(uuid.UUID) bool) []HubItem {
	out := []HubItem{}
	rows, err := h.watchDB.ListPlanToWatchHub(ctx, gen.ListPlanToWatchHubParams{
		UserID:        userID,
		MaxRatingRank: maxRank,
		ResultLimit:   hubWatchRowFetch,
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "hub: plan to watch", "err", err)
		return out
	}
	for _, row := range rows {
		if !libAllowed(row.LibraryID) {
			continue
		}
		out = append(out, HubItem{
			ID:         row.ID.String(),
			Title:      row.Title,
			Type:       row.Type,
			Year:       intPtrFrom32(row.Year),
			PosterPath: row.PosterPath,
			FanartPath: row.FanartPath,
			ThumbPath:  row.ThumbPath,
			DurationMS: row.DurationMs,
			UpdatedAt:  timestamptzToMilli(row.UpdatedAt),
		})
		if len(out) >= hubWatchRowLimit {
			break
		}
	}
	return out
}

// intOrZero32 widens an optional int4, mapping NULL to 0 (the contract's
// "number unknown").
func intOrZero32(v *int32) *int {
	n := 0
	if v != nil {
		n = int(*v)
	}
	return &n
}
