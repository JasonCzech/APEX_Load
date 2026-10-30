package salesforce

import (
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func auraTokenScript(token string) *dsl.Script {
	body := "message=" + url.QueryEscape(`{"actions":[{"id":"1;a","descriptor":"aura://ComponentController/ACTION$getComponent","params":{}}]}`) +
		"&aura.context=" + url.QueryEscape(`{"mode":"PROD","fwuid":"abcdef123456","app":"one:one"}`) +
		"&aura.token=" + token
	return &dsl.Script{
		Name:     "Aura token provenance",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.lightning.force.com"},
		Actions: []dsl.Action{
			{Name: "App", Type: "GET", Method: "GET", Target: "/one/one.app"},
			{Name: "Aura", Type: "POST", Method: "POST", Target: "/aura?r=1", Body: body},
			{Name: "Aura 2", Type: "POST", Method: "POST", Target: "/aura?r=2", Body: body},
		},
	}
}

// A Chrome DevTools HAR drops the Set-Cookie that issues the desktop
// aura.token, so nothing correlates it and every Aura request replays a stale
// token. The review must say so instead of staying silent.
func TestAuraTokenProvenanceWarning(t *testing.T) {
	const token = "eyJub25jZSI6InJlY29yZGVkIn0"
	if warning := auraTokenProvenanceWarning(auraTokenScript(token), nil, false); !strings.Contains(warning, "2 Aura request(s)") || !strings.Contains(warning, "no response that issues it") {
		t.Fatalf("missing-producer warning = %q", warning)
	}
	producer := []correlate.Correlation{{Name: "corr_cookie_host_eric_prod", Value: token}}
	if warning := auraTokenProvenanceWarning(auraTokenScript(token), producer, false); !strings.Contains(warning, "contains a producer") {
		t.Fatalf("unapplied-producer warning = %q", warning)
	}
	if warning := auraTokenProvenanceWarning(auraTokenScript("{{var.corr_cookie_host_eric_prod_encoded}}"), producer, false); warning != "" {
		t.Fatalf("wired token produced a warning: %q", warning)
	}
	for _, placeholder := range []string{"undefined", "null"} {
		if warning := auraTokenProvenanceWarning(auraTokenScript(placeholder), nil, false); warning != "" {
			t.Fatalf("guest placeholder %q produced a warning: %q", placeholder, warning)
		}
	}
}
