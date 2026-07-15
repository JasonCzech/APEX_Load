package api

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type blockingSettingsStore struct {
	mu          sync.Mutex
	raw         string
	saveStarted chan struct{}
	releaseSave chan struct{}
	once        sync.Once
}

func (s *blockingSettingsStore) GetSetting(string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.raw, nil
}

func (s *blockingSettingsStore) SaveSetting(_ string, value string) error {
	s.once.Do(func() { close(s.saveStarted) })
	<-s.releaseSave
	s.mu.Lock()
	s.raw = value
	s.mu.Unlock()
	return nil
}

func TestPreserveWriteOnlyWebhookSecretsSurvivesReordering(t *testing.T) {
	current := SystemSettings{Notifications: NotificationsSettings{Webhooks: []NotificationWebhookSettings{
		{Name: "first", URL: "https://first.example/api", Format: notificationFormatDynatrace, APIToken: "token-first"},
		{Name: "second", URL: "https://second.example/api", Format: notificationFormatDynatrace, APIToken: "token-second"},
	}}}
	next := SystemSettings{Notifications: NotificationsSettings{Webhooks: []NotificationWebhookSettings{
		{Name: "second", URL: "https://second.example/api", Format: notificationFormatDynatrace},
		{Name: "first", URL: "https://first.example/api", Format: notificationFormatDynatrace},
	}}}
	got := preserveWriteOnlySystemSettingsSecrets(next, current)
	if got.Notifications.Webhooks[0].APIToken != "token-second" || got.Notifications.Webhooks[1].APIToken != "token-first" {
		t.Fatalf("tokens followed array positions: %#v", got.Notifications.Webhooks)
	}
}

func TestMultipartProjectResolverRejectsOversizedProjectField(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("project_id", strings.Repeat("p", (64<<10)+1)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/import", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if projectID, statusCode, _ := resolveImportProjectIDFromForm(req, nil); projectID != "" || statusCode != 400 {
		t.Fatalf("resolver = %q, %d; want rejection", projectID, statusCode)
	}
}

func TestSystemSettingsCASRejectsStaleReplica(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first := NewTestRunner(2, NewHub(), store)
	initial := first.GetSystemSettings()
	if _, err := first.UpdateSystemSettings(initial); err != nil {
		t.Fatal(err)
	}
	stale := NewTestRunner(2, NewHub(), store)
	firstSettings := first.GetSystemSettings()
	firstSettings.General.MaxConcurrentTests = 3
	if _, err := first.UpdateSystemSettings(firstSettings); err != nil {
		t.Fatal(err)
	}
	staleSettings := stale.settings
	staleSettings.General.MaxConcurrentTests = 4
	if _, err := stale.UpdateSystemSettings(staleSettings); err == nil {
		t.Fatal("stale replica update unexpectedly overwrote newer settings")
	}
}

func TestSystemSettingsRevisionRejectsStaleLocalRequest(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	stale, revision := runner.systemSettingsSnapshot()
	newer := cloneSystemSettings(stale)
	newer.General.MaxConcurrentTests = 3
	if _, err := runner.UpdateSystemSettings(newer); err != nil {
		t.Fatal(err)
	}
	stale.General.MaxConcurrentTests = 4
	if _, err := runner.updateSystemSettings(stale, revision, true); !errors.Is(err, errSystemSettingsConflict) {
		t.Fatalf("stale local update error = %v, want conflict", err)
	}
}

func TestSettingsRefreshDoesNotBlockBehindDurableWrite(t *testing.T) {
	store := &blockingSettingsStore{saveStarted: make(chan struct{}), releaseSave: make(chan struct{})}
	runner := NewTestRunner(2, NewHub(), nil)
	runner.settingsStore = store
	settings := runner.settings
	updated := make(chan error, 1)
	go func() {
		_, err := runner.UpdateSystemSettings(settings)
		updated <- err
	}()
	select {
	case <-store.saveStarted:
	case <-time.After(time.Second):
		t.Fatal("settings write did not start")
	}
	readDone := make(chan struct{})
	go func() {
		_ = runner.GetSystemSettings()
		close(readDone)
	}()
	select {
	case <-readDone:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("settings refresh blocked behind durable write")
	}
	close(store.releaseSave)
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
}

func TestMalformedLibrarySettingsDoNotEraseLoadedState(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	server.libraryMu.Lock()
	server.libraryRemotes["kept"] = &LibraryRemote{ID: "kept", Name: "Kept"}
	server.libraryRemotesRaw = `[{"id":"kept","name":"Kept"}]`
	server.libraryMu.Unlock()
	if err := store.SaveSetting(libraryRemotesSettingKey, "{"); err != nil {
		t.Fatal(err)
	}
	server.loadLibraryState()
	server.libraryMu.Lock()
	defer server.libraryMu.Unlock()
	if server.libraryRemotes["kept"] == nil {
		t.Fatal("malformed durable JSON erased the last valid in-memory state")
	}
}

func TestMultipartProjectResolverDoesNotReadBodyBeforeAuthorization(t *testing.T) {
	body := bytes.NewBufferString("large upload payload")
	req := httptest.NewRequest("POST", "/import?project_id=project-a", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	projectID, statusCode, message := resolveImportProjectIDFromForm(req, nil)
	if projectID != "project-a" || statusCode != 0 || message != "" {
		t.Fatalf("resolver = %q, %d, %q", projectID, statusCode, message)
	}
	remaining := make([]byte, body.Len())
	if _, err := req.Body.Read(remaining); err != nil {
		t.Fatal(err)
	}
	if string(remaining) != "large upload payload" {
		t.Fatalf("body was consumed before authorization: %q", remaining)
	}
}
