package loadrunner

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

var sqlStatementPattern = regexp.MustCompile(`(?is)\b(select|insert\s+into|update|delete\s+from|create\s+table|drop\s+table|alter\s+table|exec(?:ute)?|call|begin|commit|rollback)\b[\s\S]*`)

func ConvertDatabase(actions []*ParsedAction, opts ConvertOptions, detected DetectedProtocol) (*ConvertResult, error) {
	result := &ConvertResult{
		DetectedProtocol: &detected,
		Warnings:         append([]string(nil), detected.Warnings...),
		Script: &dsl.Script{
			Name:          firstNonEmpty(opts.ScriptName, "Converted_LR_Database_Script"),
			Version:       "1.0.0",
			Protocol:      "database",
			GeneratedBy:   "apexload-loadrunner-converter",
			GeneratedFrom: "loadrunner",
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
		return nil, fmt.Errorf("no LoadRunner functions found in action files")
	}

	for _, fn := range functions {
		action, ok := convertDatabaseFunction(fn)
		if !ok {
			result.Stats.Skipped++
			result.Stats.UnsupportedFuncs = append(result.Stats.UnsupportedFuncs, fn.Name)
			continue
		}
		result.Script.Actions = append(result.Script.Actions, action)
		result.Stats.ConvertedActions++
	}

	if len(result.Script.Actions) == 0 {
		result.Script = nil
		result.ReviewArtifact = NewReviewArtifact(markDetectedReviewOnly(detected, "No executable SQL statements were found in the database script."), nil, actions, map[string]any{
			"reason": "no_database_actions",
		})
		result.Warnings = append(result.Warnings, "No executable SQL statements were found; import was preserved as a review artifact.")
		return result, nil
	}

	if opts.RuntimeSettings != nil {
		mergeScriptConfig(&result.Script.Config, opts.RuntimeSettings)
	}
	result.Warnings = append(result.Warnings, "Database DSN was imported as {{secret.database_dsn}}; replace it with a governed connection secret before replay.")
	return result, nil
}

func convertDatabaseFunction(fn ParsedFunction) (dsl.Action, bool) {
	name := strings.ToLower(strings.TrimSpace(fn.Name))
	switch {
	case strings.Contains(name, "begin") || strings.Contains(name, "start_transaction"):
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

func databaseActionName(fn ParsedFunction, fallback string) string {
	return sanitizeName(firstNonEmpty(fn.StepName, firstNonEmptyArg(fn, "Name"), fn.Name, fallback))
}

func detectDatabaseDriver(detected DetectedProtocol) string {
	value := strings.ToLower(firstNonEmpty(detected.SourceType, detected.CanonicalLoadRunnerProtocol, detected.DisplayName))
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
	return map[string]any{
		"source_function": fn.Name,
		"source_line":     fn.LineNumber,
	}
}

func markDetectedReviewOnly(detected DetectedProtocol, warning string) DetectedProtocol {
	detected.ImportMode = ImportModeReviewOnly
	detected.APEXLoadProtocol = ""
	if warning != "" {
		detected.Warnings = append(detected.Warnings, warning)
	}
	return detected
}
