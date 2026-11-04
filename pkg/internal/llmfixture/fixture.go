// Package llmfixture provides a deterministic mock LLM streaming server and
// continuous-batching simulator supporting OpenAI, Azure OpenAI, TGI, Ollama
// (NDJSON), and Triton dialects, along with fault injection and ground-truth
// telemetry for testing APEX Load's LLM inference capabilities.
package llmfixture

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// maxFixtureOutputTokens bounds the output tokens one response generates.
const maxFixtureOutputTokens = 1 << 16

// Dialect identifies the wire format used by the mock server.
type Dialect string

const (
	DialectOpenAIChat        Dialect = "openai_chat"
	DialectOpenAICompletions Dialect = "openai_completions"
	DialectOpenAIResponses   Dialect = "openai_responses"
	DialectAzureOpenAI       Dialect = "azure_openai"
	DialectTGI               Dialect = "tgi"
	DialectOllama            Dialect = "ollama"
	DialectTriton            Dialect = "triton"
)

// FaultKind specifies a deterministic or probabilistic fault to inject.
type FaultKind string

const (
	FaultNone          FaultKind = ""
	FaultRateLimit429  FaultKind = "429"
	FaultTruncate      FaultKind = "truncate"
	FaultStall         FaultKind = "stall"
	FaultReset         FaultKind = "reset"
	FaultContentFilter FaultKind = "content_filter"
	FaultHTTP5xx       FaultKind = "5xx"
	FaultEmptyOutput   FaultKind = "empty_output"
	FaultStreamError   FaultKind = "stream_error"
)

// FaultConfig configures fault injection on the fixture server.
type FaultConfig struct {
	Kind          FaultKind
	Probability   float64 // 0..1; if 1.0 (or >= 1), triggers on every request unless EveryN > 0
	EveryN        int     // if > 0, triggers on every Nth request (1-indexed)
	RetryAfterSec int     // Retry-After header in seconds for 429
	RetryAfterMs  int     // retry-after-ms / x-ms-retry-after-ms header for 429
	TruncateAfter int     // number of token chunks before truncation/stall/reset
	StallDuration time.Duration
	StatusCode    int    // custom status code for 5xx (defaults to 503)
	ErrorCode     string // error code in JSON payload (e.g., content_filter, context_length_exceeded)
	ErrorMessage  string
}

// Config configures a FixtureServer instance.
type Config struct {
	Dialect               Dialect
	Model                 string
	Seed                  int64
	BaseTTFT              time.Duration
	TTFTJitter            time.Duration
	BaseITL               time.Duration
	ITLJitter             time.Duration
	OutputTokens          int
	ReasoningTokens       int
	CachedPromptTokens    int
	RoleOnlyFirstChunk    bool // defaults to true for OpenAI/Azure chat when unset via NewServer
	DisableRoleChunk      bool
	IncludeUsageChunk     bool // defaults to true when unset via NewServer
	DisableUsageChunk     bool
	EmitServerTimestamps  bool
	AzureRegion           string
	RPMQuota              int
	TPMQuota              int
	DefaultFinishReason   string
	OutputText            string // optional fixed output text; tokenized by words/chunks
	ToolCallName          string // if non-empty, emits tool_calls delta and finish_reason="tool_calls"
	ToolCallArgumentsJSON string
	Fault                 FaultConfig
	Simulator             *SimulatorConfig
}

// RecordedRequest captures ground-truth server-side measurements for one request.
type RecordedRequest struct {
	RequestID         string
	Dialect           Dialect
	Model             string
	Stream            bool
	PromptBytes       int
	PromptTokens      int
	CompletionTokens  int
	ReasoningTokens   int
	CachedTokens      int
	QueueWait         time.Duration
	PrefillDuration   time.Duration
	TTFT              time.Duration
	FirstVisibleToken time.Duration
	TotalDecode       time.Duration
	TPOT              time.Duration
	E2EDuration       time.Duration
	ITLs              []time.Duration
	FinishReason      string
	StatusCode        int
	FaultInjected     FaultKind
	Throttled         bool
	Truncated         bool
	Preempted         bool
}

// LedgerSummary aggregates ground-truth metrics across all recorded requests.
type LedgerSummary struct {
	TotalRequests     int
	CompletedStreams  int
	ThrottledRequests int
	TruncatedRequests int
	ErrorRequests     int
	PromptTokens      int64
	CompletionTokens  int64
	MeanTTFTMs        float64
	MeanTPOTMs        float64
}

// Server is an in-memory or loopback HTTP server speaking LLM streaming dialects.
type Server struct {
	HTTPServer *httptest.Server
	URL        string

	mu            sync.Mutex
	cfg           Config
	rng           *rand.Rand
	reqSeq        atomic.Int64
	ledger        []RecordedRequest
	rpmRemaining  int
	tpmRemaining  int
	quotaWindowAt time.Time
	sim           *BatchSimulator
}

