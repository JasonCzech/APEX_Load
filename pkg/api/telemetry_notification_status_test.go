package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
	yaml "gopkg.in/yaml.v3"
)

func TestTelemetryNotificationStatusAuthorizesProjectAndResultScope(t *testing.T) {
	for _, tc := range []struct {
		name, project string
		scopes        []string
		allowed       bool
	}{
		{"viewer", storage.DefaultProjectID, []string{"results:read"}, true},
		{"foreign project", "foreign", []string{"results:read"}, false},
		{"wrong scope", storage.DefaultProjectID, []string{"tests:run"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store := resourceAuthTestServer(t, authModeTrustedHeaders)
			s.runner.archiveStore = store
			now := time.Now().UTC()
			if tc.project != storage.DefaultProjectID {
				if err := store.SaveProject(&storage.Project{ID: tc.project, Name: "Foreign"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.SaveTest(&storage.TestRecord{ID: "notification-status", Name: "status", ProjectID: storage.DefaultProjectID, Status: "FAILED", StartTime: now, EndTime: &now}); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveServiceAccount(&storage.ServiceAccount{ID: "notification-status-viewer", Name: "reader", KeyHash: hashAPIKey("notification-status-fixture-key"), Active: true, ProjectRoles: map[string][]string{tc.project: {RoleViewer}}, Scopes: tc.scopes, CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/notification-status/telemetry/notifications", nil)
			req.SetPathValue("id", "notification-status")
			req.Header.Set("X-APEXLoad-API-Key", "notification-status-fixture-key")
			middleware := authMiddleware(func() AuthSettings { return AuthSettings{Mode: authModeTrustedHeaders} }, store, nil)
			rec := httptest.NewRecorder()
			middleware(http.HandlerFunc(s.handleTelemetryNotificationStatus)).ServeHTTP(rec, req)
			if tc.allowed {
				if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"reason":"not_recorded"`) {
					t.Fatal(rec.Code, rec.Body.String())
				}
			} else if rec.Code == 200 {
				t.Fatal("foreign or scope-denied metadata disclosed", rec.Body.String())
			}
		})
	}
}

func TestTelemetryNotificationStatusPreservesSafeDurableDecision(t *testing.T) {
	s, store, m := notificationRuntimeFixture(t)
	commitNotificationRuntimeFixture(t, s, store, m)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/notification-run/telemetry/notifications", nil)
	req.SetPathValue("id", "notification-run")
	s.handleTelemetryNotificationStatus(rec, req)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Body.String(), `"decision":"no_recipients"`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	for _, field := range []string{`"payload"`, `"destinations"`, `"owner_token"`, `"url"`, `"api_token"`} {
		if strings.Contains(rec.Body.String(), field) {
			t.Fatal("private notification state disclosed", field)
		}
	}
	if err := store.DeleteTest("notification-run"); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	s.handleTelemetryNotificationStatus(rec, req.Clone(context.Background()))
	if rec.Code != 404 {
		t.Fatal("deleted status remained available", rec.Code)
	}
}

func TestTelemetryNotificationGeneratedStatusContract(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var document oasDoc
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	route := document.Paths["/api/v1/tests/{id}/telemetry/notifications"]["get"]
	if route.XHandler != "handleTelemetryNotificationStatus" || !strings.Contains(route.XRoles, "project roles: Viewer") || !strings.Contains(route.XRoles, "service-account scope: results:read") {
		t.Fatalf("notification route identity/access missing: %+v", route)
	}
	schema := route.Responses["200"].Content["application/json"].Schema
	if schema.Properties["available"].Type != "boolean" || schema.Properties["reason"].Type != "string" {
		t.Fatal("unavailable decision is not typed", schema)
	}
	publication := schema.Properties["publication"]
	expected := map[string]string{"manifest_sha256": "string", "event_id": "string", "decision": "string", "suppressed_publications": "integer", "updated_at": "string", "deliveries": "object"}
	if len(publication.Properties) != len(expected) {
		t.Fatal("safe publication shape changed", publication.Properties)
	}
	for field, kind := range expected {
		if publication.Properties[field].Type != kind {
			t.Fatalf("%s type lost: %+v", field, publication.Properties[field])
		}
	}
	encoded, err := yaml.Marshal(publication.Properties["deliveries"].AdditionalProperties)
	if err != nil {
		t.Fatal(err)
	}
	var count oasSchema
	if err := yaml.Unmarshal(encoded, &count); err != nil {
		t.Fatal(err)
	}
	if count.Type != "integer" {
		t.Fatal("delivery counts became opaque", count)
	}
}
