package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sprout-foundry/seed/events"
)

// ConversationHandler manages the high-level conversation flow.
type ConversationHandler struct {
	agent *Agent

	consecutiveBlank  int
	conversationStart time.Time

	transientMu   sync.Mutex
	transientMsgs []Message
}

func newConversationHandler(a *Agent) *ConversationHandler {
	return &ConversationHandler{
		agent:             a,
		conversationStart: time.Now(),
	}
}

// ProcessQuery handles a user query through the complete conversation flow.
func (ch *ConversationHandler) ProcessQuery(ctx context.Context, query string) (string, error) {
	ch.agent.debugLog("[~] ProcessQuery: %s\n", query)
	ch.conversationStart = time.Now()

	// Reset streaming buffers
	a := ch.agent
	a.outputMgr.ContentBuffer().Reset()
	a.outputMgr.ReasoningBuffer().Reset()

	// Check pause
	if ch.agent.paused {
		return "", fmt.Errorf("agent is paused; call Resume() before Run()")
	}

	// Add user message
	ch.agent.state.AddMessage(Message{Role: "user", Content: query})

	// Publish query started event
	if ch.agent.eventBus != nil {
		ch.agent.eventBus.Publish(events.EventTypeQueryStarted, map[string]interface{}{
			"query": query,
			"model": ch.agent.provider.Info().Model,
		})
	}

	// Main conversation loop
	completed := false
	for iter := 0; ch.agent.maxIterations == 0 || iter < ch.agent.maxIterations; iter++ {
		// Check for context cancellation at the top of each iteration
		select {
		case <-ctx.Done():
			ch.agent.debugLog("[!!] Context cancelled\n")
			return "", fmt.Errorf("%w: %v", ErrInterrupted, ctx.Err())
		default:
		}

		ch.agent.debugLog("[~] Iteration %d - Messages: %d\n", iter, ch.agent.state.Len())

		// Prepare messages
		messages := ch.prepareMessages()

		// Context management
		tokenEstimate := ch.agent.provider.EstimateTokens(&ChatRequest{
			Messages: messages,
			Tools:    ch.agent.executor.GetTools(),
		})
		contextSize := ch.agent.provider.Info().ContextSize
		if contextSize > 0 && tokenEstimate > contextSize {
			messages = ch.compactMessages(messages, contextSize)
		}

		// Send to LLM
		resp, err := ch.agent.provider.Chat(ctx, &ChatRequest{
			Messages: messages,
			Tools:    ch.agent.executor.GetTools(),
		})
		if err != nil {
			ch.agent.debugLog("[!!] Chat error: %v\n", err)
			// If the context was cancelled, return ErrInterrupted
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return "", fmt.Errorf("%w: %v", ErrInterrupted, err)
			}
			if ch.agent.eventBus != nil {
				ch.agent.eventBus.Publish(events.EventTypeError, events.ErrorEvent("LLM request failed", err))
			}
			return "", fmt.Errorf("LLM request failed: %w", err)
		}

		// Update token tracking
		if resp.Usage.TotalTokens > 0 {
			ch.agent.state.AddTokens(resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)

			// Publish metrics update event
			if ch.agent.eventBus != nil {
				ch.agent.eventBus.Publish(events.EventTypeMetricsUpdate, events.MetricsUpdateEvent(
					ch.agent.state.TotalTokens(),
					resp.Usage.PromptTokens,
					ch.agent.provider.Info().ContextSize,
					iter,
					ch.agent.state.TotalCost(),
				))
			}
		}

		// Record assistant message
		assistantMsg := resp.ToMessage()
		ch.agent.state.AddMessage(assistantMsg)

		// No tool calls? Check for injected input before deciding to break.
		// If injected input is found, continue so the LLM processes it.
		if len(resp.Choices) == 0 || len(assistantMsg.ToolCalls) == 0 {
			select {
			case injectedInput := <-ch.agent.inputInjectionChan:
				ch.agent.state.AddMessage(Message{Role: "user", Content: injectedInput})
				ch.agent.debugLog("[inject] Received injected input: %s\n", injectedInput)
				continue
			default:
			}
			ch.agent.debugLog("[OK] Conversation complete\n")
			completed = true
			break
		}

		// Execute tool calls
		ch.agent.debugLog("[tool] Executing %d tool calls\n", len(assistantMsg.ToolCalls))

		// Check for context cancellation before executing tools
		select {
		case <-ctx.Done():
			ch.agent.debugLog("[!!] Context cancelled before tool execution\n")
			return "", fmt.Errorf("%w: %v", ErrInterrupted, ctx.Err())
		default:
		}

		// Publish tool_start events
		if ch.agent.eventBus != nil {
			for i, tc := range assistantMsg.ToolCalls {
				ch.agent.eventBus.Publish(events.EventTypeToolStart, events.ToolStartEvent(
					tc.Function.Name,
					tc.ID,
					tc.Function.Arguments,
					"",    // displayName
					"",    // persona
					false, // isSubagent
					"",    // subagentType
					i,     // toolIndex
				))
			}
		}

		// Measure execution time
		execStart := time.Now()
		results := ch.agent.executor.Execute(ctx, assistantMsg.ToolCalls)
		execDuration := time.Since(execStart)

		// Publish tool_end events
		if ch.agent.eventBus != nil {
			toolCallMap := make(map[string]ToolCall)
			for _, tc := range assistantMsg.ToolCalls {
				toolCallMap[tc.ID] = tc
			}

			// Track which tool_call_ids have been published
			published := make(map[string]bool)

			for _, r := range results {
				tc, ok := toolCallMap[r.ToolCallID]
				toolName := ""
				if ok {
					toolName = tc.Function.Name
				}
				ch.agent.eventBus.Publish(events.EventTypeToolEnd, events.ToolEndEvent(
					r.ToolCallID,
					toolName,
					"completed",
					r.Content,
					"",           // errorMessage
					execDuration, // duration
				))
				published[r.ToolCallID] = true
			}

			// Defensive: publish tool_end for any started tools that got no result
			for _, tc := range assistantMsg.ToolCalls {
				if !published[tc.ID] {
					ch.agent.eventBus.Publish(events.EventTypeToolEnd, events.ToolEndEvent(
						tc.ID,
						tc.Function.Name,
						"failed",
						"",
						"executor returned no result for this tool call",
						execDuration,
					))
				}
			}
		}

		for _, r := range results {
			ch.agent.state.AddMessage(r)
		}
		ch.agent.debugLog("[ok] Added %d tool results\n", len(results))
	}

	if !completed && ch.agent.maxIterations > 0 {
		ch.agent.debugLog("[WARN] Max iterations reached\n")
	}

	// Finalize
	return ch.finalize(query)
}

