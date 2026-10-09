package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

const (
	maxShutdownStopWorkers          = 16
	leadershipLossRemoteStopTimeout = 2 * time.Second
)

var errRunnerShuttingDown = errors.New("controller is shutting down")

// beginShutdown closes execution admission under the same lock used for every
// start transition. An in-flight start therefore either becomes visible to the
// subsequent active-test snapshot or observes this gate and declines to launch.
func (tr *TestRunner) beginShutdown() {
	if tr == nil {
		return
	}
	tr.stopRuntimeOverrideSecretAttemptReconciliation()
	tr.mu.Lock()
	tr.shuttingDown = true
	tr.mu.Unlock()
	// The archive worker's context is canceled immediately afterward by the
	// server. Close its retained directory stream without delaying listener
	// shutdown while a slow filesystem page is in progress.
	go tr.closeOrphanArchiveScanner()
}

func (tr *TestRunner) shutdownErrorLocked() error {
	if tr != nil && tr.shuttingDown {
		return errRunnerShuttingDown
	}
	return nil
}

func (tr *TestRunner) stopActiveTests(ctx context.Context) error {
	if tr == nil {
		return nil
	}
	if err := lockMutexWithContext(ctx, &tr.activeStopMu); err != nil {
		return err
	}
	defer tr.activeStopMu.Unlock()
	if err := lockRWMutexWithContext(ctx, &tr.mu); err != nil {
		return err
	}
	ids := make([]string, 0)
	for id, test := range tr.tests {
		if test == nil {
			continue
		}
		if test.continuityHandoff {
			// A successor adopts this run; stopping it here would defeat the
			// continue policy on every rolling restart.
			continue
		}
		switch test.Status {
		case "INITIALIZING", "RUNNING", "PAUSED", "STOPPING", "SHAKEOUT":
			ids = append(ids, id)
		}
	}
	tr.mu.Unlock()

	type stopResult struct {
		id  string
		err error
	}
	results := make(chan stopResult, len(ids))
	jobs := make(chan string, len(ids))
	for _, id := range ids {
		jobs <- id
	}
	close(jobs)
	workerCount := min(len(ids), maxShutdownStopWorkers)
	for range workerCount {
		go func() {
			for id := range jobs {
				results <- stopResult{id: id, err: tr.stopTestWithContext(ctx, id, false)}
			}
		}()
	}
	var errs []error
	for range ids {
		select {
		case <-ctx.Done():
			// A legacy backend may ignore context after stopTestWithContext has
			// already persisted STOPPING. Cancel only those durably transitioned
			// runs; never kill work whose persistence failed and rolled back.
			tr.cancelDurablyStoppingTests(ids)
			errs = append(errs, ctx.Err())
			return errors.Join(errs...)
		case result := <-results:
			if result.err != nil {
				errs = append(errs, fmt.Errorf("stop active test %s: %w", result.id, result.err))
			}
		}
	}
	return errors.Join(errs...)
}

type leadershipLossRuntime struct {
	testID string
	// handoff detaches the run without a generator Stop so a successor can
	// adopt it (markContinuityHandoff).
	handoff              bool
	executionGeneration  uint64
	runSupervisionToken  []byte
	runSupervisionKeeper runSupervisionKeeperHandle
	cancel               context.CancelFunc
	stopLocal            func()
	clientsByTarget      map[string]generatorClient
	archiveClients       map[string]generatorClient
	singleClient         generatorClient
	streamsByTarget      map[string]*generatorrpc.MetricsStream
	singleStream         *generatorrpc.MetricsStream
}

