package api

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestLLMRollingWindowAndLiveMetrics(t *testing.T) {
	var win llmRollingWindow
	baseTime := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	makeInterval := func(ttftMs, tpotMs float64, inputTok, outputTok int64, throttled bool) *engine.LLMSummary {
		var s *engine.LLMSummary
		res := &protocol.Result{
			StatusCode: 200,
			Success:    true,
			Duration:   time.Duration(ttftMs+tpotMs*float64(outputTok)) * time.Millisecond,
			ServerTelemetry: &protocol.ServerTelemetry{
				LLM: &protocol.LLMDiagnostics{
					Stream:              true,
					TTFTMs:              ttftMs,
					TPOTMs:              tpotMs,
					PromptTokens:        int(inputTok),
					CompletionTokens:    int(outputTok),
					TotalTokens:         int(inputTok + outputTok),
					FinishReason:        "stop",
					Model:               "gpt-4o",
					InputLengthBand:     "short",
					Throttled:           throttled,
					FirstVisibleTokenMs: ttftMs,
				},
			},
		}
		engine.ObserveLLMAction(&s, res)
		return s
	}

	// Push 35 intervals (1 second apart); only the latest 30 slots should remain in the rolling window.
	var lastLive *LiveLLMMetrics
	for i := 0; i < 35; i++ {
		ts := baseTime.Add(time.Duration(i) * time.Second)
		ttftMs := 100.0
		if i < 5 {
			// Early high-latency spikes that should roll out of the 30s window.
			ttftMs = 5000.0
		}
		interval := makeInterval(ttftMs, 10.0, 50, 20, false)
		lastLive = win.record(ts, 1.0, interval, map[string]*engine.LLMSummary{"chat": interval}, nil)
	}

	if lastLive == nil || lastLive.Summary == nil {
		t.Fatal("expected non-nil LiveLLMMetrics and Summary")
	}
	if lastLive.Summary.Requests < 30 || lastLive.Summary.Requests > 31 {
		t.Fatalf("expected ~30 requests in rolling window, got %d", lastLive.Summary.Requests)
	}
	// Early 5000ms spikes in slots 0..3 should have aged out.
	if lastLive.Summary.P95TTFTMs > 200 {
		t.Fatalf("expected aged-out 5000ms spikes to be excluded from rolling P95 TTFT, got %v", lastLive.Summary.P95TTFTMs)
	}
	if lastLive.OutputTokensPerSec != 20.0 {
		t.Fatalf("expected OutputTokensPerSec=20, got %v", lastLive.OutputTokensPerSec)
	}
	if lastLive.InputTokensPerSec != 50.0 {
		t.Fatalf("expected InputTokensPerSec=50, got %v", lastLive.InputTokensPerSec)
	}
	if lastLive.TotalTokensPerSec != 70.0 {
		t.Fatalf("expected TotalTokensPerSec=70, got %v", lastLive.TotalTokensPerSec)
	}
	if lastLive.GoodputPct != 100.0 {
		t.Fatalf("expected GoodputPct=100, got %v", lastLive.GoodputPct)
	}
	if lastLive.Summary.ByInputBand["short"] == nil {
		t.Fatalf("expected short input band in Summary, got %+v", lastLive.Summary.ByInputBand)
	}
}

