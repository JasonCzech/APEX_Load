package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// e1Document is one final document metric observation.
type e1Document struct {
	id        int
	metric    string
	engine    string
	device    string
	value     float64
	qualified bool
	state     string
	frame     string
}

func (d e1Document) observation(at time.Time) protocol.BrowserDocumentObservation {
	doc, life := fmt.Sprintf("%s-document-%d", d2Generation, d.id), fmt.Sprintf("%s-lifecycle-%d", d2Generation, d.id)
	engine, device, state, frame := d.engine, d.device, d.state, d.frame
	if engine == "" {
		engine = "chromium"
	}
	if device == "" {
		device = "desktop"
	}
	if state == "" {
		state = "final"
	}
	if frame == "" {
		frame = "main_frame"
	}
	unit := "ms"
	if d.metric == "cls" {
		unit = "score"
	}
	v := d.value
	o := protocol.BrowserDocumentObservation{Version: protocol.BrowserEvidenceVersion, SourceGeneration: d2Generation, DocumentID: doc, LifecycleID: life,
		FrameID: d2Generation + "-frame-1", NavigationID: fmt.Sprintf("%s-navigation-%d", d2Generation, d.id), MeasurementID: doc + "/" + life + "/" + d.metric,
		Revision: 1, Metric: d.metric, Algorithm: protocol.BrowserVitalsAlgorithm, AlgorithmSHA256: protocol.BrowserVitalsAlgorithmSHA256, Unit: unit, Engine: engine,
		DeviceProfile: device, Cohort: d2Cohort("141.0"), FrameScope: frame, LifecyclePhase: "navigation", State: state, Boundary: "graceful_end",
		WindowStart: at.Add(-time.Second), WindowEnd: at, CapturedAt: at, Available: true, Qualified: d.qualified && state == "final", Value: &v}
	if !d.qualified {
		o.Cohort = nil
		o.UnavailableReason = ""
	}
	if d.metric == "inp" {
		o.InteractionCount = 1
	}
	return o
}

func e1Observations(documents ...e1Document) []protocol.BrowserDocumentObservation {
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	out := make([]protocol.BrowserDocumentObservation, 0, len(documents))
	for _, d := range documents {
		o := d.observation(at)
		if !o.Valid() {
			panic(fmt.Sprintf("invalid fixture observation %+v", o))
		}
		out = append(out, o)
	}
	return out
}

func e1Policy(metrics ...BrowserPolicyMetric) BrowserPolicyDefinition {
	return BrowserPolicyDefinition{Version: analysisPolicyVersion, Metrics: metrics, Baseline: BrowserPolicyBaseline{Mode: "none"}}
}

var (
	e1LCP = BrowserPolicyMetric{Metric: "lcp", Good: 2500, Poor: 4000, Weight: 3}
	e1CLS = BrowserPolicyMetric{Metric: "cls", Good: 0.1, Poor: 0.25, Weight: 1}
	e1INP = BrowserPolicyMetric{Metric: "inp", Good: 200, Poor: 500, Weight: 0}
)

func e1Evaluate(policy BrowserPolicyDefinition, captured, incomplete bool, dropped int64, documents ...e1Document) *BrowserIntelligenceResult {
	inputs := &browserIntelligenceInputs{runID: "browser-run", policy: policy, fingerprint: "fingerprint-1", ref: BrowserPolicyRef{ID: "browser", Revision: 1, Source: "pinned"}}
	window := BrowserFindingWindow{Start: time.Date(2026, 10, 8, 9, 59, 0, 0, time.UTC), End: time.Date(2026, 10, 8, 10, 1, 0, 0, time.UTC)}
	return evaluateBrowserFindings(inputs, window, e1Observations(documents...), captured, incomplete, dropped)
}

func e1Findings(result *BrowserIntelligenceResult, rule, metric string) []BrowserFinding {
	var out []BrowserFinding
	for _, finding := range result.Findings {
		if finding.RuleID == rule && finding.Metric == metric {
			out = append(out, finding)
		}
	}
	return out
}

