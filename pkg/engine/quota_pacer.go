package engine

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// QuotaPacer enforces dual RPM (requests-per-minute) and TPM (tokens-per-minute)
// token-bucket pacing with upfront token reservation, post-response reconciliation,
// and optional AIMD (additive-increase / multiplicative-decrease) rate adaptation.
//
// A quota belongs to the deployment, not to a virtual user: one pacer serves a
// whole group on a generator (shared by its closed, open-model, parallel-branch
// and async users) and paces that generator's share of the quota. It paces
// only LLM actions.
type QuotaPacer struct {
	mu sync.Mutex

	baseRPM       float64
	baseTPM       float64
	deploymentTPM float64 // the whole deployment's TPM, for its remaining-tokens header
	estTokens     float64
	maxBurstReqs  float64
	maxBurstToks  float64
	adaptiveAIMD  bool
	headroomRatio float64
	retryAfterCap time.Duration

	aimdFactor     float64
	reqTokens      float64
	tokTokens      float64
	lastRefill     time.Time
	cooldownUntil  time.Time
	lastAIMDWindow time.Time
	lastDecrease   time.Time
	windowSuccess  int
	window429      int
	windowLimited  bool // the buckets held a request back in this window
	paused         bool

	// gate lets one waiter at a time poll the buckets; the others queue on
	// it, so wakeups grow with the admission rate, not waiters times rate.
	gate chan struct{}
	// pauseSignal is closed when the pacer pauses (and replaced when it
	// resumes), waking bypassing waiters queued on the gate.
	pauseSignal chan struct{}
}

// quotaAIMDWindow is the AIMD control interval. The pacer is shared by a
// whole group, so it hears every in-flight response: it decreases at most once
// and increases at most once per window, so its rate of change does not scale
// with the group's concurrency or throughput.
const quotaAIMDWindow = time.Second

// NewQuotaPacer constructs a QuotaPacer from a dsl.QuotaPacingConfig. With
// shardCount > 1 the run is split across that many generators and this pacer
// paces 1/shardCount of the quota. retryAfterCap bounds the cooldown a
// Retry-After header can impose (as for retries); zero means the default.
// Returns nil if neither RPM nor TPM is positive.
func NewQuotaPacer(cfg *dsl.QuotaPacingConfig, shardCount uint32, retryAfterCap time.Duration) *QuotaPacer {
	return newQuotaPacerForShard(cfg, DataShardConfig{Count: shardCount}, retryAfterCap)
}

// newQuotaPacerForShard is NewQuotaPacer for one generator's shard: its
// ordinal staggers when the generator's first request is admitted.
func newQuotaPacerForShard(cfg *dsl.QuotaPacingConfig, shard DataShardConfig, retryAfterCap time.Duration) *QuotaPacer {
	if cfg == nil || (cfg.RPM <= 0 && cfg.TPM <= 0) {
		return nil
	}
	share := 1.0
	if shard.Count > 1 {
		share = 1 / float64(shard.Count)
	}
	if retryAfterCap <= 0 {
		retryAfterCap = defaultRetryAfterCap
	}
	hr := cfg.HeadroomRatio
	if hr <= 0 || hr > 1 || math.IsNaN(hr) || math.IsInf(hr, 0) {
		hr = 1.0
	}
	est := float64(cfg.EstimatedTokensPerRequest)
	if est <= 0 {
		est = float64(cfg.DefaultTokens)
	}
	if est <= 0 {
		est = 1000
	}
	rpm := float64(cfg.RPM) * hr * share
	tpm := float64(cfg.TPM) * hr * share

	// Buckets hold this generator's share with no per-generator floor (a
	// floor multiplies by the generator count). A request is admitted once
	// a bucket holds its cost or is full, and a bucket smaller than one
	// request's cost runs into debt, so the long-run rate stays exact.
	burstReqs := rpm / 10.0
	burstToks := float64(cfg.BurstTokens) * share
	if burstToks <= 0 {
		burstToks = tpm / 6.0
	}
	// A bucket starts holding one request's cost, so the first request is
	// admitted at once. Generator k of N starts k/N of a request behind, so
	// the generators of a fleet admit in turn instead of in lockstep.
	lag := 0.0
	if shard.Count > 1 && shard.Ordinal < shard.Count {
		lag = float64(shard.Ordinal) / float64(shard.Count)
	}
	now := time.Now()
	return &QuotaPacer{
		baseRPM:        rpm,
		baseTPM:        tpm,
		deploymentTPM:  float64(cfg.TPM) * hr,
		estTokens:      est,
		maxBurstReqs:   burstReqs,
		maxBurstToks:   burstToks,
		adaptiveAIMD:   cfg.AdaptiveAIMD,
		headroomRatio:  hr,
		retryAfterCap:  retryAfterCap,
		aimdFactor:     1.0,
		reqTokens:      math.Min(1.0, burstReqs) * (1 - lag),
		tokTokens:      math.Min(est, burstToks) * (1 - lag),
		lastRefill:     now,
		lastAIMDWindow: now,
		gate:           make(chan struct{}, 1),
		pauseSignal:    make(chan struct{}),
	}
}

