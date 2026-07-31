package api

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

type distributedGoalScaleSpy struct {
	calls atomic.Int64
}

func (s *distributedGoalScaleSpy) Run(*TestRunner, *ManagedTest) {}

func (s *distributedGoalScaleSpy) ScaleVUsers(*TestRunner, *ManagedTest, string, int) error {
	s.calls.Add(1)
	return nil
}

func newDistributedThresholdTestRunner(t *testing.T) (*TestRunner, *ManagedTest, *distributedExecutionBackend) {
	t.Helper()
	runner := NewTestRunner(1, NewHub(), nil)
	maxP99 := 500.0
	samples := 10
	runner.settings.Thresholds.MaxP99LatencyMs = &maxP99
	runner.settings.Thresholds.P99LatencyStopConsecutiveSamples = &samples

	const target = "generator-a"
	testRun := &ManagedTest{
		ID:                    "distributed-threshold-freshness",
		Name:                  "Distributed threshold freshness",
		Status:                "RUNNING",
		Groups:                []TestGroup{{Name: "buyers", VUsers: 1}},
		remoteClientsByTarget: map[string]generatorClient{target: nil},
	}
	runner.tests[testRun.ID] = testRun
	return runner, testRun, &distributedExecutionBackend{}
}

func distributedP99Batch(testID string, timestampUnixMs int64, durationMs float64) generatorrpc.MetricsBatch {
	return generatorrpc.MetricsBatch{
		TestID:          testID,
		LifecycleState:  "RUNNING",
		TimestampUnixMs: timestampUnixMs,
		WindowMs:        1000,
		Stats:           generatorrpc.GeneratorStats{ActiveVUsers: 1},
		Groups:          []generatorrpc.GroupMetric{{Name: "buyers", ActiveVUsers: 1}},
		Transactions: []generatorrpc.TransactionMetric{{
			Name:       "Checkout",
			Group:      "buyers",
			DurationMs: durationMs,
			Success:    true,
		}},
	}
}

func distributedGoalBatch(testID string, timestampUnixMs int64, tps, p95Ms float64, errors int64) generatorrpc.MetricsBatch {
	count := int64(100)
	if errors > count {
		count = errors
	}
	return generatorrpc.MetricsBatch{
		TestID:          testID,
		LifecycleState:  "RUNNING",
		TimestampUnixMs: timestampUnixMs,
		WindowMs:        1000,
		Stats:           generatorrpc.GeneratorStats{ActiveVUsers: 10},
		FleetMetrics: &generatorrpc.MetricAggregate{
			Count:  count,
			Errors: errors,
			TPS:    tps,
			AvgMs:  p95Ms,
			P95Ms:  p95Ms,
			P99Ms:  p95Ms,
		},
		Groups: []generatorrpc.GroupMetric{{Name: "buyers", ActiveVUsers: 10}},
	}
}

func TestDistributedMetricsDuplicateAndStaleWindowsDoNotAdvanceP99Samples(t *testing.T) {
	runner, testRun, backend := newDistributedThresholdTestRunner(t)
	const target = "generator-a"
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)

	first := distributedP99Batch(testRun.ID, 2_000, 900)
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, target, first) {
		t.Fatal("first metrics window was not accepted")
	}
	publish, _, _ := backend.shouldPublishDistributedMetricsBatch(runner, testRun, now)
	if !publish {
		t.Fatal("first accepted metrics window was not selected for immediate publication")
	}
	live, _, _, fresh, publication := backend.applyStoredRemoteMetricsBatch(runner, testRun, target)
	if !fresh || live == nil || live.P99Ms <= 500 {
		t.Fatalf("first publication fresh=%v live=%#v, want fresh breaching P99", fresh, live)
	}
	if !backend.publishDistributedMetricsRevision(context.Background(), runner, testRun, live, publication).published {
		t.Fatal("first accepted metrics revision was not published")
	}
	if got := testRun.p99BreachConsecutive; got != 1 {
		t.Fatalf("P99 breach samples after first window = %d, want 1", got)
	}

	for name, duplicateOrStale := range map[string]generatorrpc.MetricsBatch{
		"duplicate": distributedP99Batch(testRun.ID, 2_000, 950),
		"stale":     distributedP99Batch(testRun.ID, 1_000, 975),
	} {
		if backend.storeRemoteMetricsBatchOnly(runner, testRun, target, duplicateOrStale) {
			t.Fatalf("%s metrics window was accepted", name)
		}
		_, _, _, accepted, _ := backend.applyRemoteMetricsBatchWithFreshness(runner, testRun, target, duplicateOrStale, false)
		if accepted {
			t.Fatalf("%s metrics window reported fresh", name)
		}
		if got := testRun.p99BreachConsecutive; got != 1 {
			t.Fatalf("P99 breach samples after %s window = %d, want 1", name, got)
		}
	}

	next := distributedP99Batch(testRun.ID, 3_000, 925)
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, target, next) {
		t.Fatal("newer metrics window was not accepted")
	}
	publish, _, _ = backend.shouldPublishDistributedMetricsBatch(runner, testRun, now.Add(2*time.Second))
	if !publish {
		t.Fatal("newer accepted metrics window was not selected for publication")
	}
	live, _, _, fresh, publication = backend.applyStoredRemoteMetricsBatch(runner, testRun, target)
	if !fresh {
		t.Fatal("newer metrics window did not report fresh")
	}
	if !backend.publishDistributedMetricsRevision(context.Background(), runner, testRun, live, publication).published {
		t.Fatal("newer accepted metrics revision was not published")
	}
	if got := testRun.p99BreachConsecutive; got != 2 {
		t.Fatalf("P99 breach samples after newer window = %d, want 2", got)
	}
}

