package api

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/archivecontract"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/hostnet"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

// A long but ordinary generator name: an identity that must survive.
const telemetryRealGenerator = "apexload-generator-7d9f8b6c5d-x4k2p"

// telemetrySentinelManifest plants credential and payload sentinels in every
// text field telemetry evidence carries (TEL-B): source and target addresses
// with credentials and tokens, a payload a run redaction rule matches in
// reasons and policy names, and secrets in counter and stage reasons,
// exclusion and render keys, policy names and descriptions, in both the run's
// provenance and its admission (initial) provenance. A second rule matches
// generator addresses, which identities never take.
func telemetrySentinelManifest(rule bool) *RunArchiveManifest {
	maxLost := int64(0)
	// An imported definition can hold any stage text; its criterion ID
	// carries it.
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "fail_ci", MinWallTimeCoverage: acceptanceRatio(0.9), MaxLostRecords: &maxLost, Description: "desc password=DESC_SECRET",
		StageLagPercentiles: []TelemetryLagPercentileRule{{Stage: "lag password=LAG_SECRET", Percentile: "p95", MaxMs: 500}}})
	manifest.TestID, manifest.TestName = "telemetry-sentinels", "Telemetry sentinels"
	if rule {
		// The rule matches the sentinel without spelling it: a run's
		// configured rules are provenance, shown as recorded.
		manifest.Provenance.RuntimeSettings = &RuntimeSettingsOverride{RedactionRules: []engine.RedactionRule{{Pattern: "SENTINEL_P[A-Z]+"}, {Pattern: `10\.0\.0\.\d+`}}}
	}
	manifest.TelemetryCoverage = coverageRecord(70_000, 100_000)
	manifest.TelemetryCoverage.Coverage.Sources = append(manifest.TelemetryCoverage.Coverage.Sources, TelemetrySourceCoverage{Source: telemetryRealGenerator + ":9090", ObservedMs: 1, RequiredMs: 1})
	manifest.TelemetryCoverage.Coverage.Sources[0].Source = "https://user:URL_PASS@gen.example/x?token=URL_TOKEN password=SOURCE_SECRET"
	manifest.TelemetryLoss = &TelemetryLossAccounting{DroppedResults: &TelemetryCounterTotal{State: TelemetryCounterLowerBound, Value: 4, Reason: "reason password=REASON_SECRET SENTINEL_PRIVATE"}}
	manifest.TelemetryStageDistributions = []TelemetryStageDistribution{
		{Stage: telemetryStageTransport, Availability: "unavailable", Reason: "stage token=STAGE_SECRET"},
		{Stage: telemetryStageBrowserRender, Availability: "measured", Samples: 1, Excluded: map[string]int64{"excluded password=EXCL_SECRET": 2}},
	}
	manifest.TelemetryRenderPopulation = &TelemetryRenderPopulation{Population: telemetryRenderPopulationName, ReferenceClient: "ref token=REF_SECRET", Eligible: 1, Unqualified: map[string]int64{"unq password=UNQ_SECRET": 1}}
	manifest.Provenance.PolicyPins[0].Name = "Gate password=PIN_SECRET SENTINEL_PRIVATE"
	manifest.Provenance.PolicyPins[0].ID, manifest.Provenance.PolicyPins[0].Source = "gate token=PINID_SECRET", "src password=PINSRC_SECRET"
	manifest.Provenance.TelemetryAcceptancePolicy.Pin = manifest.Provenance.PolicyPins[0]
	manifest.Provenance.PolicyPins = append(manifest.Provenance.PolicyPins, RunPolicyPin{Kind: storage.AnalysisDefinitionKindTelemetryPolicy, ID: "budgets", Name: "Budgets password=BUDGET_SECRET", Revision: 2, SHA256: "def"})
	manifest.Provenance.GeneratorShards = []GeneratorShardSummary{{Group: "buyers", Target: "https://user:SHARD_PASS@gen.example:9090"}}
	manifest.Provenance.GeneratorInventory = []GeneratorInventoryEntry{{Target: "inv token=INV_SECRET", GeneratorID: telemetryRealGenerator}}
	manifest.Provenance.Assignment = &GeneratorAssignmentSettings{SelectedTargets: []string{"https://user:PROV_SEL_PASS@gen.example:9090"}}
	manifest.Provenance.Groups = []TestGroup{{Name: "buyers", Assignment: &GeneratorAssignmentSettings{SelectedTargets: []string{"https://user:GROUP_SEL_PASS@gen.example:9090"}}}}
	// Two generators a run rule would make alike, one of them not collected:
	// the completeness finding names it.
	manifest.SourceCoverage = &RunArchiveSourceCoverage{Mode: "required", Sources: []archivecontract.RunArchiveSource{{Target: "10.0.0.1:50051", Collected: true}, {Target: "https://user:COVER_PASS@10.0.0.2:50051"}}}
	manifest.HostNetwork = []hostnet.RunEvidence{{Source: "host password=HOST_SECRET"}}
	// Keys an imported manifest can hold: the budget policy's, the stage
	// digests' and a policy segment's.
	manifest.Provenance.TelemetryPolicy = &TelemetryBudgetPolicy{PolicyID: "policy token=POLICYID_SECRET", CadenceScope: "https://u:CADENCE_PASS@x", Budgets: []TelemetryStageBudget{{Stage: "stage token=BUDGETSTAGE_SECRET"}}}
	manifest.TelemetryStageDigests = map[string]engine.TDigestSnapshot{"browser_render token=DIGEST_SECRET": {}}
	manifest.TelemetryCoverage.Segments = []TelemetryPolicySegment{{PolicyHash: "hash password=SEGHASH_SECRET"}}
	initial := manifest.Provenance
	initial.Groups = cloneArchiveTestGroups(manifest.Provenance.Groups)
	initial.PolicyPins = cloneRunPolicyPins(manifest.Provenance.PolicyPins)
	initial.TelemetryAcceptancePolicy = cloneTelemetryAcceptancePolicySnapshot(manifest.Provenance.TelemetryAcceptancePolicy)
	manifest.InitialProvenance = &initial
	return manifest
}

var telemetryCredentialSentinels = []string{"URL_PASS", "URL_TOKEN", "SOURCE_SECRET", "REASON_SECRET", "STAGE_SECRET", "EXCL_SECRET", "UNQ_SECRET", "REF_SECRET", "PIN_SECRET", "BUDGET_SECRET", "DESC_SECRET", "SHARD_PASS", "INV_SECRET", "COVER_PASS", "HOST_SECRET", "SEL_PASS", "TM_SHARD_PASS", "TM_INV_PASS",
	"LAG_SECRET", "PINID_SECRET", "PINSRC_SECRET", "PROV_SEL_PASS", "GROUP_SEL_PASS", "CFG_SEL_PASS", "CFG_SHARD_PASS", "CFG_GROUP_PASS", "SCRIPT_SEL_PASS",
	"POLICYID_SECRET", "CADENCE_PASS", "BUDGETSTAGE_SECRET", "DIGEST_SECRET", "SEGHASH_SECRET", "TM_CADENCE_PASS", "TM_STAGE_SECRET", "TM_AVAIL_SECRET"}

