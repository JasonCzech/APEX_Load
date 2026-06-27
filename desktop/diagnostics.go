package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"runtime"
	"strings"

	"github.com/jasonczech/apexload/pkg/buildinfo"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// OpenURL opens an external URL in the system browser (not the webview). The
// dashboard routes external links here so clicking them never replaces the SPA.
// Only http/https/mailto are allowed.
func (a *App) OpenURL(raw string) error {
	if a.ctx == nil {
		return fmt.Errorf("desktop runtime is not ready")
	}
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if !isExternalOpenScheme(u.Scheme) {
		return fmt.Errorf("refusing to open scheme %q", u.Scheme)
	}
	wailsruntime.BrowserOpenURL(a.ctx, raw)
	return nil
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

// ExportDiagnostics returns a JSON diagnostics bundle (version, platform, app
// data/log paths, startup error) for support. Secrets and credentials are never
// included.
func (a *App) ExportDiagnostics() (string, error) {
	build := buildinfo.Get()
	payload := map[string]any{
		"product":       "APEX Load Desktop",
		"version":       build.DisplayVersion,
		"commit":        build.Commit,
		"build_time":    build.BuildTime,
		"go_version":    build.GoVersion,
		"os":            runtime.GOOS,
		"arch":          runtime.GOARCH,
		"app_data_path": a.appDataPath,
		"log_path":      a.logPath,
		"startup_error": a.startupErr,
	}
	if a.server != nil {
		payload["api_address"] = a.server.BoundAddr()
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
