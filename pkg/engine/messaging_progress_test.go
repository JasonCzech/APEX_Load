package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type localMessagingProgressProtocol struct {
	mu                sync.Mutex
	name              string
	progress          *protocol.MessagingProgressSnapshot
	panicName         bool
	panicRead         bool
	teardownErr       error
	teardownLifecycle string
	reads             atomic.Int64
	blockAfter        int64
	readEntered       chan struct{}
	readRelease       chan struct{}
	readOnce          sync.Once
}

func (p *localMessagingProgressProtocol) Name() string {
	if p.panicName {
		panic("fixture metadata panic")
	}
	if p.name != "" {
		return p.name
	}
	return "engine_progress_fixture"
}
func (p *localMessagingProgressProtocol) Init(context.Context, map[string]any) error { return nil }
func (p *localMessagingProgressProtocol) HealthCheck(context.Context) error          { return nil }
func (p *localMessagingProgressProtocol) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{Success: true}, nil
}
func (p *localMessagingProgressProtocol) Teardown(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.progress != nil && p.teardownLifecycle != "" {
		p.progress.Lifecycle = p.teardownLifecycle
	}
	return p.teardownErr
}
func (p *localMessagingProgressProtocol) MessagingProgressSnapshot() *protocol.MessagingProgressSnapshot {
	read := p.reads.Add(1)
	if p.readRelease != nil && read > p.blockAfter {
		p.readOnce.Do(func() { close(p.readEntered) })
		<-p.readRelease
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.panicRead {
		panic("fixture panic")
	}
	// Do not clone malformed test input here: ReadMessagingProgress owns its
	// validation and must turn invalid source evidence into an explicit marker.
	if p.progress == nil {
		return nil
	}
	out := *p.progress
	out.Rows = append([]protocol.MessagingProgressRow{}, p.progress.Rows...)
	return &out
}
func engineProgressFixture(session string) *protocol.MessagingProgressSnapshot {
	now := time.Now().UTC()
	age := int64(0)
	return &protocol.MessagingProgressSnapshot{Version: protocol.MessagingProgressVersion, CapturedAt: now, Lifecycle: "running", Rows: []protocol.MessagingProgressRow{{Origin: "forged-origin", Group: "forged-group", Protocol: "eventhubs", Mode: "processor", SourceID: "source", Source: "hub", Subscription: "sub", SessionID: session, Partition: "0", Generation: "generation", Revision: "1", CaptureRevision: "1", CapturedAt: now, UpdatedAt: now, ExpectedPartitions: 1, InventoryComplete: true, Ownership: "owned", OwnershipVerifiedAt: now, OwnershipVerifiedAgeMs: &age, Acquired: 1, Delivered: 2, DeliveredPosition: protocol.MessagingProgressPosition{State: "missing"}, CheckpointPosition: protocol.MessagingProgressPosition{State: "missing"}, TailPosition: protocol.MessagingProgressPosition{State: "disabled"}, Checkpoint: protocol.MessagingCheckpointProgress{Duration: protocol.MessagingDurationSummary{Metric: "checkpoint", Population: "checkpoint_operation"}}}}}
}

func waitForMessagingProgressSnapshot(t *testing.T, mc *MetricsCollector, match func(MetricsSnapshot) bool) MetricsSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if snapshot, ok := mc.LatestSnapshot(); ok && match(snapshot) {
			return snapshot
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("required lifecycle progress snapshot was not published")
	return MetricsSnapshot{}
}

func TestMessagingProgressIdleCountZeroAndTrustedGroup(t *testing.T) {
	mc := NewMetricsCollector(8)
	mc.resCollector = nil
	p := &localMessagingProgressProtocol{progress: engineProgressFixture("idle")}
	entry := mc.registerMessagingProgress(p, "trusted", nil)
	if entry == nil {
		t.Fatal("provider not registered")
	}
	mc.Start(5 * time.Millisecond)
	defer mc.Stop()
	s := waitForMessagingProgressSnapshot(t, mc, func(s MetricsSnapshot) bool { return s.MessagingProgress != nil && len(s.MessagingProgress.Rows) == 1 })
	if s.Transactions != 0 || s.Errors != 0 || len(s.ByAction) != 0 || len(s.ByTransaction) != 0 || mc.TotalTransactions() != 0 || mc.TotalErrors() != 0 {
		t.Fatalf("lifecycle evidence changed action populations: %+v", s)
	}
	if r := s.MessagingProgress.Rows[0]; r.Group != "trusted" || r.Origin != "" || r.Delivered != 2 {
		t.Fatalf("untrusted attribution or counts: %+v", r)
	}
	if !s.MessagingProgress.Valid() {
		t.Fatal("invalid output")
	}
	// Returned snapshots cannot mutate retained provider evidence or later reads.
	s.MessagingProgress.Rows[0].Delivered = 99
	*s.MessagingProgress.Rows[0].OwnershipVerifiedAgeMs = 99
	latest, _ := mc.LatestSnapshot()
	if latest.MessagingProgress.Rows[0].Delivered != 2 || *latest.MessagingProgress.Rows[0].OwnershipVerifiedAgeMs != 0 {
		t.Fatal("progress getter exposed retained state")
	}
}

func TestMessagingProgressMalformedAdapterMetadata(t *testing.T) {
	var typedNil *localMessagingProgressProtocol
	for _, tc := range []struct {
		name string
		p    protocol.Protocol
	}{
		{name: "panicking-name", p: &localMessagingProgressProtocol{panicName: true, progress: engineProgressFixture("unsafe-name")}},
		{name: "typed-nil", p: typedNil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mc := NewMetricsCollector(8)
			mc.resCollector = nil
			entry := mc.registerMessagingProgress(tc.p, "trusted", nil)
			if entry == nil || entry.provider != nil {
				t.Fatal("malformed metadata must retain bounded coverage without an unsafe provider")
			}
			mc.Stop()
			s, ok := mc.LatestSnapshot()
			if !ok || s.MessagingProgress == nil || !s.MessagingProgress.Valid() || !s.MessagingProgress.Incomplete || s.MessagingProgress.UnavailableReason != "malformed" {
				t.Fatalf("malformed adapter coverage lost: %+v", s.MessagingProgress)
			}
			if len(mc.messagingProgress.entries) != 0 || s.Transactions != 0 || s.Errors != 0 || len(s.ByAction) != 0 {
				t.Fatal("malformed metadata retained references or changed action populations")
			}
			if p, ok := tc.p.(*localMessagingProgressProtocol); ok && p != nil && p.reads.Load() != 0 {
				t.Fatal("malformed adapter received an asynchronous provider read")
			}
		})
	}
}

