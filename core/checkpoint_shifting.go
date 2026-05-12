package core

// ShiftCheckpointIndices updates checkpoint StartIndex/EndIndex values
// after compaction has removed or merged messages. It takes the original
// message list (before compaction), the compacted message list (after
// compaction), and the current checkpoints, and returns a new slice of
// checkpoints with corrected indices.
//
// For each checkpoint:
//   - If both its StartIndex and EndIndex messages survived compaction,
//     shift them to their new positions in the compacted array.
//   - If the checkpoint's range was partially consumed (some messages
//     removed), trim the range to only include surviving messages.
//   - If the entire range was consumed, mark the checkpoint as invalid
//     by setting StartIndex and EndIndex to -1.
//
// The function uses a greedy position-matching algorithm:
// 1. Walk through old and new message arrays simultaneously
// 2. Match messages by role, content, and tool_call_id
// 3. Build a mapping from old index → new index (or -1 if removed)
// 4. Apply the mapping to each checkpoint
//
// Parameters:
//   - oldMessages: message list before compaction
//   - newMessages: message list after compaction
//   - checkpoints: current checkpoints with old indices
//
// Returns:
//   - Updated checkpoints with corrected indices
func ShiftCheckpointIndices(oldMessages, newMessages []Message, checkpoints []TurnCheckpoint) []TurnCheckpoint {
	// Edge cases: return copies of checkpoints unchanged.
	if len(oldMessages) == 0 || len(newMessages) == 0 || len(checkpoints) == 0 {
		out := make([]TurnCheckpoint, len(checkpoints))
		copy(out, checkpoints)
		return out
	}

	// Phase 1: Build position mapping from old index → new index.
	// Use a greedy matching approach: for each old message, find the first
	// unmatched new message with the same role, content, and tool_call_id.
	// For messages with tool calls, also compare tool call names.
	matched := make([]int, len(oldMessages)) // old index → new index (or -1)
	for i := range matched {
		matched[i] = -1
	}
	newUsed := make([]bool, len(newMessages))

	for oldIdx, oldMsg := range oldMessages {
		var bestNewIdx = -1
		var bestScore = 0 // Only accept scores > 0 (actual matches).

		for newIdx, newMsg := range newMessages {
			if newUsed[newIdx] {
				continue
			}

			score := matchMessages(oldMsg, newMsg)
			if score > bestScore {
				bestScore = score
				bestNewIdx = newIdx
			}
		}

		// Require a strong match (score >= 50) to prevent false matches.
		// A score of 50 means role and content agree but tool_call_id differs;
		// for tool messages this would be incorrect, so we only accept score 100
		// when either message has a tool_call_id.
		if bestNewIdx >= 0 {
			// Reject partial matches for tool messages: tool_call_id must agree.
			candidateNewMsg := newMessages[bestNewIdx]
			if bestScore < 100 && (oldMsg.ToolCallID != "" || candidateNewMsg.ToolCallID != "") {
				// No valid match for tool messages with differing tool_call_ids.
			} else {
				matched[oldIdx] = bestNewIdx
				newUsed[bestNewIdx] = true
			}
		}
	}

	// Phase 2: Apply the position mapping to each checkpoint.
	result := make([]TurnCheckpoint, len(checkpoints))
	for i, cp := range checkpoints {
		if cp.StartIndex < 0 || cp.EndIndex < 0 || cp.StartIndex > cp.EndIndex {
			// Already invalid — keep as-is.
			result[i] = cp
			continue
		}

		// Check if indices are within bounds of old messages.
		if cp.StartIndex >= len(oldMessages) || cp.EndIndex >= len(oldMessages) {
			result[i] = cp
			continue
		}

		startNew := matched[cp.StartIndex]
		endNew := matched[cp.EndIndex]

		// Both indices survived → use their new positions directly.
		if startNew >= 0 && endNew >= 0 {
			// Ensure start <= end after remapping.
			s, e := startNew, endNew
			if s > e {
				s, e = e, s
			}
			if s >= len(newMessages) {
				s = len(newMessages) - 1
			}
			if e >= len(newMessages) {
				e = len(newMessages) - 1
			}
			result[i] = cp
			result[i].StartIndex = s
			result[i].EndIndex = e
			continue
		}

		// Both removed → checkpoint is entirely consumed.
		if startNew < 0 && endNew < 0 {
			result[i] = cp
			result[i].StartIndex = -1
			result[i].EndIndex = -1
			continue
		}

		// Partially consumed: at least one boundary is missing.
		// Find the surviving range within [StartIndex, EndIndex].
		var firstSurviving, lastSurviving = -1, -1
		for j := cp.StartIndex; j <= cp.EndIndex; j++ {
			newIdx := matched[j]
			if newIdx >= 0 {
				if firstSurviving < 0 {
					firstSurviving = newIdx
				}
				lastSurviving = newIdx
			}
		}

		if firstSurviving < 0 {
			// All messages in range were removed.
			result[i] = cp
			result[i].StartIndex = -1
			result[i].EndIndex = -1
		} else {
			result[i] = cp
			result[i].StartIndex = firstSurviving
			result[i].EndIndex = lastSurviving
		}
	}

	return result
}

// matchMessages returns a score indicating how well newMsg matches oldMsg.
// A score of 100 means a perfect match (role + content + tool_call_id all agree).
// A score of 50 means a partial match (role + content agree, but tool_call_id differs).
// A score of 0 means no match.
func matchMessages(oldMsg, newMsg Message) int {
	if oldMsg.Role != newMsg.Role {
		return 0
	}

	if oldMsg.Content != newMsg.Content {
		return 0
	}

	if oldMsg.ToolCallID != newMsg.ToolCallID {
		return 50 // Same role and content but different tool_call_id
	}

	// Compare tool calls by name for additional specificity.
	if !toolCallsSameName(oldMsg.ToolCalls, newMsg.ToolCalls) {
		return 50 // Same base fields but different tool call names
	}

	return 100
}

// toolCallsSameName checks whether two slices of tool calls share the same
// set of function names in the same order. It is used as a secondary match
// criterion when role and content already agree.
func toolCallsSameName(a, b []ToolCall) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Function.Name != b[i].Function.Name {
			return false
		}
	}
	return true
}