func TestDistributedMetricsDelayedFlushDoesNotReplayP99Samples(t *testing.T) {
	runner, testRun, backend := newDistributedThresholdTestRunner(t)
	const target = "generator-a"
	firstToken := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)

	first := distributedP99Batch(testRun.ID, 2_000, 900)
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, target, first) {
		t.Fatal("first delayed metrics window was not accepted")
	}
	runner.mu.Lock()
	testRun.remoteMetricsLastPublishedAt = firstToken
	testRun.remoteMetricsFlushScheduled = true
	runner.mu.Unlock()

	backend.flushStoredDistributedMetricsAfterDelay(context.Background(), runner, testRun, target, 0, firstToken)
	if got := testRun.p99BreachConsecutive; got != 1 {
		t.Fatalf("P99 breach samples after delayed flush = %d, want 1", got)
	}

	// The same timer callback and duplicate/stale timestamps cannot replay the
	// stored snapshot into threshold evaluation.
	backend.flushStoredDistributedMetricsAfterDelay(context.Background(), runner, testRun, target, 0, firstToken)
	for name, duplicateOrStale := range map[string]generatorrpc.MetricsBatch{
		"duplicate": distributedP99Batch(testRun.ID, 2_000, 950),
		"stale":     distributedP99Batch(testRun.ID, 1_000, 975),
	} {
		if backend.storeRemoteMetricsBatchOnly(runner, testRun, target, duplicateOrStale) {
			t.Fatalf("%s delayed metrics window was accepted", name)
		}
	}
	if got := testRun.p99BreachConsecutive; got != 1 {
		t.Fatalf("P99 breach samples after delayed replay attempts = %d, want 1", got)
	}

	// An old timer must neither publish nor clear a newer scheduled flush.
	next := distributedP99Batch(testRun.ID, 3_000, 925)
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, target, next) {
		t.Fatal("newer delayed metrics window was not accepted")
	}
	secondToken := firstToken.Add(time.Second)
	runner.mu.Lock()
	testRun.remoteMetricsLastPublishedAt = secondToken
	testRun.remoteMetricsFlushScheduled = true
	runner.mu.Unlock()
	backend.flushStoredDistributedMetricsAfterDelay(context.Background(), runner, testRun, target, 0, firstToken)
	runner.mu.RLock()
	stillScheduled := testRun.remoteMetricsFlushScheduled
	runner.mu.RUnlock()
	if !stillScheduled {
		t.Fatal("old delayed callback cleared the newer scheduled flush")
	}
	if got := testRun.p99BreachConsecutive; got != 1 {
		t.Fatalf("old delayed callback advanced P99 breach samples to %d, want 1", got)
	}

	backend.flushStoredDistributedMetricsAfterDelay(context.Background(), runner, testRun, target, 0, secondToken)
	if got := testRun.p99BreachConsecutive; got != 2 {
		t.Fatalf("P99 breach samples after newer delayed flush = %d, want 2", got)
	}
}