func TestMessagingProgressTrustedGroupPreservesIdentity(t *testing.T) {
	mc := NewMetricsCollector(8)
	mc.registerMessagingProgress(&localMessagingProgressProtocol{progress: engineProgressFixture("group-spaces")}, " trusted ", nil)
	mc.messagingProgress.sample(false)
	if got := mc.messagingProgress.snapshot().Rows[0].Group; got != " trusted " {
		t.Fatalf("trusted group identity rewritten: %q", got)
	}
}

func TestMessagingProgressRevisionsReplaysAndFreshness(t *testing.T) {
	mc := NewMetricsCollector(8)
	p := &localMessagingProgressProtocol{progress: engineProgressFixture("revision")}
	mc.registerMessagingProgress(p, "group", nil)
	mc.messagingProgress.sample(false)
	p.mu.Lock()
	p.progress.Rows[0].Delivered = 4
	p.progress.Rows[0].Revision = "2"
	p.progress.Rows[0].CaptureRevision = "2"
	p.progress.CapturedAt = p.progress.CapturedAt.Add(time.Second)
	p.mu.Unlock()
	mc.messagingProgress.sample(false)
	mc.messagingProgress.sample(false)
	if got := mc.messagingProgress.snapshot().Rows[0].Delivered; got != 4 {
		t.Fatalf("replayed cumulative progress summed: %d", got)
	}
	p.mu.Lock()
	p.progress.Rows[0].Delivered = 2
	p.progress.Rows[0].Revision = "1"
	p.progress.Rows[0].CaptureRevision = "1"
	p.progress.CapturedAt = p.progress.CapturedAt.Add(-time.Second)
	p.mu.Unlock()
	before := mc.messagingProgress.snapshot()
	mc.messagingProgress.sample(false)
	after := mc.messagingProgress.snapshot()
	if after.Rows[0].Delivered != 4 || !after.CapturedAt.Equal(before.CapturedAt) {
		t.Fatal("older revision changed source evidence or capture time")
	}
	p.mu.Lock()
	p.progress.Rows[0].Delivered = 4
	p.progress.Rows[0].Revision = "2"
	p.progress.Rows[0].CaptureRevision = "3"
	*p.progress.Rows[0].OwnershipVerifiedAgeMs = 42
	p.mu.Unlock()
	mc.messagingProgress.sample(false)
	if got := *mc.messagingProgress.snapshot().Rows[0].OwnershipVerifiedAgeMs; got != 42 {
		t.Fatalf("fresh capture age=%d", got)
	}
	p.mu.Lock()
	p.progress.Rows[0].Delivered = 1
	p.progress.Rows[0].Revision = "3"
	p.progress.Rows[0].CaptureRevision = "4"
	p.mu.Unlock()
	mc.messagingProgress.sample(false)
	after = mc.messagingProgress.snapshot()
	if after.Rows[0].Delivered != 4 || !after.Incomplete || !after.Rows[0].Incomplete {
		t.Fatal("regressing cumulative evidence accepted")
	}
}

