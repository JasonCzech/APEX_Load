package api

import (
	"context"
	"fmt"
	"log/slog"

	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

func (s *Server) startProviderMonitoring() {
	if s.runner == nil || s.store == nil {
		return
	}
	store, ok := s.store.(providerMonitorStore)
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.providerMonitoringCancel = cancel
	s.launchBackground(func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		retiredEpoch := ""
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
			term, err := s.runner.captureExecutionLeaseTerm()
			if err != nil {
				continue
			}
			epoch := fmt.Sprintf("%s:%d", term.ownerID, term.generation)
			if retiredEpoch != epoch {
				if err := s.runner.retireAppDynamicsMonitoring(); err != nil {
					slog.Warn("retired monitoring reconciliation failed", "error", err)
					continue
				}
				retiredEpoch = epoch
			}
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
