package core

import "context"

// ProviderInfo contains metadata about a provider and its model.
type ProviderInfo struct {
	Model       string `json:"model"`
	ContextSize int    `json:"context_size"`
	HasVision   bool   `json:"has_vision"`
}

// Provider represents an LLM provider that can be used for chat completions.
type Provider interface {
	// Chat sends a chat request and returns the response.
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)

	// ChatStream sends a chat request and streams the response via the handler.
	ChatStream(ctx context.Context, req *ChatRequest, handler StreamHandler) error

	// Info returns metadata about the provider and its model.
	Info() ProviderInfo

	// EstimateTokens returns an approximate token count for the request.
	EstimateTokens(req *ChatRequest) int
}

// StreamHandler handles streamed responses from a Provider.
type StreamHandler interface {
	OnContent(content string)
	OnReasoning(reasoning string)
	OnDone(resp *ChatResponse)
	OnError(err error)
}

// ToolExecutor represents a system that can execute tool calls.
type ToolExecutor interface {
	// GetTools returns the list of available tools.
	GetTools() []Tool

	// Execute runs the given tool calls and returns the resulting messages.
	Execute(ctx context.Context, calls []ToolCall) []Message
}

// UI represents a user interface for prompting and output.
type UI interface {
	// Prompt displays a prompt and returns the user's input.
	Prompt(message string) (string, error)

	// Confirm displays a confirmation message and returns the user's choice.
	Confirm(message string) (bool, error)

	// Print writes output without a trailing newline.
	Print(message string)

	// PrintLine writes output with a trailing newline.
	PrintLine(message string)
}
