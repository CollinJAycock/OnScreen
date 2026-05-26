// Package observability — safego helper for panic-resilient goroutines.
//
// The chi `Recover` middleware ([internal/api/middleware/recover.go])
// only catches panics inside the HTTP handler's goroutine. Any goroutine
// spawned *from* a handler — or any long-running background goroutine
// (transcode workers, plugin dispatchers, leader-elected singletons,
// detached enrichment runs) — runs under no such net. A panic in one
// of those tears down the entire server process.
//
// `SafeGo` is the canonical "fire-and-forget" goroutine wrapper:
//
//	observability.SafeGo(logger, "transcode-worker:run-job", func() {
//	    worker.runJob(ctx, j)
//	})
//
// The `name` is included in the log line so an operator scanning logs
// can identify which goroutine site panicked without parsing a stack
// trace. Stack is captured regardless so the trace is available too.

package observability

import (
	"log/slog"
	"runtime/debug"
)

// SafeGo runs fn in a new goroutine with a deferred recover. A panic
// inside fn is logged with the supplied name + the panic value + a
// stack trace; the process is NOT torn down. Returns immediately
// (does not wait for fn).
//
// Use this anywhere you'd otherwise write `go func() { … }()` outside
// the protection of the HTTP `Recover` middleware. For HTTP handler
// bodies themselves the middleware already covers you; SafeGo is for
// goroutines spawned BY handlers and by long-running services.
func SafeGo(logger *slog.Logger, name string, fn func()) {
	go func() {
		defer func() {
			if v := recover(); v != nil {
				if logger != nil {
					logger.Error("goroutine panic recovered",
						"goroutine", name,
						"panic", v,
						"stack", string(debug.Stack()),
					)
				}
			}
		}()
		fn()
	}()
}
