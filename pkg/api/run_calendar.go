package api

import (
	"container/heap"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	runCalendarFutureHorizon       = 366 * 24 * time.Hour
	runCalendarDefaultWindow       = 30 * 24 * time.Hour
	runCalendarMaxEvents           = 2000
	runCalendarMaxProjectedPerCron = 1000
	// calendarOccurrenceLookbackFloor is the minimum amount by which the
	// occurrence fetch reaches before the requested window's start, so an
	// occurrence that began earlier but runs into the window (e.g. an overnight
	// soak) is still returned and overlap-filtered. Widened further when a
	// scenario's configured duration exceeds it.
	calendarOccurrenceLookbackFloor = 25 * time.Hour
)

type RunCalendarResponse struct {
	GeneratedAt  time.Time            `json:"generated_at"`
	Range        RunCalendarRange     `json:"range"`
	Events       []RunCalendarEvent   `json:"events"`
	Count        int                  `json:"count"`
	StatusCounts map[string]int       `json:"status_counts,omitempty"`
	SourceCounts map[string]int       `json:"source_counts,omitempty"`
	Truncated    bool                 `json:"truncated,omitempty"`
	Capacity     *RunCalendarCapacity `json:"capacity,omitempty"`
}

// RunCalendarCapacity is an optional per-day capacity overlay (requested with
// ?capacity=true) so operators can see reservation pressure behind scheduled
// runs without a second request.
type RunCalendarCapacity struct {
	ProjectID string                   `json:"project_id,omitempty"`
	Days      []RunCalendarCapacityDay `json:"days"`
}

type RunCalendarCapacityDay struct {
	Date                   string  `json:"date"` // YYYY-MM-DD (UTC)
	ConfiguredGenerators   int     `json:"configured_generators"`
	AvailableGenerators    int     `json:"available_generators"`
	PeakReservedGenerators int     `json:"peak_reserved_generators"`
	ReservationCount       int     `json:"reservation_count"`
	PressurePct            float64 `json:"pressure_pct"`
	Pressure               string  `json:"pressure"` // clear | tight | over
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
	PlanID           string              `json:"plan_id,omitempty"`
	PlanName         string              `json:"plan_name,omitempty"`
	PlanRunID        string              `json:"plan_run_id,omitempty"`
	ApplicationID    string              `json:"application_id,omitempty"`
	ReleaseID        string              `json:"release_id,omitempty"`
	ProjectID        string              `json:"project_id,omitempty"`
	Workload         RunCalendarWorkload `json:"workload"`
	User             RunCalendarUser     `json:"user"`
	Topology         RunCalendarTopology `json:"topology"`
}

type runCalendarEventMaxHeap []RunCalendarEvent

func (events runCalendarEventMaxHeap) Len() int { return len(events) }
func (events runCalendarEventMaxHeap) Less(i, j int) bool {
	return runCalendarEventLess(events[j], events[i])
}
func (events runCalendarEventMaxHeap) Swap(i, j int) { events[i], events[j] = events[j], events[i] }
func (events *runCalendarEventMaxHeap) Push(value any) {
	*events = append(*events, value.(RunCalendarEvent))
}
func (events *runCalendarEventMaxHeap) Pop() any {
	old := *events
	last := old[len(old)-1]
	*events = old[:len(old)-1]
	return last
}

type runCalendarTopK struct {
	limit     int
	events    runCalendarEventMaxHeap
	truncated bool
}

func (collector *runCalendarTopK) add(event RunCalendarEvent) {
	if collector.limit <= 0 {
		collector.truncated = true
		return
	}
	if len(collector.events) < collector.limit {
		heap.Push(&collector.events, event)
		return
	}
	collector.truncated = true
	if runCalendarEventLess(event, collector.events[0]) {
		heap.Pop(&collector.events)
		heap.Push(&collector.events, event)
	}
}

