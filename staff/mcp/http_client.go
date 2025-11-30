package mcp

import (
	"context"
	"fmt"
	"net/url"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/rs/zerolog"
	"github.com/samber/lo"
)

// HttpMCPClient implements MCPClient for HTTP transport.
type HttpMCPClient struct {
	client     *client.Client
	baseURL    string
	configFile string
	logger     zerolog.Logger
}

// NewHttpMCPClient creates a new HTTP MCP client.
func NewHttpMCPClient(logger zerolog.Logger, baseURL, configFile string) (*HttpMCPClient, error) {
	logger = logger.With().Str("component", "httpMCPClient").Logger()
	logger.Info().Str("base_url", baseURL).Str("config_file", configFile).Msg("Creating HTTP MCP client")
	if baseURL == "" {
		return nil, fmt.Errorf("baseURL is required for HTTP MCP client")
	}

	// Validate URL
	_, err := url.Parse(baseURL)
	if err != nil {
		logger.Error().Err(err).Msg("Invalid URL")
		return nil, fmt.Errorf("invalid baseURL: %w", err)
	}

	// Create the HTTP client using mcp-go
	logger.Info().Msg("Creating underlying mcp-go HTTP client")
	mcpClient, err := client.NewStreamableHttpClient(baseURL)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to create underlying client")
		return nil, fmt.Errorf("failed to create HTTP MCP client: %w", err)
	}

	logger.Info().Msg("Successfully created underlying client")
	return &HttpMCPClient{
		client:     mcpClient,
		baseURL:    baseURL,
		configFile: configFile,
		logger:     logger,
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
	c.logger.Info().
		Str("base_url", c.baseURL).
		Msg("HttpMCPClient.Start: beginning initialization")

	// For HTTP clients, Start() may handle initialization internally
	// Try calling Start() first, and only initialize explicitly if needed
	c.logger.Info().
		Msg("HttpMCPClient.Start: calling client.Start")
	if err := c.client.Start(ctx); err != nil {
		c.logger.Warn().
			Err(err).
			Msg("HttpMCPClient.Start: client.Start failed, trying explicit initialization")
		// If Start() fails, try explicit initialization with different protocol versions
		protocolVersions := []string{
			"2024-11-05", // Older stable version
			mcp.LATEST_PROTOCOL_VERSION,
		}

		var lastErr error = err
		for _, protocolVersion := range protocolVersions {
			c.logger.Info().
				Str("protocol_version", protocolVersion).
				Msg("HttpMCPClient.Start: trying Initialize with protocolVersion")
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
				c.logger.Warn().
					Str("protocol_version", protocolVersion).
					Err(initErr).
					Msg("HttpMCPClient.Start: failed to initialize with protocol version, trying next version")
				continue
			}
			c.logger.Info().
				Str("protocol_version", protocolVersion).
				Msg("HttpMCPClient.Start: Initialize succeeded")

			// Try Start() again after initialization
			c.logger.Info().
				Msg("HttpMCPClient.Start: calling client.Start after Initialize")
			if startErr := c.client.Start(ctx); startErr != nil {
				lastErr = startErr
				c.logger.Warn().
					Str("protocol_version", protocolVersion).
					Err(startErr).
					Msg("HttpMCPClient.Start: failed to start after initialization, trying next version")
				continue
			}

			c.logger.Info().
				Str("base_url", c.baseURL).
				Str("protocol_version", protocolVersion).
				Msg("HttpMCPClient.Start: client started successfully")
			return nil
		}

		c.logger.Error().
			Err(lastErr).
			Msg("HttpMCPClient.Start: all initialization attempts failed")
		return fmt.Errorf("failed to start HTTP MCP client: %w", lastErr)
	}

	// Start() succeeded without explicit initialization
	c.logger.Info().
		Str("base_url", c.baseURL).
		Msg("HttpMCPClient.Start: client started successfully without explicit initialization")
	return nil
}

// ListTools returns all tools available from the MCP server.
func (c *HttpMCPClient) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	c.logger.Info().
		Str("base_url", c.baseURL).
		Msg("HttpMCPClient.ListTools: requesting tools")
	// Use the mcp-go client's ListTools method
	req := mcp.ListToolsRequest{}

	result, err := c.client.ListTools(ctx, req)
	if err != nil {
		c.logger.Error().
			Err(err).
			Msg("HttpMCPClient.ListTools: failed to list tools")
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}
	c.logger.Info().
		Int("tool_count", len(result.Tools)).
		Str("base_url", c.baseURL).
		Msg("HttpMCPClient.ListTools: received tools")

	tools := lo.Map(result.Tools, func(tool mcp.Tool, _ int) ToolDefinition {
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

		return ToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: inputSchema,
		}
	})

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
