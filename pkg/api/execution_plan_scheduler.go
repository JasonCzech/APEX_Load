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
	"github.com/jasonczech/apexload/pkg/contentmutation"
	"github.com/jasonczech/apexload/pkg/storage"
)

// CreateExecutionPlanScheduleRequest is the create/update payload for a recurring
// execution-plan schedule.
type CreateExecutionPlanScheduleRequest struct {
	Name             string     `json:"name"`
	CronExpr         string     `json:"cron_expr"`
	Timezone         string     `json:"timezone,omitempty"`
	ReleaseID        *string    `json:"release_id,omitempty"` // omitted keeps the stored release; "" uses the plan's
	RunNameTemplate  string     `json:"run_name_template,omitempty"`
	Enabled          *bool      `json:"enabled,omitempty"`
	MissedRunPolicy  string     `json:"missed_run_policy,omitempty"`
	DisabledFrom     *time.Time `json:"disabled_from,omitempty"`
	DisabledUntil    *time.Time `json:"disabled_until,omitempty"`
	ExpectedRevision *int       `json:"expected_revision,omitempty"`
}

type executionPlanScheduleCASPersistence interface {
	CreateExecutionPlanScheduleIfAbsent(schedule *storage.ExecutionPlanSchedule) (bool, error)
	UpdateExecutionPlanScheduleIfRevision(schedule *storage.ExecutionPlanSchedule, expectedRevision int) (bool, error)
}

type executionPlanScheduleOccurrenceCASPersistence interface {
	CompareAndSwapExecutionPlanScheduleOccurrence(occurrence *storage.ExecutionPlanScheduleOccurrence, expectedRevision int64) (bool, error)
}

type executionPlanScheduleOccurrenceClaimPersistence interface {
	ClaimExecutionPlanScheduleOccurrenceIfCurrent(occurrence *storage.ExecutionPlanScheduleOccurrence) (bool, error)
}

type leaseTermFencedExecutionPlanScheduleOccurrenceClaimPersistence interface {
	ClaimExecutionPlanScheduleOccurrenceIfCurrentAndLeaseTermHeld(occurrence *storage.ExecutionPlanScheduleOccurrence, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
}

var (
	errExecutionPlanNotFound            = errors.New("execution plan not found")
	errExecutionPlanSchedulePersistence = errors.New("execution-plan schedule persistence failed")
)

func executionPlanSchedulePersistenceError(action string, err error) error {
	return fmt.Errorf("%w: %s: %w", errExecutionPlanSchedulePersistence, action, err)
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

func (tr *TestRunner) CreateExecutionPlanSchedule(planID string, req CreateExecutionPlanScheduleRequest, actors ...testCreationActor) (*storage.ExecutionPlanSchedule, error) {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return nil, err
	}
	return tr.upsertExecutionPlanScheduleWithTerm(planID, uuid.New().String(), nil, req, leaseTerm, actors...)
}

func (tr *TestRunner) UpdateExecutionPlanSchedule(planID, scheduleID string, req CreateExecutionPlanScheduleRequest, actors ...testCreationActor) (*storage.ExecutionPlanSchedule, bool, error) {
	if tr.execPlanScheduleStore == nil {
		return nil, false, fmt.Errorf("storage is not configured")
	}
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return nil, false, err
	}
	existing, err := tr.execPlanScheduleStore.GetExecutionPlanSchedule(scheduleID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, executionPlanSchedulePersistenceError("load execution-plan schedule", err)
	}
	if existing == nil || existing.PlanID != planID {
		return nil, false, nil
	}
	if req.ExpectedRevision != nil && *req.ExpectedRevision != existing.Revision {
		return nil, true, fmt.Errorf("%w: expected revision %d, current revision %d", errScheduleConcurrentChange, *req.ExpectedRevision, existing.Revision)
	}
	updated, err := tr.upsertExecutionPlanScheduleWithTerm(planID, scheduleID, existing, req, leaseTerm, actors...)
	return updated, true, err
}

func (tr *TestRunner) upsertExecutionPlanSchedule(planID, scheduleID string, existing *storage.ExecutionPlanSchedule, req CreateExecutionPlanScheduleRequest, actors ...testCreationActor) (*storage.ExecutionPlanSchedule, error) {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return nil, err
	}
	return tr.upsertExecutionPlanScheduleWithTerm(planID, scheduleID, existing, req, leaseTerm, actors...)
}

func (tr *TestRunner) upsertExecutionPlanScheduleWithTerm(planID, scheduleID string, existing *storage.ExecutionPlanSchedule, req CreateExecutionPlanScheduleRequest, leaseTerm executionLeaseTerm, actors ...testCreationActor) (*storage.ExecutionPlanSchedule, error) {
	if tr.execPlanScheduleStore == nil {
		return nil, fmt.Errorf("storage is not configured")
	}
	plan, err := tr.execPlanScheduleStore.GetExecutionPlan(planID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", errExecutionPlanNotFound, planID)
		}
		return nil, executionPlanSchedulePersistenceError("load execution plan", err)
	}
	if plan == nil {
		return nil, fmt.Errorf("%w: %s", errExecutionPlanNotFound, planID)
	}
	cronExpr := strings.TrimSpace(req.CronExpr)
	if err := validateCronExpr(cronExpr); err != nil {
		return nil, err
	}
	tz := strings.TrimSpace(req.Timezone)
	if tz == "" {
		tz = "UTC"
	}
	if err := validateScheduleTimezone(tz); err != nil {
		return nil, err
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
	releaseID := ""
	if existing != nil {
		releaseID = existing.ReleaseID
	}
	if req.ReleaseID != nil {
		releaseID = strings.TrimSpace(*req.ReleaseID)
	}
	if releaseID != "" && (existing == nil || req.ReleaseID != nil) {
		// A schedule chooses another release of the plan's application.
		applicationID := strings.TrimSpace(plan.ApplicationID)
		if applicationID == "" {
			return nil, errors.New("choose the plan's application and release before giving a schedule its own release")
		}
		if err := normalizeExecutionPlanRelease(tr.execPlanScheduleStore, plan.ProjectID, &applicationID, &releaseID); err != nil {
			if errors.Is(err, errExecutionPlanReleaseOutsideApplication) {
				return nil, fmt.Errorf("release %q is not a release of the plan's application", releaseID)
			}
			return nil, err
		}
	}
	actor := systemTestCreationActor("controller:execution-plan-schedule")
	if existing != nil {
		actor = executionPlanScheduleCreationActor(existing)
	}
	if len(actors) > 0 {
		actor = normalizeTestCreationActor(actors[0])
	}
	schedule := &storage.ExecutionPlanSchedule{
		ID:               scheduleID,
		PlanID:           planID,
		ProjectID:        normalizeProjectID(plan.ProjectID),
		Revision:         1,
		Name:             name,
		CronExpr:         cronExpr,
		Timezone:         tz,
		ReleaseID:        releaseID,
		RunNameTemplate:  runNameTemplate,
		Enabled:          enabled,
		MissedRunPolicy:  normalizeScenarioScheduleMissedRunPolicy(req.MissedRunPolicy),
		DisabledFrom:     disabledFrom,
		DisabledUntil:    disabledUntil,
		ActorType:        actor.Type,
		ActorSubject:     actor.Subject,
		ActorDisplayName: actor.DisplayName,
		CreatedAt:        now,
		UpdatedAt:        now,
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
			// A cosmetic/governance PUT must not silently skip an occurrence
			// that is already due but has not reached the periodic sweep yet.
			// Preserve the durable anchor only when every timing input is the
			// same; cron/timezone/pause changes intentionally recompute it.
			if existing != nil && existing.NextRunAt != nil && executionPlanScheduleTimingEquivalent(existing, schedule) {
				schedule.NextRunAt = cloneTimePtr(existing.NextRunAt)
				// A run held at that time stays held, so the schedule keeps saying so.
				if existing.NextRequiredAction == "approval_required" || existing.NextRequiredAction == executionPlanWaitingForPreviousRun {
					schedule.NextRequiredAction, schedule.LastError = existing.NextRequiredAction, existing.LastError
				}
			}
		}
	}
	expectedRevision := 0
	create := existing == nil
	if existing != nil {
		expectedRevision = existing.Revision
	}
	if err := tr.persistExecutionPlanScheduleWithTerm(schedule, create, expectedRevision, leaseTerm); err != nil {
		return nil, err
	}
	tr.skipQueuedOccurrenceLeftBySchedule(existing, schedule, now, leaseTerm)
	return schedule, nil
}

