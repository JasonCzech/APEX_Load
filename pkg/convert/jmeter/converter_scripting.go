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
					"source":        "jmeter",
					"source_type":   "JSR223",
					"jsr223_source": stmt,
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
						"source":        "jmeter",
						"source_type":   "JSR223",
						"jsr223_source": stmt,
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
// Recognized statements become engine actions; unrecognized scripting is
// preserved verbatim and flagged review_required.
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
		diagnostics.Add(diag.Warning, "JMeter "+name, samplerLabel, "scripting could not be safely translated; the source was preserved for manual translation before replay")
		*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, element, name, "", samplerLabel, "scripting", "review_only", truncateScriptNote(scriptText)))
		return
	}

	startIndex := len(script.Actions)
	script.Actions = append(script.Actions, actions...)

	// The sampler produced executable actions, so account it as converted (once
	// per sampler) — countJMeterSamplerElements pre-counted it as skipped, so
	// without this the summary understates ConvertedActions and overstates
	// SkippedSamplers for a successfully-translated script sampler.
	countNativeSamplerConversion(result)

	if recognized == total {
		diagnostics.Add(diag.Info, "JMeter "+name, samplerLabel, fmt.Sprintf("translated %d JSR223 statement(s) into engine actions; verify semantics before replay", recognized))
		*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, element, name, fmt.Sprintf("actions[%d]", startIndex), samplerLabel, "scripting", "converted", fmt.Sprintf("JMeter %s safe-subset script was translated into %d engine action(s).", name, recognized)))
		return
	}

	// Partial translation: some statements were left untranslated.
	preserveJMeterScriptSource(script, name, scriptText)
	diagnostics.Add(diag.Warning, "JMeter "+name, samplerLabel, fmt.Sprintf("translated %d of %d JSR223 statement(s); the remaining logic was preserved for manual review", recognized, total))
	*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, element, name, fmt.Sprintf("actions[%d]", startIndex), samplerLabel, "scripting", "review_only", fmt.Sprintf("JMeter %s was partially translated (%d of %d statements); review the remaining script.", name, recognized, total)))
}

// preserveJMeterScriptSource stashes the verbatim script body on the script
// metadata so reviewers can see exactly what was not translated.
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
	existing = append(existing, fmt.Sprintf("%s: %s", element, scriptText))
	script.Metadata.Import.Preserved["jsr223_sources"] = existing
}

func truncateScriptNote(scriptText string) string {
	const limit = 160
	clean := strings.Join(strings.Fields(scriptText), " ")
	if len(clean) > limit {
		clean = clean[:limit] + "…"
	}
	return "Preserved JSR223/BeanShell source for manual translation: " + clean
}
