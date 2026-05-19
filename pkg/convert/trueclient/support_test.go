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
		{name: "desktop truclient", scriptType: "TruClient", wantName: "TruClient", wantStatus: ScopeOutOfScope, wantFound: true},
		{name: "desktop truclient compact", scriptType: "TruClientWeb", wantName: "TruClient", wantStatus: ScopeOutOfScope, wantFound: true},
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
		{stepName: "Select", wantName: "Select", wantStatus: ScopeSupported, wantType: "browser_select", wantOK: true},
		{stepName: "Verify", wantName: "Verify", wantStatus: ScopeSupported, wantType: "action assertions", wantOK: true},
		{stepName: "Start Transaction", wantName: "Start Transaction", wantStatus: ScopeSupported, wantType: "transaction metadata", wantOK: true},
		{stepName: "Scroll", wantName: "Scroll", wantStatus: ScopeSupported, wantType: "browser_swipe", wantOK: true},
		{stepName: "Evaluate JavaScript", wantName: "Evaluate JavaScript", wantStatus: ScopeReviewOnly, wantType: "browser_fill, browser_click, browser_swipe, or browser_navigate when decomposable", wantOK: true},
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
		if scope.DisplayName != tt.wantName || scope.Status != tt.wantStatus || scope.StormrunnerShape != tt.wantType {
			t.Fatalf("LookupStepScope(%q) = %#v, want name=%q status=%q type=%q", tt.stepName, scope, tt.wantName, tt.wantStatus, tt.wantType)
		}
	}
}

func TestImportScopeError(t *testing.T) {
	err := ImportScopeError("TruClient - Mobile Web")
	if err != nil {
		t.Fatalf("ImportScopeError mobile web = %v, want nil for supported scope", err)
	}

	err = ImportScopeError("TruClient")
	if err == nil || !containsAll(err.Error(), "out of scope", "desktop-style TruClient parity") {
		t.Fatalf("ImportScopeError desktop = %v, want out-of-scope guidance", err)
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
