package dsl

import (
	"os"
	"path/filepath"
	"strings"
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

func TestParse_ValidPlainScript(t *testing.T) {
	input := `{
		"name": "plain-script",
		"protocol": "http",
		"config": {"base_url": "https://example.com"},
		"actions": [
			{"name": "homepage", "type": "request", "method": "GET", "target": "/"}
		]
	}`

	script, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if script.Name != "plain-script" || script.Protocol != "http" {
		t.Fatalf("script = %#v", script)
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

func TestParse_GoalConfig(t *testing.T) {
	input := `{
		"script": {
			"name": "goal-config",
			"protocol": "http",
			"goal_config": {
				"p95_latency_ms": 800,
				"max_error_pct": 2.5,
				"max_error_count": 4,
				"stop_on_max_errors": true
			},
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
	if script.GoalConfig == nil {
		t.Fatal("GoalConfig = nil, want parsed goal config")
	}
	if script.GoalConfig.P95LatencyMs == nil || *script.GoalConfig.P95LatencyMs != 800 {
		t.Fatalf("P95LatencyMs = %#v, want 800", script.GoalConfig.P95LatencyMs)
	}
	if script.GoalConfig.MaxErrorPct == nil || *script.GoalConfig.MaxErrorPct != 2.5 {
		t.Fatalf("MaxErrorPct = %#v, want 2.5", script.GoalConfig.MaxErrorPct)
	}
	if script.GoalConfig.MaxErrorCount == nil || *script.GoalConfig.MaxErrorCount != 4 {
		t.Fatalf("MaxErrorCount = %#v, want 4", script.GoalConfig.MaxErrorCount)
	}
	if !script.GoalConfig.StopOnMaxErrors {
		t.Fatal("StopOnMaxErrors = false, want true")
	}
}

func TestParse_InvalidGoalConfig(t *testing.T) {
	input := `{
		"script": {
			"name": "goal-config",
			"protocol": "http",
			"goal_config": {
				"max_error_pct": 120
			},
			"config": {
				"base_url": "https://example.com"
			},
			"actions": [
				{"name": "homepage", "type": "request", "method": "GET", "target": "/"}
			]
		}
	}`

	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected validation error for invalid goal_config")
	}
}

func TestParse_SOAPConfigAndActionFields(t *testing.T) {
	input := `{
		"script": {
			"name": "soap-login",
			"protocol": "soap",
			"config": {
				"base_url": "https://soap.example.com",
				"soap": {
					"version": "1.1",
					"namespaces": {
						"soapenv": "http://schemas.xmlsoap.org/soap/envelope/",
						"auth": "urn:auth"
					},
					"ws_security": {
						"username_token": {
							"username": "svc-user",
							"password": "svc-pass",
							"password_type": "text",
							"nonce": true,
							"created": true
						},
						"timestamp": {
							"ttl_seconds": 60
						}
					},
					"fault_handling": {
						"fail_on_fault": true
					}
				}
			},
			"actions": [
				{
					"name": "login",
					"type": "request",
					"method": "POST",
					"target": "/AuthService",
					"body": "<soapenv:Envelope></soapenv:Envelope>",
					"soap": {
						"action": "urn:auth#Login",
						"operation": "Login",
						"body_mode": "raw_xml"
					}
				}
			]
		}
	}`

	script, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := script.Config.SOAP.Version; got != "1.1" {
		t.Fatalf("soap version = %q, want 1.1", got)
	}
	if got := script.Config.SOAP.Namespaces["auth"]; got != "urn:auth" {
		t.Fatalf("soap namespace auth = %q, want urn:auth", got)
	}
	if script.Config.SOAP.WSSecurity == nil || script.Config.SOAP.WSSecurity.UsernameToken == nil {
		t.Fatalf("ws_security username_token = %#v, want configured token", script.Config.SOAP.WSSecurity)
	}
	if !script.Config.SOAP.FaultHandling.FailOnFault {
		t.Fatal("fault_handling.fail_on_fault = false, want true")
	}
	if script.Actions[0].SOAP == nil {
		t.Fatal("action soap = nil, want populated config")
	}
	if got := script.Actions[0].SOAP.Action; got != "urn:auth#Login" {
		t.Fatalf("soap action = %q, want urn:auth#Login", got)
	}
	if got := script.Actions[0].SOAP.BodyMode; got != "raw_xml" {
		t.Fatalf("soap body_mode = %q, want raw_xml", got)
	}
}

func TestParse_WithExtractorsAndAssertions(t *testing.T) {
	input := `{
		"script": {
			"name": "extract-test",
			"protocol": "http",
			"config": {
				"base_url": "https://api.example.com"
			},
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

func TestParse_BrowserActionsAllowSelectWaitAndScreenshot(t *testing.T) {
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
				{"name": "select-country", "type": "browser_select", "selector": "#country", "value": "United States"},
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
	if action := script.Actions[2]; action.Type != "browser_select" || action.Selector != "#country" || action.Value != "United States" {
		t.Fatalf("action = %#v, want browser_select with selector/value", action)
	}
	if script.Actions[3].WaitFor == nil || script.Actions[3].WaitFor.TimeoutMs != 5000 {
		t.Fatalf("wait_for = %#v, want timeout", script.Actions[3].WaitFor)
	}
	if script.Actions[4].Capture == nil || !script.Actions[4].Capture.HTML {
		t.Fatalf("capture = %#v, want html capture", script.Actions[4].Capture)
	}
}

func TestParse_JMSConfigAndActions(t *testing.T) {
	input := `{
		"script": {
			"name": "jms-flow",
			"protocol": "jms-solace",
			"config": {
				"jms": {
					"provider": "solace",
					"broker_url": "tcp://broker:55555",
					"destination": "orders.in",
					"subscription": "orders-sub",
					"reply_to": "orders.reply",
					"timeout_ms": 2500,
					"mock": true
				}
			},
			"actions": [
				{"name": "publish", "type": "jms_publish", "body": "{\"orderId\":\"A-100\"}"},
				{"name": "request", "type": "jms_request_reply", "body": "{\"ping\":true}"}
			]
		}
	}`

	script, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if script.Config.JMS.BrokerURL != "tcp://broker:55555" {
		t.Fatalf("broker_url = %q, want tcp://broker:55555", script.Config.JMS.BrokerURL)
	}
	if script.Config.JMS.TimeoutMs != 2500 {
		t.Fatalf("timeout_ms = %d, want 2500", script.Config.JMS.TimeoutMs)
	}
	if script.Config.JMS.Subscription != "orders-sub" {
		t.Fatalf("subscription = %q, want orders-sub", script.Config.JMS.Subscription)
	}
	if !script.Config.JMS.Mock {
		t.Fatal("mock = false, want true")
	}
}

func TestParse_JMSRequiresDestination(t *testing.T) {
	input := `{"script":{"name":"jms","protocol":"jms-solace","actions":[{"name":"consume","type":"jms_consume"}]}}`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected destination validation error")
	}
}

func TestParse_JMSRequestReplyRequiresReplyDestination(t *testing.T) {
	input := `{"script":{"name":"jms","protocol":"jms-solace","config":{"jms":{"destination":"orders.in"}},"actions":[{"name":"request","type":"jms_request_reply","body":"{}"}]}}`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected reply_to validation error")
	}
}

func TestParse_HTTPRequiresBaseURL(t *testing.T) {
	input := `{"script":{"name":"http-no-base","protocol":"http","actions":[{"name":"request","type":"request","target":"/ping"}]}}`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected base_url validation error")
	}
	if err.Error() != "config.base_url is empty" {
		t.Fatalf("error = %q, want config.base_url is empty", err.Error())
	}
}

func TestParseFile(t *testing.T) {
	content := `{"script": {"name": "file-test", "protocol": "http", "config": {"base_url": "https://example.com"}, "actions": [{"name": "a", "type": "request", "target": "/"}]}}`
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

func TestParseFileResolvesBodyFile(t *testing.T) {
	dir := t.TempDir()
	payload := "{\n  \"sku\": \"APEX-1\"\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "order.json"), []byte(payload), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	content := `{"script":{"name":"body-file","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"create","type":"POST","target":"/orders","body_file":"order.json"}]}}`
	path := filepath.Join(dir, "script.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}

	script, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if got := script.Actions[0].Body; got != payload {
		t.Fatalf("body = %q, want %q", got, payload)
	}
	if script.Actions[0].BodyFile != "" {
		t.Fatalf("body_file = %q, want cleared", script.Actions[0].BodyFile)
	}
}

func TestParseFileSnapshotUsesSuppliedBytesAndResolvesLocalReferences(t *testing.T) {
	dir := t.TempDir()
	payload := "{\n  \"sku\": \"SNAPSHOT-1\"\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "order.json"), []byte(payload), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	path := filepath.Join(dir, "script.json")
	if err := os.WriteFile(path, []byte(`{"name":"on-disk-file-is-not-the-snapshot"}`), 0o644); err != nil {
		t.Fatalf("write on-disk script: %v", err)
	}
	snapshot := []byte(`{"script":{"name":"trusted-snapshot","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"create","type":"POST","target":"/orders","body_file":"order.json"}]}}`)

	script, err := ParseFileSnapshot(path, snapshot)
	if err != nil {
		t.Fatalf("ParseFileSnapshot: %v", err)
	}
	if script.Name != "trusted-snapshot" {
		t.Fatalf("name = %q, want trusted-snapshot", script.Name)
	}
	if got := script.Actions[0].Body; got != payload {
		t.Fatalf("body = %q, want %q", got, payload)
	}
	if script.Actions[0].BodyFile != "" {
		t.Fatalf("body_file = %q, want cleared", script.Actions[0].BodyFile)
	}
}

func TestParseFileResolvesRelativeDataSourcePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "users.csv"), []byte("name\nalice\n"), 0o644); err != nil {
		t.Fatalf("write data source: %v", err)
	}
	content := `{"script":{"name":"relative-data","protocol":"http","config":{"base_url":"https://example.test"},"data_sources":[{"name":"users","type":"csv","path":"users.csv","first_row_header":true}],"actions":[{"name":"get","type":"GET","target":"/"}]}}`
	path := filepath.Join(dir, "script.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	script, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if got, want := script.DataSources[0].Path, filepath.Join(dir, "users.csv"); got != want {
		t.Fatalf("data source path = %q, want %q", got, want)
	}
}

