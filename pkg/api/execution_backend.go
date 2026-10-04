package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type testExecutionBackend interface {
	Run(tr *TestRunner, test *ManagedTest)
}

type stoppableExecutionBackend interface {
	StopTest(tr *TestRunner, test *ManagedTest) error
}

// contextStoppableExecutionBackend lets coordinated shutdown bound remote stop
// fan-out by its own deadline instead of waiting for each backend timeout.
type contextStoppableExecutionBackend interface {
	StopTestContext(ctx context.Context, tr *TestRunner, test *ManagedTest) error
}

// abortableExecutionBackend is the optional force-teardown counterpart to the
// graceful StopTest contract. Backends that do not implement it retain the
// legacy stop path plus execution-context cancellation; distributed execution
// implements it so Abort can send graceful=false to every generator.
type abortableExecutionBackend interface {
	AbortTest(tr *TestRunner, test *ManagedTest) error
}

type contextAbortableExecutionBackend interface {
	AbortTestContext(ctx context.Context, tr *TestRunner, test *ManagedTest) error
}

type scalableExecutionBackend interface {
	ScaleVUsers(tr *TestRunner, test *ManagedTest, groupName string, targetVUsers int) error
}

// initialRampReadinessProbeExecutionBackend marks remote backends whose
// configured target can become visible to the controller before the workers
// are ready to accept runtime scaling. Stage clocks probe that same target
// through ScaleVUsers and begin only after the backend admits it.
type initialRampReadinessProbeExecutionBackend interface {
	requiresInitialRampReadinessProbe()
}

type evacuatingExecutionBackend interface {
	EvacuateGenerator(tr *TestRunner, test *ManagedTest, target string, dryRun bool, reason string) ([]GeneratorEvacuationImpact, error)
}

type durationAdjustableExecutionBackend interface {
	AdjustDuration(tr *TestRunner, test *ManagedTest, newDuration time.Duration) error
}

type inventoryExecutionBackend interface {
	SystemInventory(ctx context.Context) []GeneratorInventoryEntry
}

type drainableExecutionBackend interface {
	SetGeneratorDrainState(target string, state string) error
}

type identityDrainableExecutionBackend interface {
	SetGeneratorDrainStateByID(generatorID string, state string) (string, error)
	GeneratorTargetForID(generatorID string) string
	GeneratorIDForTarget(target string) string
}

type KubePodRef struct {
	StatefulSetUID  string `json:"-"`
	StatefulSetName string `json:"-"`
	UID             string `json:"uid,omitempty"`
	NodeName        string `json:"node_name,omitempty"`
	Name            string `json:"name"`
	Namespace       string `json:"namespace"`
	IP              string `json:"ip,omitempty"`
	Phase           string `json:"phase,omitempty"`
	Ready           bool   `json:"ready"`
	Terminating     bool   `json:"terminating"`
}

type KubeRestarter interface {
	DeletePod(ctx context.Context, namespace string, name string) error
	RolloutRestart(ctx context.Context, kind string, namespace string, name string) error
	PodByIP(ctx context.Context, namespace string, selector string, ip string) (KubePodRef, error)
	ListPods(ctx context.Context, namespace string, selector string) ([]KubePodRef, error)
	ScaleStatefulSet(ctx context.Context, namespace string, name string, replicas int) error
}

type pausableExecutionBackend interface {
	PauseTest(tr *TestRunner, test *ManagedTest) error
	ResumeTest(tr *TestRunner, test *ManagedTest) error
}

type localExecutionBackend struct{}

