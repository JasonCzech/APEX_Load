package api

// Sequential execution-plan orchestration. A "sequential" execution plan run is
// dependency-gated: the first enabled entry is created+started, and each
// subsequent entry is created+started only after the previous entry's run
// reaches a terminal state (COMPLETE/FAILED/ABORTED), honoring each entry's
// FailurePolicy (halt stops the sequence, skip continues). Live state is held in
// memory on the TestRunner (consistent with tr.tests) and the plan-run row is
// persisted at every transition, including an immutable entry-policy snapshot,
// so an in-flight sequence can be rehydrated after restart or HA takeover.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

var errExecutionPlanRunPersistence = errors.New("execution-plan run persistence failed")
var errExecutionPlanRunConflict = errors.New("execution-plan run revision conflict")
var errExecutionPlanRunCommitUnknown = errors.New("execution-plan run commit outcome is unknown")

func executionPlanPersistenceError(action string, err error) error {
	return fmt.Errorf("%w: %s: %w", errExecutionPlanRunPersistence, action, err)
}

// executionPlanRunPersistence persists plan-run progress snapshots.
type executionPlanRunPersistence interface {
	SaveExecutionPlanRun(run *storage.ExecutionPlanRun) error
	GetExecutionPlanRun(id string) (*storage.ExecutionPlanRun, error)
	ListExecutionPlanRuns(planID string, limit int, offset int) ([]*storage.ExecutionPlanRun, error)
}

type atomicExecutionPlanRunPersistence interface {
	CreateExecutionPlanRunIfAbsent(run *storage.ExecutionPlanRun) (bool, error)
	CompareAndSwapExecutionPlanRun(run *storage.ExecutionPlanRun, expectedRevision int64) (bool, error)
}

// executionPlanSchedulePersistence is the slice of storage.Store the
// execution-plan scheduler + CRUD use: load plans to materialize, persist
// schedules and their occurrences.
type executionPlanSchedulePersistence interface {
	GetExecutionPlan(id string) (*storage.ExecutionPlan, error)
	SaveExecutionPlanSchedule(schedule *storage.ExecutionPlanSchedule) error
	GetExecutionPlanSchedule(id string) (*storage.ExecutionPlanSchedule, error)
	ListExecutionPlanSchedules(planID string) ([]*storage.ExecutionPlanSchedule, error)
	ListAllExecutionPlanSchedules() ([]*storage.ExecutionPlanSchedule, error)
	DeleteExecutionPlanSchedule(id string) error
	CreateExecutionPlanScheduleOccurrenceIfAbsent(occurrence *storage.ExecutionPlanScheduleOccurrence) (bool, error)
	SaveExecutionPlanScheduleOccurrence(occurrence *storage.ExecutionPlanScheduleOccurrence) error
	GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(scheduleID string, plannedAt time.Time) (*storage.ExecutionPlanScheduleOccurrence, error)
	ListExecutionPlanScheduleOccurrences(scheduleID string) ([]*storage.ExecutionPlanScheduleOccurrence, error)
	SaveExecutionPlanApproval(approval *storage.ExecutionPlanApproval) error
	GetExecutionPlanApproval(id string) (*storage.ExecutionPlanApproval, error)
	ListExecutionPlanApprovals(planID string) ([]*storage.ExecutionPlanApproval, error)
	ClaimExecutionPlanApproval(id, runID string, updatedAt time.Time) (bool, error)
	UpdateExecutionPlanApprovalResolution(id, toStatus, resolvedBy, reason string, resolvedAt time.Time) (bool, error)
}

// seqPlanRunState is the in-memory orchestration state for one sequential plan
// run. Guarded by TestRunner.seqMu — never tr.mu, because advancing calls
// CreateTest/StartTest which take tr.mu, so seqMu is always released first.
type seqPlanRunState struct {
	runID         string
	planName      string
	projectID     string
	timeslot      *string                         // plan-level fallback timeslot
	timeslotUsed  bool                            // plan-level timeslot is bound to at most one entry
	entries       []storage.ExecutionPlanEntry    // enabled, sorted snapshot
	statuses      []storage.ExecutionPlanRunEntry // parallel to entries
	cursor        int                             // index of the active entry; -1 before start
	planID        string
	status        string
	revision      int64
	createdAt     time.Time
	attribSubject string
	attribName    string
	creationActor testCreationActor
	approval      *storage.ExecutionPlanApproval
	leaseTerm     executionLeaseTerm
	rehydrated    bool // restored from durable storage rather than materialized in this process
	recovering    bool // one recovery/resume action is currently claimed
	// stopDetail is set once Stop plan has been asked for: no further entry
	// starts, and the run ends halted when its running entry ends.
	stopDetail string
}

