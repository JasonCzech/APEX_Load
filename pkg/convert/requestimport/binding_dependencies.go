package requestimport

import (
	"sort"
	"strings"
)

const (
	defaultBindingDependencyMaxNodes     = 10_000
	defaultBindingDependencyMaxEdges     = 100_000
	defaultBindingDependencyMaxNameBytes = 1 << 10
)

// BindingDependencyLimits bound source-provided graph work before any
// adjacency maps or traversal stacks are allocated.
type BindingDependencyLimits struct {
	MaxNodes     int
	MaxEdges     int
	MaxNameBytes int
}

// DefaultBindingDependencyLimits returns the source-neutral work budget used
// by import adapters.
func DefaultBindingDependencyLimits() BindingDependencyLimits {
	return BindingDependencyLimits{
		MaxNodes:     defaultBindingDependencyMaxNodes,
		MaxEdges:     defaultBindingDependencyMaxEdges,
		MaxNameBytes: defaultBindingDependencyMaxNameBytes,
	}
}

// BindingNode describes one source binding after an adapter has applied its
// source-specific precedence rules. Dependencies contain source binding names,
// not executable values.
type BindingNode struct {
	Name         string
	Dependencies []string
}

// BindingDependencyAnalysis is a disclosure-safe, deterministic view of a
// source binding graph. InvalidBindings includes bindings that directly miss a
// dependency, participate in a cycle, or transitively depend on either.
type BindingDependencyAnalysis struct {
	MissingByBinding map[string][]string `json:"missing_by_binding,omitempty"`
	Cycles           [][]string          `json:"cycles,omitempty"`
	InvalidBindings  []string            `json:"invalid_bindings,omitempty"`
	LimitExceeded    bool                `json:"limit_exceeded,omitempty"`
}

// AnalyzeBindingDependencies evaluates a source-neutral dependency graph. The
// source adapter remains responsible for recognizing references and resolving
// scope precedence; this helper provides deterministic missing/cycle closure so
// every importer does not need to grow its own graph policy.
func AnalyzeBindingDependencies(input []BindingNode) BindingDependencyAnalysis {
	return AnalyzeBindingDependenciesWithLimits(input, DefaultBindingDependencyLimits())
}

// AnalyzeBindingDependenciesWithLimits is the bounded form used by adapters
// that need to share a smaller source-specific budget in tests or constrained
// environments. Exceeding a limit returns only LimitExceeded: callers must
// conservatively invalidate the source graph rather than use partial results.
func AnalyzeBindingDependenciesWithLimits(
	input []BindingNode,
	limits BindingDependencyLimits,
) BindingDependencyAnalysis {
	limits = normalizeBindingDependencyLimits(limits)
	if bindingDependencyLimitExceeded(input, limits) {
		return BindingDependencyAnalysis{LimitExceeded: true}
	}

	dependencies := make(map[string][]string, len(input))
	for _, node := range input {
		name := strings.TrimSpace(node.Name)
		if name == "" {
			continue
		}
		dependencies[name] = append(dependencies[name], node.Dependencies...)
	}
	names := sortedBindingKeys(dependencies)
	for _, name := range names {
		dependencies[name] = uniqueSortedBindingNames(dependencies[name])
	}

	missingByBinding := make(map[string][]string)
	for _, name := range names {
		for _, dependency := range dependencies[name] {
			if _, exists := dependencies[dependency]; !exists {
				missingByBinding[name] = append(missingByBinding[name], dependency)
			}
		}
		if len(missingByBinding[name]) > 0 {
			missingByBinding[name] = uniqueSortedBindingNames(missingByBinding[name])
		}
	}

	reverse := bindingReverseDependencies(names, dependencies)
	cycles := bindingStronglyConnectedCycles(names, dependencies, reverse)
	invalid := make(map[string]struct{}, len(missingByBinding))
	for name := range missingByBinding {
		invalid[name] = struct{}{}
	}
	for _, cycle := range cycles {
		for _, name := range cycle {
			invalid[name] = struct{}{}
		}
	}
	propagateInvalidBindings(invalid, reverse)

	if len(missingByBinding) == 0 {
		missingByBinding = nil
	}
	return BindingDependencyAnalysis{
		MissingByBinding: missingByBinding,
		Cycles:           cycles,
		InvalidBindings:  sortedBindingSet(invalid),
	}
}

func normalizeBindingDependencyLimits(limits BindingDependencyLimits) BindingDependencyLimits {
	defaults := DefaultBindingDependencyLimits()
	if limits.MaxNodes <= 0 {
		limits.MaxNodes = defaults.MaxNodes
	}
	if limits.MaxEdges <= 0 {
		limits.MaxEdges = defaults.MaxEdges
	}
	if limits.MaxNameBytes <= 0 {
		limits.MaxNameBytes = defaults.MaxNameBytes
	}
	return limits
}

