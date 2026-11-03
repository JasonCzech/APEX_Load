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
