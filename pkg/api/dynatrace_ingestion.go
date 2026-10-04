package api

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (s *Server) ingestDynatraceBatch(ctx context.Context, job storage.RunMonitorJob, sequence int64, payload storage.ProviderMonitorBatch, term executionLeaseTerm) error {
	if job.DynatraceSnapshot == nil || job.Source.Dynatrace == nil || sequence < 0 || len(payload.Points) > 512 || len(payload.Evidence) > 100 || len(payload.Coverage) > 32 || len(payload.Topology) > 200 {
		return errors.New("invalid Dynatrace batch")
	}
	now := time.Now().UTC()
	from, to := payload.QueryFromUTC, payload.QueryUntilUTC
	if from.IsZero() || from.Location() != time.UTC || to.Location() != time.UTC || !to.After(from) || from.Before(job.StartedAt) || to.After(now.Add(time.Second)) || to.Sub(from) > 31*24*time.Hour {
		return errors.New("invalid Dynatrace query window")
	}
	if job.LoadEndedAt != nil && to.After(*job.LoadEndedAt) {
		return errors.New("query extends beyond original load window")
	}
	if sequence >= job.NextSequence && !job.QueryUntilUTC.IsZero() && (!from.Equal(job.QueryFromUTC) || !to.Equal(job.QueryUntilUTC)) {
		return errors.New("query continuation changed window")
	}
	if payload.QueryWindowEnd != nil && (!payload.QueryWindowEnd.Equal(to) || payload.Progress == nil || !payload.Progress.Complete) {
		return errors.New("incomplete query cannot advance watermark")
	}
	if payload.Final && (job.State != "reconciling" || payload.QueryWindowEnd == nil) {
		return errors.New("invalid final monitoring pass")
	}
	selection := *job.Source.Dynatrace
	selection.ProjectID = job.ProjectID
	selection.QueryOffset = job.QueryOffset
	selection.Cursor = job.ProviderCursor
	// Drop excluded fields before the provider trust-boundary check. Neither
	// direct adapters nor collectors can persist unrestricted attributes.
	sanitizeDynatraceDiagnostics(payload.Evidence)
	result := dynatrace.Result{Points: payload.Points, Evidence: payload.Evidence, Coverage: payload.Coverage, Topology: payload.Topology}
	if payload.Progress != nil {
		result.Progress = *payload.Progress
		queries := len(selection.Metrics) + len(selection.Grail)
		if selection.Problems {
			queries++
		}
		if selection.Events {
			queries++
		}
		if selection.TopologyMode != "" {
			queries++
		}
		progress := payload.Progress
		if progress.TotalQueries != queries || progress.NextQuery < 0 || progress.NextQuery > queries || progress.Complete && (progress.NextQuery != queries || progress.Cursor != "") || progress.ScannedBytes < 0 || progress.ScanReservationBytes < 0 || len(progress.ScanReservationIDs) > 32 {
			return errors.New("invalid Dynatrace query progress")
		}
		if err := dynatrace.ValidateScanSettlements(*progress); err != nil {
			return err
		}
	}
	if err := dynatrace.ValidateResult(selection, job.DynatraceSnapshot.Config, from, to, job.RunID, result); err != nil {
		return err
	}
	for i, p := range payload.Points {
		if p.ProjectID != job.ProjectID || p.RunID != job.RunID || p.Source != job.Source.ID || p.ScenarioID != job.ScenarioID || p.Host != job.Source.CollectorID || p.DatasetID != "" || !p.ReceivedAt.IsZero() || p.ActiveRuntimeMS != nil {
			return errors.New("misattributed Dynatrace measurement")
		}
		// Settling and statistic eligibility are server decisions, never collector
		// claims. Missing or synthetic buckets do not establish rule coverage.
		settle := selection.SettleSeconds
		if settle == 0 {
			settle = 120
		}
		if p.Provider.Settled && p.Provider.BucketEnd.After(now.Add(-time.Duration(settle)*time.Second)) {
			return errors.New("unsettled Dynatrace bucket claimed settled")
		}
		if p.Value == nil || p.Provider.Synthetic {
			p.Provider.AggregationEligible = false
		}
		normalized, err := analysis.NormalizeRunClock(p, job.StartedAt)
		if err != nil {
			return err
		}
		normalized.Service = job.Source.Service
		payload.Points[i] = normalized
	}
	for i, d := range payload.Evidence {
		if d.TimestampUTC.IsZero() || d.TimestampUTC.After(now.Add(time.Minute)) || len(d.EntityIDs) > 16 || len(d.Properties) > 16 {
			return errors.New("invalid Dynatrace diagnostic clock or fields")
		}
		d.Provider = "dynatrace"
		d.ApplicationID = ""
		d.Summary = scrubTextBounded(d.Summary, 1024)
		d.Status = scrubTextBounded(d.Status, 128)
		d.Severity = scrubTextBounded(d.Severity, 64)
		for key, value := range d.Properties {
			if key == "run_id" {
				if value != job.RunID {
					return errors.New("diagnostic correlation does not match run")
				}
			} else {
				d.Properties[key] = scrubTextBounded(value, 256)
			}
		}
		if d.Properties["correlation"] == "run_header" && (!selection.RunHeader || d.Properties["run_id"] != job.RunID) {
			return errors.New("unsupported run-header correlation")
		}
		if d.DeepLink != "" {
			u, e := url.Parse(d.DeepLink)
			env, _ := url.Parse(job.DynatraceSnapshot.Config.EnvironmentURL)
			platform, _ := url.Parse(job.DynatraceSnapshot.Config.PlatformURL)
			if e != nil || u.Scheme != "https" || u.User != nil || len(d.DeepLink) > 2048 || (env == nil || u.Host != env.Host) && (platform == nil || u.Host != platform.Host) {
				d.DeepLink = ""
			}
		}
		payload.Evidence[i] = d
	}
	for key, coverage := range payload.Coverage {
		selected := key == "problems" && selection.Problems || key == "events" && selection.Events || key == "topology" && selection.TopologyMode != "" || key == "controller" || key == "executor" || key == "retention"
		for _, m := range selection.Metrics {
			selected = selected || key == "metrics:"+m.Name
		}
		for _, q := range selection.Grail {
			selected = selected || key == "grail:"+q.Name
		}
		if !selected || len(key) > 256 || !coverage.From.Equal(from) || !coverage.To.Equal(to) || coverage.Points < 0 || coverage.Evidence < 0 {
			return errors.New("invalid Dynatrace coverage")
		}
		switch coverage.Status {
		case "complete", "partial", "pending", "unavailable", "unreachable", "unauthorized", "forbidden", "unsupported", "unlicensed", "rate_limited", "budget_exhausted", "error", "authentication_denied", "permission_denied", "throttled", "query_rejected", "query_budget", "upstream_error", "query_expired", "invalid_response":
		default:
			return errors.New("invalid Dynatrace coverage status")
		}
		coverage.Message = scrubTextBounded(coverage.Message, 256)
		payload.Coverage[key] = coverage
	}
	for i, e := range payload.Topology {
		if len(e.ID) > 128 || len(e.EntityIDs) > 16 || len(e.Path) > 4096 || strings.ContainsAny(e.ID, "\r\n\x00") {
			return errors.New("invalid Dynatrace topology")
		}
		e.Name = scrubTextBounded(e.Name, 512)
		e.Type = scrubTextBounded(e.Type, 64)
		payload.Topology[i] = e
	}
	return s.persistProviderMonitorBatch(ctx, job, sequence, payload, term)
}

