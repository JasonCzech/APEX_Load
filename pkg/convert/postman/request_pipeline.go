package postman

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// postmanFinalization retains only disclosure-safe shared reports for package
// tests and diagnostics. Migration values never leave the conversion stack.
type postmanFinalization struct {
	Reports []requestimport.Report
	Result  *requestimport.FinalizeResult

	protectedRequirements credentialpolicy.ProtectedRequirements
}

type postmanRequestPipeline struct {
	sanitizeSensitive  bool
	registry           *credentialpolicy.NameRegistry
	provenVariables    map[string]struct{}
	sourceVariables    map[string]credentialpolicy.SourceVariable
	sourceDependencies map[string][]string
	sourceReviewNames  map[string]struct{}
	sourceReviewBodies map[string]string
	reports            []requestimport.Report
}

func newPostmanRequestPipeline(
	variables map[string]string,
	sanitizeSensitive bool,
	dependencySets ...map[string][]string,
) *postmanRequestPipeline {
	keys := make([]string, 0, len(variables))
	for name := range variables {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	sourceVariables := make(map[string]credentialpolicy.SourceVariable, len(keys))
	for _, sourceName := range keys {
		name := normalizeVariableName(sourceName)
		if name == "" {
			continue
		}
		sourceVariables[name] = credentialpolicy.SourceVariable{
			Value: variables[sourceName],
			Source: credentialpolicy.SourceRef{
				Format:  "postman",
				Path:    "postman_collection.json",
				Pointer: "/variables/" + name,
			},
		}
	}
	pipeline := &postmanRequestPipeline{
		sanitizeSensitive:  sanitizeSensitive,
		registry:           credentialpolicy.NewNameRegistry(),
		provenVariables:    make(map[string]struct{}),
		sourceVariables:    sourceVariables,
		sourceReviewNames:  make(map[string]struct{}),
		sourceReviewBodies: make(map[string]string),
		reports:            make([]requestimport.Report, 0),
	}
	if len(dependencySets) > 0 {
		pipeline.sourceDependencies = make(map[string][]string, len(dependencySets[0]))
		for name, dependencies := range dependencySets[0] {
			pipeline.sourceDependencies[normalizeVariableName(name)] = append([]string(nil), dependencies...)
		}
	}
	return pipeline
}

func (pipeline *postmanRequestPipeline) updateSourceVariables(variables map[string]string, dependencies map[string][]string) {
	if pipeline == nil {
		return
	}
	updated := newPostmanRequestPipeline(variables, pipeline.sanitizeSensitive, dependencies)
	pipeline.sourceVariables = updated.sourceVariables
	pipeline.sourceDependencies = updated.sourceDependencies
}

func (pipeline *postmanRequestPipeline) FinalizeAction(
	index int,
	item requestItem,
	source dsl.Action,
	actionPath string,
) (dsl.Action, error) {
	if pipeline == nil {
		return dsl.Action{}, fmt.Errorf("Postman request pipeline is required")
	}
	request := postmanEffectiveRequest(index, item, source)
	pipeline.collectSourceReviewCredentialEvidence(item)
	if request.Body.Kind == requestimport.BodyMultipart {
		multipart, err := parsePostmanMultipart(
			request.Body.Payload,
			multipartBoundary(request.Body.MediaType),
			request.Source,
		)
		if err != nil {
			return dsl.Action{}, err
		}
		request.Body.Payload = nil
		request.Body.Multipart = postmanEffectiveMultipart(multipart)
		for _, part := range request.Body.Multipart.Parts {
			if part.FileAssetRef == "" {
				continue
			}
			request.Body.Assets = append(request.Body.Assets, requestimport.Asset{
				Name:        part.FileAssetRef,
				FileName:    part.FileName,
				ContentType: part.ContentType,
				Source:      part.Source,
			})
		}
	}
	mode := credentialpolicy.Preserve
	projectorVariables := clonePostmanVariableSet(pipeline.provenVariables)
	sheltered := shelterPostmanTemplates(&request, pipeline.provenVariables, pipeline.sourceVariables)
	projected, report, err := requestimport.FinalizeAction(request, requestimport.FinalizeOptions{
		Project: requestimport.DefaultProjectOptions(),
		Credentials: &requestimport.CredentialProjectionOptions{
			Mode:            mode,
			Registry:        pipeline.registry,
			ProvenVars:      projectorVariables,
			SourceVariables: pipeline.sourceVariables,
			NamePrefix:      "POSTMAN",
		},
		Overlay: requestimport.ActionOverlay{
			Type:           source.Type,
			Params:         source.Params,
			Metadata:       source.Metadata,
			Extractors:     source.Extractors,
			Assertions:     source.Assertions,
			ThinkTime:      source.ThinkTime,
			ThinkTimeAfter: source.ThinkTimeAfter,
			SOAP:           source.SOAP,
		},
		ActionPath: actionPath,
	})
	if err != nil {
		return dsl.Action{}, err
	}
	restorePostmanTemplates(&projected, source, sheltered)
	pipeline.reports = append(pipeline.reports, report)
	// A response capture becomes dynamic only after its producing request has
	// executed. Register it after projecting this action so a same-named static
	// collection/environment value is still governed on this request, while
	// later requests retain genuine response-derived runtime references.
	for _, extractor := range detectPostmanExtractors(item.Events) {
		if name := strings.TrimSpace(extractor.varName); name != "" {
			pipeline.provenVariables[name] = struct{}{}
		}
	}
	return projected, nil
}

func (pipeline *postmanRequestPipeline) consumedSourceVariableNames() []string {
	if pipeline == nil {
		return nil
	}
	seen := make(map[string]struct{})
	for name := range pipeline.sourceReviewNames {
		seen[name] = struct{}{}
	}
	for _, report := range pipeline.reports {
		for _, requirement := range report.Requirements {
			for _, name := range requirement.SourceVariableNames {
				if name = strings.TrimSpace(name); name != "" {
					seen[name] = struct{}{}
				}
			}
		}
	}
	queue := make([]string, 0, len(seen))
	for name := range seen {
		queue = append(queue, name)
	}
	sort.Strings(queue)
	for index := 0; index < len(queue); index++ {
		for _, dependency := range pipeline.sourceDependencies[queue[index]] {
			dependency = normalizeVariableName(dependency)
			if dependency == "" {
				continue
			}
			if _, exists := seen[dependency]; exists {
				continue
			}
			seen[dependency] = struct{}{}
			queue = append(queue, dependency)
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (pipeline *postmanRequestPipeline) collectSourceReviewCredentialEvidence(item requestItem) {
	if pipeline == nil {
		return
	}
	recordReferences := func(value string) {
		for _, match := range variablePattern.FindAllStringSubmatch(value, -1) {
			if len(match) != 2 {
				continue
			}
			if name := normalizeVariableName(match[1]); name != "" {
				pipeline.sourceReviewNames[name] = struct{}{}
			}
		}
	}
	for _, header := range item.Request.Header {
		if credential.SensitiveHeader(header.Key) || credential.SensitiveName(header.Key) {
			recordReferences(header.Value)
		}
	}
	for _, candidate := range []*auth{item.InheritedAuth, item.Request.Auth} {
		if candidate == nil {
			continue
		}
		for _, entries := range [][]keyValueEntry{
			candidate.Bearer,
			candidate.Basic,
			candidate.Digest,
			candidate.NTLM,
			candidate.OAuth2,
			candidate.AWSV4,
			candidate.Hawk,
		} {
			for _, entry := range entries {
				if credential.SensitiveName(entry.Key) {
					recordReferences(entry.Value)
				}
			}
		}
		for _, entry := range candidate.APIKey {
			if strings.EqualFold(strings.TrimSpace(entry.Key), "value") {
				recordReferences(entry.Value)
			}
		}
	}
	effectiveURL := rawURL(item.Request.URL)
	collectPostmanCredentialURLReferences(effectiveURL, recordReferences)
	for _, match := range variablePattern.FindAllStringSubmatch(effectiveURL, -1) {
		if len(match) != 2 {
			continue
		}
		name := normalizeVariableName(match[1])
		sourceVariable, ok := pipeline.sourceVariables[name]
		if !ok {
			continue
		}
		_, projection, err := credentialpolicy.ProjectRawURL(
			sourceVariable.Value,
			credentialpolicy.Options{
				Mode:        credentialpolicy.SecretReference,
				Source:      sourceVariable.Source,
				RequestKey:  "postman_source_review",
				TargetField: "url",
			},
		)
		if err == nil && (len(projection.Requirements) > 0 ||
			len(projection.Findings) > 0) {
			pipeline.sourceReviewNames[name] = struct{}{}
		}
	}
	if item.Request.Body == nil {
		return
	}
	recordBody := func(value string) {
		if value == "" {
			return
		}
		if redacted := scrub.RedactCredentialBody(value); redacted != value {
			pipeline.sourceReviewBodies[value] = redacted
		}
	}
	recordBody(item.Request.Body.Raw)
	collectPostmanCredentialBodyReferences(
		item.Request.Body.Raw,
		postmanRequestContentType(item.Request.Header),
		recordReferences,
	)
	for _, entry := range item.Request.Body.URLEncoded {
		if !entry.Disabled && credential.SensitiveName(entry.Key) {
			recordReferences(entry.Value)
		}
	}
	for _, entry := range item.Request.Body.FormData {
		if !entry.Disabled && credential.SensitiveName(entry.Key) {
			recordReferences(entry.Value)
		}
	}
	if item.Request.Body.GraphQL != nil {
		recordBody(item.Request.Body.GraphQL.Variables)
		collectPostmanCredentialBodyReferences(
			item.Request.Body.GraphQL.Variables,
			"application/json",
			recordReferences,
		)
	}
}

func postmanRequestContentType(headers []header) string {
	for _, entry := range headers {
		if !entry.Disabled && strings.EqualFold(strings.TrimSpace(entry.Key), "Content-Type") {
			return entry.Value
		}
	}
	return ""
}

// collectPostmanCredentialURLReferences associates neutral source-variable
// names with credential-bearing URL components. Preserve mode intentionally
// emits no binding requirement, so this source provenance is what lets the
// public SourceReview hide the declaration value without changing the
// executable target.
func collectPostmanCredentialURLReferences(raw string, record func(string)) {
	if strings.TrimSpace(raw) == "" || record == nil {
		return
	}
	authorityStart := 0
	if scheme := strings.Index(raw, "://"); scheme >= 0 {
		authorityStart = scheme + 3
	} else if strings.HasPrefix(raw, "//") {
		authorityStart = 2
	}
	authorityEnd := len(raw)
	if end := strings.IndexAny(raw[authorityStart:], "/?#"); end >= 0 {
		authorityEnd = authorityStart + end
	}
	if at := strings.LastIndex(raw[authorityStart:authorityEnd], "@"); at >= 0 {
		record(raw[authorityStart : authorityStart+at])
	}

	queryStart := strings.IndexByte(raw, '?')
	if queryStart >= 0 {
		queryEnd := len(raw)
		if fragment := strings.IndexByte(raw[queryStart+1:], '#'); fragment >= 0 {
			queryEnd = queryStart + 1 + fragment
		}
		for _, pair := range strings.Split(raw[queryStart+1:queryEnd], "&") {
			name, value, _ := strings.Cut(pair, "=")
			decodedName, err := url.QueryUnescape(name)
			if err == nil && credential.SensitiveName(decodedName) {
				if decodedValue, decodeErr := url.QueryUnescape(value); decodeErr == nil {
					record(decodedValue)
				} else {
					record(value)
				}
			}
		}
	}
	if fragment := strings.IndexByte(raw, '#'); fragment >= 0 {
		// URL fragments are never transported by HTTP, but signed/auth callback
		// material is commonly placed there in authored collections. Mask any
		// referenced declaration in the review copy conservatively.
		record(raw[fragment+1:])
	}
}

func collectPostmanCredentialBodyReferences(
	raw string,
	contentType string,
	record func(string),
) {
	if strings.TrimSpace(raw) == "" || record == nil {
		return
	}
	trimmed := strings.TrimSpace(raw)
	mediaType, _, _ := mime.ParseMediaType(strings.TrimSpace(contentType))
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	switch {
	case strings.Contains(mediaType, "json"),
		strings.HasPrefix(trimmed, "{"),
		strings.HasPrefix(trimmed, "["):
		collectPostmanJSONCredentialReferences([]byte(raw), record)
	case strings.Contains(mediaType, "xml"), strings.HasPrefix(trimmed, "<"):
		collectPostmanXMLCredentialReferences([]byte(raw), record)
	case mediaType == "application/x-www-form-urlencoded":
		collectPostmanFormCredentialReferences(raw, record)
		return
	}
	// Duplicate keys and malformed declared structured content cannot always be
	// associated with one decoded node. If the bounded disclosure scrubber
	// recognizes credential structure, mask every referenced declaration rather
	// than risk retaining a value from a collapsed/ambiguous occurrence.
	if scrub.RedactCredentialBody(raw) != raw {
		record(raw)
	}
}

func collectPostmanJSONCredentialReferences(payload []byte, record func(string)) bool {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return false
	}
	var walk func(any, string, bool)
	walk = func(node any, name string, inheritedSensitive bool) {
		sensitive := inheritedSensitive || credential.SensitiveName(name)
		switch typed := node.(type) {
		case map[string]any:
			for key, child := range typed {
				walk(child, key, sensitive)
			}
		case []any:
			for _, child := range typed {
				walk(child, name, sensitive)
			}
		case string:
			if sensitive {
				record(typed)
			}
		}
	}
	walk(value, "", false)
	return true
}

func collectPostmanXMLCredentialReferences(payload []byte, record func(string)) bool {
	decoder := xml.NewDecoder(bytes.NewReader(payload))
	sensitiveDepth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if sensitiveDepth > 0 || credential.SensitiveName(typed.Name.Local) {
				sensitiveDepth++
			}
			for _, attr := range typed.Attr {
				if sensitiveDepth > 0 || credential.SensitiveName(attr.Name.Local) {
					record(attr.Value)
				}
			}
		case xml.CharData:
			if sensitiveDepth > 0 {
				record(string(typed))
			}
		case xml.EndElement:
			if sensitiveDepth > 0 {
				sensitiveDepth--
			}
		}
	}
}

func collectPostmanFormCredentialReferences(raw string, record func(string)) {
	for _, pair := range strings.Split(raw, "&") {
		name, value, _ := strings.Cut(pair, "=")
		decodedName, err := url.QueryUnescape(name)
		if err == nil && credential.SensitiveName(decodedName) {
			if decodedValue, decodeErr := url.QueryUnescape(value); decodeErr == nil {
				record(decodedValue)
			} else {
				record(value)
			}
		}
	}
}

func (pipeline *postmanRequestPipeline) redactSourceReviewBodies(
	sources map[string]string,
) bool {
	if pipeline == nil || len(sources) == 0 || len(pipeline.sourceReviewBodies) == 0 {
		return false
	}
	changed := false
	for name, source := range sources {
		redactedSource := source
		for raw, redacted := range pipeline.sourceReviewBodies {
			rawJSON, rawErr := json.Marshal(raw)
			redactedJSON, redactedErr := json.Marshal(redacted)
			if rawErr == nil && redactedErr == nil {
				redactedSource = strings.ReplaceAll(
					redactedSource,
					string(rawJSON),
					string(redactedJSON),
				)
			}
			redactedSource = strings.ReplaceAll(redactedSource, raw, redacted)
		}
		if redactedSource != source {
			sources[name] = redactedSource
			changed = true
		}
	}
	return changed
}

func (pipeline *postmanRequestPipeline) FinalizeScript(script *dsl.Script) (*postmanFinalization, error) {
	if pipeline == nil {
		return nil, fmt.Errorf("Postman request pipeline is required")
	}
	result, err := requestimport.FinalizeScript(script, pipeline.reports, requestimport.FinalizeOptions{})
	if err != nil {
		return nil, err
	}
	protectedRequirements := credentialpolicy.CloneRequirements(result.Requirements)
	reports := disclosureSafePostmanReports(pipeline.reports)
	result.Requirements = disclosureSafePostmanRequirements(result.Requirements)
	finalization := &postmanFinalization{
		Reports: reports,
		Result:  result,
	}
	finalization.protectedRequirements.Store(protectedRequirements)
	return finalization, nil
}

func (finalization *postmanFinalization) takeProtectedRequirements() []credentialpolicy.Requirement {
	if finalization == nil {
		return nil
	}
	return finalization.protectedRequirements.Take()
}

func disclosureSafePostmanReports(input []requestimport.Report) []requestimport.Report {
	if len(input) == 0 {
		return nil
	}
	out := make([]requestimport.Report, len(input))
	for index := range input {
		out[index] = input[index]
		out[index].Findings = append([]importreview.Finding(nil), input[index].Findings...)
		out[index].Requirements = disclosureSafePostmanRequirements(input[index].Requirements)
	}
	return out
}

func disclosureSafePostmanRequirements(input []credentialpolicy.Requirement) []credentialpolicy.Requirement {
	out := credentialpolicy.CloneRequirements(input)
	for index := range out {
		out[index].Value = ""
		out[index].SourceValueAvailable = false
	}
	return out
}

func postmanEffectiveRequest(index int, item requestItem, action dsl.Action) requestimport.EffectiveRequest {
	headers := make(requestimport.HeaderList, 0, len(action.HeaderList))
	for headerIndex, header := range action.HeaderList {
		headers = append(headers, requestimport.Header{
			Name:  header.Name,
			Value: header.Value,
			Source: requestimport.SourceRef{
				Format:  "postman",
				Path:    "postman_collection.json",
				Pointer: fmt.Sprintf("/requests/%d/headers/%d", index, headerIndex),
				ItemID:  item.Name,
			},
		})
	}
	if len(headers) == 0 && len(action.Headers) > 0 {
		headers = requestimport.HeaderListFromMap(action.Headers)
	}
	source := requestimport.SourceRef{
		Format:  "postman",
		Path:    "postman_collection.json",
		Pointer: fmt.Sprintf("/requests/%d", index),
		ItemID:  item.Name,
	}
	body := requestimport.Body{
		Kind:     postmanBodyKind(item.Request.Body, action),
		Payload:  []byte(action.Body),
		Encoding: requestimport.BodyEncodingText,
		Source:   source,
	}
	if body.Kind != requestimport.BodyNone {
		body.MediaType = headerValue(action.Headers, "Content-Type")
	}
	return requestimport.EffectiveRequest{
		Key:      fmt.Sprintf("postman_request_%06d_%s", index+1, action.Name),
		Name:     action.Name,
		Method:   action.Method,
		Target:   action.Target,
		Headers:  headers,
		Body:     body,
		Auth:     postmanEffectiveAuth(action.Auth, source),
		Metadata: action.Metadata,
		Source:   source,
	}
}

func postmanBodyKind(source *body, action dsl.Action) requestimport.BodyKind {
	if source == nil || action.Body == "" {
		return requestimport.BodyNone
	}
	switch strings.ToLower(strings.TrimSpace(source.Mode)) {
	case "urlencoded":
		return requestimport.BodyFormURLEncoded
	case "formdata":
		return requestimport.BodyMultipart
	case "graphql":
		return requestimport.BodyGraphQL
	case "", "raw":
		mediaType := strings.TrimSpace(headerValue(action.Headers, "Content-Type"))
		if parsed, _, err := mime.ParseMediaType(mediaType); err == nil {
			switch {
			case strings.EqualFold(parsed, "application/json"), strings.HasSuffix(strings.ToLower(parsed), "+json"):
				return requestimport.BodyJSON
			case strings.EqualFold(parsed, "application/xml"), strings.EqualFold(parsed, "text/xml"), strings.HasSuffix(strings.ToLower(parsed), "+xml"):
				return requestimport.BodyXML
			}
		}
		return requestimport.BodyRaw
	default:
		return requestimport.BodyRaw
	}
}

func postmanEffectiveAuth(auth *dsl.AuthConfig, source requestimport.SourceRef) requestimport.Auth {
	if auth == nil {
		return requestimport.Auth{Mode: requestimport.AuthUnspecified, Source: source}
	}
	effective := requestimport.Auth{
		Username: auth.Username,
		Password: auth.Password,
		Token:    auth.Token,
		Domain:   auth.Domain,
		Source:   source,
	}
	switch strings.ToLower(strings.TrimSpace(auth.Type)) {
	case "", "inherit":
		effective.Mode = requestimport.AuthInherit
	case "none", "noauth":
		effective.Mode = requestimport.AuthNone
	case "basic":
		effective.Mode = requestimport.AuthBasic
	case "bearer":
		effective.Mode = requestimport.AuthBearer
	case "digest":
		effective.Mode = requestimport.AuthDigest
	default:
		effective.Mode = requestimport.AuthUnsupported
		effective.SourceMode = auth.Type
	}
	return effective
}

type postmanTemplateShelter struct {
	headers  map[string]string
	password string
	token    string
}

func shelterPostmanTemplates(
	request *requestimport.EffectiveRequest,
	provenVariables map[string]struct{},
	sourceVariables map[string]credentialpolicy.SourceVariable,
) postmanTemplateShelter {
	var shelter postmanTemplateShelter
	if request == nil {
		return shelter
	}
	headerValues := make(map[string]struct{}, len(request.Headers))
	for _, header := range request.Headers {
		headerValues[header.Value] = struct{}{}
	}
	for index := range request.Headers {
		if !postmanCredentialTemplateIsSafeToPreserve(
			request.Headers[index].Value,
			provenVariables,
			sourceVariables,
		) {
			continue
		}
		if shelter.headers == nil {
			shelter.headers = make(map[string]string)
		}
		placeholder := postmanShelteredHeaderPlaceholder(index, headerValues)
		shelter.headers[placeholder] = request.Headers[index].Value
		request.Headers[index].Value = placeholder
		headerValues[placeholder] = struct{}{}
	}
	if postmanCredentialTemplateIsSafeToPreserve(request.Auth.Password, provenVariables, sourceVariables) {
		shelter.password = request.Auth.Password
		request.Auth.Password = "{{env.POSTMAN_AUTH_PASSWORD_TEMPLATE}}"
	}
	if postmanCredentialTemplateIsSafeToPreserve(request.Auth.Token, provenVariables, sourceVariables) {
		shelter.token = request.Auth.Token
		request.Auth.Token = "{{env.POSTMAN_AUTH_TOKEN_TEMPLATE}}"
	}
	return shelter
}

func postmanShelteredHeaderPlaceholder(index int, reserved map[string]struct{}) string {
	for suffix := 0; ; suffix++ {
		placeholder := fmt.Sprintf("{{env.APEX_POSTMAN_SHELTER_%d_%d}}", index+1, suffix+1)
		if _, exists := reserved[placeholder]; !exists {
			return placeholder
		}
	}
}

var (
	postmanWholeTemplatePattern = regexp.MustCompile(`^\s*\{\{[^{}]+\}\}\s*$`)
	postmanAuthWrapperPattern   = regexp.MustCompile(`(?i)^\s*(?:bearer|basic|digest)\s+\{\{[^{}]+\}\}\s*$`)
	postmanDynamicCookiePattern = regexp.MustCompile(`^\s*[A-Za-z0-9_.-]+\s*=\s*__APEX_DYNAMIC__(?:\s*;\s*[A-Za-z0-9_.-]+\s*=\s*__APEX_DYNAMIC__)*\s*$`)
	postmanApexVariablePattern  = regexp.MustCompile(`\{\{\s*var\.([A-Za-z0-9_.-]+)\s*\}\}`)
)

func postmanCredentialTemplateIsSafeToPreserve(
	value string,
	provenVariables map[string]struct{},
	sourceVariableSets ...map[string]credentialpolicy.SourceVariable,
) bool {
	var sourceVariables map[string]credentialpolicy.SourceVariable
	if len(sourceVariableSets) > 0 {
		sourceVariables = sourceVariableSets[0]
	}
	if !containsTemplateToken(value) {
		return false
	}
	if postmanWholeTemplatePattern.MatchString(value) || postmanAuthWrapperPattern.MatchString(value) {
		// Preserve unresolved or already-governed templates for compatibility,
		// but never shelter a known source literal merely because its variable
		// name looks innocuous. Prior response provenance wins at the current
		// action because the runtime extractor has replaced the static seed.
		for _, match := range postmanApexVariablePattern.FindAllStringSubmatch(value, -1) {
			if len(match) != 2 {
				continue
			}
			if _, dynamic := provenVariables[match[1]]; dynamic {
				continue
			}
			if _, sourceLiteral := sourceVariables[match[1]]; sourceLiteral {
				return false
			}
		}
		return true
	}
	replaced := postmanApexVariablePattern.ReplaceAllStringFunc(value, func(token string) string {
		match := postmanApexVariablePattern.FindStringSubmatch(token)
		if len(match) != 2 {
			return token
		}
		if _, proven := provenVariables[match[1]]; !proven {
			return token
		}
		return "__APEX_DYNAMIC__"
	})
	// Cookie pairs that consist solely of proven response-derived values retain
	// their source wire wrapper. Any literal prefix/suffix causes the complete
	// credential value to be governed by the shared projector.
	return postmanDynamicCookiePattern.MatchString(replaced)
}

func clonePostmanVariableSet(input map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(input))
	for name := range input {
		out[name] = struct{}{}
	}
	return out
}

