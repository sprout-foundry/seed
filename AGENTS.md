# AGENTS.md — Seed Agent Engine

## 📖 Project Overview
`seed` is a minimal, self-contained Go library that implements an LLM-powered conversation engine. It handles the full query → tool-call → response loop, emits structured UI events, tracks conversation state, and supports pluggable LLM providers and tool executors.

**Package:** `github.com/sprout-foundry/seed`

---

## 🏗 Architecture & Core Concepts

| Component | File | Responsibility |
|-----------|------|----------------|
| `Agent` | `core/agent.go` | Entry point. Holds provider, executor, UI, event bus, state, and output manager. Exposes `Run(ctx, query)`. |
| `ConversationHandler` | `core/conversation.go` | Drives the main loop: builds prompts, calls provider, executes tools, emits events, finalizes response. |
| `State` | `core/state.go` | Tracks message history, token counts, session ID. Supports export/import. |
| `Provider` | `core/interfaces.go` | LLM abstraction. `Chat()`, `ChatStream()`, `EstimateTokens()`, `Info()`. |
| `ToolExecutor` | `core/interfaces.go` | Executes tool calls returned by the model. |
| `OutputManager` | `core/output_manager.go` | Manages content/reasoning buffers, async output channel, flush callbacks, and event metadata. |
| `AgentStreamHandler` | `core/streaming.go` | Concrete `StreamHandler` that writes chunks to buffers, publishes events, and flushes. |
| `EventBus` | `events/events.go` | Thread-safe pub/sub for UI events (`query_started`, `tool_start`, `stream_chunk`, etc.). |
| `Harness` | `test/harness.go` | E2E test harness wiring `MockProvider`, `MockExecutor`, `MockUI`, and `EventBus`. |

---

## 📁 Directory Structure
```
.
├── core/               # Agent engine, state, conversation loop, streaming, output manager
├── events/             # EventBus and structured event types
├── test/               # Mocks, test harness, e2e integration tests
├── go.mod / go.sum
├── Makefile            # Build, test, vet, format targets
└── AGENTS.md           # This file
```

---

## 🛠 Development Workflow

| Task | Command |
|------|---------|
| Build | `make build` |
| Test | `make test` |
| Format | `make fmt` |
| Vet | `make vet` |
| Run e2e | `make test-e2e` |
| Full check | `make check` |

---

## 📝 Coding Conventions
- **Interfaces over implementations:** Always code against `Provider`, `ToolExecutor`, `UI`, `StreamHandler`, `OutputManager`.
- **Error wrapping:** Use `fmt.Errorf("context: %w", err)` consistently.
- **State immutability:** `State` methods mutate internally; callers should not slice/append to `state.Messages()` directly.
- **Event emission:** Publish events via `agent.eventBus.Publish(type, payload)`. Never block on full channels; drop non-critical events.
- **Streaming:** Use `AgentStreamHandler` for chunked output. It writes to `OutputManager` buffers and triggers flush callbacks.
- **Concurrency:** `EventBus`, `OutputManager`, and `State` are goroutine-safe. `ConversationHandler` is single-goroutine per query.

---

## 🧪 Testing Guidelines
- Use `test.NewHarnessWithT(t)` for all new e2e tests.
- Configure mocks via `h.Provider().AddTextResponse(...)` or `h.Executor().AddToolResult(...)`.
- Assert events with `h.AssertEventPublished(events.EventTypeX)`.
- Keep tests deterministic: avoid real network calls, real file I/O, or time-dependent logic without mocking.
- Run `make check` before committing.

---

## 🗺 Roadmap & Current State

| Area | Status | Notes |
|------|--------|-------|
| Event System | ✅ Complete | All events wired; tests pass. |
| Core Agent & Loop | ✅ Complete | Chat + tool-call loop works. |
| Output Manager | ✅ Complete | Buffers, async channel, flush callback implemented. |
| Streaming Path | ⏳ Pending | `ChatStream` not wired into `ProcessQuery` yet. |
| Error Handling & Retry | ⏳ Pending | No typed errors, backoff, or retry logic. |
| Context Cancellation | ⏳ Pending | `ctx.Done()` checks & `Interrupt()` missing. |
| Structural Refactors | ⏳ Pending | `conversation.go` monolithic; helpers not split. |
| Async Output Tests | ⏳ Pending | Channel & metadata untested. |

---

## 🤖 AI Agent Instructions
1. **Never modify `go.mod`** unless adding a direct dependency required for a new feature.
2. **Always use the test harness** (`test/harness.go`). Do not write tests that instantiate `Agent` manually.
3. **Streaming changes** must update `ProcessQuery` to call `provider.ChatStream()` and wire `AgentStreamHandler`.
4. **Error handling** must introduce typed errors (`TransientError`, `RateLimitError`, etc.) before adding retry logic.
5. **Context cancellation** requires checking `ctx.Done()` in the main loop and exposing `agent.Interrupt()`.
6. **Keep `conversation.go` focused.** If it exceeds ~150 lines, extract `prepareMessages`, `compactMessages`, or `finalize` into separate files.
7. **Run `make check`** before marking any task complete.
