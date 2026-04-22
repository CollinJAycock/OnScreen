package v1

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/requests"
)

// RequestHandler exposes the user-facing + admin REST surface for the
// media-request workflow. Business logic (TMDB snapshot, arr dispatch,
// notifications) lives in the requests.Service; this layer is HTTP shape
// only.
type RequestHandler struct {
	svc    *requests.Service
	logger *slog.Logger
	audit  *audit.Logger
}

// NewRequestHandler builds a handler around the given service.
func NewRequestHandler(svc *requests.Service, logger *slog.Logger) *RequestHandler {
	return &RequestHandler{svc: svc, logger: logger}
}

// WithAudit attaches an audit logger.
func (h *RequestHandler) WithAudit(a *audit.Logger) *RequestHandler {
	h.audit = a
	return h
}

// ── DTOs ──────────────────────────────────────────────────────────────────

type requestDTO struct {
	ID                 uuid.UUID  `json:"id"`
	UserID             uuid.UUID  `json:"user_id"`
	Type               string     `json:"type"`
	TMDBID             int32      `json:"tmdb_id"`
	Title              string     `json:"title"`
	Year               *int32     `json:"year,omitempty"`
	PosterURL          *string    `json:"poster_url,omitempty"`
	Overview           *string    `json:"overview,omitempty"`
	Status             string     `json:"status"`
	Seasons            []int      `json:"seasons,omitempty"`
	RequestedServiceID *uuid.UUID `json:"requested_service_id,omitempty"`
	QualityProfileID   *int32     `json:"quality_profile_id,omitempty"`
	RootFolder         *string    `json:"root_folder,omitempty"`
	ServiceID          *uuid.UUID `json:"service_id,omitempty"`
	DeclineReason      *string    `json:"decline_reason,omitempty"`
	DecidedBy          *uuid.UUID `json:"decided_by,omitempty"`
	DecidedAt          *string    `json:"decided_at,omitempty"`
	FulfilledItemID    *uuid.UUID `json:"fulfilled_item_id,omitempty"`
	FulfilledAt        *string    `json:"fulfilled_at,omitempty"`
	CreatedAt          string     `json:"created_at"`
	UpdatedAt          string     `json:"updated_at"`
}

