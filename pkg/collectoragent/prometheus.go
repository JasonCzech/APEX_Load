package collectoragent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/prometheus"
)

// Runtime provider credentials serialize only inside the sealed envelope.
type PrometheusCredentials struct {
	Token             string `json:"token,omitempty"`
	Username          string `json:"username,omitempty"`
	Password          string `json:"password,omitempty"`
	ClientSecret      string `json:"client_secret,omitempty"`
	ClientCertificate string `json:"client_certificate,omitempty"`
	ClientKey         string `json:"client_key,omitempty"`
}

func PrometheusCredentialBundle(c prometheus.Credentials) Credentials {
	return Credentials{Prometheus: &PrometheusCredentials{Token: c.Token, Username: c.Username, Password: c.Password, ClientSecret: c.ClientSecret, ClientCertificate: c.ClientCertificate, ClientKey: c.ClientKey}}
}
func (c *PrometheusCredentials) Runtime() prometheus.Credentials {
	if c == nil {
		return prometheus.Credentials{}
	}
	return prometheus.Credentials{Token: c.Token, Username: c.Username, Password: c.Password, ClientSecret: c.ClientSecret, ClientCertificate: c.ClientCertificate, ClientKey: c.ClientKey}
}
func hasPrometheusQueryTarget(c Config) bool {
	for _, t := range c.AdapterTargets {
		if t.Pack == "prometheus-query" && t.Prometheus != nil {
			return true
		}
	}
	return false
}
func (a *Agent) hasPrometheusQueryTarget() bool {
	return slices.Contains(a.cfg.Packs, "prometheus-query") && hasPrometheusQueryTarget(a.cfg)
}

