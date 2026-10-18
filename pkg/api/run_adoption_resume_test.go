package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

// TestAdoptedRunResumesAgainstRealGenerator drives Phase A and Phase B against
// a real generator service: a run staged by a lost leader is orphaned when its
// epoch lapses, adopted by the successor, and its live metrics flow again under
// the new generation.
func TestAdoptedRunResumesAgainstRealGenerator(t *testing.T) {
	runAdoptedResumeScenario(t, false)
}

// The loss-tolerant variant also holds a per-run lease on the generator; the
// successor must re-establish it before supervision and keep renewing it.
func TestAdoptedLossTolerantRunResumesAgainstRealGenerator(t *testing.T) {
	runAdoptedResumeScenario(t, true)
}

func runAdoptedResumeScenario(t *testing.T, lossTolerant bool) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(target.Close)
	client, _ := newTestGeneratorRPCClient(t, "gen-real", 10, generatorrpc.WithExecution(), generatorrpc.WithExecutionEpochStatePath(filepath.Join(t.TempDir(), "epoch.json")))

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	// The lost leader: its term stages and commits the run with an adoption grace.
	lost := newExecutionLeadership(store, "lost-leader")
	lost.tick()
	lostOwner, lostGeneration := lost.executionLeaseTerm()
	if _, err := client.ClaimExecutionEpoch(ctx, lostGeneration, lostOwner, 400*time.Millisecond); err != nil {
		t.Fatalf("lost leader epoch claim: %v", err)
	}
	content := []byte(fmt.Sprintf(`{"name":"adopt-resume","protocol":"http","config":{"base_url":%q,"think_time":{"type":"fixed","mean_ms":50}},"actions":[{"name":"request","type":"GET","target":"/","think_time_after":true}]}`, target.URL))
	digest := sha256.Sum256(content)
	hash := hex.EncodeToString(digest[:])
	token := bytes.Repeat([]byte{5}, generatorrpc.RunSupervisionTokenSize)
	if _, err := client.DistributeScript(ctx, generatorrpc.Script{ID: "script", ExecutionGeneration: lostGeneration, Content: content, ContentHash: hash}); err != nil {
		t.Fatalf("distribute script: %v", err)
	}
	assign, err := client.AssignWork(ctx, generatorrpc.Assignment{
		TestID: "resume-run", GroupName: "ga", ScriptID: "script", ScriptContent: content, ExpectedScriptHash: hash,
		VUserCount: 1, ExecutionGeneration: lostGeneration, TargetDuration: time.Minute,
		RunSupervisionToken: token, AdoptionGrace: 30 * time.Second, RequireTestExecutionLease: lossTolerant,
	})
	if err != nil || !assign.Accepted {
		t.Fatalf("assign work = (%+v, %v)", assign, err)
	}
	if lossTolerant {
		if _, err := client.ClaimTestExecutionEpoch(ctx, "resume-run", lostGeneration, lostOwner, 400*time.Millisecond); err != nil {
			t.Fatalf("lost leader run lease: %v", err)
		}
	}
	if claim, err := client.ClaimRunSupervision(ctx, generatorrpc.RunSupervisionClaim{TestID: "resume-run", ExecutionGeneration: lostGeneration, Token: token, RequestedTTL: time.Minute, ExecutionLeaseOwnerID: lostOwner}); err != nil || !claim.Accepted {
		t.Fatalf("supervision claim = (%+v, %v)", claim, err)
	}
	if commit, err := client.CommitWorkSupervisedAt(ctx, "resume-run", lostGeneration, time.Now().Add(100*time.Millisecond), token); err != nil || !commit.Accepted {
		t.Fatalf("commit = (%+v, %v)", commit, err)
	}
	if err := store.ReleaseSchedulerLease(executionLeaseName, lostOwner); err != nil {
		t.Fatalf("release lost lease: %v", err)
	}
	time.Sleep(600 * time.Millisecond) // the lost term's generator epoch lapses; the run is orphaned

	// The successor: a newer term with the checkpoint the lost leader armed.
	runner := NewTestRunner(10, NewHub(), store, WithRunContinuityKeyWrapper(testContinuityWrapper(t)))
	successor := newExecutionLeadership(store, "successor")
	successor.tick()
	runner.leadership = successor
	runner.durableExecutionFence = true
	backend := newDistributedExecutionBackend([]string{"gen-real"}, 5*time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
	backend.enableRunSupervision(time.Minute, 10*time.Second)
	runner.execution = backend
	if err := store.SaveTest(&storage.TestRecord{ID: "resume-run", Name: "resume-run", Status: "RUNNING", StartTime: time.Now().UTC(), Groups: []any{}}); err != nil {
		t.Fatalf("save run: %v", err)
	}
	sealed, err := sealRunSupervisionToken(ctx, runner.continuityKeyWrapper, "resume-run", lostGeneration, token)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	checkpoint, _ := json.Marshal(runContinuityCheckpoint{
		Version: runContinuityCheckpointVersion, ExecutionGeneration: lostGeneration, ExecutionLeaseOwnerID: lostOwner,
		RunSupervisionToken: sealed, Shards: map[string][]GeneratorShardSummary{"ga": {{Group: "ga", Target: "gen-real", VUsers: 1, ShardCount: 1}}},
	})
	started := time.Now().UTC()
	test := &ManagedTest{ID: "resume-run", Name: "resume-run", Status: "RUNNING", StartedAt: &started, Assignment: &GeneratorAssignmentSettings{FailoverPolicy: "continue", TolerateGeneratorLoss: lossTolerant}}
	setStoredAdoptionTestCheckpoint(t, runner, store, test, string(checkpoint))
	// The windows the lost leader persisted, and a stale count this process
	// kept from an early hydration of the run.
	for index, window := range [][2]int64{{300, 3}, {200, 0}} {
		at := started.Add(time.Duration(index-10) * time.Second)
		if err := store.SaveHistory(test.ID, at, &LiveMetrics{Timestamp: at, TotalTx: window[0], TotalErrors: window[1]}); err != nil {
			t.Fatalf("save history: %v", err)
		}
	}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	test.runTotals.countWindow(&runTotalsWindow{runTotalsCounts: runTotalsCounts{tx: 1_000_000}}, nil, nil)
	runner.mu.Unlock()

	if err := runner.adoptOrphanedRuns(); err != nil {
		t.Fatalf("Phase A: %v", err)
	}
	// Phase A itself re-establishes supervision under the rotated token, so the
	// run no longer depends on how soon admission (Phase B) follows.
	runner.mu.RLock()
	secured := test.adoptedResume != nil && test.adoptedResume.secured
	keeperRunning := test.remoteRunSupervisionCancel != nil
	runner.mu.RUnlock()
	if !secured || !keeperRunning {
		t.Fatalf("Phase A left generator supervision to Phase B: secured=%v keeper=%v", secured, keeperRunning)
	}
	_, newGeneration := successor.executionLeaseTerm()
	// The durable run now names the adopting term, so an archive the lost
	// leader finalized for it no longer identifies as the run's outcome.
	if durable, err := store.GetTest(test.ID); err != nil || durable.ExecutionGeneration != newGeneration || newGeneration <= lostGeneration {
		t.Fatalf("adoption did not advance the durable execution term: %+v %v (want %d after %d)", durable, err, newGeneration, lostGeneration)
	}
	runner.resumeAdoptedRuns()

	deadline := time.Now().Add(10 * time.Second)
	for {
		runner.mu.RLock()
		activated, status := test.remoteActivated, test.Status
		live := test.LiveMetrics
		runner.mu.RUnlock()
		if activated && status == "RUNNING" && live != nil && live.Timestamp.After(started) && test.executionGeneration == newGeneration {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("adopted run did not resume: activated=%v status=%s live=%v generation=%d (want %d)", activated, status, live != nil, test.executionGeneration, newGeneration)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The initial stream snapshot alone is not proof: live metrics must keep
	// advancing, i.e. the adopted run's periodic publications reach us.
	runner.mu.RLock()
	firstSeen := test.LiveMetrics.Timestamp
	runner.mu.RUnlock()
	for {
		runner.mu.RLock()
		latest := test.LiveMetrics.Timestamp
		runner.mu.RUnlock()
		if latest.After(firstSeen.Add(time.Second)) {
			break
		}
		if time.Now().After(deadline.Add(5 * time.Second)) {
			t.Fatalf("live metrics stopped advancing after adoption (first %s, latest %s)", firstSeen, latest)
		}
		time.Sleep(100 * time.Millisecond)
	}
	runner.mu.RLock()
	continuity := cloneManagedTestFieldsLocked(test).Continuity
	runTotalTx, runTotalErrors := deref(test.LiveMetrics.RunTotalTx), deref(test.LiveMetrics.RunTotalErrors)
	runner.mu.RUnlock()
	// The run totals start from the stored windows (500 / 3), not the kept
	// count, and the target answers every request.
	if runTotalTx < 500 || runTotalTx >= 1_000_000 || runTotalErrors != 3 {
		t.Fatalf("adopted run totals = %d / %d, want at least 500 from storage (not the kept count) / 3", runTotalTx, runTotalErrors)
	}
	if continuity == nil || len(continuity.Failovers) != 1 || continuity.ExecutionGeneration != newGeneration {
		t.Fatalf("continuity after resume = %+v", continuity)
	}
	if err := runner.StopTest(test.ID); err != nil {
		t.Logf("stop after test: %v", err)
	}
}
