package api

// The run state machine: Start/Stop/Abort/Pause/Resume/AdjustDuration with
// startability + preflight gating, script-shakeout gating, run setup
// (runTest/buildRunEngine) and lifecycle (start/fail/complete), VUser scaling
// and scale-ramps, goal-target control, and pending-test promotion.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/api/testexec"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (tr *TestRunner) StartTest(id string) error {
	return tr.StartTestWithOptions(id, StartTestOptions{})
}

func (tr *TestRunner) StartTestWithOptions(id string, opts StartTestOptions) error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	tr.mu.Lock()
	id = tr.resolveRefKeyLocked(id)
	blocked, err := tr.preflightStartBlocksLocked(id, opts)
	tr.mu.Unlock()
	if err != nil {
		return err
	}
	if blocked {
		return nil
	}

	shakeoutTest, err := tr.beginScriptShakeoutIfNeeded(id, opts)
	if err != nil {
		return err
	}
	if shakeoutTest != nil {
		report := tr.runScriptShakeoutReport(shakeoutTest)
		if !report.Passed {
			return tr.markScriptShakeoutReview(id, report)
		}
		if err := tr.markScriptShakeoutPassed(id, report); err != nil {
			return err
		}
	}
	// Shakeout can take tens of seconds. Revalidate the HA lease immediately
	// before the durable INITIALIZING transition so a stale controller cannot
	// overwrite takeover reconciliation.
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}

	tr.mu.Lock()
	if !tr.isExecutionLeader() {
		tr.mu.Unlock()
		return errNotExecutionLeader
	}
	test, err := tr.prepareTestStartLocked(id, opts)
	tr.mu.Unlock()
	if err != nil {
		return err
	}
	if test == nil {
		return nil
	}

	go tr.runTest(test)
	return nil
}

// markTestPendingLocked transitions test to PENDING with the given reason and
// broadcasts the change. Shared by the start-gate evaluators so the pending
// transition semantics live in one place. The caller must hold tr.mu.
func (tr *TestRunner) markTestPendingLocked(test *ManagedTest, reason string) error {
	previousStatus := test.Status
	previousPendingSince := cloneTimePtr(test.PendingSince)
	previousPendingReason := test.PendingReason
	previousQueuePosition := test.QueuePosition
	previousError := test.Error
	now := time.Now()
	test.Status = "PENDING"
	if test.PendingSince == nil || test.PendingSince.IsZero() {
		test.PendingSince = &now
	}
	test.PendingReason = reason
	test.QueuePosition = 0
	test.Error = ""
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Status = previousStatus
		test.PendingSince = previousPendingSince
		test.PendingReason = previousPendingReason
		test.QueuePosition = previousQueuePosition
		test.Error = previousError
		return fmt.Errorf("persist pending test state: %w", err)
	}
	tr.broadcastEvent("test_pending", test)
	return nil
}

// markTestInitializingLocked transitions test to INITIALIZING, clears its pending
// fields, and binds its execution backend. The caller persists the record because
// the save timing differs (under the lock for prepare, after unlock for promote).
func (tr *TestRunner) markTestInitializingLocked(test *ManagedTest) {
	test.executionCtx, test.cancel = context.WithCancel(context.Background())
	test.Status = "INITIALIZING"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = ""
	test.executionBackend = tr.execution
	if test.executionBackend == nil {
		test.executionBackend = localExecutionBackend{}
	}
}

// evaluateStartGatesLocked runs the capacity-advisory / governance / launch-window /
// quota gates for a start attempt. It returns blocked=true (after transitioning the
// test to PENDING) when a quota block applies, or an error when a gate rejects the
// start outright. The caller must hold tr.mu and have already validated startability.
func (tr *TestRunner) evaluateStartGatesLocked(id string, test *ManagedTest, opts StartTestOptions) (bool, error) {
	tr.refreshCapacityAdvisoryLocked(test)
	if err := buildStartGovernanceError(id, test.CapacityAdvisory, opts); err != nil {
		return false, err
	}
	if !opts.SkipLaunchWindowPreflight {
		if err := tr.launchStartWindowErrorLocked(test, time.Now().UTC(), id, ""); err != nil {
			return false, err
		}
	}
	// Re-validate a bound capacity timeslot at the actual launch instant even when
	// the launch-window preflight is skipped: the binding can be invalidated
	// (rejected/re-bound/resized) between create and start, and a scheduled run's
	// window can drift, so the bind-time fit can no longer hold.
	if err := tr.launchTimeslotRevalidationErrorLocked(test, time.Now().UTC()); err != nil {
		return false, err
	}
	if blocked, reason := tr.evaluateQuotaBlockLocked(test, ""); blocked {
		if err := tr.markTestPendingLocked(test, reason); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func (tr *TestRunner) preflightStartBlocksLocked(id string, opts StartTestOptions) (bool, error) {
	if err := tr.shutdownErrorLocked(); err != nil {
		return false, err
	}
	test, ok := tr.tests[id]
	if !ok {
		return false, fmt.Errorf("test not found: %s", id)
	}
	if !testexec.IsStartableTestStatus(test.Status) {
		return false, testexec.StartableTestStatusError(test.Status)
	}
	if err := tr.projectDeletionErrorLocked(test.ProjectID); err != nil {
		return false, err
	}
	return tr.evaluateStartGatesLocked(id, test, opts)
}

func (tr *TestRunner) beginScriptShakeoutIfNeeded(id string, opts StartTestOptions) (*ManagedTest, error) {
	if opts.OverrideScriptShakeoutError {
		return nil, nil
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if err := tr.shutdownErrorLocked(); err != nil {
		return nil, err
	}

	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if !test.ScriptShakeout {
		return nil, nil
	}
	if !testexec.IsStartableTestStatus(test.Status) {
		return nil, testexec.StartableTestStatusError(test.Status)
	}
	if err := tr.projectDeletionErrorLocked(test.ProjectID); err != nil {
		return nil, err
	}

	previousStatus := test.Status
	previousPendingSince := cloneTimePtr(test.PendingSince)
	previousPendingReason := test.PendingReason
	previousQueuePosition := test.QueuePosition
	previousError := test.Error
	test.Status = "SHAKEOUT"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = ""
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Status = previousStatus
		test.PendingSince = previousPendingSince
		test.PendingReason = previousPendingReason
		test.QueuePosition = previousQueuePosition
		test.Error = previousError
		return nil, fmt.Errorf("persist script shakeout state: %w", err)
	}
	tr.broadcastEvent("script_shakeout_started", test)

	return test, nil
}

func (tr *TestRunner) markScriptShakeoutPassed(id string, _ ScriptShakeoutReport) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	test, ok := tr.tests[id]
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "SHAKEOUT" {
		return fmt.Errorf("test is %s, shakeout result no longer applies", test.Status)
	}
	previousStatus := test.Status
	previousError := test.Error
	test.Status = "QUEUED"
	test.Error = ""
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Status = previousStatus
		test.Error = previousError
		return fmt.Errorf("persist successful script shakeout: %w", err)
	}
	tr.broadcastEvent("script_shakeout_passed", test)
	return nil
}

func (tr *TestRunner) markScriptShakeoutReview(id string, report ScriptShakeoutReport) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	test, ok := tr.tests[id]
	if !ok {
		return fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "SHAKEOUT" {
		return fmt.Errorf("test is %s, shakeout review no longer applies", test.Status)
	}
	previousStatus := test.Status
	previousError := test.Error
	test.Status = "SHAKEOUT_REVIEW"
	test.Error = formatScriptShakeoutEvidence(report)
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Status = previousStatus
		test.Error = previousError
		return fmt.Errorf("persist script shakeout review: %w", err)
	}
	tr.broadcastEvent("script_shakeout_failed", test)
	return &ScriptShakeoutGateError{TestID: id, Report: &report}
}