// skipQueuedOccurrenceLeftBySchedule skips the run a schedule held queued
// behind the plan's previous run once an edit moves the schedule off it (new
// timing, or disabled): nothing would start it, and its row would read as
// queued for good.
func (tr *TestRunner) skipQueuedOccurrenceLeftBySchedule(existing, updated *storage.ExecutionPlanSchedule, now time.Time, leaseTerm executionLeaseTerm) {
	if existing == nil || updated == nil || existing.NextRunAt == nil {
		return
	}
	if updated.Enabled && updated.NextRunAt != nil && updated.NextRunAt.Equal(*existing.NextRunAt) {
		return
	}
	occurrence, err := tr.execPlanScheduleStore.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(existing.ID, existing.NextRunAt.UTC())
	if err != nil || occurrence == nil || !strings.EqualFold(occurrence.Status, "blocked") || occurrence.ReasonCode != executionPlanWaitingForPreviousRun {
		return
	}
	expectedRevision := occurrence.Revision
	occurrence.Status = "skipped"
	occurrence.ReasonCode = "schedule_changed"
	occurrence.ConflictSummary = "Skipped: the schedule was changed while this run waited for the plan's previous run."
	occurrence.UpdatedAt = now
	occurrence.TransitionToken = uuid.NewString()
	if err := tr.persistExecutionPlanScheduleOccurrenceTransitionWithTerm(occurrence, expectedRevision, leaseTerm); err != nil {
		slog.Warn("could not skip a queued plan run its schedule no longer holds", "schedule_id", existing.ID, "occurrence_id", occurrence.ID, "error", err)
	}
}

func executionPlanScheduleCreationActor(schedule *storage.ExecutionPlanSchedule) testCreationActor {
	if schedule == nil || (strings.TrimSpace(schedule.ActorType) == "" && strings.TrimSpace(schedule.ActorSubject) == "") {
		return normalizeTestCreationActor(testCreationActor{
			Type: "legacy", Subject: "controller:execution-plan-schedule-unattributed",
		})
	}
	return normalizeTestCreationActor(testCreationActor{
		Type:        schedule.ActorType,
		Subject:     schedule.ActorSubject,
		DisplayName: schedule.ActorDisplayName,
	})
}

func stampExecutionPlanScheduleOccurrenceActor(occurrence *storage.ExecutionPlanScheduleOccurrence, schedule *storage.ExecutionPlanSchedule) {
	if occurrence == nil {
		return
	}
	actor := executionPlanScheduleCreationActor(schedule)
	occurrence.ActorType = actor.Type
	occurrence.ActorSubject = actor.Subject
	occurrence.ActorDisplayName = actor.DisplayName
}

func executionPlanScheduleOccurrenceCreationActor(occurrence *storage.ExecutionPlanScheduleOccurrence) testCreationActor {
	if occurrence == nil || (strings.TrimSpace(occurrence.ActorType) == "" && strings.TrimSpace(occurrence.ActorSubject) == "") {
		return normalizeTestCreationActor(testCreationActor{
			Type: "legacy", Subject: "controller:execution-plan-schedule-occurrence-unattributed",
		})
	}
	return normalizeTestCreationActor(testCreationActor{
		Type:        occurrence.ActorType,
		Subject:     occurrence.ActorSubject,
		DisplayName: occurrence.ActorDisplayName,
		InitiatedBy: occurrence.ActorSubject,
	})
}

func executionPlanScheduleTimingEquivalent(left, right *storage.ExecutionPlanSchedule) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Enabled == right.Enabled &&
		left.CronExpr == right.CronExpr &&
		left.Timezone == right.Timezone &&
		sameOptionalTime(left.DisabledFrom, right.DisabledFrom) &&
		sameOptionalTime(left.DisabledUntil, right.DisabledUntil)
}

func (tr *TestRunner) persistExecutionPlanSchedule(schedule *storage.ExecutionPlanSchedule, create bool, expectedRevision int) error {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return err
	}
	return tr.persistExecutionPlanScheduleWithTerm(schedule, create, expectedRevision, leaseTerm)
}

