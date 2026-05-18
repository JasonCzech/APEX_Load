package api

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/stormrunner/pkg/storage"
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

type scenarioScheduler struct {
	runner   *TestRunner
	now      func() time.Time
	ownerID  string
	leaseTTL time.Duration
}

type CreateScenarioScheduleRequest struct {
	Name                string                           `json:"name"`
	CronExpr            string                           `json:"cron_expr"`
	Timezone            string                           `json:"timezone,omitempty"`
	ReleaseID           string                           `json:"release_id,omitempty"`
	RunNameTemplate     string                           `json:"run_name_template,omitempty"`
	Enabled             *bool                            `json:"enabled,omitempty"`
	ApprovalPolicy      string                           `json:"approval_policy,omitempty"`
	MissedRunPolicy     string                           `json:"missed_run_policy,omitempty"`
	BlackoutWindows     []storage.ScenarioScheduleWindow `json:"blackout_windows,omitempty"`
	AvailabilityWindows []storage.ScenarioScheduleWindow `json:"availability_windows,omitempty"`
	OwnerSubject        string                           `json:"-"`
	OwnerDisplayName    string                           `json:"-"`
	RBACReviewSource    string                           `json:"-"`
}

type ScenarioScheduleHealth struct {
	Total           int            `json:"total"`
	Enabled         int            `json:"enabled"`
	Due             int            `json:"due"`
	Blocked         int            `json:"blocked"`
	Failed          int            `json:"failed"`
	PendingApproval int            `json:"pending_approval"`
	StatusCounts    map[string]int `json:"status_counts"`
}

func newScenarioScheduler(runner *TestRunner) *scenarioScheduler {
	return &scenarioScheduler{runner: runner, now: time.Now, ownerID: uuid.New().String(), leaseTTL: 30 * time.Second}
}

func (s *scenarioScheduler) RunDueSchedules() error {
	if s == nil || s.runner == nil {
		return nil
	}
	now := s.now().UTC()
	acquired, err := s.runner.acquireScenarioSchedulerLease(now, s.ownerID, s.leaseTTL)
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}
	toStart := make([]*ManagedTest, 0)
	s.runner.mu.Lock()
	for _, schedule := range s.runner.listAllScenarioSchedulesLocked() {
		if schedule == nil || !schedule.Enabled || schedule.NextRunAt == nil || schedule.NextRunAt.After(now) {
			continue
		}
		if handled, err := s.runner.applyMissedRunPolicyLocked(schedule, now); err != nil {
			schedule.LastError = err.Error()
			schedule.UpdatedAt = now
			_ = s.runner.saveScenarioScheduleLocked(schedule)
			continue
		} else if handled {
			continue
		}
		test, err := s.runner.runScenarioScheduleLocked(schedule, now)
		if err != nil {
			schedule.LastError = err.Error()
			schedule.UpdatedAt = now
			_ = s.runner.saveScenarioScheduleLocked(schedule)
			continue
		}
		if test != nil {
			toStart = append(toStart, test)
		}
	}
	s.runner.mu.Unlock()
	for _, test := range toStart {
		go s.runner.runTest(test)
	}
	return nil
}

