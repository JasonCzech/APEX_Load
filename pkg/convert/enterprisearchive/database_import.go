package enterprisearchive

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

var sqlStatementPattern = regexp.MustCompile(`(?is)\b(select|insert\s+into|update|delete\s+from|create\s+table|drop\s+table|alter\s+table|exec(?:ute)?|call|begin|commit|rollback)\b[\s\S]*`)

const (
	maxRetainedJavaJDBCConnections = 256
	maxRetainedJavaJDBCValueBytes  = 64 << 10
	maxRetainedJavaJDBCTotalBytes  = 2 << 20
)

type retainedJavaJDBCConnection struct {
	Driver    string `json:"driver,omitempty"`
	DSN       string `json:"dsn"`
	Reason    string `json:"reason"`
	SourceRef string `json:"source_ref"`
}

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

	var (
		sawJavaJDBC       bool
		jdbcConfigSet     bool
		jdbcReviewReason  []string
		jdbcSourceRefs    []string
		retainedDrafts    []retainedJavaJDBCConnection
		reviewConnections []ParsedFunction
	)
	for _, fn := range functions {
		if strings.EqualFold(strings.TrimSpace(fn.Name), "java_sql_connect") {
			sawJavaJDBC = true
			sourceRef := databaseSourceRef(fn)
			connection := fn.javaJDBCConnection
			reason := ""
			retainedMaterial := ""
			if connection == nil {
				reason = "jdbc_connection_arguments_unavailable"
				retainedMaterial = fn.RawBlock
			} else {
				reason = strings.TrimSpace(connection.ReviewReason)
				retainedInConfig := false
				if strings.TrimSpace(connection.Driver) != "" &&
					strings.TrimSpace(connection.DSN) != "" {
					if !jdbcConfigSet {
						result.Script.Config.Database.Driver = connection.Driver
						result.Script.Config.Database.DSN = connection.DSN
						jdbcConfigSet = true
						retainedInConfig = true
					} else if result.Script.Config.Database.Driver != connection.Driver ||
						result.Script.Config.Database.DSN != connection.DSN {
						reason = "jdbc_connection_configuration_conflict"
					} else {
						retainedInConfig = true
					}
				} else if reason == "" {
					reason = "jdbc_connection_configuration_unresolved"
				}
				if !retainedInConfig {
					retainedMaterial = connection.DSN
					if strings.TrimSpace(retainedMaterial) == "" {
						// Dynamic, malformed, and unsupported overloads cannot
						// always yield a DSN. Keep the exact bounded connection
						// statement in the same opaque server-owned envelope so
						// repair never requires the operator to re-enter a
						// literal that was already present in the import.
						retainedMaterial = fn.RawBlock
					}
				}
				if strings.TrimSpace(retainedMaterial) != "" {
					retainedDrafts = append(retainedDrafts, retainedJavaJDBCConnection{
						Driver:    connection.Driver,
						DSN:       retainedMaterial,
						Reason:    convertutil.FirstNonEmpty(reason, "jdbc_connection_configuration_unresolved"),
						SourceRef: sourceRef,
					})
				}
			}
			if connection == nil && strings.TrimSpace(retainedMaterial) != "" {
				retainedDrafts = append(retainedDrafts, retainedJavaJDBCConnection{
					DSN:       retainedMaterial,
					Reason:    reason,
					SourceRef: sourceRef,
				})
			}

			if reason == "" {
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(
					fn,
					"config.database.dsn",
					"retained JDBC connection",
					"config",
					"converted",
					"Java JDBC URL, username, password, and driver semantics were retained in the executable database configuration; ordinary disclosure surfaces mask the DSN.",
					SourceFunctionSupportExecutable,
				))
				result.Stats.ContributorFunctions++
				recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)
			} else {
				jdbcReviewReason = append(jdbcReviewReason, reason)
				jdbcSourceRefs = append(jdbcSourceRefs, sourceRef)
				reviewConnections = append(reviewConnections, fn)
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(
					fn,
					"config.database.dsn",
					"retained JDBC connection draft",
					"config",
					"review_only",
					"Java JDBC connection material was retained where representable, but the connection shape requires operator repair before execution.",
					SourceFunctionSupportReviewOnly,
				))
				recordSourceFunctionSupport(result, SourceFunctionSupportReviewOnly)
			}
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
	if len(result.Script.Init)+len(result.Script.Actions)+len(result.Script.Teardown) == 0 &&
		len(reviewConnections) > 0 {
		for _, fn := range reviewConnections {
			reason := "jdbc_connection_arguments_unavailable"
			if fn.javaJDBCConnection != nil {
				reason = "jdbc_connection_configuration_unresolved"
			}
			if fn.javaJDBCConnection != nil &&
				strings.TrimSpace(fn.javaJDBCConnection.ReviewReason) != "" {
				reason = strings.TrimSpace(fn.javaJDBCConnection.ReviewReason)
			}
			appendDatabaseConnectionReviewPlaceholder(
				result,
				fn,
				reason,
				"The exact Java JDBC connection source was retained, but its connection shape requires operator repair before execution.",
			)
		}
	}
	if err := preserveJavaJDBCConnectionDrafts(result.Script, retainedDrafts); err != nil {
		return nil, err
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
	if len(jdbcReviewReason) > 0 {
		applyDatabaseImportReviewGate(
			result.Script,
			jdbcReviewReason,
			jdbcSourceRefs,
		)
		result.Warnings = append(
			result.Warnings,
			"Java JDBC connection behavior requires review; source connection material was retained where representable and every affected database action is blocked until the gate is resolved.",
		)
	}

	if opts.RuntimeSettings != nil {
		mergeScriptConfig(&result.Script.Config, opts.RuntimeSettings)
	}
	switch {
	case sawJavaJDBC && jdbcConfigSet && len(jdbcReviewReason) == 0:
		result.Warnings = append(
			result.Warnings,
			"Java JDBC connection credentials were retained in the executable destination configuration; review their scope and rotate them under normal operational policy.",
		)
	case sawJavaJDBC && jdbcConfigSet:
		result.Warnings = append(
			result.Warnings,
			"Java JDBC connection fields were retained in the blocked destination configuration; repair the adapter or connection conflict without re-entering source credentials.",
		)
	case sawJavaJDBC && !jdbcConfigSet:
		result.Warnings = append(
			result.Warnings,
			"Java JDBC connection expressions could not be resolved into a runnable DSN; the draft remains blocked until the retained source behavior is repaired.",
		)
	default:
		result.Warnings = append(result.Warnings, "Database DSN was imported as {{secret.database_dsn}}; replace it with a governed connection secret before replay.")
	}
	return result, nil
}

