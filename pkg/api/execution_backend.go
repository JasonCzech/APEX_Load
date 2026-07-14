package api

import (
	"context"
	"fmt"
	"log/slog"
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

type scalableExecutionBackend interface {
	ScaleVUsers(tr *TestRunner, test *ManagedTest, groupName string, targetVUsers int) error
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
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	IP          string `json:"ip,omitempty"`
	Phase       string `json:"phase,omitempty"`
	Ready       bool   `json:"ready"`
	Terminating bool   `json:"terminating"`
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
	mge, err := tr.buildRunEngine(test, artifacts.resultSink())
	if err != nil {
		tr.failTestBeforeRunAndLog(test, err.Error())
		return
	}

	ctx, metricsDone, metricsStopped, started, startErr := tr.startManagedTestRun(test, mge, artifacts)
	if startErr != nil {
		endTime := time.Now()
		unlockArchiveCommit := tr.lockArchiveCommitFence(test.ID)
		_, _, finalError := artifacts.finalize(test, "FAILED", endTime, startErr.Error())
		tr.failTestBeforeRunAndLog(test, finalError)
		unlockArchiveCommit()
		return
	}
	if !started {
		endTime := time.Now()
		unlockArchiveCommit := tr.lockArchiveCommitFence(test.ID)
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
		}
		tr.mu.Unlock()
		unlockArchiveCommit()
		return
	}

	result, err := mge.Run(ctx)

	close(metricsDone)
	<-metricsStopped

	endTime := time.Now()
	finalStatus := "COMPLETE"
	finalError := ""
	if err != nil {
		finalStatus = "FAILED"
		finalError = err.Error()
	}

	unlockArchiveCommit := tr.lockArchiveCommitFence(test.ID)
	archiveRecord, finalStatus, finalError := artifacts.finalize(test, finalStatus, endTime, finalError)
	if err := tr.completeTestRunUnderArchiveFence(test, finalStatus, endTime, finalError, archiveRecord); err != nil {
		slog.Error("local terminal transition remains retryable after bounded persistence retries", "test_id", test.ID, "status", finalStatus, "error", err)
	}
	unlockArchiveCommit()

	slog.Info("test finished",
		"id", test.ID,
		"status", test.Status,
		"groups", len(test.Groups),
		"transactions", result.TotalTransactions,
		"errors", result.TotalErrors,
		"duration", result.Duration,
	)
}

func newRunArtifacts(tr *TestRunner, test *ManagedTest, saveRecord func(record *storage.RunArchiveRecord) error) (*runArtifacts, error) {
	recorder, err := newRunArchiveWriter(test, buildRunArchiveProvenance(tr, test))
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
	provenance.StartedAt = snapshot.StartedAt
	provenance.ScheduledStartAt = cloneTimePtr(snapshot.ScheduledStartAt)
	provenance.ScenarioMode = snapshot.ScenarioMode
	provenance.ScriptShakeout = snapshot.ScriptShakeout
	provenance.ResourceBaseline = snapshot.ResourceBaseline
	provenance.StartDelay = snapshot.StartDelay
	provenance.TotalVUsers = snapshot.TotalVUsers
	provenance.Duration = snapshot.DurationStr
	provenance.Assignment = cloneGeneratorAssignmentSettings(snapshot.Assignment)
	provenance.GeneratorShards = cloneGeneratorShardSummaries(snapshot.GeneratorShards)
	provenance.GoalConfig = cloneGoalConfig(snapshot.GoalConfig)
	provenance.RuntimeSettings = cloneRuntimeSettings(snapshot.RuntimeSettings)
	provenance.RuntimeSettingsReview = cloneRuntimeSettingsReview(snapshot.RuntimeSettingsReview)
	provenance.Groups = cloneArchiveTestGroups(snapshot.Groups)
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
		provenance.GeneratorInventory = cloneGeneratorInventoryEntries(inventoryBackend.SystemInventory(context.Background()))
	}
	return provenance
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
	}
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

type liveErrorEvidenceSink struct {
	runner *TestRunner
	test   *ManagedTest
}

