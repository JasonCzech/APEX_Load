package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestPhaseEventLogDeduplicatesAndBounds(t *testing.T) {
	at := time.Date(2026, 10, 5, 12, 0, 0, 100_000_000, time.UTC)
	level := func(n int) *int { return &n }
	var log phaseEventLog
	for _, tc := range []struct {
		event PhaseEvent
		want  bool
	}{
		{PhaseEvent{Kind: PhaseRampStart, Target: "buy", Level: level(10), At: at}, true},
		{PhaseEvent{Kind: PhaseRampStart, Target: "buy", Level: level(10), At: at.Add(800 * time.Millisecond)}, false}, // same second
		{PhaseEvent{Kind: PhaseRampStart, Target: "buy", Level: level(20), At: at}, true},                               // another level
		{PhaseEvent{Kind: PhaseRampStart, Target: "browse", Level: level(10), At: at}, true},                            // another group
		{PhaseEvent{Kind: PhaseStop, At: at.Add(time.Minute)}, true},
		{PhaseEvent{Kind: PhaseStop, At: at.Add(2 * time.Minute)}, false}, // one stop per run
	} {
		if _, got := log.record(tc.event); got != tc.want {
			t.Fatalf("%+v recorded=%v, want %v", tc.event, got, tc.want)
		}
	}
	index, _ := log.record(PhaseEvent{Kind: PhasePause, At: at.Add(time.Second)})
	log.deliver(index, PhaseEventDelivery{Sink: "grafana", Format: notificationFormatGrafana, Status: phaseDeliveryPending})
	log.deliver(index, PhaseEventDelivery{Sink: "grafana", Format: notificationFormatGrafana, Status: phaseDeliveryDelivered})
	events, _ := log.snapshot()
	if deliveries := events[index].Deliveries; len(deliveries) != 1 || deliveries[0].Status != phaseDeliveryDelivered {
		t.Fatalf("deliveries %+v, want the pending outcome replaced", deliveries)
	}
	for i := 0; len(events) < maxPhaseEvents; i++ {
		log.record(PhaseEvent{Kind: PhaseLevelChange, Target: "fill", Level: level(i), At: at})
		events, _ = log.snapshot()
	}
	if _, ok := log.record(PhaseEvent{Kind: PhaseResume, At: at.Add(time.Hour)}); ok {
		t.Fatal("recorded past the run's bound")
	}
	if _, truncated := log.snapshot(); !truncated {
		t.Fatal("bound reached without truncation")
	}
}

// phaseSinkFixture serves fixture Dynatrace and Grafana endpoints and one
// sink that always fails.
type phaseSinkFixture struct {
	mu       sync.Mutex
	received map[string][]map[string]any
	auth     map[string]string
}

func (f *phaseSinkFixture) handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/broken" {
		// Non-retryable, so the failing sink holds no dispatcher slot for
		// long; delivery slots are striped by URL hash and shared by chance.
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var payload map[string]any
	_ = json.NewDecoder(r.Body).Decode(&payload)
	f.mu.Lock()
	f.received[r.URL.Path] = append(f.received[r.URL.Path], payload)
	f.auth[r.URL.Path] = r.Header.Get("Authorization")
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (f *phaseSinkFixture) payloads(path string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.received[path]...)
}