func TestDistributedMetricsPublicationRejectsReorderedOlderAggregate(t *testing.T) {
	runner, testRun, backend := newDistributedThresholdTestRunner(t)
	const target = "generator-a"
	olderComputed := make(chan struct{})
	releaseOlder := make(chan struct{})
	olderPublished := make(chan bool, 1)

	go func() {
		live, _, _, fresh, publication := backend.applyRemoteMetricsBatchWithFreshness(
			runner,
			testRun,
			target,
			distributedP99Batch(testRun.ID, 2_000, 900),
			false,
		)
		close(olderComputed)
		<-releaseOlder
		olderPublished <- fresh && backend.publishDistributedMetricsRevision(
			context.Background(),
			runner,
			testRun,
			live,
			publication,
		).published
	}()

	select {
	case <-olderComputed:
	case <-time.After(2 * time.Second):
		t.Fatal("older aggregate did not finish computing")
	}

	newerLive, _, _, newerFresh, newerPublication := backend.applyRemoteMetricsBatchWithFreshness(
		runner,
		testRun,
		target,
		distributedP99Batch(testRun.ID, 3_000, 925),
		false,
	)
	if !newerFresh {
		t.Fatal("newer aggregate was not accepted")
	}
	if !backend.publishDistributedMetricsRevision(context.Background(), runner, testRun, newerLive, newerPublication).published {
		t.Fatal("newer aggregate was not published")
	}
	close(releaseOlder)

	select {
	case published := <-olderPublished:
		if published {
			t.Fatal("older aggregate published after the newer revision")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("older aggregate publication did not finish")
	}

	runner.mu.RLock()
	defer runner.mu.RUnlock()
	if testRun.remoteMetricsAcceptedRevision != 2 || testRun.remoteMetricsPublishedRevision != 2 {
		t.Fatalf(
			"metrics revisions accepted=%d published=%d, want 2/2",
			testRun.remoteMetricsAcceptedRevision,
			testRun.remoteMetricsPublishedRevision,
		)
	}
	if testRun.LiveMetrics == nil || testRun.LiveMetrics.Timestamp.UnixMilli() != 3_000 {
		t.Fatalf("latest metrics were overwritten by the older aggregate: %#v", testRun.LiveMetrics)
	}
	if len(testRun.History) != 1 || testRun.History[0].Timestamp.UnixMilli() != 3_000 {
		t.Fatalf("metrics history contains reordered publication: %#v", testRun.History)
	}
	if testRun.p99BreachConsecutive != 1 {
		t.Fatalf("reordered older aggregate reevaluated P99 threshold: samples=%d, want 1", testRun.p99BreachConsecutive)
	}
}

func TestDistributedMetricsLosingRevisionCannotMutateGoalStateOrScale(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := &distributedExecutionBackend{}
	scaleSpy := &distributedGoalScaleSpy{}
	targetTPS := 100.0
	maxP95 := 500.0
	minVUsers, maxVUsers := 1, 100
	const target = "generator-a"
	testRun := &ManagedTest{
		ID:               "distributed-goal-reordered",
		Status:           "RUNNING",
		ScenarioMode:     "goal_oriented",
		TotalVUsers:      10,
		Groups:           []TestGroup{{Name: "buyers", VUsers: 10}},
		executionBackend: scaleSpy,
		GoalConfig: &GoalConfig{
			TargetAverageTPS: &targetTPS,
			P95LatencyMs:     &maxP95,
			MinVUsers:        &minVUsers,
			MaxVUsers:        &maxVUsers,
		},
		goalControl:           &goalControllerState{},
		remoteClientsByTarget: map[string]generatorClient{target: nil},
	}
	runner.tests[testRun.ID] = testRun

	olderLive, _, _, olderFresh, olderPublication := backend.applyRemoteMetricsBatchWithFreshness(
		runner,
		testRun,
		target,
		distributedGoalBatch(testRun.ID, 2_000, 1, 900, 3),
		false,
	)
	if !olderFresh {
		t.Fatal("older goal revision was not accepted")
	}
	if testRun.goalControl.breachStartedAt != nil ||
		testRun.goalControl.errorWindowsTracked ||
		testRun.goalControl.cumulativeErrors != 0 ||
		testRun.remoteGoalUpdateRunning ||
		scaleSpy.calls.Load() != 0 {
		t.Fatalf("goal state mutated before publication admission: %#v", testRun.goalControl)
	}

	newerLive, _, _, newerFresh, newerPublication := backend.applyRemoteMetricsBatchWithFreshness(
		runner,
		testRun,
		target,
		distributedGoalBatch(testRun.ID, 3_000, 100, 100, 0),
		false,
	)
	if !newerFresh {
		t.Fatal("newer healthy goal revision was not accepted")
	}
	if !backend.publishDistributedMetricsRevision(context.Background(), runner, testRun, newerLive, newerPublication).published {
		t.Fatal("newer healthy goal revision was not published")
	}
	if backend.publishDistributedMetricsRevision(context.Background(), runner, testRun, olderLive, olderPublication).published {
		t.Fatal("older goal revision published after the newer revision")
	}

	runner.mu.RLock()
	defer runner.mu.RUnlock()
	if testRun.goalControl.breachStartedAt != nil {
		t.Fatalf("losing breach revision mutated breach start: %v", testRun.goalControl.breachStartedAt)
	}
	if !testRun.goalControl.errorWindowsTracked || testRun.goalControl.cumulativeErrors != 3 {
		t.Fatalf("coalesced goal errors = tracked:%v errors:%d, want true/3", testRun.goalControl.errorWindowsTracked, testRun.goalControl.cumulativeErrors)
	}
	if got := testRun.goalControl.lastErrorWindowBySource[target]; got.UnixMilli() != 3_000 {
		t.Fatalf("published goal error timestamp = %v, want 3000ms", got)
	}
	if testRun.remoteGoalUpdateRunning || scaleSpy.calls.Load() != 0 {
		t.Fatalf("losing scale revision scheduled work: running=%v calls=%d", testRun.remoteGoalUpdateRunning, scaleSpy.calls.Load())
	}
	if len(testRun.History) != 1 || testRun.LiveMetrics == nil || testRun.LiveMetrics.Timestamp.UnixMilli() != 3_000 {
		t.Fatalf("goal publication history was reordered: live=%#v history=%#v", testRun.LiveMetrics, testRun.History)
	}
}

func TestDistributedGoalScaleAdmissionIsPublicationFenced(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*TestRunner, *ManagedTest, *distributedGoalScaleSpy)
		wantStale bool
	}{
		{name: "current publication"},
		{
			name: "replacement run",
			mutate: func(runner *TestRunner, testRun *ManagedTest, scaleSpy *distributedGoalScaleSpy) {
				runner.tests[testRun.ID] = &ManagedTest{
					ID:               testRun.ID,
					Status:           "RUNNING",
					TotalVUsers:      10,
					Groups:           []TestGroup{{Name: "buyers", VUsers: 10}},
					executionBackend: scaleSpy,
				}
			},
			wantStale: true,
		},
		{
			name: "new execution generation",
			mutate: func(_ *TestRunner, testRun *ManagedTest, _ *distributedGoalScaleSpy) {
				testRun.executionGeneration++
			},
			wantStale: true,
		},
		{
			name: "new topology generation",
			mutate: func(_ *TestRunner, testRun *ManagedTest, _ *distributedGoalScaleSpy) {
				testRun.remoteControlGeneration++
			},
			wantStale: true,
		},
		{
			name: "new metrics publication",
			mutate: func(_ *TestRunner, testRun *ManagedTest, _ *distributedGoalScaleSpy) {
				testRun.remoteMetricsPublishedRevision++
			},
			wantStale: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := NewTestRunner(1, NewHub(), nil)
			scaleSpy := &distributedGoalScaleSpy{}
			testRun := &ManagedTest{
				ID:                             "distributed-goal-admission",
				Status:                         "RUNNING",
				TotalVUsers:                    10,
				Groups:                         []TestGroup{{Name: "buyers", VUsers: 10}},
				executionBackend:               scaleSpy,
				executionGeneration:            7,
				remoteControlGeneration:        11,
				remoteMetricsPublishedRevision: 13,
				remoteGoalUpdateRunning:        true,
				remoteGoalUpdateRevision:       13,
			}
			runner.tests[testRun.ID] = testRun
			publication := distributedMetricsPublication{
				revision:            13,
				executionGeneration: 7,
				topologyGeneration:  11,
			}
			if tt.mutate != nil {
				tt.mutate(runner, testRun, scaleSpy)
			}

			err := runner.scaleDistributedGoalVUsersNow(testRun, "buyers", 12, publication)
			if tt.wantStale {
				if !errors.Is(err, errStaleDistributedGoalScale) {
					t.Fatalf("scale error = %v, want stale distributed goal scale", err)
				}
				if got := scaleSpy.calls.Load(); got != 0 {
					t.Fatalf("stale publication issued %d backend scale calls, want 0", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("current publication scale: %v", err)
			}
			if got := scaleSpy.calls.Load(); got != 1 {
				t.Fatalf("current publication issued %d backend scale calls, want 1", got)
			}
			runner.mu.RLock()
			gotVUsers := testRun.Groups[0].VUsers
			runner.mu.RUnlock()
			if gotVUsers != 12 {
				t.Fatalf("current publication group VUsers = %d, want 12", gotVUsers)
			}
		})
	}
}

