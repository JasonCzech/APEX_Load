package api

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/analysis"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAnalysisCapabilitiesExposeCatalogWithoutEnablingWork(t *testing.T) {
	s, _ := newTimeslotServer(t)
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "")
	r := timeslotRequest(t, s, "GET", "/api/v1/analysis/capabilities?project_id=proj-a", nil)
	if r.Code != 200 {
		t.Fatalf("capabilities %d: %s", r.Code, r.Body.String())
	}
	var got AnalysisCapabilitiesResponse
	if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.MetricCatalog.Version != analysis.CatalogVersion || len(got.MetricCatalog.Metrics) != len(analysis.Catalog().Metrics) {
		t.Fatalf("catalog/availability conflated: %+v", got)
	}
	for _, metric := range got.MetricCatalog.Metrics {
		for _, population := range metric.Populations {
			for _, agg := range metric.Aggregations {
				if err := analysis.ValidateMetricAggregation(metric.Name, population, agg); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestCatalogNativeWireUnitsMatchExecutedQueries(t *testing.T) {
	for _, metric := range analysis.Catalog().Metrics {
		for _, population := range metric.Populations {
			for _, aggregation := range metric.Aggregations {
				t.Run(metric.Name+"/"+population+"/"+aggregation, func(t *testing.T) {
					query := storage.AnalyticsQuery{Population: population, Metric: metric.Name, Aggregation: aggregation}
					wireUnit := nativeMetricUnit(query)
					if metric.NativeWireUnit != wireUnit {
						t.Fatalf("catalog wire unit %q differs from native query unit %q", metric.NativeWireUnit, wireUnit)
					}
					canonical, scale, err := analysis.NormalizeUnit(wireUnit)
					if err != nil || canonical != metric.Unit || scale != 1 {
						t.Fatalf("wire unit %q canonicalizes to %q at %g: %v; catalog unit %q", wireUnit, canonical, scale, err, metric.Unit)
					}
				})
			}
		}
	}
}