func (tr *TestRunner) prepareTestStartLocked(id string, opts StartTestOptions) (*ManagedTest, error) {
	if err := tr.shutdownErrorLocked(); err != nil {
		return nil, err
	}
	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if !testexec.IsStartableTestStatus(test.Status) {
		return nil, testexec.StartableTestStatusError(test.Status)
	}
	if err := tr.projectDeletionErrorLocked(test.ProjectID); err != nil {
		return nil, err
	}

	if blocked, err := tr.evaluateStartGatesLocked(id, test, opts); err != nil {
		return nil, err
	} else if blocked {
		return nil, nil
	}

	previousStatus := test.Status
	previousPendingSince := cloneTimePtr(test.PendingSince)
	previousPendingReason := test.PendingReason
	previousQueuePosition := test.QueuePosition
	previousError := test.Error
	previousExecutionCtx := test.executionCtx
	previousCancel := test.cancel
	previousBackend := test.executionBackend
	tr.markTestInitializingLocked(test)
	if err := tr.saveManagedTestRecord(test); err != nil {
		if test.cancel != nil {
			test.cancel()
		}
		test.Status = previousStatus
		test.PendingSince = previousPendingSince
		test.PendingReason = previousPendingReason
		test.QueuePosition = previousQueuePosition
		test.Error = previousError
		test.executionCtx = previousExecutionCtx
		test.cancel = previousCancel
		test.executionBackend = previousBackend
		return nil, fmt.Errorf("persist initializing test state: %w", err)
	}
	if previousCancel != nil {
		previousCancel()
	}
	return test, nil
}

func (tr *TestRunner) ReconcilePendingTests() {
	// Only the execution leader reconciles. On a non-leader replica (HA) this is
	// a no-op; the eventual leader runs it via reloadAndReconcile on acquisition.
	if !tr.isExecutionLeader() {
		return
	}
	// Rebuild sequential plan mappings before pending promotion. Otherwise a
	// recovered plan-owned test could run to completion without its terminal hook
	// knowing which durable sequence to advance.
	if err := tr.recoverSequentialExecutionPlanRuns(); err != nil {
		slog.Error("failed to recover sequential execution-plan runs", "error", err)
		tr.enqueueSequentialExecutionPlanRecoveryRetry()
		return
	}
	tr.promotePendingTests()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.refreshPendingQueueMetadataLocked()
}

func (tr *TestRunner) reconcilePendingTestsDuringAcquire() error {
	if !tr.holdsExecutionLeadership() {
		return errNotExecutionLeader
	}
	if err := tr.recoverSequentialExecutionPlanRuns(); err != nil {
		return err
	}
	// Promotion starts execution and therefore waits for admission to open. The
	// onAdmit callback calls ReconcilePendingTests after reconciling is cleared.
	tr.mu.Lock()
	tr.refreshPendingQueueMetadataLocked()
	tr.mu.Unlock()
	// During the leadership callback admission remains closed and onAdmit will
	// perform promotion after recovery completes. Direct callers that already
	// hold an admitted lease (including recovery retries) can promote now.
	if tr.isExecutionLeader() {
		tr.ReconcilePendingTests()
	}
	return nil
}

// reloadAndReconcile refreshes in-memory test state from the database and then
// reconciles. It runs when this replica acquires execution leadership — at boot
// when it wins the lease, or after a failover takeover where the previous
// leader's runs and any tests created elsewhere are not yet in memory.
func (tr *TestRunner) reloadAndReconcile() error {
	if !tr.holdsExecutionLeadership() {
		return errNotExecutionLeader
	}
	if err := tr.reloadStoredScripts(); err != nil {
		return fmt.Errorf("reload scripts: %w", err)
	}
	if err := tr.reloadStoredTests(); err != nil {
		return fmt.Errorf("reload tests: %w", err)
	}
	if err := tr.reloadStoredScenarios(); err != nil {
		return fmt.Errorf("reload scenarios: %w", err)
	}
	return tr.reconcilePendingTestsDuringAcquire()
}

// resolveBackend returns the test's execution backend, falling back to the
// runner's shared execution backend when the test has none.
func (tr *TestRunner) resolveBackend(test *ManagedTest) testExecutionBackend {
	if test.executionBackend != nil {
		return test.executionBackend
	}
	return tr.execution
}

// lockedTestStatus resolves a possibly-ref-key id and returns the resolved id, the
// managed test, and its status captured under the read lock. It errors when the
// test is unknown. Callers must re-acquire the write lock before mutating.
func (tr *TestRunner) lockedTestStatus(id string) (string, *ManagedTest, string, error) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		return id, nil, "", fmt.Errorf("test not found: %s", id)
	}
	return id, test, test.Status, nil
}

func (tr *TestRunner) StopTest(id string) error {
	return tr.stopTestWithContext(context.Background(), id, true)
}

func (tr *TestRunner) stopTest(id string, requireLeadership bool) error {
	return tr.stopTestWithContext(context.Background(), id, requireLeadership)
}

func (tr *TestRunner) stopTestWithContext(ctx context.Context, id string, requireLeadership bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if requireLeadership && !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	tr.mu.RUnlock()
	unlockControl, err := tr.lockTestLifecycleControl(ctx, id)
	if err != nil {
		return err
	}
	defer unlockControl()
	if err := ctx.Err(); err != nil {
		return err
	}
	if requireLeadership && !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}

	tr.mu.Lock()
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.Unlock()
		return fmt.Errorf("test not found: %s", id)
	}
	if test.remoteFinalizing {
		// The distributed completion path owns remote teardown once finalization
		// starts. Treat a concurrent stop as already in progress so it cannot
		// overlap the finalizer's StopTest fan-out.
		tr.mu.Unlock()
		return nil
	}
	status := test.Status
	switch status {
	case "COMPLETE", "FAILED", "ABORTED":
		tr.mu.Unlock()
		return nil
	case "STOPPING":
		if requireLeadership {
			tr.mu.Unlock()
			return nil
		}
		tr.cancelAllVUserScaleRampsLocked(test)
	case "QUEUED", "PENDING", "RESERVED", "SHAKEOUT", "SHAKEOUT_REVIEW", "INITIALIZING":
		now := time.Now().UTC()
		previousPendingSince := cloneTimePtr(test.PendingSince)
		previousPendingReason := test.PendingReason
		previousQueuePosition := test.QueuePosition
		previousEndedAt := cloneTimePtr(test.EndedAt)
		previousError := test.Error
		test.Status = "ABORTED"
		test.PendingSince = nil
		test.PendingReason = ""
		test.QueuePosition = 0
		test.EndedAt = &now
		if test.Error == "" {
			test.Error = "test cancelled before start"
		}
		if err := tr.saveManagedTestRecord(test); err != nil {
			test.Status = status
			test.PendingSince = previousPendingSince
			test.PendingReason = previousPendingReason
			test.QueuePosition = previousQueuePosition
			test.EndedAt = previousEndedAt
			test.Error = previousError
			tr.mu.Unlock()
			return fmt.Errorf("persist aborted test state: %w", err)
		}
		tr.cancelAllVUserScaleRampsLocked(test)
		if test.cancel != nil {
			test.cancel()
		}
		test.cancel = nil
		test.executionCtx = nil
		tr.mu.Unlock()
		tr.broadcastEvent("test_aborted", test)
		if err := tr.advanceExecutionPlanForTest(test, "ABORTED"); err != nil {
			return fmt.Errorf("persist aborted execution-plan transition: %w", err)
		}
		return nil
	case "RUNNING", "PAUSED":
		// Reserve the transition before any blocking backend call. Concurrent
		// pause/resume/start requests now observe STOPPING and cannot overwrite it.
		test.Status = "STOPPING"
		if err := tr.saveManagedTestRecord(test); err != nil {
			test.Status = status
			tr.mu.Unlock()
			return fmt.Errorf("persist stopping test state: %w", err)
		}
		tr.cancelAllVUserScaleRampsLocked(test)
	default:
		tr.mu.Unlock()
		return fmt.Errorf("test is %s and cannot be stopped", status)
	}
	backend := tr.resolveBackend(test)
	multiEngine := test.multiEngine
	cancel := test.cancel
	tr.mu.Unlock()

	if multiEngine == nil {
		var stopErr error
		stopSupported := false
		if stoppable, ok := backend.(contextStoppableExecutionBackend); ok {
			stopSupported = true
			stopErr = stoppable.StopTestContext(ctx, tr, test)
		} else if stoppable, ok := backend.(stoppableExecutionBackend); ok {
			stopSupported = true
			if ctx.Done() == nil {
				stopErr = stoppable.StopTest(tr, test)
			} else if cancel == nil {
				// A legacy stop has no cancellation contract. Bounded shutdown must
				// not launch a call that can outlive the controller's deadline.
				stopErr = fmt.Errorf("test backend does not support context-bounded stop")
			}
		} else if cancel == nil {
			stopErr = fmt.Errorf("test backend does not support stop")
		}
		if stopErr != nil {
			if ctx.Err() != nil || distributedStopMayHaveApplied(stopErr) {
				if cancel != nil {
					cancel()
				}
				tr.broadcastEvent("test_stopping", test)
				if ctx.Err() != nil {
					return errors.Join(stopErr, ctx.Err())
				}
				return stopErr
			}
			if stopSupported {
				tr.mu.Lock()
				if test.Status == "STOPPING" {
					test.Status = status
					if persistErr := tr.saveManagedTestRecord(test); persistErr != nil {
						test.Status = "STOPPING"
						tr.mu.Unlock()
						return errors.Join(stopErr, fmt.Errorf("persist stop rollback: %w", persistErr))
					}
				}
				tr.mu.Unlock()
				return stopErr
			}
			tr.mu.Lock()
			if test.Status == "STOPPING" {
				test.Status = status
				if persistErr := tr.saveManagedTestRecord(test); persistErr != nil {
					test.Status = "STOPPING"
					tr.mu.Unlock()
					return errors.Join(stopErr, fmt.Errorf("persist stop rollback: %w", persistErr))
				}
			}
			tr.mu.Unlock()
			return stopErr
		}
	}
	if cancel != nil {
		cancel()
	}
	tr.broadcastEvent("test_stopping", test)
	return nil
}

