package main

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/sprout-foundry/seed/core"
	"github.com/sprout-foundry/seed/internal/test"
)

// cliState holds all shared state for the CLI session.
type cliState struct {
	mu       sync.Mutex
	h        *test.Harness
	agent    *core.Agent
	ctx      context.Context
	cancel   context.CancelFunc
	metadata map[string]string

	// blockCh is closed by mock.release to unblock a pending provider call.
	blockCh chan struct{}
}

// newCliState creates a fresh CLI state with mocks and context.
func newCliState() *cliState {
	ctx, cancel := context.WithCancel(context.Background())
	return &cliState{
		h:      test.NewHarness(),
		ctx:    ctx,
		cancel: cancel,
	}
}

// newAgent creates a new agent using the harness mocks and configured options.
func (s *cliState) newAgent(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	opts := core.Options{
		Provider:       s.h.Provider(),
		Executor:       s.h.Executor(),
		UI:             core.NoopUI,
		EventPublisher: s.h.EventBus(),
	}

	if v, ok := params["systemPrompt"]; ok {
		opts.SystemPrompt = v.(string)
	}
	if v, ok := params["maxIterations"]; ok {
		switch v2 := v.(type) {
		case float64:
			opts.MaxIterations = int(v2)
		case int:
			opts.MaxIterations = v2
		}
	}
	if v, ok := params["maxTokens"]; ok {
		switch v2 := v.(type) {
		case float64:
			opts.MaxTokens = int(v2)
		case int:
			opts.MaxTokens = v2
		}
	}
	if _, ok := params["debug"]; ok {
		// Accept the parameter but ignore it. Debug output (fmt.Printf from
		// the response validator) would corrupt the JSON-RPC protocol on stdout.
		// The CLI is headless — debug output is not applicable.
	}
	if v, ok := params["compactionTriggerFraction"]; ok {
		switch v2 := v.(type) {
		case float64:
			opts.CompactionTriggerFraction = v2
		}
	}
	if v, ok := params["disableFallbackParser"]; ok {
		opts.DisableFallbackParser = v.(bool)
	}
	if v, ok := params["disableValidator"]; ok {
		opts.DisableValidator = v.(bool)
	}
	if v, ok := params["disableNormalizer"]; ok {
		opts.DisableNormalizer = v.(bool)
	}

	// SP-016-1k: Additional Options fields
	if v, ok := params["initialMessages"]; ok {
		if arr, ok := v.([]interface{}); ok {
			opts.InitialMessages = parseMessages(arr)
		}
	}
	if v, ok := params["initialCheckpoints"]; ok {
		if arr, ok := v.([]interface{}); ok {
			opts.InitialCheckpoints = parseCheckpoints(arr)
		}
	}
	if v, ok := params["retryConfig"]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			opts.RetryConfig = parseRetryConfig(m)
		}
	}
	if v, ok := params["optimizer"]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			opts.Optimizer = parseOptimizer(m)
		}
	}

	agent, err := core.NewAgent(opts)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: err.Error()}
	}

	s.mu.Lock()
	s.agent = agent
	s.mu.Unlock()

	return map[string]interface{}{"ok": true}, nil
}

// run calls agent.Run with the given query.
func (s *cliState) run(params map[string]interface{}) (map[string]interface{}, *rpcError) {
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

	result, err := agent.Run(s.ctx, query)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "context canceled") {
			return nil, &rpcError{Code: -32603, Message: "query cancelled"}
		}
		return nil, &rpcError{Code: -32603, Message: msg}
	}
	return map[string]interface{}{"result": result}, nil
}

// interrupt calls agent.Interrupt().
func (s *cliState) interrupt() (map[string]interface{}, *rpcError) {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()

	if agent == nil {
		return nil, &rpcError{Code: -32603, Message: "agent not created; call agent.new first"}
	}

	agent.Interrupt()
	return map[string]interface{}{"ok": true}, nil
}

// resetInterrupt calls agent.ResetInterrupt().
func (s *cliState) resetInterrupt() (map[string]interface{}, *rpcError) {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()

	if agent == nil {
		return nil, &rpcError{Code: -32603, Message: "agent not created; call agent.new first"}
	}

	ctx := agent.ResetInterrupt()
	return map[string]interface{}{"ok": true, "context": ctx != nil}, nil
}

// addTextResponse queues a text response on the mock provider.
func (s *cliState) addTextResponse(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	content, ok := params["content"].(string)
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "missing required param: content"}
	}

	s.h.Provider().AddTextResponse(content)
	return map[string]interface{}{"ok": true}, nil
}

