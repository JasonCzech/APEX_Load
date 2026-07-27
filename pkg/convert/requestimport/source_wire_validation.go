package requestimport

import (
	"mime"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	"golang.org/x/net/http/httpguts"
)

// sanitizeCredentialSourceWire validates transient, importer-proven source
// literals before a secret reference can hide an invalid wire shape. Every
// omission produces an inert, structurally valid blocked draft and no binding
// requirement for the omitted value.
func sanitizeCredentialSourceWire(
	request EffectiveRequest,
	projected EffectiveRequest,
	options credentialpolicy.Options,
) (EffectiveRequest, []importreview.Finding) {
	var findings []importreview.Finding

	if !validSourceURLForProjection(projected.Target, options) {
		projected.Target = "/"
		findings = append(findings, sourceWireReviewFinding(
			request,
			request.Source,
			"invalid_url_wire_value",
			"HTTP target",
			"the source URL contained bytes that cannot be represented safely on the HTTP wire and was replaced by an inert target",
		))
	}
	if strings.TrimSpace(projected.BaseURL) != "" &&
		!validSourceURLForProjection(projected.BaseURL, options) {
		projected.BaseURL = ""
		findings = append(findings, sourceWireReviewFinding(
			request,
			chooseRequestSource(projected.BaseURLSource, request.Source),
			"invalid_url_wire_value",
			"HTTP base URL",
			"the source base URL contained bytes that cannot be represented safely on the HTTP wire and was omitted",
		))
	}

	projected, authFindings := sanitizeCredentialSourceAuth(request, projected, options)
	findings = append(findings, authFindings...)
	multipartFindings := sanitizeCredentialSourceMultipart(request, &projected.Body, options)
	findings = append(findings, multipartFindings...)
	return projected, importreview.NormalizeFindings(findings)
}

func validSourceURLForProjection(raw string, options credentialpolicy.Options) bool {
	materialized, _ := credentialpolicy.MaterializeSourceVariables(raw, options)
	if !utf8.ValidString(materialized) {
		return false
	}
	parseable, ok := replaceSourceURLTemplates(materialized)
	if !ok {
		return false
	}
	if strings.IndexFunc(parseable, func(r rune) bool {
		return r < ' ' || r == 0x7f || r == ' '
	}) >= 0 {
		return false
	}
	parsed, err := url.Parse(parseable)
	if err != nil {
		return false
	}
	if parsed.Scheme == "" {
		// RFC 3986 network-path references still require an authority. Ordinary
		// relative request targets are valid and are resolved against BaseURL.
		return !strings.HasPrefix(parseable, "//") || parsed.Host != ""
	}
	// Go accepts opaque forms such as https:user@example.test. net/http cannot
	// replay those as hierarchical HTTP requests, and the credential projector
	// cannot identify their userinfo safely.
	if parsed.Opaque != "" || parsed.Host == "" {
		return false
	}
	if sourceURLSchemeIsDynamic(materialized) {
		return true
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return true
	default:
		return false
	}
}

func sourceURLSchemeIsDynamic(value string) bool {
	colon := strings.IndexByte(value, ':')
	if colon < 0 {
		return false
	}
	firstDelimiter := len(value)
	if delimiter := strings.IndexAny(value, "/?#"); delimiter >= 0 {
		firstDelimiter = delimiter
	}
	return colon < firstDelimiter && strings.Contains(value[:colon], "{{")
}

func replaceSourceURLTemplates(value string) (string, bool) {
	var output strings.Builder
	for position := 0; position < len(value); {
		start := strings.Index(value[position:], "{{")
		if start < 0 {
			remainder := value[position:]
			if strings.Contains(remainder, "}}") {
				return "", false
			}
			output.WriteString(remainder)
			break
		}
		start += position
		literal := value[position:start]
		if strings.Contains(literal, "}}") {
			return "", false
		}
		output.WriteString(literal)
		end := strings.Index(value[start+2:], "}}")
		if end < 0 {
			return "", false
		}
		end += start + 2
		expression := value[start+2 : end]
		if strings.TrimSpace(expression) == "" ||
			strings.Contains(expression, "{{") ||
			strings.IndexFunc(expression, func(r rune) bool {
				return r < ' ' || r == 0x7f
			}) >= 0 {
			return "", false
		}
		replacement := "apex"
		if sourceURLTemplateIsPort(value, start) {
			replacement = "443"
		}
		output.WriteString(replacement)
		position = end + 2
	}
	return output.String(), true
}

