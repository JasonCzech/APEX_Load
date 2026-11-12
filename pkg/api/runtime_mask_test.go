package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestSOAPSecretLifecycle_ProtectMaskReconcile(t *testing.T) {
	script := &dsl.Script{Name: "soap-test", Protocol: "soap"}

	// 1. protectRuntimeOverrideSecretsWithOptions: reject redaction sentinel when deferRedacted is false.
	sentinelOverride := &RuntimeSettingsOverride{
		SOAP: &dsl.SOAPConfig{
			WSSecurity: &dsl.SOAPWSSecurityConfig{
				UsernameToken: &dsl.SOAPUsernameTokenConfig{
					Username: "alice",
					Password: scrub.Redacted,
				},
			},
		},
	}
	if _, _, _, err := protectRuntimeOverrideSecretsWithOptions("test-scope", 0, script, sentinelOverride, true, false, false); err == nil {
		t.Fatal("expected error for redaction sentinel in SOAP password, got nil")
	}

	// Reject mixed literal + {{secret.NAME}} reference.
	mixedOverride := &RuntimeSettingsOverride{
		SOAP: &dsl.SOAPConfig{
			WSSecurity: &dsl.SOAPWSSecurityConfig{
				UsernameToken: &dsl.SOAPUsernameTokenConfig{
					Username: "alice",
					Password: "prefix-{{secret.SOAP_PASS}}",
				},
			},
		},
	}
	if _, _, _, err := protectRuntimeOverrideSecretsWithOptions("test-scope", 0, script, mixedOverride, true, false, false); err == nil {
		t.Fatal("expected error for mixed secret reference in SOAP password, got nil")
	}

	// Preserve exact {{secret.NAME}} reference.
	refOverride := &RuntimeSettingsOverride{
		SOAP: &dsl.SOAPConfig{
			WSSecurity: &dsl.SOAPWSSecurityConfig{
				UsernameToken: &dsl.SOAPUsernameTokenConfig{
					Username: "{{secret.SOAP_USER}}",
					Password: "{{secret.SOAP_PASS}}",
				},
			},
		},
	}
	protectedRef, refValues, _, err := protectRuntimeOverrideSecretsWithOptions("test-scope", 0, script, refOverride, true, false, false)
	if err != nil {
		t.Fatalf("protectRuntimeOverrideSecretsWithOptions with exact secret refs: %v", err)
	}
	if len(refValues) != 0 {
		t.Fatalf("expected 0 sealed values for exact secret refs, got %#v", refValues)
	}
	if protectedRef.SOAP.WSSecurity.UsernameToken.Username != "{{secret.SOAP_USER}}" ||
		protectedRef.SOAP.WSSecurity.UsernameToken.Password != "{{secret.SOAP_PASS}}" {
		t.Fatalf("protectedRef UsernameToken = %#v, want exact secret refs preserved", protectedRef.SOAP.WSSecurity.UsernameToken)
	}

	// Seal raw plaintext credentials into deterministic internal references.
	rawOverride := &RuntimeSettingsOverride{
		SOAP: &dsl.SOAPConfig{
			Version: "1.2",
			WSSecurity: &dsl.SOAPWSSecurityConfig{
				UsernameToken: &dsl.SOAPUsernameTokenConfig{
					Username:     "soap-admin",
					Password:     "super-secret-pw",
					PasswordType: "PasswordDigest",
				},
				Timestamp: &dsl.SOAPTimestampConfig{TTLSeconds: 60},
			},
		},
	}
	protected, values, changed, err := protectRuntimeOverrideSecretsWithOptions("test-scope", 0, script, rawOverride, true, false, false)
	if err != nil {
		t.Fatalf("protectRuntimeOverrideSecretsWithOptions plaintext: %v", err)
	}
	if !changed || len(values) != 2 {
		t.Fatalf("changed=%v values=%#v, want changed=true and 2 sealed values", changed, values)
	}
	tok := protected.SOAP.WSSecurity.UsernameToken
	if !strings.HasPrefix(tok.Username, "{{secret."+runtimeOverrideSecretPrefix) ||
		!strings.HasPrefix(tok.Password, "{{secret."+runtimeOverrideSecretPrefix) {
		t.Fatalf("protected UsernameToken = %#v, want reserved secret references", tok)
	}

	// 2. maskRuntimeSettingsWithKeyRule masks Username (when password/password_type is set) and Password.
	masked := maskRuntimeSettings(protected)
	maskedTok := masked.SOAP.WSSecurity.UsernameToken
	if maskedTok.Username != scrub.Redacted || maskedTok.Password != scrub.Redacted {
		t.Fatalf("masked UsernameToken = %#v, want both Username and Password redacted", maskedTok)
	}
	if masked.SOAP.Version != "1.2" || masked.SOAP.WSSecurity.Timestamp.TTLSeconds != 60 {
		t.Fatalf("non-secret SOAP fields mutated by mask: %#v", masked.SOAP)
	}
	// Input must not be mutated.
	if protected.SOAP.WSSecurity.UsernameToken.Password == scrub.Redacted {
		t.Fatal("maskRuntimeSettings mutated input SOAP config")
	}

	// 3. reconcileGroupRuntimeSecrets preserves prior secrets on scrub.Redacted and clears on "".
	echoed := &RuntimeSettingsOverride{
		SOAP: &dsl.SOAPConfig{
			Version: "1.1",
			WSSecurity: &dsl.SOAPWSSecurityConfig{
				UsernameToken: &dsl.SOAPUsernameTokenConfig{
					Username:     scrub.Redacted,
					Password:     scrub.Redacted,
					PasswordType: "PasswordText",
				},
			},
		},
	}
	reconciled := reconcileGroupRuntimeSecrets(echoed, protected)
	recTok := reconciled.SOAP.WSSecurity.UsernameToken
	if recTok.Username != tok.Username || recTok.Password != tok.Password {
		t.Fatalf("reconciled UsernameToken = %#v, want prior protected refs %q / %q", recTok, tok.Username, tok.Password)
	}
	if reconciled.SOAP.Version != "1.1" || recTok.PasswordType != "PasswordText" {
		t.Fatalf("reconciled non-secret fields = %#v, want updated values", reconciled.SOAP)
	}

	// Clearing with "" clears the stored credentials.
	cleared := &RuntimeSettingsOverride{
		SOAP: &dsl.SOAPConfig{
			WSSecurity: &dsl.SOAPWSSecurityConfig{
				UsernameToken: &dsl.SOAPUsernameTokenConfig{
					Username: "",
					Password: "",
				},
			},
		},
	}
	reconciledCleared := reconcileGroupRuntimeSecrets(cleared, protected)
	if reconciledCleared.SOAP.WSSecurity.UsernameToken.Username != "" ||
		reconciledCleared.SOAP.WSSecurity.UsernameToken.Password != "" {
		t.Fatalf("cleared UsernameToken = %#v, want empty strings", reconciledCleared.SOAP.WSSecurity.UsernameToken)
	}

	// Partial OverrideFields excluding soap.ws_security.username_token must ignore unselected [REDACTED] sentinels
	// and mint 0 secret values.
	partialEcho := &RuntimeSettingsOverride{
		SOAP: &dsl.SOAPConfig{
			Version: "1.2",
			WSSecurity: &dsl.SOAPWSSecurityConfig{
				UsernameToken: &dsl.SOAPUsernameTokenConfig{
					Username: scrub.Redacted,
					Password: scrub.Redacted,
				},
			},
		},
		OverrideFields: []string{"soap.version"},
	}
	partialProtected, partialValues, _, err := protectRuntimeOverrideSecretsWithOptions("test-scope", 0, script, partialEcho, true, false, false)
	if err != nil {
		t.Fatalf("protectRuntimeOverrideSecretsWithOptions with unselected [REDACTED] failed: %v", err)
	}
	if len(partialValues) != 0 {
		t.Fatalf("partialValues = %#v, want 0 minted secrets for unselected fields", partialValues)
	}
	if partialProtected.SOAP == nil || partialProtected.SOAP.Version != "1.2" || partialProtected.SOAP.WSSecurity != nil {
		t.Fatalf("partialProtected.SOAP = %#v, want Version=1.2 and nil WSSecurity", partialProtected.SOAP)
	}
}