func sanitizeDynatraceDiagnostics(evidence []analysis.ProviderDiagnostic) {
	allowed := map[string]bool{"duration_ms": true, "correlation": true, "run_id": true, "trace_id": true, "span_id": true, "parent_span_id": true, "query_name": true, "event_type": true, "display_id": true, "start_time": true, "end_time": true, "status": true, "evidence_scope": true}
	for i := range evidence {
		d := &evidence[i]
		d.Summary = scrubTextBounded(d.Summary, 1024)
		if d.Properties["correlation"] == "trace_id" {
			d.Properties["correlation"] = "entity_time"
		}
		for key, value := range d.Properties {
			if !allowed[key] {
				delete(d.Properties, key)
			} else if key != "run_id" {
				d.Properties[key] = scrubTextBounded(value, 256)
			}
		}
	}
}

func (s *Server) collectDirectDynatrace(parent context.Context, job storage.RunMonitorJob) {
	term, err := s.runner.captureExecutionLeaseTerm()
	if err != nil || job.DynatraceSnapshot == nil || job.Source.Dynatrace == nil {
		return
	}
	ctx, cancel := s.runner.executionOperationContext(parent)
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	from, to := appDynamicsQueryWindow(job, time.Now().UTC())
	if !to.After(from) {
		return
	}
	result := dynatrace.Result{}
	client, err := s.nativeDynatraceClient(ctx, job.DynatraceSnapshot, "direct:"+s.controllerID, job.Source.Dynatrace)
	if err == nil {
		scans, ok := s.store.(interface {
			ReserveDynatraceScanFenced(string, string, string, int64, time.Time, string, string, uint64) (bool, error)
		})
		if !ok {
			err = errors.New("scan quota storage unavailable")
		} else {
			client.SetScanHook(func(attempt string, bytes int64) error {
				name, owner, generation := "", "", uint64(0)
				if s.runner.durableExecutionFence {
					name, owner, generation = executionLeaseName, term.ownerID, term.generation
				}
				allowed, e := scans.ReserveDynatraceScanFenced(job.ProjectID, job.ID, attempt, bytes, time.Now().UTC(), name, owner, generation)
				if e != nil || !allowed {
					return dynatrace.ErrBudget
				}
				return nil
			})
			selection := *job.Source.Dynatrace
			selection.ProjectID = job.ProjectID
			selection.QueryOffset = job.QueryOffset
			selection.Cursor = job.ProviderCursor
			result, err = client.Collect(ctx, selection, from, to, job.RunID)
		}
	}
	if err != nil && len(result.Coverage) == 0 {
		result.Coverage = map[string]analysis.ProviderCoverage{"executor": {Status: "unavailable", From: from, To: to, Message: "Dynatrace collection failed"}}
	}
	for i := range result.Points {
		point := &result.Points[i]
		point.RunID = job.RunID
		point.Source = job.Source.ID
		point.ScenarioID = job.ScenarioID
		point.Host = ""
		point.ReceivedAt = time.Time{}
	}
	if !s.runner.executionLeaseTermCurrent(term) {
		return
	}
	ingestCtx, ingestCancel := s.runner.executionOperationContext(parent)
	defer ingestCancel()
	// Both adapters emit the same envelope. Keep the established chunking and
	// artifact limits so mixed-provider archives obey one storage budget.
	common := appdynamics.Result{Points: result.Points, Evidence: result.Evidence, Coverage: result.Coverage, Topology: result.Topology, Progress: result.Progress}
	sequence := job.NextSequence
	for len(common.Points) > 0 || len(common.Evidence) > 0 {
		batch, n, m, e := boundedDirectAppDynamicsBatch(common, job, from, to)
		if e != nil {
			return
		}
		if e = s.ingestDynatraceBatch(ingestCtx, job, sequence, batch, term); e != nil {
			return
		}
		common.Points = common.Points[n:]
		common.Evidence = common.Evidence[m:]
		sequence++
	}
	if sequence == job.NextSequence {
		batch := directCoverageBatch(common, job, from, to)
		if !result.Progress.Complete && result.Progress.TotalQueries == 0 {
			batch.Progress = nil
		}
		_ = s.ingestDynatraceBatch(ingestCtx, job, sequence, batch, term)
	}
}

type dynatraceCancellationStore interface {
	PollDynatraceCancellationJobs(time.Time, int) ([]storage.RunMonitorJob, error)
	ClearDynatraceCancellationCursorFenced(string, string, string, string, string, uint64) (bool, error)
}

func (s *Server) cancelAbandonedDirectDynatrace(parent context.Context, store dynatraceCancellationStore, job storage.RunMonitorJob) {
	term, err := s.runner.captureExecutionLeaseTerm()
	if err != nil || job.DynatraceSnapshot == nil || job.ProviderCursor == "" {
		return
	}
	ctx, cancel := s.runner.executionOperationContext(parent)
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	client, err := s.nativeDynatraceClient(ctx, job.DynatraceSnapshot, "direct:"+s.controllerID, job.Source.Dynatrace)
	if err != nil || client.Cancel(ctx, job.ProviderCursor) != nil || !s.runner.executionLeaseTermCurrent(term) {
		return
	}
	name, owner, generation := "", "", uint64(0)
	if s.runner.durableExecutionFence {
		name, owner, generation = executionLeaseName, term.ownerID, term.generation
	}
	_, _ = store.ClearDynatraceCancellationCursorFenced(job.ProjectID, job.ID, job.ProviderCursor, name, owner, generation)
}
