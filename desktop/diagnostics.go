package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/buildinfo"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// OpenURL opens an external URL in the system browser (not the webview). The
// dashboard routes external links here so clicking them never replaces the SPA.
// Only http/https/mailto are allowed.
func (a *App) OpenURL(raw string) error {
	ctx := a.runtimeContext()
	if ctx == nil {
		return fmt.Errorf("desktop runtime is not ready")
	}
	clean, err := validateOpenURL(raw)
	if err != nil {
		return err
	}
	wailsruntime.BrowserOpenURL(ctx, clean)
	return nil
}

// OpenSystemSettings opens one narrowly allow-listed operating-system settings
// pane. Custom schemes are intentionally excluded from OpenURL; this separate
// method prevents renderer input from turning native scheme handling into a
// generic launcher.
func (a *App) OpenSystemSettings(target string) error {
	ctx := a.runtimeContext()
	if ctx == nil {
		return fmt.Errorf("desktop runtime is not ready")
	}
	settingsURL, err := systemSettingsURL(runtime.GOOS, target)
	if err != nil {
		return err
	}
	wailsruntime.BrowserOpenURL(ctx, settingsURL)
	return nil
}

func systemSettingsURL(goos, target string) (string, error) {
	return systemSettingsURLForVersion(goos, target, operatingSystemVersion())
}

func systemSettingsURLForVersion(goos, target, osVersion string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(goos)) + ":" + strings.ToLower(strings.TrimSpace(target)) {
	case "darwin:network":
		if macOSMajorVersion(osVersion) < 13 {
			return "x-apple.systempreferences:com.apple.preference.network", nil
		}
		return "x-apple.systempreferences:com.apple.Network-Settings.extension", nil
	case "windows:proxy":
		return "ms-settings:network-proxy", nil
	default:
		return "", fmt.Errorf("system settings target %q is unavailable on %s", target, goos)
	}
}

func macOSMajorVersion(raw string) int {
	major, _, _ := strings.Cut(strings.TrimSpace(raw), ".")
	value, err := strconv.Atoi(major)
	if err != nil || value < 1 {
		return 0
	}
	return value
}

// validateOpenURL parses raw and enforces the external-scheme allow-list. Pure
// (no ctx) so the rejection path is unit-testable. Returns the cleaned URL.
func validateOpenURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	if !isExternalOpenScheme(u.Scheme) {
		return "", fmt.Errorf("refusing to open scheme %q", u.Scheme)
	}
	return raw, nil
}

// isExternalOpenScheme allow-lists schemes safe to hand to the system browser.
// Pure + testable.
func isExternalOpenScheme(scheme string) bool {
	switch strings.ToLower(scheme) {
	case "http", "https", "mailto":
		return true
	default:
		return false
	}
}

// userinfoRe matches a URL's user:password@ component so credentials embedded in
// a connection-string error don't ship in support bundles.
var userinfoRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)

var (
	// Covers URL query parameters, key/value DSNs, JSON-ish error fragments,
	// and provider-specific names without trying to parse untrusted free text as
	// one particular connection-string grammar.
	sensitiveAssignmentRe = regexp.MustCompile(`(?i)(\b(password|passwd|pwd|secret|client_secret|token|access[_-]?token|refresh[_-]?token|api[_-]?key|secret[_-]?access[_-]?key)\b["']?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s;,&}]+)`)
	bearerTokenRe         = regexp.MustCompile(`(?i)(\bbearer\s+)[a-z0-9._~+\-/]+=*`)
	authorizationRe       = regexp.MustCompile(`(?i)(\bauthorization\b["']?\s*[:=]\s*)[^\r\n,;]+`)
)

// homeRelativize replaces the user's home-dir prefix of a single path with "~"
// so diagnostics don't leak the local username in support attachments.
func homeRelativize(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.Join("~", rel)
		}
	}
	return path
}

// scrubText cleans a free-text field (e.g. startup_error): replaces any embedded
// home-dir prefix with "~" and redacts credential-bearing URL userinfo.
func scrubText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		s = strings.ReplaceAll(s, home, "~")
	}
	s = userinfoRe.ReplaceAllString(s, "$1***@")
	s = bearerTokenRe.ReplaceAllString(s, "$1***")
	s = authorizationRe.ReplaceAllString(s, "$1***")
	return sensitiveAssignmentRe.ReplaceAllString(s, "$1***")
}

// ExportDiagnostics returns a JSON diagnostics bundle (version, platform, app
// data/log paths, startup error) for support. Secrets and credentials are never
// included; paths are home-relativized and any URL userinfo is redacted.
func (a *App) ExportDiagnostics() (string, error) {
	build := buildinfo.Get()
	a.lifecycleMu.RLock()
	startupErr := a.startupErr
	server := a.server
	appDataPath := a.appDataPath
	logPath := a.logPath
	a.lifecycleMu.RUnlock()
	payload := map[string]any{
		"product":       "APEX Load Desktop",
		"version":       build.DisplayVersion,
		"commit":        build.Commit,
		"build_time":    build.BuildTime,
		"go_version":    build.GoVersion,
		"os":            runtime.GOOS,
		"arch":          runtime.GOARCH,
		"app_data_path": homeRelativize(appDataPath),
		"log_path":      homeRelativize(logPath),
		"startup_error": scrubText(startupErr),
	}
	if server != nil {
		payload["api_address"] = server.BoundAddr()
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