func restorePostmanTemplates(action *dsl.Action, source dsl.Action, shelter postmanTemplateShelter) {
	if action == nil {
		return
	}
	for index := range action.HeaderList {
		if value, ok := shelter.headers[action.HeaderList[index].Value]; ok {
			action.HeaderList[index].Value = value
		}
	}
	if len(action.HeaderList) > 0 {
		action.Headers = make(map[string]string, len(action.HeaderList))
		for _, header := range action.HeaderList {
			setHeaderSummary(action.Headers, header.Name, header.Value)
		}
		if len(action.Headers) == 0 {
			action.Headers = nil
		}
	}
	if action.Auth != nil {
		if shelter.password != "" {
			action.Auth.Password = shelter.password
		}
		if shelter.token != "" {
			action.Auth.Token = shelter.token
		}
		if source.Auth != nil && strings.EqualFold(strings.TrimSpace(source.Auth.Type), "noauth") {
			action.Auth.Type = source.Auth.Type
		}
	}
}

func parsePostmanMultipart(payload []byte, boundary string, source requestimport.SourceRef) (credentialpolicy.Multipart, error) {
	boundary = strings.TrimSpace(boundary)
	if boundary == "" {
		return credentialpolicy.Multipart{}, fmt.Errorf("Postman multipart boundary is required")
	}
	text := string(payload)
	segments, err := postmanMultipartSegments(text, boundary)
	if err != nil {
		return credentialpolicy.Multipart{}, err
	}
	parts := make([]credentialpolicy.MultipartPart, 0, len(segments))
	for segmentIndex, segment := range segments {
		headerBlock, bodyText, found := strings.Cut(segment, "\r\n\r\n")
		if !found {
			return credentialpolicy.Multipart{}, fmt.Errorf("Postman multipart part %d has no header boundary", segmentIndex)
		}
		partSource := source
		partSource.Pointer = fmt.Sprintf("%s/body/formdata/%d", strings.TrimSuffix(source.Pointer, "/"), segmentIndex)
		part := credentialpolicy.MultipartPart{
			Body:   []byte(bodyText),
			Source: partSource,
		}
		for headerIndex, line := range strings.Split(headerBlock, "\r\n") {
			name, value, ok := strings.Cut(line, ":")
			if !ok || strings.TrimSpace(name) == "" {
				return credentialpolicy.Multipart{}, fmt.Errorf("Postman multipart part %d header %d is invalid", segmentIndex, headerIndex)
			}
			entry := credentialpolicy.HeaderOccurrence{
				Name:   strings.TrimSpace(name),
				Value:  strings.TrimSpace(value),
				Source: partSource,
			}
			part.Headers = append(part.Headers, entry)
			switch {
			case strings.EqualFold(entry.Name, "Content-Disposition"):
				_, params, err := mime.ParseMediaType(entry.Value)
				if err != nil {
					return credentialpolicy.Multipart{}, fmt.Errorf("Postman multipart part %d content disposition is invalid", segmentIndex)
				}
				part.Name = params["name"]
				part.FileName = params["filename"]
			case strings.EqualFold(entry.Name, "Content-Type"):
				part.ContentType = entry.Value
			}
		}
		parts = append(parts, part)
	}
	return credentialpolicy.Multipart{Boundary: boundary, Parts: parts}, nil
}

