package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type browserEvidenceUnitProtocol struct {
	protocol.Protocol
	mu       sync.Mutex
	evidence *protocol.BrowserDocumentEvidence
}

func (p *browserEvidenceUnitProtocol) Name() string { return "browser" }
func (p *browserEvidenceUnitProtocol) BrowserDocumentEvidence() *protocol.BrowserDocumentEvidence {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.evidence.Clone()
}
func (p *browserEvidenceUnitProtocol) update(o protocol.BrowserDocumentObservation) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.evidence.Observations = []protocol.BrowserDocumentObservation{o}
}
func browserEngineTestObservation() protocol.BrowserDocumentObservation {
	gen := "0123456789abcdef0123456789abcdef"
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	yes, no := true, false
	value := 0.1
	cohort := &protocol.BrowserRuntimeCohort{Version: 1, BrowserVersion: "141.0", Platform: "linux", ViewportWidth: 1440, ViewportHeight: 900, Headless: &yes, Mobile: &no, Touch: &no, DeviceScaleFactor: 1, CPUThrottleRate: 1, ShapingApplied: true, CacheEnabled: &yes, CacheContentURLs: &yes, CheckNewerVersions: &no, NewUserPolicy: "persistent_context", NewUserPolicyApplied: true}
	return protocol.BrowserDocumentObservation{Version: protocol.BrowserEvidenceVersion, SourceGeneration: gen, DocumentID: gen + "-d1", LifecycleID: gen + "-l1", FrameID: gen + "-f1", NavigationID: gen + "-n1", MeasurementID: gen + "-d1/" + gen + "-l1/cls", Revision: 1, Metric: "cls", Algorithm: protocol.BrowserVitalsAlgorithm, AlgorithmSHA256: protocol.BrowserVitalsAlgorithmSHA256, Unit: "score", Engine: "chromium", Cohort: cohort, FrameScope: "main_frame", LifecyclePhase: "navigation", State: "provisional", Boundary: "progress", WindowStart: now, WindowEnd: now.Add(time.Second), CapturedAt: now.Add(time.Second), Available: true, Value: &value}
}
func browserEngineTestProvider(o protocol.BrowserDocumentObservation) *browserEvidenceUnitProtocol {
	return &browserEvidenceUnitProtocol{evidence: &protocol.BrowserDocumentEvidence{Version: protocol.BrowserEvidenceVersion, SourceGeneration: o.SourceGeneration, Observations: []protocol.BrowserDocumentObservation{o}}}
}
func TestBrowserDocumentsSeparateWindowsUpsertFinalAndLegacyPopulation(t *testing.T) {
	mc := NewMetricsCollector(4)
	o := browserEngineTestObservation()
	provider := browserEngineTestProvider(o)
	registration := mc.registerMessagingProgress(provider, "browser-group", nil)
	if registration == nil || registration.browser == nil {
		t.Fatal("standalone browser provider not registered")
	}
	if !mc.browserDocuments.sample(false) {
		t.Fatal("new provisional evidence not captured")
	}
	first := mc.browserDocuments.snapshot()
	if first == nil || first.Metrics[0].Provisional != 1 || first.Metrics[0].EligibleFinal != 0 || first.Metrics[0].Distribution.Count != 0 {
		t.Fatal("provisional manufactured final sample")
	}
	o.Revision = 2
	provider.update(o)
	mc.browserDocuments.sample(false)
	if second := mc.browserDocuments.snapshot(); second != nil {
		t.Fatalf("provisional revision recounted population: %#v", second)
	}
	o.Revision = 3
	o.State = "final"
	o.Boundary = "pagehide"
	o.Qualified = true
	provider.update(o)
	mc.browserDocuments.sample(false)
	last := mc.browserDocuments.snapshot()
	if last == nil || last.Metrics[0].EligibleFinal != 1 || last.Metrics[0].Distribution.Count != 1 || last.Metrics[0].Provisional != 0 {
		t.Fatal("final document did not enter independent distribution exactly once")
	}
	if mc.browserDocuments.sample(false) {
		t.Fatal("identical final replay changed ledger")
	}
	if mc.browserDocuments.snapshot() != nil {
		t.Fatal("identical final replay counted again")
	}
	var merged *protocol.BrowserDocumentSummary
	protocol.MergeBrowserDocumentSummaries(&merged, first)
	protocol.MergeBrowserDocumentSummaries(&merged, last)
	if merged.Metrics[0].Provisional != 1 || merged.Metrics[0].EligibleFinal != 1 || merged.Metrics[0].Distribution.Count != 1 {
		t.Fatal("merged terminal/first-sighting populations lost meaning")
	}
	snapshot := mc.computeSnapshot(nil, time.Second)
	if snapshot.Browser != nil || snapshot.Transactions != 0 || snapshot.Errors != 0 {
		t.Fatal("document evidence contaminated legacy action/transaction populations")
	}
}

