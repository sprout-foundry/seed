# TODO

## Event System (SP-001) — ✅ Complete

[x] - EVENT: Wire `tool_start` event before `executor.Execute()` — publish `EventTypeToolStart` with tool_name, tool_call_id, arguments. `core/conversation.go`
[x] - EVENT: Wire `tool_end` event after `executor.Execute()` returns — publish `EventTypeToolEnd` with tool_call_id, tool_name, status, result, duration. `core/conversation.go`
[x] - EVENT: Wire `error` event on provider failure path — publish `EventTypeError` when `provider.Chat()` returns an error. `core/conversation.go`
[x] - EVENT: Wire `metrics_update` event after token tracking — publish `EventTypeMetricsUpdate` after `state.AddTokens()`. `core/conversation.go`
[x] - EVENT: Wire `agent_message` event in `finalize()` — publish `EventTypeAgentMessage` with final response content. `core/conversation.go`
[x] - EVENT: Wire `stream_chunk` events per streaming content chunk — publish `EventTypeStreamChunk` and `EventTypeAgentMessage` in `OnContent()`. `core/streaming.go`
[x] - EVENT: Create `OutputManager` sub-manager interface — streaming buffer, reasoning buffer, async output channel, output router, flush callback management. `core/output_manager.go`
[x] - EVENT: Wire `OutputManager` into `Agent` struct — replace direct buffer fields with `OutputManager` interface. `core/agent.go`

## Error Handling & Retry (SP-002) — ✅ Complete

[x] - ERROR: Define typed error hierarchy — `TransientError`, `RateLimitError`, `ContextOverflowError`, `AuthError` with `Wrapped error` field. `core/errors.go`
[x] - ERROR: Implement `classifyError(err, provider)` — wrap raw provider errors in typed errors by message patterns. `core/error_classifier.go`
[x] - ERROR: Implement exponential backoff — `ExponentialBackoff` struct with InitialDelay, MaxDelay, Multiplier, MaxAttempts, Jitter. `core/backoff.go`
[x] - ERROR: Add retry logic to `ProcessQuery` — wrap `provider.Chat()` in retry loop with backoff; fail fast on `AuthError`. `core/conversation.go`
[x] - ERROR: Use `ErrMaxIterations` sentinel — return it when max iterations are exceeded. `core/conversation.go`
[x] - ERROR: Publish `error` events for all error types — every typed error triggers `EventTypeError` event. `core/conversation.go`

## Streaming & Output (SP-003) — ✅ Complete

[x] - STREAMING: Implement `AgentStreamHandler` — concrete `StreamHandler` that writes to buffers, publishes events, invokes flushCallback. `core/streaming.go`
[x] - STREAMING: Add streaming path to `ProcessQuery` — call `provider.ChatStream()` when streaming is supported; fall back to `Chat()`. `core/conversation.go`
[x] - STREAMING: Handle tool calls after streaming — streamed response may include `tool_calls`; continue the tool call loop after `OnDone()`. `core/conversation.go`
[x] - STREAMING: Wire `OnDone` to record assistant message in state. `core/streaming.go`
[x] - STREAMING: Wire `OnError` to publish error events. `core/streaming.go`
[x] - STREAMING: Add streaming simulation to `MockProvider`. `test/mock_provider.go`
[x] - STREAMING: Add e2e streaming tests — callbacks fire, content accumulates in buffer, buffer content preferred over choice content. `test/e2e_test.go`

## Output Routing (SP-004) — ✅ Complete

[x] - OUTPUT: Create `OutputManager` interface and implementation — `ContentBuffer()`, `ReasoningBuffer()`, `SetFlushCallback()`, `AsyncOutput()`, `PublishOutput()`, event metadata. `core/output_manager.go`
[x] - OUTPUT: Add async output channel — buffered channel for goroutine-safe background output. `core/output_manager.go`
[x] - OUTPUT: Wire `OutputManager` into `Agent`. `core/agent.go`
[x] - OUTPUT: Add output routing tests — async output delivery and event metadata. `test/e2e_test.go`

## Context Cancellation (SP-005) — ✅ Complete

