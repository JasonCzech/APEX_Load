package main

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// notifyCounter sequences notification IDs within a launch; notifyLaunchToken
// makes them unique ACROSS launches so a new session's notification can't
// coalesce-replace a still-visible one from a prior session (macOS replaces a
// delivered notification sharing the same identifier).
var (
	notifyCounter                    atomic.Uint64
	notifyLaunchToken                = strconv.FormatInt(time.Now().UnixNano(), 36)
	initializeNotifications          = wailsruntime.InitializeNotifications
	notificationAvailable            = wailsruntime.IsNotificationAvailable
	requestNotificationAuthorization = wailsruntime.RequestNotificationAuthorization
	sendNotification                 = wailsruntime.SendNotification
	cleanupNotificationRuntime       = wailsruntime.CleanupNotifications
)

const (
	notificationClosingError            = "desktop notifications are shutting down"
	defaultNotificationCleanupTimeout   = 250 * time.Millisecond
	notificationCleanupLockPollInterval = 5 * time.Millisecond
)

// initNotifications prepares the OS notification channel without prompting.
// macOS authorization is deliberately deferred until the first Notify bridge
// call: the native permission API may block for minutes while the prompt is
// unanswered, and startup readiness must never depend on that user decision.
func (a *App) initNotifications() {
	if a.notificationClosing.Load() {
		return
	}
	a.notificationMu.Lock()
	defer a.notificationMu.Unlock()
	if a.notificationClosing.Load() {
		return
	}
	ctx := a.runtimeContext()
	if ctx == nil {
		return
	}
	if err := initializeNotifications(ctx); err != nil {
		a.notificationInitialized = false
		a.notificationInitErr = fmt.Errorf("initialize desktop notifications: %w", err)
		return
	}
	a.notificationInitialized = true
	a.notificationInitErr = nil
}

// cleanupNotifications releases notification resources (needed on Linux to
// close the D-Bus connection). Authorization can wait minutes for a macOS
// permission prompt, so process shutdown only waits a bounded interval for an
// in-flight notification operation. Skipping cleanup at process exit is safe:
// the OS reclaims the backend connection, and closing prevents any later send.
func (a *App) cleanupNotifications(ctx context.Context) {
	// Publish the closing state before waiting for an in-flight operation. This
	// prevents a new bridge call from slipping in ahead of cleanup while the
	// current send or authorization request finishes.
	a.notificationClosing.Store(true)
	if ctx == nil {
		ctx = context.Background()
	}
	wait := a.notificationCleanupWait
	if wait <= 0 {
		wait = defaultNotificationCleanupTimeout
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	ticker := time.NewTicker(notificationCleanupLockPollInterval)
	defer ticker.Stop()
	for !a.notificationMu.TryLock() {
		select {
		case <-cleanupCtx.Done():
			slog.Warn("notification backend still busy; skipping cleanup during process exit")
			return
		case <-ticker.C:
		}
	}
	defer a.notificationMu.Unlock()
	runtimeCtx := a.runtimeContext()
	if runtimeCtx == nil {
		return
	}
	cleanupNotificationRuntime(runtimeCtx)
}

// NotificationsAvailable reports whether the OS can deliver notifications. The
// dashboard gates desktop-notification UI on this (e.g. unsigned dev builds on
// macOS cannot deliver, so it falls back to the web Notification API).
func (a *App) NotificationsAvailable() bool {
	if a.notificationClosing.Load() {
		return false
	}
	a.notificationMu.Lock()
	defer a.notificationMu.Unlock()
	if a.notificationClosing.Load() {
		return false
	}
	ctx := a.runtimeContext()
	if ctx == nil {
		return false
	}
	if !a.notificationInitialized {
		return false
	}
	return notificationAvailable(ctx)
}

// Notify sends a native OS notification. Called from the dashboard on long-
// running test outcomes (complete/failed/aborted, shakeout failures).
func (a *App) Notify(title, body string) error {
	if a.notificationClosing.Load() {
		return fmt.Errorf("%s", notificationClosingError)
	}
	a.notificationMu.Lock()
	defer a.notificationMu.Unlock()
	if a.notificationClosing.Load() {
		return fmt.Errorf("%s", notificationClosingError)
	}
	ctx := a.runtimeContext()
	if ctx == nil {
		return fmt.Errorf("desktop runtime is not ready")
	}
	if !a.notificationInitialized {
		if a.notificationInitErr != nil {
			return a.notificationInitErr
		}
		return fmt.Errorf("desktop notifications are not initialized")
	}
	if !notificationAvailable(ctx) {
		return fmt.Errorf("desktop notifications are not available")
	}
	a.notificationAuthOnce.Do(func() {
		granted, err := requestNotificationAuthorization(ctx)
		if err != nil {
			a.notificationAuthErr = fmt.Errorf("request notification authorization: %w", err)
			return
		}
		if !granted {
			a.notificationAuthErr = fmt.Errorf("notification authorization was not granted")
		}
	})
	// Shutdown may have begun while the native authorization prompt was open.
	// Do not send after cleanup has either run or deliberately timed out.
	if a.notificationClosing.Load() {
		return fmt.Errorf("%s", notificationClosingError)
	}
	if a.notificationAuthErr != nil {
		return a.notificationAuthErr
	}
	id := notifyLaunchToken + "-" + strconv.FormatUint(notifyCounter.Add(1), 10)
	return sendNotification(ctx, wailsruntime.NotificationOptions{
		ID:    "apexload-" + id,
		Title: title,
		Body:  body,
	})
}
