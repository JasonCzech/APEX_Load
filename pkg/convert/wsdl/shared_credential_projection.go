package wsdl

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/credential"
)

// finalizeWSDLXMLBody is the WSDL adapter around the shared, byte-preserving
// XML credential projector. WSDL remains responsible only for proving which
// exact skeleton templates are dynamic and for retaining transient source
// spellings long enough to redact the source-review copy.
func finalizeWSDLXMLBody(body []byte, options wsdlCredentialProjectionOptions) ([]byte, []wsdlProjectedSecret, []importreview.Finding, error) {
	if len(body) == 0 {
		return body, nil, nil, nil
	}
	source := credentialpolicy.SourceRef{
		Format:  "wsdl",
		Path:    options.SourcePath,
		Pointer: options.Pointer,
	}
	projected, projection, err := credentialpolicy.ProjectXML(body, credentialpolicy.Options{
		Mode:               credentialpolicy.SecretReference,
		Source:             source,
		RequestKey:         options.RequestKey,
		NamePrefix:         "WSDL",
		Registry:           options.Registry,
		TrustedWholeValues: wsdlTrustedWholeRuntimeValues(body),
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

	secrets := make([]wsdlProjectedSecret, 0, len(review.Requirements))
	for _, requirement := range review.Requirements {
		// Existing governed references and adapter-proven runtime values do not
		// carry source migration material and must not become permanent import
		// blockers.
		if requirement.ExistingReference || !requirement.SourceValueAvailable {
			continue
		}
		sourceValues := uniqueNonEmptyStrings(
			requirement.Value,
			decodeWSDLXMLScalar(requirement.Value),
		)
		requirement.Value = ""
		requirement.SourceValueAvailable = false
		secrets = append(secrets, wsdlProjectedSecret{
			Requirement:  requirement,
			SourceValues: sourceValues,
		})
	}
	return projected, secrets, review.Findings, nil
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

func decodeWSDLXMLScalar(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var value struct {
		Text string `xml:",chardata"`
	}
	if err := xml.Unmarshal([]byte("<value>"+raw+"</value>"), &value); err != nil {
		return ""
	}
	return value.Text
}
