package core

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var _ ToolExecutor = (*ToolRegistry)(nil)

// ParameterConfig defines a single parameter within a tool's schema.
type ParameterConfig struct {
	Name         string
	Type         string
	Required     bool
	Alternatives []string
	Description  string
}

// ToolHandler is the standard handler for a registered tool.
type ToolHandler func(ctx context.Context, args map[string]interface{}) (string, error)

// ToolHandlerWithImages is a handler that may also return image data.
type ToolHandlerWithImages func(ctx context.Context, args map[string]interface{}) ([]ImageData, string, error)

// ToolConfig configures a tool registered in the registry.
type ToolConfig struct {
	Name              string
	Description       string
	Parameters        []ParameterConfig
	Handler           ToolHandler
	HandlerWithImages ToolHandlerWithImages
	Aliases           []string
	Timeout           time.Duration
	MaxResultSize     int
	SafeForParallel   bool
}

// ToolRegistryOptions configures a ToolRegistry.
type ToolRegistryOptions struct {
	DefaultTimeout          time.Duration
	MaxResultSize           int
	EventPublisher          EventPublisher
	PreExecuteHook          func(name string, args map[string]interface{}) error
	PostExecuteHook         func(name string, result string) string
	CircuitBreakerThreshold int
	CircuitBreakerTimeout   time.Duration
}

// ToolRegistry manages a collection of registered tools and executes them
// according to requests received from the conversation engine.
type ToolRegistry struct {
	mu              sync.RWMutex
	tools           map[string]*toolEntry
	aliases         map[string]string
	defaultTimeout  time.Duration
	maxResultSize   int
	circuitBreakers map[string]*circuitBreaker
	cbThreshold     int
	cbResetTimeout  time.Duration
	eventPublisher  EventPublisher
	PreExecuteHook  func(name string, args map[string]interface{}) error
	PostExecuteHook func(name string, result string) string
}

type toolEntry struct {
	config ToolConfig
	tool   Tool
}

var channelSuffixRe = regexp.MustCompile(`<\|channel\|\>\d+$`)

// NewToolRegistry creates a new ToolRegistry with the given options.
func NewToolRegistry(opts ToolRegistryOptions) *ToolRegistry {
	defaultTimeout := opts.DefaultTimeout
	if defaultTimeout <= 0 {
		defaultTimeout = 5 * time.Minute
	}
	maxResultSize := opts.MaxResultSize
	if maxResultSize <= 0 {
		maxResultSize = 50 * 1024
	}
	ep := opts.EventPublisher
	if ep == nil || reflect.ValueOf(ep).IsNil() {
		ep = noopEventPublisher{}
	}
	cbThreshold := opts.CircuitBreakerThreshold
	if cbThreshold <= 0 {
		cbThreshold = 5
	}
	cbResetTimeout := opts.CircuitBreakerTimeout
	if cbResetTimeout <= 0 {
		cbResetTimeout = 30 * time.Second
	}
	return &ToolRegistry{
		tools:           make(map[string]*toolEntry),
		aliases:         make(map[string]string),
		defaultTimeout:  defaultTimeout,
		maxResultSize:   maxResultSize,
		circuitBreakers: make(map[string]*circuitBreaker),
		cbThreshold:     cbThreshold,
		cbResetTimeout:  cbResetTimeout,
		eventPublisher:  ep,
		PreExecuteHook:  opts.PreExecuteHook,
		PostExecuteHook: opts.PostExecuteHook,
	}
}

// Register adds a single tool to the registry.
func (r *ToolRegistry) Register(config ToolConfig) error {
	if config.Name == "" {
		return fmt.Errorf("tool name must not be empty")
	}
	if config.Handler == nil && config.HandlerWithImages == nil {
		return fmt.Errorf("tool %q must have a Handler or HandlerWithImages", config.Name)
	}
	entry := &toolEntry{
		config: config,
		tool: Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        config.Name,
				Description: config.Description,
				Parameters:  buildSchema(config.Parameters),
			},
		},
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[config.Name]; exists {
		return fmt.Errorf("tool %q is already registered", config.Name)
	}
	r.tools[config.Name] = entry
	for _, alias := range config.Aliases {
		r.aliases[strings.ToLower(alias)] = config.Name
	}
	r.circuitBreakers[config.Name] = newCircuitBreaker(r.cbThreshold, r.cbResetTimeout)
	return nil
}

