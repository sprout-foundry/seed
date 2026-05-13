# SP-012: Tool Registry

**Status:** 📋 Spec
**Location:** `core/tool_registry.go`
**Size:** ~0 lines (not implemented)

## Current State

Seed's `ToolExecutor` interface is minimal — `GetTools() []Tool` and `Execute(ctx, calls) []Message`. The consumer (sprout) must implement the entire execution pipeline: name dispatch, argument parsing/validation, circuit breaking, timeout management, security classification, parallel execution, and error formatting.

This means every consumer reimplements the same boilerplate. The tool registry brings all of that into seed.

## Architecture

### What's Missing

A registry that owns:
1. Tool registration (name, description, parameter schema, handler function)
2. Name → handler dispatch
3. Argument validation and coercion
4. Per-tool timeout configuration
5. Circuit breaker state
6. Sequential and parallel execution modes
7. Error formatting and result truncation

### Tool Registration

```go
// ToolHandler executes a single tool and returns its text result.
type ToolHandler func(ctx context.Context, args map[string]interface{}) (string, error)

// ToolHandlerWithImages is like ToolHandler but can also return images.
type ToolHandlerWithImages func(ctx context.Context, args map[string]interface{}) ([]ImageData, string, error)

// ToolConfig holds everything needed to register and execute a tool.
type ToolConfig struct {
    Name          string
    Description   string
    Parameters    []ParameterConfig
    Handler       ToolHandler           // Set one or the other
    HandlerImages ToolHandlerWithImages // Takes precedence if set
    Aliases       []string             // Alternative names (e.g., ["grep"] for "search")
    Timeout       time.Duration        // 0 = use registry default
    MaxResultSize int                  // 0 = use registry default (truncate beyond this)
    SafeForParallel bool               // True if this tool can run concurrently with others
}

// ParameterConfig defines a single tool parameter.
type ParameterConfig struct {
    Name         string
    Type         string   // "string", "int", "float64", "bool"
    Required     bool
    Alternatives []string // Aliases for the parameter name
    Description  string
}
```

### ToolRegistry

```go
type ToolRegistry struct {
    tools     map[string]*registeredTool // name → tool (includes aliases)
    handlers  map[string]*registeredTool // canonical name → tool

    // Configuration
    defaultTimeout    time.Duration     // default: 5 minutes
    maxResultSize     int               // default: 50000 chars
    circuitBreaker    *CircuitBreaker   // nil = no circuit breaking
    eventPublisher    EventPublisher    // nil = no events

    mu sync.RWMutex
}

type registeredTool struct {
    config     ToolConfig
    toolDef    Tool // the Tool definition sent to the LLM
}
```

### Interface

```go
func NewToolRegistry(opts ToolRegistryOptions) *ToolRegistry

// Registration
func (r *ToolRegistry) Register(config ToolConfig) error
func (r *ToolRegistry) RegisterAll(configs []ToolConfig) error
func (r *ToolRegistry) Unregister(name string)

// LLM-facing (satisfies ToolExecutor interface)
func (r *ToolRegistry) GetTools() []Tool
func (r *ToolRegistry) Execute(ctx context.Context, calls []ToolCall) []Message

// Lookup
func (r *ToolRegistry) GetTool(name string) (ToolConfig, bool)
func (r *ToolRegistry) HasTool(name string) bool
func (r *ToolRegistry) ToolNames() []string
```

### Execution Pipeline

When `Execute()` is called with a batch of tool calls:

```
For each tool call:
  1. Resolve name (check aliases, strip <|channel|> suffix)
  2. Validate: tool exists?
  3. Parse arguments (JSON repair if needed)
  4. Validate required parameters present
  5. Check circuit breaker (if enabled)
  6. Create timeout context (tool-specific or default)
  7. Call handler in goroutine
  8. Wait for result, timeout, or context cancellation
  9. Format result (truncate if over max size)
  10. Update circuit breaker
  11. Publish tool_start/tool_end events (if publisher set)
```

**Parallel optimization:** If all tool calls in a batch are marked `SafeForParallel` and there are ≥2 calls, execute them concurrently using a goroutine pool. Otherwise, execute sequentially.

### Argument Validation

```go
func (r *ToolRegistry) validateArgs(tool *registeredTool, rawArgs string) (map[string]interface{}, error)
```

Steps:
1. JSON parse the arguments string (with repair for malformed JSON)
2. For each `ParameterConfig`, check:
   - If required and missing: return descriptive error
   - If `Alternatives` specified, check alternative names and normalize to canonical name
   - Coerce type if needed (e.g., `"5"` → `5` for int params)
3. Return validated map

### Circuit Breaker Integration

The registry owns the circuit breaker state. When a circuit breaker is provided via `ToolRegistryOptions`:
- Before execution: check if this action should be blocked
- After execution (success or failure): record the action
- On block: return rejection message to the LLM

