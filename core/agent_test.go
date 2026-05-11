package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// --- Mock implementations ---

type mockProvider struct {
	chatResp   *ChatResponse
	chatErr    error
	streamErr  error
	info       ProviderInfo
	tokenCount int
}

func (m *mockProvider) Chat(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
	return m.chatResp, m.chatErr
}
func (m *mockProvider) ChatStream(_ context.Context, _ *ChatRequest, _ StreamHandler) error {
	return m.streamErr
}
func (m *mockProvider) Info() ProviderInfo {
	return m.info
}
func (m *mockProvider) EstimateTokens(_ *ChatRequest) int {
	return m.tokenCount
}

type mockExecutor struct {
	tools   []Tool
	results []Message
}

func (m *mockExecutor) GetTools() []Tool {
	return m.tools
}
func (m *mockExecutor) Execute(_ context.Context, calls []ToolCall) []Message {
	return m.results
}

type mockUI struct {
	promptResp   string
	promptErr    error
	confirmResp  bool
	confirmErr   error
	printBuf     strings.Builder
	printLineBuf strings.Builder
}

func (m *mockUI) Prompt(_ string) (string, error) {
	return m.promptResp, m.promptErr
}
func (m *mockUI) Confirm(_ string) (bool, error) {
	return m.confirmResp, m.confirmErr
}
func (m *mockUI) Print(s string) {
	m.printBuf.WriteString(s)
}
func (m *mockUI) PrintLine(s string) {
	m.printLineBuf.WriteString(s + "\n")
}

// --- Agent tests ---

func TestNewAgent_PanicsOnNilProvider(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil Provider")
		}
	}()
	NewAgent(Options{
		Executor: &mockExecutor{},
	})
}

func TestNewAgent_PanicsOnNilExecutor(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil ToolExecutor")
		}
	}()
	NewAgent(Options{
		Provider: &mockProvider{},
	})
}

func TestNewAgent_DefaultSystemPrompt(t *testing.T) {
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
	})
	if a.systemPrompt != DefaultSystemPrompt {
		t.Errorf("expected default system prompt, got %q", a.systemPrompt)
	}
}

func TestNewAgent_CustomSystemPrompt(t *testing.T) {
	custom := "You are a coding assistant."
	a := NewAgent(Options{
		Provider:     &mockProvider{},
		Executor:     &mockExecutor{},
		SystemPrompt: custom,
	})
	if a.systemPrompt != custom {
		t.Errorf("expected %q, got %q", custom, a.systemPrompt)
	}
}

func TestNewAgent_WhitespaceSystemPrompt(t *testing.T) {
	a := NewAgent(Options{
		Provider:     &mockProvider{},
		Executor:     &mockExecutor{},
		SystemPrompt: "   \t\n  ",
	})
	if a.systemPrompt != DefaultSystemPrompt {
		t.Errorf("expected default system prompt for whitespace, got %q", a.systemPrompt)
	}
}

func TestAgent_SetSystemPrompt(t *testing.T) {
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
	})
	a.SetSystemPrompt("New prompt")
	if a.systemPrompt != "New prompt" {
		t.Errorf("expected 'New prompt', got %q", a.systemPrompt)
	}
}

func TestAgent_PauseAndResume(t *testing.T) {
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
	})
	if a.IsPaused() {
		t.Error("expected not paused initially")
	}
	a.Pause()
	if !a.IsPaused() {
		t.Error("expected paused after Pause()")
	}
	a.Resume()
	if a.IsPaused() {
		t.Error("expected not paused after Resume()")
	}
}

func TestAgent_Run_WhenPaused(t *testing.T) {
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
	})
	a.Pause()
	_, err := a.Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error when paused")
	}
	if !strings.Contains(err.Error(), "paused") {
		t.Errorf("expected 'paused' in error, got: %v", err)
	}
}

