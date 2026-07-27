package jmeter

// converter_scripting.go — conservative JSR223/BeanShell/Groovy safe-subset
// translation.
//
// Arbitrary JVM scripting cannot be executed by the importer. Instead, a tiny
// whitelist of single-statement patterns is translated into native engine
// actions (source_parameter / source_message); everything else is preserved
// verbatim for manual review. The bias is always toward review: a statement is
// only translated when it is an exact, unambiguous match.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// jmeterScriptSamplerNames are standalone script samplers (no protocol I/O) whose
// bodies are candidates for safe-subset translation.
func isJMeterScriptSampler(name string) bool {
	switch name {
	case "JSR223Sampler", "BeanShellSampler", "BSFSampler":
		return true
	default:
		return false
	}
}

var jsr223LogRE = regexp.MustCompile(`^log\.(info|warn|error|debug)\(\s*(.+?)\s*\)$`)

// extractJMeterScriptText reads the script body from a JSR223/BeanShell sampler.
func extractJMeterScriptText(element *xmlNode) string {
	for _, name := range []string{"script", "BeanShellSampler.query", "BSFSampler.query", "BeanShellSampler.script"} {
		if value := strings.TrimSpace(prop(element, name)); value != "" {
			return value
		}
	}
	return ""
}

// splitJSR223Statements splits a script body into candidate statements. It is
// deliberately simple: statements are separated by newlines or semicolons, and
// comment/import lines are dropped. A statement that accidentally splits a string
// literal simply fails to match a translation pattern and is left untranslated.
func splitJSR223Statements(raw string) []string {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\n", ";")
	parts := strings.Split(normalized, ";")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		stmt := strings.TrimSpace(part)
		if stmt == "" {
			continue
		}
		if strings.HasPrefix(stmt, "//") || strings.HasPrefix(stmt, "#") || strings.HasPrefix(stmt, "/*") || strings.HasPrefix(stmt, "*") {
			continue
		}
		if strings.HasPrefix(stmt, "import ") || strings.HasPrefix(stmt, "package ") {
			continue
		}
		statements = append(statements, stmt)
	}
	return statements
}

// unquoteJSR223Literal returns the inner text of a single- or double-quoted
// literal and true when the input is exactly one quoted string with no embedded
// quotes of the same kind (so we never mis-handle concatenation or escaping).
func unquoteJSR223Literal(expr string) (string, bool) {
	expr = strings.TrimSpace(expr)
	if len(expr) < 2 {
		return "", false
	}
	quote := expr[0]
	if quote != '"' && quote != '\'' {
		return "", false
	}
	if expr[len(expr)-1] != quote {
		return "", false
	}
	inner := expr[1 : len(expr)-1]
	if strings.IndexByte(inner, quote) >= 0 {
		// Embedded quote of the same kind → concatenation/escaping; not safe.
		return "", false
	}
	return inner, true
}

var jsr223VarsGetRE = regexp.MustCompile(`^vars\.get\(\s*(["'])([^"']*)["']\s*\)$`)

// resolveJSR223Value resolves a scalar value expression to an APEX Load template
// string. Only string literals (with JMeter ${var}/${__fn()} interpolation) and a
// single vars.get("x") are accepted; anything else is rejected.
func resolveJSR223Value(expr string) (string, bool) {
	expr = strings.TrimSpace(expr)
	if literal, ok := unquoteJSR223Literal(expr); ok {
		return convertVariables(literal), true
	}
	if match := jsr223VarsGetRE.FindStringSubmatch(expr); len(match) == 3 {
		return "{{var." + match[2] + "}}", true
	}
	return "", false
}

// parseVarsPut parses `vars.put("key", <value>)`. The key must be a string
// literal; the value must resolve via resolveJSR223Value.
func parseVarsPut(stmt string) (key, value string, ok bool) {
	stmt = strings.TrimSpace(stmt)
	const prefix = "vars.put("
	if !strings.HasPrefix(stmt, prefix) || !strings.HasSuffix(stmt, ")") {
		return "", "", false
	}
	inner := strings.TrimSpace(stmt[len(prefix) : len(stmt)-1])
	if len(inner) == 0 {
		return "", "", false
	}
	quote := inner[0]
	if quote != '"' && quote != '\'' {
		return "", "", false
	}
	closing := strings.IndexByte(inner[1:], quote)
	if closing < 0 {
		return "", "", false
	}
	key = inner[1 : 1+closing]
	rest := strings.TrimSpace(inner[1+closing+1:])
	if !strings.HasPrefix(rest, ",") {
		return "", "", false
	}
	valueExpr := strings.TrimSpace(rest[1:])
	resolved, valueOK := resolveJSR223Value(valueExpr)
	if !valueOK {
		return "", "", false
	}
	return key, resolved, true
}

// translateJSR223Script returns the engine actions translated from the whitelist
// of safe statements, along with how many statements were recognized vs. total.
func translateJSR223Script(samplerName, raw string) (actions []dsl.Action, recognized, total int) {
	statements := splitJSR223Statements(raw)
	for _, stmt := range statements {
		total++
		if key, value, ok := parseVarsPut(stmt); ok {
			actions = append(actions, dsl.Action{
				Name: fmt.Sprintf("%s: set %s", samplerName, key),
				Type: "source_parameter",
				Params: map[string]any{
					"function": "lr_save_string",
					"args":     []any{value, key},
				},
				Metadata: map[string]any{
					"source":      "jmeter",
					"source_type": "JSR223",
				},
			})
			recognized++
			continue
		}
		if match := jsr223LogRE.FindStringSubmatch(stmt); len(match) == 3 {
			if message, ok := resolveJSR223Value(match[2]); ok {
				actions = append(actions, dsl.Action{
					Name:  fmt.Sprintf("%s: log", samplerName),
					Type:  "source_message",
					Value: message,
					Params: map[string]any{
						"level": jsr223LogLevel(match[1]),
					},
					Metadata: map[string]any{
						"source":      "jmeter",
						"source_type": "JSR223",
					},
				})
				recognized++
				continue
			}
		}
	}
	return actions, recognized, total
}