func TestMessagingProgressTeardownDrainAndFinalCoverage(t *testing.T) {
	for _, terminal := range []string{"released", "quarantined", "draining"} {
		t.Run(terminal, func(t *testing.T) {
			mc := NewMetricsCollector(8)
			mc.resCollector = nil
			p := &localMessagingProgressProtocol{progress: engineProgressFixture(terminal), teardownErr: errors.New("cleanup incomplete"), teardownLifecycle: "draining"}
			entry := mc.registerMessagingProgress(p, "group", nil)
			if err := runProtocolTeardownWithMessagingProgress(p, entry); err == nil {
				t.Fatal("fixture teardown error disappeared")
			}
			if len(mc.messagingProgress.entries) != 1 || entry.latest.Lifecycle != "draining" {
				t.Fatal("draining reporter retired before close proof")
			}
			p.mu.Lock()
			p.progress.Lifecycle = terminal
			p.progress.Incomplete = terminal == "quarantined"
			p.mu.Unlock()
			mc.Start(5 * time.Millisecond)
			waitForMessagingProgressSnapshot(t, mc, func(s MetricsSnapshot) bool {
				return s.MessagingProgress != nil && s.MessagingProgress.Lifecycle == terminal
			})
			mc.Stop()
			latest, _ := mc.LatestSnapshot()
			if latest.Transactions != 0 || latest.Errors != 0 {
				t.Fatal("cleanup generated a synthetic result")
			}
			if !latest.MessagingProgress.Valid() || (terminal != "released" && !latest.MessagingProgress.Incomplete) {
				t.Fatal("unproved terminal cleanup became complete")
			}
			if len(mc.messagingProgress.entries) != 0 || entry.provider != nil {
				t.Fatal("final sampler retained protocol reference")
			}
			if terminal == "released" && latest.MessagingProgress.Incomplete {
				t.Fatal("proved cleanup marked incomplete")
			}
		})
	}
}

