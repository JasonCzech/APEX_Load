package api

// Durable recovery for sequential execution-plan runs. The plan-run row is the
// orchestration source of truth; persisted tests provide the outcome of the one
// entry that may have been active when the prior controller stopped.

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/api/testexec"
	"github.com/jasonczech/apexload/pkg/storage"
)

type runningExecutionPlanRunPersistence interface {
	ListRunningExecutionPlanRuns() ([]*storage.ExecutionPlanRun, error)
}

var errUnrecoverableSequentialPlanRun = errors.New("sequential execution-plan recovery requires operator review")

func unrecoverableSequentialPlanRunError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errUnrecoverableSequentialPlanRun, fmt.Sprintf(format, args...))
}

type sequentialPlanRecoveryAction struct {
	runID  string
	entry  storage.ExecutionPlanEntry
	idx    int
	testID string
	test   *ManagedTest
	launch bool
	start  bool
}

// recoverSequentialExecutionPlanRuns rehydrates every durable sequential run
// still marked running. It is called before pending-test promotion at startup
// and on leadership acquisition, making the test->plan mapping visible before
// any recovered test can reach its terminal hook.
func (tr *TestRunner) recoverSequentialExecutionPlanRuns() error {
	if tr == nil || tr.execPlanRunStore == nil || !tr.isExecutionLeader() {
		return nil
	}
	runs, err := tr.runningSequentialExecutionPlanRuns()
	if err != nil {
		return executionPlanPersistenceError("load running execution-plan runs for recovery", err)
	}
	var recoveryErr error
	for _, run := range runs {
		if run == nil || run.RunMode != "sequential" || run.Status != "running" {
			continue
		}
		action, claimed, err := tr.rehydrateSequentialExecutionPlanRun(run)
		if err != nil {
			if errors.Is(err, errUnrecoverableSequentialPlanRun) {
				if haltErr := tr.haltUnrecoverableSequentialExecutionPlanRun(run, err); haltErr != nil {
					recoveryErr = errors.Join(recoveryErr, haltErr)
				}
				continue
			}
			recoveryErr = errors.Join(recoveryErr, err)
			continue
		}
		if !claimed {
			continue
		}
		if !tr.isExecutionLeader() {
			tr.finishSequentialRecoveryAction(action.runID)
			tr.discardRehydratedSequentialPlanRun(action.runID)
			recoveryErr = errors.Join(recoveryErr, errNotExecutionLeader)
			continue
		}
		if err := tr.resumeRecoveredSequentialPlanRun(action); err != nil {
			// launchNext/advance install their own idempotent retry worker before
			// returning. Other failures (notably leadership loss or failure-row
			// persistence) must discard the provisional rehydration so the outer
			// recovery retry can reconstruct and resume it again.
			if !tr.sequentialRecoveryActionHasOwnedRetry(action) {
				tr.discardRehydratedSequentialPlanRun(action.runID)
			} else {
				tr.finishSequentialRecoveryAction(action.runID)
			}
			recoveryErr = errors.Join(recoveryErr, err)
		} else {
			tr.finishSequentialRecoveryAction(action.runID)
		}
	}
	return recoveryErr
}

func (tr *TestRunner) runningSequentialExecutionPlanRuns() ([]*storage.ExecutionPlanRun, error) {
	if store, ok := tr.execPlanRunStore.(runningExecutionPlanRunPersistence); ok {
		return store.ListRunningExecutionPlanRuns()
	}
	runs, err := tr.execPlanRunStore.ListExecutionPlanRuns("", 0, 0)
	if err != nil {
		return nil, err
	}
	filtered := make([]*storage.ExecutionPlanRun, 0, len(runs))
	for _, run := range runs {
		if run != nil && run.RunMode == "sequential" && run.Status == "running" {
			filtered = append(filtered, run)
		}
	}
	return filtered, nil
}

