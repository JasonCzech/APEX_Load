package engine

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestLLMConversationMultiTurnTruncationAndIterationReset(t *testing.T) {
	vu := &VUser{
		ID:        1,
		Variables: make(map[string]string),
	}

	convCfg := map[string]any{
		"history_var":         "chat_hist",
		"system_prompt":       "You are a concise assistant.",
		"max_turns":           2,
		"max_context_tokens":  500,
		"truncation_strategy": "sliding_window",
	}

	// Turn 1
	act1 := dsl.Action{
		Name: "turn_1",
		Type: "llm_request",
		Params: map[string]any{
			"prompt":       "Hello turn 1",
			"conversation": convCfg,
		},
	}
	res1 := vu.resolveAction(act1)
	msgs1, ok := res1.Params["messages"].([]map[string]any)
	if !ok || len(msgs1) != 2 {
		t.Fatalf("expected [system, user1] on turn 1, got %#v", res1.Params["messages"])
	}
	if msgs1[0]["role"] != "system" || msgs1[1]["content"] != "Hello turn 1" {
		t.Fatalf("unexpected turn 1 messages: %#v", msgs1)
	}
	if err := vu.appendLLMConversationTurn(&protocol.Result{
		Success: true,
		ProtocolEvidence: map[string]any{
			"__llm_assistant_text": "Reply 1",
		},
	}); err != nil {
		t.Fatalf("appendLLMConversationTurn 1 failed: %v", err)
	}

	// Turn 2
	act2 := dsl.Action{
		Name: "turn_2",
		Type: "llm_request",
		Params: map[string]any{
			"prompt":       "Hello turn 2",
			"conversation": convCfg,
		},
	}
	res2 := vu.resolveAction(act2)
	msgs2, ok := res2.Params["messages"].([]map[string]any)
	if !ok || len(msgs2) != 4 {
		t.Fatalf("expected [system, user1, asst1, user2] on turn 2, got %#v", res2.Params["messages"])
	}
	if err := vu.appendLLMConversationTurn(&protocol.Result{
		Success: true,
		ProtocolEvidence: map[string]any{
			"__llm_assistant_text": "Reply 2",
		},
	}); err != nil {
		t.Fatalf("appendLLMConversationTurn 2 failed: %v", err)
	}

	// Turn 3 (max_turns=2 -> keeps at most 4 non-system messages, preserving system at index 0)
	act3 := dsl.Action{
		Name: "turn_3",
		Type: "llm_request",
		Params: map[string]any{
			"prompt":       "Hello turn 3",
			"conversation": convCfg,
		},
	}
	res3 := vu.resolveAction(act3)
	msgs3, ok := res3.Params["messages"].([]map[string]any)
	if !ok || len(msgs3) != 5 {
		t.Fatalf("expected 5 messages (system + 4 recent) on turn 3, got %d: %#v", len(msgs3), msgs3)
	}
	if msgs3[0]["role"] != "system" || msgs3[len(msgs3)-1]["content"] != "Hello turn 3" {
		t.Fatalf("expected system preserved at [0] and turn 3 at end, got %#v", msgs3)
	}

	// Verify max_context_tokens truncation preserves system prompt
	tightCfg := &LLMConversationConfig{
		HistoryVar:         "chat_hist",
		SystemPrompt:       "System",
		MaxContextTokens:   20,
		TruncationStrategy: "drop_oldest",
	}
	longHistory := []LLMConversationMessage{
		{Role: "system", Content: "System"},
		{Role: "user", Content: strings.Repeat("old user text ", 10)},
		{Role: "assistant", Content: strings.Repeat("old assistant text ", 10)},
		{Role: "user", Content: "short final prompt"},
	}
	truncated := truncateLLMConversationMessages(tightCfg, longHistory)
	if len(truncated) != 2 || truncated[0].Role != "system" || truncated[1].Content != "short final prompt" {
		t.Fatalf("expected system + final prompt after token truncation, got %#v", truncated)
	}

	// Reset across iterations
	vu.resetLLMConversations()
	if raw := vu.Variables["chat_hist"]; raw != "" {
		t.Fatalf("expected chat_hist cleared after resetLLMConversations, got %q", raw)
	}
	if vu.extractedVariableBytes != 0 {
		t.Fatalf("expected extractedVariableBytes == 0 after reset, got %d", vu.extractedVariableBytes)
	}
}