[x] - CANCELLATION: Check `ctx.Done()` in `ProcessQuery` loop — return `ErrInterrupted` when context is cancelled. `core/conversation.go`
[x] - CANCELLATION: Add `Interrupt()` method to `Agent` — expose `interruptCancel` for external interruption. `core/agent.go`
[x] - CANCELLATION: Add `inputInjectionChan` for mid-conversation user input — channel-based injection with `InjectInput()` method. `core/agent.go`, `core/conversation.go`
[x] - CANCELLATION: Add e2e cancellation tests — `ctx.Cancel()` stops the loop and returns `ErrInterrupted`. `test/e2e_test.go`
[x] - CANCELLATION: Add e2e input injection tests — `InjectInput()` injects a user message mid-conversation. `test/e2e_test.go`

## Structural — remaining items

[x] - STRUCTURAL: Standardize error wrapping — adopt `fmt.Errorf("operation: %w", err)` convention across all error paths. `core/`
[x] - STRUCTURAL: Add `Len()` to `State` — needed by tests and debug logging. `core/state.go`
[x] - STRUCTURAL: Remove unused sentinel errors or wire them — `ErrNoProvider` and `ErrNoExecutor` panic at construction; decide whether to keep as sentinels or remove. `core/errors.go`
[] - STRUCTURAL: Remove unused sentinel errors or wire them — `ErrNoProvider` and `ErrNoExecutor` panic at construction; decide whether to keep as sentinels or remove. `core/errors.go`

## Fallback Parsing (SP-006)

[x] - FALLBACK: Create `FallbackParser` struct with `knownToolNames` callback — accept `FallbackParserOptions{KnownToolNames func(string) bool, Debug bool}` so consumers register their tools. `core/fallback_parser.go`
[x] - FALLBACK: Implement JSON code fence extraction — parse tool_calls arrays and single tool call objects from fenced JSON blocks. `core/fallback_parser.go`
[x] - FALLBACK: Implement bare JSON segment extraction — scan for balanced braces/brackets outside code fences containing tool call data. `core/fallback_parser.go`
[x] - FALLBACK: Implement XML function block extraction — parse `<function=name>` with `<parameter=name>value</parameter>` children. `core/fallback_parser.go`
[x] - FALLBACK: Implement function-name pattern extraction — detect `name: tool_name` followed by balanced JSON arguments. `core/fallback_parser.go`
[x] - FALLBACK: Implement named tool block extraction — detect `tool_name { ... }` where tool_name passes `knownToolNames` check. `core/fallback_parser.go`
[x] - FALLBACK: Implement tool call normalization — generate synthetic IDs (`fallback_{name}_{nano}`), normalize `Type` to `"function"`, ensure valid JSON arguments. `core/fallback_parser.go`
[x] - FALLBACK: Implement deduplication and content cleanup — dedupe by name+arguments, remove extracted blocks from content, normalize whitespace. `core/fallback_parser.go`
[x] - FALLBACK: Implement `ShouldUseFallback()` — quick pattern scan for tool-call-like patterns when structured tool_calls is empty. `core/fallback_parser.go`
[x] - FALLBACK: Wire into `ConversationHandler` — when no structured tool calls but content has patterns, run parser and inject extracted calls. `core/conversation.go`
[x] - FALLBACK: Add malformed response simulation to `MockProvider` — return tool calls embedded in content. `test/mock_provider.go`
[x] - FALLBACK: Add e2e test — malformed response -> tool calls extracted -> loop continues -> task completes. `test/e2e_test.go`

## Response Validation (SP-007)

[x] - VALIDATE: Create `ResponseValidator` struct with optional `DebugLog` callback — zero deps on Agent or concrete types. `core/response_validator.go`
[x] - VALIDATE: Implement `IsIncomplete()` — check for trailing `...`, abrupt endings, unusually short (<10 words not in complete-short list), unclosed code blocks. `core/response_validator.go`
[x] - VALIDATE: Implement `LooksLikeTentativePostToolResponse()` — detect planning prefixes ("Let me...", "I'll...", "I need to...") under 40 words. `core/response_validator.go`
[x] - VALIDATE: Wire incomplete check into `ConversationHandler` — on incomplete response, enqueue transient continuation message, loop again. `core/conversation.go`
[x] - VALIDATE: Wire tentative check into `ConversationHandler` — on tentative response with no tool calls, continue loop instead of finalizing. `core/conversation.go`
[x] - VALIDATE: Add continuation budget — track consecutive continuations, force-finalize after 3 without progress. `core/conversation.go`
[x] - VALIDATE: Add e2e test for truncated response continuation — provider returns incomplete response -> continuation -> complete response. `test/e2e_test.go`
[x] - VALIDATE: Add e2e test for tentative post-tool response — planning stub -> loop continues -> tool call executes. `test/e2e_test.go`

