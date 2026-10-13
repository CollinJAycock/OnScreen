package scanner

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/podcast"
)

// PodcastFeeds tells the scanner which subscribed feed a podcast-library file
// belongs to. Satisfied by *podcast.Service.
type PodcastFeeds interface {
	MatchFile(ctx context.Context, path string) (*podcast.FileMatch, error)
	LinkItems(ctx context.Context, m *podcast.FileMatch, showID, episodeItemID uuid.UUID) error
}

// WithPodcastFeeds lets the scanner name podcast items from their RSS feed.
// Returns the Scanner for chaining.
func (s *Scanner) WithPodcastFeeds(p PodcastFeeds) *Scanner {
	s.podcasts = p
	return s
}

// processPodcast creates the show → episode hierarchy for a podcast
// audio file in a podcast library.
//
// Folder convention:
//
//	<root>/<Podcast Show>/<episode>.mp3
//	<root>/<episode>.mp3                 → "Unknown Show" parent
//
// A file downloaded from a subscribed feed (internal/podcast) takes its
// show's and its own title, show notes and date from the feed; a file
// dropped into a feed's folder joins that feed's show. Anything else is
// named from the folder and the file name.
func (s *Scanner) processPodcast(ctx context.Context, libraryID uuid.UUID, path string, roots []string) (*media.Item, error) {
	showTitle, episodeTitle := parsePodcastPath(path, roots)

	var match *podcast.FileMatch
	if s.podcasts != nil {
		m, err := s.podcasts.MatchFile(ctx, path)
		if err != nil {
			s.logger.WarnContext(ctx, "podcast feed lookup failed", "path", path, "err", err)
		} else {
			match = m
		}
	}
	if match != nil {
		showTitle = match.ShowTitle
	}

	show := s.linkedPodcastItem(ctx, matchShowItemID(match), libraryID, "podcast", nil)
	if show == nil {
		var err error
		show, err = s.media.FindOrCreateHierarchyItem(ctx, media.CreateItemParams{
			LibraryID: libraryID,
			Type:      "podcast",
			Title:     showTitle,
			SortTitle: sortTitle(showTitle),
		})
		if err != nil {
			return nil, err
		}
	}

	// A downloaded episode is found by the item an earlier scan linked to
	// it, then by its file name (which, unlike feed titles, is unique in the
	// folder); its feed title is applied below.
	episode := s.linkedPodcastItem(ctx, matchEpisodeItemID(match), libraryID, "podcast_episode", &show.ID)
	if episode == nil {
		var err error
		episode, err = s.media.FindOrCreateHierarchyItem(ctx, media.CreateItemParams{
			LibraryID: libraryID,
			Type:      "podcast_episode",
			Title:     episodeTitle,
			SortTitle: sortTitle(episodeTitle),
			ParentID:  &show.ID,
		})
		if err != nil {
			return nil, err
		}
	}

	if match != nil {
		s.applyPodcastFeed(ctx, match, show, episode, roots)
	}
	return episode, nil
}

// linkedPodcastItem returns the item a feed linked earlier, if it still
// exists in this library with the expected type (and parent).
func (s *Scanner) linkedPodcastItem(ctx context.Context, id *uuid.UUID, libraryID uuid.UUID, itemType string, parentID *uuid.UUID) *media.Item {
	if id == nil {
		return nil
	}
	item, err := s.media.GetItem(ctx, *id)
	if err != nil || item == nil || item.LibraryID != libraryID || item.Type != itemType {
		return nil
	}
	if parentID != nil && (item.ParentID == nil || *item.ParentID != *parentID) {
		return nil
	}
	return item
}