func TestQuotaPacerDualBucketsAndAIMD(t *testing.T) {
	pacer := NewQuotaPacer(&dsl.QuotaPacingConfig{
		RPM:                       600,
		TPM:                       120000,
		EstimatedTokensPerRequest: 1000,
		BurstTokens:               4000,
		AdaptiveAIMD:              true,
		HeadroomRatio:             0.90,
	}, 1, 0)
	if pacer == nil {
		t.Fatal("expected non-nil QuotaPacer")
	}
	if math.Abs(pacer.EffectiveRPM()-540.0) > 1e-6 || math.Abs(pacer.EffectiveTPM()-108000.0) > 1e-6 {
		t.Fatalf("unexpected initial effective rates: rpm=%v tpm=%v", pacer.EffectiveRPM(), pacer.EffectiveTPM())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resTok, ok := pacer.Wait(ctx)
	if !ok || resTok != 1000 {
		t.Fatalf("expected initial reservation of 1000 tokens, got %d (ok=%v)", resTok, ok)
	}

	// A clean window of completions -> one additive increase (+5%).
	remToks := int64(50000)
	pacer.mu.Lock()
	pacer.lastAIMDWindow = time.Now().Add(-2 * quotaAIMDWindow)
	pacer.windowLimited = true
	pacer.mu.Unlock()
	for i := 0; i < 5; i++ {
		pacer.ObserveResult(1000, &protocol.Result{
			Success:    true,
			StatusCode: 200,
			ServerTelemetry: &protocol.ServerTelemetry{
				LLM: &protocol.LLMDiagnostics{
					TotalTokens:              800,
					RateLimitRemainingTokens: &remToks,
				},
			},
		})
	}
	if pacer.EffectiveTPM() <= 108000.0 {
		t.Fatalf("expected AIMD additive increase above 108000 TPM, got %v", pacer.EffectiveTPM())
	}

	// Simulate HTTP 429 -> multiplicative 0.7x backoff
	before429 := pacer.EffectiveTPM()
	pacer.ObserveResult(800, &protocol.Result{
		Success:    false,
		StatusCode: 429,
		ServerTelemetry: &protocol.ServerTelemetry{
			LLM: &protocol.LLMDiagnostics{
				Throttled: true,
			},
		},
	})
	after429 := pacer.EffectiveTPM()
	if math.Abs(after429-before429*0.7) > 1.0 {
		t.Fatalf("expected 0.7x AIMD backoff on 429: before=%v after=%v", before429, after429)
	}
}

type llmTestTxSink struct {
	txs []*protocol.Result
}

func (s *llmTestTxSink) RecordResult(*protocol.Result) error { return nil }
func (s *llmTestTxSink) RecordTransaction(r *protocol.Result) error {
	if r != nil {
		cp := *r
		s.txs = append(s.txs, &cp)
	}
	return nil
}

func TestPerHopLLMRollupInSourceTransactionAndSequence(t *testing.T) {
	sink := &llmTestTxSink{}
	mc := NewMetricsCollector(100)
	defer mc.Stop()
	vu := &VUser{
		ID:         1,
		Variables:  make(map[string]string),
		Metrics:    mc,
		ResultSink: sink,
	}

	// Start business transaction
	if err := vu.handleSourceTransactionAction(dsl.Action{
		Name:   "rag_turn",
		Type:   "source_transaction",
		Params: map[string]any{"function": "lr_start_transaction", "args": []any{"rag_turn"}},
	}); err != nil {
		t.Fatalf("start transaction: %v", err)
	}

	// Hop 1: Query rewriter LLM call
	_ = vu.recordResult(&protocol.Result{
		ActionName: "hop_rewrite",
		Success:    true,
		StatusCode: 200,
		Duration:   80 * time.Millisecond,
		ServerTelemetry: &protocol.ServerTelemetry{
			LLM: &protocol.LLMDiagnostics{
				Model:            "llama-3-8b",
				Stream:           true,
				TTFTMs:           25.0,
				TPOTMs:           10.0,
				E2EMs:            80.0,
				PromptTokens:     120,
				CompletionTokens: 10,
				TotalTokens:      130,
			},
		},
	}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})

	// Hop 2: Answer synthesis LLM call
	_ = vu.recordResult(&protocol.Result{
		ActionName: "hop_answer",
		Success:    true,
		StatusCode: 200,
		Duration:   320 * time.Millisecond,
		ServerTelemetry: &protocol.ServerTelemetry{
			LLM: &protocol.LLMDiagnostics{
				Model:            "llama-3-70b",
				Stream:           true,
				TTFTMs:           90.0,
				TPOTMs:           20.0,
				E2EMs:            320.0,
				PromptTokens:     1400,
				CompletionTokens: 40,
				TotalTokens:      1440,
				ToolCallsCount:   1,
			},
		},
	}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})

	// End business transaction
	if err := vu.handleSourceTransactionAction(dsl.Action{
		Name:   "rag_turn",
		Type:   "source_transaction",
		Params: map[string]any{"function": "lr_end_transaction", "args": []any{"rag_turn", "LR_AUTO"}},
	}); err != nil {
		t.Fatalf("end transaction: %v", err)
	}

	if len(sink.txs) != 1 || sink.txs[0].ServerTelemetry == nil || sink.txs[0].ServerTelemetry.LLM == nil {
		t.Fatalf("expected 1 recorded business transaction with rolled-up LLM diagnostics, got %#v", sink.txs)
	}
	rolled := sink.txs[0].ServerTelemetry.LLM
	if rolled.LLMHopCount != 2 || rolled.PromptTokens != 1520 || rolled.CompletionTokens != 50 || rolled.TotalTokens != 1570 {
		b, _ := json.Marshal(rolled)
		t.Fatalf("unexpected rolled LLM diagnostics: %s", string(b))
	}
	if math.Abs(rolled.TTFTMs-25.0) > 1e-6 || math.Abs(rolled.TPOTMs-18.0) > 1e-6 {
		t.Fatalf("expected first-hop TTFT=25ms and weighted TPOT=18ms, got ttft=%v tpot=%v", rolled.TTFTMs, rolled.TPOTMs)
	}
}

