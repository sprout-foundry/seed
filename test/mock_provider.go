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

	// blockUntil, when non-nil, causes the next Chat call to block
	// until the channel is closed (useful for cancellation tests).
	blockUntil chan struct{}

	// blockOnCall, when set, blocks on the Nth call (1-indexed).
	// blockOnCh is the channel to wait on; close it to unblock.
	blockOnCall int
	blockOnCh   chan struct{}

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

// BlockUntil causes the next Chat call to block until the returned channel
// is closed. Use this in cancellation tests: cancel the channel to unblock
// the provider so the context cancellation is observed.
func (m *MockProvider) BlockUntil() chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan struct{})
	m.blockUntil = ch
	return ch
}

// BlockOnCallN causes the Nth Chat call (1-indexed) to block until the
// returned channel is closed. This is useful for cancellation tests where
// you want the first N-1 calls to succeed normally.
func (m *MockProvider) BlockOnCallN(n int) chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan struct{})
	m.blockOnCall = n
	m.blockOnCh = ch
	return ch
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
func (m *MockProvider) Chat(ctx context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
	m.mu.Lock()

	// Deep-copy the request for recording
	reqCopy := *req
	reqCopy.Messages = make([]core.Message, len(req.Messages))
	copy(reqCopy.Messages, req.Messages)
	m.Calls = append(m.Calls, &reqCopy)
	callNum := len(m.Calls)

	if m.respIndex >= len(m.responses) {
		m.mu.Unlock()
		return nil, fmt.Errorf("mock provider: no more responses configured (got %d calls)", m.respIndex+1)
	}

	err := m.errors[m.respIndex]
	resp := m.responses[m.respIndex]
	m.respIndex++

	// Capture blockUntil and clear it before releasing the lock
	block := m.blockUntil
	m.blockUntil = nil

	// Check if we should block on this specific call number
	var blockOnCh chan struct{}
	if m.blockOnCall > 0 && callNum == m.blockOnCall {
		blockOnCh = m.blockOnCh
	}
	m.mu.Unlock()

	// Block until the channel is closed or context is cancelled
	if block != nil || blockOnCh != nil {
		ch := block
		if ch == nil {
			ch = blockOnCh
		}
		// Check context first to avoid race with channel close
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		select {
		case <-ch:
			// Channel closed — proceed normally
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

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