func (tr *TestRunner) rehydrateSequentialExecutionPlanRun(run *storage.ExecutionPlanRun) (sequentialPlanRecoveryAction, bool, error) {
	if run == nil {
		return sequentialPlanRecoveryAction{}, false, nil
	}
	if strings.TrimSpace(run.ID) == "" {
		return sequentialPlanRecoveryAction{}, false, unrecoverableSequentialPlanRunError("running execution-plan row has no run id")
	}
	if strings.TrimSpace(run.PlanID) == "" {
		return sequentialPlanRecoveryAction{}, false, unrecoverableSequentialPlanRunError("execution-plan run %s has no plan id", run.ID)
	}
	statuses := cloneExecutionPlanRunEntries(run.Entries)
	entries, planName := tr.recoveredSequentialEntries(run, statuses)
	if len(entries) != len(statuses) {
		return sequentialPlanRecoveryAction{}, false, unrecoverableSequentialPlanRunError("execution-plan run %s has %d statuses but %d entry snapshots", run.ID, len(statuses), len(entries))
	}

	activeIdx := -1
	cursor := -1
	for i, status := range statuses {
		switch status.Status {
		case "running":
			if activeIdx >= 0 {
				return sequentialPlanRecoveryAction{}, false, unrecoverableSequentialPlanRunError("execution-plan run %s has multiple running entries", run.ID)
			}
			activeIdx = i
			cursor = i
		case "pending":
		case "complete", "failed", "skipped":
			if activeIdx < 0 {
				cursor = i
			}
		default:
			return sequentialPlanRecoveryAction{}, false, unrecoverableSequentialPlanRunError("execution-plan run %s entry %d has unsupported status %q", run.ID, i, status.Status)
		}
	}
	claimedWithoutTest := activeIdx >= 0 && strings.TrimSpace(statuses[activeIdx].TestID) == ""
	if claimedWithoutTest {
		// A controller may have durably claimed an entry and stopped before it
		// created the queued test. Rewind only the in-memory cursor/status so the
		// next CAS reclaims the same entry at the durable revision; no external
		// side effect has happened yet.
		statuses[activeIdx].Status = "pending"
		cursor = activeIdx - 1
	}

	st := &seqPlanRunState{
		runID:      run.ID,
		planID:     run.PlanID,
		planName:   planName,
		projectID:  normalizeProjectID(run.ProjectID),
		entries:    entries,
		statuses:   statuses,
		cursor:     cursor,
		status:     "running",
		revision:   run.Revision,
		createdAt:  run.CreatedAt,
		rehydrated: true,
		recovering: true,
	}
	if st.createdAt.IsZero() {
		st.createdAt = run.UpdatedAt
	}
	if len(statuses) > 0 {
		st.attribSubject = statuses[0].AttribSubject
		st.attribName = statuses[0].AttribName
		if approvalID := strings.TrimSpace(statuses[0].ApprovalID); approvalID != "" && tr.execPlanScheduleStore != nil {
			approval, err := tr.execPlanScheduleStore.GetExecutionPlanApproval(approvalID)
			if err != nil {
				return sequentialPlanRecoveryAction{}, false, executionPlanPersistenceError("reload execution-plan approval during recovery", err)
			}
			if approval == nil {
				return sequentialPlanRecoveryAction{}, false, unrecoverableSequentialPlanRunError("execution-plan run %s references missing approval %s", run.ID, approvalID)
			}
			st.approval = approval
		}
	}

	tr.seqMu.Lock()
	if existing, exists := tr.seqPlanRuns[run.ID]; exists {
		if !existing.rehydrated || existing.recovering || tr.sequentialRecoveryRetryOwnedLocked(existing) {
			tr.seqMu.Unlock()
			return sequentialPlanRecoveryAction{}, false, nil
		}
		// Replace stale in-memory recovery state from the latest durable row.
		for testID, mappedRunID := range tr.seqPlanByTest {
			if mappedRunID == run.ID {
				delete(tr.seqPlanByTest, testID)
			}
		}
	}
	if tr.seqPlanRuns == nil {
		tr.seqPlanRuns = make(map[string]*seqPlanRunState)
	}
	if tr.seqPlanByTest == nil {
		tr.seqPlanByTest = make(map[string]string)
	}
	tr.seqPlanRuns[run.ID] = st
	if activeIdx >= 0 && strings.TrimSpace(statuses[activeIdx].TestID) != "" {
		tr.seqPlanByTest[statuses[activeIdx].TestID] = run.ID
	}
	tr.seqMu.Unlock()

	action := sequentialPlanRecoveryAction{runID: run.ID, idx: activeIdx}
	if activeIdx < 0 || claimedWithoutTest {
		action.launch = true
		return action, true, nil
	}
	action.entry = entries[activeIdx]
	testID := strings.TrimSpace(statuses[activeIdx].TestID)
	action.testID = testID
	if testID == "" {
		return action, true, nil
	}
	tr.populateSequentialRecoveryTest(&action)
	return action, true, nil
}

