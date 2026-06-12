package api

import "testing"

func TestDefaultSystemSettingsIncludesPrometheusDefaults(t *testing.T) {
	t.Setenv("APEXLOAD_PROMETHEUS_URL", "")

	prom := defaultSystemSettings(10).Prometheus
	if prom.Enabled {
		t.Fatal("prometheus integration should default to disabled without an env URL")
	}
	if prom.BaseURL != "" {
		t.Fatalf("base url = %q, want empty", prom.BaseURL)
	}
	if prom.TimeoutSeconds != 5 {
		t.Fatalf("timeout_seconds = %d, want 5", prom.TimeoutSeconds)
	}
}

func TestDefaultPrometheusSettingsSeedsBaseURLFromEnv(t *testing.T) {
	t.Setenv("APEXLOAD_PROMETHEUS_URL", " http://prometheus:9090/ ")

	prom := defaultSystemSettings(10).Prometheus
	if !prom.Enabled {
		t.Fatal("prometheus integration should default to enabled when env URL is present")
	}
	if prom.BaseURL != "http://prometheus:9090" {
		t.Fatalf("base url = %q, want env URL without trailing slash", prom.BaseURL)
	}
	if prom.TimeoutSeconds != 5 {
		t.Fatalf("timeout_seconds = %d, want 5", prom.TimeoutSeconds)
	}
}

func TestNormalizePrometheusSettings(t *testing.T) {
	t.Setenv("APEXLOAD_PROMETHEUS_URL", " http://prometheus:9090/ ")

	seeded := normalizePrometheusSettings(PrometheusSettings{})
	if !seeded.Enabled || seeded.BaseURL != "http://prometheus:9090" || seeded.TimeoutSeconds != 5 {
		t.Fatalf("zero prometheus settings normalized to %+v, want enabled env defaults", seeded)
	}

	disabled := normalizePrometheusSettings(PrometheusSettings{Enabled: false, TimeoutSeconds: 10})
	if disabled.Enabled {
		t.Fatalf("explicitly disabled prometheus settings should stay disabled: %+v", disabled)
	}
	if disabled.BaseURL != "http://prometheus:9090" {
		t.Fatalf("base url = %q, want env URL retained for later enablement", disabled.BaseURL)
	}

	trimmed := normalizePrometheusSettings(PrometheusSettings{Enabled: true, BaseURL: " http://example.com:9090/ ", TimeoutSeconds: 7})
	if trimmed.BaseURL != "http://example.com:9090" || trimmed.TimeoutSeconds != 7 {
		t.Fatalf("prometheus settings were not trimmed/preserved: %+v", trimmed)
	}
}

func TestValidatePrometheusSettings(t *testing.T) {
	t.Setenv("APEXLOAD_PROMETHEUS_URL", "")

	if err := validatePrometheusSettings(PrometheusSettings{Enabled: false}); err != nil {
		t.Fatalf("disabled prometheus should validate, got: %v", err)
	}
	if err := validatePrometheusSettings(PrometheusSettings{Enabled: true, TimeoutSeconds: 5}); err == nil {
		t.Fatal("enabled prometheus without base_url should fail")
	}
	if err := validatePrometheusSettings(PrometheusSettings{Enabled: true, BaseURL: "ftp://prometheus:9090", TimeoutSeconds: 5}); err == nil {
		t.Fatal("non-http prometheus base_url should fail")
	}
	if err := validatePrometheusSettings(PrometheusSettings{Enabled: false, TimeoutSeconds: -1}); err == nil {
		t.Fatal("negative timeout should fail even when prometheus is disabled")
	}
	if err := validatePrometheusSettings(PrometheusSettings{Enabled: true, BaseURL: "http://prometheus:9090", TimeoutSeconds: 61}); err == nil {
		t.Fatal("timeout above 60 seconds should fail")
	}
	if err := validatePrometheusSettings(PrometheusSettings{Enabled: true, BaseURL: "http://localhost:9090", TimeoutSeconds: 5}); err != nil {
		t.Fatalf("configured prometheus host should be allowlisted for validation, got: %v", err)
	}
}