func (sink liveErrorEvidenceSink) RecordResult(result *protocol.Result) error {
	if sink.runner == nil || result == nil || result.Success || result.Snapshot == nil {
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
	return nil
}

func (a *runArtifacts) recordLiveMetrics(test *ManagedTest, live *LiveMetrics) {
	if a == nil || a.recorder == nil {
		return
	}
	if err := a.recorder.RecordIntervalSummary(live); err != nil {
		slog.Warn("failed to record interval summary", "test_id", test.ID, "error", err)
	}
	if err := a.recorder.RecordResourceSample(live.Timestamp, live.ActiveVUsers, live.Resources); err != nil {
		slog.Warn("failed to record resource sample", "test_id", test.ID, "error", err)
	}
}

func (a *runArtifacts) enrichLiveMetrics(test *ManagedTest, live *LiveMetrics) {
	if a == nil || live == nil || a.externalMetrics == nil {
		return
	}
	values := a.externalMetrics.collect(context.Background(), externalMetricTargetsForTest(test))
	live.Resources = attachExternalMetrics(live.Resources, values)
}

func (a *runArtifacts) finalize(test *ManagedTest, finalStatus string, endTime time.Time, finalError string) (*storage.RunArchiveRecord, string, string) {
	if a == nil || a.recorder == nil {
		return nil, finalStatus, finalError
	}

	// Determine SLA Status
	slaBreached := false
	var slaDetails []string
	var goalStatus *GoalStatus
	if test != nil && a.runner != nil {
		a.runner.mu.RLock()
		if a.runner.tests[test.ID] == test && test.LiveMetrics != nil {
			goalStatus = cloneGoalStatus(test.LiveMetrics.GoalStatus)
		}
		a.runner.mu.RUnlock()
	} else if test != nil && test.LiveMetrics != nil {
		goalStatus = cloneGoalStatus(test.LiveMetrics.GoalStatus)
	}
	if goalStatus != nil {
		gs := goalStatus
		if gs.LatencyBreached {
			slaBreached = true
			slaDetails = append(slaDetails, "Latency breached")
		}
		if gs.ErrorBreached {
			slaBreached = true
			slaDetails = append(slaDetails, "Error rate breached")
		}
		if gs.ServerProcessingBreached {
			slaBreached = true
			slaDetails = append(slaDetails, "Server processing breached")
		}
		if gs.TransferBreached {
			slaBreached = true
			slaDetails = append(slaDetails, "Transfer time breached")
		}
		if gs.ErrorCountBreached {
			slaBreached = true
			slaDetails = append(slaDetails, "Total error count breached")
		}
		if gs.ThrottledRequestsBreached {
			slaBreached = true
			slaDetails = append(slaDetails, "Event Hubs throttled requests breached")
		}
		if gs.EndToEndLatencyBreached {
			slaBreached = true
			slaDetails = append(slaDetails, "Event Hubs end-to-end latency breached")
		}
		if gs.PartitionSkewBreached {
			slaBreached = true
			slaDetails = append(slaDetails, "Event Hubs partition skew breached")
		}
		if gs.RedeliveryRateBreached {
			slaBreached = true
			slaDetails = append(slaDetails, "Messaging redelivery rate breached")
		}
		if gs.DeadLetterRateBreached {
			slaBreached = true
			slaDetails = append(slaDetails, "Messaging dead-letter rate breached")
		}
	}

	if test != nil {
		if refresher, ok := a.recorder.(interface{ RefreshProvenance(RunArchiveProvenance) }); ok {
			refresher.RefreshProvenance(buildRunArchiveProvenance(a.runner, test))
		}
	}
	archiveRecord, archiveErr := a.recorder.Finalize(finalStatus, endTime, finalError, slaBreached, slaDetails)
	if archiveRecord != nil && test != nil && archiveRecord.RefKey == "" {
		archiveRecord.RefKey = test.RefKey
	}
	var publicationErr error
	// Make the terminal row durable before the publication outbox or any remote
	// side effect. A crash after the local manifest commit is then recoverable by
	// the orphan scanner without startup reconciliation rewriting the run FAILED.
	if a.runner != nil && test != nil && archiveRecord != nil {
		if err := a.runner.persistStoredTestStatus(test.ID, finalStatus, &endTime, finalError); err != nil {
			publicationErr = fmt.Errorf("persist terminal state before archive publication: %w", err)
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
