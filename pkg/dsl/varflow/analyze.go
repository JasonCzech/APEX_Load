package varflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol/messaging"
)

// Template namespaces readiness checks. A {{var.*}} reference resolves from
// runtime variables and falls back to script parameters; a {{param.*}}
// reference resolves only from script parameters (resolveTemplateValue,
// pkg/engine/vuser_resolve.go).
const (
	NamespaceVar   = "var"
	NamespaceParam = "param"
)

var referencePattern = regexp.MustCompile(`\{\{\s*(var|param)\.([a-zA-Z0-9_.-]+)`)

// Reference is one {{var.*}} or {{param.*}} template reference.
type Reference struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// References returns the var and param references in text, in order of first
// appearance and without duplicates.
func References(text string) []Reference {
	var refs []Reference
	seen := make(map[Reference]struct{})
	for _, match := range referencePattern.FindAllStringSubmatch(text, -1) {
		if len(match) != 3 {
			continue
		}
		ref := Reference{Namespace: match[1], Name: strings.TrimSpace(match[2])}
		if ref.Name == "" {
			continue
		}
		if _, exists := seen[ref]; exists {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	return refs
}

// ActionReferences returns the references one leaf action reads at runtime,
// sorted by name then namespace. Protocol configuration references are not
// included: they are not interpolated per action.
func ActionReferences(action dsl.Action) []Reference {
	return actionReferences(action, StepOutputs(action))
}

func actionReferences(action dsl.Action, outputs Outputs) []Reference {
	if outputs.NoReads {
		return nil
	}
	set := make(map[Reference]struct{})
	collect := func(text string) {
		for _, ref := range References(text) {
			set[ref] = struct{}{}
		}
	}
	if outputs.Known {
		for _, text := range outputs.ReadTexts {
			collect(text)
		}
	} else {
		// resolveAction (pkg/engine/vuser_resolve.go) interpolates metadata
		// other than import evidence, but most steps use it only to annotate
		// results, so it is evidence apart from the two uses below.
		metadata := action.Metadata
		action.Metadata = nil
		payload, err := json.Marshal(action)
		if err != nil {
			return nil
		}
		collect(string(payload))
		if decoded, encoded, decodeErr := dsl.DecodeBodyEncoding(action.Body, action.BodyEncoding, 0); encoded && decodeErr == nil {
			// body_encoding describes storage, not the bytes delivered on the
			// wire. Decode before template analysis so an encoded reference is
			// governed by the same execution-order rules as a text body.
			collect(string(decoded))
		}
		for _, text := range sentMetadataTexts(action.Type, metadata) {
			collect(text)
		}
		if len(metadata) > 0 && executesThroughProtocol(action.Type) {
			// safeProtoExecuteStream (pkg/engine/vuser_execute.go) refuses a
			// protocol action with an unresolved {{param.*}} anywhere in it,
			// metadata included, so a parameter use there is a read. Import
			// evidence is the exception: resolveAction keeps it as recorded
			// and the refusal skips it, so a guided repair that rebinds the
			// executable fields clears the use.
			refused := make(map[string]any, len(metadata))
			for key, value := range metadata {
				if !messaging.MetadataKeyIsEvidence(key) {
					refused[key] = value
				}
			}
			if payload, err := json.Marshal(refused); err == nil {
				for _, ref := range References(string(payload)) {
					if ref.Namespace == NamespaceParam {
						set[ref] = struct{}{}
					}
				}
			}
		}
	}
	refs := make([]Reference, 0, len(set))
	for ref := range set {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Name != refs[j].Name {
			return refs[i].Name < refs[j].Name
		}
		return refs[i].Namespace < refs[j].Namespace
	})
	return refs
}

// MetadataReads reports which namespaces replay reads in the metadata entry
// key of a leaf action, by the rule ActionReferences applies: every reference
// in a string a Kafka or JMS send delivers, and only a {{param.*}} in another
// entry of a protocol action, which replay refuses while it is unresolved.
// Import evidence, and the metadata of a step whose reads are Known or
// NoReads, are never read. Guided repairs use it to rebind a use in exactly
// the metadata replay reads and leave every other entry as recorded.
func MetadataReads(action dsl.Action, key string) (vars, params bool) {
	if outputs := StepOutputs(action); outputs.Known || outputs.NoReads {
		return false, false
	}
	value, present := action.Metadata[key]
	if !present {
		return false, false
	}
	if len(sentMetadataTexts(action.Type, map[string]any{key: value})) > 0 {
		return true, true
	}
	return false, executesThroughProtocol(action.Type) && !messaging.MetadataKeyIsEvidence(key)
}

// sentMetadataTexts returns the metadata values a messaging send step
// delivers. kafka.buildRecord copies each string entry whose key is not
// import provenance into the record headers of kafka_produce and
// kafka_request_reply, and jms.buildMessage copies them into the message
// properties of jms_publish and jms_request_reply. Other values are not sent.
// Provenance includes every import evidence key, so evidence is never sent.
func sentMetadataTexts(actionType string, metadata map[string]any) []string {
	var family string
	switch strings.ToLower(actionType) {
	case "kafka_produce", "kafka_request_reply":
		family = messaging.FamilyKafka
	case "jms_publish", "jms_request_reply":
		family = messaging.FamilyJMS
	default:
		return nil
	}
	var texts []string
	for key, value := range metadata {
		if text, ok := value.(string); ok && !messaging.MetadataKeyIsProvenance(family, key) {
			texts = append(texts, text)
		}
	}
	return texts
}

// executesThroughProtocol reports whether the runtime hands an action to its
// protocol adapter. Engine steps (handleEngineAction: the catalog's "*" specs
// and the HTTP OAuth token step) and control blocks never reach it, and an
// http_sequence wrapper only annotates its aggregate result with its metadata;
// its children are protocol actions of their own.
func executesThroughProtocol(actionType string) bool {
	if strings.EqualFold(strings.TrimSpace(actionType), "http_sequence") {
		return false
	}
	canonical := dsl.CanonicalActionType(actionType)
	if canonical == "http_oauth2_token" {
		return false
	}
	spec, ok := dsl.SpecForActionType(canonical)
	return !ok || !spec.EngineAction()
}

// Result is the source-neutral outcome of analyzing references in execution
// order. ByAction uses stable DSL paths and Missing is their sorted union.
//
// Blocking is the subset of Missing that replay cannot carry as placeholder
// text. Like LoadRunner, which uses an undefined {param} literally and warns,
// HTTP replay keeps an unresolved {{var.*}} reference verbatim, and the
// request that sends it warns (unresolved_variable_sent). SOAP and Java HTTP
// requests run through the same HTTP adapter and keep placeholders too,
// except in a Java HTTP request body, which is decoded as base64 before it is
// sent (see keepsVariablePlaceholders). Protocol configuration is never
// interpolated, a JSON helper fails its step on an unresolved buffer, an
// undeclared {{param.*}} always blocks (see recordOutputs), and other
// protocols have no placeholder fallback.
//
// LateProduced lists the missing {{var.*}} names that some step of the script
// does write: the value exists, but not on every path before its use. A write
// inside a control_parallel branch counts only for uses in that branch or a
// branch nested in it: after the join the value exists only as
// parallel.<branch>.<name>, so moving the producer cannot supply the use.
//
// ParamBlocking and ConfigBlocking say why a Blocking name blocks where no
// variable producer can help: an undeclared {{param.*}} use, which only a
// declared script parameter satisfies, and a protocol configuration
// reference, which no step can supply. A name may be in both, and also be
// late produced through an unrelated {{var.*}} use.
//
// Misses is the per-reference detail behind ByAction, in walk order. Import
// review needs it to tell a {{var.*}} miss from a {{param.*}} miss of the same
// name at one action, and to gate only the use that cannot be carried as
// placeholder text. It is not part of the readiness JSON contract.
type Result struct {
	Missing        []string            `json:"missing,omitempty"`
	Blocking       []string            `json:"blocking,omitempty"`
	ByAction       map[string][]string `json:"by_action,omitempty"`
	LateProduced   []string            `json:"late_produced,omitempty"`
	ParamBlocking  []string            `json:"param_blocking,omitempty"`
	ConfigBlocking []string            `json:"config_blocking,omitempty"`
	Misses         []Miss              `json:"-"`
}

// Miss is one reference without a value where it executes. Blocking reports
// that replay cannot carry this use as placeholder text (see Result.Blocking).
// Config marks a protocol configuration reference charged to the action that
// uses that configuration; no step can produce it. Base64Body marks a
// {{var.*}} use that blocks only because it is in the stored body of a Java
// HTTP request, which replay decodes as base64 (see base64BodyVariables).
type Miss struct {
	Path       string
	Reference  Reference
	Blocking   bool
	Config     bool
	Base64Body bool
}

// Analyze finds references that are not supplied by explicit replay seeds,
// script parameters, or an output that executes earlier on the same path.
// Save-all extractors and SelectAll JSON values publish name_N and
// name_count, not name.
func Analyze(script *dsl.Script, seeded map[string]string) Result {
	if script == nil {
		return Result{}
	}
	walker := &analyzer{
		config:       scriptConfigReferences(script),
		byAction:     make(map[string][]string),
		blocking:     make(map[string]struct{}),
		scopeParents: make(map[string]string),
		unproven:     make(map[string]map[string]struct{}),
		written:      make(map[string]*state),
	}
	state := newState(script, seeded)
	walker.walk(script.Init, "init", state)
	walker.walk(script.Actions, "actions", state)
	walker.walk(script.Teardown, "teardown", state)

	var result Result
	union := make(map[string]struct{})
	for path, names := range walker.byAction {
		names = uniqueSortedStrings(names)
		if len(names) == 0 {
			delete(walker.byAction, path)
			continue
		}
		walker.byAction[path] = names
		for _, name := range names {
			union[name] = struct{}{}
		}
	}
	late := make(map[string]struct{})
	for name, scopes := range walker.unproven {
		for scope := range scopes {
			if walker.writtenWhereVisible(name, scope) {
				late[name] = struct{}{}
				break
			}
		}
	}
	paramBlocking, configBlocking := make(map[string]struct{}), make(map[string]struct{})
	for _, miss := range walker.misses {
		switch {
		case miss.Config:
			configBlocking[miss.Reference.Name] = struct{}{}
		case miss.Reference.Namespace == NamespaceParam:
			paramBlocking[miss.Reference.Name] = struct{}{}
		}
	}
	result.Missing = sortedStringSet(union)
	result.Blocking = sortedStringSet(walker.blocking)
	result.LateProduced = sortedStringSet(late)
	result.ParamBlocking = sortedStringSet(paramBlocking)
	result.ConfigBlocking = sortedStringSet(configBlocking)
	if len(walker.byAction) > 0 {
		result.ByAction = walker.byAction
	}
	result.Misses = walker.misses
	return result
}

type analyzer struct {
	config   configReferences
	byAction map[string][]string
	misses   []Miss
	blocking map[string]struct{}
	// scope is the path of the control_parallel branch being walked, "" outside
	// any branch. A branch runs on its own copy of the variables, so what it
	// writes is visible only within it and the branches nested in it.
	scope string
	// scopeParents maps each branch scope to the scope that encloses it.
	scopeParents map[string]string
	// unproven maps every {{var.*}} name that lacked a value at some use to
	// the scopes of those uses.
	unproven map[string]map[string]struct{}
	// written accumulates, per scope, every output any step publishes on any
	// path, so LateProduced can tell "produced too late" from "never produced".
	written map[string]*state
}

// writtenIn returns the outputs published so far in scope.
func (a *analyzer) writtenIn(scope string) *state {
	written, ok := a.written[scope]
	if !ok {
		written = newWrittenState()
		a.written[scope] = written
	}
	return written
}

// writtenWhereVisible reports whether some step writes name where a use in
// scope could see it: in that scope or in one that encloses it.
func (a *analyzer) writtenWhereVisible(name, scope string) bool {
	for {
		if written := a.written[scope]; written != nil && written.satisfiedVar(name) {
			return true
		}
		if scope == "" {
			return false
		}
		scope = a.scopeParents[scope]
	}
}

type state struct {
	available      map[string]struct{}
	saveAll        map[string]struct{}
	parameterNames map[string]struct{}
	rowPrefixes    map[string]struct{}
	dataSources    map[string]struct{}
	// resultPrefixes are live_table and native_javascript result prefixes:
	// the step publishes <prefix>.status, <prefix>.values.<column> and so on.
	// Unlike a for-each row prefix they outlive the block that sets them.
	resultPrefixes map[string]struct{}
	// extracted are the available names the runtime merged as extractions
	// (mergeExtractions, pkg/engine/vuser_resolve.go): extractor outputs, a
	// browser evaluate result and a parallel join's own publications. Only
	// these leave a control_parallel branch. Source steps and the OAuth token
	// step write variables directly, so the join does not publish them.
	extracted map[string]struct{}
}

type configReferences struct {
	baseURL       []string
	httpTransport []string
	httpRequest   []string
	soap          []string
	javaHTTP      []string
	protocol      string
}

func newState(script *dsl.Script, seeded map[string]string) *state {
	s := &state{
		available:      make(map[string]struct{}, len(seeded)),
		saveAll:        make(map[string]struct{}),
		parameterNames: make(map[string]struct{}, len(script.Parameters)),
		rowPrefixes:    make(map[string]struct{}),
		dataSources:    make(map[string]struct{}),
		resultPrefixes: make(map[string]struct{}),
		extracted:      make(map[string]struct{}),
	}
	for _, source := range script.DataSources {
		s.dataSources[source.Name] = struct{}{}
	}
	for name := range seeded {
		if name = strings.TrimSpace(name); name != "" {
			s.available[name] = struct{}{}
		}
	}
	for _, parameter := range script.Parameters {
		if name := strings.TrimSpace(parameter.Name); name != "" {
			s.parameterNames[strings.ToLower(name)] = struct{}{}
		}
	}
	return s
}

func newWrittenState() *state {
	return &state{
		available:      make(map[string]struct{}),
		saveAll:        make(map[string]struct{}),
		resultPrefixes: make(map[string]struct{}),
	}
}

func (s *state) clone() *state {
	cloned := &state{
		available:      make(map[string]struct{}, len(s.available)),
		saveAll:        make(map[string]struct{}, len(s.saveAll)),
		parameterNames: s.parameterNames,
		rowPrefixes:    make(map[string]struct{}, len(s.rowPrefixes)),
		dataSources:    s.dataSources,
		resultPrefixes: make(map[string]struct{}, len(s.resultPrefixes)),
		extracted:      make(map[string]struct{}, len(s.extracted)),
	}
	for name := range s.extracted {
		cloned.extracted[name] = struct{}{}
	}
	for prefix := range s.rowPrefixes {
		cloned.rowPrefixes[prefix] = struct{}{}
	}
	for prefix := range s.resultPrefixes {
		cloned.resultPrefixes[prefix] = struct{}{}
	}
	for name := range s.available {
		cloned.available[name] = struct{}{}
	}
	for name := range s.saveAll {
		cloned.saveAll[name] = struct{}{}
	}
	return cloned
}

func (s *state) satisfied(ref Reference) bool {
	if ref.Namespace == NamespaceParam {
		_, ok := s.parameterNames[strings.ToLower(strings.TrimSpace(ref.Name))]
		return ok
	}
	return s.satisfiedVar(ref.Name)
}

// satisfiedVar applies the runtime's lookup: exact variable names, parameters
// in any case, row and result prefixes, and name_N / name_count array slots.
func (s *state) satisfiedVar(name string) bool {
	for prefix := range s.rowPrefixes {
		if strings.HasPrefix(name, prefix+".") {
			return true
		}
	}
	for prefix := range s.resultPrefixes {
		if strings.HasPrefix(name, prefix+".") {
			return true
		}
	}
	if _, ok := s.available[name]; ok {
		return true
	}
	if _, ok := s.parameterNames[strings.ToLower(strings.TrimSpace(name))]; ok {
		return true
	}
	for prefix := range s.saveAll {
		if name == prefix+"_count" {
			return true
		}
		if ordinal, ok := strings.CutPrefix(name, prefix+"_"); ok &&
			ordinal != "" && strings.Trim(ordinal, "0123456789") == "" {
			return true
		}
	}
	return false
}

// register publishes extractor outputs. Extractor names are exact
// (mergeExtractions, pkg/engine/vuser_resolve.go); mode=all publishes
// name_N and name_count instead of name.
func (a *analyzer) register(s *state, extractors []dsl.Extractor) {
	written := a.writtenIn(a.scope)
	for _, extractor := range extractors {
		name := strings.TrimSpace(extractor.Name)
		if name == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(extractor.Mode), "all") {
			s.saveAll[name] = struct{}{}
			delete(s.available, name)
			written.saveAll[name] = struct{}{}
			continue
		}
		s.available[name] = struct{}{}
		s.extracted[name] = struct{}{}
		delete(s.saveAll, name)
		written.available[name] = struct{}{}
	}
}

