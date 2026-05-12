package core

import (
	"strings"
	"testing"
	"time"
)

func TestBuildCheckpointSummary_SimpleTurn(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "What is the capital of France?"},
		{Role: "assistant", Content: "The capital of France is Paris."},
	}

	cp := BuildCheckpointSummary(messages)

	if cp.StartIndex != 0 {
		t.Errorf("expected StartIndex 0, got %d", cp.StartIndex)
	}
	if cp.EndIndex != 1 {
		t.Errorf("expected EndIndex 1, got %d", cp.EndIndex)
	}
	if !strings.Contains(cp.Summary, "User asked") {
		t.Errorf("summary should mention user question: %s", cp.Summary)
	}
	if !strings.Contains(cp.Summary, "Paris") {
		t.Errorf("summary should mention response content: %s", cp.Summary)
	}
	if !strings.Contains(cp.ActionableSummary, "Question") {
		t.Errorf("actionable summary should mention question: %s", cp.ActionableSummary)
	}
}

func TestBuildCheckpointSummary_WithToolCalls(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Read config.yaml"},
		{Role: "assistant", Content: "Let me read that file.", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{
				Name:      "read_file",
				Arguments: `{"path":"config.yaml"}`,
			}},
		}},
		{Role: "tool", Content: "host: localhost\nport: 5432", ToolCallID: "call_1"},
		{Role: "assistant", Content: "The config has host localhost on port 5432."},
	}

	cp := BuildCheckpointSummary(messages)

	if !strings.Contains(cp.Summary, "read_file") {
		t.Errorf("summary should mention read_file tool: %s", cp.Summary)
	}
	if !strings.Contains(cp.Summary, "config.yaml") {
		t.Errorf("summary should mention config.yaml: %s", cp.Summary)
	}
	if !strings.Contains(cp.ActionableSummary, "Read: config.yaml") {
		t.Errorf("actionable summary should list file read: %s", cp.ActionableSummary)
	}
}

func TestBuildCheckpointSummary_WithFileWrites(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Create a new file"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{
				Name:      "write_file",
				Arguments: `{"path":"output.txt","content":"hello"}`,
			}},
		}},
		{Role: "tool", Content: "File written successfully", ToolCallID: "call_1"},
		{Role: "assistant", Content: "File created."},
	}

	cp := BuildCheckpointSummary(messages)

	if !strings.Contains(cp.Summary, "Modified files") {
		t.Errorf("summary should mention modified files: %s", cp.Summary)
	}
	if !strings.Contains(cp.ActionableSummary, "Modified: output.txt") {
		t.Errorf("actionable summary should list modified file: %s", cp.ActionableSummary)
	}
}

func TestBuildCheckpointSummary_WithShellCommands(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "List files in directory"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{
				Name:      "shell",
				Arguments: `{"cmd":"ls -la /tmp"}`,
			}},
		}},
		{Role: "tool", Content: "total 8\ndrwxrwxrwt 2 root root 4096 Jan 1 00:00 .", ToolCallID: "call_1"},
		{Role: "assistant", Content: "Directory listing shows 8 total items."},
	}

	cp := BuildCheckpointSummary(messages)

	if !strings.Contains(cp.Summary, "shell") {
		t.Errorf("summary should mention shell tool: %s", cp.Summary)
	}
	if !strings.Contains(cp.Summary, "ls -la /tmp") {
		t.Errorf("summary should mention command: %s", cp.Summary)
	}
	if !strings.Contains(cp.ActionableSummary, "Command: ls -la /tmp") {
		t.Errorf("actionable summary should list command: %s", cp.ActionableSummary)
	}
}

func TestBuildCheckpointSummary_WithError(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Read missing file"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{
				Name:      "read_file",
				Arguments: `{"path":"nonexistent.txt"}`,
			}},
		}},
		{Role: "tool", Content: "error: file not found", ToolCallID: "call_1"},
		{Role: "assistant", Content: "The file doesn't exist."},
	}

	cp := BuildCheckpointSummary(messages)

	if !strings.Contains(cp.Summary, "error") {
		t.Errorf("summary should mention errors: %s", cp.Summary)
	}
	if !strings.Contains(cp.ActionableSummary, "Error") {
		t.Errorf("actionable summary should mention error: %s", cp.ActionableSummary)
	}
}

