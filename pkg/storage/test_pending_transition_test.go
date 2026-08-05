package storage

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newPendingTransitionTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "pending-transition.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.SaveProject(&Project{ID: "pending-project", Name: "Pending", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return store
}

func pendingTransitionTestRecord(id, refKey string, start time.Time) *TestRecord {
	return &TestRecord{
		ID: id, RefKey: refKey, Name: "Pending transition", Status: "QUEUED", ProjectID: "pending-project",
		TotalVUsers: 1, Duration: int64(time.Minute), StartTime: start,
		Groups: []any{map[string]any{"name": "preserved group"}},
	}
}

func TestTransitionTestToPendingPersistsExactQueueContract(t *testing.T) {
	store := newPendingTransitionTestStore(t)
	createdAt := time.Unix(1_800_000_000, 123_456_789).UTC()
	record := pendingTransitionTestRecord("pending-contract", "run-pending-contract", createdAt)
	timeslotID := "preserved-timeslot"
	record.TimeslotID = &timeslotID
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	revision, err := store.StartAdmissionRevision()
	if err != nil {
		t.Fatal(err)
	}
	pendingAt := createdAt.Add(time.Second)
	transition := TestPendingTransition{
		ID: record.ID, RefKey: record.RefKey, ProjectID: record.ProjectID, StartTime: createdAt,
		ExpectedStatus: "QUEUED", PendingSince: pendingAt,
		StartAuthorization: map[string]any{"capacity_shortfall": map[string]any{"approved": true}},
		StartedBySubject:   "operator-1", StartedByDisplayName: "Operator One",
		ExpectedStartAdmissionRevision: revision,
	}
	result, err := store.TransitionTestToPending(transition)
	if err != nil || result != TestStatusTransitionUpdated {
		t.Fatalf("TransitionTestToPending = %q, %v; want updated", result, err)
	}
	durable, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Status != "PENDING" || durable.PendingSince == nil || !durable.PendingSince.Equal(pendingAt) {
		t.Fatalf("durable pending lifecycle = status %q since %v", durable.Status, durable.PendingSince)
	}
	if durable.StartedBySubject != transition.StartedBySubject || durable.StartedByDisplayName != transition.StartedByDisplayName {
		t.Fatalf("durable start actor = %q/%q", durable.StartedBySubject, durable.StartedByDisplayName)
	}
	wantAuthorization, _ := json.Marshal(transition.StartAuthorization)
	gotAuthorization, _ := json.Marshal(durable.StartAuthorization)
	if string(gotAuthorization) != string(wantAuthorization) {
		t.Fatalf("durable start authorization = %s, want %s", gotAuthorization, wantAuthorization)
	}
	if durable.TimeslotID == nil || *durable.TimeslotID != timeslotID {
		t.Fatalf("targeted transition changed timeslot: %#v", durable.TimeslotID)
	}
	groups, _ := json.Marshal(durable.Groups)
	if !strings.Contains(string(groups), "preserved group") {
		t.Fatalf("targeted transition changed groups: %s", groups)
	}

	// An ambiguous response can be retried only when every targeted queue field
	// is already the exact intended value. pending_since is server-generated
	// transition metadata, so a retry with a new timestamp adopts the durable one.
	transition.PendingSince = pendingAt.Add(time.Second)
	result, err = store.TransitionTestToPending(transition)
	if err != nil || result != TestStatusTransitionUnchanged {
		t.Fatalf("exact retry = %q, %v; want unchanged", result, err)
	}
	changedActor := transition
	changedActor.StartedBySubject = "operator-2"
	if result, err = store.TransitionTestToPending(changedActor); err != nil || result != TestStatusTransitionFenced {
		t.Fatalf("changed-actor retry = %q, %v; want fenced", result, err)
	}
	changedAuthorization := transition
	changedAuthorization.StartAuthorization = map[string]any{"capacity_shortfall": map[string]any{"approved": false}}
	if result, err = store.TransitionTestToPending(changedAuthorization); err != nil || result != TestStatusTransitionFenced {
		t.Fatalf("changed-authorization retry = %q, %v; want fenced", result, err)
	}
}

func TestTransitionTestToPendingFencesSameTimestampReplacement(t *testing.T) {
	store := newPendingTransitionTestStore(t)
	createdAt := time.Unix(1_800_000_100, 987_654_321).UTC()
	original := pendingTransitionTestRecord("recreated-pending", "run-original-incarnation", createdAt)
	if err := store.SaveTest(original); err != nil {
		t.Fatal(err)
	}
	stale := TestPendingTransition{
		ID: original.ID, RefKey: original.RefKey, ProjectID: original.ProjectID, StartTime: original.StartTime,
		ExpectedStatus: "QUEUED", PendingSince: createdAt.Add(time.Second),
	}
	if err := store.DeleteTest(original.ID); err != nil {
		t.Fatal(err)
	}
	replacement := pendingTransitionTestRecord(original.ID, "run-replacement-incarnation", createdAt)
	if err := store.SaveTest(replacement); err != nil {
		t.Fatal(err)
	}
	result, err := store.TransitionTestToPending(stale)
	if err != nil {
		t.Fatalf("TransitionTestToPending: %v", err)
	}
	if result != TestStatusTransitionFenced {
		t.Fatalf("replacement transition result = %q, want fenced", result)
	}
	durable, err := store.GetTest(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.RefKey != replacement.RefKey || durable.Status != "QUEUED" {
		t.Fatalf("replacement identity was changed: ref=%q status=%q", durable.RefKey, durable.Status)
	}
}

func TestPostgresPendingTransitionLocksContentBeforeAdmissionSingletons(t *testing.T) {
	queries := postgresTestPendingTransitionLockOrderSQL()
	if len(queries) != 3 || !strings.Contains(queries[0], "FROM tests") || !strings.Contains(queries[0], "FOR UPDATE") ||
		!strings.Contains(queries[1], "scheduler_content_revision") || !strings.Contains(queries[2], "start_admission_revision") {
		t.Fatalf("PostgreSQL pending-transition lock order = %#v, want test row then scheduler/start admission singletons", queries)
	}
}
