package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type blockedTelemetryNotificationSettings struct {
	SQLiteStore *storage.SQLiteStore
	block       atomic.Bool
	entered     chan struct{}
	release     chan struct{}
	once        sync.Once
}

func (s *blockedTelemetryNotificationSettings) GetSettingContext(ctx context.Context, key string) (string, error) {
	if key == systemSettingsStoreKey && s.block.Load() {
		s.once.Do(func() { close(s.entered) })
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-s.release:
		}
	}
	return s.SQLiteStore.GetSettingContext(ctx, key)
}
func TestTelemetryNotificationDeletionBeforeSendRefusesOldLease(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	s, store, m := notificationRuntimeFixture(t)
	var requests atomic.Int32
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(202) }))
	defer receiver.Close()
	s.runner.notifier.Close()
	s.runner.notifier = newNotificationDispatcher(receiver.Client())
	saveNotificationRuntimeRoutes(t, s, NotificationWebhookSettings{Enabled: true, URL: receiver.URL, Format: "slack"})
	commitNotificationRuntimeFixture(t, s, store, m)
	due, err := store.ListDueTelemetryNotificationDeliveries(context.Background(), time.Now(), 16)
	if err != nil || len(due) != 1 {
		t.Fatal(due, err)
	}
	claim, err := store.ClaimTelemetryNotificationDelivery(context.Background(), due[0], time.Now(), time.Minute)
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	gate := &blockedTelemetryNotificationSettings{SQLiteStore: store, entered: make(chan struct{}), release: make(chan struct{})}
	gate.block.Store(true)
	var once sync.Once
	release := func() { once.Do(func() { close(gate.release) }) }
	defer release()
	adapter := sqliteOverlay(gate)
	s.store = adapter
	s.runner.settingsStore = adapter
	s.runner.archiveStore = adapter
	done := make(chan struct{})
	go func() { s.deliverTelemetryNotification(context.Background(), adapter, *claim); close(done) }()
	t.Cleanup(func() {
		release()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("delivery fixture did not join")
		}
	})
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery never reached settings boundary")
	}
	if err := store.DeleteTest("notification-run"); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery did not finish")
	}
	if requests.Load() != 0 {
		t.Fatal("deleted run was sent to receiver")
	}
}

func TestTelemetryNotificationDigestKeyCancellationAndConcurrentInitialization(t *testing.T) {
	_, store := resourceAuthTestServer(t, authModeDev)
	s := &Server{store: store}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.persistentDigestKeyContext(ctx, "notification-canceled-key", "fixture"); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled key initialization", err)
	}
	if raw, err := store.GetSetting("notification-canceled-key"); err != nil || raw != "" {
		t.Fatal("canceled key persisted", err)
	}
	keys := make(chan []byte, 12)
	failures := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, err := s.persistentDigestKeyContext(context.Background(), "notification-concurrent-key", "fixture")
			if err != nil {
				failures <- err
			} else {
				keys <- key
			}
		}()
	}
	wg.Wait()
	close(keys)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	var first []byte
	for key := range keys {
		if first == nil {
			first = key
		}
		if len(key) != 32 || !bytes.Equal(key, first) {
			t.Fatal("replicas chose different fingerprints")
		}
	}
	legacy, err := s.persistentDigestKey("notification-concurrent-key", "fixture")
	if err != nil || !bytes.Equal(first, legacy) {
		t.Fatal("existing key format changed", err)
	}
}

type legacyNotificationArchive struct{ archivePersistence }

func TestTelemetryNotificationAtomicCapabilityRequiresTheArchiveStore(t *testing.T) {
	s, store, _ := notificationRuntimeFixture(t)
	if !s.runner.hasAtomicTelemetryNotifications() {
		t.Fatal("default archive store lost atomic capability")
	}
	s.runner.archiveStore = legacyNotificationArchive{store}
	if s.runner.hasAtomicTelemetryNotifications() {
		t.Fatal("general store advertised a legacy archive capability")
	}
	s.runner.archiveStore = store
	s.runner.telemetryNotificationPublication = nil
	if s.runner.hasAtomicTelemetryNotifications() {
		t.Fatal("absent publication callback advertised atomicity")
	}
}

func TestTelemetryNotificationPreparationIsBoundedBeforeDispatch(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	s, store, m := notificationRuntimeFixture(t)
	routes := make([]NotificationWebhookSettings, maxNotificationDeliveries+3)
	for i := range routes {
		routes[i] = NotificationWebhookSettings{Enabled: true, URL: fmt.Sprintf("https://example.invalid/%d", i), Format: "slack"}
	}
	saveNotificationRuntimeRoutes(t, s, routes...)
	commitNotificationRuntimeFixture(t, s, store, m)
	gate := &blockedTelemetryNotificationSettings{SQLiteStore: store, entered: make(chan struct{}), release: make(chan struct{})}
	gate.block.Store(true)
	adapter := sqliteOverlay(gate)
	s.store, s.runner.settingsStore, s.runner.archiveStore = adapter, adapter, adapter
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); close(gate.release) })
	s.reconcileTelemetryNotifications(ctx)
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("preparation did not block")
	}
	for i := 0; i < 4; i++ {
		s.reconcileTelemetryNotifications(ctx)
	}
	state, err := store.GetTelemetryNotificationPublication(context.Background(), storage.DefaultProjectID, "notification-run")
	if err != nil {
		t.Fatal(err)
	}
	if state.Deliveries["leased"] != maxNotificationDeliveries || state.Deliveries["pending"] != 3 {
		t.Fatal("preparation exceeded owned observer bound", state)
	}
	if len(s.runner.notifier.deliverySlots) != 0 {
		t.Fatal("blocked preparation reached HTTP dispatch")
	}
	cancel()
	awaitNotificationState(t, store, map[string]int64{"pending": int64(len(routes)), "leased": 0})
}
