package api

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/kubeops"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func nodeRecoveryPreviewFixture() (*TestRunner, *ManagedTest, []PodCapacityPreview) {
	sources, _ := recoveryPlanFixture()
	var entries []GeneratorInventoryEntry
	var placements []PodCapacityPreview
	limit := kubeops.StatefulSetAutoscalingLimit{Namespace: "apex", Workload: "gen", WorkloadUID: "workload", HPAUID: "hpa", ScaledObjectUID: "scale", MaximumReplicas: 4, ObservedAt: time.Now()}
	for i, target := range []string{"a", "b", "c", "d"} {
		budget := sources[0].Demand
		snapshot := protocol.NewCapacitySnapshot(budget, protocol.CapacityVector{}, 20)
		snapshot.Incarnation = "instance-" + target
		node := "worker-1"
		if i >= 2 {
			node = "worker-2"
		}
		entries = append(entries, GeneratorInventoryEntry{Target: target, GeneratorID: target, Status: "connected", CompatibilityState: "compatible", DrainState: "active", AvailableCapacity: 100, MaxVUsers: 100, SupportedProtocols: []string{"http"}, CompatibleFeatures: []string{generatorrpc.FeaturePodCapacityV1, generatorrpc.FeaturePodCapacityStandbyV1, "http"}, PodCapacity: &snapshot, PodPlacement: &GeneratorPodPlacement{scaleLimit: limit, UID: "pod-" + target, Name: target, Namespace: "apex", NodeName: node, Ready: true}})
		if i < 2 {
			placements = append(placements, PodCapacityPreview{Target: target, Before: snapshot, Proposed: budget})
		}
	}
	settings := defaultSystemSettings(4)
	settings.CapacityPlanning.PodReservationsEnabled = true
	tr := &TestRunner{settings: settings, execution: newDistributedExecutionBackend([]string{"a", "b", "c", "d"}, time.Second, nil), liveInventoryAt: time.Now(), liveInventory: entries}
	test := &ManagedTest{Groups: []TestGroup{{Name: "http", Protocol: "http", VUsers: 200}}, Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true}}
	return tr, test, placements
}

func TestNodeRecoveryCapacityPreviewReportsUnreservedEstimate(t *testing.T) {
	tr, test, placements := nodeRecoveryPreviewFixture()
	got := tr.nodeRecoveryCapacityWarningLocked(test, placements)
	if got == nil || got.Severity != "warning" || got.Code != "node_recovery_capacity_unreserved" || !strings.Contains(got.Message, "using 2 observed unused generator pods") || !strings.Contains(got.Message, "not guaranteed") {
		t.Fatalf("preview: %+v", got)
	}
	// Preview performs no reservation or mutation of the cached capacity.
	for _, e := range tr.liveInventory {
		if e.PodCapacity.ReservedPct != 0 {
			t.Fatal("preview reserved capacity")
		}
	}
	test.Assignment.TolerateGeneratorLoss = false
	if got := tr.nodeRecoveryCapacityWarningLocked(test, placements); got != nil {
		t.Fatalf("default strict run changed: %+v", got)
	}
}

