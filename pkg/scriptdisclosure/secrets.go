package scriptdisclosure

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

type secretCollector struct {
	values                   map[string]struct{}
	credentialParameterNames map[string]struct{}
	sensitiveSourceColumns   map[string][]string
}

func newSecretCollector(
	credentialParameterNames map[string]struct{},
	sensitiveSourceColumns map[string][]string,
) *secretCollector {
	return &secretCollector{
		values:                   make(map[string]struct{}),
		credentialParameterNames: credentialParameterNames,
		sensitiveSourceColumns:   sensitiveSourceColumns,
	}
}

func (c *secretCollector) collect(value any, ctx projectionContext, depth int) {
	if depth > maxProjectionDepth {
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		if ctx.headerList {
			name, _ := typed["name"].(string)
			if credential.SensitiveHeader(name) || isSensitiveKey(name) {
				c.addNode(typed["value"])
			}
		}
		if ctx.dataSource {
			c.collectDataSource(typed)
		}
		credentialValue := mapHasCredentialValue(typed, c.credentialParameterNames)
		for key, child := range typed {
			normalized := normalizeName(key)
			switch {
			case ctx.metadata &&
				key == dsl.ImportPreservedCredentialTargetsMetadataKey &&
				validCredentialTargetLocators(child):
				// Structural field names such as "/password" are locators, not
				// credential values.
				continue
			case ctx.secretBindings:
				c.addNode(child)
			case normalizeName(ctx.key) == "proxy" && normalized == "username":
				c.addNode(child)
			case credentialValue && (normalized == "value" || normalized == "values"):
				c.addNode(child)
			case normalized == "body":
				if text, ok := child.(string); ok {
					c.collectBody(text, typed)
				}
			case ctx.headers && (credential.SensitiveHeader(key) || isSensitiveKey(key)):
				c.addNode(child)
			case isSensitiveKey(key) && !isSecretReferenceKey(key):
				switch child.(type) {
				case map[string]any, []any:
					// Container names such as auth and credentials describe a
					// structure; their type/username siblings are not secret
					// merely because the parent is credential-shaped. Recurse
					// so only password/token/etc. leaves are collected.
					c.collect(child, projectionContext{
						key:      key,
						metadata: ctx.metadata,
					}, depth+1)
				default:
					c.addNode(child)
				}
			case isURLKey(key):
				if text, ok := child.(string); ok {
					c.collectURL(text)
				}
			default:
				c.collect(child, projectionContext{
					key:            key,
					opaque:         ctx.opaque || isOpaqueMap(key),
					headers:        isHeaderMap(key),
					headerList:     isHeaderList(key),
					secretBindings: isSecretBindingsMap(key),
					parameter:      normalizeName(key) == "parameters",
					dataSource:     normalizeName(key) == "datasources",
					metadata:       ctx.metadata || normalized == "metadata",
					javaVMArgument: normalized == "additionalvmparameters",
				}, depth+1)
			}
		}
	case []any:
		for _, child := range typed {
			if ctx.javaVMArgument {
				if argument, ok := child.(string); ok &&
					javarunner.ContainsSensitiveVMParameterValue(argument) {
					c.add(argument)
					if _, value, found := strings.Cut(argument, "="); found {
						c.add(value)
					}
				}
			}
			c.collect(child, ctx, depth+1)
		}
	}
}

func (c *secretCollector) addNode(value any) {
	switch typed := value.(type) {
	case string:
		c.add(typed)
	case []any:
		for _, child := range typed {
			c.addNode(child)
		}
	case map[string]any:
		for _, child := range typed {
			c.addNode(child)
		}
	}
}

func (c *secretCollector) add(value string) {
	if value == "" || strings.Contains(value, credential.Redacted) || isPortableBinding(value) {
		return
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return
	}
	for _, scheme := range []string{"Bearer", "Basic", "Digest"} {
		if len(trimmed) > len(scheme) &&
			strings.EqualFold(trimmed[:len(scheme)], scheme) &&
			trimmed[len(scheme)] == ' ' {
			c.add(strings.TrimSpace(trimmed[len(scheme):]))
			return
		}
	}
	c.values[value] = struct{}{}
	c.values[trimmed] = struct{}{}
}

