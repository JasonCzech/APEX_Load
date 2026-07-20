package api

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

type blockingPendingPromotionProtocol struct {
	entered     chan<- struct{}
	release     <-chan struct{}
	enteredOnce *sync.Once
}

func (p *blockingPendingPromotionProtocol) Name() string { return "pending-promotion-blocking" }

func (p *blockingPendingPromotionProtocol) Init(context.Context, map[string]any) error { return nil }

func (p *blockingPendingPromotionProtocol) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.enteredOnce.Do(func() { p.entered <- struct{}{} })
	select {
	case <-p.release:
		return &protocol.Result{ActionName: action.Name, Success: true}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *blockingPendingPromotionProtocol) Teardown(context.Context) error    { return nil }
func (p *blockingPendingPromotionProtocol) HealthCheck(context.Context) error { return nil }

func TestPendingPromotionRechecksQuotaAfterShakeout(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var enteredOnce sync.Once
	protocolName := registerRunnerTestProtocol(t, "pending-promotion-race", func() protocol.Protocol {
		return &blockingPendingPromotionProtocol{entered: entered, release: release, enteredOnce: &enteredOnce}
	})
	runner.StoreScript(&dsl.Script{
		ID:       "pending-promotion-race-script",
		Name:     "Pending promotion race",
		Protocol: protocolName,
		Version:  "1",
		Actions:  []dsl.Action{{Name: "shakeout", Type: "request", Target: "/"}},
	})

	pending, err := runner.CreateTest(CreateTestRequest{
		Name:           "Pending with shakeout",
		Duration:       "30s",
		ScriptID:       "pending-promotion-race-script",
		VUsers:         1,
		ScriptShakeout: true,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	pendingSince := time.Now().UTC().Add(-time.Minute)
	runner.mu.Lock()
	runner.tests[pending.ID].Status = "PENDING"
	runner.tests[pending.ID].PendingSince = &pendingSince
	runner.tests[pending.ID].PendingReason = "waiting for capacity"
	runner.mu.Unlock()

	done := make(chan struct{})
	go func() {
		runner.promotePendingTests()
		close(done)
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("pending promotion did not enter shakeout")
	}

	// Consume the only global slot while the promotion path is outside tr.mu.
	// The final admission check must observe this run and keep the candidate queued.
	runner.mu.Lock()
	runner.tests["quota-race-winner"] = &ManagedTest{
		ID:          "quota-race-winner",
		Name:        "Concurrent winner",
		Status:      "RUNNING",
		TotalVUsers: 1,
		Duration:    time.Minute,
		CreatedAt:   time.Now().UTC(),
	}
	runner.mu.Unlock()
	close(release)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pending promotion did not finish after shakeout")
	}

	got, ok := runner.GetTest(pending.ID)
	if !ok {
		t.Fatalf("pending test %s disappeared", pending.ID)
	}
	if got.Status != "PENDING" {
		t.Fatalf("pending status = %q, want PENDING after quota was consumed", got.Status)
	}
	if !strings.Contains(got.PendingReason, "global test slot") {
		t.Fatalf("pending reason = %q, want refreshed global quota reason", got.PendingReason)
	}
}

func TestPendingPromotionFailsAfterScheduledAvailabilityExpires(t *testing.T) {
	runner := NewTestRunner(4, NewHub(), nil)
	started := make(chan string, 1)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: started})

	now := time.Now().UTC()
	scheduledAt := now.Add(-24 * time.Hour)
	pendingSince := now.Add(-time.Minute)
	test := &ManagedTest{
		ID:               "expired-window-pending",
		Name:             "Expired scheduled run",
		Status:           "PENDING",
		ScenarioID:       "expired-window-scenario",
		ProjectID:        "expired-window-project",
		TotalVUsers:      1,
		Duration:         time.Minute,
		DurationStr:      "1m",
		CreatedAt:        scheduledAt,
		ScheduledStartAt: &scheduledAt,
		PendingSince:     &pendingSince,
		PendingReason:    "waiting for capacity",
		Groups:           []TestGroup{{Name: "load", Protocol: "http", VUsers: 1}},
	}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.scenarios[test.ScenarioID] = &StoredScenario{
		ID:        test.ScenarioID,
		ProjectID: test.ProjectID,
		Schedules: []*storage.ScenarioSchedule{{
			ID:         "expired-window-schedule",
			ScenarioID: test.ScenarioID,
			ProjectID:  test.ProjectID,
			Timezone:   "UTC",
			AvailabilityWindows: []storage.ScenarioScheduleWindow{{
				Name:       "scheduled day only",
				DaysOfWeek: []int{int(scheduledAt.Weekday())},
				StartTime:  "00:00",
				EndTime:    "23:59",
				Timezone:   "UTC",
			}},
		}},
	}
	runner.mu.Unlock()

	runner.promotePendingTests()

	got, ok := runner.GetTest(test.ID)
	if !ok {
		t.Fatalf("pending test %s disappeared", test.ID)
	}
	if got.Status != "FAILED" {
		t.Fatalf("status = %q, want FAILED after availability window expired", got.Status)
	}
	if !strings.Contains(got.Error, "launch window is no longer valid") || !strings.Contains(got.Error, "availability") {
		t.Fatalf("error = %q, want expired availability detail", got.Error)
	}
	select {
	case id := <-started:
		t.Fatalf("expired scheduled run %s reached execution backend", id)
	default:
	}
}

