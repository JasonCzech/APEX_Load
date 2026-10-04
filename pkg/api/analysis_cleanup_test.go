package api

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAnalysisNamespaceCleanupIsScoped(t *testing.T) {
	_, _ = newTimeslotServer(t)
	ctx := context.Background()
	id := "00000000-0000-0000-0000-000000000001"
	a, b := analysisObjectKey("proj-a", id, "part.parquet"), analysisObjectKey("proj-b", id, "part.parquet")
	for _, key := range []string{a, b} {
		if _, err := analysisObjectWrite(ctx, key, bytes.NewBufferString("fixture")); err != nil {
			t.Fatal(err)
		}
	}
	if err := deleteAnalysisNamespace(ctx, storage.AnalysisCleanupJob{Kind: "dataset", ProjectID: "proj-a", ResourceID: id}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := analysisObjectRead(ctx, a, &out, 100); err == nil {
		t.Fatal("deleted project artifact retained")
	}
	if _, err := analysisObjectRead(ctx, b, &out, 100); err != nil {
		t.Fatal("other project artifact deleted", err)
	}
	if err := deleteAnalysisNamespace(ctx, storage.AnalysisCleanupJob{Kind: "dataset", ProjectID: "proj-a", ResourceID: "../outside"}); err == nil {
		t.Fatal("untrusted path accepted")
	}
}

type analysisCleanupReadFailureStore struct {
	*storage.SQLiteStore
	failRead  bool
	completed int
}

func (s *analysisCleanupReadFailureStore) ListAnalysisCleanupJobs(now time.Time, limit int) ([]storage.AnalysisCleanupJob, error) {
	return s.SQLiteStore.ListAnalysisCleanupJobs(now.Add(time.Hour), limit)
}
func (s *analysisCleanupReadFailureStore) GetAnalysisImport(id string) (*storage.AnalysisImport, error) {
	if s.failRead {
		return nil, errors.New("temporary metadata read failure")
	}
	return s.SQLiteStore.GetAnalysisImport(id)
}
func (s *analysisCleanupReadFailureStore) CompleteAnalysisCleanupJob(job storage.AnalysisCleanupJob) error {
	if job.Kind == "dataset" {
		s.completed++
	}
	return s.SQLiteStore.CompleteAnalysisCleanupJob(job)
}

func TestAnalysisCleanupRetriesMetadataReadFailure(t *testing.T) {
	_, durable := newTimeslotServer(t)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	store := &analysisCleanupReadFailureStore{SQLiteStore: durable.(*storage.SQLiteStore)}
	in, err := store.SaveAnalysisImport(storage.AnalysisImport{ID: "00000000-0000-0000-0000-000000000003", ProjectID: "proj-a", Name: "data", State: "uploading", Files: []storage.AnalysisImportFile{{ID: "f", RelativePath: "f.parquet", Size: 12, Uploaded: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	in = publishAnalysisAPIFixture(t, store, in, nil)
	in.State = "deleting"
	in, err = store.SaveAnalysisImport(*in, in.Revision)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store}
	store.failRead = true
	if err = server.retryAnalysisCleanup(context.Background()); err == nil || store.completed != 0 {
		t.Fatalf("metadata failure lost retry: completed=%d error=%v", store.completed, err)
	}
	jobs, err := store.ListAnalysisCleanupJobs(time.Now(), 50)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("missing retry: %+v %v", jobs, err)
	}
	store.failRead = false
	for range 2 {
		if err = server.retryAnalysisCleanup(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if in, err = store.GetAnalysisImport(in.ID); err != nil || in.State != "deleted" {
		t.Fatalf("deletion did not recover: %+v %v", in, err)
	}
	if jobs, err = store.ListAnalysisCleanupJobs(time.Now(), 50); err != nil || len(jobs) != 0 {
		t.Fatalf("recovered cleanup retained jobs: %+v %v", jobs, err)
	}
}
