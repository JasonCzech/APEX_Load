package correlate

import (
	"net/url"
	"strings"
)

// A correlation replaces a recorded literal with a value extracted from an
// earlier response, and its extractor fails the step when the value is
// missing. Values that are not server-generated state gain nothing from that
// and add a failure point, so detection rejects three provable cases:
//
//   - name-shaped values from the generic "*id*" key heuristics
//     ("navigation", "Retail_Home_Page") and HTTP authentication scheme names
//     from token keys ("Bearer" in token_type);
//   - bare URL origins, which are environment configuration (base_url);
//   - values the client already sent before the producing response, for the
//     weak heuristic rules (see clientOriginCheckApplies).

const maxDynamicIDNameBytes = 32

// candidateValueCanCorrelate applies the value-only precision rules.
func candidateValueCanCorrelate(candidate Candidate) bool {
	if bareURLOrigin(candidate.Value) {
		return false
	}
	switch candidate.RuleID {
	case RuleJSONDynamicID, RuleXMLDynamicID:
		return !identifierName(candidate.Value)
	case RuleJSONAuthToken, RuleJSONSessionID:
		return !httpAuthenticationScheme(candidate.Value)
	default:
		return true
	}
}

// clientOriginCheckApplies selects the rules whose evidence is only a key name
// or a generic response position. Credential, session and platform rules are
// exempt: an earlier occurrence of their value means the capture began after
// its first producer, and a later re-issue is still worth extracting.
func clientOriginCheckApplies(ruleID string) bool {
	switch ruleID {
	case RuleSetCookieOther, RuleLocationParam, RuleDynamicHeader, RuleJSONDynamicID, RuleJSONNumericID, RuleXMLDynamicID:
		return true
	default:
		return false
	}
}

// identifierName reports a digit-free value built from word segments
// ("actionBar", "SAMLPartner", "Retail_Home_Page", "SUCCESS"): every segment
// has two or more letters, at least one has four or more, and each of those
// contains a vowel. Generated identifiers carry digits, are long, or split
// into short irregular segments ("YWJjZGVm", "xKqPzLmWbE").
func identifierName(value string) bool {
	if value == "" || len(value) > maxDynamicIDNameBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		c := value[index]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case c == '_', c == '-', c == '.', c == ':', c == '/', c == ' ':
		default:
			return false
		}
	}
	tokens := correlationKeyTokens(value)
	word := false
	for _, token := range tokens {
		if len(token) < 2 {
			return false
		}
		if len(token) >= 4 {
			if !strings.ContainsAny(token, "aeiouy") {
				return false
			}
			word = true
		}
	}
	return word
}

// httpAuthenticationScheme reports a registered HTTP authentication scheme
// name, the value of OAuth token_type fields.
func httpAuthenticationScheme(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "bearer", "basic", "digest", "mac", "negotiate", "ntlm", "oauth", "token", "hoba", "mutual", "dpop", "pop", "scram-sha-1", "scram-sha-256", "vapid":
		return true
	default:
		return false
	}
}

// bareURLOrigin reports an absolute http(s) URL with no path beyond "/", no
// query and no fragment. Hosts are retargeted through base_url, never through
// an extractor on a redirect.
func bareURLOrigin(value string) bool {
	if !strings.HasPrefix(strings.ToLower(value), "http://") && !strings.HasPrefix(strings.ToLower(value), "https://") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return false
	}
	return (parsed.Path == "" || parsed.Path == "/") && parsed.RawQuery == "" && !parsed.ForceQuery && parsed.Fragment == ""
}

// newClientObservationSearch searches the request surfaces that the client
// authors itself. Cookie and Authorization values are left out: they carry
// state issued by earlier responses, including responses whose Set-Cookie a
// HAR exporter stripped, so an earlier occurrence there does not show that
// the client originated the value.
func newClientObservationSearch(observation Observation) observationSearch {
	search := observationSearch{
		rawSurfaces: make([]string, 0, 2+len(observation.RequestHeaders)),
	}
	search.addSurface(observation.RequestURL)
	search.addSurface(observation.RequestBody)
	for name, headerValue := range observation.RequestHeaders {
		if strings.EqualFold(name, "Cookie") || strings.EqualFold(name, "Authorization") {
			continue
		}
		search.addSurface(headerValue)
	}
	search.addFormValues(observation.RequestBody)
	return search
}
