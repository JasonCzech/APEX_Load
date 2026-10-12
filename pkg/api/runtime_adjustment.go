package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/api/testexec"
)

type RuntimeAdjustmentGroupRequest struct {
	Name           string   `json:"name"`
	TargetDuration string   `json:"target_duration,omitempty"`
	TargetVUsers   *int     `json:"target_vusers,omitempty"`
	TargetTPS      *float64 `json:"target_tps,omitempty"`
}

type RuntimeAdjustmentRequest struct {
	Groups           []RuntimeAdjustmentGroupRequest `json:"groups"`
	Transition       string                          `json:"transition,omitempty"`
	RampDuration     string                          `json:"ramp_duration,omitempty"`
	ExpectedRevision string                          `json:"expected_revision,omitempty"`
}

type RuntimeAdjustmentGroupPreview struct {
	Name             string   `json:"name"`
	CurrentDuration  string   `json:"current_duration"`
	TargetDuration   string   `json:"target_duration"`
	CurrentVUsers    int      `json:"current_vusers"`
	TargetVUsers     int      `json:"target_vusers"`
	CurrentTargetTPS *float64 `json:"current_target_tps,omitempty"`
	TargetTPS        *float64 `json:"target_tps,omitempty"`
	EffectiveEndAt   string   `json:"effective_end_at,omitempty"`
	RuntimeState     string   `json:"runtime_state"`
}

type RuntimeAdjustmentImpact struct {
	ScenarioDuration      string `json:"scenario_duration"`
	ScenarioEndAt         string `json:"scenario_end_at,omitempty"`
	PeakVUsers            int    `json:"peak_vusers"`
	RecommendedGenerators int    `json:"recommended_generators"`
}

type RuntimeAdjustmentPreview struct {
	TestID       string                          `json:"test_id"`
	Decision     string                          `json:"decision"`
	Revision     string                          `json:"revision"`
	Groups       []RuntimeAdjustmentGroupPreview `json:"groups"`
	Impact       RuntimeAdjustmentImpact         `json:"impact"`
	Conflicts    []LaunchConflict                `json:"conflicts,omitempty"`
	SafeProposal *RuntimeAdjustmentRequest       `json:"safe_proposal,omitempty"`
}

func effectiveGroupDuration(group TestGroup, fallback time.Duration) time.Duration {
	if parsed, err := time.ParseDuration(strings.TrimSpace(group.TargetDuration)); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}

func runtimeAdjustmentRevision(test *ManagedTest) string {
	if test == nil {
		return ""
	}
	type groupRevision struct {
		Name, Duration, State string
		VUsers                int
		TargetTPS             float64
	}
	groups := make([]groupRevision, 0, len(test.Groups))
	for _, group := range test.Groups {
		targetTPS := 0.0
		if status, ok := test.GoalTargets[group.Name]; ok {
			targetTPS = status.TargetTPS
		}
		groups = append(groups, groupRevision{group.Name, effectiveGroupDuration(group, test.Duration).String(), group.RuntimeState, groupCurrentVUsers(group), targetTPS})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	payload, _ := json.Marshal(struct {
		ID, Status      string
		Duration        int64
		CumulativePause int64
		StartedAt       *time.Time
		Groups          []groupRevision
	}{test.ID, test.Status, int64(test.Duration), int64(test.CumulativePause), cloneTimePtr(test.StartedAt), groups})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:12])
}

