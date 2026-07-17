package scrub

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

// Selective credential redaction for recorded baselines.
//
// A recorded baseline feeds the correlation engine, which keys off DYNAMIC
// values (session ids, CSRF, view-state) found in responses and reused in later
// requests. The general scrubbers in this package redact those very tokens, so
// they cannot be used here. Instead we redact only clearly-static, user-supplied
// secrets on the REQUEST side (passwords, Basic auth, API keys) and leave
// responses and dynamic request values intact so correlation still works.

// credentialFieldNames are request body fields holding static secrets that are
// safe to redact — none of these are correlation candidates.
var credentialFieldNames = map[string]bool{
	"password": true, "passwd": true, "pwd": true, "pass": true,
	"secret": true, "client_secret": true, "clientsecret": true,
	"api_key": true, "apikey": true, "private_key": true,
	"privatekey": true, "secret_key": true, "secretkey": true,
	"access_key": true, "accesskey": true,
	"passphrase": true, "access_token": true, "accesstoken": true,
	"refresh_token": true, "refreshtoken": true,
}

func isCredentialField(name string) bool {
	normalized := normalizeCredentialFieldName(name)
	return credentialFieldNames[normalized]
}

// IsCredentialField reports whether name is a shared static-credential field.
// Importers use this to keep source sanitization aligned with recorder scrubbing.
func IsCredentialField(name string) bool { return isCredentialField(name) }

func normalizeCredentialFieldName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "-", "_")
	return name
}

var (
	credentialXMLPattern    = regexp.MustCompile(`(?is)(<(?:[\w.-]+:)?(?:password|passwd|pwd|pass|secret|client[_-]?secret|api[_-]?key|apikey|private[_-]?key|secret[_-]?key|access[_-]?key)\b[^>]*>)(.*?)(</(?:[\w.-]+:)?(?:password|passwd|pwd|pass|secret|client[_-]?secret|api[_-]?key|apikey|private[_-]?key|secret[_-]?key|access[_-]?key)>)`)
	credentialXMLAttrDouble = regexp.MustCompile(`(?is)(^|[^\w-])((?:password|passwd|pwd|pass|secret|client[_-]?secret|api[_-]?key|apikey|private[_-]?key|secret[_-]?key|access[_-]?key)\s*=\s*")([^"]*)(")`)
	credentialXMLAttrSingle = regexp.MustCompile(`(?is)(^|[^\w-])((?:password|passwd|pwd|pass|secret|client[_-]?secret|api[_-]?key|apikey|private[_-]?key|secret[_-]?key|access[_-]?key)\s*=\s*')([^']*)(')`)
	credentialMultipartPart = regexp.MustCompile(`(?is)(Content-Disposition:\s*form-data;[^\r\n]*\bname="(?:password|passwd|pwd|pass|secret|client[_-]?secret|api[_-]?key|apikey|private[_-]?key|secret[_-]?key|access[_-]?key)"[^\r\n]*(?:\r?\nContent-[^\r\n]*)*\r?\n\r?\n)(.*?)(\r?\n--)`)
)

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
	if strings.Contains(strings.ToLower(trimmed), "content-disposition:") && strings.Contains(strings.ToLower(trimmed), "form-data") {
		if redacted := redactCredentialMultipart(body); redacted != body {
			return redacted
		}
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		var parsed any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			if redactJSONCredentials(parsed) {
				if out, err := json.Marshal(parsed); err == nil {
					return string(out)
				}
			}
		}
		return body
	}
	if trimmed[0] == '<' {
		if redacted := redactCredentialXML(body); redacted != body {
			return redacted
		}
		return body
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
				return values.Encode()
			}
		}
	}
	return body
}

func redactCredentialXML(body string) string {
	redacted := credentialXMLPattern.ReplaceAllString(body, "${1}"+Redacted+"${3}")
	redacted = credentialXMLAttrDouble.ReplaceAllString(redacted, "${1}${2}"+Redacted+"${4}")
	redacted = credentialXMLAttrSingle.ReplaceAllString(redacted, "${1}${2}"+Redacted+"${4}")
	return redacted
}

func redactCredentialMultipart(body string) string {
	return credentialMultipartPart.ReplaceAllString(body, "${1}"+Redacted+"${3}")
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
