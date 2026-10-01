package api

import (
	"bytes"
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAnalysisBackendMismatchDoesNotDeleteEvidence(t *testing.T) {
	s, _ := newTimeslotServer(t)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	if err := ensureAnalysisBackend(s.store); err != nil {
		t.Fatal(err)
	}
	store := s.store.(storage.AnalysisImportStore)
	in, err := store.SaveAnalysisImport(storage.AnalysisImport{ID: "00000000-0000-0000-0000-000000000002", ProjectID: "proj-a", Name: "source", State: "uploading", Files: []storage.AnalysisImportFile{{ID: "file", RelativePath: "part.parquet", Size: 7}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	key := analysisObjectKey(in.ProjectID, in.ID, "original-file.parquet")
	if _, err = analysisObjectWrite(context.Background(), key, bytes.NewBufferString("fixture")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	r := timeslotRequest(t, s, "DELETE", "/api/v1/analysis/imports/"+in.ID, nil)
	if r.Code != 503 {
		t.Fatalf("mismatched backend accepted deletion: %d %s", r.Code, r.Body.String())
	}
	persisted, err := store.GetAnalysisImport(in.ID)
	if err != nil || persisted.State != "uploading" || persisted.Revision != in.Revision {
		t.Fatalf("rejected deletion changed metadata: %+v %v", persisted, err)
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	if err := ensureAnalysisBackend(s.store); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err = analysisObjectRead(context.Background(), key, &out, 100); err != nil || out.String() != "fixture" {
		t.Fatalf("rejected deletion changed evidence: %q %v", out.String(), err)
	}
}

func TestAnalysisMetadataAndCancellationWithoutNativeCapability(t *testing.T) {
	s, _ := newTimeslotServer(t)
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	store := s.store.(storage.AnalysisImportStore)
	in, err := store.SaveAnalysisImport(storage.AnalysisImport{ID: "00000000-0000-0000-0000-000000000001", ProjectID: "proj-a", Name: "source", State: "uploading", Files: []storage.AnalysisImportFile{{ID: "file", RelativePath: "part.parquet", Size: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	key := analysisObjectKey(in.ProjectID, in.ID, "original-file.parquet")
	if _, err = analysisObjectWrite(context.Background(), key, bytes.NewBufferString("fixture")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/analysis/imports?project_id=proj-a", "/api/v1/analysis/imports/" + in.ID} {
		r := timeslotRequest(t, s, "GET", path, nil)
		if r.Code != 200 {
			t.Fatalf("metadata: %d %s", r.Code, r.Body.String())
		}
	}
	r := timeslotRequest(t, s, "DELETE", "/api/v1/analysis/imports/"+in.ID, nil)
	if r.Code != 204 {
		t.Fatalf("cancel: %d %s", r.Code, r.Body.String())
	}
	var out bytes.Buffer
	if _, err = analysisObjectRead(context.Background(), key, &out, 100); err == nil {
		t.Fatal("cancel retained original")
	}
}
