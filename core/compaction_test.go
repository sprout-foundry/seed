package core

import (
	"strings"
	"testing"
)

// --- Compaction tests ---

func TestCompactor_NoOpWhenUnderLimit(t *testing.T) {
	c := NewCompactor()
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "Hello!"},
	}
	result := c.Compact(messages, 10000)
	if len(result.Messages) != len(messages) {
		t.Errorf("expected no compaction, got %d messages", len(result.Messages))
	}
}

func TestCompactor_NoOpWhenTooFewMessages(t *testing.T) {
	c := NewCompactor()
	messages := []Message{
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "Hello"},
	}
	result := c.Compact(messages, 10)
	if len(result.Messages) != len(messages) {
		t.Errorf("expected no compaction for < minMessages, got %d", len(result.Messages))
	}
}

func TestCompactor_CheckpointCompaction(t *testing.T) {
	c := NewCompactor()

	// Build a conversation with many turns with substantial content
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	for i := 0; i < 30; i++ {
		messages = append(messages, Message{
			Role:    "user",
			Content: "Question " + strings.Repeat("X", 200),
		})
		messages = append(messages, Message{
			Role:    "assistant",
			Content: "Answer " + strings.Repeat("Y", 200),
		})
	}

	result := c.Compact(messages, 500)

	// Should be fewer messages than original
	if len(result.Messages) >= len(messages) {
		t.Errorf("expected compaction, got %d messages (was %d)", len(result.Messages), len(messages))
	}

	// System message should be preserved
	if result.Messages[0].Role != "system" {
		t.Error("system message should be preserved")
	}

	// Token count should be reduced
	origTokens := c.roughTokens(messages)
	newTokens := c.roughTokens(result.Messages)
	if newTokens >= origTokens {
		t.Errorf("expected token reduction: %d -> %d", origTokens, newTokens)
	}
}

func TestCompactor_StructuralCompaction(t *testing.T) {
	c := NewCompactor()

	// Build a very long conversation with substantial content
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	for i := 0; i < 50; i++ {
		messages = append(messages, Message{
			Role:    "user",
			Content: "User message " + strings.Repeat("A", 300),
		})
		messages = append(messages, Message{
			Role:    "assistant",
			Content: "Assistant response " + strings.Repeat("B", 300),
		})
	}

	result := c.Compact(messages, 200)

	// Should be significantly reduced
	if len(result.Messages) >= len(messages)-10 {
		t.Errorf("expected significant compaction, got %d messages (was %d)", len(result.Messages), len(messages))
	}

	// Token count should be reduced
	origTokens := c.roughTokens(messages)
	newTokens := c.roughTokens(result.Messages)
	if newTokens >= origTokens {
		t.Errorf("expected token reduction: %d -> %d", origTokens, newTokens)
	}
}

func TestCompactor_EmergencyTruncation(t *testing.T) {
	c := NewCompactor()

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

	result := c.Compact(messages, 100)

	// Should have fewer messages
	if len(result.Messages) >= len(messages) {
		t.Errorf("expected message reduction, got %d (was %d)", len(result.Messages), len(messages))
	}

	// Should preserve system message
	if result.Messages[0].Role != "system" {
		t.Error("system message should be preserved")
	}

	// Token count should be significantly reduced
	origTokens := c.roughTokens(messages)
	newTokens := c.roughTokens(result.Messages)
	if newTokens >= origTokens {
		t.Errorf("expected token reduction: %d -> %d", origTokens, newTokens)
	}
}

func TestCompactor_TrimToolResults(t *testing.T) {
	c := NewCompactor()
	messages := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
		{Role: "tool", Content: strings.Repeat("x", 2000)},
	}

	result := c.trimToolResults(messages, 1000)

	// Tool content should be trimmed
	if len(result[2].Content) >= 2000 {
		t.Error("tool result should be trimmed")
	}
}

