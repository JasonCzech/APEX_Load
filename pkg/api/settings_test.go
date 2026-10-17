package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestSystemSettingsAPIAndPersistence(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("initial GET status = %d, want %d", getResp.Code, http.StatusOK)
	}

	var initial SystemSettings
	if err := json.Unmarshal(getResp.Body.Bytes(), &initial); err != nil {
		t.Fatalf("unmarshal initial settings: %v", err)
	}
	if initial.General.MaxConcurrentTests != 10 {
		t.Fatalf("initial max concurrent = %d, want 10", initial.General.MaxConcurrentTests)
	}
	if initial.CapacityPlanning.HeadroomPct != 20 {
		t.Fatalf("initial headroom = %v, want 20", initial.CapacityPlanning.HeadroomPct)
	}
	if initial.CapacityPlanning.Generator.CPUCores != 4 {
		t.Fatalf("initial generator cpu = %v, want 4", initial.CapacityPlanning.Generator.CPUCores)
	}
	if initial.CapacityPlanning.Availability.Source != capacityAvailabilityConfigured {
		t.Fatalf("initial availability source = %q, want %q", initial.CapacityPlanning.Availability.Source, capacityAvailabilityConfigured)
	}
	if initial.ArchiveRetention.DefaultRetentionDays != 90 || initial.ArchiveRetention.Enabled {
		t.Fatalf("initial archive retention = %#v, want disabled 90-day default", initial.ArchiveRetention)
	}
	if initial.ScheduleDefaults.ApprovalPolicy != "auto" || initial.ScheduleDefaults.MissedRunPolicy != "skip" {
		t.Fatalf("initial schedule defaults = %#v, want auto/skip", initial.ScheduleDefaults)
	}
	if len(initial.Notifications.Webhooks) != 0 {
		t.Fatalf("initial notifications = %#v, want none", initial.Notifications.Webhooks)
	}
	if initial.Auth.Mode != authModeDev {
		t.Fatalf("initial auth mode = %q, want %q", initial.Auth.Mode, authModeDev)
	}
	if initial.Auth.UserHeader != "X-Auth-Request-User" {
		t.Fatalf("initial auth user header = %q, want X-Auth-Request-User", initial.Auth.UserHeader)
	}
	if initial.ClientApp.Availability != clientAppDisabled {
		t.Fatalf("initial client app availability = %q, want %q", initial.ClientApp.Availability, clientAppDisabled)
	}
	if initial.Thresholds.MaxTelemetrySampleAgeMs == nil || *initial.Thresholds.MaxTelemetrySampleAgeMs != defaultTelemetrySampleAgeBudgetMs {
		t.Fatalf("initial telemetry sample age threshold = %#v, want %v", initial.Thresholds.MaxTelemetrySampleAgeMs, defaultTelemetrySampleAgeBudgetMs)
	}
	if initial.Thresholds.MaxTelemetryGeneratorLagMs == nil || *initial.Thresholds.MaxTelemetryGeneratorLagMs != defaultTelemetryGeneratorProcessingBudgetMs {
		t.Fatalf("initial generator lag threshold = %#v, want %v", initial.Thresholds.MaxTelemetryGeneratorLagMs, defaultTelemetryGeneratorProcessingBudgetMs)
	}

	maxErr := 1.5
	maxP99 := 1800.0
	maxServer := 350.0
	maxTransfer := 120.0
	maxSampleAge := 1500.0
	maxGeneratorLag := 300.0
	maxTransportLag := 400.0
	maxFanoutLag := 250.0
	maxBrowserRenderLag := 350.0
	update := SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":9090",
			MaxConcurrentTests:     3,
			MetricsIntervalSeconds: 2,
		},
		Thresholds: ThresholdSettings{
			MaxErrorRatePct:                &maxErr,
			MaxP99LatencyMs:                &maxP99,
			MaxAvgServerProcessingMs:       &maxServer,
			MaxAvgTransferMs:               &maxTransfer,
			MaxTelemetrySampleAgeMs:        &maxSampleAge,
			MaxTelemetryGeneratorLagMs:     &maxGeneratorLag,
			MaxTelemetryTransportLagMs:     &maxTransportLag,
			MaxTelemetryControllerFanoutMs: &maxFanoutLag,
			MaxTelemetryBrowserRenderLagMs: &maxBrowserRenderLag,
		},
		CapacityPlanning: CapacityPlanningSettings{
			HeadroomPct: 25,
			Generator: GeneratorCapacitySettings{
				CPUCores:    8,
				MemoryMB:    16384,
				NetworkMbps: 2500,
				MaxVUsers:   18000,
			},
			Availability: AvailabilitySettings{
				Source:                        capacityAvailabilityConfiguredPlusLive,
				ConfiguredAvailableGenerators: 6,
			},
			Reservations: []CapacityReservation{{
				ProjectID:          "checkout",
				ReservedGenerators: 2,
			}},
		},
		ArchiveRetention: ArchiveRetentionSettings{
			Enabled:              true,
			DefaultRetentionDays: 45,
			Policies: []ArchiveRetentionPolicy{{
				Name:            "failed-checkout",
				ProjectID:       "checkout",
				RunStatus:       "failed",
				ArtifactType:    "bundle",
				ComplianceClass: "regulated",
				Reason:          "failed_run_short_retention",
				RetentionDays:   7,
			}},
		},
		ScheduleDefaults: ScheduleDefaultsSettings{
			ApprovalPolicy:        "production",
			MissedRunPolicy:       "catch_up_once",
			BlackoutStartTime:     "00:00",
			BlackoutEndTime:       "06:00",
			AvailabilityStartTime: "09:00",
			AvailabilityEndTime:   "17:00",
		},
		Notifications: NotificationsSettings{
			Webhooks: []NotificationWebhookSettings{{
				Name:    "Perf Alerts",
				Enabled: true,
				URL:     "https://hooks.slack.test/services/T000/B000/abc123",
				Format:  notificationFormatSlack,
				Events:  []string{"test_failed", "test_complete"},
			}},
		},
		Auth: AuthSettings{
			Mode:               authModeTrustedHeaders,
			ProviderName:       "Microsoft Entra ID",
			LoginURL:           "https://login.example.test/apexload/start",
			LogoutURL:          "/logout",
			UserHeader:         "X-Test-User",
			NameHeader:         "X-Test-Name",
			EmailHeader:        "X-Test-Email",
			RolesHeader:        "X-Test-Roles",
			ProjectRolesHeader: "X-Test-Project-Roles",
		},
		ClientApp: ClientAppSettings{Availability: clientAppEnabled},
	}
	body, _ := json.Marshal(update)
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
	withCurrentSystemSettingsIfMatch(putReq, server)
	putResp := httptest.NewRecorder()
	server.mux.ServeHTTP(putResp, putReq)
	if putResp.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want %d body=%s", putResp.Code, http.StatusOK, putResp.Body.String())
	}

	getResp2 := httptest.NewRecorder()
	getReq.Header.Set("X-Test-User", "root")
	getReq.Header.Set("X-Test-Roles", RoleSystemAdmin)
	server.mux.ServeHTTP(getResp2, getReq)
	if getResp2.Code != http.StatusOK {
		t.Fatalf("second GET status = %d, want %d", getResp2.Code, http.StatusOK)
	}
	var updated SystemSettings
	if err := json.Unmarshal(getResp2.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal updated settings: %v", err)
	}
	if updated.General.APIEndpoint != ":9090" {
		t.Fatalf("api endpoint = %q, want :9090", updated.General.APIEndpoint)
	}
	if updated.General.MaxConcurrentTests != 3 {
		t.Fatalf("max concurrent = %d, want 3", updated.General.MaxConcurrentTests)
	}
	if updated.General.MetricsIntervalSeconds != 2 {
		t.Fatalf("metrics interval = %d, want 2", updated.General.MetricsIntervalSeconds)
	}
	if updated.ClientApp.Availability != clientAppEnabled {
		t.Fatalf("client app availability = %q, want %q", updated.ClientApp.Availability, clientAppEnabled)
	}
	if updated.Thresholds.MaxErrorRatePct == nil || *updated.Thresholds.MaxErrorRatePct != maxErr {
		t.Fatalf("max error rate = %#v, want %v", updated.Thresholds.MaxErrorRatePct, maxErr)
	}
	if updated.Thresholds.MaxP99LatencyMs == nil || *updated.Thresholds.MaxP99LatencyMs != maxP99 {
		t.Fatalf("max p99 latency = %#v, want %v", updated.Thresholds.MaxP99LatencyMs, maxP99)
	}
	if updated.Thresholds.MaxAvgServerProcessingMs == nil || *updated.Thresholds.MaxAvgServerProcessingMs != maxServer {
		t.Fatalf("max avg server processing = %#v, want %v", updated.Thresholds.MaxAvgServerProcessingMs, maxServer)
	}
	if updated.Thresholds.MaxAvgTransferMs == nil || *updated.Thresholds.MaxAvgTransferMs != maxTransfer {
		t.Fatalf("max avg transfer = %#v, want %v", updated.Thresholds.MaxAvgTransferMs, maxTransfer)
	}
	if updated.Thresholds.MaxTelemetrySampleAgeMs == nil || *updated.Thresholds.MaxTelemetrySampleAgeMs != maxSampleAge {
		t.Fatalf("max telemetry sample age = %#v, want %v", updated.Thresholds.MaxTelemetrySampleAgeMs, maxSampleAge)
	}
	if updated.Thresholds.MaxTelemetryGeneratorLagMs == nil || *updated.Thresholds.MaxTelemetryGeneratorLagMs != maxGeneratorLag {
		t.Fatalf("max telemetry generator lag = %#v, want %v", updated.Thresholds.MaxTelemetryGeneratorLagMs, maxGeneratorLag)
	}
	if updated.Thresholds.MaxTelemetryTransportLagMs == nil || *updated.Thresholds.MaxTelemetryTransportLagMs != maxTransportLag {
		t.Fatalf("max telemetry transport lag = %#v, want %v", updated.Thresholds.MaxTelemetryTransportLagMs, maxTransportLag)
	}
	if updated.Thresholds.MaxTelemetryControllerFanoutMs == nil || *updated.Thresholds.MaxTelemetryControllerFanoutMs != maxFanoutLag {
		t.Fatalf("max telemetry controller fanout = %#v, want %v", updated.Thresholds.MaxTelemetryControllerFanoutMs, maxFanoutLag)
	}
	if updated.Thresholds.MaxTelemetryBrowserRenderLagMs == nil || *updated.Thresholds.MaxTelemetryBrowserRenderLagMs != maxBrowserRenderLag {
		t.Fatalf("max telemetry browser render = %#v, want %v", updated.Thresholds.MaxTelemetryBrowserRenderLagMs, maxBrowserRenderLag)
	}
	if updated.CapacityPlanning.HeadroomPct != 25 {
		t.Fatalf("headroom = %v, want 25", updated.CapacityPlanning.HeadroomPct)
	}
	if updated.CapacityPlanning.Generator.MemoryMB != 16384 {
		t.Fatalf("generator memory = %d, want 16384", updated.CapacityPlanning.Generator.MemoryMB)
	}
	if updated.CapacityPlanning.Availability.ConfiguredAvailableGenerators != 6 {
		t.Fatalf("configured available generators = %d, want 6", updated.CapacityPlanning.Availability.ConfiguredAvailableGenerators)
	}
	if len(updated.CapacityPlanning.Reservations) != 1 || updated.CapacityPlanning.Reservations[0].ProjectID != "checkout" || updated.CapacityPlanning.Reservations[0].ReservedGenerators != 2 {
		t.Fatalf("capacity reservations = %#v, want checkout reservation", updated.CapacityPlanning.Reservations)
	}
	if !updated.ArchiveRetention.Enabled || updated.ArchiveRetention.DefaultRetentionDays != 45 {
		t.Fatalf("archive retention = %#v, want enabled 45-day default", updated.ArchiveRetention)
	}
	if len(updated.ArchiveRetention.Policies) != 1 || updated.ArchiveRetention.Policies[0].RunStatus != "FAILED" || updated.ArchiveRetention.Policies[0].RetentionDays != 7 || updated.ArchiveRetention.Policies[0].ComplianceClass != "regulated" || updated.ArchiveRetention.Policies[0].Reason != "failed_run_short_retention" {
		t.Fatalf("archive retention policies = %#v, want normalized failed-checkout policy", updated.ArchiveRetention.Policies)
	}
	if updated.ScheduleDefaults.ApprovalPolicy != "production" || updated.ScheduleDefaults.MissedRunPolicy != "catch_up_once" {
		t.Fatalf("schedule governance defaults = %#v, want production/catch_up_once", updated.ScheduleDefaults)
	}
	if updated.ScheduleDefaults.BlackoutStartTime != "00:00" || updated.ScheduleDefaults.BlackoutEndTime != "06:00" || updated.ScheduleDefaults.AvailabilityStartTime != "09:00" || updated.ScheduleDefaults.AvailabilityEndTime != "17:00" {
		t.Fatalf("schedule calendar defaults = %#v, want persisted windows", updated.ScheduleDefaults)
	}
	if len(updated.Notifications.Webhooks) != 1 {
		t.Fatalf("notification webhooks = %d, want 1", len(updated.Notifications.Webhooks))
	}
	if updated.Notifications.Webhooks[0].Format != notificationFormatSlack {
		t.Fatalf("notification format = %q, want %q", updated.Notifications.Webhooks[0].Format, notificationFormatSlack)
	}
	if updated.Auth.Mode != authModeTrustedHeaders {
		t.Fatalf("auth mode = %q, want %q", updated.Auth.Mode, authModeTrustedHeaders)
	}
	if updated.Auth.RolesHeader != "X-Test-Roles" {
		t.Fatalf("roles header = %q, want X-Test-Roles", updated.Auth.RolesHeader)
	}
	if updated.Auth.ProviderName != "Microsoft Entra ID" {
		t.Fatalf("provider name = %q, want Microsoft Entra ID", updated.Auth.ProviderName)
	}
	if updated.Auth.LoginURL != "https://login.example.test/apexload/start" {
		t.Fatalf("login url = %q, want configured login url", updated.Auth.LoginURL)
	}
	if updated.Auth.LogoutURL != "/logout" {
		t.Fatalf("logout url = %q, want /logout", updated.Auth.LogoutURL)
	}

	runner := NewTestRunner(10, NewHub(), store)
	persisted := runner.GetSystemSettings()
	if persisted.General.MaxConcurrentTests != 3 {
		t.Fatalf("persisted max concurrent = %d, want 3", persisted.General.MaxConcurrentTests)
	}
	if persisted.General.MetricsIntervalSeconds != 2 {
		t.Fatalf("persisted metrics interval = %d, want 2", persisted.General.MetricsIntervalSeconds)
	}
	if persisted.Thresholds.MaxTelemetryControllerFanoutMs == nil || *persisted.Thresholds.MaxTelemetryControllerFanoutMs != maxFanoutLag {
		t.Fatalf("persisted telemetry fanout threshold = %#v, want %v", persisted.Thresholds.MaxTelemetryControllerFanoutMs, maxFanoutLag)
	}
	if persisted.CapacityPlanning.Availability.Source != capacityAvailabilityConfiguredPlusLive {
		t.Fatalf("persisted availability source = %q, want %q", persisted.CapacityPlanning.Availability.Source, capacityAvailabilityConfiguredPlusLive)
	}
	if len(persisted.CapacityPlanning.Reservations) != 1 || persisted.CapacityPlanning.Reservations[0].ProjectID != "checkout" || persisted.CapacityPlanning.Reservations[0].ReservedGenerators != 2 {
		t.Fatalf("persisted capacity reservations = %#v, want checkout reservation", persisted.CapacityPlanning.Reservations)
	}
	if len(persisted.Notifications.Webhooks) != 1 || persisted.Notifications.Webhooks[0].URL != "https://hooks.slack.test/services/T000/B000/abc123" {
		t.Fatalf("persisted notifications = %#v, want webhook URL", persisted.Notifications.Webhooks)
	}
	if persisted.ScheduleDefaults.ApprovalPolicy != "production" || persisted.ScheduleDefaults.BlackoutEndTime != "06:00" {
		t.Fatalf("persisted schedule defaults = %#v, want saved governance and window settings", persisted.ScheduleDefaults)
	}
	if persisted.Auth.ProjectRolesHeader != "X-Test-Project-Roles" {
		t.Fatalf("persisted project roles header = %q, want X-Test-Project-Roles", persisted.Auth.ProjectRolesHeader)
	}
	if persisted.Auth.LoginURL != "https://login.example.test/apexload/start" {
		t.Fatalf("persisted login url = %q, want configured login url", persisted.Auth.LoginURL)
	}
}

