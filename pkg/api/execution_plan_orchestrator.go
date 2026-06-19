package api

// Sequential execution-plan orchestration. A "sequential" execution plan run is
// dependency-gated: the first enabled entry is created+started, and each
// subsequent entry is created+started only after the previous entry's run
// reaches a terminal state (COMPLETE/FAILED/ABORTED), honoring each entry's
// FailurePolicy (halt stops the sequence, skip continues). Live state is held in
// memory on the TestRunner (consistent with tr.tests) and the plan-run row is
// persisted at every transition so progress survives a dashboard reload. An
// in-flight run is orphaned on controller restart exactly as in-flight tests are.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

// executionPlanRunPersistence persists plan-run progress snapshots.
type executionPlanRunPersistence interface {
	SaveExecutionPlanRun(run *storage.ExecutionPlanRun) error
	GetExecutionPlanRun(id string) (*storage.ExecutionPlanRun, error)
	ListExecutionPlanRuns(planID string, limit int, offset int) ([]*storage.ExecutionPlanRun, error)
}

// seqPlanRunState is the in-memory orchestration state for one sequential plan
// run. Guarded by TestRunner.seqMu — never tr.mu, because advancing calls
// CreateTest/StartTest which take tr.mu, so seqMu is always released first.
type seqPlanRunState struct {
	runID         string
	planName      string
	projectID     string
	timeslot      *string                         // plan-level fallback timeslot
	entries       []storage.ExecutionPlanEntry    // enabled, sorted snapshot
	statuses      []storage.ExecutionPlanRunEntry // parallel to entries
	cursor        int                             // index of the active entry; -1 before start
	planID        string
	status        string
	createdAt     time.Time
	attribSubject string
	attribName    string
}

func (st *seqPlanRunState) toStorageRun() *storage.ExecutionPlanRun {
	run := &storage.ExecutionPlanRun{
		ID:        st.runID,
		PlanID:    st.planID,
		ProjectID: st.projectID,
		RunMode:   "sequential",
		Status:    st.status,
		Entries:   append([]storage.ExecutionPlanRunEntry(nil), st.statuses...),
		CreatedAt: st.createdAt,
		UpdatedAt: time.Now().UTC(),
	}
	switch st.status {
	case "completed", "halted", "failed":
		t := run.UpdatedAt
		run.CompletedAt = &t
	}
	return run
}

