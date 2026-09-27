package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type archiveTimestampBackend string

func (b archiveTimestampBackend) BackendInfo() storage.StoreBackendInfo {
	return storage.StoreBackendInfo{Type: string(b)}
}

func TestArchiveRunTimestampMatchesStoragePrecision(t *testing.T) {
	base := time.Date(2026, 9, 26, 21, 48, 2, 696289000, time.UTC)
	for _, tc := range []struct {
		name              string
		requested, stored time.Time
		backend           any
		want              bool
	}{
		{"postgres below half microsecond", base.Add(135 * time.Nanosecond), base, archiveTimestampBackend(storage.StoreBackendPostgres), true},
		{"postgres above half microsecond truncates", base.Add(999 * time.Nanosecond), base, archiveTimestampBackend(storage.StoreBackendPostgres), true},
		{"postgres live identity", base.Add(999 * time.Nanosecond), base.Add(999 * time.Nanosecond), archiveTimestampBackend(storage.StoreBackendPostgres), true},
		{"postgres different microsecond", base.Add(time.Microsecond), base, archiveTimestampBackend(storage.StoreBackendPostgres), false},
		{"postgres previous microsecond", base.Add(-time.Nanosecond), base, archiveTimestampBackend(storage.StoreBackendPostgres), false},
		{"sqlite preserves nanoseconds", base.Add(time.Nanosecond), base, archiveTimestampBackend(storage.StoreBackendSQLite), false},
		{"unknown store preserves nanoseconds", base.Add(time.Nanosecond), base, nil, false},
		{"equivalent timezone", base.In(time.FixedZone("offset", -4*3600)), base, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := archiveRunTimestampMatches(tc.requested, tc.stored, tc.backend); got != tc.want {
				t.Fatalf("matches=%v want %v", got, tc.want)
			}
		})
	}
}

func TestArchiveManifestIdentityWithPostgresPrecision(t *testing.T) {
	created := time.Date(2026, 9, 26, 21, 48, 2, 696289000, time.UTC)
	for _, tc := range []struct {
		name, manifestID, project string
		requested                 time.Time
		want                      bool
	}{
		{"valid", "run", "project", created.Add(135 * time.Nanosecond), true},
		{"wrong test", "another", "project", created, false},
		{"wrong project", "run", "another", created, false},
		{"reused ID", "run", "project", created.Add(-time.Microsecond), false},
		{"legacy omitted timestamp", "run", "project", time.Time{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := &RunArchiveManifest{TestID: tc.manifestID, Provenance: RunArchiveProvenance{ProjectID: tc.project, RequestedAt: tc.requested}}
			record := &storage.TestRecord{ID: "run", ProjectID: "project", StartTime: created}
			if got := runArchiveManifestMatchesStoredTest(manifest, record, archiveTimestampBackend(storage.StoreBackendPostgres)); got != tc.want {
				t.Fatalf("matches=%v want %v", got, tc.want)
			}
		})
	}
	if runArchiveManifestMatchesStoredTest(nil, &storage.TestRecord{}, nil) || runArchiveManifestMatchesStoredTest(&RunArchiveManifest{}, nil, nil) {
		t.Fatal("nil identity accepted")
	}
}
