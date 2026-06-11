// Package scrub is the shared scrub-on-write component for captured traffic
// artifacts. Recorder sessions, runtime canary HAR exports, and any other
// HAR-with-bodies artifact pass through it before persistence so credentials,
// session tokens, and configured PII patterns never reach disk. Unlike
// design-time parameterization (pkg/convert/paramize), which replaces secrets
// with runnable templates, scrubbing is irreversible redaction.
package scrub

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Redacted replaces every scrubbed value.
const Redacted = "[REDACTED]"

// Rule is one custom pattern → replacement rewrite applied to values, bodies,
// and HAR content.
type Rule struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement,omitempty"`
}

// Policy configures scrubbing. The zero value applies the built-in protections
// (auth headers, cookies, token-bearing query/body fields).
type Policy struct {
	// ExtraHeaders extends the built-in sensitive header set.
	ExtraHeaders []string `json:"extra_headers,omitempty"`
	// ExtraPatterns are customer-defined PII/sensitive-value rules.
	ExtraPatterns []Rule `json:"extra_patterns,omitempty"`
	// KeepCookies disables cookie redaction (cookies are scrubbed by default).
	KeepCookies bool `json:"keep_cookies,omitempty"`
}

// sensitiveHeaders are always redacted (case-insensitive match).
var sensitiveHeaders = map[string]bool{
	"authorization":        true,
	"proxy-authorization":  true,
	"x-api-key":            true,
	"api-key":              true,
	"x-auth-token":         true,
	"x-access-token":       true,
	"x-csrf-token":         true,
	"x-xsrf-token":         true,
	"x-session-id":         true,
	"x-amz-security-token": true,
}

var cookieHeaders = map[string]bool{
	"cookie":     true,
	"set-cookie": true,
}

// sensitiveParamPattern redacts token-bearing query string and form fields.
var sensitiveParamPattern = regexp.MustCompile(`(?i)((?:^|[&?])(?:password|passwd|secret|token|access_token|refresh_token|id_token|api_key|apikey|session|session_id|sessionid|auth|csrf|xsrf)=)([^&\s]+)`)

// sensitiveJSONPattern redacts token-bearing JSON body fields.
var sensitiveJSONPattern = regexp.MustCompile(`(?i)("(?:password|passwd|secret|token|access_token|refresh_token|id_token|api_key|apikey|session_id|csrf_token|client_secret)"\s*:\s*")([^"]*)(")`)

// SensitiveHeader reports whether a header is always scrubbed under the policy.
func (p Policy) SensitiveHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if sensitiveHeaders[lower] {
		return true
	}
	if !p.KeepCookies && cookieHeaders[lower] {
		return true
	}
	for _, extra := range p.ExtraHeaders {
		if strings.EqualFold(strings.TrimSpace(extra), lower) {
			return true
		}
	}
	return false
}

// Headers returns a copy of the map with sensitive headers redacted.
func Headers(headers map[string]string, policy Policy) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		if policy.SensitiveHeader(key) {
			out[key] = Redacted
			continue
		}
		out[key] = Value(value, policy)
	}
	return out
}

// Value applies the built-in body/query token patterns and the policy's custom
// rules to a string.
func Value(value string, policy Policy) string {
	if value == "" {
		return value
	}
	value = sensitiveJSONPattern.ReplaceAllString(value, "${1}"+Redacted+"${3}")
	value = sensitiveParamPattern.ReplaceAllString(value, "${1}"+Redacted)
	for _, rule := range policy.ExtraPatterns {
		pattern, err := regexp.Compile(rule.Pattern)
		if err != nil {
			continue
		}
		replacement := rule.Replacement
		if replacement == "" {
			replacement = Redacted
		}
		value = pattern.ReplaceAllString(value, replacement)
	}
	return value
}

// HAR scrubs a HAR document: request/response headers and cookies, post data,
// query strings, and URLs across every entry, plus response bodies via the
// built-in and custom patterns. Unknown HAR fields are preserved verbatim.
// The returned document is always re-marshaled; the input is not modified.
func HAR(data []byte, policy Policy) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse HAR for scrubbing: %w", err)
	}
	log, _ := doc["log"].(map[string]any)
	if log == nil {
		return nil, fmt.Errorf("HAR document has no log section")
	}
	entries, _ := log["entries"].([]any)
	for _, rawEntry := range entries {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			continue
		}
		if request, ok := entry["request"].(map[string]any); ok {
			scrubNameValueList(request, "headers", policy)
			scrubNameValueList(request, "cookies", policy)
			scrubNameValueList(request, "queryString", policy)
			if rawURL, ok := request["url"].(string); ok {
				request["url"] = Value(rawURL, policy)
			}
			if postData, ok := request["postData"].(map[string]any); ok {
				if text, ok := postData["text"].(string); ok {
					postData["text"] = Value(text, policy)
				}
				scrubNameValueList(postData, "params", policy)
			}
		}
		if response, ok := entry["response"].(map[string]any); ok {
			scrubNameValueList(response, "headers", policy)
			scrubNameValueList(response, "cookies", policy)
			if content, ok := response["content"].(map[string]any); ok {
				if text, ok := content["text"].(string); ok {
					content["text"] = Value(text, policy)
				}
			}
		}
	}
	annotateScrubbed(log)
	return json.Marshal(doc)
}

// scrubNameValueList handles HAR's [{name, value}] collections. Cookie
// collections are fully redacted; for headers only sensitive names are
// redacted; query/post params are redacted when the name matches the
// sensitive-parameter set.
func scrubNameValueList(parent map[string]any, key string, policy Policy) {
	items, _ := parent[key].([]any)
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		name, _ := item["name"].(string)
		value, _ := item["value"].(string)
		switch key {
		case "cookies":
			if !policy.KeepCookies {
				item["value"] = Redacted
			}
		case "headers":
			if policy.SensitiveHeader(name) {
				item["value"] = Redacted
			} else {
				item["value"] = Value(value, policy)
			}
		default: // queryString, params
			if sensitiveParamName(name) {
				item["value"] = Redacted
			} else {
				item["value"] = Value(value, policy)
			}
		}
	}
}

var sensitiveParamNames = map[string]bool{
	"password": true, "passwd": true, "secret": true, "token": true,
	"access_token": true, "refresh_token": true, "id_token": true,
	"api_key": true, "apikey": true, "session": true, "session_id": true,
	"sessionid": true, "auth": true, "csrf": true, "xsrf": true,
	"client_secret": true, "csrf_token": true,
}

func sensitiveParamName(name string) bool {
	return sensitiveParamNames[strings.ToLower(strings.TrimSpace(name))]
}

// Sensitivity labels for artifact metadata, surfaced in API/UI responses so
// reviewers know whether an artifact passed through scrub-on-write.
const (
	SensitivityScrubbed = "scrubbed"
	SensitivityRaw      = "raw_contains_secrets"
)

// annotateScrubbed marks the HAR log so consumers can verify the artifact
// passed through scrub-on-write.
func annotateScrubbed(log map[string]any) {
	log["_apexloadSensitivity"] = SensitivityScrubbed
}

// HARSensitivity reads the sensitivity label from a HAR document, returning
// SensitivityRaw when the artifact never passed through scrub-on-write.
func HARSensitivity(data []byte) string {
	var doc struct {
		Log struct {
			Sensitivity string `json:"_apexloadSensitivity"`
		} `json:"log"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return SensitivityRaw
	}
	if doc.Log.Sensitivity == SensitivityScrubbed {
		return SensitivityScrubbed
	}
	return SensitivityRaw
}
