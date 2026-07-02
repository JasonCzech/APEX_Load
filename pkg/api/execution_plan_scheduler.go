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

func executionPlanSchedulePausedIndefinitely(disabledFrom, disabledUntil *time.Time, now time.Time) bool {
	return disabledFrom != nil && disabledUntil == nil && !now.Before(disabledFrom.UTC())
}

func renderExecutionPlanScheduleRunName(schedule *storage.ExecutionPlanSchedule, plan *storage.ExecutionPlan, plannedAt time.Time) string {
	template := ""
	if schedule != nil {
		template = strings.TrimSpace(schedule.RunNameTemplate)
	}
	planName := ""
	if plan != nil {
		planName = strings.TrimSpace(plan.Name)
	}
	if template == "" {
		template = planName
	}
	if template == "" {
		template = "Execution Plan Run"
	}
	scheduleName := ""
	timezone := ""
	if schedule != nil {
		scheduleName = strings.TrimSpace(schedule.Name)
		timezone = schedule.Timezone
	}
	local := plannedAt.UTC().In(scheduleEvalLocation(timezone))
	out := strings.NewReplacer(
		"{{plan}}", planName,
		"{{schedule}}", scheduleName,
		"{{date}}", local.Format("2006-01-02"),
		"{{time}}", local.Format("15:04"),
		"{{datetime}}", local.Format("2006-01-02 15:04 MST"),
		"{{planned_at}}", plannedAt.UTC().Format(time.RFC3339),
	).Replace(template)
	out = strings.TrimSpace(out)
	if out == "" {
		return firstNonEmpty(planName, scheduleName, "Execution Plan Run")
	}
	return out
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
		if executionPlanSchedulePausedIndefinitely(disabledFrom, disabledUntil, now) {
			schedule.NextRunAt = nil
			schedule.NextRequiredAction = "schedule_paused"
		} else {
			next, err := nextCronRun(cronExpr, tz, now, executionPlanScheduleAllows(disabledFrom, disabledUntil))
			if err != nil {
				return nil, err
			}
			schedule.NextRunAt = next
		}
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
		catchUpAll := normalizeScenarioScheduleMissedRunPolicy(schedule.MissedRunPolicy) == "catch_up_all"
		for materialized := 0; schedule.NextRunAt != nil && !schedule.NextRunAt.After(now); materialized++ {
			if materialized >= maxScheduleCatchUpAllPerSweep {
				schedule.NextRequiredAction = "catch_up_limit_reached"
				schedule.LastError = fmt.Sprintf("catch_up_all materialized %d occurrences; next sweep will continue", maxScheduleCatchUpAllPerSweep)
				schedule.UpdatedAt = now
				_ = store.SaveExecutionPlanSchedule(schedule)
				break
			}
			if handled, err := tr.applyMissedExecutionPlanSchedulePolicy(schedule, now); err != nil {
				schedule.LastError = err.Error()
				schedule.UpdatedAt = now
				_ = store.SaveExecutionPlanSchedule(schedule)
				break
			} else if handled {
				break
			}
			tr.runDueExecutionPlanScheduleOnce(schedule, now)
			if !catchUpAll {
				break
			}
		}
	}
}