func (localExecutionBackend) Run(tr *TestRunner, test *ManagedTest) {
	artifacts, err := newRunArtifacts(tr, test, tr.saveRunArchiveRecord)
	if err != nil {
		tr.failTestBeforeRunAndLog(test, fmt.Sprintf("initialize archive writer: %v", err))
		return
	}
	defer artifacts.stopMonitoring()
	mge, err := tr.buildRunEngine(test, artifacts.resultSink())
	if err != nil {
		if tr.beforeLocalBuildFailureFinalize != nil {
			tr.beforeLocalBuildFailureFinalize()
		}
		tr.finalizeLocalBuildFailure(test, artifacts, err)
		return
	}

	ctx, metricsDone, metricsStopped, started, startErr := tr.startManagedTestRun(test, mge, artifacts)
	if startErr != nil {
		tr.finalizeLocalBuildFailure(test, artifacts, startErr)
		return
	}
	if !started {
		endTime := time.Now()
		// Released with defer, not manually: artifacts.finalize and the commit
		// below can panic, and the run-driver panic backstop terminalizes through
		// completeTestRun, which needs this very fence. A manual unlock is skipped
		// during unwinding and would deadlock the backstop against an orphaned gate.
		unlockArchiveCommit := tr.lockArchiveCommitFence(test.ID)
		defer unlockArchiveCommit()
		archiveRecord, finalStatus, finalError := artifacts.finalize(test, "ABORTED", endTime, "test cancelled before execution started")
		tr.mu.Lock()
		if test.Status == "ABORTED" {
			test.Archive = archiveRecord
			if archiveRecord != nil {
				tr.archives[test.ID] = archiveRecord
			}
		} else if finalStatus == "FAILED" {
			test.Status = finalStatus
			test.Error = finalError
			test.EndedAt = &endTime
			compactTerminalManagedTestLocked(test)
		}
		tr.mu.Unlock()
		return
	}

	result, err := mge.Run(ctx)
	err = localExecutionRunError(ctx, err)
	endTime := time.Now()
	artifacts.stopMonitoring()

	close(metricsDone)
	lastPublishedSnapshot := <-metricsStopped
	finalSnapshot := engine.MetricsSnapshot{}
	if result != nil {
		finalSnapshot = result.FinalSnapshot
	}
	// Run can return a nil result after recording partial work (for example, a
	// later init-once failure). Its deferred collector Stop still drains that
	// work into LatestSnapshot, so publish it through the normal terminal path.
	if latest, ok := mge.LatestSnapshot(); ok && (finalSnapshot.Timestamp.IsZero() || latest.Timestamp.After(finalSnapshot.Timestamp)) {
		finalSnapshot = latest
	}
	tr.mu.RLock()
	groupVUsers := make(map[string]int, len(test.Groups))
	for _, group := range test.Groups {
		groupVUsers[group.Name] = group.VUsers
	}
	tr.mu.RUnlock()
	tr.publishFinalLocalMetrics(test, finalSnapshot, lastPublishedSnapshot, groupVUsers, artifacts)

	tr.mu.RLock()
	testStatus := test.Status
	testError := test.Error
	tr.mu.RUnlock()
	gracefulStopCompleted := mge.GracefulStopRequested() && !mge.HardStopRequested()
	finalStatus, finalError := localExecutionFinalState(testStatus, testError, err, gracefulStopCompleted)

	// Released with defer, not manually: artifacts.finalize is the archive writer
	// the run-driver panic backstop exists for, and that backstop terminalizes
	// through completeTestRun, which needs this very fence. A manual unlock is
	// skipped during unwinding, so a panic here would leave the gate locked
	// forever and deadlock the backstop behind it.
	unlockArchiveCommit := tr.lockArchiveCommitFence(test.ID)
	defer unlockArchiveCommit()
	if recorder, ok := artifacts.recorder.(interface{ SetTerminationReason(string) }); ok {
		reason := "unknown"
		if finalStatus == "COMPLETE" && gracefulStopCompleted {
			reason = "operator_stopped"
		} else if finalStatus == "COMPLETE" && err == nil {
			reason = "workload_completed"
		}
		recorder.SetTerminationReason(reason)
	}
	archiveRecord, finalStatus, finalError := artifacts.finalize(test, finalStatus, endTime, finalError)
	if err := tr.completeTestRunUnderArchiveFence(test, finalStatus, endTime, finalError, archiveRecord); err != nil {
		slog.Error("local terminal transition remains retryable after bounded persistence retries", "test_id", test.ID, "status", finalStatus, "error", err)
	}

	var totalTransactions, totalErrors int64
	var resultDuration time.Duration
	if result != nil {
		totalTransactions = result.TotalTransactions
		totalErrors = result.TotalErrors
		resultDuration = result.Duration
	}
	slog.Info("test finished",
		"id", test.ID,
		"status", test.Status,
		"groups", len(test.Groups),
		"transactions", totalTransactions,
		"errors", totalErrors,
		"duration", resultDuration,
	)
}

// finalizeLocalBuildFailure serializes the archive outcome with Stop/Abort.
// Engine compilation happens asynchronously, so an operator can make ABORTED
// durable while compilation is in progress. The lifecycle gate decides which
// transition won before the immutable manifest is written; a stale builder must
// never publish FAILED evidence for a run whose terminal state is ABORTED.
func (tr *TestRunner) finalizeLocalBuildFailure(test *ManagedTest, artifacts *runArtifacts, buildErr error) {
	if tr == nil || test == nil || artifacts == nil || buildErr == nil {
		return
	}
	unlockLifecycle, err := tr.lockTestLifecycleControl(context.Background(), test.ID)
	if err != nil {
		tr.failTestBeforeRunAndLog(test, buildErr.Error())
		return
	}
	defer unlockLifecycle()

	unlockArchiveCommit := tr.lockArchiveCommitFence(test.ID)
	defer unlockArchiveCommit()

	endTime := time.Now().UTC()
	finalStatus := "FAILED"
	finalError := buildErr.Error()
	tr.mu.RLock()
	current := tr.tests[test.ID]
	if current != test {
		tr.mu.RUnlock()
		return
	}
	status := strings.ToUpper(strings.TrimSpace(current.Status))
	if status != "INITIALIZING" {
		if !isTerminalTestStatus(status) {
			tr.mu.RUnlock()
			return
		}
		finalStatus = status
		finalError = current.Error
		if current.EndedAt != nil {
			endTime = current.EndedAt.UTC()
		}
	}
	tr.mu.RUnlock()

	archiveRecord, finalStatus, finalError := artifacts.finalize(test, finalStatus, endTime, finalError)
	if status == "INITIALIZING" {
		if err := tr.completeTestRunUnderArchiveFence(test, finalStatus, endTime, finalError, archiveRecord); err != nil {
			slog.Error("local pre-run terminal transition remains retryable", "test_id", test.ID, "error", err)
		}
	}
}