func TestSystemSettingsRedactsAndPreservesNotificationSecrets(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 10, Addr: ":8080"})
	settings := server.runner.GetSystemSettings()
	settings.Notifications = NotificationsSettings{Webhooks: []NotificationWebhookSettings{{
		Name:           "Dynatrace",
		Enabled:        true,
		URL:            "https://dynatrace.example.test/api/v2/events/ingest",
		Format:         notificationFormatDynatrace,
		Events:         []string{"telemetry_breached"},
		APIToken:       "dt-secret-token",
		EntitySelector: "type(SERVICE)",
		TimeoutMinutes: 5,
	}}}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET status = %d body=%s", getResp.Code, getResp.Body.String())
	}
	if bytes.Contains(getResp.Body.Bytes(), []byte("dt-secret-token")) {
		t.Fatalf("GET response leaked api token: %s", getResp.Body.String())
	}
	var redacted map[string]any
	if err := json.Unmarshal(getResp.Body.Bytes(), &redacted); err != nil {
		t.Fatalf("unmarshal redacted settings: %v", err)
	}
	webhooks := redacted["notifications"].(map[string]any)["webhooks"].([]any)
	webhook := webhooks[0].(map[string]any)
	if got := webhook["api_token"]; got != "" {
		t.Fatalf("redacted api_token = %#v, want empty string", got)
	}
	if got := webhook["has_api_token"]; got != true {
		t.Fatalf("has_api_token = %#v, want true", got)
	}
	webhook["name"] = "Renamed Dynatrace"
	renamedPayload, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal renamed settings: %v", err)
	}

	putResp := httptest.NewRecorder()
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(renamedPayload))
	withCurrentSystemSettingsIfMatch(putReq, server)
	server.mux.ServeHTTP(putResp, putReq)
	if putResp.Code != http.StatusOK {
		t.Fatalf("PUT redacted settings status = %d body=%s", putResp.Code, putResp.Body.String())
	}
	if bytes.Contains(putResp.Body.Bytes(), []byte("dt-secret-token")) {
		t.Fatalf("PUT response leaked api token: %s", putResp.Body.String())
	}
	persisted := server.runner.GetSystemSettings()
	if got := persisted.Notifications.Webhooks[0].APIToken; got != "dt-secret-token" {
		t.Fatalf("persisted token = %q, want preserved token", got)
	}
	if got := persisted.Notifications.Webhooks[0].Name; got != "Renamed Dynatrace" {
		t.Fatalf("persisted name = %q, want renamed display label", got)
	}
}

