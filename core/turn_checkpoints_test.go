package core

import (
	"strings"
	"testing"
	"time"
)

// --- BuildCheckpointCompactedMessages tests ---

func TestBuildCheckpointCompactedMessages_EmptyMessages(t *testing.T) {
	msgs, cps := BuildCheckpointCompactedMessages(nil, []TurnCheckpoint{})
	if len(msgs) != 0 {
		t.Errorf("expected empty messages, got %d", len(msgs))
	}
	if len(cps) != 0 {
		t.Errorf("expected empty checkpoints, got %d", len(cps))
	}
}

func TestBuildCheckpointCompactedMessages_EmptyCheckpoints(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	cp := []TurnCheckpoint{}
	outMsgs, outCps := BuildCheckpointCompactedMessages(msgs, cp)

	// Should return copies unchanged.
	if len(outMsgs) != len(msgs) {
		t.Errorf("expected %d messages, got %d", len(msgs), len(outMsgs))
	}
	if len(outCps) != 0 {
		t.Errorf("expected 0 checkpoints, got %d", len(outCps))
	}

	// Verify originals were not modified.
	if msgs[0].Content != "hi" {
		t.Error("original messages were mutated")
	}
}

func TestBuildCheckpointCompactedMessages_NoConsumable(t *testing.T) {
	// Checkpoint with empty summary is not consumable.
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 0, EndIndex: 1, Summary: "", ActionableSummary: ""},
	}

	outMsgs, outCps := BuildCheckpointCompactedMessages(msgs, checkpoints)

	// Messages should be unchanged.
	if len(outMsgs) != len(msgs) {
		t.Errorf("expected %d messages, got %d", len(msgs), len(outMsgs))
	}
	// Checkpoint should be kept as-is (not consumed).
	if len(outCps) != 1 {
		t.Errorf("expected 1 checkpoint, got %d", len(outCps))
	}
	if outCps[0].Summary != "" {
		t.Error("checkpoint should be unchanged")
	}
}

func TestBuildCheckpointCompactedMessages_SingleCheckpoint(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "What is 2+2?"},
		{Role: "assistant", Content: "The answer is 4."},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 1, EndIndex: 2, Summary: "User asked about 2+2, assistant answered 4.", ActionableSummary: "- Question: What is 2+2?\n- Result: 4"},
	}

	outMsgs, outCps := BuildCheckpointCompactedMessages(msgs, checkpoints)

	// Should have 2 messages: system + summary.
	if len(outMsgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(outMsgs))
	}

	if outMsgs[0].Role != "system" || outMsgs[0].Content != "You are helpful." {
		t.Errorf("system message mismatch: %+v", outMsgs[0])
	}
	if outMsgs[1].Role != "user" {
		t.Errorf("expected summary to be user role, got %s", outMsgs[1].Role)
	}
	if outMsgs[1].Content != "User asked about 2+2, assistant answered 4." {
		t.Errorf("unexpected summary content: %s", outMsgs[1].Content)
	}

	// Checkpoint should be consumed (removed from result).
	if len(outCps) != 0 {
		t.Errorf("expected 0 remaining checkpoints, got %d", len(outCps))
	}
}

func TestBuildCheckpointCompactedMessages_MultipleCheckpoints(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Question 1"},
		{Role: "assistant", Content: "Answer 1"},
		{Role: "user", Content: "Question 2"},
		{Role: "assistant", Content: "Answer 2"},
		{Role: "user", Content: "Question 3"},
		{Role: "assistant", Content: "Answer 3"},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 1, EndIndex: 2, Summary: "Summary 1"},
		{StartIndex: 3, EndIndex: 4, Summary: "Summary 2"},
	}

	outMsgs, outCps := BuildCheckpointCompactedMessages(msgs, checkpoints)

	// 3 messages (system) + 2 summaries + 1 last turn = 5 messages.
	if len(outMsgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(outMsgs))
	}

	// First two checkpoints should be consumed.
	if len(outCps) != 0 {
		t.Errorf("expected 0 remaining checkpoints, got %d", len(outCps))
	}

	// Verify structure: system, summary1, summary2, user3, assistant3
	expectedRoles := []string{"system", "user", "user", "user", "assistant"}
	for i, role := range expectedRoles {
		if outMsgs[i].Role != role {
			t.Errorf("expected role %s at index %d, got %s", role, i, outMsgs[i].Role)
		}
	}
}

func TestBuildCheckpointCompactedMessages_MixedConsumable(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "Question 1"},
		{Role: "assistant", Content: "Answer 1"},
		{Role: "user", Content: "Question 2"},
		{Role: "assistant", Content: "Answer 2"},
		{Role: "user", Content: "Question 3"},
		{Role: "assistant", Content: "Answer 3"},
	}
	// Checkpoint 1: consumable (has summary).
	// Checkpoint 2: not consumable (empty summary).
	// Checkpoint 3: consumable.
	checkpoints := []TurnCheckpoint{
		{StartIndex: 0, EndIndex: 1, Summary: "Summary 1"},
		{StartIndex: 2, EndIndex: 3, Summary: ""},
		{StartIndex: 4, EndIndex: 5, Summary: "Summary 3"},
	}

	outMsgs, outCps := BuildCheckpointCompactedMessages(msgs, checkpoints)

	// Checkpoint 1 consumed: [0,1] -> 1 summary. 1 message removed.
	// Checkpoint 2 unconsumable: kept as-is (original indices 2,3).
	// Checkpoint 3 consumed: original [4,5] -> 1 summary. 1 message removed.
	// Result messages: summary1, user2, assistant2, summary3 = 4 messages.
	//
	// Remaining checkpoint 2: original [2,3].
	// Only consumed range [0,1] (origEnd=1) is before [2,3], so removedBefore = 1.
	// Shifted: [2-1, 3-1] = [1, 2].
	// Consumed range [4,5] (origEnd=5) is NOT before [2,3], so it does not shift.

	if len(outMsgs) != 4 {
		t.Errorf("expected 4 messages, got %d", len(outMsgs))
	}

	// Remaining: only the unconsumable checkpoint 2 (shifted).
	if len(outCps) != 1 {
		t.Errorf("expected 1 remaining checkpoint, got %d", len(outCps))
	}
	// Shifted from [2,3] to [1,2] (only the consumed range before it counts).
	if outCps[0].StartIndex != 1 || outCps[0].EndIndex != 2 {
		t.Errorf("expected checkpoint [1,2], got [%d,%d]", outCps[0].StartIndex, outCps[0].EndIndex)
	}
}

func TestBuildCheckpointCompactedMessages_InvalidRange(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	// StartIndex > EndIndex is invalid.
	checkpoints := []TurnCheckpoint{
		{StartIndex: 1, EndIndex: 0, Summary: "invalid range"},
	}

	outMsgs, outCps := BuildCheckpointCompactedMessages(msgs, checkpoints)

	// Should be unchanged.
	if len(outMsgs) != len(msgs) {
		t.Errorf("expected %d messages, got %d", len(msgs), len(outMsgs))
	}
	if len(outCps) != 1 {
		t.Errorf("expected 1 checkpoint (not consumed), got %d", len(outCps))
	}
}

func TestBuildCheckpointCompactedMessages_OutOfRangeEndIndex(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 0, EndIndex: 100, Summary: "out of range"},
	}

	outMsgs, outCps := BuildCheckpointCompactedMessages(msgs, checkpoints)

	// Not consumable — EndIndex out of bounds.
	if len(outMsgs) != len(msgs) {
		t.Errorf("expected %d messages, got %d", len(msgs), len(outMsgs))
	}
	if len(outCps) != 1 {
		t.Errorf("expected 1 checkpoint (not consumed), got %d", len(outCps))
	}
}

