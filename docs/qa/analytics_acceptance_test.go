package qa_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Native analytics acceptance inventory (NA-6 and NA-5's release evidence,
// B8): every NA-6 golden behavior names the tests that accept it and the
// build each runs in (the DuckDB build, `analytics_duckdb` with cgo, or the
// default build without native analytics), and the platform lanes that build
// and exercise the packaged controller are listed. The inventory generates
// docs/qa/ANALYTICS_ACCEPTANCE.md and fails when a named test is missing,
// when a test listed for the DuckDB build is not in an `analytics_duckdb`
// file (or one listed for the default build is), when a platform lane or its
// native checks are gone, or when the generator or collector depends on
// DuckDB.

const analyticsAcceptancePath = "ANALYTICS_ACCEPTANCE.md"

type analyticsBuild string

const (
	buildDuckDB  analyticsBuild = "DuckDB build"
	buildDefault analyticsBuild = "Default build"
)

type analyticsAcceptanceTest struct {
	Ref   string
	Build analyticsBuild
}

type analyticsAcceptanceItem struct {
	Behavior string
	Tests    []analyticsAcceptanceTest
}

func analyticsAcceptanceInventory() []analyticsAcceptanceItem {
	duck := func(ref string) analyticsAcceptanceTest { return analyticsAcceptanceTest{ref, buildDuckDB} }
	def := func(ref string) analyticsAcceptanceTest { return analyticsAcceptanceTest{ref, buildDefault} }
	return []analyticsAcceptanceItem{
		{"Long, wide and nested mappings", []analyticsAcceptanceTest{duck("pkg/analysis/duckdb:TestNativeAcceptanceMappingLayoutsAgree"), def("pkg/analysis:TestDecodeParquetMappingDefinitionRejectsUnsupportedAmbiguousAndMalformedFields")}},
		{"Decimals", []analyticsAcceptanceTest{duck("pkg/analysis/duckdb:TestNativeAcceptanceDecimalValues")}},
		{"Timezones and DST", []analyticsAcceptanceTest{duck("pkg/analysis/duckdb:TestNativeSummaryWallTimeWindowsAcrossDaylightSaving"), duck("pkg/analysis/duckdb:TestNativeAbsoluteTimestampRequiresSourceOffset"), duck("pkg/analysis/duckdb:TestNativeTimestampUsesEachRowsTimezoneOffset"), duck("pkg/analysis/duckdb:TestNativeLocalTimestampRejectsShortOffsetText"), duck("pkg/analysis/duckdb:TestNativeAbsoluteTimestampRejectsNaiveTypedColumns"), def("pkg/analysis:TestResolveLocalTimestamp"), def("pkg/analysis:TestResolveLocalTimestampLimits"), def("pkg/analysis:TestResolveLocalTimestampBoundsTransitionTraversal"), duck("pkg/analysis/duckdb:TestNativeLocalWallTimeRejectsAmbiguityWithRowProvenance"), duck("pkg/analysis/duckdb:TestNativeLocalTimestampGuardCoversSummaryAndHistogram"), duck("pkg/analysis/duckdb:TestNativeLocalTimestampQualifierRequiresAgreementAndCancellation")}},
		{"Nulls", []analyticsAcceptanceTest{duck("pkg/analysis/duckdb:TestNativeRejectionProvenanceAndNullBuckets"), duck("pkg/analysis/duckdb:TestNativeSummaryPartialWindowsDuplicatesNullsAndMissingCounts")}},
		{"Malformed files and rows", []analyticsAcceptanceTest{duck("pkg/analysis/duckdb:TestNativeHistogramRejectsMalformedRowsWithProvenance"), def("pkg/analysis:TestFlattenParquetSchemaRejectsMalformedTree"), def("pkg/analysis/duckdb:TestNativeWorkerModeNeverFallsThroughOnMalformedMarker")}},
		{"Schema drift across the files of an import", []analyticsAcceptanceTest{duck("pkg/analysis/duckdb:TestNativeAcceptanceSchemaDriftAcrossFiles"), duck("pkg/api:TestAnalysisAcceptanceDuplicateAndDriftedFiles")}},
		{"Raw versus summary and distribution inputs", []analyticsAcceptanceTest{duck("pkg/api:TestAnalysisSummaryImportPreviewCommitQueryReconcile"), duck("pkg/api:TestAnalysisHistogramImportCommitQueryReconcile"), def("pkg/api:TestAnalysisComparisonAdmitsSummaryOnlyStatisticsForDatasets")}},
		{"Units", []analyticsAcceptanceTest{def("pkg/api:TestCatalogNativeWireUnitsMatchExecutedQueries"), def("pkg/analysis:TestNormalizeNativeObservationUnitAliases")}},
		{"Duplicate files", []analyticsAcceptanceTest{duck("pkg/api:TestAnalysisAcceptanceDuplicateAndDriftedFiles"), def("pkg/api:TestDuplicateAnalysisImportFiles")}},
		{"Duplicate summary windows", []analyticsAcceptanceTest{duck("pkg/analysis/duckdb:TestNativeSummaryPartialWindowsDuplicatesNullsAndMissingCounts")}},
		{"Conflicting files (overlapping observations)", []analyticsAcceptanceTest{duck("pkg/analysis/duckdb:TestNativeAcceptanceConflictingFilesKeepEveryObservation")}},
		{"Resets", []analyticsAcceptanceTest{duck("pkg/analysis/duckdb:TestNativeCounterReadingsMarkResetsAndDuplicates"), duck("pkg/analysis/duckdb:TestNativeLongCumulativeHistogramDetectsResets"), def("pkg/api:TestImportedCounterDerivationsAreResetAware")}},
		{"Exact rejection provenance", []analyticsAcceptanceTest{duck("pkg/analysis/duckdb:TestNativeRejectionProvenanceAndNullBuckets"), duck("pkg/api:TestAnalysisRejectionDownloadIsDurableAndBounded")}},
		{"Preview, commit and query reconciliation", []analyticsAcceptanceTest{duck("pkg/api:TestAnalysisSummaryImportPreviewCommitQueryReconcile"), duck("pkg/api:TestAnalysisHistogramImportCommitQueryReconcile"), def("pkg/api:TestNativeReconciliationPinsStoreAtRealServerConstruction")}},
		{"Source non-mutation", []analyticsAcceptanceTest{duck("pkg/analysis/duckdb:TestNativeAcceptanceMappingLayoutsAgree"), duck("pkg/analysis/duckdb:TestNativeAbsoluteTimestampRequiresSourceOffset")}},
		{"Cross-project denial", []analyticsAcceptanceTest{def("pkg/api:TestAnalysisSavedMappingsProjectIsolationAndRevision"), def("pkg/api:TestAnalysisComparisonBoundsAndForeignRun"), def("pkg/api:TestAnalysisUploadCursorRefusesForeignReturnedImportIdentity")}},
		{"Cancellation, recovery and HA fences", []analyticsAcceptanceTest{def("pkg/api:TestAnalysisUploadCancellationReturnsWhileActualBodyReadRemainsOwned"), def("pkg/analysis/duckdb:TestNativeParentIOFenceBlocksPublicationAndRelease"), def("pkg/api:TestNativeUnlaunchedJobFencesBeforeStopAndRetainsFailedConfirmation")}},
		{"Deletion", []analyticsAcceptanceTest{def("pkg/api:TestAnalysisDatasetDeletionPlanValidatesEveryKeyBeforeDeleting"), def("pkg/api:TestAnalysisDeletionPinsBackendBeforeMetadataMutation")}},
		{"Raw count, sum and percentile reconciliation", []analyticsAcceptanceTest{duck("pkg/analysis/duckdb:TestNativeAcceptanceMappingLayoutsAgree"), duck("pkg/api:TestAnalysisHistogramImportCommitQueryReconcile"), duck("pkg/analysis/duckdb:TestNativeRejectsInvalidRowsAndSummaryPercentiles")}},
		{"Distribution copies and oversized pending publications", []analyticsAcceptanceTest{def("pkg/engine:TestDurationSketchCopyRejectsOversizedBeforeRetention"), def("pkg/distributed/generatorrpc:TestDistributionWireCopiesKeepExistingBounds"), def("pkg/distributed/generatorrpc:TestOversizedDistributionRefusalPreservesCountsAndCompatibleFallback"), def("pkg/api:TestJointPendingOversizeRefusalPreservesEarlierWindows"), def("pkg/distributed/generatorrpc:TestJointWireAdmissionBeforeRetainedCopies"), def("pkg/distributed/generatorrpc:TestJointWireAdmissionKeepsExactCardinalityBoundary"), def("pkg/distributed/generatorrpc:TestJointWireAdmissionThroughMetricsStreamPreservesOtherPopulations")}},
		{"v4 joint and rate coverage", []analyticsAcceptanceTest{def("pkg/api:TestJointEvidenceArchiveMatchesLocalWindowsAtStoreResolution"), def("pkg/api:TestNativeMergedRatesCheckCoveragePerSource"), def("pkg/api:TestNativeMergedRatesExactFractionalCoverage")}},
		{"Builds without native analytics report it unavailable", []analyticsAcceptanceTest{def("pkg/api:TestAnalysisCapabilitiesAdvertiseExactImportedIdentityVersionWithoutNativeAdmission"), def("pkg/api:TestNativePartitionEmptySelectionCannotBypassUnavailableBuild")}},
		{"Reference workload (opt-in)", []analyticsAcceptanceTest{duck("pkg/api:TestNativeAnalyticsReferenceBenchmark"), def("pkg/api:TestAnalyticsReferenceAcceptanceRequestsArePendingOrFailed")}},
	}
}

// analyticsAcceptancePlatforms are NA-5's documented prebuilt targets and the
// desktop workflow lane that builds and exercises each.
var analyticsAcceptancePlatforms = []struct{ Platform, Lane string }{
	{"linux/amd64", "Linux (ubuntu-24.04)"},
	{"linux/arm64", "Linux ARM64 (ubuntu-24.04-arm)"},
	{"darwin/arm64", "macOS Apple Silicon (macos-latest)"},
	{"windows/amd64", "Windows (windows-latest)"},
}

// analyticsAcceptanceAPIGate decides which lanes can compile pkg/api's test
// binary for the API check.
const (
	analyticsAcceptanceAPIGate  = "scripts/ci/pkg-api-test-build-fits.sh"
	analyticsAcceptanceAPICheck = "API native analytics contracts"
)

// analyticsAcceptanceLaneChecks are the native steps every desktop lane runs.
var analyticsAcceptanceLaneChecks = []struct{ Check, Command string }{
	{"Native analytics contracts", "go test -timeout 30m -tags analytics_duckdb ./pkg/analysis/... -run 'Test(Analysis|Native|Worker|Parquet|Lagged|BucketSeries|RCAImported)'"},
	{analyticsAcceptanceAPICheck, "go test -timeout 30m -tags analytics_duckdb ./pkg/api -run 'Test(Analysis|Native|Worker|Parquet|Lagged|BucketSeries|RCAImported)'"},
	{"Worker race checks", "go test -timeout 45m -race -tags analytics_duckdb ./pkg/analysis/duckdb -run 'Test(Worker|Native)'"},
	{"Controller build with DuckDB", "go build -tags analytics_duckdb -o \"${controller}\" ./cmd/controller"},
	{"Controller native worker startup", "python3 scripts/test_native_worker_startup.py --binary \"${controller}\""},
	{"CGO-free build reports unavailable", "CGO_ENABLED=0 go test ./pkg/analysis/duckdb"},
	{"Generator and collector exclude DuckDB", "go list -tags analytics_duckdb -deps ./cmd/generator ./cmd/apexload-collector-agent"},
	{"Packaged desktop native startup", "python3 scripts/test_native_worker_startup.py --desktop-build-dir desktop/build/bin"},
}

func renderAnalyticsAcceptance(t *testing.T) string {
	var out strings.Builder
	out.WriteString("# Native analytics acceptance\n\n")
	out.WriteString("Generated by `docs/qa/analytics_acceptance_test.go` (NA-6, and NA-5's release evidence). Regenerate with `APEXLOAD_UPDATE_ANALYTICS_ACCEPTANCE=1 go test ./docs/qa -run TestAnalyticsAcceptanceInventory`.\n\n")
	out.WriteString("Each NA-6 golden behavior names the tests that accept it and the build they run in. **DuckDB build** tests need the embedded engine (`go test -tags analytics_duckdb` with cgo; `scripts/run-optin-suites.sh` runs them as the \"analytics_duckdb\" suite); **Default build** tests run in every `go test ./...`. The new goldens' expected series values come from DuckDB's own SQL over the source files, independent of the normalization and series code; their counts, the conflicting-file aggregates and the DECIMAL beyond a double's precision are fixed expectations derived from the fixtures.\n\n")
	out.WriteString("| NA-6 behavior | Test | Build |\n| --- | --- | --- |\n")
	for _, item := range analyticsAcceptanceInventory() {
		for index, test := range item.Tests {
			file, _, found := browserAcceptanceTestBody(t, test.Ref)
			if !found {
				t.Errorf("%s: test %s does not exist", item.Behavior, test.Ref)
				continue
			}
			dir, name, _ := strings.Cut(test.Ref, ":")
			raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(dir), file))
			if err != nil {
				t.Fatal(err)
			}
			tagged := strings.HasPrefix(string(raw), "//go:build analytics_duckdb")
			if tagged != (test.Build == buildDuckDB) {
				t.Errorf("%s: %s is listed for the %s but its file's analytics_duckdb build tag is %t", item.Behavior, test.Ref, test.Build, tagged)
			}
			behavior := ""
			if index == 0 {
				behavior = item.Behavior
			}
			out.WriteString(fmt.Sprintf("| %s | `%s` (`%s/%s`) | %s |\n", behavior, name, dir, file, test.Build))
		}
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "desktop.yml"))
	if err != nil {
		t.Fatal(err)
	}
	// Only the four-platform desktop job counts: other jobs (the macOS
	// recording agent) repeat some of the same commands.
	workflow := analyticsAcceptanceDesktopJob(t, string(raw))
	out.WriteString("\n## Platforms\n\nNA-5 names four prebuilt targets. Each is a lane of `.github/workflows/desktop.yml`, which builds the controller with the embedded engine and the packaged desktop app on that platform and runs the native checks below; a lane that ran is that platform's evidence. Its results are CI artifacts (`native-startup-*.json`); no F4 acceptance record is written, so an operator posts each platform's record from them. A local run proves only the host it ran on.\n\n| Platform | Desktop lane |\n| --- | --- |\n")
	for _, platform := range analyticsAcceptancePlatforms {
		if !strings.Contains(workflow, "platform: "+platform.Platform) {
			t.Errorf("desktop.yml has no %s lane", platform.Platform)
		}
		out.WriteString(fmt.Sprintf("| %s | %s |\n", platform.Platform, platform.Lane))
	}
	out.WriteString("\nEvery lane runs, in order:\n\n")
	for _, check := range analyticsAcceptanceLaneChecks {
		if !strings.Contains(workflow, check.Command) {
			t.Errorf("desktop.yml no longer runs %q (%s)", check.Command, check.Check)
		}
		out.WriteString(fmt.Sprintf("- %s: `%s`\n", check.Check, check.Command))
	}
	for _, check := range analyticsAcceptanceLaneChecks {
		gated := regexp.MustCompile(`if ` + regexp.QuoteMeta(analyticsAcceptanceAPIGate) + `; then\n\s+` + regexp.QuoteMeta(check.Command) + `\n`)
		if check.Check == analyticsAcceptanceAPICheck && !gated.MatchString(workflow) {
			t.Errorf("desktop.yml no longer runs %q behind %s, as the note below says", check.Command, analyticsAcceptanceAPIGate)
		}
	}
	out.WriteString("\nThe API check runs only where `" + analyticsAcceptanceAPIGate + "` succeeds. Compiling pkg/api's test binary peaks near 6 GB of memory, too much for the macOS Apple Silicon lane's 7 GB hosted runner, so no macOS lane runs the API check: the Linux and Windows lanes do. The macOS lane's native checks above still run.\n")
	out.WriteString("\n## Reference workload\n\n`APEXLOAD_ANALYTICS_BENCHMARK=1 go test -tags analytics_duckdb ./pkg/api -run '^TestNativeAnalyticsReferenceBenchmark$' -v` generates a reproducible fixture: one ZSTD-compressed Parquet file per day, 3,456,000 rows each (25 ms apart, values derived from a hash of the row), until the files reach `APEXLOAD_ANALYTICS_BENCHMARK_BYTES` (default 1 GiB, at most 6 GiB; at most 366 files, the span one series query may cover). It imports the fixture through the API, then times a selected-window query (one hour of the last day at 60-second buckets, cold and 20 times repeated), a full-range query and the cancellation of a heavy query. It records the fixture's hashes and rows, upload and normalization time, pruned objects and avoided bytes, peak RSS sampled for the controller, its workers and both together, and every failure; a fixture short of its target fails. The interactive target is a selected-window P95 of at most five seconds on the 4 vCPU / 16 GiB reference host; the results say whether the host had that shape.\n\n")
	out.WriteString("With `APEXLOAD_ANALYTICS_BENCHMARK_OUT` it writes `analytics-reference.json` there, and, when `APEXLOAD_COMMIT_SHA` names the measured commit, `analytics-reference-acceptance-requests.json`: one F4 request (`NA-6`, `analytics-reference`), `pending` when the run had no failure and met the target, `failed` otherwise, never `passed` by itself. `scripts/run-optin-suites.sh --all` runs it as the \"native analytics reference workload\" suite. It does not measure spill bytes, live telemetry lag under a concurrent run, a cold replica, a worker crash, disk exhaustion or contention, and no result closes the waived 10 GB benchmark.\n")
	return out.String()
}