func TestBuildCheckpointSummary_MultipleToolCalls(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Read two files"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{
				Name:      "read_file",
				Arguments: `{"path":"a.txt"}`,
			}},
			{ID: "call_2", Function: ToolCallFunction{
				Name:      "read_file",
				Arguments: `{"path":"b.txt"}`,
			}},
		}},
		{Role: "tool", Content: "content a", ToolCallID: "call_1"},
		{Role: "tool", Content: "content b", ToolCallID: "call_2"},
		{Role: "assistant", Content: "Both files read successfully."},
	}

	cp := BuildCheckpointSummary(messages)

	// Should mention read_file (2x)
	if !strings.Contains(cp.Summary, "read_file") {
		t.Errorf("summary should mention read_file: %s", cp.Summary)
	}
	// Should list both files in actionable summary
	if !strings.Contains(cp.ActionableSummary, "Read: a.txt") {
		t.Errorf("actionable summary should list a.txt: %s", cp.ActionableSummary)
	}
	if !strings.Contains(cp.ActionableSummary, "Read: b.txt") {
		t.Errorf("actionable summary should list b.txt: %s", cp.ActionableSummary)
	}
}

func TestBuildCheckpointSummary_TruncatedResponse(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Explain something"},
		{Role: "assistant", Content: "Here is the explanation..."},
	}

	cp := BuildCheckpointSummary(messages)

	// Partial status due to trailing "..."
	if !strings.Contains(cp.Summary, "partial") {
		t.Errorf("summary should indicate partial status: %s", cp.Summary)
	}
}

func TestBuildCheckpointSummary_LongResponse(t *testing.T) {
	longContent := strings.Repeat("word ", 200) + "end"
	messages := []Message{
		{Role: "user", Content: "Long question"},
		{Role: "assistant", Content: longContent},
	}

	cp := BuildCheckpointSummary(messages)

	// Summary should be truncated
	if len(cp.Summary) > 500 {
		t.Errorf("summary should be reasonably short, got %d chars", len(cp.Summary))
	}
	// Actionable summary response should be truncated
	if !strings.Contains(cp.ActionableSummary, "...") {
		t.Errorf("actionable summary should truncate long response: %s", cp.ActionableSummary)
	}
}

func TestBuildCheckpointSummary_MixedOperations(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Refactor the code"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{
				Name:      "read_file",
				Arguments: `{"path":"src/main.go"}`,
			}},
		}},
		{Role: "tool", Content: "package main\nfunc main() {}", ToolCallID: "call_1"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_2", Function: ToolCallFunction{
				Name:      "edit_file",
				Arguments: `{"path":"src/main.go","old_str":"func main() {}","new_str":"func main() { println(\"hi\") }"}`,
			}},
		}},
		{Role: "tool", Content: "File edited successfully", ToolCallID: "call_2"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_3", Function: ToolCallFunction{
				Name:      "shell",
				Arguments: `{"cmd":"go build ./..."}`,
			}},
		}},
		{Role: "tool", Content: "Build succeeded", ToolCallID: "call_3"},
		{Role: "assistant", Content: "Refactored and verified the build."},
	}

	cp := BuildCheckpointSummary(messages)

	// Should mention reading and modifying files
	if !strings.Contains(cp.Summary, "Read files") {
		t.Errorf("summary should mention file reads: %s", cp.Summary)
	}
	if !strings.Contains(cp.Summary, "Modified files") {
		t.Errorf("summary should mention file modifications: %s", cp.Summary)
	}
	if !strings.Contains(cp.Summary, "shell") {
		t.Errorf("summary should mention shell command: %s", cp.Summary)
	}

	// Actionable summary should list operations
	if !strings.Contains(cp.ActionableSummary, "Read: src/main.go") {
		t.Errorf("actionable summary should list file read: %s", cp.ActionableSummary)
	}
	if !strings.Contains(cp.ActionableSummary, "Modified: src/main.go") {
		t.Errorf("actionable summary should list file modification: %s", cp.ActionableSummary)
	}
	if !strings.Contains(cp.ActionableSummary, "Command: go build ./...") {
		t.Errorf("actionable summary should list command: %s", cp.ActionableSummary)
	}
}

