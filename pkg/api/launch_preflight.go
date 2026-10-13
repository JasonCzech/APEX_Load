package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
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
	OpenEnded       bool      `json:"open_ended,omitempty"`
	StartAt         time.Time `json:"start_at"`
	EndAt           time.Time `json:"end_at,omitempty"`
	DurationSeconds int       `json:"duration_seconds"`
}

// Open-ended capacity occupancy has no predicted finish or execution cutoff.
func (w LaunchWindow) MarshalJSON() ([]byte, error) {
	type plain LaunchWindow
	var end *time.Time
	if !w.OpenEnded {
		end = &w.EndAt
	}
	return json.Marshal(struct {
		plain
		End *time.Time `json:"end_at,omitempty"`
	}{plain(w), end})
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
	PodPlacements     []PodCapacityPreview `json:"pod_placements,omitempty"`
	Intent            string               `json:"intent,omitempty"`
	Decision          string               `json:"decision"`
	StartNowAllowed   bool                 `json:"start_now_allowed"`
	ScheduleAllowed   bool                 `json:"schedule_allowed"`
	QueueAllowed      bool                 `json:"queue_allowed"`
	QueueReason       string               `json:"queue_reason,omitempty"`
	CandidateWindow   LaunchWindow         `json:"candidate_window"`
	CapacityAdvisory  *CapacityAdvisory    `json:"capacity_advisory,omitempty"`
	BlockingConflicts []LaunchConflict     `json:"blocking_conflicts,omitempty"`
	Warnings          []LaunchConflict     `json:"warnings,omitempty"`
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
	// GroupProfileKnown distinguishes a segmented run whose groups have all
	// ended from a legacy event that has no per-group profile. Without it, an
	// empty active-group slice incorrectly falls back to the whole-run demand.
	GroupProfileKnown bool
	Groups            []launchGroupCapacity
}

type launchGroupCapacity struct {
	StartAt             time.Time
	EndAt               time.Time
	VUsers              int
	ReleaseAt           time.Time
	BeforeReleaseVUsers int
}

func launchCapacityAt(event launchCapacityEvent, sample time.Time) (int, int) {
	if len(event.Groups) == 0 && !event.GroupProfileKnown {
		return event.Generators, event.VUsers
	}
	vusers := 0
	for _, group := range event.Groups {
		if (group.StartAt.IsZero() || !sample.Before(group.StartAt)) && sample.Before(group.EndAt) {
			groupVUsers := group.VUsers
			if !group.ReleaseAt.IsZero() && sample.Before(group.ReleaseAt) {
				groupVUsers = maxInt(groupVUsers, group.BeforeReleaseVUsers)
			}
			vusers = saturatingAddNonNegative(vusers, groupVUsers)
		}
	}
	if vusers <= 0 || event.VUsers <= 0 {
		if sample.Before(event.EndAt) && event.Generators > 0 {
			return 1, 0
		}
		return 0, 0
	}
	generators := int(math.Ceil(float64(event.Generators) * float64(vusers) / float64(event.VUsers)))
	return maxInt(1, generators), vusers
}

func launchGroupCapacities(test *ManagedTest, start, now time.Time) []launchGroupCapacity {
	if test != nil && test.TerminationMode == "iterations" {
		groups := make([]launchGroupCapacity, 0, len(test.Groups))
		for _, g := range test.Groups {
			groups = append(groups, launchGroupCapacity{StartAt: start, EndAt: time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), VUsers: g.VUsers})
		}
		return groups
	}

	if test == nil {
		return nil
	}
	pause := test.CumulativePause
	if test.PausedAt != nil && !test.PausedAt.IsZero() {
		pause += max(time.Duration(0), now.Sub(*test.PausedAt))
	}
	preBaseline, _ := testexec.ResourceBaselineWindows(test.ResourceBaseline)
	offset := testexec.StartDelayWindow(test.StartDelay) + preBaseline + pause
	groups := make([]launchGroupCapacity, 0, len(test.Groups))
	for _, group := range test.Groups {
		if group.RuntimeState == "ended" {
			continue
		}
		duration, rampDown := groupCapacityDuration(group, test.Duration)
		reserved := group.VUsers
		if group.stageDriven && group.currentVUsers > reserved {
			reserved = group.currentVUsers
		}
		groupOffset := groupStartOffsetDuration(group)
		endOffset := minCapacityDuration(test.Duration, groupOffset+duration+rampDown)
		capacity := launchGroupCapacity{
			StartAt: start.Add(offset + groupOffset).UTC(),
			EndAt:   start.Add(offset + endOffset).UTC(),
			VUsers:  reserved,
		}
		if ramp, ok := test.VUserRamps[group.Name]; ok && ramp.StartVUsers > ramp.TargetVUsers {
			rampDuration := time.Duration(ramp.RampDurationSeconds) * time.Second
			if rampDuration <= 0 {
				rampDuration, _ = time.ParseDuration(ramp.RampDuration)
			}
			capacity.VUsers = ramp.TargetVUsers
			capacity.BeforeReleaseVUsers = ramp.StartVUsers
			capacity.ReleaseAt = ramp.StartedAt.Add(rampDuration).UTC()
		}
		groups = append(groups, capacity)
	}
	return groups
}

