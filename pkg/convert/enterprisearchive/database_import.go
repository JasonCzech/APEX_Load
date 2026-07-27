package enterprisearchive

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
)

var sqlStatementPattern = regexp.MustCompile(`(?is)\b(select|insert\s+into|update|delete\s+from|create\s+table|drop\s+table|alter\s+table|exec(?:ute)?|call|begin|commit|rollback)\b[\s\S]*`)

func ConvertDatabase(actions []*ParsedAction, opts ConvertOptions, detected DetectedProtocol) (*ConvertResult, error) {
	result := &ConvertResult{
		DetectedProtocol: &detected,
		Warnings:         append([]string(nil), detected.Warnings...),
		Script: &dsl.Script{
			Name:          convertutil.FirstNonEmpty(opts.ScriptName, "Converted_LR_Database_Script"),
			Version:       "1.0.0",
			Protocol:      "database",
			GeneratedBy:   "apexload-enterprise-script-archive-importer",
			GeneratedFrom: "enterprise_script_archive",
			Config: dsl.ScriptConfig{
				Database: dsl.DatabaseConfig{
					Driver:         detectDatabaseDriver(detected),
					DSN:            "{{secret.database_dsn}}",
					QueryTimeoutMs: 30000,
					MaxRows:        100,
				},
			},
			Actions: make([]dsl.Action, 0),
		},
	}

	functions := flattenParsedFunctions(actions)
	result.Stats.TotalFunctions = len(functions)
	if len(functions) == 0 {
		return nil, fmt.Errorf("no source functions found in action files")
	}

	for _, fn := range functions {
		if strings.EqualFold(strings.TrimSpace(fn.Name), "java_sql_connect") {
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.database.dsn", "{{secret.database_dsn}}", "config", "review_only", "Java JDBC connection details were redacted to {{secret.database_dsn}}; bind a governed DSN secret before replay.", SourceFunctionSupportReviewOnly))
			result.Stats.ReviewOnlyFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportReviewOnly)
			continue
		}
		if strings.EqualFold(strings.TrimSpace(fn.Name), "java_sql_dynamic") {
			appendDatabaseReviewAction(result, fn, "unresolved_dynamic_sql", "Java JDBC SQL was built dynamically and could not be recovered safely; preserved for manual review instead of fabricating an executable query.")
			continue
		}
		action, ok := convertDatabaseFunction(fn)
		if !ok {
			if fallback, mode, fallbackOK := convertSourceFunctionAction(fn); fallbackOK {
				appendSourceAction(result, fn, fallback, mode, sourceSupportNotes(fn, mode))
				continue
			}
			result.Stats.Skipped++
			result.Stats.UnsupportedFuncs = append(result.Stats.UnsupportedFuncs, fn.Name)
			appendSourceReviewMapping(result, sourceReviewMappingForFunction(fn, "metadata.import.preserved.unsupported_functions", fn.Name, "unsupported", "review_only", "No executable database mapping exists for this source function yet."))
			continue
		}
		actionIndex := actionCountForPhase(result.Script, fn.Phase)
		appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, sourceReviewActionPath(fn.Phase, actionIndex), action.Name, "action", "converted", "Converted to a generated APEX Load database action.", SourceFunctionSupportExecutable))
		appendActionForPhase(result.Script, fn.Phase, action)
		result.Stats.ConvertedActions++
		recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)
	}

	if len(result.Script.Init)+len(result.Script.Actions)+len(result.Script.Teardown) == 0 {
		result.Script = nil
		result.ReviewArtifact = NewReviewArtifact(markDetectedReviewOnly(detected, "No executable SQL statements were found in the database script."), nil, actions, map[string]any{
			"reason": "no_database_actions",
		})
		result.Warnings = append(result.Warnings, "No executable SQL statements were found; import was preserved as a review artifact.")
		return result, nil
	}
	if result.Stats.ConvertedActions == 0 {
		reviewDetected := markDetectedReviewOnly(detected, "No executable SQL statements were found in the database script.")
		result.DetectedProtocol = &reviewDetected
		result.ReviewArtifact = NewReviewArtifact(reviewDetected, nil, actions, map[string]any{
			"reason": "no_executable_database_actions",
		})
		result.Warnings = append(result.Warnings, "No executable SQL statements were found; database import actions are review-only.")
	}

	// Structure recognized source control flow (and strip the internal flow
	// tags) just like the HTTP converter.
	structureSourceControlFlow(result)

	if opts.RuntimeSettings != nil {
		mergeScriptConfig(&result.Script.Config, opts.RuntimeSettings)
	}
	result.Warnings = append(result.Warnings, "Database DSN was imported as {{secret.database_dsn}}; replace it with a governed connection secret before replay.")
	return result, nil
}

