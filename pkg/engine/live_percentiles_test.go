package engine

import (
	"math"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// timeoutBoundaryResults returns n business transactions and n requests whose
// durations are slow 30-second timeouts and otherwise near 120 ms, in random
// order, with the sorted durations. With the timeouts just under 1% of n, the
// exact p99 is a fast request beside the gap between the two modes.
func timeoutBoundaryResults(seed int64, n, slow int) ([]*protocol.Result, []float64) {
	rng := rand.New(rand.NewSource(seed))
	results := make([]*protocol.Result, 0, 2*n)
	durations := make([]float64, n)
	for i := range durations {
		ms := math.Exp(math.Log(120) + 0.3*rng.NormFloat64())
		if i < slow {
			ms = 30000 + rng.Float64()*50
		}
		duration := time.Duration(ms * float64(time.Millisecond))
		durations[i] = float64(duration)
		results = append(results,
			&protocol.Result{Group: "shop", ActionName: "Checkout", BusinessTransaction: true, Success: true, Duration: duration},
			&protocol.Result{Group: "shop", ActionName: "submit order", Success: true, Duration: duration})
	}
	rng.Shuffle(len(results), func(i, j int) { results[i], results[j] = results[j], results[i] })
	sort.Float64s(durations)
	return results, durations
}

// A live window's percentiles stay within the sketch's accuracy when timeouts
// sit just under 1% of requests, where the t-digest's interpolation reported
// values from the gap between the modes.
func TestSnapshotPercentilesHaveNoModeBoundaryError(t *testing.T) {
	const n = 20_000
	for slow := 190; slow < 200; slow++ {
		results, durations := timeoutBoundaryResults(int64(slow), n, slow)
		snap := NewMetricsCollector(10).computeSnapshot(results, time.Second)
		if len(snap.ByAction) != 1 || snap.ByTransaction["Checkout"] == nil || snap.ByGroup["shop"] == nil {
			t.Fatalf("snapshot actions %d, transaction %v, group %v", len(snap.ByAction), snap.ByTransaction["Checkout"] != nil, snap.ByGroup["shop"] != nil)
		}
		p99 := map[string]time.Duration{"window": snap.P99, "transaction": snap.ByTransaction["Checkout"].P99, "group": snap.ByGroup["shop"].P99}
		for _, action := range snap.ByAction {
			p99["action"] = action.P99
		}
		for name, got := range p99 {
			// A reported duration is truncated to whole nanoseconds.
			if err := sketchBracketError(durations, 0.99, float64(got)); err > 1e-8 {
				t.Errorf("%d timeouts in %d: %s p99 = %v, outside the sketch's band by %.3g", slow, n, name, got, err)
			}
		}
	}
}
