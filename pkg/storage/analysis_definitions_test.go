package storage

import (
	"encoding/json"
	"errors"
	"strconv"
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

func TestConnectionLinkDefinitionsStayOutOfTheDefinitionBudget(t *testing.T) {
	s, _, _ := monitorFixture(t)
	fill := func(kind string) {
		t.Helper()
		tx, err := s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for i := 0; i < 1000; i++ {
			id := kind + "-" + strconv.Itoa(i)
			body, _ := json.Marshal(AnalysisDefinition{ID: id, ProjectID: "monitor-p", Kind: kind, Name: id, Revision: 1, Definition: json.RawMessage(`{}`)})
			if _, err := tx.Exec(`INSERT INTO analysis_definitions(id,project_id,kind,revision,payload) VALUES(?,?,?,?,?)`, id, "monitor-p", kind, 1, string(body)); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	// A thousand connections' link settings leave room for mappings.
	fill("connection_links")
	mapping := AnalysisDefinition{ID: "map-after-links", ProjectID: "monitor-p", Kind: "mapping", Name: "m", Definition: json.RawMessage(`{}`)}
	if _, err := s.SaveAnalysisDefinition(mapping, 0); err != nil {
		t.Fatalf("mapping blocked by link settings: %v", err)
	}
	// A full mapping budget still admits link settings, and only them.
	fill("mapping")
	links := AnalysisDefinition{ID: "connection-links:elk:new", ProjectID: "monitor-p", Kind: "connection_links", Name: "links", Definition: json.RawMessage(`{}`)}
	if _, err := s.SaveAnalysisDefinition(links, 0); err != nil {
		t.Fatalf("link settings blocked by mappings: %v", err)
	}
	mapping.ID = "map-over-budget"
	if _, err := s.SaveAnalysisDefinition(mapping, 0); err == nil {
		t.Fatal("mapping budget not enforced")
	}
}