func TestPreserveWriteOnlyNotificationTokensUsesUnambiguousStableIdentity(t *testing.T) {
	dynatrace := func(name, url, token string) NotificationWebhookSettings {
		return NotificationWebhookSettings{
			Name: name, Enabled: true, URL: url, Format: notificationFormatDynatrace, APIToken: token,
		}
	}
	const (
		urlA = "https://a.live.dynatrace.com/api/v2/events/ingest"
		urlB = "https://b.live.dynatrace.com/api/v2/events/ingest"
	)
	tests := []struct {
		name    string
		current []NotificationWebhookSettings
		next    []NotificationWebhookSettings
		want    []string
	}{
		{
			name:    "display name change",
			current: []NotificationWebhookSettings{dynatrace("Old name", urlA, "token-a")},
			next:    []NotificationWebhookSettings{dynatrace("New name", urlA, "")},
			want:    []string{"token-a"},
		},
		{
			name: "distinct endpoints reorder deterministically",
			current: []NotificationWebhookSettings{
				dynatrace("A", urlA, "token-a"),
				dynatrace("B", urlB, "token-b"),
			},
			next: []NotificationWebhookSettings{
				dynatrace("Renamed B", urlB, ""),
				dynatrace("Renamed A", urlA, ""),
			},
			want: []string{"token-b", "token-a"},
		},
		{
			name: "duplicate current endpoint fails closed",
			current: []NotificationWebhookSettings{
				dynatrace("A1", urlA, "token-a1"),
				dynatrace("A2", urlA, "token-a2"),
			},
			next: []NotificationWebhookSettings{dynatrace("A", urlA, "")},
			want: []string{""},
		},
		{
			name:    "duplicate next endpoint fails closed",
			current: []NotificationWebhookSettings{dynatrace("A", urlA, "token-a")},
			next: []NotificationWebhookSettings{
				dynatrace("A1", urlA, ""),
				dynatrace("A2", urlA, ""),
			},
			want: []string{"", ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := preserveWriteOnlySystemSettingsSecrets(
				SystemSettings{Notifications: NotificationsSettings{Webhooks: tt.next}},
				SystemSettings{Notifications: NotificationsSettings{Webhooks: tt.current}},
			)
			tokens := make([]string, len(got.Notifications.Webhooks))
			for index, webhook := range got.Notifications.Webhooks {
				tokens[index] = webhook.APIToken
			}
			if !reflect.DeepEqual(tokens, tt.want) {
				t.Fatalf("tokens = %#v, want %#v", tokens, tt.want)
			}
		})
	}
}

func TestNormalizeLoadedSystemSettingsBackfillsTelemetryThresholds(t *testing.T) {
	normalized := normalizeLoadedSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     2,
			MetricsIntervalSeconds: 1,
		},
		Thresholds: ThresholdSettings{},
		CapacityPlanning: CapacityPlanningSettings{
			HeadroomPct: 20,
			Generator: GeneratorCapacitySettings{
				CPUCores:    4,
				MemoryMB:    8192,
				NetworkMbps: 1000,
				MaxVUsers:   10000,
			},
			Availability: AvailabilitySettings{
				Source:                        capacityAvailabilityConfigured,
				ConfiguredAvailableGenerators: 1,
			},
		},
		Auth: defaultAuthSettings(),
	}, "", 2)

	if normalized.Thresholds.MaxTelemetrySampleAgeMs == nil || *normalized.Thresholds.MaxTelemetrySampleAgeMs != defaultTelemetrySampleAgeBudgetMs {
		t.Fatalf("sample age threshold = %#v, want default", normalized.Thresholds.MaxTelemetrySampleAgeMs)
	}
	if normalized.Thresholds.MaxTelemetryBrowserRenderLagMs == nil || *normalized.Thresholds.MaxTelemetryBrowserRenderLagMs != defaultTelemetryBrowserRenderBudgetMs {
		t.Fatalf("browser render threshold = %#v, want default", normalized.Thresholds.MaxTelemetryBrowserRenderLagMs)
	}
}