// telemetrySentinelTestManifest is a generator-assignment manifest whose
// generator lists, configurations, groups and scripts carry credentials in
// generator targets.
func telemetrySentinelTestManifest() *TestRunManifest {
	configuration := TestRunManifestConfiguration{Assignment: &GeneratorAssignmentSettings{SelectedTargets: []string{"https://user:CFG_SEL_PASS@gen.example:9090"}},
		GeneratorShards: []GeneratorShardSummary{{Group: "buyers", Target: "https://user:CFG_SHARD_PASS@gen.example:9090"}},
		Groups:          []TestGroup{{Name: "buyers", Assignment: &GeneratorAssignmentSettings{SelectedTargets: []string{"https://user:CFG_GROUP_PASS@gen.example:9090"}}}}}
	// A stored test manifest (an imported archive's) can hold any policy keys.
	policy := &TelemetryPolicyEvidence{Availability: "available token=TM_AVAIL_SECRET", Policy: &TelemetryBudgetPolicy{CadenceScope: "https://u:TM_CADENCE_PASS@x", Budgets: []TelemetryStageBudget{{Stage: "stage token=TM_STAGE_SECRET"}}}}
	return &TestRunManifest{TestID: "telemetry-sentinels", TelemetryPolicy: policy, InitialConfiguration: configuration, CurrentConfiguration: configuration,
		Scripts: []TestRunManifestScript{{Assignment: &GeneratorAssignmentSettings{SelectedTargets: []string{"https://user:SCRIPT_SEL_PASS@gen.example:9090"}}}},
		LoadGenerators: TestRunManifestLoadGenerators{
			Assignment:         &GeneratorAssignmentSettings{Mode: "selected", SelectedTargets: []string{"https://user:SEL_PASS@gen.example:9090"}},
			GeneratorShards:    []GeneratorShardSummary{{Group: "buyers", Target: "https://user:TM_SHARD_PASS@gen.example:9090", VUsers: 5}},
			GeneratorInventory: []GeneratorInventoryEntry{{Target: "https://user:TM_INV_PASS@gen.example:9090", GeneratorID: telemetryRealGenerator, Status: "ready"}},
		}}
}

func requireNoTelemetrySentinel(t *testing.T, projection, text string, sentinels ...string) {
	t.Helper()
	if len(sentinels) == 0 {
		sentinels = append(append([]string(nil), telemetryCredentialSentinels...), "SENTINEL_PRIVATE")
	}
	for _, sentinel := range sentinels {
		if strings.Contains(text, sentinel) {
			t.Fatalf("%s leaks %s", projection, sentinel)
		}
	}
}

// Every projection of a run's telemetry evidence scrubs its text with the
// runs' redaction rules and the shared credential scrubber: the run report
// (JSON, full HTML with its generator lists, CSV, DOCX and the telemetry
// section), the masked manifests the manifest endpoints and safe export serve,
// baseline comparisons (built now, and saved before scrubbing) and CI output.
// Generator identities lose their credentials but never take run rules, so
// two generators stay two. Authorized identities, counts, revisions and
// verdicts survive, and the caller's manifest keeps what was recorded (TEL-B).
func TestTelemetryProjectionsScrubSentinels(t *testing.T) {
	dir := t.TempDir()
	manifest := telemetrySentinelManifest(true)
	manifest.TransactionsFile, manifest.ResourceSamplesFile, manifest.IntervalSummariesFile = "transactions.jsonl", "resources.jsonl", "interval_summaries.jsonl"
	for _, name := range []string{manifest.TransactionsFile, manifest.ResourceSamplesFile, manifest.IntervalSummariesFile} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: dir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatal(err)
	}
	reportJSON, _ := json.Marshal(report)
	requireNoTelemetrySentinel(t, "report JSON", string(reportJSON))
	// The report renders the generator-assignment manifest the server
	// projects for it.
	testManifest := telemetrySentinelTestManifest()
	server := &Server{archiveService: &stubArchiveService{testManifest: testManifest}}
	projected := server.reportTestManifest("telemetry-sentinels")
	html, err := renderRunArchiveHTMLReportWithAnnotations(report, "engineer", nil, projected)
	if err != nil {
		t.Fatal(err)
	}
	requireNoTelemetrySentinel(t, "report HTML", string(html))
	for _, want := range []string{"gen.example:9090", telemetryRealGenerator, "10.0.0.2:50051", "not collected"} {
		if !strings.Contains(string(html), want) {
			t.Fatalf("report HTML lost %q", want)
		}
	}
	for name, projection := range map[string]*TestRunManifest{"test manifest view and safe export": maskTestRunManifest(testManifest), "report test manifest": projected} {
		encoded, _ := json.Marshal(projection)
		requireNoTelemetrySentinel(t, name, string(encoded))
		if !strings.Contains(string(encoded), telemetryRealGenerator) {
			t.Fatalf("%s lost a generator's name", name)
		}
	}
	if !strings.Contains(testManifest.LoadGenerators.GeneratorShards[0].Target, "TM_SHARD_PASS") || !strings.Contains(testManifest.CurrentConfiguration.Groups[0].Assignment.SelectedTargets[0], "CFG_GROUP_PASS") || !strings.Contains(testManifest.Scripts[0].Assignment.SelectedTargets[0], "SCRIPT_SEL_PASS") {
		t.Fatal("a test manifest projection scrubbed the caller's manifest")
	}
	var reportCSV bytes.Buffer
	if err := writeRunArchiveReportCSVWithProfile(report, &reportCSV, "engineer"); err != nil {
		t.Fatal(err)
	}
	requireNoTelemetrySentinel(t, "report CSV", reportCSV.String())
	var docx bytes.Buffer
	if err := writeRunArchiveReportDOCXWithProfileAndAnnotations(report, &docx, "engineer", nil, projected); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(docx.Bytes()), int64(docx.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var docxText strings.Builder
	for _, file := range archive.File {
		content, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(content)
		_ = content.Close()
		docxText.Write(data)
	}
	requireNoTelemetrySentinel(t, "report DOCX", docxText.String())
	data := &runArchiveHTMLReportData{Report: report}
	section := reportSectionRegistry["telemetry_acceptance"]
	sectionHTML, err := section.HTML(data)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	section.PDF(data, &lines)
	requireNoTelemetrySentinel(t, "report PDF lines", strings.Join(lines, "\n"))
	var sectionCSV bytes.Buffer
	writer := csv.NewWriter(&sectionCSV)
	if err := section.CSV(data, func(a, b, c, d string) error { return writer.Write([]string{a, b, c, d}) }); err != nil {
		t.Fatal(err)
	}
	writer.Flush()
	requireNoTelemetrySentinel(t, "report section CSV", sectionCSV.String())
	// Identities, counts, availability and the verdict survive.
	for _, want := range []string{telemetryRealGenerator + ":9090", "Gate", "≥ 4", "Telemetry verdict FAIL"} {
		if !strings.Contains(string(sectionHTML), want) {
			t.Fatalf("telemetry section lost %q", want)
		}
	}
	if report.TelemetryAcceptance == nil || report.TelemetryAcceptance.Outcome != TelemetryAcceptanceFail || report.TelemetryAcceptance.Policy.Revision != 3 {
		t.Fatalf("report verdict %+v", report.TelemetryAcceptance)
	}

	maskedManifest := maskRunArchiveManifest(manifest)
	masked, _ := json.Marshal(maskedManifest)
	requireNoTelemetrySentinel(t, "manifest view and safe export", string(masked))
	if !strings.Contains(string(masked), telemetryRealGenerator) {
		t.Fatal("the manifest view lost a generator's name")
	}
	// An archive imported from the safe export still names two generators.
	if sources := maskedManifest.SourceCoverage.Sources; sources[0].Target != "10.0.0.1:50051" || sources[1].Target == sources[0].Target {
		t.Fatalf("generator identities took a run rule: %+v", sources)
	}
	if finding := archiveSourceCoverageFinding(maskedManifest); finding == nil || !strings.Contains(finding.Message, "1 of 2") {
		t.Fatalf("imported source coverage finding %+v", finding)
	}

	// A baseline run without the rule is projected with both runs' rules.
	baseline := telemetrySentinelManifest(false)
	comparison := compareBaselineTelemetry(&baselineComparisonRun{identity: BaselineRunIdentity{ID: "current"}, manifest: manifest}, &baselineComparisonRun{identity: BaselineRunIdentity{ID: "baseline"}, manifest: baseline}, rca.WorkloadQualification{})
	comparisonJSON, _ := json.Marshal(comparison)
	requireNoTelemetrySentinel(t, "comparison JSON", string(comparisonJSON))
	requireNoTelemetrySentinel(t, "comparison line", baselineTelemetryComparisonLine(comparison))
	if current := comparison.Current; current.Acceptance == nil || current.Acceptance.Outcome != TelemetryAcceptanceFail || current.Loss == nil || current.Loss.DroppedResults.Value != 4 || current.PolicyPin == nil || current.PolicyPin.Revision != 2 {
		t.Fatalf("comparison profile lost evidence: %+v", current)
	}
	// A comparison saved before scrubbing is projected with the shared
	// scrubber: its runs' rules are not at hand, so their payloads are not.
	saved := BaselineTelemetryComparison{BaselineRunID: "baseline", Current: telemetryRunProfile("current", manifest), Baseline: telemetryRunProfile("baseline", baseline)}
	legacy, _ := json.Marshal(scrubBaselineTelemetryComparison(saved, telemetryTextScrubber{}))
	requireNoTelemetrySentinel(t, "saved comparison", string(legacy), telemetryCredentialSentinels...)

	response := ciRunResponse{Status: "COMPLETE", SLAEvaluated: true}
	applyCITerminalEvidence(&response, newCIValidatedArchiveEvidence(manifest, nil, nil, false), nil)
	finalizeCIRunOutcome(&response, ciRunMetadata{})
	ciJSON, _ := json.Marshal(response)
	requireNoTelemetrySentinel(t, "CI response", string(ciJSON))
	requireNoTelemetrySentinel(t, "CI check summary", strings.Join(ciGitHubCheckTelemetryLines(response.TelemetryVerdict), "\n"))
	if response.TelemetryVerdict == nil || response.TelemetryVerdict.Outcome != TelemetryAcceptanceFail {
		t.Fatalf("CI verdict %+v", response.TelemetryVerdict)
	}
	// The archived evidence keeps what was recorded.
	if !strings.Contains(manifest.TelemetryCoverage.Coverage.Sources[0].Source, "SOURCE_SECRET") || !strings.Contains(manifest.InitialProvenance.PolicyPins[0].Name, "PIN_SECRET") || !strings.Contains(manifest.Provenance.Groups[0].Assignment.SelectedTargets[0], "GROUP_SEL_PASS") {
		t.Fatal("a projection scrubbed the caller's manifest")
	}
}