// addToolCallResponse queues a tool call response on the mock provider.
func (s *cliState) addToolCallResponse(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	content, _ := params["content"].(string)

	var toolCalls []core.ToolCall
	if raw, ok := params["toolCalls"]; ok {
		if arr, ok := raw.([]interface{}); ok {
			for _, tc := range arr {
				if obj, ok := tc.(map[string]interface{}); ok {
					call := core.ToolCall{
						ID:   getStringVal(obj, "id"),
						Type: getStringVal(obj, "type"),
					}
					if fn, ok := obj["function"].(map[string]interface{}); ok {
						call.Function = core.ToolCallFunction{
							Name:      getStringVal(fn, "name"),
							Arguments: getStringVal(fn, "arguments"),
						}
					}
					toolCalls = append(toolCalls, call)
				}
			}
		}
	}

	s.h.Provider().AddToolCallResponse(content, toolCalls...)
	return map[string]interface{}{"ok": true}, nil
}

// addError queues an error on the mock provider.
func (s *cliState) addError(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	msg, ok := params["message"].(string)
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "missing required param: message"}
	}

	s.h.Provider().AddError(errors.New(msg))
	return map[string]interface{}{"ok": true}, nil
}

// addTool adds a tool to the mock executor.
func (s *cliState) addTool(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	name, ok := params["name"].(string)
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "missing required param: name"}
	}

	tool := core.Tool{
		Type: "function",
		Function: core.ToolFunction{
			Name:        name,
			Description: getStringVal(params, "description"),
		},
	}

	if p, ok := params["parameters"].(map[string]interface{}); ok {
		tool.Function.Parameters = p
	}

	s.h.Executor().AddTool(tool)
	return map[string]interface{}{"ok": true}, nil
}

// addToolResult queues a tool result on the mock executor.
func (s *cliState) addToolResult(params map[string]interface{}) (map[string]interface{}, *rpcError) {
	callID, ok := params["toolCallId"].(string)
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "missing required param: toolCallId"}
	}

	content, _ := params["content"].(string)
	s.h.Executor().AddToolResult(callID, content)
	return map[string]interface{}{"ok": true}, nil
}

// reset clears all mock state and the agent.
func (s *cliState) reset() (map[string]interface{}, *rpcError) {
	s.h.Reset()
	s.mu.Lock()
	s.agent = nil
	s.mu.Unlock()
	return map[string]interface{}{"ok": true}, nil
}

// callCount returns the provider call count.
func (s *cliState) callCount() (map[string]interface{}, *rpcError) {
	return map[string]interface{}{"count": s.h.Provider().CallCount()}, nil
}

// lastRequest returns the last provider request.
func (s *cliState) lastRequest() (map[string]interface{}, *rpcError) {
	req := s.h.Provider().LastRequest()
	if req == nil {
		return map[string]interface{}{"request": nil}, nil
	}
	return mapToMap(req), nil
}

// ---- helper functions for serialization ----

// mapToMap converts a ChatRequest to a serializable map.
func mapToMap(req *core.ChatRequest) map[string]interface{} {
	result := map[string]interface{}{
		"model":     req.Model,
		"messages":  msgSlice(req.Messages),
		"maxTokens": req.MaxTokens,
		"stream":    req.Stream,
	}
	if len(req.Tools) > 0 {
		result["tools"] = toolSlice(req.Tools)
	}
	if req.ToolChoice != "" {
		result["toolChoice"] = req.ToolChoice
	}
	return result
}

func msgSlice(msgs []core.Message) []map[string]interface{} {
	out := make([]map[string]interface{}, len(msgs))
	for i, m := range msgs {
		out[i] = map[string]interface{}{
			"role":    m.Role,
			"content": m.Content,
		}
		if m.ReasoningContent != "" {
			out[i]["reasoningContent"] = m.ReasoningContent
		}
		if m.ToolCallID != "" {
			out[i]["toolCallId"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			out[i]["toolCalls"] = tcSlice(m.ToolCalls)
		}
	}
	return out
}

func tcSlice(calls []core.ToolCall) []map[string]interface{} {
	out := make([]map[string]interface{}, len(calls))
	for i, c := range calls {
		out[i] = map[string]interface{}{
			"id":   c.ID,
			"type": c.Type,
			"function": map[string]interface{}{
				"name":      c.Function.Name,
				"arguments": c.Function.Arguments,
			},
		}
	}
	return out
}

func toolSlice(tools []core.Tool) []map[string]interface{} {
	out := make([]map[string]interface{}, len(tools))
	for i, t := range tools {
		out[i] = map[string]interface{}{
			"type": t.Type,
			"function": map[string]interface{}{
				"name":        t.Function.Name,
				"description": t.Function.Description,
				"parameters":  t.Function.Parameters,
			},
		}
	}
	return out
}
