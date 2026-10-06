package api

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (s *Server) startAppDynamicsMonitoring() {
	if s.runner == nil || s.store == nil {
		return
	}
	store, ok := s.store.(providerMonitorStore)
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.appDynamicsCancel = cancel
	s.launchBackground(func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		var mu sync.Mutex
		busy := map[string]bool{}
		next := map[string]time.Time{}
		slots := make(chan struct{}, 2)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if !s.runner.isExecutionLeader() {
				continue
			}
			s.resumeAppDynamicsInspections(ctx)
			s.resumePrometheusInspections(ctx)
			s.resumeDynatraceOperations(ctx)
			if cancellations, ok := s.store.(dynatraceCancellationStore); ok {
				abandoned, _ := cancellations.PollDynatraceCancellationJobs(time.Now().UTC(), 2)
				for _, job := range abandoned {
					if !runMonitoringEnabled(job.ProjectID) {
						continue
					}
					mu.Lock()
					ready := !busy[job.ID] && !time.Now().Before(next[job.ID])
					if ready {
						select {
						case slots <- struct{}{}:
							busy[job.ID] = true
						default:
							ready = false
						}
					}
					mu.Unlock()
					if !ready {
						continue
					}
					work := job
					if !s.launchBackground(func() {
						defer func() {
							<-slots
							mu.Lock()
							delete(busy, work.ID)
							next[work.ID] = time.Now().Add(30 * time.Second)
							mu.Unlock()
						}()
						s.cancelAbandonedDirectDynatrace(ctx, cancellations, work)
					}) {
						<-slots
						mu.Lock()
						delete(busy, job.ID)
						mu.Unlock()
					}
				}
			}
			jobs, err := store.PollDirectRunMonitorJobs(time.Now().UTC())
			if err != nil {
				continue
			}
			mu.Lock()
			for id, at := range next {
				if !busy[id] && time.Since(at) > 10*time.Minute {
					delete(next, id)
				}
			}
			mu.Unlock()
			for _, job := range jobs {
				provider, governed := lookupProvider(job.Source.Pack)
				if !runMonitoringEnabled(job.ProjectID) || !governed {
					continue
				}
				if job.State == "reconciling" && !job.ExpiresAt.After(time.Now()) {
					_ = store.CloseExpiredProviderMonitors(job.ProjectID, job.RunID, time.Now().UTC())
					continue
				}
				mu.Lock()
				ready := !busy[job.ID] && !time.Now().Before(next[job.ID])
				if ready {
					select {
					case slots <- struct{}{}:
						busy[job.ID] = true
					default:
						ready = false
					}
				}
				mu.Unlock()
				if !ready {
					continue
				}
				work := job
				if !s.launchBackground(func() {
					defer func() {
						<-slots
						mu.Lock()
						delete(busy, work.ID)
						current, _ := store.GetRunMonitorJob(work.ProjectID, work.ID)
						if current != nil && current.ProviderCursor != "" {
							next[work.ID] = time.Now().Add(5 * time.Second)
						} else if current != nil && (current.QueryOffset > 0 || current.QueryTime != nil) {
							next[work.ID] = time.Now()
						} else if current != nil && current.State == "reconciling" && provider.Scheduling == monitorprovider.SchedulingCursor {
							// A cursor provider reconciles on its next full interval.
							next[work.ID] = time.Now().Add(time.Duration(max(provider.MinIntervalSeconds, work.Source.IntervalSeconds)) * time.Second)
						} else if current != nil && current.State == "reconciling" {
							next[work.ID] = time.Now().Add(15 * time.Second)
						} else {
							next[work.ID] = time.Now().Add(time.Duration(max(provider.MinIntervalSeconds, work.Source.IntervalSeconds)) * time.Second)
						}
						mu.Unlock()
					}()
					provider.CollectDirect(s, ctx, work)
				}) {
					<-slots
					mu.Lock()
					delete(busy, job.ID)
					mu.Unlock()
				}
			}
		}
	})
}
func appDynamicsQueryWindow(job storage.RunMonitorJob, now time.Time) (time.Time, time.Time) {
	if !job.QueryUntilUTC.IsZero() {
		return job.QueryFromUTC, job.QueryUntilUTC
	}
	end := now.UTC()
	if job.LoadEndedAt != nil {
		end = *job.LoadEndedAt
	}
	start := job.StartedAt
	if job.State != "reconciling" && !job.QueryWatermark.IsZero() {
		start = job.QueryWatermark.Add(-5 * time.Minute)
		if start.Before(job.StartedAt) {
			start = job.StartedAt
		}
	}
	return start, end
}
func (s *Server) collectDirectAppDynamics(parent context.Context, job storage.RunMonitorJob) {
	term, err := s.runner.captureExecutionLeaseTerm()
	if err != nil || job.ConnectionSnapshot == nil || job.Source.AppDynamics == nil {
		return
	}
	ctx, cancel := s.runner.executionOperationContext(parent)
	defer cancel()
	ctx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	from, to := appDynamicsQueryWindow(job, time.Now())
	if !to.After(from) {
		return
	}
	credentials, err := resolveAppDynamicsCredentials(ctx, s.runner.secretResolverForProject(job.ProjectID), job.ConnectionSnapshot)
	result := appdynamics.Result{}
	if err == nil {
		var client *appdynamics.Client
		var config appdynamics.Config
		config, err = s.appDynamicsQuotaConfig(ctx, job.ConnectionSnapshot, credentials, "direct:"+s.controllerID)
		if err == nil {
			client, err = appdynamics.NewClient(config, credentials)
		}
		if err == nil {
			selection := *job.Source.AppDynamics
			selection.ProjectID = job.ProjectID
			selection.QueryOffset = job.QueryOffset
			result, err = client.Collect(ctx, selection, from, to, job.RunID)
		}
	}
	if err != nil && len(result.Coverage) == 0 {
		result.Coverage = map[string]appdynamics.Coverage{"controller": {Status: "unavailable", From: from, To: to, Message: "AppDynamics collection failed"}}
	}
	for i := range result.Points {
		p := &result.Points[i]
		p.Source = job.Source.ID
		p.ScenarioID = job.ScenarioID
		p.Host = ""
		p.ReceivedAt = time.Time{}
	}
	// Reserve time for artifact persistence independently of the upstream poll.
	if !s.runner.executionLeaseTermCurrent(term) {
		return
	}
	ingestCtx, ingestCancel := s.runner.executionOperationContext(parent)
	defer ingestCancel()
	sequence := job.NextSequence
	for len(result.Points) > 0 || len(result.Evidence) > 0 {
		payload, n, m, err := boundedDirectAppDynamicsBatch(result, job, from, to)
		if err != nil {
			return
		}
		if err := s.ingestAppDynamicsBatch(ingestCtx, job, sequence, payload, term); err != nil {
			return
		}
		result.Points = result.Points[n:]
		result.Evidence = result.Evidence[m:]
		sequence++
	}
	if sequence == job.NextSequence {
		_ = s.ingestAppDynamicsBatch(ingestCtx, job, sequence, directCoverageBatch(result, job, from, to), term)
	}
}

