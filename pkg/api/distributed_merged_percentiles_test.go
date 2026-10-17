package api

import (
	"math"
	"math/rand"
	"testing"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
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

// Fleet, transaction and group percentiles the controller merges from
// generators' aggregates stay within the sketch's accuracy when timeouts sit
// just under 1% of requests, where the t-digest's interpolation reported
// values from the gap between the modes.
func TestControllerMergedPercentilesHaveNoModeBoundaryError(t *testing.T) {
	const n = 20_000
	for slow := 190; slow < 200; slow++ {
		durations := timeoutBoundaryDurations(int64(slow), n, slow)
		batches := make(map[string]generatorrpc.MetricsBatch, 2)
		for g, generator := range []string{"gen-a", "gen-b"} {
			acc := newRawMetricAccumulator("Checkout")
			for i := g; i < n; i += 2 {
				acc.add(generatorrpc.TransactionMetric{Name: "Checkout", Group: "shop", DurationMs: durations[i], Success: true})
			}
			fleet, transaction, group := *acc.finalize(1), *acc.finalize(1), *acc.finalize(1)
			batches[generator] = generatorrpc.MetricsBatch{
				WindowMs:           1000,
				FleetMetrics:       &fleet,
				TransactionMetrics: []generatorrpc.MetricAggregate{transaction},
				Groups:             []generatorrpc.GroupMetric{{Name: "shop", Metrics: &group}},
			}
		}
		combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("run", batches)
		live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, combined, fleetTPS, groupTPS)
		if live.Transactions["Checkout"] == nil || live.GroupMetrics["shop"] == nil {
			t.Fatalf("live transactions %v, groups %v", live.Transactions, live.GroupMetrics)
		}
		sorted := sortedGateSamples(durations)
		for name, got := range map[string]float64{"fleet": live.P99Ms, "transaction": live.Transactions["Checkout"].P99Ms, "group": live.GroupMetrics["shop"].P99Ms} {
			if err := archiveBracketError(sorted, 0.99, got); err > 0 {
				t.Errorf("%d timeouts in %d requests: %s p99 = %.1f ms, outside the sketch's band by %.3g", slow, n, name, got, err)
			}
		}
	}
}

// Selecting groups recomputes the fleet percentiles from the groups'
// distributions, which must not move p99 into the gap either.
func TestSelectedGroupPercentilesHaveNoModeBoundaryError(t *testing.T) {
	const n = 20_000
	for slow := 190; slow < 200; slow++ {
		durations := timeoutBoundaryDurations(int64(slow), n, slow)
		batch := generatorrpc.MetricsBatch{WindowMs: 1000}
		for i, duration := range durations {
			batch.Transactions = append(batch.Transactions, generatorrpc.TransactionMetric{Name: "Checkout", Group: []string{"browse", "buy"}[i%2], DurationMs: duration, Success: true})
		}
		live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, batch, 0, nil)
		applySelectedGroupAggregate(live, []*GroupLiveMetrics{live.GroupMetrics["browse"], live.GroupMetrics["buy"]})
		if err := archiveBracketError(sortedGateSamples(durations), 0.99, live.P99Ms); err > 0 {
			t.Errorf("%d timeouts in %d requests: selected groups' p99 = %.1f ms, outside the sketch's band by %.3g", slow, n, live.P99Ms, err)
		}
	}
}