func localExecutionRunError(ctx context.Context, runErr error) error {
	if runErr != nil || ctx == nil {
		return runErr
	}
	return ctx.Err()
}

func localExecutionFinalState(testStatus, testError string, runErr error, gracefulStopRequested bool) (string, string) {
	testStatus = strings.ToUpper(strings.TrimSpace(testStatus))
	testError = strings.TrimSpace(testError)
	if testStatus == "STOPPING" {
		if gracefulStopRequested {
			return "COMPLETE", ""
		}
		return "ABORTED", testError
	}
	if runErr == nil {
		return "COMPLETE", ""
	}
	if (errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded)) &&
		strings.HasPrefix(testError, "ci abort reason: ") {
		return "ABORTED", testError
	}
	return "FAILED", runErr.Error()
}

func newRunArtifacts(tr *TestRunner, test *ManagedTest, saveRecord func(record *storage.RunArchiveRecord) error) (*runArtifacts, error) {
	initialProvenance := maskRunArchiveProvenance(buildRunArchiveProvenance(tr, test))
	// Local runs must cross the same immutable provenance boundary as
	// distributed runs before any runtime mutation or archive write can occur.
	authoritativeInitial, err := tr.persistInitialArchiveProvenance(test, &initialProvenance)
	if err != nil {
		return nil, fmt.Errorf("persist initial archive provenance: %w", err)
	}
	recorder, err := newRunArchiveWriter(test, authoritativeInitial)
	if err != nil {
		return nil, err
	}
	return &runArtifacts{runner: tr, test: test, recorder: recorder, saveRecord: saveRecord, externalMetrics: newExternalMetricCollector(nil)}, nil
}

func buildRunArchiveProvenance(tr *TestRunner, test *ManagedTest) RunArchiveProvenance {
	provenance := RunArchiveProvenance{}
	if test == nil {
		return provenance
	}
	// This runs in the test-execution goroutine, which does not hold tr.mu, while
	// other goroutines mutate the ManagedTest under tr.mu (the pending-queue refresh
	// clears PendingReason/QueuePosition; lifecycle/stop attribution updates
	// Status/Error/Stopped*). Read the test fields from a snapshot taken under the
	// lock so those reads don't race. tr.GetSystemSettings/SystemInventory take
	// their own locks and must stay OUTSIDE this one.
	snapshot := test
	if tr != nil {
		tr.mu.RLock()
		copied := cloneManagedTestForReadLocked(test)
		tr.mu.RUnlock()
		snapshot = copied
	}
	provenance.ApplicationID = snapshot.ApplicationID
	provenance.ReleaseID = snapshot.ReleaseID
	provenance.ScenarioID = snapshot.ScenarioID
	provenance.ProjectID = normalizeProjectID(snapshot.ProjectID)
	provenance.RequestedAt = snapshot.CreatedAt
	if tr != nil {
		provenance.RequestedAt = archiveRunTimestamp(snapshot.CreatedAt, tr.testStore)
	}
	provenance.StartedAt = snapshot.StartedAt
	provenance.ScheduledStartAt = cloneTimePtr(snapshot.ScheduledStartAt)
	provenance.ScenarioMode = snapshot.ScenarioMode
	provenance.ScriptShakeout = snapshot.ScriptShakeout
	provenance.ResourceBaseline = snapshot.ResourceBaseline
	provenance.StartDelay = snapshot.StartDelay
	provenance.TotalVUsers = snapshot.TotalVUsers
	provenance.Duration = snapshot.DurationStr
	provenance.TerminationMode = snapshot.TerminationMode
	provenance.CSVExports = declaredCSVExports(snapshot)
	provenance.Assignment = cloneGeneratorAssignmentSettings(snapshot.Assignment)
	provenance.GeneratorShards = cloneGeneratorShardSummaries(snapshot.GeneratorShards)
	provenance.GoalConfig = cloneGoalConfig(snapshot.GoalConfig)
	provenance.TrafficProfile = cloneTrafficProfile(snapshot.TrafficProfile)
	provenance.RuntimeSettings = maskRuntimeSettings(snapshot.RuntimeSettings)
	provenance.ELKCapture = cloneELKCaptureConfig(snapshot.ELKCapture)
	provenance.DynatraceCapture = cloneDynatraceCaptureConfig(snapshot.DynatraceCapture)
	provenance.RuntimeSettingsReview = cloneRuntimeSettingsReview(snapshot.RuntimeSettingsReview)
	provenance.Groups = maskTestGroupsRuntimeSettings(snapshot.Groups)
	captureBaselineComparisonMetadata(&provenance, snapshot)
	provenance.BaselinePinned = snapshot.BaselinePinned
	provenance.BaselinePinnedAt = snapshot.BaselinePinnedAt
	provenance.ControllerEnv = captureAPEXLoadEnv()
	provenance.CreatedBySubject = snapshot.CreatedBySubject
	provenance.CreatedByDisplayName = snapshot.CreatedByDisplayName
	provenance.StartedBySubject = snapshot.StartedBySubject
	provenance.StartedByDisplayName = snapshot.StartedByDisplayName
	provenance.StoppedBySubject = snapshot.StoppedBySubject
	provenance.StoppedByDisplayName = snapshot.StoppedByDisplayName
	provenance.Governance = buildRunArchiveGovernanceSnapshot(snapshot)
	if tr == nil {
		return provenance
	}
	settings := tr.GetSystemSettings()
	provenance.ExecutionSettings = RunArchiveExecutionSettingsSnapshot{
		General:          settings.General,
		Thresholds:       settings.Thresholds,
		CapacityPlanning: settings.CapacityPlanning,
	}
	backend := snapshot.executionBackend
	if backend == nil {
		backend = tr.getExecutionBackend()
	}
	if inventoryBackend, ok := backend.(inventoryExecutionBackend); ok {
		provenance.GeneratorInventory = generatorInventoryForProvenance(inventoryBackend, generatorInventoryRefreshTimeout)
	}
	return provenance
}

