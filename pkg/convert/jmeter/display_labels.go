package jmeter

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scriptdisclosure"
)

// SanitizeDisplayLabels removes credentials known to the imported scripts from
// descriptive JMeter labels. Request, auth, body, parameter, and extractor
// values are intentionally outside this function: they remain the exact
// executable destination authored by the operator.
//
// Scenario group script_name fields are rewritten alongside script names so a
// sanitized draft remains directly saveable.
func SanitizeDisplayLabels(scripts []*dsl.Script, draft *ScenarioDraft) {
	if len(scripts) == 0 {
		return
	}
	sources := append([]*dsl.Script(nil), scripts...)
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
	rawLabels := make([]string, len(refs))
	for index, ref := range refs {
		rawLabels[index] = *ref.value
	}
	projectedLabels := scriptdisclosure.ProjectLabels(rawLabels, sources...)
	for index, ref := range refs {
		*ref.value = safeJMeterDisplayLabel(projectedLabels[index], ref.fallback)
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
		safeName := script.Name
		safeName = uniqueJMeterDisplayLabel(safeName, usedNames)
		script.Name = safeName
		if originalName != "" {
			safeNamesByOriginal[originalName] = append(safeNamesByOriginal[originalName], safeName)
		}
	}
	sanitizeJMeterActionReviewMetadata(scripts)

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

type jmeterMetadataStringRef struct {
	value string
	set   func(string)
}

func sanitizeJMeterActionReviewMetadata(scripts []*dsl.Script) {
	var refs []jmeterMetadataStringRef
	for _, script := range scripts {
		if script == nil {
			continue
		}
		appendJMeterActionReviewMetadataRefs(script.Init, &refs)
		appendJMeterActionReviewMetadataRefs(script.Actions, &refs)
		appendJMeterActionReviewMetadataRefs(script.Teardown, &refs)
	}
	if len(refs) == 0 {
		return
	}
	raw := make([]string, len(refs))
	for index := range refs {
		raw[index] = refs[index].value
	}
	projected := scriptdisclosure.ProjectLabels(raw, scripts...)
	for index := range refs {
		refs[index].set(projected[index])
	}
}

func appendJMeterActionReviewMetadataRefs(
	actions []dsl.Action,
	refs *[]jmeterMetadataStringRef,
) {
	for index := range actions {
		metadata := actions[index].Metadata
		for _, key := range []string{
			"transaction",
			"source_condition",
			"condition_original",
		} {
			appendJMeterMetadataMapStringRef(metadata, key, refs)
		}
		if review, ok := metadata["import_review"].(map[string]any); ok {
			switch sourceRefs := review["source_refs"].(type) {
			case []string:
				for sourceIndex := range sourceRefs {
					*refs = append(*refs, jmeterMetadataStringRef{
						value: sourceRefs[sourceIndex],
						set: func(value string) {
							sourceRefs[sourceIndex] = value
						},
					})
				}
			case []any:
				for sourceIndex, raw := range sourceRefs {
					value, ok := raw.(string)
					if !ok {
						continue
					}
					*refs = append(*refs, jmeterMetadataStringRef{
						value: value,
						set: func(value string) {
							sourceRefs[sourceIndex] = value
						},
					})
				}
			}
		}
		if actions[index].Control != nil {
			for _, branch := range actions[index].Control.ActionBranches() {
				appendJMeterActionReviewMetadataRefs(branch.Actions, refs)
			}
		}
	}
}

func appendJMeterMetadataMapStringRef(
	metadata map[string]any,
	key string,
	refs *[]jmeterMetadataStringRef,
) {
	if metadata == nil {
		return
	}
	value, ok := metadata[key].(string)
	if !ok {
		return
	}
	*refs = append(*refs, jmeterMetadataStringRef{
		value: value,
		set: func(value string) {
			metadata[key] = value
		},
	})
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
	if projected == "" || strings.Contains(projected, credential.Redacted) {
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

func sanitizeJMeterResultReviewText(result *ConvertResult) {
	if result == nil || len(result.Scripts) == 0 {
		return
	}
	refs := make([]*string, 0)
	for index := range result.Warnings {
		refs = append(refs, &result.Warnings[index])
	}
	for index := range result.Assumptions {
		refs = append(refs, &result.Assumptions[index])
	}
	for index := range result.Unsupported {
		refs = append(
			refs,
			&result.Unsupported[index].Element,
			&result.Unsupported[index].Detail,
			&result.Unsupported[index].Location,
		)
	}
	for index := range result.Findings {
		refs = append(
			refs,
			&result.Findings[index].Element,
			&result.Findings[index].Detail,
			&result.Findings[index].Location,
			&result.Findings[index].Source,
			&result.Findings[index].SourceRef,
			&result.Findings[index].Resolution,
		)
	}
	refs = append(refs, &result.Summary.FileName)
	for index := range result.Summary.VariablesDiscovered {
		refs = append(refs, &result.Summary.VariablesDiscovered[index])
	}
	for index := range result.Summary.CSVSourcesDiscovered {
		refs = append(refs, &result.Summary.CSVSourcesDiscovered[index])
	}
	if result.SourceReview != nil {
		for index := range result.SourceReview.SourceFiles {
			refs = append(
				refs,
				&result.SourceReview.SourceFiles[index].Path,
				&result.SourceReview.SourceFiles[index].Content,
			)
		}
		for index := range result.SourceReview.OmittedSourceFiles {
			refs = append(refs, &result.SourceReview.OmittedSourceFiles[index])
		}
		for index := range result.SourceReview.Mappings {
			refs = append(
				refs,
				&result.SourceReview.Mappings[index].ID,
				&result.SourceReview.Mappings[index].SourceFile,
				&result.SourceReview.Mappings[index].TargetName,
				&result.SourceReview.Mappings[index].Notes,
			)
		}
	}
	if len(refs) == 0 {
		return
	}
	raw := make([]string, len(refs))
	for index, ref := range refs {
		raw[index] = *ref
	}
	projected := scriptdisclosure.ProjectLabels(raw, result.Scripts...)
	for index, ref := range refs {
		*ref = projected[index]
	}
}