func (tr *TestRunner) applyMissedRunPolicyLocked(schedule *storage.ScenarioSchedule, now time.Time) (bool, error) {
	if schedule == nil || schedule.NextRunAt == nil || now.Sub(*schedule.NextRunAt) <= time.Minute {
		return false, nil
	}
	switch normalizeScenarioScheduleMissedRunPolicy(schedule.MissedRunPolicy) {
	case "skip":
		occurrence := tr.buildScenarioScheduleOccurrence(schedule, *schedule.NextRunAt, "skipped", "missed_run_skipped", "Missed scheduled occurrence skipped by policy.", nil, now)
		if err := tr.saveScenarioScheduleOccurrenceLocked(occurrence); err != nil {
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
		if err := tr.saveScenarioScheduleLocked(schedule); err != nil {
			return true, err
		}
		return true, nil
	case "require_operator_decision":
		occurrence := tr.buildScenarioScheduleOccurrence(schedule, *schedule.NextRunAt, "blocked", "missed_run_requires_decision", "Missed scheduled occurrence requires an operator decision.", nil, now)
		if err := tr.saveScenarioScheduleOccurrenceLocked(occurrence); err != nil {
			return true, err
		}
		schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrence)
		schedule.NextRequiredAction = "operator_decision_required"
		schedule.LastError = "missed scheduled occurrence requires operator decision"
		schedule.UpdatedAt = now
		if err := tr.saveScenarioScheduleLocked(schedule); err != nil {
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
	from = from.UTC()
	if strings.TrimSpace(schedule.CronExpr) == "" {
		return nil, fmt.Errorf("cron expression is required")
	}
	fields := strings.Fields(schedule.CronExpr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields")
	}
	limit := from.Add(366 * 24 * time.Hour)
	for candidate := from.Truncate(time.Minute).Add(time.Minute); !candidate.After(limit); candidate = candidate.Add(time.Minute) {
		if cronFieldMatches(fields[0], int(candidate.Minute()), 0, 59) &&
			cronFieldMatches(fields[1], int(candidate.Hour()), 0, 23) &&
			cronFieldMatches(fields[2], int(candidate.Day()), 1, 31) &&
			cronFieldMatches(fields[3], int(candidate.Month()), 1, 12) &&
			cronFieldMatches(fields[4], int(candidate.Weekday()), 0, 6) {
			if allowed, err := scheduleAllowsCandidate(schedule, candidate); err != nil {
				return nil, err
			} else if !allowed {
				continue
			}
			next := candidate.UTC()
			return &next, nil
		}
	}
	return nil, fmt.Errorf("could not find next run within one year")
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
	if slash := strings.Index(part, "/"); slash >= 0 {
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
		value = value*10 + int(ch-'0')
	}
	if value < min || value > max {
		return 0, false
	}
	return value, true
}

func scheduleAllowsCandidate(schedule *storage.ScenarioSchedule, candidate time.Time) (bool, error) {
	for _, window := range schedule.BlackoutWindows {
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

func scheduleWindowContains(window storage.ScenarioScheduleWindow, fallbackTimezone string, candidate time.Time) (bool, error) {
	timezone := firstNonEmpty(strings.TrimSpace(window.Timezone), strings.TrimSpace(fallbackTimezone))
	if timezone == "" {
		timezone = "UTC"
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return false, fmt.Errorf("invalid schedule window timezone %q: %w", timezone, err)
	}
	local := candidate.In(location)
	if len(window.DaysOfWeek) > 0 && !scheduleWindowDayMatches(window.DaysOfWeek, int(local.Weekday())) {
		return false, nil
	}
	startMinute, err := parseScheduleWindowMinute(window.StartTime)
	if err != nil {
		return false, err
	}
	endMinute, err := parseScheduleWindowMinute(window.EndTime)
	if err != nil {
		return false, err
	}
	minuteOfDay := local.Hour()*60 + local.Minute()
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
	return out
}

func (tr *TestRunner) runScenarioScheduleLocked(schedule *storage.ScenarioSchedule, now time.Time) (*ManagedTest, error) {
	if schedule == nil {
		return nil, fmt.Errorf("schedule is required")
	}
	scenario, ok := tr.scenarios[schedule.ScenarioID]
	if !ok {
		return nil, fmt.Errorf("scenario not found: %s", schedule.ScenarioID)
	}
	plannedAt := now
	if schedule.NextRunAt != nil {
		plannedAt = *schedule.NextRunAt
	}
	if handled, err := tr.reconcileScenarioScheduleOccurrenceLocked(schedule, plannedAt, now); err != nil {
		return nil, err
	} else if handled {
		return nil, nil
	}
	schedule.ConflictSummary = tr.scenarioScheduleConflictSummaryLocked(schedule, scenario)
	if strings.TrimSpace(schedule.ConflictSummary) != "" {
		occurrence := tr.buildScenarioScheduleOccurrence(schedule, plannedAt, "blocked", "schedule_conflict", schedule.ConflictSummary, nil, now)
		if err := tr.saveScenarioScheduleOccurrenceLocked(occurrence); err != nil {
			return nil, err
		}
		schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrence)
		schedule.NextRequiredAction = "resolve_conflict"
		schedule.LastError = schedule.ConflictSummary
		schedule.UpdatedAt = now
		if err := tr.saveScenarioScheduleLocked(schedule); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if schedule.NextRequiredAction == "approval_required" && schedule.PendingApprovalID != "" {
		occurrence := tr.buildScenarioScheduleOccurrence(schedule, plannedAt, "blocked", "approval_required", "Schedule requires approval before materialization.", nil, now)
		occurrence.ApprovalID = schedule.PendingApprovalID
		if err := tr.saveScenarioScheduleOccurrenceLocked(occurrence); err != nil {
			return nil, err
		}
		schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrence)
		schedule.LastError = "schedule requires approval"
		schedule.UpdatedAt = now
		if err := tr.saveScenarioScheduleLocked(schedule); err != nil {
			return nil, err
		}
		return nil, nil
	}
	occurrence := tr.buildScenarioScheduleOccurrence(schedule, plannedAt, "materializing", "due", "", nil, now)
	if err := tr.saveScenarioScheduleOccurrenceLocked(occurrence); err != nil {
		return nil, err
	}
	runName := strings.TrimSpace(schedule.RunNameTemplate)
	if runName == "" {
		runName = fmt.Sprintf("Scheduled Run %s", now.Format(time.RFC3339))
	}
	test, err := tr.buildManagedTestLocked(CreateTestRequest{
		Name:       runName,
		ScenarioID: schedule.ScenarioID,
		ReleaseID:  schedule.ReleaseID,
	}, uuid.New().String(), "QUEUED")
	if err != nil {
		occurrence.Status = "failed"
		occurrence.ReasonCode = "create_failed"
		occurrence.ConflictSummary = err.Error()
		occurrence.UpdatedAt = now
		_ = tr.saveScenarioScheduleOccurrenceLocked(occurrence)
		return nil, err
	}
	tr.tests[test.ID] = test
	tr.saveManagedTestRecord(test)
	tr.broadcastEvent("test_created", test)
	prepared, err := tr.prepareTestStartLocked(test.ID, StartTestOptions{})
	if err != nil {
		occurrence.Status = "failed"
		occurrence.ReasonCode = "start_failed"
		occurrence.TestID = test.ID
		occurrence.ConflictSummary = err.Error()
		occurrence.UpdatedAt = now
		_ = tr.saveScenarioScheduleOccurrenceLocked(occurrence)
		return nil, err
	}
	occurrence.Status = "materialized"
	occurrence.ReasonCode = "started"
	occurrence.TestID = test.ID
	occurrence.MaterializedAt = &now
	occurrence.UpdatedAt = now
	if err := tr.saveScenarioScheduleOccurrenceLocked(occurrence); err != nil {
		return nil, err
	}
	schedule.LastRunTestID = test.ID
	schedule.LastRunAt = &now
	schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrence)
	schedule.NextRequiredAction = ""
	schedule.LastError = ""
	next, err := nextScenarioScheduleRun(schedule, now)
	if err != nil {
		return nil, err
	}
	schedule.NextRunAt = next
	schedule.UpdatedAt = now
	if err := tr.saveScenarioScheduleLocked(schedule); err != nil {
		return nil, err
	}
	return prepared, nil
}

func (tr *TestRunner) reconcileScenarioScheduleOccurrenceLocked(schedule *storage.ScenarioSchedule, plannedAt time.Time, now time.Time) (bool, error) {
	if tr == nil || tr.scenarioStore == nil || schedule == nil {
		return false, nil
	}
	existing, err := tr.scenarioStore.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt.UTC())
	if err != nil || existing == nil {
		return false, err
	}
	status := strings.TrimSpace(existing.Status)
	if status == "" || status == "materializing" {
		return false, nil
	}
	if existing.ScheduleRevision != schedule.Revision && (status == "blocked" || status == "failed") {
		return false, nil
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
		next, err := nextScenarioScheduleRun(schedule, now)
		if err != nil {
			return true, err
		}
		schedule.NextRunAt = next
		schedule.NextRequiredAction = ""
		schedule.LastError = ""
	case "skipped":
		next, err := nextScenarioScheduleRun(schedule, now)
		if err != nil {
			return true, err
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
		case "schedule_conflict":
			schedule.NextRequiredAction = "resolve_conflict"
		case "missed_run_requires_decision":
			schedule.NextRequiredAction = "operator_decision_required"
		}
	case "failed":
		schedule.LastError = firstNonEmpty(existing.ConflictSummary, existing.ReasonCode)
		schedule.NextRequiredAction = "operator_decision_required"
	default:
		return false, nil
	}
	if err := tr.saveScenarioScheduleLocked(schedule); err != nil {
		return true, err
	}
	return true, nil
}

func (tr *TestRunner) buildScenarioScheduleOccurrence(schedule *storage.ScenarioSchedule, plannedAt time.Time, status string, reasonCode string, conflictSummary string, materializedAt *time.Time, now time.Time) *storage.ScenarioScheduleOccurrence {
	return &storage.ScenarioScheduleOccurrence{
		ID:               uuid.New().String(),
		ScheduleID:       schedule.ID,
		ScenarioID:       schedule.ScenarioID,
		ProjectID:        normalizeProjectID(schedule.ProjectID),
		ScheduleRevision: schedule.Revision,
		PlannedAt:        plannedAt.UTC(),
		MaterializedAt:   materializedAt,
		Status:           status,
		ReasonCode:       reasonCode,
		TestID:           schedule.LastRunTestID,
		ConflictSummary:  conflictSummary,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
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
	cloned.BlackoutWindows = append([]storage.ScenarioScheduleWindow(nil), schedule.BlackoutWindows...)
	cloned.AvailabilityWindows = append([]storage.ScenarioScheduleWindow(nil), schedule.AvailabilityWindows...)
	cloned.LastOccurrence = cloneScenarioScheduleOccurrence(schedule.LastOccurrence)
	return &cloned
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

func (tr *TestRunner) CreateScenarioSchedule(scenarioID string, req CreateScenarioScheduleRequest) (*storage.ScenarioSchedule, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.upsertScenarioScheduleLocked(scenarioID, uuid.New().String(), nil, req)
}

func (tr *TestRunner) UpdateScenarioSchedule(scenarioID string, scheduleID string, req CreateScenarioScheduleRequest) (*storage.ScenarioSchedule, bool, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		return nil, false, nil
	}
	for _, schedule := range scenario.Schedules {
		if schedule != nil && schedule.ID == scheduleID {
			updated, err := tr.upsertScenarioScheduleLocked(scenarioID, scheduleID, schedule, req)
			return updated, true, err
		}
	}
	return nil, false, nil
}

func (tr *TestRunner) ListScenarioSchedules(scenarioID string) ([]*storage.ScenarioSchedule, bool) {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		return nil, false
	}
	out := make([]*storage.ScenarioSchedule, 0, len(scenario.Schedules))
	for _, schedule := range scenario.Schedules {
		cloned := cloneScenarioSchedule(schedule)
		tr.attachLatestScenarioScheduleOccurrenceLocked(cloned)
		out = append(out, cloned)
	}
	return out, true
}

func (tr *TestRunner) attachLatestScenarioScheduleOccurrenceLocked(schedule *storage.ScenarioSchedule) {
	if schedule == nil || tr.scenarioStore == nil {
		return
	}
	occurrences, err := tr.scenarioStore.ListScenarioScheduleOccurrences(schedule.ID)
	if err != nil || len(occurrences) == 0 {
		return
	}
	schedule.LastOccurrence = cloneScenarioScheduleOccurrence(occurrences[0])
}

func (tr *TestRunner) DeleteScenarioSchedule(scenarioID string, scheduleID string) bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		return false
	}
	filtered := scenario.Schedules[:0]
	deleted := false
	for _, schedule := range scenario.Schedules {
		if schedule != nil && schedule.ID == scheduleID {
			deleted = true
			continue
		}
		filtered = append(filtered, schedule)
	}
	if !deleted {
		return false
	}
	scenario.Schedules = append([]*storage.ScenarioSchedule(nil), filtered...)
	if tr.scenarioStore != nil {
		_ = tr.scenarioStore.DeleteScenarioSchedule(scheduleID)
	}
	return true
}

