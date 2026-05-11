package test

import (
	"context"
	"fmt"
	"sync"

	"github.com/sprout-foundry/seed/core"
)

// MockProvider is a scriptable mock implementation of core.Provider.
// Configure it with a sequence of responses via AddResponse or SetResponses.
// It records every Chat call for assertion.
type MockProvider struct {
	mu         sync.Mutex
	responses  []*core.ChatResponse
	errors     []error
	respIndex  int
	streamErr  error
	info       core.ProviderInfo
	tokenCount int

	// Recorded calls for assertion
	Calls []*core.ChatRequest
}

// NewMockProvider creates a MockProvider with default info.
func NewMockProvider() *MockProvider {
	return &MockProvider{
		info: core.ProviderInfo{
			Model:       "mock-model",
			ContextSize: 128000,
			HasVision:   false,
		},
		tokenCount: 100,
	}
}

// WithInfo sets the provider info.
func (m *MockProvider) WithInfo(info core.ProviderInfo) *MockProvider {
	m.info = info
	return m
}

// WithTokenEstimate sets the token estimate returned by EstimateTokens.
func (m *MockProvider) WithTokenEstimate(count int) *MockProvider {
	m.tokenCount = count
	return m
}

// AddResponse appends a response to the sequence.
// The next call to Chat will return this response.
func (m *MockProvider) AddResponse(resp *core.ChatResponse) *MockProvider {
	m.responses = append(m.responses, resp)
	m.errors = append(m.errors, nil)
	return m
}

// AddError appends an error to the sequence.
// The next call to Chat will return this error.
func (m *MockProvider) AddError(err error) *MockProvider {
	m.responses = append(m.responses, nil)
	m.errors = append(m.errors, err)
	return m
}

// SetResponses replaces the entire response sequence.
func (m *MockProvider) SetResponses(resps []*core.ChatResponse) *MockProvider {
	m.responses = resps
	m.errors = make([]error, len(resps))
	return m
}

// AddToolCallResponse adds a response that includes tool calls.
func (m *MockProvider) AddToolCallResponse(content string, toolCalls ...core.ToolCall) *MockProvider {
	m.AddResponse(&core.ChatResponse{
		Choices: []core.ChatChoice{{
			Message: core.Message{
				Role:      "assistant",
				Content:   content,
				ToolCalls: toolCalls,
			},
		}},
		Usage: core.ChatUsage{
			PromptTokens:     50,
			CompletionTokens: 30,
			TotalTokens:      80,
		},
	})
	return m
}

// AddTextResponse adds a simple text response (no tool calls).
func (m *MockProvider) AddTextResponse(content string) *MockProvider {
	m.AddResponse(&core.ChatResponse{
		Choices: []core.ChatChoice{{
			Message: core.Message{
				Role:    "assistant",
				Content: content,
			},
		}},
		Usage: core.ChatUsage{
			PromptTokens:     20,
			CompletionTokens: 15,
			TotalTokens:      35,
		},
	})
	return m
}

// Chat implements core.Provider.
func (m *MockProvider) Chat(_ context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Deep-copy the request for recording
	reqCopy := *req
	reqCopy.Messages = make([]core.Message, len(req.Messages))
	copy(reqCopy.Messages, req.Messages)
	m.Calls = append(m.Calls, &reqCopy)

	if m.respIndex >= len(m.responses) {
		return nil, fmt.Errorf("mock provider: no more responses configured (got %d calls)", m.respIndex+1)
	}

	err := m.errors[m.respIndex]
	resp := m.responses[m.respIndex]
	m.respIndex++

	if err != nil {
		return nil, err
	}
	return resp, nil
}

// ChatStream implements core.Provider.
func (m *MockProvider) ChatStream(_ context.Context, _ *core.ChatRequest, _ core.StreamHandler) error {
	return m.streamErr
}

// Info implements core.Provider.
func (m *MockProvider) Info() core.ProviderInfo {
	return m.info
}

// EstimateTokens implements core.Provider.
func (m *MockProvider) EstimateTokens(_ *core.ChatRequest) int {
	return m.tokenCount
}

// Reset clears all recorded calls and resets the response index.
func (m *MockProvider) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = nil
	m.respIndex = 0
}

// CallCount returns the number of Chat calls made.
func (m *MockProvider) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Calls)
}

// LastRequest returns the last Chat request, or nil.
func (m *MockProvider) LastRequest() *core.ChatRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.Calls) == 0 {
		return nil
	}
	return m.Calls[len(m.Calls)-1]
}