func TestDistributedGoalScaleDropsDecisionSupersededWhileWaitingForAdmission(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := &distributedExecutionBackend{}
	scaleSpy := &distributedGoalScaleSpy{}
	targetTPS := 100.0
	minVUsers, maxVUsers := 1, 100
	const target = "generator-a"
	testRun := &ManagedTest{
		ID:               "distributed-goal-superseded-at-admission",
		Status:           "RUNNING",
		ScenarioMode:     "goal_oriented",
		TotalVUsers:      10,
		Groups:           []TestGroup{{Name: "buyers", VUsers: 10}},
		executionBackend: scaleSpy,
		GoalConfig: &GoalConfig{
			TargetAverageTPS: &targetTPS,
			MinVUsers:        &minVUsers,
			MaxVUsers:        &maxVUsers,
		},
		goalControl:           &goalControllerState{},
		remoteClientsByTarget: map[string]generatorClient{target: nil},
	}
	runner.tests[testRun.ID] = testRun

	// Hold the same group/topology gate used by scaleVUsersNow. The first
	// publication can schedule its asynchronous goal decision, but that
	// decision cannot reserve or mutate VUsers until after a newer publication
	// has become authoritative.
	releaseScaleAdmission := runner.lockDistributedGroupControl(testRun.ID, "buyers")
	defer func() {
		if releaseScaleAdmission != nil {
			releaseScaleAdmission()
		}
	}()

	olderLive, _, _, olderFresh, olderPublication := backend.applyRemoteMetricsBatchWithFreshness(
		runner,
		testRun,
		target,
		distributedGoalBatch(testRun.ID, 2_000, 1, 100, 0),
		false,
	)
	if !olderFresh || !backend.publishDistributedMetricsRevision(context.Background(), runner, testRun, olderLive, olderPublication).published {
		t.Fatal("older scale-producing publication was not admitted")
	}
	runner.mu.RLock()
	updateRunning := testRun.remoteGoalUpdateRunning
	runner.mu.RUnlock()
	if !updateRunning {
		t.Fatal("older publication did not schedule its goal update")
	}

	newerLive, _, _, newerFresh, newerPublication := backend.applyRemoteMetricsBatchWithFreshness(
		runner,
		testRun,
		target,
		distributedGoalBatch(testRun.ID, 3_000, 100, 100, 0),
		false,
	)
	if !newerFresh || !backend.publishDistributedMetricsRevision(context.Background(), runner, testRun, newerLive, newerPublication).published {
		t.Fatal("newer healthy publication was not admitted")
	}
	releaseScaleAdmission()
	releaseScaleAdmission = nil

	deadline := time.Now().Add(time.Second)
	for {
		runner.mu.RLock()
		updateRunning = testRun.remoteGoalUpdateRunning
		runner.mu.RUnlock()
		if !updateRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("superseded goal update did not leave the running state")
		}
		time.Sleep(time.Millisecond)
	}
	if got := scaleSpy.calls.Load(); got != 0 {
		t.Fatalf("superseded goal decision issued %d backend scale calls, want 0", got)
	}
	runner.mu.RLock()
	gotVUsers := testRun.Groups[0].VUsers
	publishedRevision := testRun.remoteMetricsPublishedRevision
	runner.mu.RUnlock()
	if gotVUsers != 10 || publishedRevision != newerPublication.revision {
		t.Fatalf("superseded goal decision mutated state: vusers=%d published_revision=%d want 10/%d", gotVUsers, publishedRevision, newerPublication.revision)
	}
}

