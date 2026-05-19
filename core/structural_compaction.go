package core

import (
	"context"
	"fmt"
	"strings"
)

// Structural compaction tuning. These mirror the values that previously lived
// in sprout's PruningConfig.Structural block and informed seed's chat-loop
// expectations.
const (
	// StructuralRecentToKeep is the number of trailing messages preserved
	// intact across a structural compaction pass. The most recent causal chain
	// must survive so the model can continue reasoning about the current step.
	StructuralRecentToKeep = 24

	// StructuralMinMessagesToCompact is the minimum total message count below
	// which structural compaction is skipped. Below this, the conversation is
	// short enough that compaction yields more loss than gain.
	StructuralMinMessagesToCompact = 30

	// StructuralMinMiddleMessages is the smallest middle-segment size that
	// justifies a summary rewrite. Smaller middles aren't worth the LLM call.
	StructuralMinMiddleMessages = 6

	// LayeredThreshold is the middle-segment size above which compaction
	// produces three graduated layers (brief / summary / detailed) instead of
	// a single summary, retaining more recent-middle detail.
	LayeredThreshold = 30

	// MinLayerSize is the minimum messages per layer in layered compaction.
	MinLayerSize = 10

	BriefWordLimit    = 150
	SummaryWordLimit  = 250
	DetailedWordLimit = 350
)

// StructuralCompactionResult reports what happened in a structural compaction
// pass. Strategy is one of "none", "single_summary", "layered_summary".
type StructuralCompactionResult struct {
	Messages []Message
	Strategy string
	Summarized int // count of messages that were compressed into the summary
}

// CompactWithLLMSummary rewrites the middle of the message list into one or
// more durable summary messages using the supplied LLMSummarizer. The opening
// task anchor (system message + first user/assistant turn) and the recent
// causal chain are preserved intact.
//
// Returns Strategy == "none" with the input unchanged when:
//   - summarizer is nil
//   - len(messages) < StructuralMinMessagesToCompact
//   - the middle segment is smaller than StructuralMinMiddleMessages
//   - the summarizer returns empty / an error for every layer
//
// Otherwise returns the compacted slice. The caller is expected to follow up
// with rule-based Compact() if more reduction is still needed.
func CompactWithLLMSummary(ctx context.Context, messages []Message, summarizer LLMSummarizer) StructuralCompactionResult {
	noChange := StructuralCompactionResult{Messages: messages, Strategy: "none"}

	if summarizer == nil || len(messages) < StructuralMinMessagesToCompact {
		return noChange
	}

	anchorEnd := compactionAnchorEnd(messages)
	recentStart := len(messages) - StructuralRecentToKeep
	if recentStart <= anchorEnd {
		return noChange
	}

	recentStart = adjustCompactionBoundary(messages, recentStart, anchorEnd)
	if recentStart-anchorEnd < StructuralMinMiddleMessages {
		return noChange
	}

	middle := messages[anchorEnd:recentStart]

	if len(middle) >= LayeredThreshold {
		return compactLayered(ctx, messages, anchorEnd, recentStart, middle, summarizer)
	}

	summaryText, err := summarizer(ctx, middle, SummarizerHint{})
	if err != nil || strings.TrimSpace(summaryText) == "" {
		return noChange
	}

	wrapped := wrapStructuralSummary(middle, summaryText, "")
	compacted := spliceSummary(messages, anchorEnd, recentStart, wrapped)
	compacted = dropConsecutiveAssistantAfter(compacted, anchorEnd)

	return StructuralCompactionResult{
		Messages:   compacted,
		Strategy:   "single_summary",
		Summarized: len(middle),
	}
}

// compactLayered produces three graduated summaries across an old / mid /
// recent-middle split and merges them into one combined summary message. If
// every layer fails it falls back to a single whole-middle summary; if that
// also fails it returns the unchanged input.
func compactLayered(ctx context.Context, messages []Message, anchorEnd, recentStart int, middle []Message, summarizer LLMSummarizer) StructuralCompactionResult {
	noChange := StructuralCompactionResult{Messages: messages, Strategy: "none"}

	layerSize := len(middle) / 3
	if layerSize < MinLayerSize {
		layerSize = MinLayerSize
	}
	oldMiddleEnd := anchorEnd + layerSize
	midMiddleEnd := oldMiddleEnd + layerSize

	// Guard against the layered split exceeding the actual middle bounds when
	// layer-size rounding overshoots a tight slice.
	if midMiddleEnd > recentStart {
		midMiddleEnd = recentStart
	}
	if oldMiddleEnd > midMiddleEnd {
		oldMiddleEnd = midMiddleEnd
	}

	brief, _ := summarizer(ctx, messages[anchorEnd:oldMiddleEnd], SummarizerHint{DetailLevel: "brief", MaxWords: BriefWordLimit})
	summary, _ := summarizer(ctx, messages[oldMiddleEnd:midMiddleEnd], SummarizerHint{DetailLevel: "summary", MaxWords: SummaryWordLimit})
	detailed, _ := summarizer(ctx, messages[midMiddleEnd:recentStart], SummarizerHint{DetailLevel: "detailed", MaxWords: DetailedWordLimit})

	combined := mergeLayeredSummaries(brief, summary, detailed, len(middle))
	if combined == "" {
		// Layered pass produced nothing — try a single whole-middle summary.
		fallback, err := summarizer(ctx, middle, SummarizerHint{})
		if err != nil || strings.TrimSpace(fallback) == "" {
			return noChange
		}
		combined = wrapStructuralSummary(middle, fallback, "")
		compacted := spliceSummary(messages, anchorEnd, recentStart, combined)
		compacted = dropConsecutiveAssistantAfter(compacted, anchorEnd)
		return StructuralCompactionResult{
			Messages:   compacted,
			Strategy:   "single_summary",
			Summarized: len(middle),
		}
	}

	compacted := spliceSummary(messages, anchorEnd, recentStart, combined)
	compacted = dropConsecutiveAssistantAfter(compacted, anchorEnd)
	return StructuralCompactionResult{
		Messages:   compacted,
		Strategy:   "layered_summary",
		Summarized: len(middle),
	}
}

