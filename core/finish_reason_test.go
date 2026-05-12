package core

import (
	"context"
	"testing"

	"github.com/sprout-foundry/seed/events"
)

// --- Multi-response mock provider for finish reason tests ---

type finishReasonProvider struct {
	responses []*ChatResponse
	idx       int
	info      ProviderInfo
}

func (m *finishReasonProvider) Chat(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
	if m.idx >= len(m.responses) {
		return nil, ErrNoProvider
	}
	resp := m.responses[m.idx]
	m.idx++
	return resp, nil
}

func (m *finishReasonProvider) ChatStream(_ context.Context, _ *ChatRequest, h StreamHandler) error {
	if m.idx >= len(m.responses) {
		return ErrNoProvider
	}
	resp := m.responses[m.idx]
	m.idx++
	h.OnDone(resp)
	return nil
}

func (m *finishReasonProvider) Info() ProviderInfo {
	return m.info
}

func (m *finishReasonProvider) EstimateTokens(_ *ChatRequest) int {
	return 10
}

func newFRProvider(responses ...*ChatResponse) *finishReasonProvider {
	return &finishReasonProvider{
		responses: responses,
		info: ProviderInfo{
			Model:       "test-model",
			ContextSize: 128000,
		},
	}
}

func frTextResponse(content string, finishReason string) *ChatResponse {
	return &ChatResponse{
		Choices: []ChatChoice{{
			Message:      Message{Role: "assistant", Content: content},
			FinishReason: finishReason,
		}},
		Usage: ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
}

// findEvent returns the first event of the given type, or zero value if not found.
func findEvent(evts []events.UIEvent, eventType string) events.UIEvent {
	for _, e := range evts {
		if e.Type == eventType {
			return e
		}
	}
	return events.UIEvent{}
}

// --- Tests ---

func TestFinishReasonStop(t *testing.T) {
	provider := newFRProvider(frTextResponse("Done.", "stop"))
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Done." {
		t.Errorf("expected %q, got %q", "Done.", result)
	}
	if provider.idx != 1 {
		t.Errorf("expected 1 provider call, got %d", provider.idx)
	}
}

func TestFinishReasonEmpty(t *testing.T) {
	provider := newFRProvider(frTextResponse("Hello.", ""))
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello." {
		t.Errorf("expected %q, got %q", "Hello.", result)
	}
	if provider.idx != 1 {
		t.Errorf("expected 1 provider call, got %d", provider.idx)
	}
}

func TestFinishReasonLength_Continues(t *testing.T) {
	provider := newFRProvider(
		frTextResponse("This is a long", "length"),
		frTextResponse(" response.", "stop"),
	)
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != " response." {
		t.Errorf("expected %q, got %q", " response.", result)
	}
	if provider.idx != 2 {
		t.Errorf("expected 2 provider calls, got %d", provider.idx)
	}
}

func TestFinishReasonLength_MaxContinuations(t *testing.T) {
	provider := newFRProvider(
		frTextResponse("chunk", "length"),
		frTextResponse("chunk", "length"),
		frTextResponse("chunk", "length"),
		frTextResponse("chunk", "length"),
	)
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.idx != 4 {
		t.Errorf("expected 4 provider calls, got %d", provider.idx)
	}
	if result != "chunk" {
		t.Errorf("expected %q, got %q", "chunk", result)
	}
}

