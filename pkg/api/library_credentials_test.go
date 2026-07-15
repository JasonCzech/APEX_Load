package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type countingLibraryCredentialStore struct {
	gets atomic.Int64
}

func (s *countingLibraryCredentialStore) PutRemoteAPIKey(string, string) (string, error) {
	return "", nil
}

func (s *countingLibraryCredentialStore) GetRemoteAPIKey(LibraryRemote) (string, error) {
	s.gets.Add(1)
	return "must-not-be-read", nil
}

func (s *countingLibraryCredentialStore) DeleteRemoteAPIKey(string) error { return nil }

func TestEnvLibraryCredentialStoreResolvesServerSideRefs(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_REMOTE_TEST_KEY", "secret-from-env")
	store := NewEnvLibraryCredentialStore()

	key, err := store.GetRemoteAPIKey(LibraryRemote{
		ID:            "remote-1",
		CredentialRef: "env:APEXLOAD_LIBRARY_REMOTE_TEST_KEY",
	})
	if err != nil {
		t.Fatalf("GetRemoteAPIKey returned error: %v", err)
	}
	if key != "secret-from-env" {
		t.Fatalf("resolved key = %q, want env value", key)
	}

	if _, err := store.PutRemoteAPIKey("remote-1", "secret"); err == nil {
		t.Fatal("PutRemoteAPIKey should reject secret persistence in env mode")
	}
	if _, err := store.GetRemoteAPIKey(LibraryRemote{ID: "remote-1", CredentialRef: "keyring:library-remote:remote-1"}); err == nil {
		t.Fatal("GetRemoteAPIKey should reject non-env refs in env mode")
	}
}

func TestEnvLibraryCredentialStorePreservesLegacyRemotePrefix(t *testing.T) {
	t.Setenv("APEXLOAD_REMOTE_TOKEN", "legacy-secret")
	store := NewEnvLibraryCredentialStore()
	key, err := store.GetRemoteAPIKey(LibraryRemote{ID: "remote-legacy", CredentialRef: "env:APEXLOAD_REMOTE_TOKEN"})
	if err != nil || key != "legacy-secret" {
		t.Fatalf("legacy credential = %q, %v", key, err)
	}
	if _, err := store.GetRemoteAPIKey(LibraryRemote{ID: "remote-bad", CredentialRef: "env:UNRELATED_TOKEN"}); err == nil {
		t.Fatal("unrelated environment variable prefix was accepted")
	}
}

func TestRemoteCredentialRotationsUseIndependentVersionedReferences(t *testing.T) {
	server := NewServer(ServerConfig{})
	server.libraryCreds = NewMemoryLibraryCredentialStore()
	first := LibraryRemote{ID: "remote-1", AuthMethod: "service_account"}
	if err := server.storeRemoteCredential(&first, "first-secret", ""); err != nil {
		t.Fatal(err)
	}
	second := first
	if err := server.storeRemoteCredential(&second, "second-secret", ""); err != nil {
		t.Fatal(err)
	}
	if first.CredentialRef == second.CredentialRef {
		t.Fatalf("rotation reused credential ref %q", first.CredentialRef)
	}
	if got, err := server.libraryCreds.GetRemoteAPIKey(first); err != nil || got != "first-secret" {
		t.Fatalf("first credential = %q, %v", got, err)
	}
	if got, err := server.libraryCreds.GetRemoteAPIKey(second); err != nil || got != "second-secret" {
		t.Fatalf("second credential = %q, %v", got, err)
	}
	if err := server.deleteRemoteCredential(second); err != nil {
		t.Fatal(err)
	}
	if got, err := server.libraryCreds.GetRemoteAPIKey(first); err != nil || got != "first-secret" {
		t.Fatalf("deleting staged loser changed winner: %q, %v", got, err)
	}
}

func TestPrivateHostOptInDoesNotPermitAuthenticatedHTTP(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	t.Setenv("APEXLOAD_LIBRARY_ALLOW_INSECURE_HTTP", "")
	for _, baseURL := range []string{"http://127.0.0.1:8080", "HTTP://127.0.0.1:8080"} {
		err := validateLibraryRemoteTransport(LibraryRemote{BaseURL: baseURL, AuthMethod: "service_account"})
		if err == nil {
			t.Fatalf("private-host opt-in bypassed authenticated HTTPS enforcement for %q", baseURL)
		}
	}
}

func TestLibraryRemoteUpdateValidatesTransportBeforeStagingCredential(t *testing.T) {
	server := NewServer(ServerConfig{})
	credentials := NewMemoryLibraryCredentialStore()
	server.libraryCreds = credentials
	server.libraryRemotes["remote-1"] = &LibraryRemote{
		ID: "remote-1", Name: "Remote", BaseURL: "https://example.test", AuthMethod: "service_account", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	body := []byte(`{"base_url":"http://example.test","auth_method":"service_account","api_key":"must-not-be-stored"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/library/remotes/remote-1", bytes.NewReader(body))
	req.SetPathValue("id", "remote-1")
	response := httptest.NewRecorder()
	server.handleLibraryUpdateRemote(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("update status = %d body=%s", response.Code, response.Body.String())
	}
	credentials.mu.Lock()
	defer credentials.mu.Unlock()
	if len(credentials.secrets) != 0 {
		t.Fatalf("invalid transport left staged credentials: %#v", credentials.secrets)
	}
}

func TestPersistedLegacyHTTPRemoteFailsBeforeCredentialLookupOrNetwork(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_ALLOW_INSECURE_HTTP", "")
	var requests atomic.Int64
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(remoteHTTP.Close)

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "legacy-library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	legacy := []LibraryRemote{{
		ID: "legacy-http", Name: "Legacy HTTP", BaseURL: remoteHTTP.URL,
		AuthMethod: "service_account", CredentialRef: "keyring:library-remote:legacy-http",
	}}
	payload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSetting(libraryRemotesSettingKey, string(payload)); err != nil {
		t.Fatal(err)
	}
	credentials := &countingLibraryCredentialStore{}
	server := NewServer(ServerConfig{Store: store, LibraryCredentials: credentials})
	remote, ok := server.libraryRemote("legacy-http")
	if !ok {
		t.Fatal("persisted legacy remote was not loaded")
	}
	_, err = server.remoteLibraryCapabilities(context.Background(), remote)
	if err == nil || !strings.Contains(err.Error(), "require https") {
		t.Fatalf("legacy authenticated HTTP request error = %v, want HTTPS policy rejection", err)
	}
	if got := credentials.gets.Load(); got != 0 {
		t.Fatalf("credential resolver calls = %d, want 0", got)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("network requests = %d, want 0", got)
	}
}
