package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// Fresh SSL imports are runnable migrations; older saved TLS gates still use
// guided repair. Exercise both import routes through protected-candidate save.
func TestEnterpriseArchiveLegacySSLMigrationWarnsAndSavesWithoutRepair(t *testing.T) {
	for _, route := range []string{"", "/validate"} {
		t.Run(route, func(t *testing.T) {
			server := newGuidedRepairArchiveServer(t, nil)
			source := `Action()
{
 web_set_sockets_option("SSL_VERSION", "2&3");
 web_add_header("Authorization", "Bearer ssl-migration-synthetic-token");
 web_url("orders", "URL=https://example.test/orders", "Resource=0", LAST);
 return 0;
}`
			archive := buildEnterpriseArchiveProtocolArchive(t, "Web", source)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive"+route, bytes.NewReader(archive))
			request.Header.Set("Content-Type", "application/zip")
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("import = %d: %s", response.Code, response.Body.String())
			}
			var body struct {
				Script   *dsl.Script `json:"script"`
				Warnings []string    `json:"warnings"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Script == nil {
				t.Fatal("import returned no script")
			}
			if tls := body.Script.Config.HTTP.TLS; tls.MinVersion != "1.3" || tls.MaxVersion != "1.3" {
				t.Fatalf("TLS = %#v, want TLS 1.3", tls)
			}
			warnings := strings.Join(body.Warnings, "\n")
			if !strings.Contains(warnings, `"2&3" was replaced with TLS 1.3`) {
				t.Fatalf("migration warning missing: %s", warnings)
			}
			findings, err := importreview.FindingsFromScript(body.Script)
			if err != nil {
				t.Fatal(err)
			}
			for _, finding := range findings {
				if finding.Gate == importreview.GateExecution {
					t.Fatalf("migration left an execution gate: %+v", finding)
				}
			}
			if preflight := preservedReplayPreflight(body.Script); preflight == nil || !preflight.Valid {
				t.Fatalf("migration preflight = %+v, want runnable", preflight)
			}
			var draft struct {
				Script map[string]any `json:"script"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &draft); err != nil {
				t.Fatal(err)
			}
			saved := postEnterpriseArchiveBatch(t, server, map[string]any{"operation_id": fmt.Sprintf("ssl-migration-%s", strings.TrimPrefix(route, "/")), "scripts": []any{draft.Script}})
			if saved.Code != http.StatusCreated {
				t.Fatalf("save = %d: %s", saved.Code, saved.Body.String())
			}
			stored := storedEnterpriseBatchScript(t, server, saved)
			if tls := stored.Config.HTTP.TLS; tls.MinVersion != "1.3" || tls.MaxVersion != "1.3" {
				t.Fatalf("saved TLS = %#v, want TLS 1.3", tls)
			}
			if gates := executionGatePaths(stored, "legacy_tls_replacement"); len(gates) != 0 {
				t.Fatalf("saved TLS gates = %v", gates)
			}
		})
	}
}