func groupCapacityDuration(group TestGroup, scenarioDuration time.Duration) (time.Duration, time.Duration) {
	duration := effectiveGroupDuration(group, scenarioDuration)
	rampDown, _ := time.ParseDuration(group.RampDown)
	if staged, ok := fixedLoadStageDuration(group.Stages); ok {
		duration = staged
		// A staged duration already includes authored ramp-down stages.
		rampDown = 0
	}
	offset := groupStartOffsetDuration(group)
	remaining := max(time.Duration(0), scenarioDuration-offset)
	return minCapacityDuration(duration, remaining), minCapacityDuration(rampDown, max(time.Duration(0), remaining-duration))
}

func fixedLoadStageDuration(stages []storage.LoadStage) (time.Duration, bool) {
	if len(stages) == 0 {
		return 0, false
	}
	var total time.Duration
	for _, stage := range stages {
		duration, err := time.ParseDuration(strings.TrimSpace(stage.Duration))
		if err != nil || duration <= 0 || duration > time.Duration(math.MaxInt64)-total {
			return 0, false
		}
		total += duration
	}
	return total, total > 0
}

func minCapacityDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

func managedGroupPeakVUsers(groups []TestGroup, scenarioDuration time.Duration) int {
	base := time.Unix(0, 0).UTC()
	windows := make([]launchGroupCapacity, 0, len(groups))
	for _, group := range groups {
		duration, rampDown := groupCapacityDuration(group, scenarioDuration)
		startOffset := groupStartOffsetDuration(group)
		endOffset := minCapacityDuration(scenarioDuration, startOffset+duration+rampDown)
		if endOffset <= startOffset {
			continue
		}
		reserved := group.VUsers
		if group.stageDriven && group.currentVUsers > reserved {
			reserved = group.currentVUsers
		}
		windows = append(windows, launchGroupCapacity{
			StartAt: base.Add(startOffset),
			EndAt:   base.Add(endOffset),
			VUsers:  reserved,
		})
	}
	return launchGroupPeakVUsers(windows)
}

func managedTestCapacityVUsers(test *ManagedTest) int {
	if test == nil {
		return 0
	}
	peak := trafficProfileCapacityVUsers(test.TrafficProfile, test.Groups, test.Duration, test.GoalConfig)
	if peak <= 0 {
		peak = test.TotalVUsers
	}
	return saturatingAddNonNegative(peak, recoverySpareVUsers(test))
}

func launchGroupPeakVUsers(groups []launchGroupCapacity) int {
	peak := 0
	samples := make([]time.Time, 0, len(groups))
	for _, group := range groups {
		samples = append(samples, group.StartAt)
	}
	for _, sample := range samples {
		total := 0
		for _, group := range groups {
			if !group.StartAt.IsZero() && sample.Before(group.StartAt) {
				continue
			}
			if !group.EndAt.IsZero() && !sample.Before(group.EndAt) {
				continue
			}
			groupVUsers := group.VUsers
			if !group.ReleaseAt.IsZero() && sample.Before(group.ReleaseAt) {
				groupVUsers = maxInt(groupVUsers, group.BeforeReleaseVUsers)
			}
			total = saturatingAddNonNegative(total, groupVUsers)
		}
		peak = maxInt(peak, total)
	}
	return peak
}

func launchEventPeakVUsers(groups []launchGroupCapacity, fallback int) int {
	if len(groups) == 0 {
		return fallback
	}
	return launchGroupPeakVUsers(groups)
}

// launchWindowEvidence is the durable state needed by a launch-window
// evaluation. It is loaded before taking tr.mu so a slow SQLite/PostgreSQL
// query cannot stall unrelated runner reads and lifecycle updates.
type launchWindowEvidence struct {
	window         LaunchWindow
	occurrenceKeys map[string]struct{}
	occurrenceErr  error
	boundTimeslots []*storage.CapacityReservation
	reservationErr error
	projectQuota   projectQuotaEvidence
}

func (tr *TestRunner) loadLaunchWindowEvidence(window LaunchWindow) launchWindowEvidence {
	window.StartAt = window.StartAt.UTC()
	window.EndAt = window.EndAt.UTC()
	evidence := launchWindowEvidence{
		window:         window,
		occurrenceKeys: make(map[string]struct{}),
	}
	if tr == nil {
		return evidence
	}
	if store := tr.scenarioStore; store != nil {
		var occurrences []*storage.ScenarioScheduleOccurrence
		var err error
		if windowStore, ok := store.(scenarioScheduleOccurrenceWindowStore); ok {
			// Projection starts one minute before the candidate window so it can
			// include the cron minute containing a non-minute-aligned start.
			occurrences, err = windowStore.ListScenarioScheduleOccurrencesByTimeRange("", window.StartAt.Add(-time.Minute), window.EndAt)
		} else {
			occurrences, err = store.ListAllScenarioScheduleOccurrences()
		}
		if err != nil {
			evidence.occurrenceErr = err
			slog.Warn("launch window: schedule-occurrence lookup failed", "error", err)
		} else {
			for _, occurrence := range occurrences {
				if occurrence != nil {
					evidence.occurrenceKeys[runCalendarOccurrenceKey(occurrence.ScheduleID, occurrence.PlannedAt)] = struct{}{}
				}
			}
		}
	}
	if store := tr.reservationStore; store != nil {
		evidence.boundTimeslots, evidence.reservationErr = store.ListCapacityReservationsByTimeRange("", window.StartAt, window.EndAt)
		if evidence.reservationErr != nil {
			slog.Warn("launch window: bound-timeslot lookup failed; failing closed", "error", evidence.reservationErr)
		}
	}
	return evidence
}

