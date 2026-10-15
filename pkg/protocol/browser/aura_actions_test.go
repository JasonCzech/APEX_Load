package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// A step that sends an Aura request records each action it triggered, named
// by its Apex method or descriptor, with the state its response gave; no
// value the actions sent or got back is kept.
func TestBrowserStepRecordsItsAuraActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/aura" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"actions":[{"id":"1;a","state":"SUCCESS","returnValue":{"secret":"returned-secret"}},{"id":"2;a","state":"ERROR","error":[{"message":"no access"}]}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><body><button id="search">Search</button><div id="out"></div>
<script>
document.getElementById('search').addEventListener('click', async () => {
  const message = JSON.stringify({actions: [
    {id: '1;a', descriptor: 'aura://ApexActionController/ACTION$execute', params: {namespace: '', classname: 'AccountSearch', method: 'find', params: {term: 'sent-secret'}}},
    {id: '2;a', descriptor: 'serviceComponent://ui.force.components.controllers.recordGlobalValueProvider.RecordGvpController/ACTION$getRecord', params: {recordDescriptor: 'sent-record'}}
  ]});
  const response = await fetch('/aura?r=1&aura.ApexAction.execute=1', {method: 'POST', headers: {'Content-Type': 'application/x-www-form-urlencoded'},
    body: 'message=' + encodeURIComponent(message) + '&aura.token=sent-token'});
  await response.text();
  document.getElementById('out').textContent = 'done';
});
</script></body></html>`)
	}))
	defer server.Close()
	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{"base_url": server.URL, "browser_headless": true}); err != nil {
		requireBrowserRuntime(t, "chromium", err)
	}
	defer b.Teardown(context.Background())
	for _, action := range []protocol.Action{
		{Name: "Open", Type: "browser_navigate", Target: "/"},
		{Name: "Search", Type: "browser_click", Selector: "#search", Sync: &protocol.BrowserSyncPolicy{Type: "element_visible", Value: "text=done"}},
	} {
		result, err := b.Execute(context.Background(), &action)
		if err != nil || !result.Success {
			t.Fatalf("%s: %v %+v", action.Name, err, result)
		}
		if action.Name != "Search" {
			continue
		}
		evidence, _ := result.ProtocolEvidence["aura_actions"].(map[string]any)
		actions, _ := evidence["actions"].([]any)
		if evidence["requests"] != 1 || len(actions) != 2 {
			t.Fatalf("aura evidence = %#v", result.ProtocolEvidence["aura_actions"])
		}
		apex, _ := actions[0].(map[string]any)
		record, _ := actions[1].(map[string]any)
		if apex["name"] != "AccountSearch.find" || apex["apex"] != true || apex["state"] != "SUCCESS" || apex["batch_size"] != 2 ||
			!strings.HasSuffix(record["name"].(string), "RecordGvpController/ACTION$getRecord") || record["state"] != "ERROR" {
			t.Fatalf("aura actions = %#v", actions)
		}
		encoded, _ := json.Marshal(result)
		for _, secret := range []string{"returned-secret", "no access"} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("the result kept %q", secret)
			}
		}
		evidenceJSON, _ := json.Marshal(evidence)
		for _, secret := range []string{"sent-secret", "sent-record", "sent-token"} {
			if strings.Contains(string(evidenceJSON), secret) {
				t.Fatalf("the evidence kept %q", secret)
			}
		}
	}
}
