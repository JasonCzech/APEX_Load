package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

type nativeQueryMetadataBarrier struct {
	SQLiteStore  *storage.SQLiteStore
	entered      chan struct{}
	release      chan struct{}
	blockProject bool
	blockDataset bool
	once         sync.Once
	armed        atomic.Bool
}

func (s *nativeQueryMetadataBarrier) barrier() { s.once.Do(func() { close(s.entered); <-s.release }) }
func (s *nativeQueryMetadataBarrier) GetProject(id string) (*storage.Project, error) {
	if s.armed.Load() && s.blockProject {
		s.barrier()
	}
	return s.SQLiteStore.GetProject(id)
}
func (s *nativeQueryMetadataBarrier) GetAnalysisImport(id string) (*storage.AnalysisImport, error) {
	if s.armed.Load() && s.blockDataset {
		s.barrier()
	}
	return s.SQLiteStore.GetAnalysisImport(id)
}

type nativeQueryDetachedDeadline struct {
	context.Context
	deadline time.Time
}

func (c nativeQueryDetachedDeadline) Deadline() (time.Time, bool) { return c.deadline, true }
func (c nativeQueryDetachedDeadline) Done() <-chan struct{}       { return nil }
func (c nativeQueryDetachedDeadline) Err() error                  { return nil }