func applyRuntimeAdjustmentRequest(test *ManagedTest, req RuntimeAdjustmentRequest) (*ManagedTest, []RuntimeAdjustmentGroupPreview, error) {
	if test == nil {
		return nil, nil, fmt.Errorf("test is required")
	}
	proposal := *test
	proposal.Groups = append([]TestGroup(nil), test.Groups...)
	byName := make(map[string]RuntimeAdjustmentGroupRequest, len(req.Groups))
	for _, change := range req.Groups {
		name := strings.TrimSpace(change.Name)
		if name == "" {
			return nil, nil, fmt.Errorf("group name is required")
		}
		if _, duplicate := byName[name]; duplicate {
			return nil, nil, fmt.Errorf("group %q is repeated", name)
		}
		byName[name] = change
	}
	if len(byName) == 0 {
		return nil, nil, fmt.Errorf("at least one group adjustment is required")
	}
	maxDuration := time.Duration(0)
	previews := make([]RuntimeAdjustmentGroupPreview, 0, len(proposal.Groups))
	for index := range proposal.Groups {
		group := &proposal.Groups[index]
		currentDuration := effectiveGroupDuration(test.Groups[index], test.Duration)
		targetDuration := currentDuration
		currentVUsers := groupCurrentVUsers(test.Groups[index])
		targetVUsers := currentVUsers
		currentTPS := 0.0
		if status, ok := test.GoalTargets[group.Name]; ok {
			currentTPS = status.TargetTPS
		}
		var targetTPS *float64
		if change, ok := byName[group.Name]; ok {
			if group.RuntimeState == "ended" {
				return nil, nil, fmt.Errorf("group %q has ended and cannot be adjusted", group.Name)
			}
			if group.RuntimeState == "stopping" {
				return nil, nil, fmt.Errorf("group %q is stopping and cannot be adjusted", group.Name)
			}
			if strings.TrimSpace(change.TargetDuration) != "" {
				parsed, err := time.ParseDuration(change.TargetDuration)
				if err != nil || parsed <= 0 {
					return nil, nil, fmt.Errorf("group %q target_duration must be a positive duration", group.Name)
				}
				targetDuration = parsed
			}
			if change.TargetVUsers != nil {
				if test.ScenarioMode == "goal_oriented" {
					return nil, nil, fmt.Errorf("goal-oriented group %q must adjust target_tps instead of VUsers", group.Name)
				}
				if *change.TargetVUsers < 0 || *change.TargetVUsers > maxManagedTestVUsers {
					return nil, nil, fmt.Errorf("group %q target_vusers is outside the supported range", group.Name)
				}
				if group.Role == GroupRoleObserver && *change.TargetVUsers > observerMaxVUsers {
					return nil, nil, fmt.Errorf("observer group %q cannot exceed %d VUsers", group.Name, observerMaxVUsers)
				}
				targetVUsers = *change.TargetVUsers
				if group.stageDriven {
					group.currentVUsers = targetVUsers
				} else {
					group.VUsers = targetVUsers
				}
			}
			if change.TargetTPS != nil {
				if test.ScenarioMode != "goal_oriented" || *change.TargetTPS <= 0 || math.IsNaN(*change.TargetTPS) || math.IsInf(*change.TargetTPS, 0) {
					return nil, nil, fmt.Errorf("group %q target_tps is invalid for this scenario", group.Name)
				}
				value := *change.TargetTPS
				targetTPS = &value
			}
		}
		group.TargetDuration = targetDuration.String()
		if group.RuntimeState == "" {
			group.RuntimeState = "running"
		}
		if targetDuration > maxDuration {
			maxDuration = targetDuration
		}
		previews = append(previews, RuntimeAdjustmentGroupPreview{
			Name: group.Name, CurrentDuration: currentDuration.String(), TargetDuration: targetDuration.String(),
			CurrentVUsers: currentVUsers, TargetVUsers: targetVUsers, TargetTPS: targetTPS, RuntimeState: group.RuntimeState,
		})
		if currentTPS > 0 {
			previews[len(previews)-1].CurrentTargetTPS = &currentTPS
		}
	}
	for name := range byName {
		found := false
		for _, group := range proposal.Groups {
			if group.Name == name {
				found = true
				break
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("group not found: %s", name)
		}
	}
	proposal.Duration = maxDuration
	proposal.DurationStr = maxDuration.String()
	proposal.TotalVUsers = capacityVUsersForGroups(proposal.Groups)
	return &proposal, previews, nil
}

func (tr *TestRunner) PreviewRuntimeAdjustment(id string, req RuntimeAdjustmentRequest) (*RuntimeAdjustmentPreview, error) {
	if !tr.isExecutionLeader() {
		return nil, errNotExecutionLeader
	}
	now := time.Now().UTC()
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	current := tr.tests[id]
	if current == nil {
		tr.mu.RUnlock()
		return nil, fmt.Errorf("test not found: %s", id)
	}
	if current.Status != "RUNNING" {
		status := current.Status
		tr.mu.RUnlock()
		return nil, fmt.Errorf("test is %s, must be RUNNING to adjust runtime", status)
	}
	projection, groups, err := applyRuntimeAdjustmentRequest(current, req)
	if err == nil {
		err = applyRuntimeAdjustmentRampProfile(projection, groups, req, now)
	}
	revision := runtimeAdjustmentRevision(current)
	currentSnapshot := *current
	currentSnapshot.Groups = append([]TestGroup(nil), current.Groups...)
	currentSnapshot.GoalTargets = cloneGoalTargetStatusMap(current.GoalTargets)
	timeslotID := ""
	if current.TimeslotID != nil {
		timeslotID = *current.TimeslotID
	}
	tr.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	start := calendarTestStart(projection)
	if start.IsZero() {
		start = now
	}
	window := launchWindowForTestAt(projection, start, now)
	evidence := tr.loadLaunchWindowEvidenceWithBoundTimeslotForProject(window, timeslotID, projection.ProjectID, testAssignedPoolID(projection))

	tr.mu.Lock()
	current = tr.tests[id]
	if current == nil || runtimeAdjustmentRevision(current) != revision {
		tr.mu.Unlock()
		return nil, fmt.Errorf("runtime state changed while the preview was calculated; retry")
	}
	conflicts := tr.runtimeAdjustmentConflictsLocked(projection, window, now, id, evidence)
	tr.mu.Unlock()

	activeOrigin := runtimeGroupActiveOrigin(&currentSnapshot, start)
	for i := range groups {
		duration, _ := time.ParseDuration(groups[i].TargetDuration)
		end := activeOrigin.Add(duration + currentSnapshot.CumulativePause)
		groups[i].EffectiveEndAt = end.UTC().Format(time.RFC3339)
	}
	decision := "allowed"
	if len(conflicts) > 0 {
		decision = "blocked"
	}
	preview := &RuntimeAdjustmentPreview{TestID: id, Decision: decision, Revision: revision, Groups: groups, Conflicts: conflicts}
	preview.Impact = RuntimeAdjustmentImpact{ScenarioDuration: projection.Duration.String(), ScenarioEndAt: window.EndAt.Format(time.RFC3339), PeakVUsers: maxInt(currentSnapshot.TotalVUsers, projection.TotalVUsers), RecommendedGenerators: recommendedGeneratorCountForTest(projection)}
	if len(conflicts) > 0 {
		preview.SafeProposal = tr.safeRuntimeAdjustmentProposal(id, &currentSnapshot, req, revision, start, now, evidence)
	}
	return preview, nil
}

func applyRuntimeAdjustmentRampProfile(projection *ManagedTest, groups []RuntimeAdjustmentGroupPreview, req RuntimeAdjustmentRequest, now time.Time) error {
	if projection == nil || !strings.EqualFold(req.Transition, "gradual") {
		return nil
	}
	rampDuration, err := time.ParseDuration(req.RampDuration)
	if err != nil || rampDuration <= 0 {
		return fmt.Errorf("ramp_duration must be positive for a gradual transition")
	}
	ramps := make(map[string]VUserRampStatus, len(projection.VUserRamps)+len(groups))
	for name, ramp := range projection.VUserRamps {
		ramps[name] = ramp
	}
	projection.VUserRamps = ramps
	for _, result := range groups {
		if result.TargetVUsers >= result.CurrentVUsers {
			continue
		}
		projection.VUserRamps[result.Name] = VUserRampStatus{
			Group: result.Name, StartVUsers: result.CurrentVUsers, TargetVUsers: result.TargetVUsers,
			StartedAt: now, RampDuration: rampDuration.String(), RampDurationSeconds: int64(rampDuration / time.Second),
		}
	}
	return nil
}

func (tr *TestRunner) runtimeAdjustmentConflictsLocked(proposal *ManagedTest, window LaunchWindow, now time.Time, id string, evidence launchWindowEvidence) []LaunchConflict {
	tr.refreshCapacityAdvisoryLocked(proposal)
	conflicts := tr.launchWindowConflictsWithEvidenceLocked(proposal, window, now, id, "", evidence)
	projectQuota, quotaOK := evidence.projectQuota.quotaFor(proposal)
	if !quotaOK {
		return append(conflicts, LaunchConflict{Code: "project_quota_evidence_unavailable", Severity: "blocking", Source: "project", Message: "Project quota evidence changed while the adjustment was evaluated; retry."})
	}
	if quotaErr := enforceTestQuotaWithProjectQuota(projectQuota, managedTestCapacityVUsers(proposal), proposal.Duration, proposal.Status); quotaErr != nil {
		return append(conflicts, LaunchConflict{Code: "project_quota", Severity: "blocking", Source: "project", Message: quotaErr.Error()})
	}
	return conflicts
}

// safeRuntimeAdjustmentProposal preserves requested reductions and searches the
// line of requested increases for the largest admissible point. Duration is
// rounded to seconds and VUsers to integers, so the result has the same
// precision as the public controls.
func (tr *TestRunner) safeRuntimeAdjustmentProposal(id string, current *ManagedTest, requested RuntimeAdjustmentRequest, revision string, start, now time.Time, evidence launchWindowEvidence) *RuntimeAdjustmentRequest {
	if current == nil {
		return nil
	}
	build := func(factor float64) RuntimeAdjustmentRequest {
		result := RuntimeAdjustmentRequest{Transition: requested.Transition, RampDuration: requested.RampDuration, ExpectedRevision: revision}
		for _, change := range requested.Groups {
			candidate := change
			var currentGroup *TestGroup
			for index := range current.Groups {
				if current.Groups[index].Name == change.Name {
					currentGroup = &current.Groups[index]
					break
				}
			}
			if currentGroup == nil {
				continue
			}
			fromDuration := effectiveGroupDuration(*currentGroup, current.Duration)
			if toDuration, err := time.ParseDuration(change.TargetDuration); err == nil && toDuration > fromDuration {
				candidate.TargetDuration = (fromDuration + time.Duration(float64(toDuration-fromDuration)*factor)).Round(time.Second).String()
			}
			fromVUsers := groupCurrentVUsers(*currentGroup)
			if change.TargetVUsers != nil && *change.TargetVUsers > fromVUsers {
				value := fromVUsers + int(math.Floor(float64(*change.TargetVUsers-fromVUsers)*factor))
				candidate.TargetVUsers = &value
			}
			if change.TargetTPS != nil {
				fromTPS := 0.0
				if target, ok := current.GoalTargets[change.Name]; ok {
					fromTPS = target.TargetTPS
				}
				if *change.TargetTPS > fromTPS {
					value := fromTPS + (*change.TargetTPS-fromTPS)*factor
					candidate.TargetTPS = &value
				}
			}
			result.Groups = append(result.Groups, candidate)
		}
		return result
	}
	allowed := func(candidate RuntimeAdjustmentRequest) bool {
		proposal, groups, err := applyRuntimeAdjustmentRequest(current, candidate)
		if err != nil {
			return false
		}
		if err := applyRuntimeAdjustmentRampProfile(proposal, groups, candidate, now); err != nil {
			return false
		}
		window := launchWindowForTestAt(proposal, start, now)
		tr.mu.Lock()
		defer tr.mu.Unlock()
		live := tr.tests[id]
		return live != nil && runtimeAdjustmentRevision(live) == revision && len(tr.runtimeAdjustmentConflictsLocked(proposal, window, now, id, evidence)) == 0
	}
	base := build(0)
	if !allowed(base) {
		return nil
	}
	low, high := 0.0, 1.0
	for range 24 {
		mid := (low + high) / 2
		if allowed(build(mid)) {
			low = mid
		} else {
			high = mid
		}
	}
	returnValue := build(low)
	if low <= 0 && len(returnValue.Groups) == 0 {
		return nil
	}
	return &returnValue
}

func (tr *TestRunner) ApplyRuntimeAdjustment(id string, req RuntimeAdjustmentRequest) (*RuntimeAdjustmentPreview, error) {
	// Serialize the composite control so two callers cannot both apply from the
	// same preview revision between independently fenced legacy control calls.
	tr.runtimeAdjustmentMu.Lock()
	defer tr.runtimeAdjustmentMu.Unlock()
	preview, err := tr.PreviewRuntimeAdjustment(id, req)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.ExpectedRevision) == "" || req.ExpectedRevision != preview.Revision {
		return preview, fmt.Errorf("runtime adjustment preview is stale")
	}
	if preview.Decision != "allowed" {
		return preview, fmt.Errorf("runtime adjustment conflicts with scheduled capacity")
	}

	tr.mu.RLock()
	test := tr.tests[preview.TestID]
	if test == nil || test.Status != "RUNNING" || runtimeAdjustmentRevision(test) != preview.Revision {
		tr.mu.RUnlock()
		return preview, fmt.Errorf("runtime adjustment preview is stale")
	}
	oldDuration := test.Duration
	oldGroups := cloneArchiveTestGroups(test.Groups)
	oldGoalConfig := cloneGoalConfig(test.GoalConfig)
	oldGoalTargets := cloneGoalTargetStatusMap(test.GoalTargets)
	oldTargets := make(map[string]int, len(test.Groups))
	for _, group := range test.Groups {
		oldTargets[group.Name] = groupCurrentVUsers(group)
	}
	tr.mu.RUnlock()
	newDuration, _ := time.ParseDuration(preview.Impact.ScenarioDuration)
	if newDuration != oldDuration {
		if err := tr.AdjustDuration(preview.TestID, newDuration); err != nil {
			return nil, err
		}
	}
	applied := make([]string, 0)
	for _, change := range req.Groups {
		if change.TargetTPS != nil {
			if _, err := tr.UpdateGoalTargetTPS(preview.TestID, change.Name, *change.TargetTPS); err != nil {
				return nil, tr.rollbackRuntimeAdjustment(preview.TestID, oldDuration, oldGroups, oldGoalConfig, oldGoalTargets, oldTargets, applied, err)
			}
			continue
		}
		if change.TargetVUsers == nil || *change.TargetVUsers == oldTargets[change.Name] {
			continue
		}
		options := ScaleVUsersOptions{}
		if strings.EqualFold(req.Transition, "gradual") {
			options.RampDuration, err = time.ParseDuration(req.RampDuration)
			if err != nil || options.RampDuration <= 0 {
				return nil, fmt.Errorf("ramp_duration must be positive for a gradual transition")
			}
		}
		if err := tr.ScaleVUsersWithOptions(preview.TestID, change.Name, *change.TargetVUsers, options); err != nil {
			return nil, tr.rollbackRuntimeAdjustment(preview.TestID, oldDuration, oldGroups, oldGoalConfig, oldGoalTargets, oldTargets, applied, err)
		}
		applied = append(applied, change.Name)
	}

	tr.mu.Lock()
	test = tr.tests[preview.TestID]
	if test == nil || test.Status != "RUNNING" {
		tr.mu.Unlock()
		return nil, tr.rollbackRuntimeAdjustment(preview.TestID, oldDuration, oldGroups, oldGoalConfig, oldGoalTargets, oldTargets, applied, fmt.Errorf("test lifecycle changed during runtime adjustment"))
	}
	start := calendarTestStart(test)
	for _, result := range preview.Groups {
		for index := range test.Groups {
			if test.Groups[index].Name != result.Name {
				continue
			}
			test.Groups[index].TargetDuration = result.TargetDuration
			if test.Groups[index].RuntimeState == "" {
				test.Groups[index].RuntimeState = "running"
			}
			if result.EffectiveEndAt != "" {
				if value, parseErr := time.Parse(time.RFC3339, result.EffectiveEndAt); parseErr == nil {
					test.Groups[index].EffectiveEndAt = &value
				}
			}
		}
	}
	window := launchWindowForTestAt(test, start, time.Now().UTC())
	windowEnd := window.EndAt.UTC()
	test.CapacityWindowEndAt = &windowEnd
	if saveErr := tr.saveManagedTestRecord(test); saveErr != nil {
		tr.mu.Unlock()
		return nil, tr.rollbackRuntimeAdjustment(preview.TestID, oldDuration, oldGroups, oldGoalConfig, oldGoalTargets, oldTargets, applied, saveErr)
	}
	tr.mu.Unlock()
	for _, result := range preview.Groups {
		duration, _ := time.ParseDuration(result.TargetDuration)
		tr.scheduleRuntimeGroupDeadline(preview.TestID, result.Name, start, duration)
	}
	return tr.PreviewRuntimeAdjustment(preview.TestID, req)
}

