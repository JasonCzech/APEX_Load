package collectoragent

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/prometheus"
)

// AppDynamicsCredentials is the encrypted wire representation. The provider's
// runtime Credentials deliberately cannot serialize into a public document.
type AppDynamicsCredentials struct {
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	EventsAPIKey string `json:"events_api_key,omitempty"`
}

func AppDynamicsCredentialBundle(c appdynamics.Credentials) Credentials {
	return Credentials{AppDynamics: &AppDynamicsCredentials{ClientID: c.ClientID, ClientSecret: c.ClientSecret, Username: c.Username, Password: c.Password, EventsAPIKey: c.EventsAPIKey}}
}
func (c *AppDynamicsCredentials) Runtime() appdynamics.Credentials {
	if c == nil {
		return appdynamics.Credentials{}
	}
	return appdynamics.Credentials{ClientID: c.ClientID, ClientSecret: c.ClientSecret, Username: c.Username, Password: c.Password, EventsAPIKey: c.EventsAPIKey}
}

// InspectionJob grants only administrative read access. It has no test/run ID.
type InspectionJob struct {
	Provider           string                 `json:"provider,omitempty"`
	Prometheus         *prometheus.JobConfig  `json:"prometheus,omitempty"`
	Selector           string                 `json:"selector,omitempty"`
	Label              string                 `json:"label,omitempty"`
	FromUTC            time.Time              `json:"from_utc,omitempty"`
	ToUTC              time.Time              `json:"to_utc,omitempty"`
	ID                 string                 `json:"id"`
	ProjectID          string                 `json:"project_id"`
	ConnectionID       string                 `json:"connection_id"`
	ConnectionRevision int64                  `json:"connection_revision"`
	CollectorID        string                 `json:"collector_id"`
	Kind               string                 `json:"kind"`
	ApplicationID      string                 `json:"application_id,omitempty"`
	EntityKind         string                 `json:"entity_kind,omitempty"`
	Path               string                 `json:"path,omitempty"`
	Config             appdynamics.Config     `json:"config"`
	Selection          *appdynamics.Selection `json:"selection,omitempty"`
	Credentials        *SealedCredentials     `json:"credentials"`
	OwnerToken         string                 `json:"owner_token"`
	LeaseExpiresAt     time.Time              `json:"lease_expires_at"`
}
type SignedInspectionJob struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type InspectionResult struct {
	PrometheusCapabilities *prometheus.Capabilities            `json:"prometheus_capabilities,omitempty"`
	Points                 []analysis.MetricPoint              `json:"points,omitempty"`
	Coverage               map[string]monitorprovider.Coverage `json:"coverage,omitempty"`
	Job                    SignedInspectionJob                 `json:"job"`
	Capabilities           appdynamics.Capabilities            `json:"capabilities"`
	Entities               []appdynamics.Entity                `json:"entities,omitempty"`
	Message                string                              `json:"message,omitempty"`
	Truncated              bool                                `json:"truncated,omitempty"`
}

