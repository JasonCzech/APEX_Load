package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// newAdoptionTestRunner returns a leader runner with a distributed backend
// whose generators are unreachable, a key wrapper, and a durable store.
func newAdoptionTestRunner(t *testing.T) (*TestRunner, *storage.SQLiteStore, *executionLeadership) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runner := NewTestRunner(10, NewHub(), store, WithRunContinuityKeyWrapper(testContinuityWrapper(t)))
	leadership := newExecutionLeadership(store, "adoption-controller")
	leadership.tick()
	runner.leadership = leadership
	runner.durableExecutionFence = true
	runner.execution = newDistributedExecutionBackend([]string{"gen-a:9091"}, 200*time.Millisecond, func(context.Context, string) (generatorClient, error) {
		return nil, errors.New("generator unreachable")
	})
	return runner, store, leadership
}

// storedContinueRun persists and returns a RUNNING continue-policy run armed
// under an older generation than the runner's current term.
func storedContinueRun(t *testing.T, runner *TestRunner, store *storage.SQLiteStore, id, policy string) *ManagedTest {
	t.Helper()
	if err := store.SaveTest(&storage.TestRecord{ID: id, Name: id, Status: "RUNNING", StartTime: time.Now().UTC(), Groups: []any{}}); err != nil {
		t.Fatalf("save %s: %v", id, err)
	}
	token := bytes.Repeat([]byte{7}, generatorrpc.RunSupervisionTokenSize)
	sealed, err := sealRunSupervisionToken(context.Background(), runner.continuityKeyWrapper, id, 0, token)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	checkpoint, _ := json.Marshal(runContinuityCheckpoint{
		Version: runContinuityCheckpointVersion, ExecutionGeneration: 0, ExecutionLeaseOwnerID: "old-leader",
		RunSupervisionToken: sealed,
		Shards:              map[string][]GeneratorShardSummary{"ga": {{Group: "ga", Target: "gen-a:9091", VUsers: 5}}},
	})
	test := &ManagedTest{ID: id, Name: id, Status: "RUNNING", Assignment: &GeneratorAssignmentSettings{FailoverPolicy: policy}}
	setStoredAdoptionTestCheckpoint(t, runner, store, test, string(checkpoint))
	return test
}

func TestReconcileLeavesAdoptionCandidatesRunning(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	candidate := storedContinueRun(t, runner, store, "continue-run", "continue")
	if !runner.isRunAdoptionCandidate(candidate) {
		t.Fatal("continue-policy run with a checkpoint is not an adoption candidate")
	}
	if err := runner.reconcileStoredActiveTest(candidate); err != nil || candidate.Status != "RUNNING" {
		t.Fatalf("candidate after reconcile = %s (%v), want RUNNING for adoption", candidate.Status, err)
	}

	failRun := storedContinueRun(t, runner, store, "fail-run", "")
	if status := runContinuityStatusLocked(failRun); status != nil {
		t.Fatalf("fail-policy run exposes continuity status %+v", status)
	}
	if runner.isRunAdoptionCandidate(failRun) {
		t.Fatal("fail-policy run treated as an adoption candidate")
	}
	if err := runner.reconcileStoredActiveTest(failRun); err != nil || failRun.Status != "FAILED" {
		t.Fatalf("fail-policy run after reconcile = %s (%v), want FAILED", failRun.Status, err)
	}

	noCheckpoint := storedContinueRun(t, runner, store, "unarmed-run", "continue")
	setContinuityCheckpointLocked(noCheckpoint, "")
	if runner.isRunAdoptionCandidate(noCheckpoint) {
		t.Fatal("a run that was never armed is an adoption candidate")
	}
}