// apply publishes a step's own outputs after its reads and extractors.
// Removals delete the exact and lowercase names. A scalar write leaves an
// earlier array alone and an array write leaves an earlier scalar alone, as
// the runtime does. extracted marks writes the runtime merges as extractions.
func (a *analyzer) apply(s *state, outputs Outputs, extracted bool) {
	written := a.writtenIn(a.scope)
	for _, name := range outputs.Removes {
		delete(s.available, name)
		delete(s.available, strings.ToLower(name))
	}
	for _, write := range outputs.Writes {
		name := strings.TrimSpace(write.Name)
		if name == "" {
			continue
		}
		names := []string{name}
		if write.Lower {
			names = append(names, strings.ToLower(name))
		}
		for _, alias := range names {
			if write.Array {
				s.saveAll[alias] = struct{}{}
				written.saveAll[alias] = struct{}{}
			} else {
				s.available[alias] = struct{}{}
				written.available[alias] = struct{}{}
				if extracted {
					s.extracted[alias] = struct{}{}
				}
			}
		}
	}
	for _, prefix := range outputs.Prefixes {
		if prefix = strings.TrimSpace(prefix); prefix != "" {
			s.resultPrefixes[prefix] = struct{}{}
			written.resultPrefixes[prefix] = struct{}{}
		}
	}
}

