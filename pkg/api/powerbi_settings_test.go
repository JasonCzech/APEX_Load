package api

import (
	"strings"
	"testing"
)

func validEnabledPowerBISettings() PowerBISettings {
	return PowerBISettings{
		Enabled:            true,
		TenantID:           "tenant-1",
		ClientID:           "client-1",
		ClientSecretEnvVar: "POWERBI_CLIENT_SECRET",
		WorkspaceID:        "11111111-2222-3333-4444-555555555555",
	}
}

func TestNormalizePowerBISettingsDefaults(t *testing.T) {
	out := normalizePowerBISettings(PowerBISettings{})
	if out.DatasetName != defaultPowerBIDatasetName {
		t.Fatalf("dataset name default missing: %q", out.DatasetName)
	}
	if out.AuthBaseURL != defaultPowerBIAuthBaseURL || out.APIBaseURL != defaultPowerBIAPIBaseURL {
		t.Fatalf("url defaults missing: %q %q", out.AuthBaseURL, out.APIBaseURL)
	}
	if out.Scope != defaultPowerBIScope {
		t.Fatalf("scope default missing: %q", out.Scope)
	}
	if out.TimeoutSeconds != 30 {
		t.Fatalf("timeout default missing: %d", out.TimeoutSeconds)
	}

	trimmed := normalizePowerBISettings(PowerBISettings{
		TenantID:    "  tenant  ",
		AuthBaseURL: "https://login.contoso.local/ ",
		APIBaseURL:  " https://bi.contoso.local/v1.0/myorg/",
	})
	if trimmed.TenantID != "tenant" {
		t.Fatalf("tenant not trimmed: %q", trimmed.TenantID)
	}
	if strings.HasSuffix(trimmed.AuthBaseURL, "/") || strings.HasSuffix(trimmed.APIBaseURL, "/") {
		t.Fatalf("urls not trimmed: %q %q", trimmed.AuthBaseURL, trimmed.APIBaseURL)
	}
}

func TestValidatePowerBISettings(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*PowerBISettings)
		wantErr string
	}{
		{name: "disabled empty is valid", mutate: func(s *PowerBISettings) { *s = PowerBISettings{} }},
		{name: "valid enabled", mutate: func(s *PowerBISettings) {}},
		{name: "timeout too large", mutate: func(s *PowerBISettings) { s.TimeoutSeconds = 61 }, wantErr: "timeout_seconds"},
		{name: "bad env var name", mutate: func(s *PowerBISettings) { s.ClientSecretEnvVar = "BAD NAME" }, wantErr: "client_secret_env_var"},
		{name: "missing tenant", mutate: func(s *PowerBISettings) { s.TenantID = "" }, wantErr: "required"},
		{name: "missing client id", mutate: func(s *PowerBISettings) { s.ClientID = "" }, wantErr: "required"},
		{name: "missing workspace", mutate: func(s *PowerBISettings) { s.WorkspaceID = "" }, wantErr: "required"},
		{name: "workspace not a guid", mutate: func(s *PowerBISettings) { s.WorkspaceID = "not-a-guid" }, wantErr: "GUID"},
		{name: "api url with credentials", mutate: func(s *PowerBISettings) { s.APIBaseURL = "https://user:pass@api.powerbi.com/v1.0/myorg" }, wantErr: "api_base_url"},
		{name: "auth url bad scheme", mutate: func(s *PowerBISettings) { s.AuthBaseURL = "ftp://login.microsoftonline.com" }, wantErr: "auth_base_url"},
		{name: "internal private host allowed", mutate: func(s *PowerBISettings) {
			s.AuthBaseURL = "https://login.bi.internal"
			s.APIBaseURL = "https://powerbi.bi.internal/v1.0/myorg"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := validEnabledPowerBISettings()
			tc.mutate(&settings)
			err := validatePowerBISettings(settings)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestPowerBISettingsAllowlistHosts(t *testing.T) {
	settings := normalizePowerBISettings(PowerBISettings{
		AuthBaseURL: "https://login.bi.internal",
		APIBaseURL:  "https://powerbi.bi.internal/v1.0/myorg",
	})
	hosts := settings.allowlistHosts()
	if len(hosts) != 2 || hosts[0] != "login.bi.internal" || hosts[1] != "powerbi.bi.internal" {
		t.Fatalf("unexpected allowlist hosts: %v", hosts)
	}
}

func TestMergeOmittedSystemSettingsSectionsPreservesIntegrations(t *testing.T) {
	current := defaultSystemSettings(5)
	current.PowerBI = validEnabledPowerBISettings()
	current.Grafana = GrafanaSettings{Enabled: true, BaseURL: "https://grafana.internal", ServiceAccountTokenEnvVar: "APEXLOAD_GRAFANA_TOKEN", TimeoutSeconds: 10}

	// A pre-feature client PUTs a body without the powerbi/grafana keys; the
	// zero-value sections it decodes to must not clobber the stored config.
	requested := defaultSystemSettings(5)
	rawBody := []byte(`{"general":{"api_endpoint":":8080"}}`)
	merged := mergeOmittedSystemSettingsSections(requested, current, rawBody)
	if !merged.PowerBI.Enabled || merged.PowerBI.TenantID != "tenant-1" {
		t.Fatalf("omitted powerbi section was not preserved: %+v", merged.PowerBI)
	}
	if !merged.Grafana.Enabled || merged.Grafana.BaseURL != "https://grafana.internal" {
		t.Fatalf("omitted grafana section was not preserved: %+v", merged.Grafana)
	}

	// An explicit powerbi key (even disabled) is respected as given.
	explicit := []byte(`{"powerbi":{"enabled":false}}`)
	merged = mergeOmittedSystemSettingsSections(requested, current, explicit)
	if merged.PowerBI.Enabled {
		t.Fatalf("explicit powerbi disable was overridden: %+v", merged.PowerBI)
	}
}

func TestPowerBISettingsSurviveSystemSettingsNormalize(t *testing.T) {
	in := defaultSystemSettings(5)
	in.PowerBI = validEnabledPowerBISettings()
	out := normalizeLoadedSystemSettings(in, "", 5)
	if !out.PowerBI.Enabled || out.PowerBI.TenantID != "tenant-1" {
		t.Fatalf("powerbi settings lost in normalize: %+v", out.PowerBI)
	}
	if out.PowerBI.DatasetName != defaultPowerBIDatasetName {
		t.Fatalf("powerbi defaults not applied in normalize: %+v", out.PowerBI)
	}
	if err := validateSystemSettings(out); err != nil {
		t.Fatalf("system settings with powerbi should validate: %v", err)
	}
}
