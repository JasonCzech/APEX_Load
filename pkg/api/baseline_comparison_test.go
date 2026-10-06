package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func comparisonSummaries(start time.Time, loads []int) []IntervalSummaryRecord {
	out := []IntervalSummaryRecord{}
	for i, load := range loads {
		active := float64((i + 1) * 5)
		out = append(out, IntervalSummaryRecord{Timestamp: start.Add(time.Duration(i+1) * 5 * time.Second), SampleWindowSeconds: 5, ActiveRuntimeSeconds: &active, ActiveVUsers: int64(load), Groups: map[string]*GroupLiveMetrics{"checkout": {Name: "checkout", VUsers: load}}})
	}
	return out
}
func repeatComparisonLoad(load, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = load
	}
	return out
}
func TestBaselineComparisonStableAlignment(t *testing.T) {
	start := time.Now().UTC()
	summaries := comparisonSummaries(start, repeatComparisonLoad(100, 24))
	windows := baselineStableWindows(summaries, start, "checkout", BaselineComparisonWindow{})
	if len(windows) != 1 || windows[0].StartSeconds != 30 || windows[0].EndSeconds != 120 || math.Abs(windows[0].Load-100) > 1e-6 {
		t.Fatalf("windows=%+v", windows)
	}
	for _, tc := range []struct {
		name            string
		load, tolerance float64
		match           bool
	}{{"exact", 100, 0, true}, {"boundary", 105, 5, true}, {"outside", 105.01, 5, false}, {"exact excludes approximate", 101, 0, false}} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := windows[0]
			candidate.Load = tc.load
			if got := matchingBaselineWindow(windows[0], []BaselineLoadWindow{candidate}, tc.tolerance); (got != nil) != tc.match {
				t.Fatalf("match=%v", got)
			}
		})
	}
	exact, approx := windows[0], windows[0]
	approx.Load = 101
	if got := matchingBaselineWindow(exact, []BaselineLoadWindow{approx, exact}, 5); got == nil || math.Abs(got.Load-100) > 1e-6 {
		t.Fatal("did not prefer exact load")
	}
	if got := baselineStableWindows(summaries, start, "checkout", BaselineComparisonWindow{StartSeconds: 70}); len(got) != 0 {
		t.Fatalf("short measurement window accepted: %+v", got)
	}
	summaries[12].ActiveRuntimeSeconds = summaries[11].ActiveRuntimeSeconds
	if got := baselineStableWindows(summaries, start, "checkout", BaselineComparisonWindow{}); len(got) != 0 {
		t.Fatalf("pause bridged: %+v", got)
	}
	ramp := append(repeatComparisonLoad(50, 24), repeatComparisonLoad(100, 24)...)
	ramp = append(ramp, repeatComparisonLoad(50, 24)...)
	got := baselineStableWindows(comparisonSummaries(start, ramp), start, "checkout", BaselineComparisonWindow{})
	if len(got) != 3 || got[1].Direction != "increasing" || got[2].Direction != "decreasing" {
		t.Fatalf("directions=%+v", got)
	}
	candidate := got[1]
	candidate.Direction = "decreasing"
	if matchingBaselineWindow(got[1], []BaselineLoadWindow{candidate}, 5) != nil {
		t.Fatal("mixed ramp directions")
	}
}

// Real runs' summary timestamps sit a millisecond or two off their measured
// windows; that jitter must not split a steady level, while a missed interval
// still does.
func TestBaselineStableWindowsTolerateTimestampJitter(t *testing.T) {
	start := time.Now().UTC()
	summaries := comparisonSummaries(start, repeatComparisonLoad(100, 24))
	for i := range summaries {
		jitter := time.Duration(i%3-1) * 1500 * time.Microsecond
		summaries[i].Timestamp = summaries[i].Timestamp.Add(jitter)
	}
	// A steady load averages to that load within the 1e-9 that exact
	// baseline matching allows.
	if windows := baselineStableWindows(summaries, start, "checkout", BaselineComparisonWindow{}); len(windows) != 1 || windows[0].EndSeconds-windows[0].StartSeconds < 85 || math.Abs(windows[0].Load-100) > 1e-9 || math.Abs(windows[0].TotalLoad-100) > 1e-9 {
		t.Fatalf("jittered steady level = %+v", windows)
	}
	missed := append(append([]IntervalSummaryRecord{}, summaries[:12]...), summaries[13:]...)
	for _, w := range baselineStableWindows(missed, start, "checkout", BaselineComparisonWindow{}) {
		if w.StartSeconds < summaries[12].Timestamp.Sub(start).Seconds() && w.EndSeconds > summaries[12].Timestamp.Sub(start).Seconds() {
			t.Fatalf("a missed interval was bridged: %+v", w)
		}
	}
}

