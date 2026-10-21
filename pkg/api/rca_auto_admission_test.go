package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRCAAutomaticPinnedBaselineRetrySurvivesWorkerRestart(t *testing.T) {
	server, store := rcaTestServer(t)
	t.Setenv(envRCAAutoAnalyzeProjects, storage.DefaultProjectID)
	policy := e1Policy(e1LCP)
	policy.Baseline = BrowserPolicyBaseline{Mode: "pinned_run", RunID: "rca-baseline"}
	pinRCAAdmissionPolicy(t, server, saveBrowserPolicy(t, store, "web", 1, policy))
	if err := store.SaveArchiveFinalizationJob(&storage.ArchiveFinalizationJob{TestID: "rca-baseline", ProjectID: storage.DefaultProjectID, ArchiveRecord: *server.runner.archives["rca-baseline"], PersistArtifacts: true}); err != nil {
		t.Fatal(err)
	}
	server.autoQueueRCAAnalysis("rca-run")
	ctx := context.Background()
	pending, _, err := store.ListRCAAutoAdmissions(ctx, "", 4)
	if err != nil || len(pending) != 1 {
		t.Fatalf("missing durable wait: %+v %v", pending, err)
	}
	analyses, err := store.LatestRCAAnalyses(storage.DefaultProjectID, []string{"rca-run"})
	if err != nil || analyses["rca-run"].Latest != nil {
		t.Fatal("queued against finalizing baseline", err)
	}
	if err := store.DeleteArchiveFinalizationJob("rca-baseline"); err != nil {
		t.Fatal(err)
	}
	// A new worker cursor needs no in-memory dependency registration or attachment
	// callback. The separate storage test closes/reopens the database itself.
	server.reconcileRCAAutoAdmissions(ctx, "")
	analyses, err = store.LatestRCAAnalyses(storage.DefaultProjectID, []string{"rca-run"})
	latest := analyses["rca-run"].Latest
	if err != nil || latest == nil {
		t.Fatal("retry did not queue", err)
	}
	pending, _, err = store.ListRCAAutoAdmissions(ctx, "", 4)
	if err != nil || len(pending) != 0 {
		t.Fatal("successful retry not acknowledged", err)
	}
	// Simulate a crash after queue insertion but before intent acknowledgement.
	if _, err := store.EnsureRCAAutoAdmission(ctx, "rca-run"); err != nil {
		t.Fatal(err)
	}
	server.reconcileRCAAutoAdmissions(ctx, "")
	analyses, err = store.LatestRCAAnalyses(storage.DefaultProjectID, []string{"rca-run"})
	if err != nil || analyses["rca-run"].Latest.ID != latest.ID {
		t.Fatal("retry duplicated immutable analysis", err)
	}
}

func TestRCAAutomaticPendingAdmissionHonorsDisabledGate(t *testing.T) {
	server, store := rcaTestServer(t)
	ctx := context.Background()
	if _, err := store.EnsureRCAAutoAdmission(ctx, "rca-run"); err != nil {
		t.Fatal(err)
	}
	server.reconcileRCAAutoAdmissions(ctx, "")
	pending, _, err := store.ListRCAAutoAdmissions(ctx, "", 4)
	if err != nil || len(pending) != 0 {
		t.Fatal("disabled gate retained intent", err)
	}
	analyses, err := store.LatestRCAAnalyses(storage.DefaultProjectID, []string{"rca-run"})
	if err != nil || analyses["rca-run"].Latest != nil {
		t.Fatal("disabled gate queued analysis", err)
	}
}

type replacingRCAAdmissionStore struct {
	*storage.SQLiteStore
	replace func()
}

