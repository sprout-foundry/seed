// Package main implements the seed-cli conformance test binary.
//
// seed-cli is a thin CLI wrapping the core and events packages.
// It exposes every public method through a newline-delimited JSON-RPC
// protocol over stdin/stdout. The same test suite can be run against
// seed-go, seed-js, seed-swift, seed-rust, etc. to prove equivalence.
//
// Protocol:
//
//	Input  (stdin):  {"id":1,"method":"agent.new","params":{...}}
//	Output (stdout): {"id":1,"result":{...},"error":null}
//	Events (stdout): {"event":"query_started","data":{...}}
//
// When stdin closes, any running query is cancelled via context.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

// rpcRequest is a JSON-RPC request read from stdin.
type rpcRequest struct {
	ID     *int                   `json:"id"`
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params"`
}

// rpcResponse is a JSON-RPC response written to stdout.
type rpcResponse struct {
	ID     *int                   `json:"id,omitempty"`
	Result map[string]interface{} `json:"result,omitempty"`
	Error  *rpcError              `json:"error,omitempty"`
}

// rpcEvent is an async event forwarded to stdout from the event bus.
type rpcEvent struct {
	Event string                 `json:"event"`
	Data  map[string]interface{} `json:"data"`
}

func main() {
	s := newCliState()

	// Protect stdout so events and responses don't interleave.
	var stdoutMu sync.Mutex

	// Start event forwarding goroutine: reads from the event bus and writes
	// {"event":...} lines to stdout.
	eventChan := s.h.EventBus().Subscribe("__cli_event_forwarder__")
	go func() {
		for ev := range eventChan {
			resp := rpcEvent{
				Event: ev.Type,
				Data:  toMap(ev.Data),
			}
			line, _ := json.Marshal(resp)
			stdoutMu.Lock()
			fmt.Fprintln(os.Stdout, string(line))
			stdoutMu.Unlock()
		}
	}()

	// JSON-RPC dispatch loop: read lines from stdin, parse, dispatch, write.
	scanner := bufio.NewScanner(os.Stdin)
	// Increase buffer for large JSON lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			stdoutMu.Lock()
			writeResponse(os.Stdout, nil, &rpcError{
				Code:    -32700,
				Message: "parse error: " + err.Error(),
			})
			stdoutMu.Unlock()
			continue
		}

		if req.Method == "" {
			stdoutMu.Lock()
			writeResponse(os.Stdout, req.ID, &rpcError{
				Code:    -32600,
				Message: "missing method",
			})
			stdoutMu.Unlock()
			continue
		}

		if req.Params == nil {
			req.Params = make(map[string]interface{})
		}

		result, rpcErr := dispatch(s, req.Method, req.Params)

		stdoutMu.Lock()
		if rpcErr != nil {
			writeResponse(os.Stdout, req.ID, rpcErr)
		} else if result != nil {
			resp := rpcResponse{
				ID:     req.ID,
				Result: result,
			}
			data, _ := json.Marshal(resp)
			fmt.Fprintln(os.Stdout, string(data))
		}
		stdoutMu.Unlock()
	}

	// Stdin closed — cancel any running query.
	s.cancel()
}

// writeResponse writes a JSON-RPC error response to stdout.
func writeResponse(w *os.File, id *int, err *rpcError) {
	if err == nil {
		return
	}
	resp := rpcResponse{
		ID:    id,
		Error: err,
	}
	data, _ := json.Marshal(resp)
	fmt.Fprintln(w, string(data))
}

// toMap converts any value to a map[string]interface{} for JSON serialization.
// For map[string]interface{}, it returns as-is. For other types, it wraps in a
// "value" field so the event data is always a JSON object.
func toMap(data any) map[string]interface{} {
	if m, ok := data.(map[string]interface{}); ok {
		return m
	}
	if m, ok := data.(map[string]any); ok {
		out := make(map[string]interface{})
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	return map[string]interface{}{"value": data}
}
