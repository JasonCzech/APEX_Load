// Package credential defines source-neutral credential naming and classification
// rules shared by importers, scrubbers, parameterization, and runtime-facing
// authoring helpers.
//
// The classifiers are intentionally separate:
//   - SensitiveName recognizes the complete protected-value vocabulary.
//   - SensitiveHeader recognizes the broader set of HTTP header names that
//     authoring flows should govern.
//   - StaticField recognizes request fields that recorder correlation may safely
//     redact without removing dynamic session values.
//
// Keeping those policies distinct preserves the existing security boundaries:
// a broad import-time header rule must not accidentally remove correlation
// candidates from a recorded baseline.
package credential

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

const (
	// Redacted is the repository-wide disclosure sentinel.
	Redacted = "[REDACTED]"

	// MaxLogicalNameBytes is constrained by the portable {{secret.NAME}}
	// template grammar: "secret." consumes seven of the 256 expression bytes.
	MaxLogicalNameBytes = 249

	// LogicalNameInputPattern is the compatibility grammar accepted by existing
	// script and secret-management APIs. Dots and hyphens normalize to
	// underscores for the canonical storage identity.
	LogicalNameInputPattern = `[A-Za-z0-9_.-]{1,249}`
)

var logicalNamePattern = regexp.MustCompile(`^` + LogicalNameInputPattern + `$`)
var envKeyInvalidChars = regexp.MustCompile(`[^A-Z0-9_]+`)

var sensitiveNames = map[string]struct{}{
	"accesskey": {}, "accesskeyid": {}, "accesstoken": {}, "accountkey": {}, "acktoken": {}, "apikey": {}, "apitoken": {},
	"auth": {}, "authentication": {}, "authorization": {}, "basicauth": {},
	"clientsecret": {}, "connectionstring": {}, "cookie": {}, "credential": {}, "credentials": {}, "csrf": {},
	"csrftoken": {}, "deliverytag": {}, "dsn": {}, "idsecret": {}, "idtoken": {},
	"jmssettlementtoken": {}, "locktoken": {}, "password": {}, "pass": {}, "passcode": {}, "passphrase": {}, "passwd": {},
	"privatekey": {}, "proxyauthorization": {}, "pwd": {}, "receipthandle": {},
	"refreshtoken": {}, "sas": {}, "saskey": {}, "sassignature": {}, "secret": {},
	"secretaccesskey": {}, "sharedaccesskey": {}, "sharedaccesssignature": {},
	"session": {}, "sessionid": {}, "sessiontoken": {}, "sid": {}, "storagestate": {},
	"settlementtoken": {}, "signature": {}, "token": {}, "xsrf": {}, "xsrftoken": {},
}

var alwaysSensitiveHeaders = map[string]struct{}{
	"authorization":        {},
	"proxy-authorization":  {},
	"x-api-key":            {},
	"api-key":              {},
	"x-auth-token":         {},
	"x-access-token":       {},
	"x-csrf-token":         {},
	"x-xsrf-token":         {},
	"x-session-id":         {},
	"x-amz-security-token": {},
}

var cookieHeaders = map[string]struct{}{
	"cookie":     {},
	"set-cookie": {},
}

var broadSensitiveHeaders = map[string]struct{}{
	"authorization":       {},
	"proxy-authorization": {},
	"cookie":              {},
	"set-cookie":          {},
	"x-api-key":           {},
	"api-key":             {},
}

var staticFields = map[string]struct{}{
	"password": {}, "passwd": {}, "pwd": {}, "pass": {}, "passcode": {},
	"secret": {}, "client_secret": {}, "clientsecret": {},
	"api_key": {}, "apikey": {}, "private_key": {},
	"privatekey": {}, "secret_key": {}, "secretkey": {},
	"access_key": {}, "accesskey": {},
	"passphrase": {}, "access_token": {}, "accesstoken": {},
	"refresh_token": {}, "refreshtoken": {},
}

// credentialPropertyWords end a name that describes a credential rather
// than holding one: session_feature_flags, token_type, session_duration.
//
// URL words are deliberately absent: a sas_url or session_url can carry the
// credential itself.
var credentialPropertyWords = map[string]struct{}{
	"count": {}, "duration": {}, "enabled": {}, "expiration": {}, "expires": {}, "expiry": {},
	"feature": {}, "features": {}, "flag": {}, "flags": {}, "length": {}, "method": {}, "mode": {},
	"timeout": {}, "ttl": {}, "type": {}, "version": {},
}

