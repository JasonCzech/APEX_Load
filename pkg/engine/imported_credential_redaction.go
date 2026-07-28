package engine

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	stdhttp "net/http"
	"net/url"
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
		collector.collectSurfaceKnownCredentials(ref)
		targets, err := decodeImportedCredentialTargets(
			ref.Metadata()[dsl.ImportPreservedCredentialTargetsMetadataKey],
		)
		if err != nil {
			return fmt.Errorf("%s: %w", ref.Path, err)
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
	case strings.Contains(field, "config.base_url"):
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

// collectDSN protects both the complete connection composite and components
// that database drivers may echo independently in errors. A DSN is itself a
// credential-governed field, so even neutral query option names are included.
func (collector *importedCredentialCollector) collectDSN(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "{{secret.") {
		return
	}
	collector.add(raw)
	candidate := raw
	if strings.HasPrefix(strings.ToLower(candidate), "jdbc:") {
		candidate = candidate[len("jdbc:"):]
	}
	if parsed, err := url.Parse(candidate); err == nil {
		if parsed.User != nil {
			collector.add(parsed.User.String())
			collector.add(parsed.User.Username())
			if password, ok := parsed.User.Password(); ok {
				collector.add(password)
			}
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
	if auth, ok := request["auth"].(map[string]any); ok {
		if password, _ := auth["password"].(string); password != "" {
			collector.add(password)
		}
		if token, _ := auth["token"].(string); token != "" {
			collector.add(token)
		}
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
	if list, ok := request["header_list"].([]any); ok {
		for _, raw := range list {
			header, _ := raw.(map[string]any)
			name, _ := header["name"].(string)
			value, _ := header["value"].(string)
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
	list, ok := ref.Request["header_list"].([]any)
	if !ok || index < 0 || index >= len(list) {
		return "", false
	}
	header, ok := list[index].(map[string]any)
	if !ok {
		return "", false
	}
	value, ok := header["value"].(string)
	return value, ok
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
	auth, ok := ref.Request["auth"].(map[string]any)
	if !ok {
		return "", false
	}
	value, ok := auth[name].(string)
	return value, ok
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
	if list, ok := ref.Request["header_list"].([]any); ok {
		for _, raw := range list {
			header, _ := raw.(map[string]any)
			name, _ := header["name"].(string)
			if strings.EqualFold(name, "Content-Type") {
				value, _ := header["value"].(string)
				return value
			}
		}
	}
	return ""
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
		collector.add(parsed.User.String())
		collector.add(parsed.User.Username())
		if password, ok := parsed.User.Password(); ok {
			collector.add(password)
		}
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
		collector.add(parsed.User.String())
		collector.add(parsed.User.Username())
		if password, ok := parsed.User.Password(); ok {
			collector.add(password)
		}
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
