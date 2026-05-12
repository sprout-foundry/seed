package core

import (
	"strings"
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
			name:    "open code block only",
			content: "Here is the complete code you requested, it does the thing:\n```\nfunc foo() {}",
			want:    true, // unclosed code block
		},
		{
			name:    "closed code block",
			content: "Here is the complete code you requested, it does the thing:\n```\nfunc foo() {}\n```",
			want:    false, // 11 words, closed block
		},
		{
			name:    "two open code blocks",
			content: "Here is some content that has enough words to pass the short check:\n```\nfoo\n```\n```\nbar",
			want:    true, // unclosed code block (3 ``` markers)
		},
		{
			name:    "two closed code blocks",
			content: "Here is some content that has enough words to pass the short check:\n```\nfoo\n```\n```\nbar\n```",
			want:    false, // 13 words, 4 ``` markers (even)
		},
		{
			name:    "three backticks in text",
			content: "Here is some content that has enough words to pass the short check, and mentions Use ``` for inline code",
			want:    true, // 3 ``` markers (odd)
		},
		{
			name:    "no code blocks",
			content: "Just plain text with enough words here to pass the short threshold comfortably.",
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
		{"success: all good", true},   // prefix match
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
		{"only punctuation", "!", true},   // 1 word, not a complete short answer
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

// ---------------------------------------------------------------------------
// LooksLikeTentativePostToolResponse
// ---------------------------------------------------------------------------

func TestLooksLikeTentativePostToolResponse_AllPrefixes(t *testing.T) {
	rv := newTestValidator(t)

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		// "let me" variants
		{"lowercase let me", "let me check the file", true},
		{"uppercase Let me", "Let me check the file", true},
		{"titlecase Let Me", "Let Me check the file", true},
		{"let me with ellipsis", "let me check the file...", true},
		{"let me with trailing space", "let me check the file   ", true},

		// "i'll" variants
		{"lowercase i'll", "i'll look into that", true},
		{"uppercase I'll", "I'll look into that", true},
		{"I'll with ellipsis", "I'll look into that...", true},
		{"i'll with period", "I'll look into that.", true},

		// "i will" variant
		{"lowercase i will", "i will do that", true},
		{"uppercase I will", "I will do that", true},

		// "i need to" variant
		{"lowercase i need to", "i need to run a test", true},
		{"uppercase I need to", "I need to run a test", true},

		// "i'm going to" variant
		{"lowercase i'm going to", "i'm going to search for it", true},
		{"uppercase I'm going to", "I'm going to search for it", true},

		// "im going to" variant (no apostrophe)
		{"lowercase im going to", "im going to check", true},
		{"uppercase Im going to", "Im going to check", true},

		// "i'll start by" variant
		{"lowercase i'll start by", "i'll start by reading the file", true},
		{"uppercase I'll start by", "I'll start by reading the file", true},

		// "i will start by" variant
		{"lowercase i will start by", "i will start by checking", true},
		{"uppercase I will start by", "I will start by checking", true},

		// "first, let me" variant
		{"lowercase first, let me", "first, let me check the file", true},
		{"uppercase First, let me", "First, let me check the file", true},

		// "first i'll" variant
		{"lowercase first i'll", "first i'll start checking", true},
		{"uppercase First i'll", "First i'll start checking", true},

		// "one moment" variant
		{"lowercase one moment", "one moment, let me check", true},
		{"uppercase One moment", "One moment, let me check", true},

		// "give me" variant
		{"lowercase give me", "give me a second to check", true},
		{"uppercase Give me", "Give me a second to check", true},

		// "i'll need to" variant
		{"lowercase i'll need to", "i'll need to look at this", true},
		{"uppercase I'll need to", "I'll need to look at this", true},

		// "let me think" variant
		{"lowercase let me think", "let me think about this", true},
		{"uppercase Let me think", "Let me think about this", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rv.LooksLikeTentativePostToolResponse(tt.content)
			if got != tt.want {
				t.Errorf("LooksLikeTentativePostToolResponse(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestLooksLikeTentativePostToolResponse_WordThreshold(t *testing.T) {
	rv := newTestValidator(t)

	// "let me " = 2 words, "word " * N = N words, "end" = 1 word → total = N + 3
	// For 39 words: N = 36, for 40 words: N = 37

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "39 words with prefix",
			content: "let me " + strings.Repeat("word ", 36) + "end", // 2 + 36 + 1 = 39
			want:    true,
		},
		{
			name:    "40 words with prefix",
			content: "let me " + strings.Repeat("word ", 37) + "end", // 2 + 37 + 1 = 40
			want:    false,
		},
		// Short with prefix
		{
			name:    "5 words with prefix",
			content: "let me check the file now",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rv.LooksLikeTentativePostToolResponse(tt.content)
			if got != tt.want {
				t.Errorf("LooksLikeTentativePostToolResponse(%q) = %v, want %v (word count: %d)", tt.content, got, tt.want, len(strings.Fields(tt.content)))
			}
		})
	}
}

func TestLooksLikeTentativePostToolResponse_SubstantiveLongResponses(t *testing.T) {
	rv := newTestValidator(t)

	// Build a 40+ word substantive response starting with a planning prefix.
	substantiveLong1 := "Let me explain what happened here. We ran the tests and they all passed today. " +
		"The code is functioning correctly and there are no errors to report. " +
		"Everything looks good and we can proceed with the deployment without issues." // 39 words... need 40+

	// Let me verify: Let(1) me(2) explain(3) what(4) happened(5) here(6) We(7) ran(8) the(9) tests(10)
	// and(11) they(12) all(13) passed(14) today(15) The(16) code(17) is(18) functioning(19) correctly(20)
	// and(21) there(22) are(23) no(24) errors(25) to(26) report(27) Everything(28) looks(29) good(30)
	// and(31) we(32) can(33) proceed(34) with(35) the(36) deployment(37) without(38) issues(39) = 39 words
	// Need one more word to be 40.
	substantiveLong1 = strings.TrimSpace(substantiveLong1) + " now" // 40 words

	substantiveLong2 := "I will summarize the key findings from the analysis. The data shows " +
		"significant improvements in performance and reliability across all measured metrics today " +
		"for the entire team to review and act upon accordingly going forward with confidence " +
		"in the results and outcomes for all stakeholders involved in this project." // 49 words

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "long response starting with let me (40 words)",
			content: substantiveLong1,
			want:    false,
		},
		{
			name:    "long response starting with I will (40+ words)",
			content: substantiveLong2,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wordCount := len(strings.Fields(tt.content))
			got := rv.LooksLikeTentativePostToolResponse(tt.content)
			if got != tt.want {
				t.Errorf("LooksLikeTentativePostToolResponse(%q) = %v, want %v (word count: %d)", tt.content, got, tt.want, wordCount)
			}
		})
	}
}

