package v1

import (
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

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
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Type      string    `json:"type"`
	TMDBID    int32     `json:"tmdb_id"`
	Title     string    `json:"title"`
	Year      *int32    `json:"year,omitempty"`
	PosterURL *string   `json:"poster_url,omitempty"`
	Overview  *string   `json:"overview,omitempty"`
	Status    string    `json:"status"`
	Seasons   []int     `json:"seasons,omitempty"`
	// SeasonsAvailable (show requests only) lists the requested seasons that
	// are already complete in the library while the rest are still coming —
	// "partially available" without a new status value. [] until the first
	// season lands.
	SeasonsAvailable   *[]int     `json:"seasons_available,omitempty"`
	RequestedServiceID *uuid.UUID `json:"requested_service_id,omitempty"`
	QualityProfileID   *int32     `json:"quality_profile_id,omitempty"`
	RootFolder         *string    `json:"root_folder,omitempty"`
	ServiceID          *uuid.UUID `json:"service_id,omitempty"`
	DeclineReason      *string    `json:"decline_reason,omitempty"`
	DecidedBy          *uuid.UUID `json:"decided_by,omitempty"`
	DecidedAt          *string    `json:"decided_at,omitempty"`
	FulfilledItemID    *uuid.UUID `json:"fulfilled_item_id,omitempty"`
	FulfilledAt        *string    `json:"fulfilled_at,omitempty"`
	// AutoApproved marks a request approved at creation by the requester's
	// standing permission (or because they are an admin) rather than by an
	// admin decision; decided_by is absent on such rows.
	AutoApproved bool   `json:"auto_approved"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	// Username is the requester's name, on listings and single gets.
	Username string `json:"username,omitempty"`
	// Download is the live Radarr/Sonarr state the arr sync keeps for an
	// in-flight request; absent until its first sync and once available.
	Download *requestDownloadDTO `json:"download,omitempty"`
}

// requestDownloadDTO is the web client's RequestDownload.
type requestDownloadDTO struct {
	// State: searching | queued | downloading | import_pending | stalled | failed.
	State string `json:"state"`
	// Progress is the downloaded fraction, 0..1.
	Progress  *float64 `json:"progress,omitempty"`
	ETA       *string  `json:"eta,omitempty"`
	SizeBytes *int64   `json:"size_bytes,omitempty"`
	Message   string   `json:"message,omitempty"`
	UpdatedAt string   `json:"updated_at"`
}

// toRequestDownloadDTO renders the download columns, nil when the request
// has no download state.
func toRequestDownloadDTO(req gen.MediaRequest) *requestDownloadDTO {
	if req.DownloadState == nil || *req.DownloadState == "" {
		return nil
	}
	d := &requestDownloadDTO{State: *req.DownloadState}
	if req.DownloadProgress != nil {
		// float32 in the column; round so 0.6 doesn't render as 0.6000000238.
		p := math.Round(float64(*req.DownloadProgress)*10000) / 10000
		d.Progress = &p
	}
	if req.DownloadEta.Valid {
		t := req.DownloadEta.Time.UTC().Format(time.RFC3339)
		d.ETA = &t
	}
	d.SizeBytes = req.DownloadSizeBytes
	if req.DownloadMessage != nil {
		d.Message = *req.DownloadMessage
	}
	updated := req.DownloadUpdatedAt
	if !updated.Valid {
		updated = req.UpdatedAt
	}
	d.UpdatedAt = updated.Time.UTC().Format(time.RFC3339)
	return d
}

// userDownloadMessages replaces the reason on the states whose message can be
// the *arr app's own text — a download client's error, an import path, a
// release name — with a plain phrase for non-admin callers. That text can
// name internal hosts and folders; the requester only needs the gist, and
// admins see the detail. The other states only ever carry messages the sync
// writes itself ("Waiting for release", "3 of 10 episodes downloaded").
var userDownloadMessages = map[string]string{
	requests.DownloadStalled: "The download needs attention — an admin has the details",
	requests.DownloadFailed:  "The download failed — an admin has the details",
}

// toRequestDTOFor is toRequestDTO for a caller: non-admins get the redacted
// download message.
func toRequestDTOFor(req gen.MediaRequest, admin bool) requestDTO {
	dto := toRequestDTO(req)
	if !admin && dto.Download != nil {
		if msg, ok := userDownloadMessages[dto.Download.State]; ok {
			dto.Download.Message = msg
		}
	}
	return dto
}

// toRequestDTO renders a request in full (the admin view).
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
		AutoApproved:     req.AutoApproved,
		CreatedAt:        req.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:        req.UpdatedAt.Time.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if seasons, _ := decodeIntSlice(req.Seasons); len(seasons) > 0 {
		dto.Seasons = seasons
	}
	if req.Type == requests.TypeShow {
		avail := make([]int, 0, len(req.SeasonsAvailable))
		for _, n := range req.SeasonsAvailable {
			avail = append(avail, int(n))
		}
		dto.SeasonsAvailable = &avail
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
	dto.Download = toRequestDownloadDTO(req)
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
// A requester with auto-approval gets the request back already approved /
// downloading (auto_approved=true); if that send to the arr failed it comes
// back pending like any other, and the response is 201 either way.
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
	// quality_profile_id and root_folder are admin decisions: they pick how
	// much disk a grab uses and which library folder it lands in. A regular
	// user's values used to win at approval (the approve UI sends an empty
	// body), so a user could route content into another library's folder or
	// request remux-grade profiles. Keep only the service PREFERENCE, which the
	// UI surfaces to the approving admin.
	if !claims.IsAdmin {
		body.QualityProfileID = nil
		body.RootFolder = nil
	}
	res, err := h.svc.Create(r.Context(), requests.CreateInput{
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
		case errors.Is(err, requests.ErrSeasonsAlreadyRequested):
			respond.Error(w, r, http.StatusConflict, "ALREADY_REQUESTED",
				"you already have an active request for one of these seasons")
		case errors.Is(err, requests.ErrAlreadyRequested):
			respond.Error(w, r, http.StatusConflict, "ALREADY_REQUESTED",
				"you already have an active request for this title")
		case errors.Is(err, requests.ErrTMDBLookupFailed):
			respond.ValidationError(w, r, "tmdb lookup failed — verify the tmdb_id")
		case errors.Is(err, requests.ErrInvalidSeasons):
			// "invalid season list: season 9 is not listed on TMDB"
			respond.ValidationError(w, r, strings.TrimPrefix(err.Error(), "requests: "))
		case errors.Is(err, requests.ErrTooManyPending):
			respond.Error(w, r, http.StatusTooManyRequests, "TOO_MANY_PENDING_REQUESTS",
				"you have too many pending requests; wait for some to be reviewed")
		case errors.Is(err, requests.ErrRequestsDisabled):
			respond.Error(w, r, http.StatusForbidden, "REQUESTS_DISABLED",
				"requesting is turned off for your account; ask an admin")
		default:
			h.logger.ErrorContext(r.Context(), "create request", "err", err)
			respond.InternalError(w, r)
		}
		return
	}
	req := res.MediaRequest
	if req.AutoApproved {
		// Same audit action as an admin approval so the trail shows every
		// title that was sent to an arr; "automatic" tells the two apart and
		// the actor is the requester whose standing permission approved it.
		h.auditEvent(r, audit.ActionRequestApprove, req.ID.String(), map[string]any{
			"type":      req.Type,
			"tmdb_id":   req.TmdbID,
			"title":     req.Title,
			"user_id":   req.UserID.String(),
			"automatic": true,
		})
	}
	respond.Created(w, r, createdRequestDTO{requestDTO: toRequestDTOFor(req, claims.IsAdmin), OverQuota: res.OverQuota})
}

// createdRequestDTO is the POST /requests response: the request plus
// over_quota, true when the request exceeded the user's quota for its type and
// so is waiting for an admin rather than auto-approved.
type createdRequestDTO struct {
	requestDTO
	OverQuota bool `json:"over_quota"`
}

type quotaUsageDTO struct {
	Limit int `json:"limit"`
	Used  int `json:"used"`
	// Remaining is null when the limit is 0 (unlimited).
	Remaining *int `json:"remaining"`
}

type quotaDTO struct {
	CanRequest bool          `json:"can_request"`
	WindowDays int           `json:"window_days"`
	Movies     quotaUsageDTO `json:"movies"`
	TV         quotaUsageDTO `json:"tv"`
}

// Quota handles GET /api/v1/requests/quota: the caller's own request
// allowance — whether they may request at all, and per type the limit, how
// many they've used in the current window and how many remain (null =
// unlimited). Admins are always allowed and unlimited.
func (h *RequestHandler) Quota(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	st, err := h.svc.Quota(r.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, requests.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "request quota", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, quotaDTO{
		CanRequest: st.CanRequest,
		WindowDays: st.WindowDays,
		Movies:     quotaUsageDTO{Limit: st.Movies.Limit, Used: st.Movies.Used, Remaining: st.Movies.Remaining},
		TV:         quotaUsageDTO{Limit: st.TV.Limit, Used: st.TV.Used, Remaining: st.TV.Remaining},
	})
}

// PendingCount handles GET /api/v1/requests/pending-count (admin only): the
// number of requests waiting for approval, for the nav badge.
func (h *RequestHandler) PendingCount(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	// The route is mounted behind AdminRequired; checked again so the handler
	// is safe wherever it's mounted.
	if !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	n, err := h.svc.CountPending(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "count pending requests", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, map[string]int64{"count": n})
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
		respond.List(w, r, h.dtoSlice(r, rows, true), total, "")
		return
	}

	rows, total, err := h.svc.ListForUser(r.Context(), claims.UserID, status, page.Limit, page.Offset)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list requests for user", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.List(w, r, h.dtoSlice(r, rows, claims.IsAdmin), total, "")
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
	dto := toRequestDTOFor(req, claims.IsAdmin)
	dto.Username = h.svc.Usernames(r.Context(), []gen.MediaRequest{req})[req.UserID]
	respond.Success(w, r, dto)
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

// dtoSlice renders a page of requests with their requesters' usernames (one
// batched lookup) and the download message redacted unless admin.
func (h *RequestHandler) dtoSlice(r *http.Request, rows []gen.MediaRequest, admin bool) []requestDTO {
	names := h.svc.Usernames(r.Context(), rows)
	out := make([]requestDTO, 0, len(rows))
	for _, row := range rows {
		dto := toRequestDTOFor(row, admin)
		dto.Username = names[row.UserID]
		out = append(out, dto)
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
