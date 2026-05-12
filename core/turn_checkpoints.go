package core

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// TurnCheckpoint captures a summary of a completed conversation turn.
// It records the message range consumed by the turn and a compact summary
// that can replace the original messages during context compaction.
type TurnCheckpoint struct {
	// StartIndex is the index of the first message in the turn (the user query).
	StartIndex int `json:"start_index"`

	// EndIndex is the index of the last message in the turn (the final assistant response).
	EndIndex int `json:"end_index"`

	// Summary is a concise description of what happened in the turn.
	Summary string `json:"summary"`

	// ActionableSummary is a bullet-list of accomplishments with file paths,
	// commands run, and other concrete details useful for continued context.
	ActionableSummary string `json:"actionable_summary"`
}

// TurnSummaryBuilder builds a TurnCheckpoint from a slice of messages
// representing a single conversation turn. It extracts the user question,
// tool calls, errors, files modified, and final status to produce both
// a narrative summary and an actionable bullet list.
type TurnSummaryBuilder struct {
	// KnownFileTools is a set of tool names that operate on files.
	// If nil, the default set is used.
	KnownFileTools map[string]bool

	// KnownShellTools is a set of tool names that execute shell commands.
	// If nil, the default set is used.
	KnownShellTools map[string]bool

	// KnownErrorPatterns are substrings that indicate a tool result is an error.
	// If nil, the default set is used.
	KnownErrorPatterns []string
}

// defaultFileTools is the set of tool names that operate on files.
var defaultFileTools = map[string]bool{
	"read_file":      true,
	"write_file":     true,
	"edit_file":      true,
	"create_file":    true,
	"delete_file":    true,
	"list_files":     true,
	"search_files":   true,
	"glob_files":     true,
	"file_search":    true,
	"grep":           true,
	"patch_file":     true,
	"append_file":    true,
	"read_structured": true,
	"write_structured": true,
	"patch_structured": true,
}

// defaultShellTools is the set of tool names that execute shell commands.
var defaultShellTools = map[string]bool{
	"shell":       true,
	"execute":     true,
	"run_command": true,
	"bash":        true,
	"sh":          true,
	"cmd":         true,
}

// defaultErrorPatterns are substrings that indicate a tool result is an error.
// All matching is case-insensitive, so only lowercase patterns are needed.
var defaultErrorPatterns = []string{
	"error:",
	"failed",
	"permission denied",
	"not found",
	"does not exist",
	"no such file",
	"timeout",
	"refused",
	"denied",
}

// NewTurnSummaryBuilder creates a new builder with default configuration.
func NewTurnSummaryBuilder() *TurnSummaryBuilder {
	return &TurnSummaryBuilder{}
}

// fileTools returns the set of known file tools, using defaults if not configured.
func (b *TurnSummaryBuilder) fileTools() map[string]bool {
	if b.KnownFileTools != nil {
		return b.KnownFileTools
	}
	return defaultFileTools
}

// shellTools returns the set of known shell tools, using defaults if not configured.
func (b *TurnSummaryBuilder) shellTools() map[string]bool {
	if b.KnownShellTools != nil {
		return b.KnownShellTools
	}
	return defaultShellTools
}

// errorPatterns returns the list of error patterns, using defaults if not configured.
func (b *TurnSummaryBuilder) errorPatterns() []string {
	if b.KnownErrorPatterns != nil {
		return b.KnownErrorPatterns
	}
	return defaultErrorPatterns
}

// Build constructs a TurnCheckpoint from the given messages.
// The messages should represent a single turn: starting with a user query,
// followed by any number of assistant/tool-call/tool-result cycles,
// and ending with the final assistant response.
// Returns a checkpoint with StartIndex=0 and EndIndex=len(messages)-1
// since the caller is responsible for setting the actual indices in state.
func (b *TurnSummaryBuilder) Build(messages []Message) TurnCheckpoint {
	extracted := b.extractTurnData(messages)

	summary := b.buildSummary(extracted)
	actionable := b.buildActionableSummary(extracted)

	return TurnCheckpoint{
		StartIndex:        0,
		EndIndex:          len(messages) - 1,
		Summary:           summary,
		ActionableSummary: actionable,
	}
}

