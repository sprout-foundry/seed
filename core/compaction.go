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
	oldMsgTailChars  = 400
	charsPerToken    = 4
	toolCallOverhead = 20
	msgOverhead      = 10
)

// CompactionResult holds the output of a compaction operation along with
// metadata about what strategy was used and how much was saved.
type CompactionResult struct {
	Messages     []Message
	Strategy     string // "none", "turn_drop", "checkpoint_drop", or "emergency"
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
// Otherwise tries dropping checkpoint summaries, then turns, then
// falls back to emergency truncation.
func Compact(messages []Message, tokenLimit int) CompactionResult {
	tokensBefore := roughTokens(messages)

	if len(messages) <= defaultMinMessages || tokensBefore <= tokenLimit {
		return CompactionResult{
			Messages:     messages,
			Strategy:     "none",
			TokensBefore: tokensBefore,
			TokensAfter:  tokensBefore,
		}
	}

	// Target: emergency truncation threshold — same as emergencyTruncate.
	targetTokens := int(float64(tokenLimit) * emergencyTargetFraction)
	// Phase 1: Try dropping checkpoint summaries to free space.
	dropped := dropOldestCheckpointSummaries(messages, defaultRecentToKeep, targetTokens)

	if roughTokens(dropped) <= targetTokens {
		// Dropping checkpoints alone brought us under the threshold.
		return CompactionResult{
			Messages:     dropped,
			Strategy:     "checkpoint_drop",
			TokensBefore: tokensBefore,
			TokensAfter:  roughTokens(dropped),
		}
	}

	// Phase 1.5: Try dropping oldest turns to free space.
	turnDropped := dropOldestTurns(dropped, defaultRecentToKeep, targetTokens)

	if roughTokens(turnDropped) <= targetTokens {
		// Dropping turns brought us under the threshold.
		return CompactionResult{
			Messages:     turnDropped,
			Strategy:     "turn_drop",
			TokensBefore: tokensBefore,
			TokensAfter:  roughTokens(turnDropped),
		}
	}

	// Phase 2: Emergency truncation on the already-dropped messages.
	result := emergencyTruncate(turnDropped, tokenLimit)
	return CompactionResult{
		Messages:     result,
		Strategy:     "emergency",
		TokensBefore: tokensBefore,
		TokensAfter:  roughTokens(result),
	}
}

// dropOldestTurns drops complete turns (user + assistant + tool chain) oldest
// first until the token count is at or below targetTokens, or there are no more
// removable turns outside the recent boundary.
//
// A turn is a user message followed by an assistant response and any associated
// tool results. If dropping complete turns isn't enough, it falls back to
// dropping individual messages oldest first.
//
// It uses recentToKeep as the recent boundary: messages within the
// last recentToKeep positions are never dropped.
//
// This function works on a copy and never mutates the original slice.
func dropOldestTurns(messages []Message, recentToKeep int, targetTokens int) []Message {
	// Work on a copy so we never mutate the original.
	msgs := make([]Message, len(messages))
	copy(msgs, messages)

	// Nothing to drop with too few messages.
	if len(msgs) <= defaultMinMessages {
		return msgs
	}

	// Repeatedly drop the oldest complete turn outside the recent zone.
	for roughTokens(msgs) > targetTokens && len(msgs) > defaultMinMessages {
		recentStart := len(msgs) - recentToKeep
		if recentStart < 1 {
			recentStart = 1
		}

		// Find the start of the oldest complete turn outside the recent boundary.
		turnStart, turnEnd, spanningEnd := findOldestCompleteTurn(msgs, recentStart)

		// No complete turn found — fall back to individual message dropping.
		if turnStart < 0 {
			// Try dropping individual non-system messages oldest first.
			dropIdx := findDropableMessage(msgs, recentStart, spanningEnd)
			if dropIdx < 0 {
				break
			}
			msgs = append(msgs[:dropIdx], msgs[dropIdx+1:]...)
			continue
		}

		// Remove the entire turn.
		msgs = append(msgs[:turnStart], msgs[turnEnd:]...)
	}

	return msgs
}

// findOldestCompleteTurn finds the oldest complete turn (user + assistant + tool
// results) that starts before recentStart. Returns (startIdx, endIdx) where
// endIdx is exclusive. Returns (-1, -1) if no complete turn is found.
//
// A complete turn requires at least a user message followed by an assistant
// message. Incomplete turns (lone user messages with no response) are skipped
// and handled by the individual message fallback in dropOldestTurns.
//
// If a turn starts before recentStart but extends past it, the turn cannot be
// dropped. In that case (-1, -1) is returned and spanningEnd is set to the
// end index of the spanning turn so the caller can protect those messages.
func findOldestCompleteTurn(msgs []Message, recentStart int) (startIdx, endIdx, spanningEnd int) {
	// Scan from index 1 (skip system message at 0) for the oldest user message.
	for i := 1; i < recentStart && i < len(msgs); i++ {
		if msgs[i].Role != "user" {
			continue
		}
		turnEnd := identifyTurnEnd(msgs, i)
		if turnEnd > i && turnEnd <= recentStart {
			return i, turnEnd, 0
		}
		if turnEnd > i && turnEnd > recentStart {
			// Turn spans the boundary — protect it. No later turn can be
			// fully outside the recent zone either, so stop searching.
			return -1, -1, turnEnd
		}
	}
	return -1, -1, 0
}

// identifyTurnEnd walks forward from a user message at startIdx and returns
// the index past the last message belonging to the same turn. A turn ends
// when we hit the next user message or the end of the slice.
//
// The turn structure is:
//
//	user → assistant (with optional tool calls) → [tool results → assistant]* → next user
func identifyTurnEnd(msgs []Message, startIdx int) int {
	if startIdx >= len(msgs) {
		return startIdx
	}

	pos := startIdx + 1
	for pos < len(msgs) {
		msg := msgs[pos]

		// A new user message starts a new turn.
		if msg.Role == "user" {
			break
		}

		// System messages in the middle are skipped (shouldn't happen normally).
		if msg.Role == "system" {
			pos++
			continue
		}

		// assistant message — advance past it
		pos++

		// If the assistant had tool calls, advance past matching tool results.
		// We look for tool messages that follow this assistant message.
		for pos < len(msgs) && msgs[pos].Role == "tool" {
			pos++
			// After tool results, there may be another assistant message
			// (the model continues after seeing tool results).
			if pos < len(msgs) && msgs[pos].Role == "assistant" {
				pos++
				// Check if this assistant also has tool calls.
			} else {
				break
			}
		}
	}

	return pos
}

// findDropableMessage finds the first non-system, non-recent message that can
// be dropped. Returns the index or -1 if none found.
// protectedEnd is the exclusive end index of a boundary-spanning turn that
// must not be touched; 0 means no protection needed.
func findDropableMessage(msgs []Message, recentStart int, protectedEnd int) int {
	for i := 1; i < recentStart && i < len(msgs); i++ {
		// Don't drop messages that belong to a boundary-spanning turn.
		if protectedEnd > 0 && i < protectedEnd {
			continue
		}
		if msgs[i].Role != "system" {
			return i
		}
	}
	return -1
}

// DropOldestTurns drops complete turns (user + assistant + tool chain) oldest
// first until the token count is at or below targetTokens.
//
// It uses defaultRecentToKeep as the recent boundary and works on a copy
// of the input slice. Falls back to individual message dropping when no
// complete turns remain.
func DropOldestTurns(messages []Message, targetTokens int) []Message {
	return dropOldestTurns(messages, defaultRecentToKeep, targetTokens)
}

// DropOldestCheckpointSummaries drops checkpoint summary messages (oldest first)
// until the token count is at or below targetTokens, or there are no more
// removable checkpoint messages outside the recent boundary.
//
// It uses defaultRecentToKeep as the recent boundary: messages within the
// last defaultRecentToKeep positions are never dropped.
//
// This function works on a copy and never mutates the original slice.
func DropOldestCheckpointSummaries(messages []Message, targetTokens int) []Message {
	return dropOldestCheckpointSummaries(messages, defaultRecentToKeep, targetTokens)
}

// dropOldestCheckpointSummaries is the unexported implementation that accepts
// a custom recentToKeep boundary.
func dropOldestCheckpointSummaries(messages []Message, recentToKeep int, targetTokens int) []Message {
	// Work on a copy so we never mutate the original.
	msgs := make([]Message, len(messages))
	copy(msgs, messages)

	// Nothing to drop with too few messages.
	if len(msgs) <= defaultMinMessages {
		return msgs
	}

	// Repeatedly drop the oldest checkpoint message outside the recent zone
	// until we're under targetTokens.
	for roughTokens(msgs) > targetTokens && len(msgs) > defaultMinMessages {
		dropIdx := -1
		recentStart := len(msgs) - recentToKeep
		if recentStart < 1 {
			recentStart = 1
		}

		// Find the oldest checkpoint message outside the recent boundary.
		for i := 0; i < recentStart && i < len(msgs); i++ {
			if msgs[i].Meta != nil && msgs[i].Meta[MetaKeyCheckpoint] == "true" {
				dropIdx = i
				break
			}
		}

		// No removable checkpoint message found.
		if dropIdx < 0 {
			break
		}

		// Remove the message at dropIdx.
		msgs = append(msgs[:dropIdx], msgs[dropIdx+1:]...)
	}

	return msgs
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
			trimmed[i].Content = truncateOldContentHeadTail(trimmed[i].Content)
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

// truncateOldContentHeadTail truncates old message content using head+tail
// preservation: keeps oldMsgHeadChars from the start and oldMsgTailChars from
// the end, with a truncation marker in between. This preserves both the
// beginning context and the ending context of old messages.
func truncateOldContentHeadTail(s string) string {
	return truncateHeadTail(s, oldMsgHeadChars, oldMsgTailChars)
}
