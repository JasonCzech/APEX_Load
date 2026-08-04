package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	stdhttp "net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

// One structured request may contain up to credentialpolicy.DefaultLimits'
// 100,000 governed values. Keep locator decoding independently bounded while
// allowing a valid maximum-sized conversion to reach the runtime matcher.
const maxImportedCredentialLocatorBytes = 16 << 20

const (
	maxDisclosureSourceEnvelopeItems = 256
	maxDisclosureSourceValueBytes    = 64 << 10
	maxDisclosureSourceEnvelopeBytes = 2 << 20

	// Legacy import rows predate importer-emitted credential locators. Their
	// executable request bodies are inspected with the same bounded-work
	// envelope used by current conversion policy. The scanner is deliberately
	// request-body-only: review metadata, response assertions, extractors, and
	// opaque source artifacts are never treated as executable credentials.
	maxLegacyCredentialBodyDepth      = 128
	maxLegacyCredentialBodyValues     = 100_000
	maxLegacyCredentialMultipartDepth = 16
	maxLegacyCredentialBodyScanBytes  = 32 << 20
)

type importedCredentialTarget struct {
	RequestKey string `json:"request_key,omitempty"`
	Field      string `json:"field"`
	Index      int    `json:"index,omitempty"`
	Pointer    string `json:"pointer,omitempty"`
	Occurrence int    `json:"occurrence,omitempty"`
}

type importedCredentialTemplateReference struct {
	namespace string
	name      string
}

const importedOpenAPIPathCredentialPointerPrefix = "openapi-path-v1:"

type importedOpenAPIPathSpan struct {
	start int
	end   int
}

type importedOpenAPIPathCredentialPointer struct {
	parameter     string
	segmentLength int
	shapeHash     [sha256.Size]byte
	targetSpans   []importedOpenAPIPathSpan
	allSpans      []importedOpenAPIPathSpan
}

// importedCredentialTemplatePlan contains no credential values. It records only
// authored runtime-template identities that occur in credential-governed
// executable fields. A plan can therefore be shared by every VUser running the
// same immutable script without exposing or retaining per-VUser material.
type importedCredentialTemplatePlan struct {
	expressions map[string]struct{}
	variables   map[string]struct{}
	references  []importedCredentialTemplateReference
}

// ImportedCredentialValues returns literal credentials intentionally retained
// in a server-produced import. The values are read from executable fields only
// and are used exclusively to build the runtime redaction matcher; the durable
// script remains byte-exact and runnable without an operator re-entering them.
func ImportedCredentialValues(script *dsl.Script) ([]string, error) {
	values, _, err := inspectImportedCredentials(script)
	return values, err
}

// ImportedCredentialValuesForDisclosure applies the same fail-closed locator
// contract as runtime inspection. A stale locator can mean that a credential
// moved to a neutral-name field, so publishing a best-effort projection would
// disclose an unproven value. Callers may return an error or replace their
// complete output surface with a disclosure-safe marker.
func ImportedCredentialValuesForDisclosure(script *dsl.Script) ([]string, error) {
	values, err := ImportedCredentialValues(script)
	if err != nil {
		return nil, err
	}
	collector := importedCredentialCollector{
		resolved: true,
		seen:     make(map[string]struct{}, len(values)),
		values:   append([]string(nil), values...),
	}
	for _, value := range values {
		collector.seen[value] = struct{}{}
	}
	if err := collector.collectProtectedImportSourceCredentials(script); err != nil {
		return nil, err
	}
	return collector.values, nil
}

type disclosureJavaVMSourceEnvelope struct {
	Version    int      `json:"version"`
	RowsBase64 []string `json:"rows_base64"`
}

type disclosureJavaJDBCSourceEnvelope struct {
	Version     int `json:"version"`
	Connections []struct {
		DriverBase64 string `json:"driver_base64"`
		DSNBase64    string `json:"dsn_base64"`
		Reason       string `json:"reason"`
		SourceRef    string `json:"source_ref"`
	} `json:"connections"`
}

func inspectImportedCredentials(
	script *dsl.Script,
) ([]string, *importedCredentialTemplatePlan, error) {
	if !scriptPreservesImportedCredentialValues(script) {
		return nil, nil, nil
	}
	plan := &importedCredentialTemplatePlan{
		expressions: make(map[string]struct{}),
		variables:   make(map[string]struct{}),
	}
	collector := importedCredentialCollector{
		script:       script,
		templatePlan: plan,
		seen:         make(map[string]struct{}),
	}

	if err := collector.collectSensitiveConfig(script.Config); err != nil {
		return nil, nil, fmt.Errorf("inspect imported credential config: %w", err)
	}
	collector.collectURL(script.Config.BaseURL, nil)
	if preserved := script.Metadata.Import.Preserved; preserved != nil {
		collector.collectPreservedResourceURLs(preserved["recorded_resource_urls"])
		targets, err := decodeImportedCredentialTargets(
			preserved[dsl.ImportPreservedCredentialTargetsMetadataKey],
		)
		if err != nil {
			return nil, nil, fmt.Errorf("inspect imported credential locators: %w", err)
		}
		for _, target := range targets {
			if err := collector.collectScriptConfigTarget(target); err != nil {
				return nil, nil, err
			}
		}
	}

	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		targets, err := decodeImportedCredentialTargets(
			ref.Metadata()[dsl.ImportPreservedCredentialTargetsMetadataKey],
		)
		if err != nil {
			return fmt.Errorf("%s: %w", ref.Path, err)
		}
		collector.collectSurfaceKnownCredentials(ref)
		if len(targets) == 0 {
			if err := collector.collectLegacySurfaceBodyCredentials(ref); err != nil {
				return fmt.Errorf("%s: %w", ref.Path, err)
			}
		}
		for _, target := range targets {
			if err := collector.collectSurfaceTarget(ref, target); err != nil {
				return fmt.Errorf("%s: %w", ref.Path, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("inspect imported credential actions: %w", err)
	}
	if len(plan.expressions) == 0 {
		plan = nil
	}
	return collector.values, plan, nil
}

func (collector *importedCredentialCollector) collectPreservedResourceURLs(raw any) {
	switch values := raw.(type) {
	case []string:
		for _, value := range values {
			collector.collectURL(value, nil)
		}
	case []any:
		for _, rawValue := range values {
			value, ok := rawValue.(string)
			if ok {
				collector.collectURL(value, nil)
			}
		}
	}
}

func scriptPreservesImportedCredentialValues(script *dsl.Script) bool {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return false
	}
	policy, _ := script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialPolicyKey].(string)
	return strings.EqualFold(strings.TrimSpace(policy), dsl.ImportPreservedCredentialPolicyPreserve)
}

func (collector *importedCredentialCollector) collectProtectedImportSourceCredentials(
	script *dsl.Script,
) error {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Preserved == nil {
		return nil
	}
	preserved := script.Metadata.Import.Preserved
	if raw, exists := preserved[dsl.ImportPreservedEnterpriseJavaVMSourceKey]; exists {
		var envelope disclosureJavaVMSourceEnvelope
		if err := decodeDisclosureSourceEnvelope(raw, &envelope); err != nil ||
			envelope.Version != 1 || len(envelope.RowsBase64) == 0 ||
			len(envelope.RowsBase64) > maxDisclosureSourceEnvelopeItems {
			return errors.New("protected Java VM source envelope is malformed")
		}
		total := 0
		for _, encoded := range envelope.RowsBase64 {
			row, err := decodeDisclosureSourceValue(encoded, &total)
			if err != nil {
				return errors.New("protected Java VM source envelope is malformed")
			}
			collector.add(row)
			if _, value, found := strings.Cut(row, "="); found {
				collector.add(strings.Trim(value, `"'`))
				collector.collectURL(strings.Trim(value, `"'`), nil)
			}
			collector.collectEmbeddedURL(row)
		}
	}
	if raw, exists := preserved[dsl.ImportPreservedEnterpriseJavaJDBCSourceKey]; exists {
		var envelope disclosureJavaJDBCSourceEnvelope
		if err := decodeDisclosureSourceEnvelope(raw, &envelope); err != nil ||
			envelope.Version != 1 || len(envelope.Connections) == 0 ||
			len(envelope.Connections) > maxDisclosureSourceEnvelopeItems {
			return errors.New("protected Java JDBC source envelope is malformed")
		}
		total := 0
		for _, connection := range envelope.Connections {
			if _, err := decodeDisclosureSourceValue(connection.DriverBase64, &total); err != nil {
				return errors.New("protected Java JDBC source envelope is malformed")
			}
			dsn, err := decodeDisclosureSourceValue(connection.DSNBase64, &total)
			if err != nil || dsn == "" {
				return errors.New("protected Java JDBC source envelope is malformed")
			}
			collector.collectDSN(dsn)
		}
	}
	return nil
}

func decodeDisclosureSourceEnvelope(raw any, destination any) error {
	payload, err := json.Marshal(raw)
	if err != nil || len(payload) > maxDisclosureSourceEnvelopeBytes*2 {
		return errors.New("protected import source envelope is malformed")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("protected import source envelope has trailing data")
	}
	return nil
}

func decodeDisclosureSourceValue(encoded string, total *int) (string, error) {
	if len(encoded) > base64.StdEncoding.EncodedLen(maxDisclosureSourceValueBytes) {
		return "", errors.New("protected import source value is too large")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) > maxDisclosureSourceValueBytes {
		return "", errors.New("protected import source value is malformed")
	}
	*total += len(decoded)
	if *total > maxDisclosureSourceEnvelopeBytes {
		clear(decoded)
		return "", errors.New("protected import source envelope is too large")
	}
	value := string(decoded)
	clear(decoded)
	return value, nil
}

func (collector *importedCredentialCollector) collectEmbeddedURL(value string) {
	for offset := 0; offset < len(value); {
		schemeEnd := strings.Index(value[offset:], "://")
		if schemeEnd < 0 {
			return
		}
		schemeEnd += offset
		start := schemeEnd
		for start > 0 && isURLSchemeCharacter(value[start-1]) {
			start--
		}
		end := len(value)
		for index := schemeEnd + 3; index < len(value); index++ {
			switch value[index] {
			case ' ', '\t', '\r', '\n', '"', '\'', ')', ']':
				end = index
			}
			if end != len(value) {
				break
			}
		}
		collector.collectURL(value[start:end], nil)
		offset = max(end, schemeEnd+3)
	}
}

