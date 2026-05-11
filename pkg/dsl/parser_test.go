package dsl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse_ValidScript(t *testing.T) {
	input := `{
		"script": {
			"name": "test-script",
			"protocol": "http",
			"config": {
				"base_url": "https://example.com"
			},
			"actions": [
				{"name": "homepage", "type": "request", "method": "GET", "target": "/"}
			]
		}
	}`

	script, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if script.Name != "test-script" {
		t.Errorf("name = %q, want %q", script.Name, "test-script")
	}
	if script.Protocol != "http" {
		t.Errorf("protocol = %q, want %q", script.Protocol, "http")
	}
	if script.Config.BaseURL != "https://example.com" {
		t.Errorf("base_url = %q, want %q", script.Config.BaseURL, "https://example.com")
	}
	if len(script.Actions) != 1 {
		t.Fatalf("actions count = %d, want 1", len(script.Actions))
	}
	if script.Actions[0].Method != "GET" {
		t.Errorf("action method = %q, want %q", script.Actions[0].Method, "GET")
	}
}

func TestParse_MissingName(t *testing.T) {
	input := `{"script": {"protocol": "http", "actions": [{"name": "a", "type": "request", "target": "/"}]}}`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestParse_MissingProtocol(t *testing.T) {
	input := `{"script": {"name": "test", "actions": [{"name": "a", "type": "request", "target": "/"}]}}`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for missing protocol")
	}
}

func TestParse_NoActions(t *testing.T) {
	input := `{"script": {"name": "test", "protocol": "http", "actions": []}}`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for empty actions")
	}
}

