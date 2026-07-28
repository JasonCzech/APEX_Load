package jmeter

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

var jmeterRuntimeVariablePattern = regexp.MustCompile(`\{\{\s*var\.([A-Za-z_][A-Za-z0-9_.-]*)\s*\}\}`)

// finalizeJMeterHTTPRequest is deliberately downstream of JMX inheritance,
// controller traversal, assertion/extractor parsing, and SOAP recognition.
// Only the fully effective HTTP request crosses into the shared finalizer.
func finalizeJMeterHTTPRequest(
	input *dsl.Action,
	location, sourceFile string,
	ctx *convertContext,
	preAppliedVariables jmeterVariableApplication,
) (dsl.Action, requestimport.Report, error) {
	if input == nil {
		return dsl.Action{}, requestimport.Report{Disposition: requestimport.Rejected}, fmt.Errorf("HTTP action is required")
	}
	source := credentialpolicy.SourceRef{
		Format:  "jmeter",
		Path:    sourceFile,
		Pointer: location,
	}
	headers := jmeterEffectiveHeaders(input, source)
	findings := sanitizeJMeterHeaderValues(headers, source)
	bodyKind := jmeterRequestBodyKind(input.Body, input.Headers)
	effective := requestimport.EffectiveRequest{
		Key:     input.Name,
		Name:    input.Name,
		Method:  input.Method,
		Target:  input.Target,
		Headers: headers,
		Body: requestimport.Body{
			Kind:     bodyKind,
			Payload:  []byte(input.Body),
			Encoding: requestimport.BodyEncodingText,
			Source:   credentialpolicy.SourceRef{Format: "jmeter", Path: sourceFile, Pointer: location + "/body"},
		},
		Auth:      jmeterEffectiveAuth(input.Auth, source),
		Timeout:   jmeterEffectiveTimeout(input.TimeoutMs),
		Redirects: jmeterEffectiveRedirects(input.MaxRedirects),
		Metadata:  input.Metadata,
		Source:    source,
	}
	registry := (*credentialpolicy.NameRegistry)(nil)
	if ctx != nil {
		registry = ctx.credentialNames
	}
	provenVariables := jmeterProvenVariables(ctx)
	var resolution *jmeterVariableResolution
	if ctx != nil {
		resolution = ctx.variableScope.resolve(nil)
	}
	variableApplication := applyJMeterVariablesToEffectiveRequest(
		&effective,
		resolution,
		provenVariables,
	)
	variableApplication.merge(preAppliedVariables)
	action, report, err := requestimport.FinalizeAction(effective, requestimport.FinalizeOptions{
		Project: requestimport.DefaultProjectOptions(),
		Credentials: &requestimport.CredentialProjectionOptions{
			Mode:            credentialpolicy.Preserve,
			Registry:        registry,
			ProvenVars:      provenVariables,
			SourceVariables: jmeterSourceVariables(resolution),
			NamePrefix:      "JMETER",
		},
		Overlay: requestimport.ActionOverlay{
			Type:           input.Type,
			Params:         input.Params,
			Metadata:       input.Metadata,
			Extractors:     input.Extractors,
			Assertions:     input.Assertions,
			ThinkTime:      input.ThinkTime,
			ThinkTimeAfter: input.ThinkTimeAfter,
			SOAP:           input.SOAP,
		},
		Findings: findings,
	})
	if err != nil {
		return dsl.Action{}, report, err
	}
	attachJMeterVariableRequirementProvenance(
		report.Requirements,
		variableApplication,
		resolution,
	)
	if sharedReasons := jmeterExecutionFindingCodes(report.Findings); len(sharedReasons) > 0 {
		applyJMeterSharedReviewGate(&action, sharedReasons, sourceFile+"#"+location)
	}
	action.BodyFile = input.BodyFile
	return action, report, nil
}

