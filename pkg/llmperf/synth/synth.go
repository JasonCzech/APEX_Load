package synth

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strings"

	"github.com/jasonczech/apexload/pkg/llmperf/dataset"
)

// Config controls synthetic prompt dataset generation.
type Config struct {
	Count                int     `json:"count"`
	Distribution         string  `json:"distribution,omitempty"` // "lognormal" (default), "uniform", "bimodal"
	MeanInputTokens      int     `json:"mean_input_tokens"`
	StdInputTokens       int     `json:"std_input_tokens,omitempty"`
	MinInputTokens       int     `json:"min_input_tokens,omitempty"`
	MaxInputTokens       int     `json:"max_input_tokens,omitempty"`
	ExpectedOutputTokens int     `json:"expected_output_tokens,omitempty"`
	SharedPrefixTokens   int     `json:"shared_prefix_tokens,omitempty"`
	BytesPerToken        float64 `json:"bytes_per_token,omitempty"`
	Seed                 int64   `json:"seed,omitempty"`
}

// Result contains the generated synthetic records and JSONL text.
type Result struct {
	Count              int              `json:"count"`
	Distribution       string           `json:"distribution"`
	MeanInputTokens    float64          `json:"mean_input_tokens"`
	MaxInputTokens     int              `json:"max_input_tokens"`
	SharedPrefixTokens int              `json:"shared_prefix_tokens"`
	BytesPerToken      float64          `json:"bytes_per_token"`
	InputBandCounts    map[string]int   `json:"input_band_counts"`
	Records            []dataset.Record `json:"records"`
	JSONL              string           `json:"jsonl"`
}

var synthVocab = []string{
	"architecture", "throughput", "latency", "scheduler", "kvcache", "prefill",
	"decode", "attention", "transformer", "tensor", "pipeline", "parallelism",
	"quantization", "paged", "memory", "bandwidth", "saturation", "batching",
	"iteration", "token", "embedding", "context", "window", "orchestration",
	"distributed", "checkpoint", "speculative", "routing", "replica", "headroom",
	"percentile", "telemetry", "histogram", "correlation", "anomaly", "capacity",
	"frontier", "utilization", "concurrency", "backpressure", "admission", "queue",
	"partition", "shard", "consensus", "replication", "durability", "snapshot",
	"isolation", "transaction", "serialization", "compression", "calibration", "probe",
	"benchmark", "regression", "hypothesis", "signature", "diagnostics", "observability",
	"resilience", "failover", "throttling", "guardrail", "threshold", "allocation",
}

// Generation bounds. The dataset is built in memory and returned twice (as
// records and as JSONL), so its size is bounded up front rather than by the
// request body, which a few numbers can multiply without limit.
const (
	MaxCount         = 50000
	MaxInputTokens   = 1 << 20 // the largest context windows in use
	MaxBytesPerToken = 16.0
	MaxOutputBytes   = 32 << 20
)

