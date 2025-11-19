package llm

import (
	"errors"
	"testing"
	"time"
)

func TestIsRateLimitError(t *testing.T) {
	err := NewRateLimitError("rate limit exceeded", nil, nil)
	if !IsRateLimitError(err) {
		t.Error("Expected IsRateLimitError to return true for rate limit error")
	}

	regularErr := NewProviderError("some error", nil)
	if IsRateLimitError(regularErr) {
		t.Error("Expected IsRateLimitError to return false for non-rate-limit error")
	}
}

func TestIsRequestTooLargeError(t *testing.T) {
	err := NewRequestTooLargeError("request too large", nil)
	if !IsRequestTooLargeError(err) {
		t.Error("Expected IsRequestTooLargeError to return true for request too large error")
	}

	regularErr := NewProviderError("some error", nil)
	if IsRequestTooLargeError(regularErr) {
		t.Error("Expected IsRequestTooLargeError to return false for non-request-too-large error")
	}
}

func TestIsRetryableError(t *testing.T) {
	retryableErr := NewRateLimitError("rate limit", nil, nil)
	if !IsRetryableError(retryableErr) {
		t.Error("Expected IsRetryableError to return true for retryable error")
	}

	nonRetryableErr := NewProviderError("some error", nil)
	if IsRetryableError(nonRetryableErr) {
		t.Error("Expected IsRetryableError to return false for non-retryable error")
	}
}

func TestExtractRetryAfter(t *testing.T) {
	retryAfter := 5 * time.Minute
	err := NewRateLimitError("rate limit", &retryAfter, nil)
	extracted := ExtractRetryAfter(err)
	if extracted == nil {
		t.Fatal("Expected non-nil retry after")
	}
	if *extracted != retryAfter {
		t.Errorf("Expected retry after %v, got %v", retryAfter, *extracted)
	}

	regularErr := NewProviderError("some error", nil)
	if ExtractRetryAfter(regularErr) != nil {
		t.Error("Expected nil retry after for non-rate-limit error")
	}
}

func TestErrorUnwrap(t *testing.T) {
	originalErr := errors.New("original error")
	wrappedErr := NewProviderError("wrapped", originalErr)
	if !errors.Is(wrappedErr, originalErr) {
		t.Error("Expected error to unwrap to original error")
	}
}
