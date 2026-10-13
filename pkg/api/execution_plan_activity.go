package api

// Execution-plan activity: whether a plan is running, waiting or done, for the
// plan card and the run progress view. A parallel plan run's own status turns
// "completed" once every entry has been started, so whether its load is still
// running is read from the runs it started.

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// executionPlanActivityRunLimit bounds how many recent plan runs are read to
// find the active and last runs.
const executionPlanActivityRunLimit = 20

// ExecutionPlanRunEntryView is a plan-run entry with the live state of the run
// it started.
type ExecutionPlanRunEntryView struct {
	storage.ExecutionPlanRunEntry
	TestStatus     string     `json:"test_status,omitempty"`
	TestRefKey     string     `json:"test_ref_key,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
	PlannedSeconds float64    `json:"planned_seconds,omitempty"`
	VUsers         int        `json:"vusers,omitempty"`
}

// ExecutionPlanRunView is a plan run with its entries' live state, whether it
// is still running, its outcome and its timing.
type ExecutionPlanRunView struct {
	storage.ExecutionPlanRun
	Entries []ExecutionPlanRunEntryView `json:"entries"`
	// Active is true while the plan run is in progress or any run it started
	// has not ended.
	Active bool `json:"active"`
	// Outcome is running, completed, failed or stopped.
	Outcome   string     `json:"outcome"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	// EstimatedEndAt is when the run's planned durations end; it is absent when
	// any running or remaining entry has no fixed duration.
	EstimatedEndAt *time.Time `json:"estimated_end_at,omitempty"`
	ActiveVUsers   int        `json:"active_vusers,omitempty"`
}

// ExecutionPlanWaitingOccurrence is a scheduled run of the plan held before it
// started: waiting for approval, for the previous run, or for a decision.
type ExecutionPlanWaitingOccurrence struct {
	ScheduleID   string    `json:"schedule_id"`
	ScheduleName string    `json:"schedule_name,omitempty"`
	OccurrenceID string    `json:"occurrence_id,omitempty"`
	PlannedAt    time.Time `json:"planned_at"`
	ReasonCode   string    `json:"reason_code"`
	ApprovalID   string    `json:"approval_id,omitempty"`
	Summary      string    `json:"summary,omitempty"`
}

// ExecutionPlanActivity is what a plan is doing now and how its last run ended.
type ExecutionPlanActivity struct {
	ActiveRun       *ExecutionPlanRunView            `json:"active_run,omitempty"`
	ActiveRuns      int                              `json:"active_runs,omitempty"`
	LastRun         *ExecutionPlanRunView            `json:"last_run,omitempty"`
	PendingApproval *storage.ExecutionPlanApproval   `json:"pending_approval,omitempty"`
	Waiting         []ExecutionPlanWaitingOccurrence `json:"waiting,omitempty"`
	// Quota is set when the plan's peak is over its project's VUser limit,
	// so a run waits for a project admin's approval unless one is ready.
	Quota *ExecutionPlanQuota `json:"quota,omitempty"`
}

// ExecutionPlanQuota is a plan's peak VUsers against its project's limit, as
// the run gate judges it (parallel = sum of entries, sequential = the largest).
type ExecutionPlanQuota struct {
	PeakVUsers int `json:"peak_vusers"`
	// ActiveVUsers is the load of the plan's runs already in progress, which
	// a run started alongside them adds to (overlap policy allow).
	ActiveVUsers int `json:"active_vusers,omitempty"`
	MaxVUsers    int `json:"max_vusers"`
	// ReadyApprovalID names the approval the next run uses: granted for the
	// plan as it is now and not yet used. Empty when the run must ask.
	ReadyApprovalID string `json:"ready_approval_id,omitempty"`
}

// executionPlanWithActivity is a plan as listed with ?include_activity=true.
type executionPlanWithActivity struct {
	*storage.ExecutionPlan
	Activity *ExecutionPlanActivity `json:"activity,omitempty"`
}

type executionPlanTestState struct {
	status    string
	refKey    string
	startedAt *time.Time
	endedAt   *time.Time
	duration  time.Duration
	vusers    int
	stoppedBy string
}

