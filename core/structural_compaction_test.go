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

// A middle that already holds compaction summaries is consolidated, not
// re-condensed: the earlier summary is carried through verbatim and only the
// raw history beyond it reaches the summarizer. Re-summarizing a summary is
// what makes a long session lose fidelity with every pass.
func TestCompactWithLLMSummary_CarriesPriorSummaryForward(t *testing.T) {
	msgs := buildCompactionFixture("Refactor the auth module to use JWT.")
	first := CompactWithLLMSummary(context.Background(), msgs, stubSummarizer("- first pass finding"))
	if first.Strategy == "none" {
		t.Fatalf("expected the first pass to compact")
	}

	// Grow the raw history past the recent window so the second pass has a
	// middle to condense.
	grown := append([]Message{}, first.Messages...)
	for i := 0; i < 20; i++ {
		grown = append(grown, Message{Role: "user", Content: "later question " + strings.Repeat("q", 40)})
		grown = append(grown, Message{Role: "assistant", Content: "later answer " + strings.Repeat("a", 40)})
	}

	var sawCarriedSummary bool
	second := CompactWithLLMSummary(context.Background(), grown, func(_ context.Context, m []Message, _ SummarizerHint) (string, error) {
		for _, msg := range m {
			if msg.Meta != nil && msg.Meta[MetaKeyCheckpoint] == "true" {
				sawCarriedSummary = true
			}
		}
		return "- second pass finding", nil
	})
	if second.Strategy == "none" {
		t.Fatalf("expected the second pass to compact")
	}
	if sawCarriedSummary {
		t.Error("a prior compaction summary reached the summarizer and was re-condensed")
	}
	var carried bool
	for _, m := range second.Messages {
		if strings.Contains(m.Content, "- first pass finding") {
			carried = true
		}
	}
	if !carried {
		t.Error("first-pass summary was dropped instead of carried forward")
	}
}

// The anchor is the only verbatim record of what the session was asked to do,
// so no drop phase may remove it however hard the pressure.
func TestDropCheckpoints_PreservesAnchor(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Build the thing"},
		{Role: "assistant", Content: "Starting."},
		{Role: "user", Content: "old summary", Meta: map[string]string{MetaKeyCheckpoint: "true"}},
	}
	for i := 0; i < 30; i++ {
		messages = append(messages, Message{Role: "user", Content: strings.Repeat("x", 80)})
		messages = append(messages, Message{Role: "assistant", Content: strings.Repeat("y", 80)})
	}

	result := dropOldestCheckpointSummaries(messages, defaultRecentToKeep, 1)

	for _, m := range result {
		if m.Content == "Build the thing" || m.Content == "Starting." {
			continue
		}
		if m.Role == "system" {
			continue
		}
	}
	var keptTask bool
	for _, m := range result {
		if m.Content == "Build the thing" {
			keptTask = true
		}
	}
	if !keptTask {
		t.Error("the opening task statement must survive the checkpoint-drop pass")
	}
	var keptOldSummary bool
	for _, m := range result {
		if m.Content == "old summary" {
			keptOldSummary = true
		}
	}
	if keptOldSummary {
		t.Error("a summary outside the anchor should still be droppable")
	}
}

// summaryBody returns the spliced summary message from a compacted history.
func summaryBody(t *testing.T, msgs []Message) string {
	t.Helper()
	for _, m := range msgs {
		if m.Meta != nil && m.Meta[MetaKeyCheckpoint] == "true" {
			return m.Content
		}
	}
	t.Fatal("no summary message in compacted history")
	return ""
}

