package collectoragent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/prometheus"
	"net/url"
	"time"
)

type providerResult struct {
	Points                 []analysis.MetricPoint
	Evidence               []monitorprovider.Diagnostic
	Topology               []monitorprovider.Entity
	Coverage               map[string]monitorprovider.Coverage
	Progress               monitorprovider.QueryProgress
	PrometheusCapabilities *prometheus.Capabilities
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
		// Other historical query providers recover evidence through their final window.
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
			if job.Pack == monitorprovider.PackDynatrace {
				key = "controller"
			}
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
			batch = &RunSampleBatch{Job: envelope, Sequence: job.NextSequence + int64(len(out)), PrometheusCapabilities: result.PrometheusCapabilities, QueryFromUTC: job.QueryFromUTC, QueryUntilUTC: job.QueryUntilUTC}
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
func providerQueryWindow(job RunJob, now time.Time) (time.Time, time.Time) {
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
	return from, to
}
