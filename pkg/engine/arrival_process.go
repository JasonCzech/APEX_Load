package engine

import (
	"context"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// ArrivalSampler generates inter-arrival durations for open-workload schedulers
// supporting uniform, Poisson (exponential inter-arrival), and Gamma (bursty
// with shape k = Burstiness, where CV^2 = 1/k) arrival processes, as well as
// stepped or linearly ramped rate stages.
type ArrivalSampler struct {
	process    string
	burstiness float64
	baseTPS    float64
	stages     []dsl.PacingRateStage
	shardCount int
	rng        *rand.Rand
}

// NewArrivalSampler constructs a deterministic or time-seeded arrival sampler
// from a script's PacingConfig and optional generator shard configuration.
func NewArrivalSampler(pacing dsl.PacingConfig, shard DataShardConfig) *ArrivalSampler {
	proc := strings.ToLower(strings.TrimSpace(pacing.ArrivalProcess))
	if proc == "" {
		proc = "uniform"
	}
	shardCount := 1
	if shard.Count > 1 {
		shardCount = int(shard.Count)
	}
	var seed int64
	if pacing.Seed != nil {
		seed = *pacing.Seed
		if shard.Ordinal > 0 {
			seed ^= int64(uint64(shard.Ordinal) * 0x9e3779b97f4a7c15)
		}
	} else {
		seed = time.Now().UnixNano() ^ int64(uint64(shard.Ordinal)*0x9e3779b97f4a7c15)
	}
	stages := make([]dsl.PacingRateStage, len(pacing.RateStages))
	copy(stages, pacing.RateStages)
	return &ArrivalSampler{
		process:    proc,
		burstiness: pacing.Burstiness,
		baseTPS:    pacing.TPS,
		stages:     stages,
		shardCount: shardCount,
		rng:        rand.New(rand.NewSource(seed)),
	}
}

// IsNonUniformOrStaged reports whether the pacing config uses a non-uniform
// arrival process ("poisson", "gamma") or stepped rate stages.
func IsNonUniformOrStaged(pacing dsl.PacingConfig) bool {
	proc := strings.ToLower(strings.TrimSpace(pacing.ArrivalProcess))
	if proc == "poisson" || proc == "gamma" {
		return true
	}
	return len(pacing.RateStages) > 0
}

// EffectiveTPSAt returns the shard-adjusted target TPS at the given elapsed
// duration, along with whether the current stage is marked as warmup.
func (s *ArrivalSampler) EffectiveTPSAt(elapsed time.Duration) (tps float64, warmup bool) {
	if s == nil {
		return 0, false
	}
	rawTPS := s.baseTPS
	if len(s.stages) > 0 {
		if elapsed < 0 {
			elapsed = 0
		}
		var cursor time.Duration
		matched := false
		for _, stage := range s.stages {
			if stage.DurationSeconds <= 0 {
				continue
			}
			stageDur := time.Duration(stage.DurationSeconds) * time.Second
			if elapsed < cursor+stageDur {
				rawTPS = stage.TargetTPS
				if stage.EndTPS != nil {
					progress := float64(elapsed-cursor) / float64(stageDur)
					progress = math.Max(0, math.Min(1, progress))
					rawTPS = stage.TargetTPS + progress*(*stage.EndTPS-stage.TargetTPS)
				}
				warmup = stage.Warmup
				matched = true
				break
			}
			cursor += stageDur
		}
		if !matched {
			last := s.stages[len(s.stages)-1]
			rawTPS = last.TargetTPS
			if last.EndTPS != nil {
				rawTPS = *last.EndTPS
			}
			warmup = last.Warmup
		}
	}
	if rawTPS <= 0 || math.IsNaN(rawTPS) || math.IsInf(rawTPS, 0) {
		return 0, warmup
	}
	rawTPS = math.Min(rawTPS, float64(dsl.MaxPacingTPS))
	if s.shardCount > 1 {
		rawTPS /= float64(s.shardCount)
	}
	return rawTPS, warmup
}

// NextInterArrival samples the next inter-arrival duration at the given elapsed
// time. If the effective TPS at elapsed is <= 0, it returns a short step wait
// (100ms) and ok=false so the caller can advance its clock without dispatching.
func (s *ArrivalSampler) NextInterArrival(elapsed time.Duration) (delay time.Duration, warmup bool, ok bool) {
	tps, warmup := s.EffectiveTPSAt(elapsed)
	if tps <= 0 {
		return 100 * time.Millisecond, warmup, false
	}
	meanSec := 1.0 / tps
	var sec float64
	switch s.process {
	case "poisson":
		sec = s.rng.ExpFloat64() * meanSec
	case "gamma":
		k := s.burstiness
		if k <= 0 || math.IsNaN(k) || math.IsInf(k, 0) {
			k = 1.0
		}
		// Gamma(k, theta) with mean = k * theta = meanSec -> theta = meanSec / k
		sec = sampleGamma(s.rng, k) * (meanSec / k)
	default:
		sec = meanSec
	}
	if sec < 0 || math.IsNaN(sec) {
		sec = 0
	}
	nanos := sec * float64(time.Second)
	if nanos >= float64(math.MaxInt64) {
		return time.Duration(math.MaxInt64), warmup, true
	}
	return time.Duration(nanos), warmup, true
}

// sampleGamma draws a standard Gamma(k, 1) variate using Marsaglia and Tsang's
// method for k >= 1 and Ahrens-Dieter boosting for 0 < k < 1.
func sampleGamma(rng *rand.Rand, k float64) float64 {
	if k <= 0 {
		return 1.0
	}
	if k < 1.0 {
		u := rng.Float64()
		for u <= 0 {
			u = rng.Float64()
		}
		return sampleGamma(rng, k+1.0) * math.Pow(u, 1.0/k)
	}
	d := k - 1.0/3.0
	c := 1.0 / math.Sqrt(9.0*d)
	for {
		x := rng.NormFloat64()
		v := 1.0 + c*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := rng.Float64()
		if u < 1.0-0.0331*(x*x)*(x*x) {
			return d * v
		}
		if math.Log(u) < 0.5*x*x+d*(1.0-v+math.Log(v)) {
			return d * v
		}
	}
}

func maxConfiguredOpenTPS(pacing dsl.PacingConfig) float64 {
	best := pacing.TPS
	if math.IsNaN(best) || math.IsInf(best, 0) || best < 0 {
		best = 0
	}
	for _, st := range pacing.RateStages {
		if st.TargetTPS > best && !math.IsNaN(st.TargetTPS) && !math.IsInf(st.TargetTPS, 0) {
			best = st.TargetTPS
		}
		if st.EndTPS != nil && *st.EndTPS > best && !math.IsNaN(*st.EndTPS) && !math.IsInf(*st.EndTPS, 0) {
			best = *st.EndTPS
		}
	}
	return best
}

func runSampledArrivalSchedulerFromOrigin(
	ctx context.Context,
	sampler *ArrivalSampler,
	totalArrivals int,
	waitIfPaused func(context.Context) error,
	sleepActive func(context.Context, time.Duration) bool,
	activeElapsed func() time.Duration,
	preserveActiveOrigin bool,
	dispatch func(arrivalIndex int),
	shed func(firstArrivalIndex int, count int),
) {
	if sampler == nil {
		return
	}
	started := time.Now()
	if activeElapsed == nil {
		activeElapsed = func() time.Duration { return time.Since(started) }
	}
	activeOrigin := time.Duration(0)
	if !preserveActiveOrigin {
		activeOrigin = activeElapsed()
	}
	schedulerElapsed := func() time.Duration {
		elapsed := activeElapsed() - activeOrigin
		if elapsed < 0 {
			return 0
		}
		return elapsed
	}

	var nextDeadline time.Duration
	arrival := 0
	for totalArrivals <= 0 || arrival < totalArrivals {
		if waitIfPaused != nil {
			if err := waitIfPaused(ctx); err != nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}

		now := schedulerElapsed()
		if now < nextDeadline {
			delay := nextDeadline - now
			if sleepActive != nil {
				if !sleepActive(ctx, delay) {
					return
				}
			} else if !sleepContext(ctx, delay) {
				return
			}
			continue
		}

		// Dispatch due arrivals up to maxOpenWorkloadCatchUpBurst.
		dispatched := 0
		shedCount := 0
		firstShedIndex := 0
		for (totalArrivals <= 0 || arrival < totalArrivals) && nextDeadline <= now {
			stepDelay, _, ok := sampler.NextInterArrival(nextDeadline)
			if !ok {
				nextDeadline += stepDelay
				break
			}
			if dispatched < maxOpenWorkloadCatchUpBurst {
				if waitIfPaused != nil {
					if err := waitIfPaused(ctx); err != nil {
						return
					}
				}
				if ctx.Err() != nil {
					return
				}
				dispatch(arrival)
				dispatched++
			} else {
				if shedCount == 0 {
					firstShedIndex = arrival
				}
				shedCount++
			}
			arrival++
			nextDeadline += stepDelay
		}
		if shedCount > 0 && shed != nil {
			shed(firstShedIndex, shedCount)
		}
	}
}

func runOpenWorkloadScheduler(
	ctx context.Context,
	pacing dsl.PacingConfig,
	shard DataShardConfig,
	tps float64,
	totalArrivals int,
	waitIfPaused func(context.Context) error,
	sleepActive func(context.Context, time.Duration) bool,
	activeElapsed func() time.Duration,
	preserveActiveOrigin bool,
	dispatch func(arrivalIndex int),
	shed func(firstArrivalIndex int, count int),
) {
	if IsNonUniformOrStaged(pacing) {
		sampler := NewArrivalSampler(pacing, shard)
		runSampledArrivalSchedulerFromOrigin(ctx, sampler, totalArrivals, waitIfPaused, sleepActive, activeElapsed, preserveActiveOrigin, dispatch, shed)
		return
	}
	runArrivalSchedulerFromOrigin(ctx, tps, totalArrivals, waitIfPaused, sleepActive, activeElapsed, preserveActiveOrigin, dispatch, shed)
}
