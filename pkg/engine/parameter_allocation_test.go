package engine

import (
	"context"
	"github.com/jasonczech/apexload/pkg/dsl"
	"testing"
	"time"
)

func TestAutomaticAllocationFrozenBlocksAcrossShards(t *testing.T) {
	params := []dsl.Parameter{{Name: "user", Type: "file", DataSource: "users", Column: "name", Selection: "unique", Update: "each_iteration", AutoAllocate: true, OnExhaustion: "last", StartRow: 2}}
	sources := []dsl.DataSource{{Name: "users", Type: "csv", InlineData: "name\nskip\na\nb\nc\nd\ne\nf\ng\n", FirstRowHeader: true, Strategy: "unique"}}
	for _, tc := range []struct {
		name              string
		iterations, block int
		last0, last1      string
	}{{"duration", 0, 3, "c", "f"}, {"iterations", 4, 4, "d", "g"}} {
		t.Run(tc.name, func(t *testing.T) {
			for ordinal := 0; ordinal < 2; ordinal++ {
				provider, err := NewShardedDataProvider(sources, DataShardConfig{Ordinal: uint32(ordinal), Count: 2})
				if err != nil {
					t.Fatal(err)
				}
				a := ParameterAllocation{Population: 2, Offset: ordinal, Iterations: tc.iterations}
				_, preview, err := CompileParameterAllocation(params, provider, a)
				if err != nil {
					t.Fatal(err)
				}
				if preview[0].BlockSize != tc.block {
					t.Fatalf("block=%d", preview[0].BlockSize)
				}
				runtime, err := newAllocatedParameterRuntime(params, provider, 1000000+ordinal*1000000, a)
				if err != nil {
					t.Fatal(err)
				}
				runtime.Prime()
				expected := []string{"a", "d"}
				if tc.iterations > 0 {
					expected[1] = "e"
				}
				if got, ok := runtime.ResolveNamed("user"); !ok || got != expected[ordinal] {
					t.Fatalf("ordinal %d first=%q %v", ordinal, got, ok)
				}
				for i := 0; i < 8; i++ {
					runtime.RefreshIteration()
				}
				last := tc.last0
				if ordinal == 1 {
					last = tc.last1
				}
				if got, _ := runtime.ResolveNamed("user"); got != last {
					t.Fatalf("last=%q want %q", got, last)
				}
			}
		})
	}
}
func TestAutomaticAllocationCapacityOnceAndExplicit(t *testing.T) {
	p := dsl.Parameter{Name: "p", Type: "list", Values: []string{"a", "b", "c"}, Selection: "unique", AutoAllocate: true, Update: "once", OnExhaustion: "last"}
	for _, tc := range []struct {
		population, iterations, block int
		fail                          bool
	}{{2, 100, 1, false}, {4, 0, 0, true}, {2, 0, 2, false}, {3, 0, 2, true}} {
		p.BlockSize = 0
		if tc.block == 2 {
			p.BlockSize = 2
		}
		_, preview, err := CompileParameterAllocation([]dsl.Parameter{p}, nil, ParameterAllocation{Population: tc.population, Iterations: tc.iterations})
		if (err != nil) != tc.fail {
			t.Fatalf("%+v err=%v", tc, err)
		}
		if err == nil && preview[0].BlockSize != tc.block {
			t.Fatalf("%+v preview=%+v", tc, preview)
		}
	}
	p.BlockSize = 1
	runtime := NewParameterRuntime([]dsl.Parameter{p}, nil, 9)
	runtime.Prime()
	if value, ok := runtime.ResolveNamed("p"); ok {
		t.Fatalf("unallocated user got %q", value)
	}
}

