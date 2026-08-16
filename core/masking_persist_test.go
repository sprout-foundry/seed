package core

import (
	"fmt"
	"strings"
	"testing"
)

// toolResultContent returns the content of the first tool message matching
// callID, or "" if absent.
func toolResultContent(msgs []Message, callID string) string {
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID == callID {
			return m.Content
		}
	}
	return ""
}

// maskSeedMessages builds a conversation with 8 large consumed tool results
// (the keep-last-5 window protects the newest 5, so exactly 3 are eligible for
// observation masking) followed by a final assistant message. Each result is
// ~6000 chars of distinct content so the dedup pass never collapses them.
func maskSeedMessages() []Message {
	msgs := []Message{{Role: "user", Content: "read all the files"}}
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("call-%d", i)
		msgs = append(msgs,
			Message{Role: "assistant", ToolCalls: []ToolCall{
				{ID: id, Function: ToolCallFunction{Name: "read_file", Arguments: fmt.Sprintf(`{"path":"f%d.txt"}`, i)}},
			}},
			Message{Role: "tool", ToolCallID: id, Content: strings.Repeat(fmt.Sprintf("%d", i), 6000)},
		)
	}
	return append(msgs, Message{Role: "assistant", Content: "done"})
}

func newMaskTestAgent(t *testing.T, provider Provider) *Agent {
	t.Helper()
	a, err := NewAgent(Options{
		Provider: provider,
		Executor: &mockExecutor{},
		Optimizer: NewConversationOptimizer(ConversationOptimizerOptions{
			Enabled: true,
			KnownToolFn: func(name string) ToolCategory {
				if name == "read_file" {
					return ToolCategoryFileRead
				}
				return ToolCategoryUnknown
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestPrepareMessages_PersistsObservationMaskingToState(t *testing.T) {
	provider := &mockProvider{
		info:       ProviderInfo{ContextSize: 10000},
		tokenCount: 9000, // above 0.85 × 10000 trigger → Phase 0b fires
	}
	a := newMaskTestAgent(t, provider)
	a.State().SetMessages(maskSeedMessages())

	ch := newConversationHandler(a)

	first := ch.prepareMessages()
	firstPlaceholder := toolResultContent(first, "call-0")
	if !strings.Contains(firstPlaceholder, "[PREVIOUS RESULT:") {
		t.Fatalf("expected masked placeholder in first prepareMessages, got %q", firstPlaceholder)
	}

	// Masking must be persisted to state so the next iteration doesn't redo it.
	stateTool := toolResultContent(a.State().Messages(), "call-0")
	if !strings.Contains(stateTool, "[PREVIOUS RESULT:") {
		t.Fatalf("expected masked content persisted to state, got %q", stateTool)
	}

	// The keep-last window must still be honored in state: newest results stay raw.
	stateNewest := toolResultContent(a.State().Messages(), "call-7")
	if stateNewest == "" || strings.Contains(stateNewest, "[PREVIOUS RESULT:") {
		t.Fatalf("expected newest tool result unmasked in state, got %q", stateNewest)
	}

	// Second prepareMessages must not re-mask: the placeholder is already there.
	second := ch.prepareMessages()
	secondPlaceholder := toolResultContent(second, "call-0")
	if !strings.Contains(secondPlaceholder, "[PREVIOUS RESULT:") {
		t.Fatalf("expected placeholder preserved on second prepareMessages, got %q", secondPlaceholder)
	}
	if secondPlaceholder != firstPlaceholder {
		t.Errorf("expected identical placeholder on second call, got %q vs %q", secondPlaceholder, firstPlaceholder)
	}
	// State still reflects the masked content (never restored).
	if got := toolResultContent(a.State().Messages(), "call-1"); !strings.Contains(got, "[PREVIOUS RESULT:") {
		t.Errorf("expected call-1 still masked in state, got %q", got)
	}
}

func TestPrepareMessages_NoMaskingWhenUnderTrigger(t *testing.T) {
	provider := &mockProvider{
		info:       ProviderInfo{ContextSize: 10000},
		tokenCount: 1000, // under 0.85 × 10000 trigger → Phase 0 is a no-op
	}
	a := newMaskTestAgent(t, provider)
	seed := maskSeedMessages()
	a.State().SetMessages(seed)

	ch := newConversationHandler(a)
	prepared := ch.prepareMessages()

	// Raw content must flow through unchanged below the trigger.
	if got := toolResultContent(prepared, "call-0"); !strings.Contains(got, "000000") {
		t.Errorf("expected raw tool content under the trigger, got %q", got)
	}
	// And nothing may be persisted to state.
	if got := toolResultContent(a.State().Messages(), "call-0"); got != seed[2].Content {
		t.Errorf("expected state unchanged under the trigger, got content length %d", len(got))
	}
}
