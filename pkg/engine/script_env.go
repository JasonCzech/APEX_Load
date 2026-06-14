package engine

import (
	"os"
	"strings"
)

// EnvScriptEnvAllowlist is the operator-owned, comma-separated allowlist of
// environment variable names that uploaded scripts may read — via the
// {{env.KEY}} template and the CEL condition `env` binding. When set, ONLY the
// listed keys are script-visible; everything else resolves as missing. When
// unset, scripts retain their historical access to process environment values
// (minus the always-blocked engine secret namespace below).
const EnvScriptEnvAllowlist = "APEXLOAD_SCRIPT_ENV_ALLOWLIST"

// scriptEnvSecretPrefix is the engine's own secret store namespace
// (see secrets.go: APEXLOAD_SECRET_<name>). These must never be reachable
// through script-visible env, regardless of the allowlist, so a script cannot
// read resolved credentials by name through {{env.KEY}} or CEL `env`.
const scriptEnvSecretPrefix = "APEXLOAD_SECRET_"

// scriptEnvAllowlist parses EnvScriptEnvAllowlist into a set, or returns nil
// when unset/empty (meaning "no allowlist configured").
func scriptEnvAllowlist() map[string]struct{} {
	raw := strings.TrimSpace(os.Getenv(EnvScriptEnvAllowlist))
	if raw == "" {
		return nil
	}
	allow := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		if name := strings.TrimSpace(part); name != "" {
			allow[name] = struct{}{}
		}
	}
	if len(allow) == 0 {
		return nil
	}
	return allow
}

// scriptEnvKeyAllowed reports whether a script may read the given env key.
func scriptEnvKeyAllowed(key string, allow map[string]struct{}) bool {
	if strings.HasPrefix(key, scriptEnvSecretPrefix) {
		return false
	}
	if allow == nil {
		return true
	}
	_, ok := allow[key]
	return ok
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
