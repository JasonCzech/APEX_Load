package api

// Recurring execution-plan schedules. A sibling of the scenario scheduler: it
// reuses the shared cron evaluator (nextCronRun) and date-range pause, but
// materializes an ExecutionPlanRun (via MaterializeAndRunExecutionPlan) instead
// of a single test. The sweep runs under the same execution-leader lease as the
// scenario scheduler and OUTSIDE tr.mu (materialize locks internally).

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
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

type executionPlanScheduleCASPersistence interface {
	CreateExecutionPlanScheduleIfAbsent(schedule *storage.ExecutionPlanSchedule) (bool, error)
	UpdateExecutionPlanScheduleIfRevision(schedule *storage.ExecutionPlanSchedule, expectedRevision int) (bool, error)
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
	expectedRevision := 0
	create := existing == nil
	if existing != nil {
		expectedRevision = existing.Revision
	}
	if err := tr.persistExecutionPlanSchedule(schedule, create, expectedRevision); err != nil {
		return nil, err
	}
	return schedule, nil
}

func (tr *TestRunner) persistExecutionPlanSchedule(schedule *storage.ExecutionPlanSchedule, create bool, expectedRevision int) error {
	if tr == nil || tr.execPlanScheduleStore == nil {
		return fmt.Errorf("execution plan schedule storage is not configured")
	}
	if cas, ok := tr.execPlanScheduleStore.(executionPlanScheduleCASPersistence); ok {
		var (
			changed bool
			err     error
		)
		if create {
			changed, err = cas.CreateExecutionPlanScheduleIfAbsent(schedule)
		} else {
			changed, err = cas.UpdateExecutionPlanScheduleIfRevision(schedule, expectedRevision)
		}
		if err != nil {
			return err
		}
		if !changed {
			return fmt.Errorf("execution plan schedule %s changed or was deleted concurrently", schedule.ID)
		}
		return nil
	}
	return tr.execPlanScheduleStore.SaveExecutionPlanSchedule(schedule)
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
	if !tr.isExecutionLeader() {
		return
	}
	store := tr.execPlanScheduleStore
	if store == nil {
		return
	}
	schedules, err := store.ListAllExecutionPlanSchedules()
	if err != nil {
		return
	}
	for _, schedule := range schedules {
		if !tr.isExecutionLeader() {
			return
		}
		if schedule == nil || !schedule.Enabled || schedule.NextRunAt == nil || schedule.NextRunAt.After(now) {
			continue
		}
		catchUpAll := normalizeScenarioScheduleMissedRunPolicy(schedule.MissedRunPolicy) == "catch_up_all"
		for materialized := 0; schedule.NextRunAt != nil && !schedule.NextRunAt.After(now); materialized++ {
			if !tr.isExecutionLeader() {
				return
			}
			if materialized >= maxScheduleCatchUpAllPerSweep {
				schedule.NextRequiredAction = "catch_up_limit_reached"
				schedule.LastError = fmt.Sprintf("catch_up_all materialized %d occurrences; next sweep will continue", maxScheduleCatchUpAllPerSweep)
				schedule.UpdatedAt = now
				if err := tr.persistExecutionPlanSchedule(schedule, false, schedule.Revision); err != nil {
					slog.Warn("failed to persist execution-plan catch-up limit", "schedule_id", schedule.ID, "error", err)
				}
				break
			}
			if handled, err := tr.applyMissedExecutionPlanSchedulePolicy(schedule, now); err != nil {
				schedule.LastError = err.Error()
				schedule.UpdatedAt = now
				if saveErr := tr.persistExecutionPlanSchedule(schedule, false, schedule.Revision); saveErr != nil {
					slog.Warn("failed to persist execution-plan schedule error", "schedule_id", schedule.ID, "error", saveErr)
				}
				break
			} else if handled {
				break
			}
			if err := tr.runDueExecutionPlanScheduleOnce(schedule, now); err != nil {
				schedule.LastError = err.Error()
				schedule.UpdatedAt = now
				if saveErr := retryDurableTransition(func() error { return tr.persistExecutionPlanSchedule(schedule, false, schedule.Revision) }); saveErr != nil {
					slog.Warn("failed to persist execution-plan schedule materialization error", "schedule_id", schedule.ID, "error", saveErr)
				}
				break
			}
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
			if err := tr.persistExecutionPlanSchedule(schedule, false, schedule.Revision); err != nil {
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
		if err := tr.persistExecutionPlanSchedule(schedule, false, schedule.Revision); err != nil {
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
		if err := tr.persistExecutionPlanSchedule(schedule, false, schedule.Revision); err != nil {
			return true, err
		}
		return true, nil
	default:
		return false, nil
	}
}

func (tr *TestRunner) runDueExecutionPlanScheduleOnce(schedule *storage.ExecutionPlanSchedule, now time.Time) error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	store := tr.execPlanScheduleStore
	plannedAt := schedule.NextRunAt.UTC()

	// A terminal occurrence is already committed: only advance next_run_at
	// (without re-stamping last-run, which belongs to the original
	// materialization). A materializing occurrence is a restart/leadership
	// recovery candidate. Its preassigned RunID lets us distinguish a committed
	// run from a claim that never reached the run anchor without creating a
	// duplicate plan run.
	existing, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt)
	if err != nil {
		return fmt.Errorf("load execution-plan schedule occurrence: %w", err)
	}
	if existing != nil {
		if strings.TrimSpace(existing.Status) == "materializing" || strings.TrimSpace(existing.Status) == "" {
			return tr.recoverExecutionPlanScheduleOccurrence(schedule, existing, now)
		}
		return tr.advanceExecutionPlanScheduleAfterOccurrence(schedule, existing, plannedAt, now, schedule.LastError)
	}

	// Crash-safe pre-claim: write both the occurrence and its deterministic run
	// correlation BEFORE any side effects. There is deliberately no durable state
	// in which new code can have launched tests for this occurrence while RunID is
	// empty. If the claim cannot be persisted, do not materialize or advance.
	occurrence := &storage.ExecutionPlanScheduleOccurrence{
		ID:               uuid.New().String(),
		ScheduleID:       schedule.ID,
		PlanID:           schedule.PlanID,
		ProjectID:        schedule.ProjectID,
		ScheduleRevision: schedule.Revision,
		PlannedAt:        plannedAt,
		Status:           "materializing",
		ReasonCode:       "due",
		RunID:            "exec-plan-run-" + uuid.New().String(),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	claimed, err := store.CreateExecutionPlanScheduleOccurrenceIfAbsent(occurrence)
	if err != nil {
		return fmt.Errorf("claim execution-plan schedule occurrence: %w", err)
	}
	if !claimed {
		existing, loadErr := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt)
		if loadErr != nil {
			return fmt.Errorf("load concurrently claimed execution-plan occurrence: %w", loadErr)
		}
		if existing == nil {
			return fmt.Errorf("execution-plan schedule occurrence was concurrently claimed but cannot be loaded")
		}
		if strings.TrimSpace(existing.Status) == "materializing" || strings.TrimSpace(existing.Status) == "" {
			return tr.recoverExecutionPlanScheduleOccurrence(schedule, existing, now)
		}
		return tr.advanceExecutionPlanScheduleAfterOccurrence(schedule, existing, plannedAt, now, schedule.LastError)
	}
	return tr.materializeClaimedExecutionPlanScheduleOccurrence(schedule, occurrence, now)
}

// recoverExecutionPlanScheduleOccurrence resumes a materializing occurrence
// after controller restart or leadership handoff. A persisted plan-run anchor is
// proof that materialization crossed its durable side-effect boundary. If there
// is no anchor, retrying with the exact preassigned ID is safe because every run
// mode persists that anchor before creating a test.
func (tr *TestRunner) recoverExecutionPlanScheduleOccurrence(schedule *storage.ExecutionPlanSchedule, occurrence *storage.ExecutionPlanScheduleOccurrence, now time.Time) error {
	if occurrence == nil {
		return fmt.Errorf("execution-plan schedule occurrence is required for recovery")
	}
	if strings.TrimSpace(occurrence.RunID) == "" {
		// Occurrences written by older controllers did not persist correlation
		// until after materialization. Re-running one could duplicate load. Fail
		// closed and advance the schedule so this legacy row cannot wedge forever.
		lastErr := "materializing occurrence has no durable run correlation; operator review is required before replay"
		occurrence.ConflictSummary = lastErr
		return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "blocked", "recovery_correlation_missing", "", lastErr)
	}
	if tr.execPlanRunStore == nil {
		return fmt.Errorf("execution-plan run storage is not configured")
	}
	run, err := tr.execPlanRunStore.GetExecutionPlanRun(occurrence.RunID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load execution-plan run %s while recovering occurrence: %w", occurrence.RunID, err)
	}
	if run == nil || errors.Is(err, sql.ErrNoRows) {
		return tr.materializeClaimedExecutionPlanScheduleOccurrence(schedule, occurrence, now)
	}

	// A sequential running anchor is rehydrated independently from its persisted
	// entry/policy snapshot. A parallel running row, however, is only the initial
	// pre-materialization anchor: a crash may have happened between creating a
	// test and recording its ID, so replay is not provably duplicate-free. Mark
	// that interrupted attempt failed instead of creating a second set of tests.
	if strings.EqualFold(run.RunMode, "parallel") && strings.EqualFold(run.Status, "running") {
		failedAt := now.UTC()
		run.Status = "failed"
		run.UpdatedAt = failedAt
		run.CompletedAt = &failedAt
		for i := range run.Entries {
			if run.Entries[i].Status == "pending" || run.Entries[i].Status == "running" {
				run.Entries[i].Status = "failed"
				run.Entries[i].Detail = "parallel materialization was interrupted before its final durable snapshot"
			}
		}
		if err := tr.persistExecutionPlanRunDurable(run); err != nil {
			return executionPlanPersistenceError("terminalize interrupted scheduled parallel materialization", err)
		}
		lastErr := "parallel execution-plan materialization was interrupted; replay was suppressed to avoid duplicate tests"
		occurrence.ConflictSummary = lastErr
		return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "failed", "materialization_interrupted", run.ID, lastErr)
	}
	return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "materialized", "recovered", run.ID, "")
}

