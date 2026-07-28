package requestimport

import (
	"mime"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	"golang.org/x/net/http/httpguts"
)

// sanitizeCredentialSourceWire validates transient, importer-proven source
// literals before a secret reference can hide an invalid wire shape. Invalid
// values remain byte-exact in a structurally valid blocked draft so an
// importing operator never has to re-enter a credential merely to review or
// repair the request.
func sanitizeCredentialSourceWire(
	request EffectiveRequest,
	projected EffectiveRequest,
	options credentialpolicy.Options,
) (EffectiveRequest, []runtimeMultipartOmission, []importreview.Finding) {
	var findings []importreview.Finding

	if !validSourceURLForProjection(projected.Target, options) {
		if options.Mode != credentialpolicy.Preserve {
			projected.Target = "/"
		}
		detail := "the source URL contained bytes that cannot be represented safely on the HTTP wire and was replaced by an inert target"
		if options.Mode == credentialpolicy.Preserve {
			detail = "the source URL cannot be represented safely on the HTTP wire; its exact imported value was retained in a blocked draft"
		}
		findings = append(findings, sourceWireReviewFinding(
			request,
			request.Source,
			"invalid_url_wire_value",
			"HTTP target",
			detail,
		))
	}
	if strings.TrimSpace(projected.BaseURL) != "" &&
		!validSourceURLForProjection(projected.BaseURL, options) {
		if options.Mode != credentialpolicy.Preserve {
			projected.BaseURL = ""
		}
		detail := "the source base URL contained bytes that cannot be represented safely on the HTTP wire and was omitted"
		if options.Mode == credentialpolicy.Preserve {
			detail = "the source base URL cannot be represented safely on the HTTP wire; its exact imported value was retained in a blocked draft"
		}
		findings = append(findings, sourceWireReviewFinding(
			request,
			chooseRequestSource(projected.BaseURLSource, request.Source),
			"invalid_url_wire_value",
			"HTTP base URL",
			detail,
		))
	}

	projected, authFindings := sanitizeCredentialSourceAuth(request, projected, options)
	findings = append(findings, authFindings...)
	multipartOmissions, multipartFindings := sanitizeCredentialSourceMultipart(request, &projected.Body, options)
	findings = append(findings, multipartFindings...)
	return projected, multipartOmissions, importreview.NormalizeFindings(findings)
}

func validSourceURLForProjection(raw string, options credentialpolicy.Options) bool {
	return credentialpolicy.ValidRawURL(raw, options)
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
	if options.Mode != credentialpolicy.Preserve {
		projected.Auth = Auth{
			Mode:       AuthNone,
			SourceMode: auth.SourceMode,
			Source:     source,
		}
		if projected.AuthConfig != nil {
			projected.AuthConfig = &dsl.AuthConfig{Type: httpprotocol.AuthTypeNone}
		}
	}
	detail := "the source authentication value cannot be represented safely on the HTTP wire and was replaced by explicit no-auth"
	if options.Mode == credentialpolicy.Preserve {
		detail = "the source authentication value cannot be represented safely on the HTTP wire; its exact imported fields were retained in a blocked draft"
	}
	return projected, []importreview.Finding{sourceWireReviewFinding(
		request,
		source,
		"invalid_auth_wire_value",
		"HTTP authentication",
		detail,
	)}
}

func sanitizeCredentialSourceMultipart(
	request EffectiveRequest,
	body *Body,
	options credentialpolicy.Options,
) ([]runtimeMultipartOmission, []importreview.Finding) {
	if body == nil || body.Kind != BodyMultipart || body.Multipart == nil {
		return nil, nil
	}
	var omissions []runtimeMultipartOmission
	var findings []importreview.Finding
	for partIndex := range body.Multipart.Parts {
		part := &body.Multipart.Parts[partIndex]
		headers := make(HeaderList, 0, len(part.Headers))
		var dispositionNames []string
		unsafeDisposition := false
		for headerIndex, header := range part.Headers {
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
			omissionField := "header"
			if strings.EqualFold(strings.TrimSpace(header.Name), "Content-Disposition") {
				omissionField = "content_disposition"
			}
			omissions = append(omissions, runtimeMultipartOmission{
				PartIndex:      partIndex,
				HeaderIndex:    headerIndex,
				Field:          omissionField,
				ReasonCode:     reason,
				Source:         source,
				sourceName:     header.Name,
				sourceValue:    header.Value,
				hasSourceName:  options.Mode == credentialpolicy.Preserve,
				hasSourceValue: options.Mode == credentialpolicy.Preserve,
			})
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
				omissions = append(omissions, runtimeMultipartOmission{
					PartIndex:      partIndex,
					HeaderIndex:    -1,
					Field:          "content_type",
					ReasonCode:     "invalid_multipart_header_value",
					Source:         chooseRequestSource(part.Source, request.Source),
					sourceValue:    part.ContentType,
					hasSourceValue: options.Mode == credentialpolicy.Preserve,
				})
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
	return omissions, importreview.NormalizeFindings(findings)
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