// haltUnrecoverableSequentialExecutionPlanRun quarantines one malformed durable
// row instead of returning an error that blocks all pending-test promotion. The
// terminal row and entry detail are the operator-review artifact; once saved it
// is excluded from subsequent running-run scans.
func (tr *TestRunner) haltUnrecoverableSequentialExecutionPlanRun(run *storage.ExecutionPlanRun, cause error) error {
	if run == nil {
		return nil
	}
	halted := *run
	halted.Entries = cloneExecutionPlanRunEntries(run.Entries)
	detail := "recovery halted for operator review: " + cause.Error()
	hasDetailedEntry := false
	for i := range halted.Entries {
		switch halted.Entries[i].Status {
		case "running":
			halted.Entries[i].Status = "failed"
			halted.Entries[i].Detail = detail
			hasDetailedEntry = true
		case "pending":
			halted.Entries[i].Status = "skipped"
			halted.Entries[i].Detail = "not started because recovery requires operator review"
		case "complete", "failed", "skipped":
		default:
			halted.Entries[i].Status = "failed"
			halted.Entries[i].Detail = detail
			hasDetailedEntry = true
		}
	}
	if !hasDetailedEntry && len(halted.Entries) > 0 {
		halted.Entries[0].Status = "failed"
		halted.Entries[0].Detail = detail
	}
	now := time.Now().UTC()
	halted.Status = "halted"
	halted.UpdatedAt = now
	halted.CompletedAt = &now
	if err := tr.persistExecutionPlanRunDurable(&halted); err != nil {
		return executionPlanPersistenceError("halt unrecoverable execution-plan run "+run.ID, err)
	}
	tr.discardRehydratedSequentialPlanRun(run.ID)
	tr.broadcastExecutionPlanEvent("execution_plan_run_finished", &halted, nil)
	slog.Warn("halted unrecoverable sequential execution-plan run for operator review", "run_id", run.ID, "error", cause)
	return nil
}

// recoveredSequentialEntries prefers the immutable snapshot. Rows written by a
// pre-snapshot version fall back to the current plan where available; the
// default empty FailurePolicy remains fail-closed (halt) when it is not.
func (tr *TestRunner) recoveredSequentialEntries(run *storage.ExecutionPlanRun, statuses []storage.ExecutionPlanRunEntry) ([]storage.ExecutionPlanEntry, string) {
	entries := make([]storage.ExecutionPlanEntry, len(statuses))
	planName := ""
	allSnapshotted := true
	for i, status := range statuses {
		entries[i] = executionPlanEntryFromRunEntry(status)
		if strings.TrimSpace(status.RunName) != "" {
			planName = status.RunName
		} else {
			allSnapshotted = false
		}
	}
	if allSnapshotted {
		return entries, planName
	}
	if tr.execPlanScheduleStore == nil {
		return entries, firstNonEmpty(planName, run.PlanID)
	}
	plan, err := tr.execPlanScheduleStore.GetExecutionPlan(run.PlanID)
	if err != nil || plan == nil {
		return entries, firstNonEmpty(planName, run.PlanID)
	}
	if planName == "" {
		planName = strings.TrimSpace(plan.Name)
	}
	current := enabledSortedEntries(plan.Entries)
	planTimeslotUsed := false
	for i := range statuses {
		var matched *storage.ExecutionPlanEntry
		if i < len(current) && current[i].Order == statuses[i].Order && current[i].ScenarioID == statuses[i].ScenarioID {
			matched = &current[i]
		} else {
			for j := range current {
				if current[j].Order == statuses[i].Order && current[j].ScenarioID == statuses[i].ScenarioID {
					matched = &current[j]
					break
				}
			}
		}
		if matched == nil {
			continue
		}
		entry := *matched
		entry.RuntimeOverrides = append([]byte(nil), matched.RuntimeOverrides...)
		entry.DataPools = append([]storage.ScenarioDataPoolBinding(nil), matched.DataPools...)
		entry.TimeslotID = cloneStringPtr(matched.TimeslotID)
		if trimPtr(entry.TimeslotID) == "" && !planTimeslotUsed && trimPtr(plan.TimeslotID) != "" {
			entry.TimeslotID = cloneStringPtr(plan.TimeslotID)
			planTimeslotUsed = true
		}
		entries[i] = entry
	}
	return entries, firstNonEmpty(planName, run.PlanID)
}

func (tr *TestRunner) resumeRecoveredSequentialPlanRun(action sequentialPlanRecoveryAction) error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	if action.launch {
		if err := tr.launchNextPlanEntry(action.runID); err != nil {
			tr.enqueueExecutionPlanRunAdvanceRetry(action.runID)
			return err
		}
		return nil
	}
	if action.test == nil {
		return tr.failRecoveredSequentialEntry(action, "persisted execution-plan entry references a missing test")
	}
	switch action.test.Status {
	case "COMPLETE", "FAILED", "ABORTED":
		return tr.advanceExecutionPlanForTest(action.test, action.test.Status)
	}
	if action.start {
		if err := tr.StartTest(action.test.ID); err != nil {
			if errors.Is(err, errNotExecutionLeader) {
				return err
			}
			return tr.failRecoveredSequentialEntry(action, "recovered test could not be started: "+err.Error())
		}
		return nil
	}
	switch action.test.Status {
	case "INITIALIZING", "RUNNING", "PAUSED", "PENDING", "RESERVED", "SCHEDULED", "SHAKEOUT", "SHAKEOUT_REVIEW", "STOPPING":
		return nil
	default:
		return tr.failRecoveredSequentialEntry(action, "recovered test has unsupported status "+action.test.Status)
	}
}

