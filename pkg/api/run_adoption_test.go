package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type flakyAdoptionKeyWrapper struct {
	keywrap.KeyWrapper
	failWrap   atomic.Bool
	failUnwrap atomic.Bool
}

func (w *flakyAdoptionKeyWrapper) WrapKey(ctx context.Context, plaintext, aad []byte) (keywrap.WrappedKey, error) {
	if w.failWrap.Load() {
		return keywrap.WrappedKey{}, errors.New("temporary key service outage")
	}
	return w.KeyWrapper.WrapKey(ctx, plaintext, aad)
}

func (w *flakyAdoptionKeyWrapper) UnwrapKey(ctx context.Context, wrapped keywrap.WrappedKey, aad []byte) ([]byte, error) {
	if w.failUnwrap.Load() {
		return nil, errors.New("temporary key service outage")
	}
	return w.KeyWrapper.UnwrapKey(ctx, wrapped, aad)
}

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
	t.Cleanup(runner.beginShutdown)
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
	if err := runner.reconcileStoredActiveTest(failRun); err != nil || failRun.Status != "STOPPING" {
		t.Fatalf("fail-policy run after reconcile = %s (%v), want unproven STOPPING", failRun.Status, err)
	}

	noCheckpoint := storedContinueRun(t, runner, store, "unarmed-run", "continue")
	setContinuityCheckpointLocked(noCheckpoint, "")
	if runner.isRunAdoptionCandidate(noCheckpoint) {
		t.Fatal("a run that was never armed is an adoption candidate")
	}
}

