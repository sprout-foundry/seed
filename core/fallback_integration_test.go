package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sprout-foundry/seed/events"
)

type testMP struct {
	responses []*ChatResponse
	idx       int
}

func (m *testMP) Chat(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
	if m.idx >= len(m.responses) {
		return nil, fmt.Errorf("testMP: no more responses configured (got %d calls)", m.idx+1)
	}
	r := m.responses[m.idx]
	m.idx++
	return r, nil
}
func (m *testMP) ChatStream(_ context.Context, _ *ChatRequest, _ StreamHandler) error { return nil }
func (m *testMP) Info() ProviderInfo                                                  { return ProviderInfo{ContextSize: 10000} }
func (m *testMP) EstimateTokens(_ *ChatRequest) int                                   { return 10 }

// drainEvents drains all events from the channel with a timeout to avoid hanging
// if a background goroutine is still publishing.
func drainEvents(ch <-chan events.UIEvent) []events.UIEvent {
	var evts []events.UIEvent
	timeout := time.After(2 * time.Second)
	drainDone := false
	for !drainDone {
		select {
		case evt := <-ch:
			evts = append(evts, evt)
		case <-time.After(50 * time.Millisecond):
			drainDone = true
		case <-timeout:
			drainDone = true
		}
	}
	return evts
}

func findEventType(evts []events.UIEvent, eventType string) bool {
	for _, e := range evts {
		if e.Type == eventType {
			return true
		}
	}
	return false
}

func TestFallback_JSONFence(t *testing.T) {
	bt := "```"
	content := "Let me search.\n\n" + bt + "json\n{\"name\": \"search\", \"arguments\": {\"query\": \"test\"}}\n" + bt + "\nResults."
	p := &testMP{
		responses: []*ChatResponse{
			{Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: content}}}, Usage: ChatUsage{TotalTokens: 15}},
			{Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: "Done."}}}, Usage: ChatUsage{TotalTokens: 10}},
		},
	}
	e := &mockExecutor{
		tools:   []Tool{{Name: "search", Description: "Search", Parameters: ToolParameters{Type: "object"}}},
		results: []Message{{Role: "tool", Content: "Results"}},
	}
	bus := events.NewEventBus()
	ch := bus.Subscribe("t")
	a, _ := NewAgent(Options{Provider: p, Executor: e, EventBus: bus})
	_, err := a.Run(context.Background(), "search")
	if err != nil {
		t.Fatal(err)
	}
	evts := drainEvents(ch)
	if !findEventType(evts, events.EventTypeToolStart) {
		t.Error("expected tool_start event from fallback-parsed tool call")
	}
}

func TestFallback_NoPatterns(t *testing.T) {
	p := &testMP{
		responses: []*ChatResponse{
			{Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: "This is a normal response."}}}, Usage: ChatUsage{TotalTokens: 10}},
		},
	}
	e := &mockExecutor{
		tools:   []Tool{{Name: "search", Description: "Search", Parameters: ToolParameters{Type: "object"}}},
		results: []Message{},
	}
	bus := events.NewEventBus()
	ch := bus.Subscribe("t")
	a, _ := NewAgent(Options{Provider: p, Executor: e, EventBus: bus})
	result, err := a.Run(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "normal response") {
		t.Errorf("got %q", result)
	}
	evts := drainEvents(ch)
	if findEventType(evts, events.EventTypeToolStart) {
		t.Error("expected no tool_start events")
	}
}

func TestFallback_XMLToolCalls(t *testing.T) {
	content := "I will search.\n\n<function=search>\n{\"query\": \"hello\"}\n</function=search>\n\nDone."
	p := &testMP{
		responses: []*ChatResponse{
			{Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: content}}}, Usage: ChatUsage{TotalTokens: 15}},
			{Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: "Done."}}}, Usage: ChatUsage{TotalTokens: 10}},
		},
	}
	e := &mockExecutor{
		tools:   []Tool{{Name: "search", Description: "Search", Parameters: ToolParameters{Type: "object"}}},
		results: []Message{{Role: "tool", Content: "Results"}},
	}
	a, _ := NewAgent(Options{Provider: p, Executor: e})
	_, err := a.Run(context.Background(), "search hello")
	if err != nil {
		t.Fatal(err)
	}
}

