package podcast

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// FileMatch is what a feed says about a file the scanner found in a feed's
// folder.
type FileMatch struct {
	FeedID      uuid.UUID
	Folder      string
	ShowTitle   string
	ShowSummary string
	// ShowItemID is the podcast item an earlier scan made for the feed.
	ShowItemID *uuid.UUID
	// Episode is the downloaded episode the file is; nil for a file in the
	// feed's folder that the feed didn't download (put there by hand).
	Episode *MatchedEpisode
}

// MatchedEpisode is a downloaded episode's details from its feed.
type MatchedEpisode struct {
	ID          uuid.UUID
	Title       string
	Summary     string
	PublishedAt *time.Time
	DurationMS  *int64
	Season      *int32
	Number      *int32
	// ItemID is the podcast_episode item an earlier scan made for it.
	ItemID *uuid.UUID
}

// MatchFile looks up the feed a podcast-library file belongs to: as one of
// its downloads, or as a file in its folder. nil when the file has nothing to
// do with a subscription.
func (s *Service) MatchFile(ctx context.Context, path string) (*FileMatch, error) {
	clean := filepath.Clean(path)
	row, err := s.q.GetPodcastEpisodeByFilePath(ctx, &clean)
	switch {
	case err == nil:
		m := &FileMatch{
			FeedID:      row.FeedID,
			Folder:      row.FeedFolder,
			ShowTitle:   row.FeedTitle,
			ShowSummary: deref(row.FeedDescription),
			ShowItemID:  uuidPtr(row.ShowItemID),
			Episode: &MatchedEpisode{
				ID:         row.ID,
				Title:      row.Title,
				Summary:    deref(row.Description),
				DurationMS: row.DurationMs,
				Season:     row.Season,
				Number:     row.Episode,
				ItemID:     uuidPtr(row.MediaItemID),
			},
		}
		if row.PublishedAt.Valid {
			t := row.PublishedAt.Time
			m.Episode.PublishedAt = &t
		}
		return m, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("podcast episode by path: %w", err)
	}

	dir := filepath.Dir(clean)
	feed, err := s.q.GetPodcastFeedByFolder(ctx, dir)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("podcast feed by folder: %w", err)
	}
	return &FileMatch{
		FeedID:      feed.ID,
		Folder:      dir,
		ShowTitle:   feed.Title,
		ShowSummary: deref(feed.Description),
		ShowItemID:  uuidPtr(feed.ShowItemID),
	}, nil
}

// LinkItems records the items the scanner made for a matched file, so the
// next scan reuses them and the admin pages can link to them.
func (s *Service) LinkItems(ctx context.Context, m *FileMatch, showID, episodeItemID uuid.UUID) error {
	if m.ShowItemID == nil || *m.ShowItemID != showID {
		if err := s.q.SetPodcastFeedShowItem(ctx, gen.SetPodcastFeedShowItemParams{
			ID: m.FeedID, ShowItemID: pgtype.UUID{Bytes: showID, Valid: true},
		}); err != nil {
			return fmt.Errorf("link podcast show: %w", err)
		}
	}
	if m.Episode != nil && (m.Episode.ItemID == nil || *m.Episode.ItemID != episodeItemID) {
		if err := s.q.LinkPodcastEpisodeItem(ctx, gen.LinkPodcastEpisodeItemParams{
			ID: m.Episode.ID, MediaItemID: pgtype.UUID{Bytes: episodeItemID, Valid: true},
		}); err != nil {
			return fmt.Errorf("link podcast episode: %w", err)
		}
	}
	return nil
}

func uuidPtr(u pgtype.UUID) *uuid.UUID {
	if !u.Valid {
		return nil
	}
	id := uuid.UUID(u.Bytes)
	return &id
}
