package engine

import (
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
	var elapsed time.Duration
	for i := 0; i < n; i++ {
		dt, warmup, ok := sampler.NextInterArrival(elapsed)
		if !ok || warmup {
			t.Fatalf("unexpected non-ok or warmup at sample %d", i)
		}
		samples[i] = dt.Seconds()
		elapsed += dt
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
		var elapsed time.Duration
		for i := 0; i < n; i++ {
			dt, _, ok := sampler.NextInterArrival(elapsed)
			if !ok {
				t.Fatalf("k=%v: unexpected !ok at %d", k, i)
			}
			sec := dt.Seconds()
			sum += sec
			sumSq += sec * sec
			elapsed += dt
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
		d1, w1, ok1 := s1.NextInterArrival(time.Duration(i) * time.Second)
		d2, w2, ok2 := s2.NextInterArrival(time.Duration(i) * time.Second)
		if d1 != d2 || w1 != w2 || ok1 != ok2 {
			t.Fatalf("seed replay mismatch at %d: (%v,%v,%v) vs (%v,%v,%v)", i, d1, w1, ok1, d2, w2, ok2)
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
