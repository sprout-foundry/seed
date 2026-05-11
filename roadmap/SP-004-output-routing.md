# SP-004: Output Routing

**Status:** 📋 Spec  
**Location:** `core/agent.go`, `core/interfaces.go`  
**Size:** ~0 lines (not implemented)  
**Test Files:** 0

## Current State

Output from the agent reaches the caller in exactly one way: the `Run()` return value (`string, error`). There is no async output, no routing between terminal and webui, no output manager.

The sprout project has an `OutputManager` sub-manager with:
- Streaming buffer, reasoning buffer
- Async output channel
- Output router (terminal vs webui)
- Flush callback
- Event metadata

None of this exists in seed.

## Architecture

### What's Missing

```go
type OutputManager interface {
    // Streaming buffers
    ContentBuffer() *StreamingBuffer
    ReasoningBuffer() *StreamingBuffer
    SetFlushCallback(func())

    // Async output — goroutine-safe channel for background output
    AsyncOutput() <-chan string
    PublishOutput(msg string)

    // Event metadata — attach context to events
    SetEventMetadata(key string, value any)
    GetEventMetadata(key string) any
}
```

### Output Routing

The integration (sprout) needs to decide where output goes:

| Output Type | Terminal | WebUI |
|---|---|---|
| Streaming content | Print to terminal | WebSocket event |
| Tool start/end | Debug log | Tool lifecycle event |
| Errors | Print to terminal | Error event |
| Metrics | Status bar update | Metrics event |
| Reasoning | Hidden (or --show-reasoning) | Collapsible section |

### Proposed Solution

#### Phase 1: OutputManager Interface

Create `OutputManager` interface and default implementation:

```go
type OutputManager struct {
    streamBuf    *StreamingBuffer
    reasoningBuf *StreamingBuffer
    flushCallback func()
    asyncOutput   chan string
    metadata      map[string]any
    metadataMu    sync.RWMutex
}

func NewOutputManager() *OutputManager {
    return &OutputManager{
        streamBuf:    NewStreamingBuffer(),
        reasoningBuf: NewStreamingBuffer(),
        asyncOutput:  make(chan string, 100),
        metadata:     make(map[string]any),
    }
}
```

#### Phase 2: Wire into Agent

Replace direct buffer access with OutputManager:

```go
type Agent struct {
    // ... existing fields ...
    output *OutputManager  // replaces streamBuf, reasoningBuf, flushCallback
}

func (a *Agent) StreamingBuffer() *StreamingBuffer {
    return a.output.ContentBuffer()
}

func (a *Agent) ReasoningBuffer() *StreamingBuffer {
    return a.output.ReasoningBuffer()
}
```

#### Phase 3: Async Output Worker

The integration can run an async worker:

```go
// In sprout integration:
go func() {
    for msg := range agent.Output().AsyncOutput() {
        // Route to terminal or webui as appropriate
        if isWebUI() {
            eventBus.Publish(events.EventTypeAgentMessage, msg)
        } else {
            fmt.Print(msg)
        }
    }
}()
```

## Success Criteria

| Metric | Target |
|---|---|
| OutputManager interface defined | ✅ |
| OutputManager implementation | ✅ |
| Agent uses OutputManager | ✅ |
| Async output channel | ✅ |
| Event metadata storage | ✅ |
| Integration can route output | ✅ |

## Key Files

| File | Action |
|---|---|
| `core/output_manager.go` | Create: OutputManager interface + implementation |
| `core/agent.go` | Modify: use OutputManager instead of direct buffers |
| `test/mock_ui.go` | Modify: test output routing |
| `test/e2e_test.go` | Modify: test async output |