func (tr *TestRunner) upsertScenarioScheduleLocked(scenarioID string, scheduleID string, existing *storage.ScenarioSchedule, req CreateScenarioScheduleRequest) (*storage.ScenarioSchedule, error) {
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
		return nil, fmt.Errorf("scenario not found: %s", scenarioID)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("schedule name is required")
	}
	cronExpr := strings.TrimSpace(req.CronExpr)
	if cronExpr == "" {
		return nil, fmt.Errorf("schedule cron_expr is required")
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
		Timezone:        strings.TrimSpace(req.Timezone),
		ReleaseID:       strings.TrimSpace(req.ReleaseID),
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
	schedule.AvailabilityWindows = append([]storage.ScenarioScheduleWindow(nil), req.AvailabilityWindows...)
	if strings.TrimSpace(schedule.OwnerSubject) == "" {
		schedule.OwnerSubject = strings.TrimSpace(req.OwnerSubject)
	}
	if strings.TrimSpace(schedule.OwnerDisplayName) == "" {
		schedule.OwnerDisplayName = strings.TrimSpace(req.OwnerDisplayName)
	}
	schedule.Governance = tr.scenarioScheduleGovernanceLocked(scenario, schedule, req, now)
	if schedule.Timezone == "" {
		schedule.Timezone = "UTC"
	}
	if schedule.RunNameTemplate == "" {
		schedule.RunNameTemplate = scenario.Name
	}
	schedule.ConflictSummary = tr.scenarioScheduleConflictSummaryLocked(schedule, scenario)
	schedule.NextRequiredAction = scenarioScheduleNextRequiredAction(schedule)
	var approvalToSave *storage.ScenarioScheduleApproval
	if schedule.NextRequiredAction == "approval_required" {
		approvalToSave = tr.buildScenarioScheduleApproval(schedule, now, strings.TrimSpace(req.OwnerSubject))
		schedule.PendingApprovalID = approvalToSave.ID
	} else {
		schedule.PendingApprovalID = ""
	}
	nextRun, err := nextScenarioScheduleRun(schedule, now)
	if err != nil {
		return nil, err
	}
	schedule.NextRunAt = nextRun
	if existing != nil && existing.Enabled == schedule.Enabled && existing.CronExpr == schedule.CronExpr && existing.Timezone == schedule.Timezone && existing.ReleaseID == schedule.ReleaseID && existing.RunNameTemplate == schedule.RunNameTemplate && existing.Name == schedule.Name {
		schedule.NextRunAt = existing.NextRunAt
	}
	if err := tr.saveScenarioScheduleLocked(schedule); err != nil {
		return nil, err
	}
	if approvalToSave != nil {
		if err := tr.saveScenarioScheduleApprovalLocked(approvalToSave); err != nil {
			return nil, err
		}
	}
	return cloneScenarioSchedule(schedule), nil
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
	if tr.scenarioStore != nil {
		return tr.scenarioStore.SaveScenarioScheduleApproval(approval)
	}
	return nil
}

