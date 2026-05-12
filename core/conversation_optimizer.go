package core

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ToolCategory classifies a tool for optimization purposes.
type ToolCategory int

const (
	// ToolCategoryUnknown means the optimizer should skip this tool.
	ToolCategoryUnknown ToolCategory = iota
	// ToolCategoryFileRead indicates a tool that reads file contents.
	ToolCategoryFileRead
	// ToolCategoryShellCommand indicates a tool that runs shell commands.
	ToolCategoryShellCommand
)

// maxFileReadRecords bounds the number of historical reads tracked per file.
// This keeps the dedup comparison window O(n) amortized.
const maxFileReadRecords = 5

// ConversationOptimizerOptions configures the optimizer.
type ConversationOptimizerOptions struct {
	// Enabled enables optimization. When false, OptimizeConversation is a no-op.
	Enabled bool
	// KnownToolFn classifies tool names. Return ToolCategoryUnknown to skip a tool.
	// If nil, the optimizer treats all tools as unknown (no optimization).
	// Must be deterministic: the same tool name should always return the same category.
	KnownToolFn func(name string) ToolCategory
}

// ConversationOptimizer reduces redundant conversation history by deduplicating
// repeated file reads and transient shell command outputs. It mutates the
// provided message slice in place (replacing tool-result Content with placeholders)
// but is safe to use because prepareMessages() only ever passes it ephemeral copies
// of state — the stored conversation is never modified.
type ConversationOptimizer struct {
	enabled     bool
	knownToolFn func(name string) ToolCategory
}

// NewConversationOptimizer creates a new optimizer from the given options.
func NewConversationOptimizer(opts ConversationOptimizerOptions) *ConversationOptimizer {
	return &ConversationOptimizer{
		enabled:     opts.Enabled,
		knownToolFn: opts.KnownToolFn,
	}
}

// OptimizeConversation processes the message list, replacing redundant file reads
// and shell command outputs with compact placeholders. Returns the (possibly
// modified) message slice.
func (opt *ConversationOptimizer) OptimizeConversation(messages []Message) []Message {
	if !opt.enabled || opt.knownToolFn == nil {
		return messages
	}

	// Build a map of tool-call ID → metadata extracted from the assistant message.
	type meta struct {
		category ToolCategory
		filePath string
		command  string
	}
	callMeta := make(map[string]meta)
	for _, msg := range messages {
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		for _, tc := range msg.ToolCalls {
			cat := opt.knownToolFn(tc.Function.Name)
			m := meta{category: cat}
			switch cat {
			case ToolCategoryFileRead:
				m.filePath = extractStringArg(tc.Function.Arguments, "path")
			case ToolCategoryShellCommand:
				m.command = extractStringArg(tc.Function.Arguments, "cmd")
			}
			callMeta[tc.ID] = m
		}
	}

	// --- File-read deduplication ---
	// Track: filepath → map[content] → list of message indices.
	// Only replaces earlier reads that have identical content to a later read.
	type readRecord struct {
		index int
	}
	fileReads := make(map[string]map[string][]readRecord)

	// --- Shell-command deduplication ---
	// Track: full command string → map[content] → latest messageIndex.
	// Only replaces earlier output that matches the latest output for transient commands.
	shellCmdLatest := make(map[string]map[string]int)

	for i, msg := range messages {
		if msg.Role != "tool" || msg.ToolCallID == "" {
			continue
		}

		m, ok := callMeta[msg.ToolCallID]
		if !ok {
			continue
		}

		switch m.category {
		case ToolCategoryFileRead:
			if m.filePath == "" {
				continue
			}

			byFile, ok := fileReads[m.filePath]
			if !ok {
				byFile = make(map[string][]readRecord)
				fileReads[m.filePath] = byFile
			}

			// Replace any earlier read of the same file that has identical content.
			records, ok := byFile[msg.Content]
			if ok {
				for _, rec := range records {
					messages[rec.index].Content = fmt.Sprintf("[Earlier file read: %s]", m.filePath)
				}
			}

			byFile[msg.Content] = append(records, readRecord{index: i})

			// Bound the records per file to keep comparisons O(n) amortized.
			for content, recs := range byFile {
				if len(recs) > maxFileReadRecords {
					byFile[content] = recs[len(recs)-maxFileReadRecords:]
				}
			}

		case ToolCategoryShellCommand:
			if m.command == "" {
				continue
			}
			base := baseCommand(m.command)
			if !isTransientCommand(base) {
				continue
			}

			byCmd, ok := shellCmdLatest[m.command]
			if !ok {
				byCmd = make(map[string]int)
				shellCmdLatest[m.command] = byCmd
			}

			// If there's a previous output with the same content, replace it.
			if prevIdx, exists := byCmd[msg.Content]; exists {
				messages[prevIdx].Content = fmt.Sprintf("[Earlier command output: %s]", m.command)
			}
			// Record this output as the latest for its content.
			byCmd[msg.Content] = i
		}
	}

	return messages
}

// extractStringArg parses the JSON arguments and returns the string value for
// the given key, or "" if not found / not a string.
func extractStringArg(argsJSON, key string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// baseCommand extracts the first word of a shell command string.
func baseCommand(cmd string) string {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// isTransientCommand returns true for commands whose output is inherently
// volatile and can be safely deduplicated (keep only the latest). Only
// genuinely transient commands are included; commands that read file contents
// (cat, find, head, tail, wc) are excluded because their output carries
// meaningful context that should not be discarded.
func isTransientCommand(base string) bool {
	switch base {
	case "ls", "pwd", "echo":
		return true
	}
	return false
}
