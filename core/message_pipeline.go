package core

import "strings"

// prepareMessages assembles the message list for the API request.
//
// Two passes are pressure-gated by Agent.compactionStartFractionOrDefault():
//
//   - Checkpoint substitution: replaces past checkpointed turns with their
//     summary messages. Lossy — the raw turn's tool outputs/file reads are
//     hidden behind a one-line summary. Only fires when raw history already
//     exceeds the start-gate fraction of context.
//   - Observation masking (inside the optimizer): replaces big consumed
//     tool results with placeholders. Same gate.
//
// Below the start-gate fraction, the raw conversation flows through. This
// matters in two cases the user actually cares about: (1) short and
// medium-length chats stay lucid because the model can refer back to its
// prior file reads, (2) long single-turn iteration chains don't plateau
// because old tool results aren't masked until pressure justifies it.
//
// The estimate uses the raw state.Messages() slice (pre-transform) so the
// gate reflects true pressure, not the artificially-low post-transform
// pressure the previous unconditional path produced.
func (ch *ConversationHandler) prepareMessages() []Message {
	// Get current messages
	messages := ch.agent.state.Messages()

	// Pressure check on the raw conversation. We deliberately estimate on
	// messages before checkpoint substitution and observation masking so
	// the gate sees the actual cost of shipping the raw history — if we
	// estimated after transforming, the gate would be self-negating (the
	// transformations always make it look small enough to skip them).
	pressureGate := ch.computePressureGate(messages)

	// Checkpoint compaction: replace consumed checkpoint ranges with summary
	// messages before any other transformation. Checkpoint indices reference
	// the raw state.Messages() slice, so this must run first.
	//
	// SP-059-followup: only substitute under pressure. The checkpoints
	// themselves are still recorded every turn (cheap, useful for
	// embedding lookups even when not consumed here).
	if pressureGate.underPressure {
		checkpoints := ch.agent.state.GetCheckpoints()
		if len(checkpoints) > 0 {
			messages, _ = BuildCheckpointCompactedMessages(messages, checkpoints)
		}
	}

	// Strip system messages from history (we always prepend current system prompt)
	filtered := make([]Message, 0, len(messages))
	for _, m := range messages {
		if m.Role != "system" {
			filtered = append(filtered, m)
		}
	}

	// Strip images for non-vision models
	if !ch.agent.provider.Info().HasVision {
		filtered = ch.stripImages(filtered)
	}

	// Prepend system prompt
	allMessages := []Message{{Role: "system", Content: ch.agent.systemPrompt}}
	allMessages = append(allMessages, filtered...)

	// Append transient messages (one-shot, then discard)
	ch.transientMu.Lock()
	if len(ch.transientMsgs) > 0 {
		allMessages = append(allMessages, ch.transientMsgs...)
		ch.transientMsgs = nil
	}
	ch.transientMu.Unlock()

	// Collapse multiple system messages into one at the front
	allMessages = collapseSystemMessages(allMessages)

	// Sanitize: remove orphaned tool results
	allMessages = ch.removeOrphanedToolResults(allMessages)

	// Sanitize: strip ANSI escape codes from message content to prevent
	// terminal formatting codes from polluting LLM context.
	allMessages = sanitizeMessages(allMessages)

	// Optimize: deduplicate redundant file reads and shell commands.
	// Observation masking (big-tool-result placeholders) is gated on the
	// same pressure check as checkpoint substitution above. Dedup itself
	// is always safe so it runs unconditionally.
	if ch.agent.optimizer != nil {
		allMessages = ch.agent.optimizer.OptimizeConversationWithOptions(allMessages, OptimizeOptions{
			MaskConsumedToolResults: pressureGate.underPressure,
		})
	}

	return allMessages
}

// pressureGateResult carries the cached pressure check from a single
// prepareMessages call so both gating sites use the same decision.
type pressureGateResult struct {
	rawTokens     int
	contextSize   int
	threshold     int
	underPressure bool
}

// computePressureGate returns the pressure decision for the given raw
// messages. ContextSize == 0 (some providers don't report one) collapses
// to "not under pressure" — better to keep raw history than guess.
func (ch *ConversationHandler) computePressureGate(rawMessages []Message) pressureGateResult {
	contextSize := ch.agent.provider.Info().ContextSize
	if contextSize <= 0 {
		return pressureGateResult{contextSize: contextSize}
	}
	rawTokens := ch.agent.provider.EstimateTokens(&ChatRequest{
		Messages: rawMessages,
		Tools:    ch.agent.executor.GetTools(),
	})
	threshold := int(float64(contextSize) * ch.agent.compactionStartFractionOrDefault())
	return pressureGateResult{
		rawTokens:     rawTokens,
		contextSize:   contextSize,
		threshold:     threshold,
		underPressure: rawTokens > threshold,
	}
}

// stripImages removes image data from messages for non-vision models.
func (ch *ConversationHandler) stripImages(messages []Message) []Message {
	out := make([]Message, len(messages))
	copy(out, messages)
	for i := range out {
		out[i].Images = nil
	}
	return out
}

// removeOrphanedToolResults removes tool messages whose tool_call_id doesn't
// match any assistant message with tool_calls.
func (ch *ConversationHandler) removeOrphanedToolResults(messages []Message) []Message {
	validIDs := make(map[string]struct{})
	for _, msg := range messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					validIDs[tc.ID] = struct{}{}
				}
			}
		}
	}

	filtered := make([]Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "tool" {
			if _, ok := validIDs[msg.ToolCallID]; ok {
				filtered = append(filtered, msg)
			} else {
				ch.agent.debugLog("[clean] Removed orphaned tool result: %s\n", msg.ToolCallID)
			}
		} else {
			filtered = append(filtered, msg)
		}
	}
	return filtered
}

// collapseSystemMessages merges multiple system messages into one at the front.
func collapseSystemMessages(messages []Message) []Message {
	if len(messages) <= 1 {
		return messages
	}

	var systemParts []string
	nonSystem := make([]Message, 0, len(messages))

	for _, msg := range messages {
		if msg.Role == "system" {
			if content := strings.TrimSpace(msg.Content); content != "" {
				systemParts = append(systemParts, content)
			}
		} else {
			nonSystem = append(nonSystem, msg)
		}
	}

	if len(systemParts) == 0 {
		return messages
	}

	merged := Message{Role: "system", Content: strings.Join(systemParts, "\n\n")}
	result := make([]Message, 0, len(nonSystem)+1)
	result = append(result, merged)
	result = append(result, nonSystem...)
	return result
}

// sanitizeMessages applies sanitizeANSI to every message's content.
func sanitizeMessages(messages []Message) []Message {
	changed := false
	for i := range messages {
		if messages[i].Content != sanitizeANSI(messages[i].Content) {
			changed = true
		}
	}
	if !changed {
		return messages
	}
	// Only copy if something changed to avoid unnecessary allocation.
	out := make([]Message, len(messages))
	copy(out, messages)
	for i := range out {
		out[i].Content = sanitizeANSI(out[i].Content)
	}
	return out
}
