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

// CredentialShaped reports whether binding this name to a plain list parameter
// would be destructive.
//
// pkg/api's disclosure-safe projection masks any map that carries a "type"
// field alongside a credential-ish "name" (scriptMapHasCredentialValue). A
// dsl.Parameter map has exactly that shape, so a parameter named "password"
// projects its values as [REDACTED] on every read, and the real value is lost
// on the next read-modify-write save. Such values must use {{secret.NAME}} and
// the guided credential-binding flow instead.
func CredentialShaped(name string) bool {
	if credential.SensitiveName(name) {
		return true
	}
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(name))
	for _, marker := range []string{
		"password", "passwd", "passphrase", "secret", "token",
		"credential", "authorization", "cookie", "apikey",
	} {
		if strings.Contains(normalized, marker) {
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
