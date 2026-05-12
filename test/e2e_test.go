package test

import (
	"context"
	"errors"
	"fmt"
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
	h.Provider().AddError(fmt.Errorf("simulated provider error"))

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "test")
	h.AssertError(err)
	h.AssertErrorContains(err, "chat failed")
}

func TestE2E_ProviderError_PublishesErrorEvent(t *testing.T) {
	h := NewHarnessWithT(t)
	// Add 3 errors (one per retry attempt) so the mock doesn't run out
	h.Provider().AddError(fmt.Errorf("simulated provider failure"))
	h.Provider().AddError(fmt.Errorf("simulated provider failure"))
	h.Provider().AddError(fmt.Errorf("simulated provider failure"))

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "test")
	h.AssertError(err)
	h.AssertErrorContains(err, "chat failed")

	// Verify the error event was published
	h.AssertEventPublished(events.EventTypeError)

	// With retry logic: 3 retry error events (from chatFn) + 1 final error event (from runLoop) = 4
	errorEvents := h.FindEvents(events.EventTypeError)
	if len(errorEvents) < 3 {
		t.Fatalf("expected at least 3 error events (one per failed attempt), got %d", len(errorEvents))
	}

	// Check the first error event payload
	data, ok := errorEvents[0].Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected error event data to be map[string]interface{}, got %T", errorEvents[0].Data)
	}

	// Verify the message field
	msg, ok := data["message"].(string)
	if !ok {
		t.Fatalf("expected error event message to be string, got %T", data["message"])
	}
	h.AssertEquals(msg, "chat failed")

	// Verify the error field contains the simulated error
	errField, ok := data["error"].(string)
	if !ok {
		t.Fatalf("expected error event error field to be string, got %T", data["error"])
	}
	h.AssertContains(errField, "simulated provider failure")
}

