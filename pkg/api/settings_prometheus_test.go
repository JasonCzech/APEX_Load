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

	// normalize is FAITHFUL to Enabled: it fills the base URL/timeout defaults
	// but never silently re-enables. A zero (e.g. explicitly disabled) section
	// stays disabled even while the env URL is set — the env-default-enable is
	// applied only at the defaults/load layers where presence is known.
	seeded := normalizePrometheusSettings(PrometheusSettings{})
	if seeded.Enabled {
		t.Fatalf("normalize must not auto-enable a disabled section: %+v", seeded)
	}
	if seeded.BaseURL != "http://prometheus:9090" || seeded.TimeoutSeconds != 5 {
		t.Fatalf("zero prometheus settings normalized to %+v, want env base URL + default timeout", seeded)
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

// TestPrometheusExplicitDisablePersists is the core regression for the
// "can never disable while env URL is set" footgun: an admin disable must
// survive UpdateSystemSettings + GetSystemSettings even with the env URL set.
func TestPrometheusExplicitDisablePersists(t *testing.T) {
	t.Setenv("APEXLOAD_PROMETHEUS_URL", "http://prometheus:9090")

	server := NewServer(ServerConfig{})
	settings := server.runner.GetSystemSettings()
	if !settings.Prometheus.Enabled {
		t.Fatalf("expected env-defaulted prometheus to start enabled, got %+v", settings.Prometheus)
	}

	settings.Prometheus = PrometheusSettings{Enabled: false}
	updated, err := server.runner.UpdateSystemSettings(settings)
	if err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	if updated.Prometheus.Enabled {
		t.Fatalf("explicit disable was overridden on write: %+v", updated.Prometheus)
	}
	if got := server.runner.GetSystemSettings().Prometheus; got.Enabled {
		t.Fatalf("explicit disable did not persist: %+v", got)
	}
}

func TestRawJSONHasKey(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		key  string
		want bool
	}{
		{name: "present", raw: `{"prometheus":{"enabled":false}}`, key: "prometheus", want: true},
		{name: "present null", raw: `{"prometheus":null}`, key: "prometheus", want: true},
		{name: "absent", raw: `{"github":{}}`, key: "prometheus", want: false},
		{name: "invalid json", raw: `not json`, key: "prometheus", want: false},
		{name: "empty object", raw: `{}`, key: "prometheus", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rawJSONHasKey(tc.raw, tc.key); got != tc.want {
				t.Fatalf("rawJSONHasKey(%q, %q) = %v, want %v", tc.raw, tc.key, got, tc.want)
			}
		})
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
