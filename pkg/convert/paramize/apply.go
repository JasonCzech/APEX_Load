package paramize

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Diff records one concrete change ApplySuggestions made, in the same shape as
// the authoring-correlation diffs so the dashboard reuses one renderer.
type Diff struct {
	SuggestionID string `json:"suggestion_id"`
	Kind         string `json:"kind"` // value_replaced | extractor_added | data_source_added | parameter_added
	Path         string `json:"path"`
	Before       string `json:"before,omitempty"`
	After        string `json:"after,omitempty"`
}

// ApplySuggestions returns a new script clone with only the selected
// suggestions applied: literals replaced with templates, extractors appended,
// and a consolidated data source + parameters materialized for accepted
// data-param suggestions. The input script is never mutated. Pre-applied
// suggestions are skipped even when selected.
func ApplySuggestions(script *dsl.Script, suggestions []Suggestion, selected map[string]bool) (*dsl.Script, []Diff, error) {
	if script == nil {
		return nil, nil, fmt.Errorf("script is required")
	}
	plans, err := planAuraApplication(script, suggestions, selected)
	if err != nil {
		return nil, nil, err
	}
	if err := validateSelectedSuggestionApplication(script, suggestions, selected); err != nil {
		return nil, nil, err
	}
	clone, err := cloneScript(script)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to clone script: %w", err)
	}

	diffs, err := applyAuraPlans(clone, plans)
	if err != nil {
		return nil, nil, fmt.Errorf("failed bounded Aura apply traversal")
	}
	dataColumns := make(map[string]string) // column -> seed value
	dataColumnOrder := make([]string, 0)

	for _, suggestion := range suggestions {
		if !selected[suggestion.ID] || suggestion.PreApplied {
			continue
		}
		diffs = append(diffs, applyReplacements(clone, suggestion)...)

		switch suggestion.Kind {
		case KindCorrelation:
			if diff, ok := appendExtractor(clone, suggestion); ok {
				diffs = append(diffs, diff)
			}
		case KindGenerated:
			if diff, ok := materializeGeneratedParameter(clone, suggestion); ok {
				diffs = append(diffs, diff)
			}
		case KindDataParam:
			if suggestion.Parameter == nil {
				continue
			}
			column := suggestion.Parameter.Column
			if _, exists := dataColumns[column]; !exists {
				dataColumns[column] = suggestion.LiteralValue
				dataColumnOrder = append(dataColumnOrder, column)
			}
		}
	}

	if len(dataColumnOrder) > 0 {
		sourceName, sourceDiff := materializeDataSource(clone, dataColumnOrder, dataColumns)
		diffs = append(diffs, sourceDiff)
		diffs = append(diffs, materializeParameters(clone, suggestions, selected, sourceName)...)
	}

	return clone, diffs, nil
}