func appendDatabaseConnectionReviewPlaceholder(
	result *ConvertResult,
	fn ParsedFunction,
	reason string,
	notes string,
) {
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
	importreview.ApplyToAction(&action, []importreview.Finding{{
		Code:       "import_review_required",
		Severity:   diag.Blocker,
		Element:    "Java JDBC behavior",
		Detail:     notes,
		Location:   databaseSourceRef(fn),
		Source:     "enterprise_script_archive",
		ReasonCode: reason,
		SourceRef:  databaseSourceRef(fn),
		Resolution: "replace_behavior_and_remove_gate",
		Gate:       importreview.GateExecution,
	}})
	appendActionForPhase(result.Script, fn.Phase, action)
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
	importreview.ApplyToAction(&action, []importreview.Finding{{
		Code:       "import_review_required",
		Severity:   diag.Blocker,
		Element:    "Java JDBC behavior",
		Detail:     notes,
		Location:   databaseSourceRef(fn),
		Source:     "enterprise_script_archive",
		ReasonCode: reason,
		SourceRef:  databaseSourceRef(fn),
		Resolution: "replace_behavior_and_remove_gate",
		Gate:       importreview.GateExecution,
	}})
	actionIndex := actionCountForPhase(result.Script, fn.Phase)
	appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, sourceReviewActionPath(fn.Phase, actionIndex), action.Name, "action", "review_only", notes, SourceFunctionSupportReviewOnly))
	appendActionForPhase(result.Script, fn.Phase, action)
	recordSourceFunctionSupport(result, SourceFunctionSupportReviewOnly)
}