func (s *replacingRCAAdmissionStore) RCAAutoAdmissionCurrent(ctx context.Context, in storage.RCAAutoAdmission) (bool, error) {
	current, err := s.SQLiteStore.RCAAutoAdmissionCurrent(ctx, in)
	if current && err == nil && s.replace != nil {
		replace := s.replace
		s.replace = nil
		replace()
	}
	return current, err
}
func TestRCAAutomaticAdmissionFencesReplacementAfterCheck(t *testing.T) {
	server, store := rcaTestServer(t)
	t.Setenv(envRCAAutoAnalyzeProjects, storage.DefaultProjectID)
	injected := &replacingRCAAdmissionStore{SQLiteStore: store}
	injected.replace = func() {
		record, err := store.GetTest("rca-run")
		if err != nil {
			t.Fatal(err)
		}
		// SaveTest preserves initial provenance on an existing row. Replace the
		// lifecycle first, as a real same-ID replacement would.
		if err := store.DeleteTest(record.ID); err != nil {
			t.Fatal(err)
		}
		record.InitialProvenance = map[string]any{"run_incarnation": "replacement"}
		if err := store.SaveTest(record); err != nil {
			t.Fatal(err)
		}
		replaced, err := store.GetTest(record.ID)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(replaced.InitialProvenance)
		if err != nil || !strings.Contains(string(raw), `"run_incarnation":"replacement"`) {
			t.Fatalf("replacement fixture did not change incarnation: %s %v", raw, err)
		}
	}
	server.store = injected
	server.autoQueueRCAAnalysis("rca-run")
	analyses, err := store.LatestRCAAnalyses(storage.DefaultProjectID, []string{"rca-run"})
	if err != nil || analyses["rca-run"].Latest != nil {
		t.Fatal("stale intent queued replacement", err)
	}
	evidence, err := store.LatestRCAEvidenceRevision(storage.DefaultProjectID, "rca-run")
	if evidence != nil {
		t.Fatal("stale evidence persisted", evidence, err)
	}
	server.reconcileRCAAutoAdmissions(context.Background(), "")
	pending, _, err := store.ListRCAAutoAdmissions(context.Background(), "", 4)
	if err != nil || len(pending) != 0 {
		t.Fatal("superseded intent not retired", err)
	}
}

type unavailableRCAProjectStore struct {
	*storage.SQLiteStore
	unavailable bool
}

func (s *unavailableRCAProjectStore) GetProject(id string) (*storage.Project, error) {
	if s.unavailable {
		return nil, errors.New("injected settings read failure")
	}
	return s.SQLiteStore.GetProject(id)
}
func TestRCAAutomaticAdmissionRetainsIntentOnSettingsReadFailure(t *testing.T) {
	server, store := rcaTestServer(t)
	t.Setenv(envRCAV2Projects, "")
	t.Setenv(envRCAAutoAnalyzeProjects, "")
	setProjectAnalysis(t, server, storage.DefaultProjectID, storage.ProjectRootCauseSettings{Enabled: true, AutoAnalyze: true})
	injected := &unavailableRCAProjectStore{SQLiteStore: store, unavailable: true}
	server.store = injected
	server.autoQueueRCAAnalysis("rca-run")
	ctx := context.Background()
	pending, _, err := store.ListRCAAutoAdmissions(ctx, "", 4)
	if err != nil || len(pending) != 1 {
		t.Fatal("settings outage lost durable intent", err)
	}
	server.reconcileRCAAutoAdmissions(ctx, "")
	pending, _, err = store.ListRCAAutoAdmissions(ctx, "", 4)
	if err != nil || len(pending) != 1 {
		t.Fatal("retry confused outage with disabled gate", err)
	}
	injected.unavailable = false
	server.reconcileRCAAutoAdmissions(ctx, "")
	analyses, err := store.LatestRCAAnalyses(storage.DefaultProjectID, []string{"rca-run"})
	if err != nil || analyses["rca-run"].Latest == nil {
		t.Fatal("recovered settings did not admit", err)
	}
	pending, _, err = store.ListRCAAutoAdmissions(ctx, "", 4)
	if err != nil || len(pending) != 0 {
		t.Fatal("successful admission remains pending", err)
	}
}
