package scanner

import (
	"context"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// processPodcast creates the show → episode hierarchy for a podcast
// audio file in a podcast library. v2.0 scope is local-files-only —
// folder = show, file = episode. RSS-feed subscriptions, auto-
// download, and retention windows live in v2.1.
//
// Folder convention:
//
//	<root>/<Podcast Show>/<episode>.mp3
//	<root>/<episode>.mp3                 → "Unknown Show" parent
//
// Episode title comes from the filename stem; richer metadata
// (pubdate, description, episode number) belongs to the future
// RSS-driven flow where the feed itself is authoritative.
func (s *Scanner) processPodcast(ctx context.Context, libraryID uuid.UUID, path string, roots []string) (*media.Item, error) {
	showTitle, episodeTitle := parsePodcastPath(path, roots)

	show, err := s.media.FindOrCreateHierarchyItem(ctx, media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "podcast",
		Title:     showTitle,
		SortTitle: sortTitle(showTitle),
	})
	if err != nil {
		return nil, err
	}

	episode, err := s.media.FindOrCreateHierarchyItem(ctx, media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "podcast_episode",
		Title:     episodeTitle,
		SortTitle: sortTitle(episodeTitle),
		ParentID:  &show.ID,
	})
	if err != nil {
		return nil, err
	}
	return episode, nil
}

// parsePodcastPath splits an episode file path into (show, episode)
// using the folder layout. A loose file at the library root falls
// back to "Unknown Show" so it isn't lost.
func parsePodcastPath(path string, roots []string) (show, episode string) {
	episode = trimExt(filepath.Base(path))
	dir := filepath.Dir(path)

	if isLibraryRoot(dir, roots) {
		show = "Unknown Show"
		return
	}
	show = filepath.Base(dir)
	return
}

func trimExt(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		return name
	}
	return name[:len(name)-len(ext)]
}
