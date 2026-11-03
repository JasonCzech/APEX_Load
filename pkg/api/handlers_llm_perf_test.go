package api

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/llmfixture"
	"github.com/jasonczech/apexload/pkg/llmperf/compare"
	"github.com/jasonczech/apexload/pkg/llmperf/econ"
	"github.com/jasonczech/apexload/pkg/llmperf/plan"
	"github.com/jasonczech/apexload/pkg/llmperf/synth"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/rca/signatures"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestCapacitySweepExpandAndReportKneeFit(t *testing.T) {
	sloTTFT := 250.0
	sloTPOT := 40.0
	sweepLevels := []float64{1, 2, 4, 8, 16, 32, 64}
	cfg := &CapacitySweepConfig{
		Mode:            "concurrency",
		Levels:          sweepLevels,
		WarmupDuration:  "5s",
		MeasureDuration: "15s",
		SLOTTFTMs:       &sloTTFT,
		SLOTPOTMs:       &sloTPOT,
	}
	stages, totalDur, err := ExpandCapacitySweepStages(cfg)
	if err != nil {
		t.Fatalf("ExpandCapacitySweepStages failed: %v", err)
	}
	if len(stages) != 14 {
		t.Fatalf("expected 14 stages (7 warmup + 7 measure), got %d", len(stages))
	}
	if totalDur != 140*time.Second {
		t.Fatalf("expected total duration 140s, got %v", totalDur)
	}

	// Drive llmfixture.BatchSimulator with max_num_seqs = 16 across concurrency 1..64.
	sim := llmfixture.NewBatchSimulator(llmfixture.SimulatorConfig{
		MaxNumSeqs:          16,
		KVCacheCapacityToks: 16 * 288,
		BasePrefill:         35 * time.Millisecond,
		PrefillPerToken:     60 * time.Microsecond,
		BaseDecodePerToken:  12 * time.Millisecond,
		DecodeBatchSlope:    250 * time.Microsecond,
	})

	baseTime := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	var summaries []IntervalSummaryRecord
	var txs []TransactionEvent
	var monPoints []analysis.MetricPoint

	var rcaTTFTPoints []rca.SeriesPoint
	var rcaTPOTPoints []rca.SeriesPoint
	var rcaKVPoints []rca.SeriesPoint
	rcaClock := baseTime

	cur := baseTime
	for idx, lvl := range sweepLevels {
		vusers := int(lvl)
		activeNow := min(vusers, 16)
		releases := make([]func(), 0, activeNow)
		var lastPrefill time.Duration
		var lastITLs []time.Duration
		for i := 0; i < activeNow; i++ {
			_, prefill, itls, _, release, err := sim.AdmitAndSchedule(t.Context(), 256, 32)
			if err != nil {
				t.Fatalf("AdmitAndSchedule failed: %v", err)
			}
			lastPrefill = prefill
			lastITLs = itls
			releases = append(releases, release)
		}
		snapSim := sim.Snapshot()
		for _, rel := range releases {
			rel()
		}

		ttftMs := float64(lastPrefill) / float64(time.Millisecond)
		tpotMs := 12.0
		if len(lastITLs) > 0 {
			tpotMs = float64(lastITLs[0]) / float64(time.Millisecond)
		}
		if vusers > 16 {
			overflowRatio := float64(vusers-16) / 16.0
			// Queueing behind max_num_seqs=16 and KV-cache contention above the knee.
			ttftMs += overflowRatio * 240.0
			tpotMs += overflowRatio * 18.0
		}
		e2eMs := ttftMs + 32*tpotMs
		tps := float64(activeNow) * (1000.0 / e2eMs)

		// Warmup interval (should be excluded from report)
		cur = cur.Add(5 * time.Second)
		summaries = append(summaries, IntervalSummaryRecord{
			Timestamp:           cur,
			SampleWindowSeconds: 5,
			TPS:                 tps,
			TotalTx:             int64(math.Max(1, tps*5)),
			LoadLevels: []ActiveLoadLevel{{
				Group:        "__total__",
				StageIndex:   idx * 2,
				Name:         stages[idx*2].Name,
				TargetVUsers: vusers,
				Warmup:       true,
				Duration:     "5s",
			}},
		})

		// Measurement interval
		cur = cur.Add(15 * time.Second)
		sketch := engine.NewDurationSketch()
		for r := 0; r < 20; r++ {
			sketch.Add(e2eMs)
			txs = append(txs, TransactionEvent{
				EventID:    "ev",
				Timestamp:  cur.Add(-5 * time.Second),
				DurationMs: e2eMs,
				StatusCode: 200,
				Success:    true,
				ServerTelemetry: &protocol.ServerTelemetry{
					LLM: &protocol.LLMDiagnostics{
						Dialect:          "openai_chat",
						Model:            "llama-3-8b",
						Stream:           true,
						TTFTMs:           ttftMs,
						TPOTMs:           tpotMs,
						E2EMs:            e2eMs,
						PromptTokens:     256,
						CompletionTokens: 32,
						GoldenPromptID:   "golden-1",
						OutputHash:       0x123456789abcdef0,
					},
				},
			})
		}
		snap := sketch.Snapshot()
		summaries = append(summaries, IntervalSummaryRecord{
			Timestamp:                 cur,
			SampleWindowSeconds:       15,
			TPS:                       tps,
			TotalTx:                   20,
			LoadLevelDurationSketches: map[string]*engine.DurationSketchSnapshot{"__total__": &snap},
			LoadLevels: []ActiveLoadLevel{{
				Group:        "__total__",
				StageIndex:   idx*2 + 1,
				Name:         stages[idx*2+1].Name,
				TargetVUsers: vusers,
				Warmup:       false,
				Duration:     "15s",
			}},
		})
		kvVal := snapSim.KVCacheUsageRatio
		monPoints = append(monPoints, analysis.MetricPoint{
			TimestampUTC: cur.Add(-5 * time.Second),
			MetricName:   "vllm:kv_cache_usage_perc",
			Value:        &kvVal,
		})

		// Emit 6 10s buckets per level into the RCA series so Detect has a 24-bucket baseline before the knee.
		for b := 0; b < 6; b++ {
			jitter := float64((b%3)-1) * 0.2
			kvBucket := kvVal + float64((b%3)-1)*0.002
			ttftBucket := ttftMs + jitter
			tpotBucket := tpotMs + jitter*0.1
			if idx < 4 {
				// Pre-knee steady baseline (levels 1, 2, 4, 8 -> 24 buckets)
				kvBucket = 0.25 + float64(b%3)*0.003
				ttftBucket = 52.0 + jitter
				tpotBucket = 14.0 + jitter*0.1
			} else if idx == 4 {
				// At the knee (level 16): KV cache hits ~100% while TTFT/TPOT are still within pre-queue baseline
				ttftBucket = 54.0 + jitter
				tpotBucket = 14.5 + jitter*0.1
			}
			rcaKVPoints = append(rcaKVPoints, rca.SeriesPoint{At: rcaClock, Value: kvBucket, Valid: true})
			rcaTTFTPoints = append(rcaTTFTPoints, rca.SeriesPoint{At: rcaClock, Value: ttftBucket, Valid: true})
			rcaTPOTPoints = append(rcaTPOTPoints, rca.SeriesPoint{At: rcaClock, Value: tpotBucket, Valid: true})
			rcaClock = rcaClock.Add(10 * time.Second)
		}
	}

	deployments := []econ.DeploymentSpec{{
		ID:                "dep-1",
		Name:              "A100x1",
		BillingMode:       "gpu_hourly",
		GPUCount:          1,
		GPUCostPerHourUSD: 4.0,
	}}
	report := BuildCapacitySweepReport("test-sweep-1", "Sweep 1..64", cfg, summaries, txs, monPoints, deployments)
	if len(report.Levels) != 7 {
		t.Fatalf("expected 7 non-warmup levels, got %d", len(report.Levels))
	}
	if report.Knee == nil || report.Knee.Load != 16 {
		t.Fatalf("expected knee at load 16, got %+v (note=%s)", report.Knee, report.KneeNote)
	}
	if report.MaxSustainableRate == nil || report.MaxSustainableRate.OfferedLoad != 16 {
		t.Fatalf("expected max sustainable rate at load 16, got %+v", report.MaxSustainableRate)
	}
	if report.Levels[4].ServerKVCachePeakPct == nil || *report.Levels[4].ServerKVCachePeakPct < 99.0 {
		t.Fatalf("expected level 16 server KV cache peak ~100%%, got %+v", report.Levels[4].ServerKVCachePeakPct)
	}
	if report.Levels[4].CostPerMillionOutputTokensUSD == nil || *report.Levels[4].CostPerMillionOutputTokensUSD <= 0 {
		t.Fatalf("expected cost per million output tokens > 0, got %+v", report.Levels[4].CostPerMillionOutputTokensUSD)
	}
	if report.GoldenDrift == nil || report.GoldenDrift.EvaluatedPrompts != 1 || report.GoldenDrift.DriftedPrompts != 0 {
		t.Fatalf("expected 1 stable golden prompt, got %+v", report.GoldenDrift)
	}
	csvOut := FormatCapacitySweepCSV(report)
	if !strings.Contains(csvOut, "ttft_p95_ms") || !strings.Contains(csvOut, "Level 5 (16)") {
		t.Fatalf("unexpected CSV output: %s", csvOut)
	}

	// Run rca.Detect + rca.Hypothesize over the simulator-driven 1..64 sweep series and verify kv_cache_saturation.
	nodeID := rca.EntityNodeID("prometheus", "VLLM-1")
	rcaSeries := []rca.Series{
		{
			ID: "transaction/chat/ttft_p95_ms", Kind: rca.SeriesLatency, Unit: "ms",
			Measures: signatures.KindTTFT, Observation: rca.ObservationInterval, Bucket: 10 * time.Second,
			Evidence: rca.EvidenceRef{ID: "ev-ttft", Kind: "interval", Locator: "ttft"},
			Points:   rcaTTFTPoints,
		},
		{
			ID: "transaction/chat/tpot_p95_ms", Kind: rca.SeriesLatency, Unit: "ms",
			Measures: signatures.KindTPOT, Observation: rca.ObservationInterval, Bucket: 10 * time.Second,
			Evidence: rca.EvidenceRef{ID: "ev-tpot", Kind: "interval", Locator: "tpot"},
			Points:   rcaTPOTPoints,
		},
		{
			ID: "monitor/prometheus/vllm/vllm:kv_cache_usage_perc/1", Kind: rca.SeriesProvider, Unit: "ratio",
			Measures: signatures.KindKVCache, Nodes: []string{nodeID}, Observation: rca.ObservationInterval, Bucket: 10 * time.Second,
			Evidence: rca.EvidenceRef{ID: "ev-kv", Kind: "monitor_series", Locator: "kv"},
			Points:   rcaKVPoints,
		},
	}
	graph, _ := rca.BuildGraph(rca.GraphInput{
		Calls:    []rca.GraphCall{{Transaction: "chat", Action: "POST /v1/chat/completions", DependencyType: "http", Target: "vllm.local"}},
		Entities: []rca.GraphEntity{{Provider: "prometheus", ID: "VLLM-1", Name: "vllm.local", Type: "SERVICE"}},
		Mappings: []rca.GraphMapping{{ID: "m1", Revision: 1, Dependency: "http:vllm.local", Provider: "prometheus", EntityID: "VLLM-1", Decision: rca.MappingApproved}},
	}, nil)
	levels := []rca.LoadLevel{{Start: baseTime, End: rcaClock, VUsers: 16}}
	anomalies, detection, _ := rca.Detect(
		rca.DetectionInput{Levels: levels, Series: rcaSeries},
		rca.DefaultDetectParams,
		rca.NewBudgeter(rca.DefaultBudget, nil, nil),
	)
	if len(anomalies) < 2 {
		t.Fatalf("expected >=2 anomalies detected from 1..64 simulator sweep series, got %d: %+v", len(anomalies), anomalies)
	}
	hyps, outcome, _ := rca.Hypothesize(anomalies, rcaSeries, detection.Judged, graph, levels, rca.DefaultDetectParams)
	if outcome != rca.OutcomeSupported || len(hyps) == 0 || hyps[0].Signature != "kv_cache_saturation" {
		t.Fatalf("expected RCA outcome supported with top-1 kv_cache_saturation, got outcome=%s hyps=%+v", outcome, hyps)
	}
}

