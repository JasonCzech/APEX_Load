package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
	"gocloud.dev/blob/memblob"
)

// TestRoadmapIntegratedNonMessagingReleaseScenario chains the completed
// non-Messaging roadmap tracks (AUTH, PROTO, TEL, NA, BE/BI, and REC) into a
// deterministic end-to-end release scenario (Wave 4 / INT-1).
func TestRoadmapIntegratedNonMessagingReleaseScenario(t *testing.T) {
	// Phase 1: Authoring, Import Persistence Parity, Key Rotation & Validation Context (AUTH-A1, AUTH-B1, AUTH-C1).
	t.Run("Phase1_AuthoringImportValidationAndReceiptRotation", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "int1-auth.db")
		store, err := storage.NewSQLiteStore(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })

		wrapperV1, err := keywrap.NewLocalAESKeyWrapper(keywrap.LocalKeyOptions{Key: []byte("0123456789abcdef0123456789abcdef"), KeyID: "int1-master-v1"})
		if err != nil {
			t.Fatal(err)
		}
		wrapperV2, err := keywrap.NewLocalAESKeyWrapper(keywrap.LocalKeyOptions{Key: []byte("fedcba9876543210fedcba9876543210"), KeyID: "int1-master-v2"})
		if err != nil {
			t.Fatal(err)
		}
		sharedSecrets := &runtimeProjectionSecretStore{values: map[string]string{}, attemptStore: store}
		server := NewServer(ServerConfig{
			Store:                store,
			ProjectSecretStore:   sharedSecrets,
			BrunoArtifactWrapper: wrapperV1,
			MaxConcurrent:        8,
		})
		t.Cleanup(func() { shutdownAuthoringFidelityServer(t, server) })

		// 1a. Contract v2 receipt create + replay after edit + key rotation (AUTH-A1).
		scriptBody := idempotentScriptBody("INT-1 Authored Checkout")
		createKey := "int1-script-create-key-01"
		first := receiptCreateRequest(t, server, "/api/v1/scripts", scriptBody, createKey, "2")
		if first.Code != http.StatusCreated || first.Header().Get(authoringReceiptVersionHeader) != "2" {
			t.Fatalf("Phase 1 script v2 create = %d: %s", first.Code, first.Body.String())
		}
		var created StoredScript
		if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		_, tag := requireAuthoringAPIProjection(t, server, created.ID)
		edited := authoringAPIScript(created.ID)
		edited.Name = "INT-1 Authored Checkout v2"
		if upd := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/"+created.ID, map[string]any{"script": edited}, tag); upd.Code != http.StatusOK {
			t.Fatalf("Phase 1 script edit = %d: %s", upd.Code, upd.Body.String())
		}
		replay := receiptCreateRequest(t, server, "/api/v1/scripts", scriptBody, createKey, "2")
		if replay.Code != http.StatusCreated || replay.Header().Get("Idempotent-Replayed") != "true" {
			t.Fatalf("Phase 1 script v2 replay = %d: %s", replay.Code, replay.Body.String())
		}
		outcome := decodeReceiptOutcome(t, replay)
		if outcome.ReceiptVersion != 2 || outcome.ContentRevision != created.ContentRevision {
			t.Fatalf("Phase 1 v2 receipt outcome mismatch: %+v", outcome)
		}

		scenarioKey := "int1-scenario-key-01"
		scenarioReq := CreateScenarioRequest{
			Name:     "INT-1 Credential Scenario",
			Duration: "30s",
			RuntimeSettings: &RuntimeSettingsOverride{
				Env: map[string]string{"API_TOKEN": "int1-secret-token-before-rotation"},
			},
			Groups: []CreateGroupEntry{{Name: "CheckoutGroup", ScriptID: created.ID, VUsers: 2}},
		}
		if createdScen := receiptCreateRequest(t, server, "/api/v1/scenarios", scenarioReq, scenarioKey, "2"); createdScen.Code != http.StatusCreated {
			t.Fatalf("Phase 1 scenario create = %d: %s", createdScen.Code, createdScen.Body.String())
		}
		if err := server.RotateAuthoringReceiptCredentialKey(t.Context(), wrapperV2); err != nil {
			t.Fatalf("Phase 1 RotateAuthoringReceiptCredentialKey: %v", err)
		}
		if replayRotated := receiptCreateRequest(t, server, "/api/v1/scenarios", scenarioReq, scenarioKey, "2"); replayRotated.Code != http.StatusCreated || replayRotated.Header().Get("Idempotent-Replayed") != "true" {
			t.Fatalf("Phase 1 scenario replay after key rotation = %d: %s", replayRotated.Code, replayRotated.Body.String())
		}

		// 1b. ScenarioValidationContext composite digest tracking (AUTH-B1).
		vctxServer, _, projectSecrets := validationContextFixture(t)
		vctxScript := secretReferencingScript()
		vctxScript.ID = "int1-vctx-script"
		vctxScript.Name = "INT-1 Validation Context Script"
		storedVctxScript, err := vctxServer.runner.CreateScriptAuthoringDurable(vctxScript)
		if err != nil {
			t.Fatal(err)
		}
		vctxScenario := CreateScenarioRequest{
			Name:     "INT-1 Validated Scenario",
			Duration: "30s",
			Groups:   []CreateGroupEntry{{Name: "MainGroup", ScriptID: storedVctxScript.ID, VUsers: 2}},
		}
		initialReport, code := validateScenarioWithContext(t, vctxServer, vctxScenario, false)
		if code != http.StatusOK || initialReport == nil || initialReport.ValidationContext == nil {
			t.Fatalf("Phase 1 initial scenario validation = %d, report = %+v", code, initialReport)
		}
		projectSecrets.mu.Lock()
		projectSecrets.values[storage.DefaultProjectID+"/api_token"] = "secret-v1"
		projectSecrets.mu.Unlock()
		afterSecretProvision, code := validateScenarioWithContext(t, vctxServer, vctxScenario, true)
		if code != http.StatusOK || afterSecretProvision.ValidationContext == nil ||
			afterSecretProvision.ValidationContext.Components.SecretReferences == initialReport.ValidationContext.Components.SecretReferences {
			t.Fatalf("Phase 1 expected secret_references digest to advance after secret provisioning")
		}
		projectSecrets.mu.Lock()
		projectSecrets.values[storage.DefaultProjectID+"/api_token"] = "secret-v2-rotated"
		projectSecrets.mu.Unlock()
		afterSecretRotate, code := validateScenarioWithContext(t, vctxServer, vctxScenario, true)
		if code != http.StatusOK || afterSecretRotate.ValidationContext.Revision != afterSecretProvision.ValidationContext.Revision {
			t.Fatalf("Phase 1 rotating secret value must not invalidate validation_context revision")
		}

		// 1c. Real-HTTP session import save & persisted-copy parity (AUTH-C1).
		state := runAuthoringFidelitySessionImportAndLifecycleEdges(t, server, store)
		assertAuthoringFidelitySessionImportReopen(t, server, state)
	})

	// Phase 2 & 3 & 4: Multi-Cohort Execution (HTTP, Browser, gRPC, WebSocket, Java HTTP),
	// Calibrated Telemetry & Policy Segments, Native Byte Queries, Saved PanelInputs v2, and 10-Consumer Scrubbed Parity.
	t.Run("Phase2To4_MultiCohortProtocolsTelemetryByteQueriesAndConsumerParity", func(t *testing.T) {
		server, project, candidateRecord, _ := browserPanelHTTPFixture(t)
		baseRecord, baseStart := nativeSeriesFixture(t)
		baseRecord.TestID = "int1-run-baseline"

		// Populate multi-protocol transactions (HTTP, gRPC, WebSocket, Java HTTP) on both baseline and candidate archives.
		writeMultiProtocolTransactions := func(t *testing.T, rec *storage.RunArchiveRecord, start time.Time, scale int64) {
			t.Helper()
			decBytes, prepBytes := int64(64*scale), int64(64*scale)
			shallowBytes, graphBytes, serBytes := int64(128), int64(512), int64(256)
			decMs, encMs, callMs, rttMs, serMs, deserMs := 1.1, 0.9, float64(12*scale), float64(3*scale), 0.4, 0.5
			statusOK := int32(0)

			events := []TransactionEvent{
				{
					Timestamp:      start.Add(2 * time.Second),
					ActionName:     "http_checkout",
					Population:     "action",
					DependencyType: "http",
					DurationMs:     float64(15 * scale),
					BytesSent:      200 * scale,
					BytesReceived:  1200 * scale,
					Success:        true,
					StatusCode:     http.StatusOK,
					ByteEvidence:   protocol.ByteEvidenceHTTPBody,
				},
				{
					Timestamp:      start.Add(4 * time.Second),
					ActionName:     "grpc_order_stream",
					Population:     "action",
					DependencyType: "grpc",
					DurationMs:     callMs,
					BytesSent:      320 * scale,
					BytesReceived:  640 * scale,
					Success:        true,
					ByteEvidence:   protocol.ByteEvidenceGRPCUncompressedPayload,
					ServerTelemetry: &protocol.ServerTelemetry{
						GRPC: &protocol.GRPCDiagnostics{
							Method:           "/orders.v1.OrderService/StreamOrders?token=GRPC_SECRET_SENTINEL",
							Mode:             "bidi_stream",
							CallMs:           callMs,
							EncodeMs:         &encMs,
							DecodeMs:         &decMs,
							StatusCode:       &statusOK,
							MessagesSent:     2,
							MessagesReceived: 2,
							RequestBytes:     320 * scale,
							ResponseBytes:    640 * scale,
						},
					},
				},
				{
					Timestamp:      start.Add(6 * time.Second),
					ActionName:     "ws_order_feed",
					Population:     "action",
					DependencyType: "websocket",
					DurationMs:     float64(8 * scale),
					BytesSent:      150 * scale,
					BytesReceived:  450 * scale,
					Success:        true,
					ByteEvidence:   protocol.ByteEvidenceWebSocketApplicationPayload,
					ServerTelemetry: &protocol.ServerTelemetry{
						WebSocket: &protocol.WebSocketDiagnostics{
							Operation:        "ws_receive",
							MessageType:      "text",
							ReceiveMs:        &callMs,
							PingRTTMs:        []float64{rttMs},
							MessagesSent:     1,
							MessagesReceived: 1,
							BytesSent:        150 * scale,
							BytesReceived:    450 * scale,
							PendingWrites:    1,
						},
					},
				},
				{
					Timestamp:      start.Add(8 * time.Second),
					ActionName:     "java_pricing_rpc",
					Population:     "action",
					DependencyType: "java_http",
					DurationMs:     float64(20 * scale),
					BytesSent:      88 * scale,
					BytesReceived:  176 * scale,
					Success:        true,
					ByteEvidence:   protocol.ByteEvidenceHTTPBody,
					ServerTelemetry: &protocol.ServerTelemetry{
						JavaHTTP: &protocol.JavaHTTPDiagnostics{
							Version:                   1,
							Mode:                      "embedded_opaque_bytes",
							InputState:                "decoded",
							RequestEncodedBytes:       88 * scale,
							RequestDecodedBytes:       &decBytes,
							PreparedRequestBodyBytes:  &prepBytes,
							AppliedRequestCompression: "none",
							ResponseState:             "complete",
							ResponseRetainedBytes:     &decBytes,
							ResponseBase64Bytes:       &prepBytes,
							Base64DecodeMs:            &decMs,
							Base64EncodeMs:            &encMs,
							EnclosingMs:               float64(20 * scale),
							ObjectFootprint:           "contained_jvm_helper_v1",
							ContainedObjectFootprint: &protocol.JavaHTTPObjectFootprintEvidence{
								FixtureKind: "approved_serializable_record",
								JVMVersion:  "21.0.2",
								ShallowObject: protocol.JavaHTTPShallowObjectFootprint{
									Metric:           "java_shallow_object_bytes",
									Method:           protocol.JavaHTTPShallowObjectMethodV1,
									JVMVersion:       "21.0.2",
									Available:        true,
									Bytes:            &shallowBytes,
									Unit:             "bytes",
									RetainedHeapNote: protocol.JavaHTTPObjectFootprintRetainedNote,
								},
								ReachableGraph: protocol.JavaHTTPReachableGraphFootprint{
									Metric:           "java_reachable_graph_bytes",
									Method:           protocol.JavaHTTPReachableGraphMethodV1,
									JVMVersion:       "21.0.2",
									Available:        true,
									Bytes:            &graphBytes,
									Unit:             "bytes",
									UniqueObjects:    5,
									MaxNodes:         4096,
									MaxDepth:         32,
									RetainedHeapNote: protocol.JavaHTTPObjectFootprintRetainedNote,
								},
								CodecExecuted:         true,
								SerializedBytes:       &serBytes,
								JavaSerializationMs:   &serMs,
								JavaDeserializationMs: &deserMs,
							},
						},
					},
				},
			}

			txFile, err := os.Create(filepath.Join(rec.ArchivePath, "transactions.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			for i := range events {
				sanitizeArchivedTransaction(&events[i])
				if err := json.NewEncoder(txFile).Encode(events[i]); err != nil {
					_ = txFile.Close()
					t.Fatal(err)
				}
			}
			if err := txFile.Close(); err != nil {
				t.Fatal(err)
			}
			for _, emptyName := range []string{"resources.jsonl", "interval_summaries.jsonl"} {
				if err := os.WriteFile(filepath.Join(rec.ArchivePath, emptyName), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}

		candStart := baseStart
		writeMultiProtocolTransactions(t, candidateRecord, candStart, 2)
		writeMultiProtocolTransactions(t, baseRecord, baseStart, 1)

		// Enrich candidate manifest with TEL-A1/A2/B1/B2 evidence + sentinel strings to verify scrubbing.
		rawCand, err := os.ReadFile(candidateRecord.ManifestPath)
		if err != nil {
			t.Fatal(err)
		}
		var candManifest RunArchiveManifest
		if err := json.Unmarshal(rawCand, &candManifest); err != nil {
			t.Fatal(err)
		}
		candManifest.TestID = candidateRecord.TestID
		candManifest.TransactionsFile = "transactions.jsonl"
		candManifest.ResourceSamplesFile = "resources.jsonl"
		candManifest.IntervalSummariesFile = "interval_summaries.jsonl"
		candManifest.Provenance.ProjectID = project
		candManifest.Provenance.RuntimeSettings = &RuntimeSettingsOverride{
			RedactionRules: []engine.RedactionRule{{Name: "int1-rule", Pattern: "INT1_RULE_SECRET_[0-9]+", Replacement: "[MASKED_INT1]"}},
		}
		pin := RunPolicyPin{
			Kind:     storage.AnalysisDefinitionKindTelemetryAcceptancePolicy,
			ID:       "int1-tel-gate",
			Name:     "INT-1 Gate password=INT1_CRED_SENTINEL INT1_RULE_SECRET_42",
			Revision: 1,
			SHA256:   "int1-sha",
			Source:   "project_default",
		}
		candManifest.Provenance.PolicyPins = []RunPolicyPin{pin}
		candManifest.Provenance.TelemetryAcceptancePolicy = &TelemetryAcceptancePolicySnapshot{
			Pin: pin,
			Definition: TelemetryAcceptancePolicyDefinition{
				Version:             1,
				Mode:                "fail_ci",
				Description:         "gate desc token=INT1_TOKEN_SENTINEL INT1_RULE_SECRET_42",
				MinWallTimeCoverage: acceptanceRatio(0.95),
			},
		}
		candManifest.TelemetryCoverage = &TelemetryCoverageRecord{
			Algorithm:    telemetryCoverageAlgorithm,
			CoverageRule: "qualified_source_windows",
			Coverage: TelemetryCoverage{
				WindowMs:           20_000,
				ObservedMs:         16_000,
				UnknownMs:          4_000,
				SourceRequirements: true,
				Sources:            []TelemetrySourceCoverage{{Source: "https://user:INT1_URL_PASS@gen-0.internal:7000", ObservedMs: 16_000, RequiredMs: 20_000, UnknownMs: 4_000}},
			},
			HealthyMs:    14_000,
			DegradedMs:   2_000,
			ClassifiedMs: 16_000,
			UnknownMs:    4_000,
			Segments: []TelemetryPolicySegment{
				{Sequence: 1, Start: candStart, End: candStart.Add(10 * time.Second), PolicyHash: "seg1", HealthyMs: 8_000, QualifiedCollections: 8, SourceWindowMs: 8_000},
				{Sequence: 2, Start: candStart.Add(10 * time.Second), End: candStart.Add(20 * time.Second), PolicyHash: "seg2", HealthyMs: 6_000, DegradedMs: 2_000, QualifiedCollections: 8, SourceWindowMs: 8_000},
			},
		}
		segPolicy := testRoadmapBudgetReceipt(candStart, 2000)
		candManifest.TelemetryPolicySegments = []TelemetryPolicySegmentReceipt{
			{Sequence: 1, EffectiveAt: candStart, Source: "admission", PolicyID: "int1-budget-policy", Policy: segPolicy},
			{Sequence: 2, EffectiveAt: candStart.Add(10 * time.Second), Source: "run_selection", PolicyID: "int1-budget-policy", RequestedBy: "operator?api_key=INT1_QUERY_SENTINEL", Policy: segPolicy},
		}

		// Exercise TEL-A1 browser calibration + foreground presentation proof on a stage recorder.
		stageRec := newTelemetryStageRecorder()
		stageRec.since = candStart
		epoch, seq := stageRec.nextPublicationWithTiming(true, telemetryPublicationTiming{
			publishedAt:                 candStart.Add(1900 * time.Millisecond),
			oldestObservationStartAt:    candStart.Add(1500 * time.Millisecond),
			newestCollectedControllerAt: candStart.Add(1800 * time.Millisecond),
			clockQualified:              true,
		})
		stageRec.RegisterPresentationProof(epoch, seq, "proof-seq-1")
		echo := stageRec.recordBrowserCalibration("client-int1", 1000.0, nil, candStart.Add(1800*time.Millisecond), candStart.Add(1802*time.Millisecond))
		stageRec.refineBrowserCalibration("client-int1", echo[0], 1010.0, echo[1], echo[2])
		pubRef := clientRenderPublication{Epoch: epoch, Sequence: seq}
		stageRec.renderOutcomes("client-int1", []clientRenderOutcome{{publication: pubRef, outcome: telemetryRenderRendered}})
		recvMono := 1020.0
		renderMono := 1032.0
		sample := clientRenderSample{
			ReceiveToRenderMs: 12.0,
			ReceivedMonoMs:    &recvMono,
			RenderedMonoMs:    &renderMono,
			State:             "visible",
			ObservedAt:        candStart.Add(2 * time.Second),
			DeliverySignal:    DeliverySignalVerifiedFramePresentation,
			PresentationProof: "proof-seq-1",
			Publication:       &pubRef,
		}
		stageRec.observeBrowserRenderSample(sample)
		stageRec.observeReferencePublicationEndToEnd("client-int1", sample, candStart.Add(2*time.Second))
		candManifest.TelemetryRenderPopulation = stageRec.renderPopulation()
		candManifest.TelemetryStageDistributions = stageRec.snapshot()
		candManifest.TelemetryValidityFindings = buildTelemetryValidityFindings(&candManifest)
		candManifest.TelemetryAcceptance = telemetryAcceptanceForManifest(&candManifest)
		candManifest.TelemetryEvidenceFile = telemetryEvidenceFileName

		evidenceDoc := TelemetryEvidenceDocument{
			SchemaVersion:      telemetryEvidenceSchema,
			TestID:             candManifest.TestID,
			CapturedAt:         candStart.Add(20 * time.Second),
			CoverageAlgorithm:  telemetryCoverageAlgorithm,
			CoverageRule:       "qualified_source_windows",
			Coverage:           candManifest.TelemetryCoverage,
			PolicySegments:     candManifest.TelemetryPolicySegments,
			SegmentCoverage:    candManifest.TelemetryCoverage.Segments,
			RenderPopulation:   candManifest.TelemetryRenderPopulation,
			StageDistributions: candManifest.TelemetryStageDistributions,
			ValidityFindings:   candManifest.TelemetryValidityFindings,
			Acceptance:         candManifest.TelemetryAcceptance,
		}
		evidenceBytes, err := json.MarshalIndent(evidenceDoc, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(candidateRecord.ArchivePath, telemetryEvidenceFileName), evidenceBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		candManifest.Artifacts = nil
		rawCand, err = json.Marshal(candManifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(candidateRecord.ManifestPath, rawCand, 0o600); err != nil {
			t.Fatal(err)
		}
		candidateRecord.ManifestSHA256 = sha256BytesHex(rawCand)

		rawBase, err := os.ReadFile(baseRecord.ManifestPath)
		if err != nil {
			t.Fatal(err)
		}
		var baseManifest RunArchiveManifest
		if err := json.Unmarshal(rawBase, &baseManifest); err != nil {
			t.Fatal(err)
		}
		baseManifest.TestID = baseRecord.TestID
		baseManifest.TransactionsFile = "transactions.jsonl"
		baseManifest.ResourceSamplesFile = "resources.jsonl"
		baseManifest.IntervalSummariesFile = "interval_summaries.jsonl"
		baseManifest.Provenance.ProjectID = project
		rawBase, err = json.Marshal(baseManifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(baseRecord.ManifestPath, rawBase, 0o600); err != nil {
			t.Fatal(err)
		}
		baseRecord.ManifestSHA256 = sha256BytesHex(rawBase)

		server.runner.mu.Lock()
		server.runner.tests[candidateRecord.TestID] = &ManagedTest{
			ID:                       candidateRecord.TestID,
			Name:                     "Candidate Run",
			ProjectID:                project,
			Status:                   "COMPLETE",
			StartedAt:                &candStart,
			RuntimeSettings:          candManifest.Provenance.RuntimeSettings,
			initialArchiveProvenance: &candManifest.Provenance,
			telemetryPolicySegments:  candManifest.TelemetryPolicySegments,
			TelemetryPolicySegments:  candManifest.TelemetryPolicySegments,
			Archive:                  candidateRecord,
		}
		server.runner.archives[candidateRecord.TestID] = candidateRecord
		server.runner.tests[baseRecord.TestID] = &ManagedTest{
			ID:        baseRecord.TestID,
			Name:      "Baseline Run",
			ProjectID: project,
			Status:    "COMPLETE",
			StartedAt: &baseStart,
			Archive:   baseRecord,
		}
		server.runner.archives[baseRecord.TestID] = baseRecord
		server.runner.mu.Unlock()
		if err := server.store.SaveRunArchive(candidateRecord); err != nil {
			t.Fatal(err)
		}
		if err := server.store.SaveTest(&storage.TestRecord{ID: baseRecord.TestID, Name: "Baseline Run", ProjectID: project, Status: "COMPLETE", StartTime: baseStart}); err != nil {
			t.Fatal(err)
		}
		if err := server.store.SaveRunArchive(baseRecord); err != nil {
			t.Fatal(err)
		}
		server.archiveReadCaches().forgetRunArchiveCache(candidateRecord.TestID)
		server.archiveReadCaches().forgetRunArchiveCache(baseRecord.TestID)

		// Phase 3b: Verify admitted native byte queries across HTTP, gRPC, and WebSocket (NA-C1).
		for _, tc := range []struct {
			protocol string
			action   string
			wantSent float64
		}{
			{protocol: "http", action: "http_checkout", wantSent: 400},
			{protocol: "grpc", action: "grpc_order_stream", wantSent: 640},
			{protocol: "websocket", action: "ws_order_feed", wantSent: 300},
		} {
			q := storage.AnalyticsQuery{
				Population:    analysis.PopulationByte,
				Metric:        "bytes_sent",
				Aggregation:   "sum",
				EndSeconds:    20,
				BucketSeconds: 10,
				Filters:       map[string]string{"protocol": tc.protocol, "action": tc.action},
			}
			series, err := buildNativeArchiveSeries(t.Context(), candidateRecord, q)
			if err != nil {
				t.Fatalf("Phase 3 byte query (%s/%s): %v", tc.protocol, tc.action, err)
			}
			if len(series.Buckets) == 0 || series.Buckets[0].Value == nil || *series.Buckets[0].Value != tc.wantSent {
				t.Fatalf("Phase 3 byte query (%s/%s) bucket[0] = %+v, want %v", tc.protocol, tc.action, series.Buckets, tc.wantSent)
			}
		}

		// Phase 4a: Preview comparison query -> freeze PanelInputs v2 -> save DashboardDefinition -> verify SavedAnalysisPanels (NA-C2).
		panelQ := storage.AnalyticsQuery{
			Population:      "byte",
			Metric:          "bytes_sent",
			Aggregation:     "sum",
			ComparisonRunID: baseRecord.TestID,
			EndSeconds:      20,
			BucketSeconds:   10,
			Filters:         map[string]string{"protocol": "http", "action": "http_checkout"},
		}
		previewResp := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/tests/"+candidateRecord.TestID+"/archive/native-series", panelQ)
		if previewResp.Code != http.StatusOK {
			t.Fatalf("Phase 4 preview native-series = %d: %s", previewResp.Code, previewResp.Body.String())
		}
		var preview NativeArchiveResponse
		if err := json.Unmarshal(previewResp.Body.Bytes(), &preview); err != nil {
			t.Fatal(err)
		}
		if preview.PanelInputs == nil || preview.PanelInputs.Version != 2 ||
			preview.PanelInputs.Current.ManifestSHA256 != candidateRecord.ManifestSHA256 ||
			preview.PanelInputs.Baseline == nil || preview.PanelInputs.Baseline.ManifestSHA256 != baseRecord.ManifestSHA256 {
			t.Fatalf("Phase 4 unexpected PanelInputs v2: %+v", preview.PanelInputs)
		}
		panelQ.PanelInputs = preview.PanelInputs
		def := storage.DashboardDefinition{
			ProjectID:  project,
			Name:       "INT-1 Pinned Byte Comparison Dashboard",
			Visibility: "project",
			Tabs: []storage.DashboardTab{{
				ID:    "main",
				Title: "Main",
				Panels: []storage.DashboardPanel{{
					ID:    "p-http-bytes",
					Title: "HTTP Bytes Sent",
					Kind:  "line",
					W:     12,
					H:     8,
					Query: &panelQ,
				}},
			}},
		}
		saveDefResp := dashboardAPIRequest(t, server, http.MethodPost, "/api/v1/monitoring/dashboard-definitions", def)
		if saveDefResp.Code != http.StatusCreated {
			t.Fatalf("Phase 4 save dashboard definition = %d: %s", saveDefResp.Code, saveDefResp.Body.String())
		}

		// Phase 4b: Verify consumer surfaces, SavedAnalysisPanels="ready", and zero sentinel leakage (TEL-B2).
		int1Sentinels := []string{
			"GRPC_SECRET_SENTINEL",
			"INT1_RULE_SECRET_42",
			"INT1_CRED_SENTINEL",
			"INT1_TOKEN_SENTINEL",
			"INT1_URL_PASS",
			"INT1_QUERY_SENTINEL",
		}
		for _, route := range []string{
			"/api/v1/tests/" + candidateRecord.TestID,
			"/api/v1/tests/" + candidateRecord.TestID + "/metrics",
			"/api/v1/tests/" + candidateRecord.TestID + "/archive/manifest",
			"/api/v1/tests/" + candidateRecord.TestID + "/archive/files/" + telemetryEvidenceFileName,
			"/api/v1/tests/" + candidateRecord.TestID + "/archive/report",
			"/api/v1/tests/" + candidateRecord.TestID + "/report",
			"/api/v1/tests/" + candidateRecord.TestID + "/archive/root-cause",
			"/api/v1/tests/" + candidateRecord.TestID + "/archive/root-cause/v2",
		} {
			rec := dashboardAPIRequest(t, server, http.MethodGet, route, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("Phase 4 GET %s = %d: %s", route, rec.Code, rec.Body.String())
			}
			requireNoTelemetrySentinel(t, "Phase 4 "+route, rec.Body.String(), int1Sentinels...)
			if strings.HasSuffix(route, "/archive/report") {
				var rep RunArchiveReport
				if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
					t.Fatal(err)
				}
				if rep.TelemetryAcceptance == nil || rep.TelemetryRenderPopulation == nil || rep.TelemetryCoverage == nil || len(rep.TelemetryPolicySegments) != 2 || len(rep.TelemetryStageDistributions) == 0 {
					t.Fatalf("Phase 4 archive report missing telemetry SLA fields: %+v", rep)
				}
				if len(rep.SavedAnalysisPanels) != 1 || rep.SavedAnalysisPanels[0].Status != "ready" {
					t.Fatalf("Phase 4 expected 1 ready SavedAnalysisPanel on archive report, got %+v", rep.SavedAnalysisPanels)
				}
			}
		}

		// Mutate baseline manifest -> SavedAnalysisPanels transitions to "stale".
		mutatedBase := append(append([]byte(nil), rawBase...), ' ')
		if err := os.WriteFile(baseRecord.ManifestPath, mutatedBase, 0o600); err != nil {
			t.Fatal(err)
		}
		nextBase := *baseRecord
		nextBase.ManifestSHA256 = sha256BytesHex(mutatedBase)
		server.runner.mu.Lock()
		server.runner.archives[baseRecord.TestID] = &nextBase
		server.runner.mu.Unlock()
		if err := server.store.SaveRunArchive(&nextBase); err != nil {
			t.Fatal(err)
		}
		staleReport := &RunArchiveReport{Manifest: &RunArchiveManifest{TestID: candidateRecord.TestID, Provenance: RunArchiveProvenance{ProjectID: project}}}
		server.attachDashboardPanels(staleReport)
		if len(staleReport.SavedAnalysisPanels) != 1 || staleReport.SavedAnalysisPanels[0].Status != "stale" {
			t.Fatalf("Phase 4 expected SavedAnalysisPanels status=stale after baseline manifest change, got %+v", staleReport.SavedAnalysisPanels)
		}
	})

	// Phase 5: Native Analytics Multi-Chunk Provenance, Writer Ownership Attestation & Joint Sequence Ledger (NA-A1, NA-B1, NA-C3).
	t.Run("Phase5_NativeAnalyticsProvenanceWriterOwnershipAndJointLedger", func(t *testing.T) {
		t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
		t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
		t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
		t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "int1-analysis-writer-bucket")

		server, store, ready := nativeQueryAuthorityFixture(t)
		actAsWriterB(server)

		// 5a. Multi-chunk duplicate content detection across different chunk boundaries (NA-A1).
		payload := bytes.Repeat([]byte("PAR1-int1-duplicate-provenance-"), 32)
		sum := sha256.Sum256(payload)
		wholeDigest := hex.EncodeToString(sum[:])
		files := []storage.AnalysisImportFile{
			{
				RelativePath: "chunked-a.parquet",
				Size:         int64(len(payload)),
				SHA256:       wholeDigest,
				Chunks: []storage.AnalysisChunk{
					{Offset: 0, Size: 300, SHA256: "c1"},
					{Offset: 300, Size: int64(len(payload) - 300), SHA256: "c2"},
				},
			},
			{
				RelativePath: "chunked-b.parquet",
				Size:         int64(len(payload)),
				SHA256:       wholeDigest,
				Chunks: []storage.AnalysisChunk{
					{Offset: 0, Size: 120, SHA256: "d1"},
					{Offset: 120, Size: 280, SHA256: "d2"},
					{Offset: 400, Size: int64(len(payload) - 400), SHA256: "d3"},
				},
			},
		}
		if first, then, dup := duplicateAnalysisImportFiles(files); !dup || first != "chunked-a.parquet" || then != "chunked-b.parquet" {
			t.Fatalf("Phase 5 expected multi-chunk duplicate detection, got (%q, %q, %v)", first, then, dup)
		}

		// 5b. Remote writer exit + admin attestation unblocks physical cleanup (NA-C3).
		deleted, deletedAttempt := claimAsWriter(t, store, ready, apiWriterA)
		requireDeletionPending(t, timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+deleted.ID, nil), 0, 0, 0, 1, 0)
		actAsWriterB(server, apiWriterA)
		server.reconcileAnalysisWriterIncarnationsOnce(t.Context())
		requireDeletionPending(t, timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+deleted.ID, nil), 0, 0, 1, 0, 0)

		bucket := memblob.OpenBucket(nil)
		t.Cleanup(func() { _ = bucket.Close() })
		pinned := currentArchiveBackend()
		lateKey := filepath.ToSlash(filepath.Join(pinned.Prefix, "_analysis", deletedAttempt.Normalized[0]))
		if err := bucket.WriteAll(t.Context(), lateKey, []byte("late remote commit"), nil); err != nil {
			t.Fatal(err)
		}
		attestResp := timeslotRequest(t, server, "POST", "/api/v1/analysis/imports/"+deleted.ID+"/attest-writer-stopped", map[string]any{
			"reason": "verified pod terminated and remote multipart uploads aborted for INT-1",
		})
		if attestResp.Code != http.StatusOK {
			t.Fatalf("Phase 5 attest-writer-stopped = %d: %s", attestResp.Code, attestResp.Body.String())
		}
		ops := defaultAnalysisCleanupPhysicalOps
		ops.openBucket = func(context.Context, archiveBackendRuntime) (analysisCleanupBucket, error) {
			return analysisNoCloseBucket{bucket}, nil
		}
		cleanupCtx, _ := cleanupOwnerContext(t, 4, ops)
		if err := server.retryAnalysisCleanupJob(cleanupCtx, store, storage.AnalysisCleanupJob{Kind: "attempt", ProjectID: deleted.ProjectID, ResourceID: deletedAttempt.ID}); err != nil {
			t.Fatalf("Phase 5 retryAnalysisCleanupJob: %v", err)
		}
		if exists, err := bucket.Exists(t.Context(), lateKey); err != nil || exists {
			t.Fatalf("Phase 5 expected attested remote object to be deleted, exists=%v err=%v", exists, err)
		}

		// 5c. Durable JointStreams sequence ledger round-trip across telemetryCheckpoint (NA-B1).
		cpRun := checkpointTestRun()
		cpRun.initialArchiveProvenance.JointEvidence = jointTestProjection.Clone()
		ledger := telemetrySourceLedgerLocked(cpRun)
		b3 := checkpointBatch(cpRun, 1)
		b3.BatchSequence = 3
		b3.SequencePrefixStateOnly = true
		ledger.observe("generator", b3, time.UnixMilli(b3.TimestampUnixMs))
		rememberJointPublicationLocked(cpRun, "generator", b3)
		b4 := checkpointBatch(cpRun, 2)
		b4.BatchSequence = 4
		b4.LifecycleState = "COMPLETE"
		ledger.observe("generator", b4, time.UnixMilli(b4.TimestampUnixMs))
		rememberJointPublicationLocked(cpRun, "generator", b4)

		cp := captureTelemetryCheckpointLocked(cpRun)
		rawCP, err := json.Marshal(cp)
		if err != nil {
			t.Fatal(err)
		}
		restoredLedger, _, err := decodeTelemetryCheckpoint(string(rawCP), cp.Revision, cpRun, 0)
		if err != nil {
			t.Fatal(err)
		}
		streamKey := telemetrySourceKey{source: "generator", generation: cpRun.executionGeneration, epoch: "epoch-1"}
		gotStream := restoredLedger.jointStreams[streamKey]
		if gotStream == nil || !reflect.DeepEqual(gotStream.AcceptedRanges, [][2]uint64{{3, 4}}) || !reflect.DeepEqual(gotStream.IgnoredRanges, [][2]uint64{{1, 2}}) {
			t.Fatalf("Phase 5 restored JointStream = %+v", gotStream)
		}

		// Also verify ciValidatedArchiveEvidence scrubs validity reasons cleanly.
		ciManifest := &RunArchiveManifest{
			TestID:   uuid.NewString(),
			TestName: "INT-1 CI Run",
			Provenance: RunArchiveProvenance{
				RuntimeSettings: &RuntimeSettingsOverride{
					RedactionRules: []engine.RedactionRule{{Name: "r", Pattern: "CI_RULE_SECRET", Replacement: "[MASKED]"}},
				},
			},
		}
		ciEv := newCIValidatedArchiveEvidence(ciManifest, nil, &ciRunValidity{Verdict: rca.ValiditySuspect, Reasons: []string{"suspect password=CI_CRED_SECRET CI_RULE_SECRET"}}, false)
		if len(ciEv.Validity.Reasons) != 1 || strings.Contains(ciEv.Validity.Reasons[0], "CI_CRED_SECRET") || strings.Contains(ciEv.Validity.Reasons[0], "CI_RULE_SECRET") {
			t.Fatalf("Phase 5 CI validity reasons were not scrubbed: %+v", ciEv.Validity)
		}
	})

	// Phase 6: Stateless-HTTP Evacuation & Failover Recovery with Real Generator Worker & Target Server (REC-A1, REC-A2, REC-B1, REC-C1).
	t.Run("Phase6_StatelessHTTPEvacuationAndFailoverReconciliation", func(t *testing.T) {
		res := runRECCActiveControllerAndWorkerLoss(t, 1)
		if !res.Passed || res.DuplicateGenerationOverlap != 0 || res.RetainedEvidenceTransactions != res.TargetTotalRequests || len(res.PerGenerationRequests) < 2 {
			t.Fatalf("Phase 6 evacuation/failover reconciliation failed: %+v", res)
		}
	})
}
