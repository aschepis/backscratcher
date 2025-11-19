// Package llm provides a provider-neutral abstraction layer for Large Language Model (LLM) APIs.
//
// This package defines common types, interfaces, and utilities that allow the codebase
// to work with multiple LLM providers (Anthropic, OpenAI, Ollama, etc.) without being
// tightly coupled to any specific provider's SDK.
//
// # Core Concepts
//
//  1. Messages: The Message type represents a conversation message with role (user, assistant, system)
//     and content blocks (text, tool use, tool results).
//
//  2. Tools: The ToolSpec type represents a tool definition that can be provided to an LLM,
//     and ToolUseBlock/ToolResultBlock represent tool invocations and their results.
//
//  3. Client Interface: The Client interface provides Synchronous() for non-streaming calls
//     and Stream() for streaming calls. Implementations handle provider-specific details.
//
//  4. ToolCodec Interface: The ToolCodec interface allows encoding/decoding tool calls
//     between provider-neutral and provider-specific formats.
//
//  5. Middleware: The Middleware and StreamMiddleware interfaces allow adding cross-cutting
//     concerns like logging, retry logic, rate limiting, etc. without modifying provider implementations.
//
//  6. Errors: The Error type provides provider-neutral error handling with support for
//     rate limits, retryable errors, and provider-specific error details.
//
// Usage Example
//
//	// Create a provider-specific client (e.g., Anthropic)
//	client := anthropic.NewClient(...)
//
//	// Wrap with middleware
//	client := llm.WrapWithMiddleware(
//	    baseClient,
//	    loggingMiddleware,
//	    retryMiddleware,
//	)
//
//	// Make a request
//	req := &llm.Request{
//	    Model: "claude-3-5-sonnet-20241022",
//	    Messages: []llm.Message{
//	        llm.NewTextMessage(llm.RoleUser, "Hello!"),
//	    },
//	}
//
//	resp, err := client.Synchronous(ctx, req)
//
// # Extension Points
//
// To add a new LLM provider:
//  1. Implement the Client interface
//  2. Implement the ToolCodec interface (if tool support is needed)
//  3. Translate between provider-specific types and llm package types
//  4. Handle provider-specific errors and translate to llm.Error types
//
// To add middleware:
//  1. Implement the Middleware or StreamMiddleware interface
//  2. Use WrapWithMiddleware to wrap your Client with middleware
//  3. The returned Client can be used anywhere a Client is expected
package llm
