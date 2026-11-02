package salesforce

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
)

// auraTokenCookieExtractor reads the desktop Lightning aura.token from the
// __Host-ERIC_* (or ERIC_*) Set-Cookie line, whatever org-specific suffix the
// cookie name carries. Like the captured-cookie extractor it strips optional
// RFC quotes so the value matches the aura.token form field; it also accepts a
// value with no attributes at the end of a CRLF-terminated header line.
const auraTokenCookieExtractor = `(?m)^(?i:Set-Cookie):[ \t]*(?:__Host-)?ERIC_[!#$%&'*+.^_` + "`" + `|~0-9A-Za-z-]*=[ \t]*"?([^";\s,]+)"?(?:[ \t]*;|[ \t]*\r?$)`

// auraTokenUncapturedCookieConfidence matches the rule catalog's base
// confidence: high enough to apply automatically, below a captured producer
// because the capture cannot show the cookie.
const auraTokenUncapturedCookieConfidence = 0.85

// auraTokenCookieBootstrapPath is the Lightning bootstrap document whose
// response sets the aura.token cookie.
const auraTokenCookieBootstrapPath = "/one/one.app"

// detectUncapturedAuraTokenCorrelations correlates the aura.token that Aura
// requests send when no captured response issues it. Desktop Lightning sets it
// in the __Host-ERIC_* cookie of the /one/one.app response, and Chrome DevTools
// HAR exports drop Set-Cookie, so the generic passes find no producer and the
// recorded token would be replayed stale. The live server sends that cookie
// at replay, so the extractor goes on the last /one/one.app request on the
// same host before the token is first sent (__Host- cookies are host-only).
// Values another pass already correlated, placeholders, tokens with no such
// bootstrap request before their first use, and tokens the capture shows came
// from elsewhere are left alone (auraTokenCookieEvidenceAgainst). One forward
// pass decides each token at its first use, so the cost stays linear in the
// capture, and every scanned body is charged to budget.
func detectUncapturedAuraTokenCorrelations(observations []correlate.Observation, seenValues map[string]bool, budget *salesforceCorrelationBudget, limit int) ([]correlate.Correlation, error) {
	correlations := make([]correlate.Correlation, 0)
	lastBootstrap := map[string]int{}
	decided := map[string]bool{}
	// firstERICSend is the first request that sends each value in an ERIC
	// cookie, filled as the pass goes, so the evidence check stays linear.
	firstERICSend := map[string]int{}
	for index, observation := range observations {
		if cookie := auraTokenHeader(observation.RequestHeaders, "Cookie"); strings.Contains(cookie, "ERIC_") {
			if err := budget.chargeSearchBytes(int64(len(cookie))); err != nil {
				return correlations, err
			}
			for _, pair := range strings.Split(cookie, ";") {
				name, value, _ := strings.Cut(strings.TrimSpace(pair), "=")
				value = strings.Trim(value, `"`)
				if strings.Contains(name, "ERIC_") && value != "" {
					if _, seen := firstERICSend[value]; !seen {
						firstERICSend[value] = index
					}
				}
			}
		}
		parsed, err := url.Parse(observation.RequestURL)
		if err != nil || parsed.Host == "" {
			continue
		}
		host := strings.ToLower(parsed.Host)
		path := strings.TrimRight(parsed.Path, "/")
		if path == auraTokenCookieBootstrapPath {
			lastBootstrap[host] = index
			continue
		}
		if !isAuraPath(path) || observation.RequestBodyTransformed {
			continue
		}
		if err := budget.chargeSearchBytes(int64(len(observation.RequestBody))); err != nil {
			return correlations, err
		}
		token := auraTokenFormValue(observation.RequestBody)
		if token == "" || decided[token] || seenValues[token] {
			continue
		}
		decided[token] = true
		producer, found := lastBootstrap[host]
		if !found || len(correlations) >= limit || auraTokenCookieEvidenceAgainst(observations[producer], firstERICSend, producer, token) {
			continue
		}
		if err := budget.chargeCandidates(1); err != nil {
			return correlations, err
		}
		usedIn, err := budget.requestSearch(observations).UsedAfter(producer, token, budget.chargeSearchBytes)
		if err != nil {
			return correlations, err
		}
		if len(usedIn) == 0 {
			continue
		}
		if err := budget.chargeCorrelation(); err != nil {
			return correlations, err
		}
		seenValues[token] = true
		name := "corr_salesforce_aura_token"
		if len(correlations) > 0 {
			name += "_" + strconv.Itoa(len(correlations)+1)
		}
		expression := auraTokenCookieExtractor
		reasons := []string{
			"aura.token is sent by later Aura requests but no captured response issues it",
			"Lightning sets it in the __Host-ERIC_* Set-Cookie of " + auraTokenCookieBootstrapPath + ", which the capture does not include",
		}
		// The token is already decided and charged, so a budget miss on the
		// bootstrap HTML scan keeps it under the generic ERIC_* extractor
		// rather than dropping it to a literal replay; the error is returned
		// after the correlation is kept.
		cookieName, scanErr := auraBootstrapTokenCookieName(observations[producer].ResponseBody, budget)
		if cookieName != "" {
			reasons = append(reasons, "confirmed auraConfig.eikoocnekot ("+cookieName+") in "+auraTokenCookieBootstrapPath+" bootstrap HTML")
			// The extractor scope includes redirect hops and the first match
			// wins, so the generic ERIC_* pattern could take another ERIC
			// cookie a hop sets. The confirmed name pins the cookie family;
			// its server-generated digit suffix stays open because nothing
			// shows it is the same for every user and session, and for a
			// __Host- name the scope keeps only the bootstrap host's own
			// responses, or only a Lightning-host final response for a
			// relative bootstrap under a Lightning base_url
			// (auraTokenCookieExtractorScope), so a same-family cookie a
			// my.salesforce.com hop sets cannot win.
			// A relative bootstrap under any other base_url stays unfiltered.
			expression = namedAuraTokenCookieExtractor(cookieName)
		}
		correlations = append(correlations, correlate.Correlation{
			Name:        name,
			Source:      "header",
			Expression:  expression,
			ExtractType: "regex",
			FoundInID:   observations[producer].ID,
			UsedInIDs:   usedIn,
			Value:       token,
			SampleValue: correlate.Truncate(token, 80),
			RuleID:      correlate.RuleSalesforceAuraTokenUncapturedCookie,
			Confidence:  auraTokenUncapturedCookieConfidence,
			Reasons:     reasons,
		})
		if scanErr != nil {
			return correlations, scanErr
		}
	}
	return correlations, nil
}