func (tr *TestRunner) materializeClaimedExecutionPlanScheduleOccurrence(schedule *storage.ExecutionPlanSchedule, occurrence *storage.ExecutionPlanScheduleOccurrence, now time.Time) error {
	if occurrence == nil || strings.TrimSpace(occurrence.RunID) == "" {
		return fmt.Errorf("execution-plan schedule occurrence requires a preassigned run id")
	}
	store := tr.execPlanScheduleStore
	plan, err := store.GetExecutionPlan(schedule.PlanID)
	if err != nil {
		return fmt.Errorf("load execution plan %s for scheduled materialization: %w", schedule.PlanID, err)
	}
	if plan == nil {
		return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "blocked", "plan_not_found", "", "execution plan not found")
	}
	runName := renderExecutionPlanScheduleRunName(schedule, plan, occurrence.PlannedAt)
	if !tr.isExecutionLeader() {
		// Keep the correlated claim materializing. The next leader can safely
		// inspect/resume it; declaring it blocked here would discard recoverable
		// work merely because the lease changed between two durable boundaries.
		return errNotExecutionLeader
	}
	result, err := tr.materializeAndRunExecutionPlanWithRunID(plan, "", "", runName, occurrence.RunID)
	if err != nil {
		persistedRunID := ""
		if tr.execPlanRunStore != nil {
			if persisted, loadErr := tr.execPlanRunStore.GetExecutionPlanRun(occurrence.RunID); loadErr == nil && persisted != nil {
				persistedRunID = persisted.ID
			}
		}
		return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "failed", "materialize_failed", persistedRunID, err.Error())
	}
	if result.Blocked {
		return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "blocked", "approval_required", "", "plan run requires approval (exceeds project quota)")
	}
	if result.RunID != occurrence.RunID {
		lastErr := fmt.Sprintf("execution-plan materializer returned run %s for occurrence correlated to %s", result.RunID, occurrence.RunID)
		return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "failed", "run_correlation_mismatch", result.RunID, lastErr)
	}
	return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "materialized", "started", result.RunID, "")
}

