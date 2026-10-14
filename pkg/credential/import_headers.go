package credential

import (
	"strings"
	"unicode"
)

// recordedStateWords mark headers that carry state the source tool recorded or
// generated for one session or request: session identifiers, anti-forgery
// tokens and tracing or correlation identifiers.
var recordedStateWords = map[string]struct{}{
	"session": {}, "sessionid": {}, "sessiontoken": {}, "sid": {},
	"csrf": {}, "csrftoken": {}, "xsrf": {}, "xsrftoken": {},
	"trace": {}, "traceid": {}, "traceparent": {}, "tracestate": {},
	"span": {}, "spanid": {}, "correlation": {}, "correlationid": {},
	"conversation": {}, "conversationid": {},
}

// credentialWords name what an operator supplies to authenticate. Present in
// a header name, they outweigh a recorded-state word: X-Span-Auth-Token is an
// auth token, X-Session-Token is session state.
var credentialWords = map[string]struct{}{
	"auth": {}, "authorization": {}, "authtoken": {}, "apikey": {},
	"password": {}, "passwd": {}, "secret": {}, "clientsecret": {}, "privatekey": {},
}

// ImportCredentialHeader reports whether an imported request header carries a
// credential an operator supplies, such as Authorization, an API key or an
// auth token. Importers route only these headers to credential review.
//
// Cookies and session, anti-forgery and correlation headers are state the
// source recorded. By product decision an imported script's own values are not
// secrets, so those headers stay as imported. Log and evidence redaction still
// use the broader SensitiveHeader and AlwaysSensitiveHeader checks.
func ImportCredentialHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" || CookieHeader(lower) {
		return false
	}
	words := headerNameWords(lower)
	if hasHeaderWord(words, recordedStateWords) && !hasHeaderWord(words, credentialWords) {
		return false
	}
	return AlwaysSensitiveHeader(lower)
}

func headerNameWords(lower string) []string {
	return strings.FieldsFunc(lower, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r))
	})
}

// hasHeaderWord reports whether any word of a header name, alone or joined to
// the next word, is in vocabulary.
func hasHeaderWord(words []string, vocabulary map[string]struct{}) bool {
	for index, word := range words {
		if _, ok := vocabulary[word]; ok {
			return true
		}
		if index+1 < len(words) {
			if _, ok := vocabulary[word+words[index+1]]; ok {
				return true
			}
		}
	}
	return false
}
