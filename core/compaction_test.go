package core

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// --- Compaction tests ---

func TestCompact_NoOpWhenUnderLimit(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "Hello!"},
	}
	result := Compact(messages, 10000)
	if len(result.Messages) != len(messages) {
		t.Errorf("expected no compaction, got %d messages", len(result.Messages))
	}
}

func TestCompact_NoOpWhenTooFewMessages(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "Hello"},
	}
	result := Compact(messages, 10)
	if len(result.Messages) != len(messages) {
		t.Errorf("expected no compaction for < minMessages, got %d", len(result.Messages))
	}
}

func TestCompact_EmergencyTruncation(t *testing.T) {
	// Build messages with very long content — more than recentToKeep
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	// 30 pairs = 60 messages, well beyond recentToKeep=24
	for i := 0; i < 30; i++ {
		messages = append(messages, Message{
			Role:    "user",
			Content: "User " + strings.Repeat("a", 5000),
		})
		messages = append(messages, Message{
			Role:    "assistant",
			Content: "Assistant " + strings.Repeat("b", 5000),
		})
	}

	result := Compact(messages, 100)

	// Should have fewer messages
	if len(result.Messages) >= len(messages) {
		t.Errorf("expected message reduction, got %d (was %d)", len(result.Messages), len(messages))
	}

	// Should preserve system message
	if result.Messages[0].Role != "system" {
		t.Error("system message should be preserved")
	}

	// Token count should be significantly reduced
	origTokens := roughTokens(messages)
	newTokens := roughTokens(result.Messages)
	if newTokens >= origTokens {
		t.Errorf("expected token reduction: %d -> %d", origTokens, newTokens)
	}
}

func TestCompact_RoughTokens(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Hello world"},
	}
	tokens := roughTokens(messages)
	if tokens <= 0 {
		t.Errorf("expected positive token count, got %d", tokens)
	}
}

func TestCompact_TruncateHeadTail(t *testing.T) {
	s := strings.Repeat("a", 100)
	result := truncateHeadTail(s, 10, 10)
	if !strings.Contains(result, "[truncated]") {
		t.Error("expected truncation marker")
	}
	if !strings.HasPrefix(result, "aaaaaaaaaa") {
		t.Error("expected head preserved")
	}
	if !strings.HasSuffix(result, "aaaaaaaaaa") {
		t.Error("expected tail preserved")
	}
}

// --- CompactionResult metadata tests ---

func TestCompact_ResultMetadata_NoOp(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "Hello!"},
	}
	result := Compact(messages, 10000)

	if result.Strategy != "none" {
		t.Errorf("expected strategy 'none', got %q", result.Strategy)
	}
	if result.TokensBefore != result.TokensAfter {
		t.Errorf("expected TokensBefore (%d) == TokensAfter (%d)", result.TokensBefore, result.TokensAfter)
	}
	if result.TokensSaved() != 0 {
		t.Errorf("expected 0 tokens saved for no-op, got %d", result.TokensSaved())
	}
	if result.MessageCountDelta(len(messages)) != 0 {
		t.Errorf("expected 0 message delta for no-op, got %d", result.MessageCountDelta(len(messages)))
	}
}

func TestCompact_ResultMetadata_Truncation(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	for i := 0; i < 30; i++ {
		messages = append(messages, Message{
			Role:    "user",
			Content: "User " + strings.Repeat("a", 5000),
		})
		messages = append(messages, Message{
			Role:    "assistant",
			Content: "Assistant " + strings.Repeat("b", 5000),
		})
	}

	result := Compact(messages, 100)

	if result.Strategy != "truncation" {
		t.Errorf("expected strategy 'truncation' (content truncated, recent boundary protects all messages), got %q", result.Strategy)
	}
	if result.TokensSaved() <= 0 {
		t.Errorf("expected positive tokens saved, got %d", result.TokensSaved())
	}
	if result.MessageCountDelta(len(messages)) <= 0 {
		t.Errorf("expected positive message count delta, got %d", result.MessageCountDelta(len(messages)))
	}
}

func TestCompact_ResultMetadata_TokensSaved(t *testing.T) {
	// Test TokensSaved() helper
	noneResult := CompactionResult{
		Messages:     []Message{{Role: "user", Content: "hi"}},
		Strategy:     "none",
		TokensBefore: 100,
		TokensAfter:  100,
	}
	if noneResult.TokensSaved() != 0 {
		t.Errorf("expected 0 tokens saved when before == after, got %d", noneResult.TokensSaved())
	}

	// TokensAfter > TokensBefore should also return 0
	invalidResult := CompactionResult{
		Messages:     []Message{{Role: "user", Content: "hi"}},
		Strategy:     "none",
		TokensBefore: 100,
		TokensAfter:  150,
	}
	if invalidResult.TokensSaved() != 0 {
		t.Errorf("expected 0 tokens saved when after > before, got %d", invalidResult.TokensSaved())
	}

	// Normal reduction case
	reduceResult := CompactionResult{
		Messages:     []Message{{Role: "user", Content: "hi"}},
		Strategy:     "emergency",
		TokensBefore: 500,
		TokensAfter:  300,
	}
	if reduceResult.TokensSaved() != 200 {
		t.Errorf("expected 200 tokens saved, got %d", reduceResult.TokensSaved())
	}

	// Test MessageCountDelta() helper
	if noneResult.MessageCountDelta(1) != 0 {
		t.Errorf("expected 0 message delta, got %d", noneResult.MessageCountDelta(1))
	}
	if reduceResult.MessageCountDelta(10) != 9 {
		t.Errorf("expected 9 message delta, got %d", reduceResult.MessageCountDelta(10))
	}
}