func TestLLMConversationTopLevelVarLimitsAndRedaction(t *testing.T) {
	vu := &VUser{
		ID:        1,
		Variables: make(map[string]string),
		SecretResolver: SecretResolverFunc(func(name string) (string, bool) {
			return "super-secret-value-123", name == "api_token"
		}),
	}

	// 1. Verify top-level conversation_var honors max_turns, max_history_bytes, and system.
	for turn := 1; turn <= 4; turn++ {
		act := dsl.Action{
			Name: "chat_turn",
			Type: "llm_request",
			Body: `{"messages":[{"role":"user","content":"Prompt with {{secret.api_token}}"}]}`,
			Params: map[string]any{
				"conversation_var":  "top_level_hist",
				"system":            "System instructions",
				"max_turns":         1,
				"max_history_bytes": 512,
				"prompt":            "Prompt with {{secret.api_token}}",
			},
		}
		res := vu.resolveAction(act)
		if strings.Contains(string(res.RedactedBody), "super-secret-value-123") {
			t.Fatalf("RedactedBody leaked secret on turn %d: %s", turn, string(res.RedactedBody))
		}
		if !strings.Contains(string(res.Body), "super-secret-value-123") {
			t.Fatalf("Body missing resolved secret on turn %d: %s", turn, string(res.Body))
		}
		redMsgs, _ := res.RedactedParams["messages"].([]map[string]any)
		for _, m := range redMsgs {
			if c, _ := m["content"].(string); strings.Contains(c, "super-secret-value-123") {
				t.Fatalf("RedactedParams[messages] leaked secret on turn %d: %#v", turn, redMsgs)
			}
		}

		// Append turn with 24 KiB assistant text (4 turns * 24 KiB > 64 KiB without byte bound)
		if err := vu.appendLLMConversationTurn(&protocol.Result{
			Success: true,
			ProtocolEvidence: map[string]any{
				"__llm_assistant_text": strings.Repeat("A", 24*1024),
			},
		}); err != nil {
			t.Fatalf("appendLLMConversationTurn failed on turn %d: %v", turn, err)
		}
		if len(vu.Variables["top_level_hist"]) > 512 {
			t.Fatalf("expected serialized history <= 512 bytes on turn %d, got %d", turn, len(vu.Variables["top_level_hist"]))
		}
	}

	// 2. Verify Turn 1 secret remains redacted in RedactedBody and RedactedParams[messages] on Turn 2+
	// when Turn 1 stays in history and Turn 2 itself has no secret reference.
	turn1 := dsl.Action{
		Name: "multi_secret_turn_1",
		Type: "llm_request",
		Body: `{"messages":[{"role":"system","content":"System from body"},{"role":"user","content":"Auth {{secret.api_token}}"}]}`,
		Params: map[string]any{
			"history_var":       "multi_secret_hist",
			"max_turns":         4,
			"max_history_bytes": 4096,
		},
	}
	resT1 := vu.resolveAction(turn1)
	if strings.Contains(string(resT1.RedactedBody), "super-secret-value-123") {
		t.Fatalf("turn 1 RedactedBody leaked secret: %s", string(resT1.RedactedBody))
	}
	// Simulate child sample and business transaction before parent result: must not consume pending turn.
	if err := vu.appendLLMConversationTurn(&protocol.Result{
		Success:    true,
		SampleKind: "child",
	}); err != nil {
		t.Fatalf("child appendLLMConversationTurn: %v", err)
	}
	if err := vu.appendLLMConversationTurn(&protocol.Result{
		Success:             true,
		BusinessTransaction: true,
	}); err != nil {
		t.Fatalf("business transaction appendLLMConversationTurn: %v", err)
	}
	if err := vu.appendLLMConversationTurn(&protocol.Result{
		Success: true,
		ProtocolEvidence: map[string]any{
			"__llm_assistant_text": "Turn 1 reply",
		},
	}); err != nil {
		t.Fatalf("turn 1 appendLLMConversationTurn: %v", err)
	}

	turn2 := dsl.Action{
		Name: "multi_secret_turn_2",
		Type: "llm_request",
		Body: `{"messages":[{"role":"system","content":"System from body"},{"role":"user","content":"Follow up without secret"}]}`,
		Params: map[string]any{
			"history_var":       "multi_secret_hist",
			"max_turns":         4,
			"max_history_bytes": 4096,
		},
	}
	resT2 := vu.resolveAction(turn2)
	if !strings.Contains(string(resT2.Body), "super-secret-value-123") {
		t.Fatalf("turn 2 Body should retain Turn 1 resolved secret in history, got: %s", string(resT2.Body))
	}
	if strings.Contains(string(resT2.RedactedBody), "super-secret-value-123") {
		t.Fatalf("turn 2 RedactedBody leaked Turn 1 secret from history: %s", string(resT2.RedactedBody))
	}
	redMsgsT2, _ := resT2.RedactedParams["messages"].([]map[string]any)
	for _, m := range redMsgsT2 {
		if c, _ := m["content"].(string); strings.Contains(c, "super-secret-value-123") {
			t.Fatalf("turn 2 RedactedParams[messages] leaked Turn 1 secret: %#v", redMsgsT2)
		}
	}
	msgsT2, ok := resT2.Params["messages"].([]map[string]any)
	if !ok || len(msgsT2) != 4 {
		t.Fatalf("expected 4 messages [system, user1, assistant1, user2] on turn 2, got %#v", resT2.Params["messages"])
	}
	if msgsT2[0]["role"] != "system" || msgsT2[1]["role"] != "user" || msgsT2[2]["role"] != "assistant" || msgsT2[3]["role"] != "user" {
		t.Fatalf("unexpected role ordering on turn 2: %#v", msgsT2)
	}

	// 3. Verify a failed llm_request (where recordResult is never called) followed by a non-LLM action
	// does not append the failed LLM turn onto history_var.
	failedLLM := dsl.Action{
		Name: "failed_llm",
		Type: "llm_request",
		Params: map[string]any{
			"history_var": "isolated_hist",
			"prompt":      "Should not be recorded on transport error",
		},
	}
	_ = vu.resolveAction(failedLLM)
	// Simulate transport error -> next action resolves without appendLLMConversationTurn on failedLLM
	nonLLM := dsl.Action{
		Name:   "next_http",
		Type:   "http_request",
		Method: "GET",
		Target: "http://example.com/health",
	}
	_ = vu.resolveAction(nonLLM)
	if err := vu.appendLLMConversationTurn(&protocol.Result{
		Success:    true,
		StatusCode: 200,
	}); err != nil {
		t.Fatalf("non-LLM appendLLMConversationTurn: %v", err)
	}
	if got := vu.Variables["isolated_hist"]; got != "" {
		t.Fatalf("expected isolated_hist to remain empty after failed llm_request + succeeding http_request, got %q", got)
	}
}

