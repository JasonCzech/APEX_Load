package storage

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
)

func identityPersistenceMapping() *analysis.ParquetMapping {
	return &analysis.ParquetMapping{
		Version: 2, Timestamp: analysis.ParquetColumnPath{"time"}, TimestampEncoding: "unix_s", Timezone: "UTC",
		Measures: []analysis.ParquetMeasure{{Value: analysis.ParquetColumnPath{"nested", "value"}, Name: "value", Kind: analysis.KindGauge, Unit: "ms"}},
		Identity: &analysis.ParquetIdentityMapping{Source: analysis.ParquetColumnPath{"agent", "id"}, Host: analysis.ParquetColumnPath{"host"}, Service: analysis.ParquetColumnPath{"service"}, Dimensions: []analysis.ParquetDimensionMapping{{Name: "__proto__", Value: analysis.ParquetColumnPath{"labels", "empty"}}, {Name: "😀/a~\"", Value: analysis.ParquetColumnPath{"labels", "region"}}}},
	}
}

// Both real SQL backends use this round-trip, revision and immutable-history
// oracle. Publication remains the existing dedicated attempt path; this test
// exercises stored mapping metadata without pretending to write Parquet.
func exerciseIdentityPersistence(t *testing.T, s *SQLiteStore, project string) (string, string) {
	t.Helper()
	id := uuid.NewString()
	in, err := s.SaveAnalysisImport(AnalysisImport{ID: id, ProjectID: project, Name: "identity persistence", State: "uploading", Files: []AnalysisImportFile{{ID: uuid.NewString(), RelativePath: "input.parquet", Size: 12, Uploaded: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	in.State, in.Mapping = "mapped", identityPersistenceMapping()
	in, err = s.SaveAnalysisImport(*in, in.Revision)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(identityPersistenceMapping())
	if err != nil {
		t.Fatal(err)
	}
	definition, err := s.SaveAnalysisDefinition(AnalysisDefinition{ID: uuid.NewString(), ProjectID: project, Kind: "mapping", Name: "first", Definition: body}, 0)
	if err != nil {
		t.Fatal(err)
	}
	stale := *definition
	definition.Name = "second"
	definition, err = s.SaveAnalysisDefinition(*definition, definition.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveAnalysisDefinition(stale, stale.Revision); err == nil {
		t.Fatal("stale identity mapping revision accepted")
	}
	assertIdentityPersistence(t, s, project, id, definition.ID)
	read, err := s.GetAnalysisImport(id)
	if err != nil {
		t.Fatal(err)
	}
	read.Mapping.Identity.Dimensions[0].Name = "mutated reader"
	assertIdentityPersistence(t, s, project, id, definition.ID)
	if _, err := s.GetAnalysisDefinition("foreign-project", definition.ID, 1); err == nil {
		t.Fatal("foreign project read identity mapping history")
	}
	return id, definition.ID
}

func assertIdentityPersistence(t *testing.T, s *SQLiteStore, project, id, definitionID string) {
	t.Helper()
	in, err := s.GetAnalysisImport(id)
	if err != nil || in == nil || in.ProjectID != project || in.State != "mapped" || !reflect.DeepEqual(in.Mapping, identityPersistenceMapping()) {
		t.Fatalf("identity import round-trip changed: %+v %v", in, err)
	}
	for revision, name := range map[int64]string{1: "first", 2: "second"} {
		definition, err := s.GetAnalysisDefinition(project, definitionID, revision)
		var mapping analysis.ParquetMapping
		if err != nil || definition == nil || definition.Name != name || definition.Revision != revision || json.Unmarshal(definition.Definition, &mapping) != nil || !reflect.DeepEqual(&mapping, identityPersistenceMapping()) {
			t.Fatalf("identity mapping history changed: %+v %v", definition, err)
		}
	}
}

func TestAnalysisIdentityPersistence(t *testing.T) {
	s, _, _ := monitorFixture(t)
	exerciseIdentityPersistence(t, s, "monitor-p")
}

func TestAnalysisIdentityPersistenceAfterSQLiteReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			s.Close()
		}
	}()
	seedMonitorFixture(t, s)
	importID, definitionID := exerciseIdentityPersistence(t, s, "monitor-p")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = nil
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	assertIdentityPersistence(t, s, "monitor-p", importID, definitionID)
	definition, err := s.GetAnalysisDefinition("monitor-p", definitionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	definition.Name = "stale after reopen"
	if _, err := s.SaveAnalysisDefinition(*definition, definition.Revision); err == nil {
		t.Fatal("reopened store accepted an obsolete identity definition revision")
	}
	assertIdentityPersistence(t, s, "monitor-p", importID, definitionID)
}
