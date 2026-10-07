package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Under the durable execution fence each receipt proves the run's lease
// term: the term's holder records receipts, a deposed controller (another
// owner or an older term) records nothing.
func TestRemoteHARReceiptsAreFencedOnTheExecutionLeaseTerm(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.SaveTest(&storage.TestRecord{ID: "fenced-run", Name: "fenced", Status: "COMPLETE", StartTime: now, EndTime: &now}); err != nil {
		t.Fatal(err)
	}
	acquired, term, err := store.TryAcquireSchedulerLeaseTerm(executionLeaseName, "controller-a", now, time.Minute)
	if err != nil || !acquired || term == nil {
		t.Fatalf("acquire: %v %v", acquired, err)
	}
	tr := NewTestRunner(1, nil, nil)
	tr.testStore, tr.durableExecutionFence = store, true
	entry := func(generation string) RunArchiveEvidenceArtifact {
		return RunArchiveEvidenceArtifact{Name: "browser_har_" + generation + ".har", Kind: storage.EvidenceKindHARNative, Fidelity: "native", SourceIdentity: "source-a", SourceGeneration: generation, Sequence: 1, SHA256: strings.Repeat("c", 64), Bytes: 9}
	}
	holder := &ManagedTest{ID: "fenced-run", executionFence: "controller-a", executionGeneration: term.Generation}
	if state, err := tr.acceptRemoteHARReceipt(holder, nil)(entry(strings.Repeat("1", 32))); err != nil || state != storage.EvidenceReceiptAccepted {
		t.Fatalf("lease holder: %q %v", state, err)
	}
	for name, deposed := range map[string]*ManagedTest{
		"other owner": {ID: "fenced-run", executionFence: "controller-b", executionGeneration: term.Generation},
		"later term":  {ID: "fenced-run", executionFence: "controller-a", executionGeneration: term.Generation + 1},
	} {
		if state, err := tr.acceptRemoteHARReceipt(deposed, nil)(entry(strings.Repeat("2", 32))); err != nil || state != storage.EvidenceReceiptTombstoned {
			t.Fatalf("%s: %q %v", name, state, err)
		}
	}
	receipts, _ := store.ListRunEvidenceReceipts("fenced-run")
	if len(receipts) != 1 || receipts[0].SourceGeneration != strings.Repeat("1", 32) {
		t.Fatalf("a fenced accept wrote a receipt: %+v", receipts)
	}
	// Without the durable fence (a single controller) no lease is checked.
	tr.durableExecutionFence = false
	if state, err := tr.acceptRemoteHARReceipt(&ManagedTest{ID: "fenced-run", executionFence: "controller-z", executionGeneration: term.Generation}, nil)(entry(strings.Repeat("3", 32))); err != nil || state != storage.EvidenceReceiptAccepted {
		t.Fatalf("unfenced accept: %q %v", state, err)
	}
}

// A merge whose source context ended (the controller lost the run's lease)
// records no further receipts.
func TestReceiptAcceptorStopsWhenTheSourceContextEnds(t *testing.T) {
	calls := 0
	accept := func(RunArchiveEvidenceArtifact) (string, error) {
		calls++
		return storage.EvidenceReceiptAccepted, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	fenced := fenceReceiptAcceptor(ctx, accept)
	if state, err := fenced(RunArchiveEvidenceArtifact{}); err != nil || state != storage.EvidenceReceiptAccepted || calls != 1 {
		t.Fatalf("live source: %q %v calls %d", state, err, calls)
	}
	cancel()
	if _, err := fenced(RunArchiveEvidenceArtifact{}); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("ended source accepted: %v calls %d", err, calls)
	}
	if state, _ := fenceReceiptAcceptor(nil, accept)(RunArchiveEvidenceArtifact{}); state != storage.EvidenceReceiptAccepted || calls != 2 {
		t.Fatal("an unfenced acceptor was wrapped")
	}
}

type scriptedAckClient struct {
	err   error
	calls int
}

func (c *scriptedAckClient) AcknowledgeEvidence(_ context.Context, request generatorrpc.EvidenceAcknowledgement) (generatorrpc.EvidenceAckResult, error) {
	c.calls++
	if c.err != nil {
		return generatorrpc.EvidenceAckResult{}, c.err
	}
	return generatorrpc.EvidenceAckResult{Retained: []string{request.Artifacts[0].Name}}, nil
}

// ackOnlyGeneratorClient is a generator client that only acknowledges.
type ackOnlyGeneratorClient struct {
	generatorClient
	*scriptedAckClient
}

// A generator that no longer has the run's archive confirms the
// acknowledgement; a failed one (no client, an error, a retained copy) is
// retried only after the retry interval, so later commits of the run do not
// re-drive it each time.
func TestEvidenceAcknowledgementConfirmsSweptArchivesAndBacksOffFailures(t *testing.T) {
	request := generatorrpc.EvidenceAcknowledgement{TestID: "run", SourceIdentity: "source-a", Artifacts: []generatorrpc.AcknowledgedEvidence{{Name: "browser_har_a.har", SHA256: strings.Repeat("d", 64)}}}
	key := acknowledgedEvidenceKey("run", "source-a", "browser_har_a.har", strings.Repeat("d", 64))
	for name, tc := range map[string]struct {
		client    *scriptedAckClient
		confirmed bool
	}{
		"swept archive": {client: &scriptedAckClient{err: status.Error(codes.NotFound, "archive not found")}, confirmed: true},
		"error":         {client: &scriptedAckClient{err: status.Error(codes.Unavailable, "down")}},
		"retained copy": {client: &scriptedAckClient{}},
		"no client":     {},
	} {
		t.Run(name, func(t *testing.T) {
			set := &acknowledgedEvidenceSet{}
			clients := map[string]generatorClient{}
			if tc.client != nil {
				clients["gen"] = ackOnlyGeneratorClient{scriptedAckClient: tc.client}
			}
			acknowledgeRunEvidence("run", []evidenceAcknowledgementTarget{{target: "gen", request: request}}, clients, nil, set)
			now := time.Now()
			if tc.confirmed {
				if !set.has(key) || set.due(key, now.Add(time.Hour)) {
					t.Fatal("a swept archive's acknowledgement was not confirmed")
				}
				return
			}
			if set.has(key) || set.due(key, now) || !set.due(key, now.Add(evidenceAcknowledgementRetry+time.Second)) {
				t.Fatalf("failed acknowledgement: confirmed %v due now %v", set.has(key), set.due(key, now))
			}
			// A later confirmation is never undone by a retry time.
			set.add(key)
			set.retryAfter(key, now.Add(time.Hour))
			if !set.has(key) {
				t.Fatal("a retry time replaced a confirmation")
			}
		})
	}
}

// Shutdown waits for background acknowledgements only until its deadline.
func TestShutdownWaitForAcknowledgementsIsBounded(t *testing.T) {
	tr := NewTestRunner(1, nil, nil)
	tr.evidenceAcknowledgements.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	tr.waitEvidenceAcknowledgementsUntil(ctx)
	if elapsed := time.Since(started); elapsed < 40*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("an unfinished acknowledgement held shutdown for %s", elapsed)
	}
	tr.evidenceAcknowledgements.Done()
	done := make(chan struct{})
	go func() {
		tr.waitEvidenceAcknowledgementsUntil(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("finished acknowledgements still held shutdown")
	}
}
