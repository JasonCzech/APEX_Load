package requestimport

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// CredentialProjectionOptions enables the shared credential policy at the
// effective-request boundary. A nil *CredentialProjectionOptions preserves a
// source adapter's established credential projection during staged migration.
type CredentialProjectionOptions struct {
	Mode     credentialpolicy.ProjectionMode
	Registry *credentialpolicy.NameRegistry
	Limits   credentialpolicy.Limits
	// SensitiveNames carries source-schema credential annotations for fields
	// whose names alone are not governed by the shared vocabulary.
	SensitiveNames map[string]string
	// SensitiveWholeBody marks a schema-declared credential whose complete
	// request body is the value (for example text/plain with format: password).
	// Preserve mode retains the exact wire bytes and adds a runtime-redaction
	// locator even when the payload itself contains no credential-shaped name.
	SensitiveWholeBody bool
	ProvenVars         map[string]struct{}
	// SourceVariables carries importer-proven source literals behind translated
	// {{var.*}} references. The shared policy consults it only on credential
	// surfaces; it is never serialized into the request or report.
	SourceVariables map[string]credentialpolicy.SourceVariable
	NamePrefix      string
	// PreGovernedHeaderOccurrences preserves exact ordered header occurrences
	// that a source adapter has already assembled exclusively from governed
	// runtime references. It never exempts URL, auth, or body components.
	PreGovernedHeaderOccurrences map[int]struct{}
	// TrustedWholeValues contains exact scalar values the adapter emitted itself
	// from a recognized runtime reference — a {{data.*}} data-source binding or a
	// {{param.*}} parameter it created. They are executable runtime data, not
	// declarations of a project secret: governing one would collapse a
	// per-iteration data binding into a single static value. Only whole-value
	// matches qualify, so a credential composed around such a reference still
	// governs. This forwards the field the script-level projector already uses
	// for LoadRunner parameter files.
	TrustedWholeValues map[string]struct{}
}

// CredentialProjectionError means the shared policy could not construct a
// disclosure-safe valid draft. Error text contains stable codes and source
// locations only.
type CredentialProjectionError struct {
	Codes []string
}

// CredentialProjectionReview is the safe bridge from the dependency-free
// credential projector into durable import findings. Requirement.Value remains
// available only to protected-session callers and is excluded from JSON.
type CredentialProjectionReview struct {
	Requirements []credentialpolicy.Requirement
	Findings     []importreview.Finding
	Disposition  Disposition
	Rejected     bool
}

func (err *CredentialProjectionError) Error() string {
	if err == nil || len(err.Codes) == 0 {
		return "credential projection rejected the imported request"
	}
	return "credential projection rejected the imported request: " + strings.Join(err.Codes, ", ")
}

// CredentialReport converts a structured credential projector result into the
// request-import report contract. Source adapters use this when they retain a
// richer source representation (for example ordered multipart parts) than the
// wire-byte Body currently carries.
func CredentialReport(requestKey string, result credentialpolicy.Result) Report {
	return Report{
		RequestKey:       strings.TrimSpace(requestKey),
		Disposition:      requestDisposition(result.Disposition),
		Findings:         projectionFindings(result.Findings),
		Requirements:     credentialpolicy.CloneRequirements(result.Requirements),
		preservedTargets: append([]credentialpolicy.TargetRef(nil), result.PreservedTargets...),
	}
}

