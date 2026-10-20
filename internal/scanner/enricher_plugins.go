package scanner

import (
	"context"
	"path/filepath"
	"slices"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/plugin"
)

// MetadataPlugins runs the metadata-role plugins (outbound MCP servers).
// Satisfied by *plugin.MetadataDispatcher. Enrich never returns an error: a
// slow, failing or broken plugin just yields no result.
type MetadataPlugins interface {
	Active(ctx context.Context) bool
	Enrich(ctx context.Context, req plugin.MetadataRequest) []plugin.MetadataResult
}

// SetMetadataPlugins wires the metadata plugins. nil (the default) skips
// the plugin pass.
func (e *Enricher) SetMetadataPlugins(p MetadataPlugins) {
	e.metaPlugins = p
}

// pluginItemTypes are the item types the plugin pass runs for: the ones the
// enrichment pipeline itself enriches. Photos (and other personal media) are
// left out — a photo library would otherwise make one plugin call per photo.
var pluginItemTypes = map[string]bool{
	"movie": true, "show": true, "season": true, "episode": true,
	"artist": true, "album": true, "track": true,
	"book": true, "audiobook": true,
}

// pluginArtDir is where a plugin-supplied poster for item is written: the
// show folder for a show, the file's folder otherwise — the same choice as
// SetItemPoster.
func (e *Enricher) pluginArtDir(item *media.Item, file *media.File) string {
	if file == nil || file.FilePath == "" {
		return ""
	}
	switch item.Type {
	case "show":
		return showDirFromFile(file.FilePath)
	case "artist":
		return e.artistDirFromTrack(file.FilePath)
	default:
		return filepath.Dir(file.FilePath)
	}
}

// runMetadataPlugins is the plugin pass: after the built-in agents (TMDB,
// TVDB, AniList, MusicBrainz / TheAudioDB, NFO) have written the item, every
// enabled metadata plugin is asked about it and the answers are merged in by
// mergePluginMetadata. It re-reads the item first so it sees what the
// built-in agents wrote (and skips an item a merge just folded away).
//
// Nothing here can fail enrichment or the scan: errors are logged, and a
// panic is recovered.
func (e *Enricher) runMetadataPlugins(ctx context.Context, itemID uuid.UUID, artDir, fileName string) {
	if e.metaPlugins == nil || !e.metaPlugins.Active(ctx) {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			e.logger.ErrorContext(ctx, "metadata plugin pass panicked", "item_id", itemID, "panic", r)
		}
	}()

	item, err := e.updater.GetItem(ctx, itemID)
	if err != nil || item == nil {
		return // deleted or merged into another item by the built-in pass
	}
	if !pluginItemTypes[item.Type] {
		return
	}
	results := e.metaPlugins.Enrich(ctx, e.pluginRequest(ctx, item, fileName))
	if len(results) == 0 {
		return
	}

	p, changed, posterURL := mergePluginMetadata(item, results)
	if posterURL != "" && e.artwork != nil && artDir != "" && artDir != "." {
		// Fill-only (the item has no poster), so the ID-qualified name
		// never replaces a poster another source wrote. The artwork
		// manager's client is the safehttp one: no private / loopback
		// targets, capped redirects.
		absPath, err := e.artwork.ReplacePoster(ctx, item.ID, posterURL, artDir)
		if err != nil {
			e.logger.WarnContext(ctx, "plugin poster download failed",
				"item_id", item.ID, "err", err)
		} else if e.setRelPath(&p.PosterPath, absPath); p.PosterPath != nil {
			changed = append(changed, "poster")
		}
	}
	if len(changed) == 0 {
		return
	}

	if _, err := e.updater.UpdateItemMetadata(ctx, p); err != nil {
		// A new title or year can collide with another item in the library
		// (the title+year unique key). Keep the rest of the answer.
		if p.Title != item.Title || !equalIntPtr(p.Year, item.Year) {
			p.Title, p.SortTitle, p.Year = item.Title, item.SortTitle, item.Year
			changed = slices.DeleteFunc(changed, func(f string) bool { return f == media.FieldTitle || f == media.FieldYear })
			if len(changed) > 0 {
				_, err = e.updater.UpdateItemMetadata(ctx, p)
			} else {
				err = nil
			}
		}
		if err != nil {
			e.logger.WarnContext(ctx, "metadata plugin update failed", "item_id", item.ID, "err", err)
			return
		}
	}
	plugins := make([]string, 0, len(results))
	for _, r := range results {
		plugins = append(plugins, r.PluginName)
	}
	e.logger.InfoContext(ctx, "metadata plugins applied",
		"item_id", item.ID, "fields", changed, "plugins", plugins)
}

// pluginRequest builds the enrich_item arguments for item. Parent and
// grandparent titles give an episode its show and a track its artist.
func (e *Enricher) pluginRequest(ctx context.Context, item *media.Item, fileName string) plugin.MetadataRequest {
	req := plugin.MetadataRequest{
		ItemID:       item.ID.String(),
		LibraryID:    item.LibraryID.String(),
		ItemType:     item.Type,
		Title:        item.Title,
		Index:        item.Index,
		LockedFields: item.LockedFields,
	}
	if fileName != "" {
		req.FileName = filepath.Base(fileName)
	}
	if item.OriginalTitle != nil {
		req.OriginalTitle = *item.OriginalTitle
	}
	if item.Year != nil {
		req.Year = *item.Year
	}
	if item.TMDBID != nil {
		req.ProviderIDs.TMDB = *item.TMDBID
	}
	if item.TVDBID != nil {
		req.ProviderIDs.TVDB = *item.TVDBID
	}
	if item.IMDBID != nil {
		req.ProviderIDs.IMDB = *item.IMDBID
	}
	if item.AniListID != nil {
		req.ProviderIDs.AniList = *item.AniListID
	}
	if item.MalID != nil {
		req.ProviderIDs.MAL = *item.MalID
	}
	if item.MusicBrainzID != nil {
		req.ProviderIDs.MusicBrainz = item.MusicBrainzID.String()
	}
	if item.ParentID != nil {
		if parent, err := e.updater.GetItem(ctx, *item.ParentID); err == nil && parent != nil {
			req.ParentTitle = parent.Title
			req.ParentIndex = parent.Index
			if parent.ParentID != nil && (item.Type == "episode" || item.Type == "track") {
				if gp, err := e.updater.GetItem(ctx, *parent.ParentID); err == nil && gp != nil {
					req.GrandparentTitle = gp.Title
				}
			}
		}
	}
	return req
}

