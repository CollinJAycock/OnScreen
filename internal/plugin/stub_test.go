package plugin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// toolStub is a stateless Streamable-HTTP MCP server exposing one tool whose
// behaviour the test supplies. Shared by the metadata and task tests.
type toolStub struct {
	server *httptest.Server
	calls  atomic.Int64
	// lastArgs holds the raw arguments of the most recent call.
	lastArgs atomic.Value // []byte
}

func newToolStub(t *testing.T, tool string, handle func(args map[string]any) (*mcp.CallToolResult, error)) *toolStub {
	t.Helper()
	s := &toolStub{}
	mcpSrv := mcpserver.NewMCPServer("stub", "1.0.0", mcpserver.WithToolCapabilities(false))
	if tool != "" {
		mcpSrv.AddTool(mcp.NewTool(tool, mcp.WithDescription("test tool")),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				s.calls.Add(1)
				args := req.GetArguments()
				b, _ := json.Marshal(args)
				s.lastArgs.Store(b)
				return handle(args)
			})
	}
	s.server = httptest.NewServer(mcpserver.NewStreamableHTTPServer(mcpSrv, mcpserver.WithStateLess(true)))
	t.Cleanup(s.server.Close)
	return s
}

func (s *toolStub) endpoint() string { return s.server.URL + "/mcp" }

func (s *toolStub) host() string {
	u, _ := url.Parse(s.server.URL)
	return u.Hostname()
}

func (s *toolStub) args(t *testing.T, into any) {
	t.Helper()
	b, _ := s.lastArgs.Load().([]byte)
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatalf("decode stub args: %v (%s)", err, b)
	}
}
