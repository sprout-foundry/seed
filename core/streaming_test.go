package core

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sprout-foundry/seed/events"
)

// --- StreamingBuffer tests ---

func TestStreamingBuffer_WriteAndString(t *testing.T) {
	buf := NewStreamingBuffer()
	buf.Write([]byte("Hello"))
	buf.Write([]byte(" World"))
	if buf.String() != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", buf.String())
	}
}

func TestStreamingBuffer_Len(t *testing.T) {
	buf := NewStreamingBuffer()
	if buf.Len() != 0 {
		t.Errorf("expected length 0, got %d", buf.Len())
	}
	buf.Write([]byte("abc"))
	if buf.Len() != 3 {
		t.Errorf("expected length 3, got %d", buf.Len())
	}
}

func TestStreamingBuffer_Reset(t *testing.T) {
	buf := NewStreamingBuffer()
	buf.Write([]byte("Hello"))
	buf.Reset()
	if buf.String() != "" {
		t.Errorf("expected empty after reset, got %q", buf.String())
	}
	if buf.Len() != 0 {
		t.Errorf("expected length 0 after reset, got %d", buf.Len())
	}
}

func TestStreamingBuffer_ConcurrentWrites(t *testing.T) {
	buf := NewStreamingBuffer()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			buf.Write([]byte(string(rune('A' + n%26))))
		}(i)
	}
	wg.Wait()
	if buf.Len() != 100 {
		t.Errorf("expected 100 chars, got %d", buf.Len())
	}
}

func TestStreamingBuffer_EmptyString(t *testing.T) {
	buf := NewStreamingBuffer()
	if buf.String() != "" {
		t.Errorf("expected empty string, got %q", buf.String())
	}
}

func TestStreamingBuffer_LargeWrite(t *testing.T) {
	buf := NewStreamingBuffer()
	large := strings.Repeat("x", 10000)
	buf.Write([]byte(large))
	if buf.Len() != 10000 {
		t.Errorf("expected 10000, got %d", buf.Len())
	}
	if buf.String() != large {
		t.Error("content mismatch")
	}
}

// --- AgentStreamHandler tests ---

func TestAgentStreamHandler_OnContent_WritesToBuffer(t *testing.T) {
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
	})
	h := NewAgentStreamHandler(a, a.State())

	h.OnContent("Hello from stream")

	if a.StreamingBuffer().String() != "Hello from stream" {
		t.Errorf("expected 'Hello from stream', got %q", a.StreamingBuffer().String())
	}
}

func TestAgentStreamHandler_OnContent_PublishesStreamChunkEvent(t *testing.T) {
	bus := events.NewEventBus()
	ch := bus.Subscribe("handler-test")

	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
		EventBus: bus,
	})
	h := NewAgentStreamHandler(a, a.State())

	h.OnContent("chunk data")

	// Drain the event bus channel
	var event events.UIEvent
	select {
	case event = <-ch:
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for stream_chunk event")
	}

	if event.Type != events.EventTypeStreamChunk {
		t.Errorf("expected event type %q, got %q", events.EventTypeStreamChunk, event.Type)
	}

	data, ok := event.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data as map[string]interface{}, got %T", event.Data)
	}

	if data["chunk"] != "chunk data" {
		t.Errorf("expected chunk 'chunk data', got %v", data["chunk"])
	}

	if data["content_type"] != "text" {
		t.Errorf("expected content_type 'text', got %v", data["content_type"])
	}
}

func TestAgentStreamHandler_OnContent_PublishesAgentMessageEvent(t *testing.T) {
	bus := events.NewEventBus()
	ch := bus.Subscribe("handler-test")

	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
		EventBus: bus,
	})
	h := NewAgentStreamHandler(a, a.State())

	h.OnContent("agent content")

	// The handler publishes two events: stream_chunk and agent_message.
	// Drain both.
	var count int
	var agentEvent events.UIEvent
	for count < 2 {
		select {
		case event := <-ch:
			count++
			if event.Type == events.EventTypeAgentMessage {
				agentEvent = event
			}
		case <-time.After(1 * time.Second):
			t.Fatal("timeout waiting for agent_message event")
		}
	}

	data, ok := agentEvent.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data as map[string]interface{}, got %T", agentEvent.Data)
	}

	if data["category"] != "info" {
		t.Errorf("expected category 'info', got %v", data["category"])
	}

	if data["message"] != "agent content" {
		t.Errorf("expected message 'agent content', got %v", data["message"])
	}
}