func bindingDependencyLimitExceeded(input []BindingNode, limits BindingDependencyLimits) bool {
	if len(input) > limits.MaxNodes {
		return true
	}
	edges := 0
	for _, node := range input {
		if len(strings.TrimSpace(node.Name)) > limits.MaxNameBytes ||
			len(node.Dependencies) > limits.MaxEdges-edges {
			return true
		}
		edges += len(node.Dependencies)
		for _, dependency := range node.Dependencies {
			if len(strings.TrimSpace(dependency)) > limits.MaxNameBytes {
				return true
			}
		}
	}
	return false
}

func bindingReverseDependencies(names []string, dependencies map[string][]string) map[string][]string {
	reverse := make(map[string][]string, len(names))
	for _, name := range names {
		reverse[name] = nil
	}
	for _, name := range names {
		for _, dependency := range dependencies[name] {
			if _, exists := dependencies[dependency]; !exists {
				continue
			}
			reverse[dependency] = append(reverse[dependency], name)
		}
	}
	return reverse
}

type bindingTraversalFrame struct {
	name string
	next int
}

// bindingStronglyConnectedCycles uses iterative Kosaraju traversal so a
// source-controlled dependency chain cannot consume the Go call stack.
func bindingStronglyConnectedCycles(
	names []string,
	dependencies map[string][]string,
	reverse map[string][]string,
) [][]string {
	visited := make(map[string]struct{}, len(names))
	finished := make([]string, 0, len(names))
	for _, root := range names {
		if _, seen := visited[root]; seen {
			continue
		}
		visited[root] = struct{}{}
		stack := []bindingTraversalFrame{{name: root}}
		for len(stack) > 0 {
			frame := &stack[len(stack)-1]
			if frame.next < len(dependencies[frame.name]) {
				dependency := dependencies[frame.name][frame.next]
				frame.next++
				if _, exists := dependencies[dependency]; !exists {
					continue
				}
				if _, seen := visited[dependency]; seen {
					continue
				}
				visited[dependency] = struct{}{}
				stack = append(stack, bindingTraversalFrame{name: dependency})
				continue
			}
			finished = append(finished, frame.name)
			stack = stack[:len(stack)-1]
		}
	}

	assigned := make(map[string]struct{}, len(names))
	var cycles [][]string
	for index := len(finished) - 1; index >= 0; index-- {
		root := finished[index]
		if _, seen := assigned[root]; seen {
			continue
		}
		assigned[root] = struct{}{}
		component := make([]string, 0, 1)
		stack := []string{root}
		for len(stack) > 0 {
			last := len(stack) - 1
			name := stack[last]
			stack = stack[:last]
			component = append(component, name)
			for _, dependent := range reverse[name] {
				if _, seen := assigned[dependent]; seen {
					continue
				}
				assigned[dependent] = struct{}{}
				stack = append(stack, dependent)
			}
		}
		sort.Strings(component)
		if len(component) > 1 || bindingSelfCycle(component, dependencies) {
			cycles = append(cycles, component)
		}
	}
	sort.SliceStable(cycles, func(i, j int) bool {
		return strings.Join(cycles[i], "\x00") < strings.Join(cycles[j], "\x00")
	})
	return cycles
}

func propagateInvalidBindings(invalid map[string]struct{}, reverse map[string][]string) {
	queue := sortedBindingSet(invalid)
	for index := 0; index < len(queue); index++ {
		for _, dependent := range reverse[queue[index]] {
			if _, alreadyInvalid := invalid[dependent]; alreadyInvalid {
				continue
			}
			invalid[dependent] = struct{}{}
			queue = append(queue, dependent)
		}
	}
}

func bindingSelfCycle(component []string, dependencies map[string][]string) bool {
	if len(component) != 1 {
		return false
	}
	name := component[0]
	for _, dependency := range dependencies[name] {
		if dependency == name {
			return true
		}
	}
	return false
}

func uniqueSortedBindingNames(input []string) []string {
	set := make(map[string]struct{}, len(input))
	for _, name := range input {
		if name = strings.TrimSpace(name); name != "" {
			set[name] = struct{}{}
		}
	}
	return sortedBindingSet(set)
}

func sortedBindingKeys(input map[string][]string) []string {
	out := make([]string, 0, len(input))
	for name := range input {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func sortedBindingSet(input map[string]struct{}) []string {
	if len(input) == 0 {
		return nil
	}
	out := make([]string, 0, len(input))
	for name := range input {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
