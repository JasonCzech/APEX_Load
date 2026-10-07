package collectoragent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"time"

	"github.com/jasonczech/apexload/pkg/elastic"
)

func elasticInspectionSpoolKey(job ElasticInspectionJob) string {
	if job.Kind == "cleanup" {
		return "cleanup:" + job.ID
	}
	return job.ID
}

func (a *Agent) restoreElasticInspectionSpool(pending map[string]*ElasticInspectionResult) error {
	if len(pending) > maxCollectorSpoolJobs {
		return errors.New("inspection spool job limit exceeded")
	}
	for id, result := range pending {
		if result == nil || len(id) > 256 || len(result.CleanupContinuations) > 32 {
			return errors.New("invalid inspection spool result")
		}
		var stored ElasticInspectionJob
		if json.Unmarshal([]byte(result.Job.Payload), &stored) != nil || elasticInspectionSpoolKey(stored) != id || stored.LeaseExpiresAt.IsZero() {
			return errors.New("invalid inspection spool identity")
		}
		if _, err := VerifyElasticInspectionJob(result.Job, a.cfg.ControllerPublicKey, a.cfg.CollectorID, stored.LeaseExpiresAt.Add(-time.Nanosecond)); err != nil {
			return errors.New("inspection spool authorization rejected")
		}
		if err := ValidateElasticInspectionData(result.Data); err != nil {
			return err
		}
		for _, cursor := range result.CleanupContinuations {
			if cursor == nil || cursor.Validate() != nil || cursor.PITID == "" && cursor.AsyncID == "" {
				return errors.New("invalid inspection spool owned handle")
			}
		}
	}
	a.elasticInspectionPending = pending
	return nil
}

func (a *Agent) persistElasticInspectionResult(id string, result *ElasticInspectionResult) error {
	var job ElasticInspectionJob
	if result == nil || json.Unmarshal([]byte(result.Job.Payload), &job) != nil || elasticInspectionSpoolKey(job) != id {
		return errors.New("inspection spool identity mismatch")
	}
	// A preview is deliberately bounded; include its signed envelope and private
	// cleanup state in the durable batch limit, and disclose any reduced preview.
	for {
		body, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if len(body) <= 768<<10 {
			break
		}
		if result.Data.Preview == nil {
			return errors.New("inspection result exceeds transport budget")
		}
		preview := result.Data.Preview
		if len(preview.Evidence) > 1 {
			preview.Evidence = preview.Evidence[:len(preview.Evidence)/2]
		} else if len(preview.Points) > 1 {
			preview.Points = preview.Points[:len(preview.Points)/2]
		} else {
			return errors.New("inspection result exceeds transport budget")
		}
		result.Data.Truncated = true
	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if a.elasticInspectionPending == nil {
		a.elasticInspectionPending = map[string]*ElasticInspectionResult{}
	}
	prior, had := a.elasticInspectionPending[id]
	a.elasticInspectionPending[id] = result
	if err := a.saveSpool(); err != nil {
		if had {
			a.elasticInspectionPending[id] = prior
		} else {
			delete(a.elasticInspectionPending, id)
		}
		return err
	}
	return nil
}

func (a *Agent) uploadElasticInspectionResult(ctx context.Context, id string, result *ElasticInspectionResult) error {
	var job ElasticInspectionJob
	if result == nil || json.Unmarshal([]byte(result.Job.Payload), &job) != nil || elasticInspectionSpoolKey(job) != id {
		return errors.New("inspection spool upload identity mismatch")
	}
	if err := a.jobRequest(ctx, "POST", "elastic-inspections/"+url.PathEscape(job.ID)+"/result", result, nil); err != nil {
		return err
	}
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if a.elasticInspectionPending[id] != result {
		return nil
	}
	delete(a.elasticInspectionPending, id)
	if err := a.saveSpool(); err != nil {
		a.elasticInspectionPending[id] = result
		return err
	}
	return nil
}

func (a *Agent) replayElasticInspectionResult(ctx context.Context, job ElasticInspectionJob, envelope SignedElasticInspectionJob) (bool, error) {
	a.stateMu.Lock()
	result := a.elasticInspectionPending[job.ID]
	a.stateMu.Unlock()
	if result == nil {
		return false, nil
	}
	var prior ElasticInspectionJob
	if json.Unmarshal([]byte(result.Job.Payload), &prior) != nil {
		return true, errors.New("invalid inspection retry identity")
	}
	// Renew only the same immutable operation. Lease/owner/credential wrapping may
	// change, while destinations, mappings and requested windows remain frozen.
	if prior.ProjectID != job.ProjectID || prior.ConnectionID != job.ConnectionID || prior.ConnectionRevision != job.ConnectionRevision || prior.Kind != job.Kind || !reflect.DeepEqual(prior.Config, job.Config) || !reflect.DeepEqual(prior.Selection, job.Selection) || !reflect.DeepEqual(prior.Indices, job.Indices) || prior.RunID != job.RunID || !prior.FromUTC.Equal(job.FromUTC) || !prior.ToUTC.Equal(job.ToUTC) {
		return true, errors.New("inspection retry differs from immutable operation")
	}
	a.stateMu.Lock()
	previous := result.Job
	result.Job = envelope
	if err := a.saveSpool(); err != nil {
		result.Job = previous
		a.stateMu.Unlock()
		return true, err
	}
	a.stateMu.Unlock()
	lease, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
	defer cancel()
	return true, a.uploadElasticInspectionResult(lease, job.ID, result)
}

func (a *Agent) retryElasticInspectionSpool(ctx context.Context) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if a.providerClosing {
		return
	}
	if a.providerBusy == nil {
		a.providerBusy = map[string]bool{}
	}
	now := time.Now().UTC()
	for id, result := range a.elasticInspectionPending {
		var job ElasticInspectionJob
		if json.Unmarshal([]byte(result.Job.Payload), &job) != nil {
			continue
		}
		key := "elastic-inspection:" + job.ID
		if a.providerBusy[key] {
			continue
		}
		if !job.LeaseExpiresAt.After(now) {
			owned := false
			for _, cursor := range result.CleanupContinuations {
				owned = owned || cursor.KeepAliveUntil.IsZero() || cursor.KeepAliveUntil.After(now)
			}
			if !owned {
				delete(a.elasticInspectionPending, id)
				if a.saveSpool() != nil {
					a.elasticInspectionPending[id] = result
				}
				continue
			}
			// Result transfer never executes an upstream query or decrypts a
			// credential. The controller accepts bounded historical signatures
			// so a cancelled/expired operation can retain failed-close ownership.
			if now.After(job.LeaseExpiresAt.Add(15 * time.Minute)) {
				continue
			}
		}
		select {
		case a.providerSlots <- struct{}{}:
		default:
			return
		}
		a.providerBusy[key] = true
		a.providerWorkers.Add(1)
		go func() {
			defer a.providerWorkers.Done()
			defer func() { <-a.providerSlots; a.stateMu.Lock(); delete(a.providerBusy, key); a.stateMu.Unlock() }()
			lease, cancel := context.WithTimeout(ctx, 10*time.Second)
			if job.LeaseExpiresAt.After(time.Now().UTC()) {
				cancel()
				lease, cancel = context.WithDeadline(ctx, job.LeaseExpiresAt)
			}
			defer cancel()
			_ = a.uploadElasticInspectionResult(lease, id, result)
		}()
	}
}

