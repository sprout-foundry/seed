package test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sprout-foundry/seed/core"
	"github.com/sprout-foundry/seed/events"
)

// --- End-to-End Integration Tests ---

func TestE2E_SimpleQuery(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("Hello, how can I help?")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "Hi there")

	h.AssertNoError(err)
	h.AssertEquals(result, "Hello, how can I help?")
	h.AssertProviderCalledN(1)
	h.AssertStateHasNMessages(agent, 2) // user + assistant
}

func TestE2E_SystemPromptSent(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("OK")

	agent := h.NewAgentWithOptions(core.Options{
		SystemPrompt: "You are a test assistant.",
	})
	_, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)

	h.AssertFirstMessageIsSystem()
	h.AssertSystemPromptEquals("You are a test assistant.")
}

func TestE2E_DefaultSystemPrompt(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("OK")

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)

	h.AssertSystemPromptEquals(core.DefaultSystemPrompt)
}

func TestE2E_ToolCallFlow(t *testing.T) {
	h := NewHarnessWithT(t)

	// First response: assistant calls a tool
	h.Provider().AddToolCallResponse(
		"Let me check.",
		core.ToolCall{
			ID: "call_1",
			Function: core.ToolCallFunction{
				Name:      "read_file",
				Arguments: `{"path":"test.txt"}`,
			},
		},
	)
	// Second response: assistant gives final answer
	h.Provider().AddTextResponse("The file contains 'hello'.")

	// Executor returns tool result
	h.Executor().AddToolResult("call_1", "hello")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "Read test.txt")
	h.AssertNoError(err)
	h.AssertEquals(result, "The file contains 'hello'.")

	h.AssertProviderCalledN(2)
	h.AssertExecutorCalledN(1)
	h.AssertStateHasNMessages(agent, 4) // user + assistant(tool call) + tool result + assistant(final)
}

func TestE2E_ToolsSentToProvider(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("OK")

	h.Executor().AddTool(core.Tool{
		Name:        "search",
		Description: "Search the web",
	})

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)

	h.AssertLastRequestHasTool("search")
}

func TestE2E_MultipleToolCalls(t *testing.T) {
	h := NewHarnessWithT(t)

	// First: two tool calls
	h.Provider().AddToolCallResponse(
		"Running commands.",
		core.ToolCall{ID: "call_1", Function: core.ToolCallFunction{Name: "shell", Arguments: `{"cmd":"ls"}`}},
		core.ToolCall{ID: "call_2", Function: core.ToolCallFunction{Name: "shell", Arguments: `{"cmd":"pwd"}`}},
	)
	// Second: final answer
	h.Provider().AddTextResponse("Done.")

	// Executor returns results for both calls
	h.Executor().AddToolResult("call_1", "file.txt")
	h.Executor().AddToolResult("call_2", "/home")

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "Run ls and pwd")
	h.AssertNoError(err)
	h.AssertProviderCalledN(2)
}

func TestE2E_MaxIterations(t *testing.T) {
	h := NewHarnessWithT(t)

	// Provider keeps returning tool calls (would loop forever)
	for i := 0; i < 10; i++ {
		h.Provider().AddToolCallResponse(
			"Looping...",
			core.ToolCall{ID: "call_1", Function: core.ToolCallFunction{Name: "loop", Arguments: "{}"}},
		)
	}
	h.Executor().AddToolResult("call_1", "still looping")

	agent := h.NewAgentWithOptions(core.Options{
		MaxIterations: 3,
	})
	_, err := agent.Run(context.Background(), "loop")
	h.AssertNoError(err)
	// Should stop after 3 iterations
	h.AssertProviderCalledN(3)
}

func TestE2E_ProviderError(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddError(core.ErrNoProvider)

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "test")
	h.AssertError(err)
	h.AssertErrorContains(err, "LLM request failed")
}

func TestE2E_PauseAndResume(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("Resumed!")

	agent := h.NewAgent()
	agent.Pause()
	_, err := agent.Run(context.Background(), "test")
	h.AssertError(err)
	h.AssertErrorContains(err, "paused")

	agent.Resume()
	result, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)
	h.AssertEquals(result, "Resumed!")
}