func (tr *TestRunner) executionPlanTestStates(runs []*storage.ExecutionPlanRun) map[string]executionPlanTestState {
	states := make(map[string]executionPlanTestState)
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	for _, run := range runs {
		if run == nil {
			continue
		}
		for _, entry := range run.Entries {
			id := strings.TrimSpace(entry.TestID)
			if id == "" {
				continue
			}
			test := tr.tests[id]
			if test == nil {
				continue
			}
			states[id] = executionPlanTestState{
				status:    test.Status,
				refKey:    test.RefKey,
				startedAt: cloneTimePtr(test.StartedAt),
				endedAt:   cloneTimePtr(test.EndedAt),
				duration:  test.Duration,
				vusers:    test.TotalVUsers,
				stoppedBy: test.StoppedBySubject,
			}
		}
	}
	return states
}

// executionPlanRunStopDetail describes entries Stop plan ended or skipped;
// their Stopped flag is the run's durable record of the stop.
const executionPlanRunStopDetail = "plan stopped"

func executionPlanRunStopped(entries []storage.ExecutionPlanRunEntry) bool {
	return executionPlanRunStopDetailOf(entries) != ""
}

// executionPlanRunStopDetailOf is the stop's description, or "" when the run
// was not stopped. A stopped entry that failed on its own keeps its error as
// Detail, so the description is read from an entry the stop itself wrote.
func executionPlanRunStopDetailOf(entries []storage.ExecutionPlanRunEntry) string {
	found := false
	for _, entry := range entries {
		if !entry.Stopped {
			continue
		}
		found = true
		if detail := strings.TrimSpace(entry.Detail); strings.HasPrefix(detail, executionPlanRunStopDetail) {
			return detail
		}
	}
	if found {
		return executionPlanRunStopDetail
	}
	return ""
}

// buildExecutionPlanRunView joins a plan run with the live state of its runs.
// estimate also projects an active run's end, which reads each remaining
// entry's scenario; callers that only need whether the run is active skip it.
func (tr *TestRunner) buildExecutionPlanRunView(run *storage.ExecutionPlanRun, states map[string]executionPlanTestState, now time.Time, estimate bool) *ExecutionPlanRunView {
	if run == nil {
		return nil
	}
	view := &ExecutionPlanRunView{ExecutionPlanRun: *run, Entries: make([]ExecutionPlanRunEntryView, 0, len(run.Entries))}
	view.ExecutionPlanRun.Entries = nil
	active := strings.EqualFold(run.Status, "running")
	// A run an operator stopped or aborted, or that Stop plan ended, ended on
	// purpose; one that failed or was aborted by the controller did not. A
	// failure outranks a stop: its error is what needs attention.
	failed, stopped := false, false
	var started, ended *time.Time
	for _, entry := range run.Entries {
		entryView := ExecutionPlanRunEntryView{ExecutionPlanRunEntry: entry}
		if entry.Stopped {
			stopped = true
		}
		state, known := states[strings.TrimSpace(entry.TestID)]
		if known {
			entryView.TestStatus = state.status
			entryView.TestRefKey = state.refKey
			entryView.StartedAt = cloneTimePtr(state.startedAt)
			entryView.EndedAt = cloneTimePtr(state.endedAt)
			entryView.VUsers = state.vusers
			if state.duration > 0 {
				entryView.PlannedSeconds = state.duration.Seconds()
			}
			if !isTerminalTestStatus(state.status) {
				active = true
				view.ActiveVUsers += state.vusers
			}
			switch strings.ToUpper(strings.TrimSpace(state.status)) {
			case "FAILED":
				failed = true
			case "ABORTED":
				// Stop plan aborts runs that cannot stop gracefully, and ends a run
				// it cancelled before the run started.
				if state.stoppedBy != "" || entry.Stopped {
					stopped = true
				} else {
					failed = true
				}
			case "COMPLETE":
				if state.stoppedBy != "" {
					stopped = true
				}
			}
			if state.startedAt != nil && (started == nil || state.startedAt.Before(*started)) {
				started = cloneTimePtr(state.startedAt)
			}
			if state.endedAt != nil && (ended == nil || state.endedAt.After(*ended)) {
				ended = cloneTimePtr(state.endedAt)
			}
		}
		if entry.Status == "failed" && !entry.Stopped {
			failed = true
		}
		view.Entries = append(view.Entries, entryView)
	}
	view.Active = active
	if active && estimate {
		for index := range view.Entries {
			entry := &view.Entries[index]
			if entry.PlannedSeconds == 0 && entry.Status != "skipped" && entry.Status != "failed" && (entry.TestStatus == "" || !isTerminalTestStatus(entry.TestStatus)) {
				entry.PlannedSeconds = tr.executionPlanScenarioSeconds(entry.ScenarioID)
			}
		}
	}
	if started == nil {
		created := run.CreatedAt
		started = &created
	}
	view.StartedAt = started
	switch {
	case active:
		view.Outcome = "running"
		if estimate {
			view.EstimatedEndAt = executionPlanEstimatedEnd(view, now)
		}
	case failed:
		view.Outcome = "failed"
	case stopped:
		view.Outcome = "stopped"
	case strings.EqualFold(run.Status, "halted") || strings.EqualFold(run.Status, "failed"):
		view.Outcome = "failed"
	default:
		view.Outcome = "completed"
	}
	if !active {
		if ended == nil {
			ended = cloneTimePtr(run.CompletedAt)
		}
		if ended == nil {
			updated := run.UpdatedAt
			ended = &updated
		}
		view.EndedAt = ended
	}
	return view
}

