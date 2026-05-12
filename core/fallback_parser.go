package core

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// FallbackParserOptions configures the parser.
type FallbackParserOptions struct {
	// KnownToolNames returns true if the given name is a registered tool.
	// When nil, all extracted tool names are accepted.
	KnownToolNames func(string) bool
	// Debug enables verbose logging (printed to stderr).
	Debug bool
}

// FallbackParser extracts tool calls from malformed LLM response content.
type FallbackParser struct {
	debug          bool
	knownToolNames func(string) bool
}

// FallbackParseResult contains extracted tool calls and cleaned content.
type FallbackParseResult struct {
	ToolCalls      []ToolCall
	CleanedContent string
}

// NewFallbackParser creates a new FallbackParser with the given options.
func NewFallbackParser(opts FallbackParserOptions) *FallbackParser {
	return &FallbackParser{
		debug:          opts.Debug,
		knownToolNames: opts.KnownToolNames,
	}
}

// ShouldUseFallback returns true when structured tool_calls are missing
// and the content contains patterns suggestive of tool calls.
func (fp *FallbackParser) ShouldUseFallback(content string, hasStructuredToolCalls bool) bool {
	if hasStructuredToolCalls {
		return false
	}
	return fp.containsToolCallPatterns(content)
}

// Parse extracts tool calls from malformed LLM response content.
func (fp *FallbackParser) Parse(content string) *FallbackParseResult {
	if !fp.containsToolCallPatterns(content) {
		return &FallbackParseResult{
			ToolCalls:      nil,
			CleanedContent: content,
		}
	}
	blocks := fp.extractAll(content)
	if len(blocks) == 0 {
		return &FallbackParseResult{
			ToolCalls:      nil,
			CleanedContent: content,
		}
	}
	merged := fp.mergeAndDedupe(blocks)
	toolCalls := fp.normalize(merged)
	cleaned := fp.cleanContent(content, merged)
	return &FallbackParseResult{
		ToolCalls:      toolCalls,
		CleanedContent: cleaned,
	}
}

// ---------------------------------------------------------------------------
// ShouldUseFallback helpers
// ---------------------------------------------------------------------------

var toolCallPatternMarkers = []string{
	"tool_calls", "function_call", "tool_use", "```json", "```",
	"<function=", "<tool=", "arguments",
}

