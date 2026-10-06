package sample

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
)

// The shapes came from aggregate inspection of an operator-supplied capture.
// Every URL, credential, body, identity and time below is newly invented. The
// original capture is deliberately not a repository fixture or test input.
func TestHARSampleShapeUsesLogicalStorageAndPreservesCaptureGaps(t *testing.T) {
	const vendorVersion = "HTTP/synthetic-export-9.9"
	const rawText = `{"record":"synthetic-raw","access_token":"synthetic-access-123456"}`
	const base64Text = `{"record":"synthetic-base64"}`
	binary := []byte{0, 1, 0xff, 0x1f, 0x8b, 8}
	entry := func(path string, content any) map[string]any {
		return map[string]any{
			"startedDateTime": "2026-01-02T03:04:05.123-05:00", "time": 14.5, "pageref": "synthetic-page",
			"timings":  map[string]any{"blocked": -1, "dns": -1, "connect": -17.5, "send": -1, "wait": -1, "receive": 14.5, "ssl": -1},
			"request":  map[string]any{"method": "GET", "url": "https://fixture.invalid/" + path, "httpVersion": vendorVersion, "headers": []any{map[string]any{"name": "Authorization", "value": "Bearer synthetic-credential"}}},
			"response": map[string]any{"status": 200, "httpVersion": vendorVersion, "headers": []any{map[string]any{"name": "Content-Encoding", "value": "gzip"}, map[string]any{"name": "Content-Length", "value": "7"}, map[string]any{"name": "Content-Type", "value": "application/json"}}, "content": content},
		}
	}
	tunnel := entry("tunnel", map[string]any{"text": "synthetic proxy setup"})
	tunnel["request"].(map[string]any)["method"] = "CONNECT"
	unavailable := entry("unavailable", map[string]any{"text": "synthetic rejected private content"})
	unavailable[requestimport.HARResponseContentProvenanceKey] = map[string]any{"version": 1, "captured": "wire", "stored": "unavailable", "state": "unavailable", "reason_code": "response_content_truncated"}
	payload, err := json.Marshal(map[string]any{"log": map[string]any{"version": "1.2", "creator": map[string]any{"name": "Synthetic vendor capture", "version": "fixture"}, "entries": []any{
		tunnel,
		entry("raw", map[string]any{"text": rawText, "mimeType": "application/json"}),
		entry("base64", map[string]any{"text": base64.StdEncoding.EncodeToString([]byte(base64Text)), "encoding": "base64", "mimeType": "application/json"}),
		entry("binary", map[string]any{"text": base64.StdEncoding.EncodeToString(binary), "encoding": "base64", "mimeType": "application/octet-stream"}),
		entry("missing", map[string]any{"text": nil}),
		unavailable,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	original := string(payload)
	var diagnostics diag.Collector
	exchanges, _, filtered, skipped, err := parseHAR(original, true, &diagnostics)
	if err != nil || len(exchanges) != 5 || filtered != 0 || skipped != 1 {
		t.Fatalf("sample-shaped accounting: accepted=%d filtered=%d skipped=%d err=%v", len(exchanges), filtered, skipped, err)
	}
	wantBodies := []string{rawText, base64Text, "", "", ""}
	wantReasons := []string{"", "", "", "response_content_unavailable", "response_content_truncated"}
	for index, ex := range exchanges {
		capture := ex.Response.ContentCapture
		if ex.SourceEntryID != index+2 || ex.Response.Body != wantBodies[index] || capture.ReasonCode != wantReasons[index] {
			t.Fatalf("entry %d: source=%d body=%q capture=%+v", index, ex.SourceEntryID, ex.Response.Body, capture)
		}
		versionCapture, _ := ex.Metadata["http_version_capture"].(map[string]any)
		if ex.HTTPVersion != "" || versionCapture["reason_code"] != "unsupported_http_version" || versionCapture["request_state"] != "unsupported" {
			t.Fatal("unsupported vendor version became an override or lost its quality observation")
		}
		if _, gated := ex.Metadata[requestimport.ImportHTTPVersionFidelityMetadataKey]; gated {
			t.Fatal("unsupported textual version was promoted from a quality gap to a version gate")
		}
		if !ex.HasStartedAt || ex.StartedAt.UTC().Format(time.RFC3339Nano) != "2026-01-02T08:04:05.123Z" {
			t.Fatal("source timestamp did not retain its timezone and precision")
		}
		// Legacy timings are retained source metadata, not qualified analytical
		// phases. Import must not silently replace these source gaps with zero.
		if !reflect.DeepEqual(ex.Metadata["har.timings"], map[string]any{"blocked": float64(-1), "dns": float64(-1), "connect": -17.5, "send": float64(-1), "wait": float64(-1), "receive": 14.5, "ssl": float64(-1)}) {
			t.Fatal("unknown or invalid source timings were silently rewritten")
		}
		if capture.Complete() && capture.Captured != "har_decoded" {
			t.Fatal("logical HAR storage was relabeled as wire evidence")
		}
		if len(ex.Response.HeaderList) != 3 || ex.Response.Headers["Content-Encoding"] != "gzip" {
			t.Fatal("exact captured framing was removed from the source exchange")
		}
		metadata, err := json.Marshal(ex.Metadata)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(metadata), vendorVersion) || strings.Contains(string(metadata), "synthetic rejected private content") {
			t.Fatal("unsupported version or rejected body entered active metadata")
		}
	}
	if exchanges[4].Metadata["execution_blocker"] != "import_review_required" {
		t.Fatal("explicit unavailable response-content provenance lost its durable review gate")
	}
	if capture := exchanges[2].Response.ContentCapture; !capture.Complete() || capture.BodyKind != "binary" || capture.DecodedSizeBytes != len(binary) {
		t.Fatal("binary resource lost its exact logical size or became application text")
	}
	if !exchanges[0].Response.ContentCapture.Complete() || exchanges[0].Response.ContentCapture.DecodedSizeBytes != len(rawText) || exchanges[1].Response.ContentCapture.DecodedSizeBytes != len(base64Text) {
		t.Fatal("stale wire framing caused a second decompression or wrong logical size")
	}
	sources, err := HARSourceObservations(original, true)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := HARCorrelationBaseline(original, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 5 || len(baseline) != 5 {
		t.Fatal("private source or replay-baseline projection silently lost application entries")
	}
	for index, source := range sources {
		if source.ID != index+2 || baseline[index].ID != index || source.ResponseBody != wantBodies[index] || source.Method != "GET" {
			t.Fatal("source-aware and replay-baseline projections disagreed")
		}
		if source.RequestHeaders["Authorization"] != "Bearer synthetic-credential" {
			t.Fatal("private protected observation lost source request identity")
		}
		if index < 3 {
			for name := range source.ResponseHeaders {
				if strings.EqualFold(name, "Content-Encoding") || strings.EqualFold(name, "Content-Length") {
					t.Fatal("logical correlation snapshot retained misleading wire framing")
				}
			}
		}
	}
	includeStatic := true
	converted, err := Convert(ImportRequest{Format: "har", Content: original, Options: ConvertOptions{IncludeStaticAssets: &includeStatic}})
	if err != nil {
		t.Fatal(err)
	}
	if converted.Stats.TotalRequests != 6 || converted.Stats.SkippedRequests != 1 || converted.Stats.ConvertedActions != 5 {
		t.Fatalf("public converter accounting: %+v", converted.Stats)
	}
	if string(payload) != original {
		t.Fatal("source storage changed")
	}
}
