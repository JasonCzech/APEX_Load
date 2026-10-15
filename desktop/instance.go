package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	".usz":                      true, // VuGen script archive
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
	".bru":  true, // Bruno request file
	".tar":  true, // collection archive
	".tgz":  true, // collection archive
	".gz":   true, // collection archive (.tar.gz)
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
	return classifyOpenArgsFromDir(args, "")
}

// classifyOpenArgsFromDir resolves relative file-association arguments against
// the launching process's working directory. The first instance may have a
// different cwd, so resolving against its own cwd would open the wrong file.
func classifyOpenArgsFromDir(args []string, workingDirectory string) (files, urls []string) {
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		switch {
		case strings.HasPrefix(strings.ToLower(arg), "file:"):
			// Linux desktop handlers use %U because the same entry accepts both
			// apexload:// links and HAR files. Decode only local, absolute file URLs;
			// never reinterpret a remote authority as a local/UNC path.
			path, ok := localFileURLPath(arg, runtime.GOOS)
			if !ok || !isOpenableFile(path) {
				continue
			}
			files = append(files, cleanBridgePath(path))
		case isApexloadURL(arg):
			urls = append(urls, arg)
		case isOpenableFile(arg):
			if !filepath.IsAbs(arg) && strings.TrimSpace(workingDirectory) != "" {
				arg = filepath.Join(workingDirectory, arg)
			}
			if abs, err := filepath.Abs(arg); err == nil {
				arg = abs
			}
			files = append(files, filepath.Clean(arg))
		}
	}
	return files, urls
}