func TestQuotaPacerSharesTheQuotaAcrossGenerators(t *testing.T) {
	pacer := NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 600, TPM: 120000, BurstTokens: 8000}, 4, 0)
	if math.Abs(pacer.EffectiveRPM()-150) > 1e-9 || math.Abs(pacer.EffectiveTPM()-30000) > 1e-9 || math.Abs(pacer.maxBurstToks-2000) > 1e-9 {
		t.Fatalf("each of 4 generators paces a quarter: rpm=%v tpm=%v burst=%v", pacer.EffectiveRPM(), pacer.EffectiveTPM(), pacer.maxBurstToks)
	}
}

// Requests larger than the token burst must still be admitted, once the
// bucket is full, instead of waiting forever for a bucket that cannot hold
// their estimate.
func TestQuotaPacerAdmitsRequestsLargerThanTheBurst(t *testing.T) {
	pacer := NewQuotaPacer(&dsl.QuotaPacingConfig{TPM: 30000}, 1, 0)
	for i := 0; i < 20; i++ {
		pacer.ObserveResult(int(pacer.estTokens), &protocol.Result{Success: true, StatusCode: 200, ServerTelemetry: &protocol.ServerTelemetry{LLM: &protocol.LLMDiagnostics{TotalTokens: 6000}}})
	}
	if pacer.estTokens <= pacer.maxBurstToks {
		t.Fatalf("test setup: estimate %v must exceed the burst %v", pacer.estTokens, pacer.maxBurstToks)
	}
	pacer.mu.Lock()
	pacer.tokTokens = pacer.maxBurstToks
	pacer.lastRefill = time.Now()
	pacer.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, ok := pacer.Wait(ctx); !ok {
		t.Fatalf("a full bucket must admit a request larger than the burst")
	}
	if pacer.tokTokens >= 0 {
		t.Fatalf("the oversized reservation should leave the bucket in debt, got %v", pacer.tokTokens)
	}
}

