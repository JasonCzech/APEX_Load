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

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/storage"
)

func proxyRepairFixture(mode string) *dsl.Script {
	script := &dsl.Script{ID: "proxy-choice", Name: "Proxy choice", Version: "1.0", Protocol: "http", ProjectID: storage.DefaultProjectID,
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test", HTTP: dsl.HTTPConfig{ProxyMode: mode, Proxy: dsl.ProxyConfig{Username: "{{secret.ENTERPRISE_PROXY_USERNAME}}", Password: "{{secret.ENTERPRISE_PROXY_PASSWORD}}", NoProxy: []string{"localhost"}}}},
		Init:    []dsl.Action{encryptedProxyGateForTest(), encryptedProxyGateForTest()},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Method: "GET", Target: "/home"}},
	}
	if mode == "pac" {
		script.Config.HTTP.Proxy.PACURL = "https://proxy.example.test/proxy.pac"
	} else {
		script.Config.HTTP.Proxy.URL = "http://proxy.example.test:8080"
	}
	ensureScriptImportPreserved(script, enterpriseArchiveSaveProfile)["proxy_source_evidence"] = "protected original source retained"
	return script
}

func assertProxyExcluded(t *testing.T, script *dsl.Script, operation, mode string) {
	t.Helper()
	proxy := script.Config.HTTP.Proxy
	if proxy.Username != "" || proxy.Password != "" {
		t.Fatal("proxy credentials survived exclusion")
	}
	if operation == "disable_imported_proxy" {
		if script.Config.HTTP.ProxyMode != "none" || !reflect.DeepEqual(proxy, dsl.ProxyConfig{}) {
			t.Fatalf("proxy was not disabled: %#v", script.Config.HTTP)
		}
	} else if script.Config.HTTP.ProxyMode != mode || len(proxy.NoProxy) != 1 || (mode == "pac" && proxy.PACURL == "") || (mode == "custom" && proxy.URL == "") {
		t.Fatal("credential exclusion changed proxy routing")
	}
	if len(executionGatePaths(script, "encrypted_proxy_password")) != 0 {
		t.Fatal("a repeated proxy gate survived")
	}
	if script.Metadata.Import.Preserved["proxy_source_evidence"] != "protected original source retained" {
		t.Fatal("source evidence changed")
	}
	if script.Init[0].Metadata["migration_change"] == nil {
		t.Fatal("exclusion decision was not recorded")
	}
}

func TestImportRepairProxyExclusions(t *testing.T) {
	for _, operation := range []string{"omit_proxy_credentials", "disable_imported_proxy"} {
		for _, mode := range []string{"custom", "pac"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				script := proxyRepairFixture(mode)
				// This same name is independently required by a request, and must stay.
				script.Actions[0].Headers = map[string]string{"Authorization": "Bearer {{secret.ENTERPRISE_PROXY_PASSWORD}}"}
				script.Init = append(script.Init, legacyTLSGateForTest("TLS", legacyTLSWarningForTest("SSL3")))
				if _, err := applyImportRepair(script, ImportRepairRequest{Finding: "encrypted_proxy_password", ActionPath: "init[0]", Operation: ImportRepairOperation{Type: operation}}); err != nil {
					t.Fatal(err)
				}
				assertProxyExcluded(t, script, operation, mode)
				if len(executionGatePaths(script, "legacy_tls_replacement")) != 1 || !strings.Contains(script.Actions[0].Headers["Authorization"], "ENTERPRISE_PROXY_PASSWORD") {
					t.Fatal("exclusion removed an unrelated requirement")
				}
				if _, _, _, _, err := evaluateRepairedScript(script, nil); err != nil {
					t.Fatal(err)
				}
				required, _ := replaySecretRefs(script, nil)
				if len(required) != 1 || required[0] != "ENTERPRISE_PROXY_PASSWORD" {
					t.Fatalf("remaining secret references = %v", required)
				}
			})
		}
	}
}

func TestImportRepairProxyCredentialsInURL(t *testing.T) {
	script := proxyRepairFixture("custom")
	script.Config.HTTP.Proxy.URL = "http://user:password@proxy.example.test:8080"
	if _, err := applyImportRepair(script, ImportRepairRequest{Finding: "encrypted_proxy_password", ActionPath: "init[0]", Operation: ImportRepairOperation{Type: "omit_proxy_credentials"}}); err != nil {
		t.Fatal(err)
	}
	if script.Config.HTTP.Proxy.URL != "http://proxy.example.test:8080" {
		t.Fatal("embedded proxy authentication survived")
	}
}

