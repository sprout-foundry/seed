package core

import (
	"context"
	"slices"
	"strings"
)

// PruningStrategy selects how the pruner reduces conversation history. The
// strategies trade off implementation complexity for content preservation:
// sliding-window is dumbest-and-fastest, adaptive is smartest-and-priciest.
type PruningStrategy string

const (
	// PruneStrategyNone disables pruning. ShouldPrune always returns false.
	PruneStrategyNone PruningStrategy = "none"
	// PruneStrategySlidingWindow keeps the system message plus the most
	// recent N messages. Cheap and predictable; loses all middle context.
	PruneStrategySlidingWindow PruningStrategy = "sliding_window"
	// PruneStrategyImportance scores every message and keeps the
	// highest-scoring subset within a token budget. Preserves anomalies
	// (errors, tool calls) better than sliding-window.
	PruneStrategyImportance PruningStrategy = "importance"
	// PruneStrategyHybrid runs the optimizer (dedup + observation masking +
	// LLM summary) first, then importance-based pruning over the result.
	PruneStrategyHybrid PruningStrategy = "hybrid"
	// PruneStrategyAdaptive inspects the conversation shape (length, tool
	// density, file-read footprint, current context usage) and dispatches to
	// whichever sub-strategy fits best. This is the recommended default.
	PruneStrategyAdaptive PruningStrategy = "adaptive"
)

// Pruner defaults. These mirror the values that previously lived in sprout's
// PruningConfig; seed exposes them as constants so callers can tune via the
// PrunerOptions struct rather than rewriting global state.
const (
	defaultPruningStandardPercent     = 0.87 // start pruning above 87% of context
	defaultPruningAggressivePercent   = 0.95 // aggressive mode above 95%
	defaultPruningTargetPercent       = 0.85 // post-prune target as fraction of max
	defaultPruningMinMessages         = 5
	defaultPruningRecentMessages      = 24
	defaultPruningSlidingWindow       = 30
	defaultAgenticRequiredAvailable   = 12000
	largeFileReadCharThreshold        = 5000
	importanceKeepThreshold           = 0.5
	importanceRecentKeepGroupCount    = 5
	importanceCorpusLongHistory       = 50
	importanceCorpusManyToolCalls     = 20
	hybridAdaptiveTriggerFraction     = 0.80
	pruneTokensFloorPerInvocationFrac = 0.25
	pruneTokensFloorAbsoluteMin       = 1000
)

// PrunerOptions configures a ConversationPruner. Zero values use seed
// defaults; only override fields you explicitly want to differ.
type PrunerOptions struct {
	Strategy             PruningStrategy
	ContextThreshold     float64 // fraction of max tokens that triggers pruning
	MinMessagesToKeep    int
	RecentMessagesToKeep int
	SlidingWindowSize    int
}

// PruneCallOptions are passed per-call into Prune. They carry references to
// the optimizer + summarizer the pruner needs for the hybrid and adaptive
// strategies, plus context flags used to choose tactics.
type PruneCallOptions struct {
	// Optimizer enables hybrid/adaptive paths to run dedup + observation
	// masking + LLM structural compaction before importance-based pruning.
	// May be nil.
	Optimizer *ConversationOptimizer

	// Summarizer is invoked by the hybrid/adaptive paths when they take the
	// LLM-summary route. May be nil; nil disables LLM summary inside the
	// pruner's pipelines.
	Summarizer LLMSummarizer

	// Provider names the LLM provider. Currently used only to switch the
	// importance-based path into the tool-call-aware variant for providers
	// with strict tool-call/result pairing requirements (Minimax, DeepSeek).
	Provider string

	// IsAgenticFlow signals that the caller is an autonomous loop that
	// benefits from a required-headroom guarantee after pruning. Affects
	// the final headroom-enforcement pass.
	IsAgenticFlow bool

	// RequiredAvailableTokens, if non-zero, overrides
	// defaultAgenticRequiredAvailable for the headroom pass. Only consulted
	// when IsAgenticFlow is true.
	RequiredAvailableTokens int
}

// MessageImportance is the scored representation of a single message. Public
// because it appears in the return path of the importance scorer and is
// useful for tests and external diagnostics.
type MessageImportance struct {
	Index           int
	Role            string
	IsUserQuery     bool
	HasToolCalls    bool
	IsToolResult    bool
	IsError         bool
	ContentLength   int
	TokenEstimate   int
	Age             int     // distance from the end of the slice
	ImportanceScore float64 // 0 (drop) to 1 (always keep)
}

