package api

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

func baselinePrometheusPoint(run string, start time.Time, seconds int, value float64) analysis.MetricPoint {
	return analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: storage.DefaultProjectID, RunID: run, Source: "target-source", MetricName: "cpu", Kind: analysis.KindGauge, TimestampUTC: start.Add(time.Duration(seconds) * time.Second), Unit: "percent", Quality: analysis.QualityValid, Value: &value, Dimensions: map[string]string{"job": "checkout"}, Provider: &analysis.ProviderMeasurement{Name: "prometheus", Mode: "evaluation_point", BackendProfile: "prometheus", QueryID: "cpu", MetricID: "cpu", DefinitionHash: strings.Repeat("a", 64), EntityIDs: map[string]string{"series": strings.Repeat("b", 64)}, Statistic: "gauge", EvaluationStepSeconds: 5, Settled: true, AggregationEligible: true}}
}

func writeBaselinePrometheusFixture(t *testing.T, server *Server, run string, points []analysis.MetricPoint) {
	t.Helper()
	record := server.runner.archives[run]
	body, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest RunArchiveManifest
	if err = json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.MonitorEvidenceFile = "monitor-points.jsonl"
	manifest.MonitorEvidenceCount = int64(len(points))
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	for _, point := range points {
		if err = encoder.Encode(point); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(record.ArchivePath, manifest.MonitorEvidenceFile), output.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	body, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(record.ManifestPath, body, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestBaselinePrometheusCanonicalArchiveComparison(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
	baseline := comparisonArchiveFixture(t, server, store, "baseline", 100, 100)
	for _, test := range []*ManagedTest{current, baseline} {
		points := []analysis.MetricPoint{}
		for seconds := 0; seconds <= 120; seconds += 5 {
			value := 0.0
			if test.ID == current.ID {
				value = 20
			}
			point := baselinePrometheusPoint(test.ID, *test.StartedAt, seconds, value)
			// A different source name can match only the same full query identity.
			if test.ID == baseline.ID {
				point.Source = "previous-source"
			}
			points = append(points, point)
		}
		writeBaselinePrometheusFixture(t, server, test.ID, points)
	}
	request := BaselineComparisonRequest{BaselineIDs: []string{baseline.ID}, Population: "transaction"}
	policy := defaultBaselineComparisonPolicy()
	a, err := server.readBaselineComparisonRun(context.Background(), current, request, policy)
	if err != nil {
		t.Fatal(err)
	}
	b, err := server.readBaselineComparisonRun(context.Background(), baseline, request, policy)
	if err != nil {
		t.Fatal(err)
	}
	result := compareBaselineRuns(a, []*baselineComparisonRun{b}, request, policy)
	var found *BaselineComparisonRow
	for i := range result.Resources {
		if result.Resources[i].Provider != nil {
			found = &result.Resources[i]
		}
	}
	if found == nil {
		t.Fatal("canonical Prometheus evidence absent from native Resources comparison")
	}
	cell := found.Baselines[baseline.ID]
	if found.Unit != "percent" || found.Current == nil || found.Current.Mean != 20 || found.Current.Samples != 18 || found.Current.CoveragePct != 100 || cell.Measurement == nil || cell.Measurement.Mean != 0 || cell.Delta == nil || *cell.Delta != 20 || cell.DeltaPct != nil || len(cell.Qualification) != 0 {
		t.Fatalf("comparison=%+v current=%+v cell=%+v", found, found.Current, cell)
	}
	if found.Provider.QueryID != "cpu" || found.Provider.EvaluationStepSeconds != 5 || !maps.Equal(found.Provider.Dimensions, map[string]string{"job": "checkout"}) || found.Current.MeanStatistic != "evaluation_grid_mean" || slices.Contains(found.Current.AvailableStatistics, "p95") {
		t.Fatalf("provenance/statistics lost: %+v", found)
	}
	// Existing generator and legacy external resource definitions remain available.
	if _, ok := a.resources["external/java_heap"]; !ok {
		t.Fatal("legacy resource regression")
	}
	key := found.Name
	delete(b.resources, key)
	for _, row := range compareBaselineResources(a, []*baselineComparisonRun{b}, policy, nil) {
		if row.Provider == nil {
			continue
		}
		qualification := row.Baselines[baseline.ID].Qualification
		if !slices.Contains(qualification, "Prometheus series missing or its saved query identity differs") || strings.Contains(strings.Join(qualification, " "), "mapping") || strings.Contains(strings.Join(qualification, " "), "map changed") {
			t.Fatalf("missing series suggested an unsupported remapping: %v", qualification)
		}
	}
	mappedPolicy := policy
	mappedPolicy.ResourceMappings = []BaselineResourceMapping{{RunID: "*", Keys: []string{key}, Identity: "rewritten", Unit: "count", Kind: "counter", Aggregation: "sum", Confirmed: true}}
	reloaded, err := server.readBaselineComparisonRun(context.Background(), current, request, mappedPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.resources[key].prometheus == nil || len(reloaded.warnings) == 0 {
		t.Fatal("mapping erased Prometheus identity")
	}
	if _, exists := reloaded.resources["rewritten"]; exists {
		t.Fatal("mapping reconstructed a rate or total")
	}
}

func TestBaselinePrometheusGridCoverage(t *testing.T) {
	series := baselineResourceSeries{prometheus: &BaselinePrometheusIdentity{EvaluationStepSeconds: 15}, instances: 1}
	for seconds := 0; seconds < 300; seconds += 15 {
		if seconds == 30 {
			continue
		} // Absent grid cell, never filled from its neighbor.
		value := 0.0
		if seconds == 60 {
			value = math.NaN()
		} // Stale/unsettled/missing evaluations are ineligible.
		series.points = append(series.points, BaselineMetricPoint{Seconds: float64(seconds), Value: value})
	}
	m := baselineResourceMeasurement(series, BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{StartSeconds: 0, EndSeconds: 300}, Load: 100})
	if m == nil || m.Samples != 18 || m.CoveragePct != 90 || m.Mean != 0 || m.Min != 0 || m.Max != 0 {
		t.Fatalf("missing/zero semantics: %+v", m)
	}
	if len(m.Points) != 18 || slices.Contains(m.AvailableStatistics, "p95") || slices.Contains(m.AvailableStatistics, "throughput") {
		t.Fatalf("invented statistics: %+v", m)
	}
}

func TestBaselinePrometheusIdentityCompatibility(t *testing.T) {
	point := baselinePrometheusPoint("run", time.Now().UTC(), 0, 1)
	a := baselinePrometheusIdentity(point)
	for _, test := range []struct {
		name   string
		mutate func(*BaselinePrometheusIdentity)
	}{
		{"query", func(p *BaselinePrometheusIdentity) { p.QueryID = "other" }},
		{"definition", func(p *BaselinePrometheusIdentity) { p.DefinitionHash = strings.Repeat("c", 64) }},
		{"backend", func(p *BaselinePrometheusIdentity) { p.BackendProfile = "azure_managed" }},
		{"statistic", func(p *BaselinePrometheusIdentity) { p.Statistic = "rate" }},
		{"step", func(p *BaselinePrometheusIdentity) { p.EvaluationStepSeconds = 15 }},
		{"labels", func(p *BaselinePrometheusIdentity) { p.Dimensions["instance"] = "second" }},
		{"entities", func(p *BaselinePrometheusIdentity) { p.EntityIDs["series"] = strings.Repeat("d", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := baselinePrometheusIdentity(point)
			test.mutate(b)
			if compatibleBaselinePrometheusIdentity(a, b) {
				t.Fatal("incompatible identity matched")
			}
			w := BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{0, 300}, Direction: "steady", Load: 100}
			current := &baselineComparisonRun{resources: map[string]baselineResourceSeries{"same": {prometheus: a, known: true, unit: "percent", kind: "gauge", source: "prometheus", points: []BaselineMetricPoint{{Seconds: 0, Value: 1}}}}, windows: []BaselineLoadWindow{w}}
			baseline := &baselineComparisonRun{identity: BaselineRunIdentity{ID: "baseline"}, resources: map[string]baselineResourceSeries{"same": {prometheus: b, known: true, unit: "percent", kind: "gauge", source: "prometheus", points: []BaselineMetricPoint{{Seconds: 0, Value: 2}}}}, windows: []BaselineLoadWindow{w}}
			rows := compareBaselineResources(current, []*baselineComparisonRun{baseline}, defaultBaselineComparisonPolicy(), nil)
			if len(rows) != 1 || rows[0].Baselines["baseline"].Delta != nil {
				t.Fatalf("incompatible delta: %+v", rows)
			}
		})
	}
	b := baselinePrometheusIdentity(point)
	b.DisplayLabels = map[string]string{"Job": "Checkout"}
	if !compatibleBaselinePrometheusIdentity(a, b) || compatibleBaselinePrometheusIdentity(a, nil) {
		t.Fatal("display-only spelling changed canonical compatibility")
	}
}

func TestBaselinePrometheusArchiveEligibilityAndScope(t *testing.T) {
	for _, test := range []struct {
		name      string
		mutate    func(*analysis.MetricPoint)
		wantError bool
		eligible  bool
	}{
		{"zero", func(*analysis.MetricPoint) {}, false, true},
		{"stale", func(p *analysis.MetricPoint) { p.Quality = analysis.QualityStale }, false, false},
		{"unsettled", func(p *analysis.MetricPoint) { p.Provider.Settled = false }, false, false},
		{"ineligible", func(p *analysis.MetricPoint) { p.Provider.AggregationEligible = false }, false, false},
		{"missing", func(p *analysis.MetricPoint) { p.Quality = analysis.QualityMissing; p.Value = nil }, false, false},
		{"counter", func(p *analysis.MetricPoint) { p.Provider.Statistic = "counter"; p.Kind = analysis.KindCounter }, false, false},
		{"terminal", func(p *analysis.MetricPoint) {
			p.Provider.Mode = "query_window"
			p.Provider.QueryWindow = true
			p.Provider.BucketStart = p.TimestampUTC.Add(-time.Minute)
			p.Provider.BucketEnd = p.TimestampUTC
		}, false, false},
		{"project", func(p *analysis.MetricPoint) { p.ProjectID = "different" }, true, false},
		{"run", func(p *analysis.MetricPoint) { p.RunID = "different" }, true, false},
		{"offgrid", func(p *analysis.MetricPoint) { p.TimestampUTC = p.TimestampUTC.Add(time.Second) }, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, store := baselineTestServer(t)
			current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
			point := baselinePrometheusPoint(current.ID, *current.StartedAt, 30, 0)
			test.mutate(&point)
			writeBaselinePrometheusFixture(t, server, current.ID, []analysis.MetricPoint{point})
			r, err := server.readBaselineComparisonRun(context.Background(), current, BaselineComparisonRequest{}, defaultBaselineComparisonPolicy())
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v", err)
			}
			if err != nil {
				return
			}
			usable := false
			for _, s := range r.resources {
				if s.prometheus != nil {
					usable = baselineResourceMeasurement(s, BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{30, 120}}) != nil
				}
			}
			if usable != test.eligible {
				t.Fatalf("eligible=%v resources=%+v", usable, r.resources)
			}
		})
	}
	t.Run("duplicate canonical point", func(t *testing.T) {
		server, store := baselineTestServer(t)
		current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
		point := baselinePrometheusPoint(current.ID, *current.StartedAt, 30, 1)
		writeBaselinePrometheusFixture(t, server, current.ID, []analysis.MetricPoint{point, point})
		if _, err := server.readBaselineComparisonRun(context.Background(), current, BaselineComparisonRequest{}, defaultBaselineComparisonPolicy()); err == nil {
			t.Fatal("duplicate canonical observations accepted")
		}
	})
}

