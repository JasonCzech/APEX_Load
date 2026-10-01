package storage

import (
	"testing"
	"time"
)

func TestAnalysisCleanupOutlivesProjectWithGrace(t *testing.T) {
	s, _, now := monitorFixture(t)
	in, err := s.SaveAnalysisImport(AnalysisImport{ID: "00000000-0000-0000-0000-000000000001", ProjectID: "monitor-p", Name: "data", State: "uploading", Files: []AnalysisImportFile{{ID: "f", Size: 12, RelativePath: "f.parquet"}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	in.State = "cancelled"
	if _, err = s.SaveAnalysisImport(*in, in.Revision); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteProject("monitor-p"); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListAnalysisCleanupJobs(now.Add(time.Minute), 50)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("early cleanup: %+v %v", jobs, err)
	}
	jobs, err = s.ListAnalysisCleanupJobs(now.Add(time.Hour), 50)
	if err != nil || len(jobs) == 0 {
		t.Fatalf("lost deletion intent: %+v %v", jobs, err)
	}
	for _, offset := range []int{-7 * 3600, 9 * 3600} {
		local := now.Add(time.Hour).In(time.FixedZone("test", offset))
		localJobs, err := s.ListAnalysisCleanupJobs(local, 50)
		if err != nil || len(localJobs) != len(jobs) {
			t.Fatalf("cleanup deadline changed with timezone: %+v %v", localJobs, err)
		}
	}
	found := false
	for _, j := range jobs {
		if j.Kind == "dataset" && j.ResourceID == in.ID {
			found = true
		}
		if err = s.CompleteAnalysisCleanupJob(j); err != nil {
			t.Fatal(err)
		}
	}
	if !found {
		t.Fatal("dataset cleanup missing")
	}
	jobs, err = s.ListAnalysisCleanupJobs(now.Add(time.Hour), 50)
	if err != nil || len(jobs) != 0 {
		t.Fatal("completed jobs retained")
	}
}
