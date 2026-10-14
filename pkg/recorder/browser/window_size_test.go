package browser

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// A desktop recording's page follows its window; a device preset keeps its
// emulated viewport and a headless capture keeps Playwright's fixed one.
func TestApplyRecordingPageSize(t *testing.T) {
	var desktop playwright.BrowserNewContextOptions
	applyRecordingPageSize(&desktop, Options{})
	if desktop.NoViewport == nil || !*desktop.NoViewport || desktop.Viewport != nil {
		t.Fatalf("headed desktop = %+v, want the page to follow the window", desktop)
	}

	var headless playwright.BrowserNewContextOptions
	applyRecordingPageSize(&headless, Options{Headless: true})
	if headless.NoViewport != nil || headless.Viewport != nil {
		t.Fatalf("headless = %+v, want Playwright's fixed default", headless)
	}

	var device playwright.BrowserNewContextOptions
	applyRecordingPageSize(&device, Options{Device: &DeviceEmulation{Name: "phone", Width: 393, Height: 852, DeviceScaleFactor: 3, Mobile: true, HasTouch: true}})
	if device.NoViewport != nil || device.Viewport == nil || device.Viewport.Width != 393 || device.Viewport.Height != 852 ||
		device.IsMobile == nil || !*device.IsMobile || device.HasTouch == nil || !*device.HasTouch {
		t.Fatalf("device = %+v, want the emulated viewport", device)
	}
}

func viewportClick(ts int64, width, height int) string {
	return fmt.Sprintf(`{"kind":"click","ts":%d,"url":"https://shop.example/","viewport_width":%d,"viewport_height":%d}`, ts, width, height)
}

func mustStep(t *testing.T, payload string) Step {
	t.Helper()
	steps, err := stepsFromEvent(payload)
	if err != nil {
		t.Fatal(err)
	}
	return steps[0]
}

// A step keeps the page size its capture event reported; a frame's event (no
// size) and an impossible size keep none.
func TestStepKeepsItsRecordedPageSize(t *testing.T) {
	for _, test := range []struct {
		payload       string
		width, height int
	}{
		{viewportClick(1, 1400, 900), 1400, 900},
		{`{"kind":"click","ts":2,"url":"https://shop.example/frame"}`, 0, 0},
		{viewportClick(3, 9000, 900), 0, 0},
		{viewportClick(4, 0, 900), 0, 0},
	} {
		step := mustStep(t, test.payload)
		if step.ViewportWidth != test.width || step.ViewportHeight != test.height {
			t.Fatalf("%s: step size = %dx%d, want %dx%d", test.payload, step.ViewportWidth, step.ViewportHeight, test.width, test.height)
		}
	}
}

// Only the steps a conversion keeps choose its replay size.
func TestRecordedViewportsCountOnlyConvertedSteps(t *testing.T) {
	step := func(seq, width int, skip string) Step {
		return Step{Seq: seq, Kind: StepClick, ViewportWidth: width, ViewportHeight: 900, Excluded: skip == "excluded", Deleted: skip == "deleted"}
	}
	session := &Session{Steps: []Step{
		step(1, 800, "deleted"), step(2, 800, "deleted"), step(3, 800, "excluded"),
		step(4, 1400, ""), step(5, 1400, ""), step(6, 800, ""), step(7, 0, ""),
	}}
	viewports := recordedViewports(session, StepRange{})
	if fmt.Sprint(viewports) != fmt.Sprint([]recordedViewport{{1400, 900, 2}, {800, 900, 1}}) {
		t.Fatalf("viewports = %+v, want only the kept steps counted", viewports)
	}
	if replay, ok := replayViewport(viewports); !ok || replay.Width != 1400 {
		t.Fatalf("replay = %+v %t, want 1400 wide", replay, ok)
	}
	inRange := recordedViewports(session, StepRange{StartSeq: 6})
	if replay, _ := replayViewport(inRange); replay.Width != 800 {
		t.Fatalf("range replay = %+v, want the steps in range to choose", replay)
	}
	tied := recordedViewports(&Session{Steps: []Step{step(1, 1000, ""), step(2, 1200, "")}}, StepRange{})
	if replay, _ := replayViewport(tied); replay.Width != 1000 {
		t.Fatalf("tied replay = %+v, want the first seen", replay)
	}
	if _, ok := replayViewport(nil); ok {
		t.Fatal("no sizes reported a replay viewport")
	}
}

