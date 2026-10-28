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
