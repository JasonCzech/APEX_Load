package sample

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
)

func recorderHAR(t *testing.T, entries ...map[string]any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"log": map[string]any{
		"version": "1.2",
		"creator": map[string]any{"name": "APEX Load Recorder", "version": "2.0"},
		"entries": entries,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func recorderEntry(started, method, url string, status int, mime, body string, extra map[string]any) map[string]any {
	entry := map[string]any{
		"startedDateTime": started,
		"time":            10,
		"request":         map[string]any{"method": method, "url": url, "httpVersion": "HTTP/1.1", "headers": []any{}},
		"response": map[string]any{
			"status": status, "httpVersion": "HTTP/1.1",
			"headers": []any{map[string]any{"name": "Content-Type", "value": mime}},
			"content": map[string]any{"mimeType": mime, "text": body, "size": len(body)},
		},
	}
	for key, value := range extra {
		entry[key] = value
	}
	return entry
}

func TestRecorderHAREntryFields(t *testing.T) {
	content := recorderHAR(t,
		recorderEntry("2026-01-01T00:00:00Z", "GET", "https://shop.test/home", 200, "text/html", "<title>Home</title>", map[string]any{
			"_apexload_static": false, "_apexload_step_name": "Open home", "_apexload_transaction": "Browse", "_apexload_think_time_ms": 0,
		}),
		// A recorder-classified static request that the HAR heuristics would
		// keep, and a page that the heuristics would drop.
		recorderEntry("2026-01-01T00:00:01Z", "GET", "https://shop.test/api/config", 200, "application/json", `{}`, map[string]any{"_apexload_static": true, "_apexload_think_time_ms": 0}),
		recorderEntry("2026-01-01T00:00:09Z", "POST", "https://shop.test/upload.js", 200, "text/plain", "ok", map[string]any{
			"_apexload_static": false, "_apexload_think_time_ms": 4200, "_apexload_request_body_truncated": true, "_apexload_request_body_bytes": 5242880,
		}),
	)
	result, err := Convert(ImportRequest{Format: "har", Content: content, Options: ConvertOptions{OmitSourceReview: true}})
	if err != nil {
		t.Fatal(err)
	}
	actions := result.Script.Actions
	if len(actions) != 2 || !strings.HasSuffix(actions[1].Target, "/upload.js") {
		t.Fatalf("the recorder's classification decides what is static: %+v", actions)
	}
	if actions[0].Name != "01 - Open home" || actions[0].Metadata["source_transaction"] != "Browse" {
		t.Fatalf("step and transaction names = %q %v", actions[0].Name, actions[0].Metadata["source_transaction"])
	}
	if !actions[0].ThinkTimeAfter || actions[0].ThinkTime.MeanMs != 4200 {
		t.Fatalf("the recorded pause before the next request = %+v", actions[0].ThinkTime)
	}
	findings, err := importreview.FindingsFromScript(result.Script)
	if err != nil {
		t.Fatal(err)
	}
	gated := false
	for _, finding := range findings {
		gated = gated || finding.ReasonCode == requestimport.RecordedBodyTruncatedReasonCode
	}
	if !gated {
		t.Fatalf("a truncated recorded body must be gated: %+v", findings)
	}
	if result.SourceReview != nil {
		t.Fatal("OmitSourceReview must skip the source review")
	}
	if len(result.Baseline) != 2 || result.Baseline[1].ID != 1 || !strings.HasSuffix(result.Baseline[1].RequestURL, "/upload.js") {
		t.Fatalf("baseline = %+v", result.Baseline)
	}
}

func TestHighConfidenceOnlyKeepsLowConfidenceCorrelationsForReview(t *testing.T) {
	content := recorderHAR(t,
		recorderEntry("2026-01-01T00:00:00Z", "POST", "https://shop.test/orders", 200, "application/json", `{"order_id":1234}`, nil),
		recorderEntry("2026-01-01T00:00:01Z", "GET", "https://shop.test/orders/1234", 200, "application/json", `{}`, nil),
	)
	all, err := Convert(ImportRequest{Format: "har", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Correlations) == 0 || !all.Correlations[0].PreApplied || all.Correlations[0].Confidence >= 0.8 {
		t.Fatalf("setup: want an applied low-confidence correlation, got %+v", all.Correlations)
	}
	if all.Correlations[0].FoundInEntry != 1 || len(all.Correlations[0].UsedInEntries) != 1 || all.Correlations[0].UsedInEntries[0] != 2 {
		t.Fatalf("source entries = %+v", all.Correlations[0])
	}
	strict, err := Convert(ImportRequest{Format: "har", Content: content, Options: ConvertOptions{HighConfidenceOnly: true}})
	if err != nil {
		t.Fatal(err)
	}
	if strict.Correlations[0].PreApplied || strict.Correlations[0].ReviewAction != "manual_review_required" || len(strict.Script.Actions[0].Extractors) != 0 || strict.Script.Actions[1].Target != "/orders/1234" {
		t.Fatalf("a low-confidence correlation must stay for review: %+v %+v", strict.Correlations[0], strict.Script.Actions)
	}
}

func TestHAREpochSecondsReadsRFC3339(t *testing.T) {
	if got := harEpochSeconds("2026-01-01T00:00:01.5Z"); got.UnixMilli() != 1767225601500 {
		t.Fatalf("RFC 3339 frame time = %v", got)
	}
	if got := harEpochSeconds(1767225601.5); got.UnixMilli() != 1767225601500 {
		t.Fatalf("epoch frame time = %v", got)
	}
}

// Client timestamps get a fresh value whether or not the async scan runs,
// and the import review carries the same diagnostic either way.
func TestRecordedClientTimestampsWithAndWithoutAsyncScan(t *testing.T) {
	content := recorderHAR(t,
		recorderEntry("2026-01-01T00:00:00Z", "GET", "https://shop.test/home", 200, "text/html", "<p>home</p>", nil),
		recorderEntry("2026-01-01T00:00:02Z", "GET", "https://shop.test/data?_=1767225602000", 200, "application/json", `{}`, nil),
	)
	for _, asyncScan := range []bool{true, false} {
		scan := asyncScan
		result, err := Convert(ImportRequest{Format: "har", Content: content, Options: ConvertOptions{OmitSourceReview: true, AsyncScan: &scan}})
		if err != nil {
			t.Fatal(err)
		}
		if target := result.Script.Actions[1].Target; !strings.Contains(target, "{{fn.timestamp.unix_ms}}") {
			t.Fatalf("async scan %v: target = %q", asyncScan, target)
		}
		diagnostics := 0
		for _, item := range result.Unsupported {
			if item.Code == ClientTimestampsDiagnosticCode {
				diagnostics++
			}
		}
		if diagnostics != 1 {
			t.Fatalf("async scan %v: %d client timestamp diagnostics, want 1", asyncScan, diagnostics)
		}
	}
}