func (st *seqPlanRunState) toStorageRun() *storage.ExecutionPlanRun {
	run := &storage.ExecutionPlanRun{
		ID:        st.runID,
		PlanID:    st.planID,
		ProjectID: st.projectID,
		RunMode:   "sequential",
		Status:    st.status,
		Entries:   append([]storage.ExecutionPlanRunEntry(nil), st.statuses...),
		Revision:  st.revision,
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
// pools because createTestRequestFromScenario only fills omitted DataPools), and an
// optional timeslot (entry-level, else the plan-level fallback). Shared by both the
// sequential orchestrator and the parallel materialize path.
func buildPlanEntryCreateRequest(planName, projectID string, planTimeslot *string, entry storage.ExecutionPlanEntry, actors ...testCreationActor) (CreateTestRequest, error) {
	req := CreateTestRequest{
		Name:       strings.TrimSpace(planName + " / " + executionEntryLabel(entry)),
		ScenarioID: entry.ScenarioID,
		ProjectID:  projectID,
	}
	if len(entry.RuntimeOverrides) > 0 {
		var parsed *RuntimeSettingsOverride
		if err := json.Unmarshal(entry.RuntimeOverrides, &parsed); err != nil {
			return CreateTestRequest{}, fmt.Errorf("decode runtime overrides for scenario %s: %w", entry.ScenarioID, err)
		}
		req.RuntimeSettings = parsed
	}
	if len(entry.DataPools) > 0 {
		req.DataPools = append([]storage.ScenarioDataPoolBinding(nil), entry.DataPools...)
		// Plan entries are durable snapshots. Older rows may carry access-mode
		// labels that always degraded to read-only at execution; the build boundary
		// canonicalizes those legacy labels while plan create/update rejects them.
		req.allowLegacyDataPoolModes = true
	}
	if ts := firstNonEmpty(trimPtr(entry.TimeslotID), trimPtr(planTimeslot)); ts != "" {
		req.TimeslotID = &ts
	}
	if len(actors) > 0 {
		req.creationActor = normalizeTestCreationActor(actors[0])
	}
	return req, nil
}

func trimPtr(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

func executionPlanRunEntrySnapshot(entry storage.ExecutionPlanEntry, runName, attribSubject, attribName string, effectiveTimeslot *string, approval *storage.ExecutionPlanApproval, actors ...testCreationActor) storage.ExecutionPlanRunEntry {
	actor := executionPlanCreationActor(attribSubject, attribName, actors...)
	return storage.ExecutionPlanRunEntry{
		Order:               entry.Order,
		ScenarioID:          entry.ScenarioID,
		ScenarioName:        entry.ScenarioName,
		Status:              "pending",
		FailurePolicy:       entry.FailurePolicy,
		RuntimeOverrides:    append(json.RawMessage(nil), entry.RuntimeOverrides...),
		DataPools:           append([]storage.ScenarioDataPoolBinding(nil), entry.DataPools...),
		TimeslotID:          cloneStringPtr(effectiveTimeslot),
		RunName:             runName,
		AttribSubject:       strings.TrimSpace(attribSubject),
		AttribName:          strings.TrimSpace(attribName),
		ActorType:           actor.Type,
		ActorSubject:        actor.Subject,
		ActorDisplayName:    actor.DisplayName,
		ActivityInitiatedBy: actor.InitiatedBy,
		ApprovalID:          approvalID(approval),
	}
}

func executionPlanCreationActor(attribSubject, attribName string, actors ...testCreationActor) testCreationActor {
	if len(actors) > 0 {
		return normalizeTestCreationActor(actors[0])
	}
	// Attribution text alone cannot distinguish a user from a service account.
	// Callers that may project plaintext credentials must supply the authenticated
	// actor explicitly; omitted provenance remains usable for non-secret runs.
	return normalizeTestCreationActor(testCreationActor{
		Type:        "legacy",
		Subject:     firstNonEmpty(attribSubject, "controller:execution-plan-unattributed"),
		DisplayName: attribName,
	})
}

func executionPlanRunEntryCreationActor(entry storage.ExecutionPlanRunEntry) testCreationActor {
	if strings.TrimSpace(entry.ActorType) != "" || strings.TrimSpace(entry.ActorSubject) != "" {
		return normalizeTestCreationActor(testCreationActor{
			Type:        entry.ActorType,
			Subject:     entry.ActorSubject,
			DisplayName: entry.ActorDisplayName,
			InitiatedBy: entry.ActivityInitiatedBy,
		})
	}
	// A legacy row cannot prove whether its attribution subject was a user or a
	// service account. Preserve the subject for diagnostics but mark the authority
	// unknown so later entries cannot inherit system-level secret-write power.
	return normalizeTestCreationActor(testCreationActor{
		Type:        "legacy",
		Subject:     firstNonEmpty(entry.AttribSubject, "controller:execution-plan-legacy"),
		DisplayName: entry.AttribName,
	})
}

// preflightExecutionPlanRuntimeSecretAuthority rejects a run whose actor lacks
// project-secret write authority before approval claims, run anchors, provider
// calls, or test side effects. The
// create boundary enforces the same rule again, so a concurrent scenario change
// still fails closed after this early authorization check.
func (tr *TestRunner) preflightExecutionPlanRuntimeSecretAuthority(plan *storage.ExecutionPlan, runName, runID string, actor testCreationActor) error {
	actor = normalizeTestCreationActor(actor)
	actorErr := runtimeSecretWriteActorError(actor)
	if actorErr == nil || plan == nil {
		return nil
	}
	entries := enabledSortedEntries(plan.Entries)
	planTimeslotUsed := false
	for index, entry := range entries {
		var planTimeslot *string
		if trimPtr(entry.TimeslotID) == "" && !planTimeslotUsed && trimPtr(plan.TimeslotID) != "" {
			planTimeslot = plan.TimeslotID
			planTimeslotUsed = true
		}
		req, err := buildPlanEntryCreateRequest(runName, plan.ProjectID, planTimeslot, entry, actor)
		if err != nil {
			// Preserve the materializer's existing validation/governance ordering.
			// The authoritative create boundary still enforces actor authority if a
			// malformed entry later reaches runtime-secret persistence.
			continue
		}
		preview, _, revision, err := tr.buildManagedTestFromSnapshot(req, executionPlanEntryTestID(runID, index, entry.Order), "DRAFT")
		if err != nil || !tr.managedTestBuildFenceCurrent(revision) {
			continue
		}
		for _, group := range preview.Groups {
			if len(group.runtimeSecretValues) > 0 {
				return actorErr
			}
		}
	}
	return nil
}

func executionPlanEntryFromRunEntry(entry storage.ExecutionPlanRunEntry) storage.ExecutionPlanEntry {
	return storage.ExecutionPlanEntry{
		Order:            entry.Order,
		ScenarioID:       entry.ScenarioID,
		ScenarioName:     entry.ScenarioName,
		RuntimeOverrides: append(json.RawMessage(nil), entry.RuntimeOverrides...),
		FailurePolicy:    entry.FailurePolicy,
		DataPools:        append([]storage.ScenarioDataPoolBinding(nil), entry.DataPools...),
		TimeslotID:       cloneStringPtr(entry.TimeslotID),
	}
}

func executionPlanEntryTestID(runID string, entryIndex, entryOrder int) string {
	seed := fmt.Sprintf("apexload:execution-plan-run:%s:entry:%d:order:%d", strings.TrimSpace(runID), entryIndex, entryOrder)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(seed)).String()
}

func executionPlanEntryMaterializationKey(runID string, entryIndex, entryOrder int) string {
	seed := fmt.Sprintf("apexload:execution-plan-materialization:%s:entry:%d:order:%d", strings.TrimSpace(runID), entryIndex, entryOrder)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(seed)).String()
}

// StartExecutionPlanRun launches a sequential execution plan as a gated run and
// returns the initial run snapshot. The plan's enabled entries are snapshotted so
// a mid-run edit to the plan does not change an in-flight run.
func (tr *TestRunner) StartExecutionPlanRun(plan *storage.ExecutionPlan, attribSubject, attribName string, approval ...*storage.ExecutionPlanApproval) (*storage.ExecutionPlanRun, error) {
	return tr.startExecutionPlanRun(plan, attribSubject, attribName, "", approval...)
}

func (tr *TestRunner) startExecutionPlanRun(plan *storage.ExecutionPlan, attribSubject, attribName, runNameOverride string, approval ...*storage.ExecutionPlanApproval) (*storage.ExecutionPlanRun, error) {
	return tr.startExecutionPlanRunWithID(plan, attribSubject, attribName, runNameOverride, "", approval...)
}

func (tr *TestRunner) startExecutionPlanRunWithID(plan *storage.ExecutionPlan, attribSubject, attribName, runNameOverride, runIDOverride string, approval ...*storage.ExecutionPlanApproval) (*storage.ExecutionPlanRun, error) {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return nil, err
	}
	return tr.startExecutionPlanRunWithIDAndTerm(plan, attribSubject, attribName, runNameOverride, runIDOverride, leaseTerm, approval...)
}

func (tr *TestRunner) startExecutionPlanRunWithIDAndTerm(plan *storage.ExecutionPlan, attribSubject, attribName, runNameOverride, runIDOverride string, leaseTerm executionLeaseTerm, approval ...*storage.ExecutionPlanApproval) (*storage.ExecutionPlanRun, error) {
	return tr.startExecutionPlanRunWithIDAndTermActor(plan, attribSubject, attribName, runNameOverride, runIDOverride, leaseTerm, executionPlanCreationActor(attribSubject, attribName), approval...)
}

