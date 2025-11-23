package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/aschepis/backscratcher/staff/llm"
	openai "github.com/sashabaranov/go-openai"
)

// OpenAI API errors don't directly expose retry-after headers
// We'll use a default retry after duration for rate limits
const defaultRetryAfter = 60 * time.Second

// OpenAIClient implements the llm.Client interface for OpenAI's API.
type OpenAIClient struct {
	client *openai.Client
	model  string // Default model to use if not specified in request
}

// NewOpenAIClient creates a new OpenAIClient.
// If apiKey is empty, it will return an error.
// If baseURL is empty, it will use the default OpenAI API endpoint.
// If model is empty, it will use the default from config or request.
func NewOpenAIClient(apiKey, baseURL, model, organization string) (*OpenAIClient, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("api key is required")
	}

	config := openai.DefaultConfig(apiKey)

	// Set custom base URL if provided
	if baseURL != "" {
		config.BaseURL = baseURL
	}

	// Set organization if provided
	if organization != "" {
		config.OrgID = organization
	}

	client := openai.NewClientWithConfig(config)

	return &OpenAIClient{
		client: client,
		model:  model,
	}, nil
}

// Synchronous implements llm.Client.Synchronous.
func (c *OpenAIClient) Synchronous(ctx context.Context, req *llm.Request) (*llm.Response, error) {
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
	openaiMsgs, err := ToOpenAIMessages(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	// Convert tools
	var openaiTools []openai.Tool
	if len(req.Tools) > 0 {
		openaiTools, err = ToOpenAITools(req.Tools)
		if err != nil {
			return nil, fmt.Errorf("failed to convert tools: %w", err)
		}
	}

	// Build chat completion request
	chatReq := openai.ChatCompletionRequest{
		Model:    model,
		Messages: openaiMsgs,
	}

	// Set system message if provided (OpenAI supports system role in messages)
	if req.System != "" {
		systemMsg := openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: req.System,
		}
		// Prepend system message to messages
		chatReq.Messages = append([]openai.ChatCompletionMessage{systemMsg}, openaiMsgs...)
	}

	// Set tools if provided
	if len(openaiTools) > 0 {
		chatReq.Tools = openaiTools
		// Set tool choice to auto (let model decide when to use tools)
		chatReq.ToolChoice = "auto"
	}

	// Set max tokens if provided
	if req.MaxTokens > 0 {
		chatReq.MaxTokens = int(req.MaxTokens)
	}

	// Set temperature if provided
	if req.Temperature != nil {
		chatReq.Temperature = float32(*req.Temperature)
	}

	// Make API call
	chatResp, err := c.client.CreateChatCompletion(ctx, chatReq)
	if err != nil {
		return nil, convertOpenAIError(err)
	}

	// Convert response
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := chatResp.Choices[0]
	content := make([]llm.ContentBlock, 0)

	// Handle message content
	if choice.Message.Content != "" {
		content = append(content, llm.ContentBlock{
			Type: llm.ContentBlockTypeText,
			Text: choice.Message.Content,
		})
	}

	// Handle tool calls
	for _, toolCall := range choice.Message.ToolCalls {
		toolUseBlock, err := FromOpenAIToolCall(toolCall)
		if err != nil {
			return nil, fmt.Errorf("failed to convert tool call: %w", err)
		}
		content = append(content, llm.ContentBlock{
			Type:    llm.ContentBlockTypeToolUse,
			ToolUse: toolUseBlock,
		})
	}

	// Convert usage
	usage := &llm.Usage{
		InputTokens:  int64(chatResp.Usage.PromptTokens),
		OutputTokens: int64(chatResp.Usage.CompletionTokens),
	}

	// Determine stop reason
	stopReason := "stop"
	switch reason := choice.FinishReason; reason {
	case openai.FinishReasonLength:
		stopReason = "max_tokens"
	case openai.FinishReasonToolCalls:
		stopReason = "tool_calls"
	case openai.FinishReasonStop:
		stopReason = "stop"
	default:
		// leave as default "stop"
	}

	return &llm.Response{
		Content:    content,
		Usage:      usage,
		StopReason: stopReason,
	}, nil
}

// Stream implements llm.Client.Stream.
func (c *OpenAIClient) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
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
	openaiMsgs, err := ToOpenAIMessages(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	// Convert tools
	var openaiTools []openai.Tool
	if len(req.Tools) > 0 {
		openaiTools, err = ToOpenAITools(req.Tools)
		if err != nil {
			return nil, fmt.Errorf("failed to convert tools: %w", err)
		}
	}

	// Build chat completion request with streaming enabled
	chatReq := openai.ChatCompletionRequest{
		Model:    model,
		Messages: openaiMsgs,
		Stream:   true,
	}

	// Set system message if provided
	if req.System != "" {
		systemMsg := openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: req.System,
		}
		chatReq.Messages = append([]openai.ChatCompletionMessage{systemMsg}, openaiMsgs...)
	}

	// Set tools if provided
	if len(openaiTools) > 0 {
		chatReq.Tools = openaiTools
		chatReq.ToolChoice = "auto"
	}

	// Set max tokens if provided
	if req.MaxTokens > 0 {
		chatReq.MaxTokens = int(req.MaxTokens)
	}

	// Set temperature if provided
	if req.Temperature != nil {
		chatReq.Temperature = float32(*req.Temperature)
	}

	// Create stream
	stream, err := c.client.CreateChatCompletionStream(ctx, chatReq)
	if err != nil {
		return nil, convertOpenAIError(err)
	}

	// Create and return our stream wrapper
	return newOpenAIStream(ctx, stream), nil
}

// convertOpenAIError converts OpenAI API errors to llm.Error types.
func convertOpenAIError(err error) error {
	if err == nil {
		return nil
	}

	// Check if it's an OpenAI API error using errors.As
	var apiErr *openai.APIError
	if !errors.As(err, &apiErr) {
		// Not an OpenAI API error, return as provider error
		return llm.NewProviderError("OpenAI API error", err)
	}

	// Map status codes to error types
	switch apiErr.HTTPStatusCode {
	case http.StatusTooManyRequests:
		// Rate limit error
		retryAfter := defaultRetryAfter
		return llm.NewRateLimitError(
			fmt.Sprintf("OpenAI rate limit: %s", apiErr.Message),
			&retryAfter,
			err,
		)
	case http.StatusRequestEntityTooLarge:
		// Request too large
		return llm.NewRequestTooLargeError(
			fmt.Sprintf("OpenAI request too large: %s", apiErr.Message),
			err,
		)
	case http.StatusBadRequest:
		// Invalid request
		return &llm.Error{
			Type:        llm.ErrorTypeInvalidRequest,
			Message:     fmt.Sprintf("OpenAI invalid request: %s", apiErr.Message),
			Retryable:   false,
			StatusCode:  apiErr.HTTPStatusCode,
			ProviderErr: err,
		}
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
		// Server errors - potentially retryable
		return &llm.Error{
			Type:        llm.ErrorTypeProvider,
			Message:     fmt.Sprintf("OpenAI server error: %s", apiErr.Message),
			Retryable:   true,
			StatusCode:  apiErr.HTTPStatusCode,
			ProviderErr: err,
		}
	default:
		// Other errors
		return &llm.Error{
			Type:        llm.ErrorTypeProvider,
			Message:     fmt.Sprintf("OpenAI API error: %s", apiErr.Message),
			Retryable:   false,
			StatusCode:  apiErr.HTTPStatusCode,
			ProviderErr: err,
		}
	}
}
