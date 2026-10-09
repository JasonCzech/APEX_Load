package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRCAExpandedElasticPreservesSnapshotAndQuerySemantics(t *testing.T) {
	point := analysis.MetricPoint{Source: "elastic-source", MetricName: "cluster_nodes", Kind: analysis.KindGauge, Unit: "count", Provider: &analysis.ProviderMeasurement{Name: "elasticsearch", QueryID: "health-query", MetricID: "health-query", Statistic: "value", WindowKind: "instant"}}
	series, ok := monitorSeriesTemplate(point, storage.DefaultProjectID, "monitor-evidence")
	if !ok || series.Observation != rca.ObservationSnapshot || series.Bucket != 0 {
		t.Fatalf("instantaneous health acquired interval semantics: %+v", series)
	}
	for name, change := range map[string]func(*analysis.MetricPoint){
		"units": func(p *analysis.MetricPoint) { p.Unit = "ms" },
		"window": func(p *analysis.MetricPoint) {
			p.Provider.WindowKind = "native_bucket"
			p.Provider.ResolutionSeconds = 10
		},
		"approximation": func(p *analysis.MetricPoint) { p.Provider.Approximate = true },
		"query":         func(p *analysis.MetricPoint) { p.Provider.QueryID = "different-query" },
	} {
		t.Run(name, func(t *testing.T) {
			altered := point
			meta := *point.Provider
			altered.Provider = &meta
			change(&altered)
			other, ok := monitorSeriesTemplate(altered, storage.DefaultProjectID, "monitor-evidence")
			if !ok || other.ID == series.ID || monitorSeriesKey(altered) == monitorSeriesKey(point) {
				t.Fatal("incompatible series semantics collapsed")
			}
		})
	}
}

// rcaDetectionServer archives runs whose request durations latency gives.
func rcaDetectionServer(t *testing.T, runs map[string]func(i int) float64) (*Server, *storage.SQLiteStore) {
	t.Helper()
	server, store := baselineTestServer(t)
	for id, latency := range runs {
		exemplarArchiveFixture(t, server, store, id, func(i int, tx *TransactionEvent) { tx.DurationMs = latency(i) })
	}
	t.Setenv("APEXLOAD_RCA_V2_PROJECTS", storage.DefaultProjectID)
	return server, store
}

// completedRCAAssessment requests and runs an analysis of run.
func completedRCAAssessment(t *testing.T, server *Server, run string, body map[string]any) *rca.Assessment {
	t.Helper()
	path := "/api/v1/tests/" + run + "/archive/root-cause"
	queued := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodPost, path+"/analyses", body).Body.Bytes())
	if ran, err := server.runNextRCAAnalysis(context.Background(), nil); err != nil || !ran {
		t.Fatalf("run: %v %v", ran, err)
	}
	done := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodGet, path+"/analyses/"+queued.AnalysisID, nil).Body.Bytes())
	if done.State != storage.RCAStateCompleted || done.Assessment == nil {
		t.Fatalf("analysis: %+v", done)
	}
	return done.Assessment
}

func rcaLimitation(a *rca.Assessment, code string) bool {
	for _, l := range a.Limitations {
		if l.Code == code {
			return true
		}
	}
	return false
}

func TestRCADetectsALatencyStepInTheArchive(t *testing.T) {
	// One request a second; from 200 s the cart takes 400 ms instead of 100.
	server, _ := rcaDetectionServer(t, map[string]func(int) float64{"rca-run": func(i int) float64 {
		if i >= 200 {
			return 400 + float64(i%7)
		}
		return 100 + float64(i%7)
	}})
	assessment := completedRCAAssessment(t, server, "rca-run", map[string]any{})
	var step *rca.Anomaly
	for i := range assessment.Anomalies {
		if assessment.Anomalies[i].Series == "action/GET /cart/p95_ms" {
			step = &assessment.Anomalies[i]
		}
	}
	if step == nil {
		t.Fatalf("no latency anomaly: %+v", assessment.Anomalies)
	}
	if onset := step.Onset.Sub(exemplarFixtureStart); onset < 190*time.Second || onset > 210*time.Second {
		t.Fatalf("onset %s after start", onset)
	}
	if step.Scope != rca.ScopeTime || step.Direction != rca.DirectionIncrease || step.Effect < 250 || step.Level != "vusers=5" ||
		len(step.Evidence) != 1 || !strings.HasPrefix(step.Evidence[0].Locator, intervalDistributionsArchiveFileName+"#action=GET /cart?") {
		t.Fatalf("%+v", step)
	}
	d := assessment.Detection
	if d.Status != rca.DetectionComplete || d.ParamsVersion != rca.DetectParamsVersion || d.SeriesEvaluated != 3 || len(d.Levels) != 1 ||
		assessment.Outcome != rca.OutcomeInsufficientEvidence || !rcaLimitation(assessment, rca.LimitationSignaturesUnmeasured) ||
		!rcaLimitation(assessment, limitationIntervalDistributionsReconstructed) || !rcaLimitation(assessment, limitationActionsJudged) {
		t.Fatalf("detection %+v limitations %+v", d, assessment.Limitations)
	}
}