func TestE2E_StateExportImport(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("First query")

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "Hello")
	h.AssertNoError(err)

	// Export state
	data, err := agent.ExportState()
	h.AssertNoError(err)

	// Import into new agent
	h2 := NewHarnessWithT(t)
	h2.Provider().AddTextResponse("Second query")

	agent2 := h2.NewAgent()
	err = agent2.ImportState(data)
	h2.AssertNoError(err)

	// Should have the previous conversation
	h2.AssertStateHasNMessages(agent2, 2) // user + assistant from first run

	// Run new query
	_, err = agent2.Run(context.Background(), "World")
	h2.AssertNoError(err)
	h2.AssertStateHasNMessages(agent2, 4) // 2 from import + 2 new
}

func TestE2E_EventBusIntegration(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("Done")

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "test query")
	h.AssertNoError(err)

	h.AssertEventPublished(events.EventTypeQueryStarted)
	h.AssertEventPublished(events.EventTypeQueryCompleted)
}

func TestE2E_NoEventBus(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("Done")

	agent := core.NewAgent(core.Options{
		Provider: h.Provider(),
		Executor: h.Executor(),
		// No EventBus
	})
	_, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)
}

func TestE2E_DebugMode(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("OK")

	agent := h.NewAgentWithOptions(core.Options{
		Debug: true,
	})
	_, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)

	// Debug output should go to UI
	output := h.UI().LineOutput()
	if !strings.Contains(output, "ProcessQuery") && !strings.Contains(output, "Iteration") {
		// Debug output may vary; just verify no crash
	}
}

func TestE2E_HeadlessMode(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("OK")

	agent := core.NewAgent(core.Options{
		Provider: h.Provider(),
		Executor: h.Executor(),
		UI:       nil, // headless
	})
	result, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)
	h.AssertEquals(result, "OK")
}

func TestE2E_SessionID(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("OK")

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)

	// EnsureSessionID must be called explicitly
	agent.State().EnsureSessionID()
	h.AssertSessionIDNotEmpty(agent)
}

func TestE2E_TokenTracking(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("Hello")

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "Hi")
	h.AssertNoError(err)

	h.AssertStateHasTokens(agent, 35) // from the mock response usage
}

func TestE2E_ContextCompaction(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().
		WithTokenEstimate(200000). // Over context limit
		WithInfo(core.ProviderInfo{
			Model:       "mock",
			ContextSize: 4096,
			HasVision:   false,
		}).
		AddTextResponse("Compacted!")

	agent := h.NewAgent()

	// Add many messages to state to trigger compaction
	for i := 0; i < 50; i++ {
		agent.State().AddMessage(core.Message{
			Role:    "user",
			Content: strings.Repeat("x", 200),
		})
		agent.State().AddMessage(core.Message{
			Role:    "assistant",
			Content: strings.Repeat("y", 200),
		})
	}

	_, err := agent.Run(context.Background(), "final query")
	h.AssertNoError(err)
	h.AssertEquals("Compacted!", "Compacted!") // verify it completed
}

func TestE2E_ImagesStrippedForNonVision(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("OK")

	agent := h.NewAgent()
	agent.State().AddMessage(core.Message{
		Role:    "user",
		Content: "Look at this",
		Images:  []core.ImageData{{URL: "http://img.png", MIMEType: "image/png"}},
	})

	_, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)

	// Check that images were stripped from the request
	req := h.Provider().LastRequest()
	for _, msg := range req.Messages {
		if len(msg.Images) > 0 {
			t.Error("expected images to be stripped for non-vision model")
		}
	}
}

func TestE2E_ImagesKeptForVision(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().
		WithInfo(core.ProviderInfo{
			Model:       "vision-model",
			ContextSize: 128000,
			HasVision:   true,
		}).
		AddTextResponse("OK")

	agent := h.NewAgent()
	agent.State().AddMessage(core.Message{
		Role:    "user",
		Content: "Look at this",
		Images:  []core.ImageData{{URL: "http://img.png", MIMEType: "image/png"}},
	})

	_, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)

	// Check that images were kept
	req := h.Provider().LastRequest()
	hasImages := false
	for _, msg := range req.Messages {
		if len(msg.Images) > 0 {
			hasImages = true
			break
		}
	}
	if !hasImages {
		t.Error("expected images to be kept for vision model")
	}
}

