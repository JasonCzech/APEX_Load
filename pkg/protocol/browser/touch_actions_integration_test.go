package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserProtocolTouchActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Touch Test</title>
    <script>
      function renderCounts() {
        const touches = document.querySelector('#touch-count').textContent;
        const clicks = document.querySelector('#click-count').textContent;
        document.querySelector('#status').textContent = 'touches=' + touches + ' clicks=' + clicks;
      }
      window.addEventListener('load', () => {
        const button = document.querySelector('#touch-target');
        button.addEventListener('touchend', (event) => {
          event.preventDefault();
          const counter = document.querySelector('#touch-count');
          counter.textContent = String(Number(counter.textContent) + 1);
          renderCounts();
        });
        button.addEventListener('click', () => {
          const counter = document.querySelector('#click-count');
          counter.textContent = String(Number(counter.textContent) + 1);
          renderCounts();
        });
        renderCounts();
      });
    </script>
  </head>
  <body>
    <button id="touch-target">Touch me</button>
    <div id="touch-count">0</div>
    <div id="click-count">0</div>
    <div id="status">pending</div>
  </body>
</html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":               server.URL,
		"browser_headless":       true,
		"browser_device_profile": "pixel_8",
		"browser_touch_mode":     true,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	navigate, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Open touch page",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !navigate.Success {
		t.Fatalf("navigate failed: %s", navigate.ErrorMessage)
	}

	tap, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Tap target",
		Type:     "browser_tap",
		Selector: "#touch-target",
	})
	if err != nil {
		t.Fatalf("tap returned error: %v", err)
	}
	if !tap.Success {
		t.Fatalf("tap failed: %s", tap.ErrorMessage)
	}

	click, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Click target in touch mode",
		Type:     "browser_click",
		Selector: "#touch-target",
	})
	if err != nil {
		t.Fatalf("click returned error: %v", err)
	}
	if !click.Success {
		t.Fatalf("click failed: %s", click.ErrorMessage)
	}

	capture, err := b.Execute(context.Background(), &protocol.Action{
		Name: "Capture touch state",
		Type: "browser_screenshot",
		Capture: &protocol.BrowserCaptureOptions{
			HTML: true,
		},
	})
	if err != nil {
		t.Fatalf("capture returned error: %v", err)
	}
	if !capture.Success {
		t.Fatalf("capture failed: %s", capture.ErrorMessage)
	}
	if !strings.Contains(capture.HTML, `<div id="touch-count">2</div>`) {
		t.Fatalf("capture HTML missing touch count 2: %s", capture.HTML)
	}
}

func TestBrowserProtocolAdvancedTouchActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head>
    <title>Advanced Touch Test</title>
    <script>
      window.addEventListener('load', () => {
        let longPressTimer = null;
        const holdTarget = document.querySelector('#hold-target');
        holdTarget.addEventListener('touchstart', () => {
          longPressTimer = setTimeout(() => {
            const counter = document.querySelector('#long-press-count');
            counter.textContent = String(Number(counter.textContent) + 1);
          }, 200);
        });
        holdTarget.addEventListener('touchend', () => {
          if (longPressTimer) {
            clearTimeout(longPressTimer);
            longPressTimer = null;
          }
        });

        let swipeStartX = 0;
        const swipeTarget = document.querySelector('#swipe-target');
        swipeTarget.addEventListener('touchstart', (event) => {
          swipeStartX = event.changedTouches[0].clientX;
        });
        swipeTarget.addEventListener('touchend', (event) => {
          const delta = event.changedTouches[0].clientX - swipeStartX;
          if (delta < -80) {
            const counter = document.querySelector('#swipe-count');
            counter.textContent = String(Number(counter.textContent) + 1);
          }
        });
      });
    </script>
  </head>
  <body>
    <button id="hold-target">Hold me</button>
    <div id="long-press-count">0</div>
    <div id="swipe-target" style="width: 320px; height: 80px; border: 1px solid #000;">Swipe here</div>
    <div id="swipe-count">0</div>
  </body>
</html>`))
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":               server.URL,
		"browser_headless":       true,
		"browser_device_profile": "pixel_8",
		"browser_touch_mode":     true,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	navigate, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Open gesture page",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !navigate.Success {
		t.Fatalf("navigate failed: %s", navigate.ErrorMessage)
	}

	longPress, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Long press target",
		Type:     "browser_long_press",
		Selector: "#hold-target",
		Params: map[string]any{
			"duration_ms": 350,
		},
	})
	if err != nil {
		t.Fatalf("long press returned error: %v", err)
	}
	if !longPress.Success {
		t.Fatalf("long press failed: %s", longPress.ErrorMessage)
	}

	swipe, err := b.Execute(context.Background(), &protocol.Action{
		Name:     "Swipe left",
		Type:     "browser_swipe",
		Selector: "#swipe-target",
		Params: map[string]any{
			"direction": "left",
			"distance":  180,
		},
	})
	if err != nil {
		t.Fatalf("swipe returned error: %v", err)
	}
	if !swipe.Success {
		t.Fatalf("swipe failed: %s", swipe.ErrorMessage)
	}

	capture, err := b.Execute(context.Background(), &protocol.Action{
		Name: "Capture gesture state",
		Type: "browser_screenshot",
		Capture: &protocol.BrowserCaptureOptions{
			HTML: true,
		},
	})
	if err != nil {
		t.Fatalf("capture returned error: %v", err)
	}
	if !capture.Success {
		t.Fatalf("capture failed: %s", capture.ErrorMessage)
	}
	if !strings.Contains(capture.HTML, `<div id="long-press-count">1</div>`) {
		t.Fatalf("capture HTML missing long press count 1: %s", capture.HTML)
	}
	if !strings.Contains(capture.HTML, `<div id="swipe-count">1</div>`) {
		t.Fatalf("capture HTML missing swipe count 1: %s", capture.HTML)
	}
}