// turnData holds the extracted information from a turn.
type turnData struct {
	userQuestion   string
	toolCalls      []toolCallInfo
	errors         []string
	filesRead      []string
	filesModified  []string
	shellCommands  []string
	finalResponse  string
	status         turnStatus
}

// toolCallInfo captures details about a single tool call.
type toolCallInfo struct {
	name      string
	arguments string
	result    string
	isError   bool
}

// turnStatus represents the outcome of a conversation turn.
type turnStatus int

const (
	statusCompleted turnStatus = iota
	statusInterrupted
	statusError
	statusPartial
)

func (s turnStatus) String() string {
	switch s {
	case statusCompleted:
		return "completed"
	case statusInterrupted:
		return "interrupted"
	case statusError:
		return "error"
	case statusPartial:
		return "partial"
	default:
		return "unknown"
	}
}

// extractTurnData parses messages and extracts structured information.
func (b *TurnSummaryBuilder) extractTurnData(messages []Message) turnData {
	var data turnData

	fileTools := b.fileTools()
	shellTools := b.shellTools()
	errorPatterns := b.errorPatterns()

	seenFilesRead := make(map[string]bool)
	seenFilesModified := make(map[string]bool)

	for i, msg := range messages {
		switch msg.Role {
		case "user":
			if data.userQuestion == "" {
				data.userQuestion = truncateString(msg.Content, 200)
			}

		case "assistant":
			// Track tool calls
			for _, tc := range msg.ToolCalls {
				info := toolCallInfo{
					name:      tc.Function.Name,
					arguments: tc.Function.Arguments,
				}

				// Extract file paths from arguments
				if fileTools[tc.Function.Name] {
					if path := extractFilePath(tc.Function.Arguments); path != "" {
						if isFileWriteTool(tc.Function.Name) {
							if !seenFilesModified[path] {
								seenFilesModified[path] = true
								data.filesModified = append(data.filesModified, path)
							}
						} else {
							if !seenFilesRead[path] {
								seenFilesRead[path] = true
								data.filesRead = append(data.filesRead, path)
							}
						}
					}
				}

				// Track shell commands
				if shellTools[tc.Function.Name] {
					if cmd := extractShellCommand(tc.Function.Arguments); cmd != "" {
						data.shellCommands = append(data.shellCommands, cmd)
					}
				}

				data.toolCalls = append(data.toolCalls, info)
			}

			// Last assistant message is the final response
			if i == len(messages)-1 || msg.Content != "" {
				if msg.Content != "" {
					data.finalResponse = msg.Content
				}
			}

		case "tool":
			// Match tool result to the most recent unmatched tool call by scanning
			// backwards from the end. Tool results arrive in order, so the last
			// unmatched call is the correct one.
			if len(data.toolCalls) > 0 {
				lastIdx := len(data.toolCalls) - 1
				for lastIdx >= 0 && data.toolCalls[lastIdx].result != "" {
					lastIdx--
				}
				if lastIdx < 0 {
					// All tool calls already matched; skip orphaned result
					continue
				}
				content := strings.TrimSpace(msg.Content)

				// Check for errors
				isError := false
				for _, pattern := range errorPatterns {
					if strings.Contains(strings.ToLower(msg.Content), strings.ToLower(pattern)) {
						isError = true
						break
					}
				}

				if isError && data.toolCalls[lastIdx].result == "" {
					data.errors = append(data.errors, truncateString(msg.Content, 150))
				}

				data.toolCalls[lastIdx].result = content
				data.toolCalls[lastIdx].isError = isError
			}
		}
	}

	// Determine status
	data.status = b.determineStatus(data)

	return data
}

// determineStatus assesses the outcome of the turn.
func (b *TurnSummaryBuilder) determineStatus(data turnData) turnStatus {
	if len(data.errors) > 0 && data.finalResponse == "" {
		return statusError
	}

	// Check for truncation indicators in final response
	if data.finalResponse != "" {
		if strings.HasSuffix(data.finalResponse, "...") {
			return statusPartial
		}
		// Check for abrupt endings
		lastChar := len(data.finalResponse)
		if lastChar > 0 {
			lastByte := data.finalResponse[lastChar-1]
			if lastByte == ',' || lastByte == '(' || lastByte == '[' {
				return statusPartial
			}
		}
	}

	if data.finalResponse == "" && len(data.toolCalls) > 0 {
		return statusInterrupted
	}

	return statusCompleted
}

