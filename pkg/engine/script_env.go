package engine

import (
	"os"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
)

// EnvScriptEnvAllowlist is the operator-owned, comma-separated allowlist of
// environment variable names that uploaded scripts may read — via the
// {{env.KEY}} template and the CEL condition `env` binding. When set, ONLY the
// listed keys are script-visible; everything else resolves as missing. An
// unset allowlist is deliberately default-deny.
const EnvScriptEnvAllowlist = "APEXLOAD_SCRIPT_ENV_ALLOWLIST"

// scriptEnvAllowlist parses EnvScriptEnvAllowlist into a set. An empty set
// means no process environment variables are visible to scripts.
func scriptEnvAllowlist() map[string]struct{} {
	raw := strings.TrimSpace(os.Getenv(EnvScriptEnvAllowlist))
	if raw == "" {
		return map[string]struct{}{}
	}
	allow := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		if name := strings.TrimSpace(part); name != "" {
			allow[name] = struct{}{}
		}
	}
	return allow
}

// scriptEnvKeyAllowed reports whether a script may read the given env key.
func scriptEnvKeyAllowed(key string, allow map[string]struct{}) bool {
	if ScriptEnvKeyAlwaysBlocked(key) {
		return false
	}
	_, ok := allow[key]
	return ok
}

// ScriptEnvKeyAlwaysBlocked reports an environment variable name no allowlist
// can make script-visible: one in a cloud or secret-store namespace, or one
// naming a password, token, secret, key or credential
// (credential.ScriptEnvNameAlwaysBlocked).
func ScriptEnvKeyAlwaysBlocked(key string) bool {
	return credential.ScriptEnvNameAlwaysBlocked(key)
}

// ScriptEnvKeyStatus says whether a script can read an environment variable
// through {{env.KEY}} in this process, and if not, why. It never reveals the
// value.
type ScriptEnvKeyStatus string

const (
	ScriptEnvKeyReadable ScriptEnvKeyStatus = "readable"
	// ScriptEnvKeyBlockedName: the name is always blocked; rename it.
	ScriptEnvKeyBlockedName ScriptEnvKeyStatus = "blocked_name"
	// ScriptEnvKeyNotAllowlisted: the name is not in the operator's
	// APEXLOAD_SCRIPT_ENV_ALLOWLIST.
	ScriptEnvKeyNotAllowlisted ScriptEnvKeyStatus = "not_allowlisted"
	// ScriptEnvKeyUnset: allowed, but this process's environment has no
	// such variable.
	ScriptEnvKeyUnset ScriptEnvKeyStatus = "not_set"
)

// ScriptEnvKeyStatusOf applies the same policy as {{env.KEY}} resolution.
func ScriptEnvKeyStatusOf(key string) ScriptEnvKeyStatus {
	if ScriptEnvKeyAlwaysBlocked(key) {
		return ScriptEnvKeyBlockedName
	}
	if _, ok := scriptEnvAllowlist()[key]; !ok {
		return ScriptEnvKeyNotAllowlisted
	}
	if _, ok := os.LookupEnv(key); !ok {
		return ScriptEnvKeyUnset
	}
	return ScriptEnvKeyReadable
}

// lookupScriptEnv resolves an environment value for script consumption,
// honoring the allowlist and the always-blocked secret namespace. It mirrors
// os.LookupEnv's (value, ok) contract; a blocked key reports ok=false so the
// template/condition treats it as unset.
func lookupScriptEnv(key string) (string, bool) {
	if !scriptEnvKeyAllowed(key, scriptEnvAllowlist()) {
		return "", false
	}
	return os.LookupEnv(key)
}

// ScriptEnvAvailable reports whether an uploaded script may resolve key through
// the {{env.*}} namespace. It intentionally exposes only availability so API
// preflight checks can mirror runtime policy without reading the value.
func ScriptEnvAvailable(key string) bool {
	_, ok := lookupScriptEnv(key)
	return ok
}

// scriptVisibleEnvMap returns the process environment filtered to what scripts
// are permitted to read, for binding into the CEL condition `env` variable.
func scriptVisibleEnvMap() map[string]any {
	allow := scriptEnvAllowlist()
	out := map[string]any{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if !scriptEnvKeyAllowed(key, allow) {
			continue
		}
		out[key] = value
	}
	return out
}
