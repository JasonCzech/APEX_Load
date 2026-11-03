package generatorrpc

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/llmfixture"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/http"
)

func TestLLMSummaryProtoRoundTripAndMerge(t *testing.T) {
	var s1, s2 *engine.LLMSummary
	remTokens := int64(1500)
	engine.ObserveLLMAction(&s1, &protocol.Result{
		StatusCode:        200,
		Success:           true,
		Attempts:          1,
		ThrottledAttempts: 0,
		ServerTelemetry: &protocol.ServerTelemetry{
			LLM: &protocol.LLMDiagnostics{
				Dialect:                  "openai_chat",
				Model:                    "gpt-4o-mini",
				Stream:                   true,
				TTFTMs:                   110,
				FirstVisibleTokenMs:      115,
				TPOTMs:                   14,
				E2EMs:                    390,
				PromptTokens:             128,
				CompletionTokens:         20,
				CachedTokens:             64,
				TotalTokens:              148,
				FinishReason:             "stop",
				InputLengthBand:          "<256",
				RateLimitRemainingTokens: &remTokens,
			},
		},
	})
	engine.FinalizeLLMSummary(s1)

	engine.ObserveLLMAction(&s2, &protocol.Result{
		StatusCode:        429,
		Success:           false,
		Attempts:          2,
		ThrottledAttempts: 1,
		RetryWaitMs:       500,
		ServerTelemetry: &protocol.ServerTelemetry{
			LLM: &protocol.LLMDiagnostics{
				Dialect:         "openai_chat",
				Model:           "gpt-4o-mini",
				Stream:          true,
				Throttled:       true,
				FinishReason:    "rate_limited",
				InputLengthBand: "<256",
			},
		},
	})
	engine.FinalizeLLMSummary(s2)

	pb1 := llmSummaryToProto(s1)
	if pb1 == nil {
		t.Fatalf("expected non-nil pb.LLMSummary")
	}
	rt1 := llmSummaryFromProto(pb1)
	if rt1 == nil || rt1.Requests != 1 || rt1.PromptTokens != 128 || rt1.MinRateLimitRemainingTokens == nil || *rt1.MinRateLimitRemainingTokens != 1500 {
		t.Fatalf("round-trip mismatch: %+v", rt1)
	}

	merged := mergeMetricAggregates([]MetricAggregate{
		{Name: "chat", Count: 1, LLM: rt1},
		{Name: "chat", Count: 1, Errors: 1, LLM: llmSummaryFromProto(llmSummaryToProto(s2))},
	})
	if merged == nil || merged.LLM == nil {
		t.Fatalf("expected merged LLM summary, got %+v", merged)
	}
	if merged.LLM.Requests != 2 || merged.LLM.ThrottledRequests != 1 || merged.LLM.TotalRetryWaitMs != 500 || merged.LLM.Incomplete {
		t.Fatalf("unexpected merged LLM summary: %+v", merged.LLM)
	}
}

func TestLLMSummaryVersionAndPeerCompleteness(t *testing.T) {
	svc := newIsolatedService(t, 10)
	var s *engine.LLMSummary
	engine.ObserveLLMAction(&s, &protocol.Result{
		StatusCode: 200,
		Success:    true,
		ServerTelemetry: &protocol.ServerTelemetry{
			LLM: &protocol.LLMDiagnostics{
				Dialect:          "openai_chat",
				Model:            "gpt-4o-mini",
				Stream:           true,
				TTFTMs:           50,
				TPOTMs:           5,
				E2EMs:            100,
				PromptTokens:     32,
				CompletionTokens: 10,
				TotalTokens:      42,
				FinishReason:     "stop",
			},
		},
	})
	engine.FinalizeLLMSummary(s)

	pbBatch := svc.metricsBatchForMetrics(
		"test-1", 1, GeneratorStats{ActiveVUsers: 1}, nil,
		metricsAggregateSet{
			NativeVersion: 3,
			Actions: []MetricAggregate{
				{Name: "chat", Count: 1, LLM: s},
			},
		},
		false, map[string]int32{"users": 1}, "RUNNING", "", nil,
	)
	if pbBatch.GetLlmSummaryVersion() != CurrentLLMSummaryVersion {
		t.Fatalf("expected LlmSummaryVersion=%d, got %d", CurrentLLMSummaryVersion, pbBatch.GetLlmSummaryVersion())
	}

	// Merge a modern peer with an older peer that executed the same action without an LLMSummary.
	mergedMixed := mergeMetricAggregates([]MetricAggregate{
		{Name: "chat", Count: 1, DependencyType: "http", LLM: s.Clone()},
		{Name: "chat", Count: 1, DependencyType: "http", LLM: nil},
	})
	if mergedMixed == nil || mergedMixed.LLM == nil || !mergedMixed.LLM.Incomplete {
		t.Fatalf("expected mixed peer merge to set LLM.Incomplete=true, got %+v", mergedMixed)
	}
}