func (tr *TestRunner) persistExecutionPlanScheduleWithTerm(schedule *storage.ExecutionPlanSchedule, create bool, expectedRevision int, leaseTerm executionLeaseTerm) error {
	if tr == nil || tr.execPlanScheduleStore == nil {
		return fmt.Errorf("execution plan schedule storage is not configured")
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	if cas, ok := tr.execPlanScheduleStore.(executionPlanScheduleCASPersistence); ok {
		var (
			changed bool
			err     error
		)
		if tr.durableExecutionFence {
			fenced, ok := tr.execPlanScheduleStore.(leaseTermFencedExecutionPlanSchedulePersistence)
			if !ok {
				return fmt.Errorf("execution-plan schedule storage does not support lease-term fencing")
			}
			if create {
				changed, err = fenced.CreateExecutionPlanScheduleIfAbsentAndLeaseTermHeld(schedule, executionLeaseName, leaseTerm.ownerID, leaseTerm.generation)
			} else {
				changed, err = fenced.UpdateExecutionPlanScheduleIfRevisionAndLeaseTermHeld(schedule, expectedRevision, executionLeaseName, leaseTerm.ownerID, leaseTerm.generation)
			}
		} else if create {
			changed, err = cas.CreateExecutionPlanScheduleIfAbsent(schedule)
		} else {
			changed, err = cas.UpdateExecutionPlanScheduleIfRevision(schedule, expectedRevision)
		}
		if err != nil {
			// CAS implementations assign the next revision before the surrounding
			// transaction commits. Restore the caller's durable snapshot when the
			// commit result is unknown so a retry cannot accidentally skip a
			// revision and advance the schedule twice.
			if !create {
				schedule.Revision = expectedRevision
			}
			return err
		}
		if !changed {
			if !create {
				schedule.Revision = expectedRevision
			}
			if !tr.executionLeaseTermCurrent(leaseTerm) {
				return errNotExecutionLeader
			}
			return fmt.Errorf("%w: execution plan schedule %s changed or was deleted concurrently", errScheduleConcurrentChange, schedule.ID)
		}
		return nil
	}
	if tr.durableExecutionFence {
		return fmt.Errorf("execution-plan schedule storage does not support atomic lease-term fencing")
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
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return false, err
	}
	existing, err := tr.execPlanScheduleStore.GetExecutionPlanSchedule(scheduleID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, executionPlanSchedulePersistenceError("load execution-plan schedule before delete", err)
	}
	if existing == nil || existing.PlanID != planID {
		return false, nil
	}
	if tr.durableExecutionFence {
		store, ok := tr.execPlanScheduleStore.(leaseTermFencedExecutionPlanScheduleDeletePersistence)
		if !ok {
			return true, fmt.Errorf("execution-plan schedule storage does not support lease-term-fenced guarded deletion")
		}
		result, err := store.DeleteExecutionPlanScheduleGuardedIfLeaseTermHeld(scheduleID, executionLeaseName, leaseTerm.ownerID, leaseTerm.generation)
		if err == nil && result == "" && !tr.executionLeaseTermCurrent(leaseTerm) {
			return true, errNotExecutionLeader
		}
		if result == storage.ScheduleDeleteBlockedByActiveOccurrence {
			return true, errScheduleHasActiveOccurrence
		}
		return result == storage.ScheduleDeleteDeleted, err
	}
	if store, ok := tr.execPlanScheduleStore.(guardedExecutionPlanScheduleDeletePersistence); ok {
		result, err := store.DeleteExecutionPlanScheduleGuarded(scheduleID)
		if result == storage.ScheduleDeleteBlockedByActiveOccurrence {
			return true, errScheduleHasActiveOccurrence
		}
		return result == storage.ScheduleDeleteDeleted, err
	}
	return true, tr.execPlanScheduleStore.DeleteExecutionPlanSchedule(scheduleID)
}

func (tr *TestRunner) ListExecutionPlanScheduleOccurrencesForPlan(planID, scheduleID string) ([]*storage.ExecutionPlanScheduleOccurrence, bool, error) {
	if tr.execPlanScheduleStore == nil {
		return nil, false, nil
	}
	existing, err := tr.execPlanScheduleStore.GetExecutionPlanSchedule(scheduleID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, executionPlanSchedulePersistenceError("load execution-plan schedule occurrences parent", err)
	}
	if existing == nil || existing.PlanID != planID {
		return nil, false, nil
	}
	occ, err := tr.execPlanScheduleStore.ListExecutionPlanScheduleOccurrences(scheduleID)
	return occ, true, err
}

func (tr *TestRunner) ListExecutionPlanScheduleOccurrencesPageForPlan(planID, scheduleID string, limit, offset int) ([]*storage.ExecutionPlanScheduleOccurrence, bool, error) {
	if tr.execPlanScheduleStore == nil {
		return nil, false, nil
	}
	existing, err := tr.execPlanScheduleStore.GetExecutionPlanSchedule(scheduleID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, executionPlanSchedulePersistenceError("load execution-plan schedule occurrences parent", err)
	}
	if existing == nil || existing.PlanID != planID {
		return nil, false, nil
	}
	if paged, ok := tr.execPlanScheduleStore.(interface {
		ListExecutionPlanScheduleOccurrencesPage(string, int, int) ([]*storage.ExecutionPlanScheduleOccurrence, error)
	}); ok {
		occurrences, err := paged.ListExecutionPlanScheduleOccurrencesPage(scheduleID, limit, offset)
		return occurrences, true, err
	}
	occurrences, err := tr.execPlanScheduleStore.ListExecutionPlanScheduleOccurrences(scheduleID)
	if err != nil {
		return nil, true, err
	}
	if limit <= 0 || offset >= len(occurrences) {
		return []*storage.ExecutionPlanScheduleOccurrence{}, true, nil
	}
	if offset < 0 {
		offset = 0
	}
	end := len(occurrences)
	if limit < len(occurrences)-offset {
		end = offset + limit
	}
	return occurrences[offset:end], true, nil
}

// sweepDueExecutionPlanSchedules materializes every enabled plan schedule whose
// next_run_at has arrived. Called from the leader-gated scenario-scheduler sweep
// (RunDueSchedules) AFTER tr.mu is released, since materialize locks internally.
func (tr *TestRunner) sweepDueExecutionPlanSchedules(now time.Time) {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return
	}
	_ = tr.sweepDueExecutionPlanSchedulesWithTerm(now, leaseTerm)
}

func (tr *TestRunner) sweepDueExecutionPlanSchedulesWithTerm(now time.Time, leaseTerm executionLeaseTerm) error {
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	store := tr.execPlanScheduleStore
	if store == nil {
		return nil
	}
	schedules, err := store.ListAllExecutionPlanSchedules()
	if err != nil {
		return fmt.Errorf("list execution-plan schedules: %w", err)
	}
	var sweepErr error
	processedOccurrences := 0
	for _, schedule := range schedules {
		if !tr.executionLeaseTermCurrent(leaseTerm) {
			return errors.Join(sweepErr, errNotExecutionLeader)
		}
		if schedule == nil || !schedule.Enabled || schedule.NextRunAt == nil || schedule.NextRunAt.After(now) {
			continue
		}
		catchUpAll := normalizeScenarioScheduleMissedRunPolicy(schedule.MissedRunPolicy) == "catch_up_all"
		for materialized := 0; schedule.NextRunAt != nil && !schedule.NextRunAt.After(now); materialized++ {
			if processedOccurrences >= maxScheduledOccurrencesPerSweep {
				return sweepErr
			}
			processedOccurrences++
			if !tr.executionLeaseTermCurrent(leaseTerm) {
				return errors.Join(sweepErr, errNotExecutionLeader)
			}
			if materialized >= maxScheduleCatchUpAllPerSweep {
				schedule.NextRequiredAction = "catch_up_limit_reached"
				schedule.LastError = fmt.Sprintf("catch_up_all materialized %d occurrences; next sweep will continue", maxScheduleCatchUpAllPerSweep)
				schedule.UpdatedAt = now
				if err := tr.persistExecutionPlanScheduleWithTerm(schedule, false, schedule.Revision, leaseTerm); err != nil {
					slog.Warn("failed to persist execution-plan catch-up limit", "schedule_id", schedule.ID, "error", err)
				}
				break
			}
			existing, loadErr := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, schedule.NextRunAt.UTC())
			if loadErr != nil {
				sweepErr = errors.Join(sweepErr, loadErr)
				schedule.LastError = loadErr.Error()
				break
			}
			// Recover a previously claimed/materialized occurrence before applying
			// missed-run policy. The existing row is the durable duplicate fence for
			// this exact planned_at and cannot be replaced by a skip/decision row.
			if existing == nil {
				if handled, err := tr.applyMissedExecutionPlanSchedulePolicy(schedule, now, leaseTerm); err != nil {
					sweepErr = errors.Join(sweepErr, err)
					schedule.LastError = err.Error()
					schedule.UpdatedAt = now
					if saveErr := tr.persistExecutionPlanScheduleWithTerm(schedule, false, schedule.Revision, leaseTerm); saveErr != nil {
						slog.Warn("failed to persist execution-plan schedule error", "schedule_id", schedule.ID, "error", saveErr)
					}
					break
				} else if handled {
					break
				}
			}
			if err := tr.runDueExecutionPlanScheduleOnceWithTerm(schedule, now, leaseTerm); err != nil {
				sweepErr = errors.Join(sweepErr, err)
				schedule.LastError = err.Error()
				schedule.UpdatedAt = now
				if saveErr := retryDurableTransition(func() error {
					return tr.persistExecutionPlanScheduleWithTerm(schedule, false, schedule.Revision, leaseTerm)
				}); saveErr != nil {
					slog.Warn("failed to persist execution-plan schedule materialization error", "schedule_id", schedule.ID, "error", saveErr)
				}
				break
			}
			if !catchUpAll {
				break
			}
		}
	}
	return sweepErr
}

