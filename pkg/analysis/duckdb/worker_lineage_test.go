//go:build analytics_duckdb && cgo

package duckdb

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestWorkerSeriesLineageRefusesInconsistentChildren(t *testing.T) {
	q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "latency", From: time.Unix(1700000040, 0).UTC(), To: time.Unix(1700000340, 0).UTC(), BucketSeconds: 300, Aggregation: "p95"}
	summary := func(statistic string, window int) analysis.MetricPoint {
		return analysis.MetricPoint{InputKind: analysis.KindSummary, Derivation: []analysis.DerivationStep{{Operation: statistic, Algorithm: "source_summary.v1", BucketSeconds: window}, {Operation: statistic, Algorithm: analysis.SummaryRebucketAlgorithm(statistic), BucketSeconds: 300}}}
	}
	if lineage, err := workerSeriesLineage(summary("p95", 60), q); err != nil || len(lineage) != 2 {
		t.Fatalf("consistent summary lineage refused: %v", err)
	}
	for name, point := range map[string]analysis.MetricPoint{
		"statistic other than the aggregation": summary("p50", 60),
		"window that does not divide the bucket": summary("p95", 70),
		"lineage without a source window":       {InputKind: analysis.KindSummary, Derivation: []analysis.DerivationStep{{Operation: "p95", Algorithm: "native_bucket.v1", BucketSeconds: 300}}},
		"missing reason on a raw point":         {InputKind: analysis.KindObservation, MissingReason: analysis.MissingPercentileNotMergeable},
		"histogram lineage without its window":  {InputKind: analysis.KindHistogram},
	} {
		if _, err := workerSeriesLineage(point, q); err == nil {
			t.Errorf("%s: admitted", name)
		}
	}
}
