package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/llmfixture"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func newLLMAccountingHTTP(t *testing.T, baseURL string) *HTTP {
	t.Helper()
	p := &HTTP{}
	t.Cleanup(func() { _ = p.Teardown(context.Background()) })
	if err := p.Init(context.Background(), map[string]any{"base_url": baseURL}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return p
}

func executeLLMAccounting(t *testing.T, p *HTTP, target string, params map[string]any) *protocol.Result {
	t.Helper()
	res, err := p.Execute(context.Background(), &protocol.Action{Name: "llm", Type: "llm_request", Target: target, Params: params})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ServerTelemetry == nil || res.ServerTelemetry.LLM == nil {
		t.Fatalf("expected LLM diagnostics, got result %+v", res)
	}
	return res
}

// A fixed-output benchmark (ignore_eos up to max_tokens) ends every request
// with finish_reason=length: that is the requested stop, not a truncation.
func TestLLMLengthFinishAccounting(t *testing.T) {
	fx := llmfixture.NewServer(llmfixture.Config{BaseTTFT: time.Millisecond, BaseITL: time.Millisecond, OutputTokens: 8})
	defer fx.Close()
	p := newLLMAccountingHTTP(t, fx.URL)
	target := fx.URL + "/v1/chat/completions"

	cases := []struct {
		name          string
		extra         map[string]any
		wantSuccess   bool
		wantTruncated bool
	}{
		{name: "default fails a length finish", wantSuccess: false, wantTruncated: true},
		{name: "ignore_eos makes length the requested stop", extra: map[string]any{"ignore_eos": true}, wantSuccess: true, wantTruncated: false},
		{name: "extra_body ignore_eos", extra: map[string]any{"extra_body": map[string]any{"ignore_eos": true}}, wantSuccess: true, wantTruncated: false},
		{name: "fail_on_truncation false accepts but counts it", extra: map[string]any{"fail_on_truncation": false}, wantSuccess: true, wantTruncated: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{"model": "m", "prompt": "hello", "max_tokens": 4}
			for k, v := range tc.extra {
				params[k] = v
			}
			res := executeLLMAccounting(t, p, target, params)
			llm := res.ServerTelemetry.LLM
			if llm.FinishReason != "length" {
				t.Fatalf("FinishReason = %q, want length", llm.FinishReason)
			}
			if res.Success != tc.wantSuccess || llm.Truncated != tc.wantTruncated {
				t.Fatalf("Success=%v Truncated=%v (err %q), want Success=%v Truncated=%v", res.Success, llm.Truncated, res.ErrorMessage, tc.wantSuccess, tc.wantTruncated)
			}
		})
	}
}

// A stream the client cut off has incomplete timings: no override may turn
// it into a success, or it would enter the latency sketches.
func TestLLMStreamCutOffAlwaysFails(t *testing.T) {
	fx := llmfixture.NewServer(llmfixture.Config{
		BaseTTFT:     time.Millisecond,
		BaseITL:      time.Millisecond,
		OutputTokens: 8,
		Fault:        llmfixture.FaultConfig{Kind: llmfixture.FaultStall, EveryN: 1, TruncateAfter: 2, StallDuration: 2 * time.Second},
	})
	defer fx.Close()
	p := newLLMAccountingHTTP(t, fx.URL)
	res := executeLLMAccounting(t, p, fx.URL+"/v1/chat/completions", map[string]any{
		"model":              "m",
		"prompt":             "hello",
		"stream_timeout_ms":  200,
		"ignore_eos":         true,
		"fail_on_truncation": false,
	})
	llm := res.ServerTelemetry.LLM
	if res.Success || !llm.Truncated || llm.ErrorCode != "stream_timeout" {
		t.Fatalf("Success=%v Truncated=%v ErrorCode=%q (err %q), want a failed stream_timeout", res.Success, llm.Truncated, llm.ErrorCode, res.ErrorMessage)
	}
}

// A request that never reaches a response still counts in every LLM
// denominator as a failed request.
func TestLLMTransportFailureCarriesDiagnostics(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	base := "http://" + addr
	p := newLLMAccountingHTTP(t, base)
	res := executeLLMAccounting(t, p, base+"/v1/chat/completions", map[string]any{"model": "gpt-test", "prompt": "hello"})
	llm := res.ServerTelemetry.LLM
	if res.Success {
		t.Fatalf("expected a failed request")
	}
	if llm.ErrorCode != "transport" || llm.Dialect != LLMDialectOpenAIChat || llm.Model != "gpt-test" {
		t.Fatalf("unexpected failure diagnostics %+v", llm)
	}
	if llm.PromptTokens != 0 || llm.CompletionTokens != 0 || llm.InputLengthBand == "" {
		t.Fatalf("a request that never reached the server counts no tokens but keeps its band, got %+v", llm)
	}
}

func TestLLMTritonCleanEOFIsComplete(t *testing.T) {
	fx := llmfixture.NewServer(llmfixture.Config{BaseTTFT: time.Millisecond, BaseITL: time.Millisecond, OutputTokens: 6})
	defer fx.Close()
	p := newLLMAccountingHTTP(t, fx.URL)
	res := executeLLMAccounting(t, p, fx.URL+"/v2/models/ensemble/generate_stream", map[string]any{"dialect": "triton", "prompt": "hello"})
	llm := res.ServerTelemetry.LLM
	if !res.Success || llm.Truncated || llm.FinishReason != "" {
		t.Fatalf("Success=%v Truncated=%v FinishReason=%q (err %q), want a complete stream", res.Success, llm.Truncated, llm.FinishReason, res.ErrorMessage)
	}
	if llm.TTFTMs <= 0 || llm.ITLDistribution == nil || llm.ITLDistribution.Count != 5 {
		t.Fatalf("expected TTFT and 5 ITL gaps, got %+v", llm)
	}
}

func TestLLMResponsesAPIStreaming(t *testing.T) {
	fx := llmfixture.NewServer(llmfixture.Config{
		BaseTTFT:           time.Millisecond,
		BaseITL:            time.Millisecond,
		OutputTokens:       6,
		ReasoningTokens:    2,
		CachedPromptTokens: 3,
		OutputText:         "alpha beta gamma delta epsilon zeta",
	})
	defer fx.Close()
	p := newLLMAccountingHTTP(t, fx.URL)
	res := executeLLMAccounting(t, p, fx.URL+"/v1/responses", map[string]any{
		"model":        "gpt-test",
		"prompt":       "hello",
		"max_tokens":   64,
		"save_text_as": "reply",
	})
	llm := res.ServerTelemetry.LLM
	if !res.Success || llm.Truncated {
		t.Fatalf("Success=%v Truncated=%v (err %q), want a complete Responses stream", res.Success, llm.Truncated, res.ErrorMessage)
	}
	if llm.Dialect != LLMDialectOpenAIResponses || llm.FinishReason != "stop" || llm.Model != "gpt-test" {
		t.Fatalf("unexpected dialect/finish/model %+v", llm)
	}
	if llm.TokensEstimated || llm.CompletionTokens != 8 || llm.ReasoningTokens != 2 || llm.CachedTokens != 3 || llm.PromptTokens <= 0 {
		t.Fatalf("expected server usage from the terminal event, got %+v", llm)
	}
	// Reasoning deltas start the clock; the terminal event's repeated
	// output text must not count as another token.
	if llm.TTFTMs <= 0 || llm.FirstVisibleTokenMs < llm.TTFTMs || llm.ITLDistribution == nil || llm.ITLDistribution.Count != 7 {
		t.Fatalf("unexpected timings %+v", llm)
	}
	if got := res.Extractions["reply"]; got != "alpha beta gamma delta epsilon zeta" {
		t.Fatalf("saved text = %q", got)
	}
}

