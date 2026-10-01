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

func TestNativeArchiveQueryValidation(t *testing.T) {
	q := AnalyticsQuery{Population: "action", Metric: "response_time", Aggregation: "p95", BucketSeconds: 10, EndSeconds: 60, PropertyName: "kind", PropertyValue: "paid"}
	if err := ValidateNativeArchiveQuery(q); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*AnalyticsQuery){func(q *AnalyticsQuery) { q.Population = "transaction" }, func(q *AnalyticsQuery) { q.EndSeconds = 0 }, func(q *AnalyticsQuery) { q.BucketSeconds = 0 }, func(q *AnalyticsQuery) { q.EndSeconds = 86400; q.BucketSeconds = 1 }, func(q *AnalyticsQuery) { q.PropertyValue = "" }, func(q *AnalyticsQuery) { q.Filters = map[string]string{"sql": "select"} }, func(q *AnalyticsQuery) { q.Metric = "resource_cpu" }} {
		bad := q
		mutate(&bad)
		if ValidateNativeArchiveQuery(bad) == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	d := sampleDashboardDefinition("archive")
	d.Tabs[0].Panels[0].Query = &q
	if err := validateDashboardDefinition(d); err != nil {
		t.Fatal(err)
	}
}

func TestNativeArchiveDefinitionRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/panels.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	d := sampleDashboardDefinition("filtered")
	d.Tabs[0].Panels[0].Query = &AnalyticsQuery{Population: "action", Metric: "observation_count", Aggregation: "count", BucketSeconds: 10, EndSeconds: 60, PropertyName: "kind", PropertyValue: "paid", ComparisonRunID: "baseline", Filters: map[string]string{"action": "Checkout"}}
	if err := store.SaveDashboardDefinition(d); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetDashboardDefinition(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	q := got.Tabs[0].Panels[0].Query
	if q.Population != "action" || q.PropertyName != "kind" || q.PropertyValue != "paid" || q.ComparisonRunID != "baseline" || q.Filters["action"] != "Checkout" || q.EndSeconds != 60 || q.MaxSeries != 1 {
		t.Fatalf("lost saved selection: %+v", q)
	}
}