func TestBrowserScoreNeedsEveryRequiredMetric(t *testing.T) {
	// LCP is required (weight 3) but only CLS was observed: no overall score,
	// the observed component and coverage instead.
	result := e1Evaluate(e1Policy(e1LCP, e1CLS), true, false, 0, e1Document{id: 1, metric: "cls", value: 0.05, qualified: true})
	score := result.Score
	if score.Score != nil || score.UnscoredReason != "missing_required_metric" || score.Severity != "unscored" || len(score.Missing) != 1 || score.Missing[0] != "lcp" {
		t.Fatalf("score %+v", score)
	}
	if score.Coverage != (BrowserScoreCoverage{Observed: 1, Required: 2}) {
		t.Fatalf("coverage %+v", score.Coverage)
	}
	cls := score.Components[1]
	if cls.Metric != "cls" || cls.Score == nil || *cls.Score != 100 || cls.Contribution == nil || *cls.Contribution != 25 || cls.Samples != 1 {
		t.Fatalf("cls component %+v", cls)
	}
	if lcp := score.Components[0]; lcp.Score != nil || lcp.Reason != "no_qualified_final_observations" {
		t.Fatalf("lcp component %+v", lcp)
	}
	lcp := e1Findings(result, browserFindingRuleThreshold, "lcp")
	if len(lcp) != 1 || lcp[0].State != "unscored" || lcp[0].Reason != "metric_not_observed" {
		t.Fatalf("lcp findings %+v", lcp)
	}
}

func TestBrowserScoreWeighsQualifiedComponents(t *testing.T) {
	// One value per metric, so p75 is the value itself.
	result := e1Evaluate(e1Policy(e1LCP, e1CLS, e1INP), true, false, 0,
		e1Document{id: 1, metric: "lcp", value: 2875, qualified: true},
		e1Document{id: 1, metric: "cls", value: 0.05, qualified: true},
		e1Document{id: 1, metric: "inp", value: 900, qualified: true},
		// An unqualified LCP never enters the score.
		e1Document{id: 2, metric: "lcp", value: 60000, qualified: false})
	score := result.Score
	// LCP 75 (a quarter of the way from good to poor, weight 3) and CLS 100
	// (weight 1); INP has no weight, so it is reported but neither required
	// nor counted.
	if score.Score == nil || math.Abs(*score.Score-81.25) > 1e-9 || score.Severity != "needs_improvement" || len(score.Missing) != 0 || score.Coverage != (BrowserScoreCoverage{Observed: 2, Required: 2}) {
		t.Fatalf("score %+v", score)
	}
	lcp, inp := score.Components[0], score.Components[2]
	if lcp.Score == nil || math.Abs(*lcp.Score-75) > 1e-9 || lcp.Contribution == nil || math.Abs(*lcp.Contribution-56.25) > 1e-9 || lcp.Samples != 1 {
		t.Fatalf("lcp %+v", lcp)
	}
	if inp.Required || inp.Contribution != nil || inp.Score == nil || *inp.Score != 0 {
		t.Fatalf("inp %+v", inp)
	}
	// INP is past poor: a poor finding even though it does not weigh.
	findings := e1Findings(result, browserFindingRuleThreshold, "inp")
	if len(findings) != 1 || findings[0].State != "active" || findings[0].Severity != "poor" || findings[0].Category != browserFindingCategoryApplication {
		t.Fatalf("inp findings %+v", findings)
	}
	if findings := e1Findings(result, browserFindingRuleThreshold, "lcp"); len(findings) != 1 || findings[0].Severity != "needs_improvement" || findings[0].Quality.EligibleFinal != 1 || findings[0].Quality.UnqualifiedFinal != 1 {
		t.Fatalf("lcp findings %+v", findings)
	}
	// CLS is within good: no finding.
	if findings := e1Findings(result, browserFindingRuleThreshold, "cls"); len(findings) != 0 {
		t.Fatalf("cls findings %+v", findings)
	}
	if score := browserComponentScore(4000, 2500, 4000); score != 0 {
		t.Fatalf("at poor = %v", score)
	}
	if score := browserComponentScore(2500, 2500, 4000); score != 100 {
		t.Fatalf("at good = %v", score)
	}
	if browserScoreSeverity(90) != "good" || browserScoreSeverity(89.9) != "needs_improvement" || browserScoreSeverity(50) != "needs_improvement" || browserScoreSeverity(49.9) != "poor" {
		t.Fatal("score bands")
	}
	if browserSeverity(4000, 2500, 4000) != "needs_improvement" || browserSeverity(4000.1, 2500, 4000) != "poor" || browserSeverity(2500, 2500, 4000) != "good" {
		t.Fatal("severity bands")
	}
}

