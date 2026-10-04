package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// The sensitive value exists only in a discarded failed attempt. It must still
// classify the joined sibling's neutral alias and remain known after the join,
// without publishing the failed attempt's extraction as a runtime variable.
func TestConcurrentHTTPCombinedReviewDiscardedRetryClassification(t *testing.T) {
	const value = "blue-orchid-7289461"
	for _, runner := range []string{"load", "replay"} {
		t.Run(runner, func(t *testing.T) {
			var attempts, consumed atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/alias":
					_ = json.NewEncoder(w).Encode(map[string]string{"ordinary": value})
				case "/retry":
					if attempts.Add(1) == 1 {
						w.WriteHeader(http.StatusServiceUnavailable)
						_ = json.NewEncoder(w).Encode(map[string]string{"value": value})
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"ordinary": value})
				case "/consume":
					if r.Header.Get("X-Alias") != value {
						w.WriteHeader(http.StatusConflict)
						return
					}
					consumed.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			errConfig := dsl.ErrorHandlingConfig{MaxRetries: 1, RetryDelayMs: 1}
			group := dsl.Action{Name: "Retry classification", Type: "http_sequence", Params: map[string]any{
				"execution_mode": "concurrent", "max_concurrency": 2,
				"requests": []dsl.Action{
					{Name: "Neutral alias", Type: "GET", Target: "/alias", Extractors: []dsl.Extractor{{Name: "ordinary_alias", Source: "body", Type: "jsonpath", Expression: "$.ordinary", OnMiss: "fail"}}},
					{Name: "Retry producer", Type: "GET", Target: "/retry", Extractors: []dsl.Extractor{{Name: "access_token", Source: "body", Type: "jsonpath", Expression: "$.value"}}},
				},
			}}
			consume := dsl.Action{Name: "Consume exact alias", Type: "GET", Target: "/consume", Headers: map[string]string{"X-Alias": "{{var.ordinary_alias}}"}}
			resultsConfig := dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: "sampled", SampleRate: 1}}
			var public [][]byte
			if runner == "load" {
				vu := newConcurrentVU(t, server.URL)
				vu.Script.Config.Results = resultsConfig
				sink := &concurrentCaptureSink{}
				vu.ResultSink = sink
				result, err := vu.executeHTTPSequenceAction(context.Background(), group, errConfig)
				if err != nil || result == nil || !result.Success || len(sink.results) != 2 || vu.Variables["ordinary_alias"] != value || vu.Variables["access_token"] != "" {
					t.Fatal("retry changed joined outcomes or merged a discarded extraction")
				}
				result, err = vu.executeWithRetry(context.Background(), vu.resolveAction(consume), errConfig)
				if err != nil || result == nil || !result.Success {
					t.Fatal("retry classification changed the exact neutral runtime alias")
				}
				vu.recordResult(result, errConfig, dsl.LogConfig{})
				for _, retained := range sink.results {
					data, err := json.Marshal(retained)
					if err != nil {
						t.Fatal(err)
					}
					public = append(public, data)
				}
				data, err := json.Marshal(vu.replayFinalVariables())
				if err != nil {
					t.Fatal(err)
				}
				public = append(public, data)
			} else {
				script := &dsl.Script{Name: "Discarded retry", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: errConfig, Results: resultsConfig}, Actions: []dsl.Action{group, consume}}
				report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true, OnProgress: func(event ReplayProgressEvent) {
					data, err := json.Marshal(event)
					if err != nil {
						t.Fatal(err)
					}
					public = append(public, data)
				}})
				if err != nil || report == nil || !report.Success || len(report.Events) != 3 || report.FinalVariables["access_token"] != "" {
					t.Fatal("replay retry changed joined outcomes or merged a discarded extraction")
				}
				data, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				public = append(public, data)
			}
			if attempts.Load() != 2 || consumed.Load() != 1 {
				t.Fatal("fixture did not retry once and consume the exact alias")
			}
			for _, data := range public {
				if bytes.Contains(data, []byte(value)) {
					t.Fatal("discarded sensitive retry output exposed joined or subsequent neutral evidence")
				}
			}
		})
	}
}

// Wrap the real HTTP adapter only at its result boundary, where a protocol may
// mark incomplete extraction or return diagnostic data alongside an error.
type concurrentRetryReviewHTTP struct {
	*httpprotocol.HTTP
	mode             string
	value            string
	cancel           context.CancelFunc
	attempts         atomic.Int64
	redactorBindings atomic.Int64
	redact           protocol.TextRedactor
}

