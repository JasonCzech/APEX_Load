package enterprisearchive

import (
	"fmt"
	"path"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// LoadRunner registrations (web_reg_save_param*, web_reg_find) search a
// scope of the next step's responses. APEX extractors and assertions read the
// final main response's body or headers only, so every scope or filter APEX
// does not reproduce is surfaced here instead of being searched in the wrong
// place silently.
const (
	// registrationScopeNarrowedReasonCode: the registration searches a scope
	// APEX approximates (an unrecognized scope, or Cookies with a structured
	// JSON/XPath query).
	registrationScopeNarrowedReasonCode = "registration_scope_narrowed"
	// registrationFilterReasonCode: a search filter (RequestUrl, ContentType,
	// HeaderNames, RelFrameId, IgnoreRedirections=No) APEX cannot honour.
	registrationFilterReasonCode = "registration_filter_unreproduced"
	// registrationDFEReasonCode: data-format extensions transform the
	// response before searching, so the saved value would differ.
	registrationDFEReasonCode = "registration_dfe_unsupported"
	// textCheckScopeReasonCode: web_reg_find searches a scope APEX does not
	// check yet (Headers), so the check is not executed.
	textCheckScopeReasonCode = "text_check_scope_unsupported"
	// textCheckNarrowedReasonCode: web_reg_find Search=ALL checked against the
	// main body only.
	textCheckNarrowedReasonCode = "text_check_scope_narrowed"
	// textCheckUnsupportedReasonCode: a text check form APEX cannot express
	// yet (TextPfx/TextSfx, /DIG, /ALNUM, /BIN, /REGEXP); not executed.
	textCheckUnsupportedReasonCode = "text_check_unsupported"
)

// registrationScope is how a registration's search scope maps onto an APEX
// extractor source.
type registrationScope struct {
	source   string
	findings []importreview.Finding
}

func registrationFinding(fn ParsedFunction, code string, severity diag.Severity, gate importreview.GateScope, detail string) importreview.Finding {
	return importreview.Finding{
		Code: code, ReasonCode: code, Severity: severity, Gate: gate,
		Source: "enterprise_script_archive", SourceRef: fmt.Sprintf("%s:%d", fn.Filename, fn.LineNumber),
		Resolution: "review_registration_scope", Detail: detail,
	}
}

func promotionFinding(fn ParsedFunction, code, detail string) importreview.Finding {
	return registrationFinding(fn, code, diag.Warning, importreview.GatePromotion, detail)
}

// sourceRegistrationScope maps a save-parameter registration's Scope= or
// Search= attribute. textSearch is true for boundary and regular-expression
// registrations, which can search headers and body together; JSON, XPath and
// attribute registrations always read the body.
func sourceRegistrationScope(fn ParsedFunction, paramName string, textSearch bool) registrationScope {
	raw := strings.TrimSpace(cleanConfigValue(firstNonEmptyArg(fn, "Scope")))
	if raw == "" {
		raw = strings.TrimSpace(cleanConfigValue(firstNonEmptyArg(fn, "Search")))
	}
	switch strings.ToLower(raw) {
	case "headers", "header":
		return registrationScope{source: "header"}
	case "body", "noresource":
		// Noresource is the main HTML body without resources, which is exactly
		// what APEX extractors read.
		return registrationScope{source: "body"}
	case "cookies", "cookie":
		if textSearch {
			// The cookies source searches the Set-Cookie header lines only.
			return registrationScope{source: "cookies"}
		}
		return registrationScope{source: "body", findings: []importreview.Finding{promotionFinding(fn, registrationScopeNarrowedReasonCode,
			fmt.Sprintf("Parameter %q searches cookies (Scope=Cookies) with a structured query; APEX runs structured queries against the response body.", paramName))}}
	case "all", "":
		// LoadRunner's default (and explicit ALL) searches headers and body of
		// every response of the step. "all" covers the main response's headers
		// and body; redirect hops and resources are a separate, general gap.
		if textSearch {
			return registrationScope{source: "all"}
		}
		return registrationScope{source: "body"}
	default:
		return registrationScope{source: "body", findings: []importreview.Finding{promotionFinding(fn, registrationScopeNarrowedReasonCode,
			fmt.Sprintf("Parameter %q uses an unrecognized search scope %q; APEX searches the response body.", paramName, raw))}}
	}
}

// pendingRegistrationFilter is a search filter checked against the request
// the registration ends up attached to.
type pendingRegistrationFilter struct {
	fn          ParsedFunction
	paramName   string
	requestURL  string
	contentType string
}

// sourceRegistrationFilters returns the immediate findings for a
// registration's search filters and DFEs, plus the filters that can only be
// judged once the next request is known.
func sourceRegistrationFilters(fn ParsedFunction, paramName string) ([]importreview.Finding, *pendingRegistrationFilter) {
	var findings []importreview.Finding
	if dfes := strings.TrimSpace(cleanConfigValue(firstNonEmptyArg(fn, "DFEs"))); dfes != "" {
		findings = append(findings, registrationFinding(fn, registrationDFEReasonCode, diag.Blocker, importreview.GateExecution,
			fmt.Sprintf("Parameter %q applies data-format extensions (%s) to the response before searching; APEX searches the raw response, so the saved value would differ.", paramName, dfes)))
	}
	if value := strings.TrimSpace(cleanConfigValue(firstNonEmptyArg(fn, "IgnoreRedirections"))); strings.EqualFold(value, "no") {
		findings = append(findings, promotionFinding(fn, registrationFilterReasonCode,
			fmt.Sprintf("Parameter %q also searches redirect responses (IgnoreRedirections=No); APEX reads only the final response after redirects.", paramName)))
	}
	if value := strings.TrimSpace(cleanConfigValue(firstNonEmptyArg(fn, "HeaderNames"))); value != "" {
		findings = append(findings, promotionFinding(fn, registrationFilterReasonCode,
			fmt.Sprintf("Parameter %q searches only the headers %s; APEX searches all response headers, so an earlier header can match first.", paramName, value)))
	}
	if value := strings.TrimSpace(cleanConfigValue(firstNonEmptyArg(fn, "RelFrameId"))); value != "" {
		findings = append(findings, promotionFinding(fn, registrationFilterReasonCode,
			fmt.Sprintf("Parameter %q searches frame %s of the page; APEX does not model frames and searches the main response.", paramName, value)))
	}
	pending := &pendingRegistrationFilter{
		fn:          fn,
		paramName:   paramName,
		requestURL:  strings.TrimSpace(cleanConfigValue(firstNonEmptyArg(fn, "RequestUrl"))),
		contentType: strings.TrimSpace(cleanConfigValue(firstNonEmptyArg(fn, "ContentType"))),
	}
	if pending.requestURL == "" && pending.contentType == "" {
		pending = nil
	}
	return findings, pending
}

// findingsForAttachedRequest judges RequestUrl= and ContentType= against the
// request the registration attached to: LoadRunner applies the registration
// to whichever response of the step matches, while APEX only reads the main
// response, so a filter that does not match the main request means the value
// would come from a resource or redirect APEX never searches.
func (p *pendingRegistrationFilter) findingsForAttachedRequest(action *dsl.Action, recContentType string) []importreview.Finding {
	if p == nil {
		return nil
	}
	var findings []importreview.Finding
	if p.requestURL != "" {
		url := ""
		if action != nil {
			url = metadataString(action.Metadata, "original_url")
			if url == "" {
				url = action.Target
			}
		}
		if !loadRunnerGlobMatch(p.requestURL, url) {
			findings = append(findings, promotionFinding(p.fn, registrationFilterReasonCode,
				fmt.Sprintf("Parameter %q applies to responses whose URL matches %q, which the step's main request (%s) does not; the value comes from a resource or redirect APEX does not search.", p.paramName, p.requestURL, url)))
		}
	}
	if p.contentType != "" && !loadRunnerGlobMatch(p.contentType, recContentType) {
		findings = append(findings, promotionFinding(p.fn, registrationFilterReasonCode,
			fmt.Sprintf("Parameter %q applies to responses of content type %q, which does not match the step's recorded content type %q; the value comes from a response APEX does not search.", p.paramName, p.contentType, recContentType)))
	}
	return findings
}

// loadRunnerGlobMatch matches LoadRunner's "*" wildcard filters
// case-insensitively against the whole value.
func loadRunnerGlobMatch(pattern, value string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	value = strings.ToLower(strings.TrimSpace(value))
	if pattern == "" {
		return true
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return strings.Contains(value, pattern)
	}
	// path.Match treats "/" as a separator for "*"; LoadRunner's "*" spans it.
	escaped := strings.ReplaceAll(pattern, "/", "\x00")
	matched, err := path.Match(escaped, strings.ReplaceAll(value, "/", "\x00"))
	return err == nil && matched
}
