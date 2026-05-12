# SP-008: Conversation Optimizer

**Status:** 📋 Spec
**Location:** `core/conversation_optimizer.go`
**Size:** ~0 lines (not implemented)
**Test Files:** 0

## Current State

Seed has a static `Compactor` (418 lines) that uses heuristic rules to reduce messages: checkpoint compaction, structural compaction, and emergency truncation. This works but is crude — it doesn't track file reads or shell commands for deduplication, so redundant context wastes tokens on every API call.

Sprout's `ConversationOptimizer` (1217 lines) provides stateful, context-aware deduplication. Only the lightweight dedup pass belongs in seed — the heavy layered compaction and LLM summary builder are application-level concerns handled by the existing Compactor + turn checkpoints.

## Architecture

### What's Missing

A stateful optimizer that runs before every API call to reduce redundant context.

### Optimizations

| Optimization | What it does |
|-------------|--------------|
| File read dedup | Keep only the latest read of each filepath; earlier reads become `[Earlier file read: {path}]` |
| Shell command dedup | For transient commands (ls, find, pwd, cat, echo, head, tail, wc), keep only latest output |
| Identical result dedup | Collapse consecutive identical tool results |

### Interface Design

```go
type FileReadRecord struct {
    FilePath     string
    ContentHash  string
    Timestamp    time.Time
    MessageIndex int
}

type ShellCommandRecord struct {
    Command      string
    OutputHash   string
    Timestamp    time.Time
    MessageIndex int
    IsTransient  bool
}

type ConversationOptimizer struct {
    fileReads     map[string]*FileReadRecord
    shellCommands map[string]*ShellCommandRecord
    enabled       bool
    debug         bool
    knownToolFn   func(name string) bool
}

func NewConversationOptimizer(opts OptimizerOptions) *ConversationOptimizer
func (o *ConversationOptimizer) OptimizeConversation(messages []Message) []Message
func (o *ConversationOptimizer) IsEnabled() bool
```

### Integration with Compactor

The optimizer runs *before* the existing `Compactor`:

```go
func (ch *ConversationHandler) prepareMessages() []Message {
    messages := ch.agent.state.Messages()

    // Optional optimizer (opt-in)
    if ch.agent.optimizer != nil && ch.agent.optimizer.IsEnabled() {
        messages = ch.agent.optimizer.OptimizeConversation(messages)
    }

    // ... existing system prompt prep, image stripping ...

    // Token check → compact if needed (existing Compactor)
    if tokenEstimate > contextSize {
        messages = ch.compactMessages(messages, contextSize)
    }
    return messages
}
```

The optimizer is opt-in via `Options.Optimizer` on Agent.

## Implementation Phases

### Phase 1: File Read & Shell Tracking (Week 1)

- Track file reads by filepath with content hashing
- Track shell commands with transient detection
- Replace earlier reads/executions with summaries

### Phase 2: Integration & Testing (Week 1-2)

- Wire into `prepareMessages()` before static compactor
- Add opt-in configuration to Agent Options
- E2e tests: redundant reads → deduplicated

## Success Criteria

| Metric | Target |
|--------|--------|
| File read dedup | Only latest read per filepath retained |
| Shell dedup | Transient commands deduplicated |
| Opt-in | Zero cost when not enabled |
| Falls through to Compactor | Static compaction still works |

## Key Files

| File | Action |
|------|--------|
| `core/conversation_optimizer.go` | Create: ConversationOptimizer with dedup logic |
| `core/conversation.go` | Modify: wire optimizer into prepareMessages |
| `core/agent.go` | Modify: add Optimizer option |
| `test/e2e_test.go` | Add: dedup tests |