// analyticsAcceptanceDesktopJob is the text of desktop.yml's `desktop` job:
// from its key to the next job's.
func analyticsAcceptanceDesktopJob(t *testing.T, workflow string) string {
	t.Helper()
	start := strings.Index(workflow, "\n  desktop:\n")
	if start < 0 {
		t.Fatal("desktop.yml has no desktop job")
		return ""
	}
	job := workflow[start+1:]
	if next := regexp.MustCompile(`\n  [A-Za-z0-9_-]+:\n`).FindStringIndex(job[1:]); next != nil {
		job = job[:next[0]+1]
	}
	return job
}

func TestAnalyticsAcceptanceInventory(t *testing.T) {
	want := renderAnalyticsAcceptance(t)
	if t.Failed() {
		return
	}
	if os.Getenv("APEXLOAD_UPDATE_ANALYTICS_ACCEPTANCE") == "1" {
		if err := os.WriteFile(analyticsAcceptancePath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(analyticsAcceptancePath)
	if err != nil || string(got) != want {
		t.Fatalf("%s is stale or missing (%v); regenerate with APEXLOAD_UPDATE_ANALYTICS_ACCEPTANCE=1", analyticsAcceptancePath, err)
	}
	runner, err := os.ReadFile(filepath.Join("..", "..", "scripts", "run-optin-suites.sh"))
	if err != nil || !strings.Contains(string(runner), `run_suite "native analytics reference workload`) {
		t.Fatalf("scripts/run-optin-suites.sh does not register the native analytics reference workload (%v)", err)
	}
}

// The generator and collector never link the embedded engine, even when the
// DuckDB build tag is set (NA-5).
func TestNativeAnalyticsStaysOutOfGeneratorsAndCollectors(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("the go tool is not on PATH")
	}
	command := exec.Command(goTool, "list", "-tags", "analytics_duckdb", "-deps", "./cmd/generator", "./cmd/apexload-collector-agent")
	command.Dir = filepath.Join("..", "..")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, output)
	}
	for _, dependency := range strings.Fields(string(output)) {
		if strings.HasPrefix(dependency, "github.com/duckdb/") || strings.HasPrefix(dependency, "github.com/marcboeker/go-duckdb") {
			t.Fatalf("the generator or collector depends on %s", dependency)
		}
	}
}
