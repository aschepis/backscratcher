package llm

import (
	"errors"
	"time"
)

// Error represents a provider-neutral LLM error.
type Error struct {
	Type        ErrorType
	Message     string
	Retryable   bool
	RetryAfter  *time.Duration
	StatusCode  int
	ProviderErr error // Original provider-specific error
}

// ErrorType represents the category of error.
type ErrorType string

const (
	ErrorTypeRateLimit       ErrorType = "rate_limit"
	ErrorTypeRequestTooLarge ErrorType = "request_too_large"
	ErrorTypeInvalidRequest  ErrorType = "invalid_request"
	ErrorTypeProvider        ErrorType = "provider"
	ErrorTypeNetwork         ErrorType = "network"
	ErrorTypeTimeout         ErrorType = "timeout"
	ErrorTypeUnknown         ErrorType = "unknown"
)

// Error implements the error interface.
func (e *Error) Error() string {
	if e.ProviderErr != nil {
		return e.Message + ": " + e.ProviderErr.Error()
	}
	return e.Message
}

// Unwrap returns the underlying provider error.
func (e *Error) Unwrap() error {
	return e.ProviderErr
}

// IsRateLimitError checks if an error is a rate limit error.
func IsRateLimitError(err error) bool {
	var llmErr *Error
	if errors.As(err, &llmErr) {
		return llmErr.Type == ErrorTypeRateLimit
	}
	return false
}

// IsRequestTooLargeError checks if an error is a request too large error.
func IsRequestTooLargeError(err error) bool {
	var llmErr *Error
	if errors.As(err, &llmErr) {
		return llmErr.Type == ErrorTypeRequestTooLarge
	}
	return false
}

// IsRetryableError checks if an error is retryable.
func IsRetryableError(err error) bool {
	var llmErr *Error
	if errors.As(err, &llmErr) {
		return llmErr.Retryable
	}
	return false
}

// ExtractRetryAfter extracts the retry-after duration from an error.
func ExtractRetryAfter(err error) *time.Duration {
	var llmErr *Error
	if errors.As(err, &llmErr) {
		return llmErr.RetryAfter
	}
	return nil
}

// NewRateLimitError creates a new rate limit error.
func NewRateLimitError(message string, retryAfter *time.Duration, providerErr error) *Error {
	return &Error{
		Type:        ErrorTypeRateLimit,
		Message:     message,
		Retryable:   true,
		RetryAfter:  retryAfter,
		ProviderErr: providerErr,
	}
}

// NewRequestTooLargeError creates a new request too large error.
func NewRequestTooLargeError(message string, providerErr error) *Error {
	return &Error{
		Type:        ErrorTypeRequestTooLarge,
		Message:     message,
		Retryable:   true,
		ProviderErr: providerErr,
	}
}

// NewProviderError creates a new provider error.
func NewProviderError(message string, providerErr error) *Error {
	return &Error{
		Type:        ErrorTypeProvider,
		Message:     message,
		Retryable:   false,
		ProviderErr: providerErr,
	}
}
