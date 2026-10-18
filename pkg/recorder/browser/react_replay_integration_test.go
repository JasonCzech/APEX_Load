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

func TestRecordAndReplayReactSPAWithPollingAndDynamicAuraSelectors(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration")
	}
	read := func(path string) []byte {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			if os.Getenv("APEXLOAD_BROWSER_TESTS") == "require" {
				t.Fatal(err)
			}
			t.Skipf("React fixture dependency missing; install dashboard dependencies: %v", err)
		}
		return data
	}
	react := read("../../../dashboard/node_modules/react/umd/react.production.min.js")
	reactDOM := read("../../../dashboard/node_modules/react-dom/umd/react-dom.production.min.js")
	html := read("testdata/react-spa.html")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/react.js":
			w.Header().Set("Content-Type", "application/javascript")
			w.Write(react)
		case "/react-dom.js":
			w.Header().Set("Content-Type", "application/javascript")
			w.Write(reactDOM)
		case "/suggest":
			time.Sleep(150 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"name":%q}`, r.URL.Query().Get("q"))
		case "/poll":
			time.Sleep(150 * time.Millisecond)
			fmt.Fprint(w, "ok")
		case "/save":
			time.Sleep(200 * time.Millisecond)
			fmt.Fprint(w, "ok")
		default:
			w.Header().Set("Content-Type", "text/html")
			w.Write(html)
		}
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
	page := recorder.context.Pages()[0]
	for index, value := range []string{"al", "alice"} {
		if err := page.Locator(`[data-testid="search"]`).Fill(value); err != nil {
			t.Fatal(err)
		}
		// No blur: each settled search term must survive capture.
		waitForSteps(t, recorder, index+1, 5*time.Second)
	}
	if _, err := page.WaitForFunction(`document.querySelector('#suggestion').textContent === 'alice'`, nil); err != nil {
		t.Fatal(err)
	}
	recorder.SetAssertMode(true)
	if err := page.Locator("#suggestion").Click(); err != nil {
		t.Fatal(err)
	}
	waitForSteps(t, recorder, 3, 5*time.Second)
	recorder.SetAssertMode(false)
	if err := page.Locator(`[data-testid="search"]`).Press("ArrowDown"); err != nil {
		t.Fatal(err)
	}
	if err := page.Locator(`[aria-label="Save record"]`).Click(); err != nil {
		t.Fatal(err)
	}
	if _, err := page.WaitForFunction(`document.querySelector('#result').textContent === 'Saved alice'`, nil); err != nil {
		t.Fatal(err)
	}
	session, err := recorder.Stop()
	if err != nil {
		t.Fatal(err)
	}
	fills := []string{}
	arrow := false
	for _, step := range session.Steps {
		if step.Kind == StepFill {
			fills = append(fills, step.Value)
		}
		if step.Kind == StepPressKey && step.Value == "ArrowDown" {
			arrow = true
		}
	}
	if len(fills) < 2 || fills[0] != "al" || fills[1] != "alice" || !arrow {
		t.Fatalf("input capture: fills=%v arrow=%v", fills, arrow)
	}
	converted := Convert(session, ConvertOptions{})
	for _, a := range converted.Script.Actions {
		if a.Sync != nil && a.Sync.Type == "network_idle" {
			t.Fatal("polling would block inferred network_idle")
		}
	}
	// Fresh contexts and new generated IDs on every replay. Assert the business
	// result independently; successful clicks alone do not prove success.
	for run := 0; run < 2; run++ {
		b := &browserprotocol.Browser{}
		if err := b.Init(context.Background(), map[string]any{"base_url": server.URL}); err != nil {
			t.Fatal(err)
		}
		func() {
			defer b.Teardown(context.Background())
			for _, a := range converted.Script.Actions {
				encoded, err := json.Marshal(a)
				if err != nil {
					t.Fatal(err)
				}
				action := &protocol.Action{}
				if err := json.Unmarshal(encoded, action); err != nil {
					t.Fatal(err)
				}

				result, err := b.Execute(context.Background(), action)
				if err != nil || !result.Success {
					t.Fatalf("replay %d action %s: %v %s", run, a.Name, err, result.ErrorMessage)
				}
			}
			result, err := b.Execute(context.Background(), &protocol.Action{Type: "browser_wait", WaitFor: &protocol.WaitCondition{Type: "text", Value: "Saved alice", TimeoutMs: 5000}})
			if err != nil || !result.Success {
				t.Fatalf("business result: %v %s", err, result.ErrorMessage)
			}
		}()
	}
	// The source deliberately has no stable HTML id for its Aura-like button.
	foundStable := false
	for _, a := range converted.Script.Actions {
		if strings.Contains(a.Selector, "aria-label") {
			foundStable = true
		}
	}
	if !foundStable {
		t.Fatal("dynamic Aura id outranked the accessible label")
	}
}