// writesExtractions reports whether the runtime merges a step's own writes
// as extractions. A browser evaluate result arrives in the protocol result
// (vuser_execute.go); native_javascript and live_table merge only their
// result prefixes, which the join publishes on their own.
func writesExtractions(action dsl.Action) bool {
	return dsl.CanonicalActionType(action.Type) == "browser_evaluate_js"
}

func (a *analyzer) walk(actions []dsl.Action, prefix string, s *state) {
	for index := range actions {
		path := fmt.Sprintf("%s[%d]", prefix, index)
		action := &actions[index]
		switch strings.ToLower(strings.TrimSpace(action.Type)) {
		case "control_if":
			shell := *action
			shell.Control = cloneControlShell(action.Control)
			shell.Extractors = nil
			a.record(shell, path, s, configReferences{protocol: a.config.protocol})
			if action.Control == nil {
				continue
			}
			thenState := s.clone()
			a.walk(action.Control.Steps, path+".control.steps", thenState)
			if len(action.Control.Else) == 0 {
				// The branch may not run, so only values available before the
				// branch remain guaranteed afterward.
				continue
			}
			elseState := s.clone()
			a.walk(action.Control.Else, path+".control.else", elseState)
			intersectStates(s, thenState, elseState)
		case "control_switch":
			shell := *action
			shell.Control = cloneControlShell(action.Control)
			shell.Extractors = nil
			a.record(shell, path, s, configReferences{protocol: a.config.protocol})
			if action.Control == nil {
				continue
			}
			branchStates := make([]*state, 0, len(action.Control.Cases)+1)
			for caseIndex, switchCase := range action.Control.Cases {
				caseState := s.clone()
				a.walk(switchCase.Steps, fmt.Sprintf("%s.control.cases[%d].steps", path, caseIndex), caseState)
				branchStates = append(branchStates, caseState)
			}
			if len(action.Control.Default) > 0 {
				defaultState := s.clone()
				a.walk(action.Control.Default, path+".control.default", defaultState)
				branchStates = append(branchStates, defaultState)
			} else {
				// No case is guaranteed to match.
				branchStates = append(branchStates, s.clone())
			}
			merged := branchStates[0]
			for _, branchState := range branchStates[1:] {
				intersectStates(merged, merged, branchState)
			}
			replaceState(s, merged)
		case "control_loop":
			bodyState := s.clone()
			bodyState.available["loop_index"] = struct{}{}
			if action.Control != nil && action.Control.ForEach != nil {
				forEach := action.Control.ForEach
				if _, exists := s.dataSources[strings.TrimSpace(forEach.Source)]; exists && strings.TrimSpace(forEach.Var) != "" {
					bodyState.rowPrefixes[strings.TrimSpace(forEach.Var)] = struct{}{}
				}
			}
			shell := *action
			shell.Control = cloneControlShell(action.Control)
			shell.Extractors = nil
			// A post-test loop first reads its condition after the body has run
			// once.
			postTest := postTestLoop(action.Control)
			if !postTest {
				a.record(shell, path, bodyState, configReferences{protocol: a.config.protocol})
			}
			if action.Control == nil {
				continue
			}
			a.walk(action.Control.Steps, path+".control.steps", bodyState)
			if postTest {
				a.record(shell, path, bodyState, configReferences{protocol: a.config.protocol})
			}
			if loopRunsAtLeastOnce(action.Control) {
				// Runtime restores loop locals on exit, including nested loops.
				if _, existed := s.available["loop_index"]; !existed {
					delete(bodyState.available, "loop_index")
				}
				replaceState(s, bodyState)
			}
		case "control_parallel":
			if action.Control == nil {
				continue
			}
			parallelSeed := s.clone()
			enclosing := a.scope
			written := a.writtenIn(enclosing)
			for branchIndex, branch := range action.Control.Branches {
				// Each branch runs on a copy of the variables with an empty
				// extraction ledger, and the join publishes, as
				// parallel.<branch>.<name>, only the ledger's names that are
				// still set (control_parallel.go, replay.go).
				branchPath := fmt.Sprintf("%s.control.branches[%d]", path, branchIndex)
				branchState := parallelSeed.clone()
				branchState.extracted = make(map[string]struct{})
				a.scopeParents[branchPath] = enclosing
				a.scope = branchPath
				a.walk(branch.Steps, branchPath+".steps", branchState)
				a.scope = enclosing
				for name := range branchState.extracted {
					if _, set := branchState.available[name]; !set {
						continue
					}
					published := "parallel." + branch.Name + "." + name
					s.available[published] = struct{}{}
					s.extracted[published] = struct{}{}
					written.available[published] = struct{}{}
				}
				for prefix := range branchState.resultPrefixes {
					if _, seeded := parallelSeed.resultPrefixes[prefix]; !seeded {
						published := "parallel." + branch.Name + "." + prefix
						s.resultPrefixes[published] = struct{}{}
						written.resultPrefixes[published] = struct{}{}
					}
				}
			}
		case "http_sequence":
			// executeHTTPSequenceAction (pkg/engine/vuser_execute.go) resolves
			// each child as a request of its own, and replay runs only the
			// children. The wrapper itself sends nothing: its target, method,
			// headers, body, value and params are never resolved (an LR
			// transaction group copies its first request's target there), and
			// its extractors never execute.
			a.recordOutputs(*action, sequenceWrapperOutputs(*action), path, s, configReferences{protocol: a.config.protocol})
			children := sequenceChildActions(action.Params)
			for childIndex := range children {
				childPath := fmt.Sprintf("%s.params.requests[%d]", path, childIndex)
				a.record(children[childIndex], childPath, s, a.config)
				a.register(s, children[childIndex].Extractors)
			}
		default:
			outputs := StepOutputs(*action)
			a.recordOutputs(*action, outputs, path, s, a.config)
			a.register(s, action.Extractors)
			a.apply(s, outputs, writesExtractions(*action))
		}
	}
}

