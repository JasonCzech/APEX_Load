package qa_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Browser intelligence acceptance inventory (BI-4, E7): every BI-4 golden
// behavior names the tests that accept it and where each runs: in Chromium
// and WebKit, in Chromium alone (an API only Chromium exposes), or as a unit
// test without a browser. The inventory generates
// docs/qa/BROWSER_INTELLIGENCE_ACCEPTANCE.md and fails when a named test is
// missing, when a real-browser test does not start the engines it is listed
// for, when a unit test starts a browser, or when an end-to-end spec does not
// run in both authentication profiles.

const browserIntelligenceAcceptancePath = "BROWSER_INTELLIGENCE_ACCEPTANCE.md"

const scopeChromium browserAcceptanceScope = "Chromium (the only engine exposing it)"

func browserIntelligenceAcceptanceInventory() []browserAcceptanceItem {
	both := func(ref string) browserAcceptanceTest { return browserAcceptanceTest{ref, scopeBothEngines} }
	chromium := func(ref string) browserAcceptanceTest { return browserAcceptanceTest{ref, scopeChromium} }
	unit := func(ref string) browserAcceptanceTest { return browserAcceptanceTest{ref, scopeUnit} }
	const golden = "pkg/api:TestBrowserIntelligenceGoldenAcceptance"
	return []browserAcceptanceItem{
		{"Known layout shifts (CLS equals the page's own largest session window)", []browserAcceptanceTest{both(golden), chromium("pkg/protocol/browser:TestBrowserDocumentActualCLSSessionWindows"), unit("pkg/api:TestBrowserFindingsUseTheExactP75")}},
		{"Interactions against dispatched ground truth", []browserAcceptanceTest{both(golden), both("pkg/protocol/browser:TestBrowserActualInteractionsMatchDispatchedGroundTruth"), both("pkg/protocol/browser:TestBrowserActualInteractionOutcomesTargetsAndNavigation"), both("pkg/protocol/browser:TestBrowserActualKeyboardTargetsAfterTheGesture"), unit("pkg/api:TestRetriesDeadInteractionsAndUnexpectedNavigation")}},
		{"Regressions against a baseline", []browserAcceptanceTest{unit("pkg/rca:TestBrowserBaselineFindsASeededRegression"), unit("pkg/rca:TestBrowserBaselineNeedsAPracticalEffect"), unit("pkg/rca:TestBrowserBaselineFollowsThePinnedBrowserPolicy")}},
		{"Missing and unsupported metrics (unavailable with a reason, never zero; the score stays unscored)", []browserAcceptanceTest{both(golden), both("pkg/protocol/browser:TestBrowserDocumentActualNativeIdentityAndNoInteraction"), unit("pkg/api:TestBrowserScoreNeedsEveryRequiredMetric"), unit("pkg/api:TestBrowserFindingsAreSuppressedOrUnscoredWithAReason"), unit("pkg/rca:TestBrowserBaselineLeavesWhatItCannotJudgeUnscored")}},
		{"Repeated publications counted once", []browserAcceptanceTest{both(golden), unit("pkg/api:TestBrowserDocumentRevisionsAndReportDisplayBound"), unit("pkg/api:TestBrowserDocumentArchiveMeasurementCapacityRetriesDoNotInventLoss"), unit("pkg/api:TestBrowserIntelligenceRunsAutomaticallyAndDedupes")}},
		{"Synthetic repeated taps", []browserAcceptanceTest{both(golden), both("pkg/protocol/browser:TestBrowserActualTouchGesturesSaySyntheticDispatch"), unit("pkg/api:TestRepeatedScriptedTapsAreScriptedRepetition"), unit("pkg/api:TestCapturedTapsRepeatOnOneActionWithoutPoints"), unit("pkg/api:TestEngineRetriesAreOneRetryNotRepetition")}},
		{"Layout mismatch kept apart", []browserAcceptanceTest{unit("pkg/api:TestHeatmapsKeepIncompatibleLayoutsApart"), unit("pkg/api:TestHeatmapsHoldOnlyMappedGesturesAndStateTheirSpace")}},
		{"Failed journey steps", []browserAcceptanceTest{both(golden), unit("pkg/api:TestJourneyAccountingClassifiesEveryAttempt"), unit("pkg/api:TestJourneyInterruptionsCensorWhatTheyCut"), unit("pkg/api:TestJourneyAccountingCensorsBoundariesFailedAfterAnInterruption")}},
		{"Scoring rules", []browserAcceptanceTest{both(golden), unit("pkg/api:TestBrowserScoreWeighsQualifiedComponents"), unit("pkg/api:TestBrowserFindingEvidenceAndQuality"), unit("pkg/api:TestBrowserCollectionHealthIsKeptApart")}},
		{"Exclusion rules", []browserAcceptanceTest{unit("pkg/rca:TestBrowserBaselineExcludesBucketsOutsideSteadyLoadAndCarriesSourceLoss"), unit("pkg/rca:TestBrowserBaselineRefusesABaselineWhoseScriptChanged"), unit("pkg/api:TestHeatmapsCountKeyboardTargetsAndExclusions")}},
		{"No added target traffic", []browserAcceptanceTest{both(golden), both("pkg/protocol/browser:TestBrowserDocumentActualNativeIdentityAndNoInteraction")}},
		{"Policy revisions never rewrite results", []browserAcceptanceTest{unit("pkg/api:TestRunPolicyPinsFreezeTheDefaultsInForceAtStart"), unit("pkg/api:TestAnalysisPoliciesRevisionsDefaultsAndDeletion"), unit("pkg/api:TestBrowserFindingIdentitiesAreStable")}},
		{"Report exports", []browserAcceptanceTest{unit("pkg/api:TestBrowserIntelligenceReportSectionsRenderTheirResults"), unit("pkg/api:TestBrowserIntelligenceReportSectionsSayWhyAResultIsMissing"), unit("pkg/api:TestBrowserIntelligenceIsAttachedOnlyForProfilesThatRenderIt"), unit("pkg/api:TestRootCauseSectionRendersOneModelInEveryFormat")}},
		{"Finding to resource to HAR entry", []browserAcceptanceTest{unit("pkg/api:TestRunArchiveBrowserHAREntryDrillDown")}},
		{"F4 acceptance requests (pending or failed, never passed by themselves)", []browserAcceptanceTest{unit("pkg/api:TestBrowserIntelligenceAcceptanceRequestsArePendingOrFailed")}},
	}
}