func TestAllocatedPopulationRemainsStableDuringPlannedRamp(t *testing.T) {
	state := installGroupIdentityCapture(t, 2)
	script := identityCaptureScript("allocated")
	script.DataSources = nil
	script.Parameters = []dsl.Parameter{{Name: "row", Type: "list", Values: []string{"a", "b", "c", "d"}, Selection: "unique", Update: "once", AutoAllocate: true, OnExhaustion: "last"}}
	script.Actions[0].Target = "/{{param.row}}"
	mge, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "allocated", Script: script, VUsers: 2, VUserIDOffset: 1000000, RampUp: 100 * time.Millisecond}}, Duration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	cancel, finished, runErr := runGroupIdentityEngine(t, mge)
	waitForIdentityCaptures(t, state)
	got := state.snapshot("allocated")
	if got["/a"] == 0 || got["/b"] == 0 || len(got) != 2 {
		t.Fatalf("ramp allocation=%v", got)
	}
	if err := mge.ScaleVUsers("allocated", 3); err == nil {
		t.Fatal("unsafe population change accepted")
	}
	cancel()
	<-finished
	if *runErr != nil {
		t.Fatal(*runErr)
	}
}

func TestUniqueAllocationNeverWrapsIntoAnotherUsersBlock(t *testing.T) {
	for _, exhaustion := range []string{"last", "wrap", "empty", "error"} {
		p := dsl.Parameter{Name: "p", Type: "list", Values: []string{"owned"}, Selection: "unique", BlockSize: 1, Update: "once", OnExhaustion: exhaustion}
		runtime := NewParameterRuntime([]dsl.Parameter{p}, nil, 2)
		runtime.Prime()
		if value, ok := runtime.ResolveNamed("p"); ok && value != "" {
			t.Fatalf("%s allocated another user's row %q", exhaustion, value)
		}
	}
}
func TestAllocationParallelEachOccurrenceRejected(t *testing.T) {
	s := &dsl.Script{Parameters: []dsl.Parameter{{Name: "p", Selection: "unique", BlockSize: 2, Update: "each_occurrence"}}, Actions: []dsl.Action{{Type: "control_parallel"}}}
	if err := ValidateParameterAllocationTopology(s); err == nil {
		t.Fatal("unsafe branch allocation accepted")
	}
	if _, err := ReplayScript(context.Background(), s, ReplayOptions{}); err == nil {
		t.Fatal("replay accepted unsafe branch allocation")
	}
	s.Parameters[0].Update = "once"
	if err := ValidateParameterAllocationTopology(s); err != nil {
		t.Fatal(err)
	}
}

func TestScenarioAllocationAcrossGroupsAndIncompatibleOverrides(t *testing.T) {
	s := &dsl.Script{ID: "shared", Parameters: []dsl.Parameter{{Name: "p", Type: "list", Values: []string{"a", "b", "c", "d", "e", "f"}, Selection: "unique", AutoAllocate: true}}}
	contexts, err := PlanScenarioParameterAllocations([]*dsl.Script{s, s}, []int{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if contexts[0].Population != 3 || contexts[1].Population != 3 || contexts[0].Offset != 0 || contexts[1].Offset != 1 {
		t.Fatalf("contexts=%+v", contexts)
	}
	for i, expected := range []string{"a", "c"} {
		pr, err := newAllocatedParameterRuntime(s.Parameters, nil, 9000000, contexts[i])
		if err != nil {
			t.Fatal(err)
		}
		pr.Prime()
		value, _ := pr.ResolveNamed("p")
		if value != expected {
			t.Fatalf("group %d got %q", i, value)
		}
	}
	changed := *s
	changed.Config.RunLogic.Iterations = 4
	if _, err := PlanScenarioParameterAllocations([]*dsl.Script{s, &changed}, []int{1, 2}); err == nil {
		t.Fatal("incompatible block sizes accepted")
	}
}

func TestExplicitAllocationReportsIterationExhaustionBeforeLaunch(t *testing.T) {
	p := dsl.Parameter{Name: "p", Type: "list", Values: []string{"a", "b", "c", "d", "e", "f"}, Selection: "unique", BlockSize: 2, Update: "each_iteration", OnExhaustion: "error"}
	if _, _, err := CompileParameterAllocation([]dsl.Parameter{p}, nil, ParameterAllocation{Population: 2, Iterations: 3}); err == nil {
		t.Fatal("explicit block would exhaust before planned iterations")
	}
	p.OnExhaustion = "last"
	if _, _, err := CompileParameterAllocation([]dsl.Parameter{p}, nil, ParameterAllocation{Population: 2, Iterations: 3}); err != nil {
		t.Fatal(err)
	}
}
