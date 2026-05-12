# SP-006: Fallback Parsing

**Status:** 📋 Spec
**Location:** `core/fallback_parser.go`
**Size:** ~0 lines (not implemented)
**Test Files:** 0

## Current State

Seed assumes providers always return well-formed `ChatResponse.Choices[0].Message.ToolCalls`. When a provider returns malformed output (tool calls embedded in content as JSON, XML, or code blocks), the conversation handler treats the response as a plain text final answer and stops the loop.

Sprout's `FallbackParser` (823 lines) handles this gracefully, extracting tool calls from content and continuing the tool-call loop. This is critical because many providers (especially open-source models) frequently return tool calls as content rather than structured `tool_calls`.

## Architecture

### What's Missing

A content-aware parser that extracts tool calls from LLM response content when the provider's structured `tool_calls` field is empty but the content clearly contains tool-call-like data.

### Extraction Strategies

The parser must support five extraction methods, tried in order:

| Strategy | Pattern | Example |
|----------|---------|---------|
| JSON code fences | ` ```json ... ``` ` containing tool_calls | OpenAI-style wrapped calls |
| Bare JSON segments | `{...}` or `[...]` outside fences | Anthropic-style tool use blocks |
| XML function blocks | `<function=name>...</function>` | Claude XML tool calls |
| Function-name patterns | `name: tool_name` + JSON args | Llama-style function calling |
| Named tool blocks | `tool_name { ... }` | Custom format |

### Interface Design

```go
type FallbackParser struct {
    debug          bool
    knownToolNames func(name string) bool
}

type FallbackParseResult struct {
    ToolCalls      []ToolCall
    CleanedContent string
}

func NewFallbackParser(opts FallbackParserOptions) *FallbackParser
func (fp *FallbackParser) Parse(content string) *FallbackParseResult
func (fp *FallbackParser) ShouldUseFallback(content string, hasStructuredToolCalls bool) bool
```

The `knownToolNames` callback is provided by the consumer (not a global registry) so seed stays portable. When nil, all extracted tool names are accepted.

### Processing Pipeline

1. Check `containsToolCallPatterns()` — quick scan for telltale patterns
2. `collectBlocks()` — run all five extraction strategies
3. `mergeBlocks()` — merge overlapping extraction regions
4. `dedupeToolCalls()` — deduplicate by name+arguments
5. `ensureDefaults()` — generate IDs, normalize types, ensure valid JSON args
6. `removeBlocksFromContent()` — clean the original content of extracted regions

### Integration into ConversationHandler

In the response processing path, after checking for structured tool calls:

```go
// After resp.ToMessage() — if no structured tool calls but content has patterns
if len(assistantMsg.ToolCalls) == 0 && fp.ShouldUseFallback(assistantMsg.Content) {
    result := fp.Parse(assistantMsg.Content)
    if result != nil && len(result.ToolCalls) > 0 {
        assistantMsg.ToolCalls = result.ToolCalls
        assistantMsg.Content = result.CleanedContent
        // Publish event about fallback usage
    }
}
```

### Normalization Rules

- `ID` field: generate `fallback_{sanitized_name}_{unix_nano}` if missing
- `Type` field: normalize to `"function"` (OpenAI convention)
- `Arguments`: normalize to valid JSON — handle double-encoded, string-wrapped, and raw object forms
- Deduplicate: same name + same arguments = drop duplicate

## Implementation Phases

### Phase 1: Core Parser (Week 1)

- Create `FallbackParser` struct with all five extraction strategies
- Implement JSON block extraction (code fences + bare segments)
- Implement argument normalization and tool call deduplication
- Add `Parse()` and `ShouldUseFallback()` methods

### Phase 2: XML & Named Blocks (Week 1-2)

- Add XML function block extraction
- Add function-name pattern extraction
- Add named tool block extraction (with `knownToolNames` check)

### Phase 3: Integration (Week 2)

- Wire into `ConversationHandler` response processing
- Add fallback detection event publishing
- Add e2e tests with malformed provider responses

## Success Criteria

| Metric | Target |
|--------|--------|
| Extraction strategies | 5 (JSON fences, bare JSON, XML, function-name, named blocks) |
| Tool call normalization | ID generation, type normalization, JSON argument validation |
| Deduplication | Same name + args = single call |
| Content cleaning | Extracted regions removed, whitespace normalized |
| Zero external dependencies | No imports beyond stdlib |
| E2e tests | Malformed response → tool calls extracted → loop continues |

## Key Files

| File | Action |
|------|--------|
| `core/fallback_parser.go` | Create: FallbackParser + all extraction strategies |
| `core/conversation.go` | Modify: wire fallback parsing into response path |
| `test/mock_provider.go` | Modify: add malformed response simulation |
| `test/e2e_test.go` | Add: fallback parsing e2e tests |
