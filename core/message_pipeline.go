package core

import "strings"

// prepareMessages assembles the message list for the API request.
//
// Progressive pipeline:
//
//   - First, estimate the raw conversation against the trigger fraction.
//   - If over: run **Phase 0a** (iterative checkpoint substitution) on the
//     raw state.Messages() slice — this is where checkpoint indices are
//     valid. Stop as soon as the estimate falls to target.
//   - If still over: run **Phase 0b** (iterative observation masking)
//     against the post-substitution slice. Same iterative contract.
//   - Below the trigger: raw history flows through. No substitution, no
//     masking. The model gets full fidelity for short and medium chats
//     and for long single-turn tool-iteration chains.
//
// The loop's downstream pressure check (in conversation.go) then runs
// **Phase 1+** drops via Compact() only if Phase 0 didn't relieve enough
// pressure (or if the consumer wired pruner/llmSummarizer). Recovery from
// provider-overflow errors uses Compact()'s drop path directly.
//
// See docs/compaction.md for the full design.
func (ch *ConversationHandler) prepareMessages() []Message {
	// Get current messages
	messages := ch.agent.state.Messages()

	// Phase 0 (iterative, loss-minimizing) — operate on the RAW slice so
	// checkpoint indices remain valid. Only fires when raw history exceeds
	// the trigger × context_size threshold; below that, raw flows through.
	messages = ch.runIterativeCompactionPhase0(messages)

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

	// Optimize: dedupe-only pass. Observation masking is *not* run here —
	// it's part of Compact()'s iterative Phase 0b, gated on pressure.
	// Dedup of redundant file reads / shell command outputs is always
	// strictly beneficial (it only replaces tool-result content when an
	// IDENTICAL earlier content exists) so it runs unconditionally.
	if ch.agent.optimizer != nil {
		allMessages = ch.agent.optimizer.OptimizeConversationWithOptions(allMessages, OptimizeOptions{
			MaskConsumedToolResults: false,
		})
	}

	return allMessages
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

// runIterativeCompactionPhase0 applies Phase 0 (loss-minimizing)
// compaction to the raw state.Messages() slice:
//   - Phase 0a: iterative checkpoint substitution (oldest first, one at a
//     time, stop when under target). Operates on the raw slice so the
//     checkpoint StartIndex/EndIndex references remain valid.
//   - Phase 0b: iterative observation masking of big consumed tool results
//     (oldest first, one at a time, honoring the keep-last window).
//
// Returns the raw slice unchanged when the conversation is under the
// trigger fraction of the model's context window. Returns the most
// aggressively-compacted variant Phase 0 could produce otherwise; the
// caller's downstream pressure check decides whether Phase 1+ drops are
// still needed.
func (ch *ConversationHandler) runIterativeCompactionPhase0(rawMessages []Message) []Message {
	contextSize := ch.agent.provider.Info().ContextSize
	if contextSize <= 0 || len(rawMessages) == 0 {
		return rawMessages
	}

	// Caller-supplied estimator so this code and the loop's trigger
	// decision use the same token math. Without it, the loop may decide
	// "compact" and Phase 0 may decide "nothing to do" (or vice versa)
	// and the model silently sees one of the two views.
	tools := ch.agent.executor.GetTools()
	provider := ch.agent.provider
	estimate := func(msgs []Message) int {
		return provider.EstimateTokens(&ChatRequest{Messages: msgs, Tools: tools})
	}

	// Trigger gate: same threshold the loop's downstream check uses.
	triggerLimit := int(float64(contextSize) * ch.agent.triggerFractionOrDefault())
	if estimate(rawMessages) <= triggerLimit {
		return rawMessages
	}
	target := int(float64(contextSize) * emergencyTargetFraction)

	// Phase 0a: iterative checkpoint substitution.
	checkpoints := ch.agent.state.GetCheckpoints()
	if len(checkpoints) > 0 {
		newMsgs, _, under := IterativelySubstituteCheckpoints(rawMessages, checkpoints, target, estimate)
		rawMessages = newMsgs
		if under {
			return rawMessages
		}
	}

	// Phase 0b: iterative observation masking. Only enabled when the
	// optimizer is wired — without it we can't resolve tool-call IDs to
	// human-readable tool names for the placeholder text.
	if ch.agent.optimizer != nil {
		nameFn := func(callID string) string {
			for _, m := range rawMessages {
				if m.Role != "assistant" {
					continue
				}
				for _, tc := range m.ToolCalls {
					if tc.ID == callID {
						return tc.Function.Name
					}
				}
			}
			if callID != "" {
				return callID
			}
			return "tool"
		}
		newMsgs, _, _ := IterativelyMaskOldestConsumedToolResults(rawMessages, nameFn, target, estimate)
		rawMessages = newMsgs
	}

	return rawMessages
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
