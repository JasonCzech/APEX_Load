package enterprisearchive

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// These tests encode the exact stored configs observed for the production
// re-import (apexload.db) so the parity outcome is proven, not assumed:
//   - acc_wls_reg_1 auto-clears on the fixed binary with NO re-import.
//   - the ACME scripts stay flagged until re-imported (their stored copy still
//     carries the legacy flat web_cleanup_cookies attribute), and a fresh
//     conversion clears them by emitting an executable cookie_clear action.

func compatibilityOnlyCount(t *testing.T, script *dsl.Script) int {
	t.Helper()
	parity := BuildRTSParityReportFromScript(script)
	return RuntimeSettingStatusCounts(parity)[string(RuntimeSettingStatusCompatibilityOnly)]
}

// acc_wls_reg_1 stored config: only native no-op (max_html_param_len) and a
// TLS-mapped SSL_VERSION remain. The fixed recognizer clears it without any
// re-import.
func TestStoredAccWlsConfigAutoClearsAfterFix(t *testing.T) {
	script := &dsl.Script{
		Name:     "acc_wls_reg_1",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				ConnectTimeoutMs: 120000,
				RequestTimeoutMs: 120000,
				TLS:              dsl.TLSConfig{MinVersion: "1.2"},
			},
			WebRuntime: dsl.WebRuntimeConfig{
				AdditionalAttributes: map[string]string{
					"web_set_max_html_param_len":         "99999",
					"web_set_sockets_option.SSL_VERSION": "2&3",
				},
			},
		},
	}
	if got := compatibilityOnlyCount(t, script); got != 0 {
		t.Fatalf("acc_wls stored config compatibility_only = %d, want 0 (auto-clears, no re-import)", got)
	}
}

// ACME stored config still carries the legacy flat web_cleanup_cookies; the
// fixed recognizer deliberately does NOT bless an un-executed cookie attribute,
// so the stored copy stays flagged until re-imported. This pins the "needs
// re-import" behavior so it is intentional, not a regression.
func TestStoredACMEConfigStillFlagsLegacyCookieAttribute(t *testing.T) {
	script := &dsl.Script{
		Name:     "ACME_IAO_Azure_Akamai_2025",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			HTTP: dsl.HTTPConfig{
				ConnectTimeoutMs: 500000,
				RequestTimeoutMs: 500000,
				TLS:              dsl.TLSConfig{MinVersion: "1.2"},
			},
			WebRuntime: dsl.WebRuntimeConfig{
				AdditionalAttributes: map[string]string{
					"web_cache_cleanup":                  "true",
					"web_cleanup_cookies":                "true",
					"web_set_sockets_option.SSL_VERSION": "TLS1.2",
					"web_set_timeout.RECEIVE":            "500",
					"web_set_timeout.STEP":               "500",
				},
			},
		},
	}
	if got := compatibilityOnlyCount(t, script); got != 1 {
		t.Fatalf("ACME stored config compatibility_only = %d, want 1 (legacy flat web_cleanup_cookies needs re-import)", got)
	}
}

// Re-importing an ACME-shaped script with the fixed converter emits an
// executable cookie_clear action and drops the flat cookie attribute, so the
// resulting draft is clean.
func TestReimportedACMEConfigIsClean(t *testing.T) {
	actionC := `
#include "web_api.h"

Action()
{
	web_set_sockets_option("SSL_VERSION", "TLS1.2");
	web_set_timeout("RECEIVE", "500");
	web_set_timeout("STEP", "500");
	web_cache_cleanup();
	web_cleanup_cookies();

	web_url("Home",
		"URL=https://acme.example.com/app/home",
		LAST);

	return 0;
}
`
	parsed, err := ParseActionFile("Action.c", actionC)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatalf("convert error: %v", err)
	}

	attrs := result.Script.Config.WebRuntime.AdditionalAttributes
	if _, ok := attrs["web_cleanup_cookies"]; ok {
		t.Fatalf("attrs = %#v, want web_cleanup_cookies emitted as cookie_clear action, not an attribute", attrs)
	}
	hasCookieClear := false
	for _, a := range result.Script.Actions {
		if a.Type == "cookie_clear" {
			hasCookieClear = true
		}
	}
	if !hasCookieClear {
		t.Fatalf("actions = %#v, want a cookie_clear action", result.Script.Actions)
	}
	if got := compatibilityOnlyCount(t, result.Script); got != 0 {
		t.Fatalf("reimported ACME config compatibility_only = %d, want 0 (clean)", got)
	}
}
