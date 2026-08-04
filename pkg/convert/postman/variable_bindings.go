package postman

import (
	"container/heap"
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
)

const (
	maxPostmanResolvedVariableBytes      = 1 << 20
	maxPostmanResolvedVariableTotalBytes = 16 << 20
)

type postmanVariableLookup struct {
	values       map[string]string
	bindingNames map[string]string
	// inlineLiterals identifies deterministic pm.*.set assignments whose value
	// changes during the collection. The DSL has no equivalent mutable-variable
	// action, so these bindings are materialized into each request at its source
	// position instead of pointing every request at one initial parameter value.
	inlineLiterals map[string]struct{}
}

func (lookup postmanVariableLookup) Get(name string) (value string, bindingName string, ok bool) {
	canonical := postmanVariableCanonicalName(name)
	if canonical == "" {
		return "", "", false
	}
	value, ok = lookup.values[canonical]
	if !ok {
		return "", "", false
	}
	return value, lookup.bindingNames[canonical], true
}

func (lookup postmanVariableLookup) ShouldInline(name string) bool {
	_, ok := lookup.inlineLiterals[postmanVariableCanonicalName(name)]
	return ok
}

type postmanVariableBindingLimits struct {
	Dependencies          requestimport.BindingDependencyLimits
	MaxResolvedValueBytes int
	MaxResolvedTotalBytes int
}

func defaultPostmanVariableBindingLimits() postmanVariableBindingLimits {
	return postmanVariableBindingLimits{
		Dependencies:          requestimport.DefaultBindingDependencyLimits(),
		MaxResolvedValueBytes: maxPostmanResolvedVariableBytes,
		MaxResolvedTotalBytes: maxPostmanResolvedVariableTotalBytes,
	}
}

type postmanVariableBindingResult struct {
	RuntimeValues         map[string]string
	Lookup                postmanVariableLookup
	SourceValues          map[string]string
	Dependencies          map[string][]string
	ExcludeFromDataSource map[string]struct{}
	InvalidSourceValues   []string
	ReviewRequired        bool
	OmitSourceReview      bool
}

// resolvePostmanVariableBindings applies Postman's already-selected effective
// collection/environment values to a bounded dependency graph. Source scope
// precedence remains in the adapter; the shared graph helper supplies stable
// missing/cycle closure. Invalid nodes are deliberately omitted so shared
// request readiness turns every use into a durable import_missing_variables
// gate instead of allowing a one-pass runtime substitution to send raw {{...}}.
func resolvePostmanVariableBindings(
	values map[string]string,
	diagnostics *diag.Collector,
) postmanVariableBindingResult {
	return resolvePostmanVariableBindingsWithLimits(
		values,
		diagnostics,
		defaultPostmanVariableBindingLimits(),
	)
}

