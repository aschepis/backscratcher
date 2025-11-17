package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/aschepis/backscratcher/staff/logger"
)

// StdioMCPClient implements MCPClient for STDIO transport.
type StdioMCPClient struct {
	client     *client.Client
	command    string
	args       []string
	env        []string
	configFile string
}

// NewStdioMCPClient creates a new STDIO MCP client.
func NewStdioMCPClient(command, configFile string, args, env []string) (*StdioMCPClient, error) {
	if command == "" {
		return nil, fmt.Errorf("command is required for STDIO MCP client")
	}

	// Split command into command and args if it contains spaces
	parts := strings.Fields(command)
	cmd := parts[0]
	var cmdArgs []string
	if len(parts) > 1 {
		cmdArgs = make([]string, 0, len(parts)-1+len(args))
		cmdArgs = append(cmdArgs, parts[1:]...)
		cmdArgs = append(cmdArgs, args...)
	} else {
		cmdArgs = args
	}

	// Create the stdio client using mcp-go
	mcpClient, err := client.NewStdioMCPClient(cmd, env, cmdArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to create stdio MCP client: %w", err)
	}

	return &StdioMCPClient{
		client:     mcpClient,
		command:    cmd,
		args:       cmdArgs,
		env:        env,
		configFile: configFile,
	}, nil
}

// Start initializes the MCP client connection.
func (c *StdioMCPClient) Start(ctx context.Context) error {
	// Initialize the client
	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			Capabilities:    mcp.ClientCapabilities{},
			ClientInfo: mcp.Implementation{
				Name:    "staff",
				Version: "1.0.0",
			},
		},
	}

	_, err := c.client.Initialize(ctx, initReq)
	if err != nil {
		return fmt.Errorf("failed to initialize MCP client: %w", err)
	}

	// Start the client
	if err := c.client.Start(ctx); err != nil {
		return fmt.Errorf("failed to start MCP client: %w", err)
	}

	logger.Info("STDIO MCP client started: command=%s", c.command)
	return nil
}

// ListTools returns all tools available from the MCP server.
func (c *StdioMCPClient) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	req := mcp.ListToolsRequest{}

	result, err := c.client.ListTools(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	tools := make([]ToolDefinition, 0, len(result.Tools))
	for _, tool := range result.Tools {
		// Convert mcp.Tool to ToolDefinition
		// Convert ToolInputSchema to map[string]interface{}
		inputSchema := make(map[string]interface{})
		inputSchema["type"] = tool.InputSchema.Type
		if tool.InputSchema.Properties != nil {
			inputSchema["properties"] = tool.InputSchema.Properties
		}
		if len(tool.InputSchema.Required) > 0 {
			inputSchema["required"] = tool.InputSchema.Required
		}
		if len(tool.InputSchema.Defs) > 0 {
			inputSchema["$defs"] = tool.InputSchema.Defs
		}

		tools = append(tools, ToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: inputSchema,
		})
	}

	return tools, nil
}

// InvokeTool invokes a tool on the MCP server.
func (c *StdioMCPClient) InvokeTool(ctx context.Context, name string, input map[string]interface{}) (map[string]interface{}, error) {
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: input,
		},
	}

	result, err := c.client.CallTool(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke tool %s: %w", name, err)
	}

	// Convert result to map[string]interface{}
	output := make(map[string]interface{})
	if len(result.Content) > 0 {
		// Extract text from content
		var texts []string
		for _, content := range result.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				texts = append(texts, textContent.Text)
			} else {
				// For other content types, try to convert to string
				if contentStr := mcp.GetTextFromContent(content); contentStr != "" {
					texts = append(texts, contentStr)
				}
			}
		}
		if len(texts) > 0 {
			if len(texts) == 1 {
				output["text"] = texts[0]
			} else {
				output["text"] = texts
			}
		}
	}

	// If we have an error, mark it
	if result.IsError {
		output["error"] = true
		if len(result.Content) > 0 {
			if textContent, ok := mcp.AsTextContent(result.Content[0]); ok {
				output["error_message"] = textContent.Text
			}
		}
	}

	return output, nil
}

// GetConfigSchema returns the configuration schema for the MCP server.
func (c *StdioMCPClient) GetConfigSchema(ctx context.Context) (*ConfigSchema, error) {
	// MCP doesn't have a direct "get config schema" method
	// We'll need to read it from the config file or return empty
	// For now, return empty schema
	return &ConfigSchema{
		Schema: make(map[string]interface{}),
	}, nil
}

// Close closes the connection to the MCP server.
func (c *StdioMCPClient) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// GetClient returns the underlying mcp-go client (for advanced usage).
func (c *StdioMCPClient) GetClient() *client.Client {
	return c.client
}