func TestAgentStreamHandler_OnContent_InvokesFlushCallback(t *testing.T) {
	var fired bool
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
	})
	a.SetFlushCallback(func() {
		fired = true
	})
	h := NewAgentStreamHandler(a, a.State())

	h.OnContent("test")

	if !fired {
		t.Error("expected flush callback to be invoked")
	}
}

func TestAgentStreamHandler_OnContent_NoEventBus(t *testing.T) {
	// When eventBus is nil, OnContent should not panic
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
		EventBus: nil,
	})
	h := NewAgentStreamHandler(a, a.State())

	h.OnContent("should not panic")
}

func TestAgentStreamHandler_OnContent_EmptyContentIgnored(t *testing.T) {
	bus := events.NewEventBus()
	ch := bus.Subscribe("handler-test")

	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
		EventBus: bus,
	})
	h := NewAgentStreamHandler(a, a.State())

	h.OnContent("")

	// No events should be published for empty content
	select {
	case evt := <-ch:
		t.Errorf("unexpected event for empty content: %v", evt.Type)
	default:
	}

	if a.StreamingBuffer().Len() != 0 {
		t.Errorf("expected empty buffer for empty content, got %d", a.StreamingBuffer().Len())
	}
}

func TestAgentStreamHandler_OnReasoning_WritesToBuffer(t *testing.T) {
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
	})
	h := NewAgentStreamHandler(a, a.State())

	h.OnReasoning("thinking about it")

	if a.ReasoningBuffer().String() != "thinking about it" {
		t.Errorf("expected 'thinking about it', got %q", a.ReasoningBuffer().String())
	}
}

func TestAgentStreamHandler_OnReasoning_PublishesStreamChunkEvent(t *testing.T) {
	bus := events.NewEventBus()
	ch := bus.Subscribe("handler-test")

	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
		EventBus: bus,
	})
	h := NewAgentStreamHandler(a, a.State())

	h.OnReasoning("reasoning data")

	var event events.UIEvent
	select {
	case event = <-ch:
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for stream_chunk event")
	}

	if event.Type != events.EventTypeStreamChunk {
		t.Errorf("expected event type %q, got %q", events.EventTypeStreamChunk, event.Type)
	}

	data, ok := event.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data as map[string]interface{}, got %T", event.Data)
	}

	if data["chunk"] != "reasoning data" {
		t.Errorf("expected chunk 'reasoning data', got %v", data["chunk"])
	}

	if data["content_type"] != "reasoning" {
		t.Errorf("expected content_type 'reasoning', got %v", data["content_type"])
	}
}

func TestAgentStreamHandler_OnReasoning_InvokesFlushCallback(t *testing.T) {
	var fired bool
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
	})
	a.SetFlushCallback(func() {
		fired = true
	})
	h := NewAgentStreamHandler(a, a.State())

	h.OnReasoning("reasoning")

	if !fired {
		t.Error("expected flush callback to be invoked")
	}
}

func TestAgentStreamHandler_OnReasoning_NoEventBus(t *testing.T) {
	// When eventBus is nil, OnReasoning should not panic
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
		EventBus: nil,
	})
	h := NewAgentStreamHandler(a, a.State())

	h.OnReasoning("should not panic")
}

func TestAgentStreamHandler_OnReasoning_EmptyReasoningIgnored(t *testing.T) {
	bus := events.NewEventBus()
	ch := bus.Subscribe("handler-test")

	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
		EventBus: bus,
	})
	h := NewAgentStreamHandler(a, a.State())

	h.OnReasoning("")

	select {
	case evt := <-ch:
		t.Errorf("unexpected event for empty reasoning: %v", evt.Type)
	default:
	}

	if a.ReasoningBuffer().Len() != 0 {
		t.Errorf("expected empty buffer for empty reasoning, got %d", a.ReasoningBuffer().Len())
	}
}

