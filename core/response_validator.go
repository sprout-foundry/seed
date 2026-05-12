package core

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// minWordThreshold is the minimum number of words below which a response is
// considered unusually short (and potentially truncated).
const minWordThreshold = 10

// shortCompleteWords are exact-match words that indicate a complete short answer.
var shortCompleteWords = []string{
	"done",
	"completed",
	"finished",
	"yes",
	"no",
	"success",
	"failed",
}

// shortCompletePrefixes are prefix patterns that indicate a complete short answer.
var shortCompletePrefixes = []string{
	"error:",
	"success:",
	"warning:",
}

// ResponseValidator inspects LLM response content for quality issues like
// truncation, tentativeness, or other patterns that suggest the response
// should not be finalized yet.
//
// It has zero dependencies on Agent or concrete types — all input is passed
// explicitly and the DebugLog callback is optional.
type ResponseValidator struct {
	debugLog func(format string, args ...interface{})
}

// ResponseValidatorOptions configures a ResponseValidator.
type ResponseValidatorOptions struct {
	// DebugLog is an optional callback for debug output. When nil,
	// debug logging is disabled.
	DebugLog func(format string, args ...interface{})
}

// NewResponseValidator creates a new ResponseValidator with the given options.
func NewResponseValidator(opts ResponseValidatorOptions) *ResponseValidator {
	return &ResponseValidator{
		debugLog: opts.DebugLog,
	}
}

// log is a convenience wrapper that skips output when debugLog is nil.
func (rv *ResponseValidator) log(format string, args ...interface{}) {
	if rv.debugLog != nil {
		rv.debugLog(format, args...)
	}
}

// IsIncomplete checks if a response appears to be incomplete or truncated.
// It returns true if any of these conditions are detected:
//
// - Trailing "..." (ellipsis at end)
// - Abrupt ending (ends with comma, hyphen, or no punctuation on non-code/URL text)
// - Unusually short (<10 words and not a known complete-short answer)
// - Unclosed code blocks (odd number of ``` markers)
func (rv *ResponseValidator) IsIncomplete(content string) bool {
	if len(content) == 0 {
		return false
	}

	hasPatterns := rv.hasIncompletePatterns(content)
	hasAbrupt := rv.hasAbruptEnding(content)
	isShort := rv.isUnusuallyShort(content)
	hasBadCode := rv.hasIncompleteCodeBlock(content)

	result := hasPatterns || hasAbrupt || isShort || hasBadCode

	rv.log("[validate] IsIncomplete: %v (patterns=%v, abrupt=%v, short=%v, code=%v)",
		result, hasPatterns, hasAbrupt, isShort, hasBadCode)

	return result
}

// hasIncompletePatterns checks for patterns indicating an incomplete response,
// such as a trailing ellipsis.
func (rv *ResponseValidator) hasIncompletePatterns(content string) bool {
	trimmed := strings.TrimSpace(content)
	if strings.HasSuffix(trimmed, "...") {
		return true
	}
	return false
}

// hasAbruptEnding checks if the response ends without proper sentence termination.
func (rv *ResponseValidator) hasAbruptEnding(content string) bool {
	trimmed := strings.TrimSpace(content)
	if len(trimmed) == 0 {
		return false
	}

	// Get the last rune (not byte) to handle multi-byte characters like emoji.
	lastChar, _ := utf8.DecodeLastRuneInString(trimmed)
	if lastChar == utf8.RuneError {
		return false
	}

	// Non-punctuation endings — these may be valid (URLs, identifiers, code).
	if !unicode.IsPunct(lastChar) {
		// Letters, digits, and symbols (emoji, etc.) can be valid endings.
		if unicode.IsLetter(lastChar) ||
			unicode.IsDigit(lastChar) ||
			unicode.Is(unicode.Sk, lastChar) || // Symbol, modifier (e.g., currency)
			unicode.Is(unicode.So, lastChar) {   // Symbol, other (e.g., emoji)
			return false
		}
		// Technical content with code blocks or URLs is trusted.
		if strings.Contains(content, "```") || strings.Contains(content, "http") {
			return false
		}
		// Otherwise, no punctuation at all suggests truncation.
		return true
	}

	// Ends with punctuation — only comma and hyphen are problematic.
	// (Forward slash, backslash, semicolon, colon, etc. are valid endings.)
	if lastChar == ',' || lastChar == '-' {
		return true
	}

	return false
}

