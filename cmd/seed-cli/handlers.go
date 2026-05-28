package main

import (
	"github.com/sprout-foundry/seed/core"
)

// ---- Agent methods not in state.go ----

// runStream calls agent.RunStream with the given query.
func (s *cliState) runStream(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	query, ok := params["query"].(string)
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "missing required param: query"}
	}

	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()

	if agent == nil {
		return nil, &rpcError{Code: -32603, Message: "agent not created; call agent.new first"}
	}

	result, err := agent.RunStream(s.ctx, query)
	if err != nil {
		msg := err.Error()
		if msg == "context canceled" || msg == "context deadline exceeded" {
			return nil, &rpcError{Code: -32603, Message: "query cancelled"}
		}
		return nil, &rpcError{Code: -32603, Message: msg}
	}
	return map[string]interface{}{"result": result}, nil
}

// exportState serializes the current agent state to JSON.
func (s *cliState) exportState() (map[string]interface{}, *rpcError) {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()

	if agent == nil {
		return nil, &rpcError{Code: -32603, Message: "agent not created; call agent.new first"}
	}

	data, err := agent.ExportState()
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]interface{}{"state": string(data)}, nil
}

// importState deserializes state from JSON into the agent.
func (s *cliState) importState(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	raw, ok := params["state"].(string)
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "missing required param: state"}
	}

	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()

	if agent == nil {
		return nil, &rpcError{Code: -32603, Message: "agent not created; call agent.new first"}
	}

	if err := agent.ImportState([]byte(raw)); err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}
	return map[string]interface{}{"ok": true}, nil
}

// setSystemPrompt updates the system prompt for future queries.
func (s *cliState) setSystemPrompt(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	prompt, ok := params["prompt"].(string)
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "missing required param: prompt"}
	}

	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()

	if agent == nil {
		return nil, &rpcError{Code: -32603, Message: "agent not created; call agent.new first"}
	}

	agent.SetSystemPrompt(prompt)
	return map[string]interface{}{"ok": true}, nil
}

// steer queues a transient steering message.
func (s *cliState) steer(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	role, _ := params["role"].(string)
	content, _ := params["content"].(string)

	if role == "" {
		role = "system"
	}

	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()

	if agent == nil {
		return nil, &rpcError{Code: -32603, Message: "agent not created; call agent.new first"}
	}

	agent.Steer(core.Message{Role: role, Content: content})
	return map[string]interface{}{"ok": true}, nil
}

// steerSystem is a convenience for injecting a system-level steering message.
func (s *cliState) steerSystem(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	content, ok := params["content"].(string)
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "missing required param: content"}
	}

	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()

	if agent == nil {
		return nil, &rpcError{Code: -32603, Message: "agent not created; call agent.new first"}
	}

	agent.SteerSystem(content)
	return map[string]interface{}{"ok": true}, nil
}

// pause pauses the agent for user clarification.
func (s *cliState) pause() (map[string]interface{}, *rpcError) {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()

	if agent == nil {
		return nil, &rpcError{Code: -32603, Message: "agent not created; call agent.new first"}
	}

	agent.Pause()
	return map[string]interface{}{"ok": true}, nil
}

// resume resumes a paused agent.
func (s *cliState) resume() (map[string]interface{}, *rpcError) {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()

	if agent == nil {
		return nil, &rpcError{Code: -32603, Message: "agent not created; call agent.new first"}
	}

	agent.Resume()
	return map[string]interface{}{"ok": true}, nil
}

// isPaused returns whether the agent is paused.
func (s *cliState) isPaused() (map[string]interface{}, *rpcError) {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()

	if agent == nil {
		return nil, &rpcError{Code: -32603, Message: "agent not created; call agent.new first"}
	}

	return map[string]interface{}{"paused": agent.IsPaused()}, nil
}

// checkpoints returns a copy of all recorded turn checkpoints.
func (s *cliState) checkpoints() (map[string]interface{}, *rpcError) {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()

	if agent == nil {
		return nil, &rpcError{Code: -32603, Message: "agent not created; call agent.new first"}
	}

	return map[string]interface{}{"checkpoints": agent.Checkpoints()}, nil
}

