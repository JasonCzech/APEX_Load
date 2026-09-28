package api

import (
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func recoveryPlanFixture() ([]recoveryCapacityParticipant, []recoveryCapacitySpare) {
	demand := protocol.CapacityVector{CPUCores: 1, MemoryMB: 128, NetworkMbps: 10, VUsers: 100, Protocols: map[string]float64{"http": 100}}
	sources := []recoveryCapacityParticipant{{Target: "a", PodUID: "pod-a", Incarnation: "instance-a", Node: "worker-1", Demand: demand}, {Target: "b", PodUID: "pod-b", Incarnation: "instance-b", Node: "worker-1", Demand: demand}}
	spares := []recoveryCapacitySpare{{Target: "c", PodUID: "pod-c", Incarnation: "instance-c", Node: "worker-2", Budget: demand, EligibleSources: []string{"a", "b"}}, {Target: "d", PodUID: "pod-d", Incarnation: "instance-d", Node: "worker-2", Budget: demand, EligibleSources: []string{"a"}}}
	return sources, spares
}

func TestNodeRecoveryCapacityRequiresDistinctOutsideNodeMatches(t *testing.T) {
	sources, spares := recoveryPlanFixture()
	plan, err := planNodeRecoveryCapacity(sources, spares, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]string{"worker-1": {"a": "d", "b": "c"}}
	if !reflect.DeepEqual(plan.Replacements, want) || !reflect.DeepEqual(plan.SpareTargets, []string{"c", "d"}) {
		t.Fatalf("plan: %+v", plan)
	}
	// The single flexible spare cannot replace both lost generator processes.
	if plan, err := planNodeRecoveryCapacity(sources, spares[:1], 4); err == nil || plan != nil {
		t.Fatalf("one spare overpromised: %+v %v", plan, err)
	}
	spares[1].Node = "worker-1"
	if _, err := planNodeRecoveryCapacity(sources, spares, 4); err == nil {
		t.Fatal("spare on failed node counted")
	}
}

func TestNodeRecoveryCapacityCoversEveryParticipatingNode(t *testing.T) {
	sources, spares := recoveryPlanFixture()
	sources[1].Node = "worker-2"
	spares[0].Node = "worker-3"
	spares[0].EligibleSources = []string{"a", "b"}
	// Alternative single-node failures may share the same reserved destination.
	plan, err := planNodeRecoveryCapacity(sources, spares[:1], 3)
	if err != nil || len(plan.Replacements) != 2 || !reflect.DeepEqual(plan.SpareTargets, []string{"c"}) {
		t.Fatalf("contingency plan: %+v %v", plan, err)
	}
	spares[0].Node = "worker-1"
	if _, err := planNodeRecoveryCapacity(sources, spares[:1], 3); err == nil {
		t.Fatal("uncovered participating node accepted")
	}
}

func TestNodeRecoveryCapacityRejectsInvalidOrInsufficientInputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*[]recoveryCapacityParticipant, *[]recoveryCapacitySpare, *int)
	}{
		{"empty", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) { *s = nil }},
		{"source UID missing", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) { (*s)[0].PodUID = "" }},
		{"source node unknown", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) { (*s)[0].Node = "" }},
		{"duplicate target", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) {
			(*c)[0].Target = (*s)[0].Target
		}},
		{"duplicate pod", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) {
			(*c)[0].PodUID = (*c)[1].PodUID
		}},
		{"duplicate incarnation", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) {
			(*c)[0].Incarnation = (*s)[0].Incarnation
		}},
		{"empty incarnation", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) { (*c)[0].Incarnation = "" }},
		{"whitespace identity", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) { (*c)[0].Node = " worker-2" }},
		{"unknown source", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) {
			(*c)[0].EligibleSources = []string{"missing"}
		}},
		{"duplicate source", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) {
			(*c)[0].EligibleSources = []string{"a", "a"}
		}},
		{"no eligibility", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) {
			(*c)[0].EligibleSources = nil
		}},
		{"CPU", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) {
			(*c)[0].Budget.CPUCores = .5
		}},
		{"memory", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) {
			(*c)[0].Budget.MemoryMB = 64
		}},
		{"network", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) {
			(*c)[0].Budget.NetworkMbps = 5
		}},
		{"VUsers", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) { (*c)[0].Budget.VUsers = 99 }},
		{"protocol", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) {
			(*c)[0].Budget.Protocols = map[string]float64{"http": 99}
		}},
		{"NaN demand", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) {
			(*s)[0].Demand.VUsers = math.NaN()
		}},
		{"infinite budget", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) {
			(*c)[0].Budget.MemoryMB = math.Inf(1)
		}},
		{"zero demand", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) { (*s)[0].Demand.VUsers = 0 }},
		{"replica cap", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) { *m = 3 }},
		{"invalid cap", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) { *m = 0 }},
		{"oversized protocol map", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) {
			(*c)[0].Budget.Protocols = map[string]float64{}
			for i := 0; i < 65; i++ {
				(*c)[0].Budget.Protocols[fmt.Sprintf("p%d", i)] = 1
			}
		}},
		{"oversized cap", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) {
			*m = maxRecoveryCapacityPlanPods + 1
		}},
		{"oversized input", func(s *[]recoveryCapacityParticipant, c *[]recoveryCapacitySpare, m *int) {
			*c = make([]recoveryCapacitySpare, maxRecoveryCapacityPlanPods)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c := recoveryPlanFixture()
			m := 4
			tc.change(&s, &c, &m)
			plan, err := planNodeRecoveryCapacity(s, c, m)
			if err == nil || plan != nil {
				t.Fatalf("invalid plan accepted: %+v %v", plan, err)
			}
		})
	}
}