func TestBuildCheckpointCompactedMessages_OutOfRangeStartIndex(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: -1, EndIndex: 0, Summary: "negative start"},
	}

	outMsgs, outCps := BuildCheckpointCompactedMessages(msgs, checkpoints)

	// Not consumable — negative StartIndex.
	if len(outMsgs) != len(msgs) {
		t.Errorf("expected %d messages, got %d", len(msgs), len(outMsgs))
	}
	if len(outCps) != 1 {
		t.Errorf("expected 1 checkpoint (not consumed), got %d", len(outCps))
	}
}

func TestBuildCheckpointCompactedMessages_OriginalNotModified(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "original 1"},
		{Role: "assistant", Content: "original 2"},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 0, EndIndex: 1, Summary: "replaced"},
	}

	// Copy originals for comparison.
	origMsgs := make([]Message, len(msgs))
	copy(origMsgs, msgs)

	_, _ = BuildCheckpointCompactedMessages(msgs, checkpoints)

	// Verify originals unchanged.
	for i := range msgs {
		if msgs[i].Content != origMsgs[i].Content {
			t.Errorf("original message %d was mutated: %q -> %q", i, origMsgs[i].Content, msgs[i].Content)
		}
	}
}

func TestBuildCheckpointCompactedMessages_AllButLastConsumed(t *testing.T) {
	// Three consumed checkpoints covering [0,1], [2,3], [4,5],
	// but 8 messages total — indices 6,7 are not covered.
	msgs := []Message{
		{Role: "user", Content: "Q1"},
		{Role: "assistant", Content: "A1"},
		{Role: "user", Content: "Q2"},
		{Role: "assistant", Content: "A2"},
		{Role: "user", Content: "Q3"},
		{Role: "assistant", Content: "A3"},
		{Role: "user", Content: "Q4"},
		{Role: "assistant", Content: "A4"},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 0, EndIndex: 1, Summary: "S1"},
		{StartIndex: 2, EndIndex: 3, Summary: "S2"},
		{StartIndex: 4, EndIndex: 5, Summary: "S3"},
	}

	outMsgs, outCps := BuildCheckpointCompactedMessages(msgs, checkpoints)

	// 3 summaries + Q4 + A4 = 5 messages (indices 6,7 not covered)
	if len(outMsgs) != 5 {
		t.Errorf("expected 5 messages, got %d", len(outMsgs))
	}
	if len(outCps) != 0 {
		t.Errorf("expected 0 remaining checkpoints, got %d", len(outCps))
	}
}

func TestBuildCheckpointCompactedMessages_CheckpointSummariesOnly(t *testing.T) {
	// Test with only summaries (no tool calls, just user/assistant pairs).
	msgs := []Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "First question with lots of content"},
		{Role: "assistant", Content: "First answer with lots of content"},
		{Role: "user", Content: "Second question with lots of content"},
		{Role: "assistant", Content: "Second answer with lots of content"},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 1, EndIndex: 2, Summary: "User asked about topic A."},
		{StartIndex: 3, EndIndex: 4, Summary: "User asked about topic B."},
	}

	outMsgs, outCps := BuildCheckpointCompactedMessages(msgs, checkpoints)

	// 1 system + 2 summaries = 3 messages.
	if len(outMsgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(outMsgs))
	}
	if outMsgs[0].Role != "system" {
		t.Errorf("first message should be system, got %s", outMsgs[0].Role)
	}
	if outMsgs[1].Role != "user" || outMsgs[1].Content != "User asked about topic A." {
		t.Errorf("second message wrong: %+v", outMsgs[1])
	}
	if outMsgs[2].Role != "user" || outMsgs[2].Content != "User asked about topic B." {
		t.Errorf("third message wrong: %+v", outMsgs[2])
	}
	if len(outCps) != 0 {
		t.Errorf("expected 0 remaining checkpoints, got %d", len(outCps))
	}
}

func TestBuildCheckpointCompactedMessages_CheckpointAfterConsecutiveAssistant(t *testing.T) {
	// Edge case: the checkpoint range is [1, 1] — only the assistant message.
	// Message 0 is also assistant. After replacement, message at index 0 (assistant)
	// and index 1 (summary, "user") are consecutive — but since summary is "user",
	// this is fine. The consecutive-assistant guard shouldn't trigger.
	msgs := []Message{
		{Role: "user", Content: "Query"},
		{Role: "assistant", Content: "First thought"},
		{Role: "user", Content: "Follow-up"},
		{Role: "assistant", Content: "Second response"},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 1, EndIndex: 1, Summary: "Assistant had a thought."},
	}

	outMsgs, outCps := BuildCheckpointCompactedMessages(msgs, checkpoints)

	// Checkpoint [1,1] is consumed → 1 summary replaces 1 msg, delta=0.
	// Result: user, summary(user), user, assistant = 4 messages.
	if len(outMsgs) != 4 {
		t.Errorf("expected 4 messages, got %d", len(outMsgs))
	}
	// Checkpoint was consumed, so no remaining checkpoints.
	if len(outCps) != 0 {
		t.Errorf("expected 0 remaining checkpoints, got %d", len(outCps))
	}
	// Verify structure: user(summary), summary, user, assistant
	if outMsgs[0].Role != "user" || outMsgs[0].Content != "Query" {
		t.Errorf("first message wrong: %+v", outMsgs[0])
	}
	if outMsgs[1].Role != "user" || outMsgs[1].Content != "Assistant had a thought." {
		t.Errorf("second message wrong: %+v", outMsgs[1])
	}
}

func TestBuildCheckpointCompactedMessages_ToolCallTurn(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "List files"},
		{Role: "assistant", Content: "I'll list files.", ToolCalls: []ToolCall{
			{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "list_files", Arguments: `{}`}},
		}},
		{Role: "tool", Content: "file1.txt, file2.txt", ToolCallID: "call_1"},
		{Role: "assistant", Content: "Here are the files."},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 1, EndIndex: 4, Summary: "User asked to list files. Assistant listed file1.txt and file2.txt."},
	}

	outMsgs, _ := BuildCheckpointCompactedMessages(msgs, checkpoints)

	// 1 system + 1 summary = 2 messages.
	if len(outMsgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(outMsgs))
	}
	if outMsgs[0].Role != "system" {
		t.Errorf("expected system first, got %s", outMsgs[0].Role)
	}
	if outMsgs[1].Content != "User asked to list files. Assistant listed file1.txt and file2.txt." {
		t.Errorf("unexpected summary: %s", outMsgs[1].Content)
	}
}

func TestBuildCheckpointCompactedMessages_AllMessagesConsumed(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	// Single checkpoint covering all messages — should produce one summary.
	checkpoints := []TurnCheckpoint{
		{StartIndex: 0, EndIndex: 1, Summary: "consumed"},
	}

	outMsgs, outCps := BuildCheckpointCompactedMessages(msgs, checkpoints)

	// Both messages consumed, 1 summary left.
	if len(outMsgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(outMsgs))
	}
	if len(outCps) != 0 {
		t.Errorf("expected 0 remaining checkpoints, got %d", len(outCps))
	}
}

func TestBuildCheckpointCompactedMessages_ThreeCheckpointsWithOneUnconsumed(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "Q1"},
		{Role: "assistant", Content: "A1"},
		{Role: "user", Content: "Q2"},
		{Role: "assistant", Content: "A2"},
		{Role: "user", Content: "Q3"},
		{Role: "assistant", Content: "A3"},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 0, EndIndex: 1, Summary: "S1"}, // consumed
		{StartIndex: 2, EndIndex: 3, Summary: ""},   // unconsumable (empty)
		{StartIndex: 4, EndIndex: 5, Summary: "S3"}, // consumed
	}

	outMsgs, outCps := BuildCheckpointCompactedMessages(msgs, checkpoints)

	// First consumed: [0,1] -> 1 summary. 1 message removed.
	// Third consumed: [4,5] -> 1 summary. 1 message removed.
	// Result: 1 summary + user2 + assistant2 + 1 summary = 4 messages.
	if len(outMsgs) != 4 {
		t.Errorf("expected 4 messages, got %d", len(outMsgs))
	}

	// Only checkpoint 2 should remain.
	if len(outCps) != 1 {
		t.Errorf("expected 1 remaining checkpoint, got %d", len(outCps))
	}
	// Checkpoint 2: original [2,3].
	// Consumed range [0,1] (origEnd=1) is before [2,3]: removedBefore = 1.
	// Consumed range [4,5] (origEnd=5) is NOT before [2,3]: no additional shift.
	// Shifted: [2-1, 3-1] = [1, 2].
	if outCps[0].StartIndex != 1 || outCps[0].EndIndex != 2 {
		t.Errorf("expected shifted [1,2], got [%d,%d]", outCps[0].StartIndex, outCps[0].EndIndex)
	}
}

