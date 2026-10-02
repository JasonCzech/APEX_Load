package sample

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
)

func TestHARBOMAndMalformedInput(t *testing.T) {
	const capture = `{"log":{"entries":[{"request":{"method":"GET","url":"https://example.invalid/page"},"response":{"status":200,"content":{"text":"{}"}}}]}}`
	result, err := Convert(ImportRequest{Format: "har", Content: "\uFEFF" + capture})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.TotalRequests != 1 || result.Stats.ConvertedActions != 1 {
		t.Fatalf("stats: %+v", result.Stats)
	}
	if result.SourceReview == nil || result.SourceReview.SourceFiles[0].Content != "\uFEFF"+capture {
		t.Fatal("original source bytes were changed")
	}
	for _, input := range []string{"\uFEFF", "\uFEFF" + capture[:len(capture)-4], " \uFEFF" + capture, "\uFEFF\uFEFF" + capture} {
		if _, err := Convert(ImportRequest{Format: "har", Content: input}); err == nil {
			t.Fatal("accepted incomplete JSON or misplaced BOM")
		}
	}
}

func TestHARProxyTunnelsExcludedWithSourceOrdinals(t *testing.T) {
	entry := func(method, path, body, mime string) any {
		return map[string]any{"request": map[string]any{"method": method, "url": "https://example.invalid" + path}, "response": map[string]any{"status": 200, "content": map[string]any{"text": body, "mimeType": mime}}}
	}
	payload, err := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{
		entry("CONNECT", "/tunnel", `{"access_token":"proxy-only-123456"}`, "application/json"),
		entry("GET", "/asset.js", "script", "application/javascript"),
		entry("GET", "/page", "{}", "application/json"),
		entry("POST", "/use?token=proxy-only-123456", "{}", "application/json"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, includeStatic := range []bool{false, true} {
		var diagnostics diag.Collector
		exchanges, _, filtered, skipped, err := parseHAR(string(payload), includeStatic, &diagnostics)
		if err != nil {
			t.Fatal(err)
		}
		want := 2
		if includeStatic {
			want = 3
		}
		if len(exchanges) != want || skipped != 1 || len(exchanges)+filtered+skipped != 4 {
			t.Fatalf("accounting exchanges=%d filtered=%d skipped=%d", len(exchanges), filtered, skipped)
		}
		sources, err := HARSourceObservations(string(payload), includeStatic)
		if err != nil {
			t.Fatal(err)
		}
		baseline, err := HARCorrelationBaseline(string(payload), includeStatic)
		if err != nil {
			t.Fatal(err)
		}
		for i, source := range sources {
			if source.ID != exchanges[i].SourceEntryID || baseline[i].ID != i || source.Method != exchanges[i].Method {
				t.Fatalf("source/baseline ID or method mismatch: %+v", source)
			}
			if source.Method == "CONNECT" || strings.Contains(source.ResponseBody, "proxy-only") {
				t.Fatal("tunnel leaked into correlation observations")
			}
		}
	}
	result, err := Convert(ImportRequest{Format: "har", Content: string(payload)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.TotalRequests != 4 || result.Stats.SkippedRequests != 1 || result.Stats.FilteredAssets != 1 || result.Stats.ConvertedActions != 2 {
		t.Fatalf("stats: %+v", result.Stats)
	}
	if len(result.Correlations) != 0 || len(result.Script.Actions[0].Extractors) != 0 {
		t.Fatal("proxy response caused application correlation")
	}
	found := false
	for _, d := range result.Unsupported {
		if d.Element == "HAR proxy tunnel" && d.Severity == diag.Info {
			found = true
		}
	}
	if !found {
		t.Fatal("proxy setup exclusion missing from review")
	}
	if result.SourceReview == nil || !strings.Contains(result.SourceReview.SourceFiles[0].Content, "CONNECT") {
		t.Fatal("source tunnel evidence lost")
	}
}