func (tr *TestRunner) finalizeExecutionPlanScheduleOccurrence(schedule *storage.ExecutionPlanSchedule, occurrence *storage.ExecutionPlanScheduleOccurrence, now time.Time, status, reasonCode, runID, lastErr string) error {
	occurrence.Status = status
	occurrence.ReasonCode = reasonCode
	occurrence.RunID = runID
	occurrence.UpdatedAt = now
	if status == "materialized" {
		materializedAt := now.UTC()
		occurrence.MaterializedAt = &materializedAt
	} else {
		occurrence.MaterializedAt = nil
	}
	if err := retryDurableTransition(func() error { return tr.execPlanScheduleStore.SaveExecutionPlanScheduleOccurrence(occurrence) }); err != nil {
		tr.enqueueExecutionPlanScheduleOccurrenceFinalizeRetry(occurrence, now, lastErr)
		return fmt.Errorf("persist execution-plan schedule occurrence %s as %s: %w", occurrence.ID, status, err)
	}
	if err := tr.advanceExecutionPlanScheduleAfterOccurrence(schedule, occurrence, occurrence.PlannedAt, now, lastErr); err != nil {
		tr.enqueueExecutionPlanScheduleOccurrenceFinalizeRetry(occurrence, now, lastErr)
		return err
	}
	return nil
}