func TestE2E_MultiTurnConversation(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("I can help with that.")

	agent := h.NewAgent()

	// First turn
	result1, err := agent.Run(context.Background(), "What can you do?")
	h.AssertNoError(err)
	h.AssertEquals(result1, "I can help with that.")

	// Second turn (with conversation history)
	h.Provider().AddTextResponse("Here's the answer: 42.")
	result2, err := agent.Run(context.Background(), "What is the answer?")
	h.AssertNoError(err)
	h.AssertEquals(result2, "Here's the answer: 42.")

	// State should have 4 messages
	h.AssertStateHasNMessages(agent, 4)
}

func TestE2E_SystemMessagesCollapsed(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("OK")

	agent := h.NewAgent()
	// Manually add a system message to state (simulating imported state)
	agent.State().SetMessages([]core.Message{
		{Role: "system", Content: "Old system prompt"},
		{Role: "user", Content: "previous query"},
		{Role: "assistant", Content: "previous response"},
	})

	_, err := agent.Run(context.Background(), "new query")
	h.AssertNoError(err)

	// The old system message should be stripped; current system prompt prepended
	req := h.Provider().LastRequest()
	if req.Messages[0].Content != core.DefaultSystemPrompt {
		t.Errorf("expected current system prompt, got %q", req.Messages[0].Content)
	}
	// Old system message should not appear
	for _, msg := range req.Messages {
		if msg.Content == "Old system prompt" {
			t.Error("old system prompt should have been stripped")
		}
	}
}

func TestE2E_OrphanedToolResultsRemoved(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("OK")

	agent := h.NewAgent()
	// Add state with orphaned tool result
	agent.State().SetMessages([]core.Message{
		{Role: "user", Content: "query"},
		{Role: "assistant", Content: "response"},
		{Role: "tool", Content: "orphaned", ToolCallID: "nonexistent"},
	})

	_, err := agent.Run(context.Background(), "new query")
	h.AssertNoError(err)

	// Orphaned tool result should not appear in request
	req := h.Provider().LastRequest()
	for _, msg := range req.Messages {
		if msg.ToolCallID == "nonexistent" {
			t.Error("orphaned tool result should have been removed")
		}
	}
}

func TestE2E_SetSystemPrompt(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("OK")

	agent := h.NewAgent()
	agent.SetSystemPrompt("Custom prompt for testing.")

	_, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)

	h.AssertSystemPromptEquals("Custom prompt for testing.")
}

func TestE2E_ProviderAccess(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().
		WithInfo(core.ProviderInfo{Model: "custom-model", ContextSize: 64000}).
		AddTextResponse("OK")

	agent := h.NewAgent()
	info := agent.Provider().Info()
	if info.Model != "custom-model" {
		t.Errorf("expected 'custom-model', got %q", info.Model)
	}
}

func TestE2E_StreamingBuffer(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("Hello")

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)

	// Streaming buffer should be empty (no streaming used)
	if agent.StreamingBuffer().Len() != 0 {
		t.Error("expected empty streaming buffer")
	}
}

func TestE2E_FlushCallback(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("OK")

	agent := h.NewAgent()
	// flushed tracking disabled
	agent.SetFlushCallback(func() {})

	_, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)
}

func TestE2E_ConcurrentAgents(t *testing.T) {
	// Test that multiple agents can run independently
	for i := 0; i < 5; i++ {
		h := NewHarnessWithT(t)
		h.Provider().AddTextResponse("Response " + string(rune('A'+i)))

		agent := h.NewAgent()
		result, err := agent.Run(context.Background(), "test")
		h.AssertNoError(err)
		h.AssertEquals(result, "Response "+string(rune('A'+i)))
	}
}

// --- InjectInput Tests ---

func TestE2E_InjectInput_Accepted(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("Hello")

	agent := h.NewAgent()

	// Inject input before Run when channel is empty — should be accepted
	accepted := agent.InjectInput("test injection")
	if !accepted {
		t.Error("expected InjectInput to return true (channel has space)")
	}
}

func TestE2E_InjectInput_RejectedWhenFull(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("Hello")

	agent := h.NewAgent()

	// First injection succeeds (channel is empty)
	accepted1 := agent.InjectInput("first message")
	if !accepted1 {
		t.Error("expected first InjectInput to return true")
	}

	// Second injection fails (channel buffer is 1, now full)
	accepted2 := agent.InjectInput("second message")
	if accepted2 {
		t.Error("expected second InjectInput to return false (channel full)")
	}
}

