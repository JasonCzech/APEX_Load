package browser

import (
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	playwright "github.com/playwright-community/playwright-go"
)

//go:embed capture.js
var captureScript string

// Options configures one interactive recording run.
type Options struct {
	// StartURL is opened in the recording browser. Required.
	StartURL string
	// Headless launches without a window. Interactive recording wants a headed
	// browser; headless exists for automated tests of the capture pipeline.
	Headless bool
	// CaptureHAR records the context's network traffic to a side-car HAR
	// (with content) flushed when the recording stops. Default on via New.
	CaptureHAR bool
	// HARPath overrides where the side-car HAR is written. Empty uses a
	// temporary file that is read into Session.HAR and removed on Stop.
	HARPath string
}

// Recorder drives one headed Chromium session and accumulates a Session.
type Recorder struct {
	opts    Options
	pw      *playwright.Playwright
	browser playwright.Browser
	context playwright.BrowserContext

	mu      sync.Mutex
	session *Session
	stopped bool

	done     chan struct{}
	doneOnce sync.Once
	harTemp  bool
}

// New prepares a recorder; Start launches the browser.
func New(opts Options) *Recorder {
	return &Recorder{
		opts: opts,
		session: &Session{
			ID:        fmt.Sprintf("rec_%d", time.Now().UnixNano()),
			StartURL:  strings.TrimSpace(opts.StartURL),
			StartedAt: time.Now(),
		},
		done: make(chan struct{}),
	}
}

// Start launches Chromium, injects the capture script into every frame, wires
// the record binding plus navigation/network/WebSocket listeners, and opens the
// start URL. The user interacts with the page; Stop (or closing the browser)
// finishes the session.
func (r *Recorder) Start() error {
	startURL := r.session.StartURL
	if startURL == "" {
		return fmt.Errorf("browser recorder requires a start URL")
	}
	if parsed, err := url.Parse(startURL); err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("browser recorder start URL %q must be absolute", startURL)
	}

	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf("start playwright: %w", err)
	}
	r.pw = pw

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(r.opts.Headless),
		Timeout:  playwright.Float(30000),
	})
	if err != nil {
		r.cleanup()
		return fmt.Errorf("launch chromium: %w", err)
	}
	r.browser = browser
	browser.OnDisconnected(func(playwright.Browser) { r.signalDone() })

	contextOptions := playwright.BrowserNewContextOptions{}
	if r.opts.CaptureHAR {
		harPath := strings.TrimSpace(r.opts.HARPath)
		if harPath == "" {
			harPath = filepath.Join(os.TempDir(), r.session.ID+".har")
			r.harTemp = true
		}
		r.session.HARPath = harPath
		contextOptions.RecordHarPath = playwright.String(harPath)
		contextOptions.RecordHarContent = playwright.HarContentPolicyEmbed
	}
	context, err := browser.NewContext(contextOptions)
	if err != nil {
		r.cleanup()
		return fmt.Errorf("create browser context: %w", err)
	}
	r.context = context

	if err := context.ExposeBinding("__apexloadRecord", r.handleRecordBinding); err != nil {
		r.cleanup()
		return fmt.Errorf("expose recorder binding: %w", err)
	}
	if err := context.AddInitScript(playwright.Script{Content: playwright.String(captureScript)}); err != nil {
		r.cleanup()
		return fmt.Errorf("inject capture script: %w", err)
	}
	context.OnPage(r.registerPage)

	page, err := context.NewPage()
	if err != nil {
		r.cleanup()
		return fmt.Errorf("open recording page: %w", err)
	}
	if _, err := page.Goto(startURL); err != nil {
		r.cleanup()
		return fmt.Errorf("navigate to %s: %w", startURL, err)
	}
	return nil
}

