# SP-011: Response Processing Hardening

**Status:** 📋 Spec
**Location:** `core/conversation.go` (modify), `core/tool_call_normalizer.go` (new)
**Size:** ~0 lines (not implemented)
**Test Files:** 0

## Current State

Seed's conversation loop processes LLM responses with a simple flow:
1. `resp.ToMessage()` extracts the first choice
2. Fallback parser runs if no structured `tool_calls` but content has patterns
3. If no tool calls → check truncation/tentative → finalize
4. If tool calls → execute → loop

This works for well-behaved providers but has functional gaps compared to sprout's battle-tested loop. Several gaps cause failures with gpt-oss harmony models and other non-standard providers.

### What's missing

| Gap | Impact |
|-----|--------|
| No `finish_reason` handling | `"stop"` with empty content accepted as complete; `"length"` (truncated) treated as normal |
| No `<|channel|>` suffix stripping (structured path) | gpt-oss tool names like `shell_command<|channel|>0` won't match executor |
| No malformed structured tool call recovery | Invalid JSON args in structured `tool_calls` → silent executor failure |
| No missing tool call ID generation | Models that omit `id` → orphaned tool results |
| No tool call deduplication | Duplicate calls execute multiple times |
| No argument normalization/repair | Broken JSON args passed through to executor |
| No blank/repetitive iteration detection | Stuck model loops until `MaxIterations` |
| No ANSI sanitization | Escape codes leak into conversation content |

## Architecture

### Phase 1: Tool Call Normalization (structured path)

A normalization step that runs on structured `tool_calls` **before** execution. This replaces the current bare passthrough of `assistantMsg.ToolCalls`.

```go
// tool_call_normalizer.go

type ToolCallNormalizer struct {
    debug bool
}

type NormalizedToolCalls struct {
    Calls       []ToolCall
    Malformed   []ToolCall
    Deduped     int // count removed
}

func (n *ToolCallNormalizer) Normalize(calls []ToolCall) NormalizedToolCalls
```

**Normalize does five things, in order:**

1. **Strip channel suffix** — `strings.Split(name, "<|channel|>")[0]`
2. **Generate missing IDs** — `fmt.Sprintf("call_%s_%d", name, time.Now().UnixNano())`
3. **Deduplicate** — by ID and by `name|arguments` key
4. **Repair arguments** — parse JSON, re-marshal to canonical form; collect unrepairable calls as malformed
5. **Normalize Type** — force `Type = "function"`

**Malformed handling:** If any structured tool calls have unrepairable JSON arguments, inject a transient message: *"Your previous tool call arguments were incomplete or invalid JSON. Re-emit with complete valid JSON."* Discard the malformed calls and continue the loop (model will re-emit).

### Phase 2: Finish Reason Handling

Replace the current "check tool calls → else finalize" with explicit finish reason dispatch. The key insight from sprout is that `finish_reason` carries signal, but sprout's implementation is overly complex with too many special cases. We'll use a cleaner approach.

```go
// In runLoop, after extracting assistantMsg:

finishReason := ""
if len(resp.Choices) > 0 {
    finishReason = resp.Choices[0].FinishReason
}

// Tool calls take priority regardless of finish reason
if len(assistantMsg.ToolCalls) > 0 {
    // ... execute tools, continue loop
}

// No tool calls — dispatch by finish reason
switch finishReason {
case "":
    // No finish reason — model expects to continue
    // Check truncation/tentative as before, then continue loop
case "stop":
    // Model says it's done — but validate:
    // - Empty content? → ask to continue (model stopped prematurely)
    // - Incomplete content? → ask to continue with final answer
    // - Tentative content after recent tool results? → reject with specific message
    //   (match sprout: "You just received tool results. Do not stop with a planning note.
    //    Either take the next concrete action or provide the actual final answer now.")
    //   Accept after 2 rejections to avoid infinite loops.
    // - Otherwise → accept and finalize
case "length":
    // Model hit max tokens — truncated, ask to continue
case "content_filter":
    // Content was filtered — retry once, then return error to consumer.
    // First occurrence: continue loop (model may generate different content).
    // Second occurrence: abort with error so consumer can log/handle the filter.
default:
    // Unknown — treat as continue
}
```

**Differences from sprout:**

| Sprout behavior | Seed approach |
|----------------|---------------|
| `tentativeRejectionCount` with separate tracking | Same — `tentativeRejectionCount` tracks rejections of tentative post-tool `"stop"` responses, accepts after 2 |
| `followsRecentToolResults()` scan | Same — scan backward from last message for tool role messages |
| `"stop"` with tentative → specific rejection message | Same — *"You just received tool results. Do not stop with a planning note."* |
| `"content_filter"` → continue silently forever | **Different** — retry once, then return error to consumer |
| `consecutiveBlankIterations` separate counter (threshold 2) | Same — use existing `consecutiveBlank` field (threshold 2) |
| `handleIncompleteResponse()` sends fixed message | Same pattern — transient message asking model to continue |