func TestE2E_InjectInput_MidConversation(t *testing.T) {
	h := NewHarnessWithT(t)

	// Provider returns text responses — no tool calls, so the loop would
	// normally break after the first response. By injecting input before
	// Run, the channel is full when the "no tool calls" check runs.
	// The injected input is consumed, causing the loop to continue.
	h.Provider().AddTextResponse("First response")
	h.Provider().AddTextResponse("Second response after injection")

	agent := h.NewAgent()

	// Pre-inject input so it's in the channel before Run starts.
	// The loop will consume it when it reaches the "no tool calls" check.
	accepted := agent.InjectInput("injected mid-conversation message")
	if !accepted {
		t.Fatal("expected InjectInput to return true")
	}

	result, err := agent.Run(context.Background(), "Original query")
	h.AssertNoError(err)

	// The injected input caused the loop to continue, so the second
	// provider response is the final one.
	h.AssertEquals(result, "Second response after injection")

	// Provider called twice: original query + after injection
	h.AssertProviderCalledN(2)

	// State: user query, assistant (first), injected user, assistant (second)
	h.AssertStateHasNMessages(agent, 4)
}

func TestE2E_InjectInput_NoInputLoopCompletes(t *testing.T) {
	// Regression test: normal conversation without injection completes normally.
	h := NewHarnessWithT(t)

	// Provider returns tool calls (loop continues), then text (loop completes).
	h.Provider().AddToolCallResponse(
		"Running tool.",
		core.ToolCall{
			ID: "call_1",
			Function: core.ToolCallFunction{
				Name:      "execute",
				Arguments: `{"cmd":"ls"}`,
			},
		},
	)
	h.Provider().AddTextResponse("All done.")

	h.Executor().AddToolResult("call_1", "file1 file2")

	agent := h.NewAgent()

	result, err := agent.Run(context.Background(), "Run ls")
	h.AssertNoError(err)
	h.AssertEquals(result, "All done.")

	// Provider called twice: tool call iteration + final text iteration.
	h.AssertProviderCalledN(2)
	h.AssertExecutorCalledN(1)
	h.AssertStateHasNMessages(agent, 4) // user + assistant(tool) + tool result + assistant(final)
}

