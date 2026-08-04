package jmeter

// converter_native_samplers.go — maps non-HTTP JMeter samplers onto native APEX
// Load protocols (database / network) for protocol-homogeneous thread groups,
// and captures JMS samplers as adapter_required.
//
// One APEX Load script runs under exactly one protocol, and the importer emits
// one script per thread group. So a thread group whose samplers are all one
// non-HTTP protocol becomes a native script for that protocol; mixed groups are
// converted under their dominant protocol with the rest review-gated.

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// samplerProtocol maps a JMeter sampler element name to the APEX Load protocol
// it would execute under. Script samplers (JSR223/BeanShell) are protocol-neutral
// and intentionally excluded so they don't affect homogeneity.
func samplerProtocol(name string) (string, bool) {
	switch name {
	case "HTTPSamplerProxy":
		return "http", true
	case "JDBCSampler":
		return "database", true
	case "FTPSampler", "TCPSampler":
		return "network", true
	case "JMSSampler", "JMSPublisher", "JMSSubscriber", "PublisherSampler", "SubscriberSampler":
		return "jms", true
	default:
		return "", false
	}
}

// classifyThreadGroupProtocol scans the samplers under a thread group (without
// descending into nested thread groups, which become their own scripts) and
// resolves the protocol to convert the group under, plus whether the group mixes
// protocols.
func classifyThreadGroupProtocol(tg *treeNode) (string, bool) {
	counts := map[string]int{}
	var scan func(nodes []*treeNode)
	scan = func(nodes []*treeNode) {
		for _, node := range nodes {
			if node == nil || node.Element == nil || !jmeterElementEnabled(node.Element) {
				continue
			}
			if node.Element.Name == "ThreadGroup" {
				continue
			}
			if protocol, ok := samplerProtocol(node.Element.Name); ok {
				counts[protocol]++
			}
			scan(node.Children)
		}
	}
	scan(tg.Children)
	return resolveGroupProtocol(counts)
}

// resolveGroupProtocol chooses the group protocol from the sampler counts. An
// empty group defaults to HTTP. A single protocol is homogeneous. Otherwise the
// dominant protocol is chosen by a fixed priority and the group is heterogeneous.
func resolveGroupProtocol(counts map[string]int) (string, bool) {
	if len(counts) == 0 {
		return "http", false
	}
	if len(counts) == 1 {
		for protocol := range counts {
			return protocol, false
		}
	}
	for _, protocol := range []string{"http", "database", "network", "jms"} {
		if counts[protocol] > 0 {
			return protocol, true
		}
	}
	return "http", true
}

// nativeScriptProtocol maps the resolved group protocol to the protocol set on
// the generated script. JMS has no registered runtime protocol here (it is
// adapter_required), so JMS groups fall back to an HTTP shell that ends up empty
// and is dropped; their samplers are captured as diagnostics.
func nativeScriptProtocol(groupProtocol string) string {
	switch groupProtocol {
	case "database", "network":
		return groupProtocol
	default:
		return "http"
	}
}

// finalizeNativeProtocolScript attaches the protocol-specific config block for
// database/network scripts after the thread group has been walked.
func finalizeNativeProtocolScript(script *dsl.Script, ctx *convertContext, diagnostics *diag.Collector, sourceMappings *[]sourceview.Mapping, sourceFileName, threadName string) {
	switch script.Protocol {
	case "database":
		cfg := dsl.DatabaseConfig{}
		if ctx.databaseConfig != nil {
			cfg = *ctx.databaseConfig
		}
		script.Config.Database = cfg
		if len(script.Actions) == 0 {
			return
		}
		// A database connection always needs operator verification of the DSN and
		// credentials before it can run, so the connection is adapter_required.
		setJMeterRuntimeParity(script, "jdbcConnection", "adapter_required")
		*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, nil, "JDBC Connection Configuration", "config.database", threadName, "connection", categoryAdapterRequired, "JMeter JDBC connection requires an operator-verified driver/DSN and credentials before running."))
		if strings.TrimSpace(cfg.Driver) == "" || strings.TrimSpace(cfg.DSN) == "" {
			diagnostics.Add(diag.Warning, "JMeter JDBC Connection", threadName, "JDBC driver/DSN could not be fully derived; complete config.database before running")
		} else {
			diagnostics.Add(diag.Info, "JMeter JDBC Connection", threadName, fmt.Sprintf("JDBC connection mapped to driver %q; verify DSN and credentials before running", cfg.Driver))
		}
	case "network":
		if ctx.networkConfig != nil {
			script.Config.Network = *ctx.networkConfig
		}
	}
}