func TestGoalControllerLLMSLABreachesAndAutoStop(t *testing.T) {
	maxTTFT := 200.0
	maxTPOT := 25.0
	minTokPerSec := 100.0
	minGoodput := 95.0
	maxThrottle := 2.0

	cfg := &GoalConfig{
		StopOnSLABreach:          true,
		SLABreachWindow:          "2s",
		MaxLLMP95TTFTMs:          &maxTTFT,
		MaxLLMP95TPOTMs:          &maxTPOT,
		MinLLMOutputTokensPerSec: &minTokPerSec,
		MinLLMGoodputPct:         &minGoodput,
		MaxLLMThrottleRatePct:    &maxThrottle,
	}

	state := &goalControllerState{}
	t0 := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	badMetrics := &LiveMetrics{
		ActiveVUsers: 4,
		TotalTx:      10,
		LLM: &LiveLLMMetrics{
			OutputTokensPerSec: 45.0,
			GoodputPct:         80.0,
			ThrottleRatePct:    10.0,
			Summary: &engine.LLMSummary{
				Requests:         10,
				StreamedRequests: 10,
				P95TTFTMs:        350.0,
				P95TPOTMs:        40.0,
			},
		},
	}

	u1 := evaluateGoalController(t0, 4, badMetrics, cfg, state)
	if u1.Status == nil || !u1.Status.BreachActive {
		t.Fatalf("expected SLA breach on first tick, got %+v", u1.Status)
	}
	if !u1.Status.LLMP95TTFTBreached || !u1.Status.LLMP95TPOTBreached || !u1.Status.LLMOutputTokensPerSecBreached || !u1.Status.LLMGoodputBreached || !u1.Status.LLMThrottleRateBreached {
		t.Fatalf("expected all 5 LLM breach flags true, got %+v", u1.Status)
	}
	if u1.ShouldStop {
		t.Fatalf("did not expect auto-stop before SLABreachWindow elapsed")
	}

	u2 := evaluateGoalController(t0.Add(3*time.Second), 4, badMetrics, cfg, state)
	if !u2.ShouldStop || !u2.Status.StopTriggered {
		t.Fatalf("expected ShouldStop=true after breach window exceeded, got %+v", u2.Status)
	}
	if !strings.Contains(u2.StopReason, "LLM p95 TTFT") {
		t.Fatalf("expected StopReason to include 'LLM p95 TTFT', got %q", u2.StopReason)
	}
}

func TestLLMIntervalSummaryPersistenceRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "llm_history.db")
	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ts := time.Date(2026, 11, 3, 12, 5, 0, 0, time.UTC)
	if err := store.SaveTest(&storage.TestRecord{
		ID:     "test-llm-1",
		Name:   "LLM Persistence Test",
		Status: "RUNNING",
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	live := &LiveMetrics{
		Timestamp:    ts,
		ActiveVUsers: 8,
		TPS:          12.5,
		TotalTx:      25,
		LLM: &LiveLLMMetrics{
			RollingWindowSeconds: 30.0,
			OutputTokensPerSec:   625.0,
			InputTokensPerSec:    1250.0,
			TotalTokensPerSec:    1875.0,
			GoodputPct:           96.0,
			ThrottleRatePct:      4.0,
			Summary: &engine.LLMSummary{
				Requests:         25,
				StreamedRequests: 25,
				PromptTokens:     2500,
				CompletionTokens: 1250,
				P50TTFTMs:        110.0,
				P95TTFTMs:        195.0,
				P50TPOTMs:        11.5,
				P95TPOTMs:        18.0,
				FinishReasons:    map[string]int64{"stop": 24, "length": 1},
				Models:           map[string]int64{"gpt-4o-mini": 25},
			},
		},
	}

	if err := store.SaveHistory("test-llm-1", ts, live); err != nil {
		t.Fatalf("SaveHistory: %v", err)
	}

	history, err := store.GetHistory("test-llm-1")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(history))
	}
	rec, ok := history[0].(storage.IntervalSummaryRecord)
	if !ok {
		t.Fatalf("expected storage.IntervalSummaryRecord, got %T", history[0])
	}
	if len(rec.LLM) == 0 {
		t.Fatalf("expected persisted llm_json in IntervalSummaryRecord")
	}

	restored := intervalSummaryRecordToLiveMetrics(rec)
	if restored.LLM == nil || restored.LLM.Summary == nil {
		t.Fatalf("expected restored LiveMetrics.LLM.Summary")
	}
	if restored.LLM.OutputTokensPerSec != 625.0 || restored.LLM.Summary.P95TTFTMs != 195.0 || restored.LLM.Summary.Models["gpt-4o-mini"] != 25 {
		t.Fatalf("unexpected restored LiveMetrics.LLM: %+v", restored.LLM)
	}
}

