package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func notificationRuntimeFixture(t *testing.T) (*Server, *storage.SQLiteStore, *RunArchiveManifest) {
	t.Helper()
	server, store := baselineTestServer(t)
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	start := stamp.Add(-time.Minute)
	manifest := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "advisory", MinWallTimeCoverage: acceptanceRatio(.9)})
	manifest.TestName = "nightly"
	manifest.CompletedAt = stamp
	manifest.Provenance.ProjectID = storage.DefaultProjectID
	manifest.Provenance.RunIncarnation = "notification-incarnation"
	manifest.TelemetryCoverage = coverageRecord(70000, 100000)
	if err := store.SaveTest(&storage.TestRecord{ID: "notification-run", Name: "nightly", ProjectID: storage.DefaultProjectID, Status: "FAILED", StartTime: start, EndTime: &stamp, InitialProvenance: map[string]any{"run_incarnation": manifest.Provenance.RunIncarnation}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		server.runner.notifier.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return server, store, manifest
}
func saveNotificationRuntimeRoutes(t *testing.T, s *Server, routes ...NotificationWebhookSettings) {
	t.Helper()
	settings := s.runner.GetSystemSettings()
	settings.Notifications = NotificationsSettings{Webhooks: routes}
	if _, err := s.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
}
func commitNotificationRuntimeFixture(t *testing.T, s *Server, store *storage.SQLiteStore, manifest *RunArchiveManifest) string {
	t.Helper()
	publication, err := s.telemetryNotificationPublication(context.Background(), "notification-run", manifest)
	if err != nil || publication.Event == nil {
		t.Fatal("publication", publication, err)
	}
	job := &storage.ArchiveFinalizationJob{TestID: "notification-run", ProjectID: storage.DefaultProjectID, ArchiveRecord: storage.RunArchiveRecord{TestID: "notification-run", ArchivePath: "/archive/notification", ManifestPath: "/archive/notification/manifest.json", ManifestSHA256: strings.Repeat("e", 64), CreatedAt: manifest.CompletedAt, CompletedAt: manifest.CompletedAt}}
	if err := store.SaveArchiveFinalizationJob(job); err != nil {
		t.Fatal(err)
	}
	identity := storage.ArchiveFinalizationRunIdentity{ProjectID: storage.DefaultProjectID, RunIncarnation: manifest.Provenance.RunIncarnation, StartTime: manifest.CompletedAt.Add(-time.Minute), EndTime: manifest.CompletedAt, Status: "FAILED"}
	if ok, err := store.CompleteArchiveFinalizationWithTelemetry(job.TestID, job.IntentToken, "", &job.ArchiveRecord, identity, publication); err != nil || !ok {
		t.Fatal("commit", ok, err)
	}
	id, err := storage.TelemetryNotificationID(*publication.Event)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func awaitNotificationState(t *testing.T, store *storage.SQLiteStore, want map[string]int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, err := store.GetTelemetryNotificationPublication(context.Background(), storage.DefaultProjectID, "notification-run")
		if err == nil {
			match := true
			for key, count := range want {
				match = match && state.Deliveries[key] == count
			}
			if match {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	state, err := store.GetTelemetryNotificationPublication(context.Background(), storage.DefaultProjectID, "notification-run")
	t.Fatal("delivery state did not converge", state, err)
}
func TestTelemetryNotificationRuntimeScrubsAndPinsRecipients(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	s, store, m := notificationRuntimeFixture(t)
	route := NotificationWebhookSettings{Name: "safe route", Enabled: true, URL: "https://example.invalid/hook?token=DESTINATION_SECRET", Format: notificationFormatGrafana, APIToken: "DESTINATION_TOKEN", Events: []string{"telemetry_acceptance"}}
	saveNotificationRuntimeRoutes(t, s, route)
	m.TestName = "name SENTINEL_PRIVATE"
	m.Provenance.RuntimeSettings = &RuntimeSettingsOverride{RedactionRules: []engine.RedactionRule{{Pattern: "SENTINEL_PRIVATE"}}}
	m.TelemetryCoverage.Coverage.Sources[0].Source = "source SENTINEL_PRIVATE password=SOURCE_SECRET"
	id := commitNotificationRuntimeFixture(t, s, store, m)
	event, err := store.GetTelemetryNotification(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(event)
	for _, forbidden := range []string{"SENTINEL_PRIVATE", "SOURCE_SECRET", "DESTINATION_SECRET", "DESTINATION_TOKEN", "example.invalid"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("secret retained", forbidden)
		}
	}
	if !strings.Contains(string(event.Payload), "unknown_ms:30000") || len(event.Destinations) != 1 {
		t.Fatal("safe evidence lost", string(event.Payload))
	}
	route.URL = "https://new.example.invalid/hook"
	saveNotificationRuntimeRoutes(t, s, route)
	repeated, err := s.telemetryNotificationPublication(context.Background(), "notification-run", m)
	if err != nil || repeated.Event == nil || fmt.Sprint(repeated.Event.Destinations) != fmt.Sprint(event.Destinations) {
		t.Fatal("rerouted replay", repeated, err)
	}
	s.reconcileTelemetryNotifications(context.Background())
	awaitNotificationState(t, store, map[string]int64{"revoked": 1})
	// Settings changes do not retroactively add recipients to a new no-route decision.
	saveNotificationRuntimeRoutes(t, s)
	m.Provenance.TelemetryAcceptancePolicy.Pin.Revision++
	m.Provenance.PolicyPins[0].Revision++
	empty := commitNotificationRuntimeFixture(t, s, store, m)
	saveNotificationRuntimeRoutes(t, s, route)
	repeated, err = s.telemetryNotificationPublication(context.Background(), "notification-run", m)
	if err != nil || repeated.Event == nil {
		t.Fatal("repeat projection", repeated, err)
	}
	repeatID, _ := storage.TelemetryNotificationID(*repeated.Event)
	if err != nil || repeatID != empty || len(repeated.Event.Destinations) != 0 {
		t.Fatal("no-recipient decision changed", repeated, err)
	}
}

func TestTelemetryNotificationRuntimeRetriesOnlyFailedDestinations(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	s, store, m := notificationRuntimeFixture(t)
	var fail atomic.Bool
	fail.Store(true)
	var mu sync.Mutex
	keys := map[string][]string{}
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys[r.URL.Path] = append(keys[r.URL.Path], r.Header.Get("X-APEXLoad-Notification-Key"))
		mu.Unlock()
		if r.URL.Path == "/retry" && fail.Load() {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(202)
		}
	}))
	defer receiver.Close()
	s.runner.notifier.Close()
	s.runner.notifier = newNotificationDispatcher(receiver.Client())
	saveNotificationRuntimeRoutes(t, s, NotificationWebhookSettings{Enabled: true, URL: receiver.URL + "/ok", Format: "slack"}, NotificationWebhookSettings{Enabled: true, URL: receiver.URL + "/retry", Format: "slack"})
	commitNotificationRuntimeFixture(t, s, store, m)
	s.reconcileTelemetryNotifications(context.Background())
	awaitNotificationState(t, store, map[string]int64{"delivered": 1, "pending": 1})
	fail.Store(false)
	due, err := store.ListDueTelemetryNotificationDeliveries(context.Background(), time.Now().Add(2*time.Second), 16)
	if err != nil || len(due) != 1 {
		t.Fatal("partial delivery lost", due, err)
	}
	claim, err := store.ClaimTelemetryNotificationDelivery(context.Background(), due[0], time.Now().Add(2*time.Second), time.Minute)
	if err != nil || claim == nil {
		t.Fatal("retry claim", claim, err)
	}
	s.deliverTelemetryNotification(context.Background(), store, *claim)
	awaitNotificationState(t, store, map[string]int64{"delivered": 2})
	mu.Lock()
	defer mu.Unlock()
	if len(keys["/ok"]) != 1 || len(keys["/retry"]) != 4 {
		t.Fatal("wrong HTTP population", keys)
	}
	for _, key := range keys["/retry"] {
		if key == "" || key != keys["/retry"][0] {
			t.Fatal("retry changed receiver identity")
		}
	}
	if keys["/ok"][0] == keys["/retry"][0] {
		t.Fatal("destinations shared identity")
	}
}

func TestTelemetryNotificationRuntimeSaturationAndShutdownRemainRetryable(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	s, store, m := notificationRuntimeFixture(t)
	entered := make(chan struct{}, 1)
	released := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(released) }) }
	defer release()
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-released:
		}
	}))
	defer func() { release(); receiver.Close() }()
	s.runner.notifier.Close()
	s.runner.notifier = newNotificationDispatcher(receiver.Client())
	saveNotificationRuntimeRoutes(t, s, NotificationWebhookSettings{Enabled: true, URL: receiver.URL, Format: "slack"})
	commitNotificationRuntimeFixture(t, s, store, m)
	for i := 0; i < maxNotificationDeliveries; i++ {
		s.runner.notifier.deliverySlots <- struct{}{}
	}
	s.reconcileTelemetryNotifications(context.Background())
	awaitNotificationState(t, store, map[string]int64{"pending": 1})
	for i := 0; i < maxNotificationDeliveries; i++ {
		<-s.runner.notifier.deliverySlots
	}
	due, err := store.ListDueTelemetryNotificationDeliveries(context.Background(), time.Now().Add(2*time.Second), 16)
	if err != nil || len(due) != 1 {
		t.Fatal(due, err)
	}
	claim, err := store.ClaimTelemetryNotificationDelivery(context.Background(), due[0], time.Now().Add(2*time.Second), time.Minute)
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !s.launchBackground(func() { s.deliverTelemetryNotification(ctx, store, *claim) }) {
		t.Fatal("delivery not owned")
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery did not start")
	}
	cancel()
	s.runner.notifier.Close()
	release()
	wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := s.Shutdown(wait); err != nil {
		t.Fatal("shutdown did not join observer", err)
	}
	awaitNotificationState(t, store, map[string]int64{"pending": 1})
}