func TestBaselineResourceTimeWeightingGapsAndCounters(t *testing.T) {
	w := BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{0, 100}, Load: 100}
	s := baselineResourceSeries{kind: "gauge", instances: 2, points: []BaselineMetricPoint{{Seconds: 0, Value: 10}, {Seconds: 10, Value: 20}, {Seconds: 20, Value: 30}, {Seconds: 30, Value: 40}, {Seconds: 90, Value: 50}, {Seconds: 100, Value: 60}}}
	m := baselineResourceMeasurement(s, w)
	if m == nil || m.CoveragePct != 40 || m.Mean != 27.5 || m.Instances != 2 {
		t.Fatalf("gap coverage=%+v", m)
	}
	s.kind = "counter"
	s.points = []BaselineMetricPoint{{Seconds: 0, Value: 100}, {Seconds: 10, Value: 120}, {Seconds: 20, Value: 5}, {Seconds: 30, Value: 25}}
	m = baselineResourceMeasurement(s, BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{0, 30}})
	if m == nil || m.Mean != 2 || math.Abs(m.CoveragePct-200.0/3) > 1e-6 {
		t.Fatalf("counter resets=%+v", m)
	}
	if delta, pct := baselineDelta(10, 0); *delta != 10 || pct != nil {
		t.Fatal("baseline zero produced a percentage")
	}
	for _, tc := range []struct {
		unit   string
		factor float64
	}{{"MiB", 1048576}, {"MB", 1e6}, {"bytes", 1}} {
		unit, factor := canonicalBaselineUnit(tc.unit)
		if unit != "bytes" || factor != tc.factor {
			t.Fatalf("unit=%s factor=%v", unit, factor)
		}
	}
}