func (tr *TestRunner) advanceExecutionPlanScheduleAfterOccurrence(schedule *storage.ExecutionPlanSchedule, occurrence *storage.ExecutionPlanScheduleOccurrence, plannedAt, now time.Time, lastErr string) error {
	if tr == nil || tr.execPlanScheduleStore == nil || schedule == nil {
		return fmt.Errorf("execution-plan schedule storage is not configured")
	}
	previous := *schedule
	if occurrence != nil && occurrence.RunID != "" {
		schedule.LastRunRunID = occurrence.RunID
		if occurrence.MaterializedAt != nil {
			lastRunAt := occurrence.MaterializedAt.UTC()
			schedule.LastRunAt = &lastRunAt
		} else {
			lastRunAt := now
			schedule.LastRunAt = &lastRunAt
		}
	}
	if executionPlanSchedulePausedIndefinitely(schedule.DisabledFrom, schedule.DisabledUntil, now) {
		schedule.NextRunAt = nil
		schedule.LastError = lastErr
		schedule.NextRequiredAction = "schedule_paused"
		schedule.UpdatedAt = now
		if err := retryDurableTransition(func() error { return tr.persistExecutionPlanSchedule(schedule, false, schedule.Revision) }); err != nil {
			*schedule = previous
			return fmt.Errorf("persist paused execution-plan schedule: %w", err)
		}
		return nil
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
	if err := retryDurableTransition(func() error { return tr.persistExecutionPlanSchedule(schedule, false, schedule.Revision) }); err != nil {
		*schedule = previous
		return fmt.Errorf("persist advanced execution-plan schedule: %w", err)
	}
	return nil
}

func (tr *TestRunner) enqueueExecutionPlanScheduleOccurrenceFinalizeRetry(occurrence *storage.ExecutionPlanScheduleOccurrence, now time.Time, lastErr string) {
	if tr == nil || occurrence == nil || tr.execPlanScheduleStore == nil {
		return
	}
	snapshot := *occurrence
	snapshot.MaterializedAt = cloneTimePtr(occurrence.MaterializedAt)
	key := "schedule-occurrence:" + snapshot.ID
	if !tr.beginExecutionPlanRetry(key) {
		return
	}
	go func() {
		defer tr.endExecutionPlanRetry(key)
		delay := executionPlanRetryInitialDelay
		for tr.executionPlanRetryAllowed() {
			if !tr.waitExecutionPlanRetry(delay) {
				return
			}
			err := retryDurableTransition(func() error {
				return tr.execPlanScheduleStore.SaveExecutionPlanScheduleOccurrence(&snapshot)
			})
			if err == nil {
				var schedule *storage.ExecutionPlanSchedule
				schedule, err = tr.execPlanScheduleStore.GetExecutionPlanSchedule(snapshot.ScheduleID)
				if err == nil && schedule == nil {
					err = fmt.Errorf("execution-plan schedule %s no longer exists", snapshot.ScheduleID)
				}
				if err == nil && schedule.NextRunAt != nil && !schedule.NextRunAt.After(snapshot.PlannedAt) {
					err = tr.advanceExecutionPlanScheduleAfterOccurrence(schedule, &snapshot, snapshot.PlannedAt, now, lastErr)
				}
			}
			if err == nil {
				return
			}
			slog.Warn("execution-plan schedule occurrence finalization retry failed", "schedule_id", snapshot.ScheduleID, "occurrence_id", snapshot.ID, "error", err)
			if !durableBackgroundRetryable(err) {
				return
			}
			delay = min(delay*2, executionPlanRetryMaxDelay)
		}
	}()
}
