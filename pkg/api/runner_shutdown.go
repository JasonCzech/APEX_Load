package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

const maxShutdownStopWorkers = 16

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
	testID              string
	executionGeneration uint64
	cancel              context.CancelFunc
	stopLocal           func()
	clientsByTarget     map[string]generatorClient
	archiveClients      map[string]generatorClient
	singleClient        generatorClient
	streamsByTarget     map[string]*generatorrpc.MetricsStream
	singleStream        *generatorrpc.MetricsStream
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
		activeStatus := false
		switch test.Status {
		case "INITIALIZING", "RUNNING", "PAUSED", "STOPPING", "SHAKEOUT":
			activeStatus = true
		}
		if !activeStatus && test.cancel == nil && test.multiEngine == nil && test.remoteClient == nil &&
			len(test.remoteClientsByTarget) == 0 && len(test.remoteArchiveClientsByTarget) == 0 && len(test.remoteSecretSnapshots) == 0 {
			continue
		}
		runtime := leadershipLossRuntime{
			testID:              test.ID,
			executionGeneration: test.executionGeneration,
			cancel:              test.cancel,
			clientsByTarget:     test.remoteClientsByTarget,
			archiveClients:      test.remoteArchiveClientsByTarget,
			singleClient:        test.remoteClient,
			streamsByTarget:     test.remoteMetricsStreamsByTarget,
			singleStream:        test.remoteMetricsStream,
		}
		if test.multiEngine != nil {
			engine := test.multiEngine
			runtime.stopLocal = engine.Stop
		}
		tr.cancelAllVUserScaleRampsLocked(test)
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
		test.remoteTargetStates = nil
		test.remoteFinalizing = false
		test.remoteMetricsFlushScheduled = false
		test.remoteGoalUpdateRunning = false
		test.remoteActivated = false
		test.remoteActivationPending = false
		clearDistributedRunSecretSnapshotsLocked(test)
		test.remoteControlGeneration++
		runtimes = append(runtimes, runtime)
	}
	tr.mu.Unlock()

	for _, runtime := range runtimes {
		if runtime.cancel != nil {
			runtime.cancel()
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
		client              generatorClient
		stop                bool
	}
	jobs := make([]remoteStop, 0)
	for _, runtime := range runtimes {
		if len(runtime.clientsByTarget) > 0 {
			for _, client := range runtime.clientsByTarget {
				if client != nil {
					jobs = append(jobs, remoteStop{testID: runtime.testID, executionGeneration: runtime.executionGeneration, client: client, stop: true})
				}
			}
		} else if runtime.singleClient != nil {
			jobs = append(jobs, remoteStop{testID: runtime.testID, executionGeneration: runtime.executionGeneration, client: runtime.singleClient, stop: true})
		}
		for _, client := range runtime.archiveClients {
			if client != nil {
				jobs = append(jobs, remoteStop{testID: runtime.testID, client: client})
			}
		}
	}
	if len(jobs) == 0 {
		return nil
	}
	workerCount := min(len(jobs), 16)
	jobCh := make(chan remoteStop)
	resultCh := make(chan error, len(jobs))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for job := range jobCh {
				var stopErr error
				if job.stop && ctx.Err() == nil {
					result, err := stopGeneratorTest(ctx, job.client, job.testID, false, job.executionGeneration)
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
	var errs []error
	for err := range resultCh {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