func (tr *TestRunner) loadLaunchWindowEvidenceForProject(window LaunchWindow, projectID, poolID string) launchWindowEvidence {
	evidence := tr.loadLaunchWindowEvidence(window)
	evidence.projectQuota = loadProjectQuotaEvidence(tr.projectStore, projectID, poolID)
	return evidence
}

// loadLaunchWindowEvidenceWithBoundTimeslot also pins the run's named
// reservation. An actual-time launch can intentionally fall outside a future
// planned slot; the window query alone would then omit that row and collapse a
// precise "no longer fits" result into the misleading "no longer available".
func (tr *TestRunner) loadLaunchWindowEvidenceWithBoundTimeslot(window LaunchWindow, timeslotID string) launchWindowEvidence {
	evidence := tr.loadLaunchWindowEvidence(window)
	timeslotID = strings.TrimSpace(timeslotID)
	if tr == nil || tr.reservationStore == nil || timeslotID == "" {
		return evidence
	}
	for _, reservation := range evidence.boundTimeslots {
		if reservation != nil && strings.TrimSpace(reservation.ID) == timeslotID {
			return evidence
		}
	}
	reservation, err := tr.reservationStore.GetCapacityReservation(timeslotID)
	if err != nil {
		// A successful range read followed by a named-row miss is authoritative:
		// the binding was deleted. Other lookup failures leave the evidence
		// incomplete and must be retried rather than reported as a deletion.
		if evidence.reservationErr == nil && !errors.Is(err, sql.ErrNoRows) {
			evidence.reservationErr = err
		}
		return evidence
	}
	if reservation != nil {
		evidence.boundTimeslots = append(evidence.boundTimeslots, reservation)
	}
	return evidence
}

func (tr *TestRunner) loadLaunchWindowEvidenceWithBoundTimeslotForProject(window LaunchWindow, timeslotID, projectID, poolID string) launchWindowEvidence {
	evidence := tr.loadLaunchWindowEvidenceWithBoundTimeslot(window, timeslotID)
	evidence.projectQuota = loadProjectQuotaEvidence(tr.projectStore, projectID, poolID)
	return evidence
}

// covers reports whether the durable query range contains every instant needed
// by a later in-memory evaluation. Callers snapshot the candidate under a read
// lock, load evidence without tr.mu, then verify coverage after taking the
// write lock. A concurrent duration/config edit therefore causes a retry/error
// instead of silently evaluating the expanded window with incomplete data.
func (evidence launchWindowEvidence) covers(window LaunchWindow) bool {
	if evidence.window.StartAt.IsZero() || evidence.window.EndAt.IsZero() {
		return false
	}
	return !window.StartAt.UTC().Before(evidence.window.StartAt) &&
		!window.EndAt.UTC().After(evidence.window.EndAt)
}

// startLaunchEvidenceRetryError separates unavailable/incoherent storage
// evidence from an authoritative admission rejection. Callers must retain and
// retry queued work for these errors; a successfully-read missing row or real
// capacity conflict remains a permanent gate result.
func startLaunchEvidenceRetryError(test *ManagedTest, start time.Time, evidence launchWindowEvidence) error {
	if test == nil {
		return nil
	}
	window := launchWindowForTest(test, start.UTC())
	if !evidence.covers(window) {
		return errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("launch evidence no longer covers the run window"))
	}
	if evidence.occurrenceErr != nil {
		return errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("load scheduled-run evidence: %w", evidence.occurrenceErr))
	}
	if evidence.reservationErr != nil {
		return errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("load capacity reservation evidence: %w", evidence.reservationErr))
	}
	if evidence.projectQuota.err != nil {
		if errors.Is(evidence.projectQuota.err, sql.ErrNoRows) {
			return fmt.Errorf("project %q no longer exists", evidence.projectQuota.projectID)
		}
		return errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("load project quota evidence: %w", evidence.projectQuota.err))
	}
	return nil
}

func boundTimeslotBoundariesFromEvidence(evidence launchWindowEvidence) []time.Time {
	if evidence.reservationErr != nil {
		return nil
	}
	out := make([]time.Time, 0, len(evidence.boundTimeslots)*2)
	for _, timeslot := range evidence.boundTimeslots {
		if !launchEvidenceTimeslotBound(timeslot) {
			continue
		}
		out = append(out, timeslot.StartAt.UTC(), timeslot.EndAt.UTC())
	}
	return out
}

