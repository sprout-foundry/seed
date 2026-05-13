// Package core provides a conversation engine for LLM-powered agents.
//
// Core concepts:
//
//   - Agent: the main entry point. Create with NewAgent(), run queries with Run().
//   - Provider: interface for LLM backends. Implement Chat() and ChatStream().
//   - ToolExecutor: interface for tool execution. Implement GetTools() and Execute().
//   - UI: optional interface for interactive output. Use NoopUI for headless mode.
//   - EventPublisher: optional interface for event-driven output.
//
// Quick start:
//
//	agent, err := core.NewAgent(core.Options{
//	    Provider: &myProvider{},
//	    Executor: core.NoopExecutor,
//	})
//	result, err := agent.Run(ctx, "Hello")
//
// The core package has zero external dependencies. The events and internal/test
// packages are optional.
package core

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
)

// DefaultSystemPrompt is the minimal system prompt used when none is provided.
const DefaultSystemPrompt = "You are a helpful assistant that can execute tools to complete tasks."

// RetryConfig configures retry behavior for transient provider errors.
// Zero values use sensible defaults.
type RetryConfig struct {
	// MaxAttempts is the total number of attempts (initial + retries).
	// Zero means use the default of 3. Setting to 1 means no retries
	// (only the initial attempt).
	MaxAttempts int

	// InitialDelay is the delay before the first retry.
	// Zero means use the default of 100ms.
	InitialDelay time.Duration

	// MaxDelay caps the exponential backoff growth.
	// Zero means use the default of 5s.
	MaxDelay time.Duration

	// Multiplier is the exponential growth factor.
	// Zero means use the default of 2.0.
	Multiplier float64

	// Jitter adds randomness to delays. 0 = none, (0,1) = partial, >=1 = full.
	// Zero means use the default of 0.0 (no jitter).
	Jitter float64
}

// MaxAttemptsOrDefault returns MaxAttempts or the default (3).
func (rc RetryConfig) MaxAttemptsOrDefault() int {
	if rc.MaxAttempts > 0 {
		return rc.MaxAttempts
	}
	return 3
}

// InitialDelayOrDefault returns InitialDelay or the default (100ms).
func (rc RetryConfig) InitialDelayOrDefault() time.Duration {
	if rc.InitialDelay > 0 {
		return rc.InitialDelay
	}
	return 100 * time.Millisecond
}

// MaxDelayOrDefault returns MaxDelay or the default (5s).
func (rc RetryConfig) MaxDelayOrDefault() time.Duration {
	if rc.MaxDelay > 0 {
		return rc.MaxDelay
	}
	return 5 * time.Second
}

// MultiplierOrDefault returns Multiplier or the default (2.0).
func (rc RetryConfig) MultiplierOrDefault() float64 {
	if rc.Multiplier > 0 {
		return rc.Multiplier
	}
	return 2.0
}

// JitterOrDefault returns Jitter or the default (0.0).
// Unlike other *OrDefault methods, zero is a meaningful value here:
// Jitter 0.0 means "no jitter" (deterministic retries), so it is not
// replaced by a default. Negative values are not expected and are
// returned as-is.
func (rc RetryConfig) JitterOrDefault() float64 {
	return rc.Jitter
}

