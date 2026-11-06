package credential

import "strings"

// Script environment names. A script reads an environment variable through
// {{env.KEY}} only when the operator lists KEY in APEXLOAD_SCRIPT_ENV_ALLOWLIST,
// and never when KEY falls under the names below, whatever the allowlist says:
// the engine's own secret store and cloud credentials live there, and a name
// that says it holds a password or token is one the operator should bind as a
// project secret instead (pkg/engine script_env.go applies this policy).

var scriptEnvBlockedPrefixes = []string{
	"APEXLOAD_SECRET_", "AWS_", "AZURE_", "GOOGLE_", "GCP_", "OCI_", "KUBERNETES_",
}

var scriptEnvBlockedFragments = []string{
	"TOKEN", "PASSWORD", "PASSWD", "SECRET", "API_KEY", "PRIVATE_KEY", "CREDENTIAL",
}

// ScriptEnvNameAlwaysBlocked reports an environment variable name no allowlist
// can make readable by a script.
func ScriptEnvNameAlwaysBlocked(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	for _, prefix := range scriptEnvBlockedPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	for _, fragment := range scriptEnvBlockedFragments {
		if strings.Contains(upper, fragment) {
			return true
		}
	}
	return false
}

// scriptEnvNameRewrites shorten each blocked word; the longer words come first
// so PRIVATE_KEY is not first read as part of something else.
var scriptEnvNameRewrites = []struct{ from, to string }{
	{"PASSWORD", "PW"}, {"PASSWD", "PW"}, {"PRIVATE_KEY", "PRIVKEY"}, {"API_KEY", "APIKEY"},
	{"SECRET", "SCRT"}, {"TOKEN", "TKN"}, {"CREDENTIAL", "CRED"},
}

// ReadableScriptEnvName rewrites an uppercase environment variable name that
// scripts could never read into one an operator can allow: PASSWORD becomes PW,
// TOKEN becomes TKN, and a cloud or secret-store prefix gains REC_. A name that
// is already readable is returned as it is.
func ReadableScriptEnvName(key string) string {
	if key == "" || !ScriptEnvNameAlwaysBlocked(key) {
		return key
	}
	// Collapse separators first: API__KEY would otherwise become API_KEY
	// once the rewrite below joins its underscores.
	for strings.Contains(key, "__") {
		key = strings.ReplaceAll(key, "__", "_")
	}
	// Each shortened word becomes a component of its own (ACCESSTOKEN
	// becomes ACCESS_TKN, PASSWORD2 becomes PW_2), so ReadableCredentialEnvName
	// still knows the name for a credential's.
	for _, rewrite := range scriptEnvNameRewrites {
		key = strings.ReplaceAll(key, rewrite.from, "_"+rewrite.to+"_")
	}
	for strings.Contains(key, "__") {
		key = strings.ReplaceAll(key, "__", "_")
	}
	key = strings.Trim(key, "_")
	for _, prefix := range scriptEnvBlockedPrefixes {
		if strings.HasPrefix(key, prefix) {
			key = "REC_" + key
			break
		}
	}
	return key
}

// ReadableCredentialEnvName reports an environment variable name that
// ReadableScriptEnvName shortened from a credential name: one whose
// underscore-separated components include TKN, SCRT, PRIVKEY or CRED, or PW.
// Callers that protect env values by name (runtime overrides, masks, debug
// evidence) treat it as a credential name, through SensitiveEnvName. It is
// deliberately not part of SensitiveName, which every import scrubber uses on
// arbitrary field names. It errs toward protecting: an ordinary env name with
// a CRED or PW component (CRED_SCORE) is treated as a credential too.
func ReadableCredentialEnvName(name string) bool {
	parts := strings.FieldsFunc(strings.ToUpper(strings.TrimSpace(name)), func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == ' '
	})
	// As in SensitiveName, a credential word followed by a final property
	// word (PW_MIN_LENGTH, CRED_TYPE) names a property, not the credential.
	describesCredential := false
	if len(parts) > 1 {
		_, describesCredential = credentialPropertyWords[NormalizeName(parts[len(parts)-1])]
	}
	for index, part := range parts {
		switch part {
		case "TKN", "SCRT", "PRIVKEY", "CRED", "PW":
			if !(describesCredential && index < len(parts)-1) {
				return true
			}
		}
	}
	return false
}

// SensitiveEnvName reports an environment variable name whose value is
// credential material: a credential name, or a readable one an import wrote
// for one.
func SensitiveEnvName(name string) bool {
	return SensitiveName(name) || ReadableCredentialEnvName(name)
}

// ScriptEnvKey converts an authored field or header name into the {{env.KEY}}
// name an import suggests for it: EnvKey's form, made readable by scripts.
func ScriptEnvKey(name string) string {
	if envKeyInvalidChars.ReplaceAllString(strings.ToUpper(strings.TrimSpace(name)), "") == "" {
		// Nothing to name it after; EnvKey's own fallback, SECRET, is blocked.
		return "VALUE"
	}
	return ReadableScriptEnvName(EnvKey(name))
}
