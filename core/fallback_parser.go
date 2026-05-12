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
	// Function-name pattern markers (Strategy 6)
	"name:", "function:", "tool:", "function_name:", "tool_name:",
	"args:", "input:", "parameters:", "params:",
}

func (fp *FallbackParser) containsToolCallPatterns(content string) bool {
	if len(strings.TrimSpace(content)) == 0 {
		return false
	}
	lowerContent := strings.ToLower(content)
	for _, m := range toolCallPatternMarkers {
		if strings.Contains(lowerContent, m) {
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
// Six extraction strategies
// ---------------------------------------------------------------------------

func (fp *FallbackParser) extractAll(content string) []rawBlock {
	var blocks []rawBlock
	blocks = append(blocks, fp.extractJSONFences(content)...)
	blocks = append(blocks, fp.extractXMLBlocks(content)...)
	blocks = append(blocks, fp.extractToolBlocks(content)...)
	blocks = append(blocks, fp.extractToolUseBlocks(content)...)
	blocks = append(blocks, fp.extractFunctionNamePatterns(content)...)
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

		// Find the closing ```
		closeIdxInRest := strings.Index(rest, "```")
		if closeIdxInRest == -1 {
			closeIdxInRest = len(rest)
		}

		// blockContent excludes the language tag
		blockContent := rest[contentOffset:closeIdxInRest]

		toolCalls := fp.parseToolCallsJSON(blockContent)
		if len(toolCalls) > 0 {
			// Block spans from opening fence to end of closing fence (or end of content).
			// closeIdxInRest is relative to rest (content[afterFence:]).
			blockEnd := afterFence + closeIdxInRest
			if closeIdxInRest < len(rest) {
				blockEnd += 3 // include closing fence markers
			}
			blocks = append(blocks, rawBlock{
				start:  fenceStart,
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

	// Single ToolCall object (e.g., {"id": "...", "type": "function", "function": {...}})
	var single ToolCall
	if err := json.Unmarshal([]byte(content), &single); err == nil {
		if single.Function.Name != "" {
			return []ToolCall{single}
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
		} else if c == '\\' && inString {
			escape = true
		} else if c == '"' {
			inString = !inString
		} else if !inString {
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
		bodyRaw := strings.TrimSpace(content[bodyStart:bodyEnd])
		if bodyRaw == "" {
			idx = blockEnd
			continue
		}

		// Try XML <parameter> children first; fall back to raw JSON body.
		argsStr := fp.parseXMLParameters(bodyRaw)
		if argsStr == "" {
			argsStr = bodyRaw
		}
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

// parseXMLParameters parses XML <parameter name="..." value
// children and return a JSON-encoded object string. Returns empty string
// if no <parameter> elements are found.
func (fp *FallbackParser) parseXMLParameters(body string) string {
	params := make(map[string]string)
	idx := 0
	for {
		// Find opening <parameter
		openTag := strings.Index(body[idx:], "<parameter")
		if openTag == -1 {
			break
		}
		openTag += idx

		// Ensure it is exactly "<parameter" (not "<parameterx" etc.)
		tagEnd := openTag + len("<parameter")
		if tagEnd < len(body) {
			ch := body[tagEnd]
			if ch == '<' || ch == '(' || ch == '/' ||
				(ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
				idx = tagEnd
				continue
			}
		}

		// Find closing >
		attrEnd := strings.Index(body[openTag:], ">")
		if attrEnd == -1 {
			break
		}
		attrEnd += openTag

		// Extract attributes from the opening tag
		attrs := body[openTag+10 : attrEnd] // skip "<parameter"
		name := fp.xmlGetAttr(attrs, "name")
		if name == "" {
			idx = attrEnd + 1
			continue
		}

		// Find matching closing tag
		closeTag := strings.Index(body[attrEnd:], "</"+"parameter>")
		var value string
		if closeTag != -1 {
			value = strings.TrimSpace(body[attrEnd : attrEnd+closeTag])
		} else {
			value = strings.TrimSpace(body[attrEnd:])
		}
		params[name] = value
		if closeTag != -1 {
			idx = attrEnd + closeTag + len("</"+"parameter>")
		} else {
			break
		}
	}
	if len(params) == 0 {
		return ""
	}
	b, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	return string(b)
}

// xmlGetAttr extracts a named attribute value from an XML-like attribute string.
func (fp *FallbackParser) xmlGetAttr(attrs string, name string) string {
	idx := 0
	for idx < len(attrs) {
		// Skip whitespace
		for idx < len(attrs) && (attrs[idx] == ' ' || attrs[idx] == '\t' || attrs[idx] == '\n' || attrs[idx] == '\r') {
			idx++
		}
		if idx >= len(attrs) {
			break
		}
		// Find attribute name
		start := idx
		for idx < len(attrs) && attrs[idx] != '=' && attrs[idx] != ' ' && attrs[idx] != '\t' {
			idx++
		}
		attrName := attrs[start:idx]
		if attrName != name {
			continue
		}
		// Expect '='
		if idx >= len(attrs) || attrs[idx] != '=' {
			break
		}
		idx++
		// Skip whitespace
		for idx < len(attrs) && (attrs[idx] == ' ' || attrs[idx] == '\t') {
			idx++
		}
		if idx >= len(attrs) {
			return ""
		}
		// Get value delimiter
		var delim byte
		if attrs[idx] == '"' || attrs[idx] == '\'' {
			delim = attrs[idx]
			idx++
		} else {
			// No quotes, value goes to next space
			end := idx
			for end < len(attrs) && attrs[end] != ' ' && attrs[end] != '\t' && attrs[end] != '>' {
				end++
			}
			return attrs[idx:end]
		}
		// Find closing delimiter
		end := idx
		for end < len(attrs) && attrs[end] != delim {
			end++
		}
		return attrs[idx:end]
	}
	return ""
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
				end:    bodyEnd + 7, // include </tool> closing tag (7 chars)
				parsed: toolCalls,
			})
		}
		if closeTagIdx != -1 {
			idx = bodyStart + closeTagIdx + 7
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
				end:    bodyEnd + 11, // include </tool_use> closing tag (11 chars)
				parsed: toolCalls,
			})
		}
		if closeTagIdx != -1 {
			idx = bodyStart + closeTagIdx + 11
		} else {
			break
		}
	}
	return blocks
}

// ---------------------------------------------------------------------------
// Strategy 6: Function-name pattern extraction
// ---------------------------------------------------------------------------

// extractFunctionNamePatterns detects `name: tool_name` followed by balanced
// JSON arguments. This handles patterns like:
//
//	name: search
//	arguments: {"query": "hello"}
//
// or variations with different key names (e.g., "function:", "tool:", "name:").
func (fp *FallbackParser) extractFunctionNamePatterns(content string) []rawBlock {
	var blocks []rawBlock

	// We scan for lines matching "name: <tool_name>" followed by a line
	// containing JSON arguments (balanced braces/brackets).
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])

		// Look for "name: tool_name" pattern (case-insensitive key)
		name, ok := fp.extractNameValue(line)
		if !ok {
			continue
		}

		// Check if this is a known tool (if filter is set)
		if fp.knownToolNames != nil && !fp.knownToolNames(name) {
			continue
		}

		// Look for arguments in subsequent lines
		argsJSON, argsEndLine := fp.findArgumentsInLines(lines, i+1)
		if argsJSON == "" {
			continue
		}

		// Calculate byte offsets for the block
		blockStart := fp.lineToOffset(content, i)
		if argsEndLine > len(lines) {
			argsEndLine = len(lines)
		}
		blockEnd := fp.lineToOffset(content, argsEndLine)
		if blockEnd > len(content) {
			blockEnd = len(content)
		}

		tc := ToolCall{
			Type:     "function",
			Function: ToolCallFunction{Name: name, Arguments: argsJSON},
		}
		blocks = append(blocks, rawBlock{
			start:  blockStart,
			end:    blockEnd,
			parsed: []ToolCall{tc},
		})

		// Skip past the arguments block
		i = argsEndLine - 1 // -1 because the for loop will increment i
	}

	return blocks
}

