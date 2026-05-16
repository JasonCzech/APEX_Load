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
		{name: "mobile web", scriptType: "TruClient - Mobile Web", wantName: "TruClient - Mobile Web", wantStatus: ScopePlanned, wantFound: true},
		{name: "mobile web normalized", scriptType: " truclient_mobile_web ", wantName: "TruClient - Mobile Web", wantStatus: ScopePlanned, wantFound: true},
		{name: "desktop truclient", scriptType: "TruClient", wantName: "TruClient", wantStatus: ScopeOutOfScope, wantFound: true},
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
		stepName string
		wantName string
		wantType string
		wantOK   bool
	}{
		{stepName: "Set / Type", wantName: "Set / Type", wantType: "browser_fill", wantOK: true},
		{stepName: "Start Transaction", wantName: "Start Transaction", wantType: "transaction metadata", wantOK: true},
		{stepName: "Evaluate JavaScript", wantName: "Evaluate JavaScript", wantType: "not supported", wantOK: true},
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
		if scope.DisplayName != tt.wantName || scope.StormrunnerShape != tt.wantType {
			t.Fatalf("LookupStepScope(%q) = %#v, want name=%q type=%q", tt.stepName, scope, tt.wantName, tt.wantType)
		}
	}
}

func TestImportScopeError(t *testing.T) {
	err := ImportScopeError("TruClient - Mobile Web")
	if err == nil || err.Error() == "" {
		t.Fatal("ImportScopeError should describe planned TruClient Mobile Web scope")
	}
	if got := err.Error(); got == "" || !containsAll(got, "recognized but not implemented yet", "navigate", "transaction") {
		t.Fatalf("ImportScopeError mobile web = %q, want planned scope guidance", got)
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