// SensitiveName reports whether name denotes protected credential material.
// It accepts common structured notations such as credentials[client_secret] and
// auth.private_key, including percent-encoded names, while deliberately using
// exact normalized components to avoid substring false positives. A credential
// component followed by a final property word (session_feature_flags,
// token_type) names a property of the credential, not the credential.
func SensitiveName(name string) bool {
	name = decodeName(name)
	if name == "" {
		return false
	}
	if _, ok := sensitiveNames[NormalizeName(name)]; ok {
		return true
	}
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r))
	})
	describesCredential := false
	if len(parts) > 1 {
		_, describesCredential = credentialPropertyWords[NormalizeName(parts[len(parts)-1])]
	}
	for start := range parts {
		var combined string
		for end := start; end < len(parts) && end < start+4; end++ {
			combined += NormalizeName(parts[end])
			if _, ok := sensitiveNames[combined]; ok && !(describesCredential && end < len(parts)-1) {
				return true
			}
		}
	}
	return false
}

// MinNamedValueBytes is the shortest value under a credential-like name in a
// URL-encoded pair (a query string or form body) treated as credential
// material, unless the name is password-like (a PIN can be short). A shorter
// value, such as a cookie=1 flag, cannot be told apart from ordinary text:
// matching it marks unrelated tokens such as a DNT header or an HTTP/1.1
// version.
const MinNamedValueBytes = 4

// MatchableNamedValue reports whether a value found under a credential-like
// name in a URL-encoded pair is credential material (see MinNamedValueBytes).
func MatchableNamedValue(name, value string) bool {
	return len(strings.TrimSpace(value)) >= MinNamedValueBytes || PasswordLikeName(name)
}

// PasswordLikeName reports names whose values are secrets however short:
// passwords, passcodes, PINs, one-time codes and secrets.
func PasswordLikeName(name string) bool {
	normalized := NormalizeName(name)
	for _, word := range []string{"password", "passwd", "passcode", "passphrase", "secret"} {
		if strings.Contains(normalized, word) {
			return true
		}
	}
	for _, word := range []string{"pwd", "pass", "pin", "otp"} {
		if strings.HasSuffix(normalized, word) {
			return true
		}
	}
	return false
}

// SensitiveHeader reports whether an HTTP header should be governed by
// import-time credential handling. It retains the established broad authoring
// heuristic, including names such as X-Auth and Subscription-Key.
func SensitiveHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if _, ok := broadSensitiveHeaders[lower]; ok {
		return true
	}
	if strings.Contains(lower, "authorization") ||
		strings.Contains(lower, "api-key") ||
		strings.Contains(lower, "apikey") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "credential") ||
		strings.Contains(lower, "csrf") ||
		lower == "key" ||
		strings.HasSuffix(lower, "-key") ||
		strings.HasSuffix(lower, "_key") {
		return true
	}
	for _, part := range strings.FieldsFunc(lower, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ' '
	}) {
		if part == "auth" || part == "key" {
			return true
		}
	}
	return false
}

// AlwaysSensitiveHeader reports whether a header is always protected by scrub
// policy, excluding cookies whose treatment is separately configurable.
func AlwaysSensitiveHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if _, ok := alwaysSensitiveHeaders[lower]; ok {
		return true
	}
	if CookieHeader(lower) {
		return false
	}
	return SensitiveName(name)
}