// ConversationPruner reduces conversation history according to a configured
// strategy. Construct via NewConversationPruner; call Prune from the chat
// loop when the optimizer/structural-compaction pair couldn't bring usage
// below the trigger threshold.
type ConversationPruner struct {
	strategy             PruningStrategy
	contextThreshold     float64
	minMessagesToKeep    int
	recentMessagesToKeep int
	slidingWindowSize    int
}

// NewConversationPruner builds a pruner from the supplied options, falling
// back to seed defaults for any zero-valued field.
func NewConversationPruner(opts PrunerOptions) *ConversationPruner {
	cp := &ConversationPruner{
		strategy:             opts.Strategy,
		contextThreshold:     opts.ContextThreshold,
		minMessagesToKeep:    opts.MinMessagesToKeep,
		recentMessagesToKeep: opts.RecentMessagesToKeep,
		slidingWindowSize:    opts.SlidingWindowSize,
	}
	if cp.strategy == "" {
		cp.strategy = PruneStrategyAdaptive
	}
	if cp.contextThreshold <= 0 || cp.contextThreshold >= 1 {
		cp.contextThreshold = defaultPruningStandardPercent
	}
	if cp.minMessagesToKeep <= 0 {
		cp.minMessagesToKeep = defaultPruningMinMessages
	}
	if cp.recentMessagesToKeep <= 0 {
		cp.recentMessagesToKeep = defaultPruningRecentMessages
	}
	if cp.slidingWindowSize <= 0 {
		cp.slidingWindowSize = defaultPruningSlidingWindow
	}
	return cp
}

// Strategy returns the pruner's currently configured strategy.
func (cp *ConversationPruner) Strategy() PruningStrategy { return cp.strategy }

// Threshold returns the configured trigger fraction (0–1).
func (cp *ConversationPruner) Threshold() float64 { return cp.contextThreshold }

// RecentMessagesToKeep returns how many recent messages the pruner protects
// from removal across all strategies.
func (cp *ConversationPruner) RecentMessagesToKeep() int { return cp.recentMessagesToKeep }

// SlidingWindowSize returns the configured window size used by the
// sliding-window strategy.
func (cp *ConversationPruner) SlidingWindowSize() int { return cp.slidingWindowSize }

// MinMessagesToKeep returns the floor below which no strategy is allowed
// to reduce the message list.
func (cp *ConversationPruner) MinMessagesToKeep() int { return cp.minMessagesToKeep }

// SetStrategy overrides the strategy at runtime. Useful for tests and for
// consumers that want to flip strategy on the fly.
func (cp *ConversationPruner) SetStrategy(s PruningStrategy) { cp.strategy = s }

// SetThreshold overrides the trigger fraction at runtime. Values outside
// (0, 1) are ignored.
func (cp *ConversationPruner) SetThreshold(t float64) {
	if t > 0 && t < 1 {
		cp.contextThreshold = t
	}
}

// SetRecentMessagesToKeep overrides the recent-preservation window at runtime.
func (cp *ConversationPruner) SetRecentMessagesToKeep(n int) {
	if n > 0 {
		cp.recentMessagesToKeep = n
	}
}

// SetSlidingWindowSize overrides the sliding-window strategy size.
func (cp *ConversationPruner) SetSlidingWindowSize(n int) {
	if n > 0 {
		cp.slidingWindowSize = n
	}
}

// ShouldPrune reports whether the configured strategy and current usage
// suggest a prune pass is needed. Wired into the chat loop's threshold check
// so callers can sequence: trigger → optimize → (still over?) → ShouldPrune?
// → Prune.
func (cp *ConversationPruner) ShouldPrune(currentTokens, maxTokens int) bool {
	if cp.strategy == PruneStrategyNone {
		return false
	}
	if maxTokens <= 0 {
		return false
	}
	return float64(currentTokens)/float64(maxTokens) > cp.contextThreshold
}