func TestSystemSettingsValidation(t *testing.T) {
	server := NewServer(ServerConfig{Store: nil, MaxConcurrent: 10, Addr: ":8080"})

	invalid := SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     0,
			MetricsIntervalSeconds: 1,
		},
	}
	body, _ := json.Marshal(invalid)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
	withCurrentSystemSettingsIfMatch(req, server)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusBadRequest)
	}
}

func TestSystemSettingsValidationRejectsInvalidTelemetryThresholds(t *testing.T) {
	settings := defaultSystemSettings(2)
	invalid := -1.0
	settings.Thresholds.MaxTelemetryTransportLagMs = &invalid

	err := validateSystemSettings(settings)
	if err == nil {
		t.Fatal("expected invalid telemetry threshold to fail validation")
	}
	if got := err.Error(); got != "thresholds.max_telemetry_transport_lag_ms must be >= 0 (0 disables this telemetry budget)" {
		t.Fatalf("validation error = %q, want telemetry threshold error", got)
	}

	// 0 is the documented explicit-disable value, not an error.
	disabled := 0.0
	settings.Thresholds.MaxTelemetryTransportLagMs = &disabled
	if err := validateSystemSettings(settings); err != nil {
		t.Fatalf("zero telemetry budget rejected: %v", err)
	}
}

func TestSystemSettingsValidationRejectsInvalidUsageSnapshotCadence(t *testing.T) {
	settings := defaultSystemSettings(2)
	settings.General.UsageSnapshotIntervalSeconds = -1

	err := validateSystemSettings(settings)
	if err == nil {
		t.Fatal("expected invalid usage snapshot cadence to fail validation")
	}
	if got := err.Error(); got != "general.usage_snapshot_interval_seconds must be >= 0" {
		t.Fatalf("validation error = %q, want usage snapshot cadence error", got)
	}
}

func TestSystemSettingsValidationRejectsInvalidScheduleDefaults(t *testing.T) {
	settings := defaultSystemSettings(2)
	settings.ScheduleDefaults.BlackoutStartTime = "24:00"
	settings.ScheduleDefaults.BlackoutEndTime = "06:00"

	err := validateSystemSettings(settings)
	if err == nil {
		t.Fatal("expected invalid schedule defaults to fail validation")
	}
	if got := err.Error(); got != "schedule_defaults.blackout_start_time must use HH:MM" {
		t.Fatalf("validation error = %q, want schedule default time error", got)
	}
}

func TestSystemSettingsValidationRejectsInvalidCapacityPlanning(t *testing.T) {
	err := validateSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     2,
			MetricsIntervalSeconds: 1,
		},
		Thresholds: ThresholdSettings{},
		CapacityPlanning: CapacityPlanningSettings{
			HeadroomPct: 100,
			Generator: GeneratorCapacitySettings{
				CPUCores:    4,
				MemoryMB:    8192,
				NetworkMbps: 1000,
				MaxVUsers:   10000,
			},
			Availability: AvailabilitySettings{
				Source:                        capacityAvailabilityConfigured,
				ConfiguredAvailableGenerators: 1,
			},
		},
	})
	if err == nil {
		t.Fatal("expected invalid headroom to fail validation")
	}

	_, err = json.Marshal(SystemSettings{})
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}

	err = validateSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     2,
			MetricsIntervalSeconds: 1,
		},
		Thresholds: ThresholdSettings{},
		CapacityPlanning: CapacityPlanningSettings{
			HeadroomPct: 20,
			Generator: GeneratorCapacitySettings{
				CPUCores:    4,
				MemoryMB:    8192,
				NetworkMbps: 0,
				MaxVUsers:   10000,
			},
			Availability: AvailabilitySettings{
				Source:                        "invalid",
				ConfiguredAvailableGenerators: 1,
			},
		},
	})
	if err == nil {
		t.Fatal("expected invalid capacity planning settings to fail validation")
	}

	err = validateSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     2,
			MetricsIntervalSeconds: 1,
		},
		Thresholds: ThresholdSettings{},
		CapacityPlanning: CapacityPlanningSettings{
			HeadroomPct: 20,
			Generator: GeneratorCapacitySettings{
				CPUCores:    4,
				MemoryMB:    8192,
				NetworkMbps: 1000,
				MaxVUsers:   10000,
			},
			Availability: AvailabilitySettings{
				Source:                        capacityAvailabilityConfigured,
				ConfiguredAvailableGenerators: 2,
			},
			Reservations: []CapacityReservation{{
				ProjectID:          "checkout",
				ReservedGenerators: 3,
			}},
		},
	})
	if err == nil {
		t.Fatal("expected capacity reservations exceeding configured availability to fail validation")
	}

	err = validateSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     2,
			MetricsIntervalSeconds: 1,
		},
		Thresholds: ThresholdSettings{},
		CapacityPlanning: CapacityPlanningSettings{
			HeadroomPct: 20,
			Generator: GeneratorCapacitySettings{
				CPUCores:    4,
				MemoryMB:    8192,
				NetworkMbps: 1000,
				MaxVUsers:   10000,
			},
			Availability: AvailabilitySettings{
				Source:                        capacityAvailabilityConfigured,
				ConfiguredAvailableGenerators: 2,
			},
			Reservations: []CapacityReservation{{
				ProjectID:          "checkout",
				ReservedGenerators: -1,
			}},
		},
	})
	if err == nil {
		t.Fatal("expected negative capacity reservation to fail validation")
	}
}

func TestSystemSettingsValidationRejectsInvalidNotifications(t *testing.T) {
	err := validateSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     2,
			MetricsIntervalSeconds: 1,
		},
		Thresholds: ThresholdSettings{},
		CapacityPlanning: CapacityPlanningSettings{
			HeadroomPct: 20,
			Generator: GeneratorCapacitySettings{
				CPUCores:    4,
				MemoryMB:    8192,
				NetworkMbps: 1000,
				MaxVUsers:   10000,
			},
			Availability: AvailabilitySettings{
				Source:                        capacityAvailabilityConfigured,
				ConfiguredAvailableGenerators: 1,
			},
		},
		Notifications: NotificationsSettings{
			Webhooks: []NotificationWebhookSettings{{
				Enabled: true,
				URL:     "ftp://hooks.example.test/notify",
				Format:  "email",
				Events:  []string{"test_created"},
			}},
		},
		Auth: defaultAuthSettings(),
	})
	if err == nil {
		t.Fatal("expected invalid notifications to fail validation")
	}
}

func TestSystemSettingsValidationRejectsDynatraceWebhookWithoutToken(t *testing.T) {
	err := validateSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     2,
			MetricsIntervalSeconds: 1,
		},
		Thresholds: ThresholdSettings{},
		CapacityPlanning: CapacityPlanningSettings{
			HeadroomPct: 20,
			Generator: GeneratorCapacitySettings{
				CPUCores:    4,
				MemoryMB:    8192,
				NetworkMbps: 1000,
				MaxVUsers:   10000,
			},
			Availability: AvailabilitySettings{
				Source:                        capacityAvailabilityConfigured,
				ConfiguredAvailableGenerators: 1,
			},
		},
		Notifications: NotificationsSettings{
			Webhooks: []NotificationWebhookSettings{{
				Enabled: true,
				URL:     "https://example.live.dynatrace.com/api/v2/events/ingest",
				Format:  notificationFormatDynatrace,
			}},
		},
		Auth: defaultAuthSettings(),
	})
	if err == nil {
		t.Fatal("expected Dynatrace notifications without an API token to fail validation")
	}
}