## Conversation Optimizer (SP-008)

[x] - OPTIMIZE: Create `ConversationOptimizer` struct — track file reads by filepath, shell commands by command string, enable/disable flag, `knownToolFn` callback. `core/conversation_optimizer.go`
[x] - OPTIMIZE: Implement file read tracking — hash content, keep only latest read per filepath, replace earlier reads with `[Earlier file read: {path}]`. `core/conversation_optimizer.go`
[x] - OPTIMIZE: Implement shell command tracking — detect transient commands (ls, find, pwd, cat, echo, head, tail, wc), keep only latest output. `core/conversation_optimizer.go`
[x] - OPTIMIZE: Implement `OptimizeConversation()` — lightweight pre-API-call pass that deduplicates redundant reads and commands. `core/conversation_optimizer.go`
[x] - OPTIMIZE: Wire into `prepareMessages()` — run optimizer before static compactor when enabled. `core/conversation.go`
[x] - OPTIMIZE: Add Optimizer option to Agent `Options` — opt-in, zero cost when nil. `core/agent.go`
[x] - OPTIMIZE: Add e2e test for file read dedup — multiple reads of same file -> only latest retained. `test/e2e_test.go`

## Configuration, Steering & Extensibility (SP-009)

[x] - CONFIG: Define `RetryConfig` struct — `MaxAttempts`, `InitialDelay`, `MaxDelay`, `Multiplier`, `Jitter` fields with sensible defaults. `core/agent.go`
[x] - CONFIG: Wire `RetryConfig` into `ProcessQuery` retry loop — use consumer-provided values instead of hardcoded defaults. `core/conversation.go`
[x] - CONFIG: Add `Agent.SetProvider(Provider)` — swap provider at runtime for subsequent calls. `core/agent.go`
[x] - STEER: Add `Agent.Steer(Message)` — queue a transient message prepended to the next API call, consumed once, not persisted. `core/agent.go`
[x] - STEER: Add `Agent.SteerSystem(string)` — convenience for injecting system-level steering. `core/agent.go`
[x] - STEER: Wire steering into `prepareMessages()` — transient messages appended after history before API call. `core/conversation.go`
[x] - STEER: Add e2e test for steering — steer mid-session -> next API call includes injected context. `test/e2e_test.go`
[x] - HOOKS: Add `OnIteration` callback to `Options` — fire-and-forget callback with iteration number and message count. `core/agent.go`
[x] - HOOKS: Wire `OnIteration` into `ProcessQuery` loop — call at start of each iteration. `core/conversation.go`
[x] - HOOKS: Publish compaction event — emit event with strategy name, message count delta, estimated tokens saved. `core/conversation.go`
[x] - HOOKS: Add e2e test for iteration hook — callback fires each iteration with correct counts. `test/e2e_test.go`
[x] - HOOKS: Add e2e test for compaction event — context overflow -> compaction event published. `test/e2e_test.go`

## Turn Checkpoints (SP-010)

[x] - CHECKPOINT: Define `TurnCheckpoint` struct — `StartIndex`, `EndIndex`, `Summary`, `ActionableSummary` with JSON tags. `core/turn_checkpoints.go`
[x] - CHECKPOINT: Add `[]TurnCheckpoint` to `State` — mutex-protected access with `AddCheckpoint`, `GetCheckpoints`, `SetCheckpoints`, `ClearCheckpoints`. `core/state.go`
[x] - CHECKPOINT: Add checkpoint serialization to ExportState/ImportState — checkpoints round-trip through JSON. `core/state.go`
[] - CHECKPOINT: Add checkpoint serialization to ExportState/ImportState — checkpoints round-trip through JSON. `core/state.go`
[x] - CHECKPOINT: Implement Go-only summary builder — extract user question, tool calls, errors, files modified, final status from turn messages. `core/turn_checkpoints.go`
[] - CHECKPOINT: Implement Go-only summary builder — extract user question, tool calls, errors, files modified, final status from turn messages. `core/turn_checkpoints.go`
[x] - CHECKPOINT: Implement actionable summary builder — bullet list of accomplishments with file paths and commands. `core/turn_checkpoints.go`
[x] - CHECKPOINT: Implement async `RecordTurnCheckpointAsync()` — snapshot messages, compute summary in background goroutine. `core/turn_checkpoints.go`
[x] - CHECKPOINT: Implement `BuildCheckpointCompactedMessages()` — replace consumed checkpoints with summary messages, shift remaining indices. `core/turn_checkpoints.go`
[x] - CHECKPOINT: Implement index shifting — update checkpoint StartIndex/EndIndex by delta after compaction removes messages. `core/turn_checkpoints.go`
[x] - CHECKPOINT: Handle consecutive-assistant boundary — if summary + next message are both assistant with no tool calls, merge or drop. `core/turn_checkpoints.go`
[x] - CHECKPOINT: Wire recording into ConversationHandler — set `queryStartIndex` when user message added, record checkpoint in `finalize()`. `core/conversation.go`
[x] - CHECKPOINT: Wire checkpoint compaction into `prepareMessages()` — use checkpoint-compacted messages before sending to provider. `core/conversation.go`
[] - CHECKPOINT: Wire checkpoint compaction into `prepareMessages()` — use checkpoint-compacted messages before sending to provider. `core/conversation.go`
[x] - CHECKPOINT: Add e2e checkpoint recording test — completed turn -> checkpoint created with summary and actionable summary. `test/e2e_test.go`
[x] - CHECKPOINT: Add e2e checkpoint compaction test — multiple turns -> checkpoints consumed -> message count reduced. `test/e2e_test.go`
[x] - CHECKPOINT: Add e2e index shifting test — compaction removes messages -> remaining checkpoints have valid indices. `test/e2e_test.go`

