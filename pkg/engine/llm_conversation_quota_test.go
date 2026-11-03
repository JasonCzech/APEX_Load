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
	})
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

	// Simulate 5 clean completions -> additive increase (+5%)
	remToks := int64(50000)
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
