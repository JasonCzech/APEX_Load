package api

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

const defaultGeneratorPodTerminationWatchInterval = 5 * time.Second

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
	pods, err := s.kubeRestarter.ListPods(ctx, s.k8sRestart.Namespace, s.k8sRestart.GeneratorSelector)
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