func runCalendarEventLess(left, right RunCalendarEvent) bool {
	if !left.StartAt.Equal(right.StartAt) {
		return left.StartAt.Before(right.StartAt)
	}
	if left.Title != right.Title {
		return left.Title < right.Title
	}
	if left.ID != right.ID {
		return left.ID < right.ID
	}
	return left.Source < right.Source
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
	Tests           []*ManagedTest
	Schedules       []*storage.ScenarioSchedule
	Occurrences     []*storage.ScenarioScheduleOccurrence
	Scenarios       map[string]runCalendarScenarioSummary
	PlanSchedules   []*storage.ExecutionPlanSchedule
	PlanOccurrences []*storage.ExecutionPlanScheduleOccurrence
	Plans           map[string]*storage.ExecutionPlan
}

func (s *Server) handleGetRunCalendar(w http.ResponseWriter, r *http.Request) {
	schedules := s.schedulingDomainService()
	now := time.Now().UTC()
	requestedFrom, requestedTo, ok := parseRunCalendarRange(w, r, now)
	if !ok {
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	projectID = authorizedProjectOr(r, projectID)
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	settings := schedules.ArchiveRetentionSettings()
	from, to, calendarRange := clampRunCalendarRange(requestedFrom, requestedTo, now, settings)
	inventory := s.systemInventory(r.Context())
	var response RunCalendarResponse
	response = schedules.BuildRunCalendar(from, to, projectID, now, inventory, calendarRange, limit)
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("capacity")), "true") {
		response.Capacity = schedules.BuildRunCalendarCapacity(from, to, projectID, strings.TrimSpace(r.URL.Query().Get("tz")))
	}
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
		retention, ok := checkedDuration(retentionDays, 24*time.Hour)
		if !ok || retention <= 0 {
			retention, _ = checkedDuration(defaultSystemSettings(0).ArchiveRetention.DefaultRetentionDays, 24*time.Hour)
		}
		historyFloor := now.Add(-retention).UTC()
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