// NewServer starts a new httptest.Server configured with cfg.
func NewServer(cfg Config) *Server {
	if cfg.Dialect == "" {
		cfg.Dialect = DialectOpenAIChat
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-4o-mini-mock"
	}
	if cfg.Seed == 0 {
		cfg.Seed = 42
	}
	if cfg.BaseTTFT == 0 {
		cfg.BaseTTFT = 15 * time.Millisecond
	}
	if cfg.BaseITL == 0 {
		cfg.BaseITL = 5 * time.Millisecond
	}
	if cfg.OutputTokens <= 0 {
		cfg.OutputTokens = 8
	}
	if !cfg.DisableRoleChunk {
		cfg.RoleOnlyFirstChunk = true
	}
	if !cfg.DisableUsageChunk {
		cfg.IncludeUsageChunk = true
	}
	if cfg.DefaultFinishReason == "" {
		cfg.DefaultFinishReason = "stop"
	}
	if cfg.AzureRegion == "" {
		cfg.AzureRegion = "eastus2"
	}

	s := &Server{
		cfg:           cfg,
		rng:           rand.New(rand.NewSource(cfg.Seed)),
		rpmRemaining:  cfg.RPMQuota,
		tpmRemaining:  cfg.TPMQuota,
		quotaWindowAt: time.Now(),
	}
	if cfg.Simulator != nil {
		s.sim = NewBatchSimulator(*cfg.Simulator)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", s.handleLLM)
	mux.HandleFunc("/v1/completions", s.handleLLM)
	mux.HandleFunc("/v1/responses", s.handleResponses)
	mux.HandleFunc("/openai/v1/responses", s.handleResponses)
	mux.HandleFunc("/openai/responses", s.handleResponses)
	mux.HandleFunc("/openai/deployments/", s.handleAzure)
	mux.HandleFunc("/generate_stream", s.handleTGI)
	mux.HandleFunc("/generate", s.handleTGI)
	mux.HandleFunc("/api/chat", s.handleOllama)
	mux.HandleFunc("/api/generate", s.handleOllama)
	mux.HandleFunc("/v2/models/", s.handleTriton)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	s.HTTPServer = httptest.NewServer(mux)
	s.URL = s.HTTPServer.URL
	return s
}

// Handler returns the http.Handler for standalone use (such as cmd/apexllm-target).
func (s *Server) Handler() http.Handler {
	return s.HTTPServer.Config.Handler
}

// Close shuts down the underlying httptest.Server.
func (s *Server) Close() {
	if s.HTTPServer != nil {
		s.HTTPServer.Close()
	}
}

// SetFault updates the active fault configuration dynamically.
func (s *Server) SetFault(f FaultConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Fault = f
}

// Requests returns a snapshot of all recorded requests.
func (s *Server) Requests() []RecordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RecordedRequest, len(s.ledger))
	for i, r := range s.ledger {
		cp := r
		if len(r.ITLs) > 0 {
			cp.ITLs = append([]time.Duration(nil), r.ITLs...)
		}
		out[i] = cp
	}
	return out
}

// ResetLedger clears all recorded requests.
func (s *Server) ResetLedger() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ledger = nil
}

// Summary computes aggregate ground-truth statistics from the ledger.
func (s *Server) Summary() LedgerSummary {
	reqs := s.Requests()
	var sum LedgerSummary
	sum.TotalRequests = len(reqs)
	var ttftSum, tpotSum float64
	var ttftN, tpotN int
	for _, r := range reqs {
		if r.Throttled {
			sum.ThrottledRequests++
			continue
		}
		if r.Truncated {
			sum.TruncatedRequests++
		}
		if r.StatusCode >= 400 || r.FaultInjected != FaultNone {
			sum.ErrorRequests++
		}
		sum.PromptTokens += int64(r.PromptTokens)
		sum.CompletionTokens += int64(r.CompletionTokens)
		if r.Stream && r.StatusCode == http.StatusOK && !r.Truncated && r.FaultInjected == FaultNone && r.CompletionTokens > 0 {
			sum.CompletedStreams++
			ttftSum += float64(r.TTFT.Microseconds()) / 1000.0
			ttftN++
			if r.CompletionTokens > 1 {
				tpotSum += float64(r.TPOT.Microseconds()) / 1000.0
				tpotN++
			}
		}
	}
	if ttftN > 0 {
		sum.MeanTTFTMs = ttftSum / float64(ttftN)
	}
	if tpotN > 0 {
		sum.MeanTPOTMs = tpotSum / float64(tpotN)
	}
	return sum
}

func (s *Server) handleAzure(w http.ResponseWriter, r *http.Request) {
	s.serveDialect(w, r, DialectAzureOpenAI)
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	s.serveDialect(w, r, DialectOpenAIResponses)
}

func (s *Server) handleTGI(w http.ResponseWriter, r *http.Request) {
	s.serveDialect(w, r, DialectTGI)
}

func (s *Server) handleOllama(w http.ResponseWriter, r *http.Request) {
	s.serveDialect(w, r, DialectOllama)
}

func (s *Server) handleTriton(w http.ResponseWriter, r *http.Request) {
	s.serveDialect(w, r, DialectTriton)
}

func (s *Server) handleLLM(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	d := s.cfg.Dialect
	s.mu.Unlock()
	if strings.HasSuffix(r.URL.Path, "/v1/completions") && d == DialectOpenAIChat {
		d = DialectOpenAICompletions
	}
	s.serveDialect(w, r, d)
}