// Options configures an Agent.
type Options struct {
	Provider       Provider     // required — LLM communication
	Executor       ToolExecutor // required — tool execution
	UI             UI           // nil = headless
	SystemPrompt   string       // empty = minimal default for testing
	MaxIterations  int          // 0 = unlimited
	MaxTokens      int          // 0 = use provider default
	Debug          bool
	EventPublisher EventPublisher // nil = no events
	// OnIteration is an optional callback invoked synchronously at the start of
	// each conversation-loop iteration. It receives the iteration number (0-based),
	// the current message count in state, the estimated token count for the prompt,
	// and the model's context window size. The token estimate is computed from the
	// prepared message list (before any compaction). The agent does not await a
	// result or handle errors from this callback; if the callback panics, the
	// panic is caught and logged (the agent continues).
	OnIteration func(iteration int, messages int, tokenEstimate int, contextSize int)
	// Optimizer is used to optimize conversation history across iterations.
	Optimizer   *ConversationOptimizer
	RetryConfig RetryConfig // retry behavior for transient errors; zero values use defaults
	// InitialMessages seeds the agent's conversation state with pre-existing
	// messages (e.g., from a previous query in the same session). When empty,
	// the agent starts with a blank slate.
	InitialMessages []Message
	// DisableFallbackParser disables the fallback tool-call parser. When
	// disabled, malformed tool calls in model responses will not be recovered.
	// Default (false): fallback parser is enabled when tools are configured.
	DisableFallbackParser bool
	// DisableValidator disables the response validator (truncation/tentative
	// detection). When disabled, incomplete responses will not trigger
	// automatic continuation. Default (false): validator is enabled.
	DisableValidator bool
	// DisableNormalizer disables the tool call normalizer. When disabled,
	// structured tool calls will not be cleaned before execution. Default
	// (false): normalizer is enabled.
	DisableNormalizer bool
	// OnCheckpoint is an optional fire-and-forget callback invoked after each
	// completed turn. It receives the TurnCheckpoint summarizing what happened
	// in the turn. The callback is invoked asynchronously after the checkpoint
	// is built and stored in state. If the callback panics, the panic is caught
	// and the agent continues normally.
	OnCheckpoint func(TurnCheckpoint)
}

// Agent is the main entry point for the conversation engine.
type Agent struct {
	provider       Provider
	executor       ToolExecutor
	ui             UI
	systemPrompt   string
	maxIterations  int
	maxTokens      int
	debug          bool
	eventPublisher EventPublisher

	state     *State
	outputMgr OutputManager

	paused             bool
	inputInjectionChan chan string

	fallbackParser *FallbackParser
	normalizer     *ToolCallNormalizer
	validator      *ResponseValidator
	optimizer      *ConversationOptimizer
	onIteration    func(iteration int, messages int, tokenEstimate int, contextSize int)
	onCheckpoint   func(TurnCheckpoint)
	retryConfig    RetryConfig

	// steerMu / steerMsgs hold externally-queued steering messages.
	// They are drained into the ConversationHandler when Run is called,
	// so the steer messages appear in the next API call and are consumed once.
	steerMu   sync.Mutex
	steerMsgs []Message
}

// NewAgent creates a new Agent from the given options. Returns an error if
// required options (Provider or ToolExecutor) are not provided.
func NewAgent(opts Options) (*Agent, error) {
	if opts.Provider == nil {
		return nil, ErrNoProvider
	}
	if opts.Executor == nil {
		return nil, ErrNoExecutor
	}

	systemPrompt := opts.SystemPrompt
	if strings.TrimSpace(systemPrompt) == "" {
		systemPrompt = DefaultSystemPrompt
	}

	// Build a set of known tool names for the fallback parser.
	knownTools := make(map[string]bool)
	for _, t := range opts.Executor.GetTools() {
		knownTools[t.Function.Name] = true
	}

	var fallbackParser *FallbackParser
	if !opts.DisableFallbackParser {
		fallbackParser = NewFallbackParser(FallbackParserOptions{KnownToolNames: func(name string) bool { return knownTools[name] }})
	}

	var validator *ResponseValidator
	if !opts.DisableValidator {
		validator = NewResponseValidator(ResponseValidatorOptions{DebugLog: func(format string, args ...interface{}) {
			if opts.Debug {
				fmt.Printf(format, args...)
			}
		}})
	}

	var normalizer *ToolCallNormalizer
	if !opts.DisableNormalizer {
		normalizer = NewToolCallNormalizer()
	}

	// Nil-guard: if no EventPublisher is provided, use a no-op so call sites
	// never need to nil-check. Uses reflection to catch the Go nil-interface
	// gotcha: a nil *T stored in an interface value is not == nil.
	ep := opts.EventPublisher
	if ep == nil || reflect.ValueOf(ep).IsNil() {
		ep = noopEventPublisher{}
	}

	// Create state, seeding with initial messages if provided.
	st := NewState()
	if len(opts.InitialMessages) > 0 {
		st.SetMessages(opts.InitialMessages)
	}

	return &Agent{
		provider:           opts.Provider,
		executor:           opts.Executor,
		ui:                 opts.UI,
		systemPrompt:       systemPrompt,
		maxIterations:      opts.MaxIterations,
		maxTokens:          opts.MaxTokens,
		debug:              opts.Debug,
		eventPublisher:     ep,
		state:              st,
		outputMgr:          NewOutputManager(ep),
		inputInjectionChan: make(chan string, 1),
		fallbackParser:     fallbackParser,
		normalizer:         normalizer,
		validator:          validator,
		optimizer:          opts.Optimizer,
		onIteration:        opts.OnIteration,
		onCheckpoint:       opts.OnCheckpoint,
		retryConfig:        opts.RetryConfig,
	}, nil
}

