package api

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
)

// sourceSecretValuesForImportResponse maps disclosure-safe requirement IDs to
// their original source values without ever serializing those values. Script
// ordering mirrors secretBindingRequirementsForImportScripts.
func sourceSecretValuesForImportResponse(opts scriptImportResponseOptions, requirements []SecretBindingRequirement) map[string]string {
	rawValue := importResponseRawScriptValue(opts.Scripts)
	if !hasScriptJSONNode(rawValue) {
		rawValue = importResponseRawScriptValue(opts.Script)
	}
	projectedValue := maskScriptResponseValue(rawValue)
	return sourceSecretValuesForImportNodes(rawValue, projectedValue, requirements)
}

func sourceSecretValuesForImportNodes(rawValue, projectedValue any, requirements []SecretBindingRequirement) map[string]string {
	rawNodes := make([]map[string]any, 0)
	collectScriptJSONNodes(rawValue, &rawNodes)
	if len(rawNodes) == 0 {
		return nil
	}

	projectedNodes := make([]map[string]any, 0, len(rawNodes))
	collectScriptJSONNodes(projectedValue, &projectedNodes)
	if len(projectedNodes) != len(rawNodes) {
		return nil
	}

	segmentsByScriptAndPath := make(map[int]map[string][]string)
	values := make(map[string]string)
	for _, requirement := range requirements {
		if requirement.ScriptIndex < 0 || requirement.ScriptIndex >= len(rawNodes) {
			continue
		}
		byPath := segmentsByScriptAndPath[requirement.ScriptIndex]
		if byPath == nil {
			byPath = make(map[string][]string)
			segmentsByScriptAndPath[requirement.ScriptIndex] = byPath
		}
		segments, cached := byPath[requirement.Path]
		if !cached {
			raw, rawOK := jsonPointerString(rawNodes[requirement.ScriptIndex], requirement.Path)
			projected, projectedOK := jsonPointerString(projectedNodes[requirement.ScriptIndex], requirement.Path)
			if rawOK && projectedOK {
				segments, _ = extractProjectedRedactions(raw, projected)
			}
			byPath[requirement.Path] = segments
		}
		if requirement.Occurrence < 0 || requirement.Occurrence >= len(segments) {
			continue
		}
		value := segments[requirement.Occurrence]
		if value == "" {
			continue
		}
		values[requirement.ID] = value
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func sourceSecretProjectID(opts scriptImportResponseOptions) string {
	for _, candidate := range []any{opts.Script, opts.Scripts} {
		raw := importResponseRawScriptValue(candidate)
		nodes := make([]map[string]any, 0, 1)
		collectScriptJSONNodes(raw, &nodes)
		for _, node := range nodes {
			if projectID, _ := node["project_id"].(string); strings.TrimSpace(projectID) != "" {
				return normalizeProjectID(projectID)
			}
		}
	}
	return ""
}

func sourceSecretProjectIDForRequest(r *http.Request, opts scriptImportResponseOptions) (string, bool) {
	projectID := sourceSecretProjectID(opts)
	if authorizedProjectID, scoped := authorizedProjectFromRequest(r); scoped {
		if projectID != "" && normalizeProjectID(projectID) != authorizedProjectID {
			return authorizedProjectID, false
		}
		return authorizedProjectID, true
	}
	if projectID == "" && r != nil {
		projectID = strings.TrimSpace(r.URL.Query().Get("project_id"))
	}
	return normalizeProjectID(projectID), true
}

func importResponseRawScriptValue(value any) any {
	if value == nil {
		return nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var raw any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return value
	}
	return raw
}

func jsonPointerString(root any, path string) (string, bool) {
	tokens, err := splitJSONPointer(path)
	if err != nil {
		return "", false
	}
	current := root
	for _, token := range tokens {
		switch typed := current.(type) {
		case map[string]any:
			child, ok := typed[token]
			if !ok {
				return "", false
			}
			current = child
		case []any:
			index, ok := decimalTokenIndex(token, len(typed))
			if !ok {
				return "", false
			}
			current = typed[index]
		default:
			return "", false
		}
	}
	value, ok := current.(string)
	return value, ok
}

func decimalTokenIndex(value string, length int) (int, bool) {
	if value == "" {
		return 0, false
	}
	index := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
		index = index*10 + int(r-'0')
		if index >= length {
			return 0, false
		}
	}
	return index, index >= 0 && index < length
}

const maxProjectedRedactionAlignmentWork = 8 << 20

type projectedRedactionAlignment struct {
	cursor   int
	count    uint8
	previous *projectedRedactionAlignment
	value    string
}

// extractProjectedRedactions aligns the fixed text surrounding projected
// sentinels with the raw scalar and returns each removed source segment. It
// succeeds only when exactly one segmentation exists. Picking the first matching
// delimiter can silently bind a credential to the wrong logical requirement
// when credential material itself contains that delimiter.
func extractProjectedRedactions(raw, projected string) ([]string, bool) {
	if values, ok := extractLinearlyProjectedRedactions(raw, projected); ok {
		return values, true
	}
	// JSON request bodies are structurally projected and then marshaled, which
	// intentionally canonicalizes whitespace and object-key order. Fall back to
	// a structural comparison so ordinary formatted JSON remains migratable
	// without weakening the unique-alignment rule for free-form strings.
	return extractStructurallyProjectedJSONRedactions(raw, projected)
}

func extractLinearlyProjectedRedactions(raw, projected string) ([]string, bool) {
	parts := strings.Split(projected, maskedScriptSecret)
	if len(parts) < 2 || !strings.HasPrefix(raw, parts[0]) {
		return nil, false
	}
	// Each DP stage scans the scalar at most once. Bound the product so an
	// adversarial import cannot turn ambiguity proof into unbounded CPU work.
	if len(raw) > 0 && len(parts) > maxProjectedRedactionAlignmentWork/len(raw) {
		return nil, false
	}
	states := []*projectedRedactionAlignment{{cursor: len(parts[0]), count: 1}}
	for index := 0; index < len(parts)-1; index++ {
		nextLiteral := parts[index+1]
		if nextLiteral == "" && index < len(parts)-2 {
			// Adjacent sentinels have no stable boundary in the raw scalar.
			// Treat them as unavailable instead of assigning source material to
			// the wrong logical requirement.
			return nil, false
		}
		if nextLiteral == "" && index == len(parts)-2 {
			var total uint8
			var unique *projectedRedactionAlignment
			for _, state := range states {
				if state.cursor > len(raw) {
					continue
				}
				if total == 0 && state.count == 1 {
					total, unique = 1, state
				} else if state.count > 0 {
					total, unique = 2, nil
				}
			}
			if total != 1 || unique == nil {
				return nil, false
			}
			states = []*projectedRedactionAlignment{{
				cursor: len(raw), count: 1, previous: unique, value: raw[unique.cursor:],
			}}
			continue
		}

		// Candidate literal positions and prior cursors are ordered. A prefix sum
		// gives the number of valid paths reaching each position, capped at two;
		// linked unique predecessors permit reconstruction without copying paths.
		nextStates := make([]*projectedRedactionAlignment, 0)
		stateIndex := 0
		var prefixCount uint8
		var uniquePrefix *projectedRedactionAlignment
		searchFrom := 0
		for searchFrom <= len(raw)-len(nextLiteral) {
			relative := strings.Index(raw[searchFrom:], nextLiteral)
			if relative < 0 {
				break
			}
			position := searchFrom + relative
			searchFrom = position + 1 // retain overlapping literal occurrences
			if index == len(parts)-2 && position+len(nextLiteral) != len(raw) {
				continue
			}
			for stateIndex < len(states) && states[stateIndex].cursor <= position {
				state := states[stateIndex]
				stateIndex++
				if prefixCount == 0 && state.count == 1 {
					prefixCount, uniquePrefix = 1, state
				} else if state.count > 0 {
					prefixCount, uniquePrefix = 2, nil
				}
			}
			if prefixCount == 0 {
				continue
			}
			next := &projectedRedactionAlignment{cursor: position + len(nextLiteral), count: prefixCount}
			if prefixCount == 1 && uniquePrefix != nil {
				next.previous = uniquePrefix
				next.value = raw[uniquePrefix.cursor:position]
			}
			nextStates = append(nextStates, next)
		}
		if len(nextStates) == 0 {
			return nil, false
		}
		states = nextStates
	}
	if len(states) != 1 || states[0].cursor != len(raw) || states[0].count != 1 {
		return nil, false
	}
	values := make([]string, len(parts)-1)
	state := states[0]
	for index := len(values) - 1; index >= 0; index-- {
		if state == nil || state.previous == nil {
			return nil, false
		}
		values[index] = state.value
		state = state.previous
	}
	return values, true
}

func extractStructurallyProjectedJSONRedactions(raw, projected string) ([]string, bool) {
	if len(raw) > maxProjectedRedactionAlignmentWork || len(projected) > maxProjectedRedactionAlignmentWork {
		return nil, false
	}
	rawValue, rawOK := decodeStructuredJSONValue(raw)
	projectedValue, projectedOK := decodeStructuredJSONValue(projected)
	if !rawOK || !projectedOK {
		return nil, false
	}
	values, ok := collectStructurallyProjectedJSONRedactions(rawValue, projectedValue)
	if !ok || len(values) != strings.Count(projected, maskedScriptSecret) {
		return nil, false
	}
	return values, true
}

func decodeStructuredJSONValue(value string) (any, bool) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false
	}
	switch decoded.(type) {
	case map[string]any, []any:
		return decoded, true
	default:
		return nil, false
	}
}