// executionPlanEstimatedEnd projects when an active run's planned durations
// end: a sequential run's remaining entries follow the running one; a parallel
// run ends with its last entry.
func executionPlanEstimatedEnd(view *ExecutionPlanRunView, now time.Time) *time.Time {
	end := now
	sequential := strings.EqualFold(view.RunMode, "sequential")
	for _, entry := range view.Entries {
		if entry.Status == "skipped" || entry.Status == "failed" || (entry.TestStatus != "" && isTerminalTestStatus(entry.TestStatus)) {
			continue
		}
		if entry.PlannedSeconds <= 0 {
			return nil
		}
		planned := time.Duration(entry.PlannedSeconds * float64(time.Second))
		start := end
		if !sequential {
			start = now
		}
		if entry.StartedAt != nil {
			start = *entry.StartedAt
		}
		entryEnd := start.Add(planned)
		if entryEnd.Before(now) {
			entryEnd = now
		}
		if sequential || entryEnd.After(end) {
			end = entryEnd
		}
	}
	return &end
}

func (tr *TestRunner) executionPlanScenarioSeconds(scenarioID string) float64 {
	if strings.TrimSpace(scenarioID) == "" {
		return 0
	}
	scenario, ok := tr.GetScenario(scenarioID)
	if !ok || scenario == nil {
		return 0
	}
	duration, err := time.ParseDuration(strings.TrimSpace(scenario.Duration))
	if err != nil || duration <= 0 {
		return 0
	}
	return duration.Seconds()
}

// ExecutionPlanRunViewFor returns a plan run with its entries' live state.
func (tr *TestRunner) ExecutionPlanRunViewFor(run *storage.ExecutionPlanRun) *ExecutionPlanRunView {
	if run == nil {
		return nil
	}
	// A follower reads the leader's runs as of its last refresh.
	tr.refreshTestsForHARead()
	return tr.buildExecutionPlanRunView(run, tr.executionPlanTestStates([]*storage.ExecutionPlanRun{run}), time.Now().UTC(), true)
}

// ExecutionPlanRunViewsFor returns plan runs with their entries' live state,
// reading the runs' state once for the whole list.
func (tr *TestRunner) ExecutionPlanRunViewsFor(runs []*storage.ExecutionPlanRun) []*ExecutionPlanRunView {
	tr.refreshTestsForHARead()
	states := tr.executionPlanTestStates(runs)
	now := time.Now().UTC()
	views := make([]*ExecutionPlanRunView, 0, len(runs))
	for _, run := range runs {
		if view := tr.buildExecutionPlanRunView(run, states, now, true); view != nil {
			views = append(views, view)
		}
	}
	return views
}

