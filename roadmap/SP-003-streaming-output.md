# SP-003: Streaming & Output

**Status:** 📋 Spec  
**Location:** `core/streaming.go`, `core/interfaces.go`  
**Size:** ~2 files, ~80 lines  
**Test Files:** 6 (streaming_test.go)

## Current State

The infrastructure exists but is not wired:

| Component | Status |
|---|---|
| `StreamingBuffer` | ✅ Thread-safe, tested |
| `ReasoningBuffer` | ✅ Created in Agent, never written to |
| `StreamHandler` interface | ✅ Defined, never implemented |
| `Provider.ChatStream()` | ✅ Interface method, never called |
| `flushCallback` | ✅ Settable, never invoked |
| `streamBuf.Reset()` | ✅ Called at ProcessQuery start |
| `finalize()` stream check | ✅ Checks `streamBuf.Len() > 0` |

`ProcessQuery` only calls `provider.Chat()` (blocking). The streaming path is completely dead code.

## Architecture

### StreamHandler Interface

```go
type StreamHandler interface {
    OnContent(content string)
    OnReasoning(reasoning string)
    OnDone(resp *ChatResponse)
    OnError(err error)
}
```

### What Needs to Happen

1. `ProcessQuery` needs a streaming code path that calls `ChatStream` instead of `Chat`
2. A `StreamHandler` implementation must write to `streamBuf` / `reasoningBuf`
3. `flushCallback` must be invoked after each chunk
4. `OnDone` must record the final response in state
5. Events must be published per chunk (`stream_chunk`, `agent_message`)

### Proposed StreamHandler Implementation

```go
type AgentStreamHandler struct {
    agent    *Agent
    state    *State
    session  string
}

func (h *AgentStreamHandler) OnContent(content string) {
    h.agent.streamBuf.Write([]byte(content))
    if h.agent.eventBus != nil {
        h.agent.eventBus.Publish(events.EventTypeStreamChunk,
            StreamChunkEvent(content, "text"))
        h.agent.eventBus.Publish(events.EventTypeAgentMessage,
            AgentMessageEvent("info", content))
    }
    if h.agent.flushCallback != nil {
        h.agent.flushCallback()
    }
}

func (h *AgentStreamHandler) OnReasoning(reasoning string) {
    h.agent.reasoningBuf.Write([]byte(reasoning))
    if h.agent.eventBus != nil {
        h.agent.eventBus.Publish(events.EventTypeStreamChunk,
            StreamChunkEvent(reasoning, "reasoning"))
    }
    if h.agent.flushCallback != nil {
        h.agent.flushCallback()
    }
}

func (h *AgentStreamHandler) OnDone(resp *ChatResponse) {
    if resp.Usage.TotalTokens > 0 {
        h.agent.state.AddTokens(resp.Usage.PromptTokens,
            resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
    }
    assistantMsg := resp.ToMessage()
    h.agent.state.AddMessage(assistantMsg)
}

func (h *AgentStreamHandler) OnError(err error) {
    if h.agent.eventBus != nil {
        h.agent.eventBus.Publish(events.EventTypeError,
            ErrorEvent("streaming failed", err))
    }
}
```

### ProcessQuery Streaming Path

```go
// In ProcessQuery, after preparing messages:
if ch.agent.provider != nil {
    handler := &AgentStreamHandler{agent: ch.agent, state: ch.agent.state}
    err := ch.agent.provider.ChatStream(ctx, &ChatRequest{
        Messages: messages,
        Tools:    ch.agent.executor.GetTools(),
    }, handler)
    if err != nil {
        return "", fmt.Errorf("LLM streaming failed: %w", err)
    }
    // Get the final message from state
    assistantMsg := ch.agent.state.LastAssistantMessage()
    // Continue with tool call loop...
}
```

## Proposed Solution

### Phase 1: StreamHandler Implementation

- Create `AgentStreamHandler` in `core/streaming.go`
- Wire `OnContent` → `streamBuf.Write` + event publish + flush
- Wire `OnReasoning` → `reasoningBuf.Write` + event publish + flush
- Wire `OnDone` → state update (tokens + message)
- Wire `OnError` → error event publish

### Phase 2: ProcessQuery Streaming

- Add streaming code path in `ProcessQuery`
- Fall back to `Chat()` if provider doesn't support streaming
- Handle tool calls after streaming completes (streamed response may include tool_calls)
- Continue loop for tool call → stream → tool call cycle

### Phase 3: finalize() Integration

- `finalize()` already checks `streamBuf.Len() > 0` to suppress duplicate output
- Ensure `OnDone` records the assistant message so `finalize()` can extract final content
- Handle case where streaming buffer has content but no final message

## Success Criteria

| Metric | Target |
|---|---|
| StreamHandler implemented | ✅ AgentStreamHandler in core/ |
| ChatStream called in ProcessQuery | ✅ When provider supports it |
| Content chunks written to streamBuf | ✅ Per OnContent call |
| Reasoning chunks written to reasoningBuf | ✅ Per OnReasoning call |
| flushCallback invoked per chunk | ✅ After write |
| Events published per chunk | stream_chunk + agent_message |
| Final response recorded in state | OnDone |
| Tool call loop works with streaming | ✅ Continue loop after streamed tool calls |

## Key Files

| File | Action |
|---|---|
| `core/streaming.go` | Modify: add AgentStreamHandler |
| `core/conversation.go` | Modify: add streaming path in ProcessQuery |
| `test/mock_provider.go` | Modify: add streaming simulation helpers |
| `test/e2e_test.go` | Modify: add streaming tests |