func TestFallback_StructuredSkipsFallback(t *testing.T) {
	p := &testMP{
		responses: []*ChatResponse{
			{
				Choices: []ChatChoice{{
					Message: Message{
						Role:    "assistant",
						Content: "Let me search.",
						ToolCalls: []ToolCall{
							{ID: "call_structured", Type: "function", Function: ToolCallFunction{Name: "search", Arguments: `{"query":"test"}`}},
						},
					},
				}},
				Usage: ChatUsage{TotalTokens: 15},
			},
			{Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: "Done."}}}, Usage: ChatUsage{TotalTokens: 10}},
		},
	}
	e := &mockExecutor{
		tools:   []Tool{{Name: "search", Description: "Search", Parameters: ToolParameters{Type: "object"}}},
		results: []Message{{Role: "tool", Content: "Results", ToolCallID: "call_structured"}},
	}
	bus := events.NewEventBus()
	ch := bus.Subscribe("t")
	a, _ := NewAgent(Options{Provider: p, Executor: e, EventBus: bus})
	_, err := a.Run(context.Background(), "search test")
	if err != nil {
		t.Fatal(err)
	}
	evts := drainEvents(ch)
	toolStartFound := false
	for _, evt := range evts {
		if evt.Type == events.EventTypeToolStart {
			toolStartFound = true
			data := evt.Data.(map[string]interface{})
			if tcID, ok := data["tool_call_id"].(string); ok && strings.HasPrefix(tcID, "fallback_") {
				t.Error("expected structured tool call, not fallback")
			}
		}
	}
	if !toolStartFound {
		t.Error("expected tool_start event from structured tool call")
	}
}

func TestFallback_UnknownToolFiltered(t *testing.T) {
	bt := "```"
	content := "I will do something.\n\n" + bt + "json\n{\"name\": \"unknown_tool\", \"arguments\": {\"foo\": \"bar\"}}\n" + bt + "\nDone."
	p := &testMP{
		responses: []*ChatResponse{
			{Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: content}}}, Usage: ChatUsage{TotalTokens: 15}},
			{Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: "That's all."}}}, Usage: ChatUsage{TotalTokens: 10}},
		},
	}
	e := &mockExecutor{
		tools:   []Tool{{Name: "search", Description: "Search", Parameters: ToolParameters{Type: "object"}}},
		results: []Message{},
	}
	bus := events.NewEventBus()
	ch := bus.Subscribe("t")
	a, _ := NewAgent(Options{Provider: p, Executor: e, EventBus: bus})
	_, err := a.Run(context.Background(), "do something")
	if err != nil {
		t.Fatal(err)
	}
	evts := drainEvents(ch)
	if findEventType(evts, events.EventTypeToolStart) {
		t.Error("expected no tool_start events for unknown tool name")
	}
}

func TestFallback_FunctionNamePattern(t *testing.T) {
	content := "Let me search.\n\nname: search\narguments: {\"query\": \"test\"}\n\nDone."
	p := &testMP{
		responses: []*ChatResponse{
			{Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: content}}}, Usage: ChatUsage{TotalTokens: 15}},
			{Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: "Done."}}}, Usage: ChatUsage{TotalTokens: 10}},
		},
	}
	e := &mockExecutor{
		tools:   []Tool{{Name: "search", Description: "Search", Parameters: ToolParameters{Type: "object"}}},
		results: []Message{{Role: "tool", Content: "Found it"}},
	}
	bus := events.NewEventBus()
	ch := bus.Subscribe("t")
	a, _ := NewAgent(Options{Provider: p, Executor: e, EventBus: bus})
	_, err := a.Run(context.Background(), "search")
	if err != nil {
		t.Fatal(err)
	}
	evts := drainEvents(ch)
	if !findEventType(evts, events.EventTypeToolStart) {
		t.Error("expected tool_start event from name:pattern fallback")
	}
}
