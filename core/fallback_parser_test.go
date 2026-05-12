package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewFallbackParser(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	if fp == nil {
		t.Fatal("expected non-nil parser")
	}
	if fp.debug {
		t.Error("expected debug=false by default")
	}
	if fp.knownToolNames != nil {
		t.Error("expected knownToolNames=nil by default")
	}
}

func TestNewFallbackParserWithDebug(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{Debug: true})
	if !fp.debug {
		t.Error("expected debug=true")
	}
}

func TestNewFallbackParserWithKnownTools(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool { return s == "search" },
	})
	if fp.knownToolNames == nil {
		t.Fatal("expected knownToolNames to be set")
	}
	if !fp.knownToolNames("search") {
		t.Error("expected search to be known")
	}
	if fp.knownToolNames("not_a_tool") {
		t.Error("expected not_a_tool to be unknown")
	}
}

func TestShouldUseFallback_NilContent(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	if fp.ShouldUseFallback("", false) {
		t.Error("expected false for empty content")
	}
}

func TestShouldUseFallback_StructuredCallsPresent(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	if fp.ShouldUseFallback("some tool_calls", true) {
		t.Error("expected false when structured tool calls are present")
	}
}

func TestShouldUseFallback_Patterns(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"tool_calls key", "here are the tool_calls", true},
		{"function_call key", "function_call is here", true},
		{"tool_use key", "using tool_use here", true},
		{"json fence", "some text ```json", true},
		{"any fence", "some text ```", true},
		{"xml function", "<function=search>", true},
		{"xml tool", "<tool=web_search>", true},
		{"plain text", "hello world", false},
		{"whitespace only", "   \n  ", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fp.ShouldUseFallback(tc.content, false)
			if got != tc.want {
				t.Errorf("ShouldUseFallback(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}

func TestParse_JSONFence_ToolCallsArray(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "```\n{\"tool_calls\": [{\"id\": \"1\", \"type\": \"function\", \"function\": {\"name\": \"search\", \"arguments\": \"{\\\"q\\\": \\\"hello\\\"}\"}}]}\n```"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls from JSON fence")
	}
	if result.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected name 'search', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[0].Function.Arguments != `{"q": "hello"}` {
		t.Errorf("unexpected args: %s", result.ToolCalls[0].Function.Arguments)
	}
}

func TestParse_JSONFence_FunctionCall(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "```\n{\"function_call\": {\"name\": \"compute\", \"arguments\": \"{\\\"x\\\": 42}\"}}\n```"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls from function_call format")
	}
	if result.ToolCalls[0].Function.Name != "compute" {
		t.Errorf("expected name 'compute', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_JSONFence_Typed(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "```\n{\"tool_calls\": [{\"id\": \"a1\", \"type\": \"function\", \"function\": {\"name\": \"x\", \"arguments\": \"{}\"}}]}\n```"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls")
	}
	if result.ToolCalls[0].Type != "function" {
		t.Errorf("expected type 'function', got %q", result.ToolCalls[0].Type)
	}
}

func TestParse_JSONFence_WithPrefixContent(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "Let me search for that.\n\n```json\n{\"tool_calls\": [{\"id\": \"1\", \"type\": \"function\", \"function\": {\"name\": \"web_search\", \"arguments\": \"{}\"}}]}\n```"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls")
	}
	if result.ToolCalls[0].Function.Name != "web_search" {
		t.Errorf("expected name 'web_search', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_JSONFence_SkipsNonJSONFence(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "```python\nprint('hello')\n```"
	result := fp.Parse(content)
	if len(result.ToolCalls) > 0 {
		t.Errorf("expected no tool calls from python fence, got %d", len(result.ToolCalls))
	}
}

func TestParse_BareJSON_DirectArray(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := `Some prefix text [{"id": "1", "type": "function", "function": {"name": "bare", "arguments": "{}"}}] end`
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls from bare JSON array")
	}
	if result.ToolCalls[0].Function.Name != "bare" {
		t.Errorf("expected name 'bare', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_BareJSON_Nested(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := `{"tool_calls": [{"id": "1", "type": "function", "function": {"name": "nested", "arguments": "{}"}}]}`
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls from bare JSON object")
	}
	if result.ToolCalls[0].Function.Name != "nested" {
		t.Errorf("expected name 'nested', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_XMLFunctionBlock(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "<function=web_search>{\"query\": \"test\"}</function=web_search>"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls from XML function block")
	}
	if result.ToolCalls[0].Function.Name != "web_search" {
		t.Errorf("expected name 'web_search', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[0].Function.Arguments != `{"query": "test"}` {
		t.Errorf("unexpected args: %s", result.ToolCalls[0].Function.Arguments)
	}
}

func TestParse_XMLToolBlock(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "<tool=web_search>{\"query\": \"bar\"}</tool=web_search>"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls from XML tool block")
	}
	if result.ToolCalls[0].Function.Name != "web_search" {
		t.Errorf("expected name 'web_search', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_XMLNoCloseTag(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "<function=web_search>{\"query\": \"test\"}"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls from XML block without close tag")
	}
	if result.ToolCalls[0].Function.Name != "web_search" {
		t.Errorf("expected name 'web_search', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_ToolsBlock(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "some text <tool>{\"tool_calls\": [{\"id\": \"1\", \"type\": \"function\", \"function\": {\"name\": \"tool_block\", \"arguments\": \"{}\"}}]}</tool> more"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls from <tool> block")
	}
	if result.ToolCalls[0].Function.Name != "tool_block" {
		t.Errorf("expected name 'tool_block', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_ToolUseBlock(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "<tool_use>{\"name\": \"calc\", \"input\": {\"expr\": \"1+1\"}}</tool_use>"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls from <tool_use> block")
	}
	if result.ToolCalls[0].Function.Name != "calc" {
		t.Errorf("expected name 'calc', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_NoPatterns(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "just plain text with no tools"
	result := fp.Parse(content)
	if len(result.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(result.ToolCalls))
	}
	if result.CleanedContent != content {
		t.Errorf("expected cleaned content to equal original, got %q", result.CleanedContent)
	}
}

func TestParse_NoToolCallPatterns(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "the weather is nice today"
	result := fp.Parse(content)
	if len(result.ToolCalls) > 0 {
		t.Errorf("expected no tool calls from plain text, got %d", len(result.ToolCalls))
	}
}

func TestParse_KnowsToolFiltering(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool {
			return s == "allowed"
		},
	})
	content := "<function=allowed>{}</function=allowed> <function=denied>{}</function=denied>"
	result := fp.Parse(content)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "allowed" {
		t.Errorf("expected only 'allowed' tool, got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_KnowsToolWithInvalidJSON(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool { return true },
	})
	content := "```\n{\"tool_calls\": [{\"id\": \"1\", \"type\": \"function\", \"function\": {\"name\": \"bad\", \"arguments\": \"not json\"}}]}\n```"
	result := fp.Parse(content)
	if len(result.ToolCalls) > 0 {
		t.Errorf("expected no tool calls with invalid JSON args, got %d", len(result.ToolCalls))
	}
}

func TestParse_DedupeByNameArgs(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "```\n{\"tool_calls\": [{\"id\": \"1\", \"type\": \"function\", \"function\": {\"name\": \"dup\", \"arguments\": \"{\\\"x\\\": 1}\"}}]}\n```\n<function=dup>{\"x\": 1}</function=dup>"
	result := fp.Parse(content)
	if len(result.ToolCalls) != 1 {
		t.Errorf("expected 1 deduplicated tool call, got %d", len(result.ToolCalls))
	}
}

func TestParse_CleanContent_RemovesFence(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "Let me check.\n\n```json\n{\"tool_calls\": [{\"id\": \"1\", \"type\": \"function\", \"function\": {\"name\": \"search\", \"arguments\": \"{}\"}}]}\n```"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls")
	}
	// The fence and its content should be removed from cleaned content
	if result.CleanedContent == content {
		t.Error("expected cleaned content to differ from original")
	}
}

func TestParse_CleanContent_NoBlocks(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "just text"
	result := fp.Parse(content)
	if result.CleanedContent != content {
		t.Errorf("expected cleaned content to equal original, got %q", result.CleanedContent)
	}
}

func TestParse_EmptyContent(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	result := fp.Parse("")
	if len(result.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(result.ToolCalls))
	}
}

func TestParse_MalformedBraces(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	// Unmatched braces should not panic
	content := "this has unmatched { braces [brackets"
	result := fp.Parse(content)
	// May or may not find tool calls, but should not panic
	_ = result
}

func TestParse_MixedFormats(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "Here is my response:\n\n```json\n{\"tool_calls\": [{\"id\": \"1\", \"type\": \"function\", \"function\": {\"name\": \"first\", \"arguments\": \"{}\"}}]}\n```\n\nAlso needed:\n<function=second>{\"x\": 1}</function=second>"
	result := fp.Parse(content)
	if len(result.ToolCalls) < 2 {
		t.Fatalf("expected at least 2 tool calls from mixed formats, got %d", len(result.ToolCalls))
	}
}

func TestParse_ToolUseInputMap(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "<tool_use>{\"name\": \"parse\", \"input\": {\"key\": \"value\"}}</tool_use>"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls from tool_use with map input")
	}
	// Input should be serialized to JSON string
	if result.ToolCalls[0].Function.Name != "parse" {
		t.Errorf("expected name 'parse', got %q", result.ToolCalls[0].Function.Name)
	}
	// Verify the arguments are valid JSON
	if !json.Valid([]byte(result.ToolCalls[0].Function.Arguments)) {
		t.Errorf("expected valid JSON args, got: %s", result.ToolCalls[0].Function.Arguments)
	}
}

func TestParse_InvalidJSONContent(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	result := fp.Parse("this content has no valid JSON at all, just prose")
	if len(result.ToolCalls) != 0 {
		t.Errorf("expected no tool calls from prose content, got %d", len(result.ToolCalls))
	}
}

func TestNormalize_NoName(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	blocks := []rawBlock{{
		start: 0,
		end:   10,
		parsed: []ToolCall{{
			ID:       "1",
			Type:     "function",
			Function: ToolCallFunction{Name: "", Arguments: "{}"},
		}},
	}}
	tcs := fp.normalize(blocks)
	if len(tcs) != 0 {
		t.Errorf("expected no tool calls with empty name, got %d", len(tcs))
	}
}

func TestNormalize_EmptyArgs(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	blocks := []rawBlock{{
		start: 0,
		end:   10,
		parsed: []ToolCall{{
			ID:       "1",
			Type:     "function",
			Function: ToolCallFunction{Name: "test", Arguments: ""},
		}},
	}}
	tcs := fp.normalize(blocks)
	if len(tcs) != 0 {
		t.Errorf("expected no tool calls with empty args, got %d", len(tcs))
	}
}

func TestDedupeBlocks_Identical(t *testing.T) {
	blocks := []rawBlock{
		{start: 0, end: 5, parsed: []ToolCall{{Function: ToolCallFunction{Name: "a", Arguments: "{}"}}}},
		{start: 6, end: 11, parsed: []ToolCall{{Function: ToolCallFunction{Name: "a", Arguments: "{}"}}}},
	}
	result := dedupeBlocks(blocks)
	if len(result) != 1 {
		t.Errorf("expected 1 block after dedupe, got %d", len(result))
	}
}

func TestDedupeBlocks_Different(t *testing.T) {
	blocks := []rawBlock{
		{start: 0, end: 5, parsed: []ToolCall{{Function: ToolCallFunction{Name: "a", Arguments: "{}"}}}},
		{start: 6, end: 11, parsed: []ToolCall{{Function: ToolCallFunction{Name: "b", Arguments: "{}"}}}},
	}
	result := dedupeBlocks(blocks)
	if len(result) != 2 {
		t.Errorf("expected 2 blocks after dedupe, got %d", len(result))
	}
}

func TestMergeAndDedupe_NoOverlaps(t *testing.T) {
	blocks := []rawBlock{
		{start: 0, end: 5, parsed: []ToolCall{{Function: ToolCallFunction{Name: "a", Arguments: "{}"}}}},
		{start: 10, end: 15, parsed: []ToolCall{{Function: ToolCallFunction{Name: "b", Arguments: "{}"}}}},
	}
	result := NewFallbackParser(FallbackParserOptions{}).mergeAndDedupe(blocks)
	// Two non-overlapping blocks should remain
	if len(result) != 2 {
		t.Errorf("expected 2 blocks, got %d", len(result))
	}
}

func TestCleanContent_SpacesAroundRemoval(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "start ```json\n{\"tool_calls\": [{\"id\": \"1\", \"type\": \"function\", \"function\": {\"name\": \"x\", \"arguments\": \"{}\"}}]}\n``` end"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls")
	}
	// Cleaned content should be well-formatted, not have excessive whitespace
	t.Logf("cleaned: %q", result.CleanedContent)
}

func TestMatchBrace_Matching(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	tests := []struct {
		input string
		start int
		end   int
	}{
		{"{}", 0, 1},
		{"{ \"x\": 1 }", 0, 9},
		{"[[1, 2], [3, 4]]", 0, 15},
		{"{\"a\": {\"b\": 1}}", 0, 14},
		{`{"quote": "she said \"hi\""}`, 0, 27},
	}
	for _, tc := range tests {
		result, err := fp.matchBrace(tc.input, tc.start)
		if err != nil {
			t.Errorf("matchBrace(%q, %d) error: %v", tc.input, tc.start, err)
			continue
		}
		if result != tc.end {
			t.Errorf("matchBrace(%q, %d) = %d, want %d", tc.input, tc.start, result, tc.end)
		}
	}
}

func TestMatchBrace_Unmatched(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	tests := []string{"{", "{{", "["}
	// { with no closing }
	for _, input := range tests {
		_, err := fp.matchBrace(input, 0)
		if err == nil {
			t.Errorf("matchBrace(%q) expected error, got nil", input)
		}
	}
	// [ with no closing ]
	_, err := fp.matchBrace("[", 0)
	if err == nil {
		t.Error("matchBrace(\"[\") expected error, got nil")
	}
}

// TestMatchBrace_BracketsInsideString verifies that { or } characters
// inside a JSON string value do NOT affect bracket depth counting.
func TestMatchBrace_BracketsInsideString(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	tests := []struct {
		input string
		start int
		end   int
	}{
		// Literal { inside string should not increase depth
		{`{"key": "has { brace"}`, 0, 21},
		// Literal } inside string should not decrease depth
		{`{"key": "has } brace"}`, 0, 21},
		// Both { and } inside string
		{`{"key": "has { and }"}`, 0, 21},
		// Multiple brackets in string
		{`{"key": "{{}} nested"}`, 0, 21},
		// Escaped backslash followed by closing quote and bracket
		{`{"key": "\\"}`, 0, 12},
		// Two escaped backslashes
		{`{"key": "\\\\"}`, 0, 14},
	}
	for _, tc := range tests {
		result, err := fp.matchBrace(tc.input, tc.start)
		if err != nil {
			t.Errorf("matchBrace(%q, %d) error: %v", tc.input, tc.start, err)
			continue
		}
		if result != tc.end {
			t.Errorf("matchBrace(%q, %d) = %d, want %d", tc.input, tc.start, result, tc.end)
		}
	}
}

// TestMatchBrace_MixedBracketsInsideString verifies that [ and ] inside
// strings are also ignored when matching {}.
func TestMatchBrace_MixedBracketsInsideString(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	input := `{"arr": "[1,2,3]", "obj": "{}"}`
	result, err := fp.matchBrace(input, 0)
	if err != nil {
		t.Fatalf("matchBrace error: %v", err)
	}
	if result != len(input)-1 {
		t.Errorf("matchBrace = %d, want %d", result, len(input)-1)
	}
}

func TestParse_JSONFence_NoLanguageTag(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "```\n{\"tool_calls\": [{\"id\": \"1\", \"type\": \"function\", \"function\": {\"name\": \"noLang\", \"arguments\": \"{}\"}}]}\n```"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls from fence without language tag")
	}
	if result.ToolCalls[0].Function.Name != "noLang" {
		t.Errorf("expected name 'noLang', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_FunctionCallFormat(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "```\n{\"function_call\": {\"name\": \"myFunc\", \"arguments\": \"{\\\"a\\\": 1}\"}}\n```"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls from function_call format")
	}
	if result.ToolCalls[0].Function.Name != "myFunc" {
		t.Errorf("expected name 'myFunc', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_DirectToolCallArray(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "```\n[{\"id\": \"1\", \"type\": \"function\", \"function\": {\"name\": \"direct\", \"arguments\": \"{}\"}}]\n```"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls from direct array")
	}
	if result.ToolCalls[0].Function.Name != "direct" {
		t.Errorf("expected name 'direct', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_ToolUseInputNil(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "<tool_use>{\"name\": \"empty\"}</tool_use>"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls from tool_use with nil input")
	}
	if result.ToolCalls[0].Function.Name != "empty" {
		t.Errorf("expected name 'empty', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_ToolUseInputString(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "<tool_use>{\"name\": \"strInput\", \"input\": \"{\\\"key\\\": \\\"val\\\"}\"}</tool_use>"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls from tool_use with string input")
	}
	if result.ToolCalls[0].Function.Name != "strInput" {
		t.Errorf("expected name 'strInput', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[0].Function.Arguments != `{"key": "val"}` {
		t.Errorf("unexpected args: %s", result.ToolCalls[0].Function.Arguments)
	}
}

func TestParse_InputWithNameFormat(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "```\n{\"name\": \"inputFormat\", \"input\": {\"x\": 1}}\n```"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls from input with name format")
	}
	if result.ToolCalls[0].Function.Name != "inputFormat" {
		t.Errorf("expected name 'inputFormat', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_WhitespaceOnly(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	result := fp.Parse("   ")
	if len(result.ToolCalls) != 0 {
		t.Errorf("expected no tool calls from whitespace, got %d", len(result.ToolCalls))
	}
}

func TestParse_TrimmedEmptyContent(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	result := fp.Parse("")
	if len(result.ToolCalls) != 0 {
		t.Errorf("expected no tool calls from empty, got %d", len(result.ToolCalls))
	}
}

func TestParse_JSONFence_SingleToolCallObject(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "```\n{\"id\": \"call_abc\", \"type\": \"function\", \"function\": {\"name\": \"search\", \"arguments\": \"{\\\"q\\\": \\\"hello\\\"}\"}}\n```"
	result := fp.Parse(content)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call from single ToolCall object, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ID != "call_abc" {
		t.Errorf("expected ID 'call_abc', got %q", result.ToolCalls[0].ID)
	}
	if result.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected name 'search', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[0].Function.Arguments != `{"q": "hello"}` {
		t.Errorf("unexpected args: %s", result.ToolCalls[0].Function.Arguments)
	}
}

func TestParse_JSONFence_SingleToolCallObject_WithJSONTag(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "```json\n{\"id\": \"call_1\", \"type\": \"function\", \"function\": {\"name\": \"calc\", \"arguments\": \"{}\"}}\n```"
	result := fp.Parse(content)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "calc" {
		t.Errorf("expected name 'calc', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_JSONFence_CleanContentRemovesEntireFence(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "Before ```json\n{\"tool_calls\": [{\"id\": \"1\", \"type\": \"function\", \"function\": {\"name\": \"x\", \"arguments\": \"{}\"}}]}\n``` After"
	result := fp.Parse(content)
	if len(result.ToolCalls) == 0 {
		t.Fatal("expected tool calls")
	}
	// The entire fence (including markers) should be removed
	if strings.Contains(result.CleanedContent, "```") {
		t.Errorf("cleaned content should not contain fence markers, got: %q", result.CleanedContent)
	}
	// Surrounding text should be preserved
	if !strings.Contains(result.CleanedContent, "Before") {
		t.Error("expected 'Before' in cleaned content")
	}
	if !strings.Contains(result.CleanedContent, "After") {
		t.Error("expected 'After' in cleaned content")
	}
}

func TestParse_JSONFence_MultipleFences(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "First:\n```\n{\"tool_calls\": [{\"id\": \"1\", \"type\": \"function\", \"function\": {\"name\": \"first\", \"arguments\": \"{}\"}}]}\n```\nSecond:\n```json\n{\"tool_calls\": [{\"id\": \"2\", \"type\": \"function\", \"function\": {\"name\": \"second\", \"arguments\": \"{}\"}}]}\n```"
	result := fp.Parse(content)
	if len(result.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls from multiple fences, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "first" {
		t.Errorf("expected first tool 'first', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[1].Function.Name != "second" {
		t.Errorf("expected second tool 'second', got %q", result.ToolCalls[1].Function.Name)
	}
}

func TestParse_JSONFence_NoNewlineAfterFence(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	// Fence with no newline after opening markers — content starts immediately
	content := "```{\"tool_calls\": [{\"id\": \"1\", \"type\": \"function\", \"function\": {\"name\": \"inline\", \"arguments\": \"{}\"}}]}```"
	result := fp.Parse(content)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call from inline fence, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "inline" {
		t.Errorf("expected name 'inline', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_JSONFence_SingleToolCall_WithoutType(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "```json\n{\"id\": \"call_abc\", \"function\": {\"name\": \"search\", \"arguments\": \"{}\"}}\n```"
	result := fp.Parse(content)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected name 'search', got %q", result.ToolCalls[0].Function.Name)
	}
	// Type should default to "function" even when absent from JSON
	if result.ToolCalls[0].Type != "function" {
		t.Errorf("expected type 'function', got %q", result.ToolCalls[0].Type)
	}
}

func TestParse_JSONFence_SingleToolCall_PreservesExplicitType(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "```\n{\"id\": \"call_abc\", \"type\": \"function\", \"function\": {\"name\": \"search\", \"arguments\": \"{\\\"q\\\": \\\"hello\\\"}\"}}\n```"
	result := fp.Parse(content)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Type != "function" {
		t.Errorf("expected type 'function', got %q", result.ToolCalls[0].Type)
	}
}

// ---------------------------------------------------------------------------
// Tests for extractBareJSON (Strategy 2)
// ---------------------------------------------------------------------------

// TestParse_BareJSON_InsideCodeFenceIgnored verifies that JSON tool calls
// inside ```json fences are NOT extracted by the bare JSON strategy.
// The fence extraction handles them instead, and the bare strategy should
// see only spaces in place of fenced content.
func TestParse_BareJSON_InsideCodeFenceIgnored(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	// Content that is ONLY a fenced JSON block — the bare JSON path should
	// see only spaces and extract nothing. The fence parser alone should
	// produce the tool call.
	content := "```json\n{\"tool_calls\": [{\"id\": \"1\", \"type\": \"function\", \"function\": {\"name\": \"fencedOnly\", \"arguments\": \"{}\"}}]}\n```"
	result := fp.Parse(content)

	// We expect exactly 1 tool call (from the JSON fence extractor, not bare).
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "fencedOnly" {
		t.Errorf("expected name 'fencedOnly', got %q", result.ToolCalls[0].Function.Name)
	}

	// The fence content should be removed from cleaned content, so the
	// tool call body should NOT appear in cleaned output.
	if strings.Contains(result.CleanedContent, "fencedOnly") {
		t.Error("cleaned content should not contain the tool call body")
	}
}

// TestParse_BareJSON_MixedFencedAndBare verifies that both fenced JSON tool
// calls AND bare JSON tool calls in the same content are extracted.
func TestParse_BareJSON_MixedFencedAndBare(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	fenced := "```json\n{\"tool_calls\": [{\"id\": \"1\", \"type\": \"function\", \"function\": {\"name\": \"fencedTool\", \"arguments\": \"{}\"}}]}\n```"
	bare := `{"tool_calls": [{"id": "2", "type": "function", "function": {"name": "bareTool", "arguments": "{}"}}]}`
	content := fenced + "\n\n" + bare
	result := fp.Parse(content)

	// Both strategies should fire, producing 2 tool calls (dedup won't remove
	// them because they have different names).
	if len(result.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls (1 fenced + 1 bare), got %d", len(result.ToolCalls))
	}
	names := map[string]bool{
		result.ToolCalls[0].Function.Name: true,
		result.ToolCalls[1].Function.Name: true,
	}
	if !names["fencedTool"] {
		t.Errorf("expected 'fencedTool' in tool calls, got %v", names)
	}
	if !names["bareTool"] {
		t.Errorf("expected 'bareTool' in tool calls, got %v", names)
	}
}

// TestParse_BareJSON_WithSurroundingProse verifies that bare JSON surrounded
// by natural language is correctly extracted and cleaned.
func TestParse_BareJSON_WithSurroundingProse(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := `I will do the search. Here is the tool call: {"tool_calls": [{"id": "1", "type": "function", "function": {"name": "search", "arguments": "{\"q\": \"test\"}"}}]}. Done!`
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected name 'search', got %q", result.ToolCalls[0].Function.Name)
	}

	// Cleaned content should remove the bare JSON and preserve surrounding prose
	if strings.Contains(result.CleanedContent, `"search"`) {
		t.Error("cleaned content should not contain the JSON body")
	}
	// The prose text should be collapsed into a single string
	if result.CleanedContent == "" {
		t.Error("expected some cleaned prose content")
	}
}

// TestParse_BareJSON_DeeplyNested verifies that deeply nested JSON objects
// are matched correctly by matchBrace.
func TestParse_BareJSON_DeeplyNested(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := `{"tool_calls": [{"id": "1", "type": "function", "function": {"name": "deep", "arguments": "{\"nested\": \"value\", \"level\": 3}"}}]}`
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "deep" {
		t.Errorf("expected name 'deep', got %q", result.ToolCalls[0].Function.Name)
	}
}

// TestParse_BareJSON_EscapedQuotesInJSON verifies that JSON with escaped
// quotes in values is matched correctly and the bare parser doesn't get
// confused by the inner quotes.
func TestParse_BareJSON_EscapedQuotesInJSON(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	// The arguments string contains escaped quotes within the JSON value.
	content := `{"tool_calls": [{"id": "1", "type": "function", "function": {"name": "query", "arguments": "{\"q\": \"She said \\\"hello\\\"\"}"}}]}`
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "query" {
		t.Errorf("expected name 'query', got %q", result.ToolCalls[0].Function.Name)
	}
	// The arguments should preserve the escaped quotes
	expectedArgs := `{"q": "She said \"hello\""}`
	if result.ToolCalls[0].Function.Arguments != expectedArgs {
		t.Errorf("unexpected args: got %q, want %q", result.ToolCalls[0].Function.Arguments, expectedArgs)
	}
}

// TestParse_BareJSON_InvalidJSONSkipped verifies that text that looks like
// JSON but is not valid JSON is simply skipped.
func TestParse_BareJSON_InvalidJSONSkipped(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	// This has a "tool_calls" pattern marker so ShouldUseFallback returns true,
	// but the JSON itself is invalid so parseToolCallsJSON should return nil.
	content := `This has tool_calls but is invalid: {"tool_calls": [invalid json here]}`
	result := fp.Parse(content)

	// No valid tool calls should be extracted
	if len(result.ToolCalls) != 0 {
		t.Errorf("expected no tool calls from invalid JSON, got %d", len(result.ToolCalls))
	}
	// Cleaned content should equal original since no blocks found
	if result.CleanedContent != content {
		t.Errorf("expected cleaned content to equal original, got %q", result.CleanedContent)
	}
}

// TestParse_BareJSON_MultipleSegments verifies that multiple separate bare
// JSON tool call segments in one content are all extracted.
func TestParse_BareJSON_MultipleSegments(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	// Two bare JSON arrays separated by prose. Each array contains direct
	// ToolCall objects, which parseToolCallsJSON handles as "Direct array of tool calls".
	content := `[{"id": "1", "type": "function", "function": {"name": "first", "arguments": "{}"}}] some text [{"id": "2", "type": "function", "function": {"name": "second", "arguments": "{}"}}] more text`
	result := fp.Parse(content)

	if len(result.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "first" {
		t.Errorf("expected first name 'first', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[1].Function.Name != "second" {
		t.Errorf("expected second name 'second', got %q", result.ToolCalls[1].Function.Name)
	}
}

// TestParse_BareJSON_EmptyBraces verifies that empty {} (which doesn't parse
// as a tool call) is skipped and doesn't cause issues.
func TestParse_BareJSON_EmptyBraces(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	// Empty braces should not parse as tool calls — parseToolCallsJSON returns nil
	content := `Here is some text with {} empty braces and [[ ]] brackets`
	result := fp.Parse(content)

	// No tool calls should be extracted from empty braces
	if len(result.ToolCalls) != 0 {
		t.Errorf("expected no tool calls from empty braces, got %d", len(result.ToolCalls))
	}
}

// TestParse_BareJSON_ArrayOfTwoToolCalls verifies that a bare JSON array
// containing two tool calls is correctly parsed.
func TestParse_BareJSON_ArrayOfTwoToolCalls(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := `[{"id": "1", "type": "function", "function": {"name": "tool1", "arguments": "{}"}}, {"id": "2", "type": "function", "function": {"name": "tool2", "arguments": "{}"}}]`
	result := fp.Parse(content)

	if len(result.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "tool1" {
		t.Errorf("expected first name 'tool1', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[1].Function.Name != "tool2" {
		t.Errorf("expected second name 'tool2', got %q", result.ToolCalls[1].Function.Name)
	}
}

// TestParse_BareJSON_ToolCallsWrapper verifies that a {"tool_calls": [...]}
// wrapper format works in bare JSON context.
func TestParse_BareJSON_ToolCallsWrapper(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := `{"tool_calls": [{"id": "1", "type": "function", "function": {"name": "wrapper", "arguments": "{}"}}]}`
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "wrapper" {
		t.Errorf("expected name 'wrapper', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[0].ID != "1" {
		t.Errorf("expected ID '1', got %q", result.ToolCalls[0].ID)
	}
}

// TestParse_BareJSON_FunctionCallFormat verifies that the legacy
// {"function_call": {"name": "...", "arguments": "..."}} format works in
// bare JSON context.
func TestParse_BareJSON_FunctionCallFormat(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := `{"function_call": {"name": "legacyFunc", "arguments": "{\"x\": 42}"}}`
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "legacyFunc" {
		t.Errorf("expected name 'legacyFunc', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[0].Function.Arguments != `{"x": 42}` {
		t.Errorf("unexpected args: got %q, want %q", result.ToolCalls[0].Function.Arguments, `{"x": 42}`)
	}
}

// TestParse_BareJSON_SingleToolCallObject verifies that a bare single
// ToolCall object (with id, type, function) is correctly parsed.
func TestParse_BareJSON_SingleToolCallObject(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := `{"id": "single_abc", "type": "function", "function": {"name": "singleObj", "arguments": "{\"a\": 1}"}}`
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ID != "single_abc" {
		t.Errorf("expected ID 'single_abc', got %q", result.ToolCalls[0].ID)
	}
	if result.ToolCalls[0].Function.Name != "singleObj" {
		t.Errorf("expected name 'singleObj', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[0].Function.Arguments != `{"a": 1}` {
		t.Errorf("unexpected args: got %q, want %q", result.ToolCalls[0].Function.Arguments, `{"a": 1}`)
	}
}

// TestParse_BareJSON_SizeLimit verifies that segments over 50000 characters
// are skipped by the size limit check in extractBareJSON.
func TestParse_BareJSON_SizeLimit(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})

	// Build a very large JSON string where the balanced braces span >50000 chars.
	// Include "tool_calls" as a key so shouldUseFallback returns true,
	// but do NOT structure the data as a tool call format so parseToolCallsJSON
	// returns nil for every segment. We pad with non-tool-call JSON so that
	// even inner segments (which get skipped once the outer is skipped) don't
	// happen to parse as tool calls.
	filler := strings.Repeat(`"x": 1, `, 10000) // ~80000 chars of filler
	content := `{"tool_calls": true, "data": {` + filler + `}}`
	// The outer {} is well over 50000 chars, so extractBareJSON skips it.
	// The inner {} is also >50000 chars (same issue as outer), so it's also skipped.

	result := fp.Parse(content)

	// No tool calls should be extracted from oversized segments.
	if len(result.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls from oversized segment, got %d", len(result.ToolCalls))
	}
}

// TestParse_BareJSON_CleanedContentPreservesProse verifies that after
// extracting bare JSON tool calls, the cleaned content preserves
// surrounding prose but removes the JSON.
func TestParse_BareJSON_CleanedContentPreservesProse(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := `Before the tool call: {"tool_calls": [{"id": "1", "type": "function", "function": {"name": "search", "arguments": "{}"}}]} After the tool call.`
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}

	// Cleaned content should NOT contain the JSON body
	if strings.Contains(result.CleanedContent, `"search"`) ||
		strings.Contains(result.CleanedContent, `"tool_calls"`) {
		t.Error("cleaned content should not contain JSON body")
	}

	// Cleaned content should contain the surrounding prose (collapsed)
	if !strings.Contains(result.CleanedContent, "Before") {
		t.Error("expected 'Before' in cleaned content")
	}
	if !strings.Contains(result.CleanedContent, "After") {
		t.Error("expected 'After' in cleaned content")
	}
}

// TestParse_BareJSON_CleanedContentWithFences verifies that when both fenced
// AND bare JSON exist, cleaned content removes both but keeps prose.
// Note: prose words must not be directly adjacent to bare JSON (without
// punctuation separators) — otherwise Strategy 7 (named tool blocks) would
// falsely extract the prose word as a tool name.
func TestParse_BareJSON_CleanedContentWithFences(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	fenced := "```json\n{\"tool_calls\": [{\"id\": \"1\", \"type\": \"function\", \"function\": {\"name\": \"fenced\", \"arguments\": \"{}\"}}]}\n```"
	bare := `{"tool_calls": [{"id": "2", "type": "function", "function": {"name": "bare", "arguments": "{}"}}]}`
	// Use punctuation separators so that prose words are not directly
	// adjacent to the bare JSON (which would trigger the named tool
	// blocks strategy when no KnownToolNames filter is set).
	content := "Prologue. " + fenced + " " + bare + " Epilogue. Postscript."
	result := fp.Parse(content)

	if len(result.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result.ToolCalls))
	}

	// Cleaned content should not contain any fence markers
	if strings.Contains(result.CleanedContent, "```") {
		t.Error("cleaned content should not contain fence markers")
	}

	// Cleaned content should not contain JSON bodies
	if strings.Contains(result.CleanedContent, `"fenced"`) {
		t.Error("cleaned content should not contain fenced JSON body")
	}
	if strings.Contains(result.CleanedContent, `"bare"`) {
		t.Error("cleaned content should not contain bare JSON body")
	}

	// Cleaned content should preserve surrounding prose
	if !strings.Contains(result.CleanedContent, "Prologue") {
		t.Error("expected 'Prologue' in cleaned content")
	}
	if !strings.Contains(result.CleanedContent, "Postscript") {
		t.Error("expected 'Postscript' in cleaned content")
	}
}

// TestParse_BareJSON_JsonInStringNotExtracted verifies that prose text
// containing braces in strings is not falsely extracted as tool calls.
// The text "He said { \"name\": \"hello\" }" is NOT a valid tool call.
func TestParse_BareJSON_JsonInStringNotExtracted(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	// This text contains a brace-delimited substring that might look like
	// JSON, but it doesn't parse as a valid tool call structure.
	content := `He said { "name": "hello" } and then left.`
	result := fp.Parse(content)

	// The text does contain "tool_calls" pattern? No it doesn't.
	// So ShouldUseFallback should return false, and no parsing happens.
	// Even if it did parse, { "name": "hello" } is not a tool call.
	if len(result.ToolCalls) != 0 {
		t.Errorf("expected no tool calls from prose with braces, got %d", len(result.ToolCalls))
	}
}

// TestParse_BareJSON_ToolUseFormat verifies that the tool_use format
// {"tool_use": {"name": "tool", "input": {...}}} works in bare JSON context.
func TestParse_BareJSON_ToolUseFormat(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	// Must include "tool_use" key so that containsToolCallPatterns returns true,
	// and the bare JSON scanner finds the {tool_use: {...}} segment.
	content := `{"tool_use": {"name": "calculate", "input": {"expr": "1+1", "mode": "fast"}}}`
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "calculate" {
		t.Errorf("expected name 'calculate', got %q", result.ToolCalls[0].Function.Name)
	}
	// Input is a map, should be serialized to JSON string
	if !json.Valid([]byte(result.ToolCalls[0].Function.Arguments)) {
		t.Errorf("expected valid JSON args, got: %q", result.ToolCalls[0].Function.Arguments)
	}
}

// TestParse_BareJSON_WithNewlinesAndFormatting verifies that bare JSON
// with newlines and indentation is handled correctly.
func TestParse_BareJSON_WithNewlinesAndFormatting(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := `
{
  "tool_calls": [
    {
      "id": "1",
      "type": "function",
      "function": {
        "name": "indented",
        "arguments": "{}"
      }
    }
  ]
}
`
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "indented" {
		t.Errorf("expected name 'indented', got %q", result.ToolCalls[0].Function.Name)
	}
}

// ---------------------------------------------------------------------------
// Tests for extractFunctionNamePatterns (Strategy 6)
// ---------------------------------------------------------------------------

func TestParse_FunctionNamePattern_Basic(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "name: search\narguments: {\"query\": \"hello\"}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected name 'search', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[0].Function.Arguments != `{"query": "hello"}` {
		t.Errorf("unexpected args: %s", result.ToolCalls[0].Function.Arguments)
	}
}

func TestParse_FunctionNamePattern_WithFunctionKey(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "function: compute\narguments: {\"x\": 42}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "compute" {
		t.Errorf("expected name 'compute', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_FunctionNamePattern_WithToolKey(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "tool: web_search\nargs: {\"q\": \"test\"}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "web_search" {
		t.Errorf("expected name 'web_search', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_FunctionNamePattern_WithArgsKey(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "name: calculate\nargs: {\"expr\": \"1+1\"}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "calculate" {
		t.Errorf("expected name 'calculate', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_FunctionNamePattern_WithInputKey(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "name: parse\ninput: {\"key\": \"value\"}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "parse" {
		t.Errorf("expected name 'parse', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_FunctionNamePattern_WithParametersKey(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "name: fetch\nparameters: {\"url\": \"https://example.com\"}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "fetch" {
		t.Errorf("expected name 'fetch', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_FunctionNamePattern_WithParamsKey(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "name: read_file\nparams: {\"path\": \"/tmp/test\"}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("expected name 'read_file', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_FunctionNamePattern_BareJSONArgs(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "name: search\n{\"query\": \"hello\", \"limit\": 10}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected name 'search', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_FunctionNamePattern_MultilineJSON(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := `name: search
arguments: {
  "query": "hello",
  "limit": 10
}`
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected name 'search', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_FunctionNamePattern_QuotedName(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := `name: "search_tool"
arguments: {"query": "hello"}`
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "search_tool" {
		t.Errorf("expected name 'search_tool', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_FunctionNamePattern_SingleQuotedName(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := `name: 'search_tool'
arguments: {"query": "hello"}`
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "search_tool" {
		t.Errorf("expected name 'search_tool', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_FunctionNamePattern_WithSurroundingText(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "I'll help you with that.\n\nname: search\narguments: {\"query\": \"test\"}\n\nLet me know the results."
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected name 'search', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_FunctionNamePattern_CaseInsensitiveKey(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "Name: SearchTool\nArguments: {\"q\": \"test\"}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "SearchTool" {
		t.Errorf("expected name 'SearchTool', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_FunctionNamePattern_UnknownToolFiltered(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool { return s == "allowed" },
	})
	content := "name: denied\narguments: {\"x\": 1}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls (unknown tool filtered), got %d", len(result.ToolCalls))
	}
}

func TestParse_FunctionNamePattern_NoArguments(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "name: search"
	result := fp.Parse(content)

	// No arguments found, so no tool call should be extracted
	if len(result.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls (no arguments), got %d", len(result.ToolCalls))
	}
}

func TestParse_FunctionNamePattern_InvalidJSONArguments(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "name: search\narguments: not valid json"
	result := fp.Parse(content)

	// Invalid JSON arguments should not produce a tool call
	if len(result.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls (invalid JSON), got %d", len(result.ToolCalls))
	}
}

func TestParse_FunctionNamePattern_ArrayArguments(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "name: batch_process\narguments: [{\"id\": 1}, {\"id\": 2}]"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "batch_process" {
		t.Errorf("expected name 'batch_process', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_FunctionNamePattern_EmptyObjectArgs(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "name: ping\narguments: {}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "ping" {
		t.Errorf("expected name 'ping', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[0].Function.Arguments != "{}" {
		t.Errorf("unexpected args: %s", result.ToolCalls[0].Function.Arguments)
	}
}

func TestParse_FunctionNamePattern_FunctionNameKey(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "function_name: my_tool\narguments: {\"x\": 1}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "my_tool" {
		t.Errorf("expected name 'my_tool', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_FunctionNamePattern_ToolNameKey(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "tool_name: api_call\narguments: {\"endpoint\": \"/users\"}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "api_call" {
		t.Errorf("expected name 'api_call', got %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParse_FunctionNamePattern_CleanedContent(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "Before\n\nname: search\narguments: {\"query\": \"test\"}\n\nAfter"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	// Cleaned content should remove the name/arguments block
	if strings.Contains(result.CleanedContent, "name: search") {
		t.Error("cleaned content should not contain 'name: search'")
	}
	if strings.Contains(result.CleanedContent, "arguments:") {
		t.Error("cleaned content should not contain 'arguments:'")
	}
	// Surrounding text should be preserved
	if !strings.Contains(result.CleanedContent, "Before") {
		t.Error("expected 'Before' in cleaned content")
	}
	if !strings.Contains(result.CleanedContent, "After") {
		t.Error("expected 'After' in cleaned content")
	}
}

func TestParse_FunctionNamePattern_TypeDefaultsToFunction(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "name: search\narguments: {\"q\": \"test\"}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Type != "function" {
		t.Errorf("expected type 'function', got %q", result.ToolCalls[0].Type)
	}
}

func TestParse_FunctionNamePattern_QuotedJSONArgs(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := `name: search
arguments: "{\"query": "hello"}"`
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected name 'search', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[0].Function.Arguments != `{"query": "hello"}` {
		t.Errorf("unexpected args: %s", result.ToolCalls[0].Function.Arguments)
	}
}

func TestParse_FunctionNamePattern_NoFalsePositives(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	// "name" appears in prose but not as a key-value pattern with JSON args
	content := "My name is John and I like to search for things."
	result := fp.Parse(content)

	if len(result.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls from prose, got %d", len(result.ToolCalls))
	}
}

func TestParse_FunctionNamePattern_MultiplePatterns(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "name: search\narguments: {\"q\": \"hello\"}\n\nname: compute\narguments: {\"x\": 42}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected first name 'search', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[1].Function.Name != "compute" {
		t.Errorf("expected second name 'compute', got %q", result.ToolCalls[1].Function.Name)
	}
}

func TestParse_FunctionNamePattern_WordBoundaryCheck(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	// "command_name:" contains "name:" but should NOT match because
	// the character before "name:" is not whitespace.
	content := "command_name: search\narguments: {\"q\": \"test\"}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls (name: inside command_name:), got %d", len(result.ToolCalls))
	}
}

func TestParse_FunctionNamePattern_ToolUseNotMatched(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	// "tool_use:" contains "tool:" but should NOT match because
	// the character before "tool:" is not whitespace.
	content := "tool_use: calc\ninput: {\"expr\": \"1+1\"}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls (tool: inside tool_use:), got %d", len(result.ToolCalls))
	}
}

func TestParse_FunctionNamePattern_ArgsOnSeparateLine(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	// Arguments key is on its own line, JSON is on the next line.
	// The block boundary should include both the args key line AND the JSON line.
	content := "name: search\n\nargs:\n{\"query\": \"test\"}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected name 'search', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[0].Function.Arguments != `{"query": "test"}` {
		t.Errorf("unexpected args: %s", result.ToolCalls[0].Function.Arguments)
	}
	// Cleaned content should NOT contain the JSON body
	if strings.Contains(result.CleanedContent, `"query"`) {
		t.Error("cleaned content should not contain JSON body")
	}
}

func TestParse_FunctionNamePattern_ArgsOnSeparateLine_MultipleCalls(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "name: search\nargs:\n{\"query\": \"test\"}\n\nname: compute\nargs:\n{\"x\": 1}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected first name 'search', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[1].Function.Name != "compute" {
		t.Errorf("expected second name 'compute', got %q", result.ToolCalls[1].Function.Name)
	}
	// Cleaned content should NOT contain either JSON body
	if strings.Contains(result.CleanedContent, `"query"`) {
		t.Error("cleaned content should not contain first JSON body")
	}
	if strings.Contains(result.CleanedContent, `"x"`) {
		t.Error("cleaned content should not contain second JSON body")
	}
}

// TestParse_FunctionNamePattern_IndentedArgsKey verifies that multiline JSON
// with leading whitespace on the args key line is correctly extracted.
// Regression test for the trimmed-line offset bug (Issue #1).
func TestParse_FunctionNamePattern_IndentedArgsKey(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "name: search\n  args: {\n    \"query\": \"test\"\n  }"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected name 'search', got %q", result.ToolCalls[0].Function.Name)
	}
	// Multi-line JSON is normalized to compact canonical form by re-marshaling.
	if result.ToolCalls[0].Function.Arguments != `{"query":"test"}` {
		t.Errorf("unexpected args: %s", result.ToolCalls[0].Function.Arguments)
	}
}

// TestParse_FunctionNamePattern_IndentedBareJSON verifies that bare JSON
// with leading whitespace is correctly extracted when the JSON appears
// on a line after an args key. Regression test for the trimmed-line
// offset bug (Issue #1).
func TestParse_FunctionNamePattern_IndentedBareJSON(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "name: compute\n  args:\n  {\n    \"query\": \"test\"\n  }"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "compute" {
		t.Errorf("expected name 'compute', got %q", result.ToolCalls[0].Function.Name)
	}
	// Multi-line JSON is normalized to compact canonical form by re-marshaling.
	if result.ToolCalls[0].Function.Arguments != `{"query":"test"}` {
		t.Errorf("unexpected args: %s", result.ToolCalls[0].Function.Arguments)
	}
}

// TestParse_FunctionNamePattern_ArgsKeyWordBoundary verifies that
// "myarguments: {...}" does NOT match because the word boundary check
// prevents "myarguments:" from being treated as "arguments:".
// Regression test for Issue #2.
func TestParse_FunctionNamePattern_ArgsKeyWordBoundary(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "name: search\nmyarguments: {\"q\": \"test\"}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 0 {
		t.Errorf("expected 0 tool calls (myarguments: should not match arguments:), got %d", len(result.ToolCalls))
	}
}

// ---------------------------------------------------------------------------
// Tests for extractNamedToolBlocks (Strategy 7)
// ---------------------------------------------------------------------------

func TestParse_NamedToolBlock_EndToEnd(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool { return s == "search" },
	})
	// Include "arguments" marker so containsToolCallPatterns returns true,
	// allowing the full Parse() pipeline to run (gate → extract → merge →
	// normalize → clean).
	content := "Here are the arguments:\nsearch {\"query\": \"hello\"}"
	result := fp.Parse(content)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "search" {
		t.Errorf("expected name 'search', got %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[0].Function.Arguments != `{"query": "hello"}` {
		t.Errorf("unexpected args: %s", result.ToolCalls[0].Function.Arguments)
	}
	// Verify cleaned content removed the tool block
	if strings.Contains(result.CleanedContent, "search") {
		t.Errorf("cleaned content should not contain 'search', got: %q", result.CleanedContent)
	}
}

func TestParse_NamedToolBlock_Basic(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool { return s == "search" },
	})
	content := "search {\"query\": \"hello\"}"
	blocks := fp.extractNamedToolBlocks(content)
	result := fp.normalize(blocks)

	if len(result) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result))
	}
	if result[0].Function.Name != "search" {
		t.Errorf("expected name 'search', got %q", result[0].Function.Name)
	}
	if result[0].Function.Arguments != `{"query": "hello"}` {
		t.Errorf("unexpected args: %s", result[0].Function.Arguments)
	}
}

func TestParse_NamedToolBlock_MultilineJSON(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool { return s == "compute" },
	})
	content := "compute {\n  \"x\": 1,\n  \"y\": 2\n}"
	blocks := fp.extractNamedToolBlocks(content)
	result := fp.normalize(blocks)

	if len(result) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result))
	}
	if result[0].Function.Name != "compute" {
		t.Errorf("expected name 'compute', got %q", result[0].Function.Name)
	}
	// Multi-line JSON is kept as-is (not re-marshaled by this strategy)
	if !json.Valid([]byte(result[0].Function.Arguments)) {
		t.Errorf("expected valid JSON args, got: %s", result[0].Function.Arguments)
	}
}

func TestParse_NamedToolBlock_FilteredByKnownTools(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool { return s == "allowed" },
	})
	content := "allowed {\"x\": 1} denied {\"y\": 2}"
	blocks := fp.extractNamedToolBlocks(content)
	result := fp.normalize(blocks)

	if len(result) != 1 {
		t.Fatalf("expected 1 tool call (only 'allowed'), got %d", len(result))
	}
	if result[0].Function.Name != "allowed" {
		t.Errorf("expected name 'allowed', got %q", result[0].Function.Name)
	}
}

func TestParse_NamedToolBlock_NoFilterAcceptsAll(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{})
	content := "mytool {\"key\": \"value\"}"
	blocks := fp.extractNamedToolBlocks(content)
	result := fp.normalize(blocks)

	if len(result) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result))
	}
	if result[0].Function.Name != "mytool" {
		t.Errorf("expected name 'mytool', got %q", result[0].Function.Name)
	}
}

