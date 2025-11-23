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
	cond     *sync.Cond // Condition variable to wait for events
	err      error
	done     bool
	started  bool
	response *api.ChatResponse
}

// newOllamaStream creates a new ollamaStream.
func newOllamaStream(ctx context.Context, client *api.Client, req *api.ChatRequest) *ollamaStream {
	stream := &ollamaStream{
		ctx:     ctx,
		client:  client,
		req:     req,
		events:  make([]*llm.StreamEvent, 0),
		current: -1,
	}
	stream.cond = sync.NewCond(&stream.mu)
	return stream
}

// Next advances to the next event in the stream.
func (s *ollamaStream) Next() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If we haven't started, start the stream in a goroutine
	if !s.started {
		s.started = true
		go s.startStream()
	}

	// Move to next event
	s.current++

	// Wait for events to be available if we've consumed all current events
	// and the stream isn't done yet
	for s.current >= len(s.events) && !s.done && s.err == nil {
		s.cond.Wait()
	}

	// If there's an error or we're done and no more events, return false
	if s.err != nil {
		return false
	}
	if s.done && s.current >= len(s.events) {
		return false
	}

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
	// Emit start event (lock required for shared state access)
	s.mu.Lock()
	s.events = append(s.events, &llm.StreamEvent{
		Type:  llm.StreamEventTypeStart,
		Delta: nil,
		Usage: nil,
		Done:  false,
	})
	s.cond.Broadcast() // Signal that a new event is available
	s.mu.Unlock()

	// Track accumulated content for tool calls
	// Ollama sends incremental deltas (new tokens) in each response, not cumulative content
	var accumulatedText strings.Builder
	var currentToolCall *llm.ToolUseBlock
	var isFirstContentBlock bool = true

	// Call Chat with streaming callback
	err := s.client.Chat(s.ctx, s.req, func(resp api.ChatResponse) error {
		s.mu.Lock()
		defer s.mu.Unlock()

		// Update response
		s.response = &resp

		// Handle message content deltas
		// Ollama sends incremental deltas (just the new token), not cumulative content
		if resp.Message.Content != "" {
			delta := resp.Message.Content
			if delta != "" {
				accumulatedText.WriteString(delta)

				// Emit first content block, then deltas
				if isFirstContentBlock {
					s.events = append(s.events, &llm.StreamEvent{
						Type: llm.StreamEventTypeContentBlock,
						Delta: &llm.StreamDelta{
							Type: llm.StreamDeltaTypeText,
							Text: delta,
						},
						Usage: nil,
						Done:  false,
					})
					isFirstContentBlock = false
				} else {
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
				s.cond.Broadcast() // Signal that a new event is available
			}
		}

		// Handle tool calls
		for _, toolCall := range resp.Message.ToolCalls {
			// Check if this is a new tool call or continuation
			if currentToolCall == nil || currentToolCall.Name != toolCall.Function.Name {
				// New tool call - previous one is already complete (Input already set)
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
				s.cond.Broadcast() // Signal that a new event is available
			}

			// Accumulate tool input (Arguments is a map[string]any)
			// Ollama sends incremental updates, so we merge them directly into the map
			if len(toolCall.Function.Arguments) > 0 {
				// Merge new arguments into existing input map
				if currentToolCall.Input == nil {
					currentToolCall.Input = make(map[string]interface{})
				}
				for k, v := range toolCall.Function.Arguments {
					currentToolCall.Input[k] = v
				}

				// Marshal the current merged state for streaming events
				argsBytes, err := json.Marshal(currentToolCall.Input)
				if err == nil {
					argsStr := string(argsBytes)
					s.events = append(s.events, &llm.StreamEvent{
						Type: llm.StreamEventTypeContentDelta,
						Delta: &llm.StreamDelta{
							Type:      llm.StreamDeltaTypeToolInput,
							ToolInput: argsStr,
						},
						Usage: nil,
						Done:  false,
					})
					s.cond.Broadcast() // Signal that a new event is available
				}
			}
		}

		// Check if done
		if resp.Done {
			// Finish any pending tool call
			// Input is already set from merging arguments above, no need to parse
			if currentToolCall != nil && currentToolCall.Input == nil {
				currentToolCall.Input = make(map[string]interface{})
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
			s.cond.Broadcast() // Signal that a new event is available

			// Emit stop event
			s.events = append(s.events, &llm.StreamEvent{
				Type:  llm.StreamEventTypeStop,
				Delta: nil,
				Usage: usage,
				Done:  true,
			})

			s.done = true
			s.cond.Broadcast() // Signal that stream is done
		}

		return nil
	})

	if err != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.err = err
		s.done = true
		s.cond.Broadcast() // Signal that stream has an error
	}
}