func (tr *TestRunner) AbortTest(id string) error {
	return tr.StopTest(id)
}

func (tr *TestRunner) ScaleVUsers(id string, groupName string, targetVUsers int) error {
	return tr.ScaleVUsersWithOptions(id, groupName, targetVUsers, ScaleVUsersOptions{})
}

func (tr *TestRunner) ScaleVUsersWithOptions(id string, groupName string, targetVUsers int, opts ScaleVUsersOptions) error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	if opts.RampDuration <= 0 {
		return tr.scaleVUsersNow(id, groupName, targetVUsers, true)
	}
	return tr.scheduleVUserScaleRamp(id, groupName, targetVUsers, opts.RampDuration)
}

func (tr *TestRunner) UpdateGoalTargetTPS(id string, groupName string, targetTPS float64) (*GoalTargetUpdate, error) {
	if !tr.isExecutionLeader() {
		return nil, errNotExecutionLeader
	}
	if strings.TrimSpace(groupName) == "" {
		return nil, fmt.Errorf("group name is required")
	}
	if targetTPS <= 0 || math.IsNaN(targetTPS) || math.IsInf(targetTPS, 0) {
		return nil, fmt.Errorf("target_tps must be greater than zero")
	}

	now := time.Now().UTC()
	tr.mu.Lock()
	if !tr.isExecutionLeader() {
		tr.mu.Unlock()
		return nil, errNotExecutionLeader
	}
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.Unlock()
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if test.remoteFinalizing {
		tr.mu.Unlock()
		return nil, fmt.Errorf("test is finalizing and cannot adjust goal targets")
	}
	if test.Status != "RUNNING" && test.Status != "PAUSED" {
		tr.mu.Unlock()
		return nil, fmt.Errorf("test is %s, must be RUNNING or PAUSED to adjust goal targets", test.Status)
	}
	if test.ScenarioMode != "goal_oriented" {
		tr.mu.Unlock()
		return nil, fmt.Errorf("test is %s, must be goal_oriented to adjust goal targets", firstNonEmpty(test.ScenarioMode, "vuser_driven"))
	}
	groupFound := false
	for _, group := range test.Groups {
		if group.Name == groupName {
			groupFound = true
			break
		}
	}
	if !groupFound {
		tr.mu.Unlock()
		return nil, fmt.Errorf("group not found: %s", groupName)
	}

	targets := currentGoalTargetStatuses(test, now)
	if targets == nil {
		targets = make(map[string]GoalTargetStatus, len(test.Groups))
	}
	targets[groupName] = GoalTargetStatus{
		Group:     groupName,
		TargetTPS: targetTPS,
		UpdatedAt: now,
	}
	totalTargetTPS := sumGoalTargetTPS(targets)
	if totalTargetTPS <= 0 {
		tr.mu.Unlock()
		return nil, fmt.Errorf("target_average_tps must be greater than zero")
	}

	previousGoalConfig := cloneGoalConfig(test.GoalConfig)
	previousGoalTargets := cloneGoalTargetStatusMap(test.GoalTargets)
	if test.GoalConfig == nil {
		test.GoalConfig = &GoalConfig{}
	}
	test.GoalTargets = cloneGoalTargetStatusMap(targets)
	test.GoalConfig.TargetAverageTPS = &totalTargetTPS
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.GoalConfig = previousGoalConfig
		test.GoalTargets = previousGoalTargets
		tr.mu.Unlock()
		return nil, fmt.Errorf("persist goal target adjustment: %w", err)
	}
	if test.goalControl != nil {
		test.goalControl.lastScaleAt = nil
	}
	if test.LiveMetrics != nil {
		updated := cloneLiveMetrics(test.LiveMetrics)
		applyRuntimeGoalTargetsToLiveMetrics(test, updated)
		replaceLiveMetricsLocked(test, updated)
	}
	projectID := normalizeProjectID(test.ProjectID)
	testName := test.Name
	result := &GoalTargetUpdate{
		TestID:           id,
		Group:            groupName,
		TargetTPS:        targetTPS,
		TargetAverageTPS: totalTargetTPS,
		GoalTargets:      cloneGoalTargetStatusMap(test.GoalTargets),
		Status:           "adjusted",
	}
	tr.mu.Unlock()

	tr.hub.BroadcastJSON(map[string]any{
		"type":               "goal_target_changed",
		"test_id":            id,
		"project_id":         projectID,
		"test_name":          testName,
		"group":              groupName,
		"target_tps":         targetTPS,
		"target_average_tps": totalTargetTPS,
		"goal_targets":       result.GoalTargets,
		"timestamp":          now,
	})

	return result, nil
}

func (tr *TestRunner) scheduleVUserScaleRamp(id string, groupName string, targetVUsers int, rampDuration time.Duration) error {
	_, err := tr.startVUserScaleRamp(context.Background(), id, groupName, targetVUsers, rampDuration)
	return err
}

func (tr *TestRunner) startVUserScaleRamp(parent context.Context, id string, groupName string, targetVUsers int, rampDuration time.Duration) (*vuserScaleRamp, error) {
	if targetVUsers < 0 {
		return nil, fmt.Errorf("target vusers must be non-negative")
	}
	if rampDuration <= 0 {
		return nil, fmt.Errorf("ramp duration must be positive")
	}

	tr.mu.Lock()
	if !tr.isExecutionLeader() {
		tr.mu.Unlock()
		return nil, errNotExecutionLeader
	}
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.Unlock()
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if test.remoteFinalizing {
		tr.mu.Unlock()
		return nil, fmt.Errorf("test is finalizing and cannot be scaled")
	}
	if test.Status != "RUNNING" {
		tr.mu.Unlock()
		return nil, fmt.Errorf("test is %s, must be RUNNING to scale", test.Status)
	}

	currentVUsers, found := testGroupVUsers(test, groupName)
	if !found {
		tr.mu.Unlock()
		return nil, fmt.Errorf("group not found: %s", groupName)
	}
	tr.cancelVUserScaleRampLocked(test, groupName)
	if currentVUsers == targetVUsers {
		tr.mu.Unlock()
		return nil, nil
	}

	startedAt := time.Now()
	rampStatus := VUserRampStatus{
		Group:               groupName,
		StartVUsers:         currentVUsers,
		TargetVUsers:        targetVUsers,
		StartedAt:           startedAt,
		RampDuration:        rampDuration.String(),
		RampDurationSeconds: int64(rampDuration / time.Second),
	}
	ctx, cancel := context.WithCancel(parent)
	ramp := &vuserScaleRamp{
		cancel: cancel,
		status: rampStatus,
		done:   make(chan error, 1),
	}
	if test.vuserScaleRamps == nil {
		test.vuserScaleRamps = make(map[string]*vuserScaleRamp)
	}
	if test.VUserRamps == nil {
		test.VUserRamps = make(map[string]VUserRampStatus)
	}
	test.vuserScaleRamps[groupName] = ramp
	test.VUserRamps[groupName] = rampStatus
	projectID := normalizeProjectID(test.ProjectID)
	tr.mu.Unlock()

	tr.hub.BroadcastJSON(map[string]any{
		"type":                  "vusers_ramp_started",
		"test_id":               id,
		"project_id":            projectID,
		"group":                 rampStatus.Group,
		"current_vusers":        rampStatus.StartVUsers,
		"target_vusers":         rampStatus.TargetVUsers,
		"ramp_duration":         rampStatus.RampDuration,
		"ramp_duration_seconds": rampStatus.RampDurationSeconds,
		"timestamp":             startedAt,
	})

	go tr.runVUserScaleRamp(ctx, ramp, id, groupName, currentVUsers, targetVUsers, rampDuration)
	return ramp, nil
}

