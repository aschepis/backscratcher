package memory

import (
	"context"
	"testing"
)

// fakeNormalizerHTTPClient is exercised indirectly via Normalize in integration tests
// that hit the real Anthropic API. Here we only verify basic argument validation and
// contract behavior that does not require a live HTTP call.

func TestNormalizerRejectsEmptyInput(t *testing.T) {
	n := NewNormalizer("claude-3.5-haiku-latest", "dummy", 64)

	if _, _, _, err := n.Normalize(context.Background(), ""); err == nil {
		t.Fatalf("expected error for empty input, got nil")
	}
}