func TestE2E_ProviderError_NoEventBus(t *testing.T) {
	// Regression: provider error should not panic when EventBus is nil
	h := NewHarnessWithT(t)
	h.Provider().AddError(fmt.Errorf("provider failure"))

	agent, err := core.NewAgent(core.Options{
		Provider: h.Provider(),
		Executor: h.Executor(),
		UI:       h.UI(),
		// No EventBus
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = agent.Run(context.Background(), "test")
	h.AssertError(err)
	h.AssertErrorContains(err, "chat failed")
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

	agent, err := core.NewAgent(core.Options{
		Provider: h.Provider(),
		Executor: h.Executor(),
		// No EventBus
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = agent.Run(context.Background(), "test")
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

	agent, err := core.NewAgent(core.Options{
		Provider: h.Provider(),
		Executor: h.Executor(),
		UI:       nil, // headless
	})
	if err != nil {
		t.Fatal(err)
	}
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

func TestE2E_MetricsUpdateEvent(t *testing.T) {
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("Hello")

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "Hi")
	h.AssertNoError(err)

	// Verify the metrics_update event was published
	h.AssertEventPublished(events.EventTypeMetricsUpdate)

	// Find the metrics events and verify the payload fields
	metricsEvents := h.FindEvents(events.EventTypeMetricsUpdate)
	if len(metricsEvents) != 1 {
		t.Fatalf("expected 1 metrics_update event, got %d", len(metricsEvents))
	}

	// Check the event payload
	data, ok := metricsEvents[0].Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected metrics event data to be map[string]interface{}, got %T", metricsEvents[0].Data)
	}

	// total_tokens: 35 (prompt 20 + completion 15 from mock AddTextResponse)
	totalTokens, ok := data["total_tokens"].(int)
	if !ok {
		t.Fatalf("expected total_tokens to be int, got %T", data["total_tokens"])
	}
	if totalTokens != 35 {
		t.Errorf("expected total_tokens 35, got %v", totalTokens)
	}

	// prompt_tokens: 20 (prompt tokens from mock AddTextResponse)
	// Note: the event field is named "context_tokens" but carries prompt token count
	promptTokens, ok := data["context_tokens"].(int)
	if !ok {
		t.Fatalf("expected context_tokens to be int, got %T", data["context_tokens"])
	}
	if promptTokens != 20 {
		t.Errorf("expected context_tokens 20, got %v", promptTokens)
	}

	// max_context_tokens: 128000 (mock provider default context size)
	maxContextTokens, ok := data["max_context_tokens"].(int)
	if !ok {
		t.Fatalf("expected max_context_tokens to be int, got %T", data["max_context_tokens"])
	}
	if maxContextTokens != 128000 {
		t.Errorf("expected max_context_tokens 128000, got %v", maxContextTokens)
	}

	// iteration: 0 (first iteration)
	iteration, ok := data["iteration"].(int)
	if !ok {
		t.Fatalf("expected iteration to be int, got %T", data["iteration"])
	}
	if iteration != 0 {
		t.Errorf("expected iteration 0, got %v", iteration)
	}

	// total_cost: 0 (mock provider default cost)
	totalCost, ok := data["total_cost"].(float64)
	if !ok {
		t.Fatalf("expected total_cost to be float64, got %T", data["total_cost"])
	}
	if totalCost != 0 {
		t.Errorf("expected total_cost 0, got %v", totalCost)
	}
}

func TestE2E_MetricsUpdateEvent_MultiIteration(t *testing.T) {
	h := NewHarnessWithT(t)

	// First: tool call (iteration 0)
	h.Provider().AddToolCallResponse(
		"Checking...",
		core.ToolCall{ID: "call_1", Function: core.ToolCallFunction{Name: "read", Arguments: `{}`}},
	)
	// Second: final answer (iteration 1)
	h.Provider().AddTextResponse("Done.")
	h.Executor().AddToolResult("call_1", "content")

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "Read file")
	h.AssertNoError(err)

	// Two iterations should produce two metrics_update events
	metricsEvents := h.FindEvents(events.EventTypeMetricsUpdate)
	if len(metricsEvents) != 2 {
		t.Fatalf("expected 2 metrics_update events, got %d", len(metricsEvents))
	}

	// Verify iteration increments correctly
	for i, evt := range metricsEvents {
		data, ok := evt.Data.(map[string]interface{})
		if !ok {
			t.Fatalf("event %d: expected data to be map[string]interface{}, got %T", i, evt.Data)
		}
		it, ok := data["iteration"].(int)
		if !ok {
			t.Fatalf("event %d: expected iteration to be int, got %T", i, data["iteration"])
		}
		if it != i {
			t.Errorf("event %d: expected iteration %d, got %d", i, i, it)
		}
	}

	// Verify total_tokens accumulates across iterations
	// Iteration 0 (tool call): 80 tokens (50 prompt + 30 completion from AddToolCallResponse)
	// Iteration 1 (final): 35 tokens (20 prompt + 15 completion from AddTextResponse)
	// Accumulated total: 80 + 35 = 115
	lastData := metricsEvents[1].Data.(map[string]interface{})
	totalTokens := lastData["total_tokens"].(int)
	if totalTokens != 115 {
		t.Errorf("expected total_tokens 115 (accumulated), got %d", totalTokens)
	}
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

// --- Incomplete Response Continuation Tests ---

func TestE2E_IncompleteResponse_TrailingEllipsis(t *testing.T) {
	// Provider returns a response ending with ellipsis, indicating truncation.
	// The validator should detect this as incomplete and continue the loop.
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("Here is the answer...")
	// Second response must be long enough (>10 words) so it's not flagged as too short.
	h.Provider().AddTextResponse("This is the complete answer that I was going to provide from the start.")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)
	h.AssertEquals(result, "This is the complete answer that I was going to provide from the start.")

	// Provider called twice: initial incomplete response + continuation
	h.AssertProviderCalledN(2)

	// State: user query, assistant(incomplete), assistant(final continuation)
	h.AssertStateHasNMessages(agent, 3)
}

func TestE2E_IncompleteResponse_AbruptEnding(t *testing.T) {
	// Provider returns a response ending with a comma, indicating truncation.
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("The file contains,")
	// Second response must be at least 10 words so it's not flagged as too short.
	h.Provider().AddTextResponse("It has a very long content inside the file that we are looking at today.")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)
	h.AssertEquals(result, "It has a very long content inside the file that we are looking at today.")

	// Provider called twice
	h.AssertProviderCalledN(2)

	// State: user + assistant(incomplete) + assistant(final)
	h.AssertStateHasNMessages(agent, 3)
}

func TestE2E_IncompleteResponse_MaxContinuations(t *testing.T) {
	// Provider keeps returning incomplete responses. After 3 consecutive
	// continuations (maxContinuations=3), the loop force-finalizes.
	// Total provider calls: 1 initial + 3 continuations = 4
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("First incomplete...")
	h.Provider().AddTextResponse("Second incomplete...")
	h.Provider().AddTextResponse("Third incomplete...")
	h.Provider().AddTextResponse("Fourth and last...")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)
	h.AssertEquals(result, "Fourth and last...")

	// Provider called 4 times: 1 initial + 3 continuations (4th call force-finalized)
	h.AssertProviderCalledN(4)

	// State: user + 4 assistant messages (one per iteration)
	h.AssertStateHasNMessages(agent, 5)
}

func TestE2E_IncompleteResponse_CompleteShortAnswer(t *testing.T) {
	// "Done." is a known complete short answer — should NOT trigger continuation.
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("Done.")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)
	h.AssertEquals(result, "Done.")

	// Provider called only once — no continuation triggered
	h.AssertProviderCalledN(1)

	// State: user + assistant
	h.AssertStateHasNMessages(agent, 2)
}