func TestBrowserFindingsAreSuppressedOrUnscoredWithAReason(t *testing.T) {
	policy := e1Policy(e1LCP, BrowserPolicyMetric{Metric: "inp", Good: 200, Poor: 500, Weight: 1})
	policy.Applicability.Engines = []string{"chromium"}
	documents := []e1Document{}
	for i := 1; i <= 7; i++ {
		documents = append(documents, e1Document{id: i, metric: "lcp", value: float64(4000 + i*100), qualified: true})
		documents = append(documents, e1Document{id: 100 + i, metric: "lcp", engine: "webkit", device: "iphone", value: 9000, qualified: true})
	}
	// INP on chromium was available but never qualified.
	documents = append(documents, e1Document{id: 50, metric: "inp", value: 300, qualified: false},
		// A child frame is reported, never judged.
		e1Document{id: 51, metric: "lcp", value: 99999, qualified: true, frame: "child_frame"})
	result := e1Evaluate(policy, true, false, 0, documents...)
	lcp := e1Findings(result, browserFindingRuleThreshold, "lcp")
	if len(lcp) != 2 {
		t.Fatalf("lcp findings %+v", lcp)
	}
	active, suppressed := lcp[0], lcp[1]
	if active.Cohort.Engine != "chromium" {
		active, suppressed = suppressed, active
	}
	if active.State != "active" || active.Severity != "poor" || !active.Applicable || active.Observed == nil || *active.Observed <= 4000 || active.Quality.EligibleFinal != 7 {
		t.Fatalf("active %+v", active)
	}
	// Evidence: the five worst documents, worst first, linked to their
	// retained resources.
	if len(active.Evidence.Documents) != maxBrowserFindingEvidence || active.Evidence.Documents[0].Value != 4700 || active.Evidence.Documents[4].Value != 4300 ||
		!strings.Contains(active.Evidence.Documents[0].Resources, "/api/v1/tests/browser-run/archive/browser-resources?document_id=") || !strings.Contains(active.Evidence.Documents[0].Resources, "source_generation="+d2Generation) {
		t.Fatalf("evidence %+v", active.Evidence)
	}
	if suppressed.State != "suppressed" || suppressed.Reason != "engine_not_applicable" || suppressed.Applicable || suppressed.Observed != nil {
		t.Fatalf("suppressed %+v", suppressed)
	}
	inp := e1Findings(result, browserFindingRuleThreshold, "inp")
	var chromium BrowserFinding
	for _, finding := range inp {
		if finding.Cohort.Engine == "chromium" {
			chromium = finding
		}
	}
	if chromium.State != "unscored" || chromium.Reason != "no_qualified_final_observations" || chromium.Quality.UnqualifiedFinal != 1 || chromium.Quality.ExcludedReasons["cohort_unavailable"] != 1 {
		t.Fatalf("inp chromium %+v", chromium)
	}
	if result.Counts["active"] != 1 || result.Counts["suppressed"] != 2 || result.Counts["unscored"] != 1 {
		t.Fatalf("counts %+v (%+v)", result.Counts, result.Findings)
	}
	if !strings.Contains(strings.Join(result.Notes, " "), "child_frame/navigation 1") {
		t.Fatalf("notes %v", result.Notes)
	}
	// The child frame's LCP is not in the score's component either.
	if component := result.Score.Components[0]; component.Samples != 7 {
		t.Fatalf("lcp component %+v", component)
	}
	if device := e1Policy(e1LCP); true {
		device.Applicability.DeviceProfiles = []string{"desktop"}
		if applicable, reason := browserPolicyApplicability(device, BrowserFindingCohort{Engine: "chrome", DeviceProfile: "iphone"}); applicable || reason != "device_profile_not_applicable" {
			t.Fatalf("device applicability %v %q", applicable, reason)
		}
		device.Applicability.Engines = []string{"chromium"}
		if applicable, _ := browserPolicyApplicability(device, BrowserFindingCohort{Engine: "chrome", DeviceProfile: "desktop"}); !applicable {
			t.Fatal("chrome is a chromium engine")
		}
	}
}

