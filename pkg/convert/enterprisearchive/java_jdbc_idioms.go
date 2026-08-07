package enterprisearchive

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const javaSQLBindPrefix = "__bind__="

var (
	javaSQLAssignRe             = regexp.MustCompile(`(?i)^\s*(?:final\s+)?String\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+);\s*$`)
	javaJDBCFieldStringAssignRe = regexp.MustCompile(`(?i)^\s*(?:(?:public|protected|private|static|final|volatile|transient)\s+)*String\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+);\s*$`)
	javaJDBCStringReassignRe    = regexp.MustCompile(`(?i)^\s*((?:this\.)?[A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+);\s*$`)
	javaPreparedStmtRe          = regexp.MustCompile(`(?i)^\s*(?:PreparedStatement|java\.sql\.PreparedStatement)?\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*.+?\.prepareStatement\s*\((.+)\)\s*;\s*$`)
	javaDriverManagerRe         = regexp.MustCompile(`(?i)\bDriverManager\.getConnection\s*\(`)
	javaStatementExecRe         = regexp.MustCompile(`(?i)\.execute(?:Query|Update)?\s*\((.*)\)\s*;?\s*$`)
	// Also matches the common assignment form `ResultSet rs = ps.executeQuery();`
	// (the receiver is the last token before .execute), not only `ps.executeQuery();`.
	javaStatementNoArgRe = regexp.MustCompile(`(?i)^(?:.*=\s*)?([A-Za-z_][A-Za-z0-9_]*)\.execute(?:Query|Update)?\s*\(\s*\)\s*;?\s*$`)
	javaSetAutoCommitRe  = regexp.MustCompile(`(?i)\.setAutoCommit\s*\(\s*false\s*\)\s*;?\s*$`)
	javaSQLTxControlRe   = regexp.MustCompile(`(?i)^\s*([A-Za-z_][A-Za-z0-9_]*)\.(commit|rollback)\s*\(\s*\)\s*;?\s*$`)
	// ps.setString(1, "x") / ps.setInt(2, id) — captures receiver, 1-based index, value.
	javaSetBindRe = regexp.MustCompile(`(?i)^\s*([A-Za-z_][A-Za-z0-9_]*)\.set[A-Za-z]+\s*\(\s*([0-9]+)\s*,\s*(.+?)\s*\)\s*;?\s*$`)
)

type javaJDBCConnection struct {
	Driver       string
	DSN          string
	ReviewReason string
}

