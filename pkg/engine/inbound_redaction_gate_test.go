package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// inboundRedactionFixture is a caller-supplied config (the shape apexctl and
// generator RPC staging hand to engine.New / NewMultiGroup) carrying one
// platform-managed value and one script-sourced literal.
func inboundRedactionFixture(t *testing.T, platformValue, scriptValue string) *RedactionConfig {
	t.Helper()
	base, err := NewRedactionConfig(nil)
	if err != nil {
		t.Fatalf("NewRedactionConfig: %v", err)
	}
	if platformValue != "" {
		base, err = WithSecretValueRedactionChecked(base, []string{platformValue})
		if err != nil {
			t.Fatalf("WithSecretValueRedactionChecked: %v", err)
		}
	}
	withScript, err := WithScriptSourcedSecretValueRedaction(base, []string{scriptValue})
	if err != nil {
		t.Fatalf("WithScriptSourcedSecretValueRedaction: %v", err)
	}
	return withScript
}

func inboundGateScript(redact bool) *dsl.Script {
	return setRunEvidenceRedaction(&dsl.Script{
		Name:     "inbound-redaction-gate",
		Protocol: "stub",
		Actions:  []dsl.Action{{Name: "a", Type: "request", Target: "/"}},
	}, redact)
}

func assertInboundGate(t *testing.T, label string, cfg *RedactionConfig, platformValue, scriptValue string, redact bool) {
	t.Helper()
	scrubbed := scrubTextOrIdentity(cfg, "p="+platformValue+" s="+scriptValue)
	if platformValue != "" && strings.Contains(scrubbed, platformValue) {
		t.Fatalf("%s: platform-managed value leaked with redact_run_evidence=%t: %q", label, redact, scrubbed)
	}
	if leaked := strings.Contains(scrubbed, scriptValue); leaked == redact {
		t.Fatalf("%s: inbound script-sourced value leaked=%t with redact_run_evidence=%t: %q", label, leaked, redact, scrubbed)
	}
}

// An inbound config cannot re-enable literal redaction the script turned off,
// and it never loses a platform-managed value.
func TestEngineNewGatesInboundScriptSourcedRedaction(t *testing.T) {
	const (
		platformValue = "inbound-platform-secret-4401"
		scriptValue   = "inbound-script-literal-4402"
	)
	for _, mode := range runEvidenceRedactionModes {
		for _, withPlatform := range []bool{true, false} {
			platform := ""
			if withPlatform {
				platform = platformValue
			}
			name := mode.name + "_script_only"
			if withPlatform {
				name = mode.name + "_with_platform"
			}
			t.Run(name, func(t *testing.T) {
				e, err := New(Config{
					Script:          inboundGateScript(mode.redact),
					VUsers:          1,
					Duration:        time.Second,
					RedactionConfig: inboundRedactionFixture(t, platform, scriptValue),
				})
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				assertInboundGate(t, "engine.New", e.redaction, platform, scriptValue, mode.redact)
			})
		}
	}
}

func TestNewMultiGroupGatesInboundScriptSourcedRedaction(t *testing.T) {
	const (
		platformValue = "inbound-group-platform-4403"
		scriptValue   = "inbound-group-literal-4404"
	)
	for _, mode := range runEvidenceRedactionModes {
		for _, withPlatform := range []bool{true, false} {
			platform := ""
			if withPlatform {
				platform = platformValue
			}
			name := mode.name + "_script_only"
			if withPlatform {
				name = mode.name + "_with_platform"
			}
			t.Run(name, func(t *testing.T) {
				mge, err := NewMultiGroup(MultiGroupConfig{
					Duration: time.Second,
					Groups: []GroupConfig{{
						Name:            "g",
						Script:          inboundGateScript(mode.redact),
						VUsers:          1,
						RedactionConfig: inboundRedactionFixture(t, platform, scriptValue),
					}},
				})
				if err != nil {
					t.Fatalf("NewMultiGroup: %v", err)
				}
				assertInboundGate(t, "NewMultiGroup", mge.GroupRedactionConfig(0), platform, scriptValue, mode.redact)
			})
		}
	}
}
