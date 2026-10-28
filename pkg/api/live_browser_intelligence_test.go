package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type sharedMemoryLiveStateStore struct {
	mu        sync.Mutex
	snapshots map[string]RunLiveStateSnapshot
}

func newSharedMemoryLiveStateStore() *sharedMemoryLiveStateStore {
	return &sharedMemoryLiveStateStore{
		snapshots: make(map[string]RunLiveStateSnapshot),
	}
}

func (s *sharedMemoryLiveStateStore) SetRunSnapshot(_ context.Context, snapshot RunLiveStateSnapshot, _ time.Duration) error {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	var copySnap RunLiveStateSnapshot
	if err := json.Unmarshal(raw, &copySnap); err != nil {
		return err
	}
	s.mu.Lock()
	s.snapshots[snapshot.TestID] = copySnap
	s.mu.Unlock()
	return nil
}

func (s *sharedMemoryLiveStateStore) GetRunSnapshot(_ context.Context, testID string) (*RunLiveStateSnapshot, error) {
	s.mu.Lock()
	snap, ok := s.snapshots[testID]
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return nil, err
	}
	var copySnap RunLiveStateSnapshot
	if err := json.Unmarshal(raw, &copySnap); err != nil {
		return nil, err
	}
	return &copySnap, nil
}

func (s *sharedMemoryLiveStateStore) SetGeneratorInventory(_ context.Context, _ []GeneratorInventoryEntry, _ time.Duration) error {
	return nil
}

func (s *sharedMemoryLiveStateStore) GetGeneratorInventory(_ context.Context) ([]GeneratorInventoryEntry, error) {
	return nil, nil
}

func createPinnedBrowserAndJourneyPolicies(t *testing.T, store *storage.SQLiteStore) []RunPolicyPin {
	t.Helper()
	browserPin := saveBrowserPolicy(t, store, "live-browser-pol", 1, BrowserPolicyDefinition{
		Version:  analysisPolicyVersion,
		Baseline: BrowserPolicyBaseline{Mode: "latest_compatible"},
		Metrics: []BrowserPolicyMetric{
			{Metric: "lcp", Good: 2500, Poor: 4000, Weight: 3},
		},
	})
	journeyPolicyObj := JourneyPolicyDefinition{
		Version:         analysisPolicyVersion,
		StepRiskEnabled: true,
		Journeys: []JourneyDefinition{{
			ID:      "checkout",
			Name:    "Checkout flow",
			Weight:  2,
			Start:   JourneyBoundary{Transaction: "Browse", StepPath: "actions[0]"},
			Success: JourneyBoundary{Transaction: "Checkout", StepPath: "actions[1]"},
		}},
	}
	journeyPolicyRaw, err := json.Marshal(journeyPolicyObj)
	if err != nil {
		t.Fatalf("marshal journeyPolicy: %v", err)
	}
	if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{
		ID:         "live-journey-pol",
		ProjectID:  storage.DefaultProjectID,
		Kind:       storage.AnalysisDefinitionKindJourneyPolicy,
		Name:       "Live Journey Policy",
		Revision:   1,
		Definition: journeyPolicyRaw,
	}, 0); err != nil {
		t.Fatalf("SaveAnalysisDefinition(journey_policy): %v", err)
	}
	jDigest := sha256.Sum256(journeyPolicyRaw)
	journeyPin := RunPolicyPin{
		Kind:     storage.AnalysisDefinitionKindJourneyPolicy,
		ID:       "live-journey-pol",
		Name:     "Live Journey Policy",
		Revision: 1,
		SHA256:   hex.EncodeToString(jDigest[:]),
		Source:   "project_default",
	}
	return []RunPolicyPin{browserPin, journeyPin}
}

