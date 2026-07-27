package devweb

import (
	"encoding/json"
	"fmt"
	"mime"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// finalizeDevWebHTTPRequest is the source-adapter boundary: DevWeb's static
// JavaScript recognition has already produced an effective request, and only
// that request enters the shared request/security/finalization pipeline.
//
// Before this existed the converter emitted authored header values verbatim, so
// an "Authorization: Bearer <token>" in a DevWeb script became a literal in the
// executable draft. The API response boundary masked it to [REDACTED] on the
// way out, which left the operator with a redaction and no migratable value —
// they had to retype the credential. Routing through the shared projector
// governs it into {{secret.*}} and carries the source value in the protected
// channel instead, matching every other HTTP importer.
func finalizeDevWebHTTPRequest(
	action dsl.Action,
	index int,
	sourceFile string,
	registry *credentialpolicy.NameRegistry,
	provenVars map[string]struct{},
	trustedValues map[string]struct{},
) (dsl.Action, requestimport.Report, error) {
	source := credentialpolicy.SourceRef{
		Format:  "devweb",
		Path:    sourceFile,
		Pointer: fmt.Sprintf("actions/%d", index),
	}
	location := source.Path + "#" + source.Pointer

	var findings []importreview.Finding
	headers, headerDiagnostic := requestimport.HeaderListFromMapWithDiagnostic(action.Headers, location)
	if headerDiagnostic.Detail != "" {
		findings = append(findings, importreview.Finding{
			Code:       "source_order_unavailable",
			Severity:   headerDiagnostic.Severity,
			Element:    headerDiagnostic.Element,
			Detail:     headerDiagnostic.Detail,
			Location:   headerDiagnostic.Location,
			Source:     "devweb",
			ReasonCode: "source_order_unavailable",
			SourceRef:  location,
		})
	}
	for headerIndex := range headers {
		headers[headerIndex].Source = credentialpolicy.SourceRef{
			Format:  source.Format,
			Path:    source.Path,
			Pointer: fmt.Sprintf("%s/headers/%d", source.Pointer, headerIndex),
		}
	}

	effective := requestimport.EffectiveRequest{
		Key:     action.Name,
		Name:    action.Name,
		Method:  action.Method,
		Target:  action.Target,
		Headers: headers,
		Body: requestimport.Body{
			Kind:     devWebRequestBodyKind(action.Body, action.Headers),
			Payload:  []byte(action.Body),
			Encoding: requestimport.BodyEncodingText,
			Source: credentialpolicy.SourceRef{
				Format:  source.Format,
				Path:    source.Path,
				Pointer: source.Pointer + "/body",
			},
		},
		Metadata: action.Metadata,
		Source:   source,
	}

	finalized, report, err := requestimport.FinalizeAction(effective, requestimport.FinalizeOptions{
		Credentials: &requestimport.CredentialProjectionOptions{
			Mode:       credentialpolicy.SecretReference,
			Registry:   registry,
			NamePrefix: "DEVWEB",
			// Variables a DevWeb extractor produces are response-derived, not
			// authored credentials. Without this a correlated "Basic {{var.token}}"
			// header would be governed into a secret binding the operator cannot
			// satisfy, because the credential never existed as a literal.
			ProvenVars: provenVars,
			// {{data.*}} / {{param.*}} references the converter emitted from DevWeb
			// parameter mappings are runtime data bindings, not authored secrets.
			TrustedWholeValues: trustedValues,
		},
		Overlay:  requestimport.ActionOverlay{Type: "request"},
		Findings: findings,
	})
	if err != nil {
		return dsl.Action{}, report, fmt.Errorf("finalize devweb request at %s: %w", source.Pointer, err)
	}
	// The shared finalizer owns the request surface (target, headers, auth,
	// body) and nothing else. Everything the DevWeb adapter derived from the
	// source script — extractors, textCheck assertions, think time — is
	// converter state and is carried over unchanged.
	finalized.Extractors = action.Extractors
	finalized.Assertions = action.Assertions
	finalized.ThinkTime = action.ThinkTime
	finalized.ThinkTimeAfter = action.ThinkTimeAfter
	finalized.Value = action.Value
	return finalized, report, nil
}

func devWebRequestBodyKind(body string, headers map[string]string) requestimport.BodyKind {
	if strings.TrimSpace(body) == "" {
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

// publicDevWebCredentialRequirements strips source values from the requirements
// that ride in the public conversion payload.
func publicDevWebCredentialRequirements(requirements []credentialpolicy.Requirement) []credentialpolicy.Requirement {
	if len(requirements) == 0 {
		return nil
	}
	public := credentialpolicy.CloneRequirements(requirements)
	for index := range public {
		public[index].Value = ""
		public[index].DeferredSourceExpression = ""
	}
	return public
}

// devWebExtractorVariables returns every variable name a DevWeb extractor
// produces anywhere in the script. The converter only emits {{var.X}} where it
// recognized a "<request>.extractors.X" reference, so a reference and its
// producing extractor always exist together — including on the same request,
// which is how DevWeb scripts carry a token across iterations.
func devWebExtractorVariables(actions []dsl.Action) map[string]struct{} {
	proven := make(map[string]struct{})
	for _, action := range actions {
		for _, extractor := range action.Extractors {
			if name := strings.TrimSpace(extractor.Name); name != "" {
				proven[name] = struct{}{}
			}
		}
	}
	if len(proven) == 0 {
		return nil
	}
	return proven
}

// devWebHTTPRequestAction reports whether an action is an HTTP request the
// shared pipeline can finalize. Review-only markers and non-HTTP protocol steps
// are excluded: they carry no request surface to project.
func devWebHTTPRequestAction(action dsl.Action) bool {
	switch strings.ToLower(strings.TrimSpace(action.Type)) {
	case "request", "http_request":
		return true
	}
	return false
}

// devWebRuntimeDataReferences collects the {{data.*}} and {{param.*}} whole
// references the converter emitted while mapping DevWeb parameters. They are
// trusted by construction: the converter only produces them from a recognized
// load.params/parameter-file mapping, never from authored literal text.
func devWebRuntimeDataReferences(actions []dsl.Action) map[string]struct{} {
	trusted := make(map[string]struct{})
	collect := func(value string) {
		for _, match := range devWebRuntimeReferencePattern.FindAllString(value, -1) {
			trusted[match] = struct{}{}
		}
	}
	for _, action := range actions {
		collect(action.Target)
		collect(action.Body)
		for _, value := range action.Headers {
			collect(value)
		}
		for _, header := range action.HeaderList {
			collect(header.Value)
		}
	}
	if len(trusted) == 0 {
		return nil
	}
	return trusted
}

var devWebRuntimeReferencePattern = regexp.MustCompile(`\{\{\s*(?:data|param)\.[A-Za-z0-9_.-]+\s*\}\}`)

// redactDevWebCredentialValues removes discovered credential values from the
// source text that rides in the public source review.
//
// The shared scrubber only recognizes credential names it knows, so a
// vendor-prefixed header such as "X-Api-Key" survives its patterns even though
// the projector governed that header in the executable script. Redacting
// against the values the projector actually found closes the gap without
// guessing at names. Longest-first so a value that contains another is replaced
// before its substring.
func redactDevWebCredentialValues(content string, requirements []credentialpolicy.Requirement) string {
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
	content, _ = scrub.RedactQuotedValues(content, values)
	return content
}
