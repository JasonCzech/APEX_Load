package browser

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	harconvert "github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/salesforce"
	"github.com/jasonczech/apexload/pkg/internal/aurafixture"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

// Desktop Chromium integration: this proves local recording/replay mechanics,
// not Chrome on a physical iPad or a live Salesforce session.
func TestRecordAndReplayReactAuraWithSessionCorrelation(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration")
	}
	react, reactDOM, err := aurafixture.LoadReactDependencies("../../../dashboard")
	if err != nil {
		if os.Getenv("APEXLOAD_BROWSER_TESTS") == "require" {
			t.Fatal(err)
		}
		t.Skip("React fixture dependency unavailable; install dashboard dependencies")
	}
	fixture, err := aurafixture.New(aurafixture.Options{
		ReactJS:       react,
		ReactDOMJS:    reactDOM,
		ResponseDelay: 150 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fixture)
	defer server.Close()
	recorder := New(Options{StartURL: server.URL, Headless: true, CaptureHAR: true})
	if err := recorder.Start(); err != nil {
		if os.Getenv("APEXLOAD_BROWSER_TESTS") == "require" {
			t.Fatal(err)
		}
		t.Skipf("browser runtime unavailable: %v", err)
	}
	defer recorder.Stop()
	page := recorder.context.Pages()[0]
	if _, err := page.WaitForFunction(`document.querySelector('[data-testid="client-state"]').textContent === 'Client state: cold'`, nil); err != nil {
		t.Fatal(err)
	}
	const domIDExpression = `document.querySelector('[aria-label="Run Aura batch"]').id`
	recordedID, err := page.Evaluate(domIDExpression)
	if err != nil {
		t.Fatal("fixture DOM identity unavailable")
	}
	id, ok := recordedID.(string)
	if !ok || id == "" {
		t.Fatal("fixture DOM identity empty")
	}
	domIDs := map[string]bool{id: true}
	if err := page.Fill(`[aria-label="Account name"]`, "alice"); err != nil {
		t.Fatal(err)
	}
	waitForSteps(t, recorder, 1, 5*time.Second)
	if err := page.Click(`[aria-label="Run Aura batch"]`); err != nil {
		t.Fatal(err)
	}
	// Waiting for the final result also ensures the HAR response has completed.
	if _, err := page.WaitForFunction(`document.querySelector('[data-testid="business-result"]').textContent === 'Saved synthetic account alice'`, nil); err != nil {
		t.Fatal(err)
	}
	session, err := recorder.Stop()
	if err != nil {
		t.Fatal(err)
	}
	if len(session.HAR) == 0 {
		t.Fatal("completed recording omitted sidecar HAR")
	}

	// Inspect the actual captured network bytes and original entry position. Do
	// not manufacture a parallel HAR or assume static resources were retained.
	var capture struct {
		Log struct {
			Entries []struct {
				Request  struct{ Method, URL string }
				Response struct{ Status int }
			}
		}
	}
	if err := json.Unmarshal(session.HAR, &capture); err != nil {
		t.Fatal("invalid sidecar HAR")
	}
	auraEntryID := 0
	localOrigin, _ := url.Parse(server.URL)
	assets := map[string]bool{}
	for i, entry := range capture.Log.Entries {
		u, err := url.Parse(entry.Request.URL)
		if err != nil || u.Host != localOrigin.Host {
			t.Fatal("fixture recording made a non-local network request")
		}
		if u.Path == "/react.js" || u.Path == "/react-dom.js" {
			assets[u.Path] = entry.Response.Status == 200
		}
		if err == nil && u.Path == "/aura" && entry.Request.Method == "POST" {
			if auraEntryID != 0 || entry.Response.Status != 200 {
				t.Fatal("unexpected captured Aura batch count/status")
			}
			auraEntryID = i + 1
		}
	}
	if !assets["/react.js"] || !assets["/react-dom.js"] {
		t.Fatal("actual React assets absent from completed HAR")
	}
	if auraEntryID == 0 {
		t.Fatal("Aura request absent from completed HAR")
	}
	imported, err := harconvert.Convert(session.HAR, harconvert.ConvertOptions{ScriptName: "Local React Aura recording"})
	if err != nil {
		t.Fatal(err)
	}
	review, err := salesforce.Analyze(imported.Script, salesforce.AnalyzeOptions{BuildNative: true, HARData: session.HAR})
	if err != nil {
		t.Fatal(err)
	}
	outcomes := review.AuraOutcomes
	if outcomes == nil || !outcomes.Complete || outcomes.TotalBatches != 1 || outcomes.RequestActions != 2 || outcomes.MatchedActions != 2 || outcomes.StateCounts[aura.Success] != 2 {
		t.Fatal("captured Aura outcomes incomplete")
	}
	if len(outcomes.Batches) != 1 || outcomes.Batches[0].HAREntryID != auraEntryID || outcomes.Batches[0].ActionPath == "" || !outcomes.Batches[0].Analysis.Success {
		t.Fatal("captured source identity or outcomes lost")
	}

	converted := Convert(session, ConvertOptions{})
	stableClick := false
	for _, a := range converted.Script.Actions {
		if a.Type == "browser_click" && strings.Contains(a.Selector, "aria-label") {
			stableClick = true
		}
	}
	if !stableClick {
		t.Fatal("generated DOM identity outranked accessible save control")
	}
	execute := func(t *testing.T, b *browserprotocol.Browser, a *protocol.Action) *protocol.Result {
		t.Helper()
		result, err := b.Execute(context.Background(), a)
		if err != nil || result == nil || !result.Success {
			t.Fatalf("local browser action %s failed", a.Type)
		}
		return result
	}
	runActions := func(t *testing.T, b *browserprotocol.Browser, expectedState string) {
		t.Helper()
		for _, a := range converted.Script.Actions {
			encoded, err := json.Marshal(a)
			if err != nil {
				t.Fatal(err)
			}
			var action protocol.Action
			if err := json.Unmarshal(encoded, &action); err != nil {
				t.Fatal(err)
			}
			execute(t, b, &action)
			if action.Type == "browser_navigate" {
				execute(t, b, &protocol.Action{Type: "browser_wait", WaitFor: &protocol.WaitCondition{Type: "text", Value: "Client state: " + expectedState, TimeoutMs: 5000}, Assertions: []protocol.Assertion{{Type: "element_text_equals", Value: `[data-testid="client-state"]`, Expected: "Client state: " + expectedState}}})
				identity := execute(t, b, &protocol.Action{Type: "browser_evaluate_js", Target: domIDExpression}).Extractions["result"]
				if identity == "" || domIDs[identity] {
					t.Fatal("fixture DOM identity did not change on a new mount")
				}
				domIDs[identity] = true
			}
		}
	}
	assertResult := func(t *testing.T, b *browserprotocol.Browser, expected string) {
		t.Helper()
		execute(t, b, &protocol.Action{Type: "browser_wait", WaitFor: &protocol.WaitCondition{Type: "text", Value: expected, TimeoutMs: 5000}, Assertions: []protocol.Assertion{{Type: "element_text_equals", Value: `[data-testid="business-result"]`, Expected: expected}}})
	}
	newBrowser := func(t *testing.T) *browserprotocol.Browser {
		t.Helper()
		b := &browserprotocol.Browser{}
		if err := b.Init(context.Background(), map[string]any{"base_url": server.URL}); err != nil {
			t.Fatal(err)
		}
		return b
	}
	for run := 0; run < 2; run++ {
		b := newBrowser(t)
		func() {
			defer b.Teardown(context.Background())
			runActions(t, b, "cold")
			assertResult(t, b, "Saved synthetic account alice")
			// Repeating navigation in this same context preserves the cookie session
			// while the bootstrap allocates fresh IDs and reports warm client state.
			runActions(t, b, "warm")
			assertResult(t, b, "Saved synthetic account alice")
		}()
	}
	stats := fixture.Stats()
	if stats.AcceptedBatches != 5 || stats.DistinctSessions != 3 || stats.DistinctActionIDs != 10 || stats.ColdBootstraps != 3 || stats.WarmBootstraps != 2 || stats.RejectedBatches != 0 || len(domIDs) != 5 || stats.DistinctTokens != 3 {
		t.Fatalf("unexpected local replay counts: %+v", stats)
	}

	for _, scenario := range []aurafixture.Scenario{aurafixture.Error, aurafixture.Incomplete, aurafixture.Missing, aurafixture.Duplicate} {
		t.Run(string(scenario), func(t *testing.T) {
			if err := fixture.SetScenario(scenario); err != nil {
				t.Fatal(err)
			}
			b := newBrowser(t)
			defer b.Teardown(context.Background())
			runActions(t, b, "cold")
			assertResult(t, b, "Synthetic Aura batch failed")
			// Successful controls and fulfilled HTTP 200 are not the business proof.
			result, err := b.Execute(context.Background(), &protocol.Action{Type: "browser_wait", WaitFor: &protocol.WaitCondition{Type: "text", Value: "Synthetic Aura batch failed", TimeoutMs: 5000}, Assertions: []protocol.Assertion{{Type: "element_text_equals", Value: `[data-testid="business-result"]`, Expected: "Saved synthetic account alice"}}})
			if err != nil || result == nil || result.Success {
				t.Fatal("successful control replay concealed failed business outcome")
			}
		})
	}
	stats = fixture.Stats()
	if stats.AcceptedBatches != 9 || stats.RejectedBatches != 0 || stats.ErrorActions != 1 || stats.IncompleteActions != 1 || stats.MissingActions != 1 || stats.DuplicateActions != 1 || stats.TransportFailures != 0 || len(domIDs) != 9 {
		t.Fatalf("failure scenarios did not complete as application outcomes: %+v; DOM mounts=%d", stats, len(domIDs))
	}

}