func (a *analyzer) record(action dsl.Action, path string, s *state, config configReferences) {
	a.recordOutputs(action, StepOutputs(action), path, s, config)
}

func (a *analyzer) recordOutputs(action dsl.Action, outputs Outputs, path string, s *state, config configReferences) {
	if outputs.NoReads {
		return
	}
	placeholder := keepsVariablePlaceholders(action, config.protocol)
	base64Body := base64BodyVariables(action, config.protocol)
	for _, ref := range actionReferences(action, outputs) {
		if s.satisfied(ref) {
			continue
		}
		a.byAction[path] = append(a.byAction[path], ref.Name)
		if ref.Namespace == NamespaceParam {
			// An undeclared {{param.*}} always blocks, by product decision. The
			// runtime refuses a protocol action that carries one
			// (safeProtoExecuteStream, pkg/engine/vuser_execute.go); engine
			// steps and control conditions would run with the literal
			// placeholder, which is never the intended value, so the preflight
			// refuses those too.
			a.blocking[ref.Name] = struct{}{}
			a.misses = append(a.misses, Miss{Path: path, Reference: ref, Blocking: true})
			continue
		}
		if a.unproven[ref.Name] == nil {
			a.unproven[ref.Name] = make(map[string]struct{})
		}
		a.unproven[ref.Name][a.scope] = struct{}{}
		_, inBase64Body := base64Body[ref.Name]
		blocks := !placeholder || inBase64Body
		if blocks {
			a.blocking[ref.Name] = struct{}{}
		}
		a.misses = append(a.misses, Miss{Path: path, Reference: ref, Blocking: blocks, Base64Body: inBase64Body})
	}
	// Protocol configuration is flattened and handed to the protocol before
	// init/actions run. Unlike action fields, {{var.*}} references in these
	// values are not interpolated from parameters, replay seeds, or prior
	// extractors. Always report relevant config references as unresolved.
	configMissing := relevantHTTPConfigReferences(action, config)
	a.byAction[path] = append(a.byAction[path], configMissing...)
	for _, name := range configMissing {
		a.blocking[name] = struct{}{}
		a.misses = append(a.misses, Miss{Path: path, Reference: Reference{Namespace: NamespaceVar, Name: name}, Blocking: true, Config: true})
	}
}

