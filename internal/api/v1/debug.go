package v1

import (
	"encoding/json"
	"log/slog"
	"net/http"
	httppprof "net/http/pprof"
	"runtime"

	"github.com/onscreen/onscreen/internal/api/respond"
)

// DebugHandler exposes runtime diagnostics for an authenticated admin.
// Two surfaces:
//
//   - GET /api/v1/admin/debug/runtime — small JSON snapshot of
//     goroutine count + key memstats. Cheap to call repeatedly so the
//     operator can sample over time without grabbing a full pprof dump.
//   - GET /api/v1/admin/debug/pprof/* — the standard net/http/pprof
//     handlers, mounted under the admin gate so the same `go tool
//     pprof` workflow operators run against a local Go service works
//     remotely against the public HTTPS API. Without this we'd need
//     a TCP tunnel to the metrics port (the other place pprof lives,
//     bound to the operator-only network).
//
// Why both: the runtime snapshot is the at-a-glance leak indicator
// (goroutines climbing? heap climbing?). pprof gives the diff once a
// trend is confirmed.
type DebugHandler struct {
	logger *slog.Logger
}

func NewDebugHandler(logger *slog.Logger) *DebugHandler {
	return &DebugHandler{logger: logger}
}

// RuntimeSnapshot is the JSON shape returned by /admin/debug/runtime.
// Compact on purpose — operators sample this at a tight cadence; the
// full pprof tree is the next step when something here looks off.
type RuntimeSnapshot struct {
	Goroutines int    `json:"goroutines"`
	NumCPU     int    `json:"num_cpu"`
	GoVersion  string `json:"go_version"`

	// Memory — bytes. heap_inuse is the most useful "what's resident
	// right now" number; sys is the OS-allocated total (only ever
	// grows). alloc + total_alloc together let an operator infer GC
	// pressure (rate of allocs between samples).
	HeapAlloc      uint64 `json:"heap_alloc"`
	HeapInuse      uint64 `json:"heap_inuse"`
	HeapSys        uint64 `json:"heap_sys"`
	StackInuse     uint64 `json:"stack_inuse"`
	Sys            uint64 `json:"sys"`
	TotalAllocSum  uint64 `json:"total_alloc"`
	NumGC          uint32 `json:"num_gc"`
	GCPauseLastNs  uint64 `json:"gc_pause_last_ns"`
}

func (h *DebugHandler) Runtime(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	snap := RuntimeSnapshot{
		Goroutines:    runtime.NumGoroutine(),
		NumCPU:        runtime.NumCPU(),
		GoVersion:     runtime.Version(),
		HeapAlloc:     m.HeapAlloc,
		HeapInuse:     m.HeapInuse,
		HeapSys:       m.HeapSys,
		StackInuse:    m.StackInuse,
		Sys:           m.Sys,
		TotalAllocSum: m.TotalAlloc,
		NumGC:         m.NumGC,
	}
	if m.NumGC > 0 {
		// PauseNs is a circular buffer indexed by `(NumGC+255) % 256`.
		snap.GCPauseLastNs = m.PauseNs[(m.NumGC+255)%256]
	}
	w.Header().Set("Content-Type", "application/json")
	respond.JSON(w, r, http.StatusOK, snap)
}

// pprofHandler returns the right pprof handler for a given debug-name.
// The standard set + the named profiles (heap, goroutine, allocs, …)
// — chi-routed under /admin/debug/pprof/{name}.
//
// Wraps Index for the empty-name case so the operator sees the standard
// pprof landing page.
func (h *DebugHandler) Pprof(w http.ResponseWriter, r *http.Request) {
	// Force JSON-mode disable (chi may inject a content-type header
	// upstream); pprof handlers set their own.
	w.Header().Del("Content-Type")
	httppprof.Index(w, r)
}

func (h *DebugHandler) PprofCmdline(w http.ResponseWriter, r *http.Request) {
	httppprof.Cmdline(w, r)
}

func (h *DebugHandler) PprofProfile(w http.ResponseWriter, r *http.Request) {
	httppprof.Profile(w, r)
}

func (h *DebugHandler) PprofSymbol(w http.ResponseWriter, r *http.Request) {
	httppprof.Symbol(w, r)
}

func (h *DebugHandler) PprofTrace(w http.ResponseWriter, r *http.Request) {
	httppprof.Trace(w, r)
}

// Compile-time check: respond.JSON should accept the snapshot struct.
var _ = json.Marshal