func boundedDirectAppDynamicsBatch(result appdynamics.Result, job storage.RunMonitorJob, from, to time.Time) (storage.ProviderMonitorBatch, int, int, error) {
	n, m := min(256, len(result.Points)), min(50, len(result.Evidence))
	for {
		payload := storage.ProviderMonitorBatch{Points: result.Points[:n], Evidence: result.Evidence[:m], QueryFromUTC: from, QueryUntilUTC: to}
		if n == len(result.Points) && m == len(result.Evidence) {
			payload.Coverage, payload.Topology, payload.Progress = result.Coverage, result.Topology, &result.Progress
			if result.Progress.Complete {
				payload.QueryWindowEnd = &to
				payload.Final = job.State == "reconciling"
			}
		}
		body, err := json.Marshal(payload)
		// Leave room for server-owned dimensions and run-clock normalization.
		if err == nil && len(body) <= 768<<10 {
			return payload, n, m, nil
		}
		if err != nil {
			return payload, 0, 0, errors.New("invalid AppDynamics result encoding")
		}
		if n > 1 {
			n = (n + 1) / 2
		} else if m > 1 {
			m = (m + 1) / 2
		} else {
			return payload, 0, 0, errors.New("AppDynamics record exceeds provider batch limit")
		}
	}
}

func directCoverageBatch(result appdynamics.Result, job storage.RunMonitorJob, from, to time.Time) storage.ProviderMonitorBatch {
	p := storage.ProviderMonitorBatch{Coverage: result.Coverage, Topology: result.Topology, Progress: &result.Progress, QueryFromUTC: from, QueryUntilUTC: to}
	if result.Progress.Complete {
		p.QueryWindowEnd = &to
		p.Final = job.State == "reconciling"
	}
	return p
}
