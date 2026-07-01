package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
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

	// tentativeRejectionCount tracks consecutive rejections of tentative
	// post-tool responses when finish_reason is "stop". After 2 rejections,
	// the response is accepted to avoid infinite loops. This counter is
	// separate from continuationCount and resets when tool calls are executed.
	tentativeRejectionCount int

	// substanceRejectionCount tracks consecutive rejections of short,
	// non-substantive responses returned after tool results. Mirrors the
	// tentativeRejectionCount pattern: after 2 rejections, the response is
	// accepted to avoid infinite loops. Resets when tool calls are executed.
	substanceRejectionCount int

	// contentFilterRetry tracks whether we've already retried once for a
	// content_filter finish_reason. On first occurrence, we retry. On second,
	// we return a ContentFilteredError to the consumer.
	contentFilterRetry bool
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
// (via Chat or ChatStream) and is responsible for recording token usage.
// The assistant message is NOT appended by this function; that is the
// responsibility of runLoop, which adds the message after fallback parsing
// and normalization to avoid a race window where observers could see the
// un-normalized version.
type chatOperation func(ctx context.Context, req *ChatRequest, iter int) (*ChatResponse, error)

// runLoop executes the shared conversation loop. The chatFn parameter performs
// the LLM call and records token usage. The assistant message is added by
// runLoop itself after normalization. Both ProcessQuery and ProcessQueryStream
// delegate to this method with their respective chat implementations.
func (ch *ConversationHandler) runLoop(ctx context.Context, query string, debugName string, chatFn chatOperation) (string, error) {
	ch.agent.debugLog("[~] %s: %s\n", debugName, query)
	ch.conversationStart = time.Now()

	// Reset streaming buffers
	a := ch.agent
	a.outputMgr.ContentBuffer().Reset()
	a.outputMgr.ReasoningBuffer().Reset()

	// Reset per-query counters
	ch.contentFilterRetry = false
	ch.continuationCount = 0
	ch.tentativeRejectionCount = 0
	ch.substanceRejectionCount = 0
	ch.consecutiveBlank = 0
	ch.turnCompleted = false

	// Check pause
	if ch.agent.paused {
		return "", ErrPaused
	}

	// Add user message
	ch.queryStartIndex = ch.agent.state.Len()
	ch.agent.state.AddMessage(Message{Role: "user", Content: query})

	// Publish query started event
	if ch.agent.eventPublisher != nil {
		ch.agent.eventPublisher.Publish(EventTypeQueryStarted, map[string]interface{}{
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

		// Prepare messages and estimate tokens before the callback
		// so OnIteration receives accurate token information.
		messages := ch.prepareMessages()
		tokenEstimate := ch.agent.provider.EstimateTokens(&ChatRequest{
			Messages: messages,
			Tools:    ch.agent.executor.GetTools(),
		})
		contextSize := ch.agent.provider.Info().ContextSize

		// Fire OnIteration callback (synchronous; panics are caught to avoid crashing the agent)
		if a.onIteration != nil {
			func() {
				defer func() {
					if r := recover(); r != nil {
						a.debugLog("[!!] OnIteration callback panicked: %v\n", r)
					}
				}()
				a.onIteration(iter, ch.agent.state.Len(), tokenEstimate, contextSize)
			}()
		}

		ch.agent.debugLog("[~] Iteration %d - Messages: %d, Tokens: %d\n", iter, ch.agent.state.Len(), tokenEstimate)

		// Context management.
		//
		// The trigger fires when the estimated prompt size crosses a configurable
		// share of the model's context window (default 0.85). Token estimation is
		// approximate (4 chars/token) and a single tool result can dump tens of
		// thousands of tokens into history in one iteration — so the threshold
		// must sit below 1.0 to give the loop room to react before the provider
		// rejects the request as oversized.
		//
		// When the trigger fires, the loop cascades through compaction options
		// in this order of capability:
		//   1. Pruner — if Options.Pruner was provided, its configured strategy
		//      (adaptive by default) handles everything end-to-end.
		//   2. LLM summary — if Options.LLMSummarizer was provided, compress
		//      older middle history into a single durable summary; fall back to
		//      rule-based Compact if still over the trigger.
		//   3. Compact — seed's built-in rule-based pipeline (checkpoint drop /
		//      turn drop / emergency truncate).
		triggerLimit := int(float64(contextSize) * ch.agent.triggerFractionOrDefault())
		if contextSize > 0 && tokenEstimate > triggerLimit {
			beforeCount := len(messages)
			tokensBefore := tokenEstimate
			strategy := "none"

			switch {
			case ch.agent.pruner != nil:
				messages = ch.agent.pruner.Prune(ctx, messages, tokenEstimate, contextSize, PruneCallOptions{
					Optimizer:     ch.agent.optimizer,
					Summarizer:    ch.agent.llmSummarizer,
					Provider:      ch.agent.provider.Info().Model,
					IsAgenticFlow: true,
				})
				strategy = "pruner_" + string(ch.agent.pruner.Strategy())

			case ch.agent.llmSummarizer != nil:
				r := CompactWithLLMSummary(ctx, messages, ch.agent.llmSummarizer)
				if r.Strategy != "none" {
					messages = r.Messages
					strategy = r.Strategy
				}
				// If LLM summary didn't trigger or didn't reduce enough, fall
				// back to the progressive rule-based pipeline.
				if roughTokens(messages) > triggerLimit {
					cr := CompactWith(ch.buildCompactInputs(messages, contextSize))
					messages = cr.Messages
					if cr.Strategy != "none" {
						if strategy == "none" {
							strategy = cr.Strategy
						} else {
							strategy = strategy + "+" + cr.Strategy
						}
					}
				}

			default:
				// Progressive pipeline — substitute then mask before drops.
				// See docs/compaction.md.
				cr := CompactWith(ch.buildCompactInputs(messages, contextSize))
				messages = cr.Messages
				strategy = cr.Strategy
			}

			// Publish a compaction event whenever we actually changed something.
			if ch.agent.eventPublisher != nil && strategy != "none" {
				tokensAfter := roughTokens(messages)
				tokensSaved := 0
				if tokensBefore > tokensAfter {
					tokensSaved = tokensBefore - tokensAfter
				}
				ch.agent.eventPublisher.Publish(EventTypeCompaction, map[string]interface{}{
					"strategy":            strategy,
					"messages_before":     beforeCount,
					"messages_after":      len(messages),
					"message_count_delta": beforeCount - len(messages),
					"tokens_saved":        tokensSaved,
				})
			}
			// Re-estimate after compaction to get accurate prompt size.
			tokenEstimate = ch.agent.provider.EstimateTokens(&ChatRequest{
				Messages: messages,
				Tools:    ch.agent.executor.GetTools(),
			})
		}

		// Tool-threading cleanup: run every iteration as a safety net. The
		// reorder in prepareMessages runs on the raw slice, but the compaction
		// cascade above (pruner / LLM-summary / Compact) mutates the slice
		// afterward and can break the tool-result ordering invariants that
		// MiniMax and DeepSeek enforce strictly (HTTP 400, error 2013). Even
		// without compaction, tool execution in the previous iteration may have
		// appended results out of order. Removing orphaned tool results and
		// reordering here closes the window before the provider send. This is
		// the fix for the recurring "tool call result does not follow tool
		// call" rejection that survived the original reorder-only fix.
		messages = ch.removeOrphanedToolResults(messages)
		messages = reorderToolResultsForThreading(messages)

		// Compute max_tokens for the completion.
		//
		// Math:
		//   1. If caller set an explicit MaxTokens, honor it.
		//   2. Otherwise derive from `ContextSize - estimate - safetyBuffer`
		//      where safetyBuffer = max(estimate * estimateErrorFraction,
		//      minSafetyBuffer). This scales with prompt size so larger
		//      prompts get larger absolute headroom (where they need it,
		//      because the 4-chars-per-token estimator's error band is
		//      proportional to prompt size).
		//   3. Cap the derived value at MaxOutputTokens (provider-reported)
		//      or defaultMaxOutputCap when the provider didn't report one.
		//      Avoids "asked for 190k tokens on a 200k context with a tiny
		//      prompt" requests that some providers reject as malformed.
		//   4. Floor at 1 so we never send max_tokens=0 (which means
		//      "unlimited" on some providers — wrong direction).
		maxTokens := ch.agent.maxTokens
		if maxTokens <= 0 && contextSize > 0 && tokenEstimate > 0 {
			const (
				estimateErrorFraction = 0.10 // assume tokenizer estimate may be off by up to 10%
				minSafetyBuffer       = 1024 // never reserve less than this, even for tiny prompts
				defaultMaxOutputCap   = 16384
			)
			buffer := int(float64(tokenEstimate) * estimateErrorFraction)
			if buffer < minSafetyBuffer {
				buffer = minSafetyBuffer
			}
			derived := contextSize - tokenEstimate - buffer

			// Cap at the model's stated max output (or a sensible default
			// when unreported). Without this, on a 200k-context model with
			// 5k of prompt we'd request 194k of output — most providers
			// don't accept that.
			cap := ch.agent.provider.Info().MaxOutputTokens
			if cap <= 0 {
				cap = defaultMaxOutputCap
			}
			if derived > cap {
				derived = cap
			}

			if derived < 1 {
				derived = 1
			}
			maxTokens = derived
		}

		// Diagnostic pre-send validation. The threading reorder inside
		// prepareMessages can be undone by the compaction cascade above
		// (pruner / LLM-summary / Compact all mutate the slice after reorder
		// runs). Re-validate here as a safety net: when violations are found,
		// capture a full transcript so the recurring "tool call result does
		// not follow tool call" provider rejection (MiniMax 2013) can finally
		// be reproduced and root-caused. The capture never blocks or alters
		// control flow — it is purely observational.
		ch.maybeCaptureThreadingDiagnostics(messages, iter, DiagnosticTriggerPreSendValidation, nil)

		// Send to LLM
		resp, err := chatFn(ctx, &ChatRequest{
			Messages:  messages,
			Tools:     ch.agent.executor.GetTools(),
			MaxTokens: maxTokens,
		}, iter)
		if err != nil {
			ch.agent.debugLog("[!!] Chat error: %v\n", err)
			// If the context was cancelled, return ErrInterrupted
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return "", fmt.Errorf("%w: %w", ErrInterrupted, err)
			}
			if ch.agent.eventPublisher != nil {
				ch.agent.eventPublisher.Publish(EventTypeError, map[string]interface{}{
					"message": "chat failed",
					"error":   err.Error(),
				})
			}
			return "", fmt.Errorf("chat failed: %w", err)
		}

		// Guard against zero-choice responses from the provider.
		if len(resp.Choices) == 0 {
			ch.agent.debugLog("[!!] Provider returned zero choices\n")
			return "", fmt.Errorf("%w: provider returned empty response", ErrZeroChoices)
		}

		// State recording (tokens) is handled by chatFn.
		// The assistant message is added to state here, before the finish-reason
		// dispatch, so that continue/return-finalize paths inside the switch have
		// it in state for the next iteration or finalize(). For fall-through paths,
		// the fallback parser and normalizer update it in place below before any
		// tool execution or loop exit. This ensures the message is never visible
		// to observers before normalization has been applied.

		assistantMsg := resp.ToMessage()
		if assistantMsg.Role == "" {
			assistantMsg.Role = "assistant"
		}
		a.state.AddMessage(assistantMsg)

		// Dispatch on finish reason from the first choice.
		// This provides explicit handling for each termination signal
		// the model can send, rather than inferring from content alone.
		finishReason := ""
		if len(resp.Choices) > 0 {
			finishReason = resp.Choices[0].FinishReason
		}
		switch finishReason {
		case "length":
			// Model hit the max token limit — response is truncated.
			// Continue the conversation to let the model finish.
			//
			// Note: force-finalize (max continuations reached) does NOT set
			// turnCompleted. This is intentional — an aborted turn should not
			// record a checkpoint. See content_filter case for the contrast.
			if ch.continuationCount < maxContinuations {
				ch.continuationCount++
				ch.agent.debugLog("[finish] length — truncated response (continuation %d/%d), looping again\n",
					ch.continuationCount, maxContinuations)
				ch.enqueueTransientMessage(Message{
					Role:    "user",
					Content: "Please continue your response from where you left off.",
				})
				continue
			}
			ch.agent.debugLog("[finish] length — max continuations (%d) reached, force-finalizing\n", maxContinuations)
			return ch.finalize(query)

		case "content_filter":
			// Content was filtered by the provider's safety system.
			// Uses a separate retry mechanism (contentFilterRetry bool) rather
			// than sharing continuationCount: content_filter is a provider-side
			// policy rejection, not a truncation issue. It gets exactly 1 retry
			// regardless of other continuation budget consumed.
			if !ch.contentFilterRetry {
				ch.contentFilterRetry = true
				ch.agent.debugLog("[finish] content_filter — first occurrence, retrying\n")
				// Publish error event for observability (consumer can distinguish
				// first-retry from retry-exhausted by the event message).
				if ch.agent.eventPublisher != nil {
					ch.agent.eventPublisher.Publish(EventTypeError, map[string]interface{}{
						"message": "response filtered by content policy (retrying)",
						"error":   "content_filter",
					})
				}
				ch.enqueueTransientMessage(Message{
					Role:    "user",
					Content: "Your previous response was filtered. Please rephrase your response.",
				})
				continue
			}
			// Second occurrence — return error to consumer.
			ch.agent.debugLog("[finish] content_filter — second occurrence, returning error\n")
			if ch.agent.eventPublisher != nil {
				ch.agent.eventPublisher.Publish(EventTypeError, map[string]interface{}{
					"message": "response filtered by content policy (retry exhausted)",
					"error":   "content_filter",
				})
			}
			return "", &ContentFilteredError{Provider: ch.agent.provider.Info().Model}

		case "stop":
			// "stop" — model completed normally.
			// Check for blank or repetitive content when there are no tool calls.
			if len(assistantMsg.ToolCalls) == 0 {
				isBlank := ch.isBlankIteration(assistantMsg.Content)
				isRepetitive := !isBlank && a.validator != nil && ch.isRepetitiveContent(assistantMsg.Content)

				if isBlank || isRepetitive {
					ch.consecutiveBlank++
					if ch.consecutiveBlank == 1 {
						// First blank/repetitive response — send a reminder.
						reminder := "Your previous response was empty. Please provide a complete response."
						if isRepetitive {
							reminder = "Your previous response appears repetitive. Please provide new content."
						}
						ch.agent.debugLog("[finish] stop with %s content — 1st consecutive, sending reminder\n",
							map[bool]string{true: "repetitive", false: "blank"}[isRepetitive])
						ch.enqueueTransientMessage(Message{
							Role:    "user",
							Content: reminder,
						})
						continue
					}
					// Second consecutive blank/repetitive — force-finalize with error.
					ch.agent.debugLog("[finish] stop with %s content — %d consecutive blank/repetitive responses, force-finalizing with error\n",
						map[bool]string{true: "repetitive", false: "blank"}[isRepetitive], ch.consecutiveBlank)
					return "", &BlankResponseError{
						Provider: ch.agent.provider.Info().Model,
						Count:    ch.consecutiveBlank,
					}
				}
				// Non-blank, non-repetitive content — reset the counter.
				ch.consecutiveBlank = 0
			}
			// If content is structurally incomplete (truncated), ask the model
			// to provide its final answer. This catches cases where the model
			// sends "stop" but the content has trailing "...", abrupt endings,
			// or unclosed code blocks.
			if a.validator != nil && a.validator.LooksTruncated(assistantMsg.Content) && len(assistantMsg.ToolCalls) == 0 {
				if ch.continuationCount < maxContinuations {
					ch.continuationCount++
					ch.agent.debugLog("[finish] stop with incomplete content — asking for final answer (continuation %d/%d), looping again\n",
						ch.continuationCount, maxContinuations)
					ch.enqueueTransientMessage(Message{
						Role:    "user",
						Content: "Your previous response appears incomplete. Please provide your final answer.",
					})
					continue
				}
				ch.agent.debugLog("[finish] stop with incomplete content — max continuations (%d) reached, force-finalizing\n", maxContinuations)
				return ch.finalize(query)
			}
			// If the model returned "stop" immediately after tool results with
			// tentative/planning content, reject it and ask for real action.
			// After 2 rejections, accept the response to avoid infinite loops.
			if a.validator != nil && len(assistantMsg.ToolCalls) == 0 &&
				ch.followsRecentToolResults() &&
				a.validator.LooksLikeTentativePostToolResponse(assistantMsg.Content) {
				ch.tentativeRejectionCount++
				if ch.tentativeRejectionCount >= 2 {
					// Accept after 2 rejections to avoid loops.
					ch.tentativeRejectionCount = 0
					ch.agent.debugLog("[finish] stop — tentative post-tool rejection limit reached, accepting response\n")
					// Fall through to the existing tool-call / completion logic below.
				} else {
					ch.agent.debugLog("[finish] stop — tentative content after tool results (rejection %d/2), looping again\n",
						ch.tentativeRejectionCount)
					ch.enqueueTransientMessage(Message{
						Role: "user",
						Content: "You just received tool results. Do not stop with a planning note. " +
							"Either take the next concrete action or provide the actual final answer now.",
					})
					continue
				}
			}
			// Minimum-substance-after-tools guard: when the model returns "stop"
			// with no tool calls immediately after tool results, and the content
			// is short and lacks structural/content markers of genuine findings
			// (file paths, code blocks, error messages, etc.), reject it and ask
			// for a real synthesis. After 2 rejections, accept to avoid loops.
			// This catches the gap the tentative check misses: a brief
			// meta-acknowledgement like "I've reviewed the files." is neither
			// blank, nor tentative planning language, nor truncated — yet it
			// conveys no findings and leaves the caller with nothing useful.
			if a.validator != nil && !a.disableSubstanceGuard &&
				len(assistantMsg.ToolCalls) == 0 &&
				ch.followsRecentToolResults() &&
				a.validator.LooksInsufficientAfterToolCalls(assistantMsg.Content) {
				ch.substanceRejectionCount++
				if ch.substanceRejectionCount >= 2 {
					ch.substanceRejectionCount = 0
					ch.agent.debugLog("[finish] stop — insufficient post-tool substance rejection limit reached, accepting response\n")
					// Fall through to the existing tool-call / completion logic below.
				} else {
					ch.agent.debugLog("[finish] stop — insufficient post-tool substance (rejection %d/2), looping again\n",
						ch.substanceRejectionCount)
					ch.enqueueTransientMessage(Message{
						Role: "user",
						Content: "You just received tool results. Your previous response was too brief to convey findings. " +
							"Synthesize what you learned: cite specific file paths, line numbers, function names, " +
							"error messages, or relevant code snippets from the tool results.",
					})
					continue
				}
			}
			// Fall through to the existing tool-call / completion logic below.

		case "tool_calls", "":
			// "tool_calls" — model made tool calls, fall through to tool execution.
			// ""  — no finish reason (common when tool_calls are present).
			// Fall through to the existing tool-call / completion logic below.

		default:
			// Unknown finish reason — treat conservatively as incomplete
			// and attempt continuation if budget allows.
			ch.agent.debugLog("[finish] unknown finish reason %q\n", finishReason)
			if ch.continuationCount < maxContinuations {
				ch.continuationCount++
				ch.agent.debugLog("[finish] unknown — attempting continuation (%d/%d)\n",
					ch.continuationCount, maxContinuations)
				ch.enqueueTransientMessage(Message{
					Role:    "user",
					Content: "Please continue.",
				})
				continue
			}
			ch.agent.debugLog("[finish] unknown — max continuations (%d) reached, force-finalizing\n", maxContinuations)
			return ch.finalize(query)
		}

		// Fallback: if no structured tool calls but content has patterns,
		// run the fallback parser and inject extracted calls.
		// Skip if the finish reason already finalized the turn.
		if !completed && len(resp.Choices) > 0 && len(assistantMsg.ToolCalls) == 0 && a.fallbackParser != nil {
			if a.fallbackParser.ShouldUseFallback(assistantMsg.Content, false) {
				result := a.fallbackParser.Parse(assistantMsg.Content)
				if len(result.ToolCalls) > 0 {
					a.debugLog("[fallback] Parsed %d tool calls from content\n", len(result.ToolCalls))
					assistantMsg.ToolCalls = result.ToolCalls
					assistantMsg.Content = result.CleanedContent
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
			}

			ch.agent.debugLog("[OK] Conversation complete\n")
			// Sync the (potentially fallback-parsed) assistant message to state
			// before finalization so the recorded content reflects normalization.
			msgs := a.state.Messages()
			if len(msgs) > 0 {
				msgs[len(msgs)-1] = assistantMsg
				a.state.SetMessages(msgs)
			}
			ch.turnCompleted = true
			ch.turnEndIndex = ch.agent.state.Len() - 1
			completed = true
			break
		}

		// Execute tool calls

		// Normalize structured tool calls before execution.
		// Track whether all calls were malformed so we can ask the model to re-emit.
		allMalformed := false
		if a.normalizer != nil && len(assistantMsg.ToolCalls) > 0 {
			originalCount := len(assistantMsg.ToolCalls)
			normalized := a.normalizer.Normalize(assistantMsg.ToolCalls)
			dropped := originalCount - len(normalized)
			if dropped > 0 {
				a.debugLog("[normalize] Dropped %d malformed/duplicate tool calls (%d → %d)\n",
					dropped, originalCount, len(normalized))
			}
			// If every call was dropped, flag it for retry below.
			allMalformed = len(normalized) == 0
			assistantMsg.ToolCalls = []ToolCall(normalized)
		}

		// Sync the (potentially normalized) assistant message to state.
		// This is done once after both fallback parsing and normalization,
		// so tool results are linked to the correct tool calls in the next iteration.
		msgs := a.state.Messages()
		if len(msgs) > 0 {
			msgs[len(msgs)-1] = assistantMsg
			a.state.SetMessages(msgs)
		}

		if len(assistantMsg.ToolCalls) == 0 {
			// All structured tool calls were malformed — ask the model to
			// re-emit them with proper formatting, then loop again.
			if allMalformed {
				if ch.continuationCount < maxContinuations {
					ch.continuationCount++
					ch.agent.debugLog("[normalize] All tool calls malformed (continuation %d/%d), asking model to re-emit\n",
						ch.continuationCount, maxContinuations)
					ch.enqueueTransientMessage(Message{
						Role: "user",
						Content: "Your previous tool call was malformed. " +
							"Please re-emit it using the proper structured tool call format.",
					})
					continue
				}
				ch.agent.debugLog("[normalize] Max continuations (%d) reached for malformed calls, force-finalizing\n", maxContinuations)
			}
			a.debugLog("[normalize] No tool calls remaining after normalization, finalizing\n")
			ch.turnCompleted = true
			ch.turnEndIndex = ch.agent.state.Len() - 1
			completed = true
			break
		}

		// Valid tool calls present — they represent progress, so reset the
		// continuation budget and blank counter before executing.
		ch.continuationCount = 0
		ch.tentativeRejectionCount = 0
		ch.substanceRejectionCount = 0
		ch.consecutiveBlank = 0

		ch.agent.debugLog("[tool] Executing %d tool calls\n", len(assistantMsg.ToolCalls))

		// Check for context cancellation before executing tools
		select {
		case <-ctx.Done():
			ch.agent.debugLog("[!!] Context cancelled before tool execution\n")
			return "", fmt.Errorf("%w: %w", ErrInterrupted, ctx.Err())
		default:
		}

		// Publish tool_start events
		if ch.agent.eventPublisher != nil {
			for i, tc := range assistantMsg.ToolCalls {
				ch.agent.eventPublisher.Publish(EventTypeToolStart, map[string]interface{}{
					"tool_name":    tc.Function.Name,
					"tool_call_id": tc.ID,
					"arguments":    tc.Function.Arguments,
					"tool_index":   i,
				})
			}
		}

		// Measure execution time
		execStart := time.Now()
		results := ch.agent.executor.Execute(ctx, assistantMsg.ToolCalls)
		execDuration := time.Since(execStart)

		// --- Threading recovery: dedupe + synthesize missing results ---
		//
		// When Execute returns fewer (or duplicate) results than the number of
		// tool calls, the conversation state would end up with N assistant
		// tool-calls but only M<N tool-result messages. Providers like MiniMax
		// and DeepSeek reject this with "tool call result does not follow tool
		// call" (error 2013). Fix it here so the message list stays well-formed.
		//
		// Step 1: Deduplicate results (keep first occurrence of each ToolCallID).
		seenResultIDs := make(map[string]bool, len(results))
		deduped := results[:0]
		for _, r := range results {
			if r.ToolCallID != "" && seenResultIDs[r.ToolCallID] {
				continue // skip duplicate (empty IDs are kept; they're assigned later)
			}
			if r.ToolCallID != "" {
				seenResultIDs[r.ToolCallID] = true
			}
			deduped = append(deduped, r)
		}
		results = deduped

		// Step 2: Synthesize error results for any tool call that has no match.
		for _, tc := range assistantMsg.ToolCalls {
			if tc.ID == "" {
				continue // empty IDs are kept; they're assigned later
			}
			if !seenResultIDs[tc.ID] {
				ch.agent.debugLog("[threading-recovery] Synthesizing missing result for tool call %s\n", tc.ID)
				results = append(results, ToolErrorMessage(
					tc.ID,
					tc.Function.Name,
					"synthetic-result: executor did not return a result for this tool call; recovery placeholder to keep message list well-formed",
				))
			}
		}
		// --- end threading recovery ---

		// Publish tool_end events
		if ch.agent.eventPublisher != nil {
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
				// Status comes from the Executor via Message.Status (set by
				// ToolRegistry's ToolResultMessage / ToolErrorMessage helpers).
				// Executors that don't tag results default to "completed" so
				// the historical behavior is preserved.
				status := r.Status
				if status == "" {
					status = ToolStatusCompleted
				}
				data := map[string]interface{}{
					"tool_call_id": r.ToolCallID,
					"tool_name":    toolName,
					"status":       status,
					"duration_ms":  execDuration.Milliseconds(),
				}
				if r.Content != "" {
					// Truncate results to 2000 chars for the WebUI - full result stays in the conversation
					if len(r.Content) > 2000 {
						data["result"] = r.Content[:2000] + "\n... (truncated)"
						data["result_truncated"] = true
						data["result_length"] = len(r.Content)
					} else {
						data["result"] = r.Content
						data["result_truncated"] = false
						data["result_length"] = len(r.Content)
					}
				}
				ch.agent.eventPublisher.Publish(EventTypeToolEnd, data)
				published[r.ToolCallID] = true
			}

			for _, tc := range assistantMsg.ToolCalls {
				if !published[tc.ID] {
					data := map[string]interface{}{
						"tool_call_id": tc.ID,
						"tool_name":    tc.Function.Name,
						"status":       ToolStatusError,
						"duration_ms":  execDuration.Milliseconds(),
						"error":        "executor returned no result for this tool call",
					}
					ch.agent.eventPublisher.Publish(EventTypeToolEnd, data)
				}
			}
		}

		for _, r := range results {
			ch.agent.state.AddMessage(r)
		}
		ch.agent.debugLog("[ok] Added %d tool results\n", len(results))

		// Check for injected user input after tool execution. Without this
		// check, a steer message sits in inputInjectionChan until the model
		// eventually returns a response with no tool calls — which may never
		// happen during long agentic runs. Injecting here ensures the user's
		// message is picked up at the next loop iteration boundary.
		select {
		case injectedInput := <-ch.agent.inputInjectionChan:
			ch.agent.state.AddMessage(Message{Role: "user", Content: injectedInput})
			ch.agent.debugLog("[inject] Received injected input after tool execution: %s\n", injectedInput)
		default:
		}
	}

	if !completed && ch.agent.maxIterations > 0 {
		ch.agent.debugLog("[WARN] Max iterations (%d) reached\n", ch.agent.maxIterations)
		if ch.agent.eventPublisher != nil {
			ch.agent.eventPublisher.Publish(EventTypeError, map[string]interface{}{
				"message": "max iterations reached",
				"error":   fmt.Sprintf("max iterations (%d) reached", ch.agent.maxIterations),
			})
		}
		return "", fmt.Errorf("max iterations (%d) reached: %w", ch.agent.maxIterations, ErrMaxIterations)
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
				// Record token usage inline.
				// The assistant message is NOT added to state here; that is the
				// responsibility of runLoop, which adds the message after fallback
				// parsing and normalization to avoid a race window where observers
				// could see the un-normalized version.
				if resp.Usage.TotalTokens > 0 {
					ch.agent.state.AddTokens(resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)

					// Publish metrics update event
					if ch.agent.eventPublisher != nil {
						ch.agent.eventPublisher.Publish(EventTypeMetricsUpdate, map[string]interface{}{
							"total_tokens":       ch.agent.state.TotalTokens(),
							"context_tokens":     resp.Usage.PromptTokens,
							"max_context_tokens": ch.agent.provider.Info().ContextSize,
							"iteration":          iter,
							"total_cost":         ch.agent.state.TotalCost(),
						})
					}
				}

				return resp, nil
			}

			// Classify the error
			classifiedErr := ClassifyError(err, providerName)
			lastErr = classifiedErr

			// Reactive diagnostic capture: when the provider explicitly rejects
			// the request for tool-call threading violations (MiniMax 2013),
			// snapshot the exact messages we sent plus local validation of
			// them, so the failure can be reproduced offline. This is the
			// authoritative capture path — the provider saw these messages and
			// rejected them.
			if IsToolThreadingError(classifiedErr) {
				ch.maybeCaptureThreadingDiagnostics(req.Messages, iter, DiagnosticTriggerProviderRejection, classifiedErr)
			}

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

			// Fail fast on tool-threading errors — retrying the identical
			// malformed message list wastes the retry budget (each attempt
			// sends the same broken request and gets the same rejection).
			if IsToolThreadingError(classifiedErr) {
				ch.agent.debugLog("[!!] Tool threading error, failing fast: %v\n", classifiedErr)
				return nil, classifiedErr
			}

			// Retry on transient/rate-limit errors.
			// (ClassifyError defaults to TransientError for unknown errors, so this
			// path always matches after auth/context-overflow are handled above.)
			if IsTransient(classifiedErr) || IsRateLimit(classifiedErr) {
				ch.agent.debugLog("[retry] Retryable error: %v\n", classifiedErr)
				// Publish error event for each retry attempt (observability).
				if ch.agent.eventPublisher != nil {
					ch.agent.eventPublisher.Publish(EventTypeError, map[string]interface{}{
						"message": "chat failed",
						"error":   classifiedErr.Error(),
					})
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
// is populated as content arrives. The return value is the final response
// content extracted from conversation state, just like ProcessQuery.
func (ch *ConversationHandler) ProcessQueryStream(ctx context.Context, query string) (string, error) {
	chatFn := func(ctx context.Context, req *ChatRequest, iter int) (*ChatResponse, error) {
		streamHandler := NewAgentStreamHandler(ch.agent, ch.agent.state)
		streamErr := ch.agent.provider.ChatStream(ctx, req, streamHandler)
		if streamErr != nil {
			// Classify and capture diagnostics for threading violations, mirroring
			// the non-streaming ProcessQuery path. Streaming requests can hit the
			// same MiniMax 2013 rejection; without this, failures escape unclassified
			// and no diagnostic transcript is saved. Unlike the non-streaming path,
			// there is no retry loop here — streaming errors are terminal.
			classifiedErr := ClassifyError(streamErr, ch.agent.provider.Info().Model)
			if IsToolThreadingError(classifiedErr) {
				ch.maybeCaptureThreadingDiagnostics(req.Messages, iter, DiagnosticTriggerProviderRejection, classifiedErr)
			}
			return nil, classifiedErr
		}

		resp := streamHandler.Response()
		if resp == nil {
			return nil, fmt.Errorf("chat stream returned no response")
		}
		// Token tracking is done in OnDone; assistant message is added by runLoop.
		return resp, nil
	}

	return ch.runLoop(ctx, query, "ProcessQueryStream", chatFn)
}

// maybeCaptureThreadingDiagnostics runs ValidateToolThreading against the
// prepared message list and, if violations are found (or a provider rejection
// error is supplied), invokes the agent's OnDiagnosticCapture callback with a
// full transcript snapshot. It is safe to call even when no callback is
// configured (no-op) and never panics: a panicking callback is recovered and
// logged so diagnostic capture can never break the conversation flow.
//
// trigger is one of the DiagnosticTrigger* constants. triggerErr is the
// provider error for reactive captures (DiagnosticTriggerProviderRejection)
// and nil for proactive pre-send captures.
func (ch *ConversationHandler) maybeCaptureThreadingDiagnostics(messages []Message, iter int, trigger string, triggerErr error) {
	if ch.agent.onDiagnostic == nil {
		return
	}

	violations := ValidateToolThreading(messages)

	// Proactive pre-send: only fire when there is something wrong. Reactive:
	// always fire (the provider already rejected the request) even if our
	// local validator happens to miss it — the provider's view is authoritative.
	if trigger == DiagnosticTriggerPreSendValidation && len(violations) == 0 {
		return
	}

	sortViolations(violations)

	errMsg := ""
	if triggerErr != nil {
		errMsg = triggerErr.Error()
	}

	capture := DiagnosticCapture{
		Trigger:      trigger,
		Provider:     ch.agent.provider.Info().Model,
		Iteration:    iter,
		Violations:   violations,
		Messages:     cloneMessagesForCapture(messages),
		Error:        errMsg,
		SystemPrompt: ch.agent.systemPrompt,
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				buf := make([]byte, 4096)
				n := runtime.Stack(buf, false)
				ch.agent.debugLog("[!!] OnDiagnosticCapture callback panicked: %v\n%s\n", r, buf[:n])
			}
		}()
		ch.agent.onDiagnostic(capture)
	}()
}

// cloneMessagesForCapture returns a shallow copy of the message slice with
// copied ToolCalls/Images/Meta so the diagnostic snapshot is insulated from
// any later in-place mutation of the live conversation state.
func cloneMessagesForCapture(messages []Message) []Message {
	out := make([]Message, len(messages))
	for i := range messages {
		m := messages[i]
		if len(m.ToolCalls) > 0 {
			tc := make([]ToolCall, len(m.ToolCalls))
			copy(tc, m.ToolCalls)
			m.ToolCalls = tc
		}
		if len(m.Images) > 0 {
			img := make([]ImageData, len(m.Images))
			copy(img, m.Images)
			m.Images = img
		}
		if len(m.Meta) > 0 {
			meta := make(map[string]string, len(m.Meta))
			for k, v := range m.Meta {
				meta[k] = v
			}
			m.Meta = meta
		}
		out[i] = m
	}
	return out
}

// compactMessages reduces the message list to fit within the context window.
func (ch *ConversationHandler) compactMessages(messages []Message, limit int) CompactionResult {
	return Compact(messages, limit)
}

// buildCompactInputs assembles the CompactInputs struct for the loop's
// drop-phase fallback (Phase 1+). The pipeline splits across two sites:
//
//   - Phase 0a/0b (iterative checkpoint substitution + observation masking)
//     run inside prepareMessages on the RAW state.Messages() slice where
//     checkpoint indices remain valid.
//   - Phase 1+ (drop checkpoint summaries / drop turns / emergency truncate)
//     run here, on the post-prepareMessages slice where indices have been
//     shifted by the system-prompt prepend.
//
// We deliberately DO NOT pass Checkpoints to CompactWith here — they
// reference state.Messages indices that no longer match the prepared slice.
// Phase 0 already ran on that raw slice; if it couldn't get under target,
// Phase 1+ drops the resulting summary messages (which DO live in the
// prepared slice) directly.
func (ch *ConversationHandler) buildCompactInputs(messages []Message, tokenLimit int) CompactInputs {
	provider := ch.agent.provider
	tools := ch.agent.executor.GetTools()
	return CompactInputs{
		Messages:   messages,
		TokenLimit: tokenLimit,
		// Use the provider's tokenizer so the trigger decision and the
		// drop pipeline agree on when we're under target.
		EstimateFn: func(msgs []Message) int {
			return provider.EstimateTokens(&ChatRequest{Messages: msgs, Tools: tools})
		},
		// Checkpoints / MaskNameFn intentionally omitted — Phase 0 ran
		// in prepareMessages on the raw slice.
	}
}

// tryContextOverflowRecovery runs an aggressive recovery compaction pass on
// req.Messages and returns true if the message list shrunk. Called from the
// retry loop after a ContextOverflowError to give the next attempt a chance
// to fit inside the model's context window.
//
// The recovery target is tighter than the proactive trigger
// (recoveryCompactionTargetFraction, default 0.70) so the retried request has
// clear headroom for both the response budget and the difference between our
// rough token estimator and the provider's actual tokenizer.
func (ch *ConversationHandler) tryContextOverflowRecovery(req *ChatRequest) bool {
	if req == nil || len(req.Messages) == 0 {
		return false
	}
	contextSize := ch.agent.provider.Info().ContextSize
	if contextSize <= 0 {
		return false
	}

	// Compact passes its tokenLimit through emergencyTargetFraction internally,
	// so passing contextSize * recoveryCompactionTargetFraction as the limit
	// yields a final target of roughly 0.7 * 0.85 * contextSize estimated tokens.
	recoveryLimit := int(float64(contextSize) * recoveryCompactionTargetFraction)
	before := len(req.Messages)
	// Even the recovery path benefits from substituting summaries before
	// dropping content — it's less destructive than dropping turns
	// outright, and we already paid the cost of building the summaries.
	result := CompactWith(ch.buildCompactInputs(req.Messages, recoveryLimit))
	if result.Strategy == "none" || len(result.Messages) >= before {
		// Nothing was actually dropped — compaction has bottomed out.
		return false
	}
	req.Messages = result.Messages

	if ch.agent.eventPublisher != nil {
		ch.agent.eventPublisher.Publish(EventTypeCompaction, map[string]interface{}{
			"strategy":            result.Strategy,
			"messages_before":     before,
			"messages_after":      len(result.Messages),
			"message_count_delta": before - len(result.Messages),
			"tokens_saved":        result.TokensSaved(),
			"trigger":             "context_overflow_recovery",
		})
	}
	return true
}

// enqueueTransientMessage adds a message that will be sent once then discarded.
func (ch *ConversationHandler) enqueueTransientMessage(msg Message) {
	ch.transientMu.Lock()
	defer ch.transientMu.Unlock()
	ch.transientMsgs = append(ch.transientMsgs, msg)
}

// isBlankContent checks if the content is empty or contains only whitespace.
func isBlankContent(content string) bool {
	return len(strings.TrimSpace(content)) == 0
}

// isBlankIteration checks if the model's response content is empty or
// whitespace-only, indicating a blank iteration that should trigger
// continuation rather than finalization.
func (ch *ConversationHandler) isBlankIteration(content string) bool {
	return isBlankContent(content)
}

// isRepetitiveContent checks if the current response content is repetitive
// compared to the previous assistant message. It compares against the
// assistant message that precedes the current one in the history (the
// current message is already in state when this is called, so we skip it
// and look at the one before). Returns false if there is no previous
// assistant message to compare against.
func (ch *ConversationHandler) isRepetitiveContent(content string) bool {
	prev := ch.previousAssistantMessage()
	if prev == nil {
		return false
	}
	return contentSimilar(content, prev.Content)
}

// previousAssistantMessage returns the assistant message that precedes the
// current one in the message history. The current assistant message is
// already in state (added by runLoop before finish-reason dispatch), so we
// skip it and look back for the prior assistant message.
func (ch *ConversationHandler) previousAssistantMessage() *Message {
	msgs := ch.agent.state.Messages()
	return findPreviousRole(msgs, "assistant", "assistant")
}

// findPreviousRole walks backward from the end of msgs, skipping the last
// message if its role matches skipRole, and returns the first message whose
// Role matches targetRole. Returns nil if not found.
// This is used by previousAssistantMessage. A similar backwards-walk pattern
// is also used in followsRecentToolResults.
func findPreviousRole(msgs []Message, skipRole, targetRole string) *Message {
	if len(msgs) == 0 {
		return nil
	}
	i := len(msgs) - 1
	if msgs[i].Role == skipRole {
		i--
	}
	for ; i >= 0; i-- {
		if msgs[i].Role == targetRole {
			msg := msgs[i]
			return &msg
		}
	}
	return nil
}

// repetitionMinOverlapCount is the minimum number of overlapping words
// required to flag content as repetitive, even when the overlap ratio
// passes repetitionMinOverlap. This prevents short texts (e.g., a 12-word
// summary) from being flagged when most of a much longer response is new.
const repetitionMinOverlapCount = 10

// repetitionMinOverlap is the minimum word-overlap ratio (0.0–1.0) required
// to flag content as repetitive. The ratio is computed against the shorter
// message's word set. A value of 0.8 means at least 80% of the shorter
// message's words must appear in the longer one.
const repetitionMinOverlap = 0.8

// contentSimilar returns true if two content strings are highly similar,
// indicating the model may be repeating itself. It uses a combination of
// exact match (after normalization) and word-overlap heuristic.
func contentSimilar(a, b string) bool {
	na := normalizeForComparison(a)
	nb := normalizeForComparison(b)

	if na == "" || nb == "" {
		return false
	}

	// Exact match after normalization.
	if na == nb {
		return true
	}

	// Word-overlap heuristic: if the overlap ratio exceeds the threshold
	// and the shorter text has enough words, consider it repetitive.
	wordsA := strings.Fields(na)
	wordsB := strings.Fields(nb)
	if len(wordsA) == 0 || len(wordsB) == 0 {
		return false
	}

	// Ensure wordsA is the shorter set.
	if len(wordsA) > len(wordsB) {
		wordsA, wordsB = wordsB, wordsA
	}

	// Build a set of words from the longer text.
	setB := make(map[string]bool, len(wordsB))
	for _, w := range wordsB {
		setB[w] = true
	}

	// Count overlap.
	overlap := 0
	for _, w := range wordsA {
		if setB[w] {
			overlap++
		}
	}

	// Require both: high overlap ratio AND minimum overlap of repetitionMinOverlapCount
	// to avoid false positives on short responses.
	return overlap >= repetitionMinOverlapCount && float64(overlap)/float64(len(wordsA)) > repetitionMinOverlap
}

// normalizeForComparison lowercases, trims whitespace, and strips common
// trailing punctuation for comparison. This reduces false negatives from
// minor punctuation differences between otherwise identical messages.
func normalizeForComparison(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimRight(s, ".!,;:?")
	return s
}

// followsRecentToolResults scans the message history backwards from the most
// recent message to determine whether the current turn follows tool results.
// It walks back past the current assistant message (added to state before
// finish-reason dispatch) and checks for one or more "tool" role messages.
// Returns true if tool results are found immediately before the current
// response, indicating the model just received tool output and should act on
// it rather than planning.
func (ch *ConversationHandler) followsRecentToolResults() bool {
	msgs := ch.agent.state.Messages()
	if len(msgs) == 0 {
		return false
	}

	i := len(msgs) - 1
	// Skip the current assistant message (already in state from runLoop).
	if msgs[i].Role == "assistant" {
		i--
	}
	if i < 0 {
		return false
	}

	// Walk back through consecutive tool results.
	foundTool := false
	for ; i >= 0 && msgs[i].Role == "tool"; i-- {
		foundTool = true
	}
	return foundTool
}

// ansiRegex matches common ANSI escape sequences:
// CSI sequences (e.g. \x1b[31m), OSC sequences (e.g. \x1b]...;\x07),
// set-charset sequences (e.g. \x1b(B), and device control strings
// (e.g. \x1bP...\\).
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b\][^\x1b]*\x1b\\|\x1b\([A-Za-z0-9]|\x1bP[\x20-\x7E]*\x1b\\`)

// sanitizeANSI strips ANSI escape codes from content. This prevents terminal
// formatting codes (colors, cursor moves, etc.) from polluting LLM context
// when they leak through tool results.
func sanitizeANSI(content string) string {
	if !strings.ContainsRune(content, '\x1b') {
		return content
	}
	return ansiRegex.ReplaceAllString(content, "")
}
