package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/rca/signatures"
	"github.com/jasonczech/apexload/pkg/storage"
)

const rcaRunPath = "/api/v1/tests/rca-run/archive/root-cause"

func rcaTestServer(t *testing.T) (*Server, *storage.SQLiteStore) {
	t.Helper()
	server, store := baselineTestServer(t)
	exemplarArchiveFixture(t, server, store, "rca-run", nil)
	exemplarArchiveFixture(t, server, store, "rca-baseline", nil)
	t.Setenv("APEXLOAD_RCA_V2_PROJECTS", storage.DefaultProjectID)
	return server, store
}

func decodeRCAResponse(t *testing.T, body []byte) RCAAnalysisResponse {
	t.Helper()
	var response RCAAnalysisResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return response
}

func TestRCAAnalysisLifecycle(t *testing.T) {
	server, _ := rcaTestServer(t)
	// The project gate opens computation only.
	t.Setenv("APEXLOAD_RCA_V2_PROJECTS", "")
	if reply := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{}); reply.Code != http.StatusConflict {
		t.Fatalf("gate off: %d %s", reply.Code, reply.Body.String())
	}
	t.Setenv("APEXLOAD_RCA_V2_PROJECTS", storage.DefaultProjectID)
	if reply := timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/v2", nil); reply.Code != http.StatusOK || decodeRCAResponse(t, reply.Body.Bytes()).State != "none" {
		t.Fatalf("no assessment yet: %d %s", reply.Code, reply.Body.String())
	}

	created := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{})
	queued := decodeRCAResponse(t, created.Body.Bytes())
	if created.Code != http.StatusAccepted || queued.State != storage.RCAStateQueued || queued.Revision != 1 || queued.Inputs == nil || queued.Inputs.Archive == "" || queued.Inputs.EvidenceRevision != 1 || queued.Inputs.Engine != rcaEngineVersion {
		t.Fatalf("queue: %d %s", created.Code, created.Body.String())
	}
	// A repeat POST with identical frozen inputs returns the same analysis.
	repeat := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{})
	if repeat.Code != http.StatusOK || decodeRCAResponse(t, repeat.Body.Bytes()).AnalysisID != queued.AnalysisID {
		t.Fatalf("idempotent POST: %d %s", repeat.Code, repeat.Body.String())
	}

	if ran, err := server.runNextRCAAnalysis(context.Background(), nil); err != nil || !ran {
		t.Fatalf("run: %v %v", ran, err)
	}
	if ran, _ := server.runNextRCAAnalysis(context.Background(), nil); ran {
		t.Fatal("a completed analysis ran again")
	}
	reply := timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/analyses/"+queued.AnalysisID, nil)
	done := decodeRCAResponse(t, reply.Body.Bytes())
	a := done.Assessment
	if done.State != storage.RCAStateCompleted || a == nil || a.SchemaVersion != rca.AssessmentSchemaVersion || a.AnalysisID != queued.AnalysisID || a.Revision != 1 ||
		a.Inputs != *queued.Inputs || a.Outcome != rca.OutcomeInsufficientEvidence || a.Acceptance.Status == "" || a.Validity.RulesVersion != rca.ValidityRulesVersion {
		t.Fatalf("assessment: %s", reply.Body.String())
	}
	// The fixture archives no generator samples, so validity is unknown and
	// coverage says why.
	coverage := map[string]string{}
	for _, c := range a.Coverage {
		coverage[c.Evidence] = c.Status
	}
	if a.Validity.Verdict != rca.ValidityUnknown || coverage["generator_stats"] != rca.CoverageMissing || coverage["intervals"] != rca.CoverageComplete || coverage["monitor_sources"] != rca.CoverageMissing {
		t.Fatalf("validity %s, coverage %v", a.Validity.Verdict, coverage)
	}
	latest := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/v2", nil).Body.Bytes())
	if latest.AnalysisID != queued.AnalysisID || latest.State != storage.RCAStateCompleted || latest.Assessment == nil {
		t.Fatalf("v2: %+v", latest)
	}

	// Identical frozen inputs give a byte-identical assessment.
	store := server.store.(storage.RCAStore)
	row, _ := store.GetRCAAnalysis(storage.DefaultProjectID, "rca-run", queued.AnalysisID)
	var payload rcaAnalysisPayload
	_ = json.Unmarshal(row.Payload, &payload)
	again, err := server.assessRCAAnalysis(context.Background(), row, payload)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := json.Marshal(payload.Assessment)
	second, _ := json.Marshal(again)
	if string(first) != string(second) {
		t.Fatalf("assessments differ:\n%s\n%s", first, second)
	}

	bundleReply := timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/analyses/"+queued.AnalysisID+"/bundle", nil)
	var bundle RCABundle
	if bundleReply.Code != http.StatusOK || json.Unmarshal(bundleReply.Body.Bytes(), &bundle) != nil || bundle.Schema != rcaBundleSchema || bundle.Evidence == nil || bundle.EvidenceRevision != 1 || bundle.Truncated || len(bundleReply.Body.Bytes()) > rcaMaxBundleBytes {
		t.Fatalf("bundle: %d %s", bundleReply.Code, bundleReply.Body.String())
	}

	// Saved assessments stay readable with the gate off.
	t.Setenv("APEXLOAD_RCA_V2_PROJECTS", "")
	if reply := timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/v2", nil); reply.Code != http.StatusOK || decodeRCAResponse(t, reply.Body.Bytes()).Assessment == nil {
		t.Fatalf("gate off read: %d %s", reply.Code, reply.Body.String())
	}
}