func TestAgent_Run_CompleteResponse(t *testing.T) {
	provider := &mockProvider{
		chatResp: &ChatResponse{
			Choices: []ChatChoice{{Message: Message{Role: "assistant", Content: "Hello!"}}},
			Usage:   ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
		info:       ProviderInfo{ContextSize: 10000},
		tokenCount: 100,
	}
	executor := &mockExecutor{}
	a := NewAgent(Options{
		Provider: provider,
		Executor: executor,
	})

	result, err := a.Run(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello!" {
		t.Errorf("expected 'Hello!', got %q", result)
	}

	// Check state
	st := a.State()
	if st.TotalTokens() != 15 {
		t.Errorf("expected 15 total tokens, got %d", st.TotalTokens())
	}
}

func TestAgent_Run_WithToolCalls(t *testing.T) {
	provider := &mockProvider{
		chatResp: &ChatResponse{
			Choices: []ChatChoice{{
				Message: Message{
					Role:    "assistant",
					Content: "Let me search.",
					ToolCalls: []ToolCall{{
						ID:       "call_1",
						Function: ToolCallFunction{Name: "search", Arguments: `{"query":"test"}`},
					}},
				},
			}},
			Usage: ChatUsage{TotalTokens: 20},
		},
		info:       ProviderInfo{ContextSize: 10000},
		tokenCount: 100,
	}
	executor := &mockExecutor{
		results: []Message{{Role: "tool", Content: "Search results", ToolCallID: "call_1"}},
	}
	a := NewAgent(Options{
		Provider:      provider,
		Executor:      executor,
		MaxIterations: 2,
	})

	_, err := a.Run(context.Background(), "Search for something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	st := a.State()
	msgs := st.Messages()
	// Should have: user, assistant (with tool call), tool result
	if len(msgs) < 3 {
		t.Errorf("expected at least 3 messages, got %d", len(msgs))
	}
}

func TestAgent_Run_MaxIterations(t *testing.T) {
	provider := &mockProvider{
		chatResp: &ChatResponse{
			Choices: []ChatChoice{{
				Message: Message{
					Role: "assistant",
					ToolCalls: []ToolCall{{
						ID:       "call_1",
						Function: ToolCallFunction{Name: "loop", Arguments: "{}"},
					}},
				},
			}},
			Usage: ChatUsage{TotalTokens: 10},
		},
		info:       ProviderInfo{ContextSize: 10000},
		tokenCount: 100,
	}
	executor := &mockExecutor{
		results: []Message{{Role: "tool", Content: "loop", ToolCallID: "call_1"}},
	}
	a := NewAgent(Options{
		Provider:      provider,
		Executor:      executor,
		MaxIterations: 3,
	})

	_, err := a.Run(context.Background(), "Loop")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should stop after 3 iterations
}

func TestAgent_Run_ProviderError(t *testing.T) {
	provider := &mockProvider{
		chatErr: fmt.Errorf("provider down"),
		info:    ProviderInfo{ContextSize: 10000},
	}
	a := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})

	_, err := a.Run(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "LLM request failed") {
		t.Errorf("expected 'LLM request failed' in error, got: %v", err)
	}
}

func TestAgent_ExportImportState(t *testing.T) {
	a := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
	})
	a.State().AddMessage(Message{Role: "user", Content: "hello"})
	a.State().SetSessionID("test-session")
	a.State().AddTokens(100, 50, 150)

	data, err := a.ExportState()
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}

	a2 := NewAgent(Options{
		Provider: &mockProvider{},
		Executor: &mockExecutor{},
	})
	if err := a2.ImportState(data); err != nil {
		t.Fatalf("import failed: %v", err)
	}

	st := a2.State()
	if st.SessionID() != "test-session" {
		t.Errorf("expected session 'test-session', got %q", st.SessionID())
	}
	if st.TotalTokens() != 150 {
		t.Errorf("expected 150 tokens, got %d", st.TotalTokens())
	}
	msgs := st.Messages()
	if len(msgs) != 1 || msgs[0].Content != "hello" {
		t.Errorf("expected 1 message 'hello', got %d messages", len(msgs))
	}
}

func TestAgent_ProviderAccess(t *testing.T) {
	provider := &mockProvider{
		info: ProviderInfo{Model: "test-model", ContextSize: 128000},
	}
	a := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
	})
	info := a.Provider().Info()
	if info.Model != "test-model" {
		t.Errorf("expected 'test-model', got %q", info.Model)
	}
}