func TestPendingPromotionFailsWhenBoundTimeslotIsInvalidated(t *testing.T) {
	runner, store := storeBackedRunner(t)
	if err := store.SaveCapacityReservation(approvedTimeslot(time.Now().UTC())); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	timeslotID := "ts-1"
	test, err := runner.CreateTest(CreateTestRequest{
		Name:       "Timeslot pending",
		Duration:   "10m",
		ScriptID:   "script-1",
		ProjectID:  "proj-a",
		VUsers:     1,
		TimeslotID: &timeslotID,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	pendingSince := time.Now().UTC().Add(-time.Minute)
	runner.mu.Lock()
	managed := runner.tests[test.ID]
	managed.Status = "PENDING"
	managed.PendingSince = &pendingSince
	managed.PendingReason = "waiting for capacity"
	if err := runner.saveManagedTestRecord(managed); err != nil {
		runner.mu.Unlock()
		t.Fatalf("persist pending test: %v", err)
	}
	runner.mu.Unlock()

	reservation, err := store.GetCapacityReservation(timeslotID)
	if err != nil {
		t.Fatalf("GetCapacityReservation: %v", err)
	}
	reservation.ApprovalState = "rejected"
	if err := store.SaveCapacityReservation(reservation); err != nil {
		t.Fatalf("invalidate capacity reservation: %v", err)
	}

	runner.promotePendingTests()

	got, ok := runner.GetTest(test.ID)
	if !ok {
		t.Fatalf("pending test %s disappeared", test.ID)
	}
	if got.Status != "FAILED" {
		t.Fatalf("status = %q, want FAILED after timeslot invalidation", got.Status)
	}
	if !strings.Contains(got.Error, "capacity reservation is no longer valid") || !strings.Contains(got.Error, "no longer approved") {
		t.Fatalf("error = %q, want invalidated timeslot detail", got.Error)
	}
}

func TestPendingPromotionUsesActualTimeForFutureCapacityTimeslot(t *testing.T) {
	runner, store := storeBackedRunner(t)
	futureStart := ceilToNextQuarterHour(time.Now().UTC().Add(time.Hour))
	timeslotID := "pending-future-slot"
	capacity := 100
	if err := store.SaveCapacityReservation(&storage.CapacityReservation{
		ID: timeslotID, ProjectID: "proj-a", StartAt: futureStart, EndAt: futureStart.Add(time.Hour),
		ApprovalState:     "approved",
		RequestedCapacity: &storage.CapacityReservationRequest{VUsers: &capacity},
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	created, err := runner.CreateTest(CreateTestRequest{
		Name: "Future timeslot pending promotion", Duration: "10m", ScriptID: "script-1", VUsers: 10, ProjectID: "proj-a",
		ScheduledStartAt: &futureStart, TimeslotID: &timeslotID,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	pendingSince := time.Now().UTC().Add(-time.Minute)
	runner.mu.Lock()
	pending := runner.tests[created.ID]
	pending.Status = "PENDING"
	pending.PendingSince = &pendingSince
	pending.PendingReason = "waiting for capacity"
	if err := runner.saveManagedTestRecord(pending); err != nil {
		runner.mu.Unlock()
		t.Fatalf("persist pending state: %v", err)
	}
	runner.mu.Unlock()

	started := make(chan string, 1)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: started})
	runner.promotePendingTests()
	select {
	case id := <-started:
		t.Fatalf("future-slot pending test %q dispatched early", id)
	case <-time.After(100 * time.Millisecond):
	}
	current, ok := runner.GetTest(created.ID)
	if !ok {
		t.Fatalf("pending test %s disappeared", created.ID)
	}
	if current.Status != "FAILED" {
		t.Fatalf("status = %q, want FAILED after actual-time promotion check", current.Status)
	}
	if !strings.Contains(current.Error, "capacity reservation is no longer valid") || !strings.Contains(current.Error, "no longer fits") {
		t.Fatalf("error = %q, want actual-time timeslot failure", current.Error)
	}
}

func TestPendingCapacityOverrideSurvivesQueueAndHAReload(t *testing.T) {
	runner, store := storeBackedRunner(t)
	settings := runner.GetSystemSettings()
	settings.General.MaxConcurrentTests = 1
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}

	active, err := runner.CreateTest(CreateTestRequest{
		Name: "Capacity slot holder", Duration: "30s", ScriptID: "script-1", ProjectID: "proj-a", VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest(active): %v", err)
	}
	pending, err := runner.CreateTest(CreateTestRequest{
		Name: "Capacity override queued", Duration: "30s", ScriptID: "script-1", ProjectID: "proj-a", VUsers: 15000,
	})
	if err != nil {
		t.Fatalf("CreateTest(pending): %v", err)
	}
	runner.mu.Lock()
	runner.tests[active.ID].Status = "RUNNING"
	runner.mu.Unlock()

	actor := testCreationActor{Subject: "user:capacity-approver", DisplayName: "Capacity Approver"}
	if err := runner.StartTestWithActor(pending.ID, StartTestOptions{OverrideCapacityShortfall: true}, actor); err != nil {
		t.Fatalf("StartTestWithActor: %v", err)
	}
	queued, ok := runner.GetTest(pending.ID)
	if !ok || queued.Status != "PENDING" {
		t.Fatalf("queued test = %#v, want PENDING", queued)
	}
	if queued.StartAuthorization == nil || queued.StartAuthorization.CapacityShortfall == nil {
		t.Fatalf("start authorization = %#v, want capacity approval", queued.StartAuthorization)
	}
	capacityApproval := queued.StartAuthorization.CapacityShortfall
	if capacityApproval.ApprovedBySubject != actor.Subject || capacityApproval.TotalVUsers != 15000 || capacityApproval.ShortfallGenerators <= 0 {
		t.Fatalf("capacity approval = %#v, want bounded actor/workload provenance", capacityApproval)
	}

	// A standby reconstructs the approval solely from the shared durable row.
	// This exercises the same decode/load path used during HA acquisition without
	// allowing the standby to reconcile or promote execution state.
	follower := newExecutionLeadership(store, "capacity-authorization-follower")
	reloadedRunner := NewTestRunner(1, NewHub(), store, WithExecutionLeadership(follower))
	reloaded, ok := reloadedRunner.GetTest(pending.ID)
	if !ok || reloaded.StartAuthorization == nil || reloaded.StartAuthorization.CapacityShortfall == nil {
		t.Fatalf("reloaded start authorization = %#v, want persisted capacity approval", reloaded)
	}
	if got := reloaded.StartAuthorization.CapacityShortfall; got.ApprovedBySubject != actor.Subject || got.TotalVUsers != 15000 || got.ShortfallGenerators != capacityApproval.ShortfallGenerators {
		t.Fatalf("reloaded capacity approval = %#v, want %#v", got, capacityApproval)
	}

	started := make(chan string, 1)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: started})
	runner.mu.Lock()
	runner.tests[active.ID].Status = "COMPLETE"
	runner.mu.Unlock()
	runner.promotePendingTests()

	select {
	case gotID := <-started:
		if gotID != pending.ID {
			t.Fatalf("started test = %q, want %q", gotID, pending.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("capacity-authorized pending test was not promoted")
	}
	startedTest, _ := runner.GetTest(pending.ID)
	if startedTest.Status != "INITIALIZING" {
		t.Fatalf("status = %q, want INITIALIZING after authorized promotion", startedTest.Status)
	}
}

func TestPendingCapacityOverrideRequiresReapprovalWhenShortfallWorsens(t *testing.T) {
	runner, _ := storeBackedRunner(t)
	settings := runner.GetSystemSettings()
	settings.General.MaxConcurrentTests = 1
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings(initial): %v", err)
	}

	active, err := runner.CreateTest(CreateTestRequest{
		Name: "Capacity slot holder", Duration: "30s", ScriptID: "script-1", ProjectID: "proj-a", VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest(active): %v", err)
	}
	pending, err := runner.CreateTest(CreateTestRequest{
		Name: "Capacity reapproval queued", Duration: "30s", ScriptID: "script-1", ProjectID: "proj-a", VUsers: 15000,
	})
	if err != nil {
		t.Fatalf("CreateTest(pending): %v", err)
	}
	runner.mu.Lock()
	runner.tests[active.ID].Status = "RUNNING"
	runner.mu.Unlock()

	actor := testCreationActor{Subject: "user:capacity-approver", DisplayName: "Capacity Approver"}
	if err := runner.StartTestWithActor(pending.ID, StartTestOptions{OverrideCapacityShortfall: true}, actor); err != nil {
		t.Fatalf("StartTestWithActor(initial approval): %v", err)
	}
	queued, ok := runner.GetTest(pending.ID)
	if !ok || queued.Status != "PENDING" || queued.StartAuthorization == nil || queued.StartAuthorization.CapacityShortfall == nil {
		t.Fatalf("queued test = %#v, want PENDING with capacity approval", queued)
	}
	initialApproval := queued.StartAuthorization.CapacityShortfall
	if initialApproval.RecommendedGenerators != 2 || initialApproval.ShortfallGenerators != 1 {
		t.Fatalf("initial capacity approval = %#v, want 2 recommended/1 shortfall", initialApproval)
	}

	// A lower per-generator density makes the same immutable test require one
	// additional generator. The previous approval must not silently expand to
	// cover that larger shortfall when the global test slot becomes available.
	settings = runner.GetSystemSettings()
	settings.CapacityPlanning.Generator.MaxVUsers = 5000
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings(worsened capacity): %v", err)
	}
	started := make(chan string, 1)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: started})
	runner.mu.Lock()
	runner.tests[active.ID].Status = "COMPLETE"
	runner.mu.Unlock()
	runner.promotePendingTests()

	select {
	case id := <-started:
		t.Fatalf("test %q dispatched with a stale capacity approval", id)
	case <-time.After(100 * time.Millisecond):
	}
	reapproval, ok := runner.GetTest(pending.ID)
	if !ok || reapproval.Status != "PENDING" {
		t.Fatalf("test after worsened capacity = %#v, want PENDING", reapproval)
	}
	if !strings.HasPrefix(reapproval.PendingReason, pendingCapacityAuthorizationReasonPrefix) {
		t.Fatalf("pending reason = %q, want explicit capacity reapproval reason", reapproval.PendingReason)
	}
	if reapproval.CapacityAdvisory == nil || reapproval.CapacityAdvisory.RecommendedGenerators <= initialApproval.RecommendedGenerators || reapproval.CapacityAdvisory.ShortfallGenerators <= initialApproval.ShortfallGenerators {
		t.Fatalf("worsened capacity advisory = %#v, want larger recommendation and shortfall than %#v", reapproval.CapacityAdvisory, initialApproval)
	}
	worsenedAdvisory := cloneCapacityAdvisory(reapproval.CapacityAdvisory)
	if got := reapproval.StartAuthorization.CapacityShortfall; got.RecommendedGenerators != 2 || got.ShortfallGenerators != 1 {
		t.Fatalf("stale authorization was silently expanded: %#v", got)
	}

	if err := runner.StartTestWithActor(pending.ID, StartTestOptions{OverrideCapacityShortfall: true}, actor); err != nil {
		t.Fatalf("StartTestWithActor(reapproval): %v", err)
	}
	select {
	case gotID := <-started:
		if gotID != pending.ID {
			t.Fatalf("started test = %q, want %q", gotID, pending.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("capacity-reapproved pending test was not dispatched")
	}
	startedTest, _ := runner.GetTest(pending.ID)
	if startedTest.Status != "INITIALIZING" {
		t.Fatalf("status = %q, want INITIALIZING after reapproval", startedTest.Status)
	}
	updatedApproval := startedTest.StartAuthorization.CapacityShortfall
	if updatedApproval.RecommendedGenerators != worsenedAdvisory.RecommendedGenerators || updatedApproval.ShortfallGenerators != worsenedAdvisory.ShortfallGenerators {
		t.Fatalf("updated capacity approval = %#v, want bounds from %#v", updatedApproval, worsenedAdvisory)
	}
}

func TestPendingShakeoutOverrideSurvivesQueueAndHAReload(t *testing.T) {
	runner, store := storeBackedRunner(t)
	settings := runner.GetSystemSettings()
	settings.General.MaxConcurrentTests = 1
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	runner.StoreScript(&dsl.Script{
		ID:        "pending-shakeout-override-script",
		Name:      "Pending shakeout override",
		ProjectID: "proj-a",
		Protocol:  "definitely-not-supported",
		Version:   "1",
		Actions:   []dsl.Action{{Name: "broken", Type: "request", Target: "/"}},
	})

	active, err := runner.CreateTest(CreateTestRequest{
		Name: "Shakeout slot holder", Duration: "30s", ScriptID: "script-1", ProjectID: "proj-a", VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest(active): %v", err)
	}
	review, err := runner.CreateTest(CreateTestRequest{
		Name:           "Shakeout override queued",
		Duration:       "30s",
		ScriptID:       "pending-shakeout-override-script",
		ProjectID:      "proj-a",
		VUsers:         1,
		ScriptShakeout: true,
	})
	if err != nil {
		t.Fatalf("CreateTest(review): %v", err)
	}
	if err := runner.StartTest(review.ID); err == nil {
		t.Fatal("initial failing shakeout returned nil")
	}
	blocked, _ := runner.GetTest(review.ID)
	if blocked.Status != "SHAKEOUT_REVIEW" || strings.TrimSpace(blocked.Error) == "" {
		t.Fatalf("shakeout result = %#v, want SHAKEOUT_REVIEW evidence", blocked)
	}
	wantEvidenceHash := sha256String(strings.TrimSpace(blocked.Error))

	runner.mu.Lock()
	runner.tests[active.ID].Status = "RUNNING"
	runner.mu.Unlock()
	actor := testCreationActor{Subject: "user:shakeout-approver", DisplayName: "Shakeout Approver"}
	if err := runner.StartTestWithActor(review.ID, StartTestOptions{OverrideScriptShakeoutError: true}, actor); err != nil {
		t.Fatalf("StartTestWithActor(override): %v", err)
	}
	queued, _ := runner.GetTest(review.ID)
	if queued.Status != "PENDING" || queued.StartAuthorization == nil || queued.StartAuthorization.ScriptShakeout == nil {
		t.Fatalf("queued test = %#v, want PENDING with shakeout approval", queued)
	}
	shakeoutApproval := queued.StartAuthorization.ScriptShakeout
	if shakeoutApproval.ApprovedBySubject != actor.Subject || shakeoutApproval.ReviewEvidenceSHA256 != wantEvidenceHash {
		t.Fatalf("shakeout approval = %#v, want actor and reviewed-evidence hash %q", shakeoutApproval, wantEvidenceHash)
	}

	follower := newExecutionLeadership(store, "shakeout-authorization-follower")
	reloadedRunner := NewTestRunner(1, NewHub(), store, WithExecutionLeadership(follower))
	reloaded, ok := reloadedRunner.GetTest(review.ID)
	if !ok || reloaded.StartAuthorization == nil || reloaded.StartAuthorization.ScriptShakeout == nil {
		t.Fatalf("reloaded test = %#v, want persisted shakeout approval", reloaded)
	}
	if got := reloaded.StartAuthorization.ScriptShakeout; got.ApprovedBySubject != actor.Subject || got.ReviewEvidenceSHA256 != wantEvidenceHash {
		t.Fatalf("reloaded shakeout approval = %#v, want actor/evidence provenance", got)
	}

	started := make(chan string, 1)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: started})
	runner.mu.Lock()
	runner.tests[active.ID].Status = "COMPLETE"
	runner.mu.Unlock()
	runner.promotePendingTests()
	select {
	case gotID := <-started:
		if gotID != review.ID {
			t.Fatalf("started test = %q, want %q", gotID, review.ID)
		}
	case <-time.After(5 * time.Second):
		latest, _ := runner.GetTest(review.ID)
		t.Fatalf("shakeout-authorized pending test was not promoted; status=%q error=%q", latest.Status, latest.Error)
	}
	startedTest, _ := runner.GetTest(review.ID)
	if startedTest.Status != "INITIALIZING" {
		t.Fatalf("status = %q, want INITIALIZING; persisted approval must skip shakeout rerun", startedTest.Status)
	}
}

func TestCapacityStartAuthorizationIsBounded(t *testing.T) {
	now := time.Now().UTC()
	authorization := &CapacityShortfallStartAuthorization{
		ApprovedAt:            now,
		TotalVUsers:           15000,
		RecommendedGenerators: 2,
		AvailableGenerators:   1,
		ShortfallGenerators:   1,
	}
	tests := []struct {
		name     string
		advisory CapacityAdvisory
		want     bool
	}{
		{name: "same reviewed shortfall", advisory: CapacityAdvisory{RequiresOverride: true, TotalVUsers: 15000, RecommendedGenerators: 2, AvailableGenerators: 1, ShortfallGenerators: 1}, want: true},
		{name: "improved shortfall", advisory: CapacityAdvisory{RequiresOverride: true, TotalVUsers: 15000, RecommendedGenerators: 2, AvailableGenerators: 2, ShortfallGenerators: 0}, want: true},
		{name: "changed workload", advisory: CapacityAdvisory{RequiresOverride: true, TotalVUsers: 16000, RecommendedGenerators: 2, AvailableGenerators: 1, ShortfallGenerators: 1}, want: false},
		{name: "larger recommendation", advisory: CapacityAdvisory{RequiresOverride: true, TotalVUsers: 15000, RecommendedGenerators: 3, AvailableGenerators: 2, ShortfallGenerators: 1}, want: false},
		{name: "larger shortfall", advisory: CapacityAdvisory{RequiresOverride: true, TotalVUsers: 15000, RecommendedGenerators: 2, AvailableGenerators: 0, ShortfallGenerators: 2}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := capacityStartAuthorizationAllows(authorization, &test.advisory); got != test.want {
				t.Fatalf("capacityStartAuthorizationAllows() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestPendingPromotionRevalidatesRequiredSecrets(t *testing.T) {
	const (
		projectID  = "pending-promotion-secret-project"
		secretName = "API_TOKEN"
	)
	secretEnvironmentName := engine.ProjectSecretEnvironmentName(projectID, secretName)
	t.Setenv(secretEnvironmentName, "available-while-queued")

	runner := NewTestRunner(1, NewHub(), nil)
	runner.StoreScript(&dsl.Script{
		ID:        "pending-promotion-secret-script",
		Name:      "Pending promotion secret",
		ProjectID: projectID,
		Protocol:  "http",
		Version:   "1",
		Actions: []dsl.Action{{
			Name:    "authorized request",
			Type:    "GET",
			Target:  "/",
			Headers: map[string]string{"Authorization": "Bearer {{secret.API_TOKEN}}"},
		}},
	})
	active, err := runner.CreateTest(CreateTestRequest{
		Name: "Secret slot holder", Duration: "30s", ScriptID: "pending-promotion-secret-script", ProjectID: projectID, VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest(active): %v", err)
	}
	pending, err := runner.CreateTest(CreateTestRequest{
		Name: "Secret queued run", Duration: "30s", ScriptID: "pending-promotion-secret-script", ProjectID: projectID, VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest(pending): %v", err)
	}
	runner.mu.Lock()
	runner.tests[active.ID].Status = "RUNNING"
	runner.mu.Unlock()
	if err := runner.StartTest(pending.ID); err != nil {
		t.Fatalf("StartTest(pending): %v", err)
	}
	queued, _ := runner.GetTest(pending.ID)
	if queued.Status != "PENDING" {
		t.Fatalf("status = %q, want PENDING while the global slot is occupied", queued.Status)
	}

	// Simulate rotation/removal after the start request was accepted into the
	// queue but before capacity became available.
	if err := os.Unsetenv(secretEnvironmentName); err != nil {
		t.Fatalf("Unsetenv(%s): %v", secretEnvironmentName, err)
	}
	started := make(chan string, 1)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: started})
	runner.mu.Lock()
	runner.tests[active.ID].Status = "COMPLETE"
	runner.mu.Unlock()
	runner.promotePendingTests()

	deferred, ok := runner.GetTest(pending.ID)
	if !ok {
		t.Fatalf("pending test %s disappeared", pending.ID)
	}
	if deferred.Status != "PENDING" {
		t.Fatalf("status = %q, want PENDING after correctable required secret removal", deferred.Status)
	}
	if !strings.Contains(deferred.PendingReason, "secret requirements are no longer satisfied") || !strings.Contains(deferred.PendingReason, secretName) {
		t.Fatalf("pending reason = %q, want durable promotion secret preflight detail", deferred.PendingReason)
	}
	select {
	case id := <-started:
		t.Fatalf("backend launched pending test %q after required secret removal", id)
	default:
	}
}

func TestPendingPromotionRevalidatesRequiredSecretsAfterShakeout(t *testing.T) {
	const (
		projectID  = "pending-shakeout-secret-project"
		secretName = "SESSION_TOKEN"
	)
	secretEnvironmentName := engine.ProjectSecretEnvironmentName(projectID, secretName)
	t.Setenv(secretEnvironmentName, "available-at-shakeout-start")

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var enteredOnce sync.Once
	protocolName := registerRunnerTestProtocol(t, "pending-shakeout-secret", func() protocol.Protocol {
		return &blockingPendingPromotionProtocol{entered: entered, release: release, enteredOnce: &enteredOnce}
	})
	runner := NewTestRunner(1, NewHub(), nil)
	runner.StoreScript(&dsl.Script{
		ID:        "pending-shakeout-secret-script",
		Name:      "Pending shakeout secret race",
		ProjectID: projectID,
		Protocol:  protocolName,
		Version:   "1",
		Actions: []dsl.Action{{
			Name:    "authorized shakeout request",
			Type:    "request",
			Target:  "/",
			Headers: map[string]string{"Authorization": "Bearer {{secret.SESSION_TOKEN}}"},
		}},
	})
	pending, err := runner.CreateTest(CreateTestRequest{
		Name:           "Secret removed during shakeout",
		Duration:       "30s",
		ScriptID:       "pending-shakeout-secret-script",
		ProjectID:      projectID,
		VUsers:         1,
		ScriptShakeout: true,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	pendingSince := time.Now().UTC().Add(-time.Minute)
	runner.mu.Lock()
	runner.tests[pending.ID].Status = "PENDING"
	runner.tests[pending.ID].PendingSince = &pendingSince
	runner.tests[pending.ID].PendingReason = "waiting for capacity"
	runner.mu.Unlock()
	started := make(chan string, 1)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: started})

	done := make(chan struct{})
	go func() {
		runner.promotePendingTests()
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("pending promotion did not enter shakeout with the configured secret")
	}
	// The action already resolved and used the secret to enter the protocol.
	// Remove it while shakeout is outside tr.mu; the post-shakeout check must
	// observe the removal before INITIALIZING.
	if err := os.Unsetenv(secretEnvironmentName); err != nil {
		t.Fatalf("Unsetenv(%s): %v", secretEnvironmentName, err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pending promotion did not finish after shakeout release")
	}

	deferred, ok := runner.GetTest(pending.ID)
	if !ok {
		t.Fatalf("pending test %s disappeared", pending.ID)
	}
	if deferred.Status != "PENDING" {
		t.Fatalf("status = %q, want PENDING after correctable secret removal during shakeout", deferred.Status)
	}
	if !strings.Contains(deferred.PendingReason, "secret requirements are no longer satisfied") || !strings.Contains(deferred.PendingReason, secretName) {
		t.Fatalf("pending reason = %q, want durable post-shakeout secret preflight detail", deferred.PendingReason)
	}
	select {
	case id := <-started:
		t.Fatalf("backend launched pending test %q after shakeout-time secret removal", id)
	default:
	}
}

func TestPendingPromotionTerminalizesMalformedSecretReferences(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.StoreScript(&dsl.Script{
		ID:       "pending-malformed-secret-script",
		Name:     "Pending malformed secret",
		Protocol: "http",
		Version:  "1",
		Actions:  []dsl.Action{{Name: "request", Type: "GET", Target: "/"}},
	})
	pending, err := runner.CreateTest(CreateTestRequest{
		Name: "Malformed immutable secret reference", Duration: "30s", ScriptID: "pending-malformed-secret-script", VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	pendingSince := time.Now().UTC().Add(-time.Minute)
	runner.mu.Lock()
	managed := runner.tests[pending.ID]
	managed.Status = "PENDING"
	managed.PendingSince = &pendingSince
	managed.PendingReason = "waiting for capacity"
	// Simulate an immutable snapshot restored from legacy/corrupt storage. This
	// cannot be fixed by configuring a provider value, unlike a missing name.
	managed.groupSpecs[0].Script.Actions[0].Body = "{{secret.BAD NAME}}"
	runner.mu.Unlock()
	started := make(chan string, 1)
	runner.setExecutionBackend(&scheduleExecutionBackend{started: started})

	runner.promotePendingTests()
	failed, ok := runner.GetTest(pending.ID)
	if !ok {
		t.Fatalf("pending test %s disappeared", pending.ID)
	}
	if failed.Status != "FAILED" {
		t.Fatalf("status = %q, want FAILED for immutable malformed secret reference", failed.Status)
	}
	if !strings.Contains(failed.Error, "secret requirements are no longer satisfied") || !strings.Contains(failed.Error, "inspect project secret references") {
		t.Fatalf("error = %q, want terminal secret-reference evidence", failed.Error)
	}
	select {
	case id := <-started:
		t.Fatalf("backend launched malformed-secret pending test %q", id)
	default:
	}
}
