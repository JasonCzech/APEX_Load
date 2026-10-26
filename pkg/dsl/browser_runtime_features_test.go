package dsl

import "testing"

func TestBrowserDocumentRuntimeFeatureIsExplicit(t *testing.T) {
	yes, no := true, false
	script := &Script{Protocol: "browser"}
	if len(RequiredBrowserRuntimeFeatures(script)) != 0 {
		t.Fatal("default capture enabled")
	}
	script.Config.Browser.CaptureDocumentMetrics = &no
	if len(RequiredBrowserRuntimeFeatures(script)) != 0 {
		t.Fatal("explicit false requires capture")
	}
	script.Config.Browser.CaptureDocumentMetrics = &yes
	if f := RequiredBrowserRuntimeFeatures(script); len(f) != 1 || f[0] != FeatureBrowserDocumentEvidenceV2 {
		t.Fatal("explicit capture lacks peer admission")
	}
	script.Protocol = "http"
	if len(RequiredBrowserRuntimeFeatures(script)) != 0 {
		t.Fatal("other protocol acquired browser requirement")
	}
}

func TestBrowserInteractionCaptureIsItsOwnRuntimeFeature(t *testing.T) {
	yes := true
	script := &Script{Protocol: "browser"}
	script.Config.Browser.CaptureInteractions = &yes
	if got := RequiredBrowserRuntimeFeatures(script); len(got) != 1 || got[0] != FeatureBrowserInteractionEvidenceV2 {
		t.Fatalf("features %v", got)
	}
	if BrowserDocumentCaptureRequested(script) || !BrowserInteractionCaptureRequested(script) {
		t.Fatal("interaction capture read as document capture")
	}
	script.Config.Browser.CaptureDocumentMetrics = &yes
	if got := RequiredBrowserRuntimeFeatures(script); len(got) != 2 {
		t.Fatalf("features %v", got)
	}
	script.Protocol = "http"
	if len(RequiredBrowserRuntimeFeatures(script)) != 0 || BrowserInteractionCaptureRequested(script) {
		t.Fatal("non-browser script requires browser features")
	}
}

func TestBrowserHARCaptureRuntimeFeatureAndPolicyPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		protocol  string
		capture   string
		policy    string
		effective string
		requested bool
	}{
		{name: "default_empty", protocol: "browser", effective: "off", requested: false},
		{name: "explicit_off", protocol: "browser", capture: "off", effective: "off", requested: false},
		{name: "always", protocol: "browser", capture: "always", effective: "always", requested: true},
		{name: "on_error", protocol: "browser", capture: "on_error", effective: "on_error", requested: true},
		{name: "on_sla_breach", protocol: "browser", capture: "on_sla_breach", effective: "on_sla_breach", requested: true},
		{name: "manual_trimmed_case", protocol: " Browser ", capture: " MANUAL ", effective: "manual", requested: true},
		{name: "policy_off_overrides_capture_always", protocol: "browser", capture: "always", policy: "off", effective: "off", requested: false},
		{name: "policy_whitespace_overrides_capture_always", protocol: "browser", capture: "always", policy: "   ", effective: "off", requested: false},
		{name: "policy_always_overrides_capture_off", protocol: "browser", capture: "off", policy: "always", effective: "always", requested: true},
		{name: "non_browser_ignored", protocol: "http", capture: "always", effective: "off", requested: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := &Script{
				Protocol: tc.protocol,
				Config: ScriptConfig{
					Browser: BrowserConfig{
						HARCapture:       tc.capture,
						HARCapturePolicy: tc.policy,
					},
				},
			}
			if got := EffectiveBrowserHARCapturePolicy(script); got != tc.effective {
				t.Fatalf("EffectiveBrowserHARCapturePolicy() = %q, want %q", got, tc.effective)
			}
			if got := BrowserHARCaptureRequested(script); got != tc.requested {
				t.Fatalf("BrowserHARCaptureRequested() = %v, want %v", got, tc.requested)
			}
			features := RequiredBrowserRuntimeFeatures(script)
			hasFeature := len(features) == 1 && features[0] == FeatureBrowserNativeHARV1
			if hasFeature != tc.requested {
				t.Fatalf("RequiredBrowserRuntimeFeatures() = %v, want requested=%v", features, tc.requested)
			}
		})
	}
	if EffectiveBrowserHARCapturePolicy(nil) != "off" || BrowserHARCaptureRequested(nil) {
		t.Fatal("nil script requested HAR capture")
	}

	yes := true
	combined := &Script{
		Protocol: "browser",
		Config: ScriptConfig{
			Browser: BrowserConfig{
				CaptureDocumentMetrics: &yes,
				CaptureInteractions:    &yes,
				HARCapture:             "on_error",
				Session: &BrowserSessionConfig{
					Strategy: BrowserSessionSalesforceJWT,
				},
			},
		},
	}
	wantCombined := []string{
		FeatureBrowserSessionIdentitiesV1,
		FeatureBrowserSalesforceJWTV1,
		FeatureBrowserDocumentEvidenceV2,
		FeatureBrowserInteractionEvidenceV2,
		FeatureBrowserNativeHARV1,
	}
	gotCombined := RequiredBrowserRuntimeFeatures(combined)
	if len(gotCombined) != len(wantCombined) {
		t.Fatalf("combined features = %v, want %v", gotCombined, wantCombined)
	}
	for i := range wantCombined {
		if gotCombined[i] != wantCombined[i] {
			t.Fatalf("combined features[%d] = %q, want %q (%v)", i, gotCombined[i], wantCombined[i], gotCombined)
		}
	}
}
