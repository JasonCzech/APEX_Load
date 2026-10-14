package correlate

import (
	"net/url"
	"strings"
)

// A correlation replaces a recorded literal with a value extracted from an
// earlier response, and its extractor fails the step, or for a less certain
// correlation falls back to the recorded value, when the value is missing.
// Values that are not server-generated state gain nothing from that and add
// a failure point, so detection rejects four provable cases:
//
//   - name- and word-shaped values from the generic rules, whose only
//     evidence is a key, cookie or header name ("navigation",
//     "Retail_Home_Page", a cookie "qualified", "Session" in a
//     "...SessionStorageDurationText" field), and HTTP authentication scheme
//     names from token keys ("Bearer" in token_type);
//   - short or repetitive values from the weakest of those rules (a cookie
//     "en_US"), which are not opaque tokens;
//   - bare URL origins, which are environment configuration (base_url);
//   - values the client already sent before the producing response, for the
//     weak heuristic rules (see clientOriginCheckApplies).

const maxDynamicIDNameBytes = 32

// maxWordSegmentBytes caps a word for the credential, session and cookie
// rules: a single-case run of random letters ("qwhfjdkslaieurty") is one
// identifierName segment, but a word rarely runs past 12 letters.
const maxWordSegmentBytes = 12

// Token-shaped values for the weakest rules are at least this long and use
// at least this many distinct characters.
const (
	minWeakRuleTokenBytes         = 6
	minWeakRuleTokenDistinctBytes = 4
)

// candidateValueCanCorrelate applies the value-only precision rules.
func candidateValueCanCorrelate(candidate Candidate) bool {
	if bareURLOrigin(candidate.Value) {
		return false
	}
	if (candidate.RuleID == RuleJSONDynamicID || candidate.RuleID == RuleXMLDynamicID) && identifierName(candidate.Value) {
		return false
	}
	if genericRule(candidate.RuleID) && wordValue(candidate.Value) {
		return false
	}
	switch candidate.RuleID {
	case RuleJSONAuthToken, RuleJSONSessionID:
		return !httpAuthenticationScheme(candidate.Value)
	case RuleSetCookieOther, RuleDynamicHeader, RuleLocationParam, RuleJSONDynamicID, RuleXMLDynamicID:
		return tokenShaped(candidate.Value)
	default:
		return true
	}
}

// genericRule selects the rules whose evidence is a key, cookie or header
// name rather than a protocol the value belongs to. A word is never the
// token such a name promises. Platform rules are exempt: an Aura app
// descriptor or a Location path is name-shaped by design.
func genericRule(ruleID string) bool {
	switch ruleID {
	case RuleJSONAuthToken, RuleJSONCSRFToken, RuleJSONSessionID, RuleJSONDynamicID,
		RuleHTMLHiddenToken, RuleXMLSOAPToken, RuleXMLDynamicID,
		RuleSetCookieSession, RuleSetCookieOther, RuleDynamicHeader, RuleLocationParam:
		return true
	default:
		return false
	}
}

// wordValue reports an identifierName value made of word-length segments.
func wordValue(value string) bool {
	if !identifierName(value) {
		return false
	}
	for _, token := range correlationKeyTokens(value) {
		if len(token) > maxWordSegmentBytes {
			return false
		}
	}
	return true
}

// tokenShaped reports a value long and varied enough to be an opaque token
// rather than a short setting ("en_US", "0000").
func tokenShaped(value string) bool {
	if len(value) < minWeakRuleTokenBytes {
		return false
	}
	var seen [256]bool
	distinct := 0
	for index := 0; index < len(value) && distinct < minWeakRuleTokenDistinctBytes; index++ {
		if !seen[value[index]] {
			seen[value[index]] = true
			distinct++
		}
	}
	return distinct >= minWeakRuleTokenDistinctBytes
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