func SignInspectionJob(job InspectionJob, key ed25519.PrivateKey) (SignedInspectionJob, error) {
	if len(key) != ed25519.PrivateKeySize {
		return SignedInspectionJob{}, errors.New("inspection signing key unavailable")
	}
	body, err := json.Marshal(job)
	if err != nil || len(body) > 96<<10 {
		return SignedInspectionJob{}, errors.New("inspection job exceeds limit")
	}
	return SignedInspectionJob{Payload: string(body), Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(key, body))}, nil
}
func VerifyInspectionJob(envelope SignedInspectionJob, key ed25519.PublicKey, collector string, now time.Time) (InspectionJob, error) {
	var job InspectionJob
	sig, err := base64.RawStdEncoding.DecodeString(envelope.Signature)
	if err != nil || len(key) != ed25519.PublicKeySize || len(envelope.Payload) > 96<<10 || !ed25519.Verify(key, []byte(envelope.Payload), sig) {
		return job, errors.New("inspection signature rejected")
	}
	if json.Unmarshal([]byte(envelope.Payload), &job) != nil || job.ID == "" || job.ProjectID == "" || job.ConnectionID == "" || job.ConnectionRevision < 1 || job.CollectorID != collector || job.OwnerToken == "" || !job.LeaseExpiresAt.After(now) || job.LeaseExpiresAt.After(now.Add(2*time.Minute)) || (job.Kind != "test" && job.Kind != "discover" && job.Kind != "preview") || (job.Prometheus == nil && job.Config.Validate() != nil) || (job.Prometheus != nil && (job.Provider != "prometheus" || job.Prometheus.Config.Validate() != nil)) {
		return InspectionJob{}, errors.New("inspection identity or lease rejected")
	}
	if job.Prometheus == nil && job.Kind == "preview" {
		return InspectionJob{}, errors.New("preview requires Prometheus assignment")
	}
	if job.Prometheus != nil && job.Kind == "preview" && (len(job.Prometheus.Selection.Queries) == 0 || !job.ToUTC.After(job.FromUTC) || job.ToUTC.Sub(job.FromUTC) > 10*time.Minute || job.ToUTC.After(now.Add(30*time.Second))) {
		return InspectionJob{}, errors.New("invalid preview window")
	}
	if job.Prometheus != nil && len(job.Prometheus.Selection.Queries) > 0 && (job.Prometheus.Selection.ProjectID != job.ProjectID || job.Prometheus.Selection.Validate() != nil) {
		return InspectionJob{}, errors.New("Prometheus inspection selection rejected")
	}
	if job.Selection != nil && (job.Kind != "test" || job.Selection.ProjectID != job.ProjectID || job.Selection.Validate() != nil) {
		return InspectionJob{}, errors.New("inspection selection rejected")
	}
	return job, nil
}
func inspectionCredentialJob(job InspectionJob) RunJob {
	if job.Prometheus != nil {
		return RunJob{ID: "inspection:" + job.ID, ProjectID: job.ProjectID, CollectorID: job.CollectorID, SourceID: job.ConnectionID, SourceRevision: job.ConnectionRevision, Pack: "prometheus-inspection", Destination: job.Prometheus.Config.Endpoint, LeaseExpiresAt: job.LeaseExpiresAt, Credentials: job.Credentials}
	}
	return RunJob{ID: "inspection:" + job.ID, ProjectID: job.ProjectID, CollectorID: job.CollectorID, SourceID: job.ConnectionID, SourceRevision: job.ConnectionRevision, Pack: "appdynamics-inspection", Destination: job.Config.Endpoint, LeaseExpiresAt: job.LeaseExpiresAt, Credentials: job.Credentials}
}
func SealInspectionCredentials(job InspectionJob, key []byte, c Credentials) (*SealedCredentials, error) {
	return SealCredentials(inspectionCredentialJob(job), key, c)
}

