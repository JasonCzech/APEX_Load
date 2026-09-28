package api

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/kubeops"
)

type generatorScaleLimitReader interface {
	StatefulSetAutoscalingLimit(context.Context, string, string) (kubeops.StatefulSetAutoscalingLimit, error)
}

type generatorScaleLimitCache struct {
	namespace, workload string
	value               kubeops.StatefulSetAutoscalingLimit
	expiresAt           time.Time
	inFlight            chan struct{}
}

// Cache success and unknown results; concurrent inventory requests share one
// bounded probe. Readiness and termination detection do not call this reader.
func (s *Server) cachedGeneratorScaleLimit(ctx context.Context) kubeops.StatefulSetAutoscalingLimit {
	reader, ok := s.kubeRestarter.(generatorScaleLimitReader)
	if !ok {
		return kubeops.StatefulSetAutoscalingLimit{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ns, name := s.k8sRestart.Namespace, s.k8sRestart.GeneratorStatefulSet
	for {
		s.kubeScaleLimitMu.Lock()
		cached := s.kubeScaleLimitCache
		if cached.namespace == ns && cached.workload == name && time.Now().Before(cached.expiresAt) {
			s.kubeScaleLimitMu.Unlock()
			return cached.value
		}
		if cached.inFlight != nil {
			s.kubeScaleLimitMu.Unlock()
			select {
			case <-cached.inFlight:
				continue
			case <-ctx.Done():
				return kubeops.StatefulSetAutoscalingLimit{}
			}
		}
		wait := make(chan struct{})
		s.kubeScaleLimitCache.inFlight = wait
		s.kubeScaleLimitMu.Unlock()
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		value, err := reader.StatefulSetAutoscalingLimit(probe, ns, name)
		cancel()
		ttl := generatorPodInventoryCacheTTL
		if err != nil {
			value = kubeops.StatefulSetAutoscalingLimit{}
			ttl = generatorPodInventoryFailureCacheTTL
		}
		s.kubeScaleLimitMu.Lock()
		// A caller cancellation is not evidence that the Kubernetes API failed.
		if ctx.Err() == nil {
			s.kubeScaleLimitCache = generatorScaleLimitCache{namespace: ns, workload: name, value: value, expiresAt: time.Now().Add(ttl)}
		} else {
			s.kubeScaleLimitCache.inFlight = nil
		}
		close(wait)
		s.kubeScaleLimitMu.Unlock()
		return value
	}
}

func attachGeneratorScaleLimit(placement *GeneratorPodPlacement, pods []KubePodRef, limit kubeops.StatefulSetAutoscalingLimit) {
	if placement != nil {
		placement.scaleLimit = kubeops.StatefulSetAutoscalingLimit{}
	}
	if placement == nil || limit.WorkloadUID == "" || limit.Namespace != placement.Namespace || limit.MaximumReplicas <= 0 {
		return
	}
	for _, pod := range pods {
		if pod.UID != placement.UID || pod.Name != placement.Name || pod.Namespace != placement.Namespace {
			continue
		}
		if pod.StatefulSetUID != limit.WorkloadUID || pod.StatefulSetName != limit.Workload {
			return
		}
		ordinal, err := strconv.Atoi(strings.TrimPrefix(pod.Name, limit.Workload+"-"))
		if err != nil || ordinal < 0 || ordinal >= limit.MaximumReplicas || pod.Name != limit.Workload+"-"+strconv.Itoa(ordinal) {
			return
		}
		placement.scaleLimit = limit
		return
	}
}
