package core

import (
	"context"
	"testing"
)

// frReasoningResponse builds a ChatResponse with reasoning content. The
// finish reason is "stop" (the model ended its turn) and the visible
// Content is set to whatever the caller wants — typically empty or a
// one-word acknowledgement, mirroring a reasoning-only model that
// decided to stop after thinking.
func frReasoningResponse(content, reasoning string) *ChatResponse {
	return &ChatResponse{
		Choices: []ChatChoice{{
			Message: Message{
				Role:             "assistant",
				Content:          content,
				ReasoningContent: reasoning,
			},
			FinishReason: "stop",
		}},
		Usage: ChatUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
}

// TestReasoningOnlyGuard_RejectsEmptyContentAfterToolCalls verifies that a
// reasoning-capable model which emits a tool call, then on the following
// turn sends "stop" with empty Content but non-empty ReasoningContent, is
// rejected and asked to act or state a final answer.
func TestReasoningOnlyGuard_RejectsEmptyContentAfterToolCalls(t *testing.T) {
	provider := newFRProvider(
		// 1. Model calls a tool
		frToolCallOnlyResponse("call_1", "echo", `{"message":"test"}`),
		// 2. Reasoning-only response after tool results → rejected (#1)
		frReasoningResponse("", "The user wants me to verify the build. I should run make build-all next."),
		// 3. Concrete answer → accepted
		frTextResponse("The build passed without errors after the fix.", "stop"),
	)
	executor := &mockExecutor{
		results: []Message{{Role: "tool", Content: "echo result", ToolCallID: "call_1"}},
	}
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: executor,
	})

	result, err := agent.Run(context.Background(), "verify the build")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "The build passed without errors after the fix." {
		t.Errorf("expected concrete answer after rejection, got: %q", result)
	}
	// 3 provider calls: tool → rejected reasoning-only → concrete answer.
	if provider.idx != 3 {
		t.Errorf("expected 3 provider calls, got %d", provider.idx)
	}
}

// TestReasoningOnlyGuard_AcceptsAfterOneRejection is the cap test: after
// 1 rejection (counter=2 hits the limit), the next reasoning-only response
// is accepted to break the loop. The accepted response must have non-empty
// Content — otherwise finalize returns "" (the validator stops reasoning-
// only when Content is <2 tokens, but finalize will return whatever the
// last assistant message has, which can be empty).
//
// Flow:
//  1. Tool call → result
//  2. Reasoning-only #1 (empty content, non-empty reasoning) → rejected (counter=1, nudge)
//  3. Two-token content + reasoning → counter=2, hits limit, accepted (NOT a reasoning-
//     only response because Content has 2 tokens — guards fires only on 0/1 tokens,
//     so this is the second "good" answer from the validator's perspective)
//
// The cap test exercises that the guard doesn't fire again on a 3rd hit
// that the loop has decided to accept.
func TestReasoningOnlyGuard_AcceptsAfterOneRejection(t *testing.T) {
	provider := newFRProvider(
		// 1. Tool call
		frToolCallOnlyResponse("call_1", "echo", `{"message":"test"}`),
		// 2. Reasoning-only #1 → rejected (counter=1)
		frReasoningResponse("", "I should think about whether to continue or call another tool here now."),
		// 3. Reasoning-only #2 → counter=2, hits limit, accepted (Content is here 1 token though;
		// finalize returns this 1-token string as the final result).
		frReasoningResponse("Done.", "I will summarize the findings now and conclude the run here."),
	)
	executor := &mockExecutor{
		results: []Message{{Role: "tool", Content: "result", ToolCallID: "call_1"}},
	}
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: executor,
	})

	result, err := agent.Run(context.Background(), "research")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// After cap reached, the loop accepts the 2nd reasoning-only response,
	// whose Content is "Done." — single token but with non-empty text, so
	// finalize returns it as the final result.
	if result != "Done." {
		t.Errorf("expected accepted 'Done.' response after cap, got: %q", result)
	}
	// 3 calls: tool + 2 reasoning-only (1 rejected, 1 accepted).
	if provider.idx != 3 {
		t.Errorf("expected 3 provider calls, got %d", provider.idx)
	}
}

