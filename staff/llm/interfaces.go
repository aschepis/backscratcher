package llm

import (
	"context"
)

// Client provides a provider-neutral interface for making LLM API calls.
// Implementations should handle provider-specific details internally.
type Client interface {
	// Synchronous sends a request and returns a complete response.
	// This is for non-streaming use cases.
	Synchronous(ctx context.Context, req *Request) (*Response, error)

	// Stream sends a request and returns a stream of events.
	// The caller should read from the returned Stream until it's done or an error occurs.
	Stream(ctx context.Context, req *Request) (Stream, error)
}

// Stream represents a streaming response from an LLM.
type Stream interface {
	// Next advances to the next event in the stream.
	// Returns false when the stream is complete or an error occurs.
	Next() bool

	// Event returns the current event.
	// Should only be called after Next() returns true.
	Event() *StreamEvent

	// Err returns any error that occurred during streaming.
	Err() error

	// Close closes the stream and releases resources.
	Close() error
}

// ToolCodec provides encoding and decoding of tool calls and results.
// This allows providers to work with tool data without knowing provider-specific formats.
type ToolCodec interface {
	// EncodeToolUse encodes a ToolUseBlock into provider-specific format.
	EncodeToolUse(block *ToolUseBlock) (interface{}, error)

	// DecodeToolUse decodes provider-specific tool use data into a ToolUseBlock.
	DecodeToolUse(data interface{}) (*ToolUseBlock, error)

	// EncodeToolResult encodes a ToolResultBlock into provider-specific format.
	EncodeToolResult(block *ToolResultBlock) (interface{}, error)

	// DecodeToolResult decodes provider-specific tool result data into a ToolResultBlock.
	DecodeToolResult(data interface{}) (*ToolResultBlock, error)

	// EncodeToolSpec encodes a ToolSpec into provider-specific format.
	EncodeToolSpec(spec *ToolSpec) (interface{}, error)
}

// Middleware provides hooks for decorating Client calls.
// This allows adding cross-cutting concerns like logging, retry, rate limiting, etc.
type Middleware interface {
	// BeforeRequest is called before making an API request.
	// It can modify the request or return an error to abort the request.
	BeforeRequest(ctx context.Context, req *Request) (*Request, error)

	// AfterResponse is called after receiving a response.
	// It can modify the response or return an error.
	AfterResponse(ctx context.Context, req *Request, resp *Response) (*Response, error)

	// OnError is called when an error occurs.
	// It can return a modified error or nil to use the original error.
	OnError(ctx context.Context, req *Request, err error) error
}

// StreamMiddleware provides hooks for decorating streaming calls.
type StreamMiddleware interface {
	// BeforeStream is called before starting a stream.
	BeforeStream(ctx context.Context, req *Request) (*Request, error)

	// OnStreamEvent is called for each stream event.
	// It can modify the event or return an error to abort the stream.
	OnStreamEvent(ctx context.Context, req *Request, event *StreamEvent) (*StreamEvent, error)

	// OnStreamError is called when a stream error occurs.
	OnStreamError(ctx context.Context, req *Request, err error) error
}

// MiddlewareFunc is a function type that implements Middleware.
type MiddlewareFunc struct {
	BeforeRequestFunc func(ctx context.Context, req *Request) (*Request, error)
	AfterResponseFunc func(ctx context.Context, req *Request, resp *Response) (*Response, error)
	OnErrorFunc       func(ctx context.Context, req *Request, err error) error
}

// BeforeRequest calls the BeforeRequestFunc if set.
func (f MiddlewareFunc) BeforeRequest(ctx context.Context, req *Request) (*Request, error) {
	if f.BeforeRequestFunc != nil {
		return f.BeforeRequestFunc(ctx, req)
	}
	return req, nil
}

// AfterResponse calls the AfterResponseFunc if set.
func (f MiddlewareFunc) AfterResponse(ctx context.Context, req *Request, resp *Response) (*Response, error) {
	if f.AfterResponseFunc != nil {
		return f.AfterResponseFunc(ctx, req, resp)
	}
	return resp, nil
}

// OnError calls the OnErrorFunc if set.
func (f MiddlewareFunc) OnError(ctx context.Context, req *Request, err error) error {
	if f.OnErrorFunc != nil {
		return f.OnErrorFunc(ctx, req, err)
	}
	return err
}

// StreamMiddlewareFunc is a function type that implements StreamMiddleware.
type StreamMiddlewareFunc struct {
	BeforeStreamFunc  func(ctx context.Context, req *Request) (*Request, error)
	OnStreamEventFunc func(ctx context.Context, req *Request, event *StreamEvent) (*StreamEvent, error)
	OnStreamErrorFunc func(ctx context.Context, req *Request, err error) error
}

// BeforeStream calls the BeforeStreamFunc if set.
func (f StreamMiddlewareFunc) BeforeStream(ctx context.Context, req *Request) (*Request, error) {
	if f.BeforeStreamFunc != nil {
		return f.BeforeStreamFunc(ctx, req)
	}
	return req, nil
}

