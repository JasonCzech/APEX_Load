package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// shortenFinalizingWait bounds the successor's wait for finalizing runs.
func (f *evacuationRollbackFixture) shortenFinalizingWait(wait time.Duration) {
	f.tr.execution.(*distributedExecutionBackend).runAdoptionFinalizingWait = wait
}

// assertRequeried checks that gen got the first claim and then requeries:
// claims of Phase A's generation carrying only this run's adoption.
func assertRequeried(t *testing.T, gen *rollbackGeneratorClient, testID string, want int, generation uint64) {
	t.Helper()
	claims, _ := gen.snapshot()
	if len(claims) != want {
		t.Fatalf("claims = %d, want %d", len(claims), want)
	}
	for index, claimed := range gen.claimedGenerations() {
		if claimed != generation {
			t.Fatalf("claim %d was at generation %d, want Phase A's %d", index, claimed, generation)
		}
	}
	for _, claim := range claims {
		if len(claim) != 1 || claim[0].TestID != testID || !slices.Equal(claim[0].NewToken, claims[0][0].NewToken) {
			t.Fatalf("requery carried %+v, want the first claim's adoption of %s", claim, testID)
		}
	}
}

// assertRequeriedUntilTheWaitEnded checks that gen was asked again, at Phase
// A's generation and for this run only, until the wait ended.
func assertRequeriedUntilTheWaitEnded(t *testing.T, gen *rollbackGeneratorClient, testID string, generation uint64) {
	t.Helper()
	claims := gen.claimCount()
	// A wait of at most 300 ms, polled every 100 ms, makes at most four
	// requeries; the margin absorbs a slow runner.
	if claims < 2 || claims > 8 {
		t.Fatalf("claims = %d, want a few requeries within the wait", claims)
	}
	assertRequeried(t, gen, testID, claims, generation)
}

