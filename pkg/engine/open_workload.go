package engine

import (
	"context"
	"math"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// IsOpenWorkload reports whether the script runs the open arrival model, so
// run provenance can record the model the engine actually used.
func IsOpenWorkload(script *dsl.Script) bool {
	return isOpenWorkload(script)
}

func isOpenWorkload(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	tps := script.Config.Pacing.TPS
	return dsl.CanonicalWorkloadModel(script.Config.RunLogic.WorkloadModel) == "open" && dsl.CanonicalPacingType(script.Config.Pacing.Type) == "tps" && tps > 0 && !math.IsNaN(tps) && !math.IsInf(tps, 0)
}

// defaultOpenWorkloadInFlightPerTPS bounds the live arrival-worker set, in the
// absence of an explicit RunLogic.MaxInFlight, to this many seconds' worth of
// arrivals at the offered TPS. Steady-state in-flight ≈ TPS × mean-iteration-
// duration, so a run whose iterations complete in well under this window never
// sheds; only a saturating/degrading SUT (completions falling behind arrivals)
// hits the cap. This keeps a healthy run unthrottled while preventing the live
// goroutine set from growing without limit and OOM-ing the generator.
const defaultOpenWorkloadInFlightPerTPS = 60

// maxOpenWorkloadCatchUpBurst bounds the number of overdue arrivals released
// in one scheduler turn. Ordinary dispatcher overhead is recovered from the
// absolute active-time clock, while a severely stalled generator sheds the
// excess backlog instead of producing an unbounded recovery spike.
const maxOpenWorkloadCatchUpBurst = 1024

// openWorkloadInFlightLimit returns the maximum number of concurrently live
// arrival workers for an open workload. An explicit RunLogic.MaxInFlight wins;
// otherwise a generous ceiling proportional to the offered TPS is derived.
// Returns 0 (unbounded) only when no ceiling can be derived (TPS unknown), which
// does not occur for a valid open workload (isOpenWorkload requires TPS > 0).
func openWorkloadInFlightLimit(script *dsl.Script) int {
	if script == nil {
		return 0
	}
	if explicit := script.Config.RunLogic.MaxInFlight; explicit > 0 {
		return min(explicit, dsl.MaxOpenWorkloadInFlight)
	}
	tps := script.Config.Pacing.TPS
	if tps <= 0 {
		return 0
	}
	maxDerivedTPS := float64(dsl.MaxOpenWorkloadInFlight / defaultOpenWorkloadInFlightPerTPS)
	if tps >= maxDerivedTPS {
		return dsl.MaxOpenWorkloadInFlight
	}
	limit := int(math.Ceil(tps)) * defaultOpenWorkloadInFlightPerTPS
	if limit < defaultOpenWorkloadInFlightPerTPS {
		limit = defaultOpenWorkloadInFlightPerTPS
	}
	if limit > dsl.MaxOpenWorkloadInFlight {
		return dsl.MaxOpenWorkloadInFlight
	}
	return limit
}

// inFlightLimiter bounds the number of concurrently live open-workload arrival
// workers. Open-model arrivals are paced by wall-clock TPS independent of how
// fast the system under test responds, so when completions fall behind arrivals
// the set of live worker goroutines (each ~KB of stack plus a protocol adapter
// and connection) would otherwise grow without limit. The limiter sheds excess
// arrivals rather than blocking the arrival ticker — blocking would silently
// convert the test to a closed model and mask the saturation.
type inFlightLimiter struct {
	tokens chan struct{}
	shed   atomic.Int64
	warned atomic.Bool
}

// newInFlightLimiter returns a limiter capping live workers at max, or nil
// (unbounded) when max <= 0. A nil limiter's methods are safe no-ops.
func newInFlightLimiter(max int) *inFlightLimiter {
	if max <= 0 {
		return nil
	}
	max = min(max, dsl.MaxOpenWorkloadInFlight)
	return &inFlightLimiter{tokens: make(chan struct{}, max)}
}

// tryAcquire reserves a slot without blocking, returning false (and recording a
// shed) when the limiter is at capacity. A nil limiter always succeeds.
func (l *inFlightLimiter) tryAcquire() bool {
	if l == nil {
		return true
	}
	select {
	case l.tokens <- struct{}{}:
		return true
	default:
		l.recordShed(1)
		return false
	}
}

func (l *inFlightLimiter) recordShed(count int64) {
	if l == nil || count <= 0 {
		return
	}
	l.shed.Add(count)
}

// release returns a slot. It must be called exactly once for each successful
// tryAcquire (including the early-exit paths of a worker that never ran).
func (l *inFlightLimiter) release() {
	if l == nil {
		return
	}
	<-l.tokens
}

// shouldWarn reports true exactly once, for the first shed, so callers can emit
// a single operator-visible warning instead of one per dropped arrival.
func (l *inFlightLimiter) shouldWarn() bool {
	return l != nil && l.warned.CompareAndSwap(false, true)
}

// Shed returns the total number of arrivals dropped because the limiter was at
// capacity.
func (l *inFlightLimiter) Shed() int64 {
	if l == nil {
		return 0
	}
	return l.shed.Load()
}

func openWorkloadArrivalLimit(iterationsPerWorker, workers int) (int, bool) {
	if iterationsPerWorker <= 0 || workers <= 0 {
		return 0, false
	}
	maxInt := int(^uint(0) >> 1)
	if iterationsPerWorker > maxInt/workers {
		return maxInt, true
	}
	return iterationsPerWorker * workers, true
}

func cloneSingleIterationScript(script *dsl.Script) *dsl.Script {
	if script == nil {
		return nil
	}
	clone := *script
	clone.Config = script.Config
	clone.Config.RunLogic = script.Config.RunLogic
	clone.Config.Pacing = dsl.PacingConfig{}
	clone.Config.RunLogic.Iterations = 1
	return &clone
}

func runArrivalScheduler(
	ctx context.Context,
	tps float64,
	totalArrivals int,
	waitIfPaused func(context.Context) error,
	sleepActive func(context.Context, time.Duration) bool,
	activeElapsed func() time.Duration,
	dispatch func(arrivalIndex int),
	shed func(firstArrivalIndex int, count int),
) {
	runArrivalSchedulerFromOrigin(ctx, tps, totalArrivals, waitIfPaused, sleepActive, activeElapsed, false, dispatch, shed)
}

func runArrivalSchedulerFromOrigin(
	ctx context.Context,
	tps float64,
	totalArrivals int,
	waitIfPaused func(context.Context) error,
	sleepActive func(context.Context, time.Duration) bool,
	activeElapsed func() time.Duration,
	preserveActiveOrigin bool,
	dispatch func(arrivalIndex int),
	shed func(firstArrivalIndex int, count int),
) {
	if tps <= 0 || math.IsNaN(tps) || math.IsInf(tps, 0) {
		return
	}
	tps = min(tps, float64(dsl.MaxPacingTPS))
	started := time.Now()
	if activeElapsed == nil {
		activeElapsed = func() time.Duration { return time.Since(started) }
	}
	// ActiveElapsed is shared by every group in a multi-group run and includes
	// pre-baseline plus any earlier group initialization. Anchor this scheduler
	// to its own start so those phases cannot appear as overdue arrivals and
	// trigger an artificial catch-up burst when the group starts.
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
	for arrival := 0; totalArrivals <= 0 || arrival < totalArrivals; {
		if waitIfPaused != nil {
			if err := waitIfPaused(ctx); err != nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}

		deadline := openWorkloadArrivalDeadline(arrival, tps)
		now := schedulerElapsed()
		if now < deadline {
			delay := deadline - now
			if sleepActive != nil {
				if !sleepActive(ctx, delay) {
					return
				}
			} else if !sleepContext(ctx, delay) {
				return
			}
			continue
		}

		// Credit every arrival whose absolute active-time deadline has passed.
		// This compensates for dispatcher work instead of adding that work to every
		// relative sleep interval. The burst cap makes catch-up behavior explicit
		// and bounded after long process stalls.
		dueThrough := int(math.Floor(now.Seconds()*tps + 1e-9))
		if dueThrough < arrival {
			dueThrough = arrival
		}
		due := dueThrough - arrival + 1
		if totalArrivals > 0 && due > totalArrivals-arrival {
			due = totalArrivals - arrival
		}
		dispatchCount := min(due, maxOpenWorkloadCatchUpBurst)
		for offset := 0; offset < dispatchCount; offset++ {
			if waitIfPaused != nil {
				if err := waitIfPaused(ctx); err != nil {
					return
				}
			}
			if ctx.Err() != nil {
				return
			}
			dispatch(arrival + offset)
		}
		arrival += dispatchCount
		if excess := due - dispatchCount; excess > 0 {
			if shed != nil {
				shed(arrival, excess)
			}
			arrival += excess
		}
	}
}

func openWorkloadArrivalDeadline(arrival int, tps float64) time.Duration {
	if arrival <= 0 {
		return 0
	}
	nanos := float64(arrival) * float64(time.Second) / tps
	if nanos >= float64(math.MaxInt64) {
		return time.Duration(math.MaxInt64)
	}
	if nanos <= 0 {
		return 0
	}
	return time.Duration(nanos)
}