// Prune reduces messages according to the configured strategy and returns
// the pruned slice. Always preserves the system message and at least
// cp.minMessagesToKeep messages.
func (cp *ConversationPruner) Prune(ctx context.Context, messages []Message, currentTokens, maxTokens int, opts PruneCallOptions) []Message {
	if cp.strategy == PruneStrategyNone {
		return messages
	}

	var pruned []Message
	switch cp.strategy {
	case PruneStrategySlidingWindow:
		pruned = cp.pruneSlidingWindow(messages)
	case PruneStrategyImportance:
		pruned = cp.pruneByImportance(messages, opts.Provider, maxTokens)
	case PruneStrategyHybrid:
		pruned = cp.pruneHybrid(ctx, messages, opts, maxTokens)
	case PruneStrategyAdaptive:
		pruned = cp.pruneAdaptive(ctx, messages, currentTokens, maxTokens, opts)
	default:
		pruned = messages
	}

	// Never strip below the minimum-keep window. If a strategy went too far,
	// rebuild from the original anchored to oldest + recent.
	if len(pruned) < cp.minMessagesToKeep && len(messages) >= cp.minMessagesToKeep {
		pruned = cp.fallbackKeepSet(messages)
	}

	if opts.IsAgenticFlow {
		required := opts.RequiredAvailableTokens
		if required <= 0 {
			required = defaultAgenticRequiredAvailable
		}
		pruned = cp.ensureRequiredHeadroom(pruned, maxTokens, required)
	}

	return pruned
}

// fallbackKeepSet rebuilds the minimum protected message set: the oldest
// few (including system) plus the recent window. Used when an aggressive
// strategy stripped below cp.minMessagesToKeep.
func (cp *ConversationPruner) fallbackKeepSet(messages []Message) []Message {
	keep := make(map[int]struct{}, cp.minMessagesToKeep+cp.recentMessagesToKeep)
	for i := 0; i < cp.minMessagesToKeep && i < len(messages); i++ {
		keep[i] = struct{}{}
	}
	recentStart := len(messages) - cp.recentMessagesToKeep
	if recentStart < 0 {
		recentStart = 0
	}
	for i := recentStart; i < len(messages); i++ {
		keep[i] = struct{}{}
	}
	result := make([]Message, 0, len(keep))
	for i := range messages {
		if _, ok := keep[i]; ok {
			result = append(result, messages[i])
		}
	}
	return result
}

// pruneSlidingWindow keeps the system message plus the most recent
// slidingWindowSize-1 messages.
func (cp *ConversationPruner) pruneSlidingWindow(messages []Message) []Message {
	if len(messages) <= cp.slidingWindowSize {
		return messages
	}
	pruned := []Message{messages[0]}
	startIdx := len(messages) - cp.slidingWindowSize + 1
	if startIdx < 1 {
		startIdx = 1
	}
	return append(pruned, messages[startIdx:]...)
}

// pruneByImportance keeps messages above a base importance score plus the
// recent window, until the token budget is hit. For providers with strict
// tool-call/result pairing (Minimax, DeepSeek), delegates to the
// tool-call-aware variant so a kept tool result always has its assistant
// parent and vice versa.
func (cp *ConversationPruner) pruneByImportance(messages []Message, provider string, maxTokens int) []Message {
	if strings.EqualFold(provider, "minimax") || strings.EqualFold(provider, "deepseek") {
		return cp.pruneByImportanceToolCallAware(messages, provider, maxTokens)
	}

	if len(messages) == 0 {
		return messages
	}

	scores := cp.scoreMessages(messages)
	keep := make(map[int]bool)
	keep[0] = true

	recentStart := len(messages) - cp.recentMessagesToKeep
	if recentStart < 1 {
		recentStart = 1
	}
	for i := recentStart; i < len(messages); i++ {
		keep[i] = true
	}

	// Always keep the first user query and the first response when present.
	if len(messages) > 2 {
		keep[1] = true
		keep[2] = true
	}

	targetTokens := cp.getTargetTokens(len(messages), maxTokens)
	currentTokens := cp.estimateTokensForIndices(messages, keep)

	for _, s := range scores {
		if keep[s.Index] || s.ImportanceScore <= importanceKeepThreshold {
			continue
		}
		test := currentTokens + s.TokenEstimate
		if test >= targetTokens {
			continue
		}
		keep[s.Index] = true
		currentTokens = test
	}

	out := make([]Message, 0, len(keep))
	for i := range messages {
		if keep[i] {
			out = append(out, messages[i])
		}
	}
	return out
}