### Event Publishing

When an `EventPublisher` is provided:
- Before execution: publish `EventTypeToolStart`
- After execution: publish `EventTypeToolEnd` with status, duration, result

### ToolExecutor Compatibility

`ToolRegistry` satisfies the `ToolExecutor` interface directly. Consumers can either:
- Use `ToolRegistry` as their `ToolExecutor` (full pipeline)
- Implement `ToolExecutor` themselves (for custom dispatch logic)

### Consumer Hook Points

The registry provides optional hooks for consumer-specific concerns:

```go
type ToolRegistryOptions struct {
    // Circuit breaker (nil = no circuit breaking)
    CircuitBreaker *CircuitBreaker

    // Event publisher (nil = no events)
    EventPublisher EventPublisher

    // Execution defaults
    DefaultTimeout time.Duration // default: 5 * time.Minute
    MaxResultSize  int           // default: 50000

    // PreExecuteHook runs before each tool execution.
    // Return an error to block execution (the error message becomes the tool result).
    // Use for security classification, approval prompts, logging, etc.
    PreExecuteHook func(ctx context.Context, toolName string, args map[string]interface{}) error

    // PostExecuteHook runs after each tool execution.
    // Receives the result and can modify it (e.g., redaction, sanitization).
    PostExecuteHook func(ctx context.Context, toolName string, args map[string]interface{}, result string) string
}
```

**Security is a hook, not built-in.** The registry doesn't know about security classification. The consumer registers a `PreExecuteHook` that classifies the tool call and returns an error to block dangerous operations. This keeps seed security-agnostic.

### How Sprout Would Use It

```go
registry := NewToolRegistry(ToolRegistryOptions{
    CircuitBreaker: cb,
    EventPublisher: eventBus,
    PreExecuteHook: func(ctx context.Context, name string, args map[string]interface{}) error {
        result := security.ClassifyToolCall(name, args)
        if result.ShouldBlock {
            return fmt.Errorf("security caution: %s", result.Reasoning)
        }
        return nil
    },
    PostExecuteHook: func(ctx context.Context, name string, args map[string]interface{}, result string) string {
        return redactor.Redact(result)
    },
})

// Register tools
registry.RegisterAll([]ToolConfig{
    {Name: "shell_command", Description: "...", Handler: shellHandler, Timeout: 10 * time.Minute},
    {Name: "read_file", Description: "...", Handler: readFileHandler},
    {Name: "write_file", Description: "...", Handler: writeFileHandler},
    // ...
})

// Use as ToolExecutor
agent, _ := NewAgent(Options{
    Provider:    provider,
    Executor:    registry, // ToolRegistry satisfies ToolExecutor
    EventBus:    eventBus,
})
```

## Implementation Phases

### Phase 1: Core Registry (Week 1)

- Create `ToolRegistry` with registration and lookup
- Implement `GetTools()` returning `[]Tool` definitions
- Implement argument parsing, validation, and coercion
- Implement basic sequential execution

### Phase 2: Pipeline (Week 1)

- Add timeout management per tool
- Add circuit breaker integration
- Add event publishing (tool_start, tool_end)
- Add result truncation
- Add JSON repair for malformed arguments

### Phase 3: Parallel Execution (Week 1-2)

- Detect parallel-safe tool batches
- Execute concurrent batches with goroutine pool
- Maintain ordering of results

### Phase 4: Hooks (Week 2)

- Add `PreExecuteHook` and `PostExecuteHook`
- Wire hooks into execution pipeline
- Add registry-level options (timeout, max size)

### Phase 5: Integration (Week 2)

- Wire `ToolRegistry` into sprout's seed adapter
- Migrate sprout's 35 tool registrations to seed's registry
- Remove sprout's `ToolExecutor` dispatch code

## Success Criteria

| Metric | Target |
|--------|--------|
| Registration | Name, description, params, handler, aliases, timeout |
| Dispatch | Name → handler with argument validation |
| Circuit breaker | Integrated, per-action tracking |
| Parallel execution | Concurrent batches for safe tools |
| Hooks | Pre/post execution for security, redaction |
| Event publishing | tool_start, tool_end with timing |
| Result truncation | Configurable max size |
| ToolExecutor compat | Registry satisfies interface directly |
| Zero coupling | Security is a hook, not built-in |

## Key Files

| File | Action |
|------|--------|
| `core/tool_registry.go` | Create: ToolRegistry, ToolConfig, ParameterConfig, hooks |
| `core/tool_call_normalizer.go` | Merge: channel suffix stripping into registry name resolution |
| `core/interfaces.go` | Modify: ToolRegistry satisfies ToolExecutor |
| `core/circuit_breaker.go` | Reference: integrated by registry |
| `test/tool_registry_test.go` | Create: registration, dispatch, validation, hooks |
