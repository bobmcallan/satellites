package mcpserver

import (
	"encoding/json"
	"fmt"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// getString reads a string arg without panicking on absence / wrong type.
func getString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

// jsonResult wraps an arbitrary payload as a structured MCP tool result.
func jsonResult(payload any) (*mcpgo.CallToolResult, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("jsonResult: marshal: %w", err)
	}
	return mcpgo.NewToolResultText(string(b)), nil
}