// A participant whose generator still finalizes the run's terminal outcome
// is asked again, within a bounded wait, and judged on the published outcome
// through the existing paths.
func TestEvacuationRecoveryWaitsForAFinalizingCompletion(t *testing.T) {
	t.Run("roll-forward replacement completed naturally", func(t *testing.T) {
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		completeNaturally(f.spareGen, rollForwardCommitTarget)
		f.spareGen.finalizing = 2
		capture := &journalSwapCaptureStore{SQLiteStore: f.store}
		f.tr.testStore = capture
		owner, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		assertRequeried(t, f.spareGen, f.test.ID, 3, generation)
		if swapped := capture.swappedCompletedTargets(t); !slices.Equal(swapped, []string{"gen-b"}) {
			t.Fatalf("the swapped checkpoint's completed targets = %v, want gen-b", swapped)
		}
		resume := f.test.adoptedResume
		if resume == nil || resume.finishStatus != "" || resume.adopted["gen-c"] == nil || resume.completed["gen-b"] == nil || resume.adopted["gen-b"] != nil {
			t.Fatalf("finalized replacement not rolled forward: %+v", resume)
		}
		if completed := f.durableCompletedTargets(t, owner, generation); !slices.Contains(completed, "gen-b") {
			t.Fatalf("completed targets = %v, want gen-b", completed)
		}
		// The survivor was adopted by the first claim and asked nothing more.
		if claims, _ := f.survivorGen.snapshot(); len(claims) != 1 {
			t.Fatalf("survivor claims = %d, want 1", len(claims))
		}
	})
	t.Run("roll-forward survivor completed naturally", func(t *testing.T) {
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		completeNaturally(f.survivorGen, f.test.Duration)
		f.survivorGen.finalizing = 1
		owner, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		assertRequeried(t, f.survivorGen, f.test.ID, 2, generation)
		resume := f.test.adoptedResume
		if resume == nil || resume.finishStatus != "" || resume.adopted["gen-b"] == nil || resume.completed["gen-c"] == nil {
			t.Fatalf("finalized survivor blocked the roll-forward: %+v", resume)
		}
		if completed := f.durableCompletedTargets(t, owner, generation); !slices.Contains(completed, "gen-c") {
			t.Fatalf("completed targets = %v, want gen-c", completed)
		}
	})
	t.Run("rollback source completed naturally", func(t *testing.T) {
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{phase: runEvacuationPhaseStaged})
		completeNaturally(f.sourceGen, f.test.Duration)
		f.sourceGen.finalizing, f.sourceGen.finalizingAs = 1, pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST
		owner, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		assertRequeried(t, f.sourceGen, f.test.ID, 2, generation)
		resume := f.test.adoptedResume
		if resume == nil || resume.finishStatus != "" || resume.completed["gen-a"] == nil || resume.adopted["gen-c"] == nil {
			t.Fatalf("finalized source not rolled back: %+v", resume)
		}
		if completed := f.durableCompletedTargets(t, owner, generation); !slices.Contains(completed, "gen-a") {
			t.Fatalf("completed targets = %v, want gen-a", completed)
		}
	})
	t.Run("roll-forward replacement stopped by a controller", func(t *testing.T) {
		// The published outcome is judged as it always was.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		completeWithCause(f.spareGen, pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP, 0)
		f.spareGen.finalizing = 1
		_, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		assertRequeried(t, f.spareGen, f.test.ID, 2, generation)
		f.assertFailedRollForwardHeld(t, generation, "replacement gen-b reports a completed run the journal never recorded: it was stopped by a controller", true, false)
	})
	t.Run("still finalizing when the wait ends", func(t *testing.T) {
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		completeNaturally(f.spareGen, rollForwardCommitTarget)
		f.spareGen.finalizing = 1 << 20
		f.shortenFinalizingWait(300 * time.Millisecond)
		_, generation := f.tr.leadership.executionLeaseTerm()
		started := time.Now()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(started); elapsed < 300*time.Millisecond || elapsed > 2*time.Second {
			t.Fatalf("the wait took %s", elapsed)
		}
		assertRequeriedUntilTheWaitEnded(t, f.spareGen, f.test.ID, generation)
		f.assertFailedRollForwardHeld(t, generation, "gen-b: ALREADY_STOPPED the run's executions ended and its terminal outcome is still being finalized", true, false)
		if adjustments := f.spareGen.durationAdjustments(); len(adjustments) != 0 {
			t.Fatalf("a held replacement was adjusted: %+v", adjustments)
		}
	})
	t.Run("rollback source still finalizing when the wait ends", func(t *testing.T) {
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{phase: runEvacuationPhaseStaged})
		completeNaturally(f.sourceGen, f.test.Duration)
		f.sourceGen.finalizing = 1 << 20
		f.shortenFinalizingWait(200 * time.Millisecond)
		_, generation := f.tr.leadership.executionLeaseTerm()
		started := time.Now()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(started); elapsed < 200*time.Millisecond || elapsed > 2*time.Second {
			t.Fatalf("the wait took %s", elapsed)
		}
		assertRequeriedUntilTheWaitEnded(t, f.sourceGen, f.test.ID, generation)
		guard, _ := f.durableGuard(t)
		record, err := f.store.GetTest(f.test.ID)
		if err != nil || guard.Phase != runEvacuationPhaseRollingBack || guard.RollbackGeneration != generation || record.Status != "STOPPING" ||
			!strings.Contains(record.Error, "gen-a: ALREADY_STOPPED the run's executions ended and its terminal outcome is still being finalized") || f.test.adoptedResume != nil {
			t.Fatalf("rollback with a finalizing source = phase %q, %+v, %v", guard.Phase, record, err)
		}
	})
	t.Run("older generator", func(t *testing.T) {
		// A generator that predates the flag answers as it always did: no
		// requery, and the run is held.
		f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
		f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED
		_, generation := f.tr.leadership.executionLeaseTerm()
		if err := f.tr.adoptOrphanedRuns(); err != nil {
			t.Fatal(err)
		}
		if claims := f.spareGen.claimCount(); claims != 1 {
			t.Fatalf("claims = %d, want 1", claims)
		}
		f.assertFailedRollForwardHeld(t, generation, "gen-b: ALREADY_STOPPED", true, false)
	})
}

// A restart takes an old replacement's UNKNOWN_TEST as proof that it runs
// nothing. Flagged finalizing past the wait, the same answer is not taken:
// the outcome it will publish is unknown, so the run is held.
func TestEvacuationRestartRefusesAStillFinalizingReplacement(t *testing.T) {
	f, spare := newEvacuationRestartFixture(t, restartOptions{rollForwardOptions: rollForwardOptions{phase: runEvacuationPhaseReplacementCommitting}})
	f.spareGen.outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST
	f.spareGen.finalizing, f.spareGen.finalizingAs = 1<<20, pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST
	f.shortenFinalizingWait(200 * time.Millisecond)
	_, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	f.assertFailedRollForwardHeld(t, generation, "the replacement gen-b could not be restarted: its run is still finalizing its terminal outcome", true, false)
	if assignments, _ := spare.recorder.snapshot(); len(assignments) != 0 {
		t.Fatalf("a refused restart assigned the share: %+v", assignments)
	}
}