func appendDatabaseReviewAction(result *ConvertResult, fn ParsedFunction, reason string, notes string) {
	if result == nil || result.Script == nil {
		return
	}
	action := dsl.Action{
		Name:     databaseActionName(fn, reason),
		Type:     "source_review",
		Params:   map[string]any{"function": fn.Name},
		Metadata: sourceFunctionMetadata(fn),
	}
	makeSourceReviewActionDisclosureSafe(&action, fn, SourceFunctionSupportReviewOnly)
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	action.Metadata["review_reason"] = reason
	action.Metadata["source"] = "java_jdbc_idiom"
	actionIndex := actionCountForPhase(result.Script, fn.Phase)
	appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, sourceReviewActionPath(fn.Phase, actionIndex), action.Name, "action", "review_only", notes, SourceFunctionSupportReviewOnly))
	appendActionForPhase(result.Script, fn.Phase, action)
	result.Stats.ReviewOnlyFunctions++
	recordSourceFunctionSupport(result, SourceFunctionSupportReviewOnly)
}

func convertDatabaseFunction(fn ParsedFunction) (dsl.Action, bool) {
	name := strings.ToLower(strings.TrimSpace(fn.Name))
	switch {
	case strings.Contains(name, "begin"):
		return dsl.Action{Name: databaseActionName(fn, "db_begin"), Type: "db_begin", Metadata: sourceFunctionMetadata(fn)}, true
	case strings.Contains(name, "commit"):
		return dsl.Action{Name: databaseActionName(fn, "db_commit"), Type: "db_commit", Metadata: sourceFunctionMetadata(fn)}, true
	case strings.Contains(name, "rollback"):
		return dsl.Action{Name: databaseActionName(fn, "db_rollback"), Type: "db_rollback", Metadata: sourceFunctionMetadata(fn)}, true
	}

	sql := extractSQLText(fn)
	if sql == "" {
		return dsl.Action{}, false
	}
	actionType := "db_exec"
	if isQuerySQL(sql) {
		actionType = "db_query"
	}
	action := dsl.Action{
		Name:     databaseActionName(fn, actionType),
		Type:     actionType,
		Target:   sql,
		Params:   map[string]any{},
		Metadata: sourceFunctionMetadata(fn),
	}
	if binds := extractDatabaseBinds(fn, sql); len(binds) > 0 {
		action.Params["binds"] = binds
	}
	if len(action.Params) == 0 {
		action.Params = nil
	}
	return action, true
}

func extractSQLText(fn ParsedFunction) string {
	candidates := make([]string, 0, len(fn.RawArgs)+len(fn.Args)+1)
	candidates = append(candidates, fn.Body)
	for _, raw := range fn.RawArgs {
		candidates = append(candidates, raw)
	}
	for key, value := range fn.Args {
		lowerKey := strings.ToLower(key)
		if strings.Contains(lowerKey, "sql") || strings.Contains(lowerKey, "query") || strings.Contains(lowerKey, "statement") {
			candidates = append([]string{value}, candidates...)
		} else {
			candidates = append(candidates, value)
		}
	}
	for _, candidate := range candidates {
		candidate = normalizeSQLCandidate(candidate)
		if candidate == "" {
			continue
		}
		if match := sqlStatementPattern.FindString(candidate); strings.TrimSpace(match) != "" {
			return strings.TrimSpace(match)
		}
	}
	return ""
}

