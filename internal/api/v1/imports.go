package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/serverimport"
)

// ImportService is the server-import surface the handler drives. Satisfied
// by *serverimport.Service.
type ImportService interface {
	Connect(ctx context.Context, p serverimport.ConnectParams) (serverimport.ServerInfo, []serverimport.Account, error)
	Start(ctx context.Context, p serverimport.StartParams) (gen.ImportJob, error)
	Jobs(ctx context.Context, limit int32) ([]gen.ImportJob, error)
	Job(ctx context.Context, id uuid.UUID) (gen.ImportJob, error)
	Cancel(ctx context.Context, id uuid.UUID) error
}

// ImportUsers lists the OnScreen users accounts can be mapped to.
// Satisfied by *gen.Queries.
type ImportUsers interface {
	ListUsers(ctx context.Context) ([]gen.ListUsersRow, error)
}

// ImportHandler serves the admin import API: connect to a Plex, Jellyfin or
// Emby server, map its accounts to OnScreen users, and import (or preview)
// their watch state, ratings, favorites, playlists and play history.
type ImportHandler struct {
	svc    ImportService
	users  ImportUsers
	logger *slog.Logger
	audit  *audit.Logger
}

// NewImportHandler builds the handler.
func NewImportHandler(svc ImportService, users ImportUsers, logger *slog.Logger) *ImportHandler {
	return &ImportHandler{svc: svc, users: users, logger: logger}
}

// WithAudit attaches an audit logger.
func (h *ImportHandler) WithAudit(a *audit.Logger) *ImportHandler {
	h.audit = a
	return h
}

// ── DTOs ──────────────────────────────────────────────────────────────────

type importAccountDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	FullState bool   `json:"full_state"`
	// SuggestedUserID is the OnScreen user with the same name, if any.
	SuggestedUserID *uuid.UUID `json:"suggested_user_id,omitempty"`
}

type importConnectDTO struct {
	Kind       string             `json:"kind"`
	ServerName string             `json:"server_name"`
	Version    string             `json:"version"`
	Accounts   []importAccountDTO `json:"accounts"`
}