// --- Overlapping checkpoints test ---

func TestBuildCheckpointCompactedMessages_OverlappingCheckpoints(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "Q1"},
		{Role: "assistant", Content: "A1"},
		{Role: "user", Content: "Q2"},
		{Role: "assistant", Content: "A2"},
		{Role: "user", Content: "Q3"},
		{Role: "assistant", Content: "A3"},
	}
	// Two overlapping checkpoints: [0,2] and [2,4] — message 2 is shared.
	checkpoints := []TurnCheckpoint{
		{StartIndex: 0, EndIndex: 2, Summary: "S1"},
		{StartIndex: 2, EndIndex: 4, Summary: "S2"},
	}

	outMsgs, outCps := BuildCheckpointCompactedMessages(msgs, checkpoints)

	// First consumed [0,2], second overlaps so it's kept as remaining.
	// Result: summary S1 + msgs[3] + msgs[4] + msgs[5] = 4 messages.
	if len(outMsgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(outMsgs))
	}
	if outMsgs[0].Content != "S1" {
		t.Errorf("expected first message to be S1 summary, got %q", outMsgs[0].Content)
	}

	// The overlapping checkpoint should remain (shifted).
	if len(outCps) != 1 {
		t.Errorf("expected 1 remaining checkpoint, got %d", len(outCps))
	}
	// Original [2,4] covered Q2(2), A2(3), Q3(4).
	// Q2 was consumed by cp1 [0,2], so trimmed to [3,4] (A2, Q3).
	// Consumed range [0,2] removed 2 messages (3->1), so shift by 2.
	// Shifted: [3-2, 4-2] = [1, 2].
	if outCps[0].StartIndex != 1 || outCps[0].EndIndex != 2 {
		t.Errorf("expected shifted [1,2], got [%d,%d]", outCps[0].StartIndex, outCps[0].EndIndex)
	}
}

func TestBuildCheckpointCompactedMessages_ConsecutiveAssistantMerge(t *testing.T) {
	// Two assistant messages in the original array that become adjacent
	// after compaction (no checkpoint covers them). Phase 3 should merge them.
	msgs := []Message{
		{Role: "user", Content: "Q1"},
		{Role: "assistant", Content: "A1"},
		{Role: "assistant", Content: "A2"}, // consecutive assistant — unusual but possible
		{Role: "user", Content: "Q3"},
		{Role: "assistant", Content: "A3"},
	}
	// Consume only [0,0], leaving [1,2] as consecutive assistants.
	checkpoints := []TurnCheckpoint{
		{StartIndex: 0, EndIndex: 0, Summary: "S1"},
	}

	outMsgs, outCps := BuildCheckpointCompactedMessages(msgs, checkpoints)

	// summary + merged assistant + user + assistant = 4 messages.
	if len(outMsgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(outMsgs))
	}
	// The two consecutive assistants should be merged.
	if outMsgs[1].Role != "assistant" {
		t.Errorf("expected assistant at index 1, got %s", outMsgs[1].Role)
	}
	if !strings.Contains(outMsgs[1].Content, "A1") || !strings.Contains(outMsgs[1].Content, "A2") {
		t.Errorf("expected merged content, got %q", outMsgs[1].Content)
	}
	if len(outCps) != 0 {
		t.Errorf("expected 0 remaining checkpoints, got %d", len(outCps))
	}
}

func TestIsConsumableCheckpoint_Valid(t *testing.T) {
	cp := TurnCheckpoint{StartIndex: 0, EndIndex: 1, Summary: "test"}
	if !isConsumableCheckpoint(cp, 5) {
		t.Error("expected consumable")
	}
}

func TestIsConsumableCheckpoint_EmptySummary(t *testing.T) {
	cp := TurnCheckpoint{StartIndex: 0, EndIndex: 1, Summary: ""}
	if isConsumableCheckpoint(cp, 5) {
		t.Error("expected not consumable due to empty summary")
	}
}

func TestIsConsumableCheckpoint_NegativeStart(t *testing.T) {
	cp := TurnCheckpoint{StartIndex: -1, EndIndex: 1, Summary: "test"}
	if isConsumableCheckpoint(cp, 5) {
		t.Error("expected not consumable due to negative start")
	}
}

func TestIsConsumableCheckpoint_NegativeEnd(t *testing.T) {
	cp := TurnCheckpoint{StartIndex: 0, EndIndex: -1, Summary: "test"}
	if isConsumableCheckpoint(cp, 5) {
		t.Error("expected not consumable due to negative end")
	}
}

func TestIsConsumableCheckpoint_StartGreaterThanEnd(t *testing.T) {
	cp := TurnCheckpoint{StartIndex: 2, EndIndex: 1, Summary: "test"}
	if isConsumableCheckpoint(cp, 5) {
		t.Error("expected not consumable due to start > end")
	}
}

func TestIsConsumableCheckpoint_EndOutOfBounds(t *testing.T) {
	cp := TurnCheckpoint{StartIndex: 0, EndIndex: 5, Summary: "test"}
	if isConsumableCheckpoint(cp, 5) {
		t.Error("expected not consumable due to end out of bounds")
	}
}

func TestIsConsumableCheckpoint_EndEqualsLenMinus1(t *testing.T) {
	cp := TurnCheckpoint{StartIndex: 4, EndIndex: 4, Summary: "test"}
	if !isConsumableCheckpoint(cp, 5) {
		t.Error("expected consumable — end is last valid index")
	}
}

// --- Restored tests from original_test.go ---

func TestBuildCheckpointSummary_SimpleTurn(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "What is the capital of France?"},
		{Role: "assistant", Content: "The capital of France is Paris."},
	}

	cp := BuildCheckpointSummary(messages)

	if cp.StartIndex != 0 {
		t.Errorf("expected StartIndex 0, got %d", cp.StartIndex)
	}
	if cp.EndIndex != 1 {
		t.Errorf("expected EndIndex 1, got %d", cp.EndIndex)
	}
	if !strings.Contains(cp.Summary, "User asked") {
		t.Errorf("summary should mention user question: %s", cp.Summary)
	}
	if !strings.Contains(cp.Summary, "Paris") {
		t.Errorf("summary should mention response content: %s", cp.Summary)
	}
	if !strings.Contains(cp.ActionableSummary, "Question") {
		t.Errorf("actionable summary should mention question: %s", cp.ActionableSummary)
	}
}

func TestBuildCheckpointSummary_WithToolCalls(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Read config.yaml"},
		{Role: "assistant", Content: "Let me read that file.", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{
				Name:      "read_file",
				Arguments: `{"path":"config.yaml"}`,
			}},
		}},
		{Role: "tool", Content: "host: localhost\nport: 5432", ToolCallID: "call_1"},
		{Role: "assistant", Content: "The config has host localhost on port 5432."},
	}

	cp := BuildCheckpointSummary(messages)

	if !strings.Contains(cp.Summary, "read_file") {
		t.Errorf("summary should mention read_file tool: %s", cp.Summary)
	}
	if !strings.Contains(cp.Summary, "config.yaml") {
		t.Errorf("summary should mention config.yaml: %s", cp.Summary)
	}
	if !strings.Contains(cp.ActionableSummary, "Read: config.yaml") {
		t.Errorf("actionable summary should list file read: %s", cp.ActionableSummary)
	}
}