func TestAgentStreamHandler_OnDone_RecordsTokens(t *testing.T) {
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
	})
	state := a.State()
	h := NewAgentStreamHandler(a, state)

	h.OnDone(&ChatResponse{
		Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: "response"}}},
		Usage:   ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	})

	if state.TotalTokens() != 15 {
		t.Errorf("expected 15 total tokens, got %d", state.TotalTokens())
	}
}

func TestAgentStreamHandler_OnDone_AddsMessageToState(t *testing.T) {
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
	})
	state := a.State()
	h := NewAgentStreamHandler(a, state)

	content := "assistant response content"
	h.OnDone(&ChatResponse{
		Choices: []ChatChoice{{
			Message: Message{Role: "assistant", Content: content},
		}},
		Usage: ChatUsage{TotalTokens: 0},
	})

	msgs := state.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	if msgs[0].Role != "assistant" {
		t.Errorf("expected role 'assistant', got %q", msgs[0].Role)
	}

	if msgs[0].Content != content {
		t.Errorf("expected content %q, got %q", content, msgs[0].Content)
	}
}

func TestAgentStreamHandler_OnDone_ZeroTokens(t *testing.T) {
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
	})
	state := a.State()
	// Pre-add some tokens
	state.AddTokens(100, 50, 150)

	h := NewAgentStreamHandler(a, state)

	// OnDone with zero total tokens should not add tokens
	h.OnDone(&ChatResponse{
		Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: "response"}}},
		Usage:   ChatUsage{PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0},
	})

	// Token count should remain unchanged
	if state.TotalTokens() != 150 {
		t.Errorf("expected 150 total tokens (unchanged), got %d", state.TotalTokens())
	}
}

func TestAgentStreamHandler_OnDone_NilResp(t *testing.T) {
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
	})
	state := a.State()
	h := NewAgentStreamHandler(a, state)

	// Should not panic with nil response
	h.OnDone(nil)

	if state.Len() != 0 {
		t.Errorf("expected no messages after nil resp, got %d", state.Len())
	}
}

func TestAgentStreamHandler_OnDone_EmptyChoices(t *testing.T) {
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
	})
	state := a.State()
	h := NewAgentStreamHandler(a, state)

	// Should not add a message when choices is empty
	h.OnDone(&ChatResponse{
		Choices: []ChatChoice{},
		Usage:   ChatUsage{TotalTokens: 0},
	})

	if state.Len() != 0 {
		t.Errorf("expected no messages for empty choices, got %d", state.Len())
	}
}

func TestAgentStreamHandler_OnError_PublishesErrorEvent(t *testing.T) {
	bus := events.NewEventBus()
	ch := bus.Subscribe("handler-test")

	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
		EventBus: bus,
	})
	h := NewAgentStreamHandler(a, a.State())

	expectedErr := fmt.Errorf("streaming connection dropped")
	h.OnError(expectedErr)

	var event events.UIEvent
	select {
	case event = <-ch:
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for error event")
	}

	if event.Type != events.EventTypeError {
		t.Errorf("expected event type %q, got %q", events.EventTypeError, event.Type)
	}

	data, ok := event.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data as map[string]interface{}, got %T", event.Data)
	}

	if data["message"] != "streaming failed" {
		t.Errorf("expected message 'streaming failed', got %v", data["message"])
	}

	if data["error"] != "streaming connection dropped" {
		t.Errorf("expected error 'streaming connection dropped', got %v", data["error"])
	}
}

func TestAgentStreamHandler_OnError_NoEventBus(t *testing.T) {
	// When eventBus is nil, OnError should not panic
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
		EventBus: nil,
	})
	h := NewAgentStreamHandler(a, a.State())

	expectedErr := fmt.Errorf("no bus, no panic")
	h.OnError(expectedErr)
}