func TestMessagingProgressFinalPendingAndMalformedSources(t *testing.T) {
	for _, kind := range []string{"pending", "malformed", "panic", "missing"} {
		t.Run(kind, func(t *testing.T) {
			mc := NewMetricsCollector(8)
			mc.resCollector = nil
			p := &localMessagingProgressProtocol{progress: engineProgressFixture(kind)}
			entry := mc.registerMessagingProgress(p, "group", nil)
			p.mu.Lock()
			switch kind {
			case "pending":
				p.progress = &protocol.MessagingProgressSnapshot{Version: 1, CapturedAt: time.Now().UTC(), Lifecycle: "pending", Rows: []protocol.MessagingProgressRow{}}
			case "malformed":
				p.progress.Version = 99
			case "panic":
				p.panicRead = true
			case "missing":
				p.progress = nil
			}
			p.mu.Unlock()
			mc.Start(time.Hour)
			mc.Stop() // final publication cannot depend on a tick
			latest, ok := mc.LatestSnapshot()
			if !ok || latest.MessagingProgress == nil || !latest.MessagingProgress.Incomplete || !latest.MessagingProgress.Valid() || entry.provider != nil {
				t.Fatalf("missing explicit final coverage: %+v", latest.MessagingProgress)
			}
			if kind == "malformed" || kind == "panic" {
				if latest.MessagingProgress.UnavailableReason != "malformed" {
					t.Fatal("malformed source became available")
				}
			}
		})
	}
}

func TestMessagingProgressBoundedReportersAndReleasedReplacement(t *testing.T) {
	mc := NewMetricsCollector(8)
	for i := 0; i < protocol.MaxMessagingProgressRows; i++ {
		p := &localMessagingProgressProtocol{progress: engineProgressFixture(strconv.Itoa(i)), teardownLifecycle: "released"}
		entry := mc.registerMessagingProgress(p, "group", nil)
		if entry == nil {
			t.Fatal("early bound")
		}
		entry.finish() /* source stillrunning: cannot retire until released */
	}
	if mc.registerMessagingProgress(&localMessagingProgressProtocol{progress: engineProgressFixture("overflow")}, "group", nil) != nil {
		t.Fatal("unbounded provider references")
	}
	mc.messagingProgress.sample(false)
	s := mc.messagingProgress.snapshot()
	if !s.Incomplete || s.DroppedRows != 1 || len(s.Rows) > 64 {
		t.Fatal("overflow disappeared")
	}
	for _, entry := range mc.messagingProgress.entries {
		entry.mu.Lock()
		p := entry.provider.(*localMessagingProgressProtocol)
		entry.mu.Unlock()
		p.mu.Lock()
		p.progress.Lifecycle = "released"
		p.progress.CapturedAt = time.Now().UTC()
		for i := range p.progress.Rows {
			p.progress.Rows[i].Ownership = "released"
			p.progress.Rows[i].Revision = "2"
			p.progress.Rows[i].CaptureRevision = "2"
			p.progress.Rows[i].CapturedAt = p.progress.CapturedAt
		}
		p.mu.Unlock()
	}
	mc.messagingProgress.sample(false)
	if len(mc.messagingProgress.entries) != 0 {
		t.Fatal("released reporters retained")
	}
	if mc.registerMessagingProgress(&localMessagingProgressProtocol{progress: engineProgressFixture("replacement")}, "group", nil) == nil {
		t.Fatal("terminal evidence permanently exhausted provider slots")
	}
	mc.messagingProgress.sample(false)
	s = mc.messagingProgress.snapshot()
	if !s.Valid() || len(s.Rows) > 64 || !s.Incomplete {
		t.Fatal("replacement lost bounded completeness")
	}
	if s.Lifecycle == "released" {
		t.Fatal("active reporter hidden behind retired lifecycle")
	}
	found := false
	for _, row := range s.Rows {
		found = found || row.SessionID == "replacement"
	}
	if !found {
		t.Fatal("retired history displaced current owned progress")
	}

}

func TestMessagingProgressProducerOnlyDoesNotRegister(t *testing.T) {
	script := processorResourceScript()
	script.Actions = []dsl.Action{{Type: "eventhubs_produce"}}
	resources, err := newEngineRuntimeResources([]GroupConfig{{Name: "producer", Script: script, VUsers: 1}})
	if err != nil {
		t.Fatal(err)
	}
	mc := NewMetricsCollector(8)
	p := &localMessagingProgressProtocol{name: "eventhubs", progress: engineProgressFixture("producer")}
	if mc.registerMessagingProgress(p, "producer", resources) != nil || p.reads.Load() != 0 {
		t.Fatal("producer-only processor configuration created a reporter")
	}
}

