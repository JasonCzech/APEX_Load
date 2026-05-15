package engine

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/protocol"
)

type iterationResetProtocol struct {
	initCalls     int
	teardownCalls int
	executeCalls  int
}

type loggingProtocol struct {
	result *protocol.Result
}

func (p *iterationResetProtocol) Name() string { return "browser" }
func (p *iterationResetProtocol) Init(_ context.Context, _ map[string]any) error {
	p.initCalls++
	return nil
}
func (p *iterationResetProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.executeCalls++
	return &protocol.Result{
		ActionName: action.Name,
		Success:    true,
		Duration:   time.Millisecond,
	}, nil
}
func (p *iterationResetProtocol) Teardown(_ context.Context) error {
	p.teardownCalls++
	return nil
}
func (p *iterationResetProtocol) HealthCheck(_ context.Context) error { return nil }

func (p *loggingProtocol) Name() string { return "http" }
func (p *loggingProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *loggingProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	result := *p.result
	result.ActionName = action.Name
	return &result, nil
}
func (p *loggingProtocol) Teardown(_ context.Context) error { return nil }
func (p *loggingProtocol) HealthCheck(_ context.Context) error { return nil }

func TestVUser_Interpolate(t *testing.T) {
	vu := &VUser{
		Variables: map[string]string{
			"token": "abc123",
			"host":  "example.com",
		},
	}

	tests := []struct {
		input string
		want  string
	}{
		{"{{var.token}}", "abc123"},
		{"https://{{var.host}}/api", "https://example.com/api"},
		{"Bearer {{var.token}}", "Bearer abc123"},
		{"{{data.host}}", "example.com"},
		{"no-vars-here", "no-vars-here"},
		{"", ""},
	}

	for _, tt := range tests {
		got := vu.interpolate(tt.input)
		if got != tt.want {
			t.Errorf("interpolate(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestVUser_Interpolate_EnvAndFn(t *testing.T) {
	t.Setenv("STORMRUNNER_TEST_ENV", "env-value")
	vu := &VUser{Variables: map[string]string{}}

	if got := vu.interpolate("{{env.STORMRUNNER_TEST_ENV}}"); got != "env-value" {
		t.Fatalf("env interpolation = %q, want %q", got, "env-value")
	}

	gotUUID := vu.interpolate("{{fn.uuid()}}")
	uuidPattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidPattern.MatchString(gotUUID) {
		t.Fatalf("fn.uuid() interpolation returned non-uuid value: %q", gotUUID)
	}

	if got := vu.interpolate("{{env.STORMRUNNER_MISSING_ENV}}"); got != "{{env.STORMRUNNER_MISSING_ENV}}" {
		t.Fatalf("missing env should remain unchanged, got %q", got)
	}
}

func TestVUser_InterpolateMap(t *testing.T) {
	vu := &VUser{
		Variables: map[string]string{"env": "staging"},
	}

	m := map[string]string{
		"Host":          "{{var.env}}.example.com",
		"Authorization": "static-value",
	}

	got := vu.interpolateMap(m)
	if got["Host"] != "staging.example.com" {
		t.Errorf("Host = %q, want %q", got["Host"], "staging.example.com")
	}
	if got["Authorization"] != "static-value" {
		t.Errorf("Authorization = %q, want %q", got["Authorization"], "static-value")
	}
}

func TestVUser_InterpolateMap_Nil(t *testing.T) {
	vu := &VUser{Variables: map[string]string{}}
	if got := vu.interpolateMap(nil); got != nil {
		t.Errorf("interpolateMap(nil) = %v, want nil", got)
	}
}

func TestVUser_MergeExtractions(t *testing.T) {
	vu := &VUser{Variables: map[string]string{"existing": "value"}}
	vu.mergeExtractions(&protocol.Result{
		Extractions: map[string]string{
			"token":    "new-token",
			"existing": "overwritten",
		},
	})

	if vu.Variables["token"] != "new-token" {
		t.Errorf("token = %q", vu.Variables["token"])
	}
	if vu.Variables["existing"] != "overwritten" {
		t.Errorf("existing should be overwritten, got %q", vu.Variables["existing"])
	}
}

func TestVUser_ResolveAction(t *testing.T) {
	t.Setenv("STORMRUNNER_ACTION_ENV", "env-action")

	vu := &VUser{
		Variables: map[string]string{
			"base":           "https://api.test.com",
			"users.username": "alice",
		},
	}

	action := dsl.Action{
		Name:     "get-user",
		Type:     "request",
		Method:   "GET",
		Target:   "{{var.base}}/users/1",
		Selector: "#{{data.users.username}}",
		Value:    "{{env.STORMRUNNER_ACTION_ENV}}",
		Headers:  map[string]string{"Accept": "application/json"},
		Body:     `{"key": "{{var.base}}"}`,
		Params: map[string]any{
			"user": "{{data.users.username}}",
			"auth": map[string]any{
				"token": "{{env.STORMRUNNER_ACTION_ENV}}",
			},
			"labels": []any{"{{var.base}}", 123},
		},
		Extractors: []dsl.Extractor{
			{Name: "id", Source: "body", Type: "jsonpath", Expression: "$.id"},
		},
		WaitFor: &dsl.WaitCondition{Type: "text", Value: "{{data.users.username}}", TimeoutMs: 2500},
		Capture: &dsl.BrowserCaptureOptions{Screenshot: true, HTML: true},
		Metadata: map[string]any{
			"note": "{{var.base}}",
		},
		SOAP: &dsl.SOAPActionConfig{
			Action:    "urn:user#{{data.users.username}}",
			Operation: "Lookup{{data.users.username}}",
			BodyMode:  "raw_xml",
		},
		Assertions: []dsl.Assertion{
			{Type: "status_code", Expected: 200},
		},
	}

	resolved := vu.resolveAction(action)

	if resolved.Name != "get-user" {
		t.Errorf("Name = %q", resolved.Name)
	}
	if resolved.Target != "https://api.test.com/users/1" {
		t.Errorf("Target = %q", resolved.Target)
	}
	if resolved.Selector != "#alice" {
		t.Errorf("Selector = %q, want #alice", resolved.Selector)
	}
	if resolved.Value != "env-action" {
		t.Errorf("Value = %q, want env-action", resolved.Value)
	}
	if string(resolved.Body) != `{"key": "https://api.test.com"}` {
		t.Errorf("Body = %q", string(resolved.Body))
	}
	if resolved.Params["user"] != "alice" {
		t.Errorf("Params[user] = %v, want alice", resolved.Params["user"])
	}
	auth, ok := resolved.Params["auth"].(map[string]any)
	if !ok || auth["token"] != "env-action" {
		t.Errorf("Params[auth] = %#v, want token env-action", resolved.Params["auth"])
	}
	labels, ok := resolved.Params["labels"].([]any)
	if !ok || len(labels) != 2 || labels[0] != "https://api.test.com" || labels[1] != 123 {
		t.Errorf("Params[labels] = %#v", resolved.Params["labels"])
	}
	if len(resolved.Extractors) != 1 {
		t.Fatalf("Extractors count = %d", len(resolved.Extractors))
	}
	if resolved.WaitFor == nil || resolved.WaitFor.Value != "alice" || resolved.WaitFor.TimeoutMs != 2500 {
		t.Fatalf("WaitFor = %#v, want text wait with alice", resolved.WaitFor)
	}
	if resolved.Capture == nil || !resolved.Capture.Screenshot || !resolved.Capture.HTML {
		t.Fatalf("Capture = %#v, want screenshot/html enabled", resolved.Capture)
	}
	if resolved.Metadata["note"] != "https://api.test.com" {
		t.Errorf("Metadata[note] = %v, want https://api.test.com", resolved.Metadata["note"])
	}
	if resolved.SOAP == nil {
		t.Fatal("SOAP = nil, want interpolated SOAP action config")
	}
	if resolved.SOAP.Action != "urn:user#alice" {
		t.Errorf("SOAP.Action = %q, want urn:user#alice", resolved.SOAP.Action)
	}
	if resolved.SOAP.Operation != "Lookupalice" {
		t.Errorf("SOAP.Operation = %q, want Lookupalice", resolved.SOAP.Operation)
	}
	if resolved.SOAP.BodyMode != "raw_xml" {
		t.Errorf("SOAP.BodyMode = %q, want raw_xml", resolved.SOAP.BodyMode)
	}
	if len(resolved.Assertions) != 1 {
		t.Fatalf("Assertions count = %d", len(resolved.Assertions))
	}
}

func TestSnapshotStore(t *testing.T) {
	store := NewSnapshotStore(3)

	store.Add(&protocol.ErrorSnapshot{RequestURL: "http://a.com"})
	store.Add(&protocol.ErrorSnapshot{RequestURL: "http://b.com"})
	store.Add(&protocol.ErrorSnapshot{RequestURL: "http://c.com"})
	store.Add(&protocol.ErrorSnapshot{RequestURL: "http://d.com"}) // should be dropped (max 3)

	snaps := store.Snapshots()
	if len(snaps) != 3 {
		t.Errorf("snapshots count = %d, want 3", len(snaps))
	}
}

func TestSnapshotStore_NilSafe(t *testing.T) {
	var store *SnapshotStore
	store.Add(&protocol.ErrorSnapshot{}) // should not panic
	if store.Snapshots() != nil {
		t.Error("nil store Snapshots() should return nil")
	}
}

func TestSnapshotStore_NilSnapshot(t *testing.T) {
	store := NewSnapshotStore(10)
	store.Add(nil) // should not panic or add
	if len(store.Snapshots()) != 0 {
		t.Error("nil snapshot should not be added")
	}
}

func TestReplaceAll(t *testing.T) {
	tests := []struct {
		s, old, new, want string
	}{
		{"hello world", "world", "go", "hello go"},
		{"aaa", "a", "bb", "bbbbbb"},
		{"no match", "xyz", "abc", "no match"},
		{"", "a", "b", ""},
	}

	for _, tt := range tests {
		got := replaceAll(tt.s, tt.old, tt.new)
		if got != tt.want {
			t.Errorf("replaceAll(%q, %q, %q) = %q, want %q", tt.s, tt.old, tt.new, got, tt.want)
		}
	}
}

func TestIndexOf(t *testing.T) {
	tests := []struct {
		s, substr string
		want      int
	}{
		{"hello world", "world", 6},
		{"hello world", "xyz", -1},
		{"aaa", "a", 0},
		{"", "a", -1},
	}

	for _, tt := range tests {
		got := indexOf(tt.s, tt.substr)
		if got != tt.want {
			t.Errorf("indexOf(%q, %q) = %d, want %d", tt.s, tt.substr, got, tt.want)
		}
	}
}

func TestToProtocolExtractors(t *testing.T) {
	exts := []dsl.Extractor{
		{Name: "token", Source: "body", Type: "jsonpath", Expression: "$.token"},
		{Name: "header_val", Source: "header", Type: "regex", Expression: "X-Request-Id: (.+)"},
	}

	result := toProtocolExtractors(exts)
	if len(result) != 2 {
		t.Fatalf("count = %d, want 2", len(result))
	}
	if result[0].Name != "token" || result[0].Source != "body" {
		t.Errorf("extractor[0] = %+v", result[0])
	}
}

func TestToProtocolAssertions(t *testing.T) {
	asserts := []dsl.Assertion{
		{Type: "status_code", Expected: 200},
		{Type: "response_time", Max: 5000},
	}

	result := toProtocolAssertions(asserts)
	if len(result) != 2 {
		t.Fatalf("count = %d, want 2", len(result))
	}
	if result[0].Type != "status_code" {
		t.Errorf("assertion[0].Type = %q", result[0].Type)
	}
	if result[1].Max != 5000 {
		t.Errorf("assertion[1].Max = %d, want 5000", result[1].Max)
	}
}

func TestVUser_HandleSnapshot(t *testing.T) {
	store := NewSnapshotStore(10)
	vu := &VUser{SnapshotStore: store}
	errCfg := dsl.ErrorHandlingConfig{SnapshotOnError: true}

	vu.handleSnapshot(&protocol.Result{
		Success:  false,
		Snapshot: &protocol.ErrorSnapshot{RequestURL: "http://fail.com"},
	}, errCfg)

	if len(store.Snapshots()) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(store.Snapshots()))
	}

	// Success should not add snapshot
	vu.handleSnapshot(&protocol.Result{
		Success:  true,
		Snapshot: &protocol.ErrorSnapshot{RequestURL: "http://ok.com"},
	}, errCfg)

	if len(store.Snapshots()) != 1 {
		t.Errorf("success should not add snapshot, got %d", len(store.Snapshots()))
	}
}

func TestVUser_Run_LogsExtendedOptions(t *testing.T) {
	t.Setenv("STORMRUNNER_LOG_HOST", "api.example.test")
	t.Setenv("STORMRUNNER_LOG_TOKEN", "abc123")

	protoStub := &loggingProtocol{result: &protocol.Result{
		Success:          true,
		StatusCode:       200,
		Duration:         25 * time.Millisecond,
		BytesReceived:    128,
		ResponseBody:     `{"status":"ok"}`,
		DependencyTarget: "api.example.test",
		CorrelationID:    "corr-123",
		DNSTime:          2 * time.Millisecond,
		ConnectTime:      3 * time.Millisecond,
		TLSTime:          4 * time.Millisecond,
		TTFB:             5 * time.Millisecond,
	}}

	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	vu := &VUser{
		ID:    1,
		Group: "Checkout",
		Proto: protoStub,
		Script: &dsl.Script{
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 1},
				Log: dsl.LogConfig{
					Level:           "extended",
					ExtendedOptions: []string{"parameter_substitution", "data_returned", "advanced_trace"},
				},
			},
			Actions: []dsl.Action{{
				Name:   "Checkout",
				Type:   "request",
				Method: "POST",
				Target: "https://{{env.STORMRUNNER_LOG_HOST}}/checkout",
				Body:   "token={{env.STORMRUNNER_LOG_TOKEN}}",
			}},
		},
		Metrics: NewMetricsCollector(16),
	}

	vu.Run(context.Background())

	logOutput := logBuf.String()
	for _, expected := range []string{
		"msg=\"action ok\"",
		"target=https://api.example.test/checkout",
		"request_body=\"token=abc123\"",
		"response_body=\"{\\\"status\\\":\\\"ok\\\"}\"",
		"dependency_target=api.example.test",
		"correlation_id=corr-123",
		"dns_ms=2",
		"ttfb_ms=5",
	} {
		if !strings.Contains(logOutput, expected) {
			t.Fatalf("log output missing %q: %s", expected, logOutput)
		}
	}
}

func TestVUser_Run_ResetsBrowserProtocolPerIteration(t *testing.T) {
	newUserPerIteration := true
	protoStub := &iterationResetProtocol{}
	if err := protoStub.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}

	vu := &VUser{
		ID:    1,
		Proto: protoStub,
		Script: &dsl.Script{
			Protocol: "browser",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 2},
				Browser:  dsl.BrowserConfig{NewUserPerIter: &newUserPerIteration},
			},
			Actions: []dsl.Action{{Name: "Navigate", Type: "browser_navigate", Target: "/"}},
		},
		Metrics: NewMetricsCollector(16),
	}

	vu.Run(context.Background())

	if protoStub.executeCalls != 2 {
		t.Fatalf("executeCalls = %d, want 2", protoStub.executeCalls)
	}
	if protoStub.initCalls != 2 {
		t.Fatalf("initCalls = %d, want 2 after per-iteration reset", protoStub.initCalls)
	}
	if protoStub.teardownCalls != 1 {
		t.Fatalf("teardownCalls = %d, want 1 between iterations", protoStub.teardownCalls)
	}
}
