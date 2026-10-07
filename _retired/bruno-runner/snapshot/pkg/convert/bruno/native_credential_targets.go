package bruno

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const nativeCredentialMarkerPrefix = "APEXLOAD_IMPORT_"

var nativeCredentialMarkerPattern = regexp.MustCompile(
	`(?i)\{\{\s*secret\.` + nativeCredentialMarkerPrefix + `([0-9a-f]{24})\s*\}\}`,
)

// NativeCredentialTarget binds one protected source requirement to one exact
// occurrence in the server-authored native script. It is retained only inside
// the encrypted import session and is never included in inspection responses.
type NativeCredentialTarget struct {
	RequirementID   string `json:"requirement_id"`
	JSONPointer     string `json:"json_pointer"`
	Occurrence      int    `json:"occurrence"`
	SanitizedSHA256 string `json:"sanitized_sha256"`
}

func nativeCredentialProjectionReplacement(requirement SecretRequirement) string {
	if requirement.ExistingReference {
		return requirement.Replacement
	}
	return "{{secret." + nativeCredentialMarkerPrefix +
		strings.ToUpper(strings.TrimSpace(requirement.ID)) + "}}"
}

func rejectReservedNativeCredentialMarkers(files []sourceFile) error {
	for _, file := range files {
		if nativeCredentialMarkerPattern.Match(file.Data) {
			return fmt.Errorf(
				"Bruno source %q uses a reserved native credential marker",
				file.Path,
			)
		}
	}
	return nil
}

func publicizeNativeCredentialMarkers(
	files []sourceFile,
	requirements []SecretRequirement,
) ([]sourceFile, error) {
	replacements := make(map[string]string, len(requirements))
	for _, requirement := range requirements {
		if requirement.ExistingReference {
			continue
		}
		marker := nativeCredentialProjectionReplacement(requirement)
		if prior, duplicate := replacements[marker]; duplicate && prior != requirement.Replacement {
			return nil, fmt.Errorf(
				"native credential marker %q has conflicting requirements",
				requirement.ID,
			)
		}
		replacements[marker] = requirement.Replacement
	}
	keys := make([]string, 0, len(replacements))
	for marker := range replacements {
		keys = append(keys, marker)
	}
	sort.Strings(keys)
	pairs := make([][]byte, 0, len(keys)*2)
	for _, marker := range keys {
		pairs = append(pairs, []byte(marker), []byte(replacements[marker]))
	}

	out := make([]sourceFile, 0, len(files))
	for _, file := range files {
		data := append([]byte(nil), file.Data...)
		for index := 0; index+1 < len(pairs); index += 2 {
			data = bytes.ReplaceAll(data, pairs[index], pairs[index+1])
		}
		if nativeCredentialMarkerPattern.Match(data) {
			return nil, fmt.Errorf(
				"native source %q contains an unknown credential marker",
				file.Path,
			)
		}
		copyFile := file
		copyFile.Data = data
		out = append(out, copyFile)
	}
	return out, nil
}

func finalizeNativeScriptCredentialTargets(
	script *dsl.Script,
	requirements []SecretRequirement,
) (*dsl.Script, []NativeCredentialTarget, error) {
	if script == nil {
		return nil, nil, errors.New("native script is required")
	}
	byID := make(map[string]SecretRequirement, len(requirements))
	for _, requirement := range requirements {
		id := strings.ToLower(strings.TrimSpace(requirement.ID))
		if id == "" {
			continue
		}
		byID[id] = requirement
	}
	payload, err := json.Marshal(script)
	if err != nil {
		return nil, nil, err
	}
	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, nil, err
	}
	targets := make([]NativeCredentialTarget, 0)
	var walk func(any, string) (any, error)
	walk = func(value any, pointer string) (any, error) {
		switch typed := value.(type) {
		case string:
			matches := nativeCredentialMarkerPattern.FindAllStringSubmatchIndex(typed, -1)
			if len(matches) == 0 {
				return typed, nil
			}
			var projected strings.Builder
			cursor := 0
			type markerTarget struct {
				requirement SecretRequirement
				offset      int
			}
			markerTargets := make([]markerTarget, 0, len(matches))
			for _, match := range matches {
				if len(match) < 4 {
					return nil, errors.New("native credential marker is malformed")
				}
				id := strings.ToLower(typed[match[2]:match[3]])
				requirement, ok := byID[id]
				if !ok {
					return nil, fmt.Errorf(
						"native credential marker references unknown requirement %q",
						id,
					)
				}
				projected.WriteString(typed[cursor:match[0]])
				offset := projected.Len()
				projected.WriteString(requirement.Replacement)
				markerTargets = append(markerTargets, markerTarget{
					requirement: requirement,
					offset:      offset,
				})
				cursor = match[1]
			}
			projected.WriteString(typed[cursor:])
			finalValue := projected.String()
			if nativeCredentialMarkerPattern.MatchString(finalValue) {
				return nil, errors.New("native credential marker remained after projection")
			}
			sum := sha256.Sum256([]byte(finalValue))
			digest := hex.EncodeToString(sum[:])
			for _, markerTarget := range markerTargets {
				occurrence := nativeStringOccurrenceAtOffset(
					finalValue,
					markerTarget.requirement.Replacement,
					markerTarget.offset,
				)
				if occurrence < 0 {
					return nil, fmt.Errorf(
						"native credential target %q could not be located after projection",
						markerTarget.requirement.ID,
					)
				}
				targets = append(targets, NativeCredentialTarget{
					RequirementID:   markerTarget.requirement.ID,
					JSONPointer:     pointer,
					Occurrence:      occurrence,
					SanitizedSHA256: digest,
				})
			}
			return finalValue, nil
		case []any:
			out := make([]any, len(typed))
			for index, child := range typed {
				next := pointer + "/" + fmt.Sprintf("%d", index)
				projected, err := walk(child, next)
				if err != nil {
					return nil, err
				}
				out[index] = projected
			}
			return out, nil
		case map[string]any:
			out := make(map[string]any, len(typed))
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				next := pointer + "/" + nativeJSONPointerEscape(key)
				projected, err := walk(typed[key], next)
				if err != nil {
					return nil, err
				}
				out[key] = projected
			}
			return out, nil
		default:
			return value, nil
		}
	}
	document, err = walk(document, "")
	if err != nil {
		return nil, nil, err
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, nil, err
	}
	var projected dsl.Script
	if err := json.Unmarshal(encoded, &projected); err != nil {
		return nil, nil, err
	}
	if err := restoreNativeScriptDynamicValues(&projected, script); err != nil {
		return nil, nil, err
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].JSONPointer == targets[j].JSONPointer {
			if targets[i].RequirementID == targets[j].RequirementID {
				return targets[i].Occurrence < targets[j].Occurrence
			}
			return targets[i].RequirementID < targets[j].RequirementID
		}
		return targets[i].JSONPointer < targets[j].JSONPointer
	})
	return &projected, targets, nil
}