func (c *secretCollector) collectURL(value string) {
	parsed, err := url.Parse(value)
	if err != nil {
		return
	}
	if parsed.User != nil {
		if password, ok := parsed.User.Password(); ok {
			c.add(password)
		}
	}
	for key, values := range parsed.Query() {
		if !credential.SensitiveName(key) {
			continue
		}
		for _, value := range values {
			c.add(value)
		}
	}
}

// collectScriptCredentialTargets dereferences server-authored config locators
// against the executable source copy. Generic name scanning cannot identify a
// neutral proxy query key, an isolated DSN component, or a JVM assignment RHS;
// adding those values to knownSecrets lets the disclosure projector remove
// equivalent occurrences anywhere without changing the durable script.
func (c *secretCollector) collectScriptCredentialTargets(script *dsl.Script) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return
	}
	raw := script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialTargetsMetadataKey]
	if raw == nil || !validCredentialTargetLocators(raw) {
		return
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return
	}
	var targets []credentialpolicy.TargetRef
	if json.Unmarshal(payload, &targets) != nil {
		return
	}
	for _, target := range targets {
		field := strings.ToLower(strings.TrimSpace(target.Field))
		switch {
		case strings.Contains(field, "config.base_url"):
			c.collectURLTarget(script.Config.BaseURL, target)
		case strings.Contains(field, "config.http.proxy.url"):
			c.collectURLTarget(script.Config.HTTP.Proxy.URL, target)
		case strings.Contains(field, "config.database.dsn"):
			c.collectDSN(script.Config.Database.DSN)
		case strings.Contains(field, "config.java_vuser.additional_vm_parameters"):
			if target.Index < 0 ||
				target.Index >= len(script.Config.JavaVuser.AdditionalVMParameters) {
				continue
			}
			argument := script.Config.JavaVuser.AdditionalVMParameters[target.Index]
			c.add(argument)
			if _, value, found := strings.Cut(argument, "="); found {
				c.add(value)
				c.collectURL(value)
			}
		case strings.Contains(field, "metadata.import.preserved.recorded_resource_urls["):
			index, ok := disclosureBracketIndex(
				field,
				"metadata.import.preserved.recorded_resource_urls[",
			)
			if !ok {
				continue
			}
			if value, ok := disclosureStringAt(
				script.Metadata.Import.Preserved["recorded_resource_urls"],
				index,
			); ok {
				c.collectURLTarget(value, target)
			}
		}
	}
}

func (c *secretCollector) collectURLTarget(
	raw string,
	target credentialpolicy.TargetRef,
) {
	field := strings.ToLower(strings.TrimSpace(target.Field))
	switch {
	case strings.HasSuffix(field, ".query"):
		queryStart := strings.IndexByte(raw, '?')
		if queryStart < 0 {
			return
		}
		queryEnd := len(raw)
		if fragment := strings.IndexByte(raw[queryStart+1:], '#'); fragment >= 0 {
			queryEnd = queryStart + 1 + fragment
		}
		pairs := strings.Split(raw[queryStart+1:queryEnd], "&")
		if target.Index < 0 || target.Index >= len(pairs) {
			return
		}
		_, encoded, _ := strings.Cut(pairs[target.Index], "=")
		if value, err := url.QueryUnescape(encoded); err == nil {
			c.add(value)
		}
	case strings.HasSuffix(field, ".userinfo"):
		parsed, err := url.Parse(raw)
		if err != nil || parsed.User == nil {
			return
		}
		c.add(parsed.User.String())
		c.add(parsed.User.Username())
		if password, ok := parsed.User.Password(); ok {
			c.add(password)
		}
	case strings.HasSuffix(field, ".fragment"):
		parsed, err := url.Parse(raw)
		if err == nil {
			c.add(parsed.Fragment)
		}
	default:
		c.collectURL(raw)
	}
}