// --- dropOldestCheckpointSummaries tests ---

func TestDropOldestCheckpointSummaries_DropsOldestFirst(t *testing.T) {
	// Build messages: system + 3 checkpoint summaries + recent messages
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Summary 1", Meta: map[string]string{MetaKeyCheckpoint: "true"}},
		{Role: "assistant", Content: "Response to summary 1", Meta: map[string]string{MetaKeyCheckpoint: "true"}},
		{Role: "user", Content: "Summary 2", Meta: map[string]string{MetaKeyCheckpoint: "true"}},
		{Role: "assistant", Content: "Response to summary 2"},
	}
	// Add enough recent messages to protect nothing in recent zone
	for i := 0; i < 30; i++ {
		messages = append(messages, Message{Role: "user", Content: strings.Repeat("x", 100)})
		messages = append(messages, Message{Role: "assistant", Content: strings.Repeat("y", 100)})
	}

	// Set targetTokens high enough that only some checkpoints are dropped
	tokensBefore := roughTokens(messages)
	// Each checkpoint message is ~10 tokens content + 10 overhead = ~20 tokens
	// Drop enough to remove 2 checkpoints but not 3
	targetTokens := tokensBefore - 30 // should drop 2 checkpoints (~40 tokens saved)

	result := DropOldestCheckpointSummaries(messages, targetTokens)

	// Should have dropped at least 2 checkpoint messages
	dropped := len(messages) - len(result)
	if dropped < 2 {
		t.Errorf("expected at least 2 checkpoints dropped, got %d", dropped)
	}

	// Verify dropped messages were checkpoint messages
	for _, msg := range result {
		if msg.Content == "Summary 1" || msg.Content == "Response to summary 1" {
			t.Error("oldest checkpoint messages should have been dropped")
		}
	}
}

func TestDropOldestCheckpointSummaries_RespectsRecentBoundary(t *testing.T) {
	// Checkpoint message within recentToKeep zone must not be dropped
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	// Add messages so the checkpoint is within recentToKeep
	for i := 0; i < 20; i++ {
		messages = append(messages, Message{Role: "user", Content: strings.Repeat("x", 100)})
		messages = append(messages, Message{Role: "assistant", Content: strings.Repeat("y", 100)})
	}
	// This checkpoint is in the recent zone (last 24 messages)
	messages = append(messages, Message{
		Role:    "user",
		Content: "Recent checkpoint",
		Meta:    map[string]string{MetaKeyCheckpoint: "true"},
	})

	// Very low target — should still not drop the recent checkpoint
	result := DropOldestCheckpointSummaries(messages, 0)

	// Checkpoint should still be present
	found := false
	for _, msg := range result {
		if msg.Content == "Recent checkpoint" {
			found = true
			break
		}
	}
	if !found {
		t.Error("checkpoint in recent zone should not be dropped")
	}
}

func TestDropOldestCheckpointSummaries_NoCheckpoints(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
	}
	for i := 0; i < 10; i++ {
		messages = append(messages, Message{Role: "user", Content: strings.Repeat("x", 100)})
		messages = append(messages, Message{Role: "assistant", Content: strings.Repeat("y", 100)})
	}

	result := DropOldestCheckpointSummaries(messages, 0)

	if len(result) != len(messages) {
		t.Errorf("expected no messages dropped, got %d (was %d)", len(result), len(messages))
	}
}

func TestDropOldestCheckpointSummaries_AllCheckpointsInRecent(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	// Fill up to near recentToKeep boundary
	for i := 0; i < 20; i++ {
		messages = append(messages, Message{Role: "user", Content: strings.Repeat("x", 100)})
		messages = append(messages, Message{Role: "assistant", Content: strings.Repeat("y", 100)})
	}
	// All checkpoints are in the last 24 messages
	messages = append(messages, Message{Role: "user", Content: "CP1", Meta: map[string]string{MetaKeyCheckpoint: "true"}})
	messages = append(messages, Message{Role: "user", Content: "CP2", Meta: map[string]string{MetaKeyCheckpoint: "true"}})

	result := DropOldestCheckpointSummaries(messages, 0)

	if len(result) != len(messages) {
		t.Errorf("expected no messages dropped (all checkpoints in recent zone), got %d (was %d)", len(result), len(messages))
	}
}

func TestDropOldestCheckpointSummaries_StopsAtTarget(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	// Add 4 checkpoint summaries, each ~250 tokens
	for i := 0; i < 4; i++ {
		messages = append(messages, Message{
			Role:    "user",
			Content: "Summary " + strings.Repeat("x", 990), // ~1000 tokens each
			Meta:    map[string]string{MetaKeyCheckpoint: "true"},
		})
	}
	// Add recent messages to protect nothing
	for i := 0; i < 30; i++ {
		messages = append(messages, Message{Role: "user", Content: strings.Repeat("a", 50)})
		messages = append(messages, Message{Role: "assistant", Content: strings.Repeat("b", 50)})
	}

	tokensBefore := roughTokens(messages)
	// Target: drop only 2 checkpoints (each ~250 tokens)
	targetTokens := tokensBefore - 500

	result := DropOldestCheckpointSummaries(messages, targetTokens)

	dropped := len(messages) - len(result)
	if dropped != 2 {
		t.Errorf("expected exactly 2 checkpoints dropped, got %d", dropped)
	}
}

