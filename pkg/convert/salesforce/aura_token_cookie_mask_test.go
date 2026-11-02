package salesforce_test

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
)

// The confirmed ERIC cookie extractor must survive secret masking unchanged:
// a redacted expression breaks the regex, and every Aura request then sends a
// stale token.
func TestConfirmedAuraTokenCookieExtractorSurvivesMasking(t *testing.T) {
	for _, cookieName := range []string{"__Host-ERIC_PROD-2876452450001777317", "__Host-ERIC_PROD1049330172993496433"} {
		expression := salesforce.NamedAuraTokenCookieExtractorForTest(cookieName)
		script := &dsl.Script{Name: "aura", Protocol: "http", Actions: []dsl.Action{{
			Type: "request", Name: "one.app", Method: "GET", Target: "https://example.lightning.force.com/one/one.app",
			Extractors: []dsl.Extractor{{Type: "regex", Source: "header", Expression: expression, Name: "corr_salesforce_aura_token", OnMiss: "fail"}},
		}}}
		projected := importreview.MaskScriptSecrets(script)
		if got := projected.Actions[0].Extractors[0].Expression; got != expression {
			t.Fatalf("%s: confirmed aura.token extractor was masked: %s", cookieName, got)
		}
		if requirements := importreview.DiscoverSecretBindingRequirements(projected, 0); len(requirements) != 0 {
			t.Fatalf("%s: requirements = %#v, want none", cookieName, requirements)
		}
	}
}