func TestLooksLikeTentativePostToolResponse_EmptyAndWhitespace(t *testing.T) {
	rv := newTestValidator(t)

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"empty string", "", false},
		{"whitespace only", "   ", false},
		{"newlines only", "\n\n", false},
		{"tabs only", "\t\t", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rv.LooksLikeTentativePostToolResponse(tt.content)
			if got != tt.want {
				t.Errorf("LooksLikeTentativePostToolResponse(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestLooksLikeTentativePostToolResponse_NoPrefixMatch(t *testing.T) {
	rv := newTestValidator(t)

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"plain answer", "Here is the result you requested.", false},
		{"answer starting with I", "I found the file you were looking for.", false},
		{"answer starting with let", "Let's look at the code together.", false},
		{"answer starting with give", "Give you the answer: yes, it works.", false},
		{"answer starting with one", "One of the issues was a null pointer.", false},
		{"answer starting with first", "First of all, the tests passed.", false},
		{"answer starting with think", "Think carefully about the requirements.", false},
		{"answer starting with will", "Will this work? Yes, it should.", false},
		{"answer starting with need", "Need more information before proceeding.", false},
		{"random text", "The quick brown fox jumps over the lazy dog.", false},
		{"answer with planning words in middle", "The file was found. Let me check its permissions.", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rv.LooksLikeTentativePostToolResponse(tt.content)
			if got != tt.want {
				t.Errorf("LooksLikeTentativePostToolResponse(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestLooksLikeTentativePostToolResponse_LeadingWhitespace(t *testing.T) {
	rv := newTestValidator(t)

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"leading space", " let me check", true},
		{"leading newline and space", "\n\t let me check", true},
		{"leading tabs", "\t\tlet me check", true},
		{"leading spaces on empty", "   ", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rv.LooksLikeTentativePostToolResponse(tt.content)
			if got != tt.want {
				t.Errorf("LooksLikeTentativePostToolResponse(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestLooksLikeTentativePostToolResponse_DebugLog(t *testing.T) {
	rv := NewResponseValidator(ResponseValidatorOptions{})
	// Should not panic with nil debugLog
	_ = rv.LooksLikeTentativePostToolResponse("let me check")

	var logCalled bool
	rv2 := NewResponseValidator(ResponseValidatorOptions{
		DebugLog: func(format string, args ...interface{}) {
			logCalled = true
		},
	})
	_ = rv2.LooksLikeTentativePostToolResponse("let me check")
	if !logCalled {
		t.Error("expected debugLog to be called")
	}
}