func (tr *TestRunner) runVUserScaleRamp(ctx context.Context, ramp *vuserScaleRamp, id string, groupName string, currentVUsers int, targetVUsers int, rampDuration time.Duration) {
	var result error
	defer func() {
		tr.finishVUserScaleRamp(id, groupName, ramp)
		ramp.done <- result
		close(ramp.done)
	}()

	delta := targetVUsers - currentVUsers
	steps := testexec.AbsInt(delta)
	if steps == 0 {
		return
	}
	direction := 1
	if delta < 0 {
		direction = -1
	}
	interval := rampDuration / time.Duration(steps)
	if interval <= 0 {
		interval = time.Nanosecond
	}

	timer := time.NewTimer(interval)
	defer timer.Stop()
	for step := 1; step <= steps; step++ {
		select {
		case <-ctx.Done():
			result = ctx.Err()
			return
		case <-timer.C:
		}

		nextTarget := currentVUsers + direction*step
		if err := tr.scaleVUsersNow(id, groupName, nextTarget, false); err != nil {
			result = err
			slog.Warn("gradual vuser scale stopped", "test_id", id, "group", groupName, "target_vusers", nextTarget, "error", err)
			return
		}
		if step < steps {
			timer.Reset(interval)
		}
	}
}

func (tr *TestRunner) startStageSchedules(ctx context.Context, test *ManagedTest, mge *engine.MultiGroupEngine) {
	if !tr.isExecutionLeader() {
		return
	}
	tr.mu.RLock()
	if test == nil || test.ScenarioMode != "vuser_driven" {
		tr.mu.RUnlock()
		return
	}
	testID := test.ID
	groups := make([]TestGroup, 0, len(test.Groups))
	for _, group := range test.Groups {
		if group.stageDriven && len(group.Stages) > 0 {
			group.Stages = cloneLoadStages(group.Stages)
			groups = append(groups, group)
		}
	}
	tr.mu.RUnlock()

	for _, group := range groups {
		group := group
		go tr.runStageSchedule(ctx, testID, group.Name, group.Stages, mge)
	}
}

func (tr *TestRunner) runStageSchedule(ctx context.Context, testID string, groupName string, stages []storage.LoadStage, mge *engine.MultiGroupEngine) {
	if mge != nil {
		select {
		case <-ctx.Done():
			return
		case <-mge.Ready():
		}
	}
	for index, stage := range stages {
		stageType := strings.ToLower(strings.TrimSpace(stage.Type))
		duration, err := time.ParseDuration(stage.Duration)
		if err != nil || duration <= 0 {
			slog.Warn("stage schedule stopped on invalid stage duration", "test_id", testID, "group", groupName, "stage", index, "duration", stage.Duration, "error", err)
			return
		}
		switch stageType {
		case "ramp_up", "ramp_down":
			ramp, err := tr.startVUserScaleRamp(ctx, testID, groupName, stage.TargetVUsers, duration)
			if err != nil {
				slog.Warn("stage schedule scale failed", "test_id", testID, "group", groupName, "stage", index, "target_vusers", stage.TargetVUsers, "error", err)
				return
			}
			if ramp == nil {
				if !sleepStageDuration(ctx, duration) {
					return
				}
				continue
			}
			select {
			case <-ctx.Done():
				ramp.cancel()
				return
			case err := <-ramp.done:
				if err != nil {
					if errors.Is(err, context.Canceled) {
						return
					}
					slog.Warn("stage schedule ramp stopped", "test_id", testID, "group", groupName, "stage", index, "target_vusers", stage.TargetVUsers, "error", err)
					return
				}
			}
			continue
		case "hold":
			// The engine is already at the hold target: first holds are seeded by
			// compilation, later holds inherit the preceding ramp target.
		default:
			slog.Warn("stage schedule stopped on invalid stage type", "test_id", testID, "group", groupName, "stage", index, "type", stage.Type)
			return
		}
		if !sleepStageDuration(ctx, duration) {
			return
		}
	}
}

func sleepStageDuration(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (tr *TestRunner) scaleVUsersNow(id string, groupName string, targetVUsers int, cancelExistingRamp bool) error {
	tr.mu.RLock()
	resolvedID := tr.resolveRefKeyLocked(id)
	tr.mu.RUnlock()
	unlockControl := tr.lockDistributedGroupControl(resolvedID, groupName)
	defer unlockControl()
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}

	tr.mu.Lock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.Unlock()
		return fmt.Errorf("test not found: %s", id)
	}
	if test.remoteFinalizing {
		tr.mu.Unlock()
		return fmt.Errorf("test is finalizing and cannot be scaled")
	}
	if test.Status != "RUNNING" {
		tr.mu.Unlock()
		return fmt.Errorf("test is %s, must be RUNNING to scale", test.Status)
	}
	if targetVUsers < 0 {
		tr.mu.Unlock()
		return fmt.Errorf("target vusers must be non-negative")
	}
	if _, found := testGroupVUsers(test, groupName); !found {
		tr.mu.Unlock()
		return fmt.Errorf("group not found: %s", groupName)
	}
	if cancelExistingRamp {
		tr.cancelVUserScaleRampLocked(test, groupName)
	}
	multiEngine := test.multiEngine
	backend := tr.resolveBackend(test)
	tr.mu.Unlock()

	if multiEngine == nil {
		scalable, ok := backend.(scalableExecutionBackend)
		if !ok {
			return fmt.Errorf("test backend does not support scaling vusers")
		}
		if err := scalable.ScaleVUsers(tr, test, groupName, targetVUsers); err != nil {
			return err
		}
	} else {
		if err := multiEngine.ScaleVUsers(groupName, targetVUsers); err != nil {
			return err
		}
	}

	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	totalVUsers, projectID, err := tr.updateTestGroupVUsers(id, groupName, targetVUsers)
	if err != nil {
		return err
	}
	tr.hub.BroadcastJSON(map[string]any{
		"type":          "vusers_changed",
		"test_id":       id,
		"project_id":    projectID,
		"group":         groupName,
		"target_vusers": targetVUsers,
		"total_vusers":  totalVUsers,
		"timestamp":     time.Now(),
	})

	return nil
}

func (tr *TestRunner) updateTestGroupVUsers(id string, groupName string, targetVUsers int) (int, string, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if !tr.isExecutionLeader() {
		return 0, "", errNotExecutionLeader
	}

	test, ok := tr.tests[id]
	if !ok {
		return 0, "", fmt.Errorf("test not found: %s", id)
	}
	if test.remoteFinalizing {
		return 0, "", fmt.Errorf("test is finalizing and cannot be scaled")
	}
	if test.Status != "RUNNING" {
		return 0, "", fmt.Errorf("test is %s, must be RUNNING to scale", test.Status)
	}
	currentTotalVUsers := 0
	capacityTotalVUsers := 0
	for i := range test.Groups {
		if test.Groups[i].Name == groupName {
			if test.Groups[i].stageDriven {
				test.Groups[i].currentVUsers = targetVUsers
			} else {
				test.Groups[i].VUsers = targetVUsers
			}
		}
		currentTotalVUsers += groupCurrentVUsers(test.Groups[i])
		capacityTotalVUsers += test.Groups[i].VUsers
	}
	test.TotalVUsers = capacityTotalVUsers
	return currentTotalVUsers, normalizeProjectID(test.ProjectID), nil
}

