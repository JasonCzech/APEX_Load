package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	runCalendarFutureHorizon       = 366 * 24 * time.Hour
	runCalendarDefaultWindow       = 30 * 24 * time.Hour
	runCalendarMaxEvents           = 2000
	runCalendarMaxProjectedPerCron = 1000
)

type RunCalendarResponse struct {
	GeneratedAt  time.Time          `json:"generated_at"`
	Range        RunCalendarRange   `json:"range"`
	Events       []RunCalendarEvent `json:"events"`
	Count        int                `json:"count"`
	StatusCounts map[string]int     `json:"status_counts,omitempty"`
	SourceCounts map[string]int     `json:"source_counts,omitempty"`
	Truncated    bool               `json:"truncated,omitempty"`
}

type RunCalendarRange struct {
	From              time.Time  `json:"from"`
	To                time.Time  `json:"to"`
	RequestedFrom     time.Time  `json:"requested_from"`
	RequestedTo       time.Time  `json:"requested_to"`
	HistoryFloor      *time.Time `json:"history_floor,omitempty"`
	FutureCeiling     time.Time  `json:"future_ceiling"`
	HistoryClamped    bool       `json:"history_clamped,omitempty"`
	FutureClamped     bool       `json:"future_clamped,omitempty"`
	RetentionEnforced bool       `json:"retention_enforced,omitempty"`
}

type RunCalendarEvent struct {
	ID               string              `json:"id"`
	Source           string              `json:"source"`
	RunType          string              `json:"run_type"`
	Title            string              `json:"title"`
	Status           string              `json:"status"`
	StartAt          time.Time           `json:"start_at"`
	EndAt            *time.Time          `json:"end_at,omitempty"`
	Projected        bool                `json:"projected,omitempty"`
	OccurrenceStatus string              `json:"occurrence_status,omitempty"`
	ReasonCode       string              `json:"reason_code,omitempty"`
	ConflictSummary  string              `json:"conflict_summary,omitempty"`
	TestID           string              `json:"test_id,omitempty"`
	TestRefKey       string              `json:"test_ref_key,omitempty"`
	ScheduleID       string              `json:"schedule_id,omitempty"`
	ScheduleName     string              `json:"schedule_name,omitempty"`
	CronExpr         string              `json:"cron_expr,omitempty"`
	Timezone         string              `json:"timezone,omitempty"`
	ScenarioID       string              `json:"scenario_id,omitempty"`
	ScenarioName     string              `json:"scenario_name,omitempty"`
	ApplicationID    string              `json:"application_id,omitempty"`
	ReleaseID        string              `json:"release_id,omitempty"`
	ProjectID        string              `json:"project_id,omitempty"`
	Workload         RunCalendarWorkload `json:"workload"`
	User             RunCalendarUser     `json:"user"`
	Topology         RunCalendarTopology `json:"topology"`
}

type RunCalendarWorkload struct {
	TotalVUsers int         `json:"total_vusers,omitempty"`
	Duration    string      `json:"duration,omitempty"`
	Groups      []TestGroup `json:"groups,omitempty"`
}

type RunCalendarUser struct {
	DisplayName          string `json:"display_name,omitempty"`
	Subject              string `json:"subject,omitempty"`
	Attribution          string `json:"attribution,omitempty"`
	OwnerSubject         string `json:"owner_subject,omitempty"`
	OwnerDisplayName     string `json:"owner_display_name,omitempty"`
	CreatedBySubject     string `json:"created_by_subject,omitempty"`
	CreatedByDisplayName string `json:"created_by_display_name,omitempty"`
	StartedBySubject     string `json:"started_by_subject,omitempty"`
	StartedByDisplayName string `json:"started_by_display_name,omitempty"`
}

