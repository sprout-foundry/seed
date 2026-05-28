// Package runner implements the conformance test runner for the seed project.
//
// The runner reads JSON test spec files, spawns the seed-cli binary as a
// subprocess, sends JSON-RPC actions, collects responses and events,
// evaluates assertions, and reports results in TAP format.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Spec represents a conformance test specification.
type Spec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Setup       *SpecSetup      `json:"setup,omitempty"`
	Actions     []SpecAction    `json:"actions"`
	Assertions  []SpecAssertion `json:"assertions"`
}

// SpecSetup contains optional legacy setup configuration.
type SpecSetup struct {
	Options           map[string]interface{} `json:"options,omitempty"`
	ProviderResponses []SpecProviderResponse `json:"providerResponses,omitempty"`
}

// SpecProviderResponse describes a mocked provider response.
type SpecProviderResponse struct {
	Type      string         `json:"type"`
	Content   string         `json:"content,omitempty"`
	ToolCalls []SpecToolCall `json:"toolCalls,omitempty"`
	Error     string         `json:"error,omitempty"`
	Finish    string         `json:"finish,omitempty"`
}

// SpecToolCall describes a tool call in a mock response.
type SpecToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// SpecAction is a single action to send to the CLI.
type SpecAction struct {
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params,omitempty"`
	ID     int                    `json:"id"`
	// Wait allows the runner to wait for a specific response ID before
	// sending the next action.
	Wait *int `json:"wait,omitempty"`
}

// SpecAssertion describes a condition that must hold after actions are sent.
type SpecAssertion struct {
	Type        string                 `json:"type"`
	ID          int                    `json:"id,omitempty"`
	Result      map[string]interface{} `json:"result,omitempty"`
	Error       *SpecAssertionError    `json:"error,omitempty"`
	Event       string                 `json:"eventType,omitempty"`
	Contains    string                 `json:"contains,omitempty"`
	Count       int                    `json:"count,omitempty"`
	Path        string                 `json:"path,omitempty"`
	Equals      interface{}            `json:"equals,omitempty"`
	NotEmpty    bool                   `json:"notEmpty,omitempty"`
	GreaterThan interface{}            `json:"greaterThan,omitempty"`
	ErrorType   string                 `json:"errorType,omitempty"`
}

// SpecAssertionError is used in response assertions.
type SpecAssertionError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// CollectedResponse is a response line from the CLI (has an "id" field).
type CollectedResponse struct {
	ID     *int                    `json:"id,omitempty"`
	Result map[string]interface{}  `json:"result,omitempty"`
	Error  *map[string]interface{} `json:"error,omitempty"`
}

// CollectedEvent is an event line from the CLI (has an "event" field).
type CollectedEvent struct {
	Event string                 `json:"event"`
	Data  map[string]interface{} `json:"data"`
}

// RunSpecResult holds the results of running a single spec.
type RunSpecResult struct {
	Spec       Spec
	Response   map[int]*CollectedResponse
	Events     []CollectedEvent
	Assertions []AssertionResult
	Failed     bool
}

// AssertionResult holds the result of a single assertion.
type AssertionResult struct {
	Assertion SpecAssertion
	Passed    bool
	Message   string
}