func TestNodeRecoveryCapacityMatchingAgainstExhaustiveAssignments(t *testing.T) {
	// Enumerate all 3x3 eligibility graphs; compare against six possible distinct
	// assignments, independently of the augmenting-path implementation.
	permutations := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for mask := 0; mask < 1<<9; mask++ {
		base, _ := recoveryPlanFixture()
		sources := make([]recoveryCapacityParticipant, 3)
		spares := make([]recoveryCapacitySpare, 3)
		for i := 0; i < 3; i++ {
			sources[i] = recoveryCapacityParticipant{Target: fmt.Sprintf("s%d", i), PodUID: fmt.Sprintf("sp%d", i), Incarnation: fmt.Sprintf("si%d", i), Node: "worker-1", Demand: base[0].Demand}
			spares[i] = recoveryCapacitySpare{Target: fmt.Sprintf("d%d", i), PodUID: fmt.Sprintf("dp%d", i), Incarnation: fmt.Sprintf("di%d", i), Node: "worker-2", Budget: base[0].Demand}
			for j := 0; j < 3; j++ {
				if mask&(1<<(j*3+i)) != 0 {
					spares[i].EligibleSources = append(spares[i].EligibleSources, fmt.Sprintf("s%d", j))
				}
			}
		}
		possible := false
		for _, p := range permutations {
			valid := true
			for s, d := range p {
				valid = valid && mask&(1<<(s*3+d)) != 0
			}
			possible = possible || valid
		}
		plan, err := planNodeRecoveryCapacity(sources, spares, 6)
		if (err == nil) != possible {
			t.Fatalf("mask %d: %+v %v possible=%v", mask, plan, err, possible)
		}
		if plan != nil {
			seen := map[string]bool{}
			for s, d := range plan.Replacements["worker-1"] {
				if seen[d] {
					t.Fatalf("reused destination %s", d)
				}
				seen[d] = true
				var si, di int
				fmt.Sscanf(s, "s%d", &si)
				fmt.Sscanf(d, "d%d", &di)
				if mask&(1<<(si*3+di)) == 0 {
					t.Fatal("ineligible edge")
				}
			}
		}
	}
}

func TestNodeRecoveryCapacityDeterministicWithoutInputMutation(t *testing.T) {
	s, c := recoveryPlanFixture()
	first, err := planNodeRecoveryCapacity(s, c, 4)
	if err != nil {
		t.Fatal(err)
	}
	s[0], s[1] = s[1], s[0]
	c[0], c[1] = c[1], c[0]
	second, err := planNodeRecoveryCapacity(s, c, 4)
	if err != nil || !reflect.DeepEqual(first, second) || s[0].Target != "b" || c[0].Target != "d" {
		t.Fatalf("unstable or mutating planner: %+v %v", second, err)
	}
}
