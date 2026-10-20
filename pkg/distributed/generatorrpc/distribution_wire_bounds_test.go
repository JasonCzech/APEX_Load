package generatorrpc

import (
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"testing"
)

func TestDistributionWireCopiesKeepExistingBounds(t *testing.T) {
	for _, n := range []int{1, 1024, 1025, 4096} {
		wire := &pb.TDigestSnapshot{Count: 1, Centroids: make([]*pb.TDigestCentroid, n)}
		for i := range wire.Centroids {
			wire.Centroids[i] = &pb.TDigestCentroid{Mean: 1, Weight: 1}
		}
		got := tdigestSnapshotFromProto(wire)
		if n > 1024 {
			if got != nil {
				t.Fatalf("decoded oversized digest: %d", n)
			}
		} else {
			if got == nil || len(got.Centroids) != n {
				t.Fatalf("decoded boundary lost: %d", n)
			}
			got.Centroids[0].Mean = 2
			if wire.Centroids[0].Mean != 1 {
				t.Fatal("decoded digest aliases wire")
			}
		}
		local := &engine.TDigestSnapshot{Count: 1, Centroids: make([]engine.TDigestCentroidSnapshot, n)}
		out := tdigestSnapshotToProto(local)
		if n > 1024 {
			if out != nil {
				t.Fatalf("encoded oversized digest: %d", n)
			}
		} else if out == nil || len(out.Centroids) != n {
			t.Fatal("encoded digest boundary lost")
		}
		if len(local.Centroids) != n || len(wire.Centroids) != n {
			t.Fatal("source mutated")
		}
	}
	for _, n := range []int{1, engine.DurationSketchMaxBuckets, engine.DurationSketchMaxBuckets + 1} {
		source := &engine.DurationSketchSnapshot{Count: 1, Bins: make([]engine.DurationSketchBin, n)}
		out := durationSketchSnapshotToProto(source)
		if n > engine.DurationSketchMaxBuckets {
			if out != nil {
				t.Fatal("encoded oversized sketch")
			}
		} else if out == nil || len(out.Bins) != n {
			t.Fatal("encoded sketch boundary lost")
		}
		if len(source.Bins) != n {
			t.Fatal("source sketch mutated")
		}
	}
}

func TestOversizedDistributionRefusalPreservesCountsAndCompatibleFallback(t *testing.T) {
	oversized := &pb.TDigestSnapshot{Count: 2, Centroids: make([]*pb.TDigestCentroid, 1025)}
	for i := range oversized.Centroids {
		oversized.Centroids[i] = &pb.TDigestCentroid{Mean: 1, Weight: 1}
	}
	wire := &pb.MetricAggregate{Name: "sample", Count: 2, Errors: 1, AvgMs: 15, P50Ms: 15, P99Ms: 20, DurationDigest: oversized}
	decoded := metricAggregateFromProto(wire)
	if decoded == nil || decoded.DurationDigest != nil || decoded.Count != 2 || decoded.Errors != 1 {
		t.Fatal("refusal lost count or retained invalid distribution")
	}
	merged := mergeMetricAggregates([]MetricAggregate{*decoded})
	if merged == nil || merged.Count != 2 || merged.AvgMs != 15 || merged.P99Ms != 20 || merged.DurationDigest != nil || merged.DurationSketch != nil {
		t.Fatalf("legacy fallback fabricated distribution: %+v", merged)
	}
	sketch := engine.NewDurationSketch()
	sketch.Add(10_000_000)
	sketch.Add(20_000_000)
	snapshot := sketch.Snapshot()
	wire.DurationSketch = durationSketchSnapshotToProto(&snapshot)
	decoded = metricAggregateFromProto(wire)
	merged = mergeMetricAggregates([]MetricAggregate{*decoded})
	if merged == nil || merged.Count != 2 || merged.Errors != 1 || merged.AvgMs != 15 || merged.DurationSketch == nil || merged.P95Ms < 19.4 || merged.P95Ms > 19.6 {
		t.Fatalf("valid sketch fallback lost: %+v", merged)
	}
}
