package anthropic

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/aschepis/backscratcher/staff/llm"
)

// anthropicStream implements the llm.Stream interface for Anthropic streaming responses.
type anthropicStream struct {
	ctx     context.Context
	stream  *ssestream.Stream[anthropic.MessageStreamEventUnion]
	events  []*llm.StreamEvent
	current int
	mu      sync.Mutex
	cond    *sync.Cond // Condition variable to wait for events
	err     error
	done    bool
	started bool
}

// newAnthropicStream creates a new anthropicStream.
func newAnthropicStream(ctx context.Context, stream *ssestream.Stream[anthropic.MessageStreamEventUnion]) *anthropicStream {
	as := &anthropicStream{
		ctx:     ctx,
		stream:  stream,
		events:  make([]*llm.StreamEvent, 0),
		current: -1,
	}
	as.cond = sync.NewCond(&as.mu)
	return as
}

// Next advances to the next event in the stream.
func (s *anthropicStream) Next() bool {
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
func (s *anthropicStream) Event() *llm.StreamEvent {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.current < 0 || s.current >= len(s.events) {
		return nil
	}
	return s.events[s.current]
}

// Err returns any error that occurred during streaming.
func (s *anthropicStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close closes the stream and releases resources.
func (s *anthropicStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
	if s.stream != nil {
		return s.stream.Close()
	}
	return nil
}

// startStream starts the streaming request and processes responses.
func (s *anthropicStream) startStream() {
	// Emit start event
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
	var currentToolCall *llm.ToolUseBlock
	var toolInputBuilder strings.Builder
	var usage *llm.Usage

	// Process stream events
	for s.stream.Next() {
		event := s.stream.Current()

		s.mu.Lock()

		// Process different event types from Anthropic stream using AsAny() to get the variant
		switch evt := event.AsAny().(type) {
		case anthropic.MessageStartEvent:
			// Message start - no action needed, already emitted start event

		case anthropic.ContentBlockStartEvent:
			// Content block start
			// Check the content block type
			if contentBlock := evt.ContentBlock.AsAny(); contentBlock != nil {
				switch block := contentBlock.(type) {
				case anthropic.TextBlock:
					// Text block starting - no action needed yet
				case anthropic.ToolUseBlock:
					// Tool use block starting
					toolUseID := block.ID
					toolName := block.Name
					currentToolCall = &llm.ToolUseBlock{
						ID:    toolUseID,
						Name:  toolName,
						Input: make(map[string]interface{}),
					}
					toolInputBuilder.Reset()

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
			}

		case anthropic.ContentBlockDeltaEvent:
			// Content block delta
			delta := evt.Delta.AsAny()
			if delta != nil {
				switch d := delta.(type) {
				case anthropic.TextDelta:
					// Text delta
					if d.Text != "" {
						s.events = append(s.events, &llm.StreamEvent{
							Type: llm.StreamEventTypeContentDelta,
							Delta: &llm.StreamDelta{
								Type: llm.StreamDeltaTypeText,
								Text: d.Text,
							},
							Usage: nil,
							Done:  false,
						})
						s.cond.Broadcast() // Signal that a new event is available
					}
				case anthropic.InputJSONDelta:
					// Tool input delta
					if currentToolCall != nil && d.PartialJSON != "" {
						toolInputBuilder.WriteString(d.PartialJSON)
						s.events = append(s.events, &llm.StreamEvent{
							Type: llm.StreamEventTypeContentDelta,
							Delta: &llm.StreamDelta{
								Type:      llm.StreamDeltaTypeToolInput,
								ToolInput: d.PartialJSON,
							},
							Usage: nil,
							Done:  false,
						})
						s.cond.Broadcast() // Signal that a new event is available
					}
				}
			}

		case anthropic.ContentBlockStopEvent:
			// Content block stop
			if currentToolCall != nil {
				// Finish tool call by parsing accumulated input
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
				currentToolCall = nil
			}

		case anthropic.MessageDeltaEvent:
			// Message delta - contains usage information
			usage = &llm.Usage{
				InputTokens:              evt.Usage.InputTokens,
				OutputTokens:             evt.Usage.OutputTokens,
				CacheCreationInputTokens: evt.Usage.CacheCreationInputTokens,
				CacheReadInputTokens:     evt.Usage.CacheReadInputTokens,
			}

		case anthropic.MessageStopEvent:
			// Message stop - finish any pending tool call
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
			s.mu.Unlock()
			return
		}

		s.mu.Unlock()
	}

	// Check for stream errors after loop ends
	if err := s.stream.Err(); err != nil {
		s.mu.Lock()
		s.err = err
		s.done = true
		s.cond.Broadcast() // Signal that stream has an error
		s.mu.Unlock()
		return
	}

	// If we exit the loop without an error but also without a stop event,
	// mark as done (this shouldn't happen normally, but handle gracefully)
	s.mu.Lock()
	if !s.done {
		s.done = true
		s.cond.Broadcast() // Signal that stream is done
	}
	s.mu.Unlock()
}
