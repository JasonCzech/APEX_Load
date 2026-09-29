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

// allowUnresolvedSecretsEnv opts out of the action-level gate for dynamically
// introduced references and direct VUser harnesses. Statically referenced
// script secrets are required by execution preflight and cannot be bypassed by
// this compatibility setting.
const allowUnresolvedSecretsEnv = "APEXLOAD_ALLOW_UNRESOLVED_SECRETS"
const allowUnresolvedFilesEnv = "APEXLOAD_ALLOW_UNRESOLVED_FILES"

func allowUnresolvedSecrets() bool {
	return allowUnresolvedTemplateRefs(allowUnresolvedSecretsEnv)
}

func allowUnresolvedFiles() bool {
	return allowUnresolvedTemplateRefs(allowUnresolvedFilesEnv)
}

func allowUnresolvedTemplateRefs(envName string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envName))) {
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
	if len(vu.unresolvedRunRefs) > 0 {
		return fmt.Errorf("unresolved run identity: refusing to run with a {{run.id}} placeholder")
	}
	if len(vu.unresolvedSecretRefs) == 0 || allowUnresolvedSecrets() {
		return vu.checkResolvedFiles()
	}
	refs := strings.Join(dedupeTemplateRefs(vu.unresolvedSecretRefs), ", ")
	return fmt.Errorf("unresolved secret reference(s) %s: refusing to run with placeholder values (set %s=true to override)", refs, allowUnresolvedSecretsEnv)
}

func (vu *VUser) checkResolvedFiles() error {
	if len(vu.unresolvedFileRefs) == 0 || allowUnresolvedFiles() {
		return nil
	}
	refs := strings.Join(dedupeTemplateRefs(vu.unresolvedFileRefs), ", ")
	return fmt.Errorf("unresolved file asset reference(s) %s: refusing to run with placeholder values (upload or attach the referenced file asset, or set %s=true to override)", refs, allowUnresolvedFilesEnv)
}

// Unresolved {{param.*}} references are gated by actionContainsUnresolvedParameter
// in safeProtoExecuteStream, which inspects every resolved field (body, headers,
// params, metadata, wait_for, sync, auth, soap, extractors, assertions) and names
// the offending action. Adding a second, narrower gate here only changed the
// diagnostic text and split the behavior across two code paths.

func dedupeTemplateRefs(in []string) []string {
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
