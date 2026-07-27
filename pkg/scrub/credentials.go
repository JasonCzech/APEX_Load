package scrub

import (
	"encoding/json"
	"io"
	"net/url"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
)

// Selective credential redaction for recorded baselines.
//
// A recorded baseline feeds the correlation engine, which keys off DYNAMIC
// values (session ids, CSRF, view-state) found in responses and reused in later
// requests. The general scrubbers in this package redact those very tokens, so
// they cannot be used here. Instead we redact only clearly-static, user-supplied
// secrets on the REQUEST side (passwords, Basic auth, API keys) and leave
// responses and dynamic request values intact so correlation still works.

func isCredentialField(name string) bool {
	return credential.StaticField(name)
}

// IsCredentialField reports whether name is a shared static-credential field.
// Importers use this to keep source sanitization aligned with recorder scrubbing.
func IsCredentialField(name string) bool { return isCredentialField(name) }

// RedactCredentialHeaders returns a copy of request headers with static
// credential headers redacted (Basic auth, API keys) while preserving
// Bearer/session/CSRF tokens that correlation relies on. The input is unchanged.
func RedactCredentialHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		lower := strings.ToLower(strings.TrimSpace(name))
		switch {
		case lower == "authorization" || lower == "proxy-authorization":
			// Basic auth carries base64(user:password) — a static credential.
			// Bearer and other schemes may be dynamic tokens; keep them.
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "basic ") {
				out[name] = Redacted
			} else {
				out[name] = value
			}
		case lower == "x-api-key" || lower == "api-key" || lower == "apikey":
			out[name] = Redacted
		default:
			out[name] = value
		}
	}
	return out
}

// RedactCredentialBody redacts password/secret/api-key fields in a JSON or
// form-encoded request body, leaving every other value — including dynamic
// correlation candidates — intact. Unrecognised bodies are returned unchanged.
func RedactCredentialBody(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return body
	}
	redactNestedStaticQuery := func(value string) string {
		return redactSensitiveQueryAssignments(value, isCredentialField)
	}
	if strings.Contains(strings.ToLower(trimmed), "content-disposition:") && strings.Contains(strings.ToLower(trimmed), "form-data") {
		if redacted := redactCredentialMultipart(body); redacted != body {
			return redactNestedStaticQuery(redacted)
		}
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		var parsed any
		decoder := json.NewDecoder(strings.NewReader(trimmed))
		decoder.UseNumber()
		if err := decoder.Decode(&parsed); err == nil {
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				return redactNestedStaticQuery(redactCredentialJSONFallback(body))
			}
			if redactJSONCredentials(parsed) {
				if out, err := json.Marshal(parsed); err == nil {
					return redactNestedStaticQuery(string(out))
				}
			}
			return redactNestedStaticQuery(body)
		}
		return redactNestedStaticQuery(redactCredentialJSONFallback(body))
	}
	if trimmed[0] == '<' {
		if redacted := redactCredentialXML(body); redacted != body {
			return redactNestedStaticQuery(redacted)
		}
		return redactNestedStaticQuery(body)
	}
	if strings.Contains(trimmed, "=") {
		values, err := url.ParseQuery(trimmed)
		if err == nil {
			changed := false
			for key := range values {
				if isCredentialField(key) {
					for i := range values[key] {
						values[key][i] = Redacted
					}
					changed = true
				}
			}
			if changed {
				return redactNestedStaticQuery(values.Encode())
			}
		}
		if redacted := redactSensitiveQueryAssignments(body, isCredentialField); redacted != body {
			return redacted
		}
	}
	return redactNestedStaticQuery(body)
}

func redactCredentialXML(body string) string {
	return redactXMLFields(body, func(name string) bool {
		return isCredentialField(xmlLocalName(name))
	})
}

func redactCredentialMultipart(body string) string {
	return redactMultipartFields(body, isCredentialField)
}

func redactCredentialJSONFallback(body string) string {
	return redactJSONFallbackFields(body, isCredentialField)
}

// redactJSONCredentials walks a decoded JSON value and redacts credential
// fields in place. Returns true if anything was redacted.
func redactJSONCredentials(node any) bool {
	changed := false
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			if isCredentialField(key) {
				typed[key] = Redacted
				changed = true
				continue
			}
			if redactJSONCredentials(value) {
				changed = true
			}
		}
	case []any:
		for _, item := range typed {
			if redactJSONCredentials(item) {
				changed = true
			}
		}
	}
	return changed
}
