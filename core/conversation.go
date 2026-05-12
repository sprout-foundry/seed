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

	// continuationCount tracks consecutive incomplete-response continuations.
	// After maxContinuations without progress, the loop force-finalizes.
	continuationCount int

	// queryStartIndex is the index in state.Messages() where the user query
	// message was added. Used for turn checkpoint recording.
	queryStartIndex int

	// turnEndIndex is the index of the final message in the turn (set when
	// the loop exits normally). Paired with queryStartIndex to define the
	// checkpoint range. Captured at loop-exit time so finalize() uses the
	// correct boundary even if state is mutated afterward.
	turnEndIndex int

	// turnCompleted is true only when the loop exits via the normal
	// completion path (no tool calls and no injected user input).
	// It remains false when finalize() is called after:
	//   - max iterations is reached without a clean exit
	// For other failure paths (context cancelled, agent paused, chat error),
	// finalize() is never reached because runLoop returns early.
	// Used by finalize() to decide whether to record a turn checkpoint.
	turnCompleted bool
}

// maxContinuations is the maximum number of consecutive incomplete-response
// continuations before force-finalizing to prevent infinite loops.
const maxContinuations = 3

func newConversationHandler(a *Agent) *ConversationHandler {
	ch := &ConversationHandler{
		agent:             a,
		conversationStart: time.Now(),
	}

	// Drain externally-queued steering messages into the handler's transient queue.
	// They will be appended by prepareMessages on the first API call, then discarded.
	if steer := a.drainSteerMessages(); steer != nil {
		ch.transientMu.Lock()
		ch.transientMsgs = append(ch.transientMsgs, steer...)
		ch.transientMu.Unlock()
	}

	return ch
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
	ch.queryStartIndex = ch.agent.state.Len()
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

		// Fire OnIteration callback (synchronous; panics are caught to avoid crashing the agent)
		if a.onIteration != nil {
			func() {
				defer func() {
					if r := recover(); r != nil {
						a.debugLog("[!!] OnIteration callback panicked: %v\n", r)
					}
				}()
				a.onIteration(iter, ch.agent.state.Len())
			}()
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
			beforeCount := len(messages)
			result := ch.compactMessages(messages, contextSize)
			messages = result.Messages
			// Publish compaction event
			if ch.agent.eventBus != nil && result.Strategy != "none" {
				ch.agent.eventBus.Publish(events.EventTypeCompaction, events.CompactionEvent(
					result.Strategy,
					beforeCount,
					len(result.Messages),
					result.TokensSaved(),
				))
			}
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

		// Fallback: if no structured tool calls but content has patterns,
		// run the fallback parser and inject extracted calls.
		if len(resp.Choices) > 0 && len(assistantMsg.ToolCalls) == 0 && a.fallbackParser != nil {
			if a.fallbackParser.ShouldUseFallback(assistantMsg.Content, false) {
				result := a.fallbackParser.Parse(assistantMsg.Content)
				if len(result.ToolCalls) > 0 {
					a.debugLog("[fallback] Parsed %d tool calls from content\n", len(result.ToolCalls))
					assistantMsg.ToolCalls = result.ToolCalls
					assistantMsg.Content = result.CleanedContent
					// Update the last message in state (already recorded by chatFn)
					// so that tool results are not orphaned in the next iteration.
					msgs := a.state.Messages()
					if len(msgs) > 0 {
						msgs[len(msgs)-1] = assistantMsg
						a.state.SetMessages(msgs)
					}
				}
			}
		}

		// No tool calls? Check for injected input before deciding to break.
		if len(resp.Choices) == 0 || len(assistantMsg.ToolCalls) == 0 {
			// Injected user input takes priority over truncation recovery.
			// If the user sent input while the model was responding, we prefer
			// to process the user's message rather than auto-continuing.
			select {
			case injectedInput := <-ch.agent.inputInjectionChan:
				ch.agent.state.AddMessage(Message{Role: "user", Content: injectedInput})
				ch.agent.debugLog("[inject] Received injected input: %s\n", injectedInput)
				continue
			default:
			}

			// Truncation check takes priority over the tentative check.
			// A structural truncation marker (e.g., trailing "...") is a
			// stronger signal of incomplete output than tentative phrasing,
			// so only one check fires per response.
			if a.validator != nil && a.validator.LooksTruncated(assistantMsg.Content) {
				if ch.continuationCount < maxContinuations {
					ch.continuationCount++
					ch.agent.debugLog("[validate] Incomplete response detected (continuation %d/%d), looping again\n",
						ch.continuationCount, maxContinuations)
					ch.enqueueTransientMessage(Message{
						Role:    "user",
						Content: "Please continue your response from where you left off.",
					})
					continue
				}
				ch.agent.debugLog("[validate] Max continuations (%d) reached, force-finalizing\n", maxContinuations)
			} else if a.validator != nil && a.validator.LooksLikeTentativePostToolResponse(assistantMsg.Content) {
				if ch.continuationCount < maxContinuations {
					ch.continuationCount++
					ch.agent.debugLog("[validate] Tentative response detected (continuation %d/%d), looping again\n",
						ch.continuationCount, maxContinuations)
					ch.enqueueTransientMessage(Message{
						Role:    "user",
						Content: "Please provide your actual response now.",
					})
					continue
				}
				ch.agent.debugLog("[validate] Max continuations (%d) reached, force-finalizing\n", maxContinuations)
			}

			ch.agent.debugLog("[OK] Conversation complete\n")
			ch.turnCompleted = true
			ch.turnEndIndex = ch.agent.state.Len() - 1
			completed = true
			break
		}

		// Execute tool calls
		ch.continuationCount = 0 // tool calls are progress, reset continuation budget
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
			ch.agent.retryConfig.InitialDelayOrDefault(),
			ch.agent.retryConfig.MaxDelayOrDefault(),
			ch.agent.retryConfig.MultiplierOrDefault(),
			ch.agent.retryConfig.MaxAttemptsOrDefault(),
			ch.agent.retryConfig.JitterOrDefault(),
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
func (ch *ConversationHandler) compactMessages(messages []Message, limit int) CompactionResult {
	compactor := NewCompactor()
	return compactor.Compact(messages, limit)
}

// enqueueTransientMessage adds a message that will be sent once then discarded.
func (ch *ConversationHandler) enqueueTransientMessage(msg Message) {
	ch.transientMu.Lock()
	defer ch.transientMu.Unlock()
	ch.transientMsgs = append(ch.transientMsgs, msg)
}