func TestRCAEnrichmentAndBaselineMakeNewAnalyses(t *testing.T) {
	server, _ := rcaTestServer(t)
	first := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{}).Body.Bytes())
	enriched := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{"enrich": true})
	second := decodeRCAResponse(t, enriched.Body.Bytes())
	if enriched.Code != http.StatusAccepted || second.Revision != 2 || second.Inputs.EvidenceRevision != 2 || second.AnalysisID == first.AnalysisID {
		t.Fatalf("enrich: %d %s", enriched.Code, enriched.Body.String())
	}
	for name, tc := range map[string]struct {
		body map[string]any
		code int
	}{
		"own baseline":     {map[string]any{"baseline_test_id": "rca-run"}, http.StatusBadRequest},
		"missing baseline": {map[string]any{"baseline_test_id": "no-such-run"}, http.StatusNotFound},
	} {
		if reply := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", tc.body); reply.Code != tc.code {
			t.Errorf("%s: %d %s", name, reply.Code, reply.Body.String())
		}
	}
	withBaseline := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{"baseline_test_id": "rca-baseline"}).Body.Bytes())
	if withBaseline.Inputs.Baseline == "" || withBaseline.Revision != 3 {
		t.Fatalf("baseline inputs: %+v", withBaseline)
	}
	for i := 0; i < 3; i++ {
		if _, err := server.runNextRCAAnalysis(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	done := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/analyses/"+withBaseline.AnalysisID, nil).Body.Bytes())
	var baseline *rca.Coverage
	for i := range done.Assessment.Coverage {
		if done.Assessment.Coverage[i].Evidence == "baseline" {
			baseline = &done.Assessment.Coverage[i]
		}
	}
	if baseline == nil || baseline.Status == rca.CoverageMissing {
		t.Fatalf("baseline coverage: %+v", done.Assessment.Coverage)
	}
}

func TestRCACancellationAndOwnerChange(t *testing.T) {
	server, _ := rcaTestServer(t)
	store := server.store.(storage.RCAStore)
	queued := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{}).Body.Bytes())
	cancelled := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses/"+queued.AnalysisID+"/cancel", nil)
	if cancelled.Code != http.StatusOK || decodeRCAResponse(t, cancelled.Body.Bytes()).State != storage.RCAStateCancelled {
		t.Fatalf("cancel: %d %s", cancelled.Code, cancelled.Body.String())
	}
	if reply := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses/"+queued.AnalysisID+"/cancel", nil); reply.Code != http.StatusConflict {
		t.Fatalf("second cancel: %d", reply.Code)
	}
	if ran, _ := server.runNextRCAAnalysis(context.Background(), nil); ran {
		t.Fatal("a cancelled analysis ran")
	}

	// A controller that claimed an analysis and stalled is overtaken once
	// its lease expires; its late result is rejected.
	retry := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{}).Body.Bytes())
	stalled, err := store.ClaimRCAAnalysis("stalled-controller", time.Now().Add(-2*rcaAnalysisLease), time.Minute)
	if err != nil || stalled == nil || stalled.ID != retry.AnalysisID {
		t.Fatalf("stalled claim: %+v %v", stalled, err)
	}
	if ran, err := server.runNextRCAAnalysis(context.Background(), nil); err != nil || !ran {
		t.Fatalf("takeover: %v %v", ran, err)
	}
	if ok, err := store.CompleteRCAAnalysisFenced(retry.AnalysisID, "stalled-controller", storage.RCAStateCompleted, []byte(`{}`), time.Now(), "", "", 0); err != nil || ok {
		t.Fatalf("stalled owner completed: %v %v", ok, err)
	}
	done := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/analyses/"+retry.AnalysisID, nil).Body.Bytes())
	if done.State != storage.RCAStateCompleted || done.Revision != retry.Revision || done.Assessment == nil {
		t.Fatalf("one outcome: %+v", done)
	}
}

