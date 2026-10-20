package qa_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Browser acceptance inventory (BE-4, D8): every BE-4 behavior names the
// tests that accept it and where each runs: in Chromium and WebKit, or as a
// unit test without a browser. The inventory generates
// docs/qa/BROWSER_ACCEPTANCE.md and fails when a named test is missing, when
// a test claimed for both engines does not name both, when a unit test
// starts a browser itself, or when an end-to-end spec does not run in both
// authentication profiles.

const browserAcceptancePath = "BROWSER_ACCEPTANCE.md"

type browserAcceptanceScope string

const (
	scopeBothEngines browserAcceptanceScope = "Chromium and WebKit"
	scopeUnit        browserAcceptanceScope = "Unit (no browser)"
)

type browserAcceptanceTest struct {
	// Ref is "<package dir>:<TestName>".
	Ref   string
	Scope browserAcceptanceScope
}

type browserAcceptanceItem struct {
	Behavior string
	Tests    []browserAcceptanceTest
}

func browserAcceptanceInventory() []browserAcceptanceItem {
	both := func(name string) browserAcceptanceTest {
		return browserAcceptanceTest{"pkg/protocol/browser:" + name, scopeBothEngines}
	}
	unit := func(ref string) browserAcceptanceTest {
		if !strings.Contains(ref, ":") {
			ref = "pkg/protocol/browser:" + ref
		}
		return browserAcceptanceTest{ref, scopeUnit}
	}
	return []browserAcceptanceItem{
		{"Cache and service workers", []browserAcceptanceTest{both("TestBrowserAcceptanceServiceWorkerServedResponses"), both("TestBrowserProtocolCacheDisabledReloadsCacheableAssets"), both("TestBrowserProtocolCheckNewerVersionsRevalidatesCachedAssets"), unit("TestTargetPolicyBlocksServiceWorkerBypassPaths"), unit("TestCDPJoinAssociatesDocumentsCacheAndInitiators"), unit("TestBrowserDocumentCohortReportsEffectiveNativeCacheSetting")}},
		{"Redirects", []browserAcceptanceTest{both("TestBrowserAcceptanceRedirectsHTTPErrorsAndTransportClose")}},
		{"Frames", []browserAcceptanceTest{both("TestBrowserProtocolExecutesFrameScopedActions"), both("TestBrowserProtocolExecutesNestedFramePathActions"), unit("TestLoaderTokenFromAnotherFrameProvesNothing")}},
		{"Cross-origin timing restrictions", []browserAcceptanceTest{both("TestBrowserAcceptanceCrossOriginResourcesKeepTheirTiming"), unit("TestWebKitNavigationRequestsNeverTakeAResourceTimingJoin")}},
		{"Long-running and background requests", []browserAcceptanceTest{both("TestBrowserResourceActualCompletionAcrossActions")}},
		{"Transport close", []browserAcceptanceTest{both("TestBrowserAcceptanceRedirectsHTTPErrorsAndTransportClose"), both("TestBrowserFailedSubresourceRetainsBoundedFailureEvidence")}},
		{"HTTP errors", []browserAcceptanceTest{both("TestBrowserAcceptanceRedirectsHTTPErrorsAndTransportClose"), both("TestBrowserFailedSubresourceRetainsBoundedFailureEvidence")}},
		{"Secret-bearing URLs, headers and bodies", []browserAcceptanceTest{both("TestBrowserActualInteractionRouteIsRuntimeRedacted"), unit("TestFinalizeHARCaptureRedactsLowEntropyRuntimeSecrets"), unit("TestFinalizeHARCaptureRedactsPercentEncodedRuntimeSecrets"), unit("TestApplyHARCaptureOptionsDisablesRawCaptureForRuntimeSecrets")}},
		{"Context close and crash", []browserAcceptanceTest{both("TestHAROwnershipReclaimsKilledNativeRecording"), both("TestBrowserDocumentActualBlockedDrainClosesOwnedContext"), unit("TestHAROwnershipReclaimsKilledGenerations")}},
		{"Never-finishing requests", []browserAcceptanceTest{both("TestNeverFinishingRequestFloodStaysBoundedAndCloses"), unit("TestHARInFlightBudgetUsesOutstandingRequests"), unit("TestHARInFlightBudgetDoesNotCapCompletedOrUnrecordedRequests"), unit("TestHARInFlightOverflowClosesAndCensorsThroughMonitor"), unit("TestHARInFlightOverflowCancellationRetainsExactWriter")}},
		{"Huge and chunked bodies", []browserAcceptanceTest{both("TestNeverEndingChunkedBodyIsStoppedByTheMemoryWatchdog"), unit("TestHARMonitorStopsOnActualFileSize")}},
		{"Metadata floods", []browserAcceptanceTest{unit("TestNetworkEntriesMetadataBudget"), unit("TestBoundBrowserNetworkHeadersLimitsEntriesAndBytes"), unit("TestNetworkEntriesRingAndByteBudget")}},
		{"Shared native finalization deadline and physical ownership", []browserAcceptanceTest{unit("TestHARFinalizationUsesOneDeadlineAcrossPhases"), unit("TestHARFinalizationExpiredJoinRetainsOwnershipUntilProof"), unit("TestHARFinalizationExpiryDuringScrubRefusesPublication"), unit("TestHARFinalizationExpiryAfterLinkRemovesOnlyOwnedPublication"), unit("TestHARFinalizationReportsExhaustionOnceWhileWriterIsBlocked")}},
		{"Flush-time quota exhaustion", []browserAcceptanceTest{unit("TestHAROverflowFailsTheSessionExplicitly"), unit("TestHARWritersReserveTheAggregateBudgetAndScratch")}},
		{"Recorder limits", []browserAcceptanceTest{unit("pkg/recorder/browser:TestValidateRecorderOptionsRejectsResourceBounds"), unit("pkg/recorder/browser:TestRecorderCapHitsAreRetainedInArtifact"), unit("pkg/recorder:TestReadRecorderHTTPRequestWithLimitRejectsOversizedHeaders")}},
		{"Cancellation", []browserAcceptanceTest{both("TestBrowserDocumentActualCancelledRunCannotInventFinality"), unit("TestRouteNetworkLatencyWaitIsCanceledByBrowserShutdown")}},
		{"Waterfall timing against independent fixture clocks and native HAR", []browserAcceptanceTest{both("TestBrowserAcceptanceWaterfallMatchesFixtureClockAndHAR")}},
		{"Old archives and synthetic fallback", []browserAcceptanceTest{unit("pkg/api:TestRunArchiveSyntheticHARLeavesNativeContextsOutAndScrubs"), unit("pkg/api:TestRunArchiveSyntheticHARDropsLegacyParquetHeaders"), unit("pkg/harsynth:TestSyntheticHARNeverInventsTimingsOrContent")}},
		{"Benchmark units and missing samples", []browserAcceptanceTest{unit("TestBrowserBenchmarkRSSPreservesRawBytesAndMissingness"), unit("TestBrowserBenchmarkV2ArtifactKeepsRawMemoryPopulation"), unit("TestBrowserBenchmarkRSSSampleDenominatorIncludesUnavailable"), unit("TestBrowserBenchmarkRSSRuntimeConversionRequiresAvailableFiniteSnapshot")}},
		{"Benchmark (opt-in)", []browserAcceptanceTest{both("TestBrowserBenchmark")}},
	}
}