// ProjectCredentials applies URL, ordered-header, typed-auth, and structured
// body credential policy to a fully resolved request.
func ProjectCredentials(request EffectiveRequest, options CredentialProjectionOptions) (EffectiveRequest, Report, error) {
	projected := cloneEffectiveRequest(request)
	projected.Auth = effectiveAuth(projected)
	if options.Registry == nil {
		// Every request surface must share one allocator. Letting each projector
		// normalize a nil registry independently can assign the same
		// {{secret.NAME}} to different requirement IDs and source values.
		options.Registry = credentialpolicy.NewNameRegistry()
	}
	policyOptions := credentialpolicy.Options{
		Mode:                         options.Mode,
		Source:                       request.Source,
		RequestKey:                   request.Key,
		NamePrefix:                   options.NamePrefix,
		Registry:                     options.Registry,
		Limits:                       options.Limits,
		SensitiveNames:               options.SensitiveNames,
		ProvenVars:                   options.ProvenVars,
		SourceVariables:              options.SourceVariables,
		PreGovernedHeaderOccurrences: options.PreGovernedHeaderOccurrences,
		TrustedWholeValues:           options.TrustedWholeValues,
	}
	sourceHeaders, sourceRuntimeFidelity, sourceFindings := sanitizeCredentialSourceHeaders(
		request,
		projected.Headers,
		policyOptions,
	)
	projected.Headers = sourceHeaders
	policyOptions.PreGovernedHeaderOccurrences = remapHeaderOccurrenceSet(
		policyOptions.PreGovernedHeaderOccurrences,
		sourceRuntimeFidelity.HeaderOmissions,
	)
	projected, multipartOmissions, sourceWireFindings := sanitizeCredentialSourceWire(
		request,
		projected,
		policyOptions,
	)
	sourceRuntimeFidelity.MultipartOmissions = multipartOmissions
	sourceRuntimeFidelity.PreserveMultipartBytes = options.Mode == credentialpolicy.Preserve
	sourceFindings = append(sourceFindings, sourceWireFindings...)
	sourceFindings = importreview.NormalizeFindings(sourceFindings)
	sourceReport := Report{
		RequestKey:      request.Key,
		Disposition:     dispositionForFindings(sourceFindings),
		Findings:        sourceFindings,
		runtimeFidelity: sourceRuntimeFidelity,
	}
	rejectedReport := func() Report {
		report := sourceReport
		report.Disposition = Rejected
		return report
	}
	aggregate := credentialpolicy.Result{Disposition: credentialpolicy.ExecutableExact}

	target, result, err := credentialpolicy.ProjectRawURL(projected.Target, policyOptions)
	if err != nil {
		return EffectiveRequest{}, rejectedReport(), err
	}
	if result.Disposition != credentialpolicy.Rejected {
		projected.Target = target
	}
	aggregate.Merge(result)
	if strings.TrimSpace(projected.BaseURL) != "" {
		baseOptions := policyOptions
		baseOptions.Source = chooseRequestSource(projected.BaseURLSource, request.Source)
		baseOptions.TargetField = "base_url"
		baseURL, baseResult, baseErr := credentialpolicy.ProjectRawURL(projected.BaseURL, baseOptions)
		if baseErr != nil {
			return EffectiveRequest{}, rejectedReport(), baseErr
		}
		if baseResult.Disposition != credentialpolicy.Rejected {
			projected.BaseURL = baseURL
		}
		// EffectiveRequest.BaseURL becomes script.config.base_url; it is not an
		// action field. FinalizeScript projects that executable config surface
		// and stores its locators at script scope. Attaching these locators to
		// the request action would make runtime redaction dereference the action
		// target instead of the actual base URL.
		baseResult.PreservedTargets = nil
		aggregate.Merge(baseResult)
	}

	headerInput := make([]credentialpolicy.HeaderOccurrence, len(projected.Headers))
	for index, header := range projected.Headers {
		headerInput[index] = credentialpolicy.HeaderOccurrence{
			Name: header.Name, Value: header.Value, Source: header.Source,
		}
	}
	headerOutput, result, err := credentialpolicy.ProjectHeaders(headerInput, policyOptions)
	if err != nil {
		return EffectiveRequest{}, rejectedReport(), err
	}
	for index := range headerOutput {
		projected.Headers[index].Name = headerOutput[index].Name
		projected.Headers[index].Value = headerOutput[index].Value
		projected.Headers[index].Source = headerOutput[index].Source
	}
	aggregate.Merge(result)

	authOutput, result, err := credentialpolicy.ProjectAuth(credentialpolicy.AuthFields{
		Type:     string(projected.Auth.Mode),
		Username: projected.Auth.Username,
		Password: projected.Auth.Password,
		Token:    projected.Auth.Token,
		Domain:   projected.Auth.Domain,
		Source:   projected.Auth.Source,
	}, policyOptions)
	if err != nil {
		return EffectiveRequest{}, rejectedReport(), err
	}
	if result.Disposition != credentialpolicy.Rejected {
		projected.Auth.Username = authOutput.Username
		projected.Auth.Password = authOutput.Password
		projected.Auth.Token = authOutput.Token
		projected.Auth.Domain = authOutput.Domain
		if projected.AuthConfig != nil {
			projected.AuthConfig.Username = authOutput.Username
			projected.AuthConfig.Password = authOutput.Password
			projected.AuthConfig.Token = authOutput.Token
			projected.AuthConfig.Domain = authOutput.Domain
		}
	}
	aggregate.Merge(result)

	bodyOptions := policyOptions
	bodyOptions.Source = chooseRequestSource(projected.Body.Source, request.Source)
	bodyOptions.TargetField = "body"
	structuredMultipart := projected.Body.Kind == BodyMultipart && projected.Body.Multipart != nil
	structuredGraphQL := projected.Body.Kind == BodyGraphQL && projected.Body.GraphQL != nil
	if structuredMultipart {
		if strings.TrimSpace(projected.Body.Multipart.Boundary) == "" {
			mediaBoundary, boundaryErr := multipartMediaBoundary(projected.Body.MediaType)
			if boundaryErr != nil {
				return EffectiveRequest{}, rejectedReport(), boundaryErr
			}
			projected.Body.Multipart.Boundary = mediaBoundary
			if mediaBoundary == "" {
				for attempt := 0; ; attempt++ {
					boundary := deterministicMultipartBoundary(
						requestIdentity(projected),
						projected.Body.Multipart.Parts,
						attempt,
					)
					if !multipartBoundaryOccurs(boundary, projected.Body.Multipart.Parts) {
						projected.Body.Multipart.Boundary = boundary
						break
					}
				}
			}
		}
		input := credentialMultipart(projected.Body.Multipart)
		output, bodyResult, bodyErr := credentialpolicy.ProjectMultipart(input, bodyOptions)
		if bodyErr != nil {
			return EffectiveRequest{}, rejectedReport(), bodyErr
		}
		aggregate.Merge(bodyResult)
		if bodyResult.Disposition != credentialpolicy.Rejected {
			applyCredentialMultipart(projected.Body.Multipart, output)
		}
	}
	if structuredGraphQL {
		graphQLOptions := bodyOptions
		graphQLOptions.TargetField = "graphql"
		input := credentialpolicy.GraphQL{
			Query:     []byte(projected.Body.GraphQL.Query),
			Variables: append([]byte(nil), projected.Body.GraphQL.Variables...),
		}
		output, bodyResult, bodyErr := credentialpolicy.ProjectGraphQL(input, graphQLOptions)
		if bodyErr != nil {
			return EffectiveRequest{}, rejectedReport(), bodyErr
		}
		aggregate.Merge(bodyResult)
		if bodyResult.Disposition != credentialpolicy.Rejected {
			projected.Body.GraphQL.Query = string(output.Query)
			projected.Body.GraphQL.Variables = append([]byte(nil), output.Variables...)
		}
		if len(projected.Body.GraphQL.Extensions) > 0 {
			extensionOptions := bodyOptions
			extensionOptions.TargetField = "graphql.extensions"
			extensions, extensionResult, extensionErr := credentialpolicy.ProjectJSON(
				projected.Body.GraphQL.Extensions,
				extensionOptions,
			)
			if extensionErr != nil {
				return EffectiveRequest{}, rejectedReport(), extensionErr
			}
			aggregate.Merge(extensionResult)
			if extensionResult.Disposition != credentialpolicy.Rejected {
				projected.Body.GraphQL.Extensions = extensions
			}
		}
	}
	projected, err = materializeEffectiveRequest(projected)
	if err != nil {
		return EffectiveRequest{}, rejectedReport(), err
	}
	switch projected.Body.Kind {
	case BodyJSON:
		payload, bodyResult, bodyErr := credentialpolicy.ProjectJSON(projected.Body.Payload, bodyOptions)
		if bodyErr != nil {
			return EffectiveRequest{}, rejectedReport(), bodyErr
		}
		if bodyResult.Disposition != credentialpolicy.Rejected && payload != nil {
			projected.Body.Payload = payload
		}
		aggregate.Merge(bodyResult)
	case BodyXML:
		payload, bodyResult, bodyErr := credentialpolicy.ProjectXML(projected.Body.Payload, bodyOptions)
		if bodyErr != nil {
			return EffectiveRequest{}, rejectedReport(), bodyErr
		}
		if bodyResult.Disposition != credentialpolicy.Rejected && payload != nil {
			projected.Body.Payload = payload
		}
		aggregate.Merge(bodyResult)
	case BodyFormURLEncoded:
		payload, _, bodyResult, bodyErr := credentialpolicy.ProjectFormURLEncoded(string(projected.Body.Payload), bodyOptions)
		if bodyErr != nil {
			return EffectiveRequest{}, rejectedReport(), bodyErr
		}
		if bodyResult.Disposition != credentialpolicy.Rejected {
			projected.Body.Payload = []byte(payload)
		}
		aggregate.Merge(bodyResult)
	case BodyGraphQL:
		// A GraphQL request encoded as ordinary JSON still benefits from the
		// shared body dispatcher, while a source adapter with separated query
		// and variables fields is handled above.
		if !structuredGraphQL && len(projected.Body.Payload) > 0 {
			payload, bodyResult, bodyErr := credentialpolicy.ProjectRequestBody(
				projected.Body.Payload,
				projected.Body.MediaType,
				bodyOptions,
			)
			if bodyErr != nil {
				return EffectiveRequest{}, rejectedReport(), bodyErr
			}
			if bodyResult.Disposition != credentialpolicy.Rejected && payload != nil {
				projected.Body.Payload = payload
			}
			aggregate.Merge(bodyResult)
		}
	case BodyMultipart, BodyRaw, BodyFile:
		// Some source adapters retain authoritative wire bytes instead of a
		// structured multipart/GraphQL model. Dispatch those bytes through the
		// same bounded codecs so a misleading media type or neutral wrapper
		// cannot hide a credential-bearing structured payload.
		if !structuredMultipart && len(projected.Body.Payload) > 0 {
			payload, bodyResult, bodyErr := credentialpolicy.ProjectRequestBody(
				projected.Body.Payload,
				projected.Body.MediaType,
				bodyOptions,
			)
			if bodyErr != nil {
				return EffectiveRequest{}, rejectedReport(), bodyErr
			}
			if bodyResult.Disposition != credentialpolicy.Rejected && payload != nil {
				projected.Body.Payload = payload
			}
			aggregate.Merge(bodyResult)
		}
	}
	if options.SensitiveWholeBody && len(projected.Body.Payload) > 0 {
		bodyCredential := credentialpolicy.Result{
			Disposition: credentialpolicy.ExecutableExact,
			Findings: []credentialpolicy.Finding{{
				Code:        "import_credential_preserved",
				Source:      bodyOptions.Source,
				Disposition: credentialpolicy.ExecutableExact,
			}},
		}
		if options.Mode != credentialpolicy.Preserve {
			bodyCredential.Disposition = credentialpolicy.Rejected
			bodyCredential.Findings[0].Code =
				"credential_sensitive_whole_body_unsupported"
			bodyCredential.Findings[0].Disposition = credentialpolicy.Rejected
		} else {
			field := "body.opaque"
			if projected.Body.Kind == BodyJSON {
				// The runtime JSON locator resolves the root scalar to its
				// decoded value, so an error that echoes the password without
				// JSON quoting is still redacted.
				field = "body.json"
			}
			bodyCredential.PreservedTargets = []credentialpolicy.TargetRef{{
				RequestKey: request.Key,
				Field:      field,
			}}
		}
		aggregate.Merge(bodyCredential)
	}
	if options.Mode == credentialpolicy.Preserve &&
		projected.Body.Encoding == BodyEncodingText &&
		!utf8.Valid(projected.Body.Payload) {
		// The DSL is JSON and therefore cannot round-trip invalid UTF-8 in a
		// text field. Base64 is the DSL storage representation of the exact
		// executable bytes; it does not change what the HTTP runtime sends.
		projected.Body.Encoding = BodyEncodingBase64
	}

	review := ReviewCredentialProjection(aggregate, "imported credential")
	findings := append(append([]importreview.Finding(nil), sourceFindings...), review.Findings...)
	findings = importreview.NormalizeFindings(findings)
	report := Report{
		RequestKey:      request.Key,
		Disposition:     worseRequestDisposition(dispositionForFindings(sourceFindings), review.Disposition),
		Findings:        findings,
		Requirements:    review.Requirements,
		runtimeFidelity: sourceRuntimeFidelity,
		preservedTargets: append(
			[]credentialpolicy.TargetRef(nil),
			aggregate.PreservedTargets...,
		),
	}
	if review.Rejected {
		codes := make([]string, 0, len(aggregate.Findings))
		for _, finding := range aggregate.Findings {
			if finding.Disposition == credentialpolicy.Rejected {
				codes = append(codes, finding.Code)
			}
		}
		return EffectiveRequest{}, report, &CredentialProjectionError{Codes: uniqueSorted(codes)}
	}
	return projected, report, nil
}

