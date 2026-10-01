package storage

import (
	"testing"
	"time"
)

func TestAnalysisImportLifecycleAndAssociation(t *testing.T) {
	s, _, now := monitorFixture(t)
	in := AnalysisImport{ID: "import", ProjectID: "monitor-p", Name: "dataset", State: "ready", Files: []AnalysisImportFile{{ID: "file", RelativePath: "part.parquet", Size: 20}}}
	if _, err := s.SaveAnalysisImport(in, 0); err == nil {
		t.Fatal("published without import")
	}
	in.State = "uploading"
	p, err := s.SaveAnalysisImport(in, 0)
	if err != nil {
		t.Fatal(err)
	}
	changed := *p
	changed.State = "ready"
	if _, err = s.SaveAnalysisImport(changed, p.Revision); err == nil {
		t.Fatal("skipped normalization")
	}
	changed = *p
	changed.Files = append([]AnalysisImportFile(nil), p.Files...)
	changed.Files[0].Size = 30
	if _, err = s.SaveAnalysisImport(changed, p.Revision); err == nil {
		t.Fatal("changed immutable file")
	}
	if err = s.SaveTest(&TestRecord{ID: "assoc-run", ProjectID: "monitor-p", Name: "run", Status: "QUEUED", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"mapped", "normalizing", "ready"} {
		p.State = state
		p, err = s.SaveAnalysisImport(*p, p.Revision)
		if err != nil {
			t.Fatal(err)
		}
	}
	changed = *p
	changed.Normalized = []string{"foreign-artifact"}
	if _, err = s.SaveAnalysisImport(changed, p.Revision); err == nil {
		t.Fatal("mutated published artifact list")
	}
	p.RunID = "assoc-run"
	p, err = s.SaveAnalysisImport(*p, p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	changed = *p
	changed.RunID = "unknown"
	if _, err = s.SaveAnalysisImport(changed, p.Revision); err == nil {
		t.Fatal("associated unknown run")
	}
	changed = *p
	changed.CreatedAt = time.Now()
	if _, err = s.SaveAnalysisImport(changed, p.Revision); err == nil {
		t.Fatal("changed creation identity")
	}
	for _, state := range []string{"deleting", "deleted"} {
		p.State = state
		p, err = s.SaveAnalysisImport(*p, p.Revision)
		if err != nil {
			t.Fatal(err)
		}
	}
	p.State = "uploading"
	if _, err = s.SaveAnalysisImport(*p, p.Revision); err == nil {
		t.Fatal("resurrected deleted dataset")
	}
}
