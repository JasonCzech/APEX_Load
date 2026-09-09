package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConnectionDiagnosticsSavedRevisionAndProviderResponse(t *testing.T) {
	for _, tc := range []struct {
		provider, body string
		want           int
	}{{"prometheus", `{"status":"success","data":{}}`, 200}, {"prometheus", `{"status":"error"}`, 502}, {"grafana", `[]`, 200}, {"grafana", `{"login":"required"}`, 502}, {"opensearch", `{"version":{},"cluster_name":"test"}`, 200}, {"opensearch", `<html>login</html>`, 502}} {
		t.Run(tc.provider+tc.body, func(t *testing.T) {
			hits := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if tc.provider == "grafana" && r.Header.Get("Authorization") != "Bearer token" {
					t.Error("missing saved token")
				}
				fmt.Fprint(w, tc.body)
			}))
			defer upstream.Close()
			t.Setenv("TEST_SETTINGS_GRAFANA_TOKEN", "token")
			s := NewServer(ServerConfig{})
			settings := s.runner.GetSystemSettings()
			settings.Prometheus = PrometheusSettings{Enabled: true, BaseURL: upstream.URL, TimeoutSeconds: 5}
			settings.Grafana = GrafanaSettings{Enabled: true, BaseURL: upstream.URL, ServiceAccountTokenEnvVar: "TEST_SETTINGS_GRAFANA_TOKEN", TimeoutSeconds: 5}
			settings.OpenSearch = OpenSearchSettings{Enabled: true, BaseURL: upstream.URL, TimeoutSeconds: 5}
			// Runtime settings are injected here to isolate read-only diagnostics from
			// unrelated settings-write validation and durable storage.
			settings.Revision = "saved-r1"
			s.runner.mu.Lock()
			s.runner.settings = settings
			s.runner.mu.Unlock()
			for _, revision := range []string{"stale", systemSettingsRevision(settings)} {
				w := httptest.NewRecorder()
				s.handleConnectionTest(w, httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"provider":%q,"expected_revision":%q}`, tc.provider, revision))))
				want := tc.want
				if revision == "stale" {
					want = 409
					if hits != 0 {
						t.Fatal("stale test contacted a destination")
					}
				}
				if w.Code != want {
					t.Fatalf("status %d want %d: %s", w.Code, want, w.Body.String())
				}
				if strings.Contains(w.Body.String(), "token") || strings.Contains(w.Body.String(), upstream.URL) {
					t.Fatal("diagnostics leaked private configuration")
				}
			}
		})
	}
}
