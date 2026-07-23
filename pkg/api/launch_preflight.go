package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/api/testexec"
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
	// RecommendedAssignment is populated when the launch is blocked so operators
	// get a concrete recovery path (recommended generator count/targets) rather
	// than just a wall. Mirrors the recommendation returned by the estimate API.
	RecommendedAssignment *AssignmentRecommendation `json:"recommended_assignment,omitempty"`
}

type launchCapacityEvent struct {
	ID                 string
	Source             string
	Title              string
	ProjectID          string
	StartAt            time.Time
	EndAt              time.Time
	Generators         int
	VUsers             int
	ApprovedQuotaBurst bool
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
	if conflicts := tr.javaVuserRunnerLaunchConflictsForManagedTestLocked(preview); len(conflicts) > 0 {
		blocking = append(blocking, conflicts...)
	}
	if conflicts := importPromotionLaunchConflicts(preview.Groups); len(conflicts) > 0 {
		blocking = append(blocking, conflicts...)
	}
	if conflicts := tr.generatorPlacementLaunchConflictsLocked(preview); len(conflicts) > 0 {
		blocking = append(blocking, conflicts...)
	}

	if poolBlocking, poolWarnings := tr.poolReadinessLaunchConflictsLocked(preview); len(poolBlocking) > 0 || len(poolWarnings) > 0 {
		blocking = append(blocking, poolBlocking...)
		warnings = append(warnings, poolWarnings...)
	}

	if conflicts := tr.launchWindowConflictsLocked(preview, window, now, "", ""); len(conflicts) > 0 {
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
	// When blocked, surface a recommended assignment (same helper the estimate API
	// uses) so the operator sees how much capacity the run needs. Computed under
	// the read lock before release.
	var recommendation *AssignmentRecommendation
	if len(blocking) > 0 {
		recommendation = tr.buildAssignmentRecommendation(preview)
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
		Intent:                strings.TrimSpace(req.Intent),
		Decision:              decision,
		StartNowAllowed:       startNowAllowed,
		ScheduleAllowed:       scheduleAllowed,
		QueueAllowed:          queueAllowed,
		QueueReason:           currentReason,
		CandidateWindow:       window,
		CapacityAdvisory:      cloneCapacityAdvisory(preview.CapacityAdvisory),
		BlockingConflicts:     blocking,
		Warnings:              warnings,
		RecommendedAssignment: recommendation,
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
	inventory, inventoryCapturedAt := tr.cachedGeneratorInventorySnapshot()
	for _, entry := range inventory {
		if snapshot, ok := activeTargets[entry.Target]; ok {
			snapshot.HeartbeatOK = generatorInventoryEntryReachable(entry)
			snapshot.SupportedProtocols = append([]string(nil), entry.SupportedProtocols...)
			snapshot.CompatibleFeatures = append([]string(nil), entry.CompatibleFeatures...)
			snapshot.AvailableCapacity = entry.AvailableCapacity
			snapshot.RuntimeCapacities = cloneProtocolRuntimeCapacities(entry.RuntimeCapacities)
			activeTargets[entry.Target] = snapshot
		}
	}
	projected := make(map[string]generatorLoadSnapshot, len(activeTargets))
	for target, snapshot := range activeTargets {
		snapshot.RuntimeCapacities = cloneProtocolRuntimeCapacities(snapshot.RuntimeCapacities)
		projected[target] = snapshot
	}
	tr.reserveUnreflectedJavaVuserCapacityLocked(test, projected, specs, inventoryCapturedAt)
	defaultLoad := loadSnapshotsForTargets(projected, defaultTargets)
	compatibility := newGeneratorCompatibilityIndex(specs, projected)
	conflicts := make([]groupPlacementConflict, 0)
	for _, group := range test.Groups {
		shards := chooseGeneratorShardsForGroup(
			group,
			projected,
			defaultLoad,
			orderedUniqueTargets(mapKeys(projected)),
			defaultTargets,
			rootAssignment,
			nil,
			compatibility,
		)
		rejectionLoad := defaultLoad
		effectiveAssignment := rootAssignment
		if group.Assignment != nil &&
			(group.Assignment.Mode == generatorAssignmentModeAuto || group.Assignment.Mode == generatorAssignmentModeSpecificHosts) {
			rejectionLoad = projected
			effectiveAssignment = group.Assignment
		}
		if len(shards) == 0 {
			conflicts = append(conflicts, groupPlacementConflict{Group: group.Name, Message: generatorPlacementRejectionMessage(group, rejectionLoad, specs, effectiveAssignment)})
			continue
		}
		for _, shard := range shards {
			if shard.Target == "" {
				continue
			}
			snapshot := projected[shard.Target]
			if shard.VUsers > 0 {
				snapshot.AvailableCapacity -= int32(shard.VUsers)
				snapshot.Stats.ActiveVUsers += int32(shard.VUsers)
			}
			consumeGroupRuntimeCapacity(&snapshot, group, shard.VUsers)
			projected[shard.Target] = snapshot
			if _, ok := defaultLoad[shard.Target]; ok {
				defaultLoad[shard.Target] = snapshot
			}
		}
	}
	return conflicts
}

// reserveUnreflectedJavaVuserCapacityLocked overlays controller admissions that
// a cached generator heartbeat cannot yet contain. StartTest serializes the
// INITIALIZING transition under tr.mu, so a second Java run sees the first
// admission immediately instead of assigning against the same stale JVM slots.
// Once a post-start inventory snapshot exists, its AvailableVUsers is
// authoritative and the overlay stops, avoiding double-accounting long-running
// sessions already reflected by the generator.
func (tr *TestRunner) reserveUnreflectedJavaVuserCapacityLocked(candidate *ManagedTest, projected map[string]generatorLoadSnapshot, specs []generatorTargetSpec, inventoryCapturedAt time.Time) {
	if len(projected) == 0 || inventoryCapturedAt.IsZero() {
		return
	}
	active := make([]*ManagedTest, 0)
	for _, test := range tr.tests {
		if test == nil || test == candidate ||
			(candidate != nil && strings.TrimSpace(test.ID) != "" && test.ID == candidate.ID) ||
			!testReservesRuntimeCapacity(test) ||
			!groupsUseJavaVuserProtocol(test.Groups) {
			continue
		}
		if test.StartedAt != nil && !test.StartedAt.IsZero() &&
			inventoryCapturedAt.After(test.StartedAt.UTC().Add(time.Second)) {
			continue
		}
		active = append(active, test)
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].CreatedAt.Equal(active[j].CreatedAt) {
			return active[i].ID < active[j].ID
		}
		return active[i].CreatedAt.Before(active[j].CreatedAt)
	})
	compatibility := newGeneratorCompatibilityIndex(specs, projected)
	preferredAll := orderedUniqueTargets(mapKeys(projected))
	for _, test := range active {
		rootAssignment, err := normalizeGeneratorAssignmentSettings(test.Assignment)
		if err != nil {
			continue
		}
		backend, ok := tr.execution.(*distributedExecutionBackend)
		if !ok {
			continue
		}
		defaultTargets, err := backend.allowedTargetsForTest(test)
		if err != nil {
			continue
		}
		defaultLoad := loadSnapshotsForTargets(projected, defaultTargets)
		for _, group := range test.Groups {
			if !groupUsesJavaVuserProtocol(group) {
				continue
			}
			// Reserve the configured peak, not merely the current shard count:
			// a ramped persistent VUser still needs a JVM slot when it starts.
			shards := chooseGeneratorShardsForGroup(
				group,
				projected,
				defaultLoad,
				preferredAll,
				defaultTargets,
				rootAssignment,
				nil,
				compatibility,
			)
			for _, shard := range shards {
				snapshot, ok := projected[shard.Target]
				if !ok {
					continue
				}
				consumeGroupRuntimeCapacity(&snapshot, group, shard.VUsers)
				projected[shard.Target] = snapshot
				if _, ok := defaultLoad[shard.Target]; ok {
					defaultLoad[shard.Target] = snapshot
				}
			}
		}
	}
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

// poolReadinessLaunchConflictsLocked makes fleet-pool state visible to preflight:
// a pool-scoped assignment whose pool is missing, suspended, or empty blocks the
// launch (rather than failing lazily at Run time), and a pool with fewer live
// members than the run is sized for yields a non-blocking shortfall warning.
// Tests without a pool-scoped assignment are unaffected. Per-pool project quotas
// are not enforced here — for an immediate launch they are checked by
// evaluateQuotaBlockLocked below; the windowed scheduled-run projection does not
// re-evaluate them (matching its pre-existing blindness to pool-scoped
// reservations), so a scheduled pool run over its pool quota is caught and queued
// by the authoritative start-time gate rather than blocked in preflight.
func (tr *TestRunner) poolReadinessLaunchConflictsLocked(test *ManagedTest) (blocking, warnings []LaunchConflict) {
	if test == nil || test.Assignment == nil || tr.fleetStore == nil {
		return nil, nil
	}
	poolID := strings.TrimSpace(test.Assignment.PoolID)
	if poolID == "" {
		return nil, nil
	}
	pool, err := tr.fleetStore.GetFleetPool(poolID)
	if err != nil || pool == nil {
		return []LaunchConflict{poolReadinessConflict(poolID, fmt.Sprintf("Assigned generator pool %q was not found.", poolID))}, nil
	}
	poolLabel := firstNonEmpty(pool.Name, poolID)
	if status := strings.ToLower(strings.TrimSpace(pool.Status)); status != "" && status != "active" {
		return []LaunchConflict{poolReadinessConflict(poolID, fmt.Sprintf("Generator pool %q is %s; activate it or pick another pool before launching.", poolLabel, status))}, nil
	}
	members := tr.poolMemberTargets(poolID)
	if len(members) == 0 {
		return []LaunchConflict{poolReadinessConflict(poolID, fmt.Sprintf("Generator pool %q has no available members.", poolLabel))}, nil
	}
	if recommended := recommendedGeneratorCountForTest(test); recommended > len(members) {
		warnings = append(warnings, LaunchConflict{
			Code:                "generator_pool_shortfall",
			Severity:            "warning",
			Source:              "generator_pool",
			ResourceID:          poolID,
			Message:             fmt.Sprintf("Generator pool %q has %d member%s but the run is sized for %d generators; it will run on the available members.", poolLabel, len(members), pluralSuffix(len(members)), recommended),
			RequiredGenerators:  recommended,
			AvailableGenerators: len(members),
		})
	}
	return nil, warnings
}

func poolReadinessConflict(poolID, message string) LaunchConflict {
	return LaunchConflict{
		Code:       "generator_pool_unavailable",
		Severity:   "blocking",
		Source:     "generator_pool",
		ResourceID: poolID,
		Message:    message,
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
	return launchWindowForTestAt(test, start, time.Now().UTC())
}

func launchWindowForTestAt(test *ManagedTest, start time.Time, now time.Time) LaunchWindow {
	start = start.UTC()
	now = now.UTC()
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
	pauseExtension := time.Duration(0)
	if test != nil {
		pauseExtension = test.CumulativePause
		if test.PausedAt != nil && !test.PausedAt.IsZero() {
			pauseExtension += max(time.Duration(0), now.Sub(*test.PausedAt))
		}
	}
	end := start.Add(startDelay + preBaseline + duration + pauseExtension + maxRampDown + postBaseline).UTC()
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
		if len(group.Stages) > 0 {
			continue
		}
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

func (tr *TestRunner) launchWindowConflictsLocked(test *ManagedTest, window LaunchWindow, now time.Time, excludeTestID string, excludeScheduleID string) []LaunchConflict {
	conflicts := make([]LaunchConflict, 0)
	conflicts = append(conflicts, tr.launchGovernanceWindowConflictsLocked(test, window)...)

	proposal := launchCapacityEvent{
		ID:                 "proposal",
		Source:             "proposal",
		Title:              test.Name,
		ProjectID:          normalizeProjectID(test.ProjectID),
		StartAt:            window.StartAt,
		EndAt:              window.EndAt,
		Generators:         recommendedGeneratorCountForTest(test),
		VUsers:             test.TotalVUsers,
		ApprovedQuotaBurst: hasApprovedExecutionPlanQuotaBurst(test),
	}
	if proposal.Generators < 1 && proposal.VUsers > 0 {
		proposal.Generators = 1
	}

	events := tr.launchCapacityEventsLocked(window, now, excludeTestID, excludeScheduleID)
	sampleTimes := launchSampleTimes(window, events, tr.settings.CapacityPlanning.Reservations, tr.boundTimeslotBoundariesInWindowLocked(window))
	ignoredImmediateConflicts := map[string]struct{}{}
	if excludeScheduleID == "" && !window.StartAt.After(now.Add(time.Second)) {
		for _, conflict := range tr.launchCapacityConflictsAtLocked(proposal, overlappingLaunchEvents(events, window.StartAt), window.StartAt) {
			ignoredImmediateConflicts[launchConflictProjectionKey(conflict)] = struct{}{}
		}
	}
	for _, sample := range sampleTimes {
		overlapping := overlappingLaunchEvents(events, sample)
		for _, conflict := range tr.launchCapacityConflictsAtLocked(proposal, overlapping, sample) {
			// Immediate launches use the authoritative current-capacity gate to
			// decide start-versus-queue. Suppress only an unchanged projection of
			// that baseline conflict; continue evaluating later checks so a new
			// reservation or availability change is still surfaced.
			if _, ignored := ignoredImmediateConflicts[launchConflictProjectionKey(conflict)]; ignored {
				continue
			}
			conflicts = append(conflicts, conflict)
			return conflicts
		}
	}
	return conflicts
}

func launchConflictProjectionKey(conflict LaunchConflict) string {
	return fmt.Sprintf("%s|%s|%s|%d|%d", conflict.Code, conflict.Source, conflict.ResourceID, conflict.RequiredGenerators, conflict.AvailableGenerators)
}

func (tr *TestRunner) launchCapacityEventsLocked(window LaunchWindow, now time.Time, excludeTestID string, excludeScheduleID string) []launchCapacityEvent {
	events := make([]launchCapacityEvent, 0)
	for id, test := range tr.tests {
		if test == nil || id == excludeTestID {
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
		// When evaluating a schedule's own materialization, exclude that schedule's
		// projected occurrences: its imminent next occurrence coincides with the
		// run being materialized and would otherwise double-count as a competing
		// event (a self-conflict).
		if excludeScheduleID != "" && schedule.ID == excludeScheduleID {
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
	end := launchWindowForTestAt(test, start, now).EndAt
	if launchTestStatusOccupiesCurrentCapacity(test.Status) && end.Before(now.Add(time.Minute)) {
		end = now.Add(time.Minute)
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
		totalVUsers = saturatingAddNonNegative(totalVUsers, script.VUsers)
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

func launchSampleTimes(window LaunchWindow, events []launchCapacityEvent, reservations []CapacityReservation, extraBoundaries []time.Time) []time.Time {
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
	// Evaluate at the exact instants a bound capacity timeslot opens or closes
	// inside the window so a short reservation that begins and ends between two
	// other sample points is not missed.
	for _, boundary := range extraBoundaries {
		if launchInstantInWindow(boundary.UTC(), window) {
			values = append(values, boundary.UTC())
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
	conflicts := tr.launchCapacityConflictsAtLocked(proposal, existing, sample)
	if len(conflicts) == 0 {
		return nil
	}
	conflict := conflicts[0]
	return &conflict
}

func (tr *TestRunner) launchCapacityConflictsAtLocked(proposal launchCapacityEvent, existing []launchCapacityEvent, sample time.Time) []LaunchConflict {
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
		totalGenerators = saturatingAddNonNegative(totalGenerators, event.Generators)
		existingGenerators = saturatingAddNonNegative(existingGenerators, event.Generators)
		globalTests = saturatingAddNonNegative(globalTests, 1)
		existingProjectID := normalizeProjectID(event.ProjectID)
		byProject[existingProjectID] = saturatingAddNonNegative(byProject[existingProjectID], event.Generators)
		if normalizeProjectID(event.ProjectID) == proposal.ProjectID {
			projectTests = saturatingAddNonNegative(projectTests, 1)
			projectVUsers = saturatingAddNonNegative(projectVUsers, event.VUsers)
		}
	}
	conflicts := make([]LaunchConflict, 0, 5)
	if tr.settings.General.MaxConcurrentTests > 0 && globalTests > tr.settings.General.MaxConcurrentTests {
		conflicts = append(conflicts, *launchConflictAt("global_concurrency_window", "controller", "", fmt.Sprintf("Window would exceed global max concurrent tests (%d).", tr.settings.General.MaxConcurrentTests), sample, totalGenerators, availability))
	}
	if maxConcurrent := tr.projectMaxConcurrentTests(proposal.ProjectID); maxConcurrent > 0 && projectTests > maxConcurrent {
		conflicts = append(conflicts, *launchConflictAt("project_concurrency_window", "project", proposal.ProjectID, fmt.Sprintf("Window would exceed project max concurrent tests (%d).", maxConcurrent), sample, totalGenerators, availability))
	}
	if maxVUsers := tr.projectMaxVUsers(proposal.ProjectID); maxVUsers > 0 && projectVUsers > maxVUsers && !proposal.ApprovedQuotaBurst {
		conflicts = append(conflicts, *launchConflictAt("project_vuser_window", "project", proposal.ProjectID, fmt.Sprintf("Window would exceed project VUser capacity (%d max VUsers).", maxVUsers), sample, totalGenerators, availability))
	}
	if existingGenerators > 0 && totalGenerators > availability {
		conflicts = append(conflicts, *launchConflictAt("generator_capacity_window", "controller", "", fmt.Sprintf("Window needs %d generators but only %d are available at %s.", totalGenerators, availability, sample.Format(time.RFC3339)), sample, totalGenerators, availability))
	}
	if conflict := tr.launchReservationConflictAtLocked(proposal, byProject, sample, availability); conflict != nil {
		conflicts = append(conflicts, *conflict)
	}
	return conflicts
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
		sharedAfter = saturatingAddNonNegative(sharedAfter, maxInt(0, active-projectReserved))
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
	boundaries, err := launchScheduleWindowBoundaries(schedule.AvailabilityWindows, schedule.Timezone, start, end)
	if err != nil {
		return false
	}
	for index := 0; index+1 < len(boundaries); index++ {
		if boundaries[index+1].After(boundaries[index]) && !launchScheduleWindowAllowedAt(schedule.AvailabilityWindows, schedule.Timezone, boundaries[index]) {
			return false
		}
	}
	return true
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
	boundaries, err := launchScheduleWindowBoundaries([]storage.ScenarioScheduleWindow{window}, fallbackTimezone, start, end)
	if err != nil {
		return false
	}
	for index := 0; index+1 < len(boundaries); index++ {
		if !boundaries[index+1].After(boundaries[index]) {
			continue
		}
		matches, matchErr := scheduleWindowContains(window, fallbackTimezone, boundaries[index])
		if matchErr == nil && matches {
			return true
		}
	}
	return false
}

// launchScheduleWindowBoundaries returns every instant at which the union of
// recurring minute-resolution schedule windows can change over [start, end).
// Evaluating each resulting half-open segment is exact, unlike fixed-interval
// sampling: it catches one-minute windows, disjoint availability gaps, overnight
// windows, and timezone offset changes.
func launchScheduleWindowBoundaries(windows []storage.ScenarioScheduleWindow, fallbackTimezone string, start time.Time, end time.Time) ([]time.Time, error) {
	start = start.UTC()
	end = end.UTC()
	if !end.After(start) {
		return []time.Time{start, end}, nil
	}
	values := []time.Time{start, end}
	locations := make(map[string]*time.Location)
	for _, window := range windows {
		timezone := firstNonEmpty(strings.TrimSpace(window.Timezone), strings.TrimSpace(fallbackTimezone), "UTC")
		location, ok := locations[timezone]
		if !ok {
			var err error
			location, err = cachedScheduleLocation(timezone)
			if err != nil {
				return nil, fmt.Errorf("invalid schedule window timezone %q: %w", timezone, err)
			}
			locations[timezone] = location
		}
		startMinute, err := parseScheduleWindowMinute(window.StartTime)
		if err != nil {
			return nil, err
		}
		endMinute, err := parseScheduleWindowMinute(window.EndTime)
		if err != nil {
			return nil, err
		}
		localStart := start.In(location)
		localEnd := end.In(location)
		day := time.Date(localStart.Year(), localStart.Month(), localStart.Day(), 12, 0, 0, 0, location).AddDate(0, 0, -2)
		lastDay := time.Date(localEnd.Year(), localEnd.Month(), localEnd.Day(), 12, 0, 0, 0, location).AddDate(0, 0, 2)
		for !day.After(lastDay) {
			for _, minute := range []int{0, startMinute, endMinute} {
				for _, instant := range launchWallClockInstants(location, day.Year(), day.Month(), day.Day(), minute) {
					if instant.After(start) && instant.Before(end) {
						values = append(values, instant)
					}
				}
			}
			day = day.AddDate(0, 0, 1)
		}
	}
	for _, location := range locations {
		values = append(values, launchTimezoneTransitions(location, start, end)...)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Before(values[j]) })
	out := values[:0]
	for _, value := range values {
		value = value.UTC()
		if len(out) == 0 || !value.Equal(out[len(out)-1]) {
			out = append(out, value)
		}
	}
	return out, nil
}

// launchWallClockInstants resolves both occurrences of an ambiguous wall clock
// during a fall-back transition and resolves none for a nonexistent spring-gap
// clock. Offset-transition boundaries below cover the latter discontinuity.
func launchWallClockInstants(location *time.Location, year int, month time.Month, day int, minuteOfDay int) []time.Time {
	hour := minuteOfDay / 60
	minute := minuteOfDay % 60
	wallUTC := time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
	probe := time.Date(year, month, day, hour, minute, 0, 0, location)
	offsets := make(map[int]struct{})
	for _, delta := range []time.Duration{-48 * time.Hour, -24 * time.Hour, 0, 24 * time.Hour, 48 * time.Hour} {
		_, offset := probe.Add(delta).Zone()
		offsets[offset] = struct{}{}
	}
	out := make([]time.Time, 0, len(offsets))
	for offset := range offsets {
		candidate := wallUTC.Add(-time.Duration(offset) * time.Second)
		local := candidate.In(location)
		if local.Year() == year && local.Month() == month && local.Day() == day && local.Hour() == hour && local.Minute() == minute {
			out = append(out, candidate.UTC())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

func launchTimezoneTransitions(location *time.Location, start time.Time, end time.Time) []time.Time {
	if location == nil || !end.After(start) {
		return nil
	}
	const step = 6 * time.Hour
	cursor := start.Add(-step).UTC()
	limit := end.Add(step).UTC()
	_, previousOffset := cursor.In(location).Zone()
	out := make([]time.Time, 0)
	for cursor.Before(limit) {
		next := cursor.Add(step)
		if next.After(limit) {
			next = limit
		}
		_, nextOffset := next.In(location).Zone()
		if nextOffset != previousOffset {
			lowUnix := cursor.Unix()
			highUnix := next.Unix()
			for highUnix-lowUnix > 1 {
				midUnix := lowUnix + (highUnix-lowUnix)/2
				_, offset := time.Unix(midUnix, 0).In(location).Zone()
				if offset == previousOffset {
					lowUnix = midUnix
				} else {
					highUnix = midUnix
				}
			}
			transition := time.Unix(highUnix, 0).UTC()
			if transition.After(start) && transition.Before(end) {
				out = append(out, transition)
			}
		}
		cursor = next
		previousOffset = nextOffset
	}
	return out
}

func launchIntervalsOverlap(leftStart time.Time, leftEnd time.Time, rightStart time.Time, rightEnd time.Time) bool {
	return leftStart.Before(rightEnd) && rightStart.Before(leftEnd)
}

func launchInstantInWindow(value time.Time, window LaunchWindow) bool {
	return !value.Before(window.StartAt) && value.Before(window.EndAt)
}

func (tr *TestRunner) launchStartWindowErrorLocked(test *ManagedTest, start time.Time, excludeTestID string, excludeScheduleID string) error {
	if test == nil {
		return nil
	}
	window := launchWindowForTest(test, start.UTC())
	conflicts := tr.launchWindowConflictsLocked(test, window, start.UTC(), excludeTestID, excludeScheduleID)
	if len(conflicts) == 0 {
		return nil
	}
	return fmt.Errorf("launch window conflict: %s", conflicts[0].Message)
}

func (tr *TestRunner) dueOneTimeScheduledTestIDsLocked(now time.Time, limit int) []string {
	type dueTest struct {
		id string
		at time.Time
	}
	due := make([]dueTest, 0)
	for id, test := range tr.tests {
		if test == nil || test.Status != "SCHEDULED" || test.ScheduledStartAt == nil {
			continue
		}
		if test.ScheduledStartAt.After(now) {
			continue
		}
		due = append(due, dueTest{id: id, at: test.ScheduledStartAt.UTC()})
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].at.Equal(due[j].at) {
			return due[i].id < due[j].id
		}
		return due[i].at.Before(due[j].at)
	})
	if limit > 0 && len(due) > limit {
		due = due[:limit]
	}
	ids := make([]string, len(due))
	for index := range due {
		ids[index] = due[index].id
	}
	return ids
}

func (tr *TestRunner) markScheduledStartError(id string, message string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	test, ok := tr.tests[id]
	if !ok || test == nil || test.Status != "SCHEDULED" {
		return
	}
	previousError := test.Error
	test.Error = message
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Error = previousError
		slog.Warn("failed to persist scheduled start error", "test_id", id, "error", err)
		return
	}
	tr.broadcastEvent("test_schedule_blocked", test)
}
