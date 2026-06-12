package api

import (
	"bytes"
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
	if payload["text"] != "APEX Load Test Failed: Checkout Smoke (FAILED)" {
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
	if payload["title"] != "APEX Load Test Started: Checkout Smoke" {
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
	if properties["Tool"] != "APEX Load" || properties["apexload.test_id"] != "test-123" || properties["apexload.status"] != "RUNNING" {
		t.Fatalf("dynatrace properties = %#v, want APEX Load test metadata", properties)
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

func TestBuildTelemetryNotificationPayloadIncludesDiagnosticContext(t *testing.T) {
	event := NotificationEvent{
		Type:       "telemetry_breached",
		TestID:     "test-telemetry",
		ProjectID:  "project-7",
		TestName:   "Checkout Load",
		Status:     "breached",
		Severity:   "danger",
		Summary:    "collector dropped 3 metric samples",
		Conditions: []string{"collector_drops", "websocket_drops"},
		Timestamp:  time.Date(2026, time.May, 18, 14, 0, 0, 0, time.UTC),
	}

	slack := buildSlackNotificationPayload(event)
	blocks := slack["blocks"].([]map[string]any)
	fields := blocks[1]["fields"].([]map[string]any)
	if len(fields) < 7 {
		t.Fatalf("slack fields = %#v, want diagnostic context fields", fields)
	}

	dynatrace := buildDynatraceNotificationPayload(NotificationWebhookSettings{}, event)
	if dynatrace["eventType"] != "ERROR_EVENT" {
		t.Fatalf("dynatrace event type = %#v, want ERROR_EVENT", dynatrace["eventType"])
	}
	properties := dynatrace["properties"].(map[string]string)
	if properties["apexload.severity"] != "danger" || properties["apexload.summary"] != event.Summary {
		t.Fatalf("dynatrace diagnostic properties = %#v", properties)
	}
	if properties["apexload.conditions"] != "collector_drops,websocket_drops" {
		t.Fatalf("dynatrace conditions = %#v", properties["apexload.conditions"])
	}
}

func TestDynatraceDryRunBuildsPayloadWithoutDelivery(t *testing.T) {
	server := NewServer(ServerConfig{})
	body := bytes.NewBufferString(`{
		"test_id":"test-dt-dry-run",
		"event_type":"telemetry_breached",
		"status":"breached",
		"severity":"warning",
		"summary":"oracle wait time moved with P95 latency",
		"conditions":["oracle_db_waits","p95_latency_regression"],
		"entity_selector":"type(SERVICE),entityName.equals(CheckoutService)",
		"timeout_minutes":45
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/dynatrace/dry-run", body)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload DynatraceDryRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode dry-run response: %v", err)
	}
	if !payload.DeliveryDisabled {
		t.Fatalf("DeliveryDisabled = false, want true")
	}
	if payload.Payload["eventType"] != "ERROR_EVENT" || payload.Payload["entitySelector"] != "type(SERVICE),entityName.equals(CheckoutService)" || payload.Payload["timeout"] != float64(45) {
		t.Fatalf("dry-run payload = %#v, want Dynatrace telemetry breach event", payload.Payload)
	}
	properties, ok := payload.Payload["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want object", payload.Payload["properties"])
	}
	if properties["apexload.conditions"] != "oracle_db_waits,p95_latency_regression" || properties["apexload.summary"] != "oracle wait time moved with P95 latency" {
		t.Fatalf("properties = %#v, want diagnostic context", properties)
	}
	if payload.CorrelationManifest.SchemaVersion != dynatraceCorrelationManifestSchemaVersion || payload.CorrelationManifest.TestID != "test-dt-dry-run" {
		t.Fatalf("correlation manifest = %#v, want offline manifest for test", payload.CorrelationManifest)
	}
}

func TestValidateNotificationsAllowsTelemetryEvents(t *testing.T) {
	err := validateNotificationsSettings(NotificationsSettings{
		Webhooks: []NotificationWebhookSettings{{
			Name:    "Telemetry Alerts",
			Enabled: true,
			URL:     "https://hooks.example.test/apexload",
			Format:  notificationFormatSlack,
			Events:  []string{"telemetry_degraded", "telemetry_breached", "telemetry_recovered"},
		}},
	})
	if err != nil {
		t.Fatalf("expected telemetry notification events to validate, got %v", err)
	}
}

func TestBroadcastEventDispatchesConfiguredNotificationWebhook(t *testing.T) {
	// httptest binds to loopback; allow private outbound for the SSRF guard.
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
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
		if payload["text"] != "APEX Load Test Started: Checkout Smoke (RUNNING)" {
			t.Fatalf("payload text = %#v", payload["text"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected webhook delivery")
	}
}

func TestBroadcastEventDispatchesDynatraceWebhookWithAPIToken(t *testing.T) {
	// httptest binds to loopback; allow private outbound for the SSRF guard.
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
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