// An analysis reads a frozen archive. Evidence that changed before it ran
// fails it; a finalize refresh that only rewrote adjunct sidecars and test
// notes, and so the manifest digest, is not a change.
func TestRCAAnalysisFailsWhenTheArchiveChanged(t *testing.T) {
	t.Run("evidence changed", func(t *testing.T) {
		server, _ := rcaTestServer(t)
		record := server.runner.archives["rca-run"]
		queued := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{}).Body.Bytes())
		writeArchiveSidecar(t, record, "resources.jsonl", []ResourceSample{}, func(m *RunArchiveManifest) { m.TransactionCount++ })
		server.runner.mu.Lock()
		record.ManifestSHA256 = strings.Repeat("f", 64)
		server.runner.mu.Unlock()
		if ran, err := server.runNextRCAAnalysis(context.Background(), nil); err != nil || !ran {
			t.Fatal(ran, err)
		}
		failed := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/analyses/"+queued.AnalysisID, nil).Body.Bytes())
		if failed.State != storage.RCAStateFailed || failed.Error != errRCAArchiveChanged.Error() || failed.Assessment != nil {
			t.Fatalf("changed archive: %+v", failed)
		}
	})
	t.Run("adjuncts refreshed", func(t *testing.T) {
		server, _ := rcaTestServer(t)
		record := server.runner.archives["rca-run"]
		queued := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{}).Body.Bytes())
		writeArchiveSidecar(t, record, "phase_events.jsonl", []PhaseEvent{}, func(m *RunArchiveManifest) {
			m.PhaseEventsFile, m.TestNotesFile, m.TestManifestFile = "phase_events.jsonl", "test_notes.json", "test_manifest.json"
			m.Artifacts = append(m.Artifacts, RunArchiveArtifact{Name: "phase_events.jsonl", SHA256: strings.Repeat("a", 64)})
		})
		server.runner.mu.Lock()
		record.ManifestSHA256 = strings.Repeat("e", 64)
		server.runner.mu.Unlock()
		if ran, err := server.runNextRCAAnalysis(context.Background(), nil); err != nil || !ran {
			t.Fatal(ran, err)
		}
		done := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/analyses/"+queued.AnalysisID, nil).Body.Bytes())
		if done.State != storage.RCAStateCompleted || done.Assessment == nil || done.ArchiveChanged {
			t.Fatalf("adjunct refresh: %+v", done)
		}
		// The same request after the refresh is still the same analysis.
		if repeat := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{}).Body.Bytes()); repeat.AnalysisID != queued.AnalysisID {
			t.Fatalf("adjunct refresh made new inputs: %+v", repeat)
		}
	})
}

