package sample

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

func finalizeSampleRequestAction(
	action dsl.Action,
	ex exchange,
	actionPath string,
	location sampleSourceLocation,
	format string,
	registry *credentialpolicy.NameRegistry,
	provenVars map[string]struct{},
) (dsl.Action, requestimport.Report, error) {
	pointer := fmt.Sprintf("line:%d", location.StartLine)
	if format == "har" && ex.SourceEntryID > 0 {
		// A recorded HAR is often one line, so its entry names the request;
		// the line still focuses the source view of a formatted HAR file.
		pointer = fmt.Sprintf("HAR entry %d", ex.SourceEntryID)
		if location.StartLine > 1 {
			pointer += fmt.Sprintf(", line:%d", location.StartLine)
		}
	}
	source := requestimport.SourceRef{
		Format:  format,
		Path:    location.SourceFile,
		Pointer: pointer,
		ItemID:  action.Name,
	}
	headers := make(requestimport.HeaderList, 0, len(action.HeaderList))
	for _, header := range action.HeaderList {
		headers = append(headers, requestimport.Header{
			Name:   header.Name,
			Value:  header.Value,
			Source: source,
		})
	}
	var findings []importreview.Finding
	if len(headers) == 0 && len(action.Headers) != 0 {
		headers = requestimport.HeaderListFromMap(action.Headers)
		findings = append(findings, importreview.Finding{
			Code:     "source_order_unavailable",
			Severity: diag.Info,
			Element:  action.Name,
			Detail:   "the source exposed headers as a map; deterministic order was synthesized",
		})
	}
	findings = append(findings, sampleWireFidelityFindings(ex, format)...)

	body := requestimport.Body{}
	if action.Body != "" || action.BodyEncoding != "" {
		payload := []byte(action.Body)
		encoding := requestimport.BodyEncodingText
		if strings.EqualFold(strings.TrimSpace(action.BodyEncoding), "base64") {
			decoded, err := base64.StdEncoding.DecodeString(action.Body)
			if err != nil {
				return dsl.Action{}, requestimport.Report{}, fmt.Errorf("decode sample action body: %w", err)
			}
			payload = decoded
			encoding = requestimport.BodyEncodingBase64
		}
		body = requestimport.Body{
			Kind:     sampleRequestBodyKind(action),
			Payload:  payload,
			Encoding: encoding,
			Source:   source,
		}
	}
	mode := credentialpolicy.Preserve
	credentialOptions := &requestimport.CredentialProjectionOptions{
		Mode:       mode,
		Registry:   registry,
		ProvenVars: provenVars,
		NamePrefix: "SAMPLE",
	}

	effective := requestimport.EffectiveRequest{
		Key:         action.Name,
		Name:        action.Name,
		Method:      action.Method,
		Target:      action.Target,
		HTTPVersion: ex.HTTPVersion,
		Headers:     headers,
		Body:        body,
		Auth:        sampleEffectiveAuth(action.Auth),
		Metadata:    action.Metadata,
		Source:      source,
	}
	if action.TimeoutMs > 0 {
		effective.Timeout = requestimport.Timeout{
			Mode:         requestimport.TimeoutBounded,
			Milliseconds: action.TimeoutMs,
		}
	}
	if action.MaxRedirects != nil {
		if *action.MaxRedirects == 0 {
			effective.Redirects.Mode = requestimport.RedirectDisabled
		} else if *action.MaxRedirects < 0 {
			effective.Redirects.Mode = requestimport.RedirectUnlimited
		} else {
			effective.Redirects = requestimport.Redirects{
				Mode: requestimport.RedirectLimited,
				Max:  *action.MaxRedirects,
			}
		}
	}
	projected, report, err := requestimport.FinalizeAction(effective, requestimport.FinalizeOptions{
		Project:     requestimport.ProjectOptions{Bounds: requestimport.BoundsClampWithWarning},
		Credentials: credentialOptions,
		Overlay: requestimport.ActionOverlay{
			Type:           action.Type,
			Params:         action.Params,
			Metadata:       action.Metadata,
			Extractors:     action.Extractors,
			Assertions:     action.Assertions,
			ThinkTime:      action.ThinkTime,
			ThinkTimeAfter: action.ThinkTimeAfter,
			SOAP:           action.SOAP,
		},
		Findings:   findings,
		ActionPath: actionPath,
	})
	if err != nil {
		clearSampleRequirements(report.Requirements)
	}
	return projected, report, err
}