// Evidence lists only documents past good; provisional observations are
// counted as quality, never values.
func TestBrowserFindingEvidenceAndQuality(t *testing.T) {
	result := e1Evaluate(e1Policy(e1LCP), true, false, 0,
		e1Document{id: 1, metric: "lcp", value: 1000, qualified: true},
		e1Document{id: 2, metric: "lcp", value: 1100, qualified: true},
		e1Document{id: 3, metric: "lcp", value: 3000, qualified: true},
		e1Document{id: 4, metric: "lcp", value: 3100, qualified: true},
		e1Document{id: 5, metric: "lcp", value: 90000, state: "provisional"})
	findings := e1Findings(result, browserFindingRuleThreshold, "lcp")
	if len(findings) != 1 || findings[0].Severity != "needs_improvement" || findings[0].Quality.EligibleFinal != 4 || findings[0].Quality.Provisional != 1 {
		t.Fatalf("findings %+v", findings)
	}
	documents := findings[0].Evidence.Documents
	if len(documents) != 2 || documents[0].Value != 3100 || documents[1].Value != 3000 {
		t.Fatalf("evidence %+v", documents)
	}
}

func TestBrowserCollectionHealthIsKeptApart(t *testing.T) {
	// Capture was requested and nothing was retained: a collection finding
	// and no score, never a healthy badge.
	result := e1Evaluate(e1Policy(e1LCP), true, true, 0)
	if result.Score.Score != nil || result.Score.UnscoredReason != "no_document_evidence" {
		t.Fatalf("score %+v", result.Score)
	}
	if findings := e1Findings(result, browserFindingRuleCollectionNone, ""); len(findings) != 1 || findings[0].Category != browserFindingCategoryCollection || findings[0].State != "active" {
		t.Fatalf("findings %+v", result.Findings)
	}
	// Lost observations are a collection finding beside the outcomes.
	result = e1Evaluate(e1Policy(e1LCP), true, true, 3, e1Document{id: 1, metric: "lcp", value: 1000, qualified: true})
	loss := e1Findings(result, browserFindingRuleCollectionLoss, "")
	if len(loss) != 1 || loss[0].Observed == nil || *loss[0].Observed != 3 || loss[0].Category != browserFindingCategoryCollection {
		t.Fatalf("loss %+v", result.Findings)
	}
	if result.Score.Score == nil || *result.Score.Score != 100 {
		t.Fatalf("score %+v", result.Score)
	}
	// A run that never requested document evidence has nothing to judge.
	result = e1Evaluate(e1Policy(e1LCP), false, false, 0)
	if len(result.Findings) != 0 || result.Score.Score != nil {
		t.Fatalf("not captured %+v", result)
	}
}

func TestBrowserFindingIdentitiesAreStable(t *testing.T) {
	document := e1Document{id: 1, metric: "lcp", value: 5000, qualified: true}
	first := e1Evaluate(e1Policy(e1LCP), true, false, 0, document)
	second := e1Evaluate(e1Policy(e1LCP), true, false, 0, document)
	if len(first.Findings) != 1 || first.Findings[0].ID != second.Findings[0].ID || first.Findings[0].Key != second.Findings[0].Key {
		t.Fatalf("rerun identities %+v %+v", first.Findings, second.Findings)
	}
	inputs := &browserIntelligenceInputs{runID: "browser-run", policy: e1Policy(e1LCP), fingerprint: "fingerprint-2"}
	other := evaluateBrowserFindings(inputs, first.Window, e1Observations(document), true, false, 0)
	if other.Findings[0].ID == first.Findings[0].ID || other.Findings[0].Key != first.Findings[0].Key {
		t.Fatalf("a new evaluation keeps the key and changes the record id: %+v %+v", other.Findings[0], first.Findings[0])
	}
}