func TestThreeStepRampSendsEachLevelEventOnce(t *testing.T) {
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	t.Setenv(notificationsAllowInsecureHTTPEnv, "true")
	fixture := &phaseSinkFixture{received: map[string][]map[string]any{}, auth: map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	defer server.Close()

	runner := NewTestRunner(1, NewHub(), nil)
	runner.notifier = newNotificationDispatcher(server.Client())
	settings := runner.GetSystemSettings()
	phaseOnly := []string{notificationEventLoadPhase}
	settings.Notifications = NotificationsSettings{Webhooks: []NotificationWebhookSettings{
		{Name: "dynatrace", Enabled: true, URL: server.URL + "/dynatrace", Format: notificationFormatDynatrace, APIToken: "dt0c01.fixture", EntitySelector: "type(SERVICE),tag(checkout)", Events: phaseOnly},
		{Name: "grafana", Enabled: true, URL: server.URL + "/grafana", Format: notificationFormatGrafana, APIToken: "glsa_fixture", Events: phaseOnly},
		{Name: "broken", Enabled: true, URL: server.URL + "/broken", Format: notificationFormatGrafana, APIToken: "glsa_fixture", Events: phaseOnly},
	}}
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	backend := &scalableExecutionBackendSpy{calls: make(chan scaleBackendCall, 16)}
	runner.setExecutionBackend(backend)
	runner.tests["phase-run"] = &ManagedTest{
		ID: "phase-run", Name: "Three step ramp", Status: "RUNNING", ScenarioMode: "vuser_driven", ProjectID: storage.DefaultProjectID, TotalVUsers: 6,
		Groups: []TestGroup{{Name: "buyers", VUsers: 6, stageDriven: true}},
	}
	stages := []storage.LoadStage{
		{Type: "ramp_up", Duration: "20ms", TargetVUsers: 2}, {Type: "hold", Duration: "10ms", TargetVUsers: 2},
		{Type: "ramp_up", Duration: "20ms", TargetVUsers: 4}, {Type: "hold", Duration: "10ms", TargetVUsers: 4},
		{Type: "ramp_up", Duration: "20ms", TargetVUsers: 6}, {Type: "hold", Duration: "10ms", TargetVUsers: 6},
	}
	runner.runStageSchedule(context.Background(), "phase-run", "buyers", stages, 0, nil)
	if got, _ := runner.GetTest("phase-run"); got.Status != "RUNNING" {
		t.Fatalf("a failing sink affected the run: status %q", got.Status)
	}

	want := []string{"ramp_start/2", "level_reached/2", "ramp_start/4", "level_reached/4", "ramp_start/6", "level_reached/6"}
	deadline := time.Now().Add(5 * time.Second)
	for len(fixture.payloads("/dynatrace")) < len(want) || len(fixture.payloads("/grafana")) < len(want) {
		if time.Now().After(deadline) {
			t.Fatalf("deliveries: dynatrace=%d grafana=%d, want %d each", len(fixture.payloads("/dynatrace")), len(fixture.payloads("/grafana")), len(want))
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // no late duplicates
	seen := map[string]int{}
	for _, payload := range fixture.payloads("/dynatrace") {
		properties, _ := payload["properties"].(map[string]any)
		conditions, _ := properties["apexload.conditions"].(string)
		if payload["eventType"] != "CUSTOM_ANNOTATION" || payload["entitySelector"] != "type(SERVICE),tag(checkout)" || properties["apexload.event_type"] != notificationEventLoadPhase {
			t.Fatalf("dynatrace payload %+v", payload)
		}
		seen["dynatrace:"+phaseFromConditions(conditions)]++
	}
	for _, payload := range fixture.payloads("/grafana") {
		tags := map[string]bool{}
		for _, tag := range payload["tags"].([]any) {
			tags[tag.(string)] = true
		}
		if !tags["apexload"] || !tags["run:phase-run"] || payload["time"] == nil {
			t.Fatalf("grafana payload %+v", payload)
		}
		text, _ := payload["text"].(string)
		for _, w := range want {
			kind, level, _ := strings.Cut(w, "/")
			if tags["phase:"+kind] && strings.Contains(text, " "+level+" VUsers") {
				seen["grafana:"+w]++
			}
		}
	}
	for _, sink := range []string{"dynatrace", "grafana"} {
		for _, w := range want {
			if seen[sink+":"+w] != 1 {
				t.Fatalf("%s received %s %d times (all: %v)", sink, w, seen[sink+":"+w], seen)
			}
		}
	}
	if fixture.auth["/dynatrace"] != "Api-Token dt0c01.fixture" || fixture.auth["/grafana"] != "Bearer glsa_fixture" {
		t.Fatalf("authorization headers %+v", fixture.auth)
	}

	// Every outcome is recorded, including the failing sink's.
	stored, _ := runner.phaseLogs.Load("phase-run")
	log := stored.(*phaseEventLog)
	for deadline := time.Now().Add(10 * time.Second); ; {
		events, _ := log.snapshot()
		settled := len(events) == len(want)
		for _, event := range events {
			statuses := map[string]string{}
			for _, delivery := range event.Deliveries {
				statuses[delivery.Sink] = delivery.Status
			}
			broken := statuses["broken"] == phaseDeliveryFailed || statuses["broken"] == phaseDeliveryDropped
			settled = settled && statuses["dynatrace"] == phaseDeliveryDelivered && statuses["grafana"] == phaseDeliveryDelivered && broken
		}
		if settled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("phase outcomes did not settle: %+v", events)
		}
		time.Sleep(25 * time.Millisecond)
	}

	// Pause arrives through its lifecycle event and is archived beside the levels.
	runner.broadcastEvent("test_paused", &ManagedTest{ID: "phase-run", Name: "Three step ramp", Status: "PAUSED"})
	ended := time.Now().UTC()
	archive := t.TempDir()
	record := &storage.RunArchiveRecord{TestID: "phase-run", ArchivePath: archive, ManifestPath: filepath.Join(archive, "manifest.json")}
	admitArchiveForTest(t, record)
	manifest := &RunArchiveManifest{}
	if err := runner.persistPhaseEventArchive(context.Background(), &ManagedTest{ID: "phase-run", EndedAt: &ended}, record, manifest); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(archive, manifest.PhaseEventsFile))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var kinds []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event PhaseEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil || event.Schema != phaseEventSchema {
			t.Fatalf("archived %s: %v", scanner.Text(), err)
		}
		kinds = append(kinds, event.Kind)
	}
	if strings.Join(kinds, ",") != "ramp_start,level_reached,ramp_start,level_reached,ramp_start,level_reached,pause,stop" || manifest.PhaseEventCount != 8 {
		t.Fatalf("archived kinds %v (count %d)", kinds, manifest.PhaseEventCount)
	}
}