func (tr *TestRunner) cancelVUserScaleRampLocked(test *ManagedTest, groupName string) {
	if test == nil || test.vuserScaleRamps == nil {
		return
	}
	if ramp, ok := test.vuserScaleRamps[groupName]; ok {
		ramp.cancel()
		delete(test.vuserScaleRamps, groupName)
	}
	if test.VUserRamps != nil {
		delete(test.VUserRamps, groupName)
		if len(test.VUserRamps) == 0 {
			test.VUserRamps = nil
		}
	}
	if len(test.vuserScaleRamps) == 0 {
		test.vuserScaleRamps = nil
	}
}

func (tr *TestRunner) cancelAllVUserScaleRampsLocked(test *ManagedTest) {
	if test == nil || test.vuserScaleRamps == nil {
		return
	}
	for groupName, ramp := range test.vuserScaleRamps {
		ramp.cancel()
		delete(test.vuserScaleRamps, groupName)
	}
	test.vuserScaleRamps = nil
	test.VUserRamps = nil
}

func (tr *TestRunner) finishVUserScaleRamp(id string, groupName string, ramp *vuserScaleRamp) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	test, ok := tr.tests[id]
	if !ok || test.vuserScaleRamps == nil {
		return
	}
	if test.vuserScaleRamps[groupName] == ramp {
		delete(test.vuserScaleRamps, groupName)
		if test.VUserRamps != nil {
			delete(test.VUserRamps, groupName)
			if len(test.VUserRamps) == 0 {
				test.VUserRamps = nil
			}
		}
	}
	if len(test.vuserScaleRamps) == 0 {
		test.vuserScaleRamps = nil
	}
}

func cloneVUserRampStatusMap(in map[string]VUserRampStatus) map[string]VUserRampStatus {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]VUserRampStatus, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneGoalTargetStatusMap(in map[string]GoalTargetStatus) map[string]GoalTargetStatus {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]GoalTargetStatus, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func testGroupVUsers(test *ManagedTest, groupName string) (int, bool) {
	if test == nil {
		return 0, false
	}
	for _, group := range test.Groups {
		if group.Name == groupName {
			return groupCurrentVUsers(group), true
		}
	}
	return 0, false
}

func groupCurrentVUsers(group TestGroup) int {
	if group.stageDriven {
		return group.currentVUsers
	}
	return group.VUsers
}

func (tr *TestRunner) applyDurationAdjustment(test *ManagedTest, multiEngine *engine.MultiGroupEngine, backend testExecutionBackend, duration time.Duration) error {
	if multiEngine != nil {
		return multiEngine.AdjustDuration(duration)
	}
	adjustable, ok := backend.(durationAdjustableExecutionBackend)
	if !ok {
		return fmt.Errorf("test backend does not support duration changes")
	}
	return adjustable.AdjustDuration(tr, test, duration)
}

func (tr *TestRunner) applyPause(test *ManagedTest, multiEngine *engine.MultiGroupEngine, backend testExecutionBackend) error {
	if multiEngine != nil {
		return multiEngine.Pause()
	}
	pausable, ok := backend.(pausableExecutionBackend)
	if !ok {
		return fmt.Errorf("test backend does not support pause")
	}
	return pausable.PauseTest(tr, test)
}

func (tr *TestRunner) applyResume(test *ManagedTest, multiEngine *engine.MultiGroupEngine, backend testExecutionBackend) error {
	if multiEngine != nil {
		return multiEngine.Resume()
	}
	pausable, ok := backend.(pausableExecutionBackend)
	if !ok {
		return fmt.Errorf("test backend does not support resume")
	}
	return pausable.ResumeTest(tr, test)
}

func (tr *TestRunner) AdjustDuration(id string, newDuration time.Duration) error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	tr.mu.RUnlock()
	unlockControl, err := tr.lockTestLifecycleControl(context.Background(), id)
	if err != nil {
		return err
	}
	defer unlockControl()
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}

	tr.mu.RLock()
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.RUnlock()
		return fmt.Errorf("test not found: %s", id)
	}
	status := test.Status
	finalizing := test.remoteFinalizing
	oldDuration := test.Duration
	oldDurationStr := test.DurationStr
	multiEngine := test.multiEngine
	backend := tr.resolveBackend(test)
	tr.mu.RUnlock()
	if finalizing {
		return fmt.Errorf("test is finalizing and cannot adjust duration")
	}
	if status != "RUNNING" {
		return fmt.Errorf("test is %s, must be RUNNING to adjust duration", status)
	}
	if err := tr.applyDurationAdjustment(test, multiEngine, backend, newDuration); err != nil {
		return err
	}

	tr.mu.Lock()
	if !tr.isExecutionLeader() {
		tr.mu.Unlock()
		if rollbackErr := tr.applyDurationAdjustment(test, multiEngine, backend, oldDuration); rollbackErr != nil {
			return errors.Join(errNotExecutionLeader, fmt.Errorf("restore backend duration: %w", rollbackErr))
		}
		return errNotExecutionLeader
	}
	if test.Status != status || test.remoteFinalizing {
		currentStatus := test.Status
		finalizing := test.remoteFinalizing
		tr.mu.Unlock()
		if rollbackErr := tr.applyDurationAdjustment(test, multiEngine, backend, oldDuration); rollbackErr != nil {
			return errors.Join(fmt.Errorf("test changed while duration was being adjusted"), fmt.Errorf("restore backend duration: %w", rollbackErr))
		}
		if finalizing {
			return fmt.Errorf("test began finalizing while duration was being adjusted")
		}
		return fmt.Errorf("test changed from %s to %s while duration was being adjusted", status, currentStatus)
	}
	test.Duration = newDuration
	test.DurationStr = newDuration.String()
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Duration = oldDuration
		test.DurationStr = oldDurationStr
		tr.mu.Unlock()
		if rollbackErr := tr.applyDurationAdjustment(test, multiEngine, backend, oldDuration); rollbackErr != nil {
			return errors.Join(fmt.Errorf("persist duration adjustment: %w", err), fmt.Errorf("restore backend duration: %w", rollbackErr))
		}
		return fmt.Errorf("persist duration adjustment: %w", err)
	}
	tr.mu.Unlock()

	tr.hub.BroadcastJSON(map[string]any{
		"type":         "duration_changed",
		"test_id":      id,
		"project_id":   normalizeProjectID(test.ProjectID),
		"new_duration": newDuration.String(),
		"timestamp":    time.Now(),
	})

	return nil
}

