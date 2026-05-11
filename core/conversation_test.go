package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sprout-foundry/seed/events"
)

// --- Conversation tests ---

func TestPrepareMessages_SystemPromptPrepended(t *testing.T) {
	provider := &mockProvider{
		info:       ProviderInfo{ContextSize: 10000},
		tokenCount: 100,
	}
	a := NewAgent(Options{
		Provider:     provider,
		Executor:     &mockExecutor{},
		SystemPrompt: "You are helpful.",
	})

	// Add messages to state
	a.State().AddMessage(Message{Role: "user", Content: "hi"})
	a.State().AddMessage(Message{Role: "assistant", Content: "hello"})

	ch := newConversationHandler(a)
	messages := ch.prepareMessages()

	// First message should be system prompt
	if messages[0].Role != "system" || messages[0].Content != "You are helpful." {
		t.Errorf("expected system prompt, got %v", messages[0])
	}
	// Should have 3 messages: system, user, assistant
	if len(messages) != 3 {
		t.Errorf("expected 3 messages, got %d", len(messages))
	}
}

func TestPrepareMessages_SystemMessagesStrippedFromHistory(t *testing.T) {
	a := NewAgent(Options{
		Provider: &mockProvider{
			info:       ProviderInfo{ContextSize: 10000},
			tokenCount: 100,
		},
		Executor:     &mockExecutor{},
		SystemPrompt: "Current system.",
	})

	// Add old system message to history (simulating imported state)
	a.State().SetMessages([]Message{
		{Role: "system", Content: "Old system prompt"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	})

	ch := newConversationHandler(a)
	messages := ch.prepareMessages()

	// Should have: current system + user + assistant (old system stripped)
	if len(messages) != 3 {
		t.Errorf("expected 3 messages, got %d", len(messages))
	}
	if messages[0].Content != "Current system." {
		t.Errorf("expected current system prompt, got %q", messages[0].Content)
	}
}

func TestPrepareMessages_ImagesStrippedForNonVision(t *testing.T) {
	a := NewAgent(Options{
		Provider: &mockProvider{
			info:       ProviderInfo{ContextSize: 10000, HasVision: false},
			tokenCount: 100,
		},
		Executor: &mockExecutor{},
	})

	a.State().AddMessage(Message{
		Role:    "user",
		Content: "Look at this",
		Images:  []ImageData{{URL: "http://example.com/img.png", MIMEType: "image/png"}},
	})

	ch := newConversationHandler(a)
	messages := ch.prepareMessages()

	// Find the user message
	for _, msg := range messages {
		if msg.Role == "user" {
			if len(msg.Images) != 0 {
				t.Error("expected images to be stripped for non-vision model")
			}
			return
		}
	}
	t.Error("user message not found")
}

func TestPrepareMessages_ImagesKeptForVision(t *testing.T) {
	a := NewAgent(Options{
		Provider: &mockProvider{
			info:       ProviderInfo{ContextSize: 10000, HasVision: true},
			tokenCount: 100,
		},
		Executor: &mockExecutor{},
	})

	a.State().AddMessage(Message{
		Role:    "user",
		Content: "Look at this",
		Images:  []ImageData{{URL: "http://example.com/img.png", MIMEType: "image/png"}},
	})

	ch := newConversationHandler(a)
	messages := ch.prepareMessages()

	// Find the user message
	for _, msg := range messages {
		if msg.Role == "user" {
			if len(msg.Images) != 1 {
				t.Error("expected images to be kept for vision model")
			}
			return
		}
	}
	t.Error("user message not found")
}

func TestPrepareMessages_CollapseSystemMessages(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "First system"},
		{Role: "user", Content: "hi"},
		{Role: "system", Content: "Second system"},
		{Role: "assistant", Content: "hello"},
	}

	result := collapseSystemMessages(messages)

	// Should have: merged system + user + assistant
	if len(result) != 3 {
		t.Errorf("expected 3 messages, got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Error("first message should be system")
	}
	if result[0].Content != "First system\n\nSecond system" {
		t.Errorf("expected merged system content, got %q", result[0].Content)
	}
}

func TestRemoveOrphanedToolResults(t *testing.T) {
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
	})

	ch := newConversationHandler(a)
	messages := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "let me search", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{Name: "search"}},
		}},
		{Role: "tool", Content: "results", ToolCallID: "call_1"},
		{Role: "tool", Content: "orphaned", ToolCallID: "call_999"}, // orphaned
	}

	result := ch.removeOrphanedToolResults(messages)

	// Should have 4 messages (orphaned removed)
	if len(result) != 4 {
		t.Errorf("expected 4 messages, got %d", len(result))
	}
	// Verify orphaned is gone
	for _, msg := range result {
		if msg.ToolCallID == "call_999" {
			t.Error("orphaned tool result should be removed")
		}
	}
}

func TestProcessQuery_EmptyChoices(t *testing.T) {
	provider := &mockProvider{
		chatResp: &ChatResponse{
			Choices: []ChatChoice{},
			Usage:   ChatUsage{TotalTokens: 5},
		},
		info:       ProviderInfo{ContextSize: 10000},
		tokenCount: 100,
	}
	a := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	result, err := a.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty result for empty choices, got %q", result)
	}
}