func generatorInventoryForProvenance(backend inventoryExecutionBackend, timeout time.Duration) []GeneratorInventoryEntry {
	if backend == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = generatorInventoryRefreshTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return cloneGeneratorInventoryEntries(backend.SystemInventory(ctx))
}

func buildRunArchiveGovernanceSnapshot(test *ManagedTest) *RunArchiveGovernanceSnapshot {
	if test == nil {
		return nil
	}
	snapshot := &RunArchiveGovernanceSnapshot{
		RuntimeSettingsReview: cloneRuntimeSettingsReview(test.RuntimeSettingsReview),
		ExecutionPlanApproval: cloneRunArchiveExecutionPlanApprovalSnapshot(test.ExecutionPlanApproval),
	}
	if test.TimeslotID != nil {
		snapshot.CapacityReservationID = strings.TrimSpace(*test.TimeslotID)
	}
	if test.Error != "" || test.PendingReason != "" {
		snapshot.LaunchPreflight = &RunArchiveLaunchPreflightSnapshot{
			Decision:  "recorded",
			Status:    test.Status,
			Conflicts: compactNonEmptyStrings(test.PendingReason, test.Error),
		}
	}
	if snapshot.RuntimeSettingsReview == nil && snapshot.CapacityReservationID == "" && snapshot.LaunchPreflight == nil && snapshot.ExecutionPlanApproval == nil {
		return nil
	}
	return snapshot
}

func runArchiveExecutionPlanApprovalSnapshot(approval *storage.ExecutionPlanApproval) *RunArchiveExecutionPlanApprovalSnapshot {
	if approval == nil {
		return nil
	}
	return &RunArchiveExecutionPlanApprovalSnapshot{
		ID:              strings.TrimSpace(approval.ID),
		PlanID:          strings.TrimSpace(approval.PlanID),
		Policy:          strings.TrimSpace(approval.Policy),
		Status:          strings.TrimSpace(approval.Status),
		RequestedBy:     strings.TrimSpace(approval.RequestedBy),
		RequestedAt:     approval.RequestedAt,
		ResolvedBy:      strings.TrimSpace(approval.ResolvedBy),
		ResolvedAt:      cloneTimePtr(approval.ResolvedAt),
		DecisionReason:  strings.TrimSpace(approval.DecisionReason),
		RequiredRole:    strings.TrimSpace(approval.RequiredRole),
		PlanContentHash: strings.TrimSpace(approval.PlanContentHash),
	}
}

func cloneRunArchiveExecutionPlanApprovalSnapshot(snapshot *RunArchiveExecutionPlanApprovalSnapshot) *RunArchiveExecutionPlanApprovalSnapshot {
	if snapshot == nil {
		return nil
	}
	clone := *snapshot
	clone.ResolvedAt = cloneTimePtr(snapshot.ResolvedAt)
	return &clone
}

func compactNonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (a *runArtifacts) resultSink() engine.ResultSink {
	if a == nil {
		return nil
	}
	return combinedResultSink{
		a.recorder,
		liveErrorEvidenceSink{runner: a.runner, test: a.test},
		elkTraceEvidenceSink{artifacts: a},
	}
}

type elkTraceEvidenceSink struct{ artifacts *runArtifacts }

func (sink elkTraceEvidenceSink) RecordResult(result *protocol.Result) error {
	if sink.artifacts == nil || result == nil || sink.artifacts.test == nil || (sink.artifacts.test.ELKCapture == nil && sink.artifacts.test.DynatraceCapture == nil && !sink.artifacts.nativeDynatrace.Load()) {
		return nil
	}
	traceID := strings.TrimSpace(traceIDFromCorrelationID(result.CorrelationID))
	if traceID == "" {
		return nil
	}
	if sink.artifacts.test.ELKCapture != nil {
		sink.artifacts.elkMu.Lock()
		if sink.artifacts.elkTraceIDs == nil {
			sink.artifacts.elkTraceIDs = make(map[string]struct{})
		}
		if len(sink.artifacts.elkTraceIDs) < 64 {
			sink.artifacts.elkTraceIDs[traceID] = struct{}{}
		}
		sink.artifacts.elkMu.Unlock()
	}
	if sink.artifacts.test.DynatraceCapture != nil || sink.artifacts.nativeDynatrace.Load() {
		sink.artifacts.dynatraceMu.Lock()
		if sink.artifacts.dynatraceTraceIDs == nil {
			sink.artifacts.dynatraceTraceIDs = make(map[string]struct{})
		}
		if len(sink.artifacts.dynatraceTraceIDs) < 64 {
			sink.artifacts.dynatraceTraceIDs[traceID] = struct{}{}
		}
		sink.artifacts.dynatraceMu.Unlock()
	}
	return nil
}