func isURLSchemeCharacter(value byte) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		value == '+' || value == '-' || value == '.'
}

func decodeImportedCredentialTargets(raw any) ([]importedCredentialTarget, error) {
	if raw == nil {
		return nil, nil
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil, errors.New("credential locator metadata is malformed")
	}
	if len(payload) > maxImportedCredentialLocatorBytes {
		return nil, errors.New("credential locator metadata exceeds the runtime limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var targets []importedCredentialTarget
	if err := decoder.Decode(&targets); err != nil {
		return nil, errors.New("credential locator metadata is malformed")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("credential locator metadata has trailing data")
	}
	for _, target := range targets {
		if strings.TrimSpace(target.Field) == "" || target.Index < 0 || target.Occurrence < 0 {
			return nil, errors.New("credential locator metadata is invalid")
		}
	}
	return targets, nil
}

type importedCredentialCollector struct {
	script       *dsl.Script
	templatePlan *importedCredentialTemplatePlan
	resolved     bool
	seen         map[string]struct{}
	values       []string
}

func (collector *importedCredentialCollector) add(value string) {
	if value == "" || value == credential.Redacted {
		return
	}
	if !collector.resolved {
		collector.collectTemplateReferences(value)
		trimmed := strings.TrimSpace(value)
		matches := templatePattern.FindAllStringSubmatch(trimmed, -1)
		if len(matches) > 0 {
			for _, match := range matches {
				if len(match) != 2 {
					continue
				}
				expression := strings.TrimSpace(match[1])
				switch {
				case strings.HasPrefix(expression, "var."):
					collector.collectParameterValues(strings.TrimSpace(strings.TrimPrefix(expression, "var.")))
				case strings.HasPrefix(expression, "param."):
					collector.collectParameterValues(strings.TrimSpace(strings.TrimPrefix(expression, "param.")))
				}
			}
			// Runtime templates are protected from their resolved values. Keeping
			// the expression itself in the matcher would not cover the wire value.
			return
		}
	}
	if _, exists := collector.seen[value]; exists {
		return
	}
	collector.seen[value] = struct{}{}
	collector.values = append(collector.values, value)
}

func (collector *importedCredentialCollector) collectTemplateReferences(value string) {
	if collector == nil || collector.templatePlan == nil || value == "" {
		return
	}
	for _, match := range templatePattern.FindAllStringSubmatch(value, -1) {
		if len(match) != 2 {
			continue
		}
		expression := strings.TrimSpace(match[1])
		if expression == "" {
			continue
		}
		if _, exists := collector.templatePlan.expressions[expression]; exists {
			continue
		}
		collector.templatePlan.expressions[expression] = struct{}{}
		namespace, name, ok := strings.Cut(expression, ".")
		if !ok {
			continue
		}
		switch namespace {
		case "var", "param", "data", "env":
			name = strings.TrimSpace(name)
			if name != "" {
				if namespace == "var" || namespace == "data" {
					collector.templatePlan.variables[name] = struct{}{}
				}
				collector.templatePlan.references = append(
					collector.templatePlan.references,
					importedCredentialTemplateReference{namespace: namespace, name: name},
				)
			}
		}
	}
}

func (collector *importedCredentialCollector) collectParameterValues(name string) {
	if collector.script == nil || name == "" {
		return
	}
	var matched *dsl.Parameter
	for index := range collector.script.Parameters {
		parameter := &collector.script.Parameters[index]
		if !strings.EqualFold(strings.TrimSpace(parameter.Name), name) {
			continue
		}
		if matched != nil {
			// Runtime parameter lookup is case-insensitive. An ambiguous source
			// cannot be mapped safely to one retained value.
			return
		}
		matched = parameter
	}
	if matched == nil {
		return
	}
	for _, value := range matched.Values {
		if value == "" || value == credential.Redacted {
			continue
		}
		if _, exists := collector.seen[value]; exists {
			continue
		}
		collector.seen[value] = struct{}{}
		collector.values = append(collector.values, value)
	}
}

func (collector *importedCredentialCollector) collectSensitiveConfig(config dsl.ScriptConfig) error {
	collector.collectDSN(config.Database.DSN)
	for _, argument := range config.JavaVuser.AdditionalVMParameters {
		if !javarunner.ContainsSensitiveVMParameterValue(argument) {
			continue
		}
		// Preserve both forms in the matcher. Runtime and runner diagnostics
		// may report either the complete JVM token or only its assigned value.
		collector.add(argument)
		if _, value, found := strings.Cut(argument, "="); found {
			collector.add(value)
			collector.collectURL(value, nil)
		}
	}

	payload, err := json.Marshal(config)
	if err != nil {
		return errors.New("runtime config could not be inspected")
	}
	var tree any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&tree); err != nil {
		return errors.New("runtime config could not be inspected")
	}
	collector.collectSensitiveTree(tree, "", false)
	return nil
}

func (collector *importedCredentialCollector) collectScriptConfigTarget(
	target importedCredentialTarget,
) error {
	if collector.script == nil {
		return errors.New("retained credential config locator has no executable script")
	}
	field := strings.ToLower(strings.TrimSpace(target.Field))
	switch {
	case importedBaseURLTargetField(field):
		return collector.collectURLTarget(collector.script.Config.BaseURL, target)
	case strings.Contains(field, "config.http.proxy.url"):
		return collector.collectURLTarget(collector.script.Config.HTTP.Proxy.URL, target)
	case strings.Contains(field, "config.database.dsn"):
		if strings.TrimSpace(collector.script.Config.Database.DSN) == "" {
			return errors.New("retained database DSN locator no longer matches executable config")
		}
		collector.collectDSN(collector.script.Config.Database.DSN)
		return nil
	case strings.Contains(field, "config.java_vuser.additional_vm_parameters"):
		parameters := collector.script.Config.JavaVuser.AdditionalVMParameters
		if target.Index < 0 || target.Index >= len(parameters) {
			return errors.New("retained JVM credential locator no longer matches executable config")
		}
		argument := parameters[target.Index]
		if strings.TrimSpace(argument) == "" {
			return errors.New("retained JVM credential locator no longer matches executable config")
		}
		collector.add(argument)
		if _, value, found := strings.Cut(argument, "="); found {
			collector.add(value)
			collector.collectURL(value, nil)
		}
		return nil
	case strings.Contains(field, "metadata.import.preserved.recorded_resource_urls["):
		index, ok := importedCredentialBracketIndex(
			field,
			"metadata.import.preserved.recorded_resource_urls[",
		)
		if !ok {
			return errors.New("retained resource URL locator is invalid")
		}
		value, ok := importedCredentialStringAt(
			collector.script.Metadata.Import.Preserved["recorded_resource_urls"],
			index,
		)
		if !ok {
			return errors.New("retained resource URL locator no longer matches executable metadata")
		}
		return collector.collectURLTarget(value, target)
	default:
		return errors.New("retained credential locator names an unsupported executable config field")
	}
}

// ProjectScriptCredentials emits config.base_url locators, while the shared
// EffectiveRequest finalizer historically emitted base_url locators before the
// value was assigned to dsl.Script.Config. Both forms describe the same
// executable surface and remain durable compatibility contracts.
func importedBaseURLTargetField(field string) bool {
	field = strings.ToLower(strings.TrimSpace(field))
	return field == "base_url" ||
		strings.HasPrefix(field, "base_url.") ||
		field == "config.base_url" ||
		strings.HasPrefix(field, "config.base_url.")
}

func importedCredentialBracketIndex(field, prefix string) (int, bool) {
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

func importedCredentialStringAt(raw any, index int) (string, bool) {
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

// collectDSN protects both the complete connection composite and protected
// components (passwords, tokens, and sensitive options) that database drivers
// may echo independently in errors. A DSN is itself a credential-governed
// field, so even neutral query option names are included.
func (collector *importedCredentialCollector) collectDSN(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	// collector.add records any template references and deliberately omits the
	// unresolved composite itself. Continue parsing it: a DSN can mix a
	// governed template with a literal sibling password or token, and those
	// literal components still need to enter the runtime matcher.
	collector.add(raw)
	if collector.collectJDBCReviewEnvelope(raw) {
		return
	}
	// A protected unresolved Java connection stores its exact call expression
	// in the DSN slot of the inert envelope. Recover embedded URLs and quoted
	// literals for disclosure equivalence; this source is never added to the
	// runtime matcher.
	collector.collectEmbeddedURL(raw)
	collector.collectQuotedSourceLiterals(raw)
	candidate := raw
	if strings.HasPrefix(strings.ToLower(candidate), "jdbc:") {
		candidate = candidate[len("jdbc:"):]
	}
	if parsed, err := url.Parse(candidate); err == nil {
		if parsed.User != nil {
			collector.collectURLUserinfo(parsed.User)
		}
		for _, values := range parsed.Query() {
			for _, value := range values {
				collector.add(value)
			}
		}
	}
	for _, segment := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ';' || r == '\r' || r == '\n' || r == '\t' || r == ' '
	}) {
		name, value, found := strings.Cut(segment, "=")
		if !found || !credential.SensitiveName(strings.TrimLeft(name, "-/")) {
			continue
		}
		collector.add(strings.Trim(value, `"'`))
	}
}

func (collector *importedCredentialCollector) collectJDBCReviewEnvelope(raw string) bool {
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return false
	}
	var envelope struct {
		JDBCURL  string `json:"jdbc_url"`
		Username string `json:"username,omitempty"`
		Password string `json:"password,omitempty"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		strings.TrimSpace(envelope.JDBCURL) == "" {
		return false
	}
	collector.collectDSN(envelope.JDBCURL)
	collector.add(envelope.Password)
	if envelope.Username != "" && envelope.Password != "" {
		collector.add(envelope.Username + ":" + envelope.Password)
	}
	return true
}

func (collector *importedCredentialCollector) collectQuotedSourceLiterals(raw string) {
	for index := 0; index < len(raw); index++ {
		quote := raw[index]
		if quote != '"' && quote != '\'' {
			continue
		}
		start := index
		escaped := false
		for index++; index < len(raw); index++ {
			switch {
			case escaped:
				escaped = false
			case raw[index] == '\\':
				escaped = true
			case raw[index] == quote:
				literal := raw[start : index+1]
				collector.add(literal[1 : len(literal)-1])
				if decoded, err := strconv.Unquote(literal); err == nil {
					collector.add(decoded)
				}
				goto nextLiteral
			}
		}
		return
	nextLiteral:
	}
}

func (collector *importedCredentialCollector) collectSensitiveTree(value any, key string, headerMap bool) {
	switch typed := value.(type) {
	case map[string]any:
		nextHeaderMap := headerMap || strings.Contains(strings.ToLower(key), "header")
		for childKey, child := range typed {
			sensitive := credential.SensitiveName(childKey) ||
				(nextHeaderMap && credential.SensitiveHeader(childKey))
			if scalar, ok := child.(string); ok && sensitive {
				collector.add(scalar)
				continue
			}
			collector.collectSensitiveTree(child, childKey, nextHeaderMap)
		}
	case []any:
		for _, child := range typed {
			collector.collectSensitiveTree(child, key, headerMap)
		}
	}
}

func (collector *importedCredentialCollector) collectSurfaceKnownCredentials(ref dslwalk.SurfaceRef) {
	if ref.Action != nil {
		action := ref.Action
		collector.collectMarkedBrowserActionValues(action)
		collector.collectURL(action.Target, nil)
		for _, header := range action.HeaderList {
			if credential.SensitiveHeader(header.Name) {
				collector.collectHeaderCredential(header.Name, header.Value)
			}
		}
		for name, value := range action.Headers {
			if credential.SensitiveHeader(name) {
				collector.collectHeaderCredential(name, value)
			}
		}
		if action.Auth != nil {
			collector.add(action.Auth.Password)
			collector.add(action.Auth.Token)
		}
		collector.collectSensitiveTree(action.Params, "params", false)
		return
	}
	request := ref.Request
	if request == nil {
		return
	}
	if target, _ := request["target"].(string); target != "" {
		collector.collectURL(target, nil)
	}
	collector.collectRequestHeaders(request)
	switch auth := request["auth"].(type) {
	case map[string]any:
		if password, _ := auth["password"].(string); password != "" {
			collector.add(password)
		}
		if token, _ := auth["token"].(string); token != "" {
			collector.add(token)
		}
	case *dsl.AuthConfig:
		if auth != nil {
			collector.add(auth.Password)
			collector.add(auth.Token)
		}
	case dsl.AuthConfig:
		collector.add(auth.Password)
		collector.add(auth.Token)
	}
	if params, ok := request["params"]; ok {
		collector.collectSensitiveTree(params, "params", false)
	}
}

func (collector *importedCredentialCollector) collectMarkedBrowserActionValues(action *dsl.Action) {
	if action == nil || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(action.Type)), "browser_") {
		return
	}
	marked, _ := action.Metadata["source_marked_secret"].(bool)
	masked, _ := action.Metadata["source_masked_secret"].(bool)
	if !marked && !masked {
		return
	}

	collector.add(action.Value)
	switch values := action.Params["values"].(type) {
	case []string:
		for _, value := range values {
			collector.add(value)
		}
	case []any:
		for _, raw := range values {
			if value, ok := raw.(string); ok {
				collector.add(value)
			}
		}
	}
}

func (collector *importedCredentialCollector) collectRequestHeaders(request map[string]any) {
	switch headers := request["headers"].(type) {
	case map[string]any:
		for name, raw := range headers {
			if value, ok := raw.(string); ok && credential.SensitiveHeader(name) {
				collector.collectHeaderCredential(name, value)
			}
		}
	case map[string]string:
		for name, value := range headers {
			if credential.SensitiveHeader(name) {
				collector.collectHeaderCredential(name, value)
			}
		}
	}
	if list, ok := importedRequestHeaderList(request["header_list"]); ok {
		for _, header := range list {
			name := header.Name
			value := header.Value
			if credential.SensitiveHeader(name) {
				collector.collectHeaderCredential(name, value)
			}
		}
	}
}

func (collector *importedCredentialCollector) collectCookieHeaderValues(name, value string) {
	switch {
	case strings.EqualFold(strings.TrimSpace(name), "Cookie"):
		request := &stdhttp.Request{Header: stdhttp.Header{"Cookie": []string{value}}}
		for _, cookie := range request.Cookies() {
			collector.add(cookie.Value)
		}
	case strings.EqualFold(strings.TrimSpace(name), "Set-Cookie"):
		if cookie, err := stdhttp.ParseSetCookie(value); err == nil {
			collector.add(cookie.Value)
		}
	}
}

func (collector *importedCredentialCollector) collectSurfaceTarget(
	ref dslwalk.SurfaceRef,
	target importedCredentialTarget,
) error {
	field := strings.ToLower(strings.TrimSpace(target.Field))
	switch {
	case strings.Contains(field, "multipart.parts["):
		body, mediaType, err := surfaceBody(ref)
		if err != nil {
			return err
		}
		return collector.collectBodyTarget(body, mediaType, target)
	case strings.Contains(field, "header"):
		value, ok := surfaceHeaderValue(ref, target.Index)
		if !ok {
			return errors.New("retained credential header locator no longer matches the executable request")
		}
		collector.add(value)
		return nil
	case strings.HasSuffix(field, "auth.password"):
		value, ok := surfaceAuthValue(ref, "password")
		if !ok {
			return errors.New("retained credential auth locator no longer matches the executable request")
		}
		collector.add(value)
		return nil
	case strings.HasSuffix(field, "auth.token"):
		value, ok := surfaceAuthValue(ref, "token")
		if !ok {
			return errors.New("retained credential auth locator no longer matches the executable request")
		}
		collector.add(value)
		return nil
	case strings.Contains(field, "params.token_url"):
		value, ok := surfaceParamStringValue(ref, "token_url")
		if !ok {
			return errors.New("retained credential OAuth URL locator no longer matches the executable request")
		}
		return collector.collectURLTarget(value, target)
	case strings.Contains(field, "target") || strings.Contains(field, ".url") ||
		strings.HasPrefix(field, "url."):
		value, ok := surfaceStringValue(ref, "target")
		if !ok {
			return errors.New("retained credential URL locator no longer matches the executable request")
		}
		if strings.HasSuffix(field, ".path") {
			return collector.collectURLPathTarget(
				value,
				target,
				surfaceOpenAPIPath(ref),
			)
		}
		return collector.collectURLTarget(value, target)
	case strings.Contains(field, "body") || strings.Contains(field, "json") ||
		strings.Contains(field, "xml") || strings.Contains(field, "multipart") ||
		strings.Contains(field, "graphql") || strings.Contains(field, "form"):
		body, mediaType, err := surfaceBody(ref)
		if err != nil {
			return err
		}
		return collector.collectBodyTarget(body, mediaType, target)
	default:
		return errors.New("retained credential locator names an unsupported executable field")
	}
}

func surfaceParamStringValue(ref dslwalk.SurfaceRef, key string) (string, bool) {
	if ref.Action != nil {
		if ref.Action.Params == nil {
			return "", false
		}
		value, ok := ref.Action.Params[key].(string)
		return value, ok
	}
	params, ok := ref.Request["params"].(map[string]any)
	if !ok {
		return "", false
	}
	value, ok := params[key].(string)
	return value, ok
}

func surfaceHeaderValue(ref dslwalk.SurfaceRef, index int) (string, bool) {
	if ref.Action != nil {
		if index >= 0 && index < len(ref.Action.HeaderList) {
			return ref.Action.HeaderList[index].Value, true
		}
		return "", false
	}
	list, ok := importedRequestHeaderList(ref.Request["header_list"])
	if !ok || index < 0 || index >= len(list) {
		return "", false
	}
	return list[index].Value, true
}

func surfaceAuthValue(ref dslwalk.SurfaceRef, name string) (string, bool) {
	if ref.Action != nil {
		if ref.Action.Auth == nil {
			return "", false
		}
		switch name {
		case "password":
			return ref.Action.Auth.Password, true
		case "token":
			return ref.Action.Auth.Token, true
		}
	}
	switch auth := ref.Request["auth"].(type) {
	case map[string]any:
		value, ok := auth[name].(string)
		return value, ok
	case *dsl.AuthConfig:
		if auth == nil {
			return "", false
		}
		switch name {
		case "password":
			return auth.Password, true
		case "token":
			return auth.Token, true
		}
	case dsl.AuthConfig:
		switch name {
		case "password":
			return auth.Password, true
		case "token":
			return auth.Token, true
		}
	}
	return "", false
}

func surfaceStringValue(ref dslwalk.SurfaceRef, key string) (string, bool) {
	if ref.Action != nil {
		switch key {
		case "target":
			return ref.Action.Target, true
		case "body":
			return ref.Action.Body, true
		}
	}
	value, ok := ref.Request[key].(string)
	return value, ok
}

func surfaceOpenAPIPath(ref dslwalk.SurfaceRef) string {
	metadata := ref.Metadata()
	openAPI, ok := metadata["openapi"].(map[string]any)
	if !ok {
		return ""
	}
	value, _ := openAPI["path"].(string)
	return value
}

func surfaceBody(ref dslwalk.SurfaceRef) ([]byte, string, error) {
	body, ok := surfaceStringValue(ref, "body")
	if !ok {
		return nil, "", errors.New("retained credential body locator no longer matches the executable request")
	}
	encoding := ""
	if ref.Action != nil {
		encoding = ref.Action.BodyEncoding
	} else {
		encoding, _ = ref.Request["body_encoding"].(string)
	}
	payload := []byte(body)
	if strings.EqualFold(strings.TrimSpace(encoding), "base64") {
		decoded, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			return nil, "", errors.New("retained credential body encoding is invalid")
		}
		payload = decoded
	}
	return payload, surfaceContentType(ref), nil
}

func surfaceContentType(ref dslwalk.SurfaceRef) string {
	if ref.Action != nil {
		for _, header := range ref.Action.HeaderList {
			if strings.EqualFold(header.Name, "Content-Type") {
				return header.Value
			}
		}
		for name, value := range ref.Action.Headers {
			if strings.EqualFold(name, "Content-Type") {
				return value
			}
		}
		return ""
	}
	switch headers := ref.Request["headers"].(type) {
	case map[string]any:
		for name, raw := range headers {
			if strings.EqualFold(name, "Content-Type") {
				value, _ := raw.(string)
				return value
			}
		}
	case map[string]string:
		for name, value := range headers {
			if strings.EqualFold(name, "Content-Type") {
				return value
			}
		}
	}
	if list, ok := importedRequestHeaderList(ref.Request["header_list"]); ok {
		for _, header := range list {
			if strings.EqualFold(header.Name, "Content-Type") {
				return header.Value
			}
		}
	}
	return ""
}

type legacyCredentialBodyBudget struct {
	values int
	bytes  int
}

func (budget *legacyCredentialBodyBudget) consumeValue() error {
	budget.values++
	if budget.values > maxLegacyCredentialBodyValues {
		return errors.New("legacy credential body exceeds the structured value limit")
	}
	return nil
}

func (budget *legacyCredentialBodyBudget) consumeBytes(count int) error {
	if count < 0 || count > maxLegacyCredentialBodyScanBytes-budget.bytes {
		return errors.New("legacy credential body exceeds the scan byte limit")
	}
	budget.bytes += count
	return nil
}

func (collector *importedCredentialCollector) collectLegacySurfaceBodyCredentials(
	ref dslwalk.SurfaceRef,
) error {
	if ref.Action != nil {
		if strings.TrimSpace(ref.Action.Method) == "" || ref.Action.Body == "" {
			return nil
		}
	} else {
		method, _ := ref.Request["method"].(string)
		body, _ := ref.Request["body"].(string)
		if strings.TrimSpace(method) == "" || body == "" {
			return nil
		}
	}
	payload, mediaType, err := surfaceBody(ref)
	if err != nil {
		return err
	}
	return collector.collectLegacyRequestBodyCredentials(
		payload,
		mediaType,
		0,
		&legacyCredentialBodyBudget{},
	)
}

func (collector *importedCredentialCollector) collectLegacyRequestBodyCredentials(
	payload []byte,
	mediaType string,
	multipartDepth int,
	budget *legacyCredentialBodyBudget,
) error {
	if len(payload) == 0 {
		return nil
	}
	if int64(len(payload)) > dsl.MaxBodyRefBytes {
		return errors.New("legacy credential body exceeds the runtime body limit")
	}
	if budget == nil {
		budget = &legacyCredentialBodyBudget{}
	}
	if err := budget.consumeBytes(len(payload)); err != nil {
		return err
	}

	baseMediaType, params, mediaTypeErr := mime.ParseMediaType(mediaType)
	baseMediaType = strings.ToLower(strings.TrimSpace(baseMediaType))
	if mediaTypeErr != nil {
		baseMediaType = strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0]))
	}
	declaredMediaType := baseMediaType != ""
	trimmed := bytes.TrimSpace(payload)
	scanResult := func(err error) error {
		if err == nil || legacyBodyHasSensitiveNameHint(payload) {
			return err
		}
		// A historical load test may intentionally send malformed structured
		// data. Parsing failure is security-relevant only when the bounded
		// lexical pass sees a credential-governed name that could hide a value.
		return nil
	}
	switch {
	case strings.Contains(baseMediaType, "json"):
		return scanResult(collector.collectLegacyJSONBodyCredentials(payload, budget))
	case strings.Contains(baseMediaType, "xml"):
		return scanResult(collector.collectLegacyXMLBodyCredentials(payload, budget))
	case baseMediaType == "application/x-www-form-urlencoded":
		return scanResult(collector.collectLegacyFormBodyCredentials(payload, budget))
	case strings.HasPrefix(baseMediaType, "multipart/"):
		if multipartDepth >= maxLegacyCredentialMultipartDepth {
			return scanResult(errors.New("legacy credential multipart body exceeds the nesting limit"))
		}
		return scanResult(collector.collectLegacyMultipartBodyCredentials(
			payload,
			params["boundary"],
			multipartDepth,
			budget,
		))
	case baseMediaType == "application/graphql":
		_, err := collector.collectLegacyGraphQLCredentialLiterals(string(payload), budget)
		return scanResult(err)
	}
	if declaredMediaType {
		// An explicit opaque/binary media type is authoritative. Do not
		// reinterpret file bytes that merely resemble JSON, XML, or form data.
		return nil
	}

	// Historical importers did not always retain Content-Type. Sniff only
	// structured request bodies. Opaque text remains untouched and is not
	// confused with review/source material.
	switch {
	case len(trimmed) > 0 && trimmed[0] == '<':
		return scanResult(collector.collectLegacyXMLBodyCredentials(payload, budget))
	case len(trimmed) > 0 && (trimmed[0] == '[' || trimmed[0] == '{'):
		if json.Valid(trimmed) || looksLikeLegacyJSONBody(trimmed) {
			return scanResult(collector.collectLegacyJSONBodyCredentials(payload, budget))
		}
		_, err := collector.collectLegacyGraphQLCredentialLiterals(string(payload), budget)
		return scanResult(err)
	case looksLikeLegacyGraphQLBody(trimmed):
		_, err := collector.collectLegacyGraphQLCredentialLiterals(string(payload), budget)
		return scanResult(err)
	case legacyFormBodyHasSensitiveName(trimmed):
		return scanResult(collector.collectLegacyFormBodyCredentials(payload, budget))
	default:
		return nil
	}
}

func legacyBodyHasSensitiveNameHint(payload []byte) bool {
	check := func(candidate string) bool {
		return credential.SensitiveName(candidate) ||
			credential.SensitiveXMLName(candidate)
	}
	for start := 0; start < len(payload); {
		for start < len(payload) && !legacyCredentialNameByte(payload[start]) {
			start++
		}
		end := start
		for end < len(payload) && legacyCredentialNameByte(payload[end]) {
			end++
		}
		if end > start && check(string(payload[start:end])) {
			return true
		}
		start = max(end, start+1)
	}
	for index := 0; index < len(payload); index++ {
		if payload[index] != '"' {
			continue
		}
		start := index
		for index++; index < len(payload); index++ {
			if payload[index] == '\\' {
				index++
				continue
			}
			if payload[index] != '"' {
				continue
			}
			var decoded string
			if json.Unmarshal(payload[start:index+1], &decoded) == nil && check(decoded) {
				return true
			}
			break
		}
	}
	return false
}

func legacyCredentialNameByte(value byte) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		strings.ContainsRune("_-.[]:%", rune(value))
}

func legacyFormBodyHasSensitiveName(payload []byte) bool {
	for _, pair := range bytes.Split(payload, []byte("&")) {
		rawName, _, found := bytes.Cut(pair, []byte("="))
		if !found {
			continue
		}
		name, err := url.QueryUnescape(string(rawName))
		if err == nil && credential.SensitiveName(name) {
			return true
		}
	}
	return false
}

func looksLikeLegacyJSONBody(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	if payload[0] == '[' {
		return true
	}
	if payload[0] != '{' {
		return false
	}
	remainder := bytes.TrimSpace(payload[1:])
	return len(remainder) > 0 && (remainder[0] == '"' || remainder[0] == '}')
}

func looksLikeLegacyGraphQLBody(payload []byte) bool {
	lower := strings.ToLower(strings.TrimSpace(string(payload)))
	return strings.HasPrefix(lower, "query ") ||
		strings.HasPrefix(lower, "mutation ") ||
		strings.HasPrefix(lower, "subscription ")
}

type legacyJSONCredentialScan struct {
	collector       *importedCredentialCollector
	budget          *legacyCredentialBodyBudget
	queries         []string
	variableScalars map[string][]string
}

func (collector *importedCredentialCollector) collectLegacyJSONBodyCredentials(
	payload []byte,
	budget *legacyCredentialBodyBudget,
) error {
	scan := legacyJSONCredentialScan{
		collector:       collector,
		budget:          budget,
		variableScalars: make(map[string][]string),
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := scan.walk(decoder, 0, false, "", ""); err != nil {
		return errors.New("legacy credential JSON body is malformed or exceeds runtime limits")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("legacy credential JSON body has trailing data")
	}
	for _, query := range scan.queries {
		references, err := collector.collectLegacyGraphQLCredentialLiterals(query, budget)
		if err != nil {
			return err
		}
		for name := range references {
			for _, value := range scan.variableScalars[name] {
				collector.add(value)
			}
		}
	}
	return nil
}

func (scan *legacyJSONCredentialScan) walk(
	decoder *json.Decoder,
	depth int,
	sensitive bool,
	topField string,
	variable string,
) error {
	if depth > maxLegacyCredentialBodyDepth {
		return errors.New("JSON nesting limit exceeded")
	}
	if err := scan.budget.consumeValue(); err != nil {
		return err
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); ok {
		switch delimiter {
		case '{':
			for decoder.More() {
				if err := scan.budget.consumeValue(); err != nil {
					return err
				}
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("JSON object key is invalid")
				}
				childTopField := topField
				if depth == 0 {
					childTopField = key
				}
				childVariable := variable
				if depth == 1 && strings.EqualFold(topField, "variables") {
					childVariable = key
				}
				if err := scan.walk(
					decoder,
					depth+1,
					sensitive || credential.SensitiveName(key),
					childTopField,
					childVariable,
				); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return errors.New("JSON object is not terminated")
			}
			return nil
		case '[':
			for decoder.More() {
				if err := scan.walk(
					decoder,
					depth+1,
					sensitive,
					topField,
					variable,
				); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return errors.New("JSON array is not terminated")
			}
			return nil
		default:
			return errors.New("JSON delimiter is invalid")
		}
	}

	value, hasValue := legacyCredentialScalarText(token)
	if !hasValue {
		return nil
	}
	if sensitive {
		scan.collector.add(value)
	}
	if variable != "" {
		scan.variableScalars[variable] = append(scan.variableScalars[variable], value)
	}
	if depth == 1 && strings.EqualFold(topField, "query") {
		if query, ok := token.(string); ok {
			scan.queries = append(scan.queries, query)
		}
	}
	return nil
}

func legacyCredentialScalarText(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case json.Number:
		return typed.String(), true
	case bool:
		return strconv.FormatBool(typed), true
	default:
		return "", false
	}
}

func (collector *importedCredentialCollector) collectLegacyXMLBodyCredentials(
	payload []byte,
	budget *legacyCredentialBodyBudget,
) error {
	type elementFrame struct {
		sensitive bool
		text      strings.Builder
	}
	decoder := xml.NewDecoder(bytes.NewReader(payload))
	stack := make([]elementFrame, 0, 16)
	for {
		if err := budget.consumeValue(); err != nil {
			return err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("legacy credential XML body is malformed")
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if len(stack) >= maxLegacyCredentialBodyDepth {
				return errors.New("legacy credential XML body exceeds the nesting limit")
			}
			sensitive := credential.SensitiveXMLName(typed.Name.Local)
			if len(stack) > 0 && stack[len(stack)-1].sensitive {
				sensitive = true
			}
			for _, attr := range typed.Attr {
				if sensitive || credential.SensitiveXMLName(attr.Name.Local) {
					collector.add(attr.Value)
				}
			}
			stack = append(stack, elementFrame{sensitive: sensitive})
		case xml.CharData:
			for index := range stack {
				if stack[index].sensitive {
					if err := budget.consumeBytes(len(typed)); err != nil {
						return err
					}
					stack[index].text.Write([]byte(typed))
				}
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return errors.New("legacy credential XML body is malformed")
			}
			frame := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if frame.sensitive {
				collector.add(strings.TrimSpace(frame.text.String()))
			}
		}
	}
	if len(stack) != 0 {
		return errors.New("legacy credential XML body is malformed")
	}
	return nil
}

func (collector *importedCredentialCollector) collectLegacyFormBodyCredentials(
	payload []byte,
	budget *legacyCredentialBodyBudget,
) error {
	pairs := bytes.Split(payload, []byte("&"))
	for _, pair := range pairs {
		if err := budget.consumeValue(); err != nil {
			return err
		}
		rawName, rawValue, _ := bytes.Cut(pair, []byte("="))
		name, nameErr := url.QueryUnescape(string(rawName))
		value, valueErr := url.QueryUnescape(string(rawValue))
		if nameErr != nil || valueErr != nil {
			return errors.New("legacy credential form body has invalid encoding")
		}
		if credential.SensitiveName(name) {
			collector.add(value)
		}
	}
	return nil
}

func (collector *importedCredentialCollector) collectLegacyMultipartBodyCredentials(
	payload []byte,
	boundary string,
	multipartDepth int,
	budget *legacyCredentialBodyBudget,
) error {
	if strings.TrimSpace(boundary) == "" {
		return errors.New("legacy credential multipart boundary is unavailable")
	}
	parts, err := parseImportedMultipartWire(payload, boundary)
	if err != nil {
		return errors.New("legacy credential multipart body is malformed")
	}
	for _, part := range parts {
		if err := budget.consumeValue(); err != nil {
			return err
		}
		sensitivePart := false
		filePart := false
		for _, header := range part.headers {
			if credential.SensitiveHeader(header.name) {
				collector.collectHeaderCredential(header.name, header.value)
			}
			if !strings.EqualFold(header.name, "Content-Disposition") {
				continue
			}
			_, params, parseErr := mime.ParseMediaType(header.value)
			if parseErr != nil {
				return errors.New("legacy credential multipart disposition is malformed")
			}
			sensitivePart = credential.SensitiveName(params["name"])
			filePart = strings.TrimSpace(params["filename"]) != ""
		}
		if sensitivePart {
			collector.add(string(part.body))
		}
		if !filePart && (part.contentType != "" || !sensitivePart) {
			if err := collector.collectLegacyRequestBodyCredentials(
				part.body,
				part.contentType,
				multipartDepth+1,
				budget,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

type legacyGraphQLToken struct {
	kind byte
	text string
}

const (
	legacyGraphQLNameToken   = 'n'
	legacyGraphQLStringToken = 's'
	legacyGraphQLBlockToken  = 'b'
	legacyGraphQLNumberToken = '#'
	legacyGraphQLPunctToken  = 'p'
)

func (collector *importedCredentialCollector) collectLegacyGraphQLCredentialLiterals(
	query string,
	budget *legacyCredentialBodyBudget,
) (map[string]struct{}, error) {
	tokens, err := lexLegacyGraphQL(query, budget)
	if err != nil {
		return nil, errors.New("legacy credential GraphQL body is malformed or exceeds runtime limits")
	}
	references := make(map[string]struct{})
	eligible := legacyGraphQLCredentialContexts(tokens)
	for index := 0; index+1 < len(tokens); index++ {
		if tokens[index].kind != legacyGraphQLNameToken ||
			!credential.SensitiveName(tokens[index].text) ||
			!eligible[index] ||
			tokens[index+1].text != ":" {
			continue
		}
		valueIndex := index + 2
		if index > 0 && tokens[index-1].text == "$" {
			// A sensitive variable definition governs only its authored default.
			// Runtime values are collected where the variable is used by a
			// sensitive argument below.
			foundDefault := false
			for valueIndex < len(tokens) {
				switch tokens[valueIndex].text {
				case "=":
					valueIndex++
					foundDefault = true
				case ",", ")":
					valueIndex = len(tokens)
				}
				if foundDefault || valueIndex >= len(tokens) {
					break
				}
				valueIndex++
			}
			if !foundDefault {
				continue
			}
		}
		if valueIndex >= len(tokens) {
			return nil, errors.New("legacy credential GraphQL value is missing")
		}
		if _, err := collector.collectLegacyGraphQLValue(
			tokens,
			valueIndex,
			references,
			0,
		); err != nil {
			return nil, err
		}
	}
	return references, nil
}

func legacyGraphQLCredentialContexts(tokens []legacyGraphQLToken) []bool {
	eligible := make([]bool, len(tokens))
	parenDepth := 0
	inputBraces := make([]bool, 0, 8)
	for index, token := range tokens {
		inInputObject := len(inputBraces) > 0 && inputBraces[len(inputBraces)-1]
		eligible[index] = parenDepth > 0 || inInputObject
		switch token.text {
		case "(":
			parenDepth++
		case ")":
			if parenDepth > 0 {
				parenDepth--
			}
		case "{":
			inputBraces = append(inputBraces, parenDepth > 0 || inInputObject)
		case "}":
			if len(inputBraces) > 0 {
				inputBraces = inputBraces[:len(inputBraces)-1]
			}
		}
	}
	return eligible
}

func (collector *importedCredentialCollector) collectLegacyGraphQLValue(
	tokens []legacyGraphQLToken,
	index int,
	references map[string]struct{},
	depth int,
) (int, error) {
	if depth > maxLegacyCredentialBodyDepth || index >= len(tokens) {
		return index, errors.New("legacy credential GraphQL value exceeds runtime limits")
	}
	token := tokens[index]
	switch token.kind {
	case legacyGraphQLStringToken:
		collector.add(token.text)
		return index + 1, nil
	case legacyGraphQLBlockToken:
		// GraphQL block strings apply indentation normalization that cannot be
		// recovered safely from a token fragment alone. Refuse execution rather
		// than install an incomplete exact-value matcher.
		return index, errors.New("legacy sensitive GraphQL block string requires review")
	case legacyGraphQLNumberToken:
		collector.add(token.text)
		return index + 1, nil
	case legacyGraphQLNameToken:
		if !strings.EqualFold(token.text, "null") {
			collector.add(token.text)
		}
		return index + 1, nil
	}
	switch token.text {
	case "$":
		if index+1 >= len(tokens) || tokens[index+1].kind != legacyGraphQLNameToken {
			return index, errors.New("legacy credential GraphQL variable is malformed")
		}
		references[tokens[index+1].text] = struct{}{}
		return index + 2, nil
	case "[":
		cursor := index + 1
		for cursor < len(tokens) && tokens[cursor].text != "]" {
			if tokens[cursor].text == "," {
				cursor++
				continue
			}
			next, err := collector.collectLegacyGraphQLValue(
				tokens,
				cursor,
				references,
				depth+1,
			)
			if err != nil {
				return cursor, err
			}
			cursor = next
		}
		if cursor >= len(tokens) {
			return cursor, errors.New("legacy credential GraphQL list is not terminated")
		}
		return cursor + 1, nil
	case "{":
		cursor := index + 1
		for cursor < len(tokens) && tokens[cursor].text != "}" {
			if tokens[cursor].text == "," {
				cursor++
				continue
			}
			if tokens[cursor].kind != legacyGraphQLNameToken ||
				cursor+1 >= len(tokens) || tokens[cursor+1].text != ":" {
				return cursor, errors.New("legacy credential GraphQL input object is malformed")
			}
			next, err := collector.collectLegacyGraphQLValue(
				tokens,
				cursor+2,
				references,
				depth+1,
			)
			if err != nil {
				return cursor, err
			}
			cursor = next
		}
		if cursor >= len(tokens) {
			return cursor, errors.New("legacy credential GraphQL input object is not terminated")
		}
		return cursor + 1, nil
	default:
		return index, errors.New("legacy credential GraphQL value is unsupported")
	}
}

func lexLegacyGraphQL(
	query string,
	budget *legacyCredentialBodyBudget,
) ([]legacyGraphQLToken, error) {
	tokens := make([]legacyGraphQLToken, 0, min(len(query)/4, 1024))
	appendToken := func(kind byte, text string) error {
		if err := budget.consumeValue(); err != nil {
			return err
		}
		tokens = append(tokens, legacyGraphQLToken{kind: kind, text: text})
		return nil
	}
	for index := 0; index < len(query); {
		character := query[index]
		switch {
		case character == ' ' || character == '\t' ||
			character == '\r' || character == '\n':
			index++
		case character == '#':
			for index < len(query) && query[index] != '\r' && query[index] != '\n' {
				index++
			}
		case character == '"':
			if index+3 <= len(query) && query[index:index+3] == `"""` {
				end := strings.Index(query[index+3:], `"""`)
				if end < 0 {
					return nil, errors.New("GraphQL block string is not terminated")
				}
				end += index + 3
				if err := appendToken(
					legacyGraphQLBlockToken,
					query[index+3:end],
				); err != nil {
					return nil, err
				}
				index = end + 3
				continue
			}
			start := index
			index++
			for index < len(query) {
				switch query[index] {
				case '\\':
					index += 2
				case '"':
					index++
					var decoded string
					if err := json.Unmarshal([]byte(query[start:index]), &decoded); err != nil {
						return nil, err
					}
					if err := appendToken(legacyGraphQLStringToken, decoded); err != nil {
						return nil, err
					}
					start = -1
				default:
					if query[index] == '\r' || query[index] == '\n' {
						return nil, errors.New("GraphQL string contains a line break")
					}
					index++
				}
				if start == -1 {
					break
				}
			}
			if start != -1 {
				return nil, errors.New("GraphQL string is not terminated")
			}
		case isLegacyGraphQLNameStart(character):
			start := index
			index++
			for index < len(query) && isLegacyGraphQLNameContinue(query[index]) {
				index++
			}
			if err := appendToken(legacyGraphQLNameToken, query[start:index]); err != nil {
				return nil, err
			}
		case character == '-' || character >= '0' && character <= '9':
			start := index
			index++
			for index < len(query) && (query[index] >= '0' && query[index] <= '9' ||
				query[index] == '.' || query[index] == 'e' || query[index] == 'E' ||
				query[index] == '+' || query[index] == '-') {
				index++
			}
			if err := appendToken(legacyGraphQLNumberToken, query[start:index]); err != nil {
				return nil, err
			}
		case strings.ContainsRune("!$&():=@[]{|},.", rune(character)):
			if err := appendToken(legacyGraphQLPunctToken, string(character)); err != nil {
				return nil, err
			}
			index++
		default:
			return nil, errors.New("GraphQL token is invalid")
		}
	}
	return tokens, nil
}

func isLegacyGraphQLNameStart(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isLegacyGraphQLNameContinue(value byte) bool {
	return isLegacyGraphQLNameStart(value) || value >= '0' && value <= '9'
}

// In-memory converters use typed HeaderList values while JSON-loaded scripts
// use []any. Sequence preflight and disclosure must accept both
// representations; otherwise a valid server-produced credential locator can
// fail closed before the script has crossed a serialization boundary.
func importedRequestHeaderList(raw any) (dsl.HeaderList, bool) {
	switch list := raw.(type) {
	case dsl.HeaderList:
		return list, true
	case []dsl.HeaderEntry:
		return dsl.HeaderList(list), true
	case []map[string]any:
		out := make(dsl.HeaderList, len(list))
		for index, header := range list {
			name, nameOK := header["name"].(string)
			value, valueOK := header["value"].(string)
			if !nameOK || !valueOK {
				return nil, false
			}
			enabled, enabledOK := header["enabled"].(bool)
			if _, exists := header["enabled"]; exists && !enabledOK {
				return nil, false
			} else if exists {
				out[index] = dsl.HeaderEntry{Name: name, Value: value, Enabled: &enabled}
			} else {
				out[index] = dsl.HeaderEntry{Name: name, Value: value}
			}
		}
		return out, true
	case []any:
		out := make(dsl.HeaderList, len(list))
		for index, rawHeader := range list {
			switch header := rawHeader.(type) {
			case map[string]any:
				name, nameOK := header["name"].(string)
				value, valueOK := header["value"].(string)
				if !nameOK || !valueOK {
					return nil, false
				}
				enabled, enabledOK := header["enabled"].(bool)
				if _, exists := header["enabled"]; exists && !enabledOK {
					return nil, false
				} else if exists {
					out[index] = dsl.HeaderEntry{Name: name, Value: value, Enabled: &enabled}
				} else {
					out[index] = dsl.HeaderEntry{Name: name, Value: value}
				}
			case dsl.HeaderEntry:
				out[index] = header
			default:
				return nil, false
			}
		}
		return out, true
	default:
		return nil, false
	}
}

func (collector *importedCredentialCollector) collectURL(raw string, target *importedCredentialTarget) {
	if raw == "" {
		return
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return
	}
	if parsed.User != nil {
		collector.collectURLUserinfo(parsed.User)
	}
	queryStart := strings.IndexByte(raw, '?')
	if queryStart < 0 {
		return
	}
	queryEnd := len(raw)
	if fragment := strings.IndexByte(raw[queryStart+1:], '#'); fragment >= 0 {
		queryEnd = queryStart + 1 + fragment
	}
	for index, pair := range strings.Split(raw[queryStart+1:queryEnd], "&") {
		name, value, _ := strings.Cut(pair, "=")
		decodedName, nameErr := url.QueryUnescape(name)
		decodedValue, valueErr := url.QueryUnescape(value)
		if nameErr != nil || valueErr != nil {
			continue
		}
		if credential.SensitiveName(decodedName) ||
			(target != nil && index == target.Index) {
			collector.add(decodedValue)
		}
	}
}

func (collector *importedCredentialCollector) collectURLTarget(
	raw string,
	target importedCredentialTarget,
) error {
	field := strings.ToLower(target.Field)
	switch {
	case strings.HasSuffix(field, ".path"):
		return collector.collectURLPathTarget(raw, target, "")
	case strings.HasSuffix(field, ".query"):
		queryStart := strings.IndexByte(raw, '?')
		if queryStart < 0 {
			return errors.New("retained credential query locator no longer matches the executable request")
		}
		queryEnd := len(raw)
		if fragment := strings.IndexByte(raw[queryStart+1:], '#'); fragment >= 0 {
			queryEnd = queryStart + 1 + fragment
		}
		pairs := strings.Split(raw[queryStart+1:queryEnd], "&")
		if target.Index < 0 || target.Index >= len(pairs) {
			return errors.New("retained credential query locator no longer matches the executable request")
		}
		_, rawValue, _ := strings.Cut(pairs[target.Index], "=")
		value, err := url.QueryUnescape(rawValue)
		if err != nil {
			return errors.New("retained credential query encoding is invalid")
		}
		collector.add(value)
		return nil
	case strings.HasSuffix(field, ".userinfo"):
		parsed, err := url.Parse(raw)
		if err != nil || parsed.User == nil {
			return errors.New("retained credential userinfo locator no longer matches the executable request")
		}
		collector.collectURLUserinfo(parsed.User)
		return nil
	case strings.HasSuffix(field, ".fragment"):
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Fragment == "" {
			return errors.New("retained credential fragment locator no longer matches the executable request")
		}
		collector.add(parsed.Fragment)
		return nil
	default:
		collector.collectURL(raw, &target)
		return nil
	}
}

// collectURLUserinfo protects the password and the complete userinfo composite
// without treating an ordinary username as a secret everywhere it appears. A
// bare username is a stable identifier in many imported scripts and adding it
// to the exact-value matcher corrupts paths, action names, and metric labels.
func (collector *importedCredentialCollector) collectURLUserinfo(user *url.Userinfo) {
	if collector == nil || user == nil {
		return
	}
	password, ok := user.Password()
	if !ok {
		return
	}
	username := user.Username()
	collector.add(password)
	collector.add(username + ":" + password)
	collector.add(user.String())
}

func (collector *importedCredentialCollector) collectURLPathTarget(
	raw string,
	target importedCredentialTarget,
	sourcePath string,
) error {
	pointer, recognizedPointer, pointerErr :=
		decodeImportedOpenAPIPathCredentialPointer(target.Pointer)
	if recognizedPointer && pointerErr != nil {
		return errors.New("retained credential path locator is malformed")
	}
	if recognizedPointer && collector.resolved && strings.TrimSpace(sourcePath) == "" {
		// The immutable DSL locator was already validated and its exact literal
		// values were installed in the base matcher before action resolution.
		// Other path parameters can change byte length during interpolation, so
		// the source byte spans cannot be re-applied to the resolved segment.
		// The governed OpenAPI credential itself is an authored literal and is
		// therefore unchanged on the wire.
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("retained credential path locator no longer matches the executable request")
	}
	escapedPath := parsed.EscapedPath()
	if !parsed.IsAbs() && parsed.Host == "" {
		escapedPath = raw
		if end := strings.IndexAny(escapedPath, "?#"); end >= 0 {
			escapedPath = escapedPath[:end]
		}
	}
	segments := strings.Split(escapedPath, "/")
	if target.Index < 0 || target.Index >= len(segments) ||
		segments[target.Index] == "" {
		return errors.New("retained credential path locator no longer matches the executable request")
	}
	collector.add(segments[target.Index])
	decoded, err := url.PathUnescape(segments[target.Index])
	if err != nil {
		return errors.New("retained credential path encoding is invalid")
	}
	collector.add(decoded)
	if recognizedPointer {
		rawValues, valueErr := importedOpenAPIPathCredentialValues(
			segments[target.Index],
			pointer,
		)
		if valueErr != nil {
			return valueErr
		}
		for _, rawValue := range rawValues {
			collector.add(rawValue)
			decodedValue, decodeErr := url.PathUnescape(rawValue)
			if decodeErr != nil {
				return errors.New("retained credential path encoding is invalid")
			}
			collector.add(decodedValue)
		}
		return nil
	}
	if strings.TrimSpace(sourcePath) == "" || strings.TrimSpace(target.Pointer) == "" {
		return nil
	}
	sourceSegments := strings.Split(sourcePath, "/")
	if target.Index >= len(sourceSegments) {
		return errors.New("retained credential path template no longer matches the executable request")
	}
	if values, matched := credentialValuesFromComposedPathSegment(
		sourceSegments[target.Index],
		target.Pointer,
		segments[target.Index],
	); matched {
		for _, value := range values {
			collector.add(value)
		}
	}
	sourceSegment, err := url.PathUnescape(sourceSegments[target.Index])
	if err != nil {
		return errors.New("retained credential source path encoding is invalid")
	}
	values, ok := credentialValuesFromComposedPathSegment(
		sourceSegment,
		target.Pointer,
		decoded,
	)
	if !ok {
		return errors.New("retained credential path template no longer matches the executable request")
	}
	for _, value := range values {
		collector.add(value)
	}
	return nil
}

func decodeImportedOpenAPIPathCredentialPointer(
	raw string,
) (importedOpenAPIPathCredentialPointer, bool, error) {
	if !strings.HasPrefix(raw, importedOpenAPIPathCredentialPointerPrefix) {
		return importedOpenAPIPathCredentialPointer{}, false, nil
	}
	if len(raw) > 4096 {
		return importedOpenAPIPathCredentialPointer{}, true, errors.New("pointer is too large")
	}
	parts := strings.Split(
		strings.TrimPrefix(raw, importedOpenAPIPathCredentialPointerPrefix),
		":",
	)
	if len(parts) != 5 {
		return importedOpenAPIPathCredentialPointer{}, true, errors.New("pointer field count is invalid")
	}
	parameterBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(parameterBytes) == 0 || len(parameterBytes) > 1024 {
		return importedOpenAPIPathCredentialPointer{}, true, errors.New("pointer parameter is invalid")
	}
	parameter := string(parameterBytes)
	clear(parameterBytes)
	for _, character := range parameter {
		if character < 0x20 || character == 0x7f {
			return importedOpenAPIPathCredentialPointer{}, true, errors.New("pointer parameter is invalid")
		}
	}
	segmentLength, err := strconv.Atoi(parts[1])
	if err != nil || segmentLength <= 0 || segmentLength > 1<<20 {
		return importedOpenAPIPathCredentialPointer{}, true, errors.New("pointer segment length is invalid")
	}
	hashBytes, err := hex.DecodeString(parts[2])
	if err != nil || len(hashBytes) != sha256.Size {
		return importedOpenAPIPathCredentialPointer{}, true, errors.New("pointer shape hash is invalid")
	}
	var shapeHash [sha256.Size]byte
	copy(shapeHash[:], hashBytes)
	clear(hashBytes)
	targetSpans, err := decodeImportedOpenAPIPathSpans(parts[3], segmentLength)
	if err != nil {
		return importedOpenAPIPathCredentialPointer{}, true, err
	}
	allSpans, err := decodeImportedOpenAPIPathSpans(parts[4], segmentLength)
	if err != nil {
		return importedOpenAPIPathCredentialPointer{}, true, err
	}
	for _, targetSpan := range targetSpans {
		found := false
		for _, span := range allSpans {
			if targetSpan == span {
				found = true
				break
			}
		}
		if !found {
			return importedOpenAPIPathCredentialPointer{}, true, errors.New("pointer target span is not governed")
		}
	}
	return importedOpenAPIPathCredentialPointer{
		parameter:     parameter,
		segmentLength: segmentLength,
		shapeHash:     shapeHash,
		targetSpans:   targetSpans,
		allSpans:      allSpans,
	}, true, nil
}

func decodeImportedOpenAPIPathSpans(
	raw string,
	segmentLength int,
) ([]importedOpenAPIPathSpan, error) {
	if raw == "" {
		return nil, errors.New("pointer spans are empty")
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 256 {
		return nil, errors.New("pointer has too many spans")
	}
	spans := make([]importedOpenAPIPathSpan, 0, len(parts))
	previousEnd := 0
	for _, part := range parts {
		startText, endText, found := strings.Cut(part, "-")
		if !found {
			return nil, errors.New("pointer span is invalid")
		}
		start, startErr := strconv.Atoi(startText)
		end, endErr := strconv.Atoi(endText)
		if startErr != nil || endErr != nil || start < previousEnd ||
			start < 0 || end <= start || end > segmentLength {
			return nil, errors.New("pointer span is invalid")
		}
		spans = append(spans, importedOpenAPIPathSpan{start: start, end: end})
		previousEnd = end
	}
	return spans, nil
}

func importedOpenAPIPathCredentialValues(
	segment string,
	pointer importedOpenAPIPathCredentialPointer,
) ([]string, error) {
	if len(segment) != pointer.segmentLength {
		return nil, errors.New("retained credential path locator no longer matches the executable request")
	}
	shape := []byte(segment)
	for _, span := range pointer.allSpans {
		for index := span.start; index < span.end; index++ {
			shape[index] = 0
		}
	}
	digest := sha256.Sum256(shape)
	clear(shape)
	if !bytes.Equal(digest[:], pointer.shapeHash[:]) {
		return nil, errors.New("retained credential path locator no longer matches the executable request")
	}
	values := make([]string, 0, len(pointer.targetSpans))
	for _, span := range pointer.targetSpans {
		if span.start < 0 || span.end > len(segment) || span.end <= span.start {
			return nil, errors.New("retained credential path locator no longer matches the executable request")
		}
		values = append(values, segment[span.start:span.end])
	}
	return values, nil
}

func credentialValuesFromComposedPathSegment(
	sourceTemplate string,
	parameterName string,
	executableSegment string,
) ([]string, bool) {
	parameterName = strings.TrimSpace(parameterName)
	if parameterName == "" {
		return nil, false
	}
	var pattern strings.Builder
	pattern.WriteString("^")
	captures := 0
	for offset := 0; offset < len(sourceTemplate); {
		start := strings.IndexByte(sourceTemplate[offset:], '{')
		if start < 0 {
			pattern.WriteString(regexp.QuoteMeta(sourceTemplate[offset:]))
			break
		}
		start += offset
		pattern.WriteString(regexp.QuoteMeta(sourceTemplate[offset:start]))
		end := strings.IndexByte(sourceTemplate[start+1:], '}')
		if end < 0 {
			pattern.WriteString(regexp.QuoteMeta(sourceTemplate[start:]))
			break
		}
		end += start + 1
		name := strings.TrimSpace(sourceTemplate[start+1 : end])
		if name == parameterName {
			pattern.WriteString("(.*?)")
			captures++
		} else {
			pattern.WriteString(".*?")
		}
		offset = end + 1
	}
	pattern.WriteString("$")
	if captures == 0 {
		return nil, false
	}
	expression, err := regexp.Compile(pattern.String())
	if err != nil {
		return nil, false
	}
	match := expression.FindStringSubmatch(executableSegment)
	if len(match) != captures+1 {
		return nil, false
	}
	// An authored sensitive value is never represented by an empty locator
	// capture. Treating an ambiguous adjacent-placeholder match as success
	// would make the locator look valid while failing to protect the exact
	// credential if it is repeated on another disclosure surface.
	for _, value := range match[1:] {
		if value == "" {
			return nil, false
		}
	}
	return append([]string(nil), match[1:]...), true
}

func (collector *importedCredentialCollector) collectBodyTarget(
	payload []byte,
	mediaType string,
	target importedCredentialTarget,
) error {
	field := strings.ToLower(target.Field)
	baseMediaType, params, _ := mime.ParseMediaType(mediaType)
	trimmed := bytes.TrimSpace(payload)
	if strings.HasSuffix(field, ".opaque") {
		if len(payload) == 0 {
			return errors.New("retained opaque credential body locator no longer matches the executable request")
		}
		collector.add(string(payload))
		return nil
	}
	if strings.Contains(field, "graphql.query") {
		if strings.EqualFold(baseMediaType, "application/graphql") ||
			(len(trimmed) > 0 && trimmed[0] != '{') {
			if len(trimmed) == 0 {
				return errors.New("retained GraphQL query locator no longer matches the executable request")
			}
			collector.collectQuotedLiterals(string(payload))
			return nil
		}
		values, err := jsonValuesAtPointer(payload, "/query")
		if err != nil || len(values) == 0 {
			return errors.New("retained GraphQL query locator no longer matches the executable request")
		}
		for _, value := range values {
			collector.add(value)
			collector.collectQuotedLiterals(value)
		}
		return nil
	}
	if strings.Contains(field, "multipart") || strings.HasPrefix(baseMediaType, "multipart/") {
		return collector.collectMultipartTarget(payload, params["boundary"], target)
	}
	if strings.Contains(field, "xml") || strings.Contains(baseMediaType, "xml") ||
		(len(trimmed) > 0 && trimmed[0] == '<') {
		return collector.collectXMLTarget(payload, target)
	}
	if strings.Contains(baseMediaType, "x-www-form-urlencoded") {
		pairs := strings.Split(string(payload), "&")
		if target.Index < 0 || target.Index >= len(pairs) {
			return errors.New("retained form credential locator no longer matches the executable request")
		}
		_, rawValue, _ := strings.Cut(pairs[target.Index], "=")
		value, err := url.QueryUnescape(rawValue)
		if err != nil {
			return errors.New("retained form credential encoding is invalid")
		}
		collector.add(value)
		return nil
	}
	if strings.Contains(baseMediaType, "json") ||
		(len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')) {
		pointer := target.Pointer
		switch {
		case strings.Contains(field, "graphql.variables"):
			pointer = "/variables" + pointer
		case strings.Contains(field, "graphql.extensions"):
			pointer = "/extensions" + pointer
		}
		values, err := jsonValuesAtPointer(payload, pointer)
		if err != nil || len(values) == 0 {
			return errors.New("retained JSON credential locator no longer matches the executable request")
		}
		for _, value := range values {
			collector.add(value)
		}
		return nil
	}
	if len(payload) == 0 {
		return errors.New("retained credential body locator no longer matches the executable request")
	}
	collector.add(string(payload))
	return nil
}

func jsonValuesAtPointer(payload []byte, pointer string) ([]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var values []string
	var walk func(string, bool) error
	walk = func(path string, selected bool) error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		selected = selected || path == pointer ||
			(pointer != "" && strings.HasPrefix(path, strings.TrimRight(pointer, "/")+"/"))
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{':
				for decoder.More() {
					keyToken, err := decoder.Token()
					if err != nil {
						return err
					}
					key, ok := keyToken.(string)
					if !ok {
						return errors.New("JSON object key is invalid")
					}
					if err := walk(path+"/"+escapeRuntimeJSONPointer(key), selected); err != nil {
						return err
					}
				}
				_, err = decoder.Token()
				return err
			case '[':
				for index := 0; decoder.More(); index++ {
					if err := walk(path+"/"+strconv.Itoa(index), selected); err != nil {
						return err
					}
				}
				_, err = decoder.Token()
				return err
			default:
				return errors.New("JSON delimiter is invalid")
			}
		}
		if !selected && pointer != path {
			return nil
		}
		switch scalar := token.(type) {
		case string:
			values = append(values, scalar)
		case json.Number:
			values = append(values, scalar.String())
		case bool:
			values = append(values, strconv.FormatBool(scalar))
		}
		return nil
	}
	if err := walk("", pointer == ""); err != nil {
		return nil, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("JSON body has trailing data")
	}
	return values, nil
}

func escapeRuntimeJSONPointer(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func (collector *importedCredentialCollector) collectXMLTarget(
	payload []byte,
	target importedCredentialTarget,
) error {
	decoder := xml.NewDecoder(bytes.NewReader(payload))
	pointerParts := splitImportedXMLPointer(target.Pointer)
	attributeName := ""
	if len(pointerParts) > 0 && strings.HasPrefix(pointerParts[len(pointerParts)-1], "@") {
		attributeName = strings.TrimPrefix(pointerParts[len(pointerParts)-1], "@")
		pointerParts = pointerParts[:len(pointerParts)-1]
	}
	if strings.Contains(strings.ToLower(target.Field), "attribute") && attributeName == "" {
		return errors.New("retained XML attribute locator is incomplete")
	}

	var path []string
	matchIndex := 0
	selectedDepth := 0
	matched := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("retained XML credential body is malformed")
		}
		switch typed := token.(type) {
		case xml.StartElement:
			path = append(path, typed.Name.Local)
			if !importedXMLPathMatches(path, pointerParts) {
				continue
			}
			if attributeName != "" {
				for _, attr := range typed.Attr {
					if attr.Name.Local != attributeName {
						continue
					}
					if matchIndex == target.Occurrence {
						collector.add(attr.Value)
						matched = true
					}
					matchIndex++
				}
				continue
			}
			if matchIndex == target.Occurrence {
				selectedDepth = len(path)
			}
			matchIndex++
		case xml.CharData:
			if selectedDepth > 0 {
				value := strings.TrimSpace(string(typed))
				if value == "" {
					continue
				}
				collector.add(value)
				matched = true
			}
		case xml.EndElement:
			if selectedDepth == len(path) {
				selectedDepth = 0
			}
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
		}
	}
	if !matched {
		return errors.New("retained XML credential locator no longer matches the executable request")
	}
	return nil
}

func splitImportedXMLPointer(pointer string) []string {
	raw := strings.Split(strings.Trim(strings.TrimSpace(pointer), "/"), "/")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func importedXMLPathMatches(path, target []string) bool {
	if len(target) == 0 {
		// Legacy attribute locators used only "/@name" and therefore matched
		// the first occurrence anywhere in the document.
		return true
	}
	if len(target) == 1 {
		// Keep old "/password" locators readable while new imports persist the
		// complete element ancestry.
		return len(path) > 0 && path[len(path)-1] == target[0]
	}
	if len(path) != len(target) {
		return false
	}
	for index := range target {
		if path[index] != target[index] {
			return false
		}
	}
	return true
}

func (collector *importedCredentialCollector) collectMultipartTarget(
	payload []byte,
	boundary string,
	target importedCredentialTarget,
) error {
	if boundary == "" {
		return errors.New("retained multipart credential boundary is unavailable")
	}
	parts, err := parseImportedMultipartWire(payload, boundary)
	if err != nil {
		return errors.New("retained multipart credential body is malformed")
	}
	partIndex, remainder, compositional := importedMultipartLocator(target.Field)
	if !compositional {
		// Compatibility with the first locator profile, which stored the MIME
		// part index only in TargetRef.Index.
		partIndex = target.Index
		remainder = "body"
	}
	if partIndex < 0 || partIndex >= len(parts) {
		return errors.New("retained multipart credential locator no longer matches the executable request")
	}
	part := parts[partIndex]
	switch {
	case remainder == "headers" || strings.HasPrefix(remainder, "headers."):
		if target.Index < 0 || target.Index >= len(part.headers) {
			return errors.New("retained multipart credential header locator no longer matches the executable request")
		}
		collector.add(part.headers[target.Index].value)
		return nil
	case remainder == "body":
		collector.add(string(part.body))
		return nil
	case strings.HasPrefix(remainder, "body."):
		nestedTarget := target
		nestedTarget.Field = strings.TrimPrefix(remainder, "body.")
		if strings.TrimSpace(nestedTarget.Field) == "" {
			nestedTarget.Field = "body"
		}
		return collector.collectBodyTarget(part.body, part.contentType, nestedTarget)
	default:
		return errors.New("retained multipart credential locator names an unsupported part field")
	}
}

type importedMultipartHeader struct {
	name  string
	value string
}

type importedMultipartPart struct {
	headers     []importedMultipartHeader
	body        []byte
	contentType string
}

func importedMultipartLocator(field string) (partIndex int, remainder string, ok bool) {
	const markerText = "multipart.parts["
	lower := strings.ToLower(strings.TrimSpace(field))
	marker := strings.Index(lower, markerText)
	if marker < 0 {
		return 0, "", false
	}
	start := marker + len(markerText)
	endRelative := strings.IndexByte(lower[start:], ']')
	if endRelative < 0 {
		return 0, "", false
	}
	parsed, err := strconv.Atoi(lower[start : start+endRelative])
	if err != nil || parsed < 0 {
		return 0, "", false
	}
	remainder = strings.TrimPrefix(lower[start+endRelative+1:], ".")
	if remainder == "" {
		return 0, "", false
	}
	return parsed, remainder, true
}

func parseImportedMultipartWire(payload []byte, boundary string) ([]importedMultipartPart, error) {
	if int64(len(payload)) > dsl.MaxBodyRefBytes {
		return nil, errors.New("multipart body exceeds the runtime limit")
	}
	marker := []byte("--" + boundary)
	_, closing, lineEnd, ok := nextImportedMultipartDelimiter(payload, marker, 0)
	if !ok || closing {
		return nil, errors.New("multipart opening boundary is missing")
	}
	const maxParts = 10_000
	parts := make([]importedMultipartPart, 0)
	cursor := lineEnd
	for {
		if len(parts) >= maxParts {
			return nil, errors.New("multipart part limit exceeded")
		}
		nextStart, nextClosing, nextLineEnd, found := nextImportedMultipartDelimiter(
			payload,
			marker,
			cursor,
		)
		if !found {
			return nil, errors.New("multipart closing boundary is missing")
		}
		partEnd := nextStart
		if partEnd >= 2 && bytes.Equal(payload[partEnd-2:partEnd], []byte("\r\n")) {
			partEnd -= 2
		} else if partEnd >= 1 && payload[partEnd-1] == '\n' {
			partEnd--
		}
		part, err := parseImportedMultipartPart(payload, cursor, partEnd)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
		if nextClosing {
			return parts, nil
		}
		cursor = nextLineEnd
	}
}

func nextImportedMultipartDelimiter(
	payload []byte,
	marker []byte,
	from int,
) (start int, closing bool, lineEnd int, ok bool) {
	for from <= len(payload)-len(marker) {
		relative := bytes.Index(payload[from:], marker)
		if relative < 0 {
			return 0, false, 0, false
		}
		start = from + relative
		if start > 0 && payload[start-1] != '\n' {
			from = start + 1
			continue
		}
		cursor := start + len(marker)
		if cursor+2 <= len(payload) && bytes.Equal(payload[cursor:cursor+2], []byte("--")) {
			closing = true
			cursor += 2
		}
		for cursor < len(payload) && (payload[cursor] == ' ' || payload[cursor] == '\t') {
			cursor++
		}
		switch {
		case cursor == len(payload):
			return start, closing, cursor, true
		case cursor+2 <= len(payload) && bytes.Equal(payload[cursor:cursor+2], []byte("\r\n")):
			return start, closing, cursor + 2, true
		case payload[cursor] == '\n':
			return start, closing, cursor + 1, true
		default:
			from = start + 1
		}
	}
	return 0, false, 0, false
}

func parseImportedMultipartPart(
	payload []byte,
	start int,
	end int,
) (importedMultipartPart, error) {
	if start < 0 || end < start || end > len(payload) {
		return importedMultipartPart{}, errors.New("multipart part span is invalid")
	}
	partBytes := payload[start:end]
	crlf := bytes.Index(partBytes, []byte("\r\n\r\n"))
	lf := bytes.Index(partBytes, []byte("\n\n"))
	headerEnd := -1
	bodyStart := -1
	switch {
	case crlf >= 0 && (lf < 0 || crlf <= lf):
		headerEnd = start + crlf
		bodyStart = start + crlf + 4
	case lf >= 0:
		headerEnd = start + lf
		bodyStart = start + lf + 2
	default:
		return importedMultipartPart{}, errors.New("multipart part header separator is missing")
	}
	headers, err := parseImportedMultipartHeaders(payload, start, headerEnd)
	if err != nil {
		return importedMultipartPart{}, err
	}
	part := importedMultipartPart{
		headers: headers,
		body:    append([]byte(nil), payload[bodyStart:end]...),
	}
	for _, header := range headers {
		if strings.EqualFold(header.name, "Content-Type") {
			part.contentType = header.value
		}
	}
	return part, nil
}

func parseImportedMultipartHeaders(
	payload []byte,
	start int,
	end int,
) ([]importedMultipartHeader, error) {
	headers := make([]importedMultipartHeader, 0)
	cursor := start
	for cursor < end {
		lineEnd := bytes.IndexByte(payload[cursor:end], '\n')
		if lineEnd < 0 {
			lineEnd = end
		} else {
			lineEnd += cursor
		}
		contentEnd := lineEnd
		if contentEnd > cursor && payload[contentEnd-1] == '\r' {
			contentEnd--
		}
		if contentEnd == cursor || payload[cursor] == ' ' || payload[cursor] == '\t' {
			return nil, errors.New("multipart folded or empty header is unsupported")
		}
		colon := bytes.IndexByte(payload[cursor:contentEnd], ':')
		if colon <= 0 {
			return nil, errors.New("multipart header lacks a name or colon")
		}
		colon += cursor
		name := strings.TrimSpace(string(payload[cursor:colon]))
		value := strings.TrimSpace(string(payload[colon+1 : contentEnd]))
		if name == "" {
			return nil, errors.New("multipart header name is empty")
		}
		headers = append(headers, importedMultipartHeader{name: name, value: value})
		if lineEnd == end {
			break
		}
		cursor = lineEnd + 1
	}
	return headers, nil
}

func (collector *importedCredentialCollector) collectQuotedLiterals(input string) {
	for index := 0; index < len(input); index++ {
		if input[index] != '"' {
			continue
		}
		start := index
		index++
		for index < len(input) {
			if input[index] == '\\' {
				index += 2
				continue
			}
			if input[index] == '"' {
				quoted := input[start : index+1]
				if value, err := strconv.Unquote(quoted); err == nil {
					collector.add(value)
				}
				break
			}
			index++
		}
	}
}
