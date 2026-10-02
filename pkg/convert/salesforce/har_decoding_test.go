package salesforce

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestSalesforceHARUsesSharedDecodingAndOriginalEntryIDs(t *testing.T) {
	const token = "synthetic-token-abcdef123456"
	content, err := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{
		map[string]any{"request": map[string]any{"method": "CONNECT", "url": "https://acme.my.salesforce.com"}, "response": map[string]any{"content": map[string]any{"text": "proxy setup"}}},
		map[string]any{"request": map[string]any{"method": "GET", "url": "https://acme.my.salesforce.com/login"}, "response": map[string]any{"status": 200, "headers": []any{map[string]any{"name": "Content-Encoding", "value": "gzip"}}, "content": map[string]any{"encoding": "base64", "text": base64.StdEncoding.EncodeToString([]byte(`{"access_token":"` + token + `"}`))}}},
		map[string]any{"request": map[string]any{"method": "POST", "url": "https://acme.my.salesforce.com/aura", "cookies": []any{map[string]any{"name": "theme", "value": "light"}}, "postData": map[string]any{"mimeType": "application/x-www-form-urlencoded", "params": []any{map[string]any{"name": "token", "value": token}}}}, "response": map[string]any{"status": 200, "content": map[string]any{"text": "{}"}}},
		map[string]any{"request": map[string]any{"method": "POST", "url": "https://acme.my.salesforce.com/binary", "postData": map[string]any{"encoding": "base64", "text": "/wAB"}}, "response": map[string]any{}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	content = append([]byte{0xef, 0xbb, 0xbf}, content...)
	entries, err := parseHAR(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].ID != 2 || entries[1].ID != 3 || entries[2].ID != 4 {
		t.Fatalf("source IDs: %+v", entries)
	}
	if entries[0].ResponseBody != `{"access_token":"`+token+`"}` {
		t.Fatal("base64 decoded text was incorrectly decompressed")
	}
	if entries[1].RequestBody != "token="+token || entries[1].RequestHeaders["Cookie"] != "theme=light" {
		t.Fatalf("form or cookie decoding: %+v", entries[1])
	}
	if !entries[2].RequestBodyTransformed {
		t.Fatal("binary request rewrite guard lost")
	}
	script := &dsl.Script{Name: "synthetic SF", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"}, Actions: []dsl.Action{
		{Type: "GET", Method: "GET", Target: "/login"},
		{Type: "POST", Method: "POST", Target: "/aura", Body: "token=" + token},
	}}
	review, err := Analyze(script, AnalyzeOptions{BuildNative: true, HARData: content})
	if err != nil {
		t.Fatal(err)
	}
	if review.HARMatchCoverage.MatchedRequests != 2 || review.NativePreview == nil {
		t.Fatalf("review coverage: %+v", review.HARMatchCoverage)
	}
	if len(review.NativePreview.Script.Actions[0].Extractors) != 1 || !strings.Contains(review.NativePreview.Script.Actions[1].Body, "{{var.corr_access_token}}") {
		t.Fatal("decoded response did not produce native correlation")
	}
	if script.Actions[1].Body != "token="+token {
		t.Fatal("analysis mutated original script")
	}
}

func TestSalesforceHARRejectsTruncationEvenWithBOM(t *testing.T) {
	for _, content := range []string{`{"log":{"entries":[`, "\uFEFF" + `{"log":{"entries":[`} {
		if _, err := parseHAR([]byte(content)); err == nil {
			t.Fatal("accepted truncated HAR")
		}
	}
}
