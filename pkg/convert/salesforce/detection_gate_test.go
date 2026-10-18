package salesforce

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func detectionFixture(host, body string) (*dsl.Script, []byte) {
	script := &dsl.Script{Name: "capture", Protocol: "http", GeneratedFrom: "har", Config: dsl.ScriptConfig{BaseURL: "https://" + host}, Actions: []dsl.Action{{Type: "request", Method: "GET", Target: "/services/data/v60.0/query"}}}
	data, _ := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{map[string]any{"request": map[string]any{"method": "GET", "url": "https://" + host + "/services/data/v60.0/query"}, "response": map[string]any{"content": map[string]any{"text": body}}}}}})
	return script, data
}

func TestAnalyzeDetectionGateSkipsUnrelatedReviewWork(t *testing.T) {
	// A generic path matters: a recognized Salesforce endpoint is detection even
	// when the host belongs to a private reverse proxy.
	script, data := detectionFixture("example.test", `{"token":"abcdef123456"}`)
	script.Actions[0].Target = "/orders"
	data = []byte(strings.ReplaceAll(string(data), "/services/data/v60.0/query", "/orders"))
	before, _ := json.Marshal(script)
	gated, err := Analyze(script, AnalyzeOptions{HARData: data, RequireDetected: true, BuildNative: true})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gated, &Review{}) {
		t.Fatalf("negative detection performed review work: %#v", gated)
	}
	explicit, err := Analyze(script, AnalyzeOptions{HARData: data})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Detected || len(explicit.Transactions) == 0 || explicit.HARMatchCoverage.MatchedRequests != 1 || explicit.CorrelationConfidence == "" {
		t.Fatalf("explicit review lost its established behavior: %#v", explicit)
	}
	optional, err := Analyze(script, AnalyzeOptions{HARData: data, BuildNativeIfDetected: true})
	if err != nil || optional.NativePreview != nil {
		t.Fatalf("optional native review built an unrelated preview: %#v, %v", optional, err)
	}
	after, _ := json.Marshal(script)
	if string(before) != string(after) {
		t.Fatal("analysis mutated its input")
	}
}

func TestAnalyzeDetectionGatePreservesSalesforceReview(t *testing.T) {
	for _, host := range []string{"acme.my.salesforce.com", "proxy.example.test"} {
		t.Run(host, func(t *testing.T) {
			script, data := detectionFixture(host, `{"records":[],"done":true}`)
			full, err := Analyze(script, AnalyzeOptions{HARData: data, BuildNative: true})
			if err != nil {
				t.Fatal(err)
			}
			gated, err := Analyze(script, AnalyzeOptions{HARData: data, RequireDetected: true, BuildNativeIfDetected: true})
			if err != nil {
				t.Fatal(err)
			}
			if !gated.Detected || !reflect.DeepEqual(full, gated) {
				t.Fatalf("detection changed Salesforce review\nfull=%#v\ngated=%#v", full, gated)
			}
		})
	}
}

func TestNativePreviewRetainsInputProvenance(t *testing.T) {
	for _, tc := range []struct{ source, want string }{{"har", "har"}, {"salesforce_har", "har"}, {"browser_recording_har", "browser_recording_har"}, {"enterprise_script_archive", "enterprise_script_archive+har"}} {
		t.Run(tc.source, func(t *testing.T) {
			script, data := detectionFixture("acme.my.salesforce.com", `{"records":[],"done":true}`)
			review, err := Analyze(script, AnalyzeOptions{HARData: data, BuildNative: true, Source: tc.source})
			if err != nil {
				t.Fatal(err)
			}
			if got := review.NativePreview.Script.GeneratedFrom; got != tc.want {
				t.Fatalf("generated_from=%q, want %q", got, tc.want)
			}
			if script.GeneratedFrom != "har" {
				t.Fatal("native preview changed source provenance")
			}
		})
	}
}

func BenchmarkAnalyzeUnrelatedCapture(b *testing.B) {
	script, data := detectionFixture("example.test", strings.Repeat("ordinary response payload ", 1<<14))
	script.Actions[0].Target = "/orders"
	data = []byte(strings.ReplaceAll(string(data), "/services/data/v60.0/query", "/orders"))
	for _, gate := range []bool{false, true} {
		name := "explicit-full-review"
		if gate {
			name = "detected-only-review"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				if _, err := Analyze(script, AnalyzeOptions{HARData: data, RequireDetected: gate}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestOptionalNativeFailureRetainsCompletedReview(t *testing.T) {
	script, data := detectionFixture("acme.my.salesforce.com", `{"records":[],"done":true}`)
	script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{"unencodable": make(chan int)}}}
	review, err := Analyze(script, AnalyzeOptions{HARData: data, BuildNativeIfDetected: true})
	if err == nil || review == nil || !review.Detected || review.NativePreview != nil || review.HARMatchCoverage.MatchedRequests != 1 {
		t.Fatalf("optional preparation lost review: %#v, %v", review, err)
	}
	if review, err = Analyze(script, AnalyzeOptions{HARData: data, BuildNative: true}); err == nil || review != nil {
		t.Fatalf("explicit native failure changed contract: %#v, %v", review, err)
	}
}

func TestDetectionGateStillValidatesHARAndIgnoresMismatchedDocument(t *testing.T) {
	script, data := detectionFixture("example.test", `{}`)
	script.Actions[0].Target = "/orders"
	data = []byte(strings.ReplaceAll(string(data), "/services/data/v60.0/query", "/orders"))
	doc, err := sample.ParseHARDocument(string(data))
	if err != nil {
		t.Fatal(err)
	}
	_, salesforceData := detectionFixture("acme.my.salesforce.com", `{}`)
	review, err := Analyze(script, AnalyzeOptions{HARData: salesforceData, Document: doc, RequireDetected: true})
	if err != nil || !review.Detected {
		t.Fatalf("foreign document suppressed source detection: %#v, %v", review, err)
	}
	if _, err = Analyze(script, AnalyzeOptions{HARData: []byte(`{"log":`), Document: doc, RequireDetected: true}); err == nil {
		t.Fatal("negative detection accepted malformed HAR")
	}
}