func toRequestDTO(req gen.MediaRequest) requestDTO {
	dto := requestDTO{
		ID:               req.ID,
		UserID:           req.UserID,
		Type:             req.Type,
		TMDBID:           req.TmdbID,
		Title:            req.Title,
		Year:             req.Year,
		PosterURL:        req.PosterUrl,
		Overview:         req.Overview,
		Status:           req.Status,
		QualityProfileID: req.QualityProfileID,
		RootFolder:       req.RootFolder,
		DeclineReason:    req.DeclineReason,
		CreatedAt:        req.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:        req.UpdatedAt.Time.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if seasons, _ := decodeIntSlice(req.Seasons); len(seasons) > 0 {
		dto.Seasons = seasons
	}
	if req.RequestedServiceID.Valid {
		id := uuid.UUID(req.RequestedServiceID.Bytes)
		dto.RequestedServiceID = &id
	}
	if req.ServiceID.Valid {
		id := uuid.UUID(req.ServiceID.Bytes)
		dto.ServiceID = &id
	}
	if req.DecidedBy.Valid {
		id := uuid.UUID(req.DecidedBy.Bytes)
		dto.DecidedBy = &id
	}
	if req.DecidedAt.Valid {
		t := req.DecidedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		dto.DecidedAt = &t
	}
	if req.FulfilledItemID.Valid {
		id := uuid.UUID(req.FulfilledItemID.Bytes)
		dto.FulfilledItemID = &id
	}
	if req.FulfilledAt.Valid {
		t := req.FulfilledAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		dto.FulfilledAt = &t
	}
	return dto
}

// ── User endpoints ────────────────────────────────────────────────────────

type createRequestBody struct {
	Type               string     `json:"type"`
	TMDBID             int        `json:"tmdb_id"`
	Seasons            []int      `json:"seasons,omitempty"`
	RequestedServiceID *uuid.UUID `json:"requested_service_id,omitempty"`
	QualityProfileID   *int32     `json:"quality_profile_id,omitempty"`
	RootFolder         *string    `json:"root_folder,omitempty"`
}

// Create handles POST /api/v1/requests. Anyone authenticated can submit.
// The service rejects duplicates with ErrAlreadyRequested; we map that to
// 409 so the UI can show "you already requested this" without re-checking.
func (h *RequestHandler) Create(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	var body createRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	req, err := h.svc.Create(r.Context(), requests.CreateInput{
		UserID:             claims.UserID,
		Type:               strings.ToLower(strings.TrimSpace(body.Type)),
		TMDBID:             body.TMDBID,
		Seasons:            body.Seasons,
		RequestedServiceID: body.RequestedServiceID,
		QualityProfileID:   body.QualityProfileID,
		RootFolder:         body.RootFolder,
	})
	if err != nil {
		switch {
		case errors.Is(err, requests.ErrInvalidType):
			respond.ValidationError(w, r, "type must be 'movie' or 'show'")
		case errors.Is(err, requests.ErrInvalidTMDBID):
			respond.ValidationError(w, r, "tmdb_id is required")
		case errors.Is(err, requests.ErrAlreadyRequested):
			respond.Error(w, r, http.StatusConflict, "ALREADY_REQUESTED",
				"you already have an active request for this title")
		case errors.Is(err, requests.ErrTMDBLookupFailed):
			respond.ValidationError(w, r, "tmdb lookup failed — verify the tmdb_id")
		default:
			h.logger.ErrorContext(r.Context(), "create request", "err", err)
			respond.InternalError(w, r)
		}
		return
	}
	respond.Created(w, r, toRequestDTO(req))
}

// List handles GET /api/v1/requests. Users see their own history; admins
// can pass `scope=all` to see the full queue. Optional `status` filter.
func (h *RequestHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	page := respond.ParsePagination(r, 50, 200)
	status := optionalStatusFilter(r.URL.Query().Get("status"))

	scope := r.URL.Query().Get("scope")
	if scope == "all" {
		if !claims.IsAdmin {
			respond.Forbidden(w, r)
			return
		}
		rows, total, err := h.svc.ListAll(r.Context(), status, page.Limit, page.Offset)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list all requests", "err", err)
			respond.InternalError(w, r)
			return
		}
		respond.List(w, r, dtoSlice(rows), total, "")
		return
	}

	rows, total, err := h.svc.ListForUser(r.Context(), claims.UserID, status, page.Limit, page.Offset)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list requests for user", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.List(w, r, dtoSlice(rows), total, "")
}

// Get handles GET /api/v1/requests/{id}. Owner or admin only.
func (h *RequestHandler) Get(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid request id")
		return
	}
	req, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, requests.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get request", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !claims.IsAdmin && req.UserID != claims.UserID {
		respond.Forbidden(w, r)
		return
	}
	respond.Success(w, r, toRequestDTO(req))
}

// Cancel handles POST /api/v1/requests/{id}/cancel. Withdraw a still-pending
// request; owner only. Admins decline via the admin endpoints.
func (h *RequestHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid request id")
		return
	}
	if err := h.svc.Cancel(r.Context(), id, claims.UserID); err != nil {
		switch {
		case errors.Is(err, requests.ErrNotFound):
			respond.NotFound(w, r)
		case errors.Is(err, requests.ErrNotOwner):
			respond.Forbidden(w, r)
		case errors.Is(err, requests.ErrNotPending):
			respond.Error(w, r, http.StatusConflict, "NOT_PENDING",
				"request has already been decided and cannot be cancelled")
		default:
			h.logger.ErrorContext(r.Context(), "cancel request", "id", id, "err", err)
			respond.InternalError(w, r)
		}
		return
	}
	respond.NoContent(w)
}

// ── Admin endpoints ───────────────────────────────────────────────────────

type approveBody struct {
	ServiceID        *uuid.UUID `json:"service_id"`
	QualityProfileID *int32     `json:"quality_profile_id"`
	RootFolder       *string    `json:"root_folder"`
}