func TestMessagingProgressRetainedByteAccounting(t *testing.T) {
	plain := MetricsSnapshot{}
	s := engineProgressFixture(strings.Repeat("session", 30))
	s.Rows[0].Checkpoint.Duration.Unavailable = map[string]int64{"missing": 0}
	bytes := estimateMetricsSnapshotBytes(MetricsSnapshot{MessagingProgress: s}) - estimateMetricsSnapshotBytes(plain)
	minimum := int(unsafe.Sizeof(*s)) + int(unsafe.Sizeof(s.Rows[0])) + len(s.Rows[0].SessionID) + 8 + 128 + len("missing") + 80
	if bytes < minimum {
		t.Fatalf("progress retained bytes=%d want at least%d", bytes, minimum)
	}
}

var progressProtocolSequence atomic.Int64

func TestMessagingProgressDirectAndGroupLifecycleIntegration(t *testing.T) {
	for _, mode := range []string{"direct", "direct-open", "group", "group-open", "at-once"} {
		t.Run(mode, func(t *testing.T) {
			name := fmt.Sprintf("progress_engine_%d", progressProtocolSequence.Add(1))
			var created atomic.Int64
			protocol.Register(name, func() protocol.Protocol {
				return &localMessagingProgressProtocol{name: name, progress: engineProgressFixture(strconv.FormatInt(created.Add(1), 10)), teardownLifecycle: "released"}
			})
			script := &dsl.Script{Protocol: name, Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 1}}, Actions: []dsl.Action{{Type: "http_request", Name: "one"}}}
			if strings.Contains(mode, "open") {
				script.Config.RunLogic.WorkloadModel = "open"
				script.Config.RunLogic.MaxInFlight = 1
				script.Config.Pacing.Type = "tps"
				script.Config.Pacing.TPS = 100
			}
			var latest MetricsSnapshot
			var ok bool
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if strings.HasPrefix(mode, "direct") {
				e, err := New(Config{Script: script, VUsers: 1, Duration: time.Second})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = e.Run(ctx); err != nil {
					t.Fatal(err)
				}
				latest, ok = e.LatestSnapshot()
			} else {
				group := GroupConfig{Name: "trusted", Script: script, VUsers: 1}
				if mode == "at-once" {
					group.InitStrategy = "at_once"
					group.VUsers = 0
					group.AllowZeroVUsers = true
					script.Init = script.Actions
					script.Teardown = script.Actions
					script.Actions = nil
				}
				e, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{group}, Duration: 20 * time.Millisecond})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = e.Run(ctx); err != nil {
					t.Fatal(err)
				}
				latest, ok = e.LatestSnapshot()
			}
			if !ok || latest.MessagingProgress == nil || len(latest.MessagingProgress.Rows) == 0 || !latest.MessagingProgress.Valid() || latest.MessagingProgress.Incomplete {
				t.Fatalf("lifecycle evidence absent/incomplete: %+v", latest.MessagingProgress)
			}
			for _, row := range latest.MessagingProgress.Rows {
				want := "trusted"
				if strings.HasPrefix(mode, "direct") {
					want = ""
				}
				if row.Group != want || row.Origin != "" {
					t.Fatal("untrusted integration attribution")
				}
			}
		})
	}
}

