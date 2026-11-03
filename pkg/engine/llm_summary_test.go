package engine

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestLLMSummaryMergeCommutativeAndAssociative(t *testing.T) {
	makeSummary := func(ttfts []float64, tpots []float64, throttled int) *LLMSummary {
		var s *LLMSummary
		for i, ttft := range ttfts {
			tpot := tpots[i%len(tpots)]
			dist := &protocol.DiagnosticDistribution{}
			dist.Add(tpot)
			ObserveLLMAction(&s, &protocol.Result{
				ActionName: "chat",
				Success:    true,
				Duration:   time.Duration(ttft+tpot*10) * time.Millisecond,
				ServerTelemetry: &protocol.ServerTelemetry{
					LLM: &protocol.LLMDiagnostics{
						Dialect:             "openai_chat",
						Model:               "gpt-4o-mini",
						Stream:              true,
						TTFTMs:              ttft,
						FirstVisibleTokenMs: ttft,
						TPOTMs:              tpot,
						E2EMs:               ttft + tpot*10,
						ITLDistribution:     dist,
						PromptTokens:        100,
						CompletionTokens:    11,
						TotalTokens:         111,
						FinishReason:        "stop",
					},
				},
			})
		}
		for i := 0; i < throttled; i++ {
			ObserveLLMAction(&s, &protocol.Result{
				ActionName:        "chat",
				Success:           false,
				StatusCode:        429,
				ThrottledAttempts: 1,
				RetryWaitMs:       250,
				ServerTelemetry: &protocol.ServerTelemetry{
					LLM: &protocol.LLMDiagnostics{
						Dialect:   "openai_chat",
						Model:     "gpt-4o-mini",
						Throttled: true,
					},
				},
			})
		}
		FinalizeLLMSummary(s)
		return s
	}

	a := makeSummary([]float64{20, 40, 60, 80, 100}, []float64{5, 8, 12}, 1)
	b := makeSummary([]float64{30, 50, 70, 90, 110}, []float64{6, 10, 14}, 2)
	c := makeSummary([]float64{25, 45, 95, 150, 200}, []float64{7, 9, 15}, 0)

	// Commutativity: A + B == B + A
	var ab, ba *LLMSummary
	MergeLLMSummary(&ab, a)
	MergeLLMSummary(&ab, b)
	MergeLLMSummary(&ba, b)
	MergeLLMSummary(&ba, a)

	if ab.Requests != ba.Requests || ab.PromptTokens != ba.PromptTokens || ab.ThrottledRequests != ba.ThrottledRequests {
		t.Fatalf("commutativity counter mismatch: ab=%+v ba=%+v", ab, ba)
	}
	if math.Abs(ab.P95TTFTMs-ba.P95TTFTMs) > 1e-9 || math.Abs(ab.P95TPOTMs-ba.P95TPOTMs) > 1e-9 {
		t.Fatalf("commutativity quantile mismatch: ab=(%v,%v) ba=(%v,%v)", ab.P95TTFTMs, ab.P95TPOTMs, ba.P95TPOTMs, ba.P95TPOTMs)
	}

	// Associativity: (A + B) + C == A + (B + C)
	lhs := ab.Clone()
	MergeLLMSummary(&lhs, c)

	var bc, rhs *LLMSummary
	MergeLLMSummary(&bc, b)
	MergeLLMSummary(&bc, c)
	MergeLLMSummary(&rhs, a)
	MergeLLMSummary(&rhs, bc)

	if lhs.Requests != rhs.Requests || lhs.CompletionTokens != rhs.CompletionTokens {
		t.Fatalf("associativity counter mismatch: lhs=%+v rhs=%+v", lhs, rhs)
	}
	if math.Abs(lhs.P95TTFTMs-rhs.P95TTFTMs) > 1e-9 || math.Abs(lhs.P99TPOTMs-rhs.P99TPOTMs) > 1e-9 {
		t.Fatalf("associativity quantile mismatch: lhs=(%v,%v) rhs=(%v,%v)", lhs.P95TTFTMs, lhs.P99TPOTMs, rhs.P95TTFTMs, rhs.P99TPOTMs)
	}
}

