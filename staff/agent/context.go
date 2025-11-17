package agent

import (
	"context"

	"github.com/aschepis/backscratcher/staff/contextkeys"
)

// WithDebugCallback adds a DebugCallback to the context
func WithDebugCallback(ctx context.Context, cb DebugCallback) context.Context {
	return context.WithValue(ctx, contextkeys.DebugCallbackKey{}, cb)
}

// GetDebugCallback retrieves a DebugCallback from the context.
// Returns the callback and a bool indicating if it was set.
func GetDebugCallback(ctx context.Context) (DebugCallback, bool) {
	cb, ok := ctx.Value(contextkeys.DebugCallbackKey{}).(DebugCallback)
	return cb, ok
}