func TestUnadoptableRunFailsWithAdoptionReason(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	test := storedContinueRun(t, runner, store, "orphan", "continue")
	test.Assignment.TolerateGeneratorLoss = true
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()

	if err := runner.adoptOrphanedRuns(); err != nil {
		t.Fatalf("adoptOrphanedRuns: %v", err)
	}
	// Phase A only decides; the outcome is applied once admission opens.
	runner.mu.RLock()
	pending := test.adoptedResume
	runner.mu.RUnlock()
	if pending == nil || pending.finishStatus != "FAILED" {
		t.Fatalf("Phase A outcome = %+v, want a pending FAILED", pending)
	}
	runner.resumeAdoptedRuns()
	runner.mu.RLock()
	status, message, resume := test.Status, test.Error, test.adoptedResume
	runner.mu.RUnlock()
	if status != "FAILED" || resume != nil {
		t.Fatalf("unreachable-generator run = %s (resume %v), want FAILED", status, resume != nil)
	}
	if !strings.Contains(message, "could not be adopted") || !strings.Contains(message, "gen-a:9091") {
		t.Fatalf("failure reason %q does not explain the adoption failure", message)
	}
	if !strings.Contains(message, "generator lost while no controller was leading") {
		t.Fatalf("unreachable generator omitted loss-tolerance limitation: %q", message)
	}
	record, err := store.GetTest(test.ID)
	if err != nil || record.Status != "FAILED" {
		t.Fatalf("durable status = %v (%v), want FAILED", record, err)
	}
}

func TestRunAdoptionGraceOnlyForEligibleRuns(t *testing.T) {
	runner, _, _ := newAdoptionTestRunner(t)
	if got := runner.runAdoptionGrace(&ManagedTest{Assignment: &GeneratorAssignmentSettings{FailoverPolicy: "continue"}}); got != defaultRunAdoptionGrace {
		t.Fatalf("continue-policy grace = %s, want %s", got, defaultRunAdoptionGrace)
	}
	if got := runner.runAdoptionGrace(&ManagedTest{Assignment: &GeneratorAssignmentSettings{}}); got != 0 {
		t.Fatalf("fail-policy grace = %s, want 0", got)
	}
	plain := NewTestRunner(10, NewHub(), nil)
	if got := plain.runAdoptionGrace(&ManagedTest{Assignment: &GeneratorAssignmentSettings{FailoverPolicy: "continue"}}); got != 0 {
		t.Fatalf("non-HA controller sent grace %s; a run with no possible adopter must fail fast", got)
	}
	if out, err := normalizeGeneratorAssignmentSettings(&GeneratorAssignmentSettings{FailoverPolicy: "continue", TolerateGeneratorLoss: true}); err != nil || out.FailoverPolicy != "continue" || !out.TolerateGeneratorLoss {
		t.Fatalf("continue policy with generator-loss tolerance = (%+v, %v), want both kept", out, err)
	}
}

func TestGeneratorEpochKeeperWaitsForAdoptionReconcile(t *testing.T) {
	runner, _, leadership := newAdoptionTestRunner(t)
	leadership.reconciling.Store(true)
	released := make(chan struct{})
	go func() {
		runner.awaitRunAdoptionReconcile(context.Background())
		close(released)
	}()
	select {
	case <-released:
		t.Fatal("keeper claimed generators while adoption reconcile was still running")
	case <-time.After(200 * time.Millisecond):
	}
	leadership.reconciling.Store(false)
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("keeper stayed blocked after reconcile finished")
	}
}

// adoptionFakeClient answers epoch claims with one adoption outcome for every
// requested run. Other generatorClient methods are unused by Phase A.
type adoptionFakeClient struct {
	generatorClient
	outcome pb.RunAdoptionOutcome
}

func (c *adoptionFakeClient) ClaimExecutionEpoch(_ context.Context, generation uint64, _ string, _ time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	return generatorrpc.ExecutionEpochResult{Accepted: true, CurrentGeneration: generation, AdoptionSupported: true}, nil
}

func (c *adoptionFakeClient) ClaimExecutionEpochWithAdoptions(_ context.Context, generation uint64, _ string, _ time.Duration, adoptions []generatorrpc.RunAdoption) (generatorrpc.ExecutionEpochResult, error) {
	result := generatorrpc.ExecutionEpochResult{Accepted: true, CurrentGeneration: generation, AdoptionSupported: true}
	for _, adoption := range adoptions {
		result.Adoptions = append(result.Adoptions, generatorrpc.RunAdoptionResult{TestID: adoption.TestID, Outcome: c.outcome, TerminalState: "COMPLETE"})
	}
	return result, nil
}

func (c *adoptionFakeClient) Close() error { return nil }

type retainingAdoptionClient struct {
	*generatorrpc.Client
}

func (c *retainingAdoptionClient) ClaimExecutionEpochWithAdoptions(ctx context.Context, generation uint64, owner string, ttl time.Duration, a []generatorrpc.RunAdoption) (generatorrpc.ExecutionEpochResult, error) {
	fake := adoptionFakeClient{outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED}
	return fake.ClaimExecutionEpochWithAdoptions(ctx, generation, owner, ttl, a)
}

