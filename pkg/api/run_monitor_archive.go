package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

type RunMonitorCoverage struct {
	JobID           string `json:"job_id"`
	SourceID        string `json:"source_id"`
	SourceRevision  int64  `json:"source_revision"`
	BindingRevision int64  `json:"binding_revision"`
	State           string `json:"state"`
	Reason          string `json:"reason,omitempty"`
	Batches         int64  `json:"batches"`
}

func (tr *TestRunner) persistRunMonitorArchive(project, run string, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, budget *archiveOutputBudget) error {
	store, ok := tr.testStore.(storage.RunMonitorStore)
	if !ok || manifest.MonitorEvidenceFile != "" {
		return nil
	}
	jobs, err := store.ListRunMonitorJobs(project, run)
	if err != nil || len(jobs) == 0 {
		return err
	}
	if err := ensureAnalysisBackend(tr.testStore); err != nil {
		return err
	}
	// The project mutation lock serializes this stop with receipt publication.
	// Every accepted receipt is now fixed; unaccepted batches cannot enter later.
	if err := tr.trackDurableWrite(func() error { return store.StopRunMonitors(project, run, time.Now().UTC()) }); err != nil {
		return err
	}
	jobs, err = store.ListRunMonitorJobs(project, run)
	if err != nil {
		return err
	}
	starts := map[string]time.Time{}
	for _, job := range jobs {
		starts[job.ID] = job.StartedAt
		reason := job.CoverageReason
		if reason == "" && job.NextSequence == 0 {
			reason = "no_accepted_samples"
		}
		manifest.MonitorCoverage = append(manifest.MonitorCoverage, RunMonitorCoverage{JobID: job.ID, SourceID: job.Source.ID, SourceRevision: job.Source.Revision, BindingRevision: job.BindingRevision, State: job.State, Reason: reason, Batches: job.NextSequence})
	}
	const name = "monitor_evidence.v1.jsonl"
	ctx := budget.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	var count int64
	err = writeArchiveArtifactDurableWithWriter(record.ArchivePath, name, nil, 0600, func(file *os.File, _ []byte) (int, error) {
		encoder := json.NewEncoder(&archiveOutputFile{file: file, budget: budget})
		afterJob, afterSequence := "", int64(-1)
		for {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			page, err := store.ListMonitorBatchReceipts(project, run, afterJob, afterSequence, 100)
			if err != nil {
				return 0, err
			}
			if len(page) == 0 {
				break
			}
			for _, receipt := range page {
				body, err := readMonitorArtifact(ctx, receipt.ArtifactKey, receipt.Digest)
				if err != nil {
					return 0, err
				}
				var points []analysis.MetricPoint
				if err := json.Unmarshal(body, &points); err != nil {
					return 0, err
				}
				if len(points) != receipt.SampleCount {
					return 0, fmt.Errorf("monitor receipt sample count mismatch")
				}
				for _, point := range points {
					point, err = analysis.NormalizeRunClock(point, starts[receipt.JobID])
					if err != nil {
						return 0, err
					}
					point.ReceivedAt = receipt.ReceivedAt
					point.Quality = monitorPointQuality(point, receipt.ReceivedAt)
					if err := encoder.Encode(point); err != nil {
						return 0, err
					}
					count++
				}
				afterJob, afterSequence = receipt.JobID, receipt.Sequence
			}
		}
		return 0, nil
	}, ctx)
	if err != nil {
		return err
	}
	manifest.MonitorEvidenceFile = name
	manifest.MonitorEvidenceCount = count
	return updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: name, Kind: "evidence", Format: "jsonl", RecordType: "monitor_points_v1", Count: count})
}
