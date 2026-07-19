package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// TestNotifyRequiresRuntime verifies Notify fails closed before the Wails
// runtime context is available (rather than panicking).
func TestNotifyRequiresRuntime(t *testing.T) {
	app := NewApp()
	if err := app.Notify("Test complete", "order-flow finished"); err == nil {
		t.Error("expected error when desktop runtime is not ready")
	}
}

func TestNotificationCleanupWaitsForSendAndRejectsNewCalls(t *testing.T) {
	originalRequest := requestNotificationAuthorization
	originalSend := sendNotification
	originalCleanup := cleanupNotificationRuntime
	originalAvailable := notificationAvailable
	t.Cleanup(func() {
		requestNotificationAuthorization = originalRequest
		sendNotification = originalSend
		cleanupNotificationRuntime = originalCleanup
		notificationAvailable = originalAvailable
	})

	notificationAvailable = func(context.Context) bool { return true }
	requestNotificationAuthorization = func(context.Context) (bool, error) { return true, nil }
	sendStarted := make(chan struct{})
	releaseSend := make(chan struct{})
	var sendCalls atomic.Int32
	sendNotification = func(context.Context, wailsruntime.NotificationOptions) error {
		sendCalls.Add(1)
		close(sendStarted)
		<-releaseSend
		return nil
	}
	cleanupCalled := make(chan struct{})
	cleanupNotificationRuntime = func(context.Context) {
		close(cleanupCalled)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.notificationInitialized = true
	notifyDone := make(chan error, 1)
	go func() { notifyDone <- app.Notify("Test complete", "finished") }()
	select {
	case <-sendStarted:
	case <-time.After(time.Second):
		t.Fatal("notification send did not start")
	}

	cleanupDone := make(chan struct{})
	go func() {
		app.cleanupNotifications(context.Background())
		close(cleanupDone)
	}()
	deadline := time.Now().Add(time.Second)
	for !app.notificationClosing.Load() {
		if time.Now().After(deadline) {
			t.Fatal("notification cleanup did not publish the closing state")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-cleanupCalled:
		t.Fatal("notification backend was cleaned up during an in-flight send")
	default:
	}

	if err := app.Notify("Late notification", "must be rejected"); err == nil || !strings.Contains(err.Error(), notificationClosingError) {
		t.Fatalf("Notify during shutdown error = %v", err)
	}
	if app.NotificationsAvailable() {
		t.Fatal("NotificationsAvailable reported true during shutdown")
	}

	close(releaseSend)
	if err := <-notifyDone; err != nil {
		t.Fatalf("in-flight Notify: %v", err)
	}
	select {
	case <-cleanupCalled:
	case <-time.After(time.Second):
		t.Fatal("notification backend cleanup did not follow the completed send")
	}
	select {
	case <-cleanupDone:
	case <-time.After(time.Second):
		t.Fatal("notification cleanup did not return")
	}
	if got := sendCalls.Load(); got != 1 {
		t.Fatalf("notification sends = %d, want 1", got)
	}
}

func TestNotificationCleanupDoesNotWaitForBlockedAuthorization(t *testing.T) {
	originalRequest := requestNotificationAuthorization
	originalSend := sendNotification
	originalCleanup := cleanupNotificationRuntime
	originalAvailable := notificationAvailable
	t.Cleanup(func() {
		requestNotificationAuthorization = originalRequest
		sendNotification = originalSend
		cleanupNotificationRuntime = originalCleanup
		notificationAvailable = originalAvailable
	})

	notificationAvailable = func(context.Context) bool { return true }
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	requestNotificationAuthorization = func(context.Context) (bool, error) {
		close(requestStarted)
		<-releaseRequest
		return true, nil
	}
	var sendCalls atomic.Int32
	sendNotification = func(context.Context, wailsruntime.NotificationOptions) error {
		sendCalls.Add(1)
		return nil
	}
	var cleanupCalls atomic.Int32
	cleanupNotificationRuntime = func(context.Context) {
		cleanupCalls.Add(1)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.notificationInitialized = true
	app.notificationCleanupWait = 20 * time.Millisecond
	notifyDone := make(chan error, 1)
	go func() { notifyDone <- app.Notify("Test complete", "finished") }()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("notification authorization did not start")
	}

	cleanupStarted := time.Now()
	app.cleanupNotifications(context.Background())
	if elapsed := time.Since(cleanupStarted); elapsed > 500*time.Millisecond {
		t.Fatalf("notification cleanup took %s with blocked authorization", elapsed)
	}
	if got := cleanupCalls.Load(); got != 0 {
		t.Fatalf("notification backend cleanup calls = %d, want 0 while authorization owns it", got)
	}
	if !app.notificationClosing.Load() {
		t.Fatal("notification cleanup did not publish closing state")
	}

	close(releaseRequest)
	select {
	case err := <-notifyDone:
		if err == nil || !strings.Contains(err.Error(), notificationClosingError) {
			t.Fatalf("Notify after shutdown began error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked notification authorization did not return after release")
	}
	if got := sendCalls.Load(); got != 0 {
		t.Fatalf("notification sends after shutdown = %d, want 0", got)
	}
}

func TestNotificationsAvailableWithoutRuntime(t *testing.T) {
	app := NewApp()
	if app.NotificationsAvailable() {
		t.Error("NotificationsAvailable should be false without a runtime context")
	}
}

func TestNotificationInitializationFailureDisablesBackend(t *testing.T) {
	originalInit := initializeNotifications
	originalAvailable := notificationAvailable
	originalRequest := requestNotificationAuthorization
	originalSend := sendNotification
	t.Cleanup(func() {
		initializeNotifications = originalInit
		notificationAvailable = originalAvailable
		requestNotificationAuthorization = originalRequest
		sendNotification = originalSend
	})

	initializeNotifications = func(context.Context) error { return errors.New("backend unavailable") }
	var availableCalls atomic.Int32
	notificationAvailable = func(context.Context) bool {
		availableCalls.Add(1)
		return true
	}
	var authorizationCalls atomic.Int32
	requestNotificationAuthorization = func(context.Context) (bool, error) {
		authorizationCalls.Add(1)
		return true, nil
	}
	var sendCalls atomic.Int32
	sendNotification = func(context.Context, wailsruntime.NotificationOptions) error {
		sendCalls.Add(1)
		return nil
	}

	app := NewApp()
	app.ctx = context.Background()
	app.initNotifications()
	if app.NotificationsAvailable() {
		t.Fatal("failed notification backend reported available")
	}
	if got := availableCalls.Load(); got != 0 {
		t.Fatalf("OS availability probes after initialization failure = %d, want 0", got)
	}
	if err := app.Notify("Test complete", "finished"); err == nil || !strings.Contains(err.Error(), "backend unavailable") {
		t.Fatalf("Notify after initialization failure error = %v", err)
	}
	if got := authorizationCalls.Load(); got != 0 {
		t.Fatalf("authorization requests after initialization failure = %d, want 0", got)
	}
	if got := sendCalls.Load(); got != 0 {
		t.Fatalf("notification sends after initialization failure = %d, want 0", got)
	}
}

func TestNotificationAuthorizationDoesNotBlockStartupInitialization(t *testing.T) {
	originalInit := initializeNotifications
	originalRequest := requestNotificationAuthorization
	originalSend := sendNotification
	originalAvailable := notificationAvailable
	t.Cleanup(func() {
		initializeNotifications = originalInit
		requestNotificationAuthorization = originalRequest
		sendNotification = originalSend
		notificationAvailable = originalAvailable
	})

	initializeNotifications = func(context.Context) error { return nil }
	notificationAvailable = func(context.Context) bool { return true }
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	requestNotificationAuthorization = func(context.Context) (bool, error) {
		close(requestStarted)
		<-releaseRequest
		return true, nil
	}
	sendNotification = func(context.Context, wailsruntime.NotificationOptions) error { return nil }

	app := NewApp()
	app.ctx = context.Background()
	initDone := make(chan struct{})
	go func() {
		app.initNotifications()
		close(initDone)
	}()
	select {
	case <-initDone:
	case <-time.After(time.Second):
		t.Fatal("notification initialization blocked desktop startup")
	}
	select {
	case <-requestStarted:
		t.Fatal("notification authorization was requested during startup")
	default:
	}

	notifyDone := make(chan error, 1)
	go func() { notifyDone <- app.Notify("Test complete", "finished") }()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("first notification did not request authorization lazily")
	}
	close(releaseRequest)
	if err := <-notifyDone; err != nil {
		t.Fatalf("Notify after authorization: %v", err)
	}
}
