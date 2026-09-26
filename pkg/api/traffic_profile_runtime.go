package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/api/testexec"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/trafficprofile"
)

func (tr *TestRunner) startTrafficProfileSchedules(ctx context.Context, test *ManagedTest, mge *engine.MultiGroupEngine) {
	if !tr.isExecutionLeader() || test == nil {
		return
	}
	// Every mutable ManagedTest field is read under the lock, matching
	// startStageSchedules. TrafficProfile was previously probed for nil outside
	// it, which raced any writer holding tr.mu.
	tr.mu.RLock()
	testID := test.ID
	profile := cloneTrafficProfile(test.TrafficProfile)
	preBaseline, _ := testexec.ResourceBaselineWindows(test.ResourceBaseline)
	preBaseline += testexec.StartDelayWindow(test.StartDelay)
	tr.mu.RUnlock()
	if profile == nil {
		return
	}
	for _, mapping := range profile.Mappings {
		go tr.runTrafficProfileSchedule(ctx, testID, profile.RuntimeTarget, mapping, preBaseline, mge)
	}
}

func (tr *TestRunner) runTrafficProfileSchedule(ctx context.Context, testID, runtimeTarget string, mapping TrafficProfileMapping, activeOffset time.Duration, mge *engine.MultiGroupEngine) {
	if !tr.waitTrafficProfileScalingReady(ctx, testID, mapping.GroupName, mge) {
		return
	}
	// If activation/readiness attaches after the coordinated start, jump
	// directly to the active point. Replaying every historical boundary would
	// cause thousands of needless persistent scale writes for a daily profile.
	elapsed := tr.trafficProfileElapsed(testID, activeOffset, time.Now().UTC())
	startIndex := trafficProfileStartIndex(mapping.Points, elapsed)
	for index := startIndex; index < len(mapping.Points); index++ {
		point := mapping.Points[index]
		pointOffset := time.Duration(point.OffsetMS) * time.Millisecond
		if pointOffset > time.Duration(math.MaxInt64)-activeOffset {
			return
		}
		if !tr.waitTestActiveRuntime(ctx, testID, activeOffset+pointOffset) {
			return
		}
		if !tr.waitTestRunning(ctx, testID) {
			return
		}
		var err error
		if runtimeTarget == "vusers" {
			err = tr.scaleVUsersNow(testID, mapping.GroupName, int(math.Round(point.Target)), true)
		} else {
			err = tr.applyTrafficProfileTPSTarget(testID, mapping.GroupName, point.Target)
		}
		if err != nil {
			slog.Warn("traffic profile schedule stopped", "test_id", testID, "group", mapping.GroupName, "point", index, "target", point.Target, "error", err)
			return
		}
		if runtimeTarget == "vusers" {
			tr.broadcastTrafficProfileVUserTarget(testID, mapping.GroupName, int(math.Round(point.Target)))
		}
	}
}

func (tr *TestRunner) broadcastTrafficProfileVUserTarget(testID, groupName string, target int) {
	tr.mu.RLock()
	test := tr.tests[testID]
	projectID, testName := "", ""
	if test != nil {
		projectID = normalizeProjectID(test.ProjectID)
		testName = test.Name
	}
	tr.mu.RUnlock()
	if test == nil {
		return
	}
	tr.hub.BroadcastJSON(map[string]any{
		"type":          "traffic_profile_target_changed",
		"target_kind":   "vusers",
		"test_id":       testID,
		"project_id":    projectID,
		"test_name":     testName,
		"group":         groupName,
		"target_vusers": target,
		"timestamp":     time.Now().UTC(),
	})
}

func trafficProfileStartIndex(points []trafficprofile.RuntimePoint, elapsed time.Duration) int {
	if len(points) == 0 || elapsed <= 0 {
		return 0
	}
	index := sort.Search(len(points), func(index int) bool {
		return time.Duration(points[index].OffsetMS)*time.Millisecond > elapsed
	}) - 1
	return max(0, index)
}

func (tr *TestRunner) trafficProfileElapsed(testID string, activeOffset time.Duration, now time.Time) time.Duration {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	test := tr.tests[testID]
	if test == nil || test.StartedAt == nil {
		return -activeOffset
	}
	return testActiveRuntimeLocked(test, now) - activeOffset
}

func (tr *TestRunner) waitTrafficProfileScalingReady(ctx context.Context, testID, groupName string, mge *engine.MultiGroupEngine) bool {
	if mge == nil {
		tr.mu.RLock()
		resumed := tr.tests[testID] != nil && tr.tests[testID].stageScheduleResume
		tr.mu.RUnlock()
		if resumed {
			// Adopted after HA failover: generators are already running the
			// profile's current load, and the schedule jumps to the active point.
			return true
		}
		// Distributed generators signal initial-ramp admission through the
		// scale primitive. Probe the configured target so a slow generator
		// startup cannot make the profile scheduler abandon its first point.
		tr.mu.RLock()
		test := tr.tests[testID]
		currentVUsers, found := testGroupVUsers(test, groupName)
		_, distributed := tr.resolveBackend(test).(initialRampReadinessProbeExecutionBackend)
		tr.mu.RUnlock()
		if test == nil || !found || !distributed {
			return test != nil
		}
		for {
			err := tr.scaleVUsersNow(testID, groupName, currentVUsers, false)
			if err == nil {
				return true
			}
			if errors.Is(err, engine.ErrInitialRampInProgress) {
				if !waitContextDuration(ctx, initialRampScaleRetryInterval) {
					return false
				}
				continue
			}
			if tr.testExecutionStatus(testID) == "PAUSED" && tr.waitTestRunning(ctx, testID) {
				continue
			}
			slog.Warn("traffic profile readiness probe failed", "test_id", testID, "group", groupName, "error", err)
			return false
		}
	}
	select {
	case <-ctx.Done():
		return false
	case <-mge.Ready():
	}
	ticker := time.NewTicker(executionClockPollInterval)
	defer ticker.Stop()
	for !mge.ScalingReady() {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
	return tr.testExecutionStatus(testID) == "RUNNING" || tr.waitTestRunning(ctx, testID)
}

func (tr *TestRunner) applyTrafficProfileTPSTarget(testID, groupName string, targetTPS float64) error {
	if _, err := tr.updateGoalTargetTPS(testID, groupName, targetTPS, true); err != nil {
		return err
	}
	tr.mu.RLock()
	test := tr.tests[testID]
	currentVUsers, found := testGroupVUsers(test, groupName)
	tr.mu.RUnlock()
	if !found {
		return fmt.Errorf("group not found: %s", groupName)
	}
	if targetTPS == 0 {
		if currentVUsers == 0 {
			return nil
		}
		return tr.scaleVUsersNow(testID, groupName, 0, true)
	}
	if currentVUsers == 0 {
		return tr.scaleVUsersNow(testID, groupName, 1, true)
	}
	return nil
}
