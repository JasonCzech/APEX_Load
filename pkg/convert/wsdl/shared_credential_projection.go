package wsdl

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/credential"
)

// finalizeWSDLXMLBody is the WSDL adapter around the shared, byte-preserving
// XML credential projector. WSDL remains responsible only for proving which
// exact skeleton templates are dynamic while preserving literal source XML
// verbatim.
func finalizeWSDLXMLBody(
	body []byte,
	options wsdlCredentialProjectionOptions,
) (
	[]byte,
	[]importreview.Finding,
	[]credentialpolicy.TargetRef,
	error,
) {
	if len(body) == 0 {
		return body, nil, nil, nil
	}
	source := credentialpolicy.SourceRef{
		Format:  "wsdl",
		Path:    options.SourcePath,
		Pointer: options.Pointer,
	}
	projected, projection, err := credentialpolicy.ProjectXML(body, credentialpolicy.Options{
		Mode:               credentialpolicy.Preserve,
		Source:             source,
		RequestKey:         options.RequestKey,
		NamePrefix:         "WSDL",
		Registry:           options.Registry,
		TrustedWholeValues: wsdlTrustedWholeRuntimeValues(body),
		TargetField:        "body",
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("shared XML credential projection failed: %w", err)
	}
	review := requestimport.ReviewCredentialProjection(projection, "WSDL SOAP XML")
	if review.Rejected {
		codes := make([]string, 0, len(projection.Findings))
		for _, finding := range projection.Findings {
			if finding.Disposition == credentialpolicy.Rejected {
				codes = append(codes, finding.Code)
			}
		}
		return nil, nil, nil, fmt.Errorf(
			"shared XML credential projection rejected generated SOAP XML: %s",
			strings.Join(uniqueSortedStrings(codes), ", "),
		)
	}

	hadPlaceholderSubstitution := false
	filteredFindings := make([]importreview.Finding, 0, len(review.Findings))
	for _, finding := range review.Findings {
		if finding.Code == "import_credential_source_unavailable" {
			hadPlaceholderSubstitution = true
			continue
		}
		filteredFindings = append(filteredFindings, finding)
	}
	if hadPlaceholderSubstitution {
		// Literal sentinel-looking values in WSDL schema defaults/fixed attributes
		// are present in the source XML and must remain verbatim rather than being
		// rewritten to {{secret.WSDL_*}} placeholders.
		projected = body
	}
	return projected, importreview.NormalizeFindings(filteredFindings), projection.PreservedTargets, nil
}

func wsdlTrustedWholeRuntimeValues(body []byte) map[string]struct{} {
	trusted := map[string]struct{}{
		credential.Redacted: {},
	}
	text := string(body)
	for cursor := 0; cursor < len(text); {
		startRelative := strings.Index(text[cursor:], "{{")
		if startRelative < 0 {
			break
		}
		start := cursor + startRelative
		endRelative := strings.Index(text[start+2:], "}}")
		if endRelative < 0 {
			break
		}
		end := start + 2 + endRelative + 2
		candidate := text[start:end]
		if isGovernedWSDLXMLValue(candidate) {
			trusted[strings.TrimSpace(candidate)] = struct{}{}
		}
		cursor = end
	}
	return trusted
}
