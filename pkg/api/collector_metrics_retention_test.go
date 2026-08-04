package api

import (
	"testing"
	"time"
)

func TestCollectorMetricStoreEvictsDeletedAndExpiredSnapshots(t *testing.T) {
	store := newCollectorMetricStore()
	now := time.Now().UTC()
	store.put(collectorMetricSnapshot{CollectorID: "expired", ReceivedAt: now.Add(-collectorSnapshotRetention - time.Second)})
	store.put(collectorMetricSnapshot{CollectorID: "current", ReceivedAt: now})
	if _, ok := store.get("expired"); ok {
		t.Fatal("expired collector snapshot survived the heartbeat sweep")
	}
	if _, ok := store.get("current"); !ok {
		t.Fatal("current collector snapshot was swept")
	}
	store.delete("current")
	if _, ok := store.get("current"); ok {
		t.Fatal("deleted collector snapshot remained resident")
	}
}
