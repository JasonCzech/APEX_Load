package salesforce

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// This synthetic companion capture preserves the storage and repeated-request
// shapes observed in an operator HAR without copying any source field values.
func TestSalesforceSampleShapeMatchesDecodedCompanionEntries(t *testing.T) {
	const endpoint = "https://synthetic.my.salesforce.com/services/apexrest/orders"
	entry := func(requestBody string, content any) map[string]any {
		return map[string]any{
			"request":  map[string]any{"method": "POST", "url": endpoint, "httpVersion": "HTTP/synthetic-export-9.9", "headers": []any{map[string]any{"name": "Content-Type", "value": "application/json"}}, "postData": map[string]any{"mimeType": "application/json", "text": requestBody}},
			"response": map[string]any{"status": 200, "httpVersion": "HTTP/synthetic-export-9.9", "headers": []any{map[string]any{"name": "Content-Encoding", "value": "gzip"}, map[string]any{"name": "Content-Length", "value": "7"}, map[string]any{"name": "Content-Type", "value": "application/json"}}, "content": content},
		}
	}
	const rawBody = `{"order":"synthetic-one","access_token":"synthetic-order-token-123456"}`
	const base64Body = `{"order":"synthetic-two"}`
	tunnel := entry("", map[string]any{"text": "synthetic proxy setup"})
	tunnel["request"].(map[string]any)["method"] = "CONNECT"
	payload, err := json.Marshal(map[string]any{"log": map[string]any{"version": "1.2", "entries": []any{
		tunnel,
		entry(`{"request":"first"}`, map[string]any{"text": rawBody}),
		entry(`{"request":"second"}`, map[string]any{"text": base64.StdEncoding.EncodeToString([]byte(base64Body)), "encoding": "base64"}),
		entry(`{"request":"binary"}`, map[string]any{"text": base64.StdEncoding.EncodeToString([]byte{0, 1, 0xff}), "encoding": "base64"}),
		entry(`{"request":"missing"}`, map[string]any{"text": nil}),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), payload...)
	entries, err := parseHAR(payload)
	if err != nil || len(entries) != 4 {
		t.Fatalf("companion entry count=%d err=%v", len(entries), err)
	}
	sources, err := sample.HARSourceObservations(string(payload), true)
	if err != nil {
		t.Fatal(err)
	}
	wantBodies := []string{rawBody, base64Body, "", ""}
	for index, e := range entries {
		if e.ID != index+2 || e.ResponseBody != wantBodies[index] || e.RequestBody != sources[index].RequestBody || e.Method != sources[index].Method || !reflect.DeepEqual(e.ResponseHeaders, sources[index].ResponseHeaders) {
			t.Fatal("companion path bypassed shared logical storage or original source IDs")
		}
		if index < 3 && (e.ResponseHeaders["Content-Encoding"] != "" || e.ResponseHeaders["Content-Length"] != "") {
			t.Fatal("companion text correlation inherited stale wire framing")
		}
	}
	refs := []actionRef{
		{Method: "POST", URL: endpoint, Action: &dsl.Action{Body: `{ "request" : "second" }`}},
		{Method: "POST", URL: endpoint, Action: &dsl.Action{Body: `{"request":"first"}`}},
		{Method: "POST", URL: endpoint, Action: &dsl.Action{Body: `{"request":"binary"}`}},
		{Method: "POST", URL: endpoint, Action: &dsl.Action{Body: `{"request":"missing"}`}},
	}
	if got := matchHAR(refs, entries); !reflect.DeepEqual(got, map[int]int{0: 3, 1: 2, 2: 4, 3: 5}) {
		t.Fatalf("repeated companion requests matched wrong source ordinals: %v", got)
	}
	script := &dsl.Script{ID: "synthetic-companion", Name: "Synthetic companion", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://synthetic.my.salesforce.com"}}
	for _, ref := range refs {
		script.Actions = append(script.Actions, dsl.Action{Type: "POST", Method: "POST", Target: "/services/apexrest/orders", Body: ref.Action.Body})
	}
	before, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	review, err := Analyze(script, AnalyzeOptions{HARData: payload})
	if err != nil {
		t.Fatal(err)
	}
	if !review.HARMatchCoverage.HARProvided || review.HARMatchCoverage.ImportedRequests != 4 || review.HARMatchCoverage.MatchedRequests != 4 || review.HARMatchCoverage.CoveragePct != 100 {
		t.Fatalf("shared companion review coverage: %+v", review.HARMatchCoverage)
	}
	after, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) || !reflect.DeepEqual(payload, original) {
		t.Fatal("companion analysis mutated the authored script or immutable capture bytes")
	}
}
