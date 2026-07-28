package k6

import (
	"encoding/json"
	"fmt"
	"mime"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// finalizeK6HTTPRequest is the source-adapter boundary: JavaScript recognition
// has already happened, and only the effective static request enters the shared
// request/security/finalization pipeline.
func finalizeK6HTTPRequest(parsed parsedHTTPRequest, index int, target, body, sourceFile string, registry *credentialpolicy.NameRegistry) (dsl.Action, requestimport.Report, error) {
	actionName := fmt.Sprintf("%s_%02d", titleMethod(parsed.Method), index+1)
	source := credentialpolicy.SourceRef{
		Format:  "k6",
		Path:    sourceFile,
		Pointer: fmt.Sprintf("lines/%d-%d", parsed.StartLine, parsed.EndLine),
	}
	var findings []importreview.Finding
	location := source.Path + "#" + source.Pointer
	headers, headerDiagnostic := requestimport.HeaderListFromMapWithDiagnostic(parsed.Headers, location)
	if headerDiagnostic.Detail != "" {
		findings = append(findings, importreview.Finding{
			Code:       "source_order_unavailable",
			Severity:   headerDiagnostic.Severity,
			Element:    headerDiagnostic.Element,
			Detail:     headerDiagnostic.Detail,
			Location:   headerDiagnostic.Location,
			Source:     "k6",
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
		if strings.ContainsAny(headers[headerIndex].Value, "\r\n") {
			findings = append(findings, importreview.Finding{
				Code:     "k6_invalid_header_value",
				Severity: diag.Blocker,
				Element:  headers[headerIndex].Name,
				Detail:   "the static JavaScript header contained a line break and cannot be sent by the native HTTP runtime",
				Location: location,
				Gate:     importreview.GateExecution,
			})
		}
	}
	bodyKind := k6RequestBodyKind(body, parsed.Headers)
	effective := requestimport.EffectiveRequest{
		Key:     actionName,
		Name:    actionName,
		Method:  parsed.Method,
		Target:  target,
		Headers: headers,
		Body: requestimport.Body{
			Kind:     bodyKind,
			Payload:  []byte(body),
			Encoding: requestimport.BodyEncodingText,
			Source: credentialpolicy.SourceRef{
				Format:  source.Format,
				Path:    source.Path,
				Pointer: source.Pointer + "/body",
			},
		},
		Metadata: map[string]any{
			"k6_source": "http." + strings.ToLower(parsed.Method),
		},
		Source: source,
	}
	action, report, err := requestimport.FinalizeAction(effective, requestimport.FinalizeOptions{
		Credentials: &requestimport.CredentialProjectionOptions{
			Mode:       credentialpolicy.Preserve,
			Registry:   registry,
			NamePrefix: "K6",
		},
		Overlay:  requestimport.ActionOverlay{Type: "request"},
		Findings: findings,
	})
	if err != nil {
		return dsl.Action{}, report, fmt.Errorf("finalize k6 request at %s: %w", source.Pointer, err)
	}
	return action, report, nil
}

func k6RequestBodyKind(body string, headers map[string]string) requestimport.BodyKind {
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

func unresolvedK6CredentialBindings(reports []requestimport.Report) int {
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

func k6ExecutionFindingCodes(findings []importreview.Finding) []string {
	var codes []string
	for _, finding := range findings {
		if finding.Gate == importreview.GateExecution || finding.Severity == diag.Blocker {
			codes = append(codes, finding.Code)
		}
	}
	return uniqueSortedK6Strings(codes)
}

func publicK6CredentialRequirements(requirements []credentialpolicy.Requirement) []credentialpolicy.Requirement {
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

func redactK6CredentialValues(
	content string,
	requirements []credentialpolicy.Requirement,
	requests []parsedHTTPRequest,
) (string, bool) {
	values := make([]string, 0, len(requirements)+len(requests)*2)
	for _, requirement := range requirements {
		if value := strings.TrimSpace(requirement.Value); value != "" {
			values = append(values, value)
		}
	}
	for _, request := range requests {
		if request.URL != "" && scrub.ValueURL(request.URL, scrub.Policy{}) != request.URL {
			// Redact the complete authored URL scalar. This covers URL userinfo
			// and preserves encoded query spelling without trying to splice a
			// decoded value into JavaScript source.
			values = append(values, request.URL)
		}
		for name, value := range request.Headers {
			if credential.SensitiveHeader(name) && value != "" {
				values = append(values, value)
			}
		}
		if request.Body != "" && scrub.Value(request.Body, scrub.Policy{}) != request.Body {
			// A JavaScript string may contain escaped JSON/XML/form data. The
			// quoted-value redactor below recognizes the decoded body and
			// removes the whole source scalar while retaining line positions.
			values = append(values, request.Body)
		}
	}
	sort.SliceStable(values, func(i, j int) bool {
		if len(values[i]) == len(values[j]) {
			return values[i] < values[j]
		}
		return len(values[i]) > len(values[j])
	})
	changed := false
	for _, value := range values {
		next := strings.ReplaceAll(content, value, credential.Redacted)
		changed = changed || next != content
		content = next
	}
	content, quotedChanged := scrub.RedactQuotedValues(content, values)
	return content, changed || quotedChanged
}