// RegisterAll registers multiple tools. Returns the first error encountered.
func (r *ToolRegistry) RegisterAll(configs []ToolConfig) error {
	for _, c := range configs {
		if err := r.Register(c); err != nil {
			return err
		}
	}
	return nil
}

// Unregister removes a tool and its aliases by canonical name.
func (r *ToolRegistry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	canonical := r.resolveNameLocked(name)
	entry, exists := r.tools[canonical]
	if !exists {
		return false
	}
	delete(r.tools, canonical)
	delete(r.circuitBreakers, canonical)
	for alias, can := range r.aliases {
		if can == entry.config.Name {
			delete(r.aliases, alias)
		}
	}
	return true
}

// GetTools returns all registered tool definitions, sorted by name.
func (r *ToolRegistry) GetTools() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tools := make([]Tool, 0, len(r.tools))
	for _, entry := range r.tools {
		tools = append(tools, entry.tool)
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Function.Name < tools[j].Function.Name
	})
	return tools
}

// GetTool returns the Tool definition for the given name (resolving aliases).
func (r *ToolRegistry) GetTool(name string) *Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	canonical := r.resolveNameLocked(name)
	entry, exists := r.tools[canonical]
	if !exists {
		return nil
	}
	cp := entry.tool
	return &cp
}

// HasTool reports whether a tool exists by canonical name or alias.
func (r *ToolRegistry) HasTool(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.tools[r.resolveNameLocked(name)]
	return exists
}

// ToolNames returns all registered tool canonical names.
func (r *ToolRegistry) ToolNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// Execute runs the given tool calls and returns the resulting messages.
func (r *ToolRegistry) Execute(ctx context.Context, calls []ToolCall) []Message {
	if len(calls) == 0 {
		return nil
	}
	var sequential, parallel []toolCallSlot
	for i, call := range calls {
		name := r.resolveName(stripChannelSuffix(call.Function.Name))
		r.mu.RLock()
		entry, exists := r.tools[name]
		r.mu.RUnlock()
		if !exists || !entry.config.SafeForParallel {
			sequential = append(sequential, toolCallSlot{idx: i, call: call})
			continue
		}
		parallel = append(parallel, toolCallSlot{idx: i, call: call})
	}
	results := make([]Message, len(calls))
	// Check context before spawning parallel goroutines.
	if len(parallel) > 0 && ctx.Err() != nil {
		sequential = append(sequential, parallel...)
		parallel = nil
	}
	sem := make(chan struct{}, 4)
	if len(parallel) > 0 {
		var wg sync.WaitGroup
		var mu sync.Mutex
		wg.Add(len(parallel))
		for _, slot := range parallel {
			sem <- struct{}{}
			go func(s toolCallSlot) {
				defer func() { <-sem }()
				defer wg.Done()
				msg := r.executeSingle(ctx, s.call, s.idx)
				mu.Lock()
				results[s.idx] = msg
				mu.Unlock()
			}(slot)
		}
		wg.Wait()
	}
	for _, slot := range sequential {
		results[slot.idx] = r.executeSingle(ctx, slot.call, slot.idx)
	}
	return results
}

