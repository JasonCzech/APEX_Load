package main

import (
	"fmt"
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
	notifyCounter     atomic.Uint64
	notifyLaunchToken = strconv.FormatInt(time.Now().UnixNano(), 36)
)

// initNotifications prepares the OS notification channel. On macOS this opens the
// notification center connection; on Linux it sets up D-Bus. It best-effort
// requests authorization (a no-op grant on Windows/Linux). Safe to call once at
// startup; failures are non-fatal (notifications simply stay unavailable).
func (a *App) initNotifications() {
	if a.ctx == nil {
		return
	}
	if err := wailsruntime.InitializeNotifications(a.ctx); err != nil {
		return
	}
	if !wailsruntime.IsNotificationAvailable(a.ctx) {
		return
	}
	// Authorization prompt is macOS-only; ignore the result (we feature-detect at
	// send time via NotificationsAvailable).
	_, _ = wailsruntime.RequestNotificationAuthorization(a.ctx)
}

// cleanupNotifications releases notification resources (needed on Linux to close
// the D-Bus connection).
func (a *App) cleanupNotifications() {
	if a.ctx == nil {
		return
	}
	wailsruntime.CleanupNotifications(a.ctx)
}

// NotificationsAvailable reports whether the OS can deliver notifications. The
// dashboard gates desktop-notification UI on this (e.g. unsigned dev builds on
// macOS cannot deliver, so it falls back to the web Notification API).
func (a *App) NotificationsAvailable() bool {
	if a.ctx == nil {
		return false
	}
	return wailsruntime.IsNotificationAvailable(a.ctx)
}

// Notify sends a native OS notification. Called from the dashboard on long-
// running test outcomes (complete/failed/aborted, shakeout failures).
func (a *App) Notify(title, body string) error {
	if a.ctx == nil {
		return fmt.Errorf("desktop runtime is not ready")
	}
	id := notifyLaunchToken + "-" + strconv.FormatUint(notifyCounter.Add(1), 10)
	return wailsruntime.SendNotification(a.ctx, wailsruntime.NotificationOptions{
		ID:    "apexload-" + id,
		Title: title,
		Body:  body,
	})
}
