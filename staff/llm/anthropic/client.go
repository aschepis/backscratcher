package anthropic

import (
	"context"
	"encoding/json"
	"fmt"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/aschepis/backscratcher/staff/llm"
)

// AnthropicClient implements the llm.Client interface for Anthropic's API.
type AnthropicClient struct {
	client *anthropic.Client
}

// NewAnthropicClient creates a new AnthropicClient with the given API key.
func NewAnthropicClient(apiKey string) (*AnthropicClient, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("api key is required")
	}

	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &AnthropicClient{
		client: &client,
	}, nil
}

// Synchronous implements llm.Client.Synchronous.
func (c *AnthropicClient) Synchronous(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}

	// Convert tools
	tools := ToToolUnionParams(req.Tools)

	// Convert messages
	anthropicMsgs, err := ToMessageParams(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	// Build system blocks with prompt caching
	systemBlocks := buildSystemBlocks(req.System, tools)

	// Create API params
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: req.MaxTokens,
		Messages:  anthropicMsgs,
		System:    systemBlocks,
		Tools:     tools,
	}

	// Make API call
	message, err := c.client.Messages.New(ctx, params)
	if err != nil {
		return nil, err
	}

	// Convert response
	content := make([]llm.ContentBlock, 0, len(message.Content))
	for _, blockUnion := range message.Content {
		switch block := blockUnion.AsAny().(type) {
		case anthropic.TextBlock:
			content = append(content, llm.ContentBlock{
				Type: llm.ContentBlockTypeText,
				Text: block.Text,
			})
		case anthropic.ToolUseBlock:
			// Extract input as map[string]interface{}
			var input map[string]interface{}
			if block.Input != nil {
				if inputBytes, err := json.Marshal(block.Input); err == nil {
					if err := json.Unmarshal(inputBytes, &input); err != nil {
						input = make(map[string]interface{})
					}
				} else {
					input = make(map[string]interface{})
				}
			} else {
				input = make(map[string]interface{})
			}
			content = append(content, llm.ContentBlock{
				Type: llm.ContentBlockTypeToolUse,
				ToolUse: &llm.ToolUseBlock{
					ID:    block.ID,
					Name:  block.Name,
					Input: input,
				},
			})
		}
	}

	// Convert usage
	usage := &llm.Usage{
		InputTokens:              message.Usage.InputTokens,
		OutputTokens:             message.Usage.OutputTokens,
		CacheCreationInputTokens: message.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     message.Usage.CacheReadInputTokens,
	}

	// Extract stop reason
	stopReason := string(message.StopReason)

	return &llm.Response{
		Content:    content,
		Usage:      usage,
		StopReason: stopReason,
	}, nil
}

// Stream implements llm.Client.Stream.
func (c *AnthropicClient) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}

	// Convert tools
	tools := ToToolUnionParams(req.Tools)

	// Convert messages
	anthropicMsgs, err := ToMessageParams(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	// Build system blocks with prompt caching
	systemBlocks := buildSystemBlocks(req.System, tools)

	// Create API params
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: req.MaxTokens,
		Messages:  anthropicMsgs,
		System:    systemBlocks,
		Tools:     tools,
	}

	// Create streaming request
	stream := c.client.Messages.NewStreaming(ctx, params)

	// Create and return our stream wrapper
	return newAnthropicStream(ctx, stream), nil
}

// buildSystemBlocks creates system text blocks with prompt caching enabled if appropriate.
// According to Anthropic's prompt caching documentation, placing cache_control on the system block
// caches the full prefix: tools, system, and messages (in that order) up to and including the
// block designated with cache_control. This means tools are automatically cached along with the system prompt.
//
// Prompt caching is enabled when the combined size of tools + system is at least 4000 characters
// (roughly equivalent to ~1000 tokens, meeting Anthropic's 1024 token minimum requirement).
// This helps reduce costs and latency for repeated requests with the same tools and system prompt.
func buildSystemBlocks(systemPrompt string, tools []anthropic.ToolUnionParam) []anthropic.TextBlockParam {
	// Anthropic requires minimum 1,024 tokens for caching, which is roughly 4,000 characters
	// (using a rough estimate of 1 token ≈ 4 characters). We use 4000 characters as a safe
	// threshold to ensure we meet the minimum token requirement across different tokenization methods.
	const minCacheableLength = 4000

	blocks := []anthropic.TextBlockParam{
		{Text: systemPrompt},
	}

	// Calculate approximate size of tools by marshaling to JSON
	toolsSize := 0
	if len(tools) > 0 {
		if toolsJSON, err := json.Marshal(tools); err == nil {
			toolsSize = len(toolsJSON)
		}
	}

	// Combined size of tools + system prompt
	combinedSize := toolsSize + len(systemPrompt)

	// Enable ephemeral caching if the combined static content (tools + system) is large enough
	// When cache_control is placed on the system block, it caches both tools and system together
	if combinedSize >= minCacheableLength {
		// Create cache control with default 5-minute TTL
		cacheControl := anthropic.NewCacheControlEphemeralParam()
		blocks[0].CacheControl = cacheControl
	}

	return blocks
}