func TestSystemSettingsValidationRejectsInvalidAuthSettings(t *testing.T) {
	err := validateSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     2,
			MetricsIntervalSeconds: 1,
		},
		Thresholds: ThresholdSettings{},
		CapacityPlanning: CapacityPlanningSettings{
			HeadroomPct: 20,
			Generator: GeneratorCapacitySettings{
				CPUCores:    4,
				MemoryMB:    8192,
				NetworkMbps: 1000,
				MaxVUsers:   10000,
			},
			Availability: AvailabilitySettings{
				Source:                        capacityAvailabilityConfigured,
				ConfiguredAvailableGenerators: 1,
			},
		},
		Auth: AuthSettings{
			Mode:       authModeTrustedHeaders,
			UserHeader: "X-Forwarded-User",
		},
	})
	if err == nil {
		t.Fatal("expected missing roles headers to fail validation")
	}

	err = validateSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     2,
			MetricsIntervalSeconds: 1,
		},
		Thresholds: ThresholdSettings{},
		CapacityPlanning: CapacityPlanningSettings{
			HeadroomPct: 20,
			Generator: GeneratorCapacitySettings{
				CPUCores:    4,
				MemoryMB:    8192,
				NetworkMbps: 1000,
				MaxVUsers:   10000,
			},
			Availability: AvailabilitySettings{
				Source:                        capacityAvailabilityConfigured,
				ConfiguredAvailableGenerators: 1,
			},
		},
		Auth: AuthSettings{
			Mode:               authModeTrustedHeaders,
			UserHeader:         "X-Forwarded-User",
			RolesHeader:        "X-Forwarded-Roles",
			LoginURL:           "ftp://login.example.test/start",
			ProjectRolesHeader: "X-Forwarded-Project-Roles",
		},
	})
	if err == nil {
		t.Fatal("expected invalid auth login url to fail validation")
	}

	err = validateSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     2,
			MetricsIntervalSeconds: 1,
		},
		Thresholds: ThresholdSettings{},
		CapacityPlanning: CapacityPlanningSettings{
			HeadroomPct: 20,
			Generator: GeneratorCapacitySettings{
				CPUCores:    4,
				MemoryMB:    8192,
				NetworkMbps: 1000,
				MaxVUsers:   10000,
			},
			Availability: AvailabilitySettings{
				Source:                        capacityAvailabilityConfigured,
				ConfiguredAvailableGenerators: 1,
			},
		},
		Auth: AuthSettings{
			Mode:                authModeOIDC,
			IssuerURL:           "https://login.example.test",
			ClientID:            "apexload-dashboard",
			ClientSecretEnvVar:  "APEXLOAD_OIDC_CLIENT_SECRET",
			RedirectURL:         "https://apexload.example.test/api/v1/auth/callback",
			SessionSecretEnvVar: "APEXLOAD_AUTH_SESSION_SECRET",
			Scopes:              []string{"openid", "profile", "email"},
		},
	})
	if err != nil {
		t.Fatalf("expected valid oidc settings, got error: %v", err)
	}

	err = validateSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     2,
			MetricsIntervalSeconds: 1,
		},
		Thresholds: ThresholdSettings{},
		CapacityPlanning: CapacityPlanningSettings{
			HeadroomPct: 20,
			Generator: GeneratorCapacitySettings{
				CPUCores:    4,
				MemoryMB:    8192,
				NetworkMbps: 1000,
				MaxVUsers:   10000,
			},
			Availability: AvailabilitySettings{
				Source:                        capacityAvailabilityConfigured,
				ConfiguredAvailableGenerators: 1,
			},
		},
		Auth: AuthSettings{
			Mode:        authModeOIDC,
			IssuerURL:   "https://login.example.test",
			ClientID:    "apexload-dashboard",
			RedirectURL: "https://apexload.example.test/api/v1/auth/callback",
		},
	})
	if err == nil {
		t.Fatal("expected missing oidc secret settings to fail validation")
	}

	err = validateSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     2,
			MetricsIntervalSeconds: 1,
		},
		Thresholds: ThresholdSettings{},
		CapacityPlanning: CapacityPlanningSettings{
			HeadroomPct: 20,
			Generator: GeneratorCapacitySettings{
				CPUCores:    4,
				MemoryMB:    8192,
				NetworkMbps: 1000,
				MaxVUsers:   10000,
			},
			Availability: AvailabilitySettings{
				Source:                        capacityAvailabilityConfigured,
				ConfiguredAvailableGenerators: 1,
			},
		},
		ArchiveRetention: ArchiveRetentionSettings{
			DefaultRetentionDays: -1,
		},
		Auth: AuthSettings{Mode: authModeDev},
	})
	if err == nil {
		t.Fatal("expected invalid archive retention default to fail validation")
	}
}

func TestNormalizeLoadedSystemSettingsBackfillsCapacityPlanningDefaults(t *testing.T) {
	normalized := normalizeLoadedSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     4,
			MetricsIntervalSeconds: 2,
		},
		Thresholds: ThresholdSettings{},
	}, "", 4)

	if normalized.CapacityPlanning.HeadroomPct != 20 {
		t.Fatalf("normalized headroom = %v, want 20", normalized.CapacityPlanning.HeadroomPct)
	}
	if normalized.CapacityPlanning.Generator.MaxVUsers != 10000 {
		t.Fatalf("normalized max vusers = %d, want 10000", normalized.CapacityPlanning.Generator.MaxVUsers)
	}
	if normalized.CapacityPlanning.Availability.Source != capacityAvailabilityConfigured {
		t.Fatalf("normalized availability source = %q, want %q", normalized.CapacityPlanning.Availability.Source, capacityAvailabilityConfigured)
	}
	if normalized.Auth.Mode != authModeDev {
		t.Fatalf("normalized auth mode = %q, want %q", normalized.Auth.Mode, authModeDev)
	}
	if normalized.Auth.RolesHeader != "X-APEXLoad-Roles" {
		t.Fatalf("normalized roles header = %q, want X-APEXLoad-Roles", normalized.Auth.RolesHeader)
	}
	if len(normalized.Auth.Scopes) != 3 || normalized.Auth.Scopes[0] != "openid" {
		t.Fatalf("normalized auth scopes = %#v, want default oidc scopes", normalized.Auth.Scopes)
	}
	if normalized.Auth.SessionSecretEnvVar != "APEXLOAD_AUTH_SESSION_SECRET" {
		t.Fatalf("normalized session secret env var = %q, want APEXLOAD_AUTH_SESSION_SECRET", normalized.Auth.SessionSecretEnvVar)
	}

	normalizedAlias := normalizeLoadedSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     4,
			MetricsIntervalSeconds: 2,
		},
		Thresholds: ThresholdSettings{},
		Auth:       AuthSettings{Mode: authModeDisabled},
	}, "", 4)
	if normalizedAlias.Auth.Mode != authModeDev {
		t.Fatalf("normalized disabled alias auth mode = %q, want %q", normalizedAlias.Auth.Mode, authModeDev)
	}
}

func TestStartTestQueuesWhenConfiguredMaxConcurrentReached(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)

	_, err := runner.UpdateSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     1,
			MetricsIntervalSeconds: 1,
		},
		Thresholds: ThresholdSettings{},
		CapacityPlanning: CapacityPlanningSettings{
			HeadroomPct: 20,
			Generator: GeneratorCapacitySettings{
				CPUCores:    4,
				MemoryMB:    8192,
				NetworkMbps: 1000,
				MaxVUsers:   10000,
			},
			Availability: AvailabilitySettings{
				Source:                        capacityAvailabilityConfigured,
				ConfiguredAvailableGenerators: 1,
			},
		},
	})
	if err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	runner.mu.Lock()
	runner.tests["running"] = &ManagedTest{ID: "running", Status: "RUNNING"}
	runner.tests["queued"] = &ManagedTest{ID: "queued", Status: "QUEUED"}
	runner.mu.Unlock()

	err = runner.StartTest("queued")
	if err != nil {
		t.Fatalf("StartTest returned error: %v", err)
	}

	queued, ok := runner.GetTest("queued")
	if !ok {
		t.Fatal("expected queued test to remain in runner")
	}
	if queued.Status != "PENDING" {
		t.Fatalf("test status = %s, want PENDING", queued.Status)
	}
}