type parsedLLMRequest struct {
	Model       string
	Stream      bool
	MaxTokens   int
	MinTokens   int
	IgnoreEOS   bool
	PromptBytes int
	PromptChars int
	Temperature *float64
	Seed        *int64
	// TGI returns finish details only when the request asks for them.
	Details bool
}

func parseIncomingRequest(body []byte, defaultDialect Dialect, defaultModel string) parsedLLMRequest {
	res := parsedLLMRequest{
		Model:       defaultModel,
		Stream:      defaultDialect == DialectTGI || defaultDialect == DialectOllama || defaultDialect == DialectTriton,
		PromptBytes: len(body),
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		res.PromptChars = len(body)
		return res
	}
	if m, ok := raw["model"].(string); ok && strings.TrimSpace(m) != "" {
		res.Model = strings.TrimSpace(m)
	}
	if st, ok := raw["stream"].(bool); ok {
		res.Stream = st
	}
	for _, k := range []string{"max_tokens", "max_completion_tokens", "max_new_tokens", "max_output_tokens"} {
		if v, ok := raw[k].(float64); ok && v > 0 {
			res.MaxTokens = int(v)
			break
		}
	}
	if params, ok := raw["parameters"].(map[string]any); ok {
		if v, ok := params["max_new_tokens"].(float64); ok && v > 0 {
			res.MaxTokens = int(v)
		}
		if v, ok := params["min_new_tokens"].(float64); ok && v > 0 {
			res.MinTokens = int(v)
		}
		if ig, ok := params["ignore_eos"].(bool); ok {
			res.IgnoreEOS = ig
		}
		if d, ok := params["details"].(bool); ok {
			res.Details = d
		}
	}
	if v, ok := raw["min_tokens"].(float64); ok && v > 0 {
		res.MinTokens = int(v)
	}
	if ig, ok := raw["ignore_eos"].(bool); ok {
		res.IgnoreEOS = ig
	}
	if t, ok := raw["temperature"].(float64); ok {
		res.Temperature = &t
	}
	if seedVal, ok := raw["seed"].(float64); ok {
		sv := int64(seedVal)
		res.Seed = &sv
	}

	// Estimate prompt characters from messages, prompt, inputs, or text_input.
	var sb strings.Builder
	if msgs, ok := raw["messages"].([]any); ok {
		for _, m := range msgs {
			if mm, ok := m.(map[string]any); ok {
				if c, ok := mm["content"].(string); ok {
					sb.WriteString(c)
					sb.WriteByte(' ')
				}
			}
		}
	}
	// The Responses API's input is a string or chat-style items.
	if items, ok := raw["input"].([]any); ok {
		for _, m := range items {
			if mm, ok := m.(map[string]any); ok {
				if c, ok := mm["content"].(string); ok {
					sb.WriteString(c)
					sb.WriteByte(' ')
				}
			}
		}
	}
	for _, field := range []string{"prompt", "inputs", "text_input", "input", "instructions"} {
		if s, ok := raw[field].(string); ok {
			sb.WriteString(s)
		}
	}
	res.PromptChars = sb.Len()
	if res.PromptChars == 0 {
		res.PromptChars = len(body)
	}
	return res
}