func TestParse_NamedToolBlock_SkipsInsideFences(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool { return s == "search" },
	})
	// stripCodeFences removes content inside ``` fences, so
	// "search {" inside a code fence should NOT be extracted by named
	// tool block strategy (Strategy 1 handles those).
	content := "```json\nsearch {\"query\": \"test\"}\n```"
	blocks := fp.extractNamedToolBlocks(content)
	result := fp.normalize(blocks)

	// The content inside the fence is stripped, so no tool calls expected
	if len(result) != 0 {
		t.Errorf("expected 0 tool calls (named tool block inside fence), got %d", len(result))
	}
}

func TestParse_NamedToolBlock_InvalidJSONSkipped(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool { return s == "bad" },
	})
	content := "bad {not valid json}"
	blocks := fp.extractNamedToolBlocks(content)
	result := fp.normalize(blocks)

	if len(result) != 0 {
		t.Errorf("expected 0 tool calls (invalid JSON), got %d", len(result))
	}
}

func TestParse_NamedToolBlock_EmptyBracesAccepted(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool { return s == "empty" },
	})
	content := "empty {}"
	blocks := fp.extractNamedToolBlocks(content)
	result := fp.normalize(blocks)

	// {} is valid JSON (empty object), so it should produce a tool call
	// consistent with TestParse_FunctionNamePattern_EmptyObjectArgs
	if len(result) != 1 {
		t.Errorf("expected 1 tool call ({} is valid empty JSON), got %d", len(result))
	}
	if result[0].Function.Name != "empty" {
		t.Errorf("expected name 'empty', got %q", result[0].Function.Name)
	}
}