func TestProcessQuery_ContextCompaction(t *testing.T) {
	provider := &mockProvider{
		chatResp: &ChatResponse{
			Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: "done"}}},
			Usage:   ChatUsage{TotalTokens: 10},
		},
		info:       ProviderInfo{ContextSize: 50}, // Very small context
		tokenCount: 100,                           // Over context limit
	}
	a := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	// Add many messages to trigger compaction
	for i := 0; i < 30; i++ {
		a.State().AddMessage(Message{Role: "user", Content: "user message " + string(rune('a'+i%26))})
		a.State().AddMessage(Message{Role: "assistant", Content: "assistant response " + string(rune('a'+i%26))})
	}

	_, err := a.Run(context.Background(), "final query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProcessQuery_ProviderErrorPublishesErrorEvent(t *testing.T) {
	provider := &mockProvider{
		chatErr: fmt.Errorf("provider down"),
		info:    ProviderInfo{ContextSize: 10000},
	}
	bus := events.NewEventBus()
	ch := bus.Subscribe("test")

	a := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
		EventBus: bus,
	})

	_, err := a.Run(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error from provider failure")
	}

	// With retry logic: 3 retry error events (from chatFn) + 1 final error event (from runLoop) = 4 error events
	// First event is query_started
	evt1 := <-ch
	if evt1.Type != events.EventTypeQueryStarted {
		t.Fatalf("expected first event %q, got %q", events.EventTypeQueryStarted, evt1.Type)
	}

	// Collect all error events
	var errorEvents []events.UIEvent
	for {
		select {
		case evt := <-ch:
			if evt.Type == events.EventTypeError {
				errorEvents = append(errorEvents, evt)
			}
		default:
			goto done
		}
	}
done:

	if len(errorEvents) == 0 {
		t.Fatal("expected at least 1 error event, got 0")
	}

	// The first error event is from the first retry attempt (chatFn)
	// It should contain the original error message
	data, ok := errorEvents[0].Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map data, got %T", errorEvents[0].Data)
	}
	if data["message"] != "chat failed" {
		t.Errorf("expected message 'chat failed', got %v", data["message"])
	}
	errStr, ok := data["error"].(string)
	if !ok {
		t.Fatalf("expected error string, got %T", data["error"])
	}
	if !strings.Contains(errStr, "provider down") {
		t.Errorf("expected error to contain 'provider down', got %v", errStr)
	}
}

func TestProcessQuery_ProviderErrorNoEventBus(t *testing.T) {
	provider := &mockProvider{
		chatErr: fmt.Errorf("provider down"),
		info:    ProviderInfo{ContextSize: 10000},
	}

	a := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
		// No EventBus — should not panic
	})

	_, err := a.Run(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error from provider failure")
	}
}

func TestProcessQuery_MetricsUpdateEvent(t *testing.T) {
	provider := &mockProvider{
		chatResp: &ChatResponse{
			Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: "done"}}},
			Usage:   ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
		info:       ProviderInfo{ContextSize: 10000},
		tokenCount: 100,
	}
	bus := events.NewEventBus()
	ch := bus.Subscribe("test")

	a := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
		EventBus: bus,
	})

	_, err := a.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Event 1: query_started
	evt1 := <-ch
	if evt1.Type != events.EventTypeQueryStarted {
		t.Fatalf("expected first event %q, got %q", events.EventTypeQueryStarted, evt1.Type)
	}

	// Event 2: metrics_update
	evt := <-ch
	if evt.Type != events.EventTypeMetricsUpdate {
		t.Fatalf("expected event type %q, got %q", events.EventTypeMetricsUpdate, evt.Type)
	}
	data, ok := evt.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map data, got %T", evt.Data)
	}
	if data["total_tokens"] != 15 {
		t.Errorf("expected total_tokens=15, got %v", data["total_tokens"])
	}
	if data["context_tokens"] != 10 {
		t.Errorf("expected context_tokens=10, got %v", data["context_tokens"])
	}
	if data["max_context_tokens"] != 10000 {
		t.Errorf("expected max_context_tokens=10000, got %v", data["max_context_tokens"])
	}
	if data["iteration"] != 0 {
		t.Errorf("expected iteration=0, got %v", data["iteration"])
	}
	if data["total_cost"] != 0.0 {
		t.Errorf("expected total_cost=0.0, got %v", data["total_cost"])
	}

	// Event 3: query_completed
	evt3 := <-ch
	if evt3.Type != events.EventTypeQueryCompleted {
		t.Errorf("expected event type %q, got %q", events.EventTypeQueryCompleted, evt3.Type)
	}

	// Event 4: agent_message
	evt4 := <-ch
	if evt4.Type != events.EventTypeAgentMessage {
		t.Errorf("expected event type %q, got %q", events.EventTypeAgentMessage, evt4.Type)
	}
	data4, ok := evt4.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map data, got %T", evt4.Data)
	}
	if data4["category"] != "info" {
		t.Errorf("expected category 'info', got %v", data4["category"])
	}
	if data4["message"] != "done" {
		t.Errorf("expected message 'done', got %v", data4["message"])
	}

	// No more events
	select {
	case extra := <-ch:
		t.Errorf("unexpected extra event: %v", extra.Type)
	default:
	}
}