// ProcessQueryStream handles a user query through the streaming conversation
// flow. It uses provider.ChatStream() instead of provider.Chat(), so content
// is delivered incrementally via StreamHandler callbacks. The streaming buffer
// is populated as content arrives, and the return value is empty when the
// buffer contains streamed content (the caller should read from
// agent.StreamingBuffer() instead).
func (ch *ConversationHandler) ProcessQueryStream(ctx context.Context, query string) (string, error) {
	ch.agent.debugLog("[~] ProcessQueryStream: %s\n", query)
	ch.conversationStart = time.Now()

	// Reset streaming buffers
	a := ch.agent
	a.outputMgr.ContentBuffer().Reset()
	a.outputMgr.ReasoningBuffer().Reset()

	// Check pause
	if ch.agent.paused {
		return "", fmt.Errorf("agent is paused; call Resume() before Run()")
	}

	// Add user message
	ch.agent.state.AddMessage(Message{Role: "user", Content: query})

	// Publish query started event
	if ch.agent.eventBus != nil {
		ch.agent.eventBus.Publish(events.EventTypeQueryStarted, map[string]interface{}{
			"query": query,
			"model": ch.agent.provider.Info().Model,
		})
	}

	// Main conversation loop
	completed := false
	for iter := 0; ch.agent.maxIterations == 0 || iter < ch.agent.maxIterations; iter++ {
		// Check for context cancellation at the top of each iteration
		select {
		case <-ctx.Done():
			ch.agent.debugLog("[!!] Context cancelled\n")
			return "", fmt.Errorf("%w: %v", ErrInterrupted, ctx.Err())
		default:
		}

		ch.agent.debugLog("[~] Iteration %d - Messages: %d\n", iter, ch.agent.state.Len())

		// Prepare messages
		messages := ch.prepareMessages()

		// Context management
		tokenEstimate := ch.agent.provider.EstimateTokens(&ChatRequest{
			Messages: messages,
			Tools:    ch.agent.executor.GetTools(),
		})
		contextSize := ch.agent.provider.Info().ContextSize
		if contextSize > 0 && tokenEstimate > contextSize {
			messages = ch.compactMessages(messages, contextSize)
		}

		// Send to LLM via streaming
		streamHandler := NewAgentStreamHandler(ch.agent, ch.agent.state)
		streamErr := ch.agent.provider.ChatStream(ctx, &ChatRequest{
			Messages: messages,
			Tools:    ch.agent.executor.GetTools(),
		}, streamHandler)
		if streamErr != nil {
			ch.agent.debugLog("[!!] ChatStream error: %v\n", streamErr)
			if errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
				return "", fmt.Errorf("%w: %v", ErrInterrupted, streamErr)
			}
			if ch.agent.eventBus != nil {
				ch.agent.eventBus.Publish(events.EventTypeError, events.ErrorEvent("LLM request failed", streamErr))
			}
			return "", fmt.Errorf("LLM request failed: %w", streamErr)
		}

		// Get the final response from the stream handler
		resp := streamHandler.Response()
		if resp == nil {
			ch.agent.debugLog("[!!] ChatStream returned nil response\n")
			return "", fmt.Errorf("LLM request failed: stream returned no response")
		}

		// Token tracking and assistant message already recorded in OnDone

		assistantMsg := resp.ToMessage()

		// No tool calls? Check for injected input before deciding to break.
		if len(resp.Choices) == 0 || len(assistantMsg.ToolCalls) == 0 {
			select {
			case injectedInput := <-ch.agent.inputInjectionChan:
				ch.agent.state.AddMessage(Message{Role: "user", Content: injectedInput})
				ch.agent.debugLog("[inject] Received injected input: %s\n", injectedInput)
				continue
			default:
			}
			ch.agent.debugLog("[OK] Conversation complete\n")
			completed = true
			break
		}

		// Execute tool calls
		ch.agent.debugLog("[tool] Executing %d tool calls\n", len(assistantMsg.ToolCalls))

		// Check for context cancellation before executing tools
		select {
		case <-ctx.Done():
			ch.agent.debugLog("[!!] Context cancelled before tool execution\n")
			return "", fmt.Errorf("%w: %v", ErrInterrupted, ctx.Err())
		default:
		}

		// Publish tool_start events
		if ch.agent.eventBus != nil {
			for i, tc := range assistantMsg.ToolCalls {
				ch.agent.eventBus.Publish(events.EventTypeToolStart, events.ToolStartEvent(
					tc.Function.Name,
					tc.ID,
					tc.Function.Arguments,
					"",    // displayName
					"",    // persona
					false, // isSubagent
					"",    // subagentType
					i,     // toolIndex
				))
			}
		}

		// Measure execution time
		execStart := time.Now()
		results := ch.agent.executor.Execute(ctx, assistantMsg.ToolCalls)
		execDuration := time.Since(execStart)

		// Publish tool_end events
		if ch.agent.eventBus != nil {
			toolCallMap := make(map[string]ToolCall)
			for _, tc := range assistantMsg.ToolCalls {
				toolCallMap[tc.ID] = tc
			}

			published := make(map[string]bool)
			for _, r := range results {
				tc, ok := toolCallMap[r.ToolCallID]
				toolName := ""
				if ok {
					toolName = tc.Function.Name
				}
				ch.agent.eventBus.Publish(events.EventTypeToolEnd, events.ToolEndEvent(
					r.ToolCallID,
					toolName,
					"completed",
					r.Content,
					"",           // errorMessage
					execDuration, // duration
				))
				published[r.ToolCallID] = true
			}

			for _, tc := range assistantMsg.ToolCalls {
				if !published[tc.ID] {
					ch.agent.eventBus.Publish(events.EventTypeToolEnd, events.ToolEndEvent(
						tc.ID,
						tc.Function.Name,
						"failed",
						"",
						"executor returned no result for this tool call",
						execDuration,
					))
				}
			}
		}

		for _, r := range results {
			ch.agent.state.AddMessage(r)
		}
		ch.agent.debugLog("[ok] Added %d tool results\n", len(results))
	}

	if !completed && ch.agent.maxIterations > 0 {
		ch.agent.debugLog("[WARN] Max iterations reached\n")
	}

	// Finalize
	return ch.finalize(query)
}

