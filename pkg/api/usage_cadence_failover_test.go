package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type usageHistoryStore struct {
	snapshots []*storage.UsageAccountingSnapshot
	calls     int
}

func (s *usageHistoryStore) SaveUsageAccountingSnapshot(snapshot *storage.UsageAccountingSnapshot) error {
	s.snapshots = append(s.snapshots, snapshot)
	return nil
}

func (s *usageHistoryStore) ListUsageAccountingSnapshots(string, int, int) ([]*storage.UsageAccountingSnapshot, error) {
	s.calls++
	return append([]*storage.UsageAccountingSnapshot(nil), s.snapshots...), nil
}

func TestLatestGlobalUsageSnapshotTimeBoundsCompatibilityScan(t *testing.T) {
	fullPage := make([]*storage.UsageAccountingSnapshot, usageSnapshotLookupPageSize)
	for index := range fullPage {
		fullPage[index] = &storage.UsageAccountingSnapshot{ID: "project", ProjectID: "project-a", GeneratedAt: time.Now().UTC()}
	}
	store := &usageHistoryStore{snapshots: fullPage}
	runner := &TestRunner{usageStore: store}
	got, err := runner.latestGlobalUsageSnapshotTime()
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsZero() {
		t.Fatalf("latest global snapshot = %s, want zero when marker is outside bounded scan", got)
	}
	if store.calls != maxUsageSnapshotLookupPages {
		t.Fatalf("snapshot list calls = %d, want %d", store.calls, maxUsageSnapshotLookupPages)
	}
}

func TestLatestGlobalUsageSnapshotTimeIgnoresProjectSnapshots(t *testing.T) {
	global := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	store := &usageHistoryStore{snapshots: []*storage.UsageAccountingSnapshot{
		{ID: "project", ProjectID: "project-a", GeneratedAt: global.Add(time.Minute)},
		{ID: "global", GeneratedAt: global},
	}}
	runner := &TestRunner{usageStore: store}
	got, err := runner.latestGlobalUsageSnapshotTime()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(global) {
		t.Fatalf("latest global snapshot = %s, want %s", got, global)
	}
}
