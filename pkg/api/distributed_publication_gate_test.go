package api

import (
	"context"
	"testing"
	"time"
)

// The distributed gate publishes as soon as every connected generator has a
// new window, at least the minimum spacing after its previous publication.
// A window whose siblings are missing waits one short coalescing window; a
// window arriving too soon waits for the spacing. Only one flush is pending
// at a time, and a sibling completing the set publishes at once.
func TestDistributedPublicationGatePublishesOnceEveryGeneratorHasANewWindow(t *testing.T) {
	runner, testRun, backend := newDistributedThresholdTestRunner(t)
	testRun.remoteClientsByTarget = map[string]generatorClient{"generator-a": nil, "generator-b": nil}
	store := func(target string, timestampUnixMs int64) {
		t.Helper()
		if !backend.storeRemoteMetricsBatchOnly(runner, testRun, target, distributedP99Batch(testRun.ID, timestampUnixMs, 100)) {
			t.Fatalf("%s window at %d was not accepted", target, timestampUnixMs)
		}
	}
	now := time.Date(2026, time.October, 23, 12, 0, 0, 0, time.UTC)

	// The first window publishes at once.
	store("generator-a", 1_000)
	if publish, _, _ := backend.shouldPublishDistributedMetricsBatch(runner, testRun, now); !publish {
		t.Fatal("the first window was not published at once")
	}

	// A second later, one generator's window waits for its sibling...
	store("generator-a", 2_000)
	publish, delay, from := backend.shouldPublishDistributedMetricsBatch(runner, testRun, now.Add(time.Second))
	if publish || delay != distributedMetricsCoalescingWindow || !from.Equal(now) {
		t.Fatalf("a window without its sibling: publish %v, delay %s, from %s", publish, delay, from)
	}
	// ...and the sibling completes the set: published at once, superseding
	// the pending flush.
	store("generator-b", 2_000)
	if publish, _, _ := backend.shouldPublishDistributedMetricsBatch(runner, testRun, now.Add(time.Second+5*time.Millisecond)); !publish {
		t.Fatal("the window completing the set was not published at once")
	}
	if testRun.remoteMetricsFlushScheduled {
		t.Fatal("the superseded flush is still pending")
	}

	// Every generator has a new window, but too soon after the publication:
	// one flush waits for the minimum spacing.
	last := now.Add(time.Second + 5*time.Millisecond)
	store("generator-a", 2_500)
	store("generator-b", 2_500)
	publish, delay, _ = backend.shouldPublishDistributedMetricsBatch(runner, testRun, last.Add(200*time.Millisecond))
	if publish || delay != distributedMetricsMinimumSpacing-200*time.Millisecond {
		t.Fatalf("a set too soon after the publication: publish %v, delay %s", publish, delay)
	}
	if publish, delay, _ := backend.shouldPublishDistributedMetricsBatch(runner, testRun, last.Add(300*time.Millisecond)); publish || delay >= 0 {
		t.Fatalf("a second flush was scheduled: publish %v, delay %s", publish, delay)
	}
	// Once due, the complete set publishes.
	if publish, _, _ := backend.shouldPublishDistributedMetricsBatch(runner, testRun, last.Add(distributedMetricsMinimumSpacing)); !publish {
		t.Fatal("the complete set was not published once due")
	}
}