// validateSelectedSuggestionApplication preflights every selected correlation
// before the clone is mutated and prevents order-dependent application when
// any two selected suggestion kinds claim overlapping literals on one concrete
// request surface. This guarantees that an extractor is never added without
// all of its reviewed replacements, and that a secret/data replacement cannot
// silently consume text owned by a correlation (or vice versa).
func validateSelectedSuggestionApplication(script *dsl.Script, suggestions []Suggestion, selected map[string]bool) error {
	type source struct {
		section     string
		actionIndex int
	}
	type surface struct {
		section     string
		actionIndex int
		field       string
	}
	type owner struct {
		suggestionIndex int
		literal         string
		replacement     string
	}
	owners := make(map[surface][]owner)
	pendingExtractors := make(map[source]map[string]dsl.Extractor)
	overlapBudget := NewLiteralOverlapSearchBudget()
	for index, suggestion := range suggestions {
		if suggestion.PreApplied || !selected[suggestion.ID] {
			continue
		}
		if suggestion.Kind == KindCorrelation {
			if suggestion.Extractor == nil || suggestion.FoundInAction == nil || strings.TrimSpace(suggestion.Extractor.Name) == "" {
				return fmt.Errorf("selected correlation %q is missing its extractor or source action", suggestion.ID)
			}
			sourceAction := actionAt(script, suggestion.FoundInAction.Section, suggestion.FoundInAction.ActionIndex)
			if sourceAction == nil {
				return fmt.Errorf("selected correlation %q references an unavailable source action", suggestion.ID)
			}
			extractorExists := false
			for _, existing := range sourceAction.Extractors {
				if existing.Name != suggestion.Extractor.Name {
					continue
				}
				if !reflect.DeepEqual(existing, *suggestion.Extractor) {
					return fmt.Errorf("selected correlation %q conflicts with the existing extractor %q", suggestion.ID, existing.Name)
				}
				extractorExists = true
			}
			sourceKey := source{
				section:     suggestion.FoundInAction.Section,
				actionIndex: suggestion.FoundInAction.ActionIndex,
			}
			pending := pendingExtractors[sourceKey]
			if !extractorExists {
				if pending == nil {
					pending = make(map[string]dsl.Extractor)
					pendingExtractors[sourceKey] = pending
				}
				if sibling, exists := pending[suggestion.Extractor.Name]; exists {
					if !reflect.DeepEqual(sibling, *suggestion.Extractor) {
						return fmt.Errorf("selected correlation %q conflicts with another selected extractor %q", suggestion.ID, suggestion.Extractor.Name)
					}
				} else {
					pending[suggestion.Extractor.Name] = *suggestion.Extractor
				}
			}
			if len(sourceAction.Extractors)+len(pending) > dsl.MaxActionExtractors {
				return fmt.Errorf("selected correlations for source action %q would exceed the maximum of %d extractors", sourceAction.Name, dsl.MaxActionExtractors)
			}
			if len(suggestion.Locations) == 0 {
				return fmt.Errorf("selected correlation %q has no request locations to replace", suggestion.ID)
			}
		}
		for _, location := range suggestion.Locations {
			if hasAuraLocation(location) {
				continue
			}
			literal := location.SearchText
			if literal == "" {
				if suggestion.Kind == KindCorrelation {
					return fmt.Errorf("selected correlation %q has an empty request literal", suggestion.ID)
				}
				continue
			}
			if !locationContainsOutsideTemplates(script, location, literal) {
				if suggestion.Kind == KindCorrelation {
					return fmt.Errorf("selected correlation %q no longer matches its reviewed request location", suggestion.ID)
				}
				continue
			}
			field := location.Field
			if strings.HasPrefix(strings.ToLower(field), "headers.") {
				field = strings.ToLower(field)
			}
			key := surface{section: location.Section, actionIndex: location.ActionIndex, field: field}
			current := owner{suggestionIndex: index, literal: literal, replacement: location.ReplaceText}
			for _, existing := range owners[key] {
				if existing.suggestionIndex == index {
					continue
				}
				contains := overlapBudget.Contains(existing.literal, current.literal) || overlapBudget.Contains(current.literal, existing.literal)
				if overlapBudget.Exhausted() {
					return fmt.Errorf("ambiguous selected correlation replacements: overlap analysis exceeded its deterministic work limit")
				}
				if contains && existing.replacement != current.replacement {
					return fmt.Errorf("ambiguous selected correlation replacements: multiple variables claim overlapping request literals")
				}
				// Partially-overlapping literals (a suffix of one is a prefix of the
				// other and the merged spelling really occurs in this field) never
				// survive the single-pass replacer: the leftmost literal consumes the
				// shared bytes, the other suggestion's replacement silently does not
				// happen, and its extractor would still be appended. Equal replacements
				// do not make this safe — the merged region keeps a stale tail.
				if !contains && literalsPartiallyOverlapInLocation(script, location, existing.literal, current.literal, overlapBudget) {
					if overlapBudget.Exhausted() {
						return fmt.Errorf("ambiguous selected correlation replacements: overlap analysis exceeded its deterministic work limit")
					}
					return fmt.Errorf("ambiguous selected correlation replacements: multiple variables claim partially overlapping request literals")
				}
			}
			owners[key] = append(owners[key], current)
		}
	}
	return nil
}

