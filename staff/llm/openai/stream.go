package openai

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/aschepis/backscratcher/staff/llm"
	openai "github.com/sashabaranov/go-openai"
)

// openaiStream implements the llm.Stream interface for OpenAI streaming responses.
type openaiStream struct {
	ctx     context.Context
	stream  *openai.ChatCompletionStream
	events  []*llm.StreamEvent
	current int
	mu      sync.Mutex
	err     error
	done    bool
	started bool
}

// newOpenAIStream creates a new openaiStream.
func newOpenAIStream(ctx context.Context, stream *openai.ChatCompletionStream) *openaiStream {
	return &openaiStream{
		ctx:     ctx,
		stream:  stream,
		events:  make([]*llm.StreamEvent, 0),
		current: -1,
	}
}

// Next advances to the next event in the stream.
func (s *openaiStream) Next() bool {
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
func (s *openaiStream) Event() *llm.StreamEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.current < 0 || s.current >= len(s.events) {
		return nil
	}
	return s.events[s.current]
}

// Err returns any error that occurred during streaming.
func (s *openaiStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close closes the stream and releases resources.
func (s *openaiStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
	if s.stream != nil {
		return s.stream.Close()
	}
	return nil
}

// startStream starts the streaming request and processes responses.
func (s *openaiStream) startStream() {
	// Emit start event
	s.events = append(s.events, &llm.StreamEvent{
		Type:  llm.StreamEventTypeStart,
		Delta: nil,
		Usage: nil,
		Done:  false,
	})

	// Track accumulated content for tool calls
	var accumulatedText strings.Builder
	var currentToolCall *llm.ToolUseBlock
	var toolInputBuilder strings.Builder
	var usage *llm.Usage

	// Process stream events
	for {
		response, err := s.stream.Recv()
		if err != nil {
			// Check if it's EOF or stream end
			if err.Error() == "stream closed" || err.Error() == "EOF" {
				// Stream ended normally
				break
			}
			s.err = err
			s.done = true
			return
		}

		// Process the response
		if len(response.Choices) == 0 {
			continue
		}

		choice := response.Choices[0]

		// Handle content deltas
		if choice.Delta.Content != "" {
			delta := choice.Delta.Content
			accumulatedText.WriteString(delta)

			// Emit content delta event
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

		// Handle tool call deltas
		for _, toolCallDelta := range choice.Delta.ToolCalls {
			// Check if this is a new tool call or continuation
			if toolCallDelta.Index != nil {

				// If we have a current tool call and the index changed, finish the previous one
				if currentToolCall != nil && currentToolCall.ID != toolCallDelta.ID {
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

				// Start new tool call if we don't have one or ID changed
				if currentToolCall == nil || currentToolCall.ID != toolCallDelta.ID {
					if toolCallDelta.ID != "" {
						toolUseID := toolCallDelta.ID
						currentToolCall = &llm.ToolUseBlock{
							ID:    toolUseID,
							Name:  toolCallDelta.Function.Name,
							Input: make(map[string]interface{}),
						}

						// Emit tool use start event
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
				}

				// Accumulate tool input arguments
				if toolCallDelta.Function.Arguments != "" {
					toolInputBuilder.WriteString(toolCallDelta.Function.Arguments)

					// Emit tool input delta
					s.events = append(s.events, &llm.StreamEvent{
						Type: llm.StreamEventTypeContentDelta,
						Delta: &llm.StreamDelta{
							Type:      llm.StreamDeltaTypeToolInput,
							ToolInput: toolCallDelta.Function.Arguments,
						},
						Usage: nil,
						Done:  false,
					})
				}
			}
		}

		// Handle finish reason
		if choice.FinishReason != "" {
			// Finish any pending tool call
			if currentToolCall != nil {
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

			// Extract usage if available
			if response.Usage.TotalTokens > 0 {
				usage = &llm.Usage{
					InputTokens:  int64(response.Usage.PromptTokens),
					OutputTokens: int64(response.Usage.CompletionTokens),
				}
			}

			// Emit message delta with usage
			s.events = append(s.events, &llm.StreamEvent{
				Type:  llm.StreamEventTypeMessageDelta,
				Delta: nil,
				Usage: usage,
				Done:  false,
			}, &llm.StreamEvent{ // Emit stop event
				Type:  llm.StreamEventTypeStop,
				Delta: nil,
				Usage: usage,
				Done:  true,
			})

			s.done = true
			break
		}
	}
}
