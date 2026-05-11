package core

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Sentinel errors returned by the agent lifecycle.
var (
	// ErrInterrupted is returned when the conversation is interrupted by the user.
	ErrInterrupted = errors.New("conversation interrupted")

	// ErrMaxIterations is returned when the maximum iteration count is exceeded.
	ErrMaxIterations = errors.New("maximum iterations exceeded")

	// ErrNoProvider is returned when Run is called without a Provider.
	ErrNoProvider = errors.New("no provider configured")

	// ErrNoExecutor is returned when Run is called without a ToolExecutor.
	ErrNoExecutor = errors.New("no tool executor configured")

	// ErrPaused is returned when Run is called while the agent is paused.
	ErrPaused = errors.New("agent is paused")
)

// TransientError indicates a temporary failure that may succeed on retry.
type TransientError struct {
	// Op is the operation that failed (e.g. "chat", "stream").
	Op string
	// Provider is the provider that returned the error.
	Provider string
	// RetryAfter is a suggested delay before retrying (zero means use default backoff).
	RetryAfter time.Duration
	// Wrapped is the underlying error.
	Wrapped error
}

func (e *TransientError) Error() string {
	base := "transient error"
	if e.Op != "" {
		base += " during " + e.Op
	}
	if e.Provider != "" {
		base += " (" + e.Provider + ")"
	}
	if e.Wrapped != nil {
		base += ": " + e.Wrapped.Error()
	}
	return base
}

func (e *TransientError) Unwrap() error { return e.Wrapped }

// IsTransient reports whether err is a TransientError.
func IsTransient(err error) bool {
	var t *TransientError
	return errors.As(err, &t)
}

// RateLimitError indicates the provider has rate-limited requests.
type RateLimitError struct {
	// Provider is the provider that returned the rate limit error.
	Provider string
	// RetryAfter is a suggested delay before retrying (from Retry-After header or similar).
	RetryAfter time.Duration
	// Attempt is the request attempt number when the limit was hit.
	Attempt int
	// Wrapped is the underlying error.
	Wrapped error
}

func (e *RateLimitError) Error() string {
	base := "rate limit exceeded"
	if e.Provider != "" {
		base += " (" + e.Provider + ")"
	}
	if e.Attempt > 0 {
		base += ", attempt " + strconv.Itoa(e.Attempt)
	}
	if e.Wrapped != nil {
		base += ": " + e.Wrapped.Error()
	}
	return base
}

func (e *RateLimitError) Unwrap() error { return e.Wrapped }

// IsRateLimit reports whether err is a RateLimitError.
func IsRateLimit(err error) bool {
	var r *RateLimitError
	return errors.As(err, &r)
}

// ContextOverflowError indicates the context window is exceeded.
type ContextOverflowError struct {
	// TokensUsed is the number of tokens that were estimated or used.
	TokensUsed int
	// TokensLimit is the provider's context window limit.
	TokensLimit int
	// Wrapped is the underlying error.
	Wrapped error
}

func (e *ContextOverflowError) Error() string {
	base := "context window exceeded"
	if e.TokensUsed > 0 || e.TokensLimit > 0 {
		base += fmt.Sprintf(" (%d/%d tokens)", e.TokensUsed, e.TokensLimit)
	}
	if e.Wrapped != nil {
		base += ": " + e.Wrapped.Error()
	}
	return base
}

func (e *ContextOverflowError) Unwrap() error { return e.Wrapped }

// IsContextOverflow reports whether err is a ContextOverflowError.
func IsContextOverflow(err error) bool {
	var c *ContextOverflowError
	return errors.As(err, &c)
}

// AuthError indicates authentication failure with a provider.
type AuthError struct {
	// Provider is the provider that returned the auth error.
	Provider string
	// Wrapped is the underlying error.
	Wrapped error
}

func (e *AuthError) Error() string {
	base := "authentication failed"
	if e.Provider != "" {
		base += " (" + e.Provider + ")"
	}
	if e.Wrapped != nil {
		base += ": " + e.Wrapped.Error()
	}
	return base
}

func (e *AuthError) Unwrap() error { return e.Wrapped }

// IsAuthError reports whether err is an AuthError.
func IsAuthError(err error) bool {
	var a *AuthError
	return errors.As(err, &a)
}