// executeSingle runs a single tool call through the full execution pipeline.
func (r *ToolRegistry) executeSingle(ctx context.Context, call ToolCall, callIdx int) Message {
	start := time.Now()
	name := r.resolveName(stripChannelSuffix(call.Function.Name))
	args, parseErr := parseAndValidateArgs(r, name, call.Function.Name, call.Function.Arguments)
	r.eventPublisher.Publish(EventTypeToolStart, map[string]any{
		"tool_name":    name,
		"tool_call_id": call.ID,
		"arguments":    call.Function.Arguments,
		"tool_index":   callIdx,
	})
	if parseErr != nil {
		r.recordEnd(name, call.ID, "error", fmt.Sprintf("Failed to parse arguments: %s", parseErr), start)
		return ToolResultMessage(call.ID, name, fmt.Sprintf("Failed to parse arguments: %s", parseErr))
	}
	cb := r.getCircuitBreaker(name)
	if !cb.Allow() {
		result := "Circuit breaker is open — temporarily rejecting requests"
		r.recordEnd(name, call.ID, "error", result, start)
		return ToolResultMessage(call.ID, name, result)
	}
	if r.PreExecuteHook != nil {
		if err := r.PreExecuteHook(name, args); err != nil {
			result := fmt.Sprintf("Pre-execute hook rejected: %s", err)
			r.recordEnd(name, call.ID, "error", result, start)
			return ToolResultMessage(call.ID, name, result)
		}
	}
	result, handlerErr := r.runWithTimeout(ctx, name, args)
	if handlerErr != nil {
		result = handlerErr.Error()
	}
	result = truncateResult(result, r.toolMaxResultSize(name))
	if r.PostExecuteHook != nil {
		result = r.PostExecuteHook(name, result)
	}
	if handlerErr != nil {
		cb.RecordFailure()
		r.recordEnd(name, call.ID, "error", result, start)
		return ToolResultMessage(call.ID, name, result)
	}
	cb.RecordSuccess()
	r.recordEnd(name, call.ID, "success", result, start)
	return ToolResultMessage(call.ID, name, result)
}

// runWithTimeout executes the tool handler with a per-tool timeout.
func (r *ToolRegistry) runWithTimeout(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	r.mu.RLock()
	entry, exists := r.tools[name]
	r.mu.RUnlock()
	if !exists {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	timeout := r.defaultTimeout
	if entry.config.Timeout > 0 {
		timeout = entry.config.Timeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ch := make(chan stringResult, 1)
	go func() {
		if entry.config.HandlerWithImages != nil {
			_, text, err := entry.config.HandlerWithImages(execCtx, args)
			ch <- stringResult{text: text, err: err}
		} else {
			text, err := entry.config.Handler(execCtx, args)
			ch <- stringResult{text: text, err: err}
		}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			return "", fmt.Errorf("tool execution failed: %w", res.err)
		}
		return res.text, nil
	case <-execCtx.Done():
		return "", fmt.Errorf("tool execution timed out after %v", timeout)
	}
}

// recordEnd publishes a tool_end event with timing info.
func (r *ToolRegistry) recordEnd(name, callID, status, result string, start time.Time) {
	duration := time.Since(start).Milliseconds()
	r.eventPublisher.Publish(EventTypeToolEnd, map[string]any{
		"tool_call_id": callID,
		"tool_name":    name,
		"status":       status,
		"result":       result,
		"duration_ms":  duration,
	})
}

// getCircuitBreaker returns the circuit breaker for a given tool.
func (r *ToolRegistry) getCircuitBreaker(name string) *circuitBreaker {
	r.mu.RLock()
	cb, ok := r.circuitBreakers[name]
	r.mu.RUnlock()
	if ok {
		return cb
	}
	return newCircuitBreaker(r.cbThreshold, r.cbResetTimeout)
}

// resolveName looks up a tool name, resolving aliases (case-insensitive).
func (r *ToolRegistry) resolveName(name string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.resolveNameLocked(name)
}

// resolveNameLocked looks up a tool name, resolving aliases.
// Caller MUST hold r.mu (RLock or Lock).
func (r *ToolRegistry) resolveNameLocked(name string) string {
	if _, exists := r.tools[name]; exists {
		return name
	}
	if resolved, ok := r.aliases[strings.ToLower(name)]; ok {
		return resolved
	}
	lower := strings.ToLower(name)
	for cn := range r.tools {
		if strings.ToLower(cn) == lower {
			return cn
		}
	}
	return name
}

// stripChannelSuffix removes the <|channel|>N suffix from tool names.
func stripChannelSuffix(name string) string {
	return channelSuffixRe.ReplaceAllString(name, "")
}
