package trueclient

import (
	"strings"
	"testing"
)

func TestLookupScriptTypeScope(t *testing.T) {
	tests := []struct {
		name       string
		scriptType string
		wantName   string
		wantStatus ScopeStatus
		wantFound  bool
	}{
		{name: "mobile web", scriptType: "TruClient - Mobile Web", wantName: "TruClient - Mobile Web", wantStatus: ScopeSupported, wantFound: true},
		{name: "mobile web normalized", scriptType: " truclient_mobile_web ", wantName: "TruClient - Mobile Web", wantStatus: ScopeSupported, wantFound: true},
		{name: "mobile web compact", scriptType: "TruClientMobileWeb", wantName: "TruClient - Mobile Web", wantStatus: ScopeSupported, wantFound: true},
		{name: "truclient 2 mobile web", scriptType: "TruClient 2.0 - Mobile Web", wantName: "TruClient - Mobile Web", wantStatus: ScopeSupported, wantFound: true},
		{name: "truclient 2 mobile web compact", scriptType: "TruClient2MobileWeb", wantName: "TruClient - Mobile Web", wantStatus: ScopeSupported, wantFound: true},
		{name: "truclient web", scriptType: "TruClient - Web", wantName: "TruClient - Web", wantStatus: ScopePlanned, wantFound: true},
		{name: "truclient web compact", scriptType: "TruClientWeb", wantName: "TruClient - Web", wantStatus: ScopePlanned, wantFound: true},
		{name: "truclient 2 web", scriptType: "TruClient 2.0 - Web", wantName: "TruClient 2.0 - Web", wantStatus: ScopeReviewOnly, wantFound: true},
		{name: "truclient 2 web compact", scriptType: "TruClient20Web", wantName: "TruClient 2.0 - Web", wantStatus: ScopeReviewOnly, wantFound: true},
		{name: "desktop truclient", scriptType: "TruClient", wantName: "TruClient", wantStatus: ScopeOutOfScope, wantFound: true},
		{name: "desktop truclient 2", scriptType: "TruClient 2.0", wantName: "TruClient", wantStatus: ScopeOutOfScope, wantFound: true},
		{name: "unknown", scriptType: "Web", wantFound: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scope, ok := LookupScriptTypeScope(tt.scriptType)
			if ok != tt.wantFound {
				t.Fatalf("LookupScriptTypeScope(%q) found=%v, want %v", tt.scriptType, ok, tt.wantFound)
			}
			if !tt.wantFound {
				return
			}
			if scope.DisplayName != tt.wantName || scope.Status != tt.wantStatus {
				t.Fatalf("LookupScriptTypeScope(%q) = %#v, want name=%q status=%q", tt.scriptType, scope, tt.wantName, tt.wantStatus)
			}
		})
	}
}

func TestLookupStepScope(t *testing.T) {
	tests := []struct {
		stepName   string
		wantName   string
		wantStatus ScopeStatus
		wantType   string
		wantOK     bool
	}{
		{stepName: "Set / Type", wantName: "Set / Type", wantStatus: ScopeSupported, wantType: "browser_fill", wantOK: true},
		{stepName: "Double Click", wantName: "Click / Submit / Check", wantStatus: ScopeSupported, wantType: "browser_click", wantOK: true},
		{stepName: "Submit", wantName: "Click / Submit / Check", wantStatus: ScopeSupported, wantType: "browser_click", wantOK: true},
		{stepName: "Select Radio", wantName: "Click / Submit / Check", wantStatus: ScopeSupported, wantType: "browser_click", wantOK: true},
		{stepName: "Select", wantName: "Select", wantStatus: ScopeSupported, wantType: "browser_select", wantOK: true},
		{stepName: "Verify", wantName: "Verify", wantStatus: ScopeSupported, wantType: "action assertions", wantOK: true},
		{stepName: "Start Transaction", wantName: "Start Transaction", wantStatus: ScopeSupported, wantType: "transaction metadata", wantOK: true},
		{stepName: "Scroll", wantName: "Scroll", wantStatus: ScopeSupported, wantType: "browser_swipe", wantOK: true},
		{stepName: "Evaluate JavaScript", wantName: "Evaluate JavaScript", wantStatus: ScopeReviewOnly, wantType: "browser_fill, browser_click, browser_swipe, or browser_navigate when decomposable", wantOK: true},
		{stepName: "Hover", wantName: "Hover", wantStatus: ScopeSupported, wantType: "browser_hover", wantOK: true},
		{stepName: "Press Key", wantName: "Press Key", wantStatus: ScopeSupported, wantType: "browser_press_key", wantOK: true},
		{stepName: "Unknown Step", wantOK: false},
	}

	for _, tt := range tests {
		scope, ok := LookupStepScope(tt.stepName)
		if ok != tt.wantOK {
			t.Fatalf("LookupStepScope(%q) found=%v, want %v", tt.stepName, ok, tt.wantOK)
		}
		if !tt.wantOK {
			continue
		}
		if scope.DisplayName != tt.wantName || scope.Status != tt.wantStatus || scope.APEXLoadShape != tt.wantType {
			t.Fatalf("LookupStepScope(%q) = %#v, want name=%q status=%q type=%q", tt.stepName, scope, tt.wantName, tt.wantStatus, tt.wantType)
		}
	}
}

