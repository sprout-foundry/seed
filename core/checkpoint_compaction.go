package core

import (
	"sort"
	"time"
)

// RecordTurnCheckpointAsync asynchronously builds a checkpoint from the given
// messages and stores it in state. It spawns a goroutine to compute the summary
// so it doesn't block the conversation loop.
//
// The message slice is snapshotted immediately (before the goroutine starts) so
// the background computation sees a consistent view even if the caller mutates
// the original slice. If the summary computation takes longer than timeout, a
// minimal checkpoint is stored instead.
func RecordTurnCheckpointAsync(state *State, messages []Message, startIndex, endIndex int, timeout time.Duration) {
	// Snapshot messages immediately so the goroutine sees a consistent view.
	turnMessages := make([]Message, len(messages))
	copy(turnMessages, messages)

	go func() {
		done := make(chan TurnCheckpoint, 1)

		go func() {
			builder := NewTurnSummaryBuilder()
			cp := builder.Build(turnMessages)
			cp.StartIndex = startIndex
			cp.EndIndex = endIndex
			done <- cp
		}()

		select {
		case cp := <-done:
			state.AddCheckpoint(cp)
		case <-time.After(timeout):
			// Store minimal checkpoint if computation timed out.
			state.AddCheckpoint(TurnCheckpoint{
				StartIndex:        startIndex,
				EndIndex:          endIndex,
				Summary:           "Turn completed (summary timed out)",
				ActionableSummary: "Turn completed (summary timed out)",
			})
		}
	}()
}

// isConsumableCheckpoint determines whether a checkpoint can be compacted.
// A checkpoint is consumable when:
// - Its StartIndex and EndIndex are valid within the messages slice
// - StartIndex <= EndIndex
// - The checkpoint has a non-empty Summary
func isConsumableCheckpoint(cp TurnCheckpoint, messageCount int) bool {
	if cp.Summary == "" {
		return false
	}
	if cp.StartIndex < 0 || cp.EndIndex < 0 || cp.StartIndex > cp.EndIndex {
		return false
	}
	if cp.EndIndex >= messageCount {
		return false
	}
	return true
}

