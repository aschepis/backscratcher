package ollama

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/ollama/ollama/api"
)

// OllamaClient implements the llm.Client interface for Ollama's API.
type OllamaClient struct {
	client *api.Client
	model  string // Default model to use if not specified in request
}

// NewOllamaClient creates a new OllamaClient.
// If host is empty, it will use the default from environment (OLLAMA_HOST or http://localhost:11434).
// If model is empty, it will use the default from environment or config.
func NewOllamaClient(host, model string) (*OllamaClient, error) {
	var client *api.Client
	var err error

	if host != "" {
		// Create client with custom host
		baseURL, err := parseHost(host)
		if err != nil {
			return nil, fmt.Errorf("invalid host: %w", err)
		}
		// Create HTTP client (use default client)
		httpClient := &http.Client{}
		client = api.NewClient(baseURL, httpClient)
	} else {
		// Use environment-based client
		client, err = api.ClientFromEnvironment()
		if err != nil {
			return nil, fmt.Errorf("failed to create ollama client: %w", err)
		}
	}

	return &OllamaClient{
		client: client,
		model:  model,
	}, nil
}

// parseHost parses a host string into a URL.
func parseHost(host string) (*url.URL, error) {
	// If host doesn't have a scheme, add http://
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	return url.Parse(host)
}

// Synchronous implements llm.Client.Synchronous.
func (c *OllamaClient) Synchronous(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}

	// Determine model to use
	model := req.Model
	if model == "" {
		model = c.model
	}
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}

	// Convert messages
	ollamaMsgs, err := ToOllamaMessages(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	// Convert tools
	var ollamaTools []api.Tool
	if len(req.Tools) > 0 {
		ollamaTools, err = ToOllamaTools(req.Tools)
		if err != nil {
			return nil, fmt.Errorf("failed to convert tools: %w", err)
		}
	}

	// Build chat request
	chatReq := &api.ChatRequest{
		Model:    model,
		Messages: ollamaMsgs,
		Stream:   new(bool), // false for non-streaming
		Options:  make(map[string]interface{}),
	}

	// Set system prompt if provided
	if req.System != "" {
		// Ollama supports system messages in the messages array
		// We can also set it via options
		systemMsg := api.Message{
			Role:    "system",
			Content: req.System,
		}
		// Prepend system message to messages
		ollamaMsgs = append([]api.Message{systemMsg}, ollamaMsgs...)
		chatReq.Messages = ollamaMsgs
	}

	// Set tools if provided
	if len(ollamaTools) > 0 {
		chatReq.Tools = ollamaTools
	}

	// Set max tokens if provided
	if req.MaxTokens > 0 {
		chatReq.Options["num_predict"] = int(req.MaxTokens)
	}

	// Set temperature if provided
	if req.Temperature != nil {
		chatReq.Options["temperature"] = *req.Temperature
	}

	// Make API call
	var chatResp api.ChatResponse
	err = c.client.Chat(ctx, chatReq, func(resp api.ChatResponse) error {
		chatResp = resp
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("ollama chat request failed: %w", err)
	}

	// Convert response
	content := make([]llm.ContentBlock, 0)

	// Handle message content
	if chatResp.Message.Content != "" {
		content = append(content, llm.ContentBlock{
			Type: llm.ContentBlockTypeText,
			Text: chatResp.Message.Content,
		})
	}

	// Handle tool calls
	for _, toolCall := range chatResp.Message.ToolCalls {
		toolUseBlock, err := FromOllamaToolCall(toolCall)
		if err != nil {
			return nil, fmt.Errorf("failed to convert tool call: %w", err)
		}
		content = append(content, llm.ContentBlock{
			Type:    llm.ContentBlockTypeToolUse,
			ToolUse: toolUseBlock,
		})
	}

	// Convert usage (Ollama may not provide detailed usage)
	usage := &llm.Usage{
		InputTokens:  0,
		OutputTokens: 0,
	}
	if chatResp.PromptEvalCount > 0 {
		usage.InputTokens = int64(chatResp.PromptEvalCount)
	}
	if chatResp.EvalCount > 0 {
		usage.OutputTokens = int64(chatResp.EvalCount)
	}

	// Determine stop reason
	stopReason := "end_turn"
	if chatResp.Done {
		stopReason = "stop"
	}

	return &llm.Response{
		Content:    content,
		Usage:      usage,
		StopReason: stopReason,
	}, nil
}

// Stream implements llm.Client.Stream.
func (c *OllamaClient) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}

	// Determine model to use
	model := req.Model
	if model == "" {
		model = c.model
	}
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}

	// Convert messages
	ollamaMsgs, err := ToOllamaMessages(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	// Convert tools
	var ollamaTools []api.Tool
	if len(req.Tools) > 0 {
		ollamaTools, err = ToOllamaTools(req.Tools)
		if err != nil {
			return nil, fmt.Errorf("failed to convert tools: %w", err)
		}
	}

	// Build chat request with streaming enabled
	stream := true
	chatReq := &api.ChatRequest{
		Model:    model,
		Messages: ollamaMsgs,
		Stream:   &stream,
		Options:  make(map[string]interface{}),
	}

	// Set system prompt if provided
	if req.System != "" {
		systemMsg := api.Message{
			Role:    "system",
			Content: req.System,
		}
		ollamaMsgs = append([]api.Message{systemMsg}, ollamaMsgs...)
		chatReq.Messages = ollamaMsgs
	}

	// Set tools if provided
	if len(ollamaTools) > 0 {
		chatReq.Tools = ollamaTools
	}

	// Set max tokens if provided
	if req.MaxTokens > 0 {
		chatReq.Options["num_predict"] = int(req.MaxTokens)
	}

	// Set temperature if provided
	if req.Temperature != nil {
		chatReq.Options["temperature"] = *req.Temperature
	}

	// Create and return stream
	return newOllamaStream(ctx, c.client, chatReq), nil
}