func (tr *TestRunner) startExecutionPlanRunWithIDAndTermActor(plan *storage.ExecutionPlan, attribSubject, attribName, runNameOverride, runIDOverride string, leaseTerm executionLeaseTerm, actor testCreationActor, approval ...*storage.ExecutionPlanApproval) (*storage.ExecutionPlanRun, error) {
	if plan == nil {
		return nil, fmt.Errorf("execution plan is required")
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return nil, errNotExecutionLeader
	}
	runName := strings.TrimSpace(runNameOverride)
	if runName == "" {
		runName = strings.TrimSpace(plan.Name)
	}
	enabled := enabledSortedEntries(plan.Entries)
	now := time.Now().UTC()
	statuses := make([]storage.ExecutionPlanRunEntry, len(enabled))
	var runApproval *storage.ExecutionPlanApproval
	if len(approval) > 0 {
		runApproval = approval[0]
	}
	planTimeslotUsed := false
	applicationID, releaseID := executionPlanReleaseContext(plan, nil)
	for i, e := range enabled {
		effectiveTimeslot := e.TimeslotID
		if trimPtr(effectiveTimeslot) == "" && !planTimeslotUsed && trimPtr(plan.TimeslotID) != "" {
			effectiveTimeslot = plan.TimeslotID
			planTimeslotUsed = true
		}
		statuses[i] = executionPlanRunEntrySnapshot(e, runName, attribSubject, attribName, effectiveTimeslot, runApproval, actor)
		statuses[i].PlanName = strings.TrimSpace(plan.Name)
		statuses[i].ApplicationID, statuses[i].ReleaseID = applicationID, releaseID
	}
	runID := strings.TrimSpace(runIDOverride)
	if runID == "" {
		runID = "exec-plan-run-" + uuid.New().String()
	} else if tr.execPlanRunStore != nil {
		existing, err := tr.execPlanRunStore.GetExecutionPlanRun(runID)
		switch {
		case err == nil && existing != nil:
			if existing.PlanID != plan.ID || normalizeProjectID(existing.ProjectID) != normalizeProjectID(plan.ProjectID) || existing.RunMode != "sequential" {
				return nil, fmt.Errorf("execution-plan run id %q is already used by another materialization", runID)
			}
			return existing, nil
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return nil, executionPlanPersistenceError("check preassigned execution-plan run id", err)
		}
	}
	for i := range statuses {
		statuses[i].MaterializationKey = executionPlanEntryMaterializationKey(runID, i, statuses[i].Order)
	}
	st := &seqPlanRunState{
		runID:         runID,
		planID:        plan.ID,
		planName:      runName,
		projectID:     normalizeProjectID(plan.ProjectID),
		timeslot:      plan.TimeslotID,
		entries:       enabled,
		statuses:      statuses,
		cursor:        -1,
		status:        "running",
		createdAt:     now,
		attribSubject: attribSubject,
		attribName:    attribName,
		creationActor: normalizeTestCreationActor(actor),
		leaseTerm:     leaseTerm,
	}
	st.approval = runApproval

	if len(enabled) == 0 {
		st.status = "completed"
		run := st.toStorageRun()
		created, err := tr.createExecutionPlanRunDurableWithTerm(run, leaseTerm)
		if err != nil {
			return nil, executionPlanPersistenceError("persist completed execution-plan run", err)
		}
		if !created {
			return tr.loadExistingExecutionPlanRun(runID, plan.ID, plan.ProjectID, "sequential")
		}
		st.revision = run.Revision
		return run, nil
	}

	initialRun := st.toStorageRun()
	created, err := tr.createExecutionPlanRunDurableWithTerm(initialRun, leaseTerm)
	if err != nil {
		return nil, executionPlanPersistenceError("persist execution-plan run before launch", err)
	}
	if !created {
		return tr.loadExistingExecutionPlanRun(runID, plan.ID, plan.ProjectID, "sequential")
	}
	st.revision = initialRun.Revision
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
	if err := tr.launchNextPlanEntry(st.runID); err != nil {
		if errors.Is(err, errExecutionPlanRunCommitUnknown) {
			// The queued entry is deliberately retained while its CAS outcome is
			// reconciled in the background. Terminalizing or deleting it here could
			// destroy a transition that the database actually committed.
			tr.enqueueExecutionPlanRunAdvanceRetryWithTerm(st.runID, leaseTerm)
			return nil, err
		}
		if reconcileErr := tr.failExecutionPlanRunDurablyWithTerm(st.runID, err.Error(), leaseTerm); reconcileErr != nil {
			tr.enqueueFailedExecutionPlanRunRetryWithTerm(st.runID, err.Error(), leaseTerm)
			slog.Error("failed to durably terminalize execution-plan run after launch transition failure", "run_id", st.runID, "error", reconcileErr)
		}
		return nil, err
	}

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

func (tr *TestRunner) failExecutionPlanRunDurably(runID, detail string) error {
	leaseTerm, ok := tr.executionPlanRunLeaseTerm(runID)
	if !ok {
		return errNotExecutionLeader
	}
	return tr.failExecutionPlanRunDurablyWithTerm(runID, detail, leaseTerm)
}

func (tr *TestRunner) failExecutionPlanRunDurablyWithTerm(runID, detail string, leaseTerm executionLeaseTerm) error {
	tr.seqMu.Lock()
	st := tr.seqPlanRuns[runID]
	if st == nil {
		tr.seqMu.Unlock()
		return nil
	}
	if st.leaseTerm != leaseTerm || !tr.executionLeaseTermCurrent(leaseTerm) {
		tr.seqMu.Unlock()
		return errNotExecutionLeader
	}
	previousStatus := st.status
	previousEntries := append([]storage.ExecutionPlanRunEntry(nil), st.statuses...)
	createdTestIDs := make([]string, 0)
	st.status = "failed"
	for i := range st.statuses {
		if st.statuses[i].Status == "pending" || st.statuses[i].Status == "running" {
			st.statuses[i].Status = "skipped"
			st.statuses[i].Detail = "not launched because execution-plan transition persistence failed: " + detail
		}
		if st.statuses[i].TestID != "" {
			createdTestIDs = append(createdTestIDs, st.statuses[i].TestID)
			delete(tr.seqPlanByTest, st.statuses[i].TestID)
		}
	}
	run := st.toStorageRun()
	if err := tr.persistExecutionPlanRunDurableWithTerm(run, st.leaseTerm); err != nil {
		st.status = previousStatus
		st.statuses = previousEntries
		for _, entry := range previousEntries {
			if entry.TestID != "" && entry.Status == "running" {
				tr.seqPlanByTest[entry.TestID] = runID
			}
		}
		tr.seqMu.Unlock()
		return executionPlanPersistenceError("persist failed execution-plan run after launch transition failure", err)
	}
	st.revision = run.Revision
	delete(tr.seqPlanRuns, runID)
	tr.seqMu.Unlock()
	for _, testID := range createdTestIDs {
		if _, err := tr.DeleteTestWithError(testID); err != nil {
			slog.Warn("failed to clean up test after execution-plan launch transition failure", "run_id", runID, "test_id", testID, "error", err)
		}
	}
	tr.broadcastExecutionPlanEvent("execution_plan_run_finished", run, nil)
	return nil
}

func (tr *TestRunner) enqueueFailedExecutionPlanRunRetry(runID, detail string) {
	leaseTerm, ok := tr.executionPlanRunLeaseTerm(runID)
	if !ok {
		return
	}
	tr.enqueueFailedExecutionPlanRunRetryWithTerm(runID, detail, leaseTerm)
}

func (tr *TestRunner) enqueueFailedExecutionPlanRunRetryWithTerm(runID, detail string, leaseTerm executionLeaseTerm) {
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return
	}
	key := "failed-run:" + runID
	if !tr.beginExecutionPlanRetry(key) {
		return
	}
	go func() {
		defer tr.endExecutionPlanRetry(key)
		delay := executionPlanRetryInitialDelay
		for tr.executionPlanRetryAllowed(leaseTerm) {
			if !tr.waitExecutionPlanRetry(delay, leaseTerm) {
				return
			}
			if err := tr.failExecutionPlanRunDurablyWithTerm(runID, detail, leaseTerm); err == nil {
				return
			} else {
				slog.Warn("failed execution-plan run terminalization retry failed", "run_id", runID, "error", err)
				if !durableBackgroundRetryable(err) {
					return
				}
			}
			delay = min(delay*2, executionPlanRetryMaxDelay)
		}
	}()
}

