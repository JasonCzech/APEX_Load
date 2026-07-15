package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestEnsureScriptRefKeyIsNarrowAndFencesStaleSnapshots(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	script := &dsl.Script{
		ID: "refkey-script", Name: "Checkout", Protocol: "http", Version: "1.0.0", ProjectID: DefaultProjectID,
		Actions: []dsl.Action{{Name: "Open", Type: "GET", Target: "/checkout"}},
	}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	staleUpdatedAt := script.UpdatedAt

	durable, updatedAt, err := store.EnsureScriptRefKey(script.ID, "LOC-SCR-0001-v1")
	if err != nil {
		t.Fatal(err)
	}
	if durable != "LOC-SCR-0001-v1" || !updatedAt.After(staleUpdatedAt) {
		t.Fatalf("EnsureScriptRefKey = %q, %s; want installed key and newer timestamp", durable, updatedAt)
	}
	stored, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "Checkout" || len(stored.Actions) != 1 || stored.Actions[0].Target != "/checkout" {
		t.Fatalf("script content changed during ref-key backfill: %+v", stored)
	}

	stale := *stored
	stale.RefKey = ""
	stale.Name = "stale overwrite"
	if updated, err := store.UpdateScriptIfUnchanged(&stale, staleUpdatedAt); err != nil || updated {
		t.Fatalf("stale conditional update = %v, %v; want fenced", updated, err)
	}
	winner, winnerUpdatedAt, err := store.EnsureScriptRefKey(script.ID, "LOC-SCR-9999-v1")
	if err != nil {
		t.Fatal(err)
	}
	if winner != durable || !winnerUpdatedAt.Equal(updatedAt) {
		t.Fatalf("second EnsureScriptRefKey = %q, %s; want %q, %s", winner, winnerUpdatedAt, durable, updatedAt)
	}

	// Legacy broad writers may carry a pre-backfill blank key. They must not
	// erase the durable winner even when changing other script fields.
	stale.UpdatedAt = updatedAt
	if err := store.SaveScript(&stale); err != nil {
		t.Fatal(err)
	}
	stored, err = store.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RefKey != durable {
		t.Fatalf("broad save erased script ref key: %q", stored.RefKey)
	}
}

func TestEnsureScenarioRefKeyIsNarrowAndFencesStaleSnapshots(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	script := &dsl.Script{ID: "refkey-scenario-script", Name: "Script", Protocol: "http", ProjectID: DefaultProjectID}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	scenario := &Scenario{ID: "refkey-scenario", Name: "Checkout", Duration: "1m"}
	children := []ScenarioScript{{ScenarioID: scenario.ID, ScriptID: script.ID, VUsers: 3}}
	if err := store.SaveScenario(scenario, children); err != nil {
		t.Fatal(err)
	}
	staleUpdatedAt := scenario.UpdatedAt

	durable, updatedAt, err := store.EnsureScenarioRefKey(scenario.ID, "LOC-SCN-0001")
	if err != nil {
		t.Fatal(err)
	}
	if durable != "LOC-SCN-0001" || !updatedAt.After(staleUpdatedAt) {
		t.Fatalf("EnsureScenarioRefKey = %q, %s; want installed key and newer timestamp", durable, updatedAt)
	}
	stored, storedChildren, err := store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "Checkout" || len(storedChildren) != 1 || storedChildren[0].ScriptID != script.ID || storedChildren[0].VUsers != 3 {
		t.Fatalf("scenario aggregate changed during ref-key backfill: scenario=%+v children=%+v", stored, storedChildren)
	}

	stale := *stored
	stale.RefKey = ""
	stale.Name = "stale overwrite"
	if updated, err := store.UpdateScenarioWithDataPoolBindingsIfUnchanged(&stale, storedChildren, nil, staleUpdatedAt); err != nil || updated {
		t.Fatalf("stale conditional update = %v, %v; want fenced", updated, err)
	}
	winner, winnerUpdatedAt, err := store.EnsureScenarioRefKey(scenario.ID, "LOC-SCN-9999")
	if err != nil {
		t.Fatal(err)
	}
	if winner != durable || !winnerUpdatedAt.Equal(updatedAt) {
		t.Fatalf("second EnsureScenarioRefKey = %q, %s; want %q, %s", winner, winnerUpdatedAt, durable, updatedAt)
	}

	stale.UpdatedAt = updatedAt
	if err := store.SaveScenario(&stale, storedChildren); err != nil {
		t.Fatal(err)
	}
	stored, _, err = store.GetScenario(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RefKey != durable {
		t.Fatalf("broad save erased scenario ref key: %q", stored.RefKey)
	}
}

func TestEnsureRunArchiveRefKeyPreservesPublicationAndHonorsCleanup(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	testRecord := &TestRecord{ID: "refkey-archive-test", Name: "Run", Status: "COMPLETE", ProjectID: DefaultProjectID, StartTime: now}
	if err := store.SaveTest(testRecord); err != nil {
		t.Fatal(err)
	}
	archive := &RunArchiveRecord{
		TestID: testRecord.ID, SchemaVersion: "1", ArchiveBackend: "filesystem",
		ArchivePath: "/archives/run.json", ManifestPath: "/archives/manifest.json",
		TransactionCount: 11, ResourceSampleCount: 7, CreatedAt: now, CompletedAt: now.Add(time.Minute),
	}
	if err := store.SaveRunArchive(archive); err != nil {
		t.Fatal(err)
	}
	durable, err := store.EnsureRunArchiveRefKey(testRecord.ID, "LOC-RUN-0001")
	if err != nil {
		t.Fatal(err)
	}
	if durable != "LOC-RUN-0001" {
		t.Fatalf("EnsureRunArchiveRefKey = %q", durable)
	}
	stored, err := store.GetRunArchive(testRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ArchivePath != archive.ArchivePath || stored.ManifestPath != archive.ManifestPath || stored.TransactionCount != 11 || stored.ResourceSampleCount != 7 {
		t.Fatalf("archive publication changed during ref-key backfill: %+v", stored)
	}

	archive.RefKey = ""
	archive.TransactionCount = 12
	if err := store.SaveRunArchive(archive); err != nil {
		t.Fatal(err)
	}
	stored, err = store.GetRunArchive(testRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RefKey != durable || stored.TransactionCount != 12 {
		t.Fatalf("broad archive save did not preserve ref key: %+v", stored)
	}
	if winner, err := store.EnsureRunArchiveRefKey(testRecord.ID, "LOC-RUN-9999"); err != nil || winner != durable {
		t.Fatalf("second EnsureRunArchiveRefKey = %q, %v; want %q", winner, err, durable)
	}

	created, err := store.SaveArchiveCleanupJobIfArchiveUnchanged(&ArchiveCleanupJob{
		TestID: testRecord.ID, ProjectID: DefaultProjectID, ArchiveRecord: *stored,
	})
	if err != nil || !created {
		t.Fatalf("SaveArchiveCleanupJobIfArchiveUnchanged = %v, %v", created, err)
	}
	if _, err := store.EnsureRunArchiveRefKey(testRecord.ID, "LOC-RUN-0002"); !errors.Is(err, ErrArchiveCleanupPending) {
		t.Fatalf("EnsureRunArchiveRefKey during cleanup = %v; want ErrArchiveCleanupPending", err)
	}
}