// ReviewCredentialProjection maps a projector result without including source
// values in details, locations, IDs, or serializable requirement fields.
func ReviewCredentialProjection(result credentialpolicy.Result, element string) CredentialProjectionReview {
	findings := projectionFindings(result.Findings)
	element = strings.TrimSpace(element)
	if element != "" {
		for index := range findings {
			findings[index].Element = element
		}
	}
	return CredentialProjectionReview{
		Requirements: credentialpolicy.CloneRequirements(result.Requirements),
		Findings:     importreview.NormalizeFindings(findings),
		Disposition:  requestDisposition(result.Disposition),
		Rejected:     result.Disposition == credentialpolicy.Rejected,
	}
}

func sanitizeCredentialSourceHeaders(
	request EffectiveRequest,
	headers HeaderList,
	options credentialpolicy.Options,
) (HeaderList, runtimeFidelityProjection, []importreview.Finding) {
	if len(headers) == 0 {
		return nil, runtimeFidelityProjection{}, nil
	}
	validationHeaders := append(HeaderList(nil), headers...)
	for index := range validationHeaders {
		// Validate importer-proven source literals, not merely their safe
		// {{var.*}} spelling. The materialized value is confined to this
		// transient validation copy and is never returned or reported.
		materialized, _ := credentialpolicy.MaterializeSourceVariables(
			validationHeaders[index].Value,
			options,
		)
		validationHeaders[index].Value = materialized
	}
	_, projection, findings := projectHeaderRuntimeFidelity(request, validationHeaders)
	projection.PreserveOmittedHeaderBytes = options.Mode == credentialpolicy.Preserve
	if !projection.PreserveOmittedHeaderBytes {
		for index := range projection.HeaderOmissions {
			projection.HeaderOmissions[index].sourceName = ""
			projection.HeaderOmissions[index].sourceValue = ""
			projection.HeaderOmissions[index].hasSourceHeader = false
		}
	}
	if len(projection.HeaderOmissions) == 0 {
		return append(HeaderList(nil), headers...), projection, findings
	}
	omitted := make(map[int]struct{}, len(projection.HeaderOmissions))
	for _, omission := range projection.HeaderOmissions {
		omitted[omission.SourceIndex] = struct{}{}
	}
	sanitized := make(HeaderList, 0, len(headers)-len(omitted))
	for index, header := range headers {
		if _, remove := omitted[index]; !remove {
			sanitized = append(sanitized, header)
		}
	}
	if len(sanitized) == 0 {
		sanitized = nil
	}
	return sanitized, projection, findings
}

