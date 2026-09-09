package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNotificationPreviewFormatsAndValidation(t *testing.T) {
	s := NewServer(ServerConfig{})
	for _, tc := range []struct {
		format, event string
		status        int
	}{{"slack", "test_complete", 200}, {"teams", "test_failed", 200}, {"dynatrace", "test_started", 200}, {"unknown", "test_complete", 400}, {"slack", "unknown", 400}} {
		t.Run(tc.format+tc.event, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.handleNotificationPreview(w, httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"format":%q,"event_type":%q}`, tc.format, tc.event))))
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if w.Code == 200 && !strings.Contains(w.Body.String(), `"delivery_disabled":true`) {
				t.Fatal(w.Body.String())
			}
		})
	}
}

func TestNotificationTestRequiresSavedEnabledRoute(t *testing.T) {
	s := NewServer(ServerConfig{})
	settings := s.runner.GetSystemSettings()
	settings.Notifications.Webhooks = []NotificationWebhookSettings{{Name: "route", Enabled: false, URL: "https://example.test/hook", Format: "slack"}}
	saved, err := s.runner.UpdateSystemSettings(settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		index    int
		revision string
		status   int
	}{{0, "", 409}, {0, "outdated", 409}, {-1, systemSettingsRevision(saved), 400}, {1, systemSettingsRevision(saved), 400}, {0, systemSettingsRevision(saved), 409}} {
		w := httptest.NewRecorder()
		s.handleNotificationTest(w, httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"index":%d,"expected_revision":%q}`, tc.index, tc.revision))))
		if w.Code != tc.status {
			t.Fatalf("status %d, expected %d: %s", w.Code, tc.status, w.Body.String())
		}
	}
}

func TestNotificationHistoryBoundedConcurrentAndSafe(t *testing.T) {
	d := &notificationDispatcher{}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.recordDelivery(NotificationWebhookSettings{Name: "route", Format: "slack", URL: "https://private.example/secret", APIToken: "secret"}, "delivered")
		}()
	}
	wg.Wait()
	if len(d.history) != 50 {
		t.Fatalf("history length %d", len(d.history))
	}
	raw, _ := json.Marshal(d.history)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "private.example") {
		t.Fatal("history disclosed credentials")
	}
}

func TestSettingsDiagnosticsRoutesHaveExplicitAccessAndBounds(t *testing.T) {
	for _, route := range systemAdministrationAPIRouteCatalog.Routes {
		if strings.Contains(route.Path, "/notifications/") {
			if route.Access == nil || len(route.Access.GlobalRoles) != 1 || route.Access.GlobalRoles[0] != RoleSystemAdmin || route.Access.ServiceAccountScope != "settings:manage" || route.Access.MaxBodyBytes != 4096 {
				t.Fatalf("unguarded route: %+v", route)
			}
		}
	}
}

func TestRedactionPreviewUsesRuntimeRules(t *testing.T) {
	s := NewServer(ServerConfig{})
	for _, tc := range []struct {
		body   string
		status int
		want   string
	}{
		{`{"sample":"customer-123 password=example","rules":[{"pattern":"customer-[0-9]+","replacement":"CUSTOMER"}]}`, 200, "CUSTOMER"},
		{`{"sample":"password=example","rules":[]}`, 200, "[REDACTED]"},
		{`{"sample":"example","rules":[{"pattern":"["}]}`, 400, ""},
		{`{"sample":` + fmt.Sprintf("%q", strings.Repeat("a", 16385)) + `}`, 400, ""},
		{`{`, 400, ""},
	} {
		w := httptest.NewRecorder()
		s.handleRedactionPreview(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body)))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		if w.Code == 200 && strings.Contains(w.Body.String(), "password=example") {
			t.Fatal("built-in policy was bypassed")
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("preview may be cached")
		}
	}
}

func TestNotificationDeliveryTestSendsMarkedSavedPayloadAndRecordsResult(t *testing.T) {
	t.Setenv(notificationsAllowInsecureHTTPEnv, "true")
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	received := make(chan string, 1)
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		raw, _ := json.Marshal(payload)
		received <- string(raw)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer destination.Close()
	s := NewServer(ServerConfig{})
	dispatcher := newNotificationDispatcher(destination.Client())
	defer dispatcher.Close()
	s.runner.notifier = dispatcher
	settings := s.runner.GetSystemSettings()
	settings.Notifications.Webhooks = []NotificationWebhookSettings{{Name: "Saved route", Enabled: true, URL: destination.URL, Format: "slack"}}
	saved, err := s.runner.UpdateSystemSettings(settings)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.handleNotificationTest(w, httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"index":0,"expected_revision":%q}`, systemSettingsRevision(saved)))))
	if w.Code != 202 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	select {
	case payload := <-received:
		if !strings.Contains(payload, "notification delivery test") {
			t.Fatal("payload not marked as test")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("delivery did not arrive")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		dispatcher.historyMu.Lock()
		complete := len(dispatcher.history) > 0
		dispatcher.historyMu.Unlock()
		if complete {
			break
		}
		time.Sleep(time.Millisecond)
	}
	history := httptest.NewRecorder()
	s.handleNotificationDeliveries(history, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(history.Body.String(), `"status":"delivered"`) {
		t.Fatal(history.Body.String())
	}
	// Omitting the index must not silently send to route zero.
	missing := httptest.NewRecorder()
	s.handleNotificationTest(missing, httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"expected_revision":%q}`, systemSettingsRevision(saved)))))
	if missing.Code != 400 {
		t.Fatalf("missing index status %d", missing.Code)
	}
}
