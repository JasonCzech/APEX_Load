package generatorrpc

import (
	"encoding/json"
	"math"
	"math/rand"
	"reflect"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestMetricAggregateSketchCrossesTheWire(t *testing.T) {
	generator := newMetricAccumulator("Checkout")
	for _, ms := range []float64{0, 0.4, 12, 120, 30000} {
		generator.add(TransactionMetric{Name: "Checkout", DurationMs: ms, Success: true})
	}
	sent := generator.finalize(1)
	if sent.DurationSketch == nil || sent.DurationDigest == nil {
		t.Fatalf("aggregate sketch %v, digest %v; want both for a release", sent.DurationSketch != nil, sent.DurationDigest != nil)
	}
	received := sendAggregate(t, sent)
	if !reflect.DeepEqual(received.DurationSketch, sent.DurationSketch) {
		t.Fatalf("received sketch %+v, sent %+v", received.DurationSketch, sent.DurationSketch)
	}
	sketch := engine.DurationSketchForCount(received.DurationSketch, received.Count)
	if sketch == nil {
		t.Fatal("received sketch does not restore")
	}
	if got, want := quantileFromMetricAggregate(metricAggregateToProto(&received), nil, 0.99), sketch.Quantile(0.99)/float64(time.Millisecond); got != want {
		t.Fatalf("archive summary p99 = %v, want the sketch's %v", got, want)
	}
}

// Ten generators' sketches, one of them slow, merged after proto and JSON
// round trips report exactly the quantiles of one sketch of every request.
func TestMergedSketchesMatchOneSketchOfEveryRequest(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	whole := engine.NewDurationSketch()
	received := make([]MetricAggregate, 0, 10)
	for g := 0; g < 10; g++ {
		generator := newMetricAccumulator("Checkout")
		for i := 0; i < 5_000; i++ {
			ms := math.Exp(math.Log(120) + 0.4*rng.NormFloat64())
			if g == 9 {
				ms = 30000 + rng.Float64()*50 // the slow generator
			}
			generator.add(TransactionMetric{Name: "Checkout", DurationMs: ms, Success: true})
			whole.Add(ms * float64(time.Millisecond))
		}
		aggregate := sendAggregate(t, generator.finalize(1))
		encoded, err := json.Marshal(aggregate.DurationSketch)
		if err != nil {
			t.Fatal(err)
		}
		aggregate.DurationSketch = nil
		if err := json.Unmarshal(encoded, &aggregate.DurationSketch); err != nil {
			t.Fatal(err)
		}
		received = append(received, aggregate)
	}
	merged := engine.DurationSketchForCount(mergeMetricAggregates(received).DurationSketch, whole.Count())
	if merged == nil {
		t.Fatal("merged aggregate has no sketch of every request")
	}
	for _, q := range []float64{0, 0.5, 0.9, 0.95, 0.99, 0.999, 1} {
		if got, want := merged.Quantile(q), whole.Quantile(q); got != want {
			t.Fatalf("merged q%v = %v, one sketch of every request %v", q, got, want)
		}
	}
}

func TestMergeMetricAggregatesPrefersSketchesAndFallsBack(t *testing.T) {
	durations := timeoutBoundaryDurations(3, 4_000, 20)
	parts := make([]MetricAggregate, 2)
	for i := range parts {
		generator := newMetricAccumulator("Checkout")
		for j := i; j < len(durations); j += 2 {
			generator.add(TransactionMetric{Name: "Checkout", DurationMs: durations[j], Success: true})
		}
		parts[i] = *generator.finalize(1)
	}
	digestP99 := func(values []MetricAggregate) float64 {
		digest := engine.NewTDigest(200)
		for _, value := range values {
			digest.Merge(engine.NewTDigestFromSnapshot(*value.DurationDigest))
		}
		return digest.Quantile(0.99) / float64(time.Millisecond)
	}

	t.Run("every aggregate carries a sketch", func(t *testing.T) {
		merged := mergeMetricAggregates(parts)
		want := engine.DurationSketchForCount(parts[0].DurationSketch, parts[0].Count)
		want.Merge(engine.DurationSketchForCount(parts[1].DurationSketch, parts[1].Count))
		if merged.DurationSketch == nil || merged.P99Ms != want.Quantile(0.99)/float64(time.Millisecond) {
			t.Fatalf("merged sketch %v, p99 %v; want the merged sketch's %v", merged.DurationSketch != nil, merged.P99Ms, want.Quantile(0.99)/float64(time.Millisecond))
		}
		if merged.DurationDigest == nil {
			t.Fatal("merge dropped the t-digest that older controllers read")
		}
	})

	t.Run("an older generator sends only a t-digest", func(t *testing.T) {
		values := append([]MetricAggregate(nil), parts...)
		values[1].DurationSketch = nil
		merged := mergeMetricAggregates(values)
		if merged.DurationSketch != nil || merged.P99Ms != digestP99(values) {
			t.Fatalf("merged sketch %v, p99 %v; want no sketch and the t-digests' %v", merged.DurationSketch != nil, merged.P99Ms, digestP99(values))
		}
	})

	t.Run("a sketch of other requests is not merged", func(t *testing.T) {
		other := newMetricAccumulator("Checkout")
		other.add(TransactionMetric{Name: "Checkout", DurationMs: 5, Success: true})
		values := append([]MetricAggregate(nil), parts...)
		values[1].DurationSketch = other.finalize(1).DurationSketch
		merged := mergeMetricAggregates(values)
		if merged.DurationSketch != nil || merged.P99Ms != digestP99(values) {
			t.Fatalf("merged sketch %v, p99 %v; want the t-digests' %v", merged.DurationSketch != nil, merged.P99Ms, digestP99(values))
		}
	})

	t.Run("no distribution", func(t *testing.T) {
		values := append([]MetricAggregate(nil), parts...)
		for i := range values {
			values[i].DurationSketch, values[i].DurationDigest = nil, nil
		}
		merged := mergeMetricAggregates(values)
		_, _, _, want := fallbackMergedPercentiles(values)
		if merged.P99Ms != want {
			t.Fatalf("p99 = %v, want the count-weighted %v", merged.P99Ms, want)
		}
	})
}

// A peer's sketch wider than the bucket limit is cut to one bucket over it, so
// the copy stays bounded and restoring it still fails.
func TestDurationSketchFromProtoBoundsBuckets(t *testing.T) {
	bins := make([]*pb.DurationSketchBin, engine.DurationSketchMaxBuckets+50)
	for i := range bins {
		bins[i] = &pb.DurationSketchBin{Index: int32(i), Count: 1}
	}
	snapshot := durationSketchSnapshotFromProto(&pb.DurationSketchSnapshot{
		Version: engine.DurationSketchSnapshotVersion, Alpha: engine.DurationSketchRelativeAccuracy,
		Count: uint64(len(bins)), Min: 1, Max: 2, Bins: bins,
	})
	if len(snapshot.Bins) != engine.DurationSketchMaxBuckets+1 {
		t.Fatalf("kept %d buckets, want %d", len(snapshot.Bins), engine.DurationSketchMaxBuckets+1)
	}
	if engine.DurationSketchForCount(snapshot, int64(len(bins))) != nil {
		t.Fatal("restored a sketch past the bucket limit")
	}
}