func TestBuildCheckpointSummary_WithFileWrites(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Create a new file"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{
				Name:      "write_file",
				Arguments: `{"path":"output.txt","content":"hello"}`,
			}},
		}},
		{Role: "tool", Content: "File written successfully", ToolCallID: "call_1"},
		{Role: "assistant", Content: "File created."},
	}

	cp := BuildCheckpointSummary(messages)

	if !strings.Contains(cp.Summary, "Modified files") {
		t.Errorf("summary should mention modified files: %s", cp.Summary)
	}
	if !strings.Contains(cp.ActionableSummary, "Modified: output.txt") {
		t.Errorf("actionable summary should list modified file: %s", cp.ActionableSummary)
	}
}

func TestBuildCheckpointSummary_WithShellCommands(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "List files in directory"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{
				Name:      "shell",
				Arguments: `{"cmd":"ls -la /tmp"}`,
			}},
		}},
		{Role: "tool", Content: "total 8\ndrwxrwxrwt 2 root root 4096 Jan 1 00:00 .", ToolCallID: "call_1"},
		{Role: "assistant", Content: "Directory listing shows 8 total items."},
	}

	cp := BuildCheckpointSummary(messages)

	if !strings.Contains(cp.Summary, "shell") {
		t.Errorf("summary should mention shell tool: %s", cp.Summary)
	}
	if !strings.Contains(cp.Summary, "ls -la /tmp") {
		t.Errorf("summary should mention command: %s", cp.Summary)
	}
	if !strings.Contains(cp.ActionableSummary, "Command: ls -la /tmp") {
		t.Errorf("actionable summary should list command: %s", cp.ActionableSummary)
	}
}

func TestBuildCheckpointSummary_WithError(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Read missing file"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{
				Name:      "read_file",
				Arguments: `{"path":"nonexistent.txt"}`,
			}},
		}},
		{Role: "tool", Content: "error: file not found", ToolCallID: "call_1"},
		{Role: "assistant", Content: "The file doesn't exist."},
	}

	cp := BuildCheckpointSummary(messages)

	if !strings.Contains(cp.Summary, "error") {
		t.Errorf("summary should mention errors: %s", cp.Summary)
	}
	if !strings.Contains(cp.ActionableSummary, "Error") {
		t.Errorf("actionable summary should mention error: %s", cp.ActionableSummary)
	}
}

func TestBuildCheckpointSummary_MultipleToolCalls(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Read two files"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{
				Name:      "read_file",
				Arguments: `{"path":"a.txt"}`,
			}},
			{ID: "call_2", Function: ToolCallFunction{
				Name:      "read_file",
				Arguments: `{"path":"b.txt"}`,
			}},
		}},
		{Role: "tool", Content: "content a", ToolCallID: "call_1"},
		{Role: "tool", Content: "content b", ToolCallID: "call_2"},
		{Role: "assistant", Content: "Both files read successfully."},
	}

	cp := BuildCheckpointSummary(messages)

	// Should mention read_file (2x)
	if !strings.Contains(cp.Summary, "read_file") {
		t.Errorf("summary should mention read_file: %s", cp.Summary)
	}
	// Should list both files in actionable summary
	if !strings.Contains(cp.ActionableSummary, "Read: a.txt") {
		t.Errorf("actionable summary should list a.txt: %s", cp.ActionableSummary)
	}
	if !strings.Contains(cp.ActionableSummary, "Read: b.txt") {
		t.Errorf("actionable summary should list b.txt: %s", cp.ActionableSummary)
	}
}

func TestBuildCheckpointSummary_TruncatedResponse(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Explain something"},
		{Role: "assistant", Content: "Here is the explanation..."},
	}

	cp := BuildCheckpointSummary(messages)

	// Partial status due to trailing "..."
	if !strings.Contains(cp.Summary, "partial") {
		t.Errorf("summary should indicate partial status: %s", cp.Summary)
	}
}

func TestBuildCheckpointSummary_LongResponse(t *testing.T) {
	longContent := strings.Repeat("word ", 200) + "end"
	messages := []Message{
		{Role: "user", Content: "Long question"},
		{Role: "assistant", Content: longContent},
	}

	cp := BuildCheckpointSummary(messages)

	// Summary should be truncated
	if len(cp.Summary) > 500 {
		t.Errorf("summary should be reasonably short, got %d chars", len(cp.Summary))
	}
	// Actionable summary response should be truncated
	if !strings.Contains(cp.ActionableSummary, "...") {
		t.Errorf("actionable summary should truncate long response: %s", cp.ActionableSummary)
	}
}

func TestBuildCheckpointSummary_MixedOperations(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Refactor the code"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{
				Name:      "read_file",
				Arguments: `{"path":"src/main.go"}`,
			}},
		}},
		{Role: "tool", Content: "package main\nfunc main() {}", ToolCallID: "call_1"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_2", Function: ToolCallFunction{
				Name:      "edit_file",
				Arguments: `{"path":"src/main.go","old_str":"func main() {}","new_str":"func main() { println(\"hi\") }"}`,
			}},
		}},
		{Role: "tool", Content: "File edited successfully", ToolCallID: "call_2"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_3", Function: ToolCallFunction{
				Name:      "shell",
				Arguments: `{"cmd":"go build ./..."}`,
			}},
		}},
		{Role: "tool", Content: "Build succeeded", ToolCallID: "call_3"},
		{Role: "assistant", Content: "Refactored and verified the build."},
	}

	cp := BuildCheckpointSummary(messages)

	// Should mention reading and modifying files
	if !strings.Contains(cp.Summary, "Read files") {
		t.Errorf("summary should mention file reads: %s", cp.Summary)
	}
	if !strings.Contains(cp.Summary, "Modified files") {
		t.Errorf("summary should mention file modifications: %s", cp.Summary)
	}
	if !strings.Contains(cp.Summary, "shell") {
		t.Errorf("summary should mention shell command: %s", cp.Summary)
	}

	// Actionable summary should list operations
	if !strings.Contains(cp.ActionableSummary, "Read: src/main.go") {
		t.Errorf("actionable summary should list file read: %s", cp.ActionableSummary)
	}
	if !strings.Contains(cp.ActionableSummary, "Modified: src/main.go") {
		t.Errorf("actionable summary should list file modification: %s", cp.ActionableSummary)
	}
	if !strings.Contains(cp.ActionableSummary, "Command: go build ./...") {
		t.Errorf("actionable summary should list command: %s", cp.ActionableSummary)
	}
}

func TestBuildCheckpointSummary_EmptyMessages(t *testing.T) {
	cp := BuildCheckpointSummary([]Message{})

	if cp.StartIndex != 0 {
		t.Errorf("expected StartIndex 0, got %d", cp.StartIndex)
	}
	if cp.EndIndex != -1 {
		t.Errorf("expected EndIndex -1 for empty, got %d", cp.EndIndex)
	}
}

func TestBuildCheckpointSummary_OnlyUserMessage(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Hello"},
	}

	cp := BuildCheckpointSummary(messages)

	if !strings.Contains(cp.Summary, "User asked") {
		t.Errorf("summary should mention user question: %s", cp.Summary)
	}
}

func TestTurnSummaryBuilder_CustomFileTools(t *testing.T) {
	builder := NewTurnSummaryBuilder()
	builder.KnownFileTools = map[string]bool{
		"custom_read": true,
	}

	messages := []Message{
		{Role: "user", Content: "Read with custom tool"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{
				Name:      "custom_read",
				Arguments: `{"path":"custom.txt"}`,
			}},
		}},
		{Role: "tool", Content: "content", ToolCallID: "call_1"},
		{Role: "assistant", Content: "Done."},
	}

	cp := builder.Build(messages)

	if !strings.Contains(cp.Summary, "custom.txt") {
		t.Errorf("summary should mention custom file: %s", cp.Summary)
	}
}