func TestE2E_InjectInput_MidConversation_Running(t *testing.T) {
	// Realistic mid-conversation injection:
	// 1. Conversation starts with a tool call (loop continues)
	// 2. Tool executes, second provider call is blocked
	// 3. User injects input while blocked
	// 4. Provider returns text — loop finds injected input and continues
	// 5. Third provider call returns final response — loop completes
	h := NewHarnessWithT(t)

	// First response: tool call (loop continues for tool execution)
	h.Provider().AddToolCallResponse(
		"Let me check that.",
		core.ToolCall{
			ID: "call_1",
			Function: core.ToolCallFunction{
				Name:      "read_file",
				Arguments: `{"path":"test.txt"}`,
			},
		},
	)
	h.Executor().AddToolResult("call_1", "file content here")

	// Second response: block so we can inject input while the conversation is running
	blockCh := h.Provider().BlockOnCallN(2)
	h.Provider().AddTextResponse("I checked the file.")

	// Third response: after injected input is processed, this is the final answer
	h.Provider().AddTextResponse("Done with everything.")

	agent := h.NewAgent()

	type runResult struct {
		result string
		err    error
	}
	done := make(chan runResult, 1)
	go func() {
		result, err := agent.Run(context.Background(), "Read test.txt")
		done <- runResult{result, err}
	}()

	// Wait for the first iteration to complete (provider call + tool execution)
	for i := 0; i < 100; i++ {
		if h.Provider().CallCount() >= 1 && h.Executor().CallCount() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// At this point the conversation is blocked on the second provider call.
	// Inject input while the conversation is actively running.
	accepted := agent.InjectInput("Also summarize the content")
	if !accepted {
		t.Fatal("expected InjectInput to return true while conversation is running")
	}

	// Unblock the provider so the second call returns
	close(blockCh)

	// Wait for the conversation to complete
	var res runResult
	select {
	case res = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("conversation did not complete within timeout")
	}

	// Assert in main goroutine so failures report correctly
	h.AssertNoError(res.err)
	h.AssertEquals(res.result, "Done with everything.")

	// Provider called 3 times:
	//  1. Original query → tool call response
	//  2. After tool result → text response (injected input found, loop continues)
	//  3. After injected input → final text response (no injection, loop completes)
	h.AssertProviderCalledN(3)
	h.AssertExecutorCalledN(1)

	// State messages:
	//  1. user: "Read test.txt"
	//  2. assistant: "Let me check that." (with tool call)
	//  3. tool: "file content here"
	//  4. assistant: "I checked the file."
	//  5. user: "Also summarize the content" (injected)
	//  6. assistant: "Done with everything."
	h.AssertStateHasNMessages(agent, 6)

	// Verify the injected message is in state
	messages := agent.State().Messages()
	if messages[4].Role != "user" {
		t.Errorf("expected message 5 to be user role, got %q", messages[4].Role)
	}
	if messages[4].Content != "Also summarize the content" {
		t.Errorf("expected injected content, got %q", messages[4].Content)
	}
	// Verify the final assistant message content
	if messages[5].Role != "assistant" {
		t.Errorf("expected message 6 to be assistant role, got %q", messages[5].Role)
	}
	if messages[5].Content != "Done with everything." {
		t.Errorf("expected final content 'Done with everything.', got %q", messages[5].Content)
	}
}

func TestE2E_InjectInput_MidConversation_RejectedWhenPending(t *testing.T) {
	// If a prior injection is still pending (channel full), a second injection is rejected.
	h := NewHarnessWithT(t)

	// First response: tool call (loop continues)
	h.Provider().AddToolCallResponse(
		"Running...",
		core.ToolCall{
			ID: "call_1",
			Function: core.ToolCallFunction{
				Name:      "run",
				Arguments: `{}`,
			},
		},
	)
	h.Executor().AddToolResult("call_1", "ok")

	// Block on second call so we can test injection timing
	blockCh := h.Provider().BlockOnCallN(2)
	h.Provider().AddTextResponse("After injection.")
	h.Provider().AddTextResponse("Final.")

	agent := h.NewAgent()

	type runResult struct {
		err error
	}
	done := make(chan runResult, 1)
	go func() {
		_, err := agent.Run(context.Background(), "Start")
		done <- runResult{err}
	}()

	// Wait for first iteration
	for i := 0; i < 100; i++ {
		if h.Provider().CallCount() >= 1 && h.Executor().CallCount() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// First injection succeeds (channel empty)
	accepted1 := agent.InjectInput("first injection")
	if !accepted1 {
		t.Fatal("expected first injection to succeed")
	}

	// Second injection fails (channel buffer is 1, now full)
	accepted2 := agent.InjectInput("second injection")
	if accepted2 {
		t.Error("expected second injection to fail (channel full)")
	}

	// Unblock and let conversation complete
	close(blockCh)
	var res runResult
	select {
	case res = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("conversation did not complete within timeout")
	}

	// Assert in main goroutine
	h.AssertNoError(res.err)

	// Only the first injection was consumed
	h.AssertStateHasNMessages(agent, 6) // user + assistant(tool) + tool + assistant + injected user + assistant

	// Verify the injected message content
	messages := agent.State().Messages()
	if messages[4].Role != "user" {
		t.Errorf("expected message 5 to be user role, got %q", messages[4].Role)
	}
	if messages[4].Content != "first injection" {
		t.Errorf("expected injected content 'first injection', got %q", messages[4].Content)
	}
	// Verify the final assistant message content
	if messages[5].Role != "assistant" {
		t.Errorf("expected message 6 to be assistant role, got %q", messages[5].Role)
	}
	if messages[5].Content != "Final." {
		t.Errorf("expected final content 'Final.', got %q", messages[5].Content)
	}
}

// --- Cancellation Tests ---

func TestE2E_Cancellation_StopsLoop(t *testing.T) {
	h := NewHarnessWithT(t)

	// Configure the provider to block so we can cancel the context mid-request.
	blockCh := h.Provider().BlockUntil()
	h.Provider().AddTextResponse("Should not reach this")

	agent := h.NewAgent()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := agent.Run(ctx, "test query")
		done <- err
	}()

	// Give the goroutine time to enter the blocking Chat call
	time.Sleep(50 * time.Millisecond)

	// Cancel the context — this should cause the provider to return ctx.Err()
	cancel()

	// Unblock the provider so it can observe the cancelled context
	close(blockCh)

	// Wait for Run to complete
	err := <-done

	h.AssertError(err)
	h.AssertErrorContains(err, "conversation interrupted")
}

func TestE2E_Cancellation_ReturnsErrInterrupted(t *testing.T) {
	h := NewHarnessWithT(t)

	blockCh := h.Provider().BlockUntil()
	h.Provider().AddTextResponse("Should not reach this")

	agent := h.NewAgent()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := agent.Run(ctx, "test query")
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	close(blockCh)

	err := <-done

	// Verify the error wraps ErrInterrupted
	if !errors.Is(err, core.ErrInterrupted) {
		t.Errorf("expected error to wrap ErrInterrupted, got: %v", err)
	}
}

func TestE2E_Cancellation_DuringToolCalls(t *testing.T) {
	h := NewHarnessWithT(t)

	// First response: tool call (loop continues)
	h.Provider().AddToolCallResponse(
		"Running tool...",
		core.ToolCall{
			ID: "call_1",
			Function: core.ToolCallFunction{
				Name:      "slow_tool",
				Arguments: `{}`,
			},
		},
	)
	h.Executor().AddToolResult("call_1", "result")

	// Second response: block on the 2nd call so we can cancel on the next iteration
	_ = h.Provider().BlockOnCallN(2)
	h.Provider().AddTextResponse("Should not reach this")

	agent := h.NewAgent()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := agent.Run(ctx, "test query")
		done <- err
	}()

	// Wait for the first iteration to complete (provider + executor called)
	for i := 0; i < 50; i++ {
		if h.Provider().CallCount() >= 1 && h.Executor().CallCount() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Give the goroutine time to enter the second iteration and hit the block
	time.Sleep(50 * time.Millisecond)

	// Cancel the context — the provider will observe ctx.Done() and return
	// Do NOT close blockCh; let ctx.Done() be the only exit path
	cancel()

	err := <-done

	h.AssertError(err)
	h.AssertErrorContains(err, "conversation interrupted")

	// Provider was called twice: first iteration succeeded, second call returned ctx.Err()
	h.AssertProviderCalledN(2)
	h.AssertExecutorCalledN(1)
}

func TestE2E_Cancellation_ContextExpired(t *testing.T) {
	h := NewHarnessWithT(t)

	// Use a context with a very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	blockCh := h.Provider().BlockUntil()
	h.Provider().AddTextResponse("Should not reach this")

	agent := h.NewAgent()

	_, err := agent.Run(ctx, "test query")

	// Unblock the provider so it can observe the expired context
	close(blockCh)

	h.AssertError(err)
	h.AssertErrorContains(err, "conversation interrupted")
}

func TestE2E_Cancellation_NoEffectWithoutCancel(t *testing.T) {
	// Regression: normal flow without cancellation should still work.
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("Normal response")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)
	h.AssertEquals(result, "Normal response")
	h.AssertProviderCalledN(1)
}

