package core

// Message represents a single message in a conversation.
type Message struct {
	Role       string      `json:"role"`
	Content    string      `json:"content"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	Images     []ImageData `json:"images,omitempty"`
}

// ToolCall represents a function call requested by the model.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction represents the function details of a tool call.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool represents a tool definition that can be provided to the model.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  ToolParameters `json:"parameters"`
}

// ToolParameters defines the schema for a tool's arguments.
type ToolParameters struct {
	Type       string                   `json:"type"`
	Properties map[string]ToolParameter `json:"properties"`
	Required   []string                 `json:"required,omitempty"`
}

// ToolParameter defines a single parameter within a tool's schema.
type ToolParameter struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// ImageData represents an image to include in a message.
type ImageData struct {
	URL      string `json:"url,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
	Data     []byte `json:"-"`
}

// ChatRequest is a request to the chat completion endpoint.
type ChatRequest struct {
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

// ChatResponse is a response from the chat completion endpoint.
type ChatResponse struct {
	ID      string       `json:"id"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   ChatUsage    `json:"usage"`
}

// ChatChoice represents one possible completion from the model.
type ChatChoice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// ChatUsage tracks token usage for a request/response.
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// AgentState tracks the state of an agent's conversation.
type AgentState struct {
	Messages         []Message `json:"messages"`
	SessionID        string    `json:"session_id"`
	TotalTokens      int       `json:"total_tokens"`
	TotalCost        float64   `json:"total_cost"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
}

// ToMessage returns the first choice's message from the response, or an empty message.
func (r ChatResponse) ToMessage() Message {
	if len(r.Choices) == 0 {
		return Message{}
	}
	return r.Choices[0].Message
}
