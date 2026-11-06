package credentialpolicy

import (
	"net/url"
	"slices"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
)

// URLCredentialValues returns the credential parts of a URL value:
// its userinfo and the values of credential-named query or fragment
// parameters (credential.SensitiveName), each as written and percent-decoded.
// The value is scanned as text, not through url.Parse, so a templated host
// (${IDP_HOST}), a schemeless or unparseable address and a fragment are all
// covered. A parameter whose name cannot be decoded or is a variable
// reference, or userinfo that cannot be split, cannot be judged, so the whole
// value is returned (fail closed).
func URLCredentialValues(raw string) []string {
	raw = strings.TrimSpace(raw)
	var values []string
	add := func(value string) {
		values = append(values, value)
		if decoded, err := url.QueryUnescape(value); err == nil && decoded != value {
			values = append(values, decoded)
		}
	}
	if at := strings.IndexAny(raw, "?#"); at >= 0 {
		for _, pair := range strings.FieldsFunc(raw[at+1:], func(r rune) bool { return r == '&' || r == ';' || r == '?' || r == '#' }) {
			name, value, _ := strings.Cut(pair, "=")
			decoded, err := url.QueryUnescape(name)
			if err != nil || strings.Contains(decoded, "${") {
				return []string{raw}
			}
			if credential.SensitiveName(decoded) && credential.MatchableNamedValue(decoded, value) {
				add(value)
			}
		}
	}
	// Userinfo sits between "//" (or the start of a schemeless value) and the
	// first '/', '?' or '#'. Without "//" only a user:password form counts,
	// so a plain name@domain value is not treated as a username.
	authority, slashes := raw, false
	if scheme, rest, ok := strings.Cut(raw, "//"); ok && !strings.ContainsAny(scheme, "/?#") {
		authority, slashes = rest, true
	}
	if end := strings.IndexAny(authority, "/?#"); end >= 0 {
		authority = authority[:end]
	}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		// Userinfo holding whitespace, a quote, '=' or ';' (base64 padding, a
		// JAAS-like value) cannot be split with confidence: fail closed.
		if strings.ContainsAny(authority, " \t\r\n\"'=;") {
			return []string{raw}
		}
		username, password, hasPassword := strings.Cut(authority[:at], ":")
		if (slashes || hasPassword) && credential.MatchableNamedValue("username", username) {
			add(username)
		}
		if hasPassword {
			add(password)
		}
	}
	return slices.DeleteFunc(values, func(v string) bool { return strings.TrimSpace(v) == "" })
}
