package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newThresholdAlertWebhookRunner(t *testing.T) (*TestRunner, *atomic.Int64, chan string) {
	t.Helper()
	// httptest binds to loopback; allow private outbound for the SSRF guard.
	t.Setenv("APEXLOAD_ALLOW_PRIVATE_WEBHOOKS", "true")
	t.Setenv(notificationsAllowInsecureHTTPEnv, "true")

	deliveries := &atomic.Int64{}
	texts := make(chan string, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read webhook payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !json.Valid(body) {
			t.Errorf("webhook payload is not valid JSON: %s", body)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		deliveries.Add(1)
		select {
		case texts <- string(body):
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	runner := NewTestRunner(2, NewHub(), nil)
	runner.notifier = newNotificationDispatcher(server.Client())
	runner.setNotificationSettings(NotificationsSettings{
		Webhooks: []NotificationWebhookSettings{{
			Name:    "Perf Alerts",
			Enabled: true,
			URL:     server.URL,
			Format:  notificationFormatSlack,
			Events:  []string{"threshold_alert"},
		}},
	})
	return runner, deliveries, texts
}

func waitForDeliveries(t *testing.T, deliveries *atomic.Int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if deliveries.Load() >= want {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := deliveries.Load(); got != want {
		t.Fatalf("webhook deliveries = %d, want %d", got, want)
	}
}

func TestRouteThresholdAlertNotificationsDispatchesOncePerBreach(t *testing.T) {
	runner, deliveries, _ := newThresholdAlertWebhookRunner(t)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout", Status: "RUNNING", ProjectID: "project-1"}

	breach := []map[string]any{{
		"type":      "threshold_alert",
		"metric":    "p99_ms",
		"value":     1830.0,
		"threshold": 1500.0,
	}}
	start := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)

	// A breach that persists across ticks notifies exactly once.
	runner.routeThresholdAlertNotifications(testRun, breach, start)
	runner.routeThresholdAlertNotifications(testRun, breach, start.Add(1*time.Second))
	runner.routeThresholdAlertNotifications(testRun, breach, start.Add(2*time.Second))
	waitForDeliveries(t, deliveries, 1)

	// Recovery re-arms, but a re-breach inside the cooldown stays silent for now.
	runner.routeThresholdAlertNotifications(testRun, nil, start.Add(3*time.Second))
	runner.routeThresholdAlertNotifications(testRun, breach, start.Add(4*time.Second))
	time.Sleep(50 * time.Millisecond)
	waitForDeliveries(t, deliveries, 1)

	// The cooldown defers rather than swallows: the same still-breaching episode
	// notifies on the first tick after the cooldown expires.
	runner.routeThresholdAlertNotifications(testRun, breach, start.Add(thresholdAlertRenotifyInterval+5*time.Second))
	waitForDeliveries(t, deliveries, 2)
	runner.routeThresholdAlertNotifications(testRun, breach, start.Add(thresholdAlertRenotifyInterval+6*time.Second))
	time.Sleep(50 * time.Millisecond)
	waitForDeliveries(t, deliveries, 2)

	// After another recovery and cooldown, the next episode fires immediately.
	runner.routeThresholdAlertNotifications(testRun, nil, start.Add(thresholdAlertRenotifyInterval+7*time.Second))
	runner.routeThresholdAlertNotifications(testRun, breach, start.Add(2*thresholdAlertRenotifyInterval+10*time.Second))
	waitForDeliveries(t, deliveries, 3)
}

func TestRouteThresholdAlertNotificationsWaitsForEligibleDestination(t *testing.T) {
	runner, deliveries, _ := newThresholdAlertWebhookRunner(t)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout", Status: "RUNNING", ProjectID: "project-1"}
	breach := []map[string]any{{
		"type":      "threshold_alert",
		"metric":    "p99_ms",
		"value":     1830.0,
		"threshold": 1500.0,
	}}
	start := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)

	// Neither an empty configuration nor a nonmatching webhook consumes the
	// breach episode. Enabling the matching destination during the same
	// continuing breach must deliver immediately.
	configured := runner.currentNotificationSettings()
	runner.setNotificationSettings(NotificationsSettings{})
	runner.routeThresholdAlertNotifications(testRun, breach, start)
	configured.Webhooks[0].Events = []string{"test_failed"}
	runner.setNotificationSettings(configured)
	runner.routeThresholdAlertNotifications(testRun, breach, start.Add(time.Second))
	time.Sleep(50 * time.Millisecond)
	waitForDeliveries(t, deliveries, 0)

	configured.Webhooks[0].Events = []string{"threshold_alert"}
	runner.setNotificationSettings(configured)
	runner.routeThresholdAlertNotifications(testRun, breach, start.Add(2*time.Second))
	waitForDeliveries(t, deliveries, 1)
}

func TestThresholdAlertNotificationCarriesRunLifecycleStatus(t *testing.T) {
	runner, deliveries, texts := newThresholdAlertWebhookRunner(t)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout", Status: "RUNNING", ProjectID: "project-1"}

	runner.routeThresholdAlertNotifications(testRun, []map[string]any{{
		"type":      "threshold_alert",
		"metric":    "p99_ms",
		"value":     1830.0,
		"threshold": 1500.0,
	}}, time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC))
	waitForDeliveries(t, deliveries, 1)

	select {
	case text := <-texts:
		if !strings.Contains(text, "(RUNNING)") {
			t.Fatalf("threshold webhook omitted run lifecycle status: %s", text)
		}
		if strings.Contains(text, "(p99_ms)") {
			t.Fatalf("threshold webhook used the metric as its lifecycle status: %s", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected threshold webhook payload")
	}
}

func TestThresholdAlertNotificationDescribesThroughputBelowTarget(t *testing.T) {
	event := thresholdAlertNotificationEvent(
		&ManagedTest{ID: "test-throughput", Name: "Checkout"},
		"RUNNING",
		map[string]any{
			"metric":    "throughput_gap_pct",
			"value":     80.0,
			"threshold": 100.0,
			"gap_pct":   20.0,
		},
		time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC),
	)
	if !strings.Contains(event.Summary, "80.00 TPS is below target 100.00 TPS") ||
		!strings.Contains(event.Summary, "20.00% gap") {
		t.Fatalf("throughput summary = %q, want below-target comparison", event.Summary)
	}
	if strings.Contains(event.Summary, "exceeded") {
		t.Fatalf("throughput summary inverted the breach: %q", event.Summary)
	}
}

func TestPublishFinalLocalMetricsRoutesWebhooksWithoutStops(t *testing.T) {
	runner, deliveries, texts := newThresholdAlertWebhookRunner(t)
	maxErrorPct := 5.0
	runner.settings.Thresholds.MaxErrorRatePct = &maxErrorPct

	testRun := &ManagedTest{ID: "test-1", Name: "Checkout", Status: "RUNNING"}
	testRun.cancel = func() {}
	runner.tests[testRun.ID] = testRun

	published := runner.publishFinalLocalMetrics(testRun, engine.MetricsSnapshot{
		Timestamp:    time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC),
		ActiveVUsers: 10,
		Transactions: 20,
		Errors:       5,
		TPS:          15,
		AvgDuration:  200 * time.Millisecond,
	}, time.Time{}, map[string]int{}, nil)
	if !published {
		t.Fatal("final drain snapshot was not published")
	}

	// The final drain routes breach webhooks but never issues stops: the engine
	// has already quiesced.
	waitForDeliveries(t, deliveries, 1)
	select {
	case text := <-texts:
		if !strings.Contains(text, "error_pct") {
			t.Fatalf("final drain webhook text = %q, want error_pct summary", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected final drain webhook text")
	}
	if testRun.Status != "RUNNING" {
		t.Fatalf("final drain changed status to %q, want RUNNING untouched", testRun.Status)
	}
}

func TestRouteThresholdAlertNotificationsRateLimitsRotatingTransactions(t *testing.T) {
	runner, deliveries, texts := newThresholdAlertWebhookRunner(t)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout", Status: "RUNNING", ProjectID: "project-1"}

	serverAlert := func(transaction string, value float64) map[string]any {
		return map[string]any{
			"type":        "threshold_alert",
			"metric":      "avg_server_processing_ms",
			"value":       value,
			"threshold":   350.0,
			"transaction": transaction,
		}
	}
	start := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)

	// The cooldown is enforced per METRIC: a rotating hottest transaction must
	// not mint a fresh webhook per rotation (amplification guard). The first
	// transaction notifies; the second is deferred by the shared cooldown.
	runner.routeThresholdAlertNotifications(testRun, []map[string]any{serverAlert("checkout_submit", 420)}, start)
	waitForDeliveries(t, deliveries, 1)
	runner.routeThresholdAlertNotifications(testRun, []map[string]any{serverAlert("cart_update", 390)}, start.Add(1*time.Second))
	runner.routeThresholdAlertNotifications(testRun, []map[string]any{serverAlert("checkout_submit", 410)}, start.Add(2*time.Second))
	time.Sleep(50 * time.Millisecond)
	waitForDeliveries(t, deliveries, 1)
	select {
	case text := <-texts:
		if !strings.Contains(text, "checkout_submit") {
			t.Fatalf("first webhook text = %q, want checkout_submit summary", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected the first webhook text")
	}

	// Once the metric cooldown expires, the still-breaching deferred
	// transaction notifies with its own summary.
	runner.routeThresholdAlertNotifications(testRun, []map[string]any{serverAlert("cart_update", 395)}, start.Add(thresholdAlertRenotifyInterval+3*time.Second))
	waitForDeliveries(t, deliveries, 2)
	select {
	case text := <-texts:
		if !strings.Contains(text, "cart_update") {
			t.Fatalf("deferred webhook text = %q, want cart_update summary", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected the deferred webhook text")
	}

	// A different metric is independent of the server-processing cooldown.
	runner.routeThresholdAlertNotifications(testRun, []map[string]any{{
		"type":      "threshold_alert",
		"metric":    "p99_ms",
		"value":     1830.0,
		"threshold": 1500.0,
	}}, start.Add(thresholdAlertRenotifyInterval+4*time.Second))
	waitForDeliveries(t, deliveries, 3)
}

func TestThresholdAlertSummarySanitizesTransactionNames(t *testing.T) {
	runner, deliveries, texts := newThresholdAlertWebhookRunner(t)
	testRun := &ManagedTest{ID: "test-1", Name: "Checkout", Status: "RUNNING", ProjectID: "project-1"}

	// Script-author-controlled transaction names must not smuggle Slack mrkdwn
	// links or control characters (which would invalidate the idempotency
	// header and kill delivery) into webhook payloads.
	runner.routeThresholdAlertNotifications(testRun, []map[string]any{{
		"type":        "threshold_alert",
		"metric":      "avg_server_processing_ms",
		"value":       420.0,
		"threshold":   350.0,
		"transaction": "checkout\r\n<https://attacker.example|Reset password>",
	}}, time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC))
	waitForDeliveries(t, deliveries, 1)

	select {
	case text := <-texts:
		// The mrkdwn link form <url|text> must be defused, and the raw CR/LF
		// must not survive into the summary (other payload fields legitimately
		// contain JSON-escaped newlines, so assert on the summary content).
		if strings.Contains(text, "<https://attacker.example") {
			t.Fatalf("webhook payload retained a Slack link injection: %q", text)
		}
		if !strings.Contains(text, "for transaction checkouthttps://attacker.example|Reset password") {
			t.Fatalf("webhook summary was not sanitized as expected: %q", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected sanitized webhook delivery")
	}
}

func TestSanitizeThresholdAlertTextDefusesSlackAndTeamsLinks(t *testing.T) {
	raw := "checkout\r\n<https://attacker.example|Slack> [Reset password](https://attacker.example)"
	got := sanitizeThresholdAlertText(raw)
	if strings.ContainsAny(got, "\r\n<>[]()") {
		t.Fatalf("sanitized transaction retains control or link-markup delimiters: %q", got)
	}
	if !strings.Contains(got, "https://attacker.example|Slack") ||
		!strings.Contains(got, "Reset passwordhttps://attacker.example") {
		t.Fatalf("sanitization should retain visible text while defusing hidden links: %q", got)
	}
}

func TestProcessMetricsSnapshotDispatchesThresholdAlertWebhook(t *testing.T) {
	runner, deliveries, texts := newThresholdAlertWebhookRunner(t)
	maxErrorPct := 5.0
	runner.settings.Thresholds.MaxErrorRatePct = &maxErrorPct

	testRun := &ManagedTest{ID: "test-1", Name: "Checkout", Status: "RUNNING"}
	testRun.cancel = func() {}
	runner.tests[testRun.ID] = testRun

	breachingSnapshot := engine.MetricsSnapshot{
		Timestamp:    time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC),
		ActiveVUsers: 10,
		Transactions: 20,
		Errors:       2,
		TPS:          15,
		AvgDuration:  200 * time.Millisecond,
	}
	runner.processMetricsSnapshot(testRun, breachingSnapshot, map[string]int{}, func(string, int) error { return nil }, nil)
	waitForDeliveries(t, deliveries, 1)
	select {
	case text := <-texts:
		if !strings.Contains(text, "error_pct") {
			t.Fatalf("webhook text = %q, want error_pct summary", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected webhook text")
	}

	// The same continuing breach on the next tick must not re-notify.
	runner.processMetricsSnapshot(testRun, breachingSnapshot, map[string]int{}, func(string, int) error { return nil }, nil)
	time.Sleep(50 * time.Millisecond)
	waitForDeliveries(t, deliveries, 1)
}

func sustainedP99Snapshot(p99 time.Duration) engine.MetricsSnapshot {
	return engine.MetricsSnapshot{
		Timestamp:    time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC),
		ActiveVUsers: 10,
		Transactions: 100,
		TPS:          25,
		AvgDuration:  120 * time.Millisecond,
		P99:          p99,
	}
}

func TestProcessMetricsSnapshotStopsAfterSustainedP99Breach(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	maxP99 := 500.0
	samples := 3
	runner.settings.Thresholds.MaxP99LatencyMs = &maxP99
	runner.settings.Thresholds.P99LatencyStopConsecutiveSamples = &samples

	testRun := &ManagedTest{ID: "test-1", Name: "Checkout", Status: "RUNNING"}
	testRun.cancel = func() {}
	runner.tests[testRun.ID] = testRun

	scale := func(string, int) error { return nil }

	// Two breaching samples stay short of the threshold count.
	runner.processMetricsSnapshot(testRun, sustainedP99Snapshot(800*time.Millisecond), map[string]int{}, scale, nil)
	runner.processMetricsSnapshot(testRun, sustainedP99Snapshot(900*time.Millisecond), map[string]int{}, scale, nil)
	if testRun.Status != "RUNNING" {
		t.Fatalf("status after 2 breaching samples = %q, want RUNNING", testRun.Status)
	}

	// A recovering sample resets the streak.
	runner.processMetricsSnapshot(testRun, sustainedP99Snapshot(100*time.Millisecond), map[string]int{}, scale, nil)
	runner.processMetricsSnapshot(testRun, sustainedP99Snapshot(800*time.Millisecond), map[string]int{}, scale, nil)
	runner.processMetricsSnapshot(testRun, sustainedP99Snapshot(800*time.Millisecond), map[string]int{}, scale, nil)
	if testRun.Status != "RUNNING" {
		t.Fatalf("status after reset + 2 breaches = %q, want RUNNING", testRun.Status)
	}

	runner.processMetricsSnapshot(testRun, sustainedP99Snapshot(800*time.Millisecond), map[string]int{}, scale, nil)
	if testRun.Status != "STOPPING" {
		t.Fatalf("status after 3rd consecutive breach = %q, want STOPPING", testRun.Status)
	}
}

func TestSustainedP99StopDisabledByDefault(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	maxP99 := 500.0
	runner.settings.Thresholds.MaxP99LatencyMs = &maxP99

	testRun := &ManagedTest{ID: "test-1", Name: "Checkout", Status: "RUNNING"}
	testRun.cancel = func() {}
	runner.tests[testRun.ID] = testRun

	for i := 0; i < 10; i++ {
		runner.processMetricsSnapshot(testRun, sustainedP99Snapshot(2*time.Second), map[string]int{}, func(string, int) error { return nil }, nil)
	}
	if testRun.Status != "RUNNING" {
		t.Fatalf("status with no stop sample count = %q, want RUNNING (alert-only)", testRun.Status)
	}
}

func TestPausedRunDoesNotClaimOrAccumulateThresholdStops(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	maxP99 := 500.0
	samples := 3
	thresholds := ThresholdSettings{
		MaxP99LatencyMs:                  &maxP99,
		P99LatencyStopConsecutiveSamples: &samples,
	}
	testRun := &ManagedTest{
		ID:                   "paused-threshold",
		Status:               "PAUSED",
		p99BreachConsecutive: 2,
	}
	live := &LiveMetrics{P99Ms: 900, ErrorPct: 50}

	if runner.shouldStopForSustainedP99(testRun, live, thresholds) {
		t.Fatal("paused run requested a sustained-P99 stop")
	}
	if testRun.p99BreachConsecutive != 0 {
		t.Fatalf("paused run retained P99 breach streak %d, want reset", testRun.p99BreachConsecutive)
	}

	maxError := 5.0
	thresholds.MaxErrorRatePct = &maxError
	alerts := buildThresholdAlerts(testRun, live, thresholds)
	applyThresholdAlertStopActions(alerts, false, false)
	for _, alert := range alerts {
		if action, ok := alert["action"]; ok {
			t.Fatalf("non-running threshold alert claims action %v: %#v", action, alert)
		}
	}
}

func TestSystemSettingsValidationP99StopCrossField(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)

	settings := defaultSystemSettings(10)
	samples := 3
	settings.Thresholds.P99LatencyStopConsecutiveSamples = &samples
	if _, err := runner.UpdateSystemSettings(settings); err == nil || !strings.Contains(err.Error(), "requires thresholds.max_p99_latency_ms") {
		t.Fatalf("stop samples without a P99 level accepted, err=%v", err)
	}

	maxP99 := 1500.0
	settings.Thresholds.MaxP99LatencyMs = &maxP99
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("stop samples with a P99 level rejected: %v", err)
	}

	negative := -1
	settings.Thresholds.P99LatencyStopConsecutiveSamples = &negative
	if _, err := runner.UpdateSystemSettings(settings); err == nil || !strings.Contains(err.Error(), "p99_latency_stop_consecutive_samples must be >= 0") {
		t.Fatalf("negative stop samples accepted, err=%v", err)
	}
}

type alwaysFailingThresholdProtocol struct{}

func (*alwaysFailingThresholdProtocol) Name() string { return "threshold_wiring_regression_test" }
func (*alwaysFailingThresholdProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (*alwaysFailingThresholdProtocol) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	// Keep the failure loop calm: one failed transaction every ~50ms per VU is
	// plenty to hold a 100% error rate without spinning the scheduler.
	select {
	case <-ctx.Done():
	case <-time.After(50 * time.Millisecond):
	}
	return &protocol.Result{ActionName: action.Name, Success: false, ErrorMessage: "synthetic breach", Duration: 50 * time.Millisecond}, nil
}
func (*alwaysFailingThresholdProtocol) Teardown(context.Context) error    { return nil }
func (*alwaysFailingThresholdProtocol) HealthCheck(context.Context) error { return nil }

// Regression guard for the live-path wiring: threshold enforcement must run
// from the REAL local run pipeline (startManagedTestRun's broadcastMetrics
// ticker), not only from helpers driven directly by tests. An always-failing
// protocol breaches max_error_rate_pct, which must broadcast a threshold_alert
// and auto-stop the run.
func TestLiveLocalRunEnforcesErrorRateThreshold(t *testing.T) {
	protocol.Register("threshold_wiring_regression_test", func() protocol.Protocol {
		return &alwaysFailingThresholdProtocol{}
	})

	hub := NewHub()
	runner := NewTestRunner(2, hub, nil)
	t.Cleanup(runner.beginShutdown)
	maxErrorPct := 1.0
	runner.settings.Thresholds.MaxErrorRatePct = &maxErrorPct

	script := &dsl.Script{
		Name:     "threshold-breach",
		Protocol: "threshold_wiring_regression_test",
		Actions:  []dsl.Action{{Name: "request", Type: "request", Target: "/"}},
	}
	testRun := &ManagedTest{
		ID:          "live-threshold-wiring",
		Name:        "Live threshold wiring",
		ProjectID:   storage.DefaultProjectID,
		Status:      "INITIALIZING",
		Duration:    30 * time.Second,
		DurationStr: (30 * time.Second).String(),
		Groups:      []TestGroup{{Name: "default", ScriptName: script.Name, VUsers: 2}},
		TotalVUsers: 2,
		groupSpecs: []engine.GroupConfig{{
			Name: "default", Script: script, VUsers: 2,
		}},
	}
	runner.tests[testRun.ID] = testRun

	mge, err := runner.buildRunEngine(testRun, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, metricsDone, metricsStopped, started, err := runner.startManagedTestRun(testRun, mge, nil)
	if err != nil || !started {
		t.Fatalf("startManagedTestRun = started %v err %v", started, err)
	}

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_, _ = mge.Run(ctx)
	}()

	// Teardown lives in Cleanup so failure paths cannot leak the broadcast
	// ticker goroutine (broadcastMetrics only exits when metricsDone closes) or
	// leave the engine running for the rest of the package run. The body never
	// closes/drains these channels itself, so pass and fail paths tear down
	// identically. The runDone wait doubles as the "stop actually cancels the
	// engine" assertion.
	t.Cleanup(func() {
		runner.mu.RLock()
		cancel := testRun.cancel
		runner.mu.RUnlock()
		if cancel != nil {
			cancel()
		}
		close(metricsDone)
		select {
		case <-metricsStopped:
		case <-time.After(5 * time.Second):
			t.Error("broadcast ticker did not exit during cleanup")
		}
		select {
		case <-runDone:
		case <-time.After(10 * time.Second):
			t.Error("engine run did not exit during cleanup")
		}
	})

	// The broadcast ticker samples once per metrics interval (1s); the breach
	// must trigger an auto-stop well within the run's 30s duration.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		runner.mu.RLock()
		status := testRun.Status
		runner.mu.RUnlock()
		if status == "STOPPING" || status == "STOPPED" || status == "ABORTED" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	runner.mu.RLock()
	finalStatus := testRun.Status
	runner.mu.RUnlock()
	if finalStatus != "STOPPING" && finalStatus != "STOPPED" && finalStatus != "ABORTED" {
		t.Fatalf("live run status = %q; the error-rate threshold never stopped the run through the real metrics path", finalStatus)
	}

	sawThresholdAlert := false
	for {
		select {
		case msg := <-hub.broadcast:
			var payload struct {
				Type   string `json:"type"`
				Metric string `json:"metric"`
			}
			if json.Unmarshal(msg.payload, &payload) == nil && payload.Type == "threshold_alert" && payload.Metric == "error_pct" {
				sawThresholdAlert = true
			}
			continue
		default:
		}
		break
	}
	if !sawThresholdAlert {
		t.Fatal("no threshold_alert broadcast was observed from the live metrics path")
	}
}