// keepsVariablePlaceholders reports whether replay carries an unresolved
// {{var.*}} reference in this action as its placeholder text. Interpolation
// leaves such a reference literal (interpolateWithSecrets,
// pkg/engine/vuser_resolve.go), and the HTTP adapter sends it and warns
// (noteUnresolvedVariables, pkg/protocol/http/http.go). The SOAP and Java
// HTTP adapters send their requests through that adapter (SOAP.Execute,
// pkg/protocol/soap/soap.go; JavaHTTP.Execute,
// pkg/protocol/java_http/java_http.go); no other registered protocol embeds
// it. A Java HTTP body is the exception (base64BodyVariables), and a JSON
// helper fails its step instead.
func keepsVariablePlaceholders(action dsl.Action, protocol string) bool {
	if dsl.CanonicalActionType(action.Type) == "source_json" {
		return false
	}
	switch dsl.ProtocolFamily(protocol) {
	case "http", "soap", "java_http":
		return true
	default:
		return false
	}
}

// base64BodyVariables returns the {{var.*}} names in the stored body of a
// Java HTTP request. JavaHTTP.Execute decodes the body it receives as base64
// before handing the bytes to the HTTP adapter, and placeholder braces are not
// base64, so the step fails ("invalid base64 java_http body") instead of
// sending the placeholder. A reference inside a body stored with
// body_encoding base64 is not among them: the engine decodes that body,
// interpolates it and encodes it again (interpolateActionBody,
// pkg/engine/vuser_resolve.go), so its placeholder is sent like a target or
// header placeholder. Engine steps never reach the adapter.
func base64BodyVariables(action dsl.Action, protocol string) map[string]struct{} {
	if dsl.ProtocolFamily(protocol) != "java_http" || !executesThroughProtocol(action.Type) {
		return nil
	}
	names := make(map[string]struct{})
	collectVariableNames(action.Body, names)
	return names
}