// registerPage wires capture listeners for the initial page and any popup.
func (r *Recorder) registerPage(page playwright.Page) {
	r.mu.Lock()
	isPopup := len(r.session.NavEvents) > 0 || len(r.session.Steps) > 0
	if isPopup {
		r.session.appendStep(Step{
			Kind:      StepSwitchWindow,
			Value:     page.URL(),
			Timestamp: time.Now(),
			URL:       page.URL(),
		})
	}
	r.mu.Unlock()

	page.OnFrameNavigated(func(frame playwright.Frame) {
		if frame != page.MainFrame() {
			return
		}
		r.mu.Lock()
		r.session.NavEvents = append(r.session.NavEvents, NavEvent{Timestamp: time.Now(), URL: frame.URL()})
		r.mu.Unlock()
	})
	page.OnRequest(func(playwright.Request) {
		r.mu.Lock()
		r.session.NetworkEvents = append(r.session.NetworkEvents, time.Now())
		r.mu.Unlock()
	})
	page.OnWebSocket(func(ws playwright.WebSocket) {
		r.mu.Lock()
		r.session.WebSockets = append(r.session.WebSockets, WebSocketSummary{URL: ws.URL(), OpenedAt: time.Now()})
		index := len(r.session.WebSockets) - 1
		r.mu.Unlock()
		ws.OnFrameSent(func([]byte) {
			r.mu.Lock()
			r.session.WebSockets[index].FramesSent++
			r.mu.Unlock()
		})
		ws.OnFrameReceived(func([]byte) {
			r.mu.Lock()
			r.session.WebSockets[index].FramesReceived++
			r.mu.Unlock()
		})
	})
	page.OnClose(func(playwright.Page) {
		if r.context == nil || len(r.context.Pages()) == 0 {
			r.signalDone()
		}
	})
}

// handleRecordBinding receives capture-script payloads. The binding source
// frame resolves cross-origin frames the script cannot identify itself.
func (r *Recorder) handleRecordBinding(source *playwright.BindingSource, args ...any) any {
	if len(args) == 0 {
		return nil
	}
	payload, ok := args[0].(string)
	if !ok {
		return nil
	}
	step, err := stepFromEvent(payload)
	if err != nil {
		r.mu.Lock()
		r.session.Warnings = append(r.session.Warnings, fmt.Sprintf("dropped capture event: %v", err))
		r.mu.Unlock()
		return nil
	}
	if step.FrameSelector == "" && source != nil && source.Frame != nil && source.Page != nil {
		if source.Frame != source.Page.MainFrame() {
			step.FrameSelector = frameSelectorFromURL(source.Frame.URL())
		}
	}
	r.mu.Lock()
	r.session.appendStep(step)
	r.mu.Unlock()
	return nil
}

// frameSelectorFromURL builds a replayable iframe selector for cross-origin
// frames where the capture script cannot read frameElement.
func frameSelectorFromURL(frameURL string) string {
	parsed, err := url.Parse(frameURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	match := parsed.Host + parsed.Path
	return fmt.Sprintf(`iframe[src*="%s"]`, strings.ReplaceAll(match, `"`, `\"`))
}

// Done is closed when the user closes the browser (or it disconnects).
func (r *Recorder) Done() <-chan struct{} { return r.done }

func (r *Recorder) signalDone() { r.doneOnce.Do(func() { close(r.done) }) }

// Stop closes the browser (flushing the side-car HAR), loads the HAR into the
// session, and returns the finished session. Safe to call after the user
// already closed the window.
func (r *Recorder) Stop() (*Session, error) {
	r.mu.Lock()
	if r.stopped {
		session := r.session
		r.mu.Unlock()
		return session, nil
	}
	r.stopped = true
	r.mu.Unlock()

	if r.context != nil {
		_ = r.context.Close() // flushes RecordHarPath
	}
	r.cleanup()
	r.signalDone()

	r.mu.Lock()
	defer r.mu.Unlock()
	r.session.StoppedAt = time.Now()
	if r.session.HARPath != "" {
		data, err := os.ReadFile(r.session.HARPath)
		if err != nil {
			r.session.Warnings = append(r.session.Warnings, fmt.Sprintf("side-car HAR unavailable: %v", err))
		} else {
			r.session.HAR = data
		}
		if r.harTemp {
			_ = os.Remove(r.session.HARPath)
			r.session.HARPath = ""
		}
	}
	return r.session, nil
}

func (r *Recorder) cleanup() {
	if r.browser != nil {
		_ = r.browser.Close()
		r.browser = nil
	}
	if r.pw != nil {
		_ = r.pw.Stop()
		r.pw = nil
	}
}
