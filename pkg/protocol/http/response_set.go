package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// A scoped extractor (protocol.ExtractorScope) searches more than the final
// response: the redirect hops before it and the resources fetched after it,
// optionally filtered by request URL and Content-Type, as LoadRunner
// registrations search every response of a step.
const (
	// maxResponseSetEntries and maxResponseSetBytes bound the extra responses
	// retained for scoped extractors on one request.
	maxResponseSetEntries = 64
	maxResponseSetBytes   = 8 << 20
)

type responseSetKey struct{}

// responseSetCollector holds the redirect hops and resource responses of one
// request. Resources are fetched concurrently, so it is locked.
type responseSetCollector struct {
	wantHops      bool
	wantResources bool

	mu        sync.Mutex
	hops      []responseSurfaces
	resources []responseSurfaces
	bytes     int64
}

// newResponseSetCollector returns nil when no extractor needs anything beyond
// the final response.
func newResponseSetCollector(extractors []protocol.Extractor) *responseSetCollector {
	collector := &responseSetCollector{}
	for _, ext := range extractors {
		if ext.Scope == nil {
			continue
		}
		collector.wantHops = collector.wantHops || scopeIncludes(ext.Scope.Redirects)
		collector.wantResources = collector.wantResources || scopeIncludes(ext.Scope.Resources)
	}
	if !collector.wantHops && !collector.wantResources {
		return nil
	}
	return collector
}

func scopeIncludes(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "include")
}

// addHop records a redirect hop's response, seen from CheckRedirect. Go does
// not keep a hop's body, so only its status and headers are searchable.
func (c *responseSetCollector) addHop(resp *http.Response) {
	if c == nil || !c.wantHops || resp == nil {
		return
	}
	hop := responseSurfaces{headers: resp.Header.Clone(), status: resp.StatusCode}
	if resp.Request != nil && resp.Request.URL != nil {
		hop.url = resp.Request.URL.String()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	size := flattenedHeadersByteSize(hop.headers)
	if len(c.hops)+len(c.resources) >= maxResponseSetEntries || c.bytes+size > maxResponseSetBytes {
		return
	}
	c.bytes += size
	c.hops = append(c.hops, hop)
}

// recordResourceResponse adds a fetched resource's response to the request's
// collector, when one is listening.
func recordResourceResponse(ctx context.Context, url string, status int, headers http.Header, body []byte) {
	c, ok := ctx.Value(responseSetKey{}).(*responseSetCollector)
	if !ok || c == nil || !c.wantResources || headers == nil {
		return
	}
	size := flattenedHeadersByteSize(headers) + int64(len(body))
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.hops)+len(c.resources) >= maxResponseSetEntries || c.bytes+size > maxResponseSetBytes {
		return
	}
	c.bytes += size
	c.resources = append(c.resources, responseSurfaces{headers: headers.Clone(), body: append([]byte(nil), body...), url: url, status: status})
}

