package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func pinRCAAdmissionPolicy(t *testing.T, server *Server, pin RunPolicyPin) {
	t.Helper()
	record := server.runner.archives["rca-run"]
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Provenance.PolicyPins = []RunPolicyPin{pin}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record.ManifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	record.ManifestSHA256 = sha256BytesHex(raw)
}

func TestRCABrowserPinnedBaselineAdmission(t *testing.T) {
	server, store := rcaTestServer(t)
	policy := e1Policy(e1LCP)
	policy.Baseline = BrowserPolicyBaseline{Mode: "pinned_run", RunID: "rca-baseline"}
	pinRCAAdmissionPolicy(t, server, saveBrowserPolicy(t, store, "web", 1, policy))
	reply := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{})
	queued := decodeRCAResponse(t, reply.Body.Bytes())
	if reply.Code != http.StatusAccepted || queued.Inputs == nil || queued.Inputs.Baseline == "" {
		t.Fatalf("queue: %d %s", reply.Code, reply.Body)
	}
	row, err := store.GetRCAAnalysis(storage.DefaultProjectID, "rca-run", queued.AnalysisID)
	if err != nil {
		t.Fatal(err)
	}
	frozen := rcaAnalysisPayloadOf(row)
	expected, status, message := server.rcaBaselineArchive(context.Background(), "rca-run", storage.DefaultProjectID, "rca-baseline")
	if status != 0 || frozen.Request.BaselineTestID != "rca-baseline" || frozen.Inputs.Baseline != expected.Digest {
		t.Fatalf("frozen: %+v; %d %s", frozen, status, message)
	}
	// Editing the policy creates a new revision, never changes this run's pin.
	policy.Baseline.RunID = "different-run"
	saveBrowserPolicy(t, store, "web", 2, policy)
	repeat := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{})
	if repeat.Code != http.StatusOK || decodeRCAResponse(t, repeat.Body.Bytes()).AnalysisID != queued.AnalysisID {
		t.Fatalf("revision changed admission: %d %s", repeat.Code, repeat.Body)
	}
	explicit := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{"baseline_test_id": "rca-baseline"})
	if explicit.Code != http.StatusOK || decodeRCAResponse(t, explicit.Body.Bytes()).AnalysisID != queued.AnalysisID {
		t.Fatalf("equivalent explicit request: %d %s", explicit.Code, explicit.Body)
	}
	if ran, err := server.runNextRCAAnalysis(context.Background(), nil); err != nil || !ran {
		t.Fatal(ran, err)
	}
	done, err := store.GetRCAAnalysis(storage.DefaultProjectID, "rca-run", queued.AnalysisID)
	if err != nil || done.State != storage.RCAStateCompleted || rcaAnalysisPayloadOf(done).Request.BaselineTestID != "rca-baseline" {
		t.Fatalf("execution: %+v %v", done, err)
	}
}

func TestRCABrowserPinnedBaselineAdmissionGuards(t *testing.T) {
	for _, tc := range []struct {
		name, mode, run, explicit                string
		badPin, foreign, finalizing, lookupError bool
		noArchive                                bool
		status                                   int
		selected                                 string
	}{
		{name: "off", mode: "none", status: 202},
		{name: "latest remains unselected", mode: "latest_compatible", status: 202},
		{name: "missing pin revision", mode: "pinned_run", run: "rca-baseline", badPin: true, status: 202},
		{name: "self", mode: "pinned_run", run: "rca-run", status: 400},
		{name: "missing run", mode: "pinned_run", run: "missing", status: 404},
		{name: "missing archive", mode: "pinned_run", run: "rca-baseline", noArchive: true, status: 404},
		{name: "foreign project", mode: "pinned_run", run: "foreign-baseline", foreign: true, status: 404},
		{name: "finalizing", mode: "pinned_run", run: "rca-baseline", finalizing: true, status: 409},
		{name: "explicit finalizing", mode: "none", explicit: "rca-baseline", finalizing: true, status: 409},
		{name: "lookup failure", mode: "pinned_run", run: "rca-baseline", lookupError: true, status: 503},
		{name: "explicit override", mode: "pinned_run", run: "missing", explicit: "rca-baseline", status: 202, selected: "rca-baseline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store := rcaTestServer(t)
			policy := e1Policy(e1LCP)
			policy.Baseline = BrowserPolicyBaseline{Mode: tc.mode, RunID: tc.run}
			pin := saveBrowserPolicy(t, store, "web", 1, policy)
			if tc.badPin {
				pin.Revision++
			}
			pinRCAAdmissionPolicy(t, server, pin)
			if tc.foreign {
				if err := store.SaveProject(&storage.Project{ID: "other-project", Name: "Other"}); err != nil {
					t.Fatal(err)
				}
				baseline, err := store.GetTest("rca-baseline")
				if err != nil {
					t.Fatal(err)
				}
				baseline.ID = "foreign-baseline"
				baseline.ProjectID = "other-project"
				if err := store.SaveTest(baseline); err != nil {
					t.Fatal(err)
				}
			}
			if tc.noArchive {
				if err := os.Remove(server.runner.archives["rca-baseline"].ManifestPath); err != nil {
					t.Fatal(err)
				}
			}
			if tc.finalizing {
				if err := store.SaveArchiveFinalizationJob(&storage.ArchiveFinalizationJob{TestID: "rca-baseline", ProjectID: storage.DefaultProjectID, ArchiveRecord: *server.runner.archives["rca-baseline"], PersistArtifacts: true}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.lookupError {
				server.store = sqliteOverlay(journeyPolicyLookupStore{SQLiteStore: store, err: errors.New("database is locked")})
			}
			reply := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{"baseline_test_id": tc.explicit})
			if reply.Code != tc.status {
				t.Fatalf("status: %d %s", reply.Code, reply.Body)
			}
			if tc.status == http.StatusAccepted {
				queued := decodeRCAResponse(t, reply.Body.Bytes())
				row, err := store.GetRCAAnalysis(storage.DefaultProjectID, "rca-run", queued.AnalysisID)
				if err != nil {
					t.Fatal(err)
				}
				if got := rcaAnalysisPayloadOf(row).Request.BaselineTestID; got != tc.selected {
					t.Fatalf("selected %q, want %q", got, tc.selected)
				}
			}
		})
	}
}