func resolvePostmanVariableBindingsWithLimits(
	values map[string]string,
	diagnostics *diag.Collector,
	limits postmanVariableBindingLimits,
) postmanVariableBindingResult {
	limits = normalizePostmanVariableBindingLimits(limits)
	if len(values) > limits.Dependencies.MaxNodes {
		return limitedPostmanVariableBindings(diagnostics, limits)
	}
	for name := range values {
		if len(strings.TrimSpace(name)) > limits.Dependencies.MaxNameBytes {
			return limitedPostmanVariableBindings(diagnostics, limits)
		}
	}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)

	collisionGroups := make(map[string][]string)
	for _, name := range names {
		canonical := postmanVariableCanonicalName(name)
		collisionGroups[canonical] = append(collisionGroups[canonical], name)
	}
	colliding := make(map[string]struct{})
	canonicalSourceNames := make(map[string]string, len(collisionGroups))
	for _, canonical := range sortedPostmanVariableKeys(collisionGroups) {
		group := collisionGroups[canonical]
		if canonical != "" && len(group) == 1 {
			canonicalSourceNames[canonical] = group[0]
			continue
		}
		for _, name := range group {
			colliding[name] = struct{}{}
		}
		if diagnostics != nil {
			diagnostics.Add(
				diag.Warning,
				"Postman variable shadowing",
				"collection/environment variables",
				fmt.Sprintf("%d source variable name(s) map to the same APEX binding %q and require review", len(group), canonical),
			)
		}
	}

	nodes := make([]requestimport.BindingNode, 0, len(names))
	rawDependencies := make(map[string][]string, len(names))
	excludeFromDataSource := make(map[string]struct{})
	remainingReferences := limits.Dependencies.MaxEdges
	for _, name := range names {
		if _, collision := colliding[name]; collision {
			continue
		}
		references, consumed, exceeded := postmanVariableReferences(values[name], remainingReferences)
		if exceeded {
			return limitedPostmanVariableBindings(diagnostics, limits)
		}
		remainingReferences -= consumed
		dependencySet := make(map[string]struct{}, len(references))
		for _, reference := range references {
			dependency := strings.TrimSpace(reference)
			if sourceName, ok := canonicalSourceNames[postmanVariableCanonicalName(reference)]; ok {
				dependency = sourceName
			}
			if dependency != "" {
				dependencySet[dependency] = struct{}{}
			}
		}
		dependencies := sortedPostmanVariableSet(dependencySet)
		rawDependencies[name] = dependencies
		if len(dependencies) > 0 {
			excludeFromDataSource[postmanVariableCanonicalName(name)] = struct{}{}
		}
		nodes = append(nodes, requestimport.BindingNode{Name: name, Dependencies: dependencies})
	}
	analysis := requestimport.AnalyzeBindingDependenciesWithLimits(nodes, limits.Dependencies)
	if analysis.LimitExceeded {
		return limitedPostmanVariableBindings(diagnostics, limits)
	}
	invalid := make(map[string]struct{}, len(colliding)+len(analysis.InvalidBindings))
	for name := range colliding {
		invalid[name] = struct{}{}
	}
	for _, name := range analysis.InvalidBindings {
		invalid[name] = struct{}{}
	}

	if diagnostics != nil && len(analysis.MissingByBinding) > 0 {
		missing := make(map[string]struct{})
		for _, dependencies := range analysis.MissingByBinding {
			for _, dependency := range dependencies {
				missing[dependency] = struct{}{}
			}
		}
		diagnostics.Add(
			diag.Warning,
			"Postman variable dependency",
			"collection/environment variables",
			fmt.Sprintf("%d referenced source variable binding(s) are missing: %s", len(missing), strings.Join(sortedPostmanVariableSet(missing), ", ")),
		)
	}
	if diagnostics != nil {
		for _, cycle := range analysis.Cycles {
			diagnostics.Add(
				diag.Warning,
				"Postman variable cycle",
				"collection/environment variables",
				"cyclic source variable bindings require review: "+strings.Join(cycle, " -> "),
			)
		}
	}

	nodeSet := make(map[string]struct{}, len(nodes))
	dependents := make(map[string][]string, len(nodes))
	for _, node := range nodes {
		nodeSet[node.Name] = struct{}{}
		dependents[node.Name] = nil
	}
	for _, node := range nodes {
		for _, dependency := range rawDependencies[node.Name] {
			if _, exists := nodeSet[dependency]; !exists {
				continue
			}
			dependents[dependency] = append(dependents[dependency], node.Name)
		}
	}

	indegree := make(map[string]int, len(nodes))
	ready := &postmanVariableNameHeap{}
	heap.Init(ready)
	for _, node := range nodes {
		if _, blocked := invalid[node.Name]; blocked {
			continue
		}
		indegree[node.Name] = len(rawDependencies[node.Name])
		if indegree[node.Name] == 0 {
			heap.Push(ready, node.Name)
		}
	}

	resolved := make(map[string]string, len(names))
	resolvedBytes := 0
	for ready.Len() > 0 {
		name := heap.Pop(ready).(string)
		if _, blocked := invalid[name]; !blocked {
			value, ok := expandPostmanVariableValue(
				values[name],
				limits.MaxResolvedValueBytes,
				func(reference string) (string, bool) {
					sourceName, exists := canonicalSourceNames[postmanVariableCanonicalName(reference)]
					if !exists {
						return "", false
					}
					replacement, exists := resolved[sourceName]
					return replacement, exists
				},
			)
			if !ok || len(value) > limits.MaxResolvedTotalBytes-resolvedBytes {
				propagateInvalidPostmanVariables(invalid, dependents, name)
			} else {
				resolved[name] = value
				resolvedBytes += len(value)
			}
		}
		for _, dependent := range dependents[name] {
			if remaining := indegree[dependent] - 1; remaining <= 0 {
				indegree[dependent] = 0
				heap.Push(ready, dependent)
			} else {
				indegree[dependent] = remaining
			}
		}
	}
	// The shared analysis should have removed every cycle. Fail closed if a
	// future graph change leaves any otherwise-valid node unprocessed.
	for _, node := range nodes {
		if _, blocked := invalid[node.Name]; blocked {
			continue
		}
		if _, ok := resolved[node.Name]; !ok {
			propagateInvalidPostmanVariables(invalid, dependents, node.Name)
		}
	}

	if diagnostics != nil {
		analysisInvalid := make(map[string]struct{}, len(analysis.InvalidBindings))
		for _, name := range analysis.InvalidBindings {
			analysisInvalid[name] = struct{}{}
		}
		failed := make(map[string]struct{})
		for name := range invalid {
			if _, collision := colliding[name]; collision {
				continue
			}
			if _, graphInvalid := analysisInvalid[name]; graphInvalid {
				continue
			}
			failed[name] = struct{}{}
		}
		if len(failed) > 0 {
			diagnostics.Add(
				diag.Warning,
				"Postman variable expansion",
				"collection/environment variables",
				fmt.Sprintf("%d source variable binding(s) exceeded the bounded expansion limit", len(failed)),
			)
		}
	}

	runtimeValues := make(map[string]string, len(resolved))
	lookup := postmanVariableLookup{
		values:       make(map[string]string, len(resolved)),
		bindingNames: make(map[string]string, len(resolved)),
	}
	sourceValues := make(map[string]string, len(resolved))
	normalizedDependencies := make(map[string][]string, len(resolved))
	invalidSourceValues := make(map[string]struct{}, len(invalid))
	for _, name := range names {
		if _, blocked := invalid[name]; blocked {
			if value := values[name]; strings.TrimSpace(value) != "" {
				invalidSourceValues[value] = struct{}{}
			}
			continue
		}
		value, ok := resolved[name]
		if !ok {
			continue
		}
		runtimeValues[name] = value
		canonical := postmanVariableCanonicalName(name)
		lookup.values[canonical] = value
		lookup.bindingNames[canonical] = normalizeVariableName(name)
		// Credential projection must see the same effective value that the
		// runtime parameter receives. Dependency closure lets the adapter remove
		// every contributing source value from public parameters and review.
		sourceValues[name] = value
		normalizedName := normalizeVariableName(name)
		for _, dependency := range rawDependencies[name] {
			if _, available := resolved[dependency]; !available {
				continue
			}
			normalizedDependencies[normalizedName] = append(
				normalizedDependencies[normalizedName],
				normalizeVariableName(dependency),
			)
		}
		normalizedDependencies[normalizedName] = uniqueSortedPostmanVariables(normalizedDependencies[normalizedName])
	}
	return postmanVariableBindingResult{
		RuntimeValues:         runtimeValues,
		Lookup:                lookup,
		SourceValues:          sourceValues,
		Dependencies:          normalizedDependencies,
		ExcludeFromDataSource: excludeFromDataSource,
		InvalidSourceValues:   sortedPostmanVariableSet(invalidSourceValues),
	}
}

