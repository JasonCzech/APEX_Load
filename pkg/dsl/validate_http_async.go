package dsl

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Limits shared with the engine's background HTTP conversations.
const (
	MaxHTTPAsyncCallbackSteps = 64
	maxHTTPAsyncIDBytes       = 128
)

// validateHTTPAsyncIssues validates http_async_start, http_async_stop and
// http_sync (background HTTP conversations, LoadRunner
// web_reg_async_attributes / web_stop_async / web_sync).
func validateHTTPAsyncIssues(protocol string, path string, action Action) []string {
	label := actionLabel(path, action)
	if protocol != "http" {
		return []string{fmt.Sprintf("%s: %s requires the http protocol", label, action.Type)}
	}
	text := func(key string) string {
		value, _ := action.Params[key].(string)
		return strings.TrimSpace(value)
	}
	millis := func(key string) []string {
		raw, present := action.Params[key]
		if !present {
			return nil
		}
		var value float64
		switch typed := raw.(type) {
		case float64:
			value = typed
		case int:
			value = float64(typed)
		case int64:
			value = float64(typed)
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(typed))
			if err != nil {
				return []string{fmt.Sprintf("%s: params.%s must be a number of milliseconds", label, key)}
			}
			value = float64(parsed)
		default:
			return []string{fmt.Sprintf("%s: params.%s must be a number of milliseconds", label, key)}
		}
		if value < 0 {
			return []string{fmt.Sprintf("%s: params.%s must be zero or greater", label, key)}
		}
		return nil
	}
	var issues []string
	switch action.Type {
	case "http_async_stop":
		if text("id") == "" {
			issues = append(issues, fmt.Sprintf("%s: http_async_stop requires params.id", label))
		}
	case "http_sync":
		if text("variable") == "" {
			issues = append(issues, fmt.Sprintf("%s: http_sync requires params.variable", label))
		}
		issues = append(issues, millis("timeout_ms")...)
	case "http_async_start":
		id := text("id")
		if id == "" {
			issues = append(issues, fmt.Sprintf("%s: http_async_start requires params.id", label))
		} else if len(id) > maxHTTPAsyncIDBytes {
			issues = append(issues, fmt.Sprintf("%s: params.id must not exceed %d bytes", label, maxHTTPAsyncIDBytes))
		}
		switch strings.ToLower(text("pattern")) {
		case "", "poll", "long_poll", "push", "once":
		default:
			issues = append(issues, fmt.Sprintf("%s: params.pattern must be poll, long_poll, push or once", label))
		}
		issues = append(issues, millis("interval_ms")...)
		issues = append(issues, validateHTTPAsyncRequest(protocol, path, label, action.Params["request"])...)
		issues = append(issues, validateHTTPAsyncCallbacks(protocol, path, label, action.Control)...)
	}
	return issues
}

// HTTPAsyncCallbackBranches are the callback branch names of
// http_async_start: on_request runs before the request is resolved again,
// on_response after each response, on_body_chunk for each streamed chunk.
var HTTPAsyncCallbackBranches = map[string]bool{"on_request": true, "on_response": true, "on_body_chunk": true}

// validateHTTPAsyncCallbacks checks http_async_start's control block, which
// holds only named callback branches.
func validateHTTPAsyncCallbacks(protocol, path, label string, control *ControlFlow) []string {
	if control == nil {
		return nil
	}
	var issues []string
	if control.Condition != nil || len(control.Steps) > 0 || len(control.Else) > 0 || len(control.Cases) > 0 || len(control.Default) > 0 || control.Count != 0 || control.MaxIterations != 0 || control.ForEach != nil || control.DoWhile || control.Concurrency != 0 || control.FailFast {
		issues = append(issues, fmt.Sprintf("%s: http_async_start control accepts only branches named on_request, on_response or on_body_chunk", label))
	}
	seen := map[string]bool{}
	for branchIndex, branch := range control.Branches {
		branchLabel := fmt.Sprintf("%s.control.branches[%d]", path, branchIndex)
		name := strings.TrimSpace(branch.Name)
		switch {
		case !HTTPAsyncCallbackBranches[name]:
			issues = append(issues, fmt.Sprintf("%s: callback branch %q is not on_request, on_response or on_body_chunk", branchLabel, branch.Name))
		case seen[name]:
			issues = append(issues, fmt.Sprintf("%s: callback branch %q is duplicated", branchLabel, name))
		}
		seen[name] = true
		if len(branch.Steps) == 0 || len(branch.Steps) > MaxHTTPAsyncCallbackSteps {
			issues = append(issues, fmt.Sprintf("%s: a callback branch needs 1..%d steps", branchLabel, MaxHTTPAsyncCallbackSteps))
		}
		for j, nested := range branch.Steps {
			nestedPath := fmt.Sprintf("%s.steps[%d]", branchLabel, j)
			switch CanonicalActionType(nested.Type) {
			case "http_async_start", "http_sync", "control_parallel", "rendezvous":
				issues = append(issues, fmt.Sprintf("%s: %s is not permitted in an async conversation callback", nestedPath, nested.Type))
				continue
			}
			issues = append(issues, validateActionForProtocolAtPath(protocol, nestedPath, j, nested)...)
		}
	}
	return issues
}

func validateHTTPAsyncRequest(protocol, path, label string, raw any) []string {
	if raw == nil {
		return []string{fmt.Sprintf("%s: http_async_start requires params.request", label)}
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return []string{fmt.Sprintf("%s: params.request must be an HTTP request action", label)}
	}
	var request Action
	if err := json.Unmarshal(payload, &request); err != nil {
		return []string{fmt.Sprintf("%s: params.request must be an HTTP request action", label)}
	}
	if request.Name == "" {
		request.Name = "async request"
	}
	if isEngineActionType(request.Type) || strings.EqualFold(strings.TrimSpace(request.Type), "http_sequence") {
		return []string{fmt.Sprintf("%s: params.request must be a single HTTP request, not %s", label, request.Type)}
	}
	return validateActionForProtocolAtPath(protocol, path+".params.request", 0, request)
}
