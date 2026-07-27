package wsdl

import (
	"encoding/xml"
	"io"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// wsdlCredentialProjectionOptions contains provenance only. Credential values
// are never used to derive requirement identities or diagnostics.
type wsdlCredentialProjectionOptions struct {
	RequestKey string
	SourcePath string
	Pointer    string
	Registry   *credentialpolicy.NameRegistry
}

// wsdlProjectedSecret retains source values only for the duration of Convert,
// so source-review copies can be scrubbed. Its public requirement is cleared of
// migration material because WSDL does not expose a protected import session.
type wsdlProjectedSecret struct {
	Requirement  credentialpolicy.Requirement
	SourceValues []string
}

// isGovernedWSDLXMLValue recognizes only exact runtime values emitted by the
// WSDL skeleton adapter. This is source-specific template provenance, not a
// second credential projector; credential decisions remain in ProjectXML.
func isGovernedWSDLXMLValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == credential.Redacted {
		return true
	}
	if _, ok := credential.ParseReference(value); ok {
		return true
	}
	if !strings.HasPrefix(value, "{{") || !strings.HasSuffix(value, "}}") ||
		strings.Count(value, "{{") != 1 || strings.Count(value, "}}") != 1 {
		return false
	}
	expression := strings.TrimSpace(value[2 : len(value)-2])
	return (strings.HasPrefix(expression, "data.wsdl.") && len(expression) > len("data.wsdl.")) ||
		(strings.HasPrefix(expression, "fn.") && len(expression) > len("fn."))
}

func wsdlOperationReviewFindings(op operationInfo, sourcePath, pointer string) []importreview.Finding {
	location := sourceLocation(sourcePath, pointer)
	var findings []importreview.Finding
	if !op.BindingOperationResolved {
		findings = append(findings, importreview.Finding{
			Code:     "wsdl_unresolved_binding_operation_review_required",
			Severity: diag.Blocker,
			Element:  "WSDL SOAP binding",
			Detail:   "The portType operation was not resolved through the selected SOAP binding and must be verified before execution",
			Location: location,
			Gate:     importreview.GateExecution,
		})
	}
	if strings.EqualFold(strings.TrimSpace(op.Style), "rpc") {
		findings = append(findings, importreview.Finding{
			Code:     "wsdl_rpc_style_review_required",
			Severity: diag.Blocker,
			Element:  "WSDL SOAP binding",
			Detail:   "RPC-style SOAP message wrapping is ambiguous and must be replaced with verified request behavior before execution",
			Location: location,
			Gate:     importreview.GateExecution,
		})
	}
	if strings.EqualFold(strings.TrimSpace(op.BodyUse), "encoded") {
		findings = append(findings, importreview.Finding{
			Code:     "wsdl_encoded_use_review_required",
			Severity: diag.Blocker,
			Element:  "WSDL SOAP binding",
			Detail:   "SOAP encoded body semantics are not represented exactly and must be replaced with verified request behavior before execution",
			Location: location,
			Gate:     importreview.GateExecution,
		})
	}
	return importreview.NormalizeFindings(findings)
}

func wsdlBindingReviewFindings(choice bindingChoice, doc definitions, sourcePath, pointer string) []importreview.Finding {
	location := sourceLocation(sourcePath, pointer)
	var findings []importreview.Finding
	transport := strings.TrimSpace(choice.Transport)
	if transport == "" && strings.TrimSpace(choice.BindingName) != "" {
		findings = append(findings, importreview.Finding{
			Code:     "wsdl_binding_transport_review_required",
			Severity: diag.Blocker,
			Element:  "WSDL SOAP transport",
			Detail:   "The selected SOAP binding does not declare an HTTP transport and must be verified before execution",
			Location: location,
			Gate:     importreview.GateExecution,
		})
	} else if transport != "" && !strings.EqualFold(transport, "http://schemas.xmlsoap.org/soap/http") {
		findings = append(findings, importreview.Finding{
			Code:     "wsdl_unsupported_transport_review_required",
			Severity: diag.Blocker,
			Element:  "WSDL SOAP transport",
			Detail:   "The selected SOAP binding uses a transport the native HTTP SOAP runtime cannot reproduce",
			Location: location,
			Gate:     importreview.GateExecution,
		})
	}
	if wsdlHasUnresolvedImports(doc) {
		findings = append(findings, importreview.Finding{
			Code:     "wsdl_unresolved_import_review_required",
			Severity: diag.Blocker,
			Element:  "WSDL imports",
			Detail:   "One or more referenced WSDL or XSD documents were not resolved, so the generated request shape may be incomplete",
			Location: location,
			Gate:     importreview.GateExecution,
		})
	}
	return importreview.NormalizeFindings(findings)
}

