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
}

type scenarioScheduler struct {
	runner *TestRunner
	now    func() time.Time
}

type CreateScenarioScheduleRequest struct {
	Name             string `json:"name"`
	CronExpr         string `json:"cron_expr"`
	Timezone         string `json:"timezone,omitempty"`
	ReleaseID        string `json:"release_id,omitempty"`
	RunNameTemplate  string `json:"run_name_template,omitempty"`
	Enabled          *bool  `json:"enabled,omitempty"`
	OwnerSubject     string `json:"-"`
	OwnerDisplayName string `json:"-"`
	RBACReviewSource string `json:"-"`
}

func newScenarioScheduler(runner *TestRunner) *scenarioScheduler {
	return &scenarioScheduler{runner: runner, now: time.Now}
}

func (s *scenarioScheduler) RunDueSchedules() error {
	if s == nil || s.runner == nil {
		return nil
	}
	now := s.now().UTC()
	toStart := make([]*ManagedTest, 0)
	s.runner.mu.Lock()
	for _, schedule := range s.runner.listAllScenarioSchedulesLocked() {
		if schedule == nil || !schedule.Enabled || schedule.NextRunAt == nil || schedule.NextRunAt.After(now) {
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
	if _, ok := tr.scenarios[schedule.ScenarioID]; !ok {
		return nil, fmt.Errorf("scenario not found: %s", schedule.ScenarioID)
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
		return nil, err
	}
	tr.tests[test.ID] = test
	tr.saveManagedTestRecord(test)
	tr.broadcastEvent("test_created", test)
	prepared, err := tr.prepareTestStartLocked(test.ID, StartTestOptions{})
	if err != nil {
		return nil, err
	}
	schedule.LastRunTestID = test.ID
	schedule.LastRunAt = &now
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
		out = append(out, cloneScenarioSchedule(schedule))
	}
	return out, true
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
		Name:            name,
		CronExpr:        cronExpr,
		Timezone:        strings.TrimSpace(req.Timezone),
		ReleaseID:       strings.TrimSpace(req.ReleaseID),
		RunNameTemplate: strings.TrimSpace(req.RunNameTemplate),
		Enabled:         enabled,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if existing != nil {
		schedule.CreatedAt = existing.CreatedAt
		schedule.OwnerSubject = existing.OwnerSubject
		schedule.OwnerDisplayName = existing.OwnerDisplayName
		schedule.LastRunTestID = existing.LastRunTestID
		schedule.LastRunAt = existing.LastRunAt
		schedule.LastError = existing.LastError
	}
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
	return cloneScenarioSchedule(schedule), nil
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
