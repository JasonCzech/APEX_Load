package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestSalesforceEPTURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://acme.lightning.force.com/lightning/page/home":                  "https://acme.lightning.force.com/lightning/page/home?eptVisible=1",
		"https://acme.lightning.force.com/lightning/r/Account/001/view?b=2&a=1": "https://acme.lightning.force.com/lightning/r/Account/001/view?b=2&a=1&eptVisible=1",
		"https://acme.my.salesforce.com/one/one.app#/sObject/Account/home":      "https://acme.my.salesforce.com/one/one.app?eptVisible=1#/sObject/Account/home",
		"https://acme.lightning.force.com/lightning/page/home?eptVisible=1":     "https://acme.lightning.force.com/lightning/page/home?eptVisible=1",
		"https://acme.my.salesforce.com/secur/frontdoor.jsp":                    "https://acme.my.salesforce.com/secur/frontdoor.jsp",
		"https://example.com/home":                                              "https://example.com/home",
		"not a url":                                                             "not a url",
	} {
		if got := salesforceEPTURL(raw); got != want {
			t.Errorf("salesforceEPTURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

// In calibration mode the observer opens Lightning with eptVisible=1 and
// reports each step's EPT reading when the counter changed; an unchanged
// counter, which still shows an earlier page's time, is not reported.
func TestSalesforceEPTCalibrationReadsChangedCounters(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Query().Get("eptVisible") != "1" {
			fmt.Fprint(w, `<html><body><div id="app">home</div></body></html>`)
			return
		}
		fmt.Fprint(w, `<html><body><header><span class="ept">EPT: 1.25s</span></header>
<button id="next" onclick="document.querySelector('.ept').textContent='EPT 800 ms'">next</button>
<button id="stay" onclick="this.textContent='stayed'">stay</button></body></html>`)
	}))
	defer server.Close()
	eptOf := func(result *protocol.Result) any {
		evidence, _ := result.ProtocolEvidence[SalesforceEPTEvidenceKey].(map[string]any)
		return evidence["ms"]
	}
	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{"base_url": server.URL, "browser_headless": true, "browser_ept_calibration": true}); err != nil {
		requireBrowserRuntime(t, "", err)
	}
	defer func() { _ = b.Teardown(context.Background()) }()
	steps := []struct {
		action *protocol.Action
		want   any
	}{
		{&protocol.Action{Name: "Home", Type: "browser_navigate", Target: "/lightning/page/home"}, float64(1250)},
		{&protocol.Action{Name: "Next", Type: "browser_click", Selector: "#next"}, float64(800)},
		{&protocol.Action{Name: "Stay", Type: "browser_click", Selector: "#stay"}, nil},
	}
	for _, step := range steps {
		result := executeSuccessfulBrowserAction(t, b, step.action)
		if got := eptOf(result); got != step.want {
			t.Fatalf("%s: EPT = %v, want %v", step.action.Name, got, step.want)
		}
	}

	plain := &Browser{}
	if err := plain.Init(context.Background(), map[string]any{"base_url": server.URL, "browser_headless": true}); err != nil {
		requireBrowserRuntime(t, "", err)
	}
	defer func() { _ = plain.Teardown(context.Background()) }()
	result := executeSuccessfulBrowserAction(t, plain, &protocol.Action{Name: "Home", Type: "browser_navigate", Target: "/lightning/page/home"})
	if result.ProtocolEvidence[SalesforceEPTEvidenceKey] != nil {
		t.Fatalf("a run without calibration read EPT: %v", result.ProtocolEvidence)
	}
}
