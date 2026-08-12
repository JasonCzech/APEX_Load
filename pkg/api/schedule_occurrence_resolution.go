package api

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

var (
	errScheduleOccurrenceNotResolvable     = errors.New("schedule occurrence is not awaiting an operator decision")
	errScheduleOccurrenceResolutionInvalid = errors.New("invalid schedule occurrence resolution")
)

type ScheduleOccurrenceResolutionRequest struct {
	Action           string `json:"action"`
	Reason           string `json:"reason,omitempty"`
	ExpectedRevision *int64 `json:"expected_revision,omitempty"`
}

func normalizeScheduleOccurrenceResolution(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "retry":
		return "retry", nil
	case "catch_up", "catch-up", "catchup":
		return "catch_up", nil
	case "skip":
		return "skip", nil
	default:
		return "", fmt.Errorf("%w: action must be retry, catch_up, or skip", errScheduleOccurrenceResolutionInvalid)
	}
}

type atomicScenarioOccurrenceResolutionStore interface {
	ResolveScenarioScheduleOccurrence(occurrence *storage.ScenarioScheduleOccurrence, expectedOccurrenceRevision int64, schedule *storage.ScenarioSchedule, expectedScheduleRevision int) (bool, error)
	ResolveScenarioScheduleOccurrenceIfLeaseTermHeld(occurrence *storage.ScenarioScheduleOccurrence, expectedOccurrenceRevision int64, schedule *storage.ScenarioSchedule, expectedScheduleRevision int, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
}

type atomicExecutionPlanOccurrenceResolutionStore interface {
	ResolveExecutionPlanScheduleOccurrence(occurrence *storage.ExecutionPlanScheduleOccurrence, expectedOccurrenceRevision int64, schedule *storage.ExecutionPlanSchedule, expectedScheduleRevision int) (bool, error)
	ResolveExecutionPlanScheduleOccurrenceIfLeaseTermHeld(occurrence *storage.ExecutionPlanScheduleOccurrence, expectedOccurrenceRevision int64, schedule *storage.ExecutionPlanSchedule, expectedScheduleRevision int, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
}

func (tr *TestRunner) ResolveScenarioScheduleOccurrence(scenarioID, scheduleID, occurrenceID string, req ScheduleOccurrenceResolutionRequest) (*storage.ScenarioScheduleOccurrence, *storage.ScenarioSchedule, bool, error) {
	action, err := normalizeScheduleOccurrenceResolution(req.Action)
	if err != nil {
		return nil, nil, false, err
	}
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return nil, nil, false, err
	}
	tr.refreshScenariosForHARead(true)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	scenario := tr.scenarios[scenarioID]
	if scenario == nil {
		return nil, nil, false, nil
	}
	var scheduleIndex = -1
	var schedule *storage.ScenarioSchedule
	for index, candidate := range scenario.Schedules {
		if candidate != nil && candidate.ID == scheduleID {
			scheduleIndex = index
			schedule = cloneScenarioSchedule(candidate)
			break
		}
	}
	if schedule == nil || tr.scenarioStore == nil {
		return nil, nil, false, nil
	}
	occurrence, err := tr.scenarioStore.GetScenarioScheduleOccurrence(occurrenceID)
	if err != nil {
		return nil, nil, false, err
	}
	if occurrence == nil || occurrence.ScheduleID != scheduleID || occurrence.ScenarioID != scenarioID {
		return nil, nil, false, nil
	}
	status := strings.ToLower(strings.TrimSpace(occurrence.Status))
	if status != "blocked" && status != "failed" {
		return occurrence, schedule, true, errScheduleOccurrenceNotResolvable
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(occurrence.ReasonCode)), "approval_") {
		return occurrence, schedule, true, fmt.Errorf("%w: approval-gated occurrences must be resolved through the schedule approval workflow", errScheduleOccurrenceNotResolvable)
	}
	expectedOccurrenceRevision := occurrence.Revision
	if expectedOccurrenceRevision < 1 {
		expectedOccurrenceRevision = 1
	}
	if req.ExpectedRevision != nil && *req.ExpectedRevision != expectedOccurrenceRevision {
		return occurrence, schedule, true, fmt.Errorf("%w: expected occurrence revision %d, current revision %d", errScheduleConcurrentChange, *req.ExpectedRevision, expectedOccurrenceRevision)
	}
	now := time.Now().UTC()
	expectedScheduleRevision := schedule.Revision
	if action == "skip" {
		occurrence.Status = "skipped"
		occurrence.ReasonCode = "operator_skipped"
		occurrence.ConflictSummary = strings.TrimSpace(req.Reason)
		if executionPlanSchedulePausedIndefinitely(schedule.DisabledFrom, schedule.DisabledUntil, now) {
			schedule.NextRunAt = nil
			schedule.NextRequiredAction = "schedule_paused"
		} else {
			next, nextErr := nextScenarioScheduleRun(schedule, now)
			if nextErr != nil {
				return occurrence, schedule, true, nextErr
			}
			schedule.NextRunAt = next
			schedule.NextRequiredAction = ""
		}
	} else {
		if !schedule.Enabled {
			return occurrence, schedule, true, fmt.Errorf("%w: enable the schedule before resolving the occurrence with %s", errScheduleOccurrenceResolutionInvalid, action)
		}
		occurrence.Status = "materializing"
		occurrence.ReasonCode = "operator_" + action
		occurrence.ConflictSummary = ""
		occurrence.MaterializedAt = nil
		// The tests table owns the occurrence FK. Leave correlation empty until
		// the sweep persists the deterministic test row; recovery derives the ID
		// from occurrence.ID across the intervening crash window.
		occurrence.TestID = ""
		plannedAt := occurrence.PlannedAt.UTC()
		schedule.NextRunAt = &plannedAt
		schedule.NextRequiredAction = ""
	}
	occurrence.UpdatedAt = now
	schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrence)
	schedule.ConflictSummary = ""
	schedule.LastError = ""
	schedule.UpdatedAt = now
	store, ok := tr.scenarioStore.(atomicScenarioOccurrenceResolutionStore)
	if !ok {
		return occurrence, schedule, true, fmt.Errorf("scenario schedule storage does not support atomic occurrence resolution")
	}
	var changed bool
	if tr.durableExecutionFence {
		changed, err = store.ResolveScenarioScheduleOccurrenceIfLeaseTermHeld(occurrence, expectedOccurrenceRevision, schedule, expectedScheduleRevision, executionLeaseName, leaseTerm.ownerID, leaseTerm.generation)
	} else {
		changed, err = store.ResolveScenarioScheduleOccurrence(occurrence, expectedOccurrenceRevision, schedule, expectedScheduleRevision)
	}
	if err != nil {
		return occurrence, schedule, true, err
	}
	if !changed {
		if !tr.executionLeaseTermCurrent(leaseTerm) {
			return occurrence, schedule, true, errNotExecutionLeader
		}
		return occurrence, schedule, true, errScheduleConcurrentChange
	}
	previous := scenario.Schedules[scheduleIndex]
	scenario.Schedules[scheduleIndex] = cloneScenarioSchedule(schedule)
	tr.contentRevision++
	tr.adjustSchedulerReadinessLocked(previous, schedule)
	return cloneScenarioScheduleOccurrence(occurrence), cloneScenarioSchedule(schedule), true, nil
}

