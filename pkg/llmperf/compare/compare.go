package compare

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
)

// RunSnapshot summarizes one LLM run's latency, throughput, and token distributions for comparison.
type RunSnapshot struct {
	TestID            string    `json:"test_id"`
	Model             string    `json:"model,omitempty"`
	WorkloadDigest    string    `json:"workload_digest,omitempty"`
	Requests          int64     `json:"requests"`
	TTFTP50Ms         float64   `json:"ttft_p50_ms"`
	TTFTP95Ms         float64   `json:"ttft_p95_ms"`
	TTFTP99Ms         float64   `json:"ttft_p99_ms"`
	TPOTP50Ms         float64   `json:"tpot_p50_ms"`
	TPOTP95Ms         float64   `json:"tpot_p95_ms"`
	TPOTP99Ms         float64   `json:"tpot_p99_ms"`
	E2EP50Ms          float64   `json:"e2e_p50_ms"`
	E2EP95Ms          float64   `json:"e2e_p95_ms"`
	E2EP99Ms          float64   `json:"e2e_p99_ms"`
	SystemOutputTPS   float64   `json:"system_output_tps"`
	OutputTPSP50      float64   `json:"output_tps_p50"`
	PromptTokens      int64     `json:"prompt_tokens"`
	OutputTokens      int64     `json:"output_tokens"`
	CacheHitRatio     float64   `json:"cache_hit_ratio"`
	Truncated         int64     `json:"truncated"`
	Throttled429      int64     `json:"throttled_429"`
	IntervalTTFTP95Ms []float64 `json:"interval_ttft_p95_ms,omitempty"`
	IntervalTPOTP95Ms []float64 `json:"interval_tpot_p95_ms,omitempty"`
}

// MetricDiff represents the delta, percentage change, and bootstrap confidence interval for one metric.
type MetricDiff struct {
	Metric      string  `json:"metric"`
	Baseline    float64 `json:"baseline"`
	Candidate   float64 `json:"candidate"`
	Delta       float64 `json:"delta"`
	DeltaPct    float64 `json:"delta_pct"`
	CI95Low     float64 `json:"ci95_low,omitempty"`
	CI95High    float64 `json:"ci95_high,omitempty"`
	Significant bool    `json:"significant"`
	Direction   string  `json:"direction"` // "improved", "regressed", "neutral"
}

// ComparisonResult holds the full LLM baseline vs candidate comparison.
type ComparisonResult struct {
	BaselineTestID        string       `json:"baseline_test_id"`
	CandidateTestID       string       `json:"candidate_test_id"`
	WorkloadDigestMatched bool         `json:"workload_digest_matched"`
	OutputLengthDriftPct  float64      `json:"output_length_drift_pct"`
	OutputLengthDriftFlag bool         `json:"output_length_drift_flag"`
	InputLengthDriftPct   float64      `json:"input_length_drift_pct"`
	InputLengthDriftFlag  bool         `json:"input_length_drift_flag"`
	Warnings              []string     `json:"warnings,omitempty"`
	Metrics               []MetricDiff `json:"metrics"`
}