func TestBuildCheckpointSummary_EmptyMessages(t *testing.T) {
	cp := BuildCheckpointSummary([]Message{})

	if cp.StartIndex != 0 {
		t.Errorf("expected StartIndex 0, got %d", cp.StartIndex)
	}
	if cp.EndIndex != -1 {
		t.Errorf("expected EndIndex -1 for empty, got %d", cp.EndIndex)
	}
}

func TestBuildCheckpointSummary_OnlyUserMessage(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Hello"},
	}

	cp := BuildCheckpointSummary(messages)

	if !strings.Contains(cp.Summary, "User asked") {
		t.Errorf("summary should mention user question: %s", cp.Summary)
	}
}

func TestTurnSummaryBuilder_CustomFileTools(t *testing.T) {
	builder := NewTurnSummaryBuilder()
	builder.KnownFileTools = map[string]bool{
		"custom_read": true,
	}

	messages := []Message{
		{Role: "user", Content: "Read with custom tool"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Function: ToolCallFunction{
				Name:      "custom_read",
				Arguments: `{"path":"custom.txt"}`,
			}},
		}},
		{Role: "tool", Content: "content", ToolCallID: "call_1"},
		{Role: "assistant", Content: "Done."},
	}

	cp := builder.Build(messages)

	if !strings.Contains(cp.Summary, "custom.txt") {
		t.Errorf("summary should mention custom file: %s", cp.Summary)
	}
}

func TestExtractFilePath(t *testing.T) {
	tests := []struct {
		name     string
		args     string
		expected string
	}{
		{"path key", `{"path":"src/main.go"}`, "src/main.go"},
		{"file key", `{"file":"data.json"}`, "data.json"},
		{"filename key", `{"filename":"test.txt"}`, "test.txt"},
		{"no path", `{"cmd":"ls"}`, ""},
		{"empty", `{}`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractFilePath(tt.args)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestExtractShellCommand(t *testing.T) {
	tests := []struct {
		name     string
		args     string
		expected string
	}{
		{"cmd key", `{"cmd":"ls -la"}`, "ls -la"},
		{"command key", `{"command":"go build"}`, "go build"},
		{"no command", `{"path":"file.txt"}`, ""},
		{"empty", `{}`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractShellCommand(tt.args)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{"short", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"truncated", "hello world foo bar", 10, "hello..."},
		{"truncated mid-word", "abcdefghij", 5, "abcde..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateString(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestUniqueStrings(t *testing.T) {
	input := []string{"a", "b", "a", "c", "b"}
	expected := []string{"a", "b", "c"}

	result := uniqueStrings(input)
	if len(result) != len(expected) {
		t.Fatalf("expected %d items, got %d", len(expected), len(result))
	}
	for i, v := range result {
		if v != expected[i] {
			t.Errorf("index %d: expected %q, got %q", i, expected[i], v)
		}
	}
}

func TestRecordTurnCheckpointAsync(t *testing.T) {
	state := NewState()

	messages := []Message{
		{Role: "user", Content: "Test query"},
		{Role: "assistant", Content: "Test response."},
	}

	RecordTurnCheckpointAsync(state, messages, 0, 1, 5*time.Second)

	// Wait for async completion
	time.Sleep(100 * time.Millisecond)

	checkpoints := state.GetCheckpoints()
	if len(checkpoints) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(checkpoints))
	}

	cp := checkpoints[0]
	if cp.StartIndex != 0 {
		t.Errorf("expected StartIndex 0, got %d", cp.StartIndex)
	}
	if cp.EndIndex != 1 {
		t.Errorf("expected EndIndex 1, got %d", cp.EndIndex)
	}
	if !strings.Contains(cp.Summary, "Test query") {
		t.Errorf("summary should mention query: %s", cp.Summary)
	}
}

func TestRecordTurnCheckpointAsync_Timeout(t *testing.T) {
	state := NewState()

	// Very large message set to test timeout path
	messages := make([]Message, 0, 1000)
	for i := 0; i < 500; i++ {
		messages = append(messages, Message{Role: "user", Content: strings.Repeat("x", 1000)})
		messages = append(messages, Message{Role: "assistant", Content: strings.Repeat("y", 1000)})
	}

	RecordTurnCheckpointAsync(state, messages, 0, len(messages)-1, 1*time.Millisecond)

	// Wait for async completion
	time.Sleep(100 * time.Millisecond)

	checkpoints := state.GetCheckpoints()
	if len(checkpoints) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(checkpoints))
	}

	// Even on timeout, checkpoint should be stored
	cp := checkpoints[0]
	if cp.StartIndex != 0 {
		t.Errorf("expected StartIndex 0, got %d", cp.StartIndex)
	}
}

func TestRecordTurnCheckpointAsync_SnapshotIsolation(t *testing.T) {
	state := NewState()

	messages := []Message{
		{Role: "user", Content: "Original query"},
		{Role: "assistant", Content: "Original response."},
	}

	RecordTurnCheckpointAsync(state, messages, 0, 1, 5*time.Second)

	// Mutate the original slice immediately after calling.
	// The async goroutine should see the snapshot, not these mutations.
	messages[0].Content = "Mutated query"
	messages[1].Content = "Mutated response."
	messages = append(messages, Message{Role: "user", Content: "Extra"})

	// Wait for async completion.
	time.Sleep(200 * time.Millisecond)

	checkpoints := state.GetCheckpoints()
	if len(checkpoints) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(checkpoints))
	}

	cp := checkpoints[0]
	// Summary should reference the original content, not the mutated content.
	if strings.Contains(cp.Summary, "Mutated") {
		t.Errorf("summary should use snapshot, not mutated data: %s", cp.Summary)
	}
	if !strings.Contains(cp.Summary, "Original") {
		t.Errorf("summary should contain original content: %s", cp.Summary)
	}
}