// prepareMessages assembles the message list for the API request.
func (ch *ConversationHandler) prepareMessages() []Message {
	// Get current messages
	messages := ch.agent.state.Messages()

	// Strip system messages from history (we always prepend current system prompt)
	filtered := make([]Message, 0, len(messages))
	for _, m := range messages {
		if m.Role != "system" {
			filtered = append(filtered, m)
		}
	}

	// Strip images for non-vision models
	if !ch.agent.provider.Info().HasVision {
		filtered = ch.stripImages(filtered)
	}

	// Prepend system prompt
	allMessages := []Message{{Role: "system", Content: ch.agent.systemPrompt}}
	allMessages = append(allMessages, filtered...)

	// Append transient messages (one-shot, then discard)
	ch.transientMu.Lock()
	if len(ch.transientMsgs) > 0 {
		allMessages = append(allMessages, ch.transientMsgs...)
		ch.transientMsgs = nil
	}
	ch.transientMu.Unlock()

	// Collapse multiple system messages into one at the front
	allMessages = collapseSystemMessages(allMessages)

	// Sanitize: remove orphaned tool results
	allMessages = ch.removeOrphanedToolResults(allMessages)

	return allMessages
}

// compactMessages reduces the message list to fit within the context window.
func (ch *ConversationHandler) compactMessages(messages []Message, limit int) []Message {
	compactor := NewCompactor()
	return compactor.Compact(messages, limit)
}

