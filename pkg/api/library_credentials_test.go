package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
	err := validateLibraryRemoteTransport(LibraryRemote{BaseURL: "http://127.0.0.1:8080", AuthMethod: "service_account"})
	if err == nil {
		t.Fatal("private-host opt-in bypassed authenticated HTTPS enforcement")
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
