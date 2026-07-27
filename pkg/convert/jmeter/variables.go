package jmeter

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/credential"
)

const (
	maxJMeterResolvedVariableBytes      = 1 << 20
	maxJMeterResolvedVariableTotalBytes = 16 << 20
)

type jmeterVariableDefinition struct {
	Value  string
	Source credentialpolicy.SourceRef
}

// jmeterVariableScope is immutable after construction. Applying a nested
// Arguments element returns a new scope, which keeps controller-local
// shadowing from mutating an outer scope.
type jmeterVariableScope struct {
	definitions map[string]jmeterVariableDefinition
	review      *jmeterVariableReview
	resolution  *jmeterVariableResolution
}

type jmeterVariableResolution struct {
	values          map[string]string
	sourceVariables map[string]credentialpolicy.SourceVariable
	dependencies    map[string][]string
	invalid         map[string]struct{}
	limitExceeded   bool
}

type jmeterVariableReview struct {
	valuesByName  map[string]map[string]struct{}
	dependencies  map[string]map[string]struct{}
	invalidValues map[string]struct{}
	omitSource    bool
}

func newJMeterVariableReview() *jmeterVariableReview {
	return &jmeterVariableReview{
		valuesByName:  make(map[string]map[string]struct{}),
		dependencies:  make(map[string]map[string]struct{}),
		invalidValues: make(map[string]struct{}),
	}
}

func newJMeterVariableScope(review *jmeterVariableReview) *jmeterVariableScope {
	return &jmeterVariableScope{
		definitions: make(map[string]jmeterVariableDefinition),
		review:      review,
	}
}

func (scope *jmeterVariableScope) withDefinitions(
	values map[string]string,
	source credentialpolicy.SourceRef,
	diagnostics *diag.Collector,
) *jmeterVariableScope {
	if scope == nil {
		scope = newJMeterVariableScope(nil)
	}
	next := &jmeterVariableScope{
		definitions: make(map[string]jmeterVariableDefinition, len(scope.definitions)+len(values)),
		review:      scope.review,
	}
	for name, definition := range scope.definitions {
		next.definitions[name] = definition
	}
	names := sortedJMeterVariableKeys(values)
	for _, name := range names {
		value := values[name]
		if _, shadowed := next.definitions[name]; shadowed && diagnostics != nil {
			diagnostics.Add(
				diag.Info,
				"JMeter variable shadowing",
				jmeterVariableSourceLocation(source),
				fmt.Sprintf("variable %q overrides an outer Arguments definition", name),
			)
		}
		next.definitions[name] = jmeterVariableDefinition{Value: value, Source: source}
		if next.review != nil {
			next.review.recordValue(name, value)
		}
	}
	return next
}

func (scope *jmeterVariableScope) resolve(diagnostics *diag.Collector) *jmeterVariableResolution {
	if scope == nil {
		return &jmeterVariableResolution{}
	}
	if scope.resolution != nil {
		return scope.resolution
	}
	resolution := resolveJMeterVariableDefinitions(scope.definitions, diagnostics)
	scope.resolution = resolution
	if scope.review != nil {
		scope.review.recordResolution(scope.definitions, resolution)
	}
	return resolution
}

