package api

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

const (
	defaultGeneratorPodTerminationWatchInterval = 5 * time.Second
	generatorPodInventoryCacheTTL               = 4 * time.Second
	generatorPodInventoryFailureCacheTTL        = 1 * time.Second
)

var errGeneratorPodInventoryProbeFailed = errors.New("kubernetes pod inventory probe failed")

type generatorPodInventoryCache struct {
	namespace string
	selector  string
	pods      []KubePodRef
	err       error
	expiresAt time.Time
	inFlight  chan struct{}
}

// RunGeneratorPodTerminationWatcher polls the Kubernetes pod inventory and marks
// terminating generator pods as draining before the scheduler can place more
// work on them. It intentionally uses the existing restart-control Kubernetes
// seam instead of introducing a second client/RBAC surface.
func (s *Server) RunGeneratorPodTerminationWatcher(ctx context.Context, interval time.Duration) {
	if s == nil || !s.k8sRestart.Enabled || s.kubeRestarter == nil {
		return
	}
	if interval <= 0 {
		interval = defaultGeneratorPodTerminationWatchInterval
	}
	s.reconcileTerminatingGeneratorPods(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reconcileTerminatingGeneratorPods(ctx)
		}
	}
}

func (s *Server) reconcileTerminatingGeneratorPods(ctx context.Context) {
	if s == nil || !s.isExecutionLeader() || !s.k8sRestart.Enabled || s.kubeRestarter == nil {
		return
	}
	backend := s.runner.getExecutionBackend()
	podBackend, ok := backend.(generatorPodTerminationBackend)
	if !ok {
		return
	}
	pods, err := s.cachedGeneratorPods(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("generator pod termination watch failed", "err", err)
		}
		return
	}
	for _, pod := range podBackend.SetTerminatingGeneratorPods(pods) {
		generatorID := strings.TrimSpace(pod.Name)
		target := podBackend.GeneratorTargetForID(generatorID)
		slog.Info("marked terminating generator pod draining", "generator_id", generatorID, "target", target)
		s.broadcastFleetEvent("generator_pod_terminating", map[string]any{
			"generator_id": generatorID, "target": target,
			"pod_name": pod.Name, "namespace": pod.Namespace,
		})
	}
}

type generatorPodTerminationBackend interface {
	SetTerminatingGeneratorPods([]KubePodRef) []KubePodRef
	GeneratorTargetForID(string) string
}

// SetTerminatingGeneratorPods replaces the transient scheduling exclusion from a
// successful, complete pod inventory. Operator drain/cordon intent lives in a
// separate durable map and is never changed here. A replacement StatefulSet pod
// must not inherit its predecessor's shutdown state merely by reusing its name.
// Callers retain the preceding snapshot on inventory failure.
func (b *distributedExecutionBackend) SetTerminatingGeneratorPods(pods []KubePodRef) []KubePodRef {
	next := make(map[string]struct{})
	var added []KubePodRef
	b.drainMu.Lock()
	defer b.drainMu.Unlock()
	for _, pod := range pods {
		id := strings.TrimSpace(pod.Name)
		if id == "" || !pod.Terminating {
			continue
		}
		if _, duplicate := next[id]; duplicate {
			continue
		}
		next[id] = struct{}{}
		if _, known := b.terminatingGeneratorIDs[id]; !known {
			added = append(added, pod)
		}
	}
	b.terminatingGeneratorIDs = next
	return added
}

func (s *Server) cachedGeneratorPods(ctx context.Context) ([]KubePodRef, error) {
	if s == nil || s.kubeRestarter == nil {
		return nil, nil
	}
	namespace := s.k8sRestart.Namespace
	selector := s.k8sRestart.GeneratorSelector
	for {
		now := time.Now()
		s.kubePodsCacheMu.Lock()
		if s.kubePodsCache.namespace == namespace &&
			s.kubePodsCache.selector == selector &&
			now.Before(s.kubePodsCache.expiresAt) {
			pods := cloneKubePodRefs(s.kubePodsCache.pods)
			err := s.kubePodsCache.err
			s.kubePodsCacheMu.Unlock()
			return pods, err
		}
		if wait := s.kubePodsCache.inFlight; wait != nil {
			s.kubePodsCacheMu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		wait := make(chan struct{})
		s.kubePodsCache.inFlight = wait
		s.kubePodsCacheMu.Unlock()

		pods, err := s.kubeRestarter.ListPods(ctx, namespace, selector)
		safeErr, cacheFailure := safeGeneratorPodInventoryError(ctx, err)
		s.kubePodsCacheMu.Lock()
		if err == nil {
			s.kubePodsCache.namespace = namespace
			s.kubePodsCache.selector = selector
			s.kubePodsCache.pods = cloneKubePodRefs(pods)
			s.kubePodsCache.err = nil
			s.kubePodsCache.expiresAt = time.Now().Add(generatorPodInventoryCacheTTL)
		} else if cacheFailure {
			s.kubePodsCache.namespace = namespace
			s.kubePodsCache.selector = selector
			s.kubePodsCache.pods = nil
			s.kubePodsCache.err = safeErr
			s.kubePodsCache.expiresAt = time.Now().Add(generatorPodInventoryFailureCacheTTL)
		}
		if s.kubePodsCache.inFlight == wait {
			s.kubePodsCache.inFlight = nil
			close(wait)
		}
		s.kubePodsCacheMu.Unlock()
		if err != nil {
			return nil, safeErr
		}
		return cloneKubePodRefs(pods), nil
	}
}

// safeGeneratorPodInventoryError keeps credential-bearing Kubernetes response
// bodies out of the shared cache and logs. Caller cancellation is not cached
// because it says nothing about the API server; timeouts and operational
// failures receive a short negative TTL to prevent readiness-request storms.
func safeGeneratorPodInventoryError(ctx context.Context, err error) (error, bool) {
	if err == nil {
		return nil, false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return context.DeadlineExceeded, true
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled, false
	}
	return errGeneratorPodInventoryProbeFailed, true
}

func cloneKubePodRefs(pods []KubePodRef) []KubePodRef {
	if len(pods) == 0 {
		return nil
	}
	cloned := make([]KubePodRef, len(pods))
	copy(cloned, pods)
	return cloned
}