func TestDropOldestCheckpointSummaries_DoesNotMutateOriginal(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Checkpoint", Meta: map[string]string{MetaKeyCheckpoint: "true"}},
	}
	for i := 0; i < 30; i++ {
		messages = append(messages, Message{Role: "user", Content: strings.Repeat("x", 100)})
		messages = append(messages, Message{Role: "assistant", Content: strings.Repeat("y", 100)})
	}

	origLen := len(messages)
	_ = DropOldestCheckpointSummaries(messages, 0)

	if len(messages) != origLen {
		t.Errorf("original slice was mutated: was %d, now %d", origLen, len(messages))
	}
}

func TestDropOldestCheckpointSummaries_EmptyMessages(t *testing.T) {
	result := DropOldestCheckpointSummaries(nil, 0)
	if result == nil || len(result) != 0 {
		t.Errorf("expected empty result for nil input, got %v", result)
	}

	result = DropOldestCheckpointSummaries([]Message{}, 0)
	if len(result) != 0 {
		t.Errorf("expected empty result for empty input, got %d", len(result))
	}
}

func TestDropOldestCheckpointSummaries_FewMessages(t *testing.T) {
	// Less than defaultMinMessages — should return copy unchanged
	messages := []Message{
		{Role: "user", Content: "Checkpoint", Meta: map[string]string{MetaKeyCheckpoint: "true"}},
		{Role: "assistant", Content: "Response"},
	}

	result := DropOldestCheckpointSummaries(messages, 0)

	if len(result) != len(messages) {
		t.Errorf("expected no messages dropped for < minMessages, got %d (was %d)", len(result), len(messages))
	}
}

// --- Compact integration tests ---

func TestCompact_CheckpointDropStrategy(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	// Add checkpoint summaries that are large enough to trigger compaction
	for i := 0; i < 5; i++ {
		messages = append(messages, Message{
			Role:    "user",
			Content: "Summary " + strings.Repeat("x", 990),
			Meta:    map[string]string{MetaKeyCheckpoint: "true"},
		})
	}
	// Add recent messages
	for i := 0; i < 30; i++ {
		messages = append(messages, Message{Role: "user", Content: strings.Repeat("a", 50)})
		messages = append(messages, Message{Role: "assistant", Content: strings.Repeat("b", 50)})
	}

	tokenLimit := roughTokens(messages) - 500 // Over limit by enough to need checkpoint drops
	result := Compact(messages, tokenLimit)

	if result.Strategy != "checkpoint_drop" {
		t.Errorf("expected strategy 'checkpoint_drop', got %q", result.Strategy)
	}
	if len(result.Messages) >= len(messages) {
		t.Errorf("expected message reduction, got %d (was %d)", len(result.Messages), len(messages))
	}
}

func TestCompact_CheckpointDropFallsBackToTruncation(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	// Add one small checkpoint (won't save enough)
	messages = append(messages, Message{
		Role:    "user",
		Content: "Small summary",
		Meta:    map[string]string{MetaKeyCheckpoint: "true"},
	})
	// Add lots of large non-checkpoint messages
	for i := 0; i < 30; i++ {
		messages = append(messages, Message{
			Role:    "user",
			Content: "User " + strings.Repeat("a", 5000),
		})
		messages = append(messages, Message{
			Role:    "assistant",
			Content: "Assistant " + strings.Repeat("b", 5000),
		})
	}

	result := Compact(messages, 100)

	// Checkpoint drop alone won't be enough — should fall through to truncation
	if result.Strategy != "truncation" {
		t.Errorf("expected strategy 'truncation' (checkpoint drop insufficient), got %q", result.Strategy)
	}
	if len(result.Messages) >= len(messages) {
		t.Errorf("expected message reduction, got %d (was %d)", len(result.Messages), len(messages))
	}
}

func TestCompact_CheckpointDropPreservesRecentMessages(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	// Add checkpoint summaries
	for i := 0; i < 5; i++ {
		messages = append(messages, Message{
			Role:    "user",
			Content: "Summary " + strings.Repeat("x", 990),
			Meta:    map[string]string{MetaKeyCheckpoint: "true"},
		})
	}
	// Add recent messages (these must be preserved)
	for i := 0; i < 30; i++ {
		messages = append(messages, Message{Role: "user", Content: "recent user " + string(rune('a'+i))})
		messages = append(messages, Message{Role: "assistant", Content: "recent assistant " + string(rune('a'+i))})
	}

	tokenLimit := roughTokens(messages) - 1000 // Over limit
	result := Compact(messages, tokenLimit)

	// The last defaultRecentToKeep messages should all be present
	recentMsgs := messages[len(messages)-defaultRecentToKeep:]
	resultMsgs := result.Messages[len(result.Messages)-defaultRecentToKeep:]
	if len(resultMsgs) < len(recentMsgs) {
		t.Errorf("recent messages were lost: expected %d, got %d", len(recentMsgs), len(resultMsgs))
		return
	}
	for i := range recentMsgs {
		if resultMsgs[i].Content != recentMsgs[i].Content {
			t.Errorf("recent message %d content mismatch: expected %q, got %q",
				i, recentMsgs[i].Content, resultMsgs[i].Content)
		}
	}
}

// --- truncateOldContentHeadTail tests ---

func TestTruncateOldContentHeadTail_PreservesHeadAndTail(t *testing.T) {
	// Build content > 1200 chars (oldMsgMaxChars)
	head := strings.Repeat("H", oldMsgHeadChars)
	tail := strings.Repeat("T", oldMsgTailChars)
	mid := strings.Repeat("M", 500)
	longContent := head + mid + tail

	result := truncateOldContentHeadTail(longContent)

	// Should contain truncation marker
	if !strings.Contains(result, "[truncated]") {
		t.Error("expected truncation marker in result")
	}

	// Head should be preserved
	if !strings.HasPrefix(result, head) {
		t.Error("expected head content to be preserved")
	}

	// Tail should be preserved
	if !strings.HasSuffix(result, tail) {
		t.Error("expected tail content to be preserved")
	}

	// Total rune length should be head + marker + tail
	markerLen := utf8.RuneCountInString("\n... [truncated] ...\n")
	expectedLen := oldMsgHeadChars + markerLen + oldMsgTailChars
	if utf8.RuneCountInString(result) != expectedLen {
		t.Errorf("expected total length %d, got %d", expectedLen, utf8.RuneCountInString(result))
	}

	// Middle content should NOT be present
	if strings.Contains(result, "MMMM") {
		t.Error("expected middle content to be truncated")
	}
}

