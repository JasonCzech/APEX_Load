package k6

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

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
	for index, action := range result.Script.Actions {
		if got := action.Metadata["execution_blocker"]; got != "import_review_required" {
			t.Fatalf("actions[%d] residual-flow blocker = %#v", index, got)
		}
	}
	if result.Stats.Checks != 1 || result.Stats.Sleeps != 1 || result.Stats.Thresholds != 1 {
		t.Fatalf("stats = %#v, want checks/sleeps/thresholds", result.Stats)
	}
	if result.SourceReview == nil || len(result.SourceReview.Mappings) != 2 {
		t.Fatalf("source review = %#v, want mappings", result.SourceReview)
	}
	sourceOrderFindings := 0
	for _, finding := range result.Findings {
		if finding.Code == "source_order_unavailable" {
			sourceOrderFindings++
		}
	}
	if sourceOrderFindings != 2 {
		t.Fatalf("source_order_unavailable findings = %d, want one per map-backed header list: %#v", sourceOrderFindings, result.Findings)
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
	for index, action := range result.Script.Actions {
		if got := action.Metadata["execution_blocker"]; got != "import_review_required" {
			t.Fatalf("actions[%d] browser blocker = %#v", index, got)
		}
	}
}

func TestConvertSupportsStaticPatchHeadOptionsAndDelete(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "methods",
		Content: `
http.patch('https://api.example.com/items/1', '{"name":"new"}');
http.head('https://api.example.com/health');
http.options('https://api.example.com/items', '');
http.del('https://api.example.com/items/1', '{"reason":"obsolete"}');
`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 4 {
		t.Fatalf("actions = %#v", result.Script.Actions)
	}
	methods := []string{"PATCH", "HEAD", "OPTIONS", "DELETE"}
	for i, method := range methods {
		if result.Script.Actions[i].Method != method {
			t.Fatalf("actions[%d].method = %q, want %q", i, result.Script.Actions[i].Method, method)
		}
		if _, blocked := result.Script.Actions[i].Metadata["execution_blocker"]; blocked {
			t.Fatalf("static actions[%d] unexpectedly blocked: %#v", i, result.Script.Actions[i].Metadata)
		}
	}
	if result.Script.Actions[0].Body != `{"name":"new"}` ||
		result.Script.Actions[2].Body != "" ||
		result.Script.Actions[3].Body != `{"reason":"obsolete"}` {
		t.Fatalf("method bodies = %q / %q / %q", result.Script.Actions[0].Body, result.Script.Actions[2].Body, result.Script.Actions[3].Body)
	}
}

func TestConvertGovernsAndBlocksStaticURLUserinfo(t *testing.T) {
	t.Parallel()
	const canary = "k6-userinfo-canary-71F4"
	result, err := Convert(ImportRequest{
		Name: "userinfo",
		Content: `
http.get('https://alice:` + canary + `@api.example.test/private');
`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if !strings.Contains(action.Target, "{{secret.") ||
		!strings.Contains(action.Target, "@api.example.test/private") ||
		strings.Contains(action.Target, canary) {
		t.Fatalf("projected target = %q", action.Target)
	}
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("metadata = %#v, want userinfo review gate", action.Metadata)
	}
	public, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), canary) {
		t.Fatal("userinfo credential leaked in public conversion result")
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 ||
		strings.Contains(result.SourceReview.SourceFiles[0].Content, canary) {
		t.Fatalf("source review leaked userinfo credential: %#v", result.SourceReview)
	}
}

func TestConvertDoesNotPromoteCommentedHTTPCalls(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "comments",
		Content: `
// http.get('https://should-not-run.test/line');
/*
http.post('https://should-not-run.test/block', '{"unsafe":true}');
*/
http.get('https://api.example.com/health');
`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want only the executable request", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if action.Target != "/health" || strings.Contains(action.Target, "should-not-run") {
		t.Fatalf("executable action = %#v", action)
	}
	if _, blocked := action.Metadata["execution_blocker"]; blocked {
		t.Fatalf("comments created residual behavior or a blocker: %#v", action.Metadata)
	}
}

func TestConvertRejectsSourceContainingOnlyCommentedHTTPCalls(t *testing.T) {
	_, err := Convert(ImportRequest{
		Name:    "comments only",
		Content: `// http.get('https://should-not-run.test')`,
	})
	if err == nil || !strings.Contains(err.Error(), "no supported k6 HTTP calls") {
		t.Fatalf("Convert error = %v, want no supported calls", err)
	}
}

func TestConvertDynamicExpressionsBecomeDisclosureSafeBlockedDrafts(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name: "dynamic",
		Content: `
const base = __ENV.BASE_URL;
const payload = {password: 'credential-canary-k6'};
http.get(base + '/orders', {headers: {'Authorization': __ENV.TOKEN}});
http.post('https://api.example.com/orders', JSON.stringify(payload));
`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %#v", result.Script.Actions)
	}
	first := result.Script.Actions[0]
	if first.Target != "/__k6_dynamic_target__" || first.Headers != nil {
		t.Fatalf("dynamic URL/header draft = %#v", first)
	}
	second := result.Script.Actions[1]
	if second.Body != "" {
		t.Fatalf("dynamic body was copied into executable material: %q", second.Body)
	}
	for i, action := range result.Script.Actions {
		if got := action.Metadata["execution_blocker"]; got != "import_review_required" {
			t.Fatalf("actions[%d] blocker = %#v", i, got)
		}
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"credential-canary-k6", "JSON.stringify", "base +", "__ENV.TOKEN"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("blocked script retained source expression %q: %s", forbidden, payload)
		}
	}
}