// The scrubber applies the runs' distinct valid rules (one that no longer
// compiles leaves the others, and does not count), all of them before the
// shared scrubber, including past the engine's per-configuration limit;
// withholds rule-scrubbed text only past its bound on distinct rules; removes
// control and bidirectional formatting characters but keeps joiners; keeps a
// real criterion ID; and keeps an empty source list empty.
func TestTelemetryTextScrubberRulesAndShape(t *testing.T) {
	manifest := &RunArchiveManifest{}
	manifest.Provenance.RuntimeSettings = &RuntimeSettingsOverride{RedactionRules: []engine.RedactionRule{{Pattern: "("}, {Pattern: "GROUP_SECRET"}}}
	manifest.Provenance.Groups = []TestGroup{{Name: "g", RuntimeSettings: &RuntimeSettingsOverride{RedactionRules: []engine.RedactionRule{{Pattern: `"customerId": "C-\d+"`}, {Pattern: "GROUP_SECRET"}}}}}
	initial := manifest.Provenance
	manifest.InitialProvenance = &initial
	scrubber := newTelemetryTextScrubber(manifest)
	if len(scrubber.rules) != 1 || len(scrubber.rules[0].Rules) != 2 {
		t.Fatalf("rules not deduplicated: %+v", scrubber.rules)
	}
	// A later rule still sees the JSON as written: the shared scrubber, which
	// re-encodes JSON holding a sensitive key, runs after every rule.
	if got := scrubber.text(`{"customerId": "C-123", "password": "x"}`); strings.Contains(got, "C-123") {
		t.Fatalf("rules not applied before the shared scrubber: %q", got)
	}
	if got := scrubber.text("x GROUP_SECRET"); strings.Contains(got, "GROUP_SECRET") {
		t.Fatalf("group rule not applied: %q", got)
	}
	if got := scrubber.label("gen\u202Ea\u0007b\uFEFF"); got != "gen a b " {
		t.Fatalf("control and bidirectional characters kept: %q", got)
	}
	if got := scrubber.label("a\u200Db"); got != "a\u200Db" {
		t.Fatalf("a joiner was removed: %q", got)
	}
	// Identities never take run rules.
	if got := scrubTelemetryIdentity("gen GROUP_SECRET"); got != "gen GROUP_SECRET" {
		t.Fatalf("identity took a run rule: %q", got)
	}
	// A scenario at the engine's limit and a group with more distinct rules:
	// every rule applies, in order, before the shared scrubber.
	many := &RunArchiveManifest{}
	many.Provenance.RuntimeSettings = &RuntimeSettingsOverride{}
	for i := 0; i < engine.MaxRedactionRules; i++ {
		many.Provenance.RuntimeSettings.RedactionRules = append(many.Provenance.RuntimeSettings.RedactionRules, engine.RedactionRule{Pattern: fmt.Sprintf("R%dX", i)})
	}
	many.Provenance.Groups = []TestGroup{{Name: "g", RuntimeSettings: &RuntimeSettingsOverride{RedactionRules: []engine.RedactionRule{{Pattern: "("}, {Pattern: `"customerId": "C-\d+"`}}}}}
	chained := newTelemetryTextScrubber(many)
	if len(chained.rules) != 2 || chained.withheld {
		t.Fatalf("rules past the engine's limit were not chained: %d configurations", len(chained.rules))
	}
	// Pure JSON the shared scrubber re-encodes: the second configuration's
	// rule still sees it as written.
	if got := chained.text(`{"customerId": "C-123", "password": "x", "note": "R127X"}`); strings.Contains(got, "C-123") || strings.Contains(got, "R127X") {
		t.Fatalf("a chained rule was not applied before the shared scrubber: %q", got)
	}
	for i := 0; i < maxTelemetryRuleCandidates; i++ {
		many.Provenance.Groups[0].RuntimeSettings.RedactionRules = append(many.Provenance.Groups[0].RuntimeSettings.RedactionRules, engine.RedactionRule{Pattern: fmt.Sprintf("G%dX", i)})
	}
	if over := newTelemetryTextScrubber(many); over.text("R1X reason") != telemetryTextWithheld || over.label("R1X") != telemetryTextWithheld || scrubTelemetryIdentity("R1X") != "R1X" {
		t.Fatal("text was shown although the rules exceed what is read")
	}
	verdict := &TelemetryAcceptanceVerdict{Criteria: []TelemetryAcceptanceCriterion{{Criterion: "stage_lag:transport/p95", Reason: "GROUP_SECRET"}}}
	if got := scrubTelemetryVerdict(verdict, scrubber).Criteria[0]; got.Criterion != "stage_lag:transport/p95" || strings.Contains(got.Reason, "GROUP_SECRET") {
		t.Fatalf("criterion %+v", got)
	}
	manifest.TelemetryCoverage = &TelemetryCoverageRecord{Coverage: TelemetryCoverage{Sources: []TelemetrySourceCoverage{}}}
	if scrubbed := scrubTelemetryText(manifest, scrubber); scrubbed.TelemetryCoverage.Coverage.Sources == nil {
		t.Fatal("an empty source list became absent")
	}
}