func (tr *TestRunner) applyMissedExecutionPlanSchedulePolicy(schedule *storage.ExecutionPlanSchedule, now time.Time, leaseTerm executionLeaseTerm) (bool, error) {
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
			TransitionToken:  uuid.NewString(),
		}
		stampExecutionPlanScheduleOccurrenceActor(occurrence, schedule)
		if _, err := tr.createExecutionPlanScheduleOccurrenceWithTerm(occurrence, leaseTerm); err != nil {
			return true, err
		}
		if executionPlanSchedulePausedIndefinitely(schedule.DisabledFrom, schedule.DisabledUntil, now) {
			schedule.NextRunAt = nil
			schedule.LastError = ""
			schedule.NextRequiredAction = "schedule_paused"
			schedule.UpdatedAt = now
			if err := tr.persistExecutionPlanScheduleWithTerm(schedule, false, schedule.Revision, leaseTerm); err != nil {
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
		if err := tr.persistExecutionPlanScheduleWithTerm(schedule, false, schedule.Revision, leaseTerm); err != nil {
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
			TransitionToken:  uuid.NewString(),
		}
		stampExecutionPlanScheduleOccurrenceActor(occurrence, schedule)
		if _, err := tr.createExecutionPlanScheduleOccurrenceWithTerm(occurrence, leaseTerm); err != nil {
			return true, err
		}
		schedule.LastError = "missed scheduled occurrence requires operator decision"
		schedule.NextRequiredAction = "operator_decision_required"
		schedule.UpdatedAt = now
		if err := tr.persistExecutionPlanScheduleWithTerm(schedule, false, schedule.Revision, leaseTerm); err != nil {
			return true, err
		}
		return true, nil
	default:
		return false, nil
	}
}

func (tr *TestRunner) createExecutionPlanScheduleOccurrenceWithTerm(occurrence *storage.ExecutionPlanScheduleOccurrence, leaseTerm executionLeaseTerm) (bool, error) {
	if tr == nil || tr.execPlanScheduleStore == nil {
		return false, fmt.Errorf("execution-plan schedule storage is not configured")
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return false, errNotExecutionLeader
	}
	if occurrence != nil && occurrence.TransitionToken == "" {
		occurrence.TransitionToken = uuid.NewString()
	}
	if tr.durableExecutionFence {
		store, ok := tr.execPlanScheduleStore.(leaseTermFencedExecutionPlanScheduleOccurrenceClaimPersistence)
		if !ok {
			return false, fmt.Errorf("execution-plan schedule storage does not support current-parent lease-term-fenced occurrence claims")
		}
		created, err := store.ClaimExecutionPlanScheduleOccurrenceIfCurrentAndLeaseTermHeld(occurrence, executionLeaseName, leaseTerm.ownerID, leaseTerm.generation)
		if err == nil && !created && !tr.executionLeaseTermCurrent(leaseTerm) {
			return false, errNotExecutionLeader
		}
		return created, err
	}
	if store, ok := tr.execPlanScheduleStore.(executionPlanScheduleOccurrenceClaimPersistence); ok {
		return store.ClaimExecutionPlanScheduleOccurrenceIfCurrent(occurrence)
	}
	return tr.execPlanScheduleStore.CreateExecutionPlanScheduleOccurrenceIfAbsent(occurrence)
}

func (tr *TestRunner) runDueExecutionPlanScheduleOnce(schedule *storage.ExecutionPlanSchedule, now time.Time) error {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return err
	}
	return tr.runDueExecutionPlanScheduleOnceWithTerm(schedule, now, leaseTerm)
}

func (tr *TestRunner) runDueExecutionPlanScheduleOnceWithTerm(schedule *storage.ExecutionPlanSchedule, now time.Time, leaseTerm executionLeaseTerm) error {
	if !tr.executionLeaseTermCurrent(leaseTerm) {
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
		status := strings.ToLower(strings.TrimSpace(existing.Status))
		if status == "materializing" || status == "" {
			return tr.recoverExecutionPlanScheduleOccurrence(schedule, existing, now, leaseTerm)
		}
		if status == "blocked" && existing.ReasonCode == "approval_required" {
			return tr.reconcileExecutionPlanScheduleApprovalOccurrence(schedule, existing, now, leaseTerm)
		}
		if status == "blocked" && existing.ReasonCode == executionPlanWaitingForPreviousRun {
			return tr.reconcileQueuedExecutionPlanScheduleOccurrence(schedule, existing, now, leaseTerm)
		}
		// Operator-decision occurrences are durable gates. Do not reinterpret a
		// blocked row as terminal and advance past it; only the resolution API may
		// transition this exact CAS anchor to materializing or skipped.
		if status == "blocked" && existing.ReasonCode == "missed_run_requires_decision" {
			return nil
		}
		return tr.advanceExecutionPlanScheduleAfterOccurrence(schedule, existing, plannedAt, now, schedule.LastError, leaseTerm)
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
		TransitionToken:  uuid.NewString(),
	}
	stampExecutionPlanScheduleOccurrenceActor(occurrence, schedule)
	claimed, err := tr.createExecutionPlanScheduleOccurrenceWithTerm(occurrence, leaseTerm)
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
			return tr.recoverExecutionPlanScheduleOccurrence(schedule, existing, now, leaseTerm)
		}
		if strings.EqualFold(strings.TrimSpace(existing.Status), "blocked") && existing.ReasonCode == "approval_required" {
			return tr.reconcileExecutionPlanScheduleApprovalOccurrence(schedule, existing, now, leaseTerm)
		}
		if strings.EqualFold(strings.TrimSpace(existing.Status), "blocked") && existing.ReasonCode == executionPlanWaitingForPreviousRun {
			return tr.reconcileQueuedExecutionPlanScheduleOccurrence(schedule, existing, now, leaseTerm)
		}
		return tr.advanceExecutionPlanScheduleAfterOccurrence(schedule, existing, plannedAt, now, schedule.LastError, leaseTerm)
	}
	return tr.materializeClaimedExecutionPlanScheduleOccurrence(schedule, occurrence, now, leaseTerm)
}

// executionPlanHoldSummary is the schedule's note while it holds at an
// occurrence.
func executionPlanHoldSummary(reasonCode string) string {
	if reasonCode == executionPlanWaitingForPreviousRun {
		return "waiting for the plan's previous run to finish"
	}
	return "plan run requires approval"
}

