package k6

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func k6ImportReviewReasons(t *testing.T, action dsl.Action) []string {
	t.Helper()
	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok {
		return nil
	}
	return k6MetadataStrings(review["reason_codes"])
}

func k6ImportReviewSourceRefs(t *testing.T, action dsl.Action) []string {
	t.Helper()
	review, ok := action.Metadata["import_review"].(map[string]any)
	if !ok {
		return nil
	}
	return k6MetadataStrings(review["source_refs"])
}

func TestConvertTranslatesStaticSleepAndStatusCheck(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';
import { check, sleep } from 'k6';

export default function () {
  const res = http.get('https://api.example.com/catalog');
  check(res, { 'status is 200': (r) => r.status === 200 });
  sleep(1);
  const created = http.post('https://api.example.com/orders', '{"sku":"A"}');
  check(created, { 'created': r => r.status === 201 });
  sleep(0.5);
}`
	result, err := Convert(ImportRequest{Name: "catalog.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %#v, want the two requests only", result.Script.Actions)
	}
	want := []struct {
		status int
		think  float64
	}{{status: 200, think: 1000}, {status: 201, think: 500}}
	for index, expected := range want {
		action := result.Script.Actions[index]
		if len(action.Assertions) != 1 || action.Assertions[0].Type != "status_code" ||
			action.Assertions[0].Expected != expected.status {
			t.Fatalf("actions[%d].assertions = %#v, want status_code %d", index, action.Assertions, expected.status)
		}
		if !action.ThinkTimeAfter || action.ThinkTime.Type != "fixed" || action.ThinkTime.MeanMs != expected.think {
			t.Fatalf("actions[%d] think time = %#v/%v, want fixed %.0fms after the request",
				index, action.ThinkTime, action.ThinkTimeAfter, expected.think)
		}
		if _, blocked := action.Metadata["execution_blocker"]; blocked {
			t.Fatalf("actions[%d] stayed review-gated after translation: %#v", index, action.Metadata)
		}
	}
	if result.Script.Config.ThinkTime.MeanMs != 0 {
		t.Fatalf("translated sleeps leaked into a global think time: %#v", result.Script.Config.ThinkTime)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none when every check/sleep was translated", result.Warnings)
	}
	if result.Stats.Checks != 2 || result.Stats.Sleeps != 2 {
		t.Fatalf("stats = %#v, want both checks and sleeps counted", result.Stats)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 ||
		result.SourceReview.SourceFiles[0].Redacted {
		t.Fatalf("source review = %#v, want a reviewable, non-residual source", result.SourceReview)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("translated script validation issues = %v", issues)
	}
	translated := false
	fatality := false
	for _, assumption := range result.Assumptions {
		if strings.Contains(assumption, "were translated into APEX assertions and per-action think time") {
			translated = true
		}
		// A failed k6 check() does not fail the request; the assertion it becomes
		// does, and stops the iteration under the draft's default error handling.
		// That fidelity change has to be disclosed.
		if strings.Contains(assumption, "only records a check metric") &&
			strings.Contains(assumption, "ends the iteration") {
			fatality = true
		}
	}
	if !translated {
		t.Fatalf("assumptions = %#v, want the translation disclosure", result.Assumptions)
	}
	if !fatality {
		t.Fatalf("assumptions = %#v, want the check-fatality disclosure", result.Assumptions)
	}
}

func TestConvertGatesDynamicSleepAndNonStatusCheck(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';
import { check, sleep } from 'k6';

export default function () {
  const res = http.get('https://api.example.com/catalog');
  check(res, { 'body is not empty': (r) => r.body.length > 0 });
  sleep(pause);
}`
	result, err := Convert(ImportRequest{Name: "dynamic.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	var request dsl.Action
	for _, action := range result.Script.Actions {
		if action.Type == "request" {
			request = action
		}
	}
	if request.Name == "" {
		t.Fatalf("actions = %#v, want a request action", result.Script.Actions)
	}
	if len(request.Assertions) != 0 || request.ThinkTimeAfter {
		t.Fatalf("unrecognised check/sleep produced executable material: %#v", request)
	}
	if request.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("unrecognised check/sleep did not gate the request: %#v", request.Metadata)
	}
	reasons := k6ImportReviewReasons(t, request)
	for _, want := range []string{"untranslated_check", "untranslated_sleep"} {
		if !containsK6Reason(reasons, want) {
			t.Fatalf("reason codes = %#v, want %q", reasons, want)
		}
	}
	if result.Stats.Checks != 1 || result.Stats.Sleeps != 1 {
		t.Fatalf("stats = %#v, want the dynamic sleep and non-status check counted", result.Stats)
	}
	for _, want := range []string{"k6 check() calls outside the static status-code idiom", "k6 sleep() calls with non-literal durations"} {
		found := false
		for _, warning := range result.Warnings {
			if strings.Contains(warning, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("warnings = %#v, want one describing %q", result.Warnings, want)
		}
	}
}

func TestConvertDoesNotTranslateSleepOrCheckInsideControlFlow(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';
import { check, sleep } from 'k6';

export default function () {
  for (let i = 0; i < 3; i++) {
    const res = http.get('https://api.example.com/catalog');
    check(res, { 'status is 200': (r) => r.status === 200 });
    sleep(1);
  }
}`
	result, err := Convert(ImportRequest{Name: "loop.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	var request dsl.Action
	for _, action := range result.Script.Actions {
		if action.Type == "request" {
			request = action
		}
	}
	if request.Name == "" {
		t.Fatalf("actions = %#v, want a request action", result.Script.Actions)
	}
	if len(request.Assertions) != 0 || request.ThinkTimeAfter {
		t.Fatalf("a conditional/repeated check or sleep was translated as unconditional: %#v", request)
	}
	reasons := k6ImportReviewReasons(t, request)
	for _, want := range []string{"untranslated_check", "untranslated_sleep", "residual_control_flow"} {
		if !containsK6Reason(reasons, want) {
			t.Fatalf("reason codes = %#v, want %q", reasons, want)
		}
	}
}

func TestConvertSumsConsecutiveSleepsAndGatesUnownedSleep(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';
import { sleep } from 'k6';

export default function () {
  sleep(1);
  http.get('https://api.example.com/catalog');
  sleep(1);
  sleep(0.25);
}`
	result, err := Convert(ImportRequest{Name: "additive.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if !action.ThinkTimeAfter || action.ThinkTime.MeanMs != 1250 {
		t.Fatalf("think time = %#v/%v, want the two trailing sleeps summed to 1250ms", action.ThinkTime, action.ThinkTimeAfter)
	}
	if !containsK6Reason(k6ImportReviewReasons(t, action), "untranslated_sleep") {
		t.Fatalf("leading sleep with no preceding request was dropped instead of gated: %#v", action.Metadata)
	}
}

func TestConvertWarnsThatResidualSourceKeepsTheDraftBlocked(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';

export default function () {
  const signature = computeSignature();
  http.get('https://api.example.com/catalog');
}`
	result, err := Convert(ImportRequest{Name: "residual-warning.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	found := false
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "outside the translated static subset") &&
			strings.Contains(warning, "residual-warning.js line 4") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %#v, want the blocked-draft disclosure", result.Warnings)
	}
}

func TestConvertLeavesOversizedSleepUntranslated(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';
import { sleep } from 'k6';

export default function () {
  http.get('https://api.example.com/catalog');
  sleep(3601);
}`
	result, err := Convert(ImportRequest{Name: "oversized.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if action.ThinkTimeAfter || action.ThinkTime.MeanMs != 0 {
		t.Fatalf("out-of-range sleep became think time: %#v", action.ThinkTime)
	}
	if !containsK6Reason(k6ImportReviewReasons(t, action), "untranslated_sleep") {
		t.Fatalf("out-of-range sleep was not gated: %#v", action.Metadata)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("draft validation issues = %v", issues)
	}
}

func TestConvertGatesCheckOnAnUnknownResponseBinding(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';
import { check } from 'k6';

export default function () {
  http.get('https://api.example.com/catalog');
  check(other, { 'status is 200': (r) => r.status === 200 });
}`
	result, err := Convert(ImportRequest{Name: "unbound.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if len(action.Assertions) != 0 {
		t.Fatalf("assertions = %#v, want none for an unresolvable response binding", action.Assertions)
	}
	if !containsK6Reason(k6ImportReviewReasons(t, action), "untranslated_check") {
		t.Fatalf("unresolvable check was not gated: %#v", action.Metadata)
	}
}

func TestConvertBindsChecksToTheReassignedResponseVariable(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';
import { check } from 'k6';

export default function () {
  let res = http.get('https://api.example.com/catalog');
  check(res, { 'catalog ok': (r) => r.status === 200 });
  res = http.get('https://api.example.com/orders');
  check(res, { 'orders created': (r) => r.status === 201 });
}`
	result, err := Convert(ImportRequest{Name: "rebound.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %#v, want two requests", result.Script.Actions)
	}
	first := result.Script.Actions[0]
	if len(first.Assertions) != 1 || first.Assertions[0].Expected != 200 {
		t.Fatalf("actions[0].assertions = %#v, want only the 200 check", first.Assertions)
	}
	// The bare reassignment is not the declaration form the importer proves, so
	// the second check must stay review evidence rather than bind to the first
	// request.
	second := result.Script.Actions[1]
	if len(second.Assertions) != 0 {
		t.Fatalf("actions[1].assertions = %#v, want none for the unproven rebinding", second.Assertions)
	}
	if !containsK6Reason(k6ImportReviewReasons(t, second), "untranslated_check") {
		t.Fatalf("unproven rebinding check was not gated: %#v", second.Metadata)
	}
}

func TestConvertBindsChecksInASingleLineIterationBody(t *testing.T) {
	t.Parallel()
	const source = "import http from 'k6/http';\n" +
		"import { check } from 'k6';\n" +
		"export default function () { const res = http.get('https://api.example.com/health'); check(res, { 'ok': r => r.status === 200 }); }"
	result, err := Convert(ImportRequest{Name: "single-line.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if len(action.Assertions) != 1 || action.Assertions[0].Expected != 200 {
		t.Fatalf("assertions = %#v, want the status check bound to the same-line declaration", action.Assertions)
	}
	if containsK6Reason(k6ImportReviewReasons(t, action), "untranslated_check") {
		t.Fatalf("translated check still gated the request: %#v", action.Metadata)
	}
}

func TestConvertIgnoresCommentedSleepAndCheck(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';

export default function () {
  http.get('https://api.example.com/catalog');
  // check(res, { 'status is 200': (r) => r.status === 200 });
  /* sleep(1); */
}`
	result, err := Convert(ImportRequest{Name: "commented.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Stats.Checks != 0 || result.Stats.Sleeps != 0 {
		t.Fatalf("stats = %#v, want commented calls excluded", result.Stats)
	}
	action := result.Script.Actions[0]
	if _, blocked := action.Metadata["execution_blocker"]; blocked {
		t.Fatalf("commented check/sleep gated the request: %#v", action.Metadata)
	}
}

func TestConvertDoesNotTranslateMemberCheckCalls(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';

export default function () {
  const res = http.get('https://api.example.com/catalog');
  assertions.check(res, { 'status is 200': (r) => r.status === 200 });
}`
	result, err := Convert(ImportRequest{Name: "member.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if len(action.Assertions) != 0 {
		t.Fatalf("assertions = %#v, want none for a non-k6 member call", action.Assertions)
	}
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("unknown member call did not stay residual: %#v", action.Metadata)
	}
	if result.Stats.Checks != 0 {
		t.Fatalf("stats.Checks = %d, want a non-k6 member call excluded from the check count", result.Stats.Checks)
	}
}

// The `import * as k6 from 'k6'` spelling binds the same globals, so
// `k6.check(...)`/`k6.sleep(1)` must translate exactly like the bare calls —
// including the recorded span covering the `k6.` qualifier, without which the
// leftover bytes would keep the request review-gated.
func TestConvertTranslatesNamespaceQualifiedK6Calls(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';
import * as k6 from 'k6';

export default function () {
  const res = http.get('https://api.example.com/a');
  k6.check(res, { 'ok': (r) => r.status === 200 });
  k6.sleep(1);
}`
	result, err := Convert(ImportRequest{Name: "namespace.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Stats.Checks != 1 || result.Stats.Sleeps != 1 {
		t.Fatalf("stats = %#v, want one check and one sleep counted", result.Stats)
	}
	if len(result.Script.Actions) != 1 {
		t.Fatalf("actions = %#v, want the single request", result.Script.Actions)
	}
	action := result.Script.Actions[0]
	if len(action.Assertions) != 1 || action.Assertions[0].Type != "status_code" || action.Assertions[0].Expected != 200 {
		t.Fatalf("assertions = %#v, want status_code 200 from k6.check()", action.Assertions)
	}
	if !action.ThinkTimeAfter || action.ThinkTime.Type != "fixed" || action.ThinkTime.MeanMs != 1000 {
		t.Fatalf("think time = %#v/%v, want fixed 1000ms from k6.sleep(1)", action.ThinkTime, action.ThinkTimeAfter)
	}
	if _, blocked := action.Metadata["execution_blocker"]; blocked {
		t.Fatalf("namespace-qualified calls left residual source behind: %#v", action.Metadata)
	}
}

// A namespace alias of some other module keeps the member-call exclusion: only
// the 'k6' module's namespace carries check()/sleep().
func TestConvertDoesNotTreatForeignNamespaceAsK6Global(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';
import * as helpers from './helpers.js';

export default function () {
  const res = http.get('https://api.example.com/a');
  helpers.check(res, { 'ok': (r) => r.status === 200 });
}`
	result, err := Convert(ImportRequest{Name: "foreign.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Stats.Checks != 0 {
		t.Fatalf("stats.Checks = %d, want a foreign namespace member call excluded", result.Stats.Checks)
	}
	action := result.Script.Actions[0]
	if len(action.Assertions) != 0 {
		t.Fatalf("assertions = %#v, want none for a foreign namespace member call", action.Assertions)
	}
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("foreign member call did not stay residual: %#v", action.Metadata)
	}
}

// Expression-level conditionality is invisible to brace depth: `guard &&
// check(...)` and `guard ? sleep(1) : 0` sit at iteration-body statement depth
// but run only when the guard holds. They must stay untranslated evidence
// rather than becoming unconditional assertions and think time.
func TestConvertDoesNotTranslateExpressionConditionalCalls(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{name: "logical and", body: "res.status !== 0 && check(res, { 'ok': (r) => r.status === 200 });\n  res.status !== 0 && sleep(3);"},
		{name: "ternary", body: "res.status !== 0 ? check(res, { 'ok': (r) => r.status === 200 }) : 0;\n  res.status !== 0 ? sleep(3) : 0;"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := `import http from 'k6/http';
import { check, sleep } from 'k6';

export default function () {
  const res = http.get('https://api.example.com/a');
  ` + test.body + `
}`
			result, err := Convert(ImportRequest{Name: "conditional.js", Content: source})
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}
			if result.Stats.Checks != 1 || result.Stats.Sleeps != 1 {
				t.Fatalf("stats = %#v, want the conditional check and sleep still counted", result.Stats)
			}
			action := result.Script.Actions[0]
			if len(action.Assertions) != 0 {
				t.Fatalf("assertions = %#v, want none from a conditionally executed check()", action.Assertions)
			}
			if action.ThinkTimeAfter || action.ThinkTime.MeanMs != 0 {
				t.Fatalf("think time = %#v/%v, want none from a conditionally executed sleep()", action.ThinkTime, action.ThinkTimeAfter)
			}
			reasons := k6ImportReviewReasons(t, action)
			for _, want := range []string{"untranslated_check", "untranslated_sleep"} {
				if !containsK6Reason(reasons, want) {
					t.Fatalf("reason codes = %#v, want %q", reasons, want)
				}
			}
		})
	}
}

// The namespace spelling and expression-level conditionality have to compose:
// recognising `k6.check(...)` as the global must not smuggle a conditional call
// past the statement-start gate.
func TestConvertDoesNotTranslateConditionalNamespaceQualifiedCalls(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';
import * as k6 from 'k6';

export default function () {
  const res = http.get('https://api.example.com/a');
  res.status !== 0 && k6.check(res, { 'ok': (r) => r.status === 200 });
  res.status !== 0 && k6.sleep(2);
}`
	result, err := Convert(ImportRequest{Name: "nsconditional.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Stats.Checks != 1 || result.Stats.Sleeps != 1 {
		t.Fatalf("stats = %#v, want the conditional namespace calls counted as untranslated", result.Stats)
	}
	action := result.Script.Actions[0]
	if len(action.Assertions) != 0 {
		t.Fatalf("assertions = %#v, want none from a conditional k6.check()", action.Assertions)
	}
	if action.ThinkTimeAfter || action.ThinkTime.MeanMs != 0 {
		t.Fatalf("think time = %#v/%v, want none from a conditional k6.sleep()", action.ThinkTime, action.ThinkTimeAfter)
	}
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("conditional namespace calls did not stay gated: %#v", action.Metadata)
	}
}

// The bucket invariant documented on k6StaticTranslation covers only the
// spellings this lexical pass recognises. Renamed imports are explicitly
// outside it, and the doc comment promises the residual-source scan still
// blocks them and names their lines. That promise is a guarantee shipped to
// reviewers, so it is pinned here rather than left as prose.
func TestConvertLeavesRenamedK6ImportsBlockedAndReported(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';
import { check as verify, sleep as pause } from 'k6';

export default function () {
  const res = http.get('https://api.example.com/a');
  verify(res, { 'ok': (r) => r.status === 200 });
  pause(1);
}`
	result, err := Convert(ImportRequest{Name: "renamed.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	// Not counted in either bucket: the pass never recognises these spellings.
	if result.Stats.Checks != 0 || result.Stats.Sleeps != 0 {
		t.Fatalf("stats = %#v, want renamed spellings uncounted per the documented limitation", result.Stats)
	}
	action := result.Script.Actions[0]
	if len(action.Assertions) != 0 || action.ThinkTimeAfter {
		t.Fatalf("renamed spellings were translated: %#v / %#v", action.Assertions, action.ThinkTime)
	}
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("renamed spellings were dropped without a gate: %#v", action.Metadata)
	}
	// ...and the reviewer is pointed at both offending lines, which is what
	// makes "not silently dropped" true.
	refs := k6ImportReviewSourceRefs(t, action)
	for _, want := range []string{"renamed.js line 6", "renamed.js line 7"} {
		if !containsK6Reason(refs, want) {
			t.Fatalf("source refs = %#v, want %q", refs, want)
		}
	}
}

// A plain declaration is still an unconditional statement, so the common
// `const ok = check(res, {...});` idiom must keep translating.
func TestConvertTranslatesCheckBoundToDeclaration(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';
import { check } from 'k6';

export default function () {
  const res = http.get('https://api.example.com/a');
  const ok = check(res, { 'ok': (r) => r.status === 200 });
}`
	result, err := Convert(ImportRequest{Name: "bound.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	action := result.Script.Actions[0]
	if len(action.Assertions) != 1 || action.Assertions[0].Expected != 200 {
		t.Fatalf("assertions = %#v, want status_code 200 from an assigned check()", action.Assertions)
	}
	if result.Stats.Checks != 1 {
		t.Fatalf("stats.Checks = %d, want 1", result.Stats.Checks)
	}
}

func TestParseK6StatusPredicate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		expression string
		status     int
		ok         bool
	}{
		{name: "bare parameter strict", expression: `r => r.status === 200`, status: 200, ok: true},
		{name: "parenthesised parameter", expression: `(res) => res.status === 404`, status: 404, ok: true},
		{name: "loose equality", expression: `r=>r.status==201`, status: 201, ok: true},
		{name: "renamed parameter", expression: `(response) => response.status === 500`, status: 500, ok: true},
		{name: "mismatched parameter", expression: `(r) => other.status === 200`, ok: false},
		{name: "inequality", expression: `(r) => r.status !== 200`, ok: false},
		{name: "comparison", expression: `(r) => r.status < 400`, ok: false},
		{name: "body predicate", expression: `(r) => r.body.length > 0`, ok: false},
		{name: "non-status field", expression: `(r) => r.timings.duration === 200`, ok: false},
		{name: "out-of-range literal", expression: `(r) => r.status === 999`, ok: false},
		{name: "dynamic expected", expression: `(r) => r.status === expected`, ok: false},
		{name: "function expression", expression: `function (r) { return r.status === 200; }`, ok: false},
		{name: "trailing expression", expression: `(r) => r.status === 200 && r.body !== ''`, ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, ok := parseK6StatusPredicate(test.expression)
			if ok != test.ok || status != test.status {
				t.Fatalf("parseK6StatusPredicate(%q) = %d, %v; want %d, %v", test.expression, status, ok, test.status, test.ok)
			}
		})
	}
}

func TestParseK6SleepSeconds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		args    []string
		seconds float64
		ok      bool
	}{
		{name: "integer", args: []string{"1"}, seconds: 1, ok: true},
		{name: "fraction", args: []string{"0.5"}, seconds: 0.5, ok: true},
		{name: "zero", args: []string{"0"}, seconds: 0, ok: true},
		{name: "identifier", args: []string{"pause"}, ok: false},
		{name: "call", args: []string{"randomIntBetween(1, 3)"}, ok: false},
		{name: "arithmetic", args: []string{"1 + 1"}, ok: false},
		{name: "negative", args: []string{"-1"}, ok: false},
		{name: "no arguments", args: nil, ok: false},
		{name: "extra arguments", args: []string{"1", "2"}, ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			seconds, ok := parseK6SleepSeconds(test.args)
			if ok != test.ok || seconds != test.seconds {
				t.Fatalf("parseK6SleepSeconds(%q) = %v, %v; want %v, %v", test.args, seconds, ok, test.seconds, test.ok)
			}
		})
	}
}

func TestParseK6StatusCheckRequiresEveryPropertyToBeUnderstood(t *testing.T) {
	t.Parallel()
	object := `{ 'status is 200': (r) => r.status === 200, 'has body': (r) => r.body.length > 0 }`
	if _, _, ok := parseK6StatusCheck([]string{"res", object}); ok {
		t.Fatal("a partially understood check must stay untranslated")
	}
	// Several properties that agree on one status are still one assertion.
	variable, status, ok := parseK6StatusCheck([]string{"res", `{ 'ok': (r) => r.status === 200, 'still ok': r => r.status == 200 }`})
	if !ok || variable != "res" || status != 200 {
		t.Fatalf("parseK6StatusCheck = %q, %v, %v; want res, 200, true", variable, status, ok)
	}
	// Two different statuses would become two AND-ed status_code assertions on
	// one action, and no response can satisfy both.
	if _, _, ok := parseK6StatusCheck([]string{"res", `{ 'ok': (r) => r.status === 200, 'created': (r) => r.status === 201 }`}); ok {
		t.Fatal("a check naming several statuses must stay untranslated")
	}
	if _, _, ok := parseK6StatusCheck([]string{"res", `{ 'ok': (r) => r.status === 200 }`, `{ tags: { name: 'catalog' } }`}); ok {
		t.Fatal("a tagged check carries behavior the importer does not reproduce")
	}
}

func TestConvertGatesChecksThatWouldContradictEachOther(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';
import { check } from 'k6';

export default function () {
  const res = http.get('https://api.example.com/catalog');
  check(res, { 'cache miss': (r) => r.status === 200 });
  check(res, { 'cache hit': (r) => r.status === 304 });
  const created = http.post('https://api.example.com/orders', '{"sku":"A"}');
  check(created, { 'created': (r) => r.status === 201, 'accepted': (r) => r.status === 202 });
}`
	result, err := Convert(ImportRequest{Name: "buckets.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %#v, want the two requests only", result.Script.Actions)
	}
	// The first check translates; the second names a status that could never
	// hold at the same time, so it must stay review evidence.
	first := result.Script.Actions[0]
	if len(first.Assertions) != 1 || first.Assertions[0].Expected != 200 {
		t.Fatalf("actions[0].assertions = %#v, want only the first status", first.Assertions)
	}
	// A single check naming two statuses is never translated at all.
	second := result.Script.Actions[1]
	if len(second.Assertions) != 0 {
		t.Fatalf("actions[1].assertions = %#v, want none for a multi-status check", second.Assertions)
	}
	for index, action := range result.Script.Actions {
		if !containsK6Reason(k6ImportReviewReasons(t, action), "untranslated_check") {
			t.Fatalf("actions[%d] contradictory check was not gated: %#v", index, action.Metadata)
		}
	}
	if result.Stats.Checks != 3 {
		t.Fatalf("stats = %#v, want all three checks counted", result.Stats)
	}
}

func TestConvertDoesNotTranslateLifecycleSleepOrCheckOntoIterationRequests(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';
import { check, sleep } from 'k6';

export function setup() {
  const login = http.post('https://api.example.com/login', '{}');
  check(login, { 'logged in': (r) => r.status === 200 });
}

export default function () {
  http.get('https://api.example.com/catalog');
}

export function teardown() {
  sleep(5);
}`
	result, err := Convert(ImportRequest{Name: "lifecycle.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	for index, action := range result.Script.Actions {
		if action.ThinkTimeAfter || action.ThinkTime.MeanMs != 0 {
			t.Fatalf("actions[%d] took think time from a non-iteration sleep(): %#v", index, action.ThinkTime)
		}
		if len(action.Assertions) != 0 {
			t.Fatalf("actions[%d] took an assertion from a non-iteration check(): %#v", index, action.Assertions)
		}
	}
	if result.Stats.Sleeps != 1 || result.Stats.Checks != 1 {
		t.Fatalf("stats = %#v, want the lifecycle sleep and check counted", result.Stats)
	}
	reasons := k6ImportReviewReasons(t, result.Script.Actions[0])
	for _, want := range []string{"untranslated_check", "untranslated_sleep"} {
		if !containsK6Reason(reasons, want) {
			t.Fatalf("reason codes = %#v, want %q for the lifecycle statements", reasons, want)
		}
	}
}

func TestConvertGatesCallsWithAnUndelimitedArgumentList(t *testing.T) {
	t.Parallel()
	const source = `import http from 'k6/http';
import { check } from 'k6';

export default function () {
  const res = http.get('https://api.example.com/catalog');
  check(res, {
    // sanity ( check
    'ok': (r) => r.status === 200,
  });
}`
	result, err := Convert(ImportRequest{Name: "unbalanced.js", Content: source})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Stats.Checks != 1 {
		t.Fatalf("stats = %#v, want the check counted even though its arguments could not be delimited", result.Stats)
	}
	action := result.Script.Actions[0]
	if len(action.Assertions) != 0 {
		t.Fatalf("assertions = %#v, want none for a check the importer could not parse", action.Assertions)
	}
	if !containsK6Reason(k6ImportReviewReasons(t, action), "untranslated_check") {
		t.Fatalf("undelimited check was not gated as untranslated: %#v", action.Metadata)
	}
}
