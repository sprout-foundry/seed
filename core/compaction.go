package core

import (
	"unicode/utf8"
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

// Compactor reduces a message list to fit within a context window.
// It applies emergency truncation when the message list exceeds limits.
//
// This is a concrete type now. When different strategies are needed,
// extract an interface — the loop code doesn't change.
type Compactor struct {
	// recentToKeep is the number of recent messages to always preserve.
	recentToKeep int
	// minMessages is the minimum messages to always keep.
	minMessages int
}

// NewCompactor creates a compactor with default settings.
func NewCompactor() *Compactor {
	return &Compactor{
		recentToKeep: 24,
		minMessages:  5,
	}
}

// Compact reduces messages to fit within the token limit.
// If under the limit or too few messages, returns as-is.
// Otherwise applies emergency truncation.
func (c *Compactor) Compact(messages []Message, tokenLimit int) CompactionResult {
	tokensBefore := c.roughTokens(messages)

	if len(messages) <= c.minMessages {
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
	result := c.emergencyTruncate(messages, tokenLimit)
	return CompactionResult{
		Messages:     result,
		Strategy:     "emergency",
		TokensBefore: tokensBefore,
		TokensAfter:  c.roughTokens(result),
	}
}

// emergencyTruncate aggressively trims message content to fit the limit.
func (c *Compactor) emergencyTruncate(messages []Message, tokenLimit int) []Message {
	targetTokens := int(float64(tokenLimit) * 0.85)
	currentTokens := c.roughTokens(messages)

	if currentTokens <= targetTokens {
		return messages
	}

	// Work on a copy
	trimmed := make([]Message, len(messages))
	copy(trimmed, messages)

	// Phase 1: Trim tool results to max 500 tokens (~1500 chars each)
	for i := range trimmed {
		if trimmed[i].Role == "tool" && utf8.RuneCountInString(trimmed[i].Content) > 1500 {
			trimmed[i].Content = c.truncateHeadTail(trimmed[i].Content, 750, 500)
		}
	}

	currentTokens = c.roughTokens(trimmed)
	if currentTokens <= targetTokens {
		return trimmed
	}

	// Phase 2: Trim older user/assistant messages
	recentStart := len(trimmed) - c.recentToKeep
	if recentStart < 1 {
		recentStart = 1
	}

	for i := range trimmed {
		if i >= recentStart || trimmed[i].Role == "system" || trimmed[i].Content == "" {
			continue
		}
		if utf8.RuneCountInString(trimmed[i].Content) > 1200 {
			trimmed[i].Content = c.truncateHead(trimmed[i].Content, 600)
		}
	}

	currentTokens = c.roughTokens(trimmed)
	if currentTokens <= targetTokens {
		return trimmed
	}

	// Phase 3: Drop oldest non-system, non-recent messages until under limit
	for len(trimmed) > c.minMessages+1 && c.roughTokens(trimmed) > targetTokens {
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
func (c *Compactor) roughTokens(messages []Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content) / 4
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			total += len(msg.ToolCalls) * 20
			for _, tc := range msg.ToolCalls {
				total += len(tc.Function.Arguments) / 4
			}
		}
		total += 10 // per-message overhead
	}
	return total
}

// truncateHeadTail keeps headRunes from start and tailRunes from end.
func (c *Compactor) truncateHeadTail(s string, headRunes, tailRunes int) string {
	r := []rune(s)
	if len(r) <= headRunes+tailRunes {
		return s
	}
	return string(r[:headRunes]) + "\n... [truncated] ...\n" + string(r[len(r)-tailRunes:])
}

// truncateHead keeps only headRunes from the start.
func (c *Compactor) truncateHead(s string, headRunes int) string {
	r := []rune(s)
	if len(r) <= headRunes {
		return s
	}
	return string(r[:headRunes]) + "\n... [truncated] ..."
}
