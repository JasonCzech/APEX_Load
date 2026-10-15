package dsl

import (
	"strings"
	"testing"
)

func TestPropertyAnalyticsValidation(t *testing.T) {
	good := PropertyAnalytics{Name: "operation", Extraction: "operation_type", Values: []string{"checkout", "search"}}
	if got := ValidatePropertyAnalytics([]PropertyAnalytics{good}); len(got) > 0 {
		t.Fatal(got)
	}
	for _, bad := range []PropertyAnalytics{{Name: "token", Extraction: "token", Values: []string{"yes"}}, {Name: "email", Extraction: "customer_email", Values: []string{"yes"}}, {Name: "kind", Extraction: "kind", Values: []string{"other"}}, {Name: "kind", Extraction: "kind", Values: []string{"yes", "yes"}}, {Name: "kind", Extraction: "kind"}} {
		if len(ValidatePropertyAnalytics([]PropertyAnalytics{bad})) == 0 {
			t.Fatalf("invalid promotion accepted: %+v", bad)
		}
	}
	if len(ValidatePropertyAnalytics([]PropertyAnalytics{good, good})) == 0 {
		t.Fatal("duplicate properties accepted")
	}
}

// browser_iteration is the engine's: property analytics cannot use it.
func TestPropertyAnalyticsReservesTheBrowserIterationName(t *testing.T) {
	issues := ValidatePropertyAnalytics([]PropertyAnalytics{{Name: "browser_iteration", Extraction: "tier", Values: []string{"gold"}}})
	if len(issues) != 1 || !strings.Contains(issues[0], "reserved") {
		t.Fatalf("issues = %v", issues)
	}
}