func TestLLMLatencyDecompositionAndPerfHandlers(t *testing.T) {
	var sum *engine.LLMSummary
	qMs, pMs, dMs := 25.0, 55.0, 200.0
	for i := 0; i < 10; i++ {
		engine.ObserveLLMAction(&sum, &protocol.Result{
			Success:    true,
			StatusCode: 200,
			Duration:   300 * time.Millisecond,
			ServerTelemetry: &protocol.ServerTelemetry{
				LLM: &protocol.LLMDiagnostics{
					Stream:           true,
					TTFTMs:           95.0,
					TPOTMs:           10.0,
					E2EMs:            300.0,
					PromptTokens:     512,
					CompletionTokens: 20,
					ServerQueueMs:    &qMs,
					ServerPrefillMs:  &pMs,
					ServerDecodeMs:   &dMs,
				},
			},
		})
	}
	decomp := BuildLLMLatencyDecomposition("test-1", sum, 10.0, nil)
	if decomp.Source != "response_headers" || math.Abs(decomp.NetworkGatewayAvgMs-20.0) > 1e-6 {
		t.Fatalf("unexpected decomposition: %+v", decomp)
	}

	s := &Server{}
	// 1. Prompt dataset import
	impBody, _ := json.Marshal(LLMPromptDatasetImportRequest{
		Payload: `{"prompt":"Hello api_key=\"secret123\" world"}`,
	})
	rec := httptest.NewRecorder()
	s.handleImportLLMPromptDataset(rec, httptest.NewRequest(http.MethodPost, "/api/v1/llm/prompt-datasets/import", bytes.NewReader(impBody)))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "secret123") {
		t.Fatalf("import failed or leaked secret: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 2. Synthesize
	synBody, _ := json.Marshal(synth.Config{Count: 10, MeanInputTokens: 256, ExpectedOutputTokens: 64, Seed: 42})
	rec = httptest.NewRecorder()
	s.handleSynthesizeLLMPromptDataset(rec, httptest.NewRequest(http.MethodPost, "/api/v1/llm/prompt-datasets/synthesize", bytes.NewReader(synBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("synthesize failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 3. Calibrate
	calBody, _ := json.Marshal(LLMPromptDatasetCalibrateRequest{
		Samples: []LLMCalibrationSample{{Chars: 420, Tokens: 100}},
	})
	rec = httptest.NewRecorder()
	s.handleCalibrateLLMPromptDataset(rec, httptest.NewRequest(http.MethodPost, "/api/v1/llm/prompt-datasets/calibrate", bytes.NewReader(calBody)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "4.2") {
		t.Fatalf("calibrate failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 4. Capacity plan
	planBody, _ := json.Marshal(plan.Request{
		Steps:                 []plan.StepObservation{{StepIndex: 1, OfferedTPS: 10, AchievedTPS: 10, GoodputTPS: 10, TTFTP50Ms: 80, TTFTP95Ms: 120, TPOTP50Ms: 15, TPOTP95Ms: 25}},
		SLA:                   plan.SLAPolicy{MaxTTFTP95Ms: 300, MaxTPOTP95Ms: 50},
		TargetUsers:           120,
		RequestsPerUserPerMin: 30,
	})
	rec = httptest.NewRecorder()
	s.handleEvaluateLLMCapacityPlan(rec, httptest.NewRequest(http.MethodPost, "/api/v1/llm/capacity-plan", bytes.NewReader(planBody)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"extrapolated":true`) {
		t.Fatalf("capacity plan failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 5. Prometheus packs
	rec = httptest.NewRecorder()
	s.handleGetPrometheusMetricPacks(rec, httptest.NewRequest(http.MethodGet, "/api/v1/monitoring/prometheus-packs?pack=vllm&pod_label=exported_pod", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "exported_pod") {
		t.Fatalf("prometheus packs failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 6. A/B Compare
	cmpBody, _ := json.Marshal(LLMCompareRequest{
		Baseline: compare.RunSnapshot{
			TestID:            "run-a",
			Model:             "llama-3-8b-fp16",
			WorkloadDigest:    "digest-1",
			Requests:          50,
			TTFTP50Ms:         120,
			TTFTP95Ms:         180,
			TPOTP50Ms:         25,
			TPOTP95Ms:         35,
			SystemOutputTPS:   400,
			IntervalTTFTP95Ms: []float64{175, 180, 185, 178, 182},
			IntervalTPOTP95Ms: []float64{34, 35, 36, 35, 35},
		},
		Candidate: compare.RunSnapshot{
			TestID:            "run-b",
			Model:             "llama-3-8b-fp8",
			WorkloadDigest:    "digest-1",
			Requests:          50,
			TTFTP50Ms:         85,
			TTFTP95Ms:         115,
			TPOTP50Ms:         16,
			TPOTP95Ms:         21,
			SystemOutputTPS:   680,
			IntervalTTFTP95Ms: []float64{112, 115, 118, 114, 116},
			IntervalTPOTP95Ms: []float64{20, 21, 22, 21, 21},
		},
		Seed: 42,
	})
	rec = httptest.NewRecorder()
	s.handleCompareLLMRuns(rec, httptest.NewRequest(http.MethodPost, "/api/v1/llm/compare", bytes.NewReader(cmpBody)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"direction":"improved"`) {
		t.Fatalf("compare failed: status=%d body=%s", rec.Code, rec.Body.String())
	}

	d1 := computeLLMWorkloadDigest(&RunArchiveManifest{
		Provenance: RunArchiveProvenance{
			ScenarioID:  "scen-1",
			TotalVUsers: 10,
			Duration:    "1m",
		},
	})
	d2 := computeLLMWorkloadDigest(&RunArchiveManifest{
		Provenance: RunArchiveProvenance{
			ScenarioID:  "scen-1",
			TotalVUsers: 20,
			Duration:    "1m",
		},
	})
	if d1 == "" || d2 == "" || d1 == d2 {
		t.Fatalf("expected distinct non-empty workload digests, got d1=%q d2=%q", d1, d2)
	}
}

type genAISpanRoundTripper struct {
	fn func(*http.Request) (*http.Response, error)
}

func (rt genAISpanRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return rt.fn(r)
}

func TestHandleGenAISpanLookupElasticAndDynatrace(t *testing.T) {
	server, store := businessKeyServer(t)
	if server.providerMonitoringCancel != nil {
		server.providerMonitoringCancel()
	}

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	_ = exemplarArchiveFixture(t, server, store, "genai-run", func(i int, tx *TransactionEvent) {
		if i == 0 {
			tx.ActionName = "chat_completion"
			tx.SentTraceID = traceID
			tx.ServerTelemetry = &protocol.ServerTelemetry{
				LLM: &protocol.LLMDiagnostics{
					Dialect:          "openai_chat",
					Model:            "gpt-4o-mini",
					Stream:           true,
					TTFTMs:           88,
					TPOTMs:           11,
					E2EMs:            260,
					PromptTokens:     120,
					CompletionTokens: 16,
					TotalTokens:      136,
					FinishReason:     "stop",
				},
			}
		}
	})

	conn := &storage.ELKConnection{
		ID:               "elk-genai",
		ProjectID:        storage.DefaultProjectID,
		Name:             "OTel Spans",
		Enabled:          true,
		Endpoint:         "https://elk.example",
		IndexPattern:     "traces-apm*",
		TimestampField:   "@timestamp",
		TraceIDField:     "trace.id",
		AuthType:         storage.ELKAuthAPIKey,
		APIKeySecretName: "elk-token",
	}
	if err := store.CreateELKConnection(conn); err != nil {
		t.Fatalf("CreateELKConnection: %v", err)
	}
	source := storage.MonitorSource{
		ID:              "src-elastic-genai",
		ProjectID:       storage.DefaultProjectID,
		Name:            "Elastic OTel Traces",
		Enabled:         true,
		Pack:            "elastic-query",
		ExecutorMode:    "direct",
		ConnectionID:    conn.ID,
		IntervalSeconds: 30,
		ElasticQuery:    &storage.ElasticQuerySelection{ProjectID: storage.DefaultProjectID},
	}
	if _, err := store.SaveMonitorSource(source, 0); err != nil {
		t.Fatalf("SaveMonitorSource: %v", err)
	}

	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", storage.DefaultProjectID)
	runRec, err := store.GetTest("genai-run")
	if err != nil {
		t.Fatalf("GetTest: %v", err)
	}
	runRec.MonitorBindings = &storage.MonitorBindings{
		Sources: []storage.MonitorBinding{{SourceID: source.ID}},
	}
	runRec.DynatraceCapture = dynatraceCaptureForStorage(&DynatraceCaptureConfig{
		ProjectID:           storage.DefaultProjectID,
		ConnectionID:        "dt-genai",
		ConnectionName:      "Dynatrace Grail",
		PlatformURL:         "https://tenant.apps.dynatrace.com",
		PlatformAuthType:    storage.DynatracePlatformAuthToken,
		PlatformTokenSecret: "dt-token",
		LogTraceIDField:     "trace.id",
	})
	if err := store.SaveTest(runRec); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	if jobs, err := store.StartRunMonitors(storage.DefaultProjectID, "genai-run", "", exemplarFixtureStart, exemplarFixtureStart.Add(time.Hour)); err != nil || len(jobs) != 1 {
		t.Fatalf("StartRunMonitors: %v (jobs=%d)", err, len(jobs))
	}

	server.runner.projectSecretStore = &devSecretStore{value: "fixture-token"}
	server.elasticHTTPClient = &http.Client{
		Transport: genAISpanRoundTripper{
			fn: func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.Host, "dynatrace.com") {
					reqBody, _ := io.ReadAll(r.Body)
					if strings.Contains(string(reqBody), "or isNotNull(") {
						t.Errorf("Dynatrace DQL query must not widen trace filter with unconditional OR clause: %s", string(reqBody))
					}
					if !strings.Contains(string(reqBody), traceID) {
						t.Errorf("Dynatrace DQL query missing traceID %s: %s", traceID, string(reqBody))
					}
					out, _ := json.Marshal(map[string]any{
						"state": "SUCCEEDED",
						"result": map[string]any{
							"records": []map[string]any{
								{
									"trace.id":                       traceID,
									"span.id":                        "dt-span-1",
									"span.name":                      "chat gpt-4o-mini",
									"service.name":                   "llm-gateway",
									"start_time":                     exemplarFixtureStart.Format(time.RFC3339Nano),
									"duration_ms":                    245.0,
									"gen_ai.system":                  "openai",
									"gen_ai.operation.name":          "chat",
									"gen_ai.request.model":           "gpt-4o-mini",
									"gen_ai.response.model":          "gpt-4o-mini-2024-07-18",
									"gen_ai.usage.input_tokens":      60,
									"gen_ai.usage.output_tokens":     8,
									"gen_ai.response.finish_reasons": []string{"stop"},
								},
							},
						},
					})
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(string(out))),
					}, nil
				}
				// Elastic response
				out, _ := json.Marshal(map[string]any{
					"timed_out": false,
					"hits": map[string]any{
						"total": map[string]any{"value": 1, "relation": "eq"},
						"hits": []map[string]any{
							{
								"_source": map[string]any{
									"@timestamp":   exemplarFixtureStart.Format(time.RFC3339Nano),
									"trace.id":     traceID,
									"span.id":      "es-span-1",
									"span.name":    "openai.chat",
									"service.name": "vllm-worker",
									"duration_ms":  240.0,
									"attributes": map[string]any{
										"gen_ai.system":                  "openai",
										"gen_ai.operation.name":          "chat",
										"gen_ai.request.model":           "gpt-4o-mini",
										"gen_ai.response.model":          "gpt-4o-mini-2024-07-18",
										"gen_ai.usage.input_tokens":      64,
										"gen_ai.usage.output_tokens":     8,
										"gen_ai.response.finish_reasons": []string{"stop"},
									},
								},
							},
						},
					},
				})
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(string(out))),
				}, nil
			},
		},
	}

	reply := timeslotRequest(t, server, http.MethodPost, "/api/v1/tests/genai-run/archive/genai-spans/lookup", GenAISpanLookupRequest{
		Limit: 10,
	})
	if reply.Code != http.StatusOK {
		t.Fatalf("handleGenAISpanLookup status=%d body=%s", reply.Code, reply.Body.String())
	}
	var resp GenAISpanLookupResponse
	if err := json.Unmarshal(reply.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal GenAISpanLookupResponse: %v", err)
	}
	if len(resp.Spans) != 2 {
		t.Fatalf("expected 2 spans (1 Elastic + 1 Dynatrace), got %d (%+v)", len(resp.Spans), resp)
	}
	if len(resp.Matches) != 1 || resp.Matches[0].TraceID != traceID {
		t.Fatalf("expected 1 matched transaction for trace %s, got %+v", traceID, resp.Matches)
	}
	match := resp.Matches[0]
	if match.ServerModel != "gpt-4o-mini-2024-07-18" || match.ServerInputTokens != 124 || match.ServerOutputTokens != 16 {
		t.Fatalf("unexpected server token/model aggregation: %+v", match)
	}
	if match.InputTokenDelta != 4 || match.OutputTokenDelta != 0 {
		t.Fatalf("expected InputTokenDelta=4 OutputTokenDelta=0, got in=%d out=%d", match.InputTokenDelta, match.OutputTokenDelta)
	}
}
