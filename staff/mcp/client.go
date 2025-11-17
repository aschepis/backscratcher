package mcp

import (
	"context"
)

// ToolDefinition represents an MCP tool definition.
type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// ConfigSchema represents the MCP server's configuration schema.
type ConfigSchema struct {
	Schema map[string]interface{} `json:"schema"`
}

// MCPClient is the interface for interacting with MCP servers.
type MCPClient interface {
	// Start initializes and starts the MCP client connection.
	Start(ctx context.Context) error

	// ListTools returns all tools available from the MCP server.
	ListTools(ctx context.Context) ([]ToolDefinition, error)

	// InvokeTool invokes a tool on the MCP server with the given input.
	InvokeTool(ctx context.Context, name string, input map[string]interface{}) (map[string]interface{}, error)

	// GetConfigSchema returns the configuration schema for the MCP server.
	GetConfigSchema(ctx context.Context) (*ConfigSchema, error)

	// Close closes the connection to the MCP server.
	Close() error
}