func TestImportScopeError(t *testing.T) {
	err := ImportScopeError("TruClient - Mobile Web")
	if err != nil {
		t.Fatalf("ImportScopeError mobile web = %v, want nil for supported scope", err)
	}

	err = ImportScopeError("TruClient 2.0 - Web")
	if err == nil || !containsAll(err.Error(), "requires guided import review", "Review-only VuGen Web 2.0") {
		t.Fatalf("ImportScopeError web2 = %v, want review-only guidance", err)
	}

	err = ImportScopeError("TruClient")
	if err == nil || !containsAll(err.Error(), "out of scope", "desktop-style TruClient parity") {
		t.Fatalf("ImportScopeError desktop = %v, want out-of-scope guidance", err)
	}
}

func TestWeb2ProfileCapabilityScopes(t *testing.T) {
	scopes := Web2ProfileCapabilityScopes()
	if len(scopes) == 0 {
		t.Fatal("Web2ProfileCapabilityScopes returned no scopes")
	}

	wantStatuses := map[string]ScopeStatus{
		"archive_detection":          ScopeSupported,
		"step_mapping":               ScopeSupported,
		"object_identification":      ScopeReviewOnly,
		"runtime_settings":           ScopeReviewOnly,
		"browser_to_http_conversion": ScopeReviewOnly,
		"promotion_review_artifacts": ScopeSupported,
		"native_desktop_automation":  ScopeOutOfScope,
		"high_density_browser_load":  ScopeOutOfScope,
	}
	seen := map[string]ProfileCapabilityScope{}
	for _, scope := range scopes {
		seen[scope.CanonicalName] = scope
	}
	for name, status := range wantStatuses {
		scope, ok := seen[name]
		if !ok {
			t.Fatalf("Web2ProfileCapabilityScopes missing %q in %#v", name, scopes)
		}
		if scope.Status != status {
			t.Fatalf("Web2ProfileCapabilityScopes[%q].Status = %q, want %q", name, scope.Status, status)
		}
		if scope.APEXLoadShape == "" || len(scope.RequiredEvidence) == 0 {
			t.Fatalf("Web2ProfileCapabilityScopes[%q] = %#v, want shape and evidence", name, scope)
		}
	}
	if !containsAll(strings.Join(seen["promotion_review_artifacts"].RequiredEvidence, " "), "replay session id", "selector fallback inventory") {
		t.Fatalf("promotion_review_artifacts evidence = %#v, want durable replay and selector evidence", seen["promotion_review_artifacts"].RequiredEvidence)
	}

	scriptScope, ok := LookupScriptTypeScope("TruClient 2.0 - Web")
	if !ok {
		t.Fatal("missing TruClient 2.0 - Web script scope")
	}
	if !containsAll(strings.Join(scriptScope.RequiredEvidence, " "), "promotion review artifact") {
		t.Fatalf("TruClient 2.0 Web evidence = %#v, want promotion review artifact", scriptScope.RequiredEvidence)
	}
}

func TestWeb2FixtureContracts(t *testing.T) {
	fixtures := Web2FixtureContracts()
	if len(fixtures) == 0 {
		t.Fatal("Web2FixtureContracts returned no fixtures")
	}

	// Web 2.0 import fixtures are supported where deterministic converter tests and browser
	// protocol replay evidence exist. Browser-to-HTTP conversion remains planned until the
	// governed draft/diff workflow is complete.
	type contractExpectation struct {
		required bool
		status   ScopeStatus
	}
	expected := map[string]contractExpectation{
		"web2_spa_checkout":               {required: true, status: ScopeSupported},
		"web2_iframe_window":              {required: true, status: ScopeSupported},
		"web2_custom_javascript_api":      {required: true, status: ScopeSupported},
		"web2_object_repository_variants": {required: true, status: ScopeSupported},
		"web2_runtime_parameters":         {required: true, status: ScopeSupported},
		"web2_browser_to_http_conversion": {required: false, status: ScopePlanned},
	}
	seen := map[string]FixtureContract{}
	for _, fixture := range fixtures {
		seen[fixture.ID] = fixture
	}
	for id, want := range expected {
		fixture, ok := seen[id]
		if !ok {
			t.Fatalf("Web2FixtureContracts missing %q in %#v", id, fixtures)
		}
		if fixture.Required != want.required {
			t.Fatalf("Web2FixtureContracts[%q].Required = %v, want %v", id, fixture.Required, want.required)
		}
		if fixture.Status != want.status || len(fixture.Covers) == 0 || len(fixture.RequiredEvidence) == 0 {
			t.Fatalf("Web2FixtureContracts[%q] = %#v, want status %q with coverage and evidence", id, fixture, want.status)
		}
	}
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}
