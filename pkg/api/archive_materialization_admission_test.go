package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestArchiveActiveBudgetRetainedUntilLastReader(t *testing.T) {
	m := newTestArchiveMaterializationCoordinator(t)
	reserve := func(key string) *archiveMaterializationEntry {
		t.Helper()
		e, err := m.reserve(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	first := reserve("first")
	if err := m.admitActiveBytes(context.Background(), first, 10, 10); err != nil {
		t.Fatal(err)
	}
	a := m.promoteReservation("first", "", "", first)
	defer a.Release()
	shared := reserve("first")
	if err := m.admitActiveBytes(context.Background(), shared, 10, 10); err != nil {
		t.Fatal(err)
	}
	b := m.promoteReservation("first", "", "", shared)
	defer b.Release()
	a.Release()
	other := reserve("other")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.admitActiveBytes(ctx, other, 1, 10); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("admission=%v", err)
	}
	m.failReservation(other)
	next := reserve("next")
	done := make(chan error, 1)
	go func() { done <- m.admitActiveBytes(context.Background(), next, 10, 10) }()
	b.Release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("last reader did not wake admission")
	}
	m.failReservation(next)
	a.Release()
	b.Release()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeBytes != 0 || len(m.entries) != 0 {
		t.Fatalf("leaked budget=%d entries=%d", m.activeBytes, len(m.entries))
	}
}

func TestArchiveActiveBudgetRejectsInvalidAndChangedBounds(t *testing.T) {
	for _, tc := range []struct {
		name         string
		bytes, limit int64
		valid        bool
	}{
		{"exact", 1, 1, true}, {"oversized", 2, 1, false}, {"zero", 0, 1, false}, {"negative", -1, 1, false}, {"no budget", 1, 0, false}, {"maximum", math.MaxInt64, math.MaxInt64, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestArchiveMaterializationCoordinator(t)
			e, _ := m.reserve(context.Background(), "key")
			defer m.failReservation(e)
			if err := m.admitActiveBytes(context.Background(), e, tc.bytes, tc.limit); (err == nil) != tc.valid {
				t.Fatalf("admission=%v", err)
			}
			if tc.valid && tc.bytes != 2 {
				if err := m.admitActiveBytes(context.Background(), e, 2, math.MaxInt64); err == nil {
					t.Fatal("changed frozen budget")
				}
			}
		})
	}
	m := newTestArchiveMaterializationCoordinator(t)
	e, _ := m.reserve(context.Background(), "canceled")
	defer m.failReservation(e)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.admitActiveBytes(ctx, e, 1, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled=%v", err)
	}
}

func TestArchiveActiveReaderLimitConfiguration(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "bad", "9223372036854775808", "1024"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envArchiveActiveReaderMaxBytes, value)
			want := defaultArchiveMaterializationCacheBytes
			if value == "1024" {
				want = 1024
			}
			if got := configuredArchiveActiveReaderMaxBytes(); got != want {
				t.Fatalf("limit=%d", got)
			}
		})
	}
}

func TestArchiveActiveBudgetStopsThirdColdReadBeforeNetwork(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
	t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "Active-Readers")
	t.Setenv("APEXLOAD_ARCHIVE_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_REQUEST_CHECKSUM_CALCULATION", "when_required")
	t.Setenv("AWS_RESPONSE_CHECKSUM_VALIDATION", "when_required")
	t.Setenv(envArchiveMaterializedMaxBytes, "4096")
	t.Setenv(envArchiveActiveReaderMaxBytes, "8192")
	objects := map[string][]byte{}
	counts := map[string]int{}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		data, ok := objects[r.URL.Path]
		counts[r.URL.Path]++
		mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
	defer server.Close()
	t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", server.URL)
	var records []*storage.RunArchiveRecord
	var paths []string
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("active-%d", i)
		data, _ := json.Marshal(RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: id})
		backend := currentArchiveBackend()
		path := "/Active-Readers/" + backend.objectKey(id, runArchiveManifestFileName)
		objects[path] = data
		paths = append(paths, path)
		records = append(records, &storage.RunArchiveRecord{TestID: id, ArchiveBackend: archiveBackendS3, ArchiveURI: backend.runURI(id), ManifestURI: backend.manifestURI(id), ManifestSHA256: sha256BytesHex(data)})
	}
	a, err := acquireArchiveMaterialized(context.Background(), records[0])
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b, err := acquireArchiveMaterialized(context.Background(), records[1])
	if err != nil {
		t.Fatal(err)
	}
	defer b.Release()
	// Reading the same cache shares its existing slot even when the budget is full.
	shared, err := acquireArchiveMaterialized(context.Background(), records[0])
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := acquireArchiveMaterialized(ctx, records[2]); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("third read=%v", err)
	}
	mu.Lock()
	calls := counts[paths[2]]
	mu.Unlock()
	if calls != 0 {
		t.Fatal("unadmitted read reached object store")
	}
	a.Release()
	shared.Release()
	c, err := acquireArchiveMaterialized(context.Background(), records[2])
	if err != nil {
		t.Fatal(err)
	}
	c.Release()
	b.Release()
	// A previously valid cache larger than a newly configured bound is not a
	// cache-hit bypass. The bounded repair attempt also releases its admission.
	t.Setenv(envArchiveMaterializedMaxBytes, "1")
	if lease, err := acquireArchiveMaterialized(context.Background(), records[0]); err == nil {
		lease.Release()
		t.Fatal("oversized old cache bypassed reader admission")
	}
	archiveMaterializations.mu.Lock()
	used := archiveMaterializations.activeBytes
	archiveMaterializations.mu.Unlock()
	if used != 0 {
		t.Fatalf("leaked active reader budget=%d", used)
	}
}

