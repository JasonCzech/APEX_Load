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

// ScriptRedactsRunEvidence exposes the operator setting to the one caller
// outside this package — the generator RPC staging boundary — so the
// default-false reading is written down exactly once.
func ScriptRedactsRunEvidence(script *dsl.Script) bool {
	return scriptRedactsRunEvidence(script)
}

// scriptRedactsRunEvidence reports whether script-sourced credential values
// participate in run-output redaction. Absent configuration does not redact.
func scriptRedactsRunEvidence(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	return logConfigRedactsRunEvidence(script.Config.Log)
}

func logConfigRedactsRunEvidence(logCfg dsl.LogConfig) bool {
	return logCfg.RedactRunEvidence != nil && *logCfg.RedactRunEvidence
}

// runtimeSecretValuesForScript is the one static, per-run credential chokepoint.
// engine.New, NewMultiGroup, ReplayScript, and RunScriptShakeoutWithSecretResolver
// were four byte-identical copies of this sequence; they now all call it.
//
// platformManaged carries whatever the caller already snapshotted from the secret
// resolver. The returned platform-managed slice adds the composite spellings that
// only exist once protocol config is built. The returned script-sourced slice is
// empty unless the setting is on.
//
// The third return is the value-free result of the same locator walk, and every
// caller MUST pass it to each VUser it builds (VUser.importedCredentialInspection).
// The walk is the only source of the authored template expressions that the
// run-time producers resolve, and a VUser that is not handed it would repeat the
// whole walk per VUser. It is returned regardless of the operator setting: it
// holds no values, and the producers that read it apply the setting themselves.
//
// The walk runs unconditionally: a locator that no longer matches its executable
// field means the import is corrupted, which must fail closed whether or not
// anything is redacted.
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
	if !scriptRedactsRunEvidence(script) {
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
		return config, true, nil
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
		return config, true, nil
	}
	return extended, false, nil
}

// warnScriptSourcedRedactionDropped surfaces the drop on the run. The values
// themselves are never logged; only the fact and the scope are.
func warnScriptSourcedRedactionDropped(attrs ...any) {
	slog.Warn(scriptSourcedRedactionDroppedWarning, attrs...)
}
