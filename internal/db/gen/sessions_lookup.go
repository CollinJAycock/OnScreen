// Hand-written session media lookups. These stay outside sqlc because both
// queries share the SessionMediaItem row type (one fills Bitrate, one
// doesn't) and the sessions handler depends on that shape; sqlc would
// generate two divergent row structs.
package gen

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// poster_path coalesces up the hierarchy — an episode has no poster of its
// own, so Now Playing inherits the show's (episode→season→show); a track the
// album/artist's. Same precedence as the analytics top-played query.
// parent_title carries the show/artist so the card can read "Show · Episode".
const getMediaItemsForSessions = `
SELECT mi.id, mi.title, mi.year, mi.type,
       COALESCE(gp.poster_path, p.poster_path, mi.poster_path,
                gp.thumb_path,  p.thumb_path,  mi.thumb_path) AS poster_path,
       COALESCE(gp.title, p.title) AS parent_title,
       mi.duration_ms
FROM media_items mi
LEFT JOIN media_items p  ON p.id  = mi.parent_id
LEFT JOIN media_items gp ON gp.id = p.parent_id
WHERE mi.id = ANY($1) AND mi.deleted_at IS NULL`

type SessionMediaItem struct {
	ID          uuid.UUID
	Title       string
	Year        pgtype.Int4
	Type        string
	PosterPath  pgtype.Text
	ParentTitle pgtype.Text
	DurationMS  pgtype.Int8
	Bitrate     pgtype.Int8 // file bitrate in bits/s (only set for file-path lookups)
}

func (q *Queries) GetMediaItemsForSessions(ctx context.Context, ids []uuid.UUID) ([]SessionMediaItem, error) {
	rows, err := q.db.Query(ctx, getMediaItemsForSessions, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []SessionMediaItem
	for rows.Next() {
		var i SessionMediaItem
		if err := rows.Scan(&i.ID, &i.Title, &i.Year, &i.Type, &i.PosterPath, &i.ParentTitle, &i.DurationMS); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	return items, rows.Err()
}

const getMediaItemByFilePath = `
SELECT mi.id, mi.title, mi.year, mi.type,
       COALESCE(gp.poster_path, p.poster_path, mi.poster_path,
                gp.thumb_path,  p.thumb_path,  mi.thumb_path) AS poster_path,
       COALESCE(gp.title, p.title) AS parent_title,
       mi.duration_ms, mf.bitrate
FROM media_files mf
JOIN media_items mi ON mi.id = mf.media_item_id
LEFT JOIN media_items p  ON p.id  = mi.parent_id
LEFT JOIN media_items gp ON gp.id = p.parent_id
WHERE mf.file_path = $1 AND mi.deleted_at IS NULL
LIMIT 1`

func (q *Queries) GetMediaItemByFilePath(ctx context.Context, filePath string) (*SessionMediaItem, error) {
	var i SessionMediaItem
	err := q.db.QueryRow(ctx, getMediaItemByFilePath, filePath).Scan(
		&i.ID, &i.Title, &i.Year, &i.Type, &i.PosterPath, &i.ParentTitle, &i.DurationMS, &i.Bitrate,
	)
	if err != nil {
		return nil, err
	}
	return &i, nil
}
