package core

import (
	"time"

	"github.com/sprout-foundry/seed/events"
)

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
