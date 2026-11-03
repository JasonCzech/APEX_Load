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
type QuotaPacer struct {
	mu sync.Mutex

	baseRPM       float64
	baseTPM       float64
	estTokens     float64
	maxBurstReqs  float64
	maxBurstToks  float64
	adaptiveAIMD  bool
	headroomRatio float64

	aimdFactor     float64
	reqTokens      float64
	tokTokens      float64
	lastRefill     time.Time
	cooldownUntil  time.Time
	lastAIMDWindow time.Time
	windowSuccess  int
	window429      int
}

// NewQuotaPacer constructs a QuotaPacer from a dsl.QuotaPacingConfig.
// Returns nil if neither RPM nor TPM is positive.
func NewQuotaPacer(cfg *dsl.QuotaPacingConfig) *QuotaPacer {
	if cfg == nil || (cfg.RPM <= 0 && cfg.TPM <= 0) {
		return nil
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
	rpm := float64(cfg.RPM) * hr
	tpm := float64(cfg.TPM) * hr

	burstReqs := math.Max(1.0, rpm/10.0)
	burstToks := float64(cfg.BurstTokens)
	if burstToks <= 0 {
		burstToks = math.Max(est*2, tpm/6.0)
	}
	now := time.Now()
	return &QuotaPacer{
		baseRPM:        rpm,
		baseTPM:        tpm,
		estTokens:      est,
		maxBurstReqs:   burstReqs,
		maxBurstToks:   burstToks,
		adaptiveAIMD:   cfg.AdaptiveAIMD,
		headroomRatio:  hr,
		aimdFactor:     1.0,
		reqTokens:      math.Min(burstReqs, math.Max(1.0, rpm/60.0)),
		tokTokens:      math.Min(burstToks, math.Max(est, tpm/60.0)),
		lastRefill:     now,
		lastAIMDWindow: now,
	}
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
	if qp == nil {
		return 0, true
	}
	for {
		if ctx != nil && ctx.Err() != nil {
			return 0, false
		}
		now := time.Now()
		qp.mu.Lock()
		qp.refillLocked(now)

		var waitDur time.Duration
		if now.Before(qp.cooldownUntil) {
			waitDur = qp.cooldownUntil.Sub(now)
		} else {
			if qp.baseRPM > 0 && qp.reqTokens < 1.0 {
				ratePerSec := (qp.baseRPM * qp.aimdFactor) / 60.0
				if ratePerSec > 0 {
					needSec := (1.0 - qp.reqTokens) / ratePerSec
					d := time.Duration(needSec * float64(time.Second))
					if d > waitDur {
						waitDur = d
					}
				}
			}
			if qp.baseTPM > 0 && qp.tokTokens < qp.estTokens {
				tokPerSec := (qp.baseTPM * qp.aimdFactor) / 60.0
				if tokPerSec > 0 {
					needSec := (qp.estTokens - qp.tokTokens) / tokPerSec
					d := time.Duration(needSec * float64(time.Second))
					if d > waitDur {
						waitDur = d
					}
				}
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
	if result.RetryAfter > 0 {
		until := now.Add(result.RetryAfter)
		if until.After(qp.cooldownUntil) {
			qp.cooldownUntil = until
		}
	}

	if !qp.adaptiveAIMD {
		return
	}

	if throttled {
		qp.window429++
		// Multiplicative decrease: 0.7x on 429, bounded between 0.05x and 4.0x.
		qp.aimdFactor = math.Max(0.05, qp.aimdFactor*0.7)
		qp.lastAIMDWindow = now
		qp.windowSuccess = 0
		return
	}

	if result.Success {
		qp.windowSuccess++
	}
	// Check if server reports low remaining tokens (<10% of TPM).
	lowRemaining := false
	if llmDiag != nil && llmDiag.RateLimitRemainingTokens != nil && qp.baseTPM > 0 {
		if float64(*llmDiag.RateLimitRemainingTokens) < 0.10*qp.baseTPM {
			lowRemaining = true
		}
	}

	// Additive increase (+5%) when a window of requests completes with zero 429s and adequate headroom.
	if qp.window429 == 0 && !lowRemaining && (qp.windowSuccess >= 5 || now.Sub(qp.lastAIMDWindow) >= time.Second) && qp.windowSuccess > 0 {
		qp.aimdFactor = math.Min(4.0, qp.aimdFactor+0.05)
		qp.lastAIMDWindow = now
		qp.windowSuccess = 0
	} else if now.Sub(qp.lastAIMDWindow) >= time.Second {
		qp.window429 = 0
		qp.lastAIMDWindow = now
	}
}