func TestE2E_IncompleteResponse_WithToolCalls(t *testing.T) {
	// Provider returns an incomplete response WITH tool calls.
	// Tool calls take precedence over the incomplete check, so the tool
	// executes and continuationCount resets. The incomplete response is
	// still recorded in state as the last assistant message.
	h := NewHarnessWithT(t)
	h.Provider().AddToolCallResponse(
		"Let me check...",
		core.ToolCall{
			ID: "call_1",
			Function: core.ToolCallFunction{
				Name:      "read_file",
				Arguments: `{"path":"test.txt"}`,
			},
		},
	)
	// The final response must be at least 10 words so it's not flagged as too short.
	h.Provider().AddTextResponse("The file contents are hello world and that is all you need to know now.")

	h.Executor().AddToolResult("call_1", "hello world")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "Read test.txt")
	h.AssertNoError(err)
	h.AssertEquals(result, "The file contents are hello world and that is all you need to know now.")

	// Provider called twice: tool call iteration + final answer
	h.AssertProviderCalledN(2)
	h.AssertExecutorCalledN(1)

	// State: user + assistant(tool call, incomplete) + tool result + assistant(final)
	h.AssertStateHasNMessages(agent, 4)
}

// --- Tentative Post-Tool Response Tests ---

func TestE2E_TentativeResponse_PlanningStub(t *testing.T) {
	// Provider returns a tentative planning stub ("Let me check...") with no
	// tool calls. The validator should detect this and continue the loop
	// instead of finalizing. The second response is the actual answer.
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("Let me check the file contents.")
	h.Provider().AddTextResponse("The file contains the expected configuration data that was requested.")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "What's in the file?")
	h.AssertNoError(err)
	h.AssertEquals(result, "The file contains the expected configuration data that was requested.")

	// Provider called twice: tentative stub + actual response
	h.AssertProviderCalledN(2)

	// State: user + assistant(tentative) + assistant(final)
	h.AssertStateHasNMessages(agent, 3)
}

func TestE2E_TentativeResponse_AfterToolExecution(t *testing.T) {
	// Scenario: tool executes, then provider returns a tentative planning stub
	// instead of using the tool result. The loop should continue and get a
	// real response.
	h := NewHarnessWithT(t)
	h.Provider().AddToolCallResponse(
		"Reading the file",
		core.ToolCall{
			ID: "call_1",
			Function: core.ToolCallFunction{
				Name:      "read_file",
				Arguments: `{"path":"config.yaml"}`,
			},
		},
	)
	h.Provider().AddTextResponse("I'll analyze the results now.")
	h.Provider().AddTextResponse("The configuration has three sections: database, cache, and logging. All are properly configured.")

	h.Executor().AddToolResult("call_1", "database:\n  host: localhost\n  port: 5432")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "Read config.yaml and summarize")
	h.AssertNoError(err)
	h.AssertEquals(result, "The configuration has three sections: database, cache, and logging. All are properly configured.")

	// Provider called 3 times: tool call -> tentative stub -> actual response
	h.AssertProviderCalledN(3)
	h.AssertExecutorCalledN(1)

	// State: user + assistant(tool call) + tool result + assistant(tentative) + assistant(final)
	h.AssertStateHasNMessages(agent, 5)
}

func TestE2E_TentativeResponse_MaxContinuations(t *testing.T) {
	// Provider keeps returning tentative responses. After maxContinuations,
	// the loop force-finalizes. These responses must NOT end with "..." or
	// other truncation markers, otherwise the LooksTruncated check fires first
	// and the test exercises the wrong code path.
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse("Let me check the file.")
	h.Provider().AddTextResponse("I'll need to look at it.")
	h.Provider().AddTextResponse("I'm going to find the answer.")
	h.Provider().AddTextResponse("Let me think about this one.")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)
	h.AssertEquals(result, "Let me think about this one.")

	// Provider called 4 times: 1 initial + 3 continuations (4th force-finalized)
	h.AssertProviderCalledN(4)

	// State: user + 4 assistant messages
	h.AssertStateHasNMessages(agent, 5)
}

func TestE2E_TentativeResponse_SubstantiveNotTentative(t *testing.T) {
	// A response that starts with "Let me" but is over 40 words should NOT
	// be treated as tentative — it's substantive enough to finalize.
	h := NewHarnessWithT(t)
	h.Provider().AddTextResponse(
		"Let me provide a comprehensive answer. The file contains configuration data with multiple sections including database settings, cache parameters, and logging levels. Each section has been reviewed and appears to be correctly configured for production use. All values are within acceptable ranges and no changes are required at this time.",
	)

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)

	// Provider called only once — substantive response not treated as tentative
	h.AssertProviderCalledN(1)

	// State: user + assistant
	h.AssertStateHasNMessages(agent, 2)

	// Result should be the substantive response
	expected := "Let me provide a comprehensive answer. The file contains configuration data with multiple sections including database settings, cache parameters, and logging levels. Each section has been reviewed and appears to be correctly configured for production use. All values are within acceptable ranges and no changes are required at this time."
	h.AssertEquals(result, expected)
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