func TestIsFileWriteTool(t *testing.T) {
	writeTools := []string{"write_file", "edit_file", "create_file", "delete_file", "patch_file", "append_file", "write_structured", "patch_structured"}
	readTools := []string{"read_file", "list_files", "search_files", "glob_files"}

	for _, tool := range writeTools {
		if !isFileWriteTool(tool) {
			t.Errorf("expected %s to be a write tool", tool)
		}
	}
	for _, tool := range readTools {
		if isFileWriteTool(tool) {
			t.Errorf("expected %s to NOT be a write tool", tool)
		}
	}
}

func TestDetermineStatus(t *testing.T) {
	builder := NewTurnSummaryBuilder()

	// Completed status
	completedData := turnData{
		userQuestion:  "What is 2+2?",
		finalResponse: "The answer is 4.",
	}
	if builder.determineStatus(completedData) != statusCompleted {
		t.Errorf("expected completed status, got %v", builder.determineStatus(completedData))
	}

	// Partial status (trailing ...)
	partialData := turnData{
		userQuestion:  "Explain",
		finalResponse: "Here is the explanation...",
	}
	if builder.determineStatus(partialData) != statusPartial {
		t.Errorf("expected partial status, got %v", builder.determineStatus(partialData))
	}

	// Interrupted status (no final response but tool calls)
	interruptedData := turnData{
		userQuestion: "Do something",
		toolCalls:    []toolCallInfo{{name: "shell", arguments: `{"cmd":"ls"}`}},
	}
	if builder.determineStatus(interruptedData) != statusInterrupted {
		t.Errorf("expected interrupted status, got %v", builder.determineStatus(interruptedData))
	}

	// Error status (errors but no final response)
	errorData := turnData{
		userQuestion: "Fail",
		errors:       []string{"error: something failed"},
	}
	if builder.determineStatus(errorData) != statusError {
		t.Errorf("expected error status, got %v", builder.determineStatus(errorData))
	}
}
