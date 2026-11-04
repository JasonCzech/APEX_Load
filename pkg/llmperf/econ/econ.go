package econ

import (
	"math"
	"strings"
)

// DeploymentSpec describes the infrastructure or token pricing of an LLM deployment.
type DeploymentSpec struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	Model                string  `json:"model,omitempty"`
	BillingMode          string  `json:"billing_mode"` // "gpu_hourly", "token_api", "ptu_hourly"
	GPUCount             int     `json:"gpu_count,omitempty"`
	GPUCostPerHourUSD    float64 `json:"gpu_cost_per_hour_usd,omitempty"`
	PTUCount             int     `json:"ptu_count,omitempty"`
	PTUCostPerHourUSD    float64 `json:"ptu_cost_per_hour_usd,omitempty"`
	InputCostPer1MUSD    float64 `json:"input_cost_per_1m_usd,omitempty"`
	CachedInputCost1MUSD float64 `json:"cached_input_cost_1m_usd,omitempty"`
	OutputCostPer1MUSD   float64 `json:"output_cost_per_1m_usd,omitempty"`
	AsOfDate             string  `json:"as_of_date,omitempty"`
}

// RunObservation holds the token and request counts observed during a run or stage.
type RunObservation struct {
	DurationSeconds float64 `json:"duration_seconds"`
	Requests        int64   `json:"requests"`
	GoodputRequests int64   `json:"goodput_requests"`
	PromptTokens    int64   `json:"prompt_tokens"`
	CachedTokens    int64   `json:"cached_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	SystemOutputTPS float64 `json:"system_output_tps"`
}

// Summary is the computed unit-economics breakdown for an LLM run.
type Summary struct {
	DeploymentID                      string  `json:"deployment_id,omitempty"`
	DeploymentName                    string  `json:"deployment_name,omitempty"`
	BillingMode                       string  `json:"billing_mode"`
	TotalRunCostUSD                   float64 `json:"total_run_cost_usd"`
	HourlyInfraCostUSD                float64 `json:"hourly_infra_cost_usd,omitempty"`
	CostPer1MOutputTokensUSD          float64 `json:"cost_per_1m_output_tokens_usd"`
	CostPer1MInputTokensUSD           float64 `json:"cost_per_1m_input_tokens_usd"`
	CostPer1KRequestsUSD              float64 `json:"cost_per_1k_requests_usd"`
	CostPer1KGoodputRequestsUSD       float64 `json:"cost_per_1k_goodput_requests_usd"`
	EffectiveOutputTokensPerGPUSecond float64 `json:"effective_output_tokens_per_gpu_second,omitempty"`
	PrefixCacheSavingsUSD             float64 `json:"prefix_cache_savings_usd,omitempty"`
}

// Evaluate computes the unit-economics Summary for obs under spec.
func Evaluate(spec DeploymentSpec, obs RunObservation) Summary {
	mode := strings.ToLower(strings.TrimSpace(spec.BillingMode))
	if mode == "" {
		if spec.GPUCount > 0 && spec.GPUCostPerHourUSD > 0 {
			mode = "gpu_hourly"
		} else if spec.PTUCount > 0 && spec.PTUCostPerHourUSD > 0 {
			mode = "ptu_hourly"
		} else {
			mode = "token_api"
		}
	}

	durSec := obs.DurationSeconds
	if durSec <= 0 && obs.SystemOutputTPS > 0 && obs.OutputTokens > 0 {
		durSec = float64(obs.OutputTokens) / obs.SystemOutputTPS
	}
	hours := durSec / 3600.0

	var totalCost, hourlyInfra, cacheSavings float64
	switch mode {
	case "gpu_hourly":
		gpus := max(1, spec.GPUCount)
		hourlyInfra = float64(gpus) * spec.GPUCostPerHourUSD
		totalCost = hourlyInfra * hours
	case "ptu_hourly":
		ptus := max(1, spec.PTUCount)
		hourlyInfra = float64(ptus) * spec.PTUCostPerHourUSD
		totalCost = hourlyInfra * hours
	default: // token_api
		uncachedPrompt := max(int64(0), obs.PromptTokens-obs.CachedTokens)
		cachedRate := spec.CachedInputCost1MUSD
		if cachedRate <= 0 && spec.InputCostPer1MUSD > 0 {
			cachedRate = spec.InputCostPer1MUSD * 0.5
		}
		inCost := (float64(uncachedPrompt)/1e6)*spec.InputCostPer1MUSD + (float64(obs.CachedTokens)/1e6)*cachedRate
		outCost := (float64(obs.OutputTokens) / 1e6) * spec.OutputCostPer1MUSD
		totalCost = inCost + outCost
		if obs.CachedTokens > 0 && spec.InputCostPer1MUSD > cachedRate {
			cacheSavings = (float64(obs.CachedTokens) / 1e6) * (spec.InputCostPer1MUSD - cachedRate)
		}
		if hours > 0 {
			hourlyInfra = totalCost / hours
		}
	}

	out := Summary{
		DeploymentID:          spec.ID,
		DeploymentName:        spec.Name,
		BillingMode:           mode,
		TotalRunCostUSD:       round6(totalCost),
		HourlyInfraCostUSD:    round6(hourlyInfra),
		PrefixCacheSavingsUSD: round6(cacheSavings),
	}
	if obs.OutputTokens > 0 {
		out.CostPer1MOutputTokensUSD = round6(totalCost / (float64(obs.OutputTokens) / 1e6))
	}
	if obs.PromptTokens > 0 {
		out.CostPer1MInputTokensUSD = round6(totalCost / (float64(obs.PromptTokens) / 1e6))
	}
	if obs.Requests > 0 {
		out.CostPer1KRequestsUSD = round6(totalCost / (float64(obs.Requests) / 1e3))
	}
	goodput := obs.GoodputRequests
	if goodput <= 0 {
		goodput = obs.Requests
	}
	if goodput > 0 {
		out.CostPer1KGoodputRequestsUSD = round6(totalCost / (float64(goodput) / 1e3))
	}
	if spec.GPUCount > 0 && durSec > 0 && obs.OutputTokens > 0 {
		out.EffectiveOutputTokensPerGPUSecond = round6(float64(obs.OutputTokens) / (durSec * float64(spec.GPUCount)))
	}
	return out
}

func round6(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*1e6) / 1e6
}
