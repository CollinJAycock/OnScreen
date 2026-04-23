//go:build !windows

package config

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// WatchSIGHUP starts a goroutine that reloads config on every SIGHUP.
func WatchSIGHUP(logger *slog.Logger, h *HotReloadable, current *Config) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	go func() {
		for range ch {
			logger.Info("received SIGHUP, reloading config")
			h.Reload(logger, current)
		}
	}()
}
