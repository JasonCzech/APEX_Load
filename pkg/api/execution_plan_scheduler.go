package api

// Recurring execution-plan schedules. A sibling of the scenario scheduler: it
// reuses the shared cron evaluator (nextCronRun) and date-range pause, but
// materializes an ExecutionPlanRun (via MaterializeAndRunExecutionPlan) instead
// of a single test. The sweep runs under the same execution-leader lease as the
// scenario scheduler and OUTSIDE tr.mu (materialize locks internally).

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

// CreateExecutionPlanScheduleRequest is the create/update payload for a recurring
// execution-plan schedule.
type CreateExecutionPlanScheduleRequest struct {
	Name            string     `json:"name"`
	CronExpr        string     `json:"cron_expr"`
	Timezone        string     `json:"timezone,omitempty"`
	RunNameTemplate string     `json:"run_name_template,omitempty"`
	Enabled         *bool      `json:"enabled,omitempty"`
	MissedRunPolicy string     `json:"missed_run_policy,omitempty"`
	DisabledFrom    *time.Time `json:"disabled_from,omitempty"`
	DisabledUntil   *time.Time `json:"disabled_until,omitempty"`
}

func executionPlanScheduleAllows(disabledFrom, disabledUntil *time.Time) func(time.Time) (bool, error) {
	return func(candidate time.Time) (bool, error) {
		return !dateRangePauseActive(disabledFrom, disabledUntil, candidate), nil
	}
}

func (tr *TestRunner) CreateExecutionPlanSchedule(planID string, req CreateExecutionPlanScheduleRequest) (*storage.ExecutionPlanSchedule, error) {
	return tr.upsertExecutionPlanSchedule(planID, uuid.New().String(), nil, req)
}

func (tr *TestRunner) UpdateExecutionPlanSchedule(planID, scheduleID string, req CreateExecutionPlanScheduleRequest) (*storage.ExecutionPlanSchedule, bool, error) {
	if tr.execPlanScheduleStore == nil {
		return nil, false, fmt.Errorf("storage is not configured")
	}
	existing, err := tr.execPlanScheduleStore.GetExecutionPlanSchedule(scheduleID)
	if err != nil || existing == nil || existing.PlanID != planID {
		return nil, false, nil
	}
	updated, err := tr.upsertExecutionPlanSchedule(planID, scheduleID, existing, req)
	return updated, true, err
}

