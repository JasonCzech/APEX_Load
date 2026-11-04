package plan

import (
	"fmt"
	"math"
	"sort"

	"github.com/jasonczech/apexload/pkg/llmperf/econ"
)

// StepObservation captures one load step's offered load, throughput, and tail latency.
type StepObservation struct {
	StepIndex        int      `json:"step_index"`
	Name             string   `json:"name,omitempty"`
	OfferedTPS       float64  `json:"offered_tps"`
	AchievedTPS      float64  `json:"achieved_tps"`
	GoodputTPS       float64  `json:"goodput_tps"`
	SystemOutputTPS  float64  `json:"system_output_tps"`
	TTFTP50Ms        float64  `json:"ttft_p50_ms"`
	TTFTP95Ms        float64  `json:"ttft_p95_ms"`
	TTFTP99Ms        float64  `json:"ttft_p99_ms"`
	TPOTP50Ms        float64  `json:"tpot_p50_ms"`
	TPOTP95Ms        float64  `json:"tpot_p95_ms"`
	TPOTP99Ms        float64  `json:"tpot_p99_ms"`
	ErrorRatePct     float64  `json:"error_rate_pct"`
	Throttled429Pct  float64  `json:"throttled_429_pct"`
	SLABreached      bool     `json:"sla_breached"`
	SLABreachReasons []string `json:"sla_breach_reasons,omitempty"`
}

// SLAPolicy defines the goodput acceptance bounds for capacity planning.
type SLAPolicy struct {
	MaxTTFTP95Ms    float64 `json:"max_ttft_p95_ms,omitempty"`
	MaxTPOTP95Ms    float64 `json:"max_tpot_p95_ms,omitempty"`
	MaxErrorRatePct float64 `json:"max_error_rate_pct,omitempty"`
}

// Request is the input to the LLM capacity planner.
type Request struct {
	Steps                 []StepObservation    `json:"steps"`
	SLA                   SLAPolicy            `json:"sla"`
	TargetPeakTPS         float64              `json:"target_peak_tps"`
	TargetOutputTPS       float64              `json:"target_output_tps,omitempty"`
	TargetUsers           int                  `json:"target_users,omitempty"`
	RequestsPerUserPerMin float64              `json:"requests_per_user_per_min,omitempty"`
	MeanE2EMs             float64              `json:"mean_e2e_ms,omitempty"`
	HeadroomPct           float64              `json:"headroom_pct,omitempty"` // default 20%
	CurrentReplicas       int                  `json:"current_replicas,omitempty"`
	Deployment            *econ.DeploymentSpec `json:"deployment,omitempty"`
}

// Result is the capacity plan recommendation.
type Result struct {
	EvaluatedSteps               []StepObservation `json:"evaluated_steps"`
	KneeStepIndex                int               `json:"knee_step_index"`
	KneeTPS                      float64           `json:"knee_tps"`
	MaxGoodputTPS                float64           `json:"max_goodput_tps"`
	MaxGoodputTPSPerReplica      float64           `json:"max_goodput_tps_per_replica"`
	MaxSystemOutputTPSPerReplica float64           `json:"max_system_output_tps_per_replica"`
	CurrentReplicas              int               `json:"current_replicas"`
	RequiredReplicas             int               `json:"required_replicas"`
	RequiredConcurrency          int               `json:"required_concurrency,omitempty"`
	RequiredGPUs                 int               `json:"required_gpus,omitempty"`
	RequiredPTUs                 int               `json:"required_ptus,omitempty"`
	Extrapolated                 bool              `json:"extrapolated,omitempty"`
	HeadroomPct                  float64           `json:"headroom_pct"`
	Bottleneck                   string            `json:"bottleneck"` // "prefill_ttft", "decode_tpot", "error_rate", "rate_limit_429", "none"
	ProjectedHourlyCostUSD       float64           `json:"projected_hourly_cost_usd,omitempty"`
	ProjectedMonthlyCostUSD      float64           `json:"projected_monthly_cost_usd,omitempty"`
	Recommendation               string            `json:"recommendation"`
}

