package k6

import "testing"

func TestConvertImportsConstrainedHTTPSubset(t *testing.T) {
	source := `
import http from 'k6/http';
import { check, sleep } from 'k6';
export const options = { vus: 3, duration: '1m', thresholds: { http_req_duration: ['p(95)<800'] } };
export default function () {
  const res = http.get('https://api.example.com/catalog', { headers: { 'X-Test': 'yes' } });
  check(res, { 'status is 200': r => r.status === 200 });
  http.post('https://api.example.com/orders', '{"sku":"A"}', { headers: { 'Content-Type': 'application/json' } });
  sleep(1);
}`
	result, err := Convert(ImportRequest{Name: "k6 smoke", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Protocol != "http" || result.Script.Config.BaseURL != "https://api.example.com" {
		t.Fatalf("script = %#v, want HTTP script with detected base URL", result.Script)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(result.Script.Actions))
	}
	if result.Script.Actions[0].Target != "/catalog" || result.Script.Actions[0].Headers["X-Test"] != "yes" {
		t.Fatalf("first action = %#v", result.Script.Actions[0])
	}
	if result.Script.Actions[1].Method != "POST" || result.Script.Actions[1].Body != `{"sku":"A"}` {
		t.Fatalf("second action = %#v", result.Script.Actions[1])
	}
	if result.Stats.Checks != 1 || result.Stats.Sleeps != 1 || result.Stats.Thresholds != 1 {
		t.Fatalf("stats = %#v, want checks/sleeps/thresholds", result.Stats)
	}
	if result.SourceReview == nil || len(result.SourceReview.Mappings) != 2 {
		t.Fatalf("source review = %#v, want mappings", result.SourceReview)
	}
}

func TestConvertFlagsBrowserAsReviewOnly(t *testing.T) {
	source := `import { browser } from 'k6/browser'; export default async function () { const page = await browser.newPage(); }`
	result, err := Convert(ImportRequest{Name: "browser", Content: source + "\nhttp.get('https://example.com')"})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Unsupported) == 0 {
		t.Fatalf("unsupported diagnostics = %#v, want browser blocker", result.Unsupported)
	}
}

func TestExportProducesK6HTTPSubset(t *testing.T) {
	result, err := Export(ExportRequest{Script: *mustConvert(t).Script})
	if err != nil {
		t.Fatalf("Export returned error: %v", err)
	}
	if result.Stats.HTTPRequests != 1 || result.Script == "" {
		t.Fatalf("export result = %#v", result)
	}
	if !contains(result.Script, "http.get") || !contains(result.Script, "BASE_URL") {
		t.Fatalf("exported script:\n%s", result.Script)
	}
}

func mustConvert(t *testing.T) *ConvertResult {
	t.Helper()
	result, err := Convert(ImportRequest{Name: "one", Content: `http.get('https://example.com/health')`})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && find(haystack, needle) >= 0)
}

func find(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