func TestLLMResponsesAPIFinishes(t *testing.T) {
	t.Run("max_output_tokens is a length finish", func(t *testing.T) {
		fx := llmfixture.NewServer(llmfixture.Config{BaseTTFT: time.Millisecond, BaseITL: time.Millisecond, OutputTokens: 40})
		defer fx.Close()
		p := newLLMAccountingHTTP(t, fx.URL)
		res := executeLLMAccounting(t, p, fx.URL+"/v1/responses", map[string]any{"model": "m", "prompt": "hi", "max_tokens": 20, "ignore_eos": true})
		if llm := res.ServerTelemetry.LLM; !res.Success || llm.FinishReason != "length" || llm.Truncated || llm.CompletionTokens != 20 {
			t.Fatalf("Success=%v diag=%+v (err %q)", res.Success, llm, res.ErrorMessage)
		}
	})
	t.Run("tool call", func(t *testing.T) {
		fx := llmfixture.NewServer(llmfixture.Config{BaseTTFT: time.Millisecond, BaseITL: time.Millisecond, OutputTokens: 2, ToolCallName: "lookup"})
		defer fx.Close()
		p := newLLMAccountingHTTP(t, fx.URL)
		res := executeLLMAccounting(t, p, fx.URL+"/v1/responses", map[string]any{"model": "m", "prompt": "hi", "save_tool_calls_as": "calls"})
		llm := res.ServerTelemetry.LLM
		if !res.Success || llm.FinishReason != "tool_calls" || llm.ToolCallsCount != 1 {
			t.Fatalf("Success=%v diag=%+v (err %q)", res.Success, llm, res.ErrorMessage)
		}
		if !strings.Contains(res.Extractions["calls"], `"name":"lookup"`) {
			t.Fatalf("tool calls JSON = %q", res.Extractions["calls"])
		}
	})
	t.Run("error event", func(t *testing.T) {
		fx := llmfixture.NewServer(llmfixture.Config{
			BaseTTFT: time.Millisecond, BaseITL: time.Millisecond, OutputTokens: 6,
			Fault: llmfixture.FaultConfig{Kind: llmfixture.FaultStreamError, EveryN: 1, TruncateAfter: 2, ErrorCode: "server_error"},
		})
		defer fx.Close()
		p := newLLMAccountingHTTP(t, fx.URL)
		res := executeLLMAccounting(t, p, fx.URL+"/v1/responses", map[string]any{"model": "m", "prompt": "hi"})
		if llm := res.ServerTelemetry.LLM; res.Success || !llm.HTTP200Error || llm.ErrorCode != "server_error" {
			t.Fatalf("Success=%v diag=%+v", res.Success, llm)
		}
	})
	t.Run("non-streaming", func(t *testing.T) {
		fx := llmfixture.NewServer(llmfixture.Config{BaseTTFT: time.Millisecond, BaseITL: time.Millisecond, OutputTokens: 4, CachedPromptTokens: 1})
		defer fx.Close()
		p := newLLMAccountingHTTP(t, fx.URL)
		res := executeLLMAccounting(t, p, fx.URL+"/v1/responses", map[string]any{"model": "m", "prompt": "hi", "stream": false, "save_text_as": "reply"})
		llm := res.ServerTelemetry.LLM
		if !res.Success || llm.Stream || llm.FinishReason != "stop" || llm.CompletionTokens != 4 || llm.CachedTokens != 1 || llm.TokensEstimated {
			t.Fatalf("Success=%v diag=%+v (err %q)", res.Success, llm, res.ErrorMessage)
		}
		if res.Extractions["reply"] == "" {
			t.Fatalf("expected the output text to be saved")
		}
	})
}

func TestBuildLLMRequestBodyResponses(t *testing.T) {
	body, err := buildLLMRequestBody(LLMDialectOpenAIResponses, map[string]any{
		"model":       "gpt-test",
		"prompt":      "hello",
		"system":      "be brief",
		"max_tokens":  32,
		"temperature": 0.0,
		"json_schema": map[string]any{"type": "object", "required": []any{"a"}},
	}, true)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["input"] != "hello" || got["instructions"] != "be brief" || got["max_output_tokens"] != float64(32) || got["stream"] != true {
		t.Fatalf("unexpected Responses body %s", body)
	}
	for _, chatOnly := range []string{"messages", "max_tokens", "stream_options", "response_format"} {
		if _, ok := got[chatOnly]; ok {
			t.Fatalf("Responses body must not carry %q: %s", chatOnly, body)
		}
	}
	format, _ := got["text"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" || format["name"] != "response" || format["schema"] == nil {
		t.Fatalf("unexpected text.format %v", got["text"])
	}

	withMessages, err := buildLLMRequestBody(LLMDialectOpenAIResponses, map[string]any{
		"messages": `[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"},{"role":"user","content":"more"}]`,
	}, false)
	if err != nil {
		t.Fatalf("build messages: %v", err)
	}
	if !strings.Contains(string(withMessages), `"input":[{"role":"user","content":"hi"}`) {
		t.Fatalf("chat messages must become input items: %s", withMessages)
	}
}

func TestNormalizeLLMDialectResponses(t *testing.T) {
	for _, tc := range []struct{ raw, target string }{
		{raw: "openai_responses"},
		{raw: "responses"},
		{raw: "azure_openai_responses"},
		{target: "https://api.openai.com/v1/responses"},
		{target: "https://res.openai.azure.com/openai/v1/responses"},
		{target: "https://res.openai.azure.com/openai/responses?api-version=2025-04-01-preview"},
	} {
		if got := normalizeLLMDialect(tc.raw, tc.target); got != LLMDialectOpenAIResponses {
			t.Errorf("normalizeLLMDialect(%q, %q) = %q, want %q", tc.raw, tc.target, got, LLMDialectOpenAIResponses)
		}
	}
	if got := normalizeLLMDialect("", "https://res.openai.azure.com/openai/deployments/d/chat/completions"); got != LLMDialectAzureOpenAI {
		t.Errorf("Azure chat deployment = %q, want %q", got, LLMDialectAzureOpenAI)
	}
}