func (sink elkTraceEvidenceSink) RecordTransaction(result *protocol.Result) error {
	return sink.RecordResult(result)
}

type combinedResultSink []engine.ResultSink

func (sink combinedResultSink) RecordResult(result *protocol.Result) error {
	for _, candidate := range sink {
		if candidate == nil {
			continue
		}
		if err := candidate.RecordResult(result); err != nil {
			return err
		}
	}
	return nil
}

func (sink combinedResultSink) RecordTransaction(result *protocol.Result) error {
	for _, candidate := range sink {
		transactionSink, ok := candidate.(engine.TransactionSink)
		if !ok {
			continue
		}
		if err := transactionSink.RecordTransaction(result); err != nil {
			return err
		}
	}
	return nil
}

type liveErrorEvidenceSink struct {
	runner *TestRunner
	test   *ManagedTest
}

func (sink liveErrorEvidenceSink) RecordResult(result *protocol.Result) error {
	if sink.runner == nil || result == nil || result.Snapshot == nil || result.Success && !result.MessageDetailRetained {
		return nil
	}
	evidence := liveErrorEvidenceFromResult(result)
	if evidence.Signature == "" {
		return nil
	}
	sink.runner.mu.Lock()
	test := sink.test
	if test != nil {
		test = sink.runner.tests[test.ID]
	}
	if test != nil {
		sink.runner.appendLiveErrorEvidenceLocked(test, []LiveErrorEvidence{evidence})
	}
	sink.runner.mu.Unlock()
	if evidence.Status == "success" && sink.runner.hub != nil && test != nil {
		sink.runner.hub.BroadcastScopedJSON(test.ID, test.ProjectID, map[string]any{
			"type": "transaction_sample", "test_id": test.ID, "event_id": evidence.ID,
			"action": evidence.Action, "group": evidence.Group, "region": evidence.Region,
			"network_profile": evidence.NetworkProfile, "hierarchy": evidence.Hierarchy,
			"phase": evidence.Phase, "duration_ms": evidence.DurationMs, "status_code": evidence.StatusCode,
			"has_detail": true,
		})
	}
	return nil
}

func (a *runArtifacts) recordLiveMetrics(test *ManagedTest, goalConfig *GoalConfig, live *LiveMetrics) {
	if a == nil || a.recorder == nil {
		return
	}
	if err := a.recorder.RecordIntervalSummary(live); err != nil {
		slog.Warn("failed to record interval summary", "test_id", test.ID, "error", err)
	}
	if err := a.recorder.RecordResourceSample(live.Timestamp, live.ActiveVUsers, live.Resources); err != nil {
		slog.Warn("failed to record resource sample", "test_id", test.ID, "error", err)
	}
	a.recordSLAEvidence(goalConfig, live)
}

// recordSLAEvidence updates the run-level manifest inputs without writing an
// interval. The terminal publisher uses it when the periodic broadcaster
// already persisted the collector's final timestamp before the engine returned.
func (a *runArtifacts) recordSLAEvidence(goalConfig *GoalConfig, live *LiveMetrics) {
	if a == nil {
		return
	}
	if live != nil {
		a.slaEvaluated = a.slaEvaluated || ciSLAEvidenceComplete(goalConfig, live)
		breached, details := ciLiveSLABreach(live.GoalStatus)
		if breached {
			a.slaBreached = true
			a.slaDetails = compactNonEmptyStrings(append(a.slaDetails, details...)...)
		}
	}
}

func (a *runArtifacts) enrichLiveMetrics(test *ManagedTest, live *LiveMetrics) {
	if a == nil || live == nil {
		return
	}
	var values map[string]float64
	if a.externalMetrics != nil {
		values = a.externalMetrics.collect(context.Background(), externalMetricTargetsForTest(test))
	}
	nativeWasActive := a.nativeDynatrace.Load()
	a.observeMonitoringTraces(live)
	a.startMonitoring(test)
	if !nativeWasActive && a.nativeDynatrace.Load() {
		a.observeMonitoringTraces(live)
	}
	a.elkMu.Lock()
	live.ELK = cloneELKCaptureStatus(a.elkStatus)
	a.elkMu.Unlock()
	a.dynatraceMu.Lock()
	live.Dynatrace = cloneDynatraceCaptureStatus(a.dynatraceStatus)
	a.dynatraceMu.Unlock()
	if live.ELK != nil {
		for key, value := range elkExternalMetrics(live.ELK.ConnectionID, live.ELK.Metrics) {
			if values == nil {
				values = make(map[string]float64)
			}
			values[key] = value
		}
	}
	if live.Dynatrace != nil {
		for key, value := range dynatraceExternalMetrics(live.Dynatrace.ConnectionID, live.Dynatrace.Metrics) {
			if values == nil {
				values = make(map[string]float64)
			}
			values[key] = value
		}
	}
	live.Resources = attachExternalMetrics(live.Resources, values)
}

