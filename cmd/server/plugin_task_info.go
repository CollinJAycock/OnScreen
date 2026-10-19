package main

import (
	"context"

	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/plugin"
)

// pluginTaskInfo supplies the context a task plugin's run_task call gets:
// the server version and each library's id, name and type. Library paths
// stay out — a plugin has no business knowing the server's filesystem.
type pluginTaskInfo struct {
	libs interface {
		List(context.Context) ([]library.Library, error)
	}
	version string
}

func (p pluginTaskInfo) ServerInfo() plugin.TaskServerInfo {
	return plugin.TaskServerInfo{Name: "OnScreen", Version: p.version}
}

func (p pluginTaskInfo) Libraries(ctx context.Context) ([]plugin.TaskLibrary, error) {
	libs, err := p.libs.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]plugin.TaskLibrary, 0, len(libs))
	for _, l := range libs {
		out = append(out, plugin.TaskLibrary{ID: l.ID.String(), Name: l.Name, Type: l.Type})
	}
	return out, nil
}
