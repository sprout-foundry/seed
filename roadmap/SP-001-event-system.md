# SP-001: Event System

**Status:** 📋 Spec  
**Location:** `core/`, `events/`  
**Size:** ~3 files, ~500 lines  
**Test Files:** 7 (events), 1 (harness event integration)

## Current State

The event bus (`events/EventBus`) is fully functional as a pub/sub system. The `Agent` struct holds an `eventBus` field, but only **2 of 20+ event types** are ever published:

| Event | Where | Data |
|---|---|---|
| `query_started` | `ProcessQuery` start | query, model |
| `query_completed` | `finalize()` | query, response, tokens, cost, duration |

The remaining 18+ event type constants are defined but never published.

## Architecture

### What Exists

```go
type EventBus struct {
    subscribers map[string]chan UIEvent
    mutex       sync.RWMutex
    nextID      int64
}

type UIEvent struct {
    ID        string    `json:"id"`
    Type      string    `json:"type"`
    Timestamp time.Time `json:"timestamp"`
    Data      any       `json:"data"`
}
```

### What's Missing

The `ConversationHandler` needs to publish events at every significant lifecycle point. The `Agent` needs an `OutputManager` sub-manager to handle event metadata, async output, and routing decisions.

### Event Types to Wire

| Event | When to Publish | Data |
|---|---|---|
| `agent_message` | Every assistant content chunk (streaming) or final response (non-streaming) | category, message |
| `tool_start` | Before `executor.Execute()` | tool_name, tool_call_id, arguments, display_name |
| `tool_end` | After `executor.Execute()` returns | tool_call_id, tool_name, status, result, duration |
| `stream_chunk` | Per streaming content chunk | chunk, content_type |
| `error` | On any error path (provider, tool, compaction) | message, error |
| `metrics_update` | After token tracking updates | total_tokens, context_tokens, iteration, total_cost |
| `agent_message` (system) | For debug/system messages | category ("info","warning","error"), message |
| `subagent_activity` | Subagent spawn/output/complete | tool_call_id, phase, message |
| `security_approval_request` | Before dangerous tool execution | request_id, tool_name, risk_level, reasoning |
| `security_prompt_request` | Security confirmation prompts | request_id, prompt, default_response |
| `ask_user_request` | User clarification needed | request_id, question |
| `validation` | After validation runs | file_path, diagnostics |
| `file_changed` | After file operations | file_path, action, content |
| `todo_update` | After todo state changes | todos |

### Proposed Solution

#### Phase 1: Wire Events in ConversationHandler

Add event publishing at these points in `ProcessQuery`:

```go
// Before provider.Chat()
eventBus.Publish(EventTypeMetricsUpdate, map[string]interface{}{
    "iteration": iter,
    "total_tokens": state.TotalTokens(),
})

// After provider.Chat() — on error
eventBus.Publish(EventTypeError, map[string]interface{}{
    "message": "LLM request failed",
    "error": err.Error(),
})

// After executor.Execute() — tool_start before, tool_end after
eventBus.Publish(EventTypeToolStart, ToolStartEvent(...))
results := executor.Execute(ctx, calls)
eventBus.Publish(EventTypeToolEnd, ToolEndEvent(...))

// In finalize() — agent_message for final response
eventBus.Publish(EventTypeAgentMessage, AgentMessageEvent("info", finalContent))
```

#### Phase 2: OutputManager Sub-Manager

Create `OutputManager` interface (matching sprout's sub-manager pattern):

```go
type OutputManager interface {
    // Streaming
    ContentBuffer() *StreamingBuffer
    ReasoningBuffer() *StreamingBuffer
    SetFlushCallback(func())

    // Async output
    AsyncOutput() <-chan string
    PublishOutput(msg string)

    // Event metadata
    SetEventMetadata(key string, value any)
    GetEventMetadata(key string) any
}
```

#### Phase 3: Streaming Events

When streaming is wired (SP-003), publish `stream_chunk` events per chunk:

```go
func (h *StreamHandlerImpl) OnContent(content string) {
    h.buf.Write([]byte(content))
    h.eventBus.Publish(EventTypeStreamChunk, StreamChunkEvent(content, "text"))
    h.eventBus.Publish(EventTypeAgentMessage, AgentMessageEvent("info", content))
}
```

## Implementation Phases

### Phase 1: Core Events (Week 1)

- Wire `tool_start` / `tool_end` around `executor.Execute()`
- Wire `error` event on provider failure path
- Wire `metrics_update` after token tracking
- Wire `agent_message` in `finalize()`

### Phase 2: OutputManager (Week 1-2)

- Create `OutputManager` interface and implementation
- Add async output channel
- Add event metadata storage
- Wire into Agent struct

### Phase 3: Streaming Events (Week 2)

- Wire `stream_chunk` events (depends on SP-003)
- Wire `agent_message` per content chunk

## Success Criteria

| Metric | Target |
|---|---|
| Event types published | 12+ (from 2) |
| Error events on all error paths | 100% |
| Tool lifecycle events | tool_start + tool_end for every Execute call |
| Metrics events | Published after every token tracking update |

## Key Files

| File | Action |
|---|---|
| `core/conversation.go` | Modify: add event publishing at lifecycle points |
| `core/agent.go` | Modify: add OutputManager sub-manager |
| `core/output_manager.go` | Create: OutputManager interface + implementation |
| `test/harness.go` | Modify: verify events in e2e tests |
| `test/e2e_test.go` | Modify: add event assertion tests |