func TestFinishReasonContentFilter_PublishesError(t *testing.T) {
	provider := newFRProvider(frTextResponse("filtered", "content_filter"))
	bus := events.NewEventBus()
	sub := bus.Subscribe("__fr_test__")
	agent, _ := NewAgent(Options{
		Provider:       provider,
		Executor:       &mockExecutor{},
		EventPublisher: bus,
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "filtered" {
		t.Errorf("expected %q, got %q", "filtered", result)
	}
	if provider.idx != 1 {
		t.Errorf("expected 1 provider call, got %d", provider.idx)
	}

	evts := drainEvents(sub)
	if !findEventType(evts, EventTypeError) {
		t.Errorf("expected error event for content_filter")
	}
}

func TestFinishReasonContentFilter_NoContinuation(t *testing.T) {
	provider := newFRProvider(
		frTextResponse("This was cut off...", "content_filter"),
		frTextResponse("should not be called", "stop"),
	)
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.idx != 1 {
		t.Errorf("expected 1 provider call (no continuation), got %d", provider.idx)
	}
	if result != "This was cut off..." {
		t.Errorf("expected %q, got %q", "This was cut off...", result)
	}
}

func TestFinishReasonUnknown_Continues(t *testing.T) {
	provider := newFRProvider(
		frTextResponse("partial", "some_unknown_reason"),
		frTextResponse(" done", "stop"),
	)
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != " done" {
		t.Errorf("expected %q, got %q", " done", result)
	}
	if provider.idx != 2 {
		t.Errorf("expected 2 provider calls, got %d", provider.idx)
	}
}

func TestFinishReasonUnknown_MaxContinuations(t *testing.T) {
	provider := newFRProvider(
		frTextResponse("x", "weird"),
		frTextResponse("x", "weird"),
		frTextResponse("x", "weird"),
		frTextResponse("x", "weird"),
	)
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.idx != 4 {
		t.Errorf("expected 4 provider calls, got %d", provider.idx)
	}
	if result != "x" {
		t.Errorf("expected %q, got %q", "x", result)
	}
}

func TestFinishReasonToolCalls(t *testing.T) {
	provider := newFRProvider(
		&ChatResponse{
			Choices: []ChatChoice{{
				Message: Message{
					Role:    "assistant",
					Content: "",
					ToolCalls: []ToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: ToolCallFunction{
							Name:      "echo",
							Arguments: `{"message":"hello"}`,
						},
					}},
				},
				FinishReason: "tool_calls",
			}},
			Usage: ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
		frTextResponse("Got it.", "stop"),
	)
	executor := &mockExecutor{
		results: []Message{{Role: "tool", Content: "echo result", ToolCallID: "call_1"}},
	}
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: executor,
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Got it." {
		t.Errorf("expected %q, got %q", "Got it.", result)
	}
	if provider.idx != 2 {
		t.Errorf("expected 2 provider calls, got %d", provider.idx)
	}
}

func TestFinishReason_NoChoices(t *testing.T) {
	provider := newFRProvider(&ChatResponse{
		Choices: []ChatChoice{},
		Usage:   ChatUsage{PromptTokens: 10, CompletionTokens: 0, TotalTokens: 10},
	})
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty result, got %q", result)
	}
	if provider.idx != 1 {
		t.Errorf("expected 1 provider call, got %d", provider.idx)
	}
}

func TestFinishReasonLength_NoErrorEvent(t *testing.T) {
	provider := newFRProvider(
		frTextResponse("truncated", "length"),
		frTextResponse(" done", "stop"),
	)
	bus := events.NewEventBus()
	sub := bus.Subscribe("__fr_test2__")
	agent, _ := NewAgent(Options{
		Provider:       provider,
		Executor:       &mockExecutor{},
		EventPublisher: bus,
	})

	_, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evts := drainEvents(sub)
	if findEventType(evts, EventTypeError) {
		t.Errorf("expected no error events for length finish reason")
	}
}

func TestFinishReasonContentFilter_EventDetails(t *testing.T) {
	provider := newFRProvider(frTextResponse("filtered content", "content_filter"))
	bus := events.NewEventBus()
	sub := bus.Subscribe("__fr_test3__")
	agent, _ := NewAgent(Options{
		Provider:       provider,
		Executor:       &mockExecutor{},
		EventPublisher: bus,
	})

	_, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evts := drainEvents(sub)
	errEvt := findEvent(evts, EventTypeError)
	if errEvt.Type == "" {
		t.Fatal("expected error event for content_filter")
	}
	data, ok := errEvt.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected error event data to be map, got %T", errEvt.Data)
	}
	if data["error"] != "content_filter" {
		t.Errorf("expected error field 'content_filter', got %v", data["error"])
	}
}

func TestFinishReasonLength_ResetsOnToolCalls(t *testing.T) {
	provider := newFRProvider(
		frTextResponse("thinking...", "length"),
		&ChatResponse{
			Choices: []ChatChoice{{
				Message: Message{
					Role:    "assistant",
					Content: "",
					ToolCalls: []ToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: ToolCallFunction{
							Name:      "echo",
							Arguments: `{"message":"test"}`,
						},
					}},
				},
				FinishReason: "tool_calls",
			}},
			Usage: ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
		frTextResponse("final", "stop"),
	)
	executor := &mockExecutor{
		results: []Message{{Role: "tool", Content: "tool result", ToolCallID: "call_1"}},
	}
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: executor,
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "final" {
		t.Errorf("expected %q, got %q", "final", result)
	}
	if provider.idx != 3 {
		t.Errorf("expected 3 provider calls, got %d", provider.idx)
	}
}