// buildSummary creates a narrative summary of the turn.
func (b *TurnSummaryBuilder) buildSummary(data turnData) string {
	var parts []string

	// Start with what was asked
	if data.userQuestion != "" {
		parts = append(parts, fmt.Sprintf("User asked: %s", data.userQuestion))
	}

	// Describe tool usage
	if len(data.toolCalls) > 0 {
		toolNames := make(map[string]int)
		for _, tc := range data.toolCalls {
			toolNames[tc.name]++
		}

		// Sort tool names for deterministic output.
		sortedNames := make([]string, 0, len(toolNames))
		for name := range toolNames {
			sortedNames = append(sortedNames, name)
		}
		sort.Strings(sortedNames)

		var toolDesc []string
		for _, name := range sortedNames {
			count := toolNames[name]
			if count > 1 {
				toolDesc = append(toolDesc, fmt.Sprintf("%s (%dx)", name, count))
			} else {
				toolDesc = append(toolDesc, name)
			}
		}
		parts = append(parts, fmt.Sprintf("Used tools: %s", strings.Join(toolDesc, ", ")))
	}

	// Describe file operations
	if len(data.filesRead) > 0 {
		files := uniqueStrings(data.filesRead)
		if len(files) <= 3 {
			parts = append(parts, fmt.Sprintf("Read files: %s", strings.Join(files, ", ")))
		} else {
			parts = append(parts, fmt.Sprintf("Read %d files including %s", len(files), strings.Join(files[:3], ", ")))
		}
	}

	if len(data.filesModified) > 0 {
		files := uniqueStrings(data.filesModified)
		if len(files) <= 3 {
			parts = append(parts, fmt.Sprintf("Modified files: %s", strings.Join(files, ", ")))
		} else {
			parts = append(parts, fmt.Sprintf("Modified %d files including %s", len(files), strings.Join(files[:3], ", ")))
		}
	}

	// Describe shell commands
	if len(data.shellCommands) > 0 {
		cmds := uniqueStrings(data.shellCommands)
		if len(cmds) <= 3 {
			parts = append(parts, fmt.Sprintf("Ran commands: %s", strings.Join(cmds, ", ")))
		} else {
			parts = append(parts, fmt.Sprintf("Ran %d commands including %s", len(cmds), strings.Join(cmds[:3], ", ")))
		}
	}

	// Describe errors
	if len(data.errors) > 0 {
		parts = append(parts, fmt.Sprintf("Encountered %d error(s)", len(data.errors)))
	}

	// Final status
	if data.finalResponse != "" {
		responseSummary := truncateString(strings.TrimSpace(data.finalResponse), 150)
		parts = append(parts, fmt.Sprintf("Response: %s", responseSummary))
		if data.status != statusCompleted {
			parts = append(parts, fmt.Sprintf("Status: %s", data.status))
		}
	} else {
		parts = append(parts, fmt.Sprintf("Status: %s", data.status))
	}

	return strings.Join(parts, ". ") + "."
}

// buildActionableSummary creates a bullet-list of accomplishments.
func (b *TurnSummaryBuilder) buildActionableSummary(data turnData) string {
	var bullets []string

	if data.userQuestion != "" {
		bullets = append(bullets, fmt.Sprintf("- Question: %s", data.userQuestion))
	}

	if len(data.filesRead) > 0 {
		files := uniqueStrings(data.filesRead)
		for _, f := range files {
			bullets = append(bullets, fmt.Sprintf("- Read: %s", f))
		}
	}

	if len(data.filesModified) > 0 {
		files := uniqueStrings(data.filesModified)
		for _, f := range files {
			bullets = append(bullets, fmt.Sprintf("- Modified: %s", f))
		}
	}

	if len(data.shellCommands) > 0 {
		cmds := uniqueStrings(data.shellCommands)
		for _, cmd := range cmds {
			bullets = append(bullets, fmt.Sprintf("- Command: %s", cmd))
		}
	}

	if len(data.errors) > 0 {
		for _, err := range data.errors {
			bullets = append(bullets, fmt.Sprintf("- Error: %s", err))
		}
	}

	if data.finalResponse != "" {
		responseSummary := truncateString(strings.TrimSpace(data.finalResponse), 200)
		bullets = append(bullets, fmt.Sprintf("- Result: %s", responseSummary))
	}

	if len(bullets) == 0 {
		return fmt.Sprintf("Turn completed with status: %s", data.status)
	}

	return strings.Join(bullets, "\n")
}