// pruneByImportanceToolCallAware groups assistant-with-tool-calls plus their
// downstream tool results into atomic units that are kept or dropped
// together. Required for providers that reject orphaned tool calls or
// orphaned tool results.
func (cp *ConversationPruner) pruneByImportanceToolCallAware(messages []Message, provider string, maxTokens int) []Message {
	type group struct {
		assistantIdx int
		toolIDs      []string
		toolIndices  []int
		isToolGroup  bool
		importance   float64
		tokens       int
	}

	groups := make([]*group, 0, len(messages))
	var current *group

	for i, msg := range messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			if current != nil {
				groups = append(groups, current)
			}
			ids := make([]string, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					ids = append(ids, tc.ID)
				}
			}
			current = &group{
				assistantIdx: i,
				toolIDs:      ids,
				isToolGroup:  true,
			}
			continue
		}

		if msg.Role == "tool" {
			if current != nil && msg.ToolCallID != "" && slices.Contains(current.toolIDs, msg.ToolCallID) {
				current.toolIndices = append(current.toolIndices, i)
				continue
			}
			// Orphan tool result — keep it as its own single-message group so
			// the strict-protocol provider doesn't see it without an anchor.
			if current != nil {
				groups = append(groups, current)
				current = nil
			}
			groups = append(groups, &group{assistantIdx: i})
			continue
		}

		if current != nil {
			groups = append(groups, current)
			current = nil
		}
		groups = append(groups, &group{assistantIdx: i})
	}
	if current != nil {
		groups = append(groups, current)
	}

	// Score and size each group.
	for _, g := range groups {
		if !g.isToolGroup {
			g.importance = cp.scoreSingleMessage(messages[g.assistantIdx])
			g.tokens = estimateTextTokens(messages[g.assistantIdx].Content)
			continue
		}
		maxScore := cp.scoreSingleMessage(messages[g.assistantIdx])
		tokens := estimateTextTokens(messages[g.assistantIdx].Content)
		for _, idx := range g.toolIndices {
			s := cp.scoreSingleMessage(messages[idx])
			if s > maxScore {
				maxScore = s
			}
			tokens += estimateTextTokens(messages[idx].Content)
		}
		g.importance = maxScore
		g.tokens = tokens
	}

	// Always keep first and last group plus a recent suffix.
	keep := make(map[int]bool)
	if len(groups) > 0 {
		keep[0] = true
		keep[len(groups)-1] = true
	}
	recentN := importanceRecentKeepGroupCount
	if len(groups) <= recentN {
		recentN = len(groups) - 1
	}
	for i := len(groups) - recentN; i < len(groups); i++ {
		if i >= 0 {
			keep[i] = true
		}
	}

	target := cp.getTargetTokens(len(messages), maxTokens)
	current2 := 0
	for i, g := range groups {
		if keep[i] {
			current2 += g.tokens
		}
	}

	// Greedy by importance: keep high-scoring groups while under budget.
	indices := make([]int, 0, len(groups))
	for i := range groups {
		if !keep[i] {
			indices = append(indices, i)
		}
	}
	// In-place selection sort by importance descending. groups is small enough
	// that O(n^2) is fine and avoids a sort.Slice import + closure capture.
	for i := 0; i < len(indices); i++ {
		for j := i + 1; j < len(indices); j++ {
			if groups[indices[i]].importance < groups[indices[j]].importance {
				indices[i], indices[j] = indices[j], indices[i]
			}
		}
	}
	for _, gi := range indices {
		if groups[gi].importance <= importanceKeepThreshold {
			continue
		}
		test := current2 + groups[gi].tokens
		if test >= target {
			continue
		}
		keep[gi] = true
		current2 = test
	}

	out := make([]Message, 0, len(messages))
	for i, g := range groups {
		if !keep[i] {
			continue
		}
		out = append(out, messages[g.assistantIdx])
		for _, ti := range g.toolIndices {
			out = append(out, messages[ti])
		}
	}
	_ = provider
	return out
}