func TestRCADetectsADifferenceFromTheBaselineRun(t *testing.T) {
	server, _ := rcaDetectionServer(t, map[string]func(int) float64{
		"rca-run":      func(i int) float64 { return 400 + float64(i%7) },
		"rca-baseline": func(i int) float64 { return 100 + float64(i%7) },
	})
	assessment := completedRCAAssessment(t, server, "rca-run", map[string]any{"baseline_test_id": "rca-baseline"})
	var load *rca.Anomaly
	for i := range assessment.Anomalies {
		if a := &assessment.Anomalies[i]; a.Scope == rca.ScopeLoad && a.Series == "action/GET /cart/p95_ms" {
			load = a
		}
		if assessment.Anomalies[i].Scope == rca.ScopeTime {
			t.Fatalf("steady run has a time-scoped anomaly: %+v", assessment.Anomalies[i])
		}
	}
	// The whole level is slower than the baseline's same level.
	if load == nil || load.Baseline.TestID != "rca-baseline" || load.Baseline.Qualification == "" || load.Effect < 250 ||
		load.Onset.Sub(exemplarFixtureStart) > 40*time.Second {
		t.Fatalf("%+v", assessment.Anomalies)
	}
	// The baseline's own evidence limits are part of its coverage.
	for _, c := range assessment.Coverage {
		if c.Evidence == "baseline" && (c.Status != rca.CoveragePartial || !strings.Contains(c.Detail, "baseline evidence: actions_judged, interval_distributions_reconstructed")) {
			t.Fatalf("baseline coverage %+v", c)
		}
	}
}

func TestRCADetectionEvidenceBudgetAndUnreadableSidecars(t *testing.T) {
	server, store := baselineTestServer(t)
	record := exemplarArchiveFixture(t, server, store, "rca-run", nil)
	value := 1.0
	var points []analysis.MetricPoint
	for i := 0; i < 36; i++ {
		for _, metric := range []string{"cpu", "memory"} {
			points = append(points, analysis.MetricPoint{Source: "prom-1", MetricName: metric, Kind: analysis.KindGauge, Quality: analysis.QualityValid, TimestampUTC: exemplarFixtureStart.Add(time.Duration(i) * 10 * time.Second), Value: &value,
				Provider: &analysis.ProviderMeasurement{Name: monitorprovider.ProviderPrometheus, Mode: "evaluation_point", EvaluationStepSeconds: 10}})
		}
	}
	writeArchiveSidecar(t, record, "monitor_points.jsonl", points, func(m *RunArchiveManifest) { m.MonitorEvidenceFile = "monitor_points.jsonl" })
	manifest, _ := loadRunArchiveManifestFromRecord(record)
	summaries, _ := readAllArchiveSummaries(record)
	// The run's three action series take the budget first; the monitor
	// series get what is left.
	budget := rca.DefaultBudget
	budget.MaxSeries = 4
	budgeter := rca.NewBudgeter(budget, nil, nil)
	input, err := archiveDetectionInput(context.Background(), record, manifest, summaries, rca.ArrivalClosed, budgeter, budget, nil)
	if err != nil {
		t.Fatal(err)
	}
	providers := 0
	for _, s := range input.Series {
		if s.Kind == rca.SeriesProvider {
			providers++
		}
	}
	if len(input.Series) != 4 || providers != 1 || !strings.Contains(limitationCodes(input.Limitations), limitationDetectionEvidenceCut) {
		t.Fatalf("%d series (%d provider), limitations %+v", len(input.Series), providers, input.Limitations)
	}
	if cut := budgeter.Truncations(); len(cut) != 1 || cut[0].Limit != rca.BudgetSeries || cut[0].Offered != 5 {
		t.Fatalf("truncations %+v", cut)
	}
	// A corrupt sidecar costs its evidence, not the analysis.
	if err := os.WriteFile(filepath.Join(record.ArchivePath, "monitor_points.jsonl"), []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	input, err = archiveDetectionInput(context.Background(), record, manifest, summaries, rca.ArrivalClosed, rca.NewBudgeter(rca.DefaultBudget, nil, nil), rca.DefaultBudget, nil)
	if err != nil || len(input.Series) != 3 || !strings.Contains(limitationCodes(input.Limitations), limitationDetectionEvidenceUnreadable) {
		t.Fatalf("%v: %d series, %+v", err, len(input.Series), input.Limitations)
	}
	// Cancellation still fails the read.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := archiveDetectionInput(cancelled, record, manifest, summaries, rca.ArrivalClosed, rca.NewBudgeter(rca.DefaultBudget, nil, nil), rca.DefaultBudget, nil); err == nil {
		t.Fatal("cancelled read succeeded")
	}
}

