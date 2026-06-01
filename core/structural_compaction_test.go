package core

import (
	"context"
	"strings"
	"testing"
)

// stubSummarizer returns a fixed body for any segment so the splice path
// can be exercised deterministically without an LLM.
func stubSummarizer(body string) LLMSummarizer {
	return func(_ context.Context, _ []Message, _ SummarizerHint) (string, error) {
		return body, nil
	}
}

// buildCompactionFixture produces a message list with:
//   - 1 system message
//   - 1 opening user prompt (the "evergreen" anchor that previously got
//     re-anchored as a live instruction post-compaction)
//   - 1 opening assistant reply
//   - 40 middle messages on an unrelated topic
//   - 24 recent messages on yet another topic
//
// Total = 67 messages, well above StructuralMinMessagesToCompact and
// large enough to trigger the layered path (middle ≥ LayeredThreshold).
func buildCompactionFixture(openingPrompt string) []Message {
	msgs := []Message{
		{Role: "system", Content: "You are a helpful coding assistant."},
		{Role: "user", Content: openingPrompt},
		{Role: "assistant", Content: "I'll start by exploring the codebase."},
	}
	for i := 0; i < 20; i++ {
		msgs = append(msgs,
			Message{Role: "user", Content: "middle user turn"},
			Message{Role: "assistant", Content: "middle assistant turn"},
		)
	}
	for i := 0; i < 12; i++ {
		msgs = append(msgs,
			Message{Role: "user", Content: "recent user turn about a different topic"},
			Message{Role: "assistant", Content: "recent assistant reply"},
		)
	}
	return msgs
}

// TestCompactWithLLMSummary_AnchorDemoted is the regression test for the
// "original prompt reanchoring" bug. Before the fix, the first user message
// was copied verbatim into the compacted message list, so the model would
// occasionally treat the original (now-stale) request as a fresh instruction
// after compaction. The fix demotes that message to a past-tense historical
// stub. This test fails on the pre-fix code and passes on the post-fix code.
func TestCompactWithLLMSummary_AnchorDemoted(t *testing.T) {
	const openingPrompt = "Refactor the authentication module to use JWT tokens."
	msgs := buildCompactionFixture(openingPrompt)

	res := CompactWithLLMSummary(context.Background(), msgs, stubSummarizer("- did some work\n- read some files"))
	if res.Strategy == "none" {
		t.Fatalf("expected compaction to fire on %d messages, got Strategy=none", len(msgs))
	}

	// The compacted slice must still start with the system message intact.
	if res.Messages[0].Role != "system" {
		t.Fatalf("expected first message to be system, got role=%q", res.Messages[0].Role)
	}

	// The anchored user message must be present in user-role slot 1, but
	// it must NOT contain the opening prompt verbatim — it should be the
	// demoted historical stub instead.
	if res.Messages[1].Role != "user" {
		t.Fatalf("expected demoted-anchor user message at index 1, got role=%q", res.Messages[1].Role)
	}
	anchorUser := res.Messages[1].Content
	if anchorUser == openingPrompt {
		t.Errorf("anchor still contains the opening prompt verbatim — bug not fixed")
	}
	if !strings.Contains(anchorUser, "Earlier session context") {
		t.Errorf("expected demoted anchor to carry 'Earlier session context' marker, got: %q", anchorUser)
	}
	if !strings.Contains(anchorUser, "refer to those for the current task state") {
		t.Errorf("expected demoted anchor to redirect attention to recent messages, got: %q", anchorUser)
	}
	// The opening prompt text may appear inside the stub as a quoted excerpt;
	// that is fine. What must NOT happen is the user-role slot containing
	// only the bare opening prompt with no historical framing.

	// The anchored assistant reply must also be demoted (not the original
	// "I'll start by exploring the codebase").
	if res.Messages[2].Role != "assistant" {
		t.Fatalf("expected demoted-anchor assistant at index 2, got role=%q", res.Messages[2].Role)
	}
	if !strings.Contains(res.Messages[2].Content, "Acknowledged") {
		t.Errorf("expected demoted-anchor assistant stub, got: %q", res.Messages[2].Content)
	}

	// Sanity: the recent 24 messages should still be at the tail, verbatim.
	tail := res.Messages[len(res.Messages)-StructuralRecentToKeep:]
	if tail[0].Content != "recent user turn about a different topic" {
		t.Errorf("expected recent window preserved verbatim; first recent msg = %q", tail[0].Content)
	}
}

// TestDemoteAnchorUserMessage_TruncatesLongPrompts ensures the excerpt
// stays bounded so a very long opening prompt does not dominate the
// historical stub slot.
func TestDemoteAnchorUserMessage_TruncatesLongPrompts(t *testing.T) {
	long := strings.Repeat("a", 1000)
	out := demoteAnchorUserMessage(long)
	if !strings.Contains(out, "…") {
		t.Errorf("expected truncation marker '…' in long-prompt demotion, got: %q", out)
	}
	if len(out) > 600 {
		t.Errorf("demoted stub should stay bounded; got %d chars", len(out))
	}
}

// TestDemoteAnchorUserMessage_PreservesShortPromptsAsExcerpt confirms the
// short prompt is quoted verbatim inside the historical framing (so the
// model still knows what was originally asked, just contextualized as
// history).
func TestDemoteAnchorUserMessage_PreservesShortPromptsAsExcerpt(t *testing.T) {
	const prompt = "Add unit tests for the parser."
	out := demoteAnchorUserMessage(prompt)
	if !strings.Contains(out, prompt) {
		t.Errorf("expected short opening prompt to appear as a quoted excerpt, got: %q", out)
	}
	if !strings.Contains(out, "Earlier session context") {
		t.Errorf("expected historical framing wrapper, got: %q", out)
	}
}