## Response Processing Hardening (SP-011)

[] - NORMALIZE: Create `ToolCallNormalizer` struct with `Normalize(calls []ToolCall) NormalizedToolCalls` — strips `<|channel|>` suffix, generates missing IDs, deduplicates by ID+args, repairs JSON arguments, normalizes Type to "function". `core/tool_call_normalizer.go` (new file)
[] - NORMALIZE: Wire normalizer into `runLoop` — run on structured `tool_calls` before execution. `core/conversation.go`
[] - NORMALIZE: Handle malformed structured tool calls — inject transient message asking model to re-emit, discard malformed calls. `core/conversation.go`
[] - FINISH: Implement finish reason dispatch — explicit switch on `""`, `"stop"`, `"length"`, `"content_filter"`, default. `core/conversation.go`
[] - FINISH: Handle `"stop"` with empty content — treat as incomplete, ask model to continue. `core/conversation.go`
[] - FINISH: Handle `"stop"` with incomplete content — send transient message asking for final answer. `core/conversation.go`
[] - FINISH: Handle `"stop"` with tentative content after tool results — implement `followsRecentToolResults()` scan, reject with specific message, accept after 2 rejections (match sprout). `core/conversation.go`
[] - FINISH: Handle `"length"` — always continue (model hit token limit). `core/conversation.go`
[] - FINISH: Handle `"content_filter"` — retry once, then return error to consumer on second occurrence. `core/conversation.go`
[] - BLANK: Implement `isBlankIteration(content)` — check if content is empty/whitespace. `core/conversation.go`
[] - BLANK: Implement `isRepetitiveContent(content)` — compare against previous assistant message. `core/conversation.go`
[] - BLANK: Wire blank/repetitive detection — separate `consecutiveBlank` counter, 1st → reminder, 2nd consecutive → force-finalize with error. `core/conversation.go`
[] - ANSI: Add `sanitizeANSI(content)` — strip ANSI escape codes from content. `core/conversation.go`
[] - NORMALIZE: Add e2e test — `<|channel|>0` suffix stripped → tool name matches → executes. `test/e2e_test.go`
[] - FINISH: Add e2e test — `finish_reason: "stop"` with empty content → continuation → complete response. `test/e2e_test.go`
[] - FINISH: Add e2e test — `finish_reason: "length"` → continuation. `test/e2e_test.go`
[] - FINISH: Add e2e test — `finish_reason: "content_filter"` → retry once → second occurrence → error returned. `test/e2e_test.go`
[] - NORMALIZE: Add e2e test — malformed structured tool call → transient message → model re-emits. `test/e2e_test.go`
[] - NORMALIZE: Add e2e test — missing tool call ID → synthetic ID generated → tool result linked. `test/e2e_test.go`
[] - NORMALIZE: Add e2e test — duplicate tool calls → only unique calls execute. `test/e2e_test.go`
[] - BLANK: Add e2e test — blank iteration → reminder → 2nd blank → error. `test/e2e_test.go`
[] - BLANK: Add e2e test — repetitive content → reminder → 2nd → error. `test/e2e_test.go`
[] - ANSI: Add e2e test — ANSI codes stripped from content. `test/e2e_test.go`