// activeExecutionPlanRuns lists the plan's runs that still have load running
// or to run, newest first, leaving out excludeRunID.
func (tr *TestRunner) activeExecutionPlanRuns(planID, excludeRunID string) ([]*ExecutionPlanRunView, error) {
	if tr.execPlanRunStore == nil || strings.TrimSpace(planID) == "" {
		return nil, nil
	}
	runs, err := tr.executionPlanRecentAndLiveRuns(planID, tr.liveExecutionPlanRunIndex()[planID])
	if err != nil {
		return nil, err
	}
	states := tr.executionPlanTestStates(runs)
	now := time.Now().UTC()
	active := make([]*ExecutionPlanRunView, 0)
	for _, run := range runs {
		if run == nil || run.ID == strings.TrimSpace(excludeRunID) {
			continue
		}
		if view := tr.buildExecutionPlanRunView(run, states, now, false); view.Active {
			active = append(active, view)
		}
	}
	return active, nil
}

// executionPlanRecentAndLiveRuns is the plan's recent runs, newest first, plus
// any older run one of the plan's runs still in progress belongs to: a long
// run outlasts the recent window when many others were started alongside it.
func (tr *TestRunner) executionPlanRecentAndLiveRuns(planID string, live []string) ([]*storage.ExecutionPlanRun, error) {
	runs, err := tr.execPlanRunStore.ListExecutionPlanRuns(planID, executionPlanActivityRunLimit, 0)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(runs))
	for _, run := range runs {
		if run != nil {
			seen[run.ID] = struct{}{}
		}
	}
	older := make([]string, 0)
	for _, runID := range live {
		if _, ok := seen[runID]; !ok {
			seen[runID] = struct{}{}
			older = append(older, runID)
		}
	}
	for _, runID := range older {
		run, err := tr.execPlanRunStore.GetExecutionPlanRun(runID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if run != nil && run.PlanID == planID {
			runs = append(runs, run)
		}
	}
	return runs, nil
}

// liveExecutionPlanRunIndex maps each plan to the plan runs that its runs still
// in progress belong to, from one pass over the controller's runs.
func (tr *TestRunner) liveExecutionPlanRunIndex() map[string][]string {
	index := make(map[string][]string)
	seen := make(map[string]struct{})
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	for _, test := range tr.tests {
		if test == nil || test.ExecutionPlan == nil || isTerminalTestStatus(test.Status) {
			continue
		}
		runID := test.ExecutionPlan.PlanRunID
		if _, ok := seen[runID]; ok {
			continue
		}
		seen[runID] = struct{}{}
		index[test.ExecutionPlan.PlanID] = append(index[test.ExecutionPlan.PlanID], runID)
	}
	return index
}

// ExecutionPlanActivitiesFor reports each plan's activity, reading the
// controller's runs once for the whole list.
func (tr *TestRunner) ExecutionPlanActivitiesFor(plans []*storage.ExecutionPlan) ([]*ExecutionPlanActivity, []error) {
	tr.refreshTestsForHARead()
	index := tr.liveExecutionPlanRunIndex()
	activities := make([]*ExecutionPlanActivity, len(plans))
	errs := make([]error, len(plans))
	// The plans of a list share their project: read its VUser limit once.
	limits := map[string]int{}
	projectMaxVUsers := func(projectID string) (int, error) {
		if limit, ok := limits[projectID]; ok {
			return limit, nil
		}
		limit, err := tr.projectMaxVUsersStrict(projectID)
		if err == nil {
			limits[projectID] = limit
		}
		return limit, err
	}
	for i, plan := range plans {
		if plan != nil {
			activities[i], errs[i] = tr.executionPlanActivity(plan, index[plan.ID], projectMaxVUsers)
		}
	}
	return activities, errs
}

// ExecutionPlanActivityFor reports what a plan is doing: its active run, its
// last finished run, a pending approval and scheduled runs held before start.
func (tr *TestRunner) ExecutionPlanActivityFor(plan *storage.ExecutionPlan) (*ExecutionPlanActivity, error) {
	if plan == nil {
		return nil, nil
	}
	tr.refreshTestsForHARead()
	return tr.executionPlanActivity(plan, tr.liveExecutionPlanRunIndex()[plan.ID], tr.projectMaxVUsersStrict)
}