func remapHeaderOccurrenceSet(
	input map[int]struct{},
	omissions []runtimeHeaderOmission,
) map[int]struct{} {
	if len(input) == 0 {
		return nil
	}
	omitted := make(map[int]struct{}, len(omissions))
	for _, omission := range omissions {
		omitted[omission.SourceIndex] = struct{}{}
	}
	output := make(map[int]struct{}, len(input))
	for index := range input {
		if _, removed := omitted[index]; removed {
			continue
		}
		output[index-omittedHeaderIndexesBefore(omitted, index)] = struct{}{}
	}
	if len(output) == 0 {
		return nil
	}
	return output
}

func cloneEffectiveRequest(request EffectiveRequest) EffectiveRequest {
	cloned := request
	cloned.Headers = append(HeaderList(nil), request.Headers...)
	cloned.Body.Payload = append([]byte(nil), request.Body.Payload...)
	cloned.Body.FormFields = append([]FormField(nil), request.Body.FormFields...)
	for index := range cloned.Body.FormFields {
		if request.Body.FormFields[index].HadEquals != nil {
			value := *request.Body.FormFields[index].HadEquals
			cloned.Body.FormFields[index].HadEquals = &value
		}
	}
	if request.Body.Multipart != nil {
		multipart := *request.Body.Multipart
		multipart.Parts = append([]MultipartPart(nil), request.Body.Multipart.Parts...)
		for index := range multipart.Parts {
			multipart.Parts[index].Headers = append(HeaderList(nil), request.Body.Multipart.Parts[index].Headers...)
			multipart.Parts[index].Data = append([]byte(nil), request.Body.Multipart.Parts[index].Data...)
		}
		cloned.Body.Multipart = &multipart
	}
	if request.Body.GraphQL != nil {
		graphQL := *request.Body.GraphQL
		graphQL.Variables = append([]byte(nil), request.Body.GraphQL.Variables...)
		graphQL.Extensions = append([]byte(nil), request.Body.GraphQL.Extensions...)
		cloned.Body.GraphQL = &graphQL
	}
	cloned.Body.Assets = append([]Asset(nil), request.Body.Assets...)
	for index := range cloned.Body.Assets {
		cloned.Body.Assets[index].Data = append([]byte(nil), request.Body.Assets[index].Data...)
	}
	if request.AuthConfig != nil {
		auth := *request.AuthConfig
		cloned.AuthConfig = &auth
	}
	cloned.Assertions = append([]dsl.Assertion(nil), request.Assertions...)
	cloned.Extractors = append([]dsl.Extractor(nil), request.Extractors...)
	cloned.SOAP = cloneSOAP(request.SOAP)
	cloned.MissingReferences = append([]MissingReference(nil), request.MissingReferences...)
	cloned.Bindings = append([]SourceBinding(nil), request.Bindings...)
	cloned.Diagnostics = append(diag.Diagnostics(nil), request.Diagnostics...)
	cloned.SourceMappings = append([]SourceMapping(nil), request.SourceMappings...)
	cloned.SourceRefs = append([]SourceRef(nil), request.SourceRefs...)
	cloned.Metadata = cloneAnyMap(request.Metadata)
	return cloned
}