// JSON projection is useful here because it gives the commit side stable RFC
// 6901 pointers. Values stored behind the DSL's map[string]any fields must not
// take that round trip, however: decoding through any changes typed []string
// values into []any and integer values into float64. Native credential markers
// are never authored into these converter metadata/parameter maps, so preserve
// their exact Go values after proving that invariant.
func restoreNativeScriptDynamicValues(projected, original *dsl.Script) error {
	if projected == nil || original == nil {
		return errors.New("native script dynamic-value restoration requires both scripts")
	}
	if original.Metadata != nil && original.Metadata.Import != nil {
		if err := rejectNativeCredentialMarkersInDynamicValue(
			original.Metadata.Import.Preserved,
			"script import metadata",
		); err != nil {
			return err
		}
		if projected.Metadata != nil && projected.Metadata.Import != nil {
			projected.Metadata.Import.Preserved = original.Metadata.Import.Preserved
		}
	}
	if err := restoreNativeActionDynamicValues(projected.Init, original.Init, "init"); err != nil {
		return err
	}
	if err := restoreNativeActionDynamicValues(projected.Actions, original.Actions, "actions"); err != nil {
		return err
	}
	return restoreNativeActionDynamicValues(projected.Teardown, original.Teardown, "teardown")
}

func restoreNativeActionDynamicValues(
	projected, original []dsl.Action,
	section string,
) error {
	if len(projected) != len(original) {
		return fmt.Errorf("native script %s changed shape during credential projection", section)
	}
	for index := range original {
		label := fmt.Sprintf("%s[%d]", section, index)
		if err := rejectNativeCredentialMarkersInDynamicValue(
			original[index].Params,
			label+" params",
		); err != nil {
			return err
		}
		if err := rejectNativeCredentialMarkersInDynamicValue(
			original[index].Metadata,
			label+" metadata",
		); err != nil {
			return err
		}
		projected[index].Params = original[index].Params
		projected[index].Metadata = original[index].Metadata
		if (projected[index].Control == nil) != (original[index].Control == nil) {
			return fmt.Errorf("native script %s changed control-flow shape during credential projection", label)
		}
		if original[index].Control == nil {
			continue
		}
		projectedBranches := projected[index].Control.ActionBranches()
		originalBranches := original[index].Control.ActionBranches()
		if len(projectedBranches) != len(originalBranches) {
			return fmt.Errorf("native script %s changed control-flow branch shape during credential projection", label)
		}
		for branchIndex := range projectedBranches {
			if projectedBranches[branchIndex].Path != originalBranches[branchIndex].Path {
				return fmt.Errorf("native script %s changed control-flow branch ordering during credential projection", label)
			}
			if err := restoreNativeActionDynamicValues(
				projectedBranches[branchIndex].Actions,
				originalBranches[branchIndex].Actions,
				label+".control."+projectedBranches[branchIndex].Path,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func rejectNativeCredentialMarkersInDynamicValue(value any, label string) error {
	if value == nil {
		return nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal native %s: %w", label, err)
	}
	if nativeCredentialMarkerPattern.Match(payload) {
		return fmt.Errorf("native credential marker appeared in unsupported %s", label)
	}
	return nil
}

func nativeStringOccurrenceAtOffset(value, token string, offset int) int {
	if token == "" || offset < 0 || offset+len(token) > len(value) ||
		value[offset:offset+len(token)] != token {
		return -1
	}
	occurrence := 0
	cursor := 0
	for {
		index := strings.Index(value[cursor:], token)
		if index < 0 {
			return -1
		}
		index += cursor
		if index == offset {
			return occurrence
		}
		occurrence++
		cursor = index + len(token)
	}
}