func (tr *TestRunner) executionPlanActivity(plan *storage.ExecutionPlan, live []string, projectMaxVUsers func(projectID string) (int, error)) (*ExecutionPlanActivity, error) {
	activity := &ExecutionPlanActivity{}
	if tr.execPlanRunStore != nil {
		runs, err := tr.executionPlanRecentAndLiveRuns(plan.ID, live)
		if err != nil {
			return nil, err
		}
		states := tr.executionPlanTestStates(runs)
		now := time.Now().UTC()
		for _, run := range runs {
			// Only the active run the card shows needs its end projected.
			view := tr.buildExecutionPlanRunView(run, states, now, activity.ActiveRun == nil)
			if view == nil {
				continue
			}
			if view.Active {
				activity.ActiveRuns++
				if activity.ActiveRun == nil {
					activity.ActiveRun = view
				}
				continue
			}
			if activity.LastRun == nil {
				activity.LastRun = view
			}
		}
	}
	store := tr.execPlanScheduleStore
	if store == nil {
		return activity, nil
	}
	approvals, err := store.ListExecutionPlanApprovals(plan.ID)
	if err != nil {
		return nil, err
	}
	// The plan's peak reads its scenarios, and its content hash includes it:
	// work each out at most once.
	peak, peakKnown, contentHash := 0, false, ""
	planPeak := func() int {
		if !peakKnown {
			peak, peakKnown = tr.executionPlanAggregatePeak(plan), true
		}
		return peak
	}
	planContentHash := func() string {
		if contentHash == "" {
			contentHash = tr.executionPlanApprovalContentHashForPeak(plan, planPeak())
		}
		return contentHash
	}
	for _, approval := range approvals {
		if approval == nil || !strings.EqualFold(approval.Status, "pending") {
			continue
		}
		// A request for content the plan no longer has holds nothing: the next
		// run asks again for what the plan is now.
		if strings.TrimSpace(approval.PlanContentHash) != "" && approval.PlanContentHash != planContentHash() {
			continue
		}
		if activity.PendingApproval == nil || approval.RequestedAt.After(activity.PendingApproval.RequestedAt) {
			activity.PendingApproval = approval
		}
	}
	// A quota lookup that fails leaves the position out: the run gate still
	// judges the run when it starts.
	if maxVUsers, err := projectMaxVUsers(normalizeProjectID(plan.ProjectID)); err == nil && maxVUsers > 0 {
		// As the gate, a run started alongside runs of the plan still in
		// progress is judged on their load together.
		activeVUsers := 0
		if activity.ActiveRuns > 0 {
			if vusers, _, err := tr.executionPlanActiveVUsers(plan.ID, ""); err == nil {
				activeVUsers = vusers
			}
		}
		if saturatingAddNonNegative(planPeak(), activeVUsers) > maxVUsers {
			quota := &ExecutionPlanQuota{PeakVUsers: planPeak(), ActiveVUsers: activeVUsers, MaxVUsers: maxVUsers}
			// The approval the run gate would use: the first granted, unused
			// one for exactly this content, in the order the gate reads them.
			for _, approval := range approvals {
				if approval != nil && approval.Status == "approved" && approval.RunID == "" && approval.PlanContentHash == planContentHash() {
					quota.ReadyApprovalID = approval.ID
					break
				}
			}
			activity.Quota = quota
		}
	}
	schedules, err := store.ListExecutionPlanSchedules(plan.ID)
	if err != nil {
		return nil, err
	}
	for _, schedule := range schedules {
		waiting, err := executionPlanScheduleWaitingOccurrence(store, schedule)
		if err != nil {
			slog.Warn("execution-plan activity could not read a held occurrence", "schedule_id", schedule.ID, "error", err)
			continue
		}
		if waiting != nil {
			activity.Waiting = append(activity.Waiting, *waiting)
		}
	}
	return activity, nil
}