// newGroupQuotaPacer builds the one quota pacer of a group on this generator,
// or nil when the script sets no quota.
func newGroupQuotaPacer(script *dsl.Script, shard DataShardConfig) *QuotaPacer {
	if script == nil || script.Config.Pacing.Quota == nil {
		return nil
	}
	return newQuotaPacerForShard(script.Config.Pacing.Quota, shard, retryAfterCap(script.Config.ErrorHandling))
}

// SetPaused holds admissions while the run is paused: requests already queued
// for quota wait instead of starting. Paused time earns no credit.
func (qp *QuotaPacer) SetPaused(paused bool) {
	if qp == nil {
		return
	}
	qp.mu.Lock()
	defer qp.mu.Unlock()
	if qp.paused == paused {
		return
	}
	qp.refillLocked(time.Now())
	qp.paused = paused
	if paused && qp.pauseSignal != nil {
		close(qp.pauseSignal)
	} else if !paused {
		qp.pauseSignal = make(chan struct{})
	}
}

// waitLLMQuota waits for the group's quota before an LLM action; any other
// action is not paced. ok is false when ctx ends first.
func (vu *VUser) waitLLMQuota(ctx context.Context, action *protocol.Action) (reserved int, ok bool) {
	return vu.waitLLMQuotaBypass(ctx, action, vu.quotaPauseBypass)
}

// waitLLMQuotaBypass is waitLLMQuota with the teardown bypass given, for a
// goroutine that must not read the VUser's own field.
func (vu *VUser) waitLLMQuotaBypass(ctx context.Context, action *protocol.Action, bypassPause bool) (reserved int, ok bool) {
	if vu.QuotaPacer == nil || !protocol.IsLLMAction(action) {
		return 0, true
	}
	return vu.QuotaPacer.wait(ctx, bypassPause)
}

// noteQuotaWaitIntervals records quota-wait intervals for the enclosing
// concurrent work, up to maxTrackedQuotaWaits; a wait past the cap counts as
// busy time there.
func (vu *VUser) noteQuotaWaitIntervals(intervals ...timeInterval) {
	for _, interval := range intervals {
		if len(vu.llmQuotaWaitIntervals) >= maxTrackedQuotaWaits {
			return
		}
		vu.llmQuotaWaitIntervals = append(vu.llmQuotaWaitIntervals, interval)
	}
}

// maxTrackedQuotaWaits bounds the quota-wait intervals one executor or branch
// records.
const maxTrackedQuotaWaits = 256

// quotaWaitSpan is one concurrent unit of work (a concurrent child, a
// parallel branch): when it ran and when, within that, it queued for quota.
type quotaWaitSpan struct {
	start, end time.Time
	waits      []timeInterval
}

// idleQuotaTime is the time within concurrent work when some unit queued for
// quota and none was busy.
func idleQuotaTime(spans []quotaWaitSpan) time.Duration {
	var idle time.Duration
	for _, window := range idleQuotaWindows(spans) {
		idle += window.end.Sub(window.start)
	}
	return idle
}

