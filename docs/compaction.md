# Compaction

Context compaction reduces conversation history to fit within the provider's context window. It operates in `prepareMessages()` → `compactMessages()` when estimated tokens exceed the context size.

## Turn Checkpoints

Turn checkpoints are summaries of completed conversation turns, stored in `State` for use during compaction.

- **Struct**: `TurnCheckpoint` (`core/types.go`, `core/turn_summary.go`)
  - `StartIndex` / `EndIndex` — indices into `state.Messages()` defining the turn range
  - `UserMessage` — original user query (truncated to 2000 chars)
  - `Summary` — narrative description of what happened
  - `ActionableSummary` — bullet list of accomplishments (file paths, commands, results)
- **Storage**: kept in `State.checkpoints` slice; exported/imported with `AgentState`
- **Building**: synchronous in `finalize()` (`core/finalize.go`). Calls `TurnSummaryBuilder.Build()` on the turn messages, then `state.AddCheckpoint()`. Only recorded when `turnCompleted == true`.
- **Turn data extraction**: `TurnSummaryBuilder` (`core/turn_summary.go`) parses assistant/tool messages to extract file reads, file modifications, shell commands, errors, and final response. Uses configurable tool-name sets and error patterns with sensible defaults.
- **Async recording**: `RecordTurnCheckpointAsync()` (`core/checkpoint_compaction.go`) spawns a goroutine for summary computation with a timeout fallback to a minimal checkpoint. Fires `OnCheckpoint` callback in its own goroutine with panic recovery.

## Checkpoint Compaction

`BuildCheckpointCompactedMessages()` (`core/checkpoint_compaction.go`) replaces consumed checkpoint ranges with summary messages.

- Runs in `prepareMessages()` before any other transformation
- Processes checkpoints oldest-first (sorted by `StartIndex`)
- A checkpoint is **consumable** when: indices are valid within the messages slice, `StartIndex <= EndIndex`, and `Summary` is non-empty
- Overlapping checkpoint ranges are rejected
- **Content selection**: prefers `ActionableSummary` if non-empty and ≤ 500 bytes; falls back to `Summary`
- **Marker**: inserted summary messages have `Meta["checkpoint"] = "true"` (constant `MetaKeyCheckpoint`) for identification
- **Consecutive-assistant resolution**: if inserting a summary creates two adjacent assistant messages, they are merged (content concatenated, tool calls preserved)
- Consumed checkpoints are kept in state so future `prepareMessages()` calls can re-apply their summaries against the growing message history
- Checkpoint indices always reference the raw `state.Messages()` slice, which only appends

## Compaction Algorithm

`Compact()` (`core/compaction.go`) applies phases sequentially, exiting early when under the target.

**Target**: 85% of the context window (`emergencyTargetFraction`).

### Phase 1 — Drop Checkpoint Summaries
- Removes oldest checkpoint summary messages (`Meta["checkpoint"]="true") outside the recent boundary (last 24 messages)
- Stops when under target or no more removable checkpoints
- Strategy label: `"checkpoint_drop"`

### Phase 1.5 — Drop Oldest Turns
- Identifies complete turns (user + assistant + tool chain) outside the recent boundary
- Drops entire turns oldest-first; falls back to individual message dropping when no complete turn exists
- Protects turns that span the recent boundary
- Strategy label: `"tool_trim"`

### Phase 2 — Emergency Truncation
- Trims tool results to 1500 chars (head 750 + tail 500)
- Trims older user/assistant messages to 1200 chars (head 600 + tail 400)
- If still over target, drops oldest non-system messages one at a time
- Strategy label: `"truncation"` (content trimming only) or `"emergency"` (messages dropped)

### Token Estimation
- `roughTokens()`: 4 chars ≈ 1 token, +20 per tool call, +10 per-message overhead
- Re-estimated after compaction for accurate `max_tokens` calculation

## Compaction Events

When compaction runs and changes messages, publishes `compaction` event:
- `strategy` — which phase resolved it
- `messages_before` / `messages_after` — counts before/after
- `message_count_delta` — number removed
- `tokens_saved` — estimated tokens freed

## Conversation Optimizer

`ConversationOptimizer` (`core/conversation_optimizer.go`) deduplicates redundant content before compaction. Wired into `prepareMessages()` after checkpoint compaction.

### File Read Dedup
- Tracks file reads by path; replaces earlier reads with identical content (SHA-256 hash comparison) with `[Earlier file read: path]`
- Keeps latest read only; bounded to **max 5 records** per file per content hash

### Shell Command Dedup
- Only applies to **transient commands** (`ls`, `find`, `pwd`, `cat`, `echo`, `head`, `tail`, `wc`)
- Replaces earlier output matching the latest output with `[Earlier command output: cmd]`
- Bounded to **max 10 records** per command

### Identical Result Dedup
- Uses SHA-256 hash (truncated to 16 hex chars) for content comparison
- Applied to both file reads and shell commands

## Removed Code

`checkpoint_shifting.go` was removed as dead code. All checkpoint compaction is handled by `BuildCheckpointCompactedMessages()`.
