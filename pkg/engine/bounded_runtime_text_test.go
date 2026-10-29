package engine

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type boundedTextBindingProbe struct {
	protocol.Protocol
	redact   protocol.BoundedTextRedactor
	snapshot protocol.BoundedTextRedactorSnapshot
}

func (p *boundedTextBindingProbe) SetRuntimeBoundedTextRedactor(redact protocol.BoundedTextRedactor) {
	p.redact = redact
}

func (p *boundedTextBindingProbe) SetRuntimeBoundedTextRedactorSnapshot(snapshot protocol.BoundedTextRedactorSnapshot) {
	p.snapshot = snapshot
}

func TestBoundedRuntimeDiagnosticTextUsesLiveSecretPolicy(t *testing.T) {
	config, err := NewRedactionConfig([]RedactionRule{{Pattern: "customer-[0-9]+", Replacement: "CUSTOMER"}})
	if err != nil {
		t.Fatal(err)
	}
	config = WithSecretValueRedaction(config, []string{"supersecret"})
	probe := &boundedTextBindingProbe{}
	bindProtocolRuntimeRedaction(probe, func() *RedactionConfig { return config })
	if probe.redact == nil {
		t.Fatal("bounded redactor was not bound")
	}
	for _, text := range []string{
		"token=prefix supersecret customer-42",
		`{"token":"supersecret","customer":"customer-42"}`,
		"https://example.test/path?token=supersecret&customer=customer-42",
	} {
		got, ok := probe.redact(text)
		if !ok || strings.Contains(got, "supersecret") || strings.Contains(got, "customer-42") || !strings.Contains(got, "[REDACTED]") || !strings.Contains(got, "CUSTOMER") {
			t.Fatalf("redact(%q) = %q, %v", text, got, ok)
		}
	}
	config = WithSecretValueRedaction(config, []string{"new-dynamic-secret"})
	if got, ok := probe.redact("replica=new-dynamic-secret"); !ok || strings.Contains(got, "new-dynamic-secret") {
		t.Fatalf("updated runtime policy = %q, %v", got, ok)
	}
}

