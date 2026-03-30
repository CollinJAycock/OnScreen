//go:build windows

package config

import "log/slog"

// WatchSIGHUP is a no-op on Windows (SIGHUP does not exist).
func WatchSIGHUP(logger *slog.Logger, h *HotReloadable, current *Config, lv *slog.LevelVar) {}