// e1Archive writes a finalized run archive with document evidence and a
// browser_policy pin.
func e1Archive(t *testing.T, server *Server, store *storage.SQLiteStore, id string, pin *RunPolicyPin, documents ...e1Document) *storage.RunArchiveRecord {
	t.Helper()
	start := time.Date(2026, 10, 8, 9, 59, 0, 0, time.UTC)
	end := start.Add(2 * time.Minute)
	root := t.TempDir()
	provenance := RunArchiveProvenance{ProjectID: storage.DefaultProjectID, StartedAt: &start}
	if pin != nil {
		provenance.PolicyPins = []RunPolicyPin{*pin}
	}
	manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: id, TestName: id, Status: "COMPLETE", CreatedAt: start, CompletedAt: end, Provenance: provenance, InitialProvenance: &provenance, BrowserDocumentCaptureRequested: true}
	var body bytes.Buffer
	for _, observation := range e1Observations(documents...) {
		line, err := json.Marshal(protocol.BrowserDocumentEvidence{Version: protocol.BrowserEvidenceVersion, SourceGeneration: d2Generation, Observations: []protocol.BrowserDocumentObservation{observation}})
		if err != nil {
			t.Fatal(err)
		}
		body.Write(append(line, '\n'))
		manifest.BrowserDocumentRecordCount++
	}
	if len(documents) > 0 {
		if err := os.WriteFile(filepath.Join(root, browserDocumentsFileName), body.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		sum, parquet := sha256.Sum256(body.Bytes()), sha256.Sum256([]byte("parquet"))
		manifest.BrowserDocumentsFile, manifest.BrowserDocumentsParquetFile = browserDocumentsFileName, browserDocumentsParquetFileName
		manifest.BrowserDocumentsSHA256, manifest.BrowserDocumentsParquetSHA256 = hex.EncodeToString(sum[:]), hex.EncodeToString(parquet[:])
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if err := store.SaveTest(&storage.TestRecord{ID: id, Name: id, ProjectID: storage.DefaultProjectID, ScenarioID: "scenario", Status: "COMPLETE", Groups: []any{}, StartTime: start}); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{TestID: id, SchemaVersion: archiveSchemaVersion, ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json"), ManifestSHA256: hex.EncodeToString(digest[:]), CreatedAt: start, CompletedAt: end}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Lock()
	server.runner.tests[id] = &ManagedTest{ID: id, Name: id, ProjectID: storage.DefaultProjectID, Status: "COMPLETE", StartedAt: &start, EndedAt: &end}
	server.runner.archives[id] = record
	server.runner.mu.Unlock()
	admitArchiveForTest(t, record)
	return record
}

func saveBrowserPolicy(t *testing.T, store *storage.SQLiteStore, id string, revision int64, policy BrowserPolicyDefinition) RunPolicyPin {
	t.Helper()
	body, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{ID: id, ProjectID: storage.DefaultProjectID, Kind: storage.AnalysisDefinitionKindBrowserPolicy, Name: "Browser " + id, Revision: revision, Definition: body}, revision-1); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	return RunPolicyPin{Kind: storage.AnalysisDefinitionKindBrowserPolicy, ID: id, Name: "Browser " + id, Revision: revision, SHA256: hex.EncodeToString(digest[:]), Source: "project_default"}
}

func getRunBrowserFindings(t *testing.T, server *Server, id string) RunBrowserFindingsResponse {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+id+"/archive/browser-findings", nil)
	request.SetPathValue("id", id)
	recorder := httptest.NewRecorder()
	server.handleGetRunArchiveBrowserFindings(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("browser findings %d %s", recorder.Code, recorder.Body)
	}
	var response RunBrowserFindingsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestBrowserIntelligenceRunsAutomaticallyAndDedupes(t *testing.T) {
	server, store := baselineTestServer(t)
	pin := saveBrowserPolicy(t, store, "browser", 1, e1Policy(e1LCP))
	e1Archive(t, server, store, "browser-auto", &pin, e1Document{id: 1, metric: "lcp", value: 5000, qualified: true})
	if response := getRunBrowserFindings(t, server, "browser-auto"); response.Status != "not_evaluated" {
		t.Fatalf("before %+v", response)
	}
	server.autoQueueBrowserIntelligence("browser-auto")
	server.autoQueueBrowserIntelligence("browser-auto")
	jobs, err := store.ListEvaluationJobs(storage.DefaultProjectID, browserIntelligenceKind, 10, 0)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("queued jobs %d %v: the same inputs queue once", len(jobs), err)
	}
	if response := getRunBrowserFindings(t, server, "browser-auto"); response.Status != "pending" || !response.Pending {
		t.Fatalf("pending %+v", response)
	}
	if ran, err := server.runNextEvaluationJob(context.Background()); err != nil || !ran {
		t.Fatalf("worker ran=%v err=%v", ran, err)
	}
	response := getRunBrowserFindings(t, server, "browser-auto")
	if response.Status != "ready" || response.Result == nil || response.Result.Policy.Source != "pinned" || response.Result.Policy.SHA256 != pin.SHA256 || response.Result.Score.Score == nil || *response.Result.Score.Score != 0 {
		t.Fatalf("ready %+v", response)
	}
	if findings := e1Findings(response.Result, browserFindingRuleThreshold, "lcp"); len(findings) != 1 || findings[0].State != "active" {
		t.Fatalf("findings %+v", response.Result.Findings)
	}
	// A ready evaluation of the same inputs is not queued again.
	server.autoQueueBrowserIntelligence("browser-auto")
	if jobs, _ := store.ListEvaluationJobs(storage.DefaultProjectID, browserIntelligenceKind, 10, 0); len(jobs) != 1 {
		t.Fatalf("requeued %d", len(jobs))
	}
	// A run that pinned no browser policy is not evaluated automatically,
	// and another run's evaluation is never its result.
	e1Archive(t, server, store, "browser-unpinned", nil, e1Document{id: 1, metric: "lcp", value: 5000, qualified: true})
	server.autoQueueBrowserIntelligence("browser-unpinned")
	if jobs, _ := store.ListEvaluationJobs(storage.DefaultProjectID, browserIntelligenceKind, 10, 0); len(jobs) != 1 {
		t.Fatalf("unpinned queued %d", len(jobs))
	}
	if response := getRunBrowserFindings(t, server, "browser-unpinned"); response.Status != "not_evaluated" {
		t.Fatalf("unpinned %+v", response)
	}
}