func TestNodeRecoveryCapacityPreviewExcludesUnprovenSpares(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*TestRunner, *ManagedTest, []PodCapacityPreview)
	}{
		{"missing scale limit", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[0].PodPlacement.scaleLimit = kubeops.StatefulSetAutoscalingLimit{}
		}},
		{"stale scale limit", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			for i := range tr.liveInventory {
				tr.liveInventory[i].PodPlacement.scaleLimit.ObservedAt = time.Now().Add(-time.Hour)
			}
		}},
		{"deployed replica cap", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			for i := range tr.liveInventory {
				tr.liveInventory[i].PodPlacement.scaleLimit.MaximumReplicas = 3
			}
		}},
		{"different workload", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].PodPlacement.scaleLimit.WorkloadUID = "other"
		}},
		{"future observation", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventoryAt = time.Now().Add(time.Hour)
		}},
		{"weighted root", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			test.Assignment.Mode = generatorAssignmentModeRegionWeighted
		}},
		{"weighted group", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			test.Groups[0].Assignment = &GeneratorAssignmentSettings{Mode: generatorAssignmentModeLoadZoneWeighted}
		}},
		{"non-HTTP", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) { test.Groups[0].Protocol = "database" }},
		{"incompatible", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].CompatibilityState = "incompatible"
		}},
		{"fleet too large", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory = make([]GeneratorInventoryEntry, maxRecoveryCapacityPlanPods+1)
		}},
		{"unknown runtime maximum", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) { tr.liveInventory[2].MaxVUsers = 0 }},
		{"smaller runtime maximum", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) { tr.liveInventory[2].MaxVUsers = 99 }},
		{"stale", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventoryAt = time.Now().Add(-time.Hour)
		}},
		{"disabled", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.settings.CapacityPlanning.PodReservationsEnabled = false
		}},
		{"zero available capacity", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].AvailableCapacity = 0
		}},
		{"insufficient available capacity", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].AvailableCapacity = 99
		}},
		{"active", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) { tr.liveInventory[2].ActiveVUsers = 1 }},
		{"cordoned", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) { tr.liveInventory[2].Cordoned = true }},
		{"draining", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].DrainState = "draining"
		}},
		{"unreachable", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].Status = "disconnected"
		}},
		{"unready", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].PodPlacement.Ready = false
		}},
		{"terminating", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].PodPlacement.Terminating = true
		}},
		{"unobserved", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].PodPlacement = nil
		}},
		{"different namespace", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].PodPlacement.Namespace = "other"
		}},
		{"same worker", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].PodPlacement.NodeName = "worker-1"
		}},
		{"reserved", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].PodCapacity.Reserved.VUsers = 1
		}},
		{"zero-VUser claim", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].PodCapacity.ReportedTestIDs = []string{"other"}
		}},
		{"dedicated", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].PodCapacity.Dedicated = true
		}},
		{"invalid occupancy", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].PodCapacity.Reserved.CPUCores = math.NaN()
		}},
		{"unreconciled", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].PodCapacity.State = "unreconciled"
		}},
		{"old generator", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].CompatibleFeatures = []string{generatorrpc.FeaturePodCapacityV1, "http"}
		}},
		{"replaced source", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) { p[0].Before.Incarnation = "old" }},
		{"duplicate inventory", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory = append(tr.liveInventory, tr.liveInventory[2])
		}},
		{"pool selection", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			test.Assignment.Mode = generatorAssignmentModeSpecificHosts
			test.Assignment.SelectedTargets = []string{"a", "b", "d"}
		}},
		{"group selection", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			test.Groups[0].Assignment = &GeneratorAssignmentSettings{Mode: generatorAssignmentModeSpecificHosts, SelectedTargets: []string{"a", "b", "d"}}
		}},
		{"required labels", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			test.Assignment.RequiredLabels = map[string]string{"zone": "restricted"}
		}},
		{"protocol mismatch", func(tr *TestRunner, test *ManagedTest, p []PodCapacityPreview) {
			tr.liveInventory[2].SupportedProtocols = []string{"soap"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, test, p := nodeRecoveryPreviewFixture()
			tc.change(tr, test, p)
			got := tr.nodeRecoveryCapacityWarningLocked(test, p)
			if got == nil || strings.Contains(got.Message, "can match every") || !strings.Contains(got.Message, "not guaranteed") {
				t.Fatalf("unproven capacity accepted: %+v", got)
			}
		})
	}
}

func TestPreflightLaunchWarnsRecoveryCapacityIsNotReserved(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	runner.durableExecutionFence = true
	storePreflightScript(runner, "recovery-preview", "")
	preflight, err := runner.PreflightLaunch(LaunchPreflightRequest{CreateTestRequest: CreateTestRequest{Name: "Recovery preview", Duration: "5m", ScriptID: "recovery-preview", VUsers: 10, Assignment: &GeneratorAssignmentSettings{TolerateGeneratorLoss: true}}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, warning := range preflight.Warnings {
		if warning.Code == "node_recovery_capacity_unreserved" {
			found = true
			if warning.Severity != "warning" || !strings.Contains(warning.Message, "not guaranteed") {
				t.Fatalf("warning: %+v", warning)
			}
		}
	}
	if !found {
		t.Fatalf("missing recovery warning: %+v", preflight)
	}
}
