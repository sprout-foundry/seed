# Roadmap

Roadmap specifications for the seed project. Each spec describes a major
architectural area, its current state, and open work.

## Completed

| Spec | Title | Status |
|------|-------|--------|
| SP-001 | [Event System](./SP-001-event-system.md) | ✅ Complete |
| SP-002 | [Error Handling & Retry](./SP-002-error-handling.md) | ✅ Complete |
| SP-003 | [Streaming & Output](./SP-003-streaming-output.md) | ✅ Complete |
| SP-004 | [Output Routing](./SP-004-output-routing.md) | ✅ Complete |
| SP-005 | [Context Cancellation](./SP-005-context-cancellation.md) | ✅ Complete |
| SP-006 | [Fallback Parsing](./SP-006-fallback-parsing.md) | ✅ Complete |
| SP-007 | [Response Validation](./SP-007-response-validation.md) | ✅ Complete |
| SP-008 | [Conversation Optimizer](./SP-008-conversation-optimizer.md) | ✅ Complete |
| SP-009 | [Configuration, Steering & Extensibility](./SP-009-configuration-steering.md) | ✅ Complete |
| SP-010 | [Turn Checkpoints](./SP-010-turn-checkpoints.md) | ✅ Complete |
| SP-012 | [Library Integrability](./SP-012-library-integrability.md) | ✅ Complete |

## Active

| Spec | Title | Status |
|------|-------|--------|
| SP-011 | [Response Processing Hardening](./SP-011-response-processing-hardening.md) | ⚠️ Partial — `ToolCallNormalizer` exists but not wired; finish reason dispatch, blank/repetitive detection, and ANSI sanitization are absent |

## Out of Scope

The following were initially considered but intentionally excluded — they are
application-level or consumer-side concerns, not portable library features:

| Area | Reason |
|------|--------|
| Persistence / Sessions | `State.ExportState()` / `ImportState()` are already exposed — the consumer handles file I/O, scoping, naming, retention |
| Security & Approval | Consumer implements `ToolExecutor` — approval gates belong inside their `Execute()`, not around it |
| Circuit Breaker | Same — consumer's executor controls repetition handling |
| Scripted Client | Test utility, not library functionality; existing `MockProvider` suffices |
| Agent Lifecycle / Debug Logger | Seed has no long-lived goroutines; logging is an integration concern (consumer wires `UI`) |
