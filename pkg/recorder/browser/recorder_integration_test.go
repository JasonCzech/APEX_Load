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

const keyboardFixtureHTML = `<!doctype html><html><body>
  <form onsubmit="event.preventDefault(); window.formSubmits = (window.formSubmits || 0) + 1">
    <input id="first" name="first" type="text">
    <input id="second" name="second" type="text">
    <button id="save" type="button" onclick="window.saveClicks = (window.saveClicks || 0) + 1">Save</button>
  </form>
</body></html>`

const popupMainHTML = `<!doctype html><html><body>
  <button id="main" type="button">Main action</button>
</body></html>`

const popupChildHTML = `<!doctype html><html><body>
  <button id="child" type="button">Popup action</button>
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

func keyboardFixtureServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(keyboardFixtureHTML))
	}))
}

func popupFixtureServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/popup" {
			_, _ = w.Write([]byte(popupChildHTML))
			return
		}
		_, _ = w.Write([]byte(popupMainHTML))
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

func TestRecorderCapturesSwitchBackToEarlierWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	server := popupFixtureServer()
	defer server.Close()

	recorder := New(Options{StartURL: server.URL, Headless: true})
	if err := recorder.Start(); err != nil {
		t.Skipf("browser runtime unavailable: %v", err)
	}
	defer func() { _, _ = recorder.Stop() }()

	pages := recorder.context.Pages()
	if len(pages) == 0 {
		t.Fatal("no recording page open")
	}
	main := pages[0]
	if _, err := main.Evaluate(`(url) => window.open(url, '_blank')`, server.URL+"/popup"); err != nil {
		t.Fatalf("open popup: %v", err)
	}
	waitForSteps(t, recorder, 1, 10*time.Second)
	waitForPages(t, recorder, 2, 10*time.Second)

	if err := main.BringToFront(); err != nil {
		t.Fatalf("bring main to front: %v", err)
	}
	if err := main.Click("#main"); err != nil {
		t.Fatalf("click main: %v", err)
	}
	waitForSteps(t, recorder, 3, 10*time.Second)

	session, err := recorder.Stop()
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(session.Steps) < 3 {
		t.Fatalf("steps = %+v, want popup switch, switch-back, click", session.Steps)
	}
	if session.Steps[0].Kind != StepSwitchWindow || !strings.HasSuffix(session.Steps[0].Value, "/popup") {
		t.Fatalf("first step = %+v, want switch to popup", session.Steps[0])
	}
	if session.Steps[1].Kind != StepSwitchWindow || session.Steps[1].Value != server.URL+"/" {
		t.Fatalf("second step = %+v, want switch back to main page", session.Steps[1])
	}
	if session.Steps[2].Kind != StepClick {
		t.Fatalf("third step = %+v, want click after switch-back", session.Steps[2])
	}
}

func TestRecorderCapturesTabFillBeforePressKeyWithoutBlurDuplicate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	server := keyboardFixtureServer()
	defer server.Close()

	recorder := New(Options{StartURL: server.URL, Headless: true})
	if err := recorder.Start(); err != nil {
		t.Skipf("browser runtime unavailable: %v", err)
	}
	defer func() { _, _ = recorder.Stop() }()

	pages := recorder.context.Pages()
	if len(pages) == 0 {
		t.Fatal("no recording page open")
	}
	page := pages[0]

	if err := page.Fill("#first", "alice"); err != nil {
		t.Fatalf("fill first: %v", err)
	}
	if err := page.Press("#first", "Tab"); err != nil {
		t.Fatalf("press tab: %v", err)
	}

	waitForSteps(t, recorder, 2, 10*time.Second)
	time.Sleep(350 * time.Millisecond)
	session, err := recorder.Stop()
	if err != nil {
		t.Fatalf("stop: %v", err)
	}

	if len(session.Steps) != 2 {
		t.Fatalf("steps = %d, want exactly fill + press_key: %+v", len(session.Steps), session.Steps)
	}
	if session.Steps[0].Kind != StepFill || session.Steps[0].Value != "alice" {
		t.Fatalf("first step = %+v, want fill(alice)", session.Steps[0])
	}
	if session.Steps[1].Kind != StepPressKey || session.Steps[1].Value != "Tab" {
		t.Fatalf("second step = %+v, want press_key(Tab)", session.Steps[1])
	}
}

func TestRecorderSuppressesSyntheticClickAfterEnterPress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	server := keyboardFixtureServer()
	defer server.Close()

	recorder := New(Options{StartURL: server.URL, Headless: true})
	if err := recorder.Start(); err != nil {
		t.Skipf("browser runtime unavailable: %v", err)
	}
	defer func() { _, _ = recorder.Stop() }()

	pages := recorder.context.Pages()
	if len(pages) == 0 {
		t.Fatal("no recording page open")
	}
	page := pages[0]

	if err := page.Press("#save", "Enter"); err != nil {
		t.Fatalf("press enter: %v", err)
	}
	waitForSteps(t, recorder, 1, 10*time.Second)
	time.Sleep(350 * time.Millisecond)

	clicks, err := page.Evaluate(`() => window.saveClicks || 0`)
	if err != nil {
		t.Fatalf("read save click count: %v", err)
	}
	if got := evalInt(t, clicks); got != 1 {
		t.Fatalf("save clicks = %d, want 1 keyboard activation", got)
	}

	session, err := recorder.Stop()
	if err != nil {
		t.Fatalf("stop: %v", err)
	}

	var pressKeys, clicksRecorded int
	for _, step := range session.Steps {
		switch step.Kind {
		case StepPressKey:
			pressKeys++
			if step.Value != "Enter" {
				t.Errorf("press_key value = %q, want Enter", step.Value)
			}
		case StepClick:
			clicksRecorded++
		}
	}
	if pressKeys != 1 || clicksRecorded != 0 {
		t.Fatalf("recorded press_key=%d click=%d, want press_key=1 click=0: %+v", pressKeys, clicksRecorded, session.Steps)
	}
}

func TestRecorderCapturesDebouncedHover(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	server := keyboardFixtureServer()
	defer server.Close()

	recorder := New(Options{StartURL: server.URL, Headless: true})
	if err := recorder.Start(); err != nil {
		t.Skipf("browser runtime unavailable: %v", err)
	}
	defer func() { _, _ = recorder.Stop() }()

	pages := recorder.context.Pages()
	if len(pages) == 0 {
		t.Fatal("no recording page open")
	}
	page := pages[0]

	if err := page.Hover("#save"); err != nil {
		t.Fatalf("hover save: %v", err)
	}
	waitForSteps(t, recorder, 1, 10*time.Second)
	// Moving over empty page space must not capture a whole-page HTML hover.
	if err := page.Mouse().Move(900, 650); err != nil {
		t.Fatal(err)
	}
	time.Sleep(650 * time.Millisecond)

	session, err := recorder.Stop()
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(session.Steps) != 1 || session.Steps[0].Kind != StepHover {
		t.Fatalf("steps = %+v, want one hover step", session.Steps)
	}
	if got := session.Steps[0].bestSelector(); got != "#save" {
		t.Fatalf("hover selector = %q, want #save", got)
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

func waitForPages(t *testing.T, recorder *Recorder, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := len(recorder.context.Pages()); got >= want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d pages, have %d", want, len(recorder.context.Pages()))
}

func evalInt(t *testing.T, raw any) int {
	t.Helper()
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	default:
		t.Fatalf("unexpected numeric result %T (%v)", raw, raw)
		return 0
	}
}

func TestRecorderStopFlushesLastDebouncedClick(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration is excluded by -short")
	}
	server := fixtureServer()
	defer server.Close()
	recorder := New(Options{StartURL: server.URL, Headless: true})
	if err := recorder.Start(); err != nil {
		t.Skipf("browser runtime unavailable: %v", err)
	}
	defer recorder.ForceStop()
	page := recorder.context.Pages()[0]
	if err := page.Click(`[data-testid="go-btn"]`); err != nil {
		t.Fatal(err)
	}
	// Stop immediately, before the capture script's 250ms click timer fires.
	session, err := recorder.Stop()
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, step := range session.Steps {
		if step.Kind == StepClick {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("final click count = %d, want 1; steps=%+v", found, session.Steps)
	}
}

func TestControlPanelTrafficIsExcludedFromHAR(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration is excluded by -short")
	}
	target := fixtureServer()
	defer target.Close()
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<title>Recorder panel</title><script>fetch('/api/panel-only')</script>`))
	}))
	defer panel.Close()
	recorder := New(Options{StartURL: target.URL, Headless: true, CaptureHAR: true})
	if err := recorder.Start(); err != nil {
		t.Skipf("browser runtime unavailable: %v", err)
	}
	defer recorder.ForceStop()
	if err := recorder.OpenControlPanel(panel.URL+"/#/recorder-panel?session=test", nil); err != nil {
		t.Fatal(err)
	}
	if len(recorder.context.Pages()) != 1 {
		t.Fatal("control page shares capture context")
	}
	session, err := recorder.Stop()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(session.HAR), target.URL) {
		t.Fatal("application traffic missing")
	}
	if strings.Contains(string(session.HAR), panel.URL) {
		t.Fatal("control traffic leaked into replay HAR")
	}
}

