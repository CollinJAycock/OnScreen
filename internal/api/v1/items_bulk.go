package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/bulkedit"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/itemlabel"
)

// maxBulkEditItems bounds one PATCH /items/bulk body, matching the bulk
// label and "Add to collection" caps.
const maxBulkEditItems = 500

// bulkNFOTimeout bounds the background NFO rewrite after a bulk edit; the
// rewrites run one at a time so a slow network share is never hit by 500 at
// once.
const bulkNFOTimeout = 10 * time.Minute

// BulkMetadataEditor runs a bulk metadata edit in one transaction. Satisfied
// by *bulkedit.Service.
type BulkMetadataEditor interface {
	Apply(ctx context.Context, req bulkedit.Request) (bulkedit.Result, error)
}

// WithBulkEditor enables PATCH /items/bulk.
func (h *ItemHandler) WithBulkEditor(b BulkMetadataEditor) *ItemHandler {
	h.bulk = b
	return h
}

// bulkEditSet is the "set" object of a bulk edit: the plain fields written
// to every selected item they apply to. content_rating: a string, or "" /
// null to clear. genres: the full list, [] clears.
type bulkEditSet struct {
	ContentRating nullable[string] `json:"content_rating"`
	Genres        *[]string        `json:"genres,omitempty"`
}

// bulkEditRequest is the body for PATCH /api/v1/items/bulk.
type bulkEditRequest struct {
	ItemIDs      []string     `json:"item_ids"`
	Set          *bulkEditSet `json:"set,omitempty"`
	AddGenres    []string     `json:"add_genres,omitempty"`
	RemoveGenres []string     `json:"remove_genres,omitempty"`
	AddLabels    []string     `json:"add_labels,omitempty"`
	RemoveLabels []string     `json:"remove_labels,omitempty"`
	Lock         []string     `json:"lock,omitempty"`
	Unlock       []string     `json:"unlock,omitempty"`
}

type bulkEditSkipJSON struct {
	ItemID string `json:"item_id"`
	Title  string `json:"title,omitempty"`
	Field  string `json:"field,omitempty"`
	Reason string `json:"reason"`
}

type bulkEditResponse struct {
	Matched       int                `json:"matched"`
	Updated       int                `json:"updated"`
	LabelsAdded   int64              `json:"labels_added"`
	LabelsRemoved int64              `json:"labels_removed"`
	Skipped       []bulkEditSkipJSON `json:"skipped"`
}

// toRequest validates the body into a bulkedit.Request. Returns a
// client-facing message on invalid input.
func (b *bulkEditRequest) toRequest() (bulkedit.Request, error) {
	var req bulkedit.Request
	if len(b.ItemIDs) == 0 {
		return req, errors.New("item_ids is empty")
	}
	if len(b.ItemIDs) > maxBulkEditItems {
		return req, fmt.Errorf("at most %d items in one request", maxBulkEditItems)
	}
	seen := make(map[uuid.UUID]bool, len(b.ItemIDs))
	for _, s := range b.ItemIDs {
		id, err := uuid.Parse(s)
		if err != nil {
			return req, errors.New("invalid item id in item_ids")
		}
		if seen[id] {
			return req, errors.New("duplicate item id in item_ids")
		}
		seen[id] = true
		req.ItemIDs = append(req.ItemIDs, id)
	}

	if b.Set != nil {
		if b.Set.ContentRating.Set {
			req.SetContentRating = true
			if !b.Set.ContentRating.Null {
				v, err := optionalText(b.Set.ContentRating.Value, "content_rating", maxEditRatingLen)
				if err != nil {
					return req, err
				}
				req.ContentRating = v
			}
		}
		if b.Set.Genres != nil {
			if len(b.AddGenres) > 0 || len(b.RemoveGenres) > 0 {
				return req, errors.New("set.genres replaces the list; send it or add_genres / remove_genres, not both")
			}
			g, err := cleanGenres(*b.Set.Genres)
			if err != nil {
				return req, err
			}
			req.SetGenres, req.Genres = true, g
		}
	}
	var err error
	if req.AddGenres, err = cleanGenres(b.AddGenres); err != nil {
		return req, errors.New("add_genres: " + err.Error())
	}
	if req.RemoveGenres, err = cleanGenres(b.RemoveGenres); err != nil {
		return req, errors.New("remove_genres: " + err.Error())
	}
	if req.AddLabels, err = itemlabel.NormalizeList(b.AddLabels, itemlabel.MaxPerItem); err != nil {
		return req, errors.New("add_labels: " + err.Error())
	}
	if req.RemoveLabels, err = itemlabel.NormalizeList(b.RemoveLabels, itemlabel.MaxPerItem); err != nil {
		return req, errors.New("remove_labels: " + err.Error())
	}
	req.MaxLabels = itemlabel.MaxPerItem

	if req.Lock, err = lockableList(b.Lock, "lock"); err != nil {
		return req, err
	}
	if req.Unlock, err = lockableList(b.Unlock, "unlock"); err != nil {
		return req, err
	}
	for _, f := range req.Lock {
		if slices.Contains(req.Unlock, f) {
			return req, fmt.Errorf("%q is in both lock and unlock", f)
		}
	}

	if !req.SetContentRating && !req.SetGenres &&
		len(req.AddGenres) == 0 && len(req.RemoveGenres) == 0 &&
		len(req.AddLabels) == 0 && len(req.RemoveLabels) == 0 &&
		len(req.Lock) == 0 && len(req.Unlock) == 0 {
		return req, errors.New("nothing to change")
	}
	return req, nil
}