func browserIntelligenceAcceptanceEndToEnd() []browserAcceptanceE2E {
	return []browserAcceptanceE2E{
		{Behavior: "Configuration: browser policies with revisions (the journey policy editor has component tests only)", Tests: []string{"workflow-browser-intelligence.spec.ts:browser policies save revisions, refuse stale edits and show past revisions"}},
		{Behavior: "Comparison, finding to resource to HAR entry, and exports", Tests: []string{"workflow-browser-intelligence.spec.ts:the Browser area leads from a finding to its document resources and HAR entry, and exports its sections"}},
		{Behavior: "Findings on demand; no score without evidence", Tests: []string{"workflow-browser-findings.spec.ts:browser findings are evaluated on demand and never score absent evidence"}},
		{Behavior: "Journeys under a pinned policy", Tests: []string{"workflow-journeys.spec.ts:a run pinned to a journey policy accounts its journeys from the archive"}},
	}
}

func renderBrowserIntelligenceAcceptance(t *testing.T) string {
	var out strings.Builder
	out.WriteString("# Browser intelligence acceptance\n\n")
	out.WriteString("Generated by `docs/qa/browser_intelligence_acceptance_test.go` (BI-4). Regenerate with `APEXLOAD_UPDATE_BROWSER_INTELLIGENCE_ACCEPTANCE=1 go test ./docs/qa -run TestBrowserIntelligenceAcceptanceInventory`.\n\n")
	out.WriteString("Each BI-4 golden behavior names the tests that accept it. **Chromium and WebKit** tests drive controlled pages in both engines; **Chromium** tests cover an API only Chromium exposes (the Layout Instability API); **Unit** tests exercise the same code without a browser. Real-browser tests need installed Playwright browsers. The golden runs only with `APEXLOAD_BROWSER_TESTS=require`; the other real-browser tests run whenever an engine is installed and skip when it is missing, unless `APEXLOAD_BROWSER_TESTS=require` makes a missing engine a failure. `scripts/run-optin-suites.sh --all` runs the golden in both engines as the \"browser intelligence acceptance\" suite.\n\n")
	out.WriteString("| BI-4 behavior | Test | Runs in |\n| --- | --- | --- |\n")
	for _, item := range browserIntelligenceAcceptanceInventory() {
		for index, test := range item.Tests {
			file, body, found := browserAcceptanceTestBody(t, test.Ref)
			if !found {
				t.Errorf("%s: test %s does not exist", item.Behavior, test.Ref)
				continue
			}
			namesChromium, namesWebKit := strings.Contains(body, `"chromium"`), strings.Contains(body, `"webkit"`)
			startsBrowser := strings.Contains(body, ".Init(") || strings.Contains(body, "requireBrowserRuntime(") || strings.Contains(body, "documentActualBrowser(") || strings.Contains(body, "interactionFixtureBrowser(") || strings.Contains(body, "runBrowserIntelligenceGolden(")
			switch {
			case test.Scope == scopeBothEngines && !(namesChromium && namesWebKit && startsBrowser):
				t.Errorf("%s: %s is listed for Chromium and WebKit but does not start both engines", item.Behavior, test.Ref)
			case test.Scope == scopeChromium && !(namesChromium && startsBrowser):
				t.Errorf("%s: %s is listed for Chromium but does not start it", item.Behavior, test.Ref)
			case test.Scope == scopeUnit && startsBrowser:
				t.Errorf("%s: %s is listed as a unit test but starts a browser", item.Behavior, test.Ref)
			}
			behavior := ""
			if index == 0 {
				behavior = item.Behavior
			}
			dir, name, _ := strings.Cut(test.Ref, ":")
			out.WriteString(fmt.Sprintf("| %s | `%s` (`%s/%s`) | %s |\n", behavior, name, dir, file, test.Scope))
		}
	}
	out.WriteString("\n## End to end\n\nThese Playwright tests run in both authentication profiles (default and local-auth).\n\n| BI-4 behavior | Spec | Test | Profiles |\n| --- | --- | --- | --- |\n")
	config, err := os.ReadFile(filepath.Join("..", "..", "dashboard", "playwright.config.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range browserIntelligenceAcceptanceEndToEnd() {
		for index, ref := range item.Tests {
			spec, title, _ := strings.Cut(ref, ":")
			source, err := os.ReadFile(filepath.Join("..", "..", "dashboard", "e2e", spec))
			if err != nil || !strings.Contains(string(source), "test('"+title+"'") {
				t.Errorf("%s: Playwright test %q is not in %s (%v)", item.Behavior, title, spec, err)
			}
			if !strings.Contains(string(config), "'**/"+spec+"'") {
				t.Errorf("%s: %s does not run in the local-auth profile", item.Behavior, spec)
			}
			if browserAcceptanceProfileSkip(string(source), title) {
				t.Errorf("%s: %q skips by profile, but every BI-4 end-to-end row runs in both", item.Behavior, title)
			}
			behavior := ""
			if index == 0 {
				behavior = item.Behavior
			}
			out.WriteString(fmt.Sprintf("| %s | `dashboard/e2e/%s` | %s | both |\n", behavior, spec, title))
		}
	}
	out.WriteString("\n## The golden\n\n`APEXLOAD_BROWSER_TESTS=require go test ./pkg/api -run '^TestBrowserIntelligenceGoldenAcceptance$' -v` runs a controlled page in Chromium and WebKit: a hero image the fixture server holds for 300 ms, three layout shifts in two session windows, three synthetic taps on one action, and a wait for a confirmation that never appears. The page's real document and interaction evidence and its actions' results go through the archive writer (the document evidence three times: a mid-run snapshot, then the final evidence twice), and the archive is read by the code that serves findings, interaction patterns and journeys. It checks:\n\n")
	out.WriteString("- each document metric against the page's own PerformanceObserver entries: CLS equals the largest session window (which differs from the sum of shifts), LCP and FCP equal their entries within 1 ms, and LCP is no earlier than the server's hold;\n- a metric missing from the engine's `PerformanceObserver.supportedEntryTypes` is unavailable as `unsupported`, never a value, its finding is unscored, and the run's score is unscored with that metric missing (WebKit has no Layout Instability API);\n- the blank document a context starts with carries no value;\n- the repeated publications are counted once, without a collection-health finding;\n- the findings and score under a policy that makes the held hero and the shifts poor and FCP good;\n- one scripted-repetition pattern of three taps, all excluded from heatmaps as synthetic dispatch;\n- one failed checkout journey attempt and a weighted failure index of 1;\n- the fixture server saw the page and its hero once each: capture and analysis added no target traffic.\n\n")
	out.WriteString("With `APEXLOAD_BI_ACCEPTANCE_OUT` it writes `browser-intelligence-golden.json` there. When `APEXLOAD_COMMIT_SHA` names the measured commit, it also writes `browser-intelligence-acceptance-requests.json`: one F4 feature acceptance request per engine (`BI-4`, `browser-intelligence-golden`), naming the results' SHA-256. A request is `pending` when the engine's golden held, for a reviewer to record against a release binary, and `failed` when it did not; it never claims a pass by itself. An operator posts the requests to `/api/v1/system/acceptance-records`. Baseline regressions are accepted by unit tests over seeded series: no two real runs at different load levels are compared here.\n")
	return out.String()
}

func TestBrowserIntelligenceAcceptanceInventory(t *testing.T) {
	want := renderBrowserIntelligenceAcceptance(t)
	if t.Failed() {
		return
	}
	if os.Getenv("APEXLOAD_UPDATE_BROWSER_INTELLIGENCE_ACCEPTANCE") == "1" {
		if err := os.WriteFile(browserIntelligenceAcceptancePath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(browserIntelligenceAcceptancePath)
	if err != nil || string(got) != want {
		t.Fatalf("%s is stale or missing (%v); regenerate with APEXLOAD_UPDATE_BROWSER_INTELLIGENCE_ACCEPTANCE=1", browserIntelligenceAcceptancePath, err)
	}
	runner, err := os.ReadFile(filepath.Join("..", "..", "scripts", "run-optin-suites.sh"))
	if err != nil || !strings.Contains(string(runner), `run_suite "browser intelligence acceptance`) {
		t.Fatalf("scripts/run-optin-suites.sh does not register the browser intelligence acceptance suite (%v)", err)
	}
}