func credentialMultipart(input *MultipartBody) credentialpolicy.Multipart {
	if input == nil {
		return credentialpolicy.Multipart{}
	}
	output := credentialpolicy.Multipart{
		Boundary: input.Boundary,
		Parts:    make([]credentialpolicy.MultipartPart, len(input.Parts)),
	}
	for index, part := range input.Parts {
		headers := make([]credentialpolicy.HeaderOccurrence, len(part.Headers))
		for headerIndex, header := range part.Headers {
			headers[headerIndex] = credentialpolicy.HeaderOccurrence{
				Name:   header.Name,
				Value:  header.Value,
				Source: header.Source,
			}
		}
		body := append([]byte(nil), part.Data...)
		if strings.TrimSpace(part.FileAssetRef) != "" {
			// The asset is a runtime binding, not source body bytes. Feeding its
			// {{file.*}} placeholder through scalar credential projection can
			// create a {{secret.*}} requirement that applyCredentialMultipart
			// cannot install without destroying the file binding.
			body = nil
		}
		output.Parts[index] = credentialpolicy.MultipartPart{
			Headers:     headers,
			Name:        part.Name,
			FileName:    part.FileName,
			ContentType: part.ContentType,
			Body:        body,
			Source:      part.Source,
		}
	}
	return output
}

