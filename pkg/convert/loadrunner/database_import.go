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

	dbContext := &databaseConversionContext{}
	for _, fn := range functions {
		action, mode, ok := dbContext.convert(fn)
		if !ok {
			if fallback, mode, fallbackOK := convertLoadRunnerFunctionAction(fn); fallbackOK {
				appendLoadRunnerAction(result, fn, fallback, mode, loadRunnerSupportNotes(fn, mode))
				continue
			}
			result.Stats.Skipped++
			result.Stats.UnsupportedFuncs = append(result.Stats.UnsupportedFuncs, fn.Name)
			appendSourceReviewMapping(result, sourceReviewMappingForFunction(fn, "metadata.import.preserved.unsupported_functions", fn.Name, "unsupported", "review_only", "No executable database mapping exists for this LoadRunner function yet."))
			continue
		}
		if action.Type == "" {
			continue
		}
		actionIndex := len(result.Script.Actions)
		appendConvertedProtocolAction(result, fn, action, mode, "Converted to a generated APEX Load database action.")
		if action.Type == "db_query" || action.Type == "db_exec" {
			appendDatabaseContributorMappings(result, actionIndex, action, dbContext.takeContributors())
		}
	}
	if action, fn, ok := dbContext.flushPendingStatement(); ok {
		appendConvertedProtocolAction(result, fn, action, LoadRunnerSupportExecutable, "Converted deferred LoadRunner database statement to a generated APEX Load database action.")
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

type databaseConversionContext struct {
	pendingSQL          string
	pendingBinds        []any
	pendingFunction     ParsedFunction
	pendingContributors []ParsedFunction
}

func (ctx *databaseConversionContext) convert(fn ParsedFunction) (dsl.Action, LoadRunnerSupportMode, bool) {
	name := strings.ToLower(strings.TrimSpace(fn.Name))
	switch {
	case name == "lrd_init" || name == "lrd_handle_alloc" || name == "lrd_handle_free" || name == "lrd_open_cursor" || name == "lrd_close_cursor" || name == "lrd_db_option":
		return databaseNativeAction(fn, "loadrunner_database"), supportModeForFunction(fn, LoadRunnerSupportNativeNoop), true
	case name == "lrd_open_connection" || name == "lr_db_connect":
		return dsl.Action{Name: databaseActionName(fn, "db_connect"), Type: "db_connect", Metadata: sourceFunctionMetadata(fn), Params: databaseConnectionParams(fn)}, LoadRunnerSupportExecutable, true
	case name == "lrd_close_connection" || name == "lr_db_disconnect":
		return dsl.Action{Name: databaseActionName(fn, "db_disconnect"), Type: "db_disconnect", Metadata: sourceFunctionMetadata(fn)}, LoadRunnerSupportExecutable, true
	case strings.Contains(name, "begin") || strings.Contains(name, "start_transaction"):
		return dsl.Action{Name: databaseActionName(fn, "db_begin"), Type: "db_begin", Metadata: sourceFunctionMetadata(fn)}, LoadRunnerSupportExecutable, true
	case strings.Contains(name, "commit"):
		return dsl.Action{Name: databaseActionName(fn, "db_commit"), Type: "db_commit", Metadata: sourceFunctionMetadata(fn)}, LoadRunnerSupportExecutable, true
	case strings.Contains(name, "rollback"):
		return dsl.Action{Name: databaseActionName(fn, "db_rollback"), Type: "db_rollback", Metadata: sourceFunctionMetadata(fn)}, LoadRunnerSupportExecutable, true
	case name == "lrd_stmt":
		sql := extractSQLText(fn)
		if sql == "" {
			return databaseNativeAction(fn, "loadrunner_database"), supportModeForFunction(fn, LoadRunnerSupportNativeNoop), true
		}
		ctx.pendingSQL = sql
		ctx.pendingBinds = mergeDatabaseBinds(ctx.pendingBinds, extractDatabaseBinds(fn, sql))
		ctx.pendingFunction = fn
		ctx.pendingContributors = append(ctx.pendingContributors, fn)
		return dsl.Action{}, LoadRunnerSupportExecutable, true
	case name == "lrd_exec":
		sql := extractSQLText(fn)
		binds := extractDatabaseBinds(fn, sql)
		if sql == "" {
			sql = ctx.pendingSQL
			binds = mergeDatabaseBinds(ctx.pendingBinds, binds)
		}
		if sql == "" {
			return databaseNativeAction(fn, "loadrunner_database"), supportModeForFunction(fn, LoadRunnerSupportNativeNoop), true
		}
		ctx.clearStatement()
		return databaseSQLAction(fn, sql, binds), LoadRunnerSupportExecutable, true
	case name == "lrd_fetch":
		if ctx.pendingSQL != "" && isQuerySQL(ctx.pendingSQL) {
			action := databaseSQLAction(fn, ctx.pendingSQL, ctx.pendingBinds)
			ctx.clearStatement()
			return action, LoadRunnerSupportExecutable, true
		}
		return databaseNativeAction(fn, "loadrunner_database"), supportModeForFunction(fn, LoadRunnerSupportNativeNoop), true
	case name == "lrd_bind_placeholder" || name == "lrd_assign_bind" || name == "lrd_assign":
		ctx.pendingBinds = mergeDatabaseBinds(ctx.pendingBinds, extractDatabaseBinds(fn, ""))
		return databaseNativeAction(fn, "loadrunner_database"), supportModeForFunction(fn, LoadRunnerSupportExecutable), true
	case name == "lrd_bind_col" || name == "lrd_save_value" || name == "lrd_save_col" || name == "lr_db_getValue":
		return databaseNativeAction(fn, "loadrunner_database"), supportModeForFunction(fn, LoadRunnerSupportExecutable), true
	}

	sql := extractSQLText(fn)
	if sql == "" {
		return dsl.Action{}, "", false
	}
	return databaseSQLAction(fn, sql, extractDatabaseBinds(fn, sql)), LoadRunnerSupportExecutable, true
}

func (ctx *databaseConversionContext) flushPendingStatement() (dsl.Action, ParsedFunction, bool) {
	if ctx == nil || strings.TrimSpace(ctx.pendingSQL) == "" {
		return dsl.Action{}, ParsedFunction{}, false
	}
	action := databaseSQLAction(ctx.pendingFunction, ctx.pendingSQL, ctx.pendingBinds)
	fn := ctx.pendingFunction
	ctx.pendingContributors = nil
	ctx.clearStatement()
	return action, fn, true
}

func (ctx *databaseConversionContext) clearStatement() {
	ctx.pendingSQL = ""
	ctx.pendingBinds = nil
	ctx.pendingFunction = ParsedFunction{}
}

func (ctx *databaseConversionContext) takeContributors() []ParsedFunction {
	if ctx == nil || len(ctx.pendingContributors) == 0 {
		return nil
	}
	contributors := append([]ParsedFunction(nil), ctx.pendingContributors...)
	ctx.pendingContributors = nil
	return contributors
}

func appendDatabaseContributorMappings(result *ConvertResult, actionIndex int, action dsl.Action, contributors []ParsedFunction) {
	if len(contributors) == 0 {
		return
	}
	targetPath := fmt.Sprintf("actions[%d]", actionIndex)
	for _, fn := range contributors {
		mode := supportModeForFunction(fn, LoadRunnerSupportExecutable)
		appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, targetPath, action.Name, "action", "contributor", "Contributes statement text or bind state to the generated APEX Load database action.", mode))
		recordLoadRunnerFunctionSupport(result, mode)
	}
}