func TestConvertReplaysAtTheRecordedViewport(t *testing.T) {
	base := time.Now()
	session := func(sizes ...[3]int) *Session {
		recorded := &Session{StartURL: "https://shop.example/", StartedAt: base}
		seq := 0
		for _, size := range sizes {
			for range size[2] {
				seq++
				recorded.Steps = append(recorded.Steps, Step{Seq: seq, Kind: StepClick, Timestamp: base.Add(time.Duration(seq) * time.Second), URL: "https://shop.example/", ViewportWidth: size[0], ViewportHeight: size[1]})
			}
		}
		return recorded
	}
	resized := func(result *ConvertResult) string {
		for _, warning := range result.Warnings {
			if strings.Contains(warning, "resized while recording") {
				return warning
			}
		}
		return ""
	}

	result := Convert(session([3]int{1400, 900, 3}, [3]int{1380, 860, 1}), ConvertOptions{ScriptName: "Wide"})
	if viewport := result.Script.Config.Browser.Viewport; viewport.Width != 1400 || viewport.Height != 900 {
		t.Fatalf("replay viewport = %+v, want the recorded 1400x900", viewport)
	}
	if warning := resized(result); warning != "" {
		t.Fatalf("a resize within tolerance warned: %s", warning)
	}

	result = Convert(session([3]int{1400, 900, 3}, [3]int{800, 900, 2}), ConvertOptions{ScriptName: "Resized"})
	if warning := resized(result); !strings.Contains(warning, "2 step(s) were recorded at 800x900") || !strings.Contains(warning, "replays at 1400x900") {
		t.Fatalf("resize warning = %q", warning)
	}

	// A device recording replays at its preset's viewport.
	device := session([3]int{393, 852, 1})
	device.DeviceProfile = "iphone_15"
	result = Convert(device, ConvertOptions{ScriptName: "Phone"})
	if viewport := result.Script.Config.Browser.Viewport; viewport.Width != 0 || viewport.Height != 0 || resized(result) != "" {
		t.Fatalf("device replay viewport = %+v, want the preset's", viewport)
	}

	// A recording without sizes keeps the replay default.
	result = Convert(session([3]int{0, 0, 1}), ConvertOptions{ScriptName: "Unknown"})
	if viewport := result.Script.Config.Browser.Viewport; viewport.Width != 0 || viewport.Height != 0 {
		t.Fatalf("replay viewport = %+v, want unset", viewport)
	}
}

// The recorded page follows its window: resizing the window reflows the page
// instead of leaving a fixed page in blank space, and the steps recorded at
// each size choose the script's replay viewport.
func TestRecorderPageFollowsWindowSize(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser integration test in -short mode")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Responsive</title>
<style>#wide{display:none}#narrow{display:block}@media (min-width:1000px){#wide{display:block}#narrow{display:none}}</style>
<button id="wide">Wide layout</button><button id="narrow">Narrow layout</button><button id="more">More</button>
<iframe srcdoc="<button id='inner'>Inside a frame</button>" width="300" height="80"></iframe>`))
	}))
	defer server.Close()

	recorder := New(Options{StartURL: server.URL, Headless: true, followWindowHeadless: true})
	if err := recorder.Start(); err != nil {
		t.Skipf("browser runtime unavailable: %v", err)
	}
	defer func() { _, _ = recorder.Stop() }()
	page := recorder.context.Pages()[0]
	cdp, err := recorder.context.NewCDPSession(page)
	if err != nil {
		t.Fatalf("CDP session: %v", err)
	}
	window, err := cdp.Send("Browser.getWindowForTarget", nil)
	if err != nil {
		t.Fatalf("window for target: %v", err)
	}
	windowID := window.(map[string]any)["windowId"]
	resize := func(width, height int) int {
		t.Helper()
		if _, err := cdp.Send("Browser.setWindowBounds", map[string]any{"windowId": windowID, "bounds": map[string]any{"width": width, "height": height, "windowState": "normal"}}); err != nil {
			t.Fatalf("resize window: %v", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			inner, err := page.Evaluate(`window.innerWidth`)
			if err != nil {
				t.Fatal(err)
			}
			got := int(math.Round(toFloat(inner)))
			if math.Abs(float64(got-width)) <= 40 || time.Now().After(deadline) {
				return got
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	wide := resize(1400, 900)
	if wide < 1000 {
		t.Fatalf("page width after widening the window = %d, want the page to follow it", wide)
	}
	for _, selector := range []string{"#wide", "#more"} {
		if err := page.Click(selector); err != nil {
			t.Fatalf("click %s: %v", selector, err)
		}
	}
	waitForSteps(t, recorder, 2, 10*time.Second)
	narrow := resize(800, 900)
	if narrow >= 1000 {
		t.Fatalf("page width after narrowing the window = %d, want the page to follow it", narrow)
	}
	if err := page.Click("#narrow"); err != nil {
		t.Fatalf("the narrow layout did not appear after the resize: %v", err)
	}
	waitForSteps(t, recorder, 3, 10*time.Second)
	// A frame's own size is not the page's, so its step reports none.
	if err := page.FrameLocator("iframe").Locator("#inner").Click(); err != nil {
		t.Fatalf("click inside the frame: %v", err)
	}
	waitForSteps(t, recorder, 4, 10*time.Second)

	session, err := recorder.Stop()
	if err != nil {
		t.Fatal(err)
	}
	var sizes []string
	for _, step := range session.Steps {
		sizes = append(sizes, fmt.Sprint(step.ViewportWidth))
	}
	if want := fmt.Sprintf("%d %d %d 0", wide, wide, narrow); strings.Join(sizes, " ") != want {
		t.Fatalf("step widths = %v, want %s (the frame's step without one)", sizes, want)
	}
	result := Convert(session, ConvertOptions{ScriptName: "Responsive"})
	if viewport := result.Script.Config.Browser.Viewport; viewport.Width != wide {
		t.Fatalf("replay viewport = %+v, want the %d-wide size most steps used", viewport, wide)
	}
	warned := false
	for _, warning := range result.Warnings {
		warned = warned || strings.Contains(warning, "resized while recording")
	}
	if !warned {
		t.Fatalf("warnings = %v, want the resize named", result.Warnings)
	}
}

func toFloat(value any) float64 {
	switch number := value.(type) {
	case int:
		return float64(number)
	case float64:
		return number
	}
	return 0
}