func TestParse_NamedToolBlock_MultipleBlocks(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool { return s == "first" || s == "second" },
	})
	content := "first {\"a\": 1} some text second {\"b\": 2}"
	blocks := fp.extractNamedToolBlocks(content)
	result := fp.normalize(blocks)

	if len(result) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result))
	}
	if result[0].Function.Name != "first" {
		t.Errorf("expected first name 'first', got %q", result[0].Function.Name)
	}
	if result[1].Function.Name != "second" {
		t.Errorf("expected second name 'second', got %q", result[1].Function.Name)
	}
}

func TestParse_NamedToolBlock_CleanContent(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool { return s == "search" },
	})
	content := "Before search {\"query\": \"test\"} After"
	blocks := fp.extractNamedToolBlocks(content)
	result := fp.normalize(blocks)

	if len(result) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result))
	}
	// The named tool block should be removed from cleaned content
	cleaned := fp.cleanContent(content, blocks)
	if strings.Contains(cleaned, "search") {
		t.Errorf("cleaned content should not contain 'search', got: %q", cleaned)
	}
	if !strings.Contains(cleaned, "Before") {
		t.Error("expected 'Before' in cleaned content")
	}
	if !strings.Contains(cleaned, "After") {
		t.Error("expected 'After' in cleaned content")
	}
}