func resolveJMeterVariableDefinitions(
	definitions map[string]jmeterVariableDefinition,
	diagnostics *diag.Collector,
) *jmeterVariableResolution {
	resolution := &jmeterVariableResolution{
		values:          make(map[string]string),
		sourceVariables: make(map[string]credentialpolicy.SourceVariable),
		dependencies:    make(map[string][]string),
		invalid:         make(map[string]struct{}),
	}
	limits := requestimport.DefaultBindingDependencyLimits()
	if len(definitions) > limits.MaxNodes {
		resolution.limitExceeded = true
		for name := range definitions {
			resolution.invalid[name] = struct{}{}
		}
		addJMeterVariableLimitDiagnostic(diagnostics)
		return resolution
	}

	names := sortedJMeterDefinitionKeys(definitions)
	nodes := make([]requestimport.BindingNode, 0, len(names))
	remainingEdges := limits.MaxEdges
	for _, name := range names {
		if len(strings.TrimSpace(name)) > limits.MaxNameBytes {
			resolution.limitExceeded = true
			for candidate := range definitions {
				resolution.invalid[candidate] = struct{}{}
			}
			addJMeterVariableLimitDiagnostic(diagnostics)
			return resolution
		}
		dependencies, consumed, exceeded := jmeterVariableReferences(definitions[name].Value, remainingEdges)
		if exceeded {
			resolution.limitExceeded = true
			for candidate := range definitions {
				resolution.invalid[candidate] = struct{}{}
			}
			addJMeterVariableLimitDiagnostic(diagnostics)
			return resolution
		}
		remainingEdges -= consumed
		resolution.dependencies[name] = dependencies
		nodes = append(nodes, requestimport.BindingNode{Name: name, Dependencies: dependencies})
	}

	analysis := requestimport.AnalyzeBindingDependenciesWithLimits(nodes, limits)
	if analysis.LimitExceeded {
		resolution.limitExceeded = true
		for name := range definitions {
			resolution.invalid[name] = struct{}{}
		}
		addJMeterVariableLimitDiagnostic(diagnostics)
		return resolution
	}
	for _, name := range analysis.InvalidBindings {
		resolution.invalid[name] = struct{}{}
	}
	addJMeterVariableGraphDiagnostics(diagnostics, analysis)

	reverse := make(map[string][]string, len(names))
	indegree := make(map[string]int, len(names))
	for _, name := range names {
		reverse[name] = nil
	}
	for _, name := range names {
		if _, invalid := resolution.invalid[name]; invalid {
			continue
		}
		for _, dependency := range resolution.dependencies[name] {
			if _, exists := definitions[dependency]; !exists {
				continue
			}
			reverse[dependency] = append(reverse[dependency], name)
			indegree[name]++
		}
	}
	for name := range reverse {
		sort.Strings(reverse[name])
	}
	ready := make([]string, 0, len(names))
	for _, name := range names {
		if _, invalid := resolution.invalid[name]; !invalid && indegree[name] == 0 {
			ready = append(ready, name)
		}
	}

	totalBytes := 0
	expansionFailed := false
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		value, ok := expandJMeterVariableDefinition(
			definitions[name].Value,
			resolution.values,
			maxJMeterResolvedVariableBytes,
		)
		if !ok || len(value) > maxJMeterResolvedVariableTotalBytes-totalBytes {
			resolution.invalid[name] = struct{}{}
			expansionFailed = true
		} else {
			value = convertVariables(value)
			if len(value) > maxJMeterResolvedVariableBytes ||
				len(value) > maxJMeterResolvedVariableTotalBytes-totalBytes {
				resolution.invalid[name] = struct{}{}
				expansionFailed = true
			} else {
				resolution.values[name] = value
				resolution.sourceVariables[name] = credentialpolicy.SourceVariable{
					Value:  value,
					Source: definitions[name].Source,
				}
				totalBytes += len(value)
			}
		}
		for _, dependent := range reverse[name] {
			if _, invalid := resolution.invalid[name]; invalid {
				resolution.invalid[dependent] = struct{}{}
			}
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}

	// A value-expansion failure invalidates its complete dependent closure.
	if expansionFailed {
		queue := sortedJMeterVariableSet(resolution.invalid)
		for index := 0; index < len(queue); index++ {
			for _, dependent := range reverse[queue[index]] {
				if _, invalid := resolution.invalid[dependent]; invalid {
					continue
				}
				resolution.invalid[dependent] = struct{}{}
				delete(resolution.values, dependent)
				delete(resolution.sourceVariables, dependent)
				queue = append(queue, dependent)
			}
		}
		if diagnostics != nil {
			diagnostics.Add(
				diag.Warning,
				"JMeter variable expansion",
				"Arguments",
				"one or more source variable values exceeded the bounded expansion limit",
			)
		}
	}
	return resolution
}

func addJMeterVariableLimitDiagnostic(diagnostics *diag.Collector) {
	if diagnostics == nil {
		return
	}
	diagnostics.Add(
		diag.Warning,
		"JMeter variable dependency limit",
		"Arguments",
		"the source variable graph exceeded bounded analysis limits and was left unresolved for review",
	)
}

func addJMeterVariableGraphDiagnostics(
	diagnostics *diag.Collector,
	analysis requestimport.BindingDependencyAnalysis,
) {
	if diagnostics == nil {
		return
	}
	if len(analysis.MissingByBinding) > 0 {
		missing := make(map[string]struct{})
		for _, dependencies := range analysis.MissingByBinding {
			for _, dependency := range dependencies {
				missing[dependency] = struct{}{}
			}
		}
		diagnostics.Add(
			diag.Warning,
			"JMeter variable dependency",
			"Arguments",
			fmt.Sprintf(
				"%d referenced source variable binding(s) are missing: %s",
				len(missing),
				strings.Join(sortedJMeterVariableSet(missing), ", "),
			),
		)
	}
	for _, cycle := range analysis.Cycles {
		diagnostics.Add(
			diag.Warning,
			"JMeter variable cycle",
			"Arguments",
			"cyclic source variable bindings require review: "+strings.Join(cycle, " -> "),
		)
	}
}

