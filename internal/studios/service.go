// Package studios keeps the studios (production companies) and TV networks
// that made each movie and show (migration 00049), and lists them for the
// studio / network browse pages.
//
// The enricher writes an item's list on every enrichment from TMDB (movie
// production_companies, show networks), with an NFO's <studio> tags winning
// when the item has an NFO. Studios have no visibility of their own: every
// listing here is scoped by the caller's libraries, rating ceiling and label
// restriction, through the items behind it.
package studios

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/dbconv"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/itemlabel"
	"github.com/onscreen/onscreen/internal/metadata"
)

// Studio kinds (studios_kind_check).
const (
	KindStudio  = "studio"  // a production company (movies)
	KindNetwork = "network" // a TV network (shows)
)

// maxPerItem caps how many studios one item keeps; TMDB lists a dozen
// co-producers for some films, and the detail page shows the first few.
const maxPerItem = 12

// ErrNotFound is returned for a studio id that doesn't exist.
var ErrNotFound = errors.New("studio not found")

// Studio is one studio or network. ItemCount is set by List only.
type Studio struct {
	ID        uuid.UUID
	Name      string
	Kind      string
	TMDBID    *int
	ItemCount int64
}

// Scope narrows a listing to what the caller may see, the same three rules
// every item listing applies. LibraryIDs nil = every library (admin);
// empty = none. MaxRatingRank nil = no ceiling.
type Scope struct {
	LibraryIDs    []uuid.UUID
	MaxRatingRank *int
	Labels        itemlabel.Filter
}

// Querier is the subset of gen.Queries the service needs.
type Querier interface {
	UpsertStudio(ctx context.Context, arg gen.UpsertStudioParams) (uuid.UUID, error)
	ReplaceItemStudios(ctx context.Context, arg gen.ReplaceItemStudiosParams) error
	ListItemStudios(ctx context.Context, mediaItemID uuid.UUID) ([]gen.ListItemStudiosRow, error)
	GetStudio(ctx context.Context, id uuid.UUID) (gen.GetStudioRow, error)
	ListStudiosWithCounts(ctx context.Context, arg gen.ListStudiosWithCountsParams) ([]gen.ListStudiosWithCountsRow, error)
	ListStudioItems(ctx context.Context, arg gen.ListStudioItemsParams) ([]gen.ListStudioItemsRow, error)
	CountStudioItems(ctx context.Context, arg gen.CountStudioItemsParams) (int64, error)
}

// Service reads and writes studios.
type Service struct {
	q Querier
}

// New returns a Service over q (normally gen.New on the read-write pool).
func New(q Querier) *Service {
	return &Service{q: q}
}

// ValidKind reports whether k is a studio kind.
func ValidKind(k string) bool { return k == KindStudio || k == KindNetwork }

// SetItemStudios replaces an item's studios with list (in order), creating
// studio rows as needed. Names are trimmed and de-duplicated ignoring case;
// an empty list clears the item's studios.
func (s *Service) SetItemStudios(ctx context.Context, itemID uuid.UUID, kind string, list []metadata.Studio) error {
	if !ValidKind(kind) {
		return fmt.Errorf("studios: invalid kind %q", kind)
	}
	ids := make([]uuid.UUID, 0, len(list))
	seen := map[uuid.UUID]bool{}
	for _, st := range Clean(list) {
		var tmdbID *int32
		if st.TMDBID > 0 {
			v := int32(st.TMDBID)
			tmdbID = &v
		}
		id, err := s.q.UpsertStudio(ctx, gen.UpsertStudioParams{Name: st.Name, Kind: kind, TmdbID: tmdbID})
		if err != nil {
			return fmt.Errorf("studios: upsert %q: %w", st.Name, err)
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if err := s.q.ReplaceItemStudios(ctx, gen.ReplaceItemStudiosParams{MediaItemID: itemID, StudioIds: ids}); err != nil {
		return fmt.Errorf("studios: set item %s: %w", itemID, err)
	}
	return nil
}

// Clean trims names, drops empty ones and case-insensitive repeats, and caps
// the list at maxPerItem, keeping the order.
func Clean(list []metadata.Studio) []metadata.Studio {
	out := make([]metadata.Studio, 0, len(list))
	seen := map[string]bool{}
	for _, st := range list {
		st.Name = strings.Join(strings.Fields(st.Name), " ")
		key := strings.ToLower(st.Name)
		if st.Name == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, st)
		if len(out) == maxPerItem {
			break
		}
	}
	return out
}

// ItemStudios returns an item's studios in order.
func (s *Service) ItemStudios(ctx context.Context, itemID uuid.UUID) ([]Studio, error) {
	rows, err := s.q.ListItemStudios(ctx, itemID)
	if err != nil {
		return nil, fmt.Errorf("studios: list for item %s: %w", itemID, err)
	}
	out := make([]Studio, len(rows))
	for i, r := range rows {
		out[i] = Studio{ID: r.ID, Name: r.Name, Kind: r.Kind, TMDBID: intPtr(r.TmdbID)}
	}
	return out, nil
}

// ItemStudioNames returns an item's studio names in order (the NFO writer's
// view).
func (s *Service) ItemStudioNames(ctx context.Context, itemID uuid.UUID) ([]string, error) {
	list, err := s.ItemStudios(ctx, itemID)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(list))
	for i, st := range list {
		names[i] = st.Name
	}
	return names, nil
}