func TestSourceSecretMigrationPolicyRequiresDurableAudit(t *testing.T) {
	t.Run("intent failure prevents mutation", func(t *testing.T) {
		store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		server := NewServer(ServerConfig{Store: store})
		settings := server.runner.GetSystemSettings()
		settings.ScriptImports.AllowSourceSecretMigration = true
		body, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		server.store = storeOverlay(&selectiveAuditFailureStore{Store: store, action: "system.settings.update", status: "pending"})
		request := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
		withCurrentSystemSettingsIfMatch(request, server)
		response := httptest.NewRecorder()
		server.handleUpdateSystemSettings(response, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("policy intent audit response = %d %s", response.Code, response.Body.String())
		}
		if server.runner.GetSystemSettings().ScriptImports.AllowSourceSecretMigration {
			t.Fatal("policy changed without a durable intent audit")
		}
	})

	t.Run("outcome failure is not acknowledged as success", func(t *testing.T) {
		store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		server := NewServer(ServerConfig{Store: store})
		settings := server.runner.GetSystemSettings()
		settings.ScriptImports.AllowSourceSecretMigration = true
		body, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		server.store = storeOverlay(&selectiveAuditFailureStore{Store: store, action: "system.settings.update", status: "success"})
		request := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
		withCurrentSystemSettingsIfMatch(request, server)
		response := httptest.NewRecorder()
		server.handleUpdateSystemSettings(response, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("policy outcome audit response = %d %s", response.Code, response.Body.String())
		}
		if !server.runner.GetSystemSettings().ScriptImports.AllowSourceSecretMigration {
			t.Fatal("expected the committed policy state to remain visible for reconciliation")
		}
		events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "system.settings.update", Limit: 10})
		if err != nil || len(events) != 1 || events[0].Status != "pending" {
			t.Fatalf("durable policy audit intent = %#v, %v", events, err)
		}
	})
}

func TestNotificationCredentialRotationRequiresDurableSecretFreeAudit(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	initial := server.runner.GetSystemSettings()
	initial.Notifications.Webhooks = []NotificationWebhookSettings{{
		Name: "Dynatrace", Enabled: true,
		URL: "https://dynatrace.example.test/api/v2/events/ingest", Format: notificationFormatDynatrace,
		APIToken: "old-audit-secret",
	}}
	if _, err := server.runner.UpdateSystemSettings(initial); err != nil {
		t.Fatalf("seed notification credential: %v", err)
	}

	rotated := server.runner.GetSystemSettings()
	rotated.Notifications.Webhooks[0].APIToken = "new-audit-secret"
	body, err := json.Marshal(rotated)
	if err != nil {
		t.Fatal(err)
	}
	server.store = storeOverlay(&selectiveAuditFailureStore{Store: store, action: "system.settings.update", status: "pending"})
	request := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
	withCurrentSystemSettingsIfMatch(request, server)
	response := httptest.NewRecorder()
	server.handleUpdateSystemSettings(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("credential intent audit response = %d %s", response.Code, response.Body.String())
	}
	if got := server.runner.GetSystemSettings().Notifications.Webhooks[0].APIToken; got != "old-audit-secret" {
		t.Fatalf("credential changed after required audit failed: %q", got)
	}

	server.store = store
	request = httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
	withCurrentSystemSettingsIfMatch(request, server)
	response = httptest.NewRecorder()
	server.handleUpdateSystemSettings(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("credential rotation response = %d %s", response.Code, response.Body.String())
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "system.settings.update", Limit: 10})
	if err != nil {
		t.Fatalf("list credential audit events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("credential audit events = %#v, want pending + success", events)
	}
	for _, event := range events {
		encoded, err := json.Marshal(event.Details)
		if err != nil {
			t.Fatalf("marshal audit details: %v", err)
		}
		if bytes.Contains(encoded, []byte("old-audit-secret")) || bytes.Contains(encoded, []byte("new-audit-secret")) {
			t.Fatalf("audit event exposed credential material: %s", encoded)
		}
		if changed, _ := event.Details["notification_credentials_changed"].(bool); !changed {
			t.Fatalf("credential audit event omitted change marker: %#v", event.Details)
		}
	}
}

func TestMergeOmittedSectionsPreservesThresholdsAndCapacity(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})

	maxErr := 2.5
	constrained := 6
	saturated := 2
	full := defaultSystemSettings(10)
	full.Thresholds.MaxErrorRatePct = &maxErr
	full.Thresholds.FleetConstrainedAvailableSlots = &constrained
	full.Thresholds.FleetSaturatedAvailableSlots = &saturated
	full.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 5
	full.CapacityPlanning.Reservations = []CapacityReservation{{ProjectID: "checkout", ReservedGenerators: 2}}
	full.ArchiveRetention.Enabled = true
	full.ArchiveRetention.DefaultRetentionDays = 45
	full.ScheduleDefaults.BlackoutStartTime = "00:00"
	full.ScheduleDefaults.BlackoutEndTime = "06:00"
	full.Notifications = NotificationsSettings{Webhooks: []NotificationWebhookSettings{{
		Name:    "Perf Alerts",
		Enabled: true,
		URL:     "https://hooks.slack.test/services/T000/B000/abc123",
		Format:  notificationFormatSlack,
		Events:  []string{"test_failed"},
	}}}

	body, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	putResp := httptest.NewRecorder()
	server.mux.ServeHTTP(putResp, withCurrentSystemSettingsIfMatch(httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body)), server))
	if putResp.Code != http.StatusOK {
		t.Fatalf("full PUT status = %d body=%s", putResp.Code, putResp.Body.String())
	}

	// A client that PUTs only the general section (e.g. a hand-written API
	// client) must not wipe any omitted section.
	partial := []byte(`{"general":{"api_endpoint":":9191","max_concurrent_tests":4,"metrics_interval_seconds":2}}`)
	partialResp := httptest.NewRecorder()
	server.mux.ServeHTTP(partialResp, withCurrentSystemSettingsIfMatch(httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(partial)), server))
	if partialResp.Code != http.StatusOK {
		t.Fatalf("partial PUT status = %d body=%s", partialResp.Code, partialResp.Body.String())
	}
	after := server.runner.GetSystemSettings()
	if after.General.APIEndpoint != ":9191" || after.General.MaxConcurrentTests != 4 {
		t.Fatalf("general section not applied: %+v", after.General)
	}
	if after.Thresholds.MaxErrorRatePct == nil || *after.Thresholds.MaxErrorRatePct != 2.5 {
		t.Fatalf("omitted thresholds wiped: max_error_rate_pct = %#v", after.Thresholds.MaxErrorRatePct)
	}
	if after.Thresholds.FleetConstrainedAvailableSlots == nil || *after.Thresholds.FleetConstrainedAvailableSlots != 6 {
		t.Fatalf("omitted thresholds wiped: fleet_constrained = %#v", after.Thresholds.FleetConstrainedAvailableSlots)
	}
	if len(after.CapacityPlanning.Reservations) != 1 || after.CapacityPlanning.Reservations[0].ProjectID != "checkout" {
		t.Fatalf("omitted capacity reservations wiped: %#v", after.CapacityPlanning.Reservations)
	}
	if after.CapacityPlanning.Availability.ConfiguredAvailableGenerators != 5 {
		t.Fatalf("omitted capacity availability wiped: %#v", after.CapacityPlanning.Availability)
	}
	if !after.ArchiveRetention.Enabled || after.ArchiveRetention.DefaultRetentionDays != 45 {
		t.Fatalf("omitted archive retention wiped: %#v", after.ArchiveRetention)
	}
	if len(after.Notifications.Webhooks) != 1 || after.Notifications.Webhooks[0].URL != "https://hooks.slack.test/services/T000/B000/abc123" {
		t.Fatalf("omitted notifications wiped: %#v", after.Notifications.Webhooks)
	}
	if after.ScheduleDefaults.BlackoutStartTime != "00:00" || after.ScheduleDefaults.BlackoutEndTime != "06:00" {
		t.Fatalf("omitted schedule defaults wiped: %#v", after.ScheduleDefaults)
	}

	// An explicit empty thresholds section is honored as a reset: SLA thresholds
	// clear, telemetry budgets return to defaults, everything else stays.
	reset := []byte(`{"thresholds":{}}`)
	resetResp := httptest.NewRecorder()
	server.mux.ServeHTTP(resetResp, withCurrentSystemSettingsIfMatch(httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(reset)), server))
	if resetResp.Code != http.StatusOK {
		t.Fatalf("explicit thresholds reset status = %d body=%s", resetResp.Code, resetResp.Body.String())
	}
	afterReset := server.runner.GetSystemSettings()
	if afterReset.Thresholds.MaxErrorRatePct != nil {
		t.Fatalf("explicit thresholds reset was not honored: %#v", afterReset.Thresholds.MaxErrorRatePct)
	}
	if afterReset.Thresholds.MaxTelemetrySampleAgeMs == nil || *afterReset.Thresholds.MaxTelemetrySampleAgeMs != defaultTelemetrySampleAgeBudgetMs {
		t.Fatalf("telemetry budgets not restored to defaults after reset: %#v", afterReset.Thresholds.MaxTelemetrySampleAgeMs)
	}
	if len(afterReset.Notifications.Webhooks) != 1 {
		t.Fatalf("sections omitted by the reset PUT were wiped: %#v", afterReset.Notifications.Webhooks)
	}
}