// handleJMeterJDBCSampler converts a JDBC Request sampler into a database action
// when the thread group is database-homogeneous; otherwise it is review-gated.
func handleJMeterJDBCSampler(node *treeNode, ctx *convertContext, script *dsl.Script, result *ConvertResult, diagnostics *diag.Collector, sourceFileName string, sourceMappings *[]sourceview.Mapping) {
	element := node.Element
	label := jmeterElementLocation(element)
	if ctx.scriptProtocol != "database" {
		diagnostics.Add(diag.Warning, "JMeter JDBCSampler", label, "JDBC sampler appears in a thread group that is not database-homogeneous; it was review-gated. Move JDBC samplers into their own thread group to convert them natively.")
		*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, element, "JDBCSampler", "", label, "sampler", "review_only", "JDBC sampler was not converted because the thread group mixes protocols."))
		return
	}

	queryType := prop(element, "queryType")
	actionType := jdbcQueryActionType(queryType)
	needsSQL := actionType == "db_query" || actionType == "db_exec" || actionType == "db_procedure"
	query := convertVariables(firstNonEmptyProp(element, "query", "JDBCSampler.query"))
	if needsSQL && strings.TrimSpace(query) == "" {
		diagnostics.Add(diag.Warning, "JMeter JDBCSampler", label, "JDBC sampler had no SQL query to convert")
		*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, element, "JDBCSampler", "", label, "sampler", "review_only", "JDBC sampler had no SQL query."))
		return
	}

	action := dsl.Action{
		Name: label,
		Type: actionType,
		Metadata: map[string]any{
			"source":            "jmeter",
			"source_type":       "JDBCSampler",
			"jmeter_query_type": queryType,
		},
	}
	if needsSQL {
		action.Body = query
	}
	actionIndex := len(script.Actions)
	script.Actions = append(script.Actions, action)
	*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, element, "JDBCSampler", fmt.Sprintf("actions[%d]", actionIndex), label, "action", "converted", fmt.Sprintf("JMeter JDBC sampler was converted into a %s action.", actionType)))
	countNativeSamplerConversion(result)
}

// countNativeSamplerConversion records that a non-HTTP sampler converted into an
// executable action. countJMeterSamplerElements pre-counts every non-HTTP sampler
// as skipped, so a successful conversion is one more ConvertedActions and one
// fewer SkippedSamplers — keeping the completeness accounting honest (a converted
// sampler is no longer reported as skipped with zero converted actions).
func countNativeSamplerConversion(result *ConvertResult) {
	result.Stats.ConvertedActions++
	if result.Stats.SkippedSamplers > 0 {
		result.Stats.SkippedSamplers--
	}
}

// jdbcQueryActionType maps a JMeter JDBC queryType onto a database action type.
func jdbcQueryActionType(queryType string) string {
	switch strings.ToLower(strings.TrimSpace(queryType)) {
	case "update statement", "prepared update statement":
		return "db_exec"
	case "callable statement", "prepared callable statement":
		return "db_procedure"
	case "commit":
		return "db_commit"
	case "rollback":
		return "db_rollback"
	default:
		return "db_query"
	}
}