// OnStreamEvent calls the OnStreamEventFunc if set.
func (f StreamMiddlewareFunc) OnStreamEvent(ctx context.Context, req *Request, event *StreamEvent) (*StreamEvent, error) {
	if f.OnStreamEventFunc != nil {
		return f.OnStreamEventFunc(ctx, req, event)
	}
	return event, nil
}

// OnStreamError calls the OnStreamErrorFunc if set.
func (f StreamMiddlewareFunc) OnStreamError(ctx context.Context, req *Request, err error) error {
	if f.OnStreamErrorFunc != nil {
		return f.OnStreamErrorFunc(ctx, req, err)
	}
	return err
}

// WrapWithMiddleware wraps a Client with middleware and returns a new Client.
// This allows adding cross-cutting concerns like logging, retry logic, rate limiting, etc.
// without exposing the implementation details of middleware wrapping.
func WrapWithMiddleware(client Client, middleware ...Middleware) Client {
	if len(middleware) == 0 {
		return client
	}
	return &clientWithMiddleware{
		client:     client,
		middleware: middleware,
	}
}

// clientWithMiddleware wraps a Client with middleware.
type clientWithMiddleware struct {
	client     Client
	middleware []Middleware
}

// Synchronous implements Client.Synchronous with middleware support.
func (c *clientWithMiddleware) Synchronous(ctx context.Context, req *Request) (*Response, error) {
	// Apply BeforeRequest middleware
	for _, mw := range c.middleware {
		var err error
		req, err = mw.BeforeRequest(ctx, req)
		if err != nil {
			return nil, err
		}
	}

	// Make the actual request
	resp, err := c.client.Synchronous(ctx, req)
	if err != nil {
		// Apply OnError middleware
		for _, mw := range c.middleware {
			err = mw.OnError(ctx, req, err)
			if err == nil {
				break // Middleware handled the error
			}
		}
		return nil, err
	}

	// Apply AfterResponse middleware
	for i := len(c.middleware) - 1; i >= 0; i-- {
		var err error
		resp, err = c.middleware[i].AfterResponse(ctx, req, resp)
		if err != nil {
			return nil, err
		}
	}

	return resp, nil
}

// Stream implements Client.Stream with middleware support.
func (c *clientWithMiddleware) Stream(ctx context.Context, req *Request) (Stream, error) {
	// Apply BeforeStream middleware (if any middleware implements StreamMiddleware)
	for _, mw := range c.middleware {
		if smw, ok := mw.(StreamMiddleware); ok {
			var err error
			req, err = smw.BeforeStream(ctx, req)
			if err != nil {
				return nil, err
			}
		}
	}

	// Create the stream
	stream, err := c.client.Stream(ctx, req)
	if err != nil {
		// Apply OnStreamError middleware
		for _, mw := range c.middleware {
			if smw, ok := mw.(StreamMiddleware); ok {
				err = smw.OnStreamError(ctx, req, err)
				if err == nil {
					break
				}
			}
		}
		return nil, err
	}

	// Wrap the stream with middleware
	return &streamWithMiddleware{
		stream:     stream,
		middleware: c.middleware,
		req:        req,
		ctx:        ctx,
	}, nil
}

// streamWithMiddleware wraps a Stream with middleware.
type streamWithMiddleware struct {
	stream     Stream
	middleware []Middleware
	req        *Request
	ctx        context.Context
	event      *StreamEvent
}

// Next implements Stream.Next with middleware support.
func (s *streamWithMiddleware) Next() bool {
	if !s.stream.Next() {
		return false
	}

	event := s.stream.Event()
	if event == nil {
		return false
	}

	// Apply OnStreamEvent middleware
	for _, mw := range s.middleware {
		if smw, ok := mw.(StreamMiddleware); ok {
			var err error
			event, err = smw.OnStreamEvent(s.ctx, s.req, event)
			if err != nil {
				return false
			}
			if event == nil {
				return false
			}
		}
	}

	s.event = event
	return true
}

// Event implements Stream.Event.
func (s *streamWithMiddleware) Event() *StreamEvent {
	return s.event
}

// Err implements Stream.Err.
func (s *streamWithMiddleware) Err() error {
	err := s.stream.Err()
	if err != nil {
		// Apply OnStreamError middleware
		for _, mw := range s.middleware {
			if smw, ok := mw.(StreamMiddleware); ok {
				err = smw.OnStreamError(s.ctx, s.req, err)
				if err == nil {
					break
				}
			}
		}
	}
	return err
}

// Close implements Stream.Close.
func (s *streamWithMiddleware) Close() error {
	return s.stream.Close()
}

// Ensure streamWithMiddleware implements Stream
var _ Stream = (*streamWithMiddleware)(nil)

// Ensure clientWithMiddleware implements Client
var _ Client = (*clientWithMiddleware)(nil)