func TestRunEvacuationRestartAbsenceRefusesFinalizing(t *testing.T) {
	for _, outcome := range []pb.RunAdoptionOutcome{pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST, pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED} {
		if why := runEvacuationRestartAbsence(generatorrpc.RunAdoptionResult{Outcome: outcome, Finalizing: true}); !strings.Contains(why, "still finalizing") {
			t.Fatalf("%s while finalizing = %q", outcome, why)
		}
	}
	if why := runEvacuationRestartAbsence(generatorrpc.RunAdoptionResult{Outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST}); why != "" {
		t.Fatalf("UNKNOWN_TEST without the flag = %q", why)
	}
}

// finalizingObservingClient is an in-process generator's client that records
// what each epoch claim answered for the run.
type finalizingObservingClient struct {
	*generatorrpc.Client
	mu          sync.Mutex
	answers     []generatorrpc.RunAdoptionResult
	generations []uint64
	// answered runs after each claim with the number of claims so far.
	answered func(int)
}

func (c *finalizingObservingClient) ClaimExecutionEpochWithAdoptions(ctx context.Context, generation uint64, owner string, ttl time.Duration, adoptions []generatorrpc.RunAdoption) (generatorrpc.ExecutionEpochResult, error) {
	result, err := c.Client.ClaimExecutionEpochWithAdoptions(ctx, generation, owner, ttl, adoptions)
	c.mu.Lock()
	c.answers = append(c.answers, result.Adoptions...)
	c.generations = append(c.generations, generation)
	count := len(c.answers)
	c.mu.Unlock()
	if c.answered != nil {
		c.answered(count)
	}
	return result, err
}

func (c *finalizingObservingClient) snapshot() []generatorrpc.RunAdoptionResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]generatorrpc.RunAdoptionResult(nil), c.answers...)
}

// A committed replacement whose staged duration ran out is still finalizing
// its archive on an in-process generator when the successor claims it. Its
// generator says so; the successor waits and rolls forward with the
// replacement completed, or holds the run when the outcome is not published
// within the wait.
func TestEvacuationRollForwardWaitsForARealFinalizingReplacement(t *testing.T) {
	t.Run("published within the wait", func(t *testing.T) { runRealFinalizingRollForward(t, true) })
	t.Run("not published within the wait", func(t *testing.T) { runRealFinalizingRollForward(t, false) })
}