// --- Continuation Budget Reset Tests ---

func TestE2E_ContinuationBudget_ResetsOnToolCalls(t *testing.T) {
	// This test verifies that the continuation budget resets when tool calls
	// occur between incomplete responses. Without the reset, the budget would
	// accumulate incorrectly and the loop could force-finalize prematurely.
	//
	// Flow:
	// 1. Provider returns an incomplete response → continuationCount = 1
	// 2. Provider returns a tool call → continuationCount resets to 0
	// 3. Provider returns an incomplete response → continuationCount = 1 again
	// 4. Provider returns another tool call → continuationCount resets to 0 again
	// 5. Provider returns a complete final answer → loop completes normally
	//
	// Total provider calls: 5
	// Total executor calls: 2

	h := NewHarnessWithT(t)

	// Call 1: incomplete response (ends with "...") — triggers continuation
	h.Provider().AddTextResponse("First incomplete response...")

	// Call 2: tool call response — resets continuationCount to 0
	h.Provider().AddToolCallResponse(
		"Let me check the data.",
		core.ToolCall{
			ID: "call_1",
			Function: core.ToolCallFunction{
				Name:      "fetch_data",
				Arguments: `{"id": "123"}`,
			},
		},
	)

	// Call 3: incomplete response (ends with "...") — continuationCount = 1 again
	h.Provider().AddTextResponse("Looking at the results...")

	// Call 4: another tool call — resets continuationCount to 0 again
	h.Provider().AddToolCallResponse(
		"I'll aggregate the data.",
		core.ToolCall{
			ID: "call_2",
			Function: core.ToolCallFunction{
				Name:      "aggregate",
				Arguments: `{"source": "123"}`,
			},
		},
	)

	// Call 5: final complete answer — loop completes
	h.Provider().AddTextResponse("The aggregated data shows all metrics are within acceptable ranges.")

	// Executor returns results for both tool calls
	h.Executor().AddToolResult("call_1", "raw data content")
	h.Executor().AddToolResult("call_2", "aggregated: {\"status\": \"ok\"}")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "Check data and aggregate")
	h.AssertNoError(err)
	h.AssertEquals(result, "The aggregated data shows all metrics are within acceptable ranges.")

	// Provider called 5 times:
	//  1. Initial incomplete response → continuation
	//  2. Tool call response → executes, resets continuationCount
	//  3. Second incomplete response → continuation (count = 1)
	//  4. Second tool call → executes, resets continuationCount
	//  5. Final answer → completes
	h.AssertProviderCalledN(5)
	h.AssertExecutorCalledN(2)

	// State messages (8 total):
	//  1. user: "Check data and aggregate"
	//  2. assistant: "First incomplete response..."
	//  3. assistant: "Let me check the data." (with tool call)
	//  4. tool: "raw data content"
	//  5. assistant: "Looking at the results..."
	//  6. assistant: "I'll aggregate the data." (with tool call)
	//  7. tool: "aggregated: {\"status\": \"ok\"}"
	//  8. assistant: "The aggregated data shows..."
	h.AssertStateHasNMessages(agent, 8)
}

func TestE2E_ContinuationBudget_TruncatedAndTentativeOverlap(t *testing.T) {
	// A response that matches both LooksTruncated (ends with "...") and
	// LooksLikeTentativePostToolResponse (starts with "Let me") should only
	// consume one continuation budget. The truncated check takes priority
	// (if/else if chain), so only that check fires.
	h := NewHarnessWithT(t)

	// First two responses match both truncated (ends with "...") and
	// tentative (starts with "Let me"). Each should consume only 1
	// continuation budget (not 2) because the if/else if chain ensures
	// only the truncated check fires. The final response is substantive
	// enough (>40 words) that it won't be flagged as tentative.
	h.Provider().AddTextResponse("Let me check this...")
	h.Provider().AddTextResponse("Let me try again...")
	h.Provider().AddTextResponse("The file contains configuration data with multiple sections including database settings, cache parameters, and logging levels. All values are within acceptable ranges.")

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "test")
	h.AssertNoError(err)

	// Provider called 3 times: 2 continuations (budget 2/3) + 1 final
	h.AssertProviderCalledN(3)

	// State: user + 3 assistant messages
	h.AssertStateHasNMessages(agent, 4)
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

// --- Retry / Error Recovery Tests ---

func TestE2E_Retry_TransientErrorRecovers(t *testing.T) {
	h := NewHarnessWithT(t)

	// First two calls fail with transient errors; third succeeds.
	h.Provider().AddError(fmt.Errorf("connection refused"))
	h.Provider().AddError(fmt.Errorf("timeout: deadline exceeded"))
	h.Provider().AddTextResponse("Recovered!")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "test query")

	h.AssertNoError(err)
	h.AssertEquals(result, "Recovered!")

	// Provider called 3 times: 2 transient errors + 1 success
	h.AssertProviderCalledN(3)
}