// extractFilePath extracts a file path from tool arguments.
// It tries keys in order of specificity to avoid substring collisions
// (e.g., "file" matching inside "filename").
func extractFilePath(args string) string {
	// Try most specific keys first to avoid substring collisions.
	for _, key := range []string{"filename", "path", "file"} {
		if val := extractJSONKey(args, key); val != "" {
			return val
		}
	}
	return ""
}

// extractShellCommand extracts a shell command from tool arguments.
// It tries keys in order of specificity to avoid substring collisions
// (e.g., "cmd" matching inside "command").
func extractShellCommand(args string) string {
	for _, key := range []string{"command", "cmd"} {
		if val := extractJSONKey(args, key); val != "" {
			return val
		}
	}
	return ""
}

// extractJSONKey extracts a string value for the given key from a JSON-like
// argument string. It looks for "key": and extracts the quoted string value.
// The key must be matched as a whole JSON key (surrounded by quotes).
func extractJSONKey(args, key string) string {
	target := `"` + key + `"`
	idx := strings.Index(args, target)
	if idx < 0 {
		return ""
	}
	// Verify this is a JSON key: the character after the closing quote must
	// be a colon (possibly with whitespace).
	afterKey := strings.TrimSpace(args[idx+len(target):])
	if len(afterKey) == 0 || afterKey[0] != ':' {
		return ""
	}
	return extractJSONStringValue(afterKey)
}

// extractJSONStringValue extracts a string value from a JSON value position.
// Input should start from the colon (e.g., `: "..."`).
func extractJSONStringValue(s string) string {
	// Find the colon
	colonIdx := strings.Index(s, ":")
	if colonIdx < 0 {
		return ""
	}

	rest := strings.TrimSpace(s[colonIdx+1:])
	if len(rest) == 0 {
		return ""
	}

	// Check if it starts with a quote
	if rest[0] != '"' {
		return ""
	}

	// Find the closing quote, handling escapes
	rest = rest[1:] // skip opening quote
	var result strings.Builder
	for i := 0; i < len(rest); i++ {
		if rest[i] == '\\' && i+1 < len(rest) {
			result.WriteByte(rest[i+1])
			i++
		} else if rest[i] == '"' {
			break
		} else {
			result.WriteByte(rest[i])
		}
	}

	return result.String()
}

// defaultWriteTools is the set of tool names that modify files.
var defaultWriteTools = map[string]bool{
	"write_file":       true,
	"edit_file":        true,
	"create_file":      true,
	"delete_file":      true,
	"patch_file":       true,
	"append_file":      true,
	"write_structured": true,
	"patch_structured": true,
}

// isFileWriteTool returns true if the tool name indicates a file modification.
func isFileWriteTool(name string) bool {
	return defaultWriteTools[name]
}

// truncateString truncates a string to maxLen characters, adding "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Truncate at word boundary if possible
	truncated := s[:maxLen]
	lastSpace := strings.LastIndex(truncated, " ")
	if lastSpace >= maxLen/2 {
		truncated = truncated[:lastSpace]
	}
	return truncated + "..."
}

// uniqueStrings removes duplicate strings while preserving order.
func uniqueStrings(items []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}

// BuildCheckpointSummary is a convenience function that creates a checkpoint
// summary from messages without requiring a builder instance.
func BuildCheckpointSummary(messages []Message) TurnCheckpoint {
	builder := NewTurnSummaryBuilder()
	return builder.Build(messages)
}