func (tr *TestRunner) ResolveScenarioScheduleApproval(scenarioID string, scheduleID string, approvalID string, approved bool, resolvedBy string, reason string) (*storage.ScenarioScheduleApproval, *storage.ScenarioSchedule, bool, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	scenario, ok := tr.scenarios[scenarioID]
	if !ok {
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
		return nil, nil, false, nil
	}
	if tr.scenarioStore == nil {
		return nil, nil, false, fmt.Errorf("schedule approval store is not configured")
	}
	approval, err := tr.scenarioStore.GetScenarioScheduleApproval(approvalID)
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
		schedule.NextRequiredAction = "approval_rejected"
		schedule.LastError = "schedule approval rejected"
	}
	if err := tr.scenarioStore.SaveScenarioScheduleApproval(approval); err != nil {
		return nil, nil, false, err
	}
	schedule.UpdatedAt = now
	if err := tr.saveScenarioScheduleLocked(schedule); err != nil {
		return nil, nil, false, err
	}
	return approval, cloneScenarioSchedule(schedule), true, nil
}

func normalizeScenarioScheduleApprovalPolicy(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return "auto"
	case "manual", "high_cost", "off_hours", "production", "quota_burst":
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

func (tr *TestRunner) scenarioScheduleConflictSummaryLocked(schedule *storage.ScenarioSchedule, scenario *StoredScenario) string {
	if schedule == nil || schedule.Governance == nil {
		return ""
	}
	findings := make([]string, 0)
	if schedule.Governance.QuotaReview.Decision == "exceeds_project_vuser_limit" {
		findings = append(findings, strings.Join(schedule.Governance.QuotaReview.Findings, " "))
	}
	if scenario != nil {
		preview, err := tr.buildManagedTestLocked(CreateTestRequest{
			Name:       schedule.Name,
			ScenarioID: scenario.ID,
			ReleaseID:  schedule.ReleaseID,
		}, "schedule-conflict-preview", "DRAFT")
		if err == nil && preview != nil {
			if blocked, reason := tr.evaluateQuotaBlockLocked(preview, ""); blocked {
				findings = append(findings, "Quota conflict: "+reason+".")
			}
			if preview.CapacityAdvisory != nil && preview.CapacityAdvisory.RequiresOverride {
				findings = append(findings, fmt.Sprintf("Capacity pool conflict: recommended %d generator%s but only %d available.", preview.CapacityAdvisory.RecommendedGenerators, pluralSuffix(preview.CapacityAdvisory.RecommendedGenerators), preview.CapacityAdvisory.AvailableGenerators))
			}
		}
	}
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

func (tr *TestRunner) scenarioScheduleGovernanceLocked(scenario *StoredScenario, schedule *storage.ScenarioSchedule, req CreateScenarioScheduleRequest, now time.Time) *storage.ScenarioScheduleGovernance {
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
	maxConcurrent := tr.projectMaxConcurrentTests(projectID)
	maxVUsers := tr.projectMaxVUsers(projectID)
	findings := []string{"Schedule mutation authorized for scenario project scope."}
	quotaDecision := "allowed"
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
