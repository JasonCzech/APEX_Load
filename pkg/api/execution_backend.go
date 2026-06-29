package api

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/api/testexec"
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
		tr.failTestBeforeRun(test, fmt.Sprintf("initialize archive writer: %v", err))
		return
	}
	mge, err := tr.buildRunEngine(test, artifacts.resultSink())
	if err != nil {
		tr.failTestBeforeRun(test, err.Error())
		return
	}

	ctx, metricsDone, metricsStopped := tr.startManagedTestRun(test, mge, artifacts)

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

	archiveRecord, finalStatus, finalError := artifacts.finalize(test, finalStatus, endTime, finalError)
	tr.completeTestRun(test, finalStatus, endTime, finalError, archiveRecord)

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
	provenance.ApplicationID = test.ApplicationID
	provenance.ReleaseID = test.ReleaseID
	provenance.ScenarioID = test.ScenarioID
	provenance.ProjectID = normalizeProjectID(test.ProjectID)
	provenance.RequestedAt = test.CreatedAt
	provenance.StartedAt = test.StartedAt
	provenance.ScheduledStartAt = cloneTimePtr(test.ScheduledStartAt)
	provenance.ScenarioMode = test.ScenarioMode
	provenance.ScriptShakeout = test.ScriptShakeout
	provenance.ResourceBaseline = test.ResourceBaseline
	provenance.StartDelay = test.StartDelay
	provenance.TotalVUsers = test.TotalVUsers
	provenance.Duration = test.DurationStr
	provenance.Assignment = cloneGeneratorAssignmentSettings(test.Assignment)
	provenance.GeneratorShards = cloneGeneratorShardSummaries(test.GeneratorShards)
	provenance.GoalConfig = cloneGoalConfig(test.GoalConfig)
	provenance.RuntimeSettings = cloneRuntimeSettings(test.RuntimeSettings)
	provenance.RuntimeSettingsReview = cloneRuntimeSettingsReview(test.RuntimeSettingsReview)
	provenance.Groups = cloneArchiveTestGroups(test.Groups)
	provenance.BaselinePinned = test.BaselinePinned
	provenance.BaselinePinnedAt = test.BaselinePinnedAt
	provenance.ControllerEnv = captureAPEXLoadEnv()
	provenance.CreatedBySubject = test.CreatedBySubject
	provenance.CreatedByDisplayName = test.CreatedByDisplayName
	provenance.StartedBySubject = test.StartedBySubject
	provenance.StartedByDisplayName = test.StartedByDisplayName
	provenance.StoppedBySubject = test.StoppedBySubject
	provenance.StoppedByDisplayName = test.StoppedByDisplayName
	provenance.Governance = buildRunArchiveGovernanceSnapshot(test)
	if tr == nil {
		return provenance
	}
	settings := tr.GetSystemSettings()
	provenance.ExecutionSettings = RunArchiveExecutionSettingsSnapshot{
		General:          settings.General,
		Thresholds:       settings.Thresholds,
		CapacityPlanning: settings.CapacityPlanning,
	}
	backend := test.executionBackend
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
		ID:             strings.TrimSpace(approval.ID),
		PlanID:         strings.TrimSpace(approval.PlanID),
		Policy:         strings.TrimSpace(approval.Policy),
		Status:         strings.TrimSpace(approval.Status),
		RequestedBy:    strings.TrimSpace(approval.RequestedBy),
		RequestedAt:    approval.RequestedAt,
		ResolvedBy:     strings.TrimSpace(approval.ResolvedBy),
		ResolvedAt:     cloneTimePtr(approval.ResolvedAt),
		DecisionReason: strings.TrimSpace(approval.DecisionReason),
		RequiredRole:   strings.TrimSpace(approval.RequiredRole),
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
	if test != nil && test.LiveMetrics != nil && test.LiveMetrics.GoalStatus != nil {
		gs := test.LiveMetrics.GoalStatus
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
	if archiveErr != nil {
		return archiveRecord, "FAILED", testexec.AppendRunError(finalError, "archive finalize failed", archiveErr)
	}
	if a.runner != nil && archiveRecord != nil {
		if err := a.runner.persistTestManifestArtifact(test, archiveRecord); err != nil {
			return archiveRecord, "FAILED", testexec.AppendRunError(finalError, "test manifest archive failed", err)
		}
	}
	if a.saveRecord != nil {
		if err := a.saveRecord(archiveRecord); err != nil {
			return archiveRecord, "FAILED", testexec.AppendRunError(finalError, "archive metadata save failed", err)
		}
	}
	return archiveRecord, finalStatus, finalError
}