// pruneHybrid runs the optimizer (dedup + masking + LLM structural compaction
// if a summarizer is provided) then applies importance-based pruning to the
// already-trimmed message list.
func (cp *ConversationPruner) pruneHybrid(ctx context.Context, messages []Message, opts PruneCallOptions, maxTokens int) []Message {
	if opts.Optimizer == nil {
		return cp.pruneByImportance(messages, opts.Provider, maxTokens)
	}
	optimized := opts.Optimizer.OptimizeConversation(messages)
	if opts.Summarizer != nil {
		if r := CompactWithLLMSummary(ctx, optimized, opts.Summarizer); r.Strategy != "none" {
			optimized = r.Messages
		}
	}
	return cp.pruneByImportance(optimized, opts.Provider, maxTokens)
}

// pruneAdaptive inspects conversation characteristics and dispatches to the
// best-fit strategy. The decision tree matches sprout's original logic so
// migrated consumers see identical behavior.
func (cp *ConversationPruner) pruneAdaptive(ctx context.Context, messages []Message, currentTokens, maxTokens int, opts PruneCallOptions) []Message {
	if maxTokens <= 0 {
		return cp.pruneByImportance(messages, opts.Provider, maxTokens)
	}
	usage := float64(currentTokens) / float64(maxTokens)
	hasLongHistory := len(messages) > importanceCorpusLongHistory
	hasManyToolCalls := cp.countToolCalls(messages) > importanceCorpusManyToolCalls
	hasLargeFiles := cp.hasLargeFileReads(messages)

	switch {
	case usage > defaultPruningAggressivePercent:
		// Critical — go straight to importance-based pruning.
		return cp.pruneByImportance(messages, opts.Provider, maxTokens)

	case hasLongHistory && hasManyToolCalls && usage > hybridAdaptiveTriggerFraction:
		return cp.pruneHybrid(ctx, messages, opts, maxTokens)

	case hasLargeFiles && usage > hybridAdaptiveTriggerFraction:
		if opts.Optimizer == nil {
			return cp.pruneSlidingWindow(messages)
		}
		opt := opts.Optimizer.OptimizeConversation(messages)
		if opts.Summarizer != nil {
			if r := CompactWithLLMSummary(ctx, opt, opts.Summarizer); r.Strategy != "none" {
				opt = r.Messages
			}
		}
		if cp.estimateTokens(opt) < int(float64(maxTokens)*hybridAdaptiveTriggerFraction) {
			return opt
		}
		return cp.pruneSlidingWindow(opt)

	default:
		return cp.pruneByImportance(messages, opts.Provider, maxTokens)
	}
}

// scoreMessages produces importance scores for every message. Scoring rules
// derive from sprout's pruner: system messages are always 1.0, errors and
// the first user query lift score, recent messages get a small bonus.
func (cp *ConversationPruner) scoreMessages(messages []Message) []MessageImportance {
	scores := make([]MessageImportance, 0, len(messages))
	for i, msg := range messages {
		s := MessageImportance{
			Index:         i,
			Role:          msg.Role,
			ContentLength: len(msg.Content),
			TokenEstimate: estimateTextTokens(msg.Content),
			Age:           len(messages) - i,
		}

		importance := 0.0
		lower := strings.ToLower(msg.Content)
		switch msg.Role {
		case "system":
			importance = 1.0
		case "user":
			importance = 0.6
			if i == 1 {
				importance = 0.9
				s.IsUserQuery = true
			}
			if strings.Contains(lower, "error") {
				s.IsError = true
				importance = 0.8
			}
		case "tool":
			s.IsToolResult = true
			if s.Age < 5 {
				importance = 0.7
			} else {
				importance = 0.3
			}
			if strings.Contains(lower, "error") {
				s.IsError = true
				importance = 0.8
			}
		case "assistant":
			importance = 0.5
			if len(msg.ToolCalls) > 0 {
				s.HasToolCalls = true
				importance = 0.6
			}
			if strings.Contains(msg.Content, "I'll") || strings.Contains(msg.Content, "Let me") {
				importance = 0.6
			}
			if s.Age < 3 {
				importance += 0.2
			}
		}

		if s.Age < 5 {
			importance += 0.3 * float64(5-s.Age) / 5.0
		}
		if importance > 1.0 {
			importance = 1.0
		}
		s.ImportanceScore = importance
		scores = append(scores, s)
	}
	return scores
}