func runRealFinalizingRollForward(t *testing.T, publish bool) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(target.Close)
	entered := make(chan string, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	letFinalize := func() { releaseOnce.Do(func() { close(release) }) }
	gate := func(testID string) {
		select {
		case entered <- testID:
		default:
		}
		<-release
	}
	client, service := newTestGeneratorRPCClient(t, "gen-b", 10, generatorrpc.WithExecution(),
		generatorrpc.WithExecutionEpochStatePath(filepath.Join(t.TempDir(), "epoch.json")), generatorrpc.WithArchiveFinalizationGate(gate))
	// Registered after the generator, so it runs before its shutdown.
	t.Cleanup(letFinalize)
	ctx := context.Background()
	stagedRemaining := 300 * time.Millisecond
	commitTarget := rollForwardStagedElapsed + stagedRemaining
	observing := &finalizingObservingClient{Client: client}
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{noCapacity: true, commitTarget: commitTarget}, func(f *evacuationRollbackFixture) {
		f.backendTimeout = 10 * time.Second
		if _, err := client.ClaimExecutionEpoch(ctx, f.oldGen, f.oldOwner, time.Minute); err != nil {
			t.Fatalf("lost leader epoch claim: %v", err)
		}
		content := []byte(fmt.Sprintf(`{"name":"finalizing","protocol":"http","config":{"base_url":%q,"think_time":{"type":"fixed","mean_ms":50}},"actions":[{"name":"request","type":"GET","target":"/","think_time_after":true}]}`, target.URL))
		digest := sha256.Sum256(content)
		hash := hex.EncodeToString(digest[:])
		if _, err := client.DistributeScript(ctx, generatorrpc.Script{ID: "script", ExecutionGeneration: f.oldGen, Content: content, ContentHash: hash}); err != nil {
			t.Fatalf("distribute script: %v", err)
		}
		assign, err := client.AssignWork(ctx, generatorrpc.Assignment{
			TestID: f.test.ID, GroupName: "ga", ScriptID: "script", ScriptContent: content, ExpectedScriptHash: hash,
			VUserCount: 1, ExecutionGeneration: f.oldGen, TargetDuration: stagedRemaining, DurationOffset: rollForwardStagedElapsed,
			RunSupervisionToken: f.token, AdoptionGrace: 30 * time.Second,
		})
		if err != nil || !assign.Accepted {
			t.Fatalf("assign work = (%+v, %v)", assign, err)
		}
		if claim, err := client.ClaimRunSupervision(ctx, generatorrpc.RunSupervisionClaim{TestID: f.test.ID, ExecutionGeneration: f.oldGen, Token: f.token, RequestedTTL: time.Minute, ExecutionLeaseOwnerID: f.oldOwner}); err != nil || !claim.Accepted {
			t.Fatalf("supervision claim = (%+v, %v)", claim, err)
		}
		if commit, err := client.CommitWorkSupervisedAt(ctx, f.test.ID, f.oldGen, time.Now().Add(500*time.Millisecond), f.token); err != nil || !commit.Accepted {
			t.Fatalf("commit = (%+v, %v)", commit, err)
		}
		// The staged duration runs out; the generator removes the assignment
		// and holds at the gate, before the archive and the tombstone.
		select {
		case <-entered:
		case <-time.After(20 * time.Second):
			t.Fatal("the replacement did not finish its staged duration")
		}
		if service.ActiveVUsers() != 0 {
			t.Fatalf("a finalizing replacement has %d active VUsers", service.ActiveVUsers())
		}
		f.replacement = observing
	})
	if publish {
		// Let the archive finalize once the first claim was answered.
		observing.answered = func(count int) {
			if count == 1 {
				go func() {
					time.Sleep(200 * time.Millisecond)
					letFinalize()
				}()
			}
		}
	} else {
		f.shortenFinalizingWait(500 * time.Millisecond)
	}
	owner, generation := f.tr.leadership.executionLeaseTerm()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	answers := observing.snapshot()
	if len(answers) < 2 || !answers[0].Finalizing || answers[0].Outcome != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED {
		t.Fatalf("answers = %+v, want a finalizing ALREADY_STOPPED first and then requeries", answers)
	}
	// Every requery was a claim of Phase A's generation, so the generator's
	// epoch is still that generation: a claim of it is accepted as current.
	observing.mu.Lock()
	generations := append([]uint64(nil), observing.generations...)
	observing.mu.Unlock()
	for index, claimed := range generations {
		if claimed != generation {
			t.Fatalf("claim %d was at generation %d, want Phase A's %d", index, claimed, generation)
		}
	}
	// Asked of the service itself: a held run's cleanup closes the client.
	if epoch, err := service.ClaimExecutionEpoch(ctx, &pb.ExecutionEpochClaim{ExecutionGeneration: generation, LeaseTtlMilliseconds: 60_000, ExecutionLeaseOwnerId: owner}); err != nil || !epoch.GetAccepted() || epoch.GetCurrentGeneration() != generation {
		t.Fatalf("the generator's epoch after Phase A = (%+v, %v), want generation %d", epoch, err, generation)
	}
	if !publish {
		f.assertFailedRollForwardHeld(t, generation, "gen-b: ALREADY_STOPPED the run's executions ended and its terminal outcome is still being finalized", true, false)
		return
	}
	last := answers[len(answers)-1]
	if last.Finalizing || last.Outcome != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED ||
		last.TerminalCause != pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED || last.CompletedDuration != commitTarget {
		t.Fatalf("published answer = %+v", last)
	}
	resume := f.test.adoptedResume
	if resume == nil || resume.finishStatus != "" || resume.completed["gen-b"] == nil || resume.adopted["gen-b"] != nil || resume.adopted["gen-c"] == nil {
		t.Fatalf("finalized replacement not rolled forward: %+v", resume)
	}
	if completed := f.durableCompletedTargets(t, owner, generation); !slices.Contains(completed, "gen-b") {
		t.Fatalf("completed targets = %v, want gen-b", completed)
	}
}

