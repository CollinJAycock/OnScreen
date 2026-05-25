//go:build !dev

package api

import (
	"log/slog"
	"net/http"
)

// newDevFrontendProxy is a no-op outside dev builds: production always serves
// the embedded SPA. The dev-build proxy lives in frontend_dev.go.
func newDevFrontendProxy(string, *slog.Logger) http.Handler { return nil }
