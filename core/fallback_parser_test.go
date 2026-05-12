package core

import (
	"encoding/json"
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
