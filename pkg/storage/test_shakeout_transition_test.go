package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func newShakeoutTransitionStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "shakeout-transition.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func shakeoutTransitionRecord(id string, createdAt time.Time) *TestRecord {
	slotID := "slot-owned-by-another-transition"
	return &TestRecord{
		ID: id, RefKey: "run-" + id, Name: id, Status: "SHAKEOUT",
		ProjectID: DefaultProjectID, StartTime: createdAt, TotalVUsers: 3,
		Duration: int64(time.Minute), Groups: []any{map[string]any{"name": "original"}},
		TimeslotID: &slotID, StartAuthorization: map[string]any{"capacity": "retained"},
	}
}

func TestTransitionTestFromShakeoutIsTargetedAndIdempotent(t *testing.T) {
	store := newShakeoutTransitionStore(t)
	createdAt := time.Unix(1_800_100_000, 123_456_789).UTC()
	record := shakeoutTransitionRecord("shakeout-pass", createdAt)
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	pendingAt := createdAt.Add(time.Minute)
	authorization := map[string]any{
		"capacity":             "retained",
		"script_shakeout_pass": map[string]any{"evidence_sha256": "evidence", "passed_at": pendingAt},
	}
	transition := TestShakeoutTransition{
		ID: record.ID, RefKey: record.RefKey, ProjectID: record.ProjectID, StartTime: record.StartTime,
		Status: "PENDING", PendingSince: &pendingAt, StartAuthorization: authorization,
	}
	result, err := store.TransitionTestFromShakeout(transition)
	if err != nil || result != TestStatusTransitionUpdated {
		t.Fatalf("TransitionTestFromShakeout = (%q, %v), want updated", result, err)
	}
	durable, err := store.GetTest(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Status != "PENDING" || durable.PendingSince == nil || !durable.PendingSince.Equal(pendingAt) {
		t.Fatalf("durable lifecycle = status %q pending %v", durable.Status, durable.PendingSince)
	}
	if durable.TotalVUsers != record.TotalVUsers || durable.TimeslotID == nil || *durable.TimeslotID != *record.TimeslotID {
		t.Fatalf("targeted transition overwrote unrelated fields: %#v", durable)
	}
	groups, ok := durable.Groups.([]any)
	if !ok || len(groups) != 1 {
		t.Fatalf("targeted transition changed groups: %#v", durable.Groups)
	}
	result, err = store.TransitionTestFromShakeout(transition)
	if err != nil || result != TestStatusTransitionUnchanged {
		t.Fatalf("idempotent retry = (%q, %v), want unchanged", result, err)
	}
}

func TestTransitionTestFromShakeoutCannotResurrectAbortOrReplacement(t *testing.T) {
	store := newShakeoutTransitionStore(t)
	createdAt := time.Unix(1_800_100_100, 987_654_321).UTC()
	record := shakeoutTransitionRecord("shakeout-aborted", createdAt)
	if err := store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	abortedAt := createdAt.Add(time.Second)
	if err := store.UpdateTestStatus(record.ID, "ABORTED", &abortedAt, "operator cancelled"); err != nil {
		t.Fatal(err)
	}
	transition := TestShakeoutTransition{
		ID: record.ID, RefKey: record.RefKey, ProjectID: record.ProjectID, StartTime: record.StartTime,
		Status: "QUEUED", StartAuthorization: map[string]any{"script_shakeout_pass": true},
	}
	result, err := store.TransitionTestFromShakeout(transition)
	if err != nil || result != TestStatusTransitionFenced {
		t.Fatalf("transition over abort = (%q, %v), want fenced", result, err)
	}
	durable, err := store.GetTest(record.ID)
	if err != nil || durable.Status != "ABORTED" {
		t.Fatalf("aborted row was resurrected: %#v, %v", durable, err)
	}

	replacementID := "shakeout-replaced"
	original := shakeoutTransitionRecord(replacementID, createdAt)
	if err := store.SaveTest(original); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTest(replacementID); err != nil {
		t.Fatal(err)
	}
	replacement := shakeoutTransitionRecord(replacementID, createdAt)
	replacement.RefKey = "run-replacement-incarnation"
	if err := store.SaveTest(replacement); err != nil {
		t.Fatal(err)
	}
	transition.ID = original.ID
	transition.RefKey = original.RefKey
	transition.StartTime = original.StartTime
	result, err = store.TransitionTestFromShakeout(transition)
	if err != nil || result != TestStatusTransitionFenced {
		t.Fatalf("transition over replacement = (%q, %v), want fenced", result, err)
	}
	durable, err = store.GetTest(replacementID)
	if err != nil || durable.RefKey != replacement.RefKey || durable.Status != "SHAKEOUT" {
		t.Fatalf("replacement row was changed: %#v, %v", durable, err)
	}
}