func localFileURLPath(raw, goos string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "file") || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return "", false
	}
	path := u.Path
	if path == "" || strings.ContainsRune(path, 0) {
		return "", false
	}
	if goos == "windows" {
		// RFC 8089 drive paths use /C:/...; strip only that drive prefix and
		// reject UNC-like paths, whose remote authority may be obscured.
		if len(path) >= 3 && path[0] == '/' && isASCIILetter(path[1]) && path[2] == ':' {
			path = path[1:]
		}
		if len(path) < 3 || !isASCIILetter(path[0]) || path[1] != ':' || path[2] != '/' {
			return "", false
		}
		return strings.ReplaceAll(path, "/", `\`), true
	}
	path = filepath.FromSlash(path)
	if !filepath.IsAbs(path) {
		return "", false
	}
	return filepath.Clean(path), true
}

func isASCIILetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func (a *App) captureInitialOpenRequests() {
	// macOS delivers cold-launch activations through OnFileOpen/OnUrlOpen. On
	// Windows and Linux they arrive as ordinary process arguments.
	if runtime.GOOS == "darwin" {
		return
	}
	cwd, _ := os.Getwd()
	files, urls := classifyOpenArgsFromDir(os.Args[1:], cwd)
	for _, file := range files {
		a.handleOpenFile(file)
	}
	for _, rawURL := range urls {
		a.handleOpenURL(rawURL)
	}
}

// onSecondInstanceLaunch is invoked in the FIRST instance when a second launch
// is attempted. It raises/focuses the existing window and forwards any
// openable file/URL args to the dashboard, so the single SQLite-owning process
// handles the request instead of starting a competing one.
func (a *App) onSecondInstanceLaunch(data options.SecondInstanceData) {
	a.openMu.Lock()
	ctx := a.ctx
	a.openMu.Unlock()
	if ctx != nil {
		wailsruntime.WindowUnminimise(ctx)
		wailsruntime.WindowShow(ctx)
	}

	files, urls := classifyOpenArgsFromDir(data.Args, data.WorkingDirectory)
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
	if !isOpenableFile(path) {
		return
	}
	path = cleanBridgePath(path)
	a.rememberReadPath(path)
	a.openMu.Lock()
	ctx := a.ctx
	if !a.rendererReady || ctx == nil {
		a.queueRendererEventLocked(eventOpenFile, path, true)
		a.openMu.Unlock()
		return
	}
	// Keep readiness and event publication in one critical section. A reload
	// cannot mark the renderer unready between the check and EventsEmit and lose
	// this activation in the page being torn down.
	wailsruntime.EventsEmit(ctx, eventOpenFile, path)
	a.openMu.Unlock()
}

// handleOpenURL forwards a validated apexload:// deep link to the dashboard.
func (a *App) handleOpenURL(raw string) {
	if !isApexloadURL(raw) {
		return
	}
	raw = strings.TrimSpace(raw)
	a.openMu.Lock()
	ctx := a.ctx
	if !a.rendererReady || ctx == nil {
		a.queueRendererEventLocked(eventOpenURL, raw, true)
		a.openMu.Unlock()
		return
	}
	wailsruntime.EventsEmit(ctx, eventOpenURL, raw)
	a.openMu.Unlock()
}

type pendingOpenRequests struct {
	SessionAccepted bool                   `json:"session_accepted"`
	DeliveryID      string                 `json:"delivery_id,omitempty"`
	Events          []pendingRendererEvent `json:"events"`
}

type pendingRendererEvent struct {
	Name    string `json:"name"`
	Payload string `json:"payload,omitempty"`
}

type rendererDelivery struct {
	payload    pendingOpenRequests
	eventCount int
}

const maxPendingRendererEvents = 64

// emitOrQueueRendererEvent publishes a native menu intent only after the
// renderer has installed its listeners. Menu callbacks can run before OnStartup
// has supplied a runtime context, and during a reload after the old page has
// removed its listeners. Holding openMu across the readiness check and live
// publication gives menu events the same no-gap delivery guarantee as OS file
// and deep-link activations.
func (a *App) emitOrQueueRendererEvent(name, payload string) {
	a.openMu.Lock()
	ctx := a.ctx
	if !a.rendererReady || ctx == nil {
		a.queueRendererEventLocked(name, payload, false)
		a.openMu.Unlock()
		return
	}
	if payload == "" {
		wailsruntime.EventsEmit(ctx, name)
	} else {
		wailsruntime.EventsEmit(ctx, name, payload)
	}
	a.openMu.Unlock()
}

// queueRendererEventLocked appends every cold-start/reload intent to one
// ordered stream. File-open and deep-link callbacks may be duplicated by the OS
// and are coalesced within the not-yet-delivered tail; menu clicks are never
// coalesced. An in-flight prefix is immutable until acknowledged.
func (a *App) queueRendererEventLocked(name, payload string, deduplicate bool) {
	protected := 0
	if a.pendingDelivery != nil {
		protected = a.pendingDelivery.eventCount
	}
	if deduplicate {
		for _, event := range a.pendingEvents[protected:] {
			if event.Name == name && event.Payload == payload {
				return
			}
		}
	}
	if len(a.pendingEvents)-protected >= maxPendingRendererEvents {
		copy(a.pendingEvents[protected:], a.pendingEvents[protected+1:])
		a.pendingEvents = a.pendingEvents[:len(a.pendingEvents)-1]
	}
	a.pendingEvents = append(a.pendingEvents, pendingRendererEvent{Name: name, Payload: payload})
}

// RendererReady is the renderer's activation handshake. The frontend must
// register desktop/menu listeners first, then replay and acknowledge each
// returned batch. The backend remains unready during replay, so newer native
// intents stay queued behind the batch instead of overtaking it on the live
// event bus. Unacknowledged batches are retained and returned verbatim after a
// bridge failure or renderer reload.
func (a *App) RendererReady(rendererSession string) string {
	a.openMu.Lock()
	if !a.activateRendererSessionLocked(rendererSession) {
		a.openMu.Unlock()
		return marshalPendingOpenRequests(rejectedPendingOpenRequests())
	}
	pending := a.nextRendererDeliveryLocked()
	a.openMu.Unlock()
	return marshalPendingOpenRequests(pending)
}

// RendererReadyAck commits one replayed activation batch and returns the next
// batch atomically. Repeating an acknowledgement is safe: when its batch was
// already committed, the currently in-flight batch (or an empty ready response)
// is returned without deleting anything else.
func (a *App) RendererReadyAck(rendererSession, deliveryID string) string {
	a.openMu.Lock()
	if rendererSession != a.rendererSession || !validRendererSessionID(rendererSession) {
		a.openMu.Unlock()
		return marshalPendingOpenRequests(rejectedPendingOpenRequests())
	}
	if delivery := a.pendingDelivery; delivery != nil && delivery.payload.DeliveryID == deliveryID {
		a.pendingEvents = a.pendingEvents[delivery.eventCount:]
		a.pendingDelivery = nil
	}
	pending := a.nextRendererDeliveryLocked()
	a.openMu.Unlock()
	return marshalPendingOpenRequests(pending)
}

func (a *App) nextRendererDeliveryLocked() pendingOpenRequests {
	if a.pendingDelivery != nil {
		return a.pendingDelivery.payload
	}
	if len(a.pendingEvents) == 0 {
		a.rendererReady = true
		return emptyPendingOpenRequests()
	}

	a.rendererReady = false
	a.nextDeliveryID++
	pending := pendingOpenRequests{
		SessionAccepted: true,
		DeliveryID:      strconv.FormatUint(a.nextDeliveryID, 10),
		Events:          append([]pendingRendererEvent(nil), a.pendingEvents...),
	}
	a.pendingDelivery = &rendererDelivery{
		payload:    pending,
		eventCount: len(pending.Events),
	}
	return pending
}

func emptyPendingOpenRequests() pendingOpenRequests {
	return pendingOpenRequests{
		SessionAccepted: true,
		Events:          []pendingRendererEvent{},
	}
}

func rejectedPendingOpenRequests() pendingOpenRequests {
	return pendingOpenRequests{SessionAccepted: false, Events: []pendingRendererEvent{}}
}

func marshalPendingOpenRequests(pending pendingOpenRequests) string {
	if pending.Events == nil {
		pending.Events = []pendingRendererEvent{}
	}
	data, _ := json.Marshal(pending) // strings and slices cannot fail to marshal
	return string(data)
}

// RendererUnready closes the activation delivery window before a renderer
// reload. While false, open-file/deep-link activations are queued for the next
// RendererReady handshake instead of being emitted into a page that is tearing
// down and has already removed its listeners.
func (a *App) RendererUnready(rendererSession string) {
	a.openMu.Lock()
	if validRendererSessionID(rendererSession) {
		a.retireRendererSessionLocked(rendererSession)
		if rendererSession == a.rendererSession {
			a.rendererReady = false
			a.rendererSession = ""
		}
	}
	a.openMu.Unlock()
}

func (a *App) rendererUnreadyCurrent() {
	a.openMu.Lock()
	if validRendererSessionID(a.rendererSession) {
		a.retireRendererSessionLocked(a.rendererSession)
	}
	a.rendererReady = false
	a.rendererSession = ""
	a.openMu.Unlock()
}

const maxRetiredRendererSessions = 128

func validRendererSessionID(session string) bool {
	if len(session) < 8 || len(session) > 128 {
		return false
	}
	for _, char := range session {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func (a *App) activateRendererSessionLocked(session string) bool {
	if !validRendererSessionID(session) {
		return false
	}
	if _, retired := a.retiredSessions[session]; retired {
		return false
	}
	if a.rendererSession == "" {
		a.rendererSession = session
		return true
	}
	if a.rendererSession != session {
		// A new renderer can race the old page's asynchronous pagehide bridge
		// call on macOS/Linux. Let the new session atomically supersede the old one;
		// retiring the prior token makes every late Ready/Ack/Unready harmless.
		a.retireRendererSessionLocked(a.rendererSession)
		a.rendererSession = session
		a.rendererReady = false
	}
	return true
}

func (a *App) retireRendererSessionLocked(session string) {
	if _, exists := a.retiredSessions[session]; exists {
		return
	}
	a.retiredSessions[session] = struct{}{}
	a.retiredSessionFIFO = append(a.retiredSessionFIFO, session)
	if len(a.retiredSessionFIFO) > maxRetiredRendererSessions {
		oldest := a.retiredSessionFIFO[0]
		a.retiredSessionFIFO = a.retiredSessionFIFO[1:]
		delete(a.retiredSessions, oldest)
	}
}
