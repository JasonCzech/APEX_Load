package storage

import (
	"context"
	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
	"strings"
	"testing"
	"time"
)

func TestAnalysisImportLifecycleAndAssociation(t *testing.T) {
	s, _, now := monitorFixture(t)
	in := AnalysisImport{ID: uuid.NewString(), ProjectID: "monitor-p", Name: "dataset", State: "ready", Files: []AnalysisImportFile{{ID: "file", RelativePath: "part.parquet", Size: 20, Uploaded: 20}}}
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
	p.State = "mapped"
	p.Mapping = &analysis.ParquetMapping{Version: 1, Timestamp: analysis.ParquetColumnPath{"time"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: analysis.ParquetColumnPath{"value"}, Name: "value", Kind: analysis.KindGauge, Unit: "ms"}}}
	p, err = s.SaveAnalysisImport(*p, p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	p = publishAnalysisImportTestFixture(t, s, p)
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

// This fixture has no physical writer: publication still exercises the dedicated
// durable claim/seal/CAS path, never the generic metadata bypass.
func publishAnalysisImportTestFixture(t *testing.T, s *SQLiteStore, in *AnalysisImport) *AnalysisImport {
	t.Helper()
	ctx := context.Background()
	claimed, attempt, err := s.ClaimAnalysisImportAttempt(ctx, in.ID, in.ProjectID, in.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	publication := nativeAttemptPublicationFixture(t, claimed, attempt)
	if err = s.ConfirmAnalysisAttemptWritersQuiesced(ctx, in.ID, in.ProjectID, attempt.ID); err != nil {
		t.Fatal(err)
	}
	ready, err := s.PublishAnalysisImportAttempt(ctx, publication)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range ready.Files {
		if file.SHA256 != strings.Repeat("a", 64) {
			t.Fatal("fixture publication not sealed")
		}
	}
	return ready
}