func comparisonArchiveFixture(t *testing.T, server *Server, store *storage.SQLiteStore, id string, latency, heap float64) *ManagedTest {
	t.Helper()
	start := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	end := start.Add(120 * time.Second)
	groups := []TestGroup{{Name: "checkout", Protocol: "http", VUsers: 100, ScriptID: "checkout-v1"}, {Name: "search", Protocol: "http", VUsers: 100, ScriptID: "search-v1"}}
	p := RunArchiveProvenance{ProjectID: storage.DefaultProjectID, ScenarioID: "scenario", StartedAt: &start, TotalVUsers: 200, Duration: "2m", Groups: groups, ComparisonFingerprint: "v1:fixture", ControllerEnv: map[string]string{"APEXLOAD_ENV": "test"}, Assignment: &GeneratorAssignmentSettings{}, GeneratorInventory: []GeneratorInventoryEntry{{}}, ComparisonMetrics: map[string]BaselineMetricDefinition{"java_heap": {Identity: "java/heap", Unit: "bytes", Kind: "gauge", Source: "application"}}}
	manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: id, TestName: id, Status: "COMPLETE", CreatedAt: start, CompletedAt: end, Provenance: p, InitialProvenance: &p, TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "interval_summaries.jsonl"}
	root := t.TempDir()
	write := func(name string, value any) {
		t.Helper()
		f, err := os.Create(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		e := json.NewEncoder(f)
		switch v := value.(type) {
		case []TransactionEvent:
			for _, item := range v {
				if err = e.Encode(item); err != nil {
					t.Fatal(err)
				}
			}
		case []ResourceSample:
			for _, item := range v {
				if err = e.Encode(item); err != nil {
					t.Fatal(err)
				}
			}
		case []IntervalSummaryRecord:
			for _, item := range v {
				if err = e.Encode(item); err != nil {
					t.Fatal(err)
				}
			}
		default:
			if err = e.Encode(v); err != nil {
				t.Fatal(err)
			}
		}
	}
	txs := []TransactionEvent{}
	for i := 0; i < 2400; i++ {
		group := "checkout"
		if i%2 == 1 {
			group = "search"
		}
		txs = append(txs, TransactionEvent{Timestamp: start.Add(31*time.Second + time.Duration(i)*35*time.Millisecond), Group: group, SourceTransaction: "Purchase", ActionName: "request", Population: "transaction", Success: true, DurationMs: latency})
	}
	summaries := comparisonSummaries(start, repeatComparisonLoad(200, 24))
	for i := range summaries {
		summaries[i].Groups = map[string]*GroupLiveMetrics{"checkout": {Name: "checkout", VUsers: 100}, "search": {Name: "search", VUsers: 100}}
	}
	resources := []ResourceSample{}
	for i := 0; i <= 24; i++ {
		resources = append(resources, ResourceSample{Timestamp: start.Add(time.Duration(i) * 5 * time.Second), ActiveVUsers: 200, MemProcessMB: 100, CPUProcessPct: 20, External: map[string]float64{"java_heap": heap}})
	}
	manifest.TransactionCount = int64(len(txs))
	manifest.ResourceSampleCount = int64(len(resources))
	manifest.IntervalSummaryCount = int64(len(summaries))
	write("manifest.json", manifest)
	write(manifest.TransactionsFile, txs)
	write(manifest.ResourceSamplesFile, resources)
	write(manifest.IntervalSummariesFile, summaries)
	test := &ManagedTest{ID: id, Name: id, ProjectID: storage.DefaultProjectID, ScenarioID: "scenario", Status: "COMPLETE", Groups: groups, TotalVUsers: 200, DurationStr: "2m", CreatedAt: start, StartedAt: &start, EndedAt: &end, BaselinePinned: true}
	if err := store.SaveTest(&storage.TestRecord{ID: id, Name: id, ProjectID: storage.DefaultProjectID, ScenarioID: "scenario", Status: "COMPLETE", Groups: []any{}, StartTime: start, BaselinePinned: true}); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{TestID: id, SchemaVersion: archiveSchemaVersion, ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json"), CreatedAt: start, CompletedAt: end, TransactionCount: int64(len(txs)), ResourceSampleCount: int64(len(resources)), IntervalSummaryCount: int64(len(summaries))}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	server.runner.tests[id] = test
	server.runner.archives[id] = record
	return test
}

func baselineTestServer(t *testing.T) (*Server, *storage.SQLiteStore) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.SaveScenario(&storage.Scenario{ID: "scenario", Name: "Scenario", Duration: "2m", CreatedAt: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	return NewServer(ServerConfig{Store: store}), store
}

func TestBaselineComparisonEngineHeapAndTransactionEvidence(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
	baseline := comparisonArchiveFixture(t, server, store, "baseline", 100, 100)
	request := BaselineComparisonRequest{BaselineIDs: []string{baseline.ID}, Population: "transaction"}
	p := defaultBaselineComparisonPolicy()
	a, err := server.readBaselineComparisonRun(context.Background(), current, request, p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := server.readBaselineComparisonRun(context.Background(), baseline, request, p)
	if err != nil {
		t.Fatal(err)
	}
	result := compareBaselineRuns(a, []*baselineComparisonRun{b}, request, p)
	if result == nil || len(result.Transactions) != 2 {
		t.Fatalf("transaction identities merged: %+v", result)
	}
	for _, row := range result.Transactions {
		if row.Current == nil || row.Current.P95 != 200 || row.Baselines[baseline.ID].Measurement.P95 != 100 {
			t.Fatalf("latency=%+v", row)
		}
	}
	found := false
	for _, f := range result.Findings {
		if f.Category == "resource" && strings.Contains(f.Message, "java_heap") && f.Status == "changed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("heap increase not qualified: %+v", result.Findings)
	}
	// Decreases are findings too; different loads must not be called improvements.
	for key, series := range a.resources {
		if key == "external/java_heap" {
			for i := range series.points {
				series.points[i].Value = 50
			}
			a.resources[key] = series
		}
	}
	lower := compareBaselineRuns(a, []*baselineComparisonRun{b}, request, p)
	found = false
	for _, f := range lower.Findings {
		if f.Category == "resource" && strings.Contains(f.Message, "java_heap") && strings.Contains(f.Message, "-50") {
			found = true
		}
	}
	if !found {
		t.Fatal("heap decrease was hidden")
	}
	for i := range b.windows {
		b.windows[i].Load *= 2
	}
	different := compareBaselineRuns(a, []*baselineComparisonRun{b}, request, p)
	for _, f := range different.Findings {
		if f.Status == "changed" {
			t.Fatalf("different load generated qualified finding: %+v", f)
		}
	}
}

func TestBaselineComparisonHTTPPersistenceScopeAndExports(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
	baseline := comparisonArchiveFixture(t, server, store, "baseline", 100, 100)
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}
	body := BaselineComparisonRequest{BaselineIDs: []string{baseline.ID}, Population: "transaction"}
	response := call("POST", "/api/v1/tests/"+current.ID+"/baseline-comparisons", body)
	if response.Code != 201 {
		t.Fatalf("create=%d %s", response.Code, response.Body.String())
	}
	var snapshot BaselineComparisonSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Telemetry) != 2 || snapshot.Telemetry[0].RunID != current.ID || snapshot.Telemetry[1].RunID != baseline.ID || snapshot.Telemetry[0].HealthIntervals != 0 || snapshot.Telemetry[0].BreachedIntervalPct != nil {
		t.Fatalf("lost legacy telemetry qualifications %+v", snapshot.Telemetry)
	}
	if snapshot.ID == "" || snapshot.RuleVersion == "" {
		t.Fatal("snapshot provenance missing")
	}
	saved := call("GET", "/api/v1/tests/current/baseline-comparisons/"+snapshot.ID, nil)
	if saved.Code != 200 || !bytes.Equal(bytes.TrimSpace(saved.Body.Bytes()), bytes.TrimSpace(response.Body.Bytes())) {
		t.Fatalf("snapshot changed: %d", saved.Code)
	}
	for _, path := range []string{"/api/v1/tests/current/report?profile=engineer&comparison_id=", "/api/v1/tests/current/archive/report?profile=engineer&format=csv&comparison_id=", "/api/v1/tests/current/archive/report?profile=engineer&comparison_id="} {
		resp := call("GET", path+snapshot.ID, nil)
		if resp.Code != 200 || !strings.Contains(resp.Body.String(), snapshot.ID) {
			t.Fatalf("export %s=%d %s", path, resp.Code, resp.Body.String())
		}
	}
	docx := call("GET", "/api/v1/tests/current/archive/report?profile=engineer&format=docx&comparison_id="+snapshot.ID, nil)
	if docx.Code != 200 {
		t.Fatalf("docx=%d %s", docx.Code, docx.Body.String())
	}
	archive, err := zip.NewReader(bytes.NewReader(docx.Body.Bytes()), int64(docx.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range archive.File {
		if f.Name == "word/document.xml" {
			reader, _ := f.Open()
			data, _ := io.ReadAll(reader)
			reader.Close()
			found = bytes.Contains(data, []byte(snapshot.ID))
		}
	}
	if !found {
		t.Fatal("Word omitted selected comparison")
	}
	// Optional visual-review outputs exercise the actual export endpoints.
	if output := os.Getenv("APEXLOAD_BASELINE_REVIEW_DIR"); output != "" {
		if err := os.MkdirAll(output, 0755); err != nil {
			t.Fatal(err)
		}
		for _, format := range []string{"html", "pdf", "docx"} {
			path := "/api/v1/tests/current/archive/report?profile=engineer&format=" + format + "&comparison_id=" + snapshot.ID
			if format == "html" {
				path = "/api/v1/tests/current/report?profile=engineer&comparison_id=" + snapshot.ID
			}
			export := call("GET", path, nil)
			if export.Code != 200 {
				t.Fatalf("visual %s=%d %s", format, export.Code, export.Body.String())
			}
			if err := os.WriteFile(filepath.Join(output, "baseline-comparison."+format), export.Body.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	csvExport := call("GET", "/api/v1/tests/current/baseline-comparisons/"+snapshot.ID+"?format=csv", nil)
	if csvExport.Code != 200 || !strings.Contains(csvExport.Body.String(), "effective_policy") || !strings.Contains(csvExport.Body.String(), "setup_differences") {
		t.Fatal("CSV omitted assessment context")
	}
	// Report without selection must not inherit enrichment from the shared cache.
	plain := call("GET", "/api/v1/tests/current/archive/report", nil)
	if bytes.Contains(plain.Body.Bytes(), []byte(snapshot.ID)) {
		t.Fatal("comparison leaked into cached report")
	}
	body.BaselineIDs = []string{baseline.ID, baseline.ID}
	if got := call("POST", "/api/v1/tests/current/baseline-comparisons", body); got.Code != 400 {
		t.Fatalf("duplicate=%d", got.Code)
	}
	body.BaselineIDs = []string{baseline.ID}
	baseline.ProjectID = "foreign"
	server.runner.tests[baseline.ID] = baseline
	if got := call("GET", "/api/v1/tests/current/baseline-comparisons/"+snapshot.ID, nil); got.Code != 404 {
		t.Fatalf("scope move exposed saved baseline: %d", got.Code)
	}
	if got := call("POST", "/api/v1/tests/current/baseline-comparisons", body); got.Code != 404 {
		t.Fatalf("foreign baseline=%d", got.Code)
	}
}

func TestBaselineComparisonPoliciesRejectInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*BaselineComparisonPolicy)
	}{{"negative", func(p *BaselineComparisonPolicy) { p.LatencyPct = -1 }}, {"nonfinite", func(p *BaselineComparisonPolicy) { p.ResourcePct = math.Inf(1) }}, {"tolerance", func(p *BaselineComparisonPolicy) { p.LoadTolerancePct = 26 }}, {"bad mapping", func(p *BaselineComparisonPolicy) {
		p.ResourceMappings = []BaselineResourceMapping{{Identity: "heap", Keys: []string{"heap"}, Kind: "unknown", Aggregation: "sum"}}
	}}} {
		t.Run(tc.name, func(t *testing.T) {
			p := defaultBaselineComparisonPolicy()
			tc.mutate(&p)
			if validateBaselineComparisonPolicy(p) == nil {
				t.Fatal("invalid policy accepted")
			}
		})
	}
}

func Example_baselineComparison() {
	fmt.Println("Advisory: +50% Java heap at comparable observed load; inspect setup and coverage.")
	// Output: Advisory: +50% Java heap at comparable observed load; inspect setup and coverage.
}

func TestBaselineResourceAggregationPreservesEvidenceGaps(t *testing.T) {
	a := []BaselineMetricPoint{{Seconds: 0, Value: 100}, {Seconds: 10, Value: 5}, {Seconds: 20, Value: 25}}
	b := []BaselineMetricPoint{{Seconds: 0, Value: 0}, {Seconds: 10, Value: 200}, {Seconds: 20, Value: 220}}
	points := aggregateBaselineResourcePoints([][]BaselineMetricPoint{a, b}, "counter", "sum")
	m := baselineResourceMeasurement(baselineResourceSeries{kind: "gauge", points: points}, BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{0, 20}})
	if m == nil || m.Mean != 4 || m.CoveragePct != 50 {
		t.Fatalf("reset hidden by aggregation: %+v", m)
	}
	for _, aggregation := range []string{"sum", "mean", "max"} {
		t.Run(aggregation, func(t *testing.T) {
			complete, sparse := []BaselineMetricPoint{}, []BaselineMetricPoint{}
			for i := 0; i <= 10; i++ {
				point := BaselineMetricPoint{Seconds: float64(i * 10), Value: 10}
				complete = append(complete, point)
				if i != 5 {
					sparse = append(sparse, point)
				}
			}
			points := aggregateBaselineResourcePoints([][]BaselineMetricPoint{complete, sparse}, "gauge", aggregation)
			m := baselineResourceMeasurement(baselineResourceSeries{kind: "gauge", points: points}, BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{0, 100}})
			expected := 10.0
			if aggregation == "sum" {
				expected = 20
			}
			if m == nil || m.CoveragePct != 80 || m.Mean != expected || m.Samples != 8 {
				t.Fatalf("missing instance evidence bridged: %+v", m)
			}
		})
	}
	if points := aggregateBaselineResourcePoints([][]BaselineMetricPoint{a, nil}, "gauge", "sum"); baselineResourceMeasurement(baselineResourceSeries{kind: "gauge", points: points}, BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{0, 20}}) != nil {
		t.Fatal("missing source manufactured measurements")
	}
}

