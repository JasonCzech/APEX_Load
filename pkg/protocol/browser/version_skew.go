package browser

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/browsercontract"
	playwright "github.com/mxschmitt/playwright-go"
)

// Playwright is tested with one browser build per engine, named in its
// driver's browsers.json. A browser of another major version (a distro
// Chromium that moved on, Chrome stable) can behave differently, and a script
// recorded in one major can replay in another. A session reports either skew
// once, in its first action's evidence. With APEXLOAD_BROWSER_VERSION_SKEW=fail
// a session whose browser does not match the driver's fails to start instead.

const (
	// browserVersionSkewEnv set to "fail" fails a launch whose browser major
	// differs from the driver's.
	browserVersionSkewEnv = "APEXLOAD_BROWSER_VERSION_SKEW"
	// BrowserVersionSkewEvidenceKey holds a session's skews in its first
	// action's protocol evidence.
	BrowserVersionSkewEvidenceKey = "browser_version_skew"
	// Skew sources: the driver's tested build, or the build a script was
	// recorded with.
	BrowserVersionSkewDriver    = "driver"
	BrowserVersionSkewRecording = "recording"
	// maxDriverManifestBytes bounds the driver's browsers.json.
	maxDriverManifestBytes = 1 << 20
)

// ValidBrowserVersion reports a dotted numeric browser version
// (149.0.7827.55).
func ValidBrowserVersion(version string) bool {
	return browsercontract.ValidBrowserVersion(version)
}

// BrowserMajorVersion is a browser version's major number.
func BrowserMajorVersion(version string) (int, bool) {
	return browsercontract.BrowserMajorVersion(version)
}

// browserVersionSkew is one difference between the launched browser's major
// version and the one expected.
type browserVersionSkew struct {
	source   string
	actual   string
	expected string
}

// checkBrowserVersion compares the launched browser with the driver's build
// and with the build the script was recorded with, once per session. It fails
// only for a driver skew when APEXLOAD_BROWSER_VERSION_SKEW=fail, and then at
// every launch of the session.
func (b *Browser) checkBrowserVersion(actual string) error {
	b.mu.Lock()
	checked, engine, recorded := b.browserVersionChecked, b.engine, b.recordedBrowserVersion
	b.mu.Unlock()
	if checked {
		return nil
	}
	var skews []browserVersionSkew
	if expected := driverBrowserVersion(engine); majorsDiffer(actual, expected) {
		if strings.EqualFold(strings.TrimSpace(os.Getenv(browserVersionSkewEnv)), "fail") {
			return fmt.Errorf("browser version skew: %s launched %s, but Playwright %s was tested with %s (%s=fail)", engineLabel(engine), actual, driverVersion(), expected, browserVersionSkewEnv)
		}
		skews = append(skews, browserVersionSkew{source: BrowserVersionSkewDriver, actual: actual, expected: expected})
	}
	if majorsDiffer(actual, recorded) {
		skews = append(skews, browserVersionSkew{source: BrowserVersionSkewRecording, actual: actual, expected: recorded})
	}
	b.mu.Lock()
	if !b.browserVersionChecked {
		b.browserVersionChecked, b.browserVersionSkews = true, skews
	}
	b.mu.Unlock()
	return nil
}

// applyBrowserVersionSkew reports the session's skews in the result, once.
func (b *Browser) applyBrowserVersionSkew(result *protocol.Result) {
	b.mu.Lock()
	skews := b.browserVersionSkews
	b.browserVersionSkews = nil
	b.mu.Unlock()
	if len(skews) == 0 {
		return
	}
	records := make([]any, 0, len(skews))
	for _, skew := range skews {
		code := "browser_version_skew"
		if skew.source == BrowserVersionSkewRecording {
			code = "browser_recording_version_skew"
		}
		result.WarningCodes = append(result.WarningCodes, code)
		records = append(records, map[string]any{"source": skew.source, "actual": skew.actual, "expected": skew.expected})
	}
	if result.ProtocolEvidence == nil {
		result.ProtocolEvidence = map[string]any{}
	}
	result.ProtocolEvidence[BrowserVersionSkewEvidenceKey] = records
}

func majorsDiffer(actual, expected string) bool {
	actualMajor, okActual := BrowserMajorVersion(actual)
	expectedMajor, okExpected := BrowserMajorVersion(expected)
	return okActual && okExpected && actualMajor != expectedMajor
}

func engineLabel(engine string) string {
	switch engine {
	case "webkit":
		return "WebKit"
	case "firefox":
		return "Firefox"
	case "chrome":
		return "Chrome"
	}
	return "Chromium"
}

// driverManifests caches each driver browsers.json, by path, as its browser
// versions by name.
var driverManifests sync.Map

// driverBrowserVersion is the browser version the Playwright driver was
// tested with for engine, or "" when its browsers.json cannot be read.
func driverBrowserVersion(engine string) string {
	name := "chromium"
	switch engine {
	case "webkit", "firefox":
		name = engine
	}
	path := driverManifestPath()
	if path == "" {
		return ""
	}
	if cached, ok := driverManifests.Load(path); ok {
		return cached.(map[string]string)[name]
	}
	versions := readDriverManifest(path)
	driverManifests.Store(path, versions)
	return versions[name]
}

// driverManifestPath is where playwright-go's driver keeps browsers.json: next
// to an explicit PLAYWRIGHT_CLI_PATH, under PLAYWRIGHT_DRIVER_PATH, or in the
// driver's default cache directory.
func driverManifestPath() string {
	if cli := strings.TrimSpace(os.Getenv("PLAYWRIGHT_CLI_PATH")); cli != "" {
		return filepath.Join(filepath.Dir(cli), "browsers.json")
	}
	dir := strings.TrimSpace(os.Getenv("PLAYWRIGHT_DRIVER_PATH"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		switch runtime.GOOS {
		case "windows":
			dir = filepath.Join(home, "AppData", "Local")
		case "darwin":
			dir = filepath.Join(home, "Library", "Caches")
		default:
			dir = filepath.Join(home, ".cache")
		}
		dir = filepath.Join(dir, "ms-playwright-go", driverVersion())
	}
	return filepath.Join(dir, "package", "browsers.json")
}

// driverVersion is the bundled Playwright driver's version. NewDriver without
// options has no side effects (given Stderr, it would redirect the standard
// logger).
func driverVersion() string {
	driver, err := playwright.NewDriver()
	if err != nil || driver == nil {
		return ""
	}
	return driver.Version
}

func readDriverManifest(path string) map[string]string {
	versions := map[string]string{}
	file, err := os.Open(path)
	if err != nil {
		return versions
	}
	defer file.Close()
	var manifest struct {
		Browsers []struct {
			Name           string `json:"name"`
			BrowserVersion string `json:"browserVersion"`
		} `json:"browsers"`
	}
	if err := json.NewDecoder(io.LimitReader(file, maxDriverManifestBytes)).Decode(&manifest); err != nil {
		return versions
	}
	for _, browser := range manifest.Browsers {
		if ValidBrowserVersion(browser.BrowserVersion) {
			versions[browser.Name] = browser.BrowserVersion
		}
	}
	return versions
}