func (tr *TestRunner) PauseTest(id string) error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	tr.mu.RUnlock()
	unlockControl, err := tr.lockTestLifecycleControl(context.Background(), id)
	if err != nil {
		return err
	}
	defer unlockControl()
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}

	tr.mu.RLock()
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.RUnlock()
		return fmt.Errorf("test not found: %s", id)
	}
	status := test.Status
	finalizing := test.remoteFinalizing
	multiEngine := test.multiEngine
	backend := tr.resolveBackend(test)
	tr.mu.RUnlock()
	if finalizing {
		return fmt.Errorf("test is finalizing and cannot be paused")
	}
	if status != "RUNNING" {
		return fmt.Errorf("test is %s, must be RUNNING to pause", status)
	}
	if err := tr.applyPause(test, multiEngine, backend); err != nil {
		return err
	}

	tr.mu.Lock()
	if !tr.isExecutionLeader() {
		tr.mu.Unlock()
		if rollbackErr := tr.applyResume(test, multiEngine, backend); rollbackErr != nil {
			return errors.Join(errNotExecutionLeader, fmt.Errorf("resume backend after leadership loss: %w", rollbackErr))
		}
		return errNotExecutionLeader
	}
	if test.Status != "RUNNING" || test.remoteFinalizing {
		currentStatus := test.Status
		finalizing := test.remoteFinalizing
		tr.mu.Unlock()
		if rollbackErr := tr.applyResume(test, multiEngine, backend); rollbackErr != nil {
			return errors.Join(fmt.Errorf("test changed while pause was being applied"), fmt.Errorf("resume backend: %w", rollbackErr))
		}
		if finalizing {
			return fmt.Errorf("test began finalizing while pause was being applied")
		}
		return fmt.Errorf("test changed from RUNNING to %s while pause was being applied", currentStatus)
	}
	test.Status = "PAUSED"
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Status = "RUNNING"
		tr.mu.Unlock()
		if rollbackErr := tr.applyResume(test, multiEngine, backend); rollbackErr != nil {
			return errors.Join(fmt.Errorf("persist paused test state: %w", err), fmt.Errorf("resume backend after persistence failure: %w", rollbackErr))
		}
		return fmt.Errorf("persist paused test state: %w", err)
	}
	if test.LiveMetrics != nil {
		updated := cloneLiveMetrics(test.LiveMetrics)
		updated.ActiveVUsers = 0
		for _, group := range updated.GroupMetrics {
			if group != nil {
				group.VUsers = 0
			}
		}
		replaceLiveMetricsLocked(test, updated)
	}
	tr.mu.Unlock()

	tr.broadcastEvent("test_paused", test)

	return nil
}

func (tr *TestRunner) ResumeTest(id string) error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	tr.mu.RUnlock()
	unlockControl, err := tr.lockTestLifecycleControl(context.Background(), id)
	if err != nil {
		return err
	}
	defer unlockControl()
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}

	tr.mu.RLock()
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.RUnlock()
		return fmt.Errorf("test not found: %s", id)
	}
	status := test.Status
	finalizing := test.remoteFinalizing
	multiEngine := test.multiEngine
	backend := tr.resolveBackend(test)
	tr.mu.RUnlock()
	if finalizing {
		return fmt.Errorf("test is finalizing and cannot be resumed")
	}
	if status != "PAUSED" {
		return fmt.Errorf("test is %s, must be PAUSED to resume", status)
	}
	if err := tr.applyResume(test, multiEngine, backend); err != nil {
		return err
	}

	tr.mu.Lock()
	if !tr.isExecutionLeader() {
		tr.mu.Unlock()
		if rollbackErr := tr.applyPause(test, multiEngine, backend); rollbackErr != nil {
			return errors.Join(errNotExecutionLeader, fmt.Errorf("pause backend after leadership loss: %w", rollbackErr))
		}
		return errNotExecutionLeader
	}
	if test.Status != "PAUSED" || test.remoteFinalizing {
		currentStatus := test.Status
		finalizing := test.remoteFinalizing
		tr.mu.Unlock()
		if rollbackErr := tr.applyPause(test, multiEngine, backend); rollbackErr != nil {
			return errors.Join(fmt.Errorf("test changed while resume was being applied"), fmt.Errorf("pause backend: %w", rollbackErr))
		}
		if finalizing {
			return fmt.Errorf("test began finalizing while resume was being applied")
		}
		return fmt.Errorf("test changed from PAUSED to %s while resume was being applied", currentStatus)
	}
	test.Status = "RUNNING"
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Status = "PAUSED"
		tr.mu.Unlock()
		if rollbackErr := tr.applyPause(test, multiEngine, backend); rollbackErr != nil {
			return errors.Join(fmt.Errorf("persist resumed test state: %w", err), fmt.Errorf("pause backend after persistence failure: %w", rollbackErr))
		}
		return fmt.Errorf("persist resumed test state: %w", err)
	}
	// Pause publishes an immediate zero-VUser snapshot. Restore the configured
	// live counts as part of the durable resume commit instead of leaving the API
	// and dashboard at zero until the next generator/engine metrics interval.
	if test.LiveMetrics != nil {
		updated := cloneLiveMetrics(test.LiveMetrics)
		activeVUsers := 0
		for _, configuredGroup := range test.Groups {
			vusers := groupCurrentVUsers(configuredGroup)
			activeVUsers += vusers
			if groupMetrics := updated.GroupMetrics[configuredGroup.Name]; groupMetrics != nil {
				groupMetrics.VUsers = vusers
			}
		}
		updated.ActiveVUsers = int64(activeVUsers)
		replaceLiveMetricsLocked(test, updated)
	}
	tr.mu.Unlock()

	tr.broadcastEvent("test_resumed", test)

	return nil
}

func (tr *TestRunner) runTest(test *ManagedTest) {
	// Defense in depth: a non-leader must never drive a generator fleet.
	if !tr.isExecutionLeader() {
		return
	}
	tr.mu.RLock()
	current := tr.tests[test.ID]
	startable := !tr.shuttingDown && current == test && test.Status == "INITIALIZING" && test.executionCtx != nil && test.executionCtx.Err() == nil
	tr.mu.RUnlock()
	if !startable || !tr.isExecutionLeader() {
		return
	}
	backend := tr.resolveBackend(test)
	if backend == nil {
		backend = localExecutionBackend{}
	}
	backend.Run(tr, test)
}

func (tr *TestRunner) buildRunEngine(test *ManagedTest, resultSink engine.ResultSink) (*engine.MultiGroupEngine, error) {
	preBaseline, postBaseline := testexec.ResourceBaselineWindows(test.ResourceBaseline)
	preBaseline += testexec.StartDelayWindow(test.StartDelay)
	return engine.NewMultiGroup(engine.MultiGroupConfig{
		Groups:       testexec.CloneGroupSpecsWithResultSink(test.groupSpecs, resultSink),
		Duration:     test.Duration,
		PreBaseline:  preBaseline,
		PostBaseline: postBaseline,
	})
}

func applyGoalControllerUpdate(tr *TestRunner, test *ManagedTest, live *LiveMetrics, groupVUsers map[string]int, update goalControllerUpdate, scaleVUsers func(groupName string, target int) error) {
	live.GoalStatus = update.Status
	if !update.ShouldScale {
		return
	}

	// Snapshot the group set + target statuses under the lock. The scaleVUsers
	// engine call must run unlocked, and the resulting test.Groups/goalControl
	// writes are re-taken under the lock because they share state with the API
	// read paths that copy *ManagedTest (GetTest/ListTests/capacity scans).
	tr.mu.RLock()
	groups := append([]TestGroup(nil), test.Groups...)
	targetStatuses := currentGoalTargetStatuses(test, live.Timestamp)
	tr.mu.RUnlock()

	targets := distributeTargetVUsersForGoal(groups, groupVUsers, update.TargetTotal, targetStatuses)
	for _, group := range groups {
		target, exists := targets[group.Name]
		if !exists || groupVUsers[group.Name] == target {
			continue
		}
		if err := scaleVUsers(group.Name, target); err != nil {
			slog.Warn("goal-oriented scale failed", "test_id", test.ID, "group", group.Name, "target_vusers", target, "error", err)
			live.GoalStatus.LastScaleReason = fmt.Sprintf("scale failed: %v", err)
			break
		}
		groupVUsers[group.Name] = target
		tr.mu.Lock()
		for i := range test.Groups {
			if test.Groups[i].Name == group.Name {
				test.Groups[i].VUsers = target
				break
			}
		}
		tr.mu.Unlock()
	}

	now := live.Timestamp
	tr.mu.Lock()
	if test.goalControl == nil {
		// Defense-in-depth: the distributed path may reach a scale decision
		// before run-state init in pathological orderings; never nil-deref here.
		test.goalControl = &goalControllerState{}
	}
	test.goalControl.lastScaleAt = &now
	test.goalControl.lastScaleDirection = update.ScaleDirection
	test.goalControl.lastScaleReason = update.ScaleReason
	tr.mu.Unlock()
	live.GoalStatus.LastScaleAt = &now
	live.GoalStatus.LastScaleDirection = update.ScaleDirection
	live.GoalStatus.LastScaleReason = update.ScaleReason
	live.GoalStatus.CurrentTotalVUsers = totalGroupVUsers(groupVUsers)
	live.GoalStatus.SuggestedTotalVUsers = live.GoalStatus.CurrentTotalVUsers
}

