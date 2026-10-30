// Package paramize provides a shared post-conversion parameterization pass for
// every script importer. It inspects a converted DSL script (plus optional
// request/response observations) and returns structured suggestions for three
// classes of parameterization:
//
//   - correlation: dynamic values extracted from a response and reused in later
//     requests (delegates detection to pkg/convert/correlate)
//   - generated: identifiers and time stamps a client makes up per request
//     (trace and message IDs, a request time stamp), generated each iteration
//   - data_param: literal values that should come from a data source (user ids,
//     emails, SKUs, ...) so each VUser gets distinct data
//   - secret: credentials that should be externalized to {{env.KEY}} instead of
//     being hardcoded in the script
//
// Suggest never mutates the script: applying a suggestion is a separate,
// human-approved step handled by ApplySuggestions.
package paramize

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
)

type Kind string

const (
	KindCorrelation Kind = "correlation"
	KindGenerated   Kind = "generated"
	KindDataParam   Kind = "data_param"
	KindSecret      Kind = "secret"
)

// autoApplyConfidence mirrors correlate.HighConfidence: suggestions at or above
// this confidence arrive pre-selected in the review UI (still requires save).
const autoApplyConfidence = 0.8

// Location pins one occurrence of a literal value inside the script and the
// exact text substitution that parameterizes it. SearchText carries enough
// surrounding context (e.g. `user_id=12345` or `"user_id": "12345"`) to make
// the replacement unambiguous within the field.
type Location struct {
	Section          string                `json:"section"`      // "init", "actions", or "teardown"
	ActionIndex      int                   `json:"action_index"` // 0-based index within the section
	ActionName       string                `json:"action_name,omitempty"`
	Field            string                `json:"field"` // "target", "body", "value", "headers.<Name>", or "body_multipart.<index>"
	SearchText       string                `json:"search_text,omitempty"`
	ReplaceText      string                `json:"replace_text"`
	ActionPath       string                `json:"action_path,omitempty"`
	AuraRoute        []aurapayload.Segment `json:"aura_route,omitempty"`
	DecodedFieldPath string                `json:"decoded_field_path,omitempty"`
	EncodingLayers   []string              `json:"encoding_layers,omitempty"`
	PayloadShape     string                `json:"payload_shape,omitempty"`
	// RequestIndex places the location in a request of a concurrent request
	// group: the index within the action's params.requests. Nil for the
	// action itself.
	RequestIndex      *int `json:"request_index,omitempty"`
	auraExpectedShape string
}

// Suggestion is one proposed parameterization. It is advice only: nothing in
// the script changes until ApplySuggestions runs with this ID selected.
type Suggestion struct {
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	// ScriptName scopes the suggestion when an import produced multiple
	// scripts (JMeter thread groups); empty for single-script imports.
	ScriptName   string     `json:"script_name,omitempty"`
	VarName      string     `json:"var_name"`
	Replacement  string     `json:"replacement"`             // display template, e.g. {{var.x}} / {{env.KEY}}
	LiteralValue string     `json:"literal_value,omitempty"` // the raw value being replaced; server-side only on import responses
	SampleValue  string     `json:"sample_value,omitempty"`  // truncated display copy of LiteralValue; server-side only on import responses
	Locations    []Location `json:"locations"`
	Confidence   float64    `json:"confidence"`
	RuleID       string     `json:"rule_id"`
	Rationale    string     `json:"rationale"`
	AutoApply    bool       `json:"auto_apply"`
	HighRisk     bool       `json:"high_risk"`
	// PreApplied marks correlations the converter already wired into the script
	// (sample/HAR apply high-confidence correlations in-converter). These are
	// informational: the review UI shows them without a checkbox and
	// ApplySuggestions skips them.
	PreApplied bool `json:"pre_applied,omitempty"`
	// Correlation-only: the extractor to add at the source action.
	Extractor     *dsl.Extractor `json:"extractor,omitempty"`
	FoundInAction *Location      `json:"found_in_action,omitempty"`
	// DataParam and Generated: the parameter to materialize on apply. A data
	// parameter's DataSource reference is finalized by ApplySuggestions when
	// the consolidated data source is created; a generated one needs none.
	Parameter *dsl.Parameter `json:"parameter,omitempty"`
}

type Summary struct {
	Total           int          `json:"total"`
	ByKind          map[Kind]int `json:"by_kind"`
	AutoApplyCount  int          `json:"auto_apply_count"`
	ReviewCount     int          `json:"review_count"`
	HighRiskCount   int          `json:"high_risk_count"`
	PreAppliedCount int          `json:"pre_applied_count"`
}