func jmeterEffectiveHeaders(action *dsl.Action, source credentialpolicy.SourceRef) requestimport.HeaderList {
	var headers requestimport.HeaderList
	if len(action.HeaderList) > 0 {
		headers = make(requestimport.HeaderList, 0, len(action.HeaderList))
		for index, header := range action.HeaderList {
			headers = append(headers, requestimport.Header{
				Name:  header.Name,
				Value: header.Value,
				Source: credentialpolicy.SourceRef{
					Format:  source.Format,
					Path:    source.Path,
					Pointer: fmt.Sprintf("%s/headers/%d", source.Pointer, index),
				},
			})
		}
		return headers
	}
	headers = requestimport.HeaderListFromMap(action.Headers)
	for index := range headers {
		headers[index].Source = credentialpolicy.SourceRef{
			Format:  source.Format,
			Path:    source.Path,
			Pointer: fmt.Sprintf("%s/headers/%d", source.Pointer, index),
		}
	}
	return headers
}

func sanitizeJMeterHeaderValues(headers requestimport.HeaderList, source credentialpolicy.SourceRef) []importreview.Finding {
	var findings []importreview.Finding
	for index := range headers {
		if !strings.ContainsAny(headers[index].Value, "\r\n") {
			continue
		}
		findings = append(findings, importreview.Finding{
			Code:     "jmeter_invalid_header_value",
			Severity: diag.Blocker,
			Element:  headers[index].Name,
			Detail:   "the JMeter header contained a line break and cannot be sent by the native HTTP runtime",
			Location: source.Path + "#" + source.Pointer,
			Gate:     importreview.GateExecution,
		})
	}
	return findings
}

func jmeterEffectiveAuth(auth *dsl.AuthConfig, source credentialpolicy.SourceRef) requestimport.Auth {
	if auth == nil {
		return requestimport.Auth{Mode: requestimport.AuthInherit, Source: source}
	}
	mode := requestimport.AuthMode(strings.ToLower(strings.TrimSpace(auth.Type)))
	switch mode {
	case requestimport.AuthNone, requestimport.AuthBasic, requestimport.AuthBearer, requestimport.AuthDigest:
	default:
		mode = requestimport.AuthUnsupported
	}
	return requestimport.Auth{
		Mode:       mode,
		SourceMode: auth.Type,
		Username:   auth.Username,
		Password:   auth.Password,
		Token:      auth.Token,
		Domain:     auth.Domain,
		Source:     source,
	}
}

func jmeterEffectiveTimeout(value int) requestimport.Timeout {
	if value <= 0 {
		return requestimport.Timeout{Mode: requestimport.TimeoutInherit}
	}
	return requestimport.Timeout{Mode: requestimport.TimeoutBounded, Milliseconds: value}
}

func jmeterEffectiveRedirects(value *int) requestimport.Redirects {
	if value == nil {
		return requestimport.Redirects{Mode: requestimport.RedirectInherit}
	}
	if *value <= 0 {
		return requestimport.Redirects{Mode: requestimport.RedirectDisabled}
	}
	return requestimport.Redirects{Mode: requestimport.RedirectLimited, Max: *value}
}

func jmeterRequestBodyKind(body string, headers map[string]string) requestimport.BodyKind {
	if body == "" {
		return requestimport.BodyNone
	}
	contentType := ""
	for name, value := range headers {
		if strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
			contentType = value
		}
	}
	mediaType, _, _ := mime.ParseMediaType(contentType)
	switch strings.ToLower(mediaType) {
	case "application/json", "application/problem+json":
		return requestimport.BodyJSON
	case "application/xml", "text/xml", "application/soap+xml":
		return requestimport.BodyXML
	case "application/x-www-form-urlencoded":
		return requestimport.BodyFormURLEncoded
	case "application/graphql":
		return requestimport.BodyGraphQL
	}
	trimmed := strings.TrimSpace(body)
	if json.Valid([]byte(trimmed)) && (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) {
		return requestimport.BodyJSON
	}
	if strings.HasPrefix(trimmed, "<") {
		return requestimport.BodyXML
	}
	return requestimport.BodyRaw
}

func jmeterProvenVariables(ctx *convertContext) map[string]struct{} {
	if ctx == nil || len(ctx.provenVariables) == 0 {
		return nil
	}
	proven := make(map[string]struct{}, len(ctx.provenVariables))
	for name := range ctx.provenVariables {
		proven[name] = struct{}{}
	}
	return proven
}

