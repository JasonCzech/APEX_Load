package api

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

type RunMonitorCoverage struct {
	JobID                         string `json:"job_id"`
	SourceID                      string `json:"source_id"`
	SourceRevision                int64  `json:"source_revision"`
	BindingRevision               int64  `json:"binding_revision"`
	State                         string `json:"state"`
	Reason                        string `json:"reason,omitempty"`
	ManagementSnapshots           int64  `json:"management_snapshots,omitempty"`
	ManagementIncompleteSnapshots int64  `json:"management_incomplete_snapshots,omitempty"`
	Batches                       int64  `json:"batches"`
}

func (tr *TestRunner) persistRunMonitorArchive(project, run string, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, budget *archiveOutputBudget) error {
	store, ok := tr.testStore.(storage.RunMonitorStore)
	if !ok {
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
	manifest.MonitorCoverage = make([]RunMonitorCoverage, 0, len(jobs))
	coverageByJob := map[string]int{}
	starts := map[string]time.Time{}
	for _, job := range jobs {
		starts[job.ID] = job.StartedAt
		reason := job.CoverageReason
		if reason == "" && job.NextSequence == 0 {
			reason = "no_accepted_samples"
		}
		coverageByJob[job.ID] = len(manifest.MonitorCoverage)
		manifest.MonitorCoverage = append(manifest.MonitorCoverage, RunMonitorCoverage{JobID: job.ID, SourceID: job.Source.ID, SourceRevision: job.Source.Revision, BindingRevision: job.BindingRevision, State: job.State, Reason: reason, Batches: job.NextSequence})
	}
	const name = "monitor_evidence.v1.jsonl"
	ctx := budget.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	var count, managementSnapshots, managementRows int64
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
				evidence, err := readMonitorEvidence(ctx, project, run, receipt)
				if err != nil {
					return 0, err
				}
				if evidence.MessagingManagement != nil {
					managementSnapshots++
					managementRows += int64(len(evidence.MessagingManagement.Rows))
					if index, ok := coverageByJob[receipt.JobID]; ok {
						coverage := &manifest.MonitorCoverage[index]
						coverage.ManagementSnapshots++
						if evidence.MessagingManagement.Incomplete {
							coverage.ManagementIncompleteSnapshots++
							if coverage.Reason == "" {
								coverage.Reason = "management_evidence_incomplete"
							}
						}
					}
				}
				for _, point := range evidence.Points {
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
	if err := updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: name, Kind: "evidence", Format: "jsonl", RecordType: "monitor_points_v1", Count: count}); err != nil {
		return err
	}
	if managementSnapshots == 0 {
		return nil
	}
	const managementName = "monitor_management.v1.jsonl"
	err = writeArchiveArtifactDurableWithWriter(record.ArchivePath, managementName, nil, 0600, func(file *os.File, _ []byte) (int, error) {
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
				evidence, err := readMonitorEvidence(ctx, project, run, receipt)
				if err != nil {
					return 0, err
				}
				if evidence.MessagingManagement != nil {
					record := MonitorManagementRecord{receipt.JobID, strconv.FormatInt(receipt.Sequence, 10), receipt.ReceivedAt, evidence}
					if err = encoder.Encode(record); err != nil {
						return 0, err
					}
				}
				afterJob, afterSequence = receipt.JobID, receipt.Sequence
			}
		}
		return 0, nil
	}, ctx)
	if err != nil {
		return err
	}
	manifest.MonitorManagementFile = managementName
	manifest.MonitorManagementVersion = analysis.MessagingManagementVersion
	manifest.MonitorManagementSnapshotCount = managementSnapshots
	manifest.MonitorManagementRowCount = managementRows
	return updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: managementName, Kind: "evidence", Format: "jsonl", RecordType: "monitor_management_v1", Count: managementSnapshots})
}
