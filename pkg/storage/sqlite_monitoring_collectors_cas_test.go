package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMonitoringCollectorTokenRotationCASRejectsStaleCredential(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "collector-rotation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	project := &Project{ID: "project-a", Name: "Project A", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	collector := &MonitoringCollector{
		ID: "collector-a", ProjectID: project.ID, Name: "Collector A", TokenHash: "old-hash", Health: "unreachable",
	}
	if err := store.SaveMonitoringCollector(collector); err != nil {
		t.Fatal(err)
	}

	updated, err := store.UpdateMonitoringCollectorTokenHashIfUnchanged(collector.ID, project.ID, "winning-hash", "old-hash")
	if err != nil || !updated {
		t.Fatalf("winning rotation = %v, %v; want true, nil", updated, err)
	}
	updated, err = store.UpdateMonitoringCollectorTokenHashIfUnchanged(collector.ID, project.ID, "stale-hash", "old-hash")
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("stale rotation unexpectedly replaced the winning credential")
	}
	stored, err := store.GetMonitoringCollector(collector.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.TokenHash != "winning-hash" {
		t.Fatalf("stored token hash = %q, want winning-hash", stored.TokenHash)
	}
}