func TestBoundedRuntimeDiagnosticTextFailsClosed(t *testing.T) {
	expansion, err := NewRedactionConfig([]RedactionRule{{Pattern: `^(a+)$`, Replacement: strings.Repeat("$1", 1000)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		value    string
		provider func() *RedactionConfig
	}{
		{"missing provider", "safe", nil},
		{"provider panic", "secret", func() *RedactionConfig { panic("secret") }},
		{"unavailable", "secret", func() *RedactionConfig { return unavailableRuntimePublicationConfig }},
		{"malformed policy", "secret", func() *RedactionConfig { return &RedactionConfig{Rules: []RedactionRule{{Pattern: "x"}}} }},
		{"oversized", strings.Repeat("x", maxBoundedRuntimeDiagnosticTextBytes+1), func() *RedactionConfig { return nil }},
		{"nul", "private\x00value", func() *RedactionConfig { return nil }},
		{"invalid utf8", string([]byte{0xff}), func() *RedactionConfig { return nil }},
		{"expansion", strings.Repeat("a", 100), func() *RedactionConfig { return expansion }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := boundedRuntimeDiagnosticText(tc.value, tc.provider); ok || got != "" {
				t.Fatalf("unsafe projection = %q, %v", got, ok)
			}
		})
	}
}

func TestBoundedRuntimeDiagnosticTextAllowsShortReplacementInLongField(t *testing.T) {
	config, err := NewRedactionConfig([]RedactionRule{{Pattern: "private", Replacement: "safe"}})
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Repeat("a", 8<<10) + "private"
	got, ok := boundedRuntimeDiagnosticText(input, func() *RedactionConfig { return config })
	if !ok || got != strings.Repeat("a", 8<<10)+"safe" {
		t.Fatalf("long input was not safely projected: length=%d, ok=%v", len(got), ok)
	}
}

func TestBoundedRuntimeDiagnosticTextSnapshotWitness(t *testing.T) {
	commit := func() bool { return true }
	first := WithSecretValueRedaction(nil, []string{"first-secret"})
	second := WithSecretValueRedaction(nil, []string{"second-secret"})
	var current atomic.Pointer[RedactionConfig]
	current.Store(first)
	project, stillCurrent := snapshotBoundedRuntimeDiagnosticText(current.Load)
	if project == nil || stillCurrent == nil || !stillCurrent(commit) {
		t.Fatal("initial runtime policy snapshot unavailable")
	}
	if got, ok := project("token=first-secret"); !ok || strings.Contains(got, "first-secret") {
		t.Fatalf("first projection = %q, %v", got, ok)
	}
	current.Store(second)
	if stillCurrent(commit) {
		t.Fatal("policy change did not invalidate projection witness")
	}
	project, stillCurrent = snapshotBoundedRuntimeDiagnosticText(current.Load)
	if got, ok := project("token=second-secret"); !ok || !stillCurrent(commit) || strings.Contains(got, "second-secret") {
		t.Fatalf("second projection = %q, %v", got, ok)
	}
	panicProvider := func() *RedactionConfig { panic("synthetic provider failure") }
	if project, stillCurrent := snapshotBoundedRuntimeDiagnosticText(panicProvider); project != nil || stillCurrent != nil {
		t.Fatal("panicking provider produced a snapshot")
	}
	panicOnWitness := false
	_, stillCurrent = snapshotBoundedRuntimeDiagnosticText(func() *RedactionConfig {
		if panicOnWitness {
			panic("synthetic provider failure")
		}
		return nil
	})
	panicOnWitness = true
	if stillCurrent == nil || stillCurrent(commit) {
		t.Fatal("panicking witness authorized publication")
	}
}

func TestBoundedRuntimeDiagnosticTextWitnessRejectsABAPublication(t *testing.T) {
	commit := func() bool { return true }
	a := WithSecretValueRedaction(nil, []string{"a-secret"})
	b := WithSecretValueRedaction(nil, []string{"b-secret"})
	var publications runtimeRedactionPublisher
	publications.Store(a)
	probe := &boundedTextBindingProbe{}
	bindProtocolRuntimeRedaction(probe, publications.Load, publications.snapshot)
	if probe.snapshot == nil {
		t.Fatal("versioned snapshot was not bound")
	}
	project, current := probe.snapshot()
	if project == nil || current == nil || !current(commit) {
		t.Fatal("snapshot not available")
	}
	if got, ok := project("token=a-secret"); !ok || strings.Contains(got, "a-secret") {
		t.Fatalf("A projection = %q, %v", got, ok)
	}
	publications.Store(b)
	publications.Store(a)
	if current(commit) {
		t.Fatal("A→B→A publication did not invalidate witness")
	}
	_, current = probe.snapshot()
	if !current(commit) {
		t.Fatal("fresh snapshot is not current")
	}
}

func TestBoundedRuntimeDiagnosticTextCommitFencesPolicyPublication(t *testing.T) {
	var publications runtimeRedactionPublisher
	a := WithSecretValueRedaction(nil, []string{"a-secret"})
	b := WithSecretValueRedaction(nil, []string{"b-secret"})
	publications.Store(a)
	_, guard := snapshotVersionedBoundedRuntimeDiagnosticText(publications.snapshot)
	entered, release := make(chan struct{}), make(chan struct{})
	committed := make(chan bool, 1)
	go func() {
		committed <- guard(func() bool { close(entered); <-release; return true })
	}()
	<-entered
	if publications.fence.TryLock() {
		publications.fence.Unlock()
		t.Fatal("evidence commit did not hold the policy fence")
	}
	storeStarted, storeDone := make(chan struct{}), make(chan struct{})
	go func() { close(storeStarted); publications.Store(b); publications.Store(a); close(storeDone) }()
	<-storeStarted
	select {
	case <-storeDone:
		t.Fatal("policy changed while the evidence commit held the fence")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if !<-committed {
		t.Fatal("current batch was not committed")
	}
	select {
	case <-storeDone:
	case <-time.After(time.Second):
		t.Fatal("policy publication did not resume after commit")
	}
	called := false
	if guard(func() bool { called = true; return true }) || called {
		t.Fatal("stale A→B→A snapshot committed")
	}
}
