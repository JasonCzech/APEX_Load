package synth

import (
	"math"
	"testing"
)

func TestGenerateSyntheticDatasetDistributionsAndSharedPrefix(t *testing.T) {
	for _, dist := range []string{"lognormal", "uniform", "bimodal"} {
		res, err := Generate(Config{
			Count:                200,
			Distribution:         dist,
			MeanInputTokens:      512,
			StdInputTokens:       128,
			MinInputTokens:       64,
			MaxInputTokens:       2048,
			ExpectedOutputTokens: 128,
			SharedPrefixTokens:   100,
			BytesPerToken:        4.0,
			Seed:                 42,
		})
		if err != nil {
			t.Fatalf("Generate(%s): %v", dist, err)
		}
		if res.Count != 200 || len(res.Records) != 200 {
			t.Fatalf("Generate(%s): count=%d", dist, res.Count)
		}
		if res.Records[0].Messages[0].Role != "system" || res.Records[0].Messages[0].Content != res.Records[1].Messages[0].Content {
			t.Fatalf("Generate(%s): expected identical shared system prefix across records", dist)
		}
		if res.Records[0].Prompt == res.Records[1].Prompt {
			t.Fatalf("Generate(%s): expected distinct user prompt suffixes across records", dist)
		}
		if math.Abs(res.MeanInputTokens-512) > 400 {
			t.Fatalf("Generate(%s): unexpected MeanInputTokens=%v", dist, res.MeanInputTokens)
		}
	}
}

func TestGenerateBoundsTheDatasetSize(t *testing.T) {
	for _, cfg := range []Config{
		{Count: 10, BytesPerToken: 1e9},
		{Count: 10, MaxInputTokens: MaxInputTokens + 1},
		{Count: 10, SharedPrefixTokens: MaxInputTokens + 1},
		// 50,000 records of about 64k tokens would be gigabytes.
		{Count: MaxCount, MeanInputTokens: 65536, MinInputTokens: 65536, MaxInputTokens: 65536},
		{Count: 4, MeanInputTokens: 1 << 20, MinInputTokens: 1 << 20, MaxInputTokens: 1 << 20, SharedPrefixTokens: 1<<20 - 8, BytesPerToken: 16},
	} {
		if _, err := Generate(cfg); err == nil {
			t.Fatalf("Generate(%+v) succeeded; want it refused", cfg)
		}
	}
	res, err := Generate(Config{Count: 200, MeanInputTokens: 1024, Seed: 3})
	if err != nil || len(res.JSONL) > MaxOutputBytes {
		t.Fatalf("an ordinary dataset must still generate: %v", err)
	}
}