func locationContainsOutsideTemplates(script *dsl.Script, location Location, literal string) bool {
	action := actionAt(script, location.Section, location.ActionIndex)
	if action == nil || literal == "" {
		return false
	}
	contains := func(value string) bool { return containsOutsideTemplates(value, literal) }
	if value, ok := multipartFieldValue(action, location.Field); ok {
		return contains(*value)
	}
	switch location.Field {
	case "target":
		return contains(action.Target)
	case "body":
		return contains(action.Body)
	case "value":
		return contains(action.Value)
	}
	if paramKey, isParam := strings.CutPrefix(location.Field, "params."); isParam {
		value, ok := action.Params[paramKey].(string)
		return ok && contains(value)
	}
	headerKey, isHeader := strings.CutPrefix(strings.ToLower(location.Field), "headers.")
	if !isHeader {
		return false
	}
	for key, value := range action.Headers {
		if strings.EqualFold(key, headerKey) && contains(value) {
			return true
		}
	}
	for _, header := range action.HeaderList {
		if strings.EqualFold(header.Name, headerKey) && contains(header.Value) {
			return true
		}
	}
	return false
}

// literalsPartiallyOverlapInLocation reports whether two distinct literals can
// occupy one merged region in the addressed request field. A bare
// suffix/prefix character match without a real merged occurrence is not a
// collision: single-pass replacement rewrites disjoint occurrences correctly.
func literalsPartiallyOverlapInLocation(script *dsl.Script, location Location, left, right string, budget *LiteralOverlapSearchBudget) bool {
	for _, value := range locationStringValues(script, location) {
		remaining := value
		for remaining != "" {
			start := strings.Index(remaining, "{{")
			if start < 0 {
				if budget.LiteralsPartiallyOverlapInSurface(remaining, left, right) {
					return true
				}
				break
			}
			if budget.LiteralsPartiallyOverlapInSurface(remaining[:start], left, right) {
				return true
			}
			endOffset := strings.Index(remaining[start+2:], "}}")
			if endOffset < 0 {
				break
			}
			remaining = remaining[start+2+endOffset+2:]
		}
	}
	return false
}

func locationStringValues(script *dsl.Script, location Location) []string {
	action := actionAt(script, location.Section, location.ActionIndex)
	if action == nil {
		return nil
	}
	if value, ok := multipartFieldValue(action, location.Field); ok {
		return []string{*value}
	}
	switch location.Field {
	case "target":
		return []string{action.Target}
	case "body":
		return []string{action.Body}
	case "value":
		return []string{action.Value}
	}
	if paramKey, isParam := strings.CutPrefix(location.Field, "params."); isParam {
		if value, ok := action.Params[paramKey].(string); ok {
			return []string{value}
		}
		return nil
	}
	headerKey, isHeader := strings.CutPrefix(strings.ToLower(location.Field), "headers.")
	if !isHeader {
		return nil
	}
	values := make([]string, 0, 1)
	for key, value := range action.Headers {
		if strings.EqualFold(key, headerKey) {
			values = append(values, value)
		}
	}
	for _, header := range action.HeaderList {
		if strings.EqualFold(header.Name, headerKey) {
			values = append(values, header.Value)
		}
	}
	return values
}

// maxLiteralOverlapSearchWork bounds bytes compared or scanned in one
// collision-analysis pass. The work is linear byte scanning (strings.Index
// runs at GB/s), so this is well under a second; 64 MiB could not index the
// ~100 claim literals of an ordinary 1 MB Salesforce capture once each.
const maxLiteralOverlapSearchWork int64 = 512 << 20

// LiteralOverlapSearchBudget bounds aggregate suffix comparisons and request
// surface searches across a collision-analysis pass. Exhaustion is fail-closed:
// LiteralsPartiallyOverlapInSurface returns true so callers suppress automatic
// rewrites rather than accepting an incompletely checked pair.
type LiteralOverlapSearchBudget struct {
	remaining int64
	exhausted bool
}