func normalizePostmanVariableBindingLimits(limits postmanVariableBindingLimits) postmanVariableBindingLimits {
	defaults := defaultPostmanVariableBindingLimits()
	if limits.Dependencies.MaxNodes <= 0 {
		limits.Dependencies.MaxNodes = defaults.Dependencies.MaxNodes
	}
	if limits.Dependencies.MaxEdges <= 0 {
		limits.Dependencies.MaxEdges = defaults.Dependencies.MaxEdges
	}
	if limits.Dependencies.MaxNameBytes <= 0 {
		limits.Dependencies.MaxNameBytes = defaults.Dependencies.MaxNameBytes
	}
	if limits.MaxResolvedValueBytes <= 0 {
		limits.MaxResolvedValueBytes = defaults.MaxResolvedValueBytes
	}
	if limits.MaxResolvedTotalBytes <= 0 {
		limits.MaxResolvedTotalBytes = defaults.MaxResolvedTotalBytes
	}
	return limits
}

func limitedPostmanVariableBindings(
	diagnostics *diag.Collector,
	limits postmanVariableBindingLimits,
) postmanVariableBindingResult {
	if diagnostics != nil {
		diagnostics.Add(
			diag.Warning,
			"Postman variable dependency limit",
			"collection/environment variables",
			fmt.Sprintf(
				"source variable graph exceeded the bounded work budget (%d nodes, %d references, %d-byte names) and requires review",
				limits.Dependencies.MaxNodes,
				limits.Dependencies.MaxEdges,
				limits.Dependencies.MaxNameBytes,
			),
		)
	}
	return postmanVariableBindingResult{
		RuntimeValues:    map[string]string{},
		Lookup:           postmanVariableLookup{},
		SourceValues:     map[string]string{},
		Dependencies:     map[string][]string{},
		ReviewRequired:   true,
		OmitSourceReview: true,
	}
}

