package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/rs/zerolog"
)

const (
	// DefaultRetryAfter is the default retry-after duration if not specified
	DefaultRetryAfter = 60 * time.Second
	// DefaultMaxRetries is the default maximum number of retries
	DefaultMaxRetries = 5
	// DefaultMaxElapsedTime is the default maximum elapsed time for backoff
	DefaultMaxElapsedTime = 5 * time.Minute
	// DefaultMaxInterval is the default maximum interval for backoff
	DefaultMaxInterval = 5 * time.Minute
	// DefaultInitialDelay is the default initial delay for exponential backoff
	DefaultInitialDelay = 1 * time.Second
	// RetryAfterMultiplier is the multiplier for retry-after based backoff
	RetryAfterMultiplier = 1.5
	// RetryAfterRandomizationFactor is the randomization factor for retry-after based backoff
	RetryAfterRandomizationFactor = 0.1
	// StandardMultiplier is the multiplier for standard exponential backoff
	StandardMultiplier = 2.0
	// StandardRandomizationFactor is the randomization factor for standard exponential backoff
	StandardRandomizationFactor = 0.2
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
// It first checks for properly wrapped errors, then falls back to string matching
// for backwards compatibility with errors that aren't properly wrapped.
// TODO: remove backwards compatibility support once migrated/tested
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	// First, check for our custom RateLimitError type
	var rateLimitErr *RateLimitError
	if errors.As(err, &rateLimitErr) {
		return true
	}

	// Check for llm.Error with rate limit type
	// Note: We can't import llm package here to avoid circular dependency,
	// so we check the error string for the type indicator
	// TODO: fix circular dependency issue
	errStr := err.Error()
	if strings.Contains(errStr, "rate_limit") {
		return true
	}

	// Fall back to string matching for backwards compatibility
	// Check for common 429 error indicators
	return strings.Contains(errStr, "429") ||
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
			if seconds, parseErr := strconv.Atoi(retryAfterStr); parseErr == nil {
				return time.Duration(seconds) * time.Second
			}
			// Try parsing as HTTP date
			if retryTime, parseErr := time.Parse(time.RFC1123, retryAfterStr); parseErr == nil {
				now := time.Now()
				if retryTime.After(now) {
					return retryTime.Sub(now)
				}
			}
		}
	}

	// Default retry after duration if not specified
	return DefaultRetryAfter
}

// RateLimitCallback is called when a rate limit is encountered
type RateLimitCallback func(agentID string, retryAfter time.Duration, attempt int) error

// RateLimitHandler handles rate limit errors with exponential backoff using the backoff library
type RateLimitHandler struct {
	maxRetries      uint64
	maxElapsedTime  time.Duration
	stateManager    *StateManager
	onRateLimitFunc RateLimitCallback
	logger          zerolog.Logger
	// agentBackoffs stores backoff instances per agent to preserve state across retries
	agentBackoffs map[string]backoff.BackOff
	mu            sync.RWMutex
}

// NewRateLimitHandler creates a new rate limit handler with default settings
// TODO: look to replace this with an existing library in open source community
func NewRateLimitHandler(logger zerolog.Logger, stateManager *StateManager, onRateLimitFunc RateLimitCallback) *RateLimitHandler {
	return &RateLimitHandler{
		maxRetries:      DefaultMaxRetries,
		maxElapsedTime:  DefaultMaxElapsedTime,
		stateManager:    stateManager,
		onRateLimitFunc: onRateLimitFunc,
		logger:          logger.With().Str("component", "rateLimitHandler").Logger(),
		agentBackoffs:   make(map[string]backoff.BackOff),
	}
}

// CreateBackoff creates a backoff configuration for rate limit retries
// If retryAfter is provided, it uses that as the initial delay, otherwise uses exponential backoff
func (h *RateLimitHandler) CreateBackoff(retryAfter time.Duration) backoff.BackOff {
	eb := backoff.NewExponentialBackOff()

	if retryAfter > 0 {
		// Use retry-after as initial delay with exponential backoff
		eb.InitialInterval = retryAfter
		eb.Multiplier = RetryAfterMultiplier
		eb.RandomizationFactor = RetryAfterRandomizationFactor
	} else {
		// Standard exponential backoff
		eb.InitialInterval = DefaultInitialDelay
		eb.Multiplier = StandardMultiplier
		eb.RandomizationFactor = StandardRandomizationFactor
	}

	eb.MaxInterval = DefaultMaxInterval
	eb.MaxElapsedTime = h.maxElapsedTime
	eb.Reset()

	// Limit max retries
	return backoff.WithMaxRetries(eb, h.maxRetries)
}

// getOrCreateBackoff gets an existing backoff for an agent or creates a new one
// This preserves backoff state across retries for the same agent
func (h *RateLimitHandler) getOrCreateBackoff(agentID string, retryAfter time.Duration) backoff.BackOff {
	h.mu.Lock()
	defer h.mu.Unlock()

	if b, exists := h.agentBackoffs[agentID]; exists {
		return b
	}

	b := h.CreateBackoff(retryAfter)
	h.agentBackoffs[agentID] = b
	return b
}

// HandleRateLimit handles a rate limit error and returns the next backoff delay
// Returns the retry delay and whether to retry
func (h *RateLimitHandler) HandleRateLimit(ctx context.Context, agentID string, err error, attempt int, resp *http.Response) (time.Duration, bool, error) {
	if !IsRateLimitError(err) {
		return 0, false, nil
	}

	// Extract retry-after from error or response
	retryAfter := ExtractRetryAfter(err, resp)

	// Get or create backoff strategy (preserves state across retries)
	b := h.getOrCreateBackoff(agentID, retryAfter)

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
