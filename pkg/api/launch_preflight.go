package api

import (
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/api/testexec"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type LaunchPreflightRequest struct {
	Intent           string     `json:"intent,omitempty"`
	ScheduledStartAt *time.Time `json:"scheduled_start_at,omitempty"`
	CreateTestRequest
}

func (req *LaunchPreflightRequest) UnmarshalJSON(data []byte) error {
	var envelope struct {
		Intent           string     `json:"intent,omitempty"`
		ScheduledStartAt *time.Time `json:"scheduled_start_at,omitempty"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	var createReq CreateTestRequest
	if err := json.Unmarshal(data, &createReq); err != nil {
		return err
	}
	req.Intent = envelope.Intent
	req.ScheduledStartAt = envelope.ScheduledStartAt
	req.CreateTestRequest = createReq
	if req.ScheduledStartAt != nil {
		req.CreateTestRequest.ScheduledStartAt = cloneTimePtr(req.ScheduledStartAt)
	}
	return nil
}

type LaunchWindow struct {
	StartAt         time.Time `json:"start_at"`
	EndAt           time.Time `json:"end_at"`
	DurationSeconds int       `json:"duration_seconds"`
}

type LaunchConflict struct {
	Code                string     `json:"code"`
	Severity            string     `json:"severity"`
	Source              string     `json:"source,omitempty"`
	ResourceID          string     `json:"resource_id,omitempty"`
	Message             string     `json:"message"`
	StartsAt            *time.Time `json:"starts_at,omitempty"`
	EndsAt              *time.Time `json:"ends_at,omitempty"`
	RequiredGenerators  int        `json:"required_generators,omitempty"`
	AvailableGenerators int        `json:"available_generators,omitempty"`
}

type LaunchPreflightResponse struct {
	Intent            string            `json:"intent,omitempty"`
	Decision          string            `json:"decision"`
	StartNowAllowed   bool              `json:"start_now_allowed"`
	ScheduleAllowed   bool              `json:"schedule_allowed"`
	QueueAllowed      bool              `json:"queue_allowed"`
	QueueReason       string            `json:"queue_reason,omitempty"`
	CandidateWindow   LaunchWindow      `json:"candidate_window"`
	CapacityAdvisory  *CapacityAdvisory `json:"capacity_advisory,omitempty"`
	BlockingConflicts []LaunchConflict  `json:"blocking_conflicts,omitempty"`
	Warnings          []LaunchConflict  `json:"warnings,omitempty"`
}

type launchCapacityEvent struct {
	ID         string
	Source     string
	Title      string
	ProjectID  string
	StartAt    time.Time
	EndAt      time.Time
	Generators int
	VUsers     int
}

func (tr *TestRunner) PreflightLaunch(req LaunchPreflightRequest) (*LaunchPreflightResponse, error) {
	now := time.Now().UTC()
	start := now
	scheduledRequested := req.ScheduledStartAt != nil
	if scheduledRequested {
		start = req.ScheduledStartAt.UTC()
	}

	createReq := req.CreateTestRequest
	if scheduledRequested {
		createReq.ScheduledStartAt = cloneTimePtr(req.ScheduledStartAt)
	}

	tr.mu.RLock()
	preview, err := tr.buildManagedTestLocked(createReq, "launch-preflight", "DRAFT")
	if err != nil {
		tr.mu.RUnlock()
		return nil, err
	}
	window := launchWindowForTest(preview, start)
	blocking := make([]LaunchConflict, 0)
	warnings := make([]LaunchConflict, 0)

	if preview.CapacityAdvisory != nil && preview.CapacityAdvisory.RequiresOverride {
		blocking = append(blocking, LaunchConflict{
			Code:                "capacity_shortfall",
			Severity:            "blocking",
			Source:              "capacity_advisory",
			Message:             fmt.Sprintf("Capacity advisory recommends %d load generators but only %d are available.", preview.CapacityAdvisory.RecommendedGenerators, preview.CapacityAdvisory.AvailableGenerators),
			RequiredGenerators:  preview.CapacityAdvisory.RecommendedGenerators,
			AvailableGenerators: preview.CapacityAdvisory.AvailableGenerators,
		})
	}
	if conflict := browserDensityGovernanceLaunchConflict(preview.CapacityAdvisory); conflict != nil {
		blocking = append(blocking, *conflict)
	}
	if conflicts := externalRunnerLaunchConflicts(preview.Groups); len(conflicts) > 0 {
		blocking = append(blocking, conflicts...)
	}
	if conflicts := javaVuserRunnerLaunchConflicts(preview.Groups); len(conflicts) > 0 {
		blocking = append(blocking, conflicts...)
	}
	if conflicts := importPromotionLaunchConflicts(preview.Groups); len(conflicts) > 0 {
		blocking = append(blocking, conflicts...)
	}
	if conflicts := tr.generatorPlacementLaunchConflictsLocked(preview); len(conflicts) > 0 {
		blocking = append(blocking, conflicts...)
	}

	if conflicts := tr.launchWindowConflictsLocked(preview, window, now, ""); len(conflicts) > 0 {
		blocking = append(blocking, conflicts...)
	}

	currentBlocked := false
	currentReason := ""
	if !scheduledRequested {
		if blocked, reason := tr.evaluateQuotaBlockLocked(preview, ""); blocked {
			currentBlocked = true
			currentReason = reason
			warnings = append(warnings, LaunchConflict{
				Code:     "current_capacity_unavailable",
				Severity: "warning",
				Source:   "controller",
				Message:  reason,
			})
		}
	}
	tr.mu.RUnlock()

	startNowAllowed := !scheduledRequested && len(blocking) == 0 && !currentBlocked
	scheduleAllowed := scheduledRequested && len(blocking) == 0
	queueAllowed := !scheduledRequested && currentBlocked && len(blocking) == 0
	decision := "blocked"
	switch {
	case startNowAllowed:
		decision = "start_now"
	case scheduleAllowed:
		decision = "schedule"
	case queueAllowed:
		decision = "queue"
	}

	return &LaunchPreflightResponse{
		Intent:            strings.TrimSpace(req.Intent),
		Decision:          decision,
		StartNowAllowed:   startNowAllowed,
		ScheduleAllowed:   scheduleAllowed,
		QueueAllowed:      queueAllowed,
		QueueReason:       currentReason,
		CandidateWindow:   window,
		CapacityAdvisory:  cloneCapacityAdvisory(preview.CapacityAdvisory),
		BlockingConflicts: blocking,
		Warnings:          warnings,
	}, nil
}

func browserDensityGovernanceLaunchConflict(advisory *CapacityAdvisory) *LaunchConflict {
	if !browserDensityReleaseGateBlocks(advisory) {
		return nil
	}
	return &LaunchConflict{
		Code:     "browser_density_release_gate",
		Severity: "blocking",
		Source:   "capacity_advisory",
		Message:  browserDensityReleaseGateMessage(advisory),
	}
}

// groupPlacementConflict is a per-group placement eligibility failure. Group is
// empty for fleet-level failures (bad assignment, no configured generators).
type groupPlacementConflict struct {
	Group   string
	Message string
}

// evaluateGeneratorPlacementConflictsLocked is the shared per-group placement
// eligibility check used by BOTH launch preflight and the scenario-builder
// load-model report, so the two surfaces never disagree (M1).
//
// It is a STATIC, configuration-time check: it validates drain state, protocol,
// version, and required-label compatibility against the configured fleet, but it
// intentionally does NOT dial generators to verify live reachability. Probing the
// network here would block under tr.mu and contradict the fast static contract;
// live reachability is enforced fail-closed at Run() via
// generatorPlacementRejectionMessage ("... is not reachable"), so a host that is
// configured + active but currently down is caught at launch rather than here (M2).
func (tr *TestRunner) evaluateGeneratorPlacementConflictsLocked(test *ManagedTest) []groupPlacementConflict {
	if test == nil {
		return nil
	}
	backend, ok := tr.execution.(*distributedExecutionBackend)
	if !ok {
		return nil
	}
	rootAssignment, err := normalizeGeneratorAssignmentSettings(test.Assignment)
	if err != nil {
		return []groupPlacementConflict{{Message: err.Error()}}
	}
	defaultTargets, err := backend.allowedTargetsForTest(test)
	if err != nil {
		return []groupPlacementConflict{{Message: err.Error()}}
	}
	if _, err := backend.explicitGroupTargetsForTest(test); err != nil {
		return []groupPlacementConflict{{Message: err.Error()}}
	}
	specs := tr.enrichGeneratorTargetSpecsWithLabels(backend.configuredTargetSpecs())
	if len(specs) == 0 {
		return []groupPlacementConflict{{Message: "no generator targets configured"}}
	}
	activeTargets := map[string]generatorLoadSnapshot{}
	for _, spec := range specs {
		if backend.generatorDrainState(spec.Address) != "active" {
			continue
		}
		activeTargets[spec.Address] = generatorLoadSnapshot{Target: spec.Address}
	}
	defaultLoad := loadSnapshotsForTargets(activeTargets, defaultTargets)
	compatibility := newGeneratorCompatibilityIndex(specs, activeTargets)
	conflicts := make([]groupPlacementConflict, 0)
	for _, group := range test.Groups {
		if group.Assignment != nil && group.Assignment.Mode == generatorAssignmentModeSpecificHosts {
			shards := chooseGeneratorShardsForGroup(group, activeTargets, defaultLoad, orderedUniqueTargets(mapKeys(activeTargets)), defaultTargets, rootAssignment, nil, compatibility)
			if len(shards) == 0 {
				conflicts = append(conflicts, groupPlacementConflict{Group: group.Name, Message: generatorPlacementRejectionMessage(group, activeTargets, specs, rootAssignment)})
			}
			continue
		}
		candidateLoad := defaultLoad
		candidatePreferred := defaultTargets
		effectiveAssignment := rootAssignment
		if group.Assignment != nil && group.Assignment.Mode == generatorAssignmentModeAuto {
			candidateLoad = activeTargets
			candidatePreferred = orderedUniqueTargets(mapKeys(activeTargets))
			effectiveAssignment = group.Assignment
		}
		if target := chooseGeneratorTargetForGroup(group, candidateLoad, candidatePreferred, nil, compatibility, effectiveGroupRequiredLabels(group, effectiveAssignment)); target == "" {
			conflicts = append(conflicts, groupPlacementConflict{Group: group.Name, Message: generatorPlacementRejectionMessage(group, candidateLoad, specs, effectiveAssignment)})
		}
	}
	return conflicts
}

func (tr *TestRunner) generatorPlacementLaunchConflictsLocked(test *ManagedTest) []LaunchConflict {
	conflicts := tr.evaluateGeneratorPlacementConflictsLocked(test)
	if len(conflicts) == 0 {
		return nil
	}
	out := make([]LaunchConflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		out = append(out, generatorPlacementLaunchConflict(conflict.Message))
	}
	return out
}

func generatorPlacementLaunchConflict(message string) LaunchConflict {
	return LaunchConflict{
		Code:     "generator_placement",
		Severity: "blocking",
		Source:   "generator_assignment",
		Message:  message,
	}
}

func mapKeys(values map[string]generatorLoadSnapshot) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// browserDensityReleaseGateMessage renders the operator-facing remediation
// string for a blocking browser-density release gate. Shared by the preflight
// conflict and the start-time gate error so the guidance stays identical.
func browserDensityReleaseGateMessage(advisory *CapacityAdvisory) string {
	maxCanary := browserDiagnosticCanaryEnvelope().MaximumClaimedVUsersPerGenerator
	if advisory != nil && advisory.MaxBrowserCanaryVUsersPerGenerator > 0 {
		maxCanary = advisory.MaxBrowserCanaryVUsersPerGenerator
	}
	return fmt.Sprintf("Browser VUsers exceed the diagnostic canary envelope of %d VUsers/generator; attach benchmark evidence and set APEXLOAD_BROWSER_DENSITY_OVERRIDE=true before using the bounded real-browser-load tier.", maxCanary)
}

func launchWindowForTest(test *ManagedTest, start time.Time) LaunchWindow {
	start = start.UTC()
	preBaseline, postBaseline := testexec.ResourceBaselineWindows(test != nil && test.ResourceBaseline)
	startDelay := time.Duration(0)
	duration := time.Minute
	maxRampDown := time.Duration(0)
	if test != nil {
		startDelay = testexec.StartDelayWindow(test.StartDelay)
		if test.Duration > 0 {
			duration = test.Duration
		}
		maxRampDown = maxGroupRampDown(test.Groups)
	}
	end := start.Add(startDelay + preBaseline + duration + maxRampDown + postBaseline).UTC()
	if !end.After(start) {
		end = start.Add(time.Minute)
	}
	return LaunchWindow{
		StartAt:         start,
		EndAt:           end,
		DurationSeconds: int(end.Sub(start).Seconds()),
	}
}

func maxGroupRampDown(groups []TestGroup) time.Duration {
	maxRamp := time.Duration(0)
	for _, group := range groups {
		if group.RampDown == "" {
			continue
		}
		duration, err := time.ParseDuration(group.RampDown)
		if err == nil && duration > maxRamp {
			maxRamp = duration
		}
	}
	return maxRamp
}

func (tr *TestRunner) launchWindowConflictsLocked(test *ManagedTest, window LaunchWindow, now time.Time, excludeTestID string) []LaunchConflict {
	conflicts := make([]LaunchConflict, 0)
	conflicts = append(conflicts, tr.launchGovernanceWindowConflictsLocked(test, window)...)

	proposal := launchCapacityEvent{
		ID:         "proposal",
		Source:     "proposal",
		Title:      test.Name,
		ProjectID:  normalizeProjectID(test.ProjectID),
		StartAt:    window.StartAt,
		EndAt:      window.EndAt,
		Generators: recommendedGeneratorCountForTest(test),
		VUsers:     test.TotalVUsers,
	}
	if proposal.Generators < 1 && proposal.VUsers > 0 {
		proposal.Generators = 1
	}

	events := tr.launchCapacityEventsLocked(window, now, excludeTestID)
	sampleTimes := launchSampleTimes(window, events, tr.settings.CapacityPlanning.Reservations)
	for _, sample := range sampleTimes {
		overlapping := overlappingLaunchEvents(events, sample)
		if conflict := tr.launchCapacityConflictAtLocked(proposal, overlapping, sample); conflict != nil {
			conflicts = append(conflicts, *conflict)
			break
		}
	}
	return conflicts
}

func (tr *TestRunner) launchCapacityEventsLocked(window LaunchWindow, now time.Time, excludeTestID string) []launchCapacityEvent {
	events := make([]launchCapacityEvent, 0)
	includeActiveTests := window.StartAt.After(now.Add(time.Minute))
	for id, test := range tr.tests {
		if test == nil || id == excludeTestID {
			continue
		}
		if !includeActiveTests && launchTestStatusOccupiesCurrentCapacity(test.Status) {
			continue
		}
		event, ok := tr.launchCapacityEventForTestLocked(test, now)
		if !ok || !launchIntervalsOverlap(window.StartAt, window.EndAt, event.StartAt, event.EndAt) {
			continue
		}
		events = append(events, event)
	}

	occurrences := map[string]struct{}{}
	if tr.scenarioStore != nil {
		if storedOccurrences, err := tr.scenarioStore.ListAllScenarioScheduleOccurrences(); err == nil {
			for _, occurrence := range storedOccurrences {
				if occurrence != nil {
					occurrences[runCalendarOccurrenceKey(occurrence.ScheduleID, occurrence.PlannedAt)] = struct{}{}
				}
			}
		}
	}
	for _, schedule := range tr.listAllScenarioSchedulesLocked() {
		if schedule == nil || !schedule.Enabled || schedule.NextRequiredAction == "approval_required" || strings.TrimSpace(schedule.ConflictSummary) != "" {
			continue
		}
		scenario, ok := tr.scenarios[schedule.ScenarioID]
		if !ok || scenario == nil {
			continue
		}
		cursor := window.StartAt.Add(-time.Minute)
		for projected := 0; projected < runCalendarMaxProjectedPerCron; projected++ {
			next, err := nextScenarioScheduleRun(schedule, cursor)
			if err != nil || next == nil || !next.Before(window.EndAt) {
				break
			}
			plannedAt := next.UTC()
			cursor = plannedAt
			if plannedAt.Before(now) {
				continue
			}
			if _, exists := occurrences[runCalendarOccurrenceKey(schedule.ID, plannedAt)]; exists {
				continue
			}
			event := tr.launchCapacityEventForScenarioLocked(schedule, scenario, plannedAt)
			if launchIntervalsOverlap(window.StartAt, window.EndAt, event.StartAt, event.EndAt) {
				events = append(events, event)
			}
		}
	}
	return events
}

func launchTestStatusOccupiesCurrentCapacity(status string) bool {
	return status == "RUNNING" || status == "INITIALIZING" || status == "PAUSED"
}

func (tr *TestRunner) launchCapacityEventForTestLocked(test *ManagedTest, now time.Time) (launchCapacityEvent, bool) {
	if test == nil {
		return launchCapacityEvent{}, false
	}
	start := calendarTestStart(test)
	if test.Status == "SCHEDULED" || test.Status == "RESERVED" {
		if test.ScheduledStartAt == nil {
			return launchCapacityEvent{}, false
		}
		start = test.ScheduledStartAt.UTC()
	} else if test.Status != "RUNNING" && test.Status != "INITIALIZING" && test.Status != "PAUSED" {
		return launchCapacityEvent{}, false
	}
	end := calendarTestEnd(test, start, now)
	if test.Status == "SCHEDULED" || test.Status == "RESERVED" {
		window := launchWindowForTest(test, start)
		end = window.EndAt
	}
	generators := recommendedGeneratorCountForTest(test)
	if generators < 1 && test.TotalVUsers > 0 {
		generators = 1
	}
	return launchCapacityEvent{
		ID:         test.ID,
		Source:     "test",
		Title:      firstNonEmpty(test.Name, "Run"),
		ProjectID:  normalizeProjectID(test.ProjectID),
		StartAt:    start.UTC(),
		EndAt:      end.UTC(),
		Generators: generators,
		VUsers:     test.TotalVUsers,
	}, true
}

func (tr *TestRunner) launchCapacityEventForScenarioLocked(schedule *storage.ScenarioSchedule, scenario *StoredScenario, plannedAt time.Time) launchCapacityEvent {
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
	duration, err := time.ParseDuration(scenario.Duration)
	if err != nil || duration <= 0 {
		duration = time.Hour
	}
	testLike := &ManagedTest{
		Name:             firstPresentString(schedule.RunNameTemplate, schedule.Name, scenario.Name, "Scheduled Run"),
		ProjectID:        normalizeProjectID(scenario.ProjectID),
		Groups:           groups,
		TotalVUsers:      totalVUsers,
		Duration:         duration,
		StartDelay:       scenario.StartDelay,
		ResourceBaseline: scenario.ResourceBaseline,
		RuntimeSettings:  cloneRuntimeSettings(scenario.RuntimeSettings),
		Assignment:       cloneGeneratorAssignmentSettings(scenario.Assignment),
	}
	advisory := tr.estimateCapacityAdvisoryForAssignment(testLike.ProjectID, groups, totalVUsers, duration, scenario.ResourceBaseline, testLike.RuntimeSettings, testLike.Assignment)
	testLike.CapacityAdvisory = advisory
	window := launchWindowForTest(testLike, plannedAt)
	generators := recommendedGeneratorCountForTest(testLike)
	if generators < 1 && totalVUsers > 0 {
		generators = 1
	}
	return launchCapacityEvent{
		ID:         schedule.ID,
		Source:     "cron",
		Title:      testLike.Name,
		ProjectID:  testLike.ProjectID,
		StartAt:    window.StartAt,
		EndAt:      window.EndAt,
		Generators: generators,
		VUsers:     totalVUsers,
	}
}

func launchSampleTimes(window LaunchWindow, events []launchCapacityEvent, reservations []CapacityReservation) []time.Time {
	values := []time.Time{window.StartAt, window.EndAt.Add(-time.Second)}
	for _, event := range events {
		if launchInstantInWindow(event.StartAt, window) {
			values = append(values, event.StartAt)
		}
		if launchInstantInWindow(event.EndAt, window) {
			values = append(values, event.EndAt)
		}
	}
	for _, reservation := range normalizeCapacityReservations(reservations) {
		if reservation.StartsAt != nil && launchInstantInWindow(reservation.StartsAt.UTC(), window) {
			values = append(values, reservation.StartsAt.UTC())
		}
		if reservation.EndsAt != nil && launchInstantInWindow(reservation.EndsAt.UTC(), window) {
			values = append(values, reservation.EndsAt.UTC())
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Before(values[j]) })
	out := make([]time.Time, 0, len(values))
	seen := map[int64]struct{}{}
	for _, value := range values {
		key := value.Unix()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func overlappingLaunchEvents(events []launchCapacityEvent, sample time.Time) []launchCapacityEvent {
	out := make([]launchCapacityEvent, 0)
	for _, event := range events {
		if !sample.Before(event.StartAt) && sample.Before(event.EndAt) {
			out = append(out, event)
		}
	}
	return out
}

func (tr *TestRunner) launchCapacityConflictAtLocked(proposal launchCapacityEvent, existing []launchCapacityEvent, sample time.Time) *LaunchConflict {
	availability := resolveCapacityAvailabilityAt(tr.settings, sample).EffectiveAvailableGenerators
	if availability < 1 {
		availability = tr.settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators
	}
	totalGenerators := proposal.Generators
	existingGenerators := 0
	globalTests := 1
	projectTests := 1
	projectVUsers := proposal.VUsers
	byProject := map[string]int{proposal.ProjectID: proposal.Generators}
	for _, event := range existing {
		totalGenerators += event.Generators
		existingGenerators += event.Generators
		globalTests++
		byProject[normalizeProjectID(event.ProjectID)] += event.Generators
		if normalizeProjectID(event.ProjectID) == proposal.ProjectID {
			projectTests++
			projectVUsers += event.VUsers
		}
	}
	if tr.settings.General.MaxConcurrentTests > 0 && globalTests > tr.settings.General.MaxConcurrentTests {
		return launchConflictAt("global_concurrency_window", "controller", "", fmt.Sprintf("Window would exceed global max concurrent tests (%d).", tr.settings.General.MaxConcurrentTests), sample, totalGenerators, availability)
	}
	if maxConcurrent := tr.projectMaxConcurrentTests(proposal.ProjectID); maxConcurrent > 0 && projectTests > maxConcurrent {
		return launchConflictAt("project_concurrency_window", "project", proposal.ProjectID, fmt.Sprintf("Window would exceed project max concurrent tests (%d).", maxConcurrent), sample, totalGenerators, availability)
	}
	if maxVUsers := tr.projectMaxVUsers(proposal.ProjectID); maxVUsers > 0 && projectVUsers > maxVUsers {
		return launchConflictAt("project_vuser_window", "project", proposal.ProjectID, fmt.Sprintf("Window would exceed project VUser capacity (%d max VUsers).", maxVUsers), sample, totalGenerators, availability)
	}
	if existingGenerators > 0 && totalGenerators > availability {
		return launchConflictAt("generator_capacity_window", "controller", "", fmt.Sprintf("Window needs %d generators but only %d are available at %s.", totalGenerators, availability, sample.Format(time.RFC3339)), sample, totalGenerators, availability)
	}
	if conflict := tr.launchReservationConflictAtLocked(proposal, byProject, sample, availability); conflict != nil {
		return conflict
	}
	return nil
}

func (tr *TestRunner) launchReservationConflictAtLocked(proposal launchCapacityEvent, byProject map[string]int, sample time.Time, availability int) *LaunchConflict {
	activeSlots, err := tr.boundTimeslotsActiveAtLocked(sample)
	if err != nil {
		// Fail closed: report a transient conflict rather than clearing the launch
		// when reservation state cannot be read.
		return launchConflictAt("reservation_window", "capacity_reservation", proposal.ProjectID,
			fmt.Sprintf("Reservation state is temporarily unavailable at %s; try again.", sample.Format(time.RFC3339)), sample, 0, 0)
	}
	totalReserved, _ := reservationTotalsWithTimeslots(tr.settings, "", sample, activeSlots)
	if totalReserved <= 0 {
		return nil
	}
	if totalReserved > availability {
		totalReserved = availability
	}
	shared := maxInt(0, availability-totalReserved)
	sharedAfter := 0
	for projectID, active := range byProject {
		_, projectReserved := reservationTotalsWithTimeslots(tr.settings, projectID, sample, activeSlots)
		sharedAfter += maxInt(0, active-projectReserved)
	}
	if sharedAfter > shared {
		return launchConflictAt("reservation_window", "capacity_reservation", proposal.ProjectID, fmt.Sprintf("A capacity reservation reduces the shared pool at %s.", sample.Format(time.RFC3339)), sample, sharedAfter, shared)
	}
	return nil
}

func launchConflictAt(code string, source string, resourceID string, message string, sample time.Time, required int, available int) *LaunchConflict {
	startsAt := sample.UTC()
	return &LaunchConflict{
		Code:                code,
		Severity:            "blocking",
		Source:              source,
		ResourceID:          resourceID,
		Message:             message,
		StartsAt:            &startsAt,
		RequiredGenerators:  required,
		AvailableGenerators: available,
	}
}

func (tr *TestRunner) launchGovernanceWindowConflictsLocked(test *ManagedTest, window LaunchWindow) []LaunchConflict {
	if test == nil {
		return nil
	}
	projectID := normalizeProjectID(test.ProjectID)
	conflicts := make([]LaunchConflict, 0)
	for _, schedule := range tr.listAllScenarioSchedulesLocked() {
		if schedule == nil || normalizeProjectID(schedule.ProjectID) != projectID {
			continue
		}
		for _, blocked := range schedule.BlackoutWindows {
			if launchScheduleWindowOverlaps(blocked, schedule.Timezone, window.StartAt, window.EndAt) {
				conflicts = append(conflicts, launchScheduleWindowConflict("blackout_window", "blackout", schedule.ID, blocked, window.StartAt))
				return conflicts
			}
		}
		for _, blocked := range schedule.MaintenanceWindows {
			if launchScheduleWindowOverlaps(blocked, schedule.Timezone, window.StartAt, window.EndAt) {
				conflicts = append(conflicts, launchScheduleWindowConflict("maintenance_window", "maintenance", schedule.ID, blocked, window.StartAt))
				return conflicts
			}
		}
		if schedule.ScenarioID == test.ScenarioID && len(schedule.AvailabilityWindows) > 0 && !launchScheduleAvailabilityAllows(schedule, window.StartAt, window.EndAt) {
			conflicts = append(conflicts, launchScheduleWindowConflict("availability_window", "availability", schedule.ID, schedule.AvailabilityWindows[0], window.StartAt))
			return conflicts
		}
	}
	return conflicts
}

func launchScheduleWindowConflict(code string, source string, scheduleID string, window storage.ScenarioScheduleWindow, startsAt time.Time) LaunchConflict {
	at := startsAt.UTC()
	return LaunchConflict{
		Code:       code,
		Severity:   "blocking",
		Source:     source,
		ResourceID: scheduleID,
		Message:    fmt.Sprintf("Launch window conflicts with %s window %q.", source, firstNonEmpty(window.Name, window.StartTime+"-"+window.EndTime)),
		StartsAt:   &at,
	}
}

func launchScheduleAvailabilityAllows(schedule *storage.ScenarioSchedule, start time.Time, end time.Time) bool {
	if schedule == nil || len(schedule.AvailabilityWindows) == 0 {
		return true
	}
	return launchScheduleWindowAllowedAt(schedule.AvailabilityWindows, schedule.Timezone, start) &&
		launchScheduleWindowAllowedAt(schedule.AvailabilityWindows, schedule.Timezone, end.Add(-time.Second))
}

func launchScheduleWindowAllowedAt(windows []storage.ScenarioScheduleWindow, fallbackTimezone string, candidate time.Time) bool {
	for _, window := range windows {
		if matches, err := scheduleWindowContains(window, fallbackTimezone, candidate); err == nil && matches {
			return true
		}
	}
	return false
}

func launchScheduleWindowOverlaps(window storage.ScenarioScheduleWindow, fallbackTimezone string, start time.Time, end time.Time) bool {
	if !end.After(start) {
		return false
	}
	step := 5 * time.Minute
	for cursor := start.UTC(); cursor.Before(end); cursor = cursor.Add(step) {
		if matches, err := scheduleWindowContains(window, fallbackTimezone, cursor); err == nil && matches {
			return true
		}
	}
	last := end.Add(-time.Second)
	matches, err := scheduleWindowContains(window, fallbackTimezone, last)
	return err == nil && matches
}

func launchIntervalsOverlap(leftStart time.Time, leftEnd time.Time, rightStart time.Time, rightEnd time.Time) bool {
	return leftStart.Before(rightEnd) && rightStart.Before(leftEnd)
}

func launchInstantInWindow(value time.Time, window LaunchWindow) bool {
	return !value.Before(window.StartAt) && value.Before(window.EndAt)
}

func (tr *TestRunner) launchStartWindowErrorLocked(test *ManagedTest, start time.Time, excludeTestID string) error {
	if test == nil {
		return nil
	}
	window := launchWindowForTest(test, start.UTC())
	conflicts := tr.launchWindowConflictsLocked(test, window, start.UTC(), excludeTestID)
	if len(conflicts) == 0 {
		return nil
	}
	return fmt.Errorf("launch window conflict: %s", conflicts[0].Message)
}

func (tr *TestRunner) dueOneTimeScheduledTestIDsLocked(now time.Time) []string {
	ids := make([]string, 0)
	for id, test := range tr.tests {
		if test == nil || test.Status != "SCHEDULED" || test.ScheduledStartAt == nil {
			continue
		}
		if test.ScheduledStartAt.After(now) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (tr *TestRunner) markScheduledStartError(id string, message string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	test, ok := tr.tests[id]
	if !ok || test == nil || test.Status != "SCHEDULED" {
		return
	}
	test.Error = message
	tr.saveManagedTestRecord(test)
	tr.broadcastEvent("test_schedule_blocked", test)
}
