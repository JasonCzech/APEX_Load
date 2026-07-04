package api

// The run state machine: Start/Stop/Abort/Pause/Resume/AdjustDuration with
// startability + preflight gating, script-shakeout gating, run setup
// (runTest/buildRunEngine) and lifecycle (start/fail/complete), VUser scaling
// and scale-ramps, goal-target control, and pending-test promotion.

import (
	"context"
	"fmt"
	"github.com/jasonczech/apexload/pkg/api/testexec"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
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
		tr.markScriptShakeoutPassed(id, report)
	}

	tr.mu.Lock()
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
func (tr *TestRunner) markTestPendingLocked(test *ManagedTest, reason string) {
	now := time.Now()
	test.Status = "PENDING"
	if test.PendingSince == nil || test.PendingSince.IsZero() {
		test.PendingSince = &now
	}
	test.PendingReason = reason
	test.QueuePosition = 0
	test.Error = ""
	tr.saveManagedTestRecord(test)
	tr.broadcastEvent("test_pending", test)
}

// markTestInitializingLocked transitions test to INITIALIZING, clears its pending
// fields, and binds its execution backend. The caller persists the record because
// the save timing differs (under the lock for prepare, after unlock for promote).
func (tr *TestRunner) markTestInitializingLocked(test *ManagedTest) {
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
		tr.markTestPendingLocked(test, reason)
		return true, nil
	}
	return false, nil
}

func (tr *TestRunner) preflightStartBlocksLocked(id string, opts StartTestOptions) (bool, error) {
	test, ok := tr.tests[id]
	if !ok {
		return false, fmt.Errorf("test not found: %s", id)
	}
	if !testexec.IsStartableTestStatus(test.Status) {
		return false, testexec.StartableTestStatusError(test.Status)
	}
	return tr.evaluateStartGatesLocked(id, test, opts)
}

func (tr *TestRunner) beginScriptShakeoutIfNeeded(id string, opts StartTestOptions) (*ManagedTest, error) {
	if opts.OverrideScriptShakeoutError {
		return nil, nil
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()

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

	test.Status = "SHAKEOUT"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = ""
	tr.saveManagedTestRecord(test)
	tr.broadcastEvent("script_shakeout_started", test)

	return test, nil
}

func (tr *TestRunner) markScriptShakeoutPassed(id string, _ ScriptShakeoutReport) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	test, ok := tr.tests[id]
	if !ok {
		return
	}
	if test.Status != "SHAKEOUT" {
		return
	}
	test.Status = "QUEUED"
	test.Error = ""
	tr.saveManagedTestRecord(test)
	tr.broadcastEvent("script_shakeout_passed", test)
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
	test.Status = "SHAKEOUT_REVIEW"
	test.Error = formatScriptShakeoutEvidence(report)
	tr.saveManagedTestRecord(test)
	tr.broadcastEvent("script_shakeout_failed", test)
	return &ScriptShakeoutGateError{TestID: id, Report: &report}
}