// An assessment the budget cut short describes the controller's load when
// it ran, not the run: an identical request is analyzed again instead of
// being answered with it, and a sound assessment is reused as before.
func TestRCABudgetCutAssessmentsAreNotReused(t *testing.T) {
	server, _ := rcaTestServer(t)
	store := server.store.(storage.RCAStore)
	first := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{}).Body.Bytes())
	claimed, err := store.ClaimRCAAnalysis("owner", time.Now(), time.Minute)
	if err != nil || claimed == nil || claimed.ID != first.AnalysisID {
		t.Fatal(claimed, err)
	}
	cut := rcaAnalysisPayloadOf(claimed)
	cut.Assessment = &rca.Assessment{SchemaVersion: rca.AssessmentSchemaVersion, AnalysisID: claimed.ID, Revision: claimed.Revision, Inputs: cut.Inputs, Outcome: rca.OutcomeInsufficientEvidence,
		Limitations: []rca.Limitation{{Code: rca.LimitationBudgetTruncated, Detail: rca.BudgetTruncation{Limit: rca.BudgetWallTime, Allowed: 120000, Offered: 130000}.Detail()}}}
	body, _ := json.Marshal(cut)
	if ok, err := store.CompleteRCAAnalysisFenced(claimed.ID, "owner", storage.RCAStateCompleted, body, time.Now(), "", "", 0); err != nil || !ok {
		t.Fatal(ok, err)
	}
	again := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{})
	second := decodeRCAResponse(t, again.Body.Bytes())
	if again.Code != http.StatusAccepted || second.AnalysisID == first.AnalysisID || second.Revision != 2 || *second.Inputs != *first.Inputs {
		t.Fatalf("cut assessment reused: %d %s", again.Code, again.Body.String())
	}
	if ran, err := server.runNextRCAAnalysis(context.Background(), nil); err != nil || !ran {
		t.Fatal(ran, err)
	}
	if done := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/analyses/"+second.AnalysisID, nil).Body.Bytes()); done.State != storage.RCAStateCompleted || done.Assessment == nil || rca.CutByBudget(done.Assessment.Limitations) {
		t.Fatalf("second attempt: %+v", done)
	}
	if third := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{}).Body.Bytes()); third.AnalysisID != second.AnalysisID {
		t.Fatalf("sound assessment not reused: %+v", third)
	}
}

// An analysis whose frozen evidence revision cannot be read fails, so it is
// requested again, instead of judging without the mapping decisions and
// datasets that revision holds and being reused for it.
func TestRCAAnalysisFailsWhenItsEvidenceRevisionIsUnreadable(t *testing.T) {
	server, store := rcaTestServer(t)
	digest, err := rcaArchiveDigest(server.runner.archives["rca-run"])
	if err != nil {
		t.Fatal(err)
	}
	inputs := rca.InputDigests{Archive: digest, EvidenceRevision: 99, Engine: rcaEngineVersion, SignatureSet: signatures.Version}
	payload, _ := json.Marshal(rcaAnalysisPayload{Inputs: inputs})
	queued, _, err := store.CreateRCAAnalysis(storage.DefaultProjectID, "rca-run", rcaInputDigest(inputs, 0), payload, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ran, err := server.runNextRCAAnalysis(context.Background(), nil); err != nil || !ran {
		t.Fatal(ran, err)
	}
	failed := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/analyses/"+queued.ID, nil).Body.Bytes())
	if failed.State != storage.RCAStateFailed || !strings.Contains(failed.Error, "evidence revision") || failed.Assessment != nil {
		t.Fatalf("unreadable evidence: %+v", failed)
	}
}

// While finalize is still writing the archive's sidecars and manifest, an
// analysis request waits rather than freezing evidence that is about to change.
func TestRCAAnalysisWaitsForArchiveFinalization(t *testing.T) {
	server, store := rcaTestServer(t)
	record := server.runner.archives["rca-run"]
	if err := store.SaveArchiveFinalizationJob(&storage.ArchiveFinalizationJob{TestID: "rca-run", ProjectID: storage.DefaultProjectID, ArchiveRecord: *record, PersistArtifacts: true}); err != nil {
		t.Fatal(err)
	}
	if reply := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{}); reply.Code != http.StatusConflict || !strings.Contains(reply.Body.String(), "finalized") {
		t.Fatalf("finalizing archive: %d %s", reply.Code, reply.Body.String())
	}
	// A finalization that has failed an attempt may never succeed; the
	// request does not wait on its retries.
	job, err := store.GetArchiveFinalizationJob("rca-run")
	if err != nil || job == nil {
		t.Fatal(job, err)
	}
	if err := store.MarkArchiveFinalizationJobFailedIfToken("rca-run", job.IntentToken, errors.New("object store unavailable"), time.Now()); err != nil {
		t.Fatal(err)
	}
	stuck := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{})
	if stuck.Code != http.StatusAccepted {
		t.Fatalf("stuck finalization: %d %s", stuck.Code, stuck.Body.String())
	}
	queued := decodeRCAResponse(t, stuck.Body.Bytes())
	if err := store.DeleteArchiveFinalizationJob("rca-run"); err != nil {
		t.Fatal(err)
	}
	// Once settled, the same request is answered by the analysis already queued.
	settled := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{})
	if settled.Code != http.StatusOK || decodeRCAResponse(t, settled.Body.Bytes()).AnalysisID != queued.AnalysisID {
		t.Fatalf("settled archive: %d %s", settled.Code, settled.Body.String())
	}
}