func (tr *TestRunner) upsertExecutionPlanSchedule(planID, scheduleID string, existing *storage.ExecutionPlanSchedule, req CreateExecutionPlanScheduleRequest) (*storage.ExecutionPlanSchedule, error) {
	if tr.execPlanScheduleStore == nil {
		return nil, fmt.Errorf("storage is not configured")
	}
	plan, err := tr.execPlanScheduleStore.GetExecutionPlan(planID)
	if err != nil || plan == nil {
		return nil, fmt.Errorf("execution plan not found: %s", planID)
	}
	cronExpr := strings.TrimSpace(req.CronExpr)
	if err := validateCronExpr(cronExpr); err != nil {
		return nil, err
	}
	tz := strings.TrimSpace(req.Timezone)
	if tz == "" {
		tz = "UTC"
	}
	enabled := true
	if existing != nil {
		enabled = existing.Enabled
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	disabledFrom := normalizeSchedulePauseBound(req.DisabledFrom)
	disabledUntil := normalizeSchedulePauseBound(req.DisabledUntil)
	if disabledFrom != nil && disabledUntil != nil && !disabledFrom.Before(*disabledUntil) {
		return nil, fmt.Errorf("disabled_from must be before disabled_until")
	}
	now := time.Now().UTC()
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = plan.Name + " Schedule"
	}
	runNameTemplate := strings.TrimSpace(req.RunNameTemplate)
	if runNameTemplate == "" {
		runNameTemplate = plan.Name
	}
	schedule := &storage.ExecutionPlanSchedule{
		ID:              scheduleID,
		PlanID:          planID,
		ProjectID:       normalizeProjectID(plan.ProjectID),
		Revision:        1,
		Name:            name,
		CronExpr:        cronExpr,
		Timezone:        tz,
		RunNameTemplate: runNameTemplate,
		Enabled:         enabled,
		MissedRunPolicy: normalizeScenarioScheduleMissedRunPolicy(req.MissedRunPolicy),
		DisabledFrom:    disabledFrom,
		DisabledUntil:   disabledUntil,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if existing != nil {
		schedule.Revision = existing.Revision + 1
		schedule.CreatedAt = existing.CreatedAt
		schedule.LastRunRunID = existing.LastRunRunID
		schedule.LastRunAt = existing.LastRunAt
	}
	if enabled {
		next, err := nextCronRun(cronExpr, tz, now, executionPlanScheduleAllows(disabledFrom, disabledUntil))
		if err != nil {
			return nil, err
		}
		schedule.NextRunAt = next
	}
	if err := tr.execPlanScheduleStore.SaveExecutionPlanSchedule(schedule); err != nil {
		return nil, err
	}
	return schedule, nil
}

func (tr *TestRunner) ListExecutionPlanSchedulesForPlan(planID string) ([]*storage.ExecutionPlanSchedule, error) {
	if tr.execPlanScheduleStore == nil {
		return nil, nil
	}
	return tr.execPlanScheduleStore.ListExecutionPlanSchedules(planID)
}

func (tr *TestRunner) DeleteExecutionPlanScheduleForPlan(planID, scheduleID string) (bool, error) {
	if tr.execPlanScheduleStore == nil {
		return false, nil
	}
	existing, err := tr.execPlanScheduleStore.GetExecutionPlanSchedule(scheduleID)
	if err != nil || existing == nil || existing.PlanID != planID {
		return false, nil
	}
	return true, tr.execPlanScheduleStore.DeleteExecutionPlanSchedule(scheduleID)
}

func (tr *TestRunner) ListExecutionPlanScheduleOccurrencesForPlan(planID, scheduleID string) ([]*storage.ExecutionPlanScheduleOccurrence, bool, error) {
	if tr.execPlanScheduleStore == nil {
		return nil, false, nil
	}
	existing, err := tr.execPlanScheduleStore.GetExecutionPlanSchedule(scheduleID)
	if err != nil || existing == nil || existing.PlanID != planID {
		return nil, false, nil
	}
	occ, err := tr.execPlanScheduleStore.ListExecutionPlanScheduleOccurrences(scheduleID)
	return occ, true, err
}

// sweepDueExecutionPlanSchedules materializes every enabled plan schedule whose
// next_run_at has arrived. Called from the leader-gated scenario-scheduler sweep
// (RunDueSchedules) AFTER tr.mu is released, since materialize locks internally.
func (tr *TestRunner) sweepDueExecutionPlanSchedules(now time.Time) {
	store := tr.execPlanScheduleStore
	if store == nil {
		return
	}
	schedules, err := store.ListAllExecutionPlanSchedules()
	if err != nil {
		return
	}
	for _, schedule := range schedules {
		if schedule == nil || !schedule.Enabled || schedule.NextRunAt == nil || schedule.NextRunAt.After(now) {
			continue
		}
		tr.runDueExecutionPlanScheduleOnce(schedule, now)
	}
}

func (tr *TestRunner) runDueExecutionPlanScheduleOnce(schedule *storage.ExecutionPlanSchedule, now time.Time) {
	store := tr.execPlanScheduleStore
	plannedAt := schedule.NextRunAt.UTC()

	advanceNextRun := func(lastErr string) {
		next, err := nextCronRun(schedule.CronExpr, schedule.Timezone, plannedAt, executionPlanScheduleAllows(schedule.DisabledFrom, schedule.DisabledUntil))
		if err != nil {
			schedule.NextRunAt = nil
			schedule.LastError = err.Error()
		} else {
			schedule.NextRunAt = next
			schedule.LastError = lastErr
		}
		schedule.UpdatedAt = now
		_ = store.SaveExecutionPlanSchedule(schedule)
	}

	// Idempotency: if this planned time already has an occurrence, do not
	// re-materialize — only advance next_run_at (without re-stamping last-run,
	// which belongs to the original materialization).
	if existing, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt); err == nil && existing != nil {
		advanceNextRun(schedule.LastError)
		return
	}

	// Crash-safe pre-claim: write the occurrence anchor BEFORE any side effects so
	// a crash/leadership-loss mid-materialization cannot double-run the plan (the
	// next sweep sees the occurrence and dedups). If the claim cannot be persisted,
	// do not materialize or advance — retry on the next sweep.
	occurrence := &storage.ExecutionPlanScheduleOccurrence{
		ID:               uuid.New().String(),
		ScheduleID:       schedule.ID,
		PlanID:           schedule.PlanID,
		ProjectID:        schedule.ProjectID,
		ScheduleRevision: schedule.Revision,
		PlannedAt:        plannedAt,
		Status:           "materializing",
		ReasonCode:       "due",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := store.SaveExecutionPlanScheduleOccurrence(occurrence); err != nil {
		schedule.LastError = err.Error()
		schedule.UpdatedAt = now
		_ = store.SaveExecutionPlanSchedule(schedule)
		return
	}

	finalize := func(status, reasonCode, runID, lastErr string) {
		occurrence.Status = status
		occurrence.ReasonCode = reasonCode
		occurrence.RunID = runID
		occurrence.UpdatedAt = now
		if status == "materialized" {
			occurrence.MaterializedAt = &now
		}
		_ = store.SaveExecutionPlanScheduleOccurrence(occurrence)
		if runID != "" {
			schedule.LastRunRunID = runID
			schedule.LastRunAt = &now
		}
		advanceNextRun(lastErr)
	}

	plan, err := store.GetExecutionPlan(schedule.PlanID)
	if err != nil || plan == nil {
		finalize("blocked", "plan_not_found", "", "execution plan not found")
		return
	}
	result, err := tr.MaterializeAndRunExecutionPlan(plan, "", "")
	if err != nil {
		finalize("failed", "materialize_failed", "", err.Error())
		return
	}
	if result.Blocked {
		finalize("blocked", "approval_required", "", "plan run requires approval (exceeds project quota)")
		return
	}
	finalize("materialized", "started", result.RunID, "")
}