func TestProcessQuery_MetricsUpdateNotPublishedWhenNoTokens(t *testing.T) {
	provider := &mockProvider{
		chatResp: &ChatResponse{
			Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: "done"}}},
			Usage:   ChatUsage{TotalTokens: 0},
		},
		info:       ProviderInfo{ContextSize: 10000},
		tokenCount: 100,
	}
	bus := events.NewEventBus()
	ch := bus.Subscribe("test")

	a := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
		EventBus: bus,
	})

	_, err := a.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Event 1: query_started
	evt1 := <-ch
	if evt1.Type != events.EventTypeQueryStarted {
		t.Fatalf("expected first event %q, got %q", events.EventTypeQueryStarted, evt1.Type)
	}

	// Event 2: query_completed (no metrics_update since TotalTokens == 0)
	evt2 := <-ch
	if evt2.Type != events.EventTypeQueryCompleted {
		t.Errorf("expected event type %q, got %q", events.EventTypeQueryCompleted, evt2.Type)
	}

	// Event 3: agent_message (should follow query_completed)
	evt3 := <-ch
	if evt3.Type != events.EventTypeAgentMessage {
		t.Errorf("expected event type %q, got %q", events.EventTypeAgentMessage, evt3.Type)
	}
	data, ok := evt3.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map data, got %T", evt3.Data)
	}
	if data["category"] != "info" {
		t.Errorf("expected category 'info', got %v", data["category"])
	}
	if data["message"] != "done" {
		t.Errorf("expected message 'done', got %v", data["message"])
	}

	// No more events
	select {
	case extra := <-ch:
		t.Errorf("unexpected extra event: %v", extra.Type)
	default:
	}
}

func TestProcessQuery_AgentMessageEvent(t *testing.T) {
	provider := &mockProvider{
		chatResp: &ChatResponse{
			Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: "final answer"}}},
			Usage:   ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
		info:       ProviderInfo{ContextSize: 10000},
		tokenCount: 100,
	}
	bus := events.NewEventBus()
	ch := bus.Subscribe("test")

	a := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
		EventBus: bus,
	})

	_, err := a.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Collect all events to verify ordering
	var evts []events.UIEvent
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				// Channel closed (unsubscribe was called)
				goto done
			}
			evts = append(evts, evt)
		default:
			goto done
		}
	}
done:

	// Verify EventTypeQueryCompleted comes before EventTypeAgentMessage
	queryCompletedIdx := -1
	agentMessageIdx := -1
	for i, evt := range evts {
		if evt.Type == events.EventTypeQueryCompleted {
			queryCompletedIdx = i
		}
		if evt.Type == events.EventTypeAgentMessage {
			agentMessageIdx = i
		}
	}
	if queryCompletedIdx < 0 {
		t.Error("expected EventTypeQueryCompleted in events")
	}
	if agentMessageIdx < 0 {
		t.Error("expected EventTypeAgentMessage in events")
	}
	if agentMessageIdx < queryCompletedIdx {
		t.Errorf("EventTypeAgentMessage should come after EventTypeQueryCompleted (indices: agent_message=%d, query_completed=%d)", agentMessageIdx, queryCompletedIdx)
	}

	// Verify EventTypeAgentMessage data
	if agentMessageIdx >= 0 {
		evt := evts[agentMessageIdx]
		data, ok := evt.Data.(map[string]interface{})
		if !ok {
			t.Fatalf("expected map data for agent_message, got %T", evt.Data)
		}
		if data["category"] != "info" {
			t.Errorf("expected category 'info', got %v", data["category"])
		}
		if data["message"] != "final answer" {
			t.Errorf("expected message 'final answer', got %v", data["message"])
		}
	}
}

func TestProcessQuery_AgentMessageNotPublishedForEmptyContent(t *testing.T) {
	provider := &mockProvider{
		chatResp: &ChatResponse{
			Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: ""}}},
			Usage:   ChatUsage{TotalTokens: 0},
		},
		info:       ProviderInfo{ContextSize: 10000},
		tokenCount: 100,
	}
	bus := events.NewEventBus()
	ch := bus.Subscribe("test")

	a := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
		EventBus: bus,
	})

	_, err := a.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Event 1: query_started
	evt1 := <-ch
	if evt1.Type != events.EventTypeQueryStarted {
		t.Fatalf("expected first event %q, got %q", events.EventTypeQueryStarted, evt1.Type)
	}

	// Event 2: query_completed
	evt2 := <-ch
	if evt2.Type != events.EventTypeQueryCompleted {
		t.Errorf("expected event type %q, got %q", events.EventTypeQueryCompleted, evt2.Type)
	}

	// No agent_message should be published for empty content
	select {
	case extra := <-ch:
		t.Errorf("unexpected extra event: %v (expected no agent_message for empty content)", extra.Type)
	default:
	}
}