// BenchmarkLLMResponsesDeltaChunk guards the per-chunk cost of the Responses
// dialect: a text delta must not allocate.
func BenchmarkLLMResponsesDeltaChunk(b *testing.B) {
	obs := newLLMStreamObserver(LLMDialectOpenAIResponses, time.Now(), false)
	defer obs.close()
	data := []byte(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"token"}`)
	now := time.Now()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		obs.observeChunk("response.output_text.delta", data, now)
		obs.observeChunk("", data, now)
	}
}

// scriptedSSEServer writes the given SSE frames, flushing each, then holds
// the connection open for hold (until the client goes away).
func scriptedSSEServer(t *testing.T, frames []string, hold time.Duration) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(nethttp.StatusOK)
		flusher, _ := w.(nethttp.Flusher)
		for _, frame := range frames {
			_, _ = io.WriteString(w, frame)
			if flusher != nil {
				flusher.Flush()
			}
		}
		select {
		case <-r.Context().Done():
		case <-time.After(hold):
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// A length finish followed by a stall is still a stream the client cut off:
// it fails whatever the overrides, and its timings stay out of the sketches.
func TestLLMLengthFinishThenStallFails(t *testing.T) {
	server := scriptedSSEServer(t, []string{
		"data: {\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"},\"finish_reason\":null}]}\n\n",
		"data: {\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"b\"},\"finish_reason\":\"length\"}]}\n\n",
	}, 5*time.Second)
	p := newLLMAccountingHTTP(t, server.URL)
	for _, extra := range []map[string]any{{"ignore_eos": true}, {"fail_on_truncation": false}} {
		params := map[string]any{"model": "m", "prompt": "hi", "stream_timeout_ms": 200}
		for k, v := range extra {
			params[k] = v
		}
		res := executeLLMAccounting(t, p, server.URL+"/v1/chat/completions", params)
		if llm := res.ServerTelemetry.LLM; res.Success || !llm.Truncated || llm.ErrorCode != "stream_timeout" {
			t.Fatalf("%v: Success=%v Truncated=%v ErrorCode=%q, want a failed stream_timeout", extra, res.Success, llm.Truncated, llm.ErrorCode)
		}
	}
}

func TestLLMResponsesTerminalEvents(t *testing.T) {
	completed := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"m\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"hi\"}]}],\"usage\":{\"input_tokens\":3,\"output_tokens\":2,\"total_tokens\":5}}}\n\n"
	delta := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"
	terminal := func(status, details, errObj string) string {
		return fmt.Sprintf("data: {\"type\":\"response.%s\",\"response\":{\"status\":%q,\"incomplete_details\":%s,\"error\":%s,\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n", status, status, details, errObj)
	}
	cases := []struct {
		name        string
		frames      []string
		wantSuccess bool
		wantCode    string
	}{
		// The stream is over at its terminal event even if the connection stays open.
		{name: "completed then held open", frames: []string{delta, completed}, wantSuccess: true},
		// Without event: lines, the payload's type decides, wherever it sits.
		{name: "type written last", frames: []string{
			"data: {\"delta\":\"hi\",\"type\":\"response.output_text.delta\"}\n\n",
			"data: {\"response\":{\"status\":\"completed\",\"output\":[{\"content\":[{\"text\":\"hi\",\"type\":\"output_text\"}],\"type\":\"message\"}],\"usage\":{\"input_tokens\":3,\"output_tokens\":2}},\"type\":\"response.completed\"}\n\n",
		}, wantSuccess: true},
		{name: "nested error event", frames: []string{delta, "data: {\"type\":\"error\",\"error\":{\"code\":\"server_error\",\"message\":\"boom\"}}\n\n"}, wantCode: "server_error"},
		{name: "incomplete without details", frames: []string{delta, terminal("incomplete", "null", "null")}, wantCode: "response_incomplete"},
		{name: "cancelled", frames: []string{delta, terminal("cancelled", "null", "null")}, wantCode: "response_cancelled"},
		{name: "failed without an error object", frames: []string{delta, terminal("failed", "null", "null")}, wantCode: "response_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := scriptedSSEServer(t, tc.frames, 5*time.Second)
			p := newLLMAccountingHTTP(t, server.URL)
			started := time.Now()
			res := executeLLMAccounting(t, p, server.URL+"/v1/responses", map[string]any{"model": "m", "prompt": "hi", "stream_timeout_ms": 2000})
			llm := res.ServerTelemetry.LLM
			if res.Success != tc.wantSuccess || (tc.wantCode != "" && llm.ErrorCode != tc.wantCode) {
				t.Fatalf("Success=%v ErrorCode=%q (err %q), want Success=%v ErrorCode=%q", res.Success, llm.ErrorCode, res.ErrorMessage, tc.wantSuccess, tc.wantCode)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("read for %s: the terminal event must end the stream", elapsed)
			}
		})
	}
}

func TestLLMResponsesProviderDialectAndOutputFloor(t *testing.T) {
	for _, raw := range []string{"openai", "azure", "foundry", "vllm"} {
		if got := normalizeLLMDialect(raw, "https://res.openai.azure.com/openai/v1/responses"); got != LLMDialectOpenAIResponses {
			t.Errorf("normalizeLLMDialect(%q, a /responses path) = %q, want %q", raw, got, LLMDialectOpenAIResponses)
		}
	}
	if got := normalizeLLMDialect("openai_chat", "https://api.openai.com/v1/responses"); got != LLMDialectOpenAIChat {
		t.Errorf("an explicit wire dialect must win, got %q", got)
	}
	body, err := buildLLMRequestBody(LLMDialectOpenAIResponses, map[string]any{"prompt": "hi", "max_tokens": 1}, true)
	if err != nil || !strings.Contains(string(body), `"max_output_tokens":16`) {
		t.Fatalf("max_output_tokens below 16 must be raised to the API's minimum: %s %v", body, err)
	}
}

// pacedSSEServer writes each frame after its delay, flushing it, then ends
// the response.
func pacedSSEServer(t *testing.T, frames []string, delays []time.Duration) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(nethttp.StatusOK)
		flusher, _ := w.(nethttp.Flusher)
		for i, frame := range frames {
			if i < len(delays) && delays[i] > 0 {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(delays[i]):
				}
			}
			_, _ = io.WriteString(w, frame)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// A reasoning model that does not stream its reasoning generates those
// tokens before the first visible one: TPOT is the visible cadence, not the
// span divided by every output token.
func TestLLMTPOTExcludesUnstreamedReasoningTokens(t *testing.T) {
	delta := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n"
	completed := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"o-mini\",\"output\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":105,\"output_tokens_details\":{\"reasoning_tokens\":100},\"total_tokens\":115}}}\n\n"
	gap := 20 * time.Millisecond
	server := pacedSSEServer(t,
		[]string{delta, delta, delta, delta, delta, completed},
		[]time.Duration{50 * time.Millisecond, gap, gap, gap, gap, 0})
	p := newLLMAccountingHTTP(t, server.URL)
	res := executeLLMAccounting(t, p, server.URL+"/v1/responses", map[string]any{"model": "o-mini", "prompt": "hi"})
	llm := res.ServerTelemetry.LLM
	if !res.Success || llm.CompletionTokens != 105 || llm.ReasoningTokens != 100 {
		t.Fatalf("Success=%v diag=%+v (err %q)", res.Success, llm, res.ErrorMessage)
	}
	if llm.TPOTMs < 10 || llm.TPOTMs > 60 {
		t.Fatalf("TPOT = %.2f ms, want the ~20 ms visible cadence", llm.TPOTMs)
	}
}

// TGI reports the finish reason and token count only with details: the
// request asks for them, and a stream without them still ends complete at
// its final generated_text event.
func TestLLMTGIDetailsAndFinalEvent(t *testing.T) {
	body, err := buildLLMRequestBody(LLMDialectTGI, map[string]any{"prompt": "hi", "max_tokens": 8}, true)
	if err != nil || !strings.Contains(string(body), `"details":true`) {
		t.Fatalf("a TGI request must ask for details: %s %v", body, err)
	}
	fx := llmfixture.NewServer(llmfixture.Config{BaseTTFT: time.Millisecond, BaseITL: time.Millisecond, OutputTokens: 6})
	defer fx.Close()
	p := newLLMAccountingHTTP(t, fx.URL)
	res := executeLLMAccounting(t, p, fx.URL+"/generate_stream", map[string]any{"dialect": "tgi", "prompt": "hello"})
	if llm := res.ServerTelemetry.LLM; !res.Success || llm.FinishReason != "stop" || llm.TokensEstimated || llm.CompletionTokens != 6 {
		t.Fatalf("with details: Success=%v diag=%+v (err %q)", res.Success, llm, res.ErrorMessage)
	}
	res = executeLLMAccounting(t, p, fx.URL+"/generate_stream", map[string]any{"dialect": "tgi", "prompt": "hello", "extra_body": map[string]any{"parameters": map[string]any{"details": false}}})
	if llm := res.ServerTelemetry.LLM; !res.Success || llm.Truncated || llm.FinishReason != "stop" {
		t.Fatalf("without details: Success=%v Truncated=%v diag=%+v (err %q)", res.Success, llm.Truncated, llm, res.ErrorMessage)
	}
}

// TGI reports the prompt as input_length in stream details and in the
// x-prompt-tokens header on /generate; a missing count is estimated, never
// reported as zero, so the input band follows the real prompt.
func TestLLMTGIPromptTokens(t *testing.T) {
	fx := llmfixture.NewServer(llmfixture.Config{BaseTTFT: time.Millisecond, BaseITL: time.Millisecond, OutputTokens: 4})
	defer fx.Close()
	p := newLLMAccountingHTTP(t, fx.URL)
	longPrompt := strings.Repeat("context ", 2000) // about 4000 tokens: the long band
	for _, tc := range []struct {
		name          string
		path          string
		stream        bool
		extra         map[string]any
		wantEstimated bool
	}{
		{name: "stream details", path: "/generate_stream", stream: true},
		{name: "non-stream header", path: "/generate", stream: false},
		{name: "stream without details", path: "/generate_stream", stream: true, extra: map[string]any{"parameters": map[string]any{"details": false}}, wantEstimated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{"dialect": "tgi", "prompt": longPrompt, "stream": tc.stream}
			if tc.extra != nil {
				params["extra_body"] = tc.extra
			}
			res := executeLLMAccounting(t, p, fx.URL+tc.path, params)
			llm := res.ServerTelemetry.LLM
			if !res.Success || llm.PromptTokens < 2000 || llm.InputLengthBand != "long" || llm.TotalTokens != llm.PromptTokens+llm.CompletionTokens {
				t.Fatalf("Success=%v prompt=%d band=%q total=%d completion=%d (err %q)", res.Success, llm.PromptTokens, llm.InputLengthBand, llm.TotalTokens, llm.CompletionTokens, res.ErrorMessage)
			}
			if llm.TokensEstimated != tc.wantEstimated || llm.PromptTokensEstimated {
				t.Fatalf("TokensEstimated=%v PromptTokensEstimated=%v, want %v and no prompt-only estimate", llm.TokensEstimated, llm.PromptTokensEstimated, tc.wantEstimated)
			}
		})
	}
	// Details without input_length (an older TGI): the completion count is
	// the server's and only the prompt is estimated.
	older := scriptedSSEServer(t, []string{
		"data: {\"token\":{\"id\":1,\"text\":\"a\"},\"generated_text\":null,\"details\":null}\n\n",
		"data: {\"token\":{\"id\":2,\"text\":\"b\"},\"generated_text\":\"ab\",\"details\":{\"finish_reason\":\"eos_token\",\"generated_tokens\":2}}\n\n",
	}, 0)
	p = newLLMAccountingHTTP(t, older.URL)
	res := executeLLMAccounting(t, p, older.URL+"/generate_stream", map[string]any{"dialect": "tgi", "prompt": longPrompt})
	if llm := res.ServerTelemetry.LLM; !res.Success || llm.CompletionTokens != 2 || llm.PromptTokens < 2000 || !llm.TokensEstimated || !llm.PromptTokensEstimated {
		t.Fatalf("Success=%v diag=%+v (err %q)", res.Success, llm, res.ErrorMessage)
	}
}

// A reasoning summary is streamed reasoning output, but not the reasoning
// tokens usage counts: TPOT stays the visible answer's cadence.
func TestLLMTPOTWithAStreamedReasoningSummary(t *testing.T) {
	summary := "event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"plan\"}\n\n"
	delta := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n"
	completed := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":105,\"output_tokens_details\":{\"reasoning_tokens\":100},\"total_tokens\":115}}}\n\n"
	gap := 20 * time.Millisecond
	// A long pause between the summary and the answer: timing the whole
	// span would put it into TPOT.
	server := pacedSSEServer(t,
		[]string{summary, summary, delta, delta, delta, delta, delta, completed},
		[]time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 200 * time.Millisecond, gap, gap, gap, gap, 0})
	p := newLLMAccountingHTTP(t, server.URL)
	res := executeLLMAccounting(t, p, server.URL+"/v1/responses", map[string]any{"model": "o-mini", "prompt": "hi"})
	llm := res.ServerTelemetry.LLM
	if !res.Success || llm.TTFTMs >= llm.FirstVisibleTokenMs {
		t.Fatalf("Success=%v TTFT=%.1f first visible=%.1f (err %q)", res.Success, llm.TTFTMs, llm.FirstVisibleTokenMs, res.ErrorMessage)
	}
	if llm.TPOTMs < 10 || llm.TPOTMs > 60 {
		t.Fatalf("TPOT = %.2f ms, want the ~20 ms visible cadence", llm.TPOTMs)
	}
	// Five visible tokens: four gaps cover at most the time after the first.
	if llm.TPOTMs*4 > llm.E2EMs-llm.FirstVisibleTokenMs+10 {
		t.Fatalf("TPOT %.2f ms reaches back before the visible answer (E2E %.1f, first visible %.1f)", llm.TPOTMs, llm.E2EMs, llm.FirstVisibleTokenMs)
	}
}

// A server that streams its reasoning only as a summary and counts none of
// it (Ollama's /v1/responses) has streamed the reasoning itself: the share is
// an estimate and TPOT times every token, summary included.
func TestLLMUncountedStreamedSummaryIsTheReasoning(t *testing.T) {
	summary := "event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"think\"}\n\n"
	delta := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n"
	completed := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":10,\"output_tokens_details\":{\"reasoning_tokens\":0},\"total_tokens\":20}}}\n\n"
	gap := 10 * time.Millisecond
	frames, delays := []string{}, []time.Duration{}
	for i := 0; i < 5; i++ {
		frames, delays = append(frames, summary), append(delays, gap)
	}
	for i := 0; i < 5; i++ {
		frames, delays = append(frames, delta), append(delays, gap)
	}
	frames, delays = append(frames, completed), append(delays, 0)
	server := pacedSSEServer(t, frames, delays)
	p := newLLMAccountingHTTP(t, server.URL)
	res := executeLLMAccounting(t, p, server.URL+"/v1/responses", map[string]any{"model": "qwen3", "prompt": "hi"})
	llm := res.ServerTelemetry.LLM
	if !res.Success || !llm.ReasoningTokensEstimated || llm.ReasoningTokens != 5 {
		t.Fatalf("Success=%v reasoning=%d estimated=%v, want five estimated reasoning tokens (err %q)", res.Success, llm.ReasoningTokens, llm.ReasoningTokensEstimated, res.ErrorMessage)
	}
	// Ten tokens 10 ms apart: TPOT is that cadence, not the visible span's
	// 40 ms spread over all ten.
	if llm.TPOTMs < 6 || llm.TPOTMs > 30 {
		t.Fatalf("TPOT = %.2f ms, want about the 10 ms cadence", llm.TPOTMs)
	}
}

// A stream stopped before its usage (max_events here) has no server word on
// its reasoning: an OpenAI-style summary stays a summary, and TPOT times the
// visible answer.
func TestLLMStreamStoppedBeforeUsageKeepsTheSummaryASummary(t *testing.T) {
	summary := "event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"plan\"}\n\n"
	delta := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"word\"}\n\n"
	completed := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":510,\"output_tokens_details\":{\"reasoning_tokens\":500},\"total_tokens\":520}}}\n\n"
	frames, delays := []string{}, []time.Duration{}
	for i := 0; i < 5; i++ {
		frames, delays = append(frames, summary), append(delays, 50*time.Millisecond)
	}
	for i := 0; i < 10; i++ {
		frames, delays = append(frames, delta), append(delays, 10*time.Millisecond)
	}
	frames, delays = append(frames, completed), append(delays, 0)
	server := pacedSSEServer(t, frames, delays)
	p := newLLMAccountingHTTP(t, server.URL)
	res := executeLLMAccounting(t, p, server.URL+"/v1/responses", map[string]any{"model": "o-mini", "prompt": "hi", "max_events": 15})
	llm := res.ServerTelemetry.LLM
	if llm.ReasoningTokens != 0 || llm.ReasoningTokensEstimated {
		t.Fatalf("reasoning=%d estimated=%v, want the summary left out of the counts", llm.ReasoningTokens, llm.ReasoningTokensEstimated)
	}
	if llm.TPOTMs > 30 {
		t.Fatalf("TPOT = %.2f ms, want the ~10 ms visible cadence", llm.TPOTMs)
	}
}

// When the visible answer has no length to time (a function call only, or a
// single delta), TPOT falls back from the first visible token, never from a
// reasoning summary.
func TestLLMTPOTFallbackStartsAtTheVisibleAnswer(t *testing.T) {
	summary := "event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"plan\"}\n\n"
	args := "event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{}\"}\n\n"
	delta := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n"
	completed := func(output string) string {
		return "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[" + output + "],\"usage\":{\"input_tokens\":10,\"output_tokens\":106,\"output_tokens_details\":{\"reasoning_tokens\":100},\"total_tokens\":116}}}\n\n"
	}
	step := 20 * time.Millisecond
	toolOnly := pacedSSEServer(t,
		[]string{summary, summary, summary, args, args, completed(`{"type":"function_call","name":"f","arguments":"{}"}`)},
		[]time.Duration{step, step, step, step, step, 0})
	p := newLLMAccountingHTTP(t, toolOnly.URL)
	res := executeLLMAccounting(t, p, toolOnly.URL+"/v1/responses", map[string]any{"model": "o-mini", "prompt": "hi"})
	if llm := res.ServerTelemetry.LLM; !res.Success || llm.TPOTMs != 0 {
		t.Fatalf("a function-call-only turn has no visible answer to time: TPOT=%.2f (err %q)", llm.TPOTMs, res.ErrorMessage)
	}
	single := pacedSSEServer(t,
		[]string{summary, summary, summary, summary, summary, delta, completed(`{"type":"message","content":[{"type":"output_text","text":"x"}]}`)},
		[]time.Duration{step, step, step, step, step, step, 0})
	p = newLLMAccountingHTTP(t, single.URL)
	res = executeLLMAccounting(t, p, single.URL+"/v1/responses", map[string]any{"model": "o-mini", "prompt": "hi"})
	llm := res.ServerTelemetry.LLM
	if !res.Success || llm.TPOTMs*5 > llm.E2EMs-llm.FirstVisibleTokenMs+10 {
		t.Fatalf("TPOT %.2f ms counts the summary (E2E %.1f, first visible %.1f, err %q)", llm.TPOTMs, llm.E2EMs, llm.FirstVisibleTokenMs, res.ErrorMessage)
	}
}

// A usage report with only a total is kept as the server's total.
func TestLLMTotalOnlyUsageIsKept(t *testing.T) {
	server := scriptedSSEServer(t, []string{
		"data: {\"answer\":\"hello\"}\n\n",
		"data: {\"answer\":\"world\",\"done\":\"stop\",\"usage\":{\"total_tokens\":150}}\n\n",
	}, 0)
	p := newLLMAccountingHTTP(t, server.URL)
	res := executeLLMAccounting(t, p, server.URL+"/chat", map[string]any{
		"prompt":         "hi",
		"stream_profile": map[string]any{"delta_path": "answer", "finish_path": "done", "usage_path": "usage"},
	})
	if llm := res.ServerTelemetry.LLM; llm.TotalTokens != 150 {
		t.Fatalf("TotalTokens = %d, want the server's 150 (diag %+v)", llm.TotalTokens, llm)
	}
}

// Non-streaming Ollama responses carry their answer in message.content
// (/api/chat) or response (/api/generate), their reasoning in thinking and
// their finish in done_reason.
func TestLLMOllamaNonStreamingOutput(t *testing.T) {
	fx := llmfixture.NewServer(llmfixture.Config{BaseTTFT: time.Millisecond, BaseITL: time.Millisecond, OutputTokens: 4, ReasoningTokens: 2})
	defer fx.Close()
	p := newLLMAccountingHTTP(t, fx.URL)
	res := executeLLMAccounting(t, p, fx.URL+"/api/chat", map[string]any{"dialect": "ollama", "prompt": "hello", "stream": false, "save_text_as": "reply"})
	llm := res.ServerTelemetry.LLM
	if !res.Success || llm.OutputChars == 0 || res.Extractions["reply"] == "" || llm.FinishReason != "stop" || llm.ReasoningTokens != 2 || llm.CompletionTokens != 6 || !llm.ReasoningTokensEstimated {
		t.Fatalf("/api/chat: Success=%v diag=%+v reply=%q (err %q)", res.Success, llm, res.Extractions["reply"], res.ErrorMessage)
	}
	generate := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"m","response":"{\"a\":1}","thinking":"hmm","done":true,"done_reason":"length","prompt_eval_count":5,"eval_count":4}`)
	}))
	defer generate.Close()
	p = newLLMAccountingHTTP(t, generate.URL)
	res = executeLLMAccounting(t, p, generate.URL+"/api/generate", map[string]any{"dialect": "ollama", "prompt": "hello", "stream": false, "save_text_as": "reply", "json_schema": map[string]any{"required": []any{"a"}}})
	llm = res.ServerTelemetry.LLM
	if res.Extractions["reply"] != `{"a":1}` || llm.FinishReason != "length" || !llm.Truncated || llm.SchemaFailed {
		t.Fatalf("/api/generate: diag=%+v reply=%q (err %q)", llm, res.Extractions["reply"], res.ErrorMessage)
	}
	// Without eval_count the estimated completion covers the thinking too.
	uncounted := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"m","message":{"role":"assistant","content":"hello world!","thinking":"abc abc abc abc"},"done":true,"done_reason":"stop"}`)
	}))
	defer uncounted.Close()
	p = newLLMAccountingHTTP(t, uncounted.URL)
	res = executeLLMAccounting(t, p, uncounted.URL+"/api/chat", map[string]any{"dialect": "ollama", "prompt": "hello", "stream": false})
	if llm = res.ServerTelemetry.LLM; !llm.TokensEstimated || llm.ReasoningTokens != 4 || llm.CompletionTokens != 7 {
		t.Fatalf("no eval_count: estimated=%v completion=%d reasoning=%d, want 3 visible + 4 reasoning", llm.TokensEstimated, llm.CompletionTokens, llm.ReasoningTokens)
	}
}

// Ollama returns complete tool calls in message.tool_calls, streamed or not.
func TestLLMOllamaToolCalls(t *testing.T) {
	calls := `"tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"Paris"}}},{"function":{"name":"get_time","arguments":{}}}]`
	nonStream := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"m","message":{"role":"assistant","content":"",`+calls+`},"done":true,"done_reason":"stop","prompt_eval_count":3,"eval_count":9}`)
	}))
	defer nonStream.Close()
	p := newLLMAccountingHTTP(t, nonStream.URL)
	res := executeLLMAccounting(t, p, nonStream.URL+"/api/chat", map[string]any{"dialect": "ollama", "prompt": "hi", "stream": false, "save_tool_calls_as": "calls"})
	if llm := res.ServerTelemetry.LLM; !res.Success || llm.ToolCallsCount != 2 || !strings.Contains(res.Extractions["calls"], `"get_time"`) {
		t.Fatalf("non-stream: Success=%v ToolCallsCount=%d calls=%q (err %q)", res.Success, llm.ToolCallsCount, res.Extractions["calls"], res.ErrorMessage)
	}
	stream := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"model":"m","message":{"role":"assistant","content":"",`+calls+`},"done":false}`+"\n")
		_, _ = io.WriteString(w, `{"model":"m","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":3,"eval_count":9}`+"\n")
	}))
	defer stream.Close()
	p = newLLMAccountingHTTP(t, stream.URL)
	res = executeLLMAccounting(t, p, stream.URL+"/api/chat", map[string]any{"dialect": "ollama", "prompt": "hi", "save_tool_calls_as": "calls"})
	if llm := res.ServerTelemetry.LLM; !res.Success || llm.ToolCallsCount != 2 || !strings.Contains(res.Extractions["calls"], `"get_weather"`) {
		t.Fatalf("stream: Success=%v ToolCallsCount=%d calls=%q (err %q)", res.Success, llm.ToolCallsCount, res.Extractions["calls"], res.ErrorMessage)
	}
}