// auraTokenFormValue is the aura.token field of an Aura form body, or "" for
// none, an oversized body, or a guest/pre-login placeholder. It reads only
// that field, without parsing the rest of the body.
func auraTokenFormValue(body string) string {
	if len(body) > aura.MaxEnvelopeBytes {
		return ""
	}
	const key = "aura.token="
	start := -1
	for offset := 0; offset < len(body); {
		at := strings.Index(body[offset:], key)
		if at < 0 {
			return ""
		}
		at += offset
		if at == 0 || body[at-1] == '&' {
			start = at + len(key)
			break
		}
		offset = at + len(key)
	}
	if start < 0 {
		return ""
	}
	raw, _, _ := strings.Cut(body[start:], "&")
	token, err := url.QueryUnescape(raw)
	if err != nil || token == "" || strings.Contains(token, "{{") || token == "undefined" || token == "null" {
		return ""
	}
	return token
}

// isAuraPath reports whether a URL path is a Lightning /aura endpoint,
// including Experience Cloud's /s/sfsites/aura.
func isAuraPath(path string) bool {
	return path == "/aura" || strings.HasSuffix(path, "/aura")
}

// auraTokenCookieEvidenceAgainst reports a capture that shows the bootstrap
// did not issue the token: its response kept Set-Cookie lines but none sets an
// ERIC cookie (an export that keeps cookies, with the browser already holding
// it), or a request up to and including the bootstrap request already sent the
// token in an ERIC cookie. A request after the bootstrap sending it is what a
// bootstrap that issued it looks like, so it is no evidence against. Inferring
// the cookie against the evidence would fail every iteration at the
// bootstrap, so the token is left to the stale-token warning.
func auraTokenCookieEvidenceAgainst(bootstrap correlate.Observation, firstERICSend map[string]int, producer int, token string) bool {
	if setCookie := auraTokenHeader(bootstrap.ResponseHeaders, "Set-Cookie"); setCookie != "" {
		if !auraTokenCookiePattern.MatchString("Set-Cookie: " + strings.ReplaceAll(setCookie, "\n", "\nSet-Cookie: ")) {
			return true
		}
	}
	first, sent := firstERICSend[token]
	return sent && first <= producer
}

var (
	auraTokenCookiePattern = regexp.MustCompile(auraTokenCookieExtractor)
	eikoocnekotHTMLPattern = regexp.MustCompile(`"eikoocnekot"\s*:\s*"([!#$%&'*+.^_` + "`" + `|~0-9A-Za-z-]+)"`)
)

func auraBootstrapTokenCookieName(responseBody string, budget *salesforceCorrelationBudget) (string, error) {
	if responseBody == "" || !strings.Contains(responseBody, "eikoocnekot") {
		return "", nil
	}
	scan := responseBody
	const maxBootstrapScanBytes = 2 << 20
	if len(scan) > maxBootstrapScanBytes {
		scan = scan[:maxBootstrapScanBytes]
	}
	if err := budget.chargeSearchBytes(int64(len(scan))); err != nil {
		return "", err
	}
	match := eikoocnekotHTMLPattern.FindStringSubmatch(scan)
	if len(match) < 2 {
		return "", nil
	}
	return strings.TrimSpace(match[1]), nil
}