func (tr *TestRunner) enqueueExecutionPlanRunRecordRetry(run *storage.ExecutionPlanRun) {
	tr.enqueueExecutionPlanRunRecordRetryAfter(run, nil)
}

func (tr *TestRunner) enqueueExecutionPlanRunRecordRetryAfter(run *storage.ExecutionPlanRun, afterCommit func()) {
	if run == nil {
		return
	}
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return
	}
	tr.enqueueExecutionPlanRunRecordRetryAfterWithTerm(run, afterCommit, leaseTerm)
}

func (tr *TestRunner) enqueueExecutionPlanRunRecordRetryAfterWithTerm(run *storage.ExecutionPlanRun, afterCommit func(), leaseTerm executionLeaseTerm) {
	if run == nil || !tr.executionLeaseTermCurrent(leaseTerm) {
		return
	}
	snapshot := cloneExecutionPlanRunRecord(run)
	key := "run-record:" + snapshot.ID
	if !tr.beginExecutionPlanRetry(key) {
		return
	}
	go func() {
		defer tr.endExecutionPlanRetry(key)
		delay := executionPlanRetryInitialDelay
		for tr.executionPlanRetryAllowed(leaseTerm) {
			if !tr.waitExecutionPlanRetry(delay, leaseTerm) {
				return
			}
			if err := tr.persistExecutionPlanRunDurableWithTerm(snapshot, leaseTerm); err == nil {
				if afterCommit != nil {
					afterCommit()
				}
				return
			} else {
				slog.Warn("execution-plan run record retry failed", "run_id", snapshot.ID, "status", snapshot.Status, "error", err)
				if !durableBackgroundRetryable(err) {
					return
				}
			}
			delay = min(delay*2, executionPlanRetryMaxDelay)
		}
	}()
}

func cloneExecutionPlanRunRecord(run *storage.ExecutionPlanRun) *storage.ExecutionPlanRun {
	if run == nil {
		return nil
	}
	clone := *run
	clone.Entries = append([]storage.ExecutionPlanRunEntry(nil), run.Entries...)
	clone.CompletedAt = cloneTimePtr(run.CompletedAt)
	return &clone
}

// launchNextPlanEntry advances the cursor and launches the next entry, looping
// past entries that fail to even create when the policy is skip. It returns once
// an entry is launched (waiting for completion to advance) or the run finishes.
func (tr *TestRunner) launchNextPlanEntry(runID string) error {
	leaseTerm, ok := tr.executionPlanRunLeaseTerm(runID)
	if !ok {
		return errNotExecutionLeader
	}
	return tr.launchNextPlanEntryWithTerm(runID, leaseTerm)
}

