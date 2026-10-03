// Command demo-mcp-server is a small, real MCP tool server Warden proxies
// to in local dev — standing in for a real third-party MCP server, the
// same role the mock model provider plays for milestone 2. Its tool
// description is configurable via env var specifically so it can simulate
// a silent tool-poisoning change on demand, for testing the registry's
// detection without needing an actual compromised server.
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoArgs struct {
	Text string `json:"text" jsonschema:"text to echo back"`
}

func main() {
	description := os.Getenv("ECHO_TOOL_DESCRIPTION")
	if description == "" {
		description = "Echoes back whatever text you send it."
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "demo-tools", Version: "v1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: description},
		func(ctx context.Context, req *mcp.CallToolRequest, args echoArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + args.Text}},
			}, nil, nil
		},
	)

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	addr := os.Getenv("DEMO_MCP_ADDR")
	if addr == "" {
		addr = ":9090"
	}
	log.Printf("demo-mcp-server listening on %s (tool description: %q)", addr, description)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}
