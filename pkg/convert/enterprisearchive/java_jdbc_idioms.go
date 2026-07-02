package enterprisearchive

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const javaSQLBindPrefix = "__bind__="

var (
	javaSQLAssignRe     = regexp.MustCompile(`(?i)^\s*(?:final\s+)?String\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+);\s*$`)
	javaPreparedStmtRe  = regexp.MustCompile(`(?i)^\s*(?:PreparedStatement|java\.sql\.PreparedStatement)?\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*.+?\.prepareStatement\s*\((.+)\)\s*;\s*$`)
	javaDriverManagerRe = regexp.MustCompile(`(?i)\bDriverManager\.getConnection\s*\(`)
	javaStatementExecRe = regexp.MustCompile(`(?i)\.execute(?:Query|Update)?\s*\((.*)\)\s*;?\s*$`)
	// Also matches the common assignment form `ResultSet rs = ps.executeQuery();`
	// (the receiver is the last token before .execute), not only `ps.executeQuery();`.
	javaStatementNoArgRe = regexp.MustCompile(`(?i)^(?:.*=\s*)?([A-Za-z_][A-Za-z0-9_]*)\.execute(?:Query|Update)?\s*\(\s*\)\s*;?\s*$`)
	javaSetAutoCommitRe  = regexp.MustCompile(`(?i)\.setAutoCommit\s*\(\s*false\s*\)\s*;?\s*$`)
	javaSQLTxControlRe   = regexp.MustCompile(`(?i)^\s*([A-Za-z_][A-Za-z0-9_]*)\.(commit|rollback)\s*\(\s*\)\s*;?\s*$`)
	// ps.setString(1, "x") / ps.setInt(2, id) — captures receiver, 1-based index, value.
	javaSetBindRe = regexp.MustCompile(`(?i)^\s*([A-Za-z_][A-Za-z0-9_]*)\.set[A-Za-z]+\s*\(\s*([0-9]+)\s*,\s*(.+?)\s*\)\s*;?\s*$`)
)

func javaDatabaseParsedActions(program javaProgram, files map[string]string) []*ParsedAction {
	sqlVars := map[string]string{}
	prepared := map[string]string{}
	preparedDynamic := map[string]bool{}
	binds := map[string]map[int]string{}
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
			} else {
				preparedDynamic[strings.TrimSpace(m[1])] = true
				return javaDynamicSQLFunction(statement, phase, m[2]), true
			}
			return ParsedFunction{}, false
		}
		if m := javaSetBindRe.FindStringSubmatch(text); len(m) == 4 {
			if idx, err := strconv.Atoi(m[2]); err == nil {
				receiver := strings.TrimSpace(m[1])
				if binds[receiver] == nil {
					binds[receiver] = map[int]string{}
				}
				binds[receiver][idx] = javaBindValue(m[3], sqlVars, files)
			}
			return ParsedFunction{}, false
		}
		if javaDriverManagerRe.MatchString(text) {
			return javaDatabaseFunction(statement, phase, "java_sql_connect", ""), true
		}
		if javaSetAutoCommitRe.MatchString(text) {
			return javaDatabaseFunction(statement, phase, "java_sql_begin", ""), true
		}
		if name, ok := javaSQLTransactionControlFunction(text); ok {
			return javaDatabaseFunction(statement, phase, name, ""), true
		}
		if m := javaStatementNoArgRe.FindStringSubmatch(text); len(m) == 2 {
			receiver := strings.TrimSpace(m[1])
			if sql := prepared[receiver]; sql != "" {
				return javaDatabaseFunctionWithBinds(statement, phase, "java_sql_execute", sql, binds[receiver]), true
			}
			if preparedDynamic[receiver] {
				return javaDynamicSQLFunction(statement, phase, receiver), true
			}
		}
		if m := javaStatementExecRe.FindStringSubmatch(text); len(m) == 2 {
			if sql := javaResolveSQLExpression(m[1], sqlVars, files); sql != "" {
				return javaDatabaseFunction(statement, phase, "java_sql_execute", sql), true
			}
			return javaDynamicSQLFunction(statement, phase, m[1]), true
		}
		if fn, ok := javaLRToParsedFunction(statement, phase); ok {
			if javaLRFunctionAllowedInExecutableLane(fn.Name) {
				return fn, true
			}
		}
		return ParsedFunction{}, false
	})
}

func javaDynamicSQLFunction(statement javaStatement, phase string, expr string) ParsedFunction {
	fn := javaDatabaseFunction(statement, phase, "java_sql_dynamic", "")
	expr = strings.TrimSpace(expr)
	if expr != "" {
		fn.Args["Expression"] = expr
		fn.RawArgs = []string{expr}
	}
	return fn
}

func javaSQLTransactionControlFunction(text string) (string, bool) {
	matches := javaSQLTxControlRe.FindStringSubmatch(strings.TrimSpace(text))
	if len(matches) != 3 {
		return "", false
	}
	receiver := strings.ToLower(matches[1])
	if strings.Contains(receiver, "jms") || strings.Contains(receiver, "session") {
		return "", false
	}
	if receiver != "conn" && receiver != "con" && receiver != "c" && !strings.Contains(receiver, "connection") && !strings.Contains(receiver, "jdbc") && !strings.Contains(receiver, "db") {
		return "", false
	}
	if strings.EqualFold(matches[2], "rollback") {
		return "java_sql_rollback", true
	}
	return "java_sql_commit", true
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

// javaBindValue resolves a PreparedStatement setXxx argument to a replay value:
// a string literal / resolvable expression is unquoted+templated; a bare numeric
// literal passes through; anything else (a variable) is preserved verbatim.
func javaBindValue(raw string, vars map[string]string, files map[string]string) string {
	raw = strings.TrimSpace(raw)
	env := javaStringEnv{Vars: vars}
	if value, ok := env.eval(raw, files); ok {
		return value
	}
	return raw
}

// javaDatabaseFunctionWithBinds builds an execute function carrying ordered
// PreparedStatement binds (index-sorted) so the emitted db action replays the
// parameterized query bound rather than with naked '?' placeholders.
func javaDatabaseFunctionWithBinds(statement javaStatement, phase, name, sql string, positional map[int]string) ParsedFunction {
	fn := javaDatabaseFunction(statement, phase, name, sql)
	if len(positional) == 0 {
		return fn
	}
	indexes := make([]int, 0, len(positional))
	for idx := range positional {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	for _, idx := range indexes {
		fn.RawArgs = append(fn.RawArgs, javaSQLBindPrefix+positional[idx])
	}
	return fn
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
