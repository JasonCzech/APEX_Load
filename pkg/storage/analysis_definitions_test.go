package storage

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestAnalysisDefinitionRevisionsAndIsolation(t *testing.T) {
	s, _, _ := monitorFixture(t)
	in := AnalysisDefinition{ID: "map-1", ProjectID: "monitor-p", Kind: "mapping", Name: "timestamps", Definition: json.RawMessage(`{"version":1}`)}
	first, err := s.SaveAnalysisDefinition(in, 0)
	if err != nil {
		t.Fatal(err)
	}
	in.Name = "changed"
	second, err := s.SaveAnalysisDefinition(in, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveAnalysisDefinition(in, first.Revision); !errors.Is(err, ErrAnalysisRevision) {
		t.Fatalf("stale edit: %v", err)
	}
	old, err := s.GetAnalysisDefinition("monitor-p", in.ID, 1)
	if err != nil || old.Name != "timestamps" {
		t.Fatalf("old revision: %+v %v", old, err)
	}
	if _, err = s.GetAnalysisDefinition("other", in.ID, 0); err == nil {
		t.Fatal("cross-project definition read")
	}
	in.Kind = "comparison"
	if _, err = s.SaveAnalysisDefinition(in, second.Revision); err == nil {
		t.Fatal("changed definition kind")
	}
	in.Kind = "mapping"
	in.Deleted = true
	if _, err = s.SaveAnalysisDefinition(in, second.Revision); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListAnalysisDefinitions("monitor-p", "mapping", 10, 0)
	if err != nil || len(list) != 1 || !list[0].Deleted || list[0].Revision != 3 {
		t.Fatalf("latest: %+v %v", list, err)
	}
	if _, err = s.ListAnalysisDefinitions("monitor-p", "mapping", 101, 0); err == nil {
		t.Fatal("unbounded page")
	}
}