func scriptConfigReferences(script *dsl.Script) configReferences {
	if script == nil {
		return configReferences{}
	}
	baseSet := make(map[string]struct{})
	collectVariableNames(script.Config.BaseURL, baseSet)

	transportHTTP := script.Config.HTTP
	transportHTTP.Auth = dsl.AuthConfig{}
	transportHTTP.DefaultHeaders = nil
	transportHTTP.UserAgent = ""
	transportHTTP.AcceptEncoding = ""
	transportHTTP.AcceptLanguage = ""
	requestHTTP := struct {
		Auth             dsl.AuthConfig              `json:"auth,omitempty"`
		DefaultHeaders   map[string]string           `json:"default_headers,omitempty"`
		UserAgent        string                      `json:"user_agent,omitempty"`
		AcceptEncoding   string                      `json:"accept_encoding,omitempty"`
		AcceptLanguage   string                      `json:"accept_language,omitempty"`
		WebRuntime       dsl.WebRuntimeConfig        `json:"web_runtime,omitempty"`
		BrowserEmulation *dsl.BrowserEmulationConfig `json:"browser_emulation,omitempty"`
	}{
		Auth:             script.Config.HTTP.Auth,
		DefaultHeaders:   script.Config.HTTP.DefaultHeaders,
		UserAgent:        script.Config.HTTP.UserAgent,
		AcceptEncoding:   script.Config.HTTP.AcceptEncoding,
		AcceptLanguage:   script.Config.HTTP.AcceptLanguage,
		WebRuntime:       script.Config.WebRuntime,
		BrowserEmulation: script.Config.BrowserEmulation,
	}
	// AdditionalAttributes is import evidence and is not flattened into the
	// HTTP runtime configuration.
	requestHTTP.WebRuntime.AdditionalAttributes = nil

	return configReferences{
		baseURL:       sortedStringSet(baseSet),
		httpTransport: configValueReferences(transportHTTP),
		httpRequest:   configValueReferences(requestHTTP),
		soap:          configValueReferences(script.Config.SOAP),
		javaHTTP:      configValueReferences(script.Config.JavaHTTP),
		protocol:      strings.ToLower(strings.TrimSpace(script.Protocol)),
	}
}

