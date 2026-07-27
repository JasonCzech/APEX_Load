package requestimport

import (
	"fmt"
	"reflect"
	"testing"
)

func TestAnalyzeBindingDependenciesMissingCyclesAndClosure(t *testing.T) {
	t.Parallel()

	got := AnalyzeBindingDependencies([]BindingNode{
		{Name: "valid", Dependencies: []string{"leaf"}},
		{Name: "leaf"},
		{Name: "missing_root", Dependencies: []string{"absent"}},
		{Name: "missing_parent", Dependencies: []string{"missing_root"}},
		{Name: "cycle_b", Dependencies: []string{"cycle_a"}},
		{Name: "cycle_a", Dependencies: []string{"cycle_b"}},
		{Name: "cycle_parent", Dependencies: []string{"cycle_a"}},
		{Name: "self", Dependencies: []string{"self"}},
	})

	if want := map[string][]string{"missing_root": {"absent"}}; !reflect.DeepEqual(got.MissingByBinding, want) {
		t.Fatalf("missing = %#v, want %#v", got.MissingByBinding, want)
	}
	if want := [][]string{{"cycle_a", "cycle_b"}, {"self"}}; !reflect.DeepEqual(got.Cycles, want) {
		t.Fatalf("cycles = %#v, want %#v", got.Cycles, want)
	}
	if want := []string{"cycle_a", "cycle_b", "cycle_parent", "missing_parent", "missing_root", "self"}; !reflect.DeepEqual(got.InvalidBindings, want) {
		t.Fatalf("invalid = %#v, want %#v", got.InvalidBindings, want)
	}
}

func TestAnalyzeBindingDependenciesMergesDuplicateNodesDeterministically(t *testing.T) {
	t.Parallel()

	got := AnalyzeBindingDependencies([]BindingNode{
		{Name: "a", Dependencies: []string{"c", "b"}},
		{Name: "b"},
		{Name: "a", Dependencies: []string{"b"}},
		{Name: "c"},
	})
	if len(got.MissingByBinding) != 0 || len(got.Cycles) != 0 || len(got.InvalidBindings) != 0 {
		t.Fatalf("analysis = %#v, want ready graph", got)
	}
}

func TestAnalyzeBindingDependenciesFailsClosedAtWorkLimits(t *testing.T) {
	t.Parallel()

	limits := BindingDependencyLimits{MaxNodes: 2, MaxEdges: 2, MaxNameBytes: 8}
	tests := []struct {
		name  string
		nodes []BindingNode
	}{
		{
			name: "nodes",
			nodes: []BindingNode{
				{Name: "a"},
				{Name: "b"},
				{Name: "c"},
			},
		},
		{
			name: "edges",
			nodes: []BindingNode{
				{Name: "a", Dependencies: []string{"b", "c", "d"}},
			},
		},
		{
			name: "name bytes",
			nodes: []BindingNode{
				{Name: "too-long-name"},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := AnalyzeBindingDependenciesWithLimits(test.nodes, limits)
			if !got.LimitExceeded {
				t.Fatalf("analysis = %#v, want fail-closed limit result", got)
			}
			if got.MissingByBinding != nil || got.Cycles != nil || got.InvalidBindings != nil {
				t.Fatalf("partial graph result escaped after limit: %#v", got)
			}
		})
	}
}

func TestAnalyzeBindingDependenciesDeepChainIsIterative(t *testing.T) {
	t.Parallel()

	const count = 4096
	nodes := make([]BindingNode, count)
	for index := range nodes {
		name := fmt.Sprintf("node_%04d", index)
		nodes[index].Name = name
		if index+1 < count {
			nodes[index].Dependencies = []string{fmt.Sprintf("node_%04d", index+1)}
		} else {
			nodes[index].Dependencies = []string{"absent"}
		}
	}
	got := AnalyzeBindingDependencies(nodes)
	if got.LimitExceeded {
		t.Fatal("bounded deep graph unexpectedly exceeded default limits")
	}
	if len(got.Cycles) != 0 {
		t.Fatalf("deep chain cycles = %#v", got.Cycles)
	}
	if len(got.InvalidBindings) != count {
		t.Fatalf("deep invalid closure = %d, want %d", len(got.InvalidBindings), count)
	}
	if want := map[string][]string{"node_4095": {"absent"}}; !reflect.DeepEqual(got.MissingByBinding, want) {
		t.Fatalf("deep missing = %#v, want %#v", got.MissingByBinding, want)
	}
}

func TestAnalyzeBindingDependenciesWideInvalidFanoutIsLinear(t *testing.T) {
	t.Parallel()

	const count = 4096
	nodes := make([]BindingNode, 0, count+1)
	nodes = append(nodes, BindingNode{Name: "root", Dependencies: []string{"absent"}})
	for index := 0; index < count; index++ {
		nodes = append(nodes, BindingNode{
			Name:         fmt.Sprintf("dependent_%04d", index),
			Dependencies: []string{"root"},
		})
	}
	got := AnalyzeBindingDependencies(nodes)
	if got.LimitExceeded {
		t.Fatal("bounded fanout unexpectedly exceeded default limits")
	}
	if len(got.InvalidBindings) != count+1 {
		t.Fatalf("fanout invalid closure = %d, want %d", len(got.InvalidBindings), count+1)
	}
	if want := map[string][]string{"root": {"absent"}}; !reflect.DeepEqual(got.MissingByBinding, want) {
		t.Fatalf("fanout missing = %#v, want %#v", got.MissingByBinding, want)
	}
}