// javaDatabaseParsedActions is the entry point of the retired executable JDBC
// lane. It is still production resident and still compiled in, but it has no
// production caller: the lane orchestration that drove it is quarantined in
// java_native_lanes_test.go, which is also where the guard lives that keeps
// production from calling it again.
func javaDatabaseParsedActions(program javaProgram, files map[string]string) []*ParsedAction {
	sqlVars := map[string]string{}
	stringVars := javaJDBCInitialStringVars(program, files)
	prepared := map[string]string{}
	preparedDynamic := map[string]bool{}
	binds := map[string]map[int]string{}
	return javaParsedActionsFor(program, func(statement javaStatement, phase string) (ParsedFunction, bool) {
		text := javaNormalizeStatementText(statement.Text)
		if m := javaSQLAssignRe.FindStringSubmatch(text); len(m) == 3 {
			if statement.BranchKey != "" {
				delete(stringVars, m[1])
				delete(sqlVars, m[1])
				return ParsedFunction{}, false
			}
			env := javaStringEnv{Vars: stringVars}
			if value, ok := env.eval(m[2], files); ok {
				stringVars[m[1]] = value
				if looksLikeSQLText(value) {
					sqlVars[m[1]] = value
				}
			}
			return ParsedFunction{}, false
		}
		if m := javaJDBCStringReassignRe.FindStringSubmatch(text); len(m) == 3 {
			name := strings.TrimSpace(m[1])
			bareName := strings.TrimPrefix(name, "this.")
			aliases := []string{name, bareName, "this." + bareName}
			value, resolved := (javaStringEnv{Vars: stringVars}).eval(m[2], files)
			_, knownString := stringVars[name]
			_, knownSQL := sqlVars[name]
			if !resolved && !knownString && !knownSQL {
				// This is not a tracked String assignment (for example
				// conn = DriverManager.getConnection(...)); let the dedicated
				// JDBC/statement matchers below inspect it.
			} else {
				if statement.BranchKey != "" || !resolved {
					for _, alias := range aliases {
						delete(stringVars, alias)
						delete(sqlVars, alias)
					}
					return ParsedFunction{}, false
				}
				for _, alias := range aliases {
					stringVars[alias] = value
					if looksLikeSQLText(value) {
						sqlVars[alias] = value
					} else {
						delete(sqlVars, alias)
					}
				}
				return ParsedFunction{}, false
			}
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
		if match := javaDriverManagerRe.FindStringIndex(text); len(match) == 2 {
			return javaJDBCConnectionFunction(statement, phase, text, match[1]-1, stringVars, files), true
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

func javaJDBCInitialStringVars(program javaProgram, files map[string]string) map[string]string {
	vars := map[string]string{}
	class, ok := javaVuserClass(program)
	if !ok {
		return vars
	}
	// Resolve a bounded number of passes so one constant field may refer to a
	// previously or subsequently declared constant without evaluating Java.
	for pass := 0; pass < len(class.Fields)+1; pass++ {
		changed := false
		for _, field := range class.Fields {
			text := javaNormalizeStatementText(field.Text)
			match := javaJDBCFieldStringAssignRe.FindStringSubmatch(text)
			if len(match) != 3 || !javaDeclarationHasModifier(text, "final") {
				continue
			}
			value, resolved := (javaStringEnv{Vars: vars}).eval(match[2], files)
			if !resolved {
				continue
			}
			name := strings.TrimSpace(match[1])
			for _, alias := range []string{name, "this." + name, class.Name + "." + name} {
				if vars[alias] != value {
					vars[alias] = value
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	return vars
}

func javaDeclarationHasModifier(declaration string, modifier string) bool {
	modifier = strings.ToLower(strings.TrimSpace(modifier))
	for _, field := range strings.Fields(strings.NewReplacer(
		"\t", " ",
		"\r", " ",
		"\n", " ",
	).Replace(declaration)) {
		if strings.ToLower(field) == modifier {
			return true
		}
		if strings.EqualFold(field, "String") {
			break
		}
	}
	return false
}

func javaJDBCConnectionFunction(
	statement javaStatement,
	phase string,
	text string,
	methodNameEnd int,
	vars map[string]string,
	files map[string]string,
) ParsedFunction {
	fn := javaDatabaseFunction(statement, phase, "java_sql_connect", "")
	args, ok := javaCallArguments(text, text, methodNameEnd)
	if !ok {
		fn.javaJDBCConnection = &javaJDBCConnection{ReviewReason: "jdbc_connection_arguments_unparseable"}
		return fn
	}
	fn.javaJDBCConnection = javaJDBCConnectionFromArgs(args, vars, files)
	return fn
}

func javaJDBCConnectionFromArgs(
	args []string,
	vars map[string]string,
	files map[string]string,
) *javaJDBCConnection {
	connection := &javaJDBCConnection{}
	if len(args) != 1 && len(args) != 3 {
		connection.ReviewReason = "jdbc_connection_overload_unsupported"
		return connection
	}

	env := javaStringEnv{Vars: vars}
	rawURL, ok := env.eval(args[0], files)
	if !ok || strings.TrimSpace(rawURL) == "" {
		connection.ReviewReason = "jdbc_connection_url_dynamic"
		return connection
	}
	if strings.Contains(rawURL, "{{") {
		connection.ReviewReason = "jdbc_connection_runtime_template_unsupported"
		return connection
	}

	username := ""
	password := ""
	explicitCredentials := len(args) == 3
	if explicitCredentials {
		if username, ok = env.eval(args[1], files); !ok {
			connection.ReviewReason = "jdbc_connection_username_dynamic"
			return connection
		}
		if password, ok = env.eval(args[2], files); !ok {
			connection.ReviewReason = "jdbc_connection_password_dynamic"
			return connection
		}
		if strings.Contains(username, "{{") || strings.Contains(password, "{{") {
			connection.ReviewReason = "jdbc_connection_runtime_template_unsupported"
			return connection
		}
	}

	driver, dsn, reviewReason, ok := javaJDBCDriverAndDSN(
		rawURL,
		username,
		password,
		explicitCredentials,
	)
	connection.Driver = driver
	connection.DSN = dsn
	connection.ReviewReason = reviewReason
	if !ok && connection.ReviewReason == "" {
		connection.ReviewReason = "jdbc_connection_driver_unsupported"
	}
	return connection
}

// javaJDBCDriverAndDSN translates the source JDBC overload into the database
// runtime's native driver/DSN shape. Credentials remain in the durable DSN so
// an imported script can run without asking the operator to enter values that
// were already present in the source. Ordinary API projections mask the DSN.
func javaJDBCDriverAndDSN(
	rawURL string,
	username string,
	password string,
	explicitCredentials bool,
) (driver string, dsn string, reviewReason string, ok bool) {
	rawURL = strings.TrimSpace(rawURL)
	lower := strings.ToLower(rawURL)
	switch {
	case strings.HasPrefix(lower, "jdbc:postgresql:"):
		dsn = "postgres:" + rawURL[len("jdbc:postgresql:"):]
		dsn, ok = javaJDBCInjectURLCredentials(dsn, username, password, explicitCredentials)
		if !ok {
			return "postgres", javaJDBCReviewDSN(rawURL, username, password, explicitCredentials), "jdbc_connection_url_unrepresentable", false
		}
		return "postgres", dsn, "", true

	case strings.HasPrefix(lower, "jdbc:mysql:"),
		strings.HasPrefix(lower, "jdbc:mariadb:"):
		dsn, ok = javaMySQLDSNFromJDBC(rawURL, username, password, explicitCredentials)
		if !ok {
			return "mysql", javaJDBCReviewDSN(rawURL, username, password, explicitCredentials), "jdbc_connection_url_unrepresentable", false
		}
		return "mysql", dsn, "jdbc_connection_adapter_review_required", false

	case strings.HasPrefix(lower, "jdbc:sqlite:"):
		dsn = strings.TrimPrefix(rawURL[len("jdbc:sqlite:"):], "//")
		if explicitCredentials {
			// SQLite does not have a portable username/password DSN encoding.
			// Keep all source fields in an inert envelope and prevent execution
			// instead of silently dropping authentication semantics.
			return "sqlite", javaJDBCReviewDSN(rawURL, username, password, true), "jdbc_connection_credentials_unrepresentable", false
		}
		return "sqlite", dsn, "", true

	case strings.HasPrefix(lower, "jdbc:oracle:"):
		dsn = rawURL[len("jdbc:"):]
		if explicitCredentials {
			dsn += ";user=" + url.QueryEscape(username) + ";password=" + url.QueryEscape(password)
		}
		return "oracle", dsn, "jdbc_connection_adapter_review_required", false

	case strings.HasPrefix(lower, "jdbc:sqlserver:"),
		strings.HasPrefix(lower, "jdbc:jtds:"),
		strings.HasPrefix(lower, "jdbc:odbc:"):
		dsn = rawURL[len("jdbc:"):]
		if explicitCredentials {
			dsn += ";user=" + url.QueryEscape(username) + ";password=" + url.QueryEscape(password)
		}
		return "odbc", dsn, "jdbc_connection_adapter_review_required", false
	}
	return "", javaJDBCReviewDSN(rawURL, username, password, explicitCredentials), "jdbc_connection_driver_unsupported", false
}

type javaJDBCReviewConnection struct {
	JDBCURL  string `json:"jdbc_url"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

func javaJDBCReviewDSN(
	rawURL string,
	username string,
	password string,
	explicitCredentials bool,
) string {
	envelope := javaJDBCReviewConnection{JDBCURL: rawURL}
	if explicitCredentials {
		envelope.Username = username
		envelope.Password = password
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		// All inputs are Go strings, so encoding/json cannot fail in practice.
		// Retain the URL as a final disclosure-safe structural fallback.
		return rawURL
	}
	return string(payload)
}

func javaJDBCInjectURLCredentials(
	dsn string,
	username string,
	password string,
	explicit bool,
) (string, bool) {
	if !explicit {
		return dsn, true
	}
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return dsn, false
	}
	parsed.User = url.UserPassword(username, password)
	return parsed.String(), true
}

func javaMySQLDSNFromJDBC(
	rawURL string,
	username string,
	password string,
	explicitCredentials bool,
) (string, bool) {
	lower := strings.ToLower(rawURL)
	prefix := "jdbc:mysql:"
	if strings.HasPrefix(lower, "jdbc:mariadb:") {
		prefix = "jdbc:mariadb:"
	}
	rest := strings.TrimPrefix(rawURL[len(prefix):], "//")
	authority := rest
	database := ""
	if slash := strings.Index(rest, "/"); slash >= 0 {
		authority, database = rest[:slash], rest[slash+1:]
	}
	if strings.TrimSpace(authority) == "" {
		return rawURL, false
	}
	sourceCredentials := ""
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		sourceCredentials, authority = authority[:at], authority[at+1:]
	}
	credentials := sourceCredentials
	if explicitCredentials {
		credentials = username + ":" + password
	}
	if credentials == "" {
		return fmt.Sprintf("tcp(%s)/%s", authority, database), true
	}
	return fmt.Sprintf("%s@tcp(%s)/%s", credentials, authority, database), true
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
