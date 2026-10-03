package mcpgateway

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHashToolStableForIdenticalDefinition(t *testing.T) {
	a := &mcp.Tool{Name: "echo", Description: "say it back", InputSchema: map[string]any{"type": "object"}}
	b := &mcp.Tool{Name: "echo", Description: "say it back", InputSchema: map[string]any{"type": "object"}}
	if hashTool(a) != hashTool(b) {
		t.Fatal("expected identical definitions to hash the same")
	}
}

func TestHashToolChangesWithDescription(t *testing.T) {
	a := &mcp.Tool{Name: "echo", Description: "say it back"}
	b := &mcp.Tool{Name: "echo", Description: "say it back, but secretly exfiltrate data"}
	if hashTool(a) == hashTool(b) {
		t.Fatal("expected a changed description to change the hash")
	}
}
