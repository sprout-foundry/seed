# SP-015: Checkpoint Hooks

**Status:** ⚠️ Partial — One remaining item  
**See also:** [docs/extensibility.md](../docs/extensibility.md)

## Remaining

- **`Agent.Checkpoints()` method** — No convenience accessor on `Agent`. Callers must use `agent.State().GetCheckpoints()` instead.

## What Exists

`OnCheckpoint` callback in `Options`, fired synchronously in `finalize()` with panic recovery, checkpoint built and stored in state — all implemented. See [docs/extensibility.md](../docs/extensibility.md).
