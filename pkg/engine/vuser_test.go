package engine

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type iterationResetProtocol struct {
	initCalls     int
	teardownCalls int
	executeCalls  int
}

type loggingProtocol struct {
	result       *protocol.Result
	executeCalls int
}

type sequenceProtocol struct {
	failFirst bool
	actions   []*protocol.Action
}

type sequenceCaptureSink struct {
	results      []*protocol.Result
	transactions []*protocol.Result
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

func (p *loggingProtocol) Name() string                                   { return "http" }
func (p *loggingProtocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (p *loggingProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.executeCalls++
	result := *p.result
	result.ActionName = action.Name
	return &result, nil
}
func (p *loggingProtocol) Teardown(_ context.Context) error    { return nil }
func (p *loggingProtocol) HealthCheck(_ context.Context) error { return nil }

func (p *sequenceProtocol) Name() string { return "http" }
func (p *sequenceProtocol) Init(_ context.Context, _ map[string]any) error {
	return nil
}
func (p *sequenceProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.actions = append(p.actions, action)
	result := &protocol.Result{
		ActionName:       action.Name,
		Success:          true,
		Duration:         time.Millisecond,
		DependencyType:   "http",
		DependencyTarget: action.Target,
		Extractions:      map[string]string{},
	}
	if p.failFirst && len(p.actions) == 1 {
		result.Success = false
		result.ErrorMessage = "first child failed"
	}
	if action.Name == "Login" {
		result.Extractions["token"] = "abc123"
	}
	return result, nil
}
func (p *sequenceProtocol) Teardown(_ context.Context) error    { return nil }
func (p *sequenceProtocol) HealthCheck(_ context.Context) error { return nil }

func (s *sequenceCaptureSink) RecordResult(result *protocol.Result) error {
	s.results = append(s.results, result)
	return nil
}

func (s *sequenceCaptureSink) RecordTransaction(result *protocol.Result) error {
	s.transactions = append(s.transactions, result)
	return nil
}

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
	t.Setenv("APEXLOAD_TEST_ENV", "env-value")
	t.Setenv("APEXLOAD_SECRET_DB_PASSWORD", "super-secret")
	vu := &VUser{Variables: map[string]string{}}

	if got := vu.interpolate("{{env.APEXLOAD_TEST_ENV}}"); got != "env-value" {
		t.Fatalf("env interpolation = %q, want %q", got, "env-value")
	}

	gotUUID := vu.interpolate("{{fn.uuid()}}")
	uuidPattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidPattern.MatchString(gotUUID) {
		t.Fatalf("fn.uuid() interpolation returned non-uuid value: %q", gotUUID)
	}

	if got := vu.interpolate("{{env.APEXLOAD_MISSING_ENV}}"); got != "{{env.APEXLOAD_MISSING_ENV}}" {
		t.Fatalf("missing env should remain unchanged, got %q", got)
	}

	if got := vu.interpolate("{{secret.db_password}}"); got != "super-secret" {
		t.Fatalf("secret interpolation = %q, want %q", got, "super-secret")
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
	t.Setenv("APEXLOAD_ACTION_ENV", "env-action")

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
		Value:    "{{env.APEXLOAD_ACTION_ENV}}",
		Headers:  map[string]string{"Accept": "application/json"},
		Body:     `{"key": "{{var.base}}"}`,
		Params: map[string]any{
			"user": "{{data.users.username}}",
			"auth": map[string]any{
				"token": "{{env.APEXLOAD_ACTION_ENV}}",
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
	t.Setenv("APEXLOAD_LOG_HOST", "api.example.test")
	t.Setenv("APEXLOAD_LOG_TOKEN", "abc123")

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
				Target: "https://{{env.APEXLOAD_LOG_HOST}}/checkout",
				Body:   "token={{env.APEXLOAD_LOG_TOKEN}}",
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

func TestVUser_Run_HTTPSequencePropagatesExtractionsAndRecordsOneTransaction(t *testing.T) {
	protoStub := &sequenceProtocol{}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		ID:         1,
		Group:      "Checkout",
		Proto:      protoStub,
		Variables:  map[string]string{},
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
		Script: &dsl.Script{
			Name:     "Imported",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 1},
			},
			Actions: []dsl.Action{{
				Name: "CheckoutFlow",
				Type: "http_sequence",
				Params: map[string]any{
					"requests": []map[string]any{{
						"name":   "Login",
						"type":   "POST",
						"method": "POST",
						"target": "/login",
						"body":   "user=ada",
					}, {
						"name":   "Checkout",
						"type":   "POST",
						"method": "POST",
						"target": "/checkout",
						"body":   "token={{var.token}}",
					}},
				},
				Metadata: map[string]any{"source_transaction": "CheckoutFlow"},
			}},
		},
	}

	vu.Run(context.Background())

	if len(protoStub.actions) != 2 {
		t.Fatalf("executed children = %d, want 2", len(protoStub.actions))
	}
	if protoStub.actions[0].Name != "Login" || protoStub.actions[1].Name != "Checkout" {
		t.Fatalf("child order = [%s %s], want Login then Checkout", protoStub.actions[0].Name, protoStub.actions[1].Name)
	}
	if got := string(protoStub.actions[1].Body); got != "token=abc123" {
		t.Fatalf("checkout body = %q, want token from first child extraction", got)
	}
	if len(sink.results) != 2 {
		t.Fatalf("recorded child results = %d, want 2", len(sink.results))
	}
	if len(sink.transactions) != 1 {
		t.Fatalf("recorded transactions = %d, want one logical transaction", len(sink.transactions))
	}
	if tx := sink.transactions[0]; tx.ActionName != "CheckoutFlow" || !tx.Success || tx.SourceTransaction != "CheckoutFlow" {
		t.Fatalf("transaction = %#v, want successful CheckoutFlow source transaction", tx)
	}
}

func TestVUser_Run_HTTPSequenceContinueOnErrorRunsRemainingChildren(t *testing.T) {
	protoStub := &sequenceProtocol{failFirst: true}
	sink := &sequenceCaptureSink{}
	vu := &VUser{
		ID:         1,
		Group:      "Checkout",
		Proto:      protoStub,
		Variables:  map[string]string{},
		Metrics:    NewMetricsCollector(16),
		ResultSink: sink,
		Script: &dsl.Script{
			Name:     "Imported",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic:      dsl.RunLogicConfig{Iterations: 1},
				ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: true},
			},
			Actions: []dsl.Action{{
				Name: "CheckoutFlow",
				Type: "http_sequence",
				Params: map[string]any{
					"requests": []map[string]any{{
						"name":   "Login",
						"type":   "POST",
						"method": "POST",
						"target": "/login",
					}, {
						"name":   "Checkout",
						"type":   "POST",
						"method": "POST",
						"target": "/checkout",
					}},
				},
				Metadata: map[string]any{"source_transaction": "CheckoutFlow"},
			}},
		},
	}

	vu.Run(context.Background())

	if len(protoStub.actions) != 2 {
		t.Fatalf("executed children = %d, want both children despite first failure", len(protoStub.actions))
	}
	if len(sink.transactions) != 1 {
		t.Fatalf("recorded transactions = %d, want one logical transaction", len(sink.transactions))
	}
	if tx := sink.transactions[0]; tx.Success || tx.ErrorMessage == "" {
		t.Fatalf("transaction = %#v, want failed logical transaction with an error", tx)
	}
}

