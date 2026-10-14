package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/itemlabel"
)

// maxBulkLabelItems bounds one POST /labels/apply body, matching the bulk
// "Add to collection" cap.
const maxBulkLabelItems = 500

// LabelsDB is the slice of gen.Queries the admin label endpoints use.
type LabelsDB interface {
	GetMediaItemLabels(ctx context.Context, id uuid.UUID) ([]string, error)
	SetMediaItemLabels(ctx context.Context, arg gen.SetMediaItemLabelsParams) (int64, error)
	AddMediaItemLabels(ctx context.Context, arg gen.AddMediaItemLabelsParams) (int64, error)
	RemoveMediaItemLabels(ctx context.Context, arg gen.RemoveMediaItemLabelsParams) (int64, error)
	ListLabelsInUse(ctx context.Context) ([]gen.ListLabelsInUseRow, error)
}

// LabelHandler serves the admin label endpoints: labels in use (for
// autocomplete), one item's labels, and bulk add/remove. Labels are what
// per-user label restrictions match (see internal/itemlabel); every route is
// admin-only and mounted behind AdminRequired, and each handler re-checks.
type LabelHandler struct {
	db     LabelsDB
	audit  *audit.Logger
	logger *slog.Logger
}

// NewLabelHandler creates a LabelHandler.
func NewLabelHandler(db LabelsDB, logger *slog.Logger) *LabelHandler {
	return &LabelHandler{db: db, logger: logger}
}

// WithAudit records label edits in the audit log.
func (h *LabelHandler) WithAudit(a *audit.Logger) *LabelHandler {
	h.audit = a
	return h
}

type labelInUseResponse struct {
	Label     string `json:"label"`
	ItemCount int64  `json:"item_count"`
}

type itemLabelsResponse struct {
	Labels []string `json:"labels"`
}

// adminClaims returns the caller's claims when they are an admin, writing a
// 403 and returning nil otherwise.
func (h *LabelHandler) adminClaims(w http.ResponseWriter, r *http.Request) *auth.Claims {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return nil
	}
	return claims
}

// List handles GET /api/v1/labels — every label on a live item with its item
// count, alphabetical.
func (h *LabelHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.adminClaims(w, r) == nil {
		return
	}
	rows, err := h.db.ListLabelsInUse(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list labels in use", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]labelInUseResponse, len(rows))
	for i, row := range rows {
		out[i] = labelInUseResponse{Label: row.Label, ItemCount: row.ItemCount}
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// GetItem handles GET /api/v1/items/{id}/labels — the item's own labels (not
// the ones it inherits from its show / album).
func (h *LabelHandler) GetItem(w http.ResponseWriter, r *http.Request) {
	if h.adminClaims(w, r) == nil {
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	labels, err := h.db.GetMediaItemLabels(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get item labels", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, itemLabelsResponse{Labels: nonNilStrings(labels)})
}

// SetItem handles PUT /api/v1/items/{id}/labels with {"labels": [...]},
// replacing the item's labels. Labels are normalised (lower-case,
// single-spaced) and de-duplicated; at most itemlabel.MaxPerItem. Returns
// the stored labels.
func (h *LabelHandler) SetItem(w http.ResponseWriter, r *http.Request) {
	claims := h.adminClaims(w, r)
	if claims == nil {
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	var body struct {
		Labels []string `json:"labels"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	labels, err := itemlabel.NormalizeList(body.Labels, itemlabel.MaxPerItem)
	if err != nil {
		respond.ValidationError(w, r, err.Error())
		return
	}
	n, err := h.db.SetMediaItemLabels(r.Context(), gen.SetMediaItemLabelsParams{ID: id, Labels: labels})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "set item labels", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if n == 0 {
		respond.NotFound(w, r)
		return
	}
	if h.audit != nil {
		h.audit.Log(r.Context(), &claims.UserID, audit.ActionItemLabelsChange, id.String(),
			map[string]any{"labels": labels}, audit.ClientIP(r))
	}
	respond.Success(w, r, itemLabelsResponse{Labels: labels})
}

// Apply handles POST /api/v1/labels/apply with
// {"item_ids": [...], "add": [...], "remove": [...]}: the library
// multi-select's "Add label…". Removes first, then adds, so a label in both
// lists ends up present. At most maxBulkLabelItems ids. An item the add
// would push past itemlabel.MaxPerItem is left without the added labels and
// not counted. Responds {"added": n, "removed": n} — the items each step
// changed.
func (h *LabelHandler) Apply(w http.ResponseWriter, r *http.Request) {
	claims := h.adminClaims(w, r)
	if claims == nil {
		return
	}
	var body struct {
		ItemIDs []string `json:"item_ids"`
		Add     []string `json:"add"`
		Remove  []string `json:"remove"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if len(body.ItemIDs) == 0 {
		respond.BadRequest(w, r, "item_ids is empty")
		return
	}
	if len(body.ItemIDs) > maxBulkLabelItems {
		respond.BadRequest(w, r, "too many items in one request")
		return
	}
	ids, ok := parseUniqueIDs(w, r, body.ItemIDs, "item_ids")
	if !ok {
		return
	}
	add, err := itemlabel.NormalizeList(body.Add, itemlabel.MaxPerItem)
	if err != nil {
		respond.ValidationError(w, r, "add: "+err.Error())
		return
	}
	remove, err := itemlabel.NormalizeList(body.Remove, itemlabel.MaxPerItem)
	if err != nil {
		respond.ValidationError(w, r, "remove: "+err.Error())
		return
	}
	if len(add) == 0 && len(remove) == 0 {
		respond.BadRequest(w, r, "nothing to add or remove")
		return
	}
	var removed, added int64
	if len(remove) > 0 {
		if removed, err = h.db.RemoveMediaItemLabels(r.Context(), gen.RemoveMediaItemLabelsParams{Ids: ids, Labels: remove}); err != nil {
			h.logger.ErrorContext(r.Context(), "remove item labels", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if len(add) > 0 {
		if added, err = h.db.AddMediaItemLabels(r.Context(), gen.AddMediaItemLabelsParams{
			Ids: ids, Labels: add, MaxPerItem: itemlabel.MaxPerItem,
		}); err != nil {
			h.logger.ErrorContext(r.Context(), "add item labels", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if h.audit != nil {
		h.audit.Log(r.Context(), &claims.UserID, audit.ActionItemLabelsChange, "",
			map[string]any{"items": len(ids), "add": add, "remove": remove, "added": added, "removed": removed},
			audit.ClientIP(r))
	}
	respond.Success(w, r, map[string]int64{"added": added, "removed": removed})
}