// The artifact hook must run once per pass, after the layered merge. Attaching
// the record from inside the summarizer instead puts one copy per layer into
// the single merged body, which is why the hook exists.
func TestCompactWithLLMSummary_ArtifactBlockAppearsOnceAcrossLayers(t *testing.T) {
	msgs := buildCompactionFixture("Refactor the authentication module.")

	var summarizeCalls, artifactCalls int
	summarize := func(_ context.Context, _ []Message, hint SummarizerHint) (string, error) {
		summarizeCalls++
		return "- layer " + hint.DetailLevel, nil
	}
	artifacts := func([]Message) string {
		artifactCalls++
		return "Files modified during compacted segment:\n- M pkg/auth.go"
	}

	res := CompactWithLLMSummary(context.Background(), msgs, summarize, StructuralOptions{ArtifactBlock: artifacts})
	if res.Strategy != "layered_summary" {
		t.Fatalf("expected the fixture to take the layered path, got %q", res.Strategy)
	}
	if summarizeCalls < 2 {
		t.Fatalf("expected the layered split to call the summarizer repeatedly, got %d", summarizeCalls)
	}
	if artifactCalls != 1 {
		t.Errorf("expected the artifact hook to run once, ran %d times", artifactCalls)
	}
	body := summaryBody(t, res.Messages)
	if n := strings.Count(body, "Files modified during compacted segment:"); n != 1 {
		t.Errorf("expected exactly one artifact record in the summary, got %d:\n%s", n, body)
	}
}

// buildSinglePathFixture returns a history whose middle is large enough to
// summarize but below LayeredThreshold, so compaction takes the single-pass
// path rather than the layered split.
func buildSinglePathFixture() []Message {
	msgs := []Message{
		{Role: "system", Content: "You are a helpful coding assistant."},
		{Role: "user", Content: "Refactor the authentication module."},
		{Role: "assistant", Content: "I'll start by exploring the codebase."},
	}
	for i := 0; i < 10; i++ {
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

// A summarizer that returns nothing must still yield no compaction, and must
// not invoke the artifact hook at all. A manifest-only body would be treated
// as a usable summary and spliced in place of the narrative.
func TestCompactWithLLMSummary_EmptySummaryStaysEmpty(t *testing.T) {
	msgs := buildSinglePathFixture()
	summarize := func(context.Context, []Message, SummarizerHint) (string, error) { return "", nil }

	var artifactCalls int
	artifacts := func([]Message) string {
		artifactCalls++
		return "Files modified during compacted segment:\n- M pkg/auth.go"
	}

	res := CompactWithLLMSummary(context.Background(), msgs, summarize, StructuralOptions{ArtifactBlock: artifacts})
	if res.Strategy != "none" {
		t.Fatalf("expected no compaction for an empty summary, got %q", res.Strategy)
	}
	if artifactCalls != 0 {
		t.Errorf("expected the artifact hook to be skipped, ran %d times", artifactCalls)
	}
}

// The layered fallback applies the hook on its own path, separate from the
// merged-layers path. It must still contribute exactly one record.
func TestCompactWithLLMSummary_LayeredFallbackCarriesOneArtifactBlock(t *testing.T) {
	msgs := buildCompactionFixture("Refactor the authentication module.")
	summarize := func(_ context.Context, _ []Message, hint SummarizerHint) (string, error) {
		if hint.DetailLevel != "" {
			return "", nil // every layer fails, forcing the fallback
		}
		return "- fallback summary", nil
	}
	artifacts := func([]Message) string { return "Files modified during compacted segment:\n- M pkg/auth.go" }

	res := CompactWithLLMSummary(context.Background(), msgs, summarize, StructuralOptions{ArtifactBlock: artifacts})
	if res.Strategy != "single_summary" {
		t.Fatalf("expected the layered fallback path, got %q", res.Strategy)
	}
	body := summaryBody(t, res.Messages)
	if n := strings.Count(body, "Files modified during compacted segment:"); n != 1 {
		t.Errorf("expected exactly one artifact record from the fallback, got %d:\n%s", n, body)
	}
}

// A nil hook leaves the summary byte-identical to the no-options call.
func TestCompactWithLLMSummary_NilArtifactBlockIsNoOp(t *testing.T) {
	msgs := buildCompactionFixture("Refactor the authentication module.")
	summarize := func(context.Context, []Message, SummarizerHint) (string, error) {
		return "- did some work", nil
	}

	withOpt := CompactWithLLMSummary(context.Background(), msgs, summarize, StructuralOptions{})
	without := CompactWithLLMSummary(context.Background(), msgs, summarize)
	if got, want := summaryBody(t, withOpt.Messages), summaryBody(t, without.Messages); got != want {
		t.Errorf("zero-value options changed the summary:\ngot  %q\nwant %q", got, want)
	}
}