type RunCalendarTopology struct {
	Source             string                    `json:"source,omitempty"`
	Controller         *ControllerInventoryEntry `json:"controller,omitempty"`
	ControllerEnv      map[string]string         `json:"controller_env,omitempty"`
	GeneratorShards    []GeneratorShardSummary   `json:"generator_shards,omitempty"`
	GeneratorInventory []GeneratorInventoryEntry `json:"generator_inventory,omitempty"`
	Summary            string                    `json:"summary,omitempty"`
}

type runCalendarScenarioSummary struct {
	Name        string
	Duration    string
	TotalVUsers int
	Groups      []TestGroup
}

type runCalendarSnapshot struct {
	Tests       []*ManagedTest
	Schedules   []*storage.ScenarioSchedule
	Occurrences []*storage.ScenarioScheduleOccurrence
	Scenarios   map[string]runCalendarScenarioSummary
}

func (s *Server) handleGetRunCalendar(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	requestedFrom, requestedTo, ok := parseRunCalendarRange(w, r, now)
	if !ok {
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	settings := s.runner.GetSystemSettings().ArchiveRetention
	from, to, calendarRange := clampRunCalendarRange(requestedFrom, requestedTo, now, settings)
	inventory := s.systemInventory(r.Context())
	response := s.runner.BuildRunCalendar(from, to, projectID, now, inventory, calendarRange)
	writeJSON(w, http.StatusOK, response)
}

func parseRunCalendarRange(w http.ResponseWriter, r *http.Request, now time.Time) (time.Time, time.Time, bool) {
	fromRaw := strings.TrimSpace(r.URL.Query().Get("from"))
	toRaw := strings.TrimSpace(r.URL.Query().Get("to"))
	from := now.Add(-runCalendarDefaultWindow)
	to := now.Add(runCalendarDefaultWindow)
	var err error
	if fromRaw != "" {
		from, err = time.Parse(time.RFC3339, fromRaw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "from must be RFC3339")
			return time.Time{}, time.Time{}, false
		}
	}
	if toRaw != "" {
		to, err = time.Parse(time.RFC3339, toRaw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "to must be RFC3339")
			return time.Time{}, time.Time{}, false
		}
	}
	from = from.UTC()
	to = to.UTC()
	if !from.Before(to) {
		writeError(w, http.StatusBadRequest, "from must be before to")
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

func clampRunCalendarRange(requestedFrom time.Time, requestedTo time.Time, now time.Time, settings ArchiveRetentionSettings) (time.Time, time.Time, RunCalendarRange) {
	from := requestedFrom.UTC()
	to := requestedTo.UTC()
	futureCeiling := now.Add(runCalendarFutureHorizon).UTC()
	calendarRange := RunCalendarRange{
		From:          from,
		To:            to,
		RequestedFrom: requestedFrom.UTC(),
		RequestedTo:   requestedTo.UTC(),
		FutureCeiling: futureCeiling,
	}
	if settings.Enabled {
		retentionDays := maxArchiveRetentionDays(settings)
		historyFloor := now.Add(-time.Duration(retentionDays) * 24 * time.Hour).UTC()
		calendarRange.HistoryFloor = &historyFloor
		calendarRange.RetentionEnforced = true
		if from.Before(historyFloor) {
			from = historyFloor
			calendarRange.HistoryClamped = true
		}
	}
	if to.After(futureCeiling) {
		to = futureCeiling
		calendarRange.FutureClamped = true
	}
	calendarRange.From = from
	calendarRange.To = to
	return from, to, calendarRange
}

func maxArchiveRetentionDays(settings ArchiveRetentionSettings) int {
	settings = normalizeArchiveRetentionSettings(settings)
	maxDays := settings.DefaultRetentionDays
	for _, policy := range settings.Policies {
		if policy.RetentionDays > maxDays {
			maxDays = policy.RetentionDays
		}
	}
	if maxDays < 1 {
		return defaultSystemSettings(0).ArchiveRetention.DefaultRetentionDays
	}
	return maxDays
}

func (tr *TestRunner) BuildRunCalendar(from time.Time, to time.Time, projectID string, now time.Time, inventory SystemInventoryResponse, calendarRange RunCalendarRange) RunCalendarResponse {
	snapshot := tr.runCalendarSnapshot(projectID)
	testsByID := map[string]*ManagedTest{}
	for _, test := range snapshot.Tests {
		if test != nil {
			testsByID[test.ID] = test
		}
	}
	schedulesByID := map[string]*storage.ScenarioSchedule{}
	for _, schedule := range snapshot.Schedules {
		if schedule != nil {
			schedulesByID[schedule.ID] = schedule
		}
	}
	occurrenceByScheduleAt := map[string]struct{}{}
	scheduledTestIDs := map[string]struct{}{}
	events := make([]RunCalendarEvent, 0)
	truncated := false

	appendEvent := func(event RunCalendarEvent) {
		if !runCalendarEventOverlaps(event.StartAt, event.EndAt, from, to) {
			return
		}
		if len(events) >= runCalendarMaxEvents {
			truncated = true
			return
		}
		events = append(events, event)
	}

	for _, occurrence := range snapshot.Occurrences {
		if occurrence == nil {
			continue
		}
		key := runCalendarOccurrenceKey(occurrence.ScheduleID, occurrence.PlannedAt)
		occurrenceByScheduleAt[key] = struct{}{}
		if occurrence.TestID != "" {
			scheduledTestIDs[occurrence.TestID] = struct{}{}
		}
		schedule := schedulesByID[occurrence.ScheduleID]
		test := testsByID[occurrence.TestID]
		appendEvent(runCalendarEventFromOccurrence(occurrence, schedule, test, snapshot.Scenarios, inventory, tr.manifestForCalendarTest(test), now))
	}

	for _, test := range snapshot.Tests {
		if test == nil {
			continue
		}
		if _, scheduled := scheduledTestIDs[test.ID]; scheduled {
			continue
		}
		appendEvent(runCalendarEventFromTest(test, snapshot.Scenarios, inventory, tr.manifestForCalendarTest(test), now))
	}

	for _, schedule := range snapshot.Schedules {
		if schedule == nil || !schedule.Enabled {
			continue
		}
		if schedule.NextRequiredAction == "approval_required" || strings.TrimSpace(schedule.ConflictSummary) != "" {
			continue
		}
		projected := projectRunCalendarSchedule(schedule, from, to, now, occurrenceByScheduleAt, snapshot.Scenarios)
		for _, event := range projected {
			appendEvent(event)
		}
		if len(projected) >= runCalendarMaxProjectedPerCron {
			truncated = true
		}
	}

	sort.SliceStable(events, func(i, j int) bool {
		if events[i].StartAt.Equal(events[j].StartAt) {
			return events[i].Title < events[j].Title
		}
		return events[i].StartAt.Before(events[j].StartAt)
	})
	statusCounts := map[string]int{}
	sourceCounts := map[string]int{}
	for _, event := range events {
		statusCounts[event.Status]++
		sourceCounts[event.Source]++
	}
	return RunCalendarResponse{
		GeneratedAt:  now,
		Range:        calendarRange,
		Events:       events,
		Count:        len(events),
		StatusCounts: statusCounts,
		SourceCounts: sourceCounts,
		Truncated:    truncated,
	}
}

func (tr *TestRunner) runCalendarSnapshot(projectID string) runCalendarSnapshot {
	tests := tr.ListTests()
	filteredTests := make([]*ManagedTest, 0, len(tests))
	for _, test := range tests {
		if test == nil {
			continue
		}
		if projectID != "" && normalizeProjectID(test.ProjectID) != projectID {
			continue
		}
		filteredTests = append(filteredTests, test)
	}

	tr.mu.RLock()
	schedules := make([]*storage.ScenarioSchedule, 0)
	scenarios := make(map[string]runCalendarScenarioSummary, len(tr.scenarios))
	for _, scenario := range tr.scenarios {
		if scenario == nil {
			continue
		}
		scenarioProjectID := normalizeProjectID(scenario.ProjectID)
		if projectID != "" && scenarioProjectID != projectID {
			continue
		}
		groups := make([]TestGroup, 0, len(scenario.Scripts))
		totalVUsers := 0
		for _, script := range scenario.Scripts {
			totalVUsers += script.VUsers
			groups = append(groups, TestGroup{
				Name:           script.Name,
				ScriptID:       script.ScriptID,
				ScriptName:     firstNonEmpty(script.ScriptName, script.Name),
				Protocol:       script.Protocol,
				VUsers:         script.VUsers,
				RampUp:         script.RampUp,
				RampDown:       script.RampDown,
				Stages:         append([]storage.LoadStage(nil), script.Stages...),
				Iterations:     script.Iterations,
				Initialization: script.Initialization,
			})
		}
		scenarios[scenario.ID] = runCalendarScenarioSummary{
			Name:        scenario.Name,
			Duration:    scenario.Duration,
			TotalVUsers: totalVUsers,
			Groups:      groups,
		}
		for _, schedule := range scenario.Schedules {
			if schedule == nil {
				continue
			}
			schedules = append(schedules, cloneScenarioSchedule(schedule))
		}
	}
	store := tr.scenarioStore
	tr.mu.RUnlock()

	occurrences := make([]*storage.ScenarioScheduleOccurrence, 0)
	if store != nil {
		if storedOccurrences, err := store.ListAllScenarioScheduleOccurrences(); err == nil {
			for _, occurrence := range storedOccurrences {
				if occurrence == nil {
					continue
				}
				if projectID != "" && normalizeProjectID(occurrence.ProjectID) != projectID {
					continue
				}
				occurrences = append(occurrences, cloneScenarioScheduleOccurrence(occurrence))
			}
		}
	}
	return runCalendarSnapshot{
		Tests:       filteredTests,
		Schedules:   schedules,
		Occurrences: occurrences,
		Scenarios:   scenarios,
	}
}

func (tr *TestRunner) manifestForCalendarTest(test *ManagedTest) *RunArchiveManifest {
	if tr == nil || test == nil || test.Archive == nil || strings.TrimSpace(test.Archive.ManifestPath) == "" {
		return nil
	}
	manifest, err := tr.GetRunArchiveManifest(test.ID)
	if err != nil {
		return nil
	}
	return manifest
}

func runCalendarEventFromOccurrence(occurrence *storage.ScenarioScheduleOccurrence, schedule *storage.ScenarioSchedule, test *ManagedTest, scenarios map[string]runCalendarScenarioSummary, inventory SystemInventoryResponse, manifest *RunArchiveManifest, now time.Time) RunCalendarEvent {
	start := occurrence.PlannedAt.UTC()
	duration := durationForCalendarSchedule(schedule, scenarios)
	end := start.Add(duration)
	if test != nil {
		start = calendarTestStart(test)
		end = calendarTestEnd(test, start, now)
	}
	endAt := end.UTC()
	status := strings.TrimSpace(occurrence.Status)
	if test != nil && strings.TrimSpace(test.Status) != "" {
		status = test.Status
	}
	if status == "" {
		status = "scheduled"
	}
	scenario := scenarios[occurrence.ScenarioID]
	event := RunCalendarEvent{
		ID:               "occurrence:" + occurrence.ID,
		Source:           "cron",
		RunType:          "scheduled",
		Title:            calendarTitle(firstPresentString(testName(test), scheduleRunName(schedule), scenario.Name, "Scheduled Run")),
		Status:           status,
		StartAt:          start.UTC(),
		EndAt:            &endAt,
		OccurrenceStatus: occurrence.Status,
		ReasonCode:       occurrence.ReasonCode,
		ConflictSummary:  firstNonEmpty(occurrence.ConflictSummary, scheduleConflict(schedule)),
		TestID:           occurrence.TestID,
		ScheduleID:       occurrence.ScheduleID,
		ScenarioID:       occurrence.ScenarioID,
		ScenarioName:     scenario.Name,
		ProjectID:        normalizeProjectID(occurrence.ProjectID),
		Workload:         workloadForCalendar(test, scenario, duration),
		User:             userForCalendarSchedule(schedule, test),
		Topology:         topologyForCalendar(test, inventory, manifest, now),
	}
	if test != nil {
		event.TestRefKey = test.RefKey
		event.ApplicationID = test.ApplicationID
		event.ReleaseID = test.ReleaseID
		event.ProjectID = normalizeProjectID(test.ProjectID)
	}
	if schedule != nil {
		event.ScheduleName = schedule.Name
		event.CronExpr = schedule.CronExpr
		event.Timezone = schedule.Timezone
		if event.ReleaseID == "" {
			event.ReleaseID = schedule.ReleaseID
		}
		if event.ProjectID == "" {
			event.ProjectID = normalizeProjectID(schedule.ProjectID)
		}
	}
	return event
}

func runCalendarEventFromTest(test *ManagedTest, scenarios map[string]runCalendarScenarioSummary, inventory SystemInventoryResponse, manifest *RunArchiveManifest, now time.Time) RunCalendarEvent {
	start := calendarTestStart(test)
	end := calendarTestEnd(test, start, now)
	runType := "ad_hoc"
	if test.ScheduledStartAt != nil {
		runType = "one_time_scheduled"
		if test.Status == "SCHEDULED" || test.Status == "RESERVED" {
			window := launchWindowForTest(test, start)
			end = window.EndAt
		}
	}
	endAt := end.UTC()
	scenario := scenarios[test.ScenarioID]
	return RunCalendarEvent{
		ID:            "test:" + test.ID,
		Source:        "ad_hoc",
		RunType:       runType,
		Title:         calendarTitle(firstNonEmpty(test.Name, "Ad-hoc Run")),
		Status:        firstNonEmpty(test.Status, "queued"),
		StartAt:       start.UTC(),
		EndAt:         &endAt,
		TestID:        test.ID,
		TestRefKey:    test.RefKey,
		ScenarioID:    test.ScenarioID,
		ScenarioName:  scenario.Name,
		ApplicationID: test.ApplicationID,
		ReleaseID:     test.ReleaseID,
		ProjectID:     normalizeProjectID(test.ProjectID),
		Workload:      workloadForCalendar(test, scenario, test.Duration),
		User:          userForCalendarTest(test),
		Topology:      topologyForCalendar(test, inventory, manifest, now),
	}
}

func projectRunCalendarSchedule(schedule *storage.ScenarioSchedule, from time.Time, to time.Time, now time.Time, occurrenceByScheduleAt map[string]struct{}, scenarios map[string]runCalendarScenarioSummary) []RunCalendarEvent {
	startFrom := from
	if startFrom.Before(now) {
		startFrom = now
	}
	cursor := startFrom.Add(-time.Minute)
	events := make([]RunCalendarEvent, 0)
	for len(events) < runCalendarMaxProjectedPerCron {
		next, err := nextScenarioScheduleRun(schedule, cursor)
		if err != nil || next == nil || !next.Before(to) {
			break
		}
		plannedAt := next.UTC()
		cursor = plannedAt
		if _, exists := occurrenceByScheduleAt[runCalendarOccurrenceKey(schedule.ID, plannedAt)]; exists {
			continue
		}
		duration := durationForCalendarSchedule(schedule, scenarios)
		endAt := plannedAt.Add(duration).UTC()
		scenario := scenarios[schedule.ScenarioID]
		events = append(events, RunCalendarEvent{
			ID:           fmt.Sprintf("projected:%s:%d", schedule.ID, plannedAt.Unix()),
			Source:       "cron",
			RunType:      "scheduled",
			Title:        calendarTitle(firstPresentString(schedule.RunNameTemplate, schedule.Name, scenario.Name, "Scheduled Run")),
			Status:       "scheduled",
			StartAt:      plannedAt,
			EndAt:        &endAt,
			Projected:    true,
			ScheduleID:   schedule.ID,
			ScheduleName: schedule.Name,
			CronExpr:     schedule.CronExpr,
			Timezone:     schedule.Timezone,
			ScenarioID:   schedule.ScenarioID,
			ScenarioName: scenario.Name,
			ReleaseID:    schedule.ReleaseID,
			ProjectID:    normalizeProjectID(schedule.ProjectID),
			Workload:     workloadForCalendar(nil, scenario, duration),
			User:         userForCalendarSchedule(schedule, nil),
			Topology: RunCalendarTopology{
				Source:  "planned",
				Summary: "Generator assignment will be resolved when the run materializes.",
			},
		})
	}
	return events
}

func runCalendarOccurrenceKey(scheduleID string, plannedAt time.Time) string {
	return scheduleID + "|" + plannedAt.UTC().Format(time.RFC3339)
}

func calendarTestStart(test *ManagedTest) time.Time {
	if test == nil {
		return time.Now().UTC()
	}
	if (test.Status == "SCHEDULED" || test.Status == "RESERVED") && test.ScheduledStartAt != nil {
		return test.ScheduledStartAt.UTC()
	}
	if test.StartedAt != nil && !test.StartedAt.IsZero() {
		return test.StartedAt.UTC()
	}
	return test.CreatedAt.UTC()
}

func calendarTestEnd(test *ManagedTest, start time.Time, now time.Time) time.Time {
	duration := time.Minute
	if test != nil && test.Duration > 0 {
		duration = test.Duration
	}
	end := start.Add(duration)
	if test != nil && test.EndedAt != nil && !test.EndedAt.IsZero() {
		end = test.EndedAt.UTC()
	}
	if test != nil && calendarStatusActive(test.Status) && end.Before(now.Add(time.Minute)) {
		end = now.Add(time.Minute)
	}
	return end.UTC()
}

func durationForCalendarSchedule(schedule *storage.ScenarioSchedule, scenarios map[string]runCalendarScenarioSummary) time.Duration {
	if schedule != nil {
		if scenario, ok := scenarios[schedule.ScenarioID]; ok {
			if duration, err := time.ParseDuration(scenario.Duration); err == nil && duration > 0 {
				return duration
			}
		}
	}
	return time.Hour
}

func workloadForCalendar(test *ManagedTest, scenario runCalendarScenarioSummary, fallbackDuration time.Duration) RunCalendarWorkload {
	if test != nil {
		return RunCalendarWorkload{
			TotalVUsers: test.TotalVUsers,
			Duration:    firstNonEmpty(test.DurationStr, fallbackDuration.String()),
			Groups:      cloneArchiveTestGroups(test.Groups),
		}
	}
	duration := scenario.Duration
	if duration == "" && fallbackDuration > 0 {
		duration = fallbackDuration.String()
	}
	return RunCalendarWorkload{
		TotalVUsers: scenario.TotalVUsers,
		Duration:    duration,
		Groups:      cloneArchiveTestGroups(scenario.Groups),
	}
}

func userForCalendarTest(test *ManagedTest) RunCalendarUser {
	if test == nil {
		return RunCalendarUser{}
	}
	user := RunCalendarUser{
		CreatedBySubject:     test.CreatedBySubject,
		CreatedByDisplayName: test.CreatedByDisplayName,
		StartedBySubject:     test.StartedBySubject,
		StartedByDisplayName: test.StartedByDisplayName,
	}
	user.DisplayName = firstPresentString(test.StartedByDisplayName, test.CreatedByDisplayName, test.StartedBySubject, test.CreatedBySubject)
	user.Subject = firstNonEmpty(test.StartedBySubject, test.CreatedBySubject)
	if test.StartedBySubject != "" || test.StartedByDisplayName != "" {
		user.Attribution = "started_by"
	} else if test.CreatedBySubject != "" || test.CreatedByDisplayName != "" {
		user.Attribution = "created_by"
	}
	return user
}

func userForCalendarSchedule(schedule *storage.ScenarioSchedule, test *ManagedTest) RunCalendarUser {
	user := userForCalendarTest(test)
	if schedule != nil {
		user.OwnerSubject = schedule.OwnerSubject
		user.OwnerDisplayName = schedule.OwnerDisplayName
	}
	if user.DisplayName == "" {
		user.DisplayName = firstNonEmpty(user.OwnerDisplayName, user.OwnerSubject)
		user.Subject = user.OwnerSubject
		if user.DisplayName != "" {
			user.Attribution = "schedule_owner"
		}
	}
	return user
}

func topologyForCalendar(test *ManagedTest, inventory SystemInventoryResponse, manifest *RunArchiveManifest, now time.Time) RunCalendarTopology {
	if test != nil && calendarStatusActive(test.Status) {
		controller := inventory.Controller
		return RunCalendarTopology{
			Source:             "live",
			Controller:         &controller,
			GeneratorShards:    cloneGeneratorShardSummaries(test.GeneratorShards),
			GeneratorInventory: cloneGeneratorInventoryEntries(inventory.Generators),
			Summary:            topologySummary("live", test.GeneratorShards, inventory.Generators),
		}
	}
	if manifest != nil {
		return RunCalendarTopology{
			Source:             "archive",
			ControllerEnv:      cloneStringMap(manifest.Provenance.ControllerEnv),
			GeneratorShards:    cloneGeneratorShardSummaries(manifest.Provenance.GeneratorShards),
			GeneratorInventory: cloneGeneratorInventoryEntries(manifest.Provenance.GeneratorInventory),
			Summary:            topologySummary("archive", manifest.Provenance.GeneratorShards, manifest.Provenance.GeneratorInventory),
		}
	}
	return RunCalendarTopology{
		Source:  "legacy",
		Summary: "Execution topology was not captured for this run.",
	}
}

func topologySummary(source string, shards []GeneratorShardSummary, inventory []GeneratorInventoryEntry) string {
	if len(shards) > 0 {
		return fmt.Sprintf("%d generator shard%s captured from %s topology.", len(shards), pluralSuffix(len(shards)), source)
	}
	if len(inventory) > 0 {
		return fmt.Sprintf("%d generator%s captured from %s inventory.", len(inventory), pluralSuffix(len(inventory)), source)
	}
	return "Generator topology was not captured."
}

func calendarStatusActive(status string) bool {
	switch strings.TrimSpace(strings.ToUpper(status)) {
	case "QUEUED", "PENDING", "RESERVED", "INITIALIZING", "RUNNING", "STOPPING", "PAUSED":
		return true
	default:
		return false
	}
}

func runCalendarEventOverlaps(start time.Time, end *time.Time, from time.Time, to time.Time) bool {
	eventEnd := start.Add(time.Minute)
	if end != nil && !end.IsZero() {
		eventEnd = end.UTC()
	}
	return start.Before(to) && eventEnd.After(from)
}

func testName(test *ManagedTest) string {
	if test == nil {
		return ""
	}
	return test.Name
}

func scheduleRunName(schedule *storage.ScenarioSchedule) string {
	if schedule == nil {
		return ""
	}
	return firstNonEmpty(schedule.RunNameTemplate, schedule.Name)
}

func scheduleConflict(schedule *storage.ScenarioSchedule) string {
	if schedule == nil {
		return ""
	}
	return schedule.ConflictSummary
}

func calendarTitle(title string) string {
	return strings.TrimSpace(title)
}

func firstPresentString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