func (s *Server) shouldInjectFault(seq int64, f FaultConfig) bool {
	if f.Kind == FaultNone {
		return false
	}
	if f.EveryN > 0 {
		return seq%int64(f.EveryN) == 0
	}
	if f.Probability <= 0 || f.Probability >= 1.0 {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rng.Float64() < f.Probability
}

func (s *Server) sampleTiming(cfg Config, promptTokens, outputTokens int) (ttft time.Duration, itls []time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ttft = cfg.BaseTTFT
	if cfg.TTFTJitter > 0 {
		ttft += time.Duration(s.rng.Int63n(int64(cfg.TTFTJitter)))
	}
	if outputTokens > 1 {
		itls = make([]time.Duration, outputTokens-1)
		for i := range itls {
			d := cfg.BaseITL
			if cfg.ITLJitter > 0 {
				d += time.Duration(s.rng.Int63n(int64(cfg.ITLJitter)))
			}
			itls[i] = d
		}
	}
	return ttft, itls
}

func (s *Server) checkAndDeductQuota(estTokens int) (throttled bool, remReq int, remTok int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if now.Sub(s.quotaWindowAt) >= time.Minute {
		s.quotaWindowAt = now
		s.rpmRemaining = s.cfg.RPMQuota
		s.tpmRemaining = s.cfg.TPMQuota
	}
	if s.cfg.RPMQuota > 0 {
		if s.rpmRemaining <= 0 {
			return true, 0, max(0, s.tpmRemaining)
		}
		s.rpmRemaining--
	}
	if s.cfg.TPMQuota > 0 {
		if s.tpmRemaining < estTokens {
			return true, max(0, s.rpmRemaining), 0
		}
		s.tpmRemaining -= estTokens
	}
	return false, s.rpmRemaining, s.tpmRemaining
}

func (s *Server) serveDialect(w http.ResponseWriter, r *http.Request, dialect Dialect) {
	reqStart := time.Now()
	seq := s.reqSeq.Add(1)
	reqID := fmt.Sprintf("req-llm-%d", seq)

	body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	_ = r.Body.Close()

	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()

	parsed := parseIncomingRequest(body, dialect, cfg.Model)
	promptTokens := max(1, (parsed.PromptChars+3)/4)
	// A request's token counts size the response this server builds; bound
	// them so one request cannot make it allocate without limit.
	parsed.MaxTokens = min(parsed.MaxTokens, maxFixtureOutputTokens)
	parsed.MinTokens = min(parsed.MinTokens, maxFixtureOutputTokens)
	outputTokens := min(cfg.OutputTokens, maxFixtureOutputTokens)
	finishReason := cfg.DefaultFinishReason
	if parsed.MaxTokens > 0 && parsed.MaxTokens < outputTokens {
		outputTokens = parsed.MaxTokens
		finishReason = "length"
	}
	if parsed.MinTokens > outputTokens && parsed.IgnoreEOS {
		outputTokens = parsed.MinTokens
	}
	if cfg.ToolCallName != "" {
		finishReason = "tool_calls"
	}

	w.Header().Set("x-request-id", reqID)
	if dialect == DialectAzureOpenAI {
		w.Header().Set("x-ms-region", cfg.AzureRegion)
	}

	// Check RPM/TPM quota bucket first.
	quotaThrottled, remReq, remTok := s.checkAndDeductQuota(promptTokens + outputTokens)
	if cfg.RPMQuota > 0 {
		w.Header().Set("x-ratelimit-remaining-requests", strconv.Itoa(remReq))
	}
	if cfg.TPMQuota > 0 {
		w.Header().Set("x-ratelimit-remaining-tokens", strconv.Itoa(remTok))
	}

	fault := cfg.Fault
	injectFault := s.shouldInjectFault(seq, fault)
	if quotaThrottled && !injectFault {
		injectFault = true
		fault = FaultConfig{
			Kind:          FaultRateLimit429,
			RetryAfterMs:  50,
			RetryAfterSec: 1,
		}
	}

	if injectFault && fault.Kind == FaultRateLimit429 {
		if fault.RetryAfterSec > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(fault.RetryAfterSec))
		}
		if fault.RetryAfterMs > 0 {
			w.Header().Set("retry-after-ms", strconv.Itoa(fault.RetryAfterMs))
			w.Header().Set("x-ms-retry-after-ms", strconv.Itoa(fault.RetryAfterMs))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Rate limit reached for requests",
				"type":    "rate_limit_error",
				"code":    "rate_limit_exceeded",
			},
		})
		s.recordRequest(RecordedRequest{
			RequestID:     reqID,
			Dialect:       dialect,
			Model:         parsed.Model,
			Stream:        parsed.Stream,
			PromptBytes:   parsed.PromptBytes,
			PromptTokens:  promptTokens,
			StatusCode:    http.StatusTooManyRequests,
			FaultInjected: FaultRateLimit429,
			Throttled:     true,
			E2EDuration:   time.Since(reqStart),
		})
		return
	}

	if injectFault && fault.Kind == FaultHTTP5xx {
		code := fault.StatusCode
		if code < 500 || code > 599 {
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		errCode := fault.ErrorCode
		if errCode == "" {
			errCode = "server_error"
		}
		msg := fault.ErrorMessage
		if msg == "" {
			msg = "The server is overloaded or not ready yet."
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": msg,
				"type":    "server_error",
				"code":    errCode,
			},
		})
		s.recordRequest(RecordedRequest{
			RequestID:     reqID,
			Dialect:       dialect,
			Model:         parsed.Model,
			Stream:        parsed.Stream,
			PromptBytes:   parsed.PromptBytes,
			PromptTokens:  promptTokens,
			StatusCode:    code,
			FaultInjected: FaultHTTP5xx,
			E2EDuration:   time.Since(reqStart),
		})
		return
	}

	// Simulator admission & timing if enabled.
	var queueWait, prefillDur time.Duration
	var preempted bool
	var ttft time.Duration
	var itls []time.Duration
	var releaseSim func()
	if s.sim != nil {
		var simErr error
		queueWait, prefillDur, itls, preempted, releaseSim, simErr = s.sim.AdmitAndSchedule(r.Context(), promptTokens, outputTokens)
		if simErr != nil {
			return
		}
		defer releaseSim()
		ttft = queueWait + prefillDur
	} else {
		ttft, itls = s.sampleTiming(cfg, promptTokens, outputTokens)
		prefillDur = ttft
	}

	if injectFault && fault.Kind == FaultEmptyOutput {
		outputTokens = 0
		itls = nil
	}
	if injectFault && fault.Kind == FaultContentFilter {
		finishReason = "content_filter"
	}

	var totalDecode time.Duration
	for _, d := range itls {
		totalDecode += d
	}
	if cfg.EmitServerTimestamps {
		w.Header().Set("x-apexllm-queue-ms", fmt.Sprintf("%.3f", float64(queueWait.Microseconds())/1000.0))
		w.Header().Set("x-apexllm-prefill-ms", fmt.Sprintf("%.3f", float64(prefillDur.Microseconds())/1000.0))
		w.Header().Set("x-apexllm-decode-ms", fmt.Sprintf("%.3f", float64(totalDecode.Microseconds())/1000.0))
	}

	// Sleep for prefill (TTFT; queueWait was already slept inside AdmitAndSchedule if simulator was used).
	sleepDur := prefillDur
	if sleepDur > 0 {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(sleepDur):
		}
	}

	tokens := generateTokenStrings(cfg.OutputText, outputTokens, parsed.Temperature, parsed.Seed)

	if !parsed.Stream {
		for _, d := range itls {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(d):
			}
		}
		s.writeNonStreamResponse(w, reqID, dialect, parsed.Model, tokens, promptTokens, outputTokens, cfg.ReasoningTokens, cfg.CachedPromptTokens, finishReason, cfg, injectFault, fault)
		e2e := time.Since(reqStart)
		var tpot time.Duration
		if outputTokens > 1 {
			tpot = totalDecode / time.Duration(outputTokens-1)
		}
		s.recordRequest(RecordedRequest{
			RequestID:        reqID,
			Dialect:          dialect,
			Model:            parsed.Model,
			Stream:           false,
			PromptBytes:      parsed.PromptBytes,
			PromptTokens:     promptTokens,
			CompletionTokens: outputTokens,
			ReasoningTokens:  cfg.ReasoningTokens,
			CachedTokens:     cfg.CachedPromptTokens,
			QueueWait:        queueWait,
			PrefillDuration:  prefillDur,
			TTFT:             ttft,
			TotalDecode:      totalDecode,
			TPOT:             tpot,
			E2EDuration:      e2e,
			ITLs:             itls,
			FinishReason:     finishReason,
			StatusCode:       http.StatusOK,
			FaultInjected:    fault.Kind,
			Preempted:        preempted,
		})
		return
	}

	// Streaming response (SSE or NDJSON for Ollama).
	flusher, _ := w.(http.Flusher)
	if dialect == DialectOllama {
		w.Header().Set("Content-Type", "application/x-ndjson")
	} else {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.WriteHeader(http.StatusOK)
	if flusher != nil {
		flusher.Flush()
	}

	// Role-only first chunk for OpenAI / Azure chat before any output token if configured.
	if cfg.RoleOnlyFirstChunk && (dialect == DialectOpenAIChat || dialect == DialectAzureOpenAI) {
		roleChunk := fmt.Sprintf(`{"id":%q,"object":"chat.completion.chunk","model":%q,"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`, reqID, parsed.Model)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", roleChunk)
		if flusher != nil {
			flusher.Flush()
		}
	}

	if dialect == DialectOpenAIResponses {
		created := fmt.Sprintf(`{"type":"response.created","sequence_number":0,"response":{"id":%q,"object":"response","status":"in_progress","model":%q,"output":[]}}`, reqID, parsed.Model)
		_, _ = fmt.Fprintf(w, "event: response.created\ndata: %s\n\n", created)
		if flusher != nil {
			flusher.Flush()
		}
	}

	var actualTTFT, firstVisible time.Duration
	var recordedITLs []time.Duration
	lastTokenAt := time.Time{}
	chunksSent := 0
	truncated := false

	// Optional reasoning tokens before visible tokens.
	reasoningCount := cfg.ReasoningTokens
	for rIdx := 0; rIdx < reasoningCount; rIdx++ {
		if chunksSent > 0 {
			delay := cfg.BaseITL
			select {
			case <-r.Context().Done():
				return
			case <-time.After(delay):
			}
		}
		now := time.Now()
		if actualTTFT == 0 {
			actualTTFT = now.Sub(reqStart)
		} else if !lastTokenAt.IsZero() {
			recordedITLs = append(recordedITLs, now.Sub(lastTokenAt))
		}
		lastTokenAt = now
		chunksSent++
		if dialect == DialectOllama {
			chunk := fmt.Sprintf(`{"model":%q,"message":{"role":"assistant","content":"","thinking":"think "},"done":false}`, parsed.Model)
			_, _ = fmt.Fprintf(w, "%s\n", chunk)
		} else if dialect == DialectOpenAIResponses {
			_, _ = io.WriteString(w, "event: response.reasoning_summary_text.delta\n"+
				`data: {"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"summary_index":0,"delta":"think "}`+"\n\n")
		} else {
			chunk := fmt.Sprintf(`{"id":%q,"object":"chat.completion.chunk","model":%q,"choices":[{"index":0,"delta":{"reasoning_content":"think "},"finish_reason":null}]}`, reqID, parsed.Model)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		if flusher != nil {
			flusher.Flush()
		}
	}

	truncateAt := fault.TruncateAfter
	if truncateAt <= 0 {
		truncateAt = max(1, outputTokens/2)
	}

	for i := 0; i < outputTokens; i++ {
		if injectFault && i >= truncateAt {
			switch fault.Kind {
			case FaultTruncate:
				truncated = true
				goto recordAndExit
			case FaultStall:
				stall := fault.StallDuration
				if stall <= 0 {
					stall = 5 * time.Second
				}
				select {
				case <-r.Context().Done():
				case <-time.After(stall):
				}
				truncated = true
				goto recordAndExit
			case FaultReset:
				if hj, ok := w.(http.Hijacker); ok {
					conn, _, err := hj.Hijack()
					if err == nil {
						if tcpConn, ok := conn.(*net.TCPConn); ok {
							_ = tcpConn.SetLinger(0)
						}
						_ = conn.Close()
					}
				}
				truncated = true
				goto recordAndExit
			case FaultStreamError:
				errCode := fault.ErrorCode
				if errCode == "" {
					errCode = "content_filter"
				}
				errMsg := fault.ErrorMessage
				if errMsg == "" {
					errMsg = "The response was filtered due to the prompt triggering safety policy."
				}
				errPayload := fmt.Sprintf(`{"error":{"message":%q,"code":%q}}`, errMsg, errCode)
				if dialect == DialectOpenAIResponses {
					errPayload = fmt.Sprintf(`{"type":"error","code":%q,"message":%q,"param":null}`, errCode, errMsg)
					_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", errPayload)
				} else if dialect == DialectOllama {
					_, _ = fmt.Fprintf(w, "%s\n", errPayload)
				} else {
					_, _ = fmt.Fprintf(w, "data: %s\n\n", errPayload)
				}
				if flusher != nil {
					flusher.Flush()
				}
				truncated = true
				goto recordAndExit
			}
		}

		if chunksSent > 0 {
			delay := cfg.BaseITL
			if i-1 >= 0 && i-1 < len(itls) {
				delay = itls[i-1]
			}
			if delay > 0 {
				select {
				case <-r.Context().Done():
					truncated = true
					goto recordAndExit
				case <-time.After(delay):
				}
			}
		}

		now := time.Now()
		if actualTTFT == 0 {
			actualTTFT = now.Sub(reqStart)
		} else if !lastTokenAt.IsZero() {
			recordedITLs = append(recordedITLs, now.Sub(lastTokenAt))
		}
		if firstVisible == 0 {
			firstVisible = now.Sub(reqStart)
		}
		lastTokenAt = now
		chunksSent++

		tok := tokens[i]
		isLast := i == outputTokens-1
		s.writeStreamTokenChunk(w, dialect, reqID, parsed.Model, tok, isLast, finishReason, promptTokens, outputTokens, parsed.Details, cfg)
		if flusher != nil {
			flusher.Flush()
		}
	}

	// If outputTokens == 0 (e.g. FaultEmptyOutput), still emit finish chunk and DONE.
	if outputTokens == 0 {
		s.writeStreamTokenChunk(w, dialect, reqID, parsed.Model, "", true, finishReason, promptTokens, 0, parsed.Details, cfg)
		if flusher != nil {
			flusher.Flush()
		}
	}

	// The Responses API ends with one terminal event carrying the whole
	// response and its usage, and no [DONE].
	if dialect == DialectOpenAIResponses {
		writeResponsesTerminalEvent(w, reqID, parsed.Model, strings.Join(tokens[:outputTokens], ""), promptTokens, outputTokens+reasoningCount, reasoningCount, cfg.CachedPromptTokens, finishReason, cfg)
		if flusher != nil {
			flusher.Flush()
		}
	}

	// Usage chunk and [DONE] for OpenAI / Azure dialects.
	if dialect == DialectOpenAIChat || dialect == DialectOpenAICompletions || dialect == DialectAzureOpenAI {
		if cfg.IncludeUsageChunk {
			totalTok := promptTokens + outputTokens + reasoningCount
			usageChunk := fmt.Sprintf(
				`{"id":%q,"object":"chat.completion.chunk","model":%q,"choices":[],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d,"prompt_tokens_details":{"cached_tokens":%d},"completion_tokens_details":{"reasoning_tokens":%d}}}`,
				reqID, parsed.Model, promptTokens, outputTokens+reasoningCount, totalTok, cfg.CachedPromptTokens, reasoningCount,
			)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", usageChunk)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}

recordAndExit:
	e2e := time.Since(reqStart)
	var decodeDur, tpot time.Duration
	for _, d := range recordedITLs {
		decodeDur += d
	}
	totalGenerated := chunksSent
	if totalGenerated > 1 {
		tpot = decodeDur / time.Duration(totalGenerated-1)
	}
	if s.sim != nil {
		s.sim.ObserveCompleted(queueWait, actualTTFT, tpot, e2e, promptTokens, totalGenerated)
	}
	s.recordRequest(RecordedRequest{
		RequestID:         reqID,
		Dialect:           dialect,
		Model:             parsed.Model,
		Stream:            true,
		PromptBytes:       parsed.PromptBytes,
		PromptTokens:      promptTokens,
		CompletionTokens:  totalGenerated,
		ReasoningTokens:   reasoningCount,
		CachedTokens:      cfg.CachedPromptTokens,
		QueueWait:         queueWait,
		PrefillDuration:   prefillDur,
		TTFT:              actualTTFT,
		FirstVisibleToken: firstVisible,
		TotalDecode:       decodeDur,
		TPOT:              tpot,
		E2EDuration:       e2e,
		ITLs:              recordedITLs,
		FinishReason:      finishReason,
		StatusCode:        http.StatusOK,
		FaultInjected:     pickFault(injectFault, fault.Kind),
		Truncated:         truncated,
		Preempted:         preempted,
	})
}

func pickFault(injected bool, k FaultKind) FaultKind {
	if !injected {
		return FaultNone
	}
	return k
}

func (s *Server) writeStreamTokenChunk(
	w io.Writer,
	dialect Dialect,
	reqID string,
	model string,
	tok string,
	isLast bool,
	finishReason string,
	promptTokens int,
	outputTokens int,
	details bool,
	cfg Config,
) {
	switch dialect {
	case DialectOpenAICompletions:
		fr := "null"
		if isLast {
			fr = fmt.Sprintf("%q", finishReason)
		}
		payload := fmt.Sprintf(`{"id":%q,"object":"text_completion","model":%q,"choices":[{"index":0,"text":%q,"finish_reason":%s}]}`, reqID, model, tok, fr)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)

	case DialectTGI:
		if isLast && details {
			payload := fmt.Sprintf(`{"token":{"id":1,"text":%q,"logprob":0.0,"special":false},"generated_text":%q,"details":{"finish_reason":%q,"generated_tokens":%d,"seed":null,"input_length":%d}}`,
				tok, tok, finishReason, outputTokens, promptTokens)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		} else if isLast {
			// Without details TGI's final event carries only generated_text.
			payload := fmt.Sprintf(`{"token":{"id":1,"text":%q,"logprob":0.0,"special":false},"generated_text":%q,"details":null}`, tok, tok)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		} else {
			payload := fmt.Sprintf(`{"token":{"id":1,"text":%q,"logprob":0.0,"special":false},"generated_text":null,"details":null}`, tok)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		}

	case DialectOllama:
		if isLast {
			// Real Ollama counts thinking tokens inside eval_count.
			payload := fmt.Sprintf(`{"model":%q,"message":{"role":"assistant","content":%q},"done":true,"done_reason":%q,"prompt_eval_count":%d,"eval_count":%d}`,
				model, tok, finishReason, promptTokens, outputTokens+cfg.ReasoningTokens)
			_, _ = fmt.Fprintf(w, "%s\n", payload)
		} else {
			payload := fmt.Sprintf(`{"model":%q,"message":{"role":"assistant","content":%q},"done":false}`, model, tok)
			_, _ = fmt.Fprintf(w, "%s\n", payload)
		}

	case DialectOpenAIResponses:
		payload := fmt.Sprintf(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":%q}`, tok)
		_, _ = fmt.Fprintf(w, "event: response.output_text.delta\ndata: %s\n\n", payload)

	case DialectTriton:
		// Triton's generate_stream wire shape: no finish_reason and no
		// terminal event; the stream simply ends.
		payload := fmt.Sprintf(`{"model_name":%q,"model_version":"1","text_output":%q}`, model, tok)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)

	default: // DialectOpenAIChat, DialectAzureOpenAI
		fr := "null"
		if isLast {
			fr = fmt.Sprintf("%q", finishReason)
		}
		if isLast && cfg.ToolCallName != "" {
			args := cfg.ToolCallArgumentsJSON
			if args == "" {
				args = `{"query":"status"}`
			}
			payload := fmt.Sprintf(`{"id":%q,"object":"chat.completion.chunk","model":%q,"choices":[{"index":0,"delta":{"content":%q,"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":%q,"arguments":%q}}]},"finish_reason":%s}]}`,
				reqID, model, tok, cfg.ToolCallName, args, fr)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
			return
		}
		payload := fmt.Sprintf(`{"id":%q,"object":"chat.completion.chunk","model":%q,"choices":[{"index":0,"delta":{"content":%q},"finish_reason":%s}]}`, reqID, model, tok, fr)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	}
}

func (s *Server) writeNonStreamResponse(
	w http.ResponseWriter,
	reqID string,
	dialect Dialect,
	model string,
	tokens []string,
	promptTokens int,
	outputTokens int,
	reasoningTokens int,
	cachedTokens int,
	finishReason string,
	cfg Config,
	injectFault bool,
	fault FaultConfig,
) {
	w.Header().Set("Content-Type", "application/json")
	if dialect == DialectTGI {
		// TGI's /generate reports the prompt length in a header; its details
		// carry a prefill list only with decoder_input_details.
		w.Header().Set("x-prompt-tokens", strconv.Itoa(promptTokens))
		w.Header().Set("x-generated-tokens", strconv.Itoa(outputTokens))
	}
	w.WriteHeader(http.StatusOK)
	fullText := strings.Join(tokens, "")
	if injectFault && fault.Kind == FaultStreamError {
		errCode := fault.ErrorCode
		if errCode == "" {
			errCode = "content_filter"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Filtered in HTTP 200 payload",
				"code":    errCode,
			},
		})
		return
	}
	switch dialect {
	case DialectOpenAIResponses:
		_ = json.NewEncoder(w).Encode(responsesObject(reqID, model, fullText, promptTokens, outputTokens+reasoningTokens, reasoningTokens, cachedTokens, finishReason, cfg))
	case DialectOllama:
		message := map[string]any{"role": "assistant", "content": fullText}
		if reasoningTokens > 0 {
			// "abc " estimates at exactly one token apiece (4 bytes/token).
			message["thinking"] = strings.Repeat("abc ", reasoningTokens)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":             model,
			"message":           message,
			"done":              true,
			"done_reason":       finishReason,
			"prompt_eval_count": promptTokens,
			"eval_count":        outputTokens + reasoningTokens,
		})
	case DialectTGI:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"generated_text": fullText,
			"details": map[string]any{
				"finish_reason":    finishReason,
				"generated_tokens": outputTokens,
				"prefill":          []any{},
			},
		})
	case DialectTriton:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model_name":  model,
			"text_output": fullText,
		})
	default:
		msg := map[string]any{
			"role":    "assistant",
			"content": fullText,
		}
		if cfg.ToolCallName != "" {
			args := cfg.ToolCallArgumentsJSON
			if args == "" {
				args = `{"query":"status"}`
			}
			msg["tool_calls"] = []map[string]any{
				{
					"id":   "call_1",
					"type": "function",
					"function": map[string]any{
						"name":      cfg.ToolCallName,
						"arguments": args,
					},
				},
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     reqID,
			"object": "chat.completion",
			"model":  model,
			"choices": []map[string]any{
				{
					"index":         0,
					"message":       msg,
					"finish_reason": finishReason,
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     promptTokens,
				"completion_tokens": outputTokens + reasoningTokens,
				"total_tokens":      promptTokens + outputTokens + reasoningTokens,
				"prompt_tokens_details": map[string]any{
					"cached_tokens": cachedTokens,
				},
				"completion_tokens_details": map[string]any{
					"reasoning_tokens": reasoningTokens,
				},
			},
		})
	}
}

