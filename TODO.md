# TODO

Remaining incomplete items from roadmap specs. All other specs are complete — see [docs/](./docs/).

## Error Handling (SP-002)

- [x] Return `ErrMaxIterations` when max iterations reached — returns wrapped error with context; publishes `EventTypeError` event; verified by unit and e2e tests. `core/conversation.go`

## Context Cancellation (SP-005)

- [ ] Add `Interrupt()` method to `Agent` — expose `interruptCancel` for external cancellation. `core/agent.go`
- [ ] Add `interruptCtx` / `interruptCancel` to `Agent` — internal interrupt context independent of caller's context. `core/agent.go`

## Turn Checkpoints (SP-010)

- [ ] Wire `RecordTurnCheckpointAsync` — function exists but is not called; checkpoints are built synchronously in `finalize()`. `core/conversation.go`, `core/finalize.go`

## Checkpoint Hooks (SP-015)

- [ ] Add `Agent.Checkpoints()` convenience method — callers must currently use `agent.State().GetCheckpoints()`. `core/agent.go`
