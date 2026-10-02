package engine

import (
	"math"
	"strconv"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestUniqueNumberBlocksUseRunOrdinalsAcrossScenarioGroups(t *testing.T) {
	p := dsl.Parameter{Name: "trace", Type: "counter", Selection: "unique", Start: 2, Step: 1, BlockSize: 999999, PadWidth: 1, Update: "each_iteration", OnExhaustion: "wrap"}
	s := &dsl.Script{ID: "same-script", Parameters: []dsl.Parameter{p}}
	allocations, err := PlanScenarioParameterAllocations([]*dsl.Script{s, s}, []int{2, 1})
	if err != nil || allocations[0].Population != 3 || allocations[1].Offset != 2 {
		t.Fatalf("scenario allocations=%v err=%v", allocations, err)
	}
	for ordinal, first := range []string{"2", "1000001", "2000000"} {
		pr, err := newAllocatedParameterRuntime(s.Parameters, nil, 9000000+ordinal*1000000, ParameterAllocation{Population: 3, Offset: ordinal})
		if err != nil {
			t.Fatal(err)
		}
		pr.Prime()
		if got, ok := pr.ResolveNamed("trace"); !ok || got != first {
			t.Fatalf("ordinal=%d first=%q %v want=%s", ordinal, got, ok, first)
		}
		pr.RefreshIteration()
		want := strconv.Itoa(3 + ordinal*999999)
		if got, ok := pr.ResolveNamed("trace"); !ok || got != want {
			t.Fatalf("ordinal=%d next=%q %v want=%s", ordinal, got, ok, want)
		}
	}
}

func TestUniqueNumberPerBlockExhaustion(t *testing.T) {
	for _, test := range []struct {
		policy, third string
		ok            bool
	}{{"wrap", "004", true}, {"last", "005", true}, {"empty", "", true}, {"error", "", false}} {
		t.Run(test.policy, func(t *testing.T) {
			p := dsl.Parameter{Name: "id", Type: "counter", Selection: "unique", Start: 2, Step: 1, BlockSize: 2, PadWidth: 3, Update: "each_occurrence", OnExhaustion: test.policy}
			pr, err := newAllocatedParameterRuntime([]dsl.Parameter{p}, nil, 7000000, ParameterAllocation{Population: 2, Offset: 1})
			if err != nil {
				t.Fatal(err)
			}
			for index, want := range []string{"004", "005"} {
				if value, ok := pr.ResolveNamed("id"); !ok || value != want {
					t.Fatalf("index=%d value=%q %v want=%s", index, value, ok, want)
				}
			}
			if value, ok := pr.ResolveNamed("id"); ok != test.ok || value != test.third {
				t.Fatalf("exhausted=%q %v want=%q %v", value, ok, test.third, test.ok)
			}
		})
	}
}

func TestUniqueNumberOverflowAndAllocationRejection(t *testing.T) {
	p := dsl.Parameter{Name: "id", Type: "counter", Selection: "unique", Start: math.MaxInt - 1, Step: 1, BlockSize: 2, Update: "each_occurrence", OnExhaustion: "wrap"}
	pr, err := newAllocatedParameterRuntime([]dsl.Parameter{p}, nil, 123456, ParameterAllocation{Population: 1, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []int{math.MaxInt - 1, math.MaxInt, math.MaxInt - 1} {
		if value, ok := pr.ResolveNamed("id"); !ok || value != strconv.Itoa(want) {
			t.Fatalf("integer boundary value=%q %v want=%d", value, ok, want)
		}
	}
	for _, allocation := range []ParameterAllocation{{Population: 2}, {Population: 1, Offset: -1}, {Population: 0}, {Population: 1, Offset: 1}} {
		if _, err := newAllocatedParameterRuntime([]dsl.Parameter{p}, nil, 0, allocation); err == nil {
			t.Fatalf("accepted invalid/overflowed allocation: %+v", allocation)
		}
	}
	p.Start, p.BlockSize, p.Update, p.OnExhaustion = 2, 2, "each_iteration", "error"
	if _, _, err := CompileParameterAllocation([]dsl.Parameter{p}, nil, ParameterAllocation{Population: 2, Iterations: 3}); err == nil {
		t.Fatal("abort policy allowed a run that necessarily exhausts its block")
	}
	for _, change := range []dsl.Parameter{
		{Name: "id", Type: "counter", Selection: "unique", Step: 1},
		{Name: "id", Type: "counter", Selection: "unique", Step: 2, BlockSize: 2},
		{Name: "id", Type: "counter", Selection: "unique", Step: 1, BlockSize: 2, End: 3},
	} {
		if value, ok := NewParameterRuntime([]dsl.Parameter{change}, nil, 0).ResolveNamed("id"); ok {
			t.Fatalf("invalid unique definition generated %q: %+v", value, change)
		}
	}
}

func TestUniqueNumberOnceAndParallelAllocationState(t *testing.T) {
	p := dsl.Parameter{Name: "id", Type: "counter", Selection: "unique", Start: 0, Step: 1, BlockSize: 1, Update: "once", OnExhaustion: "wrap"}
	pr, err := newAllocatedParameterRuntime([]dsl.Parameter{p}, nil, 999999, ParameterAllocation{Population: 3, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	pr.Prime()
	pr.RefreshIteration()
	if got, ok := pr.ResolveNamed("id"); !ok || got != "2" {
		t.Fatalf("once value=%q %v", got, ok)
	}
	branch := NewParameterRuntime([]dsl.Parameter{p}, nil, 123)
	branch.inheritAllocatedParameterState(pr)
	if got, ok := branch.ResolveNamed("id"); !ok || got != "2" {
		t.Fatalf("parallel branch lost assigned number=%q %v", got, ok)
	}
	if err := ValidateParameterAllocationTopology(&dsl.Script{Parameters: []dsl.Parameter{{Name: "id", Type: "counter", Selection: "unique", BlockSize: 1, Step: 1, Update: "each_occurrence"}}, Actions: []dsl.Action{{Type: "control_parallel"}}}); err == nil {
		t.Fatal("isolated parallel cursors may duplicate unique each-occurrence numbers")
	}
}
