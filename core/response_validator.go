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
			unicode.Is(unicode.So, lastChar) { // Symbol, other (e.g., emoji)
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
	"i need to check",
	"i need to investigate",
	"i need to look",
	"i need to find",
	"i'll look at",
	"i'll check",
	"let me think",
	"let me check",
	"let me look",
	"let me search",
	"let me read",
	"let me find",
	"let me investigate",
	"let me explore",
	"one moment",
	"give me",
}

// tentativeWordThreshold is the word-count threshold above which responses
// are considered too substantive to be mere planning language.
const tentativeWordThreshold = 40

// LooksTruncated checks if a response appears structurally truncated.
// It is a subset of IsIncomplete that excludes the shortness heuristic,
// making it safe for use in the conversation continuation loop where short
// but complete answers (e.g., "Done.") should not trigger a retry.
//
// It returns true if any of these conditions are detected:
//
// - Trailing "..." (ellipsis at end)
// - Abrupt ending (ends with comma or hyphen)
// - Unclosed code blocks (odd number of ``` markers)
func (rv *ResponseValidator) LooksTruncated(content string) bool {
	if len(content) == 0 {
		return false
	}

	hasPatterns := rv.hasIncompletePatterns(content)
	hasAbrupt := rv.hasAbruptEnding(content)
	hasBadCode := rv.hasIncompleteCodeBlock(content)

	result := hasPatterns || hasAbrupt || hasBadCode

	rv.log("[validate] LooksTruncated: %v (patterns=%v, abrupt=%v, code=%v)",
		result, hasPatterns, hasAbrupt, hasBadCode)

	return result
}

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

// insufficientWordThreshold is the word-count ceiling below which a
// post-tool response is a candidate for the insufficiency check.
// Responses at or above this count are assumed substantive regardless
// of content. This is deliberately higher than tentativeWordThreshold
// (40) because a model can produce 40 words of pure meta-acknowledgement
// ("I've reviewed the files you mentioned. They appear to contain the
// code for the build runner. Let me know if you need anything else.")
// that is neither tentative planning language nor useful findings.
const insufficientWordThreshold = 60

// insufficientMetaPrefixes are language patterns that indicate the model
// is claiming to have done something ("reviewed", "checked", "looked at")
// without actually reporting what it found. These are the signatures of
// the bug: a researcher reads files, then responds with a brief
// acknowledgement that conveys zero findings. The list is intentionally
// narrow — simple confirmations ("Done.", "Got it.", "Yes.") and
// substantive findings (file paths, code blocks, error messages) are
// NOT matched and pass through unaffected.
var insufficientMetaPrefixes = []string{
	"i've reviewed",
	"i have reviewed",
	"i reviewed",
	"i've checked",
	"i have checked",
	"i checked",
	"i've looked at",
	"i have looked at",
	"i looked at",
	"i've read",
	"i have read",
	"i read the",
	"i've examined",
	"i have examined",
	"i examined",
}

// LooksInsufficientAfterToolCalls detects when the model returned "stop"
// with no tool calls immediately after receiving tool results, but the
// content is a brief meta-acknowledgement that claims action without
// delivering findings.
//
// This targets a specific bug pattern: a researcher subagent reads
// files, then responds "I've reviewed the files." — which is neither
// blank, nor tentative planning language ("Let me..."), nor truncated,
// yet conveys no findings. Simple confirmations ("Done.", "Got it.")
// and substantive responses (file paths, code, errors) pass through
// unaffected.
//
// Returns true only when ALL of these hold:
//   - Content is under insufficientWordThreshold (60) words.
//   - Content starts with one of the insufficientMetaPrefixes (case-insensitive).
//   - Content contains none of the insufficientSignals (substance markers).
func (rv *ResponseValidator) LooksInsufficientAfterToolCalls(content string) bool {
	if len(content) == 0 {
		// Empty content is handled by isBlankIteration, not here.
		return false
	}

	// Word count gate: long responses are substantive regardless of prefix.
	wordCount := len(strings.Fields(content))
	if wordCount >= insufficientWordThreshold {
		rv.log("[validate] LooksInsufficientAfterToolCalls: false (too long: %d words)",
			wordCount)
		return false
	}

	// Prefix gate: only trigger on meta-acknowledgement language. A response
	// like "Done." or "Build passed." doesn't match and is left alone.
	lower := strings.ToLower(strings.TrimSpace(content))
	matchedPrefix := ""
	for _, prefix := range insufficientMetaPrefixes {
		if strings.HasPrefix(lower, prefix) {
			matchedPrefix = prefix
			break
		}
	}
	if matchedPrefix == "" {
		return false
	}

	// Signal gate: even if the prefix matches, presence of any substance
	// marker (file path, code block, error message) exempts the response.
	for _, signal := range insufficientSignals {
		if strings.Contains(lower, signal) {
			rv.log("[validate] LooksInsufficientAfterToolCalls: false (matched prefix %q but found signal %q)",
				matchedPrefix, signal)
			return false
		}
	}

	rv.log("[validate] LooksInsufficientAfterToolCalls: true (prefix %q, %d words, no signals)",
		matchedPrefix, wordCount)
	return true
}

// insufficientSignals are substrings that, when present, indicate the
// response contains substantive findings rather than a bare
// acknowledgement. The check is case-insensitive. Presence of ANY signal
// exempts the response from the insufficiency check — even a short
// response that quotes a file path or an error message is useful.
var insufficientSignals = []string{
	"\n-",          // markdown list item
	"\n*",          // markdown list item (alt)
	"\n1.",         // numbered list
	"\n2.",         // numbered list
	"\n\u2022",     // unicode bullet
	"\n```",        // fenced code block
	".go:",         // Go file:line reference
	".ts:",         // TypeScript file:line
	".py:",         // Python file:line
	".rs:",         // Rust file:line
	"line ",        // "on line 42"
	"func ",        // Go function reference
	"err != nil",   // Go error-handling pattern
	"panic(",       // Go panic
	"error:",       // generic error report
	"stack trace",  // error/panic reference
	"undefined",    // compiler/linter finding
	"imported but", // unused-import finding
	"not declared", // compiler finding
	"unexpected",   // test failure language
	"expected",     // test assertion language
	"failed:",      // test/build failure
	"passed",       // test result
}