// Usage with a completion count and a total but no prompt count gives the
// prompt exactly as their difference.
func TestLLMPromptTokensDerivedFromServerTotal(t *testing.T) {
	server := scriptedSSEServer(t, []string{
		"data: {\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n",
		"data: {\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"completion_tokens\":20,\"total_tokens\":150}}\n\n",
		"data: [DONE]\n\n",
	}, 0)
	p := newLLMAccountingHTTP(t, server.URL)
	res := executeLLMAccounting(t, p, server.URL+"/v1/chat/completions", map[string]any{"model": "m", "prompt": "hi"})
	if llm := res.ServerTelemetry.LLM; !res.Success || llm.PromptTokens != 130 || llm.TotalTokens != 150 || llm.CompletionTokens != 20 || llm.TokensEstimated || llm.PromptTokensEstimated {
		t.Fatalf("Success=%v prompt=%d completion=%d total=%d estimated=%v/%v, want 130 + 20 = 150 from the server", res.Success, llm.PromptTokens, llm.CompletionTokens, llm.TotalTokens, llm.TokensEstimated, llm.PromptTokensEstimated)
	}
}

// Ollama thinking models stream their reasoning in "thinking": it starts the
// clock, falls inside the decode span, and is reported as reasoning.
func TestLLMOllamaThinkingIsReasoning(t *testing.T) {
	fx := llmfixture.NewServer(llmfixture.Config{BaseTTFT: time.Millisecond, BaseITL: 5 * time.Millisecond, OutputTokens: 5, ReasoningTokens: 3})
	defer fx.Close()
	p := newLLMAccountingHTTP(t, fx.URL)
	res := executeLLMAccounting(t, p, fx.URL+"/api/chat", map[string]any{"dialect": "ollama", "prompt": "hello"})
	llm := res.ServerTelemetry.LLM
	if !res.Success || llm.ReasoningTokens != 3 || llm.CompletionTokens != 8 || llm.TTFTMs >= llm.FirstVisibleTokenMs {
		t.Fatalf("Success=%v completion=%d reasoning=%d TTFT=%.1f first visible=%.1f (err %q)", res.Success, llm.CompletionTokens, llm.ReasoningTokens, llm.TTFTMs, llm.FirstVisibleTokenMs, res.ErrorMessage)
	}
	// Eight tokens 5ms apart: TPOT is the all-token cadence.
	if llm.TPOTMs < 2.5 || llm.TPOTMs > 15 {
		t.Fatalf("TPOT = %.2f ms, want about 5 ms", llm.TPOTMs)
	}
}

