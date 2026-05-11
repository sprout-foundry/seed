# TODO

## Event System (SP-001)

[x] - EVENT: Wire `tool_start` event before `executor.Execute()` — publish `EventTypeToolStart` with tool_name, tool_call_id, arguments. `core/conversation.go`
[x] - EVENT: Wire `tool_end` event after `executor.Execute()` returns — publish `EventTypeToolEnd` with tool_call_id, tool_name, status, result, duration. `core/conversation.go`
[x] - EVENT: Wire `error` event on provider failure path — publish `EventTypeError` when `provider.Chat()` returns an error. `core/conversation.go`
[x] - EVENT: Wire `metrics_update` event after token tracking — publish `EventTypeMetricsUpdate` after `state.AddTokens()`. `core/conversation.go`
[x] - EVENT: Wire `agent_message` event in `finalize()` — publish `EventTypeAgentMessage` with final response content. `core/conversation.go`
[x] - EVENT: Wire `stream_chunk` events per streaming content chunk — publish `EventTypeStreamChunk` and `EventTypeAgentMessage` in `OnContent()`. `core/streaming.go` (depends on SP-003)
[x] - EVENT: Create `OutputManager` sub-manager interface — add streaming buffer, reasoning buffer, async output channel, output router, flush callback management. `core/output_manager.go` (new file)
[x] - EVENT: Wire `OutputManager` into `Agent` struct — replace direct `streamBuf`/`reasoningBuf`/`flushCallback` fields with `OutputManager` interface. `core/agent.go`

## Error Handling & Retry (SP-002)

[x] - ERROR: Define typed error hierarchy — create `TransientError`, `RateLimitError`, `ContextOverflowError`, `AuthError` types with `Wrapped error` field. `core/errors.go`
[x] - ERROR: Implement `classifyError(err, provider)` — wrap raw provider errors in typed errors based on error message patterns (timeout, rate limit, auth, context overflow). `core/errors.go` or `core/error_classifier.go`
[] - ERROR: Implement exponential backoff — create `ExponentialBackoff` struct with `InitialDelay`, `MaxDelay`, `Multiplier`, `MaxAttempts`, `Jitter`. `core/backoff.go` (new file)
[] - ERROR: Add retry logic to `ProcessQuery` — wrap `provider.Chat()` in retry loop with backoff; fail fast on `AuthError`; retry on `TransientError`/`RateLimitError`. `core/conversation.go`
[] - ERROR: Use `ErrMaxIterations` sentinel — return it when max iterations are exceeded instead of just logging a warning. `core/conversation.go`
[] - ERROR: Publish `error` events for all error types — every typed error should trigger `EventTypeError` event. `core/conversation.go`

## Streaming & Output (SP-003)

[] - STREAMING: Implement `AgentStreamHandler` — create concrete `StreamHandler` implementation that writes to `streamBuf`/`reasoningBuf`, publishes events, invokes `flushCallback`. `core/streaming.go`
[] - STREAMING: Add streaming path to `ProcessQuery` — call `provider.ChatStream()` instead of `Chat()` when streaming is supported; fall back to `Chat()` otherwise. `core/conversation.go`
[] - STREAMING: Handle tool calls after streaming — streamed response may include `tool_calls`; continue the tool call loop after `OnDone()`. `core/conversation.go`
[] - STREAMING: Wire `OnDone` to record assistant message in state — `OnDone` must add the final message to state so `finalize()` can extract it. `core/streaming.go`
[] - STREAMING: Wire `OnError` to publish error events — `OnError` must publish `EventTypeError` and return the error to the caller. `core/streaming.go`
[] - STREAMING: Add streaming simulation to `MockProvider` — `ChatStream` should simulate chunked delivery for e2e tests. `test/mock_provider.go`
[] - STREAMING: Add e2e streaming tests — test that streaming callbacks fire, content accumulates in `streamingBuffer`, and buffer content is preferred over choice content. `test/e2e_test.go`

## Output Routing (SP-004)

[] - OUTPUT: Create `OutputManager` interface and implementation — `ContentBuffer()`, `ReasoningBuffer()`, `SetFlushCallback()`, `AsyncOutput()`, `PublishOutput()`, `SetEventMetadata()`, `GetEventMetadata()`. `core/output_manager.go` (new file)
[] - OUTPUT: Add async output channel — buffered channel for goroutine-safe background output. `core/output_manager.go`
[] - OUTPUT: Wire `OutputManager` into `Agent` — replace direct buffer access with manager methods. `core/agent.go`
[] - OUTPUT: Add output routing tests — test async output delivery and event metadata. `test/e2e_test.go`

## Context Cancellation (SP-005)

[] - CANCELLATION: Check `ctx.Done()` in `ProcessQuery` loop — return `ErrInterrupted` when context is cancelled. `core/conversation.go`
[] - CANCELLATION: Add `Interrupt()` method to `Agent` — expose `interruptCancel` for external interruption. `core/agent.go`
[] - CANCELLATION: Add `inputInjectionChan` for mid-conversation user input — channel-based input injection with `InjectInput()` method. `core/agent.go`, `core/conversation.go`
[] - CANCELLATION: Add e2e cancellation tests — test that `ctx.Cancel()` stops the loop and returns `ErrInterrupted`. `test/e2e_test.go`
[] - CANCELLATION: Add e2e input injection tests — test that `InjectInput()` injects a user message mid-conversation. `test/e2e_test.go`

## Testing

[] - TEST: Add e2e test for API retry/error recovery — transient error → retry with backoff → success. `test/e2e_test.go`
[] - TEST: Add e2e test for rate limit handling — rate limit error → `RateLimitError` path exercised → retry with backoff. `test/e2e_test.go`
[] - TEST: Add e2e test for input injection/interrupt mid-conversation — conversation running → user injects input via channel → input becomes new user message → conversation continues. `test/e2e_test.go`
[] - TEST: Add e2e test for streaming responses — validate streaming callbacks fire, content accumulates in `streamingBuffer`, and buffer content is preferred over choice content. `test/e2e_test.go`
[] - TEST: Add e2e test for context cancellation — `ctx.Cancel()` → `ErrInterrupted` returned. `test/e2e_test.go`
[] - TEST: Add e2e test for tool lifecycle events — `tool_start` and `tool_end` events published around `executor.Execute()`. `test/e2e_test.go`
[] - TEST: Add e2e test for error events — provider error → `EventTypeError` event published. `test/e2e_test.go`
[] - TEST: Add e2e test for metrics events — `metrics_update` event published after token tracking. `test/e2e_test.go`

## Structural

[] - STRUCTURAL: Break up `conversation.go` (200+ lines) — extract `prepareMessages` pipeline, compaction, and finalize into separate files. `core/`
[] - STRUCTURAL: Standardize error wrapping — adopt `fmt.Errorf("operation: %w", err)` convention across all error paths. `core/`
[] - STRUCTURAL: Add `LastAssistantMessage()` to `State` — needed by streaming path to extract final message from state. `core/state.go`
[] - STRUCTURAL: Add `Len()` to `State` — needed by tests and debug logging. `core/state.go`
[] - STRUCTURAL: Remove unused sentinel errors or wire them — `ErrNoProvider` and `ErrNoExecutor` panic at construction; decide whether to keep as sentinels or remove. `core/errors.go`