// namedAuraTokenCookieExtractor reads the aura.token from the Set-Cookie line
// of the cookie auraConfig.eikoocnekot names. The name's trailing digit run
// (__Host-ERIC_PROD-<digits>) is generated by the server, and another virtual
// user or session may be issued a different one, so the extractor keeps the
// recorded prefix and accepts any digits in its place. A name without such a
// suffix is matched exactly. Leaving the digits out of the expression also
// keeps secret masking from redacting a long recorded number out of the
// regex. Like the generated Set-Cookie extractor it needs an attribute after
// the value on a CRLF-terminated header line, which Salesforce's Path and
// Secure always give.
func namedAuraTokenCookieExtractor(cookieName string) string {
	return auraTokenSetCookieLinePrefix + auraTokenCookieNamePattern(cookieName) + `=[ \t]*"?([^";\s,]+)"?(?:[ \t]*;|[ \t]*$)`
}

// auraTokenSetCookieLinePrefix starts a confirmed-name extractor: a
// Set-Cookie header line up to the cookie name.
const auraTokenSetCookieLinePrefix = `(?m)^(?i:Set-Cookie):[ \t]*`

// auraTokenCookieExtractorScope is the response scope of the aura.token
// cookie extractor on the bootstrap request at producerURL. Redirect hops are
// searched because the bootstrap may answer through a sign-in redirect chain,
// and the first match wins. When the confirmed cookie is a __Host- cookie,
// whose digit suffix the expression leaves open, the scope also keeps only
// responses from the bootstrap's own origin: a __Host- cookie is host-only,
// so the browser never sends the Lightning host a same-family ERIC cookie
// that a hop on another host (for example my.salesforce.com) sets, and the
// extractor must not take it either. producerURL is the action's own target.
// A relative target resolves through config.base_url, which may later name
// another org, so pinning the recorded host would miss every replay; when
// baseURL is a Lightning host the scope instead keeps any Lightning host
// (lightningScopeHostGlob). That glob cannot be anchored to the host, so the
// scope then drops redirect hops and searches only the final response,
// which it still drops when the bootstrap lands off Lightning. A templated
// or unparseable target, or a relative one under any other base_url, keeps
// the hop search unfiltered.
func auraTokenCookieExtractorScope(expression, producerURL, baseURL string) *dsl.ExtractorScope {
	scope := &dsl.ExtractorScope{Redirects: "include"}
	if !strings.HasPrefix(expression, auraTokenSetCookieLinePrefix+regexp.QuoteMeta("__Host-")) {
		return scope
	}
	producerURL = strings.TrimSpace(producerURL)
	if producerURL == "" || requestimport.ContainsTemplatePlaceholder(producerURL) {
		return scope
	}
	parsed, err := url.Parse(producerURL)
	if err != nil || strings.ContainsAny(parsed.Host, "*?") {
		return scope
	}
	if parsed.Host == "" {
		if parsed.Scheme != "" || !strings.HasPrefix(producerURL, "/") || strings.HasPrefix(producerURL, "//") {
			return scope
		}
		if glob := lightningScopeHostGlob(baseURL); glob != "" {
			// The glob's leading "*" also spans "/" and "?", so it cannot
			// keep a hop to its host: a my.salesforce.com hop whose query
			// carries an unencoded Lightning URL would pass it, and the
			// first match wins. Only the final response, where the
			// bootstrap lands, is searched; a token the chain sets only on
			// an earlier hop then misses loudly (on_miss fail) instead of
			// replaying another host's cookie.
			scope.Redirects = ""
			scope.RequestURL = glob
		}
		return scope
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "http" {
		return scope
	}
	scope.RequestURL = scheme + "://" + strings.ToLower(parsed.Host) + "/*"
	return scope
}

// lightningHostSuffix is the host suffix every Lightning Experience host
// (production or sandbox) shares.
const lightningHostSuffix = ".lightning.force.com"

// lightningScopeHostGlob is the request_url glob for any Lightning host on
// baseURL's scheme when baseURL is an untemplated Lightning origin, else "".
func lightningScopeHostGlob(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" || requestimport.ContainsTemplatePlaceholder(baseURL) {
		return ""
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || strings.ContainsAny(parsed.Host, "*?") {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "http" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if len(host) <= len(lightningHostSuffix) || !strings.HasSuffix(host, lightningHostSuffix) {
		return ""
	}
	return scheme + "://*" + lightningHostSuffix + "/*"
}

// auraTokenCookieNameSuffixDigits is the shortest trailing digit run treated
// as a server-generated suffix rather than part of the cookie's fixed name.
const auraTokenCookieNameSuffixDigits = 6

// auraTokenCookieNamePattern is the regex for a confirmed ERIC cookie name:
// its fixed prefix quoted, and a long trailing digit run opened to any signed
// number. The suffix is a signed 64-bit value (both "ERIC_PROD-2876..." and
// "ERIC_PROD1049..." occur), so another session's sign may differ from the
// recorded one.
func auraTokenCookieNamePattern(cookieName string) string {
	digits := strings.TrimRight(cookieName, "0123456789")
	if len(cookieName)-len(digits) < auraTokenCookieNameSuffixDigits || !strings.Contains(digits, "ERIC_") {
		return regexp.QuoteMeta(cookieName)
	}
	prefix := strings.TrimSuffix(digits, "-")
	return regexp.QuoteMeta(prefix) + `-?[0-9]+`
}

// auraTokenHeader is a header's value, matched case-insensitively.
func auraTokenHeader(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}
