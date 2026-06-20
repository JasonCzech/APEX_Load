package engine

import (
	"context"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func isOpenWorkload(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	return strings.EqualFold(script.Config.RunLogic.WorkloadModel, "open") && script.Config.Pacing.Type == "tps" && script.Config.Pacing.TPS > 0
}

// defaultOpenWorkloadInFlightPerTPS bounds the live arrival-worker set, in the
// absence of an explicit RunLogic.MaxInFlight, to this many seconds' worth of
// arrivals at the offered TPS. Steady-state in-flight ≈ TPS × mean-iteration-
// duration, so a run whose iterations complete in well under this window never
// sheds; only a saturating/degrading SUT (completions falling behind arrivals)
// hits the cap. This keeps a healthy run unthrottled while preventing the live
// goroutine set from growing without limit and OOM-ing the generator.
const defaultOpenWorkloadInFlightPerTPS = 60

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
		return explicit
	}
	tps := script.Config.Pacing.TPS
	if tps <= 0 {
		return 0
	}
	limit := int(math.Ceil(tps)) * defaultOpenWorkloadInFlightPerTPS
	if limit < defaultOpenWorkloadInFlightPerTPS {
		limit = defaultOpenWorkloadInFlightPerTPS
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
		l.shed.Add(1)
		return false
	}
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

func runArrivalScheduler(ctx context.Context, tps float64, totalArrivals int, waitIfPaused func(context.Context) error, dispatch func(arrivalIndex int)) {
	if tps <= 0 {
		return
	}
	interval := time.Duration(float64(time.Second) / tps)
	if interval <= 0 {
		interval = time.Nanosecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for arrival := 0; totalArrivals <= 0 || arrival < totalArrivals; arrival++ {
		if arrival > 0 {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
		if waitIfPaused != nil {
			if err := waitIfPaused(ctx); err != nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		dispatch(arrival)
	}
}