func NewLiteralOverlapSearchBudget() *LiteralOverlapSearchBudget {
	return &LiteralOverlapSearchBudget{remaining: maxLiteralOverlapSearchWork}
}

func (budget *LiteralOverlapSearchBudget) Exhausted() bool {
	return budget == nil || budget.exhausted
}

func (budget *LiteralOverlapSearchBudget) charge(work int) bool {
	if budget == nil || work < 0 || int64(work) > budget.remaining {
		if budget != nil {
			budget.exhausted = true
			budget.remaining = 0
		}
		return false
	}
	budget.remaining -= int64(work)
	return true
}

// Contains performs a budgeted containment check. Exhaustion returns true so
// collision callers fail closed, matching the partial-overlap predicate.
func (budget *LiteralOverlapSearchBudget) Contains(haystack, needle string) bool {
	if !budget.charge(len(haystack)) {
		return true
	}
	return strings.Contains(haystack, needle)
}

// LiteralsPartiallyOverlapInSurface reports whether either literal begins
// inside the other and both occupy one real merged region in surface. It does
// not materialize the potentially quadratic set of merged spellings.
func (budget *LiteralOverlapSearchBudget) LiteralsPartiallyOverlapInSurface(surface, left, right string) bool {
	if surface == "" {
		return false
	}
	return budget.LiteralsPartiallyOverlapInSurfaces([]string{surface}, left, right)
}

// LiteralsPartiallyOverlapInSurfaces applies LiteralsPartiallyOverlapInSurface
// to separate editable fields, computing the pair's overlap lengths once.
func (budget *LiteralOverlapSearchBudget) LiteralsPartiallyOverlapInSurfaces(surfaces []string, left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	for _, pair := range [2][2]string{{left, right}, {right, left}} {
		first, second := pair[0], pair[1]
		failure, matched, ok := budget.suffixPrefixBorders(first, second)
		if !ok {
			return true
		}
		limit := min(len(first), len(second))
		for overlap := matched; overlap > 0; overlap = failure[overlap-1] {
			if overlap >= limit {
				continue
			}
			for _, surface := range surfaces {
				if surface != "" && budget.surfaceContainsConcatenation(surface, first, second[overlap:]) {
					return true
				}
			}
		}
	}
	return false
}

// suffixPrefixBorders computes the prefix function of second and the longest
// prefix of second that is a suffix of first. Following failure from matched
// visits every overlap length k where first's k-byte suffix equals second's
// k-byte prefix, in linear work; trying each candidate length separately is
// quadratic and exhausted the budget on ordinary Salesforce captures holding a
// few long tokens.
func (budget *LiteralOverlapSearchBudget) suffixPrefixBorders(first, second string) ([]int, int, bool) {
	if min(len(first), len(second)) <= 1 {
		return nil, 0, true
	}
	if !budget.charge(len(first) + 2*len(second)) {
		return nil, 0, false
	}
	failure := make([]int, len(second))
	for index, matched := 1, 0; index < len(second); index++ {
		for matched > 0 && second[index] != second[matched] {
			matched = failure[matched-1]
		}
		if second[index] == second[matched] {
			matched++
		}
		failure[index] = matched
	}
	matched := 0
	for index := 0; index < len(first); index++ {
		if matched == len(second) {
			matched = failure[matched-1]
		}
		for matched > 0 && first[index] != second[matched] {
			matched = failure[matched-1]
		}
		if first[index] == second[matched] {
			matched++
		}
	}
	return failure, matched, true
}

