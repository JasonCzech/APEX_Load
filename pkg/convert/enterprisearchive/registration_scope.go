package enterprisearchive

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// LoadRunner registrations (web_reg_save_param*, web_reg_find) search a
// scope of the next step's responses. APEX extractors search the same
// response set through their scope (sourceRegistrationExtractorScope);
// assertions read the final main response only. Every scope or filter APEX
// does not reproduce is surfaced here instead of being searched in the wrong
// place silently.
const (
	// registrationScopeNarrowedReasonCode: the registration searches a scope
	// APEX approximates (an unrecognized scope, or Cookies with a structured
	// JSON/XPath query).
	registrationScopeNarrowedReasonCode = "registration_scope_narrowed"
	// registrationFilterReasonCode: a search filter APEX cannot honour
	// (RelFrameId, HeaderNames on a body search, or a RequestUrl/ContentType
	// filter that only a resource APEX may not fetch can match).
	registrationFilterReasonCode = "registration_filter_unreproduced"
	// registrationDFEReasonCode: data-format extensions APEX does not
	// implement transform the response before searching, so the saved value
	// would differ.
	registrationDFEReasonCode = "registration_dfe_unsupported"
	// textCheckNarrowedReasonCode: web_reg_find Search=ALL also spans
	// downloaded resources; APEX checks the main response's headers and body.
	textCheckNarrowedReasonCode = "text_check_scope_narrowed"
	// textCheckUnsupportedReasonCode: a text check APEX cannot express (an
	// unknown flag, a non-RE2 /REGEXP, /BIN bytes above 0x7F); not executed.
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
	raw := sourceRegistrationScopeName(fn)
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
		// every response of the step: "all" is headers and body, and the
		// extractor scope adds the redirect hops and resources.
		if textSearch {
			return registrationScope{source: "all"}
		}
		return registrationScope{source: "body"}
	default:
		return registrationScope{source: "body", findings: []importreview.Finding{promotionFinding(fn, registrationScopeNarrowedReasonCode,
			fmt.Sprintf("Parameter %q uses an unrecognized search scope %q; APEX searches the response body.", paramName, raw))}}
	}
}

// sourceRegistrationScopeName is a registration's Scope= attribute, or its
// Search= attribute when it has no Scope=.
func sourceRegistrationScopeName(fn ParsedFunction) string {
	if raw := strings.TrimSpace(cleanConfigValue(firstNonEmptyArg(fn, "Scope"))); raw != "" {
		return raw
	}
	return strings.TrimSpace(cleanConfigValue(firstNonEmptyArg(fn, "Search")))
}

// loadRunnerDFETypes maps LoadRunner's built-in data-format extension names
// (case and separators ignored) to APEX extractor transforms.
var loadRunnerDFETypes = map[string]string{
	"base64decode": "base64", "base64": "base64",
	"urlencoding": "url_decode", "urldecode": "url_decode",
	"json2xml": "json_to_xml", "jsontoxml": "json_to_xml",
	"xml2json": "xml_to_json", "xmltojson": "xml_to_json",
	"gzip": "gzip", "gzipdecode": "gzip", "gunzip": "gzip",
	"htmlunescape": "html_unescape", "htmldecode": "html_unescape",
}

// sourceRegistrationDFEs maps a registration's DFEs= list to APEX transforms,
// and returns the names that have no APEX equivalent (custom or GWT DFEs).
func sourceRegistrationDFEs(fn ParsedFunction) ([]string, []string) {
	var types, unknown []string
	for _, name := range strings.FieldsFunc(cleanConfigValue(firstNonEmptyArg(fn, "DFEs")), func(r rune) bool { return r == ',' || r == ';' }) {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.ToLower(name))
		if transform, ok := loadRunnerDFETypes[key]; ok {
			types = append(types, transform)
		} else {
			unknown = append(unknown, name)
		}
	}
	return types, unknown
}

// pendingRegistrationFilter is a search filter checked against the request
// the registration ends up attached to.
type pendingRegistrationFilter struct {
	fn          ParsedFunction
	paramName   string
	requestURL  string
	contentType string
}