func TestLLMResponsesErrorAndEventNameVariants(t *testing.T) {
	delta := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"
	cases := []struct {
		name        string
		frames      []string
		wantSuccess bool
		wantCode    string
	}{
		{name: "error event without a type", frames: []string{delta, "event: error\ndata: {\"error\":{\"code\":\"server_error\",\"message\":\"boom\"}}\n\n"}, wantCode: "server_error"},
		{name: "numeric error code", frames: []string{delta, "event: error\ndata: {\"type\":\"error\",\"code\":500,\"message\":\"boom\"}\n\n"}, wantCode: "500"},
		{name: "bare gateway error payload", frames: []string{delta, "data: {\"error\":{\"code\":\"rate_limited\",\"message\":\"slow down\"}}\n\n"}, wantCode: "rate_limited"},
		{name: "generic event names", frames: []string{
			"event: message\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n",
			"event: message\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"hi\"}]}],\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n",
		}, wantSuccess: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := scriptedSSEServer(t, tc.frames, 5*time.Second)
			p := newLLMAccountingHTTP(t, server.URL)
			started := time.Now()
			res := executeLLMAccounting(t, p, server.URL+"/v1/responses", map[string]any{"model": "m", "prompt": "hi", "stream_timeout_ms": 2000})
			llm := res.ServerTelemetry.LLM
			if res.Success != tc.wantSuccess || (tc.wantCode != "" && (llm.ErrorCode != tc.wantCode || !llm.HTTP200Error)) {
				t.Fatalf("Success=%v ErrorCode=%q HTTP200Error=%v (err %q), want Success=%v ErrorCode=%q", res.Success, llm.ErrorCode, llm.HTTP200Error, res.ErrorMessage, tc.wantSuccess, tc.wantCode)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("read for %s: the terminal or error event must end the stream", elapsed)
			}
		})
	}
}

