package core

import (
	"encoding/json"
	"strings"
)

// extractBareJSON extracts tool calls from bare JSON segments (not inside code fences).
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
	return -1, &matchBraceError{open: open, pos: pos}
}

// matchBraceError is returned when an unmatched bracket is found.
type matchBraceError struct {
	open byte
	pos  int
}

func (e *matchBraceError) Error() string {
	return "unmatched bracket"
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
