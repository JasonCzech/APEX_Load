package credentialpolicy

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

// ValidRawURL reports whether raw can be replayed as an HTTP(S) URL or a
// relative HTTP request target without normalizing its authored bytes.
// Runtime templates are replaced only in a transient validation copy.
func ValidRawURL(raw string, options Options) bool {
	materialized, _ := MaterializeSourceVariables(raw, options)
	if !utf8.ValidString(materialized) {
		return false
	}
	parseable, ok := replaceRawURLTemplates(materialized)
	if !ok {
		return false
	}
	if strings.IndexFunc(parseable, func(r rune) bool {
		return r < ' ' || r == 0x7f || r == ' '
	}) >= 0 {
		return false
	}
	parsed, err := url.Parse(parseable)
	if err != nil {
		return false
	}
	if parsed.Scheme == "" {
		// RFC 3986 network-path references still require an authority. Ordinary
		// relative request targets are resolved against the script base URL.
		return !strings.HasPrefix(parseable, "//") || parsed.Host != ""
	}
	// Go accepts opaque forms such as https:user@example.test, but net/http
	// cannot replay those as hierarchical requests.
	if parsed.Opaque != "" || parsed.Host == "" {
		return false
	}
	if rawURLSchemeIsDynamic(materialized) {
		return true
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return true
	default:
		return false
	}
}

func rawURLSchemeIsDynamic(value string) bool {
	colon := strings.IndexByte(value, ':')
	if colon < 0 {
		return false
	}
	firstDelimiter := len(value)
	if delimiter := strings.IndexAny(value, "/?#"); delimiter >= 0 {
		firstDelimiter = delimiter
	}
	return colon < firstDelimiter && strings.Contains(value[:colon], "{{")
}

func replaceRawURLTemplates(value string) (string, bool) {
	var output strings.Builder
	for position := 0; position < len(value); {
		start := strings.Index(value[position:], "{{")
		if start < 0 {
			remainder := value[position:]
			if strings.Contains(remainder, "}}") {
				return "", false
			}
			output.WriteString(remainder)
			break
		}
		start += position
		literal := value[position:start]
		if strings.Contains(literal, "}}") {
			return "", false
		}
		output.WriteString(literal)
		end := strings.Index(value[start+2:], "}}")
		if end < 0 {
			return "", false
		}
		end += start + 2
		expression := value[start+2 : end]
		if strings.TrimSpace(expression) == "" ||
			strings.Contains(expression, "{{") ||
			strings.IndexFunc(expression, func(r rune) bool {
				return r < ' ' || r == 0x7f
			}) >= 0 {
			return "", false
		}
		replacement := "apex"
		if rawURLTemplateIsPort(value, start) {
			replacement = "443"
		}
		output.WriteString(replacement)
		position = end + 2
	}
	return output.String(), true
}

func rawURLTemplateIsPort(value string, start int) bool {
	authorityStart := -1
	if scheme := strings.Index(value, "://"); scheme >= 0 {
		authorityStart = scheme + 3
	} else if strings.HasPrefix(value, "//") {
		authorityStart = 2
	}
	if authorityStart < 0 || start < authorityStart {
		return false
	}
	authorityEnd := len(value)
	if end := strings.IndexAny(value[authorityStart:], "/?#"); end >= 0 {
		authorityEnd = authorityStart + end
	}
	if start >= authorityEnd {
		return false
	}
	if at := strings.LastIndex(value[authorityStart:authorityEnd], "@"); at >= 0 &&
		start < authorityStart+at {
		return false
	}
	colon := strings.LastIndex(value[authorityStart:start], ":")
	if colon < 0 {
		return false
	}
	colon += authorityStart
	if bracket := strings.LastIndex(value[authorityStart:start], "]"); bracket >= 0 &&
		authorityStart+bracket > colon {
		return false
	}
	for _, current := range value[colon+1 : start] {
		if current < '0' || current > '9' {
			return false
		}
	}
	return true
}
