package scheduler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// PluginTaskRunner runs one task-role plugin. Satisfied by
// *plugin.TaskDispatcher.
type PluginTaskRunner interface {
	RunTask(ctx context.Context, pluginID uuid.UUID) (string, error)
}

// pluginTaskConfig is the config of a plugin_task row (migration 00058).
type pluginTaskConfig struct {
	PluginID string `json:"plugin_id"`
}

// NewPluginTaskHandler returns the plugin_task handler: it calls the
// run_task tool of the plugin named in the row's config and records what the
// plugin sent back as the run's output, or its error as last_error. The rows
// are created and removed with the plugins themselves (one per task plugin),
// so admins only set their schedule and enabled flag in the Tasks UI.
func NewPluginTaskHandler(r PluginTaskRunner) HandlerFunc {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var cfg pluginTaskConfig
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return "", fmt.Errorf("plugin_task config: %w", err)
			}
		}
		id, err := uuid.Parse(cfg.PluginID)
		if err != nil {
			return "", fmt.Errorf("plugin_task config: plugin_id %q is not a UUID", cfg.PluginID)
		}
		return r.RunTask(ctx, id)
	}
}