// Run executes a single query through the conversation loop.
func (a *Agent) Run(ctx context.Context, query string) (string, error) {
	ch := newConversationHandler(a)
	return ch.ProcessQuery(ctx, query)
}

// RunStream executes a single query through the streaming conversation loop.
// It uses provider.ChatStream() instead of provider.Chat(), so content is
// delivered incrementally via the StreamHandler callbacks. The return value
// is the final response content extracted from conversation state.
func (a *Agent) RunStream(ctx context.Context, query string) (string, error) {
	ch := newConversationHandler(a)
	return ch.ProcessQueryStream(ctx, query)
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

// SetProvider swaps the provider at runtime. The new provider will be used
// for all subsequent calls. It is safe to call between queries; calling
// during an active query may cause undefined behavior.
// Panics if p is nil — use NewAgent to create an agent without a provider
// and then call SetProvider with a non-nil provider.
func (a *Agent) SetProvider(p Provider) {
	if p == nil {
		panic("core: SetProvider called with nil provider")
	}
	a.provider = p
}

// InjectInput injects a user message into the conversation via a buffered
// channel. Returns true if the input was accepted (queued for the next loop
// iteration), false if a prior injection is still pending. The injection is
// fire-and-forget with backpressure: a true return means the input is queued,
// but the caller has no guarantee it was consumed yet.
func (a *Agent) InjectInput(input string) bool {
	select {
	case a.inputInjectionChan <- input:
		return true
	default:
		return false
	}
}

// Steer queues a transient message that will be appended to the next API call
// made by this agent. The message is consumed once and is not persisted in the
// conversation state. Use Steer to inject temporary guidance (e.g., "Focus on
// security concerns" or "Respond in JSON format") that should influence only
// the next model response.
//
// Steer must be called between Run() calls. If called during an active Run(),
// the message is queued and will not be consumed until the next Run().
//
// Example:
//
//	agent.Steer(core.Message{Role: "user", Content: "Focus on performance."})
func (a *Agent) Steer(msg Message) {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	a.steerMsgs = append(a.steerMsgs, msg)
}

// SteerSystem is a convenience for injecting a system-level steering message.
// It creates a Message with Role "system" and queues it via Steer, so the
// guidance takes effect on the next API call and is consumed once (like all
// steer messages). The message is not persisted in the conversation state.
//
// Example:
//
//	agent.SteerSystem("Focus on performance, not correctness.")
func (a *Agent) SteerSystem(content string) {
	a.Steer(Message{Role: "system", Content: content})
}

// drainSteerMessages atomically takes all queued steering messages and clears
// the queue. Returns nil (not empty slice) if no messages were queued, so
// callers can distinguish "no steer" from "empty steer".
func (a *Agent) drainSteerMessages() []Message {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	if len(a.steerMsgs) == 0 {
		return nil
	}
	msgs := a.steerMsgs
	a.steerMsgs = nil
	return msgs
}

// debugLog logs a debug message if debug mode is enabled.
func (a *Agent) debugLog(format string, args ...interface{}) {
	if a.debug {
		if a.ui != nil {
			a.ui.PrintLine(fmt.Sprintf(format, args...))
		}
	}
}