func (budget *LiteralOverlapSearchBudget) surfaceContainsConcatenation(surface, first, tail string) bool {
	if len(first) > len(surface) || len(tail) > len(surface)-len(first) {
		return false
	}
	maxStart := len(surface) - len(first) - len(tail)
	searchOffset := 0
	searchEnd := maxStart + len(first)
	for searchOffset <= maxStart {
		window := surface[searchOffset:searchEnd]
		relative := strings.Index(window, first)
		// Charge only the bytes the search consumed. Charging the whole
		// remaining window for every occurrence made a literal repeated in
		// each Aura request quadratic in the surface size.
		scanned := len(window)
		if relative >= 0 {
			scanned = relative + len(first)
		}
		if !budget.charge(scanned) {
			return true
		}
		if relative < 0 {
			return false
		}
		start := searchOffset + relative
		if !budget.charge(len(tail)) {
			return true
		}
		if surface[start+len(first):start+len(first)+len(tail)] == tail {
			return true
		}
		searchOffset = start + 1
	}
	return false
}

func containsOutsideTemplates(value, literal string) bool {
	remaining := value
	for remaining != "" {
		start := strings.Index(remaining, "{{")
		if start < 0 {
			return strings.Contains(remaining, literal)
		}
		if strings.Contains(remaining[:start], literal) {
			return true
		}
		endOffset := strings.Index(remaining[start+2:], "}}")
		if endOffset < 0 {
			return false
		}
		remaining = remaining[start+2+endOffset+2:]
	}
	return false
}

func applyReplacements(script *dsl.Script, suggestion Suggestion) []Diff {
	type fieldKey struct {
		section     string
		actionIndex int
		field       string
	}
	type fieldReplacements struct {
		location     Location
		replacements map[string]string
	}
	groups := make(map[fieldKey]*fieldReplacements, len(suggestion.Locations))
	order := make([]fieldKey, 0, len(suggestion.Locations))
	for _, location := range suggestion.Locations {
		if hasAuraLocation(location) {
			continue
		}
		if location.SearchText == "" {
			continue
		}
		key := fieldKey{section: location.Section, actionIndex: location.ActionIndex, field: location.Field}
		group, exists := groups[key]
		if !exists {
			group = &fieldReplacements{location: location, replacements: make(map[string]string)}
			groups[key] = group
			order = append(order, key)
		}
		if _, exists := group.replacements[location.SearchText]; !exists {
			group.replacements[location.SearchText] = location.ReplaceText
		}
	}

	diffs := make([]Diff, 0, len(order))
	for _, key := range order {
		group := groups[key]
		action := actionAt(script, group.location.Section, group.location.ActionIndex)
		if action == nil {
			continue
		}
		before, after, ok := replaceInFieldValues(action, group.location, group.replacements)
		if !ok {
			continue
		}
		diffs = append(diffs, Diff{
			SuggestionID: suggestion.ID,
			Kind:         "value_replaced",
			Path:         fmt.Sprintf("/%s/%d/%s", group.location.Section, group.location.ActionIndex, strings.ReplaceAll(group.location.Field, ".", "/")),
			Before:       truncateValue(before),
			After:        truncateValue(after),
		})
	}
	return diffs
}

// replaceInField substitutes location.SearchText in the addressed field
// ("target", "body", "value", or "headers.<Name>") and reports the before and
// after values when a replacement happened.
func replaceInField(action *dsl.Action, location Location) (before, after string, ok bool) {
	return replaceInFieldValues(action, location, map[string]string{location.SearchText: location.ReplaceText})
}

func replaceInFieldValues(action *dsl.Action, location Location, replacements map[string]string) (before, after string, ok bool) {
	replace := func(current string) (string, bool) {
		updated := replaceOutsideTemplates(current, replacements)
		return updated, updated != current
	}
	if value, isMultipart := multipartFieldValue(action, location.Field); isMultipart {
		before = *value
		if updated, changed := replace(*value); changed {
			*value = updated
			return before, updated, true
		}
		return "", "", false
	}
	switch location.Field {
	case "target":
		before = action.Target
		if updated, changed := replace(action.Target); changed {
			action.Target = updated
			return before, updated, true
		}
	case "body":
		before = action.Body
		if updated, changed := replace(action.Body); changed {
			action.Body = updated
			return before, updated, true
		}
	case "value":
		before = action.Value
		if updated, changed := replace(action.Value); changed {
			action.Value = updated
			return before, updated, true
		}
	default:
		if paramKey, isParam := strings.CutPrefix(location.Field, "params."); isParam {
			raw, exists := action.Params[paramKey]
			if !exists {
				return "", "", false
			}
			current, isStr := raw.(string)
			if !isStr {
				return "", "", false
			}
			if updated, changed := replace(current); changed {
				action.Params[paramKey] = updated
				return current, updated, true
			}
			return "", "", false
		}
		headerKey, isHeader := strings.CutPrefix(location.Field, "headers.")
		if !isHeader {
			return "", "", false
		}
		return replaceHeader(action, headerKey, replace)
	}
	return "", "", false
}

