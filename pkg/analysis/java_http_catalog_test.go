package analysis

import (
	"slices"
	"testing"
)

func TestJavaHTTPMetricCatalogHasOnlyExecutableRepresentationProfiles(t *testing.T) {
	c := Catalog()
	n := 0
	for _, m := range c.Metrics {
		if !slices.Contains(m.Populations, PopulationJavaHTTPAction) {
			continue
		}
		n++
		if len(m.QueryProfiles) != 1 || m.SLA || m.Correlation || len(m.Derivations) != 0 || m.Version != "java_http.diagnostics.v1" {
			t.Fatal("unqualified catalog claims", m)
		}
		var p *NativeQueryProfile
		for i := range c.QueryProfiles {
			if c.QueryProfiles[i].ID == m.QueryProfiles[0] {
				p = &c.QueryProfiles[i]
			}
		}
		if p == nil || p.Population != PopulationJavaHTTPAction || p.EvidenceMode != "" || p.MaxSeries != 1 || !slices.Equal(p.RequiredFilters, []string{"action"}) {
			t.Fatal("profile", p)
		}
		for _, agg := range m.Aggregations {
			if !slices.Contains(p.AllowedAggregations, agg) {
				t.Fatal("metric/profile mismatch", m.Name, agg)
			}
		}
	}
	if n != 17 {
		t.Fatal("measurement inventory", n)
	}
	if err := ValidateScalarAggregation(KindObservation, "ratio_of_sums"); err == nil {
		t.Fatal("paired operation admitted into scalar engine")
	}
}

func TestJavaHTTPPairedSumCannotUseSubsetCountEvaluator(t *testing.T) {
	n := RatioCount{Count: 20, Population: PopulationJavaHTTPAction, Scope: "request", MetricVersion: "java_http.diagnostics.v1", Quality: QualityValid, Complete: true}
	d := n
	d.Count = 10
	if _, err := EvaluateRatio("ratio_of_sums", n, d); err == nil {
		t.Fatal("byte pairs admitted as subset counts")
	}
	op, ok := LookupOperation("ratio_of_sums")
	if !ok || op.UnitRule != "paired_sums_ratio" || op.RawScalar {
		t.Fatal("paired sum contract", op)
	}
}