func TestImportRepairProxyMalformedURLCanStillBeDisabled(t *testing.T) {
	script := proxyRepairFixture("custom")
	script.Config.HTTP.Proxy.URL = "http://%invalid"
	req := ImportRepairRequest{Finding: "encrypted_proxy_password", ActionPath: "init[0]", Operation: ImportRepairOperation{Type: "omit_proxy_credentials"}}
	if _, err := applyImportRepair(script, req); err == nil {
		t.Fatal("malformed proxy URL was accepted")
	}
	req.Operation.Type = "disable_imported_proxy"
	if _, err := applyImportRepair(script, req); err != nil {
		t.Fatal(err)
	}
	assertProxyExcluded(t, script, req.Operation.Type, "custom")
}

func TestImportRepairProxyExclusionsRequireMatchingFinding(t *testing.T) {
	for _, operation := range []string{"omit_proxy_credentials", "disable_imported_proxy"} {
		script := proxyRepairFixture("custom")
		script.Init = []dsl.Action{legacyTLSGateForTest("TLS", legacyTLSWarningForTest("SSL3"))}
		if _, err := applyImportRepair(script, ImportRepairRequest{Finding: "legacy_tls_replacement", ActionPath: "init[0]", Operation: ImportRepairOperation{Type: operation}}); err == nil {
			t.Fatal("proxy operation resolved an unrelated gate")
		}
		if script.Config.HTTP.Proxy.Password == "" {
			t.Fatal("rejected operation changed proxy configuration")
		}
	}
}

func TestProxyExclusionPreviewApplyAndDurableReopen(t *testing.T) {
	for _, operation := range []string{"omit_proxy_credentials", "disable_imported_proxy"} {
		t.Run(operation, func(t *testing.T) {
			wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5a}, 32), KeyID: "proxy-choice-test"})
			if err != nil {
				t.Fatal(err)
			}
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.Close() })
			server := NewServer(ServerConfig{Store: store, BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper})
			script := proxyRepairFixture("pac")
			id, _, err := server.registerImportCandidateSession(httptest.NewRequest(http.MethodPost, "/", nil), enterpriseArchiveSaveProfile, script)
			if err != nil {
				t.Fatal(err)
			}
			token := importDraftProtectedTokenPrefix + id
			req := ImportRepairRequest{CandidateID: token, Revision: token, ProjectID: storage.DefaultProjectID, Finding: "encrypted_proxy_password", ActionPath: "init[0]", Operation: ImportRepairOperation{Type: operation}}
			call := func(apply bool) ImportRepairResponse {
				t.Helper()
				raw, _ := json.Marshal(req)
				w := httptest.NewRecorder()
				server.handleImportRepair(w, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw)), apply)
				if w.Code != http.StatusOK {
					t.Fatalf("repair %d: %s", w.Code, w.Body.String())
				}
				var result ImportRepairResponse
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				return result
			}
			preview := call(false)
			if preview.CandidateID != token || preview.Before.Config.HTTP.Proxy.Password == "" {
				t.Fatal("preview mutated the original candidate")
			}
			assertProxyExcluded(t, preview.Script, operation, "pac")
			applied := call(true)
			if applied.CandidateID == token || len(applied.SecretBindingRequirements) != 0 || applied.Readiness.UnresolvedBindings != 0 {
				t.Fatalf("repair did not replace the candidate or remove bindings: %#v", applied.Readiness)
			}
			stored := storeRepairImport(t, server, applied.Script)
			reopened := durableRepairSnapshot(t, server, stored.ID)
			assertProxyExcluded(t, reopened.Script, operation, "pac")
			if required, _ := replaySecretRefs(reopened.Script, nil); len(required) != 0 {
				t.Fatalf("reopened script still requires proxy secrets: %v", required)
			}
		})
	}
}