func applyCredentialMultipart(target *MultipartBody, projected credentialpolicy.Multipart) {
	if target == nil {
		return
	}
	target.Boundary = projected.Boundary
	for index := range target.Parts {
		if index >= len(projected.Parts) {
			return
		}
		part := projected.Parts[index]
		headers := make(HeaderList, len(part.Headers))
		for headerIndex, header := range part.Headers {
			headers[headerIndex] = Header{
				Name:   header.Name,
				Value:  header.Value,
				Source: header.Source,
			}
		}
		target.Parts[index].Headers = headers
		// A file reference is already dynamic and must retain its asset binding
		// rather than being replaced by the projector's empty inspection body.
		if target.Parts[index].FileAssetRef == "" {
			target.Parts[index].Data = append([]byte(nil), part.Body...)
		}
	}
}

func projectionFindings(input []credentialpolicy.Finding) []importreview.Finding {
	out := make([]importreview.Finding, 0, len(input))
	for _, finding := range input {
		severity := diag.Warning
		gate := importreview.GateNone
		if finding.Disposition == credentialpolicy.BlockedDraft ||
			finding.Disposition == credentialpolicy.Rejected {
			severity = diag.Blocker
			gate = importreview.GateExecution
		}
		out = append(out, importreview.Finding{
			Code:       strings.TrimSpace(finding.Code),
			Severity:   severity,
			Element:    "imported credential",
			Detail:     projectionFindingDetail(finding),
			Location:   sourceLocation(finding.Source),
			Source:     strings.TrimSpace(finding.Source.Format),
			ReasonCode: strings.TrimSpace(finding.Code),
			SourceRef:  sourceLocation(finding.Source),
			Resolution: projectionFindingResolution(finding),
			Gate:       gate,
		})
	}
	return importreview.NormalizeFindings(out)
}