// RunSpec spawns the CLI, executes the spec's actions, collects responses
// and events, then evaluates assertions. Returns the result for reporting.
func RunSpec(spec Spec, cliPath string, verbose bool) RunSpecResult {
	result := RunSpecResult{
		Spec:     spec,
		Response: make(map[int]*CollectedResponse),
		Events:   make([]CollectedEvent, 0),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, cliPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		result.Assertions = []AssertionResult{{
			Passed:  false,
			Message: "failed to create stdin pipe: " + err.Error(),
		}}
		result.Failed = true
		return result
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		result.Assertions = []AssertionResult{{
			Passed:  false,
			Message: "failed to create stdout pipe: " + err.Error(),
		}}
		result.Failed = true
		return result
	}

	if err := cmd.Start(); err != nil {
		result.Assertions = []AssertionResult{{
			Passed:  false,
			Message: "failed to start CLI: " + err.Error(),
		}}
		result.Failed = true
		return result
	}

	// Read stdout in a goroutine until EOF.
	stdoutDone := make(chan struct{})
	scanner := bufio.NewScanner(stdout)
	// Increase buffer for large JSON lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	go func() {
		defer close(stdoutDone)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			if verbose {
				fmt.Fprintf(os.Stderr, "#   [stdout] %s\n", line)
			}

			// Determine if this is a response (has "id") or an event (has "event").
			var raw map[string]interface{}
			if err := json.Unmarshal([]byte(line), &raw); err != nil {
				continue
			}

			if idVal, ok := raw["id"]; ok {
				// It's a response — extract the id
				var idF float64
				if idFloat, ok := idVal.(float64); ok {
					idF = idFloat
				} else {
					continue
				}
				id := int(idF)

				resp := &CollectedResponse{}
				if v, ok := raw["result"]; ok {
					if m, ok := v.(map[string]interface{}); ok {
						resp.Result = m
					}
				}
				if v, ok := raw["error"]; ok {
					if m, ok := v.(map[string]interface{}); ok {
						resp.Error = &m
					}
				}

				result.Response[id] = resp
			} else if eventVal, ok := raw["event"]; ok {
				if eventStr, ok := eventVal.(string); ok {
					var data map[string]interface{}
					if d, ok := raw["data"]; ok {
						if m, ok := d.(map[string]interface{}); ok {
							data = m
						}
					}
					result.Events = append(result.Events, CollectedEvent{
						Event: eventStr,
						Data:  data,
					})
				}
			}
		}
	}()

	// Send actions to stdin.
	for _, action := range spec.Actions {
		if action.Params == nil {
			action.Params = make(map[string]interface{})
		}

		req := map[string]interface{}{
			"id":     action.ID,
			"method": action.Method,
			"params": action.Params,
		}

		line, err := json.Marshal(req)
		if err != nil {
			result.Assertions = []AssertionResult{{
				Passed:  false,
				Message: "failed to marshal action: " + err.Error(),
			}}
			result.Failed = true
			stdin.Close()
			<-stdoutDone
			return result
		}

		if verbose {
			fmt.Fprintf(os.Stderr, "#   [stdin]  %s\n", line)
		}

		if _, err := fmt.Fprintln(stdin, string(line)); err != nil {
			result.Assertions = []AssertionResult{{
				Passed:  false,
				Message: "failed to send action: " + err.Error(),
			}}
			result.Failed = true
			stdin.Close()
			<-stdoutDone
			return result
		}

		// Small delay to let CLI process the action before next one.
		// This is a safety valve for fast processors.
		time.Sleep(10 * time.Millisecond)
	}

	// Close stdin to signal EOF to the CLI (cancels any running query).
	stdin.Close()

	// Wait for stdout reading to finish.
	select {
	case <-stdoutDone:
	case <-time.After(10 * time.Second):
		// Force cancel if stdout hangs.
		cancel()
		<-stdoutDone
	}

	// Wait for the CLI process to exit.
	cmd.Wait()

	// Evaluate assertions.
	result.Assertions = EvaluateAssertions(spec, result.Response, result.Events)
	for _, ar := range result.Assertions {
		if !ar.Passed {
			result.Failed = true
			break
		}
	}

	return result
}

// parseLine determines if a JSON line is a response or event and returns
// the parsed data accordingly.
func parseLine(line string) (isResponse bool, isEvent bool, data map[string]interface{}) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return false, false, nil
	}

	if _, ok := raw["id"]; ok {
		return true, false, raw
	}
	if _, ok := raw["event"]; ok {
		return false, true, raw
	}
	return false, false, raw
}

// writeJSONLine writes a JSON object as a single line to the given writer.
func writeJSONLine(w io.Writer, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}
