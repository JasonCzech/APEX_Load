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

// defaultCloseRequestAckWait bounds how long a held close waits for the
// renderer to take the request. A page that is reloading or hung must never
// stop the app from quitting.
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
	a.closeGuardMu.Unlock()
}

// AcknowledgeCloseRequest records that the renderer took a close request, so
// the held close waits for the person's answer instead of quitting.
func (a *App) AcknowledgeCloseRequest(token, requestID string) {
	id, ok := parseCloseRequestID(requestID)
	if !ok {
		return
	}
	a.closeGuardMu.Lock()
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
	a.closeGuardMu.Lock()
	if a.closeConfirmed || a.closeGuardToken == "" {
		a.closeGuardMu.Unlock()
		return false
	}
	a.closeRequests++
	id := a.closeRequests
	a.closeGuardMu.Unlock()

	// Wails may call OnBeforeClose on the UI thread (Windows); publishing an
	// event or quitting from it could wait on that same thread.
	go a.emitCloseRequest(ctx, strconv.FormatUint(id, 10))
	go a.quitIfCloseRequestUnacknowledged(ctx, id)
	return true
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