func sourceURLTemplateIsPort(value string, start int) bool {
	authorityStart := -1
	if scheme := strings.Index(value, "://"); scheme >= 0 {
		authorityStart = scheme + 3
	} else if strings.HasPrefix(value, "//") {
		authorityStart = 2
	}
	if authorityStart < 0 || start < authorityStart {
		return false
	}
	authorityEnd := len(value)
	if end := strings.IndexAny(value[authorityStart:], "/?#"); end >= 0 {
		authorityEnd = authorityStart + end
	}
	if start >= authorityEnd {
		return false
	}
	if at := strings.LastIndex(value[authorityStart:authorityEnd], "@"); at >= 0 &&
		start < authorityStart+at {
		return false
	}
	colon := strings.LastIndex(value[authorityStart:start], ":")
	if colon < 0 {
		return false
	}
	colon += authorityStart
	if bracket := strings.LastIndex(value[authorityStart:start], "]"); bracket >= 0 &&
		authorityStart+bracket > colon {
		return false
	}
	for _, current := range value[colon+1 : start] {
		if current < '0' || current > '9' {
			return false
		}
	}
	return true
}

func sanitizeCredentialSourceAuth(
	request EffectiveRequest,
	projected EffectiveRequest,
	options credentialpolicy.Options,
) (EffectiveRequest, []importreview.Finding) {
	auth := projected.Auth
	clearCredentialValues := func() {
		projected.Auth.Username = ""
		projected.Auth.Password = ""
		projected.Auth.Token = ""
		projected.Auth.Domain = ""
		if projected.AuthConfig != nil {
			projected.AuthConfig.Username = ""
			projected.AuthConfig.Password = ""
			projected.AuthConfig.Token = ""
			projected.AuthConfig.Domain = ""
		}
	}

	switch auth.Mode {
	case AuthBasic, AuthBearer, AuthDigest:
		// Continue below.
	case AuthUnsupported:
		// Review-only source auth still needs its password/token projected into
		// protected storage. projectRequest persists only the resulting runtime
		// references in non-executable review metadata so requirements retain a
		// concrete binding location while the action itself becomes no-auth.
		return projected, nil
	case AuthNone, AuthInherit, AuthUnspecified:
		// These values have no executable typed-auth target. Retaining or
		// projecting them would create a stale protected requirement.
		clearCredentialValues()
		return projected, nil
	default:
		clearCredentialValues()
		return projected, nil
	}

	materialized := &protocol.AuthConfig{
		Type:     string(auth.Mode),
		Username: materializeCredentialSourceValue(auth.Username, options),
		Password: materializeCredentialSourceValue(auth.Password, options),
		Token:    materializeCredentialSourceValue(auth.Token, options),
		Domain:   materializeCredentialSourceValue(auth.Domain, options),
	}
	if err := httpprotocol.ValidateAuthWireValues(materialized); err == nil {
		return projected, nil
	}

	source := chooseRequestSource(auth.Source, request.Source)
	projected.Auth = Auth{
		Mode:       AuthNone,
		SourceMode: auth.SourceMode,
		Source:     source,
	}
	if projected.AuthConfig != nil {
		projected.AuthConfig = &dsl.AuthConfig{Type: httpprotocol.AuthTypeNone}
	}
	return projected, []importreview.Finding{sourceWireReviewFinding(
		request,
		source,
		"invalid_auth_wire_value",
		"HTTP authentication",
		"the source authentication value cannot be represented safely on the HTTP wire and was replaced by explicit no-auth",
	)}
}