// OpenAI-compatible servers stream a tool call as its id and name, then its
// arguments in fragments, all under one index: each call counts once and is
// saved whole.
func TestLLMStreamedToolCallDeltasAssemble(t *testing.T) {
	chunk := func(delta string) string {
		return "data: {\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":" + delta + ",\"finish_reason\":null}]}\n\n"
	}
	server := scriptedSSEServer(t, []string{
		chunk(`{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}`),
		chunk(`{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\""}}]}`),
		chunk(`{"tool_calls":[{"index":0,"function":{"arguments":":\"Par"}}]}`),
		chunk(`{"tool_calls":[{"index":0,"function":{"arguments":"is\"}"}}]}`),
		chunk(`{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"get_time","arguments":"{}"}}]}`),
		"data: {\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":12,\"total_tokens\":21}}\n\n",
		"data: [DONE]\n\n",
	}, 0)
	p := newLLMAccountingHTTP(t, server.URL)
	res := executeLLMAccounting(t, p, server.URL+"/v1/chat/completions", map[string]any{"model": "m", "prompt": "hi", "save_tool_calls_as": "calls"})
	llm := res.ServerTelemetry.LLM
	if !res.Success || llm.ToolCallsCount != 2 || llm.FinishReason != "tool_calls" {
		t.Fatalf("Success=%v ToolCallsCount=%d finish=%q (err %q), want two calls", res.Success, llm.ToolCallsCount, llm.FinishReason, res.ErrorMessage)
	}
	var calls []struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal([]byte(res.Extractions["calls"]), &calls); err != nil {
		t.Fatalf("saved tool calls %q: %v", res.Extractions["calls"], err)
	}
	if len(calls) != 2 || calls[0].ID != "call_1" || calls[0].Function.Name != "get_weather" || calls[0].Function.Arguments != `{"city":"Paris"}` ||
		calls[1].ID != "call_2" || calls[1].Function.Name != "get_time" || calls[1].Function.Arguments != "{}" || calls[0].Type != "function" {
		t.Fatalf("saved tool calls = %+v", calls)
	}
}

