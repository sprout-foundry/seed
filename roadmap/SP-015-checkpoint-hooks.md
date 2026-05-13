# Checkpoint Hooks Design

## Goal

Enable consumers (like the test harness) to intercept turn checkpoints as they're created so they can:
- Embed checkpoint summaries for semantic search
- Build a "memory" tool that lets the model query past work
- Track which user messages led to which outcomes

## Current Flow

```
runLoop() completes turn
    → finalize()
        → RecordTurnCheckpointAsync(state, turnMessages, startIdx, endIdx, timeout)
            → goroutine: TurnSummaryBuilder.Build(turnMessages)
            → state.AddCheckpoint(checkpoint)
```

The checkpoint is computed asynchronously and stored in state. No one else knows about it.

## Checkpoint Data

Each `TurnCheckpoint` contains:

| Field | Content | Use Case |
|-------|---------|----------|
| `StartIndex` / `EndIndex` | Message range in state | Retrieve full turn messages |
| `UserMessage` | Original user query (truncated to 2000 chars) | **Primary embedding target** — find "what did the user ask that led to this outcome?" |
| `Summary` | Narrative: "User asked X. Used tools Y. Modified files Z. Response: W." | Embedding target (concise) |
| `ActionableSummary` | Bullet list: "- Question: X\n- Modified: path.go\n- Command: go build\n- Result: W" | Embedding target (detailed) |

## Proposed: `OnCheckpoint` Callback

Add a callback to `Options` that fires synchronously when a checkpoint is built.

### Why a callback, not an event?

- Events go through `EventPublisher` which is already used for UI telemetry. Checkpoint data is structured and rich — it's not a telemetry event.
- A callback gives the consumer the full `TurnCheckpoint` struct, not a flattened `map[string]any`.
- The harness can decide what to do (embed, index, store) without the core package knowing about those concerns.
- Fire-and-forget: if the callback panics, it's caught (same as `OnIteration`).

### Why synchronous, not async?

- `TurnSummaryBuilder.Build()` is fast (string parsing, no I/O). In testing it takes microseconds.
- The async goroutine is only needed because we don't want to block the conversation loop. But the callback is fire-and-forget — it doesn't need to complete before we return from `finalize()`.
- The consumer (harness) can do async work *inside* the callback if it wants.

## Implementation

### 1. Add `OnCheckpoint` to `Options`

```go
type Options struct {
    // ... existing fields ...

    // OnCheckpoint is called after each completed turn with the built
    // TurnCheckpoint. The checkpoint contains the summary, actionable
    // summary, and message range. The callback is invoked synchronously
    // from finalize() — keep it fast. If it panics, the panic is caught
    // and the agent continues (same behavior as OnIteration).
    OnCheckpoint func(TurnCheckpoint)
}
```

### 2. Store on `Agent`

```go
type Agent struct {
    // ... existing fields ...
    onCheckpoint func(TurnCheckpoint)
}
```

Wire in `NewAgent`:
```go
onCheckpoint: opts.OnCheckpoint,
```

### 3. Fire in `finalize()`

Build the checkpoint synchronously, fire the callback, then record async:

```go
func (ch *ConversationHandler) finalize(query string) (string, error) {
    // ... existing finalContent logic ...

    if ch.turnCompleted && ch.queryStartIndex >= 0 && ch.turnEndIndex >= ch.queryStartIndex {
        if ch.turnEndIndex < len(messages) {
            turnMessages := messages[ch.queryStartIndex : ch.turnEndIndex+1]

            // Build checkpoint synchronously for the callback.
            cp := BuildCheckpointSummary(turnMessages)
            cp.StartIndex = ch.queryStartIndex
            cp.EndIndex = ch.turnEndIndex

            // Fire callback (fire-and-forget, panic-safe).
            if ch.agent.onCheckpoint != nil {
                func() {
                    defer func() {
                        if r := recover(); r != nil {
                            ch.agent.debugLog("[!!] OnCheckpoint callback panicked: %v\n", r)
                        }
                    }()
                    ch.agent.onCheckpoint(cp)
                }()
            }

            // Record in state asynchronously (existing behavior).
            RecordTurnCheckpointAsync(ch.agent.state, turnMessages,
                ch.queryStartIndex, ch.turnEndIndex, 5*time.Second)
        }
    }

    // ... existing event publishing ...
}
```

**Key detail:** We build the checkpoint synchronously for the callback, then let the async goroutine build it again for state. This is fine — `BuildCheckpointSummary` is fast (string parsing only, no I/O). The async path exists because we don't want to block the conversation loop on summary computation. The callback path is fire-and-forget, so it doesn't block either.

**Alternative:** If we want to avoid double computation, we could build once synchronously and pass the result to `RecordTurnCheckpointAsync`. But that changes the async contract. The double-build is acceptable because it's fast.

### 4. Add `Agent.Checkpoints()` convenience method

Currently consumers must do `agent.State().GetCheckpoints()`. Add a direct accessor:

```go
// Checkpoints returns a copy of all recorded turn checkpoints.
func (a *Agent) Checkpoints() []TurnCheckpoint {
    return a.state.GetCheckpoints()
}
```

## Harness Usage Example

The harness could build a semantic search index of past turns:

```go
// In harness setup:
var checkpointIndex []CheckpointEntry

agent, _ := core.NewAgent(core.Options{
    Provider: mockProvider,
    Executor: mockExecutor,
    OnCheckpoint: func(cp core.TurnCheckpoint) {
        // Embed the user message for semantic search (best recall for "what did the user ask?").
        embedding := embed(cp.UserMessage)
        checkpointIndex = append(checkpointIndex, CheckpointEntry{
            Checkpoint: cp,
            Embedding:  embedding,
        })
    },
})

// Later: search for relevant past turns
func (h *Harness) SearchPastTurns(query string) []core.TurnCheckpoint {
    qEmbed := embed(query)
    // cosine similarity search against checkpointIndex
    // return top-K matching checkpoints
}
```

Or build a "memory" tool:

```go
// Tool: "search_memory" — search past turns by semantic similarity
executor.AddTool(core.ToolConfig{
    Name: "search_memory",
    Handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
        query := args["query"].(string)
        results := harness.SearchPastTurns(query)
        // Return formatted summaries of matching past turns
    },
})
```

## What the Harness Gets Per Checkpoint

From `TurnCheckpoint`:

| Field | Content | Use Case |
|-------|---------|----------|
| `Summary` | Narrative: "User asked X. Used tools Y. Modified files Z. Response: W." | Embedding target (concise) |
| `ActionableSummary` | Bullet list: "- Question: X\n- Modified: path.go\n- Command: go build\n- Result: W" | Embedding target (detailed) |
| `StartIndex` / `EndIndex` | Message range in state | Retrieve full turn messages from state |

The harness can choose which field to embed. `ActionableSummary` is more detailed (file paths, commands) but uses more tokens. `Summary` is more concise.

## Risks

1. **Callback panics** — mitigated by panic recovery (same as `OnIteration`)
2. **Callback is slow** — consumer's problem; core doesn't wait for it
3. **Double checkpoint computation** — `BuildCheckpointSummary` is called twice (once for callback, once async). Acceptable because it's fast (string parsing, no I/O)

## Success Criteria

1. `OnCheckpoint` fires after each completed turn with the built checkpoint
2. `OnCheckpoint` is nil-safe (no-op when not set)
3. `OnCheckpoint` panics are caught and don't crash the agent
4. `Agent.Checkpoints()` returns all recorded checkpoints
5. Harness can build semantic search index from checkpoint data
6. `make check` passes