// executionPlanScheduleWaitingOccurrence is the occurrence a schedule is held
// on, if any. A hold always has its blocked occurrence at the schedule's next
// run time; operator_decision_required alone also follows a failed run, after
// the schedule has moved on to its next (future) run, and holds nothing.
func executionPlanScheduleWaitingOccurrence(store executionPlanSchedulePersistence, schedule *storage.ExecutionPlanSchedule) (*ExecutionPlanWaitingOccurrence, error) {
	// The sweep skips a disabled schedule, so its held run waits on nothing
	// until it is enabled again.
	if schedule == nil || !schedule.Enabled || schedule.NextRunAt == nil {
		return nil, nil
	}
	switch schedule.NextRequiredAction {
	case "approval_required", executionPlanWaitingForPreviousRun, "operator_decision_required":
	default:
		return nil, nil
	}
	occurrence, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, schedule.NextRunAt.UTC())
	if err != nil {
		return nil, err
	}
	if occurrence == nil || !strings.EqualFold(occurrence.Status, "blocked") {
		return nil, nil
	}
	return &ExecutionPlanWaitingOccurrence{
		ScheduleID:   schedule.ID,
		ScheduleName: schedule.Name,
		OccurrenceID: occurrence.ID,
		PlannedAt:    schedule.NextRunAt.UTC(),
		ReasonCode:   firstNonEmpty(occurrence.ReasonCode, schedule.NextRequiredAction),
		ApprovalID:   occurrence.ApprovalID,
		Summary:      firstNonEmpty(occurrence.ConflictSummary, schedule.LastError),
	}, nil
}

