package engine

import (
	"context"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// ArrivalSampler generates arrival times for open-workload schedulers
// supporting uniform, Poisson (exponential inter-arrival), and Gamma (bursty
// with shape k = Burstiness, where CV^2 = 1/k) arrival processes, as well as
// stepped or linearly ramped rate stages.
//
// Arrivals are time-rescaled: unit-mean gaps accumulate in operational time
// tau, and each arrival lands at the wall-clock time t where the cumulative
// rate Lambda(t) (the integral of the rate schedule) reaches tau. A ramp
// therefore delivers its integral, not its starting rate: 0 to 10 TPS over an
// hour offers about 18,000 arrivals. At a constant rate this reduces to
// gaps of tau/rate, as before.
type ArrivalSampler struct {
	process    string
	burstiness float64
	baseTPS    float64
	stages     []dsl.PacingRateStage
	shardCount int
	rng        *rand.Rand
	segments   []arrivalRateSegment
	opTime     float64
}

// arrivalRateSegment is one piece of the shard-adjusted rate schedule: the
// rate moves linearly from `from` to `to` over dur seconds starting at start;
// the last segment is open-ended (dur = +Inf) at a constant rate.
type arrivalRateSegment struct {
	start, dur float64
	from, to   float64
	cumStart   float64 // Lambda(start)
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
	s := &ArrivalSampler{
		process:    proc,
		burstiness: pacing.Burstiness,
		baseTPS:    pacing.TPS,
		stages:     stages,
		shardCount: shardCount,
		rng:        rand.New(rand.NewSource(seed)),
	}
	s.segments = s.buildRateSegments()
	return s
}

// shardRate is the rate this generator offers for a configured rate.
func (s *ArrivalSampler) shardRate(raw float64) float64 {
	if raw <= 0 || math.IsNaN(raw) || math.IsInf(raw, 0) {
		return 0
	}
	raw = math.Min(raw, float64(dsl.MaxPacingTPS))
	if s.shardCount > 1 {
		raw /= float64(s.shardCount)
	}
	return raw
}

// buildRateSegments lays out the rate schedule EffectiveTPSAt describes, with
// the cumulative rate at each segment start.
func (s *ArrivalSampler) buildRateSegments() []arrivalRateSegment {
	if len(s.stages) == 0 {
		return []arrivalRateSegment{{dur: math.Inf(1), from: s.shardRate(s.baseTPS), to: s.shardRate(s.baseTPS)}}
	}
	var segments []arrivalRateSegment
	var start, cum float64
	for _, stage := range s.stages {
		if stage.DurationSeconds <= 0 {
			continue
		}
		dur := float64(stage.DurationSeconds)
		from := s.shardRate(stage.TargetTPS)
		to := from
		if stage.EndTPS != nil {
			to = s.shardRate(*stage.EndTPS)
		}
		segments = append(segments, arrivalRateSegment{start: start, dur: dur, from: from, to: to, cumStart: cum})
		start += dur
		cum += (from + to) / 2 * dur
	}
	// After the last stage the rate holds at its final value.
	last := s.stages[len(s.stages)-1]
	tail := last.TargetTPS
	if last.EndTPS != nil {
		tail = *last.EndTPS
	}
	tailRate := s.shardRate(tail)
	return append(segments, arrivalRateSegment{start: start, dur: math.Inf(1), from: tailRate, to: tailRate, cumStart: cum})
}

// arrivalTime inverts the cumulative rate: the elapsed seconds t at which
// Lambda(t) reaches opTime. ok is false when the rate stays at zero from
// there on, so no such arrival ever comes.
func (s *ArrivalSampler) arrivalTime(opTime float64) (float64, bool) {
	for _, seg := range s.segments {
		r := math.Max(0, opTime-seg.cumStart)
		if math.IsInf(seg.dur, 1) {
			if seg.from <= 0 {
				return 0, false
			}
			return seg.start + r/seg.from, true
		}
		// An arrival at a segment's end belongs to the next segment with a
		// positive rate (stages are half-open intervals). The cumulative sums
		// carry rounding error, so "at the end" allows a relative epsilon.
		area := (seg.from + seg.to) / 2 * seg.dur
		if area <= 0 || r >= area-arrivalBoundaryEpsilon*math.Max(1, seg.cumStart+area) {
			continue
		}
		// Within the segment the rate is from + slope*x, so Lambda grows by
		// from*x + slope*x^2/2; solve for x in the cancellation-free form.
		if r <= 0 {
			return seg.start, true
		}
		half := (seg.to - seg.from) / seg.dur / 2
		x := 2 * r / (seg.from + math.Sqrt(math.Max(0, seg.from*seg.from+4*half*r)))
		return seg.start + math.Min(math.Max(x, 0), seg.dur), true
	}
	return 0, false
}

// arrivalBoundaryEpsilon is the relative tolerance for an arrival landing on
// a segment boundary of the cumulative rate.
const arrivalBoundaryEpsilon = 1e-9

// unitGap draws one unit-mean inter-arrival gap in operational time.
func (s *ArrivalSampler) unitGap() float64 {
	switch s.process {
	case "poisson":
		return s.rng.ExpFloat64()
	case "gamma":
		k := s.burstiness
		if k <= 0 || math.IsNaN(k) || math.IsInf(k, 0) {
			k = 1.0
		}
		// Gamma(k, 1/k) has mean 1 and CV^2 = 1/k.
		return sampleGamma(s.rng, k) / k
	default:
		return 1
	}
}

// NextArrival returns the elapsed time of the next arrival and advances the
// sampler. The first arrival is due at the start of the first stage with a
// positive rate. ok is false once the rate stays at zero, when no further
// arrival will come.
func (s *ArrivalSampler) NextArrival() (time.Duration, bool) {
	if s == nil {
		return 0, false
	}
	at, ok := s.arrivalTime(s.opTime)
	if !ok {
		return 0, false
	}
	if gap := s.unitGap(); gap > 0 && !math.IsNaN(gap) {
		s.opTime += gap
	}
	nanos := at * float64(time.Second)
	if nanos >= float64(math.MaxInt64) {
		return time.Duration(math.MaxInt64), true
	}
	return time.Duration(nanos), true
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
	return s.shardRate(rawTPS), warmup
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

	sleep := func(delay time.Duration) bool {
		if sleepActive != nil {
			return sleepActive(ctx, delay)
		}
		return sleepContext(ctx, delay)
	}
	nextDeadline, due := sampler.NextArrival()
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
		if !due {
			// The rate stays at zero from here on: idle until the run ends.
			if !sleep(time.Second) {
				return
			}
			continue
		}

		now := schedulerElapsed()
		if now < nextDeadline {
			if !sleep(nextDeadline - now) {
				return
			}
			continue
		}

		// Dispatch due arrivals up to maxOpenWorkloadCatchUpBurst.
		dispatched := 0
		shedCount := 0
		firstShedIndex := 0
		for (totalArrivals <= 0 || arrival < totalArrivals) && due && nextDeadline <= now {
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
			nextDeadline, due = sampler.NextArrival()
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