// RecordTurnCheckpointAsync asynchronously builds a checkpoint from the given
// messages and stores it in state. It spawns a goroutine to compute the summary
// so it doesn't block the conversation loop.
//
// The message slice is snapshotted immediately (before the goroutine starts) so
// the background computation sees a consistent view even if the caller mutates
// the original slice. If the summary computation takes longer than timeout, a
// minimal checkpoint is stored instead.
func RecordTurnCheckpointAsync(state *State, messages []Message, startIndex, endIndex int, timeout time.Duration) {
	// Snapshot messages immediately so the goroutine sees a consistent view.
	turnMessages := make([]Message, len(messages))
	copy(turnMessages, messages)

	go func() {
		done := make(chan TurnCheckpoint, 1)

		go func() {
			builder := NewTurnSummaryBuilder()
			cp := builder.Build(turnMessages)
			cp.StartIndex = startIndex
			cp.EndIndex = endIndex
			done <- cp
		}()

		select {
		case cp := <-done:
			state.AddCheckpoint(cp)
		case <-time.After(timeout):
			// Store minimal checkpoint if computation timed out.
			state.AddCheckpoint(TurnCheckpoint{
				StartIndex:        startIndex,
				EndIndex:          endIndex,
				Summary:           "Turn completed (summary timed out)",
				ActionableSummary: "Turn completed (summary timed out)",
			})
		}
	}()
}

// isConsumableCheckpoint determines whether a checkpoint can be compacted.
// A checkpoint is consumable when:
// - Its StartIndex and EndIndex are valid within the messages slice
// - StartIndex <= EndIndex
// - The checkpoint has a non-empty Summary
func isConsumableCheckpoint(cp TurnCheckpoint, messageCount int) bool {
	if cp.Summary == "" {
		return false
	}
	if cp.StartIndex < 0 || cp.EndIndex < 0 || cp.StartIndex > cp.EndIndex {
		return false
	}
	if cp.EndIndex >= messageCount {
		return false
	}
	return true
}