func projectionFindingResolution(finding credentialpolicy.Finding) string {
	switch finding.Code {
	case "import_credentials_unmanaged":
		return "project_credentials_and_remove_gate"
	case "import_credential_preserved", "import_credential_review_incomplete":
		return "review_preserved_credential"
	default:
		return "replace_behavior_and_remove_gate"
	}
}

func projectionFindingDetail(finding credentialpolicy.Finding) string {
	switch finding.Code {
	case "request_body_xml_json_content_type_mismatch":
		return importreview.XMLBodyJSONContentTypeDetail
	case "import_credential_preserved":
		return "the imported credential was retained in the runnable destination script and should be reviewed by the operator"
	case "import_credential_review_incomplete":
		return "the imported value was retained, but bounded credential classification could not inspect the complete value"
	}
	switch finding.Disposition {
	case credentialpolicy.Rejected:
		return "the credential-bearing source value could not be converted into a valid disclosure-safe draft"
	case credentialpolicy.BlockedDraft:
		return "the imported request is retained as a valid draft but requires review before execution"
	default:
		return "the credential-bearing source value was reviewed by the shared import policy"
	}
}

func requestDisposition(disposition credentialpolicy.Disposition) Disposition {
	switch disposition {
	case credentialpolicy.ExecutableBounded:
		return ExecutableBounded
	case credentialpolicy.BlockedDraft:
		return BlockedDraft
	case credentialpolicy.Rejected:
		return Rejected
	default:
		return ExecutableExact
	}
}

func worseRequestDisposition(left, right Disposition) Disposition {
	rank := func(value Disposition) int {
		switch value {
		case Rejected:
			return 4
		case BlockedDraft:
			return 3
		case ExecutableBounded:
			return 2
		case ExecutableExact:
			return 1
		default:
			return 0
		}
	}
	if rank(right) > rank(left) {
		return right
	}
	if left == "" {
		return ExecutableExact
	}
	return left
}

func chooseRequestSource(primary, fallback SourceRef) SourceRef {
	if primary.Format != "" || primary.Path != "" || primary.Pointer != "" || primary.ItemID != "" {
		return primary
	}
	return fallback
}

func looksLikeJSONObject(payload []byte) bool {
	for _, value := range payload {
		switch value {
		case ' ', '\t', '\r', '\n':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}

var errCredentialProjectionDisabled = errors.New("credential projection is disabled")

func credentialProjectionInvariant(report Report, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errCredentialProjectionDisabled) {
		return nil
	}
	if report.Disposition == Rejected {
		return err
	}
	return fmt.Errorf("credential projection invariant: %w", err)
}