func (p *concurrentRetryReviewHTTP) SetRuntimeTextRedactor(redact protocol.TextRedactor) {
	p.redactorBindings.Add(1)
	p.redact = redact
}

func (p *concurrentRetryReviewHTTP) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	result, err := p.HTTP.Execute(ctx, action)
	if action.Target != "/retry" || err != nil || result == nil {
		return result, err
	}
	if p.attempts.Add(1) == 1 {
		switch p.mode {
		case "incomplete":
			result.ExtractionsIncomplete = true
		case "invalid":
			result.Extractions = map[string]string{"access_token": p.value}
			for i := 0; i < 5; i++ {
				result.Extractions[fmt.Sprintf("ordinary_%d", i)] = strings.Repeat("x", protocol.MaxExtractionValueBytes)
			}
		case "cancel":
			p.cancel()
		}
		return result, err
	}
	if p.mode == "error" {
		return nil, fmt.Errorf("retry transport error echoed %s", p.value)
	}
	if p.mode == "error_result" {
		return result, fmt.Errorf("retry diagnostic error echoed %s", p.value)
	}
	return result, err
}

func TestConcurrentHTTPCombinedReviewRetryClassificationFailureAndCancellation(t *testing.T) {
	for _, mode := range []string{"incomplete", "invalid", "oversized", "cancel", "error", "error_result"} {
		for _, runner := range []string{"load", "replay"} {
			t.Run(mode+"/"+runner, func(t *testing.T) {
				value := "silver-fern-73184"
				if mode == "oversized" {
					value = strings.Repeat("w", protocol.MaxExtractionValueBytes+1)
				}
				var traffic atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/alias" {
						_ = json.NewEncoder(w).Encode(map[string]string{"ordinary": value})
						return
					}
					if traffic.Add(1) == 1 {
						w.WriteHeader(http.StatusServiceUnavailable)
						_ = json.NewEncoder(w).Encode(map[string]string{"value": value})
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"ordinary": value})
				}))
				defer server.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				vu := newConcurrentVU(t, server.URL)
				p := &concurrentRetryReviewHTTP{HTTP: vu.Proto.(*observedConcurrentHTTP).HTTP, mode: mode, value: value, cancel: cancel}
				vu.Proto = p
				vu.bindRuntimeTextRedactor()
				vu.Script.Config.Results = dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: "sampled", SampleRate: 1}}
				group := dsl.Action{Name: "Failing retry classification", Type: "http_sequence", Params: map[string]any{
					"execution_mode": "concurrent", "max_concurrency": 1,
					"requests": []dsl.Action{
						{Name: "Earlier echo", Type: "GET", Target: "/alias"},
						{Name: "Retry producer", Type: "GET", Target: "/retry", Extractors: []dsl.Extractor{{Name: "access_token", Source: "body", Type: "jsonpath", Expression: "$.value"}}},
					},
				}}
				sink := &concurrentCaptureSink{}
				vu.ResultSink = sink
				var public [][]byte
				errConfig := dsl.ErrorHandlingConfig{MaxRetries: 1, RetryDelayMs: 1}
				if runner == "load" {
					result, err := vu.executeHTTPSequenceAction(ctx, group, errConfig)
					if result == nil || result.Success || (mode == "cancel" && !errors.Is(err, context.Canceled)) || (mode != "cancel" && err != nil) {
						t.Fatal("classification/error boundary lost its unsuccessful outcome")
					}
					data, _ := json.Marshal(result)
					public = append(public, data)
				} else {
					vu.Script.Config.ErrorHandling = errConfig
					report := &ReplayReport{ReplaySessionID: "synthetic-retry-boundary", Success: true}
					runReplayPhase(ctx, vu, []dsl.Action{group}, "action", true, true, protocol.ThinkTimeConfig{}, report, vu.guardedReplayProgress(report, func(event ReplayProgressEvent) {
						data, _ := json.Marshal(event)
						public = append(public, data)
					}), nil)
					if report.Success {
						t.Fatal("replay classification/error boundary claimed success")
					}
					data, _ := json.Marshal(report)
					public = append(public, data)
				}
				wantAttempts := int64(1)
				if mode == "error" || mode == "error_result" {
					wantAttempts = 2
				}
				if traffic.Load() != wantAttempts || p.redactorBindings.Load() != 1 {
					t.Fatal("worker retried an unavailable classification or rebound the shared protocol redactor")
				}
				unavailable := mode == "incomplete" || mode == "invalid" || mode == "oversized"
				if (vu.runtimeCredentialRedactionErr != nil) != unavailable {
					t.Fatal("classification failure did not establish the expected sticky publication policy")
				}
				if !unavailable && scrubSensitiveTextWithConfig(value, vu.RedactionConfig) != redactedPayloadValue {
					t.Fatal("cancelled or diagnostic retry discarded the admitted parent history")
				}
				if p.redact(value) == value {
					t.Fatal("retained protocol callback did not observe the parent's joined publication policy")
				}
				for _, retained := range sink.results {
					data, _ := json.Marshal(retained)
					public = append(public, data)
				}
				data, _ := json.Marshal(vu.replayFinalVariables())
				public = append(public, data)
				probe := []byte(value[:min(128, len(value))])
				for _, data := range public {
					if bytes.Contains(data, probe) {
						t.Fatal("retry classification boundary exposed an earlier echo or opaque error")
					}
				}
			})
		}
	}
}

