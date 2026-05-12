package core

import (
	"encoding/json"
	"strings"
)

// extractXMLBlocks extracts tool calls from XML <function=...> and <tool=...> blocks.
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

// parseXMLParameters parses XML <parameter name="..." value> children and returns
// a JSON-encoded object string. Returns empty string if no <parameter> elements are found.
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
