package browser

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	playwright "github.com/mxschmitt/playwright-go"
)

// Browser integration tests drive a real Playwright-managed browser process.
// They used to collapse EVERY Init failure into t.Skipf whenever the error text
// merely mentioned "chrome" or "executable" — and several skipped on any error
// at all. Chromium reports most of its own launch and startup faults with
// "chrome" in the message, so a genuinely broken runtime, or a regression in
// any code path that runs during Init, reported as a green skip across the
// whole package. Nothing distinguished "this machine has no browser" from
// "the browser is here and we broke it".
//
// requireBrowserRuntime replaces that with one documented environment gate:
//
//	APEXLOAD_BROWSER_TESTS=require
//	    A launch failure is always a test FAILURE. Set this anywhere the
//	    runtime is installed on purpose — CI images, the browser CI job — so a
//	    missing or broken runtime cannot hide as a skip.
//	APEXLOAD_BROWSER_TESTS=skip
//	    A launch failure always skips. For workstations that deliberately have
//	    no browser runtime installed.
//	APEXLOAD_BROWSER_TESTS=auto  (or unset — the default)
//	    Probe the environment. The test skips only when the probe cannot find
//	    the engine's browser executable. If the executable is installed and the
//	    launch still failed, that is a real defect and the test FAILS.
//
// What `auto` does NOT guarantee: probeBrowserExecutable boots the Playwright
// driver and stats the path the driver resolves, so "cannot find" also covers
// "playwright.Run() failed" and "the installed browser build no longer matches
// the pinned driver version". Both of those skip. Set require wherever the
// runtime is provisioned on purpose; that is the only mode in which a launch
// failure can never be reported as a skip.
//
// Any other value is a hard failure rather than a silent fallback, so a typo in
// CI cannot quietly restore the old always-skip behaviour.
const browserTestsEnv = "APEXLOAD_BROWSER_TESTS"

const (
	browserTestsAuto    = "auto"
	browserTestsRequire = "require"
	browserTestsSkip    = "skip"
)

// browserRuntimeMode normalizes the raw env value. Kept separate from the
// *testing.T plumbing so it is directly testable.
func browserRuntimeMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", browserTestsAuto:
		return browserTestsAuto, nil
	case browserTestsRequire:
		return browserTestsRequire, nil
	case browserTestsSkip:
		return browserTestsSkip, nil
	default:
		return "", fmt.Errorf("%s=%q is not one of auto, require, skip", browserTestsEnv, raw)
	}
}

// browserRuntimeVerdict decides whether a launch failure is a skip or a
// failure. probe is consulted only in auto mode, and only then; require and
// skip never pay for it. It returns the probe error so the caller can report
// exactly why the environment was judged to lack a runtime.
func browserRuntimeVerdict(mode string, probe func() error) (skip bool, probeErr error) {
	switch mode {
	case browserTestsRequire:
		return false, nil
	case browserTestsSkip:
		return true, nil
	}
	if probe == nil {
		return false, nil
	}
	if err := probe(); err != nil {
		return true, err
	}
	return false, nil
}

// browserRuntimeAction is the gate's verdict for a non-nil launch error.
type browserRuntimeAction int

const (
	browserRuntimeSkipTest browserRuntimeAction = iota
	browserRuntimeFailTest
)

func (a browserRuntimeAction) String() string {
	if a == browserRuntimeSkipTest {
		return "skip"
	}
	return "fail"
}

// browserRuntimeDecide is the entire gate as a pure function: raw env value,
// engine, the launch error and the installed-probe in; skip-or-fail plus the
// message out. It takes err specifically so a test can pin that the launch
// error's TEXT never influences the decision — the old idiom's defect. Keep
// requireBrowserRuntime a thin wrapper over this so there is nowhere else for
// a text pre-filter to hide.
func browserRuntimeDecide(rawMode, engine string, err error, probe func(string) error) (browserRuntimeAction, string) {
	engine = normalizeBrowserTestEngine(engine)
	mode, modeErr := browserRuntimeMode(rawMode)
	if modeErr != nil {
		return browserRuntimeFailTest, fmt.Sprintf("%v (launch error was: %v)", modeErr, err)
	}
	var probeFn func() error
	if probe != nil {
		probeFn = func() error { return probe(engine) }
	}
	skip, probeErr := browserRuntimeVerdict(mode, probeFn)
	if skip {
		if probeErr != nil {
			return browserRuntimeSkipTest, fmt.Sprintf(
				"%s runtime is not installed in this environment (%v); set %s=require to turn this into a failure. Launch error: %v",
				engine, probeErr, browserTestsEnv, err)
		}
		return browserRuntimeSkipTest, fmt.Sprintf("%s=skip: not exercising the %s runtime. Launch error: %v", browserTestsEnv, engine, err)
	}
	if mode == browserTestsRequire {
		return browserRuntimeFailTest, fmt.Sprintf("%s=require: the %s runtime is expected to be present here and failed to launch: %v", browserTestsEnv, engine, err)
	}
	return browserRuntimeFailTest, fmt.Sprintf("%s runtime is installed in this environment but failed to launch — this is a real failure, not an environment gap: %v", engine, err)
}

