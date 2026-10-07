package collectoragent

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/elastic"
)

// Cleanup uses separate signed leases, including after capture is disabled or
// the load ends. A cleanup grant permits releasing only its owned handle.
func (a *Agent) elasticCleanupTick(ctx context.Context) error {
	a.stateMu.Lock()
	after := a.elasticCleanupAfter
	a.stateMu.Unlock()
	query := url.Values{}
	if after != "" {
		query.Set("after_key", after)
	}
	var jobs []SignedRunJob
	var headers http.Header
	if err := a.jobRequestWithHeaders(ctx, "GET", "elastic-cleanups?"+query.Encode(), nil, &jobs, &headers); err != nil {
		return err
	}
	next := headers.Get("X-APEX-Monitor-Next-Key")
	if len(jobs) > 32 || len(next) > maxRunMonitorJobIDBytes || strings.ContainsAny(next, "\r\n\x00") || next != "" && next <= after {
		return errors.New("invalid Elastic cleanup page")
	}
	a.stateMu.Lock()
	a.elasticCleanupAfter = next
	a.stateMu.Unlock()
	for _, envelope := range jobs {
		job, err := VerifyRunJob(envelope, a.cfg.ControllerPublicKey, a.cfg.CollectorID, time.Now().UTC())
		if err != nil || !job.Cleanup {
			return errors.New("Elastic cleanup signature rejected")
		}
		a.stateMu.Lock()
		if a.providerClosing {
			a.stateMu.Unlock()
			return nil
		}
		key := "elastic-cleanup:" + job.ID
		if a.providerBusy == nil {
			a.providerBusy = map[string]bool{}
		}
		if a.providerBusy[key] {
			a.stateMu.Unlock()
			continue
		}
		select {
		case a.providerSlots <- struct{}{}:
		default:
			a.stateMu.Unlock()
			continue
		}
		a.providerBusy[key] = true
		a.providerWorkers.Add(1)
		a.stateMu.Unlock()
		go func() {
			defer a.providerWorkers.Done()
			defer func() { <-a.providerSlots; a.stateMu.Lock(); delete(a.providerBusy, key); a.stateMu.Unlock() }()
			_ = a.executeElasticCleanup(ctx, job, envelope)
		}()
	}
	return nil
}

func (a *Agent) executeElasticCleanup(parent context.Context, job RunJob, envelope SignedRunJob) error {
	if !job.Cleanup || job.Elastic == nil || job.Elastic.Continuation == nil {
		return errors.New("Elastic cleanup handle unavailable")
	}
	target, approved := a.adapterTarget(job)
	if !approved || target.Destination != job.Elastic.Config.Endpoint || !elasticIndicesApproved(job.Elastic.Selection.Indices, target.Indices) {
		return errors.New("Elastic cleanup target is not locally approved")
	}
	creds, err := OpenCredentials(job, a.cfg.CredentialPrivateKey, time.Now().UTC())
	if err != nil || creds.Elastic == nil {
		return errors.New("Elastic cleanup credential unavailable")
	}
	cfg := job.Elastic.Config
	cfg.CAPEM, cfg.ApprovedCIDRs = target.CAPEM, append([]string(nil), target.ApprovedCIDRs...)
	// Cleanup only reaches the main endpoint; exclude unrelated component targets.
	cfg.KibanaEndpoint, cfg.LogstashEndpoint = "", ""
	client, err := a.newElasticClient(cfg, creds.Elastic.Runtime())
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	err = client.CloseContinuation(ctx, job.Elastic.Continuation)
	var response *elastic.HTTPError
	expiry := job.Elastic.Continuation.KeepAliveUntil
	expired := !expiry.IsZero() && !expiry.After(time.Now().UTC())
	if err != nil && !expired && (!errors.As(err, &response) || response.Status != 404) {
		return err
	}
	return a.jobRequest(ctx, "POST", "jobs/"+url.PathEscape(job.ID)+"/cleanup", struct {
		Job SignedRunJob `json:"job"`
	}{envelope}, nil)
}

func (a *Agent) closeElasticSpoolHandles() {
	a.stateMu.Lock()
	inspections := make([]*ElasticInspectionResult, 0, len(a.elasticInspectionPending))
	for _, result := range a.elasticInspectionPending {
		inspections = append(inspections, result)
	}
	batches := make([]RunSampleBatch, 0, len(a.pendingBatches))
	for id, pending := range a.pendingBatches {
		if pending == nil {
			continue
		}
		last := pending
		if backlog := a.providerBacklog[id]; len(backlog) > 0 {
			last = backlog[len(backlog)-1]
		}
		if last != nil {
			batches = append(batches, *last)
		}
	}
	a.stateMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, batch := range batches {
		job, err := VerifyRunJob(batch.Job, a.cfg.ControllerPublicKey, a.cfg.CollectorID, time.Now().UTC())
		if err != nil || job.Pack != "elastic" || job.Elastic == nil {
			continue
		}
		_, cursors, _ := elasticBatchOwnedContinuations(job, &batch)
		for _, cursor := range cursors {
			cleanup := job
			config := *job.Elastic
			config.Continuation = cursor
			cleanup.Elastic = &config
			cleanup.Cleanup = true
			// Shutdown closes owned handles without acknowledging a new grant.
			_ = a.closeElasticHandle(ctx, cleanup)
		}
	}
	a.closeElasticInspectionSpoolHandles(ctx, inspections)
}

func (a *Agent) closeElasticHandle(parent context.Context, job RunJob) error {
	target, ok := a.adapterTarget(job)
	if !ok || job.Elastic == nil || target.Destination != job.Elastic.Config.Endpoint || job.Elastic.Continuation == nil || job.Elastic.Continuation.Validate() != nil || !elasticIndicesApproved(job.Elastic.Selection.Indices, target.Indices) {
		return errors.New("Elastic cleanup target rejected")
	}
	expiry := job.Elastic.Continuation.KeepAliveUntil
	if !expiry.IsZero() && !expiry.After(time.Now().UTC()) {
		return nil
	}
	creds, err := OpenCredentials(job, a.cfg.CredentialPrivateKey, time.Now().UTC())
	if err != nil || creds.Elastic == nil {
		return errors.New("Elastic cleanup credential expired")
	}
	cfg := job.Elastic.Config
	cfg.CAPEM, cfg.ApprovedCIDRs = target.CAPEM, target.ApprovedCIDRs
	cfg.KibanaEndpoint, cfg.LogstashEndpoint = "", ""
	client, err := a.newElasticClient(cfg, creds.Elastic.Runtime())
	if err != nil {
		return err
	}
	defer client.Close()
	err = client.CloseContinuation(parent, job.Elastic.Continuation)
	var response *elastic.HTTPError
	if errors.As(err, &response) && response.Status == http.StatusNotFound {
		return nil
	}
	return err
}
