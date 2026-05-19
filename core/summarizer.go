package core

import "context"

// SummarizerHint conveys optional shaping parameters from seed's compaction
// pipeline to a consumer-supplied LLMSummarizer. All fields are advisory: a
// summarizer that ignores hints still satisfies the contract.
type SummarizerHint struct {
	// DetailLevel is one of "brief", "summary", "detailed", or "" (default).
	// Layered compaction uses these to graduate detail across age bands of the
	// conversation: brief for oldest content, detailed for newest middle.
	DetailLevel string

	// MaxWords caps the requested length of the returned summary. Zero means
	// "use the summarizer's default budget".
	MaxWords int
}

// LLMSummarizer is the seed extension point for LLM-based structural
// compaction. Consumers (like sprout) provide an implementation that calls
// their LLM to compress a window of older conversation history into a single
// summary string.
//
// The function receives the messages to summarize (read-only — implementations
// must not mutate the slice) and a SummarizerHint shaping the response.
// It returns the raw summary text — seed wraps it into the standard
// "Compacted earlier conversation state:" header before splicing into history.
//
// A nil summarizer disables LLM-based structural compaction; seed then falls
// back to its rule-based compaction (drop oldest turns + emergency truncate).
//
// Implementations should:
//   - Honor ctx cancellation. Long-running summary calls block the chat loop.
//   - Return ("", nil) if the summary would be empty or low-value; seed treats
//     this as "skip LLM summary, fall through to structural compaction".
//   - Return (s, err) with err non-nil if the underlying call failed; seed
//     logs and falls back to rule-based compaction.
type LLMSummarizer func(ctx context.Context, messages []Message, hint SummarizerHint) (string, error)
