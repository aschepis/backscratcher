package agent

import (
	"context"

	ctxpkg "github.com/aschepis/backscratcher/staff/context"
)

// WithDebugCallback adds a DebugCallback to the context
func WithDebugCallback(ctx context.Context, cb DebugCallback) context.Context {
	return ctxpkg.WithDebugCallback(ctx, cb)
}

// GetDebugCallback retrieves a DebugCallback from the context.
// Returns the callback and a bool indicating if it was set.
func GetDebugCallback(ctx context.Context) (DebugCallback, bool) {
	cb, ok := ctxpkg.GetDebugCallback(ctx)
	return DebugCallback(cb), ok
}