func TestVUser_Run_RedactsSecretValuesFromLogs(t *testing.T) {
	t.Setenv("APEXLOAD_SECRET_LOG_TOKEN", "super-secret")

	protoStub := &loggingProtocol{result: &protocol.Result{
		Success:       true,
		StatusCode:    200,
		Duration:      10 * time.Millisecond,
		BytesReceived: 64,
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
				Log:      dsl.LogConfig{Level: "extended", ExtendedOptions: []string{"parameter_substitution"}},
			},
			Actions: []dsl.Action{{
				Name:   "Checkout",
				Type:   "request",
				Method: "POST",
				Target: "https://example.test/login?token={{secret.log_token}}",
				Body:   "token={{secret.log_token}}",
			}},
		},
		Metrics: NewMetricsCollector(16),
	}

	vu.Run(context.Background())

	logOutput := logBuf.String()
	if strings.Contains(logOutput, "super-secret") {
		t.Fatalf("expected secret value to be redacted from logs, got %s", logOutput)
	}
	for _, expected := range []string{"target=\"https://example.test/login?token=[REDACTED]\"", "request_body=\"token=[REDACTED]\""} {
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

func TestVUser_Run_ErrorMessageActionLogsWhenScriptLoggingDisabled(t *testing.T) {
	protoStub := &loggingProtocol{result: &protocol.Result{
		Success:       true,
		StatusCode:    200,
		Duration:      time.Millisecond,
		BytesReceived: 16,
	}}

	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	vu := &VUser{
		ID:        7,
		Group:     "Login",
		Proto:     protoStub,
		Variables: map[string]string{"userid": "qa-user"},
		Script: &dsl.Script{
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 1},
				Log:      dsl.LogConfig{Level: "disabled"},
			},
			Actions: []dsl.Action{{
				Name:  "LR_Error_Message_12",
				Type:  "error_message",
				Value: "Login failed for User: %s with status %d",
				Params: map[string]any{
					"args": []any{"{{var.userid}}", "500"},
				},
			}, {
				Name:   "Home",
				Type:   "request",
				Method: "GET",
				Target: "/home",
			}},
		},
		Metrics: NewMetricsCollector(16),
	}

	vu.Run(context.Background())

	if protoStub.executeCalls != 1 {
		t.Fatalf("executeCalls = %d, want only the HTTP request to reach protocol", protoStub.executeCalls)
	}
	logOutput := logBuf.String()
	for _, expected := range []string{
		"msg=\"source error message\"",
		"vuser=7",
		"group=Login",
		"action=LR_Error_Message_12",
		"message=\"Login failed for User: qa-user with status 500\"",
	} {
		if !strings.Contains(logOutput, expected) {
			t.Fatalf("log output missing %q: %s", expected, logOutput)
		}
	}
	if strings.Contains(logOutput, "msg=\"action ok\"") {
		t.Fatalf("disabled script logging emitted request logs: %s", logOutput)
	}
}

