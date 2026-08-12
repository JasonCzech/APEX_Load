package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateAuthSettingsProductionRejectsBypass(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	t.Setenv("APEXLOAD_REQUIRE_AUTH", "")

	for _, mode := range []string{authModeDev, authModeDisabled, authModeLocal, ""} {
		if err := validateAuthSettings(AuthSettings{Mode: mode}); err == nil {
			t.Fatalf("auth mode %q must be rejected in production", mode)
		}
	}

	// A fully configured OIDC setup remains valid in production.
	oidc := AuthSettings{
		Mode:                authModeOIDC,
		IssuerURL:           "https://issuer.example.com",
		ClientID:            "client",
		ClientSecretEnvVar:  "APEXLOAD_OIDC_SECRET",
		RedirectURL:         "https://app.example.com/callback",
		SessionSecretEnvVar: "APEXLOAD_AUTH_SESSION_SECRET",
		Scopes:              []string{"openid"},
	}
	if err := validateAuthSettings(oidc); err != nil {
		t.Fatalf("oidc settings should be valid in production: %v", err)
	}
}

func TestValidateAuthSettingsDevAllowedOutsideProduction(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "")
	t.Setenv("APEXLOAD_REQUIRE_AUTH", "")
	if err := validateAuthSettings(AuthSettings{Mode: authModeDev}); err != nil {
		t.Fatalf("dev mode should be permitted outside production: %v", err)
	}
	if err := validateAuthSettings(AuthSettings{Mode: authModeLocal}); err != nil {
		t.Fatalf("local mode should be permitted outside production: %v", err)
	}
}

func TestValidateAuthSettingsRequireAuthRejectsLocal(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "")
	t.Setenv("APEXLOAD_REQUIRE_AUTH", "true")
	if err := validateAuthSettings(AuthSettings{Mode: authModeLocal}); err == nil {
		t.Fatal("local mode must be rejected when APEXLOAD_REQUIRE_AUTH enables production posture")
	}
}

func TestServerStartupPostureRejectsPersistedLocalMode(t *testing.T) {
	tests := []struct {
		name        string
		environment string
		requireAuth string
	}{
		{name: "production environment", environment: "prod"},
		{name: "required authentication posture", requireAuth: "true"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Persist the setting while the non-production validator permits it,
			// then simulate the process being restarted under production posture.
			t.Setenv("APEXLOAD_ENV", "")
			t.Setenv("APEXLOAD_REQUIRE_AUTH", "")
			server := NewServer(ServerConfig{})
			settings := server.runner.GetSystemSettings()
			settings.Auth.Mode = authModeLocal
			if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
				t.Fatalf("persist local mode: %v", err)
			}
			t.Setenv("APEXLOAD_ENV", test.environment)
			t.Setenv("APEXLOAD_REQUIRE_AUTH", test.requireAuth)
			if err := server.enforceAuthPosture(); err == nil {
				t.Fatal("startup posture accepted persisted Local Test Login")
			}
		})
	}
}

func TestDevPreviewIgnoredInProduction(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tests", nil)
	r.Header.Set(devViewHeader, devViewModeAdmin)
	settings := AuthSettings{Mode: authModeDev}

	t.Run("honored outside production", func(t *testing.T) {
		t.Setenv("APEXLOAD_ENV", "")
		t.Setenv("APEXLOAD_REQUIRE_AUTH", "")
		if _, ok := devPreviewModeForRequest(settings, r); !ok {
			t.Fatal("dev preview should be honored outside production")
		}
	})

	t.Run("ignored in production", func(t *testing.T) {
		t.Setenv("APEXLOAD_ENV", "production")
		if _, ok := devPreviewModeForRequest(settings, r); ok {
			t.Fatal("dev preview header must be ignored in production")
		}
	})
}