type importJobDTO struct {
	ID         uuid.UUID       `json:"id"`
	SourceKind string          `json:"source_kind"`
	SourceURL  string          `json:"source_url"`
	ServerName *string         `json:"server_name,omitempty"`
	Status     string          `json:"status"`
	DryRun     bool            `json:"dry_run"`
	Options    json.RawMessage `json:"options"`
	Mapping    json.RawMessage `json:"mapping"`
	Phase      string          `json:"phase"`
	Stats      json.RawMessage `json:"stats"`
	Unmatched  json.RawMessage `json:"unmatched"`
	Error      *string         `json:"error,omitempty"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
	FinishedAt *string         `json:"finished_at,omitempty"`
}

func rawJSON(b []byte, empty string) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage(empty)
	}
	return json.RawMessage(b)
}

func toImportJobDTO(j gen.ImportJob) importJobDTO {
	return importJobDTO{
		ID:         j.ID,
		SourceKind: j.SourceKind,
		SourceURL:  j.SourceUrl,
		ServerName: j.ServerName,
		Status:     j.Status,
		DryRun:     j.DryRun,
		Options:    rawJSON(j.Options, "{}"),
		Mapping:    rawJSON(j.Mapping, "[]"),
		Phase:      j.Phase,
		Stats:      rawJSON(j.Stats, "{}"),
		Unmatched:  rawJSON(j.Unmatched, "[]"),
		Error:      j.Error,
		CreatedAt:  j.CreatedAt.Time.UTC().Format(time.RFC3339),
		UpdatedAt:  j.UpdatedAt.Time.UTC().Format(time.RFC3339),
		FinishedAt: rfc3339(j.FinishedAt),
	}
}

// ── Handlers ──────────────────────────────────────────────────────────────

type importConnectBody struct {
	Kind  string `json:"kind"`
	URL   string `json:"url"`
	Token string `json:"token"`
}

func (b importConnectBody) params() serverimport.ConnectParams {
	return serverimport.ConnectParams{Kind: serverimport.Kind(b.Kind), URL: b.URL, Token: strings.TrimSpace(b.Token)}
}

// Connect handles POST /api/v1/admin/imports/connect: check the server URL
// and token and list the server's accounts, each with the OnScreen user of
// the same name as a suggested mapping.
func (h *ImportHandler) Connect(w http.ResponseWriter, r *http.Request) {
	var body importConnectBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	info, accounts, err := h.svc.Connect(r.Context(), body.params())
	if err != nil {
		h.writeError(w, r, "connect import source", err)
		return
	}
	users, err := h.users.ListUsers(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list users", "err", err)
		respond.InternalError(w, r)
		return
	}
	byName := map[string]uuid.UUID{}
	for _, u := range users {
		byName[strings.ToLower(u.Username)] = u.ID
	}
	out := importConnectDTO{Kind: body.Kind, ServerName: info.Name, Version: info.Version, Accounts: []importAccountDTO{}}
	for _, a := range accounts {
		d := importAccountDTO{ID: a.ID, Name: a.Name, FullState: a.FullState}
		if id, ok := byName[strings.ToLower(a.Name)]; ok {
			d.SuggestedUserID = &id
		}
		out.Accounts = append(out.Accounts, d)
	}
	respond.Success(w, r, out)
}

type importStartBody struct {
	importConnectBody
	Mapping []serverimport.AccountMapping `json:"mapping"`
	Options serverimport.Options          `json:"options"`
	DryRun  bool                          `json:"dry_run"`
}

// Start handles POST /api/v1/admin/imports: start an import (or, with
// dry_run, a preview that matches without writing). It runs in the
// background; poll GET /admin/imports/{id} for progress.
func (h *ImportHandler) Start(w http.ResponseWriter, r *http.Request) {
	var body importStartBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	users, err := h.users.ListUsers(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list users", "err", err)
		respond.InternalError(w, r)
		return
	}
	known := map[uuid.UUID]bool{}
	for _, u := range users {
		known[u.ID] = true
	}
	for _, m := range body.Mapping {
		if !known[m.UserID] {
			respond.ValidationError(w, r, "the mapping names a user that doesn't exist")
			return
		}
	}
	p := serverimport.StartParams{
		ConnectParams: body.params(),
		Mapping:       body.Mapping,
		Options:       body.Options,
		DryRun:        body.DryRun,
	}
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		uid := claims.UserID
		p.CreatedBy = &uid
	}
	job, err := h.svc.Start(r.Context(), p)
	if err != nil {
		h.writeError(w, r, "start import", err)
		return
	}
	if !job.DryRun {
		h.auditEvent(r, audit.ActionImportStart, job.ID.String(), map[string]any{
			"source_kind": job.SourceKind,
			"source_url":  job.SourceUrl,
			"server_name": job.ServerName,
			"options":     body.Options,
			"mapping":     body.Mapping,
		})
	}
	respond.Accepted(w, r, toImportJobDTO(job))
}

// List handles GET /api/v1/admin/imports: recent imports, newest first.
func (h *ImportHandler) List(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.svc.Jobs(r.Context(), 20)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list imports", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]importJobDTO, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, toImportJobDTO(j))
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// Get handles GET /api/v1/admin/imports/{id}.
func (h *ImportHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid import id")
		return
	}
	job, err := h.svc.Job(r.Context(), id)
	if err != nil {
		h.writeError(w, r, "get import", err)
		return
	}
	respond.Success(w, r, toImportJobDTO(job))
}

// Cancel handles POST /api/v1/admin/imports/{id}/cancel. What was written
// before the cancel stays; a re-run picks up the rest.
func (h *ImportHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid import id")
		return
	}
	if err := h.svc.Cancel(r.Context(), id); err != nil {
		h.writeError(w, r, "cancel import", err)
		return
	}
	h.auditEvent(r, audit.ActionImportCancel, id.String(), nil)
	respond.NoContent(w)
}

// writeError maps service errors to responses; anything unexpected is a 500.
func (h *ImportHandler) writeError(w http.ResponseWriter, r *http.Request, op string, err error) {
	var fetchErr *serverimport.FetchError
	switch {
	case errors.Is(err, serverimport.ErrJobNotFound):
		respond.NotFound(w, r)
	case errors.Is(err, serverimport.ErrImportRunning):
		respond.Error(w, r, http.StatusConflict, "IMPORT_RUNNING", err.Error())
	case errors.Is(err, serverimport.ErrJobNotRunning):
		respond.Error(w, r, http.StatusConflict, "IMPORT_NOT_RUNNING", err.Error())
	case errors.Is(err, serverimport.ErrUnauthorized):
		respond.Error(w, r, http.StatusUnprocessableEntity, "IMPORT_SOURCE_UNAUTHORIZED", err.Error())
	case errors.Is(err, serverimport.ErrUnreachable):
		respond.Error(w, r, http.StatusBadGateway, "IMPORT_SOURCE_UNAVAILABLE", err.Error())
	case errors.As(err, &fetchErr):
		respond.Error(w, r, http.StatusBadGateway, "IMPORT_SOURCE_UNAVAILABLE", fetchErr.Msg)
	case errors.Is(err, serverimport.ErrInvalidKind), errors.Is(err, serverimport.ErrInvalidURL),
		errors.Is(err, serverimport.ErrNoToken), errors.Is(err, serverimport.ErrNoMapping),
		errors.Is(err, serverimport.ErrNothingSelected), errors.Is(err, serverimport.ErrUnknownAccount):
		respond.ValidationError(w, r, err.Error())
	default:
		h.logger.ErrorContext(r.Context(), op, "err", err)
		respond.InternalError(w, r)
	}
}

func (h *ImportHandler) auditEvent(r *http.Request, action, target string, detail map[string]any) {
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
