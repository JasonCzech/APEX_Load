package sample

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
)

// HARSanitizedExportCode marks a capture that looks like a sanitized browser
// export: Chrome DevTools' default "Export HAR" strips Cookie, Set-Cookie and
// Authorization headers and empties every cookies[] array.
const HARSanitizedExportCode = "har_sanitized_export"

// harSanitizedExportMinEntries is how many entries a capture needs before
// having no cookie at all is suspicious without a sign-in or redirect.
const harSanitizedExportMinEntries = 20

// harSessionPathMarkers are request path segment words of a sign-in flow,
// which always carries session cookies when they are kept. They match whole
// words of a segment, so /sso/start, /saml-login and /sign-in match but
// /accessories, /lessons and /bloginfo do not.
var harSessionPathMarkers = []string{"login", "logon", "signin", "oauth", "oauth2", "authorize", "saml", "sso"}

// LooksSanitized reports whether the capture holds no cookie or credential
// evidence at all: no request Cookie or Authorization header, no response
// Set-Cookie header and no cookies[] entry in any entry. A browser session
// always has some, so a session captured like that was exported with them
// removed. It is the evidence half of the capture-level sanitized-export
// warning; callers that already know the capture is a session, such as one
// sending a recorded aura.token, need nothing more.
func (d *HARDocument) LooksSanitized() bool {
	if d == nil || len(d.items) == 0 {
		return false
	}
	return harLooksSanitized(d.items)
}

func harLooksSanitized(items []map[string]any) bool {
	for _, item := range items {
		request, _ := item["request"].(map[string]any)
		response, _ := item["response"].(map[string]any)
		if harHeaderPresent(request["headers"], "Cookie") || harHeaderPresent(request["headers"], "Authorization") ||
			harHeaderPresent(response["headers"], "Set-Cookie") ||
			harListPresent(request["cookies"]) || harListPresent(response["cookies"]) {
			return false
		}
	}
	return true
}

// harSanitizedExportNote returns the capture-level warning for a capture of a
// browser session that has no cookie or Authorization evidence at all, or ""
// for any other capture. A session is a sign-in or redirect flow, or at least
// harSanitizedExportMinEntries entries; an Authorization header anywhere
// means the export kept credentials and was not sanitized.
func harSanitizedExportNote(items []map[string]any) string {
	if len(items) == 0 || !harLooksSanitized(items) {
		return ""
	}
	session := len(items) >= harSanitizedExportMinEntries
	for _, item := range items {
		if session {
			break
		}
		session = harSessionFlowEntry(item)
	}
	if !session {
		return ""
	}
	return fmt.Sprintf("None of the capture's %d entries has a Cookie, Set-Cookie or Authorization header or a cookies[] entry, so it looks like a sanitized export (Chrome DevTools' default \"Export HAR\" removes them). Session tokens the server issues in cookies cannot be correlated from it, and replay sends their recorded, stale values. Re-export with Chrome DevTools \"Export HAR (with sensitive data)\", or record with the APEX recorder.", len(items))
}

// addHARSanitizedExportWarning reports a sanitized capture once, as a coded
// diagnostic and a conversion warning.
func addHARSanitizedExportWarning(items []map[string]any, diagnostics *diag.Collector, warnings []string) []string {
	note := harSanitizedExportNote(items)
	if note == "" {
		return warnings
	}
	diagnostics.AddCoded(diag.Warning, HARSanitizedExportCode, "HAR sanitized export", "HAR import", note)
	return append(warnings, note)
}

// harSessionFlowEntry reports a redirect or a sign-in request. A redirect is
// a 301, 302, 303, 307 or 308, or any response with a Location header; a 304
// is a cache revalidation, not a redirect.
func harSessionFlowEntry(item map[string]any) bool {
	request, _ := item["request"].(map[string]any)
	if response, ok := item["response"].(map[string]any); ok {
		switch intValue(response["status"]) {
		case 301, 302, 303, 307, 308:
			return true
		}
		if harHeaderPresent(response["headers"], "Location") {
			return true
		}
	}
	parsed, err := url.Parse(stringValue(request["url"]))
	if err != nil {
		return false
	}
	for _, segment := range strings.Split(strings.ToLower(parsed.Path), "/") {
		// A hyphenated or underscored segment also matches joined up, so
		// /sign-in and /log_on count as signin and logon.
		if slices.Contains(harSessionPathMarkers, strings.NewReplacer("-", "", "_", "").Replace(segment)) {
			return true
		}
		words := strings.FieldsFunc(segment, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9')
		})
		for _, word := range words {
			if slices.Contains(harSessionPathMarkers, word) {
				return true
			}
		}
	}
	return false
}

func harListPresent(raw any) bool {
	items, _ := raw.([]any)
	return len(items) > 0
}