func jmeterSourceVariables(
	resolution *jmeterVariableResolution,
) map[string]credentialpolicy.SourceVariable {
	if resolution == nil || len(resolution.sourceVariables) == 0 {
		return nil
	}
	out := make(map[string]credentialpolicy.SourceVariable, len(resolution.sourceVariables))
	for name, variable := range resolution.sourceVariables {
		out[name] = variable
	}
	return out
}

func jmeterExecutionFindingCodes(findings []importreview.Finding) []string {
	var codes []string
	for _, finding := range findings {
		if finding.Gate == importreview.GateExecution || finding.Severity == diag.Blocker {
			code := strings.TrimSpace(finding.ReasonCode)
			if strings.HasPrefix(code, "unsupported_http_auth_") {
				// JMeter has historically exposed one stable source-facing reason
				// for every AuthManager mechanism the HTTP runtime cannot execute.
				// Keep that adapter contract while the shared finding retains the
				// more precise mechanism-specific reason in the conversion report.
				code = "unsupported_auth_scheme"
			}
			if code == "" {
				code = finding.Code
			}
			codes = append(codes, code)
		}
	}
	return uniqueJMeterStrings(codes)
}

func applyJMeterSharedReviewGate(action *dsl.Action, reasons []string, sourceRef string) {
	if action.Metadata == nil {
		action.Metadata = make(map[string]any)
	}
	action.Metadata["execution_blocker"] = "import_review_required"
	action.Metadata["import_review"] = map[string]any{
		"version":      1,
		"source":       "jmeter",
		"reason_codes": uniqueJMeterStrings(reasons),
		"source_refs":  []string{sourceRef},
		"resolution":   "replace_behavior_and_remove_gate",
	}
}

func projectJMeterHTTPConfigCredentials(script *dsl.Script, ctx *convertContext, sourceFile string) (requestimport.Report, error) {
	if script == nil {
		return requestimport.Report{Disposition: requestimport.Rejected}, nil
	}
	report := requestimport.Report{RequestKey: script.Name + ":config", Disposition: requestimport.ExecutableExact}
	source := credentialpolicy.SourceRef{Format: "jmeter", Path: sourceFile, Pointer: "script_config"}
	registry := (*credentialpolicy.NameRegistry)(nil)
	var resolution *jmeterVariableResolution
	if ctx != nil {
		registry = ctx.credentialNames
		resolution = ctx.variableScope.resolve(nil)
	}
	options := credentialpolicy.Options{
		Mode:            credentialpolicy.Preserve,
		Source:          source,
		RequestKey:      report.RequestKey,
		NamePrefix:      "JMETER",
		Registry:        registry,
		SourceVariables: jmeterSourceVariables(resolution),
	}
	if len(script.Config.HTTP.DefaultHeaders) > 0 {
		input := requestimport.HeaderListFromMap(script.Config.HTTP.DefaultHeaders)
		occurrences := make([]credentialpolicy.HeaderOccurrence, 0, len(input))
		headerApplications := make(map[int][]string)
		for _, header := range input {
			if credential.SensitiveHeader(header.Name) {
				if projected, ok := jmeterProjectedActionHeader(script, header.Name); ok {
					script.Config.HTTP.DefaultHeaders[header.Name] = projected
					continue
				}
			}
			value, used := resolution.expandRuntimeValue(header.Value, nil)
			headerApplications[len(occurrences)] = used
			occurrences = append(occurrences, credentialpolicy.HeaderOccurrence{Name: header.Name, Value: value, Source: source})
		}
		projected, result, err := credentialpolicy.ProjectHeaders(occurrences, options)
		if err != nil {
			return report, err
		}
		headers := requestimport.HeaderListFromMap(script.Config.HTTP.DefaultHeaders)
		for _, header := range projected {
			replaced := false
			for headerIndex := range headers {
				if strings.EqualFold(headers[headerIndex].Name, header.Name) {
					headers[headerIndex].Value = header.Value
					replaced = true
					break
				}
			}
			if !replaced {
				headers = append(headers, requestimport.Header{Name: header.Name, Value: header.Value})
			}
		}
		script.Config.HTTP.DefaultHeaders = headers.Summary()
		attachJMeterVariableRequirementProvenance(
			result.Requirements,
			jmeterVariableApplication{headers: headerApplications},
			resolution,
		)
		report.Requirements = append(report.Requirements, result.Requirements...)
	}
	auth := script.Config.HTTP.Auth
	if auth.Type != "" {
		authApplication := jmeterVariableApplication{auth: make(map[string][]string)}
		auth.Username, authApplication.auth["auth.username"] = resolution.expandRuntimeValue(auth.Username, nil)
		auth.Password, authApplication.auth["auth.password"] = resolution.expandRuntimeValue(auth.Password, nil)
		auth.Token, authApplication.auth["auth.token"] = resolution.expandRuntimeValue(auth.Token, nil)
		auth.Domain, authApplication.auth["auth.domain"] = resolution.expandRuntimeValue(auth.Domain, nil)
		projected, result, err := credentialpolicy.ProjectAuth(credentialpolicy.AuthFields{
			Type: auth.Type, Username: auth.Username, Password: auth.Password,
			Token: auth.Token, Domain: auth.Domain, Source: source,
		}, options)
		if err != nil {
			return report, err
		}
		script.Config.HTTP.Auth = dsl.AuthConfig{
			Type:     auth.Type,
			Username: projected.Username,
			Password: projected.Password,
			Token:    projected.Token,
			Domain:   projected.Domain,
		}
		attachJMeterVariableRequirementProvenance(
			result.Requirements,
			authApplication,
			resolution,
		)
		report.Requirements = append(report.Requirements, result.Requirements...)
	}
	return report, nil
}