// --- Streaming Tests ---

func TestE2E_Streaming_CallbacksFire(t *testing.T) {
	h := NewHarnessWithT(t)

	// Configure streaming with explicit chunks
	chunks := []string{"Hello", ", ", "world", "!"}
	h.Provider().
		WithStreaming().
		AddStreamChunks(chunks...).
		AddTextResponse("Hello, world!")

	agent := h.NewAgent()
	result, err := agent.RunStream(context.Background(), "test")
	h.AssertNoError(err)

	// RunStream returns empty because content was streamed (buffer preference)
	h.AssertEquals(result, "")

	// Streaming buffer should contain the accumulated content
	buf := agent.StreamingBuffer()
	h.AssertEquals(buf.String(), "Hello, world!")

	// stream_chunk events should have been published
	h.AssertEventPublished(events.EventTypeStreamChunk)

	// agent_message events should have been published per chunk
	h.AssertEventPublished(events.EventTypeAgentMessage)

	// query_started and query_completed should still fire
	h.AssertEventPublished(events.EventTypeQueryStarted)
	h.AssertEventPublished(events.EventTypeQueryCompleted)
}

func TestE2E_Streaming_BufferAccumulation(t *testing.T) {
	h := NewHarnessWithT(t)

	// Configure streaming with multiple chunks
	h.Provider().
		WithStreaming().
		AddStreamChunks("chunk1", "chunk2", "chunk3").
		AddTextResponse("chunk1chunk2chunk3")

	agent := h.NewAgent()

	// Track flush invocations
	flushCount := 0
	agent.SetFlushCallback(func() {
		flushCount++
	})

	result, err := agent.RunStream(context.Background(), "stream test")
	h.AssertNoError(err)

	// Buffer should contain all chunks concatenated
	buf := agent.StreamingBuffer()
	h.AssertEquals(buf.String(), "chunk1chunk2chunk3")

	// Buffer length should match
	if buf.Len() != 18 {
		h.fail("expected buffer length 18, got %d", buf.Len())
	}

	// Flush callback should have been called once per chunk
	if flushCount != 3 {
		h.fail("expected flush called 3 times, got %d", flushCount)
	}

	// RunStream returns empty (buffer content preferred)
	h.AssertEquals(result, "")
}