func TestStoredScriptProxyExclusions(t *testing.T) {
	for _, operation := range []string{"omit_proxy_credentials", "disable_imported_proxy"} {
		t.Run(operation, func(t *testing.T) {
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.Close() })
			server := NewServer(ServerConfig{Store: store})
			script := proxyRepairFixture("custom")
			script.Actions[0].Headers = map[string]string{"Authorization": "Bearer " + storedRepairCredential}
			stored := storeRepairImport(t, server, script)
			_, tag := requireAuthoringAPIProjection(t, server, stored.ID)
			req := ScriptRepairRequest{Finding: "encrypted_proxy_password", ActionPath: "init[0]", Operation: ImportRepairOperation{Type: operation}}
			preview := decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "preview", req, tag))
			assertProxyExcluded(t, preview.Script, operation, "custom")
			if durableRepairSnapshot(t, server, stored.ID).Script.Config.HTTP.Proxy.Password == "" {
				t.Fatal("preview persisted")
			}
			decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "apply", req, tag))
			reopened := durableRepairSnapshot(t, server, stored.ID)
			assertProxyExcluded(t, reopened.Script, operation, "custom")
			if reopened.Script.Actions[0].Headers["Authorization"] != "Bearer "+storedRepairCredential {
				t.Fatal("unrelated imported credential changed")
			}
		})
	}
}

func TestProxyExclusionRetainsSourceProtection(t *testing.T) {
	for _, username := range []string{"proxy-source-user-canary", "{{opaque-captured-user}}"} {
		for _, operation := range []string{"omit_proxy_credentials", "disable_imported_proxy"} {
			t.Run(operation+"/"+username, func(t *testing.T) {
				script := proxyRepairFixture("custom")
				script.Config.HTTP.Proxy.Username = username
				script.Metadata.Import.Preserved["source_review"] = map[string]any{"source_files": []map[string]string{{"path": "default.cfg", "content": "ProxyUserName=" + username}}}
				if _, err := applyImportRepair(script, ImportRepairRequest{Finding: "encrypted_proxy_password", ActionPath: "init[0]", Operation: ImportRepairOperation{Type: operation}}); err != nil {
					t.Fatal(err)
				}
				public, err := json.Marshal(repairDisclosure(script))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(public), username) {
					t.Fatal("excluding the executable proxy disclosed its source username")
				}
				private, _ := json.Marshal(script.Metadata.Import.Preserved["source_review"])
				if !strings.Contains(string(private), username) {
					t.Fatal("exclusion changed the protected original source")
				}
			})
		}
	}
}

func TestRetainIgnoredProxyDisclosureSourceRejectsMalformedEvidence(t *testing.T) {
	script := proxyRepairFixture("custom")
	script.Metadata.Import.Preserved[dsl.ImportPreservedIgnoredProxyCredentialsKey] = map[string]any{"version": 1, "values_base64": []string{"invalid!"}}
	if err := retainIgnoredProxyDisclosureSource(script); err == nil {
		t.Fatal("malformed existing evidence was accepted")
	}
}

func TestImportRepairProxyExclusionRecordsSelectedNestedRequestDecision(t *testing.T) {
	for _, operation := range []string{"omit_proxy_credentials", "disable_imported_proxy"} {
		t.Run(operation, func(t *testing.T) {
			script := proxyRepairFixture("custom")
			metadata := script.Init[0].Metadata
			script.Init = nil
			request := map[string]any{"name": "Nested proxy setting", "method": "GET", "target": "https://example.test/home", "metadata": metadata}
			script.Actions = []dsl.Action{{Name: "Sequence", Type: "http_sequence", Params: map[string]any{"requests": []any{request}}}}
			req := ImportRepairRequest{Finding: "encrypted_proxy_password", ActionPath: "actions[0].params.requests[0]", Operation: ImportRepairOperation{Type: operation}}
			if _, err := applyImportRepair(script, req); err != nil {
				t.Fatal(err)
			}
			updated, ok := request["metadata"].(map[string]any)
			if !ok || !strings.Contains(stringValue(updated["migration_change"]), "Ignored during import") {
				t.Fatal("selected nested request lost the proxy exclusion decision")
			}
			if len(executionGatePaths(script, "encrypted_proxy_password")) != 0 || script.Config.HTTP.Proxy.Password != "" || script.Config.HTTP.Proxy.Username != "" {
				t.Fatal("nested proxy exclusion left credentials or gates active")
			}
			if operation == "disable_imported_proxy" && (script.Config.HTTP.ProxyMode != "none" || !reflect.DeepEqual(script.Config.HTTP.Proxy, dsl.ProxyConfig{})) {
				t.Fatal("nested exclusion did not disable proxy routing")
			}
			if operation == "omit_proxy_credentials" && (script.Config.HTTP.ProxyMode != "custom" || script.Config.HTTP.Proxy.URL != "http://proxy.example.test:8080") {
				t.Fatal("nested credential exclusion changed proxy routing")
			}
		})
	}
}
