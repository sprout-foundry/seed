# SP-015: Checkpoint Hooks

**Status:** ✅ Complete  
**See also:** [docs/compaction.md](../docs/compaction.md)

## What's Implemented

- `OnCheckpoint` callback in `Options` — fired asynchronously via `RecordTurnCheckpointAsync` with panic recovery
- Checkpoint built and stored in state
- `Agent.Checkpoints()` — convenience accessor delegating to `State.GetCheckpoints()`