type reentrantBrowserSink struct {
	collector *MetricsCollector
	records   int
	fail      bool
	mu        sync.Mutex
}

func (s *reentrantBrowserSink) RecordResult(*protocol.Result) error {
	return errors.New("document evidence must not enter action records")
}
func (s *reentrantBrowserSink) RecordBrowserDocumentEvidence(evidence *protocol.BrowserDocumentEvidence) error {
	s.collector.browserDocuments.sample(false)
	s.collector.LatestSnapshot()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records++
	if evidence == nil || len(evidence.Observations) != 1 {
		return errors.New("missing document evidence")
	}
	if s.fail {
		return errors.New("bounded fixture publication failure")
	}
	return nil
}
func TestBrowserDocumentsPublicationOutsideLocksAndNoFalseAck(t *testing.T) {
	mc := NewMetricsCollector(4)
	o := browserEngineTestObservation()
	o.State = "final"
	o.Boundary = "pagehide"
	o.Qualified = true
	provider := browserEngineTestProvider(o)
	entry := mc.registerBrowserDocuments(provider, "browser-group")
	sink := &reentrantBrowserSink{collector: mc, fail: true}
	entry.publish(sink, nil, "region")
	done := make(chan struct{})
	go func() { entry.publishFinal(provider.BrowserDocumentEvidence(), nil); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("owned reentrant sink deadlocked registration")
	}
	entry.mu.Lock()
	acknowledged := entry.publishedGeneration
	entry.mu.Unlock()
	if acknowledged != "" || !mc.browserDocuments.incomplete {
		t.Fatal("failed archive write acknowledged")
	}
	sink.fail = false
	entry.publishFinal(provider.BrowserDocumentEvidence(), nil)
	entry.publishFinal(provider.BrowserDocumentEvidence(), nil)
	if sink.records != 2 {
		t.Fatalf("success repeated or error not retryable: %d", sink.records)
	}
}
func TestBrowserDocumentsConcurrentCaptureFinishAndImmutableOutput(t *testing.T) {
	mc := NewMetricsCollector(4)
	o := browserEngineTestObservation()
	o.State = "final"
	o.Boundary = "pagehide"
	o.Qualified = true
	provider := browserEngineTestProvider(o)
	entry := mc.registerBrowserDocuments(provider, "")
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for n := 0; n < 20; n++ {
				entry.capture(false)
			}
		}()
	}
	workers.Add(1)
	go func() { defer workers.Done(); entry.finish(nil) }()
	workers.Wait()
	out := mc.browserDocuments.snapshot()
	if out == nil || out.Metrics[0].EligibleFinal != 1 {
		t.Fatal("concurrent final capture counted multiple samples")
	}
	out.Metrics[0].Cohort.BrowserVersion = "mutated"
	if provider.BrowserDocumentEvidence().Observations[0].Cohort.BrowserVersion != "141.0" {
		t.Fatal("output aliases runtime")
	}
}
func TestBrowserDocumentsTerminalMutationAndBoundsRemainIncomplete(t *testing.T) {
	mc := NewMetricsCollector(4)
	o := browserEngineTestObservation()
	o.State = "final"
	o.Boundary = "pagehide"
	o.Qualified = true
	provider := browserEngineTestProvider(o)
	entry := mc.registerBrowserDocuments(provider, "")
	entry.capture(false)
	mc.browserDocuments.snapshot()
	o.Revision++
	v := 0.5
	o.Value = &v
	provider.update(o)
	entry.capture(false)
	if !mc.browserDocuments.incomplete {
		t.Fatal("mutated final replay appeared healthy")
	}
	mc.browserDocuments.seen = make(map[string]browserSeenMeasurement, maxBrowserSeenMeasurements)
	for i := 0; i < maxBrowserSeenMeasurements; i++ {
		mc.browserDocuments.seen[string(rune(i))] = browserSeenMeasurement{}
	}
	o.MeasurementID = o.DocumentID + "/" + o.LifecycleID + "/cls"
	entry.capture(false)
	if !mc.browserDocuments.incomplete || mc.browserDocuments.dropped != 0 {
		t.Fatal("bounded admission lost unknown coverage or invented a distinct loss count")
	}
}