func TestConvertDynamicOnlyURLProducesValidBlockedDraft(t *testing.T) {
	t.Parallel()
	result, err := Convert(ImportRequest{
		Name: "dynamic only",
		Content: `
const base = __ENV.BASE_URL;
http.get(base + '/orders');
`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Script.Config.BaseURL != "https://missing-base-url.apexload.invalid" ||
		len(result.Script.Actions) != 1 {
		t.Fatalf("script = %#v", result.Script)
	}
	action := result.Script.Actions[0]
	if action.Target != "/__k6_dynamic_target__" ||
		action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("dynamic-only action = %#v", action)
	}
	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok {
		t.Fatalf("import review = %#v", action.Metadata["import_review"])
	}
	reasons, ok := review["reason_codes"].([]string)
	if !ok || !containsK6Reason(reasons, "dynamic_url_expression") ||
		!containsK6Reason(reasons, "missing_base_url") {
		t.Fatalf("reason codes = %#v", review["reason_codes"])
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("blocked draft validation issues = %v", issues)
	}
}

func TestConvertResidualLiteralIsAbsentFromCompleteResultAndBlocksRequests(t *testing.T) {
	const canary = "k6-residual-arbitrary-canary-62f1"
	result, err := Convert(ImportRequest{
		Name: "residual",
		Content: `
import http from 'k6/http';
export default function () {
  const signingMaterial = "k6-residual-arbitrary-canary-62f1";
  http.get("https://api.example.com/orders");
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want one request", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("residual request metadata = %#v, want durable gate", action.Metadata)
	}
	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok || !containsK6Reason(k6MetadataStrings(review["reason_codes"]), "unsupported_source_logic") {
		t.Fatalf("import_review = %#v, want unsupported_source_logic", action.Metadata["import_review"])
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 ||
		!result.SourceReview.SourceFiles[0].Redacted {
		t.Fatalf("source review = %#v, want explicitly redacted source", result.SourceReview)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(payload), canary) {
		t.Fatalf("complete conversion result disclosed residual canary: %s", payload)
	}
}

func TestConvertStaticLiteralWithInvalidHeaderBecomesBlockedDraft(t *testing.T) {
	result, err := Convert(ImportRequest{
		Name:    "static",
		Content: `http.get("https://api.example.com/line\u002Ditem", {headers: {"X-Test": "a\nb"}});`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if action.Target != "/line-item" || action.Headers["X-Test"] != "a b" {
		t.Fatalf("decoded literal action = %#v", action)
	}
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("unsafe header draft was not blocked: %#v", action.Metadata)
	}
}

func TestConvertProjectsCredentialsWithoutDisclosureAndIsDeterministic(t *testing.T) {
	const (
		urlCanary    = "k6-url-canary-7d1d"
		headerCanary = "k6-header-canary-34ca"
		bodyCanary   = "k6-body-canary-81fe"
	)
	const source = `http.post(
  "https://api.example.com/login?access_token=k6-url-canary-7d1d",
  "{\"password\":\"k6-body-canary-81fe\",\"note\":\"byte-exact\"}",
  {headers: {"Content-Type": "application/json", "X-Api-Key": "k6-header-canary-34ca"}}
);`

	var baseline [sha256.Size]byte
	for run := 0; run < 25; run++ {
		result, err := Convert(ImportRequest{Name: "credential projection", Content: source})
		if err != nil {
			t.Fatalf("run %d: Convert returned error: %v", run, err)
		}
		if len(result.Script.Actions) != 1 {
			t.Fatalf("run %d: actions = %d, want 1", run, len(result.Script.Actions))
		}
		action := result.Script.Actions[0]
		if !strings.Contains(action.Target, "{{secret.") {
			t.Fatalf("run %d: target = %q, want projected query credential", run, action.Target)
		}
		if !strings.Contains(action.Headers["X-Api-Key"], "{{secret.") {
			t.Fatalf("run %d: header = %q, want projected credential", run, action.Headers["X-Api-Key"])
		}
		if !strings.Contains(action.Body, `"password":"{{secret.`) ||
			!strings.Contains(action.Body, `"note":"byte-exact"`) {
			t.Fatalf("run %d: body = %q, want projected password with nonsensitive bytes retained", run, action.Body)
		}
		if len(result.SecretRequirements) != 3 {
			t.Fatalf("run %d: requirements = %#v, want URL/header/body requirements", run, result.SecretRequirements)
		}
		for _, requirement := range result.SecretRequirements {
			if requirement.Value != "" || requirement.SourceValueAvailable {
				t.Fatalf("run %d: public requirement retained protected material: %#v", run, requirement)
			}
		}

		result.Script.CreatedAt = time.Time{}
		payload, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("run %d: marshal result: %v", run, err)
		}
		for _, canary := range []string{urlCanary, headerCanary, bodyCanary} {
			if strings.Contains(string(payload), canary) {
				t.Fatalf("run %d: result disclosed canary %q: %s", run, canary, payload)
			}
		}
		hash := sha256.Sum256(payload)
		if run == 0 {
			baseline = hash
		} else if hash != baseline {
			t.Fatalf("run %d: normalized result hash = %x, want %x", run, hash, baseline)
		}
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