// Calls that arrive whole in separate chunks (Ollama streams each parallel
// call as it is parsed) are all saved, in order.
func TestLLMOllamaToolCallsAcrossChunks(t *testing.T) {
	stream := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"model":"m","message":{"role":"assistant","content":"","tool_calls":[{"function":{"index":0,"name":"get_weather","arguments":{"city":"Paris"}}}]},"done":false}`+"\n")
		_, _ = io.WriteString(w, `{"model":"m","message":{"role":"assistant","content":"","tool_calls":[{"function":{"index":1,"name":"get_time","arguments":{}}}]},"done":false}`+"\n")
		_, _ = io.WriteString(w, `{"model":"m","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":3,"eval_count":9}`+"\n")
	}))
	defer stream.Close()
	p := newLLMAccountingHTTP(t, stream.URL)
	res := executeLLMAccounting(t, p, stream.URL+"/api/chat", map[string]any{"dialect": "ollama", "prompt": "hi", "save_tool_calls_as": "calls"})
	var calls []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal([]byte(res.Extractions["calls"]), &calls); err != nil {
		t.Fatalf("saved calls %q: %v", res.Extractions["calls"], err)
	}
	if llm := res.ServerTelemetry.LLM; !res.Success || llm.ToolCallsCount != 2 || len(calls) != 2 || calls[0].Function.Name != "get_weather" || calls[1].Function.Name != "get_time" {
		t.Fatalf("Success=%v ToolCallsCount=%d saved=%q", res.Success, llm.ToolCallsCount, res.Extractions["calls"])
	}
}

// Older TGI messages-API streams send each tool-call delta as one object
// rather than an array; the chunk still counts, with its finish and usage.
func TestLLMTGIObjectToolCallDeltas(t *testing.T) {
	chunk := func(toolCalls, finish, usage string) string {
		return `data: {"model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":` + toolCalls + `},"finish_reason":` + finish + `}]` + usage + "}\n\n"
	}
	server := scriptedSSEServer(t, []string{
		chunk(`{"index":0,"id":"","type":"function","function":{"name":"get_weather","arguments":"{\"city\":"}}`, "null", ""),
		chunk(`{"index":0,"id":"","type":"function","function":{"name":null,"arguments":"\"Paris\"}"}}`, `"eos_token"`, `,"usage":{"prompt_tokens":7,"completion_tokens":12,"total_tokens":19}`),
		"data: [DONE]\n\n",
	}, 0)
	p := newLLMAccountingHTTP(t, server.URL)
	res := executeLLMAccounting(t, p, server.URL+"/v1/chat/completions", map[string]any{"model": "m", "prompt": "hi", "save_tool_calls_as": "calls"})
	llm := res.ServerTelemetry.LLM
	if !res.Success || llm.ToolCallsCount != 1 || llm.FinishReason != "eos_token" || llm.CompletionTokens != 12 || !strings.Contains(res.Extractions["calls"], `{\"city\":\"Paris\"}`) {
		t.Fatalf("Success=%v ToolCallsCount=%d finish=%q completion=%d calls=%q (err %q)", res.Success, llm.ToolCallsCount, llm.FinishReason, llm.CompletionTokens, res.Extractions["calls"], res.ErrorMessage)
	}
}

// A delta whose id differs from the call it would continue starts a new
// call, for servers that send each whole call under one index or none; an
// id-less fragment still continues the newest call at its index.
func TestLLMToolCallDeltasSplitOnANewID(t *testing.T) {
	chunk := func(toolCalls string) string {
		return `data: {"model":"m","choices":[{"index":0,"delta":{"tool_calls":` + toolCalls + `},"finish_reason":null}]}` + "\n\n"
	}
	for name, frames := range map[string][]string{
		"no index": {
			chunk(`[{"id":"call_1","type":"function","function":{"name":"a","arguments":"{\"x\":1}"}}]`),
			chunk(`[{"id":"call_2","type":"function","function":{"name":"b","arguments":"{\"y\":"}}]`),
			chunk(`[{"function":{"arguments":"2}"}}]`),
		},
		"index 0 reused": {
			chunk(`[{"index":0,"id":"call_1","type":"function","function":{"name":"a","arguments":"{\"x\":1}"}}]`),
			chunk(`[{"index":0,"id":"call_2","type":"function","function":{"name":"b","arguments":"{\"y\":"}}]`),
			chunk(`[{"index":0,"function":{"arguments":"2}"}}]`),
		},
		// Argument fragments under a changed id (Poolside) or the literal id
		// "null" (Mistral) carry no name, so they continue their call.
		"fragments under a changed or null id": {
			chunk(`[{"index":0,"id":"call_1","type":"function","function":{"name":"a","arguments":""}}]`),
			chunk(`[{"index":0,"id":"call_9","function":{"name":null,"arguments":"{\"x\":"}}]`),
			chunk(`[{"index":0,"id":"null","function":{"arguments":"1}"}}]`),
			chunk(`[{"index":1,"id":"call_2","type":"function","function":{"name":"b","arguments":"{\"y\":"}}]`),
			chunk(`[{"index":1,"id":"null","function":{"arguments":"2}"}}]`),
		},
	} {
		t.Run(name, func(t *testing.T) {
			frames = append(frames, `data: {"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":9,"total_tokens":12}}`+"\n\n", "data: [DONE]\n\n")
			server := scriptedSSEServer(t, frames, 0)
			p := newLLMAccountingHTTP(t, server.URL)
			res := executeLLMAccounting(t, p, server.URL+"/v1/chat/completions", map[string]any{"model": "m", "prompt": "hi", "save_tool_calls_as": "calls"})
			var calls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			}
			if err := json.Unmarshal([]byte(res.Extractions["calls"]), &calls); err != nil {
				t.Fatalf("saved calls %q: %v", res.Extractions["calls"], err)
			}
			if llm := res.ServerTelemetry.LLM; llm.ToolCallsCount != 2 || len(calls) != 2 || calls[0].ID != "call_1" || calls[0].Function.Name != "a" || calls[0].Function.Arguments != `{"x":1}` || calls[1].ID != "call_2" || calls[1].Function.Name != "b" || calls[1].Function.Arguments != `{"y":2}` {
				t.Fatalf("ToolCallsCount=%d calls=%+v", llm.ToolCallsCount, calls)
			}
		})
	}
}

