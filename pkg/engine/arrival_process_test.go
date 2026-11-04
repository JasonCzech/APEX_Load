package engine

import (
	"context"
	"math"
	"sort"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestPoissonArrivalProcessKSTest(t *testing.T) {
	seed := int64(424242)
	const lambda = 20.0
	sampler := NewArrivalSampler(dsl.PacingConfig{
		Type:           "tps",
		TPS:            lambda,
		ArrivalProcess: "poisson",
		Seed:           &seed,
	}, DataShardConfig{})

	const n = 5000
	samples := make([]float64, n)
	prev, ok := sampler.NextArrival()
	if !ok {
		t.Fatalf("expected a first arrival")
	}
	for i := 0; i < n; i++ {
		at, ok := sampler.NextArrival()
		if !ok {
			t.Fatalf("unexpected non-ok at sample %d", i)
		}
		samples[i] = (at - prev).Seconds()
		prev = at
	}

	sort.Float64s(samples)
	var dMax float64
	for i, x := range samples {
		cdf := 1.0 - math.Exp(-lambda*x)
		empLow := float64(i) / float64(n)
		empHigh := float64(i+1) / float64(n)
		if diff := math.Abs(empLow - cdf); diff > dMax {
			dMax = diff
		}
		if diff := math.Abs(empHigh - cdf); diff > dMax {
			dMax = diff
		}
	}
	// Kolmogorov-Smirnov asymptotic p-value: p ≈ 2 * sum_{j>=1} (-1)^{j-1} exp(-2 j^2 z^2)
	z := dMax * math.Sqrt(float64(n))
	pVal := ksAsymptoticPValue(z)
	if pVal <= 0.01 {
		t.Fatalf("Poisson KS test failed: D=%v, z=%v, p=%v (expected p > 0.01)", dMax, z, pVal)
	}
}

func ksAsymptoticPValue(z float64) float64 {
	if z <= 0 {
		return 1.0
	}
	var sum float64
	for j := 1; j <= 50; j++ {
		term := math.Exp(-2.0 * float64(j*j) * z * z)
		if j%2 == 1 {
			sum += term
		} else {
			sum -= term
		}
	}
	return math.Max(0, math.Min(1, 2.0*sum))
}

func TestGammaArrivalProcessCVSquared(t *testing.T) {
	for _, k := range []float64{0.5, 2.5} {
		seed := int64(987654321)
		sampler := NewArrivalSampler(dsl.PacingConfig{
			Type:           "tps",
			TPS:            10.0,
			ArrivalProcess: "gamma",
			Burstiness:     k,
			Seed:           &seed,
		}, DataShardConfig{})

		const n = 20000
		var sum, sumSq float64
		prev, _ := sampler.NextArrival()
		for i := 0; i < n; i++ {
			at, ok := sampler.NextArrival()
			if !ok {
				t.Fatalf("k=%v: unexpected !ok at %d", k, i)
			}
			sec := (at - prev).Seconds()
			prev = at
			sum += sec
			sumSq += sec * sec
		}
		mean := sum / float64(n)
		variance := (sumSq / float64(n)) - mean*mean
		cvSq := variance / (mean * mean)
		expectedCVSq := 1.0 / k
		relErr := math.Abs(cvSq-expectedCVSq) / expectedCVSq
		if relErr > 0.10 {
			t.Fatalf("k=%v: Gamma CV^2=%v, expected %v (relative error %.2f%% > 10%%)", k, cvSq, expectedCVSq, relErr*100)
		}
	}
}

func TestArrivalSamplerDeterministicSeedReplayAndRateStages(t *testing.T) {
	seed := int64(1337)
	endTPS := 30.0
	cfg := dsl.PacingConfig{
		Type:           "tps",
		ArrivalProcess: "poisson",
		Seed:           &seed,
		RateStages: []dsl.PacingRateStage{
			{DurationSeconds: 10, TargetTPS: 5, Warmup: true},
			{DurationSeconds: 20, TargetTPS: 10, EndTPS: &endTPS, Warmup: false},
		},
	}

	s1 := NewArrivalSampler(cfg, DataShardConfig{})
	s2 := NewArrivalSampler(cfg, DataShardConfig{})
	for i := 0; i < 100; i++ {
		d1, ok1 := s1.NextArrival()
		d2, ok2 := s2.NextArrival()
		if d1 != d2 || ok1 != ok2 {
			t.Fatalf("seed replay mismatch at %d: (%v,%v) vs (%v,%v)", i, d1, ok1, d2, ok2)
		}
	}

	// Verify stage rate interpolation and shard division:
	sharded := NewArrivalSampler(cfg, DataShardConfig{Ordinal: 1, Count: 2})
	tps0, warm0 := sharded.EffectiveTPSAt(5 * time.Second)
	if !warm0 || math.Abs(tps0-2.5) > 1e-9 {
		t.Fatalf("stage 0 sharded TPS=%v warmup=%v, want 2.5 true", tps0, warm0)
	}
	tpsMid, warmMid := sharded.EffectiveTPSAt(20 * time.Second) // halfway through 20s ramp 10->30 => 20 / 2 = 10
	if warmMid || math.Abs(tpsMid-10.0) > 1e-9 {
		t.Fatalf("stage 1 midpoint sharded TPS=%v warmup=%v, want 10.0 false", tpsMid, warmMid)
	}
}

func TestExponentialThinkTimeClamp(t *testing.T) {
	cfg := protocol.ThinkTimeConfig{
		Type:         "exponential",
		Distribution: "exponential",
		MinMs:        100,
		MaxMs:        300,
	}
	var sum float64
	const n = 2000
	for i := 0; i < n; i++ {
		d := computeThinkTime(cfg)
		ms := float64(d) / float64(time.Millisecond)
		if ms < 100 || ms > 1200 {
			t.Fatalf("exponential think time %v ms outside clamped bounds [100, 1200]", ms)
		}
		sum += ms
	}
	mean := sum / float64(n)
	if mean < 150 || mean > 300 {
		t.Fatalf("exponential think time sample mean = %v ms, expected roughly ~200 ms", mean)
	}
}

func arrivalsBefore(t *testing.T, sampler *ArrivalSampler, until time.Duration) []time.Duration {
	t.Helper()
	var out []time.Duration
	for {
		at, ok := sampler.NextArrival()
		if !ok || at >= until {
			return out
		}
		if len(out) > 0 && at < out[len(out)-1] {
			t.Fatalf("arrival times must not decrease: %v after %v", at, out[len(out)-1])
		}
		out = append(out, at)
	}
}

// A ramp offers its integral: 0 to 10 TPS over an hour is about 18,000
// arrivals, not the one a sampled starting rate of 0 would give.
func TestArrivalSamplerRampDeliversItsIntegral(t *testing.T) {
	end := 10.0
	sampler := NewArrivalSampler(dsl.PacingConfig{Type: "tps", RateStages: []dsl.PacingRateStage{{DurationSeconds: 3600, TargetTPS: 0, EndTPS: &end}}}, DataShardConfig{})
	got := arrivalsBefore(t, sampler, time.Hour)
	if n := len(got); n < 17999 || n > 18001 {
		t.Fatalf("arrivals in the ramp = %d, want about 18000", n)
	}
	// Halfway through, the rate is 5 TPS: a quarter of the arrivals so far.
	var firstHalf int
	for _, at := range got {
		if at < 30*time.Minute {
			firstHalf++
		}
	}
	if firstHalf < 4490 || firstHalf > 4510 {
		t.Fatalf("arrivals in the first half = %d, want about 4500", firstHalf)
	}

	sharded := NewArrivalSampler(dsl.PacingConfig{Type: "tps", RateStages: []dsl.PacingRateStage{{DurationSeconds: 3600, TargetTPS: 0, EndTPS: &end}}}, DataShardConfig{Ordinal: 1, Count: 3})
	if n := len(arrivalsBefore(t, sharded, time.Hour)); n < 5999 || n > 6001 {
		t.Fatalf("one of three generators offered %d arrivals, want about 6000", n)
	}
}

// A ramp-down does not push its arrivals into the next stage, and a zero-rate
// stage offers none.
func TestArrivalSamplerStagesKeepTheirArrivals(t *testing.T) {
	zero := 0.0
	seed := int64(7)
	for _, process := range []string{"uniform", "poisson", "gamma"} {
		t.Run(process, func(t *testing.T) {
			sampler := NewArrivalSampler(dsl.PacingConfig{
				Type: "tps", ArrivalProcess: process, Burstiness: 2, Seed: &seed,
				RateStages: []dsl.PacingRateStage{
					{DurationSeconds: 10, TargetTPS: 0},
					{DurationSeconds: 100, TargetTPS: 20, EndTPS: &zero},
					{DurationSeconds: 100, TargetTPS: 10},
				},
			}, DataShardConfig{})
			got := arrivalsBefore(t, sampler, 210*time.Second)
			var idle, rampDown, steady int
			for _, at := range got {
				switch {
				case at < 10*time.Second:
					idle++
				case at < 110*time.Second:
					rampDown++
				default:
					steady++
				}
			}
			tolerance := 1
			if process != "uniform" {
				tolerance = 150
			}
			if idle != 0 || absInt(rampDown-1000) > tolerance || absInt(steady-1000) > tolerance {
				t.Fatalf("idle=%d rampDown=%d steady=%d, want 0, about 1000, about 1000", idle, rampDown, steady)
			}
			if got[0] < 10*time.Second {
				t.Fatalf("first arrival %v falls in the zero-rate stage", got[0])
			}
		})
	}
}

func TestArrivalSamplerConstantRateMatchesUniformDeadlines(t *testing.T) {
	sampler := NewArrivalSampler(dsl.PacingConfig{Type: "tps", RateStages: []dsl.PacingRateStage{{DurationSeconds: 60, TargetTPS: 8}}}, DataShardConfig{})
	for i := 0; i < 400; i++ {
		at, ok := sampler.NextArrival()
		want := time.Duration(float64(i) / 8 * float64(time.Second))
		if !ok || absDuration(at-want) > time.Microsecond {
			t.Fatalf("arrival %d at %v, want %v", i, at, want)
		}
	}
	trailingZero := NewArrivalSampler(dsl.PacingConfig{Type: "tps", RateStages: []dsl.PacingRateStage{{DurationSeconds: 1, TargetTPS: 4}, {DurationSeconds: 1, TargetTPS: 0}}}, DataShardConfig{})
	if n := len(arrivalsBefore(t, trailingZero, time.Hour)); n != 4 {
		t.Fatalf("a schedule ending at zero offered %d arrivals, want 4", n)
	}
	if _, ok := trailingZero.NextArrival(); ok {
		t.Fatalf("no arrival may follow once the rate stays at zero")
	}
}

// The scheduler dispatches what the sampler offers on the run's own clock.
func TestSampledArrivalSchedulerFollowsARamp(t *testing.T) {
	end := 100.0
	sampler := NewArrivalSampler(dsl.PacingConfig{Type: "tps", RateStages: []dsl.PacingRateStage{{DurationSeconds: 10, TargetTPS: 0, EndTPS: &end}}}, DataShardConfig{})
	var clock time.Duration
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dispatched := 0
	runSampledArrivalSchedulerFromOrigin(ctx, sampler, 0, nil,
		func(ctx context.Context, d time.Duration) bool {
			clock += d
			if clock >= 10*time.Second {
				cancel()
				return false
			}
			return true
		},
		func() time.Duration { return clock },
		true,
		func(int) { dispatched++ },
		nil,
	)
	if dispatched < 499 || dispatched > 501 {
		t.Fatalf("a 0-100 TPS ramp over 10s dispatched %d arrivals, want about 500", dispatched)
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
