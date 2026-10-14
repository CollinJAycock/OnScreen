// Package bulkedit applies one admin metadata edit to many items at once
// (PATCH /api/v1/items/bulk): the fields that make sense across a selection —
// content rating, genres (replace, add, remove), labels — plus field locks.
//
// It follows the single-item editor's lock rules (media.EditItemMetadataParams):
// a field the edit writes is locked against automatic metadata writes. A field
// an item already has locked is left alone and reported, unless the same
// request unlocks it. Everything runs in one transaction over row-locked items,
// so a failure leaves every item as it was.
package bulkedit

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// Field names a bulk edit can set, a subset of media.LockableFields.
const (
	FieldContentRating = media.FieldContentRating
	FieldGenres        = media.FieldGenres
)

// Skip reasons.
const (
	ReasonLocked        = "locked"         // the item has the field locked; unlock it in the same request to override
	ReasonNotApplicable = "not_applicable" // the item's type doesn't carry the field
	ReasonNotFound      = "not_found"      // no live item with this id
)

// applicable lists, per bulk-editable field, the item types that carry it.
// Seasons, episodes and tracks inherit content rating and genres from their
// show / album in every client, so editing them there would change nothing
// visible; photos and people-like rows (artist pages aside) have neither.
var applicable = map[string][]string{
	FieldContentRating: {"movie", "show", "episode", "music_video", "home_video", "audiobook", "book", "podcast"},
	FieldGenres:        {"movie", "show", "album", "artist", "music_video", "audiobook", "book", "podcast"},
}

// Applies reports whether field is bulk-editable on items of itemType.
func Applies(field, itemType string) bool {
	return slices.Contains(applicable[field], itemType)
}

// Request is a validated bulk edit. Each Set* field is written only when its
// Set flag is true. At most one of SetGenres and Add/RemoveGenres is used:
// the API rejects both together.
type Request struct {
	ItemIDs []uuid.UUID

	SetContentRating bool
	ContentRating    *string // nil clears

	SetGenres bool
	Genres    []string // the full list; empty clears

	AddGenres    []string
	RemoveGenres []string

	AddLabels    []string // normalised by the caller (itemlabel)
	RemoveLabels []string
	MaxLabels    int // itemlabel.MaxPerItem

	Lock   []string // lock without changing the value
	Unlock []string // dropped first, so a locked field can be overwritten
}

// Skip is one field left unchanged on one item, and why.
type Skip struct {
	ItemID uuid.UUID
	Title  string
	Field  string // empty for ReasonNotFound
	Reason string
}

// Result reports what a bulk edit did.
type Result struct {
	Matched       int         // live items among ItemIDs
	Updated       []uuid.UUID // items whose metadata or locks were written
	LabelsAdded   int64       // items the label add changed
	LabelsRemoved int64       // items the label remove changed
	Skipped       []Skip
}

// Store is the slice of gen.Queries a bulk edit runs, inside its transaction.
type Store interface {
	LockMediaItemsForBulkEdit(ctx context.Context, ids []uuid.UUID) ([]gen.LockMediaItemsForBulkEditRow, error)
	EditMediaItemMetadata(ctx context.Context, arg gen.EditMediaItemMetadataParams) (gen.EditMediaItemMetadataRow, error)
	AddMediaItemLabels(ctx context.Context, arg gen.AddMediaItemLabelsParams) (int64, error)
	RemoveMediaItemLabels(ctx context.Context, arg gen.RemoveMediaItemLabelsParams) (int64, error)
}

// Service runs bulk edits against the database.
type Service struct {
	pool *pgxpool.Pool
}

// New creates a Service on the read-write pool.
func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Apply runs req in one transaction: either every item gets its edit or none
// does.
func (s *Service) Apply(ctx context.Context, req Request) (Result, error) {
	var res Result
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		res, err = Run(ctx, gen.New(tx), req)
		return err
	})
	return res, err
}