func TestDistributedMetricsWinnerCommitsCoalescedTargetErrorWindowsOnce(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := &distributedExecutionBackend{}
	maxErrors := 100
	testRun := &ManagedTest{
		ID:           "distributed-goal-coalesced-errors",
		Status:       "RUNNING",
		ScenarioMode: "goal_oriented",
		TotalVUsers:  10,
		Groups:       []TestGroup{{Name: "buyers", VUsers: 10}},
		GoalConfig:   &GoalConfig{MaxErrorCount: &maxErrors},
		goalControl:  &goalControllerState{},
		remoteClientsByTarget: map[string]generatorClient{
			"generator-a": nil,
			"generator-b": nil,
		},
	}
	runner.tests[testRun.ID] = testRun

	for _, input := range []struct {
		target    string
		timestamp int64
		errors    int64
	}{
		{target: "generator-a", timestamp: 2_000, errors: 2},
		{target: "generator-b", timestamp: 2_000, errors: 3},
		{target: "generator-a", timestamp: 3_000, errors: 4},
	} {
		if !backend.storeRemoteMetricsBatchOnly(
			runner,
			testRun,
			input.target,
			distributedGoalBatch(testRun.ID, input.timestamp, 100, 100, input.errors),
		) {
			t.Fatalf("goal window %+v was not accepted", input)
		}
	}
	if testRun.goalControl.errorWindowsTracked || testRun.goalControl.cumulativeErrors != 0 {
		t.Fatalf("accepted windows mutated goal state before publication: %#v", testRun.goalControl)
	}

	live, _, _, fresh, publication := backend.applyStoredRemoteMetricsBatch(runner, testRun, "generator-a")
	if !fresh || !backend.publishDistributedMetricsRevision(context.Background(), runner, testRun, live, publication).published {
		t.Fatal("coalesced goal aggregate was not published")
	}
	if got := testRun.goalControl.cumulativeErrors; got != 9 {
		t.Fatalf("coalesced cumulative errors = %d, want 9", got)
	}
	if backend.publishDistributedMetricsRevision(context.Background(), runner, testRun, live, publication).published {
		t.Fatal("same coalesced publication was admitted twice")
	}
	if got := testRun.goalControl.cumulativeErrors; got != 9 {
		t.Fatalf("duplicate publication changed cumulative errors to %d, want 9", got)
	}
}

func TestDistributedMetricsPublicationGateRejectsStaleExecutionState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*TestRunner, *ManagedTest)
	}{
		{
			name: "test replacement",
			mutate: func(runner *TestRunner, testRun *ManagedTest) {
				runner.tests[testRun.ID] = &ManagedTest{ID: testRun.ID, Status: "RUNNING"}
			},
		},
		{
			name: "execution generation",
			mutate: func(_ *TestRunner, testRun *ManagedTest) {
				testRun.executionGeneration++
			},
		},
		{
			name: "topology generation",
			mutate: func(_ *TestRunner, testRun *ManagedTest) {
				testRun.remoteControlGeneration++
			},
		},
		{
			name: "stopping non-terminal",
			mutate: func(_ *TestRunner, testRun *ManagedTest) {
				testRun.Status = "STOPPING"
			},
		},
		{
			name: "already terminal",
			mutate: func(_ *TestRunner, testRun *ManagedTest) {
				testRun.Status = "COMPLETE"
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			runner, testRun, backend := newDistributedThresholdTestRunner(t)
			testRun.executionGeneration = 7
			testRun.remoteControlGeneration = 11
			live, _, _, fresh, publication := backend.applyRemoteMetricsBatchWithFreshness(
				runner,
				testRun,
				"generator-a",
				distributedP99Batch(testRun.ID, 2_000, 900),
				false,
			)
			if !fresh {
				t.Fatal("publication candidate was not accepted")
			}
			runner.mu.Lock()
			testCase.mutate(runner, testRun)
			runner.mu.Unlock()

			result := backend.publishDistributedMetricsRevision(context.Background(), runner, testRun, live, publication)
			if result.published || result.lifecycleCurrent {
				t.Fatalf("stale publication result = %+v, want rejected", result)
			}
			runner.mu.RLock()
			defer runner.mu.RUnlock()
			if testRun.LiveMetrics != nil || len(testRun.History) != 0 || testRun.remoteMetricsPublishedRevision != 0 || testRun.p99BreachConsecutive != 0 {
				t.Fatalf("stale publication mutated run state: live=%#v history=%d published=%d p99=%d", testRun.LiveMetrics, len(testRun.History), testRun.remoteMetricsPublishedRevision, testRun.p99BreachConsecutive)
			}
		})
	}
}