func TestFinishReasonContentFilter_TurnCompleted(t *testing.T) {
	provider := newFRProvider(frTextResponse("filtered", "content_filter"))
	bus := events.NewEventBus()
	sub := bus.Subscribe("__fr_test4__")
	agent, _ := NewAgent(Options{
		Provider:       provider,
		Executor:       &mockExecutor{},
		EventPublisher: bus,
	})

	_, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evts := drainEvents(sub)
	if !findEventType(evts, EventTypeQueryCompleted) {
		t.Error("expected query_completed event for content_filter")
	}
}

func TestFinishReasonContentFilter_WithEventBus(t *testing.T) {
	provider := newFRProvider(frTextResponse("filtered", "content_filter"))
	bus := events.NewEventBus()
	sub := bus.Subscribe("__fr_test5__")
	agent, _ := NewAgent(Options{
		Provider:       provider,
		Executor:       &mockExecutor{},
		EventPublisher: bus,
	})

	_, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evts := drainEvents(sub)
	if !findEventType(evts, EventTypeError) {
		t.Error("expected error event for content_filter")
	}
}

func TestFinishReasonLength_Stream(t *testing.T) {
	provider := newFRProvider(
		frTextResponse("partial", "length"),
		frTextResponse(" done", "stop"),
	)
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	result, err := agent.RunStream(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != " done" {
		t.Errorf("expected %q, got %q", " done", result)
	}
	if provider.idx != 2 {
		t.Errorf("expected 2 provider calls, got %d", provider.idx)
	}
}

func TestFinishReasonContentFilter_Stream(t *testing.T) {
	provider := newFRProvider(frTextResponse("filtered", "content_filter"))
	bus := events.NewEventBus()
	sub := bus.Subscribe("__fr_test6__")
	agent, _ := NewAgent(Options{
		Provider:       provider,
		Executor:       &mockExecutor{},
		EventPublisher: bus,
	})

	result, err := agent.RunStream(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "filtered" {
		t.Errorf("expected %q, got %q", "filtered", result)
	}
	if provider.idx != 1 {
		t.Errorf("expected 1 provider call, got %d", provider.idx)
	}

	evts := drainEvents(sub)
	if !findEventType(evts, EventTypeError) {
		t.Error("expected error event for content_filter in streaming mode")
	}
}

func TestFinishReasonContentFilter_CaseSensitive(t *testing.T) {
	// "CONTENT_FILTER" (uppercase) should NOT match "content_filter".
	provider := newFRProvider(
		frTextResponse("content", "CONTENT_FILTER"),
		frTextResponse("second", "stop"),
	)
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// CONTENT_FILTER hits default case → continues → second call returns "second"
	if result != "second" {
		t.Errorf("expected %q (default case continued), got %q", "second", result)
	}
	if provider.idx != 2 {
		t.Errorf("expected 2 provider calls (default continued), got %d", provider.idx)
	}
}

func TestFinishReasonLength_DrainsEventChannel(t *testing.T) {
	provider := newFRProvider(
		frTextResponse("a", "length"),
		frTextResponse("b", "length"),
		frTextResponse("c", "stop"),
	)
	bus := events.NewEventBus()
	_ = bus.Subscribe("__fr_test7__")
	agent, _ := NewAgent(Options{
		Provider:       provider,
		Executor:       &mockExecutor{},
		EventPublisher: bus,
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "c" {
		t.Errorf("expected %q, got %q", "c", result)
	}
	if provider.idx != 3 {
		t.Errorf("expected 3 provider calls, got %d", provider.idx)
	}
}

func TestFinishReasonContentFilter_WithTools(t *testing.T) {
	// content_filter with tool calls should still finalize without executing tools.
	provider := newFRProvider(&ChatResponse{
		Choices: []ChatChoice{{
			Message: Message{
				Role:    "assistant",
				Content: "filtered",
				ToolCalls: []ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: ToolCallFunction{
						Name:      "dangerous_tool",
						Arguments: `{}`,
					},
				}},
			},
			FinishReason: "content_filter",
		}},
		Usage: ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	})
	executor := &mockExecutor{
		results: []Message{{Role: "tool", Content: "should not execute", ToolCallID: "call_1"}},
	}
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: executor,
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "filtered" {
		t.Errorf("expected %q, got %q", "filtered", result)
	}
	if provider.idx != 1 {
		t.Errorf("expected 1 provider call, got %d", provider.idx)
	}

}

func TestFinishReasonStop_EmptyContent_Continues(t *testing.T) {
	// "stop" with empty content should be treated as incomplete and continue.
	provider := newFRProvider(
		frTextResponse("", "stop"),
		frTextResponse("Here is the answer.", "stop"),
	)
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Here is the answer." {
		t.Errorf("expected %q, got %q", "Here is the answer.", result)
	}
	if provider.idx != 2 {
		t.Errorf("expected 2 provider calls, got %d", provider.idx)
	}
}

func TestFinishReasonStop_WhitespaceOnly_Continues(t *testing.T) {
	// "stop" with whitespace-only content should also continue.
	provider := newFRProvider(
		frTextResponse("   \n\t  ", "stop"),
		frTextResponse("Real content.", "stop"),
	)
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Real content." {
		t.Errorf("expected %q, got %q", "Real content.", result)
	}
	if provider.idx != 2 {
		t.Errorf("expected 2 provider calls, got %d", provider.idx)
	}
}

