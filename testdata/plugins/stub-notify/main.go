// stub-notify is a minimal reference notification plugin for OnScreen.
//
// It starts a Streamable HTTP MCP server on :8091 and exposes a single
// `notify` tool that logs each received event to stderr. Use this as a
// starting point for real plugins and as a local smoke target.
//
// Build and run:
//
//	go run ./testdata/plugins/stub-notify
//
// Then in OnScreen's admin UI, register a notification plugin with:
//
//	endpoint_url: http://<host-reachable-from-onscreen>:8091/mcp
//
// (A host reachable from OnScreen — localhost won't work because the
// egress guard blocks loopback. Use a LAN IP or a tunnel like ngrok.)
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func main() {
	addr := flag.String("addr", ":8091", "listen address")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	srv := mcpserver.NewMCPServer(
		"onscreen-stub-notify",
		"1.0.0",
		mcpserver.WithToolCapabilities(false),
	)

	notify := mcp.NewTool("notify",
		mcp.WithDescription("Receives OnScreen notification events"),
		mcp.WithString("correlation_id"),
		mcp.WithString("event", mcp.Required()),
		mcp.WithString("user_id"),
		mcp.WithString("media_id"),
		mcp.WithString("title"),
		mcp.WithString("body"),
	)
	srv.AddTool(notify, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Log the full payload so operators can see what OnScreen delivers.
		// Real plugins would forward to Slack, Discord, a database, etc.
		raw, _ := json.MarshalIndent(req.Params.Arguments, "", "  ")
		log.Info("notify received", "args", string(raw))
		return mcp.NewToolResultText("ok"), nil
	})

	httpSrv := mcpserver.NewStreamableHTTPServer(srv, mcpserver.WithStateLess(true))
	log.Info("stub-notify listening", "addr", *addr, "path", "/mcp")
	if err := http.ListenAndServe(*addr, httpSrv); err != nil {
		log.Error("server failed", "err", err)
		os.Exit(1)
	}
}