// Evaluate analyzes a capacity sweep and computes the knee and required replicas.
func Evaluate(req Request) (*Result, error) {
	if len(req.Steps) == 0 {
		return nil, fmt.Errorf("at least one step observation is required")
	}
	replicas := req.CurrentReplicas
	if replicas <= 0 {
		replicas = 1
	}
	headroom := req.HeadroomPct
	if headroom <= 0 || headroom >= 95 {
		headroom = 20.0
	}
	maxErr := req.SLA.MaxErrorRatePct
	if maxErr <= 0 {
		maxErr = 1.0
	}

	steps := make([]StepObservation, len(req.Steps))
	copy(steps, req.Steps)
	sort.SliceStable(steps, func(i, j int) bool {
		if steps[i].OfferedTPS != steps[j].OfferedTPS {
			return steps[i].OfferedTPS < steps[j].OfferedTPS
		}
		return steps[i].StepIndex < steps[j].StepIndex
	})

	var maxGoodput, maxSysOut float64
	kneeIdx := -1
	var kneeTPS float64
	bottleneck := "none"

	for i := range steps {
		st := &steps[i]
		var reasons []string
		if req.SLA.MaxTTFTP95Ms > 0 && st.TTFTP95Ms > req.SLA.MaxTTFTP95Ms {
			reasons = append(reasons, fmt.Sprintf("TTFT P95 %.1fms > %.1fms", st.TTFTP95Ms, req.SLA.MaxTTFTP95Ms))
			if bottleneck == "none" {
				bottleneck = "prefill_ttft"
			}
		}
		if req.SLA.MaxTPOTP95Ms > 0 && st.TPOTP95Ms > req.SLA.MaxTPOTP95Ms {
			reasons = append(reasons, fmt.Sprintf("TPOT P95 %.1fms > %.1fms", st.TPOTP95Ms, req.SLA.MaxTPOTP95Ms))
			if bottleneck == "none" {
				bottleneck = "decode_tpot"
			}
		}
		if st.ErrorRatePct > maxErr {
			reasons = append(reasons, fmt.Sprintf("error rate %.2f%% > %.2f%%", st.ErrorRatePct, maxErr))
			if bottleneck == "none" {
				if st.Throttled429Pct > 0 {
					bottleneck = "rate_limit_429"
				} else {
					bottleneck = "error_rate"
				}
			}
		}
		st.SLABreached = len(reasons) > 0
		st.SLABreachReasons = reasons
		if st.GoodputTPS <= 0 && !st.SLABreached {
			st.GoodputTPS = st.AchievedTPS
		}
		if !st.SLABreached {
			if st.GoodputTPS > maxGoodput {
				maxGoodput = st.GoodputTPS
			}
			if st.SystemOutputTPS > maxSysOut {
				maxSysOut = st.SystemOutputTPS
			}
			kneeIdx = st.StepIndex
			kneeTPS = st.AchievedTPS
		}
	}

	// Also detect latency-slope inflection (>2.5x initial slope) if no SLA was breached.
	if kneeIdx == steps[len(steps)-1].StepIndex && len(steps) >= 3 {
		baseSlope := (steps[1].TTFTP95Ms - steps[0].TTFTP95Ms) / math.Max(0.1, steps[1].AchievedTPS-steps[0].AchievedTPS)
		if baseSlope > 0 {
			for i := 2; i < len(steps); i++ {
				slope := (steps[i].TTFTP95Ms - steps[i-1].TTFTP95Ms) / math.Max(0.1, steps[i].AchievedTPS-steps[i-1].AchievedTPS)
				if slope > baseSlope*2.5 {
					kneeIdx = steps[i-1].StepIndex
					kneeTPS = steps[i-1].AchievedTPS
					break
				}
			}
		}
	}

	if maxGoodput <= 0 {
		// All steps breached SLA; fall back to lowest step's achieved TPS * 0.5 as conservative ceiling.
		maxGoodput = math.Max(0.1, steps[0].AchievedTPS*0.5)
		kneeIdx = steps[0].StepIndex
		kneeTPS = steps[0].AchievedTPS
	}
	perReplicaTPS := maxGoodput / float64(replicas)
	perReplicaOutTPS := maxSysOut / float64(replicas)
	usableFactor := 1.0 - (headroom / 100.0)

	targetPeakTPS := req.TargetPeakTPS
	if targetPeakTPS <= 0 && req.TargetUsers > 0 && req.RequestsPerUserPerMin > 0 {
		targetPeakTPS = float64(req.TargetUsers) * req.RequestsPerUserPerMin / 60.0
	}

	var maxTestedTPS float64
	for _, st := range steps {
		if st.OfferedTPS > maxTestedTPS {
			maxTestedTPS = st.OfferedTPS
		}
		if st.AchievedTPS > maxTestedTPS {
			maxTestedTPS = st.AchievedTPS
		}
	}
	extrapolated := targetPeakTPS > maxTestedTPS*1.05 || req.TargetOutputTPS > maxSysOut*1.05

	var requiredConcurrency int
	meanE2EMs := req.MeanE2EMs
	if meanE2EMs <= 0 && len(steps) > 0 {
		// Estimate mean E2E from TTFT P50 + 64 tokens * TPOT P50.
		meanE2EMs = steps[0].TTFTP50Ms + 64.0*steps[0].TPOTP50Ms
	}
	if targetPeakTPS > 0 && meanE2EMs > 0 {
		requiredConcurrency = int(math.Ceil(targetPeakTPS * (meanE2EMs / 1000.0)))
	}

	required := 1
	if targetPeakTPS > 0 && perReplicaTPS*usableFactor > 0 {
		r := int(math.Ceil(targetPeakTPS / (perReplicaTPS * usableFactor)))
		if r > required {
			required = r
		}
	}
	if req.TargetOutputTPS > 0 && perReplicaOutTPS*usableFactor > 0 {
		r := int(math.Ceil(req.TargetOutputTPS / (perReplicaOutTPS * usableFactor)))
		if r > required {
			required = r
		}
	}

	var hourlyUSD, monthlyUSD float64
	var requiredGPUs, requiredPTUs int
	if req.Deployment != nil {
		if req.Deployment.GPUCount > 0 {
			requiredGPUs = required * req.Deployment.GPUCount
		}
		if req.Deployment.PTUCount > 0 {
			requiredPTUs = required * req.Deployment.PTUCount
		}
		eval := econ.Evaluate(*req.Deployment, econ.RunObservation{
			DurationSeconds: 3600,
			Requests:        int64(targetPeakTPS * 3600),
			GoodputRequests: int64(targetPeakTPS * 3600),
			OutputTokens:    int64(req.TargetOutputTPS * 3600),
		})
		if req.Deployment.BillingMode == "gpu_hourly" || req.Deployment.BillingMode == "ptu_hourly" {
			hourlyUSD = eval.HourlyInfraCostUSD * float64(required)
		} else {
			hourlyUSD = eval.HourlyInfraCostUSD
		}
		monthlyUSD = hourlyUSD * 730.0
	}

	rec := fmt.Sprintf(
		"Max SLA-compliant goodput is %.2f req/s (%.2f req/s per replica). To serve %.2f peak req/s with %.0f%% headroom, provision %d replica(s) (limiting factor: %s).",
		maxGoodput, perReplicaTPS, targetPeakTPS, headroom, required, bottleneck,
	)

	return &Result{
		EvaluatedSteps:               steps,
		KneeStepIndex:                kneeIdx,
		KneeTPS:                      kneeTPS,
		MaxGoodputTPS:                maxGoodput,
		MaxGoodputTPSPerReplica:      perReplicaTPS,
		MaxSystemOutputTPSPerReplica: perReplicaOutTPS,
		CurrentReplicas:              replicas,
		RequiredReplicas:             required,
		RequiredConcurrency:          requiredConcurrency,
		RequiredGPUs:                 requiredGPUs,
		RequiredPTUs:                 requiredPTUs,
		Extrapolated:                 extrapolated,
		HeadroomPct:                  headroom,
		Bottleneck:                   bottleneck,
		ProjectedHourlyCostUSD:       math.Round(hourlyUSD*100) / 100,
		ProjectedMonthlyCostUSD:      math.Round(monthlyUSD*100) / 100,
		Recommendation:               rec,
	}, nil
}