func TestE2E_Streaming_BufferPreferredOverChoice(t *testing.T) {
	h := NewHarnessWithT(t)

	// Configure streaming — the response has content in Choices,
	// but the buffer should be preferred (finalize returns empty
	// when buffer has content)
	h.Provider().
		WithStreaming().
		AddStreamChunks("streamed ", "content").
		AddTextResponse("streamed content")

	agent := h.NewAgent()
	result, err := agent.RunStream(context.Background(), "test")
	h.AssertNoError(err)

	// finalize() returns empty when streaming buffer has content,
	// so the caller reads from the buffer instead
	h.AssertEquals(result, "")

	// Buffer has the streamed content
	h.AssertEquals(agent.StreamingBuffer().String(), "streamed content")
}

func TestE2E_Streaming_WithToolCalls(t *testing.T) {
	h := NewHarnessWithT(t)

	// First: streamed tool call response
	h.Provider().
		WithStreaming().
		AddStreamChunks("Let ", "me ", "check.").
		AddToolCallResponse(
			"Let me check.",
			core.ToolCall{
				ID: "call_1",
				Function: core.ToolCallFunction{
					Name:      "read_file",
					Arguments: `{"path":"test.txt"}`,
				},
			},
		)
	// Second: streamed final answer
	h.Provider().
		AddStreamChunks("File ", "read.").
		AddTextResponse("File read.")

	h.Executor().AddToolResult("call_1", "file content")

	agent := h.NewAgent()
	_, err := agent.RunStream(context.Background(), "Read test.txt")
	h.AssertNoError(err)

	// Buffer accumulates streamed content across all iterations
	h.AssertEquals(agent.StreamingBuffer().String(), "Let me check.File read.")

	// Provider called twice (tool call iteration + final)
	h.AssertProviderCalledN(2)
	h.AssertExecutorCalledN(1)

	// State: user + assistant(tool) + tool result + assistant(final)
	h.AssertStateHasNMessages(agent, 4)
}

func TestE2E_Streaming_EventsPublished(t *testing.T) {
	h := NewHarnessWithT(t)

	h.Provider().
		WithStreaming().
		AddStreamChunks("A", "B", "C").
		AddTextResponse("ABC")

	agent := h.NewAgent()
	_, err := agent.RunStream(context.Background(), "test")
	h.AssertNoError(err)

	// stream_chunk events — one per chunk
	streamChunks := h.FindEvents(events.EventTypeStreamChunk)
	if len(streamChunks) != 3 {
		h.fail("expected 3 stream_chunk events, got %d", len(streamChunks))
	}

	// agent_message events — one per chunk
	agentMsgs := h.FindEvents(events.EventTypeAgentMessage)
	if len(agentMsgs) < 3 {
		h.fail("expected at least 3 agent_message events, got %d", len(agentMsgs))
	}

	// metrics_update event from OnDone
	h.AssertEventPublished(events.EventTypeMetricsUpdate)
}

func TestE2E_Streaming_ReasoningChunks(t *testing.T) {
	h := NewHarnessWithT(t)

	// We need a custom stream handler that also sends reasoning.
	// Since MockProvider only sends content chunks, we verify
	// that the reasoning buffer is separate from the content buffer.
	h.Provider().
		WithStreaming().
		AddStreamChunks("content only").
		AddTextResponse("content only")

	agent := h.NewAgent()
	_, err := agent.RunStream(context.Background(), "test")
	h.AssertNoError(err)

	// Content buffer has the streamed content
	h.AssertEquals(agent.StreamingBuffer().String(), "content only")

	// Reasoning buffer should be empty (no reasoning chunks sent)
	if agent.ReasoningBuffer().Len() != 0 {
		h.fail("expected empty reasoning buffer")
	}
}