// TestReasoningOnlyGuard_PassesShortSubstantiveResponse verifies a
// short real answer (≥3 tokens) is not caught by the guard, even when
// ReasoningContent is non-empty. This is the regression case we must
// NOT introduce — the user's bug report is specifically about content
// being essentially empty, not about content being short.
func TestReasoningOnlyGuard_PassesShortSubstantiveResponse(t *testing.T) {
	provider := newFRProvider(
		// 1. Tool call
		frToolCallOnlyResponse("call_1", "echo", `{"message":"test"}`),
		// 2. Real short answer (3 tokens) → accepted
		frReasoningResponse("Build is green.", "Verify was the main thing the user asked for."),
	)
	executor := &mockExecutor{
		results: []Message{{Role: "tool", Content: "result", ToolCallID: "call_1"}},
	}
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: executor,
	})

	result, err := agent.Run(context.Background(), "verify")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Build is green." {
		t.Errorf("expected short substantive answer to pass, got: %q", result)
	}
	// Only 2 calls: tool + accepted answer — no retry.
	if provider.idx != 2 {
		t.Errorf("expected 2 provider calls, got %d", provider.idx)
	}
}

// TestReasoningOnlyGuard_DoneAloneIsRejected guards the specific phrasing
// from the user's bug report: the model emits "Done." (one token) after
// tool calls. With reasoning content present, "Done." is not enough to
// convey findings, so the loop should ask the model to act or answer.
func TestReasoningOnlyGuard_DoneAloneIsRejected(t *testing.T) {
	provider := newFRProvider(
		frToolCallOnlyResponse("call_1", "echo", `{"message":"test"}`),
		// "Done." with reasoning → reasoning-only, rejected (#1)
		frReasoningResponse("Done.", "I have decided to stop the run here since the user's intent is satisfied."),
		// Concrete answer → accepted
		frTextResponse("All five tests pass and the WASM build errors are gone.", "stop"),
	)
	executor := &mockExecutor{
		results: []Message{{Role: "tool", Content: "result", ToolCallID: "call_1"}},
	}
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: executor,
	})

	result, err := agent.Run(context.Background(), "do the thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result, "five tests pass") {
		t.Errorf("expected expanded answer after 'Done.' rejection, got: %q", result)
	}
	if provider.idx != 3 {
		t.Errorf("expected 3 provider calls (tool + rejected 'Done.' + real answer), got %d", provider.idx)
	}
}

// TestReasoningOnlyGuard_NoReasoning_NoTrigger verifies the guard does
// NOT fire when ReasoningContent is empty. A simple empty Content after
// tool results is the blank guard's job — reasoning-only detection is
// specifically about the case where the model thought but didn't act.
func TestReasoningOnlyGuard_NoReasoning_NoTrigger(t *testing.T) {
	provider := newFRProvider(
		frToolCallOnlyResponse("call_1", "echo", `{"message":"test"}`),
		// No reasoning content, no Content → blank guard handles this,
		// reasoning-only guard does NOT.
		frReasoningResponse("", ""),
		frReasoningResponse("", ""),
		// After 2 blank responses, blank guard force-finalizes with error.
	)
	executor := &mockExecutor{
		results: []Message{{Role: "tool", Content: "result", ToolCallID: "call_1"}},
	}
	agent, _ := NewAgent(Options{
		Provider: provider,
		Executor: executor,
	})

	_, err := agent.Run(context.Background(), "do the thing")
	// The blank guard fires twice and returns BlankResponseError. The
	// reasoning-only guard should NOT have interfered.
	if err == nil {
		t.Fatal("expected blank-response error from blank guard (not reasoning-only)")
	}
	if _, ok := err.(*BlankResponseError); !ok {
		t.Errorf("expected BlankResponseError, got %T: %v", err, err)
	}
}

// contains is a tiny helper to keep test assertions readable.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