func TestLLMSummaryNoBusinessTransactionDoubleCount(t *testing.T) {
	mc := NewMetricsCollector(100)
	llmDiag := &protocol.LLMDiagnostics{
		Dialect:          "openai_chat",
		Model:            "llama-3",
		Stream:           true,
		TTFTMs:           50,
		TPOTMs:           10,
		E2EMs:            150,
		PromptTokens:     64,
		CompletionTokens: 11,
		TotalTokens:      75,
		FinishReason:     "stop",
	}
	batch := []*protocol.Result{
		{
			ActionName:          "chat_step",
			Success:             true,
			Duration:            150 * time.Millisecond,
			BusinessTransaction: false,
			ServerTelemetry:     &protocol.ServerTelemetry{LLM: llmDiag.Clone()},
		},
		{
			ActionName:          "chat_tx",
			Success:             true,
			Duration:            150 * time.Millisecond,
			BusinessTransaction: true,
			ServerTelemetry:     &protocol.ServerTelemetry{LLM: llmDiag.Clone()},
		},
	}
	snap := mc.computeSnapshot(batch, time.Second)
	am := snap.ByAction["chat_step"]
	if am == nil || am.LLM == nil {
		t.Fatalf("expected ByAction[chat_step].LLM to be populated")
	}
	if am.LLM.Requests != 1 || am.LLM.PromptTokens != 64 {
		t.Fatalf("expected Requests=1 PromptTokens=64, got %+v", am.LLM)
	}
	tm := snap.ByTransaction["chat_tx"]
	if tm != nil && tm.LLM != nil {
		t.Fatalf("expected ByTransaction[chat_tx].LLM to be nil to prevent double counting, got %+v", tm.LLM)
	}
}

type mockRetry429Protocol struct {
	calls int
}

func (m *mockRetry429Protocol) Name() string                                   { return "http" }
func (m *mockRetry429Protocol) Init(_ context.Context, _ map[string]any) error { return nil }
func (m *mockRetry429Protocol) HealthCheck(_ context.Context) error            { return nil }
func (m *mockRetry429Protocol) Teardown(_ context.Context) error               { return nil }
func (m *mockRetry429Protocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	m.calls++
	if m.calls == 1 {
		return &protocol.Result{
			ActionName:   action.Name,
			StartTime:    time.Now(),
			Duration:     5 * time.Millisecond,
			StatusCode:   429,
			Success:      false,
			ErrorMessage: "HTTP 429 Too Many Requests",
			RetryAfter:   20 * time.Millisecond,
			ServerTelemetry: &protocol.ServerTelemetry{
				LLM: &protocol.LLMDiagnostics{
					Dialect:   "azure_openai",
					Throttled: true,
				},
			},
		}, nil
	}
	return &protocol.Result{
		ActionName: action.Name,
		StartTime:  time.Now(),
		Duration:   15 * time.Millisecond,
		StatusCode: 200,
		Success:    true,
		ServerTelemetry: &protocol.ServerTelemetry{
			LLM: &protocol.LLMDiagnostics{
				Dialect:          "azure_openai",
				Stream:           true,
				TTFTMs:           8,
				TPOTMs:           2,
				E2EMs:            15,
				PromptTokens:     20,
				CompletionTokens: 5,
				FinishReason:     "stop",
			},
		},
	}, nil
}