func postmanMultipartSegments(payload, boundary string) ([]string, error) {
	delimiter := "--" + boundary
	if payload == delimiter+"--\r\n" {
		return nil, nil
	}
	opening := delimiter + "\r\n"
	if !strings.HasPrefix(payload, opening) {
		return nil, fmt.Errorf("Postman multipart payload does not match its boundary")
	}

	marker := "\r\n" + delimiter
	cursor := len(opening)
	search := cursor
	segments := make([]string, 0)
	for {
		relative := strings.Index(payload[search:], marker)
		if relative < 0 {
			return nil, fmt.Errorf("Postman multipart payload does not match its boundary")
		}
		boundaryStart := search + relative
		afterMarker := boundaryStart + len(marker)
		switch {
		case strings.HasPrefix(payload[afterMarker:], "\r\n"):
			segments = append(segments, payload[cursor:boundaryStart])
			cursor = afterMarker + len("\r\n")
			search = cursor
		case strings.HasPrefix(payload[afterMarker:], "--\r\n"):
			if afterMarker+len("--\r\n") != len(payload) {
				return nil, fmt.Errorf("Postman multipart payload has bytes after its closing boundary")
			}
			segments = append(segments, payload[cursor:boundaryStart])
			return segments, nil
		default:
			// The boundary bytes may legitimately occur in a part body. They
			// delimit a part only as a complete MIME boundary line.
			search = afterMarker
		}
	}
}

