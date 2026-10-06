package collectoragent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/elastic"
	"net/url"
	"path"
	"time"
)

type ElasticComponentCredentials struct {
	AuthType      string `json:"auth_type"`
	APIKey        string `json:"api_key,omitempty"`
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	BearerToken   string `json:"bearer_token,omitempty"`
	ClientCertPEM string `json:"client_cert_pem,omitempty"`
	ClientKeyPEM  string `json:"client_key_pem,omitempty"`
}
type ElasticCredentials struct {
	APIKey        string                      `json:"api_key,omitempty"`
	Username      string                      `json:"username,omitempty"`
	Password      string                      `json:"password,omitempty"`
	BearerToken   string                      `json:"bearer_token,omitempty"`
	ClientCertPEM string                      `json:"client_cert_pem,omitempty"`
	ClientKeyPEM  string                      `json:"client_key_pem,omitempty"`
	Kibana        ElasticComponentCredentials `json:"kibana,omitempty"`
	Logstash      ElasticComponentCredentials `json:"logstash,omitempty"`
}

func componentCredentialBundle(c elastic.ComponentCredentials) ElasticComponentCredentials {
	return ElasticComponentCredentials{c.AuthType, c.APIKey, c.Username, c.Password, c.BearerToken, c.ClientCertPEM, c.ClientKeyPEM}
}
func (c ElasticComponentCredentials) Runtime() elastic.ComponentCredentials {
	return elastic.ComponentCredentials{AuthType: c.AuthType, APIKey: c.APIKey, Username: c.Username, Password: c.Password, BearerToken: c.BearerToken, ClientCertPEM: c.ClientCertPEM, ClientKeyPEM: c.ClientKeyPEM}
}
func ElasticCredentialBundle(c elastic.Credentials) Credentials {
	return Credentials{Elastic: &ElasticCredentials{c.APIKey, c.Username, c.Password, c.BearerToken, c.ClientCertPEM, c.ClientKeyPEM, componentCredentialBundle(c.Kibana), componentCredentialBundle(c.Logstash)}}
}
func (c *ElasticCredentials) Runtime() elastic.Credentials {
	if c == nil {
		return elastic.Credentials{}
	}
	return elastic.Credentials{APIKey: c.APIKey, Username: c.Username, Password: c.Password, BearerToken: c.BearerToken, ClientCertPEM: c.ClientCertPEM, ClientKeyPEM: c.ClientKeyPEM, Kibana: c.Kibana.Runtime(), Logstash: c.Logstash.Runtime()}
}