func (a *Agent) executeElasticInspectionCleanup(ctx context.Context, job ElasticInspectionJob, envelope SignedElasticInspectionJob) error {
	target, approved := a.adapterTarget(RunJob{Pack: "elastic", Destination: job.Config.Endpoint})
	if !approved || len(job.Indices) > 0 && !elasticIndicesApproved(job.Indices, target.Indices) {
		return errors.New("inspection cleanup destination is not locally approved")
	}
	credentials, err := OpenCredentials(elasticInspectionCredentialJob(job), a.cfg.CredentialPrivateKey, time.Now().UTC())
	if err != nil || credentials.Elastic == nil {
		return errors.New("inspection cleanup credential unavailable")
	}
	cfg, err := elasticLocallyApprovedConfig(job.Config, target, false, false)
	if err != nil {
		return err
	}
	client, err := a.newElasticClient(cfg, credentials.Elastic.Runtime())
	if err != nil {
		return err
	}
	defer client.Close()
	lease, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
	defer cancel()
	for _, cursor := range job.CleanupContinuations {
		if cursor.KeepAliveUntil.IsZero() || cursor.KeepAliveUntil.After(time.Now().UTC()) {
			if err := client.CloseContinuation(lease, cursor); err != nil {
				var response *elastic.HTTPError
				if !errors.As(err, &response) || response.Status != http.StatusNotFound {
					return err
				}
			}
		}
	}
	// The acknowledgement repeats the exact signed owned handles. It carries no
	// newly discovered data and cannot reopen a query or component endpoint.
	result := &ElasticInspectionResult{Job: envelope, CleanupContinuations: job.CleanupContinuations}
	key := elasticInspectionSpoolKey(job)
	if err := a.persistElasticInspectionResult(key, result); err != nil {
		return err
	}
	return a.uploadElasticInspectionResult(lease, key, result)
}

func (a *Agent) closeElasticInspectionSpoolHandles(ctx context.Context, results []*ElasticInspectionResult) {
	for _, result := range results {
		job, err := VerifyElasticInspectionJob(result.Job, a.cfg.ControllerPublicKey, a.cfg.CollectorID, time.Now().UTC())
		if err != nil {
			continue
		}
		target, approved := a.adapterTarget(RunJob{Pack: "elastic", Destination: job.Config.Endpoint})
		if !approved {
			continue
		}
		credentials, err := OpenCredentials(elasticInspectionCredentialJob(job), a.cfg.CredentialPrivateKey, time.Now().UTC())
		if err != nil || credentials.Elastic == nil {
			continue
		}
		cfg, err := elasticLocallyApprovedConfig(job.Config, target, false, false)
		if err != nil {
			continue
		}
		client, err := a.newElasticClient(cfg, credentials.Elastic.Runtime())
		if err != nil {
			continue
		}
		for _, cursor := range result.CleanupContinuations {
			_ = client.CloseContinuation(ctx, cursor)
		}
		client.Close()
	}
}
