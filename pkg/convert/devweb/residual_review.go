package devweb

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const devWebGatedSourceMetadataKey = "devweb_gated_source"

func devWebImportHasResidualBehavior(result *ConvertResult) bool {
	if result == nil || result.Script == nil {
		return false
	}
	if result.Stats.Skipped > 0 || len(result.Stats.UnsupportedCalls) > 0 {
		return true
	}
	for _, action := range result.Script.Actions {
		if strings.EqualFold(strings.TrimSpace(action.Type), "review_required") {
			return true
		}
	}
	return false
}

// preserveDevWebGatedSource retains exact source only in the server-authored,
// durable import payload. The API response projection treats this key as opaque
// and never returns its contents on ordinary script reads or exports.
func preserveDevWebGatedSource(script *dsl.Script, files map[string]string) {
	if script == nil || len(files) == 0 {
		return
	}
	preserved := ensureDevWebImportPreserved(script)
	snapshot := make(map[string]string, len(files))
	for name, content := range files {
		snapshot[name] = content
	}
	preserved[devWebGatedSourceMetadataKey] = snapshot
}

func applyDevWebResidualGates(
	script *dsl.Script,
	sourceFile string,
) []importreview.Finding {
	if script == nil {
		return nil
	}
	sourceRef := strings.TrimSpace(sourceFile)
	if sourceRef == "" {
		sourceRef = "devweb source"
	}
	findings := make([]importreview.Finding, 0, len(script.Actions))
	for index := range script.Actions {
		actionPath := fmt.Sprintf("actions[%d]", index)
		finding := importreview.Finding{
			Code:       "import_review_required",
			Severity:   diag.Blocker,
			Element:    "DevWeb residual source behavior",
			Detail:     "Source behavior outside the proven static subset may affect this action and must be replaced before execution.",
			Location:   actionPath,
			ActionPath: actionPath,
			Source:     "devweb",
			ReasonCode: "unsupported_source_logic",
			SourceRef:  sourceRef,
			Resolution: "replace_behavior_and_remove_gate",
			Gate:       importreview.GateExecution,
		}
		importreview.ApplyToAction(&script.Actions[index], []importreview.Finding{finding})
		findings = append(findings, finding)
	}
	return findings
}

// disclosureSafeDevWebResidualSource preserves line structure for mappings but
// removes every source token. Exact source remains available only in the opaque,
// durably gated metadata payload above.
func disclosureSafeDevWebResidualSource(content string) string {
	lines := strings.Split(content, "\n")
	for index := range lines {
		if strings.TrimSpace(lines[index]) != "" {
			lines[index] = "// [SOURCE OMITTED FROM PUBLIC IMPORT REVIEW]"
		}
	}
	return strings.Join(lines, "\n")
}
