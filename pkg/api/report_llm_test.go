package api

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestRunArchiveLLMReportAggregationAndSLA(t *testing.T) {
	sloTTFT := 500.0
	sloTPOT := 50.0
	manifest := &RunArchiveManifest{
		Provenance: RunArchiveProvenance{
			GoalConfig: &GoalConfig{
				LLMSLOTTFTTargetMs: &sloTTFT,
				LLMSLOTPOTTargetMs: &sloTPOT,
			},
		},
	}
	acc := newArchiveLLMAccumulator(manifest, newArchiveQuantileBudget())

	baseTime := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	// 1. Healthy streaming request in short band
	acc.merge(TransactionEvent{
		Timestamp:  baseTime,
		Population: "action",
		ActionName: "chat_stream",
		StatusCode: 200,
		Success:    true,
		DurationMs: 420,
		Attempts:   1,
		Extractions: map[string]string{
			"golden_prompt": "golden-1",
		},
		ServerTelemetry: &protocol.ServerTelemetry{
			LLM: &protocol.LLMDiagnostics{
				Dialect:          "openai_chat",
				Model:            "gpt-4o-mini",
				Region:           "eastus",
				Stream:           true,
				TTFTMs:           120,
				TPOTMs:           15,
				E2EMs:            420,
				PromptTokens:     100,
				CompletionTokens: 20,
				CachedTokens:     50,
				TotalTokens:      120,
				FinishReason:     "stop",
				InputLengthBand:  "<256",
				OutputHash:       0x1111,
				OutputChars:      80,
			},
		},
	})

	// 2. Second request with same golden_prompt but drifted output hash, in long band
	acc.merge(TransactionEvent{
		Timestamp:  baseTime.Add(1 * time.Second),
		Population: "action",
		ActionName: "chat_stream",
		StatusCode: 200,
		Success:    true,
		DurationMs: 900,
		Attempts:   1,
		Extractions: map[string]string{
			"golden_prompt": "golden-1",
		},
		ServerTelemetry: &protocol.ServerTelemetry{
			LLM: &protocol.LLMDiagnostics{
				Dialect:          "openai_chat",
				Model:            "gpt-4o-mini",
				Region:           "eastus2",
				Stream:           true,
				TTFTMs:           250,
				TPOTMs:           25,
				E2EMs:            900,
				PromptTokens:     1500,
				CompletionTokens: 40,
				ReasoningTokens:  10,
				TotalTokens:      1540,
				FinishReason:     "stop",
				InputLengthBand:  "1k-4k",
				OutputHash:       0x2222,
				OutputChars:      160,
				ToolCallsCount:   1,
			},
		},
	})

	// 3. Throttled request (429)
	acc.merge(TransactionEvent{
		Timestamp:         baseTime.Add(2 * time.Second),
		Population:        "action",
		ActionName:        "chat_stream",
		StatusCode:        429,
		Success:           false,
		DurationMs:        1100,
		Attempts:          2,
		ThrottledAttempts: 1,
		RetryWaitMs:       1000,
		ServerTelemetry: &protocol.ServerTelemetry{
			LLM: &protocol.LLMDiagnostics{
				Dialect:         "openai_chat",
				Model:           "gpt-4o-mini",
				Region:          "eastus",
				Stream:          true,
				Throttled:       true,
				FinishReason:    "rate_limited",
				InputLengthBand: "<256",
			},
		},
	})

	// 4. Truncated request (finish_reason=length)
	acc.merge(TransactionEvent{
		Timestamp:  baseTime.Add(3 * time.Second),
		Population: "action",
		ActionName: "chat_stream",
		StatusCode: 200,
		Success:    true,
		DurationMs: 1600,
		Attempts:   1,
		ServerTelemetry: &protocol.ServerTelemetry{
			LLM: &protocol.LLMDiagnostics{
				Dialect:          "openai_chat",
				Model:            "gpt-4o-mini",
				Region:           "eastus",
				Stream:           true,
				TTFTMs:           600, // breaches LLMSLOTTFTTargetMs=500
				TPOTMs:           30,
				E2EMs:            1600,
				PromptTokens:     300,
				CompletionTokens: 128,
				TotalTokens:      428,
				FinishReason:     "length",
				Truncated:        true,
				InputLengthBand:  "256-1k",
				OutputHash:       0x3333,
			},
		},
	})

	report := acc.finalize(10.0)
	if report == nil {
		t.Fatalf("expected non-nil RunArchiveLLMReport")
	}
	if report.TotalRequests != 4 {
		t.Fatalf("TotalRequests = %d, want 4", report.TotalRequests)
	}
	// Requests 1 and 2 are goodput; request 3 is throttled/failed; request 4 is truncated + TTFT breach.
	if report.GoodRequests != 2 {
		t.Fatalf("GoodRequests = %d, want 2", report.GoodRequests)
	}
	if report.GoodputPct != 50.0 {
		t.Fatalf("GoodputPct = %v, want 50.0", report.GoodputPct)
	}
	if report.ThrottledRequests != 1 {
		t.Fatalf("ThrottledRequests = %d, want 1", report.ThrottledRequests)
	}
	if report.TruncatedRequests != 1 {
		t.Fatalf("TruncatedRequests = %d, want 1", report.TruncatedRequests)
	}
	if report.ToolCallRequests != 1 {
		t.Fatalf("ToolCallRequests = %d, want 1", report.ToolCallRequests)
	}
	if report.ReasoningTokens != 10 {
		t.Fatalf("ReasoningTokens = %d, want 10", report.ReasoningTokens)
	}
	if report.GoldenPromptDrift == nil || report.GoldenPromptDrift.DriftedPrompts != 1 {
		t.Fatalf("expected 1 drifted golden prompt, got %+v", report.GoldenPromptDrift)
	}

	actionLLM := report.ByAction["chat_stream"]
	if actionLLM == nil {
		t.Fatalf("expected ByAction[chat_stream]")
	}

	// Verify HTML, PDF, and CSV rendering of reportSectionLLMPerformance
	htmlData := &runArchiveHTMLReportData{
		Report: &RunArchiveReport{
			LLM: report,
		},
	}
	renderer := reportSectionRegistry["llm_performance"]
	htmlFrag, err := renderer.HTML(htmlData)
	if err != nil {
		t.Fatalf("renderer.HTML error: %v", err)
	}
	htmlOut := string(htmlFrag)
	if !strings.Contains(htmlOut, "LLM inference performance") || !strings.Contains(htmlOut, "golden-1") {
		t.Fatalf("unexpected HTML output: %s", htmlOut)
	}
	var pdfLines []string
	renderer.PDF(htmlData, &pdfLines)
	if len(pdfLines) == 0 {
		t.Fatalf("expected non-empty PDF lines")
	}
	var csvRows int
	err = renderer.CSV(htmlData, func(section, group, metric, value string) error {
		csvRows++
		return nil
	})
	if err != nil || csvRows == 0 {
		t.Fatalf("expected CSV rows without error, got rows=%d err=%v", csvRows, err)
	}
}