func TestParse_ActionMissingFields(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"missing action name",
			`{"script": {"name": "test", "protocol": "http", "actions": [{"type": "request", "target": "/"}]}}`,
		},
		{
			"missing action type",
			`{"script": {"name": "test", "protocol": "http", "actions": [{"name": "a", "target": "/"}]}}`,
		},
		{
			"missing action target",
			`{"script": {"name": "test", "protocol": "http", "actions": [{"name": "a", "type": "request"}]}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.input))
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestParse_InvalidJSON(t *testing.T) {
	_, err := Parse([]byte(`{not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParse_FullConfig(t *testing.T) {
	input := `{
		"script": {
			"name": "full-config",
			"protocol": "http",
			"config": {
				"base_url": "https://api.example.com",
				"http": {
					"connect_timeout_ms": 5000,
					"request_timeout_ms": 30000,
					"max_conns_per_host": 50,
					"tls": {
						"min_version": "1.2",
						"insecure_skip_verify": true
					},
					"proxy": {
						"url": "http://proxy:8080",
						"username": "user",
						"password": "pass"
					},
					"auth": {
						"type": "bearer",
						"token": "my-token"
					}
				},
				"think_time": {
					"type": "fixed",
					"mean_ms": 1000
				},
				"pacing": {
					"type": "fixed_delay",
					"delay_ms": 500
				},
				"error_handling": {
					"continue_on_error": true,
					"max_retries": 3,
					"retry_backoff": "exponential"
				}
			},
			"actions": [
				{"name": "login", "type": "request", "method": "POST", "target": "/login", "body": "{\"user\":\"test\"}"}
			]
		}
	}`

	script, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if script.Config.HTTP.ConnectTimeoutMs != 5000 {
		t.Errorf("connect_timeout_ms = %d, want 5000", script.Config.HTTP.ConnectTimeoutMs)
	}
	if script.Config.HTTP.TLS.MinVersion != "1.2" {
		t.Errorf("tls min_version = %q, want %q", script.Config.HTTP.TLS.MinVersion, "1.2")
	}
	if !script.Config.HTTP.TLS.InsecureSkipVerify {
		t.Error("tls insecure_skip_verify = false, want true")
	}
	if script.Config.HTTP.Proxy.URL != "http://proxy:8080" {
		t.Errorf("proxy url = %q, want %q", script.Config.HTTP.Proxy.URL, "http://proxy:8080")
	}
	if script.Config.HTTP.Auth.Type != "bearer" {
		t.Errorf("auth type = %q, want %q", script.Config.HTTP.Auth.Type, "bearer")
	}
	if script.Config.HTTP.Auth.Token != "my-token" {
		t.Errorf("auth token = %q, want %q", script.Config.HTTP.Auth.Token, "my-token")
	}
	if !script.Config.ErrorHandling.ContinueOnError {
		t.Error("continue_on_error = false, want true")
	}
	if script.Config.ErrorHandling.MaxRetries != 3 {
		t.Errorf("max_retries = %d, want 3", script.Config.ErrorHandling.MaxRetries)
	}
}

func TestParse_WithExtractorsAndAssertions(t *testing.T) {
	input := `{
		"script": {
			"name": "extract-test",
			"protocol": "http",
			"actions": [{
				"name": "get-token",
				"type": "request",
				"method": "POST",
				"target": "/auth",
				"extractors": [
					{"name": "token", "source": "body", "type": "jsonpath", "expression": "$.access_token"}
				],
				"assertions": [
					{"type": "status_code", "expected": 200}
				]
			}]
		}
	}`

	script, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	action := script.Actions[0]
	if len(action.Extractors) != 1 {
		t.Fatalf("extractors count = %d, want 1", len(action.Extractors))
	}
	if action.Extractors[0].Name != "token" {
		t.Errorf("extractor name = %q, want %q", action.Extractors[0].Name, "token")
	}
	if len(action.Assertions) != 1 {
		t.Fatalf("assertions count = %d, want 1", len(action.Assertions))
	}
	if action.Assertions[0].Type != "status_code" {
		t.Errorf("assertion type = %q, want %q", action.Assertions[0].Type, "status_code")
	}
}

func TestParse_BrowserActionsAllowSelectorWaitAndScreenshot(t *testing.T) {
	input := `{
		"script": {
			"name": "browser-flow",
			"protocol": "browser",
			"config": {
				"base_url": "https://example.com"
			},
			"actions": [
				{"name": "open", "type": "browser_navigate", "target": "/login"},
				{"name": "fill", "type": "browser_fill", "selector": "#email", "value": "user@example.com"},
				{"name": "wait", "type": "browser_wait", "wait_for": {"type": "text", "value": "Welcome", "timeout_ms": 5000}},
				{"name": "shot", "type": "browser_screenshot", "capture": {"screenshot": true, "html": true}}
			]
		}
	}`

	script, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := script.Actions[1].Selector; got != "#email" {
		t.Fatalf("selector = %q, want #email", got)
	}
	if script.Actions[2].WaitFor == nil || script.Actions[2].WaitFor.TimeoutMs != 5000 {
		t.Fatalf("wait_for = %#v, want timeout", script.Actions[2].WaitFor)
	}
	if script.Actions[3].Capture == nil || !script.Actions[3].Capture.HTML {
		t.Fatalf("capture = %#v, want html capture", script.Actions[3].Capture)
	}
}

func TestParseFile(t *testing.T) {
	content := `{"script": {"name": "file-test", "protocol": "http", "actions": [{"name": "a", "type": "request", "target": "/"}]}}`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	script, err := ParseFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if script.Name != "file-test" {
		t.Errorf("name = %q, want %q", script.Name, "file-test")
	}
}

func TestParseFile_NotFound(t *testing.T) {
	_, err := ParseFile("/nonexistent/path.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestParse_InitAndTeardown(t *testing.T) {
	input := `{
		"script": {
			"name": "lifecycle-test",
			"protocol": "http",
			"init": [
				{"name": "setup", "type": "request", "target": "/setup"}
			],
			"actions": [
				{"name": "main", "type": "request", "target": "/main"}
			],
			"teardown": [
				{"name": "cleanup", "type": "request", "target": "/cleanup"}
			]
		}
	}`

	script, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(script.Init) != 1 {
		t.Errorf("init count = %d, want 1", len(script.Init))
	}
	if len(script.Teardown) != 1 {
		t.Errorf("teardown count = %d, want 1", len(script.Teardown))
	}
}
