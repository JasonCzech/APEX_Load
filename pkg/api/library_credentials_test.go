package api

import "testing"

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
