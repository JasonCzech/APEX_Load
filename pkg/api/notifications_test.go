package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

func TestValidateNotificationsRequiresHTTPSAndRejectsUserinfo(t *testing.T) {
	for _, format := range []string{notificationFormatSlack, notificationFormatTeams, notificationFormatDynatrace} {
		t.Run(format, func(t *testing.T) {
			webhook := NotificationWebhookSettings{URL: "http://hooks.example.test/secret-path", Format: format}
			if format == notificationFormatDynatrace {
				webhook.APIToken = "token"
			}
			if err := validateNotificationsSettings(NotificationsSettings{Webhooks: []NotificationWebhookSettings{webhook}}); err == nil || !strings.Contains(err.Error(), "must use https") {
				t.Fatalf("cleartext %s webhook validation = %v, want HTTPS error", format, err)
			}
		})
	}
	userinfo := NotificationWebhookSettings{URL: "https://token@hooks.example.test/path", Format: notificationFormatSlack}
	if err := validateNotificationsSettings(NotificationsSettings{Webhooks: []NotificationWebhookSettings{userinfo}}); err == nil || !strings.Contains(err.Error(), "userinfo") {
		t.Fatalf("userinfo webhook validation = %v, want rejection", err)
	}
}

func TestBroadcastEventDispatchesConfiguredNotificationWebhook(t *testing.T) {
	// httptest binds to loopback; allow private outbound for the SSRF guard.
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	t.Setenv(notificationsAllowInsecureHTTPEnv, "true")
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
	t.Setenv(notificationsAllowInsecureHTTPEnv, "true")
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

func TestNotificationDispatcherRetriesRetryableStatus(t *testing.T) {
	t.Setenv(notificationsAllowInsecureHTTPEnv, "true")
	notificationDeliveryFailuresTotal.Store(0)
	attempts := 0
	var dedupeHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		dedupeHeader = r.Header.Get("X-APEXLoad-Notification-Key")
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	dispatcher := newNotificationDispatcher(server.Client())
	dispatcher.postJSON(NotificationWebhookSettings{URL: server.URL, Format: notificationFormatSlack}, map[string]any{"ok": true}, "dedupe-key")

	if attempts != 2 {
		t.Fatalf("attempts = %d, want retry then success", attempts)
	}
	if dedupeHeader != "dedupe-key" {
		t.Fatalf("dedupe header = %q, want dedupe-key", dedupeHeader)
	}
	if got := notificationDeliveryFailures(); got != 0 {
		t.Fatalf("delivery failures = %d, want 0 after retry success", got)
	}
}

func TestNotificationDedupeKeyIsBoundedHeaderSafeAndDistinct(t *testing.T) {
	webhook := NotificationWebhookSettings{
		Name: "ops\r\nX-Injected: true",
		URL:  "https://hooks.example.test/" + strings.Repeat("long-path-", 200),
	}
	event := NotificationEvent{
		Type:      "threshold_alert",
		TestID:    "test-123",
		Status:    "avg_server_processing_ms",
		Summary:   "breach\r\nX-Injected: true " + strings.Repeat("x", 10_000),
		Timestamp: time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC),
	}
	first := notificationDedupeKey(webhook, event)
	if len(first) != 64 || strings.ContainsAny(first, "\r\n") {
		t.Fatalf("dedupe key is not a fixed SHA-256 header value: len=%d key=%q", len(first), first)
	}
	if repeat := notificationDedupeKey(webhook, event); repeat != first {
		t.Fatalf("dedupe key is not deterministic: first=%q repeat=%q", first, repeat)
	}
	event.Summary += "different transaction"
	if second := notificationDedupeKey(webhook, event); second == first {
		t.Fatalf("distinct event summaries produced the same dedupe key: %q", first)
	}
}

func TestNotificationDispatcherCountsFinalFailure(t *testing.T) {
	t.Setenv(notificationsAllowInsecureHTTPEnv, "true")
	notificationDeliveryFailuresTotal.Store(0)
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	dispatcher := newNotificationDispatcher(server.Client())
	dispatcher.postJSON(NotificationWebhookSettings{URL: server.URL, Format: notificationFormatSlack}, map[string]any{"ok": true}, "dedupe-key")

	if attempts != 3 {
		t.Fatalf("attempts = %d, want bounded 3 retries", attempts)
	}
	if got := notificationDeliveryFailures(); got != 1 {
		t.Fatalf("delivery failures = %d, want 1", got)
	}
}

