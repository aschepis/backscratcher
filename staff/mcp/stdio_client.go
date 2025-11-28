package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/rs/zerolog"
)

// StdioMCPClient implements MCPClient for STDIO transport.
type StdioMCPClient struct {
	client     *client.Client
	command    string
	args       []string
	env        []string
	configFile string
	logger     zerolog.Logger
}

// NewStdioMCPClient creates a new STDIO MCP client.
func NewStdioMCPClient(logger zerolog.Logger, command, configFile string, args, env []string) (*StdioMCPClient, error) {
	if command == "" {
		return nil, fmt.Errorf("command is required for STDIO MCP client")
	}

	logger = logger.With().Str("component", "stdioMCPClient").Logger()
	logger.Info().Str("command", command).Strs("args", args).Strs("env", env).Str("config_file", configFile).Msg("Creating STDIO MCP client")

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

	logger.Info().Str("command", cmd).Strs("final_args", cmdArgs).Msg("NewStdioMCPClient: parsed command and final arguments")

	// Create the stdio client using mcp-go
	logger.Info().Str("command", cmd).Strs("env", env).Strs("final_args", cmdArgs).Msg("NewStdioMCPClient: creating underlying mcp-go client")
	mcpClient, err := client.NewStdioMCPClient(cmd, env, cmdArgs...)
	if err != nil {
		logger.Error().Err(err).Msg("NewStdioMCPClient: failed to create underlying client")
		return nil, fmt.Errorf("failed to create stdio MCP client: %w", err)
	}

	logger.Info().Str("command", cmd).Msg("NewStdioMCPClient: successfully created underlying client")
	return &StdioMCPClient{
		client:     mcpClient,
		command:    cmd,
		args:       cmdArgs,
		env:        env,
		configFile: configFile,
		logger:     logger,
	}, nil
}

// Start initializes the MCP client connection.
func (c *StdioMCPClient) Start(ctx context.Context) error {
	c.logger.Debug().
		Str("method", "Start").
		Str("command", c.command).
		Msg("StdioMCPClient: beginning initialization")

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

	c.logger.Debug().
		Str("method", "Start").
		Str("protocolVersion", mcp.LATEST_PROTOCOL_VERSION).
		Msg("StdioMCPClient: calling Initialize")

	// Check if context is already cancelled before calling Initialize
	select {
	case <-ctx.Done():
		c.logger.Error().
			Str("method", "Start").
			Str("event", "context_canceled_before_initialize").
			Str("command", c.command).
			Err(ctx.Err()).
			Msg("StdioMCPClient: context cancelled before Initialize")
		return fmt.Errorf("context cancelled before initialize: %w", ctx.Err())
	default:
	}

	// Call Initialize in a goroutine to detect hangs
	c.logger.Debug().
		Str("method", "Start").
		Str("command", c.command).
		Msg("StdioMCPClient: about to call c.client.Initialize (may hang if server is starting)")
	initDone := make(chan error, 1)
	go func() {
		_, initErr := c.client.Initialize(ctx, initReq)
		initDone <- initErr
	}()

	// Wait for Initialize to complete or context timeout
	select {
	case err := <-initDone:
		if err != nil {
			c.logger.Error().
				Str("method", "Start").
				Str("command", c.command).
				Err(err).
				Msg("StdioMCPClient: Initialize failed")
			return fmt.Errorf("failed to initialize MCP client: %w", err)
		}
		c.logger.Info().
			Str("method", "Start").
			Str("command", c.command).
			Msg("StdioMCPClient: Initialize completed successfully")
	case <-ctx.Done():
		c.logger.Error().
			Str("method", "Start").
			Str("event", "context_canceled_initialize").
			Str("command", c.command).
			Err(ctx.Err()).
			Msg("StdioMCPClient: context cancelled/timeout during Initialize")
		return fmt.Errorf("context cancelled during initialize: %w", ctx.Err())
	}

	// Start the client
	c.logger.Debug().
		Str("method", "Start").
		Str("command", c.command).
		Msg("StdioMCPClient: calling client.Start")

	// Check if context is already cancelled before calling Start
	select {
	case <-ctx.Done():
		c.logger.Error().
			Str("method", "Start").
			Str("event", "context_canceled_before_start").
			Str("command", c.command).
			Err(ctx.Err()).
			Msg("StdioMCPClient: context cancelled before Start")
		return fmt.Errorf("context cancelled before start: %w", ctx.Err())
	default:
	}

	// Call Start in a goroutine to detect hangs
	c.logger.Debug().
		Str("method", "Start").
		Str("command", c.command).
		Msg("StdioMCPClient: about to call c.client.Start")
	startDone := make(chan error, 1)
	go func() {
		startDone <- c.client.Start(ctx)
	}()

	// Wait for Start to complete or context timeout
	select {
	case err := <-startDone:
		if err != nil {
			c.logger.Error().
				Str("method", "Start").
				Str("command", c.command).
				Err(err).
				Msg("StdioMCPClient: client.Start failed")
			return fmt.Errorf("failed to start MCP client: %w", err)
		}
		c.logger.Debug().
			Str("method", "Start").
			Str("command", c.command).
			Msg("StdioMCPClient: client.Start completed successfully")
	case <-ctx.Done():
		c.logger.Error().
			Str("method", "Start").
			Str("event", "context_canceled_start").
			Str("command", c.command).
			Err(ctx.Err()).
			Msg("StdioMCPClient: context cancelled/timeout during Start")
		return fmt.Errorf("context cancelled during start: %w", ctx.Err())
	}

	c.logger.Info().
		Str("method", "Start").
		Str("command", c.command).
		Msg("StdioMCPClient: client started successfully")
	return nil
}

// ListTools returns all tools available from the MCP server.
func (c *StdioMCPClient) ListTools(ctx context.Context) ([]ToolDefinition, error) {
	c.logger.Info().
		Str("method", "ListTools").
		Str("command", c.command).
		Msg("Requesting tools from MCP server")
	req := mcp.ListToolsRequest{}

	result, err := c.client.ListTools(ctx, req)
	if err != nil {
		c.logger.Error().
			Str("method", "ListTools").
			Str("command", c.command).
			Err(err).
			Msg("Failed to list tools")
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}
	c.logger.Info().
		Str("method", "ListTools").
		Str("command", c.command).
		Int("tool_count", len(result.Tools)).
		Msg("Received tools from MCP server")

	tools := make([]ToolDefinition, 0, len(result.Tools))
	for _, tool := range result.Tools {
		// Convert mcp.Tool to ToolDefinition
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
	c.logger.Debug().
		Str("method", "InvokeTool").
		Str("tool_name", name).
		Str("command", c.command).
		Msg("Invoking tool on MCP server")
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: input,
		},
	}

	result, err := c.client.CallTool(ctx, req)
	if err != nil {
		c.logger.Error().
			Str("method", "InvokeTool").
			Str("tool_name", name).
			Str("command", c.command).
			Err(err).
			Msg("Failed to invoke tool on MCP server")
		return nil, fmt.Errorf("failed to invoke tool %s: %w", name, err)
	}

	// Convert result to map[string]interface{}
	output := make(map[string]interface{})
	if len(result.Content) > 0 {
		var texts []string
		for _, content := range result.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				texts = append(texts, textContent.Text)
			} else {
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