var postmanWholeFileTemplatePattern = regexp.MustCompile(`^\{\{\s*file\.([^{}\s]+)\s*\}\}$`)

func postmanEffectiveMultipart(input credentialpolicy.Multipart) *requestimport.MultipartBody {
	output := &requestimport.MultipartBody{
		Boundary: input.Boundary,
		Parts:    make([]requestimport.MultipartPart, 0, len(input.Parts)),
	}
	for _, sourcePart := range input.Parts {
		part := requestimport.MultipartPart{
			Name:        sourcePart.Name,
			FileName:    sourcePart.FileName,
			ContentType: sourcePart.ContentType,
			Source:      sourcePart.Source,
			Headers:     make(requestimport.HeaderList, 0, len(sourcePart.Headers)),
		}
		for _, header := range sourcePart.Headers {
			part.Headers = append(part.Headers, requestimport.Header{
				Name: header.Name, Value: header.Value, Source: header.Source,
			})
		}
		if match := postmanWholeFileTemplatePattern.FindSubmatch(sourcePart.Body); len(match) == 2 {
			part.FileAssetRef = string(match[1])
		} else {
			part.Data = append([]byte(nil), sourcePart.Body...)
		}
		output.Parts = append(output.Parts, part)
	}
	return output
}

func scrubPostmanSourceReview(review *sourceview.Review) {
	if review == nil {
		return
	}
	for index := range review.SourceFiles {
		content := review.SourceFiles[index].Content
		redacted := scrub.Value(content, scrub.Policy{})
		if redacted == content {
			continue
		}
		review.SourceFiles[index].Content = redacted
		review.SourceFiles[index].Redacted = true
	}
}

