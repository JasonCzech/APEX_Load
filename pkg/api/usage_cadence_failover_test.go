package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type usageHistoryStore struct {
	snapshots []*storage.UsageAccountingSnapshot
}

func (s *usageHistoryStore) SaveUsageAccountingSnapshot(snapshot *storage.UsageAccountingSnapshot) error {
	s.snapshots = append(s.snapshots, snapshot)
	return nil
}

func (s *usageHistoryStore) ListUsageAccountingSnapshots(string, int, int) ([]*storage.UsageAccountingSnapshot, error) {
	return append([]*storage.UsageAccountingSnapshot(nil), s.snapshots...), nil
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
