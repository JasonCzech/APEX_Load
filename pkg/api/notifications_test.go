package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBuildSlackNotificationPayload(t *testing.T) {
	event := NotificationEvent{
		Type:      "test_failed",
		TestID:    "test-123",
		ProjectID: "project-7",
		TestName:  "Checkout Smoke",
		Status:    "FAILED",
		Timestamp: time.Date(2026, time.May, 16, 12, 0, 0, 0, time.UTC),
	}
	payload := buildSlackNotificationPayload(event)
	if payload["text"] != "Stormrunner Test Failed: Checkout Smoke (FAILED)" {
		t.Fatalf("slack text = %#v", payload["text"])
	}
	blocks, ok := payload["blocks"].([]map[string]any)
	if !ok || len(blocks) != 3 {
		t.Fatalf("slack blocks = %#v, want 3 blocks", payload["blocks"])
	}
}

func TestBuildTeamsNotificationPayload(t *testing.T) {
	event := NotificationEvent{
		Type:      "test_complete",
		TestID:    "test-123",
		ProjectID: "project-7",
		TestName:  "Checkout Smoke",
		Status:    "COMPLETE",
		Timestamp: time.Date(2026, time.May, 16, 12, 0, 0, 0, time.UTC),
	}
	payload := buildTeamsNotificationPayload(event)
	attachments, ok := payload["attachments"].([]map[string]any)
	if !ok || len(attachments) != 1 {
		t.Fatalf("teams attachments = %#v, want 1 attachment", payload["attachments"])
	}
	if attachments[0]["contentType"] != "application/vnd.microsoft.card.adaptive" {
		t.Fatalf("teams content type = %#v, want adaptive card", attachments[0]["contentType"])
	}
}

func TestBroadcastEventDispatchesConfiguredNotificationWebhook(t *testing.T) {
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode webhook payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	runner := NewTestRunner(2, NewHub(), nil)
	runner.notifier = newNotificationDispatcher(server.Client())
	settings := runner.GetSystemSettings()
	settings.Notifications = NotificationsSettings{
		Webhooks: []NotificationWebhookSettings{{
			Name:    "Slack Alerts",
			Enabled: true,
			URL:     server.URL,
			Format:  notificationFormatSlack,
			Events:  []string{"test_started"},
		}},
	}
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	runner.broadcastEvent("test_started", &ManagedTest{
		ID:        "test-123",
		Name:      "Checkout Smoke",
		Status:    "RUNNING",
		ProjectID: "project-7",
	})

	select {
	case payload := <-received:
		if payload["text"] != "Stormrunner Test Started: Checkout Smoke (RUNNING)" {
			t.Fatalf("payload text = %#v", payload["text"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected webhook delivery")
	}
}