func TestBrowserIntelligenceRequestsAreValidated(t *testing.T) {
	server, store := baselineTestServer(t)
	pin := saveBrowserPolicy(t, store, "browser", 1, e1Policy(e1LCP))
	e1Archive(t, server, store, "browser-run", &pin, e1Document{id: 1, metric: "lcp", value: 1000, qualified: true})
	tampered := pin
	tampered.SHA256 = strings.Repeat("0", 64)
	e1Archive(t, server, store, "browser-tampered", &tampered, e1Document{id: 1, metric: "lcp", value: 1000, qualified: true})
	e1Archive(t, server, store, "browser-unpinned", nil, e1Document{id: 1, metric: "lcp", value: 1000, qualified: true})
	body, _ := json.Marshal(JourneyPolicyDefinition{Version: analysisPolicyVersion, Journeys: []JourneyDefinition{{ID: "j", Name: "J", Start: JourneyBoundary{Action: "a"}, Success: JourneyBoundary{Action: "b"}, Weight: 1}}})
	if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{ID: "journeys", ProjectID: storage.DefaultProjectID, Kind: storage.AnalysisDefinitionKindJourneyPolicy, Name: "Journeys", Revision: 1, Definition: body}, 0); err != nil {
		t.Fatal(err)
	}
	prepare := func(request string) (*browserIntelligenceInputs, error) {
		return prepareBrowserIntelligence(context.Background(), server, storage.DefaultProjectID, json.RawMessage(request))
	}
	pinned, err := prepare(`{"run":"browser-run"}`)
	if err != nil || pinned.ref.Source != "pinned" {
		t.Fatalf("pinned %+v %v", pinned, err)
	}
	requested, err := prepare(`{"run":"browser-run","policy":{"id":"browser","revision":1}}`)
	if err != nil || requested.ref.Source != "requested" || requested.fingerprint != pinned.fingerprint {
		t.Fatalf("requested %+v %v: the same revision of the same run has the same inputs", requested, err)
	}
	saveBrowserPolicy(t, store, "browser", 2, e1Policy(e1CLS))
	if next, err := prepare(`{"run":"browser-run","policy":{"id":"browser","revision":2}}`); err != nil || next.fingerprint == pinned.fingerprint {
		t.Fatalf("revision 2 %v", err)
	}
	for request, want := range map[string]string{
		`{"run":""}`:                                      "names one run",
		`{"run":"missing"}`:                               "not in this project",
		`{"run":"browser-tampered"}`:                      "no longer matches its pin",
		`{"run":"browser-unpinned"}`:                      "pinned no browser_policy",
		`{"run":"browser-run","policy":{"id":"browser"}}`: "explicit revision",
		`{"run":"browser-run","policy":{"id":"journeys","revision":1}}`: "not a browser_policy",
		`{"run":"browser-run","unknown":true}`:                          "invalid browser intelligence request",
	} {
		if _, err := prepare(request); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v, want %q", request, err, want)
		}
	}
	// A queued evaluation whose archive changed before it ran fails.
	recorder := httptest.NewRecorder()
	server.handleCreateEvaluationJob(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/analysis/evaluations?project_id="+storage.DefaultProjectID, strings.NewReader(`{"kind":"browser_intelligence","request":{"run":"browser-run"}}`)))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("create %d %s", recorder.Code, recorder.Body)
	}
	var queued EvaluationJobResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &queued); err != nil {
		t.Fatal(err)
	}
	record := server.runner.archives["browser-run"]
	raw, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.TestName = "browser-run (refreshed)"
	if raw, err = json.Marshal(manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record.ManifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	record.ManifestSHA256 = hex.EncodeToString(digest[:])
	if ran, err := server.runNextEvaluationJob(context.Background()); err != nil || !ran {
		t.Fatalf("worker ran=%v err=%v", ran, err)
	}
	if job, err := store.GetEvaluationJob(storage.DefaultProjectID, queued.ID); err != nil || job.State != storage.EvaluationJobFailed || !strings.Contains(job.Failure, "changed after this evaluation was queued") {
		t.Fatalf("stale job %+v %v", job, err)
	}
	server.runner.archives["browser-run"].ManifestSHA256 = ""
	if _, err := prepare(`{"run":"browser-run"}`); err == nil || !strings.Contains(err.Error(), "no finalized archive") {
		t.Fatalf("unfinalized: %v", err)
	}
}

