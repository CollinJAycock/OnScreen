package v1

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/observability"
)

// LogsHandler exposes recent server log records to admins. The records come
// from a process-local ring buffer attached to the slog handler chain at
// boot — no file tail, no shell. Useful when the server runs in a container
// the operator can't easily exec into (TrueNAS Apps, Cloud Run).
type LogsHandler struct {
	buf *observability.LogRingBuffer
}

// NewLogsHandler returns a handler reading from buf. buf may be nil for
// tests that want a 503 stub.
func NewLogsHandler(buf *observability.LogRingBuffer) *LogsHandler {
	return &LogsHandler{buf: buf}
}

// List handles GET /api/v1/admin/logs?level=...&limit=...
//
// Query params:
//   - level: minimum slog level — debug / info / warn / error (default: info)
//   - limit: max number of entries to return, newest-last (default: 200, max: 2000)
//
// Authentication: route is mounted under the AdminRequired middleware in
// router.go, so this handler doesn't re-check.
func (h *LogsHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.buf == nil {
		respond.Error(w, r, http.StatusServiceUnavailable, "LOG_BUFFER_UNAVAILABLE", "log buffer not configured")
		return
	}

	minLevel := slog.LevelInfo
	switch r.URL.Query().Get("level") {
	case "debug":
		minLevel = slog.LevelDebug
	case "warn", "warning":
		minLevel = slog.LevelWarn
	case "error":
		minLevel = slog.LevelError
	}

	limit := 200
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			limit = n
			if limit > 2000 {
				limit = 2000
			}
		}
	}

	entries := h.buf.Snapshot(minLevel)
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	respond.Success(w, r, entries)
}
