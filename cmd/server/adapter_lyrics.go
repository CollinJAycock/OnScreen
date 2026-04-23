package main

import (
	"context"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// lyricsStoreAdapter implements v1.LyricsStore on top of sqlc.
type lyricsStoreAdapter struct{ q *gen.Queries }

func (a *lyricsStoreAdapter) GetLyrics(ctx context.Context, id uuid.UUID) (string, string, error) {
	row, err := a.q.GetMediaItemLyrics(ctx, id)
	if err != nil {
		return "", "", err
	}
	var p, s string
	if row.LyricsPlain != nil {
		p = *row.LyricsPlain
	}
	if row.LyricsSynced != nil {
		s = *row.LyricsSynced
	}
	return p, s, nil
}

func (a *lyricsStoreAdapter) SetLyrics(ctx context.Context, id uuid.UUID, plain, synced string) error {
	var p, s *string
	if plain != "" {
		p = &plain
	}
	if synced != "" {
		s = &synced
	}
	return a.q.UpdateMediaItemLyrics(ctx, gen.UpdateMediaItemLyricsParams{
		ID:           id,
		LyricsPlain:  p,
		LyricsSynced: s,
	})
}

// lyricsItemAdapter bridges v1.LyricsItemSource to the media.Service.
// GetTrackMetadata walks the track→album→artist chain to resolve names
// for the LRCLIB query. Either name may come back empty when the
// library is loosely tagged; LRCLIB will then no-match and the handler
// caches the miss.
type lyricsItemAdapter struct {
	svc *media.Service
}

func (a *lyricsItemAdapter) GetItem(ctx context.Context, id uuid.UUID) (*media.Item, error) {
	return a.svc.GetItem(ctx, id)
}

func (a *lyricsItemAdapter) GetFiles(ctx context.Context, itemID uuid.UUID) ([]media.File, error) {
	return a.svc.GetFiles(ctx, itemID)
}

func (a *lyricsItemAdapter) GetTrackMetadata(ctx context.Context, trackID uuid.UUID) (string, string, error) {
	track, err := a.svc.GetItem(ctx, trackID)
	if err != nil {
		return "", "", err
	}
	var album, artist string
	if track.ParentID != nil {
		albumItem, err := a.svc.GetItem(ctx, *track.ParentID)
		if err == nil {
			album = albumItem.Title
			if albumItem.ParentID != nil {
				artistItem, err := a.svc.GetItem(ctx, *albumItem.ParentID)
				if err == nil {
					artist = artistItem.Title
				}
			}
		}
	}
	return artist, album, nil
}
