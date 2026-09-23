package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestResourceEvidenceScrubbingBoundsAndClone(t *testing.T) {
	result := &protocol.Result{Snapshot: &protocol.ErrorSnapshot{Resources: []protocol.ResourceSnapshot{{RequestURL: "https://example.test/value-secret", RequestHeaders: map[string]string{"X-Fixture": "value-secret"}, ResponseHeaders: map[string]string{"X-Fixture": "value-secret"}, ResponseBody: "value-secret", ResponseBodyState: "captured"}}}}
	scrubSensitiveResult(result, WithSecretValueRedaction(nil, []string{"value-secret"}))
	raw, _ := json.Marshal(result.Snapshot)
	if strings.Contains(string(raw), "value-secret") {
		t.Fatal("resource bypassed runtime redaction")
	}
	if result.Snapshot.Resources[0].ResponseBodyState != "redacted" {
		t.Fatal("redaction state lost")
	}
	event := cloneReplayActionEvent(ReplayActionEvent{Snapshot: result.Snapshot})
	event.Snapshot.Resources[0].RequestHeaders["X-Fixture"] = "changed"
	if result.Snapshot.Resources[0].RequestHeaders["X-Fixture"] == "changed" {
		t.Fatal("clone aliases resource headers")
	}
	for i := 0; i < 100; i++ {
		result.Snapshot.Resources = append(result.Snapshot.Resources, protocol.ResourceSnapshot{RequestURL: "https://example.test", ResponseBody: strings.Repeat("x", 1000)})
	}
	bounded, size := boundedErrorSnapshot(result.Snapshot, 2048)
	if size > 2048 || bounded.OmittedResources == 0 {
		t.Fatalf("resources escaped aggregate bounds: %d %+v", size, bounded)
	}
}

func TestResourceCaptureHintPreservesOffAndMissing(t *testing.T) {
	for _, tc := range []struct {
		mode    string
		include *bool
		want    bool
	}{{"sampled", boolPointer(true), true}, {"sampled", boolPointer(false), false}, {"sampled", nil, false}, {"off", boolPointer(true), false}, {"errors", boolPointer(true), true}} {
		vu := &VUser{Script: &dsl.Script{Config: dsl.ScriptConfig{Results: dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: tc.mode, SampleRate: 1, IncludeResources: tc.include}}}}}
		a := vu.resolveAction(dsl.Action{Name: "fixture", Type: "http_request", Target: "https://example.test"})
		if a.CaptureResourceDetails != tc.want {
			t.Fatalf("mode=%s include=%v got=%v", tc.mode, tc.include, a.CaptureResourceDetails)
		}
	}
}
func boolPointer(v bool) *bool { return &v }