func TestBaselinePrometheusReportPreservesGridSemantics(t *testing.T) {
	measurement := &BaselineMeasurement{MeanStatistic: "evaluation_grid_mean", AvailableStatistics: []string{"mean", "min", "max", "samples", "coverage_pct"}, Mean: 0, Min: 0, Max: 0, Samples: 20, CoveragePct: 100}
	snapshot := &BaselineComparisonSnapshot{Baselines: []BaselineRunIdentity{{ID: "baseline"}}, Resources: []BaselineComparisonRow{{Name: "cpu", Provider: &BaselinePrometheusIdentity{QueryID: "cpu", BackendProfile: "prometheus", Statistic: "gauge", EvaluationStepSeconds: 15, Dimensions: map[string]string{"job": "checkout"}}, Current: measurement, Baselines: map[string]BaselineComparisonCell{"baseline": {Measurement: measurement}}}}}
	data := &runArchiveHTMLReportData{Report: &RunArchiveReport{BaselineComparison: snapshot}}
	var output bytes.Buffer
	if err := baselineComparisonTemplate.Execute(&output, data); err != nil {
		t.Fatal(err)
	}
	resources := strings.Split(output.String(), "Resources — recorded means")[1]
	if !strings.Contains(resources, "evaluation-grid mean 0") || !strings.Contains(resources, "15s grid") || strings.Contains(resources, "P95") {
		t.Fatalf("misleading resource report: %s", resources)
	}
}