func (tr *TestRunner) failRecoveredSequentialEntry(action sequentialPlanRecoveryAction, detail string) error {
	halted, err := tr.recordPlanEntryFailure(action.runID, action.idx, action.entry, detail)
	if err != nil {
		return err
	}
	if action.testID != "" {
		tr.seqMu.Lock()
		if tr.seqPlanByTest[action.testID] == action.runID {
			delete(tr.seqPlanByTest, action.testID)
		}
		tr.seqMu.Unlock()
	}
	if halted {
		return nil
	}
	if err := tr.launchNextPlanEntry(action.runID); err != nil {
		tr.enqueueExecutionPlanRunAdvanceRetry(action.runID)
		return err
	}
	return nil
}

func (tr *TestRunner) populateSequentialRecoveryTest(action *sequentialPlanRecoveryAction) {
	if action == nil || action.testID == "" {
		return
	}
	tr.mu.RLock()
	action.test = tr.tests[action.testID]
	tr.mu.RUnlock()
	action.start = action.test != nil && testexec.IsStartableTestStatus(action.test.Status)
}

// sequentialRecoveryRetryOwnedLocked reports whether an existing recovery
// state is currently owned by one of the orchestrator's narrower retry workers.
// seqMu must be held.
func (tr *TestRunner) sequentialRecoveryRetryOwnedLocked(st *seqPlanRunState) bool {
	if st == nil {
		return false
	}
	if _, ok := tr.seqPlanRetrying["run:"+st.runID]; ok {
		return true
	}
	for _, entry := range st.statuses {
		if entry.TestID != "" {
			if _, ok := tr.seqPlanRetrying["test:"+entry.TestID]; ok {
				return true
			}
		}
	}
	return false
}

func (tr *TestRunner) finishSequentialRecoveryAction(runID string) {
	tr.seqMu.Lock()
	if st := tr.seqPlanRuns[runID]; st != nil {
		st.recovering = false
	}
	tr.seqMu.Unlock()
}

func (tr *TestRunner) sequentialRecoveryActionHasOwnedRetry(action sequentialPlanRecoveryAction) bool {
	tr.seqMu.Lock()
	defer tr.seqMu.Unlock()
	if _, ok := tr.seqPlanRetrying["run:"+action.runID]; ok {
		return true
	}
	if action.test != nil {
		_, ok := tr.seqPlanRetrying["test:"+action.test.ID]
		return ok
	}
	return false
}

func (tr *TestRunner) discardRehydratedSequentialPlanRun(runID string) {
	tr.seqMu.Lock()
	delete(tr.seqPlanRuns, runID)
	for testID, mappedRunID := range tr.seqPlanByTest {
		if mappedRunID == runID {
			delete(tr.seqPlanByTest, testID)
		}
	}
	tr.seqMu.Unlock()
}

func cloneExecutionPlanRunEntries(entries []storage.ExecutionPlanRunEntry) []storage.ExecutionPlanRunEntry {
	cloned := make([]storage.ExecutionPlanRunEntry, len(entries))
	for i, entry := range entries {
		cloned[i] = entry
		cloned[i].RuntimeOverrides = append([]byte(nil), entry.RuntimeOverrides...)
		cloned[i].DataPools = append([]storage.ScenarioDataPoolBinding(nil), entry.DataPools...)
		cloned[i].TimeslotID = cloneStringPtr(entry.TimeslotID)
	}
	return cloned
}

func (tr *TestRunner) enqueueSequentialExecutionPlanRecoveryRetry() {
	const key = "recovery"
	if !tr.beginExecutionPlanRetry(key) {
		return
	}
	go func() {
		defer tr.endExecutionPlanRetry(key)
		delay := executionPlanRetryInitialDelay
		for tr.executionPlanRetryAllowed() {
			if !tr.waitExecutionPlanRetry(delay) {
				return
			}
			if err := tr.recoverSequentialExecutionPlanRuns(); err == nil {
				tr.promotePendingTests()
				return
			} else {
				slog.Warn("sequential execution-plan recovery retry failed", "error", err)
			}
			delay = min(delay*2, time.Second)
		}
	}()
}
