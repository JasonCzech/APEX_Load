package generatorrpc

import (
	"math"
	"math/rand"
	"sort"
	"testing"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/protobuf/proto"
)

// timeoutBoundaryDurations returns n durations in milliseconds, slow of them
// 30-second timeouts and the rest near 120 ms, in random order. With the
// timeouts just under 1% of n, the exact p99 is a fast request beside the gap
// between the two modes.
func timeoutBoundaryDurations(seed int64, n, slow int) []float64 {
	rng := rand.New(rand.NewSource(seed))
	durations := make([]float64, n)
	for i := range durations {
		if i < slow {
			durations[i] = 30000 + rng.Float64()*50
		} else {
			durations[i] = math.Exp(math.Log(120) + 0.3*rng.NormFloat64())
		}
	}
	rng.Shuffle(n, func(i, j int) { durations[i], durations[j] = durations[j], durations[i] })
	return durations
}

// percentileBandError reports how far got lies outside the sketch's accuracy
// band around the two samples that bracket rank q*(n-1); zero means inside.
func percentileBandError(sorted []float64, q, got float64) float64 {
	rank := q * float64(len(sorted)-1)
	lo, hi := sorted[int(math.Floor(rank))], sorted[int(math.Ceil(rank))]
	low, high := lo*(1-engine.DurationSketchRelativeAccuracy-1e-9), hi*(1+engine.DurationSketchRelativeAccuracy+1e-9)
	switch {
	case got < low:
		return (low - got) / lo
	case got > high:
		return (got - high) / hi
	}
	return 0
}

// sendAggregate passes an aggregate through its wire form.
func sendAggregate(t *testing.T, aggregate *MetricAggregate) MetricAggregate {
	t.Helper()
	wire, err := proto.Marshal(metricAggregateToProto(aggregate))
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.MetricAggregate
	if err := proto.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	return *metricAggregateFromProto(&decoded)
}

// Percentiles merged from two generators' aggregates stay within the sketch's
// accuracy when timeouts sit just under 1% of requests, where the t-digest's
// interpolation reported values from the gap between the modes.
func TestMergedAggregatePercentilesHaveNoModeBoundaryError(t *testing.T) {
	const n = 20_000
	for slow := 190; slow < 200; slow++ {
		durations := timeoutBoundaryDurations(int64(slow), n, slow)
		generators := []*metricAccumulator{newMetricAccumulator("Checkout"), newMetricAccumulator("Checkout")}
		for i, duration := range durations {
			generators[i%2].add(TransactionMetric{Name: "Checkout", DurationMs: duration, Success: true})
		}
		received := []MetricAggregate{sendAggregate(t, generators[0].finalize(1)), sendAggregate(t, generators[1].finalize(1))}
		merged := mergeMetricAggregates(received)
		if merged == nil || merged.Count != n {
			t.Fatalf("merged = %+v, want %d requests", merged, n)
		}
		sort.Float64s(durations)
		for _, check := range []struct{ q, got float64 }{{0.95, merged.P95Ms}, {0.99, merged.P99Ms}} {
			if err := percentileBandError(durations, check.q, check.got); err > 0 {
				t.Errorf("%d timeouts in %d requests: merged p%v = %.1f ms, outside the sketch's band by %.3g", slow, n, check.q*100, check.got, err)
			}
		}
	}
}
