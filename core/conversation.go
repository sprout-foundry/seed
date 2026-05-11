package core

import (
	"context"
	"errors"
	"fmt"
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

// chatOperation encapsulates a single LLM call. The function performs the call
// (via Chat or ChatStream) and is responsible for recording token usage and
// appending the assistant message to state. In the non-streaming path, this is
// done inline; in the streaming path, it is delegated to the stream handler's
// OnDone callback.
type chatOperation func(ctx context.Context, req *ChatRequest, iter int) (*ChatResponse, error)

// runLoop executes the shared conversation loop. The chatFn parameter performs
// the LLM call and handles state recording (token tracking, message recording).
// Both ProcessQuery and ProcessQueryStream delegate to this method with their
// respective chat implementations.
func (ch *ConversationHandler) runLoop(ctx context.Context, query string, debugName string, chatFn chatOperation) (string, error) {
	ch.agent.debugLog("[~] %s: %s\n", debugName, query)
	ch.conversationStart = time.Now()

	// Reset streaming buffers
	a := ch.agent
	a.outputMgr.ContentBuffer().Reset()
	a.outputMgr.ReasoningBuffer().Reset()

	// Check pause
	if ch.agent.paused {
		return "", ErrPaused
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
			return "", fmt.Errorf("%w: %w", ErrInterrupted, ctx.Err())
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
		resp, err := chatFn(ctx, &ChatRequest{
			Messages: messages,
			Tools:    ch.agent.executor.GetTools(),
		}, iter)
		if err != nil {
			ch.agent.debugLog("[!!] Chat error: %v\n", err)
			// If the context was cancelled, return ErrInterrupted
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return "", fmt.Errorf("%w: %w", ErrInterrupted, err)
			}
			if ch.agent.eventBus != nil {
				ch.agent.eventBus.Publish(events.EventTypeError, events.ErrorEvent("chat failed", err))
			}
			return "", fmt.Errorf("chat failed: %w", err)
		}

		// State recording (tokens + message) is handled by chatFn
		// (inline for non-streaming; via OnDone for streaming)

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
			return "", fmt.Errorf("%w: %w", ErrInterrupted, ctx.Err())
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

// ProcessQuery handles a user query through the complete conversation flow.
func (ch *ConversationHandler) ProcessQuery(ctx context.Context, query string) (string, error) {
	providerName := ch.agent.provider.Info().Model
	chatFn := func(ctx context.Context, req *ChatRequest, iter int) (*ChatResponse, error) {
		// Retry loop for transient/rate-limit errors
		backoff := NewExponentialBackoff(
			100*time.Millisecond, // initial delay
			5*time.Second,        // max delay
			2.0,                  // multiplier
			3,                    // max attempts (1 initial + 2 retries)
			0.0,                  // no jitter (deterministic for tests)
		)
		var lastErr error
		for backoff.NextAttempt() {
			if backoff.attempt > 1 {
				ch.agent.debugLog("[retry] Attempt %d, waiting %v\n", backoff.attempt, backoff.Delay())
				select {
				case <-time.After(backoff.Delay()):
				case <-ctx.Done():
					return nil, fmt.Errorf("%w: %w", ErrInterrupted, ctx.Err())
				}
			}

			resp, err := ch.agent.provider.Chat(ctx, req)
			if err == nil {
				// Record token usage and assistant message inline
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

				ch.agent.state.AddMessage(resp.ToMessage())
				return resp, nil
			}

			// Classify the error
			classifiedErr := ClassifyError(err, providerName)
			lastErr = classifiedErr

			// Fail fast on auth errors — retry won't help
			if IsAuthError(classifiedErr) {
				ch.agent.debugLog("[!!] Auth error, failing fast: %v\n", classifiedErr)
				return nil, classifiedErr
			}

			// Fail fast on context overflow — retry won't help
			if IsContextOverflow(classifiedErr) {
				ch.agent.debugLog("[!!] Context overflow, failing fast: %v\n", classifiedErr)
				return nil, classifiedErr
			}

			// Retry on transient/rate-limit errors.
			// (ClassifyError defaults to TransientError for unknown errors, so this
			// path always matches after auth/context-overflow are handled above.)
			if IsTransient(classifiedErr) || IsRateLimit(classifiedErr) {
				ch.agent.debugLog("[retry] Retryable error: %v\n", classifiedErr)
				// Publish error event for each retry attempt (observability).
				if ch.agent.eventBus != nil {
					ch.agent.eventBus.Publish(events.EventTypeError, events.ErrorEvent("chat failed", classifiedErr))
				}
				continue
			}
		}

		// All retries exhausted — return the last classified error
		return nil, lastErr
	}

	return ch.runLoop(ctx, query, "ProcessQuery", chatFn)
}

// ProcessQueryStream handles a user query through the streaming conversation
// flow. It uses provider.ChatStream() instead of provider.Chat(), so content
// is delivered incrementally via StreamHandler callbacks. The streaming buffer
// is populated as content arrives, and the return value is empty when the
// buffer contains streamed content (the caller should read from
// agent.StreamingBuffer() instead).
func (ch *ConversationHandler) ProcessQueryStream(ctx context.Context, query string) (string, error) {
	chatFn := func(ctx context.Context, req *ChatRequest, iter int) (*ChatResponse, error) {
		streamHandler := NewAgentStreamHandler(ch.agent, ch.agent.state)
		streamErr := ch.agent.provider.ChatStream(ctx, req, streamHandler)
		if streamErr != nil {
			return nil, streamErr
		}

		resp := streamHandler.Response()
		if resp == nil {
			return nil, fmt.Errorf("chat stream returned no response")
		}
		// Token tracking and assistant message already recorded in OnDone
		return resp, nil
	}

	return ch.runLoop(ctx, query, "ProcessQueryStream", chatFn)
}

// compactMessages reduces the message list to fit within the context window.
func (ch *ConversationHandler) compactMessages(messages []Message, limit int) []Message {
	compactor := NewCompactor()
	return compactor.Compact(messages, limit)
}

// enqueueTransientMessage adds a message that will be sent once then discarded.
func (ch *ConversationHandler) enqueueTransientMessage(msg Message) {
	ch.transientMu.Lock()
	defer ch.transientMu.Unlock()
	ch.transientMsgs = append(ch.transientMsgs, msg)
}
