package engine

import (
	"errors"
	"log/slog"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Script-sourced run-evidence redaction.
//
// Nothing an import writes into a stored script is ever rewritten, masked, or
// replaced by a stub: the durable script stays byte-exact and runnable. This file
// governs only the OTHER half — whether the values that script carries are also
// scrubbed out of the run's OUTPUT (logs, snapshots, reports, archives, debugger
// evidence).
//
// The operator owns that choice through script.Config.Log.RedactRunEvidence,
// which defaults to leaving script-sourced values visible. A nil pointer means
// "not set", so read it as off.
//
// Two invariants hold regardless of the setting:
//
//   - Platform-managed values (anything a {{secret.NAME}} reference resolves to
//     through the project secret store, plus the composite spellings derived from
//     them) are ALWAYS registered. The setting never reaches them.
//   - The setting never fails a run. Script-sourced values are best effort: when
//     they would exceed a matcher bound, the script-sourced contribution is
//     dropped and a warning is surfaced, while every platform-managed value keeps
//     its existing fail-closed behaviour.

// scriptSourcedRedactionDroppedWarning is the operator-facing explanation used
// wherever a bound forces the best-effort contribution to be abandoned.
const scriptSourcedRedactionDroppedWarning = "script-sourced run-evidence redaction was dropped: " +
	"the credential values retained by this script exceed the runtime matcher limit; " +
	"platform-managed secrets remain redacted"

// ScriptRedactsRunEvidence exposes the explicit operator setting
// script.Config.Log.RedactRunEvidence (default false when nil) for non-secret
// run-evidence policy consumers such as business-key digest-only archiving.
// Literal and script-sourced credentials are redacted unconditionally via
// scriptRedactsCredentials regardless of this flag.
func ScriptRedactsRunEvidence(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	return logConfigRedactsRunEvidence(script.Config.Log)
}

// scriptRedactsCredentials reports whether script-sourced credential values
// participate in run-output redaction. Every non-nil script participates.
func scriptRedactsCredentials(script *dsl.Script) bool {
	return script != nil
}

func logConfigRedactsRunEvidence(logCfg dsl.LogConfig) bool {
	return logCfg.RedactRunEvidence != nil && *logCfg.RedactRunEvidence
}

// RuntimeProtocolAuthCompositeValues exposes derived protocol auth composite
// spellings (such as Basic auth, SASL PLAIN/LOGIN, DSN passwords, and browser
// storage-state secrets) so distributed staging can attach them to preflight
// redaction before building VUsers.
func RuntimeProtocolAuthCompositeValues(script *dsl.Script, resolver SecretResolver) []string {
	return runtimeProtocolAuthCompositeValues(script, resolver)
}

// runtimeSecretValuesForScript is the one static, per-run credential chokepoint.
// engine.New, NewMultiGroup, ReplayScript, and RunScriptShakeoutWithSecretResolver
// were four byte-identical copies of this sequence; they now all call it.
//
// platformManaged carries whatever the caller already snapshotted from the secret
// resolver. The returned platform-managed slice adds the composite spellings that
// only exist once protocol config is built. The returned script-sourced slice
// includes all literal credentials extracted from the script.
//
// The third return is the value-free result of the same locator walk, and every
// caller MUST pass it to each VUser it builds (VUser.importedCredentialInspection).
// The walk is the only source of the authored template expressions that the
// run-time producers resolve, and a VUser that is not handed it would repeat the
// whole walk per VUser.
func runtimeSecretValuesForScript(
	script *dsl.Script,
	resolver SecretResolver,
	platformManaged []string,
) ([]string, []string, *importedCredentialInspection, error) {
	platformManaged = append(platformManaged, runtimeProtocolAuthCompositeValues(script, resolver)...)
	scriptSourced, plan, err := inspectImportedCredentials(script)
	if err != nil {
		return nil, nil, nil, err
	}
	inspection := &importedCredentialInspection{plan: plan}
	if !scriptRedactsCredentials(script) {
		return platformManaged, nil, inspection, nil
	}
	return platformManaged, scriptSourced, inspection, nil
}

// buildRuntimeSecretRedaction installs the platform-managed matcher first, then
// layers the best-effort script-sourced values onto it.
//
// Ordering is load-bearing twice over: a literal registered by both origins stays
// platform-managed, and a script-sourced overflow can be abandoned without
// disturbing the platform-managed matcher that was already built. The bool
// reports that the script-sourced contribution was dropped.
func buildRuntimeSecretRedaction(
	base *RedactionConfig,
	platformManaged []string,
	scriptSourced []string,
) (*RedactionConfig, bool, error) {
	config, err := WithSecretValueRedactionChecked(base, platformManaged)
	if err != nil {
		return nil, false, err
	}
	if len(scriptSourced) == 0 {
		return config, false, nil
	}
	extended, err := WithScriptSourcedSecretValueRedaction(config, scriptSourced)
	if err != nil {
		return config, true, nil //nolint:nilerr // script-sourced values that do not fit are dropped and reported by the bool
	}
	return extended, false, nil
}

// buildBoundedRuntimeSecretRedaction is the multi-group form. The aggregate
// matcher budget shared by every group still fails NewMultiGroup closed for
// platform-managed values; script-sourced values are dropped instead, because a
// default-on setting must never turn a run that passes today into a failure.
func buildBoundedRuntimeSecretRedaction(
	platformManaged []string,
	scriptSourced []string,
	remainingBytes int,
) (*RedactionConfig, bool, error) {
	config, err := WithSecretValueRedactionChecked(nil, platformManaged)
	if err != nil {
		return nil, false, err
	}
	if config != nil && config.runtimePatternBytes > remainingBytes {
		return nil, false, errors.New("multi-group runtime secret redaction exceeds the aggregate matcher limit")
	}
	if len(scriptSourced) == 0 {
		return config, false, nil
	}
	extended, err := WithScriptSourcedSecretValueRedaction(config, scriptSourced)
	if err != nil || (extended != nil && extended.runtimePatternBytes > remainingBytes) {
		return config, true, nil //nolint:nilerr // script-sourced values that do not fit are dropped and reported by the bool
	}
	return extended, false, nil
}

// warnScriptSourcedRedactionDropped surfaces the drop on the run. The values
// themselves are never logged; only the fact and the scope are.
func warnScriptSourcedRedactionDropped(attrs ...any) {
	slog.Warn(scriptSourcedRedactionDroppedWarning, attrs...)
}