func TestNotificationDispatcherBoundsBurstAndPerDestinationConcurrency(t *testing.T) {
	t.Setenv(notificationsAllowInsecureHTTPEnv, "true")
	notificationDeliveryDroppedTotal.Store(0)
	var active atomic.Int64
	var maximum atomic.Int64
	started := make(chan struct{}, maxNotificationDeliveries)
	release := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	defer releaseOnce.Do(func() { close(release) })

	dispatcher := newNotificationDispatcher(server.Client())
	settings := NotificationsSettings{Webhooks: []NotificationWebhookSettings{{
		Name: "slow", Enabled: true, URL: server.URL, Format: notificationFormatSlack,
	}}}
	for i := 0; i < 100; i++ {
		dispatcher.Dispatch(settings, NotificationEvent{Type: "test_failed", TestID: "run", Timestamp: time.Now()})
	}
	for i := 0; i < maxNotificationPerDestination; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("started %d deliveries, want %d", i, maxNotificationPerDestination)
		}
	}
	if got := maximum.Load(); got > maxNotificationPerDestination {
		t.Fatalf("per-destination concurrency = %d, limit %d", got, maxNotificationPerDestination)
	}
	if got := len(dispatcher.deliverySlots); got > maxNotificationDeliveries {
		t.Fatalf("global delivery concurrency = %d, limit %d", got, maxNotificationDeliveries)
	}
	if got := notificationDeliveryDroppedTotal.Load(); got < 100-maxNotificationPerDestination {
		t.Fatalf("dropped deliveries = %d, want at least %d", got, 100-maxNotificationPerDestination)
	}

	dispatcher.Close()
	if dispatcher.Dispatch(settings, NotificationEvent{Type: "test_failed", TestID: "after-close", Timestamp: time.Now()}) {
		t.Fatal("closed dispatcher reported admitting a delivery")
	}
	releaseOnce.Do(func() { close(release) })
	deadline := time.Now().Add(2 * time.Second)
	for active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := active.Load(); got != 0 {
		t.Fatalf("active deliveries after release = %d", got)
	}
}

func TestValidateNotificationsSettingsCapsWebhookCount(t *testing.T) {
	settings := NotificationsSettings{Webhooks: make([]NotificationWebhookSettings, maxNotificationWebhooks+1)}
	if err := validateNotificationsSettings(settings); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("webhook cardinality error = %v", err)
	}
}

func TestValidateNotificationsSettingsCapsRetainedAndOutboundFields(t *testing.T) {
	valid := func() NotificationWebhookSettings {
		return NotificationWebhookSettings{URL: "https://hooks.example.test/events", Format: notificationFormatSlack}
	}
	tests := []struct {
		name   string
		mutate func(*NotificationWebhookSettings)
		field  string
	}{
		{name: "name", mutate: func(webhook *NotificationWebhookSettings) {
			webhook.Name = strings.Repeat("n", maxNotificationNameBytes+1)
		}, field: "name"},
		{name: "url", mutate: func(webhook *NotificationWebhookSettings) {
			webhook.URL = "https://example.test/" + strings.Repeat("u", maxNotificationURLBytes)
		}, field: "url"},
		{name: "api token", mutate: func(webhook *NotificationWebhookSettings) {
			webhook.APIToken = strings.Repeat("t", maxNotificationAPITokenBytes+1)
		}, field: "api_token"},
		{name: "entity selector", mutate: func(webhook *NotificationWebhookSettings) {
			webhook.EntitySelector = strings.Repeat("e", maxNotificationEntitySelectorBytes+1)
		}, field: "entity_selector"},
		{name: "event count", mutate: func(webhook *NotificationWebhookSettings) { webhook.Events = make([]string, maxNotificationEvents+1) }, field: "events"},
		{name: "event length", mutate: func(webhook *NotificationWebhookSettings) {
			webhook.Events = []string{strings.Repeat("e", maxNotificationEventBytes+1)}
		}, field: "events"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			webhook := valid()
			test.mutate(&webhook)
			err := validateNotificationsSettings(NotificationsSettings{Webhooks: []NotificationWebhookSettings{webhook}})
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("validation error = %v, want bounded %s error", err, test.field)
			}
		})
	}
}