// scoreSingleMessage is the per-message scorer used by the tool-call-aware
// path. Excludes recency adjustments (groups handle recency at the group
// level via the explicit "keep last N groups" rule).
func (cp *ConversationPruner) scoreSingleMessage(msg Message) float64 {
	if msg.Role == "system" {
		return 1.0
	}
	importance := 0.5
	switch msg.Role {
	case "user":
		importance = 0.6
		if strings.Contains(strings.ToLower(msg.Content), "error") {
			importance = 0.8
		}
	case "tool":
		importance = 0.5
		if strings.Contains(strings.ToLower(msg.Content), "error") {
			importance = 0.8
		}
	case "assistant":
		importance = 0.5
		if len(msg.ToolCalls) > 0 {
			importance = 0.6
		}
	}
	if importance > 1.0 {
		importance = 1.0
	}
	return importance
}

func (cp *ConversationPruner) estimateTokens(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += estimateTextTokens(m.Content)
		if m.ReasoningContent != "" {
			total += estimateTextTokens(m.ReasoningContent)
		}
	}
	return total
}

func (cp *ConversationPruner) estimateTokensForIndices(messages []Message, indices map[int]bool) int {
	total := 0
	for i, m := range messages {
		if !indices[i] {
			continue
		}
		total += estimateTextTokens(m.Content)
		if m.ReasoningContent != "" {
			total += estimateTextTokens(m.ReasoningContent)
		}
	}
	return total
}

// getTargetTokens computes the post-prune token target based on conversation
// length: bigger conversations get a tighter target so the savings actually
// matter.
func (cp *ConversationPruner) getTargetTokens(messageCount, maxTokens int) int {
	if maxTokens <= 0 {
		maxTokens = 100000
	}
	base := int(defaultPruningTargetPercent * float64(maxTokens))
	switch {
	case messageCount < 20:
		return clampTargetTokens(base, maxTokens)
	case messageCount < 50:
		return clampTargetTokens(base-int(0.08*float64(maxTokens)), maxTokens)
	default:
		return clampTargetTokens(base-int(0.15*float64(maxTokens)), maxTokens)
	}
}

func clampTargetTokens(target, maxTokens int) int {
	min := int(pruneTokensFloorPerInvocationFrac * float64(maxTokens))
	if min < pruneTokensFloorAbsoluteMin {
		min = pruneTokensFloorAbsoluteMin
	}
	if target < min {
		return min
	}
	if target > maxTokens {
		return maxTokens
	}
	return target
}

// countToolCalls counts assistant-with-tool-calls messages and tool-result
// messages combined. Used by adaptive to detect tool-heavy conversations.
func (cp *ConversationPruner) countToolCalls(messages []Message) int {
	n := 0
	for _, m := range messages {
		if m.Role == "tool" {
			n++
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			n++
		}
	}
	return n
}

// hasLargeFileReads detects "Tool call result for read_file:" headers and
// flags the conversation as file-heavy when one such message exceeds
// largeFileReadCharThreshold characters. The text-pattern match is
// sprout-compatible; consumers with different result formats can override
// the strategy via SetStrategy if needed.
func (cp *ConversationPruner) hasLargeFileReads(messages []Message) bool {
	for _, m := range messages {
		if m.Role != "tool" {
			continue
		}
		if strings.Contains(m.Content, "Tool call result for read_file") && len(m.Content) > largeFileReadCharThreshold {
			return true
		}
	}
	return false
}

// ensureRequiredHeadroom drops oldest non-system messages until the
// remaining headroom (maxTokens - tokens(messages)) is at least
// requiredAvailable. Used at the tail of an agentic prune to guarantee the
// model has room for tool outputs and the next response.
func (cp *ConversationPruner) ensureRequiredHeadroom(messages []Message, maxTokens, requiredAvailable int) []Message {
	if maxTokens <= 0 || requiredAvailable <= 0 || len(messages) <= cp.minMessagesToKeep {
		return messages
	}
	pruned := messages
	for len(pruned) > cp.minMessagesToKeep {
		remaining := maxTokens - cp.estimateTokens(pruned)
		if remaining >= requiredAvailable {
			return pruned
		}
		if len(pruned) <= 1 {
			return pruned
		}
		// Drop the first non-system message (index 1).
		pruned = append(pruned[:1], pruned[2:]...)
	}
	return pruned
}

// estimateTextTokens returns the rough token count for a single string. Uses
// the same 4-chars-per-token approximation as seed's compaction module so
// estimates line up across the codebase.
func estimateTextTokens(s string) int {
	return len(s) / charsPerToken
}