func (a *runArtifacts) collectDynatraceMetrics(ctx context.Context, test *ManagedTest, live *LiveMetrics) map[string]float64 {
	if a == nil || test == nil || test.DynatraceCapture == nil || live == nil {
		return nil
	}
	a.dynatracePollMu.Lock()
	defer a.dynatracePollMu.Unlock()
	now := live.Timestamp
	if now.IsZero() {
		now = time.Now().UTC()
	}
	cadence := time.Duration(test.DynatraceCapture.PollIntervalSeconds) * time.Second
	if cadence < 10*time.Second {
		cadence = 15 * time.Second
	}
	a.dynatraceMu.Lock()
	if a.dynatraceStatus != nil && !a.dynatraceLastPoll.IsZero() && now.Sub(a.dynatraceLastPoll) < cadence {
		status := cloneDynatraceCaptureStatus(a.dynatraceStatus)
		metrics := dynatraceExternalMetrics(test.DynatraceCapture.ConnectionID, a.dynatraceStatus.Metrics)
		a.dynatraceMu.Unlock()
		live.Dynatrace = status
		return metrics
	}
	traceIDs := make([]string, 0, len(a.dynatraceTraceIDs))
	for traceID := range a.dynatraceTraceIDs {
		traceIDs = append(traceIDs, traceID)
	}
	a.dynatraceMu.Unlock()
	sort.Strings(traceIDs)
	start := test.CreatedAt
	if test.StartedAt != nil {
		start = *test.StartedAt
	}
	result := queryDynatraceWithClient(ctx, a.runner, test.DynatraceCapture, test.ID, traceIDs, start, now, a.monitorHTTPClient)
	if ctx.Err() == context.Canceled {
		return nil // Shutdown must not replace the final good snapshot with an error.
	}

	a.dynatraceMu.Lock()
	a.dynatraceLastPoll = now
	status := &DynatraceCaptureStatus{ConnectionID: test.DynatraceCapture.ConnectionID, ConnectionName: test.DynatraceCapture.ConnectionName, LastPolledAt: &now,
		Capabilities: result.Capabilities, Metrics: cloneFloat64Map(result.Metrics)}
	connected := 0
	for _, health := range result.Capabilities {
		if health.Status == "connected" {
			connected++
		}
	}
	switch {
	case len(result.Capabilities) == 0 || connected == 0:
		status.Status = "unavailable"
	case connected < len(result.Capabilities):
		status.Status = "partial"
	default:
		status.Status = "connected"
	}
	if status.Status != "connected" && a.dynatraceStatus != nil {
		status.LastSuccessfulAt = cloneTimePtr(a.dynatraceStatus.LastSuccessfulAt)
		if status.Metrics == nil {
			status.Metrics = make(map[string]float64)
		}
		for key, value := range a.dynatraceStatus.Metrics {
			if _, exists := status.Metrics[key]; !exists {
				status.Metrics[key] = value
			}
		}
	}
	fresh := a.retainDynatraceEvidence(result.Logs, result.Problems)
	if status.Status == "connected" {
		completedAt := time.Now().UTC()
		status.LastSuccessfulAt = &completedAt
	}
	status.Logs = append([]DynatraceLogSample(nil), a.dynatraceLogs...)
	status.Problems = append([]DynatraceProblemSummary(nil), a.dynatraceProblems...)
	a.dynatraceStatus = status
	statusSnapshot := cloneDynatraceCaptureStatus(status)
	metrics := dynatraceExternalMetrics(test.DynatraceCapture.ConnectionID, status.Metrics)
	a.dynatraceMu.Unlock()

	if len(fresh) > 0 {
		if recorder, ok := a.recorder.(interface {
			RecordDynatraceEvidence([]DynatraceEvidenceRecord) error
		}); ok {
			if err := recorder.RecordDynatraceEvidence(fresh); err != nil {
				slog.Warn("failed to archive Dynatrace evidence", "test_id", test.ID, "error", err)
			}
		}
	}
	live.Dynatrace = statusSnapshot
	return metrics
}

