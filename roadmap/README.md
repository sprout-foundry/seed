# Roadmap

Incomplete work for the seed project. Completed features are documented in [docs/](../docs/).

## In Progress

| Spec | Title | Remaining |
|------|-------|-----------|
| SP-002 | [Error Handling & Retry](./SP-002-error-handling.md) | `ErrMaxIterations` not returned on max iteration exit |
| SP-005 | [Context Cancellation](./SP-005-context-cancellation.md) | `Interrupt()` method and `interruptCtx`/`interruptCancel` not implemented |
| SP-010 | [Turn Checkpoints](./SP-010-turn-checkpoints.md) | `RecordTurnCheckpointAsync` not wired; checkpoints built synchronously |
| SP-015 | [Checkpoint Hooks](./SP-015-checkpoint-hooks.md) | `Agent.Checkpoints()` convenience method not implemented |

## Completed

| Spec | Title | Docs |
|------|-------|------|
| SP-001 | Event System | [architecture](../docs/architecture.md) |
| SP-003 | Streaming & Output | [conversation-flow](../docs/conversation-flow.md) |
| SP-004 | Output Routing | [architecture](../docs/architecture.md) |
| SP-006 | Fallback Parsing | [conversation-flow](../docs/conversation-flow.md) |
| SP-007 | Response Validation | [conversation-flow](../docs/conversation-flow.md) |
| SP-008 | Conversation Optimizer | [compaction](../docs/compaction.md) |
| SP-009 | Configuration, Steering & Extensibility | [extensibility](../docs/extensibility.md) |
| SP-011 | Response Processing Hardening | [conversation-flow](../docs/conversation-flow.md) |
| SP-012 | Library Integrability | [extensibility](../docs/extensibility.md) |
| SP-013 | Tool Registry | [tool-registry](../docs/tool-registry.md) |
| SP-014 | Compaction Hardening | [compaction](../docs/compaction.md) |

## Out of Scope

| Area | Reason |
|------|--------|
| Persistence / Sessions | `State.ExportState()` / `ImportState()` are exposed — the consumer handles file I/O, scoping, naming, retention |
| Security & Approval | Consumer implements `ToolExecutor` — approval gates belong inside their `Execute()`, not around it |
| Circuit Breaker | Same — consumer's executor controls repetition handling |
| Scripted Client | Test utility, not library functionality; existing `MockProvider` suffices |
| Agent Lifecycle / Debug Logger | Seed has no long-lived goroutines; logging is an integration concern (consumer wires `UI`) |
