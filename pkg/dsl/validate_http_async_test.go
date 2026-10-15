package dsl

import (
	"strings"
	"testing"
)

func asyncTestScript(protocol string, actions ...Action) *Script {
	return &Script{Name: "async", Protocol: protocol, Config: ScriptConfig{BaseURL: "https://example.test"}, Actions: actions}
}

func validAsyncStart() Action {
	return Action{Name: "start", Type: "http_async_start", Params: map[string]any{
		"id": "poll", "pattern": "poll", "interval_ms": 500,
		"request": map[string]any{"name": "poll", "type": "GET", "method": "GET", "target": "/poll"},
	}, Control: &ControlFlow{Branches: []ParallelBranch{
		{Name: "on_response", Steps: []Action{{Name: "stop", Type: "http_async_stop", Params: map[string]any{"id": "poll"}}}},
	}}}
}

func TestValidateHTTPAsyncActions(t *testing.T) {
	ok := asyncTestScript("http", validAsyncStart(),
		Action{Name: "sync", Type: "http_sync", Params: map[string]any{"variable": "status", "value": "done", "timeout_ms": 1000}},
		Action{Name: "stop", Type: "http_async_stop", Params: map[string]any{"id": "poll"}},
	)
	if issues := ValidateScriptIssues(ok); len(issues) != 0 {
		t.Fatalf("valid async script: %v", issues)
	}
	background := validAsyncStart()
	background.Params = map[string]any{"id": "log_1", "pattern": "once", "request": map[string]any{"name": "log", "type": "POST", "method": "POST", "target": "/log"}}
	background.Control = nil
	if issues := ValidateScriptIssues(asyncTestScript("http", background)); len(issues) != 0 {
		t.Fatalf("background request (pattern once): %v", issues)
	}
	cases := map[string]struct {
		script *Script
		want   string
	}{
		"not http": {asyncTestScript("websocket", validAsyncStart()), "requires the http protocol"},
		"no request": {asyncTestScript("http", func() Action {
			a := validAsyncStart()
			delete(a.Params, "request")
			return a
		}()), "requires params.request"},
		"request without target": {asyncTestScript("http", func() Action {
			a := validAsyncStart()
			a.Params["request"] = map[string]any{"name": "poll", "type": "GET", "method": "GET"}
			return a
		}()), "missing target"},
		"bad pattern": {asyncTestScript("http", func() Action {
			a := validAsyncStart()
			a.Params["pattern"] = "stream"
			return a
		}()), "poll, long_poll, push or once"},
		"bad branch": {asyncTestScript("http", func() Action {
			a := validAsyncStart()
			a.Control.Branches[0].Name = "on_error"
			return a
		}()), "is not on_request, on_response or on_body_chunk"},
		"steps instead of branches": {asyncTestScript("http", func() Action {
			a := validAsyncStart()
			a.Control = &ControlFlow{Steps: []Action{{Name: "x", Type: "GET", Method: "GET", Target: "/x"}}}
			return a
		}()), "accepts only branches"},
		"sync in a callback": {asyncTestScript("http", func() Action {
			a := validAsyncStart()
			a.Control.Branches[0].Steps = []Action{{Name: "sync", Type: "http_sync", Params: map[string]any{"variable": "x"}}}
			return a
		}()), "not permitted in an async conversation callback"},
		"stop without id":       {asyncTestScript("http", Action{Name: "stop", Type: "http_async_stop"}), "requires params.id"},
		"sync without variable": {asyncTestScript("http", Action{Name: "sync", Type: "http_sync"}), "requires params.variable"},
		"negative timeout":      {asyncTestScript("http", Action{Name: "sync", Type: "http_sync", Params: map[string]any{"variable": "x", "timeout_ms": -1}}), "zero or greater"},
		"inside control_parallel": {asyncTestScript("http", Action{Name: "par", Type: "control_parallel", Control: &ControlFlow{Branches: []ParallelBranch{
			{Name: "a", Steps: []Action{validAsyncStart()}},
			{Name: "b", Steps: []Action{{Name: "x", Type: "GET", Method: "GET", Target: "/x"}}},
		}}}), "not permitted inside control_parallel"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			issues := ValidateScriptIssues(tc.script)
			if !strings.Contains(strings.Join(issues, "\n"), tc.want) {
				t.Fatalf("issues = %v, want one containing %q", issues, tc.want)
			}
		})
	}
}