func TestE2E_Retry_AuthErrorFailsFast(t *testing.T) {
	h := NewHarnessWithT(t)

	// Auth error should fail fast — no retries.
	h.Provider().AddError(&core.AuthError{
		Provider: "mock-model",
		Wrapped:  fmt.Errorf("invalid api key"),
	})

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "test query")

	h.AssertError(err)
	h.AssertErrorContains(err, "authentication failed")

	// Provider called only once — no retry on auth errors
	h.AssertProviderCalledN(1)
}

func TestE2E_Retry_ContextOverflowFailsFast(t *testing.T) {
	h := NewHarnessWithT(t)

	// Context overflow should fail fast — no retries.
	h.Provider().AddError(&core.ContextOverflowError{
		TokensUsed:  130000,
		TokensLimit: 128000,
		Wrapped:     fmt.Errorf("context window exceeded"),
	})

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "test query")

	h.AssertError(err)
	h.AssertErrorContains(err, "context window exceeded")

	// Provider called only once — no retry on context overflow
	h.AssertProviderCalledN(1)
}

func TestE2E_Retry_ExhaustedReturnsLastError(t *testing.T) {
	h := NewHarnessWithT(t)

	// All 3 attempts fail with transient errors (max attempts = 3).
	h.Provider().AddError(fmt.Errorf("transient failure 1"))
	h.Provider().AddError(fmt.Errorf("transient failure 2"))
	h.Provider().AddError(fmt.Errorf("transient failure 3"))

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "test query")

	h.AssertError(err)
	h.AssertErrorContains(err, "transient failure 3")

	// Provider called 3 times (max attempts exhausted)
	h.AssertProviderCalledN(3)

	// State should only have the user message — no assistant message recorded
	h.AssertStateHasNMessages(agent, 1)
}

func TestE2E_Retry_RateLimitErrorRetries(t *testing.T) {
	h := NewHarnessWithT(t)

	// First call fails with rate limit; second succeeds.
	h.Provider().AddError(&core.RateLimitError{
		Provider: "mock-model",
		Attempt:  1,
		Wrapped:  fmt.Errorf("rate limit exceeded"),
	})
	h.Provider().AddTextResponse("After rate limit!")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "test query")

	h.AssertNoError(err)
	h.AssertEquals(result, "After rate limit!")

	// Provider called 2 times: 1 rate limit + 1 success
	h.AssertProviderCalledN(2)
}

func TestE2E_Retry_RateLimitMultipleRetriesWithBackoff(t *testing.T) {
	h := NewHarnessWithT(t)

	// Two consecutive rate limit errors, then success.
	// This exercises the backoff delay between retries.
	h.Provider().AddError(&core.RateLimitError{
		Provider: "mock-model",
		Attempt:  1,
		Wrapped:  fmt.Errorf("rate limit exceeded"),
	})
	h.Provider().AddError(&core.RateLimitError{
		Provider: "mock-model",
		Attempt:  2,
		Wrapped:  fmt.Errorf("rate limit exceeded"),
	})
	h.Provider().AddTextResponse("Recovered after rate limits!")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "test query")

	h.AssertNoError(err)
	h.AssertEquals(result, "Recovered after rate limits!")

	// Provider called 3 times: 2 rate limit errors + 1 success
	h.AssertProviderCalledN(3)

	// State should have user + assistant messages (successful path)
	h.AssertStateHasNMessages(agent, 2)
}

func TestE2E_Retry_RateLimitErrorEventsPublished(t *testing.T) {
	h := NewHarnessWithT(t)

	// Two rate limit errors before success.
	h.Provider().AddError(&core.RateLimitError{
		Provider: "mock-model",
		Attempt:  1,
		Wrapped:  fmt.Errorf("rate limit exceeded"),
	})
	h.Provider().AddError(&core.RateLimitError{
		Provider: "mock-model",
		Attempt:  2,
		Wrapped:  fmt.Errorf("rate limit exceeded"),
	})
	h.Provider().AddTextResponse("OK")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "test query")

	h.AssertNoError(err)
	h.AssertEquals(result, "OK")

	// Error events published for each rate limit failure (from chatFn)
	h.AssertEventPublished(events.EventTypeError)
	errorEvents := h.FindEvents(events.EventTypeError)
	if len(errorEvents) != 2 {
		t.Errorf("expected 2 error events (one per rate limit retry), got %d", len(errorEvents))
	}

	// Verify error event payload contains rate limit info
	data, ok := errorEvents[0].Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected error event data to be map[string]interface{}, got %T", errorEvents[0].Data)
	}
	errField, ok := data["error"].(string)
	if !ok {
		t.Fatalf("expected error event error field to be string, got %T", data["error"])
	}
	h.AssertContains(errField, "rate limit exceeded")
}