// parseJMeterJDBCConnection maps a JDBC Connection Configuration element onto a
// best-effort database config. Detection may be partial; the connection is always
// flagged adapter_required so an operator verifies it before running. The second
// return reports whether a credential was retained (the separate password
// property OR one embedded inline in dbUrl), so the caller can add a review item.
func parseJMeterJDBCConnection(element *xmlNode) (*dsl.DatabaseConfig, bool, bool) {
	dbURL := prop(element, "dbUrl")
	username, _ := authoredJMeterProperty(element, "username")
	password, _ := authoredJMeterProperty(element, "password")
	connectionProperties, _ := authoredJMeterProperty(element, "connectionProperties")
	properties := parseJMeterJDBCConnectionProperties(connectionProperties)
	cfg := &dsl.DatabaseConfig{}
	credentialRetained := strings.TrimSpace(password) != ""
	effectivePassword := password
	if username == "" {
		// DataSourceElement only calls BasicDataSource.setPassword when its
		// dedicated username field is non-empty. A standalone password field is
		// therefore inert source evidence, not an override of URL/property auth.
		effectivePassword = ""
	}
	if driver, dsn, inlineCred, ok := detectDatabaseDriverAndDSN(dbURL, username, effectivePassword); ok {
		cfg.Driver = driver
		cfg.DSN, _ = applyJMeterJDBCConnectionProperties(
			driver,
			dsn,
			properties,
			username != "",
		)
		credentialRetained = credentialRetained || inlineCred
	} else if trimmed := strings.TrimSpace(dbURL); trimmed != "" {
		// An unknown driver remains review-only, but its exact connection string
		// is retained so importing it never creates a credential-reentry task.
		cfg.DSN = strings.TrimPrefix(trimmed, "jdbc:")
		cfg.DSN, _ = applyJMeterJDBCConnectionProperties(
			"",
			cfg.DSN,
			properties,
			username != "",
		)
		credentialRetained = credentialRetained || hasJDBCInlineCredentials(trimmed)
	}
	for _, property := range properties {
		if credential.SensitiveName(property.Name) &&
			strings.TrimSpace(property.Value) != "" {
			credentialRetained = true
		}
	}
	return cfg, credentialRetained, true
}

// jmeterJDBCConnectionProperty is the effective java.util.Properties entry
// produced by Commons DBCP's BasicDataSource.setConnectionProperties. Property
// names are case-sensitive and duplicates use the last authored value.
type jmeterJDBCConnectionProperty struct {
	Name  string
	Value string
}

// parseJMeterJDBCConnectionProperties mirrors Commons DBCP rather than using
// url.ParseQuery: split on semicolons, split each non-empty entry on its first
// '=', and treat an entry without a value as an empty-valued property. The
// original string is retained separately in import metadata.
func parseJMeterJDBCConnectionProperties(
	raw string,
) []jmeterJDBCConnectionProperty {
	var (
		properties []jmeterJDBCConnectionProperty
		indices    = make(map[string]int)
	)
	for _, entry := range strings.Split(raw, ";") {
		if entry == "" {
			continue
		}
		name := entry
		value := ""
		if separator := strings.IndexByte(entry, '='); separator > 0 {
			name = entry[:separator]
			value = entry[separator+1:]
		}
		if index, exists := indices[name]; exists {
			properties[index].Value = value
			continue
		}
		indices[name] = len(properties)
		properties = append(properties, jmeterJDBCConnectionProperty{
			Name:  name,
			Value: value,
		})
	}
	return properties
}

// applyJMeterJDBCConnectionProperties carries JDBC driver properties into the
// native database/sql DSN. Dedicated JMeter username/password fields have the
// same precedence as BasicDataSource: when username is authored, both explicit
// fields override connectionProperties user/password. Other properties replace
// same-named URL query options and retain their deterministic source order.
func applyJMeterJDBCConnectionProperties(
	driver string,
	dsn string,
	properties []jmeterJDBCConnectionProperty,
	explicitCredentials bool,
) (string, bool) {
	if len(properties) == 0 {
		return dsn, true
	}
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "postgres":
		parsed, err := url.Parse(dsn)
		if err != nil || parsed.Scheme == "" {
			return dsn, false
		}
		queryProperties, propertyUser, propertyPassword :=
			jmeterJDBCEffectiveProperties(properties, explicitCredentials)
		parsed.RawQuery = mergeJMeterJDBCRawQuery(
			parsed.RawQuery,
			queryProperties,
		)
		if !explicitCredentials &&
			(propertyUser != nil || propertyPassword != nil) {
			username := ""
			password := ""
			hasPassword := false
			if parsed.User != nil {
				username = parsed.User.Username()
				password, hasPassword = parsed.User.Password()
			}
			if propertyUser != nil {
				username = *propertyUser
			}
			if propertyPassword != nil {
				password = *propertyPassword
				hasPassword = true
			}
			if hasPassword {
				parsed.User = url.UserPassword(username, password)
			} else if username != "" {
				parsed.User = url.User(username)
			}
		}
		return parsed.String(), true
	case "mysql":
		queryProperties, propertyUser, propertyPassword :=
			jmeterJDBCEffectiveProperties(properties, explicitCredentials)
		base, rawQuery, hasQuery := strings.Cut(dsn, "?")
		rawQuery = mergeJMeterJDBCRawQuery(rawQuery, queryProperties)
		if !explicitCredentials &&
			(propertyUser != nil || propertyPassword != nil) {
			base = mergeJMeterMySQLCredentials(
				base,
				propertyUser,
				propertyPassword,
			)
		}
		if rawQuery != "" || hasQuery {
			return base + "?" + rawQuery, true
		}
		return base, true
	default:
		// Oracle/ODBC/custom JDBC properties cannot be translated faithfully
		// into the bundled database/sql DSNs. They remain byte-exact in the
		// protected jmeter_jdbc_sources envelope and the connection is already
		// adapter_required. Returning false records that only preservation, not
		// native application, was possible.
		return dsn, false
	}
}