// scriptedClaimClient answers each epoch claim from its script and records
// the adoptions every claim carried.
type scriptedClaimClient struct {
	generatorClient
	mu          sync.Mutex
	claims      [][]generatorrpc.RunAdoption
	generations []uint64
	answers     func(claim int, adoptions []generatorrpc.RunAdoption) []generatorrpc.RunAdoptionResult
	// fail, when set, fails the claims it reports an error for.
	fail func(claim int) error
}

func (c *scriptedClaimClient) ClaimExecutionEpoch(_ context.Context, generation uint64, _ string, _ time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	return generatorrpc.ExecutionEpochResult{Accepted: true, CurrentGeneration: generation, AdoptionSupported: true}, nil
}

func (c *scriptedClaimClient) ClaimExecutionEpochWithAdoptions(_ context.Context, generation uint64, _ string, _ time.Duration, adoptions []generatorrpc.RunAdoption) (generatorrpc.ExecutionEpochResult, error) {
	c.mu.Lock()
	c.claims = append(c.claims, append([]generatorrpc.RunAdoption(nil), adoptions...))
	c.generations = append(c.generations, generation)
	claim := len(c.claims)
	c.mu.Unlock()
	if c.fail != nil {
		if err := c.fail(claim); err != nil {
			return generatorrpc.ExecutionEpochResult{}, err
		}
	}
	return generatorrpc.ExecutionEpochResult{Accepted: true, CurrentGeneration: generation, AdoptionSupported: true, Adoptions: c.answers(claim, adoptions)}, nil
}

// alwaysFinalizing answers every adoption as still finalizing.
func alwaysFinalizing(_ int, adoptions []generatorrpc.RunAdoption) []generatorrpc.RunAdoptionResult {
	results := make([]generatorrpc.RunAdoptionResult, 0, len(adoptions))
	for _, adoption := range adoptions {
		results = append(results, generatorrpc.RunAdoptionResult{TestID: adoption.TestID, Outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED, Finalizing: true})
	}
	return results
}

// finalizingUntil answers as still finalizing until claim published, and
// then with a natural completion.
func finalizingUntil(published int) func(int, []generatorrpc.RunAdoption) []generatorrpc.RunAdoptionResult {
	return func(claim int, adoptions []generatorrpc.RunAdoption) []generatorrpc.RunAdoptionResult {
		if claim < published {
			return alwaysFinalizing(claim, adoptions)
		}
		results := make([]generatorrpc.RunAdoptionResult, 0, len(adoptions))
		for _, adoption := range adoptions {
			results = append(results, generatorrpc.RunAdoptionResult{TestID: adoption.TestID, Outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED, TerminalState: "COMPLETE",
				TerminalCause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED, CompletedDuration: time.Minute})
		}
		return results
	}
}

