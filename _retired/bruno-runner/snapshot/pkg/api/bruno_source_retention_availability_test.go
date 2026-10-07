package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/keywrap"
)

func brunoSupportMatrixRetention(t *testing.T, server *Server) map[string]any {
	t.Helper()
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/bruno/support-matrix", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("support matrix status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Retention map[string]any `json:"retention"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Retention
}

func clearConfiguredWrappingKeys(t *testing.T) {
	t.Helper()
	t.Setenv(keywrap.MasterKeyEnvVar, "")
	t.Setenv(keywrap.KeyVaultKeyIDEnvVar, "")
	t.Setenv("APEXLOAD_ENV", "local")
	t.Setenv("APEXLOAD_REQUIRE_AUTH", "")
}

// Bruno is the only importer that cannot skip protected retention — it
// publishes the encrypted source bundle during inspection and canonical
// execution replays it. An unconfigured controller therefore provisions its own
// local envelope key rather than dead-ending the import UI, and says so.
func TestBrunoImportProvisionsLocalRetentionKeyByDefault(t *testing.T) {
	clearConfiguredWrappingKeys(t)
	root := t.TempDir()
	server := NewServer(ServerConfig{ImportAssetDir: root})

	retention := brunoSupportMatrixRetention(t, server)
	if available, _ := retention["available"].(bool); !available {
		t.Fatalf("retention = %#v, want import available without configuration", retention)
	}
	if custody, _ := retention["key_custody"].(string); custody != brunoKeyCustodyLocal {
		t.Fatalf("key_custody = %#v, want %q", retention["key_custody"], brunoKeyCustodyLocal)
	}
	advisory, _ := retention["advisory"].(string)
	if !strings.Contains(advisory, keywrap.MasterKeyEnvVar) {
		t.Fatalf("advisory = %q, want the operator-managed custody upgrade", advisory)
	}
	if _, present := retention["unavailable_reason"]; present {
		t.Fatalf("retention = %#v, want no blocker when import can run", retention)
	}

	// The store opens, the key lands beside (not inside) the artifact root so
	// the store's own sweeps cannot collect it, and it is owner-only.
	if _, err := server.brunoArtifactStore(); err != nil {
		t.Fatalf("open artifact store with the local key: %v", err)
	}
	// Custody must survive the store being cached. Reporting the prospective
	// resolution instead of the key actually in use claimed operator-managed
	// custody the moment the store opened.
	if custody, _ := brunoSupportMatrixRetention(t, server)["key_custody"].(string); custody != brunoKeyCustodyLocal {
		t.Fatalf("key_custody after opening the store = %q, want %q", custody, brunoKeyCustodyLocal)
	}
	keyPath := filepath.Join(root, brunoLocalArtifactKeyFile)
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat local key: %v", err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("local key mode = %v, want owner-only", mode)
	}
	if info.Size() < 32 {
		t.Fatalf("local key size = %d, want at least 32 bytes", info.Size())
	}

	// A second controller over the same data directory reuses the key instead
	// of provisioning a new one, which would orphan the retained sources.
	original, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	second := NewServer(ServerConfig{ImportAssetDir: root})
	if _, err := second.brunoArtifactStore(); err != nil {
		t.Fatalf("reopen artifact store: %v", err)
	}
	reused, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != string(reused) {
		t.Fatal("second controller replaced the local key, orphaning retained Bruno sources")
	}
}

// The local key is Bruno-scoped. Every other importer gates its candidate
// sessions, attestations, and source retention on brunoArtifactStorePresent,
// and flipping that on would move them from their client-submitted flow to the
// protected flow — changing capability and restart-recovery semantics nothing
// asked for. Bruno's own maintenance still has to see the store, or sessions
// written under the local key would never be swept and project deletion would
// leave their artifacts behind.
func TestLocalRetentionKeyDoesNotEnableSharedProtectedImports(t *testing.T) {
	clearConfiguredWrappingKeys(t)
	root := t.TempDir()
	server := NewServer(ServerConfig{ImportAssetDir: root})
	if _, err := server.brunoArtifactStore(); err != nil {
		t.Fatalf("open artifact store with the local key: %v", err)
	}

	shared, err := server.brunoArtifactStorePresent()
	if err != nil {
		t.Fatal(err)
	}
	if shared {
		t.Fatal("local Bruno key switched every importer onto the shared protected-import flow")
	}
	brunoOwned, err := server.brunoRetentionStorePresent()
	if err != nil {
		t.Fatal(err)
	}
	if !brunoOwned {
		t.Fatal("Bruno maintenance cannot reach the store it just wrote under the local key")
	}
}

// A production posture must not self-issue a key: that would quietly weaken the
// custody guarantee the deployment was configured for. It refuses with the
// operator action instead of writeError's generic 5xx mask, which is what left
// the import UI enabled and dead-ending at "Inspect source".
func TestBrunoImportRefusesUnconfiguredProductionRetention(t *testing.T) {
	clearConfiguredWrappingKeys(t)
	t.Setenv("APEXLOAD_ENV", "production")
	server := NewServer(ServerConfig{ImportAssetDir: t.TempDir()})

	retention := brunoSupportMatrixRetention(t, server)
	if available, _ := retention["available"].(bool); available {
		t.Fatalf("retention = %#v, want unavailable in production without a key", retention)
	}
	reason, _ := retention["unavailable_reason"].(string)
	if !strings.Contains(reason, keywrap.MasterKeyEnvVar) {
		t.Fatalf("unavailable_reason = %q, want the operator action", reason)
	}

	body, err := json.Marshal(brunoInspectJSONRequest{
		ProjectID: "project-bruno-retention",
		Filename:  "opencollection.yml",
		Content:   "info: {name: Request, type: http}\nhttp: {method: GET, url: https://example.test}\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/inspect?project_id=project-bruno-retention", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("inspect status = %d body=%s", response.Code, response.Body.String())
	}
	var failure struct {
		Error string `json:"error"`
		Code  string `json:"code"`
		Ref   string `json:"ref"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Code != brunoSourceRetentionUnavailableCode {
		t.Fatalf("code = %q, want %q", failure.Code, brunoSourceRetentionUnavailableCode)
	}
	if failure.Ref != "" || !strings.Contains(failure.Error, keywrap.MasterKeyEnvVar) {
		t.Fatalf("inspect failure = %#v, want the unmasked operator action", failure)
	}
}

// A configured envelope key keeps operator-managed custody with no advisory to
// explain away.
func TestBrunoImportReportsOperatorManagedRetention(t *testing.T) {
	server, _, _ := newBrunoImportAPITestServer(t)
	retention := brunoSupportMatrixRetention(t, server)
	if available, _ := retention["available"].(bool); !available {
		t.Fatalf("retention = %#v, want available with a configured wrapper", retention)
	}
	if custody, _ := retention["key_custody"].(string); custody != brunoKeyCustodyOperator {
		t.Fatalf("key_custody = %#v, want %q", retention["key_custody"], brunoKeyCustodyOperator)
	}
	if _, present := retention["advisory"]; present {
		t.Fatalf("retention = %#v, want no local-key advisory", retention)
	}
	if _, present := retention["unavailable_reason"]; present {
		t.Fatalf("retention = %#v, want no unavailable_reason when import can run", retention)
	}
}
