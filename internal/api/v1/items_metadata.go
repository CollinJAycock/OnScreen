package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// ItemCacheInvalidator drops cached copies of item rows after an admin edits
// an item's metadata. Satisfied by *HubHandler, whose trending row is cached
// for minutes with the title and, filtered by content rating at fetch time,
// would otherwise keep showing a re-rated item to a profile whose ceiling
// it now exceeds.
type ItemCacheInvalidator interface {
	InvalidateItemCaches()
}

// WithCacheInvalidator wires the caches a metadata edit invalidates.
// Optional; nil leaves cached rows to expire on their own.
func (h *ItemHandler) WithCacheInvalidator(c ItemCacheInvalidator) *ItemHandler {
	h.caches = c
	return h
}

// Editor limits. Generous enough for any real title; they keep a runaway
// paste from landing in every list response.
const (
	maxEditTitleLen   = 500
	maxEditSummaryLen = 20000
	maxEditGenres     = 50
	maxEditGenreLen   = 100
	maxEditRatingLen  = 32
)

// nullable is an optional JSON field that tells "absent" from an explicit
// null: Set is false when the key is missing, Null is true for `null`. The
// editor sends null to clear a numeric field (year, rating), where the
// string fields' "empty string clears" convention has no equivalent.
type nullable[T any] struct {
	Set   bool
	Null  bool
	Value T
}

func (n *nullable[T]) UnmarshalJSON(b []byte) error {
	n.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		n.Null = true
		return nil
	}
	return json.Unmarshal(b, &n.Value)
}

// editParams turns the PATCH body into a media edit of existing: every field
// present in the body is written and locked against automatic metadata
// writes, and unlock_fields drops locks. Returns a client-facing message on
// invalid input.
//
// Sort title follows the title: a title edit also sets the sort title to it
// unless the body carries its own sort_title or the item has a locked one
// (an admin's earlier choice). That derived sort title isn't locked itself;
// the automatic writers leave it alone anyway while the title is locked.
// An empty sort_title resets it to the title and unlocks it.
func (b *updateMetadataRequest) editParams(existing *media.Item) (media.EditItemMetadataParams, error) {
	p := media.EditItemMetadataParams{ID: existing.ID}
	set := func(field string) {
		p.Set = append(p.Set, field)
		p.Lock = append(p.Lock, field)
	}

	if b.Title != nil {
		t := strings.TrimSpace(*b.Title)
		if t == "" {
			return p, errors.New("title cannot be empty")
		}
		if len(t) > maxEditTitleLen {
			return p, fmt.Errorf("title is longer than %d characters", maxEditTitleLen)
		}
		p.Title = t
		set(media.FieldTitle)
	}
	switch {
	case b.SortTitle != nil && strings.TrimSpace(*b.SortTitle) != "":
		st := strings.TrimSpace(*b.SortTitle)
		if len(st) > maxEditTitleLen {
			return p, fmt.Errorf("sort_title is longer than %d characters", maxEditTitleLen)
		}
		p.SortTitle = st
		set(media.FieldSortTitle)
	case b.SortTitle != nil:
		p.SortTitle = existing.Title
		if b.Title != nil {
			p.SortTitle = p.Title
		}
		p.Set = append(p.Set, media.FieldSortTitle)
		p.Unlock = append(p.Unlock, media.FieldSortTitle)
	case b.Title != nil && !slices.Contains(existing.LockedFields, media.FieldSortTitle):
		p.SortTitle = p.Title
		p.Set = append(p.Set, media.FieldSortTitle)
	}

	if b.OriginalTitle != nil {
		v, err := optionalText(*b.OriginalTitle, "original_title", maxEditTitleLen)
		if err != nil {
			return p, err
		}
		p.OriginalTitle = v
		set(media.FieldOriginalTitle)
	}
	if b.Summary != nil {
		v, err := optionalText(*b.Summary, "summary", maxEditSummaryLen)
		if err != nil {
			return p, err
		}
		p.Summary = v
		set(media.FieldSummary)
	}
	if b.Tagline != nil {
		v, err := optionalText(*b.Tagline, "tagline", maxEditTitleLen)
		if err != nil {
			return p, err
		}
		p.Tagline = v
		set(media.FieldTagline)
	}
	if b.TakenAt != nil {
		raw := strings.TrimSpace(*b.TakenAt)
		if raw != "" {
			t, err := parseTakenAt(raw)
			if err != nil {
				return p, errors.New("taken_at: " + err.Error())
			}
			p.OriginallyAvailableAt = &t
		}
		set(media.FieldOriginallyAvailableAt)
	}
	if b.Year.Set {
		if !b.Year.Null {
			if b.Year.Value < 1000 || b.Year.Value > 9999 {
				return p, errors.New("year must be a four-digit year")
			}
			y := b.Year.Value
			p.Year = &y
		}
		set(media.FieldYear)
	}
	if b.Rating.Set {
		if !b.Rating.Null {
			if b.Rating.Value < 0 || b.Rating.Value > 10 {
				return p, errors.New("rating must be between 0 and 10")
			}
			r := b.Rating.Value
			p.Rating = &r
		}
		set(media.FieldRating)
	}
	if b.ContentRating != nil {
		// Free text: an unrecognised rating ranks as most restrictive
		// (contentrating.Rank), so a typo hides the item from limited
		// profiles rather than exposing it.
		v, err := optionalText(*b.ContentRating, "content_rating", maxEditRatingLen)
		if err != nil {
			return p, err
		}
		p.ContentRating = v
		set(media.FieldContentRating)
	}
	if b.Genres != nil {
		g, err := cleanGenres(*b.Genres)
		if err != nil {
			return p, err
		}
		p.Genres = g
		set(media.FieldGenres)
	}

	for _, f := range b.UnlockFields {
		if !media.IsLockableField(f) {
			return p, fmt.Errorf("unlock_fields: %q is not a lockable field", f)
		}
		if slices.Contains(p.Lock, f) {
			return p, fmt.Errorf("unlock_fields: %q is also being edited", f)
		}
		if !slices.Contains(p.Unlock, f) {
			p.Unlock = append(p.Unlock, f)
		}
	}
	return p, nil
}

// optionalText trims s; empty clears the column (nil).
func optionalText(s, field string, max int) (*string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if len(s) > max {
		return nil, fmt.Errorf("%s is longer than %d characters", field, max)
	}
	return &s, nil
}

// cleanGenres trims each genre, drops blanks and case-insensitive repeats
// (keeping the first spelling and the order given).
func cleanGenres(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, g := range in {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if len(g) > maxEditGenreLen {
			return nil, fmt.Errorf("a genre is longer than %d characters", maxEditGenreLen)
		}
		k := strings.ToLower(g)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, g)
	}
	if len(out) > maxEditGenres {
		return nil, fmt.Errorf("at most %d genres", maxEditGenres)
	}
	return out, nil
}