func (tr *TestRunner) BuildRunCalendar(from time.Time, to time.Time, projectID string, now time.Time, inventory SystemInventoryResponse, calendarRange RunCalendarRange, limit int) RunCalendarResponse {
	maxEvents := runCalendarMaxEvents
	if limit > 0 && limit < maxEvents {
		maxEvents = limit
	}
	snapshot := tr.runCalendarSnapshot(projectID, from, to)
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
	collector := runCalendarTopK{limit: maxEvents, events: make(runCalendarEventMaxHeap, 0, maxEvents)}
	truncated := false

	appendEvent := func(event RunCalendarEvent) {
		if !runCalendarEventOverlaps(event.StartAt, event.EndAt, from, to) {
			return
		}
		collector.add(event)
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

	planRunsWithTests := map[string]struct{}{}
	for _, test := range snapshot.Tests {
		if test == nil {
			continue
		}
		if _, scheduled := scheduledTestIDs[test.ID]; scheduled {
			continue
		}
		if test.ExecutionPlan != nil {
			planRunsWithTests[test.ExecutionPlan.PlanRunID] = struct{}{}
		}
		appendEvent(runCalendarEventFromTest(test, snapshot.Scenarios, inventory, tr.manifestForCalendarTest(test), now))
	}

	for _, schedule := range snapshot.Schedules {
		if schedule == nil || !schedule.Enabled {
			continue
		}
		// Blocked/approval-pending schedules are still projected (tagged
		// "blocked" by projectRunCalendarSchedule) so operators can see runs that
		// will not fire as-is and act early, instead of the run silently
		// disappearing from the calendar.
		projected := projectRunCalendarSchedule(schedule, from, to, now, occurrenceByScheduleAt, snapshot.Scenarios)
		for _, event := range projected {
			appendEvent(event)
		}
		if len(projected) >= runCalendarMaxProjectedPerCron {
			truncated = true
		}
	}

	// Execution-plan schedules: materialized occurrences first, then projected
	// future cron runs (deduped against those occurrences), mirroring the
	// scenario-schedule path above.
	planSchedulesByID := map[string]*storage.ExecutionPlanSchedule{}
	for _, schedule := range snapshot.PlanSchedules {
		if schedule != nil {
			planSchedulesByID[schedule.ID] = schedule
		}
	}
	planOccurrenceByScheduleAt := map[string]struct{}{}
	for _, occurrence := range snapshot.PlanOccurrences {
		if occurrence == nil {
			continue
		}
		planOccurrenceByScheduleAt[runCalendarOccurrenceKey(occurrence.ScheduleID, occurrence.PlannedAt)] = struct{}{}
		// A started plan run is shown by the runs it started, each linked to the
		// plan; its occurrence would count the same load twice.
		if _, shown := planRunsWithTests[strings.TrimSpace(occurrence.RunID)]; shown && strings.TrimSpace(occurrence.RunID) != "" {
			continue
		}
		appendEvent(runCalendarEventFromPlanOccurrence(occurrence, planSchedulesByID[occurrence.ScheduleID], snapshot.Plans[occurrence.PlanID], snapshot.Scenarios))
	}
	for _, schedule := range snapshot.PlanSchedules {
		if schedule == nil || !schedule.Enabled {
			continue
		}
		projected := projectRunCalendarPlanSchedule(schedule, snapshot.Plans[schedule.PlanID], from, to, now, planOccurrenceByScheduleAt, snapshot.Scenarios)
		for _, event := range projected {
			appendEvent(event)
		}
		if len(projected) >= runCalendarMaxProjectedPerCron {
			truncated = true
		}
	}

	events := []RunCalendarEvent(collector.events)
	sort.Slice(events, func(i, j int) bool { return runCalendarEventLess(events[i], events[j]) })
	truncated = truncated || collector.truncated
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

// buildRunCalendarCapacity computes a per-day capacity overlay across [from,to):
// peak reserved generators (settings reservations + approved bound timeslots),
// configured/available pool size, and a clear/tight/over pressure classification.
// Days are bucketed in `tz` (the viewer's timezone, defaulting to UTC) so the
// returned date keys line up with the dashboard's local-day calendar cells.
// Read-only; reuses the same reservation math as admission.
func (tr *TestRunner) buildRunCalendarCapacity(from, to time.Time, projectID string, tz string) *RunCalendarCapacity {
	settings := tr.GetSystemSettings()
	location := scheduleEvalLocation(tz)
	var boundSlots []*storage.CapacityReservation
	if tr.reservationStore != nil {
		if slots, err := tr.reservationStore.ListCapacityReservationsByTimeRange(projectID, from, to); err == nil {
			for _, slot := range slots {
				if slot != nil && slot.ApprovalState == "approved" && slot.BoundTestID != nil {
					boundSlots = append(boundSlots, slot)
				}
			}
		}
	}
	reservations := normalizeCapacityReservations(settings.CapacityPlanning.Reservations)
	days := make([]RunCalendarCapacityDay, 0)
	const maxDays = 400
	localFrom := from.In(location)
	dayStart := time.Date(localFrom.Year(), localFrom.Month(), localFrom.Day(), 0, 0, 0, 0, location)
	for d := 0; dayStart.Before(to) && d < maxDays; d++ {
		dayEnd := dayStart.AddDate(0, 0, 1)
		// Evaluate reserved capacity at the day start plus every reservation/timeslot
		// edge inside the day, so a reservation that opens mid-day is not missed.
		instants := []time.Time{dayStart}
		reservationCount := 0
		for _, res := range reservations {
			// Pool-scoped reservations are accounted against a separate pool envelope
			// and are excluded from this global overlay's reserved-generator/pressure
			// figures; skip them here too so reservationCount can't disagree with them.
			if strings.TrimSpace(res.PoolID) != "" {
				continue
			}
			if projectID != "" && normalizeProjectID(res.ProjectID) != projectID {
				continue
			}
			if res.ReservedGenerators <= 0 {
				continue
			}
			if !reservationWindowOverlapsDay(res.StartsAt, res.EndsAt, dayStart, dayEnd) {
				continue
			}
			reservationCount++
			if res.StartsAt != nil && !res.StartsAt.Before(dayStart) && res.StartsAt.Before(dayEnd) {
				instants = append(instants, res.StartsAt.UTC())
			}
		}
		dayBoundSlots := make([]*storage.CapacityReservation, 0, len(boundSlots))
		for _, ts := range boundSlots {
			// Pool-scoped bound timeslots likewise sit in a pool envelope, not the
			// global one this overlay renders; exclude them from the count too.
			if timeslotPoolID(ts) != "" {
				continue
			}
			if timeslotReservedGenerators(settings, ts) <= 0 {
				continue
			}
			if ts.StartAt.Before(dayEnd) && ts.EndAt.After(dayStart) {
				dayBoundSlots = append(dayBoundSlots, ts)
				reservationCount++
				if !ts.StartAt.Before(dayStart) && ts.StartAt.Before(dayEnd) {
					instants = append(instants, ts.StartAt.UTC())
				}
			}
		}
		peakReserved := 0
		for _, at := range instants {
			active := make([]*storage.CapacityReservation, 0, len(dayBoundSlots))
			for _, ts := range dayBoundSlots {
				if !at.Before(ts.StartAt) && at.Before(ts.EndAt) {
					active = append(active, ts)
				}
			}
			total, proj := reservationTotalsWithTimeslots(settings, projectID, at, active)
			reserved := total
			if projectID != "" {
				reserved = proj
			}
			if reserved > peakReserved {
				peakReserved = reserved
			}
		}
		snapshot := resolveCapacityAvailabilityForProjectAt(settings, projectID, dayStart)
		denom := snapshot.ConfiguredAvailableGenerators
		if denom <= 0 {
			denom = snapshot.EffectiveAvailableGenerators
		}
		pressurePct := 0.0
		if denom > 0 {
			pressurePct = float64(peakReserved) / float64(denom)
		} else if peakReserved > 0 {
			pressurePct = 1.0
		}
		pressure := "clear"
		if peakReserved > 0 && denom <= 0 {
			pressure = "over"
		} else if pressurePct > 1.0 {
			pressure = "over"
		} else if pressurePct >= 0.85 {
			pressure = "tight"
		}
		days = append(days, RunCalendarCapacityDay{
			Date:                   dayStart.Format("2006-01-02"),
			ConfiguredGenerators:   snapshot.ConfiguredAvailableGenerators,
			AvailableGenerators:    snapshot.EffectiveAvailableGenerators,
			PeakReservedGenerators: peakReserved,
			ReservationCount:       reservationCount,
			PressurePct:            pressurePct,
			Pressure:               pressure,
		})
		dayStart = dayEnd
	}
	return &RunCalendarCapacity{ProjectID: projectID, Days: days}
}

// reservationWindowOverlapsDay reports whether a reservation window (nil bound =
// open-ended) intersects [dayStart, dayEnd).
func reservationWindowOverlapsDay(start, end *time.Time, dayStart, dayEnd time.Time) bool {
	if start != nil && !start.Before(dayEnd) {
		return false
	}
	if end != nil && !end.After(dayStart) {
		return false
	}
	return true
}

type scenarioScheduleOccurrenceWindowStore interface {
	ListScenarioScheduleOccurrencesByTimeRange(projectID string, from time.Time, to time.Time) ([]*storage.ScenarioScheduleOccurrence, error)
}

type executionPlanScheduleOccurrenceWindowStore interface {
	ListExecutionPlanScheduleOccurrencesByTimeRange(projectID string, from time.Time, to time.Time) ([]*storage.ExecutionPlanScheduleOccurrence, error)
}

func (tr *TestRunner) runCalendarSnapshot(projectID string, from time.Time, to time.Time) runCalendarSnapshot {
	tests := tr.listOperationalTestSnapshots()
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
	var maxScenarioDuration time.Duration
	for _, scenario := range tr.scenarios {
		if scenario == nil {
			continue
		}
		scenarioProjectID := normalizeProjectID(scenario.ProjectID)
		if projectID != "" && scenarioProjectID != projectID {
			continue
		}
		if parsed, err := time.ParseDuration(scenario.Duration); err == nil && parsed > maxScenarioDuration {
			maxScenarioDuration = parsed
		}
		groups := make([]TestGroup, 0, len(scenario.Scripts))
		totalVUsers := 0
		for _, script := range scenario.Scripts {
			totalVUsers += script.VUsers
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

	// The window queries filter on planned_at >= from, but an occurrence planned
	// before `from` can still run INTO the window (its EndAt = planned_at +
	// scenario duration), e.g. an overnight soak. Widen the lower bound of the
	// fetch by the longest scenario duration (with a floor that covers common
	// overnight runs) so those boundary-spanning occurrences are returned;
	// BuildRunCalendar then trims by precise overlap, so over-fetching is safe.
	queryLookback := maxScenarioDuration
	if queryLookback < calendarOccurrenceLookbackFloor {
		queryLookback = calendarOccurrenceLookbackFloor
	}
	queryFrom := from.Add(-queryLookback)

	occurrences := make([]*storage.ScenarioScheduleOccurrence, 0)
	if store != nil {
		var storedOccurrences []*storage.ScenarioScheduleOccurrence
		var err error
		if windowStore, ok := store.(scenarioScheduleOccurrenceWindowStore); ok {
			storedOccurrences, err = windowStore.ListScenarioScheduleOccurrencesByTimeRange(projectID, queryFrom, to)
		} else {
			storedOccurrences, err = store.ListAllScenarioScheduleOccurrences()
		}
		if err == nil {
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
	// Recurring execution-plan schedules + their materialized occurrences are read
	// directly from the plan-schedule store (fresh per-query objects — no clone
	// needed), filtered to the requested project.
	planSchedules := make([]*storage.ExecutionPlanSchedule, 0)
	planOccurrences := make([]*storage.ExecutionPlanScheduleOccurrence, 0)
	plans := map[string]*storage.ExecutionPlan{}
	if planStore := tr.execPlanScheduleStore; planStore != nil {
		windowOccurrencesLoaded := false
		if windowStore, ok := planStore.(executionPlanScheduleOccurrenceWindowStore); ok {
			if occ, err := windowStore.ListExecutionPlanScheduleOccurrencesByTimeRange(projectID, queryFrom, to); err == nil {
				for _, o := range occ {
					if o != nil {
						planOccurrences = append(planOccurrences, o)
					}
				}
				windowOccurrencesLoaded = true
			}
		}
		if allSchedules, err := planStore.ListAllExecutionPlanSchedules(); err == nil {
			for _, schedule := range allSchedules {
				if schedule == nil {
					continue
				}
				if projectID != "" && normalizeProjectID(schedule.ProjectID) != projectID {
					continue
				}
				planSchedules = append(planSchedules, schedule)
				if _, ok := plans[schedule.PlanID]; !ok {
					if plan, err := planStore.GetExecutionPlan(schedule.PlanID); err == nil && plan != nil {
						plans[schedule.PlanID] = plan
					}
				}
				if !windowOccurrencesLoaded {
					if occ, err := planStore.ListExecutionPlanScheduleOccurrences(schedule.ID); err == nil {
						for _, o := range occ {
							if o != nil {
								planOccurrences = append(planOccurrences, o)
							}
						}
					}
				}
			}
		}
	}

	return runCalendarSnapshot{
		Tests:           filteredTests,
		Schedules:       schedules,
		Occurrences:     occurrences,
		Scenarios:       scenarios,
		PlanSchedules:   planSchedules,
		PlanOccurrences: planOccurrences,
		Plans:           plans,
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
	if calendarOperatorStopped(test, manifest) {
		event.ReasonCode = "operator_stopped"
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

func calendarOperatorStopped(test *ManagedTest, manifest *RunArchiveManifest) bool {
	return test != nil && test.Status == "COMPLETE" && (test.StoppedBySubject != "" || (manifest != nil && manifest.TerminationReason == "operator_stopped"))
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
	reason := ""
	if calendarOperatorStopped(test, manifest) {
		reason = "operator_stopped"
	}
	source := "ad_hoc"
	planID, planName, planRunID := "", "", ""
	if link := test.ExecutionPlan; link != nil {
		source, runType = "execution_plan", "execution_plan"
		planID, planName, planRunID = link.PlanID, link.PlanName, link.PlanRunID
	}
	return RunCalendarEvent{
		ReasonCode:    reason,
		ID:            "test:" + test.ID,
		Source:        source,
		RunType:       runType,
		PlanID:        planID,
		PlanName:      planName,
		PlanRunID:     planRunID,
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
	// A schedule that requires approval or has an unresolved conflict will not
	// materialize as-is; project its occurrences tagged "blocked" with the reason
	// so the calendar surfaces them rather than hiding the runs.
	blockedReason := ""
	if schedule.NextRequiredAction == "approval_required" {
		blockedReason = "approval_required"
	} else if strings.TrimSpace(schedule.ConflictSummary) != "" {
		blockedReason = "schedule_conflict"
	}
	status := "scheduled"
	occurrenceStatus := ""
	if blockedReason != "" {
		status = "blocked"
		occurrenceStatus = "blocked"
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
			ID:               fmt.Sprintf("projected:%s:%d", schedule.ID, plannedAt.Unix()),
			Source:           "cron",
			RunType:          "scheduled",
			Title:            calendarTitle(firstPresentString(schedule.RunNameTemplate, schedule.Name, scenario.Name, "Scheduled Run")),
			Status:           status,
			StartAt:          plannedAt,
			EndAt:            &endAt,
			Projected:        true,
			OccurrenceStatus: occurrenceStatus,
			ReasonCode:       blockedReason,
			ConflictSummary:  schedule.ConflictSummary,
			ScheduleID:       schedule.ID,
			ScheduleName:     schedule.Name,
			CronExpr:         schedule.CronExpr,
			Timezone:         schedule.Timezone,
			ScenarioID:       schedule.ScenarioID,
			ScenarioName:     scenario.Name,
			ReleaseID:        schedule.ReleaseID,
			ProjectID:        normalizeProjectID(schedule.ProjectID),
			Workload:         workloadForCalendar(nil, scenario, duration),
			User:             userForCalendarSchedule(schedule, nil),
			Topology: RunCalendarTopology{
				Source:  "planned",
				Summary: "Generator assignment will be resolved when the run materializes.",
			},
		})
	}
	return events
}

// executionPlanCalendarWorkload approximates a plan run's workload for the
// calendar from the per-scenario summaries: aggregate peak VUsers (parallel = sum
// of enabled entries, sequential = max) and duration (parallel = longest scenario,
// sequential = sum). Mirrors executionPlanAggregatePeak's parallel/sequential split.
func executionPlanCalendarWorkload(plan *storage.ExecutionPlan, scenarios map[string]runCalendarScenarioSummary) (int, time.Duration) {
	if plan == nil {
		return 0, time.Hour
	}
	parallel := !strings.EqualFold(strings.TrimSpace(plan.RunMode), "sequential")
	totalVUsers := 0
	var seqDuration, maxDuration time.Duration
	counted := false
	for _, entry := range plan.Entries {
		if !entry.IsEnabled() {
			continue
		}
		scenario, ok := scenarios[entry.ScenarioID]
		if !ok {
			continue
		}
		counted = true
		if parallel {
			totalVUsers += scenario.TotalVUsers
		} else if scenario.TotalVUsers > totalVUsers {
			totalVUsers = scenario.TotalVUsers
		}
		d := time.Hour
		if parsed, err := time.ParseDuration(scenario.Duration); err == nil && parsed > 0 {
			d = parsed
		}
		seqDuration += d
		if d > maxDuration {
			maxDuration = d
		}
	}
	if !counted {
		return 0, time.Hour
	}
	if parallel {
		return totalVUsers, maxDuration
	}
	return totalVUsers, seqDuration
}

func executionPlanCalendarTitle(schedule *storage.ExecutionPlanSchedule, plan *storage.ExecutionPlan) string {
	candidates := make([]string, 0, 4)
	if schedule != nil {
		candidates = append(candidates, schedule.RunNameTemplate, schedule.Name)
	}
	if plan != nil {
		candidates = append(candidates, plan.Name)
	}
	candidates = append(candidates, "Execution Plan Run")
	return calendarTitle(firstPresentString(candidates...))
}

func runCalendarEventFromPlanOccurrence(occurrence *storage.ExecutionPlanScheduleOccurrence, schedule *storage.ExecutionPlanSchedule, plan *storage.ExecutionPlan, scenarios map[string]runCalendarScenarioSummary) RunCalendarEvent {
	totalVUsers, duration := executionPlanCalendarWorkload(plan, scenarios)
	start := occurrence.PlannedAt.UTC()
	if occurrence.MaterializedAt != nil && !occurrence.MaterializedAt.IsZero() {
		start = occurrence.MaterializedAt.UTC()
	}
	endAt := start.Add(duration).UTC()
	status := strings.TrimSpace(occurrence.Status)
	if status == "" {
		status = "scheduled"
	}
	event := RunCalendarEvent{
		ID:               "plan-occurrence:" + occurrence.ID,
		Source:           "execution_plan",
		RunType:          "execution_plan",
		Title:            executionPlanCalendarTitle(schedule, plan),
		Status:           status,
		StartAt:          start,
		EndAt:            &endAt,
		OccurrenceStatus: occurrence.Status,
		ReasonCode:       occurrence.ReasonCode,
		ConflictSummary:  occurrence.ConflictSummary,
		ScheduleID:       occurrence.ScheduleID,
		PlanID:           occurrence.PlanID,
		ProjectID:        normalizeProjectID(occurrence.ProjectID),
		Workload:         RunCalendarWorkload{TotalVUsers: totalVUsers, Duration: duration.String()},
		Topology: RunCalendarTopology{
			Source:  "planned",
			Summary: "Execution plan run — generator assignment resolves per scenario at materialization.",
		},
	}
	if schedule != nil {
		event.ScheduleName = schedule.Name
		event.CronExpr = schedule.CronExpr
		event.Timezone = schedule.Timezone
	}
	if plan != nil {
		event.PlanName = plan.Name
	}
	event.PlanRunID = strings.TrimSpace(occurrence.RunID)
	return event
}

func projectRunCalendarPlanSchedule(schedule *storage.ExecutionPlanSchedule, plan *storage.ExecutionPlan, from time.Time, to time.Time, now time.Time, planOccurrenceByScheduleAt map[string]struct{}, scenarios map[string]runCalendarScenarioSummary) []RunCalendarEvent {
	startFrom := from
	if startFrom.Before(now) {
		startFrom = now
	}
	totalVUsers, duration := executionPlanCalendarWorkload(plan, scenarios)
	planName := ""
	if plan != nil {
		planName = plan.Name
	}
	allows := executionPlanScheduleAllows(schedule.DisabledFrom, schedule.DisabledUntil)
	cursor := startFrom.Add(-time.Minute)
	events := make([]RunCalendarEvent, 0)
	for len(events) < runCalendarMaxProjectedPerCron {
		next, err := nextCronRun(schedule.CronExpr, schedule.Timezone, cursor, allows)
		if err != nil || next == nil || !next.Before(to) {
			break
		}
		plannedAt := next.UTC()
		cursor = plannedAt
		if _, exists := planOccurrenceByScheduleAt[runCalendarOccurrenceKey(schedule.ID, plannedAt)]; exists {
			continue
		}
		endAt := plannedAt.Add(duration).UTC()
		events = append(events, RunCalendarEvent{
			ID:           fmt.Sprintf("plan-projected:%s:%d", schedule.ID, plannedAt.Unix()),
			Source:       "execution_plan",
			RunType:      "execution_plan",
			Title:        executionPlanCalendarTitle(schedule, plan),
			Status:       "scheduled",
			StartAt:      plannedAt,
			EndAt:        &endAt,
			Projected:    true,
			ScheduleID:   schedule.ID,
			ScheduleName: schedule.Name,
			CronExpr:     schedule.CronExpr,
			Timezone:     schedule.Timezone,
			PlanID:       schedule.PlanID,
			PlanName:     planName,
			ProjectID:    normalizeProjectID(schedule.ProjectID),
			Workload:     RunCalendarWorkload{TotalVUsers: totalVUsers, Duration: duration.String()},
			Topology: RunCalendarTopology{
				Source:  "planned",
				Summary: "Execution plan run — generator assignment resolves per scenario at materialization.",
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
	// A recurring occurrence belongs to the schedule owner unless a concrete
	// starter was captured for the materialized run. Every durable test now has
	// creator attribution (including internal controller attribution), so using
	// creator presence as the fallback gate would permanently hide the schedule
	// owner for scheduler-created runs.
	if test == nil || (test.StartedBySubject == "" && test.StartedByDisplayName == "") {
		if user.OwnerSubject != "" || user.OwnerDisplayName != "" {
			user.DisplayName = firstNonEmpty(user.OwnerDisplayName, user.OwnerSubject)
			user.Subject = user.OwnerSubject
			user.Attribution = "schedule_owner"
			return user
		}
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