func TestAdoptedRunsDoNotCloseEachOthersConnections(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	client, _ := newTestGeneratorRPCClient(t, "shared", 10)
	backend := newDistributedExecutionBackend([]string{"gen-a:9091"}, time.Second, func(context.Context, string) (generatorClient, error) {
		return &retainingAdoptionClient{Client: client}, nil
	})
	runner.execution = backend
	first := storedContinueRun(t, runner, store, "first", "continue")
	second := storedContinueRun(t, runner, store, "second", "continue")
	runner.tests[first.ID], runner.tests[second.ID] = first, second
	if err := runner.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	// Phase B and the keeper race to start after admission. Sharing must still
	// work after Phase B has consumed its pending resume records.
	first.adoptedResume, second.adoptedResume = nil, nil
	keeperClients := runner.retainAdoptedEpochClients()
	if len(keeperClients) != 1 {
		t.Fatalf("epoch keeper did not retain the adopted transport: %v", keeperClients)
	}
	closeGeneratorEpochKeeperClients(keeperClients)
	backend.closeRemoteResources(runner, first)
	remaining := second.remoteClientsByTarget["gen-a:9091"]
	if remaining == nil {
		t.Fatal("second run was not adopted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := remaining.DistributeScript(ctx, generatorrpc.Script{ID: "transport-check", Content: []byte(`{"name":"transport-check"}`)})
	if err != nil {
		t.Fatalf("first run cleanup disconnected second run: %v", err)
	}
	backend.closeRemoteResources(runner, second)
}

func TestKnownCompletedGeneratorSurvivesAnotherAdoption(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	backend := newDistributedExecutionBackend([]string{"gen-a:9091"}, time.Second, func(context.Context, string) (generatorClient, error) {
		return &adoptionFakeClient{outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST}, nil
	})
	runner.execution = backend
	test := storedContinueRun(t, runner, store, "finished-before-prior-takeover", "continue")
	checkpoint, _, _ := decodeRunContinuityCheckpoint(test.continuityCheckpoint)
	checkpoint.CompletedTargets = []string{"gen-a:9091"}
	checkpoint.ArchiveTargets = []string{"retired:9091"}
	encoded, _ := json.Marshal(checkpoint)
	setStoredAdoptionTestCheckpoint(t, runner, store, test, string(encoded))
	runner.tests[test.ID] = test
	if err := runner.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	if test.adoptedResume == nil || test.adoptedResume.finishStatus != "" || len(test.adoptedResume.completed) != 1 || len(test.remoteArchiveClientsByTarget) != 1 {
		t.Fatalf("known completed participants were re-adopted or lost: %+v", test.adoptedResume)
	}
	checkpoint, _, _ = decodeRunContinuityCheckpoint(test.continuityCheckpoint)
	if len(checkpoint.CompletedTargets) != 1 || len(checkpoint.ArchiveTargets) != 1 {
		t.Fatalf("rearm lost terminal participants: %+v", checkpoint)
	}
	backend.closeRemoteResources(runner, test)
}

func TestAdoptionSharesCompletedAndRetiredGeneratorConnections(t *testing.T) {
	for _, retired := range []bool{false, true} {
		t.Run(fmt.Sprintf("retired=%v", retired), func(t *testing.T) {
			runner, store, _ := newAdoptionTestRunner(t)
			clientA, _ := newTestGeneratorRPCClient(t, "shared-a", 10)
			clientB, _ := newTestGeneratorRPCClient(t, "shared-b", 10)
			var mu sync.Mutex
			dials := make(map[string]int)
			backend := newDistributedExecutionBackend([]string{"gen-a:9091"}, time.Second, func(_ context.Context, target string) (generatorClient, error) {
				mu.Lock()
				defer mu.Unlock()
				dials[target]++
				if dials[target] > 1 {
					return nil, errors.New("reverse tunnel already consumed")
				}
				client := clientA
				if target == "gen-b:9091" {
					client = clientB
				}
				return &retainingAdoptionClient{Client: client}, nil
			})
			runner.execution = backend
			active := storedContinueRun(t, runner, store, "active", "continue")
			finished := storedContinueRun(t, runner, store, "finished", "continue")
			checkpoint, _, _ := decodeRunContinuityCheckpoint(finished.continuityCheckpoint)
			checkpoint.CompletedTargets = []string{"gen-a:9091"}
			if retired {
				checkpoint.Shards["ga"][0].Target = "gen-b:9091"
				checkpoint.CompletedTargets = []string{"gen-b:9091"}
				checkpoint.ArchiveTargets = []string{"gen-a:9091"}
			}
			encoded, _ := json.Marshal(checkpoint)
			setStoredAdoptionTestCheckpoint(t, runner, store, finished, string(encoded))
			runner.tests[active.ID], runner.tests[finished.ID] = active, finished
			if err := runner.adoptOrphanedRuns(); err != nil {
				t.Fatal(err)
			}
			if finished.adoptedResume == nil || finished.adoptedResume.finishStatus != "" {
				t.Fatalf("completed participant could not share the active transport: %+v", finished.adoptedResume)
			}
			remaining := finished.remoteClientsByTarget["gen-a:9091"]
			if retired {
				remaining = finished.remoteArchiveClientsByTarget["gen-a:9091"]
			}
			if remaining == nil {
				t.Fatal("shared archive participant was discarded")
			}
			backend.closeRemoteResources(runner, active)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := remaining.DistributeScript(ctx, generatorrpc.Script{ID: "archive-transport", Content: []byte(`{"name":"archive-transport"}`)}); err != nil {
				t.Fatalf("active run cleanup disconnected archive transport: %v", err)
			}
			backend.closeRemoteResources(runner, finished)
		})
	}
}

func TestPartialAdoptionFailureRetainsCompletedGeneratorEvidence(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	backend := newDistributedExecutionBackend([]string{"gen-a:9091", "gen-b:9091"}, time.Second, func(_ context.Context, target string) (generatorClient, error) {
		if target == "gen-b:9091" {
			return nil, errors.New("generator was lost")
		}
		return &adoptionFakeClient{outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED}, nil
	})
	runner.execution = backend
	test := storedContinueRun(t, runner, store, "partially-completed", "continue")
	checkpoint, _, _ := decodeRunContinuityCheckpoint(test.continuityCheckpoint)
	checkpoint.Shards["ga"] = append(checkpoint.Shards["ga"], GeneratorShardSummary{Group: "ga", Target: "gen-b:9091", VUsers: 5})
	checkpoint.ArchiveTargets = []string{"retired:9091"}
	encoded, _ := json.Marshal(checkpoint)
	setStoredAdoptionTestCheckpoint(t, runner, store, test, string(encoded))
	runner.tests[test.ID] = test
	if err := runner.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	if test.adoptedResume == nil || test.adoptedResume.finishStatus != "FAILED" || test.remoteClientsByTarget["gen-a:9091"] == nil || !remoteTargetStateDone(test.remoteTargetStates["gen-a:9091"]) {
		t.Fatalf("failed adoption discarded completed participant evidence: %+v", test.adoptedResume)
	}
	if test.remoteArchiveClientsByTarget["retired:9091"] == nil {
		t.Fatal("failed adoption discarded retired participant evidence")
	}
	backend.closeRemoteResources(runner, test)
}

type retryAdoptionClient struct {
	adoptionFakeClient
	attempts              int
	priorToken, nextToken []byte
}

func (c *retryAdoptionClient) ClaimExecutionEpochWithAdoptions(ctx context.Context, generation uint64, owner string, ttl time.Duration, adoptions []generatorrpc.RunAdoption) (generatorrpc.ExecutionEpochResult, error) {
	c.attempts++
	if c.attempts == 1 {
		c.priorToken, c.nextToken = bytes.Clone(adoptions[0].PriorToken), bytes.Clone(adoptions[0].NewToken)
		return generatorrpc.ExecutionEpochResult{}, status.Error(codes.Unavailable, "acknowledgement was lost")
	}
	if !bytes.Equal(adoptions[0].PriorToken, c.priorToken) || !bytes.Equal(adoptions[0].NewToken, c.nextToken) {
		return generatorrpc.ExecutionEpochResult{}, errors.New("retry changed adoption lineage")
	}
	return c.adoptionFakeClient.ClaimExecutionEpochWithAdoptions(ctx, generation, owner, ttl, adoptions)
}

func TestAdoptionRetriesLostAcknowledgementWithSameTokenRotation(t *testing.T) {
	client := &retryAdoptionClient{adoptionFakeClient: adoptionFakeClient{outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED}}
	backend := newDistributedExecutionBackend([]string{"gen"}, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results := backend.claimRunAdoptions(ctx, map[string][]generatorrpc.RunAdoption{"gen": {{TestID: "run", PriorGeneration: 1, PriorToken: []byte{1}, NewToken: []byte{2}}}}, 2, "owner", time.Now().Add(time.Minute))
	if result := results["gen"]; result.err != nil || len(result.outcomes) != 1 || client.attempts != 2 {
		t.Fatalf("retry failed: %+v, attempts=%d", result, client.attempts)
	}
}

func TestPhaseAInstallsAdoptedAndCompletedRuns(t *testing.T) {
	for _, tc := range []struct {
		name              string
		outcome           pb.RunAdoptionOutcome
		adopted, finished int
	}{
		{"adopted", pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, 1, 0},
		// Regression: every generator finished during the gap. This path used to
		// panic in the acquisition callback, wedging leadership.
		{"completed during the gap", pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, store, leadership := newAdoptionTestRunner(t)
			runner.execution = newDistributedExecutionBackend([]string{"gen-a:9091"}, time.Second, func(context.Context, string) (generatorClient, error) {
				return &adoptionFakeClient{outcome: tc.outcome}, nil
			})
			test := storedContinueRun(t, runner, store, "orphan", "continue")
			runner.mu.Lock()
			runner.tests[test.ID] = test
			runner.mu.Unlock()

			if err := runner.adoptOrphanedRuns(); err != nil {
				t.Fatalf("adoptOrphanedRuns: %v", err)
			}
			runner.mu.RLock()
			resume, status := test.adoptedResume, test.Status
			token := test.remoteRunSupervisionToken
			runner.mu.RUnlock()
			if status != "RUNNING" || resume == nil || len(resume.adopted) != tc.adopted || len(resume.completed) != tc.finished {
				t.Fatalf("after Phase A: status=%s resume=%+v", status, resume)
			}
			_, generation := leadership.executionLeaseTerm()
			if resume.generation != generation || token == ([generatorrpc.RunSupervisionTokenSize]byte{}) {
				t.Fatalf("run not bound to the new term: generation %d (want %d), token set %v", resume.generation, generation, token != ([generatorrpc.RunSupervisionTokenSize]byte{}))
			}
			record, err := store.GetTest(test.ID)
			if err != nil {
				t.Fatalf("GetTest: %v", err)
			}
			checkpoint, present, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint)
			if err != nil || !present || checkpoint.ExecutionGeneration != generation {
				t.Fatalf("checkpoint not re-armed under the new term: %+v present=%v err=%v", checkpoint, present, err)
			}
			if opened, err := openRunSupervisionToken(context.Background(), runner.continuityKeyWrapper, test.ID, checkpoint); err != nil || !bytes.Equal(opened, token[:]) {
				t.Fatalf("re-armed checkpoint does not hold the rotated token: %v", err)
			}
			runner.mu.RLock()
			public := cloneManagedTestFieldsLocked(test).Continuity
			runner.mu.RUnlock()
			if public == nil || public.Policy != "continue" || !public.Armed || public.ExecutionGeneration != generation || len(public.Failovers) != 1 {
				t.Fatalf("public continuity status = %+v", public)
			}
			if failover := public.Failovers[0]; failover.Generation != generation || failover.AdoptedGenerators != tc.adopted || failover.CompletedGenerators != tc.finished {
				t.Fatalf("recorded failover = %+v", failover)
			}
			if encoded, _ := json.Marshal(public); strings.Contains(string(encoded), "ciphertext") || strings.Contains(string(encoded), "run_supervision_token") {
				t.Fatalf("public continuity status leaks the sealed token: %s", encoded)
			}
		})
	}
}

// stoppingFakeClient is an adoptionFakeClient that also records token-fenced
// stops and has no retained archive, as the ABORTED finalization path needs.
type stoppingFakeClient struct {
	adoptionFakeClient
	mu    sync.Mutex
	stops []string
}

func (c *stoppingFakeClient) StopTestAtGenerationWithToken(_ context.Context, testID string, _ bool, generation uint64, token []byte) (generatorrpc.StopResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stops = append(c.stops, fmt.Sprintf("%s@%d/%d", testID, generation, len(token)))
	return generatorrpc.StopResult{}, nil
}

func (c *stoppingFakeClient) GetArchiveArtifact(context.Context, generatorrpc.ArchiveArtifactRequest) ([]byte, error) {
	return nil, errors.New("no retained archive in this fake")
}

func TestStoppingRunKeepsItsOutcomeAfterFailover(t *testing.T) {
	for _, tc := range []struct {
		name       string
		outcome    pb.RunAdoptionOutcome
		wantStop   bool
		reason     string
		wantStatus string
		wantError  string
	}{
		// The stop never reached this generator: it is adopted, then stopped.
		{name: "operator stop with generator running", outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, wantStop: true, wantStatus: "ABORTED"},
		{name: "operator stop already acknowledged", outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED, wantStatus: "ABORTED"},
		{name: "evacuation failure", outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, wantStop: true, reason: "generator evacuation state is uncertain: replacement unreachable", wantStatus: "FAILED", wantError: "generator evacuation state is uncertain: replacement unreachable"},
		{name: "scale failure", outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, wantStop: true, reason: "remote VUser state is uncertain: compensation failed", wantStatus: "FAILED", wantError: "remote VUser state is uncertain: compensation failed"},
		{name: "duration failure", outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, wantStop: true, reason: "remote duration state is uncertain: compensation failed", wantStatus: "FAILED", wantError: "remote duration state is uncertain: compensation failed"},
		{name: "pause failure", outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, wantStop: true, reason: "remote pause/resume state is uncertain: compensation failed", wantStatus: "FAILED", wantError: "remote pause/resume state is uncertain: compensation failed"},
		{name: "start persistence failure", outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, wantStop: true, reason: "persist coordinated distributed start: database unavailable", wantStatus: "FAILED", wantError: "persist coordinated distributed start: database unavailable"},
		{name: "distributed commit failure", outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED, reason: "commit distributed work: generator rejected commit", wantStatus: "FAILED", wantError: "commit distributed work: generator rejected commit"},
		{name: "CI abort", outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED, reason: "ci abort reason: timed_out", wantStatus: "ABORTED", wantError: "ci abort reason: timed_out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, store, leadership := newAdoptionTestRunner(t)
			client := &stoppingFakeClient{adoptionFakeClient: adoptionFakeClient{outcome: tc.outcome}}
			runner.execution = newDistributedExecutionBackend([]string{"gen-a:9091"}, time.Second, func(context.Context, string) (generatorClient, error) {
				return client, nil
			})
			test := storedContinueRun(t, runner, store, "stopping-run", "continue")
			test.Status = "STOPPING"
			test.Error = tc.reason
			checkpoint, _, _ := decodeRunContinuityCheckpoint(test.continuityCheckpoint)
			checkpoint.ArchiveTargets = []string{"retired:9091"}
			encoded, _ := json.Marshal(checkpoint)
			setStoredAdoptionTestCheckpoint(t, runner, store, test, string(encoded))
			if _, err := store.UpdateTestStatusIfActive(test.ID, "STOPPING", nil, tc.reason); err != nil {
				t.Fatalf("mark STOPPING: %v", err)
			}
			if !runner.isRunAdoptionCandidate(test) {
				t.Fatal("a stopping continue-policy run is not taken over")
			}
			runner.mu.Lock()
			runner.tests[test.ID] = test
			runner.mu.Unlock()

			if err := runner.adoptOrphanedRuns(); err != nil {
				t.Fatalf("adoptOrphanedRuns: %v", err)
			}
			if test.remoteArchiveClientsByTarget["retired:9091"] == nil {
				t.Fatal("stopping adoption discarded retired participant evidence")
			}
			runner.resumeAdoptedRuns()
			record, err := store.GetTest(test.ID)
			if err != nil || record.Status != tc.wantStatus || record.Error != tc.wantError {
				t.Fatalf("durable outcome = %v (%v), want %s error %q", record, err, tc.wantStatus, tc.wantError)
			}
			runner.mu.RLock()
			resumed := test.adoptedResume != nil
			runner.mu.RUnlock()
			if resumed {
				t.Fatal("a stopping run was scheduled to resume")
			}
			_, generation := leadership.executionLeaseTerm()
			client.mu.Lock()
			stops := append([]string(nil), client.stops...)
			client.mu.Unlock()
			want := fmt.Sprintf("stopping-run@%d/%d", generation, generatorrpc.RunSupervisionTokenSize)
			if tc.wantStop && (len(stops) == 0 || stops[0] != want) {
				t.Fatalf("adopted generator stops = %v, want a token-fenced stop %q", stops, want)
			}
		})
	}
}