func (tr *TestRunner) ResolveExecutionPlanScheduleOccurrence(planID, scheduleID, occurrenceID string, req ScheduleOccurrenceResolutionRequest) (*storage.ExecutionPlanScheduleOccurrence, *storage.ExecutionPlanSchedule, bool, error) {
	action, err := normalizeScheduleOccurrenceResolution(req.Action)
	if err != nil {
		return nil, nil, false, err
	}
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return nil, nil, false, err
	}
	store := tr.execPlanScheduleStore
	if store == nil {
		return nil, nil, false, fmt.Errorf("execution-plan schedule storage is not configured")
	}
	schedule, err := store.GetExecutionPlanSchedule(scheduleID)
	if err != nil {
		return nil, nil, false, err
	}
	if schedule == nil || schedule.PlanID != planID {
		return nil, nil, false, nil
	}
	occurrences, err := store.ListExecutionPlanScheduleOccurrences(scheduleID)
	if err != nil {
		return nil, nil, false, err
	}
	var occurrence *storage.ExecutionPlanScheduleOccurrence
	for _, candidate := range occurrences {
		if candidate != nil && candidate.ID == occurrenceID {
			copy := *candidate
			occurrence = &copy
			break
		}
	}
	if occurrence == nil {
		return nil, nil, false, nil
	}
	status := strings.ToLower(strings.TrimSpace(occurrence.Status))
	if status != "blocked" && status != "failed" {
		return occurrence, schedule, true, errScheduleOccurrenceNotResolvable
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(occurrence.ReasonCode)), "approval_") {
		return occurrence, schedule, true, fmt.Errorf("%w: approval-gated occurrences must be resolved through the execution-plan approval workflow", errScheduleOccurrenceNotResolvable)
	}
	expectedOccurrenceRevision := occurrence.Revision
	if expectedOccurrenceRevision < 1 {
		expectedOccurrenceRevision = 1
	}
	if req.ExpectedRevision != nil && *req.ExpectedRevision != expectedOccurrenceRevision {
		return occurrence, schedule, true, fmt.Errorf("%w: expected occurrence revision %d, current revision %d", errScheduleConcurrentChange, *req.ExpectedRevision, expectedOccurrenceRevision)
	}
	now := time.Now().UTC()
	expectedScheduleRevision := schedule.Revision
	if action == "skip" {
		occurrence.Status = "skipped"
		occurrence.ReasonCode = "operator_skipped"
		occurrence.ConflictSummary = strings.TrimSpace(req.Reason)
		if executionPlanSchedulePausedIndefinitely(schedule.DisabledFrom, schedule.DisabledUntil, now) {
			schedule.NextRunAt = nil
			schedule.NextRequiredAction = "schedule_paused"
		} else {
			next, nextErr := nextCronRun(schedule.CronExpr, schedule.Timezone, now, executionPlanScheduleAllows(schedule.DisabledFrom, schedule.DisabledUntil))
			if nextErr != nil {
				return occurrence, schedule, true, nextErr
			}
			schedule.NextRunAt = next
			schedule.NextRequiredAction = ""
		}
	} else {
		if !schedule.Enabled {
			return occurrence, schedule, true, fmt.Errorf("%w: enable the schedule before resolving the occurrence with %s", errScheduleOccurrenceResolutionInvalid, action)
		}
		occurrence.Status = "materializing"
		occurrence.ReasonCode = "operator_" + action
		occurrence.ConflictSummary = ""
		occurrence.MaterializedAt = nil
		occurrence.RunID = "exec-plan-run-" + uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("apexload:execution-plan-schedule-resolution:%s:%d", occurrence.ID, expectedOccurrenceRevision))).String()
		plannedAt := occurrence.PlannedAt.UTC()
		schedule.NextRunAt = &plannedAt
		schedule.NextRequiredAction = ""
	}
	occurrence.UpdatedAt = now
	schedule.ConflictSummary = ""
	schedule.LastError = ""
	schedule.UpdatedAt = now
	atomicStore, ok := store.(atomicExecutionPlanOccurrenceResolutionStore)
	if !ok {
		return occurrence, schedule, true, fmt.Errorf("execution-plan schedule storage does not support atomic occurrence resolution")
	}
	var changed bool
	if tr.durableExecutionFence {
		changed, err = atomicStore.ResolveExecutionPlanScheduleOccurrenceIfLeaseTermHeld(occurrence, expectedOccurrenceRevision, schedule, expectedScheduleRevision, executionLeaseName, leaseTerm.ownerID, leaseTerm.generation)
	} else {
		changed, err = atomicStore.ResolveExecutionPlanScheduleOccurrence(occurrence, expectedOccurrenceRevision, schedule, expectedScheduleRevision)
	}
	if err != nil {
		return occurrence, schedule, true, err
	}
	if !changed {
		if !tr.executionLeaseTermCurrent(leaseTerm) {
			return occurrence, schedule, true, errNotExecutionLeader
		}
		return occurrence, schedule, true, errScheduleConcurrentChange
	}
	return occurrence, schedule, true, nil
}
