package protocol

import (
	"math"
	"testing"
)

func comparisonTestTuple(values ...float64) BrowserDocumentMetricSummary {
	m := BrowserDocumentMetricSummary{Metric: "lcp", Algorithm: BrowserVitalsAlgorithm, AlgorithmSHA256: BrowserVitalsAlgorithmSHA256, Unit: "ms", Engine: "chromium", DeviceProfile: "desktop", Cohort: browserTestCohort(), Group: "checkout", FrameScope: "main_frame", LifecyclePhase: "navigation"}
	for _, v := range values {
		m.Distribution.Add(v)
		m.EligibleFinal++
	}
	return m
}

func comparisonTestSummary(metrics ...BrowserDocumentMetricSummary) *BrowserDocumentSummary {
	return &BrowserDocumentSummary{Version: BrowserEvidenceVersion, Metrics: metrics}
}

func TestBrowserMetricDistributionQuantileIsTheBucketUpperBound(t *testing.T) {
	var d BrowserMetricDistribution
	if _, ok := d.Quantile(0.75); ok {
		t.Fatal("empty distribution has a quantile")
	}
	for _, v := range []float64{100, 200, 300, 400} {
		d.Add(v)
	}
	p75, ok := d.Quantile(0.75)
	// The third value (300 ms) lies in a bucket no wider than 20%.
	if !ok || p75 < 300 || p75 > 360 {
		t.Fatalf("p75 = %g, %v", p75, ok)
	}
	if p100, _ := d.Quantile(1); p100 != 400 {
		t.Fatalf("p100 not capped by the observed maximum: %g", p100)
	}
	if p0, _ := d.Quantile(0); p0 < 100 || p0 > 120 {
		t.Fatalf("p0 = %g", p0)
	}
	for _, q := range []float64{-0.1, 1.1, math.NaN()} {
		if _, ok := d.Quantile(q); ok {
			t.Fatalf("invalid quantile %g answered", q)
		}
	}
	var zero BrowserMetricDistribution
	zero.Add(0)
	if v, ok := zero.Quantile(0.75); !ok || v != 0 {
		t.Fatalf("observed zero CLS reported %g", v)
	}
}

func TestCompareBrowserDocumentSummariesComparesOnlyMatchingTuples(t *testing.T) {
	current, baseline := comparisonTestTuple(100, 200, 300, 400), comparisonTestTuple(100, 150, 200, 250)
	rows := CompareBrowserDocumentSummaries(comparisonTestSummary(current), comparisonTestSummary(baseline))
	if len(rows) != 1 || !rows[0].Comparable || rows[0].Reason != "" || rows[0].DeltaP75 == nil || *rows[0].DeltaP75 <= 0 || rows[0].CurrentFinal != 4 || rows[0].BaselineFinal != 4 {
		t.Fatalf("matching tuples not compared: %+v", rows)
	}
	if *rows[0].CurrentP75-*rows[0].BaselineP75 != *rows[0].DeltaP75 {
		t.Fatal("delta is not current minus baseline")
	}
}

func TestCompareBrowserDocumentSummariesRefusesMismatchedMeasurements(t *testing.T) {
	for name, change := range map[string]func(*BrowserDocumentMetricSummary){
		"algorithm_mismatch":       func(m *BrowserDocumentMetricSummary) { m.AlgorithmSHA256 = "different" },
		"lifecycle_phase_mismatch": func(m *BrowserDocumentMetricSummary) { m.LifecyclePhase = "bfcache_restore" },
		"frame_scope_mismatch":     func(m *BrowserDocumentMetricSummary) { m.FrameScope = "child_frame" },
		"engine_mismatch":          func(m *BrowserDocumentMetricSummary) { m.Engine = "webkit" },
		"device_profile_mismatch":  func(m *BrowserDocumentMetricSummary) { m.DeviceProfile = "mobile" },
		"cohort_mismatch":          func(m *BrowserDocumentMetricSummary) { m.Cohort.BrowserVersion = "999.0" },
	} {
		t.Run(name, func(t *testing.T) {
			current, baseline := comparisonTestTuple(100, 200), comparisonTestTuple(300, 400)
			change(&baseline)
			rows := CompareBrowserDocumentSummaries(comparisonTestSummary(current), comparisonTestSummary(baseline))
			if len(rows) != 1 {
				t.Fatalf("a refused pair was reported twice: %+v", rows)
			}
			row := rows[0]
			if row.Comparable || row.Reason != name || row.CurrentP75 != nil || row.BaselineP75 != nil || row.DeltaP75 != nil {
				t.Fatalf("mismatched measurement compared: %+v", row)
			}
			if row.CurrentFinal != 2 || row.BaselineFinal != 2 {
				t.Fatalf("refused pair lost its populations: %+v", row)
			}
			if BrowserDocumentTupleMismatch(current, baseline) != name {
				t.Fatalf("mismatch reason %q", BrowserDocumentTupleMismatch(current, baseline))
			}
		})
	}
}

func TestCompareBrowserDocumentSummariesReportsUnmatchedAndUnqualifiedTuples(t *testing.T) {
	current := comparisonTestTuple(100)
	other := comparisonTestTuple(100)
	other.Metric, other.Unit = "cls", "score"
	other.Distribution = BrowserMetricDistribution{}
	other.Distribution.Add(0.1)
	empty := comparisonTestTuple()
	empty.Group = "browse"
	emptyBaseline := empty
	rows := CompareBrowserDocumentSummaries(comparisonTestSummary(current, empty), comparisonTestSummary(other, emptyBaseline))
	reasons := map[string]string{}
	for _, row := range rows {
		reasons[row.Metric+"/"+row.Group] = row.Reason
		if row.Comparable {
			t.Fatalf("unpaired or empty tuple compared: %+v", row)
		}
	}
	want := map[string]string{"lcp/checkout": "baseline_unmatched", "cls/checkout": "current_unmatched", "lcp/browse": "insufficient_final_documents"}
	if len(reasons) != len(want) {
		t.Fatalf("rows %v", reasons)
	}
	for key, reason := range want {
		if reasons[key] != reason {
			t.Fatalf("%s: %q, want %q (%v)", key, reasons[key], reason, reasons)
		}
	}
	if CompareBrowserDocumentSummaries(nil, nil) != nil {
		t.Fatal("no summaries compared")
	}
	if rows := CompareBrowserDocumentSummaries(nil, comparisonTestSummary(current)); len(rows) != 1 || rows[0].Reason != "current_unmatched" {
		t.Fatalf("baseline-only tuple: %+v", rows)
	}
}

func TestBrowserRuntimeCohortDigestSeparatesActualCohorts(t *testing.T) {
	a, b := browserTestCohort(), browserTestCohort()
	if BrowserRuntimeCohortDigest(a) != BrowserRuntimeCohortDigest(b) || len(BrowserRuntimeCohortDigest(a)) != 16 {
		t.Fatal("same cohort digests differ")
	}
	b.ViewportWidth++
	if BrowserRuntimeCohortDigest(a) == BrowserRuntimeCohortDigest(b) {
		t.Fatal("different cohorts share a digest")
	}
	if BrowserRuntimeCohortDigest(nil) != "unavailable" {
		t.Fatal("missing cohort has a digest")
	}
}