// CookieHeader reports whether name carries HTTP cookie state.
func CookieHeader(name string) bool {
	_, ok := cookieHeaders[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// StaticField reports whether name is a user-supplied credential field that a
// recorded request may redact without removing expected dynamic correlation
// candidates such as CSRF or session tokens.
func StaticField(name string) bool {
	name = decodeName(name)
	if staticFieldComponent(name) {
		return true
	}
	// Structured request names commonly wrap the real field name, for example
	// credentials[client_secret] or auth.private_key. Classify exact components
	// without applying the broader SensitiveName vocabulary, so dynamic
	// correlation fields such as credentials[session_id] remain available.
	if !strings.ContainsAny(name, "[].") {
		return false
	}
	for _, component := range strings.FieldsFunc(name, func(r rune) bool {
		return r == '[' || r == ']' || r == '.'
	}) {
		if staticFieldComponent(component) {
			return true
		}
	}
	return false
}

func staticFieldComponent(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "-", "_")
	_, ok := staticFields[name]
	return ok
}

// SensitiveXMLName extends the ordinary credential vocabulary for qualified
// XML element and attribute names commonly used by SOAP APIs.
func SensitiveXMLName(name string) bool {
	local := name
	if separator := strings.LastIndexByte(local, ':'); separator >= 0 {
		local = local[separator+1:]
	}
	if SensitiveName(local) {
		return true
	}
	normalized := NormalizeName(local)
	for _, suffix := range []string{
		"password", "passwd", "passphrase",
		"authtoken", "accesstoken", "refreshtoken", "sessiontoken",
		"apikey", "clientsecret", "privatekey", "accesskey",
	} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

// NormalizeName percent-decodes and removes separators from a field name,
// returning a lowercase comparison identity.
func NormalizeName(name string) string {
	name = decodeName(name)
	var builder strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(unicode.ToLower(r))
		}
	}
	return builder.String()
}

// EnvKey converts an authored name into the uppercase underscore identifier
// used by legacy {{env.KEY}} import projections.
func EnvKey(name string) string {
	key := envKeyInvalidChars.ReplaceAllString(strings.ToUpper(strings.TrimSpace(name)), "_")
	key = strings.Trim(key, "_")
	if key == "" {
		return "SECRET"
	}
	return key
}

// NormalizeLogicalName validates a script-facing logical secret name and
// returns its canonical uppercase/underscore storage identity.
func NormalizeLogicalName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if !logicalNamePattern.MatchString(name) {
		return "", errors.New("secret name must contain 1-249 letters, digits, underscores, dots, or hyphens")
	}
	name = strings.ToUpper(name)
	return strings.NewReplacer(".", "_", "-", "_").Replace(name), nil
}

// ValidLogicalName reports whether raw is accepted by the portable logical-name
// grammar.
func ValidLogicalName(raw string) bool {
	_, err := NormalizeLogicalName(raw)
	return err == nil
}

// Reference returns a canonical portable secret template.
func Reference(raw string) (string, error) {
	name, err := NormalizeLogicalName(raw)
	if err != nil {
		return "", err
	}
	return "{{secret." + name + "}}", nil
}

// ParseReference recognizes a whole-value {{secret.NAME}} template and returns
// its canonical logical name.
func ParseReference(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "{{") || !strings.HasSuffix(value, "}}") {
		return "", false
	}
	expression := strings.TrimSpace(value[2 : len(value)-2])
	if !strings.HasPrefix(expression, "secret.") {
		return "", false
	}
	name, err := NormalizeLogicalName(strings.TrimSpace(strings.TrimPrefix(expression, "secret.")))
	return name, err == nil
}

// DraftReference returns a disclosure-safe reference for an imported source
// value. Existing whole-value secret references retain their canonical logical
// name; literals receive suggestedName; empty values stay empty. An invalid
// suggestion fails closed to the disclosure sentinel.
func DraftReference(value, suggestedName string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	if name, ok := ParseReference(value); ok {
		if reference, err := Reference(name); err == nil {
			return reference
		}
	}
	reference, err := Reference(suggestedName)
	if err != nil {
		return Redacted
	}
	return reference
}

// SensitiveRuntimeSetting applies shared credential vocabulary to imported
// runtime configuration keys, including legacy proxy spellings where
// "username" is sensitive only within a proxy/web section.
func SensitiveRuntimeSetting(section, key string) bool {
	if SensitiveName(key) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(section)) {
	case "proxy", "web":
	default:
		return false
	}
	switch NormalizeName(key) {
	case "proxyusername", "proxyuser", "username",
		"proxypassword", "proxypass", "proxypwd":
		return true
	default:
		return false
	}
}

func decodeName(name string) string {
	name = strings.TrimSpace(name)
	for range 3 {
		decoded, err := url.QueryUnescape(name)
		if err != nil || decoded == name {
			break
		}
		name = decoded
	}
	return strings.TrimSpace(name)
}