func TestBaselineComparisonLoadRangeAndLegacyMetadata(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "range-current", 200, 150)
	baseline := comparisonArchiveFixture(t, server, store, "range-baseline", 100, 100)
	p := defaultBaselineComparisonPolicy()
	request := BaselineComparisonRequest{BaselineIDs: []string{baseline.ID}, Population: "transaction", MinimumLoad: 110, MaximumLoad: 190}
	a, err := server.readBaselineComparisonRun(context.Background(), current, request, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.windows) != 0 {
		t.Fatalf("load range retained unexpected windows: %+v", a.windows)
	}
	request.MinimumLoad, request.MaximumLoad = 0, 0
	a, err = server.readBaselineComparisonRun(context.Background(), current, request, p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := server.readBaselineComparisonRun(context.Background(), baseline, request, p)
	if err != nil {
		t.Fatal(err)
	}
	for key, series := range b.resources {
		series.known = false
		b.resources[key] = series
	}
	result := compareBaselineRuns(a, []*baselineComparisonRun{b}, request, p)
	for _, finding := range result.Findings {
		if finding.Category == "resource" && finding.Status == "changed" {
			t.Fatalf("legacy semantics qualified: %+v", finding)
		}
	}
	// Explicit one-to-one group maps preserve duplicate transaction names.
	p.GroupMappings = map[string]string{baseline.ID + "/checkout": "search"}
	if _, err = server.readBaselineComparisonRun(context.Background(), baseline, request, p); err == nil {
		t.Fatal("ambiguous group map accepted")
	}
}

