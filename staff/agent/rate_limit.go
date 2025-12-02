package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/rs/zerolog"
)

// RateLimitError represents a rate limit error from the Anthropic API
type RateLimitError struct {
	RetryAfter time.Duration
	Message    string
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limit exceeded: %s (retry after %v)", e.Message, e.RetryAfter)
}

// IsRateLimitError checks if an error is a rate limit error (HTTP 429)
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	// Check for common 429 error indicators
	return strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "rate_limit") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "Too Many Requests") ||
		strings.Contains(errStr, "Rate limit exceeded")
}

// ExtractRetryAfter extracts the retry-after duration from an error or HTTP response
func ExtractRetryAfter(err error, resp *http.Response) time.Duration {
	// First, check if it's our custom RateLimitError
	var rateLimitErr *RateLimitError
	if errors.As(err, &rateLimitErr) {
		return rateLimitErr.RetryAfter
	}

	// Check HTTP response headers
	if resp != nil {
		if retryAfterStr := resp.Header.Get("Retry-After"); retryAfterStr != "" {
			if seconds, err := strconv.Atoi(retryAfterStr); err == nil {
				return time.Duration(seconds) * time.Second
			}
			// Try parsing as HTTP date
			if retryTime, err := time.Parse(time.RFC1123, retryAfterStr); err == nil {
				now := time.Now()
				if retryTime.After(now) {
					return retryTime.Sub(now)
				}
			}
		}
	}

	// Default retry after duration if not specified
	return 60 * time.Second
}

// RateLimitHandler handles rate limit errors with exponential backoff using the backoff library
type RateLimitHandler struct {
	maxRetries      uint64
	maxElapsedTime  time.Duration
	stateManager    *StateManager
	onRateLimitFunc func(agentID string, retryAfter time.Duration, attempt int) error
	logger          zerolog.Logger
}

// NewRateLimitHandler creates a new rate limit handler with default settings
func NewRateLimitHandler(logger zerolog.Logger, stateManager *StateManager, onRateLimitFunc func(agentID string, retryAfter time.Duration, attempt int) error) *RateLimitHandler {
	return &RateLimitHandler{
		maxRetries:      5,
		maxElapsedTime:  5 * time.Minute,
		stateManager:    stateManager,
		onRateLimitFunc: onRateLimitFunc,
		logger:          logger.With().Str("component", "rateLimitHandler").Logger(),
	}
}

// CreateBackoff creates a backoff configuration for rate limit retries
// If retryAfter is provided, it uses that as the initial delay, otherwise uses exponential backoff
func (h *RateLimitHandler) CreateBackoff(retryAfter time.Duration) backoff.BackOff {
	var b backoff.BackOff

	if retryAfter > 0 {
		// Use retry-after as initial delay with exponential backoff
		b = backoff.NewExponentialBackOff()
		eb := b.(*backoff.ExponentialBackOff)
		eb.InitialInterval = retryAfter
		eb.Multiplier = 1.5 // Slight increase for subsequent retries
		eb.MaxInterval = 5 * time.Minute
		eb.MaxElapsedTime = h.maxElapsedTime
		eb.RandomizationFactor = 0.1 // 10% jitter
		eb.Reset()
	} else {
		// Standard exponential backoff
		b = backoff.NewExponentialBackOff()
		eb := b.(*backoff.ExponentialBackOff)
		eb.InitialInterval = 1 * time.Second
		eb.Multiplier = 2.0
		eb.MaxInterval = 5 * time.Minute
		eb.MaxElapsedTime = h.maxElapsedTime
		eb.RandomizationFactor = 0.2 // 20% jitter
		eb.Reset()
	}

	// Limit max retries
	return backoff.WithMaxRetries(b, h.maxRetries)
}

// HandleRateLimit handles a rate limit error and returns the next backoff delay
// Returns the retry delay and whether to retry
func (h *RateLimitHandler) HandleRateLimit(ctx context.Context, agentID string, err error, attempt int, resp *http.Response) (time.Duration, bool, error) {
	if !IsRateLimitError(err) {
		return 0, false, nil
	}

	// Extract retry-after from error or response
	retryAfter := ExtractRetryAfter(err, resp)

	// Create backoff strategy
	b := h.CreateBackoff(retryAfter)

	// Get next backoff delay
	nextDelay := b.NextBackOff()

	// Check if we should stop retrying
	if nextDelay == backoff.Stop {
		h.logger.Error().Uint64("max_retries", h.maxRetries).Str("agent_id", agentID).Msg("Max retries or elapsed time exceeded for agent due to rate limits")
		return 0, false, fmt.Errorf("rate limit: max retries or elapsed time exceeded: %w", err)
	}

	// Log rate limit event
	h.logger.Warn().
		Str("agent_id", agentID).
		Int("attempt", attempt+1).
		Uint64("max_retries", h.maxRetries).
		Err(err).
		Dur("next_delay", nextDelay).
		Msg("Rate limit encountered for agent. Retrying after delay")

	// Call callback if set
	if h.onRateLimitFunc != nil {
		if callbackErr := h.onRateLimitFunc(agentID, nextDelay, attempt); callbackErr != nil {
			h.logger.Warn().Err(callbackErr).Msg("Rate limit callback failed")
		}
	}

	return nextDelay, true, nil
}

// ScheduleRetryWithNextWake schedules an agent retry using the next_wake mechanism
// This is useful for scheduled agents that hit rate limits
func (h *RateLimitHandler) ScheduleRetryWithNextWake(agentID string, delay time.Duration) error {
	if h.stateManager == nil {
		return fmt.Errorf("state manager not available for scheduling retry")
	}

	nextWake := time.Now().Add(delay)

	// Set agent to waiting_external state with next_wake
	if err := h.stateManager.SetStateWithNextWake(agentID, StateWaitingExternal, &nextWake); err != nil {
		return fmt.Errorf("failed to schedule retry: %w", err)
	}

	h.logger.Info().
		Str("agent_id", agentID).
		Int64("next_wake_unix", nextWake.Unix()).
		Str("next_wake_human", nextWake.Format("2006-01-02 15:04:05")).
		Dur("delay", delay).
		Msg("Scheduled agent retry via next_wake")
	return nil
}

// WaitForRetry waits for the specified delay, respecting context cancellation
func (h *RateLimitHandler) WaitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
