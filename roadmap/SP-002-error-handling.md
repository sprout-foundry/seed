# SP-002: Error Handling & Retry

**Status:** 📋 Spec  
**Location:** `core/errors.go`  
**Size:** 1 file, ~15 lines  
**Test Files:** 0

## Current State

Four sentinel errors are defined but never returned:

```go
var (
    ErrInterrupted     = errors.New("conversation interrupted")
    ErrMaxIterations   = errors.New("maximum iterations exceeded")
    ErrNoProvider      = errors.New("no provider configured")
    ErrNoExecutor      = errors.New("no tool executor configured")
)
```

Two `fmt.Errorf` calls are the only error paths in `ProcessQuery`:
1. `fmt.Errorf("agent is paused; call Resume() before Run()")`
2. `fmt.Errorf("LLM request failed: %w", err)`

No typed error hierarchy. No retry logic. No backoff. No error classification.

## Architecture

### What's Missing

A typed error hierarchy that enables intelligent retry/recovery decisions. The sprout project (SP-008) defines the target:

```go
type TransientError struct {       // Retry with backoff
    Op       string
    Provider string
    RetryAfter time.Duration
    Wrapped  error
}

type RateLimitError struct {        // Retry with provider-specific backoff
    Provider   string
    RetryAfter time.Duration
    Attempt    int
    Wrapped    error
}

type ContextOverflowError struct {   // Compact and retry
    TokensUsed  int
    TokensLimit int
    Wrapped     error
}

type AuthError struct {             // Re-auth or prompt user
    Provider string
    Wrapped  error
}
```

### Error Classification

Provider errors need classification before retry decisions:

| Error Pattern | Type | Action |
|---|---|---|
| `"timeout"` / `"deadline exceeded"` | `TransientError` | Retry with backoff |
| `"429"` / `"rate limit"` | `RateLimitError` | Retry with provider-specific backoff |
| `"401"` / `"unauthorized"` | `AuthError` | Stop, prompt user |
| `"context window"` / `"maximum context"` | `ContextOverflowError` | Compact, retry |
| `"500"` / `"502"` / `"503"` | `TransientError` | Retry with backoff |
| Everything else | `TransientError` | Retry once, then fail |

### Retry/Backoff

```go
type BackoffStrategy struct {
    InitialDelay  time.Duration
    MaxDelay      time.Duration
    Multiplier    float64
    MaxAttempts   int
    Jitter        bool
}

func (b *BackoffStrategy) NextAttempt(attempt int) time.Duration
```

### Retry Logic in ProcessQuery

```go
for iter := 0; iter < maxIterations; iter++ {
    // ... prepare messages ...

    // Retry loop for provider calls
    var resp *ChatResponse
    var err error
    for attempt := 0; attempt <= backoff.MaxAttempts; attempt++ {
        resp, err = provider.Chat(ctx, req)
        if err == nil {
            break
        }
        if !isRetryable(err) {
            return "", err  // AuthError, etc. — fail fast
        }
        if attempt < backoff.MaxAttempts {
            time.Sleep(backoff.NextAttempt(attempt))
            publishRetryEvent(eventBus, err, attempt)
        }
    }
    if err != nil {
        return "", err
    }
    // ... continue loop ...
}
```

## Proposed Solution

### Phase 1: Typed Errors

Create `core/errors.go` with typed error hierarchy:

```go
// TransientError indicates a temporary failure that may succeed on retry.
type TransientError struct {
    Op         string
    Provider   string
    RetryAfter time.Duration
    Wrapped    error
}

// RateLimitError indicates the provider has rate-limited requests.
type RateLimitError struct {
    Provider   string
    RetryAfter time.Duration
    Attempt    int
    Wrapped    error
}

// ContextOverflowError indicates the context window is exceeded.
type ContextOverflowError struct {
    TokensUsed  int
    TokensLimit int
    Wrapped     error
}

// AuthError indicates authentication failure.
type AuthError struct {
    Provider string
    Wrapped  error
}
```

### Phase 2: Error Classification

Create `classifyError(err error, provider string) error` that wraps raw errors:

```go
func classifyError(err error, provider string) error {
    msg := strings.ToLower(err.Error())
    switch {
    case strings.Contains(msg, "429"), strings.Contains(msg, "rate limit"):
        return &RateLimitError{Provider: provider, Wrapped: err}
    case strings.Contains(msg, "401"), strings.Contains(msg, "unauthorized"):
        return &AuthError{Provider: provider, Wrapped: err}
    case strings.Contains(msg, "context window"), strings.Contains(msg, "maximum context"):
        return &ContextOverflowError{Wrapped: err}
    case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
        return &TransientError{Op: "chat", Provider: provider, Wrapped: err}
    default:
        return &TransientError{Op: "chat", Provider: provider, Wrapped: err}
    }
}
```

### Phase 3: Retry Logic

Add retry with backoff to `ProcessQuery`:

```go
func (ch *ConversationHandler) chatWithRetry(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
    backoff := NewExponentialBackoff(500*time.Millisecond, 5*time.Second, 3)
    var lastErr error
    for attempt := 0; attempt <= backoff.MaxAttempts; attempt++ {
        resp, err := ch.agent.provider.Chat(ctx, req)
        if err == nil {
            return resp, nil
        }
        classified := classifyError(err, ch.agent.provider.Info().Model)
        lastErr = classified

        // Publish error event
        if ch.agent.eventBus != nil {
            ch.agent.eventBus.Publish(events.EventTypeError, ErrorEvent("LLM request", classified))
        }

        // Fail fast on non-retryable errors
        var authErr *AuthError
        if errors.As(classified, &authErr) {
            return nil, classified
        }

        if attempt < backoff.MaxAttempts {
            delay := backoff.NextAttempt(attempt)
            ch.agent.debugLog("[retry] Attempt %d failed: %v, retrying in %v\n", attempt+1, classified, delay)
            time.Sleep(delay)
        }
    }
    return nil, lastErr
}
```

### Phase 4: Use Sentinel Errors

Replace `fmt.Errorf` calls with sentinel errors:

```go
// Paused check
if ch.agent.paused {
    return "", ErrPaused  // new sentinel
}

// Max iterations
if !completed && ch.agent.maxIterations > 0 {
    return finalContent, ErrMaxIterations  // actually return it
}

// Context cancellation
select {
case <-ctx.Done():
    return "", ErrInterrupted  // actually return it
default:
}
```

## Success Criteria

| Metric | Target |
|---|---|
| Typed error types defined | 4+ (Transient, RateLimit, ContextOverflow, Auth) |
| Provider errors classified | 100% of provider errors wrapped in typed errors |
| Retry on transient errors | 3 attempts with exponential backoff |
| Fail fast on auth errors | No retry on AuthError |
| Error events published | Every error path publishes EventTypeError |
| Sentinel errors used | ErrInterrupted, ErrMaxIterations actually returned |

## Key Files

| File | Action |
|---|---|
| `core/errors.go` | Modify: add typed error hierarchy |
| `core/conversation.go` | Modify: add classifyError, chatWithRetry, ctx.Done() check |
| `core/backoff.go` | Create: exponential backoff implementation |
| `test/mock_provider.go` | Modify: add error injection helpers |
| `test/e2e_test.go` | Modify: add retry/backoff tests |