// enabledSortedEntries returns the plan's enabled entries in Order.
func enabledSortedEntries(entries []storage.ExecutionPlanEntry) []storage.ExecutionPlanEntry {
	out := make([]storage.ExecutionPlanEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsEnabled() {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// buildPlanEntryCreateRequest maps a plan entry to a test-create request, applying
// per-entry runtime overrides, data-pool overrides (which win over the scenario's
// pools because createTestRequestFromScenario only fills empty DataPools), and an
// optional timeslot (entry-level, else the plan-level fallback). Shared by both the
// sequential orchestrator and the parallel materialize path.
func buildPlanEntryCreateRequest(planName, projectID string, planTimeslot *string, entry storage.ExecutionPlanEntry) CreateTestRequest {
	req := CreateTestRequest{
		Name:       strings.TrimSpace(planName + " / " + executionEntryLabel(entry)),
		ScenarioID: entry.ScenarioID,
		ProjectID:  projectID,
	}
	if len(entry.RuntimeOverrides) > 0 {
		var parsed RuntimeSettingsOverride
		if err := json.Unmarshal(entry.RuntimeOverrides, &parsed); err == nil {
			req.RuntimeSettings = &parsed
		}
	}
	if len(entry.DataPools) > 0 {
		req.DataPools = append([]storage.ScenarioDataPoolBinding(nil), entry.DataPools...)
	}
	if ts := firstNonEmpty(trimPtr(entry.TimeslotID), trimPtr(planTimeslot)); ts != "" {
		req.TimeslotID = &ts
	}
	return req
}

func trimPtr(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// StartExecutionPlanRun launches a sequential execution plan as a gated run and
// returns the initial run snapshot. The plan's enabled entries are snapshotted so
// a mid-run edit to the plan does not change an in-flight run.
func (tr *TestRunner) StartExecutionPlanRun(plan *storage.ExecutionPlan, attribSubject, attribName string) (*storage.ExecutionPlanRun, error) {
	if plan == nil {
		return nil, fmt.Errorf("execution plan is required")
	}
	enabled := enabledSortedEntries(plan.Entries)
	now := time.Now().UTC()
	statuses := make([]storage.ExecutionPlanRunEntry, len(enabled))
	for i, e := range enabled {
		statuses[i] = storage.ExecutionPlanRunEntry{
			Order: e.Order, ScenarioID: e.ScenarioID, ScenarioName: e.ScenarioName, Status: "pending",
		}
	}
	st := &seqPlanRunState{
		runID:         "exec-plan-run-" + uuid.New().String(),
		planID:        plan.ID,
		planName:      plan.Name,
		projectID:     normalizeProjectID(plan.ProjectID),
		timeslot:      plan.TimeslotID,
		entries:       enabled,
		statuses:      statuses,
		cursor:        -1,
		status:        "running",
		createdAt:     now,
		attribSubject: attribSubject,
		attribName:    attribName,
	}

	if len(enabled) == 0 {
		st.status = "completed"
		run := st.toStorageRun()
		tr.persistExecutionPlanRun(run)
		return run, nil
	}

	tr.persistExecutionPlanRun(st.toStorageRun())
	tr.seqMu.Lock()
	if tr.seqPlanRuns == nil {
		tr.seqPlanRuns = make(map[string]*seqPlanRunState)
	}
	if tr.seqPlanByTest == nil {
		tr.seqPlanByTest = make(map[string]string)
	}
	tr.seqPlanRuns[st.runID] = st
	tr.seqMu.Unlock()

	tr.broadcastExecutionPlanEvent("execution_plan_run_started", st.toStorageRun(), nil)
	tr.launchNextPlanEntry(st.runID)

	if tr.execPlanRunStore != nil {
		if latest, err := tr.execPlanRunStore.GetExecutionPlanRun(st.runID); err == nil && latest != nil {
			return latest, nil
		}
	}
	tr.seqMu.Lock()
	current, ok := tr.seqPlanRuns[st.runID]
	tr.seqMu.Unlock()
	if ok {
		return current.toStorageRun(), nil
	}
	return st.toStorageRun(), nil
}

// launchNextPlanEntry advances the cursor and launches the next entry, looping
// past entries that fail to even create when the policy is skip. It returns once
// an entry is launched (waiting for completion to advance) or the run finishes.
func (tr *TestRunner) launchNextPlanEntry(runID string) {
	for {
		tr.seqMu.Lock()
		st, ok := tr.seqPlanRuns[runID]
		if !ok {
			tr.seqMu.Unlock()
			return
		}
		st.cursor++
		if st.cursor >= len(st.entries) {
			st.status = "completed"
			run := st.toStorageRun()
			delete(tr.seqPlanRuns, runID)
			tr.seqMu.Unlock()
			tr.persistExecutionPlanRun(run)
			tr.broadcastExecutionPlanEvent("execution_plan_run_finished", run, nil)
			return
		}
		idx := st.cursor
		entry := st.entries[idx]
		st.statuses[idx].Status = "running"
		planName, projectID, timeslot := st.planName, st.projectID, st.timeslot
		subject, displayName := st.attribSubject, st.attribName
		tr.seqMu.Unlock()

		req := buildPlanEntryCreateRequest(planName, projectID, timeslot, entry)
		test, err := tr.CreateTest(req)
		if err != nil {
			if tr.recordPlanEntryFailure(runID, idx, entry, err.Error()) {
				return // halted
			}
			continue // skip → try the next entry
		}

		// Register the test→run mapping BEFORE starting the test. StartTest spawns
		// the run goroutine, whose backend can reach a terminal state synchronously
		// (e.g. "no generator targets configured" / archive-init failure). The
		// terminal hook advanceExecutionPlanForTest gates entirely on seqPlanByTest,
		// so registering afterward races: if the goroutine wins, the hook misses the
		// mapping and the run stalls in "running" forever. The test is QUEUED until
		// StartTest, so pre-registering is safe.
		tr.seqMu.Lock()
		st, ok = tr.seqPlanRuns[runID]
		if !ok {
			tr.seqMu.Unlock()
			return
		}
		st.statuses[idx].TestID = test.ID
		st.statuses[idx].Status = "running"
		tr.seqPlanByTest[test.ID] = runID
		run := st.toStorageRun()
		tr.seqMu.Unlock()

		tr.persistExecutionPlanRun(run)
		tr.broadcastExecutionPlanEvent("execution_plan_entry_started", run, &run.Entries[idx])
		if subject != "" {
			tr.AttributeTestCreated(test.ID, subject, displayName)
		}

		if err := tr.StartTest(test.ID); err != nil {
			// StartTest errors only before spawning the run goroutine, so no
			// terminal hook can have consumed the mapping yet. Unwind the
			// pre-registration and apply the entry's failure policy.
			tr.seqMu.Lock()
			delete(tr.seqPlanByTest, test.ID)
			if cur, ok := tr.seqPlanRuns[runID]; ok && idx < len(cur.statuses) {
				cur.statuses[idx].TestID = ""
			}
			tr.seqMu.Unlock()
			if tr.recordPlanEntryFailure(runID, idx, entry, err.Error()) {
				return // halted
			}
			continue // skip → try the next entry
		}
		return
	}
}

// recordPlanEntryFailure marks entry idx failed and applies its failure policy.
// Returns true if the run halted (caller should stop), false to continue (skip).
func (tr *TestRunner) recordPlanEntryFailure(runID string, idx int, entry storage.ExecutionPlanEntry, detail string) bool {
	tr.seqMu.Lock()
	st, ok := tr.seqPlanRuns[runID]
	if !ok {
		tr.seqMu.Unlock()
		return true
	}
	st.statuses[idx].Status = "failed"
	st.statuses[idx].Detail = detail
	if entry.FailurePolicy != "skip" {
		st.status = "halted"
		markRemainingSkipped(st, idx+1)
		run := st.toStorageRun()
		delete(tr.seqPlanRuns, runID)
		tr.seqMu.Unlock()
		tr.persistExecutionPlanRun(run)
		tr.broadcastExecutionPlanEvent("execution_plan_entry_finished", run, &run.Entries[idx])
		tr.broadcastExecutionPlanEvent("execution_plan_run_finished", run, nil)
		return true
	}
	run := st.toStorageRun()
	tr.seqMu.Unlock()
	tr.persistExecutionPlanRun(run)
	tr.broadcastExecutionPlanEvent("execution_plan_entry_finished", run, &run.Entries[idx])
	return false
}

// advanceExecutionPlanForTest is the terminal-state hook. For a test that is the
// active entry of a sequential plan run it records the entry's outcome and either
// halts (failure with halt policy) or launches the next entry. Non-plan tests are
// a cheap no-op.
func (tr *TestRunner) advanceExecutionPlanForTest(test *ManagedTest, terminalStatus string) {
	if test == nil {
		return
	}
	tr.seqMu.Lock()
	runID, ok := tr.seqPlanByTest[test.ID]
	if !ok {
		tr.seqMu.Unlock()
		return
	}
	delete(tr.seqPlanByTest, test.ID)
	st, ok := tr.seqPlanRuns[runID]
	if !ok {
		tr.seqMu.Unlock()
		return
	}
	idx := st.cursor
	if idx < 0 || idx >= len(st.statuses) || st.statuses[idx].TestID != test.ID {
		idx = -1
		for i := range st.statuses {
			if st.statuses[i].TestID == test.ID {
				idx = i
				break
			}
		}
		if idx < 0 {
			tr.seqMu.Unlock()
			return
		}
	}
	st.statuses[idx].Status = mapTerminalEntryStatus(terminalStatus)
	switch terminalStatus {
	case "ABORTED":
		st.statuses[idx].Detail = "run aborted before completion"
	case "FAILED":
		if strings.TrimSpace(test.Error) != "" {
			st.statuses[idx].Detail = test.Error
		}
	}
	failed := terminalStatus == "FAILED" || terminalStatus == "ABORTED"
	entry := st.entries[idx]
	if failed && entry.FailurePolicy != "skip" {
		st.status = "halted"
		markRemainingSkipped(st, idx+1)
		run := st.toStorageRun()
		delete(tr.seqPlanRuns, runID)
		tr.seqMu.Unlock()
		tr.persistExecutionPlanRun(run)
		tr.broadcastExecutionPlanEvent("execution_plan_entry_finished", run, &run.Entries[idx])
		tr.broadcastExecutionPlanEvent("execution_plan_run_finished", run, nil)
		return
	}
	run := st.toStorageRun()
	tr.seqMu.Unlock()
	tr.persistExecutionPlanRun(run)
	tr.broadcastExecutionPlanEvent("execution_plan_entry_finished", run, &run.Entries[idx])
	tr.launchNextPlanEntry(runID)
}

// markRemainingSkipped marks entries from index `from` onward as skipped (caller
// holds seqMu).
func markRemainingSkipped(st *seqPlanRunState, from int) {
	for i := from; i < len(st.statuses); i++ {
		if st.statuses[i].Status == "pending" {
			st.statuses[i].Status = "skipped"
			st.statuses[i].Detail = "skipped after a prior failure halted the plan"
		}
	}
}

func mapTerminalEntryStatus(terminalStatus string) string {
	switch terminalStatus {
	case "FAILED", "ABORTED":
		return "failed"
	default:
		return "complete"
	}
}

func (tr *TestRunner) persistExecutionPlanRun(run *storage.ExecutionPlanRun) {
	if tr == nil || tr.execPlanRunStore == nil || run == nil {
		return
	}
	_ = tr.execPlanRunStore.SaveExecutionPlanRun(run)
}

func (tr *TestRunner) broadcastExecutionPlanEvent(eventType string, run *storage.ExecutionPlanRun, entry *storage.ExecutionPlanRunEntry) {
	if tr == nil || tr.hub == nil || run == nil {
		return
	}
	msg := map[string]any{
		"type":       eventType,
		"plan_id":    run.PlanID,
		"run_id":     run.ID,
		"project_id": normalizeProjectID(run.ProjectID),
		"run_status": run.Status,
		"timestamp":  time.Now(),
	}
	if entry != nil {
		msg["entry_order"] = entry.Order
		msg["entry_status"] = entry.Status
		msg["scenario_id"] = entry.ScenarioID
		if entry.TestID != "" {
			msg["test_id"] = entry.TestID
		}
	}
	tr.hub.BroadcastJSON(msg)
}