// A failed requery keeps the earlier answers and is tried again; the next
// requery's published answer replaces the flagged one.
func TestFailedRequeryIsTriedAgain(t *testing.T) {
	client := &scriptedClaimClient{answers: finalizingUntil(3), fail: func(claim int) error {
		if claim == 2 {
			return status.Error(codes.Unavailable, "requery lost")
		}
		return nil
	}}
	b := newDistributedExecutionBackend([]string{"gen-a"}, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result := b.claimRunAdoptions(ctx, map[string][]generatorrpc.RunAdoption{"gen-a": {{TestID: "run"}}}, 4, "owner", time.Now().Add(time.Minute))["gen-a"]
	if result.err != nil {
		t.Fatal(result.err)
	}
	if got := result.outcomes["run"]; got.Finalizing || got.Outcome != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED {
		t.Fatalf("answer after a failed requery = %+v, want the published completion", got)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.claims) != 3 || slices.ContainsFunc(client.generations, func(generation uint64) bool { return generation != 4 }) {
		t.Fatalf("claims = %d at generations %v, want three at 4", len(client.claims), client.generations)
	}
}

// The wait takes at most a third of what is left of the claim context, which
// Phase A shares with every candidate's later steps.
func TestFinalizingWaitLeavesTheClaimContextMostOfItsTime(t *testing.T) {
	client := &scriptedClaimClient{answers: alwaysFinalizing}
	b := newDistributedExecutionBackend([]string{"gen-a"}, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
	b.runAdoptionFinalizingWait = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := b.claimRunAdoptions(ctx, map[string][]generatorrpc.RunAdoption{"gen-a": {{TestID: "run"}}}, 4, "owner", time.Now().Add(time.Minute))["gen-a"]
	elapsed := time.Since(started)
	if result.err != nil || !result.outcomes["run"].Finalizing {
		t.Fatalf("result = %+v", result)
	}
	if elapsed >= time.Second || ctx.Err() != nil {
		t.Fatalf("the wait took %s of a 1.5 s context (context error %v), want at most a third", elapsed, ctx.Err())
	}
}

// The requery carries only the finalizing runs, and only their answers are
// replaced: a run the first claim refused keeps that answer, which is the
// one the recoveries judge.
func TestAwaitFinalizingRunAdoptionsReplacesOnlyFinalizingAnswers(t *testing.T) {
	client := &scriptedClaimClient{answers: func(claim int, adoptions []generatorrpc.RunAdoption) []generatorrpc.RunAdoptionResult {
		results := make([]generatorrpc.RunAdoptionResult, 0, len(adoptions))
		for _, adoption := range adoptions {
			switch {
			case adoption.TestID == "refused" && claim == 1:
				results = append(results, generatorrpc.RunAdoptionResult{TestID: "refused", Outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED})
			case adoption.TestID == "refused":
				// A later claim would read the drained run differently.
				results = append(results, generatorrpc.RunAdoptionResult{TestID: "refused", Outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED, TerminalState: "COMPLETE"})
			case claim < 3:
				results = append(results, generatorrpc.RunAdoptionResult{TestID: adoption.TestID, Outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED, Finalizing: true})
			default:
				results = append(results, generatorrpc.RunAdoptionResult{TestID: adoption.TestID, Outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED, TerminalState: "COMPLETE",
					TerminalCause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED, CompletedDuration: time.Minute})
			}
		}
		return results
	}}
	b := newDistributedExecutionBackend([]string{"gen-a"}, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
	adoptions := []generatorrpc.RunAdoption{{TestID: "finalizing"}, {TestID: "refused"}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	results := b.claimRunAdoptions(ctx, map[string][]generatorrpc.RunAdoption{"gen-a": adoptions}, 4, "owner", time.Now().Add(time.Minute))
	result := results["gen-a"]
	if result.err != nil {
		t.Fatal(result.err)
	}
	if got := result.outcomes["finalizing"]; got.Finalizing || got.Outcome != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED || got.TerminalCause != pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED {
		t.Fatalf("finalizing run = %+v, want its published completion", got)
	}
	if got := result.outcomes["refused"]; got.Outcome != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED {
		t.Fatalf("refused run = %+v, want the first claim's answer", got)
	}
	client.mu.Lock()
	claims := client.claims
	client.mu.Unlock()
	if len(claims) != 3 {
		t.Fatalf("claims = %d, want the first and two requeries", len(claims))
	}
	for _, requery := range claims[1:] {
		if len(requery) != 1 || requery[0].TestID != "finalizing" {
			t.Fatalf("a requery carried %+v, want only the finalizing run", requery)
		}
	}
}

// slotProbeClient answers every adoption as still finalizing. A requery
// waits until every run's first claim arrived; it notes when one never did,
// which happens only if waiting generators hold the claim slots.
type slotProbeClient struct {
	generatorClient
	mu       sync.Mutex
	seen     map[string]bool
	total    int
	all      chan struct{}
	starved  atomic.Bool
	requeued atomic.Int32
}

func (c *slotProbeClient) ClaimExecutionEpochWithAdoptions(_ context.Context, generation uint64, _ string, _ time.Duration, adoptions []generatorrpc.RunAdoption) (generatorrpc.ExecutionEpochResult, error) {
	c.mu.Lock()
	first := !c.seen[adoptions[0].TestID]
	c.seen[adoptions[0].TestID] = true
	if first && len(c.seen) == c.total {
		close(c.all)
	}
	c.mu.Unlock()
	if !first {
		c.requeued.Add(1)
		select {
		case <-c.all:
		case <-time.After(3 * time.Second):
			c.starved.Store(true)
		}
	}
	return generatorrpc.ExecutionEpochResult{Accepted: true, CurrentGeneration: generation, AdoptionSupported: true, Adoptions: alwaysFinalizing(0, adoptions)}, nil
}

// The wait gives up its claim slot: with more generators than slots, every
// generator's first claim, which carries its adoptions, is sent while the
// generators before it still wait.
func TestFinalizingWaitDoesNotHoldAClaimSlot(t *testing.T) {
	targets := make([]string, 0, maxGeneratorEpochKeeperWorkers+1)
	byTarget := make(map[string][]generatorrpc.RunAdoption)
	for index := range maxGeneratorEpochKeeperWorkers + 1 {
		target := fmt.Sprintf("gen-%02d", index)
		targets = append(targets, target)
		byTarget[target] = []generatorrpc.RunAdoption{{TestID: "run-" + target}}
	}
	client := &slotProbeClient{seen: map[string]bool{}, total: len(targets), all: make(chan struct{})}
	b := newDistributedExecutionBackend(targets, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
	b.runAdoptionFinalizingWait = 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	results := b.claimRunAdoptions(ctx, byTarget, 4, "owner", time.Now().Add(time.Minute))
	if client.starved.Load() || client.requeued.Load() == 0 {
		t.Fatalf("a requery waited for a first claim that never came (starved %v, requeries %d)", client.starved.Load(), client.requeued.Load())
	}
	for target, result := range results {
		if result.err != nil || !result.outcomes["run-"+target].Finalizing {
			t.Fatalf("%s = %+v", target, result)
		}
	}
}

func (c *scriptedClaimClient) Close() error { return nil }

func (c *slotProbeClient) Close() error { return nil }

func (c *slotProbeClient) ClaimExecutionEpoch(_ context.Context, generation uint64, _ string, _ time.Duration) (generatorrpc.ExecutionEpochResult, error) {
	return generatorrpc.ExecutionEpochResult{Accepted: true, CurrentGeneration: generation, AdoptionSupported: true}, nil
}

// The wait is the configured one, or the default, and never more than a third
// of what is left of the claim context.
func TestFinalizingWaitBound(t *testing.T) {
	b := newDistributedExecutionBackend([]string{"gen-a"}, time.Second, nil)
	if wait := b.finalizingWait(context.Background()); wait != defaultRunAdoptionFinalizingWait {
		t.Fatalf("default wait = %s, want %s", wait, defaultRunAdoptionFinalizingWait)
	}
	b.runAdoptionFinalizingWait = 700 * time.Millisecond
	if wait := b.finalizingWait(context.Background()); wait != 700*time.Millisecond {
		t.Fatalf("configured wait = %s, want 700ms", wait)
	}
	b.runAdoptionFinalizingWait = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if wait := b.finalizingWait(ctx); wait > 500*time.Millisecond || wait < 400*time.Millisecond {
		t.Fatalf("wait with 1.5 s left = %s, want a third of it", wait)
	}
}

// hangingRequeryClient answers the first claim as still finalizing and lets
// every later claim hang until its context ends.
type hangingRequeryClient struct {
	scriptedClaimClient
}

func (c *hangingRequeryClient) ClaimExecutionEpochWithAdoptions(ctx context.Context, generation uint64, owner string, ttl time.Duration, adoptions []generatorrpc.RunAdoption) (generatorrpc.ExecutionEpochResult, error) {
	c.mu.Lock()
	c.claims = append(c.claims, adoptions)
	claim := len(c.claims)
	c.mu.Unlock()
	if claim > 1 {
		<-ctx.Done()
		return generatorrpc.ExecutionEpochResult{}, status.FromContextError(ctx.Err()).Err()
	}
	return generatorrpc.ExecutionEpochResult{Accepted: true, CurrentGeneration: generation, AdoptionSupported: true, Adoptions: alwaysFinalizing(claim, adoptions)}, nil
}

// A requery that hangs ends with the wait, not with the claim's transit bound.
func TestHungRequeryEndsWithTheWait(t *testing.T) {
	client := &hangingRequeryClient{}
	b := newDistributedExecutionBackend([]string{"gen-a"}, 10*time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
	b.runAdoptionFinalizingWait = 300 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	started := time.Now()
	result := b.claimRunAdoptions(ctx, map[string][]generatorrpc.RunAdoption{"gen-a": {{TestID: "run"}}}, 4, "owner", time.Now().Add(time.Minute))["gen-a"]
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("a hung requery held the pass for %s, past a 300 ms wait", elapsed)
	}
	if result.err != nil || !result.outcomes["run"].Finalizing {
		t.Fatalf("result = %+v", result)
	}
}