// A server that returns reasoning as text with no count of it leaves the
// reasoning share an estimate, streamed or not; one with a breakdown does not.
func TestLLMReasoningShareEstimatedWithoutABreakdown(t *testing.T) {
	jsonServer := func(body string) *httptest.Server {
		server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(server.Close)
		return server
	}
	for _, tc := range []struct {
		name          string
		path          string
		body          string
		wantEstimated bool
		wantReasoning int
	}{
		{name: "vLLM reasoning_content, no breakdown", wantEstimated: true, wantReasoning: 4,
			body: `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"answer","reasoning_content":"abc abc abc abc"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":20,"total_tokens":25}}`},
		{name: "reasoning field, no breakdown", wantEstimated: true, wantReasoning: 4,
			body: `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"answer","reasoning":"abc abc abc abc"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":20,"total_tokens":25}}`},
		{name: "server breakdown", wantReasoning: 12,
			body: `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":20,"total_tokens":25,"completion_tokens_details":{"reasoning_tokens":12}}}`},
		{name: "Responses reasoning_text, zero breakdown", path: "/v1/responses", wantEstimated: true, wantReasoning: 4,
			body: `{"model":"m","status":"completed","output":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"abc abc abc abc"}]},{"type":"message","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":5,"output_tokens":20,"total_tokens":25,"output_tokens_details":{"reasoning_tokens":0}}}`},
		{name: "Responses reasoning_text, no breakdown", path: "/v1/responses", wantEstimated: true, wantReasoning: 4,
			body: `{"model":"m","status":"completed","output":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"abc abc abc abc"}]},{"type":"message","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":5,"output_tokens":20,"total_tokens":25}}`},
		// Ollama's /v1/responses returns a thinking model's reasoning only as
		// the summary, counting none of it.
		{name: "Responses summary, zero breakdown (Ollama)", path: "/v1/responses", wantEstimated: true, wantReasoning: 4,
			body: `{"model":"m","status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"abc abc abc abc"}],"encrypted_content":"abc abc abc abc"},{"type":"message","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":5,"output_tokens":20,"total_tokens":25,"output_tokens_details":{"reasoning_tokens":0}}}`},
		{name: "Responses summary with a breakdown (OpenAI)", path: "/v1/responses", wantReasoning: 12,
			body: `{"model":"m","status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"thought"}]},{"type":"message","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":5,"output_tokens":20,"total_tokens":25,"output_tokens_details":{"reasoning_tokens":12}}}`},
		{name: "Responses reasoning output_text, no breakdown (LiteLLM bridge)", path: "/v1/responses", wantEstimated: true, wantReasoning: 4,
			body: `{"model":"m","status":"completed","output":[{"type":"reasoning","content":[{"type":"output_text","text":"abc abc abc abc","annotations":[]}]},{"type":"message","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":5,"output_tokens":20,"total_tokens":25}}`},
		{name: "Responses with a breakdown", path: "/v1/responses", wantReasoning: 12,
			body: `{"model":"m","status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"thought"}],"content":[{"type":"reasoning_text","text":"abc abc"}]},{"type":"message","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":5,"output_tokens":20,"total_tokens":25,"output_tokens_details":{"reasoning_tokens":12}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := jsonServer(tc.body)
			p := newLLMAccountingHTTP(t, server.URL)
			path := tc.path
			if path == "" {
				path = "/v1/chat/completions"
			}
			res := executeLLMAccounting(t, p, server.URL+path, map[string]any{"model": "m", "prompt": "hi", "stream": false})
			if llm := res.ServerTelemetry.LLM; llm.ReasoningTokensEstimated != tc.wantEstimated || llm.ReasoningTokens != tc.wantReasoning || llm.CompletionTokens != 20 {
				t.Fatalf("reasoning=%d estimated=%v completion=%d, want %d/%v of 20", llm.ReasoningTokens, llm.ReasoningTokensEstimated, llm.CompletionTokens, tc.wantReasoning, tc.wantEstimated)
			}
		})
	}
	stream := scriptedSSEServer(t, []string{
		"data: {\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"think\"},\"finish_reason\":null}]}\n\n",
		"data: {\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n",
		"data: {\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":3,\"total_tokens\":8}}\n\n",
		"data: [DONE]\n\n",
	}, 0)
	p := newLLMAccountingHTTP(t, stream.URL)
	res := executeLLMAccounting(t, p, stream.URL+"/v1/chat/completions", map[string]any{"model": "m", "prompt": "hi"})
	if llm := res.ServerTelemetry.LLM; !llm.ReasoningTokensEstimated || llm.ReasoningTokens != 1 {
		t.Fatalf("streamed: reasoning=%d estimated=%v, want one estimated reasoning token", llm.ReasoningTokens, llm.ReasoningTokensEstimated)
	}
}

// Whole calls are saved only while the list stays within the saved-text
// bound: past the call or byte limit nothing is saved, and every call still
// counts.
func TestLLMCompleteToolCallsSaveWithinTheBound(t *testing.T) {
	ollama := func(t *testing.T, calls []string) *protocol.Result {
		t.Helper()
		server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			w.Header().Set("Content-Type", "application/x-ndjson")
			for _, call := range calls {
				_, _ = io.WriteString(w, `{"model":"m","message":{"role":"assistant","content":"","tool_calls":[`+call+`]},"done":false}`+"\n")
			}
			_, _ = io.WriteString(w, `{"model":"m","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":3,"eval_count":9}`+"\n")
		}))
		t.Cleanup(server.Close)
		p := newLLMAccountingHTTP(t, server.URL)
		return executeLLMAccounting(t, p, server.URL+"/api/chat", map[string]any{"dialect": "ollama", "prompt": "hi", "save_tool_calls_as": "calls"})
	}
	call := func(name string, argBytes int) string {
		return `{"function":{"name":"` + name + `","arguments":{"a":"` + strings.Repeat("x", argBytes) + `"}}}`
	}
	t.Run("too many calls", func(t *testing.T) {
		calls := make([]string, maxStreamedToolCalls+1)
		for i := range calls {
			calls[i] = call("f", 1)
		}
		res := ollama(t, calls)
		if llm := res.ServerTelemetry.LLM; llm.ToolCallsCount != len(calls) || res.Extractions["calls"] != "" {
			t.Fatalf("ToolCallsCount=%d saved %d bytes, want %d counted and nothing saved", llm.ToolCallsCount, len(res.Extractions["calls"]), len(calls))
		}
	})
	t.Run("too many bytes", func(t *testing.T) {
		calls := []string{call("f", maxSavedAssistantTextBytes/2), call("g", maxSavedAssistantTextBytes/2)}
		res := ollama(t, calls)
		if llm := res.ServerTelemetry.LLM; llm.ToolCallsCount != 2 || res.Extractions["calls"] != "" {
			t.Fatalf("ToolCallsCount=%d saved %d bytes, want 2 counted and nothing saved", llm.ToolCallsCount, len(res.Extractions["calls"]))
		}
	})
	t.Run("at the bound", func(t *testing.T) {
		// Two calls whose entries plus one separator each add up to the
		// bound exactly: the brackets would carry the list one byte over.
		one, err := json.Marshal(map[string]any{"function": map[string]any{"arguments": map[string]any{"a": ""}, "name": "f"}})
		if err != nil {
			t.Fatal(err)
		}
		pad := (maxSavedAssistantTextBytes - 2*(len(one)+1)) / 2
		calls := []string{call("f", pad), call("f", pad)}
		res := ollama(t, calls)
		if got := res.Extractions["calls"]; len(got) > maxSavedAssistantTextBytes {
			t.Fatalf("saved %d bytes, over the %d-byte bound", len(got), maxSavedAssistantTextBytes)
		}
	})
}

// The literal id "null" (Mistral) is no id: a call never keeps it.
func TestLLMToolCallNullIDIsNoID(t *testing.T) {
	server := scriptedSSEServer(t, []string{
		`data: {"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"null","type":"function","function":{"name":"a","arguments":"{\"x\":"}}]},"finish_reason":null}]}` + "\n\n",
		`data: {"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"null","function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}` + "\n\n",
		"data: [DONE]\n\n",
	}, 0)
	p := newLLMAccountingHTTP(t, server.URL)
	res := executeLLMAccounting(t, p, server.URL+"/v1/chat/completions", map[string]any{"model": "m", "prompt": "hi", "save_tool_calls_as": "calls"})
	if llm := res.ServerTelemetry.LLM; llm.ToolCallsCount != 1 || res.Extractions["calls"] != `[{"function":{"arguments":"{\"x\":1}","name":"a"},"type":"function"}]` {
		t.Fatalf("ToolCallsCount=%d calls=%s, want one call with no id", llm.ToolCallsCount, res.Extractions["calls"])
	}
}
