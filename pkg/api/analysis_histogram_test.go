package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
)

func TestImportedHistogramQueriesExplainUnanswerableRequests(t *testing.T) {
	s, _ := newTimeslotServer(t)
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	r := timeslotRequest(t, s, "GET", "/api/v1/analysis/capabilities?project_id=proj-a", nil)
	var capabilities AnalysisCapabilitiesResponse
	if err := json.Unmarshal(r.Body.Bytes(), &capabilities); err != nil || capabilities.HistogramBoundLimit != analysis.MaxHistogramBounds {
		t.Fatalf("capabilities %s %v", r.Body.String(), err)
	}
	mapping := analysis.ParquetMapping{Version: 1, Measures: []analysis.ParquetMeasure{{
		Value: []string{"inf"}, Name: "latency", Kind: analysis.KindHistogram, Unit: "ms",
		Histogram: &analysis.ParquetHistogram{Bounds: []string{"10", "+Inf"}, BucketCounts: analysis.HistogramPerBin, Temporality: analysis.HistogramDelta, WindowSeconds: 60, CountColumns: []analysis.ParquetColumnPath{{"b10"}}},
	}}}
	query := func(aggregation string, bucket int) analytics.SeriesQuery {
		return analytics.SeriesQuery{Metric: "latency", Aggregation: aggregation, BucketSeconds: bucket}
	}
	for _, ok := range []analytics.SeriesQuery{query("count", 60), query("p999", 300)} {
		if err := validateImportedMeasureQuery(mapping, ok); err != nil {
			t.Fatalf("%+v: %v", ok, err)
		}
	}
	if err := validateImportedMeasureQuery(mapping, query("avg", 60)); err == nil || !strings.Contains(err.Error(), "interpolated percentile") {
		t.Fatalf("average: %v", err)
	}
	if err := validateImportedMeasureQuery(mapping, query("p95", 90)); err == nil || !strings.Contains(err.Error(), "60-second source window") {
		t.Fatalf("straddling bucket: %v", err)
	}
}