// browserAcceptanceE2E is BE-4's end-to-end requirements and the Playwright
// tests ("<spec file>:<test title>") that prove them. LocalAuthOnly marks a
// row whose tests skip in the default profile, which has no second principal.
type browserAcceptanceE2E struct {
	Behavior      string
	Tests         []string
	LocalAuthOnly bool
}

func browserAcceptanceEndToEnd() []browserAcceptanceE2E {
	return []browserAcceptanceE2E{
		{Behavior: "Archive resource to HAR and archive ZIP downloads", Tests: []string{"workflow-browser-har.spec.ts:an archived run leads from its retained resources to HAR and archive downloads"}},
		{Behavior: "Viewer/editor separation and project denial", LocalAuthOnly: true, Tests: []string{"workflow-browser-har.spec.ts:viewers list HARs but cannot download them, and other projects are denied"}},
		{Behavior: "Retained missing values", Tests: []string{"workflow-retained-resource-inspector.spec.ts:retained resource inspector paginates beyond report previews and preserves native zero, false and independent clocks", "workflow-retained-resource-inspector.spec.ts:retained resource inspector refuses a mismatched page and retries without publishing unavailable evidence as zero", "workflow-browser-resource-evidence.spec.ts:archive document evidence retains latest pending, censored and unavailable measurements independently from actions"}},
	}
}

