package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type blockingJSONBody struct {
	payload []byte
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	offset  int
}

type blockingLibraryCredentialRead struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	err     error
}

func (s *blockingLibraryCredentialRead) PutRemoteAPIKey(string, string) (string, error) {
	return "", nil
}

func (s *blockingLibraryCredentialRead) GetRemoteAPIKey(LibraryRemote) (string, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return "token", s.err
}

func (s *blockingLibraryCredentialRead) DeleteRemoteAPIKey(string) error { return nil }

type blockingLibraryStateRead struct {
	*metricsStoreSpy
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingLibraryStateRead) GetSetting(key string) (string, error) {
	if key == libraryRemotesSettingKey {
		s.once.Do(func() { close(s.entered) })
		<-s.release
	}
	return s.metricsStoreSpy.GetSetting(key)
}

func (b *blockingJSONBody) Read(p []byte) (int, error) {
	b.once.Do(func() {
		close(b.entered)
		<-b.release
	})
	if b.offset >= len(b.payload) {
		return 0, io.EOF
	}
	n := copy(p, b.payload[b.offset:])
	b.offset += n
	return n, nil
}

func (b *blockingJSONBody) Close() error { return nil }

func TestRemoteMutationBodiesAreDecodedBeforeConfigGate(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "run create", method: http.MethodPost, path: "/api/v1/remotes/missing/runs"},
		{name: "remote update", method: http.MethodPut, path: "/api/v1/library/remotes/missing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			entered := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseBody := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(releaseBody)
			body := &blockingJSONBody{payload: []byte(`{}`), entered: entered, release: release}
			responseDone := make(chan int, 1)
			go func() {
				response := httptest.NewRecorder()
				request := httptest.NewRequest(tc.method, tc.path, body)
				server.mux.ServeHTTP(response, request)
				responseDone <- response.Code
			}()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("handler did not start decoding its request body")
			}
			writerAcquired := make(chan struct{})
			go func() {
				server.remoteConfigMu.Lock()
				close(writerAcquired)
				server.remoteConfigMu.Unlock()
			}()
			select {
			case <-writerAcquired:
			case <-time.After(500 * time.Millisecond):
				t.Fatal("slow request body held the remote config gate")
			}
			releaseBody()
			select {
			case <-responseDone:
			case <-time.After(2 * time.Second):
				t.Fatal("handler did not finish after request body was released")
			}
		})
	}
}

