# SP-007: Response Validation

**Status:** 📋 Spec
**Location:** `core/response_validator.go`
**Size:** ~0 lines (not implemented)
**Test Files:** 0

## Current State

Seed treats every non-tool-call response as a final answer. If the LLM response is truncated (hit token limit), incomplete (ends mid-sentence), or tentative ("Let me check that..."), the conversation terminates prematurely. The user sees an unfinished answer instead of a complete one.

Sprout's `ResponseValidator` (286 lines) detects these cases and triggers continuation so the model can complete its thought.

## Architecture

### What's Missing

A response quality checker that runs after every `provider.Chat()` response and determines whether the response is complete enough to finalize, or whether the loop should continue to let the model finish.

### Validation Checks

| Check | Detection | Action |
|-------|-----------|--------|
| Incomplete patterns | Ends with `...` | Continue |
| Abrupt ending | No final punctuation (not URL/path/code), ends with `,` or `-` | Continue |
| Unusually short | < 10 words, not a known short complete answer | Continue |
| Unclosed code block | Odd number of triple-backtick markers | Continue |
| Tentative post-tool | Starts with "Let me...", "I'll...", "I need to..." and ≤ 40 words, no tool calls | Continue loop |

### Known Complete Short Answers

Short answers that should NOT trigger continuation:
- `"done"`, `"completed"`, `"finished"`, `"yes"`, `"no"`, `"error:"`, `"success"`, `"failed"`

### Interface Design

```go
type ResponseValidator struct {
    debugLog func(string, ...interface{})
}

func NewResponseValidator(opts ResponseValidatorOptions) *ResponseValidator

// IsIncomplete checks if a response appears to be incomplete/truncated.
func (rv *ResponseValidator) IsIncomplete(content string) bool

// LooksLikeTentativePostToolResponse detects planning stubs after tool execution.
func (rv *ResponseValidator) LooksLikeTentativePostToolResponse(content string) bool
```

The `debugLog` callback is optional — when nil, no debug output is produced. No dependency on Agent or any concrete type.

### Integration into ConversationHandler

After `provider.Chat()` returns and the response is extracted:

```go
// Check for incomplete response
if rv.IsIncomplete(content) {
    ch.enqueueTransientMessage(Message{
        Role:    "user",
        Content: "Please continue your response from where you left off.",
    })
    // Don't finalize — loop continues
    continue
}

// Check for tentative post-tool response
if len(assistantMsg.ToolCalls) == 0 && rv.LooksLikeTentativePostToolResponse(content) {
    // Model is planning but didn't call a tool — push it to continue
    continue
}
```

The continuation message uses `enqueueTransientMessage()` so it's a one-shot injection that doesn't pollute persistent state.

### Continuation Budget

To prevent infinite continuation loops, track consecutive continuation attempts. After 3 consecutive continuations without progress (no new tool calls, no token gain), force-finalize the response as-is.

## Implementation Phases

### Phase 1: Core Validator (Week 1)

- Create `ResponseValidator` with `IsIncomplete()` and `LooksLikeTentativePostToolResponse()`
- Implement all five validation checks
- Add debug logging support

### Phase 2: Integration (Week 1)

- Wire into `ConversationHandler` response processing
- Add continuation budget (max 3 consecutive continuations)
- Add transient message for continuation prompt

### Phase 3: Testing (Week 1)

- Unit tests for each validation check
- E2e test: provider returns truncated response → continuation → complete response
- E2e test: tentative post-tool response → loop continues → tool call

## Success Criteria

| Metric | Target |
|--------|--------|
| Incomplete detection | Truncated, abrupt, short, unclosed code blocks |
| Tentative detection | Planning prefixes under 40 words |
| Continuation budget | Max 3 consecutive, then force-finalize |
| Zero external dependencies | stdlib only |
| E2e coverage | Truncated response continuation test |

## Key Files

| File | Action |
|------|--------|
| `core/response_validator.go` | Create: ResponseValidator with all checks |
| `core/conversation.go` | Modify: add validation after response extraction |
| `test/e2e_test.go` | Add: incomplete response continuation tests |