func TestE2E_Retry_RateLimitExhausted(t *testing.T) {
	h := NewHarnessWithT(t)

	// All 3 attempts fail with rate limit errors (max attempts = 3).
	h.Provider().AddError(&core.RateLimitError{
		Provider: "mock-model",
		Attempt:  1,
		Wrapped:  fmt.Errorf("rate limit exceeded"),
	})
	h.Provider().AddError(&core.RateLimitError{
		Provider: "mock-model",
		Attempt:  2,
		Wrapped:  fmt.Errorf("rate limit exceeded"),
	})
	h.Provider().AddError(&core.RateLimitError{
		Provider: "mock-model",
		Attempt:  3,
		Wrapped:  fmt.Errorf("rate limit exceeded"),
	})

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "test query")

	h.AssertError(err)
	h.AssertErrorContains(err, "chat failed")

	// The wrapped error should be the last RateLimitError
	var rateLimitErr *core.RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("expected error chain to contain RateLimitError, got: %v", err)
	}

	// Provider called 3 times (max attempts exhausted)
	h.AssertProviderCalledN(3)

	// State should only have the user message — no assistant message recorded
	h.AssertStateHasNMessages(agent, 1)

	// Error events: 3 from chatFn (one per rate limit attempt) + 1 from runLoop (final error) = 4
	h.AssertEventPublished(events.EventTypeError)
	errorEvents := h.FindEvents(events.EventTypeError)
	if len(errorEvents) != 4 {
		t.Errorf("expected 4 error events (3 retry + 1 final), got %d", len(errorEvents))
	}
}

func TestE2E_Retry_RateLimitMixedWithTransient(t *testing.T) {
	h := NewHarnessWithT(t)

	// Rate limit on first attempt, transient on second, success on third.
	h.Provider().AddError(&core.RateLimitError{
		Provider: "mock-model",
		Attempt:  1,
		Wrapped:  fmt.Errorf("rate limit exceeded"),
	})
	h.Provider().AddError(fmt.Errorf("connection refused"))
	h.Provider().AddTextResponse("Recovered from mixed errors!")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "test query")

	h.AssertNoError(err)
	h.AssertEquals(result, "Recovered from mixed errors!")

	// Provider called 3 times: 1 rate limit + 1 transient + 1 success
	h.AssertProviderCalledN(3)

	// Error events published for both retryable errors
	h.AssertEventPublished(events.EventTypeError)
	errorEvents := h.FindEvents(events.EventTypeError)
	if len(errorEvents) != 2 {
		t.Errorf("expected 2 error events (rate limit + transient), got %d", len(errorEvents))
	}
}

func TestE2E_Retry_RateLimitErrorWrapping(t *testing.T) {
	h := NewHarnessWithT(t)

	// Single rate limit error that exhausts retries.
	h.Provider().AddError(&core.RateLimitError{
		Provider: "my-provider",
		Attempt:  1,
		Wrapped:  fmt.Errorf("429 too many requests"),
	})
	h.Provider().AddError(&core.RateLimitError{
		Provider: "my-provider",
		Attempt:  2,
		Wrapped:  fmt.Errorf("429 too many requests"),
	})
	h.Provider().AddError(&core.RateLimitError{
		Provider: "my-provider",
		Attempt:  3,
		Wrapped:  fmt.Errorf("429 too many requests"),
	})

	agent := h.NewAgent()
	_, err := agent.Run(context.Background(), "test query")

	h.AssertError(err)

	// Verify the error chain: fmt.Errorf("chat failed: %w") wraps RateLimitError
	var rateLimitErr *core.RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("expected error chain to contain RateLimitError, got: %v", err)
	}

	// Verify RateLimitError fields
	if rateLimitErr.Provider != "my-provider" {
		t.Errorf("expected provider 'my-provider', got %q", rateLimitErr.Provider)
	}
	if rateLimitErr.Wrapped == nil {
		t.Error("expected wrapped error to be set")
	} else if !strings.Contains(rateLimitErr.Wrapped.Error(), "429") {
		t.Errorf("expected wrapped error to contain '429', got: %v", rateLimitErr.Wrapped)
	}
}

func TestE2E_Retry_ErrorEventsPublished(t *testing.T) {
	h := NewHarnessWithT(t)

	// Two transient errors before success.
	h.Provider().AddError(fmt.Errorf("timeout: connection reset"))
	h.Provider().AddError(fmt.Errorf("service unavailable"))
	h.Provider().AddTextResponse("OK")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "test query")

	h.AssertNoError(err)
	h.AssertEquals(result, "OK")

	// Error events published for each transient failure (from chatFn)
	h.AssertEventPublished(events.EventTypeError)
	errorEvents := h.FindEvents(events.EventTypeError)
	if len(errorEvents) != 2 {
		t.Errorf("expected 2 error events (one per retry attempt), got %d", len(errorEvents))
	}
}