func TestDistributedMetricsPublicationWaiterFailsClosedOnCancellation(t *testing.T) {
	runner, testRun, backend := newDistributedThresholdTestRunner(t)
	live, _, _, fresh, publication := backend.applyRemoteMetricsBatchWithFreshness(
		runner,
		testRun,
		"generator-a",
		distributedP99Batch(testRun.ID, 2_000, 900),
		false,
	)
	if !fresh {
		t.Fatal("publication candidate was not accepted")
	}

	runner.mu.Lock()
	gate := make(chan struct{}, 1) // Deliberately no token: publication must wait.
	testRun.remoteMetricsPublishGate = gate
	runner.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan distributedMetricsPublicationResult, 1)
	go func() {
		resultCh <- backend.publishDistributedMetricsRevision(ctx, runner, testRun, live, publication)
	}()
	cancel()

	select {
	case result := <-resultCh:
		if result.published || result.lifecycleCurrent {
			t.Fatalf("cancelled waiter result = %+v, want rejected", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled publication waiter did not return")
	}
	gate <- struct{}{}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	if testRun.LiveMetrics != nil || len(testRun.History) != 0 || testRun.remoteMetricsPublishedRevision != 0 {
		t.Fatalf("cancelled waiter mutated publication state: live=%#v history=%d revision=%d", testRun.LiveMetrics, len(testRun.History), testRun.remoteMetricsPublishedRevision)
	}
}

func TestDistributedMetricsTerminalPublicationAllowedBeforeFinalization(t *testing.T) {
	runner, testRun, backend := newDistributedThresholdTestRunner(t)
	const target = "generator-a"
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, target, distributedP99Batch(testRun.ID, 2_000, 900)) {
		t.Fatal("running snapshot was not accepted")
	}
	runner.mu.Lock()
	testRun.Status = "STOPPING"
	runner.mu.Unlock()
	terminal := generatorrpc.MetricsBatch{
		TestID:          testRun.ID,
		LifecycleState:  "COMPLETE",
		TimestampUnixMs: 3_000,
	}
	live, finalStatus, _, fresh, publication := backend.applyRemoteMetricsBatchWithFreshness(runner, testRun, target, terminal, false)
	if !fresh || finalStatus != "ABORTED" || !publication.terminal {
		t.Fatalf("terminal candidate fresh=%v status=%q publication=%+v", fresh, finalStatus, publication)
	}
	result := backend.publishDistributedMetricsRevision(context.Background(), runner, testRun, live, publication)
	if !result.published || !result.lifecycleCurrent {
		t.Fatalf("legitimate terminal publication result = %+v, want published/current", result)
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	if testRun.LiveMetrics == nil || testRun.LiveMetrics.TotalTx != 1 || len(testRun.History) != 1 {
		t.Fatalf("terminal publication did not preserve/install final metrics: live=%#v history=%#v", testRun.LiveMetrics, testRun.History)
	}
	if testRun.p99BreachConsecutive != 0 {
		t.Fatalf("terminal publication evaluated stop counter: %d", testRun.p99BreachConsecutive)
	}
}

func TestDistributedMetricsTopologyRebasesOwnedSurvivingStream(t *testing.T) {
	runner, testRun, backend := newDistributedThresholdTestRunner(t)
	testRun.executionGeneration = 7
	testRun.remoteControlGeneration = 1
	streamA := &generatorrpc.MetricsStream{}
	streamB := &generatorrpc.MetricsStream{}
	testRun.remoteMetricsStreamsByTarget = map[string]*generatorrpc.MetricsStream{
		"generator-a": streamA,
		"generator-b": streamB,
	}
	// Preserve the legacy primary fields to prove the per-target map remains
	// authoritative when that primary is evacuated.
	testRun.remoteTarget = "generator-a"
	testRun.remoteMetricsStream = streamA
	testRun.remoteClientsByTarget["generator-b"] = nil

	oldFenceB, ok := captureDistributedMetricsConsumerFence(runner, testRun, "generator-b", streamB, context.Background(), 7, false)
	if !ok {
		t.Fatal("surviving stream's initial fence was not current")
	}
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, "generator-b", distributedP99Batch(testRun.ID, 2_000, 900), oldFenceB) {
		t.Fatal("surviving stream's initial batch was not accepted")
	}
	oldLive, _, _, oldFresh, oldPublication := backend.applyStoredRemoteMetricsBatch(runner, testRun, "generator-b", oldFenceB)
	if !oldFresh {
		t.Fatal("surviving stream's initial candidate was not computed")
	}
	timerToken := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	runner.mu.Lock()
	delete(testRun.remoteMetricsStreamsByTarget, "generator-a")
	delete(testRun.remoteMetricsBatchesByTarget, "generator-a")
	testRun.remoteControlGeneration++
	testRun.remoteMetricsLastPublishedAt = timerToken
	testRun.remoteMetricsFlushScheduled = true
	runner.mu.Unlock()

	if backend.publishDistributedMetricsRevision(context.Background(), runner, testRun, oldLive, oldPublication).published {
		t.Fatal("pre-topology candidate published after evacuation")
	}
	if !distributedMetricsConsumerStillOwned(runner, testRun, "generator-b", oldFenceB, false) {
		t.Fatal("topology-only invalidation retired the still-owned surviving stream")
	}
	backend.flushStoredDistributedMetricsAfterDelay(context.Background(), runner, testRun, "generator-b", 0, timerToken, oldFenceB)
	runner.mu.RLock()
	timerStillScheduled := testRun.remoteMetricsFlushScheduled
	runner.mu.RUnlock()
	if !timerStillScheduled {
		t.Fatal("stale pre-topology timer cleared current flush state")
	}
	if staleFenceA, current := captureDistributedMetricsConsumerFence(runner, testRun, "generator-a", streamA, context.Background(), 7, false); current ||
		backend.storeRemoteMetricsBatchOnly(runner, testRun, "generator-a", distributedP99Batch(testRun.ID, 3_000, 950), staleFenceA) {
		t.Fatal("evacuated stream repopulated the metrics batch map")
	}

	newFenceB, current := captureDistributedMetricsConsumerFence(runner, testRun, "generator-b", streamB, context.Background(), 7, false)
	if !current || newFenceB.topologyGeneration != 2 {
		t.Fatalf("surviving stream did not rebase to topology 2: current=%v fence=%+v", current, newFenceB)
	}
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, "generator-b", distributedP99Batch(testRun.ID, 3_000, 925), newFenceB) {
		t.Fatal("surviving stream could not accept a batch after unrelated evacuation")
	}
	newLive, _, _, fresh, publication := backend.applyStoredRemoteMetricsBatch(runner, testRun, "generator-b", newFenceB)
	if !fresh || !backend.publishDistributedMetricsRevision(context.Background(), runner, testRun, newLive, publication).published {
		t.Fatal("surviving stream could not publish after topology rebase")
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	if testRun.LiveMetrics == nil || testRun.LiveMetrics.Timestamp.UnixMilli() != 3_000 || len(testRun.History) != 1 {
		t.Fatalf("surviving stream publication = live:%#v history:%#v", testRun.LiveMetrics, testRun.History)
	}
	if _, exists := testRun.remoteMetricsBatchesByTarget["generator-a"]; exists {
		t.Fatal("evacuated target reappeared in metrics batches")
	}
}

