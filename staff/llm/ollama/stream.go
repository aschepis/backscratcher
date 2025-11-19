package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/ollama/ollama/api"
)

// ollamaStream implements the llm.Stream interface for Ollama streaming responses.
type ollamaStream struct {
	ctx      context.Context
	client   *api.Client
	req      *api.ChatRequest
	events   []*llm.StreamEvent
	current  int
	mu       sync.Mutex
	err      error
	done     bool
	started  bool
	response *api.ChatResponse
}

// newOllamaStream creates a new ollamaStream.
func newOllamaStream(ctx context.Context, client *api.Client, req *api.ChatRequest) *ollamaStream {
	return &ollamaStream{
		ctx:     ctx,
		client:  client,
		req:     req,
		events:  make([]*llm.StreamEvent, 0),
		current: -1,
	}
}

// Next advances to the next event in the stream.
func (s *ollamaStream) Next() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If we haven't started, start the stream
	if !s.started {
		s.started = true
		s.startStream()
	}

	// If there's an error or we're done, return false
	if s.err != nil || s.done {
		return false
	}

	// Move to next event
	s.current++
	return s.current < len(s.events)
}

// Event returns the current event.
func (s *ollamaStream) Event() *llm.StreamEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.current < 0 || s.current >= len(s.events) {
		return nil
	}
	return s.events[s.current]
}

// Err returns any error that occurred during streaming.
func (s *ollamaStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close closes the stream and releases resources.
func (s *ollamaStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
	return nil
}

// startStream starts the streaming request and processes responses.
func (s *ollamaStream) startStream() {
	// Emit start event
	s.events = append(s.events, &llm.StreamEvent{
		Type: llm.StreamEventTypeStart,
		Delta: nil,
		Usage: nil,
		Done:  false,
	})

	// Track accumulated content for tool calls
	var accumulatedText strings.Builder
	var currentToolCall *llm.ToolUseBlock
	var toolInputBuilder strings.Builder

	// Call Chat with streaming callback
	err := s.client.Chat(s.ctx, s.req, func(resp api.ChatResponse) error {
		s.mu.Lock()
		defer s.mu.Unlock()

		// Update response
		s.response = &resp

		// Handle message content deltas
		if resp.Message.Content != "" {
			// If we have accumulated text, check if we need to emit it
			// For streaming, we get incremental content
			contentDelta := resp.Message.Content
			if accumulatedText.Len() > 0 {
				// Calculate delta (new content since last update)
				// This is simplified - in practice, Ollama may send full content or deltas
				// We'll emit the content as a delta
				delta := contentDelta[len(accumulatedText.String()):]
				if len(delta) > 0 {
					s.events = append(s.events, &llm.StreamEvent{
						Type: llm.StreamEventTypeContentDelta,
						Delta: &llm.StreamDelta{
							Type: llm.StreamDeltaTypeText,
							Text: delta,
						},
						Usage: nil,
						Done:  false,
					})
				}
			} else {
				// First content block
				s.events = append(s.events, &llm.StreamEvent{
					Type: llm.StreamEventTypeContentBlock,
					Delta: &llm.StreamDelta{
						Type: llm.StreamDeltaTypeText,
						Text: contentDelta,
					},
					Usage: nil,
					Done:  false,
				})
			}
			accumulatedText.Reset()
			accumulatedText.WriteString(contentDelta)
		}

		// Handle tool calls
		for _, toolCall := range resp.Message.ToolCalls {
			// Check if this is a new tool call or continuation
			if currentToolCall == nil || currentToolCall.Name != toolCall.Function.Name {
				// New tool call
				if currentToolCall != nil {
					// Finish previous tool call
					var input map[string]interface{}
					if toolInputBuilder.Len() > 0 {
						if err := json.Unmarshal([]byte(toolInputBuilder.String()), &input); err != nil {
							input = make(map[string]interface{})
						}
					} else {
						input = make(map[string]interface{})
					}
					currentToolCall.Input = input
					toolInputBuilder.Reset()
				}

				// Start new tool call
				toolUseID := fmt.Sprintf("tool_%s_%d", toolCall.Function.Name, len(s.events))
				currentToolCall = &llm.ToolUseBlock{
					ID:    toolUseID,
					Name:  toolCall.Function.Name,
					Input: make(map[string]interface{}),
				}

				s.events = append(s.events, &llm.StreamEvent{
					Type: llm.StreamEventTypeContentBlock,
					Delta: &llm.StreamDelta{
						Type:    llm.StreamDeltaTypeToolUse,
						ToolUse: currentToolCall,
					},
					Usage: nil,
					Done:  false,
				})
			}

			// Accumulate tool input (Arguments is a map[string]any)
			if toolCall.Function.Arguments != nil && len(toolCall.Function.Arguments) > 0 {
				// Marshal arguments to JSON string for streaming
				argsBytes, err := json.Marshal(toolCall.Function.Arguments)
				if err == nil {
					argsStr := string(argsBytes)
					toolInputBuilder.WriteString(argsStr)
					s.events = append(s.events, &llm.StreamEvent{
						Type: llm.StreamEventTypeContentDelta,
						Delta: &llm.StreamDelta{
							Type:      llm.StreamDeltaTypeToolInput,
							ToolInput: argsStr,
						},
						Usage: nil,
						Done:  false,
					})
				}
			}
		}

		// Check if done
		if resp.Done {
			// Finish any pending tool call
			if currentToolCall != nil {
				// Parse accumulated tool input
				var input map[string]interface{}
				if toolInputBuilder.Len() > 0 {
					if err := json.Unmarshal([]byte(toolInputBuilder.String()), &input); err != nil {
						input = make(map[string]interface{})
					}
				} else {
					input = make(map[string]interface{})
				}
				currentToolCall.Input = input
			}

			// Emit usage if available
			usage := &llm.Usage{
				InputTokens:  0,
				OutputTokens: 0,
			}
			if resp.PromptEvalCount > 0 {
				usage.InputTokens = int64(resp.PromptEvalCount)
			}
			if resp.EvalCount > 0 {
				usage.OutputTokens = int64(resp.EvalCount)
			}

			// Emit message delta with usage
			s.events = append(s.events, &llm.StreamEvent{
				Type:  llm.StreamEventTypeMessageDelta,
				Delta: nil,
				Usage: usage,
				Done:  false,
			})

			// Emit stop event
			s.events = append(s.events, &llm.StreamEvent{
				Type:  llm.StreamEventTypeStop,
				Delta: nil,
				Usage: usage,
				Done:  true,
			})

			s.done = true
		}

		return nil
	})

	if err != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.err = err
		s.done = true
	}
}