func (fp *FallbackParser) containsToolCallPatterns(content string) bool {
	if len(strings.TrimSpace(content)) == 0 {
		return false
	}
	for _, m := range toolCallPatternMarkers {
		if strings.Contains(content, m) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// rawBlock: a region of content potentially containing tool calls.
// ---------------------------------------------------------------------------

type rawBlock struct {
	start  int
	end    int
	parsed []ToolCall
}

// ---------------------------------------------------------------------------
// Five extraction strategies
// ---------------------------------------------------------------------------

func (fp *FallbackParser) extractAll(content string) []rawBlock {
	var blocks []rawBlock
	blocks = append(blocks, fp.extractJSONFences(content)...)
	blocks = append(blocks, fp.extractXMLBlocks(content)...)
	blocks = append(blocks, fp.extractToolBlocks(content)...)
	blocks = append(blocks, fp.extractToolUseBlocks(content)...)
	blocks = append(blocks, fp.extractBareJSON(content)...)
	return blocks
}

// stripCodeFences returns content with fenced blocks replaced by spaces.
func (fp *FallbackParser) stripCodeFences(content string) string {
	buf := make([]byte, len(content))
	copy(buf, content)
	inFence := false
	for i := 0; i < len(content); i++ {
		if !inFence && i+2 < len(content) &&
			content[i] == '`' && content[i+1] == '`' && content[i+2] == '`' {
			inFence = true
			for j := i; j < i+3 && j < len(buf); j++ {
				buf[j] = ' '
			}
			i += 2
			continue
		}
		if inFence && i+2 < len(content) &&
			content[i] == '`' && content[i+1] == '`' && content[i+2] == '`' {
			inFence = false
			for j := i; j < i+3 && j < len(buf); j++ {
				buf[j] = ' '
			}
			i += 2
			continue
		}
		if inFence {
			buf[i] = ' '
		}
	}
	return string(buf)
}

// ---------------------------------------------------------------------------
// Strategy 1: JSON Code Fences
// ---------------------------------------------------------------------------

func (fp *FallbackParser) extractJSONFences(content string) []rawBlock {
	var blocks []rawBlock
	idx := 0
	for idx < len(content) {
		fenceStart := strings.Index(content[idx:], "```")
		if fenceStart == -1 {
			break
		}
		fenceStart += idx
		afterFence := fenceStart + 3
		rest := content[afterFence:]

		// Determine where actual content starts (skip language tag + newline)
		contentOffset := 0
		if nl := strings.IndexAny(rest, "\n\r"); nl != -1 {
			lang := strings.TrimSpace(rest[:nl])
			if lang != "" && lang != "json" {
				// Skip non-json fences
				ci := strings.Index(rest[nl+1:], "```")
				if ci == -1 {
					break
				}
				idx = afterFence + nl + 1 + ci
				continue
			}
			contentOffset = nl + 1
		}

		restStart := afterFence + contentOffset

		// Find the closing ```
		closeIdxInRest := strings.Index(rest, "```")
		if closeIdxInRest == -1 {
			closeIdxInRest = len(rest)
		}

		// blockContent excludes the language tag
		blockContent := rest[contentOffset:closeIdxInRest]

		// blockEnd is where the fence content ends
		blockEnd := restStart + closeIdxInRest

		toolCalls := fp.parseToolCallsJSON(blockContent)
		if len(toolCalls) > 0 {
			blocks = append(blocks, rawBlock{
				start:  restStart,
				end:    blockEnd,
				parsed: toolCalls,
			})
		}

		// Continue scanning after the closing fence.
		idx = afterFence + closeIdxInRest + 3
		if idx > len(content) {
			break
		}
	}
	return blocks
}

// ---------------------------------------------------------------------------
// JSON parsing helpers
// ---------------------------------------------------------------------------

func (fp *FallbackParser) parseToolCallsJSON(content string) []ToolCall {
	content = strings.TrimSpace(content)
	if len(content) == 0 {
		return nil
	}

	// Direct array of tool calls
	var tcs []ToolCall
	if err := json.Unmarshal([]byte(content), &tcs); err == nil && len(tcs) > 0 {
		return tcs
	}

	// {tool_calls: [...]}
	var w struct {
		ToolCalls []ToolCall `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(content), &w); err == nil && len(w.ToolCalls) > 0 {
		return w.ToolCalls
	}

	// {function_call: {...}}
	var sf struct {
		FunctionCall struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function_call"`
	}
	if err := json.Unmarshal([]byte(content), &sf); err == nil {
		if sf.FunctionCall.Name != "" && sf.FunctionCall.Arguments != "" {
			return []ToolCall{{
				Type:     "function",
				Function: ToolCallFunction{Name: sf.FunctionCall.Name, Arguments: sf.FunctionCall.Arguments},
			}}
		}
	}

	// {tool_use: {...}}
	var tu struct {
		ToolUse struct {
			Name      string `json:"name"`
			Input     any    `json:"input"`
			InputJSON string `json:"input_json"`
		} `json:"tool_use"`
	}
	if err := json.Unmarshal([]byte(content), &tu); err == nil {
		if tu.ToolUse.Name != "" {
			args := fp.anyToString(tu.ToolUse.Input)
			return []ToolCall{{
				Type:     "function",
				Function: ToolCallFunction{Name: tu.ToolUse.Name, Arguments: args},
			}}
		}
	}

	// {input: {...}} with name field
	var ni struct {
		Name  string `json:"name"`
		Input any    `json:"input"`
	}
	if err := json.Unmarshal([]byte(content), &ni); err == nil {
		if ni.Name != "" {
			args := fp.anyToString(ni.Input)
			return []ToolCall{{
				Type:     "function",
				Function: ToolCallFunction{Name: ni.Name, Arguments: args},
			}}
		}
	}

	return nil
}

func (fp *FallbackParser) anyToString(v any) string {
	if v == nil {
		return "{}"
	}
	switch s := v.(type) {
	case string:
		if s == "" {
			return "{}"
		}
		return s
	case map[string]any:
		b, _ := json.Marshal(s)
		return string(b)
	case []any:
		b, _ := json.Marshal(s)
		return string(b)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// ---------------------------------------------------------------------------
// Strategy 2: Bare JSON Segments
// ---------------------------------------------------------------------------

func (fp *FallbackParser) extractBareJSON(content string) []rawBlock {
	stripped := fp.stripCodeFences(content)
	var blocks []rawBlock
	idx := 0
	for idx < len(stripped) {
		ch := stripped[idx]
		if ch != '{' && ch != '[' {
			idx++
			continue
		}
		end, err := fp.matchBrace(stripped, idx)
		if err != nil {
			idx++
			continue
		}
		if end-idx > 50000 {
			idx++
			continue
		}
		segment := stripped[idx : end+1]
		toolCalls := fp.parseToolCallsJSON(segment)
		if len(toolCalls) > 0 {
			blocks = append(blocks, rawBlock{
				start:  idx,
				end:    end + 1,
				parsed: toolCalls,
			})
			idx = end + 1
			continue
		}
		idx++
	}
	return blocks
}

// matchBrace finds the index of the matching closing bracket for the open
// bracket at position pos.
func (fp *FallbackParser) matchBrace(s string, pos int) (int, error) {
	open := s[pos]
	close := byte('}')
	if open == '[' {
		close = ']'
	}
	depth := 1
	i := pos + 1
	inString, escape := false, false
	for i < len(s) {
		c := s[i]
		if escape {
			escape = false
			if c != '\\' {
				// previous backslash was consumed as escape char
			}
			// always advance i for escaped char
		} else if c == '\\' && inString {
			escape = true
		} else if c == '"' {
			inString = !inString
		} else {
			if c == open {
				depth++
			} else if c == close {
				depth--
				if depth == 0 {
					return i, nil
				}
			}
		}
		i++
	}
	return -1, fmt.Errorf("unmatched bracket")
}

// ---------------------------------------------------------------------------
// Strategy 3: XML <function=name> blocks
// ---------------------------------------------------------------------------

func (fp *FallbackParser) extractXMLBlocks(content string) []rawBlock {
	var blocks []rawBlock
	idx := 0
	for {
		openTag := -1
		for _, prefix := range []string{"<function=", "<tool="} {
			p := strings.Index(content[idx:], prefix)
			if p != -1 && openTag == -1 {
				openTag = p + idx
			}
		}
		if openTag == -1 {
			break
		}

		tagName := "function"
		if strings.HasPrefix(content[openTag:], "<tool=") {
			tagName = "tool"
		}
		prefixLen := len("<" + tagName + "=")
		afterPrefix := openTag + prefixLen
		closeAngle := strings.Index(content[afterPrefix:], ">")
		if closeAngle == -1 {
			break
		}
		name := strings.TrimSpace(content[afterPrefix : afterPrefix+closeAngle])
		if name == "" {
			idx = afterPrefix + closeAngle + 1
			continue
		}

		// Search for closing tag: </function=web_search> or </tool=web_search>
		closeTagPrefix := "</" + tagName + "="
		closeTagIdx := strings.Index(content[afterPrefix+closeAngle+1:], closeTagPrefix)
		var bodyStart, bodyEnd, blockEnd int
		if closeTagIdx != -1 {
			bodyStart = afterPrefix + closeAngle + 1
			bodyEnd = bodyStart + closeTagIdx // end of body content only
			// blockEnd includes the full closing tag so cleanContent removes it
			blockEnd = bodyEnd + len(closeTagPrefix)
			closer := strings.Index(content[blockEnd:], ">")
			if closer != -1 {
				blockEnd += closer + 1
			}
		} else {
			bodyStart = afterPrefix + closeAngle + 1
			bodyEnd = len(content)
			blockEnd = len(content)
		}
		if bodyEnd-bodyStart < 1 {
			idx = blockEnd
			continue
		}
		argsStr := strings.TrimSpace(content[bodyStart:bodyEnd])
		if argsStr == "" {
			idx = blockEnd
			continue
		}
		tc := ToolCall{
			Type:     "function",
			Function: ToolCallFunction{Name: name, Arguments: argsStr},
		}
		blocks = append(blocks, rawBlock{
			start:  openTag,
			end:    blockEnd,
			parsed: []ToolCall{tc},
		})
		idx = blockEnd
	}
	return blocks
}

// ---------------------------------------------------------------------------
// Strategy 4: <tool> blocks
// ---------------------------------------------------------------------------

func (fp *FallbackParser) extractToolBlocks(content string) []rawBlock {
	var blocks []rawBlock
	idx := 0
	for {
		openTag := strings.Index(content[idx:], "<tool>")
		if openTag == -1 {
			break
		}
		openTag += idx
		afterPrefix := openTag + 6
		closeTagIdx := strings.Index(content[afterPrefix:], "</tool>")
		var bodyStart, bodyEnd int
		if closeTagIdx != -1 {
			bodyStart = afterPrefix
			bodyEnd = bodyStart + closeTagIdx
		} else {
			bodyStart = afterPrefix
			bodyEnd = len(content)
		}
		body := strings.TrimSpace(content[bodyStart:bodyEnd])
		toolCalls := fp.parseToolCallsJSON(body)
		if len(toolCalls) > 0 {
			blocks = append(blocks, rawBlock{
				start:  openTag,
				end:    bodyEnd + 6, // include </tool> closing tag
				parsed: toolCalls,
			})
		}
		if closeTagIdx != -1 {
			idx = bodyStart + closeTagIdx + 6
		} else {
			break
		}
	}
	return blocks
}

// ---------------------------------------------------------------------------
// Strategy 5: <tool_use> blocks
// ---------------------------------------------------------------------------

func (fp *FallbackParser) extractToolUseBlocks(content string) []rawBlock {
	var blocks []rawBlock
	idx := 0
	for {
		openTag := strings.Index(content[idx:], "<tool_use>")
		if openTag == -1 {
			break
		}
		openTag += idx
		afterPrefix := openTag + 10
		closeTagIdx := strings.Index(content[afterPrefix:], "</tool_use>")
		var bodyStart, bodyEnd int
		if closeTagIdx != -1 {
			bodyStart = afterPrefix
			bodyEnd = bodyStart + closeTagIdx
		} else {
			bodyStart = afterPrefix
			bodyEnd = len(content)
		}
		body := strings.TrimSpace(content[bodyStart:bodyEnd])
		toolCalls := fp.parseToolCallsJSON(body)
		if len(toolCalls) > 0 {
			blocks = append(blocks, rawBlock{
				start:  openTag,
				end:    bodyEnd + 10, // include </tool_use> closing tag
				parsed: toolCalls,
			})
		}
		if closeTagIdx != -1 {
			idx = bodyStart + closeTagIdx + 10
		} else {
			break
		}
	}
	return blocks
}

// ---------------------------------------------------------------------------
// Merge, dedupe, normalize, clean
// ---------------------------------------------------------------------------

func (fp *FallbackParser) mergeAndDedupe(blocks []rawBlock) []rawBlock {
	if len(blocks) <= 1 {
		return blocks
	}
	// Sort by start position.
	sorted := make([]rawBlock, len(blocks))
	copy(sorted, blocks)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].start < sorted[j].start
	})
	// Merge overlapping blocks.
	var merged []rawBlock
	current := 0
	for current < len(sorted) {
		end := sorted[current].end
		parsed := make([]ToolCall, 0, len(sorted[current].parsed))
		parsed = append(parsed, sorted[current].parsed...)
		next := current + 1
		for next < len(sorted) && sorted[next].start < end {
			if sorted[next].end > end {
				end = sorted[next].end
			}
			parsed = append(parsed, sorted[next].parsed...)
			next++
		}
		merged = append(merged, rawBlock{
			start:  sorted[current].start,
			end:    end,
			parsed: parsed,
		})
		current = next
	}
	// Deduplicate by name+args.
	return dedupeBlocks(merged)
}

func dedupeBlocks(blocks []rawBlock) []rawBlock {
	seen := make(map[string]bool)
	added := make(map[int]bool)
	var result []rawBlock
	for bi, b := range blocks {
		if added[bi] {
			continue
		}
		blockHasNew := false
		for _, tc := range b.parsed {
			key := tc.Function.Name + "\x00" + tc.Function.Arguments
			if !seen[key] {
				seen[key] = true
				blockHasNew = true
			}
		}
		if blockHasNew {
			added[bi] = true
			result = append(result, b)
		}
	}
	return result
}

func (fp *FallbackParser) normalize(blocks []rawBlock) []ToolCall {
	var toolCalls []ToolCall
	seen := make(map[string]bool)
	for _, b := range blocks {
		for _, tc := range b.parsed {
			if tc.Function.Name == "" {
				continue
			}
			if fp.knownToolNames != nil && !fp.knownToolNames(tc.Function.Name) {
				continue
			}
			trimmed := strings.TrimSpace(tc.Function.Arguments)
			if trimmed == "" || !isValidJSON(trimmed) {
				continue
			}
			key := tc.Function.Name + "\x00" + tc.Function.Arguments
			if seen[key] {
				continue
			}
			seen[key] = true
			if tc.ID == "" {
				tc.ID = syntheticID(tc.Function.Name)
			}
			toolCalls = append(toolCalls, tc)
		}
	}
	return toolCalls
}

func syntheticID(name string) string {
	return fmt.Sprintf("fallback_%s_%d", name, time.Now().UnixNano())
}

func isValidJSON(s string) bool {
	return json.Valid([]byte(strings.TrimSpace(s)))
}

func (fp *FallbackParser) cleanContent(content string, blocks []rawBlock) string {
	if len(blocks) == 0 {
		return content
	}
	// Sort ascending by start position for interval merging.
	sorted := make([]rawBlock, len(blocks))
	copy(sorted, blocks)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].start < sorted[j].start
	})
	// Merge overlapping intervals (interval stabbing problem).
	var intervals []struct{ start, end int }
	for _, b := range sorted {
		if len(intervals) > 0 && b.start < intervals[len(intervals)-1].end {
			// Merge with previous interval
			if b.end > intervals[len(intervals)-1].end {
				intervals[len(intervals)-1].end = b.end
			}
		} else {
			intervals = append(intervals, struct{ start, end int }{b.start, b.end})
		}
	}
	// Build result by copying only the segments between removed intervals.
	var result strings.Builder
	pos := 0
	for _, iv := range intervals {
		if iv.start > pos {
			result.WriteString(content[pos:iv.start])
		}
		pos = iv.end
	}
	if pos < len(content) {
		result.WriteString(content[pos:])
	}
	// Normalize whitespace: collapse into single spaces.
	cleaned := strings.Join(strings.Fields(result.String()), " ")
	return strings.TrimSpace(cleaned)
}
