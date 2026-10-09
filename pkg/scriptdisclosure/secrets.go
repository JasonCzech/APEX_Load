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
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

const (
	maxStructuredCollectionBytes  = 64 << 20
	maxStructuredBodyBytes        = 8 << 20
	maxStructuredCollectionValues = 1_000_000
	maxStructuredCollectionDepth  = 128
	maxStructuredJSONStringLayers = 3
)

var plausibleCollectionTemplateExpression = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*(?:\(\s*[A-Za-z0-9_.+\s,-]*\))?$`)

type structuredCollectionBudget struct {
	bytes      int
	values     int
	incomplete bool
}

func newStructuredCollectionBudget() *structuredCollectionBudget {
	return &structuredCollectionBudget{bytes: maxStructuredCollectionBytes, values: maxStructuredCollectionValues}
}

func (b *structuredCollectionBudget) reserveBytes(n int) bool {
	if b.incomplete || n < 0 || n > b.bytes {
		b.incomplete = true
		return false
	}
	b.bytes -= n
	return true
}

func (b *structuredCollectionBudget) reserveValue() bool {
	if b.incomplete || b.values <= 0 {
		b.incomplete = true
		return false
	}
	b.values--
	return true
}

type secretCollector struct {
	values                   map[string]struct{}
	structuredBudget         *structuredCollectionBudget
	credentialParameterNames map[string]struct{}
	sensitiveSourceColumns   map[string][]string
}

func newSecretCollector(
	credentialParameterNames map[string]struct{},
	sensitiveSourceColumns map[string][]string,
) *secretCollector {
	return &secretCollector{
		values:                   make(map[string]struct{}),
		structuredBudget:         newStructuredCollectionBudget(),
		credentialParameterNames: credentialParameterNames,
		sensitiveSourceColumns:   sensitiveSourceColumns,
	}
}

func (c *secretCollector) collect(value any, ctx projectionContext, depth int) {
	if depth > maxProjectionDepth || !c.structuredBudget.reserveValue() {
		c.structuredBudget.incomplete = true
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		if ctx.headerList {
			name, _ := typed["name"].(string)
			if credential.SensitiveHeader(name) || isSensitiveKey(name) {
				c.addNamedNode(name, typed["value"])
			}
		}
		if ctx.dataSource {
			c.collectDataSource(typed)
		}
		credentialValue := mapHasCredentialValue(typed, c.credentialParameterNames)
		for key, child := range typed {
			if ctx.key == "sapgui" && !ctx.metadata && !ctx.untypedMap && key == "session_behavior" {
				if text, ok := child.(string); ok && dsl.IsSAPGUISessionBehavior(text) {
					continue
				}
			}
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
			case normalized == "bodymultipart":
				c.collectMultipartParts(child)
			case ctx.headers && (credential.SensitiveHeader(key) || isSensitiveKey(key)):
				c.addNamedNode(key, child)
			case isSensitiveKey(key) && !isSecretReferenceKey(key):
				switch child.(type) {
				case map[string]any, []any:
					// Container names such as auth and credentials describe a
					// structure; their type/username siblings are not secret
					// merely because the parent is credential-shaped. Recurse
					// so only password/token/etc. leaves are collected.
					c.collect(child, projectionContext{
						key:        key,
						metadata:   ctx.metadata,
						untypedMap: ctx.untypedMap || isUntypedDisclosureMapKey(key),
					}, depth+1)
				default:
					c.addNamedNode(key, child)
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
					untypedMap:     ctx.untypedMap || isUntypedDisclosureMapKey(key),
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
	c.addSensitiveNode(value, 0)
}

// addNamedNode adds a value that is a credential because of its field name,
// unless it is too short to tell apart from ordinary text (see
// credential.MatchableNamedValue).
func (c *secretCollector) addNamedNode(name string, value any) {
	if text, ok := value.(string); ok && !credential.MatchableNamedValue(name, text) {
		return
	}
	c.addNode(value)
}

func (c *secretCollector) addSensitiveNode(value any, depth int) {
	if depth > maxProjectionDepth || !c.structuredBudget.reserveValue() {
		c.structuredBudget.incomplete = true
		return
	}
	switch typed := value.(type) {
	case string:
		c.add(typed)
		nested := bytes.TrimSpace([]byte(typed))
		if looksStructuredJSON(nested) {
			c.collectJSON(nested, true, 0, true)
		}
	case []any:
		for _, child := range typed {
			c.addSensitiveNode(child, depth+1)
		}
	case map[string]any:
		for _, child := range typed {
			c.addSensitiveNode(child, depth+1)
		}
	}
}

func (c *secretCollector) add(value string) {
	if value == "" || isPortableBinding(value) {
		return
	}
	trimmed := strings.TrimSpace(value)
	if trimmed != "" {
		for _, scheme := range []string{"Bearer", "Basic", "Digest"} {
			if len(trimmed) > len(scheme) &&
				strings.EqualFold(trimmed[:len(scheme)], scheme) &&
				trimmed[len(scheme)] == ' ' {
				c.add(strings.TrimSpace(trimmed[len(scheme):]))
				return
			}
		}
	}
	c.values[value] = struct{}{}
	if trimmed != "" {
		c.values[trimmed] = struct{}{}
	}
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
			if credential.MatchableNamedValue(key, value) {
				c.add(value)
			}
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
		case importedBaseURLTargetField(field):
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

func importedBaseURLTargetField(field string) bool {
	field = strings.ToLower(strings.TrimSpace(field))
	return field == "base_url" ||
		strings.HasPrefix(field, "base_url.") ||
		field == "config.base_url" ||
		strings.HasPrefix(field, "config.base_url.")
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
		rawName, encoded, _ := strings.Cut(pairs[target.Index], "=")
		if value, err := url.QueryUnescape(encoded); err == nil {
			if name, _ := url.QueryUnescape(rawName); credential.MatchableNamedValue(name, value) {
				c.add(value)
			}
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

// Validate original string bytes before the outer script JSON marshal can
// replace invalid UTF8. This bounded scan shares the collection work budget.
func (c *secretCollector) collectOriginalBodyValidity(script *dsl.Script) {
	if err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		stored := ""
		if ref.Action != nil {
			stored = ref.Action.Body
		} else if ref.Request != nil {
			stored, _ = ref.Request["body"].(string)
		}
		if len(stored) > maxStructuredBodyBytes || !c.structuredBudget.reserveBytes(len(stored)) || !utf8.ValidString(stored) {
			c.structuredBudget.incomplete = true
		}
		return nil
	}); err != nil {
		c.structuredBudget.incomplete = true
	}
}

func (c *secretCollector) collectBody(stored string, owner map[string]any) {
	if stored == "" || isPortableBinding(stored) || c.structuredBudget.incomplete {
		return
	}
	if len(stored) > maxStructuredBodyBytes {
		c.structuredBudget.incomplete = true
		return
	}
	payload := []byte(stored)
	if strings.EqualFold(strings.TrimSpace(stringValue(owner["body_encoding"])), "base64") {
		if !c.structuredBudget.reserveBytes(len(stored)) {
			return
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stored))
		if err != nil {
			return
		}
		payload = decoded
	}
	c.collectStructuredBody(payload, requestBodyContentType(owner))
}

func (c *secretCollector) collectStructuredBody(payload []byte, contentType string) {
	if len(payload) > maxStructuredBodyBytes || !c.structuredBudget.reserveBytes(len(payload)) {
		c.structuredBudget.incomplete = true
		return
	}
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
		c.collectQueryBody(string(trimmed), false, 0)
		return
	}
	if looksStructuredJSON(trimmed) {
		c.collectJSON(trimmed, false, 0, false)
		return
	}
	if looksLikeGraphQLBody(trimmed) {
		c.collectQueryBody(string(trimmed), false, 0)
		return
	}
	if bytes.Contains(trimmed, []byte("=")) && !bytes.ContainsAny(trimmed, "\r\n\x00") {
		remaining := string(trimmed)
		for {
			if !c.structuredBudget.reserveValue() {
				return
			}
			part, tail, more := strings.Cut(remaining, "&")
			rawName, rawValue, _ := strings.Cut(part, "=")
			name, nameErr := url.QueryUnescape(rawName)
			value, valueErr := url.QueryUnescape(rawValue)
			if nameErr != nil || valueErr != nil {
				if !more {
					return
				}
				remaining = tail
				continue
			}
			sensitive := isSensitiveKey(name)
			if sensitive && credential.MatchableNamedValue(name, value) {
				c.add(value)
			}
			nested := bytes.TrimSpace([]byte(value))
			if normalizeName(name) == "query" || looksLikeGraphQLBody(nested) {
				c.collectQueryBody(value, sensitive, 0)
			} else if looksStructuredJSON(nested) {
				c.collectJSON(nested, sensitive, 0, true)
			}
			if !more || c.structuredBudget.incomplete {
				return
			}
			remaining = tail
		}
	}
}

func looksStructuredJSON(value []byte) bool {
	return len(value) > 0 && !bytes.Equal(value, []byte(credential.Redacted)) && !wholeCollectionRuntimeTemplate(string(value)) && (value[0] == '{' || value[0] == '[')
}

// Plausible scalar template syntax is separate from runtime portability:
// unsupported names/functions still become literal credentials in sensitive
// fields. JSON-like double braces with member punctuation must reach the
// malformed-credential hint instead of hiding a credential occurrence.
func wholeCollectionRuntimeTemplate(value string) bool {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "{{") || portableTemplatePattern.FindString(trimmed) != trimmed {
		return false
	}
	return isSupportedPortableTemplate(trimmed) || plausibleCollectionTemplateExpression.MatchString(strings.TrimSpace(trimmed[2:len(trimmed)-2]))
}

// Collect directly from JSON tokens so duplicate members and encoded JSON
// string boundaries cannot hide a credential occurrence. Every parse shares
// one script-wide byte/value budget; incomplete collection stops disclosure.
func (c *secretCollector) collectJSON(payload []byte, sensitive bool, layers int, charge bool) {
	if layers > maxStructuredJSONStringLayers || len(payload) > maxStructuredBodyBytes || (charge && !c.structuredBudget.reserveBytes(len(payload))) {
		c.structuredBudget.incomplete = true
		return
	}
	if !c.structuredBudget.reserveBytes(len(payload)) || !validCollectionJSONUnicode(payload) {
		c.structuredBudget.incomplete = true
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	complete := c.collectJSONValue(decoder, sensitive, 0, layers, "")
	if complete {
		_, err := decoder.Token()
		complete = err == io.EOF
	}
	if !complete && !c.structuredBudget.incomplete {
		// Existing credential-free malformed/template authoring remains
		// readable. A partial parse with any credential-shaped name cannot
		// prove replica safety and must stop the entire disclosure copy.
		if c.malformedCollectionCredentialHint(payload, layers) {
			c.structuredBudget.incomplete = true
		}
	}
}

// encoding/json normalizes invalid strings. Reject those semantic losses
// before collecting literals, while leaving ordinary syntax errors to the
// central body projector's fail-closed body handling.
func (c *secretCollector) malformedCollectionCredentialHint(payload []byte, layers int) bool {
	if layers > maxStructuredJSONStringLayers || !c.structuredBudget.reserveBytes(2*len(payload)) {
		return true
	}
	for i := 0; i < len(payload); {
		if payload[i] == '"' {
			start := i
			i++
			for i < len(payload) {
				if payload[i] == '\\' {
					i += 2
					continue
				}
				if payload[i] == '"' {
					break
				}
				i++
			}
			if !c.structuredBudget.reserveValue() {
				return true
			}
			if i < len(payload) {
				var decoded string
				if json.Unmarshal(payload[start:i+1], &decoded) == nil {
					if isSensitiveKey(decoded) {
						return true
					}
					nested := bytes.TrimSpace([]byte(decoded))
					if looksStructuredJSON(nested) && c.malformedCollectionCredentialHint(nested, layers+1) {
						return true
					}
				} else if c.malformedCollectionRawNameHint(payload[start : i+1]) {
					return true
				}
				i++
			} else if c.malformedCollectionRawNameHint(payload[start:]) {
				return true
			}
			continue
		}
		if !collectionNameByte(payload[i]) {
			i++
			continue
		}
		start := i
		for i < len(payload) && collectionNameByte(payload[i]) {
			i++
		}
		if !c.structuredBudget.reserveValue() || isSensitiveKey(string(payload[start:i])) {
			return true
		}
	}
	return false
}

func (c *secretCollector) malformedCollectionRawNameHint(payload []byte) bool {
	// Decode only well-formed unicode name escapes; malformed quoted syntax
	// is not proof that a later credential name is absent.
	var normalized strings.Builder
	normalized.Grow(len(payload))
	for i := 0; i < len(payload); i++ {
		if i+5 < len(payload) && payload[i] == '\\' && payload[i+1] == 'u' {
			code, err := strconv.ParseUint(string(payload[i+2:i+6]), 16, 16)
			if err == nil {
				normalized.WriteRune(rune(code))
				i += 5
				continue
			}
		}
		normalized.WriteByte(payload[i])
	}
	text := normalized.String()
	for i := 0; i < len(text); {
		if !collectionNameByte(text[i]) {
			i++
			continue
		}
		start := i
		for i < len(text) && collectionNameByte(text[i]) {
			i++
		}
		if !c.structuredBudget.reserveValue() || isSensitiveKey(text[start:i]) {
			return true
		}
	}
	return false
}

func collectionNameByte(ch byte) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' || ch == '.'
}

func validCollectionJSONUnicode(payload []byte) bool {
	if !utf8.Valid(payload) {
		return false
	}
	inString := false
	for i := 0; i < len(payload); i++ {
		if payload[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || payload[i] != '\\' {
			continue
		}
		i++
		if i >= len(payload) {
			return true
		}
		if payload[i] != 'u' || i+4 >= len(payload) {
			continue
		}
		code, err := strconv.ParseUint(string(payload[i+1:i+5]), 16, 16)
		if err != nil {
			continue
		}
		i += 4
		if code >= 0xd800 && code <= 0xdbff {
			if i+6 >= len(payload) || payload[i+1] != '\\' || payload[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(payload[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		} else if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
	}
	return true
}

func (c *secretCollector) collectJSONValue(decoder *json.Decoder, sensitive bool, depth, layers int, name string) bool {
	if depth > maxStructuredCollectionDepth || !c.structuredBudget.reserveValue() {
		c.structuredBudget.incomplete = true
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			for decoder.More() {
				if !c.structuredBudget.reserveValue() {
					return false
				}
				key, err := decoder.Token()
				name, ok := key.(string)
				if err != nil || !ok {
					return false
				}
				if !c.collectJSONValue(decoder, sensitive || isSensitiveKey(name), depth+1, layers, name) {
					return false
				}
			}
		case '[':
			for decoder.More() {
				if !c.collectJSONValue(decoder, sensitive, depth+1, layers, name) {
					return false
				}
			}
		default:
			return false
		}
		if _, err := decoder.Token(); err != nil {
			// EOF after every child token was consumed leaves only a missing
			// container terminator. The central body policy still masks/gates
			// this body; collected literals can safely mask its outside replicas.
			// EOF in a key/value or any other syntax error remains incomplete.
			return err == io.EOF
		}
	case string:
		if sensitive && credential.MatchableNamedValue(name, value) {
			c.add(value)
		}
		nested := bytes.TrimSpace([]byte(value))
		if normalizeName(name) == "query" {
			c.collectQueryBody(value, sensitive, layers+1)
			return !c.structuredBudget.incomplete
		}
		if looksStructuredJSON(nested) {
			c.collectJSON(nested, sensitive, layers+1, true)
			if c.structuredBudget.incomplete {
				return false
			}
		}
	}
	return true
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
	nameStack := make([]string, 0, 16)
	// addText adds text found under the current element's sensitive name.
	addText := func(text string) {
		if len(sensitiveStack) > 0 && sensitiveStack[len(sensitiveStack)-1] &&
			credential.MatchableNamedValue(nameStack[len(nameStack)-1], text) {
			c.add(text)
		}
	}
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
				if credential.SensitiveName(attr.Name.Local) && credential.MatchableNamedValue(attr.Name.Local, attr.Value) {
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
						if credential.MatchableNamedValue(typed.Name.Local, attr.Value) || credential.PasswordLikeName(attr.Name.Local) {
							c.add(attr.Value)
						}
					}
				}
			}
			if len(sensitiveStack) >= maxProjectionDepth {
				return
			}
			sensitiveStack = append(sensitiveStack, elementSensitive)
			nameStack = append(nameStack, typed.Name.Local)
		case xml.EndElement:
			if len(sensitiveStack) == 0 {
				return
			}
			sensitiveStack = sensitiveStack[:len(sensitiveStack)-1]
			nameStack = nameStack[:len(nameStack)-1]
		case xml.CharData:
			addText(string(typed))
		case xml.Comment:
			addText(string(typed))
		case xml.Directive:
			addText(string(typed))
		case xml.ProcInst:
			addText(string(typed.Inst))
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
		if credential.SensitiveName(part.FormName()) && credential.MatchableNamedValue(part.FormName(), string(body)) {
			c.add(string(body))
		}
		for name, values := range part.Header {
			if credential.SensitiveHeader(name) || credential.SensitiveName(name) {
				for _, value := range values {
					if credential.MatchableNamedValue(name, value) {
						c.add(value)
					}
				}
			}
		}
		c.collectStructuredBody(body, part.Header.Get("Content-Type"))
	}
}

// collectMultipartParts collects from a body_multipart as from a recorded
// multipart body: a sensitive field name or part header marks its value, and
// structured part content is searched too.
func (c *secretCollector) collectMultipartParts(raw any) {
	body, _ := raw.(map[string]any)
	parts, _ := body["parts"].([]any)
	for index, item := range parts {
		if index >= 1_000 || c.structuredBudget.incomplete {
			return
		}
		part, _ := item.(map[string]any)
		name, _ := part["name"].(string)
		value, _ := part["value"].(string)
		stored := value
		if encoding, _ := part["value_encoding"].(string); strings.EqualFold(strings.TrimSpace(encoding), "base64") {
			if !c.structuredBudget.reserveBytes(len(value)) {
				return
			}
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
			if err != nil {
				continue
			}
			value = string(decoded)
		}
		if credential.SensitiveName(name) && credential.MatchableNamedValue(name, value) {
			c.add(value)
			if stored != value {
				// The script holds the base64 form.
				c.add(stored)
			}
		}
		headers, _ := part["headers"].(map[string]any)
		for headerName, headerValue := range headers {
			text, _ := headerValue.(string)
			if (credential.SensitiveHeader(headerName) || credential.SensitiveName(headerName)) && credential.MatchableNamedValue(headerName, text) {
				c.add(text)
			}
		}
		if value != "" && !isPortableBinding(value) {
			contentType, _ := part["content_type"].(string)
			c.collectStructuredBody([]byte(value), contentType)
		}
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

// Every declared, inferred, JSON-string and form query uses this admission
// before scalar collection. No source-specific branch bypasses its budget or
// malformed JSON-member handling.
func (c *secretCollector) collectQueryBody(value string, sensitive bool, layers int) {
	queryJSON, work, classifyErr := credentialpolicy.InspectQueryValueStructure(value, c.structuredBudget.bytes)
	if !c.structuredBudget.reserveBytes(work) || classifyErr != nil {
		c.structuredBudget.incomplete = true
		return
	}
	if queryJSON && !wholeCollectionRuntimeTemplate(value) {
		c.collectJSON([]byte(value), sensitive, layers, true)
		return
	}
	c.collectGraphQLBody([]byte(value))
}

func (c *secretCollector) collectGraphQLBody(payload []byte) {
	var pendingName string
	var pendingSensitive bool
	var awaitingValue bool
	for index := 0; index < len(payload); {
		switch {
		case isGraphQLNameStart(payload[index]):
			if !c.structuredBudget.reserveValue() {
				return
			}
			start := index
			index++
			for index < len(payload) && isGraphQLNameContinue(payload[index]) {
				index++
			}
			pendingName = string(payload[start:index])
			pendingSensitive = credential.SensitiveName(pendingName)
			awaitingValue = false
		case payload[index] == ':':
			awaitingValue = pendingSensitive
			index++
		case payload[index] == '"':
			if !c.structuredBudget.reserveValue() {
				return
			}
			value, next, ok := graphQLStringLiteral(payload, index)
			if !ok {
				return
			}
			if awaitingValue && credential.MatchableNamedValue(pendingName, value) {
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