func jmeterProjectedActionHeader(script *dsl.Script, name string) (string, bool) {
	var projected string
	_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		if projected != "" {
			return nil
		}
		for _, header := range ref.Action.HeaderList {
			if !strings.EqualFold(strings.TrimSpace(header.Name), strings.TrimSpace(name)) {
				continue
			}
			if _, ok := credential.ParseReference(header.Value); ok {
				projected = header.Value
				return nil
			}
		}
		return nil
	})
	return projected, projected != ""
}

func prepareJMeterHTTPDraft(script *dsl.Script, sourceFile string) (requestimport.Report, error) {
	report := requestimport.Report{
		RequestKey:  script.Name + ":draft",
		Disposition: requestimport.ExecutableExact,
	}
	var reasonCodes []string
	if strings.TrimSpace(script.Config.BaseURL) == "" {
		script.Config.BaseURL = "http://import.invalid"
		reasonCodes = append(reasonCodes, "jmeter_missing_base_url")
	}
	missingDataBindings := false
	for index := range script.DataSources {
		source := &script.DataSources[index]
		rawPath := strings.TrimSpace(source.Path)
		if rawPath == "" || filepath.IsAbs(rawPath) {
			continue
		}
		if source.FileName == "" {
			source.FileName = filepath.Base(rawPath)
		}
		source.Path = ""
		if strings.TrimSpace(source.InlineData) == "" {
			source.InlineData = requestimport.MissingFilePlaceholder
			source.Encoding = "text"
			missingDataBindings = true
		}
	}
	if missingDataBindings {
		reasonCodes = append(reasonCodes, "import_missing_file_bindings")
	}
	reasonCodes = uniqueJMeterStrings(reasonCodes)
	if len(reasonCodes) == 0 {
		return report, nil
	}
	if err := dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		if ref.Action == nil || !jmeterRequestShapedAction(ref.Action) {
			return nil
		}
		for _, reasonCode := range reasonCodes {
			report.Findings = append(report.Findings, importreview.Finding{
				Code:       reasonCode,
				Severity:   diag.Blocker,
				Element:    ref.Action.Name,
				Detail:     "the JMeter request draft requires endpoint or data-file binding before execution",
				Location:   sourceFile,
				ActionPath: ref.Path,
				Gate:       importreview.GateExecution,
			})
		}
		applyJMeterSharedReviewGate(ref.Action, reasonCodes, sourceFile)
		return nil
	}); err != nil {
		return report, err
	}
	report.Findings = importreview.NormalizeFindings(report.Findings)
	report.Disposition = requestimport.BlockedDraft
	return report, nil
}