// Finalize and every adjunct refresh after it rewrite the test manifest, the
// test notes and the archive manifest. The evidence digest an analysis
// freezes reads through those rewrites, while the whole-manifest digest moves
// with each; so an analysis requested at publication stays current, and the
// same request after a refresh is the same analysis.
func TestRCAEvidenceDigestSurvivesAdjunctRefresh(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv(envArchiveAdjunctMinFreeBytes, "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run, record := createArchiveFinalizationTest(t, store, "rca-adjuncts", root)
	runner := NewTestRunner(1, nil, store)
	digests := func(stage string) (string, string) {
		manifest, err := loadRunArchiveManifestFromRecord(record)
		if err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		evidence, err := rcaEvidenceDigest(manifest)
		if err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		whole, err := rcaManifestDigest(record)
		if err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		return evidence, whole
	}
	published, publishedManifest := digests("published")
	if err := runner.persistTestManifestArtifact(run, record); err != nil {
		t.Fatal(err)
	}
	finalized, finalizedManifest := digests("finalized")
	if err := store.CreateTestNote(&storage.TestNote{ID: "note-1", TestID: run.ID, Body: "written after the analysis was requested"}); err != nil {
		t.Fatal(err)
	}
	if err := runner.persistTestManifestArtifact(run, record); err != nil {
		t.Fatal(err)
	}
	refreshed, refreshedManifest := digests("refreshed")
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil || manifest.TestNotesFile == "" || manifest.TestManifestFile == "" {
		t.Fatalf("adjuncts not written: %v %+v", err, manifest)
	}
	if finalizedManifest == publishedManifest || refreshedManifest == finalizedManifest {
		t.Fatalf("the manifest was not rewritten: %s %s %s", publishedManifest, finalizedManifest, refreshedManifest)
	}
	if finalized != published || refreshed != published {
		t.Fatalf("the evidence digest moved with the adjuncts: published %s, finalized %s, refreshed %s", published, finalized, refreshed)
	}
}

func TestRCABundleStaysWithin256KiB(t *testing.T) {
	assessment := rca.Assessment{SchemaVersion: rca.AssessmentSchemaVersion}
	for i := 0; i < 5000; i++ {
		assessment.Validity.Windows = append(assessment.Validity.Windows, rca.ValidityWindow{Verdict: rca.ValiditySuspect, Findings: []rca.ValidityFinding{{Rule: rca.RuleGeneratorCPU, Generator: strings.Repeat("g", 40), Value: 99, Threshold: 85}}})
	}
	evidence := json.RawMessage(`{"schema":"apexload.rca_evidence.v1"}`)
	bundle := boundRCABundle(RCABundle{Schema: rcaBundleSchema, Assessment: assessment, Evidence: &evidence})
	body, _ := json.Marshal(bundle)
	if !bundle.Truncated || len(body) > rcaMaxBundleBytes || len(bundle.Assessment.Validity.Windows) == 0 || bundle.Evidence == nil {
		t.Fatalf("bundle %d bytes, %d windows, truncated %v", len(body), len(bundle.Assessment.Validity.Windows), bundle.Truncated)
	}
}

func TestRCABundleBoundsSLADetails(t *testing.T) {
	assessment := rca.Assessment{SchemaVersion: rca.AssessmentSchemaVersion}
	for i := 0; i < 5000; i++ {
		assessment.Acceptance.Details = append(assessment.Acceptance.Details, strings.Repeat("sla breached ", 8))
	}
	bundle := boundRCABundle(RCABundle{Schema: rcaBundleSchema, Assessment: assessment})
	if body, _ := json.Marshal(bundle); !bundle.Truncated || len(body) > rcaMaxBundleBytes || len(bundle.Assessment.Acceptance.Details) == 0 {
		t.Fatalf("bundle %d bytes, %d details", len(body), len(bundle.Assessment.Acceptance.Details))
	}
}

func TestRCALegacyRunIsAnalyzedInTheDefaultProject(t *testing.T) {
	server, _ := rcaTestServer(t)
	// Runs stored before project scoping carry no project.
	server.runner.mu.Lock()
	server.runner.tests["rca-run"] = &ManagedTest{ID: "rca-run", Name: "rca-run", Status: "COMPLETE"}
	server.runner.mu.Unlock()
	created := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{"baseline_test_id": "rca-baseline"})
	if created.Code != http.StatusAccepted {
		t.Fatalf("legacy run: %d %s", created.Code, created.Body.String())
	}
	if _, err := server.runNextRCAAnalysis(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if reply := timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/v2", nil); decodeRCAResponse(t, reply.Body.Bytes()).State != storage.RCAStateCompleted {
		t.Fatalf("legacy assessment: %d %s", reply.Code, reply.Body.String())
	}
}