// browserAcceptanceTestBody finds a test's file and source.
func browserAcceptanceTestBody(t *testing.T, ref string) (string, string, bool) {
	t.Helper()
	dir, name, ok := strings.Cut(ref, ":")
	if !ok {
		t.Fatalf("malformed test reference %q", ref)
	}
	files, err := filepath.Glob(filepath.Join("..", "..", filepath.FromSlash(dir), "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	declaration := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\(t \*testing\.T\) \{`)
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		source := string(raw)
		location := declaration.FindStringIndex(source)
		if location == nil {
			continue
		}
		body := source[location[0]:]
		if end := strings.Index(body[1:], "\nfunc "); end > 0 {
			body = body[:end+1]
		}
		return filepath.Base(file), body, true
	}
	return "", "", false
}

// browserAcceptanceProfileSkip reports whether a spec's test (or its whole
// file) skips by e2e profile.
func browserAcceptanceProfileSkip(source, title string) bool {
	skip := regexp.MustCompile(`test\.(skip|fixme)\([^\n]*APEXLOAD_E2E_PROFILE`)
	start := strings.Index(source, "test('"+title+"'")
	if start < 0 {
		return false
	}
	body := source[start:]
	if end := strings.Index(body[1:], "\ntest("); end > 0 {
		body = body[:end+1]
	}
	head := source
	if first := strings.Index(source, "\ntest("); first > 0 {
		head = source[:first]
	}
	return skip.MatchString(body) || skip.MatchString(head)
}

func renderBrowserAcceptance(t *testing.T) string {
	var out strings.Builder
	out.WriteString("# Browser acceptance\n\n")
	out.WriteString("Generated by `docs/qa/browser_acceptance_test.go` (BE-4). Regenerate with `APEXLOAD_UPDATE_BROWSER_ACCEPTANCE=1 go test ./docs/qa -run TestBrowserAcceptanceInventory`.\n\n")
	out.WriteString("Each BE-4 behavior names the tests that accept it. **Chromium and WebKit** tests drive controlled pages in both engines; **Unit** tests exercise the same code without a browser (simulated CDP or WebKit events, files and budgets). The browser package's real-browser tests need installed Playwright browsers: without them they skip, or fail under `APEXLOAD_BROWSER_TESTS=require`. `scripts/run-optin-suites.sh --all` runs the browser package with both engines required and the acceptance benchmark, keeping the benchmark results, as the \"browser acceptance\" suite.\n\n")
	out.WriteString("| BE-4 behavior | Test | Runs in |\n| --- | --- | --- |\n")
	for _, item := range browserAcceptanceInventory() {
		for index, test := range item.Tests {
			file, body, found := browserAcceptanceTestBody(t, test.Ref)
			if !found {
				t.Errorf("%s: test %s does not exist", item.Behavior, test.Ref)
				continue
			}
			bothEngines := strings.Contains(body, `"chromium"`) && strings.Contains(body, `"webkit"`)
			startsBrowser := browserAcceptanceStartsBrowser(body)
			switch {
			case test.Scope == scopeBothEngines && !bothEngines:
				t.Errorf("%s: %s is listed for Chromium and WebKit but does not name both engines", item.Behavior, test.Ref)
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
	out.WriteString("\n## End to end\n\nThese Playwright tests run in both authentication profiles (default and local-auth), except where marked: the default profile has no second principal, so viewer and project-denial checks run in local-auth only.\n\n| BE-4 behavior | Spec | Test | Profiles |\n| --- | --- | --- | --- |\n")
	config, err := os.ReadFile(filepath.Join("..", "..", "dashboard", "playwright.config.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range browserAcceptanceEndToEnd() {
		for index, ref := range item.Tests {
			spec, title, _ := strings.Cut(ref, ":")
			source, err := os.ReadFile(filepath.Join("..", "..", "dashboard", "e2e", spec))
			if err != nil || !strings.Contains(string(source), "test('"+title+"'") {
				t.Errorf("%s: Playwright test %q is not in %s (%v)", item.Behavior, title, spec, err)
			}
			if !strings.Contains(string(config), "'**/"+spec+"'") {
				t.Errorf("%s: %s does not run in the local-auth profile", item.Behavior, spec)
			}
			// A test that skips by profile must be marked local-auth only.
			if skips := browserAcceptanceProfileSkip(string(source), title); skips != item.LocalAuthOnly {
				t.Errorf("%s: %q skips by profile = %t, but the row says local-auth only = %t", item.Behavior, title, skips, item.LocalAuthOnly)
			}
			behavior, profiles := "", "both"
			if index == 0 {
				behavior = item.Behavior
			}
			if item.LocalAuthOnly {
				profiles = "local-auth only"
			}
			out.WriteString(fmt.Sprintf("| %s | `dashboard/e2e/%s` | %s | %s |\n", behavior, spec, title, profiles))
		}
	}
	out.WriteString("\n## Benchmark\n\n`APEXLOAD_BROWSER_BENCHMARKS=1 go test ./pkg/protocol/browser -run '^TestBrowserBenchmark$' -v` measures each engine with a desktop and a mobile profile it accepts (Chromium: `desktop_1080p`, `pixel_8`; WebKit, which has no desktop profile: a 1920x1080 viewport, `iphone_15_pro`). Each runs five sessions in each of three modes: plain, document capture, and 200 ms of network latency. It records:\n\n")
	out.WriteString("- the host (OS and kernel version, CPU model and count, memory), the Playwright driver version and each browser's version;\n- cold launch (the engine's browser started from nothing) and session start against the pooled browser a generator keeps running;\n- navigation time, the overhead of document capture, and the throttled navigation's separation from the plain one;\n- largest observed RSS snapshot in bytes (nullable when unavailable), attempted/partial/unavailable snapshot counts, and CPU seconds of the observed test-process descendants including Playwright and browsers;\n- teardown time, and processes left once the browser closes;\n- failures.\n\n")
	out.WriteString("Each engine is a subtest, so an engine missing from the host skips or fails alone and the other's results are still written. It writes `browser-benchmark.json` to `APEXLOAD_BROWSER_BENCHMARK_OUT`. When `APEXLOAD_COMMIT_SHA` names the measured commit, it also writes `browser-benchmark-acceptance-requests.json`: one F4 feature acceptance request per engine and profile (`BE-4`, `browser-benchmark`), naming the results' SHA-256. A request is `pending` for a reviewer to judge against the published targets (launch, action overhead, RSS per VUser), or `failed` when sessions failed, processes were left or throttling did not separate; it never claims a pass by itself. Navigation time is absolute, not an overhead against a baseline. An operator posts the requests to `/api/v1/system/acceptance-records`. The Go benchmarks (`BenchmarkBrowser*`) the browser benchmark evidence names run with their own command.\n\n")
	out.WriteString("The published five-browser-VUser ceiling stays until reviewed evidence authorizes more. Synthetic shaping is a calibrated approximation, not a device equivalence claim.\n")
	return out.String()
}

func TestBrowserAcceptanceInventory(t *testing.T) {
	want := renderBrowserAcceptance(t)
	if t.Failed() {
		return
	}
	if os.Getenv("APEXLOAD_UPDATE_BROWSER_ACCEPTANCE") == "1" {
		if err := os.WriteFile(browserAcceptancePath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(browserAcceptancePath)
	if err != nil || string(got) != want {
		t.Fatalf("%s is stale or missing (%v); regenerate with APEXLOAD_UPDATE_BROWSER_ACCEPTANCE=1", browserAcceptancePath, err)
	}
	// The real-browser acceptance fixtures run as a registered opt-in suite.
	runner, err := os.ReadFile(filepath.Join("..", "..", "scripts", "run-optin-suites.sh"))
	if err != nil || !strings.Contains(string(runner), `run_suite "browser acceptance`) {
		t.Fatalf("scripts/run-optin-suites.sh does not register the browser acceptance suite (%v)", err)
	}
}

// Engine names in a serialized fixture do not launch a browser. Keep runtime
// admission checks separate from the two-engine coverage label.
func browserAcceptanceStartsBrowser(body string) bool {
	return strings.Contains(body, ".Init(") || strings.Contains(body, "requireBrowserRuntime(")
}

func TestBrowserAcceptanceRuntimeClassification(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		runtime    bool
	}{
		{"serialized engine labels", `results := []string{"chromium", "webkit"}`, false},
		{"initializer", `br.Init(ctx, config)`, true},
		{"runtime gate", `requireBrowserRuntime(t)`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := browserAcceptanceStartsBrowser(tc.body); got != tc.runtime {
				t.Fatalf("runtime = %v, want %v", got, tc.runtime)
			}
		})
	}
}
