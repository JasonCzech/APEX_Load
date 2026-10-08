package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// interruptedRunFixture stores a running distributed run with a participant
// journal (its archive-collection intent) and lets acquisition reconcile hold
// it STOPPING, as a successor does for a fail-policy run it cannot adopt.
func interruptedRunFixture(t *testing.T, runner *TestRunner, store *storage.SQLiteStore, id string, targets ...string) *ManagedTest {
	t.Helper()
	started := time.Now().UTC().Add(-time.Minute)
	if err := store.SaveTest(&storage.TestRecord{ID: id, Name: id, Status: "RUNNING", ProjectID: storage.DefaultProjectID, StartTime: started, StartedAt: &started, Groups: []any{}}); err != nil {
		t.Fatalf("save %s: %v", id, err)
	}
	if len(targets) > 0 {
		owner, _ := runner.leadership.executionLeaseTerm()
		sources := make(map[string]string, len(targets))
		for _, target := range targets {
			sources[target] = strings.Repeat("a", 64)
		}
		if _, saved, err := store.PrepareRunArchiveCollection(id, storage.DefaultProjectID, started, sources, executionLeaseName, owner); err != nil || !saved {
			t.Fatalf("prepare participant journal = (%v, %v)", saved, err)
		}
	}
	if err := runner.reloadStoredTests(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	test := runner.tests[id]
	if test == nil || !interruptedDistributedRunLocked(test) {
		t.Fatalf("reconcile did not hold the run as interrupted: %+v", test)
	}
	return test
}

func interruptedRunBackend(runner *TestRunner, reachable map[string]bool) {
	backend := newDistributedExecutionBackend([]string{"gen-a:9091", "gen-b:9091"}, 200*time.Millisecond, func(_ context.Context, target string) (generatorClient, error) {
		if reachable[target] {
			return &adoptionFakeClient{}, nil
		}
		return nil, errors.New("generator unreachable")
	})
	runner.execution = backend
}

func storedStatus(t *testing.T, store *storage.SQLiteStore, id string) *storage.TestRecord {
	t.Helper()
	record, err := store.GetTest(id)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// A run reconcile could not adopt used to stay STOPPING forever, holding its
// capacity, because nothing could prove its generators stopped. Every target
// in its participant journal accepting this term's epoch claim is that proof.
func TestInterruptedDistributedRunIsFinalizedOnceEveryGeneratorConfirms(t *testing.T) {
	runner, store, leadership := newAdoptionTestRunner(t)
	interruptedRunBackend(runner, map[string]bool{"gen-a:9091": true, "gen-b:9091": true})
	_, generation := leadership.executionLeaseTerm()
	interruptedRunFixture(t, runner, store, "interrupted-proven", "gen-a:9091", "gen-b:9091")

	if pending := runner.resolveInterruptedDistributedRuns(context.Background(), generation); pending != 0 {
		t.Fatalf("pending interrupted runs = %d, want 0", pending)
	}
	record := storedStatus(t, store, "interrupted-proven")
	if record.Status != "FAILED" || record.EndTime == nil || !strings.Contains(record.Error, "every generator it reached has confirmed") {
		t.Fatalf("resolved run = %s %q", record.Status, record.Error)
	}
	if strings.Contains(record.Error, "process-fence reconciliation is required") {
		t.Fatalf("resolved run still reports an unresolved requirement: %q", record.Error)
	}
}

func TestInterruptedDistributedRunWaitsForEveryGenerator(t *testing.T) {
	runner, store, leadership := newAdoptionTestRunner(t)
	interruptedRunBackend(runner, map[string]bool{"gen-a:9091": true})
	_, generation := leadership.executionLeaseTerm()
	interruptedRunFixture(t, runner, store, "interrupted-partial", "gen-a:9091", "gen-b:9091")
	interruptedRunFixture(t, runner, store, "interrupted-no-journal")

	if pending := runner.resolveInterruptedDistributedRuns(context.Background(), generation); pending != 2 {
		t.Fatalf("pending interrupted runs = %d, want 2", pending)
	}
	for _, id := range []string{"interrupted-partial", "interrupted-no-journal"} {
		if record := storedStatus(t, store, id); record.Status != "STOPPING" || record.EndTime != nil {
			t.Fatalf("%s without complete proof = %s, want STOPPING", id, record.Status)
		}
	}
}

func TestInterruptedRunOperatorOverrideRequiresAttestation(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	interruptedRunBackend(runner, nil)
	interruptedRunFixture(t, runner, store, "interrupted-override", "gen-gone:9091")
	server := &Server{runner: runner, store: store}

	call := func(id, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+id+"/interrupted-stop/resolve", strings.NewReader(body))
		req.SetPathValue("id", id)
		server.handleResolveInterruptedRun(rec, req)
		return rec
	}
	if rec := call("interrupted-override", `{"note":"generator node deleted"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("override without attestation = %d %s", rec.Code, rec.Body.String())
	}
	if record := storedStatus(t, store, "interrupted-override"); record.Status != "STOPPING" {
		t.Fatalf("rejected override changed the run to %s", record.Status)
	}
	if rec := call("interrupted-override", `{"attestation":"all-generator-processes-stopped","note":"generator node deleted"}`); rec.Code != http.StatusOK {
		t.Fatalf("override = %d %s", rec.Code, rec.Body.String())
	}
	record := storedStatus(t, store, "interrupted-override")
	if record.Status != "FAILED" || !strings.Contains(record.Error, "attested that every generator process") || !strings.Contains(record.Error, "generator node deleted") {
		t.Fatalf("overridden run = %s %q", record.Status, record.Error)
	}
	if rec := call("interrupted-override", `{"attestation":"all-generator-processes-stopped"}`); rec.Code != http.StatusConflict {
		t.Fatalf("override of a resolved run = %d, want 409", rec.Code)
	}
	if rec := call("missing-run", `{"attestation":"all-generator-processes-stopped"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("override of a missing run = %d, want 404", rec.Code)
	}
}
