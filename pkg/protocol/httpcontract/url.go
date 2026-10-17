package httpcontract

import (
	"regexp"
	"strings"
)

var (
	absoluteHTTPURLBoundaryWhitespace = regexp.MustCompile(`^(https?://[^/?#\s]+)\s+([/?#].*)$`)

	leadingTemplateBaseURLPattern = regexp.MustCompile(`^\{\{\s*(?:var|param)\.[^{}]+\s*\}\}\s*`)
)

func ResolveURL(baseURL string, target string) string {
	target = NormalizeURLTarget(target)
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return target
	}
	target = TrimLeadingTemplateBaseURL(target)
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(target, "/")
}

func NormalizeURLTarget(target string) string {
	target = strings.TrimSpace(target)
	return absoluteHTTPURLBoundaryWhitespace.ReplaceAllString(target, "$1$2")
}

func TrimLeadingTemplateBaseURL(target string) string {
	return leadingTemplateBaseURLPattern.ReplaceAllString(strings.TrimSpace(target), "")
}
