package api

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/kubeops"
)

type scaleLimitReaderFake struct {
	KubeRestarter
	calls atomic.Int32
	read  func(context.Context) (kubeops.StatefulSetAutoscalingLimit, error)
}

func (f *scaleLimitReaderFake) StatefulSetAutoscalingLimit(ctx context.Context, _, _ string) (kubeops.StatefulSetAutoscalingLimit, error) {
	f.calls.Add(1)
	return f.read(ctx)
}

func TestGeneratorScaleLimitCacheCoalescesAndExpiresUnknown(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "unknown"}[failed], func(t *testing.T) {
			gate := make(chan struct{})
			entered := make(chan struct{}, 1)
			value := kubeops.StatefulSetAutoscalingLimit{MaximumReplicas: 4}
			reader := &scaleLimitReaderFake{read: func(ctx context.Context) (kubeops.StatefulSetAutoscalingLimit, error) {
				entered <- struct{}{}
				select {
				case <-gate:
				case <-ctx.Done():
					return kubeops.StatefulSetAutoscalingLimit{}, ctx.Err()
				}
				if failed {
					return value, errors.New("credential-bearing upstream failure")
				}
				return value, nil
			}}
			s := &Server{kubeRestarter: reader, k8sRestart: K8sRestartSettings{Namespace: "apex", GeneratorStatefulSet: "gen"}}
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					got := s.cachedGeneratorScaleLimit(context.Background())
					if (got.MaximumReplicas == 4) == failed {
						t.Errorf("unexpected cached capacity %+v", got)
					}
				}()
			}
			<-entered
			close(gate)
			wg.Wait()
			if reader.calls.Load() != 1 {
				t.Fatalf("%d probes for one concurrent snapshot", reader.calls.Load())
			}
			s.kubeScaleLimitMu.Lock()
			s.kubeScaleLimitCache.expiresAt = time.Time{}
			s.kubeScaleLimitMu.Unlock()
			_ = s.cachedGeneratorScaleLimit(context.Background())
			if reader.calls.Load() != 2 {
				t.Fatal("expired capacity was reused")
			}
		})
	}
}

func TestGeneratorScaleLimitCacheWaiterCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	reader := &scaleLimitReaderFake{read: func(ctx context.Context) (kubeops.StatefulSetAutoscalingLimit, error) {
		close(started)
		select {
		case <-release:
			return kubeops.StatefulSetAutoscalingLimit{MaximumReplicas: 4}, nil
		case <-ctx.Done():
			return kubeops.StatefulSetAutoscalingLimit{}, ctx.Err()
		}
	}}
	s := &Server{kubeRestarter: reader}
	done := make(chan struct{})
	go func() { defer close(done); s.cachedGeneratorScaleLimit(context.Background()) }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := s.cachedGeneratorScaleLimit(ctx); got.MaximumReplicas != 0 {
		t.Fatal("canceled waiter received unknown in-flight capacity")
	}
	close(release)
	<-done
	if got := s.cachedGeneratorScaleLimit(context.Background()); got.MaximumReplicas != 4 || reader.calls.Load() != 1 {
		t.Fatal("waiter cancellation invalidated successful observation")
	}
}

func TestAttachGeneratorScaleLimitRequiresWorkloadOwnerAndOrdinal(t *testing.T) {
	for _, mode := range []string{"valid", "wrong owner", "wrong workload", "wrong namespace", "wrong pod uid", "noncanonical ordinal", "outside cap", "negative ordinal", "wrong prefix", "missing owner", "replaced workload"} {
		t.Run(mode, func(t *testing.T) {
			limit := kubeops.StatefulSetAutoscalingLimit{Namespace: "apex", Workload: "gen", WorkloadUID: "workload", MaximumReplicas: 4}
			pod := KubePodRef{Namespace: "apex", Name: "gen-3", UID: "pod", StatefulSetName: "gen", StatefulSetUID: "workload"}
			placement := &GeneratorPodPlacement{Namespace: "apex", Name: "gen-3", UID: "pod", scaleLimit: limit}
			switch mode {
			case "wrong owner":
				pod.StatefulSetUID = "old"
			case "wrong workload":
				pod.StatefulSetName = "other"
			case "wrong namespace":
				pod.Namespace = "other"
			case "wrong pod uid":
				pod.UID = "replacement"
			case "noncanonical ordinal":
				pod.Name = "gen-03"
				placement.Name = pod.Name
			case "outside cap":
				limit.MaximumReplicas = 3
			case "negative ordinal":
				pod.Name = "gen--1"
				placement.Name = pod.Name
			case "wrong prefix":
				pod.Name = "3"
				placement.Name = pod.Name
			case "missing owner":
				pod.StatefulSetUID = ""
			case "replaced workload":
				limit.WorkloadUID = "replacement"
			}
			attachGeneratorScaleLimit(placement, []KubePodRef{pod}, limit)
			if (placement.scaleLimit.MaximumReplicas == 4) != (mode == "valid") {
				t.Fatalf("mode %s retained unproven bounds %+v", mode, placement.scaleLimit)
			}
		})
	}
}

func TestGeneratorScaleLimitJoinsObservedInventory(t *testing.T) {
	entries := []GeneratorInventoryEntry{{Target: "10.0.0.1:9090", GeneratorID: "gen-0", Status: "connected"}}
	s, _, kube, _ := newRestartServer(t, entries)
	kube.setPods([]KubePodRef{
		{Name: "gen-0", Namespace: "apex", UID: "pod", IP: "10.0.0.1", NodeName: "worker", Phase: "Running", Ready: true, StatefulSetName: "gen", StatefulSetUID: "workload"},
		{Name: "gen-1", Namespace: "apex", UID: "pending", Phase: "Pending", Ready: false, StatefulSetName: "gen", StatefulSetUID: "workload"},
	})
	limit := kubeops.StatefulSetAutoscalingLimit{Namespace: "apex", Workload: "gen", WorkloadUID: "workload", MaximumReplicas: 4}
	s.kubeRestarter = &scaleLimitReaderFake{KubeRestarter: kube, read: func(context.Context) (kubeops.StatefulSetAutoscalingLimit, error) { return limit, nil }}
	s.k8sRestart.GeneratorStatefulSet = "gen"
	inventory := s.systemInventory(context.Background())
	if inventory.Generators[0].PodPlacement.scaleLimit != limit {
		t.Fatal("bound missing from joined inventory")
	}
	inventory.Generators[0].PodPlacement.scaleLimit.MaximumReplicas = 100
	if s.runner.cachedGeneratorInventory()[0].PodPlacement.scaleLimit.MaximumReplicas != 4 {
		t.Fatal("caller changed cached replica bound")
	}
	if pods := s.runner.livePodSnapshot.pods; len(pods) != 2 || pods[1].Name != "gen-1" {
		t.Fatalf("DNS-absent pending ordinal was omitted from cached pod snapshot: %+v", pods)
	}
}