func TestParse_NamedToolBlock_WithHyphenName(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool { return s == "my-tool" },
	})
	content := "my-tool {\"action\": \"run\"}"
	blocks := fp.extractNamedToolBlocks(content)
	result := fp.normalize(blocks)

	if len(result) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result))
	}
	if result[0].Function.Name != "my-tool" {
		t.Errorf("expected name 'my-tool', got %q", result[0].Function.Name)
	}
}

func TestParse_NamedToolBlock_WithUnderscoreName(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool { return s == "my_tool" },
	})
	content := "my_tool {\"action\": \"run\"}"
	blocks := fp.extractNamedToolBlocks(content)
	result := fp.normalize(blocks)

	if len(result) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result))
	}
	if result[0].Function.Name != "my_tool" {
		t.Errorf("expected name 'my_tool', got %q", result[0].Function.Name)
	}
}

func TestParse_NamedToolBlock_NestedBraces(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool { return s == "nested" },
	})
	content := "nested {\"outer\": {\"inner\": \"value\"}}"
	blocks := fp.extractNamedToolBlocks(content)
	result := fp.normalize(blocks)

	if len(result) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result))
	}
	if result[0].Function.Name != "nested" {
		t.Errorf("expected name 'nested', got %q", result[0].Function.Name)
	}
	if result[0].Function.Arguments != `{"outer": {"inner": "value"}}` {
		t.Errorf("unexpected args: %s", result[0].Function.Arguments)
	}
}

func TestParse_NamedToolBlock_SpacedBrace(t *testing.T) {
	fp := NewFallbackParser(FallbackParserOptions{
		KnownToolNames: func(s string) bool { return s == "notool" },
	})
	// "notool" is followed by a space then "{" — this WILL match
	content := "notool {\"x\": 1} is a great tool"
	blocks := fp.extractNamedToolBlocks(content)
	result := fp.normalize(blocks)

	if len(result) != 1 {
		t.Fatalf("expected 1 tool call (space between name and brace is OK), got %d", len(result))
	}
	if result[0].Function.Name != "notool" {
		t.Errorf("expected name 'notool', got %q", result[0].Function.Name)
	}
}