// reconcileQueuedExecutionPlanScheduleOccurrence starts an occurrence queued
// behind the plan's earlier run once that run has finished. While it is still
// running the occurrence keeps waiting, without a write per sweep.
func (tr *TestRunner) reconcileQueuedExecutionPlanScheduleOccurrence(schedule *storage.ExecutionPlanSchedule, occurrence *storage.ExecutionPlanScheduleOccurrence, now time.Time, leaseTerm executionLeaseTerm) error {
	if schedule == nil || occurrence == nil {
		return fmt.Errorf("execution-plan schedule and occurrence are required")
	}
	plan, err := tr.execPlanScheduleStore.GetExecutionPlan(schedule.PlanID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load execution plan %s while reconciling a queued occurrence: %w", schedule.PlanID, err)
	}
	if plan != nil && effectiveExecutionPlanOverlapPolicy(plan) == storage.ExecutionPlanOverlapQueue {
		active, err := tr.activeExecutionPlanRuns(plan.ID, occurrence.RunID)
		if err != nil {
			return fmt.Errorf("check the plan's runs in progress for a queued occurrence: %w", err)
		}
		if len(active) > 0 {
			holdError := firstNonEmpty(occurrence.ConflictSummary, executionPlanHoldSummary(executionPlanWaitingForPreviousRun))
			if schedule.NextRunAt != nil && schedule.NextRunAt.Equal(occurrence.PlannedAt) &&
				schedule.NextRequiredAction == executionPlanWaitingForPreviousRun && schedule.LastError == holdError {
				return nil
			}
			return tr.advanceExecutionPlanScheduleAfterOccurrence(schedule, occurrence, occurrence.PlannedAt, now, holdError, leaseTerm)
		}
	}
	// The earlier run has finished (or the plan no longer queues): resume this
	// occurrence under its own run ID. Materialization rechecks the policy.
	expectedRevision := occurrence.Revision
	occurrence.Status = "materializing"
	occurrence.ReasonCode = "previous_run_finished"
	occurrence.ConflictSummary = ""
	occurrence.MaterializedAt = nil
	occurrence.UpdatedAt = now
	occurrence.TransitionToken = uuid.NewString()
	if err := tr.persistExecutionPlanScheduleOccurrenceTransitionWithTerm(occurrence, expectedRevision, leaseTerm); err != nil {
		return fmt.Errorf("resume queued scheduled execution-plan occurrence: %w", err)
	}
	return tr.materializeClaimedExecutionPlanScheduleOccurrence(schedule, occurrence, now, leaseTerm)
}

// reconcileExecutionPlanScheduleApprovalOccurrence ties the governance
// decision back to the exact scheduled occurrence that requested it. The
// occurrence retains its preassigned RunID while blocked, so approval resumes
// that run identity instead of starting an unrelated later cron occurrence.
func (tr *TestRunner) reconcileExecutionPlanScheduleApprovalOccurrence(schedule *storage.ExecutionPlanSchedule, occurrence *storage.ExecutionPlanScheduleOccurrence, now time.Time, leaseTerm executionLeaseTerm) error {
	if schedule == nil || occurrence == nil {
		return fmt.Errorf("execution-plan schedule and occurrence are required")
	}
	approvalID := strings.TrimSpace(occurrence.ApprovalID)
	if approvalID == "" {
		return fmt.Errorf("scheduled execution-plan occurrence %s requires approval but has no approval correlation", occurrence.ID)
	}
	approval, err := tr.execPlanScheduleStore.GetExecutionPlanApproval(approvalID)
	if err != nil {
		return fmt.Errorf("load execution-plan approval %s for scheduled occurrence: %w", approvalID, err)
	}
	if approval == nil || strings.TrimSpace(approval.PlanID) != strings.TrimSpace(schedule.PlanID) {
		return fmt.Errorf("scheduled execution-plan occurrence %s references a missing or mismatched approval %s", occurrence.ID, approvalID)
	}
	plan, err := tr.execPlanScheduleStore.GetExecutionPlan(schedule.PlanID)
	if err != nil {
		return fmt.Errorf("load execution plan %s while reconciling scheduled approval: %w", schedule.PlanID, err)
	}
	if plan == nil {
		return fmt.Errorf("execution plan %s no longer exists while approval %s is pending", schedule.PlanID, approvalID)
	}
	contentChanged := strings.TrimSpace(approval.PlanContentHash) == "" || approval.PlanContentHash != tr.executionPlanApprovalContentHash(plan)
	approvalStatus := strings.ToLower(strings.TrimSpace(approval.Status))
	if contentChanged {
		expectedRevision := occurrence.Revision
		occurrence.Status = "materializing"
		occurrence.ReasonCode = "approval_content_changed"
		occurrence.ApprovalID = ""
		occurrence.ConflictSummary = ""
		occurrence.MaterializedAt = nil
		occurrence.UpdatedAt = now
		occurrence.TransitionToken = uuid.NewString()
		if err := tr.persistExecutionPlanScheduleOccurrenceTransitionWithTerm(occurrence, expectedRevision, leaseTerm); err != nil {
			return fmt.Errorf("resume scheduled occurrence after approval content changed: %w", err)
		}
		return tr.materializeClaimedExecutionPlanScheduleOccurrence(schedule, occurrence, now, leaseTerm)
	}
	switch approvalStatus {
	case "pending":
		// Initial finalization already holds next_run_at at this planned slot.
		// Repair the parent only when a crash left the linkage incomplete.
		holdError := firstNonEmpty(occurrence.ConflictSummary, "plan run requires approval (exceeds project quota)")
		if schedule.NextRunAt != nil && schedule.NextRunAt.Equal(occurrence.PlannedAt) &&
			schedule.NextRequiredAction == "approval_required" && schedule.LastError == holdError {
			return nil
		}
		return tr.advanceExecutionPlanScheduleAfterOccurrence(schedule, occurrence, occurrence.PlannedAt, now, holdError, leaseTerm)
	case "approved":
		expectedRevision := occurrence.Revision
		occurrence.Status = "materializing"
		occurrence.ReasonCode = "approval_granted"
		occurrence.ConflictSummary = ""
		occurrence.MaterializedAt = nil
		occurrence.UpdatedAt = now
		occurrence.TransitionToken = uuid.NewString()
		if err := tr.persistExecutionPlanScheduleOccurrenceTransitionWithTerm(occurrence, expectedRevision, leaseTerm); err != nil {
			return fmt.Errorf("resume approved scheduled execution-plan occurrence: %w", err)
		}
		return tr.materializeClaimedExecutionPlanScheduleOccurrence(schedule, occurrence, now, leaseTerm)
	case "rejected":
		// The approval's reason says why it was asked for and, when the
		// reviewer gave one, why it was refused; say first that it was refused.
		lastErr := "execution-plan approval rejected"
		if reason := strings.TrimSpace(approval.DecisionReason); reason != "" {
			lastErr += ": " + reason
		}
		occurrence.ConflictSummary = lastErr
		return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "skipped", "approval_rejected", "", lastErr, leaseTerm)
	default:
		return fmt.Errorf("execution-plan approval %s has unsupported status %q", approval.ID, approval.Status)
	}
}

