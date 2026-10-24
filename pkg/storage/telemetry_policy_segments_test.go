package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestTelemetryPolicySegmentsAppendOnly(t *testing.T) {
	exerciseTelemetryPolicySegments(t, newHAFencingStore(t))
}

// exerciseTelemetryPolicySegments: segments append after the admission receipt
// (sequence 1) only for the expected latest sequence and only under the
// execution lease; a retried request ID returns its segment without appending;
// the list reads them in order; deleting the test deletes them.
func exerciseTelemetryPolicySegments(t *testing.T, store *SQLiteStore) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.SaveTest(&TestRecord{ID: "segments-run", Name: "Segments", Status: "RUNNING", ProjectID: DefaultProjectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	const leaseName = "segments-execution-lease"
	if acquired, err := store.TryAcquireSchedulerLease(leaseName, "owner-active", now, time.Minute); err != nil || !acquired {
		t.Fatalf("acquire lease = (%v, %v)", acquired, err)
	}
	segment := func(request string, at time.Time) TelemetryPolicySegment {
		return TelemetryPolicySegment{TestID: "segments-run", EffectiveAt: at, Receipt: json.RawMessage(`{"policy_id":"` + request + `"}`), Source: "run_selection", PolicyID: "tight", RequestID: request, RequestedBySubject: "user:editor"}
	}
	if _, _, err := store.AppendTelemetryPolicySegmentIfLeaseHeld(segment("stale-owner", now), 1, leaseName, "owner-stale"); !errors.Is(err, ErrTelemetryPolicySegmentLease) {
		t.Fatalf("an append without the lease: %v", err)
	}
	if _, _, err := store.AppendTelemetryPolicySegmentIfLeaseHeld(segment("ahead", now), 2, leaseName, "owner-active"); !errors.Is(err, ErrTelemetryPolicySegmentConflict) {
		t.Fatalf("an append expecting a later sequence: %v", err)
	}
	first, replayed, err := store.AppendTelemetryPolicySegmentIfLeaseHeld(segment("first", now.Add(time.Second)), 1, leaseName, "owner-active")
	if err != nil || replayed || first.Sequence != 2 {
		t.Fatalf("first append: %+v %v %v", first, replayed, err)
	}
	retried, replayed, err := store.AppendTelemetryPolicySegmentIfLeaseHeld(segment("first", now.Add(5*time.Second)), 7, leaseName, "owner-active")
	if err != nil || !replayed || retried.Sequence != 2 || !retried.EffectiveAt.Equal(first.EffectiveAt) {
		t.Fatalf("a retried request: %+v %v %v", retried, replayed, err)
	}
	if _, _, err := store.AppendTelemetryPolicySegmentIfLeaseHeld(segment("stale-expectation", now.Add(2*time.Second)), 1, leaseName, "owner-active"); !errors.Is(err, ErrTelemetryPolicySegmentConflict) {
		t.Fatalf("an append expecting the admission receipt after a segment: %v", err)
	}
	second, _, err := store.AppendTelemetryPolicySegmentIfLeaseHeld(segment("second", now.Add(3*time.Second)), 2, leaseName, "owner-active")
	if err != nil || second.Sequence != 3 {
		t.Fatalf("second append: %+v %v", second, err)
	}
	if _, _, err := store.AppendTelemetryPolicySegmentIfLeaseHeld(TelemetryPolicySegment{TestID: "missing-run", EffectiveAt: now, Receipt: json.RawMessage(`{}`), Source: "system_settings", RequestID: "missing"}, 1, leaseName, "owner-active"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("an append for a missing test: %v", err)
	}
	// A controller outside HA mode appends without a lease.
	unfenced, _, err := store.AppendTelemetryPolicySegmentIfLeaseHeld(segment("unfenced", now.Add(4*time.Second)), 3, "", "")
	if err != nil || unfenced.Sequence != 4 {
		t.Fatalf("an unfenced append: %+v %v", unfenced, err)
	}
	if _, _, err := store.AppendTelemetryPolicySegmentIfLeaseHeld(segment("half-lease", now), 4, leaseName, ""); err == nil {
		t.Fatal("an append naming a lease without its owner was accepted")
	}
	listed, err := store.ListTelemetryPolicySegments("segments-run")
	if err != nil || len(listed) != 3 || listed[0].Sequence != 2 || listed[1].Sequence != 3 || listed[0].RequestedBySubject != "user:editor" || listed[0].PolicyID != "tight" || string(listed[1].Receipt) != `{"policy_id":"second"}` {
		t.Fatalf("listed: %+v %v", listed, err)
	}
	if err := store.DeleteTest("segments-run"); err != nil {
		t.Fatal(err)
	}
	if listed, err := store.ListTelemetryPolicySegments("segments-run"); err != nil || len(listed) != 0 {
		t.Fatalf("segments after the test was deleted: %+v %v", listed, err)
	}
}
