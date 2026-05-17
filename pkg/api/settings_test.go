package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/storage"
)

func TestSystemSettingsAPIAndPersistence(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stormrunner.db"))
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
	if len(initial.Notifications.Webhooks) != 0 {
		t.Fatalf("initial notifications = %#v, want none", initial.Notifications.Webhooks)
	}
	if initial.Auth.Mode != authModeDev {
		t.Fatalf("initial auth mode = %q, want %q", initial.Auth.Mode, authModeDev)
	}
	if initial.Auth.UserHeader != "X-Auth-Request-User" {
		t.Fatalf("initial auth user header = %q, want X-Auth-Request-User", initial.Auth.UserHeader)
	}

	maxErr := 1.5
	maxP99 := 1800.0
	maxServer := 350.0
	maxTransfer := 120.0
	update := SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":9090",
			MaxConcurrentTests:     3,
			MetricsIntervalSeconds: 2,
		},
		Thresholds: ThresholdSettings{
			MaxErrorRatePct:          &maxErr,
			MaxP99LatencyMs:          &maxP99,
			MaxAvgServerProcessingMs: &maxServer,
			MaxAvgTransferMs:         &maxTransfer,
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
			LoginURL:           "https://login.example.test/stormrunner/start",
			LogoutURL:          "/logout",
			UserHeader:         "X-Test-User",
			NameHeader:         "X-Test-Name",
			EmailHeader:        "X-Test-Email",
			RolesHeader:        "X-Test-Roles",
			ProjectRolesHeader: "X-Test-Project-Roles",
		},
	}
	body, _ := json.Marshal(update)
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/system/settings", bytes.NewReader(body))
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
	if updated.CapacityPlanning.HeadroomPct != 25 {
		t.Fatalf("headroom = %v, want 25", updated.CapacityPlanning.HeadroomPct)
	}
	if updated.CapacityPlanning.Generator.MemoryMB != 16384 {
		t.Fatalf("generator memory = %d, want 16384", updated.CapacityPlanning.Generator.MemoryMB)
	}
	if updated.CapacityPlanning.Availability.ConfiguredAvailableGenerators != 6 {
		t.Fatalf("configured available generators = %d, want 6", updated.CapacityPlanning.Availability.ConfiguredAvailableGenerators)
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
	if updated.Auth.LoginURL != "https://login.example.test/stormrunner/start" {
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
	if persisted.CapacityPlanning.Availability.Source != capacityAvailabilityConfiguredPlusLive {
		t.Fatalf("persisted availability source = %q, want %q", persisted.CapacityPlanning.Availability.Source, capacityAvailabilityConfiguredPlusLive)
	}
	if len(persisted.Notifications.Webhooks) != 1 || persisted.Notifications.Webhooks[0].URL != "https://hooks.slack.test/services/T000/B000/abc123" {
		t.Fatalf("persisted notifications = %#v, want webhook URL", persisted.Notifications.Webhooks)
	}
	if persisted.Auth.ProjectRolesHeader != "X-Test-Project-Roles" {
		t.Fatalf("persisted project roles header = %q, want X-Test-Project-Roles", persisted.Auth.ProjectRolesHeader)
	}
	if persisted.Auth.LoginURL != "https://login.example.test/stormrunner/start" {
		t.Fatalf("persisted login url = %q, want configured login url", persisted.Auth.LoginURL)
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
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusBadRequest)
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
			ClientID:            "stormrunner-dashboard",
			ClientSecretEnvVar:  "STORMRUNNER_OIDC_CLIENT_SECRET",
			RedirectURL:         "https://stormrunner.example.test/api/v1/auth/callback",
			SessionSecretEnvVar: "STORMRUNNER_AUTH_SESSION_SECRET",
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
			ClientID:    "stormrunner-dashboard",
			RedirectURL: "https://stormrunner.example.test/api/v1/auth/callback",
		},
	})
	if err == nil {
		t.Fatal("expected missing oidc secret settings to fail validation")
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
	}, 4)

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
	if normalized.Auth.RolesHeader != "X-Stormrunner-Roles" {
		t.Fatalf("normalized roles header = %q, want X-Stormrunner-Roles", normalized.Auth.RolesHeader)
	}
	if len(normalized.Auth.Scopes) != 3 || normalized.Auth.Scopes[0] != "openid" {
		t.Fatalf("normalized auth scopes = %#v, want default oidc scopes", normalized.Auth.Scopes)
	}
	if normalized.Auth.SessionSecretEnvVar != "STORMRUNNER_AUTH_SESSION_SECRET" {
		t.Fatalf("normalized session secret env var = %q, want STORMRUNNER_AUTH_SESSION_SECRET", normalized.Auth.SessionSecretEnvVar)
	}

	normalizedAlias := normalizeLoadedSystemSettings(SystemSettings{
		General: GeneralSettings{
			APIEndpoint:            ":8080",
			MaxConcurrentTests:     4,
			MetricsIntervalSeconds: 2,
		},
		Thresholds: ThresholdSettings{},
		Auth:       AuthSettings{Mode: authModeDisabled},
	}, 4)
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