func (tr *TestRunner) launchNextPlanEntryWithTerm(runID string, leaseTerm executionLeaseTerm) error {
	for {
		tr.seqMu.Lock()
		st, ok := tr.seqPlanRuns[runID]
		if !ok {
			tr.seqMu.Unlock()
			return nil
		}
		if st.leaseTerm != leaseTerm || !tr.executionLeaseTermCurrent(leaseTerm) {
			tr.seqMu.Unlock()
			return errNotExecutionLeader
		}
		previousCursor := st.cursor
		previousTimeslotUsed := st.timeslotUsed
		st.cursor++
		// Entries a Stop plan request skipped are never launched, nor is any
		// entry once the plan was stopped (one rewound by recovery included).
		for st.cursor < len(st.entries) && st.statuses[st.cursor].Status == "skipped" {
			st.cursor++
		}
		if st.stopDetail != "" {
			for i := st.cursor; i < len(st.statuses); i++ {
				if st.statuses[i].Status == "pending" {
					st.statuses[i].Status = "skipped"
					st.statuses[i].Detail = st.stopDetail
					st.statuses[i].Stopped = true
				}
			}
			st.cursor = len(st.entries)
		}
		if st.cursor >= len(st.entries) {
			previousStatus := st.status
			st.status = "completed"
			if st.stopDetail != "" || executionPlanRunStopped(st.statuses) {
				st.status = "halted"
			}
			run := st.toStorageRun()
			if err := tr.persistExecutionPlanRunDurableWithTerm(run, st.leaseTerm); err != nil {
				st.cursor = previousCursor
				st.status = previousStatus
				tr.seqMu.Unlock()
				return executionPlanPersistenceError("persist completed execution-plan transition", err)
			}
			st.revision = run.Revision
			delete(tr.seqPlanRuns, runID)
			tr.seqMu.Unlock()
			tr.broadcastExecutionPlanEvent("execution_plan_run_finished", run, nil)
			return nil
		}
		idx := st.cursor
		entry := st.entries[idx]
		previousEntry := st.statuses[idx]
		st.statuses[idx].Status = "running"
		st.statuses[idx].TestID = executionPlanEntryTestID(runID, idx, entry.Order)
		testID := st.statuses[idx].TestID
		materializationKey := st.statuses[idx].MaterializationKey
		planID, planName, projectID := st.planID, st.planName, st.projectID
		var timeslot *string
		if trimPtr(entry.TimeslotID) == "" && !st.timeslotUsed && trimPtr(st.timeslot) != "" {
			timeslot = st.timeslot
			st.timeslotUsed = true
		}
		subject, displayName, approval := st.attribSubject, st.attribName, st.approval
		creationActor := st.creationActor
		// Claim this entry durably before creating its queued test. This is the
		// side-effect fence for HA recovery: only the controller that advances the
		// run revision from pending to running may materialize the entry.
		claimRun := st.toStorageRun()
		if err := tr.persistExecutionPlanRunDurableWithTerm(claimRun, st.leaseTerm); err != nil {
			st.cursor = previousCursor
			st.timeslotUsed = previousTimeslotUsed
			st.statuses[idx] = previousEntry
			tr.seqMu.Unlock()
			return executionPlanPersistenceError("claim execution-plan entry before materialization", err)
		}
		st.revision = claimRun.Revision
		entryContext := executionPlanRunEntryContext(claimRun, claimRun.Entries[idx].PlanName, idx)
		tr.seqMu.Unlock()
		if tr.afterExecutionPlanEntryClaim != nil {
			tr.afterExecutionPlanEntryClaim()
		}

		err := tr.validateExecutionPlanApprovalMaterialization(planID, runID, approval)
		var req CreateTestRequest
		if err == nil {
			req, err = buildPlanEntryCreateRequest(planName, projectID, timeslot, entry, creationActor)
			if err == nil {
				req.MaterializationKey = materializationKey
				req.ExecutionPlanApproval = approval
				entryContext.apply(&req, entry.Order)
			}
		}
		var test *ManagedTest
		if err == nil {
			test, err = tr.createTestWithIDAndTerm(req, false, testID, leaseTerm)
		}
		if err == nil {
			err = tr.validateExecutionPlanApprovalMaterialization(planID, runID, approval)
		}
		if err != nil {
			if errors.Is(err, errNotExecutionLeader) || !tr.executionLeaseTermCurrent(leaseTerm) {
				return errNotExecutionLeader
			}
			if test != nil {
				if failErr := tr.failExecutionPlanStagedTestWithTerm(testID, err.Error(), leaseTerm, test.MaterializationKey); failErr != nil {
					return errors.Join(err, failErr)
				}
			}
			halted, recordErr := tr.recordPlanEntryFailureWithTerm(runID, idx, entry, err.Error(), leaseTerm)
			if recordErr != nil {
				tr.seqMu.Lock()
				if current := tr.seqPlanRuns[runID]; current != nil && idx < len(current.statuses) {
					current.cursor = previousCursor
					current.timeslotUsed = previousTimeslotUsed
					current.statuses[idx] = previousEntry
				}
				tr.seqMu.Unlock()
				return recordErr
			}
			if halted {
				return nil
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
			if !tr.executionLeaseTermCurrent(leaseTerm) {
				return errNotExecutionLeader
			}
			cleanupErr := tr.failExecutionPlanStagedTestWithTerm(test.ID, "execution-plan run is no longer active", leaseTerm, test.MaterializationKey)
			return errors.Join(fmt.Errorf("execution-plan run %s is no longer active", runID), cleanupErr)
		}
		if st.leaseTerm != leaseTerm || st.statuses[idx].TestID != test.ID || !tr.executionLeaseTermCurrent(leaseTerm) {
			tr.seqMu.Unlock()
			return errNotExecutionLeader
		}
		if st.stopDetail != "" {
			// Stop plan arrived while this entry's run was being created: cancel
			// it before it starts, and end the plan run.
			tr.seqMu.Unlock()
			cleanupErr := tr.endExecutionPlanStagedTestWithTerm(test.ID, "ABORTED", "execution plan stopped before this run started", leaseTerm, test.MaterializationKey)
			if err := tr.skipStoppedPlanEntryWithTerm(runID, idx, leaseTerm); err != nil {
				return errors.Join(err, cleanupErr)
			}
			if cleanupErr != nil {
				return cleanupErr
			}
			continue
		}
		tr.seqPlanByTest[test.ID] = runID
		run := st.toStorageRun()
		tr.seqMu.Unlock()

		tr.broadcastEvent("test_created", cloneManagedTestLifecycleEvent(test))
		tr.broadcastExecutionPlanEvent("execution_plan_entry_started", run, &run.Entries[idx])
		if subject != "" {
			tr.AttributeTestCreated(test.ID, subject, displayName)
		}
		tr.AttributeTestExecutionPlanApproval(test.ID, approval)

		if !tr.executionLeaseTermCurrent(leaseTerm) {
			return errNotExecutionLeader
		}
		if err := tr.startExecutionPlanTestWithTerm(test.ID, leaseTerm); err != nil {
			if errors.Is(err, errNotExecutionLeader) || !tr.executionLeaseTermCurrent(leaseTerm) {
				return errNotExecutionLeader
			}
			// StartTest errors only before spawning the run goroutine, so no
			// terminal hook can have consumed the mapping yet. Keep the durable
			// TestID correlation, terminalize the queued row, and apply policy.
			tr.seqMu.Lock()
			delete(tr.seqPlanByTest, test.ID)
			tr.seqMu.Unlock()
			if failErr := tr.failExecutionPlanStagedTestWithTerm(test.ID, err.Error(), leaseTerm, test.MaterializationKey); failErr != nil {
				return errors.Join(err, failErr)
			}
			halted, recordErr := tr.recordPlanEntryFailureWithTerm(runID, idx, entry, err.Error(), leaseTerm)
			if recordErr != nil {
				tr.seqMu.Lock()
				if current := tr.seqPlanRuns[runID]; current != nil && idx < len(current.statuses) {
					tr.seqPlanByTest[test.ID] = runID
				}
				tr.seqMu.Unlock()
				return recordErr
			}
			if halted {
				return nil
			}
			continue // skip → try the next entry
		}
		return nil
	}
}

// recordPlanEntryFailure marks entry idx failed and applies its failure policy.
// Returns true if the run halted (caller should stop), false to continue (skip).
func (tr *TestRunner) recordPlanEntryFailure(runID string, idx int, entry storage.ExecutionPlanEntry, detail string) (bool, error) {
	leaseTerm, ok := tr.executionPlanRunLeaseTerm(runID)
	if !ok {
		return true, errNotExecutionLeader
	}
	return tr.recordPlanEntryFailureWithTerm(runID, idx, entry, detail, leaseTerm)
}

func (tr *TestRunner) recordPlanEntryFailureWithTerm(runID string, idx int, entry storage.ExecutionPlanEntry, detail string, leaseTerm executionLeaseTerm) (bool, error) {
	tr.seqMu.Lock()
	st, ok := tr.seqPlanRuns[runID]
	if !ok {
		tr.seqMu.Unlock()
		return true, nil
	}
	if st.leaseTerm != leaseTerm || !tr.executionLeaseTermCurrent(leaseTerm) {
		tr.seqMu.Unlock()
		return true, errNotExecutionLeader
	}
	previousStatus := st.status
	previousEntries := append([]storage.ExecutionPlanRunEntry(nil), st.statuses...)
	st.statuses[idx].Status = "failed"
	st.statuses[idx].Detail = detail
	halted := entry.FailurePolicy != "skip"
	if halted {
		st.status = "halted"
		markRemainingHalted(st, idx+1)
	}
	run := st.toStorageRun()
	if err := tr.persistExecutionPlanRunDurableWithTerm(run, st.leaseTerm); err != nil {
		st.status = previousStatus
		st.statuses = previousEntries
		tr.seqMu.Unlock()
		return halted, executionPlanPersistenceError("persist execution-plan entry failure", err)
	}
	st.revision = run.Revision
	if halted {
		delete(tr.seqPlanRuns, runID)
	}
	tr.seqMu.Unlock()
	tr.broadcastExecutionPlanEvent("execution_plan_entry_finished", run, &run.Entries[idx])
	if halted {
		tr.broadcastExecutionPlanEvent("execution_plan_run_finished", run, nil)
	}
	return halted, nil
}

// advanceExecutionPlanForTest is the terminal-state hook. For a test that is the
// active entry of a sequential plan run it records the entry's outcome and either
// halts (failure with halt policy) or launches the next entry. Non-plan tests are
// a cheap no-op.
func (tr *TestRunner) advanceExecutionPlanForTest(test *ManagedTest, terminalStatus string) error {
	if test == nil {
		return nil
	}
	tr.notifyTerminalObserver(test.ID)
	// Most tests are not owned by an execution plan. Preserve the terminal hook's
	// documented cheap no-op for those tests; failure to obtain a term matters
	// only after a durable plan mapping has actually been established.
	tr.seqMu.Lock()
	runID, mapped := tr.seqPlanByTest[test.ID]
	state := tr.seqPlanRuns[runID]
	tr.seqMu.Unlock()
	if !mapped || state == nil {
		return nil
	}
	leaseTerm, ok := tr.executionPlanTestLeaseTerm(test.ID)
	if !ok {
		return errNotExecutionLeader
	}
	return tr.advanceExecutionPlanForTestWithTerm(test, terminalStatus, leaseTerm)
}

func (tr *TestRunner) setTerminalObserver(observer func(string)) {
	tr.terminalObserverMu.Lock()
	tr.terminalObserver = observer
	tr.terminalObserverMu.Unlock()
}

func (tr *TestRunner) notifyTerminalObserver(testID string) {
	tr.terminalObserverMu.RLock()
	observer := tr.terminalObserver
	tr.terminalObserverMu.RUnlock()
	if observer != nil && strings.TrimSpace(testID) != "" {
		observer(testID)
	}
}

func (tr *TestRunner) advanceExecutionPlanForTestWithTerm(test *ManagedTest, terminalStatus string, leaseTerm executionLeaseTerm) error {
	tr.seqMu.Lock()
	runID, ok := tr.seqPlanByTest[test.ID]
	if !ok {
		tr.seqMu.Unlock()
		return nil
	}
	st, ok := tr.seqPlanRuns[runID]
	if !ok {
		tr.seqMu.Unlock()
		return nil
	}
	if st.leaseTerm != leaseTerm || !tr.executionLeaseTermCurrent(leaseTerm) {
		tr.seqMu.Unlock()
		return errNotExecutionLeader
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
			return nil
		}
	}
	previousStatus := st.status
	previousEntries := append([]storage.ExecutionPlanRunEntry(nil), st.statuses...)
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
	halted := failed && entry.FailurePolicy != "skip"
	if st.stopDetail != "" {
		// Stop plan: the run ends with its running entry, whatever the policy.
		// A run that failed on its own keeps its error.
		halted = true
		st.statuses[idx].Stopped = true
		switch {
		case terminalStatus != "FAILED":
			st.statuses[idx].Detail = st.stopDetail
		case strings.TrimSpace(test.Error) == "":
			// It failed on its own before the stop took effect.
			st.statuses[idx].Detail = "run failed before the plan stop took effect"
		}
	}
	if halted {
		st.status = "halted"
		markRemainingHalted(st, idx+1)
	}
	run := st.toStorageRun()
	if err := tr.persistExecutionPlanRunDurableWithTerm(run, st.leaseTerm); err != nil {
		st.status = previousStatus
		st.statuses = previousEntries
		tr.seqMu.Unlock()
		retryErr := executionPlanPersistenceError("persist execution-plan terminal entry transition", err)
		tr.enqueueExecutionPlanTestAdvanceRetryWithTerm(test.ID, terminalStatus, leaseTerm)
		return retryErr
	}
	st.revision = run.Revision
	delete(tr.seqPlanByTest, test.ID)
	if halted {
		delete(tr.seqPlanRuns, runID)
	}
	tr.seqMu.Unlock()
	tr.broadcastExecutionPlanEvent("execution_plan_entry_finished", run, &run.Entries[idx])
	if halted {
		tr.broadcastExecutionPlanEvent("execution_plan_run_finished", run, nil)
		return nil
	}
	if err := tr.launchNextPlanEntry(runID); err != nil {
		tr.enqueueExecutionPlanRunAdvanceRetryWithTerm(runID, leaseTerm)
		return err
	}
	return nil
}