// isUnusuallyShort checks if the response is too short to be a complete answer.
func (rv *ResponseValidator) isUnusuallyShort(content string) bool {
	wordCount := len(strings.Fields(content))
	if wordCount < minWordThreshold && !rv.isCompleteShortAnswer(content) {
		return true
	}
	return false
}

// isCompleteShortAnswer checks if a short response is actually a complete answer.
// It uses exact word matching (not substring) to avoid false positives like
// "nothing" matching "no" or "downloaded" matching "done". Trailing punctuation
// is stripped before comparison so "Done." matches "done".
func (rv *ResponseValidator) isCompleteShortAnswer(content string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(content))
	// Strip trailing punctuation (period, exclamation, etc.) for exact matching.
	trimmed = strings.TrimRight(trimmed, ".!?.,;:'\"")

	// Exact word matches
	for _, w := range shortCompleteWords {
		if trimmed == w {
			return true
		}
	}

	// Prefix patterns (e.g., "error: not found")
	for _, p := range shortCompletePrefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}

	return false
}

// hasIncompleteCodeBlock checks for unclosed code blocks by counting ``` markers.
//
// Note: This uses a simple count of ``` sequences and does not handle 4-backtick
// fenced blocks (````). For most LLM output this is sufficient, as 3-backtick
// fences are the standard.
func (rv *ResponseValidator) hasIncompleteCodeBlock(content string) bool {
	codeBlockCount := strings.Count(content, "```")
	return codeBlockCount%2 != 0
}

// tentativePrefixes are planning-language prefixes that suggest the LLM is
// about to perform an action rather than providing a substantive answer.
// Ordered so that longer prefixes which are supersets of shorter ones are
// checked first, preventing shorter substrings from incorrectly matching
// (e.g., "i'll start by" must match before "i'll").
var tentativePrefixes = []string{
	"first, let me",
	"first i'll",
	"i'll need to",
	"i'll start by",
	"i will start by",
	"i'm going to",
	"im going to",
	"i need to",
	"i will",
	"i'll",
	"let me think",
	"one moment",
	"give me",
	"let me",
}

// tentativeWordThreshold is the word-count threshold above which responses
// are considered too substantive to be mere planning language.
const tentativeWordThreshold = 40

// LooksLikeTentativePostToolResponse detects when the LLM has run tools but
// instead of giving a real response, it's just planning what to do next.
//
// These "tentative" responses should trigger another loop iteration. A response
// is considered tentative when:
//
//   - It is under 40 words (longer responses are considered substantive even if
//     they start with planning language)
//   - It starts with a planning prefix (case-insensitive), such as "Let me...",
//     "I'll...", "I need to...", "I'm going to...", etc.
func (rv *ResponseValidator) LooksLikeTentativePostToolResponse(content string) bool {
	if len(content) == 0 {
		return false
	}

	// Long responses are considered substantive regardless of prefix.
	wordCount := len(strings.Fields(content))
	if wordCount >= tentativeWordThreshold {
		rv.log("[validate] LooksLikeTentativePostToolResponse: false (too long: %d words)",
			wordCount)
		return false
	}

	// Lowercase and trim for prefix matching.
	trimmed := strings.ToLower(strings.TrimSpace(content))

	for _, prefix := range tentativePrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			rv.log("[validate] LooksLikeTentativePostToolResponse: true (matched prefix %q)", prefix)
			return true
		}
	}

	rv.log("[validate] LooksLikeTentativePostToolResponse: false (no prefix match)")
	return false
}