// A fixed-output benchmark accepts its length finishes: the report keeps
// their timings and goodput, and counts truncation separately.
func TestRunArchiveLLMReportAcceptedLengthFinish(t *testing.T) {
	acc := newArchiveLLMAccumulator(&RunArchiveManifest{}, newArchiveQuantileBudget())
	baseTime := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		acc.merge(TransactionEvent{
			Timestamp:  baseTime.Add(time.Duration(i) * time.Second),
			Population: "action",
			ActionName: "bench",
			StatusCode: 200,
			Success:    true,
			DurationMs: 300,
			ServerTelemetry: &protocol.ServerTelemetry{LLM: &protocol.LLMDiagnostics{
				Stream: true, TTFTMs: 80, TPOTMs: 6, E2EMs: 300, PromptTokens: 64, CompletionTokens: 32,
				FinishReason: "length", Truncated: true,
			}},
		})
	}
	acc.merge(TransactionEvent{
		Timestamp:       baseTime.Add(5 * time.Second),
		Population:      "action",
		ActionName:      "bench",
		Success:         false,
		ServerTelemetry: &protocol.ServerTelemetry{LLM: &protocol.LLMDiagnostics{ErrorCode: "transport"}},
	})
	summary := acc.finalize(10)
	if summary == nil {
		t.Fatalf("expected an LLM summary")
	}
	if summary.TotalRequests != 5 || summary.SuccessfulRequests != 4 || summary.TruncatedRequests != 4 || summary.ErrorRequests != 1 {
		t.Fatalf("unexpected counters %+v", summary)
	}
	if summary.TTFT.Samples != 4 || summary.TPOT.Samples != 4 || summary.GoodputPct != 80 {
		t.Fatalf("accepted length finishes must keep their timings and goodput: TTFT=%+v TPOT=%+v goodput=%v", summary.TTFT, summary.TPOT, summary.GoodputPct)
	}
}