// recoverExecutionPlanScheduleOccurrence resumes a materializing occurrence
// after controller restart or leadership handoff. A persisted plan-run anchor is
// proof that materialization crossed its durable side-effect boundary. If there
// is no anchor, retrying with the exact preassigned ID is safe because every run
// mode persists that anchor before creating a test.
func (tr *TestRunner) recoverExecutionPlanScheduleOccurrence(schedule *storage.ExecutionPlanSchedule, occurrence *storage.ExecutionPlanScheduleOccurrence, now time.Time, leaseTerm executionLeaseTerm) error {
	if occurrence == nil {
		return fmt.Errorf("execution-plan schedule occurrence is required for recovery")
	}
	if strings.TrimSpace(occurrence.RunID) == "" {
		// Occurrences written by older controllers did not persist correlation
		// until after materialization. Re-running one could duplicate load. Fail
		// closed and advance the schedule so this legacy row cannot wedge forever.
		lastErr := "materializing occurrence has no durable run correlation; operator review is required before replay"
		occurrence.ConflictSummary = lastErr
		return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "blocked", "recovery_correlation_missing", "", lastErr, leaseTerm)
	}
	if tr.execPlanRunStore == nil {
		return fmt.Errorf("execution-plan run storage is not configured")
	}
	run, err := tr.execPlanRunStore.GetExecutionPlanRun(occurrence.RunID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load execution-plan run %s while recovering occurrence: %w", occurrence.RunID, err)
	}
	if run == nil || errors.Is(err, sql.ErrNoRows) {
		return tr.materializeClaimedExecutionPlanScheduleOccurrence(schedule, occurrence, now, leaseTerm)
	}

	// A sequential running anchor is rehydrated independently from its persisted
	// entry/policy snapshot. A parallel running row is an interrupted staging
	// attempt; every enabled entry already carries its deterministic TestID, so
	// recovery can terminalize every staged row before closing the run.
	if strings.EqualFold(run.RunMode, "parallel") && strings.EqualFold(run.Status, "running") {
		if err := tr.failInterruptedParallelExecutionPlanRun(run, leaseTerm); err != nil {
			return err
		}
		recovered, loadErr := tr.execPlanRunStore.GetExecutionPlanRun(run.ID)
		if loadErr != nil || recovered == nil {
			return fmt.Errorf("reload recovered parallel execution-plan run %s: %w", run.ID, firstNonNilError(loadErr, sql.ErrNoRows))
		}
		if strings.EqualFold(recovered.Status, "completed") && recovered.TransitionToken != interruptedParallelExecutionPlanTransitionToken {
			occurrence.ConflictSummary = ""
			return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "materialized", "recovered", recovered.ID, "", leaseTerm)
		}
		lastErr := "parallel execution-plan materialization was interrupted; recovery could not reconstruct every immutable entry"
		occurrence.ConflictSummary = lastErr
		return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "failed", "materialization_interrupted", recovered.ID, lastErr, leaseTerm)
	}
	if strings.EqualFold(run.RunMode, "parallel") && strings.EqualFold(run.Status, "failed") &&
		run.TransitionToken == interruptedParallelExecutionPlanTransitionToken {
		lastErr := "parallel execution-plan materialization was interrupted; replay was suppressed to avoid duplicate tests"
		occurrence.ConflictSummary = lastErr
		return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "failed", "materialization_interrupted", run.ID, lastErr, leaseTerm)
	}
	return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "materialized", "recovered", run.ID, "", leaseTerm)
}

func (tr *TestRunner) materializeClaimedExecutionPlanScheduleOccurrence(schedule *storage.ExecutionPlanSchedule, occurrence *storage.ExecutionPlanScheduleOccurrence, now time.Time, leaseTerm executionLeaseTerm) error {
	if occurrence == nil || strings.TrimSpace(occurrence.RunID) == "" {
		return fmt.Errorf("execution-plan schedule occurrence requires a preassigned run id")
	}
	store := tr.execPlanScheduleStore
	plan, err := store.GetExecutionPlan(schedule.PlanID)
	if err != nil {
		return fmt.Errorf("load execution plan %s for scheduled materialization: %w", schedule.PlanID, err)
	}
	if plan == nil {
		return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "blocked", "plan_not_found", "", "execution plan not found", leaseTerm)
	}
	if archiveErr := tr.checkArchivedExecutionPlan(plan, occurrence.PlannedAt); archiveErr != nil {
		if errors.Is(archiveErr, storage.ErrObjectArchived) {
			occurrence.ConflictSummary = archiveErr.Error()
			return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "skipped", "object_archived", "", archiveErr.Error(), leaseTerm)
		}
		return archiveErr
	}
	runName := renderExecutionPlanScheduleRunName(schedule, plan, occurrence.PlannedAt)
	if releaseID := strings.TrimSpace(schedule.ReleaseID); releaseID != "" {
		// The schedule files its runs under its own release.
		scheduled := *plan
		scheduled.ApplicationID, scheduled.ReleaseID = "", releaseID
		plan = &scheduled
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		// Keep the correlated claim materializing. The next leader can safely
		// inspect/resume it; declaring it blocked here would discard recoverable
		// work merely because the lease changed between two durable boundaries.
		return errNotExecutionLeader
	}
	actor := executionPlanScheduleOccurrenceCreationActor(occurrence)
	attribSubject := ""
	if actor.Type == "user" || actor.Type == "service_account" {
		attribSubject = actor.Subject
	}
	result, err := tr.materializeAndRunExecutionPlanWithRunIDAndTerm(plan, attribSubject, actor.DisplayName, runName, occurrence.RunID, leaseTerm, actor)
	if err != nil {
		persistedRunID := ""
		if tr.execPlanRunStore != nil {
			if persisted, loadErr := tr.execPlanRunStore.GetExecutionPlanRun(occurrence.RunID); loadErr == nil && persisted != nil {
				persistedRunID = persisted.ID
			}
		}
		if errors.Is(err, storage.ErrObjectArchived) {
			occurrence.ConflictSummary = err.Error()
			return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "skipped", "object_archived", persistedRunID, err.Error(), leaseTerm)
		}
		return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "failed", "materialize_failed", persistedRunID, err.Error(), leaseTerm)
	}
	if result.Overlap != nil {
		started := result.Overlap.StartedAt.UTC().Format("Jan 2 15:04")
		if result.Overlap.Policy == storage.ExecutionPlanOverlapQueue {
			occurrence.ConflictSummary = fmt.Sprintf("Waiting for the plan's run in progress (started %s UTC) to finish.", started)
			return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "blocked", executionPlanWaitingForPreviousRun, occurrence.RunID, occurrence.ConflictSummary, leaseTerm)
		}
		occurrence.ConflictSummary = fmt.Sprintf("Skipped: the plan's previous run (started %s UTC) was still running.", started)
		return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "skipped", "previous_run_active", "", occurrence.ConflictSummary, leaseTerm)
	}
	if result.Blocked {
		if result.Approval == nil || strings.TrimSpace(result.Approval.ID) == "" {
			return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "failed", "approval_correlation_missing", occurrence.RunID, "governance blocked the plan run without returning an approval correlation", leaseTerm)
		}
		occurrence.ApprovalID = result.Approval.ID
		return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "blocked", "approval_required", occurrence.RunID, "plan run requires approval (exceeds project quota)", leaseTerm)
	}
	if result.RunID != occurrence.RunID {
		lastErr := fmt.Sprintf("execution-plan materializer returned run %s for occurrence correlated to %s", result.RunID, occurrence.RunID)
		return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "failed", "run_correlation_mismatch", result.RunID, lastErr, leaseTerm)
	}
	return tr.finalizeExecutionPlanScheduleOccurrence(schedule, occurrence, now, "materialized", "started", result.RunID, "", leaseTerm)
}

