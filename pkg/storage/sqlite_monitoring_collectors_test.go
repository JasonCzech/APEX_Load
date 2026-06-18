package storage

import (
	"testing"
	"time"
)

func TestMonitoringCollectorRoundTrip(t *testing.T) {
	store := openTimeslotStore(t)
	seen := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	c := &MonitoringCollector{
		ID:                 "collector-1",
		ProjectID:          "proj-a",
		Name:               "edge-eu",
		TokenHash:          "hash-abc",
		Health:             "healthy",
		LastSeenAt:         &seen,
		AllowedMetricPacks: []string{"oracle-db", "host-cpu"},
	}
	if err := store.SaveMonitoringCollector(c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := store.GetMonitoringCollector("collector-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "edge-eu" || got.Health != "healthy" || got.TokenHash != "hash-abc" {
		t.Fatalf("collector not preserved: %+v", got)
	}
	if len(got.AllowedMetricPacks) != 2 || got.AllowedMetricPacks[0] != "oracle-db" {
		t.Fatalf("metric packs not preserved: %+v", got.AllowedMetricPacks)
	}
	if got.LastSeenAt == nil || !got.LastSeenAt.Equal(seen) {
		t.Fatalf("last_seen_at not preserved: %v", got.LastSeenAt)
	}

	// Rotate via upsert: changing the hash must persist.
	got.TokenHash = "hash-xyz"
	if err := store.SaveMonitoringCollector(got); err != nil {
		t.Fatalf("rotate save: %v", err)
	}
	if reread, _ := store.GetMonitoringCollector("collector-1"); reread.TokenHash != "hash-xyz" {
		t.Fatalf("rotated hash not persisted: %s", reread.TokenHash)
	}

	// Project isolation.
	_ = store.SaveMonitoringCollector(&MonitoringCollector{ID: "other", ProjectID: "proj-b", Name: "x", TokenHash: "h"})
	list, err := store.ListMonitoringCollectors("proj-a", 10, 0)
	if err != nil || len(list) != 1 || list[0].ID != "collector-1" {
		t.Fatalf("list = %+v (err %v), want only collector-1", list, err)
	}

	if err := store.DeleteMonitoringCollector("collector-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetMonitoringCollector("collector-1"); err == nil {
		t.Fatal("expected error after delete")
	}
}

// TestMonitoringCollectorNullDefaults proves a NULL last_seen_at scans back to
// nil (the *time.Time pointer path, no sql.NullTime needed) and that an empty
// metric-pack catalog marshals as [] rather than null.
func TestMonitoringCollectorNullDefaults(t *testing.T) {
	store := openTimeslotStore(t)
	if err := store.SaveMonitoringCollector(&MonitoringCollector{
		ID: "collector-nil", ProjectID: "proj-a", Name: "fresh", TokenHash: "h",
		// LastSeenAt nil, AllowedMetricPacks nil.
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := store.GetMonitoringCollector("collector-nil")
	if err != nil {
		t.Fatalf("get with NULL last_seen_at: %v", err)
	}
	if got.LastSeenAt != nil {
		t.Fatalf("NULL last_seen_at should scan to nil, got %v", *got.LastSeenAt)
	}
	if got.AllowedMetricPacks == nil {
		t.Fatal("AllowedMetricPacks should be an empty slice, not nil")
	}
}

func TestUpdateMonitoringCollectorHealth(t *testing.T) {
	store := openTimeslotStore(t)
	if err := store.SaveMonitoringCollector(&MonitoringCollector{
		ID: "collector-hb", ProjectID: "proj-a", Name: "edge", TokenHash: "h",
		AllowedMetricPacks: []string{"linux-hosts"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Initial heartbeat-less state: health unreachable, no last-seen.
	got, _ := store.GetMonitoringCollector("collector-hb")
	if got.Health != "unreachable" || got.LastSeenAt != nil {
		t.Fatalf("initial state = %s/%v, want unreachable/nil", got.Health, got.LastSeenAt)
	}

	seen := time.Date(2026, 7, 2, 10, 30, 0, 0, time.UTC)
	if err := store.UpdateMonitoringCollectorHealth("collector-hb", "healthy", seen); err != nil {
		t.Fatalf("update health: %v", err)
	}
	got, _ = store.GetMonitoringCollector("collector-hb")
	if got.Health != "healthy" || got.LastSeenAt == nil || !got.LastSeenAt.Equal(seen) {
		t.Fatalf("after heartbeat = %s/%v, want healthy/%v", got.Health, got.LastSeenAt, seen)
	}
	// Other fields (name/packs/token) untouched by the targeted update.
	if got.Name != "edge" || len(got.AllowedMetricPacks) != 1 || got.TokenHash != "h" {
		t.Fatalf("targeted update clobbered other fields: %+v", got)
	}

	// Unknown collector → error.
	if err := store.UpdateMonitoringCollectorHealth("missing", "healthy", seen); err == nil {
		t.Fatal("expected error for unknown collector")
	}
}