func (a *Agent) newElasticClient(cfg elastic.Config, credentials elastic.Credentials) (*elastic.Client, error) {
	if a.elasticClient != nil {
		return a.elasticClient(cfg, credentials)
	}
	return elastic.NewClient(cfg, credentials)
}
func elasticIndicesApproved(indices, approved []string) bool {
	if len(indices) == 0 || len(approved) == 0 {
		return false
	}
	for _, index := range indices {
		found := false
		for _, scope := range approved {
			if index == scope {
				found = true
				break
			}
			if !containsWildcard(index) {
				match, err := path.Match(scope, index)
				if err == nil && match {
					found = true
					break
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func containsWildcard(s string) bool {
	for _, r := range s {
		if r == '*' || r == '?' || r == '[' {
			return true
		}
	}
	return false
}

// Only selected components enter a collection client's fixed destinations.
// Local trust always replaces controller-supplied trust, including the main
// search endpoint. A connection may contain other optional component profiles.
func elasticLocallyApprovedConfig(cfg elastic.Config, target AdapterTarget, kibana, logstash bool) (elastic.Config, error) {
	if kibana && (cfg.KibanaEndpoint == "" || cfg.KibanaEndpoint != target.KibanaEndpoint) || logstash && (cfg.LogstashEndpoint == "" || cfg.LogstashEndpoint != target.LogstashEndpoint) {
		return elastic.Config{}, errors.New("Elastic component destination is not locally approved")
	}
	cfg.ApprovedCIDRs = append([]string(nil), target.ApprovedCIDRs...)
	cfg.CAPEM = target.CAPEM
	cfg.KibanaCAPEM, cfg.KibanaApprovedCIDRs = target.KibanaCAPEM, append([]string(nil), target.KibanaApprovedCIDRs...)
	cfg.LogstashCAPEM, cfg.LogstashApprovedCIDRs = target.LogstashCAPEM, append([]string(nil), target.LogstashApprovedCIDRs...)
	if !kibana {
		cfg.KibanaEndpoint, cfg.KibanaCAPEM, cfg.KibanaApprovedCIDRs = "", "", nil
	}
	if !logstash {
		cfg.LogstashEndpoint, cfg.LogstashCAPEM, cfg.LogstashApprovedCIDRs = "", "", nil
	}
	return cfg, nil
}

func (a *Agent) dispatchElasticJob(ctx context.Context, job RunJob, envelope SignedRunJob) {
	// Caller holds stateMu. The bounded worker set keeps provider requests away
	// from availability heartbeats and unrelated monitor packs.
	if a.providerClosing {
		return
	}
	if a.providerBusy == nil {
		a.providerBusy = map[string]bool{}
	}
	if a.providerBusy[job.ID] {
		return
	}
	if a.pendingBatches[job.ID] == nil && len(a.providerBacklog[job.ID]) == 0 && time.Now().Before(a.nextCollection[job.ID]) {
		return
	}
	select {
	case a.providerSlots <- struct{}{}:
	default:
		return
	}
	a.providerBusy[job.ID] = true
	a.providerWorkers.Add(1)
	go func() {
		defer a.providerWorkers.Done()
		defer func() { <-a.providerSlots; a.stateMu.Lock(); delete(a.providerBusy, job.ID); a.stateMu.Unlock() }()
		_ = a.collectElasticJob(ctx, job, envelope)
	}()
}
func (a *Agent) collectElasticJob(ctx context.Context, job RunJob, envelope SignedRunJob) error {
	target, approved := a.adapterTarget(job)
	if !approved || job.Elastic == nil || job.Elastic.Validate() != nil || job.Elastic.Config.Endpoint != target.Destination {
		return errors.New("Elastic job is not locally approved")
	}
	for _, query := range job.Elastic.Selection.Queries {
		if query.Language == "esql" && !target.ElasticESQL {
			return errors.New("ES|QL is not locally approved")
		}
	}
	if (job.Elastic.Selection.ClusterHealth || job.Elastic.Selection.KibanaHealth || job.Elastic.Selection.LogstashHealth) && !target.ElasticHealth {
		return errors.New("component health is not locally approved")
	}
	cfg, err := elasticLocallyApprovedConfig(job.Elastic.Config, target, job.Elastic.Selection.KibanaHealth, job.Elastic.Selection.LogstashHealth)
	if err != nil {
		return err
	}
	// Bound maximum-width records within the signed durable spool budget. The
	// provider returns a checkpoint for the unaccepted suffix.
	cfg.MaxPoints = min(cfg.Defaults().MaxPoints, 512)
	cfg.MaxEvidence = min(cfg.Defaults().MaxEvidence, 512)
	// Component snapshots may have no index scope. An explicitly selected
	// index (including optional index statistics) still needs local approval.
	if (len(job.Elastic.Selection.Indices) > 0 || elastic.HasIndexedSignals(job.Elastic.Selection)) && !elasticIndicesApproved(job.Elastic.Selection.Indices, target.Indices) {
		return errors.New("Elastic index scope is not locally approved")
	}
	now := time.Now().UTC()
	if err := a.retireObsoleteElasticSpool(ctx, job); err != nil {
		return err
	}
	a.stateMu.Lock()
	pending := a.pendingBatches[job.ID]
	if pending != nil && pending.Sequence < job.NextSequence {
		delete(a.pendingBatches, job.ID)
		pending = nil
	}
	if pending == nil && a.providerBacklog != nil && len(a.providerBacklog[job.ID]) > 0 {
		pending = a.providerBacklog[job.ID][0]
		a.providerBacklog[job.ID] = a.providerBacklog[job.ID][1:]
		pending.Sequence = job.NextSequence
		pending.Job = envelope
		a.pendingBatches[job.ID] = pending
	}
	until := a.nextCollection[job.ID]
	a.stateMu.Unlock()
	if pending == nil {
		a.stateMu.Lock()
		atCapacity := len(a.pendingBatches) >= maxCollectorSpoolJobs
		a.stateMu.Unlock()
		if atCapacity {
			return errors.New("collector retry storage job budget reached")
		}
		if now.Before(until) {
			return nil
		}
		credentials, err := OpenCredentials(job, a.cfg.CredentialPrivateKey, now)
		if err != nil || credentials.Elastic == nil {
			return errors.New("Elastic credential lease unavailable")
		}
		client, err := a.newElasticClient(cfg, credentials.Elastic.Runtime())
		if err != nil {
			return err
		}
		defer client.Close()
		client.SetVerificationPolicy(elastic.VerificationPolicy{ESQL: target.ElasticESQL, Health: target.ElasticHealth})
		if job.Elastic.Capabilities != nil {
			_ = client.UseVerifiedCapabilities(*job.Elastic.Capabilities)
		}
		from, to := job.QueryFromUTC, job.QueryUntilUTC
		if from.IsZero() {
			from = job.StartedAt
		}
		if to.IsZero() {
			to = now
		}
		if job.LoadEndedAt != nil && to.After(*job.LoadEndedAt) {
			to = *job.LoadEndedAt
		}
		if !to.After(from) {
			return nil
		}
		leaseCtx, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
		selection := job.Elastic.Selection
		selection.ResolvedIndices = job.Elastic.ResolvedIndices
		selection.Continuation = job.Elastic.Continuation
		selection.DisabledQueries = job.Elastic.DisabledQueries
		selection.LastHealthAt = job.Elastic.LastHealthAt
		selection.HealthCoverage = job.Elastic.HealthCoverage
		selection.FinalHealthAfter = job.Elastic.FinalHealthAfter
		selection.TerminalWindows = job.Elastic.TerminalWindows
		selection.ObservedTraceIDs, selection.CorrelationLimitReached = job.Elastic.ObservedTraceIDs, job.Elastic.CorrelationLimitReached
		selection.IngestionFrom = job.Elastic.IngestionFrom
		result, collectErr := client.Collect(leaseCtx, selection, from, to, job.RunID)
		cancel()
		checkpointPersisted := false
		defer func() {
			if !checkpointPersisted && (result.Continuation != nil || len(result.CleanupContinuations) > 0) {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, cursor := range append(append([]*analysis.ProviderContinuation(nil), result.CleanupContinuations...), result.Continuation) {
					if cursor != nil {
						_ = client.CloseContinuation(cleanup, cursor)
					}
				}
			}
		}()
		// Failed provider reads may still produce capability/coverage evidence. Keep
		// them visible without inventing zero-valued measurements.
		if collectErr != nil && len(result.Coverage) == 0 {
			result.Coverage = map[string]analysis.ProviderCoverage{"collection": {Status: "unavailable", From: from, To: to, Message: "Elastic collection failed"}}
		}
		chunks, err := elasticBatches(job, envelope, result, to)
		if err != nil {
			return err
		}
		a.stateMu.Lock()
		if _, exists := a.pendingBatches[job.ID]; !exists {
			err = a.admitMonitorRetry(job.ID, chunks[0], chunks[1:])
			if err == nil {
				pending = a.pendingBatches[job.ID]
			}
		} else {
			pending = a.pendingBatches[job.ID]
			err = a.saveSpool()
		}
		a.stateMu.Unlock()
		if err != nil {
			return err
		}
		checkpointPersisted = true
	}
	uploadCtx, stop := context.WithDeadline(ctx, job.LeaseExpiresAt)
	defer stop()
	for sent := 0; sent < max(1, job.MaxBatches); sent++ {
		a.stateMu.Lock()
		previousEnvelope := pending.Job
		pending.Job = envelope
		err := a.saveSpool()
		if err != nil {
			pending.Job = previousEnvelope
		}
		a.stateMu.Unlock()
		if err != nil {
			return err
		}
		if err = a.jobRequest(uploadCtx, "POST", "jobs/"+url.PathEscape(job.ID)+"/samples", pending, nil); err != nil {
			return err
		}
		a.stateMu.Lock()
		acknowledged := pending
		delete(a.pendingBatches, job.ID)
		if len(a.providerBacklog[job.ID]) > 0 {
			pending = a.providerBacklog[job.ID][0]
			a.providerBacklog[job.ID] = a.providerBacklog[job.ID][1:]
			pending.Sequence = acknowledged.Sequence + 1
			pending.Job = envelope
			a.pendingBatches[job.ID] = pending
		} else {
			pending = nil
			a.nextCollection[job.ID] = now.Add(time.Duration(job.IntervalSeconds) * time.Second)
			if job.Reconciling {
				a.nextCollection[job.ID] = now.Add(15 * time.Second)
			}
			if acknowledged.Progress != nil && !acknowledged.Progress.Complete {
				a.nextCollection[job.ID] = now
			}
		}
		err = a.saveSpool()
		a.stateMu.Unlock()
		if err != nil {
			return err
		}
		if pending == nil {
			return nil
		}
	}
	return nil
}

func elasticBatches(job RunJob, envelope SignedRunJob, result elastic.Result, end time.Time) ([]*RunSampleBatch, error) {
	out := []*RunSampleBatch{}
	points, evidence := result.Points, result.Evidence
	for i := range evidence {
		evidence[i].ReceivedAt = time.Time{}
		evidence[i] = scrubElasticEvidence(evidence[i])
	}
	for len(points) > 0 || len(evidence) > 0 || len(out) == 0 {
		if len(out) >= 32 {
			return nil, errors.New("Elastic result exceeds durable grant batch budget")
		}
		n, m := min(512, len(points)), min(100, len(evidence))
		var batch *RunSampleBatch
		for {
			batch = &RunSampleBatch{ResolvedIndices: append([]string(nil), result.ResolvedIndices...), Job: envelope, Sequence: job.NextSequence + int64(len(out)), QueryFromUTC: job.QueryFromUTC, QueryUntilUTC: job.QueryUntilUTC, Points: append([]analysis.MetricPoint(nil), points[:n]...), Evidence: append([]analysis.ProviderEvidence(nil), evidence[:m]...)}
			for i := range batch.Points {
				p := &batch.Points[i]
				p.ProjectID = job.ProjectID
				p.RunID = job.RunID
				p.ScenarioID = job.ScenarioID
				p.Source = job.SourceID
				p.Host = job.CollectorID
				p.ReceivedAt = time.Time{}
				normalized, err := analysis.NormalizeRunClock(*p, job.StartedAt)
				if err != nil {
					return nil, err
				}
				*p = normalized
			}
			if n == len(points) && m == len(evidence) {
				batch.CleanupContinuations = result.CleanupContinuations
				batch.Coverage = result.Coverage
				progress := result.Progress
				progress.Continuation = result.Continuation
				batch.Progress = &progress
				if progress.Complete {
					until := end.UTC()
					batch.QueryWindowEnd = &until
					batch.Final = job.Reconciling
				}
			}
			b, err := json.Marshal(batch)
			if err != nil {
				return nil, err
			}
			if len(b) <= 768<<10 {
				break
			}
			if n > 1 {
				n /= 2
			} else if m > 1 {
				m /= 2
			} else {
				return nil, errors.New("Elastic record exceeds transport budget")
			}
		}
		out = append(out, batch)
		points, evidence = points[n:], evidence[m:]
	}
	return out, nil
}
