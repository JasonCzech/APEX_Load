package llmfixture

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFixtureServerDialects(t *testing.T) {
	dialects := []struct {
		name    string
		dialect Dialect
		path    string
		wantSub string
		body    string
	}{
		{name: "OpenAI Chat", dialect: DialectOpenAIChat, path: "/v1/chat/completions", wantSub: "data: [DONE]"},
		{name: "OpenAI Completions", dialect: DialectOpenAICompletions, path: "/v1/completions", wantSub: `"object":"text_completion"`},
		{name: "Azure OpenAI", dialect: DialectAzureOpenAI, path: "/openai/deployments/gpt4/chat/completions?api-version=2024-06-01", wantSub: "data: [DONE]"},
		// TGI reports finish details only when the request asks for them.
		{name: "TGI", dialect: DialectTGI, path: "/generate_stream", wantSub: `"generated_tokens":`, body: `{"inputs":"Hello world","stream":true,"parameters":{"details":true}}`},
		{name: "TGI without details", dialect: DialectTGI, path: "/generate_stream", wantSub: `"details":null`, body: `{"inputs":"Hello world","stream":true}`},
		{name: "Ollama NDJSON", dialect: DialectOllama, path: "/api/chat", wantSub: `"done":true`},
		{name: "Triton", dialect: DialectTriton, path: "/v2/models/ensemble/generate_stream", wantSub: `"text_output":`},
	}

	for _, tc := range dialects {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewServer(Config{
				Dialect:              tc.dialect,
				BaseTTFT:             2 * time.Millisecond,
				BaseITL:              1 * time.Millisecond,
				OutputTokens:         4,
				ReasoningTokens:      1,
				CachedPromptTokens:   2,
				EmitServerTimestamps: true,
			})
			defer srv.Close()

			reqBody := `{"model":"mock-model","stream":true,"messages":[{"role":"user","content":"Hello world"}]}`
			if tc.body != "" {
				reqBody = tc.body
			}
			resp, err := http.Post(srv.URL+tc.path, "application/json", strings.NewReader(reqBody))
			if err != nil {
				t.Fatalf("POST %s: %v", tc.path, err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if !strings.Contains(string(body), tc.wantSub) {
				t.Fatalf("response missing %q:\n%s", tc.wantSub, string(body))
			}
			if resp.Header.Get("x-apexllm-prefill-ms") == "" {
				t.Fatal("expected x-apexllm-prefill-ms header when EmitServerTimestamps is true")
			}

			reqs := srv.Requests()
			if len(reqs) != 1 {
				t.Fatalf("recorded requests = %d, want 1", len(reqs))
			}
			if reqs[0].TTFT <= 0 {
				t.Fatalf("expected positive ground-truth TTFT, got %v", reqs[0].TTFT)
			}
		})
	}
}

func TestFixtureFaultInjection(t *testing.T) {
	t.Run("429 retry-after", func(t *testing.T) {
		srv := NewServer(Config{
			Fault: FaultConfig{
				Kind:          FaultRateLimit429,
				RetryAfterSec: 2,
				RetryAfterMs:  250,
			},
		})
		defer srv.Close()

		resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"stream":true}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429", resp.StatusCode)
		}
		if resp.Header.Get("Retry-After") != "2" || resp.Header.Get("retry-after-ms") != "250" {
			t.Fatalf("unexpected retry headers: Retry-After=%q retry-after-ms=%q", resp.Header.Get("Retry-After"), resp.Header.Get("retry-after-ms"))
		}
		if srv.Summary().ThrottledRequests != 1 {
			t.Fatalf("ThrottledRequests = %d, want 1", srv.Summary().ThrottledRequests)
		}
	})

	t.Run("Truncation omits DONE", func(t *testing.T) {
		srv := NewServer(Config{
			BaseTTFT:     time.Millisecond,
			BaseITL:      time.Millisecond,
			OutputTokens: 6,
			Fault:        FaultConfig{Kind: FaultTruncate, TruncateAfter: 2},
		})
		defer srv.Close()

		resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"stream":true}`))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if strings.Contains(string(body), "[DONE]") {
			t.Fatalf("truncated stream must not contain [DONE]: %s", string(body))
		}
		if srv.Summary().TruncatedRequests != 1 {
			t.Fatalf("TruncatedRequests = %d, want 1", srv.Summary().TruncatedRequests)
		}
	})
}

func TestBatchSimulatorConcurrencyAndMetrics(t *testing.T) {
	srv := NewServer(Config{
		OutputTokens: 4,
		Simulator: &SimulatorConfig{
			MaxNumSeqs:          2,
			KVCacheCapacityToks: 10,
			BasePrefill:         5 * time.Millisecond,
			BaseDecodePerToken:  2 * time.Millisecond,
		},
	})
	defer srv.Close()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", bytes.NewBufferString(`{"stream":true,"messages":[{"role":"user","content":"test prompt for simulator"}]}`))
			if err != nil {
				return
			}
			_, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
		}()
	}
	wg.Wait()

	metricsResp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metricsBody, _ := io.ReadAll(metricsResp.Body)
	_ = metricsResp.Body.Close()
	text := string(metricsBody)
	for _, metricName := range []string{
		"vllm:num_requests_running",
		"vllm:num_requests_waiting",
		"vllm:gpu_cache_usage_perc",
		"vllm:kv_cache_usage_perc",
		"vllm:num_preemptions_total",
		"DCGM_FI_DEV_GPU_UTIL",
	} {
		if !strings.Contains(text, metricName) {
			t.Fatalf("metrics output missing %q:\n%s", metricName, text)
		}
	}
}