func TestRCABrowserPinnedBaselineChangedAfterAdmission(t *testing.T) {
	server, store := rcaTestServer(t)
	policy := e1Policy(e1LCP)
	policy.Baseline = BrowserPolicyBaseline{Mode: "pinned_run", RunID: "rca-baseline"}
	pinRCAAdmissionPolicy(t, server, saveBrowserPolicy(t, store, "web", 1, policy))
	reply := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{})
	if reply.Code != http.StatusAccepted {
		t.Fatalf("queue: %d %s", reply.Code, reply.Body)
	}
	queued := decodeRCAResponse(t, reply.Body.Bytes())
	record := server.runner.archives["rca-baseline"]
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	manifest.TransactionCount++
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record.ManifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	record.ManifestSHA256 = sha256BytesHex(raw)
	if ran, err := server.runNextRCAAnalysis(context.Background(), nil); err != nil || !ran {
		t.Fatal(ran, err)
	}
	done, err := store.GetRCAAnalysis(storage.DefaultProjectID, "rca-run", queued.AnalysisID)
	if err != nil {
		t.Fatal(err)
	}
	payload := rcaAnalysisPayloadOf(done)
	if payload.Assessment == nil {
		t.Fatalf("assessment missing: %+v", payload)
	}
	for _, coverage := range payload.Assessment.Coverage {
		if coverage.Evidence == "baseline" && strings.Contains(coverage.Detail, "changed after") {
			return
		}
	}
	t.Fatalf("changed baseline used: %+v", payload.Assessment.Coverage)
}

func TestRCABrowserBaselineReferenceKeysUseCanonicalIdentity(t *testing.T) {
	server, store := rcaTestServer(t)
	ref, err := store.EnsureTestRefKey("rca-baseline", "RUN-BASELINE-ALIAS")
	if err != nil {
		t.Fatal(err)
	}
	currentRef, err := store.EnsureTestRefKey("rca-run", "RUN-CURRENT-ALIAS")
	if err != nil {
		t.Fatal(err)
	}
	if _, status, _ := server.rcaBaselineArchive(context.Background(), "rca-run", storage.DefaultProjectID, currentRef); status != http.StatusBadRequest {
		t.Fatalf("self alias status %d", status)
	}
	record := server.runner.archives["rca-baseline"]
	if err := store.SaveArchiveFinalizationJob(&storage.ArchiveFinalizationJob{TestID: "rca-baseline", ProjectID: storage.DefaultProjectID, ArchiveRecord: *record, PersistArtifacts: true}); err != nil {
		t.Fatal(err)
	}
	if _, status, _ := server.rcaBaselineArchive(context.Background(), "rca-run", storage.DefaultProjectID, ref); status != http.StatusConflict {
		t.Fatalf("finalizing alias status %d", status)
	}
	policy := e1Policy(e1LCP)
	policy.Baseline = BrowserPolicyBaseline{Mode: "pinned_run", RunID: ref}
	pinRCAAdmissionPolicy(t, server, saveBrowserPolicy(t, store, "web", 1, policy))
	reply := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{})
	if reply.Code != http.StatusConflict {
		t.Fatalf("policy alias bypassed finalization: %d %s", reply.Code, reply.Body)
	}
	if err := store.DeleteArchiveFinalizationJob("rca-baseline"); err != nil {
		t.Fatal(err)
	}
	reply = timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{"baseline_test_id": ref})
	if reply.Code != http.StatusAccepted {
		t.Fatalf("alias admission: %d %s", reply.Code, reply.Body)
	}
	queued := decodeRCAResponse(t, reply.Body.Bytes())
	row, err := store.GetRCAAnalysis(storage.DefaultProjectID, "rca-run", queued.AnalysisID)
	if err != nil {
		t.Fatal(err)
	}
	if got := rcaAnalysisPayloadOf(row).Request.BaselineTestID; got != "rca-baseline" {
		t.Fatalf("frozen alias: %q", got)
	}
	manifest, err := loadRunArchiveManifestFromRecord(server.runner.archives["rca-run"])
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := server.rcaBrowserBaselinePolicy(storage.DefaultProjectID, manifest)
	if err != nil || resolved == nil || resolved.RunID != "rca-baseline" {
		t.Fatalf("policy identity: %+v %v", resolved, err)
	}
	repeat := timeslotRequest(t, server, http.MethodPost, rcaRunPath+"/analyses", map[string]any{"baseline_test_id": "rca-baseline"})
	if repeat.Code != http.StatusOK || decodeRCAResponse(t, repeat.Body.Bytes()).AnalysisID != queued.AnalysisID {
		t.Fatalf("canonical request differs: %d %s", repeat.Code, repeat.Body)
	}
}