// finalize returns the final response content.
func (ch *ConversationHandler) finalize(query string) (string, error) {
	messages := ch.agent.state.Messages()
	var finalContent string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			finalContent = messages[i].Content
			break
		}
	}

	// Publish query completed event
	if ch.agent.eventBus != nil {
		duration := time.Since(ch.conversationStart)
		ch.agent.eventBus.Publish(events.EventTypeQueryCompleted, map[string]interface{}{
			"query":       query,
			"response":    finalContent,
			"tokens":      ch.agent.state.TotalTokens(),
			"cost":        ch.agent.state.TotalCost(),
			"duration_ms": duration.Milliseconds(),
		})

		// Publish agent_message event with final response content
		if finalContent != "" {
			ch.agent.eventBus.Publish(events.EventTypeAgentMessage, events.AgentMessageEvent(
				"info",
				finalContent,
				nil,
			))
		}
	}

	// If streaming was used, return empty to avoid duplicate display
	if ch.agent.outputMgr.ContentBuffer().Len() > 0 {
		return "", nil
	}

	return finalContent, nil
}

// enqueueTransientMessage adds a message that will be sent once then discarded.
func (ch *ConversationHandler) enqueueTransientMessage(msg Message) {
	ch.transientMu.Lock()
	defer ch.transientMu.Unlock()
	ch.transientMsgs = append(ch.transientMsgs, msg)
}

// stripImages removes image data from messages for non-vision models.
func (ch *ConversationHandler) stripImages(messages []Message) []Message {
	out := make([]Message, len(messages))
	copy(out, messages)
	for i := range out {
		out[i].Images = nil
	}
	return out
}

// removeOrphanedToolResults removes tool messages whose tool_call_id doesn't
// match any assistant message with tool_calls.
func (ch *ConversationHandler) removeOrphanedToolResults(messages []Message) []Message {
	validIDs := make(map[string]struct{})
	for _, msg := range messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					validIDs[tc.ID] = struct{}{}
				}
			}
		}
	}

	filtered := make([]Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "tool" {
			if _, ok := validIDs[msg.ToolCallID]; ok {
				filtered = append(filtered, msg)
			} else {
				ch.agent.debugLog("[clean] Removed orphaned tool result: %s\n", msg.ToolCallID)
			}
		} else {
			filtered = append(filtered, msg)
		}
	}
	return filtered
}

// collapseSystemMessages merges multiple system messages into one at the front.
func collapseSystemMessages(messages []Message) []Message {
	if len(messages) <= 1 {
		return messages
	}

	var systemParts []string
	nonSystem := make([]Message, 0, len(messages))

	for _, msg := range messages {
		if msg.Role == "system" {
			if content := strings.TrimSpace(msg.Content); content != "" {
				systemParts = append(systemParts, content)
			}
		} else {
			nonSystem = append(nonSystem, msg)
		}
	}

	if len(systemParts) == 0 {
		return messages
	}

	merged := Message{Role: "system", Content: strings.Join(systemParts, "\n\n")}
	result := make([]Message, 0, len(nonSystem)+1)
	result = append(result, merged)
	result = append(result, nonSystem...)
	return result
}
