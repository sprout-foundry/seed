package main

// rpcError represents a JSON-RPC error for the CLI protocol.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// dispatch handles a JSON-RPC method and returns the result or error to serialize.
func dispatch(state *cliState, method string, params map[string]interface{}) (map[string]interface{}, *rpcError) {
	switch method {
	case "agent.new":
		return state.newAgent(params)
	case "agent.run":
		return state.run(params)
	case "agent.runStream":
		return state.runStream(params)
	case "agent.interrupt":
		return state.interrupt()
	case "agent.resetInterrupt":
		return state.resetInterrupt()
	case "agent.exportState":
		return state.exportState()
	case "agent.importState":
		return state.importState(params)
	case "agent.setSystemPrompt":
		return state.setSystemPrompt(params)
	case "agent.steer":
		return state.steer(params)
	case "agent.steerSystem":
		return state.steerSystem(params)
	case "agent.pause":
		return state.pause()
	case "agent.resume":
		return state.resume()
	case "agent.isPaused":
		return state.isPaused()
	case "agent.checkpoints":
		return state.checkpoints()
	case "agent.state":
		return state.agentState()
	case "mock.addTextResponse":
		return state.addTextResponse(params)
	case "mock.addToolCallResponse":
		return state.addToolCallResponse(params)
	case "mock.addError":
		return state.addError(params)
	case "mock.addMalformedResponse":
		return state.addMalformedResponse(params)
	case "mock.addTextResponseWithFinish":
		return state.addTextResponseWithFinish(params)
	case "mock.addTool":
		return state.addTool(params)
	case "mock.addToolResult":
		return state.addToolResult(params)
	case "mock.reset":
		return state.reset()
	case "mock.callCount":
		return state.callCount()
	case "mock.lastRequest":
		return state.lastRequest()
	case "mock.withInfo":
		return state.withInfo(params)
	case "mock.withTokenEstimate":
		return state.withTokenEstimate(params)
	case "mock.withStreaming":
		return state.withStreaming()
	case "mock.addStreamChunks":
		return state.addStreamChunks(params)
	case "mock.blockOnCallN":
		return state.blockOnCallN(params)
	case "mock.unblock":
		return state.unblock()
	case "mock.executorCallCount":
		return state.executorCallCount()
	case "mock.executorLastCalls":
		return state.executorLastCalls()
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
	}
}