func (c *secretCollector) collectDSN(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || isPortableBinding(raw) {
		return
	}
	c.add(raw)
	candidate := raw
	if strings.HasPrefix(strings.ToLower(candidate), "jdbc:") {
		candidate = candidate[len("jdbc:"):]
	}
	if parsed, err := url.Parse(candidate); err == nil {
		if parsed.User != nil {
			c.add(parsed.User.String())
			c.add(parsed.User.Username())
			if password, ok := parsed.User.Password(); ok {
				c.add(password)
			}
		}
		for _, values := range parsed.Query() {
			for _, value := range values {
				c.add(value)
			}
		}
	}
	for _, segment := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ';' || r == '\r' || r == '\n' || r == '\t' || r == ' '
	}) {
		name, value, found := strings.Cut(segment, "=")
		if found && credential.SensitiveName(strings.TrimLeft(name, "-/")) {
			c.add(strings.Trim(value, `"'`))
		}
	}
}

func disclosureBracketIndex(field, prefix string) (int, bool) {
	start := strings.Index(field, prefix)
	if start < 0 {
		return 0, false
	}
	start += len(prefix)
	end := strings.IndexByte(field[start:], ']')
	if end < 0 {
		return 0, false
	}
	index, err := strconv.Atoi(field[start : start+end])
	return index, err == nil && index >= 0
}

func disclosureStringAt(raw any, index int) (string, bool) {
	switch values := raw.(type) {
	case []string:
		if index >= 0 && index < len(values) {
			return values[index], true
		}
	case []any:
		if index >= 0 && index < len(values) {
			value, ok := values[index].(string)
			return value, ok
		}
	}
	return "", false
}

func (c *secretCollector) collectBody(stored string, owner map[string]any) {
	if stored == "" || isPortableBinding(stored) {
		return
	}
	payload := []byte(stored)
	if strings.EqualFold(strings.TrimSpace(stringValue(owner["body_encoding"])), "base64") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stored))
		if err != nil {
			return
		}
		payload = decoded
	}
	c.collectStructuredBody(payload, requestBodyContentType(owner))
}

func (c *secretCollector) collectStructuredBody(payload []byte, contentType string) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return
	}
	mediaType, parameters, _ := mime.ParseMediaType(strings.TrimSpace(contentType))
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	switch {
	case strings.HasPrefix(mediaType, "multipart/"):
		c.collectMultipartBody(payload, parameters["boundary"])
		return
	case strings.Contains(mediaType, "xml"), trimmed[0] == '<':
		c.collectXMLBody(payload)
		return
	case mediaType == "application/graphql":
		c.collectGraphQLBody(trimmed)
		return
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		decoder.UseNumber()
		var raw any
		if decoder.Decode(&raw) == nil {
			c.collectStructuredNode(raw, "")
		}
		return
	}
	if looksLikeGraphQLBody(trimmed) {
		c.collectGraphQLBody(trimmed)
		return
	}
	if bytes.Contains(trimmed, []byte("=")) && !bytes.ContainsAny(trimmed, "\r\n\x00") {
		if values, err := url.ParseQuery(string(trimmed)); err == nil {
			for name, entries := range values {
				if !credential.SensitiveName(name) {
					continue
				}
				for _, entry := range entries {
					c.add(entry)
				}
			}
		}
	}
}

func (c *secretCollector) collectStructuredNode(value any, name string) {
	if isSensitiveKey(name) {
		c.addNode(value)
		return
	}
	if normalizeName(name) == "query" {
		if query, ok := value.(string); ok {
			c.collectGraphQLBody([]byte(query))
			return
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			c.collectStructuredNode(child, key)
		}
	case []any:
		for _, child := range typed {
			c.collectStructuredNode(child, name)
		}
	}
}

