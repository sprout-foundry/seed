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

func TestCompact_TruncateHead(t *testing.T) {
	s := strings.Repeat("a", 100)
	result := truncateHead(s, 10)
	if !strings.Contains(result, "[truncated]") {
		t.Error("expected truncation marker")
	}
	if len([]rune(result)) > 10+20 {
		t.Error("expected truncated content to be short")
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

func TestCompact_ResultMetadata_Emergency(t *testing.T) {
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

	if result.Strategy != "emergency" {
		t.Errorf("expected strategy 'emergency', got %q", result.Strategy)
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

func TestCompact_CheckpointDropFallsBackToEmergency(t *testing.T) {
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

	// Checkpoint drop alone won't be enough — should fall through to emergency
	if result.Strategy != "emergency" {
		t.Errorf("expected strategy 'emergency' (checkpoint drop insufficient), got %q", result.Strategy)
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

	result := emergencyTruncate(messages, tokenLimit)

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
