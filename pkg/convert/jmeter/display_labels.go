package jmeter

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// SanitizeDisplayLabels normalizes empty JMeter display labels and allocates
// unique script names while preserving authored text verbatim (never scrubbing
// or replacing credential-bearing names with [REDACTED]).
//
// Scenario group script_name fields are rewritten alongside script names so a
// normalized draft remains directly saveable.
func SanitizeDisplayLabels(scripts []*dsl.Script, draft *ScenarioDraft) {
	if len(scripts) == 0 {
		return
	}
	originalNames := make([]string, len(scripts))
	refs := make([]jmeterDisplayLabelRef, 0, len(scripts)*2)
	actionOrdinal := 0
	for index, script := range scripts {
		if script == nil {
			continue
		}
		originalNames[index] = strings.TrimSpace(script.Name)
		refs = append(refs, jmeterDisplayLabelRef{
			value:    &script.Name,
			fallback: fmt.Sprintf("JMeter_Import_%d", index+1),
		})
		if script.Metadata != nil && script.Metadata.Import != nil {
			refs = append(refs, jmeterDisplayLabelRef{
				value:    &script.Metadata.Import.SourceScriptName,
				fallback: fmt.Sprintf("JMeter_Import_%d", index+1),
			})
		}
		appendJMeterActionLabelRefs(script.Init, &refs, &actionOrdinal)
		appendJMeterActionLabelRefs(script.Actions, &refs, &actionOrdinal)
		appendJMeterActionLabelRefs(script.Teardown, &refs, &actionOrdinal)
	}
	if draft != nil {
		refs = append(refs, jmeterDisplayLabelRef{
			value:    &draft.Name,
			fallback: "Imported JMeter Scenario",
		})
		for index := range draft.Groups {
			refs = append(refs, jmeterDisplayLabelRef{
				value:    &draft.Groups[index].Name,
				fallback: fmt.Sprintf("Thread Group %d", index+1),
			})
		}
	}
	for _, ref := range refs {
		*ref.value = safeJMeterDisplayLabel(*ref.value, ref.fallback)
	}

	// Keep every allocation for a repeated authored name. Multi-JMX archives can
	// legitimately contain the same Thread Group name in independent plans; a
	// scalar map would make every scenario group point at the last allocation.
	safeNamesByOriginal := make(map[string][]string, len(scripts))
	usedNames := make(map[string]struct{}, len(scripts))

	for index, script := range scripts {
		if script == nil {
			continue
		}
		originalName := originalNames[index]
		safeName := uniqueJMeterDisplayLabel(script.Name, usedNames)
		script.Name = safeName
		if originalName != "" {
			safeNamesByOriginal[originalName] = append(safeNamesByOriginal[originalName], safeName)
		}
	}

	if draft == nil {
		return
	}
	consumedNames := make(map[string]int, len(safeNamesByOriginal))
	for index := range draft.Groups {
		group := &draft.Groups[index]
		originalScriptName := strings.TrimSpace(group.ScriptName)
		allocations := safeNamesByOriginal[originalScriptName]
		allocationIndex := consumedNames[originalScriptName]
		if allocationIndex < len(allocations) {
			group.ScriptName = allocations[allocationIndex]
			consumedNames[originalScriptName] = allocationIndex + 1
		}
	}
}

type jmeterDisplayLabelRef struct {
	value    *string
	fallback string
}

func appendJMeterActionLabelRefs(
	actions []dsl.Action,
	refs *[]jmeterDisplayLabelRef,
	ordinal *int,
) {
	for index := range actions {
		(*ordinal)++
		*refs = append(*refs, jmeterDisplayLabelRef{
			value:    &actions[index].Name,
			fallback: fmt.Sprintf("JMeter Action %d", *ordinal),
		})
		if actions[index].Control != nil {
			for _, branch := range actions[index].Control.ActionBranches() {
				appendJMeterActionLabelRefs(branch.Actions, refs, ordinal)
			}
		}
	}
}

func safeJMeterDisplayLabel(label string, fallback string) string {
	projected := strings.TrimSpace(label)
	if projected == "" {
		return fallback
	}
	return projected
}

func uniqueJMeterDisplayLabel(label string, used map[string]struct{}) string {
	identity := strings.ToLower(strings.TrimSpace(label))
	if _, exists := used[identity]; !exists {
		used[identity] = struct{}{}
		return label
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", label, suffix)
		candidateIdentity := strings.ToLower(strings.TrimSpace(candidate))
		if _, exists := used[candidateIdentity]; exists {
			continue
		}
		used[candidateIdentity] = struct{}{}
		return candidate
	}
}

func sanitizeJMeterResultReviewText(_ *ConvertResult) {
}
