package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func testRoadmapBudgetReceipt(capturedAt time.Time, sampleAgeMs float64) *TelemetryBudgetPolicy {
	settings := RunArchiveExecutionSettingsSnapshot{
		General: GeneralSettings{MetricsIntervalSeconds: 1},
		Thresholds: ThresholdSettings{
			MaxTelemetrySampleAgeMs:        float64Setting(sampleAgeMs),
			MaxTelemetryGeneratorLagMs:     float64Setting(500),
			MaxTelemetryTransportLagMs:     float64Setting(500),
			MaxTelemetryControllerFanoutMs: float64Setting(250),
			MaxTelemetryBrowserRenderLagMs: float64Setting(250),
		},
	}
	return captureTelemetryBudgetPolicy(settings, true, capturedAt)
}

func TestTelemetryPublicationWindowStartBoundsAndClientRenderCalibration(t *testing.T) {
	server, test, _ := clientRenderTestServer(t, true)
	base := time.Now().UTC().Truncate(time.Millisecond)

	// 1. Calibrate generator clock (+50 ms offset, 10 ms uncertainty) and verify
	// qualifyCrossHostTelemetry + OldestObservationStartAt on TelemetryPublication.
	calibrations := newClockCalibrations(30 * time.Second)
	calibrations.observe("gen-1:50051", 50*time.Millisecond, 10*time.Millisecond, base)
	offsetMs, quality := calibrations.quality("gen-1:50051", base)
	if !quality.Qualified || offsetMs != 50 || quality.UncertaintyMs != 10 {
		t.Fatalf("unexpected calibration quality: offset=%v quality=%+v", offsetMs, quality)
	}

	collectionEndGenMs := base.Add(50 * time.Millisecond).UnixMilli()
	windowStartGenMs := collectionEndGenMs - 1000 // 1,000 ms collection window
	rawTelemetry := &generatorrpc.MetricsTelemetry{
		CollectedAtUnixMs:          collectionEndGenMs,
		PublishedAtUnixMs:          collectionEndGenMs + 5,
		ControllerReceivedAtUnixMs: base.Add(15 * time.Millisecond).UnixMilli(),
		WindowMs:                   1000,
		CollectionSequence:         1,
	}
	qualifyCrossHostTelemetry(
		rawTelemetry,
		offsetMs,
		quality,
		generatorrpc.MetricsCollectionWindow{
			Group: "default",
			Start: time.UnixMilli(windowStartGenMs).UTC(),
			End:   time.UnixMilli(collectionEndGenMs).UTC(),
		},
	)
	if rawTelemetry.CollectedAtQuality == nil || !rawTelemetry.CollectedAtQuality.Qualified {
		t.Fatalf("expected qualified cross-host telemetry, got %+v", rawTelemetry.CollectedAtQuality)
	}
	wantOldestObsMs := float64(windowStartGenMs) - 50.0 - 10.0
	if rawTelemetry.OldestObservationStartBoundUnixMs != wantOldestObsMs {
		t.Fatalf("OldestObservationStartBoundUnixMs = %v, want %v", rawTelemetry.OldestObservationStartBoundUnixMs, wantOldestObsMs)
	}

	server.runner.mu.Lock()
	test.clockCalibrations = calibrations
	live1 := &LiveMetrics{
		Timestamp:           base.Add(20 * time.Millisecond),
		SampleWindowSeconds: 1.0,
		Telemetry: &TelemetryHealth{
			CollectedAt:                 time.UnixMilli(collectionEndGenMs).UTC(),
			PublishedAt:                 base.Add(20 * time.Millisecond),
			oldestObservationStartAt:    unixMillisFloatToTime(rawTelemetry.OldestObservationStartBoundUnixMs),
			newestCollectedControllerAt: unixMillisFloatToTime(rawTelemetry.NewestCollectedAtControllerUnixMs),
			SampleAgeQuality:            rawTelemetry.CollectedAtQuality,
		},
	}
	assignTelemetryPublicationLocked(test, live1)
	pub1 := live1.Telemetry.Publication.clone()
	stageRecorderLocked(test).RegisterPresentationProof(pub1.Epoch, pub1.Sequence, "proof-seq-1")
	server.runner.mu.Unlock()

	if pub1 == nil || pub1.OldestObservationStartAt == nil {
		t.Fatalf("expected OldestObservationStartAt on TelemetryPublication, got %+v", pub1)
	}
	if gotMs := pub1.OldestObservationStartAt.UnixMilli(); gotMs != int64(wantOldestObsMs) {
		t.Fatalf("OldestObservationStartAt = %d ms, want %d ms", gotMs, int64(wantOldestObsMs))
	}

	// 2. Authenticate browser clock calibration exchange and verify <= 1 per 30s rate limit.
	sendMono1 := 1000.0
	reqBody1, _ := json.Marshal(map[string]any{
		"client_id":           "tab-cal-1",
		"client_send_mono_ms": sendMono1,
		"samples": []clientRenderSample{
			{
				ReceiveToRenderMs: 12,
				State:             "visible",
				ObservedAt:        time.Now().UTC(),
				DeliverySignal:    DeliverySignalVerifiedFramePresentation,
				PresentationProof: "proof-seq-1",
				Publication:       &clientRenderPublication{Epoch: pub1.Epoch, Sequence: pub1.Sequence},
			},
		},
	})
	resp1 := postClientRender(server, test.ID, string(reqBody1))
	if resp1.Code != http.StatusAccepted {
		t.Fatalf("first client-render post status = %d, body = %s", resp1.Code, resp1.Body.String())
	}
	var parsed1 ClientRenderReportResult
	if err := json.Unmarshal(resp1.Body.Bytes(), &parsed1); err != nil {
		t.Fatalf("unmarshal response 1: %v", err)
	}
	if parsed1.ClockCalibration == nil || parsed1.ClockCalibration[0] != sendMono1 {
		t.Fatalf("expected echoed ClockCalibration for first request, got %+v", parsed1.ClockCalibration)
	}

	// Advance the 2-second report limiter so we isolate and verify the 30-second calibration rate limiter.
	server.clientRenderReports.mu.Lock()
	for k := range server.clientRenderReports.last {
		server.clientRenderReports.last[k] = time.Now().Add(-3 * time.Second)
	}
	server.clientRenderReports.mu.Unlock()

	// Immediate second calibration exchange within 30s from the same viewer/client is rate-limited (429 Too Many Requests).
	reqBodyRateLimited, _ := json.Marshal(map[string]any{
		"client_id":           "tab-cal-1",
		"client_send_mono_ms": sendMono1 + 500,
		"samples": []clientRenderSample{
			{
				ReceiveToRenderMs: 10,
				State:             "visible",
				ObservedAt:        time.Now().UTC(),
			},
		},
	})
	respRateLimited := postClientRender(server, test.ID, string(reqBodyRateLimited))
	if respRateLimited.Code != http.StatusTooManyRequests {
		t.Fatalf("expected rate-limited second calibration within 30s to return 429, got %d (%s)", respRateLimited.Code, respRateLimited.Body.String())
	}

	// 3. Publish a second eligible publication and report its render using the calibrated browser clock
	// and verified_frame_presentation proof.
	now := time.Now().UTC()
	obsStart2 := now.Add(-400 * time.Millisecond)
	newestCol2 := now.Add(-100 * time.Millisecond)
	server.runner.mu.Lock()
	live2 := &LiveMetrics{
		Timestamp: now,
		Telemetry: &TelemetryHealth{
			CollectedAt:                 newestCol2,
			PublishedAt:                 now.Add(-50 * time.Millisecond),
			oldestObservationStartAt:    obsStart2,
			newestCollectedControllerAt: newestCol2,
		},
	}
	assignTelemetryPublicationLocked(test, live2)
	pub2 := live2.Telemetry.Publication.clone()
	stageRecorderLocked(test).RegisterPresentationProof(pub2.Epoch, pub2.Sequence, "proof-seq-2")
	server.runner.mu.Unlock()

	echo := parsed1.ClockCalibration
	calRecvMono := sendMono1 + 10.0
	pub2RecvMono := calRecvMono + 20.0
	pub2RenderMono := pub2RecvMono + 15.0
	reqBody2, _ := json.Marshal(map[string]any{
		"client_id": "tab-cal-1",
		"calibration": map[string]any{
			"client_send_mono_ms":     echo[0],
			"client_recv_mono_ms":     calRecvMono,
			"controller_recv_unix_ms": echo[1],
			"controller_send_unix_ms": echo[2],
		},
		"samples": []map[string]any{
			{
				"receive_to_render_ms": 15.0,
				"received_mono_ms":     pub2RecvMono,
				"rendered_mono_ms":     pub2RenderMono,
				"state":                "visible",
				"observed_at":          now.Format(time.RFC3339Nano),
				"delivery_signal":      "verified_frame_presentation",
				"presentation_proof":   "proof-seq-2",
				"publication": map[string]any{
					"epoch":    pub2.Epoch,
					"sequence": pub2.Sequence,
				},
			},
		},
	})
	resp2 := postClientRender(server, test.ID, string(reqBody2))
	if resp2.Code != http.StatusAccepted {
		t.Fatalf("second client-render post status = %d, body = %s", resp2.Code, resp2.Body.String())
	}

	server.runner.mu.Lock()
	stages := stageRecorderLocked(test).snapshot()
	server.runner.mu.Unlock()

	var e2eStage *TelemetryStageDistribution
	for i := range stages {
		if stages[i].Stage == telemetryStageEndToEnd {
			e2eStage = &stages[i]
			break
		}
	}
	if e2eStage == nil || e2eStage.Samples != 2 || e2eStage.P99Ms == nil || e2eStage.NewestObservationP99Ms == nil {
		t.Fatalf("expected measured end_to_end stage with oldest and newest observation percentiles, got %+v", e2eStage)
	}
	if e2eStage.DeliverySignal != DeliverySignalVerifiedFramePresentation || e2eStage.Advisory {
		t.Fatalf("expected verified_frame_presentation non-advisory end_to_end stage, got signal=%q advisory=%v", e2eStage.DeliverySignal, e2eStage.Advisory)
	}
	if *e2eStage.NewestObservationP99Ms >= *e2eStage.P99Ms {
		t.Fatalf("expected newest observation age (%v ms) < oldest observation start age (%v ms)", *e2eStage.NewestObservationP99Ms, *e2eStage.P99Ms)
	}

	// 4. Verify full eligible-denominator exclusion accounting: if 5 more eligible publications
	// are published and superseded/unreported, P99 (1% exclusion budget) must fail rather than pass!
	server.runner.mu.Lock()
	for i := 0; i < 5; i++ {
		extra := &LiveMetrics{
			Timestamp: now.Add(time.Duration(i+1) * time.Second),
			Telemetry: &TelemetryHealth{
				CollectedAt:              now.Add(time.Duration(i+1) * time.Second),
				PublishedAt:              now.Add(time.Duration(i+1) * time.Second),
				oldestObservationStartAt: now.Add(time.Duration(i)*time.Second + 500*time.Millisecond),
			},
		}
		assignTelemetryPublicationLocked(test, extra)
	}
	stagesAfter := stageRecorderLocked(test).snapshot()
	popAfter := stageRecorderLocked(test).renderPopulation()
	server.runner.mu.Unlock()

	var e2eAfter *TelemetryStageDistribution
	for i := range stagesAfter {
		if stagesAfter[i].Stage == telemetryStageEndToEnd {
			e2eAfter = &stagesAfter[i]
			break
		}
	}
	if e2eAfter == nil || e2eAfter.EligiblePublications != 7 {
		t.Fatalf("expected EligiblePublications = 7 on end_to_end stage, got %+v", e2eAfter)
	}
	criterion := telemetryStageLagCriterionWithPopulation(
		TelemetryLagPercentileRule{Stage: "end_to_end", Percentile: "p99", MaxMs: 2000},
		[]TelemetryStageDistribution{*e2eAfter},
		nil,
		nil,
		popAfter,
	)
	if criterion.Outcome == TelemetryAcceptancePass {
		t.Fatalf("expected P99 criterion to refuse passing when 6 of 7 eligible publications were excluded/unreported, got %+v", criterion)
	}

	// 5. Verify that after >= 30s, a subsequent calibration probe succeeds, updates calibratedAt,
	// and returns a fresh ClockCalibration echo even if a stale calibration object is also sent.
	server.clientRenderReports.mu.Lock()
	for k := range server.clientRenderReports.last {
		server.clientRenderReports.last[k] = time.Now().Add(-3 * time.Second)
	}
	for k := range server.clientRenderReports.calibrations {
		server.clientRenderReports.calibrations[k] = time.Now().Add(-31 * time.Second)
	}
	server.clientRenderReports.mu.Unlock()

	sendMono2 := sendMono1 + 31_000.0
	reqBodyReprobe, _ := json.Marshal(map[string]any{
		"client_id":           "tab-cal-1",
		"client_send_mono_ms": sendMono2,
		"calibration": map[string]any{
			"client_send_mono_ms":     echo[0],
			"client_recv_mono_ms":     calRecvMono,
			"controller_recv_unix_ms": echo[1],
			"controller_send_unix_ms": echo[2],
		},
		"samples": []clientRenderSample{
			{
				ReceiveToRenderMs: 11,
				State:             "visible",
				ObservedAt:        time.Now().UTC(),
			},
		},
	})
	respReprobe := postClientRender(server, test.ID, string(reqBodyReprobe))
	if respReprobe.Code != http.StatusAccepted {
		t.Fatalf("re-probe after 30s status = %d, body = %s", respReprobe.Code, respReprobe.Body.String())
	}
	var parsedReprobe ClientRenderReportResult
	if err := json.Unmarshal(respReprobe.Body.Bytes(), &parsedReprobe); err != nil {
		t.Fatalf("unmarshal re-probe response: %v", err)
	}
	if parsedReprobe.ClockCalibration == nil || parsedReprobe.ClockCalibration[0] != sendMono2 {
		t.Fatalf("expected fresh ClockCalibration echo for re-probe after 30s, got %+v", parsedReprobe.ClockCalibration)
	}

	// Verify that a mismatched/stale refinement within 30s of the re-probe (where refineBrowserCalibration
	// returns false because client_send_mono_ms does not match sendMono2) does NOT fall through into the
	// 30s initial-calibration rate limiter (429) or overwrite the active browser clock calibration.
	test.telemetryStages.mu.Lock()
	var activeClockBefore browserClockCalibration
	for _, clk := range test.telemetryStages.browserClocks {
		activeClockBefore = clk
		break
	}
	test.telemetryStages.mu.Unlock()
	if activeClockBefore.clientSendMonoMs != sendMono2 {
		t.Fatalf("expected active browser clock clientSendMonoMs = %v, got %+v", sendMono2, activeClockBefore)
	}

	server.clientRenderReports.mu.Lock()
	for k := range server.clientRenderReports.last {
		server.clientRenderReports.last[k] = time.Now().Add(-3 * time.Second)
	}
	server.clientRenderReports.mu.Unlock()

	reqBodyStaleRefine, _ := json.Marshal(map[string]any{
		"client_id": "tab-cal-1",
		"calibration": map[string]any{
			"client_send_mono_ms":     echo[0], // stale sendMono1 != active sendMono2
			"client_recv_mono_ms":     calRecvMono,
			"controller_recv_unix_ms": echo[1],
			"controller_send_unix_ms": echo[2],
		},
		"samples": []clientRenderSample{
			{
				ReceiveToRenderMs: 12,
				State:             "visible",
				ObservedAt:        time.Now().UTC(),
			},
		},
	})
	respStaleRefine := postClientRender(server, test.ID, string(reqBodyStaleRefine))
	if respStaleRefine.Code != http.StatusAccepted {
		t.Fatalf("stale refinement within 30s should not return 429, got status = %d, body = %s", respStaleRefine.Code, respStaleRefine.Body.String())
	}
	var parsedStaleRefine ClientRenderReportResult
	if err := json.Unmarshal(respStaleRefine.Body.Bytes(), &parsedStaleRefine); err != nil {
		t.Fatalf("unmarshal stale refinement response: %v", err)
	}
	if parsedStaleRefine.Accepted != 1 || parsedStaleRefine.ClockCalibration != nil {
		t.Fatalf("expected Accepted=1 and nil ClockCalibration on stale refinement, got %+v", parsedStaleRefine)
	}
	test.telemetryStages.mu.Lock()
	var activeClockAfter browserClockCalibration
	for _, clk := range test.telemetryStages.browserClocks {
		activeClockAfter = clk
		break
	}
	test.telemetryStages.mu.Unlock()
	if activeClockAfter.clientSendMonoMs != activeClockBefore.clientSendMonoMs || activeClockAfter.offsetMs != activeClockBefore.offsetMs {
		t.Fatalf("stale refinement corrupted active browser clock calibration: before=%+v after=%+v", activeClockBefore, activeClockAfter)
	}

	// Verify that a valid refinement request matching sendMono2 that arrives within the 2-second per-tab
	// rate-limiting window is rejected with HTTP 429 BEFORE refineBrowserCalibration mutates browserClocks.
	reprobeEcho := parsedReprobe.ClockCalibration
	reqBodyRateLimitedRefine, _ := json.Marshal(map[string]any{
		"client_id": "tab-cal-1",
		"calibration": map[string]any{
			"client_send_mono_ms":     reprobeEcho[0], // matches active sendMono2
			"client_recv_mono_ms":     sendMono2 + 500.0,
			"controller_recv_unix_ms": reprobeEcho[1],
			"controller_send_unix_ms": reprobeEcho[2],
		},
		"samples": []clientRenderSample{
			{
				ReceiveToRenderMs: 12,
				State:             "visible",
				ObservedAt:        time.Now().UTC(),
			},
		},
	})
	respRateLimitedRefine := postClientRender(server, test.ID, string(reqBodyRateLimitedRefine))
	if respRateLimitedRefine.Code != http.StatusTooManyRequests {
		t.Fatalf("expected refinement within 2s per-tab window to return 429, got %d (%s)", respRateLimitedRefine.Code, respRateLimitedRefine.Body.String())
	}
	test.telemetryStages.mu.Lock()
	var activeClockAfter429 browserClockCalibration
	for _, clk := range test.telemetryStages.browserClocks {
		activeClockAfter429 = clk
		break
	}
	test.telemetryStages.mu.Unlock()
	if activeClockAfter429.offsetMs != activeClockBefore.offsetMs || activeClockAfter429.uncertaintyMs != activeClockBefore.uncertaintyMs {
		t.Fatalf("429-rejected refinement mutated active browser clock calibration: before=%+v after=%+v", activeClockBefore, activeClockAfter429)
	}

	// 6. Verify that an unregistered presentation_proof is downgraded to post_commit_paint_opportunity (advisory)
	// even when the recorder's proof registry is empty.
	serverForged, testForged, _ := clientRenderTestServer(t, true)
	nowForged := time.Now().UTC()
	serverForged.runner.mu.Lock()
	liveForged := &LiveMetrics{
		Timestamp: nowForged,
		Telemetry: &TelemetryHealth{
			CollectedAt:                 nowForged.Add(-100 * time.Millisecond),
			PublishedAt:                 nowForged.Add(-50 * time.Millisecond),
			oldestObservationStartAt:    nowForged.Add(-300 * time.Millisecond),
			newestCollectedControllerAt: nowForged.Add(-100 * time.Millisecond),
		},
	}
	assignTelemetryPublicationLocked(testForged, liveForged)
	pubForged := liveForged.Telemetry.Publication.clone()
	serverForged.runner.mu.Unlock()

	reqBodyForged, _ := json.Marshal(map[string]any{
		"client_id":           "tab-forged",
		"client_send_mono_ms": 2000.0,
		"samples": []map[string]any{
			{
				"receive_to_render_ms": 14.0,
				"state":                "visible",
				"observed_at":          nowForged.Format(time.RFC3339Nano),
				"delivery_signal":      "verified_frame_presentation",
				"presentation_proof":   "unregistered-forged-proof",
				"publication": map[string]any{
					"epoch":    pubForged.Epoch,
					"sequence": pubForged.Sequence,
				},
			},
		},
	})
	respForged := postClientRender(serverForged, testForged.ID, string(reqBodyForged))
	if respForged.Code != http.StatusAccepted {
		t.Fatalf("forged proof post status = %d, body = %s", respForged.Code, respForged.Body.String())
	}
	serverForged.runner.mu.Lock()
	stagesForged := stageRecorderLocked(testForged).snapshot()
	serverForged.runner.mu.Unlock()
	for _, st := range stagesForged {
		if st.Stage == telemetryStageBrowserRender || st.Stage == telemetryStageEndToEnd {
			if st.DeliverySignal != DeliverySignalPostCommitPaintOpportunity || !st.Advisory {
				t.Fatalf("stage %s accepted unregistered presentation proof: signal=%q advisory=%v", st.Stage, st.DeliverySignal, st.Advisory)
			}
		}
	}
}