func TestTruncateOldContentHeadTail_ShortContentNoOp(t *testing.T) {
	// Content <= 1000 chars (oldMsgHeadChars + oldMsgTailChars) should be returned unchanged
	shortContent := strings.Repeat("S", 900)

	result := truncateOldContentHeadTail(shortContent)

	if result != shortContent {
		t.Errorf("expected unchanged content for short input, got %d runes", utf8.RuneCountInString(result))
	}

	// Exactly at boundary: 600 + 400 = 1000 runes, should also be unchanged
	boundaryContent := strings.Repeat("B", oldMsgHeadChars+oldMsgTailChars)
	result2 := truncateOldContentHeadTail(boundaryContent)
	if result2 != boundaryContent {
		t.Error("expected no truncation at exact boundary (1000 runes)")
	}
}

func TestEmergencyTruncate_OldMessagesUseHeadTail(t *testing.T) {
	// Build messages where old user/assistant messages exceed oldMsgMaxChars
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}

	// Add old messages with long content (1400 chars each)
	numOld := 10
	for i := 0; i < numOld; i++ {
		messages = append(messages, Message{
			Role:    "user",
			Content: "User " + strings.Repeat("O", oldMsgMaxChars+200),
		})
		messages = append(messages, Message{
			Role:    "assistant",
			Content: "Assistant " + strings.Repeat("A", oldMsgMaxChars+200),
		})
	}

	// Add recent messages
	for i := 0; i < 30; i++ {
		messages = append(messages, Message{Role: "user", Content: "recent user " + string(rune('a'+i))})
		messages = append(messages, Message{Role: "assistant", Content: "recent assistant " + string(rune('a'+i))})
	}

	// Calculate a token limit that triggers Phase 2 truncation but leaves
	// old messages in place (Phase 3 shouldn't drop them).
	// After Phase 2: old msgs ~1022 chars (~255 tokens each), recent ~9 tokens each.
	// Budget for 20 old + 60 recent + system ≈ 5300 tokens.
	// Set limit slightly above so Phase 3 doesn't drop everything.
	afterPhase2Tokens := roughTokens(messages) - (numOld * 2 * 200 / charsPerToken) // rough savings from truncation
	tokenLimit := afterPhase2Tokens + 500

	result, _ := emergencyTruncate(messages, tokenLimit)

	// Find old (non-recent) messages and verify head+tail truncation
	recentStart := len(result) - defaultRecentToKeep
	if recentStart < 1 {
		recentStart = 1
	}

	markerLen := utf8.RuneCountInString("\n... [truncated] ...\n")
	expectedLen := oldMsgHeadChars + markerLen + oldMsgTailChars

	truncatedCount := 0
	for i := 1; i < recentStart && i < len(result); i++ {
		msg := result[i]
		if msg.Role == "system" || msg.Content == "" {
			continue
		}
		if strings.Contains(msg.Content, "[truncated]") {
			truncatedCount++
			contentLen := utf8.RuneCountInString(msg.Content)
			if contentLen != expectedLen {
				t.Errorf("message %d: expected length %d (head+marker+tail), got %d",
					i, expectedLen, contentLen)
			}
		}
	}
	if truncatedCount == 0 {
		t.Error("expected at least one truncated old message, got none")
	}

	// Verify Phase 3 did not drop messages — all original messages should remain
	if len(result) != len(messages) {
		t.Errorf("expected no messages dropped by Phase 3: had %d, got %d",
			len(messages), len(result))
	}
}

func TestEmergencyTruncate_Phase3DropsMessages(t *testing.T) {
	// Build messages where truncation alone is insufficient, forcing Phase 3
	// to actually drop messages. This exercises the "emergency" strategy path.
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	// Add many messages with moderate content (under oldMsgMaxChars so Phase 2
	// truncation won't help). Each is ~500 chars = ~125 tokens + 10 overhead.
	for i := 0; i < 40; i++ {
		messages = append(messages, Message{
			Role:    "user",
			Content: "User " + strings.Repeat("x", 500),
		})
		messages = append(messages, Message{
			Role:    "assistant",
			Content: "Assistant " + strings.Repeat("y", 500),
		})
	}

	// Very low token limit forces Phase 3 to drop messages.
	tokenLimit := 100
	result, msgsDropped := emergencyTruncate(messages, tokenLimit)

	if !msgsDropped {
		t.Error("expected msgsDropped=true for very low token limit")
	}
	if len(result) >= len(messages) {
		t.Errorf("expected message reduction via Phase 3, got %d (was %d)", len(result), len(messages))
	}
	// System message should be preserved
	if len(result) == 0 || result[0].Role != "system" {
		t.Error("system message should be preserved")
	}
}

// ==============================================================================
// dropOldestTurns tests
// ==============================================================================

