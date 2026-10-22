package storage

import (
	"github.com/jasonczech/apexload/pkg/analysis"
	"testing"
)

func TestBrowserTupleQueryVersionAndBounds(t *testing.T) {
	valid := AnalyticsQuery{QueryVersion: 3, BrowserTupleMode: "separate", Population: analysis.PopulationBrowserDocument, Metric: "document_lcp", Aggregation: "p75", BucketSeconds: 1, EndSeconds: 1000, MaxSeries: 20}
	if err := ValidateNativeArchiveQuery(valid); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*AnalyticsQuery){
		"legacy": func(q *AnalyticsQuery) { q.QueryVersion = 2 }, "future": func(q *AnalyticsQuery) { q.QueryVersion = 5 }, "unknown mode": func(q *AnalyticsQuery) { q.BrowserTupleMode = "pool" }, "live": func(q *AnalyticsQuery) { q.EvidenceMode = "live" }, "other population": func(q *AnalyticsQuery) { q.Population = "action" }, "21 tuples": func(q *AnalyticsQuery) { q.MaxSeries = 21 }, "negative": func(q *AnalyticsQuery) { q.MaxSeries = -1 }, "1001 buckets": func(q *AnalyticsQuery) { q.EndSeconds = 1001 }, "group": func(q *AnalyticsQuery) { q.Dimensions = []string{"engine"} }, "derived": func(q *AnalyticsQuery) { q.Derivation = &analysis.QueryDerivation{Operation: "moving_mean"} }, "joint": func(q *AnalyticsQuery) { q.JointEvidence = true },
	} {
		t.Run(name, func(t *testing.T) {
			q := valid
			mutate(&q)
			if err := ValidateNativeArchiveQuery(q); err == nil {
				t.Fatal("invalid tuple query accepted")
			}
		})
	}
	for version := 0; version < 3; version++ {
		q := valid
		q.QueryVersion = version
		q.BrowserTupleMode = ""
		q.MaxSeries = 1
		if err := ValidateNativeArchiveQuery(q); err != nil {
			t.Fatalf("legacy rejected: %v", err)
		}
		q.MaxSeries = 2
		if err := ValidateNativeArchiveQuery(q); err == nil {
			t.Fatal("legacy multi-tuple accepted")
		}
	}
}

func TestBrowserTupleQuerySavedDefaultsAndReload(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/panels.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	definition := sampleDashboardDefinition("browser-tuples")
	definition.Tabs[0].Panels[0].Query = &AnalyticsQuery{QueryVersion: 3, BrowserTupleMode: "separate", Population: analysis.PopulationBrowserDocument, Metric: "document_lcp", Aggregation: "p75", BucketSeconds: 1, EndSeconds: 1000}
	if err := store.SaveDashboardDefinition(definition); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetDashboardDefinition(definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	q := loaded.Tabs[0].Panels[0].Query
	if q.BrowserTupleMode != "separate" || q.QueryVersion != 3 || q.MaxSeries != 20 {
		t.Fatalf("saved query lost version or changed default: %+v", q)
	}
	if err := ValidateNativeArchiveQuery(*q); err != nil {
		t.Fatal(err)
	}
}
