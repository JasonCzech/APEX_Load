package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCloudEventNotificationsMatchTheirSchemaAndKeepAStableID(t *testing.T) {
	t.Setenv("APEXLOAD_PUBLIC_URL", "https://load.example.test/")
	t.Setenv(notificationsAllowInsecureHTTPEnv, "true")
	webhook := NotificationWebhookSettings{Name: "orchestrator", Enabled: true, URL: "https://hooks.example.test/apex", Format: notificationFormatCloudEvents}
	if err := validateNotificationsSettings(NotificationsSettings{Webhooks: []NotificationWebhookSettings{webhook}}); err != nil {
		t.Fatalf("cloudevents webhook rejected: %v", err)
	}
	event := NotificationEvent{Type: "test_complete", TestID: "run 1", ProjectID: "proj", TestName: "Checkout\u202e load", Status: "COMPLETE",
		Summary: "done\x1b[31m", Conditions: []string{"p95 breached"}, Timestamp: time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)}
	payload, err := buildNotificationPayloadForWebhook(webhook, event)
	if err != nil {
		t.Fatal(err)
	}
	cloudEvent := payload.(AgentCloudEvent)
	validateAgentContract(t, compileAgentContractSchema(t, "event.schema.json"), "cloud event", cloudEvent)
	if cloudEvent.Type != "com.apexload.run.completed.v1" || cloudEvent.Source != "/apexload/projects/proj" || cloudEvent.Subject != "tests/run 1" {
		t.Fatalf("envelope = %+v", cloudEvent)
	}
	if cloudEvent.ID != notificationDedupeKey(webhook, event) {
		t.Fatal("event id is not the delivery's dedupe key")
	}
	again, _ := buildNotificationPayloadForWebhook(webhook, event)
	if again.(AgentCloudEvent).ID != cloudEvent.ID {
		t.Fatal("event id changed between builds")
	}
	if cloudEvent.Data.Links.Brief != "https://load.example.test/api/v1/tests/run%201/archive/agent-brief" {
		t.Fatalf("brief link = %q", cloudEvent.Data.Links.Brief)
	}
	if strings.ContainsRune(cloudEvent.Data.Event.TestName, '\u202e') || strings.ContainsRune(cloudEvent.Data.Event.Summary, '\x1b') {
		t.Fatalf("control text kept: %+v", cloudEvent.Data.Event)
	}
	if got := agentCloudEventType("telemetry_degraded"); got != "com.apexload.telemetry.degraded.v1" {
		t.Fatalf("fallback type = %q", got)
	}
}

func TestCloudEventDeliveryUsesTheStructuredContentType(t *testing.T) {
	t.Setenv(notificationsAllowInsecureHTTPEnv, "true")
	var contentType, key string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType, key = r.Header.Get("Content-Type"), r.Header.Get("X-APEXLoad-Notification-Key")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	webhook := NotificationWebhookSettings{Name: "agents", URL: server.URL, Format: notificationFormatCloudEvents}
	event := NotificationEvent{Type: "rca_analysis_completed", TestID: "run-1", Timestamp: time.Now()}
	payload, err := buildNotificationPayloadForWebhook(webhook, event)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newNotificationDispatcher(server.Client())
	dispatcher.postJSON(webhook, payload, notificationDedupeKey(webhook, event), nil)
	if !strings.HasPrefix(contentType, "application/cloudevents+json") || body["specversion"] != "1.0" || body["id"] != key {
		t.Fatalf("delivery content type %q, key %q, body %v", contentType, key, body)
	}
}