// Generators whose windows arrive out of phase (older generators on their own
// ticker, or a replacement started on a later barrier) complete a set only
// once a second: a partial set waits a full second after the last
// publication, so the gate never publishes faster than the declared
// one-second cadence.
func TestDistributedPublicationGateKeepsTheCadenceForOutOfPhaseGenerators(t *testing.T) {
	runner, testRun, backend := newDistributedThresholdTestRunner(t)
	testRun.remoteClientsByTarget = map[string]generatorClient{"generator-a": nil, "generator-b": nil}
	store := func(target string, timestampUnixMs int64) {
		t.Helper()
		if !backend.storeRemoteMetricsBatchOnly(runner, testRun, target, distributedP99Batch(testRun.ID, timestampUnixMs, 100)) {
			t.Fatalf("%s window at %d was not accepted", target, timestampUnixMs)
		}
	}
	now := time.Date(2026, time.October, 23, 12, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return now.Add(time.Duration(ms) * time.Millisecond) }
	store("generator-a", 1_000)
	if publish, _, _ := backend.shouldPublishDistributedMetricsBatch(runner, testRun, at(0)); !publish {
		t.Fatal("the first window was not published at once")
	}
	// generator-a publishes on the second, generator-b half a second later.
	for cycle := 0; cycle < 2; cycle++ {
		base := cycle * 1_000
		store("generator-b", int64(1_500+base))
		publish, delay, _ := backend.shouldPublishDistributedMetricsBatch(runner, testRun, at(500+base))
		if publish || delay != 500*time.Millisecond {
			t.Fatalf("cycle %d: a partial set half a second after the publication: publish %v, delay %s", cycle, publish, delay)
		}
		store("generator-a", int64(2_000+base))
		if publish, _, _ := backend.shouldPublishDistributedMetricsBatch(runner, testRun, at(1_000+base)); !publish {
			t.Fatalf("cycle %d: the set completed a second after the publication was not published", cycle)
		}
	}
	if got := testRun.remoteMetricsLastPublishedAt; !got.Equal(at(2_000)) {
		t.Fatalf("last publication at %s, want a publication every second", got.Sub(now))
	}
}

// A generator whose workload ended sends no further window, so it does not
// hold back a complete set; a flush that ends without publishing releases its
// reservation; and a partial set is published once overdue whatever flush is
// pending, so a lost flush never stalls publication.
func TestDistributedPublicationGateNeverStalls(t *testing.T) {
	runner, testRun, backend := newDistributedThresholdTestRunner(t)
	testRun.remoteClientsByTarget = map[string]generatorClient{"generator-a": nil, "generator-b": nil}
	store := func(target string, timestampUnixMs int64) {
		t.Helper()
		if !backend.storeRemoteMetricsBatchOnly(runner, testRun, target, distributedP99Batch(testRun.ID, timestampUnixMs, 100)) {
			t.Fatalf("%s window at %d was not accepted", target, timestampUnixMs)
		}
	}
	now := time.Date(2026, time.October, 23, 12, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return now.Add(time.Duration(ms) * time.Millisecond) }
	store("generator-a", 1_000)
	if publish, _, _ := backend.shouldPublishDistributedMetricsBatch(runner, testRun, at(0)); !publish {
		t.Fatal("the first window was not published at once")
	}

	// A flush whose consumer ended releases its reservation.
	store("generator-a", 2_000)
	publish, delay, from := backend.shouldPublishDistributedMetricsBatch(runner, testRun, at(1_000))
	if publish || delay < 0 || !testRun.remoteMetricsFlushScheduled {
		t.Fatalf("a partial set: publish %v, delay %s", publish, delay)
	}
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	backend.flushStoredDistributedMetricsAfterDelay(ended, runner, testRun, "generator-a", 0, from)
	if testRun.remoteMetricsFlushScheduled {
		t.Fatal("a flush whose consumer ended kept its reservation")
	}

	// A lost flush (its reservation never released) holds a partial set back
	// only until it is overdue.
	testRun.remoteMetricsFlushScheduled = true
	if publish, delay, _ := backend.shouldPublishDistributedMetricsBatch(runner, testRun, at(1_200)); publish || delay >= 0 {
		t.Fatalf("a partial set behind a pending flush: publish %v, delay %s", publish, delay)
	}
	if publish, _, _ := backend.shouldPublishDistributedMetricsBatch(runner, testRun, at(1_300)); !publish {
		t.Fatal("an overdue partial set was not published")
	}

	// generator-b's workload ended: generator-a's window alone completes the set.
	runner.mu.Lock()
	terminal := testRun.remoteMetricsBatchesByTarget["generator-a"]
	terminal.LifecycleState = "COMPLETE"
	testRun.remoteMetricsBatchesByTarget["generator-b"] = terminal
	runner.mu.Unlock()
	store("generator-a", 3_000)
	if publish, _, _ := backend.shouldPublishDistributedMetricsBatch(runner, testRun, at(2_100)); !publish {
		t.Fatal("a window was held back for a generator whose workload ended")
	}
}
