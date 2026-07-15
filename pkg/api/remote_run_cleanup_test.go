package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRemoteRunCleanupRetryStopsActiveRequestsOnCancellation(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	t.Setenv("APEXLOAD_LIBRARY_ALLOW_INSECURE_HTTP", "1")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "cleanup-cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, LibraryCredentials: NewMemoryLibraryCredentialStore()})
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	server.stopRemoteRunCleanupRetry(stopCtx)
	stopCancel()

	started := make(chan struct{}, 1)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer remote.Close()
	record := newRemoteRunCleanupRecord(LibraryRemote{ID: "remote", BaseURL: remote.URL, AuthMethod: "none"}, "orphan")
	if err := server.enqueueRemoteRunCleanup(record); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		server.runRemoteRunCleanupRetry(ctx, time.Hour)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup HTTP request did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup retry did not stop its active HTTP request")
	}
	remaining, err := server.loadRemoteRunCleanups()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != record.ID {
		t.Fatalf("canceled cleanup outbox = %#v, want pending record %q", remaining, record.ID)
	}
}