// A comparison saved before telemetry text was scrubbed is served scrubbed
// through the saved-comparison route.
func TestSavedBaselineComparisonServesScrubbedTelemetry(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
	baseline := comparisonArchiveFixture(t, server, store, "baseline", 100, 100)
	saved := BaselineComparisonSnapshot{ID: "saved-raw", TelemetryComparisons: []BaselineTelemetryComparison{{BaselineRunID: baseline.ID, Scope: "whole_run", Reasons: []string{},
		Current: telemetryRunProfile(current.ID, telemetrySentinelManifest(true)), Baseline: telemetryRunProfile(baseline.ID, telemetrySentinelManifest(false))}}}
	payload, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveBaselineComparison(saved.ID, current.ProjectID, current.ID, payload); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/tests/current/baseline-comparisons/saved-raw", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Gate") {
		t.Fatalf("saved comparison = %d %s", response.Code, response.Body.String())
	}
	requireNoTelemetrySentinel(t, "saved comparison", response.Body.String(), telemetryCredentialSentinels...)
}

// The telemetry_acceptance event sent where the durable outbox is unavailable
// says what the scrubbed verdict says.
func TestTelemetryAcceptanceWebhookScrubsSentinels(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	server, store := baselineTestServer(t)
	if err := store.SaveTest(&storage.TestRecord{ID: "run-sentinel", ProjectID: storage.DefaultProjectID, Name: "run-sentinel", Status: "COMPLETE", StartTime: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	received := make(chan string, 1)
	hook := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		received <- string(body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer hook.Close()
	server.runner.notifier = newNotificationDispatcher(hook.Client())
	settings := server.runner.GetSystemSettings()
	settings.Notifications = NotificationsSettings{Webhooks: []NotificationWebhookSettings{{Enabled: true, URL: hook.URL, Format: notificationFormatSlack, Events: []string{"telemetry_acceptance"}}}}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	manifest := telemetrySentinelManifest(true)
	manifest.TestName = "nightly password=NAME_SECRET"
	server.notifyTelemetryAcceptance("run-sentinel", manifest)
	select {
	case body := <-received:
		requireNoTelemetrySentinel(t, "telemetry_acceptance webhook", body, append([]string{"NAME_SECRET", "SENTINEL_PRIVATE"}, telemetryCredentialSentinels...)...)
		if !strings.Contains(body, "nightly") || !strings.Contains(body, "fail") {
			t.Fatalf("webhook lost the run or verdict: %s", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no webhook")
	}
}

// The HTML report route renders the generator-assignment appendix from the
// scrubbed test manifest.
func TestRunReportRouteScrubsGeneratorTargets(t *testing.T) {
	dir := t.TempDir()
	manifest := telemetrySentinelManifest(true)
	manifest.TransactionsFile, manifest.ResourceSamplesFile, manifest.IntervalSummariesFile = "transactions.jsonl", "resources.jsonl", "interval_summaries.jsonl"
	for _, name := range []string{manifest.TransactionsFile, manifest.ResourceSamplesFile, manifest.IntervalSummariesFile} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{TestID: manifest.TestID, ArchivePath: dir, ManifestPath: manifestPath}
	server, _ := baselineTestServer(t)
	server.archiveService = &stubArchiveService{test: &ManagedTest{ID: manifest.TestID, ProjectID: storage.DefaultProjectID, Status: "COMPLETE", Archive: record}, testFound: true,
		archive: record, archiveFound: true, testManifest: telemetrySentinelTestManifest()}
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+manifest.TestID+"/report", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "gen.example:9090") {
		t.Fatalf("report = %d %.300s", response.Code, response.Body.String())
	}
	requireNoTelemetrySentinel(t, "HTML report route", response.Body.String())
}

// The run calendar's topology, live and archived, names generators as the
// run's other projections do.
func TestRunCalendarTopologyScrubsGeneratorTargets(t *testing.T) {
	manifest := telemetrySentinelManifest(true)
	archived := topologyForCalendar(nil, SystemInventoryResponse{}, manifest, time.Now())
	live := topologyForCalendar(&ManagedTest{Status: "RUNNING", GeneratorShards: manifest.Provenance.GeneratorShards}, SystemInventoryResponse{Generators: manifest.Provenance.GeneratorInventory}, nil, time.Now())
	for name, topology := range map[string]RunCalendarTopology{"archived": archived, "live": live} {
		encoded, _ := json.Marshal(topology)
		requireNoTelemetrySentinel(t, name+" calendar topology", string(encoded), "SHARD_PASS", "INV_SECRET")
		if !strings.Contains(string(encoded), telemetryRealGenerator) || len(topology.GeneratorShards) != 1 {
			t.Fatalf("%s calendar topology lost a generator: %s", name, encoded)
		}
	}
	if !strings.Contains(manifest.Provenance.GeneratorShards[0].Target, "SHARD_PASS") {
		t.Fatal("the calendar scrubbed the caller's manifest")
	}
}

// TestTelemetryTenConsumerParityAndSentinelMatrix verifies that all 10 telemetry
// consumer projections expose consistent telemetry SLA verdicts, stage
// distributions, render populations, coverage, policy segments, and validity
// findings while scrubbing every credential and run-rule sentinel (TEL-B2).
func TestTelemetryTenConsumerParityAndSentinelMatrix(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	dir := t.TempDir()
	now := time.Date(2026, 10, 28, 16, 0, 0, 0, time.UTC)
	start := now.Add(-10 * time.Second)

	manifest := telemetrySentinelManifest(true)
	manifest.CompletedAt = now
	manifest.Provenance.StartedAt = &start
	manifest.Provenance.ProjectID = storage.DefaultProjectID
	manifest.Provenance.RunIncarnation = "incarnation-parity-1"
	manifest.Provenance.Groups[0].RuntimeSettings = &RuntimeSettingsOverride{
		RedactionRules: []engine.RedactionRule{{Pattern: "GROUP_RULE_[A-Z]+"}},
	}

	// Inject additional sentinels into PolicySegments, PriorEpochs, ValidityFindings, and StageDistributions.
	p99 := 2450.0
	manifest.TelemetryStageDistributions = append(manifest.TelemetryStageDistributions, TelemetryStageDistribution{
		Stage:                telemetryStageEndToEnd,
		Unit:                 "ms",
		Availability:         "measured",
		Samples:              50,
		EligiblePublications: 50,
		P99Ms:                &p99,
		DeliverySignal:       "verified_frame_presentation?token=DELIVERY_TOKEN",
		Reason:               "e2e Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.E2E_BEARER_SECRET GROUP_RULE_SECRET",
		Excluded:             map[string]int64{"superseded api_key=SUPER_SECRET": 1},
	})
	manifest.TelemetryRenderPopulation.PriorEpochs = []TelemetryRenderPopulation{
		{
			Population:        telemetryRenderPopulationName,
			Epoch:             "epoch-prior?client_secret=PRIOR_EPOCH_SECRET",
			ReferenceClient:   "prior-ref password=PRIOR_REF_SECRET",
			PublishedEligible: 10,
			Eligible:          10,
			Rendered:          9,
			Unqualified:       map[string]int64{"hidden token=PRIOR_UNQ_SECRET": 1},
		},
	}
	segPolicy := &TelemetryBudgetPolicy{
		SchemaVersion:   "telemetry_policy.v1",
		PolicyID:        "seg-policy?api_key=SEGPOL_SECRET",
		Revision:        2,
		Hash:            "seghash?token=SEGHASH2_SECRET",
		CadenceScope:    "https://user:SEGCAD_PASS@gen.example",
		CoverageRule:    "qualified_source_windows",
		CaptureBoundary: "run_admission",
		Budgets:         []TelemetryStageBudget{{Stage: "sample_age?password=SEGSTAGE_SECRET", LimitMs: 1000, Enabled: true}},
	}
	manifest.TelemetryPolicySegments = []TelemetryPolicySegmentReceipt{
		{
			Sequence:    1,
			EffectiveAt: start,
			Source:      "admission?token=SEGSOURCE_SECRET",
			PolicyID:    "seg-policy?api_key=SEGPOL_SECRET",
			RequestedBy: "operator access_token=REQBY_BEARER_SECRET SENTINEL_PRIVATE GROUP_RULE_SECRET",
			Policy:      segPolicy,
		},
		{
			Sequence:    2,
			EffectiveAt: start.Add(5 * time.Second),
			Source:      "run_selection?token=SEGSOURCE2_SECRET",
			PolicyID:    "seg-policy?api_key=SEGPOL_SECRET",
			RequestedBy: "operator access_token=REQBY_BEARER_SECRET",
			Policy:      segPolicy,
		},
	}
	manifest.TelemetryValidityFindings = []TelemetryValidityFinding{
		{
			Criterion: "stage_budget_health",
			Status:    "passed",
			Compliant: true,
			Summary:   "stage health summary password=VAL_SUMMARY_SECRET SENTINEL_PRIVATE GROUP_RULE_SECRET",
			Details:   []string{"detail client_secret=VAL_DETAIL_SECRET"},
		},
		{
			Criterion:   "end_to_end_sample_age",
			Status:      "failed",
			Compliant:   false,
			ThresholdMs: 2000,
			ObservedMs:  &p99,
			Summary:     "e2e sample age summary api_key=VAL_E2E_SECRET",
		},
	}
	manifest.TelemetryAcceptance = telemetryAcceptanceForManifest(manifest)
	manifest.TelemetryEvidenceFile = telemetryEvidenceFileName

	evidenceDoc := TelemetryEvidenceDocument{
		SchemaVersion:      telemetryEvidenceSchema,
		TestID:             manifest.TestID,
		CapturedAt:         now,
		CoverageAlgorithm:  telemetryCoverageAlgorithm,
		CoverageRule:       "qualified_source_windows",
		Coverage:           manifest.TelemetryCoverage,
		PolicySegments:     manifest.TelemetryPolicySegments,
		SegmentCoverage:    manifest.TelemetryCoverage.Segments,
		Intervals:          []TelemetryEvidenceInterval{{StartAt: start, EndAt: now, DurationMs: 10000, PolicySegment: 1, PolicyHash: "hash?token=IVHASH_SECRET", Status: "degraded", ConditionLabels: []string{"degraded_stage_budget"}, Unscored: []string{"unscored password=IVUNSCORED_SECRET SENTINEL_PRIVATE"}}},
		ResourceSamples:    []TelemetryResourceSample{{Timestamp: now, Component: "generator", Source: "https://user:RES_PASS@gen.example:9090", QueueDepth: 4, ByteVolume: 1024, CPUPercent: 15, RSSBytes: 4096}},
		RenderPopulation:   manifest.TelemetryRenderPopulation,
		StageDistributions: manifest.TelemetryStageDistributions,
		ValidityFindings:   manifest.TelemetryValidityFindings,
		Acceptance:         manifest.TelemetryAcceptance,
	}
	evidenceBytes, err := json.MarshalIndent(evidenceDoc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, telemetryEvidenceFileName), evidenceBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	manifest.TransactionsFile, manifest.ResourceSamplesFile, manifest.IntervalSummariesFile = "transactions.jsonl", "resources.jsonl", "interval_summaries.jsonl"
	for _, name := range []string{manifest.TransactionsFile, manifest.ResourceSamplesFile, manifest.IntervalSummariesFile} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest.Artifacts = []RunArchiveArtifact{
		{Name: telemetryEvidenceFileName, Kind: "evidence", Format: "json", SizeBytes: int64(len(evidenceBytes)), SHA256: sha256BytesHex(evidenceBytes)},
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	allSentinels := append([]string{
		"SENTINEL_PRIVATE",
		"GROUP_RULE_SECRET",
		"DELIVERY_TOKEN",
		"E2E_BEARER_SECRET",
		"SUPER_SECRET",
		"PRIOR_EPOCH_SECRET",
		"PRIOR_REF_SECRET",
		"PRIOR_UNQ_SECRET",
		"SEGPOL_SECRET",
		"SEGHASH2_SECRET",
		"SEGCAD_PASS",
		"SEGSTAGE_SECRET",
		"SEGSOURCE_SECRET",
		"SEGSOURCE2_SECRET",
		"REQBY_BEARER_SECRET",
		"VAL_SUMMARY_SECRET",
		"VAL_DETAIL_SECRET",
		"VAL_E2E_SECRET",
		"IVHASH_SECRET",
		"IVUNSCORED_SECRET",
		"RES_PASS",
		"DIAG_SUMMARY_SECRET",
		"DIAG_COND_SECRET",
		"DIAG_ISSUE_SECRET",
		"HEALTH_SUMMARY_SECRET",
	}, telemetryCredentialSentinels...)

	server, store := baselineTestServer(t)
	record := &storage.RunArchiveRecord{
		TestID:         manifest.TestID,
		ArchivePath:    dir,
		ManifestPath:   manifestPath,
		ManifestSHA256: sha256BytesHex(manifestBytes),
	}
	if err := store.SaveTest(&storage.TestRecord{
		ID:        manifest.TestID,
		ProjectID: storage.DefaultProjectID,
		Name:      manifest.TestName,
		Status:    "COMPLETE",
		StartTime: start,
		EndTime:   &now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}

	liveHealth := &TelemetryHealth{
		CollectedAt:        now.Add(-500 * time.Millisecond),
		PublishedAt:        now,
		Coverage:           &manifest.TelemetryCoverage.Coverage,
		Loss:               manifest.TelemetryLoss.clone(),
		StageDistributions: cloneTelemetryStageDistributions(manifest.TelemetryStageDistributions),
		Acceptance:         cloneTelemetryAcceptanceVerdict(manifest.TelemetryAcceptance),
		RenderPopulation:   manifest.TelemetryRenderPopulation.clone(),
		PolicySegments:     append([]TelemetryPolicySegmentReceipt(nil), manifest.TelemetryPolicySegments...),
		ValidityFindings:   cloneTelemetryValidityFindings(manifest.TelemetryValidityFindings),
	}
	runHealth := &TelemetryRunHealth{
		Status:         "degraded",
		Severity:       "warning",
		Summary:        "health summary password=HEALTH_SUMMARY_SECRET SENTINEL_PRIVATE GROUP_RULE_SECRET",
		Conditions:     []string{"condition token=DIAG_COND_SECRET"},
		Unscored:       []string{"unscored password=IVUNSCORED_SECRET"},
		PolicySegment:  1,
		LastObservedAt: now,
		announce:       true,
	}
	livePoint := &LiveMetrics{
		Timestamp:       now,
		ActiveVUsers:    5,
		TotalTx:         100,
		TPS:             10,
		Telemetry:       liveHealth,
		TelemetryStatus: runHealth,
		TelemetryPolicy: &TelemetryPolicyEvidence{Availability: "available", Policy: segPolicy},
	}

	managed := &ManagedTest{
		ID:                       manifest.TestID,
		ProjectID:                storage.DefaultProjectID,
		Name:                     manifest.TestName,
		Status:                   "COMPLETE",
		StartedAt:                &start,
		EndedAt:                  &now,
		RuntimeSettings:          manifest.Provenance.RuntimeSettings,
		Groups:                   manifest.Provenance.Groups,
		initialArchiveProvenance: manifest.InitialProvenance,
		telemetryPolicySegments:  manifest.TelemetryPolicySegments,
		TelemetryPolicySegments:  manifest.TelemetryPolicySegments,
		LiveMetrics:              livePoint,
		History:                  []*LiveMetrics{livePoint},
		TelemetryHealth:          runHealth,
		Archive:                  record,
	}
	server.runner.mu.Lock()
	server.runner.tests[manifest.TestID] = managed
	server.runner.mu.Unlock()

	checkParity := func(consumer string, verdict *TelemetryAcceptanceVerdict, stages []TelemetryStageDistribution, pop *TelemetryRenderPopulation, segs []TelemetryPolicySegmentReceipt, findings []TelemetryValidityFinding) {
		t.Helper()
		if verdict == nil || verdict.Outcome != TelemetryAcceptanceFail {
			t.Fatalf("%s: expected telemetry_acceptance outcome=fail, got %+v", consumer, verdict)
		}
		if len(stages) < 3 {
			t.Fatalf("%s: expected >=3 stage_distributions, got %d", consumer, len(stages))
		}
		if pop == nil || len(pop.PriorEpochs) != 1 {
			t.Fatalf("%s: expected render_population with 1 prior epoch, got %+v", consumer, pop)
		}
		if len(segs) != 2 {
			t.Fatalf("%s: expected 2 policy_segments, got %d", consumer, len(segs))
		}
		if len(findings) != 2 {
			t.Fatalf("%s: expected 2 validity_findings, got %d", consumer, len(findings))
		}
	}

	// 1. Consumer 1: Live WebSocket broadcasts (metrics_update, telemetry_diagnostic, telemetry_health_changed).
	testHub := NewHub()
	origHub := server.runner.hub
	server.runner.hub = testHub
	defer func() {
		server.runner.hub = origHub
	}()

	scrubber := newManagedTestTelemetryTextScrubber(managed)
	scrubbedBroadcastLive := cloneRunLiveMetrics(managed)
	diag := &TelemetryDiagnostic{
		Severity:   "warning",
		Summary:    "diag summary password=DIAG_SUMMARY_SECRET SENTINEL_PRIVATE GROUP_RULE_SECRET",
		Conditions: []string{"cond token=DIAG_COND_SECRET"},
		Samples:    []DiagnosticSample{{Issue: "issue api_key=DIAG_ISSUE_SECRET", Transaction: "tx password=TX_SECRET"}},
	}
	scrubbedDiag := scrubTelemetryDiagnostic(diag, scrubbedBroadcastLive.Telemetry, scrubber)
	server.runner.hub.BroadcastScopedJSON(manifest.TestID, storage.DefaultProjectID, map[string]any{
		"type":       "metrics_update",
		"test_id":    manifest.TestID,
		"project_id": storage.DefaultProjectID,
		"test_name":  scrubTelemetryIdentity(scrubber.text(managed.Name)),
		"timestamp":  now,
		"data":       scrubbedBroadcastLive,
	})
	server.runner.hub.BroadcastScopedJSON(manifest.TestID, storage.DefaultProjectID, map[string]any{
		"type":       "telemetry_diagnostic",
		"test_id":    manifest.TestID,
		"project_id": storage.DefaultProjectID,
		"test_name":  scrubTelemetryIdentity(scrubber.text(managed.Name)),
		"timestamp":  now,
		"data":       scrubbedDiag,
	})
	server.runner.broadcastTelemetryHealthTransition(managed, &TelemetryRunHealth{Status: "healthy"}, runHealth, now)

	for i := 0; i < 3; i++ {
		select {
		case rawMsg := <-testHub.broadcast:
			requireNoTelemetrySentinel(t, fmt.Sprintf("Consumer 1 WebSocket message %d", i+1), string(rawMsg.payload), append([]string{"NAME_SECRET", "TX_SECRET"}, allSentinels...)...)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for WebSocket broadcast message %d", i+1)
		}
	}
	checkParity("Consumer 1 (WebSocket metrics_update)", scrubbedBroadcastLive.Telemetry.Acceptance, scrubbedBroadcastLive.Telemetry.StageDistributions, scrubbedBroadcastLive.Telemetry.RenderPopulation, scrubbedBroadcastLive.Telemetry.PolicySegments, scrubbedBroadcastLive.Telemetry.ValidityFindings)
	checkParity("Consumer 1 (WebSocket telemetry_diagnostic)", scrubbedDiag.Telemetry.Acceptance, scrubbedDiag.Telemetry.StageDistributions, scrubbedDiag.Telemetry.RenderPopulation, scrubbedDiag.Telemetry.PolicySegments, scrubbedDiag.Telemetry.ValidityFindings)

	// 2. Consumer 2: GET /api/v1/tests/{id}
	respTest := httptest.NewRecorder()
	server.mux.ServeHTTP(respTest, httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+manifest.TestID, nil))
	if respTest.Code != http.StatusOK {
		t.Fatalf("Consumer 2 GET /api/v1/tests/{id} = %d %s", respTest.Code, respTest.Body.String())
	}
	requireNoTelemetrySentinel(t, "Consumer 2 GET /api/v1/tests/{id}", respTest.Body.String(), allSentinels...)
	var testView ManagedTest
	if err := json.Unmarshal(respTest.Body.Bytes(), &testView); err != nil {
		t.Fatal(err)
	}
	checkParity("Consumer 2 (GET /api/v1/tests/{id})", testView.LiveMetrics.Telemetry.Acceptance, testView.LiveMetrics.Telemetry.StageDistributions, testView.LiveMetrics.Telemetry.RenderPopulation, testView.TelemetryPolicySegments, testView.LiveMetrics.Telemetry.ValidityFindings)

	// 3. Consumer 3: GET /api/v1/tests/{id}/metrics
	respMetrics := httptest.NewRecorder()
	server.mux.ServeHTTP(respMetrics, httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+manifest.TestID+"/metrics", nil))
	if respMetrics.Code != http.StatusOK {
		t.Fatalf("Consumer 3 GET /api/v1/tests/{id}/metrics = %d %s", respMetrics.Code, respMetrics.Body.String())
	}
	requireNoTelemetrySentinel(t, "Consumer 3 GET /api/v1/tests/{id}/metrics", respMetrics.Body.String(), allSentinels...)
	var metricsView struct {
		Metrics *LiveMetrics   `json:"metrics"`
		History []*LiveMetrics `json:"history"`
	}
	if err := json.Unmarshal(respMetrics.Body.Bytes(), &metricsView); err != nil {
		t.Fatal(err)
	}
	checkParity("Consumer 3 (GET /api/v1/tests/{id}/metrics)", metricsView.Metrics.Telemetry.Acceptance, metricsView.Metrics.Telemetry.StageDistributions, metricsView.Metrics.Telemetry.RenderPopulation, metricsView.Metrics.Telemetry.PolicySegments, metricsView.Metrics.Telemetry.ValidityFindings)

	// 4. Consumer 4: Telemetry notifications (webhook + durable outbox publication).
	cleanManifestForOutbox := *manifest
	cleanManifestForOutbox.TestName = "Run password=NAME_SECRET SENTINEL_PRIVATE GROUP_RULE_SECRET"
	cleanManifestForOutbox.Provenance.PolicyPins = append([]RunPolicyPin(nil), manifest.Provenance.PolicyPins...)
	cleanManifestForOutbox.Provenance.PolicyPins[0].ID = "gate-clean-id"
	cleanTap := *manifest.Provenance.TelemetryAcceptancePolicy
	cleanTap.Pin.ID = "gate-clean-id"
	cleanManifestForOutbox.Provenance.TelemetryAcceptancePolicy = &cleanTap
	cleanManifestForOutbox.TelemetryAcceptance = nil
	pub, err := server.telemetryNotificationPublication(t.Context(), manifest.TestID, &cleanManifestForOutbox)
	if err != nil || pub.Event == nil {
		t.Fatalf("Consumer 4 durable outbox publication failed: dec=%q err=%v", pub.Decision, err)
	}
	requireNoTelemetrySentinel(t, "Consumer 4 durable notification outbox", string(pub.Event.Payload), append([]string{"NAME_SECRET"}, allSentinels...)...)
	if pub.Event.Outcome != TelemetryAcceptanceFail {
		t.Fatalf("Consumer 4 notification outcome = %q, want fail", pub.Event.Outcome)
	}

	// 5. Consumer 5: GET /api/v1/tests/{id}/archive/manifest
	respManifest := httptest.NewRecorder()
	server.mux.ServeHTTP(respManifest, httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+manifest.TestID+"/archive/manifest", nil))
	if respManifest.Code != http.StatusOK {
		t.Fatalf("Consumer 5 GET /api/v1/tests/{id}/archive/manifest = %d %s", respManifest.Code, respManifest.Body.String())
	}
	requireNoTelemetrySentinel(t, "Consumer 5 GET /api/v1/tests/{id}/archive/manifest", respManifest.Body.String(), allSentinels...)
	var manifestView RunArchiveManifest
	if err := json.Unmarshal(respManifest.Body.Bytes(), &manifestView); err != nil {
		t.Fatal(err)
	}
	checkParity("Consumer 5 (GET /api/v1/tests/{id}/archive/manifest)", manifestView.TelemetryAcceptance, manifestView.TelemetryStageDistributions, manifestView.TelemetryRenderPopulation, manifestView.TelemetryPolicySegments, manifestView.TelemetryValidityFindings)

	// 6. Consumer 6: GET /api/v1/tests/{id}/archive/files/telemetry_evidence.json
	respEvidenceFile := httptest.NewRecorder()
	server.mux.ServeHTTP(respEvidenceFile, httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+manifest.TestID+"/archive/files/"+telemetryEvidenceFileName, nil))
	if respEvidenceFile.Code != http.StatusOK {
		t.Fatalf("Consumer 6 GET /api/v1/tests/{id}/archive/files/telemetry_evidence.json = %d %s", respEvidenceFile.Code, respEvidenceFile.Body.String())
	}
	requireNoTelemetrySentinel(t, "Consumer 6 GET /api/v1/tests/{id}/archive/files/telemetry_evidence.json", respEvidenceFile.Body.String(), allSentinels...)
	var sidecarView TelemetryEvidenceDocument
	if err := json.Unmarshal(respEvidenceFile.Body.Bytes(), &sidecarView); err != nil {
		t.Fatal(err)
	}
	checkParity("Consumer 6 (telemetry_evidence.json)", sidecarView.Acceptance, sidecarView.StageDistributions, sidecarView.RenderPopulation, sidecarView.PolicySegments, sidecarView.ValidityFindings)

	// 7. Consumer 7: GET /api/v1/tests/{id}/archive/report & GET /api/v1/tests/{id}/report
	respArchiveReport := httptest.NewRecorder()
	server.mux.ServeHTTP(respArchiveReport, httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+manifest.TestID+"/archive/report", nil))
	if respArchiveReport.Code != http.StatusOK {
		t.Fatalf("Consumer 7 GET /api/v1/tests/{id}/archive/report = %d %s", respArchiveReport.Code, respArchiveReport.Body.String())
	}
	requireNoTelemetrySentinel(t, "Consumer 7 GET /api/v1/tests/{id}/archive/report", respArchiveReport.Body.String(), allSentinels...)
	var reportView RunArchiveReport
	if err := json.Unmarshal(respArchiveReport.Body.Bytes(), &reportView); err != nil {
		t.Fatal(err)
	}
	checkParity("Consumer 7 (GET /api/v1/tests/{id}/archive/report)", reportView.TelemetryAcceptance, reportView.TelemetryStageDistributions, reportView.TelemetryRenderPopulation, reportView.TelemetryPolicySegments, reportView.TelemetryValidityFindings)

	respHTMLReport := httptest.NewRecorder()
	server.mux.ServeHTTP(respHTMLReport, httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+manifest.TestID+"/report", nil))
	if respHTMLReport.Code != http.StatusOK {
		t.Fatalf("Consumer 7 GET /api/v1/tests/{id}/report = %d", respHTMLReport.Code)
	}
	requireNoTelemetrySentinel(t, "Consumer 7 GET /api/v1/tests/{id}/report HTML", respHTMLReport.Body.String(), allSentinels...)

	// 8. Consumer 8: Root-cause endpoints (/archive/root-cause and /archive/root-cause/v2)
	respRCA := httptest.NewRecorder()
	server.mux.ServeHTTP(respRCA, httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+manifest.TestID+"/archive/root-cause", nil))
	if respRCA.Code != http.StatusOK {
		t.Fatalf("Consumer 8 GET /api/v1/tests/{id}/archive/root-cause = %d %s", respRCA.Code, respRCA.Body.String())
	}
	requireNoTelemetrySentinel(t, "Consumer 8 GET /api/v1/tests/{id}/archive/root-cause", respRCA.Body.String(), allSentinels...)

	respRCAV2 := httptest.NewRecorder()
	server.mux.ServeHTTP(respRCAV2, httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+manifest.TestID+"/archive/root-cause/v2", nil))
	if respRCAV2.Code != http.StatusOK {
		t.Fatalf("Consumer 8 GET /api/v1/tests/{id}/archive/root-cause/v2 = %d %s", respRCAV2.Code, respRCAV2.Body.String())
	}
	requireNoTelemetrySentinel(t, "Consumer 8 GET /api/v1/tests/{id}/archive/root-cause/v2", respRCAV2.Body.String(), allSentinels...)
	var rcaV2View RCAAnalysisResponse
	if err := json.Unmarshal(respRCAV2.Body.Bytes(), &rcaV2View); err != nil {
		t.Fatal(err)
	}
	checkParity("Consumer 8 (GET /api/v1/tests/{id}/archive/root-cause/v2)", rcaV2View.TelemetryAcceptance, rcaV2View.TelemetryStageDistributions, rcaV2View.TelemetryRenderPopulation, rcaV2View.TelemetryPolicySegments, rcaV2View.TelemetryValidityFindings)

	// 9. Consumer 9: apexctl CLI / CI output (ciRunResponse + GitHub check lines)
	ciResp := ciRunResponse{Status: "COMPLETE", SLAEvaluated: true}
	ciEvidence := newCIValidatedArchiveEvidence(manifest, nil, &ciRunValidity{Verdict: rca.ValiditySuspect, Reasons: []string{"reason password=VAL_SUMMARY_SECRET SENTINEL_PRIVATE GROUP_RULE_SECRET"}}, false)
	applyCITerminalEvidence(&ciResp, ciEvidence, nil)
	finalizeCIRunOutcome(&ciResp, ciRunMetadata{})
	ciBytes, err := json.Marshal(ciResp)
	if err != nil {
		t.Fatal(err)
	}
	requireNoTelemetrySentinel(t, "Consumer 9 CI run response", string(ciBytes), allSentinels...)
	requireNoTelemetrySentinel(t, "Consumer 9 CI GitHub check lines", strings.Join(ciGitHubCheckTelemetryLines(ciResp.TelemetryVerdict), "\n"), allSentinels...)
	checkParity("Consumer 9 (CI / apexctl output)", ciResp.TelemetryAcceptance, ciResp.TelemetryStageDistributions, ciResp.TelemetryRenderPopulation, ciResp.TelemetryPolicySegments, ciResp.TelemetryValidityFindings)

	// 10. Consumer 10: MCP tool surface (get_test, get_test_metrics, get_test_archive_manifest, get_test_archive_file, get_test_archive_report, get_test_root_cause, get_test_root_cause_v2)
	mcpRoutes := []string{
		"/api/v1/tests/" + manifest.TestID,
		"/api/v1/tests/" + manifest.TestID + "/metrics",
		"/api/v1/tests/" + manifest.TestID + "/archive/manifest",
		"/api/v1/tests/" + manifest.TestID + "/archive/files/" + telemetryEvidenceFileName,
		"/api/v1/tests/" + manifest.TestID + "/archive/report",
		"/api/v1/tests/" + manifest.TestID + "/archive/root-cause",
		"/api/v1/tests/" + manifest.TestID + "/archive/root-cause/v2",
	}
	for _, route := range mcpRoutes {
		rec := httptest.NewRecorder()
		server.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, route, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("Consumer 10 MCP route %s = %d %s", route, rec.Code, rec.Body.String())
		}
		requireNoTelemetrySentinel(t, "Consumer 10 MCP "+route, rec.Body.String(), allSentinels...)
	}

	// Verify buildRunRootCauseSummary deep-clones telemetry fields so mutating summary.Report does not alias report.
	rcaSummary := buildRunRootCauseSummary(manifest.TestID, &reportView)
	if stages, ok := rcaSummary.Report["telemetry_stage_distributions"].([]TelemetryStageDistribution); ok && len(stages) > 0 {
		stages[0].Reason = "MUTATED_REASON"
		if reportView.TelemetryStageDistributions[0].Reason == "MUTATED_REASON" {
			t.Fatal("buildRunRootCauseSummary aliased TelemetryStageDistributions slice")
		}
	}
	if verdict, ok := rcaSummary.Report["telemetry_acceptance"].(*TelemetryAcceptanceVerdict); ok && verdict != nil && len(verdict.Reasons) > 0 {
		verdict.Reasons[0] = "MUTATED_VERDICT"
		if reportView.TelemetryAcceptance.Reasons[0] == "MUTATED_VERDICT" {
			t.Fatal("buildRunRootCauseSummary aliased TelemetryAcceptance")
		}
	}
	if cov, ok := rcaSummary.Report["telemetry_coverage"].(*TelemetryCoverageRecord); ok && cov != nil && len(cov.Coverage.Sources) > 0 {
		cov.Coverage.Sources[0].Source = "MUTATED_SOURCE"
		if reportView.TelemetryCoverage.Coverage.Sources[0].Source == "MUTATED_SOURCE" {
			t.Fatal("buildRunRootCauseSummary aliased TelemetryCoverage")
		}
	}
	if segs, ok := rcaSummary.Report["telemetry_policy_segments"].([]TelemetryPolicySegmentReceipt); ok && len(segs) > 0 {
		segs[0].RequestedBy = "MUTATED_ACTOR"
		if reportView.TelemetryPolicySegments[0].RequestedBy == "MUTATED_ACTOR" {
			t.Fatal("buildRunRootCauseSummary aliased TelemetryPolicySegments")
		}
	}
	if findings, ok := rcaSummary.Report["telemetry_validity_findings"].([]TelemetryValidityFinding); ok && len(findings) > 0 {
		findings[0].Summary = "MUTATED_FINDING"
		if reportView.TelemetryValidityFindings[0].Summary == "MUTATED_FINDING" {
			t.Fatal("buildRunRootCauseSummary aliased TelemetryValidityFindings")
		}
	}

	// Verify buildRunArchiveReport preserves stored manifest.TelemetryAcceptance when Provenance.TelemetryAcceptancePolicy is nil.
	storedVerdictManifest := *manifest
	storedVerdictManifest.Provenance.TelemetryAcceptancePolicy = nil
	storedVerdictManifest.Provenance.PolicyPins = nil
	storedDir := t.TempDir()
	for _, name := range []string{storedVerdictManifest.TransactionsFile, storedVerdictManifest.ResourceSamplesFile, storedVerdictManifest.IntervalSummariesFile} {
		if err := os.WriteFile(filepath.Join(storedDir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(storedDir, telemetryEvidenceFileName), evidenceBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	storedManifestBytes, err := json.MarshalIndent(storedVerdictManifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	storedManifestPath := filepath.Join(storedDir, "manifest.json")
	if err := os.WriteFile(storedManifestPath, storedManifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	storedReport, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: storedDir, ManifestPath: storedManifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport with stored verdict only: %v", err)
	}
	if storedReport.TelemetryAcceptance == nil || storedReport.TelemetryAcceptance.Outcome != TelemetryAcceptanceFail {
		t.Fatalf("expected stored TelemetryAcceptance to be preserved when TelemetryAcceptancePolicy is nil, got %+v", storedReport.TelemetryAcceptance)
	}
	storedReportJSON, err := json.Marshal(storedReport.TelemetryAcceptance)
	if err != nil {
		t.Fatal(err)
	}
	requireNoTelemetrySentinel(t, "stored TelemetryAcceptance fallback in buildRunArchiveReport", string(storedReportJSON), allSentinels...)

	// Verify sanitizedRunArchiveArtifact fails closed (500) on corrupt telemetry_evidence.json and returns 404 when missing.
	if err := os.WriteFile(filepath.Join(dir, telemetryEvidenceFileName), []byte("{corrupt Bearer E2E_BEARER_SECRET SENTINEL_PRIVATE"), 0o600); err != nil {
		t.Fatal(err)
	}
	server.archiveReadCaches().forgetRunArchiveCache(manifest.TestID)
	for _, artifactName := range []string{runArchiveManifestFileName, telemetryEvidenceFileName} {
		rec := httptest.NewRecorder()
		server.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+manifest.TestID+"/archive/files/"+artifactName, nil))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("corrupt telemetry_evidence.json: GET /archive/files/%s status = %d, want 500; body=%s", artifactName, rec.Code, rec.Body.String())
		}
		requireNoTelemetrySentinel(t, "corrupt telemetry_evidence.json fail-closed "+artifactName, rec.Body.String(), allSentinels...)
	}

	if err := os.Remove(filepath.Join(dir, telemetryEvidenceFileName)); err != nil {
		t.Fatal(err)
	}
	server.archiveReadCaches().forgetRunArchiveCache(manifest.TestID)
	recMissing := httptest.NewRecorder()
	server.mux.ServeHTTP(recMissing, httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+manifest.TestID+"/archive/files/"+telemetryEvidenceFileName, nil))
	if recMissing.Code != http.StatusNotFound {
		t.Fatalf("missing telemetry_evidence.json: GET /archive/files/%s status = %d, want 404; body=%s", telemetryEvidenceFileName, recMissing.Code, recMissing.Body.String())
	}
}