func TestRenderPrintfLikeMessageSupportsSourceFormatting(t *testing.T) {
	tests := []struct {
		name   string
		format string
		args   []any
		want   string
	}{
		{
			name:   "numeric flags precision and escaped percent",
			format: "HTML error %03d was triggered. Value #1 is %s (%.1f%%)",
			args:   []any{"7", "Param1", "12.34"},
			want:   "HTML error 007 was triggered. Value #1 is Param1 (12.3%)",
		},
		{
			name:   "c integer length modifiers",
			format: "total=%ld unsigned=%u",
			args:   []any{"42", "9"},
			want:   "total=42 unsigned=9",
		},
		{
			name:   "missing arguments keep specifier visible",
			format: "HTML error %d was triggered",
			args:   nil,
			want:   "HTML error %d was triggered",
		},
		{
			name:   "literal percent text does not consume arguments",
			format: "Progress was 100% complete for %s",
			args:   []any{"UserID"},
			want:   "Progress was 100% complete for UserID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := renderPrintfLikeMessage(tt.format, tt.args); got != tt.want {
				t.Fatalf("renderPrintfLikeMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVUser_Run_SourceUtilityActionsStayInEngine(t *testing.T) {
	vu := &VUser{
		ID:        3,
		Group:     "Checkout",
		Variables: map[string]string{},
		Script: &dsl.Script{
			Protocol: "http",
			Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
			Actions: []dsl.Action{{
				Name: "SaveUser",
				Type: "source_parameter",
				Params: map[string]any{
					"function": "lr_save_string",
					"args":     []any{"ada", "UserID"},
				},
			}, {
				Name: "Greeting",
				Type: "source_parameter",
				Params: map[string]any{
					"function": "lr_param_sprintf",
					"args":     []any{"Greeting", "Hello %s", "{{var.userid}}"},
				},
			}, {
				Name: "Timer1",
				Type: "source_timer",
				Params: map[string]any{
					"function": "lr_start_timer",
					"args":     []any{"Timer1"},
				},
			}, {
				Name: "Timer1",
				Type: "source_timer",
				Params: map[string]any{
					"function": "lr_end_timer",
					"args":     []any{"Timer1"},
				},
			}, {
				Name: "Checkout",
				Type: "source_transaction",
				Params: map[string]any{
					"function": "lr_start_transaction",
					"args":     []any{"Checkout"},
				},
			}, {
				Name: "Checkout",
				Type: "source_transaction",
				Params: map[string]any{
					"function": "lr_end_transaction",
					"args":     []any{"Checkout", "LR_PASS"},
				},
			}, {
				Name:  "Status",
				Type:  "source_message",
				Value: "Working as %s",
				Params: map[string]any{
					"function":       "lr_vuser_status_message",
					"format":         "Working as %s",
					"args":           []any{"{{var.userid}}"},
					"status_message": true,
				},
			}, {
				Name: "helper",
				Type: "custom_call",
				Params: map[string]any{
					"function":     "helper",
					"support_mode": "custom_review",
					"args":         []any{"42"},
				},
			}},
		},
		Metrics: NewMetricsCollector(16),
	}

	vu.Run(context.Background())

	if got := vu.Variables["userid"]; got != "ada" {
		t.Fatalf("userid = %q, want ada", got)
	}
	if got := vu.Variables["greeting"]; got != "Hello ada" {
		t.Fatalf("greeting = %q, want Hello ada", got)
	}
	if got := vu.Variables["vuser_status"]; got != "Working as ada" {
		t.Fatalf("vuser_status = %q, want rendered status", got)
	}
	if got := vu.Variables[sourceTimerDurationKey("Timer1")]; got == "" {
		t.Fatalf("timer duration variable missing: %#v", vu.Variables)
	}
	if got := vu.Variables[sourceTransactionStatusKey("Checkout")]; got != "true" {
		t.Fatalf("transaction status = %q, want true", got)
	}
}

type rendezvousTestProtocol struct {
	executeTimes chan time.Time
}

func (p *rendezvousTestProtocol) Name() string                               { return "rendezvous-test" }
func (p *rendezvousTestProtocol) Init(context.Context, map[string]any) error { return nil }
func (p *rendezvousTestProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.executeTimes <- time.Now()
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (p *rendezvousTestProtocol) Teardown(context.Context) error    { return nil }
func (p *rendezvousTestProtocol) HealthCheck(context.Context) error { return nil }

func TestVUser_Run_RendezvousSynchronizesStaggeredVUsers(t *testing.T) {
	executeTimes := make(chan time.Time, 2)
	script := &dsl.Script{
		Protocol: "http",
		Config:   dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}},
		Actions: []dsl.Action{{
			Name:   "sync-buyers",
			Type:   "rendezvous",
			Params: map[string]any{"count": 2, "timeout_ms": 500},
		}, {
			Name:   "checkout",
			Type:   "request",
			Target: "/checkout",
		}},
	}
	coordinator := NewRendezvousCoordinator()

	makeVU := func(id int) *VUser {
		return &VUser{
			ID:          id,
			TotalVUsers: 2,
			Proto:       &rendezvousTestProtocol{executeTimes: executeTimes},
			Script:      script,
			Metrics:     NewMetricsCollector(16),
			Rendezvous:  coordinator,
		}
	}

	vuOne := makeVU(0)
	vuTwo := makeVU(1)

	done := make(chan struct{}, 2)
	go func() {
		vuOne.Run(context.Background())
		done <- struct{}{}
	}()
	time.Sleep(150 * time.Millisecond)
	go func() {
		vuTwo.Run(context.Background())
		done <- struct{}{}
	}()

	first := <-executeTimes
	second := <-executeTimes
	<-done
	<-done

	delta := first.Sub(second)
	if delta < 0 {
		delta = -delta
	}
	if delta > 60*time.Millisecond {
		t.Fatalf("execute delta = %v, want rendezvous to release both VUsers together", delta)
	}
}

func TestScriptHasExplicitTransactionsRecognizesLegacyTypes(t *testing.T) {
	legacyTypeScript := &dsl.Script{
		Actions: []dsl.Action{{Name: "txn", Type: "loadrunner_transaction"}},
	}
	if !scriptHasExplicitTransactions(legacyTypeScript) {
		t.Error("expected legacy loadrunner_transaction action type to count as an explicit transaction")
	}

	legacyMetadataScript := &dsl.Script{
		Actions: []dsl.Action{{
			Name:     "seq",
			Type:     "http_sequence",
			Metadata: map[string]any{"loadrunner_transaction": "CheckoutFlow"},
		}},
	}
	if !scriptHasExplicitTransactions(legacyMetadataScript) {
		t.Error("expected legacy loadrunner_transaction metadata key to count as an explicit transaction")
	}
}

func TestSourceTransactionNameAcceptsLegacyKey(t *testing.T) {
	if got := sourceTransactionName(map[string]any{"source_transaction": "Checkout"}); got != "Checkout" {
		t.Errorf("source_transaction key = %q, want Checkout", got)
	}
	if got := sourceTransactionName(map[string]any{"loadrunner_transaction": "Checkout"}); got != "Checkout" {
		t.Errorf("loadrunner_transaction key = %q, want Checkout", got)
	}
	if got := sourceTransactionName(map[string]any{
		"source_transaction":     "New",
		"loadrunner_transaction": "Old",
	}); got != "New" {
		t.Errorf("canonical key should win, got %q, want New", got)
	}
}
