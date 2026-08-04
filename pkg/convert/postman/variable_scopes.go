package postman

import (
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
)

// Postman resolves variables from the narrowest available store. Data-file
// variables sit between environment and local variables, but the collection
// importer has no data-file input, so the stores represented here are the four
// scopes that a collection event can mutate.
type postmanVariableScope uint8

const (
	postmanGlobalScope postmanVariableScope = iota
	postmanCollectionScope
	postmanEnvironmentScope
	postmanLocalScope
	postmanVariableScopeCount
)

type postmanScopedValue struct {
	value    string
	literal  bool
	response *detectedExtractor
}

type postmanScopedVariables struct {
	values [postmanVariableScopeCount]map[string]postmanScopedValue
}

func newPostmanScopedVariables(collectionValues, environmentValues map[string]string) *postmanScopedVariables {
	state := &postmanScopedVariables{}
	for scope := postmanGlobalScope; scope < postmanVariableScopeCount; scope++ {
		state.values[scope] = make(map[string]postmanScopedValue)
	}
	for name, value := range collectionValues {
		state.values[postmanCollectionScope][name] = postmanScopedValue{value: value}
	}
	for name, value := range environmentValues {
		state.values[postmanEnvironmentScope][name] = postmanScopedValue{value: value}
	}
	return state
}

func parsePostmanVariableScope(value string) (postmanVariableScope, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "globals":
		return postmanGlobalScope, true
	case "collectionvariables":
		return postmanCollectionScope, true
	case "environment":
		return postmanEnvironmentScope, true
	case "variables":
		return postmanLocalScope, true
	default:
		return 0, false
	}
}

func (state *postmanScopedVariables) clearLocal() {
	if state == nil {
		return
	}
	state.values[postmanLocalScope] = make(map[string]postmanScopedValue)
}

func (state *postmanScopedVariables) set(scope postmanVariableScope, name string, value postmanScopedValue) {
	if state == nil || scope >= postmanVariableScopeCount {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if state.values[scope] == nil {
		state.values[scope] = make(map[string]postmanScopedValue)
	}
	state.values[scope][name] = value
}

func (state *postmanScopedVariables) effectiveValue(name string) (postmanScopedValue, bool) {
	if state == nil {
		return postmanScopedValue{}, false
	}
	for scope := int(postmanVariableScopeCount) - 1; scope >= int(postmanGlobalScope); scope-- {
		if value, ok := state.values[scope][name]; ok {
			return value, true
		}
	}
	return postmanScopedValue{}, false
}

// effectiveValues projects only concrete values. If the winning store contains
// a response-derived value, omitting the name is intentional: request template
// rewriting then emits {{var.<name>}} instead of incorrectly falling back to a
// stale concrete value from a broader store.
func (state *postmanScopedVariables) effectiveValues() (map[string]string, map[string]struct{}) {
	values := make(map[string]string)
	literals := make(map[string]struct{})
	if state == nil {
		return values, literals
	}
	for _, name := range state.names() {
		value, ok := state.effectiveValue(name)
		if !ok || value.response != nil {
			continue
		}
		values[name] = value.value
		if value.literal {
			if canonical := postmanVariableCanonicalName(name); canonical != "" {
				literals[canonical] = struct{}{}
			}
		}
	}
	return values, literals
}

func (state *postmanScopedVariables) names() []string {
	if state == nil {
		return nil
	}
	set := make(map[string]struct{})
	for scope := postmanGlobalScope; scope < postmanVariableScopeCount; scope++ {
		for name := range state.values[scope] {
			set[name] = struct{}{}
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// effectiveResponseExtractors returns only response writes made by the current
// request that remain effective after all script writes and any requested local
// store reset. It therefore implements both "last write in source order" and
// Postman's scope precedence without attaching shadowed extractors.
func (state *postmanScopedVariables) effectiveResponseExtractors(current map[*detectedExtractor]struct{}) []detectedExtractor {
	out := make([]detectedExtractor, 0)
	seen := make(map[string]struct{})
	for _, name := range state.names() {
		value, ok := state.effectiveValue(name)
		if !ok || value.response == nil {
			continue
		}
		if _, writtenNow := current[value.response]; !writtenNow {
			continue
		}
		canonical := postmanVariableCanonicalName(value.response.varName)
		if canonical == "" {
			continue
		}
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, *value.response)
	}
	return out
}

func unionPostmanVariableSets(sets ...map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{})
	for _, set := range sets {
		for name := range set {
			if canonical := postmanVariableCanonicalName(name); canonical != "" {
				result[canonical] = struct{}{}
			}
		}
	}
	return result
}

func applyPostmanScopedEventAssignmentsForListen(
	state *postmanScopedVariables,
	request requestItem,
	listen string,
	diagnostics *diag.Collector,
) map[*detectedExtractor]struct{} {
	writes := make(map[*detectedExtractor]struct{})
	location := postmanItemLocation(request)
	for _, event := range request.Events {
		if listen != "" && !strings.EqualFold(strings.TrimSpace(event.Listen), listen) {
			continue
		}
		for _, occurrence := range postmanAssignmentOccurrences(event) {
			if occurrence.literal {
				state.set(occurrence.scope, occurrence.name, postmanScopedValue{
					value:   occurrence.value,
					literal: true,
				})
				if diagnostics != nil {
					diagnostics.Add(diag.Info, "Postman script variable", location, "translated deterministic pm variable assignment for "+occurrence.name)
				}
				continue
			}
			if occurrence.extractor == nil {
				continue
			}
			state.set(occurrence.scope, occurrence.name, postmanScopedValue{response: occurrence.extractor})
			writes[occurrence.extractor] = struct{}{}
		}
	}
	return writes
}