func maskPostmanProtectedSourceValues(sources map[string]string, requirements []credentialpolicy.Requirement) bool {
	if len(sources) == 0 || len(requirements) == 0 {
		return false
	}
	values := make([]string, 0, len(requirements))
	seen := make(map[string]struct{}, len(requirements))
	for _, requirement := range requirements {
		value := requirement.Value
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return maskPostmanLiteralSourceValues(sources, values)
}

func maskPostmanSourceVariableValues(
	sources map[string]string,
	names []string,
	variables map[string]credentialpolicy.SourceVariable,
) bool {
	if len(sources) == 0 || len(names) == 0 || len(variables) == 0 {
		return false
	}
	values := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		sourceVariable, ok := variables[name]
		if !ok || strings.TrimSpace(sourceVariable.Value) == "" {
			continue
		}
		if _, duplicate := seen[sourceVariable.Value]; duplicate {
			continue
		}
		seen[sourceVariable.Value] = struct{}{}
		values = append(values, sourceVariable.Value)
	}
	return maskPostmanLiteralSourceValues(sources, values)
}

func maskPostmanLiteralSourceValues(sources map[string]string, values []string) bool {
	if len(sources) == 0 || len(values) == 0 {
		return false
	}
	nonempty := values[:0]
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		nonempty = append(nonempty, value)
	}
	values = nonempty
	sort.SliceStable(values, func(left, right int) bool {
		if len(values[left]) == len(values[right]) {
			return values[left] < values[right]
		}
		return len(values[left]) > len(values[right])
	})
	changed := false
	for name, source := range sources {
		redacted := source
		for _, value := range values {
			redacted = strings.ReplaceAll(redacted, value, scrub.Redacted)
		}
		redacted, _ = scrub.RedactQuotedValues(redacted, values)
		if redacted != source {
			sources[name] = redacted
			changed = true
		}
	}
	return changed
}

func markPostmanSourceReviewRedacted(review *sourceview.Review) {
	if review == nil {
		return
	}
	for index := range review.SourceFiles {
		review.SourceFiles[index].Redacted = true
	}
}

func disclosureSafePostmanEventSource(source string) string {
	lines := strings.Split(source, "\n")
	for index := range lines {
		if strings.TrimSpace(lines[index]) != "" {
			lines[index] = "// [SOURCE SCRIPT OMITTED FROM PUBLIC IMPORT REVIEW]"
		}
	}
	return strings.Join(lines, "\n")
}