func TestLiveBrowserIntelligenceLocalHAFollowerAndArchiveParity(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	prevTick := liveBrowserIntelligenceTickInterval
	liveBrowserIntelligenceTickInterval = 15 * time.Millisecond
	t.Cleanup(func() {
		liveBrowserIntelligenceTickInterval = prevTick
	})

	leader, store := baselineTestServer(t)
	sharedLiveStore := newSharedMemoryLiveStateStore()
	leader.runner.setLiveStateStore(sharedLiveStore)

	// Follower shares the SQLite store and LiveStateStore, simulating an HA follower replica.
	follower := NewServer(ServerConfig{Store: store})
	follower.runner.setLiveStateStore(sharedLiveStore)

	pins := createPinnedBrowserAndJourneyPolicies(t, store)
	startedAt := time.Date(2026, 10, 27, 15, 0, 0, 0, time.UTC)
	testID := "run-live-provisional-local"
	provenance := RunArchiveProvenance{
		ProjectID:  storage.DefaultProjectID,
		PolicyPins: pins,
	}
	managed := &ManagedTest{
		ID:                       testID,
		ProjectID:                storage.DefaultProjectID,
		ScenarioID:               "scenario",
		Name:                     "Live Provisional Browser Run",
		Status:                   "RUNNING",
		CreatedAt:                startedAt,
		StartedAt:                &startedAt,
		initialArchiveProvenance: &provenance,
		Groups: []TestGroup{{
			Name:     "web",
			Protocol: "browser",
			VUsers:   1,
		}},
	}
	if err := store.SaveTest(&storage.TestRecord{
		ID:         testID,
		Name:       managed.Name,
		ProjectID:  storage.DefaultProjectID,
		ScenarioID: "scenario",
		Status:     "RUNNING",
		Groups:     []any{},
		StartTime:  startedAt,
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	leader.runner.mu.Lock()
	leader.runner.tests[testID] = managed
	leader.runner.mu.Unlock()

	recorder, err := newRunArchiveWriter(managed, provenance)
	if err != nil {
		t.Fatalf("newRunArchiveWriter: %v", err)
	}
	leader.runner.startLiveBrowserIntelligence(managed, nil)
	sink := combinedResultSink{
		recorder,
		liveBrowserIntelligenceSink{runner: leader.runner, test: managed},
	}

	liveDocSink, ok := engine.ResultSink(sink).(engine.LiveBrowserDocumentEvidenceSink)
	if !ok {
		t.Fatal("combinedResultSink must implement LiveBrowserDocumentEvidenceSink")
	}
	liveIntSink, ok := engine.ResultSink(sink).(engine.LiveBrowserInteractionEvidenceSink)
	if !ok {
		t.Fatal("combinedResultSink must implement LiveBrowserInteractionEvidenceSink")
	}
	docSink := engine.ResultSink(sink).(engine.BrowserDocumentEvidenceSink)
	intSink := engine.ResultSink(sink).(engine.BrowserInteractionEvidenceSink)

	obs := (e1Document{
		id:        1,
		metric:    "lcp",
		engine:    "chromium",
		device:    "desktop",
		value:     4600,
		qualified: true,
	}).observation(startedAt.Add(1 * time.Second))
	docEvidence := &protocol.BrowserDocumentEvidence{
		Version:          protocol.BrowserEvidenceVersion,
		SourceGeneration: d2Generation,
		Observations:     []protocol.BrowserDocumentObservation{obs},
	}
	if err := docSink.RecordBrowserDocumentEvidence(docEvidence); err != nil {
		t.Fatalf("RecordBrowserDocumentEvidence: %v", err)
	}
	if err := liveDocSink.RecordLiveBrowserDocumentEvidence(docEvidence); err != nil {
		t.Fatalf("RecordLiveBrowserDocumentEvidence: %v", err)
	}

	stepBrowse := protocol.NewBrowserOriginalAuthoredStep(protocol.BrowserAuthoredDefinition{
		Version:         1,
		ScriptSHA256:    strings.Repeat("a", 64),
		StructureSHA256: strings.Repeat("c", 64),
	}, "actions[0]")
	stepCheckout := protocol.NewBrowserOriginalAuthoredStep(protocol.BrowserAuthoredDefinition{
		Version:         1,
		ScriptSHA256:    strings.Repeat("a", 64),
		StructureSHA256: strings.Repeat("e", 64),
	}, "actions[1]")

	intEvidence := &protocol.BrowserInteractionEvidence{
		Version:          protocol.BrowserInteractionVersion,
		SourceGeneration: e4Generation,
		Interactions: []protocol.BrowserInteraction{
			e4Tap(1, 0, 100, 100),
			e4Tap(2, 300*time.Millisecond, 100, 100),
			e4Tap(3, 600*time.Millisecond, 100, 100),
		},
	}
	if err := intSink.RecordBrowserInteractionEvidence(intEvidence); err != nil {
		t.Fatalf("RecordBrowserInteractionEvidence: %v", err)
	}
	if err := liveIntSink.RecordLiveBrowserInteractionEvidence(intEvidence); err != nil {
		t.Fatalf("RecordLiveBrowserInteractionEvidence: %v", err)
	}

	// Record journey transactions + iteration completion.
	for _, res := range []*protocol.Result{
		{
			StartTime:           startedAt.Add(1 * time.Second),
			ActionName:          "Browse",
			Group:               "web",
			Phase:               "actions",
			VUserID:             1,
			VUserLifetime:       1,
			Iteration:           1,
			BusinessTransaction: true,
			StatusCode:          200,
			Success:             true,
			AuthoredStep:        stepBrowse,
		},
		{
			StartTime:           startedAt.Add(2 * time.Second),
			ActionName:          "Checkout",
			Group:               "web",
			Phase:               "actions",
			VUserID:             1,
			VUserLifetime:       1,
			Iteration:           1,
			BusinessTransaction: true,
			EndsIteration:       true,
			StatusCode:          500,
			Success:             false,
			ErrorMessage:        "HTTP 500",
			AuthoredStep:        stepCheckout,
		},
	} {
		if err := sink.RecordResult(res); err != nil {
			t.Fatalf("RecordResult: %v", err)
		}
	}

	// Wait for the periodic live ticker to evaluate and publish to LiveStateStore.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap, _ := sharedLiveStore.GetRunSnapshot(context.Background(), testID)
		if snap != nil && snap.ProvisionalBrowserIntelligence != nil &&
			snap.ProvisionalBrowserIntelligence.Findings != nil &&
			snap.ProvisionalBrowserIntelligence.Findings.Result != nil &&
			len(snap.ProvisionalBrowserIntelligence.Interactions.Patterns) > 0 &&
			len(snap.ProvisionalBrowserIntelligence.Journeys.Journeys) > 0 &&
			snap.ProvisionalBrowserIntelligence.Journeys.Journeys[0].Failed == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Check both the leader and the HA follower return identical 200 OK provisional responses!
	for name, srv := range map[string]*Server{"leader": leader, "follower": follower} {
		// 1. Provisional Findings
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/archive/browser-findings", nil)
		req.SetPathValue("id", testID)
		rec := httptest.NewRecorder()
		srv.handleGetRunArchiveBrowserFindings(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s provisional findings status = %d, body = %s", name, rec.Code, rec.Body.String())
		}
		var findingsResp RunBrowserFindingsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &findingsResp); err != nil {
			t.Fatalf("%s decode provisional findings: %v", name, err)
		}
		if !findingsResp.Provisional || findingsResp.Result == nil || !findingsResp.Result.Provisional {
			t.Fatalf("%s expected Provisional=true on findings response, got %+v", name, findingsResp)
		}
		foundNotEvaluatedLive := false
		for _, finding := range findingsResp.Result.Findings {
			if finding.Category == browserFindingCategoryApplication && finding.Metric == "lcp" {
				if finding.Baseline.Status != "not_evaluated_live" || finding.Baseline.Reason != "not_evaluated_live" {
					t.Fatalf("%s finding baseline = %+v, want not_evaluated_live", name, finding.Baseline)
				}
				foundNotEvaluatedLive = true
			}
		}
		if !foundNotEvaluatedLive {
			t.Fatalf("%s expected LCP finding with baseline status not_evaluated_live, got %+v", name, findingsResp.Result.Findings)
		}

		// 2. Provisional Interactions (JSON + CSV)
		req = httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/archive/browser-interactions", nil)
		req.SetPathValue("id", testID)
		rec = httptest.NewRecorder()
		srv.handleGetRunArchiveBrowserInteractions(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s provisional interactions status = %d, body = %s", name, rec.Code, rec.Body.String())
		}
		var intResp BrowserInteractionAnalysis
		if err := json.Unmarshal(rec.Body.Bytes(), &intResp); err != nil {
			t.Fatalf("%s decode provisional interactions: %v", name, err)
		}
		if !intResp.Provisional || intResp.Status != "ready" || len(intResp.Patterns) != 1 || intResp.Patterns[0].Kind != interactionPatternScriptedRepetition {
			t.Fatalf("%s provisional interactions = %+v", name, intResp)
		}

		reqCSV := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/archive/browser-interactions?format=csv", nil)
		reqCSV.SetPathValue("id", testID)
		recCSV := httptest.NewRecorder()
		srv.handleGetRunArchiveBrowserInteractions(recCSV, reqCSV)
		if recCSV.Code != http.StatusOK || !strings.Contains(recCSV.Body.String(), "scripted_repetition") {
			t.Fatalf("%s provisional interactions CSV status = %d, body = %s", name, recCSV.Code, recCSV.Body.String())
		}

		// 3. Provisional Journeys
		req = httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/archive/journeys", nil)
		req.SetPathValue("id", testID)
		rec = httptest.NewRecorder()
		srv.handleGetRunArchiveJourneys(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s provisional journeys status = %d, body = %s", name, rec.Code, rec.Body.String())
		}
		var journeyResp JourneyRiskReport
		if err := json.Unmarshal(rec.Body.Bytes(), &journeyResp); err != nil {
			t.Fatalf("%s decode provisional journeys: %v", name, err)
		}
		if !journeyResp.Provisional || journeyResp.Availability != "available" || len(journeyResp.Journeys) != 1 {
			t.Fatalf("%s provisional journeys = %+v", name, journeyResp)
		}
		if journeyResp.Journeys[0].Failed != 1 || journeyResp.Journeys[0].MatchMode != "step_path" {
			t.Fatalf("%s provisional journey[0] = %+v", name, journeyResp.Journeys[0])
		}
	}

	// Capture the final provisional snapshot right before archive finalization.
	endedAt := startedAt.Add(5 * time.Second)
	leader.runner.stopAndJoinLiveBrowserIntelligence(managed, endedAt)
	lastProvisionalSnap, _ := sharedLiveStore.GetRunSnapshot(context.Background(), testID)
	if lastProvisionalSnap == nil || lastProvisionalSnap.ProvisionalBrowserIntelligence == nil {
		t.Fatal("expected last provisional snapshot before archive finalization")
	}
	provisionalFindings := lastProvisionalSnap.ProvisionalBrowserIntelligence.Findings.Result
	provisionalInteractions := lastProvisionalSnap.ProvisionalBrowserIntelligence.Interactions
	provisionalJourneys := lastProvisionalSnap.ProvisionalBrowserIntelligence.Journeys

	// Finalize the archive and run the archive-attached hook, verifying that
	// provisional state is released and finalized archive responses match the last
	// provisional evaluation!
	archiveRecord, err := recorder.Finalize("COMPLETE", endedAt, "", false, false, nil)
	if err != nil || archiveRecord == nil || strings.TrimSpace(archiveRecord.ManifestSHA256) == "" {
		t.Fatalf("expected finalized archive record, got %+v err=%v", archiveRecord, err)
	}
	admitArchiveForTest(t, archiveRecord)
	if err := store.SaveRunArchive(archiveRecord); err != nil {
		t.Fatalf("SaveRunArchive: %v", err)
	}
	if err := store.SaveTest(&storage.TestRecord{
		ID:         testID,
		Name:       managed.Name,
		ProjectID:  storage.DefaultProjectID,
		ScenarioID: "scenario",
		Status:     "COMPLETE",
		Groups:     []any{},
		StartTime:  startedAt,
		EndTime:    &endedAt,
	}); err != nil {
		t.Fatalf("SaveTest(COMPLETE): %v", err)
	}
	leader.runner.mu.Lock()
	managed.Status = "COMPLETE"
	managed.EndedAt = &endedAt
	managed.Archive = archiveRecord
	releaseLiveBrowserIntelligenceLocked(managed)
	if managed.liveBrowserIntelligence != nil || managed.provisionalBrowserIntelligence != nil {
		leader.runner.mu.Unlock()
		t.Fatalf("expected releaseLiveBrowserIntelligenceLocked to nil both liveBrowserIntelligence and provisionalBrowserIntelligence")
	}
	leader.runner.archives[testID] = archiveRecord
	leader.runner.mu.Unlock()

	leader.autoQueueBrowserIntelligence(testID)
	if ran, err := leader.runNextEvaluationJob(context.Background()); err != nil || !ran {
		t.Fatalf("runNextEvaluationJob: ran=%v err=%v", ran, err)
	}

	finalFindingsResp := getRunBrowserFindings(t, leader, testID)
	if finalFindingsResp.Provisional || finalFindingsResp.Result == nil || finalFindingsResp.Result.Provisional {
		t.Fatalf("expected finalized non-provisional findings after archive commit, got %+v", finalFindingsResp)
	}
	if finalFindingsResp.Result.Counts["active"] != provisionalFindings.Counts["active"] {
		t.Fatalf("final active findings = %d, provisional = %d", finalFindingsResp.Result.Counts["active"], provisionalFindings.Counts["active"])
	}

	reqInt := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/archive/browser-interactions", nil)
	reqInt.SetPathValue("id", testID)
	recInt := httptest.NewRecorder()
	leader.handleGetRunArchiveBrowserInteractions(recInt, reqInt)
	var finalIntResp BrowserInteractionAnalysis
	if err := json.Unmarshal(recInt.Body.Bytes(), &finalIntResp); err != nil {
		t.Fatalf("decode final interactions: %v", err)
	}
	if finalIntResp.Provisional || len(finalIntResp.Patterns) != len(provisionalInteractions.Patterns) || len(finalIntResp.Heatmaps) != len(provisionalInteractions.Heatmaps) {
		t.Fatalf("final interactions mismatch vs provisional: final=%+v provisional=%+v", finalIntResp, provisionalInteractions)
	}

	reqJourneys := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/archive/journeys", nil)
	reqJourneys.SetPathValue("id", testID)
	recJourneys := httptest.NewRecorder()
	leader.handleGetRunArchiveJourneys(recJourneys, reqJourneys)
	var finalJourneyResp JourneyRiskReport
	if err := json.Unmarshal(recJourneys.Body.Bytes(), &finalJourneyResp); err != nil {
		t.Fatalf("decode final journeys: %v", err)
	}
	if finalJourneyResp.Provisional || len(finalJourneyResp.Journeys) != 1 || finalJourneyResp.Journeys[0].Failed != provisionalJourneys.Journeys[0].Failed {
		t.Fatalf("final journeys mismatch vs provisional: final=%+v provisional=%+v", finalJourneyResp, provisionalJourneys)
	}
}

func TestLiveBrowserIntelligenceDistributedTwoGeneratorsAndTruncation(t *testing.T) {
	prevTick := liveBrowserIntelligenceTickInterval
	liveBrowserIntelligenceTickInterval = 15 * time.Millisecond
	t.Cleanup(func() {
		liveBrowserIntelligenceTickInterval = prevTick
	})

	srv, store := baselineTestServer(t)
	pins := createPinnedBrowserAndJourneyPolicies(t, store)
	startedAt := time.Date(2026, 10, 27, 16, 0, 0, 0, time.UTC)
	testID := "run-live-provisional-distributed"
	managed := &ManagedTest{
		ID:         testID,
		ProjectID:  storage.DefaultProjectID,
		ScenarioID: "scenario",
		Name:       "Distributed Provisional Browser Run",
		Status:     "RUNNING",
		CreatedAt:  startedAt,
		StartedAt:  &startedAt,
		initialArchiveProvenance: &RunArchiveProvenance{
			ProjectID:  storage.DefaultProjectID,
			PolicyPins: pins,
		},
		Groups: []TestGroup{{
			Name:     "web",
			Protocol: "browser",
			VUsers:   2,
		}},
	}
	if err := store.SaveTest(&storage.TestRecord{
		ID:         testID,
		Name:       managed.Name,
		ProjectID:  storage.DefaultProjectID,
		ScenarioID: "scenario",
		Status:     "RUNNING",
		Groups:     []any{},
		StartTime:  startedAt,
	}); err != nil {
		t.Fatalf("SaveTest: %v", err)
	}
	refKey, err := store.EnsureTestRefKey(testID, "RUN-PROVISIONAL-DIST")
	if err != nil {
		t.Fatalf("EnsureTestRefKey: %v", err)
	}
	srv.runner.mu.Lock()
	srv.runner.tests[testID] = managed
	srv.runner.mu.Unlock()

	targets := []string{"gen-a:50051", "gen-b:50051"}
	srv.runner.startLiveBrowserIntelligence(managed, targets)
	t.Cleanup(func() {
		srv.runner.stopAndJoinLiveBrowserIntelligence(managed, startedAt.Add(10*time.Second))
	})

	stepBrowse := protocol.NewBrowserOriginalAuthoredStep(protocol.BrowserAuthoredDefinition{
		Version:         1,
		ScriptSHA256:    strings.Repeat("a", 64),
		StructureSHA256: strings.Repeat("c", 64),
	}, "actions[0]")
	stepCheckout := protocol.NewBrowserOriginalAuthoredStep(protocol.BrowserAuthoredDefinition{
		Version:         1,
		ScriptSHA256:    strings.Repeat("a", 64),
		StructureSHA256: strings.Repeat("e", 64),
	}, "actions[1]")

	obsA := (e1Document{
		id:        1,
		metric:    "lcp",
		engine:    "chromium",
		device:    "desktop",
		value:     1800,
		qualified: true,
	}).observation(startedAt.Add(1 * time.Second))
	docA, err := json.Marshal(protocol.BrowserDocumentEvidence{
		Version:          protocol.BrowserEvidenceVersion,
		SourceGeneration: d2Generation,
		Observations:     []protocol.BrowserDocumentObservation{obsA},
	})
	if err != nil {
		t.Fatalf("marshal docA: %v", err)
	}

	batchA := generatorrpc.MetricsBatch{
		TestID:                   testID,
		GeneratorID:              "gen-a",
		SourceEpoch:              "epoch-a",
		BatchSequence:            1,
		LifecycleState:           "RUNNING",
		Telemetry:                &generatorrpc.MetricsTelemetry{CollectionSequence: 10},
		LiveBrowserDocumentsJSON: []string{string(docA)},
		LiveJourneyEvents: []generatorrpc.LiveJourneyEvent{
			{
				TimestampUnixNano:   startedAt.Add(1 * time.Second).UnixNano(),
				ActionName:          "Browse",
				Group:               "web",
				Phase:               "actions",
				Success:             true,
				VUserID:             1,
				VUserLifetime:       1,
				Iteration:           1,
				BusinessTransaction: true,
				AuthoredStep:        stepBrowse,
			},
			{
				TimestampUnixNano:   startedAt.Add(2 * time.Second).UnixNano(),
				ActionName:          "Checkout",
				Group:               "web",
				Phase:               "actions",
				Success:             true,
				VUserID:             1,
				VUserLifetime:       1,
				Iteration:           1,
				BusinessTransaction: true,
				EndsIteration:       true,
				AuthoredStep:        stepCheckout,
			},
		},
	}

	// Generator B starts a journey attempt that remains unfinished when gen-b finishes,
	// and reports generator-side truncation.
	batchB := generatorrpc.MetricsBatch{
		TestID:         testID,
		GeneratorID:    "gen-b",
		SourceEpoch:    "epoch-b",
		BatchSequence:  1,
		LifecycleState: "RUNNING",
		Telemetry:      &generatorrpc.MetricsTelemetry{CollectionSequence: 20},
		LiveBrowserTruncation: &generatorrpc.LiveBrowserTruncation{
			DroppedInteractions: 4,
		},
		LiveJourneyEvents: []generatorrpc.LiveJourneyEvent{
			{
				TimestampUnixNano:   startedAt.Add(1 * time.Second).UnixNano(),
				ActionName:          "Browse",
				Group:               "web",
				Phase:               "actions",
				Success:             true,
				VUserID:             2,
				VUserLifetime:       2,
				Iteration:           1,
				BusinessTransaction: true,
				AuthoredStep:        stepBrowse,
			},
		},
	}

	managed.liveBrowserIntelligence.observeGeneratorBatch("gen-a:50051", batchA, false)
	// Replay gen-a's collection with the same CollectionSequence (without CollectionRepeated)
	// and then with CollectionRepeated=true: neither may double-count!
	batchARepeatSeq := batchA
	batchARepeatSeq.Telemetry = &generatorrpc.MetricsTelemetry{CollectionSequence: 10}
	managed.liveBrowserIntelligence.observeGeneratorBatch("gen-a:50051", batchARepeatSeq, false)

	backend := &distributedExecutionBackend{}
	batchARepeat := batchA
	batchARepeat.GeneratorID = ""
	batchARepeat.BatchSequence = 2
	batchARepeat.LifecycleState = "COMPLETE"
	batchARepeat.Telemetry = &generatorrpc.MetricsTelemetry{CollectionSequence: 10, CollectionRepeated: true}
	backend.applyRemoteMetricsBatch(srv.runner, managed, "gen-a:50051", batchARepeat)

	// Gen-b finishes with terminal LifecycleState="COMPLETE" via applyRemoteMetricsBatch
	// so its unfinished attempt becomes censored and completedTargets["gen-b:50051"] is recorded.
	batchB.GeneratorID = ""
	batchB.LifecycleState = "COMPLETE"
	backend.applyRemoteMetricsBatch(srv.runner, managed, "gen-b:50051", batchB)

	srv.runner.stopAndJoinLiveBrowserIntelligence(managed, startedAt.Add(5*time.Second))

	// Query provisional interactions by ReferenceKey to verify ref_key resolution.
	reqIntRef := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+refKey+"/archive/browser-interactions", nil)
	reqIntRef.SetPathValue("id", refKey)
	recIntRef := httptest.NewRecorder()
	srv.handleGetRunArchiveBrowserInteractions(recIntRef, reqIntRef)
	if recIntRef.Code != http.StatusOK {
		t.Fatalf("provisional interactions by ref_key status = %d, body = %s", recIntRef.Code, recIntRef.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+testID+"/archive/journeys", nil)
	req.SetPathValue("id", testID)
	rec := httptest.NewRecorder()
	srv.handleGetRunArchiveJourneys(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("distributed provisional journeys status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var journeyResp JourneyRiskReport
	if err := json.Unmarshal(rec.Body.Bytes(), &journeyResp); err != nil {
		t.Fatalf("decode distributed journeys: %v", err)
	}
	if !journeyResp.Provisional || !journeyResp.LiveTruncated {
		t.Fatalf("expected Provisional=true and LiveTruncated=true, got %+v", journeyResp)
	}
	if len(journeyResp.Journeys) != 1 {
		t.Fatalf("journeys len = %d, want 1", len(journeyResp.Journeys))
	}
	if journeyResp.Journeys[0].Completed != 1 || journeyResp.Journeys[0].Aborted != 0 || journeyResp.Journeys[0].Eligible != 1 || journeyResp.Journeys[0].Censored != 1 {
		t.Fatalf("journey[0] counts = %+v, want Completed=1 Aborted=0 Eligible=1 Censored=1 (deduplicated across repeated final collection)", journeyResp.Journeys[0])
	}

	if err := srv.runner.teardownActiveTestsAfterLeadershipLoss(context.Background()); err != nil {
		t.Fatalf("teardownActiveTestsAfterLeadershipLoss: %v", err)
	}
	srv.runner.mu.RLock()
	hasLiveAfterLoss := managed.liveBrowserIntelligence != nil || managed.provisionalBrowserIntelligence != nil
	srv.runner.mu.RUnlock()
	if hasLiveAfterLoss {
		t.Fatal("expected teardownActiveTestsAfterLeadershipLoss to clear liveBrowserIntelligence and provisionalBrowserIntelligence")
	}
}

func TestLiveBrowserIntelligenceControllerBoundsAndTruncation(t *testing.T) {
	srv, store := baselineTestServer(t)
	pins := createPinnedBrowserAndJourneyPolicies(t, store)
	startedAt := time.Date(2026, 10, 27, 17, 0, 0, 0, time.UTC)
	testID := "run-live-provisional-bounds"
	managed := &ManagedTest{
		ID:         testID,
		ProjectID:  storage.DefaultProjectID,
		ScenarioID: "scenario",
		Name:       "Provisional Controller Bounds Run",
		Status:     "RUNNING",
		CreatedAt:  startedAt,
		StartedAt:  &startedAt,
		initialArchiveProvenance: &RunArchiveProvenance{
			ProjectID:  storage.DefaultProjectID,
			PolicyPins: pins,
		},
		Groups: []TestGroup{{
			Name:     "web",
			Protocol: "browser",
			VUsers:   1,
		}},
	}
	state := newLiveBrowserIntelligenceState(managed, []string{"local"})
	state.firstEvaluated = true // prevent background notify wakeups during unit checks

	// 1. Malformed non-nil BrowserDocumentEvidence (Clone() == nil) and malformed JSON in generator batch.
	state.recordDocumentEvidence(&protocol.BrowserDocumentEvidence{
		Version:      protocol.BrowserEvidenceVersion,
		Observations: []protocol.BrowserDocumentObservation{{Metric: "invalid"}},
	})
	state.observeGeneratorBatch("local", generatorrpc.MetricsBatch{
		TestID:                      testID,
		LiveBrowserDocumentsJSON:    []string{"{not-json"},
		LiveBrowserInteractionsJSON: []string{"{not-json"},
	}, false)

	// 2. Malformed non-nil BrowserInteractionEvidence (Clone() == nil).
	state.recordInteractionEvidence("local", &protocol.BrowserInteractionEvidence{
		Version: protocol.BrowserInteractionVersion,
	})

	// 3. Exceed maxLiveBrowserInteractionRecords (2048).
	validInteraction := &protocol.BrowserInteractionEvidence{
		Version:          protocol.BrowserInteractionVersion,
		SourceGeneration: d2Generation,
		Interactions: []protocol.BrowserInteraction{{
			Version:           protocol.BrowserInteractionVersion,
			SourceGeneration:  d2Generation,
			InteractionID:     d2Generation + "-i-1",
			ActionID:          d2Generation + "-a-1",
			DocumentID:        d2Generation + "-idoc-" + strings.Repeat("b", 32),
			FrameScope:        "main_frame",
			Kind:              "click",
			PointerType:       "mouse",
			Points:            []protocol.BrowserInteractionPoint{{X: 100, Y: 100}},
			ViewportWidth:     1280,
			ViewportHeight:    720,
			DeviceScaleFactor: 1,
			LayoutRevision:    "0123456789abcdef",
			Route:             "/shop",
			Outcome:           protocol.BrowserInteractionOutcome{Observed: protocol.InteractionOutcomeNotDeclared},
			Provenance:        protocol.InteractionProvenanceScripted,
			Success:           true,
			CapturedAt:        startedAt.Add(1 * time.Second),
		}},
	}
	state.mu.Lock()
	state.interactionRecords = maxLiveBrowserInteractionRecords
	state.mu.Unlock()
	state.recordInteractionEvidence("local", validInteraction)

	// Also verify maxLiveBrowserInteractionTotalCount (100,000) bound.
	state.mu.Lock()
	state.interactionRecords = 0
	state.interactionCount = maxLiveBrowserInteractionTotalCount
	state.mu.Unlock()
	state.recordInteractionEvidence("local", validInteraction)

	// 4. Exceed maxLiveBrowserJourneyEvents (100,000).
	state.mu.Lock()
	state.journeyEventCount = maxLiveBrowserJourneyEvents
	state.mu.Unlock()
	state.recordJourneyEvent("local", TransactionEvent{
		Timestamp:     startedAt.Add(2 * time.Second),
		ActionName:    "Browse",
		Group:         "web",
		Phase:         "actions",
		Success:       true,
		VUserID:       1,
		VUserLifetime: 1,
		Iteration:     1,
	})

	snap := state.evaluateSnapshot(srv.runner, startedAt.Add(3*time.Second), true)
	if snap == nil || !snap.LiveTruncated {
		t.Fatalf("expected LiveTruncated=true on controller bounds snapshot, got %+v", snap)
	}
	for _, wantReason := range []string{
		"malformed_document_evidence",
		"malformed_interaction_evidence",
		"interactions_truncated",
		"journey_events_truncated",
	} {
		if !slices.Contains(snap.TruncatedReasons, wantReason) {
			t.Fatalf("TruncatedReasons = %v, missing %q", snap.TruncatedReasons, wantReason)
		}
	}
	if snap.Findings == nil || !snap.Findings.LiveTruncated || snap.Interactions == nil || !snap.Interactions.LiveTruncated || snap.Journeys == nil || !snap.Journeys.LiveTruncated {
		t.Fatalf("expected all three provisional sub-snapshots to carry LiveTruncated=true: findings=%+v interactions=%+v journeys=%+v", snap.Findings, snap.Interactions, snap.Journeys)
	}
}

func TestLiveBrowserIntelligencePinnedBaselineProjectCheckAndRefKeyResolution(t *testing.T) {
	srv, store := baselineTestServer(t)
	startedAt := time.Date(2026, 10, 27, 18, 0, 0, 0, time.UTC)

	// 1. Same-project baseline run referenced by ref_key.
	sameProjBaselineID := "run-base-same-proj"
	sameProjManifestSHA := strings.Repeat("b", 64)
	if err := store.SaveTest(&storage.TestRecord{
		ID:         sameProjBaselineID,
		Name:       "Same Project Baseline",
		ProjectID:  storage.DefaultProjectID,
		ScenarioID: "scenario",
		Status:     "COMPLETE",
		Groups:     []any{},
		StartTime:  startedAt.Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveTest(sameProjBaseline): %v", err)
	}
	sameProjRefKey, err := store.EnsureTestRefKey(sameProjBaselineID, "RUN-BASE-SAME-PROJ")
	if err != nil {
		t.Fatalf("EnsureTestRefKey(sameProjBaseline): %v", err)
	}
	if err := store.SaveRunArchive(&storage.RunArchiveRecord{
		TestID:         sameProjBaselineID,
		ArchiveBackend: "filesystem",
		ArchivePath:    t.TempDir(),
		ManifestPath:   "manifest.json",
		ManifestSHA256: sameProjManifestSHA,
		CreatedAt:      startedAt.Add(-10 * time.Minute),
		CompletedAt:    startedAt.Add(-5 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveRunArchive(sameProjBaseline): %v", err)
	}

	// 2. Foreign-project baseline run.
	if err := store.SaveProject(&storage.Project{
		ID:        "proj-foreign",
		Name:      "Foreign Project",
		CreatedAt: startedAt,
		UpdatedAt: startedAt,
	}); err != nil {
		t.Fatalf("SaveProject(proj-foreign): %v", err)
	}
	foreignBaselineID := "run-base-foreign"
	foreignManifestSHA := strings.Repeat("f", 64)
	if err := store.SaveTest(&storage.TestRecord{
		ID:         foreignBaselineID,
		Name:       "Foreign Project Baseline",
		ProjectID:  "proj-foreign",
		ScenarioID: "",
		Status:     "COMPLETE",
		Groups:     []any{},
		StartTime:  startedAt.Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveTest(foreignBaseline): %v", err)
	}
	if err := store.SaveRunArchive(&storage.RunArchiveRecord{
		TestID:         foreignBaselineID,
		ArchiveBackend: "filesystem",
		ArchivePath:    t.TempDir(),
		ManifestPath:   "manifest.json",
		ManifestSHA256: foreignManifestSHA,
		CreatedAt:      startedAt.Add(-10 * time.Minute),
		CompletedAt:    startedAt.Add(-5 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveRunArchive(foreignBaseline): %v", err)
	}

	obs := (e1Document{
		id:        1,
		metric:    "lcp",
		engine:    "chromium",
		device:    "desktop",
		value:     4600,
		qualified: true,
	}).observation(startedAt.Add(1 * time.Second))
	docEvidence := &protocol.BrowserDocumentEvidence{
		Version:          protocol.BrowserEvidenceVersion,
		SourceGeneration: d2Generation,
		Observations:     []protocol.BrowserDocumentObservation{obs},
	}

	// Evaluate with same-project ref_key: must resolve canonical ID and load ManifestSHA256.
	sameProjPin := saveBrowserPolicy(t, store, "pol-pinned-same-ref", 1, BrowserPolicyDefinition{
		Version:  analysisPolicyVersion,
		Baseline: BrowserPolicyBaseline{Mode: "pinned_run", RunID: sameProjRefKey},
		Metrics:  []BrowserPolicyMetric{{Metric: "lcp", Good: 2500, Poor: 4000, Weight: 3}},
	})
	stateSame := newLiveBrowserIntelligenceState(&ManagedTest{
		ID:        "run-live-same-ref",
		ProjectID: storage.DefaultProjectID,
		StartedAt: &startedAt,
		initialArchiveProvenance: &RunArchiveProvenance{
			ProjectID:  storage.DefaultProjectID,
			PolicyPins: []RunPolicyPin{sameProjPin},
		},
		Groups: []TestGroup{{Name: "web", Protocol: "browser", VUsers: 1}},
	}, nil)
	stateSame.firstEvaluated = true
	stateSame.recordDocumentEvidence(docEvidence)
	snapSame := stateSame.evaluateSnapshot(srv.runner, startedAt.Add(2*time.Second), false)
	if snapSame == nil || snapSame.Findings == nil || snapSame.Findings.Result == nil || len(snapSame.Findings.Result.Findings) == 0 {
		t.Fatalf("expected findings for same-project ref_key baseline, got %+v", snapSame)
	}
	gotSameBaseline := snapSame.Findings.Result.Findings[0].Baseline
	if gotSameBaseline.BaselineRunID != sameProjBaselineID || gotSameBaseline.BaselineArchiveSHA256 != sameProjManifestSHA {
		t.Fatalf("same-project ref_key baseline = %+v, want BaselineRunID=%q BaselineArchiveSHA256=%q", gotSameBaseline, sameProjBaselineID, sameProjManifestSHA)
	}

	// Evaluate with foreign-project run ID: must NOT expose foreign BaselineRunID or ManifestSHA256.
	foreignPin := saveBrowserPolicy(t, store, "pol-pinned-foreign", 1, BrowserPolicyDefinition{
		Version:  analysisPolicyVersion,
		Baseline: BrowserPolicyBaseline{Mode: "pinned_run", RunID: foreignBaselineID},
		Metrics:  []BrowserPolicyMetric{{Metric: "lcp", Good: 2500, Poor: 4000, Weight: 3}},
	})
	stateForeign := newLiveBrowserIntelligenceState(&ManagedTest{
		ID:        "run-live-foreign-base",
		ProjectID: storage.DefaultProjectID,
		StartedAt: &startedAt,
		initialArchiveProvenance: &RunArchiveProvenance{
			ProjectID:  storage.DefaultProjectID,
			PolicyPins: []RunPolicyPin{foreignPin},
		},
		Groups: []TestGroup{{Name: "web", Protocol: "browser", VUsers: 1}},
	}, nil)
	stateForeign.firstEvaluated = true
	stateForeign.recordDocumentEvidence(docEvidence)
	snapForeign := stateForeign.evaluateSnapshot(srv.runner, startedAt.Add(2*time.Second), false)
	if snapForeign == nil || snapForeign.Findings == nil || snapForeign.Findings.Result == nil || len(snapForeign.Findings.Result.Findings) == 0 {
		t.Fatalf("expected findings for foreign baseline test, got %+v", snapForeign)
	}
	gotForeignBaseline := snapForeign.Findings.Result.Findings[0].Baseline
	if gotForeignBaseline.BaselineRunID != "" || gotForeignBaseline.BaselineArchiveSHA256 != "" {
		t.Fatalf("foreign baseline leaked across projects: %+v", gotForeignBaseline)
	}
}

func TestLiveBrowserIntelligenceRound2Fixes(t *testing.T) {
	srv, store := baselineTestServer(t)
	pins := createPinnedBrowserAndJourneyPolicies(t, store)
	startedAt := time.Date(2026, 10, 27, 19, 0, 0, 0, time.UTC)

	// 1. t=0 evaluateSnapshot must not set firstEvaluated=true when no evidence or truncation exists,
	// so the first real evidence arrival still signals notifyCh.
	testNotify := &ManagedTest{
		ID:        "run-live-notify-t0",
		ProjectID: storage.DefaultProjectID,
		Status:    "RUNNING",
		CreatedAt: startedAt,
		StartedAt: &startedAt,
		initialArchiveProvenance: &RunArchiveProvenance{
			ProjectID:  storage.DefaultProjectID,
			PolicyPins: pins,
		},
		Groups: []TestGroup{{Name: "web", Protocol: "browser", VUsers: 1}},
	}
	stateNotify := newLiveBrowserIntelligenceState(testNotify, nil)
	_ = stateNotify.evaluateSnapshot(srv.runner, startedAt, false)
	stateNotify.mu.Lock()
	firstEvalAtT0 := stateNotify.firstEvaluated
	stateNotify.mu.Unlock()
	if firstEvalAtT0 {
		t.Fatal("expected firstEvaluated=false after empty t=0 evaluateSnapshot")
	}
	obs := (e1Document{
		id:        1,
		metric:    "lcp",
		engine:    "chromium",
		device:    "desktop",
		value:     2100,
		qualified: true,
	}).observation(startedAt.Add(1 * time.Second))
	stateNotify.recordDocumentEvidence(&protocol.BrowserDocumentEvidence{
		Version:          protocol.BrowserEvidenceVersion,
		SourceGeneration: d2Generation,
		Observations:     []protocol.BrowserDocumentObservation{obs},
	})
	select {
	case <-stateNotify.notifyCh:
	default:
		t.Fatal("expected notifyCh to receive wakeup on first real document evidence after t=0 publish")
	}
	_ = stateNotify.evaluateSnapshot(srv.runner, startedAt.Add(2*time.Second), false)
	stateNotify.mu.Lock()
	firstEvalAfterEvidence := stateNotify.firstEvaluated
	stateNotify.mu.Unlock()
	if !firstEvalAfterEvidence {
		t.Fatal("expected firstEvaluated=true after evaluating real document evidence")
	}

	// 2. Adopted run failover preservation, project isolation, follower explicit policy ref,
	// cacheRunSnapshot timestamp ordering, and attachFinalizedRunArchive clearing liveState.
	liveStore := newSharedMemoryLiveStateStore()
	srv.runner.setLiveStateStore(liveStore)

	failoverTestID := "run-live-adopted-failover"
	preFailoverJourneys := &JourneyRiskReport{
		SchemaVersion: "journey_accounting.v1",
		RunID:         failoverTestID,
		Availability:  "available",
		Provisional:   true,
		Policy: JourneyPolicyRef{
			ID:       "live-journey-pol",
			Name:     "Live Journey Policy",
			Revision: 1,
			SHA256:   pins[1].SHA256,
			Source:   "pinned",
		},
		Journeys: []JourneyAccounting{{ID: "checkout", Name: "Checkout flow", Completed: 3, Eligible: 3}},
	}
	preFailoverSnap := &ProvisionalBrowserIntelligenceSnapshot{
		UpdatedAt: startedAt.Add(5 * time.Second),
		Findings: &RunBrowserFindingsResponse{
			RunID:       failoverTestID,
			Status:      "ready",
			Provisional: true,
			Result: &BrowserIntelligenceResult{
				RunID:       failoverTestID,
				Provisional: true,
			},
		},
		Interactions: &BrowserInteractionAnalysis{
			RunID:       failoverTestID,
			Version:     browserInteractionAnalysisVersion,
			Status:      "ready",
			Provisional: true,
		},
		Journeys: preFailoverJourneys,
	}

	// Seed liveState with a foreign-project snapshot first: restoreAdoptedProvisionalBrowserIntelligence must reject it.
	if err := liveStore.SetRunSnapshot(context.Background(), RunLiveStateSnapshot{
		TestID:                         failoverTestID,
		ProjectID:                      "proj-foreign",
		Status:                         "RUNNING",
		UpdatedAt:                      startedAt.Add(5 * time.Second),
		ProvisionalBrowserIntelligence: preFailoverSnap,
	}, time.Minute); err != nil {
		t.Fatalf("SetRunSnapshot(foreign): %v", err)
	}
	adoptedManaged := &ManagedTest{
		ID:                  failoverTestID,
		ProjectID:           storage.DefaultProjectID,
		Name:                "Adopted Run",
		Status:              "RUNNING",
		CreatedAt:           startedAt,
		StartedAt:           &startedAt,
		continuityFailovers: []RunFailover{{PriorGeneration: 1, Generation: 2, AdoptedAt: startedAt.Add(6 * time.Second)}},
		initialArchiveProvenance: &RunArchiveProvenance{
			ProjectID:  storage.DefaultProjectID,
			PolicyPins: pins,
		},
		Groups: []TestGroup{{Name: "web", Protocol: "browser", VUsers: 1}},
	}
	srv.runner.mu.Lock()
	srv.runner.tests[failoverTestID] = adoptedManaged
	srv.runner.mu.Unlock()
	if srv.runner.restoreAdoptedProvisionalBrowserIntelligence(adoptedManaged) {
		t.Fatal("expected foreign-project cached provisional snapshot to be rejected")
	}

	// Seed liveState with matching project ID and verify adopted run restores and preserves it
	// with LiveTruncated=true and "controller_failover" even when zero post-failover records arrive.
	if err := liveStore.SetRunSnapshot(context.Background(), RunLiveStateSnapshot{
		TestID:                         failoverTestID,
		ProjectID:                      storage.DefaultProjectID,
		Status:                         "RUNNING",
		UpdatedAt:                      startedAt.Add(5 * time.Second),
		ProvisionalBrowserIntelligence: preFailoverSnap,
	}, time.Minute); err != nil {
		t.Fatalf("SetRunSnapshot(same project): %v", err)
	}
	srv.runner.startLiveBrowserIntelligence(adoptedManaged, []string{"local"})
	// Simulate publishFinalLocalMetrics writing a newer UpdatedAt before stopAndJoinLiveBrowserIntelligence
	// calls publishLiveBrowserIntelligenceSnapshot with an older completedAt timestamp.
	endedAt := startedAt.Add(10 * time.Second)
	srv.runner.mu.Lock()
	adoptedManaged.Status = "COMPLETE"
	adoptedManaged.EndedAt = &endedAt
	newerFinalSnap := buildRunLiveStateSnapshotLocked(adoptedManaged, time.Now().UTC().Add(2*time.Second))
	newerFinalSnap.ProvisionalBrowserIntelligence = nil
	srv.runner.mu.Unlock()
	srv.runner.cacheRunSnapshot(newerFinalSnap)

	srv.runner.stopAndJoinLiveBrowserIntelligence(adoptedManaged, endedAt)
	waitForCondition(t, 2*time.Second, func() bool {
		snap, err := liveStore.GetRunSnapshot(context.Background(), failoverTestID)
		return err == nil && snap != nil && snap.ProvisionalBrowserIntelligence != nil
	}, "stopAndJoinLiveBrowserIntelligence snapshot in liveStore")

	cachedAfterStop, err := liveStore.GetRunSnapshot(context.Background(), failoverTestID)
	if err != nil || cachedAfterStop == nil || cachedAfterStop.ProvisionalBrowserIntelligence == nil {
		t.Fatalf("expected stopAndJoinLiveBrowserIntelligence snapshot to survive in liveStore, got %+v (err=%v)", cachedAfterStop, err)
	}
	decodedAfterStop := cachedAfterStop.ProvisionalBrowserIntelligence
	if !decodedAfterStop.LiveTruncated || !slices.Contains(decodedAfterStop.TruncatedReasons, "controller_failover") {
		t.Fatalf("expected controller_failover truncation on preserved snapshot, got %+v", decodedAfterStop)
	}
	if decodedAfterStop.Interactions == nil || decodedAfterStop.Interactions.Status != "ready" {
		t.Fatalf("expected pre-failover ready interactions to be preserved when 0 post-failover records arrived, got %+v", decodedAfterStop.Interactions)
	}

	// Simulate follower (no local state in runner.tests) querying provisional journeys with explicit policy_id:
	// out.Policy.Source must be updated to "requested" via ref.
	srv.runner.mu.Lock()
	delete(srv.runner.tests, failoverTestID)
	srv.runner.mu.Unlock()
	followerReport, status, journeyErr, handled := srv.provisionalRunJourneys(context.Background(), &ManagedTest{
		ID:        failoverTestID,
		ProjectID: storage.DefaultProjectID,
		Status:    "COMPLETE",
	}, "live-journey-pol", "1")
	if !handled || journeyErr != nil || status != http.StatusOK || followerReport == nil {
		t.Fatalf("follower provisionalRunJourneys = (%+v, %d, %v, %v)", followerReport, status, journeyErr, handled)
	}
	if followerReport.Policy.Source != "requested" {
		t.Fatalf("follower provisionalRunJourneys Policy.Source = %q, want %q", followerReport.Policy.Source, "requested")
	}

	// Re-register adoptedManaged and attach finalized archive: must clear ProvisionalBrowserIntelligence in liveState
	// and liveBrowserIntelligenceForTest must return nil, nil, true immediately.
	srv.runner.mu.Lock()
	srv.runner.tests[failoverTestID] = adoptedManaged
	srv.runner.mu.Unlock()
	srv.runner.attachFinalizedRunArchive(failoverTestID, &storage.RunArchiveRecord{
		TestID:         failoverTestID,
		ManifestSHA256: strings.Repeat("d", 64),
	}, nil)
	waitForCondition(t, 2*time.Second, func() bool {
		snap, err := liveStore.GetRunSnapshot(context.Background(), failoverTestID)
		return err == nil && snap != nil && snap.ProvisionalBrowserIntelligence == nil
	}, "attachFinalizedRunArchive clearing ProvisionalBrowserIntelligence in liveStore")

	cachedAfterAttach, err := liveStore.GetRunSnapshot(context.Background(), failoverTestID)
	if err != nil || cachedAfterAttach == nil {
		t.Fatalf("GetRunSnapshot after attach: %+v (err=%v)", cachedAfterAttach, err)
	}
	if cachedAfterAttach.ProvisionalBrowserIntelligence != nil {
		t.Fatalf("expected attachFinalizedRunArchive to clear ProvisionalBrowserIntelligence in liveState, got %+v", cachedAfterAttach.ProvisionalBrowserIntelligence)
	}
	if gotState, gotSnap, gotTerm := srv.liveBrowserIntelligenceForTest(context.Background(), failoverTestID); gotState != nil || gotSnap != nil || !gotTerm {
		t.Fatalf("expected liveBrowserIntelligenceForTest after archive attach to return (nil, nil, true), got (%v, %+v, %v)", gotState, gotSnap, gotTerm)
	}
}

func TestLiveBrowserIntelligenceRound3Fixes(t *testing.T) {
	srv, store := baselineTestServer(t)
	pins := createPinnedBrowserAndJourneyPolicies(t, store)
	startedAt := time.Date(2026, 10, 27, 20, 0, 0, 0, time.UTC)

	liveStore := newSharedMemoryLiveStateStore()
	srv.runner.setLiveStateStore(liveStore)

	failoverTestID := "run-live-round3-failover"
	preFailoverSnap := &ProvisionalBrowserIntelligenceSnapshot{
		UpdatedAt: startedAt.Add(3 * time.Second),
		Findings: &RunBrowserFindingsResponse{
			RunID:       failoverTestID,
			Status:      "ready",
			Provisional: true,
			Result: &BrowserIntelligenceResult{
				RunID:       failoverTestID,
				Provisional: true,
			},
		},
		Interactions: &BrowserInteractionAnalysis{
			RunID:       failoverTestID,
			Version:     browserInteractionAnalysisVersion,
			Status:      "ready",
			Provisional: true,
			Patterns:    []BrowserInteractionPattern{{ID: "p-pre", Kind: interactionPatternScriptedRepetition, Count: 3}},
		},
		Journeys: &JourneyRiskReport{
			SchemaVersion: "journey_accounting.v1",
			RunID:         failoverTestID,
			Availability:  "available",
			Provisional:   true,
			Policy: JourneyPolicyRef{
				ID:       "live-journey-pol",
				Name:     "Live Journey Policy",
				Revision: 1,
				SHA256:   pins[1].SHA256,
				Source:   "pinned",
			},
			Journeys: []JourneyAccounting{{ID: "checkout", Name: "Checkout flow", Completed: 4, Eligible: 4}},
		},
	}

	// Seed liveState with untruncated pre-failover snapshot.
	if err := liveStore.SetRunSnapshot(context.Background(), RunLiveStateSnapshot{
		TestID:                         failoverTestID,
		ProjectID:                      storage.DefaultProjectID,
		Status:                         "RUNNING",
		UpdatedAt:                      startedAt.Add(3 * time.Second),
		ProvisionalBrowserIntelligence: preFailoverSnap,
	}, time.Minute); err != nil {
		t.Fatalf("SetRunSnapshot: %v", err)
	}

	adoptedManaged := &ManagedTest{
		ID:                  failoverTestID,
		ProjectID:           storage.DefaultProjectID,
		Name:                "Adopted Round 3 Run",
		Status:              "RUNNING",
		CreatedAt:           startedAt,
		StartedAt:           &startedAt,
		continuityFailovers: []RunFailover{{PriorGeneration: 1, Generation: 2, AdoptedAt: startedAt.Add(4 * time.Second)}},
		initialArchiveProvenance: &RunArchiveProvenance{
			ProjectID:  storage.DefaultProjectID,
			PolicyPins: pins,
		},
		Groups: []TestGroup{{Name: "web", Protocol: "browser", VUsers: 1}},
	}
	srv.runner.mu.Lock()
	srv.runner.tests[failoverTestID] = adoptedManaged
	srv.runner.mu.Unlock()

	// 1. High 1: startLiveBrowserIntelligence -> restoreAdoptedProvisionalBrowserIntelligence must immediately
	// persist the controller_failover truncated snapshot back to liveState before any post-failover batch arrives.
	srv.runner.startLiveBrowserIntelligence(adoptedManaged, []string{"local"})
	t.Cleanup(func() {
		srv.runner.stopAndJoinLiveBrowserIntelligence(adoptedManaged, startedAt.Add(15*time.Second))
	})

	waitForCondition(t, 2*time.Second, func() bool {
		snap, err := liveStore.GetRunSnapshot(context.Background(), failoverTestID)
		return err == nil && snap != nil && snap.ProvisionalBrowserIntelligence != nil && snap.ProvisionalBrowserIntelligence.LiveTruncated
	}, "restoreAdoptedProvisionalBrowserIntelligence persisting controller_failover snapshot to liveStore")

	restoredInStore, err := liveStore.GetRunSnapshot(context.Background(), failoverTestID)
	if err != nil || restoredInStore == nil || restoredInStore.ProvisionalBrowserIntelligence == nil {
		t.Fatalf("expected restored snapshot in liveStore, got %+v (err=%v)", restoredInStore, err)
	}
	if !restoredInStore.ProvisionalBrowserIntelligence.LiveTruncated ||
		!slices.Contains(restoredInStore.ProvisionalBrowserIntelligence.TruncatedReasons, "controller_failover") {
		t.Fatalf("expected LiveTruncated=true and controller_failover in liveStore immediately after adoption, got %+v", restoredInStore.ProvisionalBrowserIntelligence)
	}
	if restoredInStore.ProvisionalBrowserIntelligence.Journeys == nil ||
		!slices.Contains(restoredInStore.ProvisionalBrowserIntelligence.Journeys.IncompleteReasons, "controller_failover") {
		t.Fatalf("expected controller_failover in Journeys.IncompleteReasons, got %+v", restoredInStore.ProvisionalBrowserIntelligence.Journeys)
	}

	// 2. High 2A: Record 1 post-failover document while 0 interactions and 0 journey events have arrived.
	// publishLiveBrowserIntelligenceSnapshot must update Findings while preserving pre-failover Interactions and Journeys!
	obs := (e1Document{
		id:        1,
		metric:    "lcp",
		engine:    "chromium",
		device:    "desktop",
		value:     3100,
		qualified: true,
	}).observation(startedAt.Add(5 * time.Second))
	adoptedManaged.liveBrowserIntelligence.recordDocumentEvidence(&protocol.BrowserDocumentEvidence{
		Version:          protocol.BrowserEvidenceVersion,
		SourceGeneration: d2Generation,
		Observations:     []protocol.BrowserDocumentObservation{obs},
	})
	srv.runner.publishLiveBrowserIntelligenceSnapshot(adoptedManaged, adoptedManaged.liveBrowserIntelligence, startedAt.Add(6*time.Second), false)

	srv.runner.mu.RLock()
	afterDocSnap := cloneProvisionalBrowserIntelligenceSnapshot(adoptedManaged.provisionalBrowserIntelligence)
	srv.runner.mu.RUnlock()
	if afterDocSnap == nil || !afterDocSnap.LiveTruncated || !slices.Contains(afterDocSnap.TruncatedReasons, "controller_failover") {
		t.Fatalf("expected truncated snapshot after doc publish, got %+v", afterDocSnap)
	}
	if afterDocSnap.Findings == nil || afterDocSnap.Findings.Result == nil || !afterDocSnap.Findings.LiveTruncated {
		t.Fatalf("expected updated post-failover Findings with LiveTruncated=true, got %+v", afterDocSnap.Findings)
	}
	if afterDocSnap.Interactions == nil || afterDocSnap.Interactions.Status != "ready" || len(afterDocSnap.Interactions.Patterns) != 1 || !afterDocSnap.Interactions.LiveTruncated {
		t.Fatalf("expected pre-failover ready Interactions to be preserved when only doc evidence arrived, got %+v", afterDocSnap.Interactions)
	}
	if afterDocSnap.Journeys == nil || afterDocSnap.Journeys.Availability != "available" || len(afterDocSnap.Journeys.Journeys) != 1 || afterDocSnap.Journeys.Journeys[0].Completed != 4 || !afterDocSnap.Journeys.LiveTruncated {
		t.Fatalf("expected pre-failover available Journeys to be preserved when only doc evidence arrived, got %+v", afterDocSnap.Journeys)
	}

	// 3. High 2B: On the adopted leader (state != nil, journeyEventCount == 0), querying provisionalRunJourneys
	// with explicit matching ?policy=live-journey-pol&revision=1 must return the preserved pre-failover report!
	leaderMatchingReport, status, journeyErr, handled := srv.provisionalRunJourneys(context.Background(), adoptedManaged, "live-journey-pol", "1")
	if !handled || journeyErr != nil || status != http.StatusOK || leaderMatchingReport == nil {
		t.Fatalf("adopted leader provisionalRunJourneys(matching policy) = (%+v, %d, %v, %v)", leaderMatchingReport, status, journeyErr, handled)
	}
	if leaderMatchingReport.Policy.Source != "requested" || !leaderMatchingReport.LiveTruncated || !leaderMatchingReport.Incomplete ||
		!slices.Contains(leaderMatchingReport.IncompleteReasons, "controller_failover") ||
		len(leaderMatchingReport.Journeys) != 1 || leaderMatchingReport.Journeys[0].Completed != 4 {
		t.Fatalf("adopted leader matching policy report lost pre-failover journeys or truncation metadata: %+v", leaderMatchingReport)
	}

	// Save a second revision of live-journey-pol and query ?policy=live-journey-pol&revision=2 on the adopted leader:
	// since revision 2 does not match snap.Journeys.Policy.Revision (1), it evaluates over post-failover state and must still mark
	// LiveTruncated=true, Incomplete=true, and IncompleteReasons containing "live_truncated" and "controller_failover".
	journeyDef, err := store.GetAnalysisDefinition(storage.DefaultProjectID, "live-journey-pol", 1)
	if err != nil || journeyDef == nil {
		t.Fatalf("GetAnalysisDefinition(live-journey-pol@1): %v", err)
	}
	if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{
		ID:         "live-journey-pol",
		ProjectID:  storage.DefaultProjectID,
		Kind:       storage.AnalysisDefinitionKindJourneyPolicy,
		Name:       "Live Journey Policy v2",
		Revision:   2,
		Definition: journeyDef.Definition,
	}, 1); err != nil {
		t.Fatalf("SaveAnalysisDefinition(live-journey-pol@2): %v", err)
	}
	leaderOtherRevReport, status, journeyErr, handled := srv.provisionalRunJourneys(context.Background(), adoptedManaged, "live-journey-pol", "2")
	if !handled || journeyErr != nil || status != http.StatusOK || leaderOtherRevReport == nil {
		t.Fatalf("adopted leader provisionalRunJourneys(rev 2) = (%+v, %d, %v, %v)", leaderOtherRevReport, status, journeyErr, handled)
	}
	if !leaderOtherRevReport.LiveTruncated || !leaderOtherRevReport.Incomplete ||
		!slices.Contains(leaderOtherRevReport.IncompleteReasons, "live_truncated") ||
		!slices.Contains(leaderOtherRevReport.IncompleteReasons, "controller_failover") {
		t.Fatalf("expected LiveTruncated=true and controller_failover in IncompleteReasons for ad-hoc rev 2 evaluation on adopted leader, got %+v", leaderOtherRevReport)
	}

	// 4. Low 1: If a terminal test already nilled liveBrowserIntelligence (e.g. after stop) while residual
	// provisionalBrowserIntelligence remained set, teardownActiveTestsAfterLeadershipLoss must clear it.
	residualTest := &ManagedTest{
		ID:                             "run-residual-provisional",
		ProjectID:                      storage.DefaultProjectID,
		Status:                         "COMPLETE",
		provisionalBrowserIntelligence: cloneProvisionalBrowserIntelligenceSnapshot(preFailoverSnap),
	}
	srv.runner.mu.Lock()
	srv.runner.tests[residualTest.ID] = residualTest
	srv.runner.mu.Unlock()
	if err := srv.runner.teardownActiveTestsAfterLeadershipLoss(context.Background()); err != nil {
		t.Fatalf("teardownActiveTestsAfterLeadershipLoss: %v", err)
	}
	srv.runner.mu.RLock()
	residualAfterLoss := residualTest.provisionalBrowserIntelligence
	srv.runner.mu.RUnlock()
	if residualAfterLoss != nil {
		t.Fatalf("expected teardownActiveTestsAfterLeadershipLoss to clear residual provisionalBrowserIntelligence even when liveBrowserIntelligence == nil, got %+v", residualAfterLoss)
	}
}