// compactionAnchorEnd returns the index past the opening anchor — the
// system message (if any) plus the first user message and any immediately
// following non-tool-calling assistant response. This anchor stays intact
// across compactions so the model never loses the original task framing.
func compactionAnchorEnd(messages []Message) int {
	if len(messages) == 0 {
		return 0
	}

	anchorEnd := 0
	if messages[0].Role == "system" {
		anchorEnd = 1
	}

	for i := anchorEnd; i < len(messages); i++ {
		if messages[i].Role != "user" {
			continue
		}
		anchorEnd = i + 1
		if i+1 < len(messages) && messages[i+1].Role == "assistant" && len(messages[i+1].ToolCalls) == 0 {
			anchorEnd = i + 2
		}
		break
	}

	if anchorEnd == 0 {
		anchorEnd = 1
	}
	return anchorEnd
}

// adjustCompactionBoundary walks recentStart backward past dangling tool
// results and assistant-with-tool-calls messages so the compaction cut never
// splits a tool call from its result. This is the seed-side equivalent of
// the protection findOldestCompleteTurn provides for turn dropping.
func adjustCompactionBoundary(messages []Message, recentStart, anchorEnd int) int {
	for recentStart > anchorEnd {
		if recentStart < len(messages) && messages[recentStart].Role == "tool" {
			recentStart--
			continue
		}
		if recentStart-1 >= anchorEnd && messages[recentStart-1].Role == "assistant" && len(messages[recentStart-1].ToolCalls) > 0 {
			recentStart--
			continue
		}
		break
	}
	return recentStart
}

// spliceSummary builds the post-compaction message list: anchor +
// summary-as-assistant + recent. The summary message carries
// MetaKeyCheckpoint so downstream code (and the next compaction pass) can
// recognize it as a synthetic checkpoint and drop it under further pressure.
func spliceSummary(messages []Message, anchorEnd, recentStart int, summaryBody string) []Message {
	compacted := make([]Message, 0, anchorEnd+1+len(messages)-recentStart)
	compacted = append(compacted, messages[:anchorEnd]...)
	summary := Message{
		Role:    "assistant",
		Content: summaryBody,
	}
	summary.SetMeta(MetaKeyCheckpoint, "true")
	compacted = append(compacted, summary)
	compacted = append(compacted, messages[recentStart:]...)
	return compacted
}

// dropConsecutiveAssistantAfter removes an assistant message that sits
// immediately after our inserted summary, when that assistant has no tool
// calls. Some providers (llama.cpp historically) reject "two assistant
// messages at the end of the prompt"; the summary plus a trailing assistant
// can hit that. The summary wins because it carries the higher-leverage
// compressed history.
func dropConsecutiveAssistantAfter(messages []Message, summaryIdx int) []Message {
	if summaryIdx+1 >= len(messages) {
		return messages
	}
	if messages[summaryIdx].Role != "assistant" || len(messages[summaryIdx].ToolCalls) != 0 {
		return messages
	}
	next := messages[summaryIdx+1]
	if next.Role != "assistant" || len(next.ToolCalls) != 0 {
		return messages
	}
	return append(messages[:summaryIdx+1], messages[summaryIdx+2:]...)
}

// wrapStructuralSummary frames the raw summary text from the LLM with the
// standard "Compacted earlier conversation state:" header so the model
// recognises it as a synthetic message rather than treating it as live
// assistant output.
func wrapStructuralSummary(middle []Message, body, detailLevel string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}

	var b strings.Builder
	switch detailLevel {
	case "brief":
		b.WriteString("Compacted earlier conversation state (brief):\n")
	case "summary":
		b.WriteString("Compacted earlier conversation state (summary):\n")
	case "detailed":
		b.WriteString("Compacted earlier conversation state (detailed):\n")
	default:
		b.WriteString("Compacted earlier conversation state:\n")
	}
	b.WriteString(fmt.Sprintf("- Summarized %d earlier messages to preserve context headroom.\n", len(middle)))

	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "- ") {
			b.WriteString(line)
		} else {
			b.WriteString("- ")
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	b.WriteString("- Use newer messages for the exact current step-by-step state.")
	return strings.TrimSpace(b.String())
}

// mergeLayeredSummaries combines up to three graduated summaries into a
// single assistant body with clear section headers. Returns "" when every
// input is empty so callers can fall back cleanly.
func mergeLayeredSummaries(brief, summary, detailed string, totalMiddle int) string {
	brief = strings.TrimSpace(brief)
	summary = strings.TrimSpace(summary)
	detailed = strings.TrimSpace(detailed)
	if brief == "" && summary == "" && detailed == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("[Context compaction — layered summary]\n\n")

	if brief != "" {
		b.WriteString("### Earlier activities (brief)\n")
		b.WriteString(brief)
		b.WriteString("\n\n")
	}
	if summary != "" {
		b.WriteString("### Mid-session activities (summary)\n")
		b.WriteString(summary)
		b.WriteString("\n\n")
	}
	if detailed != "" {
		b.WriteString("### Recent activities (detailed)\n")
		b.WriteString(detailed)
		b.WriteString("\n\n")
	}
	b.WriteString(fmt.Sprintf("- Summarized %d earlier messages across 3 graduated detail layers.", totalMiddle))
	return strings.TrimSpace(b.String())
}