func TestQuotaPacerRetryAfterCooldownIsCappedAndThrottleOnly(t *testing.T) {
	pacer := NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 60000}, 1, 50*time.Millisecond)
	pacer.ObserveResult(0, &protocol.Result{Success: true, StatusCode: 200, RetryAfter: time.Hour})
	if !pacer.cooldownUntil.IsZero() {
		t.Fatalf("Retry-After on a 200 must not start a cooldown")
	}
	before := time.Now()
	pacer.ObserveResult(0, &protocol.Result{StatusCode: 429, RetryAfter: 24 * time.Hour})
	if limit := before.Add(time.Second); pacer.cooldownUntil.After(limit) {
		t.Fatalf("cooldown %v exceeds the 50ms cap", pacer.cooldownUntil.Sub(before))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, ok := pacer.Wait(ctx); !ok {
		t.Fatalf("expected admission once the capped cooldown ends")
	}
	if got := NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 60}, 1, 0).retryAfterCap; got != defaultRetryAfterCap {
		t.Fatalf("default cap = %v, want %v", got, defaultRetryAfterCap)
	}
}

func TestQuotaPacerPacesOnlyLLMActions(t *testing.T) {
	vu := &VUser{QuotaPacer: NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 60, TPM: 60000}, 1, 0)}
	plain := &protocol.Action{Type: "http", Params: map[string]any{}}
	for i := 0; i < 10; i++ {
		if reserved, ok := vu.waitLLMQuota(context.Background(), plain); !ok || reserved != 0 {
			t.Fatalf("a non-LLM action must not be paced: reserved=%d ok=%v", reserved, ok)
		}
	}
	llm := &protocol.Action{Type: "llm_request"}
	if reserved, ok := vu.waitLLMQuota(context.Background(), llm); !ok || reserved <= 0 {
		t.Fatalf("an LLM action reserves quota: reserved=%d ok=%v", reserved, ok)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, ok := vu.waitLLMQuota(ctx, &protocol.Action{Type: "http", Params: map[string]any{"llm": true}}); ok {
		t.Fatalf("at 60 RPM the next LLM request must wait")
	}
}