func TestResourceCaptureReplayPreservesRequestsChecksAndTransactions(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, r.Method+" "+r.URL.Path+" "+string(body))
		if r.URL.Path == "/page" {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<img src="/resource">ok`)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "resource")
	}))
	defer server.Close()
	var baseline []string
	var outcomes []bool
	for _, capture := range []bool{false, true} {
		requests = nil
		mode := "errors"
		if capture {
			mode = "sampled"
		}
		script := &dsl.Script{Name: "capture parity", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{
			BrowserEmulation: &dsl.BrowserEmulationConfig{Cache: &dsl.BrowserCacheSimulationConfig{DownloadNonHTML: boolPointer(true)}},
			Results:          dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: mode, SampleRate: 1, IncludeResources: boolPointer(true)}},
		}, Actions: []dsl.Action{
			sourceTransactionCall("lr_start_transaction", "page"),
			{Name: "page", Type: "http_request", Method: "POST", Target: server.URL + "/page", Body: "payload", Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}}},
			sourceTransactionCall("lr_end_transaction", "page", "LR_AUTO"),
		}}
		report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true})
		if err != nil || !report.Success {
			t.Fatalf("replay: %+v %v", report, err)
		}
		var current []bool
		found := false
		for _, e := range report.Events {
			current = append(current, e.Success)
			if e.ActionName == "page" && e.ActionType == "http_request" {
				found = true
				if capture && (e.Snapshot == nil || len(e.Snapshot.Resources) != 1) {
					t.Fatalf("missing resources: %+v", e.Snapshot)
				}
				if !capture && e.Snapshot != nil {
					t.Fatal("disabled all-exchange capture retained success")
				}
			}
		}
		if !found {
			t.Fatal("missing parent event")
		}
		if !capture {
			baseline = append([]string(nil), requests...)
			outcomes = current
		} else if !reflect.DeepEqual(baseline, requests) || !reflect.DeepEqual(outcomes, current) {
			t.Fatal("capture changed traffic/checks/transaction outcomes")
		}
	}
}

func TestResourceCaptureDoesNotDisableIndependentErrorStore(t *testing.T) {
	for _, mode := range []string{"off", "errors", "sampled"} {
		for _, snapshotOnError := range []bool{false, true} {
			store := NewSnapshotStore(128)
			vu := &VUser{SnapshotStore: store, Script: &dsl.Script{Config: dsl.ScriptConfig{Results: dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: mode}}}}}
			vu.handleSnapshot(&protocol.Result{Success: false, Snapshot: &protocol.ErrorSnapshot{RequestURL: "https://example.test/failure", Resources: []protocol.ResourceSnapshot{{RequestURL: "https://example.test/resource"}}}}, dsl.ErrorHandlingConfig{SnapshotOnError: snapshotOnError})
			if (len(store.Snapshots()) == 1) != snapshotOnError {
				t.Fatalf("mode %s changed independent error policy", mode)
			}
		}
	}
}

func TestReplayResourceEvidenceDoesNotReusePreviousFailureAtStoreLimit(t *testing.T) {
	name := registerReplayProtocol(t, func(a *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{Success: false, ErrorMessage: "fixture failure", Snapshot: &protocol.ErrorSnapshot{RequestURL: a.Name, Resources: []protocol.ResourceSnapshot{{RequestURL: a.Name + "/resource", ResponseBodyState: "empty"}}}}, nil
	})
	script := &dsl.Script{Name: "retention", Version: "1", Protocol: name, Config: dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: boolPointer(true), SnapshotOnError: true}}}
	for i := 0; i < 130; i++ {
		script.Actions = append(script.Actions, dsl.Action{Name: fmt.Sprintf("request-%03d", i), Type: "request", Method: "GET", Target: "https://example.test"})
	}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Events) != 130 {
		t.Fatalf("events=%d", len(report.Events))
	}
	for _, event := range report.Events {
		if event.Snapshot == nil || event.Snapshot.RequestURL != event.ActionName || len(event.Snapshot.Resources) != 1 || event.Snapshot.Resources[0].RequestURL != event.ActionName+"/resource" {
			t.Fatalf("cross-action evidence for %s: %+v", event.ActionName, event.Snapshot)
		}
	}
}

func TestResourceProjectionReportsEveryRetentionLoss(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resource protocol.ResourceSnapshot
	}{
		{"url", protocol.ResourceSnapshot{RequestURL: strings.Repeat("u", 9000), ResponseBodyState: "empty"}},
		{"request headers", protocol.ResourceSnapshot{RequestHeaders: map[string]string{"X-Large": strings.Repeat("h", 20000)}, ResponseBodyState: "empty"}},
		{"response headers and body", protocol.ResourceSnapshot{ResponseHeaders: map[string]string{"X-Large": strings.Repeat("h", 20000)}, ResponseBody: "fixture", ResponseBodyState: "captured"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, size := boundedErrorSnapshot(&protocol.ErrorSnapshot{Resources: []protocol.ResourceSnapshot{tc.resource}}, 512)
			if size > 512 || len(got.Resources) != 1 || !got.Resources[0].Truncated {
				t.Fatalf("unreported loss: size=%d snapshot=%+v", size, got)
			}
			r := got.Resources[0]
			if tc.resource.ResponseBody != "" && r.ResponseBody == "" && r.ResponseBodyState != "not_captured" {
				t.Fatalf("misleading body state: %+v", r)
			}
		})
	}
}

func TestReplayMessageDetailLimitsKeepExecutionEvents(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items int
		total int64
		want  int
	}{
		{"items", 2, 8192, 2}, {"bytes", 10, 1024, 2}, {"sub-envelope budget", 10, 128, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				io.WriteString(w, strings.Repeat("x", 1024))
			}))
			defer server.Close()
			script := &dsl.Script{Name: "bounded replay", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{Results: dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: "sampled", SampleRate: 1, MaxItems: tc.items, MaxItemBytes: 512, MaxTotalBytes: tc.total}}}}
			for i := 0; i < 5; i++ {
				script.Actions = append(script.Actions, dsl.Action{Name: fmt.Sprintf("request-%d", i), Type: "http_request", Method: "GET", Target: server.URL})
			}
			report, err := ReplayScript(context.Background(), script, ReplayOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if requests != 5 || len(report.Events) != 5 || !report.Success {
				t.Fatalf("capture changed execution: requests=%d report=%+v", requests, report)
			}
			retained := 0
			for _, event := range report.Events {
				if event.Snapshot != nil {
					retained++
					if len(event.Snapshot.ResponseBody) > 256 {
						t.Fatal("per-item bound ignored")
					}
				} else if event.ResponseBodyState != "not_retained" {
					t.Fatalf("missing omission state: %+v", event)
				}
			}
			if retained != tc.want || len(report.Snapshots) != tc.want || report.TruncatedEventEvidenceCount != 5-tc.want {
				t.Fatalf("retained=%d snapshots=%d truncated=%d", retained, len(report.Snapshots), report.TruncatedEventEvidenceCount)
			}
		})
	}
}