type browserDisclosureSink struct {
	evidence *protocol.BrowserDocumentEvidence
}

func (s *browserDisclosureSink) RecordResult(*protocol.Result) error {
	return errors.New("document evidence must never become an action")
}
func (s *browserDisclosureSink) RecordBrowserDocumentEvidence(e *protocol.BrowserDocumentEvidence) error {
	s.evidence = e.Clone()
	return nil
}

func TestBrowserDocumentsLiveAndFinalUseCurrentRuntimeSecretMatcher(t *testing.T) {
	const secret = "owned-runtime-sensitive-value"
	config, err := WithSecretValueRedactionChecked(nil, []string{secret})
	if err != nil {
		t.Fatal(err)
	}
	mc := NewMetricsCollector(4)
	o := browserEngineTestObservation()
	o.State, o.Boundary, o.Qualified = "final", "pagehide", true
	o.DeviceProfile = secret
	o.Cohort.BrowserVersion = "version-" + secret
	provider := browserEngineTestProvider(o)
	registration := mc.registerMessagingProgressWithRedaction(provider, "group-"+secret, nil, func() *RedactionConfig { return config })
	if registration == nil || registration.browser == nil {
		t.Fatal("browser registration missing")
	}
	// Even a publication racing immediately after registration is protected.
	registration.browser.capture(false)
	initial := mc.browserDocuments.snapshot()
	assertSafe := func(value any) {
		t.Helper()
		encoded, err := json.Marshal(value)
		if err != nil || strings.Contains(string(encoded), secret) {
			t.Fatal("runtime secret escaped document disclosure boundary")
		}
	}
	assertSafe(initial)
	if initial == nil || !initial.Incomplete || initial.Metrics[0].UnqualifiedFinal != 1 || initial.Metrics[0].EligibleFinal != 0 || initial.Metrics[0].ExcludedReasons["cohort_unavailable"] != 1 || initial.Metrics[0].Distribution.Count != 0 {
		t.Fatal("redacted identity remained qualified")
	}
	// The later immutable per-VUser getter must replace the initial matcher.
	newSecret := "later-owned-sensitive-value"
	updated, err := WithSecretValueRedactionChecked(config, []string{newSecret})
	if err != nil {
		t.Fatal(err)
	}
	o.DocumentID = o.SourceGeneration + "-d2"
	o.MeasurementID = o.DocumentID + "/" + o.LifecycleID + "/cls"
	o.DeviceProfile = newSecret
	provider.update(o)
	vu := &VUser{Proto: provider, Metrics: mc, RedactionConfig: updated}
	vu.bindRuntimeTextRedactor()
	registration.browser.capture(false)
	live := mc.browserDocuments.snapshot()
	encoded, _ := json.Marshal(live)
	if strings.Contains(string(encoded), newSecret) {
		t.Fatal("resolved per-VUser matcher was not used")
	}
	assertSafe(live)
	sink := &browserDisclosureSink{}
	registration.browser.publish(sink, func() *RedactionConfig { return vu.runtimeRedaction.Load() }, "region-"+newSecret)
	registration.browser.publishFinal(provider.BrowserDocumentEvidence(), updated)
	assertSafe(sink.evidence)
	encoded, _ = json.Marshal(sink.evidence)
	if strings.Contains(string(encoded), newSecret) || sink.evidence == nil || !sink.evidence.Incomplete || sink.evidence.Observations[0].Qualified {
		t.Fatal("typed sink escaped or certified a redacted selector")
	}
	original := provider.BrowserDocumentEvidence()
	if original.Observations[0].DeviceProfile != newSecret || original.Observations[0].Cohort.BrowserVersion != o.Cohort.BrowserVersion || !original.Observations[0].Qualified || original.Incomplete {
		t.Fatal("disclosure projection mutated private provider evidence")
	}
}

