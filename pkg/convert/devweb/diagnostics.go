package devweb

// diagnostics.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func diagnoseReviewOnlyJavaScript(block namedBlock, stats *ConvertStats, diagnostics *diag.Collector, steps *[]parsedStep, ctx convertContext) {
	for _, pattern := range reviewOnlyPatterns {
		for _, match := range pattern.Regex.FindAllStringIndex(block.Body, -1) {
			if pattern.Construct == "dynamic template literal" && isSafeDevWebTemplateLiteral(extractLine(block.Body, match[0]), ctx.parameterTemplates) {
				continue
			}
			stats.UnsupportedCalls = appendDevWebUnsupportedCall(stats.UnsupportedCalls, pattern.Construct)
			diagnostics.Add(diag.Warning, pattern.Element, devwebLocation(block, match[0]), pattern.Detail)
			lineContent := extractLine(block.Body, match[0])
			*steps = append(*steps, parsedStep{
				Protocol: "http",
				Action: dsl.Action{
					Name:  actionName(block.Name, "js_review"),
					Type:  "review_required",
					Value: strings.TrimSpace(lineContent),
					Metadata: map[string]any{
						"source": pattern.Construct,
					},
				},
				Function:  pattern.Construct,
				StartLine: devwebLine(block, match[0]),
				EndLine:   devwebLine(block, match[0]),
			})
		}
	}
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