func (tr *TestRunner) rollbackRuntimeAdjustment(id string, duration time.Duration, groups []TestGroup, goalConfig *GoalConfig, goalTargets map[string]GoalTargetStatus, targets map[string]int, applied []string, cause error) error {
	var compensationErr error
	for index := len(applied) - 1; index >= 0; index-- {
		if err := tr.ScaleVUsers(id, applied[index], targets[applied[index]]); err != nil {
			compensationErr = errors.Join(compensationErr, fmt.Errorf("restore group %q VUsers: %w", applied[index], err))
		}
	}
	tr.mu.RLock()
	current := tr.tests[id]
	currentDuration := time.Duration(0)
	if current != nil {
		currentDuration = current.Duration
	}
	tr.mu.RUnlock()
	if currentDuration != duration {
		if err := tr.AdjustDuration(id, duration); err != nil {
			compensationErr = errors.Join(compensationErr, fmt.Errorf("restore duration: %w", err))
		}
	}
	tr.mu.Lock()
	if test := tr.tests[id]; test != nil {
		test.Groups = cloneArchiveTestGroups(groups)
		test.GoalConfig = cloneGoalConfig(goalConfig)
		test.GoalTargets = cloneGoalTargetStatusMap(goalTargets)
		test.TotalVUsers = capacityVUsersForGroups(test.Groups)
		if err := tr.saveManagedTestRecord(test); err != nil {
			compensationErr = errors.Join(compensationErr, fmt.Errorf("persist restored runtime targets: %w", err))
		}
	}
	tr.mu.Unlock()
	tr.syncGoalThroughputCaps(id)
	if compensationErr != nil {
		return errors.Join(cause, fmt.Errorf("runtime adjustment compensation incomplete: %w", compensationErr))
	}
	return cause
}

