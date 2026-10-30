package salesforce

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
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
		correlations = append(correlations, correlate.Correlation{
			Name:        name,
			Source:      "header",
			Expression:  auraTokenCookieExtractor,
			ExtractType: "regex",
			FoundInID:   observations[producer].ID,
			UsedInIDs:   usedIn,
			Value:       token,
			SampleValue: correlate.Truncate(token, 80),
			RuleID:      correlate.RuleSalesforceAuraTokenUncapturedCookie,
			Confidence:  auraTokenUncapturedCookieConfidence,
			Reasons: []string{
				"aura.token is sent by later Aura requests but no captured response issues it",
				"Lightning sets it in the __Host-ERIC_* Set-Cookie of " + auraTokenCookieBootstrapPath + ", which the capture does not include",
			},
		})
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

var auraTokenCookiePattern = regexp.MustCompile(auraTokenCookieExtractor)

// auraTokenHeader is a header's value, matched case-insensitively.
func auraTokenHeader(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}