// BuildCheckpointCompactedMessages replaces consumed checkpoint ranges with
// summary messages and returns the compacted message list and updated checkpoints.
//
// A checkpoint is "consumable" when:
// - Its StartIndex and EndIndex are valid (within the messages slice)
// - The messages in the range [StartIndex, EndIndex] exist
// - The checkpoint has a non-empty Summary
//
// All checkpoint indices reference the raw state.Messages() slice. Because
// state.Messages() only appends (never inserts or deletes in the middle),
// checkpoint indices remain stable across calls. Consumed checkpoints are
// kept in the returned list so that every call to prepareMessages() can
// re-apply their summaries against the growing message history.
//
// The function works from oldest to newest checkpoint (by StartIndex):
//  1. For each consumable checkpoint, replace messages[StartIndex:EndIndex+1]
//     with a single summary message (role "user", content from checkpoint.Summary)
//  2. Handle consecutive-assistant boundaries: if the inserted summary message
//     would create two consecutive assistant messages with no tool calls between
//     them, merge or adjust.
//
// The summary message role is "user" to maintain proper conversation flow
// (system -> user -> assistant pattern).
//
// Return:
//   - compactedMessages: the new message slice with checkpoints applied
//   - updatedCheckpoints: all checkpoints preserved with their original indices
//     (consumed checkpoints are kept so future calls re-apply their summaries)
func BuildCheckpointCompactedMessages(messages []Message, checkpoints []TurnCheckpoint) ([]Message, []TurnCheckpoint) {
	// Guard: nothing to do with empty inputs.
	if len(messages) == 0 || len(checkpoints) == 0 {
		outMsgs := make([]Message, len(messages))
		copy(outMsgs, messages)
		outCps := make([]TurnCheckpoint, len(checkpoints))
		copy(outCps, checkpoints)
		return outMsgs, outCps
	}

	// Work on a copy so we never mutate the original.
	msgs := make([]Message, len(messages))
	copy(msgs, messages)

	// Sort a copy of checkpoints by StartIndex so we process oldest first.
	sorted := make([]TurnCheckpoint, len(checkpoints))
	copy(sorted, checkpoints)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].StartIndex < sorted[j].StartIndex
	})

	// Phase 1: Walk through sorted checkpoints and determine which are
	// consumable. Record consumable ranges for replacement in Phase 2.
	// All checkpoints (consumed and unconsumed) are kept in the returned
	// list so that every prepareMessages() call can re-apply summaries
	// against the growing state.Messages() history.
	//
	// We track lastConsumedEnd to reject overlapping ranges.
	type rangeInfo struct {
		summary   string
		origStart int // position in original msgs array
		origEnd   int // inclusive end in original msgs array
	}
	var consumables []rangeInfo
	lastConsumedEnd := -1 // highest original index consumed by a previous checkpoint

	for _, cp := range sorted {
		// Reject if not consumable (invalid range or empty summary).
		if !isConsumableCheckpoint(cp, len(msgs)) {
			continue
		}

		// Reject if this checkpoint overlaps with a previously consumed range.
		if cp.StartIndex <= lastConsumedEnd {
			continue
		}

		ri := rangeInfo{
			summary:   cp.Summary,
			origStart: cp.StartIndex,
			origEnd:   cp.EndIndex,
		}
		consumables = append(consumables, ri)

		lastConsumedEnd = cp.EndIndex
	}

	// If nothing was consumed, return copies of the original data.
	if len(consumables) == 0 {
		outMsgs := make([]Message, len(msgs))
		copy(outMsgs, msgs)
		outCps := make([]TurnCheckpoint, len(checkpoints))
		copy(outCps, checkpoints)
		return outMsgs, outCps
	}

	// Phase 2: Build compacted messages by walking through msgs and
	// replacing consumed ranges with summary messages.
	newMsgs := make([]Message, 0, len(msgs))
	msgIdx := 0
	consIdx := 0

	for msgIdx < len(msgs) {
		if consIdx < len(consumables) && msgIdx == consumables[consIdx].origStart {
			ri := consumables[consIdx]
			newMsgs = append(newMsgs, Message{Role: "user", Content: ri.summary})
			msgIdx = ri.origEnd + 1
			consIdx++
			continue
		}
		newMsgs = append(newMsgs, msgs[msgIdx])
		msgIdx++
	}

	// Phase 3: Resolve consecutive-assistant boundaries.
	// Since summaries are inserted with role "user", this should rarely fire,
	// but it guards against edge cases (e.g., ranges containing only assistant msgs).
	newMsgs = resolveConsecutiveAssistantMessages(newMsgs)

	// Phase 4: Return all checkpoints with their original indices intact.
	// Checkpoint indices always reference the raw state.Messages() slice,
	// which only appends (never inserts/deletes in the middle). Consumed
	// checkpoints are kept so future prepareMessages() calls re-apply their
	// summaries against the growing message history.
	outCps := make([]TurnCheckpoint, len(checkpoints))
	copy(outCps, checkpoints)

	return newMsgs, outCps
}

// resolveConsecutiveAssistantMessages fixes consecutive assistant messages in the
// compacted result. This can happen when checkpoint ranges are replaced and the
// boundary messages happen to be assistant role.
//
// Strategy (applied left-to-right):
//  1. Both messages have no tool calls → merge content into the first, drop the second.
//  2. First has tool calls, second does not → merge content, drop the second.
//  3. First has no tool calls, second does → merge content into the first and
//     transfer the tool calls, then drop the second.
//  4. Both have tool calls → merge content, drop the second.
//
// Note: Images and ToolCallID from the dropped (second) message are not preserved.
// In well-formed OpenAI-format conversations, assistant messages never carry
// ToolCallID (that field belongs to tool-result messages), so this is not a
// practical concern.
//
// The function works on a copy of the input slice so it never mutates the caller's
// data. After each merge/drop the loop index is decremented so the next pair is checked.
func resolveConsecutiveAssistantMessages(msgs []Message) []Message {
	// Work on a copy so we never mutate the caller's slice.
	out := make([]Message, len(msgs))
	copy(out, msgs)

	for i := 1; i < len(out); i++ {
		if out[i-1].Role != "assistant" || out[i].Role != "assistant" {
			continue
		}

		prevHasTools := len(out[i-1].ToolCalls) > 0
		currHasTools := len(out[i].ToolCalls) > 0

		switch {
		case !prevHasTools && !currHasTools:
			// Both plain — merge content.
			out[i-1].Content += "\n" + out[i].Content

		case !prevHasTools && currHasTools:
			// Second carries tool calls — merge content and transfer them.
			out[i-1].Content += "\n" + out[i].Content
			out[i-1].ToolCalls = out[i].ToolCalls

		default:
			// First already has tool calls (or both do) — merge content and drop the second.
			out[i-1].Content += "\n" + out[i].Content
		}

		// Remove the second message and re-check this index.
		out = append(out[:i], out[i+1:]...)
		i--
	}
	return out
}
