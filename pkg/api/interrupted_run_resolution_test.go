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

	if pending := runner.resolveInterruptedDistributedRuns(context.Background(), generation, nil); pending != 0 {
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

	if pending := runner.resolveInterruptedDistributedRuns(context.Background(), generation, nil); pending != 2 {
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

// A target that accepted this term's claim stays proven for the term, so later
// cycles neither re-claim it nor need it reachable again.
func TestInterruptedRunProofsAccumulateWithinATerm(t *testing.T) {
	runner, store, leadership := newAdoptionTestRunner(t)
	_, generation := leadership.executionLeaseTerm()
	interruptedRunBackend(runner, map[string]bool{"gen-a:9091": true})
	interruptedRunFixture(t, runner, store, "interrupted-accumulating", "gen-a:9091", "gen-b:9091")
	proven := make(interruptedRunProofs)
	if pending := runner.resolveInterruptedDistributedRuns(context.Background(), generation, proven); pending != 1 {
		t.Fatalf("first cycle pending = %d, want 1", pending)
	}
	if !proven["interrupted-accumulating"]["gen-a:9091"] || proven["interrupted-accumulating"]["gen-b:9091"] {
		t.Fatalf("proofs after first cycle = %v", proven)
	}
	interruptedRunBackend(runner, map[string]bool{"gen-b:9091": true})
	if pending := runner.resolveInterruptedDistributedRuns(context.Background(), generation, proven); pending != 0 {
		t.Fatalf("second cycle pending = %d, want 0", pending)
	}
	if record := storedStatus(t, store, "interrupted-accumulating"); record.Status != "FAILED" {
		t.Fatalf("run after both generators confirmed = %s", record.Status)
	}
}

// Rows held by an older controller carry the earlier marker text, which told
// operators to reconcile process fences by hand. They are still interrupted
// runs: reconcile upgrades the guidance and resolution strips either form.
func TestLegacyInterruptedRunMarkerIsUpgradedAndResolved(t *testing.T) {
	runner, store, leadership := newAdoptionTestRunner(t)
	interruptedRunBackend(runner, map[string]bool{"gen-a:9091": true})
	owner, generation := leadership.executionLeaseTerm()
	started := time.Now().UTC().Add(-time.Minute)
	if err := store.SaveTest(&storage.TestRecord{ID: "interrupted-legacy", Name: "interrupted-legacy", Status: "STOPPING", Error: "earlier failure; " + legacyInterruptedDistributedStopReason, ProjectID: storage.DefaultProjectID, StartTime: started, StartedAt: &started, Groups: []any{}}); err != nil {
		t.Fatal(err)
	}
	if _, saved, err := store.PrepareRunArchiveCollection("interrupted-legacy", storage.DefaultProjectID, started, map[string]string{"gen-a:9091": strings.Repeat("a", 64)}, executionLeaseName, owner); err != nil || !saved {
		t.Fatalf("prepare participant journal = (%v, %v)", saved, err)
	}
	if err := runner.reloadStoredTests(); err != nil {
		t.Fatal(err)
	}
	record := storedStatus(t, store, "interrupted-legacy")
	if record.Status != "STOPPING" || record.Error != "earlier failure; "+interruptedDistributedStopReason {
		t.Fatalf("reconciled legacy row = %s %q", record.Status, record.Error)
	}

	if pending := runner.resolveInterruptedDistributedRuns(context.Background(), generation, nil); pending != 0 {
		t.Fatalf("pending interrupted runs = %d, want 0", pending)
	}
	record = storedStatus(t, store, "interrupted-legacy")
	if record.Status != "FAILED" || !strings.HasPrefix(record.Error, "earlier failure; controller execution ownership was interrupted before the run completed") ||
		strings.Contains(record.Error, "held STOPPING") || strings.Contains(record.Error, "process-fence") {
		t.Fatalf("resolved legacy run = %s %q", record.Status, record.Error)
	}
}

func TestInterruptedRunMarkerStripsEitherForm(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{interruptedDistributedStopReason, ""},
		{legacyInterruptedDistributedStopReason, ""},
		{"earlier; " + interruptedDistributedStopReason, "earlier"},
		{"earlier; " + legacyInterruptedDistributedStopReason, "earlier"},
	} {
		if !hasInterruptedDistributedStopReason(tc.in) {
			t.Fatalf("marker not recognised in %q", tc.in)
		}
		if got := withoutInterruptedDistributedStopReason(tc.in); got != tc.want {
			t.Fatalf("strip(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if hasInterruptedDistributedStopReason("controller execution ownership was interrupted") {
		t.Fatal("a partial marker matched")
	}
}