func databaseSQLAction(fn ParsedFunction, sql string, binds []any) dsl.Action {
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
	if len(binds) > 0 {
		action.Params["binds"] = binds
	}
	if len(action.Params) == 0 {
		action.Params = nil
	}
	return action
}

func databaseNativeAction(fn ParsedFunction, actionType string) dsl.Action {
	def, ok := lookupLoadRunnerFunction(fn.Name)
	if !ok {
		def = LoadRunnerFunctionDefinition{Name: strings.ToLower(fn.Name), Family: "database", SupportMode: LoadRunnerSupportNativeNoop, Reason: defaultLoadRunnerReason("database", LoadRunnerSupportNativeNoop)}
	}
	return convertGenericLoadRunnerAction(fn, actionType, def)
}

func supportModeForFunction(fn ParsedFunction, fallback LoadRunnerSupportMode) LoadRunnerSupportMode {
	if def, ok := lookupLoadRunnerFunction(fn.Name); ok && def.SupportMode != "" {
		return def.SupportMode
	}
	return fallback
}

func databaseConnectionParams(fn ParsedFunction) map[string]any {
	params := map[string]any{}
	for _, key := range []string{"ConnectionString", "Connection", "DSN", "User", "UserName", "Password"} {
		if value := firstNonEmptyArg(fn, key); value != "" {
			params[strings.ToLower(key)] = value
		}
	}
	if len(params) == 0 {
		return nil
	}
	return params
}

func mergeDatabaseBinds(base []any, additions []any) []any {
	if len(base) == 0 {
		return append([]any(nil), additions...)
	}
	merged := append([]any(nil), base...)
	merged = append(merged, additions...)
	return merged
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
	for _, arg := range fn.CallArgs {
		key := strings.ToLower(strings.TrimSpace(arg.Name))
		if key == "" {
			continue
		}
		if strings.HasPrefix(key, "param") || strings.Contains(key, "bind") {
			binds = append(binds, arg.Value)
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
	metadata := map[string]any{
		"source_function": fn.Name,
		"source_line":     fn.LineNumber,
	}
	if fn.Filename != "" {
		metadata["source_file"] = fn.Filename
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