func TestControlTransitionsFlushThePrecedingClick(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration is excluded by -short")
	}
	for _, control := range []string{"section", "pause", "transaction"} {
		t.Run(control, func(t *testing.T) {
			server := fixtureServer()
			defer server.Close()
			recorder := New(Options{StartURL: server.URL, Headless: true})
			if err := recorder.Start(); err != nil {
				t.Skipf("browser runtime unavailable: %v", err)
			}
			defer recorder.ForceStop()
			recorder.SetSection(SectionTeardown)
			if err := recorder.context.Pages()[0].Click(`[data-testid="go-btn"]`); err != nil {
				t.Fatal(err)
			}
			switch control {
			case "section":
				recorder.SetSection(SectionActions)
			case "pause":
				recorder.Pause()
			case "transaction":
				if _, err := recorder.AddMarker(MarkerTransactionStart, "after click", "", 0); err != nil {
					t.Fatal(err)
				}
			}
			session := recorder.Snapshot()
			var click *Step
			for i := range session.Steps {
				if session.Steps[i].Kind == StepClick {
					click = &session.Steps[i]
				}
			}
			if click == nil || click.Section != SectionTeardown {
				t.Fatalf("preceding click lost or reclassified: %+v", session.Steps)
			}
			if control == "transaction" && !session.Markers[0].Timestamp.After(click.Timestamp) {
				t.Fatal("transaction boundary precedes its flushed input")
			}
		})
	}
}
