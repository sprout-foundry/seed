# SP-014: Context Compaction Hardening

**Status:** 📋 Spec
**Priority:** High — current compaction loses all middle context, degrading agent performance on long conversations

## Problem

Seed's compaction system has two independent mechanisms that don't work together:

1. **Checkpoint system** (`BuildCheckpointCompactedMessages`) replaces old turns with summaries in `prepareMessages()`
2. **Compactor** (`Compact()`) runs in `compactMessages()` when over context limit with three phases: checkpoint, structural, emergency

When combined, `structuralCompact` discards all middle content (including checkpoint summaries) and replaces it with a metadata-only summary like `"5 user messages, 12 tool calls"`. The LLM loses all context about what happened. Additionally, `ShiftCheckpointIndices` is never called in production, so checkpoints become stale after structural compaction.

### Root Causes

| # | Issue | Severity |
|---|-------|----------|
| 1 | `structuralCompact` replaces all middle content with metadata-only summary | Critical |
| 2 | `ShiftCheckpointIndices` never called in production; checkpoint indices go stale | Critical |
| 3 | `truncateHead` keeps beginning, drops end of messages | High |
| 4 | Emergency truncation can break turn boundaries | Medium |
| 5 | Compactor and checkpoint system are independent | Architectural |

## Design

### Guiding Principles

1. **Recent context is king** — the last 24 messages must always be preserved in full
2. **Older context is summarized, not discarded** — use checkpoint summaries; never replace them with metadata-only summaries
3. **Drop oldest first** — when under pressure, remove the oldest checkpoints one at a time
4. **Preserve turn structure** — never orphan tool results or break user/assistant pairs

### Checkpoint Summary Identification

Add a `Meta` field to `Message` to mark checkpoint summaries reliably (no string matching):

```go
type Message struct {
    // ... existing fields ...
    Meta map[string]string `json:"-"`
}
```

`BuildCheckpointCompactedMessages` sets `Meta["checkpoint"] = "true"` on inserted summary messages. The compaction algorithm checks this field.

### New Compaction Algorithm

Replace the three-phase Compactor with a single `Compact()` function:

```
Phase 1: Trim oversized tool results (safe, preserves all messages)
Phase 2: Drop oldest checkpoint summaries one at a time (Meta["checkpoint"] == "true")
Phase 3: Truncate content of oldest non-recent messages (head+tail, not head-only)
Phase 4: Drop oldest complete turns (preserves structure)
```

Each phase checks the token limit and exits early if satisfied.

### Checkpoint Summary Content

Switch from `Summary` (narrative, ~100-200 chars) to `ActionableSummary` (bullet list with file paths, commands, errors) with a 500-char guard. If `ActionableSummary` exceeds 500 chars, fall back to `Summary`.

### Checkpoint Garbage Collection

Defer to future work. Checkpoints accumulate in state but become non-consumable naturally as indices drift. Per-drop GC requires mapping between prepared-message indices and state checkpoint indices, which is complex and error-prone.

### Remove `ShiftCheckpointIndices`

Delete `checkpoint_shifting.go` entirely. Checkpoint indices reference `state.Messages()` which only appends. Compaction operates on a transient prepared list, never modifies state. The function is dead code.

## Implementation Steps

### Step 1: Core Compaction Rewrite

1. Add `Meta map[string]string` to `Message` (`json:"-"` tag)
2. Update `BuildCheckpointCompactedMessages` to set `Meta["checkpoint"] = "true"` on summary messages
3. Update `BuildCheckpointCompactedMessages` to use `ActionableSummary` with 500-char guard
4. Rewrite `Compact()` in `compaction.go`:
   - Remove `structuralCompact`, `compactTurns`, `summarizeTurn`, `checkpointCompact`
   - Remove `Compactor` struct and `NewCompactor`
   - Add `dropOldestCheckpointSummaries`, `truncateOldContentHeadTail`, `dropOldestTurns`
   - Convert to package-level function
5. Update `compactMessages()` in `conversation.go` to call new `Compact()`

### Step 2: Cleanup

1. Delete `checkpoint_shifting.go` and its tests
2. Increase truncation limits in `TurnSummaryBuilder` (user question 200→300, response 150→250, result 200→300)
3. Update `CompactionResult.Strategy` values: `"tool_trim"`, `"checkpoint_drop"`, `"truncation"`, `"emergency"`

### Step 3: Tests

1. Rewrite `compaction_test.go` for new algorithm
2. Add e2e test: long conversation → recent turns intact, old turns summarized
3. Add e2e test: conversation exceeding context after checkpoint compaction → oldest summaries dropped
4. Remove `ShiftCheckpointIndices` tests from `turn_checkpoints_test.go`

## File Changes

| File | Change |
|------|--------|
| `core/types.go` | Add `Meta map[string]string` to `Message` |
| `core/compaction.go` | Rewrite: remove Compactor struct, structuralCompact, compactTurns, summarizeTurn, checkpointCompact. Add dropOldestCheckpointSummaries, truncateOldContentHeadTail, dropOldestTurns. Convert to package-level Compact(). |
| `core/conversation.go` | Update `compactMessages()` to call package-level `Compact()` |
| `core/checkpoint_compaction.go` | Set `Meta["checkpoint"] = "true"` on summary messages. Use `ActionableSummary` with 500-char guard. |
| `core/checkpoint_shifting.go` | **Delete** |
| `core/turn_summary.go` | Increase truncation limits |
| `core/compaction_test.go` | Rewrite tests |
| `core/turn_checkpoints_test.go` | Remove `ShiftCheckpointIndices` tests |

## Risks

1. **`Meta` field on `Message`** — mitigated by `json:"-"` tag and nil-by-default map
2. **Checkpoint summaries still lose detail** — inherent to compaction; tradeoff is acceptable
3. **Dropping checkpoints means losing all context for that turn** — no middle ground; future improvement could add partial truncation of checkpoint summaries
4. **Strategy value changes** — consumers likely only check `!= "none"`; document the change

## Success Criteria

1. Recent 24 messages always preserved in full
2. Old turns replaced with actionable summaries (not metadata-only)
3. When still over limit, oldest summaries dropped one at a time
4. Emergency truncation preserves head+tail of content
5. Turn boundaries never broken during emergency dropping
6. `ShiftCheckpointIndices` deleted with no functional impact
7. `make check` passes