func TestDropOldestTurns_DropsCompleteTurns(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Turn1 User " + strings.Repeat("x", 500)},
		{Role: "assistant", Content: "Turn1 Assistant " + strings.Repeat("x", 500)},
		{Role: "user", Content: "Turn2 User " + strings.Repeat("x", 500)},
		{Role: "assistant", Content: "Turn2 Assistant " + strings.Repeat("x", 500)},
		{Role: "user", Content: "Turn3 User " + strings.Repeat("x", 500)},
		{Role: "assistant", Content: "Turn3 Assistant " + strings.Repeat("x", 500)},
	}
	for i := 0; i < 20; i++ {
		messages = append(messages, Message{Role: "user", Content: "recent user " + string(rune('a'+i))})
		messages = append(messages, Message{Role: "assistant", Content: "recent assistant " + string(rune('a'+i))})
	}

	targetTokens := roughTokens(messages) - 1200

	result := dropOldestTurns(messages, defaultRecentToKeep, targetTokens)

	// Should have dropped at least one complete turn
	if len(result) >= len(messages)-1 {
		t.Errorf("expected at least 1 turn dropped, got len=%d (was %d)", len(result), len(messages))
	}

	// Turn 1 should be completely gone
	for _, m := range result {
		if strings.HasPrefix(m.Content, "Turn1 User ") {
			t.Errorf("Turn1 user message should have been dropped: %q", m.Content)
		}
		if strings.HasPrefix(m.Content, "Turn1 Assistant ") {
			t.Errorf("Turn1 assistant message should have been dropped: %q", m.Content)
		}
	}
}

func TestDropOldestTurns_RespectsRecentBoundary(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	// 3 turns before recent zone
	for i := 1; i <= 3; i++ {
		messages = append(messages, Message{Role: "user", Content: "Turn" + string(rune('0'+i)) + "_User"})
		messages = append(messages, Message{Role: "assistant", Content: "Turn" + string(rune('0'+i)) + "_Assistant"})
	}
	// 20 recent pairs (40 messages)
	var recentContents []string
	for i := 0; i < 20; i++ {
		uContent := "Recent User " + string(rune('a'+i))
		aContent := "Recent Assistant " + string(rune('a'+i))
		messages = append(messages, Message{Role: "user", Content: uContent})
		messages = append(messages, Message{Role: "assistant", Content: aContent})
		recentContents = append(recentContents, uContent, aContent)
	}

	totalMsgs := len(messages)
	targetTokens := 0 // drop everything possible in dropable zone
	result := dropOldestTurns(messages, defaultRecentToKeep, targetTokens)

	// At least one complete turn should be dropped
	if len(result) >= totalMsgs-1 {
		t.Errorf("expected at least 1 turn dropped, got %d (was %d)", len(result), totalMsgs)
	}

	// Verify oldest turns are gone
	for _, m := range result {
		if strings.Contains(m.Content, "Turn1_") || strings.Contains(m.Content, "Turn2_") ||
			strings.Contains(m.Content, "Turn3_") {
			t.Errorf("Turn message should have been dropped: %q", m.Content)
		}
	}

	// Build expected set of preserved recent message contents
	expectedContents := make(map[string]bool)
	for _, c := range recentContents {
		expectedContents[c] = true
	}

	// At least some recent messages should be preserved
	found := 0
	for _, m := range result {
		if expectedContents[m.Content] {
			found++
		}
	}
	if found < 2 {
		t.Errorf("expected recent messages to be preserved, found %d", found)
	}
}

func TestDropOldestTurns_ToolChains(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		// Turn 1: tool chain
		{Role: "user", Content: "tool_user_question"},
		{Role: "assistant", Content: "tool_assistant_call", ToolCalls: []ToolCall{
			{ID: "tc1", Type: "function", Function: ToolCallFunction{Name: "search", Arguments: "{}"}},
		}},
		{Role: "tool", Content: "tool_result_data", ToolCallID: "tc1"},
		{Role: "assistant", Content: "tool_assistant_final"},
		// Turn 2: simple
		{Role: "user", Content: "turn2_question"},
		{Role: "assistant", Content: "turn2_answer"},
	}
	for i := 0; i < 20; i++ {
		messages = append(messages, Message{Role: "user", Content: "recent " + string(rune('a'+i))})
		messages = append(messages, Message{Role: "assistant", Content: "recent " + string(rune('b'+i))})
	}

	targetTokens := roughTokens(messages) - 200
	result := dropOldestTurns(messages, defaultRecentToKeep, targetTokens)

	// Both turns should be dropped
	for _, m := range result {
		if m.Content == "tool_user_question" || m.Content == "tool_assistant_final" {
			t.Errorf("Turn 1 messages should have been dropped: %q", m.Content)
		}
		if m.Content == "turn2_question" || m.Content == "turn2_answer" {
			t.Errorf("Turn 2 messages should have been dropped: %q", m.Content)
		}
	}
}

