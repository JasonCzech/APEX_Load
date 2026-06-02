package api

import "testing"

func TestEnvLibraryCredentialStoreResolvesServerSideRefs(t *testing.T) {
	t.Setenv("APEXLOAD_REMOTE_TEST_KEY", "secret-from-env")
	store := NewEnvLibraryCredentialStore()

	key, err := store.GetRemoteAPIKey(LibraryRemote{
		ID:            "remote-1",
		CredentialRef: "env:APEXLOAD_REMOTE_TEST_KEY",
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