func TestMergeOmittedSystemSettingsSectionsIsComplete(t *testing.T) {
	current := defaultSystemSettings(5)
	maxErr := 3.5
	current.Thresholds.MaxErrorRatePct = &maxErr
	current.CapacityPlanning.Reservations = []CapacityReservation{{ProjectID: "checkout", ReservedGenerators: 1}}
	current.Notifications = NotificationsSettings{Webhooks: []NotificationWebhookSettings{{
		Name:   "hook",
		URL:    "https://hooks.slack.test/services/T0/B0/x",
		Format: notificationFormatSlack,
	}}}
	current.ScriptImports.AllowSourceSecretMigration = true
	current.GitHub.Enabled = true
	current.Prometheus.Enabled = true
	current.Grafana.Enabled = true
	current.PowerBI.Enabled = true

	merged := mergeOmittedSystemSettingsSections(SystemSettings{}, current, []byte(`{}`))

	currentValue := reflect.ValueOf(current)
	mergedValue := reflect.ValueOf(merged)
	settingsType := reflect.TypeOf(current)
	for i := 0; i < settingsType.NumField(); i++ {
		field := settingsType.Field(i)
		if field.Name == "Revision" {
			continue
		}
		if currentValue.Field(i).IsZero() {
			t.Fatalf("fixture section %s is the zero value; give it a non-default value in this test when adding a SystemSettings section so the omitted-section guard stays meaningful", field.Name)
		}
		if !reflect.DeepEqual(mergedValue.Field(i).Interface(), currentValue.Field(i).Interface()) {
			t.Fatalf("omitted section %s (json %q) was not preserved; add it to mergeOmittedSystemSettingsSections", field.Name, field.Tag.Get("json"))
		}
	}

	// Companion guard: a PRESENT section must be honored as sent, never merged
	// from current. This catches a merge entry keyed to the wrong JSON name,
	// which the omitted-body loop above cannot see.
	for i := 0; i < settingsType.NumField(); i++ {
		field := settingsType.Field(i)
		if field.Name == "Revision" {
			continue
		}
		tag := strings.Split(field.Tag.Get("json"), ",")[0]
		explicit := mergeOmittedSystemSettingsSections(SystemSettings{}, current, []byte(`{"`+tag+`": {}}`))
		if reflect.DeepEqual(reflect.ValueOf(explicit).Field(i).Interface(), currentValue.Field(i).Interface()) {
			t.Fatalf("explicit empty section %s (json %q) was overwritten from current; its merge entry is keyed to the wrong JSON name", field.Name, tag)
		}
		// encoding/json matches struct fields case-insensitively, so a
		// case-variant key decodes into the section and must count as present —
		// otherwise the merge would silently discard the caller's block.
		caseVariant := mergeOmittedSystemSettingsSections(SystemSettings{}, current, []byte(`{"`+strings.ToUpper(tag)+`": {}}`))
		if reflect.DeepEqual(reflect.ValueOf(caseVariant).Field(i).Interface(), currentValue.Field(i).Interface()) {
			t.Fatalf("case-variant present section %s (json %q) was treated as omitted and overwritten from current", field.Name, tag)
		}
	}
}

func TestStoredInvertedFleetThresholdsBlockSubsequentPuts(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	blob := defaultSystemSettings(10)
	saturated := 9
	constrained := 3
	blob.Thresholds.FleetSaturatedAvailableSlots = &saturated
	blob.Thresholds.FleetConstrainedAvailableSlots = &constrained
	encoded, err := json.Marshal(blob)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSetting(systemSettingsStoreKey, string(encoded)); err != nil {
		t.Fatalf("SaveSetting: %v", err)
	}

	// The pre-existing inverted pair loads (write-only rule), but the omitted-
	// section merge carries it into every subsequent PUT, which must 400 until
	// the pair is corrected — pin that intentional consequence.
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})
	partial := []byte(`{"general":{"api_endpoint":":9191","max_concurrent_tests":4,"metrics_interval_seconds":2}}`)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, withCurrentSystemSettingsIfMatch(httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(partial)), server))
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "fleet_saturated_available_slots") {
		t.Fatalf("partial PUT over an inverted stored pair = %d %s, want 400 naming the fleet ordering", resp.Code, resp.Body.String())
	}

	// Correcting the pair in the same PUT unblocks the write.
	fixed := []byte(`{"general":{"api_endpoint":":9191","max_concurrent_tests":4,"metrics_interval_seconds":2},"thresholds":{"fleet_constrained_available_slots":9,"fleet_saturated_available_slots":3}}`)
	resp = httptest.NewRecorder()
	server.mux.ServeHTTP(resp, withCurrentSystemSettingsIfMatch(httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(fixed)), server))
	if resp.Code != http.StatusOK {
		t.Fatalf("corrected PUT = %d %s, want 200", resp.Code, resp.Body.String())
	}
}

func TestLoadToleratesP99StopSamplesWithoutLevel(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	blob := defaultSystemSettings(10)
	samples := 3
	blob.Thresholds.P99LatencyStopConsecutiveSamples = &samples
	encoded, err := json.Marshal(blob)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSetting(systemSettingsStoreKey, string(encoded)); err != nil {
		t.Fatalf("SaveSetting: %v", err)
	}

	// The cross-field rule is write-only: a hand-edited blob carrying only the
	// stop counter must still load instead of resetting the blob to defaults.
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})
	loaded := server.runner.GetSystemSettings()
	if loaded.Thresholds.P99LatencyStopConsecutiveSamples == nil || *loaded.Thresholds.P99LatencyStopConsecutiveSamples != 3 {
		t.Fatalf("stored counter-only blob lost on load: %#v", loaded.Thresholds.P99LatencyStopConsecutiveSamples)
	}
}

