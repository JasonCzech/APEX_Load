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

func TestBuildDynatraceNotificationPayload(t *testing.T) {
	event := NotificationEvent{
		Type:      "test_started",
		TestID:    "test-123",
		ProjectID: "project-7",
		TestName:  "Checkout Smoke",
		Status:    "RUNNING",
		Timestamp: time.Date(2026, time.May, 16, 12, 0, 0, 0, time.UTC),
	}
	payload := buildDynatraceNotificationPayload(NotificationWebhookSettings{
		EntitySelector: "type(SERVICE),entityName.equals(CheckoutService)",
		TimeoutMinutes: 45,
	}, event)

	if payload["eventType"] != "CUSTOM_INFO" {
		t.Fatalf("dynatrace eventType = %#v, want CUSTOM_INFO", payload["eventType"])
	}
	if payload["title"] != "Stormrunner Test Started: Checkout Smoke" {
		t.Fatalf("dynatrace title = %#v", payload["title"])
	}
	if payload["startTime"] != event.Timestamp.UnixMilli() {
		t.Fatalf("dynatrace startTime = %#v, want %d", payload["startTime"], event.Timestamp.UnixMilli())
	}
	if payload["entitySelector"] != "type(SERVICE),entityName.equals(CheckoutService)" {
		t.Fatalf("dynatrace entitySelector = %#v", payload["entitySelector"])
	}
	if payload["timeout"] != 45 {
		t.Fatalf("dynatrace timeout = %#v, want 45", payload["timeout"])
	}
	properties, ok := payload["properties"].(map[string]string)
	if !ok {
		t.Fatalf("dynatrace properties = %#v, want string map", payload["properties"])
	}
	if properties["Tool"] != "Stormrunner" || properties["stormrunner.test_id"] != "test-123" || properties["stormrunner.status"] != "RUNNING" {
		t.Fatalf("dynatrace properties = %#v, want Stormrunner test metadata", properties)
	}
}

func TestBuildDynatraceNotificationPayloadUsesErrorEventForFailures(t *testing.T) {
	payload := buildDynatraceNotificationPayload(NotificationWebhookSettings{}, NotificationEvent{
		Type:      "test_failed",
		TestID:    "test-123",
		TestName:  "Checkout Smoke",
		Status:    "FAILED",
		Timestamp: time.Date(2026, time.May, 16, 12, 0, 0, 0, time.UTC),
	})
	if payload["eventType"] != "ERROR_EVENT" {
		t.Fatalf("dynatrace failure eventType = %#v, want ERROR_EVENT", payload["eventType"])
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

func TestBroadcastEventDispatchesDynatraceWebhookWithAPIToken(t *testing.T) {
	received := make(chan map[string]any, 1)
	headers := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		headers <- r.Header.Get("Authorization")
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode webhook payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- payload
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	runner := NewTestRunner(2, NewHub(), nil)
	runner.notifier = newNotificationDispatcher(server.Client())
	settings := runner.GetSystemSettings()
	settings.Notifications = NotificationsSettings{
		Webhooks: []NotificationWebhookSettings{{
			Name:           "Dynatrace",
			Enabled:        true,
			URL:            server.URL,
			Format:         notificationFormatDynatrace,
			Events:         []string{"test_started"},
			APIToken:       "dt-token",
			EntitySelector: "type(SERVICE),entityName.equals(CheckoutService)",
			TimeoutMinutes: 30,
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
	case auth := <-headers:
		if auth != "Api-Token dt-token" {
			t.Fatalf("Authorization header = %q, want Dynatrace API token", auth)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected webhook authorization header")
	}
	select {
	case payload := <-received:
		if payload["eventType"] != "CUSTOM_INFO" {
			t.Fatalf("payload eventType = %#v, want CUSTOM_INFO", payload["eventType"])
		}
		if payload["entitySelector"] != "type(SERVICE),entityName.equals(CheckoutService)" {
			t.Fatalf("payload entitySelector = %#v", payload["entitySelector"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected webhook delivery")
	}
}