func TestBrowserDocumentLossUnitsPublishWithoutActions(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		observations, requests int64
		incomplete             bool
	}{
		{"request_only", 0, 5, false},
		{"request_only_flagged", 0, 5, true},
		{"observation_only", 3, 0, false},
		{"distinct_losses", 3, 5, false},
	} {
		for _, mode := range []string{"idle", "stop"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				mc := NewMetricsCollector(4)
				provider := browserEngineTestProvider(browserEngineTestObservation())
				provider.evidence.Observations = nil
				provider.evidence.IssuedRequests = 7
				provider.evidence.DroppedRequestIdentities = tc.requests
				provider.evidence.DroppedObservations = tc.observations
				provider.evidence.Incomplete = tc.incomplete
				if provider.BrowserDocumentEvidence() == nil || mc.registerBrowserDocuments(provider, "") == nil {
					t.Fatal("valid empty evidence was not registered")
				}
				defer mc.Stop()
				var snapshot MetricsSnapshot
				if mode == "idle" {
					mc.Start(5 * time.Millisecond)
					snapshot = waitForMetricsSnapshot(t, mc, func(s MetricsSnapshot) bool { return s.BrowserDocuments != nil })
				} else {
					mc.Stop()
					var ok bool
					snapshot, ok = mc.LatestSnapshot()
					if !ok {
						t.Fatal("loss-only Stop did not publish coverage")
					}
				}
				summary := snapshot.BrowserDocuments
				if summary == nil || !summary.Incomplete || summary.DroppedObservations != tc.observations || len(summary.Metrics) != 0 || snapshot.Transactions != 0 || snapshot.Browser != nil {
					t.Fatalf("distinct loss units or coverage lost: %+v", summary)
				}
				if raw := provider.BrowserDocumentEvidence(); raw.DroppedRequestIdentities != tc.requests || raw.DroppedObservations != tc.observations || raw.Incomplete != tc.incomplete {
					t.Fatal("summary changed immutable source evidence")
				}
				if mode == "idle" && mc.browserDocuments.sample(false) {
					t.Fatal("unchanged incomplete coverage repeated idle publication")
				}
			})
		}
	}
}

func TestBrowserDocumentProviderCapacityPublishesUnknownCoverage(t *testing.T) {
	for _, mode := range []string{"idle", "stop"} {
		t.Run(mode, func(t *testing.T) {
			mc := NewMetricsCollector(4)
			defer mc.Stop()
			for i := 0; i <= maxBrowserEvidenceProviders; i++ {
				provider := browserEngineTestProvider(browserEngineTestObservation())
				provider.evidence.Observations = nil
				entry := mc.registerBrowserDocuments(provider, "")
				if (entry != nil) != (i < maxBrowserEvidenceProviders) {
					t.Fatal("provider capacity admission changed")
				}
			}
			var snapshot MetricsSnapshot
			if mode == "idle" {
				mc.Start(5 * time.Millisecond)
				snapshot = waitForMetricsSnapshot(t, mc, func(s MetricsSnapshot) bool { return s.BrowserDocuments != nil })
			} else {
				mc.Stop()
				var ok bool
				snapshot, ok = mc.LatestSnapshot()
				if !ok {
					t.Fatal("unknown provider coverage was omitted at Stop")
				}
			}
			if summary := snapshot.BrowserDocuments; summary == nil || !summary.Incomplete || summary.DroppedObservations != 0 || len(summary.Metrics) != 0 {
				t.Fatalf("unknown provider population became counted observations: %+v", summary)
			}
			if mode == "idle" && mc.browserDocuments.sample(false) {
				t.Fatal("provider capacity repeated unchanged idle coverage")
			}
		})
	}
}

func TestBrowserDocumentObservationLossDeltasIgnoreRequestLoss(t *testing.T) {
	mc := NewMetricsCollector(4)
	provider := browserEngineTestProvider(browserEngineTestObservation())
	provider.evidence.Observations = nil
	provider.evidence.IssuedRequests, provider.evidence.DroppedRequestIdentities = 10, 9
	entry := mc.registerBrowserDocuments(provider, "")
	for _, step := range []struct {
		generation    string
		dropped, want int64
	}{
		{"0123456789abcdef0123456789abcdef", 3, 3},
		{"0123456789abcdef0123456789abcdef", 3, 0},
		{"0123456789abcdef0123456789abcdef", 5, 2},
		{"fedcba9876543210fedcba9876543210", 1, 1},
	} {
		provider.evidence.SourceGeneration, provider.evidence.DroppedObservations = step.generation, step.dropped
		entry.capture(false)
		summary := mc.browserDocuments.snapshot()
		if summary == nil || !summary.Incomplete || summary.DroppedObservations != step.want {
			t.Fatalf("source delta=%+v, want %d", summary, step.want)
		}
		if mc.browserDocuments.sample(false) {
			t.Fatal("snapshot did not acknowledge unchanged coverage")
		}
	}
}