func jmeterVariableReferences(value string, remaining int) ([]string, int, bool) {
	set := make(map[string]struct{})
	consumed := 0
	for _, match := range jmeterVarPattern.FindAllStringSubmatch(value, -1) {
		consumed++
		if consumed > remaining {
			return nil, consumed, true
		}
		if len(match) == 2 && !strings.HasPrefix(match[1], "__") {
			set[match[1]] = struct{}{}
		}
	}
	return sortedJMeterVariableSet(set), consumed, false
}

func expandJMeterVariableDefinition(
	value string,
	resolved map[string]string,
	maxBytes int,
) (string, bool) {
	var output strings.Builder
	last := 0
	for _, indexes := range jmeterVarPattern.FindAllStringSubmatchIndex(value, -1) {
		if len(indexes) != 4 {
			continue
		}
		name := value[indexes[2]:indexes[3]]
		if strings.HasPrefix(name, "__") {
			replacement := value[indexes[0]:indexes[1]]
			if output.Len()+indexes[0]-last+len(replacement) > maxBytes {
				return "", false
			}
			output.WriteString(value[last:indexes[0]])
			output.WriteString(replacement)
			last = indexes[1]
			continue
		}
		replacement, ok := resolved[name]
		if !ok {
			return "", false
		}
		if output.Len()+indexes[0]-last+len(replacement) > maxBytes {
			return "", false
		}
		output.WriteString(value[last:indexes[0]])
		output.WriteString(replacement)
		last = indexes[1]
	}
	if output.Len()+len(value)-last > maxBytes {
		return "", false
	}
	output.WriteString(value[last:])
	result := convertVariables(output.String())
	// Accept already-translated source values as well as native ${name}
	// references. Native JMX normally uses only the latter.
	result = jmeterRuntimeVariablePattern.ReplaceAllStringFunc(result, func(token string) string {
		match := jmeterRuntimeVariablePattern.FindStringSubmatch(token)
		if len(match) != 2 {
			return token
		}
		replacement, exists := resolved[match[1]]
		if !exists {
			return token
		}
		return replacement
	})
	if len(result) > maxBytes {
		return "", false
	}
	return result, true
}

func (resolution *jmeterVariableResolution) expandRuntimeValue(
	value string,
	proven map[string]struct{},
) (string, []string) {
	if resolution == nil || len(resolution.values) == 0 || !strings.Contains(value, "{{") {
		return value, nil
	}
	used := make(map[string]struct{})
	expanded := jmeterRuntimeVariablePattern.ReplaceAllStringFunc(value, func(token string) string {
		match := jmeterRuntimeVariablePattern.FindStringSubmatch(token)
		if len(match) != 2 {
			return token
		}
		name := match[1]
		if _, dynamic := proven[name]; dynamic {
			return token
		}
		replacement, exists := resolution.values[name]
		if !exists {
			return token
		}
		used[name] = struct{}{}
		return replacement
	})
	return expanded, sortedJMeterVariableSet(used)
}

func (resolution *jmeterVariableResolution) dependencyClosure(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	queue := append([]string(nil), names...)
	sort.Strings(queue)
	for index := 0; index < len(queue); index++ {
		name := queue[index]
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		for _, dependency := range resolution.dependencies[name] {
			if _, exists := seen[dependency]; !exists {
				queue = append(queue, dependency)
			}
		}
	}
	return sortedJMeterVariableSet(seen)
}

type jmeterVariableApplication struct {
	target  []string
	headers map[int][]string
	body    []string
	auth    map[string][]string
}

func (application *jmeterVariableApplication) merge(other jmeterVariableApplication) {
	if application == nil {
		return
	}
	application.target = uniqueSortedJMeterNames(append(application.target, other.target...))
	application.body = uniqueSortedJMeterNames(append(application.body, other.body...))
	if application.headers == nil {
		application.headers = make(map[int][]string)
	}
	for index, names := range other.headers {
		application.headers[index] = uniqueSortedJMeterNames(append(application.headers[index], names...))
	}
	if application.auth == nil {
		application.auth = make(map[string][]string)
	}
	for field, names := range other.auth {
		application.auth[field] = uniqueSortedJMeterNames(append(application.auth[field], names...))
	}
}