// idleQuotaWindows are the windows within concurrent work when some unit
// queued for quota and none was busy: the union of the waits less the union
// of the units' busy time (each unit's span outside its own waits).
func idleQuotaWindows(spans []quotaWaitSpan) []timeInterval {
	var waits, busy []timeInterval
	for _, span := range spans {
		if span.start.IsZero() || !span.end.After(span.start) {
			continue
		}
		cursor := span.start
		for _, w := range mergeTimeIntervals(append([]timeInterval(nil), span.waits...)) {
			start, end := maxTime(w.start, span.start), minTime(w.end, span.end)
			if !end.After(start) {
				continue
			}
			waits = append(waits, timeInterval{start, end})
			if start.After(cursor) {
				busy = append(busy, timeInterval{cursor, start})
			}
			cursor = maxTime(cursor, end)
		}
		if span.end.After(cursor) {
			busy = append(busy, timeInterval{cursor, span.end})
		}
	}
	waits, busy = mergeTimeIntervals(waits), mergeTimeIntervals(busy)
	var idle []timeInterval
	next := 0
	for _, w := range waits {
		cursor := w.start
		for next < len(busy) && !busy[next].end.After(cursor) {
			next++
		}
		for _, b := range busy[next:] {
			if !b.start.Before(w.end) {
				break
			}
			if b.start.After(cursor) {
				idle = append(idle, timeInterval{cursor, b.start})
			}
			cursor = maxTime(cursor, b.end)
		}
		if w.end.After(cursor) {
			idle = append(idle, timeInterval{cursor, w.end})
		}
	}
	return idle
}

// takeLLMQuotaWait returns the time the last action spent queued for quota
// and excludes it, like think time, from the open source transactions and the
// iteration's elapsed time: the wait is the client pacing itself, not the
// system under test responding.
func (vu *VUser) takeLLMQuotaWait() time.Duration {
	wait := vu.llmQuotaWait
	vu.llmQuotaWait = 0
	if wait > 0 {
		vu.noteSourceTransactionThinkTime(wait)
		vu.iterationThinkTime += wait
	}
	return wait
}

// observeLLMQuota reconciles an LLM action's reservation with its result.
func (vu *VUser) observeLLMQuota(action *protocol.Action, reserved int, result *protocol.Result) {
	if vu.QuotaPacer == nil || !protocol.IsLLMAction(action) {
		return
	}
	vu.QuotaPacer.ObserveResult(reserved, result)
}

// EffectiveRPM returns the current AIMD-adjusted RPM target.
func (qp *QuotaPacer) EffectiveRPM() float64 {
	if qp == nil {
		return 0
	}
	qp.mu.Lock()
	defer qp.mu.Unlock()
	return qp.baseRPM * qp.aimdFactor
}

// EffectiveTPM returns the current AIMD-adjusted TPM target.
func (qp *QuotaPacer) EffectiveTPM() float64 {
	if qp == nil {
		return 0
	}
	qp.mu.Lock()
	defer qp.mu.Unlock()
	return qp.baseTPM * qp.aimdFactor
}

func (qp *QuotaPacer) refillLocked(now time.Time) {
	elapsed := now.Sub(qp.lastRefill).Seconds()
	if elapsed <= 0 {
		return
	}
	qp.lastRefill = now
	if qp.paused {
		return
	}
	if qp.baseRPM > 0 {
		ratePerSec := (qp.baseRPM * qp.aimdFactor) / 60.0
		qp.reqTokens = math.Min(qp.maxBurstReqs, qp.reqTokens+elapsed*ratePerSec)
	}
	if qp.baseTPM > 0 {
		tokPerSec := (qp.baseTPM * qp.aimdFactor) / 60.0
		qp.tokTokens = math.Min(qp.maxBurstToks, qp.tokTokens+elapsed*tokPerSec)
	}
}

// Wait reserves 1 request credit and estTokens token credits, sleeping until both
// buckets and any Retry-After cooldown permit admission or ctx is cancelled.
func (qp *QuotaPacer) Wait(ctx context.Context) (reservedTokens int, ok bool) {
	return qp.wait(ctx, false)
}