func TestTelemetryCheckpointDurableRenderPopulationFailover(t *testing.T) {
	now := time.Date(2026, 10, 28, 13, 0, 0, 0, time.UTC)
	test := &ManagedTest{
		ID:        "checkpoint-render-pop",
		Status:    "RUNNING",
		StartedAt: &now,
		initialArchiveProvenance: &RunArchiveProvenance{
			RunIncarnation: "incarnation-1",
		},
	}

	// Epoch 1 on initial leader: publish 3 eligible publications, lock reference client on seq 1 & 2, leave seq 3 unreported.
	for i := 1; i <= 3; i++ {
		sampleTime := now.Add(time.Duration(i) * time.Second)
		live := &LiveMetrics{
			Timestamp: sampleTime,
			Telemetry: &TelemetryHealth{
				CollectedAt:                 sampleTime.Add(-100 * time.Millisecond),
				PublishedAt:                 sampleTime,
				oldestObservationStartAt:    sampleTime.Add(-500 * time.Millisecond),
				newestCollectedControllerAt: sampleTime.Add(-100 * time.Millisecond),
			},
		}
		assignTelemetryPublicationLocked(test, live)
	}
	epoch1 := test.telemetryStages.render.epoch
	recvMono := 1005.0
	test.telemetryStages.recordBrowserCalibration("viewer-a\x00tab-1", 1000, &recvMono, now, now.Add(1*time.Millisecond))
	test.telemetryStages.renderOutcomes("viewer-a\x00tab-1", []clientRenderOutcome{
		{publication: clientRenderPublication{Epoch: epoch1, Sequence: 1}, outcome: telemetryRenderRendered},
		{publication: clientRenderPublication{Epoch: epoch1, Sequence: 2}, outcome: "superseded"},
	})
	test.telemetryStages.observeReferencePublicationEndToEnd("viewer-a\x00tab-1", clientRenderSample{
		ReceiveToRenderMs: 20,
		State:             "visible",
		ObservedAt:        now.Add(1100 * time.Millisecond),
		Publication:       &clientRenderPublication{Epoch: epoch1, Sequence: 1},
	}, now.Add(1100*time.Millisecond))

	cp := captureTelemetryCheckpointLocked(test)
	rawCheckpointBytes, err := json.Marshal(cp)
	if err != nil || len(rawCheckpointBytes) == 0 {
		t.Fatalf("marshal telemetryCheckpoint failed: %v", err)
	}

	// Restore onto a successor controller after HA failover.
	successor := &ManagedTest{
		ID:        test.ID,
		Status:    "RUNNING",
		StartedAt: &now,
		initialArchiveProvenance: &RunArchiveProvenance{
			RunIncarnation: "incarnation-1",
		},
	}
	identity := ManagedTest{
		ID:                       successor.ID,
		initialArchiveProvenance: successor.initialArchiveProvenance,
	}
	ledger, totals, err := decodeTelemetryCheckpoint(string(rawCheckpointBytes), 1, &identity, 0)
	if err != nil {
		t.Fatalf("decodeTelemetryCheckpoint failed: %v", err)
	}
	successor.telemetrySources = ledger
	successor.runTotals = totals
	successor.telemetryCheckpointRestored = true
	if ledger != nil && ledger.restoredRenderPopulation != nil {
		restoreDurableRenderPopulation(stageRecorderLocked(successor), ledger.restoredRenderPopulation)
	}
	stageRecorderLocked(successor).adopted(now.Add(3500 * time.Millisecond))
	restoredPop := stageRecorderLocked(successor).renderPopulation()
	if restoredPop == nil || restoredPop.Epoch != epoch1 || restoredPop.PublishedEligible != 3 || restoredPop.Rendered != 1 || restoredPop.Superseded != 1 || restoredPop.Unreported != 1 {
		t.Fatalf("unexpected restored prior epoch accounting before new publication: %+v", restoredPop)
	}

	// Publish 1 new eligible publication in the successor's new epoch and verify cumulative totals.
	liveSuccessor := &LiveMetrics{
		Timestamp: now.Add(4 * time.Second),
		Telemetry: &TelemetryHealth{
			CollectedAt:              now.Add(3900 * time.Millisecond),
			PublishedAt:              now.Add(4 * time.Second),
			oldestObservationStartAt: now.Add(3500 * time.Millisecond),
		},
	}
	assignTelemetryPublicationLocked(successor, liveSuccessor)
	combinedPop := stageRecorderLocked(successor).renderPopulation()
	if combinedPop.PublishedEligible != 1 || len(combinedPop.PriorEpochs) != 1 || combinedPop.PriorEpochs[0].PublishedEligible != 3 || combinedPop.PriorEpochs[0].Unreported != 1 {
		t.Fatalf("expected active epoch PublishedEligible=1 and prior epoch PublishedEligible=3/Unreported=1, got %+v", combinedPop)
	}
	stages := stageRecorderLocked(successor).snapshot()
	var e2e *TelemetryStageDistribution
	for i := range stages {
		if stages[i].Stage == telemetryStageEndToEnd {
			e2e = &stages[i]
			break
		}
	}
	if e2e == nil || e2e.EligiblePublications != 4 || e2e.Samples != 1 || e2e.Excluded["superseded"] != 1 || e2e.Excluded["unreported"] != 2 {
		t.Fatalf("expected cumulative end_to_end stage across epochs (EligiblePublications=4, Samples=1, Superseded=1, Unreported=2), got %+v", e2e)
	}

	// Verify telemetryStageLagCriterionWithPopulation includes PriorEpochs in the end_to_end denominator
	// even when stage.EligiblePublications == 0 (e.g. legacy stage snapshot with durable renderPopulation).
	legacyStage := *e2e
	legacyStage.EligiblePublications = 0
	legacyStage.Excluded = nil
	critPrior := telemetryStageLagCriterionWithPopulation(
		TelemetryLagPercentileRule{Stage: "end_to_end", Percentile: "p99", MaxMs: 2000},
		[]TelemetryStageDistribution{legacyStage},
		nil,
		nil,
		combinedPop,
	)
	if critPrior.Denominator == nil || *critPrior.Denominator != 4 {
		t.Fatalf("expected end_to_end criterion denominator to sum PriorEpochs (4), got %+v", critPrior.Denominator)
	}
	if critPrior.Outcome == TelemetryAcceptancePass {
		t.Fatalf("expected end_to_end p99 criterion to refuse passing when 3 of 4 multi-epoch eligible publications are unreported/superseded, got %+v", critPrior)
	}
}