func TestFinishReasonStop_EmptyContent_MaxContinuations(t *testing.T) {
	// After maxContinuations empty "stop" responses, force-finalize.
	provider := newFRProvider(
		frTextResponse("", "stop"),
		frTextResponse("", "stop"),
		frTextResponse("", "stop"),
		frTextResponse("", "stop"),
	)
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.idx != 4 {
		t.Errorf("expected 4 provider calls, got %d", provider.idx)
	}
	if result != "" {
		t.Errorf("expected empty result after max continuations, got %q", result)
	}
}

func TestFinishReasonStop_EmptyContent_Stream_Continues(t *testing.T) {
	// "stop" with empty content should also continue in streaming mode.
	provider := newFRProvider(
		frTextResponse("", "stop"),
		frTextResponse("Streaming answer.", "stop"),
	)
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	result, err := agent.RunStream(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Streaming answer." {
		t.Errorf("expected %q, got %q", "Streaming answer.", result)
	}
	if provider.idx != 2 {
		t.Errorf("expected 2 provider calls, got %d", provider.idx)
	}
}

func TestFinishReasonStop_WithToolCalls_DoesNotContinue(t *testing.T) {
	// "stop" with empty content but tool calls present should NOT continue —
	// tool calls represent progress and the existing logic handles them.
	provider := newFRProvider(
		&ChatResponse{
			Choices: []ChatChoice{{
				Message: Message{
					Role:    "assistant",
					Content: "",
					ToolCalls: []ToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: ToolCallFunction{
							Name:      "echo",
							Arguments: `{"message":"hello"}`,
						},
					}},
				},
				FinishReason: "stop",
			}},
			Usage: ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
		frTextResponse("After tool.", "stop"),
	)
	executor := &mockExecutor{
		results: []Message{{Role: "tool", Content: "echo result", ToolCallID: "call_1"}},
	}
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: executor,
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have executed tool, then continued to second response
	if result != "After tool." {
		t.Errorf("expected %q, got %q", "After tool.", result)
	}
	if provider.idx != 2 {
		t.Errorf("expected 2 provider calls, got %d", provider.idx)
	}
}

func TestFinishReasonStop_EmptyContent_NoErrorEvent(t *testing.T) {
	// Force-finalize after max continuations should NOT publish error events
	// (parallel to TestFinishReasonLength_NoErrorEvent).
	provider := newFRProvider(
		frTextResponse("", "stop"),
		frTextResponse("", "stop"),
		frTextResponse("", "stop"),
		frTextResponse("", "stop"),
	)
	bus := events.NewEventBus()
	sub := bus.Subscribe("__fr_test_empty__")
	agent, _ := NewAgent(Options{
		Provider:       provider,
		Executor:       &mockExecutor{},
		EventPublisher: bus,
	})

	_, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evts := drainEvents(sub)
	if findEventType(evts, EventTypeError) {
		t.Errorf("expected no error events for stop/empty force-finalize")
	}
}

func TestFinishReasonStop_EmptyContent_NilToolCalls(t *testing.T) {
	// "stop" with empty content and nil ToolCalls (not just empty slice) should continue.
	provider := newFRProvider(
		&ChatResponse{
			Choices: []ChatChoice{{
				Message:      Message{Role: "assistant", Content: "", ToolCalls: nil},
				FinishReason: "stop",
			}},
			Usage: ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
		frTextResponse("Recovery content.", "stop"),
	)
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Recovery content." {
		t.Errorf("expected %q, got %q", "Recovery content.", result)
	}
	if provider.idx != 2 {
		t.Errorf("expected 2 provider calls, got %d", provider.idx)
	}
}