func TestRemoteCredentialResolutionDoesNotHoldConfigLock(t *testing.T) {
	tests := []struct {
		name     string
		resolve  func(*Server) error
		reserved bool
	}{
		{
			name: "ordinary snapshot",
			resolve: func(server *Server) error {
				_, _, err := server.snapshotLibraryRemoteConfig("workspace")
				return err
			},
		},
		{
			name:     "run create reservation",
			reserved: true,
			resolve: func(server *Server) error {
				_, _, release, err := server.reserveRemoteRunCreateConfig("workspace")
				release()
				return err
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			credentials := &blockingLibraryCredentialRead{
				entered: make(chan struct{}),
				release: make(chan struct{}),
			}
			server := NewServer(ServerConfig{})
			server.libraryCreds = credentials
			server.libraryMu.Lock()
			server.libraryRemotes["workspace"] = &LibraryRemote{
				ID:            "workspace",
				Name:          "Workspace",
				BaseURL:       "https://remote.example.test",
				AuthMethod:    "service_account",
				CredentialRef: "keyring:library-remote:workspace",
			}
			server.libraryMu.Unlock()

			resolveDone := make(chan error, 1)
			go func() { resolveDone <- tc.resolve(server) }()
			select {
			case <-credentials.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("credential resolution did not start")
			}

			writerAcquired := make(chan struct{})
			go func() {
				server.remoteConfigMu.Lock()
				close(writerAcquired)
				server.remoteConfigMu.Unlock()
			}()
			select {
			case <-writerAcquired:
			case <-time.After(500 * time.Millisecond):
				close(credentials.release)
				t.Fatal("credential resolution held the remote config lock")
			}
			if tc.reserved {
				server.remoteConfigMu.RLock()
				reserved := server.remoteRunCreateInFlightLocked("workspace")
				server.remoteConfigMu.RUnlock()
				if !reserved {
					close(credentials.release)
					t.Fatal("run create did not fence credential mutation before resolution")
				}
			}

			close(credentials.release)
			select {
			case err := <-resolveDone:
				if err != nil {
					t.Fatalf("resolve remote config: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("credential resolution did not finish")
			}
			if tc.reserved {
				server.remoteConfigMu.RLock()
				reserved := server.remoteRunCreateInFlightLocked("workspace")
				server.remoteConfigMu.RUnlock()
				if reserved {
					t.Fatal("run create credential fence was not released after resolution")
				}
			}
		})
	}
}

func TestRemoteRunCredentialResolutionErrorReleasesCreateFence(t *testing.T) {
	credentials := &blockingLibraryCredentialRead{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		err:     errors.New("credential provider unavailable"),
	}
	close(credentials.release)
	server := NewServer(ServerConfig{})
	server.libraryCreds = credentials
	server.libraryMu.Lock()
	server.libraryRemotes["workspace"] = &LibraryRemote{
		ID:            "workspace",
		Name:          "Workspace",
		BaseURL:       "https://remote.example.test",
		AuthMethod:    "service_account",
		CredentialRef: "keyring:library-remote:workspace",
	}
	server.libraryMu.Unlock()

	_, ok, release, err := server.reserveRemoteRunCreateConfig("workspace")
	release()
	if !ok || err == nil {
		t.Fatalf("credential resolution = ok:%t err:%v, want configured remote error", ok, err)
	}
	server.remoteConfigMu.RLock()
	reserved := server.remoteRunCreateInFlightLocked("workspace")
	server.remoteConfigMu.RUnlock()
	if reserved {
		t.Fatal("failed credential resolution leaked the remote run create fence")
	}
}

func TestRemoteStateRefreshDoesNotHoldConfigLock(t *testing.T) {
	store := &blockingLibraryStateRead{
		metricsStoreSpy: &metricsStoreSpy{settings: map[string]string{
			libraryRemotesSettingKey: `[{"id":"workspace","name":"Workspace","base_url":"https://remote.example.test","auth_method":"none"}]`,
		}},
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	server := NewServer(ServerConfig{})
	server.store = store

	resolveDone := make(chan error, 1)
	go func() {
		_, _, err := server.snapshotLibraryRemoteConfig("workspace")
		resolveDone <- err
	}()
	select {
	case <-store.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("durable remote state refresh did not start")
	}

	writerAcquired := make(chan struct{})
	go func() {
		server.remoteConfigMu.Lock()
		close(writerAcquired)
		server.remoteConfigMu.Unlock()
	}()
	select {
	case <-writerAcquired:
	case <-time.After(500 * time.Millisecond):
		close(store.release)
		t.Fatal("durable remote state refresh held the remote config lock")
	}

	close(store.release)
	select {
	case err := <-resolveDone:
		if err != nil {
			t.Fatalf("resolve refreshed remote config: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("durable remote state refresh did not finish")
	}
}

func TestLibraryCatalogNetworkWaitDoesNotHoldRemoteConfigLock(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	var enterOnce sync.Once
	var releaseOnce sync.Once
	releaseRemote := func() { releaseOnce.Do(func() { close(releaseRequest) }) }
	t.Cleanup(releaseRemote)
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enterOnce.Do(func() { close(requestEntered) })
		<-releaseRequest
		writeJSON(w, http.StatusOK, LibraryCatalogResponse{})
	}))
	t.Cleanup(remoteHTTP.Close)
	server := NewServer(ServerConfig{})
	remote := &LibraryRemote{
		ID: "workspace", Name: "Workspace", BaseURL: remoteHTTP.URL, AuthMethod: "none",
		ProjectMappings: map[string]string{"local-project": "remote-project"},
	}
	server.libraryMu.Lock()
	server.libraryRemotes[remote.ID] = remote
	server.libraryMu.Unlock()

	responseDone := make(chan int, 1)
	go func() {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/library/remotes/workspace/catalog?project_id=local-project", nil)
		server.mux.ServeHTTP(response, request)
		responseDone <- response.Code
	}()
	select {
	case <-requestEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("catalog request did not reach the remote controller")
	}
	writerAcquired := make(chan struct{})
	go func() {
		server.remoteConfigMu.Lock()
		close(writerAcquired)
		server.remoteConfigMu.Unlock()
	}()
	select {
	case <-writerAcquired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("catalog network wait held the remote config gate")
	}
	releaseRemote()
	select {
	case status := <-responseDone:
		if status != http.StatusOK {
			t.Fatalf("catalog status = %d", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("catalog request did not finish")
	}
}

func TestLibraryProbeRejectsHealthResultAfterTransportChanges(t *testing.T) {
	t.Setenv(remoteOutboundAllowPrivateHostsEnv, "1")
	probeEntered := make(chan struct{})
	releaseProbe := make(chan struct{})
	var releaseOnce sync.Once
	releaseRemote := func() { releaseOnce.Do(func() { close(releaseProbe) }) }
	t.Cleanup(releaseRemote)
	oldRemote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(probeEntered)
		<-releaseProbe
		writeJSON(w, http.StatusOK, LibraryCapabilitiesResponse{})
	}))
	t.Cleanup(oldRemote.Close)
	newRemote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, LibraryCapabilitiesResponse{})
	}))
	t.Cleanup(newRemote.Close)
	server := NewServer(ServerConfig{})
	remote := &LibraryRemote{ID: "workspace", Name: "Workspace", BaseURL: oldRemote.URL, AuthMethod: "none", Status: "unknown"}
	server.libraryMu.Lock()
	server.libraryRemotes[remote.ID] = remote
	server.libraryMu.Unlock()

	probeDone := make(chan int, 1)
	go func() {
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/library/remotes/workspace/probe", nil))
		probeDone <- response.Code
	}()
	select {
	case <-probeEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("probe did not reach the old transport")
	}
	updateBody := marshalLibraryTestJSON(t, LibraryRemoteRequest{BaseURL: newRemote.URL})
	update := httptest.NewRecorder()
	server.mux.ServeHTTP(update, httptest.NewRequest(http.MethodPut, "/api/v1/library/remotes/workspace", bytes.NewReader(updateBody)))
	if update.Code != http.StatusOK {
		t.Fatalf("transport update during probe = %d body=%s", update.Code, update.Body.String())
	}
	releaseRemote()
	select {
	case status := <-probeDone:
		if status != http.StatusConflict {
			t.Fatalf("stale probe result status = %d, want %d", status, http.StatusConflict)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("probe did not finish")
	}
	server.libraryMu.Lock()
	stored := *server.libraryRemotes[remote.ID]
	server.libraryMu.Unlock()
	if stored.BaseURL != newRemote.URL || stored.Status != "unknown" {
		t.Fatalf("stale probe overwrote new transport health: %#v", stored)
	}
}
