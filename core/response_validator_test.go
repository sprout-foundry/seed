
package core

import (
	"testing"
)

func newTestValidator(t *testing.T) *ResponseValidator {
	t.Helper()
	return NewResponseValidator(ResponseValidatorOptions{
		DebugLog: func(format string, args ...interface{}) {
			// debug output captured but not asserted — validates no panic on nil
		},
	})
}

// ---------------------------------------------------------------------------
// IsIncomplete — integration tests
// ---------------------------------------------------------------------------

func TestIsIncomplete_TrailingEllipsis(t *testing.T) {
	rv := newTestValidator(t)

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "plain ellipsis",
			content: "This is incomplete and has enough words to pass the short threshold, but trails off...",
			want:    true,
		},
		{
			name:    "ellipsis with trailing space",
			content: "This is incomplete and has enough words to pass the short threshold, but trails off...  ",
			want:    true,
		},
		{
			name:    "multiple ellipsis",
			content: "This is a response that starts with ellipsis and has enough words to pass the short check...",
			want:    true,
		},
		{
			name:    "code then ellipsis",
			content: "Here is the code you requested, it does the thing you asked for:\n```\nfunc foo() {}\n```\nMore text explaining the code above...",
			want:    true,
		},
		{
			name:    "no ellipsis",
			content: "This is a complete sentence with enough words to pass the short threshold comfortably.",
			want:    false,
		},
		{
			name:    "ellipsis in middle",
			content: "Wait... actually, here is the answer with enough words to pass the short check comfortably.",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rv.IsIncomplete(tt.content)
			if got != tt.want {
				t.Errorf("IsIncomplete(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestIsIncomplete_AbruptEnding(t *testing.T) {
	rv := newTestValidator(t)

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "ends with comma",
			content: "This is a sentence with enough words to pass the short threshold,",
			want:    true,
		},
		{
			name:    "ends with hyphen",
			content: "This is a sentence with enough words to pass the short threshold-",
			want:    true,
		},
		{
			name:    "ends with period",
			content: "This is a sentence with enough words to pass the short threshold.",
			want:    false,
		},
		{
			name:    "ends with exclamation",
			content: "This is a sentence with enough words to pass the short threshold!",
			want:    false,
		},
		{
			name:    "ends with question mark",
			content: "This is a sentence with enough words to pass the short threshold?",
			want:    false,
		},
		{
			name:    "ends with semicolon",
			content: "This is a sentence with enough words to pass the short threshold;",
			want:    false,
		},
		{
			name:    "ends with colon",
			content: "Here is the list with enough words to pass the short threshold:",
			want:    false,
		},
		{
			name:    "ends with letter (URL-like)",
			content: "See https://example.com/path for the documentation you requested earlier today please",
			want:    false,
		},
		{
			name:    "ends with digit",
			content: "The value you requested is 42 and that should answer your question completely",
			want:    false,
		},
		{
			name:    "ends with slash",
			content: "Look in /etc/config/ to find the file you were looking for earlier",
			want:    false,
		},
		{
			name:    "contains code block, no punctuation",
			content: "Here is the code you requested with enough words:\n```go\nfunc main() {}\n```",
			want:    false,
		},
		{
			name:    "contains http, no punctuation",
			content: "Visit http://example.com for more information about the topic you asked about",
			want:    false,
		},
		{
			name:    "empty content",
			content: "",
			want:    false,
		},
		{
			name:    "whitespace only",
			content: "   ",
			want:    true, // <10 words, not a complete answer
		},
		{
			name:    "ends with emoji",
			content: "All good and the task is complete with enough words here 😊",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rv.IsIncomplete(tt.content)
			if got != tt.want {
				t.Errorf("IsIncomplete(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestIsIncomplete_UnusuallyShort(t *testing.T) {
	rv := newTestValidator(t)

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"one word", "hello", true},
		{"five words", "this is five words only", true},
		{"nine words", "one two three four five six seven eight nine", true},
		{"ten words", "one two three four five six seven eight nine ten", false},
		{"short but complete - done", "done", false},
		{"short but complete - completed", "completed", false},
		{"short but complete - finished", "finished", false},
		{"short but complete - yes", "yes", false},
		{"short but complete - no", "no", false},
		{"short but complete - error:", "error: not found", false},
		{"short but complete - success", "success", false},
		{"short but complete - failed", "failed", false},
		{"short with period still short", "Ok.", true},
		{"case insensitive done", "DONE", false},
		{"case insensitive Yes", "Yes", false},
		{"substring no should not match", "nothing", true},
		{"substring done should not match", "downloaded", true},
		{"substring not should not match", "not yet", true},
		{"contains done in longer text", "Task is done now", true}, // "done" is not exact match, <10 words
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rv.IsIncomplete(tt.content)
			if got != tt.want {
				t.Errorf("IsIncomplete(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestIsIncomplete_UnclosedCodeBlock(t *testing.T) {
	rv := newTestValidator(t)

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name: "open code block only",
			content: "Here is the complete code you requested, it does the thing:\n```\nfunc foo() {}",
			want: true, // unclosed code block
		},
		{
			name: "closed code block",
			content: "Here is the complete code you requested, it does the thing:\n```\nfunc foo() {}\n```",
			want: false, // 11 words, closed block
		},
		{
			name: "two open code blocks",
			content: "Here is some content that has enough words to pass the short check:\n```\nfoo\n```\n```\nbar",
			want: true, // unclosed code block (3 ``` markers)
		},
		{
			name: "two closed code blocks",
			content: "Here is some content that has enough words to pass the short check:\n```\nfoo\n```\n```\nbar\n```",
			want: false, // 13 words, 4 ``` markers (even)
		},
		{
			name: "three backticks in text",
			content: "Here is some content that has enough words to pass the short check, and mentions Use ``` for inline code",
			want: true, // 3 ``` markers (odd)
		},
		{
			name: "no code blocks",
			content: "Just plain text with enough words here to pass the short threshold comfortably.",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rv.IsIncomplete(tt.content)
			if got != tt.want {
				t.Errorf("IsIncomplete(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestIsIncomplete_CombinedChecks(t *testing.T) {
	rv := newTestValidator(t)

	tests := []struct {
		name    string
		content string
		want    bool
		reason  string
	}{
		{
			name:    "short + abrupt",
			content: "hello,",
			want:    true,
			reason:  "both short and abrupt",
		},
		{
			name:    "short complete answer with period",
			content: "Done.",
			want:    false,
			reason:  "trailing punctuation stripped, 'done' exact-match",
		},
		{
			name:    "long complete answer",
			content: "The answer to your question is forty-two. This has more than ten words and ends with proper punctuation.",
			want:    false,
			reason:  "long and well-formed",
		},
		{
			name:    "long answer with ellipsis",
			content: "Here is a very long answer that has many words and explains everything in detail but then trails off...",
			want:    true,
			reason:  "trailing ellipsis overrides length",
		},
		{
			name:    "code block with abrupt ending",
			content: "```go\nfunc main() {\n  fmt.Println(\"hello\"",
			want:    true,
			reason:  "unclosed code block",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rv.IsIncomplete(tt.content)
			if got != tt.want {
				t.Errorf("IsIncomplete(%q) = %v, want %v (%s)", tt.content, got, tt.want, tt.reason)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Individual helper tests
// ---------------------------------------------------------------------------

func TestHasIncompletePatterns(t *testing.T) {
	rv := newTestValidator(t)

	tests := []struct {
		content string
		want    bool
	}{
		{"ends with ellipsis...", true},
		{"no ellipsis.", false},
		{"ellipsis in middle... but continues.", false},
		{"... starts with ellipsis", false},
	}

	for _, tt := range tests {
		got := rv.hasIncompletePatterns(tt.content)
		if got != tt.want {
			t.Errorf("hasIncompletePatterns(%q) = %v, want %v", tt.content, got, tt.want)
		}
	}
}

func TestHasAbruptEnding(t *testing.T) {
	rv := newTestValidator(t)

	tests := []struct {
		content string
		want    bool
	}{
		{"ends with comma,", true},
		{"ends with hyphen-", true},
		{"ends with period.", false},
		{"ends with letter", false},
		{"ends with digit 5", false},
		{"ends with slash/", false},
		{"```code```", false},
		{"http://example.com", false},
		{"", false},
		{"all good 😊", false}, // emoji should not be abrupt
	}

	for _, tt := range tests {
		got := rv.hasAbruptEnding(tt.content)
		if got != tt.want {
			t.Errorf("hasAbruptEnding(%q) = %v, want %v", tt.content, got, tt.want)
		}
	}
}

func TestIsUnusuallyShort(t *testing.T) {
	rv := newTestValidator(t)

	tests := []struct {
		content string
		want    bool
	}{
		{"hello", true},
		{"one two three four five six seven eight nine", true},
		{"one two three four five six seven eight nine ten", false},
		{"done", false},
		{"success", false},
		{"nothing", true},    // "no" is substring, not exact match
		{"downloaded", true}, // "done" is substring, not exact match
		{"not yet", true},    // "no" is substring, not exact match
	}

	for _, tt := range tests {
		got := rv.isUnusuallyShort(tt.content)
		if got != tt.want {
			t.Errorf("isUnusuallyShort(%q) = %v, want %v", tt.content, got, tt.want)
		}
	}
}

func TestIsCompleteShortAnswer(t *testing.T) {
	rv := newTestValidator(t)

	tests := []struct {
		content string
		want    bool
	}{
		{"done", true},
		{"DONE", true},
		{"completed", true},
		{"finished", true},
		{"yes", true},
		{"no", true},
		{"error: not found", true},
		{"success", true},
		{"failed", true},
		{"success: all good", true}, // prefix match
		{"warning: check logs", true}, // prefix match
		{"hello world", false},
		{"this is a response", false},
		{"nothing", false},    // "no" is substring, not exact
		{"know", false},       // "no" is substring, not exact
		{"downloaded", false}, // "done" is substring, not exact
		{"not yet", false},    // "no" is substring, not exact
		{"successful", false}, // "success" is substring, not exact
	}

	for _, tt := range tests {
		got := rv.isCompleteShortAnswer(tt.content)
		if got != tt.want {
			t.Errorf("isCompleteShortAnswer(%q) = %v, want %v", tt.content, got, tt.want)
		}
	}
}

func TestHasIncompleteCodeBlock(t *testing.T) {
	rv := newTestValidator(t)

	tests := []struct {
		content string
		want    bool
	}{
		{"```", true},
		{"```\ncode\n```", false},
		{"```\n```\n```", true},
		{"no backticks", false},
		{"```\n```\n```\n```", false},
	}

	for _, tt := range tests {
		got := rv.hasIncompleteCodeBlock(tt.content)
		if got != tt.want {
			t.Errorf("hasIncompleteCodeBlock(%q) = %v, want %v", tt.content, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestIsIncomplete_EdgeCases(t *testing.T) {
	rv := newTestValidator(t)

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"empty string", "", false},
		{"whitespace only", "   \n\t  ", true}, // whitespace is <10 words and not a complete answer
		{"single newline", "\n", true},
		{"only punctuation", "!", true}, // 1 word, not a complete short answer
		{"only question mark", "?", true}, // <10 words, not complete
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rv.IsIncomplete(tt.content)
			if got != tt.want {
				t.Errorf("IsIncomplete(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DebugLog integration
// ---------------------------------------------------------------------------

func TestResponseValidator_DebugLogNil(t *testing.T) {
	rv := NewResponseValidator(ResponseValidatorOptions{})
	// Should not panic with nil debugLog
	_ = rv.IsIncomplete("test...")
}

func TestResponseValidator_DebugLogCalled(t *testing.T) {
	var logCalled bool
	rv := NewResponseValidator(ResponseValidatorOptions{
		DebugLog: func(format string, args ...interface{}) {
			logCalled = true
		},
	})
	_ = rv.IsIncomplete("test...")
	if !logCalled {
		t.Error("expected debugLog to be called")
	}
}