func applyDatabaseImportReviewGate(
	script *dsl.Script,
	reasons []string,
	sourceRefs []string,
) {
	if script == nil {
		return
	}
	reasons = uniqueSortedDatabaseStrings(reasons)
	sourceRefs = uniqueSortedDatabaseStrings(sourceRefs)
	_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		findings := make([]importreview.Finding, 0, len(reasons)*max(len(sourceRefs), 1))
		refs := sourceRefs
		if len(refs) == 0 {
			refs = []string{"config.database.dsn"}
		}
		for _, reason := range reasons {
			for _, sourceRef := range refs {
				findings = append(findings, importreview.Finding{
					Code:       "import_review_required",
					Severity:   diag.Blocker,
					Element:    "Java JDBC connection",
					Detail:     "The imported JDBC connection cannot execute with exact source semantics until the retained connection behavior is repaired.",
					Location:   ref.Path,
					ActionPath: ref.Path,
					Source:     "enterprise_script_archive",
					ReasonCode: reason,
					SourceRef:  sourceRef,
					Resolution: "replace_behavior_and_remove_gate",
					Gate:       importreview.GateExecution,
				})
			}
		}
		importreview.ApplyToAction(ref.Action, findings)
		return nil
	})
}

func databaseSourceRef(fn ParsedFunction) string {
	file := strings.TrimSpace(fn.Filename)
	if file == "" {
		file = "java source"
	}
	if fn.LineNumber > 0 {
		return fmt.Sprintf("%s line %d", file, fn.LineNumber)
	}
	return file
}

func uniqueSortedDatabaseStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func preserveJavaJDBCConnectionDrafts(
	script *dsl.Script,
	drafts []retainedJavaJDBCConnection,
) error {
	if script == nil || len(drafts) == 0 {
		return nil
	}
	if len(drafts) > maxRetainedJavaJDBCConnections {
		return fmt.Errorf(
			"retain Java JDBC connection source: %d connections exceed the %d-item limit",
			len(drafts),
			maxRetainedJavaJDBCConnections,
		)
	}
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if strings.TrimSpace(script.Metadata.Import.Source) == "" {
		script.Metadata.Import.Source = "enterprise_script_archive"
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = make(map[string]any)
	}
	encode := func(value string) string {
		return base64.StdEncoding.EncodeToString([]byte(value))
	}
	connections := make([]map[string]any, 0, len(drafts))
	totalBytes := 0
	for _, draft := range drafts {
		if strings.TrimSpace(draft.DSN) == "" {
			return fmt.Errorf("retain Java JDBC connection source: empty protected source")
		}
		for _, item := range []struct {
			field string
			value string
		}{
			{field: "driver", value: draft.Driver},
			{field: "connection", value: draft.DSN},
		} {
			field, value := item.field, item.value
			if len(value) > maxRetainedJavaJDBCValueBytes {
				return fmt.Errorf(
					"retain Java JDBC connection source: %s exceeds the %d-byte limit",
					field,
					maxRetainedJavaJDBCValueBytes,
				)
			}
			totalBytes += len(value)
		}
		if len(draft.Reason) > 4096 || len(draft.SourceRef) > 4096 {
			return fmt.Errorf("retain Java JDBC connection source: review metadata exceeds the 4096-byte limit")
		}
		totalBytes += len(draft.Reason) + len(draft.SourceRef)
		if totalBytes > maxRetainedJavaJDBCTotalBytes {
			return fmt.Errorf(
				"retain Java JDBC connection source: protected source exceeds the %d-byte total limit",
				maxRetainedJavaJDBCTotalBytes,
			)
		}
		connections = append(connections, map[string]any{
			"driver_base64": encode(draft.Driver),
			"dsn_base64":    encode(draft.DSN),
			"reason":        draft.Reason,
			"source_ref":    draft.SourceRef,
		})
	}
	script.Metadata.Import.Preserved[dsl.ImportPreservedEnterpriseJavaJDBCSourceKey] =
		map[string]any{
			"version":     1,
			"connections": connections,
		}
	return nil
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
	// ParsedFunction.Args is a map, so ranging it directly would let Go's
	// randomized map order pick the winning statement whenever a function
	// carries more than one SQL-bearing argument. Order the keys first (the
	// same case-insensitive order extractDatabaseBinds uses) so the imported
	// SQL is a pure function of the parsed source.
	argKeys := make([]string, 0, len(fn.Args))
	for key := range fn.Args {
		argKeys = append(argKeys, key)
	}
	sort.SliceStable(argKeys, func(i, j int) bool {
		leftKey := strings.ToLower(argKeys[i])
		rightKey := strings.ToLower(argKeys[j])
		if leftKey != rightKey {
			return leftKey < rightKey
		}
		return argKeys[i] < argKeys[j]
	})
	namedSQLArgs := make([]string, 0, len(argKeys))
	otherArgs := make([]string, 0, len(argKeys))
	for _, key := range argKeys {
		lowerKey := strings.ToLower(key)
		if strings.Contains(lowerKey, "sql") || strings.Contains(lowerKey, "query") || strings.Contains(lowerKey, "statement") {
			namedSQLArgs = append(namedSQLArgs, fn.Args[key])
			continue
		}
		otherArgs = append(otherArgs, fn.Args[key])
	}
	// Precedence is unchanged: explicitly SQL-named args beat the body, which
	// beats positional raw args, which beat unnamed args.
	candidates := make([]string, 0, len(fn.RawArgs)+len(fn.Args)+1)
	candidates = append(candidates, namedSQLArgs...)
	candidates = append(candidates, fn.Body)
	candidates = append(candidates, fn.RawArgs...)
	candidates = append(candidates, otherArgs...)
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
	type namedBind struct {
		key   string
		value string
	}
	namedBinds := make([]namedBind, 0)
	for key, value := range fn.Args {
		if strings.HasPrefix(strings.ToLower(key), "param") || strings.Contains(strings.ToLower(key), "bind") {
			namedBinds = append(namedBinds, namedBind{key: key, value: value})
		}
	}
	// ParsedFunction.Args is a map, so its iteration order cannot represent
	// positional database binds. Prefer the numeric suffix convention used by
	// legacy converters (param1, param2, …), then fall back to a stable
	// case-insensitive key order for named binds that have no position.
	sort.SliceStable(namedBinds, func(i, j int) bool {
		leftPosition, leftHasPosition := databaseBindPosition(namedBinds[i].key)
		rightPosition, rightHasPosition := databaseBindPosition(namedBinds[j].key)
		switch {
		case leftHasPosition && rightHasPosition && leftPosition != rightPosition:
			return leftPosition < rightPosition
		case leftHasPosition != rightHasPosition:
			return leftHasPosition
		default:
			leftKey := strings.ToLower(namedBinds[i].key)
			rightKey := strings.ToLower(namedBinds[j].key)
			if leftKey != rightKey {
				return leftKey < rightKey
			}
			return namedBinds[i].key < namedBinds[j].key
		}
	})
	binds := make([]any, 0, len(namedBinds)+len(fn.RawArgs))
	for _, bind := range namedBinds {
		binds = append(binds, bind.value)
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

func databaseBindPosition(key string) (int, bool) {
	key = strings.TrimSpace(key)
	end := len(key)
	start := end
	for start > 0 && key[start-1] >= '0' && key[start-1] <= '9' {
		start--
	}
	if start == end {
		return 0, false
	}
	if start > 0 && key[start-1] == '-' {
		return 0, false
	}
	position, err := strconv.Atoi(key[start:])
	if err != nil || position < 0 {
		return 0, false
	}
	return position, true
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
