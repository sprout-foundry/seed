# SP-009: Configuration, Steering & Extensibility

**Status:** 📋 Spec
**Location:** `core/conversation.go`, `core/agent.go`
**Size:** ~0 lines (not implemented)
**Test Files:** 0

## Current State

Seed has a fixed set of `Options` fields set at construction time. After `NewAgent()`, the agent's behavior is locked in — retry attempts, backoff delays, and provider are immutable. There is no way for the consumer to steer the conversation by injecting context between turns, and no hooks to observe or intervene at iteration boundaries.

## Architecture

### What's Missing

Three categories of extensibility:

1. **Configurable retry/backoff** — consumer tunes retry behavior
2. **Steering** — consumer injects context that takes effect on the next API call
3. **Lifecycle hooks** — consumer observes or intervenes at iteration boundaries

### 1. Configurable Retry & Backoff

Retry and backoff parameters should be configurable via `Options`:

```go
type RetryConfig struct {
    MaxAttempts   int           // max retry attempts per API call (0 = no retry, default: 3)
    InitialDelay  time.Duration // first retry delay (default: 500ms)
    MaxDelay      time.Duration // cap on delay growth (default: 5s)
    Multiplier    float64       // exponential growth factor (default: 2.0)
    Jitter        float64       // jitter: 0 = none, >= 1 = full jitter (default: 0.5)
}
```

Added to `Options.RetryConfig`. When nil, defaults are used.

### 2. Steering — Inject Context for Next API Call

The consumer can inject a message that will be prepended to the next API request. Unlike `InjectInput()` (which injects a user message mid-loop), steering lets the consumer inject any role (system, user, assistant) with any content, and it takes effect on the *next* `provider.Chat()` call.

```go
// Steer prepends a message to the next API request. The message is consumed
// once (transient) and does not pollute persistent state. Multiple calls to
// Steer queue messages in FIFO order.
func (a *Agent) Steer(msg Message)

// SteerSystem is a convenience for injecting a system-level steering message.
func (a *Agent) SteerSystem(content string)
```

Internally, this uses the existing `enqueueTransientMessage()` mechanism but exposes it publicly. The transient messages are appended after the normal message history in `prepareMessages()`, so they appear as the most recent context before the API call.

**Use cases:**
- Consumer adjusts system prompt mid-session: `agent.SteerSystem("Focus on performance, not correctness.")`
- Consumer injects external context: `agent.Steer(Message{Role: "user", Content: "Note: the database schema changed at 2pm."})`
- Consumer corrects course: `agent.Steer(Message{Role: "user", Content: "Ignore the previous approach. Try using X instead."})`

### 3. Provider Switching

The provider should be swappable at runtime:

```go
func (a *Agent) SetProvider(p Provider)
```

This updates `a.provider` so subsequent `Run()` calls use the new provider. The consumer is responsible for ensuring the new provider is compatible with the current message history (e.g., vision images for a non-vision model).

### 4. Compaction Event

When compaction runs, publish an event so the consumer can observe it:

```go
// In compaction path:
if ch.agent.eventBus != nil {
    ch.agent.eventBus.Publish("compaction", map[string]interface{}{
        "strategy":     "checkpoint" | "structural" | "emergency",
        "messages_before": len(before),
        "messages_after":  len(after),
        "tokens_saved":    estimatedTokensSaved,
    })
}
```

### 5. Pre/Post Iteration Hooks

Optional callbacks the consumer can set to observe or intervene at iteration boundaries:

```go
type Options struct {
    // ... existing fields ...
    OnIteration func(iteration int, messages int) // called at start of each iteration
}
```

The callback receives the iteration number and current message count. If the callback returns an error (or a boolean `false`), the loop can abort. For simplicity, start with a fire-and-forget callback; if consumers need abort capability, add `OnIteration func(...) error` in a follow-up.

## Implementation Phases

### Phase 1: Retry Config & Provider Switching (Week 1)

- Add `RetryConfig` to `Options`
- Wire retry config into the retry loop in `ProcessQuery`
- Add `SetProvider()` method

### Phase 2: Steering (Week 1)

- Expose `Steer(msg Message)` and `SteerSystem(content string)` on Agent
- Wire into `prepareMessages()` via existing transient message mechanism
- Add e2e test for steering

### Phase 3: Hooks & Compaction Event (Week 1-2)

- Add `OnIteration` callback to Options
- Publish compaction event
- Add e2e tests

## Success Criteria

| Metric | Target |
|--------|--------|
| Retry configurable | Consumer sets attempts, delays, multiplier, jitter |
| Steering works | `Steer()` prepends transient message to next API call |
| Provider swappable | `SetProvider()` changes provider for subsequent calls |
| Compaction visible | Event published with strategy and message count delta |
| Iteration hook | `OnIteration` called at start of each loop iteration |

## Key Files

| File | Action |
|------|--------|
| `core/agent.go` | Modify: add RetryConfig, SetProvider, Steer, SteerSystem, OnIteration |
| `core/conversation.go` | Modify: wire retry config, compaction event, iteration hook |
| `test/e2e_test.go` | Add: steering, provider switch, retry config, compaction event tests |
