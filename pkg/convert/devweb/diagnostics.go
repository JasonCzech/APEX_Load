package devweb

// diagnostics.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func diagnoseReviewOnlyJavaScript(block namedBlock, stats *ConvertStats, diagnostics *diag.Collector, steps *[]parsedStep, ctx convertContext) {
	for _, pattern := range reviewOnlyPatterns {
		for _, match := range pattern.Regex.FindAllStringIndex(block.Body, -1) {
			if pattern.Construct == "dynamic template literal" && isSafeDevWebTemplateLiteral(extractLine(block.Body, match[0]), ctx.parameterTemplates) {
				continue
			}
			if pattern.Construct == "residual JavaScript assignment" &&
				isDirectlyConvertedDevWebAssignment(extractLine(block.Body, match[0]), *steps) {
				continue
			}
			stats.UnsupportedCalls = appendDevWebUnsupportedCall(stats.UnsupportedCalls, pattern.Construct)
			diagnostics.Add(diag.Warning, pattern.Element, devwebLocation(block, match[0]), pattern.Detail)
			if mergeDevWebReviewFinding(
				*steps,
				block,
				match[0],
				pattern.Construct,
				"unsupported_source_logic",
				pattern.Detail,
			) {
				continue
			}
			*steps = append(*steps, newDevWebReviewStep(block, match[0], pattern.Construct, "unsupported_source_logic", pattern.Detail))
		}
	}
}

var devWebAssignmentNamePattern = regexp.MustCompile(`(?i)\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=`)

func isDirectlyConvertedDevWebAssignment(line string, steps []parsedStep) bool {
	lower := strings.ToLower(line)
	trimmed := strings.TrimSpace(lower)
	if strings.Contains(lower, "require(") ||
		strings.Contains(lower, "require (") ||
		strings.HasPrefix(trimmed, "for (") ||
		strings.HasPrefix(trimmed, "for(") {
		// These are already represented by the custom-module/control-flow
		// findings, so a second placeholder would add no review information.
		return true
	}
	for _, construct := range []string{
		"new load.webrequest",
		"new load.websocket",
		"new load.transaction",
	} {
		if strings.Contains(lower, construct) {
			return true
		}
	}
	if match := devWebAssignmentNamePattern.FindStringSubmatch(line); len(match) == 2 {
		for _, step := range steps {
			if stringValue(step.Action.Metadata["devweb_request_variable"]) == match[1] {
				return true
			}
		}
	}
	return false
}

func mergeDevWebReviewFinding(
	steps []parsedStep,
	block namedBlock,
	offset int,
	construct string,
	reasonCode string,
	detail string,
) bool {
	line := devwebLine(block, offset)
	for index := range steps {
		if steps[index].StartLine != line ||
			!strings.EqualFold(strings.TrimSpace(steps[index].Action.Type), "review_required") {
			continue
		}
		applyDevWebReviewFinding(
			&steps[index].Action,
			block,
			offset,
			construct,
			reasonCode,
			detail,
		)
		return true
	}
	return false
}

func newDevWebReviewStep(
	block namedBlock,
	offset int,
	construct string,
	reasonCode string,
	detail string,
) parsedStep {
	line := devwebLine(block, offset)
	action := dsl.Action{
		Name:  actionName(block.Name, "js_review"),
		Type:  "review_required",
		Value: "Source behavior requires operator review before execution.",
		Metadata: map[string]any{
			"source": strings.TrimSpace(construct),
		},
	}
	applyDevWebReviewFinding(&action, block, offset, construct, reasonCode, detail)
	return parsedStep{
		Protocol:  "http",
		Action:    action,
		Function:  construct,
		StartLine: line,
		EndLine:   line,
	}
}

func applyDevWebReviewFinding(
	action *dsl.Action,
	block namedBlock,
	offset int,
	construct string,
	reasonCode string,
	detail string,
) {
	if action == nil {
		return
	}
	line := devwebLine(block, offset)
	sourceRef := fmt.Sprintf(
		"%s line %d",
		convertutil.FirstNonEmpty(block.Name, "DevWebAction"),
		line,
	)
	importreview.ApplyToAction(action, []importreview.Finding{{
		Code:       "import_review_required",
		Severity:   diag.Blocker,
		Element:    "DevWeb source behavior",
		Detail:     detail,
		Location:   sourceRef,
		Source:     "devweb",
		ReasonCode: reasonCode,
		SourceRef:  sourceRef,
		Resolution: "replace_behavior_and_remove_gate",
		Gate:       importreview.GateExecution,
	}})
}

func appendDevWebUnsupportedCall(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func webRequestCallCovered(offset int, matches [][]int) bool {
	for _, match := range matches {
		if len(match) >= 2 && offset >= match[0] && offset < match[1] {
			return true
		}
	}
	return false
}

func devwebLocation(block namedBlock, offset int) string {
	line := devwebLine(block, offset)
	return fmt.Sprintf("%s line %d", convertutil.FirstNonEmpty(block.Name, "DevWebAction"), line)
}

func devwebLine(block namedBlock, offset int) int {
	start := block.StartLine
	if start <= 0 {
		start = 1
	}
	if offset <= 0 {
		return start
	}
	if offset > len(block.Body) {
		offset = len(block.Body)
	}
	return start + strings.Count(block.Body[:offset], "\n")
}

func extractLine(content string, offset int) string {
	if offset < 0 || offset >= len(content) {
		return ""
	}
	start := strings.LastIndex(content[:offset], "\n")
	if start == -1 {
		start = 0
	} else {
		start++ // skip the newline
	}
	end := strings.Index(content[offset:], "\n")
	if end == -1 {
		end = len(content)
	} else {
		end += offset
	}
	return content[start:end]
}

func sortParsedSteps(steps []parsedStep) {
	for i := 0; i < len(steps); i++ {
		for j := i + 1; j < len(steps); j++ {
			if steps[i].StartLine > steps[j].StartLine {
				steps[i], steps[j] = steps[j], steps[i]
			}
		}
	}
}

func devwebSourceMapping(sourceFile string, step parsedStep, targetPath string) sourceview.Mapping {
	startLine := step.StartLine
	if startLine <= 0 {
		startLine = 1
	}
	endLine := step.EndLine
	if endLine < startLine {
		endLine = startLine
	}
	return sourceview.Mapping{
		SourceFile: sourceFile,
		StartLine:  startLine,
		EndLine:    endLine,
		Function:   convertutil.FirstNonEmpty(step.Function, step.Action.Type),
		TargetPath: targetPath,
		TargetName: step.Action.Name,
		TargetKind: "action",
		Status:     "converted",
		Notes:      "DevWeb SDK construct was converted into generated APEX Load DSL.",
	}
}

func isDevWebNativeExtractorAt(content string, offset int) bool {
	line := extractLine(content, offset)
	return strings.Contains(line, "BoundaryExtractor") || strings.Contains(line, "JsonPathExtractor")
}

func activeDevWebTransaction(prefix string) string {
	matches := transactionNewPattern.FindAllStringSubmatch(prefix, -1)
	if len(matches) == 0 {
		return ""
	}
	var activeVar, activeName string
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		activeVar = match[1]
		activeName = match[2]
	}
	if activeVar == "" || activeName == "" {
		return ""
	}
	start := strings.LastIndex(prefix, activeVar+".start(")
	stop := strings.LastIndex(prefix, activeVar+".stop(")
	if start >= 0 && start > stop {
		return activeName
	}
	return ""
}
