# SP-005: Context Cancellation

**Status:** 📋 Spec  
**Location:** `core/conversation.go`, `core/agent.go`  
**Size:** ~0 lines (not implemented)  
**Test Files:** 0

## Current State

The `ctx context.Context` parameter is passed to `provider.Chat()` and `executor.Execute()`, but `ctx.Done()` is never checked in the main loop. A cancelled context only surfaces if the provider/executor happens to check it internally.

The `ErrInterrupted` sentinel error is defined but never returned.

## Architecture

### What's Missing

```go
// In ProcessQuery main loop:
for iter := 0; iter < maxIterations; iter++ {
    // Missing: check for context cancellation
    select {
    case <-ctx.Done():
        return "", ErrInterrupted
    default:
    }
    // ... rest of loop ...
}
```

### Interrupt Support

The sprout agent has:
- `interruptCtx` / `interruptCancel` on the Agent struct
- `inputInjectionChan` for injecting prompts into a running conversation
- `ctx.Done()` checked in the main loop

Seed has none of this.

### Proposed Solution

#### Phase 1: Check ctx.Done()

Add context cancellation check at the top of the ProcessQuery loop:

```go
for iter := 0; ch.agent.maxIterations == 0 || iter < ch.agent.maxIterations; iter++ {
    select {
    case <-ctx.Done():
        return "", ErrInterrupted
    default:
    }
    // ... rest of loop ...
}
```

#### Phase 2: Interrupt Support

Add interrupt capability to Agent:

```go
type Agent struct {
    // ... existing fields ...
    interruptCtx    context.Context
    interruptCancel context.CancelFunc
}

func (a *Agent) Interrupt() {
    if a.interruptCancel != nil {
        a.interruptCancel()
    }
}

func NewAgent(opts Options) *Agent {
    // ... existing init ...
    a.interruptCtx, a.interruptCancel = context.WithCancel(context.Background())
    return a
}
```

Use `interruptCtx` as the base context for `Run()`:

```go
func (a *Agent) Run(ctx context.Context, query string) (string, error) {
    // Combine caller's ctx with interrupt ctx
    ctx = a.interruptCtx
    ch := newConversationHandler(a)
    return ch.ProcessQuery(ctx, query)
}
```

#### Phase 3: Input Injection

Add input injection channel for mid-conversation user input:

```go
type Agent struct {
    // ... existing fields ...
    inputInjectionChan  chan string
    inputInjectionMutex sync.Mutex
}

func (a *Agent) InjectInput(input string) {
    select {
    case a.inputInjectionChan <- input:
    default:
        // Channel full, drop
    }
}

// In ProcessQuery loop, after tool execution:
select {
case injected := <-ch.agent.inputInjectionChan:
    // Treat as new user message, restart loop
    ch.agent.state.AddMessage(Message{Role: "user", Content: injected})
    continue
default:
}
```

## Success Criteria

| Metric | Target |
|---|---|
| ctx.Done() checked in loop | ✅ |
| ErrInterrupted returned on cancellation | ✅ |
| Agent.Interrupt() method | ✅ |
| Input injection channel | ✅ |
| Tests for cancellation and injection | ✅ |

## Key Files

| File | Action |
|---|---|
| `core/conversation.go` | Modify: add ctx.Done() check, input injection |
| `core/agent.go` | Modify: add interruptCtx, InjectInput |
| `core/errors.go` | Modify: actually use ErrInterrupted |
| `test/e2e_test.go` | Modify: add cancellation and injection tests |