func configValueReferences(value any) []string {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	set := make(map[string]struct{})
	collectVariableNames(string(payload), set)
	return sortedStringSet(set)
}

// collectVariableNames adds the {{var.*}} names in value to set.
func collectVariableNames(value string, set map[string]struct{}) {
	for _, ref := range References(value) {
		if ref.Namespace == NamespaceVar {
			set[ref.Name] = struct{}{}
		}
	}
}

func relevantHTTPConfigReferences(action dsl.Action, refs configReferences) []string {
	actionType := strings.ToLower(strings.TrimSpace(dsl.CanonicalActionType(action.Type)))
	if actionType == "http_oauth2_token" {
		return append([]string(nil), refs.httpTransport...)
	}
	if !actionUsesHTTPRequestConfig(action) {
		return nil
	}
	references := append([]string(nil), refs.httpTransport...)
	references = append(references, refs.httpRequest...)
	if !absoluteHTTPActionTarget(action.Target) {
		references = append(references, refs.baseURL...)
	}
	family := strings.ToLower(strings.TrimSpace(dsl.ProtocolFamily(refs.protocol)))
	if family == "soap" || actionType == "soap_request" {
		references = append(references, refs.soap...)
	}
	if family == "java_http" || actionType == "java_http_request" {
		references = append(references, refs.javaHTTP...)
	}
	return uniqueSortedStrings(references)
}