func TestDistributedMetricsTerminalBatchRebasesAcrossUnrelatedTopologyChange(t *testing.T) {
	runner, testRun, backend := newDistributedThresholdTestRunner(t)
	const target = "generator-b"
	testRun.executionGeneration = 7
	testRun.remoteControlGeneration = 1
	stream := &generatorrpc.MetricsStream{}
	testRun.remoteMetricsStreamsByTarget = map[string]*generatorrpc.MetricsStream{target: stream}
	testRun.remoteClientsByTarget = map[string]generatorClient{target: nil}
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, target, distributedP99Batch(testRun.ID, 2_000, 900)) {
		t.Fatal("running snapshot was not accepted")
	}
	oldFence, current := captureDistributedMetricsConsumerFence(runner, testRun, target, stream, context.Background(), 7, true)
	if !current {
		t.Fatal("terminal stream fence was not initially current")
	}
	runner.mu.Lock()
	testRun.remoteControlGeneration++ // An unrelated target was evacuated.
	runner.mu.Unlock()

	terminal := generatorrpc.MetricsBatch{
		TestID:          testRun.ID,
		LifecycleState:  "ABORTED",
		LifecycleError:  "generator aborted work",
		TimestampUnixMs: 3_000,
	}
	if live, status, _, fresh, _ := backend.applyRemoteMetricsBatchWithFreshness(runner, testRun, target, terminal, false, oldFence); fresh || live != nil || status != "" {
		t.Fatalf("stale terminal fence applied: fresh=%v live=%#v status=%q", fresh, live, status)
	}
	runner.mu.RLock()
	finalizingBeforeRebase := testRun.remoteFinalizing
	targetStateBeforeRebase := testRun.remoteTargetStates[target]
	runner.mu.RUnlock()
	if finalizingBeforeRebase || targetStateBeforeRebase != "" {
		t.Fatalf("stale terminal fence mutated lifecycle: finalizing=%v target=%q", finalizingBeforeRebase, targetStateBeforeRebase)
	}
	if !distributedMetricsConsumerStillOwned(runner, testRun, target, oldFence, true) {
		t.Fatal("unrelated topology change retired the terminal stream")
	}

	rebasedFence, current := captureDistributedMetricsConsumerFence(runner, testRun, target, stream, context.Background(), 7, true)
	if !current || rebasedFence.topologyGeneration != 2 {
		t.Fatalf("terminal fence did not rebase: current=%v fence=%+v", current, rebasedFence)
	}
	live, status, finalError, fresh, publication := backend.applyRemoteMetricsBatchWithFreshness(runner, testRun, target, terminal, false, rebasedFence)
	if !fresh || status != "ABORTED" || finalError != terminal.LifecycleError {
		t.Fatalf("rebased terminal apply fresh=%v status=%q error=%q", fresh, status, finalError)
	}
	result := backend.publishDistributedMetricsRevision(context.Background(), runner, testRun, live, publication)
	if !result.published || !result.lifecycleCurrent {
		t.Fatalf("rebased terminal publication result = %+v", result)
	}
	if testRun.LiveMetrics == nil || testRun.LiveMetrics.TotalTx != 1 {
		t.Fatalf("rebased terminal publication lost running metrics: %#v", testRun.LiveMetrics)
	}
}