func TestMessagingProgressFinalRegistrationFence(t *testing.T) {
	mc := NewMetricsCollector(8)
	p := &localMessagingProgressProtocol{progress: engineProgressFixture("blocked-final"), blockAfter: 1, readEntered: make(chan struct{}), readRelease: make(chan struct{})}
	entry := mc.registerMessagingProgress(p, "group", nil)
	if entry == nil {
		t.Fatal("fixture not registered")
	}
	done := make(chan struct{})
	go func() { mc.messagingProgress.sample(true); close(done) }()
	select {
	case <-p.readEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("final capture did not begin")
	}
	if mc.registerMessagingProgress(&localMessagingProgressProtocol{progress: engineProgressFixture("late")}, "group", nil) != nil {
		t.Fatal("reporter admitted beyond final inventory fence")
	}
	close(p.readRelease)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("final capture did not finish")
	}
	if entry.provider != nil || len(mc.messagingProgress.entries) != 0 || !mc.messagingProgress.snapshot().Incomplete {
		t.Fatal("final capture lost incomplete cleanup/reference accounting")
	}
}

func TestMessagingProgressStopBeforeStartKeepsFinalEvidence(t *testing.T) {
	mc := NewMetricsCollector(8)
	mc.resCollector = nil
	entry := mc.registerMessagingProgress(&localMessagingProgressProtocol{progress: engineProgressFixture("not-started")}, "group", nil)
	mc.Stop()
	snapshot, ok := mc.LatestSnapshot()
	if !ok || snapshot.MessagingProgress == nil || !snapshot.MessagingProgress.Incomplete || entry.provider != nil || snapshot.Transactions != 0 {
		t.Fatal("Stop before first tick lost final coverage")
	}
}

func TestMessagingProgressPausedAndScaleZeroLifecycle(t *testing.T) {
	name := fmt.Sprintf("progress_paused_%d", progressProtocolSequence.Add(1))
	protocol.Register(name, func() protocol.Protocol {
		return &localMessagingProgressProtocol{name: name, progress: engineProgressFixture("paused"), teardownLifecycle: "released"}
	})
	script := &dsl.Script{Protocol: name, Config: dsl.ScriptConfig{Pacing: dsl.PacingConfig{Type: "fixed_delay", DelayMs: 3600000}}}
	e, err := NewMultiGroup(MultiGroupConfig{Groups: []GroupConfig{{Name: "trusted", Script: script, VUsers: 1}}, Duration: 8 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := e.Run(ctx); done <- err }()
	defer func() {
		e.Stop()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("paused engine did not stop")
		}
	}()
	s := waitForMessagingProgressSnapshot(t, e.metrics, func(s MetricsSnapshot) bool { return s.MessagingProgress != nil && s.ActiveVUsers == 1 })
	if err := e.Pause(); err != nil {
		t.Fatal(err)
	}
	beforePause := s.Timestamp
	s = waitForMessagingProgressSnapshot(t, e.metrics, func(s MetricsSnapshot) bool {
		return s.MessagingProgress != nil && s.ActiveVUsers == 1 && s.Timestamp.After(beforePause)
	})
	if s.Transactions != 0 || s.Errors != 0 || len(s.ByAction) != 0 {
		t.Fatal("paused progress fabricated action populations")
	}
	if err := e.ScaleVUsers("trusted", 0); err != nil {
		t.Fatal(err)
	}
	s = waitForMessagingProgressSnapshot(t, e.metrics, func(s MetricsSnapshot) bool {
		return s.MessagingProgress != nil && s.ActiveVUsers == 0 && s.MessagingProgress.Lifecycle == "released"
	})
	if s.Transactions != 0 || s.Errors != 0 || len(s.ByAction) != 0 || s.MessagingProgress.Incomplete {
		t.Fatal("scale-zero lifecycle lost coverage or fabricated action populations")
	}
}

