package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

func nativePreviewAuthorityFixture(t *testing.T, state string, wrappers ...func(*storage.SQLiteStore) storage.Store) (*Server, *storage.SQLiteStore, *storage.AnalysisImport) {
	t.Helper()
	s, store, ready := nativeQueryAuthorityFixture(t, wrappers...)
	if state == "ready" {
		return s, store, ready
	}
	in, err := store.SaveAnalysisImport(storage.AnalysisImport{ID: "00000000-0000-0000-0000-000000000007", ProjectID: ready.ProjectID, Name: "preview-query", State: "uploading", Files: []storage.AnalysisImportFile{{ID: "file", RelativePath: "preview.parquet", Size: 12, Uploaded: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if state == "uploading" {
		return s, store, in
	}
	in.State, in.Mapping = "mapped", ready.Mapping
	in, err = store.SaveAnalysisImport(*in, in.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if state == "mapped" {
		return s, store, in
	}
	// This metadata-only fixture admits no native child, source reader or writer.
	// It can record exact absence of fixture writers without fabricating runtime
	// proof for a real import, then reach the durable failed state.
	in, _, err = store.ClaimAnalysisImportAttempt(t.Context(), in.ID, in.ProjectID, in.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if state == "normalizing" {
		return s, store, in
	}
	if err = store.ConfirmAnalysisAttemptWritersQuiesced(t.Context(), in.ID, in.ProjectID, in.Owner); err != nil {
		t.Fatal(err)
	}
	if err = store.FailAnalysisImportAttempt(t.Context(), in.ID, in.ProjectID, in.Owner, in.Revision); err != nil {
		t.Fatal(err)
	}
	in, err = store.GetAnalysisImport(in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if in.State != state {
		t.Fatal("fixture did not reach intended state", in.State, state)
	}
	return s, store, in
}
func nativePreviewRequest(in *storage.AnalysisImport, ctx context.Context) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/analysis/imports/"+in.ID+"/files/file/preview", nil).WithContext(ctx)
	bindAuthorizedProjectToRequest(r, in.ProjectID)
	return r
}
func nativePreviewAuthorityResult() analytics.Preview {
	return analytics.Preview{Samples: []map[string]any{{"proof": "private-preview-canary"}}}
}
func TestNativePreviewFinalAdmissionAcceptsExactAvailableStates(t *testing.T) {
	for _, state := range []string{"uploading", "mapped", "failed", "ready"} {
		t.Run(state, func(t *testing.T) {
			s, _, in := nativePreviewAuthorityFixture(t, state)
			w := httptest.NewRecorder()
			if !s.writeNativeAnalysisPreview(w, nativePreviewRequest(in, t.Context()), in, nativePreviewAuthorityResult()) || w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "private-preview-canary") {
				t.Fatal("available owned preview refused", w.Code, w.Body.String())
			}
		})
	}
}
func TestNativePreviewFinalAdmissionRefusesRevocationForeignScopeAndNormalizing(t *testing.T) {
	for _, change := range []string{"cancelled", "deleting", "deleted", "association", "revision", "project", "foreign", "normalizing"} {
		t.Run(change, func(t *testing.T) {
			state := "uploading"
			if change == "normalizing" {
				state = "normalizing"
			} else if change == "deleting" || change == "deleted" || change == "association" {
				state = "ready"
			}
			s, store, in := nativePreviewAuthorityFixture(t, state)
			switch change {
			case "cancelled":
				current := *in
				current.State = "cancelled"
				if _, err := store.SaveAnalysisImport(current, current.Revision); err != nil {
					t.Fatal(err)
				}
			case "deleting", "deleted":
				current := *in
				current.State = "deleting"
				saved, err := store.SaveAnalysisImport(current, current.Revision)
				if err != nil {
					t.Fatal(err)
				}
				if change == "deleted" {
					saved.State = "deleted"
					if _, err = store.SaveAnalysisImport(*saved, saved.Revision); err != nil {
						t.Fatal(err)
					}
				}
			case "association":
				if err := store.SaveTest(&storage.TestRecord{ID: "preview-associated-run", ProjectID: in.ProjectID, Name: "preview association", Status: "COMPLETE", StartTime: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
				current := *in
				current.RunID = "preview-associated-run"
				if _, err := store.SaveAnalysisImport(current, current.Revision); err != nil {
					t.Fatal(err)
				}
			case "revision":
				current := *in
				current.Name = "later upload metadata"
				if _, err := store.SaveAnalysisImport(current, current.Revision); err != nil {
					t.Fatal(err)
				}
			case "project":
				if err := store.DeleteProject(in.ProjectID); err != nil {
					t.Fatal(err)
				}
			}
			r := nativePreviewRequest(in, t.Context())
			if change == "foreign" {
				bindAuthorizedProjectToRequest(r, "proj-b")
			}
			w := httptest.NewRecorder()
			if s.writeNativeAnalysisPreview(w, r, in, nativePreviewAuthorityResult()) || w.Code == http.StatusOK || strings.Contains(w.Body.String(), "private-preview-canary") {
				t.Fatal("stale preview was disclosed", w.Code, w.Body.String())
			}
		})
	}
}
func TestNativePreviewFinalMetadataBarrierRefusesConcurrentRevisionAndLateReply(t *testing.T) {
	for _, stage := range []string{"revision", "project-deadline", "dataset-deadline"} {
		t.Run(stage, func(t *testing.T) {
			var owner *nativeQueryMetadataBarrier
			s, store, in := nativePreviewAuthorityFixture(t, "uploading", func(store *storage.SQLiteStore) storage.Store {
				owner = &nativeQueryMetadataBarrier{SQLiteStore: store, entered: make(chan struct{}), release: make(chan struct{}), blockProject: stage == "project-deadline", blockDataset: stage != "project-deadline"}
				return sqliteOverlay(owner)
			})
			owner.armed.Store(true)
			ctx := t.Context()
			if stage != "revision" {
				ctx = nativeQueryDetachedDeadline{Context: t.Context(), deadline: time.Now().Add(time.Second)}
			}
			r := nativePreviewRequest(in, ctx)
			w := httptest.NewRecorder()
			releaseOnce := sync.Once{}
			release := func() { releaseOnce.Do(func() { close(owner.release) }) }
			done, joined := make(chan bool, 1), make(chan struct{})
			t.Cleanup(func() {
				release()
				select {
				case <-joined:
				case <-time.After(5 * time.Second):
					t.Error("exact preview metadata continuation did not join")
				}
			})
			go func() {
				defer close(joined)
				done <- s.writeNativeAnalysisPreview(w, r, in, nativePreviewAuthorityResult())
			}()
			select {
			case <-owner.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("preview metadata boundary not reached")
			}
			if stage == "revision" {
				current := *in
				current.Name = "concurrent upload metadata"
				if _, err := store.SaveAnalysisImport(current, current.Revision); err != nil {
					t.Fatal(err)
				}
			} else {
				end, _ := ctx.Deadline()
				timer := time.NewTimer(max(0, time.Until(end)) + time.Millisecond)
				<-timer.C
				timer.Stop()
			}
			release()
			select {
			case accepted := <-done:
				if accepted || w.Code == http.StatusOK || strings.Contains(w.Body.String(), "private-preview-canary") {
					t.Fatal("late preview metadata authority admitted", w.Code, w.Body.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("preview metadata did not return after exact release")
			}
			<-joined
		})
	}
}