type Result struct {
	Suggestions []Suggestion         `json:"suggestions"`
	Summary     Summary              `json:"summary"`
	Warnings    []string             `json:"warnings,omitempty"`
	AuraPayload *AuraPayloadCoverage `json:"aura_payload,omitempty"`
}

// Selection identifies one or more parameterization suggestion IDs chosen by the
// operator during import/conversion. It decodes either a plain string suggestion
// ID or an object with id/suggestion_id/selected_ids and optional script_index.
type Selection struct {
	ID          string   `json:"id,omitempty"`
	ScriptIndex int      `json:"script_index,omitempty"`
	SelectedIDs []string `json:"selected_ids,omitempty"`
}

func (s *Selection) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		id := strings.TrimSpace(single)
		s.ID = id
		s.ScriptIndex = 0
		if id != "" {
			s.SelectedIDs = []string{id}
		} else {
			s.SelectedIDs = nil
		}
		return nil
	}
	var obj struct {
		ID           string   `json:"id"`
		SuggestionID string   `json:"suggestion_id"`
		ScriptIndex  int      `json:"script_index"`
		SelectedIDs  []string `json:"selected_ids"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	s.ScriptIndex = obj.ScriptIndex
	s.ID = strings.TrimSpace(obj.ID)
	if s.ID == "" {
		s.ID = strings.TrimSpace(obj.SuggestionID)
	}
	ids := make([]string, 0, len(obj.SelectedIDs)+1)
	if s.ID != "" {
		ids = append(ids, s.ID)
	}
	for _, raw := range obj.SelectedIDs {
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	s.SelectedIDs = ids
	return nil
}

// SelectedIDsForScript collects all selected suggestion IDs targeting scriptIndex.
func SelectedIDsForScript(selections []Selection, scriptIndex int) map[string]bool {
	selected := make(map[string]bool)
	for _, item := range selections {
		if item.ScriptIndex != scriptIndex {
			continue
		}
		if id := strings.TrimSpace(item.ID); id != "" {
			selected[id] = true
		}
		for _, id := range item.SelectedIDs {
			if trimmed := strings.TrimSpace(id); trimmed != "" {
				selected[trimmed] = true
			}
		}
	}
	return selected
}

// PreAppliedCorrelation describes a correlation a converter already applied to
// the generated script (extractor added, literals replaced with {{var.X}}).
type PreAppliedCorrelation struct {
	Name        string
	Source      string
	Expression  string
	ExtractType string
	Convert     string
	SampleValue string
	FoundInStep int
	UsedInSteps []int
}

// Options gates which suggestion classes run. Importers without response data
// leave Observations empty, which makes the correlation pass a no-op.
type Options struct {
	// Observations feed the correlation pass. Observation IDs are 1-based
	// indexes into script.Actions by default (the convention used by the sample
	// importer). Set ObservationIDsZeroBased for recorder baselines, whose IDs
	// are stored as generated script action indexes.
	Observations []correlate.Observation
	// ObservationIDsZeroBased switches correlation action mapping from the
	// default 1-based observation convention to 0-based script action indexes.
	ObservationIDsZeroBased bool
	// PreApplied lists correlations the converter already wired into the
	// script; they surface as informational pre_applied suggestions.
	PreApplied        []PreAppliedCorrelation
	EnableCorrelation bool
	// EnableGenerated proposes generated parameters for per-request
	// identifiers and time stamps.
	EnableGenerated      bool
	EnableDataParams     bool
	EnableSecrets        bool
	AuraInspectionBudget *AuraInspectionBudget `json:"-"`
}

// Suggest inspects a converted script and returns parameterization advice.
// It does not mutate the script and produces deterministic ordering.
func Suggest(script *dsl.Script, opts Options) Result {
	result := Result{Suggestions: []Suggestion{}}
	if script == nil {
		result.Summary = buildSummary(result.Suggestions)
		return result
	}

	names := newNameRegistry(script)
	suggestions := make([]Suggestion, 0)

	if opts.EnableCorrelation {
		correlationAdvice, err := correlationSuggestions(script, opts.Observations, names, opts.ObservationIDsZeroBased)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Correlation analysis was not completed and no correlation suggestions were produced: %v", err))
		} else {
			suggestions = append(suggestions, correlationAdvice...)
		}
	}
	suggestions = append(suggestions, preAppliedSuggestions(opts.PreApplied)...)
	if opts.EnableSecrets {
		secretAdvice, err := secretSuggestions(script)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Secret analysis was not completed and no secret suggestions were produced: %v", err))
		} else {
			suggestions = append(suggestions, secretAdvice...)
		}
	}
	if opts.EnableGenerated {
		generatedAdvice, err := generatedSuggestions(script, names, claimedValues(suggestions))
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Generated-value analysis was not completed and no generated-value suggestions were produced: %v", err))
		} else {
			suggestions = append(suggestions, generatedAdvice...)
		}
	}
	if opts.EnableDataParams {
		claimed := claimedValues(suggestions)
		auraAdvice, coverage, warnings := auraPayloadSuggestions(script, names, claimed, opts.AuraInspectionBudget)
		result.AuraPayload = coverage
		result.Warnings = append(result.Warnings, warnings...)
		suggestions = append(suggestions, auraAdvice...)
		skipBodies := map[string]bool{}
		if coverage != nil {
			skipBodies = coverage.skipBodies
		}
		var generatedParts []string
		for _, suggestion := range suggestions {
			if suggestion.Kind == KindGenerated {
				for _, location := range suggestion.Locations {
					generatedParts = append(generatedParts, location.SearchText)
				}
			}
		}
		dataAdvice, err := dataParamSuggestions(script, names, claimed, skipBodies, generatedParts)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Data-parameter analysis was not completed and no data-parameter suggestions were produced: %v", err))
		} else {
			suggestions = append(suggestions, dataAdvice...)
		}
	}

	sortSuggestions(suggestions)
	idOccurrences := make(map[string]int, len(suggestions))
	for i := range suggestions {
		baseID := suggestionID(suggestions[i])
		idOccurrences[baseID]++
		suggestions[i].ID = baseID
		if idOccurrences[baseID] > 1 {
			// The public structural descriptor deliberately excludes literal and
			// sample values so an ID cannot become a credential oracle. Duplicate
			// sensitive headers can therefore share a descriptor even though they
			// are independently selectable suggestions. Add a deterministic public
			// occurrence suffix so selecting one row never selects its siblings.
			suggestions[i].ID = fmt.Sprintf("%s_%d", baseID, idOccurrences[baseID])
		}
		suggestions[i].AutoApply = suggestions[i].Confidence >= autoApplyConfidence &&
			!suggestions[i].HighRisk && !suggestions[i].PreApplied
	}

	result.Suggestions = suggestions
	result.Summary = buildSummary(suggestions)
	return result
}

func buildSummary(suggestions []Suggestion) Summary {
	summary := Summary{
		Total:  len(suggestions),
		ByKind: map[Kind]int{},
	}
	for _, s := range suggestions {
		summary.ByKind[s.Kind]++
		switch {
		case s.PreApplied:
			summary.PreAppliedCount++
		case s.AutoApply:
			summary.AutoApplyCount++
		default:
			summary.ReviewCount++
		}
		if s.HighRisk {
			summary.HighRiskCount++
		}
	}
	return summary
}

var kindOrder = map[Kind]int{KindCorrelation: 0, KindGenerated: 1, KindDataParam: 2, KindSecret: 3}

func sortSuggestions(suggestions []Suggestion) {
	sort.SliceStable(suggestions, func(i, j int) bool {
		if kindOrder[suggestions[i].Kind] != kindOrder[suggestions[j].Kind] {
			return kindOrder[suggestions[i].Kind] < kindOrder[suggestions[j].Kind]
		}
		if suggestions[i].Confidence != suggestions[j].Confidence {
			return suggestions[i].Confidence > suggestions[j].Confidence
		}
		return suggestions[i].VarName < suggestions[j].VarName
	})
}

func suggestionID(s Suggestion) string {
	// IDs cross the disclosure boundary even though literal/sample/search
	// values do not. Derive them exclusively from the public structural
	// descriptor so an observer cannot use the ID as an offline oracle for a
	// hidden credential or correlation sample.
	var descriptor strings.Builder
	appendSuggestionIDPart := func(value string) {
		descriptor.WriteString(strconv.Itoa(len(value)))
		descriptor.WriteByte(':')
		descriptor.WriteString(value)
	}
	appendLocation := func(location Location) {
		appendSuggestionIDPart(location.Section)
		appendSuggestionIDPart(strconv.Itoa(location.ActionIndex))
		if location.RequestIndex != nil {
			appendSuggestionIDPart("request")
			appendSuggestionIDPart(strconv.Itoa(*location.RequestIndex))
		}
		appendSuggestionIDPart(location.ActionName)
		appendSuggestionIDPart(location.Field)
		appendSuggestionIDPart(location.ReplaceText)
		if hasAuraLocation(location) {
			appendSuggestionIDPart(location.ActionPath)
			appendSuggestionIDPart(location.DecodedFieldPath)
			appendSuggestionIDPart(location.PayloadShape)
			for _, layer := range location.EncodingLayers {
				appendSuggestionIDPart(layer)
			}
			for _, segment := range location.AuraRoute {
				appendSuggestionIDPart(segment.Kind)
				appendSuggestionIDPart(segment.Key)
				appendSuggestionIDPart(strconv.Itoa(segment.Index))
			}
		}
	}

	appendSuggestionIDPart(string(s.Kind))
	appendSuggestionIDPart(s.ScriptName)
	appendSuggestionIDPart(s.VarName)
	appendSuggestionIDPart(s.Replacement)
	appendSuggestionIDPart(s.RuleID)
	appendSuggestionIDPart(strconv.FormatBool(s.PreApplied))
	appendSuggestionIDPart(strconv.FormatBool(s.HighRisk))
	appendSuggestionIDPart(strconv.Itoa(len(s.Locations)))
	for _, location := range s.Locations {
		appendLocation(location)
	}
	if s.FoundInAction == nil {
		appendSuggestionIDPart("")
	} else {
		appendSuggestionIDPart("found")
		appendLocation(*s.FoundInAction)
	}

	sum := sha256.Sum256([]byte(descriptor.String()))
	return fmt.Sprintf("pz_%x", sum[:12])
}

// claimedValues returns the literal values already covered by earlier passes so
// the data-param pass does not double-suggest a value the correlation or
// secret pass already owns.
func claimedValues(suggestions []Suggestion) map[string]bool {
	claimed := make(map[string]bool, len(suggestions))
	for _, s := range suggestions {
		if s.LiteralValue != "" {
			claimed[s.LiteralValue] = true
		}
		if s.Kind == KindGenerated {
			// One generated parameter can stand for several recorded values.
			for _, location := range s.Locations {
				if location.SearchText != "" {
					claimed[location.SearchText] = true
				}
			}
		}
	}
	return claimed
}

// nameRegistry hands out unique variable names, seeded with identifiers the
// script already uses so suggestions never collide with existing parameters,
// data sources, extractors, or variables the script reads.
type nameRegistry struct {
	taken map[string]bool
}

func newNameRegistry(script *dsl.Script) *nameRegistry {
	registry := &nameRegistry{taken: map[string]bool{}}
	if script == nil {
		return registry
	}
	for _, param := range script.Parameters {
		registry.taken[param.Name] = true
	}
	for _, source := range script.DataSources {
		registry.taken[source.Name] = true
	}
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if ref.Action != nil {
			for _, extractor := range ref.Action.Extractors {
				registry.taken[extractor.Name] = true
			}
		}
		if ref.Request != nil {
			// Native imports keep typed extractors in sequence request maps;
			// JSON round trips turn them into maps. Reserve the same names in
			// both representations so suggestion IDs remain selectable on import.
			reserveExtractorNames(registry.taken, ref.Request["extractors"])
		}
		return nil
	})
	// A variable the script reads keeps its name even when nothing defines it,
	// as with an input the source left unset: a suggestion under that name
	// would supply the input with its own value, or define it twice.
	if payload, err := json.Marshal(script); err == nil {
		for _, match := range variableReferencePattern.FindAllSubmatch(payload, -1) {
			registry.taken[string(match[1])] = true
		}
	}
	return registry
}

// variableReferencePattern finds the name in a {{var.NAME}} reference.
var variableReferencePattern = regexp.MustCompile(`\{\{\s*var\.([A-Za-z0-9_\-]+)`)

func reserveExtractorNames(taken map[string]bool, raw any) {
	reserve := func(value any) {
		var name string
		switch extractor := value.(type) {
		case dsl.Extractor:
			name = extractor.Name
		case map[string]any:
			name, _ = extractor["name"].(string)
		}
		if name != "" {
			taken[name] = true
		}
	}
	switch extractors := raw.(type) {
	case []dsl.Extractor:
		for _, extractor := range extractors {
			reserve(extractor)
		}
	case []map[string]any:
		for _, extractor := range extractors {
			reserve(extractor)
		}
	case []any:
		for _, extractor := range extractors {
			reserve(extractor)
		}
	}
}

func (r *nameRegistry) claim(base string) string {
	name := correlate.Slug(base)
	if name == "" {
		name = "value"
	}
	if !r.taken[name] {
		r.taken[name] = true
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s_%d", name, i)
		if !r.taken[candidate] {
			r.taken[candidate] = true
			return candidate
		}
	}
}

// forEachAction visits the top-level actions of every section, and the
// requests of each concurrent request group (http_sequence) held as typed
// actions, with request set to their index in params.requests. Nested
// control-flow bodies (control_if / control_loop steps) are intentionally not
// visited in this pass; their fields stay untouched by suggestions.
func forEachAction(script *dsl.Script, visit func(section string, index int, request *int, action *dsl.Action)) {
	if script == nil {
		return
	}
	visitSection := func(section string, actions []dsl.Action) {
		for i := range actions {
			children := groupRequests(&actions[i], false)
			if len(children) == 0 {
				visit(section, i, nil, &actions[i])
			}
			// A request group sends only its requests; its own target is a
			// copy of the first request's, so it is not scanned.
			for j := range children {
				request := j
				visit(section, i, &request, &children[j])
			}
		}
	}
	visitSection("init", script.Init)
	visitSection("actions", script.Actions)
	visitSection("teardown", script.Teardown)
}

// requestKey spells a location's request index for dedup keys.
func requestKey(request *int) string {
	if request == nil {
		return ""
	}
	return strconv.Itoa(*request)
}

// slotPath is the DSL path of an action or of a request of its concurrent
// group, as dslwalk spells it.
func slotPath(section string, index int, request *int) string {
	path := fmt.Sprintf("%s[%d]", section, index)
	if request != nil {
		path += fmt.Sprintf(".params.requests[%d]", *request)
	}
	return path
}

// locationAction resolves a location to its action, or to the request of a
// concurrent request group it names; nil when it does not resolve. typed
// stores a group's JSON-shaped requests back as typed actions, so changes to
// the returned request persist (ApplySuggestions passes it on its clone);
// otherwise the request is a decoded copy, for reading only.
func locationAction(script *dsl.Script, location Location, typed bool) *dsl.Action {
	action := actionAt(script, location.Section, location.ActionIndex)
	if action == nil || location.RequestIndex == nil {
		return action
	}
	children := groupRequests(action, typed)
	if *location.RequestIndex < 0 || *location.RequestIndex >= len(children) {
		return nil
	}
	return &children[*location.RequestIndex]
}

// groupRequests is the requests of an http_sequence request group as typed
// actions: its own []dsl.Action, or the decoded JSON-shaped requests a script
// holds after a JSON round trip. store writes decoded requests back into the
// action; otherwise they are copies. A request that does not decode, or any
// other action, gives none.
func groupRequests(action *dsl.Action, store bool) []dsl.Action {
	if action == nil || action.Type != "http_sequence" || action.Params == nil {
		return nil
	}
	switch requests := action.Params["requests"].(type) {
	case []dsl.Action:
		return requests
	case []any:
		return decodeGroupRequests(action, requests, store)
	case []map[string]any:
		// The LoadRunner archive importer builds its transaction groups so.
		return decodeGroupRequests(action, requests, store)
	}
	return nil
}

// decodeGroupRequests decodes a group's JSON-shaped requests into typed
// actions, storing them back into the action when store is set.
func decodeGroupRequests[T any](action *dsl.Action, requests []T, store bool) []dsl.Action {
	decoded := make([]dsl.Action, len(requests))
	for index, request := range requests {
		raw, err := json.Marshal(request)
		if err != nil || json.Unmarshal(raw, &decoded[index]) != nil {
			return nil
		}
	}
	if store {
		action.Params["requests"] = decoded
	}
	return decoded
}

func actionAt(script *dsl.Script, section string, index int) *dsl.Action {
	var actions []dsl.Action
	switch section {
	case "init":
		actions = script.Init
	case "actions":
		actions = script.Actions
	case "teardown":
		actions = script.Teardown
	default:
		return nil
	}
	if index < 0 || index >= len(actions) {
		return nil
	}
	return &actions[index]
}

func truncateValue(value string) string {
	return correlate.Truncate(value, 80)
}

// isTemplated reports whether a value already contains template syntax and
// therefore must not be re-parameterized.
func isTemplated(value string) bool {
	return strings.Contains(value, "{{")
}
