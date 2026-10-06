package api

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
)

func TestAnalysisSummaryCapabilitiesAndSavedMappingsRequireStatistics(t *testing.T) {
	s, _ := newTimeslotServer(t)
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	r := timeslotRequest(t, s, "GET", "/api/v1/analysis/capabilities?project_id=proj-a", nil)
	var capabilities AnalysisCapabilitiesResponse
	if err := json.Unmarshal(r.Body.Bytes(), &capabilities); err != nil || !slices.Equal(capabilities.SummaryStatistics, analysis.SummaryStatistics) {
		t.Fatalf("capabilities %s %v", r.Body.String(), err)
	}
	path := "/api/v1/analysis/mappings?project_id=proj-a"
	m := analysis.ParquetMapping{Version: 1, Timestamp: []string{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: []string{"p95"}, Name: "latency", Kind: analysis.KindSummary, Unit: "ms"}}}
	if r = timeslotRequest(t, s, "POST", path, map[string]any{"name": "undeclared", "definition": m}); r.Code != 400 {
		t.Fatalf("undeclared saved summary: %d %s", r.Code, r.Body.String())
	}
	m.Measures[0].Statistic, m.Measures[0].WindowSeconds = "p95", 60
	if r = timeslotRequest(t, s, "POST", path, map[string]any{"name": "declared", "definition": m}); r.Code != 200 {
		t.Fatalf("declared saved summary: %d %s", r.Code, r.Body.String())
	}
}

func TestAnalysisComparisonAdmitsSummaryOnlyStatisticsForDatasets(t *testing.T) {
	base := AnalysisComparisonRequest{From: time.Unix(1700000000, 0), To: time.Unix(1700000600, 0), BucketSeconds: 60, Mode: "overlay"}
	dataset := base
	dataset.Left = AnalysisSeriesSelection{DatasetID: "d1", Metric: "latency", Aggregation: "p90"}
	dataset.Right = AnalysisSeriesSelection{DatasetID: "d2", Metric: "latency", Aggregation: "p90"}
	if err := dataset.validate(); err != nil {
		t.Fatalf("dataset p90: %v", err)
	}
	run := dataset
	run.Right = AnalysisSeriesSelection{RunID: "run-1", Metric: "response_time", Aggregation: "p90"}
	if run.validate() == nil {
		t.Fatal("a run series accepted a summary-only statistic")
	}
}

func TestImportedSummaryQueriesExplainUnanswerableRequests(t *testing.T) {
	declared := func(statistic string) analysis.ParquetMeasure {
		return analysis.ParquetMeasure{Value: []string{statistic}, Name: "latency", Kind: analysis.KindSummary, Unit: "ms", Statistic: statistic, WindowSeconds: 60}
	}
	mapping := analysis.ParquetMapping{Version: 1, Measures: []analysis.ParquetMeasure{declared("p50"), declared("p95"), {Value: []string{"cpu"}, Name: "cpu", Kind: analysis.KindGauge, Unit: "percent"}}}
	query := func(metric, aggregation string, bucket int) analytics.SeriesQuery {
		return analytics.SeriesQuery{Metric: metric, Aggregation: aggregation, BucketSeconds: bucket}
	}
	if err := validateImportedSummaryQuery(mapping, query("latency", "p95", 120)); err != nil {
		t.Fatal(err)
	}
	if err := validateImportedSummaryQuery(mapping, query("cpu", "avg", 7)); err != nil {
		t.Fatalf("raw metric constrained: %v", err)
	}
	if err := validateImportedSummaryQuery(mapping, query("latency", "avg", 60)); err == nil || !strings.Contains(err.Error(), "p50, p95") {
		t.Fatalf("undeclared statistic: %v", err)
	}
	if err := validateImportedSummaryQuery(mapping, query("latency", "p95", 45)); err == nil {
		t.Fatal("partial window bucket accepted")
	}
	legacy := analysis.ParquetMapping{Version: 1, Measures: []analysis.ParquetMeasure{{Value: []string{"p95"}, Name: "latency", Kind: analysis.KindSummary, Unit: "ms"}}}
	if err := validateImportedSummaryQuery(legacy, query("latency", "p95", 60)); !errors.Is(err, analysis.ErrSummaryStatisticUnknown) {
		t.Fatalf("legacy summary: %v", err)
	}
}