func (tr *TestRunner) requestStopIfRunning(test *ManagedTest, warningMessage string) {
	tr.mu.RLock()
	status := test.Status
	tr.mu.RUnlock()
	if status != "RUNNING" {
		return
	}
	if err := tr.StopTest(test.ID); err != nil {
		slog.Warn(warningMessage, "test_id", test.ID, "error", err)
	}
}

func (tr *TestRunner) startManagedTestRun(test *ManagedTest, mge *engine.MultiGroupEngine, artifacts *runArtifacts) (context.Context, chan struct{}, chan struct{}, bool, error) {
	now := time.Now()
	tr.mu.Lock()
	previousExecutionCtx := test.executionCtx
	previousCancel := test.cancel
	ctx := test.executionCtx
	if ctx == nil || test.cancel == nil {
		ctx, test.cancel = tr.executionOperationContext(context.Background())
		test.executionCtx = ctx
	}
	if tr.shuttingDown || test.Status != "INITIALIZING" || ctx.Err() != nil || !tr.isExecutionLeader() {
		tr.mu.Unlock()
		return ctx, nil, nil, false, nil
	}
	previousMultiEngine := test.multiEngine
	previousStartedAt := cloneTimePtr(test.StartedAt)
	previousEndedAt := cloneTimePtr(test.EndedAt)
	previousStatus := test.Status
	previousPendingSince := cloneTimePtr(test.PendingSince)
	previousPendingReason := test.PendingReason
	previousQueuePosition := test.QueuePosition
	previousError := test.Error
	previousGoalControl := test.goalControl
	test.multiEngine = mge
	test.StartedAt = &now
	test.EndedAt = nil
	test.Status = "RUNNING"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = ""
	test.goalControl = &goalControllerState{}
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.multiEngine = previousMultiEngine
		test.StartedAt = previousStartedAt
		test.EndedAt = previousEndedAt
		test.Status = previousStatus
		test.PendingSince = previousPendingSince
		test.PendingReason = previousPendingReason
		test.QueuePosition = previousQueuePosition
		test.Error = previousError
		test.goalControl = previousGoalControl
		if previousExecutionCtx == nil || previousCancel == nil {
			test.cancel()
			test.executionCtx = previousExecutionCtx
			test.cancel = previousCancel
		}
		tr.mu.Unlock()
		return ctx, nil, nil, false, fmt.Errorf("persist running test state: %w", err)
	}
	snapshot := buildRunLiveStateSnapshotLocked(test, now)
	tr.mu.Unlock()

	tr.cacheRunSnapshot(snapshot)
	tr.broadcastEvent("test_started", test)
	tr.startStageSchedules(ctx, test, mge)

	metricsDone := make(chan struct{})
	metricsStopped := make(chan struct{})
	go func() {
		tr.broadcastMetrics(test, mge, artifacts, metricsDone)
		close(metricsStopped)
	}()

	return ctx, metricsDone, metricsStopped, true, nil
}

func (tr *TestRunner) failTestBeforeRun(test *ManagedTest, message string) error {
	now := time.Now()
	tr.mu.Lock()
	// Startup work runs asynchronously. A stop or leadership-loss cleanup may
	// have won while a backend was compiling scripts, dialing generators, or
	// assigning shards. Never resurrect that terminal decision as FAILED.
	if test == nil || tr.tests[test.ID] != test || test.Status != "INITIALIZING" {
		tr.mu.Unlock()
		return nil
	}
	if err := tr.persistStoredTestStatus(test.ID, "FAILED", &now, message); err != nil {
		tr.mu.Unlock()
		retryErr := fmt.Errorf("persist pre-run test failure: %w", err)
		slog.Error("failed to persist pre-run test failure; terminal side effects deferred", "test_id", test.ID, "error", retryErr)
		tr.enqueueTerminalRetry(test.ID, "FAILED before run", func() error {
			return tr.failTestBeforeRun(test, message)
		})
		return retryErr
	}
	test.Status = "FAILED"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = message
	test.EndedAt = &now
	test.cancel = nil
	test.executionCtx = nil
	tr.mu.Unlock()
	releaseErr := tr.releaseCapacityReservationForTerminalStatus(test.ID, "FAILED")
	if releaseErr != nil && test.TimeslotID != nil {
		tr.enqueueCapacityReservationReleaseRetry(test.ID, strings.TrimSpace(*test.TimeslotID))
	}
	tr.broadcastEvent("test_failed", test)
	tr.promotePendingTests()
	if err := tr.advanceExecutionPlanForTest(test, "FAILED"); err != nil {
		slog.Error("failed to durably advance execution plan after pre-run failure", "test_id", test.ID, "error", err)
		return errors.Join(releaseErr, err)
	}
	return releaseErr
}

func (tr *TestRunner) failTestBeforeRunAndLog(test *ManagedTest, message string) {
	if err := tr.failTestBeforeRun(test, message); err != nil {
		testID := ""
		if test != nil {
			testID = test.ID
		}
		slog.Error("pre-run terminal transition remains retryable after bounded persistence retries", "test_id", testID, "error", err)
	}
}

func (tr *TestRunner) completeTestRun(test *ManagedTest, finalStatus string, endTime time.Time, finalError string, archiveRecord *storage.RunArchiveRecord) error {
	if tr == nil || test == nil {
		return nil
	}
	unlockArchiveCommit := tr.lockArchiveCommitFence(test.ID)
	defer unlockArchiveCommit()
	return tr.completeTestRunUnderArchiveFence(test, finalStatus, endTime, finalError, archiveRecord)
}

// completeTestRunUnderArchiveFence performs the terminal row and archive-map
// commit while the caller owns test.ID's archive commit fence.
func (tr *TestRunner) completeTestRunUnderArchiveFence(test *ManagedTest, finalStatus string, endTime time.Time, finalError string, archiveRecord *storage.RunArchiveRecord) error {
	tr.mu.Lock()
	if test == nil || tr.tests[test.ID] != test {
		tr.mu.Unlock()
		return nil
	}
	// Completion retries are responsible only for the terminal test row. Once a
	// terminal transition committed, any execution-plan follow-up has its own
	// idempotent retry worker; replaying terminal side effects here would duplicate
	// WebSocket events, usage snapshots, and queue promotion.
	switch test.Status {
	case "COMPLETE", "FAILED", "ABORTED":
		tr.mu.Unlock()
		return nil
	}
	if err := tr.persistStoredTestStatus(test.ID, finalStatus, &endTime, finalError); err != nil {
		tr.mu.Unlock()
		retryErr := fmt.Errorf("persist terminal test state: %w", err)
		slog.Error("failed to persist terminal test state; terminal side effects deferred", "test_id", test.ID, "status", finalStatus, "error", retryErr)
		tr.enqueueTestCompletionRetry(test, finalStatus, endTime, finalError, archiveRecord)
		return retryErr
	}
	tr.cancelAllVUserScaleRampsLocked(test)
	test.Status = finalStatus
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = finalError
	test.EndedAt = &endTime
	test.cancel = nil
	test.executionCtx = nil
	test.Archive = archiveRecord
	if archiveRecord != nil {
		tr.archives[test.ID] = archiveRecord
	}
	tr.mu.Unlock()
	releaseErr := tr.releaseCapacityReservationForTerminalStatus(test.ID, finalStatus)
	if releaseErr != nil && test.TimeslotID != nil {
		tr.enqueueCapacityReservationReleaseRetry(test.ID, strings.TrimSpace(*test.TimeslotID))
	}
	tr.captureUsageSnapshotForTerminalRun(test.ID, test.ProjectID)
	if finalStatus == "FAILED" {
		tr.broadcastEvent("test_failed", test)
		tr.promotePendingTests()
		if err := tr.advanceExecutionPlanForTest(test, finalStatus); err != nil {
			slog.Error("failed to durably advance execution plan after terminal test failure", "test_id", test.ID, "error", err)
			return errors.Join(releaseErr, err)
		}
		return releaseErr
	}
	tr.broadcastEvent("test_complete", test)
	tr.promotePendingTests()
	if err := tr.advanceExecutionPlanForTest(test, finalStatus); err != nil {
		slog.Error("failed to durably advance execution plan after terminal test completion", "test_id", test.ID, "error", err)
		return errors.Join(releaseErr, err)
	}
	return releaseErr
}