// BuildCheckpointCompactedMessages replaces consumed checkpoint ranges with
// summary messages and shifts remaining checkpoint indices accordingly.
// It returns the compacted message list and updated checkpoints.
//
// A checkpoint is "consumable" when:
// - Its StartIndex and EndIndex are valid (within the messages slice)
// - The messages in the range [StartIndex, EndIndex] exist
// - The checkpoint has a non-empty Summary
//
// The function works from oldest to newest checkpoint (by StartIndex):
// 1. For each consumable checkpoint, replace messages[StartIndex:EndIndex+1]
//    with a single summary message (role "user", content from checkpoint.Summary)
// 2. After each replacement, compute the delta (old count - 1) and shift all
//    subsequent checkpoint indices by -delta
// 3. Handle consecutive-assistant boundaries: if the inserted summary message
//    would create two consecutive assistant messages with no tool calls between
//    them, merge the summary into the preceding message or adjust the role.
//
// The summary message role should be "user" to maintain proper conversation flow
// (system -> user -> assistant pattern).
//
// Return:
// - compactedMessages: the new message slice with checkpoints applied
// - updatedCheckpoints: checkpoints with shifted indices; consumed checkpoints
//   are removed from the list
func BuildCheckpointCompactedMessages(messages []Message, checkpoints []TurnCheckpoint) ([]Message, []TurnCheckpoint) {
	// Guard: nothing to do with empty inputs.
	if len(messages) == 0 || len(checkpoints) == 0 {
		outMsgs := make([]Message, len(messages))
		copy(outMsgs, messages)
		outCps := make([]TurnCheckpoint, len(checkpoints))
		copy(outCps, checkpoints)
		return outMsgs, outCps
	}

	// Work on a copy so we never mutate the original.
	msgs := make([]Message, len(messages))
	copy(msgs, messages)

	// Sort a copy of checkpoints by StartIndex so we process oldest first.
	sorted := make([]TurnCheckpoint, len(checkpoints))
	copy(sorted, checkpoints)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].StartIndex < sorted[j].StartIndex
	})

	// Phase 1: Walk through sorted checkpoints and determine which are
	// consumable. For consumable ones, record their summary and range in the
	// original messages array. For non-consumable ones, keep the original
	// checkpoint so it can be returned as-is.
	//
	// We track lastConsumedEnd to reject overlapping ranges.
	type rangeInfo struct {
		summary   string
		origStart int // position in original msgs array
		origEnd   int // inclusive end in original msgs array
		msgCount  int // number of original messages in this range
	}
	var consumables []rangeInfo
	var remaining []TurnCheckpoint
	lastConsumedEnd := -1 // highest original index consumed by a previous checkpoint

	for _, cp := range sorted {
		// Reject if not consumable (invalid range or empty summary).
		if !isConsumableCheckpoint(cp, len(msgs)) {
			remaining = append(remaining, cp)
			continue
		}

		// Reject if this checkpoint overlaps with a previously consumed range.
		if cp.StartIndex <= lastConsumedEnd {
			remaining = append(remaining, cp)
			continue
		}

		msgCount := cp.EndIndex - cp.StartIndex + 1

		ri := rangeInfo{
			summary:   cp.Summary,
			origStart: cp.StartIndex,
			origEnd:   cp.EndIndex,
			msgCount:  msgCount,
		}
		consumables = append(consumables, ri)

		lastConsumedEnd = cp.EndIndex
	}

	// If nothing was consumed, return copies of the original data.
	if len(consumables) == 0 {
		outMsgs := make([]Message, len(msgs))
		copy(outMsgs, msgs)
		outCps := make([]TurnCheckpoint, len(remaining))
		copy(outCps, remaining)
		return outMsgs, outCps
	}

	// Phase 2: Build compacted messages by walking through msgs and
	// replacing consumed ranges with summary messages.
	newMsgs := make([]Message, 0, len(msgs))
	msgIdx := 0
	consIdx := 0

	for msgIdx < len(msgs) {
		if consIdx < len(consumables) && msgIdx == consumables[consIdx].origStart {
			ri := consumables[consIdx]
			newMsgs = append(newMsgs, Message{Role: "user", Content: ri.summary})
			msgIdx = ri.origEnd + 1
			consIdx++
			continue
		}
		newMsgs = append(newMsgs, msgs[msgIdx])
		msgIdx++
	}

	// Phase 3: Defensive consecutive-assistant check.
	// Since we insert summaries with role "user", this should rarely fire,
	// but guard against edge cases (e.g. ranges containing only assistant msgs).
	for i := 1; i < len(newMsgs); i++ {
		if newMsgs[i-1].Role == "assistant" && newMsgs[i].Role == "assistant" {
			newMsgs[i-1].Content += "\n" + newMsgs[i].Content
			newMsgs = append(newMsgs[:i], newMsgs[i+1:]...)
			i--
		}
	}

	// Phase 4: Compute new indices for remaining (unconsumed) checkpoints.
	//
	// For each remaining checkpoint, we need to map its original [StartIndex,
	// EndIndex] into the compacted message array. Two things can happen:
	//
	//  1. A consumed range is entirely before the checkpoint — its messages
	//     were removed, shifting the checkpoint left by (msgCount-1).
	//  2. A consumed range overlaps the start of the checkpoint — the consumed
	//     messages are gone, so the checkpoint's start advances past them.
	//
	for i := range remaining {
		s := remaining[i].StartIndex
		e := remaining[i].EndIndex

		// Trim any consumed range that eats into this checkpoint.
		for _, ri := range consumables {
			// Consumed range overlaps the start?
			if ri.origEnd >= s && ri.origEnd < e {
				s = ri.origEnd + 1 // advance start past consumed range
			}
			if ri.origStart <= e && ri.origEnd >= e {
				e = ri.origStart - 1 // retreat end before consumed range
			}
		}

		if s > e || s < 0 || e < 0 {
			// Checkpoint entirely consumed — clamp to invalid.
			remaining[i].StartIndex = -1
			remaining[i].EndIndex = -1
			continue
		}

		// Compute shift amount: sum of (msgCount-1) for consumed ranges
		// entirely before the (possibly trimmed) index.
		shift := func(idx int) int {
			d := 0
			for _, ri := range consumables {
				if ri.origEnd < idx {
					d += ri.msgCount - 1
				}
			}
			return d
		}

		remaining[i].StartIndex = s - shift(s)
		remaining[i].EndIndex = e - shift(e)

		// Clamp to valid range.
		if remaining[i].StartIndex < 0 {
			remaining[i].StartIndex = 0
		}
		if remaining[i].EndIndex < 0 {
			remaining[i].EndIndex = 0
		}
		if len(newMsgs) > 0 && remaining[i].EndIndex >= len(newMsgs) {
			remaining[i].EndIndex = len(newMsgs) - 1
		}
	}

	return newMsgs, remaining
}