func TestExtractFilePath(t *testing.T) {
	tests := []struct {
		name     string
		args     string
		expected string
	}{
		{"path key", `{"path":"src/main.go"}`, "src/main.go"},
		{"file key", `{"file":"data.json"}`, "data.json"},
		{"filename key", `{"filename":"test.txt"}`, "test.txt"},
		{"no path", `{"cmd":"ls"}`, ""},
		{"empty", `{}`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractFilePath(tt.args)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestExtractShellCommand(t *testing.T) {
	tests := []struct {
		name     string
		args     string
		expected string
	}{
		{"cmd key", `{"cmd":"ls -la"}`, "ls -la"},
		{"command key", `{"command":"go build"}`, "go build"},
		{"no command", `{"path":"file.txt"}`, ""},
		{"empty", `{}`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractShellCommand(tt.args)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{"short", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"truncated", "hello world foo bar", 10, "hello..."},
		{"truncated mid-word", "abcdefghij", 5, "abcde..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateString(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestUniqueStrings(t *testing.T) {
	input := []string{"a", "b", "a", "c", "b"}
	expected := []string{"a", "b", "c"}

	result := uniqueStrings(input)
	if len(result) != len(expected) {
		t.Fatalf("expected %d items, got %d", len(expected), len(result))
	}
	for i, v := range result {
		if v != expected[i] {
			t.Errorf("index %d: expected %q, got %q", i, expected[i], v)
		}
	}
}

func TestRecordTurnCheckpointAsync(t *testing.T) {
	state := NewState()

	messages := []Message{
		{Role: "user", Content: "Test query"},
		{Role: "assistant", Content: "Test response."},
	}

	RecordTurnCheckpointAsync(state, messages, 0, 1, 5*time.Second)

	// Wait for async completion
	time.Sleep(100 * time.Millisecond)

	checkpoints := state.GetCheckpoints()
	if len(checkpoints) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(checkpoints))
	}

	cp := checkpoints[0]
	if cp.StartIndex != 0 {
		t.Errorf("expected StartIndex 0, got %d", cp.StartIndex)
	}
	if cp.EndIndex != 1 {
		t.Errorf("expected EndIndex 1, got %d", cp.EndIndex)
	}
	if !strings.Contains(cp.Summary, "Test query") {
		t.Errorf("summary should mention query: %s", cp.Summary)
	}
}

func TestRecordTurnCheckpointAsync_Timeout(t *testing.T) {
	state := NewState()

	// Very large message set to test timeout path
	messages := make([]Message, 0, 1000)
	for i := 0; i < 500; i++ {
		messages = append(messages, Message{Role: "user", Content: strings.Repeat("x", 1000)})
		messages = append(messages, Message{Role: "assistant", Content: strings.Repeat("y", 1000)})
	}

	RecordTurnCheckpointAsync(state, messages, 0, len(messages)-1, 1*time.Millisecond)

	// Wait for async completion
	time.Sleep(100 * time.Millisecond)

	checkpoints := state.GetCheckpoints()
	if len(checkpoints) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(checkpoints))
	}

	// Even on timeout, checkpoint should be stored
	cp := checkpoints[0]
	if cp.StartIndex != 0 {
		t.Errorf("expected StartIndex 0, got %d", cp.StartIndex)
	}
}

func TestRecordTurnCheckpointAsync_SnapshotIsolation(t *testing.T) {
	state := NewState()

	messages := []Message{
		{Role: "user", Content: "Original query"},
		{Role: "assistant", Content: "Original response."},
	}

	RecordTurnCheckpointAsync(state, messages, 0, 1, 5*time.Second)

	// Mutate the original slice immediately after calling.
	// The async goroutine should see the snapshot, not these mutations.
	messages[0].Content = "Mutated query"
	messages[1].Content = "Mutated response."
	messages = append(messages, Message{Role: "user", Content: "Extra"})

	// Wait for async completion.
	time.Sleep(200 * time.Millisecond)

	checkpoints := state.GetCheckpoints()
	if len(checkpoints) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(checkpoints))
	}

	cp := checkpoints[0]
	// Summary should reference the original content, not the mutated content.
	if strings.Contains(cp.Summary, "Mutated") {
		t.Errorf("summary should use snapshot, not mutated data: %s", cp.Summary)
	}
	if !strings.Contains(cp.Summary, "Original") {
		t.Errorf("summary should contain original content: %s", cp.Summary)
	}
}

func TestIsFileWriteTool(t *testing.T) {
	writeTools := []string{"write_file", "edit_file", "create_file", "delete_file", "patch_file", "append_file", "write_structured", "patch_structured"}
	readTools := []string{"read_file", "list_files", "search_files", "glob_files"}

	for _, tool := range writeTools {
		if !isFileWriteTool(tool) {
			t.Errorf("expected %s to be a write tool", tool)
		}
	}
	for _, tool := range readTools {
		if isFileWriteTool(tool) {
			t.Errorf("expected %s to NOT be a write tool", tool)
		}
	}
}

func TestDetermineStatus(t *testing.T) {
	builder := NewTurnSummaryBuilder()

	// Completed status
	completedData := turnData{
		userQuestion:  "What is 2+2?",
		finalResponse: "The answer is 4.",
	}
	if builder.determineStatus(completedData) != statusCompleted {
		t.Errorf("expected completed status, got %v", builder.determineStatus(completedData))
	}

	// Partial status (trailing ...)
	partialData := turnData{
		userQuestion:  "Explain",
		finalResponse: "Here is the explanation...",
	}
	if builder.determineStatus(partialData) != statusPartial {
		t.Errorf("expected partial status, got %v", builder.determineStatus(partialData))
	}

	// Interrupted status (no final response but tool calls)
	interruptedData := turnData{
		userQuestion: "Do something",
		toolCalls:    []toolCallInfo{{name: "shell", arguments: `{"cmd":"ls"}`}},
	}
	if builder.determineStatus(interruptedData) != statusInterrupted {
		t.Errorf("expected interrupted status, got %v", builder.determineStatus(interruptedData))
	}

	// Error status (errors but no final response)
	errorData := turnData{
		userQuestion: "Fail",
		errors:       []string{"error: something failed"},
	}
	if builder.determineStatus(errorData) != statusError {
		t.Errorf("expected error status, got %v", builder.determineStatus(errorData))
	}
}

// --- ShiftCheckpointIndices tests ---

// TestShiftCheckpointIndices_NoChanges verifies that when oldMessages ==
// newMessages, checkpoint indices are returned unchanged.
func TestShiftCheckpointIndices_NoChanges(t *testing.T) {
	oldMessages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "What is 2+2?"},
		{Role: "assistant", Content: "The answer is 4."},
		{Role: "user", Content: "What is 3+3?"},
		{Role: "assistant", Content: "The answer is 6."},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 1, EndIndex: 2, Summary: "Sum 1"},
		{StartIndex: 3, EndIndex: 4, Summary: "Sum 2"},
	}

	result := ShiftCheckpointIndices(oldMessages, oldMessages, checkpoints)

	if len(result) != len(checkpoints) {
		t.Fatalf("expected %d checkpoints, got %d", len(checkpoints), len(result))
	}
	for i, cp := range result {
		if cp.StartIndex != checkpoints[i].StartIndex {
			t.Errorf("checkpoint %d: expected start %d, got %d", i, checkpoints[i].StartIndex, cp.StartIndex)
		}
		if cp.EndIndex != checkpoints[i].EndIndex {
			t.Errorf("checkpoint %d: expected end %d, got %d", i, checkpoints[i].EndIndex, cp.EndIndex)
		}
	}
}