const (
	executionPlanRetryInitialDelay = 25 * time.Millisecond
	executionPlanRetryMaxDelay     = time.Second
)

// enqueueExecutionPlanTestAdvanceRetry reconciles a terminal test whose plan
// entry could not be persisted. The mapping is deliberately retained by
// advanceExecutionPlanForTest until the durable write succeeds, making retries
// idempotent. One worker per test runs while this controller remains leader and
// stops promptly once shutdown admission closes.
func (tr *TestRunner) enqueueExecutionPlanTestAdvanceRetry(testID, terminalStatus string) {
	leaseTerm, ok := tr.executionPlanTestLeaseTerm(testID)
	if !ok {
		return
	}
	tr.enqueueExecutionPlanTestAdvanceRetryWithTerm(testID, terminalStatus, leaseTerm)
}

func (tr *TestRunner) enqueueExecutionPlanTestAdvanceRetryWithTerm(testID, terminalStatus string, leaseTerm executionLeaseTerm) {
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return
	}
	key := "test:" + testID
	if !tr.beginExecutionPlanRetry(key) {
		return
	}
	go func() {
		defer tr.endExecutionPlanRetry(key)
		delay := executionPlanRetryInitialDelay
		for tr.executionPlanRetryAllowed(leaseTerm) {
			if !tr.waitExecutionPlanRetry(delay, leaseTerm) {
				return
			}
			tr.mu.RLock()
			test := tr.tests[testID]
			tr.mu.RUnlock()
			if test == nil {
				return
			}
			if err := tr.advanceExecutionPlanForTestWithTerm(test, terminalStatus, leaseTerm); err == nil {
				return
			} else {
				slog.Warn("execution-plan terminal transition retry failed", "test_id", testID, "status", terminalStatus, "error", err)
				if !durableBackgroundRetryable(err) {
					return
				}
			}
			delay = min(delay*2, executionPlanRetryMaxDelay)
		}
	}()
}

// enqueueExecutionPlanRunAdvanceRetry resumes cursor advancement after the
// preceding entry transition committed but materializing the next entry did not.
func (tr *TestRunner) enqueueExecutionPlanRunAdvanceRetry(runID string) {
	leaseTerm, ok := tr.executionPlanRunLeaseTerm(runID)
	if !ok {
		return
	}
	tr.enqueueExecutionPlanRunAdvanceRetryWithTerm(runID, leaseTerm)
}

func (tr *TestRunner) enqueueExecutionPlanRunAdvanceRetryWithTerm(runID string, leaseTerm executionLeaseTerm) {
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return
	}
	key := "run:" + runID
	if !tr.beginExecutionPlanRetry(key) {
		return
	}
	go func() {
		defer tr.endExecutionPlanRetry(key)
		delay := executionPlanRetryInitialDelay
		for tr.executionPlanRetryAllowed(leaseTerm) {
			if !tr.waitExecutionPlanRetry(delay, leaseTerm) {
				return
			}
			if err := tr.launchNextPlanEntryWithTerm(runID, leaseTerm); err == nil {
				return
			} else {
				slog.Warn("execution-plan run advancement retry failed", "run_id", runID, "error", err)
				if !durableBackgroundRetryable(err) {
					return
				}
			}
			delay = min(delay*2, executionPlanRetryMaxDelay)
		}
	}()
}