// agentState returns the message count and session ID from the agent's state.
func (s *cliState) agentState() (map[string]interface{}, *rpcError) {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()

	if agent == nil {
		return nil, &rpcError{Code: -32603, Message: "agent not created; call agent.new first"}
	}

	st := agent.State()
	return map[string]interface{}{
		"messageCount": st.Len(),
		"sessionID":    st.SessionID(),
		"totalTokens":  st.TotalTokens(),
		"totalCost":    st.TotalCost(),
	}, nil
}

// ---- Mock provider methods not in state.go ----

// addMalformedResponse queues a response with tool calls embedded in content.
func (s *cliState) addMalformedResponse(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	content, ok := params["content"].(string)
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "missing required param: content"}
	}

	s.h.Provider().AddMalformedResponse(content)
	return map[string]interface{}{"ok": true}, nil
}

// addTextResponseWithFinish queues a text response with a custom finish reason.
func (s *cliState) addTextResponseWithFinish(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	content, ok := params["content"].(string)
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "missing required param: content"}
	}

	finishReason, _ := params["finishReason"].(string)
	if finishReason == "" {
		finishReason = "stop"
	}

	s.h.Provider().AddTextResponseWithFinish(content, finishReason)
	return map[string]interface{}{"ok": true}, nil
}

// withInfo sets the provider info.
func (s *cliState) withInfo(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	info := core.ProviderInfo{
		Model:       getStringVal(params, "model"),
		ContextSize: getIntVal(params, "contextSize"),
		HasVision:   getBoolVal(params, "hasVision"),
	}
	if info.Model == "" {
		info.Model = "mock-model"
	}
	if info.ContextSize == 0 {
		info.ContextSize = 128000
	}

	s.h.Provider().WithInfo(info)
	return map[string]interface{}{"ok": true}, nil
}

// withTokenEstimate sets the token estimate.
func (s *cliState) withTokenEstimate(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	count, ok := params["count"].(float64)
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "missing required param: count"}
	}

	s.h.Provider().WithTokenEstimate(int(count))
	return map[string]interface{}{"ok": true}, nil
}

// withStreaming enables streaming mode on the mock provider.
func (s *cliState) withStreaming() (map[string]interface{}, *rpcError) {
	s.h.Provider().WithStreaming()
	return map[string]interface{}{"ok": true}, nil
}

// addStreamChunks configures explicit chunk sequences for streaming.
func (s *cliState) addStreamChunks(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	raw, ok := params["chunks"].([]interface{})
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "missing required param: chunks"}
	}

	var chunks []string
	for _, c := range raw {
		if s, ok := c.(string); ok {
			chunks = append(chunks, s)
		}
	}

	s.h.Provider().AddStreamChunks(chunks...)
	return map[string]interface{}{"ok": true}, nil
}

// blockOnCallN sets the Nth call to block until unblock is called.
func (s *cliState) blockOnCallN(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	n, ok := params["call"].(float64)
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "missing required param: call"}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.h.Provider().BlockOnCallN(int(n))
	s.blockCh = ch
	return map[string]interface{}{"ok": true}, nil
}

// unblock closes the block channel to unblock a pending provider call.
func (s *cliState) unblock() (map[string]interface{}, *rpcError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blockCh != nil {
		close(s.blockCh)
		s.blockCh = nil
	}
	return map[string]interface{}{"ok": true}, nil
}

// executorCallCount returns the number of executor Execute() calls.
func (s *cliState) executorCallCount() (map[string]interface{}, *rpcError) {
	return map[string]interface{}{"count": s.h.Executor().CallCount()}, nil
}

// executorLastCalls returns the tool calls from the last Execute() call.
func (s *cliState) executorLastCalls() (map[string]interface{}, *rpcError) {
	calls := s.h.Executor().LastCalls()
	if len(calls) == 0 {
		return map[string]interface{}{"calls": []map[string]interface{}{}}, nil
	}

	out := make([]map[string]interface{}, len(calls))
	for i, c := range calls {
		out[i] = map[string]interface{}{
			"id":   c.ID,
			"type": c.Type,
			"name": c.Function.Name,
			"args": c.Function.Arguments,
		}
	}
	return map[string]interface{}{"calls": out}, nil
}