func TestLiveLLMMetricsNumbersOnlyWireAndPeerIncomplete(t *testing.T) {
	var raw *engine.LLMSummary
	dist := &protocol.DiagnosticDistribution{}
	dist.Add(8)
	engine.ObserveLLMAction(&raw, &protocol.Result{
		ActionName: "chat",
		StatusCode: 200,
		Success:    true,
		Duration:   180 * time.Millisecond,
		ServerTelemetry: &protocol.ServerTelemetry{
			LLM: &protocol.LLMDiagnostics{
				Dialect:          "openai_chat",
				Model:            "gpt-4o-mini",
				Stream:           true,
				TTFTMs:           90,
				TPOTMs:           8,
				E2EMs:            180,
				ITLDistribution:  dist,
				PromptTokens:     64,
				CompletionTokens: 12,
				TotalTokens:      76,
				FinishReason:     "stop",
				InputLengthBand:  "<256",
			},
		},
	})
	engine.FinalizeLLMSummary(raw)

	modernBatch := generatorrpc.MetricsBatch{
		TimestampUnixMs:      1000,
		WindowMs:             1000,
		ActionMetricsVersion: 3,
		LLMSummaryVersion:    generatorrpc.CurrentLLMSummaryVersion,
		FleetMetrics:         &generatorrpc.MetricAggregate{Name: "fleet", Count: 1},
		ActionMetrics: []generatorrpc.MetricAggregate{
			{Name: "chat", Count: 1, DependencyType: "http", LLM: raw.Clone()},
		},
	}
	olderBatch := generatorrpc.MetricsBatch{
		TimestampUnixMs:      1000,
		WindowMs:             1000,
		ActionMetricsVersion: 3,
		LLMSummaryVersion:    0,
		FleetMetrics:         &generatorrpc.MetricAggregate{Name: "fleet", Count: 1},
		ActionMetrics: []generatorrpc.MetricAggregate{
			{Name: "chat", Count: 1, DependencyType: "http", LLM: nil},
		},
	}

	combined, fleetTPS, groupTPS := aggregateDistributedMetricsBatches("run-llm", map[string]generatorrpc.MetricsBatch{
		"gen-modern": modernBatch,
		"gen-older":  olderBatch,
	})
	if combined.LLMSummaryVersion != 0 {
		t.Fatalf("expected combined.LLMSummaryVersion=0 for mixed fleet, got %d", combined.LLMSummaryVersion)
	}
	test := &ManagedTest{ID: "run-llm"}
	live := buildLiveMetricsFromDistributedBatch(test, combined, fleetTPS, groupTPS)
	updateTestLLMRollingWindowLocked(test, live)
	if live.LLM == nil || live.LLM.Summary == nil {
		t.Fatalf("expected non-nil live.LLM.Summary")
	}
	if !live.LLM.Incomplete || !live.LLM.Summary.Incomplete {
		t.Fatalf("expected Incomplete=true on mixed-fleet LiveMetrics.LLM, got %+v", live.LLM)
	}
	if live.LLM.Summary.TTFTSketch != nil || live.LLM.Summary.TPOTSketch != nil || live.LLM.Summary.ITLDistribution != nil {
		t.Fatalf("expected LiveMetrics.LLM.Summary to strip raw sketches, got %+v", live.LLM.Summary)
	}
	wireJSON, err := json.Marshal(live.LLM)
	if err != nil {
		t.Fatalf("marshal live.LLM: %v", err)
	}
	for _, forbidden := range []string{`"ttft_sketch":`, `"tpot_sketch":`, `"e2e_sketch":`, `"first_visible_token_sketch":`, `"itl_ms":`} {
		if strings.Contains(string(wireJSON), forbidden) {
			t.Fatalf("expected numbers-only LiveMetrics.LLM JSON without %s, got %s", forbidden, string(wireJSON))
		}
	}
}
