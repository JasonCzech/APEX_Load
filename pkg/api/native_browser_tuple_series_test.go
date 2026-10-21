package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
	"strings"
	"testing"
	"time"
)

func tupleQuery() storage.AnalyticsQuery {
	return storage.AnalyticsQuery{QueryVersion: 3, BrowserTupleMode: "separate", Population: analysis.PopulationBrowserDocument, Metric: "document_lcp", Aggregation: "p75", BucketSeconds: 10, EndSeconds: 20, MaxSeries: 20}
}
func TestNativeBrowserSeparateTuplesPreservePopulations(t *testing.T) {
	record, start := nativeSeriesFixture(t)
	attachD2Documents(t, record,
		d2Document{id: 1, metric: "lcp", cohort: d2Cohort("141"), at: start.Add(2 * time.Second), value: 100, progress: 10, final: true},
		d2Document{id: 2, metric: "lcp", cohort: d2Cohort("142"), at: start.Add(3 * time.Second), value: 900, progress: 2, final: true},
		d2Document{id: 3, metric: "lcp", cohort: d2Cohort("141"), engine: "webkit", at: start.Add(4 * time.Second), value: 300, final: true},
		d2Document{id: 4, metric: "lcp", cohort: d2Cohort("141"), at: start.Add(4 * time.Second), value: 9999, progress: 1})
	q := tupleQuery()
	got, err := buildNativeArchiveSeries(context.Background(), record, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Buckets) != 0 || got.BrowserTuples == nil || got.BrowserTuples.Version != 1 || len(got.BrowserTuples.Tuples) != 3 || got.Excluded["provisional"] != 1 {
		t.Fatalf("pooled or lost observations: %+v", got)
	}
	seen := map[string]bool{}
	for _, tuple := range got.BrowserTuples.Tuples {
		if seen[tuple.ID] || len(tuple.ID) != 64 || tuple.CohortDigest == "" || tuple.Buckets[0].Count != 1 || tuple.Buckets[1].Value != nil || tuple.Buckets[1].Count != 0 {
			t.Fatalf("bad tuple: %+v", tuple)
		}
		seen[tuple.ID] = true
		want := 100.0
		if tuple.Measurement.Engine == "webkit" {
			want = 300
		} else if tuple.Measurement.Cohort.BrowserVersion == "142" {
			want = 900
		}
		if tuple.Buckets[0].Value == nil || *tuple.Buckets[0].Value < want || *tuple.Buckets[0].Value > want*1.2 {
			t.Fatalf("merged values: %+v", tuple)
		}
	}
	again, err := buildNativeArchiveSeries(context.Background(), record, q)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(again)
	if string(a) != string(b) {
		t.Fatal("nondeterministic tuple ordering")
	}
	q.MaxSeries = 2
	if _, err := buildNativeArchiveSeries(context.Background(), record, q); err == nil || !strings.Contains(err.Error(), "tuple limit exceeded") {
		t.Fatalf("silently dropped tuple: %v", err)
	}
	q = tupleQuery()
	q.BrowserTupleMode = ""
	q.MaxSeries = 1
	if _, err := buildNativeArchiveSeries(context.Background(), record, q); err == nil {
		t.Fatal("legacy query pooled tuples")
	}
}
func TestNativeBrowserSeparateTuplesBoundsAndEmpty(t *testing.T) {
	record, start := nativeSeriesFixture(t)
	q := tupleQuery()
	got, err := buildNativeArchiveSeries(context.Background(), record, q)
	if err != nil || got.BrowserTuples == nil || len(got.BrowserTuples.Tuples) != 0 || len(got.Buckets) != 0 || len(got.Warnings) == 0 {
		t.Fatalf("missing evidence fabricated: %+v %v", got, err)
	}
	var docs []d2Document
	for i := 0; i < 21; i++ {
		docs = append(docs, d2Document{id: i + 1, metric: "lcp", cohort: d2Cohort(fmt.Sprint(i)), at: start.Add(2 * time.Second), value: 100, final: true})
	}
	attachD2Documents(t, record, docs...)
	if _, err := buildNativeArchiveSeries(context.Background(), record, q); err == nil {
		t.Fatal("21 tuples accepted")
	}
	attachD2Documents(t, record, docs[:20]...)
	got, err = buildNativeArchiveSeries(context.Background(), record, q)
	if err != nil || len(got.BrowserTuples.Tuples) != 20 {
		t.Fatalf("20 tuple boundary: %+v %v", got, err)
	}
}
func TestNativeBrowserTupleComparisonsAreExactAndConservative(t *testing.T) {
	series := func(ids ...string) NativeArchiveSeries {
		s := NativeArchiveSeries{Fingerprint: "same", BrowserTuples: &NativeBrowserTupleSeries{Version: 1}}
		for _, id := range ids {
			s.BrowserTuples.Tuples = append(s.BrowserTuples.Tuples, NativeBrowserTuple{ID: id})
		}
		return s
	}
	current, baseline := series("a", "b"), series("b", "c")
	got := compareNativeBrowserTuples(current, baseline)
	if len(got) != 3 || got[0].Reason != "missing_baseline_tuple" || got[1].Status != "comparable" || got[2].Reason != "missing_current_tuple" {
		t.Fatalf("joined wrong tuples: %+v", got)
	}
	baseline.Fingerprint = "different"
	if compareNativeBrowserTuples(current, baseline)[1].Reason != "workload_version_mismatch" {
		t.Fatal("workload mismatch compared")
	}
	baseline.Fingerprint = "same"
	baseline.Excluded = map[string]int64{"censored": 1}
	if compareNativeBrowserTuples(current, baseline)[1].Reason != "incomplete_evidence" {
		t.Fatal("excluded measurements compared")
	}
	baseline.Excluded = nil
	baseline.Warnings = []string{"source loss"}
	if compareNativeBrowserTuples(current, baseline)[1].Reason != "incomplete_evidence" {
		t.Fatal("loss compared")
	}
}

func TestNativeBrowserTupleIdentityClonesRuntimeCohort(t *testing.T) {
	cohort := d2Cohort("141")
	identity := nativeBrowserTupleIdentity(protocol.BrowserDocumentMetricSummary{Metric: "lcp", Unit: "ms", Engine: "chromium", Cohort: cohort})
	identity.Cohort.BrowserVersion = "changed"
	if cohort.BrowserVersion != "141" {
		t.Fatal("tuple identity aliases source cohort")
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "eligible_final") || strings.Contains(string(raw), "distribution") {
		t.Fatal("identity invents summary counts")
	}
}