func TestUnadoptableRunWaitsForStopProof(t *testing.T) {
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
	if status != "STOPPING" || resume != nil {
		t.Fatalf("unreachable-generator run = %s (resume %v), want STOPPING until stop proof", status, resume != nil)
	}
	if !strings.Contains(message, "could not be adopted") || !strings.Contains(message, "gen-a:9091") {
		t.Fatalf("failure reason %q does not explain the adoption failure", message)
	}
	if !strings.Contains(message, "generator lost while no controller was leading") {
		t.Fatalf("unreachable generator omitted loss-tolerance limitation: %q", message)
	}
	record, err := store.GetTest(test.ID)
	if err != nil || record.Status != "STOPPING" || record.EndTime != nil {
		t.Fatalf("durable status = %v (%v), want unterminated STOPPING", record, err)
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
		runner.awaitRunAdoptionReconcile(context.Background(), 1)
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

// The keeper must not wait for the rest of acquisition reconcile once this
// term's adoption claims are done: unrelated recovery can be slow, and every
// other generator stays unclaimed (and new runs blocked) until the keeper runs.
func TestGeneratorEpochKeeperReleasesWhenAdoptionPhaseCompletes(t *testing.T) {
	runner, _, leadership := newAdoptionTestRunner(t)
	leadership.reconciling.Store(true)
	defer leadership.reconciling.Store(false)
	released := make(chan struct{})
	go func() {
		runner.awaitRunAdoptionReconcile(context.Background(), 7)
		close(released)
	}()
	runner.markRunAdoptionDone(6)
	select {
	case <-released:
		t.Fatal("an older term's adoption must not release this term's keeper")
	case <-time.After(200 * time.Millisecond):
	}
	runner.markRunAdoptionDone(7)
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("keeper stayed blocked after this term's adoption phase finished")
	}
	runner.markRunAdoptionDone(5)
	if got := runner.runAdoptionDoneGeneration.Load(); got != 7 {
		t.Fatalf("adoption marker regressed to %d", got)
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
	// This controller once started the run (a standby that led it before):
	// after another controller held it, its unpublished batches are unknown.
	test.jointHeldFromStart = true
	runner.tests[test.ID] = test
	if err := runner.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	if test.adoptedResume == nil || test.adoptedResume.finishStatus != "" || len(test.adoptedResume.completed) != 1 || len(test.remoteArchiveClientsByTarget) != 1 {
		t.Fatalf("known completed participants were re-adopted or lost: %+v", test.adoptedResume)
	}
	if test.jointHeldFromStart {
		t.Fatal("an adopted run is still held from its start")
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
	if test.adoptedResume == nil || test.adoptedResume.finishStatus != "FAILED" || test.adoptedResume.completed["gen-a:9091"] == nil ||
		test.remoteClientsByTarget["gen-a:9091"] == nil || !remoteTargetStateDone(test.remoteTargetStates["gen-a:9091"]) {
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
			// A stage recorder this process kept from an earlier ownership,
			// sampling from long before the adoption.
			kept := stageRecorderLocked(test)
			kept.since = time.Now().UTC().Add(-time.Hour)
			runner.mu.Unlock()

			if err := runner.adoptOrphanedRuns(); err != nil {
				t.Fatalf("adoptOrphanedRuns: %v", err)
			}
			runner.mu.RLock()
			resume, status := test.adoptedResume, test.Status
			token := test.remoteRunSupervisionToken
			var adoptedAt time.Time
			if len(test.continuityFailovers) > 0 {
				adoptedAt = test.continuityFailovers[len(test.continuityFailovers)-1].AdoptedAt
			}
			runner.mu.RUnlock()
			kept.mu.Lock()
			since := kept.since
			kept.mu.Unlock()
			if adoptedAt.IsZero() || !since.Equal(adoptedAt) {
				t.Fatalf("the kept stage recorder samples from %v, want the adoption at %v", since, adoptedAt)
			}
			// Coverage observed here starts at the adoption, which the ledger
			// records so finalize says observed time is a lower bound.
			runner.mu.RLock()
			ledger := test.telemetrySources
			runner.mu.RUnlock()
			if !ledger.wasAdopted() {
				t.Fatal("the adopting controller's source ledger is not marked adopted")
			}
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

type delayedStoppingFakeClient struct {
	stoppingFakeClient
	allowStop atomic.Bool
}

type phaseBForceStopFakeClient struct {
	adoptionFakeClient
	allowForce    atomic.Bool
	forceStops    atomic.Int32
	gracefulStops atomic.Int32
}

func (c *phaseBForceStopFakeClient) StopTestAtGenerationWithToken(_ context.Context, _ string, graceful bool, _ uint64, _ []byte) (generatorrpc.StopResult, error) {
	if graceful {
		c.gracefulStops.Add(1)
		return generatorrpc.StopResult{Acknowledged: true, PendingVUsers: 5}, nil
	}
	c.forceStops.Add(1)
	if !c.allowForce.Load() {
		return generatorrpc.StopResult{}, errors.New("generator force stop unavailable")
	}
	return generatorrpc.StopResult{Acknowledged: true}, nil
}

func (c *phaseBForceStopFakeClient) GetArchiveArtifact(context.Context, generatorrpc.ArchiveArtifactRequest) ([]byte, error) {
	return nil, errors.New("no retained archive in this fake")
}

func (c *delayedStoppingFakeClient) StopTestAtGenerationWithToken(ctx context.Context, testID string, graceful bool, generation uint64, token []byte) (generatorrpc.StopResult, error) {
	ack, err := c.stoppingFakeClient.StopTestAtGenerationWithToken(ctx, testID, graceful, generation, token)
	if !c.allowStop.Load() {
		ack.Acknowledged = false
	}
	return ack, err
}

func (c *stoppingFakeClient) StopTestAtGenerationWithToken(_ context.Context, testID string, _ bool, generation uint64, token []byte) (generatorrpc.StopResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stops = append(c.stops, fmt.Sprintf("%s@%d/%d", testID, generation, len(token)))
	return generatorrpc.StopResult{Acknowledged: true}, nil
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
		{name: "prior failover still awaiting failed cleanup", outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED, reason: "unclassified cleanup failure; controller failover: pending FAILED cleanup proof: generator unavailable", wantStatus: "FAILED", wantError: "unclassified cleanup failure; controller failover: pending FAILED cleanup proof: generator unavailable"},
		{name: "prior failover still awaiting aborted cleanup", outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED, reason: "controller failover: pending ABORTED cleanup proof: generator unavailable", wantStatus: "ABORTED", wantError: "controller failover: pending ABORTED cleanup proof: generator unavailable"},
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
			// A controller that only finishes the run observed none of it
			// before the adoption, which its ledger records.
			runner.mu.RLock()
			ledger := test.telemetrySources
			runner.mu.RUnlock()
			if !ledger.wasAdopted() {
				t.Fatal("the source ledger of a run finished after adoption is not marked adopted")
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

func TestStoppingRunWaitsForPositiveStopAfterFailover(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	client := &delayedStoppingFakeClient{stoppingFakeClient: stoppingFakeClient{adoptionFakeClient: adoptionFakeClient{outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED}}}
	runner.execution = newDistributedExecutionBackend([]string{"gen-a:9091"}, time.Second, func(context.Context, string) (generatorClient, error) {
		return client, nil
	})
	test := storedContinueRun(t, runner, store, "delayed-stop", "continue")
	test.Status = "STOPPING"
	if _, err := store.UpdateTestStatusIfActive(test.ID, "STOPPING", nil, ""); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	if err := runner.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	runner.resumeAdoptedRuns()
	record, err := store.GetTest(test.ID)
	if err != nil || record.Status != "STOPPING" || record.EndTime != nil {
		t.Fatalf("unacknowledged stop = %+v (%v), want unterminated STOPPING", record, err)
	}
	client.allowStop.Store(true)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		record, err = store.GetTest(test.ID)
		if err == nil && record.Status == "ABORTED" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || record.Status != "ABORTED" || record.EndTime == nil {
		t.Fatalf("acknowledged retry = %+v (%v), want terminal ABORTED", record, err)
	}
	client.mu.Lock()
	stops := len(client.stops)
	client.mu.Unlock()
	if stops < 2 {
		t.Fatalf("stop attempts = %d, want initial failure and retry", stops)
	}
}

func TestStoppingRunPersistsPartialStopProofAcrossRetry(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	test := storedContinueRun(t, runner, store, "partial-stopping-run", "continue")
	checkpoint, _, err := decodeRunContinuityCheckpoint(test.continuityCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.Shards["ga"] = append(checkpoint.Shards["ga"], GeneratorShardSummary{Group: "ga", Target: "gen-b:9091", VUsers: 5})
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	test.Status = "STOPPING"
	setStoredAdoptionTestCheckpoint(t, runner, store, test, string(raw))
	if _, err := store.UpdateTestStatusIfActive(test.ID, "STOPPING", nil, ""); err != nil {
		t.Fatal(err)
	}
	runner.tests[test.ID] = test
	a, b := &phaseBForceStopFakeClient{adoptionFakeClient: adoptionFakeClient{outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED}},
		&phaseBForceStopFakeClient{adoptionFakeClient: adoptionFakeClient{outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED}}
	a.allowForce.Store(true)
	var aGone atomic.Bool
	runner.execution = newDistributedExecutionBackend([]string{"gen-a:9091", "gen-b:9091"}, 200*time.Millisecond, func(_ context.Context, target string) (generatorClient, error) {
		if target == "gen-a:9091" {
			if aGone.Load() {
				return nil, errors.New("proven stopped generator was removed")
			}
			return a, nil
		}
		return b, nil
	})
	if err := runner.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	runner.resumeAdoptedRuns()
	record, err := store.GetTest(test.ID)
	if err != nil || record.Status != "STOPPING" || record.EndTime != nil {
		t.Fatalf("partial stop proof prematurely ended run: %+v, %v", record, err)
	}
	checkpoint, _, err = decodeRunContinuityCheckpoint(record.ContinuityCheckpoint)
	if err != nil || !slices.Contains(checkpoint.StoppedTargets, "gen-a:9091") || slices.Contains(checkpoint.StoppedTargets, "gen-b:9091") {
		t.Fatalf("successful first stop was not durably retained: %+v, %v", checkpoint.StoppedTargets, err)
	}
	aGone.Store(true)
	b.allowForce.Store(true)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		record, err = store.GetTest(test.ID)
		if err == nil && record.Status == "ABORTED" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || record.Status != "ABORTED" || record.EndTime == nil {
		t.Fatalf("remaining stop proof did not resolve run: %+v, %v", record, err)
	}
	if a.forceStops.Load() != 1 || b.forceStops.Load() < 2 || a.gracefulStops.Load() != 0 {
		t.Fatalf("retries used unsafe or repeated stop: a=%d b=%d graceful=%d", a.forceStops.Load(), b.forceStops.Load(), a.gracefulStops.Load())
	}
}

func TestAdoptedStopProofRetriesIncompleteTerminalWrite(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	runner := NewTestRunner(1, NewHub(), base)
	t.Cleanup(runner.beginShutdown)
	test := &ManagedTest{ID: "adopted-terminal-retry", Name: "adopted terminal retry", Status: "STOPPING", CreatedAt: time.Now().UTC()}
	if err := base.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.Name, Status: test.Status, StartTime: test.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	runner.tests[test.ID] = test
	store := &scriptedTestPersistence{testPersistence: base, err: errors.New("temporary database outage"), failUpdateCall: failCalls(1, durableTransitionAttempts)}
	runner.testStore = store
	backend := newDistributedExecutionBackend(nil, 100*time.Millisecond, nil)
	if err := runner.completeAdoptedRunAfterStopProof(backend, test, "FAILED", "confirmed stop"); err == nil {
		t.Fatal("confirmed stop retired the retry before a durable terminal write")
	}
	record, err := base.GetTest(test.ID)
	if err != nil || record.Status != "STOPPING" || record.EndTime != nil {
		t.Fatalf("failed terminal write changed durable row: %+v, %v", record, err)
	}
	if err := runner.completeAdoptedRunAfterStopProof(backend, test, "FAILED", "confirmed stop"); err != nil {
		t.Fatalf("terminal write remained incomplete after storage recovered: %v", err)
	}
	record, err = base.GetTest(test.ID)
	if err != nil || record.Status != "FAILED" || record.EndTime == nil || test.Status != "FAILED" || test.EndedAt == nil {
		t.Fatalf("recovered terminal write left run active: row=%+v, %v, in-memory=%s %v", record, err, test.Status, test.EndedAt)
	}
}

func TestDistributedUnprovableRunStaysStoppingUntilExternalFence(t *testing.T) {
	for _, tc := range []struct {
		policy, status, checkpoint string
	}{
		{policy: "continue", status: "RUNNING", checkpoint: ""},
		{policy: "continue", status: "PAUSED", checkpoint: "{malformed"},
		{policy: "continue", status: "STOPPING", checkpoint: ""},
		{policy: "continue", status: "STOPPING", checkpoint: "{malformed"},
		{policy: "", status: "RUNNING", checkpoint: ""},
		{policy: "", status: "PAUSED", checkpoint: ""},
		{policy: "", status: "STOPPING", checkpoint: ""},
	} {
		t.Run(fmt.Sprintf("policy=%q/status=%s/checkpoint=%q", tc.policy, tc.status, tc.checkpoint), func(t *testing.T) {
			runner, store, _ := newAdoptionTestRunner(t)
			test := storedContinueRun(t, runner, store, "unprovable-stopping", tc.policy)
			test.Status = tc.status
			setStoredAdoptionTestCheckpoint(t, runner, store, test, tc.checkpoint)
			priorReason := ""
			if tc.status == "STOPPING" {
				priorReason = "ci abort reason: operator stop"
			}
			if _, err := store.UpdateTestStatusIfActive(test.ID, tc.status, nil, priorReason); err != nil {
				t.Fatal(err)
			}
			test.Error = priorReason
			record, err := store.GetTest(test.ID)
			if err != nil {
				t.Fatal(err)
			}
			slotID := "retained-capacity-slot"
			record.TimeslotID = &slotID
			if err := store.SaveTest(record); err != nil {
				t.Fatal(err)
			}
			test.TimeslotID = &slotID
			if runner.isRunAdoptionCandidate(test) {
				t.Fatal("missing or malformed checkpoint was accepted for adoption")
			}
			if err := runner.reconcileStoredActiveTest(test); err != nil {
				t.Fatal(err)
			}
			record, err = store.GetTest(test.ID)
			if err != nil || record.Status != "STOPPING" || record.EndTime != nil ||
				!strings.Contains(record.Error, "external generator process-fence reconciliation is required") || record.TimeslotID == nil || *record.TimeslotID != slotID {
				t.Fatalf("unprovable stopping run released its reservation: %+v, %v", record, err)
			}
			if test.Status != "STOPPING" || test.EndedAt != nil {
				t.Fatalf("in-memory unprovable run became terminal: %s %v", test.Status, test.EndedAt)
			}
		})
	}
}

func TestStoppingRunWithUnprovableCheckpointDoesNotRetryForever(t *testing.T) {
	runner, store, leadership := newAdoptionTestRunner(t)
	test := storedContinueRun(t, runner, store, "unprovable-stop", "continue")
	test.Status = "STOPPING"
	if _, err := store.UpdateTestStatusIfActive(test.ID, "STOPPING", nil, ""); err != nil {
		t.Fatal(err)
	}
	checkpoint, _, err := decodeRunContinuityCheckpoint(test.continuityCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	_, generation := leadership.executionLeaseTerm()
	checkpoint.ExecutionGeneration = generation // A newer-or-equal term has no safe token rotation.
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	setStoredAdoptionTestCheckpoint(t, runner, store, test, string(raw))
	runner.tests[test.ID] = test
	if err := runner.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	runner.resumeAdoptedRuns()
	record, err := store.GetTest(test.ID)
	if err != nil || record.Status != "STOPPING" || record.EndTime != nil || !strings.Contains(record.Error, "pending ABORTED cleanup proof") {
		t.Fatalf("unprovable stop outcome = %+v, %v", record, err)
	}
	runner.terminalRetryMu.Lock()
	queued := len(runner.terminalRetrying)
	runner.terminalRetryMu.Unlock()
	if queued != 0 {
		t.Fatalf("permanently unprovable stop queued %d futile retries", queued)
	}
}

func TestStoppingRunRetriesTransientAdoptionKeyFailure(t *testing.T) {
	for _, operation := range []string{"unwrap", "wrap"} {
		t.Run(operation, func(t *testing.T) {
			runner, store, _ := newAdoptionTestRunner(t)
			client := &stoppingFakeClient{adoptionFakeClient: adoptionFakeClient{outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED}}
			runner.execution = newDistributedExecutionBackend([]string{"gen-a:9091"}, time.Second, func(context.Context, string) (generatorClient, error) {
				return client, nil
			})
			test := storedContinueRun(t, runner, store, "key-outage-stop", "continue")
			test.Status = "STOPPING"
			if _, err := store.UpdateTestStatusIfActive(test.ID, "STOPPING", nil, ""); err != nil {
				t.Fatal(err)
			}
			runner.tests[test.ID] = test
			wrapper := &flakyAdoptionKeyWrapper{KeyWrapper: runner.continuityKeyWrapper}
			if operation == "unwrap" {
				wrapper.failUnwrap.Store(true)
			} else {
				wrapper.failWrap.Store(true)
			}
			runner.continuityKeyWrapper = wrapper
			if err := runner.adoptOrphanedRuns(); !errors.Is(err, errRunAdoptionKeyAccess) {
				t.Fatalf("key-service outage did not request acquisition retry: %v", err)
			}
			record, err := store.GetTest(test.ID)
			if err != nil || record.Status != "STOPPING" || record.EndTime != nil || test.adoptedResume != nil {
				t.Fatalf("transient key failure changed run state: %+v, %v", record, err)
			}
			wrapper.failUnwrap.Store(false)
			wrapper.failWrap.Store(false)
			if err := runner.adoptOrphanedRuns(); err != nil {
				t.Fatalf("same-leader adoption did not recover: %v", err)
			}
			runner.resumeAdoptedRuns()
			record, err = store.GetTest(test.ID)
			if err != nil || record.Status != "ABORTED" || record.EndTime == nil {
				t.Fatalf("recovered stop did not finish: %+v, %v", record, err)
			}
		})
	}
}

func TestStoppingRunMalformedAdoptionTokenDoesNotBlockAcquisition(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	test := storedContinueRun(t, runner, store, "missing-adoption-token", "continue")
	test.Status = "STOPPING"
	if _, err := store.UpdateTestStatusIfActive(test.ID, "STOPPING", nil, ""); err != nil {
		t.Fatal(err)
	}
	checkpoint, _, err := decodeRunContinuityCheckpoint(test.continuityCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.RunSupervisionToken.Ciphertext = ""
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	setStoredAdoptionTestCheckpoint(t, runner, store, test, string(raw))
	runner.tests[test.ID] = test
	if err := runner.adoptOrphanedRuns(); err != nil {
		t.Fatalf("structurally corrupt token blocked all acquisition work: %v", err)
	}
	runner.resumeAdoptedRuns()
	record, err := store.GetTest(test.ID)
	if err != nil || record.Status != "STOPPING" || record.EndTime != nil {
		t.Fatalf("malformed token falsely proved generator stop: %+v, %v", record, err)
	}
}

func TestAdoptedPhaseBFailureWaitsForEveryForceStop(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	test := storedContinueRun(t, runner, store, "phase-b-failure", "continue")
	checkpoint, _, err := decodeRunContinuityCheckpoint(test.continuityCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.Shards["ga"] = append(checkpoint.Shards["ga"], GeneratorShardSummary{Group: "ga", Target: "gen-b:9091", VUsers: 5})
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	setStoredAdoptionTestCheckpoint(t, runner, store, test, string(raw))
	runner.tests[test.ID] = test
	a, b := &phaseBForceStopFakeClient{adoptionFakeClient: adoptionFakeClient{outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED}},
		&phaseBForceStopFakeClient{adoptionFakeClient: adoptionFakeClient{outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED}}
	a.allowForce.Store(true)
	var aGone atomic.Bool
	backend := newDistributedExecutionBackend([]string{"gen-a:9091", "gen-b:9091"}, 200*time.Millisecond, func(_ context.Context, target string) (generatorClient, error) {
		if target == "gen-a:9091" {
			if aGone.Load() {
				return nil, errors.New("stopped generator was removed")
			}
			return a, nil
		}
		return b, nil
	})
	backend.enableRunSupervision(time.Minute, 10*time.Second)
	runner.execution = backend
	if err := runner.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	runner.resumeAdoptedRuns() // supervision re-claim fails; Phase B must force-stop both adopters.
	record, err := store.GetTest(test.ID)
	if err != nil || record.Status != "STOPPING" || record.EndTime != nil {
		t.Fatalf("one unreachable adopter was prematurely terminal: %+v, %v", record, err)
	}
	checkpoint, _, err = decodeRunContinuityCheckpoint(record.ContinuityCheckpoint)
	if err != nil || !slices.Contains(checkpoint.StoppedTargets, "gen-a:9091") || slices.Contains(checkpoint.StoppedTargets, "gen-b:9091") {
		t.Fatalf("partial force-stop proof was not durable: %+v, %v", checkpoint.StoppedTargets, err)
	}
	if a.forceStops.Load() == 0 || b.forceStops.Load() == 0 || a.gracefulStops.Load() != 0 {
		t.Fatalf("Phase B stop attempts were not forced: a=%d b=%d graceful=%d", a.forceStops.Load(), b.forceStops.Load(), a.gracefulStops.Load())
	}
	// The failed resume canceled local renewal, but its retry still owns this
	// same-term runtime. A routine HA refresh must retain the pointer and the
	// first target's durable stop proof so the retry can finish after B returns.
	test.CreatedAt, test.ProjectID = record.StartTime, record.ProjectID
	if test.executionCtx == nil || test.executionCtx.Err() == nil {
		t.Fatal("Phase B failure did not cancel local renewal before retry")
	}
	if err := runner.reloadStoredTests(); err != nil {
		t.Fatal(err)
	}
	if runner.tests[test.ID] != test || test.Status != "STOPPING" || !strings.Contains(test.Error, "pending FAILED cleanup proof:") {
		t.Fatalf("same-leader refresh detached the adopted stop-proof worker: pointer=%p want=%p status=%s reason=%q", runner.tests[test.ID], test, test.Status, test.Error)
	}
	checkpoint, _, err = decodeRunContinuityCheckpoint(test.continuityCheckpoint)
	if err != nil || !slices.Contains(checkpoint.StoppedTargets, "gen-a:9091") {
		t.Fatalf("same-leader refresh discarded per-target stop proof: %+v, %v", checkpoint.StoppedTargets, err)
	}
	aGone.Store(true)
	b.allowForce.Store(true)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		record, err = store.GetTest(test.ID)
		if err == nil && record.Status == "FAILED" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || record.Status != "FAILED" || record.EndTime == nil {
		t.Fatalf("fully force-stopped adopters did not resolve failure: %+v, %v", record, err)
	}
	if a.forceStops.Load() != 1 {
		t.Fatalf("successor needlessly retried vanished proven target: %d", a.forceStops.Load())
	}
}

func TestAdoptedRunStopUncertaintyMessageIsScrubbedAndBounded(t *testing.T) {
	resume := &adoptedRunResume{finishStatus: "FAILED", finishMessage: "previous failure token=topsecret"}
	message := adoptedRunStopUncertaintyMessage(resume, errors.New(strings.Repeat("generator unavailable ", 1000)+" password=hunter2"))
	if !strings.HasPrefix(message, "controller failover: pending FAILED cleanup proof:") || len(message) > maxAdoptedRunStopErrorBytes {
		t.Fatalf("unbounded or misclassified cleanup message: %q", message)
	}
	if strings.Contains(message, "topsecret") || strings.Contains(message, "hunter2") {
		t.Fatalf("cleanup message leaked credentials: %q", message)
	}
}

func TestFailPolicyStoppingRunStillRequiresProcessFence(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	test := storedContinueRun(t, runner, store, "fail-stopping", "")
	test.Status = "STOPPING"
	if runner.isRunAdoptionCandidate(test) {
		t.Fatal("fail-policy stopping run treated as an adoption candidate")
	}
	if err := runner.reconcileStoredActiveTest(test); err != nil || test.Status != "STOPPING" {
		t.Fatalf("fail-policy stopping run after reconcile = %s (%v), want STOPPING", test.Status, err)
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
