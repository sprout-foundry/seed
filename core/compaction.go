package core

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// CompactionResult holds the output of a compaction operation along with
// metadata about what strategy was used and how much was saved.
type CompactionResult struct {
	Messages     []Message
	Strategy     string // "none", "checkpoint", "structural", "emergency"
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
// It implements the strategy from sprout: checkpoint compaction,
// structural compaction, and emergency truncation.
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
// It applies strategies in order: checkpoint compaction,
// structural compaction, emergency truncation.
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

	// Phase 1: Checkpoint compaction — replace completed turns with summaries
	result := c.checkpointCompact(messages, tokenLimit)
	if c.roughTokens(result) <= tokenLimit {
		return CompactionResult{
			Messages:     result,
			Strategy:     "checkpoint",
			TokensBefore: tokensBefore,
			TokensAfter:  c.roughTokens(result),
		}
	}

	// Phase 2: Structural compaction — summarize middle messages
	result = c.structuralCompact(result, tokenLimit)
	if c.roughTokens(result) <= tokenLimit {
		return CompactionResult{
			Messages:     result,
			Strategy:     "structural",
			TokensBefore: tokensBefore,
			TokensAfter:  c.roughTokens(result),
		}
	}

	// Phase 3: Emergency truncation — aggressively trim content
	result = c.emergencyTruncate(result, tokenLimit)
	return CompactionResult{
		Messages:     result,
		Strategy:     "emergency",
		TokensBefore: tokensBefore,
		TokensAfter:  c.roughTokens(result),
	}
}

// checkpointCompact replaces older completed user-assistant turn pairs
// with a single summary message, working backwards from the oldest.
func (c *Compactor) checkpointCompact(messages []Message, tokenLimit int) []Message {
	if c.roughTokens(messages) <= tokenLimit {
		return messages
	}

	// Protect: system message + recent messages
	protectedStart := len(messages) - c.recentToKeep
	if protectedStart < 1 {
		protectedStart = 1
	}

	result := make([]Message, 0, len(messages))

	// Keep system message
	if len(messages) > 0 && messages[0].Role == "system" {
		result = append(result, messages[0])
	}

	// Process middle section (between system and recent)
	middleStart := 0
	if len(messages) > 0 && messages[0].Role == "system" {
		middleStart = 1
	}
	middleEnd := protectedStart
	if middleEnd > len(messages) {
		middleEnd = len(messages)
	}

	// Group middle messages into turns and compact
	middle := messages[middleStart:middleEnd]
	compacted := c.compactTurns(middle)
	result = append(result, compacted...)

	// Keep recent messages intact
	result = append(result, messages[protectedStart:]...)

	return result
}

// compactTurns processes a slice of messages, replacing completed turns
// with summary messages.
func (c *Compactor) compactTurns(messages []Message) []Message {
	if len(messages) == 0 {
		return messages
	}

	result := make([]Message, 0, len(messages))
	i := 0

	for i < len(messages) {
		// Look for a complete turn: user message followed by assistant (optionally with tool calls/results)
		if messages[i].Role != "user" {
			result = append(result, messages[i])
			i++
			continue
		}

		// Find the end of this turn
		turnEnd := i + 1

		// Skip past assistant + tool chain
		for turnEnd < len(messages) {
			msg := messages[turnEnd]
			if msg.Role == "assistant" {
				turnEnd++
				// Skip tool results that belong to this assistant
				for turnEnd < len(messages) && messages[turnEnd].Role == "tool" {
					turnEnd++
				}
			} else if msg.Role == "tool" {
				// Orphaned tool result, include as-is
				turnEnd++
			} else {
				break
			}
		}

		turn := messages[i:turnEnd]
		if len(turn) <= 1 {
			// No assistant response, keep as-is
			result = append(result, turn...)
		} else {
			// Compact the turn into a summary
			summary := c.summarizeTurn(turn)
			result = append(result, summary...)
		}

		i = turnEnd
	}

	return result
}

