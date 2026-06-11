package browser

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fixtureHTML exercises the modern-web surfaces the recorder must capture with
// fidelity: a regular form, an open-shadow-root web component, a same-origin
// iframe, and an SPA-style pushState route change with a fetch burst.
const fixtureHTML = `<!doctype html>
<html><head><title>Recorder Fixture</title></head><body>
  <form onsubmit="return false">
    <input id="username" name="username" type="text" placeholder="Username">
    <button data-testid="go-btn" type="button">Go</button>
  </form>
  <div id="shadow-host"></div>
  <a id="spa-link" href="/inbox">Inbox</a>
  <iframe id="payframe" name="payframe" src="/frame"></iframe>
  <script>
    const host = document.getElementById('shadow-host');
    const root = host.attachShadow({mode: 'open'});
    root.innerHTML = '<button id="sbtn" aria-label="Shadow action">Shadow</button>';
    document.getElementById('spa-link').addEventListener('click', (e) => {
      e.preventDefault();
      history.pushState({}, '', '/inbox');
      fetch('/api/items');
      fetch('/api/badge');
    });
  </script>
</body></html>`

const frameHTML = `<!doctype html><html><body>
  <button id="paybtn" name="pay-now">Pay</button>
</body></html>`

func fixtureServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/frame":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(frameHTML))
		case "/api/items", "/api/badge":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(fixtureHTML))
		}
	}))
}

func TestRecorderCapturesModernWebInteractions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	server := fixtureServer()
	defer server.Close()

	recorder := New(Options{StartURL: server.URL, Headless: true, CaptureHAR: true})
	if err := recorder.Start(); err != nil {
		t.Skipf("browser runtime unavailable: %v", err)
	}
	defer func() { _, _ = recorder.Stop() }()

	pages := recorder.context.Pages()
	if len(pages) == 0 {
		t.Fatal("no recording page open")
	}
	page := pages[0]

	if err := page.Fill("#username", "jason"); err != nil {
		t.Fatalf("fill: %v", err)
	}
	if err := page.Click(`[data-testid="go-btn"]`); err != nil {
		t.Fatalf("click go: %v", err)
	}
	if err := page.Click("#sbtn"); err != nil { // pierces the open shadow root
		t.Fatalf("click shadow button: %v", err)
	}
	if err := page.FrameLocator("#payframe").Locator("#paybtn").Click(); err != nil {
		t.Fatalf("click iframe button: %v", err)
	}
	if err := page.Click("#spa-link"); err != nil {
		t.Fatalf("click spa link: %v", err)
	}

	waitForSteps(t, recorder, 5, 10*time.Second)
	session, err := recorder.Stop()
	if err != nil {
		t.Fatalf("stop: %v", err)
	}

	byKind := map[string][]Step{}
	for _, step := range session.Steps {
		byKind[step.Kind] = append(byKind[step.Kind], step)
	}
	if len(byKind[StepFill]) != 1 || byKind[StepFill][0].Value != "jason" {
		t.Errorf("fill steps = %+v", byKind[StepFill])
	}
	if len(byKind[StepClick]) < 4 {
		t.Fatalf("click steps = %d, want >= 4: %+v", len(byKind[StepClick]), session.Steps)
	}

	var shadowClick, frameClick, goClick *Step
	for i := range byKind[StepClick] {
		click := &byKind[StepClick][i]
		if click.Element == nil {
			continue
		}
		switch {
		case click.Element.ID == "sbtn":
			shadowClick = click
		case click.Element.ID == "paybtn":
			frameClick = click
		case click.Element.DataTestID == "go-btn":
			goClick = click
		}
	}
	if goClick == nil {
		t.Fatalf("go button click not captured: %+v", byKind[StepClick])
	}
	if got := goClick.bestSelector(); got != `[data-testid="go-btn"]` {
		t.Errorf("go click best selector = %q", got)
	}
	if shadowClick == nil {
		t.Fatalf("shadow-root click not captured: %+v", byKind[StepClick])
	}
	if !shadowClick.Element.InShadowDOM {
		t.Errorf("shadow click not flagged InShadowDOM: %+v", shadowClick.Element)
	}
	if shadowClick.Element.XPath != "" {
		t.Errorf("shadow click should suppress xpath, got %q", shadowClick.Element.XPath)
	}
	if !strings.Contains(shadowClick.Element.CSSPath, "shadow-host") {
		t.Errorf("shadow css path should pierce through host: %q", shadowClick.Element.CSSPath)
	}
	if frameClick == nil {
		t.Fatalf("iframe click not captured: %+v", byKind[StepClick])
	}
	if !strings.Contains(frameClick.FrameSelector, "payframe") {
		t.Errorf("iframe click frame selector = %q", frameClick.FrameSelector)
	}

	// The SPA pushState must surface as a navigation event for sync inference.
	spaNavSeen := false
	for _, nav := range session.NavEvents {
		if strings.HasSuffix(nav.URL, "/inbox") {
			spaNavSeen = true
		}
	}
	if !spaNavSeen {
		t.Errorf("SPA pushState navigation not observed: %+v", session.NavEvents)
	}
	if len(session.NetworkEvents) == 0 {
		t.Error("no network events recorded")
	}

	if len(session.HAR) == 0 {
		t.Fatal("side-car HAR empty")
	}
	var har map[string]any
	if err := json.Unmarshal(session.HAR, &har); err != nil {
		t.Fatalf("side-car HAR is not valid JSON: %v", err)
	}

	// End-to-end: conversion must produce a replayable browser script.
	result := Convert(session, ConvertOptions{ScriptName: "Fixture Flow"})
	if result.Script.Protocol != "browser" {
		t.Errorf("converted protocol = %q", result.Script.Protocol)
	}
	if len(result.Script.Actions) < 5 {
		t.Errorf("converted actions = %v", actionTypes(result.Script.Actions))
	}
	syncSeen := false
	for _, action := range result.Script.Actions {
		if action.Sync != nil {
			syncSeen = true
		}
	}
	if !syncSeen {
		t.Error("no sync policy inferred for SPA fetch burst")
	}
}

func waitForSteps(t *testing.T, recorder *Recorder, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		recorder.mu.Lock()
		count := len(recorder.session.Steps)
		recorder.mu.Unlock()
		if count >= want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	recorder.mu.Lock()
	steps := recorder.session.Steps
	recorder.mu.Unlock()
	t.Fatalf("timed out waiting for %d steps, have %d: %+v", want, len(steps), steps)
}