func TestDropOldestTurns_BoundarySpanningTurnProtected(t *testing.T) {
	// Regression test: a turn that starts before recentStart but extends
	// into the recent zone must NOT be dropped. The entire turn is protected.
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	// Add 10 fill pairs = 20 messages (indices 1-20)
	for i := 0; i < 10; i++ {
		messages = append(messages, Message{Role: "user", Content: "fill user " + string(rune('a'+i))})
		messages = append(messages, Message{Role: "assistant", Content: "fill assistant " + string(rune('a'+i))})
	}
	// Add a turn with tool chain starting at index 21:
	// user(21), assistant(22), tool(23), assistant(24)
	messages = append(messages, Message{Role: "user", Content: "boundary_user"})
	messages = append(messages, Message{Role: "assistant", Content: "boundary_assistant", ToolCalls: []ToolCall{
		{ID: "tc1", Type: "function", Function: ToolCallFunction{Name: "search", Arguments: "{}"}},
	}})
	messages = append(messages, Message{Role: "tool", Content: "boundary_tool_result", ToolCallID: "tc1"})
	messages = append(messages, Message{Role: "assistant", Content: "boundary_final"})
	// Add 22 more messages to reach 47 total.
	// recentStart = 47 - 24 = 23.
	// The boundary turn starts at index 21 (before 23) but ends at index 25 (past 23).
	// It should NOT be dropped because it spans the recent zone.
	for i := 0; i < 11; i++ {
		messages = append(messages, Message{Role: "user", Content: "recent user " + string(rune('A'+i))})
		messages = append(messages, Message{Role: "assistant", Content: "recent assistant " + string(rune('A'+i))})
	}

	targetTokens := 0 // Aggressive: try to drop everything possible
	result := dropOldestTurns(messages, defaultRecentToKeep, targetTokens)

	// Verify: all 4 boundary turn messages should still be present
	boundaryPresent := 0
	for _, m := range result {
		switch m.Content {
		case "boundary_user", "boundary_assistant", "boundary_tool_result", "boundary_final":
			boundaryPresent++
		}
	}
	if boundaryPresent != 4 {
		t.Errorf("boundary-spanning turn was partially dropped: found %d of 4 messages", boundaryPresent)
	}
}

func TestDropOldestTurns_FallbackToIndividualDrop(t *testing.T) {
	// Assistant-only messages before recent zone = no complete turns
	// This triggers the fallback to individual message dropping
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	for i := 1; i <= 30; i++ {
		messages = append(messages, Message{Role: "assistant", Content: "NoTurn A" + string(rune('a'+i))})
	}

	tokensBefore := roughTokens(messages)
	targetTokens := tokensBefore - 50

	result := dropOldestTurns(messages, defaultRecentToKeep, targetTokens)

	// Should have dropped some messages via individual fallback
	droppedCount := len(messages) - len(result)
	if droppedCount < 1 {
		t.Errorf("expected at least 1 message dropped via fallback, got %d", droppedCount)
	}

	// The first assistant message should be gone (oldest non-system)
	for _, m := range result {
		if strings.HasPrefix(m.Content, "NoTurn Aa") {
			t.Errorf("First assistant message should have been dropped: %q", m.Content)
		}
	}
}

func TestDropOldestTurns_NoMutateOriginal(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Turn User " + strings.Repeat("x", 500)},
		{Role: "assistant", Content: "Turn Assistant " + strings.Repeat("x", 500)},
	}
	for i := 0; i < 20; i++ {
		messages = append(messages, Message{Role: "user", Content: "recent " + string(rune('a'+i))})
		messages = append(messages, Message{Role: "assistant", Content: "recent " + string(rune('b'+i))})
	}

	origLen := len(messages)
	origContent := messages[1].Content
	_ = dropOldestTurns(messages, defaultRecentToKeep, 0)

	if len(messages) != origLen {
		t.Errorf("original slice was mutated: was %d, now %d", origLen, len(messages))
	}

	if messages[1].Content != origContent {
		t.Error("original message content was modified")
	}
}

func TestDropOldestTurns_UnderTarget(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "Hello"},
	}
	for i := 0; i < 5; i++ {
		messages = append(messages, Message{Role: "user", Content: "more " + string(rune('a'+i))})
		messages = append(messages, Message{Role: "assistant", Content: "more " + string(rune('b'+i))})
	}

	targetTokens := 10000 // Very high, won't drop anything

	result := dropOldestTurns(messages, defaultRecentToKeep, targetTokens)

	if len(result) != len(messages) {
		t.Errorf("expected no messages dropped when under target, got len=%d (was %d)", len(result), len(messages))
	}
}

func TestDropOldestTurns_FewMessages(t *testing.T) {
	// Less than defaultMinMessages (5) — should return copy unchanged
	messages := []Message{
		{Role: "user", Content: "checkpoint", Meta: map[string]string{MetaKeyCheckpoint: "true"}},
		{Role: "assistant", Content: "Response"},
	}

	result := dropOldestTurns(messages, defaultRecentToKeep, 0)

	if len(result) != len(messages) {
		t.Errorf("expected no messages dropped for < minMessages, got %d (was %d)", len(result), len(messages))
	}
}

func TestDropOldestTurns_EmptyInput(t *testing.T) {
	result := dropOldestTurns(nil, defaultRecentToKeep, 0)
	if result == nil || len(result) != 0 {
		t.Errorf("expected empty result for nil input, got %v", result)
	}

	result = dropOldestTurns([]Message{}, defaultRecentToKeep, 0)
	if len(result) != 0 {
		t.Errorf("expected empty result for empty input, got %d", len(result))
	}
}

// DropOldestTurns public wrapper test

func TestDropOldestTurns_PublicWrapper(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Test User " + strings.Repeat("x", 990)},
		{Role: "assistant", Content: "Test Assistant " + strings.Repeat("x", 990)},
	}
	for i := 0; i < 30; i++ {
		messages = append(messages, Message{Role: "user", Content: "recent " + string(rune('a'+i))})
		messages = append(messages, Message{Role: "assistant", Content: "recent " + string(rune('b'+i))})
	}

	target := roughTokens(messages) - 500
	result := DropOldestTurns(messages, target)

	// The turn should have been dropped by the public wrapper
	for _, m := range result {
		if strings.HasPrefix(m.Content, "Test User ") {
			t.Error("turn should have been dropped by public wrapper")
		}
	}
}

// ==============================================================================
// findOldestCompleteTurn tests
// ==============================================================================

