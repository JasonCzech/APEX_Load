package importreview

import (
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func encryptedProxyCalloutFinding(gate GateScope, severity diag.Severity) Finding {
	return Finding{Code: dsl.EncryptedProxyPasswordFindingCode, ReasonCode: dsl.EncryptedProxyPasswordFindingCode, Severity: severity, Gate: gate, Detail: EncryptedProxyPasswordDetail, Source: "enterprise_script_archive", SourceRef: "default.cfg#ProxyPasswordIsEncrypted", Resolution: "replace_proxy_secret"}
}

// An advisory callout is stamped under dsl.AdvisoryCalloutsMetadataKey, never
// as execution_blocker(s), whatever gate the finding names: a generator built
// before the callout became advisory blocks every execution_blocker code.
func TestApplyToActionStampsAdvisoryCalloutsUnderTheirOwnKey(t *testing.T) {
	for _, finding := range []Finding{
		encryptedProxyCalloutFinding(GateNone, diag.Warning),
		encryptedProxyCalloutFinding(GateExecution, diag.Blocker),
	} {
		action := dsl.Action{Name: "Restore proxy credential", Type: "source_review"}
		ApplyToAction(&action, []Finding{finding})
		if _, ok := action.Metadata["execution_blocker"]; ok {
			t.Fatalf("gate %q: callout stamped as execution_blocker: %#v", finding.Gate, action.Metadata)
		}
		if _, ok := action.Metadata["execution_blockers"]; ok {
			t.Fatalf("gate %q: callout stamped as execution_blockers: %#v", finding.Gate, action.Metadata)
		}
		if got := action.Metadata[dsl.AdvisoryCalloutsMetadataKey]; !reflect.DeepEqual(got, []string{dsl.EncryptedProxyPasswordFindingCode}) {
			t.Fatalf("gate %q: %s = %#v", finding.Gate, dsl.AdvisoryCalloutsMetadataKey, got)
		}
		review, _ := action.Metadata["import_review"].(map[string]any)
		if review["resolution"] != "replace_proxy_secret" || review["source"] != "enterprise_script_archive" {
			t.Fatalf("gate %q: import_review = %#v, want the callout's provenance", finding.Gate, review)
		}
		script := &dsl.Script{Init: []dsl.Action{action}}
		findings, err := FindingsFromScript(script)
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) != 1 || findings[0].Code != dsl.EncryptedProxyPasswordFindingCode || findings[0].Gate != GateNone ||
			findings[0].Severity != diag.Warning || findings[0].Resolution != "replace_proxy_secret" ||
			findings[0].SourceRef != "default.cfg#ProxyPasswordIsEncrypted" || findings[0].ActionPath != "init[0]" {
			t.Fatalf("gate %q: findings = %#v, want one non-blocking callout with its repair", finding.Gate, findings)
		}
		if readiness := Evaluate(EvaluationInput{Findings: findings}); !readiness.ReplayReady {
			t.Fatalf("gate %q: readiness = %#v, want the callout not to block", finding.Gate, readiness)
		}
		RemoveActionCodes(&script.Init[0], func(code string) bool { return code == dsl.EncryptedProxyPasswordFindingCode })
		if _, ok := script.Init[0].Metadata[dsl.AdvisoryCalloutsMetadataKey]; ok {
			t.Fatalf("gate %q: repair left the callout: %#v", finding.Gate, script.Init[0].Metadata)
		}
		if _, ok := script.Init[0].Metadata["import_review"]; ok {
			t.Fatalf("gate %q: repair left the callout's import_review: %#v", finding.Gate, script.Init[0].Metadata)
		}
		if findings, _ := FindingsFromScript(script); len(findings) != 0 {
			t.Fatalf("gate %q: findings after repair = %#v", finding.Gate, findings)
		}
	}
}

// Re-applying findings to a step stored with the legacy execution_blocker
// spelling moves the callout to its own key and keeps every real blocker.
func TestApplyToActionMigratesLegacyAdvisoryBlockers(t *testing.T) {
	action := dsl.Action{Name: "proxy setting", Type: "source_review", Metadata: map[string]any{
		"execution_blocker":  dsl.EncryptedProxyPasswordFindingCode,
		"execution_blockers": []any{dsl.EncryptedProxyPasswordFindingCode},
	}}
	ApplyToAction(&action, []Finding{{Code: "legacy_tls_replacement", Severity: diag.Blocker, Gate: GateExecution}})
	if action.Metadata["execution_blocker"] != "legacy_tls_replacement" || !reflect.DeepEqual(action.Metadata["execution_blockers"], []string{"legacy_tls_replacement"}) {
		t.Fatalf("execution blockers = %#v / %#v, want only the real blocker", action.Metadata["execution_blocker"], action.Metadata["execution_blockers"])
	}
	if got := action.Metadata[dsl.AdvisoryCalloutsMetadataKey]; !reflect.DeepEqual(got, []string{dsl.EncryptedProxyPasswordFindingCode}) {
		t.Fatalf("%s = %#v", dsl.AdvisoryCalloutsMetadataKey, got)
	}
}
