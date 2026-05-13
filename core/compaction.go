package core

import (
	"unicode/utf8"
)

// Compaction constants.
const (
	defaultRecentToKeep = 24
	defaultMinMessages  = 5

	emergencyTargetFraction = 0.85
	toolResultMaxChars      = 1500
	toolResultHeadChars     = 750
	toolResultTailChars     = 500

	oldMsgMaxChars   = 1200
	oldMsgHeadChars  = 600
	charsPerToken    = 4
	toolCallOverhead = 20
	msgOverhead      = 10
)

// CompactionResult holds the output of a compaction operation along with
// metadata about what strategy was used and how much was saved.
type CompactionResult struct {
	Messages     []Message
	Strategy     string // "none" or "emergency"
	TokensBefore int
	TokensAfter  int
}

// TokensSaved returns the estimated tokens saved by compaction.
func (r CompactionResult) TokensSaved() int {
	if r.TokensBefore > r.TokensAfter {
		return r.TokensBefore - r.TokensAfter
	}
	return 0
}

// MessageCountDelta returns how many messages were removed.
func (r CompactionResult) MessageCountDelta(before int) int {
	return before - len(r.Messages)
}

// Compact reduces messages to fit within the token limit.
// If under the limit or too few messages, returns as-is.
// Otherwise applies emergency truncation.
func Compact(messages []Message, tokenLimit int) CompactionResult {
	tokensBefore := roughTokens(messages)

	if len(messages) <= defaultMinMessages {
		return CompactionResult{
			Messages:     messages,
			Strategy:     "none",
			TokensBefore: tokensBefore,
			TokensAfter:  tokensBefore,
		}
	}

	if tokensBefore <= tokenLimit {
		return CompactionResult{
			Messages:     messages,
			Strategy:     "none",
			TokensBefore: tokensBefore,
			TokensAfter:  tokensBefore,
		}
	}

	// Emergency truncation — aggressively trim content
	result := emergencyTruncate(messages, tokenLimit)
	return CompactionResult{
		Messages:     result,
		Strategy:     "emergency",
		TokensBefore: tokensBefore,
		TokensAfter:  roughTokens(result),
	}
}

// emergencyTruncate aggressively trims message content to fit the limit.
func emergencyTruncate(messages []Message, tokenLimit int) []Message {
	targetTokens := int(float64(tokenLimit) * emergencyTargetFraction)
	currentTokens := roughTokens(messages)

	if currentTokens <= targetTokens {
		return messages
	}

	// Work on a copy
	trimmed := make([]Message, len(messages))
	copy(trimmed, messages)

	// Phase 1: Trim tool results to max 1500 chars each
	for i := range trimmed {
		if trimmed[i].Role == "tool" && utf8.RuneCountInString(trimmed[i].Content) > toolResultMaxChars {
			trimmed[i].Content = truncateHeadTail(trimmed[i].Content, toolResultHeadChars, toolResultTailChars)
		}
	}

	currentTokens = roughTokens(trimmed)
	if currentTokens <= targetTokens {
		return trimmed
	}

	// Phase 2: Trim older user/assistant messages
	recentStart := len(trimmed) - defaultRecentToKeep
	if recentStart < 1 {
		recentStart = 1
	}

	for i := range trimmed {
		if i >= recentStart || trimmed[i].Role == "system" || trimmed[i].Content == "" {
			continue
		}
		if utf8.RuneCountInString(trimmed[i].Content) > oldMsgMaxChars {
			trimmed[i].Content = truncateHead(trimmed[i].Content, oldMsgHeadChars)
		}
	}

	currentTokens = roughTokens(trimmed)
	if currentTokens <= targetTokens {
		return trimmed
	}

	// Phase 3: Drop oldest non-system, non-recent messages until under limit
	for len(trimmed) > defaultMinMessages+1 && roughTokens(trimmed) > targetTokens {
		// Find first non-system message that's not in recent section
		dropIdx := 1
		if dropIdx >= recentStart {
			break
		}
		trimmed = append(trimmed[:dropIdx], trimmed[dropIdx+1:]...)
		// Adjust recentStart
		recentStart--
	}

	return trimmed
}

// roughTokens gives a rough token estimate (4 chars ≈ 1 token).
func roughTokens(messages []Message) int {
	total := 0
	for _, msg := range messages {
		total += utf8.RuneCountInString(msg.Content) / charsPerToken
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			total += len(msg.ToolCalls) * toolCallOverhead
			for _, tc := range msg.ToolCalls {
				total += utf8.RuneCountInString(tc.Function.Arguments) / charsPerToken
			}
		}
		total += msgOverhead // per-message overhead
	}
	return total
}

// truncateHeadTail keeps headRunes from start and tailRunes from end.
func truncateHeadTail(s string, headRunes, tailRunes int) string {
	r := []rune(s)
	if len(r) <= headRunes+tailRunes {
		return s
	}
	return string(r[:headRunes]) + "\n... [truncated] ...\n" + string(r[len(r)-tailRunes:])
}

// truncateHead keeps only headRunes from the start.
func truncateHead(s string, headRunes int) string {
	r := []rune(s)
	if len(r) <= headRunes {
		return s
	}
	return string(r[:headRunes]) + "\n... [truncated] ..."
}