func jmeterRequestShapedAction(action *dsl.Action) bool {
	if action == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "control_if", "control_loop", "review_required":
		return false
	}
	return strings.TrimSpace(action.Target) != "" &&
		strings.TrimSpace(action.Method) != ""
}

func unresolvedJMeterCredentialBindings(reports []requestimport.Report) int {
	count := 0
	for _, report := range reports {
		for _, requirement := range report.Requirements {
			if requirement.SourceValueAvailable && requirement.Value != "" {
				count++
			}
		}
	}
	return count
}

func publicJMeterCredentialRequirements(requirements []credentialpolicy.Requirement) []credentialpolicy.Requirement {
	if len(requirements) == 0 {
		return nil
	}
	out := credentialpolicy.CloneRequirements(requirements)
	for index := range out {
		out[index].Value = ""
		out[index].SourceValueAvailable = false
	}
	return out
}

func redactJMeterCredentialValues(
	content string,
	requirements []credentialpolicy.Requirement,
	review *jmeterVariableReview,
) (string, bool) {
	values := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		if value := strings.TrimSpace(requirement.Value); value != "" {
			values = append(values, value)
		}
	}
	sort.SliceStable(values, func(i, j int) bool {
		if len(values[i]) == len(values[j]) {
			return values[i] < values[j]
		}
		return len(values[i]) > len(values[j])
	})
	for _, value := range values {
		content = strings.ReplaceAll(content, value, credential.Redacted)
	}

	if review == nil {
		if jmeterXMLContainsDecodedValue(content, values) {
			return "<!-- [JMETER SOURCE OMITTED: ENCODED CREDENTIAL VALUE REQUIRES PROTECTED BINDING] -->", true
		}
		return content, len(values) > 0
	}
	if review.omitSource {
		return "// [JMETER SOURCE OMITTED: VARIABLE GRAPH EXCEEDED SAFE ANALYSIS LIMITS]", true
	}
	names := make(map[string]struct{})
	for _, requirement := range requirements {
		for _, name := range requirement.SourceVariableNames {
			if strings.TrimSpace(name) != "" {
				names[name] = struct{}{}
			}
		}
	}
	queue := sortedJMeterVariableSet(names)
	for index := 0; index < len(queue); index++ {
		for dependency := range review.dependencies[queue[index]] {
			if _, exists := names[dependency]; exists {
				continue
			}
			names[dependency] = struct{}{}
			queue = append(queue, dependency)
		}
	}
	variableValues := make(map[string]struct{})
	for name := range names {
		for value := range review.valuesByName[name] {
			variableValues[value] = struct{}{}
		}
	}
	for value := range review.invalidValues {
		variableValues[value] = struct{}{}
	}
	beforeVariables := content
	for _, value := range sortedJMeterValuesByLength(variableValues) {
		content = redactJMeterVariableValue(content, value)
	}
	decodedValues := append([]string(nil), values...)
	for value := range variableValues {
		decodedValues = append(decodedValues, value)
	}
	if jmeterXMLContainsDecodedValue(content, decodedValues) {
		return "<!-- [JMETER SOURCE OMITTED: ENCODED CREDENTIAL VALUE REQUIRES PROTECTED BINDING] -->", true
	}
	return content, len(values) > 0 || content != beforeVariables
}

func jmeterXMLContainsDecodedValue(content string, protected []string) bool {
	protected = uniqueJMeterStrings(protected)
	if len(protected) == 0 {
		return false
	}
	contains := func(value string) bool {
		for _, protectedValue := range protected {
			if strings.Contains(value, protectedValue) {
				return true
			}
		}
		return false
	}
	decoder := xml.NewDecoder(strings.NewReader(content))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return false
		}
		if err != nil {
			// This content came from a successfully parsed JMX document. If
			// redaction made structural inspection ambiguous, fail closed by
			// omitting the public source review.
			return true
		}
		switch typed := token.(type) {
		case xml.CharData:
			if contains(string(typed)) {
				return true
			}
		case xml.StartElement:
			for _, attribute := range typed.Attr {
				if contains(attribute.Value) {
					return true
				}
			}
		}
	}
}

func uniqueJMeterStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