func TestTwoGeneratorDistributedLLMQuantilesAndOpenWorkloadSharding(t *testing.T) {
	svc := newIsolatedService(t, 10)
	fx := llmfixture.NewServer(llmfixture.Config{
		BaseTTFT:     10 * time.Millisecond,
		BaseITL:      2 * time.Millisecond,
		OutputTokens: 6,
	})
	defer fx.Close()

	// Part 1: Verify 2-generator sketch-merged quantiles match single-process quantiles.
	var singleSummary, gen1Summary, gen2Summary *engine.LLMSummary
	for i := 0; i < 20; i++ {
		ttft := float64(10 + i*2)
		tpot := float64(2 + (i % 5))
		dist := &protocol.DiagnosticDistribution{}
		for tok := 0; tok < 5; tok++ {
			dist.Add(tpot)
		}
		res := &protocol.Result{
			ActionName: "chat",
			StatusCode: 200,
			Success:    true,
			Duration:   time.Duration(ttft+tpot*5) * time.Millisecond,
			ServerTelemetry: &protocol.ServerTelemetry{
				LLM: &protocol.LLMDiagnostics{
					Dialect:             "openai_chat",
					Model:               "gpt-4o-mini",
					Stream:              true,
					TTFTMs:              ttft,
					FirstVisibleTokenMs: ttft,
					TPOTMs:              tpot,
					E2EMs:               ttft + tpot*5,
					ITLDistribution:     dist,
					PromptTokens:        64,
					CompletionTokens:    6,
					TotalTokens:         70,
					FinishReason:        "stop",
					InputLengthBand:     "<256",
				},
			},
		}
		engine.ObserveLLMAction(&singleSummary, res)
		if i%2 == 0 {
			engine.ObserveLLMAction(&gen1Summary, res)
		} else {
			engine.ObserveLLMAction(&gen2Summary, res)
		}
	}
	engine.FinalizeLLMSummary(singleSummary)
	engine.FinalizeLLMSummary(gen1Summary)
	engine.FinalizeLLMSummary(gen2Summary)

	pbBatch1 := svc.metricsBatchForMetrics("test-dist", 1, GeneratorStats{}, nil, metricsAggregateSet{
		NativeVersion: 3,
		Actions:       []MetricAggregate{{Name: "chat", Count: 10, LLM: gen1Summary}},
	}, false, nil, "RUNNING", "", nil)
	pbBatch2 := svc.metricsBatchForMetrics("test-dist", 1, GeneratorStats{}, nil, metricsAggregateSet{
		NativeVersion: 3,
		Actions:       []MetricAggregate{{Name: "chat", Count: 10, LLM: gen2Summary}},
	}, false, nil, "RUNNING", "", nil)

	dec1 := metricAggregatesFromProto(pbBatch1.GetActionMetrics())
	dec2 := metricAggregatesFromProto(pbBatch2.GetActionMetrics())
	mergedAgg := mergeMetricAggregates([]MetricAggregate{dec1[0], dec2[0]})
	if mergedAgg == nil || mergedAgg.LLM == nil {
		t.Fatalf("expected merged LLM aggregate")
	}
	if mergedAgg.LLM.Incomplete {
		t.Fatalf("expected complete merged LLM summary across two v1 generators")
	}
	if mergedAgg.LLM.Requests != singleSummary.Requests ||
		math.Abs(mergedAgg.LLM.P95TTFTMs-singleSummary.P95TTFTMs) > 1e-6 ||
		math.Abs(mergedAgg.LLM.P95TPOTMs-singleSummary.P95TPOTMs) > 1e-6 ||
		math.Abs(mergedAgg.LLM.P95ITLMs-singleSummary.P95ITLMs) > 1e-6 {
		t.Fatalf("2-generator merged quantiles (%+v) did not match single-process quantiles (%+v)", mergedAgg.LLM, singleSummary)
	}

	// Part 2: Verify 2-generator open-workload sharding against llmfixture.NewServer offers target TPS (not 2x target TPS).
	const targetTPS = 16.0
	scriptBytes, err := json.Marshal(&dsl.Script{
		ID:       "llm-open-sharded",
		Name:     "llm-open-sharded",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: fx.URL,
			Pacing: dsl.PacingConfig{
				Type:           "tps",
				TPS:            targetTPS,
				ArrivalProcess: "uniform",
			},
			RunLogic: dsl.RunLogicConfig{
				WorkloadModel: "open",
				MaxInFlight:   32,
			},
		},
		Actions: []dsl.Action{
			{
				Name:   "chat",
				Type:   "llm_request",
				Target: fx.URL + "/v1/chat/completions",
				Params: map[string]any{
					"model":      "gpt-4o-mini",
					"prompt":     "Hello shard",
					"max_tokens": 6,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal script: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var services []*Service
	for shard := 0; shard < 2; shard++ {
		shardSvc := newIsolatedService(t, 16, WithExecution())
		t.Cleanup(func() { _ = shardSvc.Shutdown(context.Background()) })
		ack, err := shardSvc.AssignWork(ctx, &pb.WorkAssignment{
			TestId:                "llm-open-run",
			GroupName:             "users",
			ScriptId:              "llm-open-sharded",
			ScriptContent:         scriptBytes,
			VuserCount:            4,
			TargetDurationSeconds: 1,
			ShardOrdinal:          uint32(shard),
			ShardCount:            2,
			VuserIdOffset:         uint64(shard) * DistributedVUserIDNamespaceSize,
		})
		if err != nil || !ack.GetAccepted() {
			t.Fatalf("AssignWork shard %d: %v %+v", shard, err, ack)
		}
		services = append(services, shardSvc)
	}
	for _, shardSvc := range services {
		ack, err := shardSvc.CommitWork(ctx, &pb.WorkCommitRequest{TestId: "llm-open-run"})
		if err != nil || !ack.GetAccepted() {
			t.Fatalf("CommitWork: %v %+v", err, ack)
		}
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		active := 0
		for _, shardSvc := range services {
			shardSvc.mu.RLock()
			active += len(shardSvc.executions)
			shardSvc.mu.RUnlock()
		}
		if active == 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("2-generator open workload run did not finish")
		case <-ticker.C:
		}
	}
	gotReq := len(fx.Requests())
	// With targetTPS=16 over 1s across 2 shards (8 TPS/shard), total requests should be ~16 (well below 2x=32).
	if gotReq < 8 || gotReq > 24 {
		t.Fatalf("expected 2-shard open workload total requests near %v (8..24), got %d", targetTPS, gotReq)
	}
}