func (tr *TestRunner) prepareTestStartLocked(id string, opts StartTestOptions) (*ManagedTest, error) {
	test, ok := tr.tests[id]
	if !ok {
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if !testexec.IsStartableTestStatus(test.Status) {
		return nil, testexec.StartableTestStatusError(test.Status)
	}

	if blocked, err := tr.evaluateStartGatesLocked(id, test, opts); err != nil {
		return nil, err
	} else if blocked {
		return nil, nil
	}

	tr.markTestInitializingLocked(test)
	tr.saveManagedTestRecord(test)
	return test, nil
}

func (tr *TestRunner) ReconcilePendingTests() {
	// Only the execution leader reconciles. On a non-leader replica (HA) this is
	// a no-op; the eventual leader runs it via reloadAndReconcile on acquisition.
	if !tr.isExecutionLeader() {
		return
	}
	tr.promotePendingTests()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.refreshPendingQueueMetadataLocked()
}

// reloadAndReconcile refreshes in-memory test state from the database and then
// reconciles. It runs when this replica acquires execution leadership — at boot
// when it wins the lease, or after a failover takeover where the previous
// leader's runs and any tests created elsewhere are not yet in memory.
func (tr *TestRunner) reloadAndReconcile() {
	if !tr.isExecutionLeader() {
		return
	}
	tr.reloadStoredTests()
	tr.ReconcilePendingTests()
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
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	id, test, status, err := tr.lockedTestStatus(id)
	if err != nil {
		return err
	}
	if status == "QUEUED" || status == "PENDING" || status == "RESERVED" || status == "SHAKEOUT" || status == "SHAKEOUT_REVIEW" {
		tr.mu.Lock()
		if current, ok := tr.tests[id]; ok {
			now := time.Now().UTC()
			current.Status = "ABORTED"
			current.PendingSince = nil
			current.PendingReason = ""
			current.QueuePosition = 0
			current.EndedAt = &now
			if current.Error == "" {
				current.Error = "test cancelled before start"
			}
			tr.saveManagedTestRecord(current)
			tr.broadcastEvent("test_aborted", current)
		}
		tr.mu.Unlock()
		tr.advanceExecutionPlanForTest(test, "ABORTED")
		return nil
	}
	tr.mu.Lock()
	tr.cancelAllVUserScaleRampsLocked(test)
	tr.mu.Unlock()
	if test.multiEngine == nil {
		backend := tr.resolveBackend(test)
		if stoppable, ok := backend.(stoppableExecutionBackend); ok {
			if err := stoppable.StopTest(tr, test); err != nil {
				return err
			}
		} else if test.cancel == nil {
			return fmt.Errorf("test backend does not support stop")
		}
	}
	if test.cancel != nil {
		test.cancel()
	}
	tr.mu.Lock()
	if test.Status == "COMPLETE" || test.Status == "FAILED" {
		tr.mu.Unlock()
		return nil
	}
	test.Status = "STOPPING"
	tr.mu.Unlock()
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
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.Unlock()
		return nil, fmt.Errorf("test not found: %s", id)
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

	if test.GoalConfig == nil {
		test.GoalConfig = &GoalConfig{}
	}
	test.GoalTargets = cloneGoalTargetStatusMap(targets)
	test.GoalConfig.TargetAverageTPS = &totalTargetTPS
	if test.goalControl != nil {
		test.goalControl.lastScaleAt = nil
	}
	if test.LiveMetrics != nil {
		applyRuntimeGoalTargetsToLiveMetrics(test, test.LiveMetrics)
	}
	tr.saveManagedTestRecord(test)
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
	if targetVUsers < 0 {
		return fmt.Errorf("target vusers must be non-negative")
	}
	if rampDuration <= 0 {
		return fmt.Errorf("ramp duration must be positive")
	}

	tr.mu.Lock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.Unlock()
		return fmt.Errorf("test not found: %s", id)
	}
	if test.Status != "RUNNING" {
		tr.mu.Unlock()
		return fmt.Errorf("test is %s, must be RUNNING to scale", test.Status)
	}

	currentVUsers, found := testGroupVUsers(test, groupName)
	if !found {
		tr.mu.Unlock()
		return fmt.Errorf("group not found: %s", groupName)
	}
	tr.cancelVUserScaleRampLocked(test, groupName)
	if currentVUsers == targetVUsers {
		tr.mu.Unlock()
		return nil
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
	ctx, cancel := context.WithCancel(context.Background())
	ramp := &vuserScaleRamp{cancel: cancel, status: rampStatus}
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
	return nil
}

func (tr *TestRunner) runVUserScaleRamp(ctx context.Context, ramp *vuserScaleRamp, id string, groupName string, currentVUsers int, targetVUsers int, rampDuration time.Duration) {
	defer tr.finishVUserScaleRamp(id, groupName, ramp)

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
			return
		case <-timer.C:
		}

		nextTarget := currentVUsers + direction*step
		if err := tr.scaleVUsersNow(id, groupName, nextTarget, false); err != nil {
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
			if err := tr.ScaleVUsersWithOptions(testID, groupName, stage.TargetVUsers, ScaleVUsersOptions{RampDuration: duration}); err != nil {
				slog.Warn("stage schedule scale failed", "test_id", testID, "group", groupName, "stage", index, "target_vusers", stage.TargetVUsers, "error", err)
				return
			}
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
	tr.mu.Lock()
	id = tr.resolveRefKeyLocked(id)
	test, ok := tr.tests[id]
	if !ok {
		tr.mu.Unlock()
		return fmt.Errorf("test not found: %s", id)
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

	totalVUsers, projectID := tr.updateTestGroupVUsers(id, groupName, targetVUsers)
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

func (tr *TestRunner) updateTestGroupVUsers(id string, groupName string, targetVUsers int) (int, string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	test, ok := tr.tests[id]
	if !ok {
		return 0, ""
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
	return currentTotalVUsers, normalizeProjectID(test.ProjectID)
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

func (tr *TestRunner) AdjustDuration(id string, newDuration time.Duration) error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	id, test, status, err := tr.lockedTestStatus(id)
	if err != nil {
		return err
	}
	if status != "RUNNING" {
		return fmt.Errorf("test is %s, must be RUNNING to adjust duration", status)
	}
	if test.multiEngine == nil {
		backend := tr.resolveBackend(test)
		adjustable, ok := backend.(durationAdjustableExecutionBackend)
		if !ok {
			return fmt.Errorf("test backend does not support duration changes")
		}
		if err := adjustable.AdjustDuration(tr, test, newDuration); err != nil {
			return err
		}
	} else {
		if err := test.multiEngine.AdjustDuration(newDuration); err != nil {
			return err
		}
	}

	tr.mu.Lock()
	test.Duration = newDuration
	test.DurationStr = newDuration.String()
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
	id, test, status, err := tr.lockedTestStatus(id)
	if err != nil {
		return err
	}
	if status != "RUNNING" {
		return fmt.Errorf("test is %s, must be RUNNING to pause", status)
	}
	if test.multiEngine == nil {
		backend := tr.resolveBackend(test)
		pausable, ok := backend.(pausableExecutionBackend)
		if !ok {
			return fmt.Errorf("test backend does not support pause")
		}
		if err := pausable.PauseTest(tr, test); err != nil {
			return err
		}
	} else {
		if err := test.multiEngine.Pause(); err != nil {
			return err
		}
	}

	tr.mu.Lock()
	test.Status = "PAUSED"
	if test.LiveMetrics != nil {
		test.LiveMetrics.ActiveVUsers = 0
		for _, group := range test.LiveMetrics.GroupMetrics {
			if group != nil {
				group.VUsers = 0
			}
		}
	}
	tr.mu.Unlock()

	tr.broadcastEvent("test_paused", test)

	return nil
}

func (tr *TestRunner) ResumeTest(id string) error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	id, test, status, err := tr.lockedTestStatus(id)
	if err != nil {
		return err
	}
	if status != "PAUSED" {
		return fmt.Errorf("test is %s, must be PAUSED to resume", status)
	}
	if test.multiEngine == nil {
		backend := tr.resolveBackend(test)
		pausable, ok := backend.(pausableExecutionBackend)
		if !ok {
			return fmt.Errorf("test backend does not support resume")
		}
		if err := pausable.ResumeTest(tr, test); err != nil {
			return err
		}
	} else {
		if err := test.multiEngine.Resume(); err != nil {
			return err
		}
	}

	tr.mu.Lock()
	test.Status = "RUNNING"
	tr.mu.Unlock()

	tr.broadcastEvent("test_resumed", test)

	return nil
}

func (tr *TestRunner) runTest(test *ManagedTest) {
	// Defense in depth: a non-leader must never drive a generator fleet.
	if !tr.isExecutionLeader() {
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

func (tr *TestRunner) startManagedTestRun(test *ManagedTest, mge *engine.MultiGroupEngine, artifacts *runArtifacts) (context.Context, chan struct{}, chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())

	now := time.Now()
	tr.mu.Lock()
	test.cancel = cancel
	test.multiEngine = mge
	test.StartedAt = &now
	test.EndedAt = nil
	test.Status = "RUNNING"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = ""
	test.goalControl = &goalControllerState{}
	tr.saveManagedTestRecord(test)
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

	return ctx, metricsDone, metricsStopped
}

func (tr *TestRunner) failTestBeforeRun(test *ManagedTest, message string) {
	now := time.Now()
	tr.mu.Lock()
	test.Status = "FAILED"
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = message
	test.EndedAt = &now
	tr.mu.Unlock()
	tr.updateStoredTestStatus(test.ID, "FAILED", &now, message)
	tr.broadcastEvent("test_failed", test)
	tr.promotePendingTests()
	tr.advanceExecutionPlanForTest(test, "FAILED")
}

func (tr *TestRunner) completeTestRun(test *ManagedTest, finalStatus string, endTime time.Time, finalError string, archiveRecord *storage.RunArchiveRecord) {
	tr.mu.Lock()
	tr.cancelAllVUserScaleRampsLocked(test)
	test.Status = finalStatus
	test.PendingSince = nil
	test.PendingReason = ""
	test.QueuePosition = 0
	test.Error = finalError
	test.EndedAt = &endTime
	test.Archive = archiveRecord
	if archiveRecord != nil {
		tr.archives[test.ID] = archiveRecord
	}
	tr.captureUsageSnapshotForTerminalRunLocked(test.ID, test.ProjectID)
	tr.mu.Unlock()
	tr.updateStoredTestStatus(test.ID, finalStatus, &endTime, finalError)
	if finalStatus == "FAILED" {
		tr.broadcastEvent("test_failed", test)
		tr.promotePendingTests()
		tr.advanceExecutionPlanForTest(test, finalStatus)
		return
	}
	tr.broadcastEvent("test_complete", test)
	tr.promotePendingTests()
	tr.advanceExecutionPlanForTest(test, finalStatus)
}

func (tr *TestRunner) promotePendingTests() {
	// Promotion starts work, so it is leader-only. Non-leaders never promote;
	// the leader drives the shared queue.
	if !tr.isExecutionLeader() {
		return
	}
	for {
		tr.mu.Lock()
		pending := tr.nextPromotablePendingTestLocked()
		if pending == nil {
			tr.mu.Unlock()
			return
		}
		if pending.ScriptShakeout {
			pending.Status = "SHAKEOUT"
			pending.PendingSince = nil
			pending.PendingReason = ""
			pending.QueuePosition = 0
			pending.Error = ""
			tr.saveManagedTestRecord(pending)
			tr.broadcastEvent("script_shakeout_started", pending)
			tr.mu.Unlock()

			report := tr.runScriptShakeoutReport(pending)
			if !report.Passed {
				_ = tr.markScriptShakeoutReview(pending.ID, report)
				tr.recordSystemTestAudit("test.script_shakeout.review", "blocked", pending, map[string]any{"report": report})
				continue
			}

			tr.mu.Lock()
			current, ok := tr.tests[pending.ID]
			if !ok || current.Status != "SHAKEOUT" {
				tr.mu.Unlock()
				continue
			}
			pending = current
		}
		tr.markTestInitializingLocked(pending)
		tr.mu.Unlock()
		tr.saveManagedTestRecord(pending)
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
	_ = tr.scenarioStore.SaveAuditEvent(&storage.AuditEvent{
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
	})
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