// enqueueParallelExecutionPlanPromotionRetryWithTerm retries the queue handoff
// after a parallel plan has durably committed its materialization snapshot. The
// queued tests are already recoverable at this point, so the retry is limited to
// the same immutable lease term; a successor promotes them during ordinary
// pending-test reconciliation instead of an old goroutine adopting a new term.
func (tr *TestRunner) enqueueParallelExecutionPlanPromotionRetryWithTerm(runID string, leaseTerm executionLeaseTerm) {
	if strings.TrimSpace(runID) == "" || !tr.executionLeaseTermCurrent(leaseTerm) {
		return
	}
	key := "parallel-promotion:" + runID
	if !tr.beginExecutionPlanRetry(key) {
		return
	}
	go func() {
		defer tr.endExecutionPlanRetry(key)
		delay := executionPlanRetryInitialDelay
		for tr.executionPlanRetryAllowed(leaseTerm) {
			if !tr.waitExecutionPlanRetry(delay, leaseTerm) {
				return
			}
			if tr.execPlanRunStore == nil {
				return
			}
			run, loadErr := tr.execPlanRunStore.GetExecutionPlanRun(runID)
			if loadErr != nil {
				slog.Warn("parallel execution-plan queue promotion retry could not load run", "run_id", runID, "error", loadErr)
				if !durableBackgroundRetryable(loadErr) {
					return
				}
				delay = min(delay*2, executionPlanRetryMaxDelay)
				continue
			}
			previousRevision := run.Revision
			if err := tr.handoffParallelExecutionPlanTestsOnceWithTerm(run, leaseTerm); err == nil {
				if run.Revision != previousRevision {
					tr.broadcastExecutionPlanEvent("execution_plan_run_finished", run, nil)
				}
				return
			} else {
				slog.Warn("parallel execution-plan queue promotion retry failed", "run_id", runID, "error", err)
				if !durableBackgroundRetryable(err) {
					return
				}
			}
			delay = min(delay*2, executionPlanRetryMaxDelay)
		}
	}()
}

func (tr *TestRunner) beginExecutionPlanRetry(key string) bool {
	tr.seqMu.Lock()
	defer tr.seqMu.Unlock()
	if tr.seqPlanRetrying == nil {
		tr.seqPlanRetrying = make(map[string]struct{})
	}
	if _, exists := tr.seqPlanRetrying[key]; exists {
		return false
	}
	tr.seqPlanRetrying[key] = struct{}{}
	return true
}

func (tr *TestRunner) endExecutionPlanRetry(key string) {
	tr.seqMu.Lock()
	delete(tr.seqPlanRetrying, key)
	tr.seqMu.Unlock()
}

func (tr *TestRunner) executionPlanRetryAllowed(leaseTerms ...executionLeaseTerm) bool {
	if tr == nil || !tr.isExecutionLeader() {
		return false
	}
	if len(leaseTerms) > 0 && !tr.executionLeaseTermCurrent(leaseTerms[0]) {
		return false
	}
	tr.mu.RLock()
	shuttingDown := tr.shuttingDown
	tr.mu.RUnlock()
	return !shuttingDown
}

func (tr *TestRunner) waitExecutionPlanRetry(delay time.Duration, leaseTerms ...executionLeaseTerm) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	<-timer.C
	return tr.executionPlanRetryAllowed(leaseTerms...)
}

func (tr *TestRunner) executionPlanRunLeaseTerm(runID string) (executionLeaseTerm, bool) {
	tr.seqMu.Lock()
	defer tr.seqMu.Unlock()
	state := tr.seqPlanRuns[runID]
	if state == nil || !tr.executionLeaseTermCurrent(state.leaseTerm) {
		return executionLeaseTerm{}, false
	}
	return state.leaseTerm, true
}