// admitPausedBypass admits a bypassing caller at once while the pacer is
// paused, reserving its credit as an ordinary admission does; otherwise it
// returns the signal that a pause has begun.
func (qp *QuotaPacer) admitPausedBypass() (reservedTokens int, ok bool, pauseBegins <-chan struct{}) {
	qp.mu.Lock()
	defer qp.mu.Unlock()
	if !qp.paused {
		return 0, false, qp.pauseSignal
	}
	qp.refillLocked(time.Now())
	if qp.baseRPM > 0 {
		qp.reqTokens -= 1.0
	}
	if qp.baseTPM > 0 {
		qp.tokTokens -= qp.estTokens
	}
	return int(qp.estTokens), true, nil
}

// wait is Wait; with bypassPause a paused pacer admits at once (teardown must
// not wait out a pause), reserving its credit so pacing absorbs the debt.
func (qp *QuotaPacer) wait(ctx context.Context, bypassPause bool) (reservedTokens int, ok bool) {
	if qp == nil {
		return 0, true
	}
	if qp.gate != nil {
		var done <-chan struct{}
		if ctx != nil {
			done = ctx.Done()
		}
		for acquired := false; !acquired; {
			// A bypassing caller is admitted while the pacer is paused, even
			// when a paused waiter holds the gate. It keeps its place in the
			// gate's queue until a pause begins.
			var pauseBegins <-chan struct{}
			if bypassPause {
				reserved, ok, signal := qp.admitPausedBypass()
				if ok {
					return reserved, true
				}
				pauseBegins = signal
			}
			select {
			case qp.gate <- struct{}{}:
				acquired = true
			case <-done:
				return 0, false
			case <-pauseBegins:
			}
		}
		defer func() { <-qp.gate }()
	}
	for {
		if ctx != nil && ctx.Err() != nil {
			return 0, false
		}
		now := time.Now()
		qp.mu.Lock()
		qp.refillLocked(now)

		var waitDur time.Duration
		if qp.paused && bypassPause {
			waitDur = 0
		} else if qp.paused {
			waitDur = 250 * time.Millisecond
		} else if now.Before(qp.cooldownUntil) {
			waitDur = qp.cooldownUntil.Sub(now)
		} else {
			reqNeed := math.Min(1.0, qp.maxBurstReqs)
			if qp.baseRPM > 0 && qp.reqTokens < reqNeed {
				ratePerSec := (qp.baseRPM * qp.aimdFactor) / 60.0
				if ratePerSec > 0 {
					needSec := (reqNeed - qp.reqTokens) / ratePerSec
					d := time.Duration(needSec * float64(time.Second))
					if d > waitDur {
						waitDur = d
					}
				}
			}
			// A request is admitted once the bucket holds its estimate, or
			// is full when the estimate exceeds the burst (the bucket can
			// never hold more): the reservation then runs the bucket into
			// debt that later requests wait out, instead of waiting forever.
			need := math.Min(qp.estTokens, qp.maxBurstToks)
			if qp.baseTPM > 0 && qp.tokTokens < need {
				tokPerSec := (qp.baseTPM * qp.aimdFactor) / 60.0
				if tokPerSec > 0 {
					needSec := (need - qp.tokTokens) / tokPerSec
					d := time.Duration(needSec * float64(time.Second))
					if d > waitDur {
						waitDur = d
					}
				}
			}
			if waitDur > 0 {
				qp.windowLimited = true
			}
		}

		if waitDur <= 0 {
			if qp.baseRPM > 0 {
				qp.reqTokens -= 1.0
			}
			resTok := int(qp.estTokens)
			if qp.baseTPM > 0 {
				qp.tokTokens -= qp.estTokens
			}
			qp.mu.Unlock()
			return resTok, true
		}
		qp.mu.Unlock()

		if waitDur > 250*time.Millisecond {
			waitDur = 250 * time.Millisecond
		}
		if waitDur < time.Millisecond {
			waitDur = time.Millisecond
		}
		if _, completed := sleepCtxElapsed(ctx, waitDur); !completed {
			return 0, false
		}
	}
}