// mergePluginMetadata folds plugin answers into the item's current metadata
// and returns the full update (every field carried over from item, since
// UpdateItemMetadata writes them all), the fields that changed, and the
// poster URL to fetch, if any.
//
// Precedence:
//   - The built-in agents have already written the item; plugins run after
//     them, in plugin-name order, each seeing the previous plugins' values.
//   - A locked field (media_items.locked_fields) is never touched; a locked
//     title also keeps the sort title. UpdateMediaItemMetadata enforces the
//     same in SQL.
//   - Mode "fill" (the default) sets a field only where it is empty, so the
//     first plugin to fill it wins. Mode "override" replaces the value, so
//     the last overriding plugin wins. The title is never empty, so only an
//     override changes it.
//   - Provider IDs and the poster are fill-only whatever the mode: a plugin
//     can't re-point a matched item (that's Fix Match) or replace a poster
//     an admin picked (posters aren't lockable).
func mergePluginMetadata(item *media.Item, results []plugin.MetadataResult) (media.UpdateItemMetadataParams, []string, string) {
	p := media.UpdateItemMetadataParams{
		ID:                    item.ID,
		Title:                 item.Title,
		SortTitle:             item.SortTitle,
		OriginalTitle:         item.OriginalTitle,
		Year:                  item.Year,
		Summary:               item.Summary,
		Tagline:               item.Tagline,
		Rating:                item.Rating,
		AudienceRating:        item.AudienceRating,
		ContentRating:         item.ContentRating,
		DurationMS:            item.DurationMS,
		Genres:                item.Genres,
		Tags:                  item.Tags,
		OriginallyAvailableAt: item.OriginallyAvailableAt,
	}
	locked := func(f string) bool { return slices.Contains(item.LockedFields, f) }
	var changed []string
	mark := func(f string) {
		if !slices.Contains(changed, f) {
			changed = append(changed, f)
		}
	}
	var posterURL string

	for _, r := range results {
		override := r.Mode == plugin.MetadataModeOverride

		if r.Title != nil && override && !locked(media.FieldTitle) && *r.Title != p.Title {
			p.Title = *r.Title
			if !locked(media.FieldSortTitle) {
				p.SortTitle = *r.Title
			}
			mark(media.FieldTitle)
		}
		mergeText := func(field string, dst **string, v *string) {
			if v == nil || locked(field) {
				return
			}
			if *dst != nil && **dst != "" && (!override || **dst == *v) {
				return
			}
			s := *v
			*dst = &s
			mark(field)
		}
		mergeText(media.FieldOriginalTitle, &p.OriginalTitle, r.OriginalTitle)
		mergeText(media.FieldSummary, &p.Summary, r.Summary)
		mergeText(media.FieldTagline, &p.Tagline, r.Tagline)
		mergeText(media.FieldContentRating, &p.ContentRating, r.ContentRating)

		if r.Year != nil && !locked(media.FieldYear) {
			empty := p.Year == nil || *p.Year == 0
			if empty || (override && *p.Year != *r.Year) {
				y := *r.Year
				p.Year = &y
				mark(media.FieldYear)
			}
		}
		if r.Rating != nil && !locked(media.FieldRating) {
			empty := p.Rating == nil || *p.Rating == 0
			if empty || (override && *p.Rating != *r.Rating) {
				v := *r.Rating
				p.Rating = &v
				mark(media.FieldRating)
			}
		}
		if len(r.Genres) > 0 && !locked(media.FieldGenres) {
			if len(p.Genres) == 0 || (override && !slices.Equal(p.Genres, r.Genres)) {
				p.Genres = slices.Clone(r.Genres)
				mark(media.FieldGenres)
			}
		}
		if r.OriginallyAvailableAt != nil && !locked(media.FieldOriginallyAvailableAt) {
			if p.OriginallyAvailableAt == nil || (override && !p.OriginallyAvailableAt.Equal(*r.OriginallyAvailableAt)) {
				t := *r.OriginallyAvailableAt
				p.OriginallyAvailableAt = &t
				mark(media.FieldOriginallyAvailableAt)
			}
		}

		// Fill-only, regardless of mode. nil leaves the column alone
		// (COALESCE in UpdateMediaItemMetadata).
		fillID := func(name string, have *int, dst **int, v *int) {
			if have == nil && *dst == nil && v != nil {
				n := *v
				*dst = &n
				mark(name)
			}
		}
		fillID("tmdb_id", item.TMDBID, &p.TMDBID, r.TMDBID)
		fillID("tvdb_id", item.TVDBID, &p.TVDBID, r.TVDBID)
		fillID("anilist_id", item.AniListID, &p.AniListID, r.AniListID)
		fillID("mal_id", item.MalID, &p.MALID, r.MALID)

		if posterURL == "" && item.PosterPath == nil && r.PosterURL != "" {
			posterURL = r.PosterURL
		}
	}
	return p, changed, posterURL
}

func equalIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
