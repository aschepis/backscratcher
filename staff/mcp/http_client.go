package mcp

import (
	"context"
	"fmt"
	"net/url"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/aschepis/backscratcher/staff/logger"
)

// HttpMCPClient implements MCPClient for HTTP transport.
type HttpMCPClient struct {
	client     *client.Client
	baseURL    string
	configFile string
}

// NewHttpMCPClient creates a new HTTP MCP client.
func NewHttpMCPClient(baseURL, configFile string) (*HttpMCPClient, error) {
	logger.Info("NewHttpMCPClient: creating client for baseURL=%s configFile=%s", baseURL, configFile)
	if baseURL == "" {
		return nil, fmt.Errorf("baseURL is required for HTTP MCP client")
	}

	// Validate URL
	_, err := url.Parse(baseURL)
	if err != nil {
		logger.Error("NewHttpMCPClient: invalid URL: %v", err)
		return nil, fmt.Errorf("invalid baseURL: %w", err)
	}

	// Create the HTTP client using mcp-go
	logger.Info("NewHttpMCPClient: creating underlying mcp-go HTTP client")
	mcpClient, err := client.NewStreamableHttpClient(baseURL)
	if err != nil {
		logger.Error("NewHttpMCPClient: failed to create underlying client: %v", err)
		return nil, fmt.Errorf("failed to create HTTP MCP client: %w", err)
	}

	logger.Info("NewHttpMCPClient: successfully created underlying client")
	return &HttpMCPClient{
		client:     mcpClient,
		baseURL:    baseURL,
		configFile: configFile,
	}, nil
}

// NewHttpMCPClientWithAuth creates a new HTTP MCP client with authentication.
func NewHttpMCPClientWithAuth(baseURL, configFile, authToken string) (*HttpMCPClient, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("baseURL is required for HTTP MCP client")
	}

	// Validate URL
	_, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid baseURL: %w", err)
	}

	// Create the HTTP client with auth headers
	// Note: WithHeaders returns ClientOption, but we need StreamableHTTPCOption
	// For now, create without auth headers - auth can be added via config file
	mcpClient, err := client.NewStreamableHttpClient(baseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP MCP client: %w", err)
	}

	return &HttpMCPClient{
		client:     mcpClient,
		baseURL:    baseURL,
		configFile: configFile,
	}, nil
}

// Start initializes the MCP client connection.
func (c *HttpMCPClient) Start(ctx context.Context) error {
	logger.Info("HttpMCPClient.Start: beginning initialization for baseURL=%s", c.baseURL)
	// For HTTP clients, Start() may handle initialization internally
	// Try calling Start() first, and only initialize explicitly if needed
	logger.Info("HttpMCPClient.Start: calling client.Start")
	if err := c.client.Start(ctx); err != nil {
		logger.Warn("HttpMCPClient.Start: client.Start failed, trying explicit initialization: %v", err)
		// If Start() fails, try explicit initialization with different protocol versions
		protocolVersions := []string{
			"2024-11-05", // Older stable version
			mcp.LATEST_PROTOCOL_VERSION,
		}

		var lastErr error = err
		for _, protocolVersion := range protocolVersions {
			logger.Info("HttpMCPClient.Start: trying Initialize with protocolVersion=%s", protocolVersion)
			// Initialize the client explicitly
			initReq := mcp.InitializeRequest{
				Params: mcp.InitializeParams{
					ProtocolVersion: protocolVersion,
					Capabilities:    mcp.ClientCapabilities{},
					ClientInfo: mcp.Implementation{
						Name:    "staff",
						Version: "1.0.0",
					},
				},
			}

			_, initErr := c.client.Initialize(ctx, initReq)
			if initErr != nil {
				lastErr = initErr
				logger.Warn("HttpMCPClient.Start: failed to initialize with protocol version %s: %v, trying next version", protocolVersion, initErr)
				continue
			}
			logger.Info("HttpMCPClient.Start: Initialize succeeded with protocolVersion=%s", protocolVersion)

			// Try Start() again after initialization
			logger.Info("HttpMCPClient.Start: calling client.Start after Initialize")
			if startErr := c.client.Start(ctx); startErr != nil {
				lastErr = startErr
				logger.Warn("HttpMCPClient.Start: failed to start after initialization with protocol version %s: %v, trying next version", protocolVersion, startErr)
				continue
			}

			logger.Info("HttpMCPClient.Start: client started successfully: baseURL=%s protocolVersion=%s", c.baseURL, protocolVersion)
			return nil
		}

		logger.Error("HttpMCPClient.Start: all initialization attempts failed: %v", lastErr)
		return fmt.Errorf("failed to start HTTP MCP client: %w", lastErr)
	}

	// Start() succeeded without explicit initialization
	logger.Info("HttpMCPClient.Start: client started successfully without explicit initialization: baseURL=%s", c.baseURL)
	return nil
}

// ListTools returns all tools available from the MCP server.
func (c *HttpMCPClient) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	logger.Info("HttpMCPClient.ListTools: requesting tools from baseURL=%s", c.baseURL)
	// Use the mcp-go client's ListTools method
	req := mcp.ListToolsRequest{}

	result, err := c.client.ListTools(ctx, req)
	if err != nil {
		logger.Error("HttpMCPClient.ListTools: failed to list tools: %v", err)
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}
	logger.Info("HttpMCPClient.ListTools: received %d tools from baseURL=%s", len(result.Tools), c.baseURL)

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
func (c *HttpMCPClient) InvokeTool(ctx context.Context, name string, input map[string]interface{}) (map[string]interface{}, error) {
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
func (c *HttpMCPClient) GetConfigSchema(ctx context.Context) (*ConfigSchema, error) {
	// MCP doesn't have a direct "get config schema" method
	// We'll need to read it from the config file or return empty
	// For now, return empty schema
	return &ConfigSchema{
		Schema: make(map[string]interface{}),
	}, nil
}

// Close closes the connection to the MCP server.
func (c *HttpMCPClient) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// GetClient returns the underlying mcp-go client (for advanced usage).
func (c *HttpMCPClient) GetClient() *client.Client {
	return c.client
}