// replaceOutsideTemplates applies every literal mapping in one non-recursive
// pass and never scans existing {{...}} expressions. This prevents one encoded
// spelling from rewriting output inserted for another spelling, and prevents a
// later accepted suggestion from corrupting a variable template added earlier.
func replaceOutsideTemplates(value string, replacements map[string]string) string {
	if value == "" || len(replacements) == 0 {
		return value
	}
	searches := make([]string, 0, len(replacements))
	for search := range replacements {
		if search != "" {
			searches = append(searches, search)
		}
	}
	if len(searches) == 0 {
		return value
	}
	sort.Slice(searches, func(i, j int) bool {
		if len(searches[i]) != len(searches[j]) {
			return len(searches[i]) > len(searches[j])
		}
		return searches[i] < searches[j]
	})
	var result strings.Builder
	remaining := value
	for remaining != "" {
		start := strings.Index(remaining, "{{")
		if start < 0 {
			result.WriteString(replaceLiteralMappings(remaining, searches, replacements))
			break
		}
		result.WriteString(replaceLiteralMappings(remaining[:start], searches, replacements))
		endOffset := strings.Index(remaining[start+2:], "}}")
		if endOffset < 0 {
			// Preserve an unterminated template-like suffix. Rewriting it is more
			// likely to corrupt author input than to parameterize a request literal.
			result.WriteString(remaining[start:])
			break
		}
		end := start + 2 + endOffset + 2
		result.WriteString(remaining[start:end])
		remaining = remaining[end:]
	}
	return result.String()
}

// replaceLiteralMappings is the non-template segment counterpart of
// strings.NewReplacer. Query/form locations deliberately include delimiters to
// avoid matching a key suffix (id inside user_id). Adjacent pairs share their
// ampersand, so consuming a matched trailing delimiter would make the next
// location unmatchable. Emit that shared delimiter on the next scan instead.
func replaceLiteralMappings(value string, searches []string, replacements map[string]string) string {
	var result strings.Builder
	remaining := value
	for remaining != "" {
		matchAt := -1
		matched := ""
		for _, search := range searches {
			at := strings.Index(remaining, search)
			if at < 0 {
				continue
			}
			if matchAt < 0 || at < matchAt {
				matchAt = at
				matched = search
			}
		}
		if matchAt < 0 {
			result.WriteString(remaining)
			break
		}
		result.WriteString(remaining[:matchAt])
		replacement := replacements[matched]
		advance := len(matched)
		if len(matched) > 1 && len(replacement) > 0 {
			last := matched[len(matched)-1]
			if (last == '&' || last == '#') && replacement[len(replacement)-1] == last {
				// Do not consume the delimiter: another exact pair may begin there.
				result.WriteString(replacement[:len(replacement)-1])
				advance--
			} else {
				result.WriteString(replacement)
			}
		} else {
			result.WriteString(replacement)
		}
		remaining = remaining[matchAt+advance:]
	}
	return result.String()
}

