package livetable

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestObservationsRetainWaitsWithoutInventingInspectionRecovery(t *testing.T) {
	table := tableFor(t, ProfileNative, nil, Limits{})
	_, _ = table.Execute(context.Background(), Request{ClientID: "waiting", Sequence: 1, Operation: OpPop, Wait: time.Millisecond})
	before := table.Stats().Observations
	if before.WaitCount != 1 || before.WaitTotalNS == 0 || before.Incomplete {
		t.Fatalf("wait evidence: %+v", before)
	}
	snapshot, err := table.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	retained := restored.Stats().Observations
	if retained.WaitCount != before.WaitCount || retained.WaitTotalNS != before.WaitTotalNS || retained.RecoveryGaps != 0 || retained.Incomplete {
		t.Fatalf("inspection changed retained evidence: %+v", retained)
	}
	restored.RecordRecoveryGap()
	retained = restored.Stats().Observations
	if retained.RecoveryGaps != 1 || !retained.Incomplete || retained.Events[len(retained.Events)-1].Kind != "recovery_observation_gap" {
		t.Fatalf("serving recovery failed to declare observation gap: %+v", retained)
	}
}

func TestObservationsMarkCheckpointWithInFlightWaitIncomplete(t *testing.T) {
	table := tableFor(t, ProfileNative, nil, Limits{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = table.Execute(ctx, Request{ClientID: "waiting", Sequence: 1, Operation: OpPop, Wait: time.Second})
	}()
	deadline := time.Now().Add(time.Second)
	for table.Stats().Waiters == 0 {
		if time.Now().After(deadline) {
			t.Fatal("wait was not admitted")
		}
		time.Sleep(time.Millisecond)
	}
	snapshot, err := table.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Observations.Incomplete {
		t.Fatal("in-flight observations presented as complete")
	}
	cancel()
	<-done
	if table.Stats().Observations.WaitCount != 1 {
		t.Fatal("settled wait was not observed")
	}
}

func TestObservationEventsAreBoundedValueFreeAndValidated(t *testing.T) {
	table := tableFor(t, ProfileNative, nil, Limits{})
	for i := 0; i < 40; i++ {
		table.observeEvidence(OpPop, Result{Status: StatusEmpty}, nil)
		table.observeEvidence(OpPop, Result{}, &Error{Code: CodeCapacity, Message: "private-row-value"})
	}
	observed := table.Stats().Observations
	if len(observed.Events) != maxObservationEvents || observed.DroppedEvents == 0 || observed.EmptyOutcomes != 40 || observed.Throttled != 40 {
		t.Fatalf("unbounded or missing observations: %+v", observed)
	}
	encoded, _ := json.Marshal(observed)
	if strings.Contains(string(encoded), "private-row-value") {
		t.Fatal("raw error text entered evidence")
	}
	snapshot, _ := table.Snapshot()
	snapshot.Observations.Events[0].Kind = "private-unrecognized-event"
	if _, err := Restore(snapshot, nil); err == nil {
		t.Fatal("unrecognized event restored")
	}
	snapshot.Observations = Observations{}
	legacy, err := Restore(snapshot, nil)
	if err != nil || !legacy.Stats().Observations.Incomplete {
		t.Fatalf("old snapshot claimed complete evidence: %v", err)
	}
}

func TestUnknownPopEvidenceNamesAttemptsNotConsumedRecords(t *testing.T) {
	table := tableFor(t, ProfileNative, nil, Limits{})
	table.observeEvidence(OpPop, Result{}, &Error{Code: CodeUnavailable, OutcomeUnknown: true})
	observed := table.Stats().Observations
	if observed.UnknownPopAttempts != 1 || observed.UnknownOutcomes != 1 || observed.Events[0].Kind != "unknown_pop_attempt" || table.Stats().Counts.Popped != 0 {
		t.Fatalf("attempt evidence invented removed work: %+v", observed)
	}
}

func TestObservationEnvelopeCountsAgainstSnapshotBudget(t *testing.T) {
	table := tableFor(t, ProfileNative, nil, Limits{})
	for i := 0; i < 16; i++ {
		table.observeEvidence(OpPop, Result{Status: StatusEmpty}, nil)
		table.observeEvidence(OpPop, Result{}, &Error{Code: CodeCapacity})
	}
	snapshot, err := table.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotEncodedSize(snapshot) != len(encoded) {
		t.Fatal("envelope accounting differs from encoded snapshot")
	}
	// Payload fits while outer fields and retained events do not.
	table.s.Limits.MaxSnapshotBytes = len(snapshot.Payload) + 512
	if _, err := table.Snapshot(); CodeOf(err) != CodeCapacity {
		t.Fatalf("outer envelope bypassed snapshot cap: %v", err)
	}
	snapshot.Payload, _ = json.Marshal(table.s)
	if _, err := Restore(snapshot, nil); CodeOf(err) != CodeCapacity {
		t.Fatalf("outer envelope bypassed restore cap: %v", err)
	}
}

func TestObservationClockRegressionKeepsValidIntervals(t *testing.T) {
	table := tableFor(t, ProfileNative, nil, Limits{})
	now := time.Now().UTC()
	table.observationMu.Lock()
	table.observationEventLocked("empty", now)
	table.observationEventLocked("empty", now.Add(-time.Hour))
	table.observationMu.Unlock()
	observed := table.Stats().Observations
	if observed.Events[0].Last.Before(observed.Events[0].First) || observed.Events[0].Count != 2 {
		t.Fatalf("clock reversal corrupted interval: %+v", observed)
	}
	if err := validateObservations(observed); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreBoundsCanonicalSnapshotEncoding(t *testing.T) {
	table := tableFor(t, ProfileNative, []Row{row(strings.Repeat("<", 4096), "1")}, Limits{})
	snapshot, err := table.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	shortPayload := strings.ReplaceAll(string(snapshot.Payload), `\u003c`, "<")
	table.s.Limits.MaxSnapshotBytes = len(shortPayload) + 1024
	canonical, _ := json.Marshal(table.s)
	snapshot.Payload = []byte(strings.ReplaceAll(string(canonical), `\u003c`, "<"))
	if snapshotEncodedSize(snapshot) > table.s.Limits.MaxSnapshotBytes {
		t.Fatal("fixture input should fit before canonicalization")
	}
	if _, err := Restore(snapshot, nil); CodeOf(err) != CodeCapacity {
		t.Fatalf("recovered a state that cannot be checkpointed within its bound: %v", err)
	}
}