func TestBaselineComparisonResourceBudgetIsExplicit(t *testing.T) {
	manifest := &RunArchiveManifest{}
	run := &baselineComparisonRun{manifest: manifest, resources: map[string]baselineResourceSeries{}, windows: make([]BaselineLoadWindow, 128)}
	for i := 0; i < 33; i++ {
		run.resources[fmt.Sprint(i)] = baselineResourceSeries{}
	}
	if compareBaselineRuns(run, nil, BaselineComparisonRequest{}, defaultBaselineComparisonPolicy()) != nil {
		t.Fatal("over-budget resource evidence silently omitted")
	}
}

func TestBaselineResourceMappingPreservesGeneratorSource(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "source-current", 100, 100)
	policy := defaultBaselineComparisonPolicy()
	policy.ResourceMappings = []BaselineResourceMapping{{RunID: current.ID, Keys: []string{"generator/cpu_process_pct"}, Identity: "generator-cpu", Unit: "%", Kind: "gauge", Aggregation: "mean", Confirmed: true}}
	run, err := server.readBaselineComparisonRun(context.Background(), current, BaselineComparisonRequest{Population: "transaction"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if series := run.resources["mapped/generator-cpu"]; series.source != "generator" || !series.known {
		t.Fatalf("source changed by mapping: %+v", series)
	}
	policy.ResourceMappings[0].Keys = append(policy.ResourceMappings[0].Keys, "external/java_heap")
	run, err = server.readBaselineComparisonRun(context.Background(), current, BaselineComparisonRequest{Population: "transaction"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if series := run.resources["mapped/generator-cpu"]; series.source != "mixed" || series.known {
		t.Fatalf("mixed application/generator mapping qualified: %+v", series)
	}
}

func TestBaselinePolicyRequiresExplicitMetricSemantics(t *testing.T) {
	valid := BaselineResourceMapping{RunID: "*", Identity: "heap", Keys: []string{"external/heap"}, Unit: "bytes", Kind: "gauge", Aggregation: "mean", Confirmed: true}
	for _, field := range []string{"run", "unit", "identity", "key", "overlap"} {
		t.Run(field, func(t *testing.T) {
			policy := defaultBaselineComparisonPolicy()
			mapping := valid
			mapping.Keys = append([]string{}, valid.Keys...)
			switch field {
			case "run":
				mapping.RunID = " "
			case "unit":
				mapping.Unit = " "
			case "identity":
				mapping.Identity = " "
			case "key":
				mapping.Keys[0] = " "
			}
			policy.ResourceMappings = []BaselineResourceMapping{mapping}
			if field == "overlap" {
				second := valid
				second.RunID = "run-1"
				policy.ResourceMappings = append(policy.ResourceMappings, second)
			}
			if validateBaselineComparisonPolicy(policy) == nil {
				t.Fatal("ambiguous or unconfirmed semantics accepted")
			}
		})
	}
}

func TestBaselineResourceAbsoluteThresholdUsesMappingUnits(t *testing.T) {
	const mib = 1048576.0
	window := &BaselineLoadWindow{Load: 100}
	delta, percent := baselineDelta(1.5*mib, mib)
	snapshot := BaselineComparisonSnapshot{Policy: defaultBaselineComparisonPolicy(), Baselines: []BaselineRunIdentity{{ID: "baseline"}}, Resources: []BaselineComparisonRow{{ID: "resource/heap", Name: "mapped/heap", Unit: "bytes", Window: window, Current: &BaselineMeasurement{Mean: 1.5 * mib, Samples: 20, CoveragePct: 100}, Baselines: map[string]BaselineComparisonCell{"baseline": {Measurement: &BaselineMeasurement{Mean: mib, Samples: 20, CoveragePct: 100}, Window: window, Delta: delta, DeltaPct: percent}}}}}
	snapshot.Policy.ResourceMappings = []BaselineResourceMapping{{RunID: "*", Confirmed: true, Identity: "heap", Unit: "MiB", AbsoluteThreshold: 1}}
	buildBaselineFindings(&snapshot)
	if len(snapshot.Findings) != 0 {
		t.Fatalf("half MiB change exceeded a one MiB minimum: %+v", snapshot.Findings)
	}
	snapshot.Policy.ResourceMappings[0].AbsoluteThreshold = .25
	buildBaselineFindings(&snapshot)
	if len(snapshot.Findings) != 1 || snapshot.Findings[0].Status != "changed" {
		t.Fatalf("qualified change missing: %+v", snapshot.Findings)
	}
}

func TestBaselineCandidatesSuggestionBeyondFirstPage(t *testing.T) {
	before := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	candidates := []BaselineRunIdentity{{ID: "undated-z"}, {ID: "undated-a"}}
	for i := 0; i < 60; i++ {
		completed := before.Add(time.Duration(i-2) * time.Hour)
		candidates = append(candidates, BaselineRunIdentity{ID: fmt.Sprintf("run-%02d", i), CompletedAt: &completed, ScenarioID: "scenario", Status: "COMPLETE", ArchiveAvailable: true})
	}
	sortBaselineCandidates(candidates)
	if candidates[len(candidates)-2].ID != "undated-a" || candidates[len(candidates)-1].ID != "undated-z" {
		t.Fatalf("undated sort: %+v", candidates)
	}
	suggestion := suggestedBaselineCandidate(candidates, "scenario", before)
	if suggestion == nil || suggestion.ID != "run-01" {
		t.Fatalf("suggestion=%+v", suggestion)
	}
	if suggestedBaselineCandidate(candidates[:50], "scenario", before) != nil {
		t.Fatal("fixture did not exercise pagination")
	}
	for i := range candidates {
		if candidates[i].ID == "run-01" {
			candidates[i].Status = "FAILED"
		}
	}
	suggestion = suggestedBaselineCandidate(candidates, "scenario", before)
	if suggestion == nil || suggestion.ID != "run-00" {
		t.Fatalf("failed run suggested: %+v", suggestion)
	}
	if suggestedBaselineCandidate(candidates, "other", before) != nil {
		t.Fatal("cross scenario suggestion")
	}
}

func TestBaselineCapturedMetricKind(t *testing.T) {
	for _, tc := range []struct{ expr, name, want string }{
		{`requests_total{job="app"}`, "requests_total", "counter"},
		{`rate(requests_total[5m])`, "requests_total", "gauge"},
		{`irate (requests_total[5m])`, "requests_total", "gauge"},
		{`increase(requests_total[5m])`, "requests_total", "gauge"},
		{`requests_total / 100`, "requests_total", ""},
		{`requests_total{label="rate("}`, "requests_total", "counter"},
		{`java_heap_bytes{job="app"}`, "java_heap_bytes", "gauge"},
	} {
		if got := baselineCapturedMetricKind(tc.expr, tc.name); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.expr, got, tc.want)
		}
	}
}

func TestBaselineRecordedSLAKeepsPopulationAndMappedGroup(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
	policy := defaultBaselineComparisonPolicy()
	policy.GroupMappings = map[string]string{"current/checkout": "mapped-checkout"}
	run, err := server.readBaselineComparisonRun(context.Background(), current, BaselineComparisonRequest{Population: "transaction"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	run.manifest.TransactionResponseTimeSLAResults = []TransactionResponseTimeSLAResult{{Group: "checkout", Transaction: "Purchase"}}
	key := baselineTransactionKey{"mapped-checkout", "http", "Purchase"}
	for index := range run.transactions[key] {
		m := baselineTransactionMeasurement(run, key, index)
		if len(m.SLA) != 1 {
			t.Fatal("lost original SLA after group mapping")
		}
		for _, population := range []string{"action", "unknown"} {
			run.population = population
			if len(baselineTransactionMeasurement(run, key, index).SLA) != 0 {
				t.Fatal("named SLA attached to another population")
			}
		}
		return
	}
	t.Fatal("fixture has no transaction samples")
}

func TestBaselineResourceThresholdIsPairSpecific(t *testing.T) {
	window := &BaselineLoadWindow{Load: 100}
	delta, pct := baselineDelta(150, 100)
	snapshot := BaselineComparisonSnapshot{Current: BaselineRunIdentity{ID: "current"}, Policy: defaultBaselineComparisonPolicy(), Baselines: []BaselineRunIdentity{{ID: "b1"}, {ID: "b2"}}}
	snapshot.Policy.ResourceMappings = []BaselineResourceMapping{{RunID: "b2", Confirmed: true, Identity: "heap", Unit: "bytes", AbsoluteThreshold: 100}}
	cells := map[string]BaselineComparisonCell{}
	for _, b := range snapshot.Baselines {
		cells[b.ID] = BaselineComparisonCell{Measurement: &BaselineMeasurement{Mean: 100, Samples: 20, CoveragePct: 100}, Window: window, Delta: delta, DeltaPct: pct}
	}
	snapshot.Resources = []BaselineComparisonRow{{ID: "resource/heap", Name: "mapped/heap", Unit: "bytes", Window: window, Current: &BaselineMeasurement{Mean: 150, Samples: 20, CoveragePct: 100}, Baselines: cells}}
	buildBaselineFindings(&snapshot)
	if len(snapshot.Findings) != 1 || snapshot.Findings[0].BaselineID != "b1" {
		t.Fatalf("threshold leaked across baselines: %+v", snapshot.Findings)
	}
}

func TestBaselineResourceEmptyWindowIsQualified(t *testing.T) {
	window := BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{30, 120}, Load: 100, TotalLoad: 100, Direction: "steady", Mix: map[string]float64{"group": 1}}
	series := baselineResourceSeries{identity: "heap", unit: "bytes", kind: "gauge", source: "application", known: true, points: []BaselineMetricPoint{{Seconds: 0, Value: 100}, {Seconds: 5, Value: 100}}}
	current := &baselineComparisonRun{identity: BaselineRunIdentity{ID: "current"}, windows: []BaselineLoadWindow{window}, resources: map[string]baselineResourceSeries{"heap": series}}
	baseline := &baselineComparisonRun{identity: BaselineRunIdentity{ID: "baseline"}, windows: []BaselineLoadWindow{window}, resources: map[string]baselineResourceSeries{"heap": series}}
	rows := compareBaselineResources(current, []*baselineComparisonRun{baseline}, defaultBaselineComparisonPolicy(), nil)
	if len(rows) != 1 || !strings.Contains(strings.Join(rows[0].Baselines["baseline"].Qualification, ";"), "no usable samples") {
		t.Fatalf("missing qualification: %+v", rows)
	}
}

func TestBaselineResourceMappingSemanticsAreQualified(t *testing.T) {
	w := BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{0, 100}, Group: "checkout", Load: 100, TotalLoad: 200, Direction: "steady", Mix: map[string]float64{"checkout": .5, "search": .5}}
	a := baselineResourceSeries{identity: "heap", unit: "bytes", kind: "gauge", source: "application", group: "checkout", aggregation: "sum", known: true}
	for i := 0; i <= 20; i++ {
		a.points = append(a.points, BaselineMetricPoint{Seconds: float64(i * 5), Value: 100})
	}
	for _, change := range []string{"group", "aggregation"} {
		t.Run(change, func(t *testing.T) {
			b := a
			if change == "group" {
				b.group = "search"
			} else {
				b.aggregation = "mean"
			}
			current := &baselineComparisonRun{identity: BaselineRunIdentity{ID: "current"}, windows: []BaselineLoadWindow{w}, resources: map[string]baselineResourceSeries{"heap": a}}
			baseline := &baselineComparisonRun{identity: BaselineRunIdentity{ID: "baseline"}, windows: []BaselineLoadWindow{w}, resources: map[string]baselineResourceSeries{"heap": b}}
			rows := compareBaselineResources(current, []*baselineComparisonRun{baseline}, defaultBaselineComparisonPolicy(), nil)
			if len(rows) != 1 || !strings.Contains(strings.Join(rows[0].Baselines["baseline"].Qualification, ";"), "group mapping or aggregation differs") {
				t.Fatalf("semantics mismatch accepted: %+v", rows)
			}
		})
	}
}

func TestBaselineCaptureDoesNotMatchAmbiguousExternalSources(t *testing.T) {
	for _, tc := range []struct {
		name  string
		urls  []string
		known bool
	}{
		{"direct", []string{"https://metrics.example/metrics"}, true},
		{"query", []string{"https://metrics.example/metrics?environment=prod"}, false},
		{"credential", []string{"https://user:secret@metrics.example/metrics"}, false},
		{"duplicate names", []string{"https://one.example/metrics", "https://two.example/metrics"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := &dsl.Script{}
			for _, address := range tc.urls {
				script.Config.Observability.ExternalMetrics = append(script.Config.Observability.ExternalMetrics, dsl.ExternalMetricConfig{Name: "heap", URL: address, MetricName: "java_heap_bytes"})
			}
			p := RunArchiveProvenance{}
			captureBaselineComparisonMetadata(&p, &ManagedTest{Groups: []TestGroup{{Name: "group", script: script}}})
			if (p.ComparisonMetrics["heap"].Kind != "") != tc.known {
				t.Fatalf("definition=%+v", p.ComparisonMetrics["heap"])
			}
			data, _ := json.Marshal(p.ComparisonMetrics)
			if strings.Contains(string(data), "secret") || strings.Contains(string(data), "environment") {
				t.Fatal("unsafe source metadata")
			}
		})
	}
}