// extractNameValue checks if a line matches "name: value" pattern and returns
// the value. It handles variations like "name:", "Name:", "NAME:", etc.
// The matched key must be preceded by whitespace or be at the start of the
// line to avoid false positives (e.g., "command_name:" should not match).
func (fp *FallbackParser) extractNameValue(line string) (string, bool) {
	// Match patterns like "name: value", "Name: value", "NAME: value"
	// Also handle "function: value", "tool: value", "function_name: value"
	keyPatterns := []string{"name:", "function:", "tool:", "function_name:", "tool_name:"}
	lowerLine := strings.ToLower(line)
	for _, pattern := range keyPatterns {
		idx := strings.Index(lowerLine, pattern)
		if idx == -1 {
			continue
		}

		// Require word boundary: the pattern must be preceded by whitespace
		// or be at the start of the line. This prevents false positives like
		// "command_name:" matching "name:" or "tool_use:" matching "tool:".
		if idx > 0 {
			prev := line[idx-1]
			if prev != ' ' && prev != '\t' && prev != '\r' {
				continue
			}
		}

		// Extract the value after the colon
		valueStart := idx + len(pattern)
		if valueStart >= len(line) {
			continue
		}

		// Skip whitespace
		for valueStart < len(line) && (line[valueStart] == ' ' || line[valueStart] == '\t') {
			valueStart++
		}

		if valueStart >= len(line) {
			continue
		}

		// Extract value (trim quotes if present)
		value := strings.TrimSpace(line[valueStart:])
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}

		if value != "" {
			return value, true
		}
	}

	return "", false
}