func TestCompactor_RoughTokens(t *testing.T) {
	c := NewCompactor()
	messages := []Message{
		{Role: "user", Content: "Hello world"},
	}
	tokens := c.roughTokens(messages)
	if tokens <= 0 {
		t.Errorf("expected positive token count, got %d", tokens)
	}
}

func TestCompactor_TruncateHeadTail(t *testing.T) {
	c := NewCompactor()
	s := strings.Repeat("a", 100)
	result := c.truncateHeadTail(s, 10, 10)
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

func TestCompactor_TruncateHead(t *testing.T) {
	c := NewCompactor()
	s := strings.Repeat("a", 100)
	result := c.truncateHead(s, 10)
	if !strings.Contains(result, "[truncated]") {
		t.Error("expected truncation marker")
	}
	if len([]rune(result)) > 10+20 {
		t.Error("expected truncated content to be short")
	}
}

func TestCompactor_CompactTurns(t *testing.T) {
	c := NewCompactor()
	turns := []Message{
		{Role: "user", Content: "What is 2+2?"},
		{Role: "assistant", Content: "Let me calculate.", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{Name: "shell_command", Arguments: `{"command":"echo 4"}`}},
		}},
		{Role: "tool", Content: "4", ToolCallID: "call_1"},
		{Role: "assistant", Content: "The answer is 4."},
	}

	result := c.compactTurns(turns)

	// Should be compacted
	if len(result) >= len(turns) {
		t.Errorf("expected compaction, got %d messages (was %d)", len(result), len(turns))
	}

	// Should contain summary with tool name
	hasTools := false
	for _, msg := range result {
		if strings.Contains(msg.Content, "shell_command") {
			hasTools = true
			break
		}
	}
	if !hasTools {
		t.Error("expected tool name in summary")
	}
}

func TestCompactor_SummarizeTurn(t *testing.T) {
	c := NewCompactor()
	turn := []Message{
		{Role: "user", Content: "Read the file"},
		{Role: "assistant", Content: "I'll read it now.", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{Name: "read_file", Arguments: `{}`}},
		}},
		{Role: "tool", Content: "file contents here", ToolCallID: "call_1"},
	}

	result := c.summarizeTurn(turn)

	if len(result) != 1 {
		t.Errorf("expected 1 summary message, got %d", len(result))
	}
	summary := result[0].Content
	if !strings.Contains(summary, "Turn summary") {
		t.Error("expected 'Turn summary' marker")
	}
	if !strings.Contains(summary, "read_file") {
		t.Error("expected tool name in summary")
	}
}

// --- CompactionResult metadata tests ---

func TestCompactor_ResultMetadata_NoOp(t *testing.T) {
	c := NewCompactor()
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "Hello!"},
	}
	result := c.Compact(messages, 10000)

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

func TestCompactor_ResultMetadata_Checkpoint(t *testing.T) {
	c := NewCompactor()

	messages := []Message{
		{Role: "system", Content: "You are helpful."},
	}
	for i := 0; i < 30; i++ {
		messages = append(messages, Message{
			Role:    "user",
			Content: "Question " + strings.Repeat("X", 200),
		})
		messages = append(messages, Message{
			Role:    "assistant",
			Content: "Answer " + strings.Repeat("Y", 200),
		})
	}

	result := c.Compact(messages, 3000)

	if result.Strategy != "checkpoint" {
		t.Errorf("expected strategy 'checkpoint', got %q", result.Strategy)
	}
	if result.TokensSaved() <= 0 {
		t.Errorf("expected positive tokens saved, got %d", result.TokensSaved())
	}
	if result.MessageCountDelta(len(messages)) <= 0 {
		t.Errorf("expected positive message count delta, got %d", result.MessageCountDelta(len(messages)))
	}
}

func TestCompactor_ResultMetadata_Emergency(t *testing.T) {
	c := NewCompactor()

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

	result := c.Compact(messages, 100)

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

func TestCompactor_ResultMetadata_TokensSaved(t *testing.T) {
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
		Strategy:     "checkpoint",
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