// browserRuntimeReporter is the slice of *testing.T that requireBrowserRuntime
// uses. The gate takes this interface rather than *testing.T for one reason:
// the final hop — turning the verdict into t.Skip or t.Fatal — is exactly where
// the original defect lived, and with a concrete *testing.T that hop only runs
// on a machine whose browser runtime failed to launch, i.e. never on a green CI
// image. A fake reporter lets TestRequireBrowserRuntimeMapsVerdictToSkipOrFail
// drive it directly.
type browserRuntimeReporter interface {
	Helper()
	Skip(args ...any)
	Fatal(args ...any)
}

// browserRuntimeProbe is the installed-probe requireBrowserRuntime consults.
// It is a var solely so the gate's own table test can pin both probe answers on
// any machine; nothing outside that test reassigns it.
var browserRuntimeProbe func(string) error = browserRuntimeInstalled

// requireBrowserRuntime interprets a Browser.Init error. It never returns when
// err is non-nil: it either fails or skips the test. engine may be empty for
// the default (chromium) engine.
func requireBrowserRuntime(t browserRuntimeReporter, engine string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	action, message := browserRuntimeDecide(os.Getenv(browserTestsEnv), engine, err, browserRuntimeProbe)
	if action == browserRuntimeSkipTest {
		t.Skip(message)
	}
	t.Fatal(message)
}

func normalizeBrowserTestEngine(engine string) string {
	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine == "" {
		return "chromium"
	}
	return engine
}

var (
	browserProbeMu    sync.Mutex
	browserProbeCache = map[string]error{}
)

// browserRuntimeInstalled reports whether the engine's executable exists on
// this machine, independent of whether a launch succeeded. The result is
// memoized: the probe boots the Playwright driver, and the package runs dozens
// of integration tests.
func browserRuntimeInstalled(engine string) error {
	browserProbeMu.Lock()
	defer browserProbeMu.Unlock()
	if cached, ok := browserProbeCache[engine]; ok {
		return cached
	}
	err := probeBrowserExecutable(engine)
	browserProbeCache[engine] = err
	return err
}

func probeBrowserExecutable(engine string) error {
	// chromiumLaunchOptions honours APEXLOAD_BROWSER_EXECUTABLE ahead of the
	// Playwright-managed build for both chromium-family engines, so the probe
	// has to look at the same override the pool would use.
	if engine == "chromium" || engine == "chrome" {
		if override := strings.TrimSpace(os.Getenv("APEXLOAD_BROWSER_EXECUTABLE")); override != "" {
			if _, err := os.Stat(override); err != nil {
				return fmt.Errorf("APEXLOAD_BROWSER_EXECUTABLE=%q is not usable: %w", override, err)
			}
			return nil
		}
	}

	globalMutex.Lock()
	defer globalMutex.Unlock()
	if globalPlaywright == nil {
		pw, runErr := playwright.Run()
		if runErr != nil {
			return fmt.Errorf("playwright driver unavailable: %w", runErr)
		}
		globalPlaywright = pw
	}

	var executablePath string
	switch engine {
	case "webkit":
		executablePath = globalPlaywright.WebKit.ExecutablePath()
	case "firefox":
		executablePath = globalPlaywright.Firefox.ExecutablePath()
	default:
		// "chrome" resolves the Chromium channel, whose binary lives outside
		// the Playwright download cache. Treating it as chromium is the
		// conservative choice: a channel-only gap surfaces as a failure with
		// the real launch error attached rather than a silent skip.
		executablePath = globalPlaywright.Chromium.ExecutablePath()
	}
	if strings.TrimSpace(executablePath) == "" {
		return errors.New("browser executable path is empty")
	}
	if _, err := os.Stat(executablePath); err != nil {
		return fmt.Errorf("browser executable %q is not installed: %w", executablePath, err)
	}
	return nil
}

