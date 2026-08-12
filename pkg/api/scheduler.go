package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

type scenarioSchedulePersistence interface {
	SaveScenarioSchedule(schedule *storage.ScenarioSchedule) error
	GetScenarioSchedule(id string) (*storage.ScenarioSchedule, error)
	ListScenarioSchedules(scenarioID string) ([]*storage.ScenarioSchedule, error)
	ListAllScenarioSchedules() ([]*storage.ScenarioSchedule, error)
	DeleteScenarioSchedule(id string) error
	SaveScenarioScheduleOccurrence(occurrence *storage.ScenarioScheduleOccurrence) error
	GetScenarioScheduleOccurrence(id string) (*storage.ScenarioScheduleOccurrence, error)
	GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(scheduleID string, plannedAt time.Time) (*storage.ScenarioScheduleOccurrence, error)
	ListScenarioScheduleOccurrences(scheduleID string) ([]*storage.ScenarioScheduleOccurrence, error)
	ListAllScenarioScheduleOccurrences() ([]*storage.ScenarioScheduleOccurrence, error)
	SaveScenarioScheduleApproval(approval *storage.ScenarioScheduleApproval) error
	GetScenarioScheduleApproval(id string) (*storage.ScenarioScheduleApproval, error)
	ListScenarioScheduleApprovals(scheduleID string) ([]*storage.ScenarioScheduleApproval, error)
	TryAcquireSchedulerLease(name string, ownerID string, now time.Time, ttl time.Duration) (bool, error)
	GetSchedulerLease(name string) (*storage.SchedulerLease, error)
	SaveAuditEvent(event *storage.AuditEvent) error
}

type latestScenarioScheduleOccurrenceStore interface {
	LatestScenarioScheduleOccurrence(scheduleID string) (*storage.ScenarioScheduleOccurrence, error)
}

type scenarioScheduleOccurrenceByTestStore interface {
	GetScenarioScheduleOccurrenceByTestID(testID string) (*storage.ScenarioScheduleOccurrence, error)
}

type atomicScenarioScheduleApprovalStore interface {
	ResolveScenarioScheduleApproval(approval *storage.ScenarioScheduleApproval, schedule *storage.ScenarioSchedule, expectedScheduleRevision int) (bool, error)
}

type atomicScenarioScheduleApprovalWriteStore interface {
	SaveScenarioScheduleWithApproval(schedule *storage.ScenarioSchedule, approval *storage.ScenarioScheduleApproval, expectedRevision int) (bool, error)
}

type scenarioScheduler struct {
	runner   *TestRunner
	now      func() time.Time
	ownerID  string
	leaseTTL time.Duration
	// oneTimeCursor rotates bounded one-time batches after failures. Without it,
	// a stable lexical first page of blocked jobs can starve every later due job.
	oneTimeCursor  string
	healthMu       sync.RWMutex
	sweepHealth    map[string]SchedulerSweepHealth
	workerMu       sync.Mutex
	workerStarted  bool
	workerStopping bool
	workerCancel   context.CancelFunc
	workerDone     chan struct{}
	sweepInterval  time.Duration
	// runSweep is an injectable worker seam. Direct RunDueSchedules callers and
	// production workers use the method below; lifecycle tests replace only this
	// callback so they can prove bounded shutdown without storage fixtures.
	runSweep func() error
}

const (
	maxScheduleCatchUpAllPerSweep         = 25
	maxScheduledOccurrencesPerSweep       = 256
	maxOneTimeScheduledStartsPerSweep     = 256
	defaultScenarioSchedulerSweepInterval = 30 * time.Second
)

const (
	maxCronExpressionBytes              = 512
	maxCronEligibleCandidateEvaluations = 100_000
	maxScenarioScheduleWindows          = 64
	maxScenarioScheduleWindowNameBytes  = 256
)

type CreateScenarioScheduleRequest struct {
	Name                string                           `json:"name"`
	CronExpr            string                           `json:"cron_expr"`
	Timezone            string                           `json:"timezone,omitempty"`
	ReleaseID           string                           `json:"release_id,omitempty"`
	TimeslotID          string                           `json:"timeslot_id,omitempty"`
	RunNameTemplate     string                           `json:"run_name_template,omitempty"`
	Enabled             *bool                            `json:"enabled,omitempty"`
	ApprovalPolicy      string                           `json:"approval_policy,omitempty"`
	MissedRunPolicy     string                           `json:"missed_run_policy,omitempty"`
	BlackoutWindows     []storage.ScenarioScheduleWindow `json:"blackout_windows,omitempty"`
	MaintenanceWindows  []storage.ScenarioScheduleWindow `json:"maintenance_windows,omitempty"`
	AvailabilityWindows []storage.ScenarioScheduleWindow `json:"availability_windows,omitempty"`
	// DisabledFrom/DisabledUntil pause the schedule over an absolute date range
	// without disabling it; either bound may be nil for an open-ended pause.
	DisabledFrom     *time.Time `json:"disabled_from,omitempty"`
	DisabledUntil    *time.Time `json:"disabled_until,omitempty"`
	ExpectedRevision *int       `json:"expected_revision,omitempty"`
	OwnerSubject     string     `json:"-"`
	OwnerDisplayName string     `json:"-"`
	RBACReviewSource string     `json:"-"`
}

type ScenarioScheduleHealth struct {
	Total                int                             `json:"total"`
	Enabled              int                             `json:"enabled"`
	Due                  int                             `json:"due"`
	Blocked              int                             `json:"blocked"`
	Failed               int                             `json:"failed"`
	PendingApproval      int                             `json:"pending_approval"`
	Queued               int                             `json:"queued"`
	Materialized         int                             `json:"materialized"`
	Skipped              int                             `json:"skipped"`
	Completed            int                             `json:"completed"`
	StatusCounts         map[string]int                  `json:"status_counts"`
	IsLeader             bool                            `json:"is_leader"`
	StorageError         string                          `json:"storage_error,omitempty"`
	Sweeps               map[string]SchedulerSweepHealth `json:"sweeps,omitempty"`
	ExecutionPlanTotal   int                             `json:"execution_plan_total"`
	ExecutionPlanEnabled int                             `json:"execution_plan_enabled"`
	ExecutionPlanDue     int                             `json:"execution_plan_due"`
	ExecutionPlanBlocked int                             `json:"execution_plan_blocked"`
	OneTimeScheduled     int                             `json:"one_time_scheduled"`
	OneTimeDue           int                             `json:"one_time_due"`
}

