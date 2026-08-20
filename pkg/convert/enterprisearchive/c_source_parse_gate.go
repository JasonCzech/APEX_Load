package enterprisearchive

import (
	"fmt"
	filepath "path"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const cSourceParseFailureCode = "source_c_parse_failure"

type cSourceParseFailureGroup struct {
	filename string
	phase    string
	failures []CSourceParseFailure
}

// appendCSourceParseFailureGates preserves incomplete C analysis as a durable,
// value-free action gate. One gate is emitted for each lifecycle file and
// phase, so a malformed sibling can never be hidden by another file that
// parsed successfully.
func appendCSourceParseFailureGates(result *ConvertResult, actions []*ParsedAction) {
	if result == nil || result.Script == nil {
		return
	}
	groups := groupCSourceParseFailures(actions)
	for _, group := range groups {
		key := cSourceParseFailureGroupKey(group.filename, group.phase)
		if scriptHasCSourceParseFailureGate(result.Script, key) {
			continue
		}

		kinds := make([]string, 0, len(group.failures))
		lines := make([]int, 0, len(group.failures))
		findings := make([]importreview.Finding, 0, len(group.failures))
		startLine, endLine := 0, 0
		for _, failure := range group.failures {
			kinds = append(kinds, failure.Kind)
			if failure.LineNumber > 0 {
				lines = append(lines, failure.LineNumber)
				if startLine == 0 || failure.LineNumber < startLine {
					startLine = failure.LineNumber
				}
				if failure.LineNumber > endLine {
					endLine = failure.LineNumber
				}
			}
			sourceRef := cSourceParseFailureSourceRef(group.filename, failure.LineNumber)
			findings = append(findings, importreview.Finding{
				Code:       cSourceParseFailureCode,
				Element:    "C source analysis",
				Detail:     "This lifecycle file was not analyzed completely; its imported behavior cannot execute safely until the source is corrected or reviewed.",
				Location:   sourceRef,
				Source:     "enterprise_script_archive",
				ReasonCode: cSourceParseFailureCode,
				SourceRef:  sourceRef,
				Resolution: "correct_or_review_the_source_file_and_reimport",
				Gate:       importreview.GateExecution,
			})
		}
		kinds = uniqueSortedStrings(kinds)
		sort.Ints(lines)

		action := dsl.Action{
			Name: uniqueCSourceParseFailureActionName(
				result.Script,
				group.filename,
				group.phase,
			),
			Type: "source_review",
			Params: map[string]any{
				"function":     "c_source_parse_failure",
				"support_mode": string(SourceFunctionSupportReviewOnly),
			},
			Metadata: map[string]any{
				"source_file":           group.filename,
				"source_phase":          normalizeSourcePhase(group.phase),
				"source_parse_failures": kinds,
				"source_parse_lines":    lines,
				"support_mode":          string(SourceFunctionSupportReviewOnly),
				"source_argument_count": 0,
				"source_call_shape":     "c_source_parse_failure()",
				"c_source_parse_gate":   key,
			},
		}
		importreview.ApplyToAction(&action, findings)
		actionIndex := actionCountForPhase(result.Script, group.phase)
		appendActionForPhase(result.Script, group.phase, action)

		fn := ParsedFunction{
			Name:       "c_source_parse_failure",
			Filename:   group.filename,
			Phase:      group.phase,
			LineNumber: startLine,
			EndLine:    endLine,
		}
		appendSourceReviewMapping(
			result,
			sourceReviewMappingWithSupportMode(
				fn,
				sourceReviewActionPath(group.phase, actionIndex),
				action.Name,
				"action",
				"review_only",
				"Source analysis was incomplete; execution remains blocked pending correction or explicit review.",
				SourceFunctionSupportReviewOnly,
			),
		)
		result.Stats.TotalFunctions++
		result.Stats.ConvertedActions++
		recordSourceFunctionSupport(result, SourceFunctionSupportReviewOnly)
		result.Warnings = appendUnique(
			result.Warnings,
			fmt.Sprintf("%s requires C source review before execution.", group.filename),
		)
	}
}

func groupCSourceParseFailures(actions []*ParsedAction) []cSourceParseFailureGroup {
	grouped := make(map[string]*cSourceParseFailureGroup)
	for _, action := range actions {
		if action == nil {
			continue
		}
		for _, failure := range action.ParseFailures {
			filename := strings.TrimSpace(failure.Filename)
			if filename == "" {
				filename = strings.TrimSpace(action.Filename)
			}
			phase := strings.TrimSpace(failure.Phase)
			if phase == "" {
				phase = strings.TrimSpace(action.Phase)
			}
			phase = normalizeSourcePhase(phase)
			kind := strings.TrimSpace(failure.Kind)
			if kind == "" {
				kind = "incomplete_source_analysis"
			}
			failure = CSourceParseFailure{
				Filename:   filename,
				Phase:      phase,
				LineNumber: failure.LineNumber,
				Kind:       kind,
			}
			key := cSourceParseFailureGroupKey(filename, phase)
			group := grouped[key]
			if group == nil {
				group = &cSourceParseFailureGroup{filename: filename, phase: phase}
				grouped[key] = group
			}
			group.failures = appendUniqueCSourceParseFailures(group.failures, failure)
		}
	}
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]cSourceParseFailureGroup, 0, len(keys))
	for _, key := range keys {
		group := grouped[key]
		sort.SliceStable(group.failures, func(i, j int) bool {
			if group.failures[i].LineNumber != group.failures[j].LineNumber {
				return group.failures[i].LineNumber < group.failures[j].LineNumber
			}
			return group.failures[i].Kind < group.failures[j].Kind
		})
		out = append(out, *group)
	}
	return out
}

func cSourceParseFailureGroupKey(filename string, phase string) string {
	filename = strings.ToLower(strings.TrimSpace(filename))
	return fmt.Sprintf("%d:%s:%s", len(filename), filename, normalizeSourcePhase(phase))
}

func cSourceParseFailureSourceRef(filename string, line int) string {
	filename = strings.TrimSpace(filename)
	if line > 0 {
		return fmt.Sprintf("%s:%d", filename, line)
	}
	return filename
}

func scriptHasCSourceParseFailureGate(script *dsl.Script, key string) bool {
	if script == nil {
		return false
	}
	for _, actions := range [][]dsl.Action{script.Init, script.Actions, script.Teardown} {
		for index := range actions {
			if strings.TrimSpace(stringMapValue(actions[index].Metadata, "c_source_parse_gate", "")) == key {
				return true
			}
		}
	}
	return false
}

func uniqueCSourceParseFailureActionName(script *dsl.Script, filename string, phase string) string {
	base := sanitizeName(fmt.Sprintf(
		"Review C source parse %s %s",
		normalizeSourcePhase(phase),
		filepath.Base(filename),
	))
	if base == "" {
		base = "Review C source parse"
	}
	used := make(map[string]struct{})
	if script != nil {
		for _, actions := range [][]dsl.Action{script.Init, script.Actions, script.Teardown} {
			for _, action := range actions {
				used[strings.ToLower(action.Name)] = struct{}{}
			}
		}
	}
	if _, exists := used[strings.ToLower(base)]; !exists {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", base, suffix)
		if _, exists := used[strings.ToLower(candidate)]; !exists {
			return candidate
		}
	}
}
