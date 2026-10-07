package api

import (
	"context"
	"encoding/base64"
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/prometheus"
	"github.com/jasonczech/apexload/pkg/storage"
)

// The quota journal is shared by provider executors, with isolated identity keys.
func (s *Server) prometheusQuotaConfig(ctx context.Context, snapshot *storage.PrometheusConnectionSnapshot, credentials prometheus.Credentials, executor string) (prometheus.Config, error) {
	cfg := snapshot.Config.Defaults()
	if err := ctx.Err(); err != nil {
		return cfg, err
	}
	store, ok := s.store.(interface {
		AllocateMonitorQuota(string, string, time.Time) (bool, error)
	})
	if !ok {
		return cfg, errors.New("monitoring request quota storage unavailable")
	}
	allowed, err := store.AllocateMonitorQuota(prometheus.QuotaKey(cfg, credentials), executor, time.Now().UTC())
	if err != nil || !allowed {
		return cfg, errors.New("Prometheus executor request quota is busy")
	}
	cfg.RequestsPerMinute = max(1, cfg.RequestsPerMinute/2)
	cfg.MaxPoints = min(cfg.MaxPoints, 4096)
	return cfg, nil
}

func prometheusSelection(job storage.RunMonitorJob) prometheus.Selection {
	selection := job.Source.Prometheus.Defaults()
	selection.ProjectID = job.ProjectID
	selection.CollectionID = job.ID
	selection.QueryOffset = job.QueryOffset
	selection.QueryTime = job.QueryTime
	anchor := prometheusGridAnchor(job.StartedAt)
	selection.GridAnchor = &anchor
	if job.State == "reconciling" {
		selection.Targets = false
		selection.Alerts = false
	}
	return selection
}

func prometheusTerminalWindows(job storage.RunMonitorJob) []monitorprovider.EvaluationWindow {
	if job.LoadEndedAt == nil || job.Source.Prometheus == nil {
		return nil
	}
	var windows []monitorprovider.EvaluationWindow
	seen := map[string]bool{}
	add := func(query string, from, to time.Time) {
		if from.Before(job.StartedAt) || !to.After(from) || to.After(*job.LoadEndedAt) {
			return
		}
		key := query + ":" + from.Format(time.RFC3339Nano) + ":" + to.Format(time.RFC3339Nano)
		if !seen[key] {
			seen[key] = true
			windows = append(windows, monitorprovider.EvaluationWindow{QueryID: query, From: from, To: to})
		}
	}
	for _, query := range job.Source.Prometheus.Queries {
		if query.TerminalExpression == "" {
			continue
		}
		add(query.ID, job.StartedAt, *job.LoadEndedAt)
		for _, rule := range job.FrozenMonitorRules {
			if rule.EvaluationMode != "exact_window" || rule.Metric != query.Name || rule.QueryID != "" && rule.QueryID != query.ID {
				continue
			}
			from, to := job.StartedAt.Add(time.Duration(rule.StartOffsetSeconds)*time.Second), *job.LoadEndedAt
			if rule.EndOffsetSeconds != nil {
				to = job.StartedAt.Add(time.Duration(*rule.EndOffsetSeconds) * time.Second)
				if to.After(*job.LoadEndedAt) {
					to = *job.LoadEndedAt
				}
			}
			add(query.ID, from, to)
		}
	}
	return windows
}

func (s *Server) populatePrometheusGrant(ctx context.Context, grant *collectoragent.RunJob, job storage.RunMonitorJob) error {
	snapshot := job.PrometheusConnectionSnapshot
	if snapshot == nil || job.Source.Prometheus == nil || snapshot.ProjectID != job.ProjectID {
		return errors.New("Prometheus job snapshot unavailable")
	}
	credentials, err := resolvePrometheusCredentials(ctx, s.runner.prometheusSecretResolverForProject(job.ProjectID), snapshot)
	if err != nil {
		return err
	}
	cfg, err := s.prometheusQuotaConfig(ctx, snapshot, credentials, "collector:"+job.Source.CollectorID)
	if err != nil {
		return err
	}
	grant.Prometheus = &prometheus.JobConfig{Config: cfg, Selection: prometheusSelection(job)}
	grant.QueryFromUTC, grant.QueryUntilUTC = providerQueryWindow(job, time.Now().UTC())
	grant.LoadEndedAt, grant.DrainDeadline, grant.QueryWatermark = job.LoadEndedAt, job.DrainDeadline, job.QueryWatermark
	grant.Reconciling = job.State == "reconciling"
	grant.MaxBatches = 32
	grant.Destination = snapshot.Config.Endpoint
	grant.TerminalWindows = prometheusTerminalWindows(job)
	if snapshot.RequiresCredentials() {
		key, e := base64.StdEncoding.DecodeString(job.Source.CollectorPublicKey)
		if e != nil {
			return errors.New("collector encryption identity unavailable")
		}
		grant.Credentials, err = collectoragent.SealCredentials(*grant, key, collectoragent.PrometheusCredentialBundle(credentials))
	}
	return err
}

