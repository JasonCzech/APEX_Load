package api

import (
	"context"
	"errors"
	"github.com/jasonczech/apexload/pkg/kubeops"
	"testing"
)

func TestObservedGeneratorPodPlacement(t *testing.T) {
	base := KubePodRef{Name: "gen-0", Namespace: "apex", IP: "10.0.0.1", UID: "uid-current", NodeName: "worker-2", Phase: "Running", Ready: true}
	for _, tc := range []struct {
		name      string
		target    string
		id        string
		alter     func(*KubePodRef)
		duplicate bool
		want      bool
	}{
		{name: "current", want: true},
		{name: "unready remains observable", alter: func(p *KubePodRef) { p.Ready = false }, want: true},
		{name: "terminating remains observable", alter: func(p *KubePodRef) { p.Terminating = true }, want: true},
		{name: "reused name different IP", alter: func(p *KubePodRef) { p.IP = "10.0.0.2" }},
		{name: "wrong namespace", alter: func(p *KubePodRef) { p.Namespace = "other" }},
		{name: "missing UID", alter: func(p *KubePodRef) { p.UID = "" }},
		{name: "unscheduled", alter: func(p *KubePodRef) { p.NodeName = "" }},
		{name: "wrong generator", id: "another"},
		{name: "DNS hint cannot bind identity", target: "gen-0.apex:9090"},
		{name: "ambiguous IP", duplicate: true},
		{name: "IPv6", target: "[2001:db8::1]:9090", alter: func(p *KubePodRef) { p.IP = "2001:db8:0::1" }, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			if tc.alter != nil {
				tc.alter(&p)
			}
			target := tc.target
			if target == "" {
				target = "10.0.0.1:9090"
			}
			id := tc.id
			if id == "" {
				id = "gen-0"
			}
			pods := []KubePodRef{p}
			if tc.duplicate {
				pods = append(pods, p)
			}
			got := observedGeneratorPodPlacement(GeneratorInventoryEntry{Target: target, GeneratorID: id, PodName: "stale-hint"}, pods, "apex")
			if (got != nil) != tc.want {
				t.Fatalf("placement=%+v, want present=%v", got, tc.want)
			}
			if got != nil && (got.UID != p.UID || got.NodeName != p.NodeName || got.Ready != (p.Ready && p.Phase == "Running") || got.Terminating != p.Terminating) {
				t.Fatalf("placement did not preserve pod state: %+v", got)
			}
		})
	}
}

func TestGeneratorPlacementClearedOnUnavailableInventory(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{true: "disabled", false: "failed"}[disabled], func(t *testing.T) {
			server, _, kube, _ := newRestartServer(t, nil)
			server.k8sRestart.Enabled = !disabled
			kube.listErr = errors.New("unavailable")
			entries := []GeneratorInventoryEntry{{Target: "10.0.0.1:9090", GeneratorID: "gen-0", PodPlacement: &GeneratorPodPlacement{UID: "stale", NodeName: "old"}}}
			server.enrichGeneratorRestartability(context.Background(), entries)
			if entries[0].PodPlacement != nil {
				t.Fatal("stale or self-reported placement survived unavailable Kubernetes inventory")
			}
		})
	}
}

func TestRemoteKubePodRefPreservesPlacement(t *testing.T) {
	got := kubePodRefFrom(kubeops.PodRef{StatefulSetUID: "owner", StatefulSetName: "gen", UID: "uid", Name: "pod", Namespace: "remote", NodeName: "remote-worker"})
	if got.StatefulSetUID != "owner" || got.StatefulSetName != "gen" || got.UID != "uid" || got.NodeName != "remote-worker" || got.Namespace != "remote" {
		t.Fatalf("lost remote placement: %+v", got)
	}
}

func TestGeneratorPlacementInventoryIsIndependentAndBoundToKubernetes(t *testing.T) {
	entries := []GeneratorInventoryEntry{{Target: "10.0.0.1:9090", GeneratorID: "gen-0", Status: "connected"}}
	server, _, kube, _ := newRestartServer(t, entries)
	kube.setPods([]KubePodRef{{Name: "gen-0", Namespace: "apex", UID: "current", IP: "10.0.0.1", NodeName: "worker-1", Phase: "Running", Ready: true}})
	inventory := server.systemInventory(context.Background())
	if len(inventory.Generators) != 1 || inventory.Generators[0].PodPlacement == nil {
		t.Fatalf("placement not attached: %+v", inventory.Generators)
	}
	observed := inventory.Generators[0].PodPlacement
	if observed.UID != "current" || observed.NodeName != "worker-1" {
		t.Fatalf("wrong placement: %+v", observed)
	}
	observed.NodeName = "consumer mutation"
	cached := server.runner.cachedGeneratorInventory()
	if cached[0].PodPlacement == nil || cached[0].PodPlacement.NodeName != "worker-1" {
		t.Fatalf("cached placement aliases caller: %+v", cached[0].PodPlacement)
	}
	cached[0].PodPlacement.UID = "second mutation"
	if server.runner.cachedGeneratorInventory()[0].PodPlacement.UID != "current" {
		t.Fatal("cached placement aliases subsequent reads")
	}
}