func jmeterJDBCEffectiveProperties(
	properties []jmeterJDBCConnectionProperty,
	explicitCredentials bool,
) (
	query []jmeterJDBCConnectionProperty,
	user *string,
	password *string,
) {
	query = make([]jmeterJDBCConnectionProperty, 0, len(properties))
	for _, property := range properties {
		switch property.Name {
		case "user":
			if !explicitCredentials {
				value := property.Value
				user = &value
			}
		case "password":
			if !explicitCredentials {
				value := property.Value
				password = &value
			}
		default:
			query = append(query, property)
		}
	}
	return query, user, password
}

func mergeJMeterJDBCRawQuery(
	rawQuery string,
	properties []jmeterJDBCConnectionProperty,
) string {
	if len(properties) == 0 {
		return rawQuery
	}
	replaced := make(map[string]struct{}, len(properties))
	for _, property := range properties {
		replaced[property.Name] = struct{}{}
	}
	segments := make([]string, 0, len(properties)+1)
	for _, segment := range strings.Split(rawQuery, "&") {
		if segment == "" {
			continue
		}
		encodedName, _, _ := strings.Cut(segment, "=")
		name, err := url.QueryUnescape(encodedName)
		if err == nil {
			if _, replace := replaced[name]; replace {
				continue
			}
		}
		segments = append(segments, segment)
	}
	for _, property := range properties {
		segments = append(
			segments,
			url.QueryEscape(property.Name)+"="+url.QueryEscape(property.Value),
		)
	}
	return strings.Join(segments, "&")
}

func mergeJMeterMySQLCredentials(
	dsn string,
	propertyUser *string,
	propertyPassword *string,
) string {
	marker := strings.Index(dsn, "@tcp(")
	credentials := ""
	suffix := dsn
	if marker >= 0 {
		credentials = dsn[:marker]
		suffix = dsn[marker+1:]
	}
	username := credentials
	password := ""
	hasPassword := false
	if separator := strings.IndexByte(credentials, ':'); separator >= 0 {
		username = credentials[:separator]
		password = credentials[separator+1:]
		hasPassword = true
	}
	if propertyUser != nil {
		username = *propertyUser
	}
	if propertyPassword != nil {
		password = *propertyPassword
		hasPassword = true
	}
	if username == "" && !hasPassword {
		return suffix
	}
	if hasPassword {
		return username + ":" + password + "@" + suffix
	}
	return username + "@" + suffix
}

var (
	// jdbcCredPropRe matches a password carried as a JDBC connection property or
	// query parameter (SQL Server / jtds use ;password=, Postgres uses ?password=
	// / &password=), case-insensitive. The value runs to the next ; or & or end.
	jdbcCredPropRe = regexp.MustCompile(`(?i)([;&?](?:password|pwd)=)([^;&]*)`)
	// jdbcOracleCredRe matches Oracle thin/oci inline credentials of the form
	// jdbc:oracle:<drivertype>:user/password@... (the user/pass precede the '@').
	jdbcOracleCredRe = regexp.MustCompile(`(?i)^(jdbc:oracle:[a-z]+:)([^/@:][^/@]*)/([^@]*)(@)`)
)

// hasJDBCInlineCredentials detects connection-property, Oracle thin, and URL
// userinfo credential forms without changing any source bytes.
func hasJDBCInlineCredentials(raw string) bool {
	if jdbcCredPropRe.MatchString(raw) || jdbcOracleCredRe.MatchString(raw) {
		return true
	}
	if idx := strings.Index(raw, "://"); idx >= 0 {
		rest := raw[idx+3:]
		authority := rest
		if end := strings.IndexAny(rest, "/?;"); end >= 0 {
			authority = rest[:end]
		}
		if at := strings.LastIndex(authority, "@"); at >= 0 {
			return strings.Contains(authority[:at], ":")
		}
	}
	return false
}

