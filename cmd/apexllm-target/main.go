// Command apexllm-target serves a configurable mock LLM streaming and
// continuous-batching inference server (OpenAI, Azure OpenAI, TGI, Ollama,
// Triton dialects, plus vLLM/DCGM Prometheus /metrics) for local trials.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	llmfixture "github.com/jasonczech/apexload/pkg/llmperf/fixture"
)

func main() {
	var (
		addr             = flag.String("addr", "127.0.0.1:18088", "TCP listen address")
		dialect          = flag.String("dialect", string(llmfixture.DialectOpenAIChat), "Default LLM dialect (openai_chat, openai_completions, azure_openai, tgi, ollama, triton)")
		model            = flag.String("model", "gpt-4o-mini-mock", "Default model identifier")
		seed             = flag.Int64("seed", 42, "Deterministic RNG seed")
		ttftMs           = flag.Int("ttft-ms", 25, "Base time to first token in milliseconds")
		itlMs            = flag.Int("itl-ms", 8, "Base inter-token latency in milliseconds")
		outputTokens     = flag.Int("output-tokens", 32, "Default completion token count")
		reasoningTokens  = flag.Int("reasoning-tokens", 0, "Reasoning token count before visible content")
		cachedTokens     = flag.Int("cached-tokens", 0, "Cached prompt token count")
		emitTimestamps   = flag.Bool("emit-server-timestamps", true, "Emit x-apexllm-* server latency breakdown headers")
		rpmQuota         = flag.Int("rpm-quota", 0, "Optional requests-per-minute quota (0 = unlimited)")
		tpmQuota         = flag.Int("tpm-quota", 0, "Optional tokens-per-minute quota (0 = unlimited)")
		faultKind        = flag.String("fault", "", "Optional fault kind (429, truncate, stall, reset, content_filter, 5xx, empty_output, stream_error)")
		faultProb        = flag.Float64("fault-prob", 0, "Fault probability (0..1)")
		faultEveryN      = flag.Int("fault-every-n", 0, "Trigger fault on every Nth request")
		retryAfterMs     = flag.Int("retry-after-ms", 250, "retry-after-ms value for 429 faults")
		enableSimulator  = flag.Bool("simulator", false, "Enable continuous-batching vLLM simulator")
		maxNumSeqs       = flag.Int("max-num-seqs", 16, "Simulator max_num_seqs batch ceiling")
		kvCapacityTokens = flag.Int("kv-capacity-tokens", 4096, "Simulator KV cache token capacity")
	)
	flag.Parse()

	cfg := llmfixture.Config{
		Dialect:              llmfixture.Dialect(*dialect),
		Model:                *model,
		Seed:                 *seed,
		BaseTTFT:             time.Duration(*ttftMs) * time.Millisecond,
		BaseITL:              time.Duration(*itlMs) * time.Millisecond,
		OutputTokens:         *outputTokens,
		ReasoningTokens:      *reasoningTokens,
		CachedPromptTokens:   *cachedTokens,
		EmitServerTimestamps: *emitTimestamps,
		RPMQuota:             *rpmQuota,
		TPMQuota:             *tpmQuota,
		Fault: llmfixture.FaultConfig{
			Kind:          llmfixture.FaultKind(*faultKind),
			Probability:   *faultProb,
			EveryN:        *faultEveryN,
			RetryAfterMs:  *retryAfterMs,
			RetryAfterSec: max(1, (*retryAfterMs+999)/1000),
		},
	}
	if *enableSimulator {
		cfg.Simulator = &llmfixture.SimulatorConfig{
			MaxNumSeqs:          *maxNumSeqs,
			KVCacheCapacityToks: *kvCapacityTokens,
			BasePrefill:         time.Duration(*ttftMs) * time.Millisecond,
			BaseDecodePerToken:  time.Duration(*itlMs) * time.Millisecond,
		}
	}

	fixture := llmfixture.NewServer(cfg)
	// Close the auto-started httptest listener and serve on the requested address instead.
	handler := fixture.Handler()
	fixture.Close()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "apexllm-target listen %s: %v\n", *addr, err)
		os.Exit(1)
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	slog.Info("apexllm-target listening", "addr", ln.Addr().String(), "dialect", *dialect, "simulator", *enableSimulator)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "apexllm-target serve: %v\n", err)
		os.Exit(1)
	}
}