// lockExecutionPlanStart serializes starts of one plan.
func (tr *TestRunner) lockExecutionPlanStart(planID string) func() {
	lock, _ := tr.executionPlanStarts.LoadOrStore(strings.TrimSpace(planID), &sync.Mutex{})
	mutex := lock.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

// executionPlanWaitingForPreviousRun holds a queued scheduled occurrence until
// the plan's earlier run finishes.
const executionPlanWaitingForPreviousRun = "waiting_for_previous_run"

// ExecutionPlanOverlap describes a run of the plan already in progress when
// another is asked for.
type ExecutionPlanOverlap struct {
	Policy       string    `json:"overlap_policy"`
	ActiveRunID  string    `json:"active_run_id"`
	StartedAt    time.Time `json:"started_at"`
	ActiveVUsers int       `json:"active_vusers,omitempty"`
	Message      string    `json:"message"`
}

// executionPlanOverlap reports the plan's run in progress, if another run of it
// must not start now. Under the allow policy it returns nil.
func (tr *TestRunner) executionPlanOverlap(plan *storage.ExecutionPlan, runID string) (*ExecutionPlanOverlap, error) {
	policy := effectiveExecutionPlanOverlapPolicy(plan)
	if policy == storage.ExecutionPlanOverlapAllow {
		return nil, nil
	}
	active, err := tr.activeExecutionPlanRuns(plan.ID, runID)
	if err != nil {
		return nil, fmt.Errorf("check the plan's runs in progress: %w", err)
	}
	if len(active) == 0 {
		return nil, nil
	}
	current := active[0]
	started := current.CreatedAt
	if current.StartedAt != nil {
		started = *current.StartedAt
	}
	name := strings.TrimSpace(plan.Name)
	message := fmt.Sprintf("%q is already running (started %s UTC).", name, started.UTC().Format("Jan 2 15:04"))
	if policy == storage.ExecutionPlanOverlapQueue {
		message += " Its overlap policy queues scheduled runs until that run finishes; start this one when it does."
	} else {
		message += " Its overlap policy skips a run that would start alongside it."
	}
	return &ExecutionPlanOverlap{
		Policy:       policy,
		ActiveRunID:  current.ID,
		StartedAt:    started.UTC(),
		ActiveVUsers: current.ActiveVUsers,
		Message:      message,
	}, nil
}

// executionPlanActiveVUsers is the VUsers of the plan's runs in progress, which
// a run started alongside them adds to.
func (tr *TestRunner) executionPlanActiveVUsers(planID, runID string) (int, int, error) {
	active, err := tr.activeExecutionPlanRuns(planID, runID)
	if err != nil {
		return 0, 0, err
	}
	total := 0
	for _, run := range active {
		total = saturatingAddNonNegative(total, run.ActiveVUsers)
	}
	return total, len(active), nil
}

var errExecutionPlanRunNotActive = errors.New("execution-plan run is not running")

// StopExecutionPlanRun stops a plan run: entries not yet started are skipped and
// the runs it started are stopped. A sequential run then ends after its running
// entry stops instead of starting the next.
func (tr *TestRunner) StopExecutionPlanRun(planID, runID string, actor testCreationActor) (*ExecutionPlanRunView, error) {
	if !tr.isExecutionLeader() {
		return nil, errNotExecutionLeader
	}
	if tr.execPlanRunStore == nil {
		return nil, errors.New("execution-plan run storage is not configured")
	}
	// A parallel run creates its entries' runs under the plan's start lock;
	// waiting for it means every run it stages exists, and is stopped, here.
	unlockStart := tr.lockExecutionPlanStart(planID)
	defer unlockStart()
	stoppedBy := firstNonEmpty(strings.TrimSpace(actor.DisplayName), strings.TrimSpace(actor.Subject))
	detail := executionPlanRunStopDetail
	if stoppedBy != "" {
		detail += " by " + stoppedBy
	}
	testIDs := make([]string, 0)
	pendingSkipped := false

	tr.seqMu.Lock()
	st := tr.seqPlanRuns[runID]
	if st != nil && st.planID == planID {
		marked, err := tr.stopSequentialPlanRunLocked(st, detail)
		if err != nil {
			tr.seqMu.Unlock()
			return nil, err
		}
		pendingSkipped = marked
	}
	tr.seqMu.Unlock()

	run, err := tr.execPlanRunStore.GetExecutionPlanRun(runID)
	if err != nil {
		return nil, err
	}
	if run == nil || run.PlanID != planID {
		return nil, errExecutionPlanRunNotFound
	}
	if st == nil && strings.EqualFold(run.RunMode, "sequential") && strings.EqualFold(run.Status, "running") {
		// Recovery has not restored this run's state yet (just after a
		// takeover): it applies the stop when it does, so no further entry
		// starts. Its running entry's run is stopped below.
		tr.seqMu.Lock()
		if restored := tr.seqPlanRuns[runID]; restored != nil {
			marked, err := tr.stopSequentialPlanRunLocked(restored, detail)
			if err != nil {
				tr.seqMu.Unlock()
				return nil, err
			}
			pendingSkipped = marked
		} else {
			if tr.pendingPlanStops == nil {
				tr.pendingPlanStops = make(map[string]string)
			}
			tr.pendingPlanStops[runID] = detail
			pendingSkipped = true
		}
		tr.seqMu.Unlock()
	}
	states := tr.executionPlanTestStates([]*storage.ExecutionPlanRun{run})
	for _, entry := range run.Entries {
		id := strings.TrimSpace(entry.TestID)
		if id == "" {
			continue
		}
		if state, ok := states[id]; ok && !isTerminalTestStatus(state.status) && !slices.Contains(testIDs, id) {
			testIDs = append(testIDs, id)
		}
	}
	if len(testIDs) == 0 && !pendingSkipped {
		return nil, errExecutionPlanRunNotActive
	}
	if !strings.EqualFold(run.RunMode, "sequential") {
		tr.markParallelPlanRunStopped(run, testIDs, detail)
	}
	var stopErr error
	for _, id := range testIDs {
		if err := tr.StopTestAsActor(id, actor); err != nil {
			// A run still queued cannot be stopped gracefully; cancel it.
			if abortErr := tr.AbortTestAsActor(id, actor); abortErr != nil {
				stopErr = errors.Join(stopErr, fmt.Errorf("stop run %s: %w", id, err))
			}
		}
	}
	if latest, err := tr.execPlanRunStore.GetExecutionPlanRun(runID); err == nil && latest != nil {
		run = latest
	}
	return tr.ExecutionPlanRunViewFor(run), stopErr
}

var errExecutionPlanRunNotFound = errors.New("execution plan run not found")

// markParallelPlanRunStopped records Stop plan on the parallel run's entries
// whose runs it is stopping, as stopSequentialPlanRunLocked does for a
// sequential run, so the run reads as stopping until they end and as stopped
// after. It is best-effort: the stop itself is the stops of those runs. A run
// whose final record is still being retried is left to that retry; a write that
// loses to another writer of the run (a queue-handoff retry) is reapplied.
func (tr *TestRunner) markParallelPlanRunStopped(run *storage.ExecutionPlanRun, testIDs []string, detail string) {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return
	}
	for attempt := 0; attempt < 3; attempt++ {
		if run == nil || run.Revision <= 0 || strings.EqualFold(run.Status, "running") {
			return
		}
		tr.seqMu.Lock()
		_, retrying := tr.seqPlanRetrying["run-record:"+run.ID]
		tr.seqMu.Unlock()
		if retrying {
			return
		}
		updated := cloneExecutionPlanRunRecord(run)
		marked := false
		for i := range updated.Entries {
			entry := &updated.Entries[i]
			if entry.Stopped || !slices.Contains(testIDs, strings.TrimSpace(entry.TestID)) {
				continue
			}
			entry.Stopped = true
			if strings.TrimSpace(entry.Detail) == "" {
				entry.Detail = detail
			}
			marked = true
		}
		if !marked {
			return
		}
		updated.UpdatedAt = time.Now().UTC()
		updated.TransitionToken = ""
		err := tr.persistExecutionPlanRunDurableWithTerm(updated, leaseTerm)
		if err == nil {
			return
		}
		if !errors.Is(err, errExecutionPlanRunConflict) {
			slog.Warn("execution-plan run stop could not be recorded on the run", "run_id", run.ID, "error", err)
			return
		}
		if run, err = tr.execPlanRunStore.GetExecutionPlanRun(run.ID); err != nil {
			slog.Warn("execution-plan run stop could not reload the run", "run_id", updated.ID, "error", err)
			return
		}
	}
	if run != nil {
		slog.Warn("execution-plan run stop could not be recorded on the run: it kept changing", "run_id", run.ID)
	}
}