func TestFindOldestCompleteTurn_NoUserMessages(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: "a1"},
		{Role: "assistant", Content: "a2"},
	}

	start, end, spanning := findOldestCompleteTurn(msgs, 3)

	if start != -1 || end != -1 || spanning != 0 {
		t.Errorf("expected (-1,-1,0) for no user messages, got (%d,%d,%d)", start, end, spanning)
	}
}

func TestFindOldestCompleteTurn_SimpleTurn(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
	}

	start, end, spanning := findOldestCompleteTurn(msgs, 5)

	if start != 1 || end != 3 || spanning != 0 {
		t.Errorf("expected (1,3,0) for simple turn, got (%d,%d,%d)", start, end, spanning)
	}
}

func TestFindOldestCompleteTurn_ToolChainTurn(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1", ToolCalls: []ToolCall{
			{ID: "tc1", Type: "function", Function: ToolCallFunction{Name: "search", Arguments: "{}"}},
		}},
		{Role: "tool", Content: "result", ToolCallID: "tc1"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a3"},
	}

	start, end, spanning := findOldestCompleteTurn(msgs, 7)

	// Turn spans user(1), assistant(2), tool(3), assistant(4) → end=5
	if start != 1 || end != 5 || spanning != 0 {
		t.Errorf("expected (1,5,0) for tool chain turn, got (%d,%d,%d)", start, end, spanning)
	}
}

func TestFindOldestCompleteTurn_StopsAtNextUser(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
	}

	// recentStart = 3, so scan only indices 1,2
	// Only the first turn (u1,a1) is in range; u2 at index 3 is the boundary
	start, end, spanning := findOldestCompleteTurn(msgs, 3)

	if start != 1 || end != 3 || spanning != 0 {
		t.Errorf("expected (1,3,0) when recentStart limits scan, got (%d,%d,%d)", start, end, spanning)
	}
}

func TestFindOldestCompleteTurn_BoundarySpanning(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "tool", Content: "t1", ToolCallID: "tc1"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u2"},
	}

	// recentStart = 3: turn starts at 1 but ends at 5 (past boundary)
	// Should return (-1,-1) with spanningEnd=5
	start, end, spanning := findOldestCompleteTurn(msgs, 3)

	if start != -1 || end != -1 {
		t.Errorf("expected (-1,-1) for boundary-spanning turn, got (%d,%d)", start, end)
	}
	if spanning != 5 {
		t.Errorf("expected spanningEnd=5, got %d", spanning)
	}
}

// ==============================================================================
// identifyTurnEnd tests (table-driven)
// ==============================================================================

func TestIdentifyTurnEnd(t *testing.T) {
	tests := []struct {
		name     string
		msgs     []Message
		startIdx int
		wantEnd  int
	}{
		{
			name: "simple turn: user→assistant→next user",
			msgs: []Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "u1"},
				{Role: "assistant", Content: "a1"},
				{Role: "user", Content: "u2"},
			},
			startIdx: 1,
			wantEnd:  3,
		},
		{
			name: "turn with tool calls: user→assistant(tool)→tool→assistant→next user",
			msgs: []Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "u1"},
				{Role: "assistant", Content: "a1", ToolCalls: []ToolCall{
					{ID: "tc1", Type: "function", Function: ToolCallFunction{Name: "search", Arguments: "{}"}},
				}},
				{Role: "tool", Content: "result", ToolCallID: "tc1"},
				{Role: "assistant", Content: "a2"},
				{Role: "user", Content: "u2"},
			},
			startIdx: 1,
			wantEnd:  5,
		},
		{
			name: "turn at end of slice (no next user)",
			msgs: []Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "u1"},
				{Role: "assistant", Content: "a1"},
			},
			startIdx: 1,
			wantEnd:  3,
		},
		{
			name: "consecutive tool results",
			msgs: []Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "u1"},
				{Role: "assistant", Content: "a1"},
				{Role: "tool", Content: "t1", ToolCallID: "tc1"},
				{Role: "tool", Content: "t2", ToolCallID: "tc2"},
				{Role: "assistant", Content: "a2"},
				{Role: "user", Content: "u2"},
			},
			startIdx: 1,
			wantEnd:  6,
		},
		{
			name: "nested tool chain: tool→assistant(tool)→tool→assistant→next user",
			msgs: []Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "u1"},
				{Role: "assistant", Content: "a1"},
				{Role: "tool", Content: "t1", ToolCallID: "tc1"},
				{Role: "assistant", Content: "a2"},
				{Role: "tool", Content: "t2", ToolCallID: "tc2"},
				{Role: "assistant", Content: "a3"},
				{Role: "user", Content: "u2"},
			},
			startIdx: 1,
			wantEnd:  7,
		},
		{
			name: "out of range startIdx",
			msgs: []Message{
				{Role: "system", Content: "sys"},
			},
			startIdx: 5,
			wantEnd:  5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			end := identifyTurnEnd(tt.msgs, tt.startIdx)
			if end != tt.wantEnd {
				t.Errorf("expected turn end at %d, got %d", tt.wantEnd, end)
			}
		})
	}
}

// ==============================================================================
// findDropableMessage tests (table-driven)
// ==============================================================================