// findArgumentsInLines searches for JSON arguments in lines starting from
// startIndex. It looks for patterns like "arguments: {...}" or just "{...}".
// Returns the JSON string and the line number AFTER the last consumed line.
func (fp *FallbackParser) findArgumentsInLines(lines []string, startIndex int) (string, int) {
	if startIndex >= len(lines) {
		return "", 0
	}

	// Look for arguments in the next few lines (up to 10 lines)
	maxLines := startIndex + 10
	if maxLines > len(lines) {
		maxLines = len(lines)
	}

	for i := startIndex; i < maxLines; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}

		// Check for "arguments: {...}" pattern
		argsKeyPatterns := []string{"arguments:", "args:", "input:", "parameters:", "params:"}
		lowerLine := strings.ToLower(line)
		origLine := lines[i]
		leadingSpace := len(origLine) - len(line)
		for _, pattern := range argsKeyPatterns {
			idx := strings.Index(lowerLine, pattern)
			if idx == -1 {
				continue
			}

			// Word boundary check: the pattern must be preceded by whitespace
			// or be at the start of the line. This prevents false positives like
			// "myarguments:" matching "arguments:".
			if idx > 0 {
				prev := lowerLine[idx-1]
				if prev != ' ' && prev != '\t' && prev != '\r' {
					continue
				}
			}

			// Extract value after the colon
			valueStart := idx + len(pattern)
			if valueStart >= len(line) {
				continue
			}

			// Skip whitespace
			for valueStart < len(line) && (line[valueStart] == ' ' || line[valueStart] == '\t') {
				valueStart++
			}

			if valueStart >= len(line) {
				continue
			}

			value := line[valueStart:]

			// If the value starts with { or [, try to find balanced JSON
			if len(value) > 0 && (value[0] == '{' || value[0] == '[') {
				// Try to parse as JSON directly
				if json.Valid([]byte(value)) {
					return value, i + 1
				}

				// Handle case where value is a quoted JSON string like
				// "{\"key\": \"val\"}" — the outer quotes wrap escaped JSON.
				// Find the matching closing quote and try the unescaped content.
				if len(value) > 1 && value[0] == '{' {
					unescaped := fp.unescapeJSONString(value)
					if unescaped != "" && json.Valid([]byte(unescaped)) {
						return unescaped, i + 1
					}
				}

				// If not valid, try to collect more lines
				// Use leadingSpace + valueStart to get the correct offset
				// on the original (non-trimmed) line.
				jsonStr, endLine := fp.collectJSONLines(lines, i, leadingSpace+valueStart)
				if jsonStr != "" {
					return jsonStr, endLine
				}
				return "", 0
			}

			// If the value is a quoted string, try to parse it as JSON
			if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'')) {
				unquoted := value[1 : len(value)-1]
				if json.Valid([]byte(unquoted)) {
					return unquoted, i + 1
				}
				// Try unescaping \" to " (model may output escaped JSON in quotes)
				unescaped := strings.ReplaceAll(unquoted, `\"`, `"`)
				if json.Valid([]byte(unescaped)) {
					return unescaped, i + 1
				}
			}
		}

		// Check if the line starts with { or [ (bare JSON)
		if len(line) > 0 && (line[0] == '{' || line[0] == '[') {
			if json.Valid([]byte(line)) {
				return line, i + 1
			}
			// Try to collect more lines
			// Use leadingSpace so collectJSONLines starts from the actual
			// content position on the original (non-trimmed) line.
			jsonStr, endLine := fp.collectJSONLines(lines, i, leadingSpace)
			if jsonStr != "" {
				return jsonStr, endLine
			}
			return "", 0
		}
	}

	return "", 0
}

