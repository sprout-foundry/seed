package core

import (
	"encoding/json"
	"strings"
)

// extractNamedToolBlocks extracts tool calls from named block patterns like:
//
//	search {
//	  "query": "hello"
//	}
func (fp *FallbackParser) extractNamedToolBlocks(content string) []rawBlock {
	stripped := fp.stripCodeFences(content)
	var blocks []rawBlock

	idx := 0
	for idx < len(stripped) {
		// Skip to a valid identifier start (letter or underscore)
		if !isIdentStart(stripped[idx]) {
			idx++
			continue
		}

		// Extract the full identifier
		identStart := idx
		for idx < len(stripped) && isIdentChar(stripped[idx]) {
			idx++
		}
		identEnd := idx
		name := stripped[identStart:identEnd]

		// Check if this is a known tool (if filter is set)
		if fp.knownToolNames != nil && !fp.knownToolNames(name) {
			continue
		}

		// After the identifier, skip whitespace
		for idx < len(stripped) && (stripped[idx] == ' ' || stripped[idx] == '\t' || stripped[idx] == '\n' || stripped[idx] == '\r') {
			idx++
		}

		// Expect '{' immediately after whitespace
		if idx >= len(stripped) || stripped[idx] != '{' {
			continue
		}

		braceStart := idx
		braceEnd, err := fp.matchBrace(stripped, braceStart)
		if err != nil {
			continue
		}

		// Extract the full JSON object including braces
		argsStr := strings.TrimSpace(stripped[braceStart : braceEnd+1])

		// Validate that the content is valid JSON
		if !json.Valid([]byte(argsStr)) {
			continue
		}

		// Create the tool call
		tc := ToolCall{
			Type:     "function",
			Function: ToolCallFunction{Name: name, Arguments: argsStr},
		}

		// The block spans from the identifier start to the end of the closing brace
		blockEnd := braceEnd + 1
		blocks = append(blocks, rawBlock{
			start:  identStart,
			end:    blockEnd,
			parsed: []ToolCall{tc},
		})

		// Continue scanning after this block
		idx = blockEnd
	}
	return blocks
}

// isIdentStart returns true if the byte can start a Go-like identifier
// (letter or underscore).
func isIdentStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

// isIdentChar returns true if the byte can appear in a Go-like identifier
// (letter, digit, underscore, or hyphen).
func isIdentChar(b byte) bool {
	return isIdentStart(b) || (b >= '0' && b <= '9') || b == '-'
}

// dedupeBlocks removes duplicate tool calls (same name+arguments) from blocks.
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
