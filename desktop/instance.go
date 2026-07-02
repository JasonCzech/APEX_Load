package main

import (
	"net/url"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/options"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// singleInstanceUniqueID is the OS-wide lock + messaging id. Must be stable and
// app-specific so a second launch is detected and routed to the first instance
// (protecting the shared SQLite handle from two writers).
const singleInstanceUniqueID = "com.projectstormrunner.apexload"

// apexloadURLScheme is the custom protocol registered for deep links.
const apexloadURLScheme = "apexload"

// openableFileExtensions are the file types the desktop app accepts via
// file-association / drag-open. Treated as untrusted input: only known import
// types are forwarded to the dashboard, and they are never auto-executed.
var openableFileExtensions = map[string]bool{
	".json":                     true, // JSON DSL script / import draft
	".zip":                      true, // enterprise script archive
	".har":                      true, // recording capture
	".jmx":                      true, // JMeter test plan
	".xml":                      true, // JMeter/XML-backed import formats
	".wsdl":                     true, // SOAP/WSDL import
	".yaml":                     true, // OpenAPI/RAML/AsyncAPI
	".yml":                      true, // OpenAPI/RAML/AsyncAPI
	".raml":                     true, // RAML import
	".js":                       true, // DevWeb/k6 JavaScript import
	".devweb":                   true, // DevWeb archive/source import
	".postman_collection.json":  true, // Postman collection
	".postman_environment.json": true, // Postman environment
	".asyncapi.json":            true, // AsyncAPI JSON
	".asyncapi.yaml":            true, // AsyncAPI YAML
	".asyncapi.yml":             true, // AsyncAPI YAML
	".openapi.json":             true, // OpenAPI JSON
	".openapi.yaml":             true, // OpenAPI YAML
	".openapi.yml":              true, // OpenAPI YAML
	".curl":                     true, // curl sample
}

// importReadableFileExtensions are additional sample/input file types that may
// be read after the renderer receives an explicit native drop path. They are
// intentionally not registered as OS open-with associations.
var importReadableFileExtensions = map[string]bool{
	".http": true, // raw HTTP sample
	".txt":  true, // raw/curl/sample response
	".sh":   true, // shell/curl sample
}

// Events emitted to the dashboard when the OS asks us to open a file/URL (via
// file association, custom scheme, or a second-instance launch).
const (
	eventOpenFile = "desktop:open-file" // payload: absolute file path
	eventOpenURL  = "desktop:open-url"  // payload: apexload:// URL
)

// isOpenableFile reports whether a path is a supported open-with target. Pure.
func isOpenableFile(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	lower := strings.ToLower(path)
	for ext := range openableFileExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func isImportableFile(path string) bool {
	if isOpenableFile(path) {
		return true
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	lower := strings.ToLower(path)
	for ext := range importReadableFileExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// isApexloadURL reports whether a string is a well-formed apexload:// URL. Pure.
func isApexloadURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, apexloadURLScheme)
}

// classifyOpenArgs splits process args into openable file paths and apexload
// URLs, ignoring anything else (flags, the executable path, etc.). Pure +
// unit-testable; used for the second-instance open-with handoff on
// Windows/Linux (macOS uses the dedicated OnFileOpen/OnUrlOpen callbacks).
func classifyOpenArgs(args []string) (files, urls []string) {
	for _, arg := range args {
		switch {
		case isApexloadURL(arg):
			urls = append(urls, strings.TrimSpace(arg))
		case isOpenableFile(arg):
			files = append(files, strings.TrimSpace(arg))
		}
	}
	return files, urls
}

// onSecondInstanceLaunch is invoked in the FIRST instance when a second launch
// is attempted. It raises/focuses the existing window and forwards any
// openable file/URL args to the dashboard, so the single SQLite-owning process
// handles the request instead of starting a competing one.
func (a *App) onSecondInstanceLaunch(data options.SecondInstanceData) {
	if a.ctx == nil {
		return
	}
	wailsruntime.WindowUnminimise(a.ctx)
	wailsruntime.WindowShow(a.ctx)

	files, urls := classifyOpenArgs(data.Args)
	for _, f := range files {
		a.handleOpenFile(f)
	}
	for _, u := range urls {
		a.handleOpenURL(u)
	}
}

// handleOpenFile forwards a validated open-with file path to the dashboard.
// Wired to macOS OnFileOpen and the second-instance handoff.
func (a *App) handleOpenFile(path string) {
	if a.ctx == nil || !isOpenableFile(path) {
		return
	}
	a.rememberReadPath(path)
	wailsruntime.EventsEmit(a.ctx, eventOpenFile, strings.TrimSpace(path))
}

// handleOpenURL forwards a validated apexload:// deep link to the dashboard.
func (a *App) handleOpenURL(raw string) {
	if a.ctx == nil || !isApexloadURL(raw) {
		return
	}
	wailsruntime.EventsEmit(a.ctx, eventOpenURL, strings.TrimSpace(raw))
}
