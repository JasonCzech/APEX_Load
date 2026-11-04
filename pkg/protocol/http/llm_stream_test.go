package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/llmfixture"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestLLMRequestStreamingOpenAIChat(t *testing.T) {
	fx := llmfixture.NewServer(llmfixture.Config{
		BaseTTFT:           12 * time.Millisecond,
		BaseITL:            4 * time.Millisecond,
		OutputTokens:       8,
		CachedPromptTokens: 4,
	})
	defer fx.Close()

	p := &HTTP{}
	defer func() { _ = p.Teardown(context.Background()) }()
	if err := p.Init(context.Background(), map[string]any{
		"base_url": fx.URL,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	res, err := p.Execute(context.Background(), &protocol.Action{
		Name:   "chat_stream",
		Type:   "llm_request",
		Target: fx.URL + "/v1/chat/completions",
		Params: map[string]any{
			"model":        "gpt-4o-mini",
			"prompt":       "Say hello in eight tokens.",
			"max_tokens":   16,
			"save_text_as": "assistant_reply",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !res.Success {
		t.Fatalf("Result.Success=false: %s", res.ErrorMessage)
	}
	if res.ServerTelemetry == nil || res.ServerTelemetry.LLM == nil {
		t.Fatalf("expected ServerTelemetry.LLM to be populated")
	}
	llm := res.ServerTelemetry.LLM
	if !llm.Stream {
		t.Errorf("expected Stream=true")
	}
	if llm.TTFTMs < 5 {
		t.Errorf("expected TTFTMs >= 5ms, got %v", llm.TTFTMs)
	}
	if llm.FirstVisibleTokenMs != llm.TTFTMs {
		t.Errorf("expected FirstVisibleTokenMs == TTFTMs, got %v vs %v", llm.FirstVisibleTokenMs, llm.TTFTMs)
	}
	if llm.TPOTMs <= 0 {
		t.Errorf("expected TPOTMs > 0, got %v", llm.TPOTMs)
	}
	if llm.ITLDistribution == nil || llm.ITLDistribution.Count != 7 {
		t.Errorf("expected ITLDistribution.Count=7, got %+v", llm.ITLDistribution)
	}
	if llm.CompletionTokens != 8 || llm.CachedTokens != 4 {
		t.Errorf("unexpected token counts: prompt=%d completion=%d cached=%d", llm.PromptTokens, llm.CompletionTokens, llm.CachedTokens)
	}
	if llm.TokensEstimated {
		t.Errorf("expected TokensEstimated=false when usage chunk is reported")
	}
	if llm.FinishReason != "stop" {
		t.Errorf("expected FinishReason=stop, got %q", llm.FinishReason)
	}
	if llm.OutputHash == 0 {
		t.Errorf("expected non-zero OutputHash")
	}
	if res.Extractions["assistant_reply"] == "" {
		t.Errorf("expected assistant_reply to be extracted")
	}
}

func TestLLMRequestRoleOnlyFirstChunkAndReasoning(t *testing.T) {
	fx := llmfixture.NewServer(llmfixture.Config{
		BaseTTFT:           5 * time.Millisecond,
		BaseITL:            10 * time.Millisecond,
		OutputTokens:       4,
		ReasoningTokens:    3,
		RoleOnlyFirstChunk: true,
	})
	defer fx.Close()

	p := &HTTP{}
	defer func() { _ = p.Teardown(context.Background()) }()
	if err := p.Init(context.Background(), map[string]any{
		"base_url": fx.URL,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	res, err := p.Execute(context.Background(), &protocol.Action{
		Name:   "reasoning_stream",
		Type:   "llm_request",
		Target: fx.URL + "/v1/chat/completions",
		Params: map[string]any{
			"model":  "deepseek-r1",
			"prompt": "Think step by step.",
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("Result.Success=false: %s", res.ErrorMessage)
	}
	llm := res.ServerTelemetry.LLM
	if llm == nil {
		t.Fatalf("expected LLM diagnostics")
	}
	if llm.TTFTMs <= 0 || llm.FirstVisibleTokenMs <= 0 {
		t.Fatalf("expected both TTFTMs and FirstVisibleTokenMs to be set: %+v", llm)
	}
	if llm.FirstVisibleTokenMs <= llm.TTFTMs {
		t.Errorf("expected FirstVisibleTokenMs (%f) > TTFTMs (%f) when reasoning precedes visible content",
			llm.FirstVisibleTokenMs, llm.TTFTMs)
	}
	if llm.ReasoningTokens != 3 {
		t.Errorf("expected ReasoningTokens=3, got %d", llm.ReasoningTokens)
	}
}

func TestLLMRequestDialectsAndHeaders(t *testing.T) {
	fx := llmfixture.NewServer(llmfixture.Config{
		BaseTTFT:     2 * time.Millisecond,
		BaseITL:      1 * time.Millisecond,
		OutputTokens: 5,
		TPMQuota:     45000,
		RPMQuota:     120,
		AzureRegion:  "eastus2",
	})
	defer fx.Close()

	p := &HTTP{}
	defer func() { _ = p.Teardown(context.Background()) }()
	if err := p.Init(context.Background(), map[string]any{
		"base_url": fx.URL,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	cases := []struct {
		name    string
		path    string
		dialect string
		stream  bool
	}{
		{name: "azure_openai", path: "/openai/deployments/gpt4o/chat/completions?api-version=2024-06-01", dialect: "azure_openai", stream: true},
		{name: "non_stream_chat", path: "/v1/chat/completions", dialect: "openai_chat", stream: false},
		{name: "tgi", path: "/generate_stream", dialect: "tgi", stream: true},
		{name: "ollama", path: "/api/chat", dialect: "ollama", stream: true},
		{name: "triton", path: "/v2/models/ensemble/generate_stream", dialect: "triton", stream: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := p.Execute(context.Background(), &protocol.Action{
				Name:   tc.name,
				Type:   "llm_request",
				Target: fx.URL + tc.path,
				Params: map[string]any{
					"llm_dialect": tc.dialect,
					"model":       "test-model",
					"prompt":      "Hello dialect test",
					"stream":      tc.stream,
				},
			})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if !res.Success {
				t.Fatalf("Result.Success=false: %s", res.ErrorMessage)
			}
			if res.ServerTelemetry == nil || res.ServerTelemetry.LLM == nil {
				t.Fatalf("expected LLM telemetry")
			}
			llm := res.ServerTelemetry.LLM
			if llm.Stream != tc.stream {
				t.Errorf("Stream=%v, want %v", llm.Stream, tc.stream)
			}
			if !tc.stream && llm.TPOTMs != 0 {
				t.Errorf("non-streaming request must not synthesize TPOTMs, got %v", llm.TPOTMs)
			}
			if tc.stream && (llm.TTFTMs <= 0 || llm.OutputChars == 0) {
				t.Errorf("streaming request expected TTFTMs and OutputChars > 0, got %+v", llm)
			}
			if tc.name == "azure_openai" {
				if llm.RateLimitRemainingTokens == nil || *llm.RateLimitRemainingTokens <= 0 {
					t.Errorf("expected positive RateLimitRemainingTokens, got %v", llm.RateLimitRemainingTokens)
				}
				if llm.RateLimitRemainingRequests == nil || *llm.RateLimitRemainingRequests <= 0 {
					t.Errorf("expected positive RateLimitRemainingRequests, got %v", llm.RateLimitRemainingRequests)
				}
				if llm.Region != "eastus2" {
					t.Errorf("expected Region=eastus2, got %q", llm.Region)
				}
			}
		})
	}
}

func TestLLMRequestVercelAIAndQualityChecks(t *testing.T) {
	t.Run("vercel_ai", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: 0:\"Hello \"\n\ndata: 0:\"world\"\n\ndata: d:{\"finishReason\":\"stop\",\"usage\":{\"promptTokens\":10,\"completionTokens\":2}}\n\n"))
		}))
		defer srv.Close()

		p := &HTTP{}
		defer func() { _ = p.Teardown(context.Background()) }()
		_ = p.Init(context.Background(), map[string]any{"base_url": srv.URL})

		res, err := p.Execute(context.Background(), &protocol.Action{
			Name:   "vercel",
			Type:   "llm_request",
			Target: srv.URL + "/api/chat",
			Params: map[string]any{
				"llm_dialect": "vercel_ai",
				"prompt":      "hi",
			},
		})
		if err != nil || !res.Success {
			t.Fatalf("Execute: err=%v success=%v msg=%s", err, res.Success, res.ErrorMessage)
		}
		llm := res.ServerTelemetry.LLM
		if llm == nil || llm.PromptTokens != 10 || llm.CompletionTokens != 2 || llm.FinishReason != "stop" {
			t.Fatalf("unexpected vercel LLM diagnostics: %+v", llm)
		}
	})

	t.Run("content_filter", func(t *testing.T) {
		fx := llmfixture.NewServer(llmfixture.Config{
			OutputTokens:        3,
			DefaultFinishReason: "content_filter",
		})
		defer fx.Close()
		p := &HTTP{}
		defer func() { _ = p.Teardown(context.Background()) }()
		_ = p.Init(context.Background(), map[string]any{"base_url": fx.URL})

		res, err := p.Execute(context.Background(), &protocol.Action{
			Name:   "cf",
			Type:   "llm_request",
			Target: fx.URL + "/v1/chat/completions",
			Params: map[string]any{
				"prompt": "test",
			},
		})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Success {
			t.Fatalf("expected content_filter finish_reason to fail action")
		}
		if res.ServerTelemetry == nil || res.ServerTelemetry.LLM == nil || res.ServerTelemetry.LLM.ErrorCode != "content_filter" {
			t.Fatalf("expected ErrorCode=content_filter, got %+v", res.ServerTelemetry)
		}
	})

	t.Run("http_200_error", func(t *testing.T) {
		fx := llmfixture.NewServer(llmfixture.Config{
			Fault: llmfixture.FaultConfig{
				Kind:         llmfixture.FaultStreamError,
				Probability:  1.0,
				ErrorCode:    "overloaded",
				ErrorMessage: "engine overloaded",
			},
		})
		defer fx.Close()
		p := &HTTP{}
		defer func() { _ = p.Teardown(context.Background()) }()
		_ = p.Init(context.Background(), map[string]any{"base_url": fx.URL})

		res, err := p.Execute(context.Background(), &protocol.Action{
			Name:   "err200",
			Type:   "llm_request",
			Target: fx.URL + "/v1/chat/completions",
			Params: map[string]any{
				"prompt": "test",
				"stream": true,
			},
		})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Success {
			t.Fatalf("expected HTTP 200 error payload to set res.Success=false")
		}
		if res.ServerTelemetry == nil || res.ServerTelemetry.LLM == nil || !res.ServerTelemetry.LLM.HTTP200Error {
			t.Fatalf("expected LLM.HTTP200Error=true")
		}
	})

	t.Run("truncate_eof", func(t *testing.T) {
		fx := llmfixture.NewServer(llmfixture.Config{
			OutputTokens: 10,
			Fault: llmfixture.FaultConfig{
				Kind:          llmfixture.FaultTruncate,
				Probability:   1.0,
				TruncateAfter: 3,
			},
		})
		defer fx.Close()
		p := &HTTP{}
		defer func() { _ = p.Teardown(context.Background()) }()
		_ = p.Init(context.Background(), map[string]any{"base_url": fx.URL})

		res, err := p.Execute(context.Background(), &protocol.Action{
			Name:   "trunc_eof",
			Type:   "llm_request",
			Target: fx.URL + "/v1/chat/completions",
			Params: map[string]any{
				"prompt": "test truncate",
				"stream": true,
			},
		})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Success {
			t.Fatalf("expected mid-stream EOF truncation without [DONE]/finish_reason to fail action")
		}
		if res.ServerTelemetry == nil || res.ServerTelemetry.LLM == nil || !res.ServerTelemetry.LLM.Truncated {
			t.Fatalf("expected LLM.Truncated=true, got %+v", res.ServerTelemetry)
		}
	})

	t.Run("stream_timeout", func(t *testing.T) {
		fx := llmfixture.NewServer(llmfixture.Config{
			OutputTokens: 8,
			Fault: llmfixture.FaultConfig{
				Kind:          llmfixture.FaultStall,
				Probability:   1.0,
				TruncateAfter: 2,
				StallDuration: 250 * time.Millisecond,
			},
		})
		defer fx.Close()
		p := &HTTP{}
		defer func() { _ = p.Teardown(context.Background()) }()
		_ = p.Init(context.Background(), map[string]any{"base_url": fx.URL})

		res, err := p.Execute(context.Background(), &protocol.Action{
			Name:   "stall_timeout",
			Type:   "llm_request",
			Target: fx.URL + "/v1/chat/completions",
			Params: map[string]any{
				"prompt":            "test timeout",
				"stream":            true,
				"stream_timeout_ms": 60,
			},
		})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Success {
			t.Fatalf("expected stream cut off by stream_timeout_ms to fail action")
		}
		if res.ServerTelemetry == nil || res.ServerTelemetry.LLM == nil || !res.ServerTelemetry.LLM.Truncated || res.ServerTelemetry.LLM.ErrorCode != "stream_timeout" {
			t.Fatalf("expected LLM.Truncated=true and ErrorCode=stream_timeout, got %+v", res.ServerTelemetry)
		}
	})

	t.Run("finish_reason_length", func(t *testing.T) {
		fx := llmfixture.NewServer(llmfixture.Config{
			OutputTokens:        4,
			DefaultFinishReason: "length",
		})
		defer fx.Close()
		p := &HTTP{}
		defer func() { _ = p.Teardown(context.Background()) }()
		_ = p.Init(context.Background(), map[string]any{"base_url": fx.URL})

		res, err := p.Execute(context.Background(), &protocol.Action{
			Name:   "len_trunc",
			Type:   "llm_request",
			Target: fx.URL + "/v1/chat/completions",
			Params: map[string]any{
				"prompt": "test length",
				"stream": true,
			},
		})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Success {
			t.Fatalf("expected finish_reason=length to fail action in LLM mode by default")
		}
		if res.ServerTelemetry == nil || res.ServerTelemetry.LLM == nil || !res.ServerTelemetry.LLM.Truncated {
			t.Fatalf("expected LLM.Truncated=true, got %+v", res.ServerTelemetry)
		}
	})
}

func BenchmarkLLMStreamChunk(b *testing.B) {
	obs := newLLMStreamObserver(LLMDialectOpenAIChat, time.Now(), false)
	defer obs.close()
	chunk := []byte(`{"id":"chatcmpl-123","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":" token"},"finish_reason":null}]}`)
	now := time.Now().Add(10 * time.Millisecond)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		obs.observeChunk("", chunk, now)
	}
}

func TestBuildMessagesFieldSystemPromptWithSlice(t *testing.T) {
	anySlice := []any{
		map[string]any{"role": "user", "content": "Hello"},
	}
	gotAny, err := buildMessagesField(anySlice, "Be concise", "")
	if err != nil {
		t.Fatalf("buildMessagesField([]any): %v", err)
	}
	msgsAny, ok := gotAny.([]any)
	if !ok || len(msgsAny) != 2 {
		t.Fatalf("expected 2 messages in []any with system prompt prepended, got %#v", gotAny)
	}
	firstAny, _ := msgsAny[0].(map[string]any)
	if firstAny["role"] != "system" || firstAny["content"] != "Be concise" {
		t.Fatalf("expected prepended system message, got %#v", firstAny)
	}

	mapSlice := []map[string]any{
		{"role": "user", "content": "Hello"},
	}
	gotMap, err := buildMessagesField(mapSlice, "Be concise", "")
	if err != nil {
		t.Fatalf("buildMessagesField([]map[string]any): %v", err)
	}
	msgsMap, ok := gotMap.([]map[string]any)
	if !ok || len(msgsMap) != 2 {
		t.Fatalf("expected 2 messages in []map[string]any with system prompt prepended, got %#v", gotMap)
	}
	if msgsMap[0]["role"] != "system" || msgsMap[0]["content"] != "Be concise" {
		t.Fatalf("expected prepended system message, got %#v", msgsMap[0])
	}

	// Ensure leading system message is not duplicated when already present in messages.
	alreadyWithSys := []map[string]any{
		{"role": "system", "content": "Be concise"},
		{"role": "user", "content": "Hello"},
	}
	gotNoDup, err := buildMessagesField(alreadyWithSys, "Be concise", "")
	if err != nil {
		t.Fatalf("buildMessagesField(alreadyWithSys): %v", err)
	}
	msgsNoDup, ok := gotNoDup.([]map[string]any)
	if !ok || len(msgsNoDup) != 2 {
		t.Fatalf("expected 2 messages without duplicate system message, got %#v", gotNoDup)
	}
}

func TestExtractPromptFromMessagesParamMapSlice(t *testing.T) {
	msgs := []map[string]any{
		{"role": "system", "content": "System prompt"},
		{"role": "user", "content": "Final user prompt"},
	}
	if got := extractPromptFromMessagesParam(msgs); got != "Final user prompt" {
		t.Fatalf("extractPromptFromMessagesParam([]map[string]any) = %q, want %q", got, "Final user prompt")
	}
}

func TestNormalizeLLMDialectDocumentedAliases(t *testing.T) {
	if got := normalizeLLMDialect("vercel_ai_data_stream", ""); got != LLMDialectVercelAI {
		t.Fatalf("normalizeLLMDialect(vercel_ai_data_stream) = %q, want %q", got, LLMDialectVercelAI)
	}
	if got := normalizeLLMDialect("langserve_sse", ""); got != LLMDialectLangServe {
		t.Fatalf("normalizeLLMDialect(langserve_sse) = %q, want %q", got, LLMDialectLangServe)
	}
}

func TestLLMStreamHistoryVarAndStreamModeAliases(t *testing.T) {
	fx := llmfixture.NewServer(llmfixture.Config{
		OutputTokens: 4,
	})
	defer fx.Close()

	p := &HTTP{}
	defer func() { _ = p.Teardown(context.Background()) }()
	if err := p.Init(context.Background(), map[string]any{"base_url": fx.URL}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// 1. Streaming llm_request with history_var captures assistant text into ProtocolEvidence["__llm_assistant_text"]
	// and supports response_var / finish_reason_var aliases.
	res, err := p.Execute(context.Background(), &protocol.Action{
		Name:   "hist_var_stream",
		Type:   "llm_request",
		Target: fx.URL + "/v1/chat/completions",
		Params: map[string]any{
			"prompt":            "hello",
			"system_prompt":     "be helpful",
			"ignore_eos":        true,
			"history_var":       "chat_hist",
			"response_var":      "resp_text",
			"finish_reason_var": "fin_reason",
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success, got error: %s", res.ErrorMessage)
	}
	asst, _ := res.ProtocolEvidence["__llm_assistant_text"].(string)
	if strings.TrimSpace(asst) == "" {
		t.Fatalf("expected non-empty __llm_assistant_text when history_var is set, got %#v", res.ProtocolEvidence)
	}
	if res.Extractions["resp_text"] == "" || res.Extractions["fin_reason"] == "" {
		t.Fatalf("expected resp_text and fin_reason extractions, got %#v", res.Extractions)
	}

	// 2. Verify prepareLLMRequestAction preserves stream: "ndjson" as stream_mode="ndjson" and honors stream_mode="none".
	ndjsonAct, err := prepareLLMRequestAction(&protocol.Action{
		Type: "llm_request",
		Params: map[string]any{
			"prompt": "hello ndjson",
			"stream": "ndjson",
		},
	}, fx.URL+"/api/chat")
	if err != nil {
		t.Fatalf("prepareLLMRequestAction(ndjson): %v", err)
	}
	if ndjsonAct.Params["stream"] != true || ndjsonAct.Params["stream_mode"] != "ndjson" {
		t.Fatalf("expected stream=true and stream_mode=ndjson, got %#v", ndjsonAct.Params)
	}
	if !shouldReadNDJSON("application/json", ndjsonAct.Params) {
		t.Fatalf("expected shouldReadNDJSON=true when stream_mode=ndjson")
	}

	nonStreamAct, err := prepareLLMRequestAction(&protocol.Action{
		Type: "llm_request",
		Params: map[string]any{
			"prompt":      "hello non-stream",
			"stream_mode": "none",
		},
	}, fx.URL+"/v1/chat/completions")
	if err != nil {
		t.Fatalf("prepareLLMRequestAction(stream_mode=none): %v", err)
	}
	if nonStreamAct.Params["stream"] != false {
		t.Fatalf("expected stream=false when stream_mode=none, got %#v", nonStreamAct.Params)
	}
}