func (a *runArtifacts) collectELKMetrics(ctx context.Context, test *ManagedTest, live *LiveMetrics) map[string]float64 {
	if a == nil || test == nil || test.ELKCapture == nil || live == nil {
		return nil
	}
	// Only one controller poll may run at a time, but never hold elkMu across
	// network or archive I/O: VUser result sinks use elkMu to contribute trace
	// IDs and must remain independent of monitoring availability.
	a.elkPollMu.Lock()
	defer a.elkPollMu.Unlock()

	now := live.Timestamp
	if now.IsZero() {
		now = time.Now().UTC()
	}
	a.elkMu.Lock()
	if a.elkStatus != nil && !a.elkLastPoll.IsZero() {
		if now.Sub(a.elkLastPoll) < 10*time.Second {
			status := cloneELKCaptureStatus(a.elkStatus)
			metrics := elkExternalMetrics(test.ELKCapture.ConnectionID, a.elkStatus.Metrics)
			a.elkMu.Unlock()
			live.ELK = status
			return metrics
		}
	}
	traceIDs := make([]string, 0, len(a.elkTraceIDs))
	for traceID := range a.elkTraceIDs {
		traceIDs = append(traceIDs, traceID)
	}
	a.elkMu.Unlock()
	start := test.CreatedAt
	if test.StartedAt != nil {
		start = *test.StartedAt
	}
	sort.Strings(traceIDs)
	result, err := queryELKWithClient(ctx, a.runner, test.ELKCapture, test.ID, traceIDs, start, now, a.monitorHTTPClient)
	if ctx.Err() == context.Canceled {
		return nil
	}

	a.elkMu.Lock()
	a.elkLastPoll = now
	status := &ELKCaptureStatus{ConnectionID: test.ELKCapture.ConnectionID, ConnectionName: test.ELKCapture.ConnectionName, LastPolledAt: &now}
	var fresh []ELKLogSample
	if err != nil {
		status.Status = "unavailable"
		status.Message = err.Error()
		if a.elkStatus != nil {
			status.LastSuccessfulAt = cloneTimePtr(a.elkStatus.LastSuccessfulAt)
			status.Metrics = cloneFloat64Map(a.elkStatus.Metrics)
			status.Samples = append([]ELKLogSample(nil), a.elkStatus.Samples...)
		}
	} else {
		status.Status = "connected"
		completedAt := time.Now().UTC()
		status.LastSuccessfulAt = &completedAt
		status.Metrics = cloneFloat64Map(result.Metrics)
		if a.elkSampleIDs == nil {
			a.elkSampleIDs = make(map[string]struct{})
		}
		fresh = make([]ELKLogSample, 0, len(result.Samples))
		for _, sample := range result.Samples {
			key := sample.ID
			if key == "" {
				key = sample.Timestamp.String() + "\x00" + sample.Message
			}
			if _, exists := a.elkSampleIDs[key]; exists || len(a.elkSampleIDs) >= 50 {
				continue
			}
			a.elkSampleIDs[key] = struct{}{}
			a.elkSamples = append(a.elkSamples, sample)
			fresh = append(fresh, sample)
		}
		status.Samples = append([]ELKLogSample(nil), a.elkSamples...)
	}
	a.elkStatus = status
	statusSnapshot := cloneELKCaptureStatus(status)
	metrics := elkExternalMetrics(test.ELKCapture.ConnectionID, status.Metrics)
	a.elkMu.Unlock()

	if len(fresh) > 0 {
		if recorder, ok := a.recorder.(interface{ RecordELKSamples([]ELKLogSample) error }); ok {
			if err := recorder.RecordELKSamples(fresh); err != nil {
				slog.Warn("failed to archive ELK evidence samples", "test_id", test.ID, "error", err)
			}
		}
	}
	live.ELK = statusSnapshot
	return metrics
}