func TestConcurrentHTTPCombinedReviewRetryHistoryBudgets(t *testing.T) {
	t.Run("worker cumulative and atomic", func(t *testing.T) {
		const value = "lilac-38465"
		history := &httpRetryClassificationHistory{allowance: len(value) + 64}
		vu := &VUser{}
		result := &protocol.Result{Extractions: map[string]string{"access_token": value}}
		if err := history.observe(vu, result); err != nil {
			t.Fatal(err)
		}
		if err := history.observe(vu, result); err != nil || history.used != history.allowance {
			t.Fatal("repeated observations consumed the allowance twice")
		}
		result.Extractions = map[string]string{"access_token": "second-463", "refresh_token": "third-812"}
		if history.observe(vu, result) == nil || vu.runtimeCredentialRedactionErr == nil || !reflect.DeepEqual(history.observedValues(), []string{value}) || history.used != history.allowance {
			t.Fatal("history overflow admitted a prefix or did not fail closed")
		}
	})
	t.Run("group allowances", func(t *testing.T) {
		const count = 256
		allowance := maxConcurrentRetryClassificationBytes / count
		value := strings.Repeat("r", protocol.MaxExtractionValueBytes)
		var total int
		for i := 0; i < count; i++ {
			history := &httpRetryClassificationHistory{allowance: allowance}
			vu := &VUser{}
			for attempt := 0; attempt < 4; attempt++ {
				observed := fmt.Sprintf("%03d/%d/", i, attempt) + value[8:]
				err := history.observe(vu, &protocol.Result{Extractions: map[string]string{"access_token": observed}})
				if (err != nil) != (attempt == 3) {
					t.Fatal("attempt history bypassed its fixed source-ordinal group allowance")
				}
			}
			total += history.used
		}
		if total > maxConcurrentRetryClassificationBytes {
			t.Fatal("joined histories exceeded the cumulative group ceiling")
		}
	})
	t.Run("joined dynamic capacity", func(t *testing.T) {
		values := make([]string, maxRuntimeSecretRedactionValues-1)
		for i := range values {
			values[i] = fmt.Sprintf("review-inherited-%08d", i)
		}
		parent := &VUser{}
		if err := parent.replaceRuntimeCredentialScope("extraction:observed", values); err != nil {
			t.Fatal(err)
		}
		base := parent.RedactionConfig
		children := make([]preparedHTTPChild, 2)
		for i := range children {
			history := &httpRetryClassificationHistory{base: base, allowance: maxConcurrentRetryClassificationBytes / len(children)}
			worker := &VUser{RedactionConfig: base}
			if err := history.observe(worker, &protocol.Result{Extractions: map[string]string{"access_token": fmt.Sprintf("review-fresh-%d", i)}}); err != nil {
				t.Fatal("individually supported worker matcher was rejected")
			}
			children[i].retrySensitiveValues = history.observedValues()
		}
		if _, err := parent.admitConcurrentRetryClassifications(children); err == nil || parent.runtimeCredentialRedactionErr == nil || !reflect.DeepEqual(parent.runtimeCredentialScopes["extraction:observed"], values) || parent.RedactionConfig != base {
			t.Fatal("joined worker observations bypassed dynamic matcher capacity or committed a partial history")
		}
	})
	t.Run("error without final result", func(t *testing.T) {
		const value = "amber-rose-5372"
		config, err := boundConcurrentCompletedResult(nil, 2, nil, []string{value})
		if err != nil || scrubSensitiveTextWithConfig(value, config) != redactedPayloadValue {
			t.Fatal("an absent final result lost the discarded attempt's error projection")
		}
	})
}
