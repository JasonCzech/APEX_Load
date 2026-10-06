package collectoragent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// Caller holds stateMu. The last chunk owns the latest provider cursor; earlier
// chunks do not advance pagination before their durable acceptance.
func (a *Agent) latestElasticSpoolBatch(id string) *RunSampleBatch {
	batch := a.pendingBatches[id]
	if queue := a.providerBacklog[id]; len(queue) > 0 {
		batch = queue[len(queue)-1]
	}
	return batch
}

// Validate the original authorization at its signed lease horizon. This proves
// the local spool's assigned identity after cancellation without extending its
// credential lease: OpenCredentials still checks the current time before I/O.
// Caller holds stateMu.
func (a *Agent) elasticSpoolContinuations(id string) (RunJob, []*analysis.ProviderContinuation, bool) {
	batch := a.latestElasticSpoolBatch(id)
	if batch == nil {
		return RunJob{}, nil, false
	}
	var stored RunJob
	if json.Unmarshal([]byte(batch.Job.Payload), &stored) != nil || stored.ID != id || stored.LeaseExpiresAt.IsZero() {
		return RunJob{}, nil, false
	}
	job, err := VerifyRunJob(batch.Job, a.cfg.ControllerPublicKey, a.cfg.CollectorID, stored.LeaseExpiresAt.Add(-time.Nanosecond))
	if err != nil || job.Pack != "elastic" || job.Elastic == nil {
		return RunJob{}, nil, false
	}
	return elasticBatchOwnedContinuations(job, batch)

}

// Keep the single-handle helper for callers that need the primary cursor.
func (a *Agent) elasticSpoolContinuation(id string) (RunJob, *analysis.ProviderContinuation, bool) {
	job, cursors, owned := a.elasticSpoolContinuations(id)
	if !owned {
		return job, nil, false
	}
	return job, cursors[0], true
}

func elasticBatchOwnedContinuations(job RunJob, batch *RunSampleBatch) (RunJob, []*analysis.ProviderContinuation, bool) {
	cursor := job.Elastic.Continuation
	if batch.Progress != nil {
		cursor = batch.Progress.Continuation
	}
	candidates := append(append([]*analysis.ProviderContinuation(nil), batch.CleanupContinuations...), cursor)
	cursors := make([]*analysis.ProviderContinuation, 0, len(candidates))
	for _, cursor := range candidates {
		if cursor == nil || cursor.Validate() != nil || cursor.PITID == "" && cursor.AsyncID == "" {
			continue
		}
		copy := *cursor
		cursors = append(cursors, &copy)
	}
	return job, cursors, len(cursors) > 0
}

// Unknown expiry cannot authorize discarding an owned handle. Keeping the
// existing bounded spool also preserves cleanup after restart or lease expiry.
// Caller holds stateMu.
func (a *Agent) elasticSpoolNeedsCleanup(id string, now time.Time) bool {
	_, cursors, owned := a.elasticSpoolContinuations(id)
	for _, cursor := range cursors {
		if cursor.KeepAliveUntil.IsZero() || cursor.KeepAliveUntil.After(now) {
			return owned
		}
	}
	return false
}

// Caller holds stateMu. A failed persistence operation restores the queued
// suffix, so a process restart cannot diverge from the in-memory cleanup state.
func (a *Agent) discardElasticSpool(id string) error {
	pending, hadPending := a.pendingBatches[id]
	queue, hadQueue := a.providerBacklog[id]
	delete(a.pendingBatches, id)
	delete(a.providerBacklog, id)
	if err := a.saveSpool(); err != nil {
		if hadPending {
			a.pendingBatches[id] = pending
		}
		if hadQueue {
			a.providerBacklog[id] = queue
		}
		return err
	}
	delete(a.nextCollection, id)
	delete(a.jobScanUnseen, id)
	return nil
}

// Load completion fixes a new reconciliation window. Release the old spool's
// latest owned handle before replacing its unaccepted suffix with the final
// read. The renewed grant may supply credentials only for that same identity.
func (a *Agent) retireObsoleteElasticSpool(parent context.Context, current RunJob) error {
	if !current.Reconciling {
		return nil
	}
	a.stateMu.Lock()
	batch := a.latestElasticSpoolBatch(current.ID)
	obsolete := batch != nil && (!batch.QueryFromUTC.Equal(current.QueryFromUTC) || !batch.QueryUntilUTC.Equal(current.QueryUntilUTC))
	prior, cursors, owned := a.elasticSpoolContinuations(current.ID)
	a.stateMu.Unlock()
	if !obsolete {
		return nil
	}
	if owned {
		if prior.ProjectID != current.ProjectID || prior.RunID != current.RunID || prior.SourceID != current.SourceID || prior.SourceRevision != current.SourceRevision || prior.Destination != current.Destination || prior.Elastic.Config.Product != current.Elastic.Config.Product {
			return errors.New("obsolete Elastic spool identity differs from renewed grant")
		}
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		defer cancel()
		for _, cursor := range cursors {
			cleanup := current
			config := *current.Elastic
			config.Continuation = cursor
			cleanup.Elastic = &config
			if err := a.closeElasticHandle(ctx, cleanup); err != nil {
				return err
			}
		}

	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if a.latestElasticSpoolBatch(current.ID) != batch {
		return errors.New("obsolete Elastic spool changed during cleanup")
	}
	return a.discardElasticSpool(current.ID)
}

// Caller holds stateMu. Cancellation cleanup uses the shared bounded worker
// set, and the per-job exclusion prevents racing collection or spool rewrites.
func (a *Agent) dispatchCancelledElasticSpoolCleanup(parent context.Context, id string) {
	if a.providerClosing {
		return
	}
	if a.providerBusy[id] {
		return
	}
	job, cursors, owned := a.elasticSpoolContinuations(id)
	if !owned {
		return
	}
	select {
	case a.providerSlots <- struct{}{}:
	default:
		return
	}
	if a.providerBusy == nil {
		a.providerBusy = map[string]bool{}
	}
	a.providerBusy[id] = true
	batch := a.latestElasticSpoolBatch(id)
	a.providerWorkers.Add(1)
	go func() {
		defer a.providerWorkers.Done()
		defer func() {
			<-a.providerSlots
			a.stateMu.Lock()
			delete(a.providerBusy, id)
			a.stateMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		var err error
		for _, cursor := range cursors {
			cleanup := job
			config := *job.Elastic
			config.Continuation = cursor
			cleanup.Elastic = &config
			if err = a.closeElasticHandle(ctx, cleanup); err != nil {
				break
			}
		}
		cancel()
		if err != nil {
			return
		}
		a.stateMu.Lock()
		defer a.stateMu.Unlock()
		if a.latestElasticSpoolBatch(id) == batch {
			_ = a.discardElasticSpool(id)
		}
	}()
}