// scopedCandidates returns the responses ext searches, in order: redirect
// hops, the final response, then resources in arrival order, keeping those
// that pass the request URL and Content-Type filters.
func scopedCandidates(ext protocol.Extractor, final responseSurfaces, set *responseSetCollector) []responseSurfaces {
	scope := ext.Scope
	if scope == nil {
		return []responseSurfaces{final}
	}
	var all []responseSurfaces
	if set != nil {
		set.mu.Lock()
		defer set.mu.Unlock()
	}
	if set != nil && scopeIncludes(scope.Redirects) {
		all = append(all, set.hops...)
	}
	final.isFinal = true
	all = append(all, final)
	if set != nil && scopeIncludes(scope.Resources) {
		all = append(all, set.resources...)
	}
	candidates := make([]responseSurfaces, 0, len(all))
	for _, candidate := range all {
		if scope.RequestURL != "" && !MatchScopeGlob(scope.RequestURL, candidate.url) {
			continue
		}
		if scope.ContentType != "" && !MatchScopeGlob(scope.ContentType, candidate.headers.Get("Content-Type")) {
			continue
		}
		if len(scope.HeaderNames) > 0 {
			candidate.headers = onlyHeaders(candidate.headers, scope.HeaderNames)
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func onlyHeaders(headers http.Header, names []string) http.Header {
	kept := http.Header{}
	for _, name := range names {
		if values := headers.Values(name); len(values) > 0 {
			kept[http.CanonicalHeaderKey(strings.TrimSpace(name))] = append([]string(nil), values...)
		}
	}
	return kept
}

// MatchScopeGlob matches LoadRunner's filter wildcards case-insensitively:
// "*" spans any text (including "/"), "?" one character, every other
// character is literal, and a pattern without wildcards matches as a
// substring.
func MatchScopeGlob(pattern, value string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	value = strings.ToLower(strings.TrimSpace(value))
	if pattern == "" {
		return true
	}
	if !strings.ContainsAny(pattern, "*?") {
		return strings.Contains(value, pattern)
	}
	// path.Match treats "/" as a separator for "*" (LoadRunner's "*" spans
	// it) and "[", "]" and "\" as syntax (LoadRunner's are literal).
	var literal strings.Builder
	for _, r := range pattern {
		switch r {
		case '/':
			literal.WriteByte(0)
			continue
		case '[', ']', '\\':
			literal.WriteByte('\\')
		}
		literal.WriteRune(r)
	}
	matched, err := path.Match(literal.String(), strings.ReplaceAll(value, "/", "\x00"))
	return err == nil && matched
}

// chargeCandidates charges the shared extractor budget for every candidate
// other than the final response, which the caller has already charged.
func chargeCandidates(budget *extractorInputWorkBudget, ext protocol.Extractor, candidates []responseSurfaces) error {
	for _, candidate := range candidates {
		if candidate.isFinal {
			continue
		}
		candidateBudget := newExtractorInputWorkBudget(candidate.headers, candidate.body)
		candidateBudget.limit = budget.limit - budget.used
		if err := candidateBudget.charge(ext); err != nil {
			return err
		}
		budget.used += candidateBudget.used
	}
	return nil
}

// extractFromCandidates returns the first match across candidates. A
// non-miss error (a bad expression, a resource limit) stops the search.
func extractFromCandidates(ext protocol.Extractor, candidates []responseSurfaces) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("%w: no response matches the extractor scope", protocol.ErrExtractionMiss)
	}
	var lastMiss error
	for _, candidate := range candidates {
		value, err := extractResponse(ext, candidate)
		if err == nil {
			return value, nil
		}
		if !errors.Is(err, protocol.ErrExtractionMiss) {
			return "", err
		}
		lastMiss = err
	}
	return "", lastMiss
}

// extractAllFromCandidates returns every match across candidates, in order.
func extractAllFromCandidates(ext protocol.Extractor, candidates []responseSurfaces) ([]string, error) {
	if len(candidates) == 1 {
		return extractAllResponse(ext, candidates[0])
	}
	var values []string
	for _, candidate := range candidates {
		matches, err := extractAllResponse(ext, candidate)
		if err != nil && !errors.Is(err, protocol.ErrExtractionMiss) {
			return nil, err
		}
		values = append(values, matches...)
		if len(values) > maxAllExtractorMatches {
			return nil, fmt.Errorf("%w: all extractor matches exceed %d", errExtractorResourceLimit, maxAllExtractorMatches)
		}
	}
	return values, nil
}

// applyHTTPExtractor evaluates one extractor against its candidate responses
// and stores the outcome on result. It reports whether extraction must stop
// for the remaining extractors.
func applyHTTPExtractor(result *protocol.Result, ext protocol.Extractor, candidates []responseSurfaces) bool {
	if ext.Mode == "all" {
		values, err := extractAllFromCandidates(ext, candidates)
		if errors.Is(err, errExtractorResourceLimit) {
			result.Success = false
			result.Extractions = nil
			result.ExtractionsIncomplete = true
			result.ErrorMessage = fmt.Sprintf("extractor %q output rejected: %v", ext.Name, err)
			return true
		}
		if ext.OnMiss == "default" && (ext.DefaultValue == nil || (err != nil && !errors.Is(err, protocol.ErrExtractionMiss))) {
			result.Success = false
			result.ErrorMessage = fmt.Sprintf("extractor %q default rejected: %v", ext.Name, err)
			return false
		}
		if err == nil && len(values) > 0 {
			for i, value := range values {
				if !storeHTTPExtraction(result, ext, fmt.Sprintf("%s_%d", ext.Name, i+1), value) {
					return true
				}
			}
			return !storeHTTPExtraction(result, ext, ext.Name+"_count", fmt.Sprintf("%d", len(values)))
		}
		if ext.OnMiss == "fail" {
			if result.Success {
				result.Success = false
				if err == nil {
					err = fmt.Errorf("no matches")
				}
				result.ErrorMessage = fmt.Sprintf("extractor %q found no match: %v", ext.Name, err)
			}
			return false
		}
		// A lenient miss still publishes the count so loops over
		// {{var.name_count}} behave.
		if !storeHTTPExtraction(result, ext, ext.Name+"_count", "0") {
			return true
		}
		if ext.OnMiss == "default" && ext.DefaultValue != nil {
			if !storeHTTPExtraction(result, ext, ext.Name, *ext.DefaultValue) {
				return true
			}
		}
		if ext.OnMiss == "empty" {
			// Mirror single-value "empty" semantics: assign the base
			// variable an empty string so {{var.name}} expands to "" rather
			// than leaking the literal template into later requests.
			if !storeHTTPExtraction(result, ext, ext.Name, "") {
				return true
			}
		}
		return false
	}
	val, err := extractFromCandidates(ext, candidates)
	if errors.Is(err, errExtractorResourceLimit) {
		result.Success = false
		result.Extractions = nil
		result.ExtractionsIncomplete = true
		result.ErrorMessage = fmt.Sprintf("extractor %q output rejected: %v", ext.Name, err)
		return true
	}
	if err == nil {
		return !storeHTTPExtraction(result, ext, ext.Name, val)
	}
	switch ext.OnMiss {
	case "default":
		if ext.DefaultValue == nil || !errors.Is(err, protocol.ErrExtractionMiss) {
			result.Success = false
			result.ErrorMessage = fmt.Sprintf("extractor %q failed: %v", ext.Name, err)
			return false
		}
		if !storeHTTPExtraction(result, ext, ext.Name, *ext.DefaultValue) {
			return true
		}
	case "fail":
		if result.Success {
			result.Success = false
			result.ErrorMessage = fmt.Sprintf("extractor %q found no match: %v", ext.Name, err)
		}
	case "empty":
		// Source-tool NotFound=warning/empty semantics: the variable is
		// assigned an empty string so later templates expand to "".
		if !storeHTTPExtraction(result, ext, ext.Name, "") {
			return true
		}
	}
	return false
}

// pageResourcesPlanned reports whether Execute will fetch the page's
// resources after the main response; it mirrors the condition there.
func (h *HTTP) pageResourcesPlanned(action *protocol.Action, resp *http.Response, respBody []byte, contentComplete bool) bool {
	if !contentComplete || resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return false
	}
	explicit, _ := ParseExplicitResources(action.Params)
	if len(explicit) > 0 {
		return true
	}
	return h.resources != nil && len(respBody) > 0 && h.shouldParseHTML(resp.Header) && stepHTMLMode(action)
}