func TestBrowserRuntimeModeParsing(t *testing.T) {
	cases := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{raw: "", want: browserTestsAuto},
		{raw: "auto", want: browserTestsAuto},
		{raw: "  AUTO  ", want: browserTestsAuto},
		{raw: "require", want: browserTestsRequire},
		{raw: "Require", want: browserTestsRequire},
		{raw: "skip", want: browserTestsSkip},
		{raw: "1", wantErr: true},
		{raw: "true", wantErr: true},
		{raw: "yes", wantErr: true},
	}
	for _, tc := range cases {
		got, err := browserRuntimeMode(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("browserRuntimeMode(%q) = %q, want an error", tc.raw, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("browserRuntimeMode(%q) returned error: %v", tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("browserRuntimeMode(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestBrowserRuntimeVerdict(t *testing.T) {
	missing := errors.New("browser executable is not installed")

	cases := []struct {
		name       string
		mode       string
		probe      func() error
		wantSkip   bool
		wantProbed bool
	}{
		{
			name:     "require never skips even when the runtime is absent",
			mode:     browserTestsRequire,
			probe:    func() error { return missing },
			wantSkip: false,
		},
		{
			name:     "skip always skips even when the runtime is present",
			mode:     browserTestsSkip,
			probe:    func() error { return nil },
			wantSkip: true,
		},
		{
			name:       "auto skips only when the runtime is genuinely absent",
			mode:       browserTestsAuto,
			probe:      func() error { return missing },
			wantSkip:   true,
			wantProbed: true,
		},
		{
			name:       "auto fails when the runtime is installed but the launch broke",
			mode:       browserTestsAuto,
			probe:      func() error { return nil },
			wantSkip:   false,
			wantProbed: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probed := false
			skip, probeErr := browserRuntimeVerdict(tc.mode, func() error {
				probed = true
				return tc.probe()
			})
			if skip != tc.wantSkip {
				t.Fatalf("skip = %v, want %v", skip, tc.wantSkip)
			}
			if probed != tc.wantProbed {
				t.Fatalf("probe called = %v, want %v", probed, tc.wantProbed)
			}
			if tc.wantSkip && tc.wantProbed && probeErr == nil {
				t.Fatal("auto-mode skip must report why the runtime was judged absent")
			}
		})
	}
}

// The old idiom skipped on any error mentioning "chrome" or "executable".
// Chromium puts "chrome" in the message for faults that have nothing to do with
// the runtime being absent, which is precisely how a broken runtime read as
// green. Feed those exact messages through the gate and pin that the outcome
// tracks the probe and the mode only — never the text. Both probe answers are
// stubbed, so this holds on a machine with or without a browser installed.
func TestBrowserRuntimeGateDoesNotSkipOnErrorTextAlone(t *testing.T) {
	installed := func(string) error { return nil }
	absent := func(string) error {
		return errors.New(`browser executable "/opt/ms-playwright/chromium/chrome" is not installed`)
	}

	launchErrors := []string{
		"chrome crashed while opening the first page",
		"browser executable exited unexpectedly",
		"chromium closed the connection",
		// Control: no runtime-flavoured words at all. It must be treated
		// identically to the three above.
		"context deadline exceeded",
	}

	for _, message := range launchErrors {
		launchErr := errors.New(message)

		// auto + runtime present: always a failure, whatever the text says.
		action, msg := browserRuntimeDecide("", "chromium", launchErr, installed)
		if action != browserRuntimeFailTest {
			t.Fatalf("auto mode with chromium installed: launch error %q produced %s (%s), want fail", message, action, msg)
		}
		if !strings.Contains(msg, message) {
			t.Fatalf("failure message for %q does not carry the launch error: %s", message, msg)
		}

		// auto + runtime absent: always a skip, whatever the text says.
		action, msg = browserRuntimeDecide("", "chromium", launchErr, absent)
		if action != browserRuntimeSkipTest {
			t.Fatalf("auto mode with chromium absent: launch error %q produced %s (%s), want skip", message, action, msg)
		}

		// require: never skips, whatever the probe or the text says.
		if action, msg = browserRuntimeDecide("require", "chromium", launchErr, absent); action != browserRuntimeFailTest {
			t.Fatalf("%s=require: launch error %q produced %s (%s), want fail", browserTestsEnv, message, action, msg)
		}

		// skip: always skips, whatever the probe or the text says.
		if action, msg = browserRuntimeDecide("skip", "chromium", launchErr, installed); action != browserRuntimeSkipTest {
			t.Fatalf("%s=skip: launch error %q produced %s (%s), want skip", browserTestsEnv, message, action, msg)
		}
	}
}

// errFakeReporterStop unwinds the fake reporter the way testing's own
// runtime.Goexit unwinds a real t.Skip/t.Fatal, so the fake reproduces the
// "never returns" contract instead of falling through into the next branch.
var errFakeReporterStop = errors.New("fake browser runtime reporter stop")

type fakeBrowserRuntimeReporter struct {
	helperCalls int
	skipped     bool
	failed      bool
	message     string
}

func (f *fakeBrowserRuntimeReporter) Helper() { f.helperCalls++ }

func (f *fakeBrowserRuntimeReporter) Skip(args ...any) {
	f.skipped = true
	f.message = fmt.Sprint(args...)
	panic(errFakeReporterStop)
}

func (f *fakeBrowserRuntimeReporter) Fatal(args ...any) {
	f.failed = true
	f.message = fmt.Sprint(args...)
	panic(errFakeReporterStop)
}

// runRequireBrowserRuntime calls the gate against a fake reporter and reports
// whether it returned normally (only legal when err is nil).
func runRequireBrowserRuntime(t *testing.T, rec *fakeBrowserRuntimeReporter, engine string, err error) (returned bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			if r != errFakeReporterStop {
				panic(r)
			}
			returned = false
		}
	}()
	requireBrowserRuntime(rec, engine, err)
	return true
}

// TestBrowserRuntimeGateDoesNotSkipOnErrorTextAlone pins the pure decision
// function. This test pins the hop after it: requireBrowserRuntime itself, the
// only place that turns a verdict into Skip or Fatal. Without it that hop is
// dead code on every machine where the runtime launches — including CI — so the
// mapping could be inverted, or a "chrome"/"executable" text pre-filter
// reinstated ahead of browserRuntimeDecide, with the whole package green.
func TestRequireBrowserRuntimeMapsVerdictToSkipOrFail(t *testing.T) {
	installed := func(string) error { return nil }
	absent := func(string) error {
		return errors.New(`browser executable "/opt/ms-playwright/chromium/chrome" is not installed`)
	}

	// Every message here is one the old text pre-filter would have swallowed,
	// plus a control with no runtime-flavoured words at all.
	launchMessages := []string{
		"chrome crashed while opening the first page",
		"browser executable exited unexpectedly",
		"browserType.launch: Browser closed /root/.cache/ms-playwright/webkit-2311/pw_run.sh",
		"context deadline exceeded",
	}

	cases := []struct {
		name     string
		rawMode  string
		probe    func(string) error
		engine   string
		wantSkip bool
	}{
		{name: "auto with runtime installed fails", rawMode: "", probe: installed, engine: "chromium", wantSkip: false},
		{name: "auto with runtime absent skips", rawMode: "auto", probe: absent, engine: "chromium", wantSkip: true},
		{name: "require never skips", rawMode: "require", probe: absent, engine: "webkit", wantSkip: false},
		{name: "skip always skips", rawMode: "skip", probe: installed, engine: "webkit", wantSkip: true},
		{name: "invalid mode fails", rawMode: "1", probe: installed, engine: "", wantSkip: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(browserTestsEnv, tc.rawMode)
			previous := browserRuntimeProbe
			browserRuntimeProbe = tc.probe
			t.Cleanup(func() { browserRuntimeProbe = previous })

			for _, message := range launchMessages {
				rec := &fakeBrowserRuntimeReporter{}
				if runRequireBrowserRuntime(t, rec, tc.engine, errors.New(message)) {
					t.Fatalf("requireBrowserRuntime returned for launch error %q; it must skip or fail", message)
				}
				if rec.helperCalls != 1 {
					t.Fatalf("Helper() called %d times, want 1", rec.helperCalls)
				}
				if tc.wantSkip {
					if !rec.skipped || rec.failed {
						t.Fatalf("launch error %q: skipped=%v failed=%v, want a skip (message: %s)", message, rec.skipped, rec.failed, rec.message)
					}
				} else {
					if !rec.failed || rec.skipped {
						t.Fatalf("launch error %q: skipped=%v failed=%v, want a failure (message: %s)", message, rec.skipped, rec.failed, rec.message)
					}
				}
				if !strings.Contains(rec.message, message) {
					t.Fatalf("reported message does not carry the launch error %q: %s", message, rec.message)
				}
			}
		})
	}

	// A nil error must return without touching the reporter at all.
	t.Run("nil error returns", func(t *testing.T) {
		t.Setenv(browserTestsEnv, "require")
		previous := browserRuntimeProbe
		browserRuntimeProbe = absent
		t.Cleanup(func() { browserRuntimeProbe = previous })

		rec := &fakeBrowserRuntimeReporter{}
		if !runRequireBrowserRuntime(t, rec, "chromium", nil) {
			t.Fatal("requireBrowserRuntime did not return for a nil error")
		}
		if rec.skipped || rec.failed {
			t.Fatalf("nil error touched the reporter: skipped=%v failed=%v (%s)", rec.skipped, rec.failed, rec.message)
		}
	})
}
