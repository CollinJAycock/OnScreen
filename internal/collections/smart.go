// Package collections maintains manual (admin-curated) collections whose
// members come from somewhere other than an admin's hands: smart collections,
// whose members follow saved rules, and collections imported from Kodi
// movie.nfo files.
//
// Both write collection_items, the same membership a hand-made collection
// has, so every read path (who may see a collection, its count and cover,
// its items in their order) treats them alike.
package collections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// Rules is a smart collection's definition: the smart playlist grammar,
// limited to what a collection holds (movies and shows).
type Rules struct {
	// Types is a non-empty subset of movie, show.
	Types []string `json:"types"`
	// Genres: a member must have the first genre (one genre, as for smart
	// playlists). Empty = any.
	Genres    []string `json:"genres,omitempty"`
	YearMin   *int     `json:"year_min,omitempty"`
	YearMax   *int     `json:"year_max,omitempty"`
	RatingMin *float64 `json:"rating_min,omitempty"`
	// LibraryIDs limits the rules to these libraries. Empty = every library.
	LibraryIDs []string `json:"library_ids,omitempty"`
	// Limit caps the members, best-sorted by title (default 200, max 500).
	Limit *int `json:"limit,omitempty"`
}

const (
	defaultSmartLimit = 200
	maxSmartLimit     = 500
)

// ErrInvalidRules wraps a rules validation failure.
var ErrInvalidRules = errors.New("invalid collection rules")

// ParseRules decodes and validates rules. Anything wrong is an
// ErrInvalidRules with the reason.
func ParseRules(raw []byte) (Rules, error) {
	var r Rules
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, fmt.Errorf("%w: %v", ErrInvalidRules, err)
	}
	return r, r.Validate()
}

// Validate checks rules a smart collection can resolve.
func (r Rules) Validate() error {
	if len(r.Types) == 0 {
		return fmt.Errorf("%w: types is required (movie, show)", ErrInvalidRules)
	}
	for _, t := range r.Types {
		if t != "movie" && t != "show" {
			return fmt.Errorf("%w: a collection holds movies and shows, not %q", ErrInvalidRules, t)
		}
	}
	if r.YearMin != nil && r.YearMax != nil && *r.YearMin > *r.YearMax {
		return fmt.Errorf("%w: year_min is after year_max", ErrInvalidRules)
	}
	if r.RatingMin != nil && (*r.RatingMin < 0 || *r.RatingMin > 10) {
		return fmt.Errorf("%w: rating_min must be between 0 and 10", ErrInvalidRules)
	}
	if r.Limit != nil && (*r.Limit < 1 || *r.Limit > maxSmartLimit) {
		return fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidRules, maxSmartLimit)
	}
	for _, id := range r.LibraryIDs {
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("%w: library_ids has an invalid id", ErrInvalidRules)
		}
	}
	return nil
}

// SmartDB is the slice of generated queries smart collections need.
type SmartDB interface {
	ListLibraries(ctx context.Context) ([]gen.Library, error)
	ListSmartCollections(ctx context.Context) ([]gen.ListSmartCollectionsRow, error)
	ListMediaItemsForSmartPlaylist(ctx context.Context, arg gen.ListMediaItemsForSmartPlaylistParams) ([]gen.ListMediaItemsForSmartPlaylistRow, error)
	DeleteCollectionItemsNotIn(ctx context.Context, arg gen.DeleteCollectionItemsNotInParams) error
	UpsertCollectionItemPositions(ctx context.Context, arg gen.UpsertCollectionItemPositionsParams) error
}

// Smart refreshes smart collections' members from their rules.
type Smart struct {
	db     SmartDB
	logger *slog.Logger
}

// NewSmart returns a smart-collection refresher.
func NewSmart(db SmartDB, logger *slog.Logger) *Smart {
	return &Smart{db: db, logger: logger}
}

// Refresh sets a smart collection's members to what its rules match now,
// with no viewer's limits applied: each caller's library grant and rating
// ceiling filter the members when they read them, as for any collection.
// Returns the member count.
func (s *Smart) Refresh(ctx context.Context, collectionID uuid.UUID, rules Rules) (int, error) {
	if err := rules.Validate(); err != nil {
		return 0, err
	}
	libIDs, err := s.libraryIDs(ctx, rules)
	if err != nil {
		return 0, err
	}
	ids := []uuid.UUID{}
	if len(libIDs) > 0 {
		limit := defaultSmartLimit
		if rules.Limit != nil {
			limit = *rules.Limit
		}
		params := gen.ListMediaItemsForSmartPlaylistParams{
			LibraryIds:  libIDs,
			Types:       rules.Types,
			ResultLimit: int32(limit),
		}
		if len(rules.Genres) > 0 {
			g := rules.Genres[0]
			params.Genre = &g
		}
		if rules.YearMin != nil {
			v := int32(*rules.YearMin)
			params.YearMin = &v
		}
		if rules.YearMax != nil {
			v := int32(*rules.YearMax)
			params.YearMax = &v
		}
		if rules.RatingMin != nil {
			params.RatingMin = pgtype.Numeric{Int: big.NewInt(int64(*rules.RatingMin * 100)), Exp: -2, Valid: true}
		}
		items, err := s.db.ListMediaItemsForSmartPlaylist(ctx, params)
		if err != nil {
			return 0, fmt.Errorf("resolve rules: %w", err)
		}
		for _, it := range items {
			ids = append(ids, it.ID)
		}
	}
	if err := s.db.DeleteCollectionItemsNotIn(ctx, gen.DeleteCollectionItemsNotInParams{CollectionID: collectionID, ItemIds: ids}); err != nil {
		return 0, fmt.Errorf("drop old members: %w", err)
	}
	if len(ids) > 0 {
		if err := s.db.UpsertCollectionItemPositions(ctx, gen.UpsertCollectionItemPositionsParams{CollectionID: collectionID, ItemIds: ids}); err != nil {
			return 0, fmt.Errorf("add members: %w", err)
		}
	}
	return len(ids), nil
}

// libraryIDs is the rules' libraries, or every library.
func (s *Smart) libraryIDs(ctx context.Context, rules Rules) ([]uuid.UUID, error) {
	if len(rules.LibraryIDs) > 0 {
		out := make([]uuid.UUID, 0, len(rules.LibraryIDs))
		for _, id := range rules.LibraryIDs {
			out = append(out, uuid.MustParse(id))
		}
		return out, nil
	}
	libs, err := s.db.ListLibraries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
	}
	out := make([]uuid.UUID, 0, len(libs))
	for _, l := range libs {
		out = append(out, l.ID)
	}
	return out, nil
}

// RefreshAll refreshes every smart collection (the smart_collections task).
// One collection failing doesn't stop the others; the summary counts them.
func (s *Smart) RefreshAll(ctx context.Context) (string, error) {
	cols, err := s.db.ListSmartCollections(ctx)
	if err != nil {
		return "", fmt.Errorf("list smart collections: %w", err)
	}
	refreshed, failed, members := 0, 0, 0
	for _, c := range cols {
		rules, err := ParseRules(c.Rules)
		if err == nil {
			var n int
			n, err = s.Refresh(ctx, c.ID, rules)
			members += n
		}
		if err != nil {
			failed++
			s.logger.WarnContext(ctx, "smart collection not refreshed", "collection_id", c.ID, "err", err)
			continue
		}
		refreshed++
	}
	return fmt.Sprintf("%d smart collections refreshed (%d members), %d failed", refreshed, members, failed), nil
}