// writeArchiveSidecar adds a JSONL sidecar to an archive and names it in the
// manifest.
func writeArchiveSidecar[T any](t *testing.T, record *storage.RunArchiveRecord, name string, rows []T, set func(*RunArchiveManifest)) {
	t.Helper()
	var lines []byte
	for _, row := range rows {
		line, _ := json.Marshal(row)
		lines = append(append(lines, line...), '\n')
	}
	if err := os.WriteFile(filepath.Join(record.ArchivePath, name), lines, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	set(manifest)
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(record.ManifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRCADetectionInputFromSidecars(t *testing.T) {
	server, store := baselineTestServer(t)
	record := exemplarArchiveFixture(t, server, store, "rca-run", nil)
	var distributions []IntervalDistributionRecord
	for i := 1; i <= 36; i++ {
		transactions := map[string]IntervalDistributionMetric{"checkout": {Count: 10, Errors: 1, P95: 120}}
		if i == 20 {
			// Checkout completed nothing in this interval.
			transactions = map[string]IntervalDistributionMetric{}
		}
		distributions = append(distributions, IntervalDistributionRecord{Schema: intervalDistributionSchema, Timestamp: exemplarFixtureStart.Add(time.Duration(i) * 10 * time.Second), WindowSeconds: 10,
			Dimensions: map[string]map[string]IntervalDistributionMetric{"transaction": transactions, "action": {"GET /cart": {Count: 10, P95: 100}}}})
	}
	writeArchiveSidecar(t, record, intervalDistributionsArchiveFileName, distributions, func(m *RunArchiveManifest) { m.IntervalDistributionsFile = intervalDistributionsArchiveFileName })
	value := func(v float64) *float64 { return &v }
	var points []analysis.MetricPoint
	for i := 0; i < 36; i++ {
		at := exemplarFixtureStart.Add(time.Duration(i) * 10 * time.Second)
		gauge := analysis.MetricPoint{Source: "prom-1", MetricName: "cpu", Kind: analysis.KindGauge, Unit: "%", Quality: analysis.QualityValid, TimestampUTC: at, Value: value(40),
			Dimensions: map[string]string{"pod": "api-1"}, Provider: &analysis.ProviderMeasurement{Name: monitorprovider.ProviderPrometheus, Mode: "evaluation_point", EvaluationStepSeconds: 10, Statistic: "value"}}
		counter := gauge
		counter.MetricName, counter.Kind = "requests_total", analysis.KindCounter
		unknown := gauge
		unknown.Provider = &analysis.ProviderMeasurement{Name: "not-a-provider"}
		points = append(points, gauge, counter, unknown)
	}
	writeArchiveSidecar(t, record, "monitor_points.jsonl", points, func(m *RunArchiveManifest) { m.MonitorEvidenceFile = "monitor_points.jsonl" })
	manifest, _ := loadRunArchiveManifestFromRecord(record)
	summaries, err := readAllArchiveSummaries(record)
	if err != nil {
		t.Fatal(err)
	}
	budgeter := rca.NewBudgeter(rca.DefaultBudget, nil, nil)
	input, err := archiveDetectionInput(context.Background(), record, manifest, summaries, rca.ArrivalClosed, budgeter, rca.DefaultBudget, nil)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]rca.Series{}
	for _, s := range input.Series {
		byID[s.ID] = s
	}
	// Business transactions take precedence over actions.
	throughput, ok := byID["transaction/checkout/throughput"]
	if !ok || byID["action/GET /cart/p95_ms"].ID != "" || len(throughput.Points) != 36 || byID["transaction/checkout/error_rate"].Points[0].Value != 10 {
		t.Fatalf("series %v", byID)
	}
	zeros := 0
	for _, p := range throughput.Points {
		if p.Valid && p.Value == 0 {
			zeros++
		}
	}
	if zeros != 1 || len(byID["transaction/checkout/p95_ms"].Points) != 35 {
		t.Fatalf("absent interval: %d zero points, %d p95 points", zeros, len(byID["transaction/checkout/p95_ms"].Points))
	}
	var gauge, counter int
	for _, s := range input.Series {
		if s.Kind != rca.SeriesProvider {
			continue
		}
		switch s.Observation {
		case rca.ObservationSnapshot:
			gauge++
			if s.Bucket != 10*time.Second || len(s.Points) != 36 || !strings.HasPrefix(s.ID, "monitor/prometheus/prom-1/cpu/") || s.Evidence.Kind != "monitor_series" {
				t.Fatalf("gauge %+v", s)
			}
		case rca.ObservationCumulative:
			counter++
		}
	}
	if gauge != 1 || counter != 1 || len(input.Levels) != 1 || input.Levels[0].VUsers != 5 {
		t.Fatalf("%d gauges, %d counters, levels %+v", gauge, counter, input.Levels)
	}
	codes := map[string]bool{}
	for _, l := range input.Limitations {
		codes[l.Code] = true
	}
	if !codes[limitationMonitorSemanticsUnknown] || codes[limitationIntervalDistributionsReconstructed] || codes[limitationActionsJudged] {
		t.Fatalf("limitations %+v", input.Limitations)
	}
	// The counter is excluded from detection with a limitation.
	_, detection, limitations := rca.Detect(input, rca.DefaultDetectParams, budgeter)
	if detection.SeriesExcluded != 1 || !strings.Contains(limitationCodes(limitations), rca.LimitationCumulativeExcluded) {
		t.Fatalf("%+v %+v", detection, limitations)
	}
}

// A script without authored transactions records the engine's automatic
// per-iteration transaction under the script's name; its actions are judged
// beside it, so a slow request is named, not only a slow iteration.
func TestRCAJudgesActionsBesideAutomaticIterationTransactions(t *testing.T) {
	server, store := baselineTestServer(t)
	record := exemplarArchiveFixture(t, server, store, "rca-run", nil)
	var distributions []IntervalDistributionRecord
	for i := 1; i <= 36; i++ {
		distributions = append(distributions, IntervalDistributionRecord{Schema: intervalDistributionSchema, Timestamp: exemplarFixtureStart.Add(time.Duration(i) * 10 * time.Second), WindowSeconds: 10,
			Dimensions: map[string]map[string]IntervalDistributionMetric{
				"transaction": {"Checkout script": {Count: 10, P95: 300}},
				"action":      {"GET /cart": {Count: 10, P95: 100}, "POST /pay": {Count: 10, P95: 200}},
			}})
	}
	writeArchiveSidecar(t, record, intervalDistributionsArchiveFileName, distributions, func(m *RunArchiveManifest) {
		m.IntervalDistributionsFile = intervalDistributionsArchiveFileName
		m.Provenance.Groups = []TestGroup{{Name: "shop", ScriptName: "Checkout script"}}
		if m.InitialProvenance != nil {
			m.InitialProvenance.Groups = m.Provenance.Groups
		}
	})
	manifest, _ := loadRunArchiveManifestFromRecord(record)
	read := func(manifest *RunArchiveManifest) (map[string]bool, string) {
		t.Helper()
		reader, _, limitations, err := readTransactionSeries(context.Background(), record, manifest, rca.NewBudgeter(rca.DefaultBudget, nil, nil), -1, rca.MaxSeriesPoints)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, s := range reader.list() {
			ids[s.ID] = true
		}
		return ids, limitationCodes(limitations)
	}
	ids, codes := read(manifest)
	if !ids["transaction/Checkout script/p95_ms"] || !ids["action/GET /cart/p95_ms"] || !ids["action/POST /pay/p95_ms"] || !strings.Contains(codes, limitationActionsJudged) {
		t.Fatalf("series %v, limitations %s", ids, codes)
	}
	// A transaction the script authored takes precedence, as before.
	authored := *manifest
	authored.Provenance.Groups = []TestGroup{{Name: "shop", ScriptName: "Another script"}}
	authored.InitialProvenance = nil
	if ids, codes := read(&authored); ids["action/GET /cart/p95_ms"] || !ids["transaction/Checkout script/p95_ms"] || strings.Contains(codes, limitationActionsJudged) {
		t.Fatalf("authored transaction: series %v, limitations %s", ids, codes)
	}
}

func limitationCodes(limitations []rca.Limitation) string {
	codes := make([]string, 0, len(limitations))
	for _, l := range limitations {
		codes = append(codes, l.Code)
	}
	return strings.Join(codes, ",")
}

func TestRCAMonitorSeriesIdentity(t *testing.T) {
	uncertainty := 250.0
	point := analysis.MetricPoint{Source: "dt-1", MetricName: "builtin:service.response.time", Kind: analysis.KindGauge, Unit: "ms",
		Dimensions: map[string]string{"service": "checkout", "region": "eu"}, Timing: &analysis.PointTiming{ClockUncertaintyMs: &uncertainty},
		Provider: &analysis.ProviderMeasurement{Name: monitorprovider.ProviderDynatrace, ResolutionSeconds: 60, Statistic: "avg"}}
	first, ok := monitorSeriesTemplate(point, "project-a", "monitor_points.jsonl")
	if !ok || first.Observation != rca.ObservationInterval || first.Bucket != time.Minute || first.ClockUncertainty != 250*time.Millisecond {
		t.Fatalf("%+v", first)
	}
	// Dimension order does not change the series; a dimension value does.
	point.Dimensions = map[string]string{"region": "eu", "service": "checkout"}
	if again, _ := monitorSeriesTemplate(point, "project-a", "monitor_points.jsonl"); again.ID != first.ID {
		t.Fatalf("%s != %s", again.ID, first.ID)
	}
	point.Dimensions["service"] = "cart"
	if other, _ := monitorSeriesTemplate(point, "project-a", "monitor_points.jsonl"); other.ID == first.ID {
		t.Fatal("different dimensions share a series")
	}
}

func TestRCABundleKeepsTheStrongestAnomalies(t *testing.T) {
	assessment := rca.Assessment{SchemaVersion: rca.AssessmentSchemaVersion}
	for i := 0; i < 500; i++ {
		assessment.Anomalies = append(assessment.Anomalies, rca.Anomaly{ID: fmt.Sprintf("anomaly-%03d", i), Series: strings.Repeat("s", 400), Onset: exemplarFixtureStart.Add(time.Duration(i) * time.Second), PeakZ: float64(i % 50)})
		if i < 200 {
			assessment.Detection.Spikes = append(assessment.Detection.Spikes, rca.Spike{Series: strings.Repeat("s", 200), Z: 9})
		}
	}
	bundle := boundRCABundle(RCABundle{Schema: rcaBundleSchema, Assessment: assessment})
	body, _ := json.Marshal(bundle)
	kept := bundle.Assessment.Anomalies
	if !bundle.Truncated || len(body) > rcaMaxBundleBytes || len(kept) == 0 || len(bundle.Assessment.Detection.Spikes) != 0 {
		t.Fatalf("bundle %d bytes, %d anomalies, %d spikes", len(body), len(kept), len(bundle.Assessment.Detection.Spikes))
	}
	for i := range kept {
		if math.Abs(kept[i].PeakZ) < 25 || i > 0 && kept[i].Onset.Before(kept[i-1].Onset) {
			t.Fatalf("kept a weak or misordered anomaly: %+v", kept[i])
		}
	}
}

func TestRCAStoredAssessmentIsTrimmedNotFailed(t *testing.T) {
	assessment := &rca.Assessment{SchemaVersion: rca.AssessmentSchemaVersion}
	for i := 0; i < 4000; i++ {
		assessment.Anomalies = append(assessment.Anomalies, rca.Anomaly{ID: fmt.Sprintf("a-%04d", i), Series: strings.Repeat("s", 600), Onset: exemplarFixtureStart.Add(time.Duration(i) * time.Second), PeakZ: float64(i % 50)})
	}
	// The top hypothesis cites two weak anomalies; they must survive.
	assessment.Hypotheses = []rca.Hypothesis{{ID: "h", Rank: 1, Symptoms: []string{"a-0000"}, Chain: []rca.ChainStep{{From: "a-0001", To: "a-0000"}}}}
	payload := rcaAnalysisPayload{Assessment: assessment}
	body, err := boundStoredRCAPayload(&payload)
	if err != nil || len(body) > storage.MaxRCAPayloadBytes || !rcaLimitation(payload.Assessment, limitationAssessmentTrimmed) {
		t.Fatalf("%v: %d bytes, limitations %+v", err, len(body), payload.Assessment.Limitations)
	}
	ids := map[string]bool{}
	for _, a := range payload.Assessment.Anomalies {
		ids[a.ID] = true
	}
	if !ids["a-0000"] || !ids["a-0001"] || len(payload.Assessment.Hypotheses) != 1 {
		t.Fatalf("cited anomalies cut: %d kept", len(ids))
	}
}

// Advisory browser baseline results are trimmed before anomalies and
// hypotheses, and a bundle's trim never touches the stored assessment.
func TestRCABrowserBaselineIsTrimmedBeforeHypotheses(t *testing.T) {
	assessment := &rca.Assessment{SchemaVersion: rca.AssessmentSchemaVersion, BrowserBaseline: &rca.BrowserBaseline{Version: rca.BrowserBaselineVersion, Status: rca.DetectionComplete, Limitations: []rca.Limitation{}}}
	for i := 0; i < 2000; i++ {
		assessment.BrowserBaseline.Results = append(assessment.BrowserBaseline.Results, rca.BrowserBaselineResult{ID: fmt.Sprintf("b-%04d", i), Series: strings.Repeat("s", 600), State: rca.BrowserBaselineWithinRange})
	}
	assessment.Anomalies = []rca.Anomaly{{ID: "a-0000", Series: "latency"}}
	assessment.Hypotheses = []rca.Hypothesis{{ID: "h", Rank: 1, Symptoms: []string{"a-0000"}}}
	bundle := boundRCABundle(RCABundle{Assessment: *assessment})
	if len(bundle.Assessment.Hypotheses) != 1 || len(bundle.Assessment.Anomalies) != 1 {
		t.Fatalf("hypotheses cut before browser baseline results: %+v", bundle.Assessment.Hypotheses)
	}
	trimmed := bundle.Assessment.BrowserBaseline
	if len(trimmed.Results) >= 2000 || trimmed.Status != rca.DetectionPartial || trimmed.Limitations[len(trimmed.Limitations)-1].Code != rca.BrowserBaselineResultsTruncated {
		t.Fatalf("browser baseline not trimmed: %d results, %s", len(trimmed.Results), trimmed.Status)
	}
	if len(assessment.BrowserBaseline.Results) != 2000 || assessment.BrowserBaseline.Status != rca.DetectionComplete {
		t.Fatal("the bundle's trim changed the stored assessment")
	}
}

func TestRCABaselineComparesNetworkPhases(t *testing.T) {
	server, _ := baselineTestServer(t)
	_, store := server, server.store.(*storage.SQLiteStore)
	for id, tls := range map[string]float64{"rca-run": 120, "rca-baseline": 20} {
		exemplarArchiveFixture(t, server, store, id, func(i int, tx *TransactionEvent) {
			tx.DependencyTarget, tx.DurationMs, tx.TLSMs = "api.shop.local", 100+float64(i%7), tls+float64(i%5)
		})
	}
	t.Setenv("APEXLOAD_RCA_V2_PROJECTS", storage.DefaultProjectID)
	assessment := completedRCAAssessment(t, server, "rca-run", map[string]any{"baseline_test_id": "rca-baseline"})
	found := false
	for _, a := range assessment.Anomalies {
		if a.Series == "dependency/http:api.shop.local/tls_ms" && a.Scope == rca.ScopeLoad && a.Direction == rca.DirectionIncrease {
			found = true
		}
	}
	if !found {
		t.Fatalf("no load-scope TLS anomaly: %+v", assessment.Anomalies)
	}
	for _, c := range assessment.Coverage {
		// Only TLS time was spent, so it is the one timing series.
		if c.Evidence == "detection_series" && !strings.Contains(c.Detail, "1 dependency timing series") {
			t.Fatalf("coverage %+v", c)
		}
	}
}