// Rebind only captured collecting batches. Reconciliation batches already carry
// the unified immutable query cursor and must retain it on retries.
func rebindPrometheusCapturedBatch(batch *RunSampleBatch, job RunJob) (keep, changed bool) {
	if batch == nil || job.LoadEndedAt == nil {
		return batch != nil, false
	}
	var prior RunJob
	oldGrant := json.Unmarshal([]byte(batch.Job.Payload), &prior) != nil || !prior.Reconciling
	if !oldGrant && batch.QueryFromUTC.Equal(job.QueryFromUTC) && batch.QueryUntilUTC.Equal(job.QueryUntilUTC) {
		return true, false
	}
	from, to := job.QueryFromUTC, job.QueryUntilUTC
	if from.IsZero() {
		from = job.StartedAt
	}
	if to.IsZero() || to.After(*job.LoadEndedAt) {
		to = *job.LoadEndedAt
	}
	points := batch.Points[:0]
	for _, point := range batch.Points {
		if !point.TimestampUTC.Before(from) && !point.TimestampUTC.After(to) {
			points = append(points, point)
		}
	}
	batch.Points = points
	evidence := batch.Evidence[:0]
	for _, record := range batch.Evidence {
		if !record.TimestampUTC.Before(job.StartedAt) && !record.TimestampUTC.After(to) {
			evidence = append(evidence, record)
		}
	}
	batch.Evidence = evidence
	coverage := map[string]monitorprovider.Coverage{}
	for _, kind := range []string{"targets", "alerts"} {
		if cov, ok := batch.Coverage[kind]; ok && cov.ObservedAt != nil && !cov.ObservedAt.Before(job.StartedAt) && !cov.ObservedAt.After(to) {
			cov.From, cov.To = from, to
			coverage[kind] = cov
		}
	}
	batch.Coverage = coverage
	batch.Topology = nil
	batch.Progress, batch.QueryWindowEnd, batch.Final = nil, nil, false
	batch.QueryFromUTC, batch.QueryUntilUTC = from, to
	return len(batch.Points)+len(batch.Evidence)+len(batch.Coverage) > 0, true
}
func prometheusNeedsSealing(c prometheus.Config) bool {
	c = c.Defaults()
	return c.MTLS || c.AuthType == "basic" || c.AuthType == "bearer" || c.AuthType == "azure" && c.AzureCredentialMode == "service_principal"
}
func (a *Agent) localPrometheusConfig(job RunJob) (prometheus.Config, error) {
	target, approved := a.adapterTarget(job)
	if !approved || target.Prometheus == nil || job.Prometheus == nil || job.Prometheus.Config.Validate() != nil || job.Prometheus.Config.Endpoint != target.Destination {
		return prometheus.Config{}, errors.New("Prometheus query configuration is not locally approved")
	}
	cfg, local := job.Prometheus.Config.Defaults(), target.Prometheus.Defaults()
	if cfg.AuthType != local.AuthType || cfg.BackendProfile != local.BackendProfile || cfg.ClientID != local.ClientID || cfg.AzureTenantID != local.AzureTenantID || cfg.AzureCredentialMode != local.AzureCredentialMode || cfg.MTLS != local.MTLS || cfg.ContextPath != local.ContextPath {
		return prometheus.Config{}, errors.New("Prometheus authentication identity is not locally approved")
	}
	cfg.ApprovedCIDRs = append([]string(nil), local.ApprovedCIDRs...)
	cfg.CAPEM = local.CAPEM
	cfg.AllowPrivateHTTP = local.AllowPrivateHTTP
	return cfg, nil
}
func (a *Agent) prometheusJobCredentials(job RunJob, cfg prometheus.Config) (prometheus.Credentials, error) {
	if job.Credentials == nil {
		if prometheusNeedsSealing(cfg) {
			return prometheus.Credentials{}, errors.New("Prometheus credential lease unavailable")
		}
		return prometheus.Credentials{}, nil
	}
	c, err := OpenCredentials(job, a.cfg.CredentialPrivateKey, time.Now().UTC())
	if err != nil || c.Prometheus == nil {
		return prometheus.Credentials{}, errors.New("Prometheus credential lease unavailable")
	}
	return c.Prometheus.Runtime(), nil
}
func (a *Agent) collectPrometheusJob(ctx context.Context, job RunJob, envelope SignedRunJob) error {
	cfg, err := a.localPrometheusConfig(job)
	if err != nil {
		return err
	}
	if job.Prometheus.Validate() != nil {
		return errors.New("invalid Prometheus query selection")
	}
	return a.collectProviderJob(ctx, job, envelope, func(collectCtx context.Context, from, to time.Time) (providerResult, error) {
		collectCtx, stop := context.WithTimeout(collectCtx, time.Duration(cfg.MaxPollSeconds)*time.Second)
		defer stop()
		credentials, err := a.prometheusJobCredentials(job, cfg)
		if err != nil {
			return providerResult{}, err
		}
		client, err := a.newPrometheusClient(cfg, credentials)
		if err != nil {
			return providerResult{}, err
		}
		selection := job.Prometheus.Selection
		selection.CollectionID = job.ID
		var windows []monitorprovider.EvaluationWindow
		if job.Reconciling {
			windows = job.TerminalWindows
		}
		result, err := client.CollectPlan(collectCtx, selection, from, to, job.RunID, windows)
		return providerResult{Points: result.Points, Evidence: result.Evidence, Coverage: result.Coverage, Topology: result.Topology, Progress: result.Progress, PrometheusCapabilities: &result.Capabilities}, err
	})
}
func (a *Agent) executePrometheusInspection(ctx context.Context, job InspectionJob, envelope SignedInspectionJob) error {
	grant := inspectionCredentialJob(job)
	grant.Pack = "prometheus-query"
	grant.Prometheus = job.Prometheus
	cfg, err := a.localPrometheusConfig(grant)
	if err != nil {
		return err
	}
	// Credential AAD remains the inspection assignment, rather than a run grant.
	grant = inspectionCredentialJob(job)
	credentials, err := a.prometheusJobCredentials(grant, cfg)
	if err != nil {
		return err
	}
	client, err := a.newPrometheusClient(cfg, credentials)
	if err != nil {
		return err
	}
	leaseCtx, cancel := context.WithDeadline(ctx, job.LeaseExpiresAt)
	defer cancel()
	result := InspectionResult{Job: envelope}
	var caps prometheus.Capabilities
	switch job.Kind {
	case "test":
		if len(job.Prometheus.Selection.Queries) > 0 {
			caps, err = client.TestSelection(leaseCtx, job.Prometheus.Selection)
		} else {
			caps, err = client.Test(leaseCtx)
		}
		result.PrometheusCapabilities = &caps
	case "discover":
		result.Entities, err = client.Discover(leaseCtx, job.Selector, job.EntityKind, job.Label)
	case "preview":
		var preview prometheus.Result
		preview, err = client.Collect(leaseCtx, job.Prometheus.Selection, job.FromUTC, job.ToUTC, "inspection:"+job.ID)
		result.Points = preview.Points
		result.Coverage = preview.Coverage
		result.PrometheusCapabilities = &preview.Capabilities
	default:
		return errors.New("unsupported Prometheus inspection")
	}
	if err != nil {
		result.Message = "Prometheus inspection failed"
	}
	if len(result.Entities) >= 200 {
		result.Truncated = true
		result.Entities = result.Entities[:200]
	}
	if len(result.Points) > 512 {
		result.Truncated = true
		result.Points = result.Points[:512]
	}
	if encoded, e := json.Marshal(result); e != nil || len(encoded) > 1<<20 {
		return fmt.Errorf("inspection result exceeds transport limit")
	}
	return a.jobRequest(leaseCtx, "POST", "inspections/"+url.PathEscape(job.ID)+"/result", result, nil)
}

// The per-instance seam is private and fixture-only; production always uses the
// governed SDK transport and exposes no configuration bypass.
func (a *Agent) newPrometheusClient(cfg prometheus.Config, credentials prometheus.Credentials) (*prometheus.Client, error) {
	if a.prometheusClient != nil {
		return a.prometheusClient(cfg, credentials)
	}
	return prometheus.NewClient(cfg, credentials)
}
