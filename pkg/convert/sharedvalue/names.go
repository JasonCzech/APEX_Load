package sharedvalue

import (
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// parameterNamePattern is what the engine's {{param.X}} / {{var.X}} template
// grammar can address. Anything outside it would produce a parameter no
// template could reference.
var parameterNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// NormalizeName maps a source variable spelling onto a DSL parameter name. It
// mirrors the rewriting the Postman and RAML converters already apply so a
// collection variable detected here lands on the same name those converters
// would have produced.
func NormalizeName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "{}")
	value = strings.TrimSpace(value)
	replacer := strings.NewReplacer("-", "_", ".", "_", " ", "_", "/", "_", ":", "_")
	value = replacer.Replace(value)
	// Leading digits cannot start an identifier; prefix rather than drop so two
	// distinct source names never collapse onto one parameter.
	if value != "" && value[0] >= '0' && value[0] <= '9' {
		value = "v_" + value
	}
	return value
}

// ValidName reports whether a name is addressable as {{param.NAME}}.
func ValidName(name string) bool {
	return parameterNamePattern.MatchString(name)
}

// CredentialShaped reports whether a shared-value candidate name matches a
// credential-like field or parameter identifier. Literal candidate values are
// preserved during discovery and Apply; callers may use CredentialShaped as an
// advisory classification signal (for example when offering optional secret
// bindings).
func CredentialShaped(name string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(name))
	if normalized == "tokenurl" || normalized == "persistsessionacrossiterations" ||
		normalized == "credentialpolicy" || credential.NonCredentialName(name) {
		return false
	}
	if credential.SensitiveName(name) {
		return true
	}
	switch normalized {
	case "authorization", "proxyauthorization", "password", "passwd", "passphrase",
		"pwd", "pass",
		"token", "accesstoken", "refreshtoken", "idtoken", "authtoken",
		"apikey", "accesskey", "accesskeyid", "secretkey", "secretaccesskey",
		"awsaccesskeyid", "awssecretaccesskey", "clientsecret", "privatekey",
		"credential", "credentials", "connectionstring", "dsn", "saslpassword",
		"cookie", "setcookie", "session", "sessionid", "csrftoken", "xsrftoken":
		return true
	}
	// The name endings the parameter projection masks (IsSensitiveScriptKey),
	// such as jsessionid, userSessionId, myApiToken, or sessionCookie, which
	// SensitiveName does not split on camelCase while avoiding non-credential
	// prefixes like tokenType, maxTokens, tokenExpiry, or cookieDomain.
	for _, suffix := range []string{
		"authorization", "password", "passwd", "passphrase", "token", "secret",
		"apikey", "credential", "cookie", "sessionid", "privatekey", "accesskey",
		"accesskeyid", "secretaccesskey",
	} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

// parameterIndex finds a script parameter by name, matching the
// case-insensitive uniqueness rule pkg/dsl/validate.go enforces. Returning the
// index rather than a copy lets callers update in place.
func parameterIndex(script *dsl.Script, name string) int {
	if script == nil {
		return -1
	}
	target := strings.ToLower(strings.TrimSpace(name))
	for i := range script.Parameters {
		if strings.ToLower(strings.TrimSpace(script.Parameters[i].Name)) == target {
			return i
		}
	}
	return -1
}