// detectDatabaseDriverAndDSN converts a JDBC URL into an APEX Load driver + DSN.
// Imported credentials remain in the durable runtime DSN. Ordinary API reads
// and exports mask that field, while the trusted import-save path retains it so
// the operator can run the destination without re-entering a password. The third
// return reports whether an inline credential was found.
func detectDatabaseDriverAndDSN(dbURL, username, password string) (string, string, bool, bool) {
	raw := strings.TrimSpace(dbURL)
	inlineCred := hasJDBCInlineCredentials(raw)
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "jdbc:postgresql:"):
		dsn := "postgres:" + raw[len("jdbc:postgresql:"):]
		return "postgres", injectURLCredentials(dsn, username, password), inlineCred, true
	case strings.HasPrefix(lower, "jdbc:mysql:"), strings.HasPrefix(lower, "jdbc:mariadb:"):
		return "mysql", mysqlDSNFromJDBC(raw, username, password), inlineCred, true
	case strings.HasPrefix(lower, "jdbc:oracle:"):
		return "oracle", strings.TrimPrefix(raw, "jdbc:"), inlineCred, true
	case strings.HasPrefix(lower, "jdbc:sqlite:"):
		return "sqlite", strings.TrimPrefix(strings.TrimPrefix(raw, "jdbc:sqlite:"), "//"), inlineCred, true
	case strings.HasPrefix(lower, "jdbc:sqlserver:"), strings.HasPrefix(lower, "jdbc:jtds:"):
		return "odbc", strings.TrimPrefix(raw, "jdbc:"), inlineCred, true
	}
	return "", "", false, false
}

func mysqlDSNFromJDBC(raw, username, password string) string {
	lower := strings.ToLower(raw)
	prefix := "jdbc:mysql:"
	if strings.HasPrefix(lower, "jdbc:mariadb:") {
		prefix = "jdbc:mariadb:"
	}
	rest := raw[len(prefix):]
	rest = strings.TrimPrefix(rest, "//")
	authority := rest
	database := ""
	if slash := strings.Index(rest, "/"); slash >= 0 {
		authority, database = rest[:slash], rest[slash+1:]
	}
	sourceCredentials := ""
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		sourceCredentials, authority = authority[:at], authority[at+1:]
	}
	credentials := sourceCredentials
	if username != "" || password != "" {
		credentials = username
		if password != "" {
			credentials += ":" + password
		}
	}
	if credentials == "" {
		return fmt.Sprintf("tcp(%s)/%s", authority, database)
	}
	return fmt.Sprintf("%s@tcp(%s)/%s", credentials, authority, database)
}

// injectURLCredentials inserts user[:pass]@ after the scheme of a URL-style DSN.
func injectURLCredentials(dsn, username, password string) string {
	if strings.TrimSpace(username) == "" {
		return dsn
	}
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme == "" {
		return dsn
	}
	if password != "" {
		parsed.User = url.UserPassword(username, password)
	} else {
		parsed.User = url.User(username)
	}
	return parsed.String()
}

func preserveJMeterJDBCSource(script *dsl.Script, element *xmlNode) {
	if script == nil || element == nil {
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
	script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey] =
		dsl.ImportPreservedCredentialPolicyPreserve
	existing, _ := script.Metadata.Import.Preserved[dsl.ImportPreservedJMeterJDBCSourcesKey].([]map[string]string)
	dbURL, _ := authoredJMeterProperty(element, "dbUrl")
	username, _ := authoredJMeterProperty(element, "username")
	password, _ := authoredJMeterProperty(element, "password")
	connectionProperties, _ := authoredJMeterProperty(element, "connectionProperties")
	existing = append(existing, map[string]string{
		"db_url":                dbURL,
		"username":              username,
		"password":              password,
		"connection_properties": connectionProperties,
	})
	script.Metadata.Import.Preserved[dsl.ImportPreservedJMeterJDBCSourcesKey] = existing
}

func authoredJMeterProperty(
	node *xmlNode,
	name string,
) (string, bool) {
	if node == nil {
		return "", false
	}
	if node.Attrs["name"] == name {
		return node.Text, true
	}
	for _, child := range node.Children {
		if value, ok := authoredJMeterProperty(child, name); ok {
			return value, true
		}
	}
	return "", false
}