func TestTelemetrySourceWindowCoverageV4AndPolicySegments(t *testing.T) {
	start := time.Date(2026, 10, 28, 14, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Second)
	seg1Policy := testRoadmapBudgetReceipt(start, 2000)
	seg2Boundary := start.Add(5 * time.Second)
	seg2Policy := testRoadmapBudgetReceipt(seg2Boundary, 1000)

	test := &ManagedTest{
		ID:        "coverage-v4",
		Status:    "COMPLETE",
		StartedAt: &start,
		EndedAt:   &end,
		initialArchiveProvenance: &RunArchiveProvenance{
			TelemetryPolicy: seg1Policy,
		},
		telemetryPolicySegments: []TelemetryPolicySegmentReceipt{
			{
				Sequence:    2,
				EffectiveAt: seg2Boundary,
				Source:      telemetrySegmentSourceRunSelection,
				PolicyID:    seg2Policy.PolicyID,
				Policy:      seg2Policy,
			},
		},
	}

	ledger := newTelemetrySourceLedger()
	ledger.assign("gen-a:50051", start)
	test.telemetrySources = ledger

	// Window 1: [1000ms, 3000ms] with offset=0, uncertainty=50ms -> [950ms, 3050ms], wholly inside segment 1.
	b1 := generatorrpc.MetricsBatch{
		GeneratorID:   "gen-a",
		SourceEpoch:   "epoch-a",
		BatchSequence: 1,
		WindowMs:      2000,
		Telemetry: &generatorrpc.MetricsTelemetry{
			CollectionSequence: 1,
			CollectedAtUnixMs:  start.Add(3 * time.Second).UnixMilli(),
			WindowMs:           2000,
			CollectedAtQuality: &generatorrpc.ClockQuality{Qualified: true, UncertaintyMs: 50},
		},
	}
	ledger.observe("gen-a:50051", b1, start.Add(3*time.Second))
	retainTelemetryCollectionLocked(test, "gen-a:50051", b1)

	// Window 2: [4900ms, 5100ms] with offset=0, uncertainty=50ms -> [4850ms, 5150ms], straddles seg2Boundary (5000ms).
	b2 := generatorrpc.MetricsBatch{
		GeneratorID:   "gen-a",
		SourceEpoch:   "epoch-a",
		BatchSequence: 2,
		WindowMs:      200,
		Telemetry: &generatorrpc.MetricsTelemetry{
			CollectionSequence: 2,
			CollectedAtUnixMs:  start.Add(5100 * time.Millisecond).UnixMilli(),
			WindowMs:           200,
			CollectedAtQuality: &generatorrpc.ClockQuality{Qualified: true, UncertaintyMs: 50},
		},
	}
	ledger.observe("gen-a:50051", b2, start.Add(5100*time.Millisecond))
	retainTelemetryCollectionLocked(test, "gen-a:50051", b2)

	// Window 3: [6000ms, 8000ms] with unqualified clock -> unqualified_clock.
	b3 := generatorrpc.MetricsBatch{
		GeneratorID:   "gen-a",
		SourceEpoch:   "epoch-a",
		BatchSequence: 3,
		WindowMs:      2000,
		Telemetry: &generatorrpc.MetricsTelemetry{
			CollectionSequence: 3,
			CollectedAtUnixMs:  start.Add(8 * time.Second).UnixMilli(),
			WindowMs:           2000,
			CollectedAtQuality: &generatorrpc.ClockQuality{Qualified: false, Reason: clockReasonUncalibrated},
		},
	}
	ledger.observe("gen-a:50051", b3, start.Add(8*time.Second))
	retainTelemetryCollectionLocked(test, "gen-a:50051", b3)

	// Window 4: [8500ms, 9500ms] with offset=0, uncertainty=20ms -> [8480ms, 9520ms], wholly inside segment 2.
	b4 := generatorrpc.MetricsBatch{
		GeneratorID:   "gen-a",
		SourceEpoch:   "epoch-a",
		BatchSequence: 4,
		WindowMs:      1000,
		Telemetry: &generatorrpc.MetricsTelemetry{
			CollectionSequence: 4,
			CollectedAtUnixMs:  start.Add(9500 * time.Millisecond).UnixMilli(),
			WindowMs:           1000,
			CollectedAtQuality: &generatorrpc.ClockQuality{Qualified: true, UncertaintyMs: 20},
		},
	}
	ledger.observe("gen-a:50051", b4, start.Add(9500*time.Millisecond))
	retainTelemetryCollectionLocked(test, "gen-a:50051", b4)

	if len(ledger.collections) != 4 {
		t.Fatalf("expected 4 retained collections, got %d", len(ledger.collections))
	}
	if c1 := ledger.collections[0]; c1.Classification != telemetryCollectionClassificationQualifiedSourceWindows || c1.PolicySegment != 1 || c1.PolicyHash != seg1Policy.Hash {
		t.Fatalf("collection 1 classification = (%q, seg=%d, hash=%q), want (%q, 1, %q)", c1.Classification, c1.PolicySegment, c1.PolicyHash, telemetryCollectionClassificationQualifiedSourceWindows, seg1Policy.Hash)
	}
	if c2 := ledger.collections[1]; c2.Classification != telemetryCollectionClassificationUnscoredBoundarySpanning || c2.PolicySegment != 0 {
		t.Fatalf("collection 2 classification = (%q, seg=%d), want (%q, 0)", c2.Classification, c2.PolicySegment, telemetryCollectionClassificationUnscoredBoundarySpanning)
	}
	if c3 := ledger.collections[2]; c3.Classification != telemetryCollectionClassificationUnqualifiedClock || c3.PolicySegment != 0 {
		t.Fatalf("collection 3 classification = (%q, seg=%d), want (%q, 0)", c3.Classification, c3.PolicySegment, telemetryCollectionClassificationUnqualifiedClock)
	}
	if c4 := ledger.collections[3]; c4.Classification != telemetryCollectionClassificationQualifiedSourceWindows || c4.PolicySegment != 2 || c4.PolicyHash != seg2Policy.Hash {
		t.Fatalf("collection 4 classification = (%q, seg=%d, hash=%q), want (%q, 2, %q)", c4.Classification, c4.PolicySegment, c4.PolicyHash, telemetryCollectionClassificationQualifiedSourceWindows, seg2Policy.Hash)
	}

	runner := &TestRunner{}
	manifest := &RunArchiveManifest{
		TestID: test.ID,
	}
	record := &storage.RunArchiveRecord{TestID: test.ID, ArchivePath: t.TempDir()}
	if err := runner.persistTelemetryCoverage(context.Background(), test, record, manifest); err != nil {
		t.Fatalf("persistTelemetryCoverage failed: %v", err)
	}
	if manifest.TelemetryCoverage == nil {
		t.Fatalf("expected TelemetryCoverage on manifest")
	}
	if manifest.TelemetryCoverage.Algorithm != "telemetry_coverage.v4" || manifest.TelemetryCoverage.CoverageRule != telemetryCollectionClassificationQualifiedSourceWindows {
		t.Fatalf("unexpected coverage algorithm/rule: %+v", manifest.TelemetryCoverage)
	}
	cov := manifest.TelemetryCoverage
	if cov.QualifiedCollections != 2 || cov.UnscoredBoundaryCollections != 1 || cov.UnqualifiedClockCollections != 1 {
		t.Fatalf("unexpected collection classification counts in coverage: %+v", cov)
	}
	if len(manifest.TelemetryCoverage.Segments) != 2 || manifest.TelemetryCoverage.Segments[0].QualifiedCollections != 1 || manifest.TelemetryCoverage.Segments[1].QualifiedCollections != 1 {
		t.Fatalf("unexpected per-segment qualified collection counts: %+v", manifest.TelemetryCoverage.Segments)
	}

	// Verify checkpoint encode + decode round-trip with fallback windows synthesized by MetricsCollectionWindowFromTelemetry.
	test.initialArchiveProvenance.RunIncarnation = "coverage-v4-incarnation"
	cp := captureTelemetryCheckpointLocked(test)
	rawCP, err := json.Marshal(cp)
	if err != nil {
		t.Fatalf("marshal checkpoint with fallback windows: %v", err)
	}
	identity := ManagedTest{ID: test.ID, initialArchiveProvenance: test.initialArchiveProvenance}
	restoredLedger, _, err := decodeTelemetryCheckpoint(string(rawCP), cp.Revision, &identity, 0)
	if err != nil {
		t.Fatalf("decodeTelemetryCheckpoint with fallback windows failed: %v", err)
	}
	if len(restoredLedger.collections) != 4 || restoredLedger.collections[0].Windows[0].Group != "default" {
		t.Fatalf("unexpected restored collections from fallback windows: %+v", restoredLedger.collections)
	}
}

