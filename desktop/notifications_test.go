package main

import "testing"

// TestNotifyRequiresRuntime verifies Notify fails closed before the Wails
// runtime context is available (rather than panicking).
func TestNotifyRequiresRuntime(t *testing.T) {
	app := NewApp()
	if err := app.Notify("Test complete", "order-flow finished"); err == nil {
		t.Error("expected error when desktop runtime is not ready")
	}
}

func TestNotificationsAvailableWithoutRuntime(t *testing.T) {
	app := NewApp()
	if app.NotificationsAvailable() {
		t.Error("NotificationsAvailable should be false without a runtime context")
	}
}