The key difference from sprout: `"content_filter"` retries once then surfaces to the consumer. Everything else matches sprout's behavior.

### Phase 3: Blank/Repetitive Iteration Detection

Detect when the model returns empty or repetitive content with no tool calls.

```go
func (ch *ConversationHandler) isBlankIteration(content string) bool {
    return strings.TrimSpace(content) == ""
}

func (ch *ConversationHandler) isRepetitiveContent(content string) bool {
    // Check if content matches the previous assistant message exactly
    msgs := ch.agent.state.Messages()
    for i := len(msgs) - 1; i >= 0; i-- {
        if msgs[i].Role == "assistant" {
            return strings.TrimSpace(content) == strings.TrimSpace(msgs[i].Content)
        }
        if msgs[i].Role == "user" {
            break // Don't look past the last user message
        }
    }
    return false
}
```

**Behavior:**
- 1st blank/repetitive → send reminder transient message, continue
- 2nd consecutive blank/repetitive → force-finalize with error message
- Uses `ch.consecutiveBlank` (already exists but unused)

### Phase 4: ANSI Sanitization

Strip ANSI escape codes from content before recording in state. Simple regex:

```go
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\([A-Za-z]`)

func sanitizeANSI(content string) string {
    return ansiRegex.ReplaceAllString(content, "")
}
```

Applied to `assistantMsg.Content` before state recording.

### Integration Points

```
resp.ToMessage()
  → sanitizeANSI(content)
  → fallback parse (if no structured tool_calls)
  → normalize tool calls (strip channel, generate IDs, dedupe, repair args)
  → if malformed structured → transient message, continue
  → if tool calls → execute, continue
  → finish reason dispatch:
    - "" → truncation/tentative checks → continue or finalize
    - "stop" → validate: empty → continue; incomplete → continue; tentative after tools → reject (max 2); else → finalize
    - "length" → ask to continue
    - "content_filter" → retry once, then error to consumer
    - default → continue
  → blank/repetitive detection (separate consecutiveBlank counter, threshold 2)
  → finalize
```

## Implementation Phases

### Phase 1: Tool Call Normalizer (Week 1)

- Create `ToolCallNormalizer` with channel stripping, ID generation, deduplication, argument repair, type normalization
- Wire into `runLoop` before tool execution
- Handle malformed structured calls → transient message

### Phase 2: Finish Reason Handling (Week 1)

- Implement finish reason dispatch in `runLoop`
- Handle `stop` (validate content), `length` (continue), `content_filter` (skip), empty (continue)
- Use existing continuation budget for all retry cases

### Phase 3: Blank/Repetitive Detection (Week 1)

- Implement `isBlankIteration` and `isRepetitiveContent`
- Wire into no-tool-call path before finalizing
- Use `consecutiveBlank` counter

### Phase 4: ANSI Sanitization (Week 1)

- Add `sanitizeANSI` to content processing

### Phase 5: Testing (Week 1)

- E2e test: gpt-oss channel suffix → tool name matches → executes
- E2e test: `finish_reason: "stop"` with empty content → continuation → complete response
- E2e test: `finish_reason: "length"` → continuation
- E2e test: malformed structured tool call → transient message → model re-emits
- E2e test: missing tool call ID → synthetic ID generated → tool result linked
- E2e test: duplicate tool calls → only unique calls execute
- E2e test: blank iteration → reminder → 2nd blank → error
- E2e test: repetitive content → reminder → 2nd → error
- E2e test: ANSI codes → stripped from content

## Success Criteria

| Metric | Target |
|--------|--------|
| Channel suffix stripping | `shell_command<|channel|>0` → `shell_command` |
| Missing ID generation | Tool calls without ID get synthetic ID |
| Deduplication | Duplicate calls (same ID or name+args) execute once |
| Argument repair | Broken JSON args repaired or flagged as malformed |
| Malformed recovery | Transient message → model re-emits valid calls |
| Finish reason `"stop"` | Empty → continue; incomplete → continue; tentative after tools → reject up to 2x, then accept |
| Finish reason `"length"` | Always continue |
| Finish reason `"content_filter"` | Retry once, then error to consumer |
| Tentative post-tool rejection | Match sprout: `followsRecentToolResults()` + `tentativeRejectionCount` (max 2) |
| Blank iteration | Separate `consecutiveBlank` counter; 1st → reminder; 2nd → error |
| Repetitive content | Same as blank iteration |
| ANSI sanitization | Escape codes removed from content |
| Continuation budget | `maxContinuations = 3` for truncation, tentative, empty stop, length |

## Key Files

| File | Action |
|------|--------|
| `core/tool_call_normalizer.go` | Create: ToolCallNormalizer |
| `core/conversation.go` | Modify: normalize calls, finish reason dispatch, blank detection, ANSI |
| `test/e2e_test.go` | Add: all e2e tests above |