func TestTelemetryEvidenceArchiveSidecarAndLiveSegmentViews(t *testing.T) {
	start := time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)
	end := start.Add(6 * time.Second)
	policy := testRoadmapBudgetReceipt(start, 5000) // relaxed 5,000 ms sample_age stage budget

	test := &ManagedTest{
		ID:        "evidence-sidecar-run",
		Name:      "Evidence sidecar run",
		Status:    "RUNNING",
		StartedAt: &start,
		EndedAt:   &end,
		initialArchiveProvenance: &RunArchiveProvenance{
			TelemetryPolicy: policy,
		},
	}

	// Verify all 6 interval condition labels: healthy, degraded_stage_budget,
	// clock_unqualified, boundary_unscored, source_gap, overload_shed.
	p1 := &LiveMetrics{
		Timestamp:           start.Add(1 * time.Second),
		SampleWindowSeconds: 1,
		ActiveVUsers:        10,
		TelemetryPolicy:     &TelemetryPolicyEvidence{Availability: "available", Policy: policy},
		TelemetryStatus:     &TelemetryRunHealth{Status: "healthy", Severity: "healthy", PolicySegment: 1},
		Telemetry:           &TelemetryHealth{SampleAgeMs: 100},
		Resources:           &engine.SystemResources{CPUProcessPct: 12.5, MemProcessMb: 64},
	}
	p2 := &LiveMetrics{
		Timestamp:           start.Add(2 * time.Second),
		SampleWindowSeconds: 1,
		ActiveVUsers:        20,
		TelemetryStatus: &TelemetryRunHealth{
			Status:     "degraded",
			Severity:   "warning",
			Conditions: []string{"generator_lag", "generator_stream_gaps"},
			Unscored:   []string{"stale_sample:uncalibrated", telemetryPolicyBoundaryUnscored},
		},
		Telemetry: &TelemetryHealth{
			DroppedResults:   2,
			SheddedArrivals:  5,
			SampleAgeQuality: &generatorrpc.ClockQuality{Qualified: false, Reason: clockReasonUncalibrated},
		},
		GeneratorResources: map[string]*GeneratorResourceSnapshot{
			"gen-1": {
				GeneratorID:  "gen-1",
				Target:       "gen-1:50051",
				ActiveVUsers: 20,
				Resources:    &engine.SystemResources{CPUProcessPct: 45.0, MemProcessMb: 128, NetSentKbps: 512, NetRecvKbps: 256},
				Timestamp:    start.Add(2 * time.Second),
			},
		},
	}
	test.History = []*LiveMetrics{p1, p2}
	test.LiveMetrics = p1
	recordTelemetryResourceSamplesLocked(test, p1, p1.Timestamp)
	recordTelemetryResourceSamplesLocked(test, p2, p2.Timestamp)

	// Check live telemetry view enrichment (PolicySegments, StageBudgets, IntervalConditionLabels).
	liveClone := cloneRunLiveMetrics(test)
	if liveClone == nil || liveClone.Telemetry == nil {
		t.Fatalf("expected liveClone.Telemetry")
	}
	if len(liveClone.Telemetry.PolicySegments) != 1 || len(liveClone.Telemetry.StageBudgets) == 0 {
		t.Fatalf("expected PolicySegments and StageBudgets on live telemetry view, got segments=%d budgets=%d", len(liveClone.Telemetry.PolicySegments), len(liveClone.Telemetry.StageBudgets))
	}
	if len(liveClone.Telemetry.IntervalConditionLabels) != 1 || liveClone.Telemetry.IntervalConditionLabels[0] != TelemetryConditionHealthy {
		t.Fatalf("expected healthy interval condition label on p1, got %+v", liveClone.Telemetry.IntervalConditionLabels)
	}
	p2Labels := deriveTelemetryIntervalConditionLabels(p2, p2.TelemetryStatus)
	wantP2 := []string{
		TelemetryConditionDegradedStageBudget,
		TelemetryConditionClockUnqualified,
		TelemetryConditionBoundaryUnscored,
		TelemetryConditionSourceGap,
		TelemetryConditionOverloadShed,
	}
	if len(p2Labels) != len(wantP2) {
		t.Fatalf("p2 condition labels = %+v, want %+v", p2Labels, wantP2)
	}
	for i := range wantP2 {
		if p2Labels[i] != wantP2[i] {
			t.Fatalf("p2 condition labels[%d] = %q, want %q", i, p2Labels[i], wantP2[i])
		}
	}

	// Stage budget health is healthy (100% healthy coverage under 5,000 ms budget),
	// while end-to-end sample age P99 is 2,450 ms (> 2,000 ms independent target).
	p99 := 2450.0
	archiveDir := t.TempDir()
	manifest := &RunArchiveManifest{
		SchemaVersion: archiveSchemaVersion,
		TestID:        test.ID,
		TestName:      test.Name,
		TelemetryCoverage: &TelemetryCoverageRecord{
			Algorithm:            telemetryCoverageAlgorithm,
			CoverageRule:         telemetryCollectionClassificationQualifiedSourceWindows,
			HealthyMs:            6000,
			ClassifiedMs:         6000,
			QualifiedCollections: 2,
			Coverage: TelemetryCoverage{
				WindowMs:   6000,
				ObservedMs: 6000,
			},
			Segments: []TelemetryPolicySegment{
				{
					Sequence:             1,
					Start:                start,
					End:                  end,
					PolicyHash:           policy.Hash,
					HealthyMs:            6000,
					QualifiedCollections: 2,
					SourceWindowMs:       6000,
				},
			},
		},
		TelemetryStageDistributions: []TelemetryStageDistribution{
			{
				Stage:                telemetryStageEndToEnd,
				Unit:                 "ms",
				Availability:         "measured",
				Samples:              100,
				EligiblePublications: 100,
				P99Ms:                &p99,
				DeliverySignal:       DeliverySignalVerifiedFramePresentation,
			},
		},
	}
	record := &storage.RunArchiveRecord{
		TestID:       test.ID,
		ArchivePath:  archiveDir,
		ManifestPath: filepath.Join(archiveDir, "manifest.json"),
	}
	runner := &TestRunner{}
	if err := runner.persistTelemetryEvidenceArchive(context.Background(), test, record, manifest, nil); err != nil {
		t.Fatalf("persistTelemetryEvidenceArchive failed: %v", err)
	}
	if manifest.TelemetryEvidenceFile != telemetryEvidenceFileName {
		t.Fatalf("manifest.TelemetryEvidenceFile = %q, want %q", manifest.TelemetryEvidenceFile, telemetryEvidenceFileName)
	}
	if err := validateImmutableArchiveArtifactIntegrity(archiveDir, manifest); err != nil {
		t.Fatalf("validateImmutableArchiveArtifactIntegrity failed: %v", err)
	}

	rawSidecar, err := os.ReadFile(filepath.Join(archiveDir, telemetryEvidenceFileName))
	if err != nil {
		t.Fatalf("read %s: %v", telemetryEvidenceFileName, err)
	}
	var doc TelemetryEvidenceDocument
	if err := json.Unmarshal(rawSidecar, &doc); err != nil {
		t.Fatalf("unmarshal %s: %v", telemetryEvidenceFileName, err)
	}
	if doc.SchemaVersion != telemetryEvidenceSchema || doc.CoverageAlgorithm != "telemetry_coverage.v4" {
		t.Fatalf("unexpected schema/algorithm in telemetry_evidence.json: %+v", doc)
	}
	if len(doc.SegmentCoverage) != 1 || len(doc.Intervals) != 2 || len(doc.ResourceSamples) < 3 {
		t.Fatalf("unexpected intervals/segments/resources in telemetry_evidence.json: segments=%d intervals=%d resources=%d", len(doc.SegmentCoverage), len(doc.Intervals), len(doc.ResourceSamples))
	}
	if len(doc.ValidityFindings) != 2 {
		t.Fatalf("expected 2 validity findings, got %+v", doc.ValidityFindings)
	}
	var stageHealthFinding, e2eAgeFinding *TelemetryValidityFinding
	for i := range doc.ValidityFindings {
		switch doc.ValidityFindings[i].Criterion {
		case "stage_budget_health":
			stageHealthFinding = &doc.ValidityFindings[i]
		case "end_to_end_sample_age":
			e2eAgeFinding = &doc.ValidityFindings[i]
		}
	}
	if stageHealthFinding == nil || !stageHealthFinding.Compliant || stageHealthFinding.Status != "passed" {
		t.Fatalf("expected stage_budget_health finding to be passed/compliant, got %+v", stageHealthFinding)
	}
	if e2eAgeFinding == nil || e2eAgeFinding.Compliant || e2eAgeFinding.Status != "failed" || e2eAgeFinding.ThresholdMs != 2000 {
		t.Fatalf("expected independent <= 2,000 ms end_to_end_sample_age finding to fail when P99=2450ms, got %+v", e2eAgeFinding)
	}

	// Verify retry idempotency: a second persistTelemetryEvidenceArchive call when manifest.TelemetryEvidenceFile is already set
	// is a no-op and preserves the immutable archive integrity digest.
	if err := runner.persistTelemetryEvidenceArchive(context.Background(), test, record, manifest, nil); err != nil {
		t.Fatalf("second persistTelemetryEvidenceArchive failed: %v", err)
	}
	if err := validateImmutableArchiveArtifactIntegrity(archiveDir, manifest); err != nil {
		t.Fatalf("validateImmutableArchiveArtifactIntegrity after retry failed: %v", err)
	}
}