// summarizeTurn creates a compact representation of a completed turn.
func (c *Compactor) summarizeTurn(turn []Message) []Message {
	var userContent, assistantContent strings.Builder
	var toolNames []string

	for _, msg := range turn {
		switch msg.Role {
		case "user":
			userContent.WriteString(msg.Content)
		case "assistant":
			assistantContent.WriteString(msg.Content)
			for _, tc := range msg.ToolCalls {
				toolNames = append(toolNames, tc.Function.Name)
			}
		case "tool":
			// Tool results are summarized by the tool name, not content
		}
	}

	// Build summary message
	var summary strings.Builder
	summary.WriteString("[Turn summary: ")
	if userContent.Len() > 0 {
		userText := strings.TrimSpace(userContent.String())
		if len(userText) > 120 {
			userText = userText[:117] + "..."
		}
		summary.WriteString("user: " + userText)
	}
	if assistantContent.Len() > 0 {
		asstText := strings.TrimSpace(assistantContent.String())
		if len(asstText) > 120 {
			asstText = asstText[:117] + "..."
		}
		if summary.Len() > len("[Turn summary: ") {
			summary.WriteString("; ")
		}
		summary.WriteString("assistant: " + asstText)
	}
	if len(toolNames) > 0 {
		// Deduplicate tool names
		seen := make(map[string]bool)
		var unique []string
		for _, n := range toolNames {
			if !seen[n] {
				seen[n] = true
				unique = append(unique, n)
			}
		}
		if summary.Len() > len("[Turn summary: ") {
			summary.WriteString("; ")
		}
		summary.WriteString("tools: " + strings.Join(unique, ", "))
	}
	summary.WriteString("]")

	return []Message{
		{Role: "user", Content: summary.String()},
	}
}

// structuralCompact summarizes the middle section of messages when
// checkpoint compaction wasn't enough.
func (c *Compactor) structuralCompact(messages []Message, tokenLimit int) []Message {
	if len(messages) <= c.recentToKeep+2 {
		return messages
	}

	// Identify sections: system, middle, recent
	systemEnd := 0
	if len(messages) > 0 && messages[0].Role == "system" {
		systemEnd = 1
	}
	recentStart := len(messages) - c.recentToKeep
	if recentStart < systemEnd {
		recentStart = systemEnd
	}

	// If middle section is small, just truncate tool results
	middle := messages[systemEnd:recentStart]
	if len(middle) < 6 {
		return c.trimToolResults(messages, tokenLimit)
	}

	// Summarize the middle section
	var summary strings.Builder
	summary.WriteString("[Earlier conversation summary]\n")

	// Count key metrics from middle section
	userCount := 0
	assistantCount := 0
	toolCount := 0
	var toolNames []string
	errorCount := 0

	for _, msg := range middle {
		switch msg.Role {
		case "user":
			userCount++
			if strings.Contains(strings.ToLower(msg.Content), "error") {
				errorCount++
			}
		case "assistant":
			assistantCount++
			for _, tc := range msg.ToolCalls {
				toolCount++
				toolNames = append(toolNames, tc.Function.Name)
			}
			if strings.Contains(strings.ToLower(msg.Content), "error") {
				errorCount++
			}
		case "tool":
			if strings.Contains(strings.ToLower(msg.Content), "error") {
				errorCount++
			}
		}
	}

	// Deduplicate tool names
	seen := make(map[string]bool)
	var uniqueTools []string
	for _, n := range toolNames {
		if !seen[n] {
			seen[n] = true
			uniqueTools = append(uniqueTools, n)
		}
	}

	summary.WriteString("- ")
	summary.WriteString(strconv.Itoa(userCount))
	summary.WriteString(" user messages, ")
	summary.WriteString(strconv.Itoa(assistantCount))
	summary.WriteString(" assistant responses, ")
	summary.WriteString(strconv.Itoa(toolCount))
	summary.WriteString(" tool calls")
	if len(uniqueTools) > 0 {
		summary.WriteString(" (")
		summary.WriteString(strings.Join(uniqueTools, ", "))
		summary.WriteString(")")
	}
	if errorCount > 0 {
		summary.WriteString(", ")
		summary.WriteString(strconv.Itoa(errorCount))
		summary.WriteString(" errors encountered")
	}
	summary.WriteString(".")

	// Build result: system + summary + recent
	result := make([]Message, 0, 2+recentStart)
	if systemEnd > 0 {
		result = append(result, messages[0])
	}
	result = append(result, Message{Role: "user", Content: summary.String()})
	result = append(result, messages[recentStart:]...)

	return result
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

// trimToolResults trims tool result content to reduce token usage.
func (c *Compactor) trimToolResults(messages []Message, tokenLimit int) []Message {
	result := make([]Message, len(messages))
	copy(result, messages)

	for i := range result {
		if result[i].Role == "tool" && utf8.RuneCountInString(result[i].Content) > 1000 {
			result[i].Content = c.truncateHeadTail(result[i].Content, 500, 250)
		}
	}

	return result
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

// suppress unused import
var _ = fmt.Sprintf
