package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// A native close (the window's close button, Cmd+Q, File > Quit) never reaches
// the page, so the dashboard's unsaved-changes prompt cannot run. While a
// renderer has registered its close guard, the shell holds the close and asks
// the renderer instead: the page answers through its own prompt (save, discard
// or keep editing) and confirms the quit when the work is resolved.
const eventCloseRequested = "desktop:close-requested" // payload: request id

// View > Reload replaces the page the same way, so it asks the same guard.
const eventReloadRequested = "desktop:reload-requested" // payload: request id

// defaultCloseRequestAckWait bounds how long a held close, or a View > Reload,
// waits for the renderer to take its request. A page that is reloading or hung
// must never stop the app from quitting, and a reload goes ahead after it too.
const defaultCloseRequestAckWait = 3 * time.Second

// EnableCloseGuard registers the mounted renderer's close guard and returns
// the token its later calls carry. A newer registration replaces an older one.
func (a *App) EnableCloseGuard() (string, error) {
	token, err := newDesktopCapability()
	if err != nil {
		return "", err
	}
	a.closeGuardMu.Lock()
	a.closeGuardToken = token
	a.closeGuardSetAside = ""
	a.closeGuardMu.Unlock()
	return token, nil
}

// DisableCloseGuard removes the guard registered with token, if it is still the
// current one.
func (a *App) DisableCloseGuard(token string) {
	a.closeGuardMu.Lock()
	if token != "" && token == a.closeGuardToken {
		a.closeGuardToken = ""
	}
	if token != "" && token == a.closeGuardSetAside {
		a.closeGuardSetAside = ""
	}
	a.closeGuardMu.Unlock()
}

// AcknowledgeCloseRequest records that the renderer took a close or reload
// request, so the shell waits for the person's answer instead of going ahead.
func (a *App) AcknowledgeCloseRequest(token, requestID string) {
	id, ok := parseCloseRequestID(requestID)
	if !ok {
		return
	}
	a.closeGuardMu.Lock()
	// A page that answers a reload it let pass was only busy. If it is still
	// here (its browser kept it at a leave-page prompt, say), it holds closes
	// again, unless a newer page has registered since.
	if token != "" && token == a.closeGuardSetAside && a.closeGuardToken == "" && id <= a.closeRequests {
		a.closeGuardToken = token
		a.closeGuardSetAside = ""
	}
	if token != "" && token == a.closeGuardToken && id <= a.closeRequests && id > a.closeRequestsAcked {
		a.closeRequestsAcked = id
	}
	a.closeGuardMu.Unlock()
}

// ConfirmClose quits after the renderer resolved its unsaved work for a close
// request it was sent.
func (a *App) ConfirmClose(token, requestID string) error {
	id, ok := parseCloseRequestID(requestID)
	a.closeGuardMu.Lock()
	if !ok || token == "" || token != a.closeGuardToken || id > a.closeRequests {
		a.closeGuardMu.Unlock()
		return fmt.Errorf("the close request is no longer current")
	}
	a.closeConfirmed = true
	a.closeGuardMu.Unlock()
	if ctx := a.runtimeContext(); ctx != nil {
		a.quitApp(ctx)
	}
	return nil
}

// holdCloseForRenderer reports whether a native close must wait for the
// renderer. It sends the renderer a close request and, should the renderer not
// take it in time, quits anyway. A reloaded page's registration simply
// replaces the old one; a page that went away without registering again (a
// reload into sign-in) costs one timed-out close, after which its stale
// registration is dropped.
func (a *App) holdCloseForRenderer(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	id, _, held := a.beginRendererRequest()
	if !held {
		return false
	}
	// Wails may call OnBeforeClose on the UI thread (Windows); publishing an
	// event or quitting from it could wait on that same thread.
	go a.emitCloseRequest(ctx, strconv.FormatUint(id, 10))
	go a.quitIfCloseRequestUnacknowledged(ctx, id)
	return true
}

// beginRendererRequest numbers a close or reload request for the renderer's
// guard and returns the registration it goes to. Without a registered guard
// (or once a quit is confirmed) there is no one to ask, and the action goes
// ahead.
func (a *App) beginRendererRequest() (uint64, string, bool) {
	a.closeGuardMu.Lock()
	defer a.closeGuardMu.Unlock()
	if a.closeConfirmed || a.closeGuardToken == "" {
		return 0, "", false
	}
	a.closeRequests++
	return a.closeRequests, a.closeGuardToken, true
}

// reloadWithRendererGuard reloads the page, asking the renderer first when its
// guard is registered: the page answers through its unsaved-work prompt and
// reloads itself. A page that does not take the request within the wait (one
// still loading, or one whose guard is no longer listening) is reloaded
// anyway. That reload runs as script in the page, since Wails has no native
// reload: a page whose script is stuck cannot run it, and a page busy past the
// wait runs it straight after the prompt the late request opens.
func (a *App) reloadWithRendererGuard(ctx context.Context) {
	if ctx == nil {
		return
	}
	id, token, held := a.beginRendererRequest()
	if !held {
		windowReloadApp(ctx)
		return
	}
	go a.emitReloadRequest(ctx, strconv.FormatUint(id, 10))
	go func() {
		if !a.rendererTookRequest(id, token) {
			windowReloadApp(ctx)
		}
	}()
}

// rendererTookRequest waits the acknowledgement window and reports whether the
// renderer took request id (or a later one). A registration that let the
// request pass unanswered is set aside, as a timed-out close drops it, so the
// next close or reload does not wait on it; its page restores it by answering
// late (see AcknowledgeCloseRequest). A newer page's registration is kept.
func (a *App) rendererTookRequest(id uint64, token string) bool {
	wait := a.closeAckWait
	if wait <= 0 {
		wait = defaultCloseRequestAckWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	<-timer.C
	a.closeGuardMu.Lock()
	defer a.closeGuardMu.Unlock()
	if a.closeRequestsAcked >= id {
		return true
	}
	if a.closeGuardToken == token {
		a.closeGuardToken = ""
		a.closeGuardSetAside = token
	}
	return false
}

func (a *App) quitIfCloseRequestUnacknowledged(ctx context.Context, id uint64) {
	wait := a.closeAckWait
	if wait <= 0 {
		wait = defaultCloseRequestAckWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	<-timer.C
	a.closeGuardMu.Lock()
	// A later request the page took (a close, or a reload) covers this one:
	// the page is alive and may be asking about unsaved work, which quitting
	// under it would discard. A close it never saw is then pressed again.
	if a.closeConfirmed || a.closeRequestsAcked >= id {
		a.closeGuardMu.Unlock()
		return
	}
	a.closeConfirmed = true
	a.closeGuardToken = ""
	a.closeGuardMu.Unlock()
	a.quitApp(ctx)
}

func parseCloseRequestID(raw string) (uint64, bool) {
	id, err := strconv.ParseUint(raw, 10, 64)
	return id, err == nil && id > 0
}

func emitCloseRequestEvent(ctx context.Context, requestID string) {
	wailsruntime.EventsEmit(ctx, eventCloseRequested, requestID)
}

func emitReloadRequestEvent(ctx context.Context, requestID string) {
	wailsruntime.EventsEmit(ctx, eventReloadRequested, requestID)
}
