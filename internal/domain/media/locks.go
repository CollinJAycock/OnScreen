package media

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
)

// Lockable metadata field names, as stored in media_items.locked_fields.
// They are the column names, so the CASE guards in the automatic-write
// queries (UpdateMediaItemMetadata, UpdateMediaItemTitle,
// UpdateMediaItemTakenAt) read them directly. Cast, artwork and tags are not
// lockable here: cast has its own refresh, artwork its own picker, and tags
// are curated elsewhere.
const (
	FieldTitle                 = "title"
	FieldSortTitle             = "sort_title"
	FieldOriginalTitle         = "original_title"
	FieldYear                  = "year"
	FieldOriginallyAvailableAt = "originally_available_at"
	FieldSummary               = "summary"
	FieldTagline               = "tagline"
	FieldRating                = "rating"
	FieldContentRating         = "content_rating"
	FieldGenres                = "genres"
)

// LockableFields lists every field an admin edit can lock, in the order the
// editor shows them.
var LockableFields = []string{
	FieldTitle, FieldSortTitle, FieldOriginalTitle,
	FieldYear, FieldOriginallyAvailableAt,
	FieldSummary, FieldTagline,
	FieldRating, FieldContentRating, FieldGenres,
}

// IsLockableField reports whether name is one of LockableFields.
func IsLockableField(name string) bool {
	return slices.Contains(LockableFields, name)
}

// ErrNotLockable is returned for a set or unlock of a field name that isn't
// one of LockableFields.
var ErrNotLockable = errors.New("field is not lockable")

// EditItemMetadataParams is an admin's manual metadata edit. Set names the
// fields the edit writes: only those columns change. The value fields are
// read only for names in Set (a nil pointer there clears a nullable column).
// Lock names the fields to add to the item's lock set, normally the same as
// Set; the API leaves out a sort title it derived from a new title. Unlock
// names fields to drop from the lock set; their stored values stay until the
// next refresh.
type EditItemMetadataParams struct {
	ID     uuid.UUID
	Set    []string
	Lock   []string
	Unlock []string

	Title                 string
	SortTitle             string
	OriginalTitle         *string
	Year                  *int
	OriginallyAvailableAt *time.Time
	Summary               *string
	Tagline               *string
	Rating                *float64
	ContentRating         *string
	Genres                []string
}

// EditItemMetadata applies an admin's manual edit (see
// EditItemMetadataParams). Field names outside LockableFields are rejected
// with ErrNotLockable so a typo can't plant a lock nothing honours. Returns
// ErrNotFound when the item doesn't exist or is deleted.
func (s *Service) EditItemMetadata(ctx context.Context, p EditItemMetadataParams) (*Item, error) {
	for _, names := range [][]string{p.Set, p.Lock, p.Unlock} {
		for _, f := range names {
			if !IsLockableField(f) {
				return nil, fmt.Errorf("%w: %q", ErrNotLockable, f)
			}
		}
	}
	p.Set = nonNil(p.Set)
	p.Lock = nonNil(p.Lock)
	p.Unlock = nonNil(p.Unlock)
	p.Genres = nonNil(p.Genres)
	item, err := s.rw.EditMediaItemMetadata(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("edit item metadata %s: %w", p.ID, mapNotFound(err))
	}
	return &item, nil
}

// nonNil maps a nil slice to an empty one: the edit query's array parameters
// must never be NULL (`f <> ALL(NULL)` would drop every lock the item has).
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
