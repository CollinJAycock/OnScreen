package plugin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// defaultCallTimeout is the per-tool-call deadline applied if the caller
// hasn't already set a tighter one on the context. Notification dispatch
// is fire-and-forget — a plugin that takes longer than this is broken.
const defaultCallTimeout = 10 * time.Second

// pluginClient owns the mark3labs/mcp-go Streamable HTTP client for a single
// plugin and caches the negotiated capabilities + tool list across calls.
//
// Lifecycle is lazy: the first CallTool initialises the underlying transport.
// On any retryable transport error subsequent calls re-initialise transparently.
type pluginClient struct {
	plugin     Plugin
	httpClient *http.Client

	mu     sync.Mutex
	mc     *mcpclient.Client
	tools  map[string]struct{}
	ready  bool
	closed bool
}

// newPluginClient prepares a client struct without connecting. The actual
// MCP handshake happens on first call so a misconfigured plugin doesn't
// block startup.
func newPluginClient(p Plugin) (*pluginClient, error) {
	hc, err := httpClientForPlugin(p)
	if err != nil {
		return nil, err
	}
	return &pluginClient{plugin: p, httpClient: hc, tools: map[string]struct{}{}}, nil
}

// connect opens the transport and runs Initialize + ListTools, populating the
// capability cache. Caller must hold c.mu.
func (c *pluginClient) connect(ctx context.Context) error {
	if c.closed {
		return errors.New("plugin client closed")
	}
	mc, err := mcpclient.NewStreamableHttpClient(
		c.plugin.EndpointURL,
		mcptransport.WithHTTPBasicClient(c.httpClient),
	)
	if err != nil {
		return fmt.Errorf("new client: %w", err)
	}
	if err := mc.Start(ctx); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ClientInfo = mcp.Implementation{Name: "onscreen", Version: "1"}
	if _, err := mc.Initialize(ctx, initReq); err != nil {
		_ = mc.Close()
		return fmt.Errorf("initialize: %w", err)
	}
	listed, err := mc.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		_ = mc.Close()
		return fmt.Errorf("list tools: %w", err)
	}
	tools := make(map[string]struct{}, len(listed.Tools))
	for _, t := range listed.Tools {
		tools[t.Name] = struct{}{}
	}
	c.mc = mc
	c.tools = tools
	c.ready = true
	return nil
}

// callTool invokes a named tool, lazily connecting and reconnecting once on
// transport-level errors. Plugin-side errors (IsError=true) are returned as
// PluginToolError so the dispatcher can audit them without retrying.
func (c *pluginClient) callTool(ctx context.Context, name string, args any) (*mcp.CallToolResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultCallTimeout)
		defer cancel()
	}

	if !c.ready {
		if err := c.connect(ctx); err != nil {
			return nil, err
		}
	}
	if _, ok := c.tools[name]; !ok {
		return nil, &ToolNotAdvertisedError{Tool: name}
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	res, err := c.mc.CallTool(ctx, req)
	if err == nil {
		if res.IsError {
			return res, &PluginToolError{Result: res}
		}
		return res, nil
	}

	// One transparent reconnect attempt — covers idle-connection drops and
	// short transport blips. Repeated failures fall through to the caller.
	if shouldReconnect(err) {
		c.tearDownLocked()
		if cerr := c.connect(ctx); cerr != nil {
			return nil, fmt.Errorf("reconnect: %w (after %v)", cerr, err)
		}
		res, err = c.mc.CallTool(ctx, req)
		if err == nil {
			if res.IsError {
				return res, &PluginToolError{Result: res}
			}
			return res, nil
		}
	}
	return nil, err
}

// tearDownLocked closes the underlying transport and resets the cache so the
// next call reconnects from scratch. Caller must hold c.mu.
func (c *pluginClient) tearDownLocked() {
	if c.mc != nil {
		_ = c.mc.Close()
	}
	c.mc = nil
	c.tools = map[string]struct{}{}
	c.ready = false
}

// close shuts the underlying transport down for good. Subsequent calls return
// an error.
func (c *pluginClient) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tearDownLocked()
	c.closed = true
}

// hasTool reports whether the cached capability set advertises name. Returns
// false if the client hasn't connected yet.
func (c *pluginClient) hasTool(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ready {
		return false
	}
	_, ok := c.tools[name]
	return ok
}

// shouldReconnect classifies which transport errors warrant a single reconnect
// attempt. We err toward retrying — the cost of a wasted reconnect is small;
// the cost of a swallowed transient is a missed notification.
func shouldReconnect(err error) bool {
	if err == nil {
		return false
	}
	// mark3labs/mcp-go does not export sentinel errors for the SSE/Streamable
	// transports as of v0.49, so we fall back to broad treatment of any
	// non-context error as retryable. context.Canceled / DeadlineExceeded
	// are explicitly not retried — they came from the caller.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// ToolNotAdvertisedError is returned when the plugin's capability set doesn't
// include the requested tool. Indicates the plugin is incompatible with the
// role OnScreen registered it under (or the plugin was misconfigured server-side).
type ToolNotAdvertisedError struct{ Tool string }

func (e *ToolNotAdvertisedError) Error() string {
	return fmt.Sprintf("plugin does not advertise tool %q", e.Tool)
}

// PluginToolError wraps a tool result whose IsError flag is set. The Result
// is preserved so the dispatcher can extract any plugin-supplied detail for
// the audit log.
type PluginToolError struct{ Result *mcp.CallToolResult }

func (e *PluginToolError) Error() string {
	return "plugin tool returned error result"
}
