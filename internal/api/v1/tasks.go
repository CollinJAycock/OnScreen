package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/scheduler"
)

// TasksQuerier is the sqlc surface the handler uses. Kept as an
// interface so tests can provide a fake without a live DB.
type TasksQuerier interface {
	ListScheduledTasks(ctx context.Context) ([]gen.ScheduledTask, error)
	GetScheduledTask(ctx context.Context, id uuid.UUID) (gen.ScheduledTask, error)
	CreateScheduledTask(ctx context.Context, arg gen.CreateScheduledTaskParams) (gen.ScheduledTask, error)
	UpdateScheduledTask(ctx context.Context, arg gen.UpdateScheduledTaskParams) (gen.ScheduledTask, error)
	DeleteScheduledTask(ctx context.Context, id uuid.UUID) error
	SetScheduledTaskNextRun(ctx context.Context, arg gen.SetScheduledTaskNextRunParams) error
	ListTaskRuns(ctx context.Context, arg gen.ListTaskRunsParams) ([]gen.TaskRun, error)
}

// TasksHandler exposes admin-only CRUD + trigger + run-history for the
// scheduler's scheduled_tasks table. Route group is /api/v1/admin/tasks.
type TasksHandler struct {
	q        TasksQuerier
	registry *scheduler.Registry
	logger   *slog.Logger
}

// NewTasksHandler constructs a TasksHandler.
func NewTasksHandler(q TasksQuerier, registry *scheduler.Registry, logger *slog.Logger) *TasksHandler {
	return &TasksHandler{q: q, registry: registry, logger: logger}
}

// taskResponse is the wire shape for a scheduled_tasks row. Kept separate
// from gen.ScheduledTask so we control time/pointer types at the edge.
type taskResponse struct {
	ID         uuid.UUID       `json:"id"`
	Name       string          `json:"name"`
	TaskType   string          `json:"task_type"`
	Config     json.RawMessage `json:"config"`
	CronExpr   string          `json:"cron_expr"`
	Enabled    bool            `json:"enabled"`
	LastRunAt  *time.Time      `json:"last_run_at"`
	NextRunAt  time.Time       `json:"next_run_at"`
	LastStatus string          `json:"last_status"`
	LastError  string          `json:"last_error"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

func toTaskResponse(r gen.ScheduledTask) taskResponse {
	out := taskResponse{
		ID:         r.ID,
		Name:       r.Name,
		TaskType:   r.TaskType,
		Config:     json.RawMessage(r.Config),
		CronExpr:   r.CronExpr,
		Enabled:    r.Enabled,
		LastStatus: r.LastStatus,
		LastError:  r.LastError,
	}
	if r.LastRunAt.Valid {
		t := r.LastRunAt.Time
		out.LastRunAt = &t
	}
	if r.NextRunAt.Valid {
		out.NextRunAt = r.NextRunAt.Time
	}
	if r.CreatedAt.Valid {
		out.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		out.UpdatedAt = r.UpdatedAt.Time
	}
	if len(out.Config) == 0 {
		out.Config = json.RawMessage("{}")
	}
	return out
}

type runResponse struct {
	ID        uuid.UUID  `json:"id"`
	TaskID    uuid.UUID  `json:"task_id"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
	Status    string     `json:"status"`
	Output    string     `json:"output"`
	Error     string     `json:"error"`
}

func toRunResponse(r gen.TaskRun) runResponse {
	out := runResponse{
		ID:     r.ID,
		TaskID: r.TaskID,
		Status: r.Status,
		Output: r.Output,
		Error:  r.Error,
	}
	if r.StartedAt.Valid {
		out.StartedAt = r.StartedAt.Time
	}
	if r.EndedAt.Valid {
		t := r.EndedAt.Time
		out.EndedAt = &t
	}
	return out
}

// List handles GET /api/v1/admin/tasks.
func (h *TasksHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.ListScheduledTasks(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "tasks: list failed", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]taskResponse, len(rows))
	for i, row := range rows {
		out[i] = toTaskResponse(row)
	}
	respond.Success(w, r, out)
}

// ListTypes handles GET /api/v1/admin/tasks/types.
// Returns the task_type names registered with the scheduler so the admin
// UI can populate its dropdown.
func (h *TasksHandler) ListTypes(w http.ResponseWriter, _ *http.Request) {
	types := h.registry.Types()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": types})
}

type createTaskRequest struct {
	Name     string          `json:"name"`
	TaskType string          `json:"task_type"`
	Config   json.RawMessage `json:"config"`
	CronExpr string          `json:"cron_expr"`
	Enabled  *bool           `json:"enabled"`
}

