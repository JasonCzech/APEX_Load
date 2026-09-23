package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestErrorMessagePolicyMarksOpenTransactionsWithoutStopping(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		sink := &sequenceCaptureSink{}
		vu := &VUser{Script: &dsl.Script{Config: dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{FailTransactionOnErrorMessage: &enabled}}}, Variables: map[string]string{}, ResultSink: sink}
		openSourceTransaction(t, vu, "Outer")
		openSourceSubTransaction(t, vu, "Inner", "Outer")
		handled, err := vu.handleEngineAction(context.Background(), dsl.Action{Type: "error_message", Value: "problem"})
		if !handled || err != nil || len(vu.sourceTransactions) != 2 || len(sink.transactions) != 0 {
			t.Fatal("message changed execution flow")
		}
		for _, state := range vu.sourceTransactions {
			if (state.status == sourceTransactionFail) != enabled {
				t.Fatalf("enabled=%v status=%s", enabled, state.status)
			}
		}
	}
}
func TestResponseLogLimitDoesNotMutateResponse(t *testing.T) {
	result := &protocol.Result{ResponseBody: strings.Repeat("sample ", 100)}
	attrs := actionLogAttrs("request", nil, result, dsl.LogConfig{ExtendedOptions: []string{"data_returned"}, MaxResponseBytes: 12}, false, nil)
	found := false
	for i := 0; i < len(attrs)-1; i += 2 {
		if attrs[i] == "response_body" {
			found = true
			if value := attrs[i+1].(string); len(value) > 12 {
				t.Fatalf("log exceeded limit: %q", value)
			}
		}
	}
	if !found || len(result.ResponseBody) != 700 {
		t.Fatal("response was mutated")
	}
}
func TestHTTPReplayPolicyConfigPreservesFalse(t *testing.T) {
	off := false
	script := &dsl.Script{Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{DNSCache: &off, HTTPErrorsAsWarnings: &off, LegacyHTMLChecks: &off, AuthDelayMs: 25}}}
	cfg := map[string]any{}
	appendHTTPConfig(cfg, script)
	for _, key := range []string{"dns_cache", "http_errors_as_warnings", "legacy_html_checks"} {
		if v, ok := cfg[key]; !ok || v != false {
			t.Fatalf("lost false %s: %#v", key, cfg)
		}
	}
	if cfg["challenge_delay_ms"] != 25 {
		t.Fatal("lost auth delay")
	}
}

func TestNewUserCanRetainCacheWhileClearingCookies(t *testing.T) {
	on, off := true, false
	proto := &cacheAndCookieControllerProtocol{}
	vu := &VUser{ID: 1, Group: "test", Proto: proto, Metrics: NewMetricsCollector(16), Script: &dsl.Script{Name: "session", Protocol: "http", Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 3}, BrowserEmulation: &dsl.BrowserEmulationConfig{Cache: &dsl.BrowserCacheSimulationConfig{NewUserEachIteration: &on, ClearCacheForNewUser: &off}}}, Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}}}}
	vu.Run(context.Background())
	if proto.cookieClears != 2 || proto.cacheClears != 0 {
		t.Fatalf("cookies=%d cache=%d", proto.cookieClears, proto.cacheClears)
	}
}

func TestErrorMessagePolicyReplaysFailedTransactionAndContinues(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	on := true
	script := &dsl.Script{Name: "message policy", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{FailTransactionOnErrorMessage: &on}}, Actions: []dsl.Action{
		sourceTransactionCall("lr_start_transaction", "Open"),
		{Name: "message", Type: "error_message", Value: "failure"},
		{Name: "after message", Type: "http_request", Method: "GET", Target: server.URL},
		sourceTransactionCall("lr_end_transaction", "Open", "LR_AUTO"),
	}}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Success || hits.Load() != 1 {
		t.Fatalf("report=%+v hits=%d", report, hits.Load())
	}
}
func TestErrorMessagePolicyFailsAutomaticIterationWithoutStopping(t *testing.T) {
	on := true
	sink := &sequenceCaptureSink{}
	vu := &VUser{ID: 1, Group: "test", Proto: &cacheAndCookieControllerProtocol{}, Metrics: NewMetricsCollector(16), ResultSink: sink, Script: &dsl.Script{Name: "message", Protocol: "http", Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "per_iteration"}, ErrorHandling: dsl.ErrorHandlingConfig{FailTransactionOnErrorMessage: &on}}, Actions: []dsl.Action{{Name: "message", Type: "error_message", Value: "failure"}, {Name: "after", Type: "request", Method: "GET", Target: "/"}}}}
	vu.Run(context.Background())
	if len(sink.transactions) != 1 || sink.transactions[0].Success {
		t.Fatalf("iteration outcomes=%+v", sink.transactions)
	}
}