// responsesObject builds an OpenAI Responses API response object. A length
// finish is status incomplete with reason max_output_tokens, a content filter
// is incomplete with reason content_filter, as the real API reports them.
func responsesObject(reqID, model, text string, inputTokens, outputTokens, reasoningTokens, cachedTokens int, finishReason string, cfg Config) map[string]any {
	output := []map[string]any{{
		"type":    "message",
		"id":      "msg_1",
		"status":  "completed",
		"role":    "assistant",
		"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}},
	}}
	if cfg.ToolCallName != "" {
		args := cfg.ToolCallArgumentsJSON
		if args == "" {
			args = `{"query":"status"}`
		}
		output = append(output, map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": cfg.ToolCallName, "arguments": args, "status": "completed"})
	}
	resp := map[string]any{
		"id":                 reqID,
		"object":             "response",
		"status":             "completed",
		"model":              model,
		"output":             output,
		"incomplete_details": nil,
		"error":              nil,
		"usage": map[string]any{
			"input_tokens":          inputTokens,
			"input_tokens_details":  map[string]any{"cached_tokens": cachedTokens},
			"output_tokens":         outputTokens,
			"output_tokens_details": map[string]any{"reasoning_tokens": reasoningTokens},
			"total_tokens":          inputTokens + outputTokens,
		},
	}
	switch finishReason {
	case "length":
		resp["status"] = "incomplete"
		resp["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	case "content_filter":
		resp["status"] = "incomplete"
		resp["incomplete_details"] = map[string]any{"reason": "content_filter"}
	}
	return resp
}

func writeResponsesTerminalEvent(w io.Writer, reqID, model, text string, inputTokens, outputTokens, reasoningTokens, cachedTokens int, finishReason string, cfg Config) {
	resp := responsesObject(reqID, model, text, inputTokens, outputTokens, reasoningTokens, cachedTokens, finishReason, cfg)
	eventType := "response.completed"
	if resp["status"] == "incomplete" {
		eventType = "response.incomplete"
	}
	payload, _ := json.Marshal(map[string]any{"type": eventType, "sequence_number": 1, "response": resp})
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, payload)
}

func (s *Server) recordRequest(rec RecordedRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ledger = append(s.ledger, rec)
}

func generateTokenStrings(customText string, count int, temp *float64, seed *int64) []string {
	if count <= 0 || count > maxFixtureOutputTokens {
		return nil
	}
	if customText != "" {
		words := strings.Fields(customText)
		if len(words) > 0 {
			out := make([]string, count)
			for i := 0; i < count; i++ {
				w := words[i%len(words)]
				if i < count-1 {
					w += " "
				}
				out[i] = w
			}
			return out
		}
	}
	vocab := []string{"The ", "steady ", "state ", "throughput ", "meets ", "SLO ", "under ", "continuous ", "batching ", "load."}
	offset := 0
	if seed != nil {
		offset = int(math.Abs(float64(*seed))) % len(vocab)
	}
	out := make([]string, count)
	for i := 0; i < count; i++ {
		out[i] = vocab[(i+offset)%len(vocab)]
	}
	return out
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	if s.sim != nil {
		_, _ = io.WriteString(w, s.sim.PrometheusMetrics())
		return
	}
	sum := s.Summary()
	fmt.Fprintf(w, "# TYPE vllm:prompt_tokens_total counter\nvllm:prompt_tokens_total %d\n", sum.PromptTokens)
	fmt.Fprintf(w, "# TYPE vllm:generation_tokens_total counter\nvllm:generation_tokens_total %d\n", sum.CompletionTokens)
}