func applyJMeterVariablesToEffectiveRequest(
	request *requestimport.EffectiveRequest,
	resolution *jmeterVariableResolution,
	proven map[string]struct{},
) jmeterVariableApplication {
	application := jmeterVariableApplication{
		headers: make(map[int][]string),
		auth:    make(map[string][]string),
	}
	if request == nil || resolution == nil {
		return application
	}
	request.Target, application.target = resolution.expandRuntimeValue(request.Target, proven)
	for index := range request.Headers {
		request.Headers[index].Value, application.headers[index] =
			resolution.expandRuntimeValue(request.Headers[index].Value, proven)
	}
	body := string(request.Body.Payload)
	body, application.body = resolution.expandRuntimeValue(body, proven)
	request.Body.Payload = []byte(body)
	request.Auth.Username, application.auth["auth.username"] =
		resolution.expandRuntimeValue(request.Auth.Username, proven)
	request.Auth.Password, application.auth["auth.password"] =
		resolution.expandRuntimeValue(request.Auth.Password, proven)
	request.Auth.Token, application.auth["auth.token"] =
		resolution.expandRuntimeValue(request.Auth.Token, proven)
	request.Auth.Domain, application.auth["auth.domain"] =
		resolution.expandRuntimeValue(request.Auth.Domain, proven)
	return application
}

func attachJMeterVariableRequirementProvenance(
	requirements []credentialpolicy.Requirement,
	application jmeterVariableApplication,
	resolution *jmeterVariableResolution,
) {
	if resolution == nil {
		return
	}
	for index := range requirements {
		names := append([]string(nil), requirements[index].SourceVariableNames...)
		for _, target := range requirements[index].Targets {
			switch {
			case strings.HasPrefix(target.Field, "url."):
				names = append(names, application.target...)
			case target.Field == "header_list":
				names = append(names, application.headers[target.Index]...)
			case strings.HasPrefix(target.Field, "auth."):
				names = append(names, application.auth[target.Field]...)
			default:
				names = append(names, application.body...)
			}
		}
		requirements[index].SourceVariableNames = resolution.dependencyClosure(names)
	}
}

func (review *jmeterVariableReview) recordValue(name, value string) {
	if review == nil || strings.TrimSpace(name) == "" || value == "" {
		return
	}
	if review.valuesByName[name] == nil {
		review.valuesByName[name] = make(map[string]struct{})
	}
	review.valuesByName[name][value] = struct{}{}
}

func (review *jmeterVariableReview) recordResolution(
	definitions map[string]jmeterVariableDefinition,
	resolution *jmeterVariableResolution,
) {
	if review == nil || resolution == nil {
		return
	}
	if resolution.limitExceeded {
		review.omitSource = true
	}
	for name, value := range resolution.values {
		review.recordValue(name, value)
	}
	for name, dependencies := range resolution.dependencies {
		if review.dependencies[name] == nil {
			review.dependencies[name] = make(map[string]struct{})
		}
		for _, dependency := range dependencies {
			review.dependencies[name][dependency] = struct{}{}
		}
	}
	for name := range resolution.invalid {
		if value := definitions[name].Value; value != "" {
			review.invalidValues[value] = struct{}{}
		}
	}
}

func redactJMeterVariableValue(content, value string) string {
	if value == "" {
		return content
	}
	escaped := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	).Replace(value)
	content = strings.ReplaceAll(content, ">"+escaped+"<", ">"+credential.Redacted+"<")
	// Existing JMeter source redaction is value-based. Retain that behavior for
	// nontrivial values so composed variable credentials are also removed when
	// whitespace or a nonstandard JMX property wrapper is present.
	if len(value) >= 8 {
		content = strings.ReplaceAll(content, value, credential.Redacted)
		content = strings.ReplaceAll(content, escaped, credential.Redacted)
	}
	return content
}

func sortedJMeterVariableKeys(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for name := range values {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func sortedJMeterDefinitionKeys(values map[string]jmeterVariableDefinition) []string {
	out := make([]string, 0, len(values))
	for name := range values {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func sortedJMeterVariableSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func uniqueSortedJMeterNames(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	return sortedJMeterVariableSet(set)
}

func sortedJMeterValuesByLength(values map[string]struct{}) []string {
	out := sortedJMeterVariableSet(values)
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i]) == len(out[j]) {
			return out[i] < out[j]
		}
		return len(out[i]) > len(out[j])
	})
	return out
}

func jmeterVariableSourceLocation(source credentialpolicy.SourceRef) string {
	location := strings.TrimSpace(source.Path)
	if pointer := strings.TrimSpace(source.Pointer); pointer != "" {
		if location != "" {
			location += "#"
		}
		location += pointer
	}
	if location == "" {
		return "Arguments"
	}
	return location
}
