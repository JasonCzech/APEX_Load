package sample

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func cacheValidatorHAREntry(method, url string, status int, headers map[string]string) map[string]any {
	requestHeaders := make([]any, 0, len(headers))
	for name, value := range headers {
		requestHeaders = append(requestHeaders, map[string]any{"name": name, "value": value})
	}
	return map[string]any{
		"request":  map[string]any{"method": method, "url": url, "headers": requestHeaders},
		"response": map[string]any{"status": status, "headers": []any{}, "content": map[string]any{"mimeType": "application/json", "text": "{}"}},
	}
}

// A browser's cache validators are left out of converted requests, so replay
// gets the full response however a CDN edge answered the recording; the
// application's own preconditions stay.
func TestConvertHARDropsBrowserCacheValidators(t *testing.T) {
	data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": []any{
		cacheValidatorHAREntry("GET", "https://www.example.test/consent.json", 200, map[string]string{"If-None-Match": `"edge-a"`, "If-Modified-Since": "Mon, 12 Oct 2026 10:00:00 GMT", "Accept": "application/json"}),
		cacheValidatorHAREntry("GET", "https://www.example.test/profile.json", 304, map[string]string{"if-none-match": `"v7"`}),
		cacheValidatorHAREntry("GET", "https://www.example.test/report.json", 304, map[string]string{"Range": "bytes=0-99", "If-Range": `"v3"`}),
		cacheValidatorHAREntry("PUT", "https://www.example.test/items/1", 200, map[string]string{"If-Match": `"rev-9"`, "If-Unmodified-Since": "Mon, 12 Oct 2026 10:00:00 GMT"}),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert(ImportRequest{Format: "har", Content: string(data)})
	if err != nil {
		t.Fatal(err)
	}
	actions := result.Script.Actions
	if len(actions) != 4 {
		t.Fatalf("actions = %d, want 4", len(actions))
	}
	for index, action := range actions[:3] {
		for _, name := range []string{"If-None-Match", "If-Modified-Since", "If-Range"} {
			if len(headerListValues(action.HeaderList, name)) > 0 || headerLookup(action.Headers, name) != "" {
				t.Fatalf("action %d still sends %s: %v", index, name, action.HeaderList)
			}
		}
	}
	if headerLookup(actions[0].Headers, "Accept") != "application/json" {
		t.Fatalf("action 0 headers = %v, want the other headers kept", actions[0].Headers)
	}
	for name, want := range map[string]string{"If-Match": `"rev-9"`, "If-Unmodified-Since": "Mon, 12 Oct 2026 10:00:00 GMT"} {
		if got := headerLookup(actions[3].Headers, name); got != want {
			t.Fatalf("PUT %s = %q, want the application's precondition kept", name, got)
		}
	}
	// A recorded 304 is checked as the full response the request now gets.
	for index, want := range []int{200, 200, 206, 200} {
		if got := statusAssertion(actions[index]); got != want {
			t.Fatalf("action %d status check = %v, want %d", index, got, want)
		}
	}
	if !containsSampleDiagnostic(result, "HAR cache validators") {
		t.Fatalf("diagnostics = %v, want the removal noted", result.Unsupported)
	}
	for _, item := range result.Unsupported {
		if item.Element == "HAR cache validators" && !strings.Contains(item.String(), "3 requests") {
			t.Fatalf("cache validator note = %q, want a count of 3", item.String())
		}
	}
}

func statusAssertion(action dsl.Action) any {
	for _, assertion := range action.Assertions {
		if assertion.Type == "status_code" {
			return assertion.Expected
		}
	}
	return nil
}
