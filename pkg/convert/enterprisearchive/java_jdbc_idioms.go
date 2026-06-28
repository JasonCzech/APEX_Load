package enterprisearchive

import (
	"regexp"
	"strings"
)

var (
	javaSQLAssignRe      = regexp.MustCompile(`(?i)^\s*(?:final\s+)?String\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+);\s*$`)
	javaPreparedStmtRe   = regexp.MustCompile(`(?i)^\s*(?:PreparedStatement|java\.sql\.PreparedStatement)?\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*.+?\.prepareStatement\s*\((.+)\)\s*;\s*$`)
	javaDriverManagerRe  = regexp.MustCompile(`(?i)\bDriverManager\.getConnection\s*\(`)
	javaStatementExecRe  = regexp.MustCompile(`(?i)\.execute(?:Query|Update)?\s*\((.*)\)\s*;?\s*$`)
	javaStatementNoArgRe = regexp.MustCompile(`(?i)^([A-Za-z_][A-Za-z0-9_]*)\.execute(?:Query|Update)?\s*\(\s*\)\s*;?\s*$`)
	javaSetAutoCommitRe  = regexp.MustCompile(`(?i)\.setAutoCommit\s*\(\s*false\s*\)\s*;?\s*$`)
	javaSQLCommitRe      = regexp.MustCompile(`(?i)\.commit\s*\(\s*\)\s*;?\s*$`)
	javaSQLRollbackRe    = regexp.MustCompile(`(?i)\.rollback\s*\(\s*\)\s*;?\s*$`)
)

func javaDatabaseParsedActions(program javaProgram, files map[string]string) []*ParsedAction {
	sqlVars := map[string]string{}
	prepared := map[string]string{}
	return javaParsedActionsFor(program, func(statement javaStatement, phase string) (ParsedFunction, bool) {
		text := javaNormalizeStatementText(statement.Text)
		if m := javaSQLAssignRe.FindStringSubmatch(text); len(m) == 3 {
			env := javaStringEnv{Vars: sqlVars}
			if value, ok := env.eval(m[2], files); ok && looksLikeSQLText(value) {
				sqlVars[m[1]] = value
			}
			return ParsedFunction{}, false
		}
		if m := javaPreparedStmtRe.FindStringSubmatch(text); len(m) == 3 {
			if sql := javaResolveSQLExpression(m[2], sqlVars, files); sql != "" {
				prepared[strings.TrimSpace(m[1])] = sql
			}
			return ParsedFunction{}, false
		}
		if javaDriverManagerRe.MatchString(text) {
			return javaDatabaseFunction(statement, phase, "java_sql_connect", ""), true
		}
		if javaSetAutoCommitRe.MatchString(text) {
			return javaDatabaseFunction(statement, phase, "java_sql_begin", ""), true
		}
		if javaSQLCommitRe.MatchString(text) {
			return javaDatabaseFunction(statement, phase, "java_sql_commit", ""), true
		}
		if javaSQLRollbackRe.MatchString(text) {
			return javaDatabaseFunction(statement, phase, "java_sql_rollback", ""), true
		}
		if m := javaStatementNoArgRe.FindStringSubmatch(text); len(m) == 2 {
			if sql := prepared[strings.TrimSpace(m[1])]; sql != "" {
				return javaDatabaseFunction(statement, phase, "java_sql_execute", sql), true
			}
		}
		if m := javaStatementExecRe.FindStringSubmatch(text); len(m) == 2 {
			if sql := javaResolveSQLExpression(m[1], sqlVars, files); sql != "" {
				return javaDatabaseFunction(statement, phase, "java_sql_execute", sql), true
			}
		}
		if fn, ok := javaLRToParsedFunction(statement, phase); ok {
			switch fn.Name {
			case "lr_start_transaction", "lr_end_transaction", "lr_start_sub_transaction", "lr_end_sub_transaction", "lr_think_time",
				"lr_error_message", "lr_message", "lr_log_message", "lr_output_message":
				return fn, true
			}
		}
		return ParsedFunction{}, false
	})
}

func javaResolveSQLExpression(expr string, vars map[string]string, files map[string]string) string {
	env := javaStringEnv{Vars: vars}
	value, ok := env.eval(expr, files)
	if !ok {
		value = strings.TrimSpace(expr)
		if resolved, found := vars[value]; found {
			value = resolved
		}
	}
	value = strings.TrimSpace(value)
	if !looksLikeSQLText(value) {
		return ""
	}
	return value
}

func javaDatabaseFunction(statement javaStatement, phase, name, sql string) ParsedFunction {
	fn := ParsedFunction{
		Name:       name,
		StepName:   strings.TrimPrefix(name, "java_"),
		Phase:      phase,
		Filename:   statement.File,
		LineNumber: statement.StartLine,
		EndLine:    statement.EndLine,
		RawBlock:   statement.Text,
		Args:       map[string]string{},
		Headers:    map[string]string{},
		Body:       sql,
	}
	if sql != "" {
		fn.Args["SQL"] = sql
		fn.RawArgs = []string{sql}
	}
	return fn
}