// collectJSONLines collects lines starting from lineIdx, offset, and tries to
// form valid JSON by accumulating lines until the JSON is balanced.
// Returns the JSON string and the line number AFTER the last consumed line.
func (fp *FallbackParser) collectJSONLines(lines []string, lineIdx int, offset int) (string, int) {
	if lineIdx >= len(lines) {
		return "", 0
	}

	// Start with the current line from offset
	accumulated := strings.TrimSpace(lines[lineIdx][offset:])

	// Check if we already have balanced JSON
	if json.Valid([]byte(accumulated)) {
		return accumulated, lineIdx + 1
	}

	// Accumulate more lines until we have balanced JSON or hit a limit
	for i := lineIdx + 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			accumulated += "\n"
			continue
		}

		accumulated += "\n" + line

		if json.Valid([]byte(accumulated)) {
			// Normalize to compact canonical JSON so indentation from the
			// original content doesn't affect the result.
			var parsed any
			if err := json.Unmarshal([]byte(accumulated), &parsed); err == nil {
				normalized, _ := json.Marshal(parsed)
				return string(normalized), i + 1
			}
			return accumulated, i + 1
		}

		// Stop if we've accumulated too many lines (limit: 20 lines)
		if i-lineIdx > 20 {
			break
		}
	}

	return "", 0
}

// lineToOffset converts a line number to a byte offset in the content string.
func (fp *FallbackParser) lineToOffset(content string, lineNum int) int {
	if lineNum <= 0 {
		return 0
	}

	offset := 0
	currentLine := 0
	for offset < len(content) && currentLine < lineNum {
		if content[offset] == '\n' {
			currentLine++
			if currentLine == lineNum {
				return offset + 1
			}
		}
		offset++
	}

	return offset
}

// unescapeJSONString handles the case where a JSON string value is wrapped
// in outer quotes with escaped inner quotes, like:
//
//	{"key": "val"}  (from the raw text: "{\"key\": \"val\"}")
//
// It strips the outer quotes and unescapes \" to ".
func (fp *FallbackParser) unescapeJSONString(s string) string {
	if len(s) < 2 {
		return ""
	}

	// Check if the string looks like a quoted JSON: starts with { or [
	// and ends with } or ] (possibly preceded by ")
	trimmed := strings.TrimSpace(s)
	if len(trimmed) < 2 {
		return ""
	}

	// Case 1: trimmed starts and ends with the same quote
	if (trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"') ||
		(trimmed[0] == '\'' && trimmed[len(trimmed)-1] == '\'') {
		inner := trimmed[1 : len(trimmed)-1]
		// Unescape \" to "
		unescaped := strings.ReplaceAll(inner, `\"`, `"`)
		return unescaped
	}

	// Case 2: starts with { or [ but contains \" patterns suggesting
	// it's a quoted JSON string that lost its outer quotes
	if (trimmed[0] == '{' || trimmed[0] == '[') && strings.Contains(trimmed, `\"`) {
		// Try to find the matching closing quote at the end
		// The pattern is: {\"key\": \"val\"}
		// We need to unescape \" to " and check if valid JSON
		unescaped := strings.ReplaceAll(trimmed, `\"`, `"`)
		if json.Valid([]byte(unescaped)) {
			return unescaped
		}
	}

	return ""
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
			if tc.Type == "" {
				tc.Type = "function"
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