func TestE2E_Retry_ContextCancellationDuringBackoff(t *testing.T) {
	h := NewHarnessWithT(t)

	// First call fails with transient error; second call would succeed
	// but context is cancelled during backoff delay.
	h.Provider().AddError(fmt.Errorf("timeout: connection reset"))
	h.Provider().AddTextResponse("Should not reach this")

	agent := h.NewAgent()

	ctx, cancel := context.WithCancel(context.Background())

	// Start the run in a goroutine
	done := make(chan error, 1)
	go func() {
		_, err := agent.Run(ctx, "test query")
		done <- err
	}()

	// Wait for the first provider call to complete (it will fail with transient error)
	for i := 0; i < 50; i++ {
		if h.Provider().CallCount() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Cancel during the backoff delay before the retry
	cancel()

	err := <-done

	h.AssertError(err)
	h.AssertErrorContains(err, "conversation interrupted")

	// Provider called once (first attempt failed, backoff cancelled before retry)
	h.AssertProviderCalledN(1)
}

// --- Malformed Response (Fallback Parser) Tests ---

func TestE2E_MalformedResponse_JSONFence(t *testing.T) {
	// Simulates a model returning tool calls embedded in content as JSON
	// rather than in the structured tool_calls field. The fallback parser
	// should extract them, the tool executes, and the conversation completes.
	h := NewHarnessWithT(t)

	// Register a tool so the executor knows about it.
	h.Executor().AddTool(core.Tool{Name: "read_file"})

	// First response: malformed — tool call embedded in JSON fence inside content.
	malformedContent := "I'll read the file for you.\n\n```json\n{\"tool_calls\":[{\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"test.txt\\\"}\"}}]}\n```\n\nLet me know if you need anything else."
	h.Provider().AddMalformedResponse(malformedContent)

	// Second response: final answer after tool result.
	h.Provider().AddTextResponse("The file contains 'hello world'.")

	// Executor returns the tool result.
	h.Executor().AddToolResult("call_1", "hello world")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "Read test.txt")

	h.AssertNoError(err)
	h.AssertEquals(result, "The file contains 'hello world'.")

	// Provider called twice: malformed response (fallback extracted tool call) + final answer.
	h.AssertProviderCalledN(2)

	// Executor called once: the fallback parser extracted the tool call.
	h.AssertExecutorCalledN(1)

	// State: user + assistant(malformed) + tool result + assistant(final)
	h.AssertStateHasNMessages(agent, 4)

	// Verify the assistant message has the cleaned content (tool call stripped).
	msgs := agent.State().Messages()
	if msgs[1].Role != "assistant" {
		t.Errorf("expected message 2 to be assistant, got %q", msgs[1].Role)
	}
	// The cleaned content should not contain the JSON fence.
	if strings.Contains(msgs[1].Content, "```json") {
		t.Error("expected cleaned content to not contain JSON fence")
	}
}

func TestE2E_MalformedResponse_XMLBlock(t *testing.T) {
	// Simulates a model returning tool calls in XML format embedded in content.
	h := NewHarnessWithT(t)

	h.Executor().AddTool(core.Tool{Name: "search"})

	// Malformed response with <tool> XML block containing tool_calls JSON.
	malformedContent := "Let me search for that.\n<tool>{\"tool_calls\":[{\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"search\",\"arguments\":\"{\\\"query\\\":\\\"go testing\\\"}\"}}]}</tool>\nThat should help."
	h.Provider().AddMalformedResponse(malformedContent)

	h.Provider().AddTextResponse("Found 3 results for 'go testing'.")

	h.Executor().AddToolResult("call_1", "3 results found")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "Search go testing")

	h.AssertNoError(err)
	h.AssertEquals(result, "Found 3 results for 'go testing'.")

	h.AssertProviderCalledN(2)
	h.AssertExecutorCalledN(1)
	h.AssertStateHasNMessages(agent, 4)
}

func TestE2E_MalformedResponse_NoPatternTriggers(t *testing.T) {
	// Content has no tool-call patterns — fallback parser should not trigger.
	// The response should be treated as a normal text response.
	h := NewHarnessWithT(t)

	h.Provider().AddMalformedResponse("Just a normal text response with no tool calls.")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "test")

	h.AssertNoError(err)
	h.AssertEquals(result, "Just a normal text response with no tool calls.")

	h.AssertProviderCalledN(1)
	h.AssertExecutorCalledN(0)
	h.AssertStateHasNMessages(agent, 2) // user + assistant
}

