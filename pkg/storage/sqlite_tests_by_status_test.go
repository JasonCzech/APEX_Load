package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestListTestsByStatusReturnsOnlyRequestedStatusesNewestFirst(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "tests-by-status.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, record := range []*TestRecord{
		{ID: "running-old", Name: "running old", Status: "RUNNING", StartTime: now.Add(-2 * time.Hour)},
		{ID: "stopping", Name: "stopping", Status: "STOPPING", StartTime: now.Add(-time.Hour)},
		{ID: "running-new", Name: "running new", Status: "RUNNING", StartTime: now},
		{ID: "complete", Name: "complete", Status: "COMPLETE", StartTime: now, EndTime: &now},
		{ID: "queued", Name: "queued", Status: "QUEUED", StartTime: now},
	} {
		if err := store.SaveTest(record); err != nil {
			t.Fatalf("save %s: %v", record.ID, err)
		}
	}

	tests, err := store.ListTestsByStatus([]string{"RUNNING", "STOPPING"})
	if err != nil {
		t.Fatalf("ListTestsByStatus: %v", err)
	}
	var ids []string
	for _, test := range tests {
		ids = append(ids, test.ID)
	}
	want := []string{"running-new", "stopping", "running-old"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	}

	none, err := store.ListTestsByStatus(nil)
	if err != nil || len(none) != 0 {
		t.Fatalf("empty status list = (%v, %v), want no rows", none, err)
	}
}
