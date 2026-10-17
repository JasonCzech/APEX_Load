package importreview

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"io"
	"reflect"
	"sort"
	"strings"
)

func SourceSecretValuesForImportNodes(rawValue, projectedValue any, requirements []SecretBindingRequirement) map[string]string {
	rawNodes := make([]map[string]any, 0)
	CollectScriptJSONNodes(rawValue, &rawNodes)
	if len(rawNodes) == 0 {
		return nil
	}

	projectedNodes := make([]map[string]any, 0, len(rawNodes))
	CollectScriptJSONNodes(projectedValue, &projectedNodes)
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
			raw, rawOK := JsonPointerString(rawNodes[requirement.ScriptIndex], requirement.Path)
			projected, projectedOK := JsonPointerString(projectedNodes[requirement.ScriptIndex], requirement.Path)
			if rawOK && projectedOK {
				segments, _ = ExtractProjectedRedactions(raw, projected)
			}
			byPath[requirement.Path] = segments
		}
		if requirement.Occurrence < 0 || requirement.Occurrence >= len(segments) {
			continue
		}
		value := segments[requirement.Occurrence]
		if _, recoverable := credentialpolicy.RecoverableSourceCredentialValue(value); !recoverable {
			continue
		}
		values[requirement.ID] = value
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func ImportResponseRawScriptValue(value any) any {
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

func JsonPointerString(root any, path string) (string, bool) {
	tokens, err := SplitJSONPointer(path)
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
			index, ok := DecimalTokenIndex(token, len(typed))
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

func DecimalTokenIndex(value string, length int) (int, bool) {
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

const MaxProjectedRedactionAlignmentWork = 8 << 20

const ProjectedRedactionCandidateWork = 64

type ProjectedRedactionAlignment struct {
	cursor   int
	count    uint8
	previous *ProjectedRedactionAlignment
	value    string
}

func ExtractProjectedRedactions(raw, projected string) ([]string, bool) {
	if values, ok := ExtractLinearlyProjectedRedactions(raw, projected); ok {
		return values, true
	}
	// A JSON body whose projection does not line up byte for byte (a nested
	// JSON string the projection had to re-encode whole because its spans
	// could not be spliced) can still pair its sentinels with recorded values
	// by structure, without weakening the unique-alignment rule for free-form
	// strings. Such values can be kept or migrated but not rebound: binding
	// splices with the linear alignment.
	return ExtractStructurallyProjectedJSONRedactions(raw, projected)
}

func ExtractLinearlyProjectedRedactions(raw, projected string) ([]string, bool) {
	projected = ProjectedMessageIDsAsRecorded(raw, projected)
	parts := strings.Split(projected, MaskedScriptSecret)
	if len(parts) < 2 || !strings.HasPrefix(raw, parts[0]) {
		return nil, false
	}
	// latest[i] is the last position parts[i] can begin at and still leave
	// room for every later literal, the last one ending the scalar. An
	// occurrence after it begins only paths that never reach the end, so a
	// stage scans just from the earliest cursor a path reaches to there, and a
	// unique alignment costs about three passes over the scalar (this one, the
	// stages' scans and their literal comparisons) however many sentinels it
	// has; scanning the whole rest of the scalar per sentinel refused large
	// recorded bodies carrying many credentials.
	latest := make([]int, len(parts))
	latest[len(parts)-1] = len(raw) - len(parts[len(parts)-1])
	for index := len(parts) - 2; index >= 1; index-- {
		if latest[index+1] < 0 {
			return nil, false
		}
		latest[index] = strings.LastIndex(raw[:latest[index+1]], parts[index])
	}
	// Bound the bytes scanned and compared, and the candidate states
	// allocated, so an adversarial import cannot turn ambiguity proof into
	// unbounded CPU or memory. A unique alignment needs those three passes and
	// a state per sentinel, so the bound grows with the scalar and only
	// ambiguous input reaches it.
	work, budget := len(raw), MaxProjectedRedactionAlignmentWork+4*len(raw)
	states := []*ProjectedRedactionAlignment{{cursor: len(parts[0]), count: 1}}
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
			var unique *ProjectedRedactionAlignment
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
			states = []*ProjectedRedactionAlignment{{
				cursor: len(raw), count: 1, previous: unique, value: raw[unique.cursor:],
			}}
			continue
		}

		// Candidate literal positions and prior cursors are ordered. A prefix sum
		// gives the number of valid paths reaching each position, capped at two;
		// linked unique predecessors permit reconstruction without copying paths.
		nextStates := make([]*ProjectedRedactionAlignment, 0)
		stateIndex := 0
		var prefixCount uint8
		var uniquePrefix *ProjectedRedactionAlignment
		searchFrom, last := states[0].cursor, latest[index+1]
		if index == len(parts)-2 {
			searchFrom = max(searchFrom, last)
		}
		if work += max(0, last-searchFrom) + 1; work > budget {
			return nil, false
		}
		for searchFrom <= last {
			relative := strings.Index(raw[searchFrom:last+len(nextLiteral)], nextLiteral)
			if relative < 0 {
				break
			}
			position := searchFrom + relative
			searchFrom = position + 1 // retain overlapping literal occurrences
			if work += len(nextLiteral) + ProjectedRedactionCandidateWork; work > budget {
				return nil, false
			}
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
			next := &ProjectedRedactionAlignment{cursor: position + len(nextLiteral), count: prefixCount}
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

func ExtractStructurallyProjectedJSONRedactions(raw, projected string) ([]string, bool) {
	if len(raw) > MaxProjectedRedactionAlignmentWork || len(projected) > MaxProjectedRedactionAlignmentWork {
		return nil, false
	}
	projected = ProjectedMessageIDsAsRecorded(raw, projected)
	rawValue, rawOK := DecodeStructuredJSONValue(raw)
	projectedValue, projectedOK := DecodeStructuredJSONValue(projected)
	if !rawOK || !projectedOK {
		return nil, false
	}
	values, ok := CollectStructurallyProjectedJSONRedactions(rawValue, projectedValue)
	if !ok || len(values) != strings.Count(projected, MaskedScriptSecret) {
		return nil, false
	}
	return values, true
}

func DecodeStructuredJSONValue(value string) (any, bool) {
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

func CollectStructurallyProjectedJSONRedactions(raw, projected any) ([]string, bool) {
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
			child, childOK := CollectStructurallyProjectedJSONRedactions(rawValue[key], projectedValue[key])
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
			child, childOK := CollectStructurallyProjectedJSONRedactions(rawValue[index], projectedValue[index])
			if !childOK {
				return nil, false
			}
			values = append(values, child...)
		}
		return values, true
	case string:
		if strings.Contains(projectedValue, MaskedScriptSecret) {
			switch rawValue := raw.(type) {
			case string:
				return ExtractLinearlyProjectedRedactions(rawValue, projectedValue)
			default:
				if projectedValue != MaskedScriptSecret {
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
