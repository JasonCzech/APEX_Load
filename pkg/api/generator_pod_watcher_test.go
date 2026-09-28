package api

import (
	"context"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/storage"
	"sync"
	"testing"
	"time"
)

func TestGeneratorTerminationSnapshotPreservesOperatorIntent(t *testing.T) {
	for _, manual := range []string{"active", "cordoned", "draining"} {
		t.Run(manual, func(t *testing.T) {
			b := newDistributedExecutionBackend([]string{"old:9090"}, time.Second, nil)
			b.recordGeneratorIdentity("old:9090", "generator-3")
			if _, err := b.SetGeneratorDrainStateByID("generator-3", manual); err != nil {
				t.Fatal(err)
			}
			terminating := []KubePodRef{{Name: "generator-3", Terminating: true}, {Name: "generator-3", Terminating: true}, {Terminating: true}}
			if added := b.SetTerminatingGeneratorPods(terminating); len(added) != 1 {
				t.Fatalf("new transitions=%+v", added)
			}
			if added := b.SetTerminatingGeneratorPods(terminating); len(added) != 0 {
				t.Fatal("repeated snapshot produced duplicate transitions")
			}
			if b.generatorDrainState("old:9090") != "draining" || b.GeneratorDrainStateByID("generator-3") != "draining" || len(b.configuredTargets()) != 0 {
				t.Fatal("terminating generator remained eligible")
			}
			durable := b.GeneratorDrainStates()
			if manual == "active" && len(durable) != 0 || manual != "active" && durable["generator-3"] != manual {
				t.Fatalf("operator state changed: %v", durable)
			}
			b.ApplyGeneratorDrainStates(durable)
			if b.generatorDrainState("old:9090") != "draining" {
				t.Fatal("metadata reload removed transient exclusion")
			}
			b.SetTargets([]string{"new:9090"})
			b.recordGeneratorIdentity("new:9090", "generator-3")
			b.SetTerminatingGeneratorPods([]KubePodRef{{Name: "generator-3", Ready: true, IP: "new"}})
			if got := b.generatorDrainState("new:9090"); got != manual {
				t.Fatalf("replacement state=%s, want %s", got, manual)
			}
			// A successful empty inventory also clears only transient state.
			b.SetTerminatingGeneratorPods(terminating)
			b.SetTerminatingGeneratorPods(nil)
			if got := b.GeneratorDrainStateByID("generator-3"); got != manual {
				t.Fatalf("empty snapshot state=%s", got)
			}
		})
	}
}

func TestGeneratorTerminationWatcherRetainsExclusionOnInventoryFailure(t *testing.T) {
	kube := &fakeKubeRestarter{}
	kube.setPods([]KubePodRef{{Name: "generator-0", Namespace: "apex", Terminating: true}})
	s := NewServer(ServerConfig{MaxConcurrent: 1, GeneratorTargets: []string{"gen:9090"}, KubeRestarter: kube, K8sRestart: K8sRestartSettings{Enabled: true, Namespace: "apex", GeneratorSelector: "app=generator"}})
	b := s.runner.getExecutionBackend().(*distributedExecutionBackend)
	b.recordGeneratorIdentity("gen:9090", "generator-0")
	reconcile := func() {
		s.kubePodsCacheMu.Lock()
		s.kubePodsCache.expiresAt = time.Time{}
		s.kubePodsCacheMu.Unlock()
		s.reconcileTerminatingGeneratorPods(context.Background())
	}
	reconcile()
	kube.mu.Lock()
	kube.listErr = errors.New("inventory unavailable")
	kube.mu.Unlock()
	reconcile()
	if b.generatorDrainState("gen:9090") != "draining" {
		t.Fatal("failed list cleared the exclusion")
	}
	kube.mu.Lock()
	kube.listErr = nil
	kube.pods = []KubePodRef{{Name: "generator-0", Namespace: "apex", Ready: true}}
	kube.mu.Unlock()
	reconcile()
	if b.generatorDrainState("gen:9090") != "active" {
		t.Fatal("replacement remained excluded")
	}
}

func TestGeneratorTerminationDoesNotOverrideOperatorUpdates(t *testing.T) {
	b := newDistributedExecutionBackend([]string{"gen:9090"}, time.Second, nil)
	b.recordGeneratorIdentity("gen:9090", "generator-0")
	b.SetTerminatingGeneratorPods([]KubePodRef{{Name: "generator-0", Terminating: true}})
	if _, err := b.SetGeneratorDrainStateByID("generator-0", "active"); err != nil {
		t.Fatal(err)
	}
	if b.generatorDrainState("gen:9090") != "draining" {
		t.Fatal("operator active admitted a terminating pod")
	}
	if _, err := b.SetGeneratorDrainStateByID("generator-0", "cordoned"); err != nil {
		t.Fatal(err)
	}
	b.SetTerminatingGeneratorPods(nil)
	if b.generatorDrainState("gen:9090") != "cordoned" {
		t.Fatal("replacement lost concurrent operator cordon")
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.SetTerminatingGeneratorPods(nil)
				_ = b.generatorDrainState("gen:9090")
				_ = b.GeneratorDrainStateByID("generator-0")
				_ = b.GeneratorDrainStates()
			}
		}()
	}
	wg.Wait()
}

func TestGeneratorTerminationDoesNotMaskOfflineMetadataOrRollbackIntent(t *testing.T) {
	for _, cordoned := range []bool{false, true} {
		t.Run(fmt.Sprint(cordoned), func(t *testing.T) {
			s, store := newTimeslotServer(t)
			b := newDistributedExecutionBackend([]string{"gen:9090"}, time.Second, nil)
			s.runner.setExecutionBackend(b)
			b.SetTerminatingGeneratorPods([]KubePodRef{{Name: "generator-0", Terminating: true}})
			if err := store.SaveGeneratorMetadata(&storage.GeneratorMetadata{GeneratorID: "generator-0", Cordoned: cordoned}); err != nil {
				t.Fatal(err)
			}
			b.setOnIdentityRecorded(func(id string) { s.applyMetadataCordonOnReconnect(b, id) })
			b.recordGeneratorIdentity("gen:9090", "generator-0")
			expected := "active"
			if cordoned {
				expected = "cordoned"
			}
			if b.GeneratorDrainStateByID("generator-0") != "draining" {
				t.Fatal("metadata reconciliation admitted a terminating pod")
			}
			previous := s.currentGeneratorLifecycleState("", "gen:9090")
			if previous != expected {
				t.Fatalf("rollback snapshot=%s, want operator %s", previous, expected)
			}
			if err := s.restoreGeneratorLifecycleState("generator-0", "gen:9090", previous); err != nil {
				t.Fatal(err)
			}
			b.SetTerminatingGeneratorPods(nil)
			if got := b.GeneratorDrainStateByID("generator-0"); got != expected {
				t.Fatalf("replacement=%s want %s", got, expected)
			}
		})
	}
}