// Get returns one studio, or ErrNotFound.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Studio, error) {
	r, err := s.q.GetStudio(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("studios: get %s: %w", id, err)
	}
	return &Studio{ID: r.ID, Name: r.Name, Kind: r.Kind, TMDBID: intPtr(r.TmdbID)}, nil
}

// List returns the studios with at least one item the caller can see, with
// how many, optionally narrowed to a library (nil = all) and a kind ("" =
// both), ordered by name.
func (s *Service) List(ctx context.Context, libraryID *uuid.UUID, kind string, scope Scope) ([]Studio, error) {
	var k *string
	if kind != "" {
		k = &kind
	}
	rows, err := s.q.ListStudiosWithCounts(ctx, gen.ListStudiosWithCountsParams{
		LibraryID:     pgUUID(libraryID),
		Kind:          k,
		LibraryIds:    scope.LibraryIDs,
		MaxRatingRank: rank32(scope.MaxRatingRank),
		BlockedLabels: scope.Labels.BlockedArg(),
		AllowedLabels: scope.Labels.AllowedArg(),
	})
	if err != nil {
		return nil, fmt.Errorf("studios: list: %w", err)
	}
	out := make([]Studio, len(rows))
	for i, r := range rows {
		out[i] = Studio{ID: r.ID, Name: r.Name, Kind: r.Kind, TMDBID: intPtr(r.TmdbID), ItemCount: r.ItemCount}
	}
	return out, nil
}

// ItemsParams selects one page of a studio's items.
type ItemsParams struct {
	StudioID  uuid.UUID
	LibraryID *uuid.UUID // nil = every library in scope
	Scope     Scope
	Sort      string // title (default) | year | rating | created_at
	SortAsc   bool
	Limit     int32
	Offset    int32
}

// ValidSort reports whether s is a sort ListItems accepts ("" = title).
func ValidSort(s string) bool {
	switch s {
	case "", "title", "year", "rating", "created_at":
		return true
	}
	return false
}

// Items returns one page of a studio's items the caller can see, and the
// total across pages.
func (s *Service) Items(ctx context.Context, p ItemsParams) ([]media.Item, int64, error) {
	sort := p.Sort
	if sort == "" {
		sort = "title"
	}
	rows, err := s.q.ListStudioItems(ctx, gen.ListStudioItemsParams{
		StudioID:      p.StudioID,
		LibraryID:     pgUUID(p.LibraryID),
		LibraryIds:    p.Scope.LibraryIDs,
		MaxRatingRank: rank32(p.Scope.MaxRatingRank),
		BlockedLabels: p.Scope.Labels.BlockedArg(),
		AllowedLabels: p.Scope.Labels.AllowedArg(),
		Sort:          sort,
		SortAsc:       p.SortAsc,
		LimitN:        p.Limit,
		OffsetN:       p.Offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("studios: items of %s: %w", p.StudioID, err)
	}
	total, err := s.q.CountStudioItems(ctx, gen.CountStudioItemsParams{
		StudioID:      p.StudioID,
		LibraryID:     pgUUID(p.LibraryID),
		LibraryIds:    p.Scope.LibraryIDs,
		MaxRatingRank: rank32(p.Scope.MaxRatingRank),
		BlockedLabels: p.Scope.Labels.BlockedArg(),
		AllowedLabels: p.Scope.Labels.AllowedArg(),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("studios: count items of %s: %w", p.StudioID, err)
	}
	items := make([]media.Item, len(rows))
	for i, r := range rows {
		items[i] = dbconv.ItemFromGenFields(r.ID, r.LibraryID, r.Type, r.Title, r.SortTitle,
			r.OriginalTitle, r.Year, r.Summary, nil,
			r.Rating, pgtype.Numeric{}, r.ContentRating, r.DurationMs,
			r.Genres, nil, nil, nil, nil,
			pgtype.UUID{}, nil, r.PosterPath, nil, nil,
			r.OriginallyAvailableAt, r.CreatedAt, r.UpdatedAt, pgtype.Timestamptz{})
	}
	return items, total, nil
}

func intPtr(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func rank32(v *int) *int32 {
	if v == nil {
		return nil
	}
	n := int32(*v)
	return &n
}

func pgUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}