func TestRCAFailuresAndShutdown(t *testing.T) {
	// A panicking assessment fails instead of being re-claimed after every lease.
	assessment, err := recoverRCAAssessment("a", func() (*rca.Assessment, error) { panic("boom") })
	if assessment != nil || err == nil {
		t.Fatalf("panic: %+v %v", assessment, err)
	}

	server, _ := rcaTestServer(t)
	queued := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{}).Body.Bytes())
	stopping, stop := context.WithCancel(context.Background())
	stop()
	if ran, err := server.runNextRCAAnalysis(stopping, nil); err != nil || !ran {
		t.Fatalf("interrupted run: %v %v", ran, err)
	}
	// Shutdown records nothing; the next leader re-claims the analysis.
	if state := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/analyses/"+queued.AnalysisID, nil).Body.Bytes()).State; state != storage.RCAStateRunning {
		t.Fatalf("interrupted analysis is %s", state)
	}
}

func TestRCAAutoEnrichmentAtFinalize(t *testing.T) {
	server, store := rcaTestServer(t)
	record := server.runner.archives["rca-run"]
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	test := &ManagedTest{ID: "rca-run", ProjectID: storage.DefaultProjectID}
	server.runner.autoEnrichRCAEvidence(test, record, manifest)
	if latest, _ := store.LatestRCAEvidenceRevision(storage.DefaultProjectID, "rca-run"); latest != nil {
		t.Fatal("enriched without the project gate")
	}
	t.Setenv("APEXLOAD_RCA_AUTO_ENRICH_PROJECTS", storage.DefaultProjectID)
	server.runner.autoEnrichRCAEvidence(test, record, manifest)
	latest, err := store.LatestRCAEvidenceRevision(storage.DefaultProjectID, "rca-run")
	var evidence rcaEvidence
	if err != nil || latest == nil || json.Unmarshal(latest.Payload, &evidence) != nil || !evidence.Enriched || evidence.Sidecars["exemplars"] != "absent" {
		t.Fatalf("auto enrichment: %+v %v", latest, err)
	}
	// A refresh of the same archive keeps its enrichment.
	server.runner.autoEnrichRCAEvidence(test, record, manifest)
	if again, err := store.LatestRCAEvidenceRevision(storage.DefaultProjectID, "rca-run"); err != nil || again.Revision != latest.Revision {
		t.Fatalf("refresh appended evidence: %+v %v", again, err)
	}
}

func TestRCAConcurrentFirstRequestsShareOneAnalysis(t *testing.T) {
	server, store := rcaTestServer(t)
	const requests = 6
	replies := make(chan RCAAnalysisResponse, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var response RCAAnalysisResponse
			_ = json.Unmarshal(timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{}).Body.Bytes(), &response)
			replies <- response
		}()
	}
	wg.Wait()
	close(replies)
	ids := map[string]bool{}
	for reply := range replies {
		if reply.Inputs == nil || reply.Inputs.EvidenceRevision != 1 {
			t.Fatalf("evidence not shared: %+v", reply)
		}
		ids[reply.AnalysisID] = true
	}
	if len(ids) != 1 {
		t.Fatalf("concurrent POSTs created %d analyses", len(ids))
	}
	if latest, err := store.LatestRCAEvidenceRevision(storage.DefaultProjectID, "rca-run"); err != nil || latest.Revision != 1 {
		t.Fatalf("evidence revisions: %+v %v", latest, err)
	}
}