func TestFailPolicyStoppingRunStillFails(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	test := storedContinueRun(t, runner, store, "fail-stopping", "")
	test.Status = "STOPPING"
	if runner.isRunAdoptionCandidate(test) {
		t.Fatal("fail-policy stopping run treated as an adoption candidate")
	}
	if err := runner.reconcileStoredActiveTest(test); err != nil || test.Status != "FAILED" {
		t.Fatalf("fail-policy stopping run after reconcile = %s (%v), want FAILED", test.Status, err)
	}
}

// Generator-loss evacuation must outwait an orphaned source: a generator cut off
// from a live controller keeps a continue-policy run going for the grace.
func TestLostGeneratorFenceWaitsOutAdoptionGrace(t *testing.T) {
	runner, _, _ := newAdoptionTestRunner(t)
	now := time.Now()
	lease := now.Add(20 * time.Second)
	continuing := &ManagedTest{Assignment: &GeneratorAssignmentSettings{FailoverPolicy: "continue", TolerateGeneratorLoss: true}}
	if got := lostGeneratorFenceDeadline(runner, continuing, lease, now); !got.Equal(lease.Add(defaultRunAdoptionGrace)) {
		t.Fatalf("continue-policy fence deadline = %s, want lease + %s", got.Sub(now), defaultRunAdoptionGrace)
	}
	failing := &ManagedTest{Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true}}
	if got := lostGeneratorFenceDeadline(runner, failing, lease, now); !got.Equal(lease) {
		t.Fatalf("fail-policy fence deadline = %s, want the lease deadline", got.Sub(now))
	}
	if got := lostGeneratorFenceDeadline(runner, failing, now.Add(time.Hour), now); !got.Equal(now.Add(defaultExecutionLeaseTTL)) {
		t.Fatalf("corrupt far deadline not capped: %s", got.Sub(now))
	}
	for _, ttl := range []time.Duration{30 * time.Second, 60 * time.Second} {
		if got := continuitySupervisionFenceTTL(ttl, defaultRunAdoptionGrace); got < defaultExecutionLeaseTTL+defaultRunAdoptionGrace {
			t.Fatalf("supervised replacement may start before orphan grace expires: fence TTL %s", got)
		}
		if got := continuitySupervisionFenceTTL(ttl, 0); got != ttl {
			t.Fatalf("fail-policy supervision TTL changed from %s to %s", ttl, got)
		}
	}
}