// TestShiftCheckpointIndices_MessagesRemovedBefore verifies that checkpoints
// shift correctly when messages before them are removed. Only messages with
// matching role AND content are matched (score > 0 required).
func TestShiftCheckpointIndices_MessagesRemovedBefore(t *testing.T) {
	oldMessages := []Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "alpha query"},
		{Role: "assistant", Content: "alpha answer"},
		{Role: "user", Content: "beta query"},
		{Role: "assistant", Content: "beta answer"},
		{Role: "user", Content: "gamma query"},
		{Role: "assistant", Content: "gamma answer"},
	}
	// System + gamma turn survive (alpha and beta turns removed).
	newMessages := []Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "gamma query"},
		{Role: "assistant", Content: "gamma answer"},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 5, EndIndex: 6, Summary: "Sum for gamma"},
	}

	result := ShiftCheckpointIndices(oldMessages, newMessages, checkpoints)

	if len(result) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(result))
	}
	// Greedy matching (score > 0 required):
	// old[0] sys "system prompt" → new[0] score=100
	// old[1] user "alpha query" → no match (content differs from new[1]) → -1
	// old[2] assistant "alpha answer" → no match → -1
	// old[3] user "beta query" → no match → -1
	// old[4] assistant "beta answer" → no match → -1
	// old[5] user "gamma query" → new[1] score=100
	// old[6] assistant "gamma answer" → new[2] score=100
	// matched = [0, -1, -1, -1, -1, 1, 2]
	// Checkpoint [5,6]: startNew=1, endNew=2 → [1,2]
	if result[0].StartIndex != 1 {
		t.Errorf("expected StartIndex 1, got %d", result[0].StartIndex)
	}
	if result[0].EndIndex != 2 {
		t.Errorf("expected EndIndex 2, got %d", result[0].EndIndex)
	}
}

// TestShiftCheckpointIndices_MessagesRemovedAfter verifies that checkpoints
// for messages that were removed are marked invalid, while surviving ones
// keep their indices.
func TestShiftCheckpointIndices_MessagesRemovedAfter(t *testing.T) {
	oldMessages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Question 1"},
		{Role: "assistant", Content: "Answer 1"},
		{Role: "user", Content: "Question 2"},
		{Role: "assistant", Content: "Answer 2"},
	}
	// System, Q1, A1 survive (tail removed).
	newMessages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Question 1"},
		{Role: "assistant", Content: "Answer 1"},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 1, EndIndex: 2, Summary: "Sum for Q1/A1"},
		{StartIndex: 3, EndIndex: 4, Summary: "Sum for Q2/A2"},
	}

	result := ShiftCheckpointIndices(oldMessages, newMessages, checkpoints)

	if len(result) != 2 {
		t.Fatalf("expected 2 checkpoints, got %d", len(result))
	}
	// CP1 survived: [1,2] → [1,2]
	if result[0].StartIndex != 1 || result[0].EndIndex != 2 {
		t.Errorf("CP1: expected [1,2], got [%d,%d]", result[0].StartIndex, result[0].EndIndex)
	}
	// CP2 fully consumed: [3,4] → [-1,-1]
	if result[1].StartIndex != -1 || result[1].EndIndex != -1 {
		t.Errorf("CP2: expected [-1,-1], got [%d,%d]", result[1].StartIndex, result[1].EndIndex)
	}
}

// TestShiftCheckpointIndices_PartiallyConsumed verifies that when a checkpoint
// range contains both surviving and removed messages, the range is trimmed
// to only the surviving messages.
func TestShiftCheckpointIndices_PartiallyConsumed(t *testing.T) {
	oldMessages := []Message{
		{Role: "user", Content: "alpha query"},
		{Role: "assistant", Content: "alpha answer"},
		{Role: "user", Content: "beta query"},
		{Role: "assistant", Content: "beta answer"},
		{Role: "user", Content: "gamma query"},
		{Role: "assistant", Content: "gamma answer"},
	}
	// alpha query and gamma answer survive (middle removed).
	newMessages := []Message{
		{Role: "user", Content: "alpha query"},
		{Role: "assistant", Content: "gamma answer"},
	}
	// Checkpoint covers [0,3]: alpha query(0), alpha answer(1), beta query(2), beta answer(3).
	// Greedy matching:
	//   old[0] user "alpha query" → new[0] score=100
	//   old[1] assistant "alpha answer" → no match → -1
	//   old[2] user "beta query" → no match → -1
	//   old[3] assistant "beta answer" → no match → -1
	//   old[4] user "gamma query" → no match → -1
	//   old[5] assistant "gamma answer" → new[1] score=100
	// matched = [0, -1, -1, -1, -1, 1]
	//
	// Checkpoint [0,3]: startNew=0 (survived), endNew=-1 (removed)
	// Partially consumed: scan range [0,3], only old[0] survived → [0, 0]
	checkpoints := []TurnCheckpoint{
		{StartIndex: 0, EndIndex: 3, Summary: "Sum for first half"},
	}

	result := ShiftCheckpointIndices(oldMessages, newMessages, checkpoints)

	if len(result) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(result))
	}
	// Only old[0] survived in range [0,3]. firstSurviving=0, lastSurviving=0.
	if result[0].StartIndex != 0 {
		t.Errorf("expected StartIndex 0, got %d", result[0].StartIndex)
	}
	if result[0].EndIndex != 0 {
		t.Errorf("expected EndIndex 0, got %d", result[0].EndIndex)
	}
}

// TestShiftCheckpointIndices_MultipleCheckpoints verifies correct handling
// of multiple checkpoints where different ones survive or get consumed.
func TestShiftCheckpointIndices_MultipleCheckpoints(t *testing.T) {
	oldMessages := []Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "alpha query"},
		{Role: "assistant", Content: "alpha answer"},
		{Role: "user", Content: "beta query"},
		{Role: "assistant", Content: "beta answer"},
		{Role: "user", Content: "gamma query"},
		{Role: "assistant", Content: "gamma answer"},
	}
	// System + beta turn + gamma turn survive (alpha turn removed).
	newMessages := []Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "beta query"},
		{Role: "assistant", Content: "beta answer"},
		{Role: "user", Content: "gamma query"},
		{Role: "assistant", Content: "gamma answer"},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 1, EndIndex: 2, Summary: "Sum for alpha"}, // fully consumed
		{StartIndex: 3, EndIndex: 4, Summary: "Sum for beta"},  // shifted to [1,2]
		{StartIndex: 5, EndIndex: 6, Summary: "Sum for gamma"}, // shifted to [3,4]
	}

	result := ShiftCheckpointIndices(oldMessages, newMessages, checkpoints)

	if len(result) != 3 {
		t.Fatalf("expected 3 checkpoints, got %d", len(result))
	}
	// Greedy matching:
	// old[0] sys "system prompt" → new[0] score=100
	// old[1] user "alpha query" → no match → -1
	// old[2] assistant "alpha answer" → no match → -1
	// old[3] user "beta query" → new[1] score=100
	// old[4] assistant "beta answer" → new[2] score=100
	// old[5] user "gamma query" → new[3] score=100
	// old[6] assistant "gamma answer" → new[4] score=100
	// matched = [0, -1, -1, 1, 2, 3, 4]
	//
	// CP1 [1,2]: both -1 → fully consumed → [-1,-1]
	// CP2 [3,4]: startNew=1, endNew=2 → [1,2]
	// CP3 [5,6]: startNew=3, endNew=4 → [3,4]
	if result[0].StartIndex != -1 || result[0].EndIndex != -1 {
		t.Errorf("CP1: expected [-1,-1], got [%d,%d]", result[0].StartIndex, result[0].EndIndex)
	}
	if result[1].StartIndex != 1 || result[1].EndIndex != 2 {
		t.Errorf("CP2: expected [1,2], got [%d,%d]", result[1].StartIndex, result[1].EndIndex)
	}
	if result[2].StartIndex != 3 || result[2].EndIndex != 4 {
		t.Errorf("CP3: expected [3,4], got [%d,%d]", result[2].StartIndex, result[2].EndIndex)
	}
} // TestShiftCheckpointIndices_EmptyInputs verifies edge cases with empty slices.
func TestShiftCheckpointIndices_EmptyInputs(t *testing.T) {
	// Empty oldMessages → checkpoints returned unchanged.
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 0, EndIndex: 1, Summary: "test"},
	}

	result1 := ShiftCheckpointIndices([]Message{}, msgs, checkpoints)
	if len(result1) != 1 {
		t.Errorf("empty oldMessages: expected 1 checkpoint, got %d", len(result1))
	}
	if result1[0].StartIndex != 0 || result1[0].EndIndex != 1 {
		t.Errorf("empty oldMessages: expected [0,1], got [%d,%d]", result1[0].StartIndex, result1[0].EndIndex)
	}

	// Empty newMessages → checkpoints returned unchanged.
	result2 := ShiftCheckpointIndices(msgs, []Message{}, checkpoints)
	if len(result2) != 1 {
		t.Errorf("empty newMessages: expected 1 checkpoint, got %d", len(result2))
	}
	if result2[0].StartIndex != 0 || result2[0].EndIndex != 1 {
		t.Errorf("empty newMessages: expected [0,1], got [%d,%d]", result2[0].StartIndex, result2[0].EndIndex)
	}

	// Empty checkpoints → empty result.
	result3 := ShiftCheckpointIndices(msgs, msgs, []TurnCheckpoint{})
	if len(result3) != 0 {
		t.Errorf("empty checkpoints: expected 0, got %d", len(result3))
	}

	// All three empty → empty result.
	result4 := ShiftCheckpointIndices([]Message{}, []Message{}, []TurnCheckpoint{})
	if len(result4) != 0 {
		t.Errorf("all empty: expected 0, got %d", len(result4))
	}
}

