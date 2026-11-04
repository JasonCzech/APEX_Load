package econ

import (
	"math"
	"testing"
)

func TestEvaluateGPUAndTokenEconomics(t *testing.T) {
	gpuSum := Evaluate(DeploymentSpec{
		ID:                "vllm-h100",
		Name:              "2x H100 vLLM",
		BillingMode:       "gpu_hourly",
		GPUCount:          2,
		GPUCostPerHourUSD: 4.50,
	}, RunObservation{
		DurationSeconds: 1800, // 0.5h -> $4.50 total
		Requests:        1000,
		GoodputRequests: 900,
		PromptTokens:    500000,
		OutputTokens:    250000,
	})
	if math.Abs(gpuSum.TotalRunCostUSD-4.50) > 1e-4 || math.Abs(gpuSum.CostPer1MOutputTokensUSD-18.0) > 1e-4 {
		t.Fatalf("unexpected GPU economics summary: %+v", gpuSum)
	}
	if gpuSum.CostPer1KGoodputRequestsUSD <= gpuSum.CostPer1KRequestsUSD {
		t.Fatalf("expected goodput cost > raw request cost when goodput < requests: %+v", gpuSum)
	}

	apiSum := Evaluate(DeploymentSpec{
		ID:                   "aoai-gpt4o",
		Name:                 "Azure OpenAI GPT-4o",
		BillingMode:          "token_api",
		InputCostPer1MUSD:    2.50,
		CachedInputCost1MUSD: 1.25,
		OutputCostPer1MUSD:   10.00,
	}, RunObservation{
		DurationSeconds: 600,
		Requests:        500,
		GoodputRequests: 500,
		PromptTokens:    1000000,
		CachedTokens:    400000,
		OutputTokens:    200000,
	})
	// uncached 0.6M * 2.50 = 1.50; cached 0.4M * 1.25 = 0.50; output 0.2M * 10.00 = 2.00 => total 4.00, savings 0.50
	if math.Abs(apiSum.TotalRunCostUSD-4.00) > 1e-4 || math.Abs(apiSum.PrefixCacheSavingsUSD-0.50) > 1e-4 {
		t.Fatalf("unexpected token_api economics summary: %+v", apiSum)
	}
}
