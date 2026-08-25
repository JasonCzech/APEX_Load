package storage

import "testing"

func sampleDashboardDefinition(id string) *DashboardDefinition {
	return &DashboardDefinition{
		ID: id, ProjectID: DefaultProjectID, Name: "Shared operations", Visibility: "project",
		Tabs: []DashboardTab{{ID: "overview", Title: "Overview", Panels: []DashboardPanel{{
			ID: "latency", Title: "Latency", Kind: "line", X: 0, Y: 0, W: 12, H: 8,
			Query: &AnalyticsQuery{Metric: "response_time", Aggregation: "p95", Dimensions: []string{"region"}, BucketSeconds: 10, MaxSeries: 20},
		}}}},
	}
}

func TestDashboardDefinitionRevisionAndProjectIsolation(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/dashboard.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	definition := sampleDashboardDefinition("shared-dashboard")
	if err := store.SaveDashboardDefinition(definition); err != nil {
		t.Fatalf("save: %v", err)
	}
	if definition.Revision != 1 {
		t.Fatalf("revision = %d", definition.Revision)
	}
	loaded, err := store.GetDashboardDefinition(definition.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	loaded.Name = "Shared operations v2"
	updated, err := store.UpdateDashboardDefinitionIfRevision(loaded, 1)
	if err != nil || !updated || loaded.Revision != 2 {
		t.Fatalf("update = %v, %v, revision %d", updated, err, loaded.Revision)
	}
	stale := sampleDashboardDefinition(definition.ID)
	stale.ProjectID = definition.ProjectID
	if updated, err := store.UpdateDashboardDefinitionIfRevision(stale, 1); err != nil || updated {
		t.Fatalf("stale update = %v, %v", updated, err)
	}
	items, err := store.ListDashboardDefinitions(definition.ProjectID, 10, 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("list = %#v, %v", items, err)
	}
	if deleted, err := store.DeleteDashboardDefinitionIfRevision(definition.ID, definition.ProjectID, 1); err != nil || deleted {
		t.Fatalf("stale delete = %v, %v", deleted, err)
	}
	if deleted, err := store.DeleteDashboardDefinitionIfRevision(definition.ID, definition.ProjectID, 2); err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}
}

func TestDashboardDefinitionRejectsUnboundedQuery(t *testing.T) {
	definition := sampleDashboardDefinition("bad-dashboard")
	definition.Tabs[0].Panels[0].Query.MaxSeries = MaxDashboardSeries + 1
	if err := validateDashboardDefinition(definition); err == nil {
		t.Fatal("unbounded query was accepted")
	}
}

func TestDashboardDefinitionRejectsUnknownPanelAndFilter(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*DashboardDefinition)
	}{
		{"panel kind", func(definition *DashboardDefinition) { definition.Tabs[0].Panels[0].Kind = "iframe" }},
		{"filter dimension", func(definition *DashboardDefinition) {
			definition.Tabs[0].Panels[0].Query.Filters = map[string]string{"raw_response": "secret"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := sampleDashboardDefinition("bad-dashboard")
			test.mutate(definition)
			if err := validateDashboardDefinition(definition); err == nil {
				t.Fatalf("%s was accepted", test.name)
			}
		})
	}
}