func TestGroupQuotaPacerUsesTheScriptRetryAfterCap(t *testing.T) {
	script := &dsl.Script{}
	if newGroupQuotaPacer(script, DataShardConfig{}) != nil {
		t.Fatalf("no quota, no pacer")
	}
	script.Config.Pacing.Quota = &dsl.QuotaPacingConfig{RPM: 120}
	script.Config.ErrorHandling.RetryAfterMaxMs = 1500
	pacer := newGroupQuotaPacer(script, DataShardConfig{Ordinal: 1, Count: 2})
	if pacer == nil || pacer.retryAfterCap != 1500*time.Millisecond || math.Abs(pacer.EffectiveRPM()-60) > 1e-9 {
		t.Fatalf("unexpected group pacer %+v", pacer)
	}
}

// One pacer hears every in-flight response of its group, so a burst of 429s
// is one congestion signal and a burst of successes one window of probing.
func TestQuotaPacerAIMDChangesOncePerWindow(t *testing.T) {
	pacer := NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 600, AdaptiveAIMD: true}, 1, 0)
	for i := 0; i < 20; i++ {
		pacer.ObserveResult(0, &protocol.Result{StatusCode: 429})
	}
	if got := pacer.EffectiveRPM(); math.Abs(got-420) > 1e-6 {
		t.Fatalf("20 concurrent 429s cut the rate to %v RPM, want one 0.7x step to 420", got)
	}
	pacer = NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 600, AdaptiveAIMD: true}, 1, 0)
	pacer.mu.Lock()
	pacer.lastAIMDWindow = time.Now().Add(-2 * quotaAIMDWindow)
	pacer.windowLimited = true
	pacer.mu.Unlock()
	for i := 0; i < 300; i++ {
		pacer.ObserveResult(0, &protocol.Result{Success: true, StatusCode: 200})
	}
	if got := pacer.EffectiveRPM(); math.Abs(got-630) > 1e-6 {
		t.Fatalf("300 successes in one window raised the rate to %v RPM, want one +5%% step to 630", got)
	}
	// The deployment's remaining-tokens header is compared with the whole
	// deployment's TPM, not this generator's share.
	sharded := NewQuotaPacer(&dsl.QuotaPacingConfig{TPM: 100000, AdaptiveAIMD: true}, 10, 0)
	plenty := int64(50000)
	sharded.mu.Lock()
	sharded.lastAIMDWindow = time.Now().Add(-2 * quotaAIMDWindow)
	sharded.windowLimited = true
	sharded.mu.Unlock()
	sharded.ObserveResult(0, &protocol.Result{Success: true, StatusCode: 200, ServerTelemetry: &protocol.ServerTelemetry{LLM: &protocol.LLMDiagnostics{RateLimitRemainingTokens: &plenty}}})
	if got := sharded.EffectiveTPM(); math.Abs(got-10500) > 1e-6 {
		t.Fatalf("50k of 100k deployment tokens left is not low: TPM %v, want 10500", got)
	}
}

// Each generator's buckets hold only its share, with no per-generator floor:
// a fleet cannot start or resume from idle with more than its quota allows.
func TestQuotaPacerFleetBurstStaysWithinTheQuota(t *testing.T) {
	const generators = 20
	var reqs, toks float64
	for i := 0; i < generators; i++ {
		p := NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 60, TPM: 10000, EstimatedTokensPerRequest: 1000}, generators, 0)
		reqs += p.maxBurstReqs
		toks += p.maxBurstToks
	}
	if reqs > 6+1e-9 || toks > 10000.0/6+1e-6 {
		t.Fatalf("fleet burst = %v requests, %v tokens; want at most 6 requests and 1667 tokens (10 s of quota)", reqs, toks)
	}
	// A generator whose share is under one request per burst still sends:
	// it admits when its bucket is full and runs into debt.
	p := NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 60, TPM: 10000, EstimatedTokensPerRequest: 1000}, generators, 0)
	p.mu.Lock()
	p.reqTokens, p.tokTokens = p.maxBurstReqs, p.maxBurstToks
	p.lastRefill = time.Now()
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, ok := p.Wait(ctx); !ok {
		t.Fatalf("a full bucket must admit a request costlier than its burst")
	}
}