func (tr *TestRunner) scheduleRuntimeGroupDeadline(testID, groupName string, start time.Time, target time.Duration) {
	go func() {
		for {
			tr.mu.RLock()
			test := tr.tests[testID]
			if test == nil || isTerminalTestStatus(test.Status) || test.Status == "STOPPING" {
				tr.mu.RUnlock()
				return
			}
			var group *TestGroup
			for index := range test.Groups {
				if test.Groups[index].Name == groupName {
					copy := test.Groups[index]
					group = &copy
					break
				}
			}
			pause := test.CumulativePause
			fallbackDuration := test.Duration
			activeOrigin := runtimeGroupActiveOrigin(test, start)
			status := test.Status
			if test.PausedAt != nil {
				pause += time.Since(*test.PausedAt)
			}
			tr.mu.RUnlock()
			if group == nil || effectiveGroupDuration(*group, fallbackDuration) != target || group.RuntimeState == "ended" {
				return
			}
			// The engine owns the final scenario deadline and its configured
			// ramp-down. A second controller timer at the same instant races that
			// teardown and can incorrectly restore the group to running.
			if target >= fallbackDuration {
				return
			}
			remaining := activeOrigin.Add(target + pause).Sub(time.Now())
			if remaining > 0 || status != "RUNNING" {
				delay := min(remaining, time.Second)
				if delay <= 0 {
					delay = time.Second
				}
				timer := time.NewTimer(delay)
				<-timer.C
				continue
			}
			tr.mu.Lock()
			test = tr.tests[testID]
			if test == nil || test.Status != "RUNNING" {
				tr.mu.Unlock()
				return
			}
			currentVUsers := 0
			rampDown := time.Duration(0)
			claimed := false
			for index := range test.Groups {
				if test.Groups[index].Name == groupName && effectiveGroupDuration(test.Groups[index], test.Duration) == target && test.Groups[index].RuntimeState != "ended" && test.Groups[index].RuntimeState != "stopping" {
					currentVUsers = groupCurrentVUsers(test.Groups[index])
					rampDown, _ = time.ParseDuration(test.Groups[index].RampDown)
					test.Groups[index].RuntimeState = "stopping"
					claimed = tr.saveManagedTestRecord(test) == nil
					if !claimed {
						test.Groups[index].RuntimeState = "running"
					}
					break
				}
			}
			tr.mu.Unlock()
			if !claimed {
				timer := time.NewTimer(time.Second)
				<-timer.C
				continue
			}

			var scaleErr error
			if rampDown > 0 && currentVUsers > 0 {
				var ramp *vuserScaleRamp
				ramp, scaleErr = tr.startVUserScaleRamp(context.Background(), testID, groupName, 0, rampDown)
				if scaleErr == nil && ramp != nil {
					scaleErr = <-ramp.done
				}
			} else {
				scaleErr = tr.ScaleVUsers(testID, groupName, 0)
			}

			tr.mu.Lock()
			test = tr.tests[testID]
			failClosed := false
			if test != nil {
				for index := range test.Groups {
					if test.Groups[index].Name != groupName || effectiveGroupDuration(test.Groups[index], test.Duration) != target || test.Groups[index].RuntimeState != "stopping" {
						continue
					}
					if scaleErr == nil {
						now := time.Now().UTC()
						test.Groups[index].RuntimeState = "ended"
						test.Groups[index].EndedAt = &now
					} else {
						test.Groups[index].RuntimeState = "running"
						failClosed = test.Status == "RUNNING"
					}
					_ = tr.saveManagedTestRecord(test)
					break
				}
			}
			tr.mu.Unlock()
			if failClosed {
				tr.requestStopIfRunning(test, "group deadline could not be enforced; stopping scenario fail closed")
			}
			return
		}
	}()
}

