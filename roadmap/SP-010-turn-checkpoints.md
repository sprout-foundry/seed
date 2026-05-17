# SP-010: Turn Checkpoints

**Status:** ✅ Complete  
**See also:** [docs/compaction.md](../docs/compaction.md)

## What's Implemented

- `TurnCheckpoint` struct with `StartIndex`/`EndIndex`/`Summary`/`ActionableSummary`
- Checkpoint storage in `State` (mutex-protected)
- Go-only `TurnSummaryBuilder` — no LLM calls
- `BuildCheckpointCompactedMessages` — actionable summary (500-char guard), `Meta["checkpoint"]="true"` marker
- Checkpoint compaction wired into `prepareMessages()`
- `RecordTurnCheckpointAsync` wired in `finalize()` — non-blocking with 5s timeout and `onCheckpoint` callback