func sanitizeCredentialSourceMultipart(
	request EffectiveRequest,
	body *Body,
	options credentialpolicy.Options,
) []importreview.Finding {
	if body == nil || body.Kind != BodyMultipart || body.Multipart == nil {
		return nil
	}
	var findings []importreview.Finding
	for partIndex := range body.Multipart.Parts {
		part := &body.Multipart.Parts[partIndex]
		headers := make(HeaderList, 0, len(part.Headers))
		var dispositionNames []string
		unsafeDisposition := false
		for _, header := range part.Headers {
			source := chooseRequestSource(header.Source, chooseRequestSource(part.Source, request.Source))
			materialized := materializeCredentialSourceValue(header.Value, options)
			reason := ""
			switch {
			case !httpguts.ValidHeaderFieldName(header.Name):
				reason = "invalid_multipart_header_name"
			case !httpguts.ValidHeaderFieldValue(materialized):
				reason = "invalid_multipart_header_value"
			}
			if reason == "" && strings.EqualFold(strings.TrimSpace(header.Name), "Content-Disposition") {
				disposition, parameters, err := mime.ParseMediaType(materialized)
				if err != nil || strings.TrimSpace(disposition) == "" {
					reason = "invalid_multipart_content_disposition"
				} else if name := strings.TrimSpace(parameters["name"]); name != "" {
					dispositionNames = append(dispositionNames, name)
				}
			}
			if reason == "" {
				headers = append(headers, header)
				continue
			}
			if strings.EqualFold(strings.TrimSpace(header.Name), "Content-Disposition") {
				unsafeDisposition = true
			}
			findings = append(findings, sourceWireReviewFinding(
				request,
				source,
				reason,
				"multipart part header",
				"the source multipart header cannot be represented safely and was omitted from the blocked draft",
			))
		}
		part.Headers = headers
		forceGovernBody := unsafeDisposition
		if len(dispositionNames) > 0 {
			effectiveName := dispositionNames[len(dispositionNames)-1]
			allNames := append([]string{strings.TrimSpace(part.Name)}, dispositionNames...)
			conflict := false
			first := ""
			for _, name := range allNames {
				if name == "" {
					continue
				}
				if first == "" {
					first = name
				} else if name != first {
					conflict = true
				}
				if credential.SensitiveName(name) {
					// If any structured or authored spelling says the part is a
					// credential, govern the body even when another duplicate
					// spelling would otherwise win.
					effectiveName = name
				}
			}
			if strings.Contains(effectiveName, "{{") {
				// A dynamic wire name cannot prove the body nonsensitive. Force
				// conservative whole-value governance and retain a durable gate.
				conflict = true
			}
			if conflict {
				forceGovernBody = true
				findings = append(findings, sourceWireReviewFinding(
					request,
					chooseRequestSource(part.Source, request.Source),
					"multipart_name_conflict",
					"multipart part name",
					"the structured and wire multipart names conflict; the body was conservatively governed and requires review",
				))
			}
			if forceGovernBody {
				effectiveName = "password"
			}
			part.Name = effectiveName
		}
		if forceGovernBody && len(dispositionNames) == 0 {
			// Once the authoritative wire name is malformed, the source cannot
			// prove this body nonsensitive. Govern it as a whole value so the
			// blocked draft remains disclosure-safe.
			part.Name = "password"
		}
		if strings.TrimSpace(part.FileAssetRef) != "" &&
			(forceGovernBody || sourceMultipartSensitiveName(part.Name, options)) {
			// File assets are bound at execution time and have no protected raw
			// scalar for the credential store to replace. Projecting {{file.*}}
			// as a secret would create an orphan requirement while the body
			// encoder intentionally retained the file binding. Keep a valid,
			// inert file placeholder under a neutral name and require review.
			part.Name = "blocked_file_part_" + strconv.Itoa(partIndex+1)
			findings = append(findings, sourceWireReviewFinding(
				request,
				chooseRequestSource(part.Source, request.Source),
				"credential_multipart_sensitive_file_unsupported",
				"multipart file part",
				"a sensitive multipart file binding cannot be projected as a scalar credential and was retained only in a blocked draft",
			))
		}

		if part.ContentType != "" {
			materialized := materializeCredentialSourceValue(part.ContentType, options)
			if !httpguts.ValidHeaderFieldValue(materialized) {
				part.ContentType = ""
				findings = append(findings, sourceWireReviewFinding(
					request,
					chooseRequestSource(part.Source, request.Source),
					"invalid_multipart_header_value",
					"multipart part content type",
					"the source multipart content type cannot be represented safely and was omitted from the blocked draft",
				))
			}
		}

		// If an invalid Content-Disposition was the only source of the part name,
		// give the inert draft a deterministic neutral name so serialization stays
		// valid without retaining any source header bytes.
		if !part.Headers.Has("Content-Disposition") && strings.TrimSpace(part.Name) == "" {
			part.Name = "part_" + strconv.Itoa(partIndex+1)
		}
	}
	return importreview.NormalizeFindings(findings)
}

func sourceMultipartSensitiveName(name string, options credentialpolicy.Options) bool {
	if credential.SensitiveName(name) || credential.SensitiveXMLName(name) {
		return true
	}
	if len(options.SensitiveNames) == 0 {
		return false
	}
	if separator := strings.LastIndexByte(name, ':'); separator >= 0 {
		name = name[separator+1:]
	}
	_, governed := options.SensitiveNames[strings.ToLower(strings.TrimSpace(name))]
	return governed
}

func materializeCredentialSourceValue(value string, options credentialpolicy.Options) string {
	materialized, _ := credentialpolicy.MaterializeSourceVariables(value, options)
	return materialized
}

func sourceWireReviewFinding(
	request EffectiveRequest,
	source SourceRef,
	reason string,
	element string,
	detail string,
) importreview.Finding {
	finding := requestReviewFinding(request, reason, element, detail)
	finding.Location = sourceLocation(source)
	finding.SourceRef = sourceLocation(source)
	return finding
}
