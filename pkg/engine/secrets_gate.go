package engine

// secrets_gate.go — fail-closed handling of unresolved {{secret.*}} references.
//
// Template interpolation leaves an unresolved {{secret.x}} as its literal token
// (the secret backend had no value for it). Without a gate, the action would
// execute with that placeholder as its value — e.g. sending the bytes
// "{{secret.api_key}}" as an Authorization header. checkResolvedSecrets fails
// such an action closed instead, so a mis-provisioned run errors loudly rather
// than silently exercising the target with bogus credentials.

import (
	"fmt"
	"os"
	"strings"
)

// allowUnresolvedSecretsEnv opts out of the fail-closed gate for the rare case
// where running with a literal placeholder is intentional (e.g. a smoke test
// with no secret backend wired).
const allowUnresolvedSecretsEnv = "APEXLOAD_ALLOW_UNRESOLVED_SECRETS"

func allowUnresolvedSecrets() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(allowUnresolvedSecretsEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// checkResolvedSecrets returns an error when the most recent resolveAction left
// one or more {{secret.*}} references unresolved, unless the operator opted out.
// It is consulted at the start of executeWithRetry, immediately after the action
// was resolved, so the placeholder is never sent to the protocol.
func (vu *VUser) checkResolvedSecrets() error {
	if len(vu.unresolvedSecretRefs) == 0 || allowUnresolvedSecrets() {
		return nil
	}
	refs := strings.Join(dedupeSecretRefs(vu.unresolvedSecretRefs), ", ")
	return fmt.Errorf("unresolved secret reference(s) %s: refusing to run with placeholder values (set %s=true to override)", refs, allowUnresolvedSecretsEnv)
}

func dedupeSecretRefs(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