func runtimeGroupActiveOrigin(test *ManagedTest, start time.Time) time.Time {
	if test == nil {
		return start
	}
	preBaseline, _ := testexec.ResourceBaselineWindows(test.ResourceBaseline)
	return start.Add(testexec.StartDelayWindow(test.StartDelay) + preBaseline)
}

// initializeRuntimeGroupsLocked establishes the durable active-clock deadline
// for every group at the RUNNING transition. Legacy groups inherit the scenario
// duration, preserving the behavior of snapshots created before group deadlines.
func initializeRuntimeGroupsLocked(test *ManagedTest, start time.Time) {
	if test == nil {
		return
	}
	for index := range test.Groups {
		group := &test.Groups[index]
		duration := effectiveGroupDuration(*group, test.Duration)
		group.TargetDuration = duration.String()
		if group.RuntimeState != "ended" {
			group.RuntimeState = "running"
			group.EndedAt = nil
		}
	}
	refreshRuntimeGroupEffectiveEndsLocked(test, start)
}

func refreshRuntimeGroupEffectiveEndsLocked(test *ManagedTest, start time.Time) {
	if test == nil || start.IsZero() {
		return
	}
	origin := runtimeGroupActiveOrigin(test, start)
	for index := range test.Groups {
		if test.Groups[index].RuntimeState == "ended" {
			continue
		}
		end := origin.Add(effectiveGroupDuration(test.Groups[index], test.Duration) + test.CumulativePause).UTC()
		test.Groups[index].EffectiveEndAt = &end
	}
}

func (tr *TestRunner) scheduleRuntimeGroupDeadlines(test *ManagedTest) {
	if test == nil {
		return
	}
	start := calendarTestStart(test)
	if start.IsZero() {
		return
	}
	for _, group := range test.Groups {
		if group.RuntimeState == "ended" || group.RuntimeState == "stopping" {
			continue
		}
		tr.scheduleRuntimeGroupDeadline(test.ID, group.Name, start, effectiveGroupDuration(group, test.Duration))
	}
}