func collectStructurallyProjectedJSONRedactions(raw, projected any) ([]string, bool) {
	switch projectedValue := projected.(type) {
	case map[string]any:
		rawValue, ok := raw.(map[string]any)
		if !ok || len(rawValue) != len(projectedValue) {
			return nil, false
		}
		keys := make([]string, 0, len(projectedValue))
		for key := range projectedValue {
			if _, exists := rawValue[key]; !exists {
				return nil, false
			}
			keys = append(keys, key)
		}
		// encoding/json marshals object keys lexicographically, so this is also
		// the sentinel occurrence order used by the binding requirements.
		sort.Strings(keys)
		values := make([]string, 0)
		for _, key := range keys {
			child, childOK := collectStructurallyProjectedJSONRedactions(rawValue[key], projectedValue[key])
			if !childOK {
				return nil, false
			}
			values = append(values, child...)
		}
		return values, true
	case []any:
		rawValue, ok := raw.([]any)
		if !ok || len(rawValue) != len(projectedValue) {
			return nil, false
		}
		values := make([]string, 0)
		for index := range projectedValue {
			child, childOK := collectStructurallyProjectedJSONRedactions(rawValue[index], projectedValue[index])
			if !childOK {
				return nil, false
			}
			values = append(values, child...)
		}
		return values, true
	case string:
		if strings.Contains(projectedValue, maskedScriptSecret) {
			switch rawValue := raw.(type) {
			case string:
				return extractLinearlyProjectedRedactions(rawValue, projectedValue)
			default:
				if projectedValue != maskedScriptSecret {
					return nil, false
				}
				payload, err := json.Marshal(rawValue)
				if err != nil || string(payload) == "null" {
					return nil, false
				}
				return []string{string(payload)}, true
			}
		}
		rawValue, ok := raw.(string)
		return nil, ok && rawValue == projectedValue
	default:
		return nil, reflect.DeepEqual(raw, projected)
	}
}