// TestShiftCheckpointIndices_WithToolCalls verifies that matching considers
// tool_call_id for tool result messages and tool calls for assistant messages.
// Note: The greedy matching in matchMessages uses score 0 for content mismatches,
// and since 0 > -1, it will match by role even when content differs. This test
// uses unique content to demonstrate correct behavior with no false matches.
func TestShiftCheckpointIndices_WithToolCalls(t *testing.T) {
	oldMessages := []Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "List files please"},
		{Role: "assistant", Content: "Let me list them.", ToolCalls: []ToolCall{
			{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "list_files", Arguments: `{}`}},
		}},
		{Role: "tool", Content: "file1.txt, file2.txt", ToolCallID: "call_1"},
		{Role: "assistant", Content: "Done listing files.", ToolCalls: []ToolCall{
			{ID: "call_2", Type: "function", Function: ToolCallFunction{Name: "read_file", Arguments: `{"path":"file1.txt"}`}},
		}},
		{Role: "tool", Content: "file content here", ToolCallID: "call_2"},
	}
	// system + final assistant survives (tool turn replaced by summary).
	newMessages := []Message{
		{Role: "system", Content: "system prompt"},
		{Role: "assistant", Content: "Done listing files.", ToolCalls: []ToolCall{
			{ID: "call_2", Type: "function", Function: ToolCallFunction{Name: "read_file", Arguments: `{"path":"file1.txt"}`}},
		}},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 2, EndIndex: 3, Summary: "Sum for tool call"},
	}

	result := ShiftCheckpointIndices(oldMessages, newMessages, checkpoints)

	if len(result) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(result))
	}
	// old[2]="Let me list them." (assistant) → -1 (no unmatched new assistant)
	// old[3]="file1.txt, file2.txt" (tool) → -1 (no unmatched new tool)
	// Both indices in checkpoint range map to -1 → fully consumed
	if result[0].StartIndex != -1 || result[0].EndIndex != -1 {
		t.Errorf("expected [-1,-1], got [%d,%d]", result[0].StartIndex, result[0].EndIndex)
	}
}

// TestShiftCheckpointIndices_DuplicateContent verifies that the greedy
// matching algorithm correctly handles multiple messages with identical content.
// When multiple old messages have the same content, they match in order:
// the first unmatched old message matches the first unmatched new message.
func TestShiftCheckpointIndices_DuplicateContent(t *testing.T) {
	oldMessages := []Message{
		{Role: "system", Content: "system prompt"},
		{Role: "tool", Content: "OK"},
		{Role: "tool", Content: "OK"},
		{Role: "tool", Content: "OK"},
		{Role: "user", Content: "Final question"},
	}
	// Two tool results removed, system, remaining "OK", and user survive.
	newMessages := []Message{
		{Role: "system", Content: "system prompt"},
		{Role: "tool", Content: "OK"},
		{Role: "user", Content: "Final question"},
	}
	// Greedy matching (score > 0 required):
	//   old[0]="system prompt"(system) → new[0] score=100
	//   old[1]="OK"(tool) → new[1] score=100 (exact match)
	//   old[2]="OK"(tool) → new[1] taken; new[2]="Final question"(user) role≠tool → score 0, skip
	//     no more unmatched → -1
	//   old[3]="OK"(tool) → no unmatched tool → -1
	//   old[4]="Final question"(user) → new[2] score=100
	//   matched = [0, 1, -1, -1, 2]
	//
	// Checkpoint [1,2]: old[1]→new[1] (surviving), old[2]→-1 (removed).
	// Partially consumed: scan [1,2], only old[1] survived → [1, 1].
	checkpoints := []TurnCheckpoint{
		{StartIndex: 1, EndIndex: 2, Summary: "Sum for two OKs"},
	}

	result := ShiftCheckpointIndices(oldMessages, newMessages, checkpoints)

	if len(result) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(result))
	}
	// First "OK" (old[1]) matches new[1] with score 100.
	// Second "OK" (old[2]) has no unmatched new message with score > 0 → -1.
	// Partially consumed: firstSurviving=1, lastSurviving=1 → [1, 1].
	if result[0].StartIndex != 1 {
		t.Errorf("expected StartIndex 1, got %d", result[0].StartIndex)
	}
	if result[0].EndIndex != 1 {
		t.Errorf("expected EndIndex 1, got %d", result[0].EndIndex)
	}
}

// TestShiftCheckpointIndices_OutOfBoundsIndices verifies that checkpoints
// with invalid indices (out of bounds or negative) are preserved as-is.
func TestShiftCheckpointIndices_OutOfBoundsIndices(t *testing.T) {
	oldMessages := []Message{
		{Role: "user", Content: "Question 1"},
		{Role: "assistant", Content: "Answer 1"},
	}
	newMessages := []Message{
		{Role: "user", Content: "Question 1"},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 5, EndIndex: 6, Summary: "out of bounds start"},
		{StartIndex: -1, EndIndex: 0, Summary: "negative start"},
		{StartIndex: 0, EndIndex: 0, Summary: "valid"},
	}

	result := ShiftCheckpointIndices(oldMessages, newMessages, checkpoints)

	if len(result) != 3 {
		t.Fatalf("expected 3 checkpoints, got %d", len(result))
	}
	// CP0: start index 5 >= len(oldMessages)=2, preserved as-is.
	if result[0].StartIndex != 5 || result[0].EndIndex != 6 {
		t.Errorf("CP0: expected [5,6], got [%d,%d]", result[0].StartIndex, result[0].EndIndex)
	}
	// CP1: negative start, preserved as-is.
	if result[1].StartIndex != -1 || result[1].EndIndex != 0 {
		t.Errorf("CP1: expected [-1,0], got [%d,%d]", result[1].StartIndex, result[1].EndIndex)
	}
	// CP2: valid → [0,0] → [0,0] (survived).
	if result[2].StartIndex != 0 || result[2].EndIndex != 0 {
		t.Errorf("CP2: expected [0,0], got [%d,%d]", result[2].StartIndex, result[2].EndIndex)
	}
}