// Run applies req through st. Apply wraps it in a transaction; tests call it
// with a fake store.
func Run(ctx context.Context, st Store, req Request) (Result, error) {
	var res Result
	rows, err := st.LockMediaItemsForBulkEdit(ctx, req.ItemIDs)
	if err != nil {
		return res, fmt.Errorf("lock items: %w", err)
	}
	res.Matched = len(rows)
	found := make(map[uuid.UUID]bool, len(rows))
	live := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		found[row.ID] = true
		live = append(live, row.ID)
	}
	for _, id := range req.ItemIDs {
		if !found[id] {
			res.Skipped = append(res.Skipped, Skip{ItemID: id, Reason: ReasonNotFound})
		}
	}

	for _, row := range rows {
		p, skips, write := Plan(row, req)
		res.Skipped = append(res.Skipped, skips...)
		if !write {
			continue
		}
		if _, err := st.EditMediaItemMetadata(ctx, p); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Locked FOR UPDATE above, so it can't have gone; report it
				// rather than fail the whole edit if it somehow did.
				res.Skipped = append(res.Skipped, Skip{ItemID: row.ID, Title: row.Title, Reason: ReasonNotFound})
				continue
			}
			return Result{}, fmt.Errorf("edit item %s: %w", row.ID, err)
		}
		res.Updated = append(res.Updated, row.ID)
	}

	// Labels aren't lockable and apply to every type. Remove first, then
	// add, as POST /labels/apply does.
	if len(req.RemoveLabels) > 0 && len(live) > 0 {
		if res.LabelsRemoved, err = st.RemoveMediaItemLabels(ctx, gen.RemoveMediaItemLabelsParams{
			Ids: live, Labels: req.RemoveLabels,
		}); err != nil {
			return Result{}, fmt.Errorf("remove labels: %w", err)
		}
	}
	if len(req.AddLabels) > 0 && len(live) > 0 {
		if res.LabelsAdded, err = st.AddMediaItemLabels(ctx, gen.AddMediaItemLabelsParams{
			Ids: live, Labels: req.AddLabels, MaxPerItem: int32(req.MaxLabels),
		}); err != nil {
			return Result{}, fmt.Errorf("add labels: %w", err)
		}
	}
	return res, nil
}

// Plan works out one item's share of req: the EditMediaItemMetadata params,
// the fields skipped on it, and whether there is anything to write.
//
// Locks mirror the single-item editor: every field written is locked. A field
// the item has locked is skipped unless req.Unlock names it, in which case the
// unlock happens first and the new value is written and locked again. Lock
// and Unlock on their own change only the lock set. A genre add / remove that
// leaves the item's genres as they were writes (and locks) nothing.
func Plan(row gen.LockMediaItemsForBulkEditRow, req Request) (gen.EditMediaItemMetadataParams, []Skip, bool) {
	p := gen.EditMediaItemMetadataParams{
		ID:           row.ID,
		SetFields:    []string{},
		LockFields:   []string{},
		UnlockFields: []string{},
		Genres:       []string{},
		// Title and sort title are never in SetFields here; the query reads
		// them only for fields it sets.
		Title:     row.Title,
		SortTitle: row.Title,
	}
	var skips []Skip
	skip := func(field, reason string) {
		skips = append(skips, Skip{ItemID: row.ID, Title: row.Title, Field: field, Reason: reason})
	}
	// writable reports whether field may be written on this item, recording
	// the skip when not.
	writable := func(field string) bool {
		if !Applies(field, row.Type) {
			skip(field, ReasonNotApplicable)
			return false
		}
		if slices.Contains(row.LockedFields, field) && !slices.Contains(req.Unlock, field) {
			skip(field, ReasonLocked)
			return false
		}
		return true
	}
	set := func(field string) {
		p.SetFields = append(p.SetFields, field)
		p.LockFields = append(p.LockFields, field)
	}

	if req.SetContentRating && writable(FieldContentRating) {
		p.ContentRating = req.ContentRating
		set(FieldContentRating)
	}
	switch {
	case req.SetGenres:
		if writable(FieldGenres) {
			p.Genres = append([]string{}, req.Genres...)
			set(FieldGenres)
		}
	case len(req.AddGenres) > 0 || len(req.RemoveGenres) > 0:
		if writable(FieldGenres) {
			if merged, changed := MergeGenres(row.Genres, req.AddGenres, req.RemoveGenres); changed {
				p.Genres = merged
				set(FieldGenres)
			}
		}
	}

	for _, f := range req.Lock {
		if !slices.Contains(p.LockFields, f) && !slices.Contains(row.LockedFields, f) {
			p.LockFields = append(p.LockFields, f)
		}
	}
	// An unlock of a field this edit writes would undo the lock the write
	// adds (the query drops unlock_fields last), so it's left out: the write
	// re-locks it, as intended.
	for _, f := range req.Unlock {
		if slices.Contains(row.LockedFields, f) && !slices.Contains(p.SetFields, f) {
			p.UnlockFields = append(p.UnlockFields, f)
		}
	}
	write := len(p.SetFields) > 0 || len(p.LockFields) > 0 || len(p.UnlockFields) > 0
	return p, skips, write
}

// MergeGenres removes then adds genres, case-insensitively: an added genre
// the item already has (in any case) keeps the item's spelling, and the
// item's order is kept with additions appended. changed is false when the
// result equals current.
func MergeGenres(current, add, remove []string) ([]string, bool) {
	drop := make(map[string]bool, len(remove))
	for _, g := range remove {
		drop[strings.ToLower(g)] = true
	}
	out := make([]string, 0, len(current)+len(add))
	seen := make(map[string]bool, len(current)+len(add))
	for _, g := range current {
		k := strings.ToLower(g)
		if drop[k] || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, g)
	}
	for _, g := range add {
		k := strings.ToLower(g)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, g)
	}
	return out, !slices.Equal(out, current)
}