// teardownActiveTestsAfterLeadershipLoss is deliberately separate from the
// normal StopTest lifecycle. Once the lease is gone, lifecycle persistence is
// correctly fenced and must not be a prerequisite for stopping process-local or
// generator work. The durable active row is left untouched for the successor to
// reconcile; only stale runtime ownership is canceled and detached here.
func (tr *TestRunner) teardownActiveTestsAfterLeadershipLoss(ctx context.Context) error {
	if tr == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := lockMutexWithContext(ctx, &tr.activeStopMu); err != nil {
		return err
	}
	defer tr.activeStopMu.Unlock()
	if err := lockRWMutexWithContext(ctx, &tr.mu); err != nil {
		return err
	}
	runtimes := make([]leadershipLossRuntime, 0)
	for _, test := range tr.tests {
		if test == nil {
			continue
		}
		// Joint batches held for a sidecar (B5) will not be carried or
		// committed by this controller once it has lost leadership, whether
		// the run is still active or awaits its archive's publication retry.
		releaseHeldJointPublicationsLocked(test)
		activeStatus := false
		switch test.Status {
		case "INITIALIZING", "RUNNING", "PAUSED", "STOPPING", "SHAKEOUT":
			activeStatus = true
		}
		if !activeStatus && test.cancel == nil && test.multiEngine == nil && test.remoteClient == nil &&
			len(test.remoteClientsByTarget) == 0 && len(test.remoteArchiveClientsByTarget) == 0 && len(test.remoteSecretSnapshots) == 0 &&
			test.remoteRunSupervisionCancel == nil {
			continue
		}
		runtime := leadershipLossRuntime{
			testID:               test.ID,
			handoff:              test.continuityHandoff,
			executionGeneration:  test.executionGeneration,
			runSupervisionToken:  cloneRunSupervisionToken(test.remoteRunSupervisionToken),
			runSupervisionKeeper: detachRunSupervisionKeeperLocked(test),
			cancel:               test.cancel,
			clientsByTarget:      test.remoteClientsByTarget,
			archiveClients:       test.remoteArchiveClientsByTarget,
			singleClient:         test.remoteClient,
			streamsByTarget:      test.remoteMetricsStreamsByTarget,
			singleStream:         test.remoteMetricsStream,
		}
		if test.multiEngine != nil {
			engine := test.multiEngine
			runtime.stopLocal = engine.Stop
		}
		tr.cancelAllVUserScaleRampsLocked(test)
		test.continuityHandoff = false
		// Another controller may now hold the run's batches (B5).
		test.jointHeldFromStart = false
		test.cancel = nil
		test.executionCtx = nil
		test.multiEngine = nil
		test.executionBackend = nil
		test.goalControl = nil
		test.remoteClient = nil
		test.remoteClientsByTarget = nil
		test.remoteArchiveClientsByTarget = nil
		test.remoteMetricsStream = nil
		test.remoteMetricsStreamsByTarget = nil
		test.remoteGroupTargets = nil
		test.remoteGroupShards = nil
		test.remoteMetricsBatchesByTarget = nil
		test.remoteTransactionResponseTimeSLA = nil
		test.remoteTransactionResponseTimeSLALegacyByTarget = nil
		test.remoteTargetStates = nil
		test.remoteGoalAcceptedErrorWindows = nil
		test.remoteGoalPublishedErrorWindows = nil
		test.remoteFinalizing = false
		test.remoteMetricsFlushScheduled = false
		test.remoteGoalUpdateRunning = false
		test.remoteGoalUpdateRevision = 0
		test.remoteActivated = false
		test.remoteActivationPending = false
		test.remoteRunSupervisionFencedTargets = nil
		clear(test.remoteRunSupervisionToken[:])
		clearDistributedRunSecretSnapshotsLocked(test)
		test.remoteControlGeneration++
		runtimes = append(runtimes, runtime)
	}
	tr.mu.Unlock()

	for _, runtime := range runtimes {
		if runtime.cancel != nil {
			runtime.cancel()
		}
		if runtime.runSupervisionKeeper.cancel != nil {
			runtime.runSupervisionKeeper.cancel()
		}
	}
	var errs []error
	for _, runtime := range runtimes {
		if err := stopAndWaitRunSupervisionKeeper(ctx, runtime.runSupervisionKeeper); err != nil {
			errs = append(errs, fmt.Errorf("join stale run-supervision keeper for %s: %w", runtime.testID, err))
		}
		if runtime.stopLocal != nil {
			runtime.stopLocal()
		}
		if len(runtime.streamsByTarget) > 0 {
			for _, stream := range runtime.streamsByTarget {
				if stream != nil {
					_ = stream.CloseSend()
				}
			}
		} else if runtime.singleStream != nil {
			_ = runtime.singleStream.CloseSend()
		}
	}

	type remoteStop struct {
		testID              string
		executionGeneration uint64
		runSupervisionToken []byte
		client              generatorClient
		stop                bool
	}
	jobs := make([]remoteStop, 0)
	for runtimeIndex := range runtimes {
		runtime := &runtimes[runtimeIndex]
		// A handed-off run keeps its generators running for the successor's
		// adoption; its clients are only closed.
		stop := !runtime.handoff
		if len(runtime.clientsByTarget) > 0 {
			for _, client := range runtime.clientsByTarget {
				if client != nil {
					jobs = append(jobs, remoteStop{testID: runtime.testID, executionGeneration: runtime.executionGeneration, runSupervisionToken: append([]byte(nil), runtime.runSupervisionToken...), client: client, stop: stop})
				}
			}
		} else if runtime.singleClient != nil {
			jobs = append(jobs, remoteStop{testID: runtime.testID, executionGeneration: runtime.executionGeneration, runSupervisionToken: append([]byte(nil), runtime.runSupervisionToken...), client: runtime.singleClient, stop: stop})
		}
		for _, client := range runtime.archiveClients {
			if client != nil {
				jobs = append(jobs, remoteStop{testID: runtime.testID, client: client})
			}
		}
		clear(runtime.runSupervisionToken)
		runtime.runSupervisionToken = nil
	}
	if len(jobs) == 0 {
		return errors.Join(errs...)
	}
	workerCount := min(len(jobs), maxShutdownStopWorkers)
	jobCh := make(chan remoteStop)
	resultCh := make(chan error, len(jobs))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for job := range jobCh {
				var stopErr error
				if job.stop {
					// Keeper joins must precede StopTest so no ordinary renewal races the
					// stop. A cancellation-ignoring keeper can nevertheless exhaust the
					// leadership teardown context. Use an independent bounded RPC here:
					// the tokenized stop installs the generator tombstone that rejects a
					// late claim, and skipping it would throw away the immediate cleanup
					// path for every run after the stuck keeper.
					stopCtx, stopCancel := context.WithTimeout(context.Background(), leadershipLossRemoteStopTimeout)
					result, err := stopGeneratorTestWithToken(stopCtx, job.client, job.testID, false, job.executionGeneration, job.runSupervisionToken)
					stopCancel()
					if err != nil {
						stopErr = fmt.Errorf("stop stale remote test %s: %w", job.testID, err)
					} else if !result.Acknowledged {
						stopErr = fmt.Errorf("generator did not acknowledge stale remote test stop for %s", job.testID)
					}
				}
				closeErr := job.client.Close()
				if closeErr != nil {
					closeErr = fmt.Errorf("close stale remote test client %s: %w", job.testID, closeErr)
				}
				clear(job.runSupervisionToken)
				job.runSupervisionToken = nil
				resultCh <- errors.Join(stopErr, closeErr)
			}
		}()
	}
	go func() {
		defer close(jobCh)
		for _, job := range jobs {
			jobCh <- job
		}
	}()
	workers.Wait()
	close(resultCh)
	for err := range resultCh {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// shutdownRunFinalizationWait bounds how long graceful shutdown waits for runs
// it stopped to finish finalizing before it releases the execution lease.
const shutdownRunFinalizationWait = 20 * time.Second

// ownedActiveRuns snapshots the active runs whose live runtime this replica
// owns, excluding runs handed off to the successor: the runs graceful shutdown
// stops and must see finalized before it releases the execution lease.
func (tr *TestRunner) ownedActiveRuns() map[string]*ManagedTest {
	owned := make(map[string]*ManagedTest)
	if tr == nil {
		return owned
	}
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	for id, test := range tr.tests {
		if test == nil || test.continuityHandoff || (test.cancel == nil && test.executionCtx == nil) {
			continue
		}
		switch test.Status {
		case "INITIALIZING", "RUNNING", "PAUSED", "STOPPING", "SHAKEOUT":
			owned[id] = test
		}
	}
	return owned
}

// awaitRunsFinalized waits, bounded by ctx, until every run in runs has been
// finalized. Graceful shutdown stops runs asynchronously: the stop is
// dispatched and STOPPING persisted, then the terminal status is committed and
// the generators' archives are collected and recorded. Releasing the lease
// before that leaves the successor a STOPPING row it cannot prove stopped, or
// an archive it must recover from retained generator sources. It reports
// whether every run finished.
func (tr *TestRunner) awaitRunsFinalized(ctx context.Context, runs map[string]*ManagedTest) bool {
	if tr == nil {
		return true
	}
	for {
		pending := false
		for id, test := range runs {
			if !tr.runFinalized(id, test) {
				pending = true
				break
			}
		}
		if !pending {
			return true
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

// runFinalized reports whether test no longer needs the lease: it is terminal
// with its generator cleanup and archive commit complete, it is no longer owned
// by this runner, or the stop pass left it RUNNING/PAUSED because its stop
// failed (waiting cannot finalize it; lease-loss teardown detaches it). A run
// already finalizing on its own stays RUNNING (the stop pass leaves it to the
// finalizer), so it is still waited for.
func (tr *TestRunner) runFinalized(id string, test *ManagedTest) bool {
	tr.mu.RLock()
	if tr.tests[id] != test || ((test.Status == "RUNNING" || test.Status == "PAUSED") && !test.remoteFinalizing) {
		tr.mu.RUnlock()
		return true
	}
	finished := isTerminalTestStatus(test.Status) && test.remoteClient == nil &&
		len(test.remoteClientsByTarget) == 0 && len(test.remoteArchiveClientsByTarget) == 0
	tr.mu.RUnlock()
	if !finished {
		return false
	}
	// Distributed finalization collects and records the archive under the
	// run's archive commit fence after the terminal status is visible.
	tr.archiveCommitMu.Lock()
	_, committing := tr.archiveCommitGates[id]
	tr.archiveCommitMu.Unlock()
	return !committing
}

// markContinuityHandoff marks every run a successor can adopt so a voluntary
// step-down (graceful shutdown or failed acquire-time recovery) detaches it
// instead of stopping it. The generators keep running it under their execution
// epoch and orphan grace until the successor's adoption claim. It returns the
// number of runs marked; all other runs keep the ordinary stop behaviour.
func (tr *TestRunner) markContinuityHandoff(generation uint64) int {
	if tr == nil || generation == 0 || !tr.runAdoptionEnabled() {
		return 0
	}
	if _, distributed := tr.execution.(*distributedExecutionBackend); !distributed {
		return 0
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	marked := 0
	for _, test := range tr.tests {
		if test == nil {
			continue
		}
		test.continuityHandoff = continuityHandoffEligibleLocked(test, generation)
		if test.continuityHandoff {
			marked++
		}
	}
	return marked
}

// continuityHandoffEligibleLocked reports whether a successor can adopt test.
// Its adoption checkpoint must be armed for this exact term, so the successor
// can prove the token the generators currently hold. Runs that are stopping,
// finalizing, or carrying an unresolved evacuation guard are stopped instead.
func continuityHandoffEligibleLocked(test *ManagedTest, generation uint64) bool {
	if test.Status != "RUNNING" && test.Status != "PAUSED" {
		return false
	}
	if effectiveFailoverPolicy(test.Assignment) != failoverPolicyContinue {
		return false
	}
	if test.continuityArmedGeneration != generation || test.executionCtx == nil || test.remoteFinalizing {
		return false
	}
	if len(test.remoteClientsByTarget) == 0 && test.remoteClient == nil {
		return false
	}
	return !interruptedRunEvacuation(test.continuityCheckpoint)
}

func (tr *TestRunner) cancelDurablyStoppingTests(ids []string) {
	// The shutdown deadline has already fired on this path. Do not turn a
	// best-effort cancellation pass into another unbounded wait behind scheduler
	// persistence; active execution contexts were canceled synchronously when the
	// leadership epoch closed.
	if !tr.mu.TryLock() {
		return
	}
	defer tr.mu.Unlock()
	for _, id := range ids {
		test := tr.tests[id]
		if test != nil && test.Status == "STOPPING" && test.cancel != nil {
			test.cancel()
		}
	}
}

func lockMutexWithContext(ctx context.Context, mu *sync.Mutex) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if mu.TryLock() {
			return nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func lockRWMutexWithContext(ctx context.Context, mu *sync.RWMutex) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	acquired := make(chan struct{})
	abandon := make(chan struct{})
	go func() {
		// A blocking writer records writer intent in sync.RWMutex, preventing
		// a continuous stream of new readers from starving leadership-loss
		// cleanup.
		mu.Lock()
		// Transfer ownership only while the caller is still waiting. Once the
		// caller abandons the acquisition, this same goroutine releases the late
		// lock; no second cleanup goroutine or leaked ownership is needed.
		select {
		case acquired <- struct{}{}:
		case <-abandon:
			mu.Unlock()
		}
	}()
	select {
	case <-acquired:
		// Cancellation and acquisition can become ready together. Recheck after
		// ownership transfer so a randomly selected acquire case cannot make a
		// deadline-expired caller continue shutdown work while holding the lock.
		if err := ctx.Err(); err != nil {
			mu.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		// sync.RWMutex cannot retract a queued writer. Tell the acquisition
		// goroutine to release immediately after the existing readers drain.
		close(abandon)
		return ctx.Err()
	}
}