type unavailableBrowserDocumentProvider struct {
	protocol.Protocol
	failure string
}

func (p *unavailableBrowserDocumentProvider) BrowserDocumentEvidence() *protocol.BrowserDocumentEvidence {
	if p.failure == "panic" {
		panic("bounded fixture provider failure")
	}
	if p.failure == "nil" {
		return nil
	}
	evidence := browserEngineTestProvider(browserEngineTestObservation()).evidence
	evidence.Version = 99
	return evidence
}
func TestBrowserDocumentUnavailableProviderPublishesCoverageOnce(t *testing.T) {
	for _, failure := range []string{"nil", "panic", "malformed"} {
		for _, mode := range []string{"idle", "stop"} {
			t.Run(failure+"/"+mode, func(t *testing.T) {
				mc := NewMetricsCollector(4)
				defer mc.Stop()
				mc.registerBrowserDocuments(&unavailableBrowserDocumentProvider{failure: failure}, "")
				var snapshot MetricsSnapshot
				if mode == "idle" {
					mc.Start(5 * time.Millisecond)
					snapshot = waitForMetricsSnapshot(t, mc, func(s MetricsSnapshot) bool { return s.BrowserDocuments != nil })
					if mc.browserDocuments.sample(false) {
						t.Fatal("acknowledged unavailable provider repeated idle coverage")
					}
					mc.Stop()
					mc.snapshotsMu.RLock()
					count := len(mc.snapshots)
					mc.snapshotsMu.RUnlock()
					if count != 1 {
						t.Fatalf("unchanged unavailable provider emitted %d snapshots, want 1", count)
					}
				} else {
					mc.Stop()
					var ok bool
					snapshot, ok = mc.LatestSnapshot()
					if !ok {
						t.Fatal("unavailable provider did not publish Stop coverage")
					}
				}
				if summary := snapshot.BrowserDocuments; summary == nil || !summary.Incomplete || summary.DroppedObservations != 0 || len(summary.Metrics) != 0 {
					t.Fatalf("unavailable provider invented a population: %+v", summary)
				}
			})
		}
	}
}
func TestBrowserDocumentMeasurementCapacityRetriesDoNotInventLoss(t *testing.T) {
	mc := NewMetricsCollector(4)
	provider := browserEngineTestProvider(browserEngineTestObservation())
	mc.registerBrowserDocuments(provider, "")
	mc.browserDocuments.seen = make(map[string]browserSeenMeasurement, maxBrowserSeenMeasurements)
	for i := 0; i < maxBrowserSeenMeasurements; i++ {
		mc.browserDocuments.seen[string(rune(i))] = browserSeenMeasurement{}
	}
	if !mc.browserDocuments.sample(false) {
		t.Fatal("first unknown admission loss was not published")
	}
	first := mc.browserDocuments.snapshot()
	if first == nil || !first.Incomplete || first.DroppedObservations != 0 || len(first.Metrics) != 0 {
		t.Fatalf("unknown distinct admission loss was counted: %+v", first)
	}
	for i := 0; i < 3; i++ {
		if mc.browserDocuments.sample(false) {
			t.Fatal("same rejected measurement repeated coverage publication")
		}
		if summary := mc.browserDocuments.snapshot(); summary == nil || !summary.Incomplete || summary.DroppedObservations != 0 || len(summary.Metrics) != 0 {
			t.Fatalf("rejected retry invented loss/population: %+v", summary)
		}
	}
}