func (tr *TestRunner) applyMissedExecutionPlanSchedulePolicy(schedule *storage.ExecutionPlanSchedule, now time.Time) (bool, error) {
	if schedule == nil || schedule.NextRunAt == nil || now.Sub(schedule.NextRunAt.UTC()) <= time.Minute {
		return false, nil
	}
	plannedAt := schedule.NextRunAt.UTC()
	switch normalizeScenarioScheduleMissedRunPolicy(schedule.MissedRunPolicy) {
	case "catch_up_once", "catch_up_all":
		return false, nil
	case "skip":
		occurrence := &storage.ExecutionPlanScheduleOccurrence{
			ID:               uuid.New().String(),
			ScheduleID:       schedule.ID,
			PlanID:           schedule.PlanID,
			ProjectID:        schedule.ProjectID,
			ScheduleRevision: schedule.Revision,
			PlannedAt:        plannedAt,
			Status:           "skipped",
			ReasonCode:       "missed_run_skipped",
			ConflictSummary:  "Missed scheduled execution-plan occurrence skipped by policy.",
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if _, err := tr.execPlanScheduleStore.CreateExecutionPlanScheduleOccurrenceIfAbsent(occurrence); err != nil {
			return true, err
		}
		if executionPlanSchedulePausedIndefinitely(schedule.DisabledFrom, schedule.DisabledUntil, now) {
			schedule.NextRunAt = nil
			schedule.LastError = ""
			schedule.NextRequiredAction = "schedule_paused"
			schedule.UpdatedAt = now
			if err := tr.execPlanScheduleStore.SaveExecutionPlanSchedule(schedule); err != nil {
				return true, err
			}
			return true, nil
		}
		next, err := nextCronRun(schedule.CronExpr, schedule.Timezone, now, executionPlanScheduleAllows(schedule.DisabledFrom, schedule.DisabledUntil))
		if err != nil {
			return true, err
		}
		schedule.NextRunAt = next
		schedule.LastError = ""
		schedule.NextRequiredAction = ""
		schedule.UpdatedAt = now
		if err := tr.execPlanScheduleStore.SaveExecutionPlanSchedule(schedule); err != nil {
			return true, err
		}
		return true, nil
	case "require_operator_decision":
		occurrence := &storage.ExecutionPlanScheduleOccurrence{
			ID:               uuid.New().String(),
			ScheduleID:       schedule.ID,
			PlanID:           schedule.PlanID,
			ProjectID:        schedule.ProjectID,
			ScheduleRevision: schedule.Revision,
			PlannedAt:        plannedAt,
			Status:           "blocked",
			ReasonCode:       "missed_run_requires_decision",
			ConflictSummary:  "Missed scheduled execution-plan occurrence requires an operator decision.",
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if _, err := tr.execPlanScheduleStore.CreateExecutionPlanScheduleOccurrenceIfAbsent(occurrence); err != nil {
			return true, err
		}
		schedule.LastError = "missed scheduled occurrence requires operator decision"
		schedule.NextRequiredAction = "operator_decision_required"
		schedule.UpdatedAt = now
		if err := tr.execPlanScheduleStore.SaveExecutionPlanSchedule(schedule); err != nil {
			return true, err
		}
		return true, nil
	default:
		return false, nil
	}
}

func (tr *TestRunner) runDueExecutionPlanScheduleOnce(schedule *storage.ExecutionPlanSchedule, now time.Time) {
	store := tr.execPlanScheduleStore
	plannedAt := schedule.NextRunAt.UTC()

	advanceNextRun := func(lastErr string) {
		if executionPlanSchedulePausedIndefinitely(schedule.DisabledFrom, schedule.DisabledUntil, now) {
			schedule.NextRunAt = nil
			schedule.LastError = lastErr
			schedule.NextRequiredAction = "schedule_paused"
			schedule.UpdatedAt = now
			_ = store.SaveExecutionPlanSchedule(schedule)
			return
		}
		next, err := nextCronRun(schedule.CronExpr, schedule.Timezone, plannedAt, executionPlanScheduleAllows(schedule.DisabledFrom, schedule.DisabledUntil))
		if err != nil {
			schedule.NextRunAt = nil
			schedule.LastError = err.Error()
		} else {
			schedule.NextRunAt = next
			schedule.LastError = lastErr
			schedule.NextRequiredAction = ""
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
	claimed, err := store.CreateExecutionPlanScheduleOccurrenceIfAbsent(occurrence)
	if err != nil {
		schedule.LastError = err.Error()
		schedule.UpdatedAt = now
		_ = store.SaveExecutionPlanSchedule(schedule)
		return
	}
	if !claimed {
		advanceNextRun(schedule.LastError)
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
	runName := renderExecutionPlanScheduleRunName(schedule, plan, plannedAt)
	result, err := tr.materializeAndRunExecutionPlan(plan, "", "", runName)
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
