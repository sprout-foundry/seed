package core

// Message represents a single message in a conversation.
type Message struct {
	Role             string      `json:"role"`
	Content          string      `json:"content"`
	ReasoningContent string      `json:"reasoning_content,omitempty"`
	ToolCallID       string      `json:"tool_call_id,omitempty"`
	ToolCalls        []ToolCall  `json:"tool_calls,omitempty"`
	Images           []ImageData `json:"images,omitempty"`
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
// The structure matches the OpenAI function-calling wire format where
// tool definitions are nested under a "function" key.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction describes a tool's identity and parameter schema.
type ToolFunction struct {
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
// Images are typically provided as base64-encoded strings.
type ImageData struct {
	URL    string `json:"url,omitempty"`
	Base64 string `json:"base64,omitempty"`
	Type   string `json:"type,omitempty"` // MIME type (image/jpeg, image/png, etc.)
}

// ChatRequest is a request to the chat completion endpoint.
type ChatRequest struct {
	Model      string    `json:"model,omitempty"`
	Messages   []Message `json:"messages"`
	Tools      []Tool    `json:"tools,omitempty"`
	ToolChoice string    `json:"tool_choice,omitempty"`
	MaxTokens  int       `json:"max_tokens,omitempty"`
	Reasoning  string    `json:"reasoning,omitempty"`
	Stream     bool      `json:"stream,omitempty"`
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

// ChatUsage tracks token usage and cost for a request/response.
type ChatUsage struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	EstimatedCost    float64 `json:"estimated_cost,omitempty"`
	Cost             float64 `json:"cost,omitempty"`
}

// AgentState tracks the state of an agent's conversation.
type AgentState struct {
	Messages         []Message        `json:"messages"`
	SessionID        string           `json:"session_id"`
	TotalTokens      int              `json:"total_tokens"`
	TotalCost        float64          `json:"total_cost"`
	PromptTokens     int              `json:"prompt_tokens"`
	CompletionTokens int              `json:"completion_tokens"`
	Checkpoints      []TurnCheckpoint `json:"checkpoints,omitempty"`
}

// ToMessage returns the first choice's message from the response, or an empty message.
func (r ChatResponse) ToMessage() Message {
	if len(r.Choices) == 0 {
		return Message{}
	}
	return r.Choices[0].Message
}
