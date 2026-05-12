package core

import (
	"bytes"
	"sync"
)

// StreamingBuffer captures streamed output for controlled display.
type StreamingBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

// NewStreamingBuffer creates a new streaming buffer.
func NewStreamingBuffer() *StreamingBuffer {
	return &StreamingBuffer{}
}

// Write appends content to the buffer.
func (b *StreamingBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

// String returns the current buffer contents.
func (b *StreamingBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// Len returns the current buffer length.
func (b *StreamingBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Len()
}

// Reset clears the buffer.
func (b *StreamingBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buffer.Reset()
}

// AgentStreamHandler is a concrete StreamHandler implementation that writes
// streamed content to the agent's buffers and publishes events via the
// agent's eventPublisher.
type AgentStreamHandler struct {
	agent    *Agent
	state    *State
	response *ChatResponse
}

// NewAgentStreamHandler creates a new StreamHandler for the given agent.
func NewAgentStreamHandler(agent *Agent, state *State) *AgentStreamHandler {
	return &AgentStreamHandler{
		agent: agent,
		state: state,
	}
}

// OnContent handles a streaming content chunk: writes to the stream buffer,
// publishes stream_chunk and agent_message events, and invokes the flush
// callback. Empty content chunks are silently ignored.
func (h *AgentStreamHandler) OnContent(content string) {
	if content == "" {
		return
	}
	h.agent.outputMgr.ContentBuffer().Write([]byte(content))
	if h.agent.eventPublisher != nil {
		h.agent.eventPublisher.Publish(EventTypeStreamChunk,
			map[string]interface{}{"chunk": content, "content_type": "text"})
		h.agent.eventPublisher.Publish("agent_message",
			map[string]interface{}{"category": "info", "message": content})
	}
	h.agent.outputMgr.Flush()
}

// OnReasoning handles a streaming reasoning chunk: writes to the reasoning
// buffer, publishes a stream_chunk event, and invokes the flush callback.
// Empty reasoning chunks are silently ignored.
func (h *AgentStreamHandler) OnReasoning(reasoning string) {
	if reasoning == "" {
		return
	}
	h.agent.outputMgr.ReasoningBuffer().Write([]byte(reasoning))
	if h.agent.eventPublisher != nil {
		h.agent.eventPublisher.Publish(EventTypeStreamChunk,
			map[string]interface{}{"chunk": reasoning, "content_type": "reasoning"})
	}
	h.agent.outputMgr.Flush()
}

// OnDone handles the end of streaming: records token usage, publishes metrics,
// and appends the final assistant message to the agent's state.
func (h *AgentStreamHandler) OnDone(resp *ChatResponse) {
	if resp == nil {
		return
	}
	h.response = resp
	if resp.Usage.TotalTokens > 0 {
		h.state.AddTokens(resp.Usage.PromptTokens,
			resp.Usage.CompletionTokens, resp.Usage.TotalTokens)

		// Publish metrics update event (matches non-streaming path)
		if h.agent.eventPublisher != nil {
			h.agent.eventPublisher.Publish(EventTypeMetricsUpdate, map[string]interface{}{
				"total_tokens":       h.state.TotalTokens(),
				"context_tokens":     resp.Usage.PromptTokens,
				"max_context_tokens": h.agent.provider.Info().ContextSize,
				"iteration":          0, // iteration index not tracked in stream handler
				"total_cost":         h.state.TotalCost(),
			})
		}
	}
	if len(resp.Choices) > 0 {
		assistantMsg := resp.ToMessage()
		h.state.AddMessage(assistantMsg)
	}
}

// Response returns the final ChatResponse from OnDone, or nil if not yet set.
func (h *AgentStreamHandler) Response() *ChatResponse {
	return h.response
}

// OnError handles streaming errors: publishes an error event via the bus.
func (h *AgentStreamHandler) OnError(err error) {
	if h.agent.eventPublisher != nil {
		h.agent.eventPublisher.Publish(EventTypeError,
			map[string]interface{}{"message": "chat stream failed", "error": err.Error()})
	}
}