func (tr *TestRunner) persistExecutionPlanScheduleOccurrenceTransitionWithTerm(occurrence *storage.ExecutionPlanScheduleOccurrence, expectedRevision int64, leaseTerm executionLeaseTerm) error {
	if tr == nil || tr.execPlanScheduleStore == nil || occurrence == nil {
		return fmt.Errorf("execution-plan schedule occurrence storage is not configured")
	}
	if expectedRevision < 1 {
		return fmt.Errorf("execution-plan schedule occurrence expected revision must be positive")
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	if occurrence.TransitionToken == "" {
		occurrence.TransitionToken = uuid.NewString()
	}
	occurrence.Revision = expectedRevision
	var (
		updated  bool
		writeErr error
	)
	if tr.durableExecutionFence {
		store, ok := tr.execPlanScheduleStore.(leaseTermFencedExecutionPlanSchedulePersistence)
		if !ok {
			return fmt.Errorf("execution-plan schedule occurrence storage does not support lease-term fencing")
		}
		updated, writeErr = store.CompareAndSwapExecutionPlanScheduleOccurrenceIfLeaseTermHeld(occurrence, expectedRevision, executionLeaseName, leaseTerm.ownerID, leaseTerm.generation)
	} else {
		store, ok := tr.execPlanScheduleStore.(executionPlanScheduleOccurrenceCASPersistence)
		if !ok {
			return fmt.Errorf("execution-plan schedule occurrence storage does not support revision CAS")
		}
		updated, writeErr = store.CompareAndSwapExecutionPlanScheduleOccurrence(occurrence, expectedRevision)
	}
	resolution := contentmutation.ResolveWrite(updated, writeErr,
		func() bool { return tr.executionLeaseTermCurrent(leaseTerm) },
		func() (*storage.ExecutionPlanScheduleOccurrence, error) {
			return tr.execPlanScheduleStore.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(occurrence.ScheduleID, occurrence.PlannedAt)
		},
		func(current *storage.ExecutionPlanScheduleOccurrence) bool {
			return executionPlanScheduleOccurrenceTransitionMatches(current, occurrence, expectedRevision)
		},
	)
	if resolution.State == contentmutation.WriteApplied {
		if resolution.Current != nil {
			*occurrence = *resolution.Current
			occurrence.MaterializedAt = cloneTimePtr(resolution.Current.MaterializedAt)
		}
		return nil
	}
	if resolution.State == contentmutation.WriteOwnershipLost {
		occurrence.Revision = expectedRevision
		return errNotExecutionLeader
	}
	current, reloadErr := resolution.Current, resolution.ReadError
	occurrence.Revision = expectedRevision
	if writeErr != nil {
		if reloadErr != nil {
			return fmt.Errorf("%w: reconcile occurrence %s after CAS error %w: %v", errExecutionPlanRunCommitUnknown, occurrence.ID, writeErr, reloadErr)
		}
		return fmt.Errorf("%w: occurrence %s CAS failed: %w", errExecutionPlanRunCommitUnknown, occurrence.ID, writeErr)
	}
	if reloadErr != nil {
		return errors.Join(fmt.Errorf("%w: occurrence %s expected revision %d", errExecutionPlanRunConflict, occurrence.ID, expectedRevision), reloadErr)
	}
	if current == nil {
		return fmt.Errorf("%w: occurrence %s was deleted", errExecutionPlanRunConflict, occurrence.ID)
	}
	return fmt.Errorf("%w: occurrence %s expected revision %d, durable revision is %d", errExecutionPlanRunConflict, occurrence.ID, expectedRevision, current.Revision)
}

func executionPlanScheduleOccurrenceTransitionMatches(current, intended *storage.ExecutionPlanScheduleOccurrence, expectedRevision int64) bool {
	return current != nil && intended != nil && current.ID == intended.ID && current.ScheduleID == intended.ScheduleID &&
		current.PlanID == intended.PlanID && normalizeProjectID(current.ProjectID) == normalizeProjectID(intended.ProjectID) &&
		current.PlannedAt.Equal(intended.PlannedAt) && current.Revision == expectedRevision+1 &&
		current.TransitionToken == intended.TransitionToken && current.Status == intended.Status &&
		current.ReasonCode == intended.ReasonCode && current.RunID == intended.RunID && current.ApprovalID == intended.ApprovalID &&
		current.ConflictSummary == intended.ConflictSummary && current.ActorType == intended.ActorType &&
		current.ActorSubject == intended.ActorSubject && current.ActorDisplayName == intended.ActorDisplayName &&
		sameOptionalTime(current.MaterializedAt, intended.MaterializedAt)
}

func (tr *TestRunner) finalizeExecutionPlanScheduleOccurrence(schedule *storage.ExecutionPlanSchedule, occurrence *storage.ExecutionPlanScheduleOccurrence, now time.Time, status, reasonCode, runID, lastErr string, leaseTerm executionLeaseTerm) error {
	expectedRevision := occurrence.Revision
	if expectedRevision < 1 {
		expectedRevision = 1
	}
	occurrence.Status = status
	occurrence.ReasonCode = reasonCode
	occurrence.RunID = runID
	occurrence.UpdatedAt = now
	if status == "materialized" {
		materializedAt := atMicrosecond(now.UTC())
		occurrence.MaterializedAt = &materializedAt
	} else {
		occurrence.MaterializedAt = nil
	}
	occurrence.TransitionToken = uuid.NewString()
	if err := tr.persistExecutionPlanScheduleOccurrenceTransitionWithTerm(occurrence, expectedRevision, leaseTerm); err != nil {
		tr.enqueueExecutionPlanScheduleOccurrenceFinalizeRetry(occurrence, expectedRevision, now, lastErr, leaseTerm)
		return fmt.Errorf("persist execution-plan schedule occurrence %s as %s: %w", occurrence.ID, status, err)
	}
	if err := tr.advanceExecutionPlanScheduleAfterOccurrence(schedule, occurrence, occurrence.PlannedAt, now, lastErr, leaseTerm); err != nil {
		tr.enqueueExecutionPlanScheduleOccurrenceFinalizeRetry(occurrence, expectedRevision, now, lastErr, leaseTerm)
		return err
	}
	return nil
}

func (tr *TestRunner) advanceExecutionPlanScheduleAfterOccurrence(schedule *storage.ExecutionPlanSchedule, occurrence *storage.ExecutionPlanScheduleOccurrence, plannedAt, now time.Time, lastErr string, leaseTerm executionLeaseTerm) error {
	if tr == nil || tr.execPlanScheduleStore == nil || schedule == nil {
		return fmt.Errorf("execution-plan schedule storage is not configured")
	}
	previous := *schedule
	if occurrence != nil && strings.EqualFold(strings.TrimSpace(occurrence.Status), "blocked") &&
		(occurrence.ReasonCode == "approval_required" || occurrence.ReasonCode == executionPlanWaitingForPreviousRun) {
		// The schedule holds at this occurrence until it is approved (or
		// rejected), or until the plan's earlier run finishes; it then runs or
		// is skipped, and the schedule moves on from it.
		holdAt := plannedAt.UTC()
		holdAction := occurrence.ReasonCode
		holdError := firstNonEmpty(strings.TrimSpace(occurrence.ConflictSummary), strings.TrimSpace(lastErr), executionPlanHoldSummary(holdAction))
		if schedule.NextRunAt != nil && schedule.NextRunAt.Equal(holdAt) &&
			schedule.NextRequiredAction == holdAction && schedule.LastError == holdError {
			return nil
		}
		schedule.NextRunAt = &holdAt
		schedule.NextRequiredAction = holdAction
		schedule.LastError = holdError
		schedule.UpdatedAt = now
		expectedRevision := schedule.Revision
		if err := retryDurableTransition(func() error {
			return tr.persistExecutionPlanScheduleWithTerm(schedule, false, expectedRevision, leaseTerm)
		}); err != nil {
			*schedule = previous
			return fmt.Errorf("persist execution-plan schedule hold: %w", err)
		}
		return nil
	}
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
	outcomeError, outcomeAction := executionPlanScheduleOccurrenceOutcome(occurrence, lastErr)
	// A concurrent schedule PUT may already have recomputed a later next_run_at
	// after the occurrence committed but before this linkage write. Merge only
	// occurrence-owned outcome/linkage metadata into that latest revision;
	// recomputing from plannedAt here could regress the user's newer cron/pause
	// configuration.
	if schedule.NextRunAt != nil && schedule.NextRunAt.After(plannedAt) {
		schedule.LastError = outcomeError
		schedule.NextRequiredAction = outcomeAction
		schedule.UpdatedAt = now
		expectedRevision := schedule.Revision
		if err := retryDurableTransition(func() error {
			return tr.persistExecutionPlanScheduleWithTerm(schedule, false, expectedRevision, leaseTerm)
		}); err != nil {
			*schedule = previous
			return fmt.Errorf("persist execution-plan schedule occurrence linkage: %w", err)
		}
		return nil
	}
	if executionPlanSchedulePausedIndefinitely(schedule.DisabledFrom, schedule.DisabledUntil, now) {
		schedule.NextRunAt = nil
		// Pause configuration owns the next action, but the occurrence still
		// owns its outcome. Do not retain an unrelated transient schedule error
		// after a recovered success (or hide a newly failed occurrence behind it).
		schedule.LastError = outcomeError
		schedule.NextRequiredAction = "schedule_paused"
		schedule.UpdatedAt = now
		expectedRevision := schedule.Revision
		if err := retryDurableTransition(func() error {
			return tr.persistExecutionPlanScheduleWithTerm(schedule, false, expectedRevision, leaseTerm)
		}); err != nil {
			*schedule = previous
			return fmt.Errorf("persist paused execution-plan schedule: %w", err)
		}
		return nil
	}
	advanceFrom := plannedAt
	if occurrence != nil && occurrence.ReasonCode == "object_archived" {
		advanceFrom = now
	}
	if normalizeScenarioScheduleMissedRunPolicy(schedule.MissedRunPolicy) == "catch_up_once" && plannedAt.Before(now) {
		advanceFrom = now
	}
	next, err := nextCronRun(schedule.CronExpr, schedule.Timezone, advanceFrom, executionPlanScheduleAllows(schedule.DisabledFrom, schedule.DisabledUntil))
	if err != nil {
		schedule.NextRunAt = nil
		schedule.LastError = err.Error()
	} else {
		schedule.NextRunAt = next
		schedule.LastError = outcomeError
		schedule.NextRequiredAction = outcomeAction
	}
	schedule.UpdatedAt = now
	expectedRevision := schedule.Revision
	if err := retryDurableTransition(func() error {
		return tr.persistExecutionPlanScheduleWithTerm(schedule, false, expectedRevision, leaseTerm)
	}); err != nil {
		*schedule = previous
		return fmt.Errorf("persist advanced execution-plan schedule: %w", err)
	}
	return nil
}

func executionPlanScheduleOccurrenceOutcome(occurrence *storage.ExecutionPlanScheduleOccurrence, lastErr string) (string, string) {
	status := ""
	occurrenceError := ""
	if occurrence != nil {
		status = strings.ToLower(strings.TrimSpace(occurrence.Status))
		occurrenceError = strings.TrimSpace(occurrence.ConflictSummary)
	}
	switch status {
	case "failed", "blocked":
		if occurrenceError != "" {
			return occurrenceError, "operator_decision_required"
		}
		return strings.TrimSpace(lastErr), "operator_decision_required"
	case "materialized", "skipped", "completed", "approved", "queued":
		return "", ""
	default:
		return strings.TrimSpace(lastErr), ""
	}
}

func (tr *TestRunner) enqueueExecutionPlanScheduleOccurrenceFinalizeRetry(occurrence *storage.ExecutionPlanScheduleOccurrence, expectedRevision int64, now time.Time, lastErr string, leaseTerm executionLeaseTerm) {
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
		for tr.executionPlanRetryAllowed(leaseTerm) {
			if !tr.waitExecutionPlanRetry(delay, leaseTerm) {
				return
			}
			err := tr.persistExecutionPlanScheduleOccurrenceTransitionWithTerm(&snapshot, expectedRevision, leaseTerm)
			if err == nil {
				var schedule *storage.ExecutionPlanSchedule
				schedule, err = tr.execPlanScheduleStore.GetExecutionPlanSchedule(snapshot.ScheduleID)
				if err == nil && schedule == nil {
					err = fmt.Errorf("execution-plan schedule %s no longer exists", snapshot.ScheduleID)
				}
				if err == nil {
					err = tr.advanceExecutionPlanScheduleAfterOccurrence(schedule, &snapshot, snapshot.PlannedAt, now, lastErr, leaseTerm)
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

// PreviewExecutionPlanScheduleRuns computes a candidate plan schedule's next
// count fire times without persisting anything, with the same evaluator and
// pause handling the sweep uses, so the editor shows what will actually run.
func (tr *TestRunner) PreviewExecutionPlanScheduleRuns(planID string, req CreateExecutionPlanScheduleRequest, count int) (*ScenarioSchedulePreview, error) {
	if tr.execPlanScheduleStore == nil {
		return nil, fmt.Errorf("storage is not configured")
	}
	plan, err := tr.execPlanScheduleStore.GetExecutionPlan(planID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", errExecutionPlanNotFound, planID)
		}
		return nil, executionPlanSchedulePersistenceError("load execution plan", err)
	}
	if plan == nil {
		return nil, fmt.Errorf("%w: %s", errExecutionPlanNotFound, planID)
	}
	cronExpr := strings.TrimSpace(req.CronExpr)
	if err := validateCronExpr(cronExpr); err != nil {
		return nil, err
	}
	tz := strings.TrimSpace(req.Timezone)
	if tz == "" {
		tz = "UTC"
	}
	if err := validateScheduleTimezone(tz); err != nil {
		return nil, err
	}
	count = min(max(count, 1), 50)
	disabledFrom := normalizeSchedulePauseBound(req.DisabledFrom)
	disabledUntil := normalizeSchedulePauseBound(req.DisabledUntil)
	if disabledFrom != nil && disabledUntil != nil && !disabledFrom.Before(*disabledUntil) {
		return nil, fmt.Errorf("disabled_from must be before disabled_until")
	}
	preview := &ScenarioSchedulePreview{CronExpr: cronExpr, Timezone: tz, Occurrences: make([]time.Time, 0, count)}
	previewFrom := time.Now().UTC()
	cursor := previewFrom
	allows := executionPlanScheduleAllows(disabledFrom, disabledUntil)
	if !executionPlanSchedulePausedIndefinitely(disabledFrom, disabledUntil, cursor) {
		for len(preview.Occurrences) < count {
			next, err := nextCronRun(cronExpr, tz, cursor, allows)
			if err != nil || next == nil {
				break
			}
			preview.Occurrences = append(preview.Occurrences, next.UTC())
			cursor = *next
		}
	}
	preview.Count = len(preview.Occurrences)
	var through *time.Time
	if preview.Count > 0 {
		through = &preview.Occurrences[preview.Count-1]
	}
	preview.ClockChanges = scheduleClockChanges(cronExpr, tz, previewFrom, scheduleClockChangeHorizon(previewFrom, through), allows)
	return preview, nil
}