func TestBrowserDocumentFinishedProviderPublishesPendingTerminalAtStop(t *testing.T) {
	mc := NewMetricsCollector(4)
	observation := browserEngineTestObservation()
	observation.State, observation.Boundary, observation.Qualified = "final", "graceful_end", true
	provider := browserEngineTestProvider(observation)
	entry := mc.registerBrowserDocuments(provider, "")
	entry.finish(nil)
	mc.Stop()
	snapshot, ok := mc.LatestSnapshot()
	if !ok || snapshot.BrowserDocuments == nil || len(snapshot.BrowserDocuments.Metrics) != 1 || snapshot.BrowserDocuments.Metrics[0].EligibleFinal != 1 || snapshot.BrowserDocuments.Metrics[0].Distribution.Count != 1 || snapshot.Transactions != 0 || snapshot.Browser != nil {
		t.Fatal("finished provider's final document window was not published at Stop")
	}
	if mc.browserDocuments.sample(true) || mc.browserDocuments.snapshot() != nil {
		t.Fatal("acknowledged terminal window was published again")
	}
}

type slowBrowserDocumentTeardownProvider struct {
	*browserEvidenceUnitProtocol
	entered, release chan struct{}
	final            protocol.BrowserDocumentObservation
}

func (p *slowBrowserDocumentTeardownProvider) Teardown(ctx context.Context) error {
	close(p.entered)
	select {
	case <-p.release:
		p.update(p.final)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestBrowserDocumentFinishedProviderPublishesAfterZeroGaugeAck(t *testing.T) {
	for _, mode := range []string{"idle", "stop"} {
		t.Run(mode, func(t *testing.T) {
			mc := NewMetricsCollector(4)
			defer mc.Stop()
			provisional := browserEngineTestObservation()
			final := provisional
			final.Revision, final.State, final.Boundary, final.Qualified = 2, "final", "graceful_end", true
			provider := &slowBrowserDocumentTeardownProvider{browserEvidenceUnitProtocol: browserEngineTestProvider(provisional), entered: make(chan struct{}), release: make(chan struct{}), final: final}
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(provider.release) })
			entry := mc.registerMessagingProgress(provider, "", nil)
			mc.SetActiveVUsers(1)
			mc.Start(5 * time.Millisecond)
			initial := waitForMetricsSnapshot(t, mc, func(s MetricsSnapshot) bool {
				return s.ActiveVUsers == 1 && s.BrowserDocuments != nil && len(s.BrowserDocuments.Metrics) == 1 && s.BrowserDocuments.Metrics[0].Provisional == 1
			})
			// The real VUser lifecycle decrements the gauge before slow teardown.
			mc.SetActiveVUsers(0)
			done := make(chan error, 1)
			joined := false
			go func() { done <- runProtocolTeardownWithMessagingProgress(provider, entry) }()
			defer func() {
				releaseOnce.Do(func() { close(provider.release) })
				if !joined {
					select {
					case err := <-done:
						if err != nil {
							t.Errorf("controlled teardown cleanup: %v", err)
						}
					case <-time.After(5 * time.Second):
						t.Error("controlled teardown cleanup did not join")
					}
				}
			}()
			select {
			case <-provider.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("controlled teardown did not begin")
			}
			waitForMetricsSnapshot(t, mc, func(s MetricsSnapshot) bool {
				return s.ActiveVUsers == 0 && s.Timestamp.After(initial.Timestamp) && !mc.activeGaugeDirty.Load()
			})
			releaseOnce.Do(func() { close(provider.release) })
			select {
			case err := <-done:
				joined = true
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("controlled teardown did not finish")
			}
			if mode == "idle" {
				waitForMetricsSnapshot(t, mc, func(s MetricsSnapshot) bool {
					return s.BrowserDocuments != nil && len(s.BrowserDocuments.Metrics) == 1 && s.BrowserDocuments.Metrics[0].EligibleFinal == 1
				})
			}
			mc.Stop()
			var provisionalCount, finalCount, distributionCount int64
			mc.snapshotsMu.RLock()
			for _, snapshot := range mc.snapshots {
				if snapshot.BrowserDocuments == nil {
					continue
				}
				for _, metric := range snapshot.BrowserDocuments.Metrics {
					provisionalCount += metric.Provisional
					finalCount += metric.EligibleFinal
					distributionCount += metric.Distribution.Count
				}
			}
			mc.snapshotsMu.RUnlock()
			if provisionalCount != 1 || finalCount != 1 || distributionCount != 1 {
				t.Fatalf("terminal after acknowledged zero lost or repeated: provisional=%d final=%d distribution=%d", provisionalCount, finalCount, distributionCount)
			}
			if mc.browserDocuments.sample(true) || mc.browserDocuments.snapshot() != nil {
				t.Fatal("finished provider retained repeated terminal publication")
			}
		})
	}
}