// The rate only probes upward while it is the limit, and holds through a
// Retry-After cooldown, whose successes come from requests admitted before it.
func TestQuotaPacerAIMDHoldsWhenUnlimitedOrCoolingDown(t *testing.T) {
	pacer := NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 600, AdaptiveAIMD: true}, 1, 0)
	pacer.mu.Lock()
	pacer.lastAIMDWindow = time.Now().Add(-2 * quotaAIMDWindow)
	pacer.mu.Unlock()
	pacer.ObserveResult(0, &protocol.Result{Success: true, StatusCode: 200})
	if got := pacer.EffectiveRPM(); got != 600 {
		t.Fatalf("a window in which the pacer limited nothing raised the rate to %v", got)
	}
	pacer.ObserveResult(0, &protocol.Result{StatusCode: 429, RetryAfter: 30 * time.Second})
	afterThrottle := pacer.EffectiveRPM()
	for i := 0; i < 50; i++ {
		pacer.mu.Lock()
		pacer.lastAIMDWindow = time.Now().Add(-2 * quotaAIMDWindow)
		pacer.windowLimited = true
		pacer.mu.Unlock()
		pacer.ObserveResult(0, &protocol.Result{Success: true, StatusCode: 200})
	}
	if got := pacer.EffectiveRPM(); got != afterThrottle {
		t.Fatalf("successes during the cooldown moved the rate from %v to %v", afterThrottle, got)
	}
}

// A paused run admits nothing, even to requests already queued for quota, and
// the pause earns no credit.
func TestQuotaPacerPauseHoldsQueuedRequests(t *testing.T) {
	pacer := NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 600}, 1, 0)
	pacer.SetPaused(true)
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if _, ok := pacer.Wait(ctx); ok {
		t.Fatalf("a paused pacer admitted a request")
	}
	pacer.SetPaused(false)
	// 400ms at 10 requests a second would have earned 4 credits.
	if got := pacer.reqTokens; got > 1.1 {
		t.Fatalf("the pause earned %v request credits", got)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if _, ok := pacer.Wait(ctx2); !ok {
		t.Fatalf("a resumed pacer must admit")
	}
}

// A single generator's first request goes at once even on a small quota, and
// the generators of a fleet start staggered, not in lockstep.
func TestQuotaPacerFirstAdmissionAndFleetStagger(t *testing.T) {
	start := time.Now()
	single := NewQuotaPacer(&dsl.QuotaPacingConfig{RPM: 6, TPM: 1000, EstimatedTokensPerRequest: 500}, 1, 0)
	if _, ok := single.Wait(context.Background()); !ok || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("a fresh single-generator pacer took %s for its first request", time.Since(start))
	}
	const generators = 4
	var prev float64 = 2
	for ordinal := uint32(0); ordinal < generators; ordinal++ {
		p := newQuotaPacerForShard(&dsl.QuotaPacingConfig{RPM: 240}, DataShardConfig{Ordinal: ordinal, Count: generators}, 0)
		if p.reqTokens >= prev {
			t.Fatalf("generator %d starts with %v request credits, not behind generator %d's %v", ordinal, p.reqTokens, ordinal-1, prev)
		}
		prev = p.reqTokens
	}
}

// Requests queued for quota are paced client-side: that wait is not the
// system under test responding, so transactions leave it out.
func TestQuotaWaitIsExcludedFromTransactionTime(t *testing.T) {
	vu := &VUser{}
	vu.llmQuotaWait = 1500 * time.Millisecond
	if got := vu.takeLLMQuotaWait(); got != 1500*time.Millisecond || vu.llmQuotaWait != 0 || vu.iterationThinkTime != 1500*time.Millisecond {
		t.Fatalf("take = %v, left %v, iteration think time %v", got, vu.llmQuotaWait, vu.iterationThinkTime)
	}
	vu.Script = &dsl.Script{}
	start := time.Now().Add(-2 * time.Second)
	if got := vu.actionSampleDuration(start, &protocol.Result{Duration: 5 * time.Millisecond}, 1500*time.Millisecond); got < 450*time.Millisecond || got > 700*time.Millisecond {
		t.Fatalf("action transaction = %v, want about 500ms once the 1.5s quota wait is left out", got)
	}
}