func replaceHeader(action *dsl.Action, headerKey string, replace func(string) (string, bool)) (before, after string, ok bool) {
	if action == nil {
		return "", "", false
	}
	firstBefore := ""
	lastAfter := ""
	mapKey := headerKey
	changed := false
	if action.Headers != nil {
		matchingKeys := make([]string, 0, 1)
		for _, candidate := range sortedKeys(action.Headers) {
			if strings.EqualFold(candidate, headerKey) {
				matchingKeys = append(matchingKeys, candidate)
			}
		}
		for _, candidate := range matchingKeys {
			if candidate == headerKey {
				mapKey = candidate
				break
			}
			if mapKey == headerKey {
				mapKey = candidate
			}
		}
		for _, candidate := range matchingKeys {
			current := action.Headers[candidate]
			if updated, didChange := replace(current); didChange {
				action.Headers[candidate] = updated
				if firstBefore == "" {
					firstBefore = current
				}
				lastAfter = updated
				changed = true
			}
		}
	}
	matchedHeaderList := false
	for i := range action.HeaderList {
		if !strings.EqualFold(action.HeaderList[i].Name, headerKey) {
			continue
		}
		matchedHeaderList = true
		if updated, didChange := replace(action.HeaderList[i].Value); didChange {
			if firstBefore == "" {
				firstBefore = action.HeaderList[i].Value
			}
			action.HeaderList[i].Value = updated
			changed = true
		}
		// Headers is only a compatibility summary of HeaderList. Keep it equal
		// to the last wire-order entry even when this particular replacement
		// changed an earlier duplicate and left the final duplicate untouched.
		lastAfter = action.HeaderList[i].Value
	}
	if changed {
		// An empty replacement is still a real change: the loops above already
		// mutated Headers/HeaderList, so gating on lastAfter != "" would report no
		// change, skip the diff, and leave the Headers map out of sync with the
		// now-empty HeaderList value.
		if action.Headers == nil {
			action.Headers = map[string]string{}
		}
		if !matchedHeaderList && lastAfter == "" {
			lastAfter = action.Headers[mapKey]
		}
		if !matchedHeaderList {
			return firstBefore, lastAfter, true
		}
		// HeaderList is authoritative. Collapse any case-only compatibility map
		// variants to one summary instead of leaving a stale duplicate that a
		// downstream map consumer could select nondeterministically.
		for candidate := range action.Headers {
			if strings.EqualFold(candidate, headerKey) && candidate != mapKey {
				delete(action.Headers, candidate)
			}
		}
		action.Headers[mapKey] = lastAfter
		return firstBefore, lastAfter, true
	}
	return "", "", false
}

func appendExtractor(script *dsl.Script, suggestion Suggestion) (Diff, bool) {
	if suggestion.Extractor == nil || suggestion.FoundInAction == nil {
		return Diff{}, false
	}
	action := actionAt(script, suggestion.FoundInAction.Section, suggestion.FoundInAction.ActionIndex)
	if action == nil {
		return Diff{}, false
	}
	for _, existing := range action.Extractors {
		if existing.Name == suggestion.Extractor.Name {
			return Diff{}, false
		}
	}
	action.Extractors = append(action.Extractors, *suggestion.Extractor)
	return Diff{
		SuggestionID: suggestion.ID,
		Kind:         "extractor_added",
		Path:         fmt.Sprintf("/%s/%d/extractors", suggestion.FoundInAction.Section, suggestion.FoundInAction.ActionIndex),
		After:        fmt.Sprintf("%s (%s %s)", suggestion.Extractor.Name, suggestion.Extractor.Type, suggestion.Extractor.Expression),
	}, true
}