func normalizeSQLCandidate(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "SQL=")
	value = strings.TrimPrefix(value, "Sql=")
	value = strings.TrimPrefix(value, "Query=")
	value = strings.TrimPrefix(value, "Statement=")
	value = strings.ReplaceAll(value, `\"`, `"`)
	value = strings.ReplaceAll(value, `\r\n`, " ")
	value = strings.ReplaceAll(value, `\n`, " ")
	return strings.TrimSpace(value)
}

func isQuerySQL(sql string) bool {
	sql = strings.ToLower(strings.TrimSpace(sql))
	return strings.HasPrefix(sql, "select") || strings.HasPrefix(sql, "with") || strings.HasPrefix(sql, "show") || strings.HasPrefix(sql, "describe")
}

func extractDatabaseBinds(fn ParsedFunction, sql string) []any {
	// Explicit ordered binds (e.g. Java PreparedStatement setXxx, encoded into
	// RawArgs in positional order) take precedence over heuristic extraction.
	if ordered := orderedExplicitBinds(fn); len(ordered) > 0 {
		return ordered
	}
	binds := make([]any, 0)
	for key, value := range fn.Args {
		if strings.HasPrefix(strings.ToLower(key), "param") || strings.Contains(strings.ToLower(key), "bind") {
			binds = append(binds, value)
		}
	}
	for _, raw := range fn.RawArgs {
		if raw == sql || strings.Contains(raw, "SQL=") || strings.Contains(strings.ToLower(raw), "select ") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(raw), "param") || strings.Contains(strings.ToLower(raw), "bind") {
			binds = append(binds, raw)
		}
	}
	return binds
}

// orderedExplicitBinds returns the positional binds encoded into RawArgs with the
// javaSQLBindPrefix marker (in the order they were appended), or nil if none.
func orderedExplicitBinds(fn ParsedFunction) []any {
	ordered := make([]any, 0)
	for _, raw := range fn.RawArgs {
		if value, ok := strings.CutPrefix(raw, javaSQLBindPrefix); ok {
			ordered = append(ordered, value)
		}
	}
	return ordered
}

func databaseActionName(fn ParsedFunction, fallback string) string {
	return sanitizeName(convertutil.FirstNonEmpty(fn.StepName, firstNonEmptyArg(fn, "Name"), fn.Name, fallback))
}

func detectDatabaseDriver(detected DetectedProtocol) string {
	value := strings.ToLower(convertutil.FirstNonEmpty(detected.SourceType, detected.CanonicalSourceProtocol, detected.DisplayName))
	switch {
	case strings.Contains(value, "oracle"):
		return "oracle"
	case strings.Contains(value, "postgres"):
		return "postgres"
	case strings.Contains(value, "mysql"):
		return "mysql"
	default:
		return "odbc"
	}
}

func sourceFunctionMetadata(fn ParsedFunction) map[string]any {
	metadata := map[string]any{
		"source_function": fn.Name,
		"source_line":     fn.LineNumber,
	}
	if fn.Filename != "" {
		metadata["source_file"] = fn.Filename
	}
	if phase := normalizeSourcePhase(fn.Phase); phase != "" {
		metadata["phase"] = phase
	}
	if fn.EndLine > 0 && fn.EndLine != fn.LineNumber {
		metadata["source_end_line"] = fn.EndLine
	}
	return metadata
}

func markDetectedReviewOnly(detected DetectedProtocol, warning string) DetectedProtocol {
	detected.ImportMode = ImportModeReviewOnly
	detected.APEXLoadProtocol = ""
	if warning != "" {
		detected.Warnings = append(detected.Warnings, warning)
	}
	return detected
}