// sourceRegistrationExtractorScope is the response set a registration
// searches. LoadRunner searches every response of the step, redirect hops and
// resources included, unless IgnoreRedirections=Yes; RequestUrl=,
// ContentType= and HeaderNames= narrow it, exactly as APEX's scope does.
func sourceRegistrationExtractorScope(fn ParsedFunction, source string) *dsl.ExtractorScope {
	scope := &dsl.ExtractorScope{Redirects: "include", Resources: "include"}
	if strings.EqualFold(strings.TrimSpace(cleanConfigValue(firstNonEmptyArg(fn, "IgnoreRedirections"))), "yes") {
		scope.Redirects = "exclude"
	}
	if strings.EqualFold(sourceRegistrationScopeName(fn), "noresource") {
		// Noresource searches the main HTML body only, never a resource.
		scope.Resources = "exclude"
	}
	scope.RequestURL = strings.TrimSpace(cleanConfigValue(firstNonEmptyArg(fn, "RequestUrl")))
	scope.ContentType = strings.TrimSpace(cleanConfigValue(firstNonEmptyArg(fn, "ContentType")))
	if registrationSourceSearchesHeaders(source) {
		for _, name := range strings.FieldsFunc(cleanConfigValue(firstNonEmptyArg(fn, "HeaderNames")), func(r rune) bool { return r == ',' || r == ';' }) {
			if name = strings.TrimSpace(name); name != "" {
				scope.HeaderNames = append(scope.HeaderNames, name)
			}
		}
	}
	return scope
}

func registrationSourceSearchesHeaders(source string) bool {
	switch source {
	case "header", "all", "cookies":
		return true
	}
	return false
}

// sourceRegistrationFilters returns the immediate findings for a
// registration's search filters and DFEs, plus the filters that can only be
// judged once the next request is known. source is the extractor source the
// registration maps to.
func sourceRegistrationFilters(fn ParsedFunction, paramName, source string) ([]importreview.Finding, *pendingRegistrationFilter) {
	var findings []importreview.Finding
	if _, unknown := sourceRegistrationDFEs(fn); len(unknown) > 0 {
		findings = append(findings, registrationFinding(fn, registrationDFEReasonCode, diag.Blocker, importreview.GateExecution,
			fmt.Sprintf("Parameter %q applies data-format extensions APEX does not implement (%s) to the response before searching, so the saved value would differ.", paramName, strings.Join(unknown, ", "))))
	}
	if value := strings.TrimSpace(cleanConfigValue(firstNonEmptyArg(fn, "HeaderNames"))); value != "" && !registrationSourceSearchesHeaders(source) {
		findings = append(findings, promotionFinding(fn, registrationFilterReasonCode,
			fmt.Sprintf("Parameter %q limits its search to the headers %s but searches the response body, so the limit has no effect.", paramName, value)))
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
// request the registration attached to. The extractor scope applies the same
// filters to every response APEX sees, but APEX sees resource responses only
// when the step fetches them: explicit EXTRARES resources, or Browser
// Emulation downloads. A filter that matches neither the main request nor an
// explicit resource may therefore find nothing.
func (p *pendingRegistrationFilter) findingsForAttachedRequest(action *dsl.Action, recContentType string) []importreview.Finding {
	if p == nil {
		return nil
	}
	explicit := explicitResourceURLs(action)
	var findings []importreview.Finding
	if p.requestURL != "" {
		url := ""
		if action != nil {
			url = metadataString(action.Metadata, "original_url")
			if url == "" {
				url = action.Target
			}
		}
		matched := loadRunnerGlobMatch(p.requestURL, url)
		for _, resource := range explicit {
			matched = matched || loadRunnerGlobMatch(p.requestURL, resource)
		}
		if !matched {
			findings = append(findings, promotionFinding(p.fn, registrationFilterReasonCode,
				fmt.Sprintf("Parameter %q applies to responses whose URL matches %q, which neither the step's main request (%s) nor its explicit resources match; APEX finds the value only if Browser Emulation downloads that resource.", p.paramName, p.requestURL, url)))
		}
	}
	if p.contentType != "" && !loadRunnerGlobMatch(p.contentType, recContentType) && len(explicit) == 0 {
		findings = append(findings, promotionFinding(p.fn, registrationFilterReasonCode,
			fmt.Sprintf("Parameter %q applies to responses of content type %q, which does not match the step's recorded content type %q and the step lists no explicit resources; APEX finds the value only if Browser Emulation downloads a matching resource.", p.paramName, p.contentType, recContentType)))
	}
	return findings
}

// explicitResourceURLs lists the EXTRARES resource URLs of a converted step.
func explicitResourceURLs(action *dsl.Action) []string {
	if action == nil {
		return nil
	}
	resources, _ := action.Params["resources"].([]any)
	urls := make([]string, 0, len(resources))
	for _, resource := range resources {
		entry, _ := resource.(map[string]any)
		if url, _ := entry["url"].(string); url != "" {
			urls = append(urls, url)
		}
	}
	return urls
}

// loadRunnerGlobMatch matches LoadRunner's wildcard filters as the runtime
// does (httpprotocol.MatchScopeGlob): case-insensitively, and as a substring
// when the pattern has no wildcard.
func loadRunnerGlobMatch(pattern, value string) bool {
	return httpprotocol.MatchScopeGlob(pattern, value)
}