type SchedulerSweepHealth struct {
	LastSweepAt      *time.Time `json:"last_sweep_at,omitempty"`
	LastSuccessfulAt *time.Time `json:"last_successful_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
}

func newScenarioScheduler(runner *TestRunner) *scenarioScheduler {
	return &scenarioScheduler{
		runner:        runner,
		now:           time.Now,
		ownerID:       uuid.New().String(),
		leaseTTL:      30 * time.Second,
		sweepHealth:   make(map[string]SchedulerSweepHealth),
		sweepInterval: defaultScenarioSchedulerSweepInterval,
	}
}

var errScenarioSchedulerStopped = errors.New("scenario scheduler is stopped")

// Start owns the periodic schedule evaluator for the lifetime of ctx. It is
// intentionally idempotent so composition roots can safely converge on one
// owner during migration. A scheduler cannot be resurrected after Shutdown.
func (s *scenarioScheduler) Start(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.workerMu.Lock()
	if s.workerStopping {
		s.workerMu.Unlock()
		return errScenarioSchedulerStopped
	}
	if s.workerStarted {
		s.workerMu.Unlock()
		return nil
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	interval := s.sweepInterval
	if interval <= 0 {
		interval = defaultScenarioSchedulerSweepInterval
	}
	runSweep := s.runSweep
	if runSweep == nil {
		runSweep = s.RunDueSchedules
	}
	s.workerStarted = true
	s.workerCancel = cancel
	s.workerDone = done
	s.workerMu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				if err := runSweep(); err != nil {
					slog.Error("scheduled test evaluator failed", "err", err)
				}
				// Prefer cancellation after a sweep completes. Without this check a
				// queued tick could win the next select and begin another sweep while
				// Shutdown is already waiting.
				if workerCtx.Err() != nil {
					return
				}
			}
		}
	}()
	return nil
}

// Shutdown cancels the periodic evaluator and bounds the wait using ctx. A
// schedule sweep may be in durable storage or execution work that is not
// cancellable; callers receive ctx.Err instead of waiting indefinitely, and a
// later call can still observe the worker's eventual completion.
func (s *scenarioScheduler) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	done := s.stop()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stop requests worker cancellation without waiting. Server shutdown uses this
// before draining HTTP so a slow in-flight sweep cannot delay listener closure;
// Shutdown remains the sole bounded wait operation.
func (s *scenarioScheduler) stop() <-chan struct{} {
	if s == nil {
		return nil
	}
	s.workerMu.Lock()
	s.workerStopping = true
	cancel := s.workerCancel
	done := s.workerDone
	s.workerMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return done
}

func (s *scenarioScheduler) RunDueSchedules() (runErr error) {
	if s == nil || s.runner == nil {
		return nil
	}
	sweepAt := s.now().UTC()
	// Followers are healthy standbys, but they did not perform a sweep. Do not
	// overwrite the last leader sweep with a misleading successful timestamp;
	// only advance the fixed-size readiness due-time index.
	if !s.runner.isExecutionLeader() {
		if s.runner.leadership != nil {
			if err := s.runner.refreshSchedulerContentForHASweep(); err != nil {
				return fmt.Errorf("refresh standby scheduler content for readiness: %w", err)
			}
		}
		s.runner.mu.Lock()
		s.runner.advanceSchedulerReadinessLocked(sweepAt)
		s.runner.publishRuntimeReadinessLocked(time.Now().UTC())
		s.runner.mu.Unlock()
		return nil
	}
	type domainSweepState struct {
		attempted bool
		completed bool
		err       error
	}
	var scenarioSweep, oneTimeSweep, executionPlanSweep domainSweepState
	sweepAcquired := false
	defer func() {
		// A failed preflight belongs to the global scheduler health only: none of
		// the three domains was attempted. Failure to acquire the secondary lease
		// is normal standby contention and must not overwrite leader health.
		if !sweepAcquired {
			if runErr != nil {
				s.recordSweepHealth("scheduler", sweepAt, runErr)
			}
			return
		}
		recordDomain := func(name string, state domainSweepState) error {
			if !state.attempted {
				return nil
			}
			domainErr := state.err
			if !state.completed {
				incompleteErr := runErr
				if incompleteErr == nil {
					incompleteErr = fmt.Errorf("%s scheduler sweep did not complete", name)
				}
				domainErr = errors.Join(domainErr, incompleteErr)
			}
			s.recordSweepHealth(name, sweepAt, domainErr)
			return domainErr
		}
		scenarioErr := recordDomain("scenario", scenarioSweep)
		oneTimeErr := recordDomain("one_time", oneTimeSweep)
		executionPlanErr := recordDomain("execution_plan", executionPlanSweep)
		s.recordSweepHealth("scheduler", sweepAt, errors.Join(runErr, scenarioErr, oneTimeErr, executionPlanErr))
	}()
	// Materializing a due schedule starts a run, so scheduling is leader-only.
	// This keeps scheduling on the same replica that executes; the dedicated
	// scenario-scheduler lease below still guards against duplicate sweeps.
	leaseTerm, err := s.runner.captureExecutionLeaseTerm()
	if err != nil {
		return err
	}
	// Schedule/scenario CRUD may be served by any HA replica. Refresh the
	// leader's durable view before each sweep so follower writes and deletes are
	// observed without requiring another leadership transition.
	if s.runner.leadership != nil {
		if err := s.runner.refreshSchedulerContentForHASweep(); err != nil {
			return fmt.Errorf("refresh scheduler content before schedule sweep: %w", err)
		}
	}
	if !s.runner.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	now := s.now().UTC()
	acquired, err := s.runner.acquireScenarioSchedulerLease(now, s.ownerID, s.leaseTTL)
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}
	sweepAcquired = true
	if !s.runner.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	toStart := make([]*ManagedTest, 0)
	scenarioSweep.attempted = true
	s.runner.mu.Lock()
	s.runner.advanceSchedulerReadinessLocked(now)
	processedOccurrences := 0
	scheduleSweepExhausted := false
	schedules := s.runner.listAllScenarioSchedulesLocked()
	for _, currentSchedule := range schedules {
		if !s.runner.executionLeaseTermCurrent(leaseTerm) {
			scenarioSweep.err = errors.Join(scenarioSweep.err, errNotExecutionLeader)
			scheduleSweepExhausted = true
			break
		}
		schedule := cloneScenarioSchedule(currentSchedule)
		if schedule == nil || !schedule.Enabled || schedule.NextRunAt == nil || schedule.NextRunAt.After(now) {
			continue
		}
		catchUpAll := normalizeScenarioScheduleMissedRunPolicy(schedule.MissedRunPolicy) == "catch_up_all"
		for materialized := 0; schedule.NextRunAt != nil && !schedule.NextRunAt.After(now); materialized++ {
			if processedOccurrences >= maxScheduledOccurrencesPerSweep {
				scheduleSweepExhausted = true
				break
			}
			processedOccurrences++
			if materialized >= maxScheduleCatchUpAllPerSweep {
				schedule.NextRequiredAction = "catch_up_limit_reached"
				schedule.LastError = fmt.Sprintf("catch_up_all materialized %d occurrences; next sweep will continue", maxScheduleCatchUpAllPerSweep)
				schedule.UpdatedAt = now
				if err := s.runner.saveScenarioScheduleForSweepLocked(schedule, leaseTerm); err != nil {
					slog.Warn("failed to persist scenario catch-up limit", "schedule_id", schedule.ID, "error", err)
				}
				break
			}
			plannedAt := schedule.NextRunAt.UTC()
			var existing *storage.ScenarioScheduleOccurrence
			var loadErr error
			if s.runner.scenarioStore != nil {
				s.runner.mu.Unlock()
				existing, loadErr = s.runner.scenarioStore.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt)
				s.runner.mu.Lock()
			}
			if loadErr != nil {
				schedule.LastError = loadErr.Error()
				scenarioSweep.err = errors.Join(scenarioSweep.err, loadErr)
				break
			}
			// A durable occurrence always wins over missed-run policy. It may
			// represent a crash between the occurrence/test/schedule commits and
			// must be reconciled before skip/operator policy considers a new slot.
			if existing == nil {
				if handled, err := s.runner.applyMissedRunPolicyLocked(schedule, now, leaseTerm); err != nil {
					scenarioSweep.err = errors.Join(scenarioSweep.err, err)
					schedule.LastError = err.Error()
					schedule.UpdatedAt = now
					if saveErr := s.runner.saveScenarioScheduleForSweepLocked(schedule, leaseTerm); saveErr != nil {
						slog.Warn("failed to persist scenario schedule error", "schedule_id", schedule.ID, "error", saveErr)
					}
					break
				} else if handled {
					break
				}
			}
			if !s.runner.executionLeaseTermCurrent(leaseTerm) {
				scenarioSweep.err = errors.Join(scenarioSweep.err, errNotExecutionLeader)
				scheduleSweepExhausted = true
				break
			}
			test, err := s.runner.runScenarioScheduleLocked(schedule, now, leaseTerm)
			if err != nil {
				scenarioSweep.err = errors.Join(scenarioSweep.err, err)
				schedule.LastError = err.Error()
				schedule.UpdatedAt = now
				if saveErr := s.runner.saveScenarioScheduleForSweepLocked(schedule, leaseTerm); saveErr != nil {
					slog.Warn("failed to persist scenario materialization error", "schedule_id", schedule.ID, "error", saveErr)
				}
				break
			}
			if test != nil {
				toStart = append(toStart, test)
			}
			if !catchUpAll || schedule.NextRunAt == nil || !schedule.NextRunAt.After(plannedAt) {
				break
			}
		}
		if scheduleSweepExhausted {
			break
		}
	}
	dueOneTimeScheduled := s.dueOneTimeScheduledTestIDsLocked(now, maxOneTimeScheduledStartsPerSweep)
	// Schedule updates above operate on clones, so every successful fenced
	// installer applied its old->new counter/heap delta. Publish the combined
	// result once instead of rescanning the full catalog or allocating per row.
	s.runner.publishRuntimeReadinessLocked(time.Now().UTC())
	s.runner.mu.Unlock()
	if !s.runner.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	for _, test := range toStart {
		go s.runner.runScenarioScheduledTestWithTerm(test, leaseTerm)
	}
	scenarioSweep.completed = true
	oneTimeSweep.attempted = true
	for _, testID := range dueOneTimeScheduled {
		if !s.runner.executionLeaseTermCurrent(leaseTerm) {
			oneTimeSweep.err = errors.Join(oneTimeSweep.err, errNotExecutionLeader)
			return errNotExecutionLeader
		}
		if err := s.runner.startTestWithOptionsAndExpectedTerm(testID, StartTestOptions{}, &leaseTerm); err != nil {
			oneTimeSweep.err = errors.Join(oneTimeSweep.err, err)
			s.runner.markScheduledStartError(testID, err.Error())
		}
	}
	if !s.runner.executionLeaseTermCurrent(leaseTerm) {
		oneTimeSweep.err = errors.Join(oneTimeSweep.err, errNotExecutionLeader)
		return errNotExecutionLeader
	}
	oneTimeSweep.completed = true
	// Recurring execution-plan schedules share this leader-gated sweep + lease.
	// Run lock-free (materialize takes tr.mu internally).
	if !s.runner.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	executionPlanSweep.attempted = true
	executionPlanSweep.err = s.runner.sweepDueExecutionPlanSchedulesWithTerm(now, leaseTerm)
	executionPlanSweep.completed = !errors.Is(executionPlanSweep.err, errNotExecutionLeader)
	return nil
}

func (s *scenarioScheduler) recordSweepHealth(name string, at time.Time, err error) {
	if s == nil {
		return
	}
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	state := s.sweepHealth[name]
	state.LastSweepAt = cloneTimePtr(&at)
	if err == nil {
		state.LastSuccessfulAt = cloneTimePtr(&at)
		state.LastError = ""
	} else {
		state.LastError = err.Error()
	}
	s.sweepHealth[name] = state
}

func (s *scenarioScheduler) healthSnapshot() map[string]SchedulerSweepHealth {
	if s == nil {
		return nil
	}
	s.healthMu.RLock()
	defer s.healthMu.RUnlock()
	out := make(map[string]SchedulerSweepHealth, len(s.sweepHealth))
	for name, state := range s.sweepHealth {
		state.LastSweepAt = cloneTimePtr(state.LastSweepAt)
		state.LastSuccessfulAt = cloneTimePtr(state.LastSuccessfulAt)
		out[name] = state
	}
	return out
}

func (s *scenarioScheduler) dueOneTimeScheduledTestIDsLocked(now time.Time, limit int) []string {
	ids := s.runner.dueOneTimeScheduledTestIDsLocked(now, 0)
	if limit <= 0 || len(ids) <= limit {
		if len(ids) > 0 {
			s.oneTimeCursor = ids[len(ids)-1]
		}
		return ids
	}
	start := 0
	if s.oneTimeCursor != "" {
		for index, id := range ids {
			if id == s.oneTimeCursor {
				start = (index + 1) % len(ids)
				break
			}
		}
	}
	selected := make([]string, 0, limit)
	for offset := 0; offset < limit; offset++ {
		selected = append(selected, ids[(start+offset)%len(ids)])
	}
	s.oneTimeCursor = selected[len(selected)-1]
	return selected
}

func (tr *TestRunner) applyMissedRunPolicyLocked(schedule *storage.ScenarioSchedule, now time.Time, leaseTerm executionLeaseTerm) (bool, error) {
	if schedule == nil || schedule.NextRunAt == nil || now.Sub(*schedule.NextRunAt) <= time.Minute {
		return false, nil
	}
	switch normalizeScenarioScheduleMissedRunPolicy(schedule.MissedRunPolicy) {
	case "catch_up_once", "catch_up_all":
		return false, nil
	case "skip":
		occurrence := tr.buildScenarioScheduleOccurrence(schedule, *schedule.NextRunAt, "skipped", "missed_run_skipped", "Missed scheduled occurrence skipped by policy.", nil, now)
		if err := tr.saveScenarioScheduleOccurrenceForSweepLocked(occurrence, leaseTerm); err != nil {
			return true, err
		}
		next, err := nextScenarioScheduleRun(schedule, now)
		if err != nil {
			return true, err
		}
		schedule.NextRunAt = next
		schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrence)
		schedule.LastError = ""
		schedule.UpdatedAt = now
		if err := tr.saveScenarioScheduleForSweepLocked(schedule, leaseTerm); err != nil {
			return true, err
		}
		return true, nil
	case "require_operator_decision":
		const summary = "Missed scheduled occurrence requires operator decision."
		if err := tr.blockScheduleOccurrenceLocked(schedule, *schedule.NextRunAt, "missed_run_requires_decision", summary, "operator_decision_required", summary, now, nil, leaseTerm); err != nil {
			return true, err
		}
		return true, nil
	default:
		return false, nil
	}
}

func (tr *TestRunner) acquireScenarioSchedulerLease(now time.Time, ownerID string, ttl time.Duration) (bool, error) {
	if tr == nil || tr.scenarioStore == nil {
		return true, nil
	}
	return tr.scenarioStore.TryAcquireSchedulerLease("scenario-scheduler", ownerID, now, ttl)
}

func nextScenarioScheduleRun(schedule *storage.ScenarioSchedule, from time.Time) (*time.Time, error) {
	if schedule == nil {
		return nil, fmt.Errorf("schedule is required")
	}
	return nextCronRun(schedule.CronExpr, schedule.Timezone, from, func(candidate time.Time) (bool, error) {
		return scheduleAllowsCandidate(schedule, candidate)
	})
}

// nextCronRun is the shared cron evaluator behind both scenario schedules and
// execution-plan schedules. It finds the first UTC instant strictly after `from`
// whose cron fields match in the declared timezone and that `allows` accepts
// (window/blackout/pause gating). `allows` may be nil (no extra gating).
func nextCronRun(cronExpr, timezone string, from time.Time, allows func(candidate time.Time) (bool, error)) (*time.Time, error) {
	from = from.UTC()
	if err := validateCronExpr(cronExpr); err != nil {
		return nil, err
	}
	fields := strings.Fields(expandCronMacro(cronExpr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields")
	}
	// Resolve 3-letter month/day-of-week aliases (and dow 7=Sunday) to numbers up
	// front so the rest of the matcher stays purely numeric.
	fields[3] = applyCronAliases(fields[3], cronMonthAliases)
	fields[4] = applyCronDowAliases(fields[4])
	location := scheduleEvalLocation(timezone)
	previousLocal := from.In(location)
	// Search far enough ahead to find a Feb-29 occurrence, whose gap between valid
	// dates is up to ~4 years (a leap-day schedule like "0 0 29 2 *"). A 1-year
	// window wrongly rejected those; this covers a full leap cycle plus margin.
	limit := from.Add(maxCronSearchHorizon)
	if !cronHasPossibleCalendarDate(fields[2], fields[3], fields[4], from, limit, location) {
		return nil, fmt.Errorf("could not find a matching run within %d years — check for an impossible day/month combination (e.g. Feb 30, or Feb 29 outside a leap year)", maxCronSearchHorizonYears)
	}
	evaluatedCandidates := 0
	for candidate := from.Truncate(time.Minute).Add(time.Minute); !candidate.After(limit); {
		// Match cron fields in the schedule's declared timezone so a schedule
		// fires at its local wall-clock time; iteration and the returned
		// next_run_at stay in UTC. During a DST spring-forward the skipped local
		// hour simply never matches (the desired skip); during fall-back the
		// earliest matching UTC instant wins.
		local := candidate.In(location)
		// A fall-back transition makes local wall time move backwards even though
		// UTC keeps advancing. Never return a candidate on the same local date whose
		// wall-clock minute is at or before the caller's local minute: it belongs to
		// the repeated portion of the day and has already had an earlier UTC
		// representation. This also covers a sweep that reconciles several minutes
		// after the first occurrence rather than advancing from plannedAt exactly.
		if localCalendarMinuteNotAfter(local, previousLocal) {
			candidate = candidate.Add(time.Minute)
			continue
		}
		if !cronFieldMatches(fields[3], int(local.Month()), 1, 12) || !cronMatchesDayOfMonthAndWeek(fields[2], fields[4], local) {
			candidate = nextCronLocalDateBoundary(candidate, local, location)
			continue
		}
		if !cronFieldMatches(fields[1], local.Hour(), 0, 23) {
			// Re-evaluate on each local-hour boundary rather than walking every
			// minute. Advancing in UTC keeps repeated/skipped DST hours correct.
			candidate = candidate.Add(time.Duration(60-local.Minute()) * time.Minute)
			continue
		}
		if !cronFieldMatches(fields[0], local.Minute(), 0, 59) {
			candidate = candidate.Add(time.Duration(nextCronMinuteDelta(fields[0], local.Minute())) * time.Minute)
			continue
		}
		// The caller can enter this evaluator after a restart during the second
		// side of a fall-back fold. In that case previousLocal is itself in the
		// repeated hour and cannot prove that the candidate's wall-clock minute
		// already had an earlier UTC representation. Inspect the candidate's zone
		// transition directly and always select the first instant for an ambiguous
		// local minute.
		if localCalendarMinuteHasEarlierInstant(candidate, location) {
			candidate = candidate.Add(time.Minute)
			continue
		}
		evaluatedCandidates++
		if evaluatedCandidates > maxCronEligibleCandidateEvaluations {
			return nil, fmt.Errorf("cron eligibility filters rejected more than %d matching candidates; narrow the cron expression or schedule windows", maxCronEligibleCandidateEvaluations)
		}
		if allows != nil {
			if allowed, err := allows(candidate); err != nil {
				return nil, err
			} else if !allowed {
				candidate = candidate.Add(time.Minute)
				continue
			}
		}
		next := candidate.UTC()
		return &next, nil
	}
	return nil, fmt.Errorf("could not find a matching run within %d years — check for an impossible day/month combination (e.g. Feb 30, or Feb 29 outside a leap year)", maxCronSearchHorizonYears)
}

func sameLocalCalendarMinute(left, right time.Time) bool {
	return left.Year() == right.Year() && left.Month() == right.Month() && left.Day() == right.Day() &&
		left.Hour() == right.Hour() && left.Minute() == right.Minute()
}

func localCalendarMinuteNotAfter(candidate, previous time.Time) bool {
	if candidate.Year() != previous.Year() || candidate.Month() != previous.Month() || candidate.Day() != previous.Day() {
		return false
	}
	return candidate.Hour() < previous.Hour() ||
		(candidate.Hour() == previous.Hour() && candidate.Minute() <= previous.Minute())
}

func localCalendarMinuteHasEarlierInstant(candidate time.Time, location *time.Location) bool {
	if location == nil {
		location = time.UTC
	}
	local := candidate.In(location)
	zoneStart, _ := local.ZoneBounds()
	if zoneStart.IsZero() || candidate.Before(zoneStart) {
		return false
	}
	_, currentOffset := local.Zone()
	_, previousOffset := zoneStart.Add(-time.Nanosecond).In(location).Zone()
	// Only an offset decrease repeats local wall time. The difference between
	// offsets is exactly the distance to the candidate's earlier representation.
	if previousOffset <= currentOffset {
		return false
	}
	earlier := candidate.Add(-time.Duration(previousOffset-currentOffset) * time.Second)
	return earlier.Before(candidate) && sameLocalCalendarMinute(earlier.In(location), local)
}

// nextCronLocalDateBoundary skips a calendar date that cannot satisfy the
// month/day fields. Constructing the following local midnight avoids the
// previous 1,440 checks per rejected day while preserving timezone/DST rules.
func nextCronLocalDateBoundary(candidate time.Time, local time.Time, location *time.Location) time.Time {
	next := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, location).UTC()
	if next.After(candidate) {
		return next
	}
	// Defensive fallback for an exotic timezone transition normalized across
	// midnight in an unexpected direction. Always make forward progress.
	return candidate.Add(time.Hour)
}

func nextCronMinuteDelta(expr string, minute int) int {
	for delta := 1; delta <= 60; delta++ {
		if cronFieldMatches(expr, (minute+delta)%60, 0, 59) {
			return delta
		}
	}
	return 1
}

func cronHasPossibleCalendarDate(domField, monthField, dowField string, from time.Time, limit time.Time, location *time.Location) bool {
	dowField = strings.TrimSpace(dowField)
	if dowField != "" && dowField != "*" && dowField != "?" {
		return true
	}
	domField = strings.ToUpper(strings.TrimSpace(domField))
	if domField == "" || domField == "*" || domField == "?" || strings.ContainsAny(domField, "LW#") {
		return true
	}
	startYear := from.In(location).Year()
	endYear := limit.In(location).Year()
	for year := startYear; year <= endYear; year++ {
		for month := 1; month <= 12; month++ {
			if !cronFieldMatches(monthField, month, 1, 12) {
				continue
			}
			for day := 1; day <= 31; day++ {
				if !cronFieldMatches(domField, day, 1, 31) {
					continue
				}
				candidate := time.Date(year, time.Month(month), day, 12, 0, 0, 0, location)
				if candidate.Month() != time.Month(month) || candidate.Day() != day {
					continue
				}
				if candidate.Before(from.In(location)) || candidate.After(limit.In(location)) {
					continue
				}
				return true
			}
		}
	}
	return false
}

const (
	// maxCronSearchHorizonYears bounds the minute-by-minute next-run search. It
	// must exceed the longest gap between valid occurrences of any legal cron — a
	// Feb-29 schedule recurs only every ~4 years — so legitimate leap-day
	// schedules are not wrongly rejected.
	maxCronSearchHorizonYears = 5
	maxCronSearchHorizon      = maxCronSearchHorizonYears * 366 * 24 * time.Hour
)

var cronFieldNames = [5]string{"minute", "hour", "day-of-month", "month", "day-of-week"}

// day-of-week upper bound is 7 because both 0 and 7 denote Sunday (POSIX/Quartz).
var cronFieldBounds = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}

var cronMonthAliases = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var cronDowAliases = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

var cronAlphaToken = regexp.MustCompile(`[A-Za-z]+`)

// applyCronAliases replaces 3-letter month/day-of-week alias tokens (case
// insensitive) with their numeric equivalents, leaving every other token —
// including the single-letter L/W specifiers — untouched. Run before the numeric
// matcher so ranges/lists/steps over aliases (MON-FRI, JAN,MAR) work for free.
func applyCronAliases(field string, aliases map[string]int) string {
	return cronAlphaToken.ReplaceAllStringFunc(field, func(token string) string {
		if n, ok := aliases[strings.ToLower(token)]; ok {
			return strconv.Itoa(n)
		}
		return token
	})
}

func applyCronDowAliases(field string) string {
	return cronAlphaToken.ReplaceAllStringFunc(field, func(token string) string {
		lower := strings.ToLower(token)
		if n, ok := cronDowAliases[lower]; ok {
			return strconv.Itoa(n)
		}
		if strings.HasSuffix(lower, "l") {
			if n, ok := cronDowAliases[strings.TrimSuffix(lower, "l")]; ok {
				return strconv.Itoa(n) + "L"
			}
		}
		return token
	})
}

// expandCronMacro rewrites a named macro (@daily, @weekly, …) into its 5-field
// equivalent. Non-macro expressions pass through unchanged; the raw form is what
// is persisted, so a schedule keeps showing the macro the operator entered.
func expandCronMacro(expr string) string {
	switch strings.ToLower(strings.TrimSpace(expr)) {
	case "@yearly", "@annually":
		return "0 0 1 1 *"
	case "@monthly":
		return "0 0 1 * *"
	case "@weekly":
		return "0 0 * * 0"
	case "@daily", "@midnight":
		return "0 0 * * *"
	case "@hourly":
		return "0 * * * *"
	default:
		return expr
	}
}

// validateCronExpr checks a 5-field cron expression and returns a specific error
// naming the offending field/token, so a rejected expression (e.g. an imported
// LoadRunner schedule using L/W/#) fails with an actionable message instead of
// the generic "could not find next run within one year".
func validateCronExpr(expr string) error {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return fmt.Errorf("cron expression is required")
	}
	if len(expr) > maxCronExpressionBytes {
		return fmt.Errorf("cron expression exceeds the %d-byte limit", maxCronExpressionBytes)
	}
	fields := strings.Fields(expandCronMacro(expr))
	if len(fields) != 5 {
		return fmt.Errorf("cron expression must have 5 space-separated fields (minute hour day-of-month month day-of-week), got %d", len(fields))
	}
	fields[3] = applyCronAliases(fields[3], cronMonthAliases)
	fields[4] = applyCronDowAliases(fields[4])
	for i, field := range fields {
		min, max := cronFieldBounds[i][0], cronFieldBounds[i][1]
		for _, part := range strings.Split(field, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				return fmt.Errorf("empty term in cron %s field %q", cronFieldNames[i], field)
			}
			if err := validateCronPart(part, min, max, cronFieldNames[i]); err != nil {
				return err
			}
		}
	}
	if err := validateCronDayMonthFeasible(fields[2], fields[3]); err != nil {
		return err
	}
	return nil
}

// singleCronNumber returns the value of a field that is a single fixed integer
// (no list/range/step/wildcard/specifier), else ok=false.
func singleCronNumber(field string) (int, bool) {
	field = strings.TrimSpace(field)
	if field == "" || strings.ContainsAny(field, ",-/*?") || strings.ContainsAny(strings.ToUpper(field), "LW#") {
		return 0, false
	}
	return parseCronNumber(field, 0, 9999)
}

// validateCronDayMonthFeasible rejects the common hand-typed impossible date
// (e.g. "31 2" → Feb 31, "31 4" → Apr 31) when both day-of-month and month are
// fixed singletons. February allows day 29 so leap-day schedules stay valid.
// Anything more complex (lists/ranges/steps/specifiers) defers to the search.
func validateCronDayMonthFeasible(domField, monthField string) error {
	day, ok := singleCronNumber(domField)
	if !ok {
		return nil
	}
	month, ok := singleCronNumber(monthField)
	if !ok || month < 1 || month > 12 {
		return nil
	}
	maxDays := [12]int{31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if day > maxDays[month-1] {
		return fmt.Errorf("impossible date in cron: day %d does not occur in month %d", day, month)
	}
	return nil
}

func validateCronPart(part string, min, max int, fieldName string) error {
	base := part
	hasStep := false
	if slash := strings.Index(part, "/"); slash >= 0 {
		hasStep = true
		base = part[:slash]
		if base == "" {
			base = "*"
		}
		if _, ok := parseCronNumber(part[slash+1:], 1, max-min+1); !ok {
			return fmt.Errorf("invalid step in cron %s field %q: step must be a positive number", fieldName, part)
		}
	}
	if base == "*" || base == "?" {
		return nil
	}
	if fieldName == "day-of-month" {
		if handled, err := validateDomSpecialToken(base, part, fieldName); handled {
			if err == nil && hasStep {
				return fmt.Errorf("steps are not supported on special cron %s token %q", fieldName, part)
			}
			return err
		}
	}
	if fieldName == "day-of-week" {
		if handled, err := validateDowSpecialToken(base, part, fieldName); handled {
			if err == nil && hasStep {
				return fmt.Errorf("steps are not supported on special cron %s token %q", fieldName, part)
			}
			return err
		}
	}
	if strings.ContainsAny(base, "LW#lw") {
		return fmt.Errorf("unsupported cron token in %s field %q: L/W/# are only valid in the day-of-month (L, LW, L-3, 15W) and day-of-week (5L, 5#3) fields", fieldName, part)
	}
	if dash := strings.Index(base, "-"); dash >= 0 {
		start, ok := parseCronNumber(base[:dash], min, max)
		if !ok {
			return fmt.Errorf("invalid range start in cron %s field %q (allowed %d-%d)", fieldName, part, min, max)
		}
		end, ok := parseCronNumber(base[dash+1:], min, max)
		if !ok {
			return fmt.Errorf("invalid range end in cron %s field %q (allowed %d-%d)", fieldName, part, min, max)
		}
		if end < start {
			return fmt.Errorf("range start %d is after end %d in cron %s field %q", start, end, fieldName, part)
		}
		return nil
	}
	if _, ok := parseCronNumber(base, min, max); !ok {
		return fmt.Errorf("invalid value in cron %s field %q (allowed %d-%d, or *, ?, ranges, steps, lists)", fieldName, part, min, max)
	}
	return nil
}

// validateDomSpecialToken validates a day-of-month special token (L, LW, L-n,
// dW). It returns handled=false for non-special tokens so normal numeric
// validation proceeds.
func validateDomSpecialToken(base, part, fieldName string) (bool, error) {
	upper := strings.ToUpper(base)
	switch {
	case upper == "L" || upper == "LW":
		return true, nil
	case strings.HasPrefix(upper, "L-"):
		if _, ok := parseCronNumber(upper[2:], 1, 30); !ok {
			return true, fmt.Errorf("invalid offset in cron %s field %q: use L-1 .. L-30", fieldName, part)
		}
		return true, nil
	case strings.HasSuffix(upper, "W"):
		if _, ok := parseCronNumber(upper[:len(upper)-1], 1, 31); !ok {
			return true, fmt.Errorf("invalid nearest-weekday day in cron %s field %q: use 1W .. 31W", fieldName, part)
		}
		return true, nil
	}
	return false, nil
}

// validateDowSpecialToken validates a day-of-week special token (dL last weekday,
// d#k nth weekday). Weekday accepts 0..7 (0 and 7 both Sunday).
func validateDowSpecialToken(base, part, fieldName string) (bool, error) {
	upper := strings.ToUpper(base)
	if upper == "L" {
		return true, nil
	}
	if hash := strings.Index(upper, "#"); hash >= 0 {
		if _, ok := parseCronNumber(upper[:hash], 0, 7); !ok {
			return true, fmt.Errorf("invalid weekday in cron %s field %q: use 0#n .. 7#n (0 or 7 = Sunday)", fieldName, part)
		}
		if _, ok := parseCronNumber(upper[hash+1:], 1, 5); !ok {
			return true, fmt.Errorf("invalid occurrence in cron %s field %q: use d#1 .. d#5", fieldName, part)
		}
		return true, nil
	}
	if strings.HasSuffix(upper, "L") {
		if _, ok := parseCronNumber(upper[:len(upper)-1], 0, 7); !ok {
			return true, fmt.Errorf("invalid weekday in cron %s field %q: use 0L .. 7L (last weekday of month)", fieldName, part)
		}
		return true, nil
	}
	return false, nil
}

func cronFieldMatches(expr string, value int, min int, max int) bool {
	for _, part := range strings.Split(expr, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if cronPartMatches(part, value, min, max) {
			return true
		}
	}
	return false
}

func cronPartMatches(part string, value int, min int, max int) bool {
	step := 1
	base := part
	hasStep := false
	if slash := strings.Index(part, "/"); slash >= 0 {
		hasStep = true
		base = part[:slash]
		if base == "" {
			base = "*"
		}
		parsedStep, ok := parseCronNumber(part[slash+1:], 1, max-min+1)
		if !ok {
			return false
		}
		step = parsedStep
	}
	if base == "?" {
		// "?" ("no specific value", used in day-of-month/day-of-week) is treated
		// as a wildcard, matching the standard Quartz convention.
		base = "*"
	}
	if base == "*" {
		return (value-min)%step == 0
	}
	if dash := strings.Index(base, "-"); dash >= 0 {
		start, ok := parseCronNumber(base[:dash], min, max)
		if !ok {
			return false
		}
		end, ok := parseCronNumber(base[dash+1:], min, max)
		if !ok || end < start {
			return false
		}
		if value < start || value > end {
			return false
		}
		return (value-start)%step == 0
	}
	number, ok := parseCronNumber(base, min, max)
	if !ok {
		return false
	}
	if hasStep {
		return value >= number && (value-number)%step == 0
	}
	return value == number
}

func parseCronNumber(raw string, min int, max int) (int, bool) {
	if strings.TrimSpace(raw) == "" {
		return 0, false
	}
	value := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		digit := int(ch - '0')
		// All cron fields have a small positive maximum. Stop before integer
		// overflow (or needlessly scanning a megabyte-long numeric token).
		if value > (max-digit)/10 {
			return 0, false
		}
		value = value*10 + digit
	}
	if value < min || value > max {
		return 0, false
	}
	return value, true
}

// scheduleEvalLocation resolves the timezone in which a schedule's cron fields
// are evaluated. An empty or unparseable timezone falls back to UTC so a
// mis-typed zone degrades to the previous (UTC) behavior rather than failing the
// schedule. next_run_at is always stored in UTC regardless of this location.
func scheduleEvalLocation(timezone string) *time.Location {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		return time.UTC
	}
	location, err := cachedScheduleLocation(timezone)
	if err != nil {
		return time.UTC
	}
	return location
}

var scheduleLocationCache sync.Map // map[string]*time.Location

func cachedScheduleLocation(timezone string) (*time.Location, error) {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" || timezone == "UTC" {
		return time.UTC, nil
	}
	if cached, ok := scheduleLocationCache.Load(timezone); ok {
		return cached.(*time.Location), nil
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, err
	}
	actual, _ := scheduleLocationCache.LoadOrStore(timezone, location)
	return actual.(*time.Location), nil
}

func validateScheduleTimezone(timezone string) error {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		return fmt.Errorf("schedule timezone is required")
	}
	if len(timezone) > 253 {
		return fmt.Errorf("schedule timezone exceeds the 253-byte limit")
	}
	if _, err := cachedScheduleLocation(timezone); err != nil {
		return fmt.Errorf("invalid schedule timezone %q: %w", timezone, err)
	}
	return nil
}

func validateScenarioScheduleWindowSets(blackout, maintenance, availability []storage.ScenarioScheduleWindow, fallbackTimezone string) error {
	total := len(blackout) + len(maintenance) + len(availability)
	if total > maxScenarioScheduleWindows {
		return fmt.Errorf("schedule contains %d windows, limit is %d", total, maxScenarioScheduleWindows)
	}
	sets := []struct {
		kind    string
		windows []storage.ScenarioScheduleWindow
	}{
		{kind: "blackout", windows: blackout},
		{kind: "maintenance", windows: maintenance},
		{kind: "availability", windows: availability},
	}
	for _, set := range sets {
		for index, window := range set.windows {
			prefix := fmt.Sprintf("%s window %d", set.kind, index)
			if len(window.Name) > maxScenarioScheduleWindowNameBytes {
				return fmt.Errorf("%s name exceeds the %d-byte limit", prefix, maxScenarioScheduleWindowNameBytes)
			}
			if _, err := parseScheduleWindowMinute(window.StartTime); err != nil {
				return fmt.Errorf("%s start_time: %w", prefix, err)
			}
			if _, err := parseScheduleWindowMinute(window.EndTime); err != nil {
				return fmt.Errorf("%s end_time: %w", prefix, err)
			}
			timezone := firstNonEmpty(strings.TrimSpace(window.Timezone), fallbackTimezone)
			if err := validateScheduleTimezone(timezone); err != nil {
				return fmt.Errorf("%s: %w", prefix, err)
			}
			if len(window.DaysOfWeek) > 7 {
				return fmt.Errorf("%s days_of_week contains %d entries, limit is 7", prefix, len(window.DaysOfWeek))
			}
			for _, day := range window.DaysOfWeek {
				if day < 0 || day > 6 {
					return fmt.Errorf("%s days_of_week contains %d; allowed values are 0 through 6", prefix, day)
				}
			}
		}
	}
	return nil
}

// cronMatchesDayOfMonthAndWeek applies POSIX cron semantics for the
// day-of-month (field 3) and day-of-week (field 5) fields: when BOTH are
// restricted (neither is "*" nor "?"), a candidate matches if EITHER field
// matches; otherwise both must match (and an unrestricted field always matches,
// so this reduces to the single restricted field). This fixes the prior AND-only
// behavior where "0 9 15 * 1" only fired on a Monday that was also the 15th.
// Both fields additionally honor the Quartz day specifiers (L/LW/L-n/dW in
// day-of-month; dL/d#k in day-of-week).
func cronMatchesDayOfMonthAndWeek(domField, dowField string, t time.Time) bool {
	domField = strings.TrimSpace(domField)
	dowField = strings.TrimSpace(dowField)
	domRestricted := domField != "*" && domField != "?" && domField != ""
	dowRestricted := dowField != "*" && dowField != "?" && dowField != ""
	domMatch := cronDomMatches(domField, t)
	dowMatch := cronDowMatches(dowField, t)
	if domRestricted && dowRestricted {
		return domMatch || dowMatch
	}
	return domMatch && dowMatch
}

func cronDomMatches(field string, t time.Time) bool {
	field = strings.TrimSpace(field)
	if field == "*" || field == "?" || field == "" {
		return true
	}
	for _, part := range strings.Split(field, ",") {
		if cronDomPartMatches(strings.TrimSpace(part), t) {
			return true
		}
	}
	return false
}

func cronDomPartMatches(part string, t time.Time) bool {
	if part == "" {
		return false
	}
	upper := strings.ToUpper(part)
	last := daysInMonth(t.Year(), t.Month(), t.Location())
	switch {
	case upper == "L":
		return t.Day() == last
	case upper == "LW":
		return t.Day() == nearestWeekday(t.Year(), t.Month(), last, t.Location())
	case strings.HasPrefix(upper, "L-"):
		n, ok := parseCronNumber(upper[2:], 1, 30)
		if !ok {
			return false
		}
		return t.Day() == last-n
	case strings.HasSuffix(upper, "W"):
		d, ok := parseCronNumber(upper[:len(upper)-1], 1, 31)
		if !ok {
			return false
		}
		return t.Day() == nearestWeekday(t.Year(), t.Month(), d, t.Location())
	default:
		return cronPartMatches(part, t.Day(), 1, 31)
	}
}

func cronDowMatches(field string, t time.Time) bool {
	field = strings.TrimSpace(field)
	if field == "*" || field == "?" || field == "" {
		return true
	}
	for _, part := range strings.Split(field, ",") {
		if cronDowPartMatches(strings.TrimSpace(part), t) {
			return true
		}
	}
	return false
}

func cronDowPartMatches(part string, t time.Time) bool {
	if part == "" {
		return false
	}
	weekday := int(t.Weekday()) // 0=Sunday .. 6=Saturday
	upper := strings.ToUpper(part)
	if upper == "L" {
		return weekday == int(time.Saturday) && t.Day()+7 > daysInMonth(t.Year(), t.Month(), t.Location())
	}
	if hash := strings.Index(upper, "#"); hash >= 0 {
		d, ok := parseCronNumber(upper[:hash], 0, 7)
		if !ok {
			return false
		}
		k, ok := parseCronNumber(upper[hash+1:], 1, 5)
		if !ok {
			return false
		}
		if normalizeCronDow(d) != weekday {
			return false
		}
		return (t.Day()-1)/7+1 == k
	}
	if strings.HasSuffix(upper, "L") {
		d, ok := parseCronNumber(upper[:len(upper)-1], 0, 7)
		if !ok {
			return false
		}
		if normalizeCronDow(d) != weekday {
			return false
		}
		return t.Day()+7 > daysInMonth(t.Year(), t.Month(), t.Location())
	}
	if cronPartMatches(part, weekday, 0, 7) {
		return true
	}
	// Sunday (weekday 0) also matches a literal 7, e.g. "7" or a range like "5-7".
	return weekday == 0 && cronPartMatches(part, 7, 0, 7)
}

func normalizeCronDow(d int) int {
	if d == 7 {
		return 0
	}
	return d
}

func daysInMonth(year int, month time.Month, loc *time.Location) int {
	firstOfNext := time.Date(year, month, 1, 0, 0, 0, 0, loc).AddDate(0, 1, 0)
	return firstOfNext.AddDate(0, 0, -1).Day()
}

// nearestWeekday returns the day-of-month of the weekday (Mon-Fri) nearest to
// `day`, never crossing the month boundary (Quartz "W" semantics: a Saturday
// shifts to the prior Friday unless that leaves the month, in which case it
// shifts to the following Monday, and vice versa for Sunday).
func nearestWeekday(year int, month time.Month, day int, loc *time.Location) int {
	last := daysInMonth(year, month, loc)
	if day > last {
		day = last
	}
	if day < 1 {
		day = 1
	}
	switch time.Date(year, month, day, 0, 0, 0, 0, loc).Weekday() {
	case time.Saturday:
		if day-1 >= 1 {
			return day - 1
		}
		return day + 2
	case time.Sunday:
		if day+1 <= last {
			return day + 1
		}
		return day - 2
	default:
		return day
	}
}

func scheduleAllowsCandidate(schedule *storage.ScenarioSchedule, candidate time.Time) (bool, error) {
	if scheduleDisabledAt(schedule, candidate) {
		return false, nil
	}
	for _, window := range schedule.BlackoutWindows {
		matches, err := scheduleWindowContains(window, schedule.Timezone, candidate)
		if err != nil {
			return false, err
		}
		if matches {
			return false, nil
		}
	}
	for _, window := range schedule.MaintenanceWindows {
		matches, err := scheduleWindowContains(window, schedule.Timezone, candidate)
		if err != nil {
			return false, err
		}
		if matches {
			return false, nil
		}
	}
	if len(schedule.AvailabilityWindows) == 0 {
		return true, nil
	}
	for _, window := range schedule.AvailabilityWindows {
		matches, err := scheduleWindowContains(window, schedule.Timezone, candidate)
		if err != nil {
			return false, err
		}
		if matches {
			return true, nil
		}
	}
	return false, nil
}

// normalizeSchedulePauseBound drops zero-value pause bounds to nil and stores the
// rest in UTC so date-range comparisons in scheduleDisabledAt are unambiguous.
func normalizeSchedulePauseBound(t *time.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// scheduleDisabledAt reports whether candidate falls inside the schedule's
// optional date-range pause [DisabledFrom, DisabledUntil). Either bound may be
// nil for an open-ended pause; with both nil the schedule is never paused.
func scheduleDisabledAt(schedule *storage.ScenarioSchedule, candidate time.Time) bool {
	if schedule == nil {
		return false
	}
	return dateRangePauseActive(schedule.DisabledFrom, schedule.DisabledUntil, candidate)
}

// dateRangePauseActive reports whether candidate falls inside the optional pause
// window [disabledFrom, disabledUntil) — either bound may be nil for an
// open-ended pause; both nil means never paused. Shared by scenario and
// execution-plan schedules.
func dateRangePauseActive(disabledFrom, disabledUntil *time.Time, candidate time.Time) bool {
	if disabledFrom == nil && disabledUntil == nil {
		return false
	}
	if disabledFrom != nil && candidate.Before(*disabledFrom) {
		return false
	}
	if disabledUntil != nil && !candidate.Before(*disabledUntil) {
		return false
	}
	return true
}

func scheduleWindowContains(window storage.ScenarioScheduleWindow, fallbackTimezone string, candidate time.Time) (bool, error) {
	timezone := firstNonEmpty(strings.TrimSpace(window.Timezone), strings.TrimSpace(fallbackTimezone))
	if timezone == "" {
		timezone = "UTC"
	}
	location, err := cachedScheduleLocation(timezone)
	if err != nil {
		return false, fmt.Errorf("invalid schedule window timezone %q: %w", timezone, err)
	}
	local := candidate.In(location)
	startMinute, err := parseScheduleWindowMinute(window.StartTime)
	if err != nil {
		return false, err
	}
	endMinute, err := parseScheduleWindowMinute(window.EndTime)
	if err != nil {
		return false, err
	}
	minuteOfDay := local.Hour()*60 + local.Minute()
	// DaysOfWeek identifies the local day on which the window starts. For an
	// overnight Monday 22:00-02:00 window, the Tuesday 00:00-02:00 tail still
	// belongs to Monday's window. Checking Tuesday directly would silently open
	// a blackout/maintenance window at midnight and could admit an unsafe run.
	windowStartDay := local.Weekday()
	if startMinute > endMinute && minuteOfDay < endMinute {
		windowStartDay = local.AddDate(0, 0, -1).Weekday()
	}
	if len(window.DaysOfWeek) > 0 && !scheduleWindowDayMatches(window.DaysOfWeek, int(windowStartDay)) {
		return false, nil
	}
	if startMinute == endMinute {
		return true, nil
	}
	if startMinute < endMinute {
		return minuteOfDay >= startMinute && minuteOfDay < endMinute, nil
	}
	return minuteOfDay >= startMinute || minuteOfDay < endMinute, nil
}

func scheduleWindowDayMatches(days []int, weekday int) bool {
	for _, day := range days {
		if day == weekday {
			return true
		}
	}
	return false
}

func parseScheduleWindowMinute(raw string) (int, error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid schedule window time %q: expected HH:MM", raw)
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}

func (tr *TestRunner) listAllScenarioSchedulesLocked() []*storage.ScenarioSchedule {
	out := make([]*storage.ScenarioSchedule, 0)
	for _, scenario := range tr.scenarios {
		for _, schedule := range scenario.Schedules {
			out = append(out, cloneScenarioSchedule(schedule))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i], out[j]
		if left == nil {
			return false
		}
		if right == nil {
			return true
		}
		if left.NextRunAt == nil {
			return false
		}
		if right.NextRunAt == nil {
			return true
		}
		if !left.NextRunAt.Equal(*right.NextRunAt) {
			return left.NextRunAt.Before(*right.NextRunAt)
		}
		return left.ID < right.ID
	})
	return out
}

// saveScenarioScheduleOccurrenceForSweepLocked keeps the scheduler's global
// state lock out of storage I/O. The caller owns tr.mu on entry and regains it
// before return; occurrence persistence itself does not consult runner maps.
func (tr *TestRunner) scenarioScheduleSweepTerm(terms []executionLeaseTerm) (executionLeaseTerm, error) {
	if len(terms) > 0 {
		return terms[0], nil
	}
	return tr.captureExecutionLeaseTerm()
}

func (tr *TestRunner) saveScenarioScheduleOccurrenceForSweepLocked(occurrence *storage.ScenarioScheduleOccurrence, leaseTerms ...executionLeaseTerm) error {
	if occurrence == nil {
		return fmt.Errorf("schedule occurrence is required")
	}
	leaseTerm, termErr := tr.scenarioScheduleSweepTerm(leaseTerms)
	if termErr != nil {
		return termErr
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	expectedRevision := occurrence.Revision
	occurrence.TransitionToken = uuid.NewString()
	store := tr.scenarioStore
	tr.mu.Unlock()
	var err error
	if store != nil {
		var changed bool
		if expectedRevision < 1 {
			if tr.durableExecutionFence {
				fencedStore, ok := store.(leaseTermFencedScenarioScheduleOccurrenceClaimPersistence)
				if !ok {
					err = fmt.Errorf("scenario schedule persistence does not support current-parent lease-term-fenced occurrence claims")
				} else {
					changed, err = fencedStore.ClaimScenarioScheduleOccurrenceIfCurrentAndLeaseTermHeld(occurrence, executionLeaseName, leaseTerm.ownerID, leaseTerm.generation)
				}
			} else if claimStore, ok := store.(scenarioScheduleOccurrenceClaimPersistence); ok {
				changed, err = claimStore.ClaimScenarioScheduleOccurrenceIfCurrent(occurrence)
			} else if casStore, ok := store.(scenarioScheduleOccurrenceCASPersistence); ok {
				// Compatibility for lightweight in-memory test stores. Production
				// persistence implements the parent-conditional claim above.
				changed, err = casStore.CreateScenarioScheduleOccurrenceIfAbsent(occurrence)
			} else {
				err = fmt.Errorf("scenario schedule persistence does not support occurrence create-only insertion")
			}
		} else {
			if tr.durableExecutionFence {
				fencedStore, ok := store.(leaseTermFencedScenarioSchedulePersistence)
				if !ok {
					err = fmt.Errorf("scenario schedule persistence does not support execution lease-term fencing")
				} else {
					changed, err = fencedStore.CompareAndSwapScenarioScheduleOccurrenceIfLeaseTermHeld(occurrence, expectedRevision, executionLeaseName, leaseTerm.ownerID, leaseTerm.generation)
				}
			} else if casStore, ok := store.(scenarioScheduleOccurrenceCASPersistence); ok {
				changed, err = casStore.CompareAndSwapScenarioScheduleOccurrence(occurrence, expectedRevision)
			} else {
				err = fmt.Errorf("scenario schedule persistence does not support occurrence revision CAS")
			}
		}
		if err == nil && !changed {
			current, reloadErr := store.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(occurrence.ScheduleID, occurrence.PlannedAt)
			if reloadErr != nil {
				err = reloadErr
			} else if scenarioScheduleOccurrenceTransitionMatches(current, occurrence, expectedRevision) {
				*occurrence = *current
				occurrence.MaterializedAt = cloneTimePtr(current.MaterializedAt)
				changed = true
			} else if !tr.executionLeaseTermCurrent(leaseTerm) {
				err = errNotExecutionLeader
			} else if current == nil {
				err = fmt.Errorf("scenario schedule occurrence %q was deleted or not created", occurrence.ID)
			} else {
				err = fmt.Errorf("scenario schedule occurrence %q changed concurrently (expected revision %d, durable revision %d)", occurrence.ID, expectedRevision, current.Revision)
			}
		}
		if err == nil && changed && tr.executionLeaseTermCurrent(leaseTerm) {
			tr.recordScenarioScheduleOccurrenceAuditLocked(occurrence)
		} else if err == nil {
			err = errNotExecutionLeader
		}
	}
	tr.mu.Lock()
	return err
}

func scenarioScheduleOccurrenceTransitionMatches(current, intended *storage.ScenarioScheduleOccurrence, expectedRevision int64) bool {
	if current == nil || intended == nil || current.ID != intended.ID || current.ScheduleID != intended.ScheduleID ||
		current.ScenarioID != intended.ScenarioID || normalizeProjectID(current.ProjectID) != normalizeProjectID(intended.ProjectID) ||
		!current.PlannedAt.Equal(intended.PlannedAt) || current.TransitionToken != intended.TransitionToken ||
		current.Status != intended.Status || current.ReasonCode != intended.ReasonCode || current.TestID != intended.TestID ||
		current.ApprovalID != intended.ApprovalID || current.ConflictSummary != intended.ConflictSummary ||
		!sameOptionalTime(current.MaterializedAt, intended.MaterializedAt) {
		return false
	}
	if expectedRevision < 1 {
		return current.Revision == 1
	}
	return current.Revision == expectedRevision+1
}

// saveScenarioScheduleForSweepLocked is the schedule-CAS equivalent of the
// occurrence helper above. It snapshots the expected in-memory revision, does
// the durable CAS without tr.mu, then applies the result only if the same
// in-memory revision is still current.
func (tr *TestRunner) saveScenarioScheduleForSweepLocked(schedule *storage.ScenarioSchedule, leaseTerms ...executionLeaseTerm) error {
	if schedule == nil {
		return fmt.Errorf("schedule is required")
	}
	leaseTerm, termErr := tr.scenarioScheduleSweepTerm(leaseTerms)
	if termErr != nil {
		return termErr
	}
	scenario, ok := tr.scenarios[schedule.ScenarioID]
	if !ok {
		return fmt.Errorf("scenario not found: %s", schedule.ScenarioID)
	}
	existingIndex := -1
	expectedRevision := 0
	for index, existing := range scenario.Schedules {
		if existing != nil && existing.ID == schedule.ID {
			existingIndex = index
			expectedRevision = existing.Revision
			break
		}
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	if tr.scenarioStore != nil {
		tr.mu.Unlock()
		changed := true
		var err error
		if tr.durableExecutionFence {
			fencedStore, ok := tr.scenarioStore.(leaseTermFencedScenarioSchedulePersistence)
			if !ok {
				err = fmt.Errorf("scenario schedule persistence does not support execution lease-term fencing")
			} else if existingIndex < 0 {
				changed, err = fencedStore.CreateScenarioScheduleIfAbsentAndLeaseTermHeld(schedule, executionLeaseName, leaseTerm.ownerID, leaseTerm.generation)
			} else {
				changed, err = fencedStore.UpdateScenarioScheduleIfRevisionAndLeaseTermHeld(schedule, expectedRevision, executionLeaseName, leaseTerm.ownerID, leaseTerm.generation)
			}
		} else if casStore, ok := tr.scenarioStore.(scenarioScheduleCASPersistence); ok {
			if existingIndex < 0 {
				changed, err = casStore.CreateScenarioScheduleIfAbsent(schedule)
			} else {
				changed, err = casStore.UpdateScenarioScheduleIfRevision(schedule, expectedRevision)
			}
		} else {
			err = tr.scenarioStore.SaveScenarioSchedule(schedule)
		}
		tr.mu.Lock()
		if err != nil {
			return err
		}
		if !changed {
			if tr.durableExecutionFence {
				return fmt.Errorf("%w: scenario schedule %q changed, was deleted, or belongs to an expired execution epoch", errNotExecutionLeader, schedule.ID)
			}
			return fmt.Errorf("scenario schedule %q changed or was deleted; reload and retry", schedule.ID)
		}
		if !tr.executionLeaseTermCurrent(leaseTerm) {
			return errNotExecutionLeader
		}
	}

	scenario, ok = tr.scenarios[schedule.ScenarioID]
	if !ok {
		return fmt.Errorf("scenario %q changed or was deleted; reload and retry", schedule.ScenarioID)
	}
	currentIndex := -1
	for index, existing := range scenario.Schedules {
		if existing != nil && existing.ID == schedule.ID {
			currentIndex = index
			if existingIndex >= 0 && existing.Revision != expectedRevision {
				return fmt.Errorf("scenario schedule %q changed while persistence was in flight; reload and retry", schedule.ID)
			}
			break
		}
	}
	if existingIndex >= 0 && currentIndex < 0 {
		return fmt.Errorf("scenario schedule %q was deleted while persistence was in flight", schedule.ID)
	}
	var previous *storage.ScenarioSchedule
	if currentIndex >= 0 {
		previous = scenario.Schedules[currentIndex]
		scenario.Schedules[currentIndex] = cloneScenarioSchedule(schedule)
	} else {
		scenario.Schedules = append(scenario.Schedules, cloneScenarioSchedule(schedule))
	}
	tr.contentRevision++
	tr.adjustSchedulerReadinessCountersLocked(previous, schedule)
	return nil
}

func (tr *TestRunner) runScenarioScheduleLocked(schedule *storage.ScenarioSchedule, now time.Time, leaseTerms ...executionLeaseTerm) (*ManagedTest, error) {
	if schedule == nil {
		return nil, fmt.Errorf("schedule is required")
	}
	leaseTerm, termErr := tr.scenarioScheduleSweepTerm(leaseTerms)
	if termErr != nil {
		return nil, termErr
	}
	scenario, ok := tr.scenarios[schedule.ScenarioID]
	if !ok {
		return nil, fmt.Errorf("scenario not found: %s", schedule.ScenarioID)
	}
	plannedAt := now
	if schedule.NextRunAt != nil {
		plannedAt = *schedule.NextRunAt
	}
	handled, resumeOccurrence, recoveredPrepared, err := tr.reconcileScenarioScheduleOccurrenceLocked(schedule, plannedAt, now, leaseTerm)
	if err != nil {
		return nil, err
	} else if handled {
		return recoveredPrepared, nil
	}
	if allowed, err := scheduleAllowsCandidate(schedule, plannedAt); err != nil {
		return nil, err
	} else if !allowed {
		schedule.ConflictSummary = "Environment availability conflict: planned occurrence falls outside allowed schedule windows."
		if err := tr.blockScheduleOccurrenceLocked(schedule, plannedAt, "environment_unavailable", schedule.ConflictSummary, "resolve_conflict", schedule.ConflictSummary, now, nil, leaseTerm); err != nil {
			return nil, err
		}
		return nil, nil
	}
	conflictSummary, err := tr.scenarioScheduleConflictSummaryForSweepLocked(schedule, scenario, leaseTerm)
	if err != nil {
		return nil, err
	}
	schedule.ConflictSummary = conflictSummary
	if strings.TrimSpace(schedule.ConflictSummary) != "" {
		if err := tr.blockScheduleOccurrenceLocked(schedule, plannedAt, "schedule_conflict", schedule.ConflictSummary, "resolve_conflict", schedule.ConflictSummary, now, nil, leaseTerm); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if schedule.NextRequiredAction == "approval_required" && schedule.PendingApprovalID != "" {
		if err := tr.blockScheduleOccurrenceLocked(schedule, plannedAt, "approval_required", "Schedule requires approval before materialization.", "", "schedule requires approval", now, func(occurrence *storage.ScenarioScheduleOccurrence) {
			occurrence.ApprovalID = schedule.PendingApprovalID
		}, leaseTerm); err != nil {
			return nil, err
		}
		return nil, nil
	}
	occurrence := resumeOccurrence
	isNewOccurrence := occurrence == nil
	if isNewOccurrence {
		occurrence = tr.buildScenarioScheduleOccurrence(schedule, plannedAt, "materializing", "due", "", nil, now)
	}
	occurrence.Status = "materializing"
	if strings.TrimSpace(occurrence.ReasonCode) == "" {
		occurrence.ReasonCode = "due"
	}
	occurrence.ConflictSummary = ""
	occurrence.MaterializedAt = nil
	testID := strings.TrimSpace(occurrence.TestID)
	if testID == "" {
		testID = scenarioScheduleOccurrenceMaterializationTestID(occurrence)
	}
	if isNewOccurrence {
		occurrence.UpdatedAt = now
		if err := tr.saveScenarioScheduleOccurrenceForSweepLocked(occurrence, leaseTerm); err != nil {
			return nil, err
		}
	}
	runName := strings.TrimSpace(schedule.RunNameTemplate)
	if runName == "" {
		runName = fmt.Sprintf("Scheduled Run %s", now.Format(time.RFC3339))
	}
	test, err := tr.buildManagedTestForScheduleSweepLockedWithTerm(CreateTestRequest{
		Name:       runName,
		ScenarioID: schedule.ScenarioID,
		ReleaseID:  schedule.ReleaseID,
		creationActor: testCreationActor{
			Type:        "system",
			Subject:     schedule.OwnerSubject,
			DisplayName: schedule.OwnerDisplayName,
		},
	}, testID, "QUEUED", leaseTerm)
	if err == nil {
		for _, group := range test.Groups {
			if len(group.runtimeSecretValues) > 0 {
				err = fmt.Errorf("%w: scenario %s contains legacy plaintext runtime credentials; save the scenario to convert them to {{secret.NAME}} references and configure Project Secrets before the next scheduled run", errUnattributedRuntimeSecretWrite, schedule.ScenarioID)
				break
			}
		}
	}
	if err == nil {
		err = tr.claimTestExecutionFenceWithTerm(test, leaseTerm)
	}
	if err != nil {
		occurrence.Status = "failed"
		occurrence.ReasonCode = "create_failed"
		occurrence.ConflictSummary = err.Error()
		occurrence.UpdatedAt = now
		if saveErr := tr.saveScenarioScheduleOccurrenceForSweepLocked(occurrence, leaseTerm); saveErr != nil {
			slog.Warn("failed to persist scenario occurrence creation failure", "occurrence_id", occurrence.ID, "error", saveErr)
		}
		return nil, err
	}
	// Recurring runs use the same launch-window evaluator as manual/scheduled
	// starts and preflight. A run that would launch into a capacity/concurrency/
	// reservation-window conflict is recorded as a blocked occurrence (surfaced on
	// the run calendar) instead of being launched anyway. The test is built but
	// not yet registered, so it does not self-conflict; the "materializing"
	// occurrence already dedups this schedule's own projection.
	if windowErr := tr.launchStartWindowErrorForScheduleSweepLockedWithTerm(test, now, test.ID, schedule.ID, leaseTerm); windowErr != nil {
		schedule.ConflictSummary = windowErr.Error()
		if err := tr.blockScheduleOccurrenceLocked(schedule, plannedAt, "schedule_conflict", schedule.ConflictSummary, "resolve_conflict", schedule.ConflictSummary, now, nil, leaseTerm); err != nil {
			return nil, err
		}
		return nil, nil
	}
	// Validate the schedule's default capacity timeslot, if configured, for this
	// occurrence's run. The actual bind and test insert commit atomically below.
	// Lenient: if the slot is
	// missing, unapproved, already bound, or doesn't fit, block this occurrence
	// with a clear reason rather than failing the whole schedule — schedules are
	// long-lived and the slot may be transiently unavailable.
	if strings.TrimSpace(schedule.TimeslotID) != "" {
		tr.mu.Unlock()
		timeslotEvidence, bindErr := tr.loadCapacityTimeslotEvidence(strings.TrimSpace(schedule.TimeslotID))
		tr.mu.Lock()
		if bindErr == nil {
			bindErr = validateAndApplyTimeslotEvidenceLocked(timeslotEvidence, test)
		}
		if bindErr != nil {
			schedule.ConflictSummary = bindErr.Error()
			if err := tr.blockScheduleOccurrenceLocked(schedule, plannedAt, "timeslot_unavailable", bindErr.Error(), "resolve_conflict", bindErr.Error(), now, nil, leaseTerm); err != nil {
				return nil, err
			}
			return nil, nil
		}
	}
	tr.mu.Unlock()
	saveTestErr := tr.saveNewManagedTestRecordWithTerm(test, leaseTerm)
	tr.mu.Lock()
	if saveTestErr != nil {
		occurrence.Status = "failed"
		occurrence.ReasonCode = "create_persist_failed"
		occurrence.ConflictSummary = saveTestErr.Error()
		occurrence.UpdatedAt = now
		if saveErr := tr.saveScenarioScheduleOccurrenceForSweepLocked(occurrence, leaseTerm); saveErr != nil {
			slog.Warn("failed to persist scheduled test creation failure", "occurrence_id", occurrence.ID, "error", saveErr)
		}
		return nil, fmt.Errorf("persist scheduled test: %w", saveTestErr)
	}
	tr.tests[test.ID] = test
	tr.indexRefKeyLocked(test.RefKey, test.ID)
	tr.broadcastEvent("test_created", cloneManagedTestLifecycleEvent(test))
	// The launch window was just evaluated above; skip the redundant re-check.
	prepared, releaseStartLifecycle, err := tr.prepareTestStartForScheduleSweepLockedWithTerm(test.ID, StartTestOptions{
		SkipLaunchWindowPreflight:        true,
		deferPostShakeoutRecoveryEnqueue: true,
	}, leaseTerm)
	if releaseStartLifecycle != nil {
		defer releaseStartLifecycle()
	}
	deferredStartRecovery := false
	if err != nil {
		if scenarioScheduleStartRetryableAfterOneShot(test, err) {
			markScenarioScheduleOccurrenceStartRetrying(occurrence, test, err)
			occurrence.UpdatedAt = now
			deferredStartRecovery = true
			prepared = nil
		} else {
			occurrence.Status = "failed"
			occurrence.ReasonCode = "start_failed"
			occurrence.TestID = test.ID
			occurrence.ConflictSummary = err.Error()
			occurrence.UpdatedAt = now
			if saveErr := tr.saveScenarioScheduleOccurrenceForSweepLocked(occurrence, leaseTerm); saveErr != nil {
				slog.Warn("failed to persist scheduled test start failure", "occurrence_id", occurrence.ID, "error", saveErr)
			}
			if releaseStartLifecycle != nil {
				releaseStartLifecycle()
			}
			if failErr := tr.failScheduledTestBeforeLaunchLocked(test, now, "prepare scheduled test: "+err.Error()); failErr != nil {
				return nil, errors.Join(err, failErr)
			}
			return nil, err
		}
	}
	if prepared == nil {
		if !deferredStartRecovery {
			markScenarioScheduleOccurrenceQueued(occurrence, test)
		}
	} else {
		occurrence.Status = "materialized"
		occurrence.ReasonCode = "started"
		occurrence.TestID = test.ID
		occurrence.ConflictSummary = ""
		occurrence.MaterializedAt = &now
	}
	occurrence.UpdatedAt = now
	if err := tr.saveScenarioScheduleOccurrenceForSweepLocked(occurrence, leaseTerm); err != nil {
		if deferredStartRecovery {
			return nil, err
		}
		if releaseStartLifecycle != nil {
			releaseStartLifecycle()
		}
		if failErr := tr.failScheduledTestBeforeLaunchLocked(test, now, "save materialized schedule occurrence: "+err.Error()); failErr != nil {
			return nil, errors.Join(err, failErr)
		}
		return nil, err
	}
	schedule.LastRunTestID = test.ID
	if occurrence.MaterializedAt != nil {
		materializedAt := occurrence.MaterializedAt.UTC()
		schedule.LastRunAt = &materializedAt
	}
	schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrence)
	schedule.NextRequiredAction = ""
	schedule.LastError = ""
	advanceFrom := now
	if normalizeScenarioScheduleMissedRunPolicy(schedule.MissedRunPolicy) == "catch_up_all" && plannedAt.Before(now) {
		advanceFrom = plannedAt
	}
	next, err := nextScenarioScheduleRun(schedule, advanceFrom)
	if err != nil {
		if deferredStartRecovery {
			return nil, err
		}
		occurrence.Status = "failed"
		occurrence.ReasonCode = "schedule_calculation_failed"
		occurrence.ConflictSummary = err.Error()
		occurrence.UpdatedAt = now
		if saveErr := tr.saveScenarioScheduleOccurrenceForSweepLocked(occurrence, leaseTerm); saveErr != nil {
			slog.Warn("failed to persist schedule calculation failure", "occurrence_id", occurrence.ID, "error", saveErr)
		}
		if releaseStartLifecycle != nil {
			releaseStartLifecycle()
		}
		if failErr := tr.failScheduledTestBeforeLaunchLocked(test, now, "calculate next scheduled run: "+err.Error()); failErr != nil {
			return nil, errors.Join(err, failErr)
		}
		return nil, err
	}
	schedule.NextRunAt = next
	schedule.UpdatedAt = now
	if err := tr.saveScenarioScheduleForSweepLocked(schedule, leaseTerm); err != nil {
		if deferredStartRecovery {
			return nil, err
		}
		occurrence.Status = "failed"
		occurrence.ReasonCode = "schedule_persist_failed"
		occurrence.ConflictSummary = err.Error()
		occurrence.UpdatedAt = now
		if saveErr := tr.saveScenarioScheduleOccurrenceForSweepLocked(occurrence, leaseTerm); saveErr != nil {
			slog.Warn("failed to persist schedule persistence failure", "occurrence_id", occurrence.ID, "error", saveErr)
		}
		if releaseStartLifecycle != nil {
			releaseStartLifecycle()
		}
		if failErr := tr.failScheduledTestBeforeLaunchLocked(test, now, "save updated schedule: "+err.Error()); failErr != nil {
			return nil, errors.Join(err, failErr)
		}
		return nil, err
	}
	if deferredStartRecovery {
		// The one-shot action outcome is process-private until verification
		// succeeds. Publish both correlation rows before the coordinator may
		// continue admission and dispatch the backend.
		tr.mu.Unlock()
		tr.enqueueQueuedCIRecoveryRetryForManagedTestWithTerm(test, leaseTerm)
		tr.mu.Lock()
		if !tr.executionLeaseTermCurrent(leaseTerm) {
			return nil, errNotExecutionLeader
		}
	}
	return prepared, nil
}

func scenarioScheduleStartRetryableAfterOneShot(test *ManagedTest, err error) bool {
	if test == nil || (!errors.Is(err, errStartAdmissionEvidenceStale) && !retryableStartSecretRequirement(err)) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(test.Status), "SHAKEOUT") && test.pendingScriptShakeoutOutcome != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(test.Status), "QUEUED") && test.ScriptShakeout &&
		test.StartAuthorization != nil && scriptShakeoutPassAllows(test.StartAuthorization.ScriptShakeoutPass)
}

func markScenarioScheduleOccurrenceStartRetrying(occurrence *storage.ScenarioScheduleOccurrence, test *ManagedTest, err error) {
	if occurrence == nil {
		return
	}
	occurrence.Status = "queued"
	occurrence.ReasonCode = "start_retrying"
	occurrence.MaterializedAt = nil
	if test != nil {
		occurrence.TestID = test.ID
	}
	occurrence.ConflictSummary = "scheduled start deferred while completed script shakeout evidence is retried"
	if err != nil {
		occurrence.ConflictSummary += ": " + err.Error()
	}
}

func markScenarioScheduleOccurrenceShakeoutReview(occurrence *storage.ScenarioScheduleOccurrence, test *ManagedTest, summary string) {
	if occurrence == nil {
		return
	}
	occurrence.Status = "blocked"
	occurrence.ReasonCode = "script_shakeout_review"
	occurrence.MaterializedAt = nil
	if test != nil {
		occurrence.TestID = test.ID
	}
	occurrence.ConflictSummary = firstNonEmpty(
		strings.TrimSpace(summary),
		"script shakeout evidence requires operator review; full load test has not started",
	)
}

// scenarioScheduleStartRetryingForTest identifies only the exact recurring
// correlation published before asynchronous post-shakeout recovery. Such a
// retry must use the scheduler's already-approved launch-window decision: a
// generic preflight would include the schedule's newly advanced next run and
// can count the same recurring workload twice.
func (tr *TestRunner) scenarioScheduleStartRetryingForTest(testID string) (bool, error) {
	if tr == nil || tr.scenarioStore == nil || strings.TrimSpace(testID) == "" {
		return false, nil
	}
	store, ok := tr.scenarioStore.(scenarioScheduleOccurrenceByTestStore)
	if !ok {
		return false, nil
	}
	occurrence, err := store.GetScenarioScheduleOccurrenceByTestID(strings.TrimSpace(testID))
	if err != nil {
		return false, err
	}
	return occurrence != nil && occurrence.TestID == strings.TrimSpace(testID) &&
		strings.EqualFold(strings.TrimSpace(occurrence.Status), "queued") &&
		strings.EqualFold(strings.TrimSpace(occurrence.ReasonCode), "start_retrying"), nil
}

// failRecoveredScenarioScheduleStartWithTerm terminalizes an asynchronous
// post-shakeout start only when it still owns the exact recurring occurrence
// that requested recovery. Production test persistence changes the test,
// queued occurrence, and parent schedule in one transaction, so a coordinator
// cannot retire a permanent error while leaving start_retrying on the calendar.
func (tr *TestRunner) failRecoveredScenarioScheduleStartWithTerm(testID string, cause error, leaseTerm executionLeaseTerm) (bool, error) {
	return tr.failRecoveredScenarioScheduleStartForIdentityWithTerm(testID, cause, nil, leaseTerm)
}

func (tr *TestRunner) failRecoveredScenarioScheduleStartForIdentityWithTerm(
	testID string,
	cause error,
	expectedIdentity *queuedStartRecoveryIdentity,
	leaseTerm executionLeaseTerm,
) (bool, error) {
	if tr == nil || tr.scenarioStore == nil || strings.TrimSpace(testID) == "" {
		return false, nil
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return false, errNotExecutionLeader
	}
	tr.mu.RLock()
	testID = tr.resolveRefKeyLocked(testID)
	tr.mu.RUnlock()
	occurrenceStore, ok := tr.scenarioStore.(scenarioScheduleOccurrenceByTestStore)
	if !ok {
		return false, nil
	}
	occurrence, err := occurrenceStore.GetScenarioScheduleOccurrenceByTestID(testID)
	if err != nil {
		return true, errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("load failed scheduled-start occurrence: %w", err))
	}
	if occurrence == nil || occurrence.TestID != testID ||
		!strings.EqualFold(strings.TrimSpace(occurrence.Status), "queued") ||
		!strings.EqualFold(strings.TrimSpace(occurrence.ReasonCode), "start_retrying") {
		return false, nil
	}

	message := "resume correlated scheduled test"
	if cause != nil {
		message += ": " + cause.Error()
	}
	now := time.Now().UTC()
	tr.mu.Lock()
	test := tr.tests[testID]
	if expectedIdentity != nil && !expectedIdentity.matches(test) {
		tr.mu.Unlock()
		return true, nil
	}
	if test == nil {
		tr.mu.Unlock()
		return true, nil
	}
	failed, failErr := tr.failScheduledTestBeforeLaunchIfStatusAndTermLocked(test, now, message, "QUEUED", &leaseTerm)
	tr.mu.Unlock()
	if failErr != nil {
		return true, failErr
	}
	if !failed {
		// A concurrent lifecycle winner changed the exact test after the durable
		// occurrence read. It owns any subsequent calendar projection.
		return true, nil
	}
	if err := tr.syncScenarioScheduleLifecycleForTest(testID); err != nil {
		return true, err
	}
	return true, nil
}

// reconcileScenarioScheduleShakeoutReviewWithTerm closes the durable boundary
// between a one-shot shakeout becoming SHAKEOUT_REVIEW and its recurring
// schedule correlation becoming operator-visible. The test lifecycle gate
// serializes this transition with Stop/Abort, while the storage transaction
// changes the exact queued/start_retrying occurrence and parent schedule under
// their revisions and the original execution lease term.
func (tr *TestRunner) reconcileScenarioScheduleShakeoutReviewWithTerm(testID, summary string, leaseTerm executionLeaseTerm) error {
	return tr.reconcileScenarioScheduleShakeoutReviewForIdentityWithTerm(testID, summary, nil, leaseTerm)
}

func (tr *TestRunner) reconcileScenarioScheduleShakeoutReviewForIdentityWithTerm(
	testID, summary string,
	expectedIdentity *queuedStartRecoveryIdentity,
	leaseTerm executionLeaseTerm,
) error {
	if tr == nil || tr.scenarioStore == nil || strings.TrimSpace(testID) == "" {
		return nil
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	testID = tr.resolveRefKeyLocked(testID)
	tr.mu.RUnlock()
	unlockLifecycle, err := tr.lockTestLifecycleControl(context.Background(), testID)
	if err != nil {
		return err
	}
	defer unlockLifecycle()
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}

	tr.mu.Lock()
	test := tr.tests[testID]
	if expectedIdentity != nil && !expectedIdentity.matches(test) {
		tr.mu.Unlock()
		return nil
	}
	testInReview := test != nil && strings.EqualFold(strings.TrimSpace(test.Status), "SHAKEOUT_REVIEW") && test.StartedAt == nil
	if strings.TrimSpace(summary) == "" && test != nil {
		summary = test.Error
	}
	if !testInReview {
		tr.mu.Unlock()
		// A concurrent Stop/Abort or newer start decision owns the lifecycle now.
		return nil
	}
	claimedFence, claimErr := tr.claimPassiveExecutionFenceLocked(test, &leaseTerm)
	operationToken := test.passiveOperationToken
	tr.mu.Unlock()
	if claimErr != nil {
		return claimErr
	}
	if claimedFence {
		defer func() {
			tr.mu.Lock()
			if tr.tests[testID] == test && test.passiveOperationToken == operationToken {
				releasePassiveExecutionFenceLocked(test)
			}
			tr.mu.Unlock()
		}()
	}
	occurrenceStore, ok := tr.scenarioStore.(scenarioScheduleOccurrenceByTestStore)
	if !ok {
		return nil
	}
	occurrence, err := occurrenceStore.GetScenarioScheduleOccurrenceByTestID(testID)
	if err != nil {
		return errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("load scheduled shakeout-review occurrence: %w", err))
	}
	if occurrence == nil {
		// Ordinary/manual starts use the same recovery coordinator but have no
		// recurring correlation to repair.
		return nil
	}
	status := strings.ToLower(strings.TrimSpace(occurrence.Status))
	reason := strings.ToLower(strings.TrimSpace(occurrence.ReasonCode))
	isQueuedRetry := status == "queued" && reason == "start_retrying" && occurrence.TestID == testID
	isReviewCorrelation := status == "blocked" && reason == "script_shakeout_review" && occurrence.TestID == testID && occurrence.MaterializedAt == nil
	if !isQueuedRetry && !isReviewCorrelation {
		// Exact-CAS semantics: any different peer/state is a newer durable winner.
		return nil
	}
	schedule, err := tr.scenarioStore.GetScenarioSchedule(occurrence.ScheduleID)
	if err != nil {
		return errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("load scheduled shakeout-review parent: %w", err))
	}
	if schedule == nil || schedule.ID != occurrence.ScheduleID || schedule.ScenarioID != occurrence.ScenarioID ||
		normalizeProjectID(schedule.ProjectID) != normalizeProjectID(occurrence.ProjectID) {
		return fmt.Errorf("scheduled shakeout-review occurrence %q has an invalid parent schedule", occurrence.ID)
	}
	summary = firstNonEmpty(
		strings.TrimSpace(summary),
		"script shakeout evidence requires operator review; full load test has not started",
	)
	if isReviewCorrelation && schedule.NextRequiredAction == "operator_decision_required" && schedule.LastError == summary {
		return tr.syncScenarioScheduleLifecycleForTest(testID)
	}

	expectedOccurrenceRevision := occurrence.Revision
	if expectedOccurrenceRevision < 1 {
		expectedOccurrenceRevision = 1
	}
	expectedScheduleRevision := schedule.Revision
	now := time.Now().UTC()
	markScenarioScheduleOccurrenceShakeoutReview(occurrence, test, summary)
	occurrence.TransitionToken = uuid.NewString()
	occurrence.UpdatedAt = now
	schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrence)
	schedule.LastError = occurrence.ConflictSummary
	schedule.NextRequiredAction = "operator_decision_required"
	schedule.UpdatedAt = now
	atomicStore, ok := tr.scenarioStore.(atomicScenarioOccurrenceResolutionStore)
	if !ok {
		return fmt.Errorf("scenario schedule storage does not support atomic shakeout-review correlation")
	}
	var changed bool
	if tr.durableExecutionFence {
		changed, err = atomicStore.ResolveScenarioScheduleOccurrenceIfLeaseTermHeld(
			occurrence, expectedOccurrenceRevision, schedule, expectedScheduleRevision,
			executionLeaseName, leaseTerm.ownerID, leaseTerm.generation,
		)
	} else {
		changed, err = atomicStore.ResolveScenarioScheduleOccurrence(
			occurrence, expectedOccurrenceRevision, schedule, expectedScheduleRevision,
		)
	}
	if err != nil {
		return errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("persist scheduled shakeout-review correlation: %w", err))
	}
	if !changed {
		if !tr.executionLeaseTermCurrent(leaseTerm) {
			return errNotExecutionLeader
		}
		current, loadErr := occurrenceStore.GetScenarioScheduleOccurrenceByTestID(testID)
		if loadErr != nil {
			return errors.Join(errStartAdmissionEvidenceStale, loadErr)
		}
		if current == nil {
			return nil
		}
		currentStatus := strings.ToLower(strings.TrimSpace(current.Status))
		currentReason := strings.ToLower(strings.TrimSpace(current.ReasonCode))
		if currentStatus != "queued" || currentReason != "start_retrying" || current.TestID != testID {
			// The desired blocked state and every unrelated terminal/newer state
			// both supersede this exact queued retry attempt.
			return tr.syncScenarioScheduleLifecycleForTest(testID)
		}
		return errors.Join(errStartAdmissionEvidenceStale, errScheduleConcurrentChange)
	}
	return tr.syncScenarioScheduleLifecycleForTest(testID)
}

func scenarioScheduleOccurrenceTestID(occurrenceID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload:scenario-schedule-occurrence:"+strings.TrimSpace(occurrenceID))).String()
}

// scenarioScheduleOccurrenceMaterializationTestID keeps the original
// occurrence-derived ID for first attempts, while operator retries/catch-ups
// get a new deterministic ID from the revision committed by the resolution
// CAS. A resumed occurrence is already a durable materializing anchor, so the
// scheduler deliberately does not increment that revision before the test row
// is persisted. Crash recovery therefore derives the same attempt ID.
func scenarioScheduleOccurrenceMaterializationTestID(occurrence *storage.ScenarioScheduleOccurrence) string {
	if occurrence == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(occurrence.ReasonCode)) {
	case "operator_retry", "operator_catch_up":
		revision := occurrence.Revision
		if revision < 1 {
			revision = 1
		}
		seed := fmt.Sprintf("apexload:scenario-schedule-occurrence:%s:attempt:%d", strings.TrimSpace(occurrence.ID), revision)
		return uuid.NewSHA1(uuid.NameSpaceURL, []byte(seed)).String()
	default:
		return scenarioScheduleOccurrenceTestID(occurrence.ID)
	}
}

func (tr *TestRunner) failScheduledTestBeforeLaunchLocked(test *ManagedTest, now time.Time, message string) error {
	_, err := tr.failScheduledTestBeforeLaunchIfStatusAndTermLocked(test, now, message, "", nil)
	return err
}

// failScheduledTestBeforeLaunchIfStatusAndTermLocked is the exact-status form
// used by asynchronous schedule recovery. The operation token closes the gap
// between revalidating QUEUED and the off-lock terminal write: a manual start
// that is still gathering admission evidence cannot begin persistence after
// this failure operation takes ownership. The caller holds tr.mu on entry and
// again on return.
func (tr *TestRunner) failScheduledTestBeforeLaunchIfStatusAndTermLocked(
	test *ManagedTest,
	now time.Time,
	message string,
	expectedStatus string,
	expectedTerm *executionLeaseTerm,
) (bool, error) {
	if test == nil {
		return false, nil
	}
	testID := test.ID
	tr.mu.Unlock()
	unlockLifecycle, err := tr.lockTestLifecycleControl(context.Background(), testID)
	tr.mu.Lock()
	if err != nil {
		return false, err
	}
	defer unlockLifecycle()
	current := tr.tests[testID]
	if current == nil || current != test {
		return false, nil
	}
	switch current.Status {
	case "COMPLETE", "FAILED", "ABORTED":
		return false, nil
	}
	if expectedStatus != "" && !strings.EqualFold(strings.TrimSpace(current.Status), expectedStatus) {
		return false, nil
	}
	if current.passiveOperationToken != "" {
		return false, errStartAdmissionEvidenceStale
	}
	if tr.durableExecutionFence {
		if expectedTerm != nil {
			if !tr.executionLeaseTermLocallyCurrent(*expectedTerm) {
				return false, errNotExecutionLeader
			}
			if current.executionFence != "" &&
				(current.executionFence != expectedTerm.ownerID || current.executionGeneration != expectedTerm.generation) {
				return false, errNotExecutionLeader
			}
			current.executionFence = expectedTerm.ownerID
			current.executionGeneration = expectedTerm.generation
		} else if err := tr.claimTestExecutionFence(current); err != nil {
			return false, err
		}
	}
	current.passiveOperationToken = uuid.NewString()
	operationToken := current.passiveOperationToken
	executionFence := current.executionFence
	executionGeneration := current.executionGeneration
	failedSnapshot := cloneManagedTestForReadLocked(current)
	failedSnapshot.Status = "FAILED"
	failedSnapshot.PendingSince = nil
	failedSnapshot.PendingReason = ""
	failedSnapshot.QueuePosition = 0
	failedSnapshot.Error = message
	failedSnapshot.EndedAt = cloneTimePtr(&now)
	slotID := ""
	if current.TimeslotID != nil {
		slotID = strings.TrimSpace(*current.TimeslotID)
	}

	tr.mu.Unlock()
	var persistErr error
	if _, supportsMissingRowFence := tr.testStore.(interface {
		UpdateTestStatusIfActive(string, string, *time.Time, string) (storage.TestStatusTransitionResult, error)
	}); supportsMissingRowFence || tr.durableExecutionFence {
		persistErr = tr.persistStoredTestStatus(testID, executionFence, "FAILED", &now, message)
	} else {
		// Compatibility for focused legacy stores. Production SQLite/PostgreSQL
		// use the missing-row-safe partial transition above.
		persistErr = tr.saveManagedTestRecord(failedSnapshot)
	}
	var releaseErr error
	if persistErr == nil && tr.reservationStore != nil && slotID != "" {
		if clearErr := tr.releaseCapacityReservationBinding(testID, slotID); clearErr != nil {
			slog.Warn("failed to release failed scheduled test timeslot", "test_id", testID, "timeslot_id", slotID, "error", clearErr)
			releaseErr = fmt.Errorf("release failed scheduled test timeslot: %w", clearErr)
			tr.enqueueCapacityReservationReleaseRetry(testID, slotID)
		}
	}
	tr.mu.Lock()
	current = tr.tests[testID]
	if current == nil || current != test || current.passiveOperationToken != operationToken ||
		current.executionFence != executionFence || current.executionGeneration != executionGeneration {
		return false, errors.Join(errStartAdmissionEvidenceStale, persistErr)
	}
	current.passiveOperationToken = ""
	if isStoredTestTransitionCancelled(persistErr) {
		return false, nil
	}
	if persistErr != nil {
		retryErr := fmt.Errorf("persist scheduled test failure: %w", persistErr)
		slog.Error("failed to persist scheduled test failure; cleanup and notification deferred", "test_id", test.ID, "error", retryErr)
		tr.enqueueScheduledTestFailureRetry(test, now, message)
		return true, retryErr
	}
	current.Status = "FAILED"
	current.PendingSince = nil
	current.PendingReason = ""
	current.QueuePosition = 0
	current.Error = message
	current.EndedAt = cloneTimePtr(&now)
	if current.cancel != nil {
		current.cancel()
	}
	tr.broadcastEvent("test_failed", cloneManagedTestLifecycleEvent(current))
	compactTerminalManagedTestLocked(current)
	return true, releaseErr
}

func (tr *TestRunner) enqueueScheduledTestFailureRetry(expected *ManagedTest, now time.Time, message string) {
	if expected == nil {
		return
	}
	testID := expected.ID
	expectedIdentity, hasExpectedIdentity := queuedStartRecoveryIdentityForTest(expected)
	tr.enqueueTerminalRetry(testID, "FAILED scheduled pre-launch", func() error {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		test := tr.tests[testID]
		if test == nil {
			return nil
		}
		if hasExpectedIdentity {
			if !expectedIdentity.matches(test) {
				return nil
			}
		} else if test != expected {
			return nil
		}
		if test.Status == "FAILED" || test.Status == "COMPLETE" || test.Status == "ABORTED" {
			return nil
		}
		return tr.failScheduledTestBeforeLaunchLocked(test, now, message)
	})
}

func (tr *TestRunner) reconcileScenarioScheduleOccurrenceLocked(schedule *storage.ScenarioSchedule, plannedAt time.Time, now time.Time, leaseTerm executionLeaseTerm) (bool, *storage.ScenarioScheduleOccurrence, *ManagedTest, error) {
	if tr == nil || tr.scenarioStore == nil || schedule == nil {
		return false, nil, nil, nil
	}
	tr.mu.Unlock()
	existing, err := tr.scenarioStore.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt.UTC())
	tr.mu.Lock()
	if err != nil || existing == nil {
		return false, nil, nil, err
	}
	var recoveredPrepared *ManagedTest
	var releaseRecoveredLifecycle func()
	releaseRecoveredStartLifecycle := func() {
		if releaseRecoveredLifecycle != nil {
			releaseRecoveredLifecycle()
		}
	}
	defer releaseRecoveredStartLifecycle()
	var deferredStartRecoveryTest *ManagedTest
	status := strings.ToLower(strings.TrimSpace(existing.Status))
	if status == "queued" && strings.EqualFold(strings.TrimSpace(existing.ReasonCode), "start_retrying") && strings.TrimSpace(existing.TestID) != "" {
		if recovered := tr.tests[existing.TestID]; recovered != nil && strings.EqualFold(strings.TrimSpace(recovered.Status), "SHAKEOUT_REVIEW") {
			// The durable test transition can win immediately before a controller
			// crash or process termination. Converge the process-private recovery
			// coordinator's correlation work on the next ordinary sweep instead of
			// treating this permanently review-gated row as a successful queue.
			testID := recovered.ID
			summary := recovered.Error
			identity, identityOK := queuedStartRecoveryIdentityForTest(recovered)
			tr.mu.Unlock()
			var err error
			if identityOK {
				err = tr.reconcileScenarioScheduleShakeoutReviewForIdentityWithTerm(testID, summary, &identity, leaseTerm)
			} else {
				err = tr.reconcileScenarioScheduleShakeoutReviewWithTerm(testID, summary, leaseTerm)
			}
			tr.mu.Lock()
			return true, nil, nil, err
		} else if recovered != nil && queuedCIRecoveryEligible(recovered) {
			// Acquisition recovery covers an occurrence whose parent has already
			// advanced. This path is the same-occurrence sweep fallback: it repairs a
			// transient acquisition-scan failure without replaying shakeout actions or
			// treating an unrelated manual QUEUED row as scheduler-owned. Return after
			// enqueueing so a fast coordinator cannot make the stale queued projection
			// below overwrite its materialized lifecycle.
			tr.mu.Unlock()
			tr.enqueueQueuedCIRecoveryRetryForManagedTestWithTerm(recovered, leaseTerm)
			tr.mu.Lock()
			return true, nil, nil, nil
		}
	}
	if status == "materialized" && strings.TrimSpace(existing.TestID) != "" {
		// A materialized occurrence with the parent schedule still pointing at the
		// same planned_at is the crash window after test/occurrence persistence but
		// before the schedule advance and launch. Startup recovery fail-closes the
		// orphan INITIALIZING row; do not subsequently reinterpret that terminal
		// test as a successful materialization.
		if recovered := tr.tests[existing.TestID]; recovered != nil {
			switch recovered.Status {
			case "PENDING":
				// Repair rows written by older controllers that labeled a quota-
				// queued run as started.
				markScenarioScheduleOccurrenceQueued(existing, recovered)
				existing.UpdatedAt = now
				if err := tr.saveScenarioScheduleOccurrenceForSweepLocked(existing, leaseTerm); err != nil {
					return true, nil, nil, err
				}
				status = "queued"
			case "INITIALIZING":
				cause := fmt.Errorf("correlated scheduled test %s was left initializing after occurrence materialization", recovered.ID)
				return true, nil, nil, tr.failRecoveredScenarioSchedulePreparationLocked(schedule, existing, recovered, now, "recovery_interrupted", cause.Error(), cause, leaseTerm)
			case "FAILED", "ABORTED":
				existing.Status = "failed"
				existing.ReasonCode = "recovery_interrupted"
				existing.ConflictSummary = firstNonEmpty(recovered.Error, "correlated scheduled test failed before launch recovery committed")
				existing.MaterializedAt = nil
				existing.UpdatedAt = now
				if err := tr.saveScenarioScheduleOccurrenceForSweepLocked(existing, leaseTerm); err != nil {
					return true, nil, nil, err
				}
				status = "failed"
			}
		}
	}
	if status == "" || status == "materializing" {
		// A correlated durable test means the prior controller crossed the test
		// persistence boundary before crashing. Finalize this occurrence and
		// advance the schedule instead of launching a duplicate. If no test row
		// exists, resume materialization with the preassigned ID; this safely covers
		// crashes between occurrence intent and test persistence.
		if strings.TrimSpace(existing.TestID) == "" {
			candidateID := scenarioScheduleOccurrenceMaterializationTestID(existing)
			if tr.tests[candidateID] != nil {
				existing.TestID = candidateID
				existing.UpdatedAt = now
				if err := tr.saveScenarioScheduleOccurrenceForSweepLocked(existing, leaseTerm); err != nil {
					return false, nil, nil, err
				}
			} else {
				return false, cloneScenarioScheduleOccurrence(existing), nil, nil
			}
		}
		if recovered := tr.tests[existing.TestID]; recovered == nil {
			return false, cloneScenarioScheduleOccurrence(existing), nil, nil
		} else if recovered.Status == "QUEUED" {
			// The prior controller persisted the test but crashed before preparing
			// its execution context. Resume that exact row and return it to the
			// scheduler's normal post-lock launch path.
			prepared, releaseLifecycle, err := tr.prepareTestStartForScheduleSweepLockedWithTerm(recovered.ID, StartTestOptions{
				SkipLaunchWindowPreflight:        true,
				deferPostShakeoutRecoveryEnqueue: true,
			}, leaseTerm)
			releaseRecoveredLifecycle = releaseLifecycle
			if err != nil {
				if scenarioScheduleStartRetryableAfterOneShot(recovered, err) {
					markScenarioScheduleOccurrenceStartRetrying(existing, recovered, err)
					existing.UpdatedAt = now
					if saveErr := tr.saveScenarioScheduleOccurrenceForSweepLocked(existing, leaseTerm); saveErr != nil {
						return true, nil, nil, errors.Join(err, saveErr)
					}
					status = "queued"
					deferredStartRecoveryTest = recovered
				} else {
					existing.Status = "failed"
					existing.ReasonCode = "start_failed"
					existing.ConflictSummary = err.Error()
					existing.UpdatedAt = now
					saveErr := tr.saveScenarioScheduleOccurrenceForSweepLocked(existing, leaseTerm)
					releaseRecoveredStartLifecycle()
					failErr := tr.failScheduledTestBeforeLaunchLocked(recovered, now, "resume correlated scheduled test: "+err.Error())
					return true, nil, nil, errors.Join(fmt.Errorf("resume correlated scheduled test %s: %w", recovered.ID, err), saveErr, failErr)
				}
			}
			recoveredPrepared = prepared
			if prepared == nil && recovered.Status == "PENDING" {
				markScenarioScheduleOccurrenceQueued(existing, recovered)
				existing.UpdatedAt = now
				if err := tr.saveScenarioScheduleOccurrenceForSweepLocked(existing, leaseTerm); err != nil {
					return true, nil, nil, err
				}
				status = "queued"
			}
		} else if recovered.Status == "SHAKEOUT" {
			if recovered.pendingScriptShakeoutOutcome == nil {
				cause := fmt.Errorf("correlated scheduled test %s lost process-private shakeout outcome before recovery", recovered.ID)
				return true, nil, nil, tr.failRecoveredScenarioSchedulePreparationLocked(schedule, existing, recovered, now, "recovery_shakeout_evidence_lost", cause.Error(), cause, leaseTerm)
			}
			markScenarioScheduleOccurrenceStartRetrying(existing, recovered, errStartAdmissionEvidenceStale)
			existing.UpdatedAt = now
			if err := tr.saveScenarioScheduleOccurrenceForSweepLocked(existing, leaseTerm); err != nil {
				return true, nil, nil, err
			}
			status = "queued"
			deferredStartRecoveryTest = recovered
		} else if recovered.Status == "PENDING" {
			// The quota transition committed before the prior controller could
			// update its materializing occurrence. Converge without re-preparing.
			markScenarioScheduleOccurrenceQueued(existing, recovered)
			existing.UpdatedAt = now
			if err := tr.saveScenarioScheduleOccurrenceForSweepLocked(existing, leaseTerm); err != nil {
				return true, nil, nil, err
			}
			status = "queued"
		} else if recovered.Status == "INITIALIZING" {
			// There is no safe commit point from which an INITIALIZING test can be
			// resumed: after restart its execution context is gone, while in this
			// process it may be the residue of a failed occurrence/schedule write.
			// Fail it closed instead of converting the occurrence to materialized
			// and either stranding or launching an ambiguously prepared run.
			cause := fmt.Errorf("correlated scheduled test %s was left initializing before schedule recovery committed", recovered.ID)
			return true, nil, nil, tr.failRecoveredScenarioSchedulePreparationLocked(schedule, existing, recovered, now, "recovery_initializing_test", cause.Error(), cause, leaseTerm)
		} else if recovered.Status == "FAILED" || recovered.Status == "ABORTED" {
			// A prior recovery attempt may have durably failed the test but lost the
			// occurrence update. Repair that half-written state idempotently.
			existing.Status = "failed"
			existing.ReasonCode = "recovered_test_failed"
			existing.ConflictSummary = firstNonEmpty(recovered.Error, "correlated scheduled test is terminal")
			existing.UpdatedAt = now
			if err := tr.saveScenarioScheduleOccurrenceForSweepLocked(existing, leaseTerm); err != nil {
				return true, nil, nil, err
			}
			status = "failed"
		}
		if status != "failed" && status != "queued" {
			existing.Status = "materialized"
			existing.ReasonCode = "recovered"
			if existing.MaterializedAt == nil {
				materializedAt := now
				existing.MaterializedAt = &materializedAt
			}
			existing.UpdatedAt = now
			if err := tr.saveScenarioScheduleOccurrenceForSweepLocked(existing, leaseTerm); err != nil {
				if recoveredPrepared != nil {
					releaseRecoveredStartLifecycle()
					return true, nil, nil, tr.failRecoveredScenarioSchedulePreparationLocked(schedule, existing, recoveredPrepared, now, "recovery_occurrence_persist_failed", "save recovered materialized schedule occurrence: "+err.Error(), err, leaseTerm)
				}
				return true, nil, nil, err
			}
			status = "materialized"
		}
	}
	if status == "blocked" && existing.ReasonCode == "approval_required" {
		// Approval requests are revision-scoped. A schedule edit may replace the
		// request, remove the policy, or resolve the request while this exact
		// occurrence remains the durable planned_at anchor. Reconcile the current
		// parent state without ever reattaching a rejected/superseded historical ID.
		loadApproval := func(id string) (*storage.ScenarioScheduleApproval, error) {
			if strings.TrimSpace(id) == "" {
				return nil, nil
			}
			tr.mu.Unlock()
			approval, loadErr := tr.scenarioStore.GetScenarioScheduleApproval(id)
			tr.mu.Lock()
			return approval, loadErr
		}
		resume := func(reason string) (bool, *storage.ScenarioScheduleOccurrence, *ManagedTest, error) {
			existing.Status = "materializing"
			existing.ReasonCode = reason
			existing.ConflictSummary = ""
			existing.TestID = ""
			existing.UpdatedAt = now
			if err := tr.saveScenarioScheduleOccurrenceForSweepLocked(existing, leaseTerm); err != nil {
				return true, nil, nil, err
			}
			return false, cloneScenarioScheduleOccurrence(existing), nil, nil
		}
		markBlocked := func(reasonCode, summary, requiredAction string) error {
			existing.Status = "blocked"
			existing.ReasonCode = reasonCode
			existing.ConflictSummary = summary
			existing.TestID = ""
			existing.UpdatedAt = now
			if err := tr.saveScenarioScheduleOccurrenceForSweepLocked(existing, leaseTerm); err != nil {
				return err
			}
			status = "blocked"
			schedule.PendingApprovalID = ""
			schedule.NextRequiredAction = requiredAction
			schedule.LastError = summary
			return nil
		}

		currentApprovalID := strings.TrimSpace(schedule.PendingApprovalID)
		currentApprovalRequired := schedule.NextRequiredAction == "approval_required"
		if currentApprovalRequired && currentApprovalID == "" {
			summary := "schedule requires approval but has no current approval request"
			if err := markBlocked("approval_state_inconsistent", summary, "operator_decision_required"); err != nil {
				return true, nil, nil, err
			}
		} else if currentApprovalRequired {
			approval, approvalErr := loadApproval(currentApprovalID)
			if approvalErr != nil {
				return true, nil, nil, approvalErr
			}
			approvalStatus := ""
			if approval != nil && approval.ScheduleID == schedule.ID {
				approvalStatus = strings.ToLower(strings.TrimSpace(approval.Status))
			}
			switch approvalStatus {
			case "pending":
				if existing.ApprovalID != currentApprovalID {
					existing.ApprovalID = currentApprovalID
					existing.ConflictSummary = "Schedule requires approval before materialization."
					existing.UpdatedAt = now
					if err := tr.saveScenarioScheduleOccurrenceForSweepLocked(existing, leaseTerm); err != nil {
						return true, nil, nil, err
					}
				}
			case "approved":
				schedule.PendingApprovalID = ""
				schedule.NextRequiredAction = ""
				schedule.LastError = ""
				return resume("approval_granted")
			case "rejected":
				if err := markBlocked("approval_rejected", "schedule approval rejected", "approval_rejected"); err != nil {
					return true, nil, nil, err
				}
			default:
				summary := "current schedule approval is missing or no longer pending"
				if err := markBlocked("approval_state_inconsistent", summary, "operator_decision_required"); err != nil {
					return true, nil, nil, err
				}
			}
		} else {
			approval, approvalErr := loadApproval(existing.ApprovalID)
			if approvalErr != nil {
				return true, nil, nil, approvalErr
			}
			approvalStatus := ""
			if approval != nil && approval.ScheduleID == schedule.ID {
				approvalStatus = strings.ToLower(strings.TrimSpace(approval.Status))
			}
			switch approvalStatus {
			case "approved":
				return resume("approval_granted")
			case "rejected":
				if err := markBlocked("approval_rejected", "schedule approval rejected", "approval_rejected"); err != nil {
					return true, nil, nil, err
				}
			case "pending", "superseded":
				// The parent no longer requires this revision-scoped request.
				return resume("approval_no_longer_required")
			default:
				summary := "blocked occurrence references a missing or invalid approval request"
				if err := markBlocked("approval_state_inconsistent", summary, "operator_decision_required"); err != nil {
					return true, nil, nil, err
				}
			}
		}
	}
	if status == "blocked" && schedule.LastOccurrence != nil && schedule.LastOccurrence.ID == existing.ID &&
		schedule.LastOccurrence.Revision == existing.Revision {
		switch existing.ReasonCode {
		case "approval_required":
			if schedule.NextRequiredAction == "approval_required" && schedule.PendingApprovalID == existing.ApprovalID {
				return true, nil, nil, nil
			}
		case "approval_rejected":
			if schedule.NextRequiredAction == "approval_rejected" && schedule.PendingApprovalID == "" {
				return true, nil, nil, nil
			}
		case "approval_state_inconsistent":
			if schedule.NextRequiredAction == "operator_decision_required" && schedule.PendingApprovalID == "" {
				return true, nil, nil, nil
			}
		}
	}
	if scenarioScheduleOccurrenceProjectionUnchanged(schedule, existing, status) {
		return true, nil, nil, nil
	}
	schedule.LastOccurrence = cloneScenarioScheduleOccurrence(existing)
	schedule.UpdatedAt = now
	switch status {
	case "materialized", "queued", "completed", "approved":
		if existing.TestID != "" {
			schedule.LastRunTestID = existing.TestID
		}
		if existing.MaterializedAt != nil {
			materializedAt := *existing.MaterializedAt
			schedule.LastRunAt = &materializedAt
		}
		advanceFrom := now
		if normalizeScenarioScheduleMissedRunPolicy(schedule.MissedRunPolicy) == "catch_up_all" && existing.PlannedAt.Before(now) {
			advanceFrom = existing.PlannedAt
		}
		next, err := nextScenarioScheduleRun(schedule, advanceFrom)
		if err != nil {
			if recoveredPrepared != nil {
				releaseRecoveredStartLifecycle()
				return true, nil, nil, tr.failRecoveredScenarioSchedulePreparationLocked(schedule, existing, recoveredPrepared, now, "recovery_schedule_calculation_failed", "calculate next run for recovered schedule: "+err.Error(), err, leaseTerm)
			}
			return true, nil, nil, err
		}
		schedule.NextRunAt = next
		schedule.NextRequiredAction = ""
		schedule.LastError = ""
	case "skipped":
		next, err := nextScenarioScheduleRun(schedule, now)
		if err != nil {
			return true, nil, nil, err
		}
		schedule.NextRunAt = next
		schedule.NextRequiredAction = ""
		schedule.LastError = ""
	case "blocked":
		schedule.LastError = firstNonEmpty(existing.ConflictSummary, existing.ReasonCode)
		switch existing.ReasonCode {
		case "approval_required":
			schedule.NextRequiredAction = "approval_required"
			schedule.PendingApprovalID = existing.ApprovalID
		case "approval_rejected":
			schedule.PendingApprovalID = ""
			schedule.NextRequiredAction = "approval_rejected"
		case "approval_state_inconsistent":
			schedule.PendingApprovalID = ""
			schedule.NextRequiredAction = "operator_decision_required"
		case "schedule_conflict":
			schedule.NextRequiredAction = "resolve_conflict"
		case "missed_run_requires_decision":
			schedule.NextRequiredAction = "operator_decision_required"
		case "script_shakeout_review":
			schedule.NextRequiredAction = "operator_decision_required"
		}
	case "failed":
		schedule.LastError = firstNonEmpty(existing.ConflictSummary, existing.ReasonCode)
		schedule.NextRequiredAction = "operator_decision_required"
	default:
		return false, nil, nil, nil
	}
	if err := tr.saveScenarioScheduleForSweepLocked(schedule, leaseTerm); err != nil {
		if recoveredPrepared != nil {
			releaseRecoveredStartLifecycle()
			return true, nil, nil, tr.failRecoveredScenarioSchedulePreparationLocked(schedule, existing, recoveredPrepared, now, "recovery_schedule_persist_failed", "save recovered schedule: "+err.Error(), err, leaseTerm)
		}
		return true, nil, nil, err
	}
	if deferredStartRecoveryTest != nil {
		tr.mu.Unlock()
		tr.enqueueQueuedCIRecoveryRetryForManagedTestWithTerm(deferredStartRecoveryTest, leaseTerm)
		tr.mu.Lock()
		if !tr.executionLeaseTermCurrent(leaseTerm) {
			return true, nil, nil, errNotExecutionLeader
		}
	}
	return true, nil, recoveredPrepared, nil
}

// scenarioScheduleOccurrenceProjectionUnchanged recognizes every terminal
// blocked/failed projection, not only approval-related reasons. Keeping this
// check pure makes a scheduler sweep idempotent: an unresolved occurrence does
// not continuously rewrite the schedule and invalidate an operator's revision.
func scenarioScheduleOccurrenceProjectionUnchanged(schedule *storage.ScenarioSchedule, existing *storage.ScenarioScheduleOccurrence, status string) bool {
	if schedule == nil || existing == nil || schedule.LastOccurrence == nil ||
		schedule.LastOccurrence.ID != existing.ID || schedule.LastOccurrence.Revision != existing.Revision {
		return false
	}
	expectedError := firstNonEmpty(existing.ConflictSummary, existing.ReasonCode)
	expectedAction := schedule.NextRequiredAction
	expectedApprovalID := schedule.PendingApprovalID
	switch status {
	case "blocked":
		switch existing.ReasonCode {
		case "approval_required":
			expectedAction = "approval_required"
			expectedApprovalID = existing.ApprovalID
		case "approval_rejected":
			expectedAction = "approval_rejected"
			expectedApprovalID = ""
		case "approval_state_inconsistent", "missed_run_requires_decision", "script_shakeout_review":
			expectedAction = "operator_decision_required"
			if existing.ReasonCode == "approval_state_inconsistent" {
				expectedApprovalID = ""
			}
		case "schedule_conflict", "environment_unavailable", "timeslot_unavailable":
			expectedAction = "resolve_conflict"
		default:
			// The block writer persists its reason-specific action together with
			// LastOccurrence. For a future/extension reason that has no canonical
			// mapping here, preserve that already-projected action rather than
			// rewriting the same occurrence every sweep.
		}
	case "failed":
		expectedAction = "operator_decision_required"
	default:
		return false
	}
	return schedule.LastError == expectedError && schedule.NextRequiredAction == expectedAction && schedule.PendingApprovalID == expectedApprovalID
}

// failRecoveredScenarioSchedulePreparationLocked closes every durable boundary
// crossed while recovering a materializing occurrence. It makes the occurrence
// and schedule operator-visible before failing the prepared test; each write is
// independently retryable by a later sweep, so a partial failure converges on
// the same terminal state without ever returning the test for launch.
func (tr *TestRunner) failRecoveredScenarioSchedulePreparationLocked(schedule *storage.ScenarioSchedule, occurrence *storage.ScenarioScheduleOccurrence, test *ManagedTest, now time.Time, reasonCode, message string, cause error, leaseTerm executionLeaseTerm) error {
	if occurrence == nil {
		return errors.Join(cause, tr.failScheduledTestBeforeLaunchLocked(test, now, message))
	}
	occurrence.Status = "failed"
	occurrence.ReasonCode = reasonCode
	occurrence.ConflictSummary = message
	occurrence.UpdatedAt = now
	occurrenceErr := tr.saveScenarioScheduleOccurrenceForSweepLocked(occurrence, leaseTerm)

	var scheduleErr error
	if schedule != nil {
		schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrence)
		schedule.LastError = message
		schedule.NextRequiredAction = "operator_decision_required"
		schedule.UpdatedAt = now
		scheduleErr = tr.saveScenarioScheduleForSweepLocked(schedule, leaseTerm)
	}
	testErr := tr.failScheduledTestBeforeLaunchLocked(test, now, message)
	return errors.Join(cause, occurrenceErr, scheduleErr, testErr)
}

// blockScheduleOccurrenceLocked records a "blocked" occurrence for schedule at
// plannedAt, persists it, then updates the schedule's LastOccurrence / LastError /
// UpdatedAt (and NextRequiredAction when non-empty). The optional mutate hook lets
// callers set extra occurrence fields (e.g. ApprovalID) before persisting. It
// returns the first persistence error encountered.
func (tr *TestRunner) blockScheduleOccurrenceLocked(schedule *storage.ScenarioSchedule, plannedAt time.Time, reasonCode, summary, nextRequiredAction, lastError string, now time.Time, mutate func(*storage.ScenarioScheduleOccurrence), leaseTerm executionLeaseTerm) error {
	// Launch/timeslot conflicts are discovered after the durable materializing
	// claim has been inserted. Transition that exact duplicate fence with CAS;
	// never try to create a second row for the same (schedule, planned_at).
	tr.mu.Unlock()
	occurrence, loadErr := tr.scenarioStore.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt.UTC())
	tr.mu.Lock()
	if loadErr != nil {
		return loadErr
	}
	if occurrence == nil {
		occurrence = tr.buildScenarioScheduleOccurrence(schedule, plannedAt, "blocked", reasonCode, summary, nil, now)
	} else {
		occurrence.Status = "blocked"
		occurrence.ReasonCode = reasonCode
		occurrence.ConflictSummary = summary
		occurrence.TestID = ""
		occurrence.MaterializedAt = nil
		occurrence.UpdatedAt = now
	}
	if mutate != nil {
		mutate(occurrence)
	}
	if err := tr.saveScenarioScheduleOccurrenceForSweepLocked(occurrence, leaseTerm); err != nil {
		return err
	}
	schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrence)
	if nextRequiredAction != "" {
		schedule.NextRequiredAction = nextRequiredAction
	}
	schedule.LastError = lastError
	schedule.UpdatedAt = now
	return tr.saveScenarioScheduleForSweepLocked(schedule, leaseTerm)
}

func (tr *TestRunner) buildScenarioScheduleOccurrence(schedule *storage.ScenarioSchedule, plannedAt time.Time, status string, reasonCode string, conflictSummary string, materializedAt *time.Time, now time.Time) *storage.ScenarioScheduleOccurrence {
	return &storage.ScenarioScheduleOccurrence{
		ID:               uuid.New().String(),
		ScheduleID:       schedule.ID,
		ScenarioID:       schedule.ScenarioID,
		ProjectID:        normalizeProjectID(schedule.ProjectID),
		ScheduleRevision: schedule.Revision,
		TransitionToken:  uuid.NewString(),
		PlannedAt:        plannedAt.UTC(),
		MaterializedAt:   materializedAt,
		Status:           status,
		ReasonCode:       reasonCode,
		TestID:           "",
		ConflictSummary:  conflictSummary,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

// markScenarioScheduleOccurrenceQueued records that a due occurrence produced
// a durable run but did not cross the execution boundary. In particular,
// quota admission transitions the run to PENDING; the calendar must not claim
// it started or assign a materialization timestamp while it waits.
func markScenarioScheduleOccurrenceQueued(occurrence *storage.ScenarioScheduleOccurrence, test *ManagedTest) {
	if occurrence == nil {
		return
	}
	occurrence.Status = "queued"
	occurrence.ReasonCode = "quota_pending"
	occurrence.MaterializedAt = nil
	if test == nil {
		return
	}
	occurrence.TestID = test.ID
	occurrence.ConflictSummary = strings.TrimSpace(test.PendingReason)
}

func cloneScenarioSchedule(schedule *storage.ScenarioSchedule) *storage.ScenarioSchedule {
	if schedule == nil {
		return nil
	}
	cloned := *schedule
	if schedule.LastRunAt != nil {
		lastRun := *schedule.LastRunAt
		cloned.LastRunAt = &lastRun
	}
	if schedule.NextRunAt != nil {
		nextRun := *schedule.NextRunAt
		cloned.NextRunAt = &nextRun
	}
	if schedule.DisabledFrom != nil {
		disabledFrom := *schedule.DisabledFrom
		cloned.DisabledFrom = &disabledFrom
	}
	if schedule.DisabledUntil != nil {
		disabledUntil := *schedule.DisabledUntil
		cloned.DisabledUntil = &disabledUntil
	}
	cloned.BlackoutWindows = cloneScenarioScheduleWindows(schedule.BlackoutWindows)
	cloned.MaintenanceWindows = cloneScenarioScheduleWindows(schedule.MaintenanceWindows)
	cloned.AvailabilityWindows = cloneScenarioScheduleWindows(schedule.AvailabilityWindows)
	if schedule.Governance != nil {
		governance := *schedule.Governance
		governance.RBACReview.RequiredRoles = append([]string(nil), schedule.Governance.RBACReview.RequiredRoles...)
		governance.QuotaReview.Findings = append([]string(nil), schedule.Governance.QuotaReview.Findings...)
		cloned.Governance = &governance
	}
	cloned.LastOccurrence = cloneScenarioScheduleOccurrence(schedule.LastOccurrence)
	return &cloned
}

func cloneScenarioScheduleWindows(windows []storage.ScenarioScheduleWindow) []storage.ScenarioScheduleWindow {
	if len(windows) == 0 {
		return nil
	}
	cloned := append([]storage.ScenarioScheduleWindow(nil), windows...)
	for index := range cloned {
		cloned[index].DaysOfWeek = append([]int(nil), windows[index].DaysOfWeek...)
	}
	return cloned
}

func cloneScenarioScheduleOccurrence(occurrence *storage.ScenarioScheduleOccurrence) *storage.ScenarioScheduleOccurrence {
	if occurrence == nil {
		return nil
	}
	cloned := *occurrence
	if occurrence.MaterializedAt != nil {
		materializedAt := *occurrence.MaterializedAt
		cloned.MaterializedAt = &materializedAt
	}
	return &cloned
}

type scenarioScheduleLifecycleSnapshot struct {
	schedule   *storage.ScenarioSchedule
	occurrence *storage.ScenarioScheduleOccurrence
}

// loadScenarioScheduleLifecycleForTest reads the already-atomic durable
// test/occurrence/schedule transition without holding the runner mutex. The
// calendar projection is latest-by-planned-at: the test-correlated occurrence
// identifies the parent, but an older run finishing later must not replace a
// newer planned occurrence in the schedule cache.
func (tr *TestRunner) loadScenarioScheduleLifecycleForTest(testID string) (*scenarioScheduleLifecycleSnapshot, error) {
	if tr == nil || tr.scenarioStore == nil || strings.TrimSpace(testID) == "" {
		return nil, nil
	}
	store, ok := tr.scenarioStore.(scenarioScheduleOccurrenceByTestStore)
	if !ok {
		return nil, nil
	}
	occurrence, err := store.GetScenarioScheduleOccurrenceByTestID(testID)
	if err != nil {
		return nil, err
	}
	if occurrence == nil {
		return nil, nil
	}
	schedule, err := tr.scenarioStore.GetScenarioSchedule(occurrence.ScheduleID)
	if err != nil {
		return nil, err
	}
	if schedule == nil {
		return nil, nil
	}
	latest := occurrence
	if latestStore, ok := tr.scenarioStore.(latestScenarioScheduleOccurrenceStore); ok {
		latest, err = latestStore.LatestScenarioScheduleOccurrence(schedule.ID)
		if err != nil {
			return nil, err
		}
		if latest == nil {
			latest = occurrence
		}
	}
	return &scenarioScheduleLifecycleSnapshot{
		schedule:   cloneScenarioSchedule(schedule),
		occurrence: cloneScenarioScheduleOccurrence(latest),
	}, nil
}

// syncScenarioScheduleLifecycleForTest mirrors a durable lifecycle snapshot
// into this controller's authoring cache. Storage I/O happens before taking
// tr.mu so a slow or unavailable database cannot stall all runner operations.
// Durable state remains authoritative if the read fails; HA readers and the
// next leadership refresh will converge independently.
func (tr *TestRunner) syncScenarioScheduleLifecycleForTest(testID string) error {
	snapshot, err := tr.loadScenarioScheduleLifecycleForTest(testID)
	if err != nil || snapshot == nil || snapshot.schedule == nil {
		return err
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	schedule := snapshot.schedule
	scenario := tr.scenarios[schedule.ScenarioID]
	if scenario == nil {
		return nil
	}
	for index, current := range scenario.Schedules {
		if current == nil || current.ID != schedule.ID {
			continue
		}
		if schedule.Revision < current.Revision {
			return nil
		}
		schedule.LastOccurrence = cloneScenarioScheduleOccurrence(snapshot.occurrence)
		previous := scenario.Schedules[index]
		scenario.Schedules[index] = cloneScenarioSchedule(schedule)
		tr.contentRevision++
		tr.adjustSchedulerReadinessLocked(previous, schedule)
		return nil
	}
	return nil
}

// scenarioScheduleIDForTest resolves the durable parent identity outside the
// runner mutex. Lookup failures are admission failures, not "no schedule": the
// latter would revalidate an occurrence against its own recurring projection
// and could terminalize an otherwise valid queued run.
func (tr *TestRunner) scenarioScheduleIDForTest(testID string) (string, error) {
	if tr == nil || tr.scenarioStore == nil || strings.TrimSpace(testID) == "" {
		return "", nil
	}
	store, ok := tr.scenarioStore.(scenarioScheduleOccurrenceByTestStore)
	if !ok {
		return "", nil
	}
	occurrence, err := store.GetScenarioScheduleOccurrenceByTestID(testID)
	if err != nil {
		return "", err
	}
	if occurrence == nil {
		return "", nil
	}
	return strings.TrimSpace(occurrence.ScheduleID), nil
}

func (tr *TestRunner) CreateScenarioSchedule(scenarioID string, req CreateScenarioScheduleRequest) (*storage.ScenarioSchedule, error) {
	tr.refreshScenariosForHARead(true)
	quotaAt := time.Now().UTC()
	activeSlots, reservationErr := tr.loadBoundTimeslotsActiveAt(quotaAt)
	projectQuota := tr.loadScenarioProjectQuotaEvidence(scenarioID)
	releaseEvidence := loadScenarioScheduleReleaseEvidence(tr.releaseStore, req.ReleaseID)
	capacityEvidence := tr.loadScenarioScheduleCapacityEvidence(scenarioID, req.Name, projectQuota)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.upsertScenarioScheduleLocked(scenarioID, uuid.New().String(), nil, req, quotaAt, activeSlots, reservationErr, projectQuota, releaseEvidence, capacityEvidence)
}

type scenarioScheduleReleaseEvidence struct {
	releaseID       string
	resolvedID      string
	resolvedName    string
	archived        bool
	storeConfigured bool
	loaded          bool
	err             error
}

func loadScenarioScheduleReleaseEvidence(store releasePersistence, releaseID string) scenarioScheduleReleaseEvidence {
	releaseID = strings.TrimSpace(releaseID)
	evidence := scenarioScheduleReleaseEvidence{releaseID: releaseID, storeConfigured: store != nil, loaded: true}
	if store == nil || releaseID == "" {
		return evidence
	}
	release, err := store.GetRelease(releaseID)
	if err != nil {
		evidence.err = err
		return evidence
	}
	if release == nil {
		evidence.err = sql.ErrNoRows
		return evidence
	}
	evidence.resolvedID = release.ID
	evidence.resolvedName = release.Name
	evidence.archived = release.ArchivedAt != nil
	return evidence
}

func scenarioScheduleReleaseConflictFindings(schedule *storage.ScenarioSchedule, evidence scenarioScheduleReleaseEvidence) []string {
	if schedule == nil || strings.TrimSpace(schedule.ReleaseID) == "" || !evidence.storeConfigured {
		return nil
	}
	releaseID := strings.TrimSpace(schedule.ReleaseID)
	if !evidence.loaded || evidence.releaseID != releaseID {
		return []string{"Release conflict: release evidence changed while the schedule was evaluated; retry."}
	}
	if evidence.err != nil || evidence.resolvedID == "" {
		return []string{fmt.Sprintf("Release conflict: release %s could not be resolved.", releaseID)}
	}
	if evidence.archived {
		return []string{fmt.Sprintf("Release conflict: release %s is archived.", firstNonEmpty(evidence.resolvedName, evidence.resolvedID))}
	}
	return nil
}

func (tr *TestRunner) loadScenarioProjectQuotaEvidence(scenarioID string) projectQuotaEvidence {
	tr.mu.RLock()
	scenario := tr.scenarios[tr.resolveRefKeyLocked(scenarioID)]
	projectID := ""
	poolID := ""
	if scenario != nil {
		projectID = scenario.ProjectID
		if scenario.Assignment != nil {
			poolID = scenario.Assignment.PoolID
		}
	}
	projectStore := tr.projectStore
	tr.mu.RUnlock()
	if scenario == nil {
		return projectQuotaEvidence{}
	}
	return loadProjectQuotaEvidence(projectStore, projectID, poolID)
}

type scenarioScheduleCapacityEvidence struct {
	preview  *ManagedTest
	revision managedTestBuildFence
	loaded   bool
	err      error
}

func (tr *TestRunner) loadScenarioScheduleCapacityEvidence(scenarioID, name string, projectQuota projectQuotaEvidence) scenarioScheduleCapacityEvidence {
	req := CreateTestRequest{
		Name:       firstNonEmpty(strings.TrimSpace(name), "schedule capacity preview"),
		ScenarioID: scenarioID,
	}
	// The schedule entry point already loaded project quota once. Reuse that
	// immutable evidence while compiling the private preview so the off-lock
	// refactor does not turn one admission snapshot into duplicate store reads.
	req.projectQuotaEvidence = projectQuota
	preview, _, revision, err := tr.buildManagedTestFromSnapshot(req, "schedule-conflict-preview", "DRAFT")
	return scenarioScheduleCapacityEvidence{preview: preview, revision: revision, loaded: true, err: err}
}

// ScenarioSchedulePreview is the result of a (non-persisting) schedule preview:
// the next N UTC fire times for a candidate cron/timezone/window configuration,
// plus a pre-save conflict summary (empty when the schedule would materialize
// cleanly) computed with the same evaluator the materialization sweep uses, so
// operators can fix a quota/capacity/release conflict before saving instead of
// discovering blocked occurrences on the calendar afterward.
type ScenarioSchedulePreview struct {
	CronExpr        string      `json:"cron_expr"`
	Timezone        string      `json:"timezone"`
	Occurrences     []time.Time `json:"occurrences"`
	Count           int         `json:"count"`
	ConflictSummary string      `json:"conflict_summary,omitempty"`
}

// PreviewScenarioScheduleRuns computes the next `count` fire times for a candidate
// schedule WITHOUT persisting anything. It reuses the production evaluator
// (nextScenarioScheduleRun + scheduleAllowsCandidate), so the preview honors the
// declared timezone, DST, POSIX day-of-month/day-of-week OR semantics, the
// Quartz day specifiers, blackout/maintenance/availability windows, and the
// date-range pause exactly as the scheduler will at runtime — the preview cannot
// drift from real behavior.
func (tr *TestRunner) PreviewScenarioScheduleRuns(scenarioID string, req CreateScenarioScheduleRequest, count int) (*ScenarioSchedulePreview, error) {
	if _, ok := tr.GetScenario(scenarioID); !ok {
		return nil, fmt.Errorf("scenario not found: %s", scenarioID)
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
	if err := validateScenarioScheduleWindowSets(req.BlackoutWindows, req.MaintenanceWindows, req.AvailabilityWindows, tz); err != nil {
		return nil, err
	}
	if count <= 0 {
		count = 5
	}
	if count > 50 {
		count = 50
	}
	disabledFrom := normalizeSchedulePauseBound(req.DisabledFrom)
	disabledUntil := normalizeSchedulePauseBound(req.DisabledUntil)
	if disabledFrom != nil && disabledUntil != nil && !disabledFrom.Before(*disabledUntil) {
		return nil, fmt.Errorf("disabled_from must be before disabled_until")
	}
	schedule := &storage.ScenarioSchedule{
		ScenarioID:          scenarioID,
		Name:                strings.TrimSpace(req.Name),
		ReleaseID:           req.ReleaseID,
		CronExpr:            cronExpr,
		Timezone:            tz,
		BlackoutWindows:     req.BlackoutWindows,
		MaintenanceWindows:  req.MaintenanceWindows,
		AvailabilityWindows: req.AvailabilityWindows,
		DisabledFrom:        disabledFrom,
		DisabledUntil:       disabledUntil,
	}
	occurrences := make([]time.Time, 0, count)
	cursor := time.Now().UTC()
	if !executionPlanSchedulePausedIndefinitely(disabledFrom, disabledUntil, cursor) {
		for i := 0; i < count; i++ {
			next, err := nextScenarioScheduleRun(schedule, cursor)
			if err != nil || next == nil {
				break
			}
			occurrences = append(occurrences, *next)
			// nextCronRun is strictly-after. Reusing the returned instant avoids
			// skipping every second occurrence for one-minute schedules.
			cursor = *next
		}
	}
	// Pre-save conflict check: surface the same quota/capacity/release conflict the
	// materialization sweep would record, so it can be fixed before saving. Uses
	// the production evaluator under a read lock (it builds a non-registered preview
	// test, exactly like the estimate/preflight paths).
	conflictSummary := ""
	quotaAt := time.Now().UTC()
	activeSlots, reservationErr := tr.loadBoundTimeslotsActiveAt(quotaAt)
	projectQuota := tr.loadScenarioProjectQuotaEvidence(scenarioID)
	releaseEvidence := loadScenarioScheduleReleaseEvidence(tr.releaseStore, req.ReleaseID)
	capacityEvidence := tr.loadScenarioScheduleCapacityEvidence(scenarioID, req.Name, projectQuota)
	func() {
		tr.mu.RLock()
		defer tr.mu.RUnlock()
		scenario, ok := tr.scenarios[tr.resolveRefKeyLocked(scenarioID)]
		if !ok {
			return
		}
		conflictSummary = tr.previewScheduleConflictSummaryWithTimeslotsLocked(schedule, scenario, quotaAt, activeSlots, reservationErr, projectQuota, releaseEvidence, capacityEvidence)
	}()
	return &ScenarioSchedulePreview{CronExpr: cronExpr, Timezone: tz, Occurrences: occurrences, Count: len(occurrences), ConflictSummary: conflictSummary}, nil
}

func (tr *TestRunner) UpdateScenarioSchedule(scenarioID string, scheduleID string, req CreateScenarioScheduleRequest) (*storage.ScenarioSchedule, bool, error) {
	tr.refreshScenariosForHARead(true)
	quotaAt := time.Now().UTC()
	activeSlots, reservationErr := tr.loadBoundTimeslotsActiveAt(quotaAt)
	projectQuota := tr.loadScenarioProjectQuotaEvidence(scenarioID)
	releaseEvidence := loadScenarioScheduleReleaseEvidence(tr.releaseStore, req.ReleaseID)
	capacityEvidence := tr.loadScenarioScheduleCapacityEvidence(scenarioID, req.Name, projectQuota)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		return nil, false, nil
	}
	for _, schedule := range scenario.Schedules {
		if schedule != nil && schedule.ID == scheduleID {
			updated, err := tr.upsertScenarioScheduleLocked(scenarioID, scheduleID, schedule, req, quotaAt, activeSlots, reservationErr, projectQuota, releaseEvidence, capacityEvidence)
			return updated, true, err
		}
	}
	return nil, false, nil
}

func (tr *TestRunner) ListScenarioSchedules(scenarioID string) ([]*storage.ScenarioSchedule, bool) {
	tr.refreshScenariosForHARead(true)
	tr.mu.RLock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		tr.mu.RUnlock()
		return nil, false
	}
	out := make([]*storage.ScenarioSchedule, 0, len(scenario.Schedules))
	for _, schedule := range scenario.Schedules {
		out = append(out, cloneScenarioSchedule(schedule))
	}
	store := tr.scenarioStore
	tr.mu.RUnlock()
	for _, schedule := range out {
		attachLatestScenarioScheduleOccurrence(store, schedule)
	}
	return out, true
}

func attachLatestScenarioScheduleOccurrence(store scenarioSchedulePersistence, schedule *storage.ScenarioSchedule) {
	if schedule == nil || store == nil {
		return
	}
	if latestStore, ok := store.(latestScenarioScheduleOccurrenceStore); ok {
		occurrence, err := latestStore.LatestScenarioScheduleOccurrence(schedule.ID)
		if err == nil && occurrence != nil {
			schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrence)
		}
		return
	}
	occurrences, err := store.ListScenarioScheduleOccurrences(schedule.ID)
	if err != nil || len(occurrences) == 0 {
		return
	}
	schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrences[0])
}

func (tr *TestRunner) DeleteScenarioSchedule(scenarioID string, scheduleID string) (bool, error) {
	tr.refreshScenariosForHARead(true)
	tr.mu.Lock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		tr.mu.Unlock()
		return false, nil
	}
	found := false
	for _, schedule := range scenario.Schedules {
		if schedule != nil && schedule.ID == scheduleID {
			found = true
			break
		}
	}
	if !found {
		tr.mu.Unlock()
		return false, nil
	}
	store := tr.scenarioStore
	tr.mu.Unlock()
	if store != nil {
		var result storage.ScheduleDeleteResult
		var err error
		// Metadata deletion is replica-safe. The guarded transaction serializes
		// with the leader's current-parent occurrence claim: either the claim wins
		// and deletion reports a conflict, or deletion wins and the claim sees a
		// missing parent.
		if guardedStore, ok := store.(guardedScenarioScheduleDeletePersistence); ok {
			result, err = guardedStore.DeleteScenarioScheduleGuarded(scheduleID)
		} else {
			err = store.DeleteScenarioSchedule(scheduleID)
			if err == nil {
				result = storage.ScheduleDeleteDeleted
			}
		}
		if err != nil {
			return true, err
		}
		switch result {
		case storage.ScheduleDeleteBlockedByActiveOccurrence:
			return true, errScheduleHasActiveOccurrence
		case storage.ScheduleDeleteMissing:
			return false, nil
		}
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	scenario, ok = tr.scenarios[scenarioID]
	if !ok {
		return true, nil
	}
	filtered := make([]*storage.ScenarioSchedule, 0, len(scenario.Schedules))
	var removed *storage.ScenarioSchedule
	for _, schedule := range scenario.Schedules {
		if schedule != nil && schedule.ID == scheduleID {
			removed = schedule
			continue
		}
		filtered = append(filtered, schedule)
	}
	if removed == nil {
		return true, nil
	}
	scenario.Schedules = append([]*storage.ScenarioSchedule(nil), filtered...)
	tr.contentRevision++
	tr.adjustSchedulerReadinessLocked(removed, nil)
	return true, nil
}

func (tr *TestRunner) upsertScenarioScheduleLocked(scenarioID string, scheduleID string, existing *storage.ScenarioSchedule, req CreateScenarioScheduleRequest, quotaAt time.Time, activeSlots []*storage.CapacityReservation, reservationErr error, projectQuota projectQuotaEvidence, releaseEvidence scenarioScheduleReleaseEvidence, capacityEvidence scenarioScheduleCapacityEvidence) (*storage.ScenarioSchedule, error) {
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		return nil, fmt.Errorf("scenario not found: %s", scenarioID)
	}
	quotaProbe := &ManagedTest{ProjectID: scenario.ProjectID, Assignment: scenario.Assignment}
	if _, ok := projectQuota.quotaFor(quotaProbe); !ok {
		return nil, fmt.Errorf("scenario project or pool assignment changed while quota evidence was loaded; retry")
	}
	if existing != nil && req.ExpectedRevision != nil && *req.ExpectedRevision != existing.Revision {
		return nil, fmt.Errorf("%w: expected revision %d, current revision %d", errScheduleConcurrentChange, *req.ExpectedRevision, existing.Revision)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("schedule name is required")
	}
	cronExpr := strings.TrimSpace(req.CronExpr)
	if cronExpr == "" {
		return nil, fmt.Errorf("schedule cron_expr is required")
	}
	if err := validateCronExpr(cronExpr); err != nil {
		return nil, err
	}
	timezone := strings.TrimSpace(req.Timezone)
	if timezone == "" {
		timezone = "UTC"
	}
	if err := validateScheduleTimezone(timezone); err != nil {
		return nil, err
	}
	if err := validateScenarioScheduleWindowSets(req.BlackoutWindows, req.MaintenanceWindows, req.AvailabilityWindows, timezone); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	enabled := true
	if existing != nil {
		enabled = existing.Enabled
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	schedule := &storage.ScenarioSchedule{
		ID:              scheduleID,
		ScenarioID:      scenarioID,
		ProjectID:       normalizeProjectID(scenario.ProjectID),
		Revision:        1,
		Name:            name,
		CronExpr:        cronExpr,
		Timezone:        timezone,
		ReleaseID:       strings.TrimSpace(req.ReleaseID),
		TimeslotID:      strings.TrimSpace(req.TimeslotID),
		RunNameTemplate: strings.TrimSpace(req.RunNameTemplate),
		Enabled:         enabled,
		ApprovalPolicy:  normalizeScenarioScheduleApprovalPolicy(req.ApprovalPolicy),
		MissedRunPolicy: normalizeScenarioScheduleMissedRunPolicy(req.MissedRunPolicy),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if existing != nil {
		schedule.Revision = existing.Revision + 1
		schedule.CreatedAt = existing.CreatedAt
		schedule.OwnerSubject = existing.OwnerSubject
		schedule.OwnerDisplayName = existing.OwnerDisplayName
		schedule.LastRunTestID = existing.LastRunTestID
		schedule.LastRunAt = existing.LastRunAt
		schedule.LastError = existing.LastError
		schedule.LastOccurrence = cloneScenarioScheduleOccurrence(existing.LastOccurrence)
	}
	schedule.BlackoutWindows = append([]storage.ScenarioScheduleWindow(nil), req.BlackoutWindows...)
	schedule.MaintenanceWindows = append([]storage.ScenarioScheduleWindow(nil), req.MaintenanceWindows...)
	schedule.AvailabilityWindows = append([]storage.ScenarioScheduleWindow(nil), req.AvailabilityWindows...)
	schedule.DisabledFrom = normalizeSchedulePauseBound(req.DisabledFrom)
	schedule.DisabledUntil = normalizeSchedulePauseBound(req.DisabledUntil)
	if schedule.DisabledFrom != nil && schedule.DisabledUntil != nil && !schedule.DisabledFrom.Before(*schedule.DisabledUntil) {
		return nil, fmt.Errorf("disabled_from must be before disabled_until")
	}
	if strings.TrimSpace(schedule.OwnerSubject) == "" {
		schedule.OwnerSubject = strings.TrimSpace(req.OwnerSubject)
	}
	if strings.TrimSpace(schedule.OwnerDisplayName) == "" {
		schedule.OwnerDisplayName = strings.TrimSpace(req.OwnerDisplayName)
	}
	schedule.Governance = tr.scenarioScheduleGovernanceLocked(scenario, schedule, req, now, projectQuota)
	if schedule.RunNameTemplate == "" {
		schedule.RunNameTemplate = scenario.Name
	}
	schedule.ConflictSummary = tr.scenarioScheduleConflictSummaryWithTimeslotsLocked(schedule, scenario, quotaAt, activeSlots, reservationErr, projectQuota, releaseEvidence, capacityEvidence)
	schedule.NextRequiredAction = scenarioScheduleNextRequiredAction(schedule)
	var approvalToSave *storage.ScenarioScheduleApproval
	if schedule.NextRequiredAction == "approval_required" {
		approvalToSave = tr.buildScenarioScheduleApproval(schedule, now, strings.TrimSpace(req.OwnerSubject))
		schedule.PendingApprovalID = approvalToSave.ID
	} else {
		schedule.PendingApprovalID = ""
	}
	// An open-ended pause (disabled_from set, disabled_until nil) makes every future
	// candidate ineligible, so computing a next run would exhaust the full search
	// horizon and return a misleading "impossible day/month" error. Short-circuit to
	// a paused state instead (mirrors the execution-plan schedule path).
	if executionPlanSchedulePausedIndefinitely(schedule.DisabledFrom, schedule.DisabledUntil, now) {
		schedule.NextRunAt = nil
	} else {
		nextRun, err := nextScenarioScheduleRun(schedule, now)
		if err != nil {
			return nil, err
		}
		schedule.NextRunAt = nextRun
		// Preserve the prior next-run only when it exists and no schedule-affecting
		// field changed. A nil existing NextRunAt means the schedule was previously
		// paused/unset, so it must NOT be preserved — otherwise clearing an
		// open-ended pause (which doesn't touch the fields compared below) would
		// leave the resumed schedule stuck at NextRunAt=nil and never fire.
		if existing != nil && existing.NextRunAt != nil && scenarioScheduleTimingEquivalent(existing, schedule) {
			schedule.NextRunAt = existing.NextRunAt
		}
	}
	if approvalToSave != nil {
		if atomicStore, ok := tr.scenarioStore.(atomicScenarioScheduleApprovalWriteStore); ok {
			expectedRevision := 0
			if existing != nil {
				expectedRevision = existing.Revision
			}
			if err := tr.saveScenarioScheduleWithApprovalLocked(atomicStore, schedule, approvalToSave, expectedRevision); err != nil {
				return nil, err
			}
			return cloneScenarioSchedule(schedule), nil
		}
	}
	if err := tr.saveScenarioScheduleLocked(schedule); err != nil {
		return nil, err
	}
	if approvalToSave != nil {
		if err := tr.saveScenarioScheduleApprovalLocked(approvalToSave); err != nil {
			// Compensate the schedule write so a transient approval-store failure
			// cannot leave a durable PendingApprovalID that points at nothing.
			if existing == nil && tr.scenarioStore != nil {
				if deleteErr := tr.compensateScenarioScheduleCreateLocked(schedule.ScenarioID, schedule.ID, schedule.Revision); deleteErr != nil {
					return nil, errors.Join(err, fmt.Errorf("compensating schedule delete failed: %w", deleteErr))
				}
			} else if existing != nil {
				if rollbackErr := tr.compensateScenarioScheduleUpdateLocked(schedule, existing); rollbackErr != nil {
					return nil, errors.Join(err, fmt.Errorf("compensating schedule rollback failed: %w", rollbackErr))
				}
			}
			return nil, err
		}
	}
	return cloneScenarioSchedule(schedule), nil
}

// compensateScenarioScheduleCreateLocked removes a just-created schedule after
// its non-atomic approval write fails. The durable delete must not inherit the
// caller's global runner lock. Both the durable delete and the in-memory removal
// are fenced by the revision written before the approval attempt, so a newer
// mutation wins instead of being erased by delayed compensation.
func (tr *TestRunner) compensateScenarioScheduleCreateLocked(scenarioID, scheduleID string, expectedRevision int) error {
	store := tr.scenarioStore
	if store == nil {
		return nil
	}
	fence, err := tr.captureScenarioScheduleMemoryFenceLocked(scenarioID, scheduleID)
	if err != nil {
		return err
	}
	if !fence.existed || fence.expectedRevision != expectedRevision {
		return fmt.Errorf("%w: scenario schedule %q advanced before create compensation", errScheduleConcurrentChange, scheduleID)
	}
	guardedStore, ok := store.(revisionGuardedScenarioScheduleDeletePersistence)
	if !ok {
		return fmt.Errorf("scenario schedule persistence does not support revision-fenced compensation")
	}
	tr.mu.Unlock()
	result, err := guardedStore.DeleteScenarioScheduleGuardedIfRevision(scheduleID, expectedRevision)
	tr.mu.Lock()
	if err != nil {
		return err
	}
	if result == storage.ScheduleDeleteBlockedByActiveOccurrence {
		return errScheduleHasActiveOccurrence
	}
	if result != storage.ScheduleDeleteDeleted {
		return fmt.Errorf("%w: scenario schedule %q advanced or was deleted before create compensation", errScheduleConcurrentChange, scheduleID)
	}
	scenario := tr.scenarios[scenarioID]
	if scenario == nil {
		return nil
	}
	for index, candidate := range scenario.Schedules {
		if candidate == nil || candidate.ID != scheduleID {
			continue
		}
		if candidate.Revision != expectedRevision {
			return fmt.Errorf("%w: scenario schedule %q advanced while create compensation was in flight", errScheduleConcurrentChange, scheduleID)
		}
		scenario.Schedules = append(scenario.Schedules[:index], scenario.Schedules[index+1:]...)
		tr.contentRevision++
		tr.adjustSchedulerReadinessLocked(candidate, nil)
		break
	}
	return nil
}

// compensateScenarioScheduleUpdateLocked restores the pre-update fields only
// if the failed schedule revision is still current. A newer update wins its CAS
// and is never overwritten by cleanup from an older approval attempt.
func (tr *TestRunner) compensateScenarioScheduleUpdateLocked(written, previous *storage.ScenarioSchedule) error {
	if written == nil || previous == nil {
		return fmt.Errorf("schedule rollback state is required")
	}
	store, ok := tr.scenarioStore.(scenarioScheduleCASPersistence)
	if !ok {
		return fmt.Errorf("scenario schedule persistence does not support revision-fenced compensation")
	}
	fence, err := tr.captureScenarioScheduleMemoryFenceLocked(written.ScenarioID, written.ID)
	if err != nil {
		return err
	}
	if !fence.existed || fence.expectedRevision != written.Revision {
		return fmt.Errorf("%w: scenario schedule %q advanced before update compensation", errScheduleConcurrentChange, written.ID)
	}
	rollback := cloneScenarioSchedule(previous)
	rollback.UpdatedAt = time.Now().UTC()
	tr.mu.Unlock()
	changed, err := store.UpdateScenarioScheduleIfRevision(rollback, written.Revision)
	tr.mu.Lock()
	if err != nil {
		return err
	}
	if !changed {
		return fmt.Errorf("%w: scenario schedule %q advanced or was deleted before update compensation", errScheduleConcurrentChange, written.ID)
	}
	return tr.installScenarioScheduleAfterPersistenceLocked(rollback, fence)
}

// scenarioScheduleTimingEquivalent reports whether every field consulted by
// nextScenarioScheduleRun/scheduleAllowsCandidate is unchanged. Only then is it
// safe to preserve the prior next_run_at during a PUT. Cosmetic, release, and
// governance changes do not move the cron anchor; eligibility windows and pause
// bounds do.
func scenarioScheduleTimingEquivalent(left, right *storage.ScenarioSchedule) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Enabled == right.Enabled &&
		left.CronExpr == right.CronExpr &&
		left.Timezone == right.Timezone &&
		sameOptionalTime(left.DisabledFrom, right.DisabledFrom) &&
		sameOptionalTime(left.DisabledUntil, right.DisabledUntil) &&
		scenarioScheduleWindowsEqual(left.BlackoutWindows, right.BlackoutWindows, left.Timezone) &&
		scenarioScheduleWindowsEqual(left.MaintenanceWindows, right.MaintenanceWindows, left.Timezone) &&
		scenarioScheduleWindowsEqual(left.AvailabilityWindows, right.AvailabilityWindows, left.Timezone)
}

func scenarioScheduleWindowsEqual(left, right []storage.ScenarioScheduleWindow, fallbackTimezone string) bool {
	canonical := func(windows []storage.ScenarioScheduleWindow) []string {
		keys := make(map[string]struct{}, len(windows))
		for _, window := range windows {
			days := append([]int(nil), window.DaysOfWeek...)
			sort.Ints(days)
			uniqueDays := days[:0]
			for _, day := range days {
				if len(uniqueDays) == 0 || uniqueDays[len(uniqueDays)-1] != day {
					uniqueDays = append(uniqueDays, day)
				}
			}
			if len(uniqueDays) == 7 && uniqueDays[0] == 0 && uniqueDays[6] == 6 {
				uniqueDays = nil
			}
			dayParts := make([]string, len(uniqueDays))
			for index, day := range uniqueDays {
				dayParts[index] = strconv.Itoa(day)
			}
			// Name is presentation-only. Window order, duplicate windows, and
			// day order do not affect scheduleAllowsCandidate either.
			key := strings.Join([]string{
				strings.TrimSpace(window.StartTime),
				strings.TrimSpace(window.EndTime),
				firstNonEmpty(strings.TrimSpace(window.Timezone), strings.TrimSpace(fallbackTimezone), "UTC"),
				strings.Join(dayParts, ","),
			}, "\x00")
			keys[key] = struct{}{}
		}
		out := make([]string, 0, len(keys))
		for key := range keys {
			out = append(out, key)
		}
		sort.Strings(out)
		return out
	}
	return slices.Equal(canonical(left), canonical(right))
}

func (tr *TestRunner) buildScenarioScheduleApproval(schedule *storage.ScenarioSchedule, now time.Time, requestedBy string) *storage.ScenarioScheduleApproval {
	return &storage.ScenarioScheduleApproval{
		ID:               uuid.New().String(),
		ScheduleID:       schedule.ID,
		ScenarioID:       schedule.ScenarioID,
		ProjectID:        normalizeProjectID(schedule.ProjectID),
		ScheduleRevision: schedule.Revision,
		Policy:           schedule.ApprovalPolicy,
		Status:           "pending",
		RequestedBy:      requestedBy,
		RequestedAt:      now,
		RequiredRole:     RoleProjectAdmin,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

func (tr *TestRunner) saveScenarioScheduleApprovalLocked(approval *storage.ScenarioScheduleApproval) error {
	if approval == nil {
		return fmt.Errorf("schedule approval is required")
	}
	store := tr.scenarioStore
	if store == nil {
		return nil
	}
	tr.mu.Unlock()
	err := store.SaveScenarioScheduleApproval(approval)
	tr.mu.Lock()
	return err
}

// saveScenarioScheduleWithApprovalLocked preserves the store's atomic
// schedule+approval contract without holding the global runner mutex across the
// transaction. The caller owns tr.mu on entry and again on return.
func (tr *TestRunner) saveScenarioScheduleWithApprovalLocked(store atomicScenarioScheduleApprovalWriteStore, schedule *storage.ScenarioSchedule, approval *storage.ScenarioScheduleApproval, expectedRevision int) error {
	if store == nil || schedule == nil || approval == nil {
		return fmt.Errorf("schedule approval persistence is not configured")
	}
	fence, err := tr.captureScenarioScheduleMemoryFenceLocked(schedule.ScenarioID, schedule.ID)
	if err != nil {
		return err
	}
	if fence.existed != (expectedRevision > 0) || (fence.existed && fence.expectedRevision != expectedRevision) {
		return fmt.Errorf("%w: scenario schedule %q changed before approval persistence", errScheduleConcurrentChange, schedule.ID)
	}
	tr.mu.Unlock()
	changed, err := store.SaveScenarioScheduleWithApproval(schedule, approval, expectedRevision)
	tr.mu.Lock()
	if err != nil {
		return err
	}
	if !changed {
		return fmt.Errorf("%w: scenario schedule %q changed or was deleted; reload and retry", errScheduleConcurrentChange, schedule.ID)
	}
	return tr.installScenarioScheduleAfterPersistenceLocked(schedule, fence)
}

func (tr *TestRunner) ResolveScenarioScheduleApproval(scenarioID string, scheduleID string, approvalID string, approved bool, resolvedBy string, reason string) (*storage.ScenarioScheduleApproval, *storage.ScenarioSchedule, bool, error) {
	tr.refreshScenariosForHARead(true)
	tr.mu.RLock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		tr.mu.RUnlock()
		return nil, nil, false, nil
	}
	var schedule *storage.ScenarioSchedule
	for _, candidate := range scenario.Schedules {
		if candidate != nil && candidate.ID == scheduleID {
			schedule = cloneScenarioSchedule(candidate)
			break
		}
	}
	if schedule == nil {
		tr.mu.RUnlock()
		return nil, nil, false, nil
	}
	store := tr.scenarioStore
	fence := scenarioScheduleMemoryFence{
		scenarioID:       scenarioID,
		scheduleID:       scheduleID,
		existed:          true,
		expectedRevision: schedule.Revision,
	}
	tr.mu.RUnlock()
	if store == nil {
		return nil, nil, false, fmt.Errorf("schedule approval store is not configured")
	}
	atomicStore, ok := store.(atomicScenarioScheduleApprovalStore)
	if !ok {
		// Resolving the approval and clearing the schedule pointer are one logical
		// decision. A split fallback can mark an obsolete approval approved after a
		// concurrent schedule revision supersedes it, or leave a resolved approval
		// attached to a still-pending schedule after a crash. Fail before either
		// write when the store cannot provide the atomic CAS contract.
		return nil, nil, false, fmt.Errorf("schedule approval persistence does not support atomic resolution")
	}
	approval, err := store.GetScenarioScheduleApproval(approvalID)
	if err != nil {
		return nil, nil, false, err
	}
	if approval == nil || approval.ScheduleID != scheduleID {
		return nil, nil, false, nil
	}
	now := time.Now().UTC()
	approval.ResolvedBy = strings.TrimSpace(resolvedBy)
	approval.ResolvedAt = &now
	approval.DecisionReason = strings.TrimSpace(reason)
	approval.UpdatedAt = now
	if approved {
		approval.Status = "approved"
		schedule.PendingApprovalID = ""
		schedule.NextRequiredAction = ""
		schedule.LastError = ""
	} else {
		approval.Status = "rejected"
		schedule.PendingApprovalID = ""
		schedule.NextRequiredAction = "approval_rejected"
		schedule.LastError = "schedule approval rejected"
	}
	schedule.UpdatedAt = now
	changed, err := atomicStore.ResolveScenarioScheduleApproval(approval, schedule, schedule.Revision)
	if err != nil {
		return nil, nil, false, err
	}
	if !changed {
		return nil, nil, false, fmt.Errorf("schedule approval was already resolved or the schedule changed concurrently")
	}
	tr.mu.Lock()
	if err := tr.installScenarioScheduleAfterPersistenceLocked(schedule, fence); err != nil {
		tr.mu.Unlock()
		return nil, nil, false, err
	}
	tr.mu.Unlock()
	return approval, cloneScenarioSchedule(schedule), true, nil
}

func normalizeScenarioScheduleApprovalPolicy(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return "auto"
	case "manual", "manual_review", "high_cost", "off_hours", "production", "quota_burst":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "manual"
	}
}

func normalizeScenarioScheduleMissedRunPolicy(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "skip":
		return "skip"
	case "catch_up_once", "catch_up_all", "require_operator_decision":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "skip"
	}
}

func (tr *TestRunner) scenarioScheduleConflictSummaryWithTimeslotsLocked(schedule *storage.ScenarioSchedule, scenario *StoredScenario, quotaAt time.Time, activeSlots []*storage.CapacityReservation, reservationErr error, projectQuota projectQuotaEvidence, releaseEvidence scenarioScheduleReleaseEvidence, capacityEvidence scenarioScheduleCapacityEvidence) string {
	if schedule == nil || schedule.Governance == nil {
		return ""
	}
	findings := make([]string, 0)
	if schedule.Governance.QuotaReview.Decision == "exceeds_project_vuser_limit" {
		findings = append(findings, strings.Join(schedule.Governance.QuotaReview.Findings, " "))
	}
	findings = append(findings, scenarioScheduleReleaseConflictFindings(schedule, releaseEvidence)...)
	findings = append(findings, tr.scenarioScheduleCapacityConflictFindingsWithTimeslotsLocked(schedule, scenario, quotaAt, activeSlots, reservationErr, projectQuota, capacityEvidence)...)
	return strings.Join(compactScheduleConflictFindings(findings), " ")
}

// schedulerSnapshotLocked captures the controller state needed by schedule
// compilation and launch-window evaluation. Storage-backed work runs against
// this private snapshot after the live runner lock is released, so a slow
// database cannot block leadership-loss cleanup or unrelated API reads.
func (tr *TestRunner) schedulerSnapshotLocked() (*TestRunner, uint64) {
	snapshot := &TestRunner{
		tests:            make(map[string]*ManagedTest, len(tr.tests)),
		scripts:          make(map[string]*StoredScript, len(tr.scripts)),
		scenarios:        make(map[string]*StoredScenario, len(tr.scenarios)),
		archives:         make(map[string]*storage.RunArchiveRecord),
		deletingProjects: make(map[string]struct{}, len(tr.deletingProjects)),
		// Snapshot evaluation must never emit user-visible events or
		// notifications; the live state publishes only after its durable
		// transition succeeds and tr.mu is reacquired.
		hub:                     nil,
		maxTests:                tr.maxTests,
		settings:                cloneSystemSettings(tr.settings),
		scriptStore:             tr.scriptStore,
		testStore:               tr.testStore,
		scenarioStore:           tr.scenarioStore,
		dataPoolStore:           tr.dataPoolStore,
		projectStore:            tr.projectStore,
		releaseStore:            tr.releaseStore,
		historyStore:            tr.historyStore,
		archiveStore:            tr.archiveStore,
		testNotesStore:          tr.testNotesStore,
		auditStore:              tr.auditStore,
		settingsStore:           tr.settingsStore,
		usageStore:              tr.usageStore,
		objectRepoStore:         tr.objectRepoStore,
		reservationStore:        tr.reservationStore,
		execPlanRunStore:        tr.execPlanRunStore,
		execPlanScheduleStore:   tr.execPlanScheduleStore,
		fleetStore:              tr.fleetStore,
		leadership:              tr.leadership,
		durableExecutionFence:   tr.durableExecutionFence,
		liveState:               tr.liveState,
		execution:               tr.execution,
		notifier:                nil,
		routeCache:              make(map[string]latencyRouteCacheEntry),
		envCode:                 tr.envCode,
		refScriptSeq:            tr.refScriptSeq,
		refScenarioSeq:          tr.refScenarioSeq,
		scriptRefSeqAllocator:   tr.scriptRefSeqAllocator,
		scenarioRefSeqAllocator: tr.scenarioRefSeqAllocator,
		runRefSeqAllocator:      tr.runRefSeqAllocator,
		refKeyIndex:             make(map[string]string, len(tr.refKeyIndex)),
		capacityAdvisoryCache:   make(map[string]cachedCapacityAdvisory),
		capacityAdvisoryGen:     tr.capacityAdvisoryGen,
		remoteControlGates:      make(map[string]*distributedControlGate),
		seqPlanRuns:             make(map[string]*seqPlanRunState),
		seqPlanByTest:           make(map[string]string),
		seqPlanRetrying:         make(map[string]struct{}),
		terminalRetrying:        make(map[string]struct{}),
		archiveCommitGates:      make(map[string]*archiveCommitGate),
	}
	for id, test := range tr.tests {
		snapshot.tests[id] = cloneManagedTestForReadLocked(test)
	}
	for id, script := range tr.scripts {
		snapshot.scripts[id] = cloneStoredScriptForReadLocked(script)
	}
	for id, scenario := range tr.scenarios {
		snapshot.scenarios[id] = cloneStoredScenarioForReadLocked(scenario)
	}
	for id := range tr.deletingProjects {
		snapshot.deletingProjects[id] = struct{}{}
	}
	for key, id := range tr.refKeyIndex {
		snapshot.refKeyIndex[key] = id
	}
	tr.liveInventoryMu.RLock()
	snapshot.liveInventory = cloneGeneratorInventoryEntries(tr.liveInventory)
	snapshot.liveInventoryAt = tr.liveInventoryAt
	tr.liveInventoryMu.RUnlock()
	return snapshot, tr.contentRevision
}

func (tr *TestRunner) mergeSchedulerSnapshotSequenceLocked(snapshot *TestRunner) {
	// Run sequence allocation is shared by live/request snapshot runners through
	// runRefSeqAllocator, so there is no per-snapshot counter to merge.
}

func (tr *TestRunner) buildManagedTestForScheduleSweepLocked(req CreateTestRequest, id, status string) (*ManagedTest, error) {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return nil, err
	}
	return tr.buildManagedTestForScheduleSweepLockedWithTerm(req, id, status, leaseTerm)
}

func (tr *TestRunner) buildManagedTestForScheduleSweepLockedWithTerm(req CreateTestRequest, id, status string, leaseTerm executionLeaseTerm) (*ManagedTest, error) {
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return nil, errNotExecutionLeader
	}
	snapshot, req, revision := tr.managedTestBuildSnapshotLocked(req)
	tr.mu.Unlock()
	if !req.projectQuotaEvidence.loaded {
		req.projectQuotaEvidence = snapshot.loadCreateTestProjectQuotaEvidence(req)
	}
	if !req.releaseContextEvidence.loaded {
		req.releaseContextEvidence = snapshot.loadCreateTestReleaseEvidence(req)
	}
	test, err := snapshot.buildManagedTestLocked(req, id, status)
	tr.mu.Lock()
	tr.mergeSchedulerSnapshotSequenceLocked(snapshot)
	if err != nil {
		return nil, err
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return nil, errNotExecutionLeader
	}
	if !tr.managedTestBuildFenceCurrentLocked(revision) {
		return nil, fmt.Errorf("controller content changed while scheduled run was being compiled; retry next sweep")
	}
	return test, nil
}

func (tr *TestRunner) launchStartWindowErrorForScheduleSweepLocked(test *ManagedTest, start time.Time, excludeTestID, excludeScheduleID string) error {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return err
	}
	return tr.launchStartWindowErrorForScheduleSweepLockedWithTerm(test, start, excludeTestID, excludeScheduleID, leaseTerm)
}

func (tr *TestRunner) launchStartWindowErrorForScheduleSweepLockedWithTerm(test *ManagedTest, start time.Time, excludeTestID, excludeScheduleID string, leaseTerm executionLeaseTerm) error {
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	snapshot, revision := tr.schedulerSnapshotLocked()
	testSnapshot := cloneManagedTestForReadLocked(test)
	tr.mu.Unlock()
	err := snapshot.launchStartWindowErrorLocked(testSnapshot, start, excludeTestID, excludeScheduleID)
	tr.mu.Lock()
	if err == nil && !tr.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	if err == nil && tr.contentRevision != revision {
		return fmt.Errorf("controller content changed while scheduled launch window was being evaluated; retry next sweep")
	}
	return err
}

// prepareTestStartForScheduleSweepLocked performs the durable start transition
// through the live runner. Evidence is loaded outside tr.mu through the same
// revision-seqlocked path as an ordinary start; using the older private-snapshot
// helper here left ExpectedStartAdmissionRevision at zero and therefore let a
// cross-replica admission change bypass the transactional start CAS. A per-test
// lifecycle gate prevents a concurrent start/stop/control request from racing
// the scheduler while tr.mu is released. The caller owns tr.mu on entry and
// again on return.
func (tr *TestRunner) prepareTestStartForScheduleSweepLocked(id string, opts StartTestOptions) (*ManagedTest, error) {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return nil, err
	}
	prepared, releaseLifecycle, err := tr.prepareTestStartForScheduleSweepLockedWithTerm(id, opts, leaseTerm)
	if releaseLifecycle != nil {
		releaseLifecycle()
	}
	return prepared, err
}

func (tr *TestRunner) prepareTestStartForScheduleSweepLockedWithTerm(id string, opts StartTestOptions, leaseTerm executionLeaseTerm) (*ManagedTest, func(), error) {
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return nil, nil, errNotExecutionLeader
	}
	candidate := tr.tests[id]
	if candidate == nil {
		return nil, nil, fmt.Errorf("test not found: %s", id)
	}
	tr.mu.Unlock()
	unlockLifecycle, err := tr.lockTestLifecycleControl(context.Background(), id)
	if err != nil {
		tr.mu.Lock()
		return nil, nil, err
	}
	prepared, prepareErr := tr.prepareTestForExecutionWithOptionsAndExpectedTerm(id, opts, &leaseTerm)
	// Preserve lifecycle -> runner lock order through validation. Releasing the
	// lifecycle gate first lets Stop/Abort acquire it and mutate the same object
	// before the occurrence and parent schedule record that preparation succeeded.
	// The returned idempotent closure deliberately keeps the gate owned through
	// those correlation commits. Callers must invoke it while they still hold
	// tr.mu, so a waiting Stop becomes the next writer before backend dispatch.
	tr.mu.Lock()
	var releaseOnce sync.Once
	releaseLifecycle := func() {
		releaseOnce.Do(func() {
			if tr.beforeScenarioScheduleStartLifecycleRelease != nil {
				tr.beforeScenarioScheduleStartLifecycleRelease()
			}
			unlockLifecycle()
		})
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		if prepared != nil && prepared.cancel != nil {
			prepared.cancel()
		}
		return nil, releaseLifecycle, errNotExecutionLeader
	}
	current := tr.tests[id]
	if current != candidate {
		if prepared != nil && prepared.cancel != nil {
			prepared.cancel()
		}
		return nil, releaseLifecycle, fmt.Errorf("scheduled test %s identity changed while start persistence was in flight", id)
	}
	if current.passiveOperationToken != "" {
		if prepared != nil && prepared.cancel != nil {
			prepared.cancel()
		}
		return nil, releaseLifecycle, fmt.Errorf("scheduled test %s retained an unfinished start operation token", id)
	}
	if tr.durableExecutionFence && current.executionFence != "" &&
		(current.executionFence != leaseTerm.ownerID || current.executionGeneration != leaseTerm.generation) {
		if prepared != nil && prepared.cancel != nil {
			prepared.cancel()
		}
		return nil, releaseLifecycle, errNotExecutionLeader
	}
	if prepareErr != nil {
		return nil, releaseLifecycle, prepareErr
	}
	if prepared == nil {
		if current == nil || current.Status != "PENDING" || current.executionCtx != nil || current.cancel != nil {
			return nil, releaseLifecycle, fmt.Errorf("scheduled test %s changed while pending start persistence was in flight", id)
		}
		return nil, releaseLifecycle, nil
	}
	validPrepared := current == prepared && prepared.Status == "INITIALIZING" &&
		prepared.executionCtx != nil && prepared.executionCtx.Err() == nil &&
		prepared.passiveOperationToken == ""
	if tr.durableExecutionFence {
		validPrepared = validPrepared && prepared.executionFence == leaseTerm.ownerID &&
			prepared.executionGeneration == leaseTerm.generation
	}
	if !validPrepared {
		if prepared.cancel != nil {
			prepared.cancel()
		}
		return nil, releaseLifecycle, fmt.Errorf("scheduled test %s changed while start persistence was in flight", id)
	}
	return prepared, releaseLifecycle, nil
}

func (tr *TestRunner) scenarioScheduleConflictSummaryForSweepLocked(schedule *storage.ScenarioSchedule, scenario *StoredScenario, leaseTerm executionLeaseTerm) (string, error) {
	if schedule == nil || schedule.Governance == nil {
		return "", nil
	}
	findings := make([]string, 0)
	if schedule.Governance.QuotaReview.Decision == "exceeds_project_vuser_limit" {
		findings = append(findings, strings.Join(schedule.Governance.QuotaReview.Findings, " "))
	}
	if tr.releaseStore != nil && strings.TrimSpace(schedule.ReleaseID) != "" {
		releaseID := strings.TrimSpace(schedule.ReleaseID)
		store := tr.releaseStore
		tr.mu.Unlock()
		release, err := store.GetRelease(releaseID)
		tr.mu.Lock()
		if !tr.executionLeaseTermCurrent(leaseTerm) {
			return "", errNotExecutionLeader
		}
		if err != nil || release == nil {
			findings = append(findings, fmt.Sprintf("Release conflict: release %s could not be resolved.", releaseID))
		} else if release.ArchivedAt != nil {
			findings = append(findings, fmt.Sprintf("Release conflict: release %s is archived.", firstNonEmpty(release.Name, release.ID)))
		}
	}
	preview, err := tr.buildManagedTestForScheduleSweepLockedWithTerm(CreateTestRequest{
		Name:       schedule.Name,
		ScenarioID: scenario.ID,
		ReleaseID:  schedule.ReleaseID,
	}, "schedule-conflict-preview", "DRAFT", leaseTerm)
	if err != nil {
		return "", err
	}
	if preview != nil {
		quotaAt, activeSlots, reservationErr, projectQuota, evidenceErr := tr.loadScheduleQuotaEvidenceForSweepLocked(leaseTerm, preview)
		if evidenceErr != nil {
			return "", evidenceErr
		}
		quota, quotaOK := projectQuota.quotaFor(preview)
		if !quotaOK {
			return "", fmt.Errorf("project quota evidence changed while the scheduled run was evaluated; retry next sweep")
		}
		if blocked, reason := tr.evaluateQuotaBlockWithTimeslotsAndProjectQuotaLocked(preview, "", quotaAt, activeSlots, reservationErr, quota); blocked {
			findings = append(findings, "Quota conflict: "+reason+".")
		}
		if preview.CapacityAdvisory != nil && preview.CapacityAdvisory.RequiresOverride {
			findings = append(findings, fmt.Sprintf("Capacity pool conflict: recommended %d generator%s but only %d available.", preview.CapacityAdvisory.RecommendedGenerators, pluralSuffix(preview.CapacityAdvisory.RecommendedGenerators), preview.CapacityAdvisory.AvailableGenerators))
		}
	}
	return strings.Join(compactScheduleConflictFindings(findings), " "), nil
}

// loadScheduleQuotaEvidenceForSweepLocked moves the reservation query out of
// tr.mu and verifies that neither scheduler content nor the execution lease
// changed while the durable snapshot was being read. The caller owns tr.mu on
// entry and regains it before return.
func (tr *TestRunner) loadScheduleQuotaEvidenceForSweepLocked(leaseTerm executionLeaseTerm, preview *ManagedTest) (time.Time, []*storage.CapacityReservation, error, projectQuotaEvidence, error) {
	revision := tr.contentRevision
	quotaAt := time.Now().UTC()
	projectStore := tr.projectStore
	tr.mu.Unlock()
	activeSlots, reservationErr := tr.loadBoundTimeslotsActiveAt(quotaAt)
	projectQuota := projectQuotaEvidence{}
	if preview != nil {
		projectQuota = loadProjectQuotaEvidence(projectStore, preview.ProjectID, testAssignedPoolID(preview))
	}
	tr.mu.Lock()
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return quotaAt, nil, reservationErr, projectQuotaEvidence{}, errNotExecutionLeader
	}
	if tr.contentRevision != revision {
		return quotaAt, nil, reservationErr, projectQuotaEvidence{}, fmt.Errorf("controller content changed while scheduled quota evidence was being loaded; retry next sweep")
	}
	return quotaAt, activeSlots, reservationErr, projectQuota, nil
}

// scenarioScheduleCapacityConflictFindingsLocked computes the standing
// quota/capacity findings for a candidate schedule's scenario footprint by
// building a non-registered preview test — independent of governance state, so it
// is reusable by both the governance-time summary and the pre-save preview.
func (tr *TestRunner) scenarioScheduleCapacityConflictFindingsWithTimeslotsLocked(schedule *storage.ScenarioSchedule, scenario *StoredScenario, quotaAt time.Time, activeSlots []*storage.CapacityReservation, reservationErr error, projectQuota projectQuotaEvidence, capacityEvidence scenarioScheduleCapacityEvidence) []string {
	if schedule == nil || scenario == nil {
		return nil
	}
	if !capacityEvidence.loaded || capacityEvidence.err != nil || capacityEvidence.preview == nil {
		return nil
	}
	findings := make([]string, 0, 2)
	if !tr.managedTestBuildFenceCurrentLocked(capacityEvidence.revision) {
		return append(findings, "Capacity conflict: controller content changed while schedule capacity evidence was compiled; retry.")
	}
	preview := capacityEvidence.preview
	quota, quotaOK := projectQuota.quotaFor(preview)
	if !quotaOK {
		findings = append(findings, "Quota conflict: project quota evidence changed while the schedule was evaluated; retry.")
	} else if blocked, reason := tr.evaluateQuotaBlockWithTimeslotsAndProjectQuotaLocked(preview, "", quotaAt, activeSlots, reservationErr, quota); blocked {
		findings = append(findings, "Quota conflict: "+reason+".")
	}
	if preview.CapacityAdvisory != nil && preview.CapacityAdvisory.RequiresOverride {
		findings = append(findings, fmt.Sprintf("Capacity pool conflict: recommended %d generator%s but only %d available.", preview.CapacityAdvisory.RecommendedGenerators, pluralSuffix(preview.CapacityAdvisory.RecommendedGenerators), preview.CapacityAdvisory.AvailableGenerators))
	}
	return findings
}

// previewScheduleConflictSummaryLocked is the pre-save equivalent of
// scenarioScheduleConflictSummaryLocked for a not-yet-persisted candidate (no
// governance record yet): it reports the release + quota/capacity conflicts the
// schedule would hit, so the form can warn before the schedule is saved.
func (tr *TestRunner) previewScheduleConflictSummaryWithTimeslotsLocked(schedule *storage.ScenarioSchedule, scenario *StoredScenario, quotaAt time.Time, activeSlots []*storage.CapacityReservation, reservationErr error, projectQuota projectQuotaEvidence, releaseEvidence scenarioScheduleReleaseEvidence, capacityEvidence scenarioScheduleCapacityEvidence) string {
	findings := scenarioScheduleReleaseConflictFindings(schedule, releaseEvidence)
	findings = append(findings, tr.scenarioScheduleCapacityConflictFindingsWithTimeslotsLocked(schedule, scenario, quotaAt, activeSlots, reservationErr, projectQuota, capacityEvidence)...)
	return strings.Join(compactScheduleConflictFindings(findings), " ")
}

func compactScheduleConflictFindings(findings []string) []string {
	out := make([]string, 0, len(findings))
	seen := map[string]struct{}{}
	for _, finding := range findings {
		finding = strings.TrimSpace(finding)
		if finding == "" {
			continue
		}
		if _, exists := seen[finding]; exists {
			continue
		}
		seen[finding] = struct{}{}
		out = append(out, finding)
	}
	return out
}

func scenarioScheduleNextRequiredAction(schedule *storage.ScenarioSchedule) string {
	if schedule == nil || !schedule.Enabled {
		return ""
	}
	if strings.TrimSpace(schedule.ConflictSummary) != "" {
		return "resolve_conflict"
	}
	if schedule.ApprovalPolicy != "" && schedule.ApprovalPolicy != "auto" {
		return "approval_required"
	}
	return ""
}

func (tr *TestRunner) scenarioScheduleGovernanceLocked(scenario *StoredScenario, schedule *storage.ScenarioSchedule, req CreateScenarioScheduleRequest, now time.Time, projectQuota projectQuotaEvidence) *storage.ScenarioScheduleGovernance {
	projectID := storage.DefaultProjectID
	if scenario != nil {
		projectID = normalizeProjectID(scenario.ProjectID)
	}
	reviewer := strings.TrimSpace(req.OwnerSubject)
	if reviewer == "" && schedule != nil {
		reviewer = strings.TrimSpace(schedule.OwnerSubject)
	}
	reviewSource := strings.TrimSpace(req.RBACReviewSource)
	if reviewSource == "" {
		reviewSource = "api"
	}
	estimatedPeak := 0
	if scenario != nil {
		for _, script := range scenario.Scripts {
			estimatedPeak += script.VUsers
		}
	}
	quota, quotaOK := projectQuota.quotaForProject(projectID)
	maxConcurrent := quota.maxConcurrentTests
	maxVUsers := quota.maxVUsers
	findings := []string{"Schedule mutation authorized for scenario project scope."}
	quotaDecision := "allowed"
	if !quotaOK {
		quotaDecision = "quota_evidence_unavailable"
		findings = append(findings, "Project quota evidence changed while the schedule was evaluated; retry the mutation.")
	}
	if maxConcurrent > 0 {
		findings = append(findings, fmt.Sprintf("Project allows %d concurrent test(s).", maxConcurrent))
	}
	if maxVUsers > 0 {
		findings = append(findings, fmt.Sprintf("Project allows %d active VUser(s).", maxVUsers))
		if estimatedPeak > maxVUsers {
			quotaDecision = "exceeds_project_vuser_limit"
			findings = append(findings, fmt.Sprintf("Scheduled scenario peak %d VUsers exceeds project limit.", estimatedPeak))
		}
	}
	if maxConcurrent == 0 && maxVUsers == 0 {
		findings = append(findings, "No project-specific quota limit configured; global capacity controls still apply at materialization.")
	}
	return &storage.ScenarioScheduleGovernance{
		PolicyVersion: "schedule-governance.v1",
		RBACReview: storage.ScenarioScheduleRBACReview{
			Decision:      "allowed",
			ReviewedAt:    now,
			ReviewedBy:    reviewer,
			RequiredRoles: []string{RoleEditor, RoleProjectAdmin},
			ReviewSource:  reviewSource,
		},
		QuotaReview: storage.ScenarioScheduleQuotaReview{
			Decision:             quotaDecision,
			ProjectMaxConcurrent: maxConcurrent,
			ProjectMaxVUsers:     maxVUsers,
			EstimatedPeakVUsers:  estimatedPeak,
			Findings:             findings,
		},
	}
}
