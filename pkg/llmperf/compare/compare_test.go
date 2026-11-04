package compare

import (
	"testing"
)

func TestCompareOutputLengthDriftAndBootstrapSignificance(t *testing.T) {
	base := RunSnapshot{
		TestID:            "run-base",
		WorkloadDigest:    "sha256:abc",
		Requests:          100,
		TTFTP50Ms:         80,
		TTFTP95Ms:         150,
		TTFTP99Ms:         210,
		TPOTP50Ms:         12,
		TPOTP95Ms:         18,
		TPOTP99Ms:         25,
		E2EP95Ms:          600,
		SystemOutputTPS:   1200,
		OutputTPSP50:      80,
		PromptTokens:      50000,
		OutputTokens:      20000, // 200 tok/req
		IntervalTTFTP95Ms: []float64{145, 150, 148, 152, 149, 151},
		IntervalTPOTP95Ms: []float64{17.5, 18.0, 18.2, 17.9, 18.1, 18.0},
	}
	cand := RunSnapshot{
		TestID:            "run-cand",
		WorkloadDigest:    "sha256:abc",
		Requests:          100,
		TTFTP50Ms:         82,
		TTFTP95Ms:         195, // significant regression
		TTFTP99Ms:         260,
		TPOTP50Ms:         12,
		TPOTP95Ms:         18.1, // neutral
		TPOTP99Ms:         25,
		E2EP95Ms:          850,
		SystemOutputTPS:   1180,
		OutputTPSP50:      79,
		PromptTokens:      50000,
		OutputTokens:      26000, // 260 tok/req (+30% > 15% drift threshold)
		IntervalTTFTP95Ms: []float64{190, 195, 198, 192, 196, 194},
		IntervalTPOTP95Ms: []float64{17.8, 18.1, 18.3, 18.0, 17.9, 18.2},
	}
	res := Compare(base, cand, 42)
	if !res.WorkloadDigestMatched {
		t.Fatal("expected WorkloadDigestMatched=true")
	}
	if !res.OutputLengthDriftFlag || res.OutputLengthDriftPct < 29.9 {
		t.Fatalf("expected OutputLengthDriftFlag=true (+30%%), got %+v", res)
	}
	var ttft95, tpot95 MetricDiff
	for _, m := range res.Metrics {
		switch m.Metric {
		case "ttft_p95_ms":
			ttft95 = m
		case "tpot_p95_ms":
			tpot95 = m
		}
	}
	if !ttft95.Significant || ttft95.Direction != "regressed" {
		t.Fatalf("expected ttft_p95_ms significant regression, got %+v", ttft95)
	}
	if tpot95.Significant || tpot95.Direction != "neutral" {
		t.Fatalf("expected tpot_p95_ms neutral, got %+v", tpot95)
	}
}
