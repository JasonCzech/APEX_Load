package engine

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"net/http"
	"net/http/httptest"

	"github.com/jasonczech/apexload/pkg/dsl"
	_ "github.com/jasonczech/apexload/pkg/protocol/http"
)

func TestReplayScriptRejectsImportGateBeforeTransport(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	script := &dsl.Script{
		Name:     "blocked import",
		Protocol: "http",
		Actions: []dsl.Action{{
			Name:   "unsafe request",
			Type:   "request",
			Method: http.MethodGet,
			Target: server.URL,
			Metadata: map[string]any{
				"execution_blocker": "import_review_required",
			},
		}},
	}

	report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true})
	if err == nil || !strings.Contains(err.Error(), "import_review_required") {
		t.Fatalf("ReplayScript() = (%#v, %v), want durable import gate error", report, err)
	}
	if report != nil {
		t.Fatalf("ReplayScript() report = %#v, want nil", report)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("transport requests = %d, want 0", got)
	}
}

func TestNativeTransactionValueGateBlocksDirectEngineEntryPoints(t *testing.T) {
	const blocker = "source_transaction_native_value_unsupported"
	script := &dsl.Script{
		Name:     "blocked native transaction getter",
		Protocol: "http",
		Actions: []dsl.Action{{
			Name: "lr_get_transaction_duration",
			Type: "source_review",
			Params: map[string]any{
				"function":     "lr_get_transaction_duration",
				"support_mode": "review_only",
			},
			Metadata: map[string]any{
				"execution_blocker": blocker,
				"source_file":       "Action.c",
				"source_line":       3,
				"source_call":       `lr_get_transaction_duration("Checkout");`,
			},
		}},
	}

	if _, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true}); err == nil ||
		!strings.Contains(err.Error(), blocker) {
		t.Fatalf("ReplayScript error = %v, want transaction-value import gate", err)
	}
	if _, err := New(Config{Script: script, VUsers: 1, Duration: time.Second}); err == nil ||
		!strings.Contains(err.Error(), blocker) {
		t.Fatalf("New error = %v, want transaction-value import gate", err)
	}
}

func TestEngineEntryPointsRejectImportExecutionGates(t *testing.T) {
	blocked := &dsl.Script{
		Name:     "blocked",
		Protocol: "http",
		Actions: []dsl.Action{{
			Name:   "nested sequence",
			Type:   "http_sequence",
			Target: "sequence",
			Params: map[string]any{
				"requests": []any{map[string]any{
					"name":   "child",
					"method": http.MethodGet,
					"target": "https://example.test",
					"metadata": map[string]any{
						"execution_blockers": []any{"import_missing_variables"},
					},
				}},
			},
		}},
	}

	if _, err := New(Config{
		Script:   blocked,
		VUsers:   1,
		Duration: time.Second,
	}); err == nil || !strings.Contains(err.Error(), "import_missing_variables") {
		t.Fatalf("New() error = %v, want nested import blocker", err)
	}
	if _, err := NewMultiGroup(MultiGroupConfig{
		Duration: time.Second,
		Groups: []GroupConfig{{
			Name:   "blocked",
			Script: blocked,
			VUsers: 1,
		}},
	}); err == nil || !strings.Contains(err.Error(), "import_missing_variables") {
		t.Fatalf("NewMultiGroup() error = %v, want nested import blocker", err)
	}
	if err := RunScriptShakeout(context.Background(), "blocked", blocked); err == nil ||
		!strings.Contains(err.Error(), "import_missing_variables") {
		t.Fatalf("RunScriptShakeout() error = %v, want nested import blocker", err)
	}
}

func TestEngineRunRechecksImportGateBeforeTransport(t *testing.T) {
	script := &dsl.Script{
		Name: "becomes blocked",
		// A missing protocol makes the ordering assertion deterministic without
		// opening a socket: if the gate is bypassed, protocol lookup is the
		// first observable failure.
		Protocol: "missing-import-gate-test-protocol",
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "request",
			Method: http.MethodGet,
			Target: "https://example.test",
		}},
	}
	runner, err := New(Config{Script: script, VUsers: 1, Duration: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	script.Actions[0].Metadata = map[string]any{
		"execution_blocker": "import_review_required",
	}

	result, err := runner.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "import_review_required") {
		t.Fatalf("Run() = (%#v, %v), want execution blocker", result, err)
	}
	if result != nil {
		t.Fatalf("Run() result = %#v, want nil", result)
	}
	if strings.Contains(err.Error(), "protocol init") {
		t.Fatalf("Run() reached protocol lookup before enforcing gate: %v", err)
	}
}

func TestMultiGroupRuntimeBoundariesRecheckImportGate(t *testing.T) {
	script := &dsl.Script{
		Name:     "becomes blocked",
		Protocol: "http",
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "request",
			Method: http.MethodGet,
			Target: "https://example.test",
		}},
	}
	runner, err := NewMultiGroup(MultiGroupConfig{
		Duration: 50 * time.Millisecond,
		Groups: []GroupConfig{{
			Name:   "group",
			Script: script,
			VUsers: 1,
		}},
	})
	if err != nil {
		t.Fatalf("NewMultiGroup() error = %v", err)
	}
	script.Actions[0].Metadata = map[string]any{
		"execution_blocker": "import_missing_variables",
	}

	if err := runner.PreflightRuntime(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "import_missing_variables") {
		t.Fatalf("PreflightRuntime() error = %v, want execution blocker", err)
	}
	result, err := runner.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "import_missing_variables") {
		t.Fatalf("Run() = (%#v, %v), want execution blocker", result, err)
	}
	if result != nil {
		t.Fatalf("Run() result = %#v, want nil", result)
	}
}

func TestImportExecutionGateDoesNotReinterpretHistoricalReviewFlag(t *testing.T) {
	script := &dsl.Script{
		Name:     "legacy reviewed",
		Protocol: "http",
		Actions: []dsl.Action{
			{
				Name:   "legacy action",
				Type:   "request",
				Method: http.MethodGet,
				Target: "https://example.test",
				Metadata: map[string]any{
					"review_required": true,
				},
			},
			{
				Name: "legacy sequence",
				Type: "http_sequence",
				Params: map[string]any{
					"requests": []map[string]any{{
						"name":   "legacy child",
						"method": http.MethodGet,
						"target": "https://example.test",
						"metadata": map[string]any{
							"review_required": true,
						},
					}},
				},
			},
		},
	}
	if err := importExecutionGateError(script, "replay"); err != nil {
		t.Fatalf("importExecutionGateError() = %v, want historical review flag unchanged", err)
	}
}

func TestImportExecutionGateFindsTypedSequenceChildInNestedControlFlow(t *testing.T) {
	script := &dsl.Script{
		Name:     "typed nested blocker",
		Protocol: "http",
		Actions: []dsl.Action{{
			Name: "control",
			Type: "control_if",
			Control: &dsl.ControlFlow{
				Else: []dsl.Action{{
					Name: "sequence",
					Type: "http_sequence",
					Params: map[string]any{
						"requests": []dsl.Action{{
							Name:   "typed child",
							Method: http.MethodGet,
							Target: "https://example.test",
							Metadata: map[string]any{
								"execution_blocker": "import_review_required",
							},
						}},
					},
				}},
			},
		}},
	}

	err := importExecutionGateError(script, "debug")
	if err == nil ||
		!strings.Contains(err.Error(), "actions[0].control.else[0].params.requests[0]") ||
		!strings.Contains(err.Error(), "import_review_required") {
		t.Fatalf("importExecutionGateError() = %v, want nested typed child blocker", err)
	}
}
