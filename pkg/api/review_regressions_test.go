package api

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

func TestPreserveWriteOnlyWebhookSecretsSurvivesReordering(t *testing.T) {
	current := SystemSettings{Notifications: NotificationsSettings{Webhooks: []NotificationWebhookSettings{
		{Name: "first", URL: "https://first.example/api", Format: notificationFormatDynatrace, APIToken: "token-first"},
		{Name: "second", URL: "https://second.example/api", Format: notificationFormatDynatrace, APIToken: "token-second"},
	}}}
	next := SystemSettings{Notifications: NotificationsSettings{Webhooks: []NotificationWebhookSettings{
		{Name: "second", URL: "https://second.example/api", Format: notificationFormatDynatrace},
		{Name: "first", URL: "https://first.example/api", Format: notificationFormatDynatrace},
	}}}
	got := preserveWriteOnlySystemSettingsSecrets(next, current)
	if got.Notifications.Webhooks[0].APIToken != "token-second" || got.Notifications.Webhooks[1].APIToken != "token-first" {
		t.Fatalf("tokens followed array positions: %#v", got.Notifications.Webhooks)
	}
}

func TestMultipartProjectResolverDoesNotReadBodyBeforeAuthorization(t *testing.T) {
	body := bytes.NewBufferString("large upload payload")
	req := httptest.NewRequest("POST", "/import?project_id=project-a", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	projectID, statusCode, message := resolveImportProjectIDFromForm(req, nil)
	if projectID != "project-a" || statusCode != 0 || message != "" {
		t.Fatalf("resolver = %q, %d, %q", projectID, statusCode, message)
	}
	remaining := make([]byte, body.Len())
	if _, err := req.Body.Read(remaining); err != nil {
		t.Fatal(err)
	}
	if string(remaining) != "large upload payload" {
		t.Fatalf("body was consumed before authorization: %q", remaining)
	}
}