func requestBodyContentType(owner map[string]any) string {
	if entries, ok := owner["header_list"].([]any); ok {
		for _, raw := range entries {
			entry, _ := raw.(map[string]any)
			name, _ := entry["name"].(string)
			if strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
				value, _ := entry["value"].(string)
				return value
			}
		}
	}
	switch headers := owner["headers"].(type) {
	case map[string]any:
		for name, raw := range headers {
			if strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
				value, _ := raw.(string)
				return value
			}
		}
	case map[string]string:
		for name, value := range headers {
			if strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
				return value
			}
		}
	}
	return ""
}

func (c *secretCollector) collectXMLBody(payload []byte) {
	decoder := xml.NewDecoder(bytes.NewReader(payload))
	sensitiveStack := make([]bool, 0, 16)
	for {
		token, err := decoder.Token()
		if err != nil {
			return
		}
		switch typed := token.(type) {
		case xml.StartElement:
			parentSensitive := len(sensitiveStack) > 0 &&
				sensitiveStack[len(sensitiveStack)-1]
			elementSensitive := parentSensitive ||
				credential.SensitiveName(typed.Name.Local)
			for _, attr := range typed.Attr {
				if credential.SensitiveName(attr.Name.Local) {
					c.add(attr.Value)
				}
				switch normalizeName(attr.Name.Local) {
				case "name", "key", "property":
					if credential.SensitiveName(attr.Value) {
						elementSensitive = true
					}
				}
			}
			if elementSensitive {
				for _, attr := range typed.Attr {
					switch normalizeName(attr.Name.Local) {
					case "value", "fixed", "default", "token", "secret", "password":
						c.add(attr.Value)
					}
				}
			}
			if len(sensitiveStack) >= maxProjectionDepth {
				return
			}
			sensitiveStack = append(sensitiveStack, elementSensitive)
		case xml.EndElement:
			if len(sensitiveStack) == 0 {
				return
			}
			sensitiveStack = sensitiveStack[:len(sensitiveStack)-1]
		case xml.CharData:
			if len(sensitiveStack) > 0 && sensitiveStack[len(sensitiveStack)-1] {
				c.add(string(typed))
			}
		case xml.Comment:
			if len(sensitiveStack) > 0 && sensitiveStack[len(sensitiveStack)-1] {
				c.add(string(typed))
			}
		case xml.Directive:
			if len(sensitiveStack) > 0 && sensitiveStack[len(sensitiveStack)-1] {
				c.add(string(typed))
			}
		case xml.ProcInst:
			if len(sensitiveStack) > 0 && sensitiveStack[len(sensitiveStack)-1] {
				c.add(string(typed.Inst))
			}
		}
	}
}

func (c *secretCollector) collectMultipartBody(payload []byte, boundary string) {
	if strings.TrimSpace(boundary) == "" {
		boundary = inferredMultipartBoundary(payload)
	}
	if strings.TrimSpace(boundary) == "" {
		return
	}
	reader := multipart.NewReader(bytes.NewReader(payload), boundary)
	for partIndex := 0; partIndex < 1_000; partIndex++ {
		part, err := reader.NextPart()
		if err != nil {
			return
		}
		body, readErr := io.ReadAll(io.LimitReader(part, int64(len(payload))+1))
		_ = part.Close()
		if readErr != nil || len(body) > len(payload) {
			return
		}
		if credential.SensitiveName(part.FormName()) {
			c.add(string(body))
		}
		for name, values := range part.Header {
			if credential.SensitiveHeader(name) || credential.SensitiveName(name) {
				for _, value := range values {
					c.add(value)
				}
			}
		}
		c.collectStructuredBody(body, part.Header.Get("Content-Type"))
	}
}

func inferredMultipartBoundary(payload []byte) string {
	line := payload
	if end := bytes.IndexByte(line, '\n'); end >= 0 {
		line = line[:end]
	}
	line = bytes.TrimSpace(bytes.TrimSuffix(line, []byte("\r")))
	if !bytes.HasPrefix(line, []byte("--")) || len(line) <= 2 {
		return ""
	}
	return string(bytes.TrimSuffix(line[2:], []byte("--")))
}