func TestFindDropableMessage(t *testing.T) {
	tests := []struct {
		name         string
		msgs         []Message
		recentStart  int
		protectedEnd int
		wantIdx      int
	}{
		{
			name: "skips system messages",
			msgs: []Message{
				{Role: "system", Content: "sys"},
				{Role: "system", Content: "sys2"},
				{Role: "user", Content: "u1"},
				{Role: "assistant", Content: "a1"},
			},
			recentStart: 4,
			wantIdx:     2,
		},
		{
			name: "returns first non-system message",
			msgs: []Message{
				{Role: "system", Content: "sys"},
				{Role: "assistant", Content: "a1"},
				{Role: "user", Content: "u1"},
			},
			recentStart: 3,
			wantIdx:     1,
		},
		{
			name: "returns -1 when all messages are system",
			msgs: []Message{
				{Role: "system", Content: "sys"},
				{Role: "system", Content: "sys2"},
				{Role: "system", Content: "sys3"},
			},
			recentStart: 4,
			wantIdx:     -1,
		},
		{
			name: "returns -1 when all messages in recent zone",
			msgs: []Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "u1"},
			},
			recentStart: 1,
			wantIdx:     -1,
		},
		{
			name: "protects boundary-spanning turn",
			msgs: []Message{
				{Role: "system", Content: "sys"},
				{Role: "user", Content: "u1"},
				{Role: "assistant", Content: "a1"},
				{Role: "tool", Content: "t1"},
				{Role: "assistant", Content: "a2"},
				{Role: "user", Content: "u2"},
			},
			recentStart:  3,
			protectedEnd: 5,  // turn at 1-5 spans boundary
			wantIdx:      -1, // all messages in range are protected or system
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := findDropableMessage(tt.msgs, tt.recentStart, tt.protectedEnd)
			if idx != tt.wantIdx {
				t.Errorf("expected index %d, got %d", tt.wantIdx, idx)
			}
		})
	}
}

// ==============================================================================
// Compact turn_drop integration tests
// ==============================================================================

func TestCompact_TurnDropStrategy(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	// Add 2 small checkpoint summaries
	for i := 0; i < 2; i++ {
		messages = append(messages, Message{
			Role:    "user",
			Content: "CP" + string(rune('0'+i)),
			Meta:    map[string]string{MetaKeyCheckpoint: "true"},
		})
	}
	// Add 3 turns with large content
	for i := 1; i <= 3; i++ {
		messages = append(messages, Message{Role: "user", Content: "Turn" + string(rune('0'+i)) + " User " + strings.Repeat("x", 990)})
		messages = append(messages, Message{Role: "assistant", Content: "Turn" + string(rune('0'+i)) + " Assistant " + strings.Repeat("x", 990)})
	}
	// Add recent message pairs
	for i := 0; i < 20; i++ {
		messages = append(messages, Message{Role: "user", Content: "recent user " + string(rune('a'+i))})
		messages = append(messages, Message{Role: "assistant", Content: "recent assistant " + string(rune('a'+i))})
	}

	// tokenLimit set so that:
	// 1. total > tokenLimit (triggers compaction)
	// 2. after checkpoint drop, still > target (0.85 * tokenLimit)
	// 3. after 1 turn drop, < target → strategy is "tool_trim"
	tokenLimit := int(float64(roughTokens(messages)) * 0.9)
	result := Compact(messages, tokenLimit)

	if result.Strategy != "tool_trim" {
		t.Errorf("expected strategy 'tool_trim', got %q", result.Strategy)
	}

	// Checkpoint messages should be dropped
	for _, m := range result.Messages {
		if m.Meta != nil && m.Meta[MetaKeyCheckpoint] == "true" {
			t.Error("checkpoint messages should have been dropped")
		}
	}

	// Verify some messages were actually dropped
	if len(result.Messages) >= len(messages) {
		t.Errorf("expected message reduction, got %d (was %d)", len(result.Messages), len(messages))
	}
}

func TestCompact_TurnDropPreservesRecent(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	// Add 3 large turns
	for i := 1; i <= 3; i++ {
		messages = append(messages, Message{Role: "user", Content: "Turn" + string(rune('0'+i)) + " User " + strings.Repeat("x", 990)})
		messages = append(messages, Message{Role: "assistant", Content: "Turn" + string(rune('0'+i)) + " Assistant " + strings.Repeat("x", 990)})
	}
	// Add recent pairs with unique content
	var recentContents []string
	for i := 0; i < 20; i++ {
		uContent := "recent user " + string(rune('a'+i))
		aContent := "recent assistant " + string(rune('a'+i))
		messages = append(messages, Message{Role: "user", Content: uContent})
		messages = append(messages, Message{Role: "assistant", Content: aContent})
		recentContents = append(recentContents, uContent, aContent)
	}

	result := Compact(messages, int(float64(roughTokens(messages))*0.9))

	// Verify recent messages are preserved at the end of the result
	expectedContents := make(map[string]bool)
	for _, c := range recentContents {
		expectedContents[c] = true
	}

	found := 0
	for _, m := range result.Messages {
		if expectedContents[m.Content] {
			found++
		}
	}
	if found < len(recentContents)/2 {
		t.Errorf("expected most recent messages to be preserved: found %d of %d", found, len(recentContents))
	}
}

func TestCompact_TurnDropPreservesSystem(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	// Add 3 large turns
	for i := 1; i <= 3; i++ {
		messages = append(messages, Message{Role: "user", Content: "Turn" + string(rune('0'+i)) + " User " + strings.Repeat("x", 990)})
		messages = append(messages, Message{Role: "assistant", Content: "Turn" + string(rune('0'+i)) + " Assistant " + strings.Repeat("x", 990)})
	}
	for i := 0; i < 20; i++ {
		messages = append(messages, Message{Role: "user", Content: "recent user " + string(rune('a'+i))})
		messages = append(messages, Message{Role: "assistant", Content: "recent assistant " + string(rune('a'+i))})
	}

	result := Compact(messages, int(float64(roughTokens(messages))*0.9))

	// System message should be first and preserved
	if len(result.Messages) == 0 || result.Messages[0].Role != "system" {
		t.Error("system message should be preserved as first message")
	}
	if result.Messages[0].Content != "You are helpful." {
		t.Error("system message content should be preserved")
	}
}