// Approve handles POST /api/v1/admin/requests/{id}/approve. Body fields
// override the per-request and per-service defaults at approval time —
// useful when the admin wants to redirect a request to a different arr
// instance than the user originally chose.
func (h *RequestHandler) Approve(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid request id")
		return
	}
	var body approveBody
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respond.BadRequest(w, r, "invalid request body")
			return
		}
	}
	approved, err := h.svc.Approve(r.Context(), requests.ApproveInput{
		RequestID:        id,
		AdminID:          claims.UserID,
		ServiceID:        body.ServiceID,
		QualityProfileID: body.QualityProfileID,
		RootFolder:       body.RootFolder,
	})
	if err != nil {
		switch {
		case errors.Is(err, requests.ErrNotFound):
			respond.NotFound(w, r)
		case errors.Is(err, requests.ErrNotPending):
			respond.Error(w, r, http.StatusConflict, "NOT_PENDING",
				"request is no longer pending")
		case errors.Is(err, requests.ErrNoArrService):
			respond.ValidationError(w, r,
				"no arr service configured for this media type — set a default in admin")
		case errors.Is(err, requests.ErrArrServiceMismatch):
			respond.ValidationError(w, r, "selected arr service does not match the request type")
		case errors.Is(err, requests.ErrArrServiceDisabled):
			respond.ValidationError(w, r, "selected arr service is disabled")
		case errors.Is(err, requests.ErrArrAddFailed):
			respond.ValidationError(w, r, err.Error())
		default:
			h.logger.ErrorContext(r.Context(), "approve request", "id", id, "err", err)
			respond.InternalError(w, r)
		}
		return
	}
	h.auditEvent(r, audit.ActionRequestApprove, approved.ID.String(), map[string]any{
		"type":     approved.Type,
		"tmdb_id":  approved.TmdbID,
		"title":    approved.Title,
		"user_id":  approved.UserID.String(),
		"admin_id": claims.UserID.String(),
	})
	respond.Success(w, r, toRequestDTO(approved))
}

type declineBody struct {
	Reason string `json:"reason"`
}

// Decline handles POST /api/v1/admin/requests/{id}/decline.
func (h *RequestHandler) Decline(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid request id")
		return
	}
	var body declineBody
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respond.BadRequest(w, r, "invalid request body")
			return
		}
	}
	declined, err := h.svc.Decline(r.Context(), id, claims.UserID, strings.TrimSpace(body.Reason))
	if err != nil {
		switch {
		case errors.Is(err, requests.ErrNotFound):
			respond.NotFound(w, r)
		case errors.Is(err, requests.ErrNotPending):
			respond.Error(w, r, http.StatusConflict, "NOT_PENDING",
				"request is no longer pending")
		default:
			h.logger.ErrorContext(r.Context(), "decline request", "id", id, "err", err)
			respond.InternalError(w, r)
		}
		return
	}
	h.auditEvent(r, audit.ActionRequestDecline, declined.ID.String(), map[string]any{
		"type":     declined.Type,
		"tmdb_id":  declined.TmdbID,
		"title":    declined.Title,
		"user_id":  declined.UserID.String(),
		"admin_id": claims.UserID.String(),
		"reason":   body.Reason,
	})
	respond.Success(w, r, toRequestDTO(declined))
}

// Delete handles DELETE /api/v1/admin/requests/{id}. Admin escape hatch —
// the upstream arr is not contacted, only the OnScreen row goes away.
func (h *RequestHandler) Delete(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid request id")
		return
	}
	// Snapshot pre-delete so the audit row preserves what was wiped.
	if existing, getErr := h.svc.Get(r.Context(), id); getErr == nil {
		defer h.auditEvent(r, audit.ActionRequestDelete, id.String(), map[string]any{
			"type":    existing.Type,
			"tmdb_id": existing.TmdbID,
			"title":   existing.Title,
			"status":  existing.Status,
		})
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		h.logger.ErrorContext(r.Context(), "delete request", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// ── helpers ───────────────────────────────────────────────────────────────

func dtoSlice(rows []gen.MediaRequest) []requestDTO {
	out := make([]requestDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, toRequestDTO(r))
	}
	return out
}

// optionalStatusFilter returns a *string when the value is one of the known
// status names. Unknown / empty values map to nil so the SQL filter is a
// no-op rather than silently returning zero rows.
func optionalStatusFilter(s string) *string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case requests.StatusPending,
		requests.StatusApproved,
		requests.StatusDeclined,
		requests.StatusDownloading,
		requests.StatusAvailable,
		requests.StatusFailed:
		v := strings.ToLower(strings.TrimSpace(s))
		return &v
	}
	return nil
}

func decodeIntSlice(raw []byte) ([]int, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []int
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (h *RequestHandler) auditEvent(r *http.Request, action, target string, detail map[string]any) {
	if h.audit == nil {
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return
	}
	actor := claims.UserID
	h.audit.Log(r.Context(), &actor, action, target, detail, audit.ClientIP(r))
}