func TestMessagingProgressSourceEvictionAndProvisionalReplacement(t *testing.T) {
	mc := NewMetricsCollector(8)
	initial := engineProgressFixture("inventory")
	initial.Rows[0].Generation = "unverified"
	initial.Rows[0].Ownership = "unverified"
	initial.Rows[0].Acquired = 0
	p := &localMessagingProgressProtocol{progress: initial}
	mc.registerMessagingProgress(p, "group", nil)
	verified := engineProgressFixture("inventory")
	verified.CapturedAt = initial.CapturedAt.Add(time.Second)
	p.mu.Lock()
	p.progress = verified
	p.mu.Unlock()
	mc.messagingProgress.sample(false)
	if got := mc.messagingProgress.snapshot(); len(got.Rows) != 1 || got.Rows[0].Generation == "unverified" || got.Incomplete {
		t.Fatal("verified inventory retained fictitious provisional generation", got)
	}
	bounded := verified.Clone()
	bounded.Rows = nil
	for i := 0; i < protocol.MaxMessagingProgressRows; i++ {
		row := verified.Rows[0]
		row.Generation = strconv.Itoa(i)
		bounded.Rows = append(bounded.Rows, row)
	}
	bounded.CapturedAt = verified.CapturedAt.Add(time.Second)
	bounded.DroppedRows = 6
	bounded.Incomplete = true
	p.mu.Lock()
	p.progress = bounded
	p.mu.Unlock()
	for i := 0; i < 4; i++ {
		mc.messagingProgress.sample(false)
	}
	got := mc.messagingProgress.snapshot()
	if !got.Valid() || len(got.Rows) != 64 || got.DroppedRows != 6 {
		t.Fatalf("source eviction replay changed dropped population: %+v", got)
	}
	bounded = bounded.Clone()
	bounded.Rows[0].Generation = "new-generation"
	bounded.CapturedAt = bounded.CapturedAt.Add(time.Second)
	bounded.DroppedRows = 7
	p.mu.Lock()
	p.progress = bounded
	p.mu.Unlock()
	for i := 0; i < 4; i++ {
		mc.messagingProgress.sample(false)
	}
	if got = mc.messagingProgress.snapshot(); !got.Valid() || len(got.Rows) != 64 || got.DroppedRows != 7 {
		t.Fatal("new eviction or duplicate capture lost cumulative source population", got)
	}
}

func TestMessagingProgressExpectedProcessorMissingIsExplicit(t *testing.T) {
	resources, err := newEngineRuntimeResources([]GroupConfig{{Name: "consumer", Script: processorResourceScript(), VUsers: 1}})
	if err != nil {
		t.Fatal(err)
	}
	mc := NewMetricsCollector(8)
	mc.resCollector = nil
	entry := mc.registerMessagingProgress(&localMessagingProgressProtocol{name: "eventhubs"}, "consumer", resources)
	if entry == nil {
		t.Fatal("failed processor initialization hid expected progress source")
	}
	mc.Stop()
	latest, ok := mc.LatestSnapshot()
	if !ok || latest.MessagingProgress == nil || latest.MessagingProgress.UnavailableReason != "source_missing" || !latest.MessagingProgress.Incomplete || !latest.MessagingProgress.Valid() {
		t.Fatal("missing expected source became legacy/zero evidence")
	}
}

func TestMessagingProgressAtOncePhaseEligibility(t *testing.T) {
	for _, phase := range []string{"runtime", "init", "teardown"} {
		for _, consume := range []string{"actions", "init", "teardown"} {
			t.Run(phase+"/"+consume, func(t *testing.T) {
				script := processorResourceScript()
				script.Actions = []dsl.Action{{Type: "eventhubs_produce"}}
				switch consume {
				case "actions":
					script.Actions = []dsl.Action{{Type: "eventhubs_consume"}}
				case "init":
					script.Init = []dsl.Action{{Type: "eventhubs_consume"}}
				case "teardown":
					script.Teardown = []dsl.Action{{Type: "eventhubs_consume"}}
				}
				resources, err := newEngineRuntimeResources([]GroupConfig{{Name: "group", Script: script, VUsers: 1000, InitStrategy: "at_once"}})
				if err != nil {
					t.Fatal(err)
				}
				mc := NewMetricsCollector(8)
				p := &localMessagingProgressProtocol{name: "eventhubs", progress: engineProgressFixture("phase")}
				entry := mc.registerMessagingProgress(p, "group", resources, phase)
				want := phase == consume || phase == "runtime" && consume == "actions"
				if (entry != nil) != want || !want && p.reads.Load() != 0 {
					t.Fatal("at-once nonconsuming instance created progress/overflow", phase, consume)
				}
			})
		}
	}
}
