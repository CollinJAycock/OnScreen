package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/onscreen/onscreen/internal/audit"
)

// clientPool holds one MCP client and one circuit breaker per plugin for the
// roles OnScreen calls synchronously (metadata during enrichment, task from
// the scheduler). The notification role keeps its own queued workers.
//
// Like NotificationDispatcher.workerFor, an entry whose endpoint URL or
// allowlist changed since it was built is replaced on the next call, so admin
// edits take effect without a restart. The replacement starts with a closed
// breaker: a new endpoint gets a fresh chance.
type clientPool struct {
	role   Role
	logger *slog.Logger
	audit  *audit.Logger

	// breakerOpenAction / breakerCloseAction are the audit actions written
	// when a plugin's breaker trips and recovers.
	breakerOpenAction  string
	breakerCloseAction string

	mu      sync.Mutex
	entries map[uuid.UUID]*poolEntry
	closed  bool
}

type poolEntry struct {
	plugin  Plugin
	client  *pluginClient
	breaker *breaker
	allowed allowedHostSet
	// noToolWarned keeps a mis-registered plugin (no tool for its role)
	// to one warning per client instead of one per item in a scan.
	noToolWarned atomic.Bool
}

func newClientPool(role Role, logger *slog.Logger, auditLog *audit.Logger, openAction, closeAction string) *clientPool {
	return &clientPool{
		role:               role,
		logger:             logger,
		audit:              auditLog,
		breakerOpenAction:  openAction,
		breakerCloseAction: closeAction,
		entries:            map[uuid.UUID]*poolEntry{},
	}
}

// entryFor returns the cached entry for p, building or replacing it as needed.
func (cp *clientPool) entryFor(p Plugin) (*poolEntry, error) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if cp.closed {
		return nil, errDispatcherClosed
	}
	if e, ok := cp.entries[p.ID]; ok {
		if e.plugin.EndpointURL == p.EndpointURL && equalStringSets(e.plugin.AllowedHosts, p.AllowedHosts) {
			return e, nil
		}
		// Close off the lock's critical path: pluginClient.close waits for
		// an in-flight call (see pluginWorker.stop for why that matters).
		old := e.client
		go old.close()
		delete(cp.entries, p.ID)
	}
	allowed, err := newAllowedHostSet(p.EndpointURL, p.AllowedHosts)
	if err != nil {
		return nil, err
	}
	pc, err := newPluginClient(p)
	if err != nil {
		return nil, err
	}
	e := &poolEntry{plugin: p, client: pc, breaker: newBreaker(), allowed: allowed}
	cp.entries[p.ID] = e
	return e, nil
}

// close shuts every cached client. Later calls fail with errDispatcherClosed.
func (cp *clientPool) close() {
	cp.mu.Lock()
	if cp.closed {
		cp.mu.Unlock()
		return
	}
	cp.closed = true
	entries := cp.entries
	cp.entries = nil
	cp.mu.Unlock()
	for _, e := range entries {
		e.client.close()
	}
}

// BreakerOpenError is returned when a call is skipped because the plugin's
// circuit breaker is open.
type BreakerOpenError struct {
	Plugin string
	Until  time.Time
}

func (e *BreakerOpenError) Error() string {
	return fmt.Sprintf("plugin %q circuit breaker open until %s", e.Plugin, e.Until.UTC().Format(time.RFC3339))
}

// call runs one tool call through the entry's breaker:
//   - breaker open → *BreakerOpenError, no call made;
//   - tool not advertised → *ToolNotAdvertisedError, breaker untouched (the
//     plugin is reachable, just mis-registered for this role);
//   - transport failure, timeout or isError result → counted as a failure;
//     the third in a row opens the breaker for breakerCooldown;
//   - success → closes the breaker.
//
// A panic below (a malformed result the decoder didn't expect) is recovered
// and reported as an error, so it counts against the breaker instead of
// killing the scan or the scheduler goroutine.
func (cp *clientPool) call(ctx context.Context, e *poolEntry, tool string, args any, timeout time.Duration) (res *mcp.CallToolResult, err error) {
	if ok, until := e.breaker.allow(); !ok {
		return nil, &BreakerOpenError{Plugin: e.plugin.Name, Until: until}
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	func() {
		defer func() {
			if r := recover(); r != nil {
				res, err = nil, fmt.Errorf("plugin call panicked: %v", r)
			}
		}()
		res, err = e.client.callTool(callCtx, tool, args)
	}()

	if err == nil {
		if e.breaker.success() && cp.audit != nil {
			cp.audit.Log(ctx, nil, cp.breakerCloseAction, e.plugin.Name, nil, "")
		}
		return res, nil
	}
	var notAdvertised *ToolNotAdvertisedError
	if errors.As(err, &notAdvertised) {
		return res, err
	}
	// The caller's own context ending (scan cancelled, server shutting
	// down) says nothing about the plugin's health.
	if ctx.Err() != nil {
		return res, err
	}
	opened, until, fails := e.breaker.failure()
	cp.logger.Warn("plugin call failed",
		"plugin", e.plugin.Name, "role", string(cp.role), "tool", tool,
		"consec_fails", fails, "err", err)
	if opened {
		cp.logger.Warn("plugin breaker tripped",
			"plugin", e.plugin.Name, "role", string(cp.role),
			"cooldown", breakerCooldown, "open_until", until)
		if cp.audit != nil {
			cp.audit.Log(ctx, nil, cp.breakerOpenAction, e.plugin.Name,
				map[string]any{"cooldown": breakerCooldown.String()}, "")
		}
	}
	return res, err
}

// resultText joins a tool result's text content blocks. Non-text blocks
// (images, resources) are ignored.
func resultText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var parts []string
	for _, c := range res.Content {
		if tc, ok := mcp.AsTextContent(c); ok && tc.Text != "" {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// truncate caps s at n bytes on a rune boundary, marking the cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// ProbeTool connects to a plugin, runs the MCP handshake and checks that it
// advertises tool, without calling it. The admin "Test" button uses it for
// the metadata and task roles, where a real call would enrich an item or run
// the task. Bypasses pools and breakers, like TestDispatch.
func ProbeTool(ctx context.Context, p Plugin, tool string) error {
	client, err := newPluginClient(p)
	if err != nil {
		return fmt.Errorf("build client: %w", err)
	}
	defer client.close()

	probeCtx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()
	client.mu.Lock()
	defer client.mu.Unlock()
	if err := client.connect(probeCtx); err != nil {
		return err
	}
	if _, ok := client.tools[tool]; !ok {
		return &ToolNotAdvertisedError{Tool: tool}
	}
	return nil
}