func (tr *TestRunner) promotePendingTests() {
	// Promotion starts work, so it is leader-only. Non-leaders never promote;
	// the leader drives the shared queue.
	if !tr.isExecutionLeader() {
		return
	}
	tr.promotePendingTestsAllowed()
}

func (tr *TestRunner) promotePendingTestsAllowed() {
	for {
		tr.mu.Lock()
		if tr.shuttingDown {
			tr.mu.Unlock()
			return
		}
		pending := tr.nextPromotablePendingTestLocked()
		if pending == nil {
			tr.mu.Unlock()
			return
		}
		if pending.ScriptShakeout {
			previousStatus := pending.Status
			previousPendingSince := cloneTimePtr(pending.PendingSince)
			previousPendingReason := pending.PendingReason
			previousQueuePosition := pending.QueuePosition
			previousError := pending.Error
			pending.Status = "SHAKEOUT"
			pending.PendingSince = nil
			pending.PendingReason = ""
			pending.QueuePosition = 0
			pending.Error = ""
			if err := tr.saveManagedTestRecord(pending); err != nil {
				pending.Status = previousStatus
				pending.PendingSince = previousPendingSince
				pending.PendingReason = previousPendingReason
				pending.QueuePosition = previousQueuePosition
				pending.Error = previousError
				tr.mu.Unlock()
				slog.Warn("pending test promotion could not persist shakeout", "test_id", pending.ID, "error", err)
				return
			}
			tr.broadcastEvent("script_shakeout_started", pending)
			tr.mu.Unlock()

			report := tr.runScriptShakeoutReport(pending)
			if !report.Passed {
				if err := tr.markScriptShakeoutReview(pending.ID, report); err != nil {
					var gateErr *ScriptShakeoutGateError
					if !errors.As(err, &gateErr) {
						slog.Warn("pending test promotion could not persist shakeout review", "test_id", pending.ID, "error", err)
					}
				}
				tr.recordSystemTestAudit("test.script_shakeout.review", "blocked", pending, map[string]any{"report": report})
				continue
			}

			tr.mu.Lock()
			current, ok := tr.tests[pending.ID]
			if !ok || current.Status != "SHAKEOUT" || !tr.holdsExecutionLeadership() {
				tr.mu.Unlock()
				return
			}
			pending = current
			// Return to a durable retryable state before attempting INITIALIZING.
			// If that second transition cannot be saved, a later reconciliation can
			// safely rerun the deterministic shakeout instead of stranding SHAKEOUT.
			shakeoutStatus := pending.Status
			shakeoutError := pending.Error
			now := time.Now()
			pending.Status = "PENDING"
			pending.PendingSince = &now
			pending.PendingReason = "awaiting capacity after script shakeout"
			pending.QueuePosition = 0
			pending.Error = ""
			if err := tr.saveManagedTestRecord(pending); err != nil {
				pending.Status = shakeoutStatus
				pending.PendingSince = nil
				pending.PendingReason = ""
				pending.Error = shakeoutError
				tr.mu.Unlock()
				slog.Warn("pending test promotion could not persist successful shakeout", "test_id", pending.ID, "error", err)
				return
			}
			tr.broadcastEvent("script_shakeout_passed", pending)
		}
		previousStatus := pending.Status
		previousPendingSince := cloneTimePtr(pending.PendingSince)
		previousPendingReason := pending.PendingReason
		previousQueuePosition := pending.QueuePosition
		previousError := pending.Error
		previousExecutionCtx := pending.executionCtx
		previousCancel := pending.cancel
		previousBackend := pending.executionBackend
		tr.markTestInitializingLocked(pending)
		if err := tr.saveManagedTestRecord(pending); err != nil {
			if pending.cancel != nil {
				pending.cancel()
			}
			pending.Status = previousStatus
			pending.PendingSince = previousPendingSince
			pending.PendingReason = previousPendingReason
			pending.QueuePosition = previousQueuePosition
			pending.Error = previousError
			pending.executionCtx = previousExecutionCtx
			pending.cancel = previousCancel
			pending.executionBackend = previousBackend
			tr.mu.Unlock()
			slog.Warn("pending test promotion could not persist initializing state", "test_id", pending.ID, "error", err)
			return
		}
		if previousCancel != nil {
			previousCancel()
		}
		tr.mu.Unlock()
		tr.recordSystemTestAudit("test.pending.promote", "success", pending, map[string]any{
			"status": pending.Status,
			"reason": "fair_share_capacity_available",
		})
		go tr.runTest(pending)
	}
}

func (tr *TestRunner) recordSystemTestAudit(action string, status string, test *ManagedTest, details map[string]any) {
	if tr == nil || tr.scenarioStore == nil || test == nil {
		return
	}
	if err := tr.scenarioStore.SaveAuditEvent(&storage.AuditEvent{
		ID:               uuid.New().String(),
		Action:           action,
		ActorType:        "system",
		ActorSubject:     "test-runner",
		ActorDisplayName: "Test Runner",
		ProjectID:        normalizeProjectID(test.ProjectID),
		ResourceType:     "test",
		ResourceID:       test.ID,
		Status:           status,
		Details:          details,
		CreatedAt:        time.Now().UTC(),
	}); err != nil {
		slog.Warn("failed to persist system test audit event", "test_id", test.ID, "action", action, "error", err)
	}
}

func (tr *TestRunner) nextPromotablePendingTestLocked() *ManagedTest {
	pending := make([]*ManagedTest, 0)
	for _, test := range tr.tests {
		if test.Status == "PENDING" {
			pending = append(pending, test)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	tr.sortPendingTestsForFairShareLocked(pending)
	for _, test := range pending {
		if tr.projectDeletionErrorLocked(test.ProjectID) != nil {
			continue
		}
		tr.refreshCapacityAdvisoryLocked(test)
		if buildStartGovernanceError(test.ID, test.CapacityAdvisory, StartTestOptions{}) != nil {
			continue
		}
		if blocked, _ := tr.evaluateQuotaBlockLocked(test, ""); blocked {
			continue
		}
		return test
	}
	return nil
}

func (tr *TestRunner) runScriptShakeoutReport(test *ManagedTest) ScriptShakeoutReport {
	report := ScriptShakeoutReport{
		Passed:    true,
		StartedAt: time.Now().UTC(),
		Results:   make([]ScriptShakeoutResult, 0, len(test.groupSpecs)),
	}
	for _, group := range test.groupSpecs {
		result := ScriptShakeoutResult{
			GroupName: group.Name,
			Status:    "passed",
		}
		if group.Script == nil {
			result.Status = "failed"
			result.Error = "script is missing"
			report.Passed = false
			report.Results = append(report.Results, result)
			continue
		}
		result.ScriptID = group.Script.ID
		result.ScriptName = group.Script.Name

		shakeoutScript := *group.Script
		shakeoutScript.Config.RunLogic.Iterations = 1

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := engine.RunScriptShakeout(ctx, group.Name, &shakeoutScript)
		cancel()
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			report.Passed = false
		}
		report.Results = append(report.Results, result)
	}

	report.EndedAt = time.Now().UTC()
	if report.Passed {
		report.Summary = fmt.Sprintf("Script shakeout passed for %d group%s; full load can start.", len(report.Results), pluralSuffix(len(report.Results)))
	} else {
		report.Summary = "Script shakeout failed; full load test has not started. Review the evidence and choose Continue or Cancel."
	}
	return report
}

func formatScriptShakeoutEvidence(report ScriptShakeoutReport) string {
	if report.Passed {
		return report.Summary
	}
	lines := []string{report.Summary}
	for _, result := range report.Results {
		if result.Status != "failed" {
			continue
		}
		label := result.GroupName
		if result.ScriptName != "" {
			label = fmt.Sprintf("%s (%s)", result.GroupName, result.ScriptName)
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", label, result.Error))
	}
	return strings.Join(lines, "\n")
}