func (a *runArtifacts) finalize(test *ManagedTest, finalStatus string, endTime time.Time, finalError string) (*storage.RunArchiveRecord, string, string) {
	a.stopMonitoring()
	if a == nil || a.recorder == nil {
		return nil, finalStatus, finalError
	}
	// Logs are append-only evidence; problem summaries are the latest retained
	// state and are written once, after polling has stopped.
	if recorder, ok := a.recorder.(interface {
		RecordDynatraceEvidence([]DynatraceEvidenceRecord) error
	}); ok {
		var records []DynatraceEvidenceRecord
		for _, problem := range a.dynatraceProblems {
			records = append(records, DynatraceEvidenceRecord{Type: "problem_summary", Problem: &problem})
		}
		if err := recorder.RecordDynatraceEvidence(records); err != nil {
			slog.Warn("failed to archive Dynatrace problem summaries", "test_id", test.ID, "error", err)
		}
	}

	slaEvaluated := a.slaEvaluated
	slaBreached := a.slaBreached
	slaDetails := append([]string(nil), a.slaDetails...)
	var goalStatus *GoalStatus
	if test != nil && a.runner != nil {
		a.runner.mu.RLock()
		if strings.TrimSpace(finalError) == "" && strings.HasPrefix(test.Error, "early stop:") {
			finalError = test.Error
		}
		if a.runner.tests[test.ID] == test && test.LiveMetrics != nil {
			goalStatus = cloneGoalStatus(test.LiveMetrics.GoalStatus)
			slaEvaluated = slaEvaluated || test.ciSLAEvaluated || ciSLAEvidenceComplete(test.GoalConfig, test.LiveMetrics)
			slaBreached = slaBreached || test.ciSLABreached
			slaDetails = compactNonEmptyStrings(append(slaDetails, test.ciSLADetails...)...)
		}
		a.runner.mu.RUnlock()
	} else if test != nil && test.LiveMetrics != nil {
		goalStatus = cloneGoalStatus(test.LiveMetrics.GoalStatus)
		slaEvaluated = slaEvaluated || test.ciSLAEvaluated || ciSLAEvidenceComplete(test.GoalConfig, test.LiveMetrics)
		slaBreached = slaBreached || test.ciSLABreached
		slaDetails = compactNonEmptyStrings(append(slaDetails, test.ciSLADetails...)...)
	}
	if goalStatus != nil {
		breached, details := ciLiveSLABreach(goalStatus)
		if breached {
			slaBreached = true
			slaDetails = compactNonEmptyStrings(append(slaDetails, details...)...)
		}
	}

	if test != nil {
		if refresher, ok := a.recorder.(interface{ RefreshProvenance(RunArchiveProvenance) }); ok {
			refresher.RefreshProvenance(buildRunArchiveProvenance(a.runner, test))
		}
	}
	archiveRecord, archiveErr := a.recorder.Finalize(finalStatus, endTime, finalError, slaEvaluated, slaBreached, slaDetails)
	if archiveRecord != nil && test != nil && archiveRecord.RefKey == "" {
		archiveRecord.RefKey = test.RefKey
	}
	var publicationErr error
	// Make the terminal row durable before the publication outbox or any remote
	// side effect. A crash after the local manifest commit is then recoverable by
	// the orphan scanner without startup reconciliation rewriting the run FAILED.
	if a.runner != nil && test != nil && archiveRecord != nil {
		if err := a.runner.persistStoredTestStatus(test.ID, test.executionFence, finalStatus, &endTime, finalError); err != nil {
			if isStoredTestTransitionCancelled(err) {
				// A deleted durable row is a tombstone. Keep the locally staged artifact
				// unpublished and suppress every downstream terminal side effect.
				archiveRecord = nil
			} else {
				publicationErr = fmt.Errorf("persist terminal state before archive publication: %w", err)
			}
		}
	}
	if publicationErr == nil && a.runner != nil && archiveRecord != nil {
		publishCtx, cancel := context.WithTimeout(context.Background(), archiveFinalizationAttemptTimeout)
		// Every production caller holds test.ID's archive commit fence across
		// finalization and the terminal test transition. Use the under-fence form
		// to keep that transaction boundary without recursively locking the stripe.
		publicationErr = a.runner.finalizeRunArchiveDurablyUnderFence(publishCtx, test, archiveRecord, true)
		cancel()
	} else if publicationErr == nil && a.saveRecord != nil && archiveRecord != nil {
		publicationErr = retryDurableTransition(func() error { return a.saveRecord(archiveRecord) })
	}
	if archiveErr != nil {
		// Finalize may have already committed the manifest locally. The execution
		// outcome encoded in that immutable evidence remains authoritative; archive
		// availability is an operational concern, not a reason to rewrite it.
		slog.Error("run archive finalization failed without changing the execution outcome", "test_id", test.ID, "error", archiveErr)
	}
	if publicationErr != nil {
		slog.Error("run archive publication remains durably retryable without changing the execution outcome", "test_id", test.ID, "error", publicationErr)
		a.enqueueArchivePersistenceRetry(test, archiveRecord, true)
		// Keep the sole recoverable local source private while its durable outbox
		// job is pending. Exposing it through test.Archive would let readers or
		// retention treat an unpublished archive as committed and prune it.
		if a.runner != nil {
			archiveRecord = nil
		}
	}
	if archiveErr != nil && archiveRecord == nil && a.runner != nil && test != nil {
		a.runner.mu.Lock()
		if a.runner.tests[test.ID] == test {
			test.localArchiveFinalizationFailed = true
		}
		a.runner.mu.Unlock()
		// An operator abort may already be durable. Otherwise completion below
		// records this reason only after its terminal row commits successfully.
		a.runner.recordLocalArchiveFailure(test)
	}
	return archiveRecord, finalStatus, finalError
}

func (a *runArtifacts) enqueueArchivePersistenceRetry(test *ManagedTest, archiveRecord *storage.RunArchiveRecord, persistArtifacts bool) {
	if a == nil || a.runner == nil || test == nil || archiveRecord == nil {
		return
	}
	record := *archiveRecord
	a.runner.enqueueTerminalRetry("archive:"+test.ID, "archive_finalize", func() error {
		unlockArchiveCommit := a.runner.lockArchiveCommitFence(test.ID)
		defer unlockArchiveCommit()

		a.runner.mu.RLock()
		current := a.runner.tests[test.ID]
		if current == nil || current != test {
			a.runner.mu.RUnlock()
			return nil
		}
		terminal := isTerminalTestStatus(current.Status)
		a.runner.mu.RUnlock()
		if !terminal {
			return fmt.Errorf("test %s is not durably terminal yet", test.ID)
		}
		publishCtx, cancel := context.WithTimeout(context.Background(), archiveFinalizationAttemptTimeout)
		defer cancel()
		return a.runner.finalizeRunArchiveDurablyUnderFence(publishCtx, test, &record, persistArtifacts)
	})
}

func isTerminalTestStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "COMPLETE", "FAILED", "ABORTED":
		return true
	default:
		return false
	}
}

// IsTerminalTestStatus exposes the controller's terminal-state vocabulary to
// first-party clients and contract tests without duplicating lifecycle rules.
func IsTerminalTestStatus(status string) bool {
	return isTerminalTestStatus(status)
}