func TestVUserExecuteRetry429HonorsRetryAfter(t *testing.T) {
	proto := &mockRetry429Protocol{}
	errCfg := dsl.ErrorHandlingConfig{
		MaxRetries:      2,
		RetryDelayMs:    5,
		RetryBackoff:    "retry_after",
		HonorRetryAfter: true,
		RetryAfterMaxMs: 500,
		RetryOn:         []string{"429"},
	}
	vu := &VUser{
		ID:    1,
		Proto: proto,
		Script: &dsl.Script{
			Protocol: "http",
			Config: dsl.ScriptConfig{
				ErrorHandling: errCfg,
			},
		},
	}

	res, err := vu.executeWithRetry(context.Background(), &protocol.Action{
		Name:   "azure_chat",
		Type:   "llm_request",
		Target: "https://example.openai.azure.com/openai/deployments/gpt4o/chat/completions",
	}, errCfg)
	if err != nil {
		t.Fatalf("executeWithRetry error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected retry to succeed on second attempt, got ErrorMessage=%q", res.ErrorMessage)
	}
	if res.Attempts != 2 {
		t.Errorf("expected Attempts=2, got %d", res.Attempts)
	}
	if res.ThrottledAttempts != 1 {
		t.Errorf("expected ThrottledAttempts=1, got %d", res.ThrottledAttempts)
	}
	if res.RetryWaitMs < 15 {
		t.Errorf("expected RetryWaitMs >= 15ms from Retry-After, got %v", res.RetryWaitMs)
	}
}

func TestLLMSummaryNumbersOnlyFinalizeAndMerge(t *testing.T) {
	var raw1, raw2 *LLMSummary
	dist := &protocol.DiagnosticDistribution{}
	dist.Add(10)
	ObserveLLMAction(&raw1, &protocol.Result{
		ActionName: "chat",
		Success:    true,
		Duration:   150 * time.Millisecond,
		ServerTelemetry: &protocol.ServerTelemetry{
			LLM: &protocol.LLMDiagnostics{
				Dialect:          "openai_chat",
				Model:            "gpt-4o-mini",
				Stream:           true,
				TTFTMs:           100,
				TPOTMs:           10,
				E2EMs:            150,
				ITLDistribution:  dist,
				PromptTokens:     100,
				CompletionTokens: 10,
				TotalTokens:      110,
				FinishReason:     "stop",
				InputLengthBand:  "<256",
			},
		},
	})
	FinalizeLLMSummary(raw1)

	ObserveLLMAction(&raw2, &protocol.Result{
		ActionName: "chat",
		Success:    true,
		Duration:   300 * time.Millisecond,
		ServerTelemetry: &protocol.ServerTelemetry{
			LLM: &protocol.LLMDiagnostics{
				Dialect:          "openai_chat",
				Model:            "gpt-4o-mini",
				Stream:           true,
				TTFTMs:           200,
				TPOTMs:           20,
				E2EMs:            300,
				ITLDistribution:  dist,
				PromptTokens:     100,
				CompletionTokens: 10,
				TotalTokens:      110,
				FinishReason:     "stop",
				InputLengthBand:  "<256",
			},
		},
	})
	FinalizeLLMSummary(raw2)

	n1 := raw1.NumbersOnly()
	n2 := raw2.NumbersOnly()
	if n1.TTFTSketch != nil || n1.TPOTSketch != nil || n1.E2ESketch != nil || n1.ITLDistribution != nil {
		t.Fatalf("expected NumbersOnly to strip top-level sketches, got %+v", n1)
	}
	if band := n1.ByInputBand["<256"]; band == nil || band.TTFTSketch != nil || band.TPOTSketch != nil || band.E2ESketch != nil {
		t.Fatalf("expected NumbersOnly to strip band sketches, got %+v", band)
	}
	if n1.P95TTFTMs <= 0 || n1.P95TPOTMs <= 0 || n1.P95ITLMs <= 0 {
		t.Fatalf("expected NumbersOnly to retain scalar quantiles, got TTFT=%v TPOT=%v ITL=%v", n1.P95TTFTMs, n1.P95TPOTMs, n1.P95ITLMs)
	}

	var merged *LLMSummary
	MergeLLMSummary(&merged, n1)
	MergeLLMSummary(&merged, n2)
	if merged == nil || merged.P95TTFTMs <= 0 || merged.P95TPOTMs <= 0 || merged.P95E2EMs <= 0 {
		t.Fatalf("expected merged numbers-only summary to preserve non-zero quantiles, got %+v", merged)
	}
	if merged.ByInputBand["<256"] == nil || merged.ByInputBand["<256"].P95TTFTMs <= 0 {
		t.Fatalf("expected merged numbers-only band summary to preserve quantiles, got %+v", merged.ByInputBand["<256"])
	}
}
