package context

import (
	stdctx "context"
)

// debugCallbackKey is the type used as a context key for storing debug callbacks.
// This is in a separate package to avoid circular dependencies.
type debugCallbackKey struct{}

// WithDebugCallback adds a debug callback function to the context.
// The callback function should accept a string message parameter.
func WithDebugCallback(ctx stdctx.Context, cb func(string)) stdctx.Context {
	return stdctx.WithValue(ctx, debugCallbackKey{}, cb)
}

// GetDebugCallback retrieves a debug callback function from the context.
// Returns the callback and a bool indicating if it was set.
func GetDebugCallback(ctx stdctx.Context) (func(string), bool) {
	cb, ok := ctx.Value(debugCallbackKey{}).(func(string))
	return cb, ok
}