func wsdlHasUnresolvedImports(doc definitions) bool {
	if len(doc.Imports) > 0 {
		return true
	}
	for _, typed := range doc.Types {
		for _, schema := range typed.Schemas {
			if len(schema.Imports) > 0 || len(schema.Includes) > 0 {
				return true
			}
		}
	}
	return false
}

func wsdlSkeletonReviewFindings(diagnostics diag.Diagnostics, sourcePath, pointer string) []importreview.Finding {
	location := sourceLocation(sourcePath, pointer+"/body")
	type findingTemplate struct {
		code    string
		element string
		detail  string
	}
	templates := map[string]findingTemplate{
		"WSDL SOAP header": {
			code:    "wsdl_unresolved_soap_header_review_required",
			element: "WSDL SOAP header",
			detail:  "A declared SOAP header could not be resolved exactly and must be completed before execution",
		},
		"WSDL input message": {
			code:    "wsdl_unresolved_input_message_review_required",
			element: "WSDL input message",
			detail:  "The operation input message or selected body parts could not be resolved and the generated placeholder must be replaced before execution",
		},
		"WSDL XSD type": {
			code:    "wsdl_incomplete_xsd_type_review_required",
			element: "WSDL XSD type",
			detail:  "An XSD type could not be represented completely and the generated request body must be verified before execution",
		},
		"WSDL XSD element": {
			code:    "wsdl_unresolved_xsd_element_review_required",
			element: "WSDL XSD element",
			detail:  "A message element or type could not be resolved exactly and the generated placeholder must be replaced before execution",
		},
		"WSDL XSD ref": {
			code:    "wsdl_unresolved_xsd_reference_review_required",
			element: "WSDL XSD reference",
			detail:  "An XSD element reference could not be resolved exactly and the generated placeholder must be replaced before execution",
		},
		"WSDL XSD choice": {
			code:    "wsdl_xsd_choice_review_required",
			element: "WSDL XSD choice",
			detail:  "The importer selected one branch of an XSD choice; select and verify the intended request shape before execution",
		},
	}
	findings := make([]importreview.Finding, 0)
	seen := make(map[string]struct{})
	for _, item := range diagnostics {
		if item.Severity != diag.Warning && item.Severity != diag.Blocker {
			continue
		}
		template, ok := templates[item.Element]
		if !ok {
			continue
		}
		if _, exists := seen[template.code]; exists {
			continue
		}
		seen[template.code] = struct{}{}
		findings = append(findings, importreview.Finding{
			Code:     template.code,
			Severity: diag.Blocker,
			Element:  template.element,
			Detail:   template.detail,
			Location: location,
			Gate:     importreview.GateExecution,
		})
	}
	return importreview.NormalizeFindings(findings)
}

func wsdlMissingEndpointReviewFinding(sourcePath, pointer string) importreview.Finding {
	return importreview.Finding{
		Code:     "wsdl_missing_endpoint_review_required",
		Severity: diag.Blocker,
		Element:  "WSDL SOAP endpoint",
		Detail:   "No SOAP service endpoint or base URL was available; configure and verify the destination before execution",
		Location: sourceLocation(sourcePath, pointer),
		Gate:     importreview.GateExecution,
	}
}

