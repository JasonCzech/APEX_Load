package api

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

const (
	defaultGeneratorPodTerminationWatchInterval = 5 * time.Second
	generatorPodInventoryCacheTTL               = 4 * time.Second
)

type generatorPodInventoryCache struct {
	namespace string
	selector  string
	pods      []KubePodRef
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
	identityBackend, ok := backend.(identityDrainableExecutionBackend)
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
	var changed bool
	current := map[string]string{}
	if persistent, ok := backend.(persistentDrainBackend); ok {
		current = persistent.GeneratorDrainStates()
	}
	for _, pod := range pods {
		generatorID := strings.TrimSpace(pod.Name)
		if generatorID == "" || !pod.Terminating {
			continue
		}
		if strings.EqualFold(current[generatorID], "draining") {
			continue
		}
		target, err := identityBackend.SetGeneratorDrainStateByID(generatorID, "draining")
		if err != nil {
			slog.Warn("failed to mark terminating generator pod draining", "generator_id", generatorID, "err", err)
			continue
		}
		changed = true
		slog.Info("marked terminating generator pod draining", "generator_id", generatorID, "target", target)
		s.broadcastFleetEvent("generator_pod_terminating", map[string]any{
			"generator_id": generatorID,
			"target":       target,
			"pod_name":     pod.Name,
			"namespace":    pod.Namespace,
		})
	}
	if changed {
		if persistent, ok := backend.(persistentDrainBackend); ok {
			if err := s.saveGeneratorDrainStates(persistent.GeneratorDrainStates()); err != nil {
				slog.Warn("failed to persist terminating generator drain state", "err", err)
			}
		}
	}
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
			s.kubePodsCacheMu.Unlock()
			return pods, nil
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
		s.kubePodsCacheMu.Lock()
		if err == nil {
			s.kubePodsCache.namespace = namespace
			s.kubePodsCache.selector = selector
			s.kubePodsCache.pods = cloneKubePodRefs(pods)
			s.kubePodsCache.expiresAt = time.Now().Add(generatorPodInventoryCacheTTL)
		}
		if s.kubePodsCache.inFlight == wait {
			s.kubePodsCache.inFlight = nil
			close(wait)
		}
		s.kubePodsCacheMu.Unlock()
		if err != nil {
			return nil, err
		}
		return cloneKubePodRefs(pods), nil
	}
}

func cloneKubePodRefs(pods []KubePodRef) []KubePodRef {
	if len(pods) == 0 {
		return nil
	}
	cloned := make([]KubePodRef, len(pods))
	copy(cloned, pods)
	return cloned
}