func looksLikeGraphQLBody(payload []byte) bool {
	lower := strings.ToLower(strings.TrimSpace(string(payload)))
	for _, prefix := range []string{
		"query ", "query{", "mutation ", "mutation{",
		"subscription ", "subscription{", "fragment ",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func (c *secretCollector) collectGraphQLBody(payload []byte) {
	var pendingSensitive bool
	var awaitingValue bool
	for index := 0; index < len(payload); {
		switch {
		case isGraphQLNameStart(payload[index]):
			start := index
			index++
			for index < len(payload) && isGraphQLNameContinue(payload[index]) {
				index++
			}
			pendingSensitive = credential.SensitiveName(string(payload[start:index]))
			awaitingValue = false
		case payload[index] == ':':
			awaitingValue = pendingSensitive
			index++
		case payload[index] == '"':
			value, next, ok := graphQLStringLiteral(payload, index)
			if !ok {
				return
			}
			if awaitingValue {
				c.add(value)
			}
			awaitingValue = false
			pendingSensitive = false
			index = next
		case payload[index] == '#':
			for index < len(payload) && payload[index] != '\n' {
				index++
			}
		case payload[index] == ',' || payload[index] == ')' ||
			payload[index] == '}':
			awaitingValue = false
			pendingSensitive = false
			index++
		default:
			index++
		}
	}
}

func isGraphQLNameStart(value byte) bool {
	return value == '_' || value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z'
}

func isGraphQLNameContinue(value byte) bool {
	return isGraphQLNameStart(value) || value >= '0' && value <= '9'
}

func graphQLStringLiteral(payload []byte, start int) (string, int, bool) {
	if start+2 < len(payload) && bytes.Equal(payload[start:start+3], []byte(`"""`)) {
		end := bytes.Index(payload[start+3:], []byte(`"""`))
		if end < 0 {
			return "", len(payload), false
		}
		end += start + 3
		return string(payload[start+3 : end]), end + 3, true
	}
	index := start + 1
	escaped := false
	for index < len(payload) {
		switch {
		case escaped:
			escaped = false
		case payload[index] == '\\':
			escaped = true
		case payload[index] == '"':
			quoted := string(payload[start : index+1])
			value, err := strconv.Unquote(quoted)
			if err != nil {
				return "", index + 1, false
			}
			return value, index + 1, true
		}
		index++
	}
	return "", len(payload), false
}

func (c *secretCollector) sorted() []string {
	return sortedSecrets(c.values)
}

func credentialParameterNames(script *dsl.Script) map[string]struct{} {
	result := make(map[string]struct{})
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return result
	}
	value := script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialParameterNamesKey]
	switch typed := value.(type) {
	case []string:
		for _, name := range typed {
			if name = canonicalName(name); name != "" {
				result[name] = struct{}{}
			}
		}
	case []any:
		for _, item := range typed {
			if name, ok := item.(string); ok {
				if name = canonicalName(name); name != "" {
					result[name] = struct{}{}
				}
			}
		}
	}
	return result
}

func sensitiveDataSourceColumns(script *dsl.Script) map[string][]string {
	result := make(map[string][]string)
	if script == nil {
		return result
	}
	proven := credentialParameterNames(script)
	for _, parameter := range script.Parameters {
		_, provenCredential := proven[canonicalName(parameter.Name)]
		if !provenCredential && !isSensitiveKey(parameter.Name) && !isSensitiveKey(parameter.Column) {
			continue
		}
		source := canonicalName(parameter.DataSource)
		if source == "" {
			continue
		}
		result[source] = append(result[source], strings.TrimSpace(parameter.Column))
	}
	if script.Metadata != nil && script.Metadata.Import != nil {
		value := script.Metadata.Import.Preserved["recorded_input_secret_columns"]
		switch typed := value.(type) {
		case []string:
			result["recorded_inputs"] = append(result["recorded_inputs"], typed...)
		case []any:
			for _, item := range typed {
				if column, ok := item.(string); ok {
					result["recorded_inputs"] = append(result["recorded_inputs"], column)
				}
			}
		}
	}
	return result
}
