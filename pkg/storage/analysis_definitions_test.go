package storage

import (
	"encoding/json"
	"errors"
	"strconv"
	"sync"
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

func testPolicyDefault(raw json.RawMessage) (bool, error) {
	var envelope struct {
		ApplyByDefault bool `json:"apply_by_default"`
	}
	err := json.Unmarshal(raw, &envelope)
	return envelope.ApplyByDefault, err
}

// exerciseExclusivePolicyDefaults races default saves of one kind; the check
// under the project lock admits exactly one, and deleting it frees the slot.
func exerciseExclusivePolicyDefaults(t *testing.T, store *SQLiteStore) {
	kind := AnalysisDefinitionKindTelemetryAcceptancePolicy
	defaultBody := json.RawMessage(`{"version":1,"apply_by_default":true,"max_loss_ratio":0}`)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := store.SaveAnalysisPolicyDefinition(AnalysisDefinition{ID: "default-" + strconv.Itoa(i), ProjectID: DefaultProjectID, Kind: kind, Name: "Default " + strconv.Itoa(i), Definition: defaultBody}, 0, testPolicyDefault)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	winners := 0
	for err := range errs {
		switch {
		case err == nil:
			winners++
		case !errors.Is(err, ErrAnalysisPolicyDefaultConflict):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d concurrent defaults committed", winners)
	}
	if _, err := store.SaveAnalysisPolicyDefinition(AnalysisDefinition{ID: "opt-in", ProjectID: DefaultProjectID, Kind: kind, Name: "Opt in", Definition: json.RawMessage(`{"version":1,"max_loss_ratio":0}`)}, 0, testPolicyDefault); err != nil {
		t.Fatalf("a non-default policy was refused: %v", err)
	}
	latest, err := store.ListLatestAnalysisDefinitions(DefaultProjectID, kind)
	if err != nil || len(latest) != 2 {
		t.Fatalf("latest policies %d %v", len(latest), err)
	}
	winner := latest[0]
	if winner.ID == "opt-in" {
		winner = latest[1]
	}
	winner.Deleted = true
	if _, err := store.SaveAnalysisPolicyDefinition(winner, winner.Revision, testPolicyDefault); err != nil {
		t.Fatalf("delete the default: %v", err)
	}
	if _, err := store.SaveAnalysisPolicyDefinition(AnalysisDefinition{ID: "replacement", ProjectID: DefaultProjectID, Kind: kind, Name: "Replacement", Definition: defaultBody}, 0, testPolicyDefault); err != nil {
		t.Fatalf("a deleted default still blocks a new one: %v", err)
	}
}

func TestExclusivePolicyDefaults(t *testing.T) {
	exerciseExclusivePolicyDefaults(t, newAuthoringReceiptTestStore(t))
}