func TestBrowserFindingTransitionsNotifyOnceAndResolve(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	server, store := baselineTestServer(t)
	received := make(chan map[string]any, 16)
	hook := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		received <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer hook.Close()
	server.runner.notifier = newNotificationDispatcher(hook.Client())
	settings := server.runner.GetSystemSettings()
	settings.Notifications = NotificationsSettings{Webhooks: []NotificationWebhookSettings{{Enabled: true, URL: hook.URL, Format: notificationFormatSlack, Events: []string{"browser_finding", "browser_finding_resolved"}}}}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	strict := saveBrowserPolicy(t, store, "browser", 1, e1Policy(e1LCP))
	e1Archive(t, server, store, "browser-notify", &strict, e1Document{id: 1, metric: "lcp", value: 5000, qualified: true})
	evaluate := func(request string) {
		t.Helper()
		recorder := httptest.NewRecorder()
		server.handleCreateEvaluationJob(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/analysis/evaluations?project_id="+storage.DefaultProjectID, strings.NewReader(`{"kind":"browser_intelligence","request":`+request+`}`)))
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("create %d %s", recorder.Code, recorder.Body)
		}
		if ran, err := server.runNextEvaluationJob(context.Background()); err != nil || !ran {
			t.Fatalf("worker ran=%v err=%v", ran, err)
		}
	}
	next := func() map[string]any {
		t.Helper()
		select {
		case payload := <-received:
			return payload
		case <-time.After(5 * time.Second):
			t.Fatal("no webhook")
		}
		return nil
	}
	quiet := func() {
		t.Helper()
		select {
		case payload := <-received:
			t.Fatalf("unexpected webhook %v", payload)
		case <-time.After(300 * time.Millisecond):
		}
	}
	evaluate(`{"run":"browser-notify"}`)
	if payload := next(); !strings.Contains(fmt.Sprint(payload["text"]), "Browser Finding: browser-notify (active)") || !strings.Contains(fmt.Sprint(payload["blocks"]), "LCP p75") {
		t.Fatalf("finding webhook %v", payload)
	}
	quiet()
	// A rerun of the same inputs finds the same finding: nothing new.
	time.Sleep(10 * time.Millisecond)
	evaluate(`{"run":"browser-notify"}`)
	quiet()
	// An evaluation under a named revision is an ordinary evaluation job: it
	// neither replaces the run's result nor sends anything.
	saveBrowserPolicy(t, store, "browser", 2, e1Policy(BrowserPolicyMetric{Metric: "lcp", Good: 6000, Poor: 8000, Weight: 1}))
	time.Sleep(10 * time.Millisecond)
	evaluate(`{"run":"browser-notify","policy":{"id":"browser","revision":2}}`)
	quiet()
	if latest := getRunBrowserFindings(t, server, "browser-notify"); latest.Result == nil || latest.Result.Policy.Revision != 1 || len(e1Findings(latest.Result, browserFindingRuleThreshold, "lcp")) != 1 {
		t.Fatalf("a named revision replaced the run's result: %+v", latest)
	}
	// The run's archive changes (its slow document is gone): the pinned
	// evaluation resolves the finding.
	e1Archive(t, server, store, "browser-notify", &strict, e1Document{id: 1, metric: "lcp", value: 1000, qualified: true})
	time.Sleep(10 * time.Millisecond)
	evaluate(`{"run":"browser-notify"}`)
	if payload := next(); !strings.Contains(fmt.Sprint(payload["text"]), "Browser Finding Resolved: browser-notify (resolved)") {
		t.Fatalf("resolved webhook %v", payload)
	}
	quiet()
	if latest := getRunBrowserFindings(t, server, "browser-notify"); latest.Result == nil || len(e1Findings(latest.Result, browserFindingRuleThreshold, "lcp")) != 0 {
		t.Fatalf("latest %+v", latest)
	}
	// An evaluation that completes late compares with the evaluation before
	// it, never a newer one: replaying the strict rerun's delivery after the
	// resolving evaluation compares it with the first strict one and sends
	// nothing.
	jobs, err := pinnedBrowserEvaluations(store, storage.DefaultProjectID, "browser-notify")
	if err != nil || len(jobs) != 3 {
		t.Fatalf("pinned jobs %d %v", len(jobs), err)
	}
	rerun, err := store.GetEvaluationJob(storage.DefaultProjectID, jobs[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	notifyBrowserFindingTransitions(server, rerun, []byte(rerun.Result))
	quiet()
}

// Several transitions are one event: per-destination delivery admits only a
// few concurrent events, so one event per finding would be dropped.
func TestBrowserFindingTransitionsAreOneEvent(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	server, store := baselineTestServer(t)
	received := make(chan map[string]any, 16)
	hook := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		received <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer hook.Close()
	server.runner.notifier = newNotificationDispatcher(hook.Client())
	settings := server.runner.GetSystemSettings()
	settings.Notifications = NotificationsSettings{Webhooks: []NotificationWebhookSettings{{Enabled: true, URL: hook.URL, Format: notificationFormatSlack, Events: []string{"browser_finding"}}}}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	pin := saveBrowserPolicy(t, store, "browser", 1, e1Policy(e1LCP, e1CLS))
	e1Archive(t, server, store, "browser-many", &pin,
		e1Document{id: 1, metric: "lcp", value: 5000, qualified: true}, e1Document{id: 2, metric: "lcp", engine: "webkit", device: "iphone", value: 5000, qualified: true},
		e1Document{id: 1, metric: "cls", value: 0.5, qualified: true})
	server.autoQueueBrowserIntelligence("browser-many")
	if ran, err := server.runNextEvaluationJob(context.Background()); err != nil || !ran {
		t.Fatalf("worker ran=%v err=%v", ran, err)
	}
	select {
	case payload := <-received:
		if !strings.Contains(fmt.Sprint(payload["blocks"]), "3 browser findings") {
			t.Fatalf("webhook %v", payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no webhook")
	}
	select {
	case payload := <-received:
		t.Fatalf("a second event %v", payload)
	case <-time.After(300 * time.Millisecond):
	}
}

// Attaching a finalized archive queues the evaluation; a failed evaluation
// is reported as such.
func TestBrowserIntelligenceQueuesOnAttachAndReportsFailures(t *testing.T) {
	server, store := baselineTestServer(t)
	pin := saveBrowserPolicy(t, store, "browser", 1, e1Policy(e1LCP))
	record := e1Archive(t, server, store, "browser-attach", &pin, e1Document{id: 1, metric: "lcp", value: 5000, qualified: true})
	server.runner.attachFinalizedRunArchive("browser-attach", record, nil)
	deadline := time.Now().Add(5 * time.Second)
	for {
		jobs, _ := pinnedBrowserEvaluations(store, storage.DefaultProjectID, "browser-attach")
		if len(jobs) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("attaching the archive queued no evaluation")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The archive changes before the queued evaluation runs: it fails, and
	// the run says so instead of "not evaluated".
	raw, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw[:len(raw)-1], []byte(`,"test_name":"refreshed"}`)...)
	if err := os.WriteFile(record.ManifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	record.ManifestSHA256 = hex.EncodeToString(digest[:])
	if ran, err := server.runNextEvaluationJob(context.Background()); err != nil || !ran {
		t.Fatalf("worker ran=%v err=%v", ran, err)
	}
	if response := getRunBrowserFindings(t, server, "browser-attach"); response.Status != "failed" || response.Failure == "" {
		t.Fatalf("failed status %+v", response)
	}
}
