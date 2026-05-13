package core

import (
	"strings"
	"testing"
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
