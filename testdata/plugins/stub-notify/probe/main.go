// Probe is a minimal MCP client that exercises stub-notify end-to-end.
// Used for smoke testing outside unit tests.
//
//	./bin/stub-notify.exe --addr :18091 &
//	go run ./testdata/plugins/stub-notify/probe
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func main() {
	endpoint := "http://127.0.0.1:18091/mcp"
	if len(os.Args) > 1 {
		endpoint = os.Args[1]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	c, err := mcpclient.NewStreamableHttpClient(endpoint)
	if err != nil {
		die("new client", err)
	}
	defer c.Close()

	if err := c.Start(ctx); err != nil {
		die("start", err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ClientInfo = mcp.Implementation{Name: "probe", Version: "1"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		die("initialize", err)
	}

	listed, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		die("list tools", err)
	}
	fmt.Printf("advertised tools: %d\n", len(listed.Tools))
	found := false
	for _, t := range listed.Tools {
		fmt.Printf("  - %s\n", t.Name)
		if t.Name == "notify" {
			found = true
		}
	}
	if !found {
		die("contract", fmt.Errorf("plugin does not advertise 'notify' tool"))
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = "notify"
	req.Params.Arguments = map[string]any{
		"correlation_id": uuid.NewString(),
		"event":          "media.play",
		"user_id":        uuid.NewString(),
		"media_id":       uuid.NewString(),
		"title":          "probe title",
		"body":           "probe body",
	}
	res, err := c.CallTool(ctx, req)
	if err != nil {
		die("call notify", err)
	}
	if res.IsError {
		die("notify result", fmt.Errorf("plugin returned IsError=true"))
	}
	fmt.Println("OK — notify delivered")
}

func die(label string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", label, err)
	os.Exit(1)
}