func sampleWireFidelityFindings(ex exchange, format string) []importreview.Finding {
	normalizedFormat := strings.ToLower(strings.TrimSpace(format))
	var findings []importreview.Finding
	if normalizedFormat == "curl" {
		reasons, _ := ex.Metadata[curlReviewReasonsMetadataKey].([]string)
		for _, reason := range reasons {
			reason = strings.TrimSpace(reason)
			if reason == "" {
				continue
			}
			findings = append(findings, importreview.Finding{
				Code:       reason,
				Severity:   diag.Blocker,
				Element:    "cURL request behavior",
				Detail:     "source cURL behavior was omitted or safely approximated and requires review before replay",
				ReasonCode: reason,
				Resolution: "replace_behavior_and_remove_gate",
				Gate:       importreview.GateExecution,
			})
		}
	}
	if normalizedFormat != "raw_http" {
		return importreview.NormalizeFindings(findings)
	}
	if version := strings.TrimSpace(ex.HTTPVersion); (version != "" &&
		httpprotocol.NormalizeVersion(version) != httpprotocol.VersionHTTP11) || ex.Metadata[rawHTTP3RequestLineKey] != nil {
		// Preserve the established sample-specific finding for clients that
		// key off it. Shared request finalization adds the generic durable
		// version-fidelity reason independently when the source mode cannot be
		// represented.
		findings = append(findings, importreview.Finding{
			Code:       "raw_http_version_review_required",
			Severity:   diag.Blocker,
			Element:    "raw HTTP version",
			Detail:     "the textual raw request does not preserve the source protocol framing and requires review",
			ReasonCode: "raw_http_version_review_required",
			Resolution: "replace_behavior_and_remove_gate",
			Gate:       importreview.GateExecution,
		})
	}
	if target, _ := ex.Metadata["raw_request_target"].(string); strings.TrimSpace(target) != "" {
		findings = append(findings, importreview.Finding{
			Code:     "raw_http_request_target_review_required",
			Severity: diag.Blocker,
			Element:  "raw HTTP request target",
			Detail:   "the source used a non-origin request-target form that the native HTTP runtime cannot reproduce exactly",
			Gate:     importreview.GateExecution,
		})
	}
	return importreview.NormalizeFindings(findings)
}

func rawHTTP11Only(exchanges []exchange) bool {
	if len(exchanges) == 0 {
		return false
	}
	for _, ex := range exchanges {
		if httpprotocol.NormalizeVersion(ex.HTTPVersion) != httpprotocol.VersionHTTP11 {
			return false
		}
	}
	return true
}

func sampleProvenVariables(replacements map[string]string) map[string]struct{} {
	if len(replacements) == 0 {
		return nil
	}
	const (
		prefix = "{{var."
		suffix = "}}"
	)
	proven := make(map[string]struct{}, len(replacements))
	for _, replacement := range replacements {
		replacement = strings.TrimSpace(replacement)
		if !strings.HasPrefix(replacement, prefix) || !strings.HasSuffix(replacement, suffix) {
			continue
		}
		name := strings.TrimSpace(replacement[len(prefix) : len(replacement)-len(suffix)])
		if name == "" || strings.ContainsAny(name, "{}") {
			continue
		}
		proven[name] = struct{}{}
	}
	if len(proven) == 0 {
		return nil
	}
	return proven
}

func clearSampleReportRequirementValues(reports []requestimport.Report) {
	for index := range reports {
		clearSampleRequirements(reports[index].Requirements)
	}
}

func clearSampleRequirements(requirements []credentialpolicy.Requirement) {
	for index := range requirements {
		requirements[index].Value = ""
	}
}

func sampleRequestBodyKind(action dsl.Action) requestimport.BodyKind {
	if action.Body == "" && action.BodyEncoding == "" {
		return requestimport.BodyNone
	}
	if action.SOAP != nil {
		return requestimport.BodyXML
	}
	contentType := ""
	for index := len(action.HeaderList) - 1; index >= 0; index-- {
		if strings.EqualFold(strings.TrimSpace(action.HeaderList[index].Name), "Content-Type") {
			contentType = strings.ToLower(action.HeaderList[index].Value)
			break
		}
	}
	if contentType == "" {
		for name, value := range action.Headers {
			if strings.EqualFold(strings.TrimSpace(name), "Content-Type") {
				contentType = strings.ToLower(value)
			}
		}
	}
	switch {
	case strings.Contains(contentType, "graphql"):
		return requestimport.BodyGraphQL
	case strings.Contains(contentType, "json"):
		return requestimport.BodyJSON
	case strings.Contains(contentType, "xml"):
		return requestimport.BodyXML
	case strings.Contains(contentType, "application/x-www-form-urlencoded"):
		return requestimport.BodyFormURLEncoded
	case strings.Contains(contentType, "multipart/form-data"):
		return requestimport.BodyMultipart
	default:
		trimmed := strings.TrimSpace(action.Body)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			return requestimport.BodyJSON
		}
		if strings.HasPrefix(trimmed, "<") {
			return requestimport.BodyXML
		}
		return requestimport.BodyRaw
	}
}

func sampleEffectiveAuth(auth *dsl.AuthConfig) requestimport.Auth {
	if auth == nil {
		return requestimport.Auth{Mode: requestimport.AuthInherit}
	}
	projected := requestimport.Auth{
		SourceMode: auth.Type,
		Username:   auth.Username,
		Password:   auth.Password,
		Token:      auth.Token,
		Domain:     auth.Domain,
	}
	switch strings.ToLower(strings.TrimSpace(auth.Type)) {
	case "", "inherit":
		projected.Mode = requestimport.AuthInherit
	case "none", "noauth":
		projected.Mode = requestimport.AuthNone
	case "basic":
		projected.Mode = requestimport.AuthBasic
	case "bearer":
		projected.Mode = requestimport.AuthBearer
	case "digest":
		projected.Mode = requestimport.AuthDigest
	default:
		projected.Mode = requestimport.AuthUnsupported
	}
	return projected
}
