# TODO

All remaining incomplete items from roadmap specs have been resolved. See [docs/](./docs/) for current state and [roadmap/](./roadmap/) for in-progress work.

## Error Handling (SP-002)

- [x] Return `ErrMaxIterations` when max iterations reached — returns wrapped error with context; publishes `EventTypeError` event; verified by unit and e2e tests. `core/conversation.go`

## Context Cancellation (SP-005)

- [x] Add `Interrupt()` method to `Agent` — expose `interruptCancel` for external cancellation with mutex-protected access. `core/agent.go`
- [x] Add `interruptCtx` / `interruptCancel` to `Agent` — internal interrupt context, atomically captured via `ResetInterrupt()` return value. `core/agent.go`

## Turn Checkpoints (SP-010)

- [x] Wire `RecordTurnCheckpointAsync` — `finalize()` now calls the async function instead of building synchronously; `onCheckpoint` callback fires in goroutine. `core/finalize.go`, `core/checkpoint_compaction.go`

## Checkpoint Hooks (SP-015)

- [x] Add `Agent.Checkpoints()` convenience method — delegates to `State.GetCheckpoints()`. `core/agent.go`