func TestParseFileBodyFileExample(t *testing.T) {
	script, err := ParseFile(filepath.Join("..", "..", "examples", "bodyrefs", "body-file-http.json"))
	if err != nil {
		t.Fatalf("ParseFile example: %v", err)
	}
	if len(script.Actions) != 1 {
		t.Fatalf("actions count = %d, want 1", len(script.Actions))
	}
	if !strings.Contains(script.Actions[0].Body, "APEX-LOAD-001") {
		t.Fatalf("body = %q, want resolved example payload", script.Actions[0].Body)
	}
	if script.Actions[0].BodyFile != "" {
		t.Fatalf("body_file = %q, want cleared", script.Actions[0].BodyFile)
	}
}

func TestParseRejectsBodyFileWithoutLocalBaseDir(t *testing.T) {
	input := `{"script":{"name":"upload-body-file","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"create","type":"POST","target":"/orders","body_file":"order.json"}]}}`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected body_file validation error")
	}
	if !strings.Contains(err.Error(), "body_file is only supported when loading scripts from a local file") {
		t.Fatalf("error = %q, want body_file upload-context message", err.Error())
	}
}

func TestParseFile_NotFound(t *testing.T) {
	_, err := ParseFile("/nonexistent/path.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestParse_JavaHTTPExampleFiles(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		helperMode string
	}{
		{
			name:       "embedded helper example",
			path:       filepath.Join("..", "..", "examples", "simple-java-http-embedded.json"),
			helperMode: "embedded",
		},
		{
			name:       "byte-preserving helper example",
			path:       filepath.Join("..", "..", "examples", "simple-java-http-sidecar.json"),
			helperMode: "embedded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script, err := ParseFile(tt.path)
			if err != nil {
				t.Fatalf("unexpected error parsing %s: %v", tt.path, err)
			}
			if script.Protocol != "java_http" {
				t.Fatalf("protocol = %q, want java_http", script.Protocol)
			}
			if script.Config.JavaHTTP.HelperMode != tt.helperMode {
				t.Fatalf("helper_mode = %q, want %q", script.Config.JavaHTTP.HelperMode, tt.helperMode)
			}
			if len(script.Actions) != 1 {
				t.Fatalf("actions count = %d, want 1", len(script.Actions))
			}
		})
	}
}

func TestParse_InitAndTeardown(t *testing.T) {
	input := `{
		"script": {
			"name": "lifecycle-test",
			"protocol": "http",
			"config": {
				"base_url": "https://example.com"
			},
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