func (a *Agent) dispatchAppDynamicsJob(ctx context.Context, job RunJob, envelope SignedRunJob) {
	// Caller holds stateMu. The bounded worker set keeps provider requests away
	// from availability heartbeats and unrelated monitor packs.
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
		if job.Prometheus != nil {
			_ = a.collectPrometheusJob(ctx, job, envelope)
		} else {
			_ = a.collectAppDynamicsJob(ctx, job, envelope)
		}
	}()
}
func (a *Agent) collectAppDynamicsJob(ctx context.Context, job RunJob, envelope SignedRunJob) error {
	target, approved := a.adapterTarget(job)
	if !approved || job.AppDynamics == nil || job.AppDynamics.Validate() != nil || job.AppDynamics.Config.Endpoint != target.Destination {
		return errors.New("AppDynamics job configuration is not locally approved")
	}
	cfg := job.AppDynamics.Config
	// Trust/egress approval is local. A controller grant cannot broaden it.
	cfg.ApprovedCIDRs = append([]string(nil), target.ApprovedCIDRs...)
	cfg.CAPEM = target.CAPEM
	if target.EventsEndpoint != "" && cfg.EventsEndpoint != target.EventsEndpoint {
		return errors.New("Analytics endpoint is not locally approved")
	}
	if cfg.EventsEndpoint != "" && target.EventsEndpoint == "" {
		return errors.New("Analytics endpoint requires local approval")
	}
	if len(target.Applications) > 0 {
		for _, id := range job.AppDynamics.Selection.Applications {
			if !slices.Contains(target.Applications, id) {
				return errors.New("application scope is not locally approved")
			}
		}
	}
	return a.collectProviderJob(ctx, job, envelope, func(collectCtx context.Context, from, to time.Time) (providerResult, error) {
		credentials, err := OpenCredentials(job, a.cfg.CredentialPrivateKey, time.Now().UTC())
		if err != nil || credentials.AppDynamics == nil {
			return providerResult{}, errors.New("AppDynamics credential lease unavailable")
		}
		client, err := appdynamics.NewClient(cfg, credentials.AppDynamics.Runtime())
		if err != nil {
			return providerResult{}, err
		}
		result, err := client.Collect(collectCtx, job.AppDynamics.Selection, from, to, job.RunID)
		return providerResult{Points: result.Points, Evidence: result.Evidence, Topology: result.Topology, Coverage: result.Coverage, Progress: result.Progress, Capabilities: result.Capabilities}, err
	})
}
func (a *Agent) collectProviderJob(ctx context.Context, job RunJob, envelope SignedRunJob, collect func(context.Context, time.Time, time.Time) (providerResult, error)) error {
	now := time.Now().UTC()
	a.stateMu.Lock()
	pending := a.pendingBatches[job.ID]
	if pending != nil && pending.Sequence < job.NextSequence {
		delete(a.pendingBatches, job.ID)
		pending = nil
	}
	// Prometheus current-state diagnostics cannot be recovered by historical
	// queries. Preserve captured pre-end observations across the new final window,
	// while the fresh query plan alone owns continuation and watermark progress.
	if job.Reconciling && job.Prometheus != nil {
		changed := false
		if pending != nil {
			keep, rebound := rebindPrometheusCapturedBatch(pending, job)
			changed = rebound
			if !keep {
				delete(a.pendingBatches, job.ID)
				pending = nil
			}
		}
		backlog := a.providerBacklog[job.ID]
		retained := backlog[:0]
		for _, batch := range backlog {
			keep, rebound := rebindPrometheusCapturedBatch(batch, job)
			changed = changed || rebound
			if keep {
				retained = append(retained, batch)
			}
		}
		a.providerBacklog[job.ID] = retained
		if changed {
			a.nextCollection[job.ID] = now
			if err := a.saveSpool(); err != nil {
				a.stateMu.Unlock()
				return err
			}
		}
	} else if pending != nil && job.Reconciling && (!pending.QueryFromUTC.Equal(job.QueryFromUTC) || !pending.QueryUntilUTC.Equal(job.QueryUntilUTC)) {
		// AppDynamics evidence is recovered through its final historical window.
		delete(a.pendingBatches, job.ID)
		delete(a.providerBacklog, job.ID)
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
		result, collectErr := collect(leaseCtx, from, to)
		cancel()
		// Failed provider reads may still produce capability/coverage evidence. Keep
		// them visible without inventing zero-valued measurements.
		if collectErr != nil && len(result.Coverage) == 0 {
			key := "collection"
			if job.Prometheus != nil {
				// Prometheus ingestion accepts this bounded failure capability even
				// when no approved query could execute. Keep the failed read durable
				// so its receipt can clear the spool and a later poll can recover.
				key = "query"
			}
			result.Coverage = map[string]monitorprovider.Coverage{key: {Status: "unavailable", From: from, To: to, Message: "provider collection failed"}}
		}
		if job.Prometheus != nil && result.Progress.TotalQueries == 0 {
			total := len(job.Prometheus.Selection.Queries)
			if job.Prometheus.Selection.Targets {
				total++
			}
			if job.Prometheus.Selection.Alerts {
				total++
			}
			if job.Reconciling {
				total += len(job.TerminalWindows)
			}
			result.Progress = monitorprovider.QueryProgress{TotalQueries: total, NextQuery: job.Prometheus.Selection.QueryOffset, NextTime: job.Prometheus.Selection.QueryTime}
		}
		chunks, err := providerBatches(job, envelope, result, to)
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
			if job.Prometheus != nil && job.Reconciling && acknowledged.Progress == nil {
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

type providerResult struct {
	Points                 []analysis.MetricPoint
	Evidence               []monitorprovider.Diagnostic
	Topology               []monitorprovider.Entity
	Coverage               map[string]monitorprovider.Coverage
	Progress               monitorprovider.QueryProgress
	Capabilities           appdynamics.Capabilities
	PrometheusCapabilities *prometheus.Capabilities
}

func appDynamicsBatches(job RunJob, envelope SignedRunJob, result appdynamics.Result, end time.Time) ([]*RunSampleBatch, error) {
	return providerBatches(job, envelope, providerResult{Points: result.Points, Evidence: result.Evidence, Topology: result.Topology, Coverage: result.Coverage, Progress: result.Progress, Capabilities: result.Capabilities}, end)
}
func providerBatches(job RunJob, envelope SignedRunJob, result providerResult, end time.Time) ([]*RunSampleBatch, error) {
	out := []*RunSampleBatch{}
	points, evidence := result.Points, result.Evidence
	topologyCount := len(result.Topology)
	retainedPoints, retainedEvidence := 0, 0
	for len(points) > 0 || len(evidence) > 0 || len(out) == 0 {
		n, m := min(len(points), 512), min(len(evidence), 100)
		var batch *RunSampleBatch
		for {
			last := n == len(points) && m == len(evidence) || len(out) == 31
			batch = &RunSampleBatch{Job: envelope, Sequence: job.NextSequence + int64(len(out)), Capabilities: result.Capabilities, PrometheusCapabilities: result.PrometheusCapabilities, QueryFromUTC: job.QueryFromUTC, QueryUntilUTC: job.QueryUntilUTC}
			batch.Points = append([]analysis.MetricPoint(nil), points[:n]...)
			batch.Evidence = append([]monitorprovider.Diagnostic(nil), evidence[:m]...)
			for i := range batch.Points {
				p := &batch.Points[i]
				p.ProjectID = job.ProjectID
				p.RunID = job.RunID
				p.ScenarioID = job.ScenarioID
				p.Source = job.SourceID
				p.Host = job.CollectorID
				p.ReceivedAt = time.Time{}
				if err := normalizeAdapterPoint(p); err != nil {
					return nil, err
				}
				normalized, err := analysis.NormalizeRunClock(*p, job.StartedAt)
				if err != nil {
					return nil, err
				}
				*p = normalized
			}
			if last {
				batch.Coverage = make(map[string]monitorprovider.Coverage, len(result.Coverage)+2)
				for key, coverage := range result.Coverage {
					batch.Coverage[key] = coverage
				}
				batch.Topology = result.Topology[:topologyCount]
				truncated := n < len(points) || m < len(evidence) || topologyCount < len(result.Topology)
				if truncated {
					for key, coverage := range batch.Coverage {
						if job.Prometheus != nil {
							// Preserve the approved query/diagnostic capability identity.
							// Generic retention capabilities are outside the signed
							// Prometheus ingestion scope and would strand this chunk.
							coverage.Truncated = true
							if coverage.Status == "complete" || coverage.Status == "no_data" {
								coverage.Status = "partial"
							}
							coverage.Message = "collector transport retention budget reached; bounded prefix retained"
							batch.Coverage[key] = coverage
						} else if coverage.Status == "complete" {
							coverage.Status, coverage.Truncated = "partial", true
							coverage.Message = "collector transport retention budget reached"
							batch.Coverage[key] = coverage
						}
					}
					if job.Prometheus == nil {
						batch.Coverage["retention"] = monitorprovider.Coverage{Status: "partial", From: job.QueryFromUTC, To: job.QueryUntilUTC, Points: retainedPoints + n, Evidence: retainedEvidence + m, Truncated: true, Message: "collector transport retention budget reached; bounded prefix retained"}
					}
					if topologyCount < len(result.Topology) {
						batch.Coverage["topology"] = monitorprovider.Coverage{Status: "partial", From: job.QueryFromUTC, To: job.QueryUntilUTC, Truncated: true, Message: "topology exceeds collector batch byte budget"}
					}
				}
				if result.Progress.TotalQueries > 0 {
					progress := result.Progress
					batch.Progress = &progress
					if progress.Complete {
						until := end.UTC()
						batch.QueryWindowEnd = &until
						batch.Final = job.Reconciling
					}
				}
			}
			// Size the complete payload, including final coverage, topology,
			// progress and the signed authorization, before accepting a chunk.
			encoded, err := json.Marshal(batch)
			if err != nil {
				return nil, err
			}
			if len(encoded) <= 768<<10 {
				break
			}
			if n > 1 {
				n /= 2
			} else if m > 1 {
				m /= 2
			} else if last && topologyCount > 0 {
				topologyCount /= 2
			} else if n > 0 && m > 0 {
				m = 0
			} else {
				return nil, errors.New("provider record exceeds transport budget")
			}
		}
		out = append(out, batch)
		retainedPoints, retainedEvidence = retainedPoints+n, retainedEvidence+m
		points, evidence = points[n:], evidence[m:]
		if len(out) == 32 {
			return out, nil
		}
	}
	return out, nil
}

func (a *Agent) inspectionTick(ctx context.Context) error {
	capacity := cap(a.providerSlots) - len(a.providerSlots)
	if capacity < 1 {
		return nil
	}
	var jobs []SignedInspectionJob
	if err := a.jobRequest(ctx, "GET", fmt.Sprintf("inspections?capacity=%d", capacity), nil, &jobs); err != nil {
		return err
	}
	if len(jobs) > capacity {
		return errors.New("too many inspection jobs")
	}
	for _, envelope := range jobs {
		job, err := VerifyInspectionJob(envelope, a.cfg.ControllerPublicKey, a.cfg.CollectorID, time.Now().UTC())
		if err != nil {
			return err
		}
		a.stateMu.Lock()
		key := "inspection:" + job.ID
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
			_ = a.executeInspection(ctx, job, envelope)
		}()
	}
	return nil
}
func (a *Agent) executeInspection(ctx context.Context, job InspectionJob, envelope SignedInspectionJob) error {
	if job.Prometheus != nil {
		return a.executePrometheusInspection(ctx, job, envelope)
	}
	target, approved := a.adapterTarget(RunJob{Pack: "appdynamics", Destination: job.Config.Endpoint})
	if !approved {
		return errors.New("inspection destination is not locally approved")
	}
	credentials, err := OpenCredentials(inspectionCredentialJob(job), a.cfg.CredentialPrivateKey, time.Now().UTC())
	if err != nil || credentials.AppDynamics == nil {
		return errors.New("inspection credential unavailable")
	}
	cfg := job.Config
	cfg.ApprovedCIDRs = append([]string(nil), target.ApprovedCIDRs...)
	cfg.CAPEM = target.CAPEM
	if cfg.EventsEndpoint != "" && cfg.EventsEndpoint != target.EventsEndpoint {
		return errors.New("inspection Analytics endpoint is not locally approved")
	}
	client, err := appdynamics.NewClient(cfg, credentials.AppDynamics.Runtime())
	if err != nil {
		return err
	}
	leaseCtx, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
	defer cancel()
	result := InspectionResult{Job: envelope}
	if job.Kind == "test" {
		result.Capabilities, err = inspectAppDynamicsCapabilities(leaseCtx, client, job.Selection, target.Applications)
	} else {
		if len(target.Applications) > 0 && job.ApplicationID != "" && !slices.Contains(target.Applications, job.ApplicationID) {
			return errors.New("inspection application is not locally approved")
		}
		result.Entities, err = client.Discover(leaseCtx, job.ApplicationID, job.EntityKind, job.Path)
		if job.EntityKind == "applications" && len(target.Applications) > 0 {
			filtered := result.Entities[:0]
			for _, entity := range result.Entities {
				if slices.Contains(target.Applications, entity.ID) {
					filtered = append(filtered, entity)
				}
			}
			result.Entities = filtered
		}
	}
	if err != nil {
		result.Message = "AppDynamics inspection failed"
	}
	if len(result.Entities) >= 200 {
		result.Truncated = true
		result.Entities = result.Entities[:200]
	}
	if encoded, e := json.Marshal(result); e != nil || len(encoded) > 1<<20 {
		return fmt.Errorf("inspection result exceeds transport limit")
	}
	return a.jobRequest(leaseCtx, "POST", "inspections/"+url.PathEscape(job.ID)+"/result", result, nil)
}

type appDynamicsInspectionClient interface {
	Test(context.Context) (appdynamics.Capabilities, error)
	TestApplications(context.Context, []string) (appdynamics.Capabilities, error)
	TestSelection(context.Context, appdynamics.Selection) (appdynamics.Capabilities, error)
}

func inspectAppDynamicsCapabilities(ctx context.Context, client appDynamicsInspectionClient, selection *appdynamics.Selection, approvedApplications []string) (appdynamics.Capabilities, error) {
	if selection != nil {
		for _, application := range selection.Applications {
			if len(approvedApplications) > 0 && !slices.Contains(approvedApplications, application) {
				return appdynamics.Capabilities{}, errors.New("inspection application is not locally approved")
			}
		}
		return client.TestSelection(ctx, *selection)
	}
	if len(approvedApplications) > 0 {
		return client.TestApplications(ctx, approvedApplications)
	}
	return client.Test(ctx)
}

func slicesContainsAppDynamics(packs []string) bool { return slices.Contains(packs, "appdynamics") }
