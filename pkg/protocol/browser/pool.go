package browser

import (
	"fmt"
	"os"
	"strings"
	"sync"

	playwright "github.com/mxschmitt/playwright-go"
)

var (
	globalPlaywright *playwright.Playwright
	globalBrowsers   = make(map[string]playwright.Browser)
	globalMutex      sync.Mutex
	// testChromiumLaunchArgs lets integration tests add Chromium flags, such
	// as trusting a fixture certificate for QUIC. It is part of the pool key
	// and is never set outside tests. Guarded by globalMutex.
	testChromiumLaunchArgs []string
)

// Browser QUIC modes (config.browser.quic). "auto" leaves the engine's own
// HTTP/3 behavior alone; "off" disables QUIC at launch. QUIC is a
// launch-level switch, so it is part of the browser pool key.
const (
	browserQUICAuto = "auto"
	browserQUICOff  = "off"
)

func getBrowserInstance(engine string, headless bool, quic ...string) (playwright.Browser, error) {
	globalMutex.Lock()
	defer globalMutex.Unlock()

	if globalPlaywright == nil {
		pw, err := playwright.Run()
		if err != nil {
			return nil, fmt.Errorf("failed to start playwright: %w", err)
		}
		globalPlaywright = pw
	}

	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine == "" {
		engine = "chromium"
	}
	quicMode := browserQUICAuto
	if len(quic) > 0 && quic[0] == browserQUICOff {
		quicMode = browserQUICOff
	}

	key := browserPoolKey(engine, headless, quicMode)
	if browser := globalBrowsers[key]; browser != nil {
		if browser.IsConnected() {
			return browser, nil
		}
		delete(globalBrowsers, key)
	}

	var (
		b   playwright.Browser
		err error
	)
	if engine == "webkit" {
		b, err = globalPlaywright.WebKit.Launch(playwright.BrowserTypeLaunchOptions{
			Headless: playwright.Bool(headless),
			Timeout:  playwright.Float(30000),
		})
		if err != nil {
			return nil, err
		}
		globalBrowsers[key] = b
		return b, nil
	}

	if engine == "firefox" {
		options := playwright.BrowserTypeLaunchOptions{
			Headless: playwright.Bool(headless),
			Timeout:  playwright.Float(30000),
		}
		if quicMode == browserQUICOff {
			options.FirefoxUserPrefs = map[string]any{"network.http.http3.enable": false}
		}
		b, err = globalPlaywright.Firefox.Launch(options)
		if err != nil {
			return nil, err
		}
		globalBrowsers[key] = b
		return b, nil
	}

	if engine == "chrome" {
		b, err = globalPlaywright.Chromium.Launch(chromiumLaunchOptions(headless, "chrome", quicMode))
		if err != nil {
			return nil, err
		}
		globalBrowsers[key] = b
		return b, nil
	}

	b, err = globalPlaywright.Chromium.Launch(chromiumLaunchOptions(headless, "", quicMode))
	if err != nil {
		return nil, err
	}
	globalBrowsers[key] = b
	return b, nil
}

func browserPoolKey(engine string, headless bool, quic string) string {
	channel := ""
	if engine == "chrome" {
		channel = "chrome"
	}
	if quic != browserQUICOff {
		quic = browserQUICAuto
	}
	return strings.Join([]string{
		engine,
		fmt.Sprintf("headless=%t", headless),
		"channel=" + channel,
		"executable=" + strings.TrimSpace(os.Getenv("APEXLOAD_BROWSER_EXECUTABLE")),
		fmt.Sprintf("sandbox=%t", envFlagEnabled("APEXLOAD_BROWSER_SANDBOX")),
		"quic=" + quic,
		"test_args=" + strings.Join(testChromiumLaunchArgs, " "),
	}, "|")
}

// chromiumLaunchOptions builds the launch configuration for Chromium-family
// engines from the container-facing env contract:
//
//	APEXLOAD_BROWSER_EXECUTABLE
//	    Path to a system Chromium/Chrome binary. Required when the
//	    Playwright-managed Chromium build is not installed — e.g. the Alpine
//	    generator image, whose musl-built chromium package Playwright cannot
//	    download itself. When set it overrides the channel: both the
//	    "chromium" and "chrome" engines drive this executable.
//	APEXLOAD_BROWSER_SANDBOX=true
//	    Drops --no-sandbox. The default keeps it because the hardened pod
//	    securityContext (drop-ALL capabilities, RuntimeDefault seccomp) cannot
//	    start Chromium's setuid or user-namespace sandbox; without a sandbox a
//	    compromised page runs as the generator process, so isolate browser
//	    generators at the pod layer instead (dedicated pool with a gVisor/Kata
//	    RuntimeClass) or enable this where unprivileged userns is available.
//
// --disable-dev-shm-usage is always set: containers default to a 64MB
// /dev/shm, which crashes Chromium renderers under load; this moves shared
// memory to /tmp, which the manifests back with a sized emptyDir.
func chromiumLaunchOptions(headless bool, channel string, quic ...string) playwright.BrowserTypeLaunchOptions {
	options := playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(headless),
		Timeout:  playwright.Float(30000),
		Args: []string{
			"--disable-gpu",
			"--hide-scrollbars",
			"--disable-dev-shm-usage",
		},
	}
	if !envFlagEnabled("APEXLOAD_BROWSER_SANDBOX") {
		options.Args = append(options.Args, "--no-sandbox")
	}
	if len(quic) > 0 && quic[0] == browserQUICOff {
		options.Args = append(options.Args, "--disable-quic")
	}
	options.Args = append(options.Args, testChromiumLaunchArgs...)
	if executable := strings.TrimSpace(os.Getenv("APEXLOAD_BROWSER_EXECUTABLE")); executable != "" {
		options.ExecutablePath = playwright.String(executable)
	} else if channel != "" {
		options.Channel = playwright.String(channel)
	}
	return options
}

func envFlagEnabled(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func RuntimeInstallWarning(engine string) string {
	return runtimeInstallWarning(engine, browserExecutableAvailable)
}

func runtimeInstallWarning(engine string, executableAvailable func(string) error) string {
	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine == "" || engine == "chromium" || engine == "chrome" {
		return ""
	}
	if err := executableAvailable(engine); err != nil {
		return fmt.Sprintf("Playwright %s runtime is unavailable: %v. Run `npx playwright install %s` before replaying this browser script.", engine, err, engine)
	}
	return ""
}

func browserExecutableAvailable(engine string) error {
	globalMutex.Lock()
	defer globalMutex.Unlock()

	if globalPlaywright == nil {
		pw, err := playwright.Run()
		if err != nil {
			return fmt.Errorf("failed to start playwright: %w", err)
		}
		globalPlaywright = pw
	}

	var executablePath string
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "webkit":
		executablePath = globalPlaywright.WebKit.ExecutablePath()
	case "firefox":
		executablePath = globalPlaywright.Firefox.ExecutablePath()
	default:
		return nil
	}
	if strings.TrimSpace(executablePath) == "" {
		return fmt.Errorf("browser executable path is empty")
	}
	if _, err := os.Stat(executablePath); err != nil {
		return err
	}
	return nil
}
