package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/robfig/cron/v3"

	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// RunTaskToolName is the tool a task plugin must advertise. The scheduler
// calls it each time the plugin's scheduled task fires (or on "Run now").
const RunTaskToolName = "run_task"

// TaskTypePlugin is the scheduled_tasks.task_type of plugin tasks. Each task
// plugin has one row of this type with config {"plugin_id": "<uuid>"}
// (migration 00058); the scheduler handler registered under this name looks
// the plugin up from that config.
const TaskTypePlugin = "plugin_task"

// DefaultTaskCron is the schedule a new task plugin's row starts with: daily
// at 04:30 server time. Admins change it in Settings → Tasks like any task.
const DefaultTaskCron = "30 4 * * *"

// taskCallTimeout is the deadline for one run_task call. Longer than the
// per-call default because a task does real work, but capped by the plugin
// HTTP client's hard ceiling (clientHardTimeout) — a plugin with more to do
// should accept the run, return, and carry on asynchronously.
const taskCallTimeout = clientHardTimeout

// maxTaskOutput caps how much of a run_task result lands in task_runs.output
// and scheduled_tasks.last_error.
const maxTaskOutput = 4096

// Audit actions for the task role. Each run is already recorded in task_runs,
// so only breaker transitions are audited.
const (
	ActionPluginTaskBreakerOpen  = "plugin.task.breaker_open"
	ActionPluginTaskBreakerClose = "plugin.task.breaker_close"
)

// TaskContext is the argument object of the run_task tool: enough for a
// plugin to orient itself, nothing secret (no paths, keys, users or URLs).
type TaskContext struct {
	CorrelationID string         `json:"correlation_id"`
	PluginID      string         `json:"plugin_id"`
	StartedAt     string         `json:"started_at"` // RFC 3339, UTC
	Server        TaskServerInfo `json:"server"`
	Libraries     []TaskLibrary  `json:"libraries"`
}

// TaskServerInfo identifies the calling server.
type TaskServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// TaskLibrary is one library in the run_task context: identity and type
// only — no filesystem paths.
type TaskLibrary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// TaskInfoSource supplies the server and library parts of TaskContext.
// Wired in cmd/server from the build version and the library service.
type TaskInfoSource interface {
	ServerInfo() TaskServerInfo
	Libraries(ctx context.Context) ([]TaskLibrary, error)
}

// TaskDispatcher runs task plugins for the scheduler's plugin_task handler.
type TaskDispatcher struct {
	registry *Registry
	info     TaskInfoSource
	logger   *slog.Logger
	pool     *clientPool
	timeout  time.Duration
}

// NewTaskDispatcher builds a dispatcher. info and auditLog may be nil.
func NewTaskDispatcher(reg *Registry, info TaskInfoSource, logger *slog.Logger, auditLog *audit.Logger) *TaskDispatcher {
	return &TaskDispatcher{
		registry: reg,
		info:     info,
		logger:   logger,
		pool: newClientPool(RoleTask, logger, auditLog,
			ActionPluginTaskBreakerOpen, ActionPluginTaskBreakerClose),
		timeout: taskCallTimeout,
	}
}

// RunTask calls run_task on one plugin and returns the text it sent back as
// the run's output. A disabled plugin is skipped with a note (the run still
// succeeds, so the Tasks page doesn't fill with failures while a plugin is
// switched off); an unknown plugin, a plugin of another role, an open
// breaker, a transport failure or an isError result fail the run, with the
// plugin's error text as the error.
func (d *TaskDispatcher) RunTask(ctx context.Context, pluginID uuid.UUID) (string, error) {
	p, err := d.registry.Get(ctx, pluginID)
	if err != nil {
		return "", fmt.Errorf("plugin %s: %w", pluginID, err)
	}
	if p.Role != RoleTask {
		return "", fmt.Errorf("plugin %q has role %q, not task", p.Name, p.Role)
	}
	if !p.Enabled {
		return fmt.Sprintf("skipped: plugin %q is disabled", p.Name), nil
	}
	e, err := d.pool.entryFor(p)
	if err != nil {
		return "", fmt.Errorf("plugin %q: %w", p.Name, err)
	}

	args := TaskContext{
		CorrelationID: uuid.NewString(),
		PluginID:      p.ID.String(),
		StartedAt:     time.Now().UTC().Format(time.RFC3339),
		Libraries:     []TaskLibrary{},
	}
	if d.info != nil {
		args.Server = d.info.ServerInfo()
		libs, err := d.info.Libraries(ctx)
		if err != nil {
			d.logger.WarnContext(ctx, "task plugin: list libraries", "err", err)
		} else if libs != nil {
			args.Libraries = libs
		}
	}

	res, err := d.pool.call(ctx, e, RunTaskToolName, args, d.timeout)
	text := truncate(strings.TrimSpace(resultText(res)), maxTaskOutput)
	if err != nil {
		var toolErr *PluginToolError
		if errors.As(err, &toolErr) {
			if text == "" {
				text = "plugin reported a failure without a message"
			}
			return "", fmt.Errorf("plugin %q: %s", p.Name, text)
		}
		return "", fmt.Errorf("plugin %q: %w", p.Name, err)
	}
	return text, nil
}

// Close shuts the cached plugin connections down.
func (d *TaskDispatcher) Close() {
	if d == nil {
		return
	}
	d.pool.close()
}

// pluginTaskParams builds the scheduled_tasks row for a task plugin: named
// after it, on DefaultTaskCron, enabled. Runs while the plugin itself is
// disabled are skipped by RunTask.
func pluginTaskParams(p Plugin, now time.Time) (gen.EnsurePluginTaskParams, error) {
	sched, err := cron.ParseStandard(DefaultTaskCron)
	if err != nil {
		return gen.EnsurePluginTaskParams{}, err
	}
	return gen.EnsurePluginTaskParams{
		Name:      "Plugin: " + p.Name,
		PluginID:  p.ID,
		CronExpr:  DefaultTaskCron,
		Enabled:   true,
		NextRunAt: pgtype.Timestamptz{Time: sched.Next(now), Valid: true},
	}, nil
}
