package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateAuthSettingsProductionRejectsBypass(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "production")
	t.Setenv("APEXLOAD_REQUIRE_AUTH", "")

	for _, mode := range []string{authModeDev, authModeDisabled, ""} {
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