// stopSequentialPlanRunLocked records Stop plan on a sequential run's state:
// entries not started are skipped, the running entry is marked, and no entry
// starts after this. It reports whether the run was still in progress. The
// caller holds seqMu.
func (tr *TestRunner) stopSequentialPlanRunLocked(st *seqPlanRunState, detail string) (bool, error) {
	if st == nil || st.status != "running" {
		return false, nil
	}
	previousEntries := append([]storage.ExecutionPlanRunEntry(nil), st.statuses...)
	previousStop := st.stopDetail
	st.stopDetail = detail
	marked := false
	for i := range st.statuses {
		switch st.statuses[i].Status {
		case "pending":
			st.statuses[i].Status = "skipped"
			st.statuses[i].Detail = detail
			st.statuses[i].Stopped = true
			marked = true
		case "running":
			// The running entry records the stop too, so a run whose last entry
			// is being stopped still ends as stopped, after a restart as well.
			// Its run is stopped by the caller once it exists; one still being
			// created is cancelled by the launcher.
			st.statuses[i].Detail = detail
			st.statuses[i].Stopped = true
			marked = true
		}
	}
	if !marked {
		return false, nil
	}
	run := st.toStorageRun()
	expectedRevision := run.Revision
	if err := tr.persistExecutionPlanRunDurableWithTerm(run, st.leaseTerm); err != nil {
		// A write whose outcome is unknown may have committed: keep the stop in
		// memory when the durable row shows it, so the two stay in step.
		if errors.Is(err, errExecutionPlanRunCommitUnknown) {
			if current, loadErr := tr.execPlanRunStore.GetExecutionPlanRun(run.ID); loadErr == nil && executionPlanRunTransitionMatches(current, run, expectedRevision) {
				st.revision = current.Revision
				return true, nil
			}
		}
		st.statuses = previousEntries
		st.stopDetail = previousStop
		return false, executionPlanPersistenceError("persist stopped execution-plan run", err)
	}
	st.revision = run.Revision
	return true, nil
}