func TestFinishedGeneratorsAreExcludedFromRenewal(t *testing.T) {
	runner, _, _ := newAdoptionTestRunner(t)
	backend := runner.execution.(*distributedExecutionBackend)
	test := &ManagedTest{ID: "mixed"}
	runner.excludeFinishedGenerators(backend, test, map[string]generatorClient{"gen-done:9091": &adoptionFakeClient{}})
	if !test.remoteRunSupervisionFencedTargets["gen-done:9091"] || !backend.testExecutionLeaseFenced(test, "gen-done:9091") {
		t.Fatal("a generator that finished during the gap is still renewed")
	}
	if backend.testExecutionLeaseFenced(test, "gen-live:9091") || test.remoteRunSupervisionFencedTargets["gen-live:9091"] {
		t.Fatal("an adopted generator was excluded from renewal")
	}
}

// Adoption now consumes an exact durable checkpoint snapshot. Fixtures that
// change completed/retired shards must persist that same snapshot before reconcile.
func setStoredAdoptionTestCheckpoint(t *testing.T, runner *TestRunner, store *storage.SQLiteStore, test *ManagedTest, raw string) {
	t.Helper()
	owner, _ := runner.leadership.executionLeaseTerm()
	if result, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(test.ID, raw, executionLeaseName, owner); err != nil || result != storage.TestStatusTransitionUpdated {
		t.Fatalf("persist checkpoint fixture: %s %v", result, err)
	}
	setContinuityCheckpointLocked(test, raw)
}