func nativeQueryAuthorityFixture(t *testing.T, wrappers ...func(*storage.SQLiteStore) storage.Store) (*Server, *storage.SQLiteStore, *storage.AnalysisImport) {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir() + "/query-authority.db")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "proj-a", Name: "Query project", CreatedAt: now, UpdatedAt: now}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	configured := storage.Store(store)
	if len(wrappers) > 1 {
		store.Close()
		t.Fatal("invalid fixture wrappers")
	}
	if len(wrappers) == 1 {
		configured = wrappers[0](store)
	}
	s := NewServer(ServerConfig{Store: configured, MaxConcurrent: 2})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error("retaining query fixture store after unjoined shutdown", err)
			return
		}
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	in, err := store.SaveAnalysisImport(storage.AnalysisImport{ID: "00000000-0000-0000-0000-000000000006", ProjectID: "proj-a", Name: "current-query", State: "uploading", Files: []storage.AnalysisImportFile{{ID: "file", RelativePath: "fixture.parquet", Size: 12, Uploaded: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	in = publishAnalysisAPIFixture(t, store, in, nil)
	return s, store, in
}

func TestNativeDatasetSeriesFinalAdmissionRefusesDeletionRevisionAndForeignScope(t *testing.T) {
	for _, change := range []string{"cancel", "revision", "project", "foreign"} {
		t.Run(change, func(t *testing.T) {
			s, store, in := nativeQueryAuthorityFixture(t)
			if change == "cancel" {
				current := *in
				current.State = "deleting"
				if _, err := store.SaveAnalysisImport(current, current.Revision); err != nil {
					t.Fatal(err)
				}
			}
			if change == "revision" {
				current := *in
				current.Name = "later metadata edit"
				if _, err := store.SaveAnalysisImport(current, current.Revision); err != nil {
					t.Fatal(err)
				}
			}
			if change == "project" {
				if err := store.DeleteProject(in.ProjectID); err != nil {
					t.Fatal(err)
				}
			}
			r := httptest.NewRequest(http.MethodPost, "/api/v1/analysis/datasets/"+in.ID+"/series", nil)
			if change == "foreign" {
				bindAuthorizedProjectToRequest(r, "proj-b")
			} else {
				bindAuthorizedProjectToRequest(r, in.ProjectID)
			}
			value := 42.0
			points := []analysis.MetricPoint{{Value: &value, MetricName: "never-release-stale-result"}}
			w := httptest.NewRecorder()
			if s.writeNativeDatasetSeries(w, r, in, points) || w.Code == http.StatusOK {
				t.Fatal("stale/foreign completed query served", w.Code, w.Body.String())
			}
			if w.Body.String() == "" {
				t.Fatal("refusal missing")
			}
		})
	}
}

func TestNativeDatasetFinalMetadataReadBarrierObservesCurrentRevision(t *testing.T) {
	var owner *nativeQueryMetadataBarrier
	s, store, in := nativeQueryAuthorityFixture(t, func(store *storage.SQLiteStore) storage.Store {
		owner = &nativeQueryMetadataBarrier{SQLiteStore: store, entered: make(chan struct{}), release: make(chan struct{}), blockDataset: true}
		return sqliteOverlay(owner)
	})
	owner.armed.Store(true)
	var released sync.Once
	done := make(chan error, 1)
	joined := false
	t.Cleanup(func() {
		released.Do(func() { close(owner.release) })
		if !joined {
			select {
			case <-done:
				joined = true
			case <-time.After(5 * time.Second):
				t.Error("owned metadata read did not join")
			}
		}
	})
	go func() { done <- s.verifyNativeDatasetQuery(t.Context(), in.ProjectID, in) }()
	select {
	case <-owner.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("metadata read not admitted")
	}
	current := *in
	current.Name = "concurrent metadata revision"
	if _, err := store.SaveAnalysisImport(current, current.Revision); err != nil {
		t.Fatal(err)
	}
	released.Do(func() { close(owner.release) })
	select {
	case err := <-done:
		joined = true
		if err == nil {
			t.Fatal("later revision served")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("metadata barrier did not join")
	}
}

func TestNativeComparisonAndSeriesRefuseLateContextFreeMetadataReplies(t *testing.T) {
	for _, stage := range []string{"project", "dataset"} {
		t.Run(stage, func(t *testing.T) {
			var owner *nativeQueryMetadataBarrier
			s, _, in := nativeQueryAuthorityFixture(t, func(store *storage.SQLiteStore) storage.Store {
				owner = &nativeQueryMetadataBarrier{SQLiteStore: store, entered: make(chan struct{}), release: make(chan struct{}), blockProject: stage == "project", blockDataset: stage == "dataset"}
				return sqliteOverlay(owner)
			})
			owner.armed.Store(true)
			deadline := time.Now().Add(30 * time.Millisecond)
			ctx := nativeQueryDetachedDeadline{Context: context.Background(), deadline: deadline}
			plan := &analysisComparisonPipeline{plans: map[analysisDatasetQueryIdentity]analysisDatasetQueryPlan{{dataset: in.ID, metric: "value"}: {dataset: in}}}
			var released sync.Once
			done := make(chan error, 1)
			joined := false
			t.Cleanup(func() {
				released.Do(func() { close(owner.release) })
				if !joined {
					select {
					case <-done:
						joined = true
					case <-time.After(5 * time.Second):
						t.Error("owned late reply did not join")
					}
				}
			})
			go func() {
				done <- s.verifyNativeAnalysisComparison(context.WithValue(ctx, analysisComparisonPipelineKey{}, plan), in.ProjectID)
			}()
			select {
			case <-owner.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("late-read barrier not reached")
			}
			timer := time.NewTimer(time.Until(deadline) + time.Millisecond)
			<-timer.C
			released.Do(func() { close(owner.release) })
			select {
			case err := <-done:
				joined = true
				if err == nil {
					t.Fatal("expired context-free reply admitted")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("late reply not joined")
			}
			r := httptest.NewRequest(http.MethodPost, "/api/v1/analysis/datasets/"+in.ID+"/series", nil).WithContext(ctx)
			w := httptest.NewRecorder()
			if s.writeNativeDatasetSeries(w, r, in, nil) || w.Code == http.StatusOK {
				t.Fatal("expired completed series served", w.Code)
			}
		})
	}
}

func TestNativeDatasetSeriesFinalAdmissionAcceptsExactCurrentSnapshot(t *testing.T) {
	s, _, in := nativeQueryAuthorityFixture(t)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/analysis/datasets/"+in.ID+"/series", nil)
	bindAuthorizedProjectToRequest(r, in.ProjectID)
	w := httptest.NewRecorder()
	if !s.writeNativeDatasetSeries(w, r, in, []analysis.MetricPoint{}) || w.Code != http.StatusOK {
		t.Fatal("owned current query refused", w.Code, w.Body.String())
	}
}
