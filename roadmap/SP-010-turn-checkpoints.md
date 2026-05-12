# SP-010: Turn Checkpoints

**Status:** 📋 Spec
**Location:** `core/turn_checkpoints.go`
**Size:** ~0 lines (not implemented)
**Test Files:** 0

## Current State

Seed's `Compactor` mentions "checkpoint compaction" but has no actual `TurnCheckpoint` struct, no async recording, no actionable summaries, and no checkpoint-based message reconstruction. The compactor's "checkpoint" phase just looks for user-assistant pairs and string-truncates them.

Sprout has a full turn checkpoint system with `TurnCheckpoint` structs, async recording, Go-generated summaries, actionable summaries, and efficient checkpoint-based compaction that replaces completed turns with their summaries.

## Architecture

### What's Missing

A checkpoint system that records a compact summary for every completed conversation turn, enabling efficient compaction that preserves key context without keeping full message history.

### TurnCheckpoint Struct

```go
type TurnCheckpoint struct {
    StartIndex        int    `json:"start_index"`
    EndIndex          int    `json:"end_index"`
    Summary           string `json:"summary"`
    ActionableSummary string `json:"actionable_summary,omitempty"`
}
```

- `StartIndex` / `EndIndex`: message array indices for the turn range
- `Summary`: compact narrative of what happened in the turn
- `ActionableSummary`: bullet list of what was accomplished (files, commands, results)

### Summary Builder (Go-only, no LLM)

Extract key facts from a turn's messages:

```
- User question (truncated to 120 chars)
- Tools called (deduplicated names)
- Files modified (deduplicated paths)
- Errors encountered (count + first error)
- Final answer (truncated to 200 chars)
```

**Actionable summary** format:
```
- Modified: src/agent.go, src/types.go
- Ran: go test ./..., go build ./...
- Result: 2 tests fixed, build passing
```

### Async Recording

Checkpoint recording happens after `finalize()` and should not block the response:

```go
func (a *Agent) RecordTurnCheckpointAsync(startIndex, endIndex int) {
    // Snapshot messages immediately
    turnMessages := append([]Message(nil), a.state.Messages()[startIndex:endIndex+1]...)
    go func() {
        summary := buildGoSummary(turnMessages)
        actionable := buildActionableSummary(turnMessages)
        checkpoint := TurnCheckpoint{
            StartIndex:        startIndex,
            EndIndex:          endIndex,
            Summary:           summary,
            ActionableSummary: actionable,
        }
        a.state.AddCheckpoint(checkpoint)
    }()
}
```

The message snapshot ensures the background goroutine sees a consistent view even if the main thread mutates state.

### Checkpoint-Based Compaction

Replace completed turn ranges with their summary messages:

```go
func BuildCheckpointCompactedMessages(messages []Message, checkpoints []TurnCheckpoint) ([]Message, []TurnCheckpoint)
```

Algorithm:
1. Sort checkpoints by StartIndex
2. For each consumed checkpoint: keep messages before it, insert summary as `assistant` message, skip the turn range
3. Remaining (unconsumed) checkpoints have their indices shifted by the cumulative delta
4. Handle boundary: if summary message + next message are both `assistant` with no tool calls, merge/deduplicate

### Index Shifting

When compaction removes messages, all checkpoint indices must be updated:

```go
func shiftCheckpoints(checkpoints []TurnCheckpoint, delta int) {
    for i := range checkpoints {
        checkpoints[i].StartIndex += delta
        checkpoints[i].EndIndex += delta
    }
}
```

### Integration Points

1. **After finalize()**: Record checkpoint for the completed turn (async)
   - `startIndex` = query start message index (set when user message added)
   - `endIndex` = last message index

2. **In prepareMessages()**: Use checkpoint-compacted messages before sending to provider

3. **In ExportState/ImportState**: Checkpoints are serialized as part of state

4. **In Compactor**: Checkpoint compaction is the first strategy tried, before structural/emergency

## Implementation Phases

### Phase 1: Checkpoint Structure (Week 1)

- Define `TurnCheckpoint` struct
- Add `[]TurnCheckpoint` to `State` with mutex-protected access
- Add serialization to ExportState/ImportState

### Phase 2: Summary Builder (Week 1)

- Implement Go-only summary builder from message sequences
- Implement actionable summary builder
- Add word limit enforcement

### Phase 3: Recording & Compaction (Week 1-2)

- Add async recording with message snapshot
- Implement `BuildCheckpointCompactedMessages`
- Add index shifting for remaining checkpoints
- Handle consecutive-assistant boundary case

### Phase 4: Integration (Week 2)

- Wire recording into ConversationHandler.finalize()
- Use checkpoint-compacted messages in prepareMessages()
- Add `queryStartIndex` tracking to ConversationHandler
- E2e tests

## Success Criteria

| Metric | Target |
|--------|--------|
| TurnCheckpoint struct | StartIndex, EndIndex, Summary, ActionableSummary |
| Go-only summaries | No LLM calls in summary generation |
| Async recording | Non-blocking, message-snapshot based |
| Checkpoint compaction | Replace turn ranges with summaries, preserve ordering |
| Index shifting | Remaining checkpoints updated after compaction |
| Serialization | Checkpoints survive ExportState/ImportState |
| Boundary safety | No consecutive assistant messages after compaction |

## Key Files

| File | Action |
|------|--------|
| `core/turn_checkpoints.go` | Create: TurnCheckpoint, recording, compaction, shifting |
| `core/state.go` | Modify: add checkpoint storage |
| `core/compaction.go` | Modify: use checkpoints as first compaction strategy |
| `core/conversation.go` | Modify: record checkpoint after finalize, track queryStartIndex |
| `test/e2e_test.go` | Add: checkpoint recording, compaction, index shifting tests |