func TestArchiveAdmittedCacheChecksActualLegacyPayload(t *testing.T) {
	root := t.TempDir()
	artifact := []byte("legacy payload")
	manifest := RunArchiveManifest{TestID: "legacy", TransactionsFile: "transactions.jsonl"}
	data, _ := json.Marshal(manifest)
	manifestPath := filepath.Join(root, runArchiveManifestFileName)
	if err := os.WriteFile(manifestPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "transactions.jsonl"), artifact, 0600); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{TestID: "legacy"}
	size := int64(len(data) + len(artifact))
	if err := validateAdmittedArchiveCache(context.Background(), record, root, manifestPath, size); err != nil {
		t.Fatal(err)
	}
	if err := validateAdmittedArchiveCache(context.Background(), record, root, manifestPath, size-1); err == nil {
		t.Fatal("accepted cached payload larger than frozen admission")
	}
	if err := validateMaterializedArchive(record, root, manifestPath); err != nil {
		t.Fatalf("reader admission constrained producer validation: %v", err)
	}
}

func TestArchiveActiveBudgetComparisonsWithOneArchiveSlot(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
	t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "One-Slot")
	t.Setenv("APEXLOAD_ARCHIVE_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_REQUEST_CHECKSUM_CALCULATION", "when_required")
	t.Setenv("AWS_RESPONSE_CHECKSUM_VALIDATION", "when_required")
	t.Setenv(envArchiveMaterializedMaxBytes, "4096")
	t.Setenv(envArchiveActiveReaderMaxBytes, "4096")
	// Evict each idle cache immediately: reports must finish file access before release.
	t.Setenv(envArchiveMaterializationCacheMaxBytes, "1")
	t.Setenv(envArchiveMaterializeTimeout, "2s")
	objects := map[string][]byte{}
	objectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := objects[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
	defer objectServer.Close()
	t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", objectServer.URL)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveApplication(&storage.Application{ID: "one-slot-app", ProjectID: storage.DefaultProjectID, Name: "Reader admission"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRelease(&storage.Release{ID: "one-slot-release", ApplicationID: "one-slot-app", ProjectID: storage.DefaultProjectID, Name: "1.0"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	backend := currentArchiveBackend()
	for _, id := range []string{"one-slot-a", "one-slot-b", "one-slot-c"} {
		manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: id, Status: "COMPLETE", CreatedAt: now, CompletedAt: now, Provenance: RunArchiveProvenance{ProjectID: storage.DefaultProjectID, RequestedAt: now, ReleaseID: "one-slot-release"}, TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "summaries.jsonl"}
		for _, name := range []string{manifest.TransactionsFile, manifest.ResourceSamplesFile, manifest.IntervalSummariesFile} {
			objects["/One-Slot/"+backend.objectKey(id, name)] = []byte{}
			manifest.Artifacts = append(manifest.Artifacts, RunArchiveArtifact{Name: name, SHA256: sha256BytesHex(nil)})
		}
		data, _ := json.Marshal(manifest)
		objects["/One-Slot/"+backend.objectKey(id, runArchiveManifestFileName)] = data
		if err := store.SaveTest(&storage.TestRecord{ID: id, Name: id, ProjectID: storage.DefaultProjectID, Status: "COMPLETE", StartTime: now, EndTime: &now, ApplicationID: "one-slot-app", ReleaseID: "one-slot-release"}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveRunArchive(&storage.RunArchiveRecord{TestID: id, ArchiveBackend: archiveBackendS3, ArchiveURI: backend.runURI(id), ManifestURI: backend.manifestURI(id), ManifestSHA256: sha256BytesHex(data), CreatedAt: now, CompletedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	server := NewServer(ServerConfig{Store: store})
	for _, path := range []string{
		"/api/v1/tests/one-slot-a/archive/analysis?baseline_id=one-slot-b",
		"/api/v1/tests/one-slot-a/archive/comparison?baseline_id=one-slot-b&breakdown=overall",
		"/api/v1/tests/one-slot-a/archive/comparison?comparison_ids=one-slot-b,one-slot-c&breakdown=overall",
		"/api/v1/tests/one-slot-a/archive/report?profile=audit",
		"/api/v1/tests/one-slot-a/archive/report?profile=%20AuDiT%20",
		"/api/v1/tests/one-slot-a/report?profile=audit",
		"/api/v1/tests/one-slot-a/archive/report?profile=executive",
		"/api/v1/tests/one-slot-a/report?profile=executive",
	} {
		t.Run(path, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
			if strings.Contains(path, "/archive/report?") && strings.EqualFold(strings.TrimSpace(request.URL.Query().Get("profile")), "audit") {
				var report RunArchiveReport
				if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				if report.Integrity == nil || report.Integrity.Status != integrityStatusVerified {
					t.Fatalf("audit lost verified integrity: %+v", report.Integrity)
				}
			}
			if ctx.Err() != nil {
				t.Fatal("comparison waited for its own pinned cache")
			}
			archiveMaterializations.mu.Lock()
			used := archiveMaterializations.activeBytes
			archiveMaterializations.mu.Unlock()
			if used != 0 {
				t.Fatalf("handler retained %d bytes", used)
			}
		})
	}
}
