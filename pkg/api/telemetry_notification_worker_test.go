package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestTelemetryNotificationFingerprintPinsConfigurationAndCanonicalFilters(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	route := NotificationWebhookSettings{Enabled: true, URL: "https://example.invalid/hook", Format: "grafana", APIToken: "fixture-token", Events: []string{"telemetry_acceptance", "test_failed"}}
	original, err := telemetryDestinationFingerprint(key, route)
	if err != nil || len(original) != 64 {
		t.Fatal(original, err)
	}
	reordered := route
	reordered.Events = []string{"test_failed", "telemetry_acceptance", "test_failed"}
	same, err := telemetryDestinationFingerprint(key, reordered)
	if err != nil || same != original {
		t.Fatal("filter order changed destination identity", err)
	}
	for _, name := range []string{"token", "URL", "enabled", "format", "name"} {
		t.Run(name, func(t *testing.T) {
			changed := route
			switch name {
			case "token":
				changed.APIToken = "rotated"
			case "URL":
				changed.URL += "/changed"
			case "enabled":
				changed.Enabled = false
			case "format":
				changed.Format = "dynatrace"
			case "name":
				changed.Name = "renamed"
			}
			next, err := telemetryDestinationFingerprint(key, changed)
			if err != nil || next == original {
				t.Fatal("changed configuration retained delivery authority", err)
			}
		})
	}
	s, _, _ := notificationRuntimeFixture(t)
	saveNotificationRuntimeRoutes(t, s, route, reordered)
	destinations, err := s.telemetryNotificationDestinations(context.Background(), key)
	if err != nil || len(destinations) != 1 {
		t.Fatal("identical destinations did not collapse", len(destinations), err)
	}
}

type unavailableNotificationSettings struct {
	SQLiteStore *storage.SQLiteStore
	fail        atomic.Bool
}

func (s *unavailableNotificationSettings) GetSettingContext(ctx context.Context, key string) (string, error) {
	if key == systemSettingsStoreKey && s.fail.Load() {
		return "", errors.New("injected settings read failure")
	}
	return s.SQLiteStore.GetSettingContext(ctx, key)
}
func TestTelemetryNotificationSettingsFailureRetriesWithoutRevocation(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	s, store, m := notificationRuntimeFixture(t)
	var requests atomic.Int32
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(202) }))
	defer receiver.Close()
	s.runner.notifier.Close()
	s.runner.notifier = newNotificationDispatcher(receiver.Client())
	saveNotificationRuntimeRoutes(t, s, NotificationWebhookSettings{Enabled: true, URL: receiver.URL, Format: "slack"})
	commitNotificationRuntimeFixture(t, s, store, m)
	fault := &unavailableNotificationSettings{SQLiteStore: store}
	fault.fail.Store(true)
	adapter := sqliteOverlay(fault)
	s.store, s.runner.settingsStore, s.runner.archiveStore = adapter, adapter, adapter
	s.reconcileTelemetryNotifications(context.Background())
	awaitNotificationState(t, store, map[string]int64{"pending": 1, "revoked": 0, "leased": 0})
	due, err := store.ListDueTelemetryNotificationDeliveries(context.Background(), time.Now().Add(2*time.Second), 16)
	if err != nil || len(due) != 1 || due[0].Attempts != 1 || requests.Load() != 0 {
		t.Fatal("failed settings read lost retry state", due, err, requests.Load())
	}
	fault.fail.Store(false)
	claim, err := store.ClaimTelemetryNotificationDelivery(context.Background(), due[0], time.Now().Add(2*time.Second), time.Minute)
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	s.deliverTelemetryNotification(context.Background(), store, *claim)
	awaitNotificationState(t, store, map[string]int64{"delivered": 1})
	if requests.Load() != 1 {
		t.Fatal("recovered settings did not deliver once", requests.Load())
	}
}

func TestTelemetryNotificationWorkerWaitsForLeaderAndOwnsShutdown(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	s, store, m := notificationRuntimeFixture(t)
	var requests atomic.Int32
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(202) }))
	defer receiver.Close()
	s.runner.notifier.Close()
	s.runner.notifier = newNotificationDispatcher(receiver.Client())
	saveNotificationRuntimeRoutes(t, s, NotificationWebhookSettings{Enabled: true, URL: receiver.URL, Format: "slack"})
	commitNotificationRuntimeFixture(t, s, store, m)
	leadership := newExecutionLeadership(store, "notification-worker-fixture")
	s.runner.leadership = leadership
	t.Cleanup(leadership.relinquish)
	s.startTelemetryNotificationWorker()
	time.Sleep(1100 * time.Millisecond)
	due, err := store.ListDueTelemetryNotificationDeliveries(context.Background(), time.Now(), 16)
	if err != nil || len(due) != 1 || due[0].Attempts != 0 || requests.Load() != 0 {
		t.Fatal("follower claimed notification", due, err)
	}
	leadership.tick()
	if !leadership.IsLeader() {
		t.Fatal("fixture failed to acquire real SQL lease")
	}
	awaitNotificationState(t, store, map[string]int64{"delivered": 1})
	if requests.Load() != 1 {
		t.Fatal("leader did not deliver once", requests.Load())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal("worker did not join shutdown", err)
	}
}