// jsr223LogLevel maps a Groovy log level onto the engine's supported set
// (debug / info / error). warn collapses to info.
func jsr223LogLevel(level string) string {
	switch level {
	case "debug":
		return "debug"
	case "error":
		return "error"
	default:
		return "info"
	}
}

// handleJMeterScriptSampler converts a standalone JSR223/BeanShell/BSF sampler.
// Fully recognized statements become engine actions. Unrecognized or partially
// recognized scripting becomes a disclosure-safe blocked placeholder. Raw JVM
// source is never copied into the generated script or its review metadata.
func handleJMeterScriptSampler(node *treeNode, script *dsl.Script, result *ConvertResult, diagnostics *diag.Collector, sourceFileName string, sourceMappings *[]sourceview.Mapping) {
	element := node.Element
	name := element.Name
	samplerLabel := jmeterElementLocation(element)
	scriptText := extractJMeterScriptText(element)

	if strings.TrimSpace(scriptText) == "" {
		diagnostics.Add(diag.Info, "JMeter "+name, samplerLabel, "script sampler had no script body to translate")
		*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, element, name, "", samplerLabel, "scripting", "ignored", "JMeter script sampler had no script body."))
		return
	}

	actions, recognized, total := translateJSR223Script(samplerLabel, scriptText)
	if recognized == 0 {
		preserveJMeterScriptSource(script, name, scriptText)
		actionIndex := appendJMeterSourceReviewPlaceholder(script, name, samplerLabel, sourceFileName, "unsupported_source_script")
		diagnostics.Add(diag.Warning, "JMeter "+name, samplerLabel, "scripting could not be safely translated; source text was omitted from the draft and manual replacement is required before replay")
		*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, element, name, fmt.Sprintf("actions[%d]", actionIndex), samplerLabel, "scripting", "review_only", truncateScriptNote(scriptText)))
		return
	}

	if recognized != total {
		// A partial translation cannot prove which source literals belong to the
		// safe statement versus the omitted JVM behavior. Keep the draft useful
		// and structurally valid, but do not copy either subset into API-facing
		// action fields.
		preserveJMeterScriptSource(script, name, scriptText)
		actionIndex := appendJMeterSourceReviewPlaceholder(script, name, samplerLabel, sourceFileName, "partially_translated_source_script")
		diagnostics.Add(diag.Warning, "JMeter "+name, samplerLabel, fmt.Sprintf("recognized %d of %d JSR223 statement(s); source text was omitted and the behavior must be replaced before replay", recognized, total))
		*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, element, name, fmt.Sprintf("actions[%d]", actionIndex), samplerLabel, "scripting", "review_only", fmt.Sprintf("JMeter %s was partially recognized (%d of %d statements); the source was omitted from the blocked draft.", name, recognized, total)))
		return
	}

	startIndex := len(script.Actions)
	script.Actions = append(script.Actions, actions...)

	// The sampler produced executable actions, so account it as converted (once
	// per sampler) — countJMeterSamplerElements pre-counted it as skipped, so
	// without this the summary understates ConvertedActions and overstates
	// SkippedSamplers for a successfully-translated script sampler.
	countNativeSamplerConversion(result)

	diagnostics.Add(diag.Info, "JMeter "+name, samplerLabel, fmt.Sprintf("translated %d JSR223 statement(s) into engine actions; verify semantics before replay", recognized))
	*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, element, name, fmt.Sprintf("actions[%d]", startIndex), samplerLabel, "scripting", "converted", fmt.Sprintf("JMeter %s safe-subset script was translated into %d engine action(s).", name, recognized)))
}

// preserveJMeterScriptSource retains the legacy metadata key without retaining
// the source body. The protected source file remains the authority for an
// operator-controlled re-import; public conversion results contain only this
// disclosure-safe marker.
func preserveJMeterScriptSource(script *dsl.Script, element, scriptText string) {
	if script == nil {
		return
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{Source: "jmeter"}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	existing, _ := script.Metadata.Import.Preserved["jsr223_sources"].([]string)
	existing = append(existing, fmt.Sprintf("%s: [SOURCE LOGIC OMITTED]", element))
	script.Metadata.Import.Preserved["jsr223_sources"] = existing
}

func truncateScriptNote(scriptText string) string {
	return "JSR223/BeanShell source text was omitted from the public draft; replace the behavior before replay."
}

func appendJMeterSourceReviewPlaceholder(script *dsl.Script, element, location, sourceFileName, reasonCode string) int {
	action := dsl.Action{
		Name:  "Review " + element,
		Type:  "review_required",
		Value: "Replace unsupported JMeter source behavior before replay.",
		Metadata: map[string]any{
			"source":      "jmeter",
			"source_type": element,
		},
	}
	importreview.ApplyToAction(&action, []importreview.Finding{{
		Code:       "import_review_required",
		Severity:   diag.Blocker,
		Element:    "JMeter " + element,
		Detail:     "source behavior was omitted from the disclosure-safe draft and must be replaced before replay",
		Location:   location,
		Source:     "jmeter",
		ReasonCode: reasonCode,
		SourceRef:  sourceFileName + "#" + location,
		Resolution: "replace_behavior_and_remove_gate",
		Gate:       importreview.GateExecution,
	}})
	actionIndex := len(script.Actions)
	script.Actions = append(script.Actions, action)
	return actionIndex
}
