package api

import (
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
)

// rawAggregates splits durations between n generators' aggregates.
func rawAggregates(durations []float64, n int) []generatorrpc.MetricAggregate {
	parts := make([]generatorrpc.MetricAggregate, n)
	for i := range parts {
		generator := newRawMetricAccumulator("Checkout")
		for j := i; j < len(durations); j += n {
			generator.add(generatorrpc.TransactionMetric{Name: "Checkout", DurationMs: durations[j], Success: true})
		}
		parts[i] = *generator.finalize(1)
	}
	return parts
}

func mergedDigestP99(digests ...*engine.TDigestSnapshot) float64 {
	merged := engine.NewTDigest(200)
	for _, digest := range digests {
		merged.Merge(engine.NewTDigestFromSnapshot(*digest))
	}
	return merged.Quantile(0.99) / float64(time.Millisecond)
}

// A generator that predates the sketch sends only a t-digest; the merge then
// uses the t-digests and passes on no sketch.
func TestControllerMergeFallsBackToTDigestsForOlderGenerators(t *testing.T) {
	parts := rawAggregates(timeoutBoundaryDurations(3, 4_000, 20), 2)
	merged := mergeGeneratorMetricAggregates(parts)
	if merged.DurationSketch == nil || merged.DurationDigest == nil {
		t.Fatalf("merged sketch %v, digest %v; want both", merged.DurationSketch != nil, merged.DurationDigest != nil)
	}
	older := append([]generatorrpc.MetricAggregate(nil), parts...)
	older[0].DurationSketch = nil
	merged = mergeGeneratorMetricAggregates(older)
	if want := mergedDigestP99(older[0].DurationDigest, older[1].DurationDigest); merged.DurationSketch != nil || merged.P99Ms != want {
		t.Fatalf("merged sketch %v, p99 %v; want no sketch and the t-digests' %v", merged.DurationSketch != nil, merged.P99Ms, want)
	}
}

// Aggregates keep their sketch through the live path as independent copies.
func TestLiveMetricsCarryAggregateSketches(t *testing.T) {
	aggregate := rawAggregates([]float64{10, 20, 30, 40}, 1)[0]
	batch := generatorrpc.MetricsBatch{
		WindowMs:           1000,
		FleetMetrics:       &aggregate,
		TransactionMetrics: []generatorrpc.MetricAggregate{aggregate},
		Groups:             []generatorrpc.GroupMetric{{Name: "buy", Metrics: &aggregate}},
	}
	live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, batch, 0, nil)
	transaction, group := live.Transactions["Checkout"], live.GroupMetrics["buy"]
	if transaction == nil || group == nil {
		t.Fatalf("transaction %v, group %v", transaction != nil, group != nil)
	}
	for name, sketch := range map[string]*engine.DurationSketchSnapshot{"transaction": transaction.DurationSketch, "group": group.DurationSketch} {
		if !reflect.DeepEqual(sketch, aggregate.DurationSketch) {
			t.Fatalf("%s sketch = %+v, want the aggregate's", name, sketch)
		}
	}
	transaction.DurationSketch.Bins[0].Count++
	if aggregate.DurationSketch.Bins[0].Count == transaction.DurationSketch.Bins[0].Count {
		t.Fatal("transaction metrics share the aggregate's buckets")
	}
}

func TestGroupLiveMetricsSketchClonesAndCompacts(t *testing.T) {
	batch := generatorrpc.MetricsBatch{WindowMs: 1000}
	for _, ms := range []float64{10, 20, 30} {
		batch.Transactions = append(batch.Transactions, generatorrpc.TransactionMetric{Name: "Checkout", Group: "buy", DurationMs: ms, Success: true})
	}
	live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, batch, 0, nil)
	group := live.GroupMetrics["buy"]
	if engine.DurationSketchForCount(group.DurationSketch, group.Transactions+group.Errors) == nil {
		t.Fatalf("group sketch %+v does not cover its %d attempts", group.DurationSketch, group.Transactions+group.Errors)
	}
	clone := cloneGroupLiveMetrics(group)
	clone.DurationSketch.Bins[0].Count++
	if group.DurationSketch.Bins[0].Count == clone.DurationSketch.Bins[0].Count {
		t.Fatal("clone shares its buckets with the original")
	}
	// The response-only cache is weighed by its JSON, which omits both.
	compactGroupFilteredMetricsForCache(live)
	if group.DurationSketch != nil || group.DurationDigest != nil {
		t.Fatal("cached group metrics keep a distribution the cache weight does not count")
	}
}

// When a selected group carries no sketch, selection uses the t-digests.
func TestSelectedGroupsFallBackToTDigests(t *testing.T) {
	durations := timeoutBoundaryDurations(5, 2_000, 10)
	batch := generatorrpc.MetricsBatch{WindowMs: 1000}
	for i, duration := range durations {
		batch.Transactions = append(batch.Transactions, generatorrpc.TransactionMetric{Name: "Checkout", Group: []string{"browse", "buy"}[i%2], DurationMs: duration, Success: true})
	}
	live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, batch, 0, nil)
	browse, buy := live.GroupMetrics["browse"], live.GroupMetrics["buy"]
	buy.DurationSketch = nil
	applySelectedGroupAggregate(live, []*GroupLiveMetrics{browse, buy})
	if want := mergedDigestP99(browse.DurationDigest, buy.DurationDigest); live.P99Ms != want {
		t.Fatalf("selected p99 = %v, want the t-digests' %v", live.P99Ms, want)
	}
}