func boundTimeslotsActiveAtFromEvidence(evidence launchWindowEvidence, at time.Time) ([]*storage.CapacityReservation, error) {
	if evidence.reservationErr != nil {
		return nil, evidence.reservationErr
	}
	out := make([]*storage.CapacityReservation, 0)
	for _, timeslot := range evidence.boundTimeslots {
		if !launchEvidenceTimeslotBound(timeslot) || at.Before(timeslot.StartAt) || !at.Before(timeslot.EndAt) {
			continue
		}
		out = append(out, timeslot)
	}
	return out, nil
}

func launchEvidenceTimeslotBound(timeslot *storage.CapacityReservation) bool {
	return timeslot != nil && timeslot.ApprovalState == "approved" && timeslot.BoundTestID != nil && strings.TrimSpace(*timeslot.BoundTestID) != ""
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
	preview, buildSnapshot, buildRevision, err := tr.buildManagedTestFromSnapshot(createReq, "launch-preflight", "DRAFT")
	if err != nil {
		return nil, err
	}
	window := launchWindowForTest(preview, start)
	blocking := make([]LaunchConflict, 0)
	warnings := make([]LaunchConflict, 0)

	// All static launch checks run against the immutable request snapshot. Java
	// approval verification can read the authoring-session store and a cold local
	// runtime check can launch a JVM health probe; neither is safe under the live
	// runner mutex. Generator placement uses the same snapshot's capacity rows and
	// cached inventory, which are fenced again below before the result is returned.
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
	if conflicts := importPromotionLaunchConflicts(preview.Groups); len(conflicts) > 0 {
		blocking = append(blocking, conflicts...)
	}
	if warning := tr.runContinuityLaunchWarning(preview); warning != nil {
		warnings = append(warnings, *warning)
	}

	// Durable occurrence/reservation reads can block on another transaction.
	// Keep them completely outside the global runner lock, then evaluate the
	// in-memory projection against the request-scoped snapshot below.
	evidence := tr.loadLaunchWindowEvidenceForProject(window, preview.ProjectID, testAssignedPoolID(preview))
	poolEvidence := tr.loadPoolReadinessEvidence(preview)
	// Evaluate short-TTL runtime and generator-health evidence last so a slow
	// durable query cannot consume most of its freshness window before the fence.
	if conflicts := buildSnapshot.javaVuserRunnerLaunchConflictsForManagedTestLocked(preview); len(conflicts) > 0 {
		blocking = append(blocking, conflicts...)
	}
	if conflicts := buildSnapshot.generatorPlacementLaunchConflictsLocked(preview); len(conflicts) > 0 {
		if buildSnapshot.settings.CapacityPlanning.PodReservationsEnabled {
			for _, conflict := range conflicts {
				conflict.Severity = "warning"
				warnings = append(warnings, conflict)
			}
		} else {
			blocking = append(blocking, conflicts...)
		}
	}

	tr.mu.RLock()
	if !tr.managedTestBuildFenceCurrentLocked(buildRevision) {
		tr.mu.RUnlock()
		return nil, fmt.Errorf("controller content changed while launch preflight was being compiled; retry")
	}

	if poolBlocking, poolWarnings := poolReadinessLaunchConflicts(preview, poolEvidence); len(poolBlocking) > 0 || len(poolWarnings) > 0 {
		blocking = append(blocking, poolBlocking...)
		warnings = append(warnings, poolWarnings...)
	}

	if conflicts := tr.launchWindowConflictsWithEvidenceLocked(preview, window, now, "", "", evidence); len(conflicts) > 0 {
		blocking = append(blocking, conflicts...)
	}

	currentBlocked := false
	currentReason := ""
	if !scheduledRequested {
		if blocked, reason := tr.evaluateQuotaBlockWithLaunchEvidenceLocked(preview, "", now, evidence); blocked {
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
	// When blocked, surface a recommended assignment using the same immutable
	// snapshot rather than extending the live lock over placement projection.
	var recommendation *AssignmentRecommendation
	if len(blocking) > 0 {
		recommendation = buildSnapshot.buildAssignmentRecommendation(preview)
	}

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

	tr.mu.RLock()
	podPlacements, _ := tr.previewPodPlacementLocked(preview)
	if warning := tr.nodeRecoveryCapacityWarningLocked(preview, podPlacements); warning != nil {
		warnings = append(warnings, *warning)
	}
	tr.mu.RUnlock()
	return &LaunchPreflightResponse{
		PodPlacements:         podPlacements,
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
		for _, g := range test.Groups {
			if javaHostSettings(g) != nil {
				return []groupPlacementConflict{{Group: g.Name, Message: "java_host_requires_distributed_backend"}}
			}
		}
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
			snapshot.PodCapacity = entry.PodCapacity
			snapshot.CPULimitCores = entry.CPULimitCores
			snapshot.MemoryLimitBytes = entry.MemoryLimitBytes
			snapshot.MaxVUsers = entry.MaxVUsers
			snapshot.RuntimeCapacities = cloneProtocolRuntimeCapacities(entry.RuntimeCapacities)
			snapshot.JavaHost = cloneJavaHostCapabilitySummary(entry.JavaHost)
			activeTargets[entry.Target] = snapshot
		}
	}
	configurePodLoads(tr.settings, activeTargets)
	projected := make(map[string]generatorLoadSnapshot, len(activeTargets))
	for target, snapshot := range activeTargets {
		snapshot.RuntimeCapacities = cloneProtocolRuntimeCapacities(snapshot.RuntimeCapacities)
		projected[target] = snapshot
	}
	tr.reserveUnreflectedJavaVuserCapacityLocked(test, projected, specs, inventoryCapturedAt)
	defaultLoad := loadSnapshotsForTargets(projected, defaultTargets)
	compatibility := newGeneratorCompatibilityIndex(specs, projected)
	conflicts := make([]groupPlacementConflict, 0)
	resourceGroups, err := runtimeReservationGroups(test, false)
	if err != nil {
		return []groupPlacementConflict{{Message: "runtime resource peak plan invalid"}}
	}
	for _, group := range resourceGroups {
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
			if err := validateJavaHostRun(test, group, shard.JavaHost); err != nil {
				conflicts = append(conflicts, groupPlacementConflict{Group: group.Name, Message: err.Error()})
				continue
			}
			if shard.Target == "" {
				continue
			}
			snapshot := projected[shard.Target]
			if shard.VUsers > 0 {
				snapshot.AvailableCapacity -= int32(shard.VUsers)
				snapshot.Stats.ActiveVUsers += int32(shard.VUsers)
			}
			consumeGroupRuntimeCapacity(&snapshot, group, shard.VUsers)
			consumePodGroupCapacity(&snapshot, group, shard.VUsers)
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

// poolReadinessEvidence is loaded before PreflightLaunch takes tr.mu. Fleet
// persistence can block behind another transaction and must not turn the global
// runner lock into a storage serialization boundary.
type poolReadinessEvidence struct {
	configured bool
	poolID     string
	pool       *storage.FleetPool
	members    []string
	err        error
}

func (tr *TestRunner) loadPoolReadinessEvidence(test *ManagedTest) poolReadinessEvidence {
	evidence := poolReadinessEvidence{}
	if test == nil || test.Assignment == nil || tr == nil || tr.fleetStore == nil {
		return evidence
	}
	poolID := strings.TrimSpace(test.Assignment.PoolID)
	if poolID == "" {
		return evidence
	}
	evidence.configured = true
	evidence.poolID = poolID
	evidence.pool, evidence.err = tr.fleetStore.GetFleetPool(poolID)
	if evidence.err != nil || evidence.pool == nil {
		return evidence
	}
	seen := make(map[string]struct{})
	add := func(raw string) {
		target := strings.TrimSpace(raw)
		if target == "" {
			return
		}
		seen[target] = struct{}{}
	}
	members, err := tr.fleetStore.ListFleetPoolMembers(poolID)
	if err != nil {
		evidence.err = err
		return evidence
	}
	for _, member := range members {
		if member != nil {
			add(member.Target)
		}
	}
	metadata, err := tr.fleetStore.ListGeneratorMetadata(0, 0)
	if err != nil {
		evidence.err = err
		return evidence
	}
	for _, item := range metadata {
		if item != nil && item.FleetPoolID != nil && strings.TrimSpace(*item.FleetPoolID) == poolID {
			add(item.Target)
		}
	}
	for target := range seen {
		evidence.members = append(evidence.members, target)
	}
	sort.Strings(evidence.members)
	return evidence
}

// poolReadinessLaunchConflicts makes fleet-pool state visible to preflight:
// a pool-scoped assignment whose pool is missing, suspended, or empty blocks the
// launch (rather than failing lazily at Run time), and a pool with fewer live
// members than the run is sized for yields a non-blocking shortfall warning.
// Tests without a pool-scoped assignment are unaffected. Per-pool project quotas
// are not enforced here — for an immediate launch they are checked by
// evaluateQuotaBlockLocked below; the windowed scheduled-run projection does not
// re-evaluate them (matching its pre-existing blindness to pool-scoped
// reservations), so a scheduled pool run over its pool quota is caught and queued
// by the authoritative start-time gate rather than blocked in preflight.
func poolReadinessLaunchConflicts(test *ManagedTest, evidence poolReadinessEvidence) (blocking, warnings []LaunchConflict) {
	if test == nil || test.Assignment == nil || !evidence.configured {
		return nil, nil
	}
	poolID := strings.TrimSpace(test.Assignment.PoolID)
	if poolID == "" {
		return nil, nil
	}
	if evidence.poolID != poolID || evidence.err != nil || evidence.pool == nil {
		return []LaunchConflict{poolReadinessConflict(poolID, fmt.Sprintf("Assigned generator pool %q was not found.", poolID))}, nil
	}
	pool := evidence.pool
	poolLabel := firstNonEmpty(pool.Name, poolID)
	if status := strings.ToLower(strings.TrimSpace(pool.Status)); status != "" && status != "active" {
		return []LaunchConflict{poolReadinessConflict(poolID, fmt.Sprintf("Generator pool %q is %s; activate it or pick another pool before launching.", poolLabel, status))}, nil
	}
	members := evidence.members
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
	if test != nil && test.TerminationMode == "iterations" {
		return LaunchWindow{OpenEnded: true, StartAt: start, EndAt: time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)}
	}
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

func (tr *TestRunner) launchWindowConflictsWithEvidenceLocked(test *ManagedTest, window LaunchWindow, now time.Time, excludeTestID string, excludeScheduleID string, evidence launchWindowEvidence) []LaunchConflict {
	if !evidence.covers(window) {
		return []LaunchConflict{{
			Code:     "launch_evidence_stale",
			Severity: "blocking",
			Source:   "controller",
			Message:  "The run window changed while launch evidence was being loaded; retry the operation.",
		}}
	}
	if evidence.occurrenceErr != nil {
		return []LaunchConflict{{
			Code:     "launch_occurrence_evidence_unavailable",
			Severity: "blocking",
			Source:   "scheduler",
			Message:  "Scheduled-run evidence is temporarily unavailable; retry the launch after storage recovers.",
		}}
	}
	conflicts := make([]LaunchConflict, 0)
	conflicts = append(conflicts, tr.launchGovernanceWindowConflictsLocked(test, window)...)
	conflicts = append(conflicts, launchVUserCapConflicts(test, evidence)...)

	proposalGroups := launchGroupCapacities(test, window.StartAt, now)
	proposal := launchCapacityEvent{
		ID:                 "proposal",
		Source:             "proposal",
		Title:              test.Name,
		ProjectID:          normalizeProjectID(test.ProjectID),
		StartAt:            window.StartAt,
		EndAt:              window.EndAt,
		Generators:         recommendedGeneratorCountForTest(test),
		VUsers:             launchEventPeakVUsers(proposalGroups, test.TotalVUsers),
		ApprovedQuotaBurst: hasApprovedExecutionPlanQuotaBurst(test),
		GroupProfileKnown:  true,
		Groups:             proposalGroups,
	}
	if proposal.Generators < 1 && proposal.VUsers > 0 {
		proposal.Generators = 1
	}

	events := tr.launchCapacityEventsWithOccurrencesLocked(window, now, excludeTestID, excludeScheduleID, evidence.occurrenceKeys)
	sampleTimes := launchSampleTimes(window, events, tr.settings.CapacityPlanning.Reservations, boundTimeslotBoundariesFromEvidence(evidence))
	ignoredImmediateConflicts := map[string]struct{}{}
	if excludeScheduleID == "" && !window.StartAt.After(now.Add(time.Second)) {
		for _, conflict := range tr.launchCapacityConflictsAtWithEvidenceLocked(proposal, overlappingLaunchEvents(events, window.StartAt), window.StartAt, evidence) {
			ignoredImmediateConflicts[launchConflictProjectionKey(conflict)] = struct{}{}
		}
	}
	for _, sample := range sampleTimes {
		overlapping := overlappingLaunchEvents(events, sample)
		for _, conflict := range tr.launchCapacityConflictsAtWithEvidenceLocked(proposal, overlapping, sample, evidence) {
			// Immediate launches use the authoritative current-capacity gate to
			// decide start-versus-queue. Suppress only an unchanged projection of
			// that baseline conflict; continue evaluating later checks so a new
			// reservation or availability change is still surfaced.
			if _, ignored := ignoredImmediateConflicts[launchConflictProjectionKey(conflict)]; ignored && conflict.Code != "project_quota_evidence_unavailable" {
				continue
			}
			conflicts = append(conflicts, conflict)
			return conflicts
		}
	}
	return conflicts
}

// launchVUserCapConflicts refuses a run that is larger on its own than the
// project's or its pool's VUser cap. Such a run can never start, so offering
// to queue or schedule it would only leave it waiting forever. A plan run
// approved to burst the project quota is exempt from the project cap (the
// approval doesn't cover pool quotas), and a run that has already started is
// exempt from both: changes to it (including reductions after a cap was
// lowered) are judged by the projected-capacity checks instead.
func launchVUserCapConflicts(test *ManagedTest, evidence launchWindowEvidence) []LaunchConflict {
	if test == nil || launchHasStarted(test) {
		return nil
	}
	quota, ok := evidence.projectQuota.quotaFor(test)
	if !ok {
		// Missing quota evidence is reported by the capacity checks.
		return nil
	}
	need := managedTestCapacityVUsers(test)
	conflicts := make([]LaunchConflict, 0, 2)
	if quota.maxVUsers > 0 && need > quota.maxVUsers && !hasApprovedExecutionPlanQuotaBurst(test) {
		conflicts = append(conflicts, LaunchConflict{
			Code:       "exceeds_project_vuser_cap",
			Severity:   "blocking",
			Source:     "project",
			ResourceID: normalizeProjectID(test.ProjectID),
			Message:    fmt.Sprintf("This run needs %d VUsers at its peak, but the project allows %d at a time, so it can never start. Lower its VUsers, or ask a project administrator to raise the project's VUser limit or to approve it as an execution plan run.", need, quota.maxVUsers),
		})
	}
	if poolID := testAssignedPoolID(test); poolID != "" && quota.hasPoolQuota && quota.poolQuota.MaxVUsers > 0 && need > quota.poolQuota.MaxVUsers {
		conflicts = append(conflicts, LaunchConflict{
			Code:       "exceeds_pool_vuser_cap",
			Severity:   "blocking",
			Source:     "pool",
			ResourceID: poolID,
			Message:    fmt.Sprintf("This run needs %d VUsers at its peak, but the project may use at most %d on pool %q, so it can never start there. Lower its VUsers, choose another pool, or ask an administrator to raise the pool quota.", need, quota.poolQuota.MaxVUsers, poolID),
		})
	}
	return conflicts
}

func launchHasStarted(test *ManagedTest) bool {
	switch test.Status {
	case "SHAKEOUT", "STARTING", "INITIALIZING", "RUNNING", "PAUSED", "STOPPING":
		return true
	}
	return test.StartedAt != nil && !test.StartedAt.IsZero()
}

// overCapacityLimitReason says why a run larger than a VUser cap on its own
// waits, so a queued run never looks as if it will start once others finish.
func overCapacityLimitReason(need, limit int, scope string) string {
	return fmt.Sprintf("waiting for the %s VUser limit to be raised: this run needs %d VUsers and the limit is %d", scope, need, limit)
}

func launchConflictProjectionKey(conflict LaunchConflict) string {
	return fmt.Sprintf("%s|%s|%s|%d|%d", conflict.Code, conflict.Source, conflict.ResourceID, conflict.RequiredGenerators, conflict.AvailableGenerators)
}

func (tr *TestRunner) launchCapacityEventsWithOccurrencesLocked(window LaunchWindow, now time.Time, excludeTestID string, excludeScheduleID string, occurrences map[string]struct{}) []launchCapacityEvent {
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
	groups := launchGroupCapacities(test, start, now)
	return launchCapacityEvent{
		ID:                test.ID,
		Source:            "test",
		Title:             firstNonEmpty(test.Name, "Run"),
		ProjectID:         normalizeProjectID(test.ProjectID),
		StartAt:           start.UTC(),
		EndAt:             end.UTC(),
		Generators:        generators,
		VUsers:            launchEventPeakVUsers(groups, test.TotalVUsers),
		GroupProfileKnown: true,
		Groups:            groups,
	}, true
}

func (tr *TestRunner) launchCapacityEventForScenarioLocked(schedule *storage.ScenarioSchedule, scenario *StoredScenario, plannedAt time.Time) launchCapacityEvent {
	groups := make([]TestGroup, 0, len(scenario.Scripts))
	totalVUsers := 0
	for _, script := range scenario.Scripts {
		totalVUsers = saturatingAddNonNegative(totalVUsers, script.VUsers)
		groups = append(groups, TestGroup{
			Name:                         script.Name,
			ScriptID:                     script.ScriptID,
			ScriptName:                   firstNonEmpty(script.ScriptName, script.Name),
			Protocol:                     script.Protocol,
			VUsers:                       script.VUsers,
			RampUp:                       script.RampUp,
			RampDown:                     script.RampDown,
			StartOffset:                  script.StartOffset,
			Stages:                       append([]storage.LoadStage(nil), script.Stages...),
			Iterations:                   script.Iterations,
			Initialization:               script.Initialization,
			TransactionResponseTimeSLAMs: cloneFloat64Ptr(script.TransactionResponseTimeSLAMs),
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
	capacityGroups := launchGroupCapacities(testLike, plannedAt, plannedAt)
	return launchCapacityEvent{
		ID:                schedule.ID,
		Source:            "cron",
		Title:             testLike.Name,
		ProjectID:         testLike.ProjectID,
		StartAt:           window.StartAt,
		EndAt:             window.EndAt,
		Generators:        generators,
		VUsers:            launchEventPeakVUsers(capacityGroups, totalVUsers),
		GroupProfileKnown: true,
		Groups:            capacityGroups,
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
		for _, group := range event.Groups {
			if !group.StartAt.IsZero() && launchInstantInWindow(group.StartAt, window) {
				values = append(values, group.StartAt)
			}
			if !group.ReleaseAt.IsZero() && launchInstantInWindow(group.ReleaseAt, window) {
				values = append(values, group.ReleaseAt)
			}
			if launchInstantInWindow(group.EndAt, window) {
				values = append(values, group.EndAt)
			}
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
	return tr.launchCapacityConflictsAtWithEvidenceLocked(proposal, existing, sample, tr.loadLaunchWindowEvidenceForProject(LaunchWindow{StartAt: sample, EndAt: sample.Add(time.Nanosecond)}, proposal.ProjectID, ""))
}

func (tr *TestRunner) launchCapacityConflictsAtWithEvidenceLocked(proposal launchCapacityEvent, existing []launchCapacityEvent, sample time.Time, evidence launchWindowEvidence) []LaunchConflict {
	availability := resolveCapacityAvailabilityAt(tr.settings, sample).EffectiveAvailableGenerators
	if availability < 1 {
		availability = tr.settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators
	}
	proposalGenerators, proposalVUsers := launchCapacityAt(proposal, sample)
	totalGenerators := proposalGenerators
	existingGenerators := 0
	globalTests := 1
	projectTests := 1
	projectVUsers := proposalVUsers
	byProject := map[string]int{proposal.ProjectID: proposalGenerators}
	for _, event := range existing {
		eventGenerators, eventVUsers := launchCapacityAt(event, sample)
		totalGenerators = saturatingAddNonNegative(totalGenerators, eventGenerators)
		existingGenerators = saturatingAddNonNegative(existingGenerators, eventGenerators)
		globalTests = saturatingAddNonNegative(globalTests, 1)
		existingProjectID := normalizeProjectID(event.ProjectID)
		byProject[existingProjectID] = saturatingAddNonNegative(byProject[existingProjectID], eventGenerators)
		if normalizeProjectID(event.ProjectID) == proposal.ProjectID {
			projectTests = saturatingAddNonNegative(projectTests, 1)
			projectVUsers = saturatingAddNonNegative(projectVUsers, eventVUsers)
		}
	}
	conflicts := make([]LaunchConflict, 0, 5)
	if tr.settings.General.MaxConcurrentTests > 0 && globalTests > tr.settings.General.MaxConcurrentTests {
		conflicts = append(conflicts, *launchConflictAt("global_concurrency_window", "controller", "", fmt.Sprintf("Window would exceed global max concurrent tests (%d).", tr.settings.General.MaxConcurrentTests), sample, totalGenerators, availability))
	}
	projectQuota, quotaOK := evidence.projectQuota.quotaForProject(proposal.ProjectID)
	if !quotaOK {
		conflicts = append(conflicts, *launchConflictAt("project_quota_evidence_unavailable", "project", proposal.ProjectID, "Project quota evidence changed while the launch window was evaluated; retry.", sample, totalGenerators, availability))
	}
	if maxConcurrent := projectQuota.maxConcurrentTests; quotaOK && maxConcurrent > 0 && projectTests > maxConcurrent {
		conflicts = append(conflicts, *launchConflictAt("project_concurrency_window", "project", proposal.ProjectID, fmt.Sprintf("Window would exceed project max concurrent tests (%d).", maxConcurrent), sample, totalGenerators, availability))
	}
	if maxVUsers := projectQuota.maxVUsers; quotaOK && maxVUsers > 0 && projectVUsers > maxVUsers && !proposal.ApprovedQuotaBurst {
		conflicts = append(conflicts, *launchConflictAt("project_vuser_window", "project", proposal.ProjectID, fmt.Sprintf("Window would exceed project VUser capacity (%d max VUsers).", maxVUsers), sample, totalGenerators, availability))
	}
	if existingGenerators > 0 && totalGenerators > availability {
		conflicts = append(conflicts, *launchConflictAt("generator_capacity_window", "controller", "", fmt.Sprintf("Window needs %d generators but only %d are available at %s.", totalGenerators, availability, sample.Format(time.RFC3339)), sample, totalGenerators, availability))
	}
	if conflict := tr.launchReservationConflictAtWithEvidenceLocked(proposal, byProject, sample, availability, evidence); conflict != nil {
		conflicts = append(conflicts, *conflict)
	}
	return conflicts
}

func (tr *TestRunner) launchReservationConflictAtWithEvidenceLocked(proposal launchCapacityEvent, byProject map[string]int, sample time.Time, availability int, evidence launchWindowEvidence) *LaunchConflict {
	activeSlots, err := boundTimeslotsActiveAtFromEvidence(evidence, sample)
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
	evidence := tr.loadLaunchWindowEvidenceForProject(window, test.ProjectID, testAssignedPoolID(test))
	return tr.launchStartWindowErrorWithEvidenceLocked(test, start, excludeTestID, excludeScheduleID, evidence)
}

func (tr *TestRunner) launchStartWindowErrorWithEvidenceLocked(test *ManagedTest, start time.Time, excludeTestID string, excludeScheduleID string, evidence launchWindowEvidence) error {
	if test == nil {
		return nil
	}
	if err := startLaunchEvidenceRetryError(test, start, evidence); err != nil {
		return err
	}
	window := launchWindowForTest(test, start.UTC())
	conflicts := tr.launchWindowConflictsWithEvidenceLocked(test, window, start.UTC(), excludeTestID, excludeScheduleID, evidence)
	if len(conflicts) == 0 {
		return nil
	}
	return &LaunchWindowConflictError{Conflicts: conflicts}
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
	tr.broadcastEvent("test_schedule_blocked", cloneManagedTestLifecycleEvent(test))
}
