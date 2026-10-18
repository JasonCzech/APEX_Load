package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

// A Lightning page keeps a hidden copy of an earlier page: a recorded click
// replays on the visible button, not its hidden twin, and a selector that
// matched two visible buttons when recorded is not the one replay uses.
func TestRecordedLightningSelectorsMatchOneVisibleElement(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration")
	}
	const page = `<!doctype html><html><head><title>Account</title><script>window.$A = { getCallback: (callback) => callback };</script></head><body>
<div hidden><button title="Save">Save</button></div>
<div><button id="save-12" title="Save" onclick="document.getElementById('out').textContent = 'saved'">Save</button></div>
<div><button title="Edit">Edit</button><button title="Edit" onclick="document.getElementById('out').textContent += ' edited'">Edit</button></div>
<div id="out"></div>
</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, page)
	}))
	defer server.Close()
	recorder := New(Options{StartURL: server.URL, Headless: true})
	if err := recorder.Start(); err != nil {
		if os.Getenv("APEXLOAD_BROWSER_TESTS") == "require" {
			t.Fatal(err)
		}
		t.Skipf("browser runtime unavailable: %v", err)
	}
	defer recorder.Stop()
	browserPage := recorder.context.Pages()[0]
	if err := browserPage.Locator("#save-12").Click(); err != nil {
		t.Fatal(err)
	}
	waitForSteps(t, recorder, 1, 5*time.Second)
	if err := browserPage.Locator(`button[title="Edit"]`).Nth(1).Click(); err != nil {
		t.Fatal(err)
	}
	waitForSteps(t, recorder, 2, 5*time.Second)
	session, err := recorder.Stop()
	if err != nil {
		t.Fatal(err)
	}
	clicks := []Step{}
	for _, step := range session.Steps {
		if step.Kind == StepClick {
			clicks = append(clicks, step)
		}
	}
	if len(clicks) != 2 {
		t.Fatalf("clicks = %+v", clicks)
	}
	// The Save button's generated id ranks below its title, which matches
	// only the visible button.
	if top := clicks[0].Candidates; len(top) < 2 || top[1].Value != `button[title="Save"] >> visible=true` {
		t.Fatalf("Save candidates = %+v", top)
	}
	for _, candidate := range clicks[1].Candidates {
		if candidate.Value == `button[title="Edit"] >> visible=true` && candidate.Confidence != ambiguousConfidence {
			t.Fatalf("the title matching two visible buttons was not demoted: %+v", clicks[1].Candidates)
		}
	}

	converted := Convert(session, ConvertOptions{})
	b := &browserprotocol.Browser{}
	if err := b.Init(context.Background(), map[string]any{"base_url": server.URL}); err != nil {
		t.Fatal(err)
	}
	defer b.Teardown(context.Background())
	for _, a := range converted.Script.Actions {
		encoded, _ := json.Marshal(a)
		action := &protocol.Action{}
		if err := json.Unmarshal(encoded, action); err != nil {
			t.Fatal(err)
		}
		result, err := b.Execute(context.Background(), action)
		if err != nil || !result.Success {
			t.Fatalf("replay %s (%s): %v %s", a.Name, action.Selector, err, result.ErrorMessage)
		}
		if strings.Contains(action.Selector, `title="Edit"`) {
			t.Fatalf("replay used the ambiguous selector %q", action.Selector)
		}
		// The recorded selector itself works: replay does not heal it.
		if healed, ok := result.ProtocolEvidence["selector_self_heal"]; ok {
			t.Fatalf("replay of %s self-healed from %q: %v", a.Name, action.Selector, healed)
		}
	}
	check, err := b.Execute(context.Background(), &protocol.Action{Name: "Result", Type: "browser_wait", WaitFor: &protocol.WaitCondition{Type: "text", Value: "saved edited", TimeoutMs: 3000}})
	if err != nil || !check.Success {
		t.Fatalf("replayed clicks did not reach the visible buttons: %v %+v", err, check)
	}
}

// A verification code typed on a Salesforce identity-verification page is
// masked at capture, like a password: its digits never leave the page.
func TestRecordedVerificationCodeIsMasked(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><body><input id="smc" type="text"><input id="other" type="text" autocomplete="one-time-code"></body></html>`)
	}))
	defer server.Close()
	recorder := New(Options{StartURL: server.URL + "/_ui/identity/verification/method/SmsVerificationFinish", Headless: true})
	if err := recorder.Start(); err != nil {
		if os.Getenv("APEXLOAD_BROWSER_TESTS") == "require" {
			t.Fatal(err)
		}
		t.Skipf("browser runtime unavailable: %v", err)
	}
	defer recorder.Stop()
	page := recorder.context.Pages()[0]
	for index, field := range []string{"#smc", "#other"} {
		if err := page.Locator(field).Fill("482913"); err != nil {
			t.Fatal(err)
		}
		if err := page.Locator(field).Press("Tab"); err != nil {
			t.Fatal(err)
		}
		waitForSteps(t, recorder, index+1, 5*time.Second)
	}
	session, err := recorder.Stop()
	if err != nil {
		t.Fatal(err)
	}
	fills := 0
	for _, step := range session.Steps {
		if step.Kind != StepFill {
			continue
		}
		fills++
		if !step.Masked || step.Value != "" {
			t.Fatalf("verification code step = %+v", step)
		}
	}
	if fills == 0 {
		t.Fatalf("no fill recorded: %+v", session.Steps)
	}
	encoded, _ := json.Marshal(session)
	if strings.Contains(string(encoded), "482913") {
		t.Fatal("the code reached the recording")
	}
}
