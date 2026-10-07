package collections

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata/nfo"
)

// NFO import modes (libraries.nfo_collections).
const (
	NFOOff         = "off"
	NFOSets        = "sets"
	NFOSetsAndTags = "sets_and_tags"
)

// sourceNFO is collections.source for imported collections.
const sourceNFO = "nfo"

// NFODB is the slice of generated queries the NFO import needs.
type NFODB interface {
	GetLibrary(ctx context.Context, id uuid.UUID) (gen.Library, error)
	ListLibraries(ctx context.Context) ([]gen.Library, error)
	ListActiveFilesForLibrary(ctx context.Context, libraryID uuid.UUID) ([]gen.MediaFile, error)
	FindOrCreateSourceCollection(ctx context.Context, arg gen.FindOrCreateSourceCollectionParams) (uuid.UUID, error)
	IsKnownTMDBCollection(ctx context.Context, arg gen.IsKnownTMDBCollectionParams) (bool, error)
	AddCollectionItem(ctx context.Context, arg gen.AddCollectionItemParams) (gen.CollectionItem, error)
}

// NFOImporter turns movie.nfo groupings into manual collections, for the
// libraries whose nfo_collections setting asks for it: <set> always, <tag>
// too in sets_and_tags mode. Each grouping is one collection across the
// whole server (found again by name on later scans, so re-imports join it),
// listed by release date. Membership is only ever added: a movie whose NFO
// stops naming a set stays in it until an admin removes it.
//
// A <set> that is a TMDB collection the server already tracks as a film
// series (Radarr writes every movie's TMDB collection as its set) is skipped:
// the automatic franchise collection already covers it.
type NFOImporter struct {
	db     NFODB
	logger *slog.Logger
}

// NewNFOImporter returns an NFO collection importer.
func NewNFOImporter(db NFODB, logger *slog.Logger) *NFOImporter {
	return &NFOImporter{db: db, logger: logger}
}

// groupings is what a movie's NFO puts it in under a mode, as (source key,
// display name) pairs.
func groupings(m *nfo.Movie, mode string) [][2]string {
	if m == nil || (mode != NFOSets && mode != NFOSetsAndTags) {
		return nil
	}
	var out [][2]string
	seen := map[string]bool{}
	add := func(kind, name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := kind + ":" + strings.ToLower(name)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, [2]string{key, name})
	}
	add("set", m.Set)
	if mode == NFOSetsAndTags {
		for _, t := range m.Tags {
			add("tag", t)
		}
	}
	return out
}

// ImportMovie adds a movie to the collections its NFO names, when its
// library imports them. Returns how many collections it was added to.
func (n *NFOImporter) ImportMovie(ctx context.Context, libraryID, itemID uuid.UUID, m *nfo.Movie) (int, error) {
	lib, err := n.db.GetLibrary(ctx, libraryID)
	if err != nil {
		return 0, fmt.Errorf("get library: %w", err)
	}
	return n.importMovie(ctx, lib.NfoCollections, itemID, m)
}

func (n *NFOImporter) importMovie(ctx context.Context, mode string, itemID uuid.UUID, m *nfo.Movie) (int, error) {
	added := 0
	source := sourceNFO
	for _, g := range groupings(m, mode) {
		key := g[0]
		if strings.HasPrefix(key, "set:") {
			known, err := n.db.IsKnownTMDBCollection(ctx, gen.IsKnownTMDBCollectionParams{TmdbID: int32(m.SetTMDBID), Name: g[1]})
			if err != nil {
				return added, fmt.Errorf("check set %q: %w", g[1], err)
			}
			if known {
				continue
			}
		}
		colID, err := n.db.FindOrCreateSourceCollection(ctx, gen.FindOrCreateSourceCollectionParams{
			Name:      g[1],
			Source:    &source,
			SourceKey: &key,
		})
		if err != nil {
			return added, fmt.Errorf("collection %q: %w", g[1], err)
		}
		if _, err := n.db.AddCollectionItem(ctx, gen.AddCollectionItemParams{CollectionID: colID, MediaItemID: itemID}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return added, fmt.Errorf("add to %q: %w", g[1], err)
		}
		added++
	}
	return added, nil
}

// SweepLibrary reads the movie.nfo of every file in a library and imports
// its groupings: the catch-up for a library that just turned the setting on,
// and for NFO files edited since. Files whose NFO can't be read are skipped.
func (n *NFOImporter) SweepLibrary(ctx context.Context, libraryID uuid.UUID) (string, error) {
	lib, err := n.db.GetLibrary(ctx, libraryID)
	if err != nil {
		return "", fmt.Errorf("get library: %w", err)
	}
	return n.sweep(ctx, lib)
}

func (n *NFOImporter) sweep(ctx context.Context, lib gen.Library) (string, error) {
	if lib.NfoCollections != NFOSets && lib.NfoCollections != NFOSetsAndTags {
		return fmt.Sprintf("%s: NFO collections off", lib.Name), nil
	}
	files, err := n.db.ListActiveFilesForLibrary(ctx, lib.ID)
	if err != nil {
		return "", fmt.Errorf("list files: %w", err)
	}
	read, memberships := 0, 0
	seenItem := map[uuid.UUID]bool{}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if seenItem[f.MediaItemID] {
			continue // another version of a movie already read
		}
		path, err := nfo.FindMovieNFO(f.FilePath)
		if err != nil {
			continue
		}
		fh, err := os.Open(path)
		if err != nil {
			continue
		}
		m, perr := nfo.ParseMovie(fh)
		_ = fh.Close()
		if perr != nil {
			continue // not a movie.nfo (an episode's, say)
		}
		seenItem[f.MediaItemID] = true
		read++
		added, err := n.importMovie(ctx, lib.NfoCollections, f.MediaItemID, m)
		memberships += added
		if err != nil {
			n.logger.WarnContext(ctx, "nfo collections: import failed", "path", path, "err", err)
		}
	}
	return fmt.Sprintf("%s: %d NFO files read, %d collection memberships", lib.Name, read, memberships), nil
}

// SweepAll sweeps every library that imports NFO collections (the
// nfo_collections task).
func (n *NFOImporter) SweepAll(ctx context.Context) (string, error) {
	libs, err := n.db.ListLibraries(ctx)
	if err != nil {
		return "", fmt.Errorf("list libraries: %w", err)
	}
	var parts []string
	for _, lib := range libs {
		if lib.NfoCollections == NFOOff || lib.NfoCollections == "" {
			continue
		}
		s, err := n.sweep(ctx, lib)
		if err != nil {
			return "", err
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return "no library imports NFO collections", nil
	}
	return strings.Join(parts, "; "), nil
}
