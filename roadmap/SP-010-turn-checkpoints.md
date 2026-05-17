# SP-010: Turn Checkpoints

**Status:** ⚠️ Partial — One remaining item  
**See also:** [docs/compaction.md](../docs/compaction.md)

## Remaining

- **`RecordTurnCheckpointAsync` not wired** — The async recording function exists in `checkpoint_compaction.go` but is never called. Checkpoints are built synchronously in `finalize()`. The spec calls for async recording with a message snapshot to avoid blocking the response.

## What Exists

`TurnCheckpoint` struct, checkpoint storage in `State`, Go-only `TurnSummaryBuilder`, `BuildCheckpointCompactedMessages` with actionable summary (500-char guard), `Meta["checkpoint"]="true"` marker, checkpoint compaction wired into `prepareMessages()` — all implemented. See [docs/compaction.md](../docs/compaction.md).
