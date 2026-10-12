package enterprisearchive

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const vtsSourceReviewCode = "source_vts_requires_review"
const maxVTSSourceReferences = 64

type vtsSourceReference struct {
	Name string
	Line int
}

// Scan identifiers, not source substrings: comments and quoted credentials are
// never evidence. References (including aliases and macros) are conservatively
// gated; deciding whether an unfamiliar SDK call executes is not a safe mapping.
// This scanner recognizes C, Java and C# identifiers, plus direct JS namespaces.
// Dynamic JavaScript expressions retain the translator's own execution gate.
func scanVTSSourceReferences(content string) []vtsSourceReference {
	tokens := lexCTransactionTokens(content, false)
	var refs []vtsSourceReference
	seen := map[string]bool{}
	for i, token := range tokens {
		if token.kind != cTransactionIdentifier {
			continue
		}
		name := token.text
		lower := strings.ToLower(name)
		isFunction := strings.HasPrefix(lower, "lrvtc_") || strings.HasPrefix(lower, "vtc_")
		isNamespace := lower == "lrvtc" || lower == "vtc" || lower == "vts" || lower == "vts_multi"
		if !isFunction && !isNamespace {
			continue
		}
		if isNamespace && i+2 < len(tokens) && tokens[i+1].text == "." && tokens[i+2].kind == cTransactionIdentifier {
			name += "." + tokens[i+2].text
		}
		if len(name) > 160 {
			name = "vts_unresolved_identifier"
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		refs = append(refs, vtsSourceReference{Name: name, Line: token.line})
		if len(refs) == maxVTSSourceReferences {
			break // the file is already unconditionally execution-blocked
		}
	}
	return refs
}

// appendVTSSourceGates adds one value-free gate per referenced source file to
// every executable output lane. Raw values, URLs and return expressions never
// enter this metadata. A manual live_table replacement needs explicit review of
// parameter writes, return values, control flow and connection ownership.
func appendVTSSourceGates(result *ConvertResult, sources []*ParsedAction, files map[string]string) {
	if result == nil {
		return
	}
	for _, source := range sources {
		if source == nil {
			continue
		}
		refs := source.vtsReferences
		if len(refs) == 0 {
			for _, fn := range source.Functions {
				if found := scanVTSSourceReferences(fn.Name); len(found) > 0 {
					refs = append(refs, vtsSourceReference{Name: found[0].Name, Line: fn.LineNumber})
					if len(refs) == maxVTSSourceReferences {
						break
					}
				}
			}
		}
		if len(refs) == 0 && files != nil {
			_, content, ambiguous := findSourceArchiveFile(files, source.Filename)
			if len(ambiguous) == 0 {
				refs = scanVTSSourceReferences(content)
			}
		}
		if len(refs) == 0 {
			continue
		}
		for _, script := range result.effectiveScripts() {
			key := strings.ToLower(source.Filename)
			found := false
			for _, actions := range [][]dsl.Action{script.Init, script.Actions, script.Teardown} {
				for _, action := range actions {
					if action.Metadata["source_vts_gate"] == key {
						found = true
					}
				}
			}
			if found {
				continue
			}
			findings := make([]importreview.Finding, 0, len(refs))
			for _, ref := range refs {
				location := fmt.Sprintf("%s:%d", source.Filename, ref.Line)
				findings = append(findings, importreview.Finding{
					Code: vtsSourceReviewCode, ReasonCode: vtsSourceReviewCode,
					Element: ref.Name, Location: location, SourceRef: location,
					Source: "enterprise_script_archive", Gate: importreview.GateExecution,
					Detail:     "VTS source semantics and remote connections are not translated. Review return codes, parameter writes, column behavior and dependent control flow before replacing this dependency with an explicitly bound live_table action.",
					Resolution: "replace_vts_dependency_with_verified_live_table_actions",
				})
			}
			action := dsl.Action{
				Name:     uniqueCSourceParseFailureActionName(script, "VTS_"+source.Filename, "init"),
				Type:     "source_review",
				Params:   map[string]any{"function": "vts_source_dependency", "support_mode": string(SourceFunctionSupportReviewOnly)},
				Metadata: map[string]any{"source_file": source.Filename, "source_vts_gate": key, "support_mode": string(SourceFunctionSupportReviewOnly)},
			}
			importreview.ApplyToAction(&action, findings)
			// An unconditional init gate cannot be bypassed by an imported branch.
			script.Init = append(script.Init, action)
			result.Stats.ConvertedActions++
			recordSourceFunctionSupport(result, SourceFunctionSupportReviewOnly)
		}
		result.Warnings = appendUnique(result.Warnings, "VTS source dependencies require explicit review and live-table binding before execution.")
	}
}