func TestRCAWorkerRunsQueuedAnalyses(t *testing.T) {
	server, _ := rcaTestServer(t)
	server.startRCAAnalysisWorker()
	t.Cleanup(func() {
		if server.rcaCancel != nil {
			server.rcaCancel()
		}
	})
	queued := map[string]string{} // analysis ID → run
	for _, request := range []struct {
		run  string
		body map[string]any
	}{{"rca-run", map[string]any{}}, {"rca-run", map[string]any{"enrich": true}}, {"rca-baseline", map[string]any{}}} {
		reply := timeslotRequest(t, server, http.MethodPost, "/api/v1/tests/"+request.run+"/archive/root-cause/analyses", request.body)
		queued[decodeRCAResponse(t, reply.Body.Bytes()).AnalysisID] = request.run
	}
	deadline := time.Now().Add(20 * time.Second)
	for id, run := range queued {
		for {
			reply := timeslotRequest(t, server, http.MethodGet, "/api/v1/tests/"+run+"/archive/root-cause/analyses/"+id, nil)
			if state := decodeRCAResponse(t, reply.Body.Bytes()).State; state == storage.RCAStateCompleted {
				break
			} else if state == storage.RCAStateFailed || time.Now().After(deadline) {
				t.Fatalf("analysis %s: %s", id, reply.Body.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// New qualification logic gets a new input identity; historical assessments
// remain readable and unchanged rather than being relabelled as requalified.
func TestRCABrowserQualificationVersionPreservesHistoricalAssessment(t *testing.T) {
	server, store := rcaTestServer(t)
	currentVersion := rcaEngineVersion
	historicalVersion := strings.Replace(currentVersion, "apexload.rca.engine.v6+", "apexload.rca.engine.v5+", 1)
	if currentVersion == historicalVersion {
		t.Fatal("qualification change did not advance engine identity")
	}
	t.Cleanup(func() { rcaEngineVersion = currentVersion })
	rcaEngineVersion = historicalVersion
	firstReply := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{})
	if firstReply.Code != http.StatusAccepted {
		t.Fatalf("historical admission: %d", firstReply.Code)
	}
	first := decodeRCAResponse(t, firstReply.Body.Bytes())
	if ran, err := server.runNextRCAAnalysis(context.Background(), nil); err != nil || !ran {
		t.Fatal(ran, err)
	}
	before, err := store.GetRCAAnalysis(storage.DefaultProjectID, "rca-run", first.AnalysisID)
	if err != nil || before == nil || before.State != storage.RCAStateCompleted {
		t.Fatal(before, err)
	}
	rcaEngineVersion = currentVersion
	nextReply := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{})
	if nextReply.Code != http.StatusAccepted {
		t.Fatalf("new admission: %d", nextReply.Code)
	}
	next := decodeRCAResponse(t, nextReply.Body.Bytes())
	if next.AnalysisID == first.AnalysisID || next.Inputs == nil || next.Inputs.Engine != currentVersion {
		t.Fatal("new qualification reused historical identity")
	}
	historical := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/analyses/"+first.AnalysisID, nil).Body.Bytes())
	if historical.State != storage.RCAStateCompleted || historical.Inputs == nil || historical.Inputs.Engine != historicalVersion || historical.Assessment == nil {
		t.Fatal("historical assessment changed")
	}
	after, err := store.GetRCAAnalysis(storage.DefaultProjectID, "rca-run", first.AnalysisID)
	if err != nil || after == nil || string(after.Payload) != string(before.Payload) {
		t.Fatal("historical payload rewritten", err)
	}
}

func TestRCABrowserQualificationRefusesQueuedOldEngine(t *testing.T) {
	for _, oldVersion := range []string{"", "apexload.rca.engine.v3+historical"} {
		t.Run(oldVersion, func(t *testing.T) {
			server, _ := rcaTestServer(t)
			current := rcaEngineVersion
			t.Cleanup(func() { rcaEngineVersion = current })
			rcaEngineVersion = oldVersion
			reply := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{})
			if reply.Code != http.StatusAccepted {
				t.Fatalf("admission: %d", reply.Code)
			}
			queued := decodeRCAResponse(t, reply.Body.Bytes())
			rcaEngineVersion = current
			if ran, err := server.runNextRCAAnalysis(context.Background(), nil); err != nil || !ran {
				t.Fatal(ran, err)
			}
			failed := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodGet, rcaRunPath+"/analyses/"+queued.AnalysisID, nil).Body.Bytes())
			if failed.State != storage.RCAStateFailed || failed.Assessment != nil || !strings.Contains(failed.Error, "engine changed") {
				t.Fatalf("old engine evaluated: %+v", failed)
			}
			retry := decodeRCAResponse(t, timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{}).Body.Bytes())
			if retry.AnalysisID == queued.AnalysisID || retry.Inputs == nil || retry.Inputs.Engine != current {
				t.Fatal("fresh request did not freeze current engine")
			}
		})
	}
}