// applyPodcastFeed writes the feed's details onto the show and episode items,
// points the show at the feed's artwork, and links the items to the feed.
// Best effort: a failure is logged and the file is still scanned.
func (s *Scanner) applyPodcastFeed(ctx context.Context, m *podcast.FileMatch, show, episode *media.Item, roots []string) {
	if p, changed := podcastShowUpdate(show, m); changed {
		if _, err := s.media.UpdateItemMetadata(ctx, p); err != nil {
			s.logger.WarnContext(ctx, "podcast show metadata", "item_id", show.ID, "err", err)
		}
	}
	if show.PosterPath == nil {
		poster := filepath.Join(m.Folder, "poster.jpg")
		if s.posterExists(ctx, poster) {
			if rel := relToRoots(poster, roots); rel != "" {
				if err := s.media.UpdateItemPosterPath(ctx, show.ID, rel); err != nil {
					s.logger.WarnContext(ctx, "podcast show poster", "item_id", show.ID, "err", err)
				}
			}
		}
	}
	if m.Episode != nil {
		if p, changed := podcastEpisodeUpdate(episode, m.Episode); changed {
			if _, err := s.media.UpdateItemMetadata(ctx, p); err != nil {
				s.logger.WarnContext(ctx, "podcast episode metadata", "item_id", episode.ID, "err", err)
			}
		}
	}
	if err := s.podcasts.LinkItems(ctx, m, show.ID, episode.ID); err != nil {
		s.logger.WarnContext(ctx, "podcast feed link", "feed_id", m.FeedID, "err", err)
	}
}

// podcastShowUpdate brings the show's title and description in line with the
// feed. A description an admin cleared stays set from the feed; one they
// wrote is kept.
func podcastShowUpdate(show *media.Item, m *podcast.FileMatch) (media.UpdateItemMetadataParams, bool) {
	p := metadataParamsOf(show)
	changed := false
	if m.ShowTitle != "" && show.Title != m.ShowTitle {
		p.Title, p.SortTitle = m.ShowTitle, sortTitle(m.ShowTitle)
		changed = true
	}
	if (show.Summary == nil || *show.Summary == "") && m.ShowSummary != "" {
		summary := m.ShowSummary
		p.Summary = &summary
		changed = true
	}
	return p, changed
}

// podcastEpisodeUpdate gives the episode its feed title, show notes and
// release date. The feed's duration only fills a gap: the probe that follows
// (processFile) replaces it with the file's real length.
func podcastEpisodeUpdate(item *media.Item, ep *podcast.MatchedEpisode) (media.UpdateItemMetadataParams, bool) {
	p := metadataParamsOf(item)
	changed := false
	if ep.Title != "" && item.Title != ep.Title {
		p.Title, p.SortTitle = ep.Title, sortTitle(ep.Title)
		changed = true
	}
	if ep.Summary != "" && (item.Summary == nil || *item.Summary != ep.Summary) {
		summary := ep.Summary
		p.Summary = &summary
		changed = true
	}
	if ep.PublishedAt != nil && (item.OriginallyAvailableAt == nil || !sameDay(*item.OriginallyAvailableAt, *ep.PublishedAt)) {
		t := ep.PublishedAt.UTC()
		year := t.Year()
		p.OriginallyAvailableAt = &t
		p.Year = &year
		changed = true
	}
	if item.DurationMS == nil && ep.DurationMS != nil {
		p.DurationMS = ep.DurationMS
		changed = true
	}
	return p, changed
}

// metadataParamsOf copies an item's editable metadata, so an update that
// changes a few fields leaves the rest as they are (UpdateItemMetadata
// writes every field it's given).
func metadataParamsOf(it *media.Item) media.UpdateItemMetadataParams {
	return media.UpdateItemMetadataParams{
		ID:                    it.ID,
		Title:                 it.Title,
		SortTitle:             it.SortTitle,
		OriginalTitle:         it.OriginalTitle,
		Year:                  it.Year,
		Summary:               it.Summary,
		Tagline:               it.Tagline,
		Rating:                it.Rating,
		AudienceRating:        it.AudienceRating,
		ContentRating:         it.ContentRating,
		DurationMS:            it.DurationMS,
		Genres:                it.Genres,
		Tags:                  it.Tags,
		OriginallyAvailableAt: it.OriginallyAvailableAt,
	}
}

func sameDay(a, b time.Time) bool {
	return a.UTC().Format("2006-01-02") == b.UTC().Format("2006-01-02")
}

// relToRoots returns path relative to the first library root holding it,
// with forward slashes (the form poster_path is stored in); "" if none does.
func relToRoots(path string, roots []string) string {
	for _, root := range roots {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return ""
}

// matchShowItemID and matchEpisodeItemID read a possibly-nil match.
func matchShowItemID(m *podcast.FileMatch) *uuid.UUID {
	if m == nil {
		return nil
	}
	return m.ShowItemID
}

func matchEpisodeItemID(m *podcast.FileMatch) *uuid.UUID {
	if m == nil || m.Episode == nil {
		return nil
	}
	return m.Episode.ItemID
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