// lockableList checks every name is a lockable field, dropping repeats.
func lockableList(in []string, what string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, f := range in {
		if !media.IsLockableField(f) {
			return nil, fmt.Errorf("%s: %q is not a lockable field", what, f)
		}
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out, nil
}

// bulkFieldsSet names the metadata fields a bulk request writes, for the
// audit event.
func bulkFieldsSet(req bulkedit.Request) []string {
	var out []string
	if req.SetContentRating {
		out = append(out, bulkedit.FieldContentRating)
	}
	if req.SetGenres || len(req.AddGenres) > 0 || len(req.RemoveGenres) > 0 {
		out = append(out, bulkedit.FieldGenres)
	}
	return out
}

// BulkUpdateMetadata handles PATCH /api/v1/items/bulk.
//
// Admin-only. Applies one edit to up to 500 items in a single transaction:
// set.content_rating, set.genres (replace) or add_genres / remove_genres
// (merge, case-insensitive), add_labels / remove_labels (as POST
// /labels/apply), and lock / unlock of any lockable field. Item types may be
// mixed; a field an item's type doesn't carry is skipped and reported, and
// so is a field the item has locked — unless the request also unlocks it,
// in which case the unlock comes first and the new value is written. As in
// the single-item editor, every field written is locked against refreshes.
// Ids with no live item are reported as not_found.
//
// Responds {matched, updated, labels_added, labels_removed, skipped: [{item_id,
// title, field, reason}]}. NFOs of the updated items are rewritten in the
// background in libraries that save them.
func (h *ItemHandler) BulkUpdateMetadata(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	if h.bulk == nil {
		respond.NotFound(w, r)
		return
	}
	var body bulkEditRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	req, err := body.toRequest()
	if err != nil {
		respond.BadRequest(w, r, err.Error())
		return
	}

	res, err := h.bulk.Apply(r.Context(), req)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "bulk metadata edit", "items", len(req.ItemIDs), "err", err)
		respond.InternalError(w, r)
		return
	}

	if len(res.Updated) > 0 || res.LabelsAdded > 0 || res.LabelsRemoved > 0 {
		if h.caches != nil {
			h.caches.InvalidateItemCaches()
		}
	}
	h.writeNFOsAfterBulkEdit(r.Context(), res.Updated)

	if h.audit != nil {
		h.audit.Log(r.Context(), &claims.UserID, audit.ActionItemBulkEdit, "",
			map[string]any{
				"items":          len(req.ItemIDs),
				"matched":        res.Matched,
				"updated":        len(res.Updated),
				"skipped":        len(res.Skipped),
				"fields_set":     bulkFieldsSet(req),
				"add_genres":     req.AddGenres,
				"remove_genres":  req.RemoveGenres,
				"add_labels":     req.AddLabels,
				"remove_labels":  req.RemoveLabels,
				"labels_added":   res.LabelsAdded,
				"labels_removed": res.LabelsRemoved,
				"lock":           req.Lock,
				"unlock":         req.Unlock,
			}, audit.ClientIP(r))
	}

	out := bulkEditResponse{
		Matched:       res.Matched,
		Updated:       len(res.Updated),
		LabelsAdded:   res.LabelsAdded,
		LabelsRemoved: res.LabelsRemoved,
		Skipped:       make([]bulkEditSkipJSON, 0, len(res.Skipped)),
	}
	for _, s := range res.Skipped {
		out.Skipped = append(out.Skipped, bulkEditSkipJSON{
			ItemID: s.ItemID.String(), Title: s.Title, Field: s.Field, Reason: s.Reason,
		})
	}
	respond.Success(w, r, out)
}

// writeNFOsAfterBulkEdit rewrites the edited items' NFOs (save_nfo libraries
// only; WriteItemNFO checks) one at a time in the background, detached from
// the request and bounded by bulkNFOTimeout.
func (h *ItemHandler) writeNFOsAfterBulkEdit(ctx context.Context, ids []uuid.UUID) {
	if h.nfoWriter == nil || len(ids) == 0 {
		return
	}
	ids = slices.Clone(ids)
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), bulkNFOTimeout)
	go func() {
		defer cancel()
		for i, id := range ids {
			if bg.Err() != nil {
				h.logger.WarnContext(context.WithoutCancel(bg), "bulk edit: NFO rewrite timed out",
					"remaining", len(ids)-i)
				return
			}
			h.nfoWriter.WriteItemNFO(bg, id)
		}
	}()
}