func wsdlImportReviewMetadata(findings []importreview.Finding, sourcePath, pointer string) map[string]any {
	reasonCodes := make([]string, 0, len(findings))
	sourceRefs := make([]string, 0, len(findings)+1)
	sourceRefs = append(sourceRefs, sourceLocation(sourcePath, pointer))
	for _, finding := range findings {
		if finding.Code != "" {
			reasonCodes = append(reasonCodes, finding.Code)
		}
		if finding.Location != "" {
			sourceRefs = append(sourceRefs, finding.Location)
		}
	}
	return map[string]any{
		"version":      1,
		"source":       "wsdl",
		"reason_codes": uniqueSortedStrings(reasonCodes),
		"source_refs":  uniqueSortedStrings(sourceRefs),
		"resolution":   "replace_behavior_and_remove_gate",
	}
}

func sourceLocation(path, pointer string) string {
	path = strings.TrimSpace(path)
	pointer = strings.TrimSpace(pointer)
	switch {
	case path == "":
		return pointer
	case pointer == "":
		return path
	default:
		return path + "#" + pointer
	}
}

func redactWSDLProjectedSecrets(files map[string]string, secrets []wsdlProjectedSecret) map[string]string {
	if len(files) == 0 {
		return nil
	}
	values := make([]string, 0)
	for _, secret := range secrets {
		values = append(values, secret.SourceValues...)
	}
	values = uniqueNonEmptyStrings(values...)
	sort.SliceStable(values, func(i, j int) bool {
		if len(values[i]) == len(values[j]) {
			return values[i] < values[j]
		}
		return len(values[i]) > len(values[j])
	})
	replacements := make([]string, 0, len(values)*2)
	for _, value := range values {
		replacements = append(replacements, value, credential.Redacted)
	}
	var replacer *strings.Replacer
	if len(replacements) > 0 {
		// A single trie-backed replacement pass avoids O(source-bytes ×
		// requirement-count) work for schemas containing many distinct authored
		// credential defaults.
		replacer = strings.NewReplacer(replacements...)
	}
	out := make(map[string]string, len(files))
	for name, content := range files {
		if replacer != nil {
			content = replacer.Replace(content)
		}
		if wsdlXMLContainsDecodedProtectedValue(content, replacer) {
			content = "<!-- [WSDL SOURCE OMITTED: ENCODED CREDENTIAL VALUE REQUIRES PROTECTED BINDING] -->"
		}
		// Source review is a disclosure surface, including for unused schema
		// branches that never contribute a generated request and therefore never
		// create a projection requirement. Apply the irreversible shared scrubber
		// after exact projected-value removal so those dormant credentials cannot
		// survive in API JSON.
		content = scrub.Value(content, scrub.Policy{})
		out[name] = content
	}
	return out
}

func wsdlXMLContainsDecodedProtectedValue(content string, replacer *strings.Replacer) bool {
	if replacer == nil {
		return false
	}
	decoder := xml.NewDecoder(strings.NewReader(content))
	for {
		token, err := decoder.Token()
		if err != nil {
			// The source parsed before conversion. If redaction makes the
			// public copy ambiguous, fail closed instead of assuming an entity
			// or alternate XML spelling no longer discloses a credential.
			return err != io.EOF
		}
		switch typed := token.(type) {
		case xml.CharData:
			if containsWSDLProtectedValue(string(typed), replacer) {
				return true
			}
		case xml.StartElement:
			for _, attribute := range typed.Attr {
				if containsWSDLProtectedValue(attribute.Value, replacer) {
					return true
				}
			}
		}
	}
}

func containsWSDLProtectedValue(value string, replacer *strings.Replacer) bool {
	return replacer != nil && replacer.Replace(value) != value
}

func uniqueNonEmptyStrings(values ...string) []string {
	return uniqueSortedStrings(values)
}

func uniqueSortedStrings(values []string) []string {
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