func TestDistributedMetricsTerminalBatchRepublishesAfterPostApplyTopologyRace(t *testing.T) {
	runner, testRun, backend := newDistributedThresholdTestRunner(t)
	const target = "generator-b"
	testRun.executionGeneration = 7
	testRun.remoteControlGeneration = 1
	stream := &generatorrpc.MetricsStream{}
	testRun.remoteMetricsStreamsByTarget = map[string]*generatorrpc.MetricsStream{target: stream}
	testRun.remoteClientsByTarget = map[string]generatorClient{target: nil}
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, target, distributedP99Batch(testRun.ID, 2_000, 900)) {
		t.Fatal("running snapshot was not accepted")
	}
	fence, current := captureDistributedMetricsConsumerFence(runner, testRun, target, stream, context.Background(), 7, true)
	if !current {
		t.Fatal("terminal stream fence was not initially current")
	}
	terminal := generatorrpc.MetricsBatch{
		TestID:          testRun.ID,
		LifecycleState:  "ABORTED",
		LifecycleError:  "generator aborted work",
		TimestampUnixMs: 3_000,
	}
	live, status, finalError, fresh, publication := backend.applyRemoteMetricsBatchWithFreshness(runner, testRun, target, terminal, false, fence)
	if !fresh || status != "ABORTED" || finalError != terminal.LifecycleError {
		t.Fatalf("initial terminal apply fresh=%v status=%q error=%q", fresh, status, finalError)
	}

	runner.mu.Lock()
	testRun.remoteControlGeneration++ // Topology changes after apply, before publication.
	runner.mu.Unlock()
	if result := backend.publishDistributedMetricsRevision(context.Background(), runner, testRun, live, publication); result.published || result.lifecycleCurrent {
		t.Fatalf("pre-topology terminal publication result = %+v, want rejected", result)
	}
	if !distributedMetricsConsumerStillOwned(runner, testRun, target, fence, true) {
		t.Fatal("post-apply topology race retired the still-owned terminal stream")
	}

	rebasedFence, current := captureDistributedMetricsConsumerFence(runner, testRun, target, stream, context.Background(), 7, true)
	if !current || rebasedFence.topologyGeneration != 2 {
		t.Fatalf("terminal fence did not rebase: current=%v fence=%+v", current, rebasedFence)
	}
	retryLive, retryStatus, retryError, retryFresh, retryPublication := backend.applyRemoteMetricsBatchWithFreshness(runner, testRun, target, terminal, false, rebasedFence)
	if !retryFresh {
		t.Fatal("same terminal batch was not admitted for topology-race republication")
	}
	if retryStatus != "" || retryError != "" {
		t.Fatalf("terminal retry rewrote saved outcome: status=%q error=%q", retryStatus, retryError)
	}
	if retryPublication.revision <= publication.revision || retryPublication.topologyGeneration != 2 {
		t.Fatalf("terminal retry publication = %+v, want newer revision at topology 2", retryPublication)
	}
	result := backend.publishDistributedMetricsRevision(context.Background(), runner, testRun, retryLive, retryPublication)
	if !result.published || !result.lifecycleCurrent {
		t.Fatalf("rebased terminal retry result = %+v, want published/current", result)
	}
	if testRun.LiveMetrics == nil || testRun.LiveMetrics.TotalTx != 1 {
		t.Fatalf("terminal retry lost the populated running snapshot: %#v", testRun.LiveMetrics)
	}
}

func TestApplyRemoteMetricsBatchAbortedPreservesPopulatedSnapshot(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	backend := &distributedExecutionBackend{}
	const target = "generator-a"
	testRun := &ManagedTest{
		ID:                    "distributed-aborted-terminal",
		Status:                "RUNNING",
		Groups:                []TestGroup{{Name: "buyers", VUsers: 1}},
		remoteClientsByTarget: map[string]generatorClient{target: nil},
	}

	running := distributedP99Batch(testRun.ID, 2_000, 900)
	live, status, _ := backend.applyRemoteMetricsBatch(runner, testRun, target, running)
	if status != "" || live == nil || live.TotalTx != 1 {
		t.Fatalf("running snapshot status=%q live=%#v, want one populated transaction", status, live)
	}

	aborted := generatorrpc.MetricsBatch{
		TestID:          testRun.ID,
		LifecycleState:  "ABORTED",
		LifecycleError:  "operator cancelled generator work",
		TimestampUnixMs: 3_000,
		Stats:           generatorrpc.GeneratorStats{ActiveVUsers: 0},
	}
	live, status, finalError := backend.applyRemoteMetricsBatch(runner, testRun, target, aborted)
	if status != "ABORTED" {
		t.Fatalf("ABORTED snapshot final status = %q, want ABORTED", status)
	}
	if finalError != aborted.LifecycleError {
		t.Fatalf("ABORTED snapshot final error = %q, want %q", finalError, aborted.LifecycleError)
	}
	if live == nil || live.TotalTx != 1 || live.Transactions["Checkout"] == nil || live.GroupMetrics["buyers"] == nil {
		t.Fatalf("ABORTED snapshot did not preserve populated RUNNING metrics: %#v", live)
	}
	runner.mu.RLock()
	targetState := testRun.remoteTargetStates[target]
	finalizing := testRun.remoteFinalizing
	storedLifecycle := testRun.remoteMetricsBatchesByTarget[target].LifecycleState
	runner.mu.RUnlock()
	if targetState != "ABORTED" || !finalizing || storedLifecycle != "ABORTED" {
		t.Fatalf("ABORTED lifecycle state=%q finalizing=%v stored=%q", targetState, finalizing, storedLifecycle)
	}
	if !isTerminalRemoteLifecycle("ABORTED") || !remoteTargetStateDone("ABORTED") {
		t.Fatal("ABORTED must be recognized as a terminal remote lifecycle")
	}
}