// Compare evaluates candidate against baseline, flagging >15% output-length drift
// and computing bootstrap 95% confidence intervals when interval samples are available.
func Compare(baseline, candidate RunSnapshot, seed int64) ComparisonResult {
	if seed == 0 {
		seed = 42
	}
	rng := rand.New(rand.NewSource(seed))

	digestMatch := baseline.WorkloadDigest != "" && baseline.WorkloadDigest == candidate.WorkloadDigest
	var warnings []string
	if baseline.WorkloadDigest != "" && candidate.WorkloadDigest != "" && !digestMatch {
		warnings = append(warnings, "Workload digest mismatch: baseline and candidate runs used different offered loads or prompt datasets.")
	}

	baseMeanOut := meanPerReq(baseline.OutputTokens, baseline.Requests)
	candMeanOut := meanPerReq(candidate.OutputTokens, candidate.Requests)
	outDriftPct := pctChange(baseMeanOut, candMeanOut)
	outDriftFlag := math.Abs(outDriftPct) > 15.0
	if outDriftFlag {
		warnings = append(warnings, fmt.Sprintf(
			"Mean output tokens drifted by %+.1f%% (%.1f -> %.1f tokens/req); E2E latency comparison is confounded by verbosity change — rely on TPOT for decode regression assessment.",
			outDriftPct, baseMeanOut, candMeanOut,
		))
	}

	baseMeanIn := meanPerReq(baseline.PromptTokens, baseline.Requests)
	candMeanIn := meanPerReq(candidate.PromptTokens, candidate.Requests)
	inDriftPct := pctChange(baseMeanIn, candMeanIn)
	inDriftFlag := math.Abs(inDriftPct) > 15.0
	if inDriftFlag {
		warnings = append(warnings, fmt.Sprintf(
			"Mean prompt tokens drifted by %+.1f%% (%.1f -> %.1f tokens/req); TTFT comparison is affected by prompt length shift.",
			inDriftPct, baseMeanIn, candMeanIn,
		))
	}

	metrics := []MetricDiff{
		diffLowerBetter("ttft_p50_ms", baseline.TTFTP50Ms, candidate.TTFTP50Ms, nil, nil, rng),
		diffLowerBetter("ttft_p95_ms", baseline.TTFTP95Ms, candidate.TTFTP95Ms, baseline.IntervalTTFTP95Ms, candidate.IntervalTTFTP95Ms, rng),
		diffLowerBetter("ttft_p99_ms", baseline.TTFTP99Ms, candidate.TTFTP99Ms, nil, nil, rng),
		diffLowerBetter("tpot_p50_ms", baseline.TPOTP50Ms, candidate.TPOTP50Ms, nil, nil, rng),
		diffLowerBetter("tpot_p95_ms", baseline.TPOTP95Ms, candidate.TPOTP95Ms, baseline.IntervalTPOTP95Ms, candidate.IntervalTPOTP95Ms, rng),
		diffLowerBetter("tpot_p99_ms", baseline.TPOTP99Ms, candidate.TPOTP99Ms, nil, nil, rng),
		diffLowerBetter("e2e_p95_ms", baseline.E2EP95Ms, candidate.E2EP95Ms, nil, nil, rng),
		diffHigherBetter("system_output_tps", baseline.SystemOutputTPS, candidate.SystemOutputTPS),
		diffHigherBetter("output_tps_p50", baseline.OutputTPSP50, candidate.OutputTPSP50),
	}

	return ComparisonResult{
		BaselineTestID:        baseline.TestID,
		CandidateTestID:       candidate.TestID,
		WorkloadDigestMatched: digestMatch,
		OutputLengthDriftPct:  round2(outDriftPct),
		OutputLengthDriftFlag: outDriftFlag,
		InputLengthDriftPct:   round2(inDriftPct),
		InputLengthDriftFlag:  inDriftFlag,
		Warnings:              warnings,
		Metrics:               metrics,
	}
}

func diffLowerBetter(name string, base, cand float64, baseSamples, candSamples []float64, rng *rand.Rand) MetricDiff {
	delta := cand - base
	pct := pctChange(base, cand)
	ciLow, ciHigh, sig := bootstrapMeanDiffCI(baseSamples, candSamples, rng)
	if len(baseSamples) < 3 || len(candSamples) < 3 {
		sig = math.Abs(pct) >= 10.0
	}
	dir := "neutral"
	if sig {
		if delta > 0 {
			dir = "regressed"
		} else if delta < 0 {
			dir = "improved"
		}
	}
	return MetricDiff{
		Metric:      name,
		Baseline:    round2(base),
		Candidate:   round2(cand),
		Delta:       round2(delta),
		DeltaPct:    round2(pct),
		CI95Low:     round2(ciLow),
		CI95High:    round2(ciHigh),
		Significant: sig,
		Direction:   dir,
	}
}

func diffHigherBetter(name string, base, cand float64) MetricDiff {
	delta := cand - base
	pct := pctChange(base, cand)
	sig := math.Abs(pct) >= 10.0
	dir := "neutral"
	if sig {
		if delta < 0 {
			dir = "regressed"
		} else if delta > 0 {
			dir = "improved"
		}
	}
	return MetricDiff{
		Metric:      name,
		Baseline:    round2(base),
		Candidate:   round2(cand),
		Delta:       round2(delta),
		DeltaPct:    round2(pct),
		Significant: sig,
		Direction:   dir,
	}
}

func bootstrapMeanDiffCI(base, cand []float64, rng *rand.Rand) (low, high float64, sig bool) {
	if len(base) < 3 || len(cand) < 3 {
		return 0, 0, false
	}
	const iters = 500
	diffs := make([]float64, iters)
	for b := 0; b < iters; b++ {
		var bSum, cSum float64
		for i := 0; i < len(base); i++ {
			bSum += base[rng.Intn(len(base))]
		}
		for i := 0; i < len(cand); i++ {
			cSum += cand[rng.Intn(len(cand))]
		}
		diffs[b] = (cSum / float64(len(cand))) - (bSum / float64(len(base)))
	}
	sort.Float64s(diffs)
	low = diffs[int(math.Floor(float64(iters)*0.025))]
	high = diffs[int(math.Floor(float64(iters)*0.975))]
	sig = (low > 0 && high > 0) || (low < 0 && high < 0)
	return low, high, sig
}

func meanPerReq(tokens, reqs int64) float64 {
	if reqs <= 0 {
		return 0
	}
	return float64(tokens) / float64(reqs)
}

func pctChange(base, cand float64) float64 {
	if base == 0 {
		return 0
	}
	return ((cand - base) / base) * 100.0
}

func round2(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*100) / 100
}