func (s *Server) newPrometheusClient(config prometheus.Config, credentials prometheus.Credentials) (*prometheus.Client, error) {
	if s.prometheusClient != nil {
		return s.prometheusClient(config, credentials)
	}
	return prometheus.NewClient(config, credentials)
}

func (s *Server) collectDirectPrometheus(parent context.Context, job storage.RunMonitorJob) {
	term, err := s.runner.captureExecutionLeaseTerm()
	if err != nil || job.PrometheusConnectionSnapshot == nil || job.Source.Prometheus == nil {
		return
	}
	ctx, cancel := s.runner.executionOperationContext(parent)
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	from, to := providerQueryWindow(job, time.Now().UTC())
	if !to.After(from) {
		return
	}
	credentials, err := resolvePrometheusCredentials(ctx, s.runner.prometheusSecretResolverForProject(job.ProjectID), job.PrometheusConnectionSnapshot)
	result := prometheus.Result{}
	if err == nil {
		cfg, e := s.prometheusQuotaConfig(ctx, job.PrometheusConnectionSnapshot, credentials, "direct:"+s.controllerID)
		err = e
		var client *prometheus.Client
		if err == nil {
			client, err = s.newPrometheusClient(cfg, credentials)
		}
		if err == nil {
			selection := prometheusSelection(job)
			var windows []monitorprovider.EvaluationWindow
			if job.State == "reconciling" {
				windows = prometheusTerminalWindows(job)
			}
			result, err = client.CollectPlan(ctx, selection, from, to, job.RunID, windows)
		}
	}
	if err != nil && len(result.Coverage) == 0 {
		result.Coverage = map[string]monitorprovider.Coverage{"query": {Status: "unavailable", From: from, To: to, Message: "Prometheus collection failed"}}
	}
	if result.Progress.TotalQueries == 0 {
		selection := prometheusSelection(job)
		total := len(selection.Queries)
		if selection.Targets {
			total++
		}
		if selection.Alerts {
			total++
		}
		if job.State == "reconciling" {
			total += len(prometheusTerminalWindows(job))
		}
		result.Progress = monitorprovider.QueryProgress{TotalQueries: total, NextQuery: job.QueryOffset, NextTime: job.QueryTime}
	}
	for i := range result.Points {
		p := &result.Points[i]
		p.Source = job.Source.ID
		p.ScenarioID = job.ScenarioID
		p.Host = ""
		p.ReceivedAt = time.Time{}
	}
	if !s.runner.executionLeaseTermCurrent(term) {
		return
	}
	ingestCtx, ingestCancel := s.runner.executionOperationContext(parent)
	defer ingestCancel()
	// Reuse the common bounded chunk and final-progress protocol. Provider SDK
	// result types share their observation/evidence contracts.
	common := providerCollectionResult{Points: result.Points, Evidence: result.Evidence, Coverage: result.Coverage, Progress: result.Progress, Topology: result.Topology}
	sequence := job.NextSequence
	for len(common.Points) > 0 || len(common.Evidence) > 0 {
		payload, n, m, e := boundedProviderMonitorBatch(common, job, from, to)
		if e != nil {
			return
		}
		if e = s.ingestPrometheusBatch(ingestCtx, job, sequence, payload, term); e != nil {
			return
		}
		common.Points = common.Points[n:]
		common.Evidence = common.Evidence[m:]
		sequence++
	}
	if sequence == job.NextSequence {
		_ = s.ingestPrometheusBatch(ingestCtx, job, sequence, directCoverageBatch(common, job, from, to), term)
	}
}
