package credentialpolicy

import (
	"net/url"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
)

// ContainsCredentialURL reports whether text embeds an HTTP(S) URL with
// userinfo or a governed query/fragment parameter. It is intended for non-request
// execution surfaces (for example process arguments) where any credential URL
// must be rejected rather than projected onto the wire.
func ContainsCredentialURL(text string) bool {
	lower := strings.ToLower(text)
	for cursor := 0; cursor < len(text); {
		httpAt := strings.Index(lower[cursor:], "http://")
		httpsAt := strings.Index(lower[cursor:], "https://")
		relative := httpAt
		if relative < 0 || (httpsAt >= 0 && httpsAt < relative) {
			relative = httpsAt
		}
		if relative < 0 {
			return false
		}
		start := cursor + relative
		end := start
		for end < len(text) && !strings.ContainsRune(" \t\r\n", rune(text[end])) {
			end++
		}
		candidate := strings.TrimRight(text[start:end], `"'(),;]}`)
		parsed, err := url.Parse(candidate)
		if err == nil && parsed.Host != "" {
			if parsed.User != nil {
				return true
			}
			for name, values := range parsed.Query() {
				if credential.SensitiveName(name) && len(values) > 0 {
					return true
				}
			}
			if credentialURLFragment(parsed.Fragment) {
				return true
			}
		}
		cursor = max(end, start+1)
	}
	return false
}

func credentialURLFragment(fragment string) bool {
	fragment = strings.TrimSpace(fragment)
	if fragment == "" {
		return false
	}
	// Router fragments often carry their own query string
	// (#/callback?token=...). Check both the whole fragment and the query-like
	// suffix. Malformed escaping still fails closed through the manual scan.
	candidates := []string{fragment}
	if question := strings.IndexByte(fragment, '?'); question >= 0 && question+1 < len(fragment) {
		candidates = append(candidates, fragment[question+1:])
	}
	for _, candidate := range candidates {
		for _, field := range strings.FieldsFunc(candidate, func(r rune) bool {
			return r == '&' || r == ';'
		}) {
			field = strings.TrimSpace(field)
			if field == "" {
				continue
			}
			name := field
			hasValue := false
			if separator := strings.IndexByte(field, '='); separator >= 0 {
				name = field[:separator]
				hasValue = strings.TrimSpace(field[separator+1:]) != ""
			}
			if decoded, err := url.QueryUnescape(name); err == nil {
				name = decoded
			}
			if credential.SensitiveName(name) && (hasValue || !strings.Contains(field, "=")) {
				// A bare governed name (#token) is ambiguous on a
				// process-observable surface, so reject it conservatively.
				return true
			}
		}
	}
	return false
}