// Generate produces a deterministic synthetic JSONL prompt dataset matching cfg.
func Generate(cfg Config) (*Result, error) {
	count := cfg.Count
	if count <= 0 {
		count = 100
	}
	if count > MaxCount {
		return nil, fmt.Errorf("count %d exceeds maximum %d", count, MaxCount)
	}
	bpt := cfg.BytesPerToken
	if bpt <= 0 || math.IsNaN(bpt) || math.IsInf(bpt, 0) {
		bpt = 4.0
	}
	if bpt > MaxBytesPerToken {
		return nil, fmt.Errorf("bytes_per_token %g exceeds maximum %g", bpt, MaxBytesPerToken)
	}
	for _, field := range []struct {
		name  string
		value int
	}{
		{"mean_input_tokens", cfg.MeanInputTokens},
		{"std_input_tokens", cfg.StdInputTokens},
		{"min_input_tokens", cfg.MinInputTokens},
		{"max_input_tokens", cfg.MaxInputTokens},
		{"expected_output_tokens", cfg.ExpectedOutputTokens},
		{"shared_prefix_tokens", cfg.SharedPrefixTokens},
	} {
		if field.value > MaxInputTokens {
			return nil, fmt.Errorf("%s %d exceeds maximum %d", field.name, field.value, MaxInputTokens)
		}
	}
	meanIn := cfg.MeanInputTokens
	if meanIn <= 0 {
		meanIn = 512
	}
	stdIn := cfg.StdInputTokens
	if stdIn <= 0 {
		stdIn = meanIn / 3
		if stdIn < 16 {
			stdIn = 16
		}
	}
	minIn := cfg.MinInputTokens
	if minIn <= 0 {
		minIn = 16
	}
	maxIn := cfg.MaxInputTokens
	if maxIn <= 0 {
		maxIn = max(meanIn*4, minIn+64)
	}
	if maxIn < minIn {
		maxIn = minIn
	}
	expectedOut := cfg.ExpectedOutputTokens
	if expectedOut <= 0 {
		expectedOut = 256
	}
	dist := strings.ToLower(strings.TrimSpace(cfg.Distribution))
	if dist == "" {
		dist = "lognormal"
	}
	if dist != "lognormal" && dist != "uniform" && dist != "bimodal" {
		return nil, fmt.Errorf("unsupported distribution %q (must be lognormal, uniform, or bimodal)", cfg.Distribution)
	}

	seed := cfg.Seed
	if seed == 0 {
		seed = 1
	}
	rng := rand.New(rand.NewSource(seed))

	prefixTokens := max(0, min(cfg.SharedPrefixTokens, maxIn-4))
	if float64(prefixTokens)*bpt > MaxOutputBytes {
		return nil, outputTooLarge()
	}
	var sharedPrefix string
	if prefixTokens > 0 {
		sharedPrefix = buildTokenString(rng, prefixTokens, bpt, "system-context")
	}

	records := make([]dataset.Record, count)
	bandCounts := map[string]int{
		"short":     0,
		"medium":    0,
		"long":      0,
		"very_long": 0,
	}
	var totalIn, observedMax int
	var jsonlBuf strings.Builder

	for i := 0; i < count; i++ {
		targetTokens := sampleTokenCount(rng, dist, meanIn, stdIn, minIn, maxIn, i)
		suffixTokens := max(4, targetTokens-prefixTokens)
		// Each record carries the shared prefix and its own prompt (twice:
		// prompt and message); refuse before building what cannot fit.
		if float64(jsonlBuf.Len())+float64(prefixTokens+2*suffixTokens)*bpt > MaxOutputBytes {
			return nil, outputTooLarge()
		}
		userPrompt := buildTokenString(rng, suffixTokens, bpt, fmt.Sprintf("req-%d", i+1))
		var msgs []dataset.Message
		if sharedPrefix != "" {
			msgs = []dataset.Message{
				{Role: "system", Content: sharedPrefix},
				{Role: "user", Content: userPrompt},
			}
		} else {
			msgs = []dataset.Message{
				{Role: "user", Content: userPrompt},
			}
		}
		actualTokens := prefixTokens + suffixTokens
		band := dataset.ClassifyInputBand(actualTokens)
		bandCounts[band]++
		totalIn += actualTokens
		if actualTokens > observedMax {
			observedMax = actualTokens
		}
		rec := dataset.Record{
			PromptID:             fmt.Sprintf("synth-%d", i+1),
			ConversationID:       fmt.Sprintf("synth-conv-%d", i+1),
			TurnIndex:            0,
			Prompt:               userPrompt,
			Messages:             msgs,
			EstimatedInputTokens: actualTokens,
			ExpectedOutputTokens: expectedOut,
			InputBand:            band,
		}
		records[i] = rec
		line, err := json.Marshal(rec)
		if err != nil {
			return nil, err
		}
		jsonlBuf.Write(line)
		jsonlBuf.WriteByte('\n')
		if jsonlBuf.Len() > MaxOutputBytes {
			return nil, outputTooLarge()
		}
	}

	return &Result{
		Count:              count,
		Distribution:       dist,
		MeanInputTokens:    float64(totalIn) / float64(count),
		MaxInputTokens:     observedMax,
		SharedPrefixTokens: prefixTokens,
		BytesPerToken:      bpt,
		InputBandCounts:    bandCounts,
		Records:            records,
		JSONL:              jsonlBuf.String(),
	}, nil
}

func outputTooLarge() error {
	return fmt.Errorf("synthetic dataset would exceed %d MiB; lower count, input token sizes, shared_prefix_tokens or bytes_per_token", MaxOutputBytes>>20)
}

func sampleTokenCount(rng *rand.Rand, dist string, meanIn, stdIn, minIn, maxIn, idx int) int {
	var val float64
	switch dist {
	case "uniform":
		lo := math.Max(float64(minIn), float64(meanIn)-float64(stdIn)*1.732)
		hi := math.Min(float64(maxIn), float64(meanIn)+float64(stdIn)*1.732)
		if hi <= lo {
			lo, hi = float64(minIn), float64(maxIn)
		}
		if hi <= lo {
			val = lo
		} else {
			val = lo + rng.Float64()*(hi-lo)
		}
	case "bimodal":
		// 70% short (~0.4*mean), 30% long (~2.4*mean) so overall mean ≈ meanIn
		if rng.Float64() < 0.70 {
			lowMean := math.Max(float64(minIn), float64(meanIn)*0.4)
			val = lowMean + rng.NormFloat64()*(lowMean*0.2)
		} else {
			highMean := math.Min(float64(maxIn), float64(meanIn)*2.4)
			val = highMean + rng.NormFloat64()*(highMean*0.2)
		}
	default: // lognormal
		m := float64(meanIn)
		s := float64(stdIn)
		sigma2 := math.Log(1.0 + (s*s)/(m*m))
		mu := math.Log(m) - 0.5*sigma2
		val = math.Exp(mu + math.Sqrt(sigma2)*rng.NormFloat64())
	}
	n := int(math.Round(val))
	if n < minIn {
		n = minIn
	}
	if n > maxIn {
		n = maxIn
	}
	return n
}

func buildTokenString(rng *rand.Rand, targetTokens int, bytesPerToken float64, tag string) string {
	targetBytes := int(math.Round(float64(targetTokens) * bytesPerToken))
	if targetBytes < 8 {
		targetBytes = 8
	}
	var sb strings.Builder
	sb.Grow(targetBytes + 16)
	sb.WriteString("[")
	sb.WriteString(tag)
	sb.WriteString("]")
	for sb.Len() < targetBytes {
		word := synthVocab[rng.Intn(len(synthVocab))]
		sb.WriteByte(' ')
		sb.WriteString(word)
	}
	out := sb.String()
	if len(out) > targetBytes {
		out = out[:targetBytes]
	}
	return out
}
