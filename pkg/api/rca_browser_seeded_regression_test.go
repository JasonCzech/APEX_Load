package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestBrowserSeededRegressionProductionPath(t *testing.T) {
	if os.Getenv("APEXLOAD_BROWSER_TESTS") != "require" {
		t.Skip("native browser seeded regression qualification requires APEXLOAD_BROWSER_TESTS=require")
	}
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_RCA_V2_PROJECTS", storage.DefaultProjectID)

	db, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "seeded-regression.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var regressed atomic.Bool
	var warmupCount atomic.Int64
	var checkoutCount atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		switch r.URL.Path {
		case "/warmup":
			if warmupCount.Add(1)%2 == 0 {
				time.Sleep(950 * time.Millisecond)
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, "<!doctype html><html><head><style>body{min-height:16px;margin:0}</style><title>warmup</title></head><body></body></html>")
		case "/park":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, "<!doctype html><html><head><style>body{min-height:16px;margin:0}</style><title>park</title></head><body></body></html>")
		case "/checkout":
			seq := int(checkoutCount.Add(1))
			delay := time.Duration(seq%5) * 8 * time.Millisecond
			if regressed.Load() {
				delay += 180 * time.Millisecond
			}
			time.Sleep(delay)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, "<!doctype html><html><head><title>Checkout %d</title></head><body><h1>Order %d confirmed</h1><p>Payment processed.</p></body></html>", seq, seq)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(target.Close)

	server := NewServer(ServerConfig{Store: db, MaxConcurrent: 4, Addr: "127.0.0.1:0"})
	t.Cleanup(func() {
		if !browserprotocol.ShutdownPool(10 * time.Second) {
			t.Error("native browser pool did not close cleanly")
		}
	})
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	})

	policy := BrowserPolicyDefinition{
		Version:        analysisPolicyVersion,
		ApplyByDefault: true,
		Metrics: []BrowserPolicyMetric{
			{Metric: "lcp", Good: 110, Poor: 160, Weight: 3},
			{Metric: "fcp", Good: 110, Poor: 160, Weight: 2},
			{Metric: "cls", Good: 0.1, Poor: 0.25, Weight: 1},
		},
		Baseline: BrowserPolicyBaseline{Mode: "latest_compatible"},
	}
	policyPin := saveBrowserPolicy(t, db, "browser", 1, policy)

	yes := true
	const scriptID = "browser-seeded-regression-script"
	server.runner.StoreScript(&dsl.Script{
		ID:        scriptID,
		Name:      "Storefront Checkout Browser Flow",
		Protocol:  "browser",
		Version:   "1.0.0",
		ProjectID: storage.DefaultProjectID,
		Config: dsl.ScriptConfig{
			BaseURL: target.URL,
			Browser: dsl.BrowserConfig{
				Engine:                 "chromium",
				Headless:               &yes,
				CaptureDocumentMetrics: &yes,
			},
		},
		Actions: []dsl.Action{
			{
				Name:           "warmup_settle",
				Type:           "browser_navigate",
				Target:         "/warmup",
				ThinkTimeAfter: true,
				ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 30200},
			},
			{
				Name: "steady_checkout_loop",
				Type: "control_loop",
				Control: &dsl.ControlFlow{
					Count: 33,
					Steps: []dsl.Action{
						{
							Name:           "checkout_navigation",
							Type:           "browser_navigate",
							Target:         "/checkout?id={{fn.uuid()}}",
							ThinkTimeAfter: true,
							ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 1750},
						},
					},
				},
			},
			{
				Name:           "park_steady",
				Type:           "browser_navigate",
				Target:         "/park",
				ThinkTimeAfter: true,
				ThinkTime:      dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 30000},
			},
		},
	})

	scenBody, err := json.Marshal(map[string]any{
		"name":     "Storefront Checkout Steady Scenario",
		"duration": "98s",
		"groups": []map[string]any{
			{"name": "Storefront Checkout", "script_id": scriptID, "vusers": 2, "ramp_up": "0s"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	scenRec := httptest.NewRecorder()
	server.mux.ServeHTTP(scenRec, httptest.NewRequest(http.MethodPost, "/api/v1/scenarios", bytes.NewReader(scenBody)))
	if scenRec.Code != http.StatusCreated {
		t.Fatalf("create scenario: %d %s", scenRec.Code, scenRec.Body.String())
	}
	var createdScenario struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(scenRec.Body.Bytes(), &createdScenario); err != nil || createdScenario.ID == "" {
		t.Fatalf("decode scenario: %v %s", err, scenRec.Body.String())
	}

	runManagedBrowserTest := func(t *testing.T, name string) (*storage.TestRecord, *storage.RunArchiveRecord, *RunArchiveManifest) {
		t.Helper()
		createBody, err := json.Marshal(CreateTestRequest{
			Name:       name,
			ProjectID:  storage.DefaultProjectID,
			ScenarioID: createdScenario.ID,
			ScriptID:   scriptID,
			VUsers:     2,
			Duration:   "98s",
			RampUp:     "0s",
			AnalysisPolicySelections: &RunAnalysisPolicySelections{
				Browser: &RunAnalysisPolicySelection{ID: policyPin.ID, Revision: policyPin.Revision},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		createRec := httptest.NewRecorder()
		server.mux.ServeHTTP(createRec, httptest.NewRequest(http.MethodPost, "/api/v1/tests", bytes.NewReader(createBody)))
		if createRec.Code != http.StatusCreated {
			t.Fatalf("create test %s: %d %s", name, createRec.Code, createRec.Body.String())
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil || created.ID == "" {
			t.Fatalf("decode created test %s: %v %s", name, err, createRec.Body.String())
		}

		startRec := httptest.NewRecorder()
		server.mux.ServeHTTP(startRec, httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+created.ID+"/start", nil))
		if startRec.Code != http.StatusOK {
			t.Fatalf("start test %s (%s): %d %s", name, created.ID, startRec.Code, startRec.Body.String())
		}

		deadline := time.Now().Add(3 * time.Minute)
		var row *storage.TestRecord
		var record *storage.RunArchiveRecord
		for time.Now().Before(deadline) {
			row, err = db.GetTest(created.ID)
			if err == nil && row != nil && isTerminalTestStatus(row.Status) && !server.rcaArchiveFinalizing(created.ID) {
				if rec, ok := server.archiveDomainService().GetRunArchiveContext(context.Background(), created.ID); ok && rec != nil && strings.TrimSpace(rec.ManifestSHA256) != "" {
					record = rec
					break
				}
			}
			time.Sleep(250 * time.Millisecond)
		}
		if row == nil || row.Status != "COMPLETE" || record == nil {
			t.Fatalf("run %s (%s) did not complete with settled archive: row=%+v record=%+v", name, created.ID, row, record)
		}
		for {
			ran, evalErr := server.runNextEvaluationJob(context.Background())
			if evalErr != nil {
				t.Fatalf("drain evaluation job for %s: %v", name, evalErr)
			}
			if !ran {
				break
			}
		}
		manifest, err := loadRunArchiveManifestFromRecord(record)
		if err != nil {
			t.Fatalf("load manifest for %s: %v", name, err)
		}
		return row, record, manifest
	}

	// 1. Run qualified baseline managed Chromium run (no seeded slow path).
	regressed.Store(false)
	warmupCount.Store(0)
	checkoutCount.Store(0)
	baselineRow, baselineRecord, baselineManifest := runManagedBrowserTest(t, "Baseline Storefront Run")

	baselineInput, err := browserSelectionDocumentInput(context.Background(), baselineRecord, baselineManifest)
	if err != nil {
		t.Fatalf("baseline document input: %v", err)
	}
	if len(baselineInput.Levels) == 0 {
		t.Fatalf("expected steady load level on baseline run, got none")
	}
	steadySpan := baselineInput.Levels[0].End.Sub(baselineInput.Levels[0].Start)
	if steadySpan < 60*time.Second {
		t.Fatalf("expected >= 60s post-settle steady window on baseline run, got %v (%+v)", steadySpan, baselineInput.Levels[0])
	}
	for _, s := range baselineInput.Series {
		if s.Kind != rca.SeriesBrowserDocument || s.Document == nil {
			continue
		}
		if s.Document.ClockQualification != rca.BrowserEvidenceQualified ||
			s.Document.SourceCoverageQualification != rca.BrowserEvidenceQualified ||
			s.Document.TimelineQualification != rca.BrowserEvidenceQualified {
			t.Fatalf("baseline series %s missing qualification: %+v", s.ID, s.Document)
		}
		validBuckets := 0
		for _, pt := range s.Points {
			if pt.Valid && !pt.At.Before(baselineInput.Levels[0].Start) && pt.At.Before(baselineInput.Levels[0].End) {
				validBuckets++
			}
		}
		if validBuckets < rca.DefaultBrowserBaselineParams.MinBaselineBuckets {
			t.Fatalf("baseline series %s has %d valid post-settle buckets, want >= %d", s.ID, validBuckets, rca.DefaultBrowserBaselineParams.MinBaselineBuckets)
		}
	}

	// Ensure distinct start/completion timestamps before launching the candidate run.
	time.Sleep(1100 * time.Millisecond)

	// 2. Run regressed candidate managed Chromium run (+180ms seeded delay on /checkout).
	regressed.Store(true)
	warmupCount.Store(0)
	checkoutCount.Store(0)
	candidateRow, candidateRecord, candidateManifest := runManagedBrowserTest(t, "Regressed Candidate Storefront Run")

	// 3. Queue RCA analysis on candidate run through the production HTTP endpoint (latest_compatible).
	rcaReply := timeslotRequest(t, server, http.MethodPost, "/api/v1/tests/"+candidateRow.ID+"/archive/root-cause/analyses", map[string]any{})
	if rcaReply.Code != http.StatusAccepted && rcaReply.Code != http.StatusOK {
		t.Fatalf("queue candidate RCA analysis: %d %s", rcaReply.Code, rcaReply.Body.String())
	}
	queuedRCA := decodeRCAResponse(t, rcaReply.Body.Bytes())
	rcaRow, err := db.GetRCAAnalysis(storage.DefaultProjectID, candidateRow.ID, queuedRCA.AnalysisID)
	if err != nil {
		t.Fatal(err)
	}
	frozenPayload := rcaAnalysisPayloadOf(rcaRow)
	if frozenPayload.BrowserSelection == nil ||
		frozenPayload.BrowserSelection.Status != "selected" ||
		frozenPayload.BrowserSelection.SelectedTestID != baselineRow.ID ||
		frozenPayload.BrowserSelection.Policy != "browser@1" ||
		frozenPayload.Inputs.BrowserSelection == "" ||
		validateRCABrowserSelection(frozenPayload) != nil {
		t.Fatalf("unexpected frozen browser selection receipt on candidate run: %+v", frozenPayload)
	}

	// Run the async RCA worker to completion (which also auto-queues browser_findings.v2).
	for {
		ran, rcaErr := server.runNextRCAAnalysis(context.Background(), nil)
		if rcaErr != nil {
			t.Fatalf("runNextRCAAnalysis: %v", rcaErr)
		}
		if !ran {
			break
		}
	}
	// Run the async evaluation worker triggered by RCA completion.
	for {
		ran, evalErr := server.runNextEvaluationJob(context.Background())
		if evalErr != nil {
			t.Fatalf("runNextEvaluationJob after RCA: %v", evalErr)
		}
		if !ran {
			break
		}
	}

	assessmentReply := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/"+candidateRow.ID+"/archive/root-cause/v2", nil)
	if assessmentReply.Code != http.StatusOK {
		t.Fatalf("get candidate RCA: %d %s", assessmentReply.Code, assessmentReply.Body.String())
	}
	completedRCA := decodeRCAResponse(t, assessmentReply.Body.Bytes())
	if completedRCA.State != storage.RCAStateCompleted || completedRCA.Assessment == nil || completedRCA.Assessment.BrowserBaseline == nil {
		t.Fatalf("expected completed RCA assessment with BrowserBaseline, got %+v", completedRCA)
	}
	bb := completedRCA.Assessment.BrowserBaseline
	if bb.Status != rca.DetectionComplete || bb.Policy != "browser@1" || bb.Baseline.TestID != baselineRow.ID || bb.Baseline.Qualification != rca.BaselineQualified {
		t.Fatalf("unexpected BrowserBaseline header: %+v", bb)
	}
	anomalyMetrics := map[string]rca.BrowserBaselineResult{}
	for _, res := range bb.Results {
		if res.State == rca.BrowserBaselineAnomaly && res.Direction == rca.DirectionIncrease {
			anomalyMetrics[res.Metric] = res
		}
	}
	for _, metric := range []string{"lcp", "fcp"} {
		res, ok := anomalyMetrics[metric]
		if !ok || res.BaselineBuckets < rca.DefaultBrowserBaselineParams.MinBaselineBuckets || res.Effect < res.EffectFloor || res.Z < rca.DefaultBrowserBaselineParams.Sigma {
			t.Fatalf("expected %s anomaly result with >= %d baseline buckets, got %+v (all=%+v)", metric, rca.DefaultBrowserBaselineParams.MinBaselineBuckets, res, bb.Results)
		}
	}

	// Verify browser_findings.v2 output and full selection/baseline provenance chain.
	findingsEnv := getRunBrowserFindings(t, server, candidateRow.ID)
	if findingsEnv.Status != "ready" || findingsEnv.Result == nil || findingsEnv.Result.RuleSet != browserFindingsRuleSet {
		t.Fatalf("expected ready browser_findings.v2 result, got %+v", findingsEnv)
	}
	for _, metric := range []string{"lcp", "fcp"} {
		matched := e1Findings(findingsEnv.Result, browserFindingRuleThreshold, metric)
		if len(matched) != 1 {
			t.Fatalf("expected 1 finding for %s, got %+v", metric, matched)
		}
		f := matched[0]
		if f.State != "active" || f.Baseline.Status != "regressed" ||
			f.Baseline.AnalysisID != completedRCA.AnalysisID ||
			f.Baseline.BaselineRunID != baselineRow.ID ||
			f.Baseline.BaselineArchiveSHA256 != frozenPayload.Inputs.Baseline ||
			f.Baseline.SelectionReceiptSHA256 != frozenPayload.Inputs.BrowserSelection ||
			f.Baseline.SelectionStatus != "selected" ||
			len(f.Baseline.PolicyPins) != 1 || f.Baseline.PolicyPins[0].SHA256 != policyPin.SHA256 {
			t.Fatalf("unexpected %s finding baseline provenance: %+v", metric, f)
		}
	}

	// Remove candidateRun from the COMPLETE inventory before running the 5 refusal
	// subtests so baselineRow.ID is the sole candidate in latest_compatible.
	if err := db.DeleteTest(candidateRow.ID); err != nil {
		t.Fatalf("remove candidate run before refusal subtests: %v", err)
	}

	cloneCandidateArchive := func(t *testing.T, id string, mutate func(root string, manifest *RunArchiveManifest, row *storage.TestRecord)) (*storage.TestRecord, *storage.RunArchiveRecord) {
		t.Helper()
		root := filepath.Join(t.TempDir(), id)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(candidateRecord.ArchivePath)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			data, readErr := os.ReadFile(filepath.Join(candidateRecord.ArchivePath, entry.Name()))
			if readErr != nil {
				t.Fatal(readErr)
			}
			data = bytes.ReplaceAll(data, []byte(candidateRow.ID), []byte(id))
			if writeErr := os.WriteFile(filepath.Join(root, entry.Name()), data, 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		manifestCopy := *candidateManifest
		provCopy := candidateManifest.Provenance
		provCopy.Groups = slices.Clone(candidateManifest.Provenance.Groups)
		provCopy.PolicyPins = cloneRunPolicyPins(candidateManifest.Provenance.PolicyPins)
		if candidateManifest.Provenance.BrowserAuthoredSources != nil {
			provCopy.BrowserAuthoredSources = make(map[string]protocol.BrowserAuthoredDefinition, len(candidateManifest.Provenance.BrowserAuthoredSources))
			for k, v := range candidateManifest.Provenance.BrowserAuthoredSources {
				provCopy.BrowserAuthoredSources[k] = v
			}
		}
		manifestCopy.Provenance = provCopy
		if candidateManifest.InitialProvenance != nil {
			initCopy := *candidateManifest.InitialProvenance
			initCopy.Groups = slices.Clone(candidateManifest.InitialProvenance.Groups)
			initCopy.PolicyPins = cloneRunPolicyPins(candidateManifest.InitialProvenance.PolicyPins)
			if candidateManifest.InitialProvenance.BrowserAuthoredSources != nil {
				initCopy.BrowserAuthoredSources = make(map[string]protocol.BrowserAuthoredDefinition, len(candidateManifest.InitialProvenance.BrowserAuthoredSources))
				for k, v := range candidateManifest.InitialProvenance.BrowserAuthoredSources {
					initCopy.BrowserAuthoredSources[k] = v
				}
			}
			manifestCopy.InitialProvenance = &initCopy
		}

		manifestCopy.TestID = id

		rowCopy := *candidateRow
		rowCopy.ID = id

		mutate(root, &manifestCopy, &rowCopy)

		// Refresh artifact digests and sizes in the manifest after mutation.
		var refreshedArtifacts []RunArchiveArtifact
		for _, art := range manifestCopy.Artifacts {
			artPath := filepath.Join(root, art.Name)
			data, readErr := os.ReadFile(artPath)
			if readErr != nil {
				continue
			}
			art.SizeBytes = int64(len(data))
			art.SHA256 = sha256BytesHex(data)
			refreshedArtifacts = append(refreshedArtifacts, art)
			switch art.Name {
			case manifestCopy.BrowserDocumentsFile:
				manifestCopy.BrowserDocumentsSHA256 = art.SHA256
			case manifestCopy.BrowserReceiptsFile:
				manifestCopy.BrowserReceiptsSHA256 = art.SHA256
			}
		}
		manifestCopy.Artifacts = refreshedArtifacts

		manifestBytes, err := json.MarshalIndent(&manifestCopy, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		manifestPath := filepath.Join(root, runArchiveManifestFileName)
		if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		rec := *candidateRecord
		rec.TestID = id
		rec.ArchivePath = root
		rec.ManifestPath = manifestPath
		rec.ManifestSHA256 = sha256BytesHex(manifestBytes)
		if !browserSelectionArchiveIdentity(&rowCopy, &rec, &manifestCopy, db) {
			t.Fatalf("cloned archive identity check failed for %s", id)
		}
		if err := db.SaveTest(&rowCopy); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveRunArchive(&rec); err != nil {
			t.Fatal(err)
		}
		server.runner.mu.Lock()
		server.runner.tests[id] = &ManagedTest{
			ID:            id,
			Name:          rowCopy.Name,
			ProjectID:     rowCopy.ProjectID,
			ApplicationID: rowCopy.ApplicationID,
			ScenarioID:    rowCopy.ScenarioID,
			ScriptID:      scriptID,
			Status:        "COMPLETE",
			CreatedAt:     rowCopy.StartTime,
			EndedAt:       rowCopy.EndTime,
			Archive:       &rec,
		}
		server.runner.mu.Unlock()
		t.Cleanup(func() {
			server.runner.mu.Lock()
			delete(server.runner.tests, id)
			server.runner.mu.Unlock()
			_ = db.DeleteTest(id)
		})
		return &rowCopy, &rec
	}

	runRCAAndFindings := func(t *testing.T, runID string, body map[string]any) (rcaAnalysisPayload, RCAAnalysisResponse, *BrowserIntelligenceResult) {
		t.Helper()
		reply := timeslotRequest(t, server, http.MethodPost, "/api/v1/tests/"+runID+"/archive/root-cause/analyses", body)
		if reply.Code != http.StatusAccepted && reply.Code != http.StatusOK {
			t.Fatalf("queue RCA for %s: %d %s", runID, reply.Code, reply.Body.String())
		}
		queued := decodeRCAResponse(t, reply.Body.Bytes())
		for {
			ran, rcaErr := server.runNextRCAAnalysis(context.Background(), nil)
			if rcaErr != nil {
				t.Fatalf("runNextRCAAnalysis for %s: %v", runID, rcaErr)
			}
			if !ran {
				break
			}
		}
		for {
			ran, evalErr := server.runNextEvaluationJob(context.Background())
			if evalErr != nil {
				t.Fatalf("runNextEvaluationJob for %s: %v", runID, evalErr)
			}
			if !ran {
				break
			}
		}
		row, err := db.GetRCAAnalysis(storage.DefaultProjectID, runID, queued.AnalysisID)
		if err != nil {
			t.Fatal(err)
		}
		getReply := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/"+runID+"/archive/root-cause/analyses/"+queued.AnalysisID, nil)
		if getReply.Code != http.StatusOK {
			t.Fatalf("get RCA analysis for %s: %d %s", runID, getReply.Code, getReply.Body.String())
		}
		findings := getRunBrowserFindings(t, server, runID)
		if findings.Status != "ready" || findings.Result == nil {
			t.Fatalf("expected ready findings for %s, got %+v", runID, findings)
		}
		return rcaAnalysisPayloadOf(row), decodeRCAResponse(t, getReply.Body.Bytes()), findings.Result
	}

	// Refusal 1: Pause splits steady load so no >= 60s post-settle steady window qualifies.
	t.Run("refusal_pause", func(t *testing.T) {
		_, _ = cloneCandidateArchive(t, "refusal-pause-run", func(root string, manifest *RunArchiveManifest, _ *storage.TestRecord) {
			summariesPath := filepath.Join(root, manifest.IntervalSummariesFile)
			raw, err := os.ReadFile(summariesPath)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			var out bytes.Buffer
			mid := len(lines) / 2
			for i, line := range lines {
				var rec IntervalSummaryRecord
				if err := json.Unmarshal([]byte(line), &rec); err != nil {
					t.Fatal(err)
				}
				if i == mid || i == mid+1 {
					rec.ActiveVUsers = 0
				}
				encoded, _ := json.Marshal(rec)
				out.Write(append(encoded, '\n'))
			}
			if err := os.WriteFile(summariesPath, out.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
		})
		payload, completed, findings := runRCAAndFindings(t, "refusal-pause-run", map[string]any{})
		if payload.BrowserSelection == nil || payload.BrowserSelection.Status != "current_unqualified" || !slices.Contains(payload.BrowserSelection.Reasons, rca.BrowserBaselineLevelsUnavailable) {
			t.Fatalf("expected pause refusal current_unqualified/load_levels_unavailable, got %+v", payload.BrowserSelection)
		}
		if completed.Assessment == nil || completed.Assessment.BrowserBaseline == nil ||
			completed.Assessment.BrowserBaseline.Status != rca.DetectionUnavailable ||
			len(completed.Assessment.BrowserBaseline.Limitations) == 0 ||
			completed.Assessment.BrowserBaseline.Limitations[0].Code != rca.BrowserBaselineLevelsUnavailable {
			t.Fatalf("expected BrowserBaseline load_levels_unavailable, got %+v", completed.Assessment.BrowserBaseline)
		}
		lcp := e1Findings(findings, browserFindingRuleThreshold, "lcp")
		if len(lcp) != 1 || lcp[0].Baseline.Status != "unqualified" || lcp[0].Baseline.Reason != rca.BrowserBaselineLevelsUnavailable || lcp[0].Baseline.SelectionStatus != "current_unqualified" {
			t.Fatalf("expected pause finding baseline unqualified/load_levels_unavailable, got %+v", lcp)
		}
	})

	// Refusal 2: Lost source (dropped document observations / incomplete coverage).
	t.Run("refusal_lost_source", func(t *testing.T) {
		_, _ = cloneCandidateArchive(t, "refusal-lost-source-run", func(root string, manifest *RunArchiveManifest, _ *storage.TestRecord) {
			docsPath := filepath.Join(root, manifest.BrowserDocumentsFile)
			raw, err := os.ReadFile(docsPath)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			var out bytes.Buffer
			for i, line := range lines {
				ev, decErr := protocol.DecodeBrowserDocumentEvidence([]byte(line))
				if decErr != nil {
					t.Fatal(decErr)
				}
				if i == 0 {
					ev.DroppedObservations = 5
					ev.Incomplete = true
				}
				packed := protocol.PackBrowserDocumentEvidence(ev)
				encoded, _ := json.Marshal(packed)
				out.Write(append(encoded, '\n'))
			}
			if err := os.WriteFile(docsPath, out.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			manifest.BrowserDocumentCoverageIncomplete = true
		})
		payload, _, _ := runRCAAndFindings(t, "refusal-lost-source-run", map[string]any{})
		if payload.BrowserSelection == nil || payload.BrowserSelection.Status != "current_unqualified" ||
			!slices.Contains(payload.BrowserSelection.Reasons, "current_document_evidence_incomplete") ||
			!slices.Contains(payload.BrowserSelection.Reasons, rca.BrowserBaselineSourceLoss) {
			t.Fatalf("expected lost-source selection refusal current_unqualified/source_loss, got %+v", payload.BrowserSelection)
		}
		_, explicitRCA, explicitFindings := runRCAAndFindings(t, "refusal-lost-source-run", map[string]any{"baseline_test_id": baselineRow.ID})
		for _, res := range explicitRCA.Assessment.BrowserBaseline.Results {
			if res.State != rca.BrowserBaselineUnscored || res.Reason != rca.BrowserBaselineSourceLoss {
				t.Fatalf("expected unscored source_loss when comparing against qualified baseline, got %+v", res)
			}
		}
		lcp := e1Findings(explicitFindings, browserFindingRuleThreshold, "lcp")
		if len(lcp) != 1 || lcp[0].Baseline.Status != "unqualified" || lcp[0].Baseline.Reason != rca.BrowserBaselineSourceLoss {
			t.Fatalf("expected lost-source finding unqualified/source_loss, got %+v", lcp)
		}
	})

	// Refusal 3: Changed script (authored definition digest differs from the qualified baseline archive).
	t.Run("refusal_changed_script", func(t *testing.T) {
		_, _ = cloneCandidateArchive(t, "refusal-changed-script-run", func(_ string, manifest *RunArchiveManifest, _ *storage.TestRecord) {
			for group, def := range manifest.Provenance.BrowserAuthoredSources {
				def.ScriptSHA256 = strings.Repeat("9", 64)
				def.StructureSHA256 = strings.Repeat("8", 64)
				manifest.Provenance.BrowserAuthoredSources[group] = def
			}
			if manifest.InitialProvenance != nil {
				for group, def := range manifest.InitialProvenance.BrowserAuthoredSources {
					def.ScriptSHA256 = strings.Repeat("9", 64)
					def.StructureSHA256 = strings.Repeat("8", 64)
					manifest.InitialProvenance.BrowserAuthoredSources[group] = def
				}
			}
		})
		payload, _, findings := runRCAAndFindings(t, "refusal-changed-script-run", map[string]any{})
		if payload.BrowserSelection == nil || payload.BrowserSelection.Status != "no_compatible_candidate" ||
			len(payload.BrowserSelection.Rejected) != 1 ||
			payload.BrowserSelection.Rejected[0].TestID != baselineRow.ID ||
			!slices.Contains(payload.BrowserSelection.Rejected[0].Reasons, rca.BrowserBaselineCohortChanged) {
			t.Fatalf("expected changed-script selection rejection cohort_changed on baseline %s, got %+v", baselineRow.ID, payload.BrowserSelection)
		}
		lcp := e1Findings(findings, browserFindingRuleThreshold, "lcp")
		if len(lcp) != 1 || lcp[0].Baseline.Status != "not_selected" || lcp[0].Baseline.SelectionStatus != "no_compatible_candidate" {
			t.Fatalf("expected changed-script finding not_selected, got %+v", lcp)
		}
		_, explicitRCA, _ := runRCAAndFindings(t, "refusal-changed-script-run", map[string]any{"baseline_test_id": baselineRow.ID})
		for _, res := range explicitRCA.Assessment.BrowserBaseline.Results {
			if res.State != rca.BrowserBaselineUnscored || res.Reason != rca.BrowserBaselineCohortChanged || !strings.Contains(res.Detail, "different or unrecorded script") {
				t.Fatalf("expected unscored cohort_changed (script changed) against baseline, got %+v", res)
			}
		}
	})

	// Refusal 4: Incompatible runtime cohort (same family, different viewport in runtime cohort).
	t.Run("refusal_incompatible_cohort", func(t *testing.T) {
		_, _ = cloneCandidateArchive(t, "refusal-incompatible-cohort-run", func(root string, manifest *RunArchiveManifest, _ *storage.TestRecord) {
			docsPath := filepath.Join(root, manifest.BrowserDocumentsFile)
			raw, err := os.ReadFile(docsPath)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			var out bytes.Buffer
			for _, line := range lines {
				ev, decErr := protocol.DecodeBrowserDocumentEvidence([]byte(line))
				if decErr != nil {
					t.Fatal(decErr)
				}
				for i := range ev.Observations {
					if ev.Observations[i].Cohort != nil {
						ev.Observations[i].Cohort.ViewportWidth = 1920
					}
				}
				packed := protocol.PackBrowserDocumentEvidence(ev)
				encoded, _ := json.Marshal(packed)
				out.Write(append(encoded, '\n'))
			}
			if err := os.WriteFile(docsPath, out.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
		})
		payload, _, findings := runRCAAndFindings(t, "refusal-incompatible-cohort-run", map[string]any{})
		if payload.BrowserSelection == nil || payload.BrowserSelection.Status != "no_compatible_candidate" ||
			len(payload.BrowserSelection.Rejected) != 1 ||
			payload.BrowserSelection.Rejected[0].TestID != baselineRow.ID ||
			!slices.Contains(payload.BrowserSelection.Rejected[0].Reasons, rca.BrowserBaselineCohortChanged) {
			t.Fatalf("expected incompatible-cohort selection rejection cohort_changed on baseline %s, got %+v", baselineRow.ID, payload.BrowserSelection)
		}
		lcp := e1Findings(findings, browserFindingRuleThreshold, "lcp")
		if len(lcp) != 1 || lcp[0].Baseline.Status != "not_selected" || lcp[0].Baseline.Reason != rca.BrowserBaselineCohortChanged {
			t.Fatalf("expected incompatible-cohort finding not_selected/cohort_changed, got %+v", lcp)
		}
		_, explicitRCA, _ := runRCAAndFindings(t, "refusal-incompatible-cohort-run", map[string]any{"baseline_test_id": baselineRow.ID})
		for _, res := range explicitRCA.Assessment.BrowserBaseline.Results {
			if res.State != rca.BrowserBaselineUnscored || res.Reason != rca.BrowserBaselineCohortChanged || !strings.Contains(res.Detail, "viewport") {
				t.Fatalf("expected unscored cohort_changed naming viewport against baseline, got %+v", res)
			}
		}
	})

	// Refusal 5: Missing metric (candidate observes a metric/tuple absent from the qualified baseline archive).
	t.Run("refusal_missing_metric", func(t *testing.T) {
		baselineDocsPath := filepath.Join(baselineRecord.ArchivePath, baselineManifest.BrowserDocumentsFile)
		origBaselineDocs, err := os.ReadFile(baselineDocsPath)
		if err != nil {
			t.Fatal(err)
		}
		origBaselineManifestBytes, err := os.ReadFile(baselineRecord.ManifestPath)
		if err != nil {
			t.Fatal(err)
		}
		origBaselineRecord := *baselineRecord
		syncBaselineCache := func(rec *storage.RunArchiveRecord) {
			server.runner.mu.Lock()
			if server.runner.archives != nil {
				copied := *rec
				server.runner.archives[baselineRow.ID] = &copied
			}
			if test := server.runner.tests[baselineRow.ID]; test != nil {
				copied := *rec
				test.Archive = &copied
			}
			server.runner.mu.Unlock()
			server.runner.archiveReadCaches().forgetRunArchiveCache(baselineRow.ID)
		}
		t.Cleanup(func() {
			_ = os.WriteFile(baselineDocsPath, origBaselineDocs, 0o600)
			_ = os.WriteFile(baselineRecord.ManifestPath, origBaselineManifestBytes, 0o600)
			*baselineRecord = origBaselineRecord
			_ = db.SaveRunArchive(baselineRecord)
			syncBaselineCache(baselineRecord)
		})

		lines := strings.Split(strings.TrimSpace(string(origBaselineDocs)), "\n")
		var out bytes.Buffer
		unavailableFCP := 0
		for _, line := range lines {
			ev, decErr := protocol.DecodeBrowserDocumentEvidence([]byte(line))
			if decErr != nil {
				t.Fatal(decErr)
			}
			for i := range ev.Observations {
				if ev.Observations[i].Metric == "fcp" {
					ev.Observations[i].Available = false
					ev.Observations[i].Value = nil
					ev.Observations[i].UnavailableReason = "no_observation"
					ev.Observations[i].Qualified = false
					unavailableFCP++
				}
			}
			packed := protocol.PackBrowserDocumentEvidence(ev)
			if packed == nil {
				t.Fatal("PackBrowserDocumentEvidence returned nil after fcp mutation")
			}
			encoded, _ := json.Marshal(packed)
			out.Write(append(encoded, '\n'))
		}
		if unavailableFCP == 0 {
			t.Fatal("expected to mark fcp observations unavailable in baseline archive")
		}
		if err := os.WriteFile(baselineDocsPath, out.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		mutatedBaselineManifest := *baselineManifest
		mutatedBaselineManifest.Artifacts = slices.Clone(baselineManifest.Artifacts)
		docsSHA := sha256BytesHex(out.Bytes())
		mutatedBaselineManifest.BrowserDocumentsSHA256 = docsSHA
		for i := range mutatedBaselineManifest.Artifacts {
			if mutatedBaselineManifest.Artifacts[i].Name == mutatedBaselineManifest.BrowserDocumentsFile {
				mutatedBaselineManifest.Artifacts[i].SizeBytes = int64(out.Len())
				mutatedBaselineManifest.Artifacts[i].SHA256 = docsSHA
			}
		}
		mutatedManifestBytes, err := json.MarshalIndent(&mutatedBaselineManifest, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(baselineRecord.ManifestPath, mutatedManifestBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		baselineRecord.ManifestSHA256 = sha256BytesHex(mutatedManifestBytes)
		if err := db.SaveRunArchive(baselineRecord); err != nil {
			t.Fatal(err)
		}
		syncBaselineCache(baselineRecord)

		_, _ = cloneCandidateArchive(t, "refusal-missing-metric-run", func(_ string, _ *RunArchiveManifest, _ *storage.TestRecord) {})
		payload, _, findings := runRCAAndFindings(t, "refusal-missing-metric-run", map[string]any{})
		if payload.BrowserSelection == nil || payload.BrowserSelection.Status != "no_compatible_candidate" ||
			len(payload.BrowserSelection.Rejected) != 1 ||
			payload.BrowserSelection.Rejected[0].TestID != baselineRow.ID ||
			!slices.Contains(payload.BrowserSelection.Rejected[0].Reasons, rca.BrowserBaselineNoBaselineCohort) {
			t.Fatalf("expected missing-metric selection rejection no_baseline_cohort on baseline %s, got %+v", baselineRow.ID, payload.BrowserSelection)
		}
		fcpFinding := e1Findings(findings, browserFindingRuleThreshold, "fcp")
		if len(fcpFinding) != 1 || fcpFinding[0].Baseline.Status != "not_selected" || fcpFinding[0].Baseline.Reason != rca.BrowserBaselineNoBaselineCohort {
			t.Fatalf("expected missing-metric fcp finding not_selected/no_baseline_cohort, got %+v", fcpFinding)
		}
		_, explicitRCA, _ := runRCAAndFindings(t, "refusal-missing-metric-run", map[string]any{"baseline_test_id": baselineRow.ID})
		foundFCPNoCohort := false
		for _, res := range explicitRCA.Assessment.BrowserBaseline.Results {
			if res.Metric == "fcp" {
				if res.State != rca.BrowserBaselineUnscored || res.Reason != rca.BrowserBaselineNoBaselineCohort {
					t.Fatalf("expected fcp result to be unscored with no_baseline_cohort, got %+v", res)
				}
				foundFCPNoCohort = true
			}
		}
		if !foundFCPNoCohort {
			t.Fatalf("expected fcp result in BrowserBaseline.Results, got %+v", explicitRCA.Assessment.BrowserBaseline.Results)
		}
	})
}