func phaseFromConditions(conditions string) string {
	var kind, level string
	for _, part := range strings.Split(conditions, ",") {
		if value, ok := strings.CutPrefix(part, "kind="); ok {
			kind = value
		}
		if value, ok := strings.CutPrefix(part, "level="); ok {
			level = value
		}
	}
	return kind + "/" + level
}

func TestLifecycleBroadcastUnderRunnerLockDoesNotDeadlock(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	done := make(chan struct{})
	go func() {
		// The scheduler broadcasts a start failure while holding tr.mu.
		runner.mu.Lock()
		runner.broadcastEvent("test_failed", &ManagedTest{ID: "locked-run", Status: "FAILED"})
		runner.mu.Unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a lifecycle broadcast under tr.mu deadlocked")
	}
	stored, ok := runner.phaseLogs.Load("locked-run")
	if !ok {
		t.Fatal("the failure was not recorded as a stop")
	}
	if events, _ := stored.(*phaseEventLog).snapshot(); len(events) != 1 || events[0].Kind != PhaseStop {
		t.Fatalf("events %+v", events)
	}
}

func TestGrafanaWebhookRequiresTokenAndKeepsItAcrossEdits(t *testing.T) {
	grafana := NotificationWebhookSettings{Name: "grafana", Enabled: true, URL: "https://grafana.example.com/api/annotations", Format: notificationFormatGrafana}
	if err := validateNotificationsSettings(NotificationsSettings{Webhooks: []NotificationWebhookSettings{grafana}}); err == nil || !strings.Contains(err.Error(), "api_token") {
		t.Fatalf("a Grafana webhook without a token was accepted: %v", err)
	}
	current := SystemSettings{Notifications: NotificationsSettings{Webhooks: []NotificationWebhookSettings{{Name: "grafana", Enabled: true, URL: grafana.URL, Format: notificationFormatGrafana, APIToken: "glsa_kept"}}}}
	renamed := grafana
	renamed.Name = "dashboards"
	next := preserveWriteOnlySystemSettingsSecrets(SystemSettings{Notifications: NotificationsSettings{Webhooks: []NotificationWebhookSettings{renamed}}}, current)
	if next.Notifications.Webhooks[0].APIToken != "glsa_kept" {
		t.Fatal("a redacted Grafana token was dropped on edit")
	}
	payload := buildGrafanaNotificationPayload(NotificationEvent{Type: "test_started", TestID: "r1", TestName: "Peak", Timestamp: time.UnixMilli(1_790_000_000_000)})
	if payload["time"] != int64(1_790_000_000_000) || strings.Join(payload["tags"].([]string), ",") != "apexload,run:r1,event:test_started" {
		t.Fatalf("payload %+v", payload)
	}
}