func (tr *TestRunner) executionPlanTestLeaseTerm(testID string) (executionLeaseTerm, bool) {
	tr.seqMu.Lock()
	defer tr.seqMu.Unlock()
	runID := tr.seqPlanByTest[testID]
	state := tr.seqPlanRuns[runID]
	if state == nil || !tr.executionLeaseTermCurrent(state.leaseTerm) {
		return executionLeaseTerm{}, false
	}
	return state.leaseTerm, true
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

// markRemainingHalted skips the entries a halted run will not start: those
// Stop plan kept from starting carry the stop, others the failure that halted
// the plan.
func markRemainingHalted(st *seqPlanRunState, from int) {
	if st.stopDetail == "" {
		markRemainingSkipped(st, from)
		return
	}
	for i := from; i < len(st.statuses); i++ {
		if st.statuses[i].Status == "pending" {
			st.statuses[i].Status = "skipped"
			st.statuses[i].Detail = st.stopDetail
			st.statuses[i].Stopped = true
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
	if err := tr.persistExecutionPlanRunDurable(run); err != nil {
		slog.Error("failed to persist execution-plan run transition", "plan_id", run.PlanID, "run_id", run.ID, "status", run.Status, "error", err)
	}
}

// createExecutionPlanRunDurable is the only valid way to cross a plan
// materialization's durable side-effect boundary. Production stores implement
// the atomic extension; the Save fallback keeps focused in-memory test doubles
// working but is never used by SQLite/PostgreSQL.
func (tr *TestRunner) createExecutionPlanRunDurable(run *storage.ExecutionPlanRun) (bool, error) {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return false, err
	}
	return tr.createExecutionPlanRunDurableWithTerm(run, leaseTerm)
}

func (tr *TestRunner) createExecutionPlanRunDurableWithTerm(run *storage.ExecutionPlanRun, leaseTerm executionLeaseTerm) (bool, error) {
	if tr == nil || tr.execPlanRunStore == nil || run == nil {
		return true, nil
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return false, errNotExecutionLeader
	}
	if run.Revision < 1 {
		run.Revision = 1
	}
	if store, ok := tr.execPlanRunStore.(atomicExecutionPlanRunPersistence); ok {
		if run.TransitionToken == "" {
			run.TransitionToken = uuid.NewString()
		}
		created := false
		err := retryDurableTransition(func() error {
			if !tr.executionLeaseTermCurrent(leaseTerm) {
				return errNotExecutionLeader
			}
			var err error
			if tr.durableExecutionFence {
				fencedStore, fenced := tr.execPlanRunStore.(leaseTermFencedExecutionPlanRunPersistence)
				if !fenced {
					return fmt.Errorf("execution-plan run storage does not support lease-term fencing")
				}
				created, err = fencedStore.CreateExecutionPlanRunIfAbsentAndLeaseTermHeld(run, executionLeaseName, leaseTerm.ownerID, leaseTerm.generation)
			} else {
				created, err = store.CreateExecutionPlanRunIfAbsent(run)
			}
			if err == nil && !created && !tr.executionLeaseTermCurrent(leaseTerm) {
				return errNotExecutionLeader
			}
			if err == nil && !created {
				current, loadErr := tr.execPlanRunStore.GetExecutionPlanRun(run.ID)
				if loadErr != nil {
					return loadErr
				}
				if current != nil && current.TransitionToken == run.TransitionToken {
					created = true
					run.Revision = current.Revision
					return nil
				}
			}
			return err
		})
		if err != nil {
			current, loadErr := tr.execPlanRunStore.GetExecutionPlanRun(run.ID)
			if loadErr == nil && current != nil && current.TransitionToken == run.TransitionToken {
				run.Revision = current.Revision
				return true, nil
			}
		}
		return created, err
	}
	if tr.durableExecutionFence {
		return false, fmt.Errorf("execution-plan run storage does not support atomic lease-term fencing")
	}
	if err := retryDurableTransition(func() error {
		if !tr.executionLeaseTermCurrent(leaseTerm) {
			return errNotExecutionLeader
		}
		return tr.execPlanRunStore.SaveExecutionPlanRun(run)
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (tr *TestRunner) persistExecutionPlanRunDurable(run *storage.ExecutionPlanRun) error {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return err
	}
	return tr.persistExecutionPlanRunDurableWithTerm(run, leaseTerm)
}

func (tr *TestRunner) persistExecutionPlanRunDurableWithTerm(run *storage.ExecutionPlanRun, leaseTerm executionLeaseTerm) error {
	if tr == nil || tr.execPlanRunStore == nil || run == nil {
		return nil
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	if store, ok := tr.execPlanRunStore.(atomicExecutionPlanRunPersistence); ok && run.Revision > 0 {
		if run.TransitionToken == "" {
			run.TransitionToken = uuid.New().String()
		}
		expectedRevision := run.Revision
		// A transport error does not establish whether the database committed:
		// the write can still be in flight even if an immediate read observes the
		// old revision. Submit once, then reconcile; background workers retry the
		// idempotent CAS only after this call reports an unknown outcome.
		run.Revision = expectedRevision
		var updated bool
		var err error
		if tr.durableExecutionFence {
			fencedStore, ok := tr.execPlanRunStore.(leaseTermFencedExecutionPlanRunPersistence)
			if !ok {
				return fmt.Errorf("execution-plan run storage does not support lease-term fencing")
			}
			updated, err = fencedStore.CompareAndSwapExecutionPlanRunIfLeaseTermHeld(run, expectedRevision, executionLeaseName, leaseTerm.ownerID, leaseTerm.generation)
		} else {
			updated, err = store.CompareAndSwapExecutionPlanRun(run, expectedRevision)
		}
		if err == nil && updated {
			return nil
		}
		if err == nil && !updated {
			if !tr.executionLeaseTermCurrent(leaseTerm) {
				return errNotExecutionLeader
			}
			// A retry can miss because its earlier, ambiguously acknowledged CAS
			// committed after the first reconciliation read. The per-transition token
			// distinguishes that case from another controller's otherwise identical
			// transition.
			current, reloadErr := tr.reloadExecutionPlanRunForTransition(run.ID)
			if reloadErr == nil && executionPlanRunTransitionMatches(current, run, expectedRevision) {
				run.Revision = current.Revision
				run.UpdatedAt = current.UpdatedAt
				run.CompletedAt = cloneTimePtr(current.CompletedAt)
				return nil
			}
			if reloadErr != nil {
				return fmt.Errorf("%w: reload run %s after CAS miss at revision %d: %v", errExecutionPlanRunCommitUnknown, run.ID, expectedRevision, reloadErr)
			}
			return fmt.Errorf("%w: run %s expected revision %d, durable revision is %d", errExecutionPlanRunConflict, run.ID, expectedRevision, current.Revision)
		}

		current, reloadErr := tr.reloadExecutionPlanRunForTransition(run.ID)
		if reloadErr == nil && executionPlanRunTransitionMatches(current, run, expectedRevision) {
			run.Revision = current.Revision
			run.UpdatedAt = current.UpdatedAt
			run.CompletedAt = cloneTimePtr(current.CompletedAt)
			return nil
		}
		run.Revision = expectedRevision
		if err != nil {
			if reloadErr != nil {
				return fmt.Errorf("%w: reconcile run %s after CAS error %v: %v", errExecutionPlanRunCommitUnknown, run.ID, err, reloadErr)
			}
			return fmt.Errorf("%w: run %s CAS failed: %v", errExecutionPlanRunCommitUnknown, run.ID, err)
		}
		if reloadErr != nil {
			return errors.Join(fmt.Errorf("%w: run %s expected revision %d", errExecutionPlanRunConflict, run.ID, expectedRevision), reloadErr)
		}
		return fmt.Errorf("%w: run %s expected revision %d, durable revision is %d", errExecutionPlanRunConflict, run.ID, expectedRevision, current.Revision)
	}
	if tr.durableExecutionFence {
		return fmt.Errorf("execution-plan run storage does not support atomic lease-term fencing")
	}
	return retryDurableTransition(func() error {
		if !tr.executionLeaseTermCurrent(leaseTerm) {
			return errNotExecutionLeader
		}
		return tr.execPlanRunStore.SaveExecutionPlanRun(run)
	})
}

func (tr *TestRunner) reloadExecutionPlanRunForTransition(runID string) (*storage.ExecutionPlanRun, error) {
	var current *storage.ExecutionPlanRun
	err := retryDurableTransition(func() error {
		var err error
		current, err = tr.execPlanRunStore.GetExecutionPlanRun(runID)
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("execution-plan run %s was not found", runID)
		}
		return nil
	})
	return current, err
}

func executionPlanRunTransitionMatches(current, intended *storage.ExecutionPlanRun, expectedRevision int64) bool {
	if current == nil || intended == nil || current.ID != intended.ID || current.Revision != expectedRevision+1 ||
		current.Status != intended.Status || current.PlanID != intended.PlanID ||
		normalizeProjectID(current.ProjectID) != normalizeProjectID(intended.ProjectID) || current.RunMode != intended.RunMode ||
		current.TransitionToken != intended.TransitionToken ||
		!sameOptionalTime(current.CompletedAt, intended.CompletedAt) {
		return false
	}
	currentEntries, currentErr := json.Marshal(current.Entries)
	intendedEntries, intendedErr := json.Marshal(intended.Entries)
	return currentErr == nil && intendedErr == nil && string(currentEntries) == string(intendedEntries)
}

func sameOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func (tr *TestRunner) loadExistingExecutionPlanRun(runID, planID, projectID, runMode string) (*storage.ExecutionPlanRun, error) {
	if tr == nil || tr.execPlanRunStore == nil {
		return nil, executionPlanPersistenceError("reload conflicting execution-plan run", fmt.Errorf("execution-plan run storage is not configured"))
	}
	existing, err := tr.execPlanRunStore.GetExecutionPlanRun(runID)
	if err != nil {
		return nil, executionPlanPersistenceError("reload conflicting execution-plan run", err)
	}
	if existing == nil {
		return nil, executionPlanPersistenceError("reload conflicting execution-plan run", fmt.Errorf("run %s was not found after an atomic create conflict", runID))
	}
	if existing.PlanID != planID || normalizeProjectID(existing.ProjectID) != normalizeProjectID(projectID) || existing.RunMode != runMode {
		return nil, fmt.Errorf("execution-plan run id %q is already used by another materialization", runID)
	}
	return existing, nil
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

// skipStoppedPlanEntryWithTerm records an entry Stop plan cancelled before its
// run started.
func (tr *TestRunner) skipStoppedPlanEntryWithTerm(runID string, idx int, leaseTerm executionLeaseTerm) error {
	tr.seqMu.Lock()
	defer tr.seqMu.Unlock()
	st := tr.seqPlanRuns[runID]
	if st == nil || idx < 0 || idx >= len(st.statuses) {
		return nil
	}
	if st.leaseTerm != leaseTerm || !tr.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	previous := st.statuses[idx]
	st.statuses[idx].Status = "skipped"
	st.statuses[idx].Detail = st.stopDetail
	st.statuses[idx].Stopped = true
	run := st.toStorageRun()
	if err := tr.persistExecutionPlanRunDurableWithTerm(run, st.leaseTerm); err != nil {
		st.statuses[idx] = previous
		return executionPlanPersistenceError("persist an entry stopped before it started", err)
	}
	st.revision = run.Revision
	return nil
}