// postmanVariableReferences bounds template recognition by occurrence, not
// merely by unique dependency, so repeated source tokens cannot force an
// unbounded regex result allocation.
func postmanVariableReferences(value string, maxMatches int) ([]string, int, bool) {
	if maxMatches <= 0 {
		if variablePattern.FindStringIndex(value) != nil {
			return nil, 0, true
		}
		return nil, 0, false
	}
	matches := variablePattern.FindAllStringSubmatch(value, maxMatches+1)
	if len(matches) > maxMatches {
		return nil, maxMatches, true
	}
	set := make(map[string]struct{})
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		name := strings.TrimSpace(match[1])
		if name == "" || isApexExpressionName(name) {
			continue
		}
		set[name] = struct{}{}
	}
	return sortedPostmanVariableSet(set), len(matches), false
}

func expandPostmanVariableValue(
	value string,
	maxBytes int,
	resolve func(string) (string, bool),
) (string, bool) {
	matches := variablePattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value, len(value) <= maxBytes
	}
	var builder strings.Builder
	builder.Grow(min(len(value), maxBytes))
	last := 0
	for _, match := range matches {
		if len(match) != 4 {
			return "", false
		}
		if match[0] > last {
			if !writeBoundedPostmanVariable(&builder, value[last:match[0]], maxBytes) {
				return "", false
			}
		}
		name := strings.TrimSpace(value[match[2]:match[3]])
		if isApexExpressionName(name) {
			if !writeBoundedPostmanVariable(&builder, value[match[0]:match[1]], maxBytes) {
				return "", false
			}
		} else {
			replacement, ok := resolve(name)
			if !ok || !writeBoundedPostmanVariable(&builder, replacement, maxBytes) {
				return "", false
			}
		}
		last = match[1]
	}
	if last < len(value) && !writeBoundedPostmanVariable(&builder, value[last:], maxBytes) {
		return "", false
	}
	return builder.String(), true
}

func writeBoundedPostmanVariable(builder *strings.Builder, value string, maxBytes int) bool {
	if len(value) > maxBytes || builder.Len() > maxBytes-len(value) {
		return false
	}
	builder.WriteString(value)
	return true
}

func propagateInvalidPostmanVariables(
	invalid map[string]struct{},
	dependents map[string][]string,
	root string,
) {
	if _, exists := invalid[root]; exists {
		return
	}
	invalid[root] = struct{}{}
	queue := []string{root}
	for index := 0; index < len(queue); index++ {
		for _, dependent := range dependents[queue[index]] {
			if _, exists := invalid[dependent]; exists {
				continue
			}
			invalid[dependent] = struct{}{}
			queue = append(queue, dependent)
		}
	}
}

func postmanVariableCanonicalName(value string) string {
	return strings.ToLower(normalizeVariableName(value))
}

type postmanVariableNameHeap []string

func (names postmanVariableNameHeap) Len() int           { return len(names) }
func (names postmanVariableNameHeap) Less(i, j int) bool { return names[i] < names[j] }
func (names postmanVariableNameHeap) Swap(i, j int)      { names[i], names[j] = names[j], names[i] }
func (names *postmanVariableNameHeap) Push(value any)    { *names = append(*names, value.(string)) }
func (names *postmanVariableNameHeap) Pop() any {
	previous := *names
	last := len(previous) - 1
	value := previous[last]
	*names = previous[:last]
	return value
}

func sortedPostmanVariableKeys(input map[string][]string) []string {
	out := make([]string, 0, len(input))
	for name := range input {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func sortedPostmanVariableSet(input map[string]struct{}) []string {
	out := make([]string, 0, len(input))
	for name := range input {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func uniqueSortedPostmanVariables(input []string) []string {
	set := make(map[string]struct{}, len(input))
	for _, name := range input {
		if name = strings.TrimSpace(name); name != "" {
			set[name] = struct{}{}
		}
	}
	return sortedPostmanVariableSet(set)
}