// ObserveResult reconciles actual token consumption against the upfront reservation
// and updates AIMD pacing multiplier based on 429 responses and x-ratelimit headers.
func (qp *QuotaPacer) ObserveResult(reservedTokens int, result *protocol.Result) {
	if qp == nil || result == nil {
		return
	}
	now := time.Now()
	qp.mu.Lock()
	defer qp.mu.Unlock()
	qp.refillLocked(now)

	var llmDiag *protocol.LLMDiagnostics
	if result.ServerTelemetry != nil {
		llmDiag = result.ServerTelemetry.LLM
	}

	// Reconcile actual tokens vs reserved estimate.
	if qp.baseTPM > 0 && llmDiag != nil && llmDiag.TotalTokens > 0 {
		diff := float64(llmDiag.TotalTokens - reservedTokens)
		qp.tokTokens -= diff
		if qp.tokTokens > qp.maxBurstToks {
			qp.tokTokens = qp.maxBurstToks
		}
		// Smoothly update estimated tokens per request via EWMA so reservations track reality.
		qp.estTokens = 0.8*qp.estTokens + 0.2*float64(llmDiag.TotalTokens)
		if qp.estTokens < 1 {
			qp.estTokens = 1
		}
	}

	throttled := result.StatusCode == 429 || (llmDiag != nil && llmDiag.Throttled)
	// Retry-After asks for a cooldown only on a throttle or an unavailable
	// server, and never longer than the retry path would wait.
	if result.RetryAfter > 0 && (throttled || result.StatusCode == 503) {
		until := now.Add(min(result.RetryAfter, qp.retryAfterCap))
		if until.After(qp.cooldownUntil) {
			qp.cooldownUntil = until
		}
	}

	if !qp.adaptiveAIMD {
		return
	}

	if throttled {
		qp.window429++
		// Multiplicative decrease (0.7x, floor 0.05x), at most once per
		// window: a burst of 429s across the group's in-flight requests is
		// one congestion signal. The window restarts, so the next increase
		// needs a full window free of 429s.
		if now.Sub(qp.lastDecrease) >= quotaAIMDWindow {
			qp.aimdFactor = math.Max(0.05, qp.aimdFactor*0.7)
			qp.lastDecrease = now
		}
		qp.lastAIMDWindow = now
		qp.windowSuccess = 0
		qp.windowLimited = false
		return
	}
	if now.Before(qp.cooldownUntil) {
		// Nothing is admitted during a cooldown, so these results come from
		// requests admitted before the throttle and say nothing about the
		// reduced rate: hold the factor and restart the window.
		qp.lastAIMDWindow = now
		qp.windowSuccess = 0
		qp.window429 = 0
		qp.windowLimited = false
		return
	}

	if result.Success {
		qp.windowSuccess++
	}
	// The deployment reports its own remaining tokens: low means under 10%
	// of the whole deployment's TPM, not of this generator's share.
	lowRemaining := false
	if llmDiag != nil && llmDiag.RateLimitRemainingTokens != nil && qp.deploymentTPM > 0 {
		if float64(*llmDiag.RateLimitRemainingTokens) < 0.10*qp.deploymentTPM {
			lowRemaining = true
		}
	}
	if lowRemaining {
		qp.window429++
	}

	// Additive increase (+5%, cap 4.0x) once per window that had successes,
	// no throttle or low-remaining signal, and in which the pacer held
	// requests back (raising a limit nothing reaches is not probing):
	// probing climbs 5% per second whatever the group's throughput.
	if now.Sub(qp.lastAIMDWindow) >= quotaAIMDWindow {
		if qp.window429 == 0 && qp.windowSuccess > 0 && qp.windowLimited {
			qp.aimdFactor = math.Min(4.0, qp.aimdFactor+0.05)
		}
		qp.lastAIMDWindow = now
		qp.windowSuccess = 0
		qp.window429 = 0
		qp.windowLimited = false
	}
}