// TestShiftCheckpointIndices_AllMessagesRemoved verifies that when all
// old messages in a checkpoint range are replaced by new messages with
// completely different structure, all checkpoint indices are marked invalid.
//
// With bestScore = 0, only scores > 0 are accepted, meaning role AND content
// must agree for a match. If no old message matches any new message, all
// map to -1.
func TestShiftCheckpointIndices_AllMessagesRemoved(t *testing.T) {
	// Old has 4 messages, new has only 2. Content differs across all messages,
	// so no old message matches any new message (score 0 is not accepted).
	oldMessages := []Message{
		{Role: "user", Content: "question one"},
		{Role: "assistant", Content: "answer one"},
		{Role: "tool", Content: "tool result 1"},
		{Role: "tool", Content: "tool result 2"},
	}
	newMessages := []Message{
		{Role: "user", Content: "summary user"},
		{Role: "assistant", Content: "summary assistant"},
	}
	// Greedy matching (score > 0 required):
	//   old[0] user "question one" → new[0] user "summary user" score=0 (content differs) → skip
	//     new[1] assistant "summary assistant" score=0 (role differs) → skip
	//     no match → -1
	//   old[1] assistant "answer one" → no match → -1
	//   old[2] tool "tool result 1" → no match → -1
	//   old[3] tool "tool result 2" → no match → -1
	//   matched = [-1, -1, -1, -1]
	//
	// Checkpoint [2,3]: both boundaries unmatched → [-1,-1]
	checkpoints := []TurnCheckpoint{
		{StartIndex: 2, EndIndex: 3, Summary: "Sum for tool results"},
	}

	result := ShiftCheckpointIndices(oldMessages, newMessages, checkpoints)

	if len(result) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(result))
	}
	// All old messages have no match → fully consumed
	if result[0].StartIndex != -1 || result[0].EndIndex != -1 {
		t.Errorf("expected [-1,-1], got [%d,%d]", result[0].StartIndex, result[0].EndIndex)
	}
}

// TestShiftCheckpointIndices_CheckpointNotMutated verifies that the
// input checkpoints slice is not mutated by the function.
func TestShiftCheckpointIndices_CheckpointNotMutated(t *testing.T) {
	oldMessages := []Message{
		{Role: "user", Content: "Question 1"},
		{Role: "assistant", Content: "Answer 1"},
		{Role: "user", Content: "Question 2"},
		{Role: "assistant", Content: "Answer 2"},
	}
	// Remove first turn, keep second.
	newMessages := []Message{
		{Role: "user", Content: "Question 2"},
		{Role: "assistant", Content: "Answer 2"},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 0, EndIndex: 1, Summary: "Sum for Q1/A1"},
		{StartIndex: 2, EndIndex: 3, Summary: "Sum for Q2/A2"},
	}

	// Snapshot original checkpoints for comparison.
	origCps := make([]TurnCheckpoint, len(checkpoints))
	copy(origCps, checkpoints)

	_ = ShiftCheckpointIndices(oldMessages, newMessages, checkpoints)

	// Verify the input checkpoints were not mutated.
	for i, cp := range checkpoints {
		if cp.StartIndex != origCps[i].StartIndex {
			t.Errorf("checkpoint %d StartIndex was mutated: %d → %d", i, origCps[i].StartIndex, cp.StartIndex)
		}
		if cp.EndIndex != origCps[i].EndIndex {
			t.Errorf("checkpoint %d EndIndex was mutated: %d → %d", i, origCps[i].EndIndex, cp.EndIndex)
		}
		if cp.Summary != origCps[i].Summary {
			t.Errorf("checkpoint %d Summary was mutated", i)
		}
	}
}

// TestMatchMessages_PerfectMatch verifies a perfect match returns score 100.
func TestMatchMessages_PerfectMatch(t *testing.T) {
	old := Message{Role: "user", Content: "hello", ToolCallID: "id1", ToolCalls: []ToolCall{
		{ID: "tc1", Function: ToolCallFunction{Name: "shell"}},
	}}
	new := Message{Role: "user", Content: "hello", ToolCallID: "id1", ToolCalls: []ToolCall{
		{ID: "tc1", Function: ToolCallFunction{Name: "shell"}},
	}}
	score := matchMessages(old, new)
	if score != 100 {
		t.Errorf("expected score 100, got %d", score)
	}
}

// TestMatchMessages_PartialMatch verifies a partial match (same role/content,
// different tool_call_id) returns score 50.
func TestMatchMessages_PartialMatch(t *testing.T) {
	old := Message{Role: "tool", Content: "result", ToolCallID: "id1"}
	new := Message{Role: "tool", Content: "result", ToolCallID: "id2"}
	score := matchMessages(old, new)
	if score != 50 {
		t.Errorf("expected score 50, got %d", score)
	}
}

// TestMatchMessages_DifferentRole verifies that different roles return score 0.
func TestMatchMessages_DifferentRole(t *testing.T) {
	old := Message{Role: "user", Content: "hello"}
	new := Message{Role: "assistant", Content: "hello"}
	score := matchMessages(old, new)
	if score != 0 {
		t.Errorf("expected score 0, got %d", score)
	}
}

// TestMatchMessages_DifferentContent verifies that different content returns score 0.
func TestMatchMessages_DifferentContent(t *testing.T) {
	old := Message{Role: "user", Content: "hello"}
	new := Message{Role: "user", Content: "world"}
	score := matchMessages(old, new)
	if score != 0 {
		t.Errorf("expected score 0, got %d", score)
	}
}

// TestMatchMessages_EmptyMessages verifies that empty messages return score 100
// (empty role and empty content match each other).
func TestMatchMessages_EmptyMessages(t *testing.T) {
	old := Message{}
	new := Message{}
	score := matchMessages(old, new)
	if score != 100 {
		t.Errorf("expected score 100, got %d", score)
	}
}

// TestShiftCheckpointIndices_SingleMessageTurn verifies correct shifting
// when checkpoint covers a single message that survives.
func TestShiftCheckpointIndices_SingleMessageTurn(t *testing.T) {
	oldMessages := []Message{
		{Role: "user", Content: "Question"},
		{Role: "assistant", Content: "Answer"},
	}
	newMessages := []Message{
		{Role: "user", Content: "Question"},
	}
	checkpoints := []TurnCheckpoint{
		{StartIndex: 1, EndIndex: 1, Summary: "Single msg checkpoint"},
	}

	result := ShiftCheckpointIndices(oldMessages, newMessages, checkpoints)

	if len(result) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(result))
	}
	// Assistant message (index 1) was removed, so checkpoint should be [-1,-1].
	if result[0].StartIndex != -1 || result[0].EndIndex != -1 {
		t.Errorf("expected [-1,-1], got [%d,%d]", result[0].StartIndex, result[0].EndIndex)
	}
}

// TestShiftCheckpointIndices_CheckpointSpanningMultipleNewMessages verifies
// that a checkpoint spanning multiple surviving messages gets its StartIndex
// and EndIndex correctly mapped.
func TestShiftCheckpointIndices_CheckpointSpanningMultipleNewMessages(t *testing.T) {
	oldMessages := []Message{
		{Role: "user", Content: "Q1"},
		{Role: "assistant", Content: "A1"},
		{Role: "user", Content: "Q2"},
		{Role: "assistant", Content: "A2"},
	}
	newMessages := []Message{
		{Role: "user", Content: "Q1"},
		{Role: "assistant", Content: "A1"},
		{Role: "user", Content: "Q2"},
		{Role: "assistant", Content: "A2"},
	}
	// Checkpoint spans the entire old array.
	checkpoints := []TurnCheckpoint{
		{StartIndex: 0, EndIndex: 3, Summary: "All messages"},
	}

	result := ShiftCheckpointIndices(oldMessages, newMessages, checkpoints)

	if len(result) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(result))
	}
	// All messages survived, indices unchanged.
	if result[0].StartIndex != 0 || result[0].EndIndex != 3 {
		t.Errorf("expected [0,3], got [%d,%d]", result[0].StartIndex, result[0].EndIndex)
	}
}