func TestSystemSettingsUpdateRejectsInvertedFleetThresholds(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	settings := defaultSystemSettings(10)
	saturated := 5
	constrained := 5
	settings.Thresholds.FleetSaturatedAvailableSlots = &saturated
	settings.Thresholds.FleetConstrainedAvailableSlots = &constrained
	if _, err := runner.UpdateSystemSettings(settings); err == nil || !strings.Contains(err.Error(), "thresholds.fleet_saturated_available_slots must be less than thresholds.fleet_constrained_available_slots") {
		t.Fatalf("inverted fleet thresholds accepted, err=%v", err)
	}

	ordered := 2
	settings.Thresholds.FleetSaturatedAvailableSlots = &ordered
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("ordered fleet thresholds rejected: %v", err)
	}
}

func TestLoadToleratesInvertedFleetThresholds(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	blob := defaultSystemSettings(10)
	blob.General.APIEndpoint = ":9797"
	saturated := 9
	constrained := 3
	blob.Thresholds.FleetSaturatedAvailableSlots = &saturated
	blob.Thresholds.FleetConstrainedAvailableSlots = &constrained
	encoded, err := json.Marshal(blob)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSetting(systemSettingsStoreKey, string(encoded)); err != nil {
		t.Fatalf("SaveSetting: %v", err)
	}

	// The ordering rule is write-time only. A pre-existing inverted pair must
	// keep loading; leaking it into load validation would silently reset the
	// whole blob to defaults.
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})
	loaded := server.runner.GetSystemSettings()
	if loaded.General.APIEndpoint != ":9797" {
		t.Fatalf("stored settings were reset to defaults on load: %+v", loaded.General)
	}
	if loaded.Thresholds.FleetSaturatedAvailableSlots == nil || *loaded.Thresholds.FleetSaturatedAvailableSlots != 9 {
		t.Fatalf("stored inverted fleet threshold lost on load: %#v", loaded.Thresholds.FleetSaturatedAvailableSlots)
	}
}

func TestSystemSettingsUpdateRejectsReservationWithoutProject(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	settings := defaultSystemSettings(10)
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 4
	settings.CapacityPlanning.Reservations = []CapacityReservation{{ReservedGenerators: 2}}
	if _, err := runner.UpdateSystemSettings(settings); err == nil || !strings.Contains(err.Error(), "capacity_planning.reservations[0].project_id is required") {
		t.Fatalf("reservation without project accepted, err=%v", err)
	}
}

func TestSystemSettingsUpdateNormalizesReservationsOnWrite(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})

	settings := defaultSystemSettings(10)
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 5
	settings.CapacityPlanning.Reservations = []CapacityReservation{
		{ProjectID: " checkout ", ReservedGenerators: 2},
		// Zero generators with a project is the documented delete-row idiom: it
		// validates, then normalization drops it before persisting.
		{ProjectID: "reporting", ReservedGenerators: 0},
	}
	// The SQLite store implements the capacity fence, so this write also proves
	// the fenced path accepts a body containing a to-be-dropped zero-count row.
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}

	raw, err := store.GetSetting(systemSettingsStoreKey)
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if !strings.Contains(raw, `"project_id":"checkout"`) {
		t.Fatalf("persisted blob does not contain the trimmed project id: %s", raw)
	}
	if strings.Contains(raw, "reporting") {
		t.Fatalf("persisted blob retains the zero-count reservation: %s", raw)
	}

	// On-disk shape == post-restart shape: reloading through the normalize path
	// must be a no-op for what this write persisted.
	before := server.runner.GetSystemSettings()
	reopened := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})
	afterRestart := reopened.runner.GetSystemSettings()
	if !reflect.DeepEqual(before.CapacityPlanning, afterRestart.CapacityPlanning) {
		t.Fatalf("capacity settings changed across restart:\nbefore = %#v\nafter  = %#v", before.CapacityPlanning, afterRestart.CapacityPlanning)
	}
}

func TestHeadroomZeroSurvivesReload(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})

	settings := defaultSystemSettings(10)
	settings.CapacityPlanning.HeadroomPct = 0
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("headroom 0 rejected on write: %v", err)
	}
	if got := server.runner.GetSystemSettings().CapacityPlanning.HeadroomPct; got != 0 {
		t.Fatalf("headroom after write = %v, want 0", got)
	}

	reopened := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})
	if got := reopened.runner.GetSystemSettings().CapacityPlanning.HeadroomPct; got != 0 {
		t.Fatalf("headroom after reload = %v, want the explicit 0 preserved (not backfilled to the default)", got)
	}
}

func TestNormalizeLoadedSystemSettingsKeepsExplicitZeroHeadroom(t *testing.T) {
	base := SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     4,
			MetricsIntervalSeconds: 2,
		},
	}

	legacy := normalizeLoadedSystemSettings(base, `{"capacity_planning":{"generator":{"cpu_cores":4}}}`, 4)
	if legacy.CapacityPlanning.HeadroomPct != 20 {
		t.Fatalf("legacy blob without headroom key = %v, want backfilled 20", legacy.CapacityPlanning.HeadroomPct)
	}

	explicit := normalizeLoadedSystemSettings(base, `{"capacity_planning":{"headroom_pct":0}}`, 4)
	if explicit.CapacityPlanning.HeadroomPct != 0 {
		t.Fatalf("explicit zero headroom = %v, want 0 preserved", explicit.CapacityPlanning.HeadroomPct)
	}
}

func TestSystemSettingsFromStoredValueKeepsExplicitZeroHeadroom(t *testing.T) {
	blob := defaultSystemSettings(10)
	blob.CapacityPlanning.HeadroomPct = 0
	encoded, err := json.Marshal(blob)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := systemSettingsFromStoredValue(string(encoded), defaultSystemSettings(10))
	if err != nil {
		t.Fatalf("systemSettingsFromStoredValue: %v", err)
	}
	if loaded.CapacityPlanning.HeadroomPct != 0 {
		t.Fatalf("fence decode headroom = %v, want the stored 0 (not resurrected to 20)", loaded.CapacityPlanning.HeadroomPct)
	}
}

func TestSystemSettingsAllowsZeroTelemetryBudgetAsDisabled(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})

	settings := defaultSystemSettings(10)
	zero := 0.0
	settings.Thresholds.MaxTelemetrySampleAgeMs = &zero
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("zero telemetry budget rejected: %v", err)
	}
	if got := server.runner.GetSystemSettings().Thresholds.MaxTelemetrySampleAgeMs; got == nil || *got != 0 {
		t.Fatalf("stored budget = %#v, want explicit 0", got)
	}

	reopened := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})
	if got := reopened.runner.GetSystemSettings().Thresholds.MaxTelemetrySampleAgeMs; got == nil || *got != 0 {
		t.Fatalf("budget after reload = %#v, want explicit 0 (not refilled with the default)", got)
	}

	negative := -1.0
	settings.Thresholds.MaxTelemetrySampleAgeMs = &negative
	if _, err := server.runner.UpdateSystemSettings(settings); err == nil || !strings.Contains(err.Error(), "0 disables this telemetry budget") {
		t.Fatalf("negative telemetry budget accepted, err=%v", err)
	}
}

func TestAppendTelemetryBudgetDiagnosticSkipsDisabledBudget(t *testing.T) {
	var conditions, summaries, signatureParts []string
	severity := "healthy"
	disabled := 0.0
	appendTelemetryBudgetDiagnostic(&conditions, &summaries, &signatureParts, &severity, "stale_sample", "sample age", 99999, &disabled)
	if len(conditions) != 0 || severity != "healthy" {
		t.Fatalf("disabled budget produced a diagnostic: conditions=%v severity=%s", conditions, severity)
	}

	budget := 100.0
	appendTelemetryBudgetDiagnostic(&conditions, &summaries, &signatureParts, &severity, "stale_sample", "sample age", 250, &budget)
	if len(conditions) != 1 || severity != "warning" {
		t.Fatalf("active budget did not fire: conditions=%v severity=%s", conditions, severity)
	}
}