// materializeDataSource creates one inline CSV data source seeded with the
// recorded literals (one row), following the enterprise-archive importer's
// inline data-source pattern. A free name is chosen if the base name is taken.
func materializeDataSource(script *dsl.Script, columnOrder []string, seeds map[string]string) (string, Diff) {
	name := consolidatedDataSourceName
	taken := map[string]bool{}
	for _, source := range script.DataSources {
		taken[source.Name] = true
	}
	for i := 2; taken[name]; i++ {
		name = fmt.Sprintf("%s_%d", consolidatedDataSourceName, i)
	}

	values := make([]string, 0, len(columnOrder))
	for _, column := range columnOrder {
		values = append(values, csvEscape(seeds[column]))
	}
	script.DataSources = append(script.DataSources, dsl.DataSource{
		Name:           name,
		Type:           "csv",
		InlineData:     strings.Join(columnOrder, ",") + "\n" + strings.Join(values, ","),
		Strategy:       "sequential",
		FirstRowHeader: true,
	})
	return name, Diff{
		Kind:  "data_source_added",
		Path:  "/data_sources",
		After: name + " (" + strings.Join(columnOrder, ", ") + ")",
	}
}

func materializeParameters(script *dsl.Script, suggestions []Suggestion, selected map[string]bool, sourceName string) []Diff {
	existing := map[string]bool{}
	for _, param := range script.Parameters {
		existing[param.Name] = true
	}
	diffs := make([]Diff, 0)
	for _, suggestion := range suggestions {
		if !selected[suggestion.ID] || suggestion.Kind != KindDataParam || suggestion.Parameter == nil {
			continue
		}
		if existing[suggestion.Parameter.Name] {
			continue
		}
		param := *suggestion.Parameter
		param.DataSource = sourceName
		script.Parameters = append(script.Parameters, param)
		existing[param.Name] = true
		diffs = append(diffs, Diff{
			SuggestionID: suggestion.ID,
			Kind:         "parameter_added",
			Path:         "/parameters",
			After:        fmt.Sprintf("%s -> %s.%s", param.Name, sourceName, param.Column),
		})
	}
	sort.SliceStable(diffs, func(i, j int) bool { return diffs[i].After < diffs[j].After })
	return diffs
}

// materializeGeneratedParameter declares the parameter a generated suggestion
// references, unless the script already has one by that name.
func materializeGeneratedParameter(script *dsl.Script, suggestion Suggestion) (Diff, bool) {
	if suggestion.Parameter == nil {
		return Diff{}, false
	}
	for _, param := range script.Parameters {
		if strings.EqualFold(param.Name, suggestion.Parameter.Name) {
			return Diff{}, false
		}
	}
	param := *suggestion.Parameter
	script.Parameters = append(script.Parameters, param)
	when := "new each iteration"
	if param.Update == "each_occurrence" {
		when = "new each time it is sent"
	}
	return Diff{
		SuggestionID: suggestion.ID,
		Kind:         "parameter_added",
		Path:         "/parameters",
		After:        fmt.Sprintf("%s -> %s, %s", param.Name, param.Type, when),
	}, true
}

func csvEscape(value string) string {
	if strings.ContainsAny(value, ",\"\n") {
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return value
}

func cloneScript(in *dsl.Script) (*dsl.Script, error) {
	data, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var out dsl.Script
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReplaceOutsideTemplates exposes the template-aware literal replacement used
// by ApplySuggestions so the cross-script pass in pkg/convert/sharedvalue can
// share one rule set. Getting this wrong corrupts previously inserted
// templates, so it must not be reimplemented elsewhere.
func ReplaceOutsideTemplates(value string, replacements map[string]string) string {
	return replaceOutsideTemplates(value, replacements)
}

// multipartFieldPrefix addresses the text value of a body_multipart part:
// "body_multipart.<index>".
const multipartFieldPrefix = "body_multipart."

// multipartFieldValue returns the text value a "body_multipart.<index>"
// location addresses.
func multipartFieldValue(action *dsl.Action, field string) (*string, bool) {
	raw, ok := strings.CutPrefix(field, multipartFieldPrefix)
	if !ok || action == nil || action.BodyMultipart == nil {
		return nil, false
	}
	index, err := strconv.Atoi(raw)
	if err != nil || index < 0 || index >= len(action.BodyMultipart.Parts) {
		return nil, false
	}
	part := &action.BodyMultipart.Parts[index]
	if strings.EqualFold(strings.TrimSpace(part.ValueEncoding), "base64") {
		return nil, false
	}
	return &part.Value, true
}