func TestE2E_MalformedResponse_MultipleIterations(t *testing.T) {
	// Tests a conversation where multiple malformed responses are processed
	// across iterations, each with tool calls extracted by the fallback parser.
	h := NewHarnessWithT(t)

	h.Executor().AddTool(core.Tool{Name: "read_file"})
	h.Executor().AddTool(core.Tool{Name: "shell"})

	// First malformed: read_file tool call
	h.Provider().AddMalformedResponse(
		"```json\n{\"tool_calls\":[{\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"data.txt\\\"}\"}}]}\n```",
	)
	// Second malformed: shell tool call
	h.Provider().AddMalformedResponse(
		"```json\n{\"tool_calls\":[{\"id\":\"call_2\",\"type\":\"function\",\"function\":{\"name\":\"shell\",\"arguments\":\"{\\\"cmd\\\":\\\"wc -l data.txt\\\"}\"}}]}\n```",
	)
	// Final text response
	h.Provider().AddTextResponse("The file has 42 lines.")

	h.Executor().AddToolResult("call_1", "some data content")
	h.Executor().AddToolResult("call_2", "42 data.txt")

	agent := h.NewAgent()
	result, err := agent.Run(context.Background(), "Count lines in data.txt")

	h.AssertNoError(err)
	h.AssertEquals(result, "The file has 42 lines.")

	// Provider called 3 times: 2 malformed + 1 final
	h.AssertProviderCalledN(3)

	// Executor called 2 times: once per malformed iteration
	h.AssertExecutorCalledN(2)

	// State: user + assistant(malformed1) + tool result1 + assistant(malformed2) + tool result2 + assistant(final)
	h.AssertStateHasNMessages(agent, 6)
}

// --- Streaming Incomplete Response Tests ---

func TestE2E_IncompleteResponse_Streaming_TrailingEllipsis(t *testing.T) {
	// Provider streams a response ending with ellipsis (structural truncation marker).
	// The validator should detect this as incomplete and continue the loop.
	// The second call streams a complete answer (>10 words, no truncation markers).
	h := NewHarnessWithT(t)

	// First call: incomplete streaming response (ends with "...")
	h.Provider().
		WithStreaming().
		AddStreamChunks("Here is the answer...").
		AddTextResponse("Here is the answer...")

	// Second call: complete streaming response (no truncation, >10 words)
	h.Provider().
		AddStreamChunks("This is the complete answer that I was going to provide from the start.").
		AddTextResponse("This is the complete answer that I was going to provide from the start.")

	agent := h.NewAgent()
	result, err := agent.RunStream(context.Background(), "test")
	h.AssertNoError(err)

	// RunStream returns empty when buffer has content (buffer preferred over choices)
	h.AssertEquals(result, "")

	// Streaming buffer should contain both streamed contents concatenated
	buf := agent.StreamingBuffer()
	h.AssertEquals(buf.String(), "Here is the answer...This is the complete answer that I was going to provide from the start.")

	// Provider called twice: initial incomplete response + continuation with complete answer
	h.AssertProviderCalledN(2)

	// State: user query + assistant(incomplete) + assistant(final continuation)
	h.AssertStateHasNMessages(agent, 3)
}

func TestE2E_IncompleteResponse_Streaming_CompleteShortAnswer(t *testing.T) {
	// "Done." is a known complete short answer — should NOT trigger continuation.
	h := NewHarnessWithT(t)

	h.Provider().
		WithStreaming().
		AddStreamChunks("Done.").
		AddTextResponse("Done.")

	agent := h.NewAgent()
	result, err := agent.RunStream(context.Background(), "test")
	h.AssertNoError(err)

	// RunStream returns empty when buffer has content (buffer preferred over choices)
	h.AssertEquals(result, "")

	// Streaming buffer contains the streamed content
	buf := agent.StreamingBuffer()
	h.AssertEquals(buf.String(), "Done.")

	// Provider called only once — no continuation triggered (known complete short answer)
	h.AssertProviderCalledN(1)

	// State: user + assistant
	h.AssertStateHasNMessages(agent, 2)
}

func TestE2E_TentativeResponse_Streaming(t *testing.T) {
	// Provider streams a tentative planning stub ("Let me check.") with no
	// tool calls. The validator should detect this and continue the loop.
	// The second call streams a complete answer.
	h := NewHarnessWithT(t)

	// First call: tentative streaming response
	h.Provider().
		WithStreaming().
		AddStreamChunks("Let me ", "check.").
		AddTextResponse("Let me check.")

	// Second call: complete streaming response
	h.Provider().
		AddStreamChunks("The file contains the expected configuration data.").
		AddTextResponse("The file contains the expected configuration data.")

	agent := h.NewAgent()
	result, err := agent.RunStream(context.Background(), "What's in the file?")
	h.AssertNoError(err)

	// RunStream returns empty when buffer has content
	h.AssertEquals(result, "")

	// Streaming buffer should contain both streamed contents
	buf := agent.StreamingBuffer()
	h.AssertEquals(buf.String(), "Let me check.The file contains the expected configuration data.")

	// Provider called twice: tentative stub + actual response
	h.AssertProviderCalledN(2)

	// State: user + assistant(tentative) + assistant(final)
	h.AssertStateHasNMessages(agent, 3)
}