// Create handles POST /api/v1/admin/tasks.
func (h *TasksHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.BadRequest(w, r, "invalid json")
		return
	}
	if req.Name == "" {
		respond.ValidationError(w, r, "name is required")
		return
	}
	if _, ok := h.registry.Get(req.TaskType); !ok {
		respond.ValidationError(w, r, "unknown task_type: "+req.TaskType)
		return
	}
	next, err := scheduler.NextRun(req.CronExpr, time.Now())
	if err != nil {
		respond.ValidationError(w, r, "invalid cron_expr: "+err.Error())
		return
	}
	if len(req.Config) == 0 {
		req.Config = json.RawMessage("{}")
	} else if !json.Valid(req.Config) {
		respond.ValidationError(w, r, "config must be valid json")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	row, err := h.q.CreateScheduledTask(r.Context(), gen.CreateScheduledTaskParams{
		Name:      req.Name,
		TaskType:  req.TaskType,
		Config:    []byte(req.Config),
		CronExpr:  req.CronExpr,
		Enabled:   enabled,
		NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "tasks: create failed", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Created(w, r, toTaskResponse(row))
}

type updateTaskRequest struct {
	Name     *string          `json:"name"`
	TaskType *string          `json:"task_type"`
	Config   *json.RawMessage `json:"config"`
	CronExpr *string          `json:"cron_expr"`
	Enabled  *bool            `json:"enabled"`
}

// Update handles PATCH /api/v1/admin/tasks/{id}. Accepts partial updates;
// any field omitted is preserved from the existing row.
func (h *TasksHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid id")
		return
	}
	var req updateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.BadRequest(w, r, "invalid json")
		return
	}

	existing, err := h.q.GetScheduledTask(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "tasks: get failed", "err", err)
		respond.InternalError(w, r)
		return
	}

	params := gen.UpdateScheduledTaskParams{
		ID:        existing.ID,
		Name:      existing.Name,
		TaskType:  existing.TaskType,
		Config:    existing.Config,
		CronExpr:  existing.CronExpr,
		Enabled:   existing.Enabled,
		NextRunAt: existing.NextRunAt,
	}
	if req.Name != nil {
		if *req.Name == "" {
			respond.ValidationError(w, r, "name cannot be empty")
			return
		}
		params.Name = *req.Name
	}
	if req.TaskType != nil {
		if _, ok := h.registry.Get(*req.TaskType); !ok {
			respond.ValidationError(w, r, "unknown task_type: "+*req.TaskType)
			return
		}
		params.TaskType = *req.TaskType
	}
	if req.Config != nil {
		if !json.Valid(*req.Config) {
			respond.ValidationError(w, r, "config must be valid json")
			return
		}
		params.Config = []byte(*req.Config)
	}
	if req.CronExpr != nil {
		next, err := scheduler.NextRun(*req.CronExpr, time.Now())
		if err != nil {
			respond.ValidationError(w, r, "invalid cron_expr: "+err.Error())
			return
		}
		params.CronExpr = *req.CronExpr
		params.NextRunAt = pgtype.Timestamptz{Time: next, Valid: true}
	}
	if req.Enabled != nil {
		params.Enabled = *req.Enabled
	}

	row, err := h.q.UpdateScheduledTask(r.Context(), params)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "tasks: update failed", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, toTaskResponse(row))
}

// Delete handles DELETE /api/v1/admin/tasks/{id}.
func (h *TasksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid id")
		return
	}
	if err := h.q.DeleteScheduledTask(r.Context(), id); err != nil {
		h.logger.ErrorContext(r.Context(), "tasks: delete failed", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// RunNow handles POST /api/v1/admin/tasks/{id}/run. Implementation: bump
// next_run_at to NOW so the next scheduler tick (<= 30 s) picks it up.
// Trades latency for simplicity — we avoid a second execution path with
// its own race/double-fire concerns.
func (h *TasksHandler) RunNow(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid id")
		return
	}
	if _, err := h.q.GetScheduledTask(r.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "tasks: get failed", "err", err)
		respond.InternalError(w, r)
		return
	}
	err = h.q.SetScheduledTaskNextRun(r.Context(), gen.SetScheduledTaskNextRunParams{
		ID:        id,
		NextRunAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "tasks: run-now failed", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, map[string]any{"queued": true})
}

// Runs handles GET /api/v1/admin/tasks/{id}/runs. Returns the most recent
// N runs (default 50, max 500) for the task.
func (h *TasksHandler) Runs(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid id")
		return
	}
	limit := int32(50)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > 500 {
				n = 500
			}
			limit = int32(n)
		}
	}
	rows, err := h.q.ListTaskRuns(r.Context(), gen.ListTaskRunsParams{TaskID: id, Limit: limit})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "tasks: list runs failed", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]runResponse, len(rows))
	for i, row := range rows {
		out[i] = toRunResponse(row)
	}
	respond.Success(w, r, out)
}
