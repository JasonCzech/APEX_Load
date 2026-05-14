package api

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jasonczech/stormrunner/pkg/engine"
	"github.com/jasonczech/stormrunner/pkg/storage"
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

type durationAdjustableExecutionBackend interface {
	AdjustDuration(tr *TestRunner, test *ManagedTest, newDuration time.Duration) error
}

type inventoryExecutionBackend interface {
	SystemInventory(ctx context.Context) []GeneratorInventoryEntry
}

type pausableExecutionBackend interface {
	PauseTest(tr *TestRunner, test *ManagedTest) error
	ResumeTest(tr *TestRunner, test *ManagedTest) error
}

type localExecutionBackend struct{}

func (localExecutionBackend) Run(tr *TestRunner, test *ManagedTest) {
	if test.ScriptShakeout {
		if err := tr.runScriptShakeout(test); err != nil {
			tr.failTestBeforeRun(test, err.Error())
			return
		}
	}
	artifacts, err := newRunArtifacts(test, tr.saveRunArchiveRecord)
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

	archiveRecord, finalStatus, finalError := artifacts.finalize(finalStatus, endTime, finalError)
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

func newRunArtifacts(test *ManagedTest, saveRecord func(record *storage.RunArchiveRecord) error) (*runArtifacts, error) {
	recorder, err := newRunArchiveWriter(test)
	if err != nil {
		return nil, err
	}
	return &runArtifacts{recorder: recorder, saveRecord: saveRecord}, nil
}

func (a *runArtifacts) resultSink() engine.ResultSink {
	if a == nil {
		return nil
	}
	return a.recorder
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

func (a *runArtifacts) finalize(finalStatus string, endTime time.Time, finalError string) (*storage.RunArchiveRecord, string, string) {
	if a == nil || a.recorder == nil {
		return nil, finalStatus, finalError
	}
	archiveRecord, archiveErr := a.recorder.Finalize(finalStatus, endTime, finalError)
	if archiveErr != nil {
		return archiveRecord, "FAILED", appendRunError(finalError, "archive finalize failed", archiveErr)
	}
	if a.saveRecord != nil {
		if err := a.saveRecord(archiveRecord); err != nil {
			return archiveRecord, "FAILED", appendRunError(finalError, "archive metadata save failed", err)
		}
	}
	return archiveRecord, finalStatus, finalError
}