func actionUsesHTTPRequestConfig(action dsl.Action) bool {
	actionType := strings.ToLower(strings.TrimSpace(dsl.CanonicalActionType(action.Type)))
	switch actionType {
	case "request", "http_request", "soap_request", "java_http_request":
		return true
	}
	method := strings.ToUpper(strings.TrimSpace(action.Method))
	if method == "" {
		method = strings.ToUpper(strings.TrimSpace(action.Type))
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}

func absoluteHTTPActionTarget(target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	return strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
}

func cloneControlShell(control *dsl.ControlFlow) *dsl.ControlFlow {
	if control == nil {
		return nil
	}
	cloned := *control
	cloned.Steps = nil
	cloned.Else = nil
	cloned.Default = nil
	cloned.Cases = append([]dsl.SwitchCase(nil), control.Cases...)
	for index := range cloned.Cases {
		cloned.Cases[index].Steps = nil
	}
	return &cloned
}

// sequenceWrapperOutputs is what an http_sequence wrapper reads itself: the
// attribution executeHTTPSequenceAction interpolates for the aggregate result.
// It also interpolates the wrapper's metadata, but only to annotate that
// result, so the metadata is evidence as for any step that does not send it.
func sequenceWrapperOutputs(action dsl.Action) Outputs {
	outputs := Outputs{Known: true}
	if action.Attribution != nil {
		outputs.ReadTexts = []string{action.Attribution.HierarchyPath, action.Attribution.PageName}
	}
	return outputs
}

func sequenceChildActions(params map[string]any) []dsl.Action {
	if params == nil || params["requests"] == nil {
		return nil
	}
	payload, err := json.Marshal(params["requests"])
	if err != nil {
		return nil
	}
	var children []dsl.Action
	if err := json.Unmarshal(payload, &children); err != nil {
		return nil
	}
	return children
}

func intersectStates(target, left, right *state) {
	target.available = intersectStringSets(left.available, right.available)
	target.saveAll = intersectStringSets(left.saveAll, right.saveAll)
	target.resultPrefixes = intersectStringSets(left.resultPrefixes, right.resultPrefixes)
	target.extracted = intersectStringSets(left.extracted, right.extracted)
}

func replaceState(target, source *state) {
	target.available = source.available
	target.saveAll = source.saveAll
	target.resultPrefixes = source.resultPrefixes
	target.extracted = source.extracted
}

func intersectStringSets(left, right map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, min(len(left), len(right)))
	for name := range left {
		if _, ok := right[name]; ok {
			out[name] = struct{}{}
		}
	}
	return out
}

// postTestLoop reports whether a loop runs its body before it first reads its
// condition: a condition-driven do_while loop skips the condition on its first
// pass (controlLoopCursor.next, pkg/engine/control_flow.go). A for_each or
// counted loop ignores do_while.
func postTestLoop(control *dsl.ControlFlow) bool {
	return control != nil && control.ForEach == nil && control.Count <= 0 && control.DoWhile
}

func loopRunsAtLeastOnce(control *dsl.ControlFlow) bool {
	if control == nil || control.ForEach != nil {
		return false
	}
	return control.Count > 0 || control.DoWhile
}

func sortedStringSet(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func uniqueSortedStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	return sortedStringSet(set)
}