// handleJMeterNetworkSampler converts FTP/TCP samplers into network actions when
// the thread group is network-homogeneous; otherwise they are review-gated.
func handleJMeterNetworkSampler(node *treeNode, ctx *convertContext, script *dsl.Script, result *ConvertResult, diagnostics *diag.Collector, sourceFileName string, sourceMappings *[]sourceview.Mapping) {
	element := node.Element
	name := element.Name
	label := jmeterElementLocation(element)
	if ctx.scriptProtocol != "network" {
		diagnostics.Add(diag.Warning, "JMeter "+name, label, "network sampler appears in a thread group that is not network-homogeneous; it was review-gated. Move it into its own thread group to convert it natively.")
		*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, element, name, "", label, "sampler", "review_only", "Network sampler was not converted because the thread group mixes protocols."))
		return
	}

	var action dsl.Action
	var host, subprotocol string
	var port int
	switch name {
	case "TCPSampler":
		host = prop(element, "TCPSampler.server")
		port = parseInt(prop(element, "TCPSampler.port"))
		address := host
		if port > 0 {
			address = fmt.Sprintf("%s:%d", host, port)
		}
		if strings.TrimSpace(address) == "" {
			diagnostics.Add(diag.Warning, "JMeter TCPSampler", label, "TCP sampler had no server address to convert")
			*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, element, name, "", label, "sampler", "review_only", "TCP sampler had no server address."))
			return
		}
		action = dsl.Action{
			Name:   label,
			Type:   "tcp_request",
			Target: address,
			Body:   convertVariables(prop(element, "TCPSampler.request")),
			Metadata: map[string]any{
				"source":      "jmeter",
				"source_type": "TCPSampler",
			},
		}
		subprotocol = "tcp"
	case "FTPSampler":
		host = prop(element, "FTPSampler.server")
		port = parseInt(prop(element, "FTPSampler.port"))
		remote := convertVariables(prop(element, "FTPSampler.filename"))
		command := "RETR"
		if upload, ok := parseOptionalBool(prop(element, "FTPSampler.uploadfile")); ok && upload {
			command = "STOR"
		}
		target := strings.TrimSpace(command + " " + remote)
		action = dsl.Action{
			Name:   label,
			Type:   "ftp_command",
			Target: target,
			Metadata: map[string]any{
				"source":      "jmeter",
				"source_type": "FTPSampler",
			},
		}
		subprotocol = "ftp"
	default:
		return
	}

	if ctx.networkConfig == nil {
		ctx.networkConfig = &dsl.NetworkConfig{Subprotocol: subprotocol, Host: host, Port: port}
	}
	actionIndex := len(script.Actions)
	script.Actions = append(script.Actions, action)
	*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, element, name, fmt.Sprintf("actions[%d]", actionIndex), label, "action", "converted", fmt.Sprintf("JMeter %s was converted into a %s action.", name, action.Type)))
	countNativeSamplerConversion(result)
}

// handleJMeterJMSSampler captures a JMS sampler as adapter_required. JMeter JMS
// samplers carry no concrete provider, and APEX Load registers only
// provider-specific JMS protocols (jms-solace/jms-ibmmq/jms-servicebus), so the
// sampler is recorded for review rather than producing an unrunnable script.
func handleJMeterJMSSampler(node *treeNode, script *dsl.Script, result *ConvertResult, diagnostics *diag.Collector, sourceFileName string, sourceMappings *[]sourceview.Mapping) {
	element := node.Element
	name := element.Name
	label := jmeterElementLocation(element)
	destination := firstNonEmptyProp(element, "jms.topic", "jms.queue", "JMSSampler.queue", "JMSSampler.SendQueue", "destination")
	detail := "JMS sampler maps to APEX Load jms_publish/jms_consume but requires selecting and configuring a JMS provider (jms-solace, jms-ibmmq, or jms-servicebus) before running."
	if destination != "" {
		detail = fmt.Sprintf("%s Destination: %s.", detail, destination)
	}
	diagnostics.Add(diag.Warning, "JMeter "+name, label, detail)
	*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, element, name, "", label, "sampler", categoryAdapterRequired, detail))
}

// firstNonEmptyProp returns the first non-empty stringProp value among names.
func firstNonEmptyProp(element *xmlNode, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(prop(element, name)); value != "" {
			return value
		}
	}
	return ""
}
