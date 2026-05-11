package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/sprout-foundry/seed/events"
)

// DefaultSystemPrompt is the minimal system prompt used when none is provided.
const DefaultSystemPrompt = "You are a helpful assistant that can execute tools to complete tasks."

// Options configures an Agent.
type Options struct {
	Provider      Provider     // required — LLM communication
	Executor      ToolExecutor // required — tool execution
	UI            UI           // nil = headless
	SystemPrompt  string       // empty = minimal default for testing
	MaxIterations int          // 0 = unlimited
	Debug         bool
	EventBus      *events.EventBus // nil = no events
}

// Agent is the main entry point for the conversation engine.
type Agent struct {
	provider      Provider
	executor      ToolExecutor
	ui            UI
	systemPrompt  string
	maxIterations int
	debug         bool
	eventBus      *events.EventBus

	state     *State
	outputMgr OutputManager

	paused bool
}

// NewAgent creates a new Agent from the given options.
func NewAgent(opts Options) *Agent {
	if opts.Provider == nil {
		panic("seed: Provider is required")
	}
	if opts.Executor == nil {
		panic("seed: ToolExecutor is required")
	}

	systemPrompt := opts.SystemPrompt
	if strings.TrimSpace(systemPrompt) == "" {
		systemPrompt = DefaultSystemPrompt
	}

	return &Agent{
		provider:      opts.Provider,
		executor:      opts.Executor,
		ui:            opts.UI,
		systemPrompt:  systemPrompt,
		maxIterations: opts.MaxIterations,
		debug:         opts.Debug,
		eventBus:      opts.EventBus,
		state:         NewState(),
		outputMgr:     NewOutputManager(opts.EventBus),
	}
}

// Run executes a single query through the conversation loop.
func (a *Agent) Run(ctx context.Context, query string) (string, error) {
	ch := newConversationHandler(a)
	return ch.ProcessQuery(ctx, query)
}

// State returns the agent's conversation state.
func (a *Agent) State() *State {
	return a.state
}

// ExportState serializes the current state to JSON.
func (a *Agent) ExportState() ([]byte, error) {
	return a.state.ExportState()
}

// ImportState deserializes state from JSON.
func (a *Agent) ImportState(data []byte) error {
	return a.state.ImportState(data)
}

// SetSystemPrompt updates the system prompt for future queries.
func (a *Agent) SetSystemPrompt(prompt string) {
	a.systemPrompt = prompt
}

// SetFlushCallback sets a callback to flush streaming output.
func (a *Agent) SetFlushCallback(fn func()) {
	a.outputMgr.SetFlushCallback(fn)
}

// StreamingBuffer returns the content streaming buffer.
func (a *Agent) StreamingBuffer() *StreamingBuffer {
	return a.outputMgr.ContentBuffer()
}

// ReasoningBuffer returns the reasoning streaming buffer.
func (a *Agent) ReasoningBuffer() *StreamingBuffer {
	return a.outputMgr.ReasoningBuffer()
}

// Pause pauses the agent for user clarification.
func (a *Agent) Pause() {
	a.paused = true
}

// Resume resumes a paused agent.
func (a *Agent) Resume() {
	a.paused = false
}

// IsPaused returns whether the agent is paused.
func (a *Agent) IsPaused() bool {
	return a.paused
}

// Provider returns the provider (for accessing Info, etc.).
func (a *Agent) Provider() Provider {
	return a.provider
}

// debugLog logs a debug message if debug mode is enabled.
func (a *Agent) debugLog(format string, args ...interface{}) {
	if a.debug {
		if a.ui != nil {
			a.ui.PrintLine(fmt.Sprintf(format, args...))
		}
	}
}
