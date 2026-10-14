package scanner

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/metadata/nfo"
)

// WriteItemNFO writes a movie's, show's or episode's NFO next to its media
// when the item's library has save_nfo on: movie → <file>.nfo (or the
// movie.nfo already there), show → tvshow.nfo in the show folder, episode →
// <file>.nfo. The file says what the database says, so an admin's locked
// edits are in it. Only files OnScreen wrote (nfo.Marker) are replaced; a
// read-only or unreachable folder is a logged warning. Never fails: callers
// are enrichment, Fix Match and the metadata editor, none of which should
// fail over a sidecar.
func (e *Enricher) WriteItemNFO(ctx context.Context, itemID uuid.UUID) {
	if e.extras == nil {
		return
	}
	if e.localMediaFn != nil && !e.localMediaFn() {
		return // object storage: the media folders aren't ours to write
	}
	item, err := e.updater.GetItem(ctx, itemID)
	if err != nil || item == nil {
		return
	}
	switch item.Type {
	case "movie", "show", "episode":
	default:
		return
	}
	save, _, err := e.extras.LibraryNFOOptions(ctx, item.LibraryID)
	if err != nil || !save {
		return
	}

	target, data, err := e.buildNFO(ctx, item)
	if err != nil {
		e.logger.WarnContext(ctx, "nfo write: build failed", "item_id", item.ID, "err", err)
		return
	}
	if target == "" {
		return
	}
	written, err := nfo.WriteFile(target, data)
	switch {
	case errors.Is(err, nfo.ErrNotOurs):
		e.logger.DebugContext(ctx, "nfo write: leaving an NFO OnScreen didn't write",
			"item_id", item.ID, "path", target)
	case err != nil:
		e.logger.WarnContext(ctx, "nfo write failed (media folder read-only or unreachable?)",
			"item_id", item.ID, "path", target, "err", err)
	case written:
		e.logger.InfoContext(ctx, "nfo written", "item_id", item.ID, "path", target)
	}
}

// buildNFO renders item's NFO and picks where it goes. target "" = nowhere
// sensible (no file, or the folder is a library root shared with other
// titles).
func (e *Enricher) buildNFO(ctx context.Context, item *media.Item) (target string, data []byte, err error) {
	studios := e.studioNames(ctx, item)
	switch item.Type {
	case "movie":
		file := e.firstActiveFile(ctx, item.ID)
		if file == nil {
			return "", nil, nil
		}
		// What the scanner reads first: an existing <basename>.nfo, else an
		// existing movie.nfo; a new file is <basename>.nfo, which is right
		// even when several movies share a folder.
		target = strings.TrimSuffix(file.FilePath, filepath.Ext(file.FilePath)) + ".nfo"
		if existing, ferr := nfo.FindMovieNFO(file.FilePath); ferr == nil {
			target = existing
		}
		data, err = nfo.MarshalMovie(&nfo.Movie{
			Title:         item.Title,
			OriginalTitle: derefStr(item.OriginalTitle),
			SortTitle:     item.SortTitle,
			Year:          derefInt(item.Year),
			Plot:          derefStr(item.Summary),
			Tagline:       derefStr(item.Tagline),
			RuntimeMin:    runtimeMinutes(item.DurationMS),
			MPAA:          derefStr(item.ContentRating),
			Rating:        derefFloat(item.Rating),
			Genres:        item.Genres,
			Studios:       studios,
			Premiered:     item.OriginallyAvailableAt,
			TMDBID:        derefInt(item.TMDBID),
			IMDBID:        derefStr(item.IMDBID),
			TVDBID:        derefInt(item.TVDBID),
		})
		return target, data, err

	case "show":
		file := e.findDescendantFile(ctx, item.ID)
		if file == nil {
			return "", nil, nil
		}
		dir := showDirFromFile(file.FilePath)
		if dir == "" || dir == "." || e.isScanRoot(dir) {
			return "", nil, nil
		}
		data, err = nfo.MarshalShow(&nfo.Show{
			Title:         item.Title,
			OriginalTitle: derefStr(item.OriginalTitle),
			SortTitle:     item.SortTitle,
			Year:          derefInt(item.Year),
			Plot:          derefStr(item.Summary),
			Rating:        derefFloat(item.Rating),
			ContentRating: derefStr(item.ContentRating),
			Genres:        item.Genres,
			Studios:       studios,
			Premiered:     item.OriginallyAvailableAt,
			TMDBID:        derefInt(item.TMDBID),
			IMDBID:        derefStr(item.IMDBID),
			TVDBID:        derefInt(item.TVDBID),
		})
		return filepath.Join(dir, "tvshow.nfo"), data, err

	case "episode":
		file := e.firstActiveFile(ctx, item.ID)
		if file == nil || item.ParentID == nil || item.Index == nil {
			return "", nil, nil
		}
		season, serr := e.updater.GetItem(ctx, *item.ParentID)
		if serr != nil || season == nil || season.Index == nil {
			return "", nil, nil
		}
		data, err = nfo.MarshalEpisode(&nfo.Episode{
			Title:      item.Title,
			Season:     *season.Index,
			Episode:    *item.Index,
			Plot:       derefStr(item.Summary),
			Rating:     derefFloat(item.Rating),
			Aired:      item.OriginallyAvailableAt,
			RuntimeMin: runtimeMinutes(item.DurationMS),
			TMDBID:     derefInt(item.TMDBID),
			IMDBID:     derefStr(item.IMDBID),
			TVDBID:     derefInt(item.TVDBID),
		})
		return strings.TrimSuffix(file.FilePath, filepath.Ext(file.FilePath)) + ".nfo", data, err
	}
	return "", nil, nil
}

func (e *Enricher) studioNames(ctx context.Context, item *media.Item) []string {
	if item.Type != "movie" && item.Type != "show" {
		return nil
	}
	names, err := e.extras.ItemStudioNames(ctx, item.ID)
	if err != nil {
		return nil
	}
	return names
}

func (e *Enricher) firstActiveFile(ctx context.Context, itemID uuid.UUID) *media.File {
	files, err := e.updater.GetFiles(ctx, itemID)
	if err != nil {
		return nil
	}
	for i := range files {
		if files[i].Status == "active" {
			return &files[i]
		}
	}
	return nil
}

func runtimeMinutes(ms *int64) int {
	if ms == nil || *ms <= 0 {
		return 0
	}
	return int((*ms + 30_000) / 60_000)
}

func derefFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// The readers below treat an NFO OnScreen wrote as a record of the database,
// not as operator curation: only its provider ids are used (they make the
// lookup exact). Applying the rest would pin every refresh to the values the
// last write captured, so a provider's corrections would never land. An
// admin's edits are safe either way — they are locked in the database.

// ownMovieNFO keeps only the ids of a movie NFO OnScreen wrote.
func ownMovieNFO(m *nfo.Movie) *nfo.Movie {
	return &nfo.Movie{TMDBID: m.TMDBID, IMDBID: m.IMDBID, TVDBID: m.TVDBID}
}

// ownShowNFO keeps only the ids of a tvshow.nfo OnScreen wrote.
func ownShowNFO(s *nfo.Show) *nfo.Show {
	return &nfo.Show{TMDBID: s.TMDBID, IMDBID: s.IMDBID, TVDBID: s.TVDBID}
}